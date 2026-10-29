//go:build !windows

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

type fakeToolStatusProvider struct {
	status helperproto.ToolStatusResponse
	err    error
}

func (f fakeToolStatusProvider) ToolStatus(context.Context) (helperproto.ToolStatusResponse, error) {
	return f.status, f.err
}

func TestResolveToolBinDirUsesHelperWrapperPath(t *testing.T) {
	dir := resolveToolBinDir(context.Background(), fakeToolStatusProvider{
		status: helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{
			{ID: helperproto.ToolComposer, WrapperPath: "/opt/bin/composer"},
			// Installed/Managed remain false: the configured bin directory is
			// still known before the wrapper exists.
			{ID: helperproto.ToolPHPDefault, WrapperPath: "/opt/bin/php"},
			{ID: helperproto.ToolNodeDefault, WrapperPath: "/node/bin/node"},
		}},
	})
	if dir != "/opt/bin" {
		t.Fatalf("dir = %q, want /opt/bin", dir)
	}
}

func TestResolveToolBinDirFallsBackToNodeWrapper(t *testing.T) {
	dir := resolveToolBinDir(context.Background(), fakeToolStatusProvider{
		status: helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{
			{ID: helperproto.ToolPHPDefault},
			{ID: helperproto.ToolNodeDefault, WrapperPath: "/node/bin/node"},
		}},
	})
	if dir != "/node/bin" {
		t.Fatalf("dir = %q, want /node/bin", dir)
	}
}

func TestResolveToolBinDirDefaultsWhenUnavailable(t *testing.T) {
	for _, provider := range []fakeToolStatusProvider{
		{err: errors.New("helper unavailable")},
		{status: helperproto.ToolStatusResponse{}},
		{status: helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{{ID: helperproto.ToolComposer}}}},
	} {
		if got := resolveToolBinDir(context.Background(), provider); got != "/usr/local/bin" {
			t.Fatalf("dir = %q, want default /usr/local/bin", got)
		}
	}
}

func TestWindowsToolServiceManagesNodeShims(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	st, err := store.Open(paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	buildRoot := filepath.Join(paths.Runtimes, "node@22.11.0")
	if err := os.MkdirAll(filepath.Join(buildRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if err := os.WriteFile(filepath.Join(buildRoot, "bin", name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	build := model.RuntimeBuild{
		ID:          "node@22.11.0",
		Kind:        "node",
		Version:     "22.11.0",
		Backend:     model.BackendLocal,
		Path:        buildRoot,
		InstalledAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
	if err := st.UpsertRuntime(ctx, build); err != nil {
		t.Fatal(err)
	}

	svc := newWindowsToolService(home, "devstack", st)
	status, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.ID != helperproto.ToolNodeDefault || !status.Managed || status.Installed || status.Conflict != "" {
		t.Fatalf("initial node shim status = %+v", status)
	}
	if status.Target != filepath.Join(buildRoot, "bin", "node") {
		t.Fatalf("target = %q, want %q", status.Target, filepath.Join(buildRoot, "bin", "node"))
	}

	binDir := filepath.Join(home, "bin")
	foreignNPM := filepath.Join(binDir, "npm.cmd")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignNPM, []byte("@echo off\r\nrem foreign\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(ctx, false); !errors.Is(err, helperproto.ErrToolConflict) {
		t.Fatalf("install without force error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(binDir, "node.cmd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("install without force wrote node.cmd: %v", err)
	}
	if data, err := os.ReadFile(foreignNPM); err != nil || !strings.Contains(string(data), "foreign") {
		t.Fatalf("foreign npm.cmd changed: %q, %v", data, err)
	}

	installed, err := svc.Install(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !installed.Installed || !installed.Managed {
		t.Fatalf("installed status = %+v", installed)
	}
	if installed.Backup == "" {
		t.Fatal("install did not back up the foreign npm.cmd")
	}
	if data, err := os.ReadFile(installed.Backup); err != nil || !strings.Contains(string(data), "foreign") {
		t.Fatalf("backup content = %q, %v", data, err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		shim := filepath.Join(binDir, name+".cmd")
		info, err := os.Lstat(shim)
		if err != nil {
			t.Fatalf("stat %s.cmd: %v", name, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("%s.cmd is not a regular file", name)
		}
		data, err := os.ReadFile(shim)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "wsl.exe -d devstack --exec") {
			t.Fatalf("%s.cmd does not use the managed distro:\n%s", name, text)
		}
		want := filepath.ToSlash(filepath.Join(buildRoot, "bin", name))
		if !strings.Contains(text, want) {
			t.Fatalf("%s.cmd does not use the exact build %s:\n%s", name, want, text)
		}
		if !strings.Contains(text, `\"${args[@]}\"`) || !strings.Contains(text, "%*") {
			t.Fatalf("%s.cmd does not forward argv:\n%s", name, text)
		}
	}

	if _, err := svc.Install(ctx, false); err != nil {
		t.Fatalf("idempotent reinstall: %v", err)
	}

	// Switching the default leaves devstack-owned shims that must be reported
	// stale and repointed without a conflict.
	nextRoot := filepath.Join(paths.Runtimes, "node@24.0.0")
	if err := os.MkdirAll(filepath.Join(nextRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if err := os.WriteFile(filepath.Join(nextRoot, "bin", name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	next := model.RuntimeBuild{
		ID:          "node@24.0.0",
		Kind:        "node",
		Version:     "24.0.0",
		Backend:     model.BackendLocal,
		Path:        nextRoot,
		InstalledAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}
	if err := st.UpsertRuntime(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, tooling.DefaultNodeSettingKey, "node@24.0.0"); err != nil {
		t.Fatal(err)
	}
	stale, err := svc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Managed || stale.Installed || stale.Conflict != "" {
		t.Fatalf("stale shim status = %+v", stale)
	}
	if _, err := svc.Install(ctx, false); err != nil {
		t.Fatalf("install after default change: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(binDir, "node.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.ToSlash(filepath.Join(nextRoot, "bin", "node"))) {
		t.Fatalf("node.cmd was not repointed at the new default:\n%s", data)
	}

	removed, err := svc.Remove(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Installed {
		t.Fatalf("status after remove = %+v", removed)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if _, err := os.Lstat(filepath.Join(binDir, name+".cmd")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s.cmd remains: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(buildRoot, "bin", "node")); err != nil {
		t.Fatalf("runtime build was removed: %v", err)
	}

	// A configured-but-uninstalled default is a clear error and never falls
	// back to the newest installed build.
	if err := st.SetSetting(ctx, tooling.DefaultNodeSettingKey, "node@99.99.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(ctx, false); err == nil {
		t.Fatal("install accepted a configured-but-uninstalled default")
	}
	for _, name := range []string{"node", "npm", "npx"} {
		if _, err := os.Lstat(filepath.Join(binDir, name+".cmd")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s.cmd was installed for a missing default: %v", name, err)
		}
	}
}
