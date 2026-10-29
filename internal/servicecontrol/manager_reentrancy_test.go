package servicecontrol

import (
	"context"
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
