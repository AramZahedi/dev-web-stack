package servicecontrol

import (
	"context"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/dockerweb"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// Compile-time proof that the real dockerweb provider can be passed directly
// to NewWebPreparer/NewWebPreparers (string server names).
var _ WebLifecycle = (*dockerweb.Provider)(nil)

type lifecycleState struct {
	mode, provider string
	saved          []string
}

func (s *lifecycleState) ServingMode() string     { return s.mode }
func (s *lifecycleState) ServingProvider() string { return s.provider }
func (s *lifecycleState) SaveServingState(_ context.Context, mode, provider string) error {
	s.mode = mode
	s.provider = provider
	s.saved = append(s.saved, mode+"/"+provider)
	return nil
}

type lifecycleBuiltin struct {
	stopped, started int
}

func (b *lifecycleBuiltin) StopForDocker(context.Context) error {
	b.stopped++
	return nil
}

func (b *lifecycleBuiltin) StartAfterDocker(context.Context) error {
	b.started++
	return nil
}

type lifecycleSyncer struct {
	ensured []string
	synced  []string
}

func (s *lifecycleSyncer) EnsureBase(server dockerweb.Server) error {
	s.ensured = append(s.ensured, string(server))
	return nil
}

func (s *lifecycleSyncer) Sync(_ context.Context, server dockerweb.Server) error {
	s.synced = append(s.synced, string(server))
	return nil
}

type lifecycleManager struct {
	validated []services.ID
	reloaded  []services.ID
}

func (m *lifecycleManager) Activate(context.Context, services.ID) error   { return nil }
func (m *lifecycleManager) Deactivate(context.Context, services.ID) error { return nil }
func (m *lifecycleManager) Validate(_ context.Context, id services.ID) error {
	m.validated = append(m.validated, id)
	return nil
}
func (m *lifecycleManager) Reload(_ context.Context, id services.ID) error {
	m.reloaded = append(m.reloaded, id)
	return nil
}

// TestRealProviderWiresThroughWebPreparerAndServiceControl exercises the exact
// integration sequence the daemon is documented to use: real provider -> real
// WebPreparer -> real WebServiceControl -> fake P3 manager.
func TestRealProviderWiresThroughWebPreparerAndServiceControl(t *testing.T) {
	state := &lifecycleState{mode: "builtin", provider: "native"}
	builtin := &lifecycleBuiltin{}
	syncer := &lifecycleSyncer{}
	manager := &lifecycleManager{}

	provider, err := dockerweb.NewProvider(dockerweb.ProviderConfig{
		State:    state,
		Builtin:  builtin,
		Syncer:   syncer,
		Services: NewWebServiceControl(manager),
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	preparer := NewWebPreparer(provider)
	svc, ok := services.DefaultCatalog().Get(services.IDNginx)
	if !ok {
		t.Fatal("default catalog is missing nginx")
	}
	ctx := context.Background()

	if err := preparer.PrepareInstall(ctx, svc); err != nil {
		t.Fatalf("PrepareInstall: %v", err)
	}
	if len(syncer.ensured) != 1 || syncer.ensured[0] != "nginx" {
		t.Fatalf("EnsureBase calls = %v", syncer.ensured)
	}
	if err := preparer.PrepareActivate(ctx, svc); err != nil {
		t.Fatalf("PrepareActivate: %v", err)
	}
	if len(syncer.synced) != 1 || syncer.synced[0] != "nginx" {
		t.Fatalf("Sync calls = %v", syncer.synced)
	}
	if err := preparer.CommitActivate(ctx, svc); err != nil {
		t.Fatalf("CommitActivate: %v", err)
	}
	if len(manager.validated) != 1 || manager.validated[0] != services.IDNginx {
		t.Fatalf("validated = %v", manager.validated)
	}
	if len(manager.reloaded) != 1 || manager.reloaded[0] != services.IDNginx {
		t.Fatalf("reloaded = %v", manager.reloaded)
	}
	if state.mode != "nginx" || state.provider != "docker" {
		t.Fatalf("state = %s/%s, want nginx/docker", state.mode, state.provider)
	}
}
