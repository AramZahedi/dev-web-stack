//go:build !windows

package helper_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/hosts"
)

func helperFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func helperUniquePort(t *testing.T, used map[int]bool) int {
	t.Helper()
	for {
		port := helperFreePort(t)
		if used[port] {
			continue
		}
		used[port] = true
		return port
	}
}

func targetPort(t *testing.T, server *httptest.Server) int {
	t.Helper()
	addr, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("target address = %T, want *net.TCPAddr", server.Listener.Addr())
	}
	return addr.Port
}

func forwarderConfig(t *testing.T, forwardHTTP, forwardHTTPS, proxyHTTP, proxyHTTPS int) helper.Config {
	t.Helper()
	return helper.Config{
		AllowUID:         os.Getuid(),
		ForwardHTTPPort:  forwardHTTP,
		ForwardHTTPSPort: forwardHTTPS,
		ProxyHTTPPort:    proxyHTTP,
		ProxyHTTPSPort:   proxyHTTPS,
	}
}

func getForwarded(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("request %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(data)
}

func TestHelperForwarders(t *testing.T) {
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "http-ok")
	}))
	defer httpTarget.Close()
	httpsTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "https-ok")
	}))
	defer httpsTarget.Close()

	httpTargetPort := targetPort(t, httpTarget)
	httpsTargetPort := targetPort(t, httpsTarget)
	used := map[int]bool{httpTargetPort: true, httpsTargetPort: true}
	forwardHTTP := helperUniquePort(t, used)
	forwardHTTPS := helperUniquePort(t, used)

	socketPath, _ := startTestServer(t, forwarderConfig(t, forwardHTTP, forwardHTTPS, httpTargetPort, httpsTargetPort))
	c := dialClient(t, socketPath)
	ctx := context.Background()
	client := &http.Client{Timeout: 3 * time.Second}

	if err := c.SetForwarders(ctx, httpTargetPort, httpsTargetPort, true); err != nil {
		t.Fatalf("SetForwarders enable: %v", err)
	}
	defer func() { _ = c.SetForwarders(context.Background(), httpTargetPort, httpsTargetPort, false) }()

	if got := getForwarded(t, client, "http://127.0.0.1:"+strconv.Itoa(forwardHTTP)+"/"); got != "http-ok" {
		t.Fatalf("HTTP forward body = %q, want http-ok", got)
	}
	if got := getForwarded(t, client, "http://127.0.0.1:"+strconv.Itoa(forwardHTTPS)+"/"); got != "https-ok" {
		t.Fatalf("HTTPS forward body = %q, want https-ok", got)
	}

	// Repeating the same request is a no-op rather than a bind conflict.
	if err := c.SetForwarders(ctx, httpTargetPort, httpsTargetPort, true); err != nil {
		t.Fatalf("SetForwarders repeat: %v", err)
	}
	// Zero means "use the configured proxy ports".
	if err := c.SetForwarders(ctx, 0, 0, true); err != nil {
		t.Fatalf("SetForwarders with configured ports: %v", err)
	}

	// The RPC cannot redirect the forwarder to a different port.
	if err := c.SetForwarders(ctx, httpTargetPort+1, httpsTargetPort, true); !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("arbitrary target port error = %v, want ErrInvalidRequest", err)
	}
	if err := c.SetForwarders(ctx, -1, httpsTargetPort, true); !errors.Is(err, helperclient.ErrInvalidRequest) {
		t.Fatalf("negative target port error = %v, want ErrInvalidRequest", err)
	}

	if err := c.SetForwarders(ctx, httpTargetPort, httpsTargetPort, false); err != nil {
		t.Fatalf("SetForwarders disable: %v", err)
	}
	if err := c.SetForwarders(ctx, httpTargetPort, httpsTargetPort, false); err != nil {
		t.Fatalf("SetForwarders disable again: %v", err)
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(forwardHTTP), 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("HTTP listener still accepts after disable")
	}
}

func TestHelperAutoForwardStartsConfiguredForwarders(t *testing.T) {
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "auto-http-ok")
	}))
	defer httpTarget.Close()
	httpsTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "auto-https-ok")
	}))
	defer httpsTarget.Close()

	httpTargetPort := targetPort(t, httpTarget)
	httpsTargetPort := targetPort(t, httpsTarget)
	used := map[int]bool{httpTargetPort: true, httpsTargetPort: true}
	forwardHTTP := helperUniquePort(t, used)
	forwardHTTPS := helperUniquePort(t, used)

	cfg := forwarderConfig(t, forwardHTTP, forwardHTTPS, httpTargetPort, httpsTargetPort)
	cfg.AutoForward = true
	startTestServer(t, cfg)

	client := &http.Client{Timeout: 3 * time.Second}
	if got := getForwarded(t, client, "http://127.0.0.1:"+strconv.Itoa(forwardHTTP)+"/"); got != "auto-http-ok" {
		t.Fatalf("auto HTTP forward body = %q", got)
	}
	if got := getForwarded(t, client, "http://127.0.0.1:"+strconv.Itoa(forwardHTTPS)+"/"); got != "auto-https-ok" {
		t.Fatalf("auto HTTPS forward body = %q", got)
	}
}

func TestHelperAutoForwardBindFailureIsStartupError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	conflictPort := occupied.Addr().(*net.TCPAddr).Port

	httpTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer httpTarget.Close()
	httpsTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer httpsTarget.Close()

	httpTargetPort := targetPort(t, httpTarget)
	httpsTargetPort := targetPort(t, httpsTarget)
	used := map[int]bool{httpTargetPort: true, httpsTargetPort: true, conflictPort: true}
	forwardHTTPS := helperUniquePort(t, used)

	dir := t.TempDir()
	cfg := forwarderConfig(t, conflictPort, forwardHTTPS, httpTargetPort, httpsTargetPort)
	cfg.AutoForward = true
	cfg.SocketPath = filepath.Join(dir, "run", "helper.sock")
	cfg.HostsPath = filepath.Join(dir, "hosts")
	server, err := helper.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()

	startErr := server.Start(context.Background())
	if startErr == nil {
		t.Fatal("Start succeeded with an occupied auto-forward port")
	}
	for _, want := range []string{"127.0.0.1:" + strconv.Itoa(conflictPort), "auto_forward"} {
		if !strings.Contains(startErr.Error(), want) {
			t.Fatalf("start error = %q, want %q", startErr, want)
		}
	}
	if _, statErr := os.Stat(cfg.SocketPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed auto-forward left socket behind: %v", statErr)
	}
}

func TestHelperForwarderBindConflict(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	conflictPort := occupied.Addr().(*net.TCPAddr).Port

	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer httpTarget.Close()
	httpsTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer httpsTarget.Close()

	httpTargetPort := targetPort(t, httpTarget)
	httpsTargetPort := targetPort(t, httpsTarget)
	used := map[int]bool{httpTargetPort: true, httpsTargetPort: true, conflictPort: true}
	forwardHTTPS := helperUniquePort(t, used)

	socketPath, _ := startTestServer(t, forwarderConfig(t, conflictPort, forwardHTTPS, httpTargetPort, httpsTargetPort))
	c := dialClient(t, socketPath)

	err = c.SetForwarders(context.Background(), httpTargetPort, httpsTargetPort, true)
	if err == nil {
		_ = c.SetForwarders(context.Background(), httpTargetPort, httpsTargetPort, false)
		t.Fatal("SetForwarders succeeded with an occupied listen port")
	}
	var apiErr *helperclient.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %T, want *helperclient.Error", err)
	}
	listen := "127.0.0.1:" + strconv.Itoa(conflictPort)
	if !strings.Contains(err.Error(), listen) {
		t.Fatalf("error = %q, want listen address %q", err, listen)
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("error = %q, want an address-in-use explanation", err)
	}

	// The failed start must not leave the other listener bound.
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(forwardHTTPS))
	if err != nil {
		t.Fatalf("HTTPS listener left behind after failed start: %v", err)
	}
	_ = ln.Close()
}

func TestHelperCloseStopsForwarders(t *testing.T) {
	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer httpTarget.Close()
	httpsTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer httpsTarget.Close()

	httpTargetPort := targetPort(t, httpTarget)
	httpsTargetPort := targetPort(t, httpsTarget)
	used := map[int]bool{httpTargetPort: true, httpsTargetPort: true}
	forwardHTTP := helperUniquePort(t, used)
	forwardHTTPS := helperUniquePort(t, used)

	dir := t.TempDir()
	cfg := forwarderConfig(t, forwardHTTP, forwardHTTPS, httpTargetPort, httpsTargetPort)
	cfg.SocketPath = filepath.Join(dir, "run", "helper.sock")
	cfg.HostsPath = filepath.Join(dir, "hosts")
	srv, err := helper.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatal(err)
	}
	c := dialClient(t, cfg.SocketPath)
	if err := c.SetForwarders(ctx, httpTargetPort, httpsTargetPort, true); err != nil {
		t.Fatalf("SetForwarders: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(forwardHTTP), 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("forwarder still accepts after helper Close")
	}
}

func TestHelperUninstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	anchorDir := t.TempDir()
	caDir := filepath.Join(t.TempDir(), "ca")
	runner := &serverFakeRunner{}

	httpTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer httpTarget.Close()
	httpsTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer httpsTarget.Close()

	httpTargetPort := targetPort(t, httpTarget)
	httpsTargetPort := targetPort(t, httpsTarget)
	used := map[int]bool{httpTargetPort: true, httpsTargetPort: true}
	forwardHTTP := helperUniquePort(t, used)
	forwardHTTPS := helperUniquePort(t, used)

	cfg := forwarderConfig(t, forwardHTTP, forwardHTTPS, httpTargetPort, httpsTargetPort)
	cfg.AllowHome = home
	cfg.CADir = caDir
	cfg.TrustDir = anchorDir
	cfg.Runner = runner
	socketPath, hostsPath := startTestServer(t, cfg)
	c := dialClient(t, socketPath)
	ctx := context.Background()

	if _, err := c.EnsureCA(ctx); err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	if _, err := c.InstallTrust(ctx, home); err != nil {
		t.Fatalf("InstallTrust: %v", err)
	}
	anchorPath := filepath.Join(anchorDir, "devstack-root.crt")
	if _, err := os.Stat(anchorPath); err != nil {
		t.Fatalf("anchor not installed: %v", err)
	}
	if err := c.SyncHosts(ctx, []string{"blog.test", "www.blog.test"}); err != nil {
		t.Fatalf("SyncHosts: %v", err)
	}
	if err := c.SetForwarders(ctx, httpTargetPort, httpsTargetPort, true); err != nil {
		t.Fatalf("SetForwarders: %v", err)
	}
	if got := getForwarded(t, &http.Client{Timeout: 3 * time.Second}, "http://127.0.0.1:"+strconv.Itoa(forwardHTTP)+"/"); got != "ok" {
		t.Fatalf("forward before uninstall = %q", got)
	}

	if err := c.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if _, err := os.Stat(anchorPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchor still present after uninstall: %v", err)
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatalf("read hosts after uninstall: %v", err)
	}
	domains, found := hosts.Parse(string(data))
	if !found {
		t.Fatalf("managed block removed from hosts file %q", data)
	}
	if len(domains) != 0 {
		t.Fatalf("domains after uninstall = %v, want none", domains)
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(forwardHTTP), 200*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("forwarder still accepts after uninstall")
	}

	// Uninstall is idempotent.
	if err := c.Uninstall(ctx); err != nil {
		t.Fatalf("second Uninstall: %v", err)
	}
}

func TestHelperUninstallWithoutCA(t *testing.T) {
	socketPath, hostsPath := newTestServer(t, os.Getuid())
	c := dialClient(t, socketPath)
	ctx := context.Background()

	if err := c.SyncHosts(ctx, []string{"blog.test"}); err != nil {
		t.Fatalf("SyncHosts: %v", err)
	}
	if err := c.Uninstall(ctx); err != nil {
		t.Fatalf("Uninstall without CA: %v", err)
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}
	domains, found := hosts.Parse(string(data))
	if !found || len(domains) != 0 {
		t.Fatalf("hosts after uninstall = %q (found=%v, domains=%v)", data, found, domains)
	}
	if err := c.Uninstall(ctx); err != nil {
		t.Fatalf("second Uninstall: %v", err)
	}
}
