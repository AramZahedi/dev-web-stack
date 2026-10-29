package sites

import (
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
	if _, err := svc.ResolveNodeToolchain(ctx, model.Site{
		Kind: model.SiteStatic, Name: "missing", NodeVersion: "99.0.0",
	}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("uninstalled pin error = %v", err)
	}
}

func TestSiteUpdateRequiresExplicitNodeVersion(t *testing.T) {
	svc, fakes := newTestService(t)
	build := installSiteNodeBuild(t, fakes, "22.11.0")
	path, sourceMode := fakes.root, "local"
	if runtime.GOOS == "windows" {
		path, sourceMode = "/home/aram/api", "wsl"
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
