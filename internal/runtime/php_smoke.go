package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/fcgi"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
)

const (
	// phpSmokeTimeout bounds the whole registration smoke test.
	phpSmokeTimeout = 60 * time.Second
	// phpSmokeListenTimeout bounds how long the master may take to accept TCP.
	phpSmokeListenTimeout = 15 * time.Second
	// phpSmokeHealthTimeout bounds the FastCGI health request itself.
	phpSmokeHealthTimeout = 15 * time.Second
	// phpSmokeStopGrace is the SIGTERM grace period for the smoke master.
	phpSmokeStopGrace = 5 * time.Second
)

// phpSmoke gates PHP registration. It is a package variable so tests can
// substitute a check that does not launch a real master.
var phpSmoke = smokePHPBuild

// smokePHPBuild verifies an extracted PHP build before it is published: the
// CLI and FPM binaries run, the master starts on a free loopback port, and one
// FastCGI request against a temporary health script returns "ok". The master
// runs in its own process group and is always stopped, including on failure.
func smokePHPBuild(ctx context.Context, buildPath string) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		last = smokePHPBuildOnce(ctx, buildPath)
		if last == nil || ctx.Err() != nil || !isPHPSmokePortError(last) {
			return last
		}
	}
	return last
}

func isPHPSmokePortError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "did not listen") ||
		strings.Contains(message, "exited before listening") ||
		strings.Contains(message, "address already in use")
}

func smokePHPBuildOnce(ctx context.Context, buildPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(buildPath) == "" {
		return errors.New("runtime: php smoke: build path is required")
	}
	phpBin := filepath.Join(buildPath, "bin", "php")
	fpmBin := filepath.Join(buildPath, "sbin", "php-fpm")
	for _, binary := range []string{phpBin, fpmBin} {
		info, err := os.Stat(binary)
		if err != nil {
			return fmt.Errorf("runtime: php smoke: %s: %w", binary, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("runtime: php smoke: %s is not executable", binary)
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, phpSmokeTimeout)
	defer cancel()

	if err := runPHPVersion(runCtx, buildPath, phpBin); err != nil {
		return err
	}
	if err := runPHPVersion(runCtx, buildPath, fpmBin); err != nil {
		return err
	}

	dir, err := os.MkdirTemp("", "devstack-php-smoke-")
	if err != nil {
		return fmt.Errorf("runtime: php smoke: create temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	iniPath := filepath.Join(dir, "php.ini")
	if err := os.WriteFile(iniPath, []byte("; devstack PHP smoke\nmemory_limit = 64M\ndisplay_errors = On\n"), 0o600); err != nil {
		return fmt.Errorf("runtime: php smoke: write php.ini: %w", err)
	}
	healthPath := filepath.Join(dir, "health.php")
	if err := os.WriteFile(healthPath, []byte(`<?php echo "ok";`), 0o644); err != nil {
		return fmt.Errorf("runtime: php smoke: write health script: %w", err)
	}
	port, err := freePHPLoopbackPort()
	if err != nil {
		return err
	}
	configPath := filepath.Join(dir, "php-fpm.conf")
	rendered, err := phpfpm.Render(phpfpm.Config{
		SiteID:       "smoke",
		BuildRoot:    buildPath,
		PHPIni:       iniPath,
		ListenHost:   "127.0.0.1",
		ListenPort:   port,
		DocumentRoot: dir,
		ErrorLog:     "/proc/self/fd/2",
		PIDFile:      filepath.Join(dir, "php-fpm.pid"),
	})
	if err != nil {
		return fmt.Errorf("runtime: php smoke: render php-fpm.conf: %w", err)
	}
	if err := os.WriteFile(configPath, rendered, 0o600); err != nil {
		return fmt.Errorf("runtime: php smoke: write php-fpm.conf: %w", err)
	}

	argv := phpfpm.Argv(fpmBin, configPath, iniPath)
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = dir
	command.Env = phpSmokeEnv(buildPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	output := &synchronizedBuffer{}
	command.Stdout = output
	command.Stderr = output

	if err := command.Start(); err != nil {
		return fmt.Errorf("runtime: php smoke: start php-fpm: %w", err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = command.Wait()
		close(done)
	}()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		stopSmokeProcess(command.Process.Pid, done)
	}
	defer stop()

	fail := func(stage string, err error) error {
		stop()
		message := fmt.Sprintf("runtime: php smoke: %s: %v", stage, err)
		if tail := strings.TrimSpace(output.String()); tail != "" {
			message += "\n" + tail
		}
		select {
		case <-done:
			if waitErr != nil && !errors.Is(waitErr, exec.ErrWaitDelay) {
				message += "\nphp-fpm: " + waitErr.Error()
			}
		default:
		}
		return errors.New(message)
	}

	if err := waitPHPSmokeListen(runCtx, "127.0.0.1:"+strconv.Itoa(port), done); err != nil {
		return fail("waiting for php-fpm to listen", err)
	}
	healthCtx, healthCancel := context.WithTimeout(runCtx, phpSmokeHealthTimeout)
	defer healthCancel()
	if err := phpSmokeHealth(healthCtx, port, healthPath); err != nil {
		return fail("health request", err)
	}
	stop()
	return nil
}

// runPHPVersion runs `binary -v` with the host ini scan directory cleared.
func runPHPVersion(ctx context.Context, buildPath, binary string) error {
	command := exec.CommandContext(ctx, binary, "-v")
	command.Env = phpSmokeEnv(buildPath)
	output, err := command.CombinedOutput()
	message := strings.TrimSpace(string(output))
	if err != nil {
		if message != "" {
			return fmt.Errorf("runtime: php smoke: %s -v: %w: %s", binary, err, message)
		}
		return fmt.Errorf("runtime: php smoke: %s -v: %w", binary, err)
	}
	if message == "" {
		return fmt.Errorf("runtime: php smoke: %s -v printed nothing", binary)
	}
	return nil
}

// phpSmokeEnv clears PHP_INI_SCAN_DIR and puts the build's bin directory first
// on PATH so the extracted tree is the one that runs.
func phpSmokeEnv(buildPath string) []string {
	binDir := filepath.Join(buildPath, "bin")
	env := make([]string, 0, len(os.Environ())+2)
	pathSet := false
	for _, entry := range os.Environ() {
		switch {
		case strings.HasPrefix(entry, "PHP_INI_SCAN_DIR="):
			continue
		case strings.HasPrefix(entry, "PHPRC="):
			continue
		case strings.HasPrefix(entry, "PATH="):
			value := strings.TrimPrefix(entry, "PATH=")
			if value == "" {
				entry = "PATH=" + binDir
			} else {
				entry = "PATH=" + binDir + string(os.PathListSeparator) + value
			}
			pathSet = true
		}
		env = append(env, entry)
	}
	if !pathSet {
		env = append(env, "PATH="+binDir)
	}
	return append(env, "PHP_INI_SCAN_DIR=")
}

// waitPHPSmokeListen polls the master's TCP address and fails fast when it
// exits first.
func waitPHPSmokeListen(ctx context.Context, addr string, done <-chan struct{}) error {
	deadline := time.Now().Add(phpSmokeListenTimeout)
	for {
		select {
		case <-done:
			return errors.New("php-fpm exited before listening")
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("php-fpm did not listen on %s within %s: %w", addr, phpSmokeListenTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return errors.New("php-fpm exited before listening")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// phpSmokeHealth sends one FastCGI GET for the temporary health script.
func phpSmokeHealth(ctx context.Context, port int, script string) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	client, err := fcgi.Dial(ctx, addr)
	if err != nil {
		return err
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
			"SERVER_SOFTWARE":   "devstack-smoke",
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
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health script status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	body := strings.TrimSpace(string(data))
	if body != "ok" {
		return fmt.Errorf("health script returned %q", body)
	}
	return nil
}

// freePHPLoopbackPort reserves and releases a free port inside the PHP backend
// range that php-fpm configuration accepts.
func freePHPLoopbackPort() (int, error) {
	start := time.Now().UnixNano() % 1000
	for offset := int64(0); offset < 1000; offset++ {
		port := 43000 + int((start+offset)%1000)
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			continue
		}
		if err := listener.Close(); err != nil {
			return 0, fmt.Errorf("runtime: php smoke: release port: %w", err)
		}
		return port, nil
	}
	return 0, errors.New("runtime: php smoke: no free port in 43000-43999")
}

// stopSmokeProcess terminates the whole process group, escalating to SIGKILL
// after the grace period.
func stopSmokeProcess(pid int, done <-chan struct{}) {
	select {
	case <-done:
		return
	default:
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(phpSmokeStopGrace):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// synchronizedBuffer collects master output while the process may still write
// to it, so failure messages can be built without a data race.
type synchronizedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len() > 64<<10 {
		return b.buf.String()[b.buf.Len()-(64<<10):]
	}
	return b.buf.String()
}
