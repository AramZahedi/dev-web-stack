package dockerweb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeSiteStore struct {
	sites []model.Site
	err   error
}

func (f *fakeSiteStore) ListSites(context.Context) ([]model.Site, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]model.Site, len(f.sites))
	copy(out, f.sites)
	return out, nil
}

type fakeCertManager struct {
	dir   string
	err   map[string]error
	calls []string
}

func (f *fakeCertManager) Ensure(_ context.Context, owner string, _ []string) (model.CertRecord, error) {
	f.calls = append(f.calls, owner)
	if err := f.err[owner]; err != nil {
		return model.CertRecord{}, err
	}
	if f.dir != "" {
		if err := os.WriteFile(filepath.Join(f.dir, owner+".crt"), []byte("CERT "+owner), 0o644); err != nil {
			return model.CertRecord{}, err
		}
		if err := os.WriteFile(filepath.Join(f.dir, owner+".key"), []byte("KEY "+owner), 0o600); err != nil {
			return model.CertRecord{}, err
		}
	}
	return model.CertRecord{SiteID: owner}, nil
}

func (f *fakeCertManager) PEM(model.CertRecord) ([]byte, []byte, error) {
	return []byte("CERT"), []byte("KEY"), nil
}

type fakeController struct {
	validated []string
	reloaded  []string
	err       error
}

func (f *fakeController) Validate(_ context.Context, server Server) error {
	f.validated = append(f.validated, string(server))
	return f.err
}

func (f *fakeController) Reload(_ context.Context, server Server) error {
	f.reloaded = append(f.reloaded, string(server))
	return f.err
}

type managerFixture struct {
	root  string
	store *fakeSiteStore
	certs *fakeCertManager
	ctrl  *fakeController
	mgr   *Manager
	now   time.Time
}

func newManagerFixture(t *testing.T) *managerFixture {
	t.Helper()
	root := t.TempDir()
	certDir := filepath.Join(root, "certs")
	projects := filepath.Join(root, "projects")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &managerFixture{
		root:  root,
		store: &fakeSiteStore{},
		certs: &fakeCertManager{dir: certDir, err: map[string]error{}},
		ctrl:  &fakeController{},
		now:   time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
	}
	mgr, err := NewManager(ManagerConfig{
		Root:         root,
		Store:        f.store,
		Certs:        f.certs,
		Controller:   f.ctrl,
		Dashboard:    DashboardSpec{Domain: "devstack.test", UIPort: 7070},
		MaxBodyBytes: 64 << 20,
		Now:          func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	f.mgr = mgr
	return f
}

func (f *managerFixture) addStaticSite(t *testing.T, id, name, rel string) model.Site {
	t.Helper()
	docroot := filepath.Join(f.root, "projects", filepath.FromSlash(rel))
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := model.Site{
		ID:           id,
		Name:         name,
		Domain:       name + ".test",
		Kind:         model.SiteStatic,
		HostPath:     docroot,
		BackendPath:  docroot,
		DocumentRoot: ".",
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
		TLS:          true,
	}
	f.store.sites = append(f.store.sites, site)
	return site
}

func TestManagerEnsureBaseSeedsAndPreservesUserEdits(t *testing.T) {
	for _, server := range []Server{ServerNginx, ServerApache} {
		t.Run(string(server), func(t *testing.T) {
			f := newManagerFixture(t)
			if err := f.mgr.EnsureBase(server); err != nil {
				t.Fatalf("EnsureBase: %v", err)
			}
			baseName := "nginx.conf"
			defaultName := "00-devstack-default.conf"
			if server == ServerApache {
				baseName = "httpd.conf"
			}
			serviceDir := filepath.Join(f.root, "services", string(server))
			base := filepath.Join(serviceDir, "config", baseName)
			def := filepath.Join(serviceDir, "config", "conf.d", defaultName)
			for _, path := range []string{base, def} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("expected seeded %s: %v", path, err)
				}
			}
			for _, dir := range []string{"config/conf.d/generated", "config/conf.d/custom", "logs"} {
				if _, err := os.Stat(filepath.Join(serviceDir, filepath.FromSlash(dir))); err != nil {
					t.Errorf("expected directory %s: %v", dir, err)
				}
			}
			if _, err := os.Stat(filepath.Join(serviceDir, "config", ".config-manifest.json")); err != nil {
				t.Errorf("manifest is not under the config root: %v", err)
			}
			if _, err := os.Stat(filepath.Join(serviceDir, ".config-manifest.json")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("manifest must not be at the service root: %v", err)
			}
			if err := os.WriteFile(base, []byte("user edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := f.mgr.EnsureBase(server); err != nil {
				t.Fatalf("second EnsureBase: %v", err)
			}
			data, err := os.ReadFile(base)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "user edited\n" {
				t.Fatalf("EnsureBase overwrote a user-edited base config:\n%s", data)
			}
		})
	}
}

func TestManagerSyncWritesDashboardAndSiteVhosts(t *testing.T) {
	for _, server := range []Server{ServerNginx, ServerApache} {
		t.Run(string(server), func(t *testing.T) {
			f := newManagerFixture(t)
			f.addStaticSite(t, "app", "app", "app")
			if err := f.mgr.Sync(context.Background(), server); err != nil {
				t.Fatalf("Sync: %v", err)
			}
			generated := filepath.Join(f.root, "services", string(server), "config", "conf.d", "generated")
			for _, name := range []string{"app.conf", "_devstack.test.conf"} {
				path := filepath.Join(generated, name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("expected %s: %v", path, err)
				}
				text := string(data)
				if !strings.Contains(text, "server="+string(server)) || !strings.Contains(text, "sha256=") {
					t.Errorf("%s missing generated header\n%s", name, text)
				}
			}
			if _, err := os.Stat(filepath.Join(generated, "_default.conf")); !os.IsNotExist(err) {
				t.Errorf("default vhost must be seeded as a base file, not generated")
			}
			base := filepath.Join(f.root, "services", string(server), "config", "conf.d", "00-devstack-default.conf")
			if _, err := os.Stat(base); err != nil {
				t.Errorf("default 404 vhost missing: %v", err)
			}
			if len(f.certs.calls) < 2 || f.certs.calls[0] != "__dashboard__" || f.certs.calls[1] != "app" {
				t.Errorf("certificate ensure calls = %v, want dashboard then app", f.certs.calls)
			}
		})
	}
}

func TestManagerSyncNoSitesStillRendersDashboard(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.mgr.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	generated := filepath.Join(f.root, "services", "nginx", "config", "conf.d", "generated")
	if _, err := os.Stat(filepath.Join(generated, "_devstack.test.conf")); err != nil {
		t.Fatalf("dashboard vhost missing: %v", err)
	}
}

func TestManagerSyncRemovesStaleUnmodifiedGeneratedFiles(t *testing.T) {
	f := newManagerFixture(t)
	f.addStaticSite(t, "one", "one", "one")
	f.addStaticSite(t, "two", "two", "two")
	if err := f.mgr.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	generated := filepath.Join(f.root, "services", "nginx", "config", "conf.d", "generated")
	if _, err := os.Stat(filepath.Join(generated, "two.conf")); err != nil {
		t.Fatalf("two.conf missing after first sync: %v", err)
	}
	f.store.sites = f.store.sites[:1]
	if err := f.mgr.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(generated, "two.conf")); !os.IsNotExist(err) {
		t.Fatalf("stale unmodified generated file was not removed")
	}
	if _, err := os.Stat(filepath.Join(generated, "one.conf")); err != nil {
		t.Fatalf("live generated file removed: %v", err)
	}
}

func TestManagerSyncReportsConflictAndPreservesModifiedGeneratedFile(t *testing.T) {
	f := newManagerFixture(t)
	site := f.addStaticSite(t, "app", "app", "app")
	if err := f.mgr.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	generated := filepath.Join(f.root, "services", "nginx", "config", "conf.d", "generated", "app.conf")
	if err := os.WriteFile(generated, []byte("# user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.store.sites[0] = site
	f.store.sites[0].Domain = "changed.test"

	err := f.mgr.Sync(context.Background(), ServerNginx)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error type = %T, want *ConflictError: %v", err, err)
	}
	if len(conflict.Conflicts) == 0 || conflict.Conflicts[0].Path != "conf.d/generated/app.conf" {
		t.Fatalf("conflicts = %+v", conflict.Conflicts)
	}
	data, readErr := os.ReadFile(generated)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "# user edit\n" {
		t.Fatalf("modified generated file was overwritten:\n%s", data)
	}
}

func TestManagerSyncNeverTouchesCustomIncludes(t *testing.T) {
	f := newManagerFixture(t)
	f.addStaticSite(t, "app", "app", "app")
	customDir := filepath.Join(f.root, "services", "nginx", "config", "conf.d", "custom")
	if err := os.MkdirAll(customDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(customDir, "mine.conf")
	if err := os.WriteFile(custom, []byte("# custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.mgr.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := os.ReadFile(custom)
	if err != nil || string(data) != "# custom\n" {
		t.Fatalf("custom include changed: data=%q err=%v", data, err)
	}
}

func TestManagerSyncRejectsSiteOutsideProjects(t *testing.T) {
	f := newManagerFixture(t)
	outside := filepath.Join(f.root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	f.store.sites = []model.Site{{
		ID:           "outside",
		Name:         "outside",
		Domain:       "outside.test",
		Kind:         model.SiteStatic,
		HostPath:     outside,
		BackendPath:  outside,
		DocumentRoot: ".",
		DesiredState: model.DesiredRunning,
	}}
	err := f.mgr.Sync(context.Background(), ServerNginx)
	if err == nil {
		t.Fatal("expected outside-projects error")
	}
	if !strings.Contains(err.Error(), "outside") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagerSyncSkipsCertFailureForNonTLSSite(t *testing.T) {
	f := newManagerFixture(t)
	site := f.addStaticSite(t, "app", "app", "app")
	site.TLS = false
	f.store.sites[0] = site
	f.certs.err["app"] = errors.New("signer unavailable")
	if err := f.mgr.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("non-TLS site should still sync with the dashboard certificate: %v", err)
	}
	generated := filepath.Join(f.root, "services", "nginx", "config", "conf.d", "generated", "app.conf")
	data, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/certs/__dashboard__.crt") {
		t.Errorf("non-TLS site should fall back to the dashboard certificate\n%s", data)
	}
}

func TestManagerStatusReportsSuccessAndConflict(t *testing.T) {
	f := newManagerFixture(t)
	site := f.addStaticSite(t, "app", "app", "app")
	if err := f.mgr.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	status := f.mgr.Status()
	if !status.ConfigOK {
		t.Fatalf("status after successful sync = %+v, want ConfigOK", status)
	}
	if !status.LastSync.Equal(f.now) {
		t.Fatalf("LastSync = %v, want %v", status.LastSync, f.now)
	}
	if status.LastError != "" {
		t.Fatalf("LastError = %q, want empty", status.LastError)
	}

	generated := filepath.Join(f.root, "services", "nginx", "config", "conf.d", "generated", "app.conf")
	if err := os.WriteFile(generated, []byte("# local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.store.sites[0] = site
	f.store.sites[0].Domain = "changed.test"
	if err := f.mgr.Sync(context.Background(), ServerNginx); err == nil {
		t.Fatal("expected conflict")
	}
	status = f.mgr.Status()
	if status.ConfigOK {
		t.Fatalf("status after conflict = %+v, want ConfigOK=false", status)
	}
	if !strings.Contains(status.LastError, "app.conf") {
		t.Fatalf("LastError = %q, want the conflicted path", status.LastError)
	}
}

func TestManagerValidateReloadDelegates(t *testing.T) {
	f := newManagerFixture(t)
	if err := f.mgr.Validate(context.Background(), ServerApache); err != nil {
		t.Fatal(err)
	}
	if err := f.mgr.Reload(context.Background(), ServerApache); err != nil {
		t.Fatal(err)
	}
	if len(f.ctrl.validated) != 1 || f.ctrl.validated[0] != "apache" {
		t.Errorf("validated = %v", f.ctrl.validated)
	}
	if len(f.ctrl.reloaded) != 1 || f.ctrl.reloaded[0] != "apache" {
		t.Errorf("reloaded = %v", f.ctrl.reloaded)
	}
}
