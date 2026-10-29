package servicecontrol

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// reentrantPreparer models the Docker web preparer: its Commit/Rollback path
// calls back into the Manager through servicecontrol.WebServiceControl.
type reentrantPreparer struct {
	manager    *Manager
	commitCall bool
}

func (p *reentrantPreparer) PrepareInstall(context.Context, services.Service) error { return nil }
func (p *reentrantPreparer) PrepareActivate(context.Context, services.Service) error {
	return nil
}
func (p *reentrantPreparer) CommitActivate(ctx context.Context, svc services.Service) error {
	p.commitCall = true
	return p.manager.Deactivate(ctx, svc.ID)
}
func (p *reentrantPreparer) CleanupActivate(context.Context, services.Service, error) error {
	return nil
}
func (p *reentrantPreparer) Credentials(context.Context, services.ID) (Credentials, error) {
	return Credentials{}, ErrCredentialsUnsupported
}
func (p *reentrantPreparer) RotateCredentials(context.Context, services.ID) (Credentials, error) {
	return Credentials{}, ErrCredentialsUnsupported
}

func TestActivateAllowsPreparerReentrantDeactivateWithoutDeadlock(t *testing.T) {
	catalog := services.Catalog{Services: []services.Service{{
		ID:          services.IDNginx,
		DisplayName: "nginx",
		Group:       services.GroupWeb,
		Image:       nginxImage,
	}}}
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		{ID: string(services.IDNginx), State: string(services.StateInstalledButStopped), Installed: true},
	}}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, catalog, nil)
	preparer := &reentrantPreparer{manager: manager}
	manager.RegisterPreparers(map[services.ID]Preparer{services.IDNginx: preparer})

	done := make(chan error, 1)
	go func() {
		done <- manager.Activate(context.Background(), services.IDNginx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Activate: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Activate deadlocked while the preparer called back into the Manager")
	}
	if !preparer.commitCall {
		t.Fatal("CommitActivate was not called")
	}
}

func TestConcurrentProviderSwitchAndActivationSerialize(t *testing.T) {
	catalog := services.Catalog{Services: []services.Service{
		{ID: services.IDNginx, DisplayName: "nginx", Group: services.GroupWeb, Image: nginxImage},
		{ID: services.IDRedis, DisplayName: "redis", Group: services.GroupStandalone, Image: redisImage},
	}}
	helper := &serializedHelper{}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, catalog, nil)

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		errs <- manager.StopForProviderSwitch(context.Background(), services.IDNginx)
	}()
	go func() {
		<-start
		errs <- manager.Activate(context.Background(), services.IDRedis)
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("operation: %v", err)
		}
	}
	if got := helper.maxConcurrent(); got > 1 {
		t.Fatalf("lifecycle RPCs overlapped: max concurrency %d", got)
	}
}

type serializedHelper struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (h *serializedHelper) enter() {
	h.mu.Lock()
	h.active++
	if h.active > h.maxActive {
		h.maxActive = h.active
	}
	h.mu.Unlock()
	time.Sleep(25 * time.Millisecond)
	h.mu.Lock()
	h.active--
	h.mu.Unlock()
}

func (h *serializedHelper) maxConcurrent() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.maxActive
}

func (h *serializedHelper) Status(context.Context) ([]helperproto.ServiceStatus, error) {
	return nil, nil
}
func (h *serializedHelper) Install(context.Context, string) error    { h.enter(); return nil }
func (h *serializedHelper) Activate(context.Context, string) error   { h.enter(); return nil }
func (h *serializedHelper) Deactivate(context.Context, string) error { h.enter(); return nil }
func (h *serializedHelper) Restart(context.Context, string) error    { h.enter(); return nil }
func (h *serializedHelper) Uninstall(context.Context, string, bool) error {
	h.enter()
	return nil
}
func (h *serializedHelper) Logs(context.Context, string, int) (string, error) { return "", nil }
