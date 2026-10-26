//go:build windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/api"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/platform"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"go.yaml.in/yaml/v3"
)

const (
	windowsStartTimeout = 75 * time.Second
	windowsPollInterval = 25 * time.Millisecond
)

// Run owns the Windows daemon's per-user SQLite state and local control
// endpoints. WSL agent/bootstrap and public listeners are added by the
// platform wiring that composes on top of this lifecycle.
func Run(ctx context.Context, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := loadWindowsConfig(opts.Home)
	if err != nil {
		return fmt.Errorf("daemon: load config: %w", err)
	}
	if opts.HTTPPort != 0 {
		cfg.HTTPPort = opts.HTTPPort
	}
	if opts.HTTPSPort != 0 {
		cfg.HTTPSPort = opts.HTTPSPort
	}
	if opts.UIPort != 0 {
		cfg.UIPort = opts.UIPort
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("daemon: invalid configuration: %w", err)
	}
	if err := validateWindowsHome(cfg.Home); err != nil {
		return err
	}
	if err := prepareWindowsLayout(cfg.Home); err != nil {
		return err
	}
	lock, err := acquireLifetimeLock(cfg.Home)
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			return fmt.Errorf("%w: %s", ErrAlreadyRunning, cfg.Home)
		}
		return err
	}
	defer lock.release()

	readyPath := ReadyPath(cfg.Home)
	if err := os.Remove(readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemon: remove stale readiness marker: %w", err)
	}

	logPath := filepath.Join(cfg.Home, "logs", "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("daemon: open log %s: %w", logPath, err)
	}
	defer logFile.Close()
	if err := logFile.Chmod(0o600); err != nil {
		return fmt.Errorf("daemon: secure log %s: %w", logPath, err)
	}
	logger := slog.New(slog.NewTextHandler(logFile, nil))
	slog.SetDefault(logger)

	paths := config.NewPaths(cfg.Home)
	st, err := store.Open(paths.DB)
	if err != nil {
		return fmt.Errorf("daemon: open SQLite state on local Windows storage: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Warn("daemon: close SQLite state", "error", err)
		}
	}()

	apiServer := api.NewServer(st, nil, nil, nil, nil, nil, api.Config{
		UserHome:     cfg.Home,
		MaxBodyBytes: int64(cfg.MaxRequestBodyMB) << 20,
		Listeners:    api.ListenerPorts{HTTPPort: cfg.HTTPPort, HTTPSPort: cfg.HTTPSPort, UIPort: cfg.UIPort},
		Logger:       logger,
	})
	tcpLn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(cfg.UIPort)))
	if err != nil {
		return fmt.Errorf("daemon: listen on dashboard port %d: %w", cfg.UIPort, err)
	}
	pipeLn, err := listenControlPipe(cfg.Home)
	if err != nil {
		_ = tcpLn.Close()
		return err
	}
	servers := []*http.Server{
		{Handler: apiServer.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute},
		{Handler: api.WithSocketAuth(apiServer.Handler()), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute},
	}
	serveErr := make(chan error, len(servers))
	go serveWindows(servers[0], tcpLn, "dashboard", serveErr)
	go serveWindows(servers[1], pipeLn, "control pipe", serveErr)
	router, publicProxy, err := newWindowsProxy(cfg, logger)
	if err != nil {
		shutdownWindowsServers(servers)
		return err
	}
	_ = router
	if err := startWindowsPublicEntry(ctx,
		func() error { return publicProxy.Start(ctx) },
		func(ctx context.Context) error { return updateWindowsProxyTargets(ctx, opts.HelperSocket, cfg.HTTPPort, cfg.HTTPSPort) },
		func(context.Context) error { return nil },
	); err != nil {
		_ = publicProxy.Stop(context.Background())
		shutdownWindowsServers(servers)
		return err
	}
	defer func() { _ = publicProxy.Stop(context.Background()) }()
	if err := writeWindowsReady(readyPath, os.Getpid()); err != nil {
		shutdownWindowsServers(servers)
		return err
	}
	logger.Info("Windows daemon ready", "home", cfg.Home, "dashboard_port", cfg.UIPort, "pipe", SocketPath(cfg.Home))

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil {
			shutdownWindowsServers(servers)
			return err
		}
	}
	if err := os.Remove(readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("daemon: remove readiness marker", "error", err)
	}
	shutdownWindowsServers(servers)
	return nil
}

// EnsureRunning starts one detached daemon and waits until it publishes its
// readiness marker. The installer-owned logon task is the durable per-user
// startup mechanism; this function is the CLI/session activation path.
func EnsureRunning(ctx context.Context, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := loadWindowsConfig(opts.Home)
	if err != nil {
		return fmt.Errorf("daemon: load config: %w", err)
	}
	if err := prepareWindowsLayout(cfg.Home); err != nil {
		return err
	}
	if readyWindows(cfg.Home) {
		return nil
	}
	lock, err := acquireSpawnLock(cfg.Home)
	if err != nil {
		if !errors.Is(err, ErrAlreadyRunning) {
			return fmt.Errorf("daemon: acquire spawn lock: %w", err)
		}
		return waitWindowsReady(ctx, cfg.Home, windowsStartTimeout)
	}
	defer lock.release()
	if readyWindows(cfg.Home) {
		return nil
	}
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemon: locate executable: %w", err)
	}
	if opts.Home == "" {
		opts.Home = cfg.Home
	}
	cmd := exec.Command(bin, windowsDaemonArgs(opts)...)
	logFile, err := os.OpenFile(filepath.Join(cfg.Home, "logs", "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("daemon: open daemon log: %w", err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = daemonEnvironment(cfg.Home)
	platform.ConfigureProcess(cmd)
	err = cmd.Start()
	_ = logFile.Close()
	if err != nil {
		return fmt.Errorf("daemon: start background daemon: %w", err)
	}
	_ = cmd.Process.Release()
	return waitWindowsReady(ctx, cfg.Home, windowsStartTimeout)
}

func loadWindowsConfig(home string) (config.Config, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("DEVSTACK_HOME"))
	}
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			legacy := filepath.Join(userHome, ".devstack")
			if fileExists(filepath.Join(legacy, "config.yaml")) || fileExists(filepath.Join(legacy, "state.db")) {
				home = legacy
			}
		}
	}
	if home == "" {
		home = platform.WindowsRoot
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return config.Config{}, fmt.Errorf("resolve Windows home %q: %w", home, err)
	}
	cfg := config.Default()
	cfg.Home = abs
	cfg.ProjectsRoot = filepath.Join(abs, "projects")
	configPath := filepath.Join(abs, "config.yaml")
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return config.Config{}, fmt.Errorf("parse %s: %w", configPath, err)
		}
		cfg.Home = abs
		if cfg.ProjectsRoot == "" {
			cfg.ProjectsRoot = filepath.Join(abs, "projects")
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return config.Config{}, fmt.Errorf("create Windows home: %w", err)
		}
		if err := cfg.Save(); err != nil {
			return config.Config{}, err
		}
	default:
		return config.Config{}, fmt.Errorf("read %s: %w", configPath, err)
	}
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func validateWindowsHome(home string) error {
	if !filepath.IsAbs(home) || filepath.VolumeName(home) == "" || strings.HasPrefix(home, `\\`) {
		return fmt.Errorf("daemon: Windows SQLite home must be an absolute local-drive path, got %q", home)
	}
	isNTFS, err := isNTFS(home)
	if err != nil {
		return fmt.Errorf("daemon: verify local filesystem for SQLite home %q: %w", home, err)
	}
	if !isNTFS {
		return fmt.Errorf("daemon: SQLite home %q must be on an NTFS volume", home)
	}
	return nil
}

func prepareWindowsLayout(home string) error {
	for _, name := range []string{"", "pki", "logs", "run", "cache", "projects"} {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("daemon: create %s: %w", path, err)
		}
	}
	return prepareRunDir(home)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func serveWindows(server *http.Server, ln net.Listener, name string, errs chan<- error) {
	if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		select {
		case errs <- fmt.Errorf("daemon: %s listener stopped: %w", name, err):
		default:
		}
	}
}

func shutdownWindowsServers(servers []*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, server := range servers {
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
		}
	}
}

func daemonEnvironment(home string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(entry), "DEVSTACK_HOME=") {
			env = append(env, entry)
		}
	}
	return append(env, "DEVSTACK_HOME="+home)
}

func windowsDaemonArgs(opts Options) []string {
	args := []string{"daemon", "run", "--home", opts.Home}
	if opts.HelperSocket != "" {
		args = append(args, "--helper-socket", opts.HelperSocket)
	}
	if opts.HostsPath != "" {
		args = append(args, "--hosts", opts.HostsPath)
	}
	if opts.HTTPPort != 0 {
		args = append(args, "--http-port", strconv.Itoa(opts.HTTPPort))
	}
	if opts.HTTPSPort != 0 {
		args = append(args, "--https-port", strconv.Itoa(opts.HTTPSPort))
	}
	if opts.UIPort != 0 {
		args = append(args, "--ui-port", strconv.Itoa(opts.UIPort))
	}
	if opts.LogLevel != "" {
		args = append(args, "--log-level", opts.LogLevel)
	}
	return args
}

func newWindowsProxy(cfg config.Config, logger *slog.Logger) (*proxy.Router, *proxy.Server, error) {
	httpAddr, err := proxy.WindowsPublicListenAddr(cfg.HTTPPort)
	if err != nil {
		return nil, nil, err
	}
	httpsAddr, err := proxy.WindowsPublicListenAddr(cfg.HTTPSPort)
	if err != nil {
		return nil, nil, err
	}
	router := proxy.NewRouter()
	router.Replace(proxy.Snapshot{Routes: map[string]proxy.Route{
		"devstack.test": {
			SiteID: "__dashboard__", Host: "devstack.test", Kind: proxy.KindStatic,
			Upstream: net.JoinHostPort("127.0.0.1", fmt.Sprint(cfg.UIPort)), TLS: true,
		},
	}})
	return router, proxy.NewServer(proxy.ServerConfig{
		HTTPAddr: httpAddr, HTTPSAddr: httpsAddr, Router: router,
		MaxBodyBytes: int64(cfg.MaxRequestBodyMB) << 20, Logger: logger,
	}), nil
}

func startWindowsPublicEntry(ctx context.Context, startProxy func() error,
	updateTargets func(context.Context) error, reconcile func(context.Context) error) error {
	if err := startProxy(); err != nil {
		return fmt.Errorf("daemon: start Windows public proxy: %w", err)
	}
	if err := updateTargets(ctx); err != nil {
		return fmt.Errorf("daemon: update helper proxy targets before reconcile: %w", err)
	}
	if err := reconcile(ctx); err != nil {
		return fmt.Errorf("daemon: reconcile Windows serving mode: %w", err)
	}
	return nil
}

func updateWindowsProxyTargets(ctx context.Context, socket string, httpPort, httpsPort int) error {
	if strings.TrimSpace(socket) == "" {
		socket = DefaultHelperSocket
	}
	helper, err := helperclient.Dial(socket)
	if err != nil {
		return err
	}
	defer helper.Close()
	_, err = helper.UpdateProxyTargets(ctx, httpPort, httpsPort)
	return err
}

func waitWindowsReady(ctx context.Context, home string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(windowsPollInterval)
	defer ticker.Stop()
	for {
		if readyWindows(home) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("daemon: waiting for Windows startup: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("daemon: Windows daemon did not become ready within %s", timeout)
		case <-ticker.C:
		}
	}
}
