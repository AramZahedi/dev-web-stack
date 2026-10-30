//go:build !windows

package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/fcgi"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
)

// systemPHPBuild is this machine's PHP installation, used by the real-FPM
// tests. CI machines without it skip instead of failing.
const systemPHPBuild = "/usr/local/php"

func systemPHP(t *testing.T) string {
	t.Helper()
	for _, rel := range []string{"bin/php", "sbin/php-fpm"} {
		info, err := os.Stat(filepath.Join(systemPHPBuild, rel))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			t.Skipf("%s/%s is not an executable PHP binary", systemPHPBuild, rel)
		}
	}
	return systemPHPBuild
}

func TestLocalPHPStartsFPMAndServesHealth(t *testing.T) {
	root := systemPHP(t)
	b, _ := newTestLocal(t)
	ctx := context.Background()
	spec := phpTestSpec(t, b.cfg.Home, root, "php@8.4.4-r1", freePHPCPort(t))

	if _, err := b.Prepare(ctx, spec); err != nil {
		t.Fatal(err)
	}
	info, err := b.Start(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Stop(context.Background(), spec.ID, true) }()
	if info.PID <= 0 {
		t.Fatalf("Start PID = %d", info.PID)
	}

	resp, err := fastcgiHealth(ctx, spec.FastCGIPort, spec.PHPHealthScript)
	if err != nil || resp != "ok" {
		t.Fatalf("%q %v", resp, err)
	}
	status, err := b.Status(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.StatusRunning || status.PID != info.PID {
		t.Fatalf("status = %+v, want running %d", status, info.PID)
	}
}

func TestLocalPHPStageRunsAlongsideDefault(t *testing.T) {
	root := systemPHP(t)
	b, _ := newTestLocal(t)
	ctx := context.Background()

	defaultSpec := phpTestSpec(t, b.cfg.Home, root, "php@8.4.4-r1", freePHPCPort(t))
	if _, err := b.Prepare(ctx, defaultSpec); err != nil {
		t.Fatal(err)
	}
	defaultInfo, err := b.Start(ctx, defaultSpec.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Stop(context.Background(), defaultSpec.ID, true) }()

	stagedSpec := phpTestSpec(t, b.cfg.Home, root, "php@8.4.4-r1", freePHPCPort(t))
	if _, err := b.PreparePHP(ctx, defaultSpec.ID, "swap-1", stagedSpec); err != nil {
		t.Fatal(err)
	}
	stagedInfo, err := b.StartPHP(ctx, defaultSpec.ID, "swap-1")
	if err != nil {
		t.Fatal(err)
	}

	for _, port := range []int{defaultSpec.FastCGIPort, stagedSpec.FastCGIPort} {
		resp, err := fastcgiHealth(ctx, port, defaultSpec.PHPHealthScript)
		if err != nil || resp != "ok" {
			t.Fatalf("port %d health = %q, %v", port, resp, err)
		}
	}

	status, err := b.Status(ctx, defaultSpec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.PID != stagedInfo.PID {
		t.Fatalf("active PID = %d, want the staged generation %d", status.PID, stagedInfo.PID)
	}

	// Stopping the active generation promotes the remaining default master,
	// which keeps serving throughout.
	if err := b.StopPHP(ctx, defaultSpec.ID, "swap-1"); err != nil {
		t.Fatal(err)
	}
	waitPortClosed(t, stagedSpec.FastCGIPort, 10*time.Second)
	if resp, err := fastcgiHealth(ctx, defaultSpec.FastCGIPort, defaultSpec.PHPHealthScript); err != nil || resp != "ok" {
		t.Fatalf("default health after staged stop = %q, %v", resp, err)
	}
	status, err = b.Status(ctx, defaultSpec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.PID != defaultInfo.PID || status.State != model.StatusRunning {
		t.Fatalf("active status after staged stop = %+v, want default pid %d", status, defaultInfo.PID)
	}

	// A second staged generation proves Stop terminates every generation.
	if _, err := b.StartPHP(ctx, defaultSpec.ID, "swap-1"); err != nil {
		t.Fatal(err)
	}
	if err := b.Stop(ctx, defaultSpec.ID, true); err != nil {
		t.Fatal(err)
	}
	waitPortClosed(t, defaultSpec.FastCGIPort, 10*time.Second)
	waitPortClosed(t, stagedSpec.FastCGIPort, 10*time.Second)
	status, err = b.Status(ctx, defaultSpec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.StatusStopped || status.PID != 0 {
		t.Fatalf("status after Stop = %+v", status)
	}
}

func TestLocalPHPExecUsesGeneratedIni(t *testing.T) {
	root := systemPHP(t)
	b, _ := newTestLocal(t)
	ctx := context.Background()
	spec := phpTestSpec(t, b.cfg.Home, root, "php@8.4.4-r1", freePHPCPort(t))
	if _, err := b.Prepare(ctx, spec); err != nil {
		t.Fatal(err)
	}

	phpBin := filepath.Join(root, "bin", "php")
	result, err := b.Exec(ctx, spec.ID, []string{phpBin, "-r", "echo ini_get('memory_limit');"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "256M" {
		t.Fatalf("Exec = %+v, want memory_limit 256M", result)
	}

	// The build's bin directory is first in PATH, and PHP_INI_SCAN_DIR is
	// cleared so host configuration cannot leak into the run.
	result, err = b.Exec(ctx, spec.ID,
		[]string{phpBin, "-r", `echo getcwd(), "\n", "scan=[", getenv("PHP_INI_SCAN_DIR"), "]";`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(strings.TrimSuffix(result.Stdout, "\n"), "\n", 2)
	wantDir, err := filepath.EvalSymlinks(filepath.Join(spec.BackendPath, spec.DocumentRoot))
	if err != nil {
		t.Fatal(err)
	}
	gotDir, err := filepath.EvalSymlinks(lines[0])
	if err != nil {
		t.Fatalf("getcwd output %q: %v", result.Stdout, err)
	}
	if gotDir != wantDir {
		t.Fatalf("getcwd = %q, want %q", gotDir, wantDir)
	}
	if len(lines) != 2 || lines[1] != "scan=[]" {
		t.Fatalf("PHP_INI_SCAN_DIR was inherited: %q", result.Stdout)
	}

	result, err = b.Exec(ctx, spec.ID, []string{"php", "-r", "echo PHP_BINARY;"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Stdout) != phpBin {
		t.Fatalf("bare php resolved to %q, want %q", result.Stdout, phpBin)
	}
}

func TestLocalEnsureRuntimeBuildDelegates(t *testing.T) {
	build := model.RuntimeBuild{ID: "php@8.4.4-r1", Kind: "php", Version: "8.4.4", Path: "/runtimes/php/8.4.4-r1-linux-x64"}
	fake := &fakeRuntime{build: build}
	b, _ := newTestLocalWithRuntime(t, fake)
	ctx := context.Background()

	got, err := b.EnsureRuntimeBuild(ctx, build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != build.ID || got.Path != build.Path {
		t.Fatalf("EnsureRuntimeBuild = %+v, want %+v", got, build)
	}
	if calls := fake.buildCallsForTest(); len(calls) != 1 || calls[0] != build.ID {
		t.Fatalf("EnsureBuild calls = %v", calls)
	}
	if _, err := b.EnsureRuntimeBuild(ctx, ""); err == nil {
		t.Fatal("EnsureRuntimeBuild accepted an empty build id")
	}

	unconfigured, _ := newTestLocalWithRuntime(t, nil)
	if _, err := unconfigured.EnsureRuntimeBuild(ctx, build.ID); err == nil {
		t.Fatal("EnsureRuntimeBuild succeeded without a runtime provider")
	}
}

func TestWaitPHPHealthUnresponsiveMasterRespectsDeadline(t *testing.T) {
	listener := listenSilentFastCGI(t, 0)
	port := listener.Addr().(*net.TCPAddr).Port

	oldTimeout, oldPoll, oldAttempt := phpHealthTimeout, phpHealthPoll, phpHealthAttemptTimeout
	phpHealthTimeout, phpHealthPoll, phpHealthAttemptTimeout = 300*time.Millisecond, 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() { phpHealthTimeout, phpHealthPoll, phpHealthAttemptTimeout = oldTimeout, oldPoll, oldAttempt })

	script := filepath.Join(t.TempDir(), "health.php")
	result := make(chan error, 1)
	started := time.Now()
	go func() {
		result <- waitPHPHealth(context.Background(), port, script)
	}()

	select {
	case err := <-result:
		elapsed := time.Since(started)
		if err == nil {
			t.Fatal("waitPHPHealth accepted a master that never answers")
		}
		if elapsed > 2*time.Second {
			t.Fatalf("waitPHPHealth took %s, want about %s", elapsed, phpHealthTimeout)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waitPHPHealth error = %v, want context deadline exceeded", err)
		}
		if !strings.Contains(err.Error(), "did not pass") {
			t.Fatalf("waitPHPHealth error = %v, want the readiness timeout message", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitPHPHealth hung on a master that accepts TCP but never answers")
	}
}

func TestLocalPHPStartDoesNotHangOnUnresponsiveMaster(t *testing.T) {
	b, _ := newTestLocal(t)
	ctx := context.Background()

	oldTimeout, oldPoll, oldAttempt := phpHealthTimeout, phpHealthPoll, phpHealthAttemptTimeout
	phpHealthTimeout, phpHealthPoll, phpHealthAttemptTimeout = 400*time.Millisecond, 20*time.Millisecond, 60*time.Millisecond
	t.Cleanup(func() { phpHealthTimeout, phpHealthPoll, phpHealthAttemptTimeout = oldTimeout, oldPoll, oldAttempt })

	build := filepath.Join(t.TempDir(), "php")
	writeExecutable(t, filepath.Join(build, "bin"), "php", "exit 0")
	pidFile := filepath.Join(b.cfg.Home, "fake-fpm.pid")
	writeExecutable(t, filepath.Join(build, "sbin"), "php-fpm",
		fmt.Sprintf("echo $$ > %q\nexec sleep 120", pidFile))

	port := freePHPCPort(t)
	listener := listenSilentFastCGI(t, port)
	spec := phpTestSpec(t, b.cfg.Home, build, "php@8.4.4-r1", port)
	if _, err := b.Prepare(ctx, spec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background(), spec.ID, true) })
	if _, err := b.logs.Writer(spec.ID, "fpm").Write([]byte("hung master marker\n")); err != nil {
		t.Fatal(err)
	}

	type startResult struct {
		info ProcessInfo
		err  error
	}
	result := make(chan startResult, 1)
	started := time.Now()
	go func() {
		info, err := b.Start(ctx, spec.ID)
		result <- startResult{info: info, err: err}
	}()

	select {
	case got := <-result:
		elapsed := time.Since(started)
		if got.err == nil {
			t.Fatalf("Start accepted a master that never answers health: %+v", got.info)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("Start took %s, want about %s", elapsed, phpHealthTimeout)
		}
		if !strings.Contains(got.err.Error(), "did not become healthy") ||
			!strings.Contains(got.err.Error(), "hung master marker") {
			t.Fatalf("Start error = %v, want readiness failure and the log tail", got.err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Start hung on a master that accepts TCP but never answers")
	}

	pid := waitPIDFile(t, pidFile, 2*time.Second)
	waitProcessGone(t, pid, 5*time.Second)
	status, err := b.Status(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.StatusStopped || status.PID != 0 {
		t.Fatalf("status after health hang = %+v", status)
	}
	_ = listener.Close()
	waitPortClosed(t, port, 5*time.Second)
}

// listenSilentFastCGI accepts connections and never reads or answers, modelling
// an FPM master that is listening but wedged.
func listenSilentFastCGI(t *testing.T, port int) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu     sync.Mutex
		conns  []net.Conn
		closed bool
	)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			if closed {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		closed = true
		for _, conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
	})
	return listener
}

func TestLocalPHPHealthFailureStopsMaster(t *testing.T) {
	root := systemPHP(t)
	b, _ := newTestLocal(t)
	ctx := context.Background()
	oldTimeout, oldPoll := phpHealthTimeout, phpHealthPoll
	phpHealthTimeout, phpHealthPoll = 500*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { phpHealthTimeout, phpHealthPoll = oldTimeout, oldPoll })

	spec := phpTestSpec(t, b.cfg.Home, root, "php@8.4.4-r1", freePHPCPort(t))
	if err := os.WriteFile(spec.PHPHealthScript, []byte(`<?php echo "not-ok";`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Prepare(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := b.logs.Writer(spec.ID, "fpm").Write([]byte("health failure marker\n")); err != nil {
		t.Fatal(err)
	}

	if _, err := b.Start(ctx, spec.ID); err == nil {
		t.Fatal("Start accepted a master whose health script failed")
	} else {
		if !strings.Contains(err.Error(), "did not become healthy") || !strings.Contains(err.Error(), "health failure marker") {
			t.Fatalf("readiness error = %v, want health failure and the log tail", err)
		}
	}
	waitPortClosed(t, spec.FastCGIPort, 10*time.Second)
	status, err := b.Status(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != model.StatusStopped || status.PID != 0 {
		t.Fatalf("status after health failure = %+v", status)
	}
}

func TestLocalPHPPrepareValidation(t *testing.T) {
	b, _ := newTestLocal(t)
	ctx := context.Background()
	build := filepath.Join(t.TempDir(), "php")
	writeExecutable(t, filepath.Join(build, "bin"), "php", "exit 0")
	writeExecutable(t, filepath.Join(build, "sbin"), "php-fpm", "exit 0")

	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	health := filepath.Join(dir, "health.php")
	fpm := filepath.Join(dir, "php-fpm.conf")
	for _, path := range []string{ini, health, fpm} {
		if err := os.WriteFile(path, []byte("; test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	valid := func() SiteSpec {
		return SiteSpec{
			ID: "php-validate", Kind: "php", BackendPath: dir, DocumentRoot: ".",
			PHPBuild: "php@8.4.4-r1", PHPBuildPath: build, PHPIniPath: ini,
			FPMConfigPath: fpm, PHPHealthScript: health, FastCGIPort: 43000,
		}
	}

	if _, err := b.Prepare(ctx, valid()); err != nil {
		t.Fatalf("valid spec: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*SiteSpec)
	}{
		{name: "empty build id", mutate: func(s *SiteSpec) { s.PHPBuild = "" }},
		{name: "missing build path", mutate: func(s *SiteSpec) { s.PHPBuildPath = filepath.Join(dir, "absent") }},
		{name: "relative ini", mutate: func(s *SiteSpec) { s.PHPIniPath = "php.ini" }},
		{name: "unclean ini", mutate: func(s *SiteSpec) { s.PHPIniPath = filepath.Join(dir, ".", "php.ini") + "/../php.ini" }},
		{name: "empty fpm config", mutate: func(s *SiteSpec) { s.FPMConfigPath = "" }},
		{name: "relative health script", mutate: func(s *SiteSpec) { s.PHPHealthScript = "health.php" }},
		{name: "port below range", mutate: func(s *SiteSpec) { s.FastCGIPort = 42999 }},
		{name: "port above range", mutate: func(s *SiteSpec) { s.FastCGIPort = 44000 }},
		{name: "mismatched site id", mutate: func(s *SiteSpec) { s.ID = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := valid()
			test.mutate(&spec)
			if _, err := b.Prepare(ctx, spec); err == nil {
				t.Fatalf("Prepare accepted %+v", spec)
			}
		})
	}

	// A missing or non-executable binary must also be rejected.
	for _, rel := range []string{"bin/php", "sbin/php-fpm"} {
		t.Run("missing "+rel, func(t *testing.T) {
			broken := filepath.Join(t.TempDir(), "php")
			writeExecutable(t, filepath.Join(broken, "bin"), "php", "exit 0")
			writeExecutable(t, filepath.Join(broken, "sbin"), "php-fpm", "exit 0")
			if err := os.Remove(filepath.Join(broken, rel)); err != nil {
				t.Fatal(err)
			}
			spec := valid()
			spec.PHPBuildPath = broken
			if _, err := b.Prepare(ctx, spec); err == nil {
				t.Fatal("Prepare accepted a build without " + rel)
			}
		})
	}
	if err := os.Chmod(filepath.Join(build, "bin", "php"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Prepare(ctx, valid()); err == nil {
		t.Fatal("Prepare accepted a non-executable bin/php")
	}

	if _, err := b.PreparePHP(ctx, "php-validate", "", valid()); err == nil {
		t.Fatal("PreparePHP accepted an empty generation")
	}
	if _, err := b.PreparePHP(ctx, "php-validate", "../swap", valid()); err == nil {
		t.Fatal("PreparePHP accepted an unsafe generation")
	}
	if _, err := b.StartPHP(ctx, "unknown", "default"); err == nil {
		t.Fatal("StartPHP accepted an unprepared site")
	}
}

// phpTestSpec writes a base php.ini, a generated php.ini, a rendered
// php-fpm.conf, and health.php under home, and returns the matching spec.
func phpTestSpec(t *testing.T, home, buildRoot, buildID string, port int) SiteSpec {
	t.Helper()
	if port < 43000 || port > 43999 {
		t.Fatalf("phpTestSpec port %d is outside the PHP range", port)
	}
	configDir := filepath.Join(home, "php-test", strconv.Itoa(port))
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	docRoot := filepath.Join(home, "php-test", "public")
	if err := os.MkdirAll(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docRoot, "index.php"), []byte("<?php echo 'index';"), 0o644); err != nil {
		t.Fatal(err)
	}

	baseIni := filepath.Join(configDir, "base.ini")
	if err := os.WriteFile(baseIni, []byte("memory_limit = 128M\ndisplay_errors = Off\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	iniPath := filepath.Join(configDir, "generated.ini")
	if err := os.WriteFile(iniPath, []byte("memory_limit = 256M\ndisplay_errors = On\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	healthPath := filepath.Join(configDir, "health.php")
	if err := os.WriteFile(healthPath, []byte(`<?php echo "ok";`), 0o644); err != nil {
		t.Fatal(err)
	}
	fpmPath := filepath.Join(configDir, "php-fpm.conf")
	rendered, err := phpfpm.Render(phpfpm.Config{
		SiteID:       "php-test",
		BuildRoot:    buildRoot,
		PHPIni:       iniPath,
		ListenHost:   "127.0.0.1",
		ListenPort:   port,
		DocumentRoot: docRoot,
		ErrorLog:     "/proc/self/fd/2",
		PIDFile:      filepath.Join(configDir, "php-fpm.pid"),
		Env:          map[string]string{"APP_ENV": "local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fpmPath, rendered, 0o600); err != nil {
		t.Fatal(err)
	}

	return SiteSpec{
		ID:              "php-test",
		Kind:            string(model.SitePHP),
		BackendPath:     filepath.Join(home, "php-test"),
		DocumentRoot:    "public",
		PHPVersion:      "8.4",
		PHPBuild:        buildID,
		PHPBuildPath:    buildRoot,
		PHPIniPath:      iniPath,
		FPMConfigPath:   fpmPath,
		PHPHealthScript: healthPath,
		FastCGIPort:     port,
		Env:             map[string]string{"APP_ENV": "local"},
	}
}

// fastcgiHealth issues one GET to the site health script over FastCGI.
func fastcgiHealth(ctx context.Context, port int, script string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	client, err := fcgi.Dial(ctx, addr)
	if err != nil {
		return "", err
	}
	defer func() { _ = client.Close() }()
	resp, err := client.Do(ctx, fcgi.Request{
		Params: map[string]string{
			"GATEWAY_INTERFACE": "CGI/1.1",
			"REQUEST_METHOD":    "GET",
			"REQUEST_URI":       "/health.php",
			"SCRIPT_FILENAME":   script,
			"SCRIPT_NAME":       "/health.php",
			"SERVER_PROTOCOL":   "HTTP/1.1",
			"SERVER_SOFTWARE":   "devstack-test",
			"REMOTE_ADDR":       "127.0.0.1",
			"SERVER_ADDR":       "127.0.0.1",
			"SERVER_NAME":       "localhost",
			"SERVER_PORT":       strconv.Itoa(port),
			"DOCUMENT_ROOT":     filepath.Dir(script),
			"QUERY_STRING":      "",
		},
		BodyLength: 0,
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	body := string(data)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("health status %d: %s", resp.StatusCode, strings.TrimSpace(body))
	}
	return strings.TrimSpace(body), nil
}

// freePHPCPort finds a free loopback port inside the PHP backend range.
func freePHPCPort(t *testing.T) int {
	t.Helper()
	for port := 43000; port <= 43999; port++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port
	}
	t.Fatal("no free port in 43000-43999")
	return 0
}

func waitPortClosed(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("port %d stayed open after stop", port)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestExecEnvironmentPHPIncludesRuntimeEnv(t *testing.T) {
	local := &Local{}
	env := local.execEnvironment(preparedSite{spec: SiteSpec{
		Kind:         string(model.SitePHP),
		Env:          map[string]string{"APP_ENV": "local"},
		RuntimeEnv:   map[string]string{"DB_PASSWORD": "s3cret"},
		PHPBuildPath: t.TempDir(),
		PHPIniPath:   filepath.Join(t.TempDir(), "php.ini"),
	}}, nil)
	if env["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("DB_PASSWORD = %q, want runtime env", env["DB_PASSWORD"])
	}
	if env["APP_ENV"] != "local" {
		t.Fatalf("APP_ENV = %q", env["APP_ENV"])
	}
}
