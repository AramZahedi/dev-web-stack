package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
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
		name   string
		site   model.Site
		want   model.RuntimeBuild
		source string
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global := globalBuild
			env, err := BuildSiteEnv(tc.site, EnvDeps{
				Paths:       paths,
				Builds:      []model.RuntimeBuild{pinnedBuild, globalBuild, phpBuild},
				PHPInfo:     &phpInfo,
				NodeDefault: &global,
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

	// With no configured global default, the newest installed Node still wins
	// over the build profile because the build profile is not a shell setting.
	env, err = BuildSiteEnv(site, EnvDeps{
		Paths:    paths,
		Builds:   []model.RuntimeBuild{buildNode, globalNode},
		BasePATH: "/usr/bin:/bin",
	})
	if err != nil {
		t.Fatalf("BuildSiteEnv without a configured default: %v", err)
	}
	if env.NodeBuild == nil || env.NodeBuild.ID != globalNode.ID {
		t.Fatalf("shell node build without a default = %+v, want newest %s", env.NodeBuild, globalNode.ID)
	}
}
