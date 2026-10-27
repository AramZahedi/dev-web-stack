//go:build windows

package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
)

func TestWindowsPublicEntryUpdatesHelperTargetsBeforeReconcile(t *testing.T) {
	var order []string
	err := startWindowsPublicEntry(context.Background(),
		func() error { order = append(order, "proxy"); return nil },
		func(context.Context) error { order = append(order, "helper-targets"); return nil },
		func(context.Context) error { order = append(order, "reconcile"); return nil },
	)
	if err != nil {
		t.Fatalf("startWindowsPublicEntry: %v", err)
	}
	if want := []string{"proxy", "helper-targets", "reconcile"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("startup order = %v, want %v", order, want)
	}
}

func TestWindowsPublicEntryDoesNotReconcileWhenHelperTargetUpdateFails(t *testing.T) {
	var order []string
	wantErr := context.DeadlineExceeded
	err := startWindowsPublicEntry(context.Background(),
		func() error { order = append(order, "proxy"); return nil },
		func(context.Context) error { order = append(order, "helper-targets"); return wantErr },
		func(context.Context) error { order = append(order, "reconcile"); return nil },
	)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("startup error = %v, want %v", err, wantErr)
	}
	if want := []string{"proxy", "helper-targets"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("startup order = %v, want %v", order, want)
	}
}

func TestWindowsProxyRoutesDashboardToLoopbackUI(t *testing.T) {
	cfg := config.Default()
	router, _, err := newWindowsProxy(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	route, ok := router.Lookup("devstack.test")
	if !ok || route.Upstream != "127.0.0.1:7070" {
		t.Fatalf("dashboard route = %+v, found=%v", route, ok)
	}
}
