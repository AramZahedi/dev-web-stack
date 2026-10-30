package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestListenSitePortRetriesTransientBind(t *testing.T) {
	blocked, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := blocked.Addr().(*net.TCPAddr).Port
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = blocked.Close()
	}()

	runtime, _ := newTestRuntimeOnly(t)
	listener, err := runtime.listenSitePort(context.Background(), port)
	if err != nil {
		t.Fatalf("listenSitePort did not retry a transient bind: %v", err)
	}
	_ = listener.Close()
}

func TestStaticRuntimePrepareStartStopAndServe(t *testing.T) {
	runtime, root, spec := newStaticTestRuntime(t)
	prepared, err := runtime.PrepareSite(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.BackendHost != "127.0.0.1" || prepared.HTTPPort < 41000 || prepared.HTTPPort > 42999 {
		t.Fatalf("prepared = %+v", prepared)
	}
	if _, err := runtime.StartSite(context.Background(), spec.ID); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://127.0.0.1:" + strconv.Itoa(prepared.HTTPPort) + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "hello static\n" {
		t.Fatalf("static response = %d %q", response.StatusCode, body)
	}
	if status, err := runtime.SiteStatus(spec.ID); err != nil || status.Status != "running" {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if err := runtime.StopSite(context.Background(), spec.ID, false); err != nil {
		t.Fatal(err)
	}
	if status, err := runtime.SiteStatus(spec.ID); err != nil || status.Status != "stopped" {
		t.Fatalf("stopped status = %+v, %v", status, err)
	}
	if root == "" {
		t.Fatal("test runtime root is empty")
	}
}

func TestStopSiteWithExpiredContextClearsTheServerAndRestarts(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	prepared, err := runtime.PrepareSite(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartSite(context.Background(), spec.ID); err != nil {
		t.Fatal(err)
	}
	address := "127.0.0.1:" + strconv.Itoa(prepared.HTTPPort)
	// An in-flight (partial) request makes Shutdown wait for the context
	// instead of returning immediately.
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: devstack.test\r\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.StopSite(ctx, spec.ID, false); err == nil {
		t.Fatal("StopSite reported success while a request was still in flight")
	}

	runtime.mu.Lock()
	server := runtime.sites[spec.ID].server
	runtime.mu.Unlock()
	if server != nil {
		t.Fatal("a failed shutdown left a phantom server that blocks a real restart")
	}
	if _, err := runtime.StartSite(context.Background(), spec.ID); err != nil {
		t.Fatalf("restart after a failed shutdown: %v", err)
	}
	response, err := http.Get("http://" + address + "/")
	if err != nil {
		t.Fatalf("restarted site is not listening: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restarted site status = %d", response.StatusCode)
	}
	if err := runtime.StopSite(context.Background(), spec.ID, false); err != nil {
		t.Fatal(err)
	}
}

func TestStaticRuntimeRejectsEscapingAndSymlinkDocumentRoots(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	for _, documentRoot := range []string{"../outside", filepath.Join(filepath.Dir(spec.BackendPath), "outside")} {
		spec.DocumentRoot = documentRoot
		if _, err := runtime.PrepareSite(context.Background(), spec); err == nil {
			t.Errorf("PrepareSite accepted escaping document root %q", documentRoot)
		}
	}
	link := filepath.Join(spec.BackendPath, "linked")
	if err := os.Symlink(filepath.Join(spec.BackendPath, "public"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	spec.DocumentRoot = "linked"
	if _, err := runtime.PrepareSite(context.Background(), spec); err == nil {
		t.Fatal("PrepareSite accepted a symlink document root")
	}
}

func TestAgentPortAllocatorValidatesAllocatesReusesAndReleases(t *testing.T) {
	ports := NewPortAllocator()
	first, reused, err := ports.Allocate("site-a", "http", 0)
	if err != nil || reused {
		t.Fatalf("first allocate = %d reused=%v err=%v", first, reused, err)
	}
	second, reused, err := ports.Allocate("site-a", "http", 0)
	if err != nil || !reused || second != first {
		t.Fatalf("reused allocate = %d reused=%v err=%v", second, reused, err)
	}
	if valid, err := ports.Validate("site-a", "http", first); err != nil || !valid {
		t.Fatalf("Validate(owned) = %v, %v", valid, err)
	}
	if _, _, err := ports.Allocate("site-b", "http", first); err == nil {
		t.Fatal("allocator handed one port to two sites")
	}
	if err := ports.Release("site-a", first); err != nil {
		t.Fatal(err)
	}
	if valid, err := ports.Validate("site-a", "http", first); err != nil || !valid {
		t.Fatalf("Validate(released) = %v, %v; released port should be available", valid, err)
	}
	if got, reused, err := ports.Allocate("site-b", "http", first); err != nil || reused || got != first {
		t.Fatalf("reallocate released port = %d reused=%v err=%v", got, reused, err)
	}
}

func TestAgentPortAllocatorNormalizesKindBeforeOwnership(t *testing.T) {
	ports := NewPortAllocator()
	first, reused, err := ports.Allocate("site-case", "HTTP", 0)
	if err != nil || reused {
		t.Fatalf("first allocate = %d reused=%v err=%v", first, reused, err)
	}
	second, reused, err := ports.Allocate("site-case", "http", 0)
	if err != nil || !reused || second != first {
		t.Fatalf("normalized allocate = %d reused=%v err=%v", second, reused, err)
	}
}

func TestStaticRuntimeStreamsLifecycleLogs(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartSite(context.Background(), spec.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	stream, err := runtime.SiteLogs(ctx, spec.ID, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stream)
	started, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(started, "started") {
		t.Fatalf("initial log = %q, %v", started, err)
	}
	if err := runtime.StopSite(context.Background(), spec.ID, false); err != nil {
		t.Fatal(err)
	}
	stopped, err := reader.ReadString('\n')
	cancel()
	_ = stream.Close()
	if err != nil || !strings.Contains(stopped, "stopped") {
		t.Fatalf("stopped log = %q, %v", stopped, err)
	}
}

func TestStaticRuntimeReconcilesPreviouslyRunningSiteAfterRestart(t *testing.T) {
	runtime, root, spec := newStaticTestRuntime(t)
	prepared, err := runtime.PrepareSite(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartSite(context.Background(), spec.ID); err != nil {
		t.Fatal(err)
	}
	// Model a process crash: stop the listener without clearing the persisted
	// desired-running record, then let a new runtime reconcile it.
	runtime.mu.Lock()
	if err := runtime.sites[spec.ID].server.Close(); err != nil {
		runtime.mu.Unlock()
		t.Fatal(err)
	}
	runtime.sites[spec.ID].server = nil
	runtime.mu.Unlock()

	restarted, err := NewRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	logsRc, _ := restarted.SiteLogs(context.Background(), spec.ID, false, 20)
	logsBytes, _ := io.ReadAll(logsRc)
	_ = logsRc.Close()
	t.Logf("site logs: %s", string(logsBytes))
	status, err := restarted.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" || status.HTTPPort != prepared.HTTPPort {
		t.Fatalf("reconciled status = %+v, %v; wanted port %d", status, err, prepared.HTTPPort)
	}
	_ = restarted.StopSite(context.Background(), spec.ID, false)
}

func TestPHPRuntimeReconcilesPreviouslyRunningSiteAfterRestart(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	spec := agentproto.SiteSpec{
		ID:          "reconcile-php",
		Kind:        "php",
		BackendPath: root,
		FastCGIPort: 43050,
		PHPVersion:  "8.4",
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "default", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "default"}); err != nil {
		t.Fatal(err)
	}

	// Model process restart: stop previous listener without clearing desired-running state.
	runtime.mu.Lock()
	if gen := runtime.sites[spec.ID].phpGenerations["default"]; gen != nil && gen.process != nil {
		gen.process.stop()
	}
	runtime.mu.Unlock()

	restarted, err := NewRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	logsRc, _ := restarted.SiteLogs(context.Background(), spec.ID, false, 20)
	logsBytes, _ := io.ReadAll(logsRc)
	_ = logsRc.Close()
	t.Logf("site logs: %s", string(logsBytes))
	status, err := restarted.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" {
		t.Fatalf("reconciled php status = %+v, %v", status, err)
	}
	_, _ = restarted.StopPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "default"})
}

func TestAgentRuntimeRoutesAreBehindAuth(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	identity, err := GenerateTLSIdentity(filepath.Join(t.TempDir(), "tls"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithRuntime(Config{Root: t.TempDir(), Distro: "devstack"}, "route-secret", identity, nil, runtime)
	if err != nil {
		t.Fatal(err)
	}
	requestBody := `{"site":{"id":"` + spec.ID + `","kind":"static","backend_path":"` + strings.ReplaceAll(spec.BackendPath, `\`, `\\`) + `","document_root":"public"}}`
	request := httptest.NewRequest(http.MethodPost, agentproto.PathSitePrepareFor(spec.ID), strings.NewReader(requestBody))
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"route-secret")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("prepare status = %d: %s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, agentproto.PathSiteStatusFor(spec.ID), nil)
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status request = %d", recorder.Code)
	}
}

func newStaticTestRuntime(t *testing.T) (*Runtime, string, agentproto.SiteSpec) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	docroot := filepath.Join(project, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.html"), []byte("hello static\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "agent-root")
	runtime, err := NewRuntime(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, runtimeRoot, agentproto.SiteSpec{ID: "static-site", Kind: "static", BackendPath: project, DocumentRoot: "public"}
}

func TestStaticRuntimeHonorsCanceledPrepareAndUnknownLogs(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtime.PrepareSite(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareSite error = %v, want context.Canceled", err)
	}
	if _, err := runtime.SiteLogs(context.Background(), "missing", false, 10); err == nil {
		t.Fatal("SiteLogs accepted unknown site")
	}
}

func TestStaticRuntimeReconcileHasBoundedStart(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeReleasePortsForgetsPreparedSite(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	prepared, err := runtime.PrepareSite(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReleasePorts(spec.ID, prepared.HTTPPort); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SiteStatus(spec.ID); !errors.Is(err, ErrSiteNotFound) {
		t.Fatalf("SiteStatus after release = %v, want ErrSiteNotFound", err)
	}
	if _, err := NewRuntime(runtime.root); err != nil {
		t.Fatalf("released site remained in persisted runtime state: %v", err)
	}
}

func TestRuntimeReleasePortsForgetsPHPPreparedSite(t *testing.T) {
	runtime, root, _ := newStaticTestRuntime(t)
	if err := os.MkdirAll(filepath.Join(root, "project", "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:           "testphprelease",
		Kind:         "php",
		HostPath:     filepath.Join(root, "project"),
		BackendPath:  filepath.Join(root, "project"),
		DocumentRoot: "public",
		PHPVersion:   "8.4",
		PHPBuild:     "php@8.4.27-r1",
	}
	prepared, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{
		Generation: "gen-1",
		Site:       spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReleasePorts(spec.ID, prepared.FastCGIPort); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.SiteStatus(spec.ID); !errors.Is(err, ErrSiteNotFound) {
		t.Fatalf("SiteStatus after release = %v, want ErrSiteNotFound", err)
	}
	if _, err := NewRuntime(runtime.root); err != nil {
		t.Fatalf("released PHP site remained in persisted runtime state: %v", err)
	}
}

func TestStaticRuntimeInjectsInstalledCABundleIntoSiteEnvironment(t *testing.T) {
	runtime, _, spec := newStaticTestRuntime(t)
	caPath, err := InstallCABundle(runtime.root, testCABundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	prepared := runtime.sites[spec.ID].spec.Env
	if prepared["SSL_CERT_FILE"] != caPath || prepared["CURL_CA_BUNDLE"] != caPath || prepared["NODE_EXTRA_CA_CERTS"] != caPath || !strings.Contains(prepared["PHP_INI_SCAN_DIR"], "php-conf.d") {
		t.Fatalf("site CA environment = %#v", prepared)
	}
}

func newTestRuntimeOnly(t *testing.T) (*Runtime, string) {
	t.Helper()
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "agent-root")
	runtime, err := NewRuntime(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, runtimeRoot
}

func TestAgentInstallRuntimeCatalogValidationAndListing(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	// Stage a runnable build so the test never downloads the real 242 MB PHP
	// archive. The compatibility gate executes both binaries on Linux; a
	// trivial script exits 0.
	buildDir := filepath.Join(root, "runtimes", "php@8.4.27-r1")
	for _, binary := range []string{"bin/php", "sbin/php-fpm"} {
		path := filepath.Join(buildDir, filepath.FromSlash(binary))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := runtime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "php@8.4.27-r1"})
	if err != nil {
		t.Fatalf("InstallRuntime(php@8.4.27-r1) = %v", err)
	}
	if resp.ID != "php@8.4.27-r1" || resp.Kind != "php" {
		t.Fatalf("unexpected runtime response: %+v", resp)
	}
	if _, err := runtime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "php@9.9.9"}); err == nil {
		t.Fatal("InstallRuntime accepted uncataloged runtime")
	}
	list, err := runtime.ListRuntimes(context.Background())
	if err != nil || len(list.Runtimes) == 0 {
		t.Fatalf("ListRuntimes = %+v, %v", list, err)
	}
	if err := runtime.RemoveRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "php@8.4.27-r1"}); err != nil {
		t.Fatalf("RemoveRuntime = %v", err)
	}
}

func TestAgentInstallRuntimeFailsClosedWhenTheBuildIsMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runtime downloads and binary checks are disabled on native Windows")
	}
	agentRuntime, _ := newTestRuntimeOnly(t)
	t.Setenv("DEVSTACK_NO_DOWNLOAD", "1")
	if _, err := agentRuntime.InstallRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "node@24.22.1"}); err == nil {
		t.Fatal("InstallRuntime registered a runtime that was never installed")
	}
}

func TestRemoveRuntimeUsesRegisteredPathAndRejectsTraversal(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)

	victim := filepath.Join(root, "audit-victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runtime.RemoveRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "../audit-victim"}); err != nil {
		t.Fatalf("unknown runtime removal should be idempotent, got %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("traversal build id deleted a path outside the runtime root: %v", err)
	}

	const id = "php@8.4.27-r1"
	registered := filepath.Join(root, "runtimes", id)
	if err := os.MkdirAll(registered, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(registered, "php"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.runtimes[id] = agentproto.RuntimeResponse{ID: id, Path: registered}
	runtime.mu.Unlock()
	if err := runtime.RemoveRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: id}); err != nil {
		t.Fatalf("RemoveRuntime(%s) = %v", id, err)
	}
	if _, err := os.Stat(registered); !os.IsNotExist(err) {
		t.Fatalf("registered runtime directory still exists: %v", err)
	}

	tampered := filepath.Join(root, "tampered-victim")
	if err := os.MkdirAll(tampered, 0o755); err != nil {
		t.Fatal(err)
	}
	tamperedSentinel := filepath.Join(tampered, "keep")
	if err := os.WriteFile(tamperedSentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.runtimes["php@8.4.28-r1"] = agentproto.RuntimeResponse{ID: "php@8.4.28-r1", Path: tampered}
	runtime.mu.Unlock()
	if err := runtime.RemoveRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: "php@8.4.28-r1"}); err == nil {
		t.Fatal("RemoveRuntime accepted a registered path outside the runtime root")
	}
	if _, err := os.Stat(tamperedSentinel); err != nil {
		t.Fatalf("tampered registered path was deleted: %v", err)
	}
}

func TestAgentCopySourceCopiesAndHashes(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	source := filepath.Join(root, "projects", "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.html"), []byte("<h1>hello</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "projects", "destination")
	resp, err := runtime.CopySource(context.Background(), agentproto.SourceCopyRequest{
		Mode:        "wsl",
		Source:      source,
		Destination: destination,
	})
	if err != nil {
		t.Fatalf("CopySource = %v", err)
	}
	if resp.Files != 1 || resp.Bytes == 0 || len(resp.SHA256) != 64 {
		t.Fatalf("response = %+v", resp)
	}
	data, err := os.ReadFile(filepath.Join(destination, "index.html"))
	if err != nil || string(data) != "<h1>hello</h1>" {
		t.Fatalf("destination = %q, %v", data, err)
	}
}

func TestAgentCopySourceRejectsRelativeOrUnknownPaths(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []agentproto.SourceCopyRequest{
		{Mode: "wsl", Source: "relative", Destination: filepath.Join(root, "dst")},
		{Mode: "wsl", Source: source, Destination: "relative"},
		{Mode: "bogus", Source: source, Destination: filepath.Join(root, "dst")},
		{Mode: "wsl", Source: source + "\x00", Destination: filepath.Join(root, "dst")},
	}
	for _, request := range cases {
		if _, err := runtime.CopySource(context.Background(), request); err == nil {
			t.Fatalf("CopySource accepted %+v", request)
		}
	}
}

func TestRemoveRuntimeKeepsRegistrationWhenFilesCannotBeRemoved(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	const id = "php@8.4.27-r1"
	registered := filepath.Join(root, "runtimes", id)
	if err := os.MkdirAll(registered, 0o755); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	runtime.runtimes[id] = agentproto.RuntimeResponse{ID: id, Path: registered}
	runtime.mu.Unlock()

	previous := removeRuntimePath
	removeRuntimePath = func(string) error { return errors.New("in use") }
	t.Cleanup(func() { removeRuntimePath = previous })
	if err := runtime.RemoveRuntime(context.Background(), agentproto.RuntimeRequest{BuildID: id}); err == nil {
		t.Fatal("RemoveRuntime reported success after a failed delete")
	}
	runtime.mu.Lock()
	_, stillRegistered := runtime.runtimes[id]
	runtime.mu.Unlock()
	if !stillRegistered {
		t.Fatal("failed removal forgot the runtime row")
	}
}

func TestDebian12PHPCatalogCompatibilityGate(t *testing.T) {
	tempDir := t.TempDir()
	if err := CheckDebian12PHPCompatibility(context.Background(), tempDir); err == nil {
		t.Fatal("compatibility gate accepted missing php binaries")
	}
}

func TestAgentPHPPrepareGeneratesFPMConfAndAllocatesPort(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	project := filepath.Join(root, "my-php-app")
	publicDir := filepath.Join(project, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "index.php"), []byte("<?php echo 'hello';"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:           "php-site",
		Kind:         "php",
		BackendPath:  project,
		DocumentRoot: "public",
		PHPVersion:   "8.4",
		PHPBuild:     "php@8.4.27-r1",
	}
	prepared, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{
		Generation: "gen-1",
		Site:       spec,
	})
	if err != nil {
		t.Fatalf("PreparePHP = %v", err)
	}
	if prepared.FastCGIPort < 43000 || prepared.FastCGIPort > 43999 {
		t.Fatalf("invalid FastCGI port: %d", prepared.FastCGIPort)
	}
	fpmConf := filepath.Join(root, "sites", spec.ID, "fpm.conf")
	data, err := os.ReadFile(fpmConf)
	if err != nil {
		t.Fatalf("fpm.conf missing: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[global]") || !strings.Contains(content, "[www]") || !strings.Contains(content, fmt.Sprintf("listen = 127.0.0.1:%d", prepared.FastCGIPort)) {
		t.Fatalf("fpm.conf content invalid:\n%s", content)
	}
}

func TestAgentPHPStartStopAndSwapGenerations(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	project := filepath.Join(root, "swap-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "swap-site",
		Kind:        "php",
		BackendPath: project,
		PHPVersion:  "8.4",
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-1", Site: spec}); err != nil {
		t.Fatal(err)
	}
	startResp, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-1"})
	if err != nil {
		t.Fatalf("StartPHP(gen-1) = %v", err)
	}
	if startResp.Status != "running" {
		t.Fatalf("start status = %q", startResp.Status)
	}
	status1, err := runtime.SiteStatus(spec.ID)
	if err != nil || status1.Status != "running" {
		t.Fatalf("SiteStatus after StartPHP = %+v, %v", status1, err)
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-2", Site: spec}); err != nil {
		t.Fatal(err)
	}
	swapResp, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-2"})
	if err != nil {
		t.Fatalf("StartPHP(gen-2) swap = %v", err)
	}
	if swapResp.Status != "running" {
		t.Fatalf("swap status = %q", swapResp.Status)
	}
	status2, err := runtime.SiteStatus(spec.ID)
	if err != nil || status2.Status != "running" {
		t.Fatalf("SiteStatus after swap = %+v, %v", status2, err)
	}
	stopResp, err := runtime.StopPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-2"})
	if err != nil {
		t.Fatalf("StopPHP = %v", err)
	}
	if stopResp.Status != "stopped" {
		t.Fatalf("stop status = %q", stopResp.Status)
	}
	status3, err := runtime.SiteStatus(spec.ID)
	if err != nil || status3.Status != "stopped" {
		t.Fatalf("SiteStatus after StopPHP = %+v, %v", status3, err)
	}
}

func TestAgentPHPStopUnknownGenerationDoesNotStopActiveMaster(t *testing.T) {
	runtime, _ := newTestRuntimeOnly(t)
	project := filepath.Join(t.TempDir(), "swap-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "swap-site",
		Kind:        "php",
		BackendPath: project,
		PHPVersion:  "8.4",
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-1", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-2", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-2"}); err != nil {
		t.Fatal(err)
	}

	if _, err := runtime.StopPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "never-prepared"}); err != nil {
		t.Fatalf("StopPHP(unknown generation) = %v", err)
	}
	status, err := runtime.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" {
		t.Fatalf("SiteStatus after stopping an unknown generation = %+v, %v", status, err)
	}
	runtime.mu.Lock()
	active := runtime.sites[spec.ID].activeGen
	gen := runtime.sites[spec.ID].phpGenerations[active]
	runtime.mu.Unlock()
	if active != "gen-2" || gen == nil || gen.process == nil || !gen.process.running() {
		t.Fatalf("active generation after stopping an unknown generation = %q", active)
	}
}

func TestAgentPHPStopDrainedGenerationKeepsDesiredRunning(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	project := filepath.Join(t.TempDir(), "swap-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "swap-site",
		Kind:        "php",
		BackendPath: project,
		PHPVersion:  "8.4",
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-1", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-2", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-2"}); err != nil {
		t.Fatal(err)
	}

	// Drain the previous generation after the swap; the site must stay desired-running.
	if _, err := runtime.StopPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "gen-1"}); err != nil {
		t.Fatalf("StopPHP(drained generation) = %v", err)
	}
	// Model an agent process restart: the child dies but the persisted desired
	// state must still ask for a restart.
	runtime.mu.Lock()
	if gen := runtime.sites[spec.ID].phpGenerations["gen-2"]; gen != nil && gen.process != nil {
		gen.process.stop()
	}
	runtime.mu.Unlock()
	restarted, err := NewRuntime(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := restarted.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" {
		t.Fatalf("reconciled status after draining an old generation = %+v, %v", status, err)
	}
}

func TestAgentPHPStopDefaultAfterHashSwapKeepsSiteRunning(t *testing.T) {
	runtime, _ := newTestRuntimeOnly(t)
	project := filepath.Join(t.TempDir(), "swap-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "swap-site",
		Kind:        "php",
		BackendPath: project,
		PHPVersion:  "8.4",
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "default", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "hash-2", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "hash-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StopPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "default"}); err != nil {
		t.Fatalf("StopPHP(default) = %v", err)
	}
	status, err := runtime.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" {
		t.Fatalf("SiteStatus after draining default = %+v, %v", status, err)
	}
}

func TestAgentPHPPreparePreservesRunningGeneration(t *testing.T) {
	runtime, _ := newTestRuntimeOnly(t)
	project := filepath.Join(t.TempDir(), "exec-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "exec-site",
		Kind:        "php",
		BackendPath: project,
		PHPVersion:  "8.4",
		FastCGIPort: 43060,
	}
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "default", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StartPHP(context.Background(), spec.ID, agentproto.PHPActionRequest{Generation: "default"}); err != nil {
		t.Fatal(err)
	}
	status, err := runtime.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" {
		t.Fatalf("SiteStatus after start = %+v, %v", status, err)
	}
	startedPID := status.PID

	// devstack php --site re-prepares the active generation before running the
	// CLI. Re-preparing the same generation must not detach its live process.
	if _, err := runtime.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "default", Site: spec}); err != nil {
		t.Fatal(err)
	}
	status, err = runtime.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" {
		t.Fatalf("SiteStatus after re-prepare = %+v, %v", status, err)
	}
	if status.PID != startedPID {
		t.Fatalf("re-prepare replaced the master: pid %d -> %d", startedPID, status.PID)
	}
}

func TestAgentNodeBuildDetectionAndExecution(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	project := filepath.Join(root, "node-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"test","scripts":{"build":"echo build-ok"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "node-site",
		Kind:        "static",
		BackendPath: project,
		SourceMode:  "windows",
	}
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	buildResp, err := runtime.BuildSite(context.Background(), spec.ID, agentproto.BuildRequest{})
	if err != nil {
		t.Fatalf("BuildSite = %v", err)
	}
	if buildResp.ExitCode != 0 {
		t.Fatalf("build exit code = %d, stderr = %q", buildResp.ExitCode, buildResp.Stderr)
	}
	stream, err := runtime.SiteLogs(context.Background(), spec.ID, false, 50)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	logData, _ := io.ReadAll(stream)
	if !strings.Contains(string(logData), "warning") && !strings.Contains(string(logData), "performance") {
		t.Fatalf("expected performance warning in logs:\n%s", logData)
	}
}

func TestAgentTerminalTicketAndWebSocketPTY(t *testing.T) {
	runtime, root := newTestRuntimeOnly(t)
	project := filepath.Join(root, "term-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "term-site",
		Kind:        "static",
		BackendPath: project,
	}
	if _, err := runtime.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	ticketResp, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("IssueTerminalTicket = %v", err)
	}
	if ticketResp.Ticket == "" || !strings.Contains(ticketResp.URL, spec.ID) {
		t.Fatalf("invalid ticket response: %+v", ticketResp)
	}

	identity, err := GenerateTLSIdentity(filepath.Join(t.TempDir(), "tls"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServerWithRuntime(Config{Root: t.TempDir(), Distro: "devstack"}, "secret-token", identity, nil, runtime)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// 1. WebSocket request without ticket -> 403
	reqWithoutTicket, _ := http.NewRequest(http.MethodGet, ts.URL+agentproto.PathSiteTerminalFor(spec.ID), nil)
	reqWithoutTicket.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	reqWithoutTicket.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"secret-token")
	res1, err := http.DefaultClient.Do(reqWithoutTicket)
	if err != nil {
		t.Fatal(err)
	}
	_ = res1.Body.Close()
	if res1.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without ticket, got %d", res1.StatusCode)
	}

	// 2. WebSocket request with invalid ticket -> 403
	reqWithBadTicket, _ := http.NewRequest(http.MethodGet, ts.URL+agentproto.PathSiteTerminalFor(spec.ID)+"?ticket=invalid", nil)
	reqWithBadTicket.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	reqWithBadTicket.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"secret-token")
	res2, err := http.DefaultClient.Do(reqWithBadTicket)
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 with bad ticket, got %d", res2.StatusCode)
	}

	// 3. Connect via WebSocket with valid ticket
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + agentproto.PathSiteTerminalFor(spec.ID) + "?ticket=" + ticketResp.Ticket
	wsHeader := http.Header{}
	wsHeader.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	wsHeader.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"secret-token")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: wsHeader})
	if err != nil {
		t.Fatalf("websocket dial failed: %v (resp=%+v)", err, resp)
	}
	defer conn.CloseNow()

	// Send resize frame
	resizeData, _ := json.Marshal(map[string]any{"type": "resize", "cols": 120, "rows": 40})
	if err := conn.Write(ctx, websocket.MessageText, resizeData); err != nil {
		t.Fatalf("write resize frame = %v", err)
	}

	// Send input frame
	inputData, _ := json.Marshal(map[string]any{"type": "input", "data": "echo terminal-ok\n"})
	if err := conn.Write(ctx, websocket.MessageText, inputData); err != nil {
		t.Fatalf("write input frame = %v", err)
	}

	// Read frame from server
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame = %v", err)
	}
	if len(msg) == 0 {
		t.Fatal("empty message from terminal websocket")
	}

	// 4. Reuse same ticket -> should fail with 403 because ticket was consumed (one-time use)
	reqReuseTicket, _ := http.NewRequest(http.MethodGet, ts.URL+agentproto.PathSiteTerminalFor(spec.ID)+"?ticket="+ticketResp.Ticket, nil)
	reqReuseTicket.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	reqReuseTicket.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"secret-token")
	res3, err := http.DefaultClient.Do(reqReuseTicket)
	if err != nil {
		t.Fatal(err)
	}
	_ = res3.Body.Close()
	if res3.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 on reused ticket, got %d", res3.StatusCode)
	}

	// 5. Connect via WebSocket with ticket in X-Terminal-Ticket header
	ticketResp2, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("IssueTerminalTicket = %v", err)
	}
	wsHeader2 := http.Header{}
	wsHeader2.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	wsHeader2.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"secret-token")
	wsHeader2.Set("X-Terminal-Ticket", ticketResp2.Ticket)
	wsURLNoParam := "ws" + strings.TrimPrefix(ts.URL, "http") + agentproto.PathSiteTerminalFor(spec.ID)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	conn2, resp2, err := websocket.Dial(ctx2, wsURLNoParam, &websocket.DialOptions{HTTPHeader: wsHeader2})
	if err != nil {
		t.Fatalf("websocket dial with X-Terminal-Ticket header failed: %v (resp=%+v)", err, resp2)
	}
	defer conn2.CloseNow()
	if err := conn2.Write(ctx2, websocket.MessageText, inputData); err != nil {
		t.Fatalf("write input frame = %v", err)
	}
	if _, _, err := conn2.Read(ctx2); err != nil {
		t.Fatalf("read frame = %v", err)
	}

	// 6. Connect via WebSocket with ticket in Sec-WebSocket-Protocol
	ticketResp3, err := runtime.IssueTerminalTicket(spec.ID, agentproto.TerminalTicketRequest{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("IssueTerminalTicket = %v", err)
	}
	wsHeader3 := http.Header{}
	wsHeader3.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	wsHeader3.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"secret-token")
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	conn3, resp3, err := websocket.Dial(ctx3, wsURLNoParam, &websocket.DialOptions{
		HTTPHeader:   wsHeader3,
		Subprotocols: []string{ticketResp3.Ticket},
	})
	if err != nil {
		t.Fatalf("websocket dial with Sec-WebSocket-Protocol failed: %v (resp=%+v)", err, resp3)
	}
	defer conn3.CloseNow()
	if err := conn3.Write(ctx3, websocket.MessageText, inputData); err != nil {
		t.Fatalf("write input frame = %v", err)
	}
	if _, _, err := conn3.Read(ctx3); err != nil {
		t.Fatalf("read frame = %v", err)
	}
}

func TestAgentSiteExec(t *testing.T) {
	rt, root := newTestRuntimeOnly(t)
	project := filepath.Join(root, "exec-app")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:          "exec-site",
		Kind:        "static",
		BackendPath: project,
		Env:         map[string]string{"MY_VAR": "my_val"},
	}
	if _, err := rt.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	argv := []string{"echo", "hello-exec"}
	if runtime.GOOS == "windows" {
		argv = []string{"cmd.exe", "/c", "echo", "hello-exec"}
	}
	resp, err := rt.ExecSite(context.Background(), spec.ID, agentproto.SiteExecRequest{Argv: argv})
	if err != nil {
		t.Fatalf("ExecSite = %v", err)
	}
	if resp.ExitCode != 0 || !strings.Contains(resp.Stdout, "hello-exec") {
		t.Fatalf("unexpected ExecSite response: %+v", resp)
	}
}

func TestToPOSIXPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/var/www/site", "/var/www/site"},
		{`C:\Projects\app`, "/mnt/c/Projects/app"},
		{`c:/Projects/app`, "/mnt/c/Projects/app"},
		{`D:\Data\site`, "/mnt/d/Data/site"},
		{`\\wsl.localhost\devstack\data\devstack\projects\app`, "/data/devstack/projects/app"},
		{`\\wsl$\devstack\home\user\site`, "/home/user/site"},
		{`//wsl.localhost/devstack/var/www`, "/var/www"},
		{`//wsl$/devstack/srv/data`, "/srv/data"},
		{`\\wsl.localhost\devstack`, "/"},
		{`//wsl.localhost/devstack/`, "/"},
	}

	for _, tc := range tests {
		got := toPOSIXPath(tc.input)
		if got != tc.want {
			t.Errorf("toPOSIXPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLogHubReaderCloseUnregistersSubscriber(t *testing.T) {
	dir := t.TempDir()
	hub := newLogHub(dir)
	hub.append("mysite", "line 1")

	// Read with follow=true using a background context that is NOT cancelled
	rc, err := hub.read("mysite", true, 10, context.Background())
	if err != nil {
		t.Fatal(err)
	}

	hub.mu.Lock()
	subsCount := len(hub.subs["mysite"])
	hub.mu.Unlock()
	if subsCount != 1 {
		t.Fatalf("expected 1 subscriber, got %d", subsCount)
	}

	// Reading initial line
	buf := make([]byte, 100)
	n, err := rc.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n]), "line 1") {
		t.Fatalf("expected line 1, got %q", string(buf[:n]))
	}

	// Closing read closer MUST unregister subscriber without needing new log lines or ctx cancellation
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		hub.mu.Lock()
		count := len(hub.subs["mysite"])
		hub.mu.Unlock()
		if count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscriber was not unregistered after Close(): count = %d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeAgentDatabaseSecret(t *testing.T, root, serviceID, userID, value string) {
	t.Helper()
	dir := filepath.Join(root, "services", serviceID, "secrets", "database-users")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, userID), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareSiteInjectsManagedDatabaseEnv(t *testing.T) {
	rt, root, spec := newStaticTestRuntime(t)
	writeAgentDatabaseSecret(t, root, "mysql", "usr_app", "s3cret")
	spec.DatabaseLinks = []agentproto.DatabaseLinkSpec{{
		ServiceID: "mysql", Engine: "mysql", Database: "app", UserID: "usr_app",
		Username: "app", Port: 3306, Primary: true, PasswordManaged: true,
	}}
	if _, err := rt.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	env, err := rt.resolveDatabaseEnv(spec)
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_PASSWORD"] != "s3cret" || env["DB_DATABASE"] != "app" {
		t.Fatalf("env=%v", env)
	}
	if env["DB_HOST"] != "127.0.0.1" || env["DB_PORT"] != "3306" || env["DB_USERNAME"] != "app" || env["DB_CONNECTION"] != "mysql" {
		t.Fatalf("env=%v", env)
	}
	if !strings.HasPrefix(env["DATABASE_URL"], "mysql://app:s3cret@127.0.0.1:3306/app") {
		t.Fatalf("DATABASE_URL=%q", env["DATABASE_URL"])
	}
}

func TestPrepareSiteOmitsMetadataPassword(t *testing.T) {
	rt, _, spec := newStaticTestRuntime(t)
	spec.DatabaseLinks = []agentproto.DatabaseLinkSpec{{
		ServiceID: "mysql", Engine: "mysql", Database: "app", Username: "app", Primary: true, PasswordManaged: false,
	}}
	if _, err := rt.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	env, err := rt.resolveDatabaseEnv(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env["DB_PASSWORD"]; ok {
		t.Fatalf("metadata-only env has a password: %v", env)
	}
	if _, ok := env["DATABASE_URL"]; ok {
		t.Fatalf("metadata-only env has a URL: %v", env)
	}
}

func TestPrepareSiteFailsWhenManagedSecretIsMissing(t *testing.T) {
	rt, _, spec := newStaticTestRuntime(t)
	spec.DatabaseLinks = []agentproto.DatabaseLinkSpec{{
		ServiceID: "mysql", Engine: "mysql", Database: "app", UserID: "usr_missing",
		Username: "app", Primary: true, PasswordManaged: true,
	}}
	if _, err := rt.PrepareSite(context.Background(), spec); err == nil {
		t.Fatal("PrepareSite silently omitted a missing managed password")
	}
}

func TestPrepareSiteAdditionalLinkUsesNamespacedEnv(t *testing.T) {
	rt, root, spec := newStaticTestRuntime(t)
	writeAgentDatabaseSecret(t, root, "postgres", "usr_blog", "pgsecret")
	spec.DatabaseLinks = []agentproto.DatabaseLinkSpec{
		{ServiceID: "mysql", Engine: "mysql", Database: "app", Username: "app", Primary: true},
		{ServiceID: "postgres", Engine: "postgres", Database: "blog", UserID: "usr_blog", Username: "blog", Port: 5432, PasswordManaged: true},
	}
	if _, err := rt.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	env, err := rt.resolveDatabaseEnv(spec)
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_BLOG_DATABASE"] != "blog" || env["DB_BLOG_PASSWORD"] != "pgsecret" || env["DB_BLOG_PORT"] != "5432" {
		t.Fatalf("env=%v", env)
	}
	if !strings.HasPrefix(env["DB_BLOG_URL"], "postgresql://blog:pgsecret@127.0.0.1:5432/blog") {
		t.Fatalf("DB_BLOG_URL=%q", env["DB_BLOG_URL"])
	}
	if _, ok := env["DB_PASSWORD"]; ok {
		t.Fatalf("primary metadata-only link gained a password: %v", env)
	}
}

func TestPrepareSiteDoesNotPersistDatabasePassword(t *testing.T) {
	rt, root, spec := newStaticTestRuntime(t)
	writeAgentDatabaseSecret(t, root, "mysql", "usr_app", "s3cret")
	spec.DatabaseLinks = []agentproto.DatabaseLinkSpec{{
		ServiceID: "mysql", Engine: "mysql", Database: "app", UserID: "usr_app",
		Username: "app", Primary: true, PasswordManaged: true,
	}}
	if _, err := rt.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "run", "sites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") {
		t.Fatalf("runtime state persisted a database password:\n%s", data)
	}
}

func TestPreparePHPInjectsDatabaseEnvWithoutWritingSecretToFPM(t *testing.T) {
	rt, root := newTestRuntimeOnly(t)
	project := filepath.Join(root, "php-db-app")
	publicDir := filepath.Join(project, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "index.php"), []byte("<?php echo 'ok';"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeAgentDatabaseSecret(t, root, "mysql", "usr_app", "s3cret")
	spec := agentproto.SiteSpec{
		ID: "php-db-site", Kind: "php", BackendPath: project, DocumentRoot: "public",
		PHPVersion: "8.4", PHPBuild: "php@8.4.27-r1",
		DatabaseLinks: []agentproto.DatabaseLinkSpec{{
			ServiceID: "mysql", Engine: "mysql", Database: "app", UserID: "usr_app",
			Username: "app", Primary: true, PasswordManaged: true,
		}},
	}
	if _, err := rt.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-1", Site: spec}); err != nil {
		t.Fatal(err)
	}
	if rt.sites[spec.ID].phpGenerations["gen-1"] == nil {
		t.Fatal("PHP generation was not prepared")
	}
	env, err := rt.resolveDatabaseEnv(spec)
	if err != nil || env["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("resolved env = %+v, %v", env, err)
	}
	fpmData, err := os.ReadFile(filepath.Join(root, "sites", spec.ID, "fpm.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fpmData), "s3cret") {
		t.Fatalf("php-fpm.conf contains a database password:\n%s", fpmData)
	}
}

func TestPrepareSiteStripsReservedDatabaseEnv(t *testing.T) {
	rt, root, spec := newStaticTestRuntime(t)
	spec.Env = map[string]string{
		"APP_ENV":      "local",
		"DB_PASSWORD":  "leaked",
		"DB_DATABASE":  "evil",
		"DB_HOST":      "evil",
		"DATABASE_URL": "mysql://evil",
	}
	if _, err := rt.PrepareSite(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	stored := rt.sites[spec.ID].spec.Env
	if stored["APP_ENV"] != "local" {
		t.Fatalf("non-reserved env was lost: %v", stored)
	}
	for _, key := range []string{"DB_PASSWORD", "DB_DATABASE", "DB_HOST", "DATABASE_URL"} {
		if _, ok := stored[key]; ok {
			t.Fatalf("reserved key %s was persisted: %v", key, stored)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "run", "sites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "leaked") || strings.Contains(string(data), "mysql://evil") {
		t.Fatalf("reserved database env reached sites.json:\n%s", data)
	}
}

func TestPreparePHPStripsReservedDatabaseEnv(t *testing.T) {
	rt, root := newTestRuntimeOnly(t)
	project := filepath.Join(root, "php-env-app")
	publicDir := filepath.Join(project, "public")
	if err := os.MkdirAll(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(publicDir, "index.php"), []byte("<?php echo 'ok';"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID: "php-env-site", Kind: "php", BackendPath: project, DocumentRoot: "public",
		PHPVersion: "8.4", PHPBuild: "php@8.4.27-r1",
		Env: map[string]string{"APP_ENV": "local", "DB_PASSWORD": "leaked", "DATABASE_URL": "mysql://evil"},
	}
	if _, err := rt.PreparePHP(context.Background(), spec.ID, agentproto.PHPPrepareRequest{Generation: "gen-1", Site: spec}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "sites", spec.ID, "fpm.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "leaked") || strings.Contains(string(data), "mysql://evil") {
		t.Fatalf("reserved database env reached php-fpm.conf:\n%s", data)
	}
	if !strings.Contains(string(data), "APP_ENV") {
		t.Fatalf("non-reserved env was lost:\n%s", data)
	}
}

func TestPrepareSiteRejectsInsecureSecretDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes are not meaningful on Windows")
	}
	rt, root, spec := newStaticTestRuntime(t)
	secrets := filepath.Join(root, "services", "mysql", "secrets")
	dir := filepath.Join(secrets, "database-users")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "usr_app"), []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec.DatabaseLinks = []agentproto.DatabaseLinkSpec{{
		ServiceID: "mysql", Engine: "mysql", Database: "app", UserID: "usr_app",
		Username: "app", Primary: true, PasswordManaged: true,
	}}
	if _, err := rt.PrepareSite(context.Background(), spec); err == nil {
		t.Fatal("PrepareSite accepted a world-readable secret directory")
	}
}

func TestResolveDatabaseEnvUsesFrameworkConnectionNames(t *testing.T) {
	rt, _, _ := newStaticTestRuntime(t)
	cases := []struct {
		service string
		engine  string
		want    string
	}{
		{"mysql", "mysql", "mysql"},
		{"mariadb", "mariadb", "mariadb"},
		{"postgres", "postgres", "pgsql"},
		{"redis", "redis", "redis"},
	}
	for _, tc := range cases {
		t.Run(tc.service, func(t *testing.T) {
			env, err := rt.resolveDatabaseEnv(agentproto.SiteSpec{
				ID: "site-" + tc.service,
				DatabaseLinks: []agentproto.DatabaseLinkSpec{{
					ServiceID: tc.service, Engine: tc.engine, Database: "app", Username: "app", Primary: true,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if env["DB_CONNECTION"] != tc.want {
				t.Fatalf("DB_CONNECTION = %q, want %q (env=%v)", env["DB_CONNECTION"], tc.want, env)
			}
		})
	}
}
