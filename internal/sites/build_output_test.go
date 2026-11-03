package sites

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
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
	if site.DocumentRoot != profile.Output {
		t.Fatalf("document root = %q, want build output %q", site.DocumentRoot, profile.Output)
	}
	if !site.BuildOutputMissing {
		t.Fatal("created site did not report its missing build output")
	}
	route, ok := fakes.router.Lookup(site.Domain)
	if !ok {
		t.Fatal("stopped build site published no route; browsers would get the dashboard certificate")
	}
	if route.State != string(model.StatusStopped) || route.Upstream != "" {
		t.Fatalf("stopped build route = %+v, want stopped with no upstream", route)
	}
	cert, err := fakes.router.Certificate(&tls.ClientHelloInfo{ServerName: site.Domain})
	if err != nil {
		t.Fatal(err)
	}
	if cert != fakes.certs.certFor(site.ID) {
		t.Fatal("stopped build site did not serve its own SNI certificate")
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.StatusStopped || stored.DesiredState != model.DesiredStopped {
		t.Fatalf("stored site = %+v", stored)
	}
	fetched, err := svc.Get(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !fetched.BuildOutputMissing {
		t.Fatal("Get did not report the missing build output")
	}
}

func TestStartStaticBuildSiteWithMissingOutputSaysBuildFirst(t *testing.T) {
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
	err = svc.Start(ctx, site.ID)
	if err == nil {
		t.Fatal("expected the missing build output to block start")
	}
	if !strings.Contains(err.Error(), "devstack site build app") {
		t.Fatalf("start error = %v, want a clear `devstack site build app` hint", err)
	}
	if strings.Contains(err.Error(), "agentweb:") {
		t.Fatalf("start error leaked an internal Docker target error: %v", err)
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
	if site.BuildOutputMissing {
		t.Fatal("running build site reports a missing output")
	}
	fetched, err := svc.Get(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.BuildOutputMissing {
		t.Fatal("Get reports a missing output for an existing build directory")
	}
}
