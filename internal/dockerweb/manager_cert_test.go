package dockerweb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func TestManagerRenderedCertPathsMatchRealCertManagerFiles(t *testing.T) {
	root := t.TempDir()
	certsDir := filepath.Join(root, "certs")
	projects := filepath.Join(root, "projects", "app")
	if err := os.MkdirAll(certsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	signer := newTestCASigner(t)
	certManager := certs.NewManager(st, signer, certsDir, nil)

	storeSites := &fakeSiteStore{sites: []model.Site{{
		ID:           "app",
		Name:         "app",
		Domain:       "app.test",
		Kind:         model.SiteStatic,
		HostPath:     projects,
		BackendPath:  projects,
		DocumentRoot: ".",
		DesiredState: model.DesiredRunning,
		TLS:          true,
	}}}
	manager, err := NewManager(ManagerConfig{
		Root:         root,
		Store:        storeSites,
		Certs:        certManager,
		Dashboard:    DashboardSpec{Domain: "devstack.test", UIPort: 7070},
		MaxBodyBytes: 64 << 20,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := manager.Sync(context.Background(), ServerNginx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	generated := filepath.Join(root, "services", "nginx", "config", "conf.d", "generated")
	siteConf, err := os.ReadFile(filepath.Join(generated, "app.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/certs/app.crt", "/certs/app.key"} {
		if !strings.Contains(string(siteConf), want) {
			t.Errorf("site vhost missing %q", want)
		}
	}
	dashboardConf, err := os.ReadFile(filepath.Join(generated, "_devstack.test.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/certs/__dashboard__.crt", "/certs/__dashboard__.key"} {
		if !strings.Contains(string(dashboardConf), want) {
			t.Errorf("dashboard vhost missing %q", want)
		}
	}
	for _, name := range []string{"app.crt", "app.key", "__dashboard__.crt", "__dashboard__.key"} {
		if _, err := os.Stat(filepath.Join(certsDir, name)); err != nil {
			t.Errorf("real cert manager did not write %s: %v", name, err)
		}
	}
}
