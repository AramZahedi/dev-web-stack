package sites

import (
	"context"
	"crypto/tls"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeDockerSyncer struct {
	mu        sync.Mutex
	calls     []string
	err       error
	lastSync  time.Time
	configOK  bool
	lastErr   string
	routable  func(model.Site) bool
	reloadErr error
	reloads   []string
}

func (f *fakeDockerSyncer) Sync(_ context.Context, server string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, server)
	return f.err
}

func (f *fakeDockerSyncer) DockerStatus() (time.Time, bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastSync, f.configOK, f.lastErr
}

func (f *fakeDockerSyncer) DockerRoutable(site model.Site) bool {
	if f.routable != nil {
		return f.routable(site)
	}
	return true
}

func (f *fakeDockerSyncer) DockerReload(_ context.Context, server string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads = append(f.reloads, server)
	return f.reloadErr
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

type fakeDockerSwitch struct {
	mu       sync.Mutex
	stopped  []string
	started  []string
	stopErr  error
	startErr error
}

func (f *fakeDockerSwitch) StopActiveServer(_ context.Context, server string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, server)
	return f.stopErr
}

func (f *fakeDockerSwitch) StartActiveServer(_ context.Context, server string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, server)
	return f.startErr
}

func (f *fakeDockerSwitch) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stopped), len(f.started)
}

func TestSetServingModeTakesSharedLifecycleLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows external modes delegate to the Docker service manager")
	}
	svc, fakes := newTestService(t)
	if _, err := svc.Create(context.Background(), CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err != nil {
		t.Fatal(err)
	}
	coordinator := &sync.Mutex{}
	svc.deps.Lifecycle = coordinator
	coordinator.Lock()

	done := make(chan error, 1)
	go func() {
		done <- svc.SetServingMode(context.Background(), config.ServingModeNginx)
	}()
	select {
	case err := <-done:
		t.Fatalf("SetServingMode returned while the lifecycle lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	coordinator.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("SetServingMode: %v", err)
	}
}

type fakeDockerRoute struct {
	active bool
	host   string
	port   int
	keep   map[string]bool
	err    error
}

func (r *fakeDockerRoute) UpdateDockerWebRoutes(_ context.Context, active bool, host string, port int) error {
	return r.UpdateDockerWebRoutesExcept(context.Background(), active, host, port, nil)
}

func (r *fakeDockerRoute) UpdateDockerWebRoutesExcept(_ context.Context, active bool, host string, port int, keep map[string]bool) error {
	r.keep = keep
	r.active, r.host, r.port = active, host, port
	return r.err
}

func TestDockerRouteRedirectsOnlyWhenSyncerHealthy(t *testing.T) {
	svc, _ := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	svc.deps.DockerSyncer = &fakeDockerSyncer{configOK: true}
	route := &fakeDockerRoute{}
	svc.deps.DockerRoute = route
	svc.deps.DockerRouteHost = "127.0.0.1"
	svc.deps.DockerRoutePort = 18080
	if err := svc.RebuildRoutes(context.Background()); err != nil {
		t.Fatalf("RebuildRoutes: %v", err)
	}
	if !route.active || route.host != "127.0.0.1" || route.port != 18080 {
		t.Fatalf("route = %+v, want active 127.0.0.1:18080", route)
	}

	svc.deps.DockerSyncer = &fakeDockerSyncer{configOK: false}
	route.active = false
	if err := svc.RebuildRoutes(context.Background()); err != nil {
		t.Fatalf("RebuildRoutes with unhealthy sync: %v", err)
	}
	if route.active {
		t.Fatal("unhealthy Docker sync still redirected routes")
	}
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
	if len(syncer.reloads) != 1 || syncer.reloads[0] != "nginx" {
		t.Fatalf("docker reloads = %v, want nginx", syncer.reloads)
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

func TestDockerToBuiltinTransitionStopsContainerAndRestoresBuiltin(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	switcher := &fakeDockerSwitch{}
	forwarders := &fakeForwarders{}
	svc.deps.DockerSwitch = switcher
	svc.deps.Forwarders = forwarders

	if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err != nil {
		t.Fatalf("SetServingMode: %v", err)
	}
	stopped, started := switcher.counts()
	if stopped != 1 || switcher.stopped[0] != "nginx" {
		t.Fatalf("docker stops = %v, want nginx", switcher.stopped)
	}
	if started != 0 {
		t.Fatalf("docker starts = %v, want none", switcher.started)
	}
	if !fakes.proxy.isStarted() {
		t.Fatal("builtin proxy was not restored")
	}
	call, ok := forwarders.last()
	if !ok || !call.enable {
		t.Fatalf("forwarders = %+v, want enabled", forwarders.calls)
	}
	if svc.deps.Config.ServingMode != config.ServingModeBuiltin || svc.deps.Config.ServingProvider != config.ServingProviderNative {
		t.Fatalf("config = %s/%s, want builtin/native", svc.deps.Config.ServingMode, svc.deps.Config.ServingProvider)
	}
}

func TestDockerToBuiltinStopFailureLeavesDockerServing(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeApache
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	switcher := &fakeDockerSwitch{stopErr: errors.New("container busy")}
	forwarders := &fakeForwarders{}
	svc.deps.DockerSwitch = switcher
	svc.deps.Forwarders = forwarders

	err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin)
	if err == nil {
		t.Fatal("expected stop failure")
	}
	if fakes.proxy.isStarted() {
		t.Fatal("builtin proxy must not start when the container stop failed")
	}
	if _, ok := forwarders.last(); ok {
		t.Fatal("builtin forwarders must not be enabled when the container stop failed")
	}
	if svc.deps.Config.ServingMode != config.ServingModeApache || svc.deps.Config.ServingProvider != config.ServingProviderDocker {
		t.Fatalf("config = %s/%s, want apache/docker", svc.deps.Config.ServingMode, svc.deps.Config.ServingProvider)
	}
}

func TestDockerToBuiltinSaveFailureStillRestoresBuiltin(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	switcher := &fakeDockerSwitch{}
	forwarders := &fakeForwarders{}
	svc.deps.DockerSwitch = switcher
	svc.deps.Forwarders = forwarders
	// Force saveServingStateLocked to fail after the container is stopped and
	// the builtin runtime is back up.
	svc.deps.Config.Home = ""

	err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin)
	if err == nil {
		t.Fatal("expected save failure")
	}
	stopped, _ := switcher.counts()
	if stopped != 1 {
		t.Fatalf("docker stops = %v, want one", switcher.stopped)
	}
	if !fakes.proxy.isStarted() {
		t.Fatal("80/443 must remain served by the builtin proxy after a save failure")
	}
	call, ok := forwarders.last()
	if !ok || !call.enable {
		t.Fatalf("forwarders = %+v, want enabled", forwarders.calls)
	}
}

func TestDockerToBuiltinStaticFailureStillRestoresBuiltin(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	switcher := &fakeDockerSwitch{}
	forwarders := &fakeForwarders{}
	svc.deps.DockerSwitch = switcher
	svc.deps.Forwarders = forwarders
	fakes.backend.startErr = errors.New("static backend failed")
	site := model.Site{
		ID:           "static",
		Name:         "static",
		Domain:       "static.test",
		Kind:         model.SiteStatic,
		HostPath:     fakes.root,
		BackendPath:  fakes.root,
		DocumentRoot: ".",
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
		HTTPPort:     41001,
	}
	if err := fakes.store.CreateSite(context.Background(), site); err != nil {
		t.Fatalf("create static site: %v", err)
	}

	if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err != nil {
		t.Fatalf("one failed static site must not block the builtin fallback: %v", err)
	}
	if !fakes.proxy.isStarted() {
		t.Fatal("builtin proxy did not start")
	}
	call, ok := forwarders.last()
	if !ok || !call.enable {
		t.Fatalf("forwarders = %+v, want enabled", forwarders.calls)
	}
	stopped, started := switcher.counts()
	if stopped != 1 || started != 0 {
		t.Fatalf("switcher stop=%v start=%v, want only the docker stop", switcher.stopped, switcher.started)
	}
	if svc.ServingMode() != config.ServingModeBuiltin || svc.ServingProvider() != config.ServingProviderNative {
		t.Fatalf("serving state = %s/%s, want builtin/native", svc.ServingMode(), svc.ServingProvider())
	}
}

func TestDockerToBuiltinSaveFailureReportsDegradedStatus(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	switcher := &fakeDockerSwitch{}
	svc.deps.DockerSwitch = switcher
	svc.deps.Forwarders = &fakeForwarders{}
	svc.deps.DockerSyncer = &fakeDockerSyncer{configOK: true}
	home := svc.deps.Config.Home
	svc.deps.Config.Home = ""

	if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err == nil {
		t.Fatal("expected save failure")
	}
	status, err := svc.VhostStatus(context.Background())
	if err != nil {
		t.Fatalf("VhostStatus: %v", err)
	}
	if status.ConfigOK {
		t.Fatal("a failed builtin state save must not advertise Docker config as OK")
	}
	if !strings.Contains(status.LastError, "saving builtin/native") {
		t.Fatalf("LastError = %q, want the save recovery error", status.LastError)
	}

	// Recovery: after storage is fixed, retrying the builtin switch persists
	// state without stopping an already-stopped container again.
	svc.deps.Config.Home = home
	if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err != nil {
		t.Fatalf("recovery retry: %v", err)
	}
	if svc.deps.Config.ServingMode != config.ServingModeBuiltin || svc.deps.Config.ServingProvider != config.ServingProviderNative {
		t.Fatalf("recovered config = %s/%s", svc.deps.Config.ServingMode, svc.deps.Config.ServingProvider)
	}
	stopped, _ := switcher.counts()
	if stopped != 1 {
		t.Fatalf("recovery must not stop the already-stopped container again: %v", switcher.stopped)
	}
	_ = fakes
}

func TestDockerProviderWithBuiltinModeIsRejected(t *testing.T) {
	svc, _ := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeBuiltin
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	switcher := &fakeDockerSwitch{}
	svc.deps.DockerSwitch = switcher
	if err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin); err == nil {
		t.Fatal("provider=docker with mode=builtin must return a clear inconsistency error")
	}
	stopped, _ := switcher.counts()
	if stopped != 0 {
		t.Fatalf("inconsistent state must not guess a container to stop: %v", switcher.stopped)
	}
}

func TestDockerToBuiltinStartFailureRestartsContainer(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	switcher := &fakeDockerSwitch{}
	svc.deps.DockerSwitch = switcher
	svc.deps.Forwarders = &fakeForwarders{}
	svc.deps.Proxy = &fakeProxy{startErr: errors.New("proxy bind failed")}

	err := svc.SetServingMode(context.Background(), config.ServingModeBuiltin)
	if err == nil {
		t.Fatal("expected builtin start failure")
	}
	stopped, started := switcher.counts()
	if stopped != 1 || started != 1 || switcher.started[0] != "nginx" {
		t.Fatalf("switcher stop=%v start=%v, want rollback start nginx", switcher.stopped, switcher.started)
	}
	_ = fakes
}

func TestDockerToNativeTransitionStillRefused(t *testing.T) {
	svc, _ := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	svc.deps.DockerSwitch = &fakeDockerSwitch{}
	if err := svc.SetServingMode(context.Background(), config.ServingModeApache); err == nil {
		t.Fatal("expected docker->other external mode to be refused")
	}
}

func TestVhostStatusExposesDockerSyncState(t *testing.T) {
	svc, _ := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	last := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	svc.deps.DockerSyncer = &fakeDockerSyncer{
		lastSync: last,
		configOK: false,
		lastErr:  "config/conf.d/generated/app.conf",
	}

	status, err := svc.VhostStatus(context.Background())
	if err != nil {
		t.Fatalf("VhostStatus: %v", err)
	}
	if status.ConfigOK {
		t.Fatal("docker status must not report ConfigOK when the last sync failed")
	}
	if status.LastError != "config/conf.d/generated/app.conf" {
		t.Fatalf("LastError = %q", status.LastError)
	}
	if !status.LastSync.Equal(last) {
		t.Fatalf("LastSync = %v, want %v", status.LastSync, last)
	}
	if status.Mode != "nginx" || status.Server != "nginx" {
		t.Fatalf("mode/server = %s/%s", status.Mode, status.Server)
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

func TestDockerProviderExternalStaticSiteRoutesToPrivateServer(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	svc.deps.DockerSyncer = &fakeDockerSyncer{configOK: true}
	svc.deps.DockerRoute = &fakeDockerRoute{}
	svc.deps.DockerRouteHost = "127.0.0.1"
	svc.deps.DockerRoutePort = 18080
	site := model.Site{
		ID:           "static",
		Name:         "static",
		Domain:       "static.test",
		Kind:         model.SiteStatic,
		SourceMode:   model.SourceWSL,
		BackendPath:  "/data/devstack/projects/static",
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
		TLS:          true,
	}
	if err := fakes.store.CreateSite(context.Background(), site); err != nil {
		t.Fatalf("create static site: %v", err)
	}
	if err := svc.RebuildRoutes(context.Background()); err != nil {
		t.Fatalf("RebuildRoutes: %v", err)
	}
	route, ok := fakes.router.Lookup("static.test")
	if !ok {
		t.Fatal("external static site has no proxy route")
	}
	if route.Upstream != "127.0.0.1:18080" {
		t.Fatalf("upstream = %q, want 127.0.0.1:18080", route.Upstream)
	}
	if route.Kind != "node" || !route.TLS {
		t.Fatalf("route = %+v, want KindNode/TLS", route)
	}
}

func TestStoppedBuildSitePublishesDockerRouteAndCertificate(t *testing.T) {
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	svc.deps.DockerSyncer = &fakeDockerSyncer{configOK: true}
	svc.deps.DockerRoute = &fakeDockerRoute{}
	svc.deps.DockerRouteHost = "127.0.0.1"
	svc.deps.DockerRoutePort = 18080
	site, err := svc.Create(context.Background(), CreateRequest{
		Name: "app",
		Path: fakes.root,
		Kind: "static",
		Build: &model.BuildProfile{
			NodeVersion: "22",
			Command:     "npm run build",
			Output:      "dist",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	route, ok := fakes.router.Lookup(site.Domain)
	if !ok {
		t.Fatal("stopped build site published no route")
	}
	if route.State != string(model.StatusStopped) {
		t.Fatalf("route state = %q, want stopped", route.State)
	}
	cert, err := fakes.router.Certificate(&tls.ClientHelloInfo{ServerName: site.Domain})
	if err != nil {
		t.Fatal(err)
	}
	if cert != fakes.certs.certFor(site.ID) {
		t.Fatal("stopped build site did not publish its own certificate")
	}
}

func TestDockerProviderWindowsStaticKeepsSupervisedBackend(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows source mode is only valid on Windows")
	}
	svc, fakes := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	svc.deps.DockerSyncer = &fakeDockerSyncer{
		configOK: true,
		routable: func(site model.Site) bool {
			return site.SourceMode != model.SourceWindows
		},
	}
	route := &fakeDockerRoute{}
	svc.deps.DockerRoute = route
	svc.deps.DockerRouteHost = "127.0.0.1"
	svc.deps.DockerRoutePort = 18080
	site, err := svc.Create(context.Background(), CreateRequest{
		Name:       "win",
		Path:       fakes.root,
		Kind:       "static",
		SourceMode: "windows",
		TLS:        true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if site.HTTPPort == 0 {
		t.Fatal("Windows static site has no backend port")
	}
	if !fakes.backend.started[site.ID] {
		t.Fatal("Windows static backend was not started")
	}
	direct, ok := fakes.router.Lookup("win.test")
	if !ok {
		t.Fatal("Windows static site has no proxy route")
	}
	if direct.Upstream == "127.0.0.1:18080" {
		t.Fatal("Windows static site was routed to the Docker server")
	}
	if !route.keep["win.test"] {
		t.Fatalf("Docker route keep = %v, want win.test", route.keep)
	}
}

func TestStartStaticBackendsBestEffortIgnoresFailedSite(t *testing.T) {
	svc, fakes := newTestService(t)
	site := model.Site{
		ID:           "static",
		Name:         "static",
		Domain:       "static.test",
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
	fakes.backend.startErr = errors.New("backend failed")
	if err := svc.startStaticBackendsBestEffortLocked(context.Background()); err != nil {
		t.Fatalf("best-effort start should not block a serving transition: %v", err)
	}
}

func TestStopStaticBackendsSkipsSitesWithoutBackend(t *testing.T) {
	svc, fakes := newTestService(t)
	site := model.Site{
		ID:           "static",
		Name:         "static",
		Domain:       "static.test",
		Kind:         model.SiteStatic,
		DesiredState: model.DesiredRunning,
		Status:       model.StatusRunning,
	}
	if err := fakes.store.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if err := svc.stopStaticBackendsLocked(context.Background()); err != nil {
		t.Fatalf("stopStaticBackendsLocked: %v", err)
	}
	if got := fakes.backend.stopCount(); got != 0 {
		t.Fatalf("backend Stop calls = %d, want 0 for a site without a backend", got)
	}
}

func TestExternalStaticNeedsBackendDuringBuiltinTransition(t *testing.T) {
	svc, _ := newTestService(t)
	svc.deps.Config.ServingMode = config.ServingModeNginx
	svc.deps.Config.ServingProvider = config.ServingProviderDocker
	svc.deps.DockerSyncer = &fakeDockerSyncer{}
	site := model.Site{
		Kind:        model.SiteStatic,
		SourceMode:  model.SourceWSL,
		BackendPath: "/data/devstack/projects/static",
	}
	if !svc.externalStatic(site) {
		t.Fatal("WSL static site is not external while Docker serving is active")
	}
	svc.suppressDockerRoutes = true
	if svc.externalStatic(site) {
		t.Fatal("WSL static site stayed external during the builtin transition")
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
