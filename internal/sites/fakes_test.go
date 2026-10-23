package sites

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

// testFakes groups the simulated edges of the service with the real store,
// router, and allocator used by the tests.
type testFakes struct {
	store   *store.Store
	backend *fakeBackend
	hosts   *fakeHosts
	certs   *fakeCerts
	vhosts  *fakeVhosts
	proxy   *fakeProxy
	router  *proxy.Router
	ports   *domain.PortAllocator
	config  config.Config
	root    string
	order   *[]string
}

// newTestService builds a Service against a temporary SQLite database, a real
// router and port allocator, and deterministic ids ("site", "site-2", ...).
func newTestService(t *testing.T) (*Service, *testFakes) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	root := filepath.Join(dir, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create project root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	logStore, err := logs.New(filepath.Join(dir, "logs"), 10, 5)
	if err != nil {
		t.Fatalf("open log store: %v", err)
	}

	fb := &fakeBackend{
		started: make(map[string]bool),
		stopped: make(map[string]bool),
		status:  make(map[string]model.SiteStatus),
	}
	fh := &fakeHosts{}
	fc := &fakeCerts{
		store:     st,
		ensured:   make(map[string][]string),
		records:   make(map[string]model.CertRecord),
		certsByID: make(map[string]*tls.Certificate),
		pems:      make(map[string]fakePEM),
		deleted:   make(map[string]bool),
	}
	fv := &fakeVhosts{}
	fp := &fakeProxy{}
	order := []string{}
	fc.order = &order
	fv.order = &order

	cfg := config.Default()
	cfg.Home = dir
	router := proxy.NewRouter()
	ports := domain.NewPortAllocator()

	nextID := 0
	newID := func() string {
		nextID++
		if nextID == 1 {
			return "site"
		}
		return fmt.Sprintf("site-%d", nextID)
	}

	svc := NewService(Deps{
		Store:   st,
		Backend: fb,
		Helper:  fh,
		Certs:   fc,
		Vhosts:  fv,
		Proxy:   fp,
		Router:  router,
		Ports:   ports,
		Config:  cfg,
		Logs:    logStore,
		Now: func() time.Time {
			return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
		},
		NewID: newID,
	})

	return svc, &testFakes{
		store:   st,
		backend: fb,
		hosts:   fh,
		certs:   fc,
		vhosts:  fv,
		proxy:   fp,
		router:  router,
		ports:   ports,
		config:  cfg,
		root:    root,
		order:   &order,
	}
}

// fakeBackend is an in-memory backend.Backend.
type fakeBackend struct {
	mu sync.Mutex

	prepared       []backend.SiteSpec
	started        map[string]bool
	stopped        map[string]bool
	status         map[string]model.SiteStatus
	runtimeCalls   []string
	buildCalls     []string
	stopCalls      int
	prepareErr     error
	startErr       error
	stopErr        error
	statusErr      error
	ensureRTError  error
	ensureBuildErr error
}

var _ backend.Backend = (*fakeBackend)(nil)

func (f *fakeBackend) Prepare(_ context.Context, spec backend.SiteSpec) (backend.PreparedSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = append(f.prepared, spec)
	if f.prepareErr != nil {
		return backend.PreparedSite{}, f.prepareErr
	}
	return backend.PreparedSite{
		BackendHost: "127.0.0.1",
		HTTPPort:    spec.HTTPPort,
		FastCGIPort: spec.FastCGIPort,
	}, nil
}

func (f *fakeBackend) Start(_ context.Context, id string) (backend.ProcessInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return backend.ProcessInfo{}, f.startErr
	}
	f.started[id] = true
	f.status[id] = model.StatusRunning
	return backend.ProcessInfo{PID: 4242, StartedAt: time.Now()}, nil
}

func (f *fakeBackend) Stop(_ context.Context, id string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped[id] = true
	f.status[id] = model.StatusStopped
	return nil
}

func (f *fakeBackend) Status(_ context.Context, id string) (backend.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return backend.Status{}, f.statusErr
	}
	state, ok := f.status[id]
	if !ok {
		state = model.StatusStopped
	}
	return backend.Status{State: state}, nil
}

func (f *fakeBackend) Logs(context.Context, string, bool) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeBackend) Exec(context.Context, string, []string, map[string]string) (backend.Result, error) {
	return backend.Result{}, nil
}

func (f *fakeBackend) EnsureRuntime(_ context.Context, kind, version string) (model.RuntimeBuild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runtimeCalls = append(f.runtimeCalls, kind+"@"+version)
	if f.ensureRTError != nil {
		return model.RuntimeBuild{}, f.ensureRTError
	}
	return model.RuntimeBuild{
		ID:      kind + "-" + version,
		Kind:    kind,
		Version: version,
		Path:    filepath.Join("/fake", kind, version),
	}, nil
}

func (f *fakeBackend) EnsureRuntimeBuild(_ context.Context, buildID string) (model.RuntimeBuild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buildCalls = append(f.buildCalls, buildID)
	if f.ensureBuildErr != nil {
		return model.RuntimeBuild{}, f.ensureBuildErr
	}
	kind, version := "", ""
	if at := strings.Index(buildID, "@"); at > 0 {
		kind, version = buildID[:at], buildID[at+1:]
		if rev := strings.LastIndex(version, "-r"); rev > 0 {
			version = version[:rev]
		}
	}
	return model.RuntimeBuild{
		ID:      buildID,
		Kind:    kind,
		Version: version,
		Path:    filepath.Join("/fake", kind, buildID),
	}, nil
}

func (f *fakeBackend) RemoveRuntime(context.Context, string) error { return nil }

func (f *fakeBackend) InstallCA(context.Context, []byte) error { return nil }

func (f *fakeBackend) Health(context.Context) error { return nil }

func (f *fakeBackend) lastPrepared() (backend.SiteSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prepared) == 0 {
		return backend.SiteSpec{}, false
	}
	return f.prepared[len(f.prepared)-1], true
}

func (f *fakeBackend) runtimeList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.runtimeCalls...)
}

func (f *fakeBackend) setStatus(id string, state model.SiteStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = state
}

func (f *fakeBackend) statusFor(id string) model.SiteStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[id]
}

func (f *fakeBackend) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

// fakeHosts records every SyncHosts call. failAt fails the Nth call (1-based)
// without changing the current managed set, matching a helper that rejected the
// update.
type fakeHosts struct {
	mu sync.Mutex

	calls   [][]string
	current []string
	err     error
	failAt  int
}

var _ Hosts = (*fakeHosts)(nil)

func (f *fakeHosts) SyncHosts(_ context.Context, domains []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), domains...))
	if f.err != nil {
		return f.err
	}
	if f.failAt == len(f.calls) {
		return errors.New("fake hosts: sync failed")
	}
	f.current = append([]string(nil), domains...)
	return nil
}

func (f *fakeHosts) calledWith(domain string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if containsString(call, domain) {
			return true
		}
	}
	return false
}

func (f *fakeHosts) has(domain string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return containsString(f.current, domain)
}

func (f *fakeHosts) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.current...)
}

func (f *fakeHosts) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeHosts) anyCallWithAll(domains ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		all := true
		for _, domain := range domains {
			if !containsString(call, domain) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// fakeCerts implements the certificate manager boundary. It mirrors the real
// manager's contract closely enough for the service: successful issuance is
// persisted in the real store, and the service loads the returned record.
type fakeCerts struct {
	mu sync.Mutex

	store     *store.Store
	ensured   map[string][]string
	records   map[string]model.CertRecord
	certsByID map[string]*tls.Certificate
	pems      map[string]fakePEM
	deleted   map[string]bool
	serial    int
	order     *[]string

	err       error
	loadErr   error
	deleteErr error
	pemErr    error
}

type fakePEM struct {
	cert []byte
	key  []byte
}

var _ Certs = (*fakeCerts)(nil)

func (f *fakeCerts) Ensure(ctx context.Context, owner string, domains []string) (model.CertRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return model.CertRecord{}, f.err
	}
	if strings.TrimSpace(owner) == "" {
		return model.CertRecord{}, errors.New("fake certs: empty owner")
	}

	f.serial++
	rec := model.CertRecord{
		SiteID:   owner,
		Serial:   fmt.Sprintf("%08x", f.serial),
		Domains:  append([]string(nil), domains...),
		NotAfter: time.Now().Add(24 * time.Hour).UTC(),
		KeyPath:  owner + ".key",
		CertPath: owner + ".crt",
	}
	if f.store != nil {
		if err := f.store.SaveCert(ctx, rec); err != nil {
			return model.CertRecord{}, err
		}
	}
	f.records[owner] = rec
	f.ensured[owner] = append([]string(nil), domains...)
	f.certsByID[owner] = fakeTLSCert(owner, domains)
	f.pems[owner] = fakePEM{
		cert: []byte("cert:" + owner),
		key:  []byte("key:" + owner),
	}
	return rec, nil
}

func (f *fakeCerts) Load(rec model.CertRecord) (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	cert, ok := f.certsByID[rec.SiteID]
	if !ok {
		return nil, fmt.Errorf("fake certs: no certificate for %q", rec.SiteID)
	}
	return cert, nil
}

func (f *fakeCerts) Delete(ctx context.Context, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if f.order != nil {
		*f.order = append(*f.order, "cert-delete:"+owner)
	}
	delete(f.records, owner)
	delete(f.certsByID, owner)
	delete(f.ensured, owner)
	delete(f.pems, owner)
	f.deleted[owner] = true
	if f.store != nil {
		if err := f.store.DeleteCertBySite(ctx, owner); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (f *fakeCerts) PEM(rec model.CertRecord) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pemErr != nil {
		return nil, nil, f.pemErr
	}
	value, ok := f.pems[rec.SiteID]
	if !ok {
		return nil, nil, fmt.Errorf("fake certs: no PEM for %q", rec.SiteID)
	}
	return append([]byte(nil), value.cert...), append([]byte(nil), value.key...), nil
}

func (f *fakeCerts) ensuredFor(owner string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ensured[owner]...)
}

func (f *fakeCerts) certFor(owner string) *tls.Certificate {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.certsByID[owner]
}

func (f *fakeCerts) wasDeleted(owner string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleted[owner]
}

func fakeTLSCert(owner string, domains []string) *tls.Certificate {
	return &tls.Certificate{
		Leaf: &x509.Certificate{
			Subject:  pkix.Name{CommonName: owner},
			DNSNames: append([]string(nil), domains...),
		},
	}
}

// fakeVhosts implements the helper-managed vhost boundary. Sync and mode calls
// are recorded even when they fail so tests can assert rollback requests.
type fakeVhosts struct {
	mu sync.Mutex

	status helperproto.VhostStatusResponse
	order  *[]string

	syncRequests []helperproto.VHostSyncRequest
	modeCalls    []string
	modeRequests []helperproto.VHostSyncRequest
	statusCalls  int
	removed      int

	err       error
	statusErr error
	modeErr   error
	removeErr error
}

var _ Vhosts = (*fakeVhosts)(nil)

func (f *fakeVhosts) VhostStatus(context.Context) (helperproto.VhostStatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	if f.statusErr != nil {
		return helperproto.VhostStatusResponse{}, f.statusErr
	}
	status := f.status
	if status.Mode == "" {
		status.Mode = "builtin"
	}
	return status, nil
}

func (f *fakeVhosts) SyncVhosts(_ context.Context, req helperproto.VHostSyncRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncRequests = append(f.syncRequests, req)
	if f.order != nil {
		*f.order = append(*f.order, "vhost-sync")
	}
	if f.err != nil {
		return f.err
	}
	f.status.Mode = req.Mode
	f.status.Server = req.Mode
	f.status.ConfigOK = true
	return nil
}

func (f *fakeVhosts) SetServingMode(_ context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modeCalls = append(f.modeCalls, mode)
	f.modeRequests = append(f.modeRequests, req)
	if f.modeErr != nil {
		return helperproto.VhostStatusResponse{}, f.modeErr
	}
	if f.err != nil && mode != "builtin" {
		return helperproto.VhostStatusResponse{}, f.err
	}
	f.status.Mode = mode
	f.status.Server = mode
	f.status.ConfigOK = true
	return f.status, nil
}

func (f *fakeVhosts) RemoveVhosts(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed++
	return f.removeErr
}

func (f *fakeVhosts) lastSync() (helperproto.VHostSyncRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.syncRequests) == 0 {
		return helperproto.VHostSyncRequest{}, false
	}
	return f.syncRequests[len(f.syncRequests)-1], true
}

func (f *fakeVhosts) syncCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.syncRequests)
}

func (f *fakeVhosts) syncAt(index int) (helperproto.VHostSyncRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.syncRequests) {
		return helperproto.VHostSyncRequest{}, false
	}
	return f.syncRequests[index], true
}

// fakeProxy implements ProxyControl.
type fakeProxy struct {
	mu       sync.Mutex
	started  bool
	starts   int
	stops    int
	startErr error
	stopErr  error
}

var _ ProxyControl = (*fakeProxy)(nil)

func (f *fakeProxy) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	return nil
}

func (f *fakeProxy) Stop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if f.stopErr != nil {
		return f.stopErr
	}
	f.started = false
	return nil
}

func (f *fakeProxy) isStarted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
