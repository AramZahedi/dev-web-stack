package tooling

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
)

func TestBuildSiteEnvUsesSitePinThenGlobal(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	pinnedBuild := fakeNodeBuild(t, home, "20.18.0")
	globalBuild := fakeNodeBuild(t, home, "22.11.0")
	phpBuild := fakePHPBuild(t, home, "8.4.12")
	project := t.TempDir()
	phpSite := model.Site{
		ID: "p1", Name: "app", HostPath: project, Kind: model.SitePHP,
		PHPVersion: "8.4", PHPBuild: phpBuild.ID,
		PHPConfigHash: strings.Repeat("d", 64),
	}
	phpInfo := fakeSitePHPInfo(t, paths, phpSite, phpBuild)

	cases := []struct {
		name        string
		site        model.Site
		nodeDefault *model.RuntimeBuild
		want        model.RuntimeBuild
		source      string
	}{
		{
			name:   "node site pin",
			site:   model.Site{ID: "n1", Name: "api", HostPath: project, Kind: model.SiteNode, NodeVersion: pinnedBuild.Version},
			want:   pinnedBuild,
			source: "site",
		},
		{
			name:   "static site pin",
			site:   model.Site{ID: "s1", Name: "docs", HostPath: project, Kind: model.SiteStatic, NodeVersion: pinnedBuild.Version},
			want:   pinnedBuild,
			source: "site",
		},
		{
			name:   "static inherits global",
			site:   model.Site{ID: "s2", Name: "site", HostPath: project, Kind: model.SiteStatic},
			want:   globalBuild,
			source: "global",
		},
		{
			name:   "php inherits global",
			site:   phpSite,
			want:   globalBuild,
			source: "global",
		},
		{
			name:        "configured older default wins over newest",
			site:        model.Site{ID: "s3", Name: "legacy", HostPath: project, Kind: model.SiteStatic},
			nodeDefault: &pinnedBuild,
			want:        pinnedBuild,
			source:      "global",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			selected := globalBuild
			if tc.nodeDefault != nil {
				selected = *tc.nodeDefault
			}
			env, err := BuildSiteEnv(tc.site, EnvDeps{
				Paths:       paths,
				Builds:      []model.RuntimeBuild{pinnedBuild, globalBuild, phpBuild},
				PHPInfo:     &phpInfo,
				NodeDefault: &selected,
				BasePATH:    "/usr/bin:/bin",
			})
			if err != nil {
				t.Fatalf("BuildSiteEnv: %v", err)
			}
			if env.NodeBuild == nil || env.NodeBuild.ID != tc.want.ID {
				t.Fatalf("node build = %+v, want %s", env.NodeBuild, tc.want.ID)
			}
			if env.Values["DEVSTACK_NODE_VERSION"] != tc.want.Version {
				t.Fatalf("DEVSTACK_NODE_VERSION = %q", env.Values["DEVSTACK_NODE_VERSION"])
			}
			if env.Values["DEVSTACK_NODE_SOURCE"] != tc.source {
				t.Fatalf("DEVSTACK_NODE_SOURCE = %q, want %q", env.Values["DEVSTACK_NODE_SOURCE"], tc.source)
			}
		})
	}
}

func TestBuildSiteEnvFailsWithoutResolvedNodeDefault(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	project := t.TempDir()
	phpBuild := fakePHPBuild(t, home, "8.4.12")
	phpSite := model.Site{
		ID: "p1", Name: "app", HostPath: project, Kind: model.SitePHP,
		PHPVersion: "8.4", PHPBuild: phpBuild.ID,
		PHPConfigHash: strings.Repeat("e", 64),
	}
	phpInfo := fakeSitePHPInfo(t, paths, phpSite, phpBuild)

	for _, tc := range []struct {
		name  string
		site  model.Site
		build []model.RuntimeBuild
		info  *sites.PHPInfo
	}{
		{
			name: "static without any node",
			site: model.Site{ID: "s1", Name: "docs", HostPath: project, Kind: model.SiteStatic},
		},
		{
			name: "static with installed node but no default",
			site: model.Site{ID: "s2", Name: "site", HostPath: project, Kind: model.SiteStatic},
			build: []model.RuntimeBuild{
				fakeNodeBuild(t, home, "22.11.0"),
			},
		},
		{
			name: "php without any node",
			site: phpSite,
			build: []model.RuntimeBuild{
				phpBuild,
			},
			info: &phpInfo,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildSiteEnv(tc.site, EnvDeps{
				Paths:    paths,
				Builds:   tc.build,
				PHPInfo:  tc.info,
				BasePATH: "/usr/bin:/bin",
			})
			if !errors.Is(err, ErrRuntimeMissing) {
				t.Fatalf("BuildSiteEnv error = %v, want ErrRuntimeMissing", err)
			}
		})
	}
}

func TestStaticBuildProfileDoesNotOverrideShellNode(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	buildNode := fakeNodeBuild(t, home, "20.18.0")
	globalNode := fakeNodeBuild(t, home, "22.11.0")
	project := t.TempDir()
	site := model.Site{
		ID: "s1", Name: "web", HostPath: project, Kind: model.SiteStatic,
		Build: &model.BuildProfile{
			NodeVersion: buildNode.Version,
			Command:     "npm run build",
			Output:      "dist",
		},
	}

	global := globalNode
	env, err := BuildSiteEnv(site, EnvDeps{
		Paths:       paths,
		Builds:      []model.RuntimeBuild{buildNode, globalNode},
		NodeDefault: &global,
		BasePATH:    "/usr/bin:/bin",
	})
	if err != nil {
		t.Fatalf("BuildSiteEnv: %v", err)
	}
	if env.NodeBuild == nil || env.NodeBuild.ID != globalNode.ID {
		t.Fatalf("shell node build = %+v, want global %s", env.NodeBuild, globalNode.ID)
	}
	if env.Values["DEVSTACK_NODE_VERSION"] != globalNode.Version {
		t.Fatalf("DEVSTACK_NODE_VERSION = %q", env.Values["DEVSTACK_NODE_VERSION"])
	}
	shim, err := os.ReadFile(filepath.Join(env.ShimDir, "node"))
	if err != nil {
		t.Fatalf("read node shim: %v", err)
	}
	if !strings.Contains(string(shim), globalNode.Path) || strings.Contains(string(shim), buildNode.Path) {
		t.Fatalf("node shim does not use the global build:\n%s", shim)
	}

	// Without a resolved default the shell fails closed; the build profile is
	// never promoted to a shell setting.
	if _, err := BuildSiteEnv(site, EnvDeps{
		Paths:    paths,
		Builds:   []model.RuntimeBuild{buildNode, globalNode},
		BasePATH: "/usr/bin:/bin",
	}); !errors.Is(err, ErrRuntimeMissing) {
		t.Fatalf("BuildSiteEnv without a configured default error = %v, want ErrRuntimeMissing", err)
	}
}
