//go:build !windows

package helper_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/hosts"
)

func newTestServer(t *testing.T, allowUID int) (socketPath, hostsPath string) {
	t.Helper()
	return startTestServer(t, helper.Config{AllowUID: allowUID})
}

func startTestServer(t *testing.T, cfg helper.Config) (string, string) {
	t.Helper()
	dir := t.TempDir()
	cfg.SocketPath = filepath.Join(dir, "run", "helper.sock")
	cfg.HostsPath = filepath.Join(dir, "hosts")
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	srv, err := helper.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close helper: %v", err)
		}
	})
	return cfg.SocketPath, cfg.HostsPath
}

func dialClient(t *testing.T, socketPath string) *helperclient.Client {
	t.Helper()
	c, err := helperclient.Dial(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close client: %v", err)
		}
	})
	return c
}

func TestHelperStatusAndHostsSync(t *testing.T) {
	socketPath, hostsPath := newTestServer(t, os.Getuid())
	c := dialClient(t, socketPath)
	ctx := context.Background()

	status, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Version != helperproto.Version {
		t.Fatalf("version = %q", status.Version)
	}
	if status.HostsSynced {
		t.Fatal("hosts reported synced before first sync")
	}
	if status.CAFingerprint != "" {
		t.Fatalf("unexpected CA fingerprint %q", status.CAFingerprint)
	}

	if err := c.SyncHosts(ctx, []string{"www.blog.test", "blog.test"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	domains, found := hosts.Parse(string(data))
	if !found {
		t.Fatalf("managed block missing from %q", data)
	}
	if len(domains) != 2 || domains[0] != "blog.test" || domains[1] != "www.blog.test" {
		t.Fatalf("domains = %v", domains)
	}

	status, err = c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.HostsSynced {
		t.Fatal("hosts not reported synced after sync")
	}

	if err := c.SyncHosts(ctx, []string{"blog.test"}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "www.blog.test") {
		t.Fatalf("stale domain kept in %q", data)
	}
}

func TestHelperRejectsWrongUID(t *testing.T) {
	allowUID := os.Getuid() + 1
	socketPath, hostsPath := newTestServer(t, allowUID)
	c := dialClient(t, socketPath)

	err := c.SyncHosts(context.Background(), []string{"blog.test"})
	if err == nil {
		t.Fatal("want unauthorized error")
	}
	if !errors.Is(err, helperclient.ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
	var apiErr *helperclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T, want *helperclient.Error", err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", apiErr.Status)
	}
	if _, statErr := os.Stat(hostsPath); !os.IsNotExist(statErr) {
		t.Fatalf("handler ran for rejected peer: stat error = %v", statErr)
	}
}

func TestHelperRejectsProtocolMismatch(t *testing.T) {
	socketPath, _ := newTestServer(t, os.Getuid())
	raw := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}}
	defer raw.CloseIdleConnections()

	for _, version := range []string{"", "2"} {
		req, err := http.NewRequest(http.MethodGet, "http://helper/v1/status", nil)
		if err != nil {
			t.Fatal(err)
		}
		if version != "" {
			req.Header.Set(helperproto.ProtocolHeader, version)
		}
		resp, err := raw.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body helperproto.ErrorResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUpgradeRequired {
			t.Fatalf("version %q: status = %d, want 426", version, resp.StatusCode)
		}
		if decodeErr != nil {
			t.Fatalf("version %q: decode error: %v", version, decodeErr)
		}
		if body.Code != helperproto.CodeProtocolMismatch {
			t.Fatalf("version %q: code = %q", version, body.Code)
		}
		if got := resp.Header.Get(helperproto.ProtocolHeader); got != helperproto.Version {
			t.Fatalf("version %q: supported header = %q", version, got)
		}
	}
}

func TestHelperReportsNotImplemented(t *testing.T) {
	socketPath, _ := newTestServer(t, os.Getuid())
	c := dialClient(t, socketPath)
	ctx := context.Background()

	if _, err := c.EnsureCA(ctx); !errors.Is(err, helperclient.ErrNotImplemented) {
		t.Fatalf("EnsureCA error = %v, want ErrNotImplemented", err)
	}
	if got := c.RootPEM(); got != nil {
		t.Fatalf("RootPEM after failed EnsureCA = %q", got)
	}
	if _, err := c.SignCertificate(ctx, []byte("csr"), []string{"blog.test"}); !errors.Is(err, helperclient.ErrNotImplemented) {
		t.Fatalf("SignCertificate error = %v, want ErrNotImplemented", err)
	}
	if _, err := c.InstallTrust(ctx, "/home/user"); !errors.Is(err, helperclient.ErrNotImplemented) {
		t.Fatalf("InstallTrust error = %v, want ErrNotImplemented", err)
	}
	if _, err := c.RemoveTrust(ctx, "/home/user"); !errors.Is(err, helperclient.ErrNotImplemented) {
		t.Fatalf("RemoveTrust error = %v, want ErrNotImplemented", err)
	}
	if _, err := c.TrustStatus(ctx, "/home/user"); !errors.Is(err, helperclient.ErrNotImplemented) {
		t.Fatalf("TrustStatus error = %v, want ErrNotImplemented", err)
	}
}

func TestHelperServiceHooks(t *testing.T) {
	root := []byte("-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n")
	socketPath, _ := startTestServer(t, helper.Config{
		AllowUID: os.Getuid(),
		Service: helper.Service{
			CAFingerprint: func() string { return "abc123" },
			EnsureCA: func(context.Context) (helperproto.EnsureCAResponse, error) {
				return helperproto.EnsureCAResponse{RootPEM: root, Fingerprint: "abc123"}, nil
			},
		},
	})
	c := dialClient(t, socketPath)
	ctx := context.Background()

	resp, err := c.EnsureCA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.RootPEM) != string(root) || resp.Fingerprint != "abc123" {
		t.Fatalf("EnsureCA = %+v", resp)
	}
	cached := c.RootPEM()
	if string(cached) != string(root) {
		t.Fatalf("RootPEM = %q, want %q", cached, root)
	}
	cached[0] = 'X'
	if string(c.RootPEM()) != string(root) {
		t.Fatal("RootPEM did not return a copy")
	}

	status, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.CAFingerprint != "abc123" {
		t.Fatalf("CA fingerprint = %q", status.CAFingerprint)
	}
}

func TestHelperRejectsInvalidHostsDomain(t *testing.T) {
	socketPath, hostsPath := newTestServer(t, os.Getuid())
	c := dialClient(t, socketPath)

	err := c.SyncHosts(context.Background(), []string{"evil.com"})
	if !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if _, statErr := os.Stat(hostsPath); !os.IsNotExist(statErr) {
		t.Fatalf("hosts file written for invalid domain: stat error = %v", statErr)
	}
}

func TestHelperSocketPermissions(t *testing.T) {
	socketPath, _ := newTestServer(t, os.Getuid())
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, want 0600", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat type = %T", info.Sys())
	}
	if int(stat.Uid) != os.Getuid() {
		t.Fatalf("socket owner = %d, want %d", stat.Uid, os.Getuid())
	}
	dirInfo, err := os.Stat(filepath.Dir(socketPath))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("socket dir mode = %v, want 0755", dirInfo.Mode().Perm())
	}
}

func TestHelperRejectsTooManyHostsDomains(t *testing.T) {
	socketPath, hostsPath := newTestServer(t, os.Getuid())
	c := dialClient(t, socketPath)
	domains := make([]string, 1001)
	for i := range domains {
		domains[i] = fmt.Sprintf("s%d.test", i)
	}
	err := c.SyncHosts(context.Background(), domains)
	if !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if _, statErr := os.Stat(hostsPath); !os.IsNotExist(statErr) {
		t.Fatalf("hosts file written for oversized request: stat error = %v", statErr)
	}
}
