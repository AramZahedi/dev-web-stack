package sites

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// createNodeToolchainSite creates a site in tests that also build on Windows,
// where the shared Unix-only service test helpers are not compiled.
func createNodeToolchainSite(t *testing.T, svc *Service, req CreateRequest) model.Site {
	t.Helper()
	site, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	return site
}

func getNodeToolchainSite(t *testing.T, svc *Service, ref string) model.Site {
	t.Helper()
	site, err := svc.Get(ctx, ref)
	if err != nil {
		t.Fatalf("get site: %v", err)
	}
	return site
}

// installSiteNodeBuild registers an installed Node build so the site service can
// resolve and validate exact versions.
func installSiteNodeBuild(t *testing.T, fakes *testFakes, version string) model.RuntimeBuild {
	t.Helper()
	build := model.RuntimeBuild{
		ID:          "node@" + version,
		Kind:        "node",
		Version:     version,
		Backend:     model.BackendLocal,
		Path:        "/runtimes/node@" + version,
		InstalledAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := fakes.store.UpsertRuntime(ctx, build); err != nil {
		t.Fatalf("install node %s: %v", version, err)
	}
	return build
}

func TestResolveNodeToolchainPrefersSiteThenGlobal(t *testing.T) {
	svc, fakes := newTestService(t)
	siteBuild := installSiteNodeBuild(t, fakes, "20.18.0")
	globalBuild := installSiteNodeBuild(t, fakes, "22.11.0")
	if err := fakes.store.SetSetting(ctx, "node_default", globalBuild.ID); err != nil {
		t.Fatalf("set node default: %v", err)
	}

	pinned := model.Site{Kind: model.SiteNode, Name: "api", NodeVersion: siteBuild.Version}
	got, err := svc.ResolveNodeToolchain(ctx, pinned)
	if err != nil {
		t.Fatalf("resolve pinned toolchain: %v", err)
	}
	if got.Version != siteBuild.Version || got.BuildID != siteBuild.ID ||
		got.Source != model.NodeSourceSite || !got.Installed {
		t.Fatalf("pinned toolchain = %+v", got)
	}

	inherited := model.Site{Kind: model.SiteStatic, Name: "web"}
	got, err = svc.ResolveNodeToolchain(ctx, inherited)
	if err != nil {
		t.Fatalf("resolve inherited toolchain: %v", err)
	}
	if got.Version != globalBuild.Version || got.BuildID != globalBuild.ID ||
		got.Source != model.NodeSourceGlobal || !got.Installed {
		t.Fatalf("inherited toolchain = %+v", got)
	}

	if _, err := svc.ResolveNodeToolchain(ctx, model.Site{Kind: model.SiteNode, Name: "empty"}); err == nil {
		t.Fatal("node site without a pin resolved a toolchain")
	}
	missing, err := svc.ResolveNodeToolchain(ctx, model.Site{
		Kind: model.SiteStatic, Name: "missing", NodeVersion: "99.0.0",
	})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("uninstalled pin error = %v", err)
	}
	if missing.Installed || missing.Version != "99.0.0" || missing.Source != model.NodeSourceSite {
		t.Fatalf("uninstalled pin summary = %+v", missing)
	}
}

func TestCreateNodeSiteUsesInstalledDiscoveryBuildWithoutCatalogInstall(t *testing.T) {
	svc, fakes := newTestService(t)
	build := installSiteNodeBuild(t, fakes, "22.11.0")
	site := createNodeToolchainSite(t, svc, CreateRequest{
		Name:        "api",
		Path:        fakes.root,
		Kind:        "node",
		NodeVersion: build.Version,
		Command:     "node server.js",
		SourceMode:  "windows",
	})
	if site.NodeVersion != build.Version {
		t.Fatalf("site node version = %q, want %q", site.NodeVersion, build.Version)
	}
	if len(fakes.backend.runtimeCalls) != 0 {
		t.Fatalf("node site creation used the embedded catalog installer: %v", fakes.backend.runtimeCalls)
	}
}

func TestBackendSpecResolvesInheritedNodeDefault(t *testing.T) {
	svc, fakes := newTestService(t)
	older := installSiteNodeBuild(t, fakes, "20.18.0")
	installSiteNodeBuild(t, fakes, "22.11.0")
	if err := fakes.store.SetSetting(ctx, "node_default", older.ID); err != nil {
		t.Fatalf("set node default: %v", err)
	}

	for _, kind := range []model.SiteKind{model.SiteStatic, model.SitePHP} {
		spec := svc.backendSpecFor(ctx, model.Site{ID: string(kind), Kind: kind})
		if spec.RuntimeVersion != older.Version {
			t.Fatalf("%s backend runtime = %q, want configured default %q", kind, spec.RuntimeVersion, older.Version)
		}
	}
}

func TestServiceResponseIncludesNodeToolchainSummary(t *testing.T) {
	svc, fakes := newTestService(t)
	installSiteNodeBuild(t, fakes, "20.18.0")
	globalBuild := installSiteNodeBuild(t, fakes, "22.11.0")
	if err := fakes.store.SetSetting(ctx, "node_default", globalBuild.ID); err != nil {
		t.Fatalf("set node default: %v", err)
	}

	created := createNodeToolchainSite(t, svc, CreateRequest{Name: "web", Path: fakes.root, Kind: "static"})
	if created.NodeToolchain == nil || created.NodeToolchain.Version != globalBuild.Version ||
		created.NodeToolchain.Source != model.NodeSourceGlobal || !created.NodeToolchain.Installed {
		t.Fatalf("created node toolchain = %+v", created.NodeToolchain)
	}
	fetched := getNodeToolchainSite(t, svc, created.ID)
	if fetched.NodeToolchain == nil || fetched.NodeToolchain.Version != globalBuild.Version {
		t.Fatalf("fetched node toolchain = %+v", fetched.NodeToolchain)
	}

	stored, err := fakes.store.GetSite(ctx, created.ID)
	if err != nil {
		t.Fatalf("read stored site: %v", err)
	}
	stored.NodeVersion = "99.0.0"
	if err := fakes.store.UpdateSite(ctx, stored); err != nil {
		t.Fatalf("persist stale pin: %v", err)
	}
	stale := getNodeToolchainSite(t, svc, created.ID)
	if stale.NodeToolchain == nil {
		t.Fatal("stale pin dropped its node_toolchain summary")
	}
	if stale.NodeToolchain.Installed || stale.NodeToolchain.Version != "99.0.0" ||
		stale.NodeToolchain.Source != model.NodeSourceSite {
		t.Fatalf("stale node toolchain = %+v", stale.NodeToolchain)
	}
	if _, err := svc.ResolveNodeToolchain(ctx, stale); !errors.Is(err, ErrNodeVersionNotInstalled) {
		t.Fatalf("stale resolver error = %v, want ErrNodeVersionNotInstalled", err)
	}
}

func TestSiteUpdateRequiresExplicitNodeVersion(t *testing.T) {
	svc, fakes := newTestService(t)
	build := installSiteNodeBuild(t, fakes, "22.11.0")
	path, sourceMode := fakes.root, "local"
	if runtime.GOOS == "windows" {
		path, sourceMode = t.TempDir(), "windows"
	}
	site := createNodeToolchainSite(t, svc, CreateRequest{
		Name: "api", Path: path, Kind: "node", SourceMode: sourceMode,
		NodeVersion: build.Version, Command: "node server.js",
	})

	clear := ""
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{NodeVersion: &clear}); err == nil ||
		!strings.Contains(err.Error(), "requires a version") {
		t.Fatalf("clearing a node pin error = %v", err)
	}
	got := getNodeToolchainSite(t, svc, site.ID)
	if got.NodeVersion != build.Version {
		t.Fatalf("rejected update changed node pin to %q", got.NodeVersion)
	}

	if _, err := svc.Update(ctx, site.ID, UpdateRequest{}); err != nil {
		t.Fatalf("omitted node pin update: %v", err)
	}
	got = getNodeToolchainSite(t, svc, site.ID)
	if got.NodeVersion != build.Version {
		t.Fatalf("omitted update changed node pin to %q", got.NodeVersion)
	}
}

func TestStaticSiteCanClearNodeVersionToInherit(t *testing.T) {
	svc, fakes := newTestService(t)
	siteBuild := installSiteNodeBuild(t, fakes, "20.18.0")
	globalBuild := installSiteNodeBuild(t, fakes, "22.11.0")
	if err := fakes.store.SetSetting(ctx, "node_default", globalBuild.ID); err != nil {
		t.Fatalf("set node default: %v", err)
	}

	site := createNodeToolchainSite(t, svc, CreateRequest{
		Name: "web", Path: fakes.root, Kind: "static", NodeVersion: "20",
	})
	if site.NodeVersion != siteBuild.Version {
		t.Fatalf("created static node pin = %q, want exact %q", site.NodeVersion, siteBuild.Version)
	}

	clear := ""
	updated, err := svc.Update(ctx, site.ID, UpdateRequest{NodeVersion: &clear})
	if err != nil {
		t.Fatalf("clear static node pin: %v", err)
	}
	if updated.NodeVersion != "" {
		t.Fatalf("cleared static node pin = %q", updated.NodeVersion)
	}
	toolchain, err := svc.ResolveNodeToolchain(ctx, updated)
	if err != nil {
		t.Fatalf("resolve cleared static toolchain: %v", err)
	}
	if toolchain.Version != globalBuild.Version || toolchain.Source != model.NodeSourceGlobal {
		t.Fatalf("cleared static toolchain = %+v", toolchain)
	}

	missing := "99.0.0"
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{NodeVersion: &missing}); err == nil ||
		!strings.Contains(err.Error(), "not installed") {
		t.Fatalf("uninstalled static pin error = %v", err)
	}
}
