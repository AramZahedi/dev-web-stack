//go:build !windows

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
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

var (
	testBinaryOnce sync.Once
	testBinaryDir  string
	testBinaryPath string
	testBinaryErr  error

	allocatedPortsMu sync.Mutex
	allocatedPorts   = make(map[int]bool)
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
	waitForReady(t, ReadyPath(opts.Home), done)
	return run
}

// waitForReady blocks until the daemon has finished publishing routes and
// written its readiness marker. The control socket appears before routes are
// published, so shutdown tests must not cancel the daemon merely because the
// socket accepts connections; under the race detector that window is wide
// enough to make startup fail with context cancellation.
func waitForReady(t *testing.T, ready string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("daemon exited before readiness: %v", err)
		default:
		}
		if _, err := os.Stat(ready); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon readiness marker %s did not appear", ready)
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	for {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
		// Never hand out the backend upstream/FastCGI ranges, and never hand
		// out the same port twice: tests configure HTTP and HTTPS listeners
		// from separate calls, and a duplicate makes the daemon reject its
		// configuration.
		if port >= 41000 && port <= 43999 {
			continue
		}
		allocatedPortsMu.Lock()
		if allocatedPorts[port] {
			allocatedPortsMu.Unlock()
			continue
		}
		allocatedPorts[port] = true
		allocatedPortsMu.Unlock()
		return port
	}
}

func TestFreePortOutsideBackendRange(t *testing.T) {
	for i := 0; i < 50; i++ {
		port := freePort(t)
		if port >= 41000 && port <= 43999 {
			t.Fatalf("freePort returned reserved backend port %d", port)
		}
	}
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

func startProxyTargetTestHelper(t *testing.T, dir, configPath string, proxyHTTP, proxyHTTPS int) helper.Config {
	t.Helper()
	hosts := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := helper.Config{
		SocketPath:       filepath.Join(dir, "run", "helper.sock"),
		HostsPath:        hosts,
		AllowUID:         os.Getuid(),
		CADir:            filepath.Join(dir, "pki"),
		TrustDir:         filepath.Join(dir, "trust"),
		ForwardHTTPPort:  freePort(t),
		ForwardHTTPSPort: freePort(t),
		ProxyHTTPPort:    proxyHTTP,
		ProxyHTTPSPort:   proxyHTTPS,
		ProxyConfigPath:  configPath,
	}
	server, err := helper.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := server.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
	})
	return cfg
}

func assertDaemonMovedHelperTargets(t *testing.T, helperCfg helper.Config) Options {
	t.Helper()
	opts := testOptions(t, t.TempDir(), helperCfg.SocketPath)
	run := startDaemon(t, opts)
	defer run.stop(t)

	client, err := helperclient.Dial(helperCfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	// The helper rejects targets that do not match its configured values, so a
	// successful no-op disable proves the daemon updated the in-memory targets.
	if err := client.SetForwarders(context.Background(), opts.HTTPPort, opts.HTTPSPort, false); err != nil {
		t.Fatalf("SetForwarders with daemon ports: %v", err)
	}
	st, err := store.Open(filepath.Join(opts.Home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	reconcileError, err := st.GetSetting(context.Background(), "serving_mode_error")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if reconcileError != "" {
		t.Fatalf("serving-mode reconciliation failed: %s", reconcileError)
	}
	return opts
}

func TestDaemonUpdatesHelperProxyTargetsOnStartup(t *testing.T) {
	useBuiltBinary(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "helper.yaml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`
proxy_http_port: %d
proxy_https_port: %d
`, freePort(t), freePort(t))), 0o640); err != nil {
		t.Fatal(err)
	}
	helperCfg := startProxyTargetTestHelper(t, dir, configPath, freePort(t), freePort(t))
	opts := assertDaemonMovedHelperTargets(t, helperCfg)

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		fmt.Sprintf("proxy_http_port: %d", opts.HTTPPort),
		fmt.Sprintf("proxy_https_port: %d", opts.HTTPSPort),
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("helper config missing %q:\n%s", want, data)
		}
	}
}

func TestDaemonMovesHelperTargetsWhenConfigWriteFails(t *testing.T) {
	useBuiltBinary(t)
	dir := t.TempDir()
	helperCfg := startProxyTargetTestHelper(t, dir, filepath.Join(dir, "missing.yaml"), freePort(t), freePort(t))
	// The daemon logs the persistence error but must still move the runtime
	// targets so serving-mode reconciliation restarts 80/443 against the new
	// daemon ports.
	assertDaemonMovedHelperTargets(t, helperCfg)
}

func TestDaemonPHPAPIIsWired(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))
	run := startDaemon(t, opts)
	defer run.stop(t)

	client := unixHTTPClient(SocketPath(home), 5*time.Second)

	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/php/extensions?version=8.4", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("extensions = %d %s", resp.StatusCode, data)
	}
	var extensions []catalog.Extension
	if err := json.Unmarshal(data, &extensions); err != nil || len(extensions) == 0 {
		t.Fatalf("extensions = %d %v", len(extensions), err)
	}

	resp, data = apiRequest(t, client, http.MethodGet, "/api/v1/php/defaults/8.4", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("defaults = %d %s", resp.StatusCode, data)
	}
	var defaults model.PHPDefaults
	if err := json.Unmarshal(data, &defaults); err != nil || defaults.Version != "8.4" || len(defaults.Ini) == 0 {
		t.Fatalf("defaults = %+v %v", defaults, err)
	}

	// DELETE is idempotent per the OpenAPI contract: an already-absent build is
	// not in use and removes as a no-op.
	resp, data = apiRequest(t, client, http.MethodDelete, "/api/v1/runtimes/php@8.4.4-r1", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove uninstalled runtime = %d %s", resp.StatusCode, data)
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
	if err := os.WriteFile(ReadyPath(home), []byte("999\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Run(context.Background(), testOptions(t, home, startTestHelper(t)))
	if err == nil {
		t.Fatal("Run replaced a non-socket file")
	}
	if _, err := os.Stat(ReadyPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed startup left a readiness marker: %v", err)
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

func TestUserUnitCompatibility(t *testing.T) {
	if !userUnitCompatible(Options{}) {
		t.Fatal("default options should use the user unit")
	}
	if !userUnitCompatible(Options{HelperSocket: DefaultHelperSocket, LogLevel: "info"}) {
		t.Fatal("explicit default socket/info level should use the user unit")
	}
	for name, opts := range map[string]Options{
		"custom helper socket": {HelperSocket: "/tmp/helper.sock"},
		"hosts override":       {HostsPath: "/tmp/hosts"},
		"http override":        {HTTPPort: 9080},
		"https override":       {HTTPSPort: 9443},
		"ui override":          {UIPort: 9070},
		"debug log":            {LogLevel: "debug"},
	} {
		if userUnitCompatible(opts) {
			t.Fatalf("%s should use the detached fallback", name)
		}
	}
}

func TestEnsureRunningStartsUserUnit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	devHome := filepath.Join(home, ".devstack")
	if _, err := config.Load(devHome); err != nil {
		t.Fatal(err)
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", "devstack-daemon.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate the daemon that systemd would have started: hold the lifetime
	// lock, listen on the control socket, and publish a live readiness marker.
	lock, err := acquireLifetimeLock(devHome)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	listener, err := net.Listen("unix", SocketPath(devHome))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	old := systemctlUser
	t.Cleanup(func() { systemctlUser = old })
	started := false
	systemctlUser = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "show-environment" {
			return nil, nil
		}
		if len(args) == 2 && args[0] == "start" && args[1] == "devstack-daemon.service" {
			started = true
			return nil, os.WriteFile(ReadyPath(devHome), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
		}
		return nil, errors.New("unexpected systemctl args")
	}

	if err := EnsureRunning(context.Background(), Options{Home: devHome}); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if !started {
		t.Fatal("EnsureRunning did not start the installed user unit")
	}
}

func TestStartUserDaemonUnitStartsInstalledUnit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	unitPath := filepath.Join(home, ".config", "systemd", "user", "devstack-daemon.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := systemctlUser
	t.Cleanup(func() { systemctlUser = old })
	var calls []string
	systemctlUser = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}

	started, err := startUserDaemonUnit(context.Background(), filepath.Join(home, ".devstack"))
	if err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("startUserDaemonUnit did not use the installed unit")
	}
	want := []string{"show-environment", "start devstack-daemon.service"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("systemctl calls = %v, want %v", calls, want)
	}
}

func TestStartUserDaemonUnitHonorsXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	configHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	unitPath := filepath.Join(configHome, "systemd", "user", "devstack-daemon.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := systemctlUser
	t.Cleanup(func() { systemctlUser = old })
	var started bool
	systemctlUser = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 2 && args[0] == "start" {
			started = true
		}
		return nil, nil
	}

	gotPath, err := userDaemonUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != unitPath {
		t.Fatalf("unit path = %q, want %q", gotPath, unitPath)
	}
	used, err := startUserDaemonUnit(context.Background(), filepath.Join(home, ".devstack"))
	if err != nil {
		t.Fatal(err)
	}
	if !used || !started {
		t.Fatal("XDG_CONFIG_HOME unit was not started")
	}
}

func TestStartUserDaemonUnitFallbackCases(t *testing.T) {
	t.Run("no unit", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		old := systemctlUser
		t.Cleanup(func() { systemctlUser = old })
		systemctlUser = func(_ context.Context, _ ...string) ([]byte, error) {
			t.Fatal("systemctl called without an installed unit")
			return nil, nil
		}
		started, err := startUserDaemonUnit(context.Background(), filepath.Join(home, ".devstack"))
		if err != nil || started {
			t.Fatalf("started = %v, err = %v; want fallback", started, err)
		}
	})

	t.Run("custom home", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		unitPath := filepath.Join(home, ".config", "systemd", "user", "devstack-daemon.service")
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unitPath, []byte("[Service]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := systemctlUser
		t.Cleanup(func() { systemctlUser = old })
		systemctlUser = func(_ context.Context, _ ...string) ([]byte, error) {
			t.Fatal("systemctl called for a custom home")
			return nil, nil
		}
		started, err := startUserDaemonUnit(context.Background(), filepath.Join(home, "custom"))
		if err != nil || started {
			t.Fatalf("started = %v, err = %v; want fallback", started, err)
		}
	})

	t.Run("no user manager", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		unitPath := filepath.Join(home, ".config", "systemd", "user", "devstack-daemon.service")
		if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unitPath, []byte("[Service]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := systemctlUser
		t.Cleanup(func() { systemctlUser = old })
		systemctlUser = func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) != 1 || args[0] != "show-environment" {
				t.Fatalf("unexpected systemctl call %v", args)
			}
			return nil, errors.New("no user manager")
		}
		started, err := startUserDaemonUnit(context.Background(), filepath.Join(home, ".devstack"))
		if err != nil || started {
			t.Fatalf("started = %v, err = %v; want fallback", started, err)
		}
	})
}

func TestStartUserDaemonUnitReportsStartFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	unitPath := filepath.Join(home, ".config", "systemd", "user", "devstack-daemon.service")
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitPath, []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	old := systemctlUser
	t.Cleanup(func() { systemctlUser = old })
	systemctlUser = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "show-environment" {
			return nil, nil
		}
		return []byte("Job for devstack-daemon.service failed"), errors.New("exit status 1")
	}

	started, err := startUserDaemonUnit(context.Background(), filepath.Join(home, ".devstack"))
	if err == nil {
		t.Fatal("startUserDaemonUnit ignored a systemctl start failure")
	}
	if started {
		t.Fatal("started = true after systemctl start failure")
	}
	for _, want := range []string{"devstack-daemon.service", "Job for devstack-daemon.service failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
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
	if info, err := os.Stat(PidPath(home)); err != nil {
		t.Fatalf("stat pid file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("pid file permissions = %04o, want 0600", info.Mode().Perm())
	}
	if info, err := os.Stat(LockPath(home)); err != nil {
		t.Fatalf("stat lifetime lock: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("lifetime lock permissions = %04o, want 0600", info.Mode().Perm())
	}
	waitForSocket(t, SocketPath(home), nil)
	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, client)

	// EnsureRunning must not return before the ready marker, pid file, and
	// store-backed settings are all available.
	ready, err := os.ReadFile(ReadyPath(home))
	if err != nil {
		t.Fatalf("readiness marker: %v", err)
	}
	if got, err := strconv.Atoi(strings.TrimSpace(string(ready))); err != nil || got != pid {
		t.Fatalf("readiness marker = %q, want pid %d", ready, pid)
	}
	if info, err := os.Stat(ReadyPath(home)); err != nil {
		t.Fatalf("stat readiness marker: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("readiness marker permissions = %04o, want 0600", info.Mode().Perm())
	}
	if info, err := os.Stat(filepath.Join(home, "run")); err != nil {
		t.Fatalf("stat run directory: %v", err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("run directory permissions = %04o, want 0700", info.Mode().Perm())
	}
	resp, data := apiRequest(t, client, http.MethodGet, "/api/v1/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings after EnsureRunning = %d %s", resp.StatusCode, data)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["home"] != home {
		t.Fatalf("settings home = %v, want %s", settings["home"], home)
	}
	if settings["restart_required"] != false {
		t.Fatalf("settings restart_required = %v, want false after startup", settings["restart_required"])
	}

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

func TestRunFailsFastWhenLifetimeLockIsHeld(t *testing.T) {
	home := t.TempDir()
	if err := prepareRunDir(home); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireLifetimeLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	err = Run(context.Background(), Options{Home: home, LogLevel: "error"})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Run error = %v, want ErrAlreadyRunning", err)
	}
}

func TestEnsureRunningConcurrentCallsStartOneDaemon(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))

	const callers = 4
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs[index] = EnsureRunning(context.Background(), opts)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("EnsureRunning caller %d: %v", i, err)
		}
	}
	pid := readPIDFile(t, PidPath(home))
	t.Cleanup(func() { terminateAndWait(t, pid) })
	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, client)
	ready, err := os.ReadFile(ReadyPath(home))
	if err != nil {
		t.Fatalf("readiness marker: %v", err)
	}
	if got, err := strconv.Atoi(strings.TrimSpace(string(ready))); err != nil || got != pid {
		t.Fatalf("readiness marker = %q, want pid %d", ready, pid)
	}

	// The daemon owns the lock, so a direct in-process Run must fail fast
	// rather than binding a second socket or a second set of listeners.
	if err := Run(context.Background(), opts); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run error = %v, want ErrAlreadyRunning", err)
	}
}

func TestShutdownOverlapKeepsReplacementDaemonResources(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	useBuiltBinary(t)
	home := t.TempDir()
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	// A short grace keeps the overlap test fast while still forcing shutdown to
	// wait on a process that ignores SIGTERM.
	cfg.GracefulStopSeconds = 1
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(t, home, startTestHelper(t))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := seedNodeSite(t, home, "overlap", root, freePort(t), `trap '' TERM; exec python3 -m http.server "$PORT"`)

	first := startDaemon(t, opts)
	waitDaemonReady(t, home)
	oldPID := readPIDFile(t, PidPath(home))
	if body := fetchSiteBody(t, site.HTTPPort); !strings.Contains(body, "replacement") {
		t.Fatalf("seed site body = %q", body)
	}

	// Begin the old daemon's shutdown and wait until it has retired readiness
	// but is still stopping the TERM-ignoring backend. EnsureRunning must wait
	// on the lifetime lock, then spawn a replacement without the old instance
	// deleting its socket or pid file on the way out.
	first.cancel()
	first.cancel = nil
	oldDone := first.done
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ReadyPath(home)); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old daemon did not retire its readiness marker")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ensureDone := make(chan error, 1)
	go func() { ensureDone <- EnsureRunning(context.Background(), opts) }()

	select {
	case err := <-ensureDone:
		if err != nil {
			t.Fatalf("EnsureRunning during shutdown: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("EnsureRunning did not return during daemon shutdown overlap")
	}
	select {
	case err := <-oldDone:
		if err != nil {
			t.Fatalf("old daemon shutdown: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("old daemon did not finish shutting down")
	}

	newPID := readPIDFile(t, PidPath(home))
	if newPID == oldPID {
		t.Fatalf("replacement daemon reused pid %d", newPID)
	}
	t.Cleanup(func() { terminateAndWait(t, newPID) })

	if info, err := os.Stat(SocketPath(home)); err != nil {
		t.Fatalf("replacement socket was removed: %v", err)
	} else if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("replacement socket is not a socket: %v", info.Mode())
	}
	if data, err := os.ReadFile(ReadyPath(home)); err != nil {
		t.Fatalf("replacement readiness marker: %v", err)
	} else if got, err := strconv.Atoi(strings.TrimSpace(string(data))); err != nil || got != newPID {
		t.Fatalf("replacement readiness marker = %q, want pid %d", data, newPID)
	}
	replacementClient := unixHTTPClient(SocketPath(home), 5*time.Second)
	getHealth(t, replacementClient)

	resp, data := apiRequest(t, replacementClient, http.MethodGet, "/api/v1/sites/"+site.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replacement site lookup = %d %s", resp.StatusCode, data)
	}
	var restarted model.Site
	if err := json.Unmarshal(data, &restarted); err != nil {
		t.Fatal(err)
	}
	if restarted.Status != model.StatusRunning {
		t.Fatalf("replacement site status = %q, want running", restarted.Status)
	}
	if body := fetchSiteBody(t, restarted.HTTPPort); body != "replacement" {
		t.Fatalf("replacement site body = %q", body)
	}
}

func TestDaemonShutdownStopsDesiredStoppedBackend(t *testing.T) {
	useBuiltBinary(t)
	home := t.TempDir()
	opts := testOptions(t, home, startTestHelper(t))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("still running"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := startDaemon(t, opts)
	client := unixHTTPClient(SocketPath(home), 5*time.Second)
	site := createStaticSite(t, client, "inconsistent", root)
	if body := fetchSiteBody(t, site.HTTPPort); body != "still running" {
		t.Fatalf("site body = %q", body)
	}

	// Simulate a row that disagrees with the live supervisor. The daemon must
	// still stop the process, without changing the persisted desired state.
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetSite(context.Background(), site.ID)
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	stored.DesiredState = model.DesiredStopped
	stored.Status = model.StatusStopped
	if err := st.UpdateSite(context.Background(), stored); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	run.stop(t)
	waitTCPClosed(t, site.HTTPPort)

	after := readSiteFromStore(t, home)
	if after.DesiredState != model.DesiredStopped {
		t.Fatalf("desired state = %q, want stopped (shutdown must not rewrite it)", after.DesiredState)
	}
}

func TestRemoveOwnedSocketKeepsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedSocket(path, recorded); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement resource was removed: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	owned, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	owned.SetUnlinkOnClose(false)
	current, err := os.Lstat(path)
	if err != nil {
		_ = owned.Close()
		t.Fatal(err)
	}
	_ = owned.Close()
	if err := removeOwnedSocket(path, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned resource still exists: %v", err)
	}
}

func TestRemoveOwnedPIDKeepsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.pid")
	if err := os.WriteFile(path, []byte("111\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedPID(path, "222"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign pid file was removed: %v", err)
	}
	if err := removeOwnedPID(path, "111"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned pid file still exists: %v", err)
	}
}

func TestRemovableSocketError(t *testing.T) {
	if !removableSocketError(syscall.ECONNREFUSED) {
		t.Fatal("ECONNREFUSED should be removable")
	}
	if !removableSocketError(syscall.ENOENT) {
		t.Fatal("ENOENT should be removable")
	}
	if removableSocketError(syscall.ETIMEDOUT) {
		t.Fatal("ETIMEDOUT must not be removable")
	}
	if removableSocketError(syscall.EACCES) {
		t.Fatal("EACCES must not be removable")
	}
}

func TestPrepareSocketPathRemovesStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	_ = listener.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket was not left behind: %v", err)
	}
	if err := prepareSocketPath(path); err != nil {
		t.Fatalf("prepareSocketPath: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket still exists: %v", err)
	}
}

func TestPrepareSocketPathRefusesLiveSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if err := prepareSocketPath(path); err == nil {
		t.Fatal("prepareSocketPath removed a live socket")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
}

// seedNodeSite inserts a desired-running Node row before the daemon starts.
// It avoids the site service's runtime install path while still exercising a
// real supervised process on a real port.
func seedNodeSite(t *testing.T, home, name, root string, port int, command string) model.Site {
	t.Helper()
	paths := config.NewPaths(home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	site := model.Site{
		ID:           "seed-" + name,
		Name:         name,
		Domain:       name + ".test",
		Backend:      model.BackendLocal,
		HostPath:     root,
		BackendPath:  root,
		Kind:         model.SiteNode,
		Command:      command,
		DesiredState: model.DesiredRunning,
		Status:       model.StatusStopped,
		HTTPPort:     port,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := st.CreateSite(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	return site
}

func waitDaemonReady(t *testing.T, home string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		data, err := os.ReadFile(ReadyPath(home))
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && processAlive(pid) && socketReachable(SocketPath(home)) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never became ready: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
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

func TestSpawnLockSerializesCallers(t *testing.T) {
	home := t.TempDir()
	if err := prepareRunDir(home); err != nil {
		t.Fatal(err)
	}
	first, err := acquireSpawnLock(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireSpawnLock(home); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second spawn lock error = %v, want ErrAlreadyRunning", err)
	}
	first.release()
	second, err := acquireSpawnLock(home)
	if err != nil {
		t.Fatalf("spawn lock after release: %v", err)
	}
	second.release()
}
