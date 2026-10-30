package tooling

import (
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestWindowsSiteEnvironmentUsesExactResolvedBuild(t *testing.T) {
	site := model.Site{
		Name:        "api",
		BackendPath: "/mnt/c/projects/api",
		NodeVersion: "24.22.1",
		Build:       &model.BuildProfile{NodeVersion: "20"},
	}
	env := WindowsSiteEnvironmentForNode(site, "22.11.0")
	path := env["PATH"]
	exactBin := "/data/devstack/runtimes/node@22.11.0/bin"
	if !strings.Contains(path, exactBin) {
		t.Fatalf("PATH = %q, missing exact resolved Node build %q", path, exactBin)
	}
	for _, stale := range []string{
		"/data/devstack/runtimes/node@24.22.1/bin",
		"/data/devstack/runtimes/node@20/bin",
	} {
		if strings.Contains(path, stale) {
			t.Fatalf("PATH = %q contains a stale Node build %q", path, stale)
		}
	}
	if env["DEVSTACK_NODE_VERSION"] != "22.11.0" {
		t.Fatalf("DEVSTACK_NODE_VERSION = %q, want the exact resolved build", env["DEVSTACK_NODE_VERSION"])
	}

	prefixed := WindowsSiteEnvironmentForNode(site, "node@22.11.0")
	if !strings.Contains(prefixed["PATH"], exactBin) {
		t.Fatalf("node@-prefixed exact build was not normalized: %q", prefixed["PATH"])
	}

	raw := WindowsSiteEnvironmentForNode(site, "22")
	if strings.Contains(raw["PATH"], "/data/devstack/runtimes/node@22/bin") {
		t.Fatalf("raw version line produced a runtime path: %q", raw["PATH"])
	}
	if raw["DEVSTACK_NODE_VERSION"] != "" {
		t.Fatalf("raw version line was reported as exact: %q", raw["DEVSTACK_NODE_VERSION"])
	}
}

func TestWindowsSiteEnvironmentDoesNotUseBuildNodeVersion(t *testing.T) {
	site := model.Site{
		Name:        "docs",
		BackendPath: "/mnt/c/projects/docs",
		NodeVersion: "24.22.1",
		Build:       &model.BuildProfile{NodeVersion: "22"},
	}

	env := WindowsSiteEnvironmentForNode(site, "")
	if strings.Contains(env["PATH"], "/data/devstack/runtimes/node@") {
		t.Fatalf("exact environment invented a Node runtime from the build profile: %q", env["PATH"])
	}
	if _, ok := env["DEVSTACK_NODE_VERSION"]; ok {
		t.Fatalf("exact environment reported a Node version without a resolved build: %q", env["DEVSTACK_NODE_VERSION"])
	}

	// The legacy wrapper keeps its pre-resolution behavior, but an explicit
	// site pin still wins over build.node_version.
	legacy := WindowsSiteEnvironment(site)
	if !strings.Contains(legacy["PATH"], "/data/devstack/runtimes/node@24.22.1/bin") {
		t.Fatalf("legacy PATH = %q, missing the site Node pin", legacy["PATH"])
	}
	if strings.Contains(legacy["PATH"], "/data/devstack/runtimes/node@22/bin") {
		t.Fatalf("legacy PATH = %q used build.node_version over the site pin", legacy["PATH"])
	}
}
