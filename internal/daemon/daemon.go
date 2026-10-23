// Package daemon wires the long-running devstack services together: the
// SQLite store, logs, privileged helper client, runtime and certificate
// managers, local backend, site lifecycle, doctor, proxy, REST API, and the
// Unix-socket control API.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

	"github.com/AramZahedi/dev-web-stack/internal/api"
	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	runtimemgr "github.com/AramZahedi/dev-web-stack/internal/runtime"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/supervisor"
)

const (
	// DefaultHelperSocket is the production privileged helper socket.
	DefaultHelperSocket = "/run/devstack-helper/helper.sock"

	helperDialTimeout   = 15 * time.Second
	helperRetryInterval = 100 * time.Millisecond
	renewInterval       = 24 * time.Hour
	apiShutdownTimeout  = 5 * time.Second
	socketDialTimeout   = 250 * time.Millisecond
	maxLogTailBytes     = 32 << 10
)

// selfExecutable is a seam so tests can point the backend at a built devstack
// binary instead of the test executable.
var (
	selfExecutable     = os.Executable
	ensureStartTimeout = 10 * time.Second
)

// Options configures one daemon run. Zero ports keep the values loaded from
// config.yaml. Home, when empty, follows config.Load's precedence
// ($DEVSTACK_HOME, then ~/.devstack).
type Options struct {
	Home         string
	HelperSocket string
	HostsPath    string
	HTTPPort     int
	HTTPSPort    int
	UIPort       int
	LogLevel     string
}

// SocketPath returns the daemon's Unix-socket control path below home.
func SocketPath(home string) string {
	return filepath.Join(home, "run", "daemon.sock")
}

// PidPath returns the daemon's pid file path below home.
func PidPath(home string) string {
	return filepath.Join(home, "run", "daemon.pid")
}

// Run starts every devstack service and blocks until ctx is cancelled or a
// listener fails. On return it stops the supervised processes without changing
// their desired state, so sites marked running come back on the next launch.
func Run(ctx context.Context, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}

	logger, err := newLogger(opts.LogLevel)
	if err != nil {
		return err
	}

	cfg, err := config.Load(opts.Home)
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

	paths := config.NewPaths(cfg.Home)
	if err := paths.Ensure(); err != nil {
		return fmt.Errorf("daemon: prepare home: %w", err)
	}

	d := &daemon{
		cfg:        cfg,
		paths:      paths,
		logger:     logger,
		socketPath: SocketPath(cfg.Home),
		pidPath:    PidPath(cfg.Home),
		serveErr:   make(chan error, 2),
	}
	defer d.shutdown()

	st, err := store.Open(paths.DB)
	if err != nil {
		return fmt.Errorf("daemon: open store: %w", err)
	}
	d.store = st
	logStore, err := logs.New(paths.Logs, cfg.LogMaxMB, cfg.LogKeep)
	if err != nil {
		return fmt.Errorf("daemon: open logs: %w", err)
	}
	d.logs = logStore

	helper, err := dialHelper(ctx, opts.HelperSocket, logger)
	if err != nil {
		return err
	}
	d.helper = helper

	ca, err := helper.EnsureCA(ctx)
	if err != nil {
		return fmt.Errorf("daemon: ensure CA: %w", err)
	}
	rootPEM := ca.RootPEM
	if len(rootPEM) == 0 {
		rootPEM = helper.RootPEM()
	}

	selfBin, err := selfExecutable()
	if err != nil {
		return fmt.Errorf("daemon: locate devstack binary: %w", err)
	}
	runtimes := runtimemgr.NewManager(st, cfg.Home, nil)
	local, err := backend.NewLocal(cfg, st, logStore, runtimes, selfBin)
	if err != nil {
		return fmt.Errorf("daemon: local backend: %w", err)
	}
	d.backend = local
	if err := local.InstallCA(ctx, rootPEM); err != nil {
		return fmt.Errorf("daemon: install CA bundle: %w", err)
	}

	certManager := certs.NewManager(st, helper, paths.PKI, nil)
	router := proxy.NewRouter()
	siteService := sites.NewService(sites.Deps{
		Store:   st,
		Backend: local,
		Helper:  helper,
		Certs:   certManager,
		Router:  router,
		Ports:   domain.NewPortAllocator(),
		Config:  cfg,
		Logs:    logStore,
	})
	d.certs = certManager
	d.sites = siteService

	userHome, _ := os.UserHomeDir()
	doctorService := doctor.NewService(doctor.Deps{
		Config:    cfg,
		Home:      userHome,
		Store:     st,
		Helper:    helper,
		Trust:     helper,
		Ports:     portProbe{},
		Runtimes:  runtimeChecker{manager: runtimes},
		Disk:      diskChecker{},
		HostsPath: opts.HostsPath,
	})

	apiServer := api.NewServer(st, siteService, runtimes, doctorService, logStore, helper, api.Config{
		UserHome:     userHome,
		MaxBodyBytes: int64(cfg.MaxRequestBodyMB) << 20,
		Logger:       logger,
	})

	proxyServer := proxy.NewServer(proxy.ServerConfig{
		HTTPAddr:     net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.HTTPPort)),
		HTTPSAddr:    net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.HTTPSPort)),
		Router:       router,
		MaxBodyBytes: int64(cfg.MaxRequestBodyMB) << 20,
		Logger:       logger,
	})
	d.proxy = proxyServer
	if err := proxyServer.Start(context.Background()); err != nil {
		return fmt.Errorf("daemon: start proxy: %w", err)
	}

	if err := d.startAPI(apiServer.Handler()); err != nil {
		return err
	}
	if err := d.writePID(); err != nil {
		return err
	}

	// A previous daemon may have died without stopping its supervised
	// children. Clean those up before starting the desired sites. The pid file
	// above is not a supervisor record and is ignored by CleanupOrphans.
	if err := supervisor.NewManager(paths.Run, logger).CleanupOrphans(); err != nil {
		logger.Warn("daemon: orphan cleanup", "error", err)
	}

	d.persistSettings(ctx)
	if err := siteService.RebuildRoutes(ctx); err != nil {
		return fmt.Errorf("daemon: publish routes: %w", err)
	}
	d.startDesiredSites(ctx)

	logger.Info("daemon started",
		"home", cfg.Home,
		"http", cfg.HTTPPort,
		"https", cfg.HTTPSPort,
		"ui", cfg.UIPort,
		"socket", d.socketPath,
	)

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("daemon shutting down")
			return nil
		case err := <-d.serveErr:
			return err
		case <-ticker.C:
			d.renew(ctx)
		}
	}
}

// daemon owns the resources created by Run so shutdown can release them in
// dependency order.
type daemon struct {
	cfg    config.Config
	paths  config.Paths
	logger *slog.Logger

	store  *store.Store
	logs   *logs.Store
	helper *helperclient.Client

	backend *backend.Local
	certs   *certs.Manager
	sites   *sites.Service
	proxy   *proxy.Server

	apiTCP   *http.Server
	apiUnix  *http.Server
	tcpLn    net.Listener
	unixLn   net.Listener
	serveErr chan error
	socket   bool
	pidFile  bool

	socketPath string
	pidPath    string

	shutdownOnce sync.Once
}

// startAPI binds the loopback dashboard listener and the Unix control socket.
// The Unix socket uses WithSocketAuth so CLI calls bypass the session and CSRF
// checks while still passing the host allowlist.
func (d *daemon) startAPI(handler http.Handler) error {
	tcpLn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(d.cfg.UIPort)))
	if err != nil {
		return fmt.Errorf("daemon: listen on dashboard port %d: %w", d.cfg.UIPort, err)
	}
	d.tcpLn = tcpLn
	d.apiTCP = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go d.serve(d.apiTCP, tcpLn, "api")

	if err := prepareSocketPath(d.socketPath); err != nil {
		return err
	}
	unixLn, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("daemon: listen on socket %s: %w", d.socketPath, err)
	}
	if err := os.Chmod(d.socketPath, 0o600); err != nil {
		_ = unixLn.Close()
		_ = os.Remove(d.socketPath)
		return fmt.Errorf("daemon: set socket permissions: %w", err)
	}
	d.unixLn = unixLn
	d.socket = true
	d.apiUnix = &http.Server{
		Handler:           api.WithSocketAuth(handler),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go d.serve(d.apiUnix, unixLn, "socket")
	return nil
}

func (d *daemon) serve(server *http.Server, listener net.Listener, name string) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		select {
		case d.serveErr <- fmt.Errorf("daemon: %s listener stopped: %w", name, err):
		default:
		}
	}
}

func (d *daemon) writePID() error {
	data := []byte(strconv.Itoa(os.Getpid()) + "\n")
	if err := os.WriteFile(d.pidPath, data, 0o600); err != nil {
		return fmt.Errorf("daemon: write pid file: %w", err)
	}
	if err := os.Chmod(d.pidPath, 0o600); err != nil {
		return fmt.Errorf("daemon: set pid file permissions: %w", err)
	}
	d.pidFile = true
	return nil
}

// persistSettings mirrors the effective machine configuration into the store so
// GET /api/v1/settings reports the values the daemon is actually using.
func (d *daemon) persistSettings(ctx context.Context) {
	values := map[string]string{
		"home":                  d.cfg.Home,
		"wsl_distro":            d.cfg.WSLDistro,
		"http_port":             strconv.Itoa(d.cfg.HTTPPort),
		"https_port":            strconv.Itoa(d.cfg.HTTPSPort),
		"ui_port":               strconv.Itoa(d.cfg.UIPort),
		"auto_start_sites":      strconv.FormatBool(d.cfg.AutoStartSites),
		"graceful_stop_seconds": strconv.Itoa(d.cfg.GracefulStopSeconds),
		"max_request_body_mb":   strconv.Itoa(d.cfg.MaxRequestBodyMB),
		"log_max_mb":            strconv.Itoa(d.cfg.LogMaxMB),
		"log_keep":              strconv.Itoa(d.cfg.LogKeep),
	}
	for key, value := range values {
		if err := d.store.SetSetting(ctx, key, value); err != nil {
			d.logger.Warn("daemon: persist setting", "key", key, "error", err)
		}
	}
}

// startDesiredSites reconciles the persisted desired state. A failure in one
// site is logged and does not prevent the others or the daemon from starting.
func (d *daemon) startDesiredSites(ctx context.Context) {
	stored, err := d.store.ListSites(ctx)
	if err != nil {
		d.logger.Error("daemon: list sites for startup", "error", err)
		return
	}
	for _, site := range stored {
		if site.DesiredState != model.DesiredRunning {
			continue
		}
		if err := d.sites.Start(ctx, site.ID); err != nil {
			d.logger.Error("daemon: start desired site", "site", site.Name, "error", err)
			continue
		}
		d.logger.Info("daemon: site running", "site", site.Name, "id", site.ID)
	}
}

// renew reissues certificates inside the renewal window and republishes routes.
func (d *daemon) renew(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	if renewed, err := d.certs.RenewDue(ctx); err != nil {
		d.logger.Warn("daemon: renew certificates", "error", err)
	} else if len(renewed) != 0 {
		d.logger.Info("daemon: renewed certificates", "count", len(renewed))
	}
	if err := d.sites.RebuildRoutes(ctx); err != nil {
		d.logger.Warn("daemon: republish routes after renewal", "error", err)
	}
}

// shutdown stops the API and proxy, terminates site processes without changing
// their desired state, and releases the remaining resources. It is idempotent.
func (d *daemon) shutdown() {
	d.shutdownOnce.Do(func() {
		apiCtx, cancelAPI := context.WithTimeout(context.Background(), apiShutdownTimeout)
		for _, server := range []*http.Server{d.apiUnix, d.apiTCP} {
			if server == nil {
				continue
			}
			if err := server.Shutdown(apiCtx); err != nil {
				_ = server.Close()
			}
		}
		cancelAPI()

		if d.proxy != nil {
			proxyCtx, cancelProxy := context.WithTimeout(context.Background(), apiShutdownTimeout)
			if err := d.proxy.Stop(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
				d.logger.Warn("daemon: stop proxy", "error", err)
			}
			cancelProxy()
		}

		stopCtx, cancelStop := context.WithTimeout(context.Background(), d.shutdownBudget())
		d.stopBackends(stopCtx)
		cancelStop()

		if d.helper != nil {
			_ = d.helper.Close()
		}
		if d.logs != nil {
			if err := d.logs.Close(); err != nil {
				d.logger.Warn("daemon: close logs", "error", err)
			}
		}
		if d.store != nil {
			if err := d.store.Close(); err != nil {
				d.logger.Warn("daemon: close store", "error", err)
			}
		}

		if d.socket {
			if err := removeIfSocket(d.socketPath); err != nil {
				d.logger.Warn("daemon: remove socket", "error", err)
			}
		}
		if d.pidFile {
			if err := os.Remove(d.pidPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				d.logger.Warn("daemon: remove pid file", "error", err)
			}
		}
	})
}

func (d *daemon) shutdownBudget() time.Duration {
	grace := d.cfg.GracefulStopSeconds
	if grace <= 0 {
		grace = 10
	}
	return time.Duration(grace)*time.Second + 10*time.Second
}

// stopBackends terminates every site whose desired state is running. It
// deliberately bypasses sites.Service.Stop, which would persist DesiredStopped.
func (d *daemon) stopBackends(ctx context.Context) {
	if d.store == nil || d.backend == nil {
		return
	}
	stored, err := d.store.ListSites(ctx)
	if err != nil {
		d.logger.Warn("daemon: list sites for shutdown", "error", err)
		return
	}
	for _, site := range stored {
		if site.DesiredState != model.DesiredRunning {
			continue
		}
		if err := d.backend.Stop(ctx, site.ID, true); err != nil {
			d.logger.Warn("daemon: stop site", "site", site.Name, "error", err)
		}
	}
}

// dialHelper retries until the privileged helper answers. helperclient.Dial is
// lazy, so the retry loop also performs a protocol-status call.
func dialHelper(ctx context.Context, socket string, logger *slog.Logger) (*helperclient.Client, error) {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		socket = DefaultHelperSocket
	}
	deadline := time.Now().Add(helperDialTimeout)
	var lastErr error
	for {
		client, err := helperclient.Dial(socket)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err = client.Status(pingCtx)
			cancel()
			if err == nil {
				return client, nil
			}
			_ = client.Close()
		}
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("daemon: connect to helper: %w", ctxErr)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("daemon: helper at %s is not reachable: %w", socket, lastErr)
		}
		logger.Debug("daemon: waiting for helper", "socket", socket, "error", lastErr)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("daemon: connect to helper: %w", ctx.Err())
		case <-time.After(helperRetryInterval):
		}
	}
}

// prepareSocketPath removes a stale socket but never a live one or a
// non-socket file.
func prepareSocketPath(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("daemon: inspect socket %s: %w", path, err)
	case info.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("daemon: refusing to replace non-socket %s", path)
	}

	conn, dialErr := net.DialTimeout("unix", path, socketDialTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("daemon: socket %s is already in use", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("daemon: remove stale socket %s: %w", path, err)
	}
	return nil
}

func removeIfSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil
	}
	return os.Remove(path)
}

// EnsureRunning returns immediately when the daemon socket is live. Otherwise
// it spawns `devstack daemon run` in a new session, redirects its output to
// <home>/run/daemon.log, and waits up to ten seconds for the socket.
func EnsureRunning(ctx context.Context, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return fmt.Errorf("daemon: load config: %w", err)
	}
	socket := SocketPath(cfg.Home)
	if socketReachable(socket) {
		return nil
	}

	selfBin, err := selfExecutable()
	if err != nil {
		return fmt.Errorf("daemon: locate devstack binary: %w", err)
	}
	args := []string{"daemon", "run", "--home", cfg.Home}
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

	logPath := filepath.Join(cfg.Home, "run", "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("daemon: open daemon log: %w", err)
	}
	if err := os.Chmod(logPath, 0o600); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("daemon: set daemon log permissions: %w", err)
	}

	cmd := exec.Command(selfBin, args...)
	cmd.Dir = cfg.Home
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = daemonEnvironment(cfg.Home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	startErr := cmd.Start()
	closeErr := logFile.Close()
	if startErr != nil {
		return fmt.Errorf("daemon: start background daemon: %w", startErr)
	}
	if closeErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("daemon: close daemon log: %w", closeErr)
	}

	deadline := time.Now().Add(ensureStartTimeout)
	for {
		if socketReachable(socket) {
			// Detach: the daemon must outlive this CLI invocation.
			_ = cmd.Process.Release()
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("daemon: waiting for startup: %w", ctxErr)
		}
		if !processAlive(cmd.Process.Pid) {
			waitErr := cmd.Wait()
			return fmt.Errorf("daemon exited before the socket appeared: %v\n%s", waitErr, tailFile(logPath))
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
			return fmt.Errorf("daemon did not start within %s\n%s", ensureStartTimeout, tailFile(logPath))
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("daemon: waiting for startup: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func socketReachable(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, socketDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// processAlive reports whether pid is a live (non-zombie) process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return !errors.Is(err, syscall.ESRCH)
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	return !(end >= 0 && end+2 < len(text) && text[end+2] == 'Z')
}

func daemonEnvironment(home string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "DEVSTACK_HOME=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "DEVSTACK_HOME="+home)
}

func tailFile(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	offset := int64(0)
	if info.Size() > maxLogTailBytes {
		offset = info.Size() - maxLogTailBytes
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLogTailBytes))
	if err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	tail := strings.TrimSpace(string(data))
	if tail == "" {
		return "(daemon log is empty)"
	}
	return "daemon log tail:\n" + tail
}

func newLogger(level string) (*slog.Logger, error) {
	var parsed slog.Level
	value := strings.TrimSpace(level)
	if value != "" {
		if err := parsed.UnmarshalText([]byte(value)); err != nil {
			return nil, fmt.Errorf("daemon: invalid log level %q", level)
		}
	}
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parsed})
	return slog.New(handler), nil
}

// portProbe implements doctor.PortChecker.
type portProbe struct{}

func (portProbe) Listenable(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return listener.Close()
}

// diskChecker implements doctor.DiskChecker.
type diskChecker struct{}

func (diskChecker) FreeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// runtimeChecker adapts runtime.Manager to doctor.RuntimeChecker.
type runtimeChecker struct {
	manager *runtimemgr.Manager
}

func (c runtimeChecker) VerifyAll(ctx context.Context) error {
	builds, err := c.manager.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, build := range builds {
		if err := c.manager.Verify(ctx, build.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
