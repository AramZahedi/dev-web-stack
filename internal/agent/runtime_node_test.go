package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// sitePathEnv must put the resolved exact Node build first for every site kind.
// An empty RuntimeVersion means the daemon did not resolve a build, so the
// agent must not invent a node@<empty> directory or pick a build on its own.
func TestAgentSitePathEnvUsesResolvedNode(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	for _, version := range []string{"22.11.0", "24.1.0"} {
		bin := filepath.Join(root, "runtimes", "node@"+version, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		runtime.runtimes["node@"+version] = agentproto.RuntimeResponse{
			ID: "node@" + version, Kind: "node", Version: version, Path: filepath.Dir(bin),
		}
	}

	for _, tc := range []struct {
		name    string
		spec    agentproto.SiteSpec
		version string
	}{
		{name: "static", spec: agentproto.SiteSpec{Kind: "static", RuntimeVersion: "22.11.0"}, version: "22.11.0"},
		{name: "php", spec: agentproto.SiteSpec{Kind: "php", RuntimeVersion: "22.11.0"}, version: "22.11.0"},
		{name: "node", spec: agentproto.SiteSpec{Kind: "node", RuntimeVersion: "24.1.0"}, version: "24.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runtime.sitePathEnv(tc.spec, "/usr/bin:/bin")
			wantBin := filepath.Join(root, "runtimes", "node@"+tc.version, "bin")
			if !strings.HasPrefix(got, wantBin+":") {
				t.Fatalf("sitePathEnv = %q, want exact Node bin %q first", got, wantBin)
			}
		})
	}

	got := runtime.sitePathEnv(agentproto.SiteSpec{Kind: "static"}, "/usr/bin:/bin")
	if want := "/usr/bin:/bin"; got != want {
		t.Fatalf("sitePathEnv without a resolved Node = %q, want %q", got, want)
	}
}
