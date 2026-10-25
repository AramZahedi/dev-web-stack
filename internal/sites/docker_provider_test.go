package sites

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeDockerSyncer struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakeDockerSyncer) Sync(_ context.Context, server string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, server)
	return f.err
}

func (f *fakeDockerSyncer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type forwarderCall struct {
	httpPort  int
	httpsPort int
	enable    bool
}

type fakeForwarders struct {
	mu    sync.Mutex
	calls []forwarderCall
	err   error
}

func (f *fakeForwarders) SetForwarders(_ context.Context, httpPort, httpsPort int, enable bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, forwarderCall{httpPort: httpPort, httpsPort: httpsPort, enable: enable})
	return f.err
}

func (f *fakeForwarders) last() (forwarderCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return forwarderCall{}, false
	}
	return f.calls[len(f.calls)-1], true
}

func TestDockerProviderSyncVhostsUsesDockerSyncer(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	syncer := &fakeDockerSyncer{}
	svc.deps.DockerSyncer = syncer

	if err := svc.SyncVhosts(context.Background()); err != nil {
		t.Fatalf("SyncVhosts: %v", err)
	}
	if syncer.count() != 1 || syncer.calls[0] != "nginx" {
		t.Fatalf("docker sync calls = %v, want nginx", syncer.calls)
	}
	if fakes.vhosts.syncCount() != 0 || len(fakes.vhosts.modeCalls) != 0 {
		t.Fatalf("native vhosts were called in docker mode: sync=%d modes=%v", fakes.vhosts.syncCount(), fakes.vhosts.modeCalls)
	}
}

func TestDockerProviderReconcileUsesDockerSyncer(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeApache
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	syncer := &fakeDockerSyncer{}
	svc.deps.DockerSyncer = syncer

	if err := svc.ReconcileServingMode(context.Background()); err != nil {
		t.Fatalf("ReconcileServingMode: %v", err)
	}
	if syncer.count() != 1 || syncer.calls[0] != "apache" {
		t.Fatalf("docker sync calls = %v", syncer.calls)
	}
	if fakes.vhosts.statusCalls != 0 || fakes.vhosts.syncCount() != 0 {
		t.Fatal("ReconcileServingMode must not touch the native helper in docker mode")
	}
}

func TestDockerProviderSetServingModeRefused(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	syncer := &fakeDockerSyncer{}
	svc.deps.DockerSyncer = syncer

	err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin)
	if err == nil {
		t.Fatal("expected SetServingMode to refuse taking over docker state")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Fatalf("error = %v, want a docker provider explanation", err)
	}
	if len(fakes.vhosts.modeCalls) != 0 || syncer.count() != 0 {
		t.Fatal("SetServingMode must not change docker or native serving state")
	}
}

func TestDockerProviderSyncFailureIsReturned(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	svc.deps.DockerSyncer = &fakeDockerSyncer{err: errors.New("render conflict")}

	if err := svc.SyncVhosts(context.Background()); err == nil {
		t.Fatal("expected sync error")
	}
	if fakes.vhosts.syncCount() != 0 {
		t.Fatal("native helper must not be called")
	}
}

func TestDockerProviderRequiresDockerSyncer(t *testing.T) {
	svc, _ := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker

	if err := svc.SyncVhosts(context.Background()); err == nil {
		t.Fatal("expected missing docker syncer error")
	}
}

func TestStopForDockerStopsProxyAndForwarders(t *testing.T) {
	svc, fakes := newTestService(t)
	forwarders := &fakeForwarders{}
	svc.deps.Forwarders = forwarders
	if err := fakes.proxy.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := svc.StopForDocker(context.Background()); err != nil {
		t.Fatalf("StopForDocker: %v", err)
	}
	if fakes.proxy.isStarted() {
		t.Fatal("builtin proxy is still running")
	}
	call, ok := forwarders.last()
	if !ok || call.enable {
		t.Fatalf("forwarders last call = %+v, want disabled", call)
	}
	if call.httpPort != svc.deps.Config.HTTPPort || call.httpsPort != svc.deps.Config.HTTPSPort {
		t.Fatalf("forwarders ports = %d/%d", call.httpPort, call.httpsPort)
	}
}

func TestStartAfterDockerStartsProxyAndForwarders(t *testing.T) {
	svc, fakes := newTestService(t)
	forwarders := &fakeForwarders{}
	svc.deps.Forwarders = forwarders

	if err := svc.StartAfterDocker(context.Background()); err != nil {
		t.Fatalf("StartAfterDocker: %v", err)
	}
	if !fakes.proxy.isStarted() {
		t.Fatal("builtin proxy was not started")
	}
	call, ok := forwarders.last()
	if !ok || !call.enable {
		t.Fatalf("forwarders last call = %+v, want enabled", call)
	}
}

func TestStartAfterDockerRestoresProxyWhenForwardersFail(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Forwarders = &fakeForwarders{err: errors.New("forwarder failed")}
	if err := svc.StartAfterDocker(context.Background()); err == nil {
		t.Fatal("expected forwarder error")
	}
	if fakes.proxy.isStarted() {
		t.Fatal("proxy must be stopped again after a failed builtin restore")
	}
}

func TestDockerProviderStaticSitesStillNeedNoBackend(t *testing.T) {
	svc, _ := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	site := model.Site{Kind: model.SiteStatic}
	if svc.siteNeedsBackend(site) {
		t.Fatal("static site must not need a backend with the docker provider")
	}
	site.Kind = model.SitePHP
	if !svc.siteNeedsBackend(site) {
		t.Fatal("PHP site must keep its native FastCGI backend")
	}
	site.Kind = model.SiteNode
	if !svc.siteNeedsBackend(site) {
		t.Fatal("Node site must keep its native process")
	}
}

func TestSaveServingStatePersistsProvider(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.SaveServingState(ctx, config.ServingModeNginx, config.ServingProviderDocker); err != nil {
		t.Fatalf("SaveServingState: %v", err)
	}
	if svc.deps.Config.ServingMode != config.ServingModeNginx || svc.deps.Config.ServingProvider != config.ServingProviderDocker {
		t.Fatalf("in-memory state = %s/%s", svc.deps.Config.ServingMode, svc.deps.Config.ServingProvider)
	}
	value, err := svc.deps.Store.GetSetting(ctx, "serving_provider")
	if err != nil || value != config.ServingProviderDocker {
		t.Fatalf("stored serving_provider = %q, %v", value, err)
	}
	value, err = svc.deps.Store.GetSetting(ctx, "serving_mode")
	if err != nil || value != config.ServingModeNginx {
		t.Fatalf("stored serving_mode = %q, %v", value, err)
	}

	if err := svc.SaveServingState(ctx, config.ServingModeBuiltin, config.ServingProviderDocker); err == nil {
		t.Fatal("docker provider with builtin mode must be rejected")
	}
	if svc.deps.Config.ServingMode != config.ServingModeNginx || svc.deps.Config.ServingProvider != config.ServingProviderDocker {
		t.Fatal("failed save must not change in-memory state")
	}
}
