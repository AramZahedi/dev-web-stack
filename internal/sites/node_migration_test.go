package sites

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// seedNodeLifecycleSite inserts a row directly so legacy line-valued pins can
// be represented without the create/update normalization under test elsewhere.
func seedNodeLifecycleSite(t *testing.T, fakes *testFakes, site model.Site) model.Site {
	t.Helper()
	if site.ID == "" {
		site.ID = "site-" + site.Name
	}
	if site.Domain == "" {
		site.Domain = site.Name + ".test"
	}
	if site.Backend == "" {
		site.Backend = model.BackendLocal
	}
	if site.Kind == "" {
		site.Kind = model.SiteStatic
	}
	if site.HostPath == "" {
		site.HostPath = fakes.root
	}
	if site.BackendPath == "" {
		site.BackendPath = site.HostPath
	}
	if site.SourceMode == "" {
		site.SourceMode = model.SourceLocal
	}
	if err := fakes.store.CreateSite(ctx, site); err != nil {
		t.Fatalf("seed site %q: %v", site.Name, err)
	}
	return site
}

func mustNodeLifecycleSite(t *testing.T, fakes *testFakes, id string) model.Site {
	t.Helper()
	site, err := fakes.store.GetSite(ctx, id)
	if err != nil {
		t.Fatalf("read site %q: %v", id, err)
	}
	return site
}

func findNodeNormalization(t *testing.T, results []NodeVersionNormalization, site, field, from string) NodeVersionNormalization {
	t.Helper()
	for _, result := range results {
		if result.Site == site && result.Field == field && result.From == from {
			return result
		}
	}
	t.Fatalf("normalization result for %s/%s from %q missing: %+v", site, field, from, results)
	return NodeVersionNormalization{}
}

func TestCheckRuntimeRemovalBlocksNodeBuildForEverySiteKind(t *testing.T) {
	svc, fakes := newTestService(t)
	build := installSiteNodeBuild(t, fakes, "24.22.1")
	other := installSiteNodeBuild(t, fakes, "22.11.0")

	seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "alpha-static", Kind: model.SiteStatic, NodeVersion: build.Version,
	})
	seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "beta-php", Kind: model.SitePHP, NodeVersion: "24",
	})
	seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "gamma-node", Kind: model.SiteNode, NodeVersion: "24.22", Command: "node server.js",
	})
	seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "delta-build", Kind: model.SiteStatic,
		Build: &model.BuildProfile{
			NodeVersion: "node@" + build.Version, Command: "npm run build", Output: "dist",
		},
	})
	seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "epsilon-other", Kind: model.SiteNode, NodeVersion: other.Version, Command: "node server.js",
	})
	seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "zeta-php-build", Kind: model.SitePHP,
		Build: &model.BuildProfile{NodeVersion: build.Version, Command: "npm run build"},
	})

	err := svc.CheckRuntimeRemoval(ctx, build.ID)
	if !errors.Is(err, ErrRuntimeInUse) {
		t.Fatalf("err = %v, want ErrRuntimeInUse", err)
	}
	var inUse *RuntimeInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("err = %T, want *RuntimeInUseError", err)
	}
	want := []string{"alpha-static", "beta-php", "delta-build", "gamma-node", "zeta-php-build"}
	if !reflect.DeepEqual(inUse.Sites, want) {
		t.Fatalf("sites = %v, want %v", inUse.Sites, want)
	}
	if inUse.BuildID != build.ID {
		t.Fatalf("build = %q, want %q", inUse.BuildID, build.ID)
	}
	for _, name := range want {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("in-use error %q does not name %q", err, name)
		}
	}
	if !strings.Contains(err.Error(), "Node runtime") || strings.Contains(err.Error(), "global default") {
		t.Fatalf("in-use message = %q, want Node runtime wording without a default", err)
	}
	if got := mustNodeLifecycleSite(t, fakes, "site-epsilon-other"); got.NodeVersion != other.Version {
		t.Fatalf("seed fixture changed: %+v", got)
	}
	if err := svc.CheckRuntimeRemoval(ctx, "node@25.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("absent build err = %v, want store.ErrNotFound", err)
	}
}

func TestCheckRuntimeRemovalBlocksGlobalNodeDefault(t *testing.T) {
	svc, fakes := newTestService(t)
	defaultBuild := installSiteNodeBuild(t, fakes, "22.11.0")
	other := installSiteNodeBuild(t, fakes, "20.18.0")

	if err := fakes.store.SetSetting(ctx, defaultNodeSettingKey, defaultBuild.ID); err != nil {
		t.Fatalf("set node default: %v", err)
	}
	err := svc.CheckRuntimeRemoval(ctx, defaultBuild.ID)
	if !errors.Is(err, ErrRuntimeInUse) {
		t.Fatalf("err = %v, want ErrRuntimeInUse", err)
	}
	var inUse *RuntimeInUseError
	if !errors.As(err, &inUse) || !reflect.DeepEqual(inUse.Sites, []string{"global default"}) {
		t.Fatalf("default in-use sites = %+v, want only the global default", inUse)
	}
	if !strings.Contains(err.Error(), "global default") {
		t.Fatalf("in-use message = %q, want the global default named", err)
	}
	if err := svc.CheckRuntimeRemoval(ctx, other.ID); err != nil {
		t.Fatalf("other build blocked by the default: %v", err)
	}

	// A line-valued default protects every installed build it can select until
	// it is cleared; no build is silently substituted.
	if err := fakes.store.SetSetting(ctx, defaultNodeSettingKey, "22"); err != nil {
		t.Fatalf("set line default: %v", err)
	}
	if err := svc.CheckRuntimeRemoval(ctx, defaultBuild.ID); !errors.Is(err, ErrRuntimeInUse) {
		t.Fatalf("line default did not block its build: %v", err)
	}

	seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "alpha", Kind: model.SiteStatic, NodeVersion: defaultBuild.Version,
	})
	err = svc.CheckRuntimeRemoval(ctx, defaultBuild.ID)
	if !errors.As(err, &inUse) || !reflect.DeepEqual(inUse.Sites, []string{"alpha", "global default"}) {
		t.Fatalf("combined in-use sites = %+v, want site then global default", inUse)
	}

	if err := fakes.store.DeleteSetting(ctx, defaultNodeSettingKey); err != nil {
		t.Fatalf("clear node default: %v", err)
	}
	if err := svc.CheckRuntimeRemoval(ctx, defaultBuild.ID); !errors.Is(err, ErrRuntimeInUse) {
		t.Fatalf("site pin stopped blocking after clearing the default: %v", err)
	}
	if err := svc.CheckRuntimeRemoval(ctx, other.ID); err != nil {
		t.Fatalf("cleared default still blocks an unrelated build: %v", err)
	}
}

func TestNormalizeNodeVersionsStoresExactInstalledBuilds(t *testing.T) {
	svc, fakes := newTestService(t)
	build20 := installSiteNodeBuild(t, fakes, "20.18.0")
	build22 := installSiteNodeBuild(t, fakes, "22.11.0")

	nodeSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "api", Kind: model.SiteNode, NodeVersion: "20", Command: "node server.js",
	})
	phpSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "shop", Kind: model.SitePHP, NodeVersion: "22.11",
	})
	buildSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "web", Kind: model.SiteStatic,
		Build: &model.BuildProfile{
			NodeVersion: "22", Command: "npm run build", Output: "dist",
			Env: map[string]string{"NODE_ENV": "production"},
		},
	})
	exactSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "docs", Kind: model.SiteStatic, NodeVersion: build20.Version,
	})

	results, err := svc.NormalizeNodeVersions(ctx)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v, want one entry per normalized pin", results)
	}
	nodeResult := findNodeNormalization(t, results, nodeSite.Name, "node_version", "20")
	if !nodeResult.Updated || nodeResult.To != build20.Version {
		t.Fatalf("node result = %+v", nodeResult)
	}
	phpResult := findNodeNormalization(t, results, phpSite.Name, "node_version", "22.11")
	if !phpResult.Updated || phpResult.To != build22.Version {
		t.Fatalf("php result = %+v", phpResult)
	}
	buildResult := findNodeNormalization(t, results, buildSite.Name, "build.node_version", "22")
	if !buildResult.Updated || buildResult.To != build22.Version {
		t.Fatalf("build result = %+v", buildResult)
	}

	if got := mustNodeLifecycleSite(t, fakes, nodeSite.ID); got.NodeVersion != build20.Version {
		t.Fatalf("node pin = %q, want %q", got.NodeVersion, build20.Version)
	}
	if got := mustNodeLifecycleSite(t, fakes, phpSite.ID); got.NodeVersion != build22.Version {
		t.Fatalf("php pin = %q, want %q", got.NodeVersion, build22.Version)
	}
	storedBuild := mustNodeLifecycleSite(t, fakes, buildSite.ID)
	if storedBuild.NodeVersion != "" || storedBuild.Build == nil ||
		storedBuild.Build.NodeVersion != build22.Version {
		t.Fatalf("build profile = %+v, want exact node_version", storedBuild)
	}
	if storedBuild.Build.Command != "npm run build" || storedBuild.Build.Output != "dist" ||
		storedBuild.Build.Env["NODE_ENV"] != "production" {
		t.Fatalf("normalization changed the build profile: %+v", storedBuild.Build)
	}
	if got := mustNodeLifecycleSite(t, fakes, exactSite.ID); got.NodeVersion != build20.Version {
		t.Fatalf("exact pin changed to %q", got.NodeVersion)
	}

	again, err := svc.NormalizeNodeVersions(ctx)
	if err != nil {
		t.Fatalf("second normalize: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second normalize results = %+v, want none", again)
	}
}

func TestNormalizeNodeVersionsLeavesUnresolvedValuesVisible(t *testing.T) {
	svc, fakes := newTestService(t)
	build := installSiteNodeBuild(t, fakes, "20.18.0")

	staticSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "web", Kind: model.SiteStatic, NodeVersion: "23",
	})
	phpSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "shop", Kind: model.SitePHP,
		Build: &model.BuildProfile{NodeVersion: "22", Command: "npm run build", Output: "dist"},
	})
	nodeSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "api", Kind: model.SiteNode, NodeVersion: "node@99.0.0", Command: "node server.js",
	})
	exactSite := seedNodeLifecycleSite(t, fakes, model.Site{
		Name: "docs", Kind: model.SiteStatic, NodeVersion: build.Version,
	})

	results, err := svc.NormalizeNodeVersions(ctx)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("results = %+v, want one entry per unresolved pin", results)
	}
	for _, result := range results {
		if result.Updated || result.To != "" {
			t.Fatalf("unresolved pin was rewritten: %+v", result)
		}
	}
	staticResult := findNodeNormalization(t, results, staticSite.Name, "node_version", "23")
	if staticResult.From != "23" {
		t.Fatalf("static result = %+v", staticResult)
	}
	findNodeNormalization(t, results, phpSite.Name, "build.node_version", "22")
	findNodeNormalization(t, results, nodeSite.Name, "node_version", "node@99.0.0")

	if got := mustNodeLifecycleSite(t, fakes, staticSite.ID); got.NodeVersion != "23" {
		t.Fatalf("static pin = %q, want the unresolved raw value", got.NodeVersion)
	}
	storedPHP := mustNodeLifecycleSite(t, fakes, phpSite.ID)
	if storedPHP.Build == nil || storedPHP.Build.NodeVersion != "22" {
		t.Fatalf("php build pin = %+v, want the unresolved raw value", storedPHP.Build)
	}
	if got := mustNodeLifecycleSite(t, fakes, nodeSite.ID); got.NodeVersion != "node@99.0.0" {
		t.Fatalf("node pin = %q, want the unresolved raw value", got.NodeVersion)
	}
	if got := mustNodeLifecycleSite(t, fakes, exactSite.ID); got.NodeVersion != build.Version {
		t.Fatalf("exact pin changed to %q", got.NodeVersion)
	}

	// Unresolved values stay actionable: the response summary reports the raw
	// pin as uninstalled instead of hiding or substituting it.
	site, err := svc.Get(ctx, staticSite.ID)
	if err != nil {
		t.Fatalf("get static site: %v", err)
	}
	if site.NodeToolchain == nil || site.NodeToolchain.Installed ||
		site.NodeToolchain.Version != "23" || site.NodeToolchain.Source != model.NodeSourceSite {
		t.Fatalf("unresolved summary = %+v, want the raw pin marked uninstalled", site.NodeToolchain)
	}
}
