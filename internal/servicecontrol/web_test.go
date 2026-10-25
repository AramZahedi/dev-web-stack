package servicecontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeWebLifecycle struct {
	ensured    []string
	prepared   []string
	committed  []string
	rolledBack int
	err        error
}

func (f *fakeWebLifecycle) EnsureBase(server string) error {
	f.ensured = append(f.ensured, server)
	return f.err
}

func (f *fakeWebLifecycle) Prepare(_ context.Context, server string) error {
	f.prepared = append(f.prepared, server)
	return f.err
}

func (f *fakeWebLifecycle) Commit(_ context.Context, server string) error {
	f.committed = append(f.committed, server)
	return f.err
}

func (f *fakeWebLifecycle) Rollback(context.Context) error {
	f.rolledBack++
	return f.err
}

func TestWebPreparersRegisterNginxAndApache(t *testing.T) {
	lifecycle := &fakeWebLifecycle{}
	preparers := NewWebPreparers(lifecycle)
	if _, ok := preparers[services.IDNginx]; !ok {
		t.Fatal("missing nginx preparer")
	}
	if _, ok := preparers[services.IDApache]; !ok {
		t.Fatal("missing apache preparer")
	}
	if len(preparers) != 2 {
		t.Fatalf("preparers = %d, want 2", len(preparers))
	}
}

func TestWebPreparerLifecycle(t *testing.T) {
	lifecycle := &fakeWebLifecycle{}
	preparer := NewWebPreparer(lifecycle)
	nginx, _ := services.DefaultCatalog().Get(services.IDNginx)
	if nginx.ID == "" {
		nginx = services.Service{ID: services.IDNginx}
	}
	ctx := context.Background()

	if err := preparer.PrepareInstall(ctx, nginx); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.ensured) != 1 || lifecycle.ensured[0] != "nginx" {
		t.Errorf("EnsureBase calls = %v", lifecycle.ensured)
	}
	if err := preparer.PrepareActivate(ctx, nginx); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.prepared) != 1 || lifecycle.prepared[0] != "nginx" {
		t.Errorf("Prepare calls = %v", lifecycle.prepared)
	}
	if err := preparer.CommitActivate(ctx, nginx); err != nil {
		t.Fatal(err)
	}
	if len(lifecycle.committed) != 1 || lifecycle.committed[0] != "nginx" {
		t.Errorf("Commit calls = %v", lifecycle.committed)
	}
	if err := preparer.CleanupActivate(ctx, nginx, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	if lifecycle.rolledBack != 1 {
		t.Errorf("rollbacks = %d, want 1", lifecycle.rolledBack)
	}
}

func TestWebPreparerRejectsNonWebService(t *testing.T) {
	preparer := NewWebPreparer(&fakeWebLifecycle{})
	_, err := preparer.Credentials(context.Background(), services.IDMySQL)
	if !errors.Is(err, ErrCredentialsUnsupported) {
		t.Fatalf("Credentials error = %v, want ErrCredentialsUnsupported", err)
	}
}

func TestWebGuardRefusesActiveDockerWebServer(t *testing.T) {
	guard := WebGuard{State: fakeServingState{mode: "nginx", provider: "docker"}}
	if err := guard.CanDeactivate(context.Background(), services.IDNginx); err == nil {
		t.Fatal("expected deactivation refusal for the active docker nginx")
	} else if !errors.Is(err, ErrWebServerActive) {
		t.Fatalf("error = %v, want ErrWebServerActive", err)
	}
	if err := guard.CanUninstall(context.Background(), services.IDNginx); !errors.Is(err, ErrWebServerActive) {
		t.Fatalf("uninstall error = %v", err)
	}
	if err := guard.CanDeactivate(context.Background(), services.IDApache); err != nil {
		t.Errorf("inactive apache should be deactivatable: %v", err)
	}
}

func TestWebGuardAllowsWhenProviderNativeOrModeDiffers(t *testing.T) {
	native := WebGuard{State: fakeServingState{mode: "nginx", provider: "native"}}
	if err := native.CanDeactivate(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("native provider should not be guarded: %v", err)
	}
	dockerApache := WebGuard{State: fakeServingState{mode: "apache", provider: "docker"}}
	if err := dockerApache.CanDeactivate(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("inactive docker nginx should be deactivatable: %v", err)
	}
	if err := dockerApache.CanDeactivate(context.Background(), services.IDMySQL); err != nil {
		t.Fatalf("non-web service should not be guarded: %v", err)
	}
}

type fakeServingState struct {
	mode, provider string
}

func (f fakeServingState) ServingMode() string     { return f.mode }
func (f fakeServingState) ServingProvider() string { return f.provider }

type fakeWebManager struct {
	activated   []services.ID
	deactivated []services.ID
	validated   []services.ID
	reloaded    []services.ID
}

func (f *fakeWebManager) Activate(context.Context, services.ID) error { return nil }
func (f *fakeWebManager) Deactivate(_ context.Context, id services.ID) error {
	f.deactivated = append(f.deactivated, id)
	return nil
}
func (f *fakeWebManager) Validate(_ context.Context, id services.ID) error {
	f.validated = append(f.validated, id)
	return nil
}
func (f *fakeWebManager) Reload(_ context.Context, id services.ID) error {
	f.reloaded = append(f.reloaded, id)
	return nil
}

func TestManagerRefusesActiveDockerWebServer(t *testing.T) {
	helper := &fakeHelper{}
	preparers := NewWebPreparers(&fakeWebLifecycle{}, WebGuard{State: fakeServingState{mode: "nginx", provider: "docker"}})
	manager := NewManager(t.TempDir(), newFakeStore(), helper, testCatalog(), preparers)
	ctx := context.Background()

	if err := manager.Deactivate(ctx, services.IDNginx); !errors.Is(err, ErrWebServerActive) {
		t.Fatalf("Deactivate error = %v, want ErrWebServerActive", err)
	}
	if len(helper.calls) != 0 {
		t.Fatalf("helper was called for a refused deactivation: %v", helper.calls)
	}
	if _, err := manager.Uninstall(ctx, services.IDNginx, false); !errors.Is(err, ErrWebServerActive) {
		t.Fatalf("Uninstall error = %v, want ErrWebServerActive", err)
	}
	if len(helper.calls) != 0 {
		t.Fatalf("helper was called for a refused uninstall: %v", helper.calls)
	}
}

func TestManagerAllowsInactiveWebServerDeactivate(t *testing.T) {
	helper := &fakeHelper{}
	preparers := NewWebPreparers(&fakeWebLifecycle{}, WebGuard{State: fakeServingState{mode: "builtin", provider: "native"}})
	manager := NewManager(t.TempDir(), newFakeStore(), helper, testCatalog(), preparers)
	if err := manager.Deactivate(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Deactivate(nginx) should be allowed with a native provider guard: %v", err)
	}
	if len(helper.calls) != 1 || helper.calls[0] != "helper_deactivate:nginx" {
		t.Fatalf("helper calls = %v", helper.calls)
	}
}

type validationHelper struct {
	*fakeHelper
	validated []string
	reloaded  []string
	err       error
}

func (h *validationHelper) Validate(_ context.Context, id string) error {
	h.validated = append(h.validated, id)
	return h.err
}

func (h *validationHelper) Reload(_ context.Context, id string) error {
	h.reloaded = append(h.reloaded, id)
	return h.err
}

func TestManagerValidateReloadDelegatesToHelper(t *testing.T) {
	helper := &validationHelper{fakeHelper: &fakeHelper{}}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, testCatalog(), nil)
	if err := manager.Validate(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := manager.Reload(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(helper.validated) != 1 || helper.validated[0] != "nginx" {
		t.Fatalf("validated = %v", helper.validated)
	}
	if len(helper.reloaded) != 1 || helper.reloaded[0] != "nginx" {
		t.Fatalf("reloaded = %v", helper.reloaded)
	}
}

func TestWebSwitchBypassesActiveServerGuard(t *testing.T) {
	helper := &fakeHelper{}
	preparers := NewWebPreparers(&fakeWebLifecycle{}, WebGuard{State: fakeServingState{mode: "nginx", provider: "docker"}})
	manager := NewManager(t.TempDir(), newFakeStore(), helper, testCatalog(), preparers)
	switcher := NewWebSwitch(manager)
	ctx := context.Background()

	if err := switcher.StopActiveServer(ctx, "nginx"); err != nil {
		t.Fatalf("StopActiveServer: %v", err)
	}
	if len(helper.calls) != 1 || helper.calls[0] != "helper_deactivate:nginx" {
		t.Fatalf("helper calls = %v, want explicit provider-switch deactivate", helper.calls)
	}
	if err := switcher.StartActiveServer(ctx, "nginx"); err != nil {
		t.Fatalf("StartActiveServer: %v", err)
	}
	if len(helper.calls) != 2 || helper.calls[1] != "helper_activate:nginx" {
		t.Fatalf("helper calls = %v, want provider-switch activate", helper.calls)
	}
}

func TestWebServiceControlMapsServerNamesToIDs(t *testing.T) {
	manager := &fakeWebManager{}
	control := NewWebServiceControl(manager)
	ctx := context.Background()
	if err := control.Deactivate(ctx, "apache"); err != nil {
		t.Fatal(err)
	}
	if err := control.Validate(ctx, "nginx"); err != nil {
		t.Fatal(err)
	}
	if err := control.Reload(ctx, "nginx"); err != nil {
		t.Fatal(err)
	}
	if len(manager.deactivated) != 1 || manager.deactivated[0] != services.IDApache {
		t.Errorf("deactivated = %v", manager.deactivated)
	}
	if len(manager.validated) != 1 || manager.validated[0] != services.IDNginx {
		t.Errorf("validated = %v", manager.validated)
	}
	if len(manager.reloaded) != 1 || manager.reloaded[0] != services.IDNginx {
		t.Errorf("reloaded = %v", manager.reloaded)
	}
	if err := control.Activate(ctx, "caddy"); err == nil {
		t.Fatal("expected unknown server error")
	}
}

func TestWebServiceControlCanBindAfterConstruction(t *testing.T) {
	control := NewWebServiceControl(nil)
	if err := control.Deactivate(context.Background(), "nginx"); err == nil {
		t.Fatal("expected unbound manager error")
	}
	manager := &fakeWebManager{}
	control.Bind(manager)
	if err := control.Deactivate(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if len(manager.deactivated) != 1 || manager.deactivated[0] != services.IDNginx {
		t.Fatalf("deactivated = %v", manager.deactivated)
	}
}
