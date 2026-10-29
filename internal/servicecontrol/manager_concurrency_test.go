package servicecontrol

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type exclusiveTestHelper struct {
	mu            sync.Mutex
	active        services.ID
	statusDelay   time.Duration
	activateCalls int
}

func (h *exclusiveTestHelper) Status(ctx context.Context) ([]helperproto.ServiceStatus, error) {
	if h.statusDelay > 0 {
		select {
		case <-time.After(h.statusDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	statuses := make([]helperproto.ServiceStatus, 0, 2)
	for _, id := range []services.ID{services.IDNginx, services.IDApache} {
		statuses = append(statuses, helperproto.ServiceStatus{
			ID:        string(id),
			State:     string(services.StateInstalledButStopped),
			Installed: true,
			Active:    h.active == id,
		})
	}
	return statuses, nil
}

func (h *exclusiveTestHelper) Install(context.Context, string) error { return nil }

func (h *exclusiveTestHelper) Activate(_ context.Context, id string) error {
	h.mu.Lock()
	h.activateCalls++
	h.active = services.ID(id)
	h.mu.Unlock()
	return nil
}

func (h *exclusiveTestHelper) Deactivate(context.Context, string) error { return nil }
func (h *exclusiveTestHelper) Restart(context.Context, string) error    { return nil }
func (h *exclusiveTestHelper) Uninstall(context.Context, string, bool) error {
	return nil
}
func (h *exclusiveTestHelper) Logs(context.Context, string, int) (string, error) {
	return "", nil
}

func exclusiveCatalog() services.Catalog {
	image := "nginx@sha256:" + "a" + "000000000000000000000000000000000000000000000000000000000000000"
	return services.Catalog{Services: []services.Service{
		{ID: services.IDNginx, DisplayName: "Nginx", Group: services.GroupWeb, Image: image},
		{ID: services.IDApache, DisplayName: "Apache", Group: services.GroupWeb, Image: image},
	}}
}

func TestConcurrentActivationOfExclusiveGroupSerializes(t *testing.T) {
	helper := &exclusiveTestHelper{statusDelay: 100 * time.Millisecond}
	manager := NewManager(t.TempDir(), newFakeStore(), helper, exclusiveCatalog(), nil)

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, id := range []services.ID{services.IDNginx, services.IDApache} {
		go func(id services.ID) {
			<-start
			errs <- manager.Activate(context.Background(), id)
		}(id)
	}
	close(start)

	var successes, conflicts int
	for i := 0; i < 2; i++ {
		err := <-errs
		switch {
		case err == nil:
			successes++
		case errors.Is(err, helperproto.ErrServiceConflict):
			conflicts++
		default:
			t.Fatalf("activation error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	helper.mu.Lock()
	calls := helper.activateCalls
	helper.mu.Unlock()
	if calls != 1 {
		t.Fatalf("helper Activate calls = %d, want 1", calls)
	}
}
