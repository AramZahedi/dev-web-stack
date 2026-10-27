package dockerweb_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/dockerweb"
)

type windowsState struct {
	mode, provider string
	saved          []string
}

func (s *windowsState) ServingMode() string     { return s.mode }
func (s *windowsState) ServingProvider() string { return s.provider }
func (s *windowsState) SaveServingState(_ context.Context, mode, provider string) error {
	s.mode, s.provider = mode, provider
	s.saved = append(s.saved, mode+"/"+provider)
	return nil
}

type privateSyncer struct {
	ensured []dockerweb.Server
	synced  []dockerweb.Server
	err     error
}

func (s *privateSyncer) EnsurePrivateBase(server dockerweb.Server) error {
	s.ensured = append(s.ensured, server)
	return s.err
}
func (s *privateSyncer) SyncPrivate(_ context.Context, server dockerweb.Server) error {
	s.synced = append(s.synced, server)
	return s.err
}

type windowsServices struct {
	validated []string
	reloaded  []string
	stopped   []string
	err       error
}

type fakeWebAgent struct {
	requests []agentproto.WebTargetsRequest
}

func (a *fakeWebAgent) SyncWeb(_ context.Context, request agentproto.WebTargetsRequest) (agentproto.WebSyncResponse, error) {
	a.requests = append(a.requests, request)
	return agentproto.WebSyncResponse{Changed: len(request.Targets)}, nil
}

type fakeTargetSource struct{ targets []agentproto.WebTarget }

func (s fakeTargetSource) PrivateWebTargets(context.Context, string) ([]agentproto.WebTarget, error) {
	return append([]agentproto.WebTarget(nil), s.targets...), nil
}

func (s *windowsServices) Activate(context.Context, string) error { return nil }
func (s *windowsServices) Deactivate(_ context.Context, id string) error {
	s.stopped = append(s.stopped, id)
	return nil
}
func (s *windowsServices) Validate(_ context.Context, id string) error {
	s.validated = append(s.validated, id)
	return s.err
}
func (s *windowsServices) Reload(_ context.Context, id string) error {
	s.reloaded = append(s.reloaded, id)
	return s.err
}

func TestWindowsProviderUsesPrivateHTTPWithoutTakingPublicPorts(t *testing.T) {
	state := &windowsState{mode: "builtin", provider: "native"}
	syncer := &privateSyncer{}
	services := &windowsServices{}
	provider, err := dockerweb.NewWindowsProvider(dockerweb.WindowsProviderConfig{State: state, Syncer: syncer, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Prepare(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if len(syncer.ensured) != 1 || len(syncer.synced) != 1 {
		t.Fatalf("private sync calls = ensured %v synced %v", syncer.ensured, syncer.synced)
	}
	if err := provider.Commit(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if state.mode != "nginx" || state.provider != "docker" {
		t.Fatalf("serving state = %s/%s, want nginx/docker", state.mode, state.provider)
	}
	if len(services.validated) != 1 || len(services.reloaded) != 1 {
		t.Fatalf("validation/reload = %v/%v", services.validated, services.reloaded)
	}
	host, port, tls := provider.PrivateBackend()
	if host != "127.0.0.1" || port != 18080 || tls {
		t.Fatalf("PrivateBackend() = %s:%d tls=%t, want loopback:18080 HTTP", host, port, tls)
	}
	if len(services.stopped) != 0 {
		t.Fatalf("Windows provider unexpectedly stopped the builtin daemon: %v", services.stopped)
	}
}

func TestWindowsProviderRollsBackAndRejectsUnsupportedServer(t *testing.T) {
	state := &windowsState{mode: "builtin", provider: "native"}
	services := &windowsServices{err: errors.New("validation failed")}
	provider, err := dockerweb.NewWindowsProvider(dockerweb.WindowsProviderConfig{State: state, Syncer: &privateSyncer{}, Services: services})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Prepare(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Commit(context.Background(), "nginx"); err == nil {
		t.Fatal("Commit succeeded despite validation failure")
	}
	if state.mode != "builtin" || state.provider != "native" {
		t.Fatalf("rollback left state %s/%s", state.mode, state.provider)
	}
	if err := provider.Prepare(context.Background(), "arbitrary"); err == nil {
		t.Fatal("unsupported web server accepted")
	}
}

func TestAgentWindowsPrivateSyncerUsesTypedAgentTargets(t *testing.T) {
	agent := &fakeWebAgent{}
	target := agentproto.WebTarget{SiteID: "shop", Domain: "shop.test", Kind: "node", Address: "127.0.0.1", Port: 41000}
	syncer, err := dockerweb.NewAgentWindowsPrivateSyncer(agent, fakeTargetSource{targets: []agentproto.WebTarget{target}})
	if err != nil {
		t.Fatal(err)
	}
	if err := syncer.EnsurePrivateBase(dockerweb.ServerNginx); err != nil {
		t.Fatal(err)
	}
	if err := syncer.SyncPrivate(context.Background(), dockerweb.ServerNginx); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 1 || len(agent.requests[0].Targets) != 1 || agent.requests[0].Targets[0] != target {
		t.Fatalf("agent target sync = %+v, want one typed private target", agent.requests)
	}
	if err := syncer.SyncPrivate(context.Background(), dockerweb.Server("arbitrary")); err == nil {
		t.Fatal("unsupported server was accepted by the Windows agent adapter")
	}
}

type fakeRouterUpdater struct {
	calls []string
	err   error
}

func (r *fakeRouterUpdater) UpdateDockerWebRoutes(_ context.Context, active bool, host string, port int) error {
	r.calls = append(r.calls, fmt.Sprintf("%t:%s:%d", active, host, port))
	return r.err
}

func TestWindowsProviderUpdatesProxyRouterTargetsOnCommitAndStop(t *testing.T) {
	state := &windowsState{mode: "builtin", provider: "native"}
	syncer := &privateSyncer{}
	services := &windowsServices{}
	router := &fakeRouterUpdater{}
	provider, err := dockerweb.NewWindowsProvider(dockerweb.WindowsProviderConfig{
		State:    state,
		Syncer:   syncer,
		Services: services,
		Router:   router,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Prepare(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Commit(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if len(router.calls) != 1 || router.calls[0] != "true:127.0.0.1:18080" {
		t.Fatalf("router calls after commit = %v, want [true:127.0.0.1:18080]", router.calls)
	}

	if err := provider.StopActiveServer(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if len(router.calls) != 2 || router.calls[1] != "false::0" {
		t.Fatalf("router calls after stop = %v, want second call false::0", router.calls)
	}
}
