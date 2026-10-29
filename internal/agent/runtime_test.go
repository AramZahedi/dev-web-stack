package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	runtime, _ := newTestRuntimeOnly(t)
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
