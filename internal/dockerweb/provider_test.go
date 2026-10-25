package dockerweb

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeServingState struct {
	mode, provider string
	saved          []string
	saveErr        error
}

func (f *fakeServingState) ServingMode() string     { return f.mode }
func (f *fakeServingState) ServingProvider() string { return f.provider }
func (f *fakeServingState) SaveServingState(_ context.Context, mode, provider string) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mode = mode
	f.provider = provider
	f.saved = append(f.saved, mode+"/"+provider)
	return nil
}

type fakeBuiltin struct {
	stopped  []string
	started  []string
	stopErr  error
	startErr error
}

func (f *fakeBuiltin) StopForDocker(context.Context) error {
	f.stopped = append(f.stopped, "stop")
	return f.stopErr
}

func (f *fakeBuiltin) StartAfterDocker(context.Context) error {
	f.started = append(f.started, "start")
	return f.startErr
}

type fakeSyncer struct {
	ensured   []Server
	synced    []Server
	ensureErr error
	syncErr   error
}

func (f *fakeSyncer) EnsureBase(server Server) error {
	f.ensured = append(f.ensured, server)
	return f.ensureErr
}

func (f *fakeSyncer) Sync(_ context.Context, server Server) error {
	f.synced = append(f.synced, server)
	return f.syncErr
}

type fakePortProbe struct {
	busy  map[int]bool
	err   error
	calls []int
}

func (f *fakePortProbe) InUse(_ context.Context, port int) (bool, error) {
	f.calls = append(f.calls, port)
	if f.err != nil {
		return false, f.err
	}
	return f.busy[port], nil
}

type fakeWebServices struct {
	activated     []string
	deactivated   []string
	validated     []string
	reloaded      []string
	activateErr   error
	deactivateErr error
	validateErr   error
	reloadErr     error
	onActivate    func(server string)
}

func (f *fakeWebServices) Activate(ctx context.Context, server string) error {
	f.activated = append(f.activated, server)
	if f.onActivate != nil {
		f.onActivate(server)
	}
	return f.activateErr
}

func (f *fakeWebServices) Deactivate(_ context.Context, server string) error {
	f.deactivated = append(f.deactivated, server)
	return f.deactivateErr
}

func (f *fakeWebServices) Validate(_ context.Context, server string) error {
	f.validated = append(f.validated, server)
	return f.validateErr
}

func (f *fakeWebServices) Reload(_ context.Context, server string) error {
	f.reloaded = append(f.reloaded, server)
	return f.reloadErr
}

func newProviderFixture(t *testing.T) (*Provider, *fakeServingState, *fakeBuiltin, *fakeSyncer, *fakeWebServices) {
	t.Helper()
	state := &fakeServingState{mode: "builtin", provider: "native"}
	builtin := &fakeBuiltin{}
	syncer := &fakeSyncer{}
	services := &fakeWebServices{}
	provider, err := NewProvider(ProviderConfig{
		State:    state,
		Builtin:  builtin,
		Syncer:   syncer,
		Services: services,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return provider, state, builtin, syncer, services
}

func TestProviderPrepareStopsBuiltinAndSyncs(t *testing.T) {
	provider, state, builtin, syncer, _ := newProviderFixture(t)
	if err := provider.Prepare(context.Background(), string(ServerNginx)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(builtin.stopped) != 1 {
		t.Errorf("builtin stops = %v", builtin.stopped)
	}
	if len(syncer.ensured) != 1 || syncer.ensured[0] != ServerNginx {
		t.Errorf("EnsureBase calls = %v", syncer.ensured)
	}
	if len(syncer.synced) != 1 || syncer.synced[0] != ServerNginx {
		t.Errorf("Sync calls = %v", syncer.synced)
	}
	if state.mode != "builtin" || state.provider != "native" {
		t.Errorf("Prepare must not persist state before commit: %s/%s", state.mode, state.provider)
	}
}

func TestProviderPrepareRefusesActiveNativeExternalServer(t *testing.T) {
	provider, state, builtin, _, _ := newProviderFixture(t)
	state.mode = "nginx"
	state.provider = "native"
	err := provider.Prepare(context.Background(), string(ServerApache))
	if err == nil {
		t.Fatal("expected a native server conflict")
	}
	if !strings.Contains(err.Error(), "native") {
		t.Fatalf("error = %v, want a native provider explanation", err)
	}
	if len(builtin.stopped) != 0 {
		t.Error("builtin must not be stopped when a native server is active")
	}
}

func TestProviderPrepareSyncFailureRestoresBuiltin(t *testing.T) {
	provider, _, builtin, syncer, _ := newProviderFixture(t)
	syncer.syncErr = errors.New("render failed")
	err := provider.Prepare(context.Background(), string(ServerNginx))
	if err == nil {
		t.Fatal("expected sync failure")
	}
	if len(builtin.stopped) != 1 || len(builtin.started) != 1 {
		t.Fatalf("builtin calls stop=%v start=%v, want one each", builtin.stopped, builtin.started)
	}
	if err := provider.Commit(context.Background(), string(ServerNginx)); err == nil {
		t.Fatal("Commit after failed Prepare must fail")
	}
}

func TestProviderCommitValidatesSavesAndReloads(t *testing.T) {
	provider, state, _, _, services := newProviderFixture(t)
	if err := provider.Prepare(context.Background(), string(ServerApache)); err != nil {
		t.Fatal(err)
	}
	if err := provider.Commit(context.Background(), string(ServerApache)); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(services.validated) != 1 || services.validated[0] != "apache" {
		t.Errorf("validated = %v", services.validated)
	}
	if len(services.reloaded) != 1 || services.reloaded[0] != "apache" {
		t.Errorf("reloaded = %v", services.reloaded)
	}
	if state.mode != "apache" || state.provider != "docker" {
		t.Errorf("persisted state = %s/%s, want apache/docker", state.mode, state.provider)
	}
}

func TestProviderCommitValidationFailureRollsBack(t *testing.T) {
	provider, state, builtin, _, services := newProviderFixture(t)
	services.validateErr = errors.New("apache config broken")
	if err := provider.Prepare(context.Background(), string(ServerApache)); err != nil {
		t.Fatal(err)
	}
	err := provider.Commit(context.Background(), string(ServerApache))
	if err == nil {
		t.Fatal("expected validation failure")
	}
	if len(services.deactivated) != 1 || services.deactivated[0] != "apache" {
		t.Errorf("deactivated = %v, want the failed container stopped", services.deactivated)
	}
	if len(builtin.started) != 1 {
		t.Errorf("builtin starts = %v, want restored builtin", builtin.started)
	}
	if state.mode != "builtin" || state.provider != "native" {
		t.Errorf("state = %s/%s, want restored builtin/native", state.mode, state.provider)
	}
}

func TestProviderCommitStateSaveFailureRollsBack(t *testing.T) {
	provider, state, _, _, services := newProviderFixture(t)
	state.saveErr = errors.New("disk full")
	if err := provider.Prepare(context.Background(), string(ServerNginx)); err != nil {
		t.Fatal(err)
	}
	if err := provider.Commit(context.Background(), string(ServerNginx)); err == nil {
		t.Fatal("expected state save failure")
	}
	if len(services.deactivated) != 1 {
		t.Errorf("deactivated = %v", services.deactivated)
	}
}

func TestProviderPrepareStopFailureRestoresBuiltin(t *testing.T) {
	provider, _, builtin, _, _ := newProviderFixture(t)
	builtin.stopErr = errors.New("proxy still running")
	if err := provider.Prepare(context.Background(), string(ServerNginx)); err == nil {
		t.Fatal("expected stop failure")
	}
	if len(builtin.started) != 1 {
		t.Fatalf("builtin starts = %v, want restore after stop failure", builtin.started)
	}
}

func TestProviderCommitReloadFailureRollsBack(t *testing.T) {
	provider, state, builtin, _, services := newProviderFixture(t)
	services.reloadErr = errors.New("reload failed")
	if err := provider.Prepare(context.Background(), string(ServerNginx)); err != nil {
		t.Fatal(err)
	}
	if err := provider.Commit(context.Background(), string(ServerNginx)); err == nil {
		t.Fatal("expected reload failure")
	}
	if len(services.deactivated) != 1 || services.deactivated[0] != "nginx" {
		t.Errorf("deactivated = %v", services.deactivated)
	}
	if len(builtin.started) != 1 {
		t.Errorf("builtin starts = %v", builtin.started)
	}
	if state.mode != "builtin" || state.provider != "native" {
		t.Errorf("state = %s/%s, want builtin/native", state.mode, state.provider)
	}
}

func TestProviderRollbackRestoresPreviousDockerServer(t *testing.T) {
	provider, state, builtin, syncer, services := newProviderFixture(t)
	state.mode = "nginx"
	state.provider = "docker"
	if err := provider.Prepare(context.Background(), string(ServerApache)); err != nil {
		t.Fatal(err)
	}
	services.activated = nil
	if err := provider.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if len(services.deactivated) != 1 || services.deactivated[0] != "apache" {
		t.Errorf("deactivated = %v, want apache", services.deactivated)
	}
	if len(services.activated) != 1 || services.activated[0] != "nginx" {
		t.Errorf("activated = %v, want previous nginx", services.activated)
	}
	if len(builtin.started) != 0 {
		t.Errorf("builtin must not start when the previous provider was docker: %v", builtin.started)
	}
	if state.mode != "nginx" || state.provider != "docker" {
		t.Errorf("state = %s/%s", state.mode, state.provider)
	}
	_ = syncer
}

func TestProviderRollbackRestoresBuiltin(t *testing.T) {
	provider, state, builtin, _, services := newProviderFixture(t)
	if err := provider.Prepare(context.Background(), string(ServerNginx)); err != nil {
		t.Fatal(err)
	}
	if err := provider.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if len(services.deactivated) != 1 || services.deactivated[0] != "nginx" {
		t.Errorf("deactivated = %v", services.deactivated)
	}
	if len(builtin.started) != 1 {
		t.Errorf("builtin starts = %v", builtin.started)
	}
	if state.mode != "builtin" || state.provider != "native" {
		t.Errorf("state = %s/%s", state.mode, state.provider)
	}
}

func TestProviderPrepareSkipsPortProbeForDockerProvider(t *testing.T) {
	state := &fakeServingState{mode: "nginx", provider: "docker"}
	builtin := &fakeBuiltin{}
	syncer := &fakeSyncer{}
	services := &fakeWebServices{}
	probe := &fakePortProbe{busy: map[int]bool{80: true, 443: true}}
	provider, err := NewProvider(ProviderConfig{
		State:    state,
		Builtin:  builtin,
		Syncer:   syncer,
		Services: services,
		Probe:    probe,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	ctx := context.Background()
	if err := provider.Prepare(ctx, "apache"); err != nil {
		t.Fatalf("Prepare(nginx->apache) must not probe 80/443: %v", err)
	}
	if err := provider.Commit(ctx, "apache"); err != nil {
		t.Fatalf("Commit(apache): %v", err)
	}
	if err := provider.Prepare(ctx, "nginx"); err != nil {
		t.Fatalf("re-Prepare(nginx) must not probe 80/443: %v", err)
	}
	if len(probe.calls) != 0 {
		t.Fatalf("port probe calls = %v, want none for provider=docker", probe.calls)
	}
	if len(syncer.synced) != 2 || syncer.synced[0] != "apache" || syncer.synced[1] != "nginx" {
		t.Fatalf("sync calls = %v, want apache then nginx", syncer.synced)
	}
}

func TestProviderPrepareRejectsOccupiedPortBeforeSync(t *testing.T) {
	state := &fakeServingState{mode: "builtin", provider: "native"}
	builtin := &fakeBuiltin{}
	syncer := &fakeSyncer{}
	services := &fakeWebServices{}
	probe := &fakePortProbe{busy: map[int]bool{443: true}}
	provider, err := NewProvider(ProviderConfig{
		State:    state,
		Builtin:  builtin,
		Syncer:   syncer,
		Services: services,
		Probe:    probe,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	err = provider.Prepare(context.Background(), string(ServerNginx))
	if err == nil {
		t.Fatal("expected a native server conflict when 443 is occupied")
	}
	if !strings.Contains(err.Error(), "443") || !strings.Contains(strings.ToLower(err.Error()), "native") {
		t.Fatalf("error = %v, want a clear native 443 conflict", err)
	}
	if len(builtin.started) != 1 {
		t.Fatalf("builtin starts = %v, want restore after conflict", builtin.started)
	}
	if len(syncer.ensured) != 0 || len(syncer.synced) != 0 {
		t.Fatal("config must not be rendered while 80/443 is owned by a native server")
	}
	if probe.calls[0] != 80 || probe.calls[1] != 443 {
		t.Fatalf("probe calls = %v, want 80 then 443", probe.calls)
	}
}

func TestProviderPrepareRejectsUnknownServer(t *testing.T) {
	provider, _, _, _, _ := newProviderFixture(t)
	if err := provider.Prepare(context.Background(), "caddy"); err == nil {
		t.Fatal("expected unsupported server error")
	}
}

func TestProviderCommitRequiresPrepare(t *testing.T) {
	provider, _, _, _, _ := newProviderFixture(t)
	if err := provider.Commit(context.Background(), string(ServerNginx)); err == nil {
		t.Fatal("expected commit-without-prepare error")
	}
}
