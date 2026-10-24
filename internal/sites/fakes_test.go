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
	php     *fakePHPCommands
	hosts   *fakeHosts
	certs   *fakeCerts
	vhosts  *fakeVhosts
	proxy   *fakeProxy
	router  *proxy.Router
	ports   *domain.PortAllocator
	config  config.Config
	root    string
	dbPath  string
	order   *[]string
}

// newTestService builds a Service against a temporary SQLite database, a real
// router and port allocator, and deterministic ids ("site", "site-2", ...).
func newTestService(t *testing.T) (*Service, *testFakes) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	st, err := store.Open(dbPath)
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
		started:        make(map[string]bool),
		stopped:        make(map[string]bool),
		status:         make(map[string]model.SiteStatus),
		startedGens:    make(map[string]bool),
		phpSpecs:       make(map[string]backend.SiteSpec),
		phpSequences:   make(map[string]int),
		phpActive:      make(map[string]string),
		phpPrepareErrs: make(map[string]error),
		phpStartErrs:   make(map[string]error),
		phpStopErrs:    make(map[string]error),
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
	fphp := &fakePHPCommands{}
	order := []string{}
	fc.order = &order
	fv.order = &order
	fb.order = &order

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
		PHP:     fphp,
		Now: func() time.Time {
			return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
		},
		NewID: newID,
	})

	return svc, &testFakes{
		store:   st,
		backend: fb,
		php:     fphp,
		hosts:   fh,
		certs:   fc,
		vhosts:  fv,
		proxy:   fp,
		router:  router,
		ports:   ports,
		config:  cfg,
		root:    root,
		dbPath:  dbPath,
		order:   &order,
	}
}

// fakeBackend is an in-memory backend.Backend and backend.PHPStager.
type fakeBackend struct {
	mu sync.Mutex

	prepared       []backend.SiteSpec
	started        map[string]bool
	stopped        map[string]bool
	status         map[string]model.SiteStatus
	runtimeCalls   []string
	buildCalls     []string
	execCalls      []fakeExecCall
	startedGens    map[string]bool
	phpSpecs       map[string]backend.SiteSpec
	phpSequences   map[string]int
	phpActive      map[string]string
	phpEvents      []fakePHPOperation
	phpPrepareErrs map[string]error
	phpStartErrs   map[string]error
	phpStopErrs    map[string]error
	phpStartSeq    int
	phpStartHook   func()
	order          *[]string
	startCalls     int
	stopCalls      int
	prepareErr     error
	startErr       error
	startPHPErr    error
	preparePHPErr  error
	stopPHPErr     error
	stopErr        error
	statusErr      error
	ensureRTError  error
	ensureBuildErr error
	execResult     backend.Result
	execErr        error
}

// fakePHPOperation records one PHP staging transition. Sequence is the
// start order assigned to a successful start and is zero for other operations.
type fakePHPOperation struct {
	Op         string
	SiteID     string
	Generation string
	Sequence   int
	Port       int
}

// fakeExecCall records one Backend.Exec invocation.
type fakeExecCall struct {
	ID   string
	Argv []string
	Env  map[string]string
}

var (
	_ backend.Backend   = (*fakeBackend)(nil)
	_ backend.PHPStager = (*fakeBackend)(nil)
)

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
	f.startCalls++
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
	// Local.Stop terminates every staged PHP generation, including generations
	// left behind by an earlier apply.
	prefix := id + "#"
	for key, started := range f.startedGens {
		if started && strings.HasPrefix(key, prefix) {
			f.startedGens[key] = false
		}
	}
	f.phpActive[id] = ""
	return nil
}

func (f *fakeBackend) Status(_ context.Context, id string) (backend.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return backend.Status{}, f.statusErr
	}
	if generation := f.phpActive[id]; generation != "" && f.startedGens[id+"#"+generation] {
		return backend.Status{State: model.StatusRunning}, nil
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

func (f *fakeBackend) Exec(_ context.Context, id string, argv []string, env map[string]string) (backend.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCalls = append(f.execCalls, fakeExecCall{ID: id, Argv: append([]string(nil), argv...), Env: cloneEnv(env)})
	if f.execErr != nil {
		return backend.Result{}, f.execErr
	}
	return f.execResult, nil
}

// PreparePHP records a staged PHP generation. It shares the recorder with
// Prepare so tests can inspect the last prepared spec regardless of path.
func (f *fakeBackend) PreparePHP(_ context.Context, siteID, generation string, spec backend.SiteSpec) (backend.PreparedSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensurePHPMapsLocked()
	key := phpFakeKey(siteID, generation)
	spec.ID = siteID
	f.prepared = append(f.prepared, spec)
	f.phpEvents = append(f.phpEvents, fakePHPOperation{
		Op:         "prepare",
		SiteID:     siteID,
		Generation: generation,
		Port:       spec.FastCGIPort,
	})
	f.recordOrderLocked("php-prepare:" + key)
	if err := f.phpFailureLocked(f.phpPrepareErrs[key], f.preparePHPErr, f.prepareErr); err != nil {
		return backend.PreparedSite{}, err
	}
	f.phpSpecs[key] = spec
	return backend.PreparedSite{
		BackendHost: "127.0.0.1",
		FastCGIPort: spec.FastCGIPort,
	}, nil
}

// StartPHP starts a prepared PHP generation and makes it active.
func (f *fakeBackend) StartPHP(_ context.Context, siteID, generation string) (backend.ProcessInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensurePHPMapsLocked()
	key := phpFakeKey(siteID, generation)
	f.phpEvents = append(f.phpEvents, fakePHPOperation{
		Op:         "start",
		SiteID:     siteID,
		Generation: generation,
		Port:       f.phpSpecs[key].FastCGIPort,
	})
	f.recordOrderLocked("php-start:" + key)
	if err := f.phpFailureLocked(f.phpStartErrs[key], f.startPHPErr, f.startErr); err != nil {
		return backend.ProcessInfo{}, err
	}
	if _, ok := f.phpSpecs[key]; !ok {
		return backend.ProcessInfo{}, fmt.Errorf("fake backend: generation %q is not prepared for site %q", generation, siteID)
	}
	if f.startedGens[key] {
		return backend.ProcessInfo{}, fmt.Errorf("fake backend: generation %q is already running for site %q", generation, siteID)
	}
	f.phpStartSeq++
	f.phpSequences[key] = f.phpStartSeq
	f.startCalls++
	f.started[siteID] = true
	f.startedGens[key] = true
	f.phpActive[siteID] = generation
	f.status[siteID] = model.StatusRunning
	if f.phpStartHook != nil {
		f.phpStartHook()
	}
	return backend.ProcessInfo{PID: 4242, StartedAt: time.Now()}, nil
}

// StopPHP stops one PHP generation. Stopping the active generation promotes
// the most recently started remaining generation, mirroring the local backend.
func (f *fakeBackend) StopPHP(ctx context.Context, siteID, generation string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Local.StopPHP refuses to stop once the caller's context is done. Mirror
	// that so tests can prove cleanup detaches from request cancellation.
	if err := ctx.Err(); err != nil {
		return err
	}
	f.ensurePHPMapsLocked()
	key := phpFakeKey(siteID, generation)
	f.stopCalls++
	f.phpEvents = append(f.phpEvents, fakePHPOperation{
		Op:         "stop",
		SiteID:     siteID,
		Generation: generation,
		Port:       f.phpSpecs[key].FastCGIPort,
	})
	f.recordOrderLocked("php-stop:" + key)
	if err := f.phpFailureLocked(f.phpStopErrs[key], f.stopPHPErr, f.stopErr); err != nil {
		return err
	}
	if _, ok := f.phpSpecs[key]; !ok {
		return nil
	}
	if f.startedGens[key] {
		f.startedGens[key] = false
	}
	if f.phpActive[siteID] == generation {
		f.phpActive[siteID] = f.mostRecentGenerationLocked(siteID)
	}
	f.stopped[siteID] = true
	if f.phpActive[siteID] == "" {
		f.status[siteID] = model.StatusStopped
	} else {
		f.status[siteID] = model.StatusRunning
	}
	return nil
}

// ensurePHPMapsLocked initialises the PHP staging maps for fakes built by hand.
func (f *fakeBackend) ensurePHPMapsLocked() {
	if f.phpSpecs == nil {
		f.phpSpecs = make(map[string]backend.SiteSpec)
	}
	if f.phpSequences == nil {
		f.phpSequences = make(map[string]int)
	}
	if f.phpActive == nil {
		f.phpActive = make(map[string]string)
	}
	if f.phpPrepareErrs == nil {
		f.phpPrepareErrs = make(map[string]error)
	}
	if f.phpStartErrs == nil {
		f.phpStartErrs = make(map[string]error)
	}
	if f.phpStopErrs == nil {
		f.phpStopErrs = make(map[string]error)
	}
}

func (f *fakeBackend) phpFailureLocked(overrides ...error) error {
	for _, err := range overrides {
		if err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeBackend) mostRecentGenerationLocked(siteID string) string {
	active := ""
	sequence := 0
	for key, started := range f.startedGens {
		if !started || !strings.HasPrefix(key, siteID+"#") {
			continue
		}
		if f.phpSequences[key] >= sequence {
			active = strings.TrimPrefix(key, siteID+"#")
			sequence = f.phpSequences[key]
		}
	}
	return active
}

func (f *fakeBackend) recordOrderLocked(event string) {
	if f.order != nil {
		*f.order = append(*f.order, event)
	}
}

func phpFakeKey(siteID, generation string) string {
	return siteID + "#" + generation
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

// installPHPBuild creates a minimal installed PHP build tree and upserts its
// runtime row, so build selection and config composition can run against real
// paths.
func (f *testFakes) installPHPBuild(id, version string) model.RuntimeBuild {
	buildRoot := filepath.Join(f.config.Home, "runtimes", id)
	for _, dir := range []string{
		filepath.Join(buildRoot, "bin"),
		filepath.Join(buildRoot, "sbin"),
		filepath.Join(buildRoot, "etc"),
		filepath.Join(buildRoot, "lib", "php", "extensions", "no-debug-non-zts-20240924"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(err)
		}
	}
	for _, file := range []struct {
		path string
		mode os.FileMode
		body string
	}{
		{filepath.Join(buildRoot, "bin", "php"), 0o755, "#!/bin/sh\nexit 0\n"},
		{filepath.Join(buildRoot, "sbin", "php-fpm"), 0o755, "#!/bin/sh\nexit 0\n"},
		{filepath.Join(buildRoot, "etc", "php.ini"), 0o644, "; base build configuration\nmemory_limit = 128M\n"},
		{filepath.Join(buildRoot, "lib", "php", "extensions", "no-debug-non-zts-20240924", "opcache.so"), 0o644, "so"},
	} {
		if err := os.WriteFile(file.path, []byte(file.body), file.mode); err != nil {
			panic(err)
		}
	}
	build := model.RuntimeBuild{
		ID:          id,
		Kind:        "php",
		Version:     version,
		Backend:     model.BackendLocal,
		Path:        buildRoot,
		InstalledAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}
	if err := f.store.UpsertRuntime(context.Background(), build); err != nil {
		panic(err)
	}
	return build
}

// fakePHPCommands records validator calls and returns injected failures.
type fakePHPCommands struct {
	mu sync.Mutex

	lintCalls int
	fpmCalls  int
	lintErr   error
	fpmErr    error
	lintFunc  func(ctx context.Context, phpBin, iniPath string) error
	fpmFunc   func(ctx context.Context, fpmBin, configPath, iniPath string) error

	lintPHPBin string
	lintIni    string
	fpmBin     string
	fpmConfig  string
	fpmIni     string
}

var _ PHPCommands = (*fakePHPCommands)(nil)

func (f *fakePHPCommands) Lint(ctx context.Context, phpBin, iniPath string) error {
	f.mu.Lock()
	f.lintCalls++
	f.lintPHPBin = phpBin
	f.lintIni = iniPath
	hook := f.lintFunc
	err := f.lintErr
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx, phpBin, iniPath)
	}
	return err
}

func (f *fakePHPCommands) ValidateFPM(ctx context.Context, fpmBin, configPath, iniPath string) error {
	f.mu.Lock()
	f.fpmCalls++
	f.fpmBin = fpmBin
	f.fpmConfig = configPath
	f.fpmIni = iniPath
	hook := f.fpmFunc
	err := f.fpmErr
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx, fpmBin, configPath, iniPath)
	}
	return err
}

func (f *fakePHPCommands) counts() (lint, fpm int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lintCalls, f.fpmCalls
}

func (f *fakePHPCommands) lastLint() (phpBin, iniPath string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lintPHPBin, f.lintIni
}

func (f *fakePHPCommands) lastFPM() (fpmBin, configPath, iniPath string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fpmBin, f.fpmConfig, f.fpmIni
}

func (f *fakeBackend) execList() []fakeExecCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeExecCall(nil), f.execCalls...)
}

func (f *fakeBackend) lastExec() (fakeExecCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.execCalls) == 0 {
		return fakeExecCall{}, false
	}
	return f.execCalls[len(f.execCalls)-1], true
}

func (f *fakeBackend) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startCalls
}

// phpSpec returns the spec staged for one generation.
func (f *fakeBackend) phpSpec(siteID, generation string) (backend.SiteSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	spec, ok := f.phpSpecs[phpFakeKey(siteID, generation)]
	return spec, ok
}

// phpActiveGeneration returns the generation Status reports as active.
func (f *fakeBackend) phpActiveGeneration(siteID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.phpActive[siteID]
}

// phpStarted reports whether one generation is running.
func (f *fakeBackend) phpStarted(siteID, generation string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.startedGens[phpFakeKey(siteID, generation)]
}

// phpEventList returns a copy of every staging transition in order.
func (f *fakeBackend) phpEventList() []fakePHPOperation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakePHPOperation(nil), f.phpEvents...)
}
