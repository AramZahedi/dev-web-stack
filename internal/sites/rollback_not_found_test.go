package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func TestStopTreatsMissingBackendAsStopped(t *testing.T) {
	svc, fakes := newTestService(t)
	site := model.Site{
		ID:           "gone",
		Name:         "gone",
		Domain:       "gone.test",
		Kind:         model.SiteStatic,
		HostPath:     fakes.root,
		BackendPath:  fakes.root,
		DocumentRoot: ".",
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
		HTTPPort:     41001,
	}
	if err := fakes.store.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	useServingMode(svc, fakes, config.ServingModeNginx)
	fakes.backend.stopErr = agentproto.ErrNotFound
	if err := svc.Stop(context.Background(), site.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	stopped, err := fakes.store.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != model.StatusStopped || stopped.DesiredState != model.DesiredStopped {
		t.Fatalf("site state = %s/%s, want stopped/stopped", stopped.Status, stopped.DesiredState)
	}
	if err := svc.Delete(context.Background(), site.ID); err != nil {
		t.Fatalf("Delete with a missing backend: %v", err)
	}
	if _, err := fakes.store.GetSite(context.Background(), site.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("site still exists after delete: %v", err)
	}
}

func TestRollbackCreateCleansUpWhenPHPSiteWasNeverPrepared(t *testing.T) {
	svc, fakes := newTestService(t)
	site := model.Site{
		ID:          "never-prepared",
		Name:        "never-prepared",
		Kind:        model.SitePHP,
		FastCGIPort: 43000,
	}
	generation := filepath.Join(fakes.config.Home, "sites", site.Name, "hash")
	if err := os.MkdirAll(generation, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fakes.ports.Reserve(site.FastCGIPort); err != nil {
		t.Fatal(err)
	}
	fakes.backend.phpStopErrs[phpFakeKey(site.ID, phpDefaultGeneration)] = agentproto.ErrNotFound

	cause := errors.New("prepare failed")
	err := svc.rollbackCreate(ctx, site, nil, false, 0, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("rollback error = %v, want cause %v", err, cause)
	}
	if _, statErr := os.Stat(filepath.Join(fakes.config.Home, "sites", site.Name)); !os.IsNotExist(statErr) {
		t.Fatalf("generation was retained after a never-prepared site: %v", statErr)
	}
	if reserveErr := fakes.ports.Reserve(site.FastCGIPort); reserveErr != nil {
		t.Fatalf("FastCGI port %d was not released: %v", site.FastCGIPort, reserveErr)
	}
	fakes.ports.Release(site.FastCGIPort)
	if _, ok := svc.retainedPHP[site.ID]; ok {
		t.Fatal("never-prepared site was recorded as retained")
	}
}
