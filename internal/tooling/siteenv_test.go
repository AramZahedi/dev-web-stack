package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

func fakeNodeBuild(t *testing.T, home, version string) model.RuntimeBuild {
	t.Helper()
	root := filepath.Join(home, "runtimes", "node", version+"-linux-x64")
	for _, name := range []string{"node", "npm", "npx"} {
		writeTestScript(t, filepath.Join(root, "bin", name), "#!/bin/sh\nexit 0\n", 0o755)
	}
	return model.RuntimeBuild{
		ID:      "node@" + version,
		Kind:    "node",
		Version: version,
		Backend: model.BackendLocal,
		Path:    root,
	}
}

func fakeSitePHPInfo(t *testing.T, paths config.Paths, site model.Site, build model.RuntimeBuild) sites.PHPInfo {
	t.Helper()
	generation := filepath.Join(paths.Sites, site.ID, site.PHPConfigHash)
	if err := os.MkdirAll(generation, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(generation, "php.ini")
	if err := os.WriteFile(ini, []byte("[PHP]\nmemory_limit = 512M\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return sites.PHPInfo{
		Site:         site,
		Build:        build,
		GeneratedIni: "[PHP]\nmemory_limit = 512M\n",
		ConfigHash:   site.PHPConfigHash,
	}
}

func TestResolveSiteByCWDAndExplicitOverride(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "app")
	if err := os.MkdirAll(filepath.Join(project, "src", "pages"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "projects", "shop")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	sitesList := []model.Site{
		{ID: "one", Name: "app", HostPath: project},
		{ID: "two", Name: "shop", HostPath: other},
	}

	got, err := ResolveSite(sitesList, "", filepath.Join(project, "src", "pages"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "app" {
		t.Fatalf("resolved %q", got.Name)
	}

	got, err = ResolveSite(sitesList, "shop", project)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "shop" {
		t.Fatalf("explicit override resolved %q", got.Name)
	}
}

func TestResolveSiteErrorsWhenMissingOrAmbiguous(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(filepath.Join(shared, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	sitesList := []model.Site{
		{ID: "one", Name: "app", HostPath: shared},
		{ID: "two", Name: "shop", HostPath: shared},
	}

	_, err := ResolveSite(sitesList, "", filepath.Join(shared, "sub"))
	if err == nil || !strings.Contains(err.Error(), "--site") || !strings.Contains(err.Error(), "app") || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("ambiguous error = %v", err)
	}

	_, err = ResolveSite(sitesList, "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "app") || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("unregistered error = %v", err)
	}

	if _, err := ResolveSite(sitesList, "missing", ""); err == nil {
		t.Fatal("unknown --site accepted")
	}
}

func TestBuildSiteEnvPHP(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	phpBuild := fakePHPBuild(t, home, "8.4.12")
	nodeBuild := fakeNodeBuild(t, home, "22.11.0")
	project := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	site := model.Site{
		ID:            "site-1",
		Name:          "app",
		HostPath:      project,
		Kind:          model.SitePHP,
		PHPVersion:    "8.4",
		PHPBuild:      phpBuild.ID,
		PHPConfigHash: strings.Repeat("a", 64),
		Env:           map[string]string{"APP_ENV": "local", "PATH": "/evil", "PHPRC": "/evil"},
	}
	info := fakeSitePHPInfo(t, paths, site, phpBuild)

	env, err := BuildSiteEnv(site, EnvDeps{
		Paths:    paths,
		Builds:   []model.RuntimeBuild{phpBuild, nodeBuild},
		PHPInfo:  &info,
		BasePATH: "/usr/bin:/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantIni := filepath.Join(paths.Sites, site.ID, site.PHPConfigHash, "php.ini")
	if env.PHPIni != wantIni || env.Values["PHPRC"] != wantIni {
		t.Fatalf("PHPRC = %q, want %q", env.Values["PHPRC"], wantIni)
	}
	if env.Values["DEVSTACK_SITE"] != "app" || env.Values["DEVSTACK_HOME"] != home || env.Values["DEVSTACK_PROJECT"] != project {
		t.Fatalf("devstack env = %+v", env.Values)
	}
	if env.ShimDir != filepath.Join(paths.Tools, "sites", "app", "bin") {
		t.Fatalf("site shim dir = %q, want the DEVSTACK_SITE name", env.ShimDir)
	}
	if env.Values["APP_ENV"] != "local" {
		t.Fatalf("site env missing: %+v", env.Values)
	}
	if !strings.HasPrefix(env.PATH, SiteShimDir(paths, site.Name)+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q", env.PATH)
	}
	if strings.Contains(env.PATH, "/evil") {
		t.Fatalf("site env overwrote PATH: %q", env.PATH)
	}
	phpShim := filepath.Join(SiteShimDir(paths, site.Name), "php")
	if _, err := os.Stat(phpShim); err != nil {
		t.Fatalf("site php shim missing: %v", err)
	}
	script, err := os.ReadFile(phpShim)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), filepath.Join(phpBuild.Path, "bin", "php")) || !strings.Contains(string(script), wantIni) {
		t.Fatalf("site php shim:\n%s", script)
	}
}

func TestBuildSiteEnvNodeAndStaticBuild(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	nodeBuild := fakeNodeBuild(t, home, "22.11.0")
	project := filepath.Join(home, "projects", "web")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		site model.Site
	}{
		{
			name: "node site",
			site: model.Site{ID: "n1", Name: "node-app", HostPath: project, Kind: model.SiteNode, NodeVersion: "22.11.0"},
		},
		{
			name: "static build line",
			site: model.Site{ID: "s1", Name: "static-app", HostPath: project, Kind: model.SiteStatic,
				Build: &model.BuildProfile{NodeVersion: "22", Command: "npm run build", Output: "dist"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := BuildSiteEnv(tc.site, EnvDeps{
				Paths:    paths,
				Builds:   []model.RuntimeBuild{nodeBuild},
				BasePATH: "/usr/bin:/bin",
			})
			if err != nil {
				t.Fatal(err)
			}
			if env.NodeBuild == nil || env.NodeBuild.ID != nodeBuild.ID {
				t.Fatalf("node build = %+v", env.NodeBuild)
			}
			if !strings.HasPrefix(env.PATH, SiteShimDir(paths, tc.site.Name)+string(os.PathListSeparator)) {
				t.Fatalf("PATH = %q", env.PATH)
			}
			for _, name := range []string{"node", "npm", "npx"} {
				shim := filepath.Join(SiteShimDir(paths, tc.site.Name), name)
				data, err := os.ReadFile(shim)
				if err != nil {
					t.Fatalf("%s shim: %v", name, err)
				}
				if !strings.Contains(string(data), filepath.Join(nodeBuild.Path, "bin", name)) {
					t.Fatalf("%s shim:\n%s", name, data)
				}
			}
		})
	}
}

func TestBuildSiteEnvRequiresInstalledRuntimesAndConfig(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	project := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	phpSite := model.Site{ID: "p1", Name: "php", HostPath: project, Kind: model.SitePHP,
		PHPVersion: "8.4", PHPBuild: "php@8.4.12-r1", PHPConfigHash: strings.Repeat("b", 64)}
	if _, err := BuildSiteEnv(phpSite, EnvDeps{Paths: paths, BasePATH: "/bin"}); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("missing php error = %v", err)
	}

	nodeSite := model.Site{ID: "n1", Name: "node", HostPath: project, Kind: model.SiteNode, NodeVersion: "22.11.0"}
	if _, err := BuildSiteEnv(nodeSite, EnvDeps{Paths: paths, BasePATH: "/bin"}); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("missing node error = %v", err)
	}

	phpBuild := fakePHPBuild(t, home, "8.4.12")
	phpSite.PHPBuild = phpBuild.ID
	if _, err := BuildSiteEnv(phpSite, EnvDeps{Paths: paths, Builds: []model.RuntimeBuild{phpBuild}, BasePATH: "/bin"}); err == nil {
		t.Fatal("missing site ini accepted")
	}
}

func TestBuildSiteEnvRejectsShellUnsafeEnvironmentNames(t *testing.T) {
	site := model.Site{
		ID: "site1", Name: "app", Kind: model.SiteStatic, HostPath: t.TempDir(),
		Env: map[string]string{"EVIL; touch /tmp/pwned": "x"},
	}
	_, err := BuildSiteEnv(site, EnvDeps{Paths: config.NewPaths(t.TempDir())})
	if err == nil {
		t.Fatal("BuildSiteEnv accepted a shell-unsafe environment name")
	}
	if !strings.Contains(err.Error(), "invalid environment variable name") {
		t.Fatalf("error = %v", err)
	}
}

func TestShellExportsSkipsInvalidEnvironmentNames(t *testing.T) {
	exports := ShellExports(SiteEnvironment{Values: map[string]string{
		"GOOD":              "value",
		"BAD; touch /tmp/x": "value",
	}})
	if !strings.Contains(exports, "export GOOD='value'") {
		t.Fatalf("valid export missing: %q", exports)
	}
	if strings.Contains(exports, "BAD") {
		t.Fatalf("invalid export was rendered: %q", exports)
	}
}

func TestEnvironmentAndShellExports(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/user", "KEEP=yes"}
	values := map[string]string{"PATH": "/shim:/usr/bin", "PHPRC": "/tmp/a b/php.ini"}
	env := Environment(base, values)
	got := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", entry)
		}
		got[key] = value
	}
	if got["PATH"] != "/shim:/usr/bin" || got["PHPRC"] != "/tmp/a b/php.ini" || got["KEEP"] != "yes" || got["HOME"] != "/home/user" {
		t.Fatalf("env = %+v", got)
	}
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("PATH appears %d times", count)
	}

	exports := ShellExports(SiteEnvironment{Values: map[string]string{
		"DEVSTACK_SITE": "app",
		"PATH":          "/shim:/usr/bin",
		"PHPRC":         "/tmp/it's here/php.ini",
	}})
	for _, want := range []string{
		"export DEVSTACK_SITE='app'",
		"export PATH='/shim:/usr/bin'",
		`export PHPRC='/tmp/it'\''s here/php.ini'`,
	} {
		if !strings.Contains(exports, want) {
			t.Fatalf("exports missing %q:\n%s", want, exports)
		}
	}
}

func TestBuildSiteEnvRejectsRoot(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	project := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	site := model.Site{ID: "n1", Name: "node", HostPath: project, Kind: model.SiteNode, NodeVersion: "22.11.0"}
	old := currentEUID
	currentEUID = func() int { return 0 }
	t.Cleanup(func() { currentEUID = old })
	if _, err := BuildSiteEnv(site, EnvDeps{Paths: paths, BasePATH: "/bin"}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("root error = %v", err)
	}
}

func TestSiteShimCleansStaleTools(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	builder := fakePHPBuild(t, home, "8.4.12")
	project := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	site := model.Site{ID: "p1", Name: "app", HostPath: project, Kind: model.SitePHP,
		PHPVersion: "8.4", PHPBuild: builder.ID, PHPConfigHash: strings.Repeat("c", 64)}
	info := fakeSitePHPInfo(t, paths, site, builder)
	if _, err := BuildSiteEnv(site, EnvDeps{Paths: paths, Builds: []model.RuntimeBuild{builder}, PHPInfo: &info, BasePATH: "/bin"}); err != nil {
		t.Fatal(err)
	}
	// A PHP-only site must not leave stale node shims from a removed build
	// profile.
	stale := filepath.Join(SiteShimDir(paths, site.Name), "node")
	writeTestScript(t, stale, "#!/bin/sh\nexit 0\n", 0o755)
	if _, err := BuildSiteEnv(site, EnvDeps{Paths: paths, Builds: []model.RuntimeBuild{builder}, PHPInfo: &info, BasePATH: "/bin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale node shim remains: %v", err)
	}
}

func TestResolveSiteMatchesID(t *testing.T) {
	list := []model.Site{{ID: "abc", Name: "app", HostPath: "/tmp/app"}}
	got, err := ResolveSite(list, "abc", "")
	if err != nil || got.Name != "app" {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

var _ = context.Background
