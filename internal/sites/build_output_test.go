package sites

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestCreateStaticBuildSiteWithMissingOutputIsStopped(t *testing.T) {
	svc, fakes := newTestService(t)
	profile := &model.BuildProfile{NodeVersion: "22", Command: "npm run build", Output: "dist"}

	site, err := svc.Create(ctx, CreateRequest{
		Name:  "app",
		Path:  fakes.root,
		Kind:  "static",
		Build: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.Status != model.StatusStopped || site.DesiredState != model.DesiredStopped {
		t.Fatalf("site status = %q desired = %q, want stopped/stopped", site.Status, site.DesiredState)
	}
	if site.HTTPPort != 0 {
		t.Fatalf("stopped site allocated HTTP port %d", site.HTTPPort)
	}
	if _, ok := fakes.router.Lookup(site.Domain); ok {
		t.Fatal("stopped site published a route")
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.StatusStopped || stored.DesiredState != model.DesiredStopped {
		t.Fatalf("stored site = %+v", stored)
	}
}

func TestCreateStaticBuildSiteWithOutputIsRunning(t *testing.T) {
	svc, fakes := newTestService(t)
	if err := os.MkdirAll(filepath.Join(fakes.root, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := &model.BuildProfile{NodeVersion: "22", Command: "npm run build", Output: "dist"}
	site, err := svc.Create(ctx, CreateRequest{
		Name:  "app",
		Path:  fakes.root,
		Kind:  "static",
		Build: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.Status != model.StatusRunning {
		t.Fatalf("status = %q, want running", site.Status)
	}
}
