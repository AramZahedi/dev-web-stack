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
