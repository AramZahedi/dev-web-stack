package daemon

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// backendReadyTimeout bounds the host-side wait for a freshly started backend.
// WSL2 localhost forwarding can lag the agent's own readiness probe by about a
// second; the proxy would otherwise return 502 immediately after a start.
var backendReadyTimeout = 2 * time.Second

// Node dev servers may compile or boot for many seconds before listening.
var backendNodeReadyTimeout = 30 * time.Second

func backendReadyBudget(site model.Site) time.Duration {
	if site.Kind == model.SiteNode {
		return backendNodeReadyTimeout
	}
	return backendReadyTimeout
}

func waitBackendReady(ctx context.Context, site model.Site) error {
	port := site.HTTPPort
	if site.Kind == model.SitePHP {
		port = site.FastCGIPort
	}
	if port <= 0 {
		return nil
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(backendReadyBudget(site))
	var lastErr error
	for {
		dialer := net.Dialer{Timeout: 100 * time.Millisecond}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("host cannot reach backend on %s: %w", addr, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
