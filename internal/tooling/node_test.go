package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestResolveNodeDefaultUsesExplicitThenNewest(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	dir := t.TempDir()

	// PHP builds must never be considered by the Node default resolver.
	runtimeRow(t, st, "php@8.4.12-r1", "php", "8.4.12", filepath.Join(dir, "php"))
	runtimeRow(t, st, "node@22.10.0", "node", "22.10.0", filepath.Join(dir, "22.10.0"))
	runtimeRow(t, st, "node@22.11.0", "node", "22.11.0", filepath.Join(dir, "22.11.0"))
	runtimeRow(t, st, "node@24.1.0", "node", "24.1.0", filepath.Join(dir, "24.1.0"))

	for _, tc := range []struct {
		name    string
		setting string
		wantID  string
	}{
		{name: "exact build id", setting: "node@22.10.0", wantID: "node@22.10.0"},
		{name: "exact version", setting: "22.10.0", wantID: "node@22.10.0"},
		{name: "major line", setting: "22", wantID: "node@22.11.0"},
		{name: "minor line", setting: "22.11", wantID: "node@22.11.0"},
		{name: "newest fallback", setting: "", wantID: "node@24.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := st.SetSetting(ctx, DefaultNodeSettingKey, tc.setting); err != nil {
				t.Fatal(err)
			}
			build, err := ResolveNodeDefault(ctx, st)
			if err != nil {
				t.Fatal(err)
			}
			if build.ID != tc.wantID {
				t.Fatalf("build = %s, want %s", build.ID, tc.wantID)
			}
		})
	}

	// Installing a newer build must never replace an explicit default.
	if err := st.SetSetting(ctx, DefaultNodeSettingKey, "node@22.10.0"); err != nil {
		t.Fatal(err)
	}
	runtimeRow(t, st, "node@24.2.0", "node", "24.2.0", filepath.Join(dir, "24.2.0"))
	build, err := ResolveNodeDefault(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if build.ID != "node@22.10.0" {
		t.Fatalf("install replaced the configured default: %s", build.ID)
	}
}

func TestResolveNodeDefaultRejectsUninstalledBuild(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	dir := t.TempDir()
	runtimeRow(t, st, "node@22.11.0", "node", "22.11.0", filepath.Join(dir, "node"))

	for _, tc := range []struct {
		name    string
		setting string
		wantErr error
	}{
		{name: "uninstalled build id", setting: "node@22.99.0", wantErr: ErrNodeDefaultNotInstalled},
		{name: "uninstalled version", setting: "22.99.0", wantErr: ErrNodeDefaultNotInstalled},
		{name: "uninstalled major", setting: "24", wantErr: ErrNodeDefaultNotInstalled},
		{name: "invalid value", setting: "not-a-build", wantErr: ErrUnsupportedNodeDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := st.SetSetting(ctx, DefaultNodeSettingKey, tc.setting); err != nil {
				t.Fatal(err)
			}
			if _, err := ResolveNodeDefault(ctx, st); !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	empty := testStore(t)
	if _, err := ResolveNodeDefault(ctx, empty); !errors.Is(err, ErrNoNodeInstalled) {
		t.Fatalf("empty store error = %v", err)
	}
	if _, err := ResolveNodeDefault(ctx, nil); err == nil {
		t.Fatal("nil store was accepted")
	}
}

func TestEnsureNodeShimsWritesNodeNpmNpx(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	root := filepath.Join(home, "runtimes", "node@22.11.0")
	for _, name := range []string{"node", "npm", "npx"} {
		writeTestScript(t, filepath.Join(root, "bin", name), "#!/bin/sh\nexit 0\n", 0o755)
	}
	build := model.RuntimeBuild{
		ID:      "node@22.11.0",
		Kind:    "node",
		Version: "22.11.0",
		Backend: model.BackendLocal,
		Path:    root,
	}

	if err := EnsureNodeShims(paths, build); err != nil {
		t.Fatal(err)
	}

	dir := NodeDefaultShimDir(paths)
	if want := filepath.Join(home, "tools", "node-default", "bin"); dir != want {
		t.Fatalf("NodeDefaultShimDir = %q, want %q", dir, want)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		wrapper := filepath.Join(dir, name)
		info, err := os.Stat(wrapper)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if runtime.GOOS != "windows" {
			if perm := info.Mode().Perm(); perm != 0o755 {
				t.Fatalf("%s mode = %04o, want 0755", name, perm)
			}
		}
		data, err := os.ReadFile(wrapper)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		wantBinary := filepath.ToSlash(filepath.Join(root, "bin", name))
		if !strings.Contains(text, wantBinary) {
			t.Fatalf("%s does not execute the exact build binary %s:\n%s", name, wantBinary, text)
		}
		if strings.Contains(text, wrapper) {
			t.Fatalf("%s recurses into its own shim:\n%s", name, text)
		}
		if !strings.HasPrefix(text, "#!/bin/sh\n") || !strings.Contains(text, `"$@"`) {
			t.Fatalf("%s is not a safe POSIX wrapper:\n%s", name, text)
		}
	}

	version, err := os.ReadFile(filepath.Join(home, "tools", "node-default", "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(version)) != build.ID {
		t.Fatalf("VERSION = %q, want %q", version, build.ID)
	}

	// Re-running is deterministic and leaves no temporary files behind.
	if err := EnsureNodeShims(paths, build); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}

func TestNodeDefaultStatusReportsSourceAndShim(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	home := t.TempDir()
	paths := config.NewPaths(home)
	dir := t.TempDir()
	runtimeRow(t, st, "node@22.11.0", "node", "22.11.0", filepath.Join(dir, "22.11.0"))
	runtimeRow(t, st, "node@24.1.0", "node", "24.1.0", filepath.Join(dir, "24.1.0"))

	status, err := ResolveNodeDefaultStatus(ctx, st, paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.Build == nil || status.Build.ID != "node@24.1.0" {
		t.Fatalf("fallback build = %+v", status.Build)
	}
	if status.Source != NodeDefaultSourceNewest {
		t.Fatalf("source = %q, want %q", status.Source, NodeDefaultSourceNewest)
	}
	if status.Configured != "" {
		t.Fatalf("configured = %q, want empty", status.Configured)
	}
	if status.ShimInstalled {
		t.Fatal("shim reported installed before it was written")
	}
	if runtime.GOOS == "windows" {
		if want := filepath.Join(home, "bin"); status.ShimDir != want {
			t.Fatalf("ShimDir = %q, want %q", status.ShimDir, want)
		}
	} else if want := NodeDefaultShimDir(paths); status.ShimDir != want {
		t.Fatalf("ShimDir = %q, want %q", status.ShimDir, want)
	}

	if err := st.SetSetting(ctx, DefaultNodeSettingKey, "node@22.11.0"); err != nil {
		t.Fatal(err)
	}
	status, err = ResolveNodeDefaultStatus(ctx, st, paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.Build == nil || status.Build.ID != "node@22.11.0" {
		t.Fatalf("configured build = %+v", status.Build)
	}
	if status.Source != NodeDefaultSourceSetting {
		t.Fatalf("source = %q, want %q", status.Source, NodeDefaultSourceSetting)
	}
	if status.Configured != "node@22.11.0" {
		t.Fatalf("configured = %q", status.Configured)
	}

	shimPath := filepath.Join(status.ShimDir, "node")
	if runtime.GOOS == "windows" {
		shimPath += ".cmd"
	}
	if err := os.MkdirAll(filepath.Dir(shimPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shimPath, []byte("@echo off\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = ResolveNodeDefaultStatus(ctx, st, paths)
	if err != nil {
		t.Fatal(err)
	}
	if !status.ShimInstalled {
		t.Fatal("shim was not reported installed")
	}

	// A default that is no longer installed keeps the configured value visible
	// instead of silently falling back to another build.
	if err := st.SetSetting(ctx, DefaultNodeSettingKey, "node@22.99.0"); err != nil {
		t.Fatal(err)
	}
	status, err = ResolveNodeDefaultStatus(ctx, st, paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.Build != nil {
		t.Fatalf("uninstalled default resolved to %+v", status.Build)
	}
	if status.Configured != "node@22.99.0" || status.Source != NodeDefaultSourceSetting {
		t.Fatalf("configured = %q source = %q", status.Configured, status.Source)
	}
}

func TestNodeDefaultEnvironmentUsesExactVersionAndSource(t *testing.T) {
	build := model.RuntimeBuild{ID: "node@22.11.0", Kind: "node", Version: "22.11.0"}
	env := NodeDefaultEnvironment(&build)
	if env["DEVSTACK_NODE_VERSION"] != "22.11.0" {
		t.Fatalf("DEVSTACK_NODE_VERSION = %q", env["DEVSTACK_NODE_VERSION"])
	}
	if env["DEVSTACK_NODE_SOURCE"] != "global" {
		t.Fatalf("DEVSTACK_NODE_SOURCE = %q", env["DEVSTACK_NODE_SOURCE"])
	}
	if env := NodeDefaultEnvironment(nil); len(env) != 0 {
		t.Fatalf("nil build environment = %v", env)
	}
}

func TestEnsureNodeShimsRejectsUnsafeBuild(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if err := EnsureNodeShims(paths, model.RuntimeBuild{Kind: "node", Version: "22.11.0", Path: "relative/path"}); err == nil {
		t.Fatal("relative build path was accepted")
	}
	if err := EnsureNodeShims(paths, model.RuntimeBuild{Kind: "php", Version: "8.4.12", Path: t.TempDir()}); err == nil {
		t.Fatal("PHP build was accepted as a Node build")
	}
}
