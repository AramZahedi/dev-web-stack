package sites

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestRollbackCreateCleansUpWhenPHPSiteWasNeverPrepared(t *testing.T) {
	svc, fakes := newTestService(t)
	site := model.Site{
		ID:          "never-prepared",
		Name:        "never-prepared",
		Kind:        model.SitePHP,
		FastCGIPort: 43000,
	}
	generation := filepath.Join(fakes.config.Home, "sites", site.ID, "hash")
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
	if _, statErr := os.Stat(filepath.Join(fakes.config.Home, "sites", site.ID)); !os.IsNotExist(statErr) {
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
