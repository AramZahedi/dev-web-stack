package tooling

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// nodeDefaultTestHelper records the helper calls the Node default service makes
// and mirrors the Windows shim side effect so status is meaningful on every
// host platform.
type nodeDefaultTestHelper struct {
	paths      config.Paths
	conflict   bool
	installErr error
	installs   []helperproto.ToolInstallRequest
	removes    []string
	onInstall  func(id string)
}

func (h *nodeDefaultTestHelper) ToolStatus(context.Context) (helperproto.ToolStatusResponse, error) {
	status := helperproto.ToolStatus{
		ID:          helperproto.ToolNodeDefault,
		WrapperPath: "/usr/local/bin/node",
	}
	if h.conflict {
		status.Conflict = "a foreign file occupies /usr/local/bin/node"
	}
	return helperproto.ToolStatusResponse{Tools: []helperproto.ToolStatus{status}}, nil
}

func (h *nodeDefaultTestHelper) ToolInstall(_ context.Context, id string, force bool) (helperproto.ToolStatus, error) {
	h.installs = append(h.installs, helperproto.ToolInstallRequest{ID: id, Force: force})
	if h.onInstall != nil {
		h.onInstall(id)
	}
	if h.conflict && !force {
		return helperproto.ToolStatus{}, helperproto.ErrToolConflict
	}
	if h.installErr != nil {
		return helperproto.ToolStatus{}, h.installErr
	}
	if err := h.writeWindowsShims(true); err != nil {
		return helperproto.ToolStatus{}, err
	}
	return helperproto.ToolStatus{
		ID:          id,
		Installed:   true,
		Managed:     true,
		WrapperPath: "/usr/local/bin/node",
	}, nil
}

func (h *nodeDefaultTestHelper) ToolRemove(_ context.Context, id string) (helperproto.ToolStatus, error) {
	h.removes = append(h.removes, id)
	if err := h.writeWindowsShims(false); err != nil {
		return helperproto.ToolStatus{}, err
	}
	return helperproto.ToolStatus{ID: id}, nil
}

func (h *nodeDefaultTestHelper) writeWindowsShims(installed bool) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	dir := WindowsNodeShimDir(h.paths)
	for _, name := range []string{"node", "npm", "npx"} {
		path := filepath.Join(dir, name+".cmd")
		if !installed {
			_ = os.Remove(path)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("@echo off\r\n"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func fakeNodeDefaultBuild(t *testing.T, home, version string) model.RuntimeBuild {
	t.Helper()
	root := filepath.Join(home, "runtimes", "node@"+version)
	for _, name := range []string{"node", "npm", "npx"} {
		writeTestScript(t, filepath.Join(root, "bin", name), "#!/bin/sh\nexit 0\n", 0o755)
	}
	return model.RuntimeBuild{
		ID:          "node@" + version,
		Kind:        "node",
		Version:     version,
		Backend:     model.BackendLocal,
		Path:        root,
		InstalledAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
	}
}

func newNodeDefaultService(t *testing.T, versions ...string) (*Service, *store.Store, config.Paths, []model.RuntimeBuild, *nodeDefaultTestHelper) {
	t.Helper()
	home := t.TempDir()
	st := testStore(t)
	paths := config.NewPaths(home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	builds := make([]model.RuntimeBuild, 0, len(versions))
	for _, version := range versions {
		build := fakeNodeDefaultBuild(t, home, version)
		if err := st.UpsertRuntime(context.Background(), build); err != nil {
			t.Fatalf("upsert runtime %s: %v", build.ID, err)
		}
		builds = append(builds, build)
	}
	helper := &nodeDefaultTestHelper{paths: paths}
	svc := NewService(Deps{Store: st, Paths: paths, Helper: helper})
	return svc, st, paths, builds, helper
}

func readNodeDefaultVersion(t *testing.T, paths config.Paths) string {
	t.Helper()
	data, err := os.ReadFile(NodeDefaultVersionPath(paths))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestNodeDefaultServiceSetClearAndShims(t *testing.T) {
	svc, st, paths, builds, helper := newNodeDefaultService(t, "22.10.0", "24.1.0")
	ctx := context.Background()

	status, err := svc.SetNodeDefault(ctx, "  22.10.0  ", false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured != "22.10.0" || status.Source != NodeDefaultSourceSetting {
		t.Fatalf("status = %+v", status)
	}
	if status.Build == nil || status.Build.ID != builds[0].ID {
		t.Fatalf("build = %+v, want %s", status.Build, builds[0].ID)
	}
	if !status.ShimInstalled {
		t.Fatal("set did not report an installed shim")
	}
	stored, err := st.GetSetting(ctx, DefaultNodeSettingKey)
	if err != nil || stored != "22.10.0" {
		t.Fatalf("setting = %q err=%v", stored, err)
	}
	if got := readNodeDefaultVersion(t, paths); got != builds[0].ID {
		t.Fatalf("VERSION = %q, want %q", got, builds[0].ID)
	}
	for _, name := range []string{"node", "npm", "npx"} {
		shim := filepath.Join(NodeDefaultShimDir(paths), name)
		data, err := os.ReadFile(shim)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(data), filepath.ToSlash(filepath.Join(builds[0].Path, "bin", name))) {
			t.Fatalf("%s does not execute the selected build:\n%s", name, data)
		}
	}
	if len(helper.installs) != 1 || helper.installs[0].ID != helperproto.ToolNodeDefault || helper.installs[0].Force {
		t.Fatalf("helper installs = %+v", helper.installs)
	}

	status, err = svc.ClearNodeDefault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured != "" || status.Source != NodeDefaultSourceNewest {
		t.Fatalf("clear status = %+v", status)
	}
	if status.Build == nil || status.Build.ID != builds[1].ID {
		t.Fatalf("clear build = %+v, want newest %s", status.Build, builds[1].ID)
	}
	if !status.ShimInstalled {
		t.Fatal("clear did not report the repointed shim")
	}
	if _, err := st.GetSetting(ctx, DefaultNodeSettingKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("setting remains after clear: %v", err)
	}
	if got := readNodeDefaultVersion(t, paths); got != builds[1].ID {
		t.Fatalf("VERSION after clear = %q, want %q", got, builds[1].ID)
	}
	if len(helper.installs) != 2 || helper.installs[1].ID != helperproto.ToolNodeDefault || helper.installs[1].Force {
		t.Fatalf("helper installs after clear = %+v", helper.installs)
	}
}

func TestNodeDefaultSetInstallsHelperWrappers(t *testing.T) {
	svc, st, paths, builds, helper := newNodeDefaultService(t, "22.11.0", "24.1.0")
	ctx := context.Background()

	var settingAtInstall string
	var versionAtInstall string
	helper.onInstall = func(id string) {
		if id != helperproto.ToolNodeDefault {
			return
		}
		settingAtInstall, _ = st.GetSetting(ctx, DefaultNodeSettingKey)
		versionAtInstall = readNodeDefaultVersion(t, paths)
	}

	status, err := svc.SetNodeDefault(ctx, "node@22.11.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if status.Build == nil || status.Build.ID != builds[0].ID {
		t.Fatalf("status = %+v", status)
	}
	if len(helper.installs) != 1 || helper.installs[0].ID != helperproto.ToolNodeDefault || !helper.installs[0].Force {
		t.Fatalf("helper installs = %+v", helper.installs)
	}
	if settingAtInstall != "node@22.11.0" || versionAtInstall != builds[0].ID {
		t.Fatalf("helper observed setting=%q VERSION=%q", settingAtInstall, versionAtInstall)
	}

	// A foreign wrapper stops the mutation before the setting, VERSION, or any
	// managed shim is written.
	conflictSvc, conflictStore, conflictPaths, _, conflictHelper := newNodeDefaultService(t, "22.11.0")
	conflictHelper.conflict = true
	if _, err := conflictSvc.SetNodeDefault(ctx, "22.11.0", false); !errors.Is(err, helperproto.ErrToolConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	if _, err := conflictStore.GetSetting(ctx, DefaultNodeSettingKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("setting persisted despite conflict: %v", err)
	}
	if _, err := os.Stat(NodeDefaultVersionPath(conflictPaths)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("VERSION written despite conflict: %v", err)
	}
	if len(conflictHelper.installs) != 0 {
		t.Fatalf("helper install ran despite conflict: %+v", conflictHelper.installs)
	}

	// --force passes through to the helper and replaces the foreign wrapper.
	if _, err := conflictSvc.SetNodeDefault(ctx, "22.11.0", true); err != nil {
		t.Fatal(err)
	}
	if len(conflictHelper.installs) != 1 || !conflictHelper.installs[0].Force {
		t.Fatalf("forced helper installs = %+v", conflictHelper.installs)
	}

	before := len(helper.installs)
	if _, err := svc.SetNodeDefault(ctx, "20", false); !errors.Is(err, ErrNodeDefaultNotInstalled) {
		t.Fatalf("uninstalled default error = %v", err)
	}
	if len(helper.installs) != before {
		t.Fatalf("uninstalled default reached the helper: %+v", helper.installs)
	}
}

func TestNodeDefaultClearReResolvesNewest(t *testing.T) {
	svc, st, paths, builds, helper := newNodeDefaultService(t, "22.10.0", "24.1.0")
	ctx := context.Background()
	if err := st.SetSetting(ctx, DefaultNodeSettingKey, "node@22.10.0"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureNodeShims(paths, builds[0]); err != nil {
		t.Fatal(err)
	}
	helper.installs = nil
	helper.removes = nil

	var settingErrAtInstall error
	var versionAtInstall string
	helper.onInstall = func(id string) {
		if id != helperproto.ToolNodeDefault {
			return
		}
		_, settingErrAtInstall = st.GetSetting(ctx, DefaultNodeSettingKey)
		versionAtInstall = readNodeDefaultVersion(t, paths)
	}

	status, err := svc.ClearNodeDefault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured != "" || status.Source != NodeDefaultSourceNewest {
		t.Fatalf("status = %+v", status)
	}
	if status.Build == nil || status.Build.ID != builds[1].ID {
		t.Fatalf("fallback build = %+v, want %s", status.Build, builds[1].ID)
	}
	if !status.ShimInstalled {
		t.Fatal("fallback shim is not installed")
	}
	if !errors.Is(settingErrAtInstall, store.ErrNotFound) {
		t.Fatalf("setting still present when helper installed: %v", settingErrAtInstall)
	}
	if versionAtInstall != builds[1].ID {
		t.Fatalf("VERSION at helper install = %q, want %q", versionAtInstall, builds[1].ID)
	}
	if got := readNodeDefaultVersion(t, paths); got != builds[1].ID {
		t.Fatalf("VERSION after clear = %q, want %q", got, builds[1].ID)
	}
	wrapper, err := os.ReadFile(filepath.Join(NodeDefaultShimDir(paths), "node"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wrapper), filepath.ToSlash(filepath.Join(builds[1].Path, "bin", "node"))) {
		t.Fatalf("wrapper still points at the old build:\n%s", wrapper)
	}
	if len(helper.installs) != 1 || helper.installs[0].ID != helperproto.ToolNodeDefault {
		t.Fatalf("helper installs = %+v", helper.installs)
	}
	if len(helper.removes) != 0 {
		t.Fatalf("clear removed the fallback wrapper: %v", helper.removes)
	}

	// With no installed Node build, clear removes the setting, the managed
	// artifacts, and the helper wrappers instead of erroring.
	emptySvc, emptyStore, emptyPaths, _, emptyHelper := newNodeDefaultService(t)
	if err := emptyStore.SetSetting(ctx, DefaultNodeSettingKey, "node@22.10.0"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(NodeDefaultToolDir(emptyPaths), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(NodeDefaultVersionPath(emptyPaths), []byte("node@22.10.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err = emptySvc.ClearNodeDefault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Build != nil || status.Configured != "" || status.ShimInstalled {
		t.Fatalf("empty clear status = %+v", status)
	}
	if _, err := emptyStore.GetSetting(ctx, DefaultNodeSettingKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("setting remains without builds: %v", err)
	}
	if _, err := os.Stat(NodeDefaultToolDir(emptyPaths)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed tool directory remains: %v", err)
	}
	if len(emptyHelper.removes) != 1 || emptyHelper.removes[0] != helperproto.ToolNodeDefault {
		t.Fatalf("helper removes = %v", emptyHelper.removes)
	}
}

func TestNodeDefaultToolInstallNeverChangesSetting(t *testing.T) {
	svc, st, paths, builds, helper := newNodeDefaultService(t, "22.11.0")
	ctx := context.Background()

	tool, err := svc.InstallTool(ctx, helperproto.ToolNodeDefault, false)
	if err != nil {
		t.Fatal(err)
	}
	if tool.ID != helperproto.ToolNodeDefault || !tool.Installed {
		t.Fatalf("tool = %+v", tool)
	}
	if _, err := st.GetSetting(ctx, DefaultNodeSettingKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tool install persisted a global default: %v", err)
	}
	if got := readNodeDefaultVersion(t, paths); got != builds[0].ID {
		t.Fatalf("VERSION = %q, want %q", got, builds[0].ID)
	}
	if len(helper.installs) != 1 || helper.installs[0].ID != helperproto.ToolNodeDefault {
		t.Fatalf("helper installs = %+v", helper.installs)
	}

	if _, err := svc.RemoveTool(ctx, helperproto.ToolNodeDefault); err != nil {
		t.Fatal(err)
	}
	if len(helper.removes) != 1 || helper.removes[0] != helperproto.ToolNodeDefault {
		t.Fatalf("helper removes = %v", helper.removes)
	}
	if _, err := os.Stat(NodeDefaultToolDir(paths)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed tool directory remains after remove: %v", err)
	}
}
