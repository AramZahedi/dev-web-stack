package servicecontrol

import (
	"context"
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestRestartOfStoppedExclusiveMemberIsRejectedWhilePeerRuns(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		{ID: string(services.IDNginx), State: string(services.StateInstalledButStopped), Installed: true},
		{ID: string(services.IDApache), State: string(services.StateActive), Installed: true, Active: true},
	}}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, exclusiveCatalog(), nil)

	err := manager.Restart(context.Background(), services.IDNginx)
	if !errors.Is(err, helperproto.ErrServiceConflict) {
		t.Fatalf("Restart() error = %v, want ErrServiceConflict", err)
	}
	for _, call := range helper.calls {
		if call == "helper_restart:nginx" || call == "helper_activate:nginx" {
			t.Fatalf("helper started the conflicting service: %v", helper.calls)
		}
	}
}

func TestRestartOfActiveExclusiveMemberStillWorks(t *testing.T) {
	helper := &fakeHelper{statuses: []helperproto.ServiceStatus{
		{ID: string(services.IDNginx), State: string(services.StateActive), Installed: true, Active: true},
		{ID: string(services.IDApache), State: string(services.StateInstalledButStopped), Installed: true},
	}}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, exclusiveCatalog(), nil)

	if err := manager.Restart(context.Background(), services.IDNginx); err != nil {
		t.Fatalf("Restart(active member): %v", err)
	}
	if len(helper.calls) != 1 || helper.calls[0] != "helper_restart:nginx" {
		t.Fatalf("helper calls = %v", helper.calls)
	}
}
