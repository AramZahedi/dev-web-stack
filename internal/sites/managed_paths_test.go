package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/pathmode"
)

// managedTestMode is the platform's valid source mode for a static site test.
func managedTestMode() model.SourceMode {
	if runtime.GOOS == "windows" {
		return model.SourceWindows
	}
	return model.SourceLocal
}

// managedTestRoot returns the managed projects root used by newTestService. It
// does not depend on the fixture root so the rejection tests can create their
// own children.
func managedTestRoot(t *testing.T, fakes *testFakes) string {
	t.Helper()
	if err := os.MkdirAll(fakes.projectsRoot, 0o755); err != nil {
		t.Fatalf("create projects root: %v", err)
	}
	return fakes.projectsRoot
}

// externalTestPath returns an existing-site path outside every managed root.
func externalTestPath(t *testing.T) (host, backend string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `C:\devstack-outside\legacy`, "/mnt/c/devstack-outside/legacy"
	}
	outside := filepath.Join(t.TempDir(), "legacy")
	return outside, outside
}

func seedExternalSite(t *testing.T, fakes *testFakes) model.Site {
	t.Helper()
	host, backend := externalTestPath(t)
	site := model.Site{
		ID:           "external",
		Name:         "external",
		Domain:       "external.test",
		Backend:      model.BackendLocal,
		Kind:         model.SiteStatic,
		SourceMode:   managedTestMode(),
		HostPath:     host,
		BackendPath:  backend,
		Status:       model.StatusStopped,
		DesiredState: model.DesiredStopped,
	}
	if err := fakes.store.CreateSite(ctx, site); err != nil {
		t.Fatalf("seed external site: %v", err)
	}
	return site
}

func TestCreateRejectsProjectsRootItself(t *testing.T) {
	svc, fakes := newTestService(t)
	root := managedTestRoot(t, fakes)

	_, err := svc.Create(ctx, CreateRequest{
		Name: "root", Path: root, Kind: "static", SourceMode: string(managedTestMode()),
	})
	if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
		t.Fatalf("Create(root) error = %v, want ErrOutsideProjectsRoot", err)
	}
	if sites, err := fakes.store.ListSites(ctx); err != nil {
		t.Fatal(err)
	} else if len(sites) != 0 {
		t.Fatalf("site persisted after rejecting the projects root: %+v", sites)
	}
}

func TestCreateRejectsSiblingOfProjectsRoot(t *testing.T) {
	svc, fakes := newTestService(t)
	root := managedTestRoot(t, fakes)
	sibling := filepath.Join(filepath.Dir(root), "projects-old", "blog")

	_, err := svc.Create(ctx, CreateRequest{
		Name: "sibling", Path: sibling, Kind: "static", SourceMode: string(managedTestMode()),
	})
	if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
		t.Fatalf("Create(sibling) error = %v, want ErrOutsideProjectsRoot", err)
	}
}

func TestCreateRejectsRootAlias(t *testing.T) {
	svc, fakes := newTestService(t)
	root := managedTestRoot(t, fakes)
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}

	_, err := svc.Create(ctx, CreateRequest{
		Name: "alias", Path: alias, Kind: "static", SourceMode: string(managedTestMode()),
	})
	if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
		t.Fatalf("Create(root alias) error = %v, want ErrOutsideProjectsRoot", err)
	}
	if sites, err := fakes.store.ListSites(ctx); err != nil {
		t.Fatal(err)
	} else if len(sites) != 0 {
		t.Fatalf("site persisted after rejecting the root alias: %+v", sites)
	}
}

func TestCreateAcceptsChildOfProjectsRoot(t *testing.T) {
	svc, fakes := newTestService(t)
	root := managedTestRoot(t, fakes)
	child := filepath.Join(root, "blog")

	site, err := svc.Create(ctx, CreateRequest{
		Name: "blog", Path: child, Kind: "static", SourceMode: string(managedTestMode()),
	})
	if err != nil {
		t.Fatalf("Create(child): %v", err)
	}
	if !pathmode.ContainsPath(root, site.HostPath, runtime.GOOS == "windows") {
		t.Fatalf("site host path %q is not inside %q", site.HostPath, root)
	}
}

func TestCreateCreatesMissingLocalPHPDocumentRoot(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	root := managedTestRoot(t, fakes)
	project := filepath.Join(root, "php-app")

	site, err := svc.Create(ctx, CreateRequest{
		Name:         "php-app",
		Path:         project,
		Kind:         "php",
		PHPVersion:   "8.4",
		DocumentRoot: "public",
	})
	if err != nil {
		t.Fatalf("Create(php child): %v", err)
	}
	if site.DocumentRoot != "public" {
		t.Fatalf("document root = %q, want public", site.DocumentRoot)
	}
	for _, path := range []string{project, filepath.Join(project, "public")} {
		if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
			t.Fatalf("%s = %v, %v", path, info, statErr)
		}
	}
}

func TestCreateAcceptsWindowsCaseVariant(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows case-insensitive containment")
	}
	svc, fakes := newTestService(t)
	root := managedTestRoot(t, fakes)
	variant := strings.ToUpper(filepath.Join(root, "CaseSite"))

	if _, err := svc.Create(ctx, CreateRequest{
		Name: "cased", Path: variant, Kind: "static", SourceMode: string(model.SourceWindows),
	}); err != nil {
		t.Fatalf("Create(case variant): %v", err)
	}
}

func TestCreateRejectsSymlinkEscape(t *testing.T) {
	svc, fakes := newTestService(t)
	root := managedTestRoot(t, fakes)
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks are unavailable on this host: %v", err)
	}

	_, err := svc.Create(ctx, CreateRequest{
		Name: "escape", Path: filepath.Join(link, "site"), Kind: "static", SourceMode: string(managedTestMode()),
	})
	if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
		t.Fatalf("Create(symlink escape) error = %v, want ErrOutsideProjectsRoot", err)
	}
}

func TestStartRejectsExternalSiteBeforeBackend(t *testing.T) {
	svc, fakes := newTestService(t)
	site := seedExternalSite(t, fakes)

	guardCalls := 0
	svc.deps.DatabaseGuard = func(context.Context, model.Site) error {
		guardCalls++
		return nil
	}

	err := svc.Start(ctx, site.ID)
	if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
		t.Fatalf("Start error = %v, want ErrOutsideProjectsRoot", err)
	}
	if guardCalls != 0 {
		t.Fatalf("database guard called %d times before path validation", guardCalls)
	}
	if fakes.backend.genericPrepareCalls != 0 || fakes.backend.startCount() != 0 {
		t.Fatalf("backend prepared=%d started=%d for an external site", fakes.backend.genericPrepareCalls, fakes.backend.startCount())
	}
}

func TestRestartRejectsExternalSiteBeforeBackend(t *testing.T) {
	svc, fakes := newTestService(t)
	site := seedExternalSite(t, fakes)

	err := svc.Restart(ctx, site.ID)
	if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
		t.Fatalf("Restart error = %v, want ErrOutsideProjectsRoot", err)
	}
	if fakes.backend.genericPrepareCalls != 0 || fakes.backend.startCount() != 0 {
		t.Fatalf("backend prepared=%d started=%d for an external site", fakes.backend.genericPrepareCalls, fakes.backend.startCount())
	}
}

func TestReconcileRejectsExternalSiteBeforeBackend(t *testing.T) {
	svc, fakes := newTestService(t)
	site := seedExternalSite(t, fakes)
	site.DesiredState = model.DesiredRunning
	if err := fakes.store.UpdateSite(ctx, site); err != nil {
		t.Fatal(err)
	}

	err := svc.startStaticBackendsLocked(ctx)
	if !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
		t.Fatalf("startStaticBackendsLocked error = %v, want ErrOutsideProjectsRoot", err)
	}
	if fakes.backend.genericPrepareCalls != 0 || fakes.backend.startCount() != 0 {
		t.Fatalf("backend prepared=%d started=%d for an external site", fakes.backend.genericPrepareCalls, fakes.backend.startCount())
	}
}

func TestValidateManagedSitePathInfersLegacyModeFromPath(t *testing.T) {
	svc, fakes := newTestService(t)
	root := managedTestRoot(t, fakes)

	t.Run("static wsl row without a mode", func(t *testing.T) {
		site := model.Site{
			Name:        "legacy-wsl",
			Kind:        model.SiteStatic,
			HostPath:    `\\wsl.localhost\devstack\data\devstack\projects\legacy-wsl`,
			BackendPath: "/data/devstack/projects/legacy-wsl",
		}
		if err := svc.validateManagedSitePath(site); err != nil {
			t.Fatalf("legacy WSL static row was rejected: %v", err)
		}
	})

	t.Run("php windows row without a mode", func(t *testing.T) {
		child := filepath.Join(root, "legacy-php")
		site := model.Site{
			Name:        "legacy-php",
			Kind:        model.SitePHP,
			HostPath:    child,
			BackendPath: child,
		}
		if err := svc.validateManagedSitePath(site); err != nil {
			t.Fatalf("legacy Windows PHP row was rejected: %v", err)
		}
	})

	t.Run("external row without a mode is still rejected", func(t *testing.T) {
		host, backend := externalTestPath(t)
		site := model.Site{Name: "legacy-external", Kind: model.SiteStatic, HostPath: host, BackendPath: backend}
		if err := svc.validateManagedSitePath(site); !errors.Is(err, pathmode.ErrOutsideProjectsRoot) {
			t.Fatalf("err = %v, want ErrOutsideProjectsRoot", err)
		}
	})
}
