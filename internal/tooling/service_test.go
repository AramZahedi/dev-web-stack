package tooling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

type fakeToolHelper struct {
	statuses map[string]helperproto.ToolStatus
	conflict bool
	calls    []string
	installs []helperproto.ToolInstallRequest
	removes  []string
}

func (f *fakeToolHelper) ToolStatus(context.Context) (helperproto.ToolStatusResponse, error) {
	f.calls = append(f.calls, "status")
	tools := []helperproto.ToolStatus{}
	for _, id := range []string{helperproto.ToolPHPDefault, helperproto.ToolComposer} {
		status, ok := f.statuses[id]
		if !ok {
			status = helperproto.ToolStatus{ID: id}
		}
		if f.conflict {
			status.Conflict = "foreign wrapper"
		}
		tools = append(tools, status)
	}
	return helperproto.ToolStatusResponse{Tools: tools}, nil
}

func (f *fakeToolHelper) ToolInstall(_ context.Context, id string, force bool) (helperproto.ToolStatus, error) {
	f.calls = append(f.calls, "install:"+id)
	f.installs = append(f.installs, helperproto.ToolInstallRequest{ID: id, Force: force})
	if f.conflict && !force {
		return helperproto.ToolStatus{}, helperproto.ErrToolConflict
	}
	status := f.statuses[id]
	status.ID = id
	status.Installed = true
	return status, nil
}

func (f *fakeToolHelper) ToolRemove(_ context.Context, id string) (helperproto.ToolStatus, error) {
	f.calls = append(f.calls, "remove:"+id)
	f.removes = append(f.removes, id)
	status := f.statuses[id]
	status.ID = id
	return status, nil
}

func newToolService(t *testing.T) (*Service, *store.Store, config.Paths, model.RuntimeBuild, *fakeToolHelper) {
	t.Helper()
	home := t.TempDir()
	st := testStore(t)
	paths := config.NewPaths(home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	phpBuild := fakePHPBuild(t, home, "8.4.12")
	if err := st.UpsertRuntime(context.Background(), phpBuild); err != nil {
		t.Fatal(err)
	}
	helper := &fakeToolHelper{
		statuses: map[string]helperproto.ToolStatus{
			helperproto.ToolPHPDefault: {ID: helperproto.ToolPHPDefault, WrapperPath: "/usr/local/bin/php"},
			helperproto.ToolComposer:   {ID: helperproto.ToolComposer, WrapperPath: "/usr/local/bin/composer"},
		},
	}
	svc := NewService(Deps{
		Store:  st,
		Paths:  paths,
		Helper: helper,
	})
	return svc, st, paths, phpBuild, helper
}

func TestServiceSetPHPDefaultPersistsAndPreparesShims(t *testing.T) {
	svc, st, paths, build, helper := newToolService(t)
	ctx := context.Background()

	status, err := svc.SetPHPDefault(ctx, "8.4", false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Build == nil || status.Build.ID != build.ID || status.Configured != "8.4" {
		t.Fatalf("status = %+v", status)
	}
	if !status.ShimInstalled {
		t.Fatal("shim not installed")
	}
	stored, err := st.GetSetting(ctx, DefaultPHPSettingKey)
	if err != nil || stored != "8.4" {
		t.Fatalf("setting = %q err=%v", stored, err)
	}
	if _, err := os.Stat(GlobalIniPath(paths)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ShimDir(paths), "php")); err != nil {
		t.Fatal(err)
	}
	if len(helper.installs) != 1 || helper.installs[0].ID != helperproto.ToolPHPDefault {
		t.Fatalf("helper installs = %+v", helper.installs)
	}
	if _, err := svc.PHPDefault(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestServiceSetPHPDefaultPreflightsConflictBeforePersisting(t *testing.T) {
	svc, st, _, _, helper := newToolService(t)
	ctx := context.Background()
	helper.conflict = true

	if _, err := svc.SetPHPDefault(ctx, "8.4", false); !errors.Is(err, helperproto.ErrToolConflict) {
		t.Fatalf("error = %v", err)
	}
	if _, err := st.GetSetting(ctx, DefaultPHPSettingKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("setting persisted despite conflict: %v", err)
	}
	if len(helper.installs) != 0 {
		t.Fatalf("helper install called: %+v", helper.installs)
	}
}

func TestServiceSetPHPDefaultMissingBuild(t *testing.T) {
	svc, _, _, _, _ := newToolService(t)
	if _, err := svc.SetPHPDefault(context.Background(), "8.9", false); !errors.Is(err, ErrPHPDefaultNotInstalled) {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceClearPHPDefaultFallsBackToNewestInstalled(t *testing.T) {
	svc, st, paths, _, helper := newToolService(t)
	ctx := context.Background()
	newest := fakePHPBuild(t, paths.Home, "8.5.0")
	if err := st.UpsertRuntime(ctx, newest); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPHPDefault(ctx, "8.4", false); err != nil {
		t.Fatal(err)
	}
	helper.calls = nil
	helper.installs = nil
	status, err := svc.ClearPHPDefault(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Configured != "" || status.Build == nil || status.Build.ID != newest.ID {
		t.Fatalf("status = %+v, want fallback build %s", status, newest.ID)
	}
	if !status.ShimInstalled {
		t.Fatal("fallback shim is not installed")
	}
	if _, err := st.GetSetting(ctx, DefaultPHPSettingKey); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("setting remains: %v", err)
	}
	if len(helper.removes) != 0 {
		t.Fatalf("clear removed the fallback shim: %v", helper.removes)
	}
	if len(helper.installs) != 1 || helper.installs[0].ID != helperproto.ToolPHPDefault {
		t.Fatalf("helper installs = %+v, want php-default fallback", helper.installs)
	}
	if _, err := os.Stat(GlobalIniPath(paths)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ShimDir(paths), "php")); err != nil {
		t.Fatal(err)
	}
}

func TestServiceClearPHPDefaultWithoutInstalledBuildRemovesShim(t *testing.T) {
	home := t.TempDir()
	st := testStore(t)
	paths := config.NewPaths(home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	helper := &fakeToolHelper{statuses: map[string]helperproto.ToolStatus{}}
	svc := NewService(Deps{Store: st, Paths: paths, Helper: helper})

	status, err := svc.ClearPHPDefault(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Build != nil || status.Configured != "" || status.ShimInstalled {
		t.Fatalf("status = %+v", status)
	}
	if len(helper.removes) != 1 || helper.removes[0] != helperproto.ToolPHPDefault {
		t.Fatalf("helper removes = %v", helper.removes)
	}
}

func TestServiceGlobalIniReadWrite(t *testing.T) {
	svc, _, paths, _, _ := newToolService(t)
	ctx := context.Background()
	if _, err := svc.SetPHPDefault(ctx, "8.4", false); err != nil {
		t.Fatal(err)
	}
	ini, err := svc.GlobalIni(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ini.Path != GlobalIniPath(paths) || ini.Content == "" || ini.BuildID == "" {
		t.Fatalf("ini = %+v", ini)
	}
	updated, err := svc.SaveGlobalIni(ctx, "[PHP]\nmemory_limit = 640M\n")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Content != "[PHP]\nmemory_limit = 640M\n" {
		t.Fatalf("content = %q", updated.Content)
	}
	if _, err := svc.SaveGlobalIni(ctx, "not ini\n"); err == nil {
		t.Fatal("invalid ini accepted")
	}
}

func TestServiceToolsAndComposerInstall(t *testing.T) {
	svc, _, paths, _, helper := newToolService(t)
	ctx := context.Background()

	payload := []byte("composer fixture")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	svc.deps.ComposerSource = &composerSource{
		URL:     srv.URL + "/composer.phar",
		Version: "test",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(payload)),
	}
	svc.deps.HTTPClient = srv.Client()

	tools, err := svc.Tools(ctx)
	if err != nil || len(tools) != 2 {
		t.Fatalf("tools = %+v err=%v", tools, err)
	}
	status, err := svc.InstallTool(ctx, helperproto.ToolComposer, false)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed {
		t.Fatalf("status = %+v", status)
	}
	if _, err := os.Stat(filepath.Join(paths.Tools, "composer", "composer.phar")); err != nil {
		t.Fatal(err)
	}
	if len(helper.installs) != 1 || helper.installs[0].ID != helperproto.ToolComposer {
		t.Fatalf("helper installs = %+v", helper.installs)
	}
	if _, err := svc.RemoveTool(ctx, helperproto.ToolComposer); err != nil {
		t.Fatal(err)
	}
	if len(helper.removes) != 1 {
		t.Fatalf("helper removes = %v", helper.removes)
	}
	if _, err := svc.InstallTool(ctx, "nginx", false); err == nil || !errors.Is(err, ErrUnsupportedTool) {
		t.Fatalf("unknown tool error = %v", err)
	}
}
