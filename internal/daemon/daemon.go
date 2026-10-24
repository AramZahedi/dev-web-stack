// Package daemon wires the long-running devstack services together: the
// SQLite store, logs, privileged helper client, runtime and certificate
// managers, local backend, site lifecycle, doctor, proxy, REST API, and the
// Unix-socket control API.
package daemon

import (
	"bytes"
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
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
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
	apiShutdownTimeout  = 5 * time.Second
	socketDialTimeout   = 250 * time.Millisecond
	maxLogTailBytes     = 32 << 10
)

// renewInterval is a variable so tests can shorten the certificate renewal
// period without sleeping for a day.
var renewInterval = 24 * time.Hour

// selfExecutable is a seam so tests can point the backend at a built devstack
// binary instead of the test executable.
var (
	selfExecutable     = os.Executable
	ensureStartTimeout = 10 * time.Second

	// Seams for the systemd user-unit path. Tests replace these so they never
	// need a live user manager or a real unit file.
	userHomeDir   = os.UserHomeDir
	systemctlUser = func(ctx context.Context, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
		return command.CombinedOutput()
	}
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

// StartTicksPath returns the sidecar that records /proc/<pid>/stat field 22 for
// the daemon pid. CLI stop verifies it before signalling, protecting against a
// reused PID after a stale pid file.
func StartTicksPath(home string) string {
	return filepath.Join(home, "run", "daemon.start")
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
	if err := prepareRunDir(cfg.Home); err != nil {
		return err
	}
	slog.SetDefault(logger)

	// The lifetime lock serialises daemon instances and is held until shutdown
	// has removed this daemon's socket, pid file, and readiness marker.
	lock, err := acquireLifetimeLock(cfg.Home)
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			return fmt.Errorf("%w: %s", ErrAlreadyRunning, cfg.Home)
		}
		return err
	}
	defer lock.release()

	// A marker without the lock is always stale; the previous instance either
	// crashed or is the one that failed to start.
	if err := os.Remove(ReadyPath(cfg.Home)); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("daemon: remove stale readiness marker", "error", err)
	}

	d := &daemon{
		cfg:        cfg,
		logger:     logger,
		socketPath: SocketPath(cfg.Home),
		pidPath:    PidPath(cfg.Home),
		startPath:  StartTicksPath(cfg.Home),
		readyPath:  ReadyPath(cfg.Home),
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
	// Load the embedded PHP manifest once at startup. A malformed manifest is
	// a build error and must fail startup rather than every later PHP call.
	phpManifest, err := catalog.LoadPHP()
	if err != nil {
		return fmt.Errorf("daemon: load PHP catalog: %w", err)
	}

	certManager := certs.NewManager(st, helper, paths.PKI, nil)
	router := proxy.NewRouter()
	proxyServer := proxy.NewServer(proxy.ServerConfig{
		HTTPAddr:     net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.HTTPPort)),
		HTTPSAddr:    net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.HTTPSPort)),
		Router:       router,
		MaxBodyBytes: int64(cfg.MaxRequestBodyMB) << 20,
		Logger:       logger,
	})
	d.proxy = proxyServer
	siteService := sites.NewService(sites.Deps{
		Store:       st,
		Backend:     local,
		Helper:      helper,
		Certs:       certManager,
		Vhosts:      helper,
		Proxy:       proxyServer,
		Router:      router,
		Ports:       domain.NewPortAllocator(),
		Config:      cfg,
		Logs:        logStore,
		PHP:         sites.NewPHPCommands(),
		PHPBackend:  local,
		PHPManifest: &phpManifest,
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
		Vhosts:    siteService,
		PHP:       siteService,
		HostsPath: opts.HostsPath,
	})

	apiServer := api.NewServer(st, siteService, runtimes, doctorService, logStore, helper, api.Config{
		UserHome:     userHome,
		MaxBodyBytes: int64(cfg.MaxRequestBodyMB) << 20,
		Vhosts:       siteService,
		Logger:       logger,
	})

	// The built-in proxy owns the loopback HTTP/HTTPS listeners only in
	// builtin mode. External modes let the native server own 80/443.
	if cfg.ServingMode == config.ServingModeBuiltin {
		if err := proxyServer.Start(context.Background()); err != nil {
			return fmt.Errorf("daemon: start proxy: %w", err)
		}
	}

	// Publish the API and socket before touching serving state. A broken helper
	// vhost configuration must never lock the user out of `vhost use builtin`,
	// Doctor, or site removal. The built-in proxy, when applicable, is already
	// listening, so a builtin switch can restore its forwarders safely.
	if err := d.startAPI(apiServer.Handler()); err != nil {
		return err
	}
	if err := d.writePID(); err != nil {
		return err
	}
	if err := siteService.ReconcileServingMode(ctx); err != nil {
		d.reconcileErr = err
		logger.Error("daemon: reconcile serving mode; continuing so the API can repair it", "error", err)
	}

	// A previous daemon may have died without stopping its supervised
	// children. Clean those up before starting the desired sites. The pid file
	// above is not a supervisor record and is ignored by CleanupOrphans.
	if err := supervisor.NewManager(paths.Run, logger).CleanupOrphans(); err != nil {
		logger.Warn("daemon: orphan cleanup", "error", err)
	}

	d.persistSettings(ctx)
	if cfg.ServingMode == config.ServingModeBuiltin {
		if err := siteService.RebuildRoutes(ctx); err != nil {
			return fmt.Errorf("daemon: publish routes: %w", err)
		}
	}
	d.startDesiredSites(ctx)
	if err := d.writeReady(); err != nil {
		return err
	}

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
	logger *slog.Logger

	store        *store.Store
	logs         *logs.Store
	helper       *helperclient.Client
	reconcileErr error

	backend *backend.Local
	certs   *certs.Manager
	sites   *sites.Service
	proxy   *proxy.Server

	apiTCP   *http.Server
	apiUnix  *http.Server
	serveErr chan error

	socketPath string
	socketInfo os.FileInfo
	pidPath    string
	startPath  string
	pidValue   string
	readyPath  string

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
	info, err := os.Lstat(d.socketPath)
	if err != nil {
		_ = unixLn.Close()
		_ = os.Remove(d.socketPath)
		return fmt.Errorf("daemon: inspect socket: %w", err)
	}
	d.socketInfo = info
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
	value := strconv.Itoa(os.Getpid())
	if err := writeAtomic(d.pidPath, []byte(value+"\n"), 0o600); err != nil {
		return fmt.Errorf("daemon: write pid file: %w", err)
	}
	ticks, err := daemonStartTicks(os.Getpid())
	if err != nil {
		return fmt.Errorf("daemon: record start time: %w", err)
	}
	if err := writeAtomic(d.startPath, []byte(strconv.FormatUint(ticks, 10)+"\n"), 0o600); err != nil {
		return fmt.Errorf("daemon: write start-time file: %w", err)
	}
	d.pidValue = value
	return nil
}

func daemonStartTicks(pid int) (uint64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	closeParen := bytes.LastIndexByte(data, ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return 0, fmt.Errorf("truncated /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(data[closeParen+2:]))
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return 0, fmt.Errorf("truncated /proc/%d/stat", pid)
	}
	return strconv.ParseUint(fields[startTimeIndex], 10, 64)
}

// writeReady publishes the readiness marker only after the proxy, API,
// orphan cleanup, routes, and desired sites are all reconciled.
func (d *daemon) writeReady() error {
	if err := writeAtomic(d.readyPath, []byte(d.pidValue+"\n"), 0o600); err != nil {
		return fmt.Errorf("daemon: write readiness marker: %w", err)
	}
	return nil
}

// persistSettings mirrors the effective machine configuration into the store so
// GET /api/v1/settings reports the values the daemon is actually using.
func (d *daemon) persistSettings(ctx context.Context) {
	servingMode := d.cfg.ServingMode
	if d.sites != nil {
		servingMode = d.sites.ServingMode()
	}
	servingModeError := ""
	if d.reconcileErr != nil {
		servingModeError = d.reconcileErr.Error()
	}
	values := map[string]string{
		"home":                  d.cfg.Home,
		"wsl_distro":            d.cfg.WSLDistro,
		"http_port":             strconv.Itoa(d.cfg.HTTPPort),
		"https_port":            strconv.Itoa(d.cfg.HTTPSPort),
		"ui_port":               strconv.Itoa(d.cfg.UIPort),
		"auto_start_sites":      strconv.FormatBool(d.cfg.AutoStartSites),
		"serving_mode":          servingMode,
		"serving_mode_error":    servingModeError,
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
	renewed, err := d.certs.RenewDue(ctx)
	if err != nil {
		d.logger.Warn("daemon: renew certificates", "error", err)
	}
	if len(renewed) == 0 {
		return
	}
	d.logger.Info("daemon: renewed certificates", "count", len(renewed))
	if d.sites.ServingMode() == config.ServingModeBuiltin {
		if err := d.sites.RebuildRoutes(ctx); err != nil {
			d.logger.Error("daemon: republish routes after renewal", "error", err)
		}
		return
	}
	// The next daily tick is too late to discover a transient helper/reload
	// failure, so retry a few times before reporting the stale on-disk certs.
	for attempt, delay := range []time.Duration{0, 100 * time.Millisecond, 500 * time.Millisecond} {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		if err := d.sites.SyncVhosts(ctx); err == nil {
			return
		} else if attempt == 2 {
			d.logger.Error("daemon: sync vhosts after renewal failed; external servers may serve stale certificates",
				"error", err)
		} else {
			d.logger.Warn("daemon: sync vhosts after renewal; retrying", "attempt", attempt+1, "error", err)
		}
	}
}

// shutdown stops the API and proxy, terminates site processes without changing
// their desired state, and releases the remaining resources. It is idempotent.
func (d *daemon) shutdown() {
	d.shutdownOnce.Do(func() {
		// Retire readiness first. EnsureRunning must not treat a shutting-down
		// daemon as ready while its backends are still terminating.
		if d.readyPath != "" {
			if err := os.Remove(d.readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				d.logger.Warn("daemon: remove readiness marker", "error", err)
			}
		}

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

		// The Run context is already cancelled at this point. Backends need
		// their own budgets, so detach from cancellation and bound each stop.
		d.stopBackends(context.WithoutCancel(context.Background()))

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

		if err := removeOwnedSocket(d.socketPath, d.socketInfo); err != nil {
			d.logger.Warn("daemon: remove socket", "error", err)
		}
		if d.startPath != "" {
			if err := os.Remove(d.startPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				d.logger.Warn("daemon: remove start-time file", "error", err)
			}
		}
		if err := removeOwnedPID(d.pidPath, d.pidValue); err != nil {
			d.logger.Warn("daemon: remove pid file", "error", err)
		}
	})
}

func (d *daemon) perProcessStopBudget() time.Duration {
	grace := d.cfg.GracefulStopSeconds
	if grace <= 0 {
		grace = 10
	}
	return time.Duration(grace)*time.Second + 10*time.Second
}

// stopBackends terminates every supervised process, independent of the
// persisted desired state. StopAll catches processes whose rows were deleted or
// whose state disagrees with the live supervisor; ListSites is attempted first
// only to stop known sites individually with their own budgets.
func (d *daemon) stopBackends(ctx context.Context) {
	if d.backend == nil {
		return
	}
	if d.store != nil {
		stored, err := d.store.ListSites(ctx)
		if err != nil {
			d.logger.Warn("daemon: list sites for shutdown; stopping all supervised processes", "error", err)
		} else {
			for _, site := range stored {
				stopCtx, cancel := context.WithTimeout(ctx, d.perProcessStopBudget())
				if err := d.backend.Stop(stopCtx, site.ID, true); err != nil {
					d.logger.Warn("daemon: stop site", "site", site.Name, "error", err)
				}
				cancel()
			}
		}
	}
	if err := d.backend.StopAll(ctx); err != nil {
		d.logger.Warn("daemon: stop all supervised processes", "error", err)
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

// prepareSocketPath removes a stale socket but never a live one, a non-socket
// file, or a socket whose probe failed for an ambiguous reason (timeout or
// permission). The inode is checked again before removal to avoid racing a
// replacement listener.
func prepareSocketPath(path string) error {
	first, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("daemon: inspect socket %s: %w", path, err)
	case first.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("daemon: refusing to replace non-socket %s", path)
	}

	conn, dialErr := net.DialTimeout("unix", path, socketDialTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("daemon: socket %s is already in use", path)
	}
	if !removableSocketError(dialErr) {
		return fmt.Errorf("daemon: socket %s is not safely removable: %w", path, dialErr)
	}

	second, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("daemon: re-inspect socket %s: %w", path, err)
	case !os.SameFile(first, second):
		return fmt.Errorf("daemon: socket %s changed while checking for staleness", path)
	case second.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("daemon: refusing to replace non-socket %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("daemon: remove stale socket %s: %w", path, err)
	}
	return nil
}

// EnsureRunning returns when a ready daemon owns the machine. Otherwise it
// coordinates through the lifetime lock: a free lock means this invocation may
// spawn one detached `devstack daemon run`, while a busy lock means another
// instance is starting or running and must be waited for. It never reports
// success before the readiness marker exists.
func EnsureRunning(ctx context.Context, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return fmt.Errorf("daemon: load config: %w", err)
	}
	if err := prepareRunDir(cfg.Home); err != nil {
		return err
	}
	home := cfg.Home
	logPath := filepath.Join(home, "run", "daemon.log")
	deadline := time.Now().Add(ensureStartTimeout)

	var spawnLock *lifetimeLock
	for {
		spawnLock, err = acquireSpawnLock(home)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrAlreadyRunning) {
			return fmt.Errorf("daemon: acquire spawn lock: %w", err)
		}
		if daemonReady(home) {
			return nil
		}
		if err := sleepContext(ctx, 25*time.Millisecond); err != nil {
			return fmt.Errorf("daemon: waiting for spawn lock: %w", err)
		}
	}
	defer spawnLock.release()

	if daemonReady(home) {
		return nil
	}
	// Prefer the installed user unit when it is present and this invocation
	// targets the default home and default options. The unit is the durable
	// path across logins and reboots; a custom --home, custom helper socket, or
	// a headless session keeps the detached spawn fallback below.
	if userUnitCompatible(opts) {
		if started, err := startUserDaemonUnit(ctx, home); err != nil {
			return err
		} else if started {
			return waitForUserDaemonUnit(ctx, home)
		}
	}

	var (
		cmd       *exec.Cmd
		waitCh    <-chan error
		childDone bool
		childErr  error
	)
	terminateChild := func() {
		if cmd == nil || childDone {
			return
		}
		terminateCommand(cmd, waitCh)
		childDone = true
	}

	for {
		if daemonReady(home) {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			terminateChild()
			return fmt.Errorf("daemon: waiting for startup: %w", ctxErr)
		}
		if time.Now().After(deadline) {
			terminateChild()
			return fmt.Errorf("daemon did not become ready within %s\n%s", ensureStartTimeout, tailFile(logPath))
		}

		if cmd == nil {
			lock, lockErr := acquireLifetimeLock(home)
			switch {
			case lockErr == nil:
				lock.release()
				if err := os.Remove(ReadyPath(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("daemon: remove stale readiness marker: %w", err)
				}
				cmd, waitCh, err = spawnDaemon(home, opts)
				if err != nil {
					return err
				}
			case errors.Is(lockErr, ErrAlreadyRunning):
				// Another instance is starting or running. Its readiness
				// marker will release this waiter without spawning a duplicate.
			default:
				return fmt.Errorf("daemon: inspect lifetime lock: %w", lockErr)
			}
			if cmd == nil {
				if err := sleepContext(ctx, 25*time.Millisecond); err != nil {
					return fmt.Errorf("daemon: waiting for startup: %w", err)
				}
			}
			continue
		}

		if childDone {
			// Our child lost the race to another daemon. Wait for that daemon
			// instead of treating the duplicate's exit as a failure.
			if lockHeld(home) {
				if err := sleepContext(ctx, 25*time.Millisecond); err != nil {
					return fmt.Errorf("daemon: waiting for startup: %w", err)
				}
				continue
			}
			return fmt.Errorf("daemon exited before becoming ready: %v\n%s", childErr, tailFile(logPath))
		}

		select {
		case err := <-waitCh:
			childDone = true
			childErr = err
			waitCh = nil
		case <-ctx.Done():
			terminateChild()
			return fmt.Errorf("daemon: waiting for startup: %w", ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// userUnitCompatible reports whether the installed unit would reproduce the
// requested daemon invocation. The unit has no flags, so any override means the
// detached spawn is the correct path.
func userUnitCompatible(opts Options) bool {
	if opts.HostsPath != "" || opts.HTTPPort != 0 || opts.HTTPSPort != 0 || opts.UIPort != 0 {
		return false
	}
	if level := strings.TrimSpace(opts.LogLevel); level != "" && !strings.EqualFold(level, "info") {
		return false
	}
	socket := strings.TrimSpace(opts.HelperSocket)
	return socket == "" || socket == DefaultHelperSocket
}

// startUserDaemonUnit starts the installed systemd user unit when that is the
// right owner for this home. It returns false when the caller should use the
// detached spawn fallback (no unit, custom home, or no usable user manager).
func startUserDaemonUnit(ctx context.Context, home string) (bool, error) {
	unitPath, err := userDaemonUnitPath()
	if err != nil {
		// Without a resolvable home there is nothing safe to inspect; preserve
		// the historical detached-spawn behavior.
		return false, nil
	}
	if _, err := os.Stat(unitPath); err != nil {
		return false, nil
	}

	defaultHome, err := defaultDaemonHome()
	if err != nil || filepath.Clean(home) != filepath.Clean(defaultHome) {
		return false, nil
	}
	if !systemctlUserUsable(ctx) {
		return false, nil
	}

	out, err := systemctlUser(ctx, "start", "devstack-daemon.service")
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			detail = ": " + detail
		}
		return false, fmt.Errorf(
			"daemon: start systemd user unit devstack-daemon.service: %w%s; run `systemctl --user status devstack-daemon.service`",
			err, detail,
		)
	}
	return true, nil
}

// waitForUserDaemonUnit waits for a unit-started daemon to publish readiness.
// Unlike the detached path there is no child process to reap or diagnose, so a
// timeout points at the unit journal and the configured home.
func waitForUserDaemonUnit(ctx context.Context, home string) error {
	deadline := time.Now().Add(ensureStartTimeout)
	logPath := filepath.Join(home, "run", "daemon.log")
	for {
		if daemonReady(home) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("daemon: waiting for systemd user unit: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"daemon: systemd user unit did not become ready within %s; inspect `systemctl --user status devstack-daemon.service` and `journalctl --user -u devstack-daemon`\n%s",
				ensureStartTimeout, tailFile(logPath),
			)
		}
		if err := sleepContext(ctx, 25*time.Millisecond); err != nil {
			return fmt.Errorf("daemon: waiting for systemd user unit: %w", err)
		}
	}
}

// userDaemonUnitPath resolves the per-user unit path with the same
// XDG_CONFIG_HOME precedence the installer uses.
func userDaemonUnitPath() (string, error) {
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(configHome) {
		return "", fmt.Errorf("XDG_CONFIG_HOME must be absolute, got %q", configHome)
	}
	return filepath.Join(configHome, "systemd", "user", "devstack-daemon.service"), nil
}

// defaultDaemonHome is the home that the installed unit runs with: the unit
// has no --home/DEVSTACK_HOME override, so it follows ~/.devstack.
func defaultDaemonHome() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".devstack"), nil
}

// systemctlUserUsable distinguishes a live user manager from a headless or
// container session where only the detached fallback is possible.
func systemctlUserUsable(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := systemctlUser(checkCtx, "show-environment")
	return err == nil
}

// spawnDaemon starts the detached daemon process and a bounded Wait observer.
func spawnDaemon(home string, opts Options) (*exec.Cmd, <-chan error, error) {
	selfBin, err := selfExecutable()
	if err != nil {
		return nil, nil, fmt.Errorf("daemon: locate devstack binary: %w", err)
	}
	args := []string{"daemon", "run", "--home", home}
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

	logPath := filepath.Join(home, "run", "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("daemon: open daemon log: %w", err)
	}
	if err := logFile.Chmod(0o600); err != nil {
		_ = logFile.Close()
		return nil, nil, fmt.Errorf("daemon: set daemon log permissions: %w", err)
	}

	cmd := exec.Command(selfBin, args...)
	cmd.Dir = home
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = daemonEnvironment(home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	startErr := cmd.Start()
	closeErr := logFile.Close()
	if startErr != nil {
		return nil, nil, fmt.Errorf("daemon: start background daemon: %w", startErr)
	}
	if closeErr != nil {
		_ = cmd.Process.Kill()
		waitCommand(cmd, 2*time.Second)
		return nil, nil, fmt.Errorf("daemon: close daemon log: %w", closeErr)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	return cmd, waitCh, nil
}

// terminateCommand sends SIGTERM, waits a bounded grace period, then escalates
// to SIGKILL. It never waits on the process without a deadline.
func terminateCommand(cmd *exec.Cmd, waitCh <-chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	if waitCh != nil {
		select {
		case <-waitCh:
			return
		case <-time.After(3 * time.Second):
		}
	}
	_ = cmd.Process.Kill()
	if waitCh == nil {
		waitCommand(cmd, 2*time.Second)
		return
	}
	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
	}
}

// waitCommand waits for an already-started command with a deadline.
func waitCommand(cmd *exec.Cmd, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
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
