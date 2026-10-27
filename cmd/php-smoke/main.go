//go:build !windows

// Command php-smoke verifies an extracted devstack PHP build by starting its
// php-fpm binary with a temporary configuration and issuing one FastCGI health
// request. It is intended for CI and refuses to run as root.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
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
	phpPortMin = 43000
	phpPortMax = 43999

	smokeTimeout  = 60 * time.Second
	listenTimeout = 15 * time.Second
	healthTimeout = 15 * time.Second
	stopGrace     = 5 * time.Second
)

type options struct {
	root string
	port int
}

func parseOptions(args []string) (options, error) {
	return parseOptionsWithOutput(args, os.Stderr)
}

func parseOptionsWithOutput(args []string, stderr io.Writer) (options, error) {
	flags := flag.NewFlagSet("php-smoke", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		root string
		port int
	)
	flags.StringVar(&root, "root", "", "extracted PHP build prefix")
	flags.IntVar(&port, "port", 0, "loopback FastCGI port (43000-43999)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: php-smoke --root <prefix> --port <43000-43999>")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return options{}, errors.New("--root is required")
	}
	if !filepath.IsAbs(root) {
		return options{}, fmt.Errorf("--root %q must be an absolute path", root)
	}
	if port < phpPortMin || port > phpPortMax {
		return options{}, fmt.Errorf("--port %d is outside %d-%d", port, phpPortMin, phpPortMax)
	}
	return options{root: filepath.Clean(root), port: port}, nil
}

// checkNotRoot is separate so tests can exercise the uid 0 refusal without
// running the process as root.
func checkNotRoot(euid int) error {
	if euid == 0 {
		return errors.New("refusing to run as root; run php-smoke as the unprivileged build user")
	}
	return nil
}

func validateRoot(root string) error {
	if root == "" {
		return errors.New("PHP build root is required")
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("PHP build root %q must be absolute", root)
	}
	root = filepath.Clean(root)
	for _, binary := range []string{"bin/php", "sbin/php-fpm"} {
		path := filepath.Join(root, filepath.FromSlash(binary))
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("PHP build root %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("PHP build root %s is not a regular file", path)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("PHP build root %s is not executable", path)
		}
	}
	extdir := filepath.Join(root, "lib", "php", "extensions")
	info, err := os.Stat(extdir)
	if err != nil {
		return fmt.Errorf("PHP build extension directory %s: %w", extdir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("PHP build extension directory %s is not a directory", extdir)
	}
	return nil
}

func run(args []string) error {
	return runWithEUID(args, os.Geteuid())
}

func runWithEUID(args []string, euid int) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if err := checkNotRoot(euid); err != nil {
		return err
	}
	if err := validateRoot(opts.root); err != nil {
		return err
	}
	if err := smoke(opts); err != nil {
		return err
	}
	fmt.Printf("php-smoke: FastCGI 200 ok on 127.0.0.1:%d\n", opts.port)
	return nil
}

func smoke(opts options) error {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "devstack-php-smoke-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	iniPath := filepath.Join(dir, "php.ini")
	if err := os.WriteFile(iniPath, []byte("[PHP]\nmemory_limit = 64M\ndisplay_errors = On\n"), 0o600); err != nil {
		return fmt.Errorf("write php.ini: %w", err)
	}
	healthPath := filepath.Join(dir, "health.php")
	if err := os.WriteFile(healthPath, []byte(`<?php echo "ok";`), 0o644); err != nil {
		return fmt.Errorf("write health script: %w", err)
	}
	configPath := filepath.Join(dir, "php-fpm.conf")
	rendered, err := phpfpm.Render(phpfpm.Config{
		SiteID:       "smoke",
		BuildRoot:    opts.root,
		PHPIni:       iniPath,
		ListenHost:   "127.0.0.1",
		ListenPort:   opts.port,
		DocumentRoot: dir,
		ErrorLog:     "/proc/self/fd/2",
		PIDFile:      filepath.Join(dir, "php-fpm.pid"),
	})
	if err != nil {
		return fmt.Errorf("render php-fpm.conf: %w", err)
	}
	if err := os.WriteFile(configPath, rendered, 0o600); err != nil {
		return fmt.Errorf("write php-fpm.conf: %w", err)
	}

	argv := phpfpm.Argv(filepath.Join(opts.root, "sbin", "php-fpm"), configPath, iniPath)
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = dir
	command.Env = smokeEnv(opts.root)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	output := &synchronizedBuffer{}
	command.Stdout = output
	command.Stderr = output

	if err := command.Start(); err != nil {
		return fmt.Errorf("start php-fpm: %w", err)
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
		stopGroup(command.Process.Pid, done)
	}
	defer stop()

	fail := func(stage string, cause error) error {
		stop()
		message := fmt.Sprintf("php-smoke: %s: %v", stage, cause)
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

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.port))
	if err := waitListen(ctx, addr, done); err != nil {
		return fail("waiting for php-fpm to listen", err)
	}
	healthCtx, healthCancel := context.WithTimeout(ctx, healthTimeout)
	defer healthCancel()
	if err := health(healthCtx, addr, healthPath, opts.port); err != nil {
		return fail("FastCGI health request", err)
	}
	stop()
	return nil
}

func waitListen(ctx context.Context, addr string, done <-chan struct{}) error {
	deadline := time.Now().Add(listenTimeout)
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
			return fmt.Errorf("php-fpm did not listen on %s within %s: %w", addr, listenTimeout, err)
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

func health(ctx context.Context, addr, script string, port int) error {
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
			"SERVER_SOFTWARE":   "devstack-php-smoke",
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
		return fmt.Errorf("health script returned %q, want %q", body, "ok")
	}
	return nil
}

func smokeEnv(root string) []string {
	binDir := filepath.Join(root, "bin")
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

// stopGroup terminates the master's whole process group, escalating to SIGKILL
// after a bounded grace period.
func stopGroup(pid int, done <-chan struct{}) {
	select {
	case <-done:
		return
	default:
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(stopGrace):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

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

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "php-smoke:", err)
		os.Exit(1)
	}
}
