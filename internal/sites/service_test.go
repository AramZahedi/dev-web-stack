//go:build !windows

package sites

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func TestCreateStaticSiteSyncsHostsAndPublishesRoute(t *testing.T) {
	svc, fakes := newTestService(t)

	site, err := svc.Create(ctx, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", TLS: true, WWW: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.ID != "site" {
		t.Fatalf("id = %q, want deterministic test id", site.ID)
	}
	if site.Domain != "blog.test" || site.Status != model.StatusRunning {
		t.Fatalf("site = %+v", site)
	}
	if site.DesiredState != model.DesiredRunning || !site.WWW || !site.TLS {
		t.Fatalf("site state = %+v", site)
	}
	if site.HTTPPort < 41000 || site.HTTPPort > 42999 {
		t.Fatalf("HTTPPort = %d, want the backend HTTP range", site.HTTPPort)
	}

	for _, host := range []string{"blog.test", "www.blog.test", "devstack.test"} {
		if !fakes.hosts.calledWith(host) {
			t.Fatalf("hosts not synced with %q: %v", host, fakes.hosts.last())
		}
	}

	route, ok := fakes.router.Lookup("blog.test")
	if !ok {
		t.Fatal("route missing")
	}
	if route.SiteID != site.ID || route.Kind != proxy.KindStatic {
		t.Fatalf("route = %+v", route)
	}
	wantUpstream := net.JoinHostPort("127.0.0.1", strconv.Itoa(site.HTTPPort))
	if route.Upstream != wantUpstream {
		t.Fatalf("upstream = %q, want %q", route.Upstream, wantUpstream)
	}
	if !route.TLS || route.ForceHTTPS {
		t.Fatalf("route TLS flags = %+v", route)
	}
	if _, ok := fakes.router.Lookup("www.blog.test"); !ok {
		t.Fatal("www route missing")
	}
	if got := fakes.certs.ensuredFor("site"); !reflect.DeepEqual(got, []string{"blog.test", "www.blog.test"}) {
		t.Fatalf("certificate domains = %v", got)
	}
	if !fakes.backend.started[site.ID] {
		t.Fatal("backend not started")
	}
	if calls := fakes.backend.runtimeList(); len(calls) != 0 {
		t.Fatalf("static site ensured runtimes: %v", calls)
	}

	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatalf("site not persisted: %v", err)
	}
	if stored.Domain != "blog.test" || stored.Status != model.StatusRunning {
		t.Fatalf("stored site = %+v", stored)
	}
}

func TestCreateRollsBackWhenHostsSyncFails(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.hosts.err = errors.New("boom")

	if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err == nil {
		t.Fatal("want error")
	}
	sites, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 0 {
		t.Fatalf("site persisted: %+v", sites)
	}
	if got := fakes.certs.ensuredFor("site"); got != nil {
		t.Fatalf("certificate issued before hosts sync succeeded: %v", got)
	}
	if len(fakes.backend.started) != 0 {
		t.Fatalf("backend started: %v", fakes.backend.started)
	}
}

func TestCreateRollsBackOnBackendFailureAndReleasesPort(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*fakeBackend)
	}{
		{name: "prepare", setup: func(f *fakeBackend) { f.prepareErr = errors.New("prepare failed") }},
		{name: "start", setup: func(f *fakeBackend) { f.startErr = errors.New("start failed") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fakes := newTestService(t)
			tc.setup(fakes.backend)

			_, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static", TLS: true})
			if err == nil {
				t.Fatal("want error")
			}
			sites, err := svc.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(sites) != 0 {
				t.Fatalf("site persisted: %+v", sites)
			}
			if _, err := fakes.store.GetCertBySite(ctx, "site"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("certificate row remains: %v", err)
			}
			if fakes.certs.certFor("site") != nil {
				t.Fatal("certificate material remains")
			}
			if fakes.hosts.has("blog.test") {
				t.Fatalf("new domain remains in hosts: %v", fakes.hosts.last())
			}
			if !fakes.hosts.has("devstack.test") {
				t.Fatalf("dashboard host missing after rollback: %v", fakes.hosts.last())
			}

			spec, ok := fakes.backend.lastPrepared()
			if !ok {
				t.Fatal("backend never prepared")
			}
			if err := fakes.ports.Reserve(spec.HTTPPort); err != nil {
				t.Fatalf("port %d was not released: %v", spec.HTTPPort, err)
			}
		})
	}
}

func TestUpdateDomainRollsBackOnCertFailure(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static", TLS: true})

	fakes.certs.err = errors.New("signer down")
	if _, err := svc.Update(ctx, site.Name, UpdateRequest{Domain: "new.test"}); err == nil {
		t.Fatal("want error")
	}

	got, err := svc.Get(ctx, site.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "blog.test" {
		t.Fatalf("domain changed to %q", got.Domain)
	}
	if !fakes.hosts.has("blog.test") {
		t.Fatalf("old domain not restored: %v", fakes.hosts.last())
	}
	if fakes.hosts.has("new.test") {
		t.Fatalf("new domain left in hosts: %v", fakes.hosts.last())
	}
	if _, ok := fakes.router.Lookup("new.test"); ok {
		t.Fatal("route for failed rename published")
	}
	if _, ok := fakes.router.Lookup("blog.test"); !ok {
		t.Fatal("original route lost")
	}
	rec, err := fakes.store.GetCertBySite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.Domains, []string{"blog.test"}) {
		t.Fatalf("certificate domains = %v", rec.Domains)
	}
}

func TestUpdateDomainSuccessReconcilesHostsAndCertificate(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", TLS: true, WWW: true,
	})

	if _, err := svc.Update(ctx, site.ID, UpdateRequest{Domain: "shop.test"}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(ctx, site.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "shop.test" || !got.WWW {
		t.Fatalf("site = %+v", got)
	}

	for _, host := range []string{"shop.test", "www.shop.test"} {
		if _, ok := fakes.router.Lookup(host); !ok {
			t.Fatalf("route %q missing", host)
		}
	}
	for _, host := range []string{"blog.test", "www.blog.test"} {
		if _, ok := fakes.router.Lookup(host); ok {
			t.Fatalf("stale route %q still present", host)
		}
	}

	if !fakes.hosts.anyCallWithAll("blog.test", "shop.test") {
		t.Fatalf("rename did not sync the old and new domains together: %v", fakes.hosts.last())
	}
	last := fakes.hosts.last()
	for _, host := range []string{"shop.test", "www.shop.test", "devstack.test"} {
		if !containsString(last, host) {
			t.Fatalf("final hosts set %v missing %q", last, host)
		}
	}
	for _, host := range []string{"blog.test", "www.blog.test"} {
		if containsString(last, host) {
			t.Fatalf("final hosts set %v still has %q", last, host)
		}
	}

	if got := fakes.certs.ensuredFor(site.ID); !reflect.DeepEqual(got, []string{"shop.test", "www.shop.test"}) {
		t.Fatalf("certificate domains = %v", got)
	}
}

func TestUpdateRollsBackWhenFinalHostsSyncFails(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static", TLS: true})

	// The update performs two syncs: the union of old+new domains, then the
	// final set without the old domain. Fail the second so persistence and
	// certificate issuance have already happened.
	fakes.hosts.failAt = fakes.hosts.callCount() + 2

	if _, err := svc.Update(ctx, site.ID, UpdateRequest{Domain: "new.test"}); err == nil {
		t.Fatal("want error")
	}

	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Domain != "blog.test" {
		t.Fatalf("stored domain = %q, want rollback", stored.Domain)
	}
	if _, ok := fakes.router.Lookup("blog.test"); !ok {
		t.Fatal("original route not restored")
	}
	if _, ok := fakes.router.Lookup("new.test"); ok {
		t.Fatal("failed rename route still published")
	}
	if !fakes.hosts.has("blog.test") || fakes.hosts.has("new.test") {
		t.Fatalf("hosts not rolled back: %v", fakes.hosts.last())
	}
	rec, err := fakes.store.GetCertBySite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.Domains, []string{"blog.test"}) {
		t.Fatalf("certificate domains = %v, want old domains", rec.Domains)
	}
}

func TestUpdatePointerFieldsAndEnvironment(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", TLS: true,
	})

	www, tlsOff, force, spa := true, false, true, true
	updated, err := svc.Update(ctx, site.Name, UpdateRequest{
		WWW:          &www,
		TLS:          &tlsOff,
		ForceHTTPS:   &force,
		SPAFallback:  &spa,
		DocumentRoot: ".",
		Env:          map[string]string{"APP_ENV": "local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.WWW || updated.TLS || !updated.ForceHTTPS || !updated.SPAFallback {
		t.Fatalf("updated flags = %+v", updated)
	}
	if !reflect.DeepEqual(updated.Env, map[string]string{"APP_ENV": "local"}) {
		t.Fatalf("env = %v", updated.Env)
	}

	route, ok := fakes.router.Lookup("blog.test")
	if !ok {
		t.Fatal("route missing")
	}
	if route.TLS || !route.ForceHTTPS {
		t.Fatalf("route flags = %+v", route)
	}
	if _, ok := fakes.router.Lookup("www.blog.test"); !ok {
		t.Fatal("www route not published after enabling the alias")
	}
	if got := fakes.certs.ensuredFor(site.ID); !reflect.DeepEqual(got, []string{"blog.test", "www.blog.test"}) {
		t.Fatalf("certificate domains = %v", got)
	}
}

func TestStartStopRestartAreIdempotent(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})

	for i := 0; i < 2; i++ {
		if err := svc.Start(ctx, site.Name); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		got := mustGet(t, svc, site.ID)
		if got.Status != model.StatusRunning || got.DesiredState != model.DesiredRunning {
			t.Fatalf("after start %d: %+v", i, got)
		}
	}

	if err := svc.Stop(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	got := mustGet(t, svc, site.ID)
	if got.Status != model.StatusStopped || got.DesiredState != model.DesiredStopped {
		t.Fatalf("after stop: %+v", got)
	}
	if !fakes.backend.stopped[site.ID] {
		t.Fatal("backend not stopped")
	}
	if !hasEvent(t, fakes, site.ID, "site.stopped") {
		t.Fatal("stop event missing")
	}

	if err := svc.Stop(ctx, site.Name); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, svc, site.ID); got.Status != model.StatusRunning {
		t.Fatalf("after restart-start: %+v", got)
	}

	if err := svc.Restart(ctx, site.Name); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, svc, site.ID); got.Status != model.StatusRunning || got.DesiredState != model.DesiredRunning {
		t.Fatalf("after restart: %+v", got)
	}
	if _, ok := fakes.router.Lookup("blog.test"); !ok {
		t.Fatal("route missing after restart")
	}
}

func TestGetRefreshesStatusFromBackendAndEmitsEvent(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})

	fakes.backend.setStatus(site.ID, model.StatusCrashed)
	got := mustGet(t, svc, site.ID)
	if got.Status != model.StatusCrashed {
		t.Fatalf("status = %q", got.Status)
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.StatusCrashed {
		t.Fatalf("persisted status = %q", stored.Status)
	}
	if !hasEvent(t, fakes, site.ID, "site.crashed") {
		t.Fatal("status change event missing")
	}
	before := countEvent(t, fakes, site.ID, "site.crashed")
	if _, err := svc.Get(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if after := countEvent(t, fakes, site.ID, "site.crashed"); after != before {
		t.Fatalf("duplicate status event: %d -> %d", before, after)
	}
}

func TestListRefreshesStatuses(t *testing.T) {
	svc, fakes := newTestService(t)
	blog := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	shop := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "static"})

	fakes.backend.setStatus(blog.ID, model.StatusCrashed)
	fakes.backend.setStatus(shop.ID, model.StatusStopped)

	sites, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 2 {
		t.Fatalf("sites = %d", len(sites))
	}
	for _, site := range sites {
		want := model.StatusCrashed
		if site.ID == shop.ID {
			want = model.StatusStopped
		}
		if site.Status != want {
			t.Fatalf("site %s status = %q, want %q", site.Name, site.Status, want)
		}
		stored, err := fakes.store.GetSite(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != want {
			t.Fatalf("persisted %s status = %q", site.Name, stored.Status)
		}
	}
}

func TestDeleteRemovesHostsCertAndRoute(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})

	if err := svc.Delete(ctx, site.Name); err != nil {
		t.Fatal(err)
	}

	if _, ok := fakes.router.Lookup("blog.test"); ok {
		t.Fatal("route still present")
	}
	if fakes.hosts.has("blog.test") {
		t.Fatalf("hosts entry still present: %v", fakes.hosts.last())
	}
	if !fakes.hosts.has("devstack.test") {
		t.Fatalf("dashboard host missing: %v", fakes.hosts.last())
	}
	if _, err := fakes.store.GetSite(ctx, site.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("site row remains: %v", err)
	}
	if !fakes.backend.stopped["site"] {
		t.Fatal("backend not stopped")
	}
	if _, err := fakes.store.GetCertBySite(ctx, site.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("certificate row remains: %v", err)
	}
	if fakes.certs.certFor(site.ID) != nil {
		t.Fatal("certificate material remains")
	}
	if !fakes.certs.wasDeleted(site.ID) {
		t.Fatal("certificate manager delete not called")
	}
	if err := fakes.ports.Reserve(site.HTTPPort); err != nil {
		t.Fatalf("port %d not released: %v", site.HTTPPort, err)
	}
	if !hasEvent(t, fakes, site.ID, "site.deleted") {
		t.Fatal("delete event missing")
	}
}

func TestDeleteKeepsStoppedSiteWhenHostsSyncFails(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})

	fakes.hosts.failAt = fakes.hosts.callCount() + 1
	if err := svc.Delete(ctx, site.ID); err == nil {
		t.Fatal("want error")
	}

	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatalf("site row removed: %v", err)
	}
	if stored.Status != model.StatusStopped || stored.DesiredState != model.DesiredStopped {
		t.Fatalf("stored site = %+v", stored)
	}
	if !fakes.backend.stopped[site.ID] {
		t.Fatal("backend not stopped")
	}
	if _, ok := fakes.router.Lookup("blog.test"); !ok {
		t.Fatal("route removed before hosts sync")
	}
	if _, err := fakes.store.GetCertBySite(ctx, site.ID); err != nil {
		t.Fatalf("certificate removed before hosts sync: %v", err)
	}
	if err := fakes.ports.Reserve(site.HTTPPort); err == nil {
		t.Fatalf("port %d released despite failed delete", site.HTTPPort)
	}
}

func TestDeleteSyncsRemainingDomainsAndDashboard(t *testing.T) {
	svc, fakes := newTestService(t)
	blog := mustCreate(t, svc, fakes, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", WWW: true,
	})
	shop := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "static"})

	if err := svc.Delete(ctx, blog.ID); err != nil {
		t.Fatal(err)
	}

	last := fakes.hosts.last()
	for _, host := range []string{"shop.test", "devstack.test"} {
		if !containsString(last, host) {
			t.Fatalf("hosts %v missing %q", last, host)
		}
	}
	for _, host := range []string{"blog.test", "www.blog.test"} {
		if containsString(last, host) {
			t.Fatalf("hosts %v still contains %q", last, host)
		}
	}
	route, ok := fakes.router.Lookup("shop.test")
	if !ok {
		t.Fatal("remaining site route lost")
	}
	if route.SiteID != shop.ID {
		t.Fatalf("remaining route site = %q, want %q", route.SiteID, shop.ID)
	}
	if _, ok := fakes.router.Lookup("blog.test"); ok {
		t.Fatal("deleted site route still present")
	}
}

func TestRebuildRoutesPublishesDashboardAndCertificates(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", TLS: true, WWW: true,
	})

	if err := svc.RebuildRoutes(ctx); err != nil {
		t.Fatal(err)
	}

	route, ok := fakes.router.Lookup("devstack.test")
	if !ok {
		t.Fatal("dashboard route missing")
	}
	if route.Upstream != "127.0.0.1:7070" || route.Kind != proxy.KindStatic || !route.TLS {
		t.Fatalf("dashboard route = %+v", route)
	}

	dashboard := fakes.certs.certFor(certs.DashboardOwner)
	if dashboard == nil {
		t.Fatal("dashboard certificate not issued")
	}
	got, err := fakes.router.Certificate(&tls.ClientHelloInfo{ServerName: "unknown.test"})
	if err != nil {
		t.Fatal(err)
	}
	if got != dashboard {
		t.Fatalf("default certificate = %p, want dashboard %p", got, dashboard)
	}

	for _, host := range []string{"blog.test", "www.blog.test"} {
		got, err := fakes.router.Certificate(&tls.ClientHelloInfo{ServerName: host})
		if err != nil {
			t.Fatalf("certificate for %s: %v", host, err)
		}
		if got != fakes.certs.certFor(site.ID) {
			t.Fatalf("certificate for %s is not the site certificate", host)
		}
	}
}

func TestCreateRejectsInvalidAndDuplicateSites(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, tc := range []struct {
		name string
		req  CreateRequest
		want string
	}{
		{name: "invalid name", req: CreateRequest{Name: "bad name", Kind: "static"}, want: "project name"},
		{name: "reserved domain", req: CreateRequest{Name: "devstack", Kind: "static"}, want: "reserved"},
		{name: "relative path", req: CreateRequest{Name: "blog", Path: "relative", Kind: "static"}, want: "absolute"},
		{name: "missing path", req: CreateRequest{Name: "blog", Path: missing, Kind: "static"}, want: "resolve project path"},
		{name: "unsupported kind", req: CreateRequest{Name: "blog", Kind: "perl"}, want: "unsupported site kind"},
		{name: "node missing version", req: CreateRequest{Name: "blog", Kind: "node", Command: "npm run dev"}, want: "requires a version"},
		{name: "node missing command", req: CreateRequest{Name: "blog", Kind: "node", NodeVersion: "24"}, want: "requires a command"},
		{name: "missing document root", req: CreateRequest{Name: "blog", Kind: "static", DocumentRoot: "public"}, want: "document root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, fakes := newTestService(t)
			if tc.req.Path == "" {
				tc.req.Path = fakes.root
			}
			if _, err := svc.Create(ctx, tc.req); err == nil {
				t.Fatal("want error")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
			sites, err := svc.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(sites) != 0 {
				t.Fatalf("invalid request persisted a site: %+v", sites)
			}
		})
	}

	t.Run("duplicate name", func(t *testing.T) {
		svc, fakes := newTestService(t)
		mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
		if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err == nil {
			t.Fatal("want error")
		} else if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("duplicate domain alias", func(t *testing.T) {
		svc, fakes := newTestService(t)
		mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static", WWW: true})
		shop := mustCreate(t, svc, fakes, CreateRequest{Name: "shop", Path: fakes.root, Kind: "static"})
		if _, err := svc.Update(ctx, shop.ID, UpdateRequest{Domain: "www.blog.test"}); err == nil {
			t.Fatal("want error")
		} else if !strings.Contains(err.Error(), "already used") {
			t.Fatalf("error = %v", err)
		}
		got := mustGet(t, svc, shop.ID)
		if got.Domain != "shop.test" {
			t.Fatalf("domain = %q, want unchanged", got.Domain)
		}
	})
}

func TestCreateAndUpdateRejectUnsafeEnvironmentNames(t *testing.T) {
	svc, fakes := newTestService(t)
	for _, env := range []map[string]string{
		{"BAD NAME": "x"},
		{"X;id": "x"},
		{"NUL": "a\x00b"},
	} {
		if _, err := svc.Create(ctx, CreateRequest{Name: "envbad", Path: fakes.root, Kind: "static", Env: env}); err == nil {
			t.Fatalf("Create accepted environment %v", env)
		}
	}
	sites, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 0 {
		t.Fatalf("invalid environment persisted a site: %+v", sites)
	}

	site := mustCreate(t, svc, fakes, CreateRequest{Name: "envsite", Path: fakes.root, Kind: "static"})
	for _, env := range []map[string]string{
		{"BAD NAME": "x"},
		{"X;id": "x"},
		{"NUL": "a\x00b"},
	} {
		if _, err := svc.Update(ctx, site.ID, UpdateRequest{Env: env}); err == nil {
			t.Fatalf("Update accepted environment %v", env)
		}
	}
	got := mustGet(t, svc, site.ID)
	if len(got.Env) != 0 {
		t.Fatalf("invalid update changed environment: %+v", got.Env)
	}

	good := mustCreate(t, svc, fakes, CreateRequest{
		Name: "envgood", Path: fakes.root, Kind: "static",
		Env: map[string]string{"APP_ENV": "dev"},
	})
	if good.Env["APP_ENV"] != "dev" {
		t.Fatalf("valid environment = %+v", good.Env)
	}
}

func TestStartRunningStaticSiteIsIdempotentWithoutReprepare(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})

	fakes.backend.mu.Lock()
	before := fakes.backend.genericPrepareCalls
	fakes.backend.prepareErr = errors.New("agent: stop the site before changing its prepared configuration")
	fakes.backend.mu.Unlock()

	if err := svc.Start(ctx, site.ID); err != nil {
		t.Fatalf("Start on a running site: %v", err)
	}
	fakes.backend.mu.Lock()
	defer fakes.backend.mu.Unlock()
	if fakes.backend.genericPrepareCalls != before {
		t.Fatalf("Start re-prepared a running site (%d -> %d)", before, fakes.backend.genericPrepareCalls)
	}
}

func TestCreateNodeSiteEnsuresRuntime(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{
		Name:        "shop",
		Path:        fakes.root,
		Kind:        "node",
		NodeVersion: "24.9.0",
		Command:     "npm run dev",
	})

	if calls := fakes.backend.runtimeList(); !containsString(calls, "node@24.9.0") {
		t.Fatalf("runtime calls = %v", calls)
	}
	spec, ok := fakes.backend.lastPrepared()
	if !ok {
		t.Fatal("backend never prepared")
	}
	if spec.RuntimeVersion != "24.9.0" || spec.Command != "npm run dev" {
		t.Fatalf("spec = %+v", spec)
	}
	route, ok := fakes.router.Lookup("shop.test")
	if !ok {
		t.Fatal("route missing")
	}
	if route.Kind != proxy.KindNode {
		t.Fatalf("route kind = %q", route.Kind)
	}
	if got := fakes.certs.ensuredFor(site.ID); !reflect.DeepEqual(got, []string{"shop.test"}) {
		t.Fatalf("certificate domains = %v (certificates are issued even when TLS serving is off)", got)
	}
}

func TestPHPSiteRouteCarriesFastCGIConfiguration(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
	if err := os.MkdirAll(filepath.Join(fakes.root, "public"), 0o755); err != nil {
		t.Fatal(err)
	}

	site := mustCreate(t, svc, fakes, CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4",
		DocumentRoot: ".", FrontController: true, TLS: true,
	})

	route, ok := fakes.router.Lookup("blog.test")
	if !ok {
		t.Fatal("PHP route missing")
	}
	if route.Kind != proxy.KindPHP {
		t.Fatalf("route kind = %q, want %q", route.Kind, proxy.KindPHP)
	}
	if want := net.JoinHostPort("127.0.0.1", strconv.Itoa(site.FastCGIPort)); route.Upstream != want {
		t.Fatalf("route upstream = %q, want %q", route.Upstream, want)
	}
	if got, want := route.DocumentRoot, filepath.Clean(fakes.root); got != want {
		t.Fatalf("route document root = %q, want %q", got, want)
	}
	if !route.FrontController {
		t.Fatal("route did not carry the front controller")
	}
	if !route.TLS {
		t.Fatal("route did not carry TLS")
	}

	frontController := false
	updated, err := svc.Update(ctx, site.Name, UpdateRequest{
		DocumentRoot:    "public",
		FrontController: &frontController,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DocumentRoot != "public" {
		t.Fatalf("updated document root = %q", updated.DocumentRoot)
	}

	route, ok = fakes.router.Lookup("blog.test")
	if !ok {
		t.Fatal("PHP route missing after update")
	}
	if want := filepath.Join(fakes.root, "public"); route.DocumentRoot != want {
		t.Fatalf("route document root after update = %q, want %q", route.DocumentRoot, want)
	}
	if route.FrontController {
		t.Fatal("route front controller was not updated")
	}
	if want := net.JoinHostPort("127.0.0.1", strconv.Itoa(updated.FastCGIPort)); route.Upstream != want {
		t.Fatalf("route upstream after update = %q, want %q", route.Upstream, want)
	}
}

func TestNewServiceReservesPersistedPorts(t *testing.T) {
	svc, fakes := newTestService(t)
	site := mustCreate(t, svc, fakes, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})

	router := proxy.NewRouter()
	ports := domain.NewPortAllocator()
	_ = NewService(Deps{
		Store:   fakes.store,
		Backend: fakes.backend,
		Helper:  fakes.hosts,
		Certs:   fakes.certs,
		Router:  router,
		Ports:   ports,
		Config:  fakes.config,
	})

	if err := ports.Reserve(site.HTTPPort); err == nil {
		t.Fatalf("port %d was not reserved from persisted state", site.HTTPPort)
	}
}

func mustCreate(t *testing.T, svc *Service, fakes *testFakes, req CreateRequest) model.Site {
	t.Helper()
	site, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("create %q: %v", req.Name, err)
	}
	return site
}

func mustGet(t *testing.T, svc *Service, ref string) model.Site {
	t.Helper()
	site, err := svc.Get(ctx, ref)
	if err != nil {
		t.Fatalf("get %q: %v", ref, err)
	}
	return site
}

func hasEvent(t *testing.T, fakes *testFakes, siteID, typ string) bool {
	t.Helper()
	return countEvent(t, fakes, siteID, typ) > 0
}

func countEvent(t *testing.T, fakes *testFakes, siteID, typ string) int {
	t.Helper()
	events, err := fakes.store.ListEvents(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.SiteID == siteID && event.Type == typ {
			count++
		}
	}
	return count
}

func TestValidateDocumentRootConfinesAbsolutePaths(t *testing.T) {
	project := t.TempDir()
	inside := filepath.Join(project, "public")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := validateDocumentRoot(project, inside); err != nil || got != inside {
		t.Fatalf("inside absolute root = %q, err = %v", got, err)
	}
	outside := t.TempDir()
	if _, err := validateDocumentRoot(project, outside); err == nil {
		t.Fatal("accepted an absolute document root outside the project")
	}
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err == nil {
		if _, err := validateDocumentRoot(project, filepath.Join(project, "escape")); err == nil {
			t.Fatal("accepted a symlinked document root outside the project")
		}
	}
}

func TestRestartStartFailurePreservesDesiredRunning(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	fakes.backend.startErr = errors.New("start failed")
	if err := svc.Restart(ctx, site.ID); err == nil {
		t.Fatal("Restart succeeded despite the injected start failure")
	}
	stored, err := fakes.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DesiredState != model.DesiredRunning {
		t.Fatalf("desired state = %q, want running", stored.DesiredState)
	}
	if stored.Status != model.StatusError {
		t.Fatalf("status = %q, want error", stored.Status)
	}
}

func TestUpdateRunningStaticAppliesDocumentRoot(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(fakes.root, "public")
	if err := os.Mkdir(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "index.html"), []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{DocumentRoot: "public"}); err != nil {
		t.Fatal(err)
	}
	fakes.backend.mu.Lock()
	defer fakes.backend.mu.Unlock()
	if len(fakes.backend.prepared) == 0 {
		t.Fatal("backend was not prepared")
	}
	last := fakes.backend.prepared[len(fakes.backend.prepared)-1]
	if last.DocumentRoot != "public" {
		t.Fatalf("prepared document root = %q, want public", last.DocumentRoot)
	}
}

func TestRefreshStatusRepublishesRouteState(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	fakes.backend.mu.Lock()
	fakes.backend.status[site.ID] = model.StatusCrashed
	fakes.backend.mu.Unlock()
	if _, err := svc.Get(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	route, ok := fakes.router.Lookup("blog.test")
	if !ok || route.State != string(model.StatusCrashed) {
		t.Fatalf("route = %+v, ok=%v, want crashed state", route, ok)
	}
}

func TestCreateSiteContractsDefaults(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(ctx, CreateRequest{
		Name: "defaults-site",
		Path: fakes.root,
		Kind: "static",
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.FrontControllerFile != "index.php" {
		t.Errorf("FrontControllerFile = %q, want index.php", site.FrontControllerFile)
	}
	if site.FrontControllerMode != "auto" {
		t.Errorf("FrontControllerMode = %q, want auto", site.FrontControllerMode)
	}
	if site.ApacheAllowOverride != "all" {
		t.Errorf("ApacheAllowOverride = %q, want all", site.ApacheAllowOverride)
	}
	if site.SPAFallbackFile != "index.html" {
		t.Errorf("SPAFallbackFile = %q, want index.html", site.SPAFallbackFile)
	}
	if site.Build != nil {
		t.Errorf("Build = %+v, want nil", site.Build)
	}
}

func TestCreateSiteContractsValidation(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.5.1-r2", "8.5.1")

	// Valid creation with non-default values
	validPHP, err := svc.Create(ctx, CreateRequest{
		Name:                "php-valid",
		Path:                fakes.root,
		Kind:                "php",
		PHPVersion:          "8.5",
		PHPBuild:            "php@8.5.1-r2",
		FrontControllerFile: "app.php",
		FrontControllerMode: "generated",
		ApacheAllowOverride: "fileinfo",
		SPAFallbackFile:     "fallback.html",
		Build: &model.BuildProfile{
			NodeVersion: "20.0.0",
			Command:     "npm run build",
			Output:      "dist",
		},
	})
	if err != nil {
		t.Fatalf("valid create failed: %v", err)
	}
	if validPHP.FrontControllerFile != "app.php" || validPHP.FrontControllerMode != "generated" ||
		validPHP.ApacheAllowOverride != "fileinfo" || validPHP.SPAFallbackFile != "fallback.html" ||
		validPHP.Build == nil || validPHP.Build.Command != "npm run build" {
		t.Fatalf("contract fields not set correctly: %+v", validPHP)
	}

	tests := []struct {
		name string
		req  CreateRequest
	}{
		{
			name: "fc file absolute",
			req: CreateRequest{
				Name: "bad-fc-1", Path: fakes.root, Kind: "php", PHPVersion: "8.5", PHPBuild: "php@8.5.1-r2",
				FrontControllerFile: "/index.php",
			},
		},
		{
			name: "fc file traversal",
			req: CreateRequest{
				Name: "bad-fc-2", Path: fakes.root, Kind: "php", PHPVersion: "8.5", PHPBuild: "php@8.5.1-r2",
				FrontControllerFile: "../index.php",
			},
		},
		{
			name: "fc file dot segment",
			req: CreateRequest{
				Name: "bad-fc-3", Path: fakes.root, Kind: "php", PHPVersion: "8.5", PHPBuild: "php@8.5.1-r2",
				FrontControllerFile: "./index.php",
			},
		},
		{
			name: "fc file not php",
			req: CreateRequest{
				Name: "bad-fc-4", Path: fakes.root, Kind: "php", PHPVersion: "8.5", PHPBuild: "php@8.5.1-r2",
				FrontControllerFile: "index.html",
			},
		},
		{
			name: "fc mode invalid",
			req: CreateRequest{
				Name: "bad-mode", Path: fakes.root, Kind: "php", PHPVersion: "8.5", PHPBuild: "php@8.5.1-r2",
				FrontControllerMode: "custom",
			},
		},
		{
			name: "apache override invalid",
			req: CreateRequest{
				Name: "bad-override", Path: fakes.root, Kind: "php", PHPVersion: "8.5", PHPBuild: "php@8.5.1-r2",
				ApacheAllowOverride: "invalid",
			},
		},
		{
			name: "spa file absolute",
			req: CreateRequest{
				Name: "bad-spa-1", Path: fakes.root, Kind: "static",
				SPAFallbackFile: "/index.html",
			},
		},
		{
			name: "spa file traversal",
			req: CreateRequest{
				Name: "bad-spa-2", Path: fakes.root, Kind: "static",
				SPAFallbackFile: "../index.html",
			},
		},
		{
			name: "spa file php",
			req: CreateRequest{
				Name: "bad-spa-3", Path: fakes.root, Kind: "static",
				SPAFallbackFile: "index.php",
			},
		},
		{
			name: "build missing node version",
			req: CreateRequest{
				Name: "bad-build-1", Path: fakes.root, Kind: "static",
				Build: &model.BuildProfile{Command: "npm run build"},
			},
		},
		{
			name: "build missing command",
			req: CreateRequest{
				Name: "bad-build-2", Path: fakes.root, Kind: "static",
				Build: &model.BuildProfile{NodeVersion: "20"},
			},
		},
		{
			name: "build output traversal",
			req: CreateRequest{
				Name: "bad-build-3", Path: fakes.root, Kind: "static",
				Build: &model.BuildProfile{NodeVersion: "20", Command: "npm run build", Output: "../outside"},
			},
		},
		{
			name: "build output absolute",
			req: CreateRequest{
				Name: "bad-build-4", Path: fakes.root, Kind: "static",
				Build: &model.BuildProfile{NodeVersion: "20", Command: "npm run build", Output: "/dist"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Create(ctx, tt.req)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
		})
	}
}

func TestUpdateSiteContractsValidation(t *testing.T) {
	svc, fakes := newTestService(t)
	fakes.installPHPBuild("php@8.5.1-r2", "8.5.1")
	site, err := svc.Create(ctx, CreateRequest{
		Name:       "up-site",
		Path:       fakes.root,
		Kind:       "php",
		PHPVersion: "8.5",
		PHPBuild:   "php@8.5.1-r2",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Invalid updates
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{FrontControllerFile: "../bad.php"}); err == nil {
		t.Fatal("expected error for traversal in FrontControllerFile")
	}
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{FrontControllerMode: "invalid"}); err == nil {
		t.Fatal("expected error for invalid FrontControllerMode")
	}
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{ApacheAllowOverride: "invalid"}); err == nil {
		t.Fatal("expected error for invalid ApacheAllowOverride")
	}
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{SPAFallbackFile: "index.php"}); err == nil {
		t.Fatal("expected error for .php SPAFallbackFile")
	}
	if _, err := svc.Update(ctx, site.ID, UpdateRequest{Build: &model.BuildProfile{Command: "npm run build"}}); err == nil {
		t.Fatal("expected error for build with command but no node version")
	}

	// Valid update
	updated, err := svc.Update(ctx, site.ID, UpdateRequest{
		FrontControllerFile: "bootstrap.php",
		FrontControllerMode: "htaccess",
		ApacheAllowOverride: "none",
		SPAFallbackFile:     "app.html",
		Build: &model.BuildProfile{
			NodeVersion: "20",
			Command:     "npm run build",
			Output:      "dist",
		},
	})
	if err != nil {
		t.Fatalf("valid update failed: %v", err)
	}
	if updated.FrontControllerFile != "bootstrap.php" ||
		updated.FrontControllerMode != "htaccess" ||
		updated.ApacheAllowOverride != "none" ||
		updated.SPAFallbackFile != "app.html" ||
		updated.Build == nil ||
		updated.Build.Command != "npm run build" {
		t.Fatalf("updated fields mismatch: %+v", updated)
	}
}
