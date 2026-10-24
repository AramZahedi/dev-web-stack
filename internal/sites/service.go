// Package sites implements the site lifecycle: creation, updates, process
// control, deletion, and publishing the proxy route table. Every mutating
// operation keeps the database, hosts block, certificates, backend, and router
// consistent and rolls back partial work when a later step fails.
package sites

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// CreateRequest describes a new site. The domain is derived from Name. PHP
// options are rejected for static and Node sites.
type CreateRequest struct {
	Name, Path, Kind, NodeVersion, Command, DocumentRoot string
	PHPVersion, PHPBuild                                 string
	PHPExtensions                                        map[string]bool
	PHPIni                                               map[string]string
	PHPIniRaw                                            string
	WWW, TLS, ForceHTTPS, SPAFallback, FrontController   bool
	Env                                                  map[string]string
}

// UpdateRequest describes a partial site update. Empty strings and nil
// pointers leave the corresponding field unchanged; a non-nil Env replaces the
// site environment (an empty map clears it). FrontController is accepted for
// PHP sites only.
type UpdateRequest struct {
	Domain          string
	WWW             *bool
	TLS             *bool
	ForceHTTPS      *bool
	SPAFallback     *bool
	FrontController *bool
	Command         string
	DocumentRoot    string
	Env             map[string]string
}

// Hosts synchronizes the helper-managed hosts block.
type Hosts interface {
	SyncHosts(ctx context.Context, domains []string) error
}

// Certs is the certificate-manager boundary used by the service. Every site
// owns one certificate (the site id is the owner), and the dashboard uses
// certs.DashboardOwner.
type Certs interface {
	Ensure(ctx context.Context, owner string, domains []string) (model.CertRecord, error)
	Load(rec model.CertRecord) (*tls.Certificate, error)
	PEM(rec model.CertRecord) (certPEM, keyPEM []byte, err error)
	Delete(ctx context.Context, owner string) error
}

// Vhosts is the privileged helper's external serving boundary.
type Vhosts interface {
	VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error)
	SyncVhosts(ctx context.Context, req helperproto.VHostSyncRequest) error
	SetServingMode(ctx context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error)
	RemoveVhosts(ctx context.Context) error
}

// ProxyControl starts and stops the built-in HTTP/HTTPS proxy.
type ProxyControl interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// Deps is the complete set of service dependencies. Now and NewID are optional
// and default to the wall clock and a random 128-bit hex id.
type Deps struct {
	Store       *store.Store
	Backend     backend.Backend
	Helper      Hosts
	Certs       Certs
	Vhosts      Vhosts
	Proxy       ProxyControl
	Router      *proxy.Router
	Ports       *domain.PortAllocator
	Config      config.Config
	Logs        *logs.Store
	PHP         PHPCommands
	PHPBackend  backend.PHPStager
	PHPManifest *catalog.PHPManifest
	Now         func() time.Time
	NewID       func() string
}

// Service owns site lifecycle operations. Mutating operations and status
// refreshes are serialized so the store, hosts, certificates, backend, and
// routing snapshot never observe two transitions at once.
type Service struct {
	deps Deps
	mu   sync.Mutex

	// phpManifest caches the lazily loaded embedded PHP manifest. It is only
	// accessed while mu is held.
	phpManifest    *catalog.PHPManifest
	phpManifestErr error
}

// NewService returns a service and reserves every port already recorded for a
// site. Reservation failures (for example a duplicate row) are ignored because
// the constructor cannot return an error; allocation still probes each port
// before handing it out.
func NewService(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = defaultNewID
	}
	if deps.Ports == nil {
		deps.Ports = domain.NewPortAllocator()
	}
	if deps.PHP == nil {
		deps.PHP = defaultPHPCommands{}
	}
	// The production local backend is also the generation-aware PHP stager.
	// Reuse it when no separate stager was supplied.
	if deps.PHPBackend == nil {
		if stager, ok := deps.Backend.(backend.PHPStager); ok {
			deps.PHPBackend = stager
		}
	}
	svc := &Service{deps: deps}
	svc.reserveExistingPorts()
	return svc
}

// Create validates and starts a site. The certificate is issued before the
// row is persisted, and any failure after the hosts block changes rolls the
// hosts block, certificate, row, backend process, and port back.
func (s *Service) Create(ctx context.Context, req CreateRequest) (model.Site, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, true); err != nil {
		return model.Site{}, err
	}
	site, err := s.newSite(ctx, req)
	if err != nil {
		return model.Site{}, err
	}

	oldHosts, err := s.hostsFor(ctx, "", nil)
	if err != nil {
		return model.Site{}, err
	}
	newHosts, err := s.hostsFor(ctx, "", siteDomains(site))
	if err != nil {
		return model.Site{}, err
	}

	var port int
	switch {
	case site.Kind == model.SitePHP:
		// PHP sites use only a FastCGI port, allocated before any external
		// side effects.
	case site.Kind == model.SiteNode || !s.externalServing():
		port, err = s.deps.Ports.Allocate(domain.PortHTTP)
		if err != nil {
			return model.Site{}, fmt.Errorf("sites: allocate HTTP port for %q: %w", site.Name, err)
		}
		site.HTTPPort = port
	}

	fail := func(cause error, certIssued bool) (model.Site, error) {
		return model.Site{}, s.rollbackCreate(ctx, site, oldHosts, certIssued, port, cause)
	}

	if site.Kind == model.SiteNode {
		if _, err := s.deps.Backend.EnsureRuntime(ctx, "node", site.NodeVersion); err != nil {
			return fail(fmt.Errorf("sites: ensure node %s for %q: %w", site.NodeVersion, site.Name, err), false)
		}
	}

	var phpBuild model.RuntimeBuild
	if site.Kind == model.SitePHP {
		if err := s.checkPHPBackend(); err != nil {
			return model.Site{}, err
		}
		fastCGIPort, err := s.deps.Ports.Allocate(domain.PortFastCGI)
		if err != nil {
			return model.Site{}, fmt.Errorf("sites: allocate FastCGI port for %q: %w", site.Name, err)
		}
		site.FastCGIPort = fastCGIPort

		phpBuild, err = s.phpBuildForSiteLocked(ctx, site)
		if err != nil {
			return fail(err, false)
		}
		files, err := s.composePHPFilesLocked(ctx, site, phpBuild)
		if err != nil {
			return fail(err, false)
		}
		site.PHPConfigHash = files.Hash
		generationDir, err := s.writePHPFilesLocked(site.ID, files)
		if err != nil {
			return fail(err, false)
		}
		if err := s.validatePHPFilesLocked(ctx, site, phpBuild, generationDir); err != nil {
			return fail(err, false)
		}
	}

	if err := s.syncHosts(ctx, newHosts); err != nil {
		return fail(fmt.Errorf("sites: sync hosts for %q: %w", site.Name, err), false)
	}

	if _, err := s.deps.Certs.Ensure(ctx, site.ID, siteDomains(site)); err != nil {
		return fail(fmt.Errorf("sites: issue certificate for %q: %w", site.Name, err), false)
	}

	site.Status = model.StatusStarting
	site.DesiredState = model.DesiredRunning
	site.CreatedAt = s.currentTime()
	site.UpdatedAt = site.CreatedAt
	if err := s.deps.Store.CreateSite(ctx, site); err != nil {
		return fail(fmt.Errorf("sites: persist %q: %w", site.Name, err), true)
	}

	if site.Kind == model.SitePHP {
		if err := s.saveAppliedPHPConfigLocked(ctx, site); err != nil {
			return fail(fmt.Errorf("sites: save PHP configuration for %q: %w", site.Name, err), true)
		}
		spec, err := s.phpSiteSpecFromBuildLocked(site, phpBuild)
		if err != nil {
			return fail(err, true)
		}
		if _, err := s.deps.PHPBackend.PreparePHP(ctx, site.ID, phpDefaultGeneration, spec); err != nil {
			return fail(fmt.Errorf("sites: prepare %q: %w", site.Name, err), true)
		}
		if _, err := s.deps.PHPBackend.StartPHP(ctx, site.ID, phpDefaultGeneration); err != nil {
			return fail(fmt.Errorf("sites: start %q: %w", site.Name, err), true)
		}
		site.Status = model.StatusRunning
		site.UpdatedAt = s.currentTime()
		if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
			return fail(fmt.Errorf("sites: record running state for %q: %w", site.Name, err), true)
		}
		s.commitPHPFilesBestEffortLocked(ctx, site)
		if err := s.rebuildRoutesLocked(ctx); err != nil {
			return fail(fmt.Errorf("sites: publish routes for %q: %w", site.Name, err), true)
		}
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			return fail(fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err), true)
		}
		s.emitStatus(ctx, site, "", model.StatusRunning, "")
		return site, nil
	}

	// External static sites are served directly by nginx/Apache: no backend
	// process and no proxy port.
	if s.externalServing() && site.Kind == model.SiteStatic {
		site.Status = model.StatusRunning
		site.UpdatedAt = s.currentTime()
		if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
			return fail(fmt.Errorf("sites: record running state for %q: %w", site.Name, err), true)
		}
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			return fail(fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err), true)
		}
		if err := s.rebuildRoutesLocked(ctx); err != nil {
			return fail(fmt.Errorf("sites: publish routes for %q: %w", site.Name, err), true)
		}
		s.emitStatus(ctx, site, "", model.StatusRunning, "")
		return site, nil
	}

	prepared, err := s.deps.Backend.Prepare(ctx, backendSpec(site))
	if err != nil {
		return fail(fmt.Errorf("sites: prepare %q: %w", site.Name, err), true)
	}
	if _, err := s.deps.Backend.Start(ctx, site.ID); err != nil {
		return fail(fmt.Errorf("sites: start %q: %w", site.Name, err), true)
	}

	site.Status = model.StatusRunning
	site.FastCGIPort = prepared.FastCGIPort
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return fail(fmt.Errorf("sites: record running state for %q: %w", site.Name, err), true)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return fail(fmt.Errorf("sites: publish routes for %q: %w", site.Name, err), true)
	}
	if err := s.syncVhostsLocked(ctx, ""); err != nil {
		return fail(fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err), true)
	}

	s.emitStatus(ctx, site, "", model.StatusRunning, "")
	return site, nil
}

// Update applies a partial update. A domain or www-alias change unions the old
// and new domains in the hosts block, issues the replacement certificate, and
// removes the old entries only after the new state is live. Any failure
// restores the old row, certificate, hosts, and routes.
func (s *Service) Update(ctx context.Context, ref string, req UpdateRequest) (model.Site, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(false, true); err != nil {
		return model.Site{}, err
	}
	current, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return model.Site{}, err
	}
	updated, err := s.applyUpdate(ctx, current, req)
	if err != nil {
		return model.Site{}, err
	}

	oldDomains := siteDomains(current)
	newDomains := siteDomains(updated)
	domainsChanged := !sameDomainSet(oldDomains, newDomains)

	oldHosts, err := s.hostsFor(ctx, current.ID, oldDomains)
	if err != nil {
		return model.Site{}, err
	}
	phpUpdate, err := s.preparePHPUpdateLocked(ctx, current, &updated)
	if err != nil {
		return model.Site{}, err
	}
	fail := func(certChanged bool, cause error) (model.Site, error) {
		phpUpdate.cleanup()
		return model.Site{}, s.rollbackUpdate(ctx, current, oldHosts, oldDomains, certChanged, cause)
	}

	if domainsChanged {
		union := append(append([]string(nil), oldDomains...), newDomains...)
		unionHosts, err := s.hostsFor(ctx, current.ID, union)
		if err != nil {
			phpUpdate.cleanup()
			return model.Site{}, err
		}
		if err := s.syncHosts(ctx, unionHosts); err != nil {
			phpUpdate.cleanup()
			cause := fmt.Errorf("sites: sync hosts for %q: %w", current.Name, err)
			return model.Site{}, errors.Join(cause, s.syncHosts(ctx, oldHosts))
		}
		if _, err := s.deps.Certs.Ensure(ctx, current.ID, newDomains); err != nil {
			cause := fmt.Errorf("sites: issue certificate for %q: %w", current.Name, err)
			return fail(true, cause)
		}
	}

	updated.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, updated); err != nil {
		cause := fmt.Errorf("sites: update %q: %w", current.Name, err)
		return fail(domainsChanged, cause)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		cause := fmt.Errorf("sites: publish routes for %q: %w", current.Name, err)
		return fail(domainsChanged, cause)
	}
	if err := s.syncVhostsLocked(ctx, ""); err != nil {
		cause := fmt.Errorf("sites: publish vhost for %q: %w", current.Name, err)
		return fail(domainsChanged, cause)
	}

	if domainsChanged {
		finalHosts, err := s.hostsFor(ctx, current.ID, newDomains)
		if err != nil {
			cause := fmt.Errorf("sites: rebuild hosts for %q: %w", current.Name, err)
			return fail(true, cause)
		}
		if err := s.syncHosts(ctx, finalHosts); err != nil {
			cause := fmt.Errorf("sites: remove old hosts for %q: %w", current.Name, err)
			return fail(true, cause)
		}
	}

	if phpUpdate.applied {
		if updated.PHPConfigHash != "" {
			if err := s.saveAppliedPHPConfigLocked(ctx, updated); err != nil {
				cause := fmt.Errorf("sites: save PHP configuration for %q: %w", current.Name, err)
				return fail(domainsChanged, cause)
			}
		}
		s.commitPHPFilesBestEffortLocked(ctx, updated)
	}

	s.emitEvent(ctx, current.ID, "site.updated", fmt.Sprintf("site %s updated", updated.Name))
	return updated, nil
}

// Get returns one site after refreshing its observed status from the backend.
func (s *Service) Get(ctx context.Context, ref string) (model.Site, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, false); err != nil {
		return model.Site{}, err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return model.Site{}, err
	}
	return s.refreshLocked(ctx, site)
}

// List returns every site after refreshing observed statuses.
func (s *Service) List(ctx context.Context) ([]model.Site, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, false); err != nil {
		return nil, err
	}
	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("sites: list: %w", err)
	}
	sites := make([]model.Site, 0, len(stored))
	for _, site := range stored {
		refreshed, err := s.refreshLocked(ctx, site)
		if err != nil {
			return nil, err
		}
		sites = append(sites, refreshed)
	}
	return sites, nil
}

// Start prepares and starts the site, then publishes routes. It is idempotent:
// preparing and starting an already running site succeeds.
func (s *Service) Start(ctx context.Context, ref string) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, false); err != nil {
		return err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return err
	}
	started, err := s.startLocked(ctx, site)
	if err != nil {
		return err
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return fmt.Errorf("sites: publish routes for %q: %w", site.Name, err)
	}
	if s.externalServing() && started.Kind != model.SiteStatic {
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			cause := fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err)
			if started.DesiredState != site.DesiredState || started.Status != site.Status {
				return errors.Join(cause, s.restoreSiteStateLocked(ctx, site, started))
			}
			return cause
		}
	}
	return nil
}

// Stop terminates the backend process, records the stopped state, and
// republishes routes.
func (s *Service) Stop(ctx context.Context, ref string) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, false); err != nil {
		return err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return err
	}
	stopped, err := s.stopLocked(ctx, site)
	if err != nil {
		return err
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return fmt.Errorf("sites: publish routes for %q: %w", site.Name, err)
	}
	if s.externalServing() && stopped.Kind != model.SiteStatic {
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			cause := fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err)
			if stopped.DesiredState != site.DesiredState || stopped.Status != site.Status {
				return errors.Join(cause, s.restoreSiteStateLocked(ctx, site, stopped))
			}
			return cause
		}
	}
	return nil
}

// Restart stops and starts a site. The route snapshot is rebuilt once after
// the new process is running.
func (s *Service) Restart(ctx context.Context, ref string) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, false); err != nil {
		return err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return err
	}
	previous := site
	site, err = s.stopLocked(ctx, site)
	if err != nil {
		return err
	}
	site, err = s.startLocked(ctx, site)
	if err != nil {
		return err
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return fmt.Errorf("sites: publish routes for %q: %w", site.Name, err)
	}
	if s.externalServing() && site.Kind != model.SiteStatic {
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			cause := fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err)
			return errors.Join(cause, s.restoreSiteStateLocked(ctx, previous, site))
		}
	}
	return nil
}

// Delete stops the site, removes its hosts entries, certificate, and row, and
// releases its ports. If the hosts sync fails the stopped row is kept so a
// retry can finish the deletion.
func (s *Service) Delete(ctx context.Context, ref string) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(true, true); err != nil {
		return err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return err
	}
	if s.externalServing() {
		return s.deleteExternalLocked(ctx, site)
	}
	site, err = s.stopLocked(ctx, site)
	if err != nil {
		return err
	}

	remaining, err := s.hostsFor(ctx, site.ID, nil)
	if err != nil {
		return err
	}
	if err := s.syncHosts(ctx, remaining); err != nil {
		return fmt.Errorf("sites: sync hosts before deleting %q: %w", site.Name, err)
	}

	if err := s.deps.Certs.Delete(ctx, site.ID); err != nil {
		return fmt.Errorf("sites: delete certificate for %q: %w", site.Name, err)
	}
	if err := s.deps.Store.DeleteSite(ctx, site.ID); err != nil {
		return fmt.Errorf("sites: delete %q: %w", site.Name, err)
	}
	s.emitEvent(ctx, site.ID, "site.deleted", fmt.Sprintf("site %s deleted", site.Name))

	s.deps.Ports.Release(site.HTTPPort)
	if site.FastCGIPort != 0 {
		s.deps.Ports.Release(site.FastCGIPort)
	}
	var removeErr error
	if site.Kind == model.SitePHP {
		removeErr = s.removePHPConfigLocked(site.ID)
	}

	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return fmt.Errorf("sites: publish routes after deleting %q: %w", site.Name, err)
	}
	if removeErr != nil {
		return fmt.Errorf("sites: remove PHP config for %q: %w", site.Name, removeErr)
	}
	return nil
}

// newSite validates a create request and builds the row that will be
// persisted. It performs no external side effects.
func (s *Service) newSite(ctx context.Context, req CreateRequest) (model.Site, error) {
	name, err := domain.NormalizeName(strings.TrimSpace(req.Name))
	if err != nil {
		return model.Site{}, fmt.Errorf("sites: %w", err)
	}
	project, err := domain.ValidateProjectPath(req.Path)
	if err != nil {
		return model.Site{}, fmt.Errorf("sites: %w", err)
	}

	kind := model.SiteKind(strings.ToLower(strings.TrimSpace(req.Kind)))
	switch kind {
	case model.SiteStatic, model.SiteNode, model.SitePHP:
	default:
		return model.Site{}, fmt.Errorf("sites: unsupported site kind %q", req.Kind)
	}
	if kind != model.SitePHP {
		if err := rejectPHPOnlyCreateFields(req); err != nil {
			return model.Site{}, err
		}
	}

	siteDomain := domain.DomainFor(name)
	if err := domain.ValidateSiteDomain(siteDomain); err != nil {
		return model.Site{}, fmt.Errorf("sites: invalid domain %q: %w", siteDomain, err)
	}

	if _, err := s.deps.Store.GetSiteByName(ctx, name); err == nil {
		return model.Site{}, fmt.Errorf("sites: site name %q already exists", name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return model.Site{}, fmt.Errorf("sites: check name %q: %w", name, err)
	}

	site := model.Site{
		Name:         name,
		Domain:       siteDomain,
		WWW:          req.WWW,
		Backend:      model.BackendLocal,
		HostPath:     project,
		BackendPath:  project,
		Kind:         kind,
		NodeVersion:  strings.TrimSpace(req.NodeVersion),
		Command:      strings.TrimSpace(req.Command),
		Env:          cloneEnv(req.Env),
		DocumentRoot: strings.TrimSpace(req.DocumentRoot),
		SPAFallback:  req.SPAFallback,
		TLS:          req.TLS,
		ForceHTTPS:   req.ForceHTTPS,
		DesiredState: model.DesiredRunning,
	}

	switch kind {
	case model.SiteStatic:
		site.DocumentRoot, err = validateDocumentRoot(project, req.DocumentRoot)
		if err != nil {
			return model.Site{}, err
		}
	case model.SiteNode:
		if site.NodeVersion == "" {
			return model.Site{}, errors.New("sites: node site requires a version")
		}
		if site.Command == "" {
			return model.Site{}, errors.New("sites: node site requires a command")
		}
	case model.SitePHP:
		site.PHPVersion = strings.TrimSpace(req.PHPVersion)
		site.PHPBuild = strings.TrimSpace(req.PHPBuild)
		if site.PHPVersion == "" {
			return model.Site{}, errors.New("sites: php site requires a version")
		}
		if !supportedPHPLine(site.PHPVersion) {
			return model.Site{}, fmt.Errorf("sites: unsupported PHP version %q (want 8.2, 8.3, 8.4, or 8.5)", site.PHPVersion)
		}
		site.PHPExtensions = cloneBoolMap(req.PHPExtensions)
		site.PHPIni = cloneStringMap(req.PHPIni)
		site.PHPIniRaw = req.PHPIniRaw
		site.FrontController = req.FrontController
		site.DocumentRoot, err = validateDocumentRoot(project, req.DocumentRoot)
		if err != nil {
			return model.Site{}, err
		}
		if err := phpini.ValidateOverrides(site.PHPIni); err != nil {
			return model.Site{}, fmt.Errorf("sites: php.ini overrides: %w", err)
		}
		if err := phpini.ValidateRaw(site.PHPIniRaw); err != nil {
			return model.Site{}, fmt.Errorf("sites: raw php.ini: %w", err)
		}
		manifest, err := s.phpCatalog()
		if err != nil {
			return model.Site{}, err
		}
		if _, err := manifest.Resolve(site.PHPVersion, site.PHPExtensions); err != nil {
			return model.Site{}, fmt.Errorf("sites: PHP extensions: %w", err)
		}
		build, err := s.resolvePHPBuildLocked(ctx, site.PHPVersion, site.PHPBuild)
		if err != nil {
			return model.Site{}, err
		}
		site.PHPBuild = build.ID
	}

	if err := s.ensureDomainsAvailable(ctx, "", siteDomains(site)); err != nil {
		return model.Site{}, err
	}

	id := strings.TrimSpace(s.deps.NewID())
	if id == "" {
		return model.Site{}, errors.New("sites: generated site id is empty")
	}
	site.ID = id
	return site, nil
}

// rejectPHPOnlyCreateFields keeps PHP options from silently configuring a
// static or Node site.
func rejectPHPOnlyCreateFields(req CreateRequest) error {
	if strings.TrimSpace(req.PHPVersion) != "" ||
		strings.TrimSpace(req.PHPBuild) != "" ||
		len(req.PHPExtensions) != 0 ||
		len(req.PHPIni) != 0 ||
		req.PHPIniRaw != "" {
		return errors.New("sites: PHP options require a php site")
	}
	if req.FrontController {
		return errors.New("sites: front controller requires a php site")
	}
	return nil
}

// applyUpdate validates and applies the partial update without touching the
// store or external systems.
func (s *Service) applyUpdate(ctx context.Context, current model.Site, req UpdateRequest) (model.Site, error) {
	updated := current

	if value := strings.TrimSpace(req.Domain); value != "" {
		value = strings.ToLower(value)
		if value != current.Domain {
			if err := domain.ValidateSiteDomain(value); err != nil {
				return model.Site{}, fmt.Errorf("sites: invalid domain %q: %w", req.Domain, err)
			}
			updated.Domain = value
		}
	}
	if req.WWW != nil {
		updated.WWW = *req.WWW
	}
	if req.TLS != nil {
		updated.TLS = *req.TLS
	}
	if req.ForceHTTPS != nil {
		updated.ForceHTTPS = *req.ForceHTTPS
	}
	if req.SPAFallback != nil {
		updated.SPAFallback = *req.SPAFallback
	}
	if strings.TrimSpace(req.Command) != "" {
		updated.Command = strings.TrimSpace(req.Command)
	}
	if req.FrontController != nil {
		if updated.Kind != model.SitePHP {
			return model.Site{}, errors.New("sites: front controller requires a php site")
		}
		updated.FrontController = *req.FrontController
	}
	if strings.TrimSpace(req.DocumentRoot) != "" {
		switch updated.Kind {
		case model.SiteStatic, model.SitePHP:
			root, err := validateDocumentRoot(updated.HostPath, req.DocumentRoot)
			if err != nil {
				return model.Site{}, err
			}
			updated.DocumentRoot = root
		default:
			updated.DocumentRoot = strings.TrimSpace(req.DocumentRoot)
		}
	}
	if req.Env != nil {
		updated.Env = cloneEnv(req.Env)
	}

	if updated.Kind == model.SiteNode && strings.TrimSpace(updated.Command) == "" {
		return model.Site{}, errors.New("sites: node site requires a command")
	}

	if oldDomains, newDomains := siteDomains(current), siteDomains(updated); !sameDomainSet(oldDomains, newDomains) {
		if err := s.ensureDomainsAvailable(ctx, current.ID, newDomains); err != nil {
			return model.Site{}, err
		}
	}
	return updated, nil
}

// startLocked allocates a port if the row has none, prepares and starts the
// backend, and persists the running state. The caller must hold s.mu.
func (s *Service) startLocked(ctx context.Context, site model.Site) (model.Site, error) {
	if site.Kind == model.SitePHP {
		return s.startPHPLocked(ctx, site)
	}
	if s.externalServing() && site.Kind == model.SiteStatic {
		return s.startExternalStaticLocked(ctx, site)
	}

	var allocated int
	if site.HTTPPort == 0 {
		port, err := s.deps.Ports.Allocate(domain.PortHTTP)
		if err != nil {
			return site, fmt.Errorf("sites: allocate HTTP port for %q: %w", site.Name, err)
		}
		site.HTTPPort = port
		allocated = port
	}
	releaseAllocated := func() {
		if allocated != 0 {
			s.deps.Ports.Release(allocated)
		}
	}

	prepared, err := s.deps.Backend.Prepare(ctx, backendSpec(site))
	if err != nil {
		releaseAllocated()
		return site, fmt.Errorf("sites: prepare %q: %w", site.Name, err)
	}

	wasRunning := site.Status == model.StatusRunning
	if _, err := s.deps.Backend.Start(ctx, site.ID); err != nil {
		releaseAllocated()
		return site, fmt.Errorf("sites: start %q: %w", site.Name, err)
	}

	oldStatus := site.Status
	site.DesiredState = model.DesiredRunning
	site.Status = model.StatusRunning
	site.FastCGIPort = prepared.FastCGIPort
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		releaseAllocated()
		if !wasRunning {
			_ = s.deps.Backend.Stop(ctx, site.ID, false)
		}
		return site, fmt.Errorf("sites: record running state for %q: %w", site.Name, err)
	}

	s.emitStatus(ctx, site, oldStatus, model.StatusRunning, "")
	return site, nil
}

// stopLocked stops the backend and persists the stopped state. It is
// idempotent. The caller must hold s.mu.
func (s *Service) stopLocked(ctx context.Context, site model.Site) (model.Site, error) {
	if site.Kind == model.SitePHP {
		return s.stopPHPLocked(ctx, site)
	}
	if s.externalServing() && site.Kind == model.SiteStatic {
		return s.stopExternalStaticLocked(ctx, site)
	}

	if err := s.deps.Backend.Stop(ctx, site.ID, true); err != nil {
		return site, fmt.Errorf("sites: stop %q: %w", site.Name, err)
	}
	oldStatus := site.Status
	site.DesiredState = model.DesiredStopped
	site.Status = model.StatusStopped
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return site, fmt.Errorf("sites: record stopped state for %q: %w", site.Name, err)
	}
	s.emitStatus(ctx, site, oldStatus, model.StatusStopped, "")
	return site, nil
}

// startExternalStaticLocked validates the document root, records the desired
// running state, and syncs the vhost. There is no backend process in external
// mode. A sync failure restores the previous row.
func (s *Service) startExternalStaticLocked(ctx context.Context, site model.Site) (model.Site, error) {
	if err := staticRootError(site); err != nil {
		return site, fmt.Errorf("sites: document root for %q: %w", site.Name, err)
	}
	previous := site
	oldStatus := site.Status
	site.DesiredState = model.DesiredRunning
	site.Status = model.StatusRunning
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return previous, fmt.Errorf("sites: record running state for %q: %w", site.Name, err)
	}
	if err := s.syncVhostsLocked(ctx, ""); err != nil {
		if restoreErr := s.deps.Store.UpdateSite(ctx, previous); restoreErr != nil {
			return previous, errors.Join(err, fmt.Errorf("rollback: restore %q: %w", site.Name, restoreErr))
		}
		return previous, fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err)
	}
	s.emitStatus(ctx, site, oldStatus, model.StatusRunning, "")
	return site, nil
}

// stopExternalStaticLocked records the stopped state and syncs the vhost. A
// sync failure restores the previous row.
func (s *Service) stopExternalStaticLocked(ctx context.Context, site model.Site) (model.Site, error) {
	previous := site
	oldStatus := site.Status
	site.DesiredState = model.DesiredStopped
	site.Status = model.StatusStopped
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return previous, fmt.Errorf("sites: record stopped state for %q: %w", site.Name, err)
	}
	if err := s.syncVhostsLocked(ctx, ""); err != nil {
		if restoreErr := s.deps.Store.UpdateSite(ctx, previous); restoreErr != nil {
			return previous, errors.Join(err, fmt.Errorf("rollback: restore %q: %w", site.Name, restoreErr))
		}
		return previous, fmt.Errorf("sites: publish vhost for %q: %w", site.Name, err)
	}
	s.emitStatus(ctx, site, oldStatus, model.StatusStopped, "")
	return site, nil
}

// restoreSiteStateLocked reverses a start/stop that was followed by a failed
// external vhost sync. The row is restored and the backend converged to the
// previous desired state. Ports allocated only by the failed transition are
// released.
func (s *Service) restoreSiteStateLocked(ctx context.Context, previous, current model.Site) error {
	var errs []error
	if current.Kind == model.SitePHP {
		cleanupCtx, cancel := phpCleanupContext(ctx)
		err := s.stopPHPProcessLocked(cleanupCtx, current.ID)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("rollback: stop %q: %w", current.Name, err))
		}
	} else if s.deps.Backend != nil {
		if err := s.deps.Backend.Stop(ctx, current.ID, true); err != nil {
			errs = append(errs, fmt.Errorf("rollback: stop %q: %w", current.Name, err))
		}
	}
	if previous.DesiredState == model.DesiredRunning {
		switch {
		case previous.Kind == model.SitePHP:
			if s.deps.PHPBackend == nil {
				errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", previous.Name, errors.New("PHP backend is not configured")))
				break
			}
			spec, err := s.phpSiteSpecLocked(ctx, previous)
			if err != nil {
				errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", previous.Name, err))
				break
			}
			if _, err := s.deps.PHPBackend.PreparePHP(ctx, previous.ID, phpDefaultGeneration, spec); err != nil {
				errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", previous.Name, err))
			} else if _, err := s.deps.PHPBackend.StartPHP(ctx, previous.ID, phpDefaultGeneration); err != nil {
				errs = append(errs, fmt.Errorf("rollback: start %q: %w", previous.Name, err))
			}
		case s.deps.Backend != nil:
			if _, err := s.deps.Backend.Prepare(ctx, backendSpec(previous)); err != nil {
				errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", previous.Name, err))
			} else if _, err := s.deps.Backend.Start(ctx, previous.ID); err != nil {
				errs = append(errs, fmt.Errorf("rollback: start %q: %w", previous.Name, err))
			}
		}
	}
	if s.deps.Store != nil {
		if err := s.deps.Store.UpdateSite(ctx, previous); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restore row %q: %w", previous.Name, err))
		}
	}
	if previous.HTTPPort == 0 && current.HTTPPort != 0 && s.deps.Ports != nil {
		s.deps.Ports.Release(current.HTTPPort)
	}
	if previous.FastCGIPort == 0 && current.FastCGIPort != 0 && s.deps.Ports != nil {
		s.deps.Ports.Release(current.FastCGIPort)
	}
	return errors.Join(errs...)
}

// refreshLocked asks the backend for the observed status and persists a
// transition. External static status is derived from desired state and document
// root readability without touching a backend. The caller must hold s.mu.
func (s *Service) refreshLocked(ctx context.Context, site model.Site) (model.Site, error) {
	if s.externalServing() && site.Kind == model.SiteStatic {
		return s.refreshExternalStaticLocked(ctx, site)
	}
	status, err := s.deps.Backend.Status(ctx, site.ID)
	if err != nil {
		return site, fmt.Errorf("sites: status for %q: %w", site.Name, err)
	}
	if status.State == "" || status.State == site.Status {
		return site, nil
	}
	oldStatus := site.Status
	site.Status = status.State
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return site, fmt.Errorf("sites: record status for %q: %w", site.Name, err)
	}
	s.emitStatus(ctx, site, oldStatus, status.State, status.Message)
	return site, nil
}

// refreshExternalStaticLocked derives status from desired state and document
// root readability, persisting a transition when practical.
func (s *Service) refreshExternalStaticLocked(ctx context.Context, site model.Site) (model.Site, error) {
	status := model.StatusStopped
	if site.DesiredState == model.DesiredRunning {
		status = model.StatusRunning
		if err := staticRootError(site); err != nil {
			status = model.StatusError
		}
	}
	if status == site.Status {
		return site, nil
	}
	oldStatus := site.Status
	site.Status = status
	site.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		return site, fmt.Errorf("sites: record status for %q: %w", site.Name, err)
	}
	s.emitStatus(ctx, site, oldStatus, status, "")
	return site, nil
}

// rollbackCreate reverses every side effect of a failed creation and returns
// the original error joined with any rollback failures.
func (s *Service) rollbackCreate(ctx context.Context, site model.Site, oldHosts []string, certIssued bool, port int, cause error) error {
	errs := []error{cause}
	phpStopFailed := false
	if s.deps.Backend != nil && s.siteNeedsBackend(site) {
		if site.Kind == model.SitePHP {
			if s.deps.PHPBackend != nil {
				if err := s.stopPHPGenerationForCleanup(ctx, site.ID, phpDefaultGeneration); err != nil {
					phpStopFailed = true
					errs = append(errs, fmt.Errorf("rollback: stop %q: %w", site.Name, err))
				}
			}
		} else if err := s.deps.Backend.Stop(ctx, site.ID, false); err != nil {
			errs = append(errs, fmt.Errorf("rollback: stop %q: %w", site.Name, err))
		}
	}
	if certIssued && s.deps.Certs != nil {
		if err := s.deps.Certs.Delete(ctx, site.ID); err != nil {
			errs = append(errs, fmt.Errorf("rollback: delete certificate for %q: %w", site.Name, err))
		}
	}
	if s.deps.Helper != nil {
		if err := s.syncHosts(ctx, oldHosts); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restore hosts: %w", err))
		}
	}
	if s.deps.Store != nil {
		if err := s.deps.Store.DeleteSite(ctx, site.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			errs = append(errs, fmt.Errorf("rollback: delete row %q: %w", site.Name, err))
		}
	}
	if port != 0 && s.deps.Ports != nil {
		s.deps.Ports.Release(port)
	}
	if site.Kind == model.SitePHP {
		// A failed stop may leave a master listening on the FastCGI port, so
		// retain its generation directory and port reservation for diagnosis and
		// a later cleanup instead of advertising a free port.
		if phpStopFailed {
			errs = append(errs, fmt.Errorf(
				"rollback: retained PHP generation and FastCGI port %d for %q after a failed stop",
				site.FastCGIPort, site.Name))
		} else {
			if err := s.removePHPConfigLocked(site.ID); err != nil {
				errs = append(errs, fmt.Errorf("rollback: remove PHP config for %q: %w", site.Name, err))
			}
			if site.FastCGIPort != 0 && s.deps.Ports != nil {
				s.deps.Ports.Release(site.FastCGIPort)
			}
		}
	}
	if s.externalServing() {
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restore vhosts: %w", err))
		}
	}
	return errors.Join(errs...)
}

// rollbackUpdate restores the old row, certificate, hosts block, and routes.
func (s *Service) rollbackUpdate(ctx context.Context, old model.Site, oldHosts, oldDomains []string, certChanged bool, cause error) error {
	errs := []error{cause}
	// A failed external sync may already have copied new certificate material
	// for the same owner, so restore the old leaf even when the domain set did
	// not change.
	if certChanged || s.externalServing() {
		if _, err := s.deps.Certs.Ensure(ctx, old.ID, oldDomains); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restore certificate for %q: %w", old.Name, err))
		}
	}
	if err := s.deps.Store.UpdateSite(ctx, old); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore row %q: %w", old.Name, err))
	}
	if err := s.syncHosts(ctx, oldHosts); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore hosts for %q: %w", old.Name, err))
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore routes for %q: %w", old.Name, err))
	}
	if s.externalServing() {
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restore vhosts for %q: %w", old.Name, err))
		}
	}
	return errors.Join(errs...)
}

// resolveLocked resolves a site reference by id first and then by name.
func (s *Service) resolveLocked(ctx context.Context, ref string) (model.Site, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return model.Site{}, errors.New("sites: site reference is empty")
	}
	site, err := s.deps.Store.GetSite(ctx, ref)
	if err == nil {
		return site, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.Site{}, fmt.Errorf("sites: resolve %q: %w", ref, err)
	}
	site, err = s.deps.Store.GetSiteByName(ctx, strings.ToLower(ref))
	if err == nil {
		return site, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.Site{}, fmt.Errorf("sites: resolve %q: %w", ref, err)
	}
	return model.Site{}, fmt.Errorf("sites: site %q: %w", ref, store.ErrNotFound)
}

// hostsFor returns the complete hosts set: every domain of every other site,
// the optional extra domains, and the dashboard's reserved host. The result is
// sorted for deterministic helper updates.
func (s *Service) hostsFor(ctx context.Context, excludeID string, extra []string) ([]string, error) {
	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return nil, fmt.Errorf("sites: list sites for hosts: %w", err)
	}
	set := map[string]struct{}{dashboardHost: {}}
	for _, site := range stored {
		if site.ID == excludeID {
			continue
		}
		for _, name := range siteDomains(site) {
			set[name] = struct{}{}
		}
	}
	for _, name := range extra {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			set[name] = struct{}{}
		}
	}
	domains := make([]string, 0, len(set))
	for name := range set {
		domains = append(domains, name)
	}
	sort.Strings(domains)
	return domains, nil
}

// ensureDomainsAvailable rejects domains (including www aliases) already owned
// by another site.
func (s *Service) ensureDomainsAvailable(ctx context.Context, excludeID string, domains []string) error {
	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("sites: list sites for domain check: %w", err)
	}
	used := make(map[string]string, len(stored)*2)
	for _, site := range stored {
		if site.ID == excludeID {
			continue
		}
		for _, name := range siteDomains(site) {
			used[name] = site.Name
		}
	}
	for _, name := range domains {
		if owner, ok := used[name]; ok {
			return fmt.Errorf("sites: domain %q is already used by site %q", name, owner)
		}
	}
	return nil
}

func (s *Service) syncHosts(ctx context.Context, domains []string) error {
	if s.deps.Helper == nil {
		return errors.New("sites: hosts manager is not configured")
	}
	return s.deps.Helper.SyncHosts(ctx, domains)
}

// reserveExistingPorts restores the allocator's view of ports recorded for
// persisted sites. Failures are ignored because NewService cannot return an
// error; allocation still probes each candidate port before handing it out.
func (s *Service) reserveExistingPorts() {
	if s.deps.Store == nil || s.deps.Ports == nil {
		return
	}
	stored, err := s.deps.Store.ListSites(context.Background())
	if err != nil {
		return
	}
	for _, site := range stored {
		for _, port := range []int{site.HTTPPort, site.FastCGIPort} {
			if port > 0 {
				_ = s.deps.Ports.Reserve(port)
			}
		}
	}
}

// checkDeps reports missing required dependencies in one actionable error.
func (s *Service) checkDeps(needBackend, needHosts bool) error {
	var missing []string
	if s.deps.Store == nil {
		missing = append(missing, "store")
	}
	if s.deps.Certs == nil {
		missing = append(missing, "certificate manager")
	}
	if s.deps.Router == nil {
		missing = append(missing, "router")
	}
	if s.deps.Ports == nil {
		missing = append(missing, "port allocator")
	}
	if needBackend && s.deps.Backend == nil {
		missing = append(missing, "backend")
	}
	if needHosts && s.deps.Helper == nil {
		missing = append(missing, "hosts manager")
	}
	if len(missing) != 0 {
		return fmt.Errorf("sites: missing dependencies: %s", strings.Join(missing, ", "))
	}
	return nil
}

// backendSpec converts a persisted site into the backend's execution view.
func backendSpec(site model.Site) backend.SiteSpec {
	runtimeVersion := ""
	if site.Kind == model.SiteNode {
		runtimeVersion = site.NodeVersion
	}
	return backend.SiteSpec{
		ID:              site.ID,
		Kind:            string(site.Kind),
		HostPath:        site.HostPath,
		BackendPath:     site.BackendPath,
		DocumentRoot:    site.DocumentRoot,
		Command:         site.Command,
		Env:             cloneEnv(site.Env),
		HTTPPort:        site.HTTPPort,
		FastCGIPort:     site.FastCGIPort,
		SPAFallback:     site.SPAFallback,
		FrontController: site.FrontController,
		RuntimeVersion:  runtimeVersion,
		PHPVersion:      site.PHPVersion,
	}
}

// siteDomains returns the primary domain followed by the optional www alias.
func siteDomains(site model.Site) []string {
	primary := strings.ToLower(strings.TrimSpace(site.Domain))
	if primary == "" {
		return nil
	}
	domains := []string{primary}
	if site.WWW {
		domains = append(domains, "www."+primary)
	}
	return domains
}

// sameDomainSet compares domain lists regardless of order.
func sameDomainSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, value := range a {
		set[value] = struct{}{}
	}
	for _, value := range b {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func validateDocumentRoot(projectRoot, value string) (string, error) {
	root := strings.TrimSpace(value)
	if root == "" {
		root = "."
	}
	target := root
	if !filepath.IsAbs(root) {
		if !filepath.IsLocal(root) {
			return "", fmt.Errorf("sites: document root %q escapes the project", root)
		}
		target = filepath.Join(projectRoot, root)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("sites: document root %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("sites: document root %q is not a directory", root)
	}
	return root, nil
}

func (s *Service) currentTime() time.Time {
	if s.deps.Now != nil {
		if now := s.deps.Now(); !now.IsZero() {
			return now.UTC()
		}
	}
	return time.Now().UTC()
}

func (s *Service) emitStatus(ctx context.Context, site model.Site, old, current model.SiteStatus, detail string) {
	if old == current {
		return
	}
	from := string(old)
	if from == "" {
		from = "none"
	}
	message := fmt.Sprintf("site %s status %s -> %s", site.Name, from, current)
	if detail != "" {
		message += ": " + detail
	}
	s.emitEvent(ctx, site.ID, "site."+string(current), message)
}

// emitEvent records a diagnostic event. Event persistence is best effort and
// never fails the lifecycle operation.
func (s *Service) emitEvent(ctx context.Context, siteID, typ, message string) {
	if s.deps.Store == nil {
		return
	}
	_ = s.deps.Store.AddEvent(ctx, model.Event{
		SiteID:    siteID,
		Type:      typ,
		Message:   message,
		CreatedAt: s.currentTime(),
	})
}

func cloneEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	cloned := make(map[string]string, len(env))
	for key, value := range env {
		cloned[key] = value
	}
	return cloned
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// defaultNewID returns a random 128-bit hex identifier. random failure is not
// recoverable through this signature, so it falls back to a timestamp.
func defaultNewID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UTC().UnixNano(), 16)
	}
	return hex.EncodeToString(raw[:])
}
