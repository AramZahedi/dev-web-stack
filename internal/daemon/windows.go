//go:build windows

package daemon

import (
	"context"
	"crypto/tls"
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
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/api"
	wslbackend "github.com/AramZahedi/dev-web-stack/internal/backend/wsl"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	perfservice "github.com/AramZahedi/dev-web-stack/internal/performance/service"
	"github.com/AramZahedi/dev-web-stack/internal/platform"
	"github.com/AramZahedi/dev-web-stack/internal/ports"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
	"go.yaml.in/yaml/v3"
)

const (
	windowsStartTimeout = 75 * time.Second
	windowsPollInterval = 25 * time.Millisecond
)

// Run owns the Windows daemon's per-user SQLite state and local control
// endpoints. WSL agent/bootstrap and public listeners are composed through
// bootstrapWindows.
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
	if os.Getenv("DEVSTACK_HOME") == "" {
		_ = os.Setenv("DEVSTACK_HOME", cfg.Home)
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

	logStore, err := logs.New(paths.Logs, cfg.LogMaxMB, cfg.LogKeep)
	if err != nil {
		return fmt.Errorf("daemon: open logs: %w", err)
	}

	phpManifest, err := catalog.LoadPHP()
	if err != nil {
		return fmt.Errorf("daemon: load PHP catalog: %w", err)
	}

	var helper *helperclient.Client
	var agentClient *agentclient.Client
	var rootPEM []byte
	defer func() {
		if helper != nil {
			_ = helper.Close()
		}
		if agentClient != nil {
			_ = agentClient.Close()
		}
	}()

	adapter := &helperAdapter{}
	wslBackend := wslbackend.New(nil)
	agentPorts := ports.NewAgentProvider(nil)

	router, publicProxy, err := newWindowsProxy(cfg, logger)
	if err != nil {
		return err
	}
	defer func() { _ = publicProxy.Stop(context.Background()) }()

	certManager := certs.NewManager(st, adapter, paths.PKI, nil)

	siteService := sites.NewService(sites.Deps{
		Store:        st,
		Backend:      wslBackend,
		Helper:       adapter,
		Certs:        certManager,
		Vhosts:       adapter,
		Proxy:        publicProxy,
		Forwarders:   adapter,
		Router:       router,
		Ports:        domain.NewPortAllocator(),
		PortProvider: agentPorts,
		Config:       cfg,
		Logs:         logStore,
		PHP:          sites.NewPHPCommands(),
		PHPBackend:   wslBackend,
		PHPManifest:  &phpManifest,
	})

	agentConfig := &windowsAgentConfigAdapter{}
	serviceConfigService, err := servicecontrol.NewConfigService(servicecontrol.ConfigDeps{
		Root:    cfg.Home,
		Catalog: services.DefaultCatalog(),
		Events:  st,
		Agent:   agentConfig,
		Logger:  logger,
	})
	if err != nil {
		logger.Warn("daemon: service config manager init", "error", err)
	}

	performanceService := perfservice.NewService(perfservice.ServiceConfig{
		Root:              cfg.Home,
		Store:             st,
		Sites:             siteService,
		HTTPPort:          cfg.HTTPPort,
		HTTPSPort:         cfg.HTTPSPort,
		ServingMode:       cfg.ServingMode,
		DisableHostTuning: true,
		Logger:            logger,
	})

	apiServer := api.NewServer(st, siteService, nil, nil, logStore, adapter, api.Config{
		UserHome:      cfg.Home,
		MaxBodyBytes:  int64(cfg.MaxRequestBodyMB) << 20,
		Listeners:     api.ListenerPorts{HTTPPort: cfg.HTTPPort, HTTPSPort: cfg.HTTPSPort, UIPort: cfg.UIPort},
		Vhosts:        siteService,
		ServiceConfig: serviceConfigService,
		Performance:   performanceService,
		Logger:        logger,
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

	stopChan := make(chan struct{}, 1)
	controlHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && (r.URL.Path == "/control/stop" || r.URL.Path == "/api/v1/daemon/stop") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stopped":true}` + "\n"))
			go func() {
				time.Sleep(25 * time.Millisecond)
				select {
				case stopChan <- struct{}{}:
				default:
				}
			}()
			return
		}
		if r.Method == http.MethodGet && (r.URL.Path == "/control/status" || r.URL.Path == "/api/v1/daemon/status") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"running":true,"pid":%d}`+"\n", os.Getpid())))
			return
		}
		api.WithSocketAuth(apiServer.Handler()).ServeHTTP(w, r)
	})

	servers := []*http.Server{
		{Handler: apiServer.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute},
		{Handler: controlHandler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute},
	}
	serveErr := make(chan error, len(servers))
	go serveWindows(servers[0], tcpLn, "dashboard", serveErr)
	go serveWindows(servers[1], pipeLn, "control pipe", serveErr)

	steps := windowsBootstrapSteps{
		Helper: func(ctx context.Context) error {
			helperSocket := opts.HelperSocket
			if strings.TrimSpace(helperSocket) == "" {
				helperSocket = DefaultHelperSocket
			}
			client, err := helperclient.Dial(helperSocket)
			if err != nil {
				logger.Warn("daemon: helper service not available or not yet started; continuing without privileged helper", "error", err)
				return nil
			}
			statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err = client.Status(statusCtx)
			cancel()
			if err != nil {
				_ = client.Close()
				logger.Warn("daemon: helper service ping failed; continuing without privileged helper", "error", err)
				return nil
			}
			adapter.set(client)
			helper = client

			if err := updateWindowsProxyTargets(ctx, helperSocket, cfg.HTTPPort, cfg.HTTPSPort); err != nil {
				logger.Warn("daemon: update helper proxy targets", "error", err)
			}

			ca, err := client.EnsureCA(ctx)
			if err != nil {
				logger.Warn("daemon: ensure helper CA", "error", err)
			} else {
				rootPEM = ca.RootPEM
				if len(rootPEM) == 0 {
					rootPEM = client.RootPEM()
				}
				if len(rootPEM) > 0 && wslBackend.Client != nil {
					if err := wslBackend.InstallCA(ctx, rootPEM); err != nil {
						logger.Warn("daemon: install CA bundle into WSL agent", "error", err)
					}
				}
			}
			return nil
		},
		Agent: func(ctx context.Context) error {
			baseURL := fmt.Sprintf("https://127.0.0.1:%d", cfg.AgentPort)
			client, err := loadWindowsAgentClient(cfg.Home, baseURL)
			if err != nil {
				logger.Debug("daemon: WSL agent client credentials not loaded", "error", err)
				return nil
			}
			agentClient = client
			agentConfig.set(client)
			wslBackend.Client = client
			agentPorts.Client = client

			healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			health, err := client.Health(healthCtx)
			cancel()
			if err != nil {
				logger.Warn("daemon: WSL agent health check failed", "error", err)
				if errors.Is(err, agentproto.ErrUnauthenticated) || errors.Is(err, agentproto.ErrCertPinMismatch) {
					return err
				}
			} else {
				logger.Info("daemon: WSL agent connected", "version", health.Version, "distro", health.Distro)
				if len(rootPEM) > 0 {
					if err := wslBackend.InstallCA(ctx, rootPEM); err != nil {
						logger.Warn("daemon: install CA bundle into WSL agent", "error", err)
					}
				}
			}
			return nil
		},
		Proxy: func(ctx context.Context) error {
			_ = wireDashboardTLS(ctx, router, certManager, cfg, logger)
			if err := publicProxy.Start(ctx); err != nil {
				return fmt.Errorf("daemon: start Windows public proxy: %w", err)
			}
			return nil
		},
		Reconcile: func(ctx context.Context) error {
			if err := siteService.ReconcileServingMode(ctx); err != nil {
				logger.Warn("daemon: reconcile Windows serving mode", "error", err)
			}
			if err := siteService.RebuildRoutes(ctx); err != nil {
				logger.Warn("daemon: rebuild routes", "error", err)
			}
			return nil
		},
	}

	if err := bootstrapWindows(ctx, steps); err != nil {
		shutdownWindowsServers(servers)
		return err
	}

	pidPath := PidPath(cfg.Home)
	if err := writePID(cfg.Home, os.Getpid()); err != nil {
		logger.Warn("daemon: write pid file", "error", err)
	}
	defer func() { _ = os.Remove(pidPath) }()

	if err := writeWindowsReady(readyPath, os.Getpid()); err != nil {
		shutdownWindowsServers(servers)
		return err
	}
	defer func() {
		if err := os.Remove(readyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("daemon: remove readiness marker", "error", err)
		}
	}()

	logger.Info("Windows daemon ready", "home", cfg.Home, "dashboard_port", cfg.UIPort, "pipe", SocketPath(cfg.Home))

	select {
	case <-ctx.Done():
	case <-stopChan:
		logger.Info("Windows daemon stopping on control pipe request")
	case err := <-serveErr:
		if err != nil {
			shutdownWindowsServers(servers)
			return err
		}
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
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		helper, err := helperclient.Dial(socket)
		if err == nil {
			defer helper.Close()
			_, err = helper.UpdateProxyTargets(ctx, httpPort, httpsPort)
			return err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return lastErr
}

type helperAdapter struct {
	mu     sync.RWMutex
	client *helperclient.Client
}

func (h *helperAdapter) set(c *helperclient.Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.client = c
}

func (h *helperAdapter) get() *helperclient.Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.client
}

func (h *helperAdapter) SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error) {
	c := h.get()
	if c == nil {
		return helperproto.SignResponse{}, errors.New("helper not connected")
	}
	return c.SignCertificate(ctx, csrPEM, domains)
}

func (h *helperAdapter) RootPEM() []byte {
	c := h.get()
	if c == nil {
		return nil
	}
	return c.RootPEM()
}

func (h *helperAdapter) SyncHosts(ctx context.Context, domains []string) error {
	c := h.get()
	if c == nil {
		return nil
	}
	return c.SyncHosts(ctx, domains)
}

func (h *helperAdapter) InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error) {
	c := h.get()
	if c == nil {
		return nil, errors.New("helper not connected")
	}
	return c.InstallTrust(ctx, userHome)
}

func (h *helperAdapter) RemoveTrust(ctx context.Context, userHome string) ([]trust.Target, error) {
	c := h.get()
	if c == nil {
		return nil, errors.New("helper not connected")
	}
	return c.RemoveTrust(ctx, userHome)
}

func (h *helperAdapter) TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error) {
	c := h.get()
	if c == nil {
		return nil, errors.New("helper not connected")
	}
	return c.TrustStatus(ctx, userHome)
}

func (h *helperAdapter) Status(ctx context.Context) (helperproto.StatusResponse, error) {
	c := h.get()
	if c == nil {
		return helperproto.StatusResponse{}, errors.New("helper not connected")
	}
	return c.Status(ctx)
}

func (h *helperAdapter) VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	c := h.get()
	if c == nil {
		return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin}, nil
	}
	return c.VhostStatus(ctx)
}

func (h *helperAdapter) SyncVhosts(ctx context.Context, req helperproto.VHostSyncRequest) error {
	c := h.get()
	if c == nil {
		return nil
	}
	return c.SyncVhosts(ctx, req)
}

func (h *helperAdapter) SetServingMode(ctx context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
	c := h.get()
	if c == nil {
		return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin}, nil
	}
	return c.SetServingMode(ctx, mode, req)
}

func (h *helperAdapter) RemoveVhosts(ctx context.Context) error {
	c := h.get()
	if c == nil {
		return nil
	}
	return c.RemoveVhosts(ctx)
}

func (h *helperAdapter) SetForwarders(ctx context.Context, httpPort, httpsPort int, enable bool) error {
	c := h.get()
	if c == nil {
		return nil
	}
	return c.SetForwarders(ctx, httpPort, httpsPort, enable)
}

func (h *helperAdapter) UpdateProxyTargets(ctx context.Context, httpPort, httpsPort int) (helperproto.ProxyTargetsResponse, error) {
	c := h.get()
	if c == nil {
		return helperproto.ProxyTargetsResponse{}, errors.New("helper not connected")
	}
	return c.UpdateProxyTargets(ctx, httpPort, httpsPort)
}

func (h *helperAdapter) EnsureCA(ctx context.Context) (helperproto.EnsureCAResponse, error) {
	c := h.get()
	if c == nil {
		return helperproto.EnsureCAResponse{}, errors.New("helper not connected")
	}
	return c.EnsureCA(ctx)
}

func writePID(home string, pid int) error {
	path := PidPath(home)
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return fmt.Errorf("daemon: write pid file %s: %w", path, err)
	}
	return nil
}

func wireDashboardTLS(ctx context.Context, router *proxy.Router, certManager *certs.Manager, cfg config.Config, logger *slog.Logger) error {
	if router == nil || certManager == nil {
		return nil
	}
	dashboardRecord, err := certManager.Ensure(ctx, certs.DashboardOwner, []string{"devstack.test"})
	if err != nil {
		if logger != nil {
			logger.Warn("daemon: ensure dashboard certificate", "error", err)
		}
		return err
	}
	dashboardCert, err := certManager.Load(dashboardRecord)
	if err != nil {
		if logger != nil {
			logger.Warn("daemon: load dashboard certificate", "error", err)
		}
		return err
	}
	upstreamPort := cfg.UIPort
	if upstreamPort == 0 {
		upstreamPort = 7070
	}
	router.Replace(proxy.Snapshot{
		Routes: map[string]proxy.Route{
			"devstack.test": {
				SiteID:   certs.DashboardOwner,
				Host:     "devstack.test",
				Kind:     proxy.KindStatic,
				Upstream: net.JoinHostPort("127.0.0.1", strconv.Itoa(upstreamPort)),
				TLS:      true,
			},
		},
		Certs:       map[string]*tls.Certificate{"devstack.test": dashboardCert},
		DefaultCert: dashboardCert,
	})
	return nil
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

type windowsAgentConfigAdapter struct {
	mu     sync.RWMutex
	client *agentclient.Client
}

func (a *windowsAgentConfigAdapter) set(client *agentclient.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = client
}

func (a *windowsAgentConfigAdapter) get() (*agentclient.Client, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.client == nil {
		return nil, errors.New("WSL agent is not connected")
	}
	return a.client, nil
}

func (a *windowsAgentConfigAdapter) ServiceConfig(ctx context.Context, id string) (agentproto.ServiceConfigResponse, error) {
	c, err := a.get()
	if err != nil {
		return agentproto.ServiceConfigResponse{}, err
	}
	return c.ServiceConfig(ctx, id)
}

func (a *windowsAgentConfigAdapter) ServiceContent(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	c, err := a.get()
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	return c.ServiceContent(ctx, req)
}

func (a *windowsAgentConfigAdapter) ServiceApply(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	c, err := a.get()
	if err != nil {
		return agentproto.ServiceContentResponse{}, err
	}
	return c.ServiceApply(ctx, req)
}

func (a *windowsAgentConfigAdapter) ServiceReset(ctx context.Context, req agentproto.ServiceConfigRequest) error {
	c, err := a.get()
	if err != nil {
		return err
	}
	return c.ServiceReset(ctx, req)
}
