package daemon

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestBackendReadyBudgetGivesNodeMoreTime(t *testing.T) {
	if backendReadyBudget(model.Site{Kind: model.SiteStatic}) != backendReadyTimeout {
		t.Fatal("static budget changed")
	}
	if backendReadyBudget(model.Site{Kind: model.SiteNode}) <= backendReadyTimeout {
		t.Fatal("Node sites must allow a longer boot budget")
	}
}

func TestWaitBackendReadyWaitsForLoopbackListener(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	_ = reserved.Close()

	previous := backendReadyTimeout
	backendReadyTimeout = 500 * time.Millisecond
	t.Cleanup(func() { backendReadyTimeout = previous })

	go func() {
		time.Sleep(60 * time.Millisecond)
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			defer listener.Close()
			time.Sleep(200 * time.Millisecond)
		}
	}()

	site := model.Site{ID: "site", Kind: model.SiteStatic, HTTPPort: port}
	if err := waitBackendReady(context.Background(), site); err != nil {
		t.Fatalf("waitBackendReady: %v", err)
	}
}

func TestWaitBackendReadyReportsUnreachablePort(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.Addr().(*net.TCPAddr).Port
	_ = reserved.Close()

	previous := backendReadyTimeout
	backendReadyTimeout = 80 * time.Millisecond
	t.Cleanup(func() { backendReadyTimeout = previous })

	site := model.Site{ID: "site", Kind: model.SitePHP, FastCGIPort: port}
	if err := waitBackendReady(context.Background(), site); err == nil {
		t.Fatal("waitBackendReady accepted an unreachable port")
	}
}
