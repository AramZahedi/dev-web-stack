package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

var (
	testBinaryOnce sync.Once
	testBinaryDir  string
	testBinaryPath string
	testBinaryErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if testBinaryDir != "" {
		_ = os.RemoveAll(testBinaryDir)
	}
	os.Exit(code)
}

// devstackBinary builds the real CLI once so the daemon can supervise static
// sites through `devstack serve-static`. Building takes a few seconds but keeps
// the daemon tests end-to-end.
func devstackBinary(t *testing.T) string {
	t.Helper()
	testBinaryOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			testBinaryErr = errors.New("resolve test source path")
			return
		}
		repo := filepath.Dir(filepath.Dir(filepath.Dir(file)))
		testBinaryDir, testBinaryErr = os.MkdirTemp("", "devstack-daemon-bin-")
		if testBinaryErr != nil {
			return
		}
		testBinaryPath = filepath.Join(testBinaryDir, "devstack")
		cmd := exec.Command("go", "build", "-o", testBinaryPath, "./cmd/devstack")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			testBinaryErr = fmt.Errorf("build devstack: %w\n%s", err, out)
		}
	})
	if testBinaryErr != nil {
		t.Fatal(testBinaryErr)
	}
	return testBinaryPath
}

// useBuiltBinary points the daemon's self-executable lookup at the real CLI.
func useBuiltBinary(t *testing.T) {
	t.Helper()
	path := devstackBinary(t)
	previous := selfExecutable
	selfExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { selfExecutable = previous })
}

// startTestHelper runs a real helper server against temporary state. It uses
// high forwarder ports so the test never needs root.
func startTestHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	hosts := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	allowHome := filepath.Join(dir, "allowed-home")
	if err := os.MkdirAll(allowHome, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := helper.Config{
		SocketPath:       filepath.Join(dir, "run", "helper.sock"),
		HostsPath:        hosts,
		AllowUID:         os.Getuid(),
		AllowHome:        allowHome,
		CADir:            filepath.Join(dir, "pki"),
		TrustDir:         filepath.Join(dir, "trust"),
		ForwardHTTPPort:  freePort(t),
		ForwardHTTPSPort: freePort(t),
		ProxyHTTPPort:    freePort(t),
		ProxyHTTPSPort:   freePort(t),
	}
	srv, err := helper.NewServer(cfg)
	if err != nil {
		t.Fatalf("new helper: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	return cfg.SocketPath
}

func testOptions(t *testing.T, home, helperSocket string) Options {
	t.Helper()
	return Options{
		Home:         home,
		HelperSocket: helperSocket,
		HTTPPort:     freePort(t),
		HTTPSPort:    freePort(t),
		UIPort:       freePort(t),
		LogLevel:     "error",
	}
}

type runningDaemon struct {
	cancel context.CancelFunc
	done   chan error
}

func startDaemon(t *testing.T, opts Options) *runningDaemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	run := &runningDaemon{cancel: cancel, done: done}
	t.Cleanup(func() { run.stop(t) })
	waitForSocket(t, SocketPath(opts.Home), done)
	return run
}

func (r *runningDaemon) stop(t *testing.T) {
	t.Helper()
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
	r.cancel = nil
	select {
	case err := <-r.done:
		if err != nil {
			t.Errorf("daemon Run returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Error("daemon did not shut down")
	}
}

func waitForSocket(t *testing.T, socket string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("daemon exited before the socket appeared: %v", err)
		default:
		}
		conn, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon socket %s did not appear: %v", socket, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func unixHTTPClient(socket string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socket)
			},
		},
	}
}

func apiRequest(t *testing.T, client *http.Client, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://devstack.test"+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

func createStaticSite(t *testing.T, client *http.Client, name, root string) model.Site {
	t.Helper()
	resp, data := apiRequest(t, client, http.MethodPost, "/api/v1/sites", map[string]any{
		"name": name,
		"path": root,
		"kind": "static",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create site = %d %s", resp.StatusCode, data)
	}
	var site model.Site
	if err := json.Unmarshal(data, &site); err != nil {
		t.Fatal(err)
	}
	return site
}

func getHealth(t *testing.T, client *http.Client) {
	t.Helper()
	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health = %d %s", resp.StatusCode, data)
	}
	var payload map[string]string
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("health payload = %+v", payload)
	}
}

func fetchSiteBody(t *testing.T, port int) string {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET site: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET site = %d %q", resp.StatusCode, body)
	}
	return string(body)
}

func waitTCPClosed(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("site port %d is still accepting connections", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readSiteFromStore(t *testing.T, home string) model.Site {
	t.Helper()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	sites, err := st.ListSites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("stored sites = %d, want 1", len(sites))
	}
	return sites[0]
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestDaemonServesAndRestartsCleanly(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	helperSocket := startTestHelper(t)
	opts := testOptions(t, home, helperSocket)
	socket := SocketPath(opts.Home)

	for round := 0; round < 2; round++ {
		run := startDaemon(t, opts)
		client := unixHTTPClient(socket, 5*time.Second)
		getHealth(t, client)

		if info, err := os.Stat(socket); err != nil {
			t.Fatalf("stat socket: %v", err)
		} else if info.Mode()&os.ModeSocket == 0 {
			t.Fatalf("socket mode = %v, want a Unix socket", info.Mode())
		} else if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("socket permissions = %04o, want 0600", perm)
		}

		run.stop(t)
		if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("socket still exists after shutdown: %v", err)
		}
		if _, err := os.Stat(PidPath(home)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("pid file still exists after shutdown: %v", err)
		}
	}
}

func TestDaemonSocketHostGuard(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))
	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	req, err := http.NewRequest(http.MethodGet, "http://devstack.test/api/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host over the socket = %d, want 403", resp.StatusCode)
	}
}

func TestRunRefusesToReplaceNonSocket(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(SocketPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("not a socket")
	if err := os.WriteFile(SocketPath(home), sentinel, 0o600); err != nil {
		t.Fatal(err)
	}

	err := Run(context.Background(), testOptions(t, home, startTestHelper(t)))
	if err == nil {
		t.Fatal("Run replaced a non-socket file")
	}
	data, readErr := os.ReadFile(SocketPath(home))
	if readErr != nil {
		t.Fatalf("non-socket file was removed: %v", readErr)
	}
	if !bytes.Equal(data, sentinel) {
		t.Fatalf("non-socket file changed: %q", data)
	}
}

func TestDaemonRestartKeepsDesiredStateRunning(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("blog hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := startDaemon(t, opts)
	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	site := createStaticSite(t, client, "blog", root)
	if site.Domain != "blog.test" {
		t.Fatalf("domain = %q, want blog.test", site.Domain)
	}
	if body := fetchSiteBody(t, site.HTTPPort); body != "blog hello" {
		t.Fatalf("site body = %q", body)
	}
	run.stop(t)

	stored := readSiteFromStore(t, home)
	if stored.DesiredState != model.DesiredRunning {
		t.Fatalf("desired state after shutdown = %q, want running", stored.DesiredState)
	}
	waitTCPClosed(t, stored.HTTPPort)

	second := startDaemon(t, opts)
	defer second.stop(t)
	client = unixHTTPClient(SocketPath(home), 5*time.Second)
	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/sites/"+stored.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get site after restart = %d %s", resp.StatusCode, data)
	}
	var restarted model.Site
	if err := json.Unmarshal(data, &restarted); err != nil {
		t.Fatal(err)
	}
	if restarted.Status != model.StatusRunning {
		t.Fatalf("status after restart = %q, want running", restarted.Status)
	}
	if body := fetchSiteBody(t, restarted.HTTPPort); body != "blog hello" {
		t.Fatalf("site body after restart = %q", body)
	}
}

func TestDaemonGracefulShutdownStopsBackendsWithoutChangingDesiredState(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("stop me"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := startDaemon(t, opts)
	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	site := createStaticSite(t, client, "graceful", root)
	if body := fetchSiteBody(t, site.HTTPPort); body != "stop me" {
		t.Fatalf("site body = %q", body)
	}
	run.stop(t)

	stored := readSiteFromStore(t, home)
	if stored.DesiredState != model.DesiredRunning {
		t.Fatalf("desired state after graceful stop = %q, want running", stored.DesiredState)
	}
	waitTCPClosed(t, stored.HTTPPort)
}

func TestEnsureRunningStartsDaemonAndReturnsWhenRunning(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))
	ctx := context.Background()

	if err := EnsureRunning(ctx, opts); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	pid := readPIDFile(t, PidPath(home))
	t.Cleanup(func() { terminateAndWait(t, pid) })
	waitForSocket(t, SocketPath(home), nil)
	getHealth(t, unixHTTPClient(SocketPath(home), 5*time.Second))

	started := time.Now()
	if err := EnsureRunning(ctx, opts); err != nil {
		t.Fatalf("second EnsureRunning: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("EnsureRunning on a running daemon took %s", elapsed)
	}
	if data, err := os.ReadFile(filepath.Join(home, "run", "daemon.log")); err != nil {
		t.Fatalf("daemon log: %v", err)
	} else if info, statErr := os.Stat(filepath.Join(home, "run", "daemon.log")); statErr != nil {
		t.Fatalf("stat daemon log: %v", statErr)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("daemon log permissions = %04o, want 0600", info.Mode().Perm())
	} else if len(data) == 0 {
		// An empty log is valid, but the file must exist with the right mode.
		t.Log("daemon log is empty")
	}
}

func TestEnsureRunningReportsFailureLogTail(t *testing.T) {
	home := t.TempDir()
	script := filepath.Join(t.TempDir(), "fake-devstack")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'boom: cannot start' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := selfExecutable
	selfExecutable = func() (string, error) { return script, nil }
	t.Cleanup(func() { selfExecutable = previous })

	previousTimeout := ensureStartTimeout
	ensureStartTimeout = time.Second
	t.Cleanup(func() { ensureStartTimeout = previousTimeout })

	err := EnsureRunning(context.Background(), Options{Home: home, HTTPPort: freePort(t), HTTPSPort: freePort(t), UIPort: freePort(t)})
	if err == nil {
		t.Fatal("EnsureRunning succeeded with a failing daemon")
	}
	if !strings.Contains(err.Error(), "boom: cannot start") {
		t.Fatalf("error does not include the daemon log tail: %v", err)
	}
}

func readPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr != nil || pid <= 0 {
				t.Fatalf("invalid pid file %q: %q", path, data)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s was not written: %v", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func terminateAndWait(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal daemon: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("daemon process %d did not exit", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
