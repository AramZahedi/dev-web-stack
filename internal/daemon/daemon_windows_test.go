//go:build windows

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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

func TestInstallManifestDetectionRequiresARegularFile(t *testing.T) {
	root := t.TempDir()
	if installManifestExists(root) {
		t.Fatal("empty directory reported an install manifest")
	}
	systemDir := filepath.Join(root, "system")
	if err := os.MkdirAll(filepath.Join(systemDir, "install-manifest.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if installManifestExists(root) {
		t.Fatal("manifest directory was accepted as a regular file")
	}
	if err := os.Remove(filepath.Join(systemDir, "install-manifest.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemDir, "install-manifest.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !installManifestExists(root) {
		t.Fatal("regular install manifest was not detected")
	}
}

func TestWSLKeepaliveBackoffEscalatesAndCaps(t *testing.T) {
	cases := []struct {
		failures int
		ranFor   time.Duration
		want     time.Duration
	}{
		{0, 0, keepaliveMinBackoff},
		{1, 0, 250 * time.Millisecond},
		{2, 0, 500 * time.Millisecond},
		{3, 0, time.Second},
		{4, 0, 2 * time.Second},
		{5, 0, 4 * time.Second},
		{8, 0, keepaliveMaxBackoff},
		{20, 0, keepaliveMaxBackoff},
		{20, keepaliveHealthyRun, keepaliveMinBackoff},
		{9, keepaliveHealthyRun + time.Second, keepaliveMinBackoff},
	}
	for _, tc := range cases {
		got := keepaliveBackoff(tc.failures, tc.ranFor)
		if got != tc.want {
			t.Errorf("keepaliveBackoff(%d, %s) = %s, want %s", tc.failures, tc.ranFor, got, tc.want)
		}
	}
}

func TestWSLKeepaliveBackoffNeverHotLoops(t *testing.T) {
	for failures := 1; failures < 64; failures++ {
		if delay := keepaliveBackoff(failures, 0); delay < keepaliveMinBackoff {
			t.Fatalf("failures=%d delay=%s is below the minimum", failures, delay)
		}
		if delay := keepaliveBackoff(failures, 0); delay > keepaliveMaxBackoff {
			t.Fatalf("failures=%d delay=%s exceeds the cap", failures, delay)
		}
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
