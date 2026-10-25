package dockerweb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/serviceconfig"
)

// SiteStore lists the persisted sites that need vhosts.
type SiteStore interface {
	ListSites(ctx context.Context) ([]model.Site, error)
}

// CertManager ensures the native leaf certificates that the container mounts
// read-only from the host certs directory.
type CertManager interface {
	Ensure(ctx context.Context, owner string, domains []string) (model.CertRecord, error)
	PEM(rec model.CertRecord) (certPEM, keyPEM []byte, err error)
}

// Controller is the P2 service RPC subset used to validate and reload the
// active container configuration.
type Controller interface {
	Validate(ctx context.Context, server Server) error
	Reload(ctx context.Context, server Server) error
}

// ManagerConfig describes one devstack root and its web-server service trees.
type ManagerConfig struct {
	Root         string
	Projects     string
	ServicesDir  string
	Store        SiteStore
	Certs        CertManager
	Controller   Controller
	Dashboard    DashboardSpec
	MaxBodyBytes int64
	// HTTPListenPort and HTTPSListenPort exist for tests and the Docker smoke
	// test. Production leaves them zero, which means 80/443.
	HTTPListenPort  int
	HTTPSListenPort int
}

// ConflictError reports generated vhosts that have local edits and were not
// overwritten.
type ConflictError struct {
	Conflicts []serviceconfig.Conflict
}

func (e *ConflictError) Error() string {
	if len(e.Conflicts) == 0 {
		return "dockerweb: config conflict"
	}
	paths := make([]string, 0, len(e.Conflicts))
	for _, conflict := range e.Conflicts {
		paths = append(paths, conflict.Path)
	}
	return "dockerweb: generated configuration has local edits: " + strings.Join(paths, ", ")
}

// Manager owns the Docker web-server config tree and keeps generated vhosts
// in sync with the site store.
type Manager struct {
	projects    string
	servicesDir string
	store       SiteStore
	certs       CertManager
	controller  Controller
	dashboard   DashboardSpec
	maxBody     int64
	httpPort    int
	httpsPort   int
}

// NewManager validates the root layout and returns a Manager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, errors.New("dockerweb: root is required")
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("dockerweb: resolve root: %w", err)
	}
	projects := cfg.Projects
	if strings.TrimSpace(projects) == "" {
		projects = filepath.Join(root, "projects")
	}
	servicesDir := cfg.ServicesDir
	if strings.TrimSpace(servicesDir) == "" {
		servicesDir = filepath.Join(root, "services")
	}
	dashboard := cfg.Dashboard
	if strings.TrimSpace(dashboard.Domain) == "" {
		dashboard.Domain = dashboardDomain
	}
	if dashboard.UIPort == 0 {
		dashboard.UIPort = 7070
	}
	if err := validateDashboard(dashboard); err != nil {
		return nil, err
	}
	m := &Manager{
		projects:    filepath.Clean(projects),
		servicesDir: filepath.Clean(servicesDir),
		store:       cfg.Store,
		certs:       cfg.Certs,
		controller:  cfg.Controller,
		dashboard:   dashboard,
		maxBody:     cfg.MaxBodyBytes,
		httpPort:    cfg.HTTPListenPort,
		httpsPort:   cfg.HTTPSListenPort,
	}
	if m.maxBody == 0 {
		m.maxBody = defaultMaxBodyBytes
	}
	if m.httpPort == 0 {
		m.httpPort = defaultHTTPPort
	}
	if m.httpsPort == 0 {
		m.httpsPort = defaultHTTPSPort
	}
	if err := validateListenPorts(m.httpPort, m.httpsPort); err != nil {
		return nil, err
	}
	return m, nil
}

// EnsureBase seeds the full base config and the branded default 404 vhost. It
// creates the generated/custom/log directories and never replaces a base file
// that already exists, so user edits survive every sync.
func (m *Manager) EnsureBase(server Server) error {
	if err := server.Validate(); err != nil {
		return err
	}
	serviceRoot := filepath.Join(m.servicesDir, string(server))
	cfg, err := serviceconfig.New(serviceRoot)
	if err != nil {
		return err
	}
	for _, dir := range []string{
		filepath.Join(serviceRoot, "config", "conf.d", "generated"),
		filepath.Join(serviceRoot, "config", "conf.d", "custom"),
		filepath.Join(serviceRoot, "logs"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("dockerweb: create %s: %w", dir, err)
		}
	}

	baseRel := "config/nginx.conf"
	if server == ServerApache {
		baseRel = "config/httpd.conf"
	}
	base, err := RenderBase(server, m.httpPort, m.httpsPort)
	if err != nil {
		return err
	}
	if _, err := cfg.SeedBase(baseRel, base, 0o644); err != nil {
		return fmt.Errorf("dockerweb: seed %s base: %w", server, err)
	}

	defaultSet := Set{
		Projects:        m.projects,
		CertsDir:        defaultCertsDir,
		Dashboard:       m.dashboard,
		MaxBodyBytes:    m.maxBody,
		HTTPListenPort:  m.httpPort,
		HTTPSListenPort: m.httpsPort,
	}
	files, err := Render(server, defaultSet)
	if err != nil {
		return err
	}
	if _, err := cfg.SeedBase("config/conf.d/00-devstack-default.conf", files["_default.conf"], 0o644); err != nil {
		return fmt.Errorf("dockerweb: seed default 404 vhost: %w", err)
	}
	return nil
}

// Sync renders the dashboard and site vhosts, tracks them in the service
// config manifest, and removes stale unmodified generated files. A modified
// generated file is reported as a ConflictError and left in place.
func (m *Manager) Sync(ctx context.Context, server Server) error {
	if err := server.Validate(); err != nil {
		return err
	}
	if m.store == nil {
		return errors.New("dockerweb: site store is not configured")
	}
	if m.certs == nil {
		return errors.New("dockerweb: certificate manager is not configured")
	}
	if err := m.EnsureBase(server); err != nil {
		return err
	}

	if _, err := m.certs.Ensure(ctx, dashboardOwner, []string{dashboardDomain}); err != nil {
		return fmt.Errorf("dockerweb: ensure dashboard certificate: %w", err)
	}

	sites, err := m.store.ListSites(ctx)
	if err != nil {
		return fmt.Errorf("dockerweb: list sites: %w", err)
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Name == sites[j].Name {
			return sites[i].ID < sites[j].ID
		}
		return sites[i].Name < sites[j].Name
	})

	specs := make([]SiteSpec, 0, len(sites))
	for _, site := range sites {
		spec, err := m.siteSpec(ctx, site)
		if err != nil {
			return err
		}
		specs = append(specs, spec)
	}

	files, err := Render(server, Set{
		Projects:        m.projects,
		CertsDir:        defaultCertsDir,
		Sites:           specs,
		Dashboard:       m.dashboard,
		MaxBodyBytes:    m.maxBody,
		HTTPListenPort:  m.httpPort,
		HTTPSListenPort: m.httpsPort,
	})
	if err != nil {
		return err
	}

	serviceRoot := filepath.Join(m.servicesDir, string(server))
	cfg, err := serviceconfig.New(serviceRoot)
	if err != nil {
		return err
	}
	desired := map[string][]byte{
		"_devstack.test.conf": files["_devstack.test.conf"],
	}
	for _, spec := range specs {
		desired[spec.ID+".conf"] = files[spec.ID+".conf"]
	}

	var conflicts []serviceconfig.Conflict
	for _, name := range sortedKeys(desired) {
		rel := "config/conf.d/generated/" + name
		result, err := cfg.WriteGenerated(rel, desired[name], 0o644, "docker web vhost for "+string(server))
		if err != nil {
			return err
		}
		if result.Conflict != nil {
			conflicts = append(conflicts, *result.Conflict)
		}
	}
	if len(conflicts) > 0 {
		return &ConflictError{Conflicts: conflicts}
	}

	if err := m.removeStaleGenerated(cfg, serviceRoot, desired); err != nil {
		return err
	}
	return nil
}

func (m *Manager) siteSpec(ctx context.Context, site model.Site) (SiteSpec, error) {
	root := absoluteSiteRoot(site)
	if site.Kind != model.SiteNode {
		if _, err := containerRoot(m.projects, SiteSpec{ID: site.ID, Kind: site.Kind, HostDocumentRoot: root}); err != nil {
			return SiteSpec{}, err
		}
	}
	certOwner := site.ID
	_, err := m.certs.Ensure(ctx, site.ID, siteDomains(site))
	if err != nil {
		if site.TLS {
			return SiteSpec{}, fmt.Errorf("dockerweb: ensure certificate for %q: %w", site.Name, err)
		}
		certOwner = dashboardOwner
	}
	return SiteSpec{
		ID:                  site.ID,
		Name:                site.Name,
		Domain:              site.Domain,
		WWW:                 site.WWW,
		Kind:                site.Kind,
		HostDocumentRoot:    root,
		BackendPort:         siteBackendPort(site),
		TLS:                 site.TLS,
		ForceHTTPS:          site.ForceHTTPS,
		FrontController:     site.FrontController,
		FrontControllerFile: site.FrontControllerFile,
		FrontControllerMode: site.FrontControllerMode,
		ApacheAllowOverride: site.ApacheAllowOverride,
		SPAFallback:         site.SPAFallback,
		SPAFallbackFile:     site.SPAFallbackFile,
		DesiredState:        site.DesiredState,
		CertOwner:           certOwner,
	}, nil
}

func (m *Manager) removeStaleGenerated(cfg *serviceconfig.Manager, serviceRoot string, desired map[string][]byte) error {
	dir := filepath.Join(serviceRoot, "config", "conf.d", "generated")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("dockerweb: read generated directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		if _, ok := desired[entry.Name()]; ok {
			continue
		}
		rel := "config/conf.d/generated/" + entry.Name()
		status, err := cfg.Status(rel)
		if err != nil {
			return err
		}
		if status.Class != serviceconfig.ClassGenerated || status.Modified || status.Detached {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("dockerweb: remove stale %s: %w", rel, err)
		}
	}
	return nil
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func absoluteSiteRoot(site model.Site) string {
	root := strings.TrimSpace(site.DocumentRoot)
	if root == "" || root == "." {
		if site.Kind == model.SiteStatic || site.Kind == model.SitePHP {
			return filepath.Clean(site.BackendPath)
		}
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Clean(filepath.Join(site.BackendPath, root))
}

func siteBackendPort(site model.Site) int {
	switch site.Kind {
	case model.SitePHP:
		return site.FastCGIPort
	case model.SiteNode:
		return site.HTTPPort
	default:
		return 0
	}
}

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

// Validate runs the catalog validation command inside the active container.
func (m *Manager) Validate(ctx context.Context, server Server) error {
	if m.controller == nil {
		return errors.New("dockerweb: service controller is not configured")
	}
	return m.controller.Validate(ctx, server)
}

// Reload gracefully reloads the active container configuration.
func (m *Manager) Reload(ctx context.Context, server Server) error {
	if m.controller == nil {
		return errors.New("dockerweb: service controller is not configured")
	}
	return m.controller.Reload(ctx, server)
}
