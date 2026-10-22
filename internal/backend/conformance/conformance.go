// Package conformance contains a reusable black-box acceptance suite for
// backend.Backend implementations. The local backend runs it in tests today;
// the WSL backend will run the same suite in Plan 3.
package conformance

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// Config describes the fixture a backend needs to run the suite. Root must be
// a directory in the backend namespace containing an index.html, and HTTPPort
// must be a free loopback port.
type Config struct {
	Root     string
	HTTPPort int
	// SiteID overrides the default conformance site id.
	SiteID string
	// Env is passed to the static site.
	Env map[string]string
}

// Suite exercises Prepare, Start, Status, Logs, Exec, Stop, and Health. It is
// safe for any Backend implementation and cleans up the site on failure.
func Suite(t *testing.T, b backend.Backend, cfg Config) {
	t.Helper()
	if b == nil {
		t.Fatal("conformance: nil backend")
	}
	if strings.TrimSpace(cfg.Root) == "" {
		t.Fatal("conformance: Config.Root is required")
	}
	if cfg.HTTPPort < 1 || cfg.HTTPPort > 65535 {
		t.Fatalf("conformance: Config.HTTPPort %d is not a port", cfg.HTTPPort)
	}
	siteID := cfg.SiteID
	if siteID == "" {
		siteID = "conformance-static"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := b.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	spec := backend.SiteSpec{
		ID:          siteID,
		Kind:        string(model.SiteStatic),
		HostPath:    cfg.Root,
		BackendPath: cfg.Root,
		HTTPPort:    cfg.HTTPPort,
		Env:         cfg.Env,
	}
	prepared, err := b.Prepare(ctx, spec)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared.BackendHost == "" {
		t.Fatal("Prepare returned an empty BackendHost")
	}
	if prepared.HTTPPort != cfg.HTTPPort {
		t.Fatalf("Prepare HTTPPort = %d, want %d", prepared.HTTPPort, cfg.HTTPPort)
	}

	info, err := b.Start(ctx, siteID)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.PID <= 0 {
		t.Fatalf("Start PID = %d", info.PID)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = b.Stop(context.Background(), siteID, true)
		}
	})

	status, err := b.Status(ctx, siteID)
	if err != nil {
		t.Fatalf("Status after Start: %v", err)
	}
	if status.State != model.StatusRunning {
		t.Fatalf("Status after Start = %q, want running", status.State)
	}

	url := "http://" + net.JoinHostPort(prepared.BackendHost, strconv.Itoa(prepared.HTTPPort)) + "/"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}

	stream, err := b.Logs(ctx, siteID, true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close logs: %v", err)
	}

	result, err := b.Exec(ctx, siteID,
		[]string{"/bin/sh", "-c", `printf '%s' "$DEVSTACK_CONFORMANCE"`},
		map[string]string{"DEVSTACK_CONFORMANCE": "1"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Exec exit = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	if strings.TrimSpace(result.Stdout) != "1" {
		t.Fatalf("Exec stdout = %q, want 1", result.Stdout)
	}

	if err := b.Stop(ctx, siteID, true); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	stopped = true
	if status, err = waitForState(ctx, b, siteID, model.StatusStopped, 5*time.Second); err != nil {
		t.Fatalf("Status after Stop: %v", err)
	}
	if status.PID != 0 {
		t.Fatalf("Status after Stop PID = %d, want 0", status.PID)
	}

	if err := b.Health(ctx); err != nil {
		t.Fatalf("Health after Stop: %v", err)
	}
}

func waitForState(ctx context.Context, b backend.Backend, id string, want model.SiteStatus, timeout time.Duration) (backend.Status, error) {
	deadline := time.Now().Add(timeout)
	var last backend.Status
	for {
		status, err := b.Status(ctx, id)
		if err != nil {
			return backend.Status{}, err
		}
		last = status
		if status.State == want {
			return status, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("state %q, want %q", last.State, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
