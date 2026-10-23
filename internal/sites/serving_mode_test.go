package sites

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

func useServingMode(svc *Service, fakes *testFakes, mode string) {
	svc.deps.Config.ServingMode = mode
	fakes.vhosts.status.Mode = mode
}

func persistServingMode(t *testing.T, svc *Service, mode string) {
	t.Helper()
	svc.deps.Config.ServingMode = mode
	if err := svc.deps.Config.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalStaticCreateSkipsBackendAndPort(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)

	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", TLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.Status != model.StatusRunning || site.DesiredState != model.DesiredRunning {
		t.Fatalf("site = %+v", site)
	}
	if site.HTTPPort != 0 {
		t.Fatalf("external static HTTPPort = %d, want 0", site.HTTPPort)
	}
	if fakes.backend.started[site.ID] {
		t.Fatal("static backend started in external mode")
	}
	req, ok := fakes.vhosts.lastSync()
	if !ok {
		t.Fatal("vhosts were not synced")
	}
	if req.Mode != "nginx" || len(req.Sites) != 1 {
		t.Fatalf("sync request = %+v", req)
	}
	got := req.Sites[0]
	if got.ID != site.ID || got.BackendPath != fakes.root || got.DocumentRoot != fakes.root {
		t.Fatalf("site request = %+v", got)
	}
	if got.BackendPort != 0 || got.Kind != "static" || got.DesiredState != "running" || !got.TLS {
		t.Fatalf("site request = %+v", got)
	}
	if len(got.CertPEM) == 0 || len(got.KeyPEM) == 0 {
		t.Fatal("TLS site was synced without certificate material")
	}
	if req.Dashboard.Domain != "devstack.test" || req.Dashboard.BackendPort != fakes.config.UIPort {
		t.Fatalf("dashboard request = %+v", req.Dashboard)
	}
	if len(req.Dashboard.CertPEM) == 0 || len(req.Dashboard.KeyPEM) == 0 {
		t.Fatal("dashboard certificate missing from sync request")
	}
	if req.MaxBodyBytes != int64(fakes.config.MaxRequestBodyMB)<<20 {
		t.Fatalf("MaxBodyBytes = %d", req.MaxBodyBytes)
	}
}

func TestCreateRollsBackWhenVhostSyncFails(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)
	fakes.vhosts.err = errors.New("bad config")

	if _, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err == nil {
		t.Fatal("want error")
	}
	if list, err := svc.List(context.Background()); err != nil || len(list) != 0 {
		t.Fatalf("sites = %+v, err = %v", list, err)
	}
	if fakes.backend.started["site"] {
		t.Fatal("backend left running")
	}
	if got := fakes.backend.stopCount(); got != 0 {
		t.Fatalf("external static rollback called Backend.Stop %d times", got)
	}
	if fakes.hosts.has("blog.test") {
		t.Fatal("hosts entry left behind")
	}
	if !fakes.certs.wasDeleted("site") {
		t.Fatal("certificate left behind")
	}
}

func TestExternalStaticStartStopAndObservedStatus(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)
	public := filepath.Join(fakes.root, "public")
	if err := os.MkdirAll(public, 0o755); err != nil {
		t.Fatal(err)
	}
	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", DocumentRoot: "public",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Stop(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	req, _ := fakes.vhosts.lastSync()
	if req.Sites[0].DesiredState != "stopped" {
		t.Fatalf("stop sync state = %q", req.Sites[0].DesiredState)
	}
	stopped, err := svc.Get(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != model.StatusStopped {
		t.Fatalf("status after stop = %q", stopped.Status)
	}

	if err := svc.Start(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	req, _ = fakes.vhosts.lastSync()
	if req.Sites[0].DesiredState != "running" {
		t.Fatalf("start sync state = %q", req.Sites[0].DesiredState)
	}
	running, err := svc.List(context.Background())
	if err != nil || len(running) != 1 || running[0].Status != model.StatusRunning {
		t.Fatalf("list = %+v, err = %v", running, err)
	}
	if fakes.backend.started[site.ID] {
		t.Fatal("static backend started in external mode")
	}

	if err := os.Rename(public, public+".gone"); err != nil {
		t.Fatal(err)
	}
	broken, err := svc.Get(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if broken.Status != model.StatusError {
		t.Fatalf("status with missing root = %q, want error", broken.Status)
	}
	// A running site with a missing root is published as a 503 without
	// changing the stored desired state.
	if err := svc.SyncVhosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	req, _ = fakes.vhosts.lastSync()
	if req.Sites[0].DesiredState != "stopped" {
		t.Fatalf("broken running site sync state = %q, want stopped", req.Sites[0].DesiredState)
	}
	stored, err := fakes.store.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DesiredState != model.DesiredRunning {
		t.Fatalf("stored desired state = %q, want running", stored.DesiredState)
	}
	if err := svc.Stop(context.Background(), site.ID); err != nil {
		t.Fatalf("stop after root deletion: %v", err)
	}
	req, _ = fakes.vhosts.lastSync()
	if req.Sites[0].DesiredState != "stopped" {
		t.Fatalf("stop sync state = %q", req.Sites[0].DesiredState)
	}
	if got := fakes.backend.stopCount(); got != 0 {
		t.Fatalf("external static lifecycle called Backend.Stop %d times", got)
	}
}

func TestSyncDegradesBrokenSitesInsteadOfFailing(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)
	ctx := context.Background()

	good, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	badNode, err := svc.Create(ctx, CreateRequest{
		Name: "shop", Path: fakes.root, Kind: "node", NodeVersion: "24", Command: "node server.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "index.html"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	badStatic, err := svc.Create(ctx, CreateRequest{
		Name: "docs", Path: fakes.root, Kind: "static", DocumentRoot: outside,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Break the Node site's recorded backend path after creation.
	stored, err := fakes.store.GetSite(ctx, badNode.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.BackendPath = filepath.Join(t.TempDir(), "missing")
	if err := fakes.store.UpdateSite(ctx, stored); err != nil {
		t.Fatal(err)
	}

	if err := svc.SyncVhosts(ctx); err != nil {
		t.Fatalf("one broken site poisoned the sync: %v", err)
	}
	req, ok := fakes.vhosts.lastSync()
	if !ok {
		t.Fatal("vhosts were not synced")
	}
	if len(req.Sites) != 3 {
		t.Fatalf("sync request has %d sites, want 3: %+v", len(req.Sites), req.Sites)
	}

	byID := make(map[string]helperproto.VHostSite, len(req.Sites))
	for _, site := range req.Sites {
		byID[site.ID] = site
	}
	for _, broken := range []model.Site{badNode, badStatic} {
		spec := byID[broken.ID]
		if spec.DesiredState != string(model.DesiredStopped) {
			t.Errorf("broken site %s desired state = %q, want stopped", broken.Name, spec.DesiredState)
		}
		if spec.TLS || spec.ForceHTTPS || spec.DocumentRoot != "" || spec.BackendPort != 0 {
			t.Errorf("broken site %s was not degraded: %+v", broken.Name, spec)
		}
		if spec.Domain == "" {
			t.Errorf("broken site %s lost its domain, so it would not serve the 503 vhost", broken.Name)
		}
	}
	if spec := byID[good.ID]; spec.DesiredState != string(model.DesiredRunning) {
		t.Errorf("healthy site = %+v, want running", spec)
	}

	events, err := fakes.store.ListEvents(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, event := range events {
		if event.Type == "site.error" {
			failed++
		}
	}
	if failed < 2 {
		t.Fatalf("site.error events = %d, want at least one per broken site: %+v", failed, events)
	}
}

func TestExternalNodeStillStartsBackend(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)

	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "shop", Path: fakes.root, Kind: "node", NodeVersion: "24", Command: "node server.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	if site.HTTPPort == 0 || !fakes.backend.started[site.ID] {
		t.Fatalf("node site = %+v, started = %v", site, fakes.backend.started)
	}
	req, _ := fakes.vhosts.lastSync()
	if len(req.Sites) != 1 || req.Sites[0].BackendPort != site.HTTPPort {
		t.Fatalf("sync request = %+v", req.Sites)
	}
}

func TestExternalUpdateSyncFailureRestoresOldState(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)

	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "blog", Path: fakes.root, Kind: "static", TLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldDomain := site.Domain
	fakes.vhosts.err = errors.New("helper rejected the set")

	if _, err := svc.Update(context.Background(), site.ID, UpdateRequest{Domain: "new.test"}); err == nil {
		t.Fatal("want error")
	}
	stored, err := fakes.store.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Domain != oldDomain {
		t.Fatalf("stored domain = %q, want %q", stored.Domain, oldDomain)
	}
	if !fakes.hosts.has(oldDomain) || fakes.hosts.has("new.test") {
		t.Fatalf("hosts = %v", fakes.hosts.last())
	}
	if got := fakes.certs.ensuredFor(site.ID); len(got) != 1 || got[0] != oldDomain {
		t.Fatalf("old certificate not re-ensured: %v", got)
	}
	req, ok := fakes.vhosts.lastSync()
	if !ok || len(req.Sites) != 1 || req.Sites[0].Domain != oldDomain {
		t.Fatalf("rollback sync = %+v", req)
	}
}

func TestExternalDeleteSyncsRemainingSetBeforeCertDelete(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)
	site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}

	fakes.vhosts.err = errors.New("sync failed")
	baseline := fakes.vhosts.syncCount()
	stopsBefore := fakes.backend.stopCount()
	if err := svc.Delete(context.Background(), site.ID); err == nil {
		t.Fatal("want error")
	}
	if got := fakes.backend.stopCount(); got != stopsBefore {
		t.Fatalf("external static delete called Backend.Stop %d times", got-stopsBefore)
	}
	if _, err := fakes.store.GetSite(context.Background(), site.ID); err != nil {
		t.Fatalf("row removed after failed vhost sync: %v", err)
	}
	if fakes.certs.wasDeleted(site.ID) {
		t.Fatal("certificate deleted after failed vhost sync")
	}
	req, ok := fakes.vhosts.syncAt(baseline)
	if !ok || len(req.Sites) != 0 {
		t.Fatalf("sync before delete still included the site: %+v", req.Sites)
	}

	fakes.vhosts.err = nil
	*fakes.order = nil
	stopsBefore = fakes.backend.stopCount()
	if err := svc.Delete(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	if got := fakes.backend.stopCount(); got != stopsBefore {
		t.Fatalf("external static delete called Backend.Stop %d times", got-stopsBefore)
	}
	order := strings.Join(*fakes.order, ",")
	syncIndex := strings.Index(order, "vhost-sync")
	deleteIndex := strings.Index(order, "cert-delete:"+site.ID)
	if syncIndex < 0 || deleteIndex < 0 || syncIndex > deleteIndex {
		t.Fatalf("delete order = %q; vhost sync must precede certificate deletion", order)
	}
	if _, err := fakes.store.GetSite(context.Background(), site.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row lookup after delete = %v", err)
	}
}

func TestSwitchBuiltinExternalBuiltin(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
	if err != nil {
		t.Fatal(err)
	}
	if !fakes.backend.started[site.ID] {
		t.Fatal("builtin static site did not start a backend")
	}
	fakes.proxy.started = true

	if err := svc.SetServingMode(context.Background(), config.ServingModeNginx); err != nil {
		t.Fatal(err)
	}
	if fakes.proxy.isStarted() {
		t.Fatal("proxy still running after switching to nginx")
	}
	if !fakes.backend.stopped[site.ID] {
		t.Fatal("static backend not stopped for external mode")
	}
	if calls := fakes.vhosts.modeCalls; len(calls) == 0 || calls[len(calls)-1] != "nginx" {
		t.Fatalf("mode calls = %v", calls)
	}
	if req := fakes.vhosts.modeRequests[len(fakes.vhosts.modeRequests)-1]; req.Mode != "nginx" || len(req.Sites) != 1 {
		t.Fatalf("mode sync request = %+v", req)
	}
	assertSavedMode(t, svc, config.ServingModeNginx)

	if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err != nil {
		t.Fatal(err)
	}
	if !fakes.proxy.isStarted() {
		t.Fatal("proxy not started after switching to builtin")
	}
	if calls := fakes.vhosts.modeCalls; calls[len(calls)-1] != "builtin" {
		t.Fatalf("mode calls = %v", calls)
	}
	if !fakes.backend.started[site.ID] {
		t.Fatal("static backend not restarted for builtin mode")
	}
	stored, err := fakes.store.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.HTTPPort == 0 {
		t.Fatal("builtin restart did not allocate a static HTTP port")
	}
	assertSavedMode(t, svc, config.ServingModeBuiltin)
}

func TestSwitchFailureRollsBackRuntimeAndConfig(t *testing.T) {
	t.Run("static stop failure builtin to external", func(t *testing.T) {
		svc, fakes := newTestService(t)
		site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
		if err != nil {
			t.Fatal(err)
		}
		fakes.proxy.started = true
		fakes.backend.stopErr = errors.New("stop failed")

		if err := svc.SetServingMode(context.Background(), config.ServingModeNginx); err == nil {
			t.Fatal("want error")
		}
		if !fakes.backend.started[site.ID] {
			t.Fatal("static backend was not restarted after a partial stop")
		}
		if !fakes.proxy.isStarted() {
			t.Fatal("proxy changed after the stop failed")
		}
		if len(fakes.vhosts.modeCalls) != 0 {
			t.Fatalf("helper called after static stop failure: %v", fakes.vhosts.modeCalls)
		}
		assertSavedMode(t, svc, config.ServingModeBuiltin)
	})

	t.Run("helper failure builtin to external", func(t *testing.T) {
		svc, fakes := newTestService(t)
		site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
		if err != nil {
			t.Fatal(err)
		}
		fakes.proxy.started = true
		fakes.vhosts.modeErr = errors.New("forwarder failed")

		if err := svc.SetServingMode(context.Background(), config.ServingModeNginx); err == nil {
			t.Fatal("want error")
		}
		if !fakes.proxy.isStarted() {
			t.Fatal("proxy not restored")
		}
		if !fakes.backend.started[site.ID] {
			t.Fatal("static backend not restored")
		}
		assertSavedMode(t, svc, config.ServingModeBuiltin)
	})

	t.Run("proxy failure external to builtin", func(t *testing.T) {
		svc, fakes := newTestService(t)
		useServingMode(svc, fakes, config.ServingModeNginx)
		persistServingMode(t, svc, config.ServingModeNginx)
		if _, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err != nil {
			t.Fatal(err)
		}
		fakes.proxy.startErr = errors.New("port in use")
		if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err == nil {
			t.Fatal("want error")
		}
		if len(fakes.vhosts.modeCalls) != 0 {
			t.Fatalf("helper switched despite proxy failure: %v", fakes.vhosts.modeCalls)
		}
		assertSavedMode(t, svc, config.ServingModeNginx)
	})

	t.Run("helper failure external to builtin", func(t *testing.T) {
		svc, fakes := newTestService(t)
		useServingMode(svc, fakes, config.ServingModeNginx)
		persistServingMode(t, svc, config.ServingModeNginx)
		if _, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err != nil {
			t.Fatal(err)
		}
		fakes.vhosts.modeErr = errors.New("teardown failed")
		if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err == nil {
			t.Fatal("want error")
		}
		if fakes.proxy.isStarted() {
			t.Fatal("proxy left running after failed teardown")
		}
		assertSavedMode(t, svc, config.ServingModeNginx)
	})

	t.Run("config save failure builtin to external", func(t *testing.T) {
		svc, fakes := newTestService(t)
		site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
		if err != nil {
			t.Fatal(err)
		}
		fakes.proxy.started = true
		notADir := filepath.Join(t.TempDir(), "home-is-a-file")
		if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		svc.deps.Config.Home = notADir

		if err := svc.SetServingMode(context.Background(), config.ServingModeNginx); err == nil {
			t.Fatal("want error")
		}
		if !fakes.proxy.isStarted() || !fakes.backend.started[site.ID] {
			t.Fatalf("local runtime not restored: proxy=%v backend=%v", fakes.proxy.isStarted(), fakes.backend.started[site.ID])
		}
		calls := fakes.vhosts.modeCalls
		if len(calls) < 2 || calls[len(calls)-1] != "builtin" {
			t.Fatalf("helper mode not rolled back: %v", calls)
		}
		if svc.deps.Config.ServingMode != config.ServingModeBuiltin {
			t.Fatalf("in-memory mode = %q", svc.deps.Config.ServingMode)
		}
	})
}

func TestSetServingModeRepairsHelperMismatch(t *testing.T) {
	t.Run("config builtin but helper external", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.vhosts.status.Mode = config.ServingModeNginx
		fakes.proxy.started = true
		fakes.proxy.startErr = proxy.ErrAlreadyStarted

		if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err != nil {
			t.Fatal(err)
		}
		calls := fakes.vhosts.modeCalls
		if len(calls) != 1 || calls[0] != config.ServingModeBuiltin {
			t.Fatalf("helper mode calls = %v, want one builtin switch", calls)
		}
		if !fakes.proxy.isStarted() {
			t.Fatal("builtin proxy is not running after the repair")
		}
		assertSavedMode(t, svc, config.ServingModeBuiltin)
	})

	t.Run("config external but helper builtin", func(t *testing.T) {
		svc, fakes := newTestService(t)
		persistServingMode(t, svc, config.ServingModeNginx)
		fakes.vhosts.status.Mode = config.ServingModeBuiltin
		if _, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err != nil {
			t.Fatal(err)
		}
		// Simulate a helper that was switched to builtin out of band.
		fakes.vhosts.status.Mode = config.ServingModeBuiltin
		callsBefore := len(fakes.vhosts.modeCalls)
		if err := svc.SetServingMode(context.Background(), config.ServingModeNginx); err != nil {
			t.Fatal(err)
		}
		calls := fakes.vhosts.modeCalls[callsBefore:]
		if len(calls) != 1 || calls[0] != config.ServingModeNginx {
			t.Fatalf("helper mode calls = %v, want one nginx switch", calls)
		}
		req := fakes.vhosts.modeRequests[len(fakes.vhosts.modeRequests)-1]
		if req.Mode != config.ServingModeNginx || len(req.Sites) != 1 {
			t.Fatalf("repair request = %+v", req)
		}
		assertSavedMode(t, svc, config.ServingModeNginx)
	})
}

func TestSyncVhostsRepairsHelperModeMismatch(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)
	if _, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err != nil {
		t.Fatal(err)
	}
	fakes.vhosts.status.Mode = config.ServingModeBuiltin
	syncsBefore := fakes.vhosts.syncCount()
	modesBefore := len(fakes.vhosts.modeCalls)

	if err := svc.SyncVhosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fakes.vhosts.syncCount(); got != syncsBefore {
		t.Fatalf("plain syncs = %d, want no plain sync when the helper is builtin", got-syncsBefore)
	}
	calls := fakes.vhosts.modeCalls[modesBefore:]
	if len(calls) != 1 || calls[0] != config.ServingModeNginx {
		t.Fatalf("mode calls = %v, want one nginx switch", calls)
	}
	req := fakes.vhosts.modeRequests[len(fakes.vhosts.modeRequests)-1]
	if req.Mode != config.ServingModeNginx || len(req.Sites) != 1 {
		t.Fatalf("repair request = %+v", req)
	}
}

func TestReconcileServingMode(t *testing.T) {
	t.Run("builtin config wins over external helper", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.vhosts.status.Mode = "nginx"
		if err := svc.ReconcileServingMode(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls := fakes.vhosts.modeCalls; len(calls) != 1 || calls[0] != "builtin" {
			t.Fatalf("mode calls = %v", calls)
		}
	})
	t.Run("external config reconciles helper and syncs", func(t *testing.T) {
		svc, fakes := newTestService(t)
		useServingMode(svc, fakes, config.ServingModeNginx)
		fakes.vhosts.status.Mode = "apache"
		if err := svc.ReconcileServingMode(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls := fakes.vhosts.modeCalls; len(calls) != 1 || calls[0] != "nginx" {
			t.Fatalf("mode calls = %v", calls)
		}
		req := fakes.vhosts.modeRequests[0]
		if req.Mode != "nginx" || req.Dashboard.Domain != "devstack.test" {
			t.Fatalf("mode request = %+v", req)
		}
	})
	t.Run("matching external mode syncs without a switch", func(t *testing.T) {
		svc, fakes := newTestService(t)
		useServingMode(svc, fakes, config.ServingModeNginx)
		if err := svc.ReconcileServingMode(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(fakes.vhosts.modeCalls) != 0 || fakes.vhosts.syncCount() != 1 {
			t.Fatalf("mode calls = %v, syncs = %d", fakes.vhosts.modeCalls, fakes.vhosts.syncCount())
		}
	})
	t.Run("not implemented builtin is tolerated", func(t *testing.T) {
		svc, fakes := newTestService(t)
		fakes.vhosts.statusErr = helperproto.ErrNotImplemented
		status, err := svc.VhostStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status.Mode != "builtin" || !status.ConfigOK {
			t.Fatalf("status = %+v", status)
		}
		if err := svc.ReconcileServingMode(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("not implemented external propagates", func(t *testing.T) {
		svc, fakes := newTestService(t)
		useServingMode(svc, fakes, config.ServingModeNginx)
		fakes.vhosts.statusErr = helperproto.ErrNotImplemented
		if _, err := svc.VhostStatus(context.Background()); !errors.Is(err, helperproto.ErrNotImplemented) {
			t.Fatalf("status error = %v", err)
		}
		if err := svc.ReconcileServingMode(context.Background()); !errors.Is(err, helperproto.ErrNotImplemented) {
			t.Fatalf("reconcile error = %v", err)
		}
	})
}

func TestExternalNodeStartStopSyncFailureRestoresState(t *testing.T) {
	svc, fakes := newTestService(t)
	useServingMode(svc, fakes, config.ServingModeNginx)
	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "shop", Path: fakes.root, Kind: "node", NodeVersion: "24", Command: "node server.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	fakes.vhosts.err = errors.New("sync failed")
	if err := svc.Start(context.Background(), site.ID); err == nil {
		t.Fatal("want start error")
	}
	stored, err := fakes.store.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DesiredState != model.DesiredStopped || stored.Status != model.StatusStopped {
		t.Fatalf("row after failed start = %+v", stored)
	}
	if state := fakes.backend.statusFor(site.ID); state != model.StatusStopped {
		t.Fatalf("backend after failed start = %q", state)
	}

	fakes.vhosts.err = nil
	if err := svc.Start(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	fakes.vhosts.err = errors.New("sync failed")
	if err := svc.Stop(context.Background(), site.ID); err == nil {
		t.Fatal("want stop error")
	}
	stored, err = fakes.store.GetSite(context.Background(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DesiredState != model.DesiredRunning || stored.Status != model.StatusRunning {
		t.Fatalf("row after failed stop = %+v", stored)
	}
	if state := fakes.backend.statusFor(site.ID); state != model.StatusRunning {
		t.Fatalf("backend after failed stop = %q", state)
	}
}

func TestPreviewVhostRendersWithoutHelper(t *testing.T) {
	svc, fakes := newTestService(t)
	site, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static", TLS: true})
	if err != nil {
		t.Fatal(err)
	}
	fakes.vhosts.err = errors.New("helper must not be called")
	syncs := fakes.vhosts.syncCount()
	modes := len(fakes.vhosts.modeCalls)

	content, err := svc.PreviewVhost(context.Background(), site.ID, "nginx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "server_name blog.test;") {
		t.Fatalf("preview = %q", content)
	}
	if fakes.vhosts.syncCount() != syncs || len(fakes.vhosts.modeCalls) != modes {
		t.Fatal("preview called the helper")
	}
	if _, err := svc.PreviewVhost(context.Background(), site.ID, "caddy"); err == nil {
		t.Fatal("unsupported server accepted")
	}
	if _, err := svc.PreviewVhost(context.Background(), "missing", "nginx"); err == nil {
		t.Fatal("missing site accepted")
	}
	apache, err := svc.PreviewVhost(context.Background(), site.Name, "apache")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(apache, "ServerName blog.test") {
		t.Fatalf("apache preview = %q", apache)
	}
}

func assertSavedMode(t *testing.T, svc *Service, want string) {
	t.Helper()
	if svc.deps.Config.ServingMode != want {
		t.Fatalf("in-memory serving mode = %q, want %q", svc.deps.Config.ServingMode, want)
	}
	got, err := config.Load(svc.deps.Config.Home)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServingMode != want {
		t.Fatalf("saved serving mode = %q, want %q", got.ServingMode, want)
	}
	if svc.deps.Store != nil {
		stored, err := svc.deps.Store.GetSetting(context.Background(), "serving_mode")
		switch {
		case err == nil:
			if stored != want {
				t.Fatalf("stored serving_mode setting = %q, want %q", stored, want)
			}
		case errors.Is(err, store.ErrNotFound):
			// A switch that never succeeded has no persisted setting.
		default:
			t.Fatal(err)
		}
	}
}
