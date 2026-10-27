//go:build windows

package sites

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func TestWindowsSetServingModeTruthfulness(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Default()
	cfg.Home = dir
	cfg.ServingMode = config.ServingModeBuiltin
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	svc := NewService(Deps{
		Store:  db,
		Config: cfg,
	})

	ctx := context.Background()

	// 1. Setting external mode (nginx) when helper is nil/unsupported must fail with clear error
	err = svc.SetServingMode(ctx, "nginx")
	if err == nil {
		t.Fatal("expected error setting nginx serving mode on Windows, got nil")
	}
	want := "external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}

	// Verify persisted config and sqlite were not modified
	if svc.ServingMode() != config.ServingModeBuiltin {
		t.Fatalf("serving mode in memory = %q, want builtin", svc.ServingMode())
	}
	stored, err := db.GetSetting(ctx, "serving_mode")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if stored == "nginx" {
		t.Fatal("persisted config was updated to nginx in sqlite")
	}

	// 2. Setting apache mode must also fail
	err = svc.SetServingMode(ctx, "apache")
	if err == nil {
		t.Fatal("expected error setting apache serving mode on Windows, got nil")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}

	// 3. Setting builtin mode succeeds
	err = svc.SetServingMode(ctx, "builtin")
	if err != nil {
		t.Fatalf("SetServingMode(builtin) error = %v", err)
	}

	// 4. VhostStatus accurately reports builtin
	status, err := svc.VhostStatus(ctx)
	if err != nil {
		t.Fatalf("VhostStatus error = %v", err)
	}
	if status.Mode != config.ServingModeBuiltin || !status.ConfigOK {
		t.Fatalf("status = %+v, want builtin with ConfigOK: true", status)
	}
}

type fakeWindowsVhostHelper struct {
	statusErr error
	modeErr   error
	syncErr   error
}

func (f *fakeWindowsVhostHelper) VhostStatus(context.Context) (helperproto.VhostStatusResponse, error) {
	if f.statusErr != nil {
		return helperproto.VhostStatusResponse{}, f.statusErr
	}
	return helperproto.VhostStatusResponse{Mode: "builtin", ConfigOK: true}, nil
}

func (f *fakeWindowsVhostHelper) SyncVhosts(context.Context, helperproto.VHostSyncRequest) error {
	return f.syncErr
}

func TestWindowsPHPIniRawCRLFNormalization(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Default()
	cfg.Home = dir
	cfg.ServingMode = config.ServingModeBuiltin
	cfg.WSLDistro = "devstack"

	if err := db.UpsertRuntime(context.Background(), model.RuntimeBuild{
		ID:      "php@8.4.4-r1",
		Kind:    "php",
		Version: "8.4",
		Backend: model.BackendWSL,
		Path:    "/data/devstack/runtimes/php-8.4",
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(Deps{
		Store:  db,
		Config: cfg,
	})

	site, err := svc.newSite(context.Background(), CreateRequest{
		Name:       "winphp",
		Kind:       "php",
		SourceMode: "windows",
		Path:       `C:\projects\winphp`,
		PHPVersion: "8.4",
		PHPIniRaw:  "display_errors = On\r\nerror_reporting = E_ALL\r\n",
	})
	if err != nil {
		t.Fatalf("newSite with CRLF PHPIniRaw failed: %v", err)
	}
	if strings.Contains(site.PHPIniRaw, "\r") {
		t.Fatalf("site.PHPIniRaw contains carriage returns: %q", site.PHPIniRaw)
	}
	if site.PHPIniRaw != "display_errors = On\nerror_reporting = E_ALL\n" {
		t.Fatalf("unexpected normalized PHPIniRaw: %q", site.PHPIniRaw)
	}
}

func (f *fakeWindowsVhostHelper) SetServingMode(context.Context, string, helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
	if f.modeErr != nil {
		return helperproto.VhostStatusResponse{}, f.modeErr
	}
	return helperproto.VhostStatusResponse{Mode: "builtin", ConfigOK: true}, nil
}

func (f *fakeWindowsVhostHelper) RemoveVhosts(context.Context) error {
	return nil
}

func TestWindowsSetServingModeWithUnsupportedHelper(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Default()
	cfg.Home = dir
	cfg.ServingMode = config.ServingModeBuiltin
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	fakeHelper := &fakeWindowsVhostHelper{
		statusErr: helperproto.ErrNotImplemented,
		modeErr:   helperproto.ErrNotImplemented,
	}

	svc := NewService(Deps{
		Store:  db,
		Config: cfg,
		Vhosts: fakeHelper,
	})

	ctx := context.Background()

	// External mode switch must be rejected
	err = svc.SetServingMode(ctx, "nginx")
	if err == nil {
		t.Fatal("expected error setting nginx serving mode on Windows, got nil")
	}
	want := "external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}

	// Persisted config must still be builtin
	stored, err := db.GetSetting(ctx, "serving_mode")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if stored == "nginx" {
		t.Fatal("persisted config was updated to nginx in sqlite")
	}
}
