package agent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
	status, err := restarted.SiteStatus(spec.ID)
	if err != nil || status.Status != "running" || status.HTTPPort != prepared.HTTPPort {
		t.Fatalf("reconciled status = %+v, %v; wanted port %d", status, err, prepared.HTTPPort)
	}
	_ = restarted.StopSite(context.Background(), spec.ID, false)
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
