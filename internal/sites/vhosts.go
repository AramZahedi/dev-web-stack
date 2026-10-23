package sites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/vhost"
)

// servingMode returns the configured serving mode, treating an unset value as
// builtin. Config.Default always sets it; the fallback keeps zero-value Config
// values usable in tests.
func (s *Service) servingMode() string {
	mode := strings.TrimSpace(s.deps.Config.ServingMode)
	if mode == "" {
		return config.ServingModeBuiltin
	}
	return mode
}

func (s *Service) externalServing() bool {
	return s.servingMode() != config.ServingModeBuiltin
}

// ServingMode returns the currently effective serving mode. It reflects a
// runtime switch saved by SetServingMode.
func (s *Service) ServingMode() string {
	if s == nil {
		return config.ServingModeBuiltin
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servingMode()
}

func builtinVhostStatus() helperproto.VhostStatusResponse {
	return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin, ConfigOK: true}
}

// VhostStatus reports the helper's persisted vhost state. A daemon without a
// vhost-capable helper reports builtin state while the configured mode is
// builtin; an external mode surfaces the helper error.
func (s *Service) VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.vhostStatusLocked(ctx)
}

func (s *Service) vhostStatusLocked(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	if s.deps.Vhosts == nil {
		return builtinVhostStatus(), nil
	}
	status, err := s.deps.Vhosts.VhostStatus(ctx)
	if err != nil {
		if errors.Is(err, helperproto.ErrNotImplemented) && !s.externalServing() {
			return builtinVhostStatus(), nil
		}
		return helperproto.VhostStatusResponse{}, err
	}
	if strings.TrimSpace(status.Mode) == "" {
		status.Mode = config.ServingModeBuiltin
	}
	return status, nil
}

// SyncVhosts installs a complete vhost set when an external mode is active.
// Builtin mode is a no-op.
func (s *Service) SyncVhosts(ctx context.Context) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.syncVhostsLocked(ctx, "")
}

func (s *Service) syncVhostsLocked(ctx context.Context, excludeID string) error {
	if !s.externalServing() {
		return nil
	}
	if s.deps.Vhosts == nil {
		return errors.New("sites: vhost helper is not configured")
	}
	req, err := s.buildVhostRequestLocked(ctx, s.servingMode(), excludeID)
	if err != nil {
		return err
	}
	if err := s.deps.Vhosts.SyncVhosts(ctx, req); err != nil {
		return fmt.Errorf("sites: sync external vhosts: %w", err)
	}
	return nil
}

// ReconcileServingMode makes the helper agree with config.yaml. Configuration
// wins: the helper is switched without saving config, and a matching external
// mode receives a full sync. It never starts or stops the proxy or static
// backends; daemon startup owns that ordering.
func (s *Service) ReconcileServingMode(ctx context.Context) error {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	mode := s.servingMode()
	if s.deps.Vhosts == nil {
		if s.externalServing() {
			return errors.New("sites: vhost helper is not configured")
		}
		return nil
	}

	status, err := s.deps.Vhosts.VhostStatus(ctx)
	if err != nil {
		if errors.Is(err, helperproto.ErrNotImplemented) && mode == config.ServingModeBuiltin {
			return nil
		}
		return fmt.Errorf("sites: read vhost status: %w", err)
	}
	helperMode := strings.TrimSpace(status.Mode)
	if helperMode == "" {
		helperMode = config.ServingModeBuiltin
	}
	if helperMode == mode {
		if mode == config.ServingModeBuiltin {
			return nil
		}
		return s.syncVhostsLocked(ctx, "")
	}

	req := helperproto.VHostSyncRequest{}
	if mode != config.ServingModeBuiltin {
		req, err = s.buildVhostRequestLocked(ctx, mode, "")
		if err != nil {
			return err
		}
	}
	if _, err := s.deps.Vhosts.SetServingMode(ctx, mode, req); err != nil {
		return fmt.Errorf("sites: reconcile serving mode %q: %w", mode, err)
	}
	return nil
}

// SetServingMode performs a runtime switch and persists it only after the
// helper succeeds. Every failure restores the helper, proxy, static backends,
// and in-memory configuration to their previous state.
func (s *Service) SetServingMode(ctx context.Context, mode string) error {
	ctx = nonNilContext(ctx)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !config.ValidServingMode(mode) {
		return fmt.Errorf("sites: unsupported serving mode %q", mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.servingMode()
	if mode == current {
		if current == config.ServingModeBuiltin {
			return nil
		}
		return s.syncVhostsLocked(ctx, "")
	}
	if s.deps.Vhosts == nil {
		return errors.New("sites: vhost helper is not configured")
	}
	if strings.TrimSpace(s.deps.Config.Home) == "" {
		return errors.New("sites: config home is not set")
	}

	var target helperproto.VHostSyncRequest
	var err error
	if mode != config.ServingModeBuiltin {
		target, err = s.buildVhostRequestLocked(ctx, mode, "")
		if err != nil {
			return err
		}
	}

	switch {
	case current == config.ServingModeBuiltin:
		return s.switchBuiltinToExternal(ctx, mode, target)
	case mode == config.ServingModeBuiltin:
		return s.switchExternalToBuiltin(ctx, current)
	default:
		return s.switchExternal(ctx, current, mode, target)
	}
}

func (s *Service) switchBuiltinToExternal(ctx context.Context, mode string, req helperproto.VHostSyncRequest) error {
	if err := s.stopStaticBackendsLocked(ctx); err != nil {
		return err
	}

	if s.deps.Proxy != nil {
		if err := s.deps.Proxy.Stop(ctx); err != nil {
			restore := s.restoreBuiltinRuntimeLocked(ctx)
			return errors.Join(fmt.Errorf("sites: stop built-in proxy: %w", err), restore)
		}
	}

	if _, err := s.deps.Vhosts.SetServingMode(ctx, mode, req); err != nil {
		restore := s.restoreBuiltinRuntimeLocked(ctx)
		return errors.Join(fmt.Errorf("sites: switch helper to %q: %w", mode, err), restore)
	}

	if err := s.saveServingModeLocked(mode); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("sites: save serving mode: %w", err))
		if _, herr := s.deps.Vhosts.SetServingMode(ctx, config.ServingModeBuiltin, helperproto.VHostSyncRequest{}); herr != nil {
			errs = append(errs, fmt.Errorf("rollback: restore helper builtin mode: %w", herr))
		}
		if restoreErr := s.restoreBuiltinRuntimeLocked(ctx); restoreErr != nil {
			errs = append(errs, restoreErr)
		}
		return errors.Join(errs...)
	}
	return nil
}

func (s *Service) switchExternalToBuiltin(ctx context.Context, previous string) error {
	if s.deps.Proxy != nil {
		if err := s.deps.Proxy.Start(ctx); err != nil {
			return fmt.Errorf("sites: start built-in proxy: %w", err)
		}
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("sites: publish routes before builtin switch: %w", err))
		if s.deps.Proxy != nil {
			if stopErr := s.deps.Proxy.Stop(ctx); stopErr != nil {
				errs = append(errs, fmt.Errorf("rollback: stop built-in proxy: %w", stopErr))
			}
		}
		return errors.Join(errs...)
	}

	if _, err := s.deps.Vhosts.SetServingMode(ctx, config.ServingModeBuiltin, helperproto.VHostSyncRequest{}); err != nil {
		var errs []error
		errs = append(errs, fmt.Errorf("sites: switch helper to builtin: %w", err))
		if s.deps.Proxy != nil {
			if stopErr := s.deps.Proxy.Stop(ctx); stopErr != nil {
				errs = append(errs, fmt.Errorf("rollback: stop built-in proxy: %w", stopErr))
			}
		}
		return errors.Join(errs...)
	}

	if err := s.saveServingModeLocked(config.ServingModeBuiltin); err != nil {
		return errors.Join(
			fmt.Errorf("sites: save serving mode: %w", err),
			s.restoreExternalModeLocked(ctx, previous),
		)
	}

	if err := s.startStaticBackendsLocked(ctx); err != nil {
		return errors.Join(
			fmt.Errorf("sites: start static backends: %w", err),
			s.restoreExternalModeLocked(ctx, previous),
			s.stopStaticBackendsLocked(ctx),
		)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return errors.Join(
			fmt.Errorf("sites: publish routes after builtin switch: %w", err),
			s.restoreExternalModeLocked(ctx, previous),
			s.stopStaticBackendsLocked(ctx),
		)
	}
	return nil
}

func (s *Service) switchExternal(ctx context.Context, previous, mode string, req helperproto.VHostSyncRequest) error {
	if _, err := s.deps.Vhosts.SetServingMode(ctx, mode, req); err != nil {
		return fmt.Errorf("sites: switch helper to %q: %w", mode, err)
	}
	if err := s.saveServingModeLocked(mode); err != nil {
		errs := []error{fmt.Errorf("sites: save serving mode: %w", err)}
		oldReq, buildErr := s.buildVhostRequestLocked(ctx, previous, "")
		if buildErr != nil {
			errs = append(errs, fmt.Errorf("rollback: build %q request: %w", previous, buildErr))
		} else if _, herr := s.deps.Vhosts.SetServingMode(ctx, previous, oldReq); herr != nil {
			errs = append(errs, fmt.Errorf("rollback: restore helper %q mode: %w", previous, herr))
		}
		return errors.Join(errs...)
	}
	return nil
}

// restoreExternalModeLocked switches the helper back to previous and stops the
// built-in proxy. Configuration is restored when possible.
func (s *Service) restoreExternalModeLocked(ctx context.Context, previous string) error {
	var errs []error
	req, err := s.buildVhostRequestLocked(ctx, previous, "")
	if err != nil {
		errs = append(errs, fmt.Errorf("rollback: build %q request: %w", previous, err))
	} else if _, err := s.deps.Vhosts.SetServingMode(ctx, previous, req); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore helper %q mode: %w", previous, err))
	}
	if s.deps.Proxy != nil {
		if err := s.deps.Proxy.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("rollback: stop built-in proxy: %w", err))
		}
	}
	if err := s.saveServingModeLocked(previous); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore saved serving mode: %w", err))
	}
	return errors.Join(errs...)
}

func (s *Service) saveServingModeLocked(mode string) error {
	cfg := s.deps.Config
	cfg.ServingMode = mode
	if strings.TrimSpace(cfg.Home) == "" {
		return errors.New("config home is not set")
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	s.deps.Config.ServingMode = mode
	return nil
}

// restoreBuiltinRuntimeLocked starts the built-in proxy and any static sites
// whose desired state is running. The configured mode is already builtin.
func (s *Service) restoreBuiltinRuntimeLocked(ctx context.Context) error {
	var errs []error
	if s.deps.Proxy != nil {
		if err := s.deps.Proxy.Start(ctx); err != nil {
			errs = append(errs, fmt.Errorf("rollback: start built-in proxy: %w", err))
		}
	}
	if err := s.startStaticBackendsLocked(ctx); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restart static backends: %w", err))
	}
	return errors.Join(errs...)
}

// stopStaticBackendsLocked stops every static site process without changing
// desired state. Static sites have no process in external mode, so Stop is
// idempotent and already-stopped sites are unaffected.
func (s *Service) stopStaticBackendsLocked(ctx context.Context) error {
	if s.deps.Backend == nil || s.deps.Store == nil {
		return nil
	}
	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("sites: list sites for mode switch: %w", err)
	}
	var stopped []model.Site
	var errs []error
	for _, site := range stored {
		if site.Kind != model.SiteStatic || site.DesiredState != model.DesiredRunning {
			continue
		}
		stopped = append(stopped, site)
		if err := s.deps.Backend.Stop(ctx, site.ID, true); err != nil {
			errs = append(errs, fmt.Errorf("sites: stop static site %q: %w", site.Name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	// A partial stop must not leave some running sites stopped. Start every
	// site that was targeted, including one whose Stop reported an error.
	for _, site := range stopped {
		if _, err := s.startLocked(ctx, site); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restart static site %q: %w", site.Name, err))
		}
	}
	return errors.Join(errs...)
}

// startStaticBackendsLocked starts every static site whose desired state is
// running, allocating a port when one is not already reserved.
func (s *Service) startStaticBackendsLocked(ctx context.Context) error {
	if s.deps.Store == nil {
		return nil
	}
	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("sites: list sites for mode switch: %w", err)
	}
	var errs []error
	for _, site := range stored {
		if site.Kind != model.SiteStatic || site.DesiredState != model.DesiredRunning {
			continue
		}
		if _, err := s.startLocked(ctx, site); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// buildVhostRequestLocked renders the complete helper request from the store.
// Certificates are ensured before their PEM is read.
func (s *Service) buildVhostRequestLocked(ctx context.Context, mode, excludeID string) (helperproto.VHostSyncRequest, error) {
	if s.deps.Store == nil || s.deps.Certs == nil {
		return helperproto.VHostSyncRequest{}, errors.New("sites: store and certificate manager are required")
	}
	dashboardRecord, err := s.deps.Certs.Ensure(ctx, certs.DashboardOwner, []string{dashboardHost})
	if err != nil {
		return helperproto.VHostSyncRequest{}, fmt.Errorf("sites: ensure dashboard certificate: %w", err)
	}
	dashboardCert, dashboardKey, err := s.deps.Certs.PEM(dashboardRecord)
	if err != nil {
		return helperproto.VHostSyncRequest{}, fmt.Errorf("sites: read dashboard certificate: %w", err)
	}

	stored, err := s.deps.Store.ListSites(ctx)
	if err != nil {
		return helperproto.VHostSyncRequest{}, fmt.Errorf("sites: list sites for vhosts: %w", err)
	}
	sort.Slice(stored, func(i, j int) bool {
		if stored[i].Name == stored[j].Name {
			return stored[i].ID < stored[j].ID
		}
		return stored[i].Name < stored[j].Name
	})

	sites := make([]helperproto.VHostSite, 0, len(stored))
	for _, site := range stored {
		if site.ID == excludeID {
			continue
		}
		spec, err := s.vhostSiteSpecLocked(ctx, site)
		if err != nil {
			return helperproto.VHostSyncRequest{}, err
		}
		sites = append(sites, spec)
	}
	return helperproto.VHostSyncRequest{
		Mode:         mode,
		MaxBodyBytes: int64(s.deps.Config.MaxRequestBodyMB) << 20,
		Sites:        sites,
		Dashboard: helperproto.VHostDashboard{
			Domain:      dashboardHost,
			BackendPort: dashboardPort(s.deps.Config),
			CertPEM:     dashboardCert,
			KeyPEM:      dashboardKey,
		},
		RequestedAt: s.currentTime(),
	}, nil
}

func (s *Service) vhostSiteSpecLocked(ctx context.Context, site model.Site) (helperproto.VHostSite, error) {
	documentRoot := absoluteDocumentRoot(site)
	spec := helperproto.VHostSite{
		ID:              site.ID,
		Name:            site.Name,
		Domain:          site.Domain,
		BackendPath:     site.BackendPath,
		WWW:             site.WWW,
		Kind:            string(site.Kind),
		DocumentRoot:    documentRoot,
		TLS:             site.TLS,
		ForceHTTPS:      site.ForceHTTPS,
		SPAFallback:     site.SPAFallback,
		FrontController: site.FrontController,
		DesiredState:    string(site.DesiredState),
	}
	switch site.Kind {
	case model.SiteStatic:
		spec.BackendPort = 0
	case model.SiteNode:
		spec.BackendPort = site.HTTPPort
	default:
		spec.BackendPort = site.HTTPPort
	}
	if site.TLS {
		rec, err := s.deps.Certs.Ensure(ctx, site.ID, siteDomains(site))
		if err != nil {
			return helperproto.VHostSite{}, fmt.Errorf("sites: ensure certificate for %q: %w", site.Name, err)
		}
		certPEM, keyPEM, err := s.deps.Certs.PEM(rec)
		if err != nil {
			return helperproto.VHostSite{}, fmt.Errorf("sites: read certificate for %q: %w", site.Name, err)
		}
		spec.CertPEM = certPEM
		spec.KeyPEM = keyPEM
	}
	return spec, nil
}

// PreviewVhost renders one site's vhost without installing it or calling the
// helper. Certificate material is ensured so the rendered TLS paths are valid.
func (s *Service) PreviewVhost(ctx context.Context, ref, server string) (string, error) {
	ctx = nonNilContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkDeps(false, false); err != nil {
		return "", err
	}
	site, err := s.resolveLocked(ctx, ref)
	if err != nil {
		return "", err
	}
	server = strings.ToLower(strings.TrimSpace(server))
	if server != string(vhost.ServerNginx) && server != string(vhost.ServerApache) {
		return "", fmt.Errorf("sites: unsupported preview server %q", server)
	}

	spec, err := s.vhostSiteSpecLocked(ctx, site)
	if err != nil {
		return "", err
	}
	dashboardRecord, err := s.deps.Certs.Ensure(ctx, certs.DashboardOwner, []string{dashboardHost})
	if err != nil {
		return "", fmt.Errorf("sites: ensure dashboard certificate: %w", err)
	}
	dashboardCert, dashboardKey, err := s.deps.Certs.PEM(dashboardRecord)
	if err != nil {
		return "", fmt.Errorf("sites: read dashboard certificate: %w", err)
	}
	files, err := vhost.Render(vhost.Set{
		Server: vhost.Server(server),
		Sites: []vhost.SiteSpec{{
			ID:              spec.ID,
			Name:            spec.Name,
			Domain:          spec.Domain,
			WWW:             spec.WWW,
			Kind:            site.Kind,
			DocumentRoot:    spec.DocumentRoot,
			BackendPort:     spec.BackendPort,
			TLS:             spec.TLS,
			ForceHTTPS:      spec.ForceHTTPS,
			SPAFallback:     spec.SPAFallback,
			FrontController: spec.FrontController,
			DesiredState:    model.DesiredState(spec.DesiredState),
			CertPEM:         spec.CertPEM,
			KeyPEM:          spec.KeyPEM,
		}},
		Dashboard: vhost.DashboardSpec{
			Domain:  dashboardHost,
			UIPort:  dashboardPort(s.deps.Config),
			CertPEM: dashboardCert,
			KeyPEM:  dashboardKey,
		},
		MaxBodyBytes: int64(s.deps.Config.MaxRequestBodyMB) << 20,
	})
	if err != nil {
		return "", fmt.Errorf("sites: render %s preview for %q: %w", server, site.Name, err)
	}
	content, ok := files[vhostFilename(site)]
	if !ok {
		return "", fmt.Errorf("sites: rendered preview for %q is missing", site.Name)
	}
	return string(content), nil
}

// absoluteDocumentRoot resolves a site document root against its backend path.
// Static sites with an empty or "." root serve the backend path itself; an
// empty Node document root stays empty.
func absoluteDocumentRoot(site model.Site) string {
	root := strings.TrimSpace(site.DocumentRoot)
	if root == "" || root == "." {
		if site.Kind == model.SiteStatic {
			return filepath.Clean(site.BackendPath)
		}
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Clean(filepath.Join(site.BackendPath, root))
}

// vhostFilename mirrors vhost.siteFilename without exporting the renderer's
// internal naming helper.
func vhostFilename(site model.Site) string {
	id := site.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return site.Name + "-" + id + ".conf"
}

// staticRootError verifies that an external static site's document root exists,
// is a directory, and can be opened for reading.
func staticRootError(site model.Site) error {
	path := absoluteDocumentRoot(site)
	if path == "" {
		return errors.New("document root is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if _, err := dir.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// deleteExternalLocked removes an external site: stop any process, publish the
// remaining hosts and vhost set, and only then delete its certificate and row.
// Failures restore the row, hosts, certificate, and process where possible.
func (s *Service) deleteExternalLocked(ctx context.Context, site model.Site) error {
	oldHosts, err := s.hostsFor(ctx, "", nil)
	if err != nil {
		return err
	}
	if s.deps.Backend != nil {
		if err := s.deps.Backend.Stop(ctx, site.ID, true); err != nil {
			return fmt.Errorf("sites: stop %q: %w", site.Name, err)
		}
	}

	stopped := site
	stopped.DesiredState = model.DesiredStopped
	stopped.Status = model.StatusStopped
	stopped.UpdatedAt = s.currentTime()
	if err := s.deps.Store.UpdateSite(ctx, stopped); err != nil {
		return fmt.Errorf("sites: record stopped state for %q: %w", site.Name, err)
	}

	remaining, err := s.hostsFor(ctx, site.ID, nil)
	if err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, err)
	}
	if err := s.syncHosts(ctx, remaining); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: sync hosts before deleting %q: %w", site.Name, err))
	}
	if err := s.syncVhostsLocked(ctx, site.ID); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: sync vhosts before deleting %q: %w", site.Name, err))
	}

	if err := s.deps.Certs.Delete(ctx, site.ID); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: delete certificate for %q: %w", site.Name, err))
	}
	if err := s.deps.Store.DeleteSite(ctx, site.ID); err != nil {
		return s.restoreExternalDelete(ctx, site, oldHosts, fmt.Errorf("sites: delete %q: %w", site.Name, err))
	}

	s.emitEvent(ctx, site.ID, "site.deleted", fmt.Sprintf("site %s deleted", site.Name))
	s.deps.Ports.Release(site.HTTPPort)
	if site.FastCGIPort != 0 {
		s.deps.Ports.Release(site.FastCGIPort)
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		return fmt.Errorf("sites: publish routes after deleting %q: %w", site.Name, err)
	}
	return nil
}

// restoreExternalDelete reverses the reversible parts of a failed external
// deletion. The vhost set is re-synced last so it matches the restored rows.
func (s *Service) restoreExternalDelete(ctx context.Context, site model.Site, oldHosts []string, cause error) error {
	errs := []error{cause}
	if _, err := s.deps.Certs.Ensure(ctx, site.ID, siteDomains(site)); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore certificate for %q: %w", site.Name, err))
	}
	if err := s.deps.Store.UpdateSite(ctx, site); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore row %q: %w", site.Name, err))
	}
	if err := s.syncHosts(ctx, oldHosts); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore hosts for %q: %w", site.Name, err))
	}
	if site.DesiredState == model.DesiredRunning && site.Kind == model.SiteNode && s.deps.Backend != nil {
		if _, err := s.deps.Backend.Prepare(ctx, backendSpec(site)); err == nil {
			if _, err := s.deps.Backend.Start(ctx, site.ID); err != nil {
				errs = append(errs, fmt.Errorf("rollback: restart %q: %w", site.Name, err))
			}
		} else {
			errs = append(errs, fmt.Errorf("rollback: prepare %q: %w", site.Name, err))
		}
	}
	if s.externalServing() {
		if err := s.syncVhostsLocked(ctx, ""); err != nil {
			errs = append(errs, fmt.Errorf("rollback: restore vhosts for %q: %w", site.Name, err))
		}
	}
	if err := s.rebuildRoutesLocked(ctx); err != nil {
		errs = append(errs, fmt.Errorf("rollback: restore routes for %q: %w", site.Name, err))
	}
	return errors.Join(errs...)
}
