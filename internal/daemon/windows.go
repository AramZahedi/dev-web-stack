//go:build windows

package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/api"
	wslbackend "github.com/AramZahedi/dev-web-stack/internal/backend/wsl"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/certs"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	perfservice "github.com/AramZahedi/dev-web-stack/internal/performance/service"
	"github.com/AramZahedi/dev-web-stack/internal/platform"
	"github.com/AramZahedi/dev-web-stack/internal/ports"
	"github.com/AramZahedi/dev-web-stack/internal/proxy"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/windows"
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
	// This process serves exactly cfg.Home. Do not inherit a different
	// DEVSTACK_HOME from the launching shell: the agent credential loader and
	// securestore compare against it and would reject a --home override.
	if err := os.Setenv("DEVSTACK_HOME", cfg.Home); err != nil {
		return fmt.Errorf("daemon: set DEVSTACK_HOME: %w", err)
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

	nodeReleases, err := newNodeReleaseClient(cfg.Home)
	if err != nil {
		return fmt.Errorf("daemon: init node release discovery: %w", err)
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

	helperSocket := opts.HelperSocket
	if strings.TrimSpace(helperSocket) == "" {
		helperSocket = DefaultHelperSocket
	}
	adapter := &helperAdapter{
		socketPath: helperSocket,
		home:       cfg.Home,
		distro:     cfg.WSLDistro,
		nodeTools:  newWindowsToolService(cfg.Home, cfg.WSLDistro, st),
	}
	wslBackend := wslbackend.New(nil)
	agentPorts := ports.NewAgentProvider(nil)

	router, publicProxy, err := newWindowsProxy(cfg, logger)
	if err != nil {
		return err
	}
	defer func() { stopWindowsProxy(publicProxy, logger) }()

	certManager := certs.NewManager(st, adapter, paths.PKI, nil)

	agentBaseURL := fmt.Sprintf("https://127.0.0.1:%d", cfg.AgentPort)
	agentServices := &windowsAgentServiceAdapter{
		home:    cfg.Home,
		baseURL: agentBaseURL,
		distro:  cfg.WSLDistro,
		logger:  logger,
	}

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
		SourceCopier: agentServices,
	})

	agentDocker, err := servicecontrol.NewAgentDocker(agentServices, agent.DefaultAgentRoot)
	if err != nil {
		return fmt.Errorf("daemon: init agent docker: %w", err)
	}

	preparers := make(map[services.ID]servicecontrol.Preparer)
	for _, id := range servicecontrol.AllowedAgentServiceIDs() {
		preparers[services.ID(id)] = agentDocker
	}
	preparers[services.IDPostgreSQL] = agentDocker

	serviceCatalog := services.DefaultCatalog()
	serviceManager := servicecontrol.NewManager(
		cfg.Home,
		st,
		agentDocker,
		serviceCatalog,
		preparers,
	)

	agentConfig := &windowsAgentConfigAdapter{
		home:    cfg.Home,
		baseURL: agentBaseURL,
		distro:  cfg.WSLDistro,
		logger:  logger,
	}
	serviceConfigService, err := servicecontrol.NewConfigService(servicecontrol.ConfigDeps{
		Root:     cfg.Home,
		Catalog:  serviceCatalog,
		Services: serviceManager,
		Events:   st,
		Agent:    agentConfig,
		Logger:   logger,
	})
	if err != nil {
		logger.Warn("daemon: service config manager init", "error", err)
	}

	performanceService := perfservice.NewService(windowsPerformanceServiceConfig(cfg, st, serviceManager, siteService, logger))

	runtimeService := &windowsRuntimeService{
		backend:            wslBackend,
		store:              st,
		agent:              agentServices,
		nodeReleaseService: newNodeReleaseService(nodeReleases),
	}

	buildService := newWindowsBuildService(agentServices, paths)

	terminalService := newWindowsTerminalService(agentServices, logger)

	toolingService := tooling.NewService(tooling.Deps{
		Store:  st,
		Paths:  paths,
		Helper: adapter,
	})

	hostsPath := opts.HostsPath
	if strings.TrimSpace(hostsPath) == "" {
		hostsPath = `C:\Windows\System32\drivers\etc\hosts`
	}

	doctorService := doctor.NewService(doctor.Deps{
		Config:     cfg,
		Platform:   "windows",
		Home:       cfg.Home,
		Store:      st,
		Helper:     adapter,
		Trust:      adapter,
		Ports:      portProbe{},
		Runtimes:   runtimeChecker{},
		Disk:       windowsDiskChecker{},
		Vhosts:     siteService,
		PHP:        siteService,
		HostsPath:  hostsPath,
		ToolBinDir: resolveWindowsToolBinDir(cfg),
		Windows: &windowsDiagnostics{
			runner:  wsl.ExecRunner{},
			backend: wslBackend,
		},
	})

	authToken, err := dashboardAuthToken(cfg.Home, cfg.DashboardAuth)
	if err != nil {
		return err
	}
	apiServer := api.NewServer(st, siteService, runtimeService, doctorService, logStore, adapter, api.Config{
		UserHome:         cfg.Home,
		ProjectsRoot:     cfg.ProjectsRoot,
		MaxBodyBytes:     int64(cfg.MaxRequestBodyMB) << 20,
		AuthToken:        authToken,
		Listeners:        api.ListenerPorts{HTTPPort: cfg.HTTPPort, HTTPSPort: cfg.HTTPSPort, UIPort: cfg.UIPort},
		PublicPortsFixed: true,
		Vhosts:           siteService,
		Services:         serviceManager,
		ServiceConfig:    serviceConfigService,
		Tooling:          toolingService,
		Build:            buildService,
		Terminal:         terminalService,
		Performance:      performanceService,
		WSLFS:            agentServices,
		Logger:           logger,
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

	// The listeners are bound but not serving yet. Mirror the effective
	// settings before the dashboard can accept requests so a concurrent port
	// save cannot be overwritten by this startup snapshot.
	persistEffectiveSettings(ctx, st, cfg, cfg.ServingMode, "", logger)

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
			adapter.set(client)
			helper = client

			statusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err = client.Status(statusCtx)
			cancel()
			if err != nil {
				logger.Warn("daemon: helper service ping failed; continuing without privileged helper", "error", err)
				return nil
			}

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
			agentServices.set(client)
			wslBackend.Client = client
			agentPorts.Client = client

			healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			health, err := client.Health(healthCtx)
			cancel()
			if err != nil && isConnectionRefused(err) {
				distro := cfg.WSLDistro
				if strings.TrimSpace(distro) == "" {
					distro = "devstack"
				}
				wakeWSL(ctx, distro, logger)
				retryCtx, retryCancel := context.WithTimeout(ctx, 5*time.Second)
				health, err = client.Health(retryCtx)
				retryCancel()
			}
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

	distro := cfg.WSLDistro
	if strings.TrimSpace(distro) == "" {
		distro = "devstack"
	}
	go runWSLKeepalive(ctx, distro, logger)

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
		return waitWindowsReady(ctx, cfg.Home, windowsStartTimeout, nil)
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
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	readyErr := waitWindowsReady(ctx, cfg.Home, windowsStartTimeout, waitCh)
	if readyErr != nil {
		// Do not leave a daemon we already reported as failed running in the
		// background; a retry would then hit ErrAlreadyRunning.
		_ = cmd.Process.Kill()
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
		return readyErr
	}
	_ = cmd.Process.Release()
	return nil
}

func loadWindowsConfig(home string) (config.Config, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("DEVSTACK_HOME"))
	}
	if home == "" {
		if legacy, ok := LegacyWindowsHome(); ok {
			home = legacy
		}
	}
	if home == "" {
		home = platform.DefaultRoot("windows")
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

// stopWindowsProxy bounds the public-proxy shutdown. A background context would
// wait forever for a stuck upstream or streaming response, preventing the
// daemon process from exiting.
func stopWindowsProxy(server *proxy.Server, logger *slog.Logger) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil && logger != nil {
		logger.Warn("daemon: stop Windows public proxy", "error", err)
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
			ForceHTTPS: true,
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
	mu                sync.RWMutex
	socketPath        string
	client            *helperclient.Client
	home              string
	distro            string
	nodeTools         *windowsToolService
	removeWSLComposer func(context.Context, string) error
}

func (h *helperAdapter) set(c *helperclient.Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.client = c
}

func (h *helperAdapter) get() *helperclient.Client {
	h.mu.RLock()
	client := h.client
	h.mu.RUnlock()
	if client != nil {
		return client
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.client != nil {
		return h.client
	}
	if h.socketPath != "" {
		c, err := helperclient.Dial(h.socketPath)
		if err == nil {
			h.client = c
			return c
		}
	}
	return nil
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
		return fmt.Errorf("helper is not connected; the managed hosts file was not updated: %w", servicecontrol.ErrHelperUnavailable)
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
		return helperproto.VhostStatusResponse{}, helperproto.ErrNotImplemented
	}
	resp, err := c.VhostStatus(ctx)
	if err != nil {
		var hErr *helperclient.Error
		if errors.As(err, &hErr) && (hErr.Status == http.StatusNotFound || hErr.Status == http.StatusNotImplemented || errors.Is(err, helperproto.ErrNotImplemented)) {
			return helperproto.VhostStatusResponse{}, helperproto.ErrNotImplemented
		}
		if errors.Is(err, helperproto.ErrNotImplemented) {
			return helperproto.VhostStatusResponse{}, helperproto.ErrNotImplemented
		}
		return helperproto.VhostStatusResponse{}, err
	}
	return resp, nil
}

func (h *helperAdapter) SyncVhosts(ctx context.Context, req helperproto.VHostSyncRequest) error {
	c := h.get()
	if c == nil {
		return fmt.Errorf("external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active: %w", helperproto.ErrNotImplemented)
	}
	err := c.SyncVhosts(ctx, req)
	if err != nil {
		var hErr *helperclient.Error
		if errors.As(err, &hErr) && (hErr.Status == http.StatusNotFound || hErr.Status == http.StatusNotImplemented || errors.Is(err, helperproto.ErrNotImplemented)) {
			return fmt.Errorf("external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active: %w", helperproto.ErrNotImplemented)
		}
		return err
	}
	return nil
}

func (h *helperAdapter) SetServingMode(ctx context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
	if mode != config.ServingModeBuiltin {
		c := h.get()
		if c == nil {
			return helperproto.VhostStatusResponse{}, fmt.Errorf("external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active: %w", helperproto.ErrNotImplemented)
		}
		resp, err := c.SetServingMode(ctx, mode, req)
		if err != nil {
			var hErr *helperclient.Error
			if errors.As(err, &hErr) && (hErr.Status == http.StatusNotFound || hErr.Status == http.StatusNotImplemented || errors.Is(err, helperproto.ErrNotImplemented)) {
				return helperproto.VhostStatusResponse{}, fmt.Errorf("external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active: %w", helperproto.ErrNotImplemented)
			}
			return helperproto.VhostStatusResponse{}, err
		}
		return resp, nil
	}
	c := h.get()
	if c == nil {
		return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin, ConfigOK: true}, nil
	}
	resp, err := c.SetServingMode(ctx, mode, req)
	if err != nil {
		var hErr *helperclient.Error
		if errors.As(err, &hErr) && (hErr.Status == http.StatusNotFound || hErr.Status == http.StatusNotImplemented || errors.Is(err, helperproto.ErrNotImplemented)) {
			return helperproto.VhostStatusResponse{Mode: config.ServingModeBuiltin, ConfigOK: true}, nil
		}
		return helperproto.VhostStatusResponse{}, err
	}
	return resp, nil
}

func (h *helperAdapter) RemoveVhosts(ctx context.Context) error {
	c := h.get()
	if c == nil {
		return nil
	}
	err := c.RemoveVhosts(ctx)
	if err != nil {
		var hErr *helperclient.Error
		if errors.As(err, &hErr) && (hErr.Status == http.StatusNotFound || hErr.Status == http.StatusNotImplemented || errors.Is(err, helperproto.ErrNotImplemented)) {
			return nil
		}
		return err
	}
	return nil
}

func (h *helperAdapter) devstackHome() string {
	if strings.TrimSpace(h.home) != "" {
		return h.home
	}
	if home := os.Getenv("DEVSTACK_HOME"); strings.TrimSpace(home) != "" {
		return home
	}
	return platform.DefaultRoot("windows")
}

func (h *helperAdapter) toolShimName(id string) string {
	switch id {
	case helperproto.ToolPHPDefault:
		return "php.cmd"
	case helperproto.ToolComposer:
		return "composer.cmd"
	case helperproto.ToolNodeDefault:
		return "node.cmd"
	default:
		return id + ".cmd"
	}
}

var windowsWSLDistroName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const (
	wslLegacyShare    = `\\wsl$\`
	wslLocalhostShare = `\\wsl.localhost\`
)

// windowsUNCDataRoot builds the UNC view of a WSL share's agent data root.
func windowsUNCDataRoot(share, distro string) string {
	if share != wslLegacyShare && share != wslLocalhostShare {
		return ""
	}
	distro = strings.TrimSpace(distro)
	if !windowsWSLDistroName.MatchString(distro) {
		return ""
	}
	rel := strings.TrimPrefix(filepath.ToSlash(agent.DefaultAgentRoot), "/")
	return share + distro + `\` + strings.ReplaceAll(rel, "/", `\`)
}

// windowsPerformanceRoot returns the UNC view of the agent data root where
// the Docker web servers read their config. The Windows home only holds host
// state; writing includes there leaves the live containers untouched.
//
// The \\wsl$ alias is used because it exists on both the Windows 10 22H2 floor
// and Windows 11, so the root is valid before WSL starts and needs no probe.
func windowsPerformanceRoot(distro string) string {
	return windowsUNCDataRoot(wslLegacyShare, distro)
}

// windowsPerformanceServiceConfig wires the performance service. Managed
// includes and performance backups live beside the Docker services inside the
// WSL agent root, not in the Windows home.
func windowsPerformanceServiceConfig(cfg config.Config, st perfservice.SettingsStore, web perfservice.WebServices, sites perfservice.Sites, logger *slog.Logger) perfservice.ServiceConfig {
	return perfservice.ServiceConfig{
		Root:              windowsPerformanceRoot(cfg.WSLDistro),
		Store:             st,
		Services:          web,
		Sites:             sites,
		HTTPPort:          cfg.HTTPPort,
		HTTPSPort:         cfg.HTTPSPort,
		ServingMode:       cfg.ServingMode,
		DisableHostTuning: true,
		Logger:            logger,
	}
}

func (h *helperAdapter) ToolStatus(ctx context.Context) (helperproto.ToolStatusResponse, error) {
	home := h.devstackHome()
	binDir := filepath.Join(home, "bin")
	var tools []helperproto.ToolStatus
	for _, id := range []string{helperproto.ToolPHPDefault, helperproto.ToolComposer} {
		wrapper := filepath.Join(binDir, h.toolShimName(id))
		status := helperproto.ToolStatus{
			ID:          id,
			WrapperPath: wrapper,
		}
		if _, err := os.Stat(wrapper); err == nil {
			status.Target = wrapper
			status.Installed = true
			status.Managed = true
		}
		tools = append(tools, status)
	}
	if h.nodeTools != nil {
		node, err := h.nodeTools.Status(ctx)
		if err != nil {
			node = helperproto.ToolStatus{
				ID:          helperproto.ToolNodeDefault,
				WrapperPath: filepath.Join(binDir, h.toolShimName(helperproto.ToolNodeDefault)),
				Conflict:    err.Error(),
			}
		}
		tools = append(tools, node)
	}
	return helperproto.ToolStatusResponse{Tools: tools}, nil
}

func (h *helperAdapter) ToolInstall(ctx context.Context, id string, force bool) (helperproto.ToolStatus, error) {
	if id == helperproto.ToolNodeDefault {
		if h.nodeTools == nil {
			return helperproto.ToolStatus{}, fmt.Errorf("%w: node tool shims are not configured", helperproto.ErrInvalidRequest)
		}
		return h.nodeTools.Install(ctx, force)
	}
	home := h.devstackHome()
	_ = tooling.EnsureGlobalWindowsToolShims(home, "")
	wrapper := filepath.Join(home, "bin", h.toolShimName(id))
	return helperproto.ToolStatus{
		ID:          id,
		Installed:   true,
		Managed:     true,
		WrapperPath: wrapper,
		Target:      wrapper,
	}, nil
}

func (h *helperAdapter) ToolRemove(ctx context.Context, id string) (helperproto.ToolStatus, error) {
	if id == helperproto.ToolNodeDefault {
		if h.nodeTools == nil {
			return helperproto.ToolStatus{}, fmt.Errorf("%w: node tool shims are not configured", helperproto.ErrInvalidRequest)
		}
		return h.nodeTools.Remove(ctx)
	}
	home := h.devstackHome()
	if id == helperproto.ToolComposer {
		removeWSL := h.removeWSLComposer
		if removeWSL == nil {
			removeWSL = tooling.RemoveWSLComposer
		}
		if err := removeWSL(ctx, h.distro); err != nil {
			return helperproto.ToolStatus{}, fmt.Errorf("daemon: remove WSL composer: %w", err)
		}
		if err := os.RemoveAll(filepath.Join(home, "tools", "composer")); err != nil {
			return helperproto.ToolStatus{}, fmt.Errorf("daemon: remove composer artifacts: %w", err)
		}
	}
	wrapper := filepath.Join(home, "bin", h.toolShimName(id))
	if err := os.Remove(wrapper); err != nil && !errors.Is(err, os.ErrNotExist) {
		return helperproto.ToolStatus{}, fmt.Errorf("daemon: remove %s wrapper: %w", id, err)
	}
	return helperproto.ToolStatus{
		ID:          id,
		Installed:   false,
		Managed:     false,
		WrapperPath: wrapper,
	}, nil
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

func waitWindowsReady(ctx context.Context, home string, timeout time.Duration, waitCh <-chan error) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(windowsPollInterval)
	defer ticker.Stop()
	logPath := filepath.Join(home, "logs", "daemon.log")
	for {
		if readyWindows(home) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("daemon: waiting for Windows startup: %w", ctx.Err())
		case err, ok := <-waitCh:
			if ok {
				if lockHeld(home) {
					waitCh = nil
					continue
				}
				return fmt.Errorf("daemon: Windows daemon exited before becoming ready: %v\n%s", err, tailWindowsLog(logPath))
			}
			waitCh = nil
		case <-deadline.C:
			return fmt.Errorf("daemon: Windows daemon did not become ready within %s", timeout)
		case <-ticker.C:
		}
	}
}

const maxWindowsLogTailBytes = 4096

func tailWindowsLog(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	offset := int64(0)
	if info.Size() > maxWindowsLogTailBytes {
		offset = info.Size() - maxWindowsLogTailBytes
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWindowsLogTailBytes))
	if err != nil {
		return "(daemon log is unavailable: " + err.Error() + ")"
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "(daemon log is empty)"
	}
	return trimmed
}

func isConnectionRefused(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "actively refused") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connectex") ||
		strings.Contains(msg, "WSL agent is not connected") ||
		strings.Contains(msg, "WSL agent not connected")
}

func wakeWSL(ctx context.Context, distro string, logger *slog.Logger) {
	if strings.TrimSpace(distro) == "" {
		distro = "devstack"
	}
	if logger != nil {
		logger.Info("daemon: waking WSL distro", "distro", distro)
	}
	wakeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(wakeCtx, "wsl.exe", "-d", distro, "-u", "root", "--exec", "/bin/true")
	platform.ConfigureProcess(cmd)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Run()
}

const (
	keepaliveMinBackoff = 250 * time.Millisecond
	keepaliveMaxBackoff = 30 * time.Second
	// A keepalive that stayed up this long was healthy; the next exit starts a
	// fresh backoff instead of inheriting an old failure count.
	keepaliveHealthyRun = 30 * time.Second
)

// keepaliveBackoff returns the restart delay after the given consecutive
// failures. A keepalive that exits immediately (missing distro, WSL disabled,
// or a broken wsl.exe) must not respawn four times a second forever.
func keepaliveBackoff(failures int, ranFor time.Duration) time.Duration {
	if ranFor >= keepaliveHealthyRun {
		return keepaliveMinBackoff
	}
	if failures < 1 {
		failures = 1
	}
	delay := keepaliveMinBackoff
	for i := 1; i < failures && delay < keepaliveMaxBackoff; i++ {
		delay *= 2
	}
	if delay > keepaliveMaxBackoff {
		delay = keepaliveMaxBackoff
	}
	return delay
}

func runWSLKeepalive(ctx context.Context, distro string, logger *slog.Logger) {
	if strings.TrimSpace(distro) == "" {
		distro = "devstack"
	}
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		started := time.Now()
		cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "-u", "root", "--exec", "/bin/sleep", "infinity")
		platform.ConfigureProcess(cmd)
		cmd.Stdin = nil
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			failures++
			delay := keepaliveBackoff(failures, 0)
			if logger != nil {
				if failures == 1 {
					logger.Warn("daemon: start WSL keepalive; retrying", "distro", distro, "error", err, "backoff", delay)
				} else {
					logger.Debug("daemon: WSL keepalive still failing", "distro", distro, "error", err, "backoff", delay)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			continue
		}

		_ = cmd.Wait()
		ranFor := time.Since(started)

		select {
		case <-ctx.Done():
			return
		case <-time.After(keepaliveBackoff(failures+1, ranFor)):
		}
		if ranFor >= keepaliveHealthyRun {
			failures = 0
		} else {
			failures++
		}
		if logger != nil {
			if failures <= 1 {
				logger.Info("daemon: WSL keepalive exited; restarting",
					"distro", distro, "ran_for", ranFor.Round(time.Millisecond), "backoff", keepaliveBackoff(failures, ranFor))
			} else {
				logger.Debug("daemon: WSL keepalive exited; backing off",
					"distro", distro, "ran_for", ranFor.Round(time.Millisecond), "failures", failures)
			}
		}
	}
}

type windowsAgentConfigAdapter struct {
	mu      sync.RWMutex
	home    string
	baseURL string
	distro  string
	logger  *slog.Logger
	client  *agentclient.Client
}

func (a *windowsAgentConfigAdapter) set(client *agentclient.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = client
}

func (a *windowsAgentConfigAdapter) get() (*agentclient.Client, error) {
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client != nil {
		return client, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	if a.home != "" && a.baseURL != "" {
		c, err := loadWindowsAgentClient(a.home, a.baseURL)
		if err == nil {
			a.client = c
			return c, nil
		}
	}
	return nil, errors.New("WSL agent is not connected")
}

func (a *windowsAgentConfigAdapter) ensureAwake(ctx context.Context) (*agentclient.Client, error) {
	c, err := a.get()
	if err == nil {
		healthCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		_, healthErr := c.Health(healthCtx)
		cancel()
		if healthErr == nil {
			return c, nil
		}
	}
	wakeWSL(ctx, a.distro, a.logger)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		c, err = a.get()
		if err != nil {
			continue
		}
		healthCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		_, healthErr := c.Health(healthCtx)
		cancel()
		if healthErr == nil {
			return c, nil
		}
	}
	return a.get()
}

func (a *windowsAgentServiceAdapter) BrowseFS(ctx context.Context, path string) (agentproto.FSListing, error) {
	c, err := a.ensureAwake(ctx)
	if err != nil {
		return agentproto.FSListing{}, err
	}
	return c.BrowseFS(ctx, path)
}

// CopySource performs the verified source-mode move inside the managed distro.
func (a *windowsAgentServiceAdapter) CopySource(ctx context.Context, req agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error) {
	c, err := a.ensureAwake(ctx)
	if err != nil {
		return agentproto.SourceCopyResponse{}, err
	}
	return c.CopySource(ctx, req)
}

func (a *windowsAgentConfigAdapter) ServiceConfig(ctx context.Context, id string) (agentproto.ServiceConfigResponse, error) {
	c, err := a.get()
	if err == nil {
		resp, cfgErr := c.ServiceConfig(ctx, id)
		if cfgErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(cfgErr) {
			return resp, cfgErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr != nil {
		return agentproto.ServiceConfigResponse{}, wakeErr
	}
	return c.ServiceConfig(ctx, id)
}

func (a *windowsAgentConfigAdapter) ServiceContent(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	c, err := a.get()
	if err == nil {
		resp, contentErr := c.ServiceContent(ctx, req)
		if contentErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(contentErr) {
			return resp, contentErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr != nil {
		return agentproto.ServiceContentResponse{}, wakeErr
	}
	return c.ServiceContent(ctx, req)
}

func (a *windowsAgentConfigAdapter) ServiceApply(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	c, err := a.get()
	if err == nil {
		resp, applyErr := c.ServiceApply(ctx, req)
		if applyErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(applyErr) {
			return resp, applyErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr != nil {
		return agentproto.ServiceContentResponse{}, wakeErr
	}
	return c.ServiceApply(ctx, req)
}

func (a *windowsAgentConfigAdapter) ServiceReset(ctx context.Context, req agentproto.ServiceConfigRequest) error {
	c, err := a.get()
	if err == nil {
		resetErr := c.ServiceReset(ctx, req)
		if resetErr == nil {
			return nil
		}
		if !isConnectionRefused(resetErr) {
			return resetErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr != nil {
		return wakeErr
	}
	return c.ServiceReset(ctx, req)
}

type windowsAgentServiceAdapter struct {
	mu      sync.RWMutex
	home    string
	baseURL string
	distro  string
	logger  *slog.Logger
	client  *agentclient.Client
}

func (a *windowsAgentServiceAdapter) set(client *agentclient.Client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = client
}

func (a *windowsAgentServiceAdapter) get() (*agentclient.Client, error) {
	a.mu.RLock()
	client := a.client
	a.mu.RUnlock()
	if client != nil {
		return client, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	if a.home != "" && a.baseURL != "" {
		c, err := loadWindowsAgentClient(a.home, a.baseURL)
		if err == nil {
			a.client = c
			return c, nil
		}
	}
	return nil, errors.New("WSL agent is not connected")
}

func (a *windowsAgentServiceAdapter) ensureAwake(ctx context.Context) (*agentclient.Client, error) {
	c, err := a.get()
	if err == nil {
		healthCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		_, healthErr := c.Health(healthCtx)
		cancel()
		if healthErr == nil {
			return c, nil
		}
	}
	wakeWSL(ctx, a.distro, a.logger)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		c, err = a.get()
		if err != nil {
			continue
		}
		healthCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		_, healthErr := c.Health(healthCtx)
		cancel()
		if healthErr == nil {
			return c, nil
		}
	}
	return a.get()
}

func (a *windowsAgentServiceAdapter) ServiceAction(ctx context.Context, req agentproto.ServiceRequest) (agentproto.ServiceResponse, error) {
	c, err := a.get()
	if err == nil {
		resp, actionErr := c.ServiceAction(ctx, req)
		if actionErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(actionErr) {
			return resp, actionErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr != nil {
		return agentproto.ServiceResponse{}, wakeErr
	}
	return c.ServiceAction(ctx, req)
}

func (a *windowsAgentServiceAdapter) ServiceStatus(ctx context.Context, id string) (agentproto.ServiceResponse, error) {
	c, err := a.get()
	if err == nil {
		resp, statusErr := c.ServiceStatus(ctx, id)
		if statusErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(statusErr) {
			return resp, statusErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr == nil && c != nil {
		resp, statusErr := c.ServiceStatus(ctx, id)
		if statusErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(statusErr) {
			return resp, statusErr
		}
	}
	// Fallback to stopped state so dashboard does not return 500 error.
	return agentproto.ServiceResponse{
		ID:    id,
		State: string(services.StateNotInstalled),
	}, nil
}

func (a *windowsAgentServiceAdapter) ServiceLogs(ctx context.Context, id string, req agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error) {
	c, err := a.get()
	if err == nil {
		resp, logsErr := c.ServiceLogs(ctx, id, req)
		if logsErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(logsErr) {
			return resp, logsErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr != nil {
		return agentproto.ServiceLogsResponse{}, wakeErr
	}
	return c.ServiceLogs(ctx, id, req)
}

func (a *windowsAgentServiceAdapter) ServiceCredential(ctx context.Context, req agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error) {
	c, err := a.get()
	if err == nil {
		resp, credErr := c.ServiceCredential(ctx, req)
		if credErr == nil {
			return resp, nil
		}
		if !isConnectionRefused(credErr) {
			return resp, credErr
		}
	}
	c, wakeErr := a.ensureAwake(ctx)
	if wakeErr != nil {
		return agentproto.ServiceCredentialResponse{}, wakeErr
	}
	return c.ServiceCredential(ctx, req)
}

type windowsDiagnostics struct {
	runner  wsl.Runner
	backend *wslbackend.Backend
}

func (w *windowsDiagnostics) CheckWSL(ctx context.Context, distro string) error {
	runner := w.runner
	if runner == nil {
		runner = wsl.ExecRunner{}
	}
	_, err := runner.Run(ctx, "-d", distro, "--", "true")
	return err
}

func (w *windowsDiagnostics) CheckAgent(ctx context.Context, distro string) error {
	if w.backend == nil || w.backend.Client == nil {
		return errors.New("agent client not configured")
	}
	return w.backend.Health(ctx)
}

func (w *windowsDiagnostics) CheckDocker(ctx context.Context, distro string) error {
	runner := w.runner
	if runner == nil {
		runner = wsl.ExecRunner{}
	}
	_, err := runner.Run(ctx, "-d", distro, "--", "docker", "info")
	return err
}

func (w *windowsDiagnostics) CheckLocalhostForwarding(ctx context.Context, distro string, port int) error {
	runner := w.runner
	if runner == nil {
		runner = wsl.ExecRunner{}
	}
	if port <= 0 {
		port = 8080
	}
	return wsl.ProbeLocalhostForwarding(ctx, runner, distro, port)
}

func (w *windowsDiagnostics) CheckPorts(ctx context.Context, distro string) error {
	if w.backend == nil || w.backend.Client == nil {
		return errors.New("agent client not configured")
	}
	resp, err := w.backend.Client.AllocatePorts(ctx, agentproto.PortRequest{
		SiteID: "doctor-probe",
		Kind:   "http",
	})
	if err != nil {
		return err
	}
	_ = w.backend.Client.ReleasePorts(ctx, agentproto.PortRequest{
		SiteID: "doctor-probe",
	})
	if resp.HTTPPort == 0 {
		return errors.New("allocated port is zero")
	}
	return nil
}

type portProbe struct{}

func (portProbe) Listenable(addr string) error {
	if strings.HasPrefix(addr, ":") {
		if l, err := net.Listen("tcp", "127.0.0.1"+addr); err != nil {
			if isPortInUse(err) {
				return err
			}
		} else {
			_ = l.Close()
		}
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return listener.Close()
}

func isPortInUse(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == 10048 || errno == syscall.EADDRINUSE {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "only one usage of each socket address") ||
		strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "eaddrinuse")
}

type windowsDiskChecker struct{}

func (windowsDiskChecker) FreeBytes(path string) (uint64, error) {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	dir, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(dir, &available, &total, &free); err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx: %w", err)
	}
	return available, nil
}

type runtimeChecker struct{}

func (c runtimeChecker) VerifyAll(ctx context.Context) error {
	return nil
}

type windowsRuntimeService struct {
	backend *wslbackend.Backend
	store   *store.Store
	agent   *windowsAgentServiceAdapter
	nodeReleaseService
	installAgent runtimeInstallAgent
}

func (s *windowsRuntimeService) ensureClient() {
	if s.backend != nil && s.backend.Client == nil && s.agent != nil {
		if c, err := s.agent.get(); err == nil {
			s.backend.Client = c
		}
	}
}

func (s *windowsRuntimeService) List(ctx context.Context) ([]model.RuntimeBuild, error) {
	s.ensureClient()
	if s.backend != nil && s.backend.Client != nil {
		resp, err := s.backend.Client.ListRuntimes(ctx)
		if err == nil {
			builds := make([]model.RuntimeBuild, len(resp.Runtimes))
			for i, r := range resp.Runtimes {
				b := model.RuntimeBuild{
					ID:       r.ID,
					Kind:     r.Kind,
					Version:  r.Version,
					Backend:  model.BackendWSL,
					Path:     r.Path,
					Checksum: r.Checksum,
				}
				builds[i] = b
				if s.store != nil {
					_ = s.store.UpsertRuntime(ctx, b)
				}
			}
			return builds, nil
		}
	}
	if s.agent != nil {
		if c, err := s.agent.ensureAwake(ctx); err == nil && c != nil {
			if s.backend != nil {
				s.backend.Client = c
			}
			if resp, err := c.ListRuntimes(ctx); err == nil {
				builds := make([]model.RuntimeBuild, len(resp.Runtimes))
				for i, r := range resp.Runtimes {
					b := model.RuntimeBuild{
						ID:       r.ID,
						Kind:     r.Kind,
						Version:  r.Version,
						Backend:  model.BackendWSL,
						Path:     r.Path,
						Checksum: r.Checksum,
					}
					builds[i] = b
					if s.store != nil {
						_ = s.store.UpsertRuntime(ctx, b)
					}
				}
				return builds, nil
			}
		}
	}
	if s.store != nil {
		if builds, storeErr := s.store.ListRuntimes(ctx); storeErr == nil && len(builds) > 0 {
			return builds, nil
		}
	}
	return nil, errors.New("WSL agent not connected")
}

func (s *windowsRuntimeService) Install(ctx context.Context, id string) (model.RuntimeBuild, error) {
	if selector, ok := nodeReleaseSelector(id); ok {
		return s.installPinnedNode(ctx, selector)
	}
	s.ensureClient()
	if s.backend == nil || s.backend.Client == nil {
		return model.RuntimeBuild{}, errors.New("WSL agent not connected")
	}
	build, err := s.backend.EnsureRuntimeBuild(ctx, id)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	if s.store != nil {
		_ = s.store.UpsertRuntime(ctx, build)
	}
	return build, nil
}

// installPinnedNode resolves a Node selector through discovery and preflight,
// then asks the WSL agent to install that exact artifact. Preflight failures
// return before the agent is contacted or the store is touched.
func (s *windowsRuntimeService) installPinnedNode(ctx context.Context, selector string) (model.RuntimeBuild, error) {
	preflight, err := s.NodeRelease(ctx, selector)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	artifact, err := nodeReleaseArtifact(preflight.Entry)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	agent, err := s.pinnedNodeInstaller()
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	resp, err := agent.InstallRuntime(ctx, agentproto.RuntimeRequest{Artifact: &artifact})
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	build := model.RuntimeBuild{
		ID:       resp.ID,
		Kind:     resp.Kind,
		Version:  resp.Version,
		Backend:  model.BackendWSL,
		Path:     resp.Path,
		Checksum: resp.Checksum,
	}
	if s.store != nil {
		_ = s.store.UpsertRuntime(ctx, build)
	}
	return build, nil
}

func (s *windowsRuntimeService) pinnedNodeInstaller() (runtimeInstallAgent, error) {
	if s.installAgent != nil {
		return s.installAgent, nil
	}
	s.ensureClient()
	if s.backend == nil || s.backend.Client == nil {
		return nil, errors.New("WSL agent not connected")
	}
	return s.backend.Client, nil
}

func (s *windowsRuntimeService) Remove(ctx context.Context, buildID string) error {
	s.ensureClient()
	if s.backend == nil || s.backend.Client == nil {
		return errors.New("WSL agent not connected")
	}
	if _, err := catalog.ParseID(buildID); err != nil {
		return err
	}
	builds, err := s.List(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, build := range builds {
		if build.ID == buildID {
			found = true
			break
		}
	}
	if !found {
		// DELETE is idempotent; an absent runtime cannot be in use.
		if s.store != nil {
			_ = s.store.DeleteRuntime(ctx, buildID)
		}
		return nil
	}
	if err := s.backend.RemoveRuntime(ctx, buildID); err != nil {
		return err
	}
	if s.store != nil {
		_ = s.store.DeleteRuntime(ctx, buildID)
	}
	return nil
}

func (s *windowsRuntimeService) Available() []catalog.Entry {
	cat, err := catalog.Load()
	if err != nil {
		return nil
	}
	return cat.Entries
}

func resolveWindowsToolBinDir(cfg config.Config) string {
	return filepath.Join(cfg.Home, "bin")
}
