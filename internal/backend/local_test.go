package backend

import (
	"bufio"
	"context"
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

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

var (
	selfBinOnce sync.Once
	selfBinPath string
	selfBinErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if selfBinPath != "" {
		_ = os.RemoveAll(filepath.Dir(selfBinPath))
	}
	os.Exit(code)
}

func TestLocalStaticSite(t *testing.T) {
	b, root := newTestLocal(t)
	rootFile(t, root, "index.html", "hello")
	spec := SiteSpec{ID: "s1", Kind: "static", HostPath: root, BackendPath: root, HTTPPort: freePort(t), SPAFallback: false}
	prepared, err := b.Prepare(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.BackendHost != "127.0.0.1" || prepared.HTTPPort != spec.HTTPPort {
		t.Fatalf("prepared = %+v", prepared)
	}
	if _, err := b.Start(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background(), "s1", true) })

	status, err := b.Status(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.StatusRunning || status.PID <= 0 {
		t.Fatalf("status = %+v", status)
	}

	body, code := readBody(t, fmt.Sprintf("http://127.0.0.1:%d/", spec.HTTPPort))
	if body != "hello" || code != http.StatusOK {
		t.Fatalf("GET / = %d %q", code, body)
	}
}

func TestLocalNodeCommandGetsPort(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	b, root := newTestLocal(t)
	spec := SiteSpec{ID: "n1", Kind: "node", HostPath: root, BackendPath: root, HTTPPort: freePort(t),
		Command: `python3 -m http.server "$PORT"`}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Start(context.Background(), "n1"); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(context.Background(), "n1", true)

	_, code := readBody(t, fmt.Sprintf("http://127.0.0.1:%d/", spec.HTTPPort))
	if code != http.StatusOK {
		t.Fatalf("GET / = %d", code)
	}
}

func TestLocalStaticSPAFallback(t *testing.T) {
	b, root := newTestLocal(t)
	rootFile(t, root, "index.html", "spa")
	spec := SiteSpec{ID: "spa", Kind: "static", BackendPath: root, HTTPPort: freePort(t), SPAFallback: true}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Start(context.Background(), "spa"); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(context.Background(), "spa", true)

	body, code := readBody(t, fmt.Sprintf("http://127.0.0.1:%d/some/client/route", spec.HTTPPort))
	if code != http.StatusOK || body != "spa" {
		t.Fatalf("GET route = %d %q", code, body)
	}
	_, code = readBody(t, fmt.Sprintf("http://127.0.0.1:%d/missing.js", spec.HTTPPort))
	if code != http.StatusNotFound {
		t.Fatalf("GET missing.js = %d, want 404", code)
	}
}

func TestLocalStaticDocumentRootRelativeToProject(t *testing.T) {
	b, root := newTestLocal(t)
	rootFile(t, filepath.Join(root, "public"), "index.html", "public")
	spec := SiteSpec{ID: "rel", Kind: "static", BackendPath: root, DocumentRoot: "public", HTTPPort: freePort(t)}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Start(context.Background(), "rel"); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(context.Background(), "rel", true)

	body, code := readBody(t, fmt.Sprintf("http://127.0.0.1:%d/", spec.HTTPPort))
	if code != http.StatusOK || body != "public" {
		t.Fatalf("GET / = %d %q", code, body)
	}
}

func TestLocalStopAllStopsEveryProcess(t *testing.T) {
	b, root := newTestLocal(t)
	rootFile(t, root, "index.html", "hello")
	ids := []string{"all-a", "all-b"}
	ports := make(map[string]int, len(ids))
	for _, id := range ids {
		port := freePort(t)
		ports[id] = port
		spec := SiteSpec{ID: id, Kind: "static", HostPath: root, BackendPath: root, HTTPPort: port}
		if _, err := b.Prepare(context.Background(), spec); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Start(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	for _, id := range ids {
		status, err := b.Status(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != model.StatusStopped {
			t.Fatalf("%s status = %q, want stopped", id, status.State)
		}
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ports[id]), 100*time.Millisecond); err == nil {
			_ = conn.Close()
			t.Fatalf("%s port %d is still open", id, ports[id])
		}
	}
}

func TestLocalPrepareValidation(t *testing.T) {
	b, root := newTestLocal(t)
	ctx := context.Background()

	if _, err := b.Prepare(ctx, SiteSpec{ID: "missing", Kind: "static", BackendPath: filepath.Join(root, "absent"), HTTPPort: freePort(t)}); err == nil {
		t.Fatal("Prepare accepted a missing document root")
	}
	rootFile(t, root, "file.txt", "not a directory")
	if _, err := b.Prepare(ctx, SiteSpec{ID: "file", Kind: "static", BackendPath: root, DocumentRoot: "file.txt", HTTPPort: freePort(t)}); err == nil {
		t.Fatal("Prepare accepted a file as document root")
	}
	if _, err := b.Prepare(ctx, SiteSpec{ID: "escaping", Kind: "static", BackendPath: root, DocumentRoot: "../outside", HTTPPort: freePort(t)}); err == nil {
		t.Fatal("Prepare accepted a document root outside the project")
	}
	if _, err := b.Prepare(ctx, SiteSpec{ID: "nocommand", Kind: "node", BackendPath: root, HTTPPort: freePort(t)}); err == nil {
		t.Fatal("Prepare accepted a node site without a command")
	}
	if _, err := b.Prepare(ctx, SiteSpec{ID: "noport", Kind: "node", BackendPath: root, Command: "true"}); err == nil {
		t.Fatal("Prepare accepted a node site without a port")
	}
	if _, err := b.Prepare(ctx, SiteSpec{ID: "php", Kind: "php", BackendPath: root, HTTPPort: freePort(t)}); err == nil {
		t.Fatal("Prepare accepted a PHP site before Plan 2")
	}
}

func TestLocalStartRequiresPrepare(t *testing.T) {
	b, _ := newTestLocal(t)
	if _, err := b.Start(context.Background(), "unknown"); err == nil {
		t.Fatal("Start accepted an unprepared site")
	}
}

func TestLocalLogs(t *testing.T) {
	b, root := newTestLocal(t)
	rootFile(t, root, "index.html", "hello")
	spec := SiteSpec{ID: "logs", Kind: "static", BackendPath: root, HTTPPort: freePort(t)}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Start(context.Background(), "logs"); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(context.Background(), "logs", true)

	stream, err := b.Logs(context.Background(), "logs", true)
	if err != nil {
		t.Fatal(err)
	}
	writer := b.logs.Writer("logs", "app")
	if _, err := fmt.Fprintln(writer, "hello world"); err != nil {
		t.Fatal(err)
	}

	line := readLine(t, stream, 5*time.Second)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	fields := strings.SplitN(strings.TrimSuffix(line, "\n"), " ", 3)
	if len(fields) != 3 {
		t.Fatalf("log line = %q", line)
	}
	if _, err := time.Parse(time.RFC3339, fields[0]); err != nil {
		t.Fatalf("timestamp %q: %v", fields[0], err)
	}
	if fields[1] != "app" || fields[2] != "hello world" {
		t.Fatalf("log line = %q", line)
	}

	// A non-following reader is a finite snapshot.
	if _, err := fmt.Fprintln(writer, "second"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := b.Logs(context.Background(), "logs", false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), " app hello world\n") || !strings.Contains(string(data), " app second\n") {
		t.Fatalf("snapshot = %q", data)
	}

	if _, err := b.Logs(context.Background(), "", true); err == nil {
		t.Fatal("Logs accepted an empty site id")
	}
}

func TestLocalExec(t *testing.T) {
	b, root := newTestLocal(t)
	spec := SiteSpec{ID: "exec", Kind: "static", BackendPath: root, HTTPPort: freePort(t),
		Env: map[string]string{"SITE_VAR": "site"}}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	result, err := b.Exec(context.Background(), "exec", []string{"/bin/sh", "-c", `printf '%s' "$SITE_VAR"`}, nil)
	if err != nil || result.ExitCode != 0 || result.Stdout != "site" {
		t.Fatalf("site env result = %+v, %v", result, err)
	}
	result, err = b.Exec(context.Background(), "exec", []string{"/bin/sh", "-c", `printf '%s' "$SITE_VAR"`},
		map[string]string{"SITE_VAR": "call"})
	if err != nil || result.ExitCode != 0 || result.Stdout != "call" {
		t.Fatalf("call env result = %+v, %v", result, err)
	}

	result, err = b.Exec(context.Background(), "exec",
		[]string{"/bin/sh", "-c", "printf out; printf err >&2; exit 7"}, nil)
	if err != nil {
		t.Fatalf("non-zero exit returned a Go error: %v", err)
	}
	if result.ExitCode != 7 || result.Stdout != "out" || result.Stderr != "err" {
		t.Fatalf("result = %+v", result)
	}

	if _, err := b.Exec(context.Background(), "exec", []string{"/no/such/devstack-test-binary"}, nil); err == nil {
		t.Fatal("Exec accepted a missing executable")
	}
	if _, err := b.Exec(context.Background(), "unknown", []string{"/bin/true"}, nil); err == nil {
		t.Fatal("Exec accepted an unprepared site")
	}
}

func TestLocalExecResolvesCommandFromSitePATH(t *testing.T) {
	b, root := newTestLocal(t)
	binDir := filepath.Join(root, "site-bin")
	writeExecutable(t, binDir, "site-tool", `printf '%s' "$SITE_MARKER"`)
	spec := SiteSpec{ID: "exec-path", Kind: "static", BackendPath: root, HTTPPort: freePort(t),
		Env: map[string]string{
			"PATH":        binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
			"SITE_MARKER": "found-on-site-path",
		}}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	result, err := b.Exec(context.Background(), "exec-path", []string{"site-tool"}, nil)
	if err != nil {
		t.Fatalf("bare command did not resolve through the site PATH: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "found-on-site-path" {
		t.Fatalf("result = %+v", result)
	}
}

func TestLocalExecUsesNodeRuntimeEnvironment(t *testing.T) {
	nodeRoot := t.TempDir()
	binDir := filepath.Join(nodeRoot, "bin")
	writeExecutable(t, binDir, "node-tool",
		`printf '%s|%s|%s\n' "$PORT" "$NODE_EXTRA_CA_CERTS" "$(command -v node-tool)"`)
	fake := &fakeRuntime{build: model.RuntimeBuild{
		ID: "node-22", Kind: "node", Version: "22", Path: nodeRoot,
	}}
	b, root := newTestLocalWithRuntime(t, fake)
	port := freePort(t)
	spec := SiteSpec{ID: "node-exec", Kind: "node", BackendPath: root, HTTPPort: port,
		Command: "true", RuntimeVersion: "22"}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}

	result, err := b.Exec(context.Background(), "node-exec", []string{"node-tool"}, nil)
	if err != nil {
		t.Fatalf("node Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	want := fmt.Sprintf("%d|%s|%s", port,
		filepath.Join(b.cfg.Home, "pki", "ca-bundle.crt"),
		filepath.Join(binDir, "node-tool"))
	if strings.TrimSpace(result.Stdout) != want {
		t.Fatalf("stdout = %q, want %q", result.Stdout, want)
	}
}

func TestLocalExecCancellationReturnsContextErrorAndKillsGroup(t *testing.T) {
	b, root := newTestLocal(t)
	spec := SiteSpec{ID: "exec-cancel", Kind: "static", BackendPath: root, HTTPPort: freePort(t)}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(root, "background.pid")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := b.Exec(ctx, "exec-cancel",
		[]string{"/bin/sh", "-c", `sleep 30 & echo $! > "$PID_FILE"; wait`},
		map[string]string{"PID_FILE": pidFile})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Exec returned success after its context expired")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Exec took %s to honour cancellation", elapsed)
	}

	pid := waitPIDFile(t, pidFile, 2*time.Second)
	waitProcessGone(t, pid, 2*time.Second)
}

func TestLocalInstallCA(t *testing.T) {
	b, _ := newTestLocal(t)
	dir := t.TempDir()
	system := filepath.Join(dir, "system.pem")
	if err := os.WriteFile(system, []byte("SYSTEM-ROOT\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	old := systemRootFiles
	systemRootFiles = []string{filepath.Join(dir, "absent.pem"), system}
	t.Cleanup(func() { systemRootFiles = old })

	pem := []byte("DEVSTACK-ROOT\n")
	if err := b.InstallCA(context.Background(), pem); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.cfg.Home, "pki", "ca-bundle.crt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "SYSTEM-ROOT\nDEVSTACK-ROOT\n" {
		t.Fatalf("bundle = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	if err := b.InstallCA(context.Background(), pem); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Fatalf("InstallCA is not idempotent: %q then %q", data, again)
	}
}

func TestLocalInstallCAWithoutSystemRoots(t *testing.T) {
	b, _ := newTestLocal(t)
	old := systemRootFiles
	systemRootFiles = []string{filepath.Join(t.TempDir(), "absent.pem")}
	t.Cleanup(func() { systemRootFiles = old })

	if err := b.InstallCA(context.Background(), []byte("DEVSTACK\n")); err == nil {
		t.Fatal("InstallCA succeeded without a system root bundle")
	}
}

func TestLocalHealth(t *testing.T) {
	b, _ := newTestLocal(t)
	if err := b.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(b.cfg.Home, "run")); err != nil {
		t.Fatal(err)
	}
	if err := b.Health(context.Background()); err == nil {
		t.Fatal("Health accepted a missing run directory")
	}
}

func TestLocalNodePrepareUsesRuntimeProvider(t *testing.T) {
	fake := &fakeRuntime{build: model.RuntimeBuild{
		ID: "node-22", Kind: "node", Version: "22", Path: filepath.Join(t.TempDir(), "node"),
	}}
	b, root := newTestLocalWithRuntime(t, fake)
	spec := SiteSpec{ID: "n2", Kind: "node", BackendPath: root, HTTPPort: freePort(t),
		Command: "true", RuntimeVersion: "22"}
	if _, err := b.Prepare(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	calls := fake.ensuredCalls()
	if len(calls) != 1 || calls[0].kind != "node" || calls[0].version != "22" {
		t.Fatalf("ensure calls = %+v", calls)
	}
}

func TestNodeEnvironment(t *testing.T) {
	t.Setenv("PATH", "/usr/local/bin:/usr/bin")
	env := nodeEnvironment(41234, "/opt/node", "/home/u/.devstack/pki/ca-bundle.crt",
		map[string]string{"FOO": "bar", "PORT": "9999", "NODE_EXTRA_CA_CERTS": "wrong"})
	if env["PORT"] != "41234" {
		t.Fatalf("PORT = %q", env["PORT"])
	}
	if env["NODE_EXTRA_CA_CERTS"] != "/home/u/.devstack/pki/ca-bundle.crt" {
		t.Fatalf("NODE_EXTRA_CA_CERTS = %q", env["NODE_EXTRA_CA_CERTS"])
	}
	if env["PATH"] != "/opt/node/bin:/usr/local/bin:/usr/bin" {
		t.Fatalf("PATH = %q", env["PATH"])
	}
	if env["FOO"] != "bar" {
		t.Fatalf("FOO = %q", env["FOO"])
	}

	noRuntime := nodeEnvironment(42000, "", "/ca.pem", map[string]string{"PATH": "/custom/bin"})
	if noRuntime["PATH"] != "/custom/bin" {
		t.Fatalf("site PATH = %q", noRuntime["PATH"])
	}
}

func TestLocalStatusWithoutStart(t *testing.T) {
	b, _ := newTestLocal(t)
	status, err := b.Status(context.Background(), "never-started")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.StatusStopped {
		t.Fatalf("status = %+v", status)
	}
}

func newTestLocal(t *testing.T) (*Local, string) {
	t.Helper()
	return newTestLocalWithRuntime(t, &fakeRuntime{})
}

func newTestLocalWithRuntime(t *testing.T, runtimes RuntimeProvider) (*Local, string) {
	t.Helper()
	home := t.TempDir()
	cfg := config.Default()
	cfg.Home = home
	paths := config.NewPaths(home)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logStore, err := logs.New(paths.Logs, cfg.LogMaxMB, cfg.LogKeep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logStore.Close() })

	b, err := NewLocal(cfg, st, logStore, runtimes, testSelfBin(t))
	if err != nil {
		t.Fatal(err)
	}
	return b, t.TempDir()
}

func testSelfBin(t *testing.T) string {
	t.Helper()
	selfBinOnce.Do(func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			selfBinErr = fmt.Errorf("resolve test source path")
			return
		}
		repo := filepath.Dir(filepath.Dir(filepath.Dir(file)))
		dir, err := os.MkdirTemp("", "devstack-test-bin-")
		if err != nil {
			selfBinErr = err
			return
		}
		selfBinPath = filepath.Join(dir, "devstack")
		cmd := exec.Command("go", "build", "-o", selfBinPath, "./cmd/devstack")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			selfBinErr = fmt.Errorf("build devstack: %w\n%s", err, out)
		}
	})
	if selfBinErr != nil {
		t.Fatal(selfBinErr)
	}
	return selfBinPath
}

func rootFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitPIDFile(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
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

// waitProcessGone accepts both a reaped process and a zombie, because a killed
// descendant may remain a zombie briefly until the init process reaps it.
func waitProcessGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if processGone(pid) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d survived Exec cancellation", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	closeParen := strings.LastIndexByte(string(data), ')')
	return closeParen >= 0 && closeParen+2 < len(data) && data[closeParen+2] == 'Z'
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

func readBody(t *testing.T, url string) (string, int) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	return string(data), resp.StatusCode
}

func readLine(t *testing.T, r io.Reader, timeout time.Duration) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		ch <- result{line, err}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read log line: %v", got.err)
		}
		return got.line
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a log line")
		return ""
	}
}

type fakeRuntime struct {
	mu      sync.Mutex
	build   model.RuntimeBuild
	err     error
	ensured []runtimeCall
	removed []string
}

type runtimeCall struct{ kind, version string }

func (f *fakeRuntime) Ensure(_ context.Context, kind, version string) (model.RuntimeBuild, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, runtimeCall{kind, version})
	if f.err != nil {
		return model.RuntimeBuild{}, f.err
	}
	return f.build, nil
}

func (f *fakeRuntime) Remove(_ context.Context, buildID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, buildID)
	return f.err
}

func (f *fakeRuntime) ensuredCalls() []runtimeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]runtimeCall(nil), f.ensured...)
}
