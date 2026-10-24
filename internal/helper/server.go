// Package helper implements the privileged devstack helper's local HTTP
// service. It owns operations that need root: the managed hosts block,
// certificate authority, trust stores, and privileged port forwarders.
package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/forwarder"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/hosts"
	"github.com/AramZahedi/dev-web-stack/internal/vhost"
)

const maxRequestBody = 8 << 20

// Production forwarder ports. Tests override the four ports in Config so they
// can bind unprivileged high ports.
const (
	defaultForwardHTTPPort  = 80
	defaultForwardHTTPSPort = 443
	defaultProxyHTTPPort    = 8080
	defaultProxyHTTPSPort   = 8443
)

// Sentinel errors are re-exported for callers that embed the helper server and
// want to match protocol errors without importing helperproto directly.
var (
	ErrUnauthorized     = helperproto.ErrUnauthorized
	ErrProtocolMismatch = helperproto.ErrProtocolMismatch
	ErrInvalidRequest   = helperproto.ErrInvalidRequest
	ErrNotImplemented   = helperproto.ErrNotImplemented
	ErrInternal         = helperproto.ErrInternal
)

// Config configures a helper Server.
type Config struct {
	// SocketPath is the Unix-domain socket to listen on.
	SocketPath string
	// HostsPath is the hosts file whose managed block is updated.
	HostsPath string
	// AllowUID is the only user permitted to call the helper. The socket is
	// owned by this user and is readable only by its owner.
	AllowUID int
	// AllowHome overrides the home directory that trust operations may
	// modify. When empty, the home directory of AllowUID is resolved with
	// os/user. Tests set this field so they never touch a real home.
	AllowHome string
	// CADir is the directory containing the helper's root CA. When non-empty
	// the CA and trust handlers are wired automatically.
	CADir string
	// TrustDir overrides the system CA anchor directory. When empty the
	// distribution's anchor directory is detected.
	TrustDir string
	// Runner executes system trust-update commands. A nil runner uses
	// os/exec.
	Runner CommandRunner
	// ForwardHTTPPort and ForwardHTTPSPort are the privileged listener ports
	// the helper owns. Zero selects the production defaults, 80 and 443.
	// Tests set high ports so they never need root.
	ForwardHTTPPort  int
	ForwardHTTPSPort int
	// ProxyHTTPPort and ProxyHTTPSPort are the unprivileged loopback proxy
	// ports that the forwarders target. Zero selects 8080 and 8443. The
	// SetForwarders RPC may only name these configured ports.
	ProxyHTTPPort  int
	ProxyHTTPSPort int
	// AutoForward makes Start begin forwarding the configured privileged
	// ports immediately. The helper binary enables it so a service restart or
	// reboot restores 80/443 without another `devstack init`; embedding
	// callers leave it false unless they want that behavior.
	AutoForward bool
	// VhostRoot is the parent directory owning the vhost release tree and TLS
	// material, normally /etc/devstack. When set, the helper vhost manager is
	// wired automatically at <VhostRoot>/vhosts with TLS in <VhostRoot>/tls.
	VhostRoot string
	// VhostStatePath is the helper's vhost state file. It defaults to
	// <VhostRoot>/vhosts.json when empty.
	VhostStatePath string
	// NginxIncludeDir and ApacheIncludeDir override the detected bridge
	// include directories. Empty values auto-detect.
	NginxIncludeDir  string
	ApacheIncludeDir string
	// VhostHTTPPort and VhostHTTPSPort are the listen ports rendered into
	// helper-managed nginx/Apache vhosts. Zero selects the production
	// defaults, 80 and 443. Tests use high ports so a real server can run
	// without root.
	VhostHTTPPort  int
	VhostHTTPSPort int
	// Logger receives server diagnostics. A nil logger uses slog.Default.
	Logger *slog.Logger
	// Service contains the privileged operations exposed by the helper. When
	// CADir is set, the CA and trust hooks are filled automatically; any hook
	// supplied by an embedding caller is left untouched.
	Service Service
}

// Service holds the optional privileged operations exposed over the helper
// protocol. Each function is wired by the command that owns the corresponding
// subsystem; leaving one nil keeps its route visible but unimplemented.
type Service struct {
	CAFingerprint   func() string
	EnsureCA        func(context.Context) (helperproto.EnsureCAResponse, error)
	SignCertificate func(context.Context, helperproto.SignRequest) (helperproto.SignResponse, error)
	InstallTrust    func(context.Context, helperproto.TrustRequest) (helperproto.TrustResponse, error)
	RemoveTrust     func(context.Context, helperproto.TrustRequest) (helperproto.TrustResponse, error)
	TrustStatus     func(context.Context, helperproto.TrustRequest) (helperproto.TrustResponse, error)
	SetForwarders   func(context.Context, helperproto.ForwardersRequest) error
	VhostStatus     func(context.Context) (helperproto.VhostStatusResponse, error)
	VhostSync       func(context.Context, helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error)
	VhostSetMode    func(context.Context, string, helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error)
	VhostRemove     func(context.Context) error
	Uninstall       func(context.Context, helperproto.UninstallRequest) error
}

// Server is a helper RPC server listening on a Unix-domain socket.
type Server struct {
	cfg    Config
	logger *slog.Logger

	authority *Authority
	vhosts    *VhostManager

	hostsMu sync.Mutex

	forwardersMu  sync.Mutex
	forwarders    *forwarder.Set
	forwardersKey string

	startMu    sync.Mutex
	listener   net.Listener
	httpServer *http.Server
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

type peerUIDKey struct{}

// NewServer validates cfg and returns a server that is not yet listening.
func NewServer(cfg Config) (*Server, error) {
	if cfg.SocketPath == "" {
		return nil, errors.New("helper: socket path is empty")
	}
	if cfg.AllowUID < 0 {
		return nil, fmt.Errorf("helper: invalid allowed UID %d", cfg.AllowUID)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, logger: logger}
	if _, _, _, _, err := s.forwarderPorts(); err != nil {
		return nil, fmt.Errorf("helper: %w", err)
	}
	if cfg.CADir != "" {
		authority, err := LoadOrCreateAuthority(cfg.CADir, time.Now)
		if err != nil {
			return nil, fmt.Errorf("helper: load CA: %w", err)
		}
		s.authority = authority
	}
	if cfg.VhostRoot != "" {
		if err := vhost.ValidateListenPorts(cfg.VhostHTTPPort, cfg.VhostHTTPSPort); err != nil {
			return nil, fmt.Errorf("helper: %w", err)
		}
		statePath := cfg.VhostStatePath
		if statePath == "" {
			statePath = filepath.Join(cfg.VhostRoot, "vhosts.json")
		}
		manager := NewVhostManager(filepath.Join(cfg.VhostRoot, "vhosts"), statePath, s.authority, cfg.Runner, logger)
		manager.AllowUID = cfg.AllowUID
		manager.AllowHome = cfg.AllowHome
		manager.TLSDir = filepath.Join(cfg.VhostRoot, "tls")
		manager.NginxIncludeDir = cfg.NginxIncludeDir
		manager.ApacheIncludeDir = cfg.ApacheIncludeDir
		manager.HTTPListenPort = cfg.VhostHTTPPort
		manager.HTTPSListenPort = cfg.VhostHTTPSPort
		manager.Forwarders = func(ctx context.Context, enable bool) error {
			return s.setForwarders(ctx, helperproto.ForwardersRequest{Enable: enable})
		}
		s.vhosts = manager
	}
	s.cfg.Service = s.wireService(s.cfg.Service)
	return s, nil
}

// wireService fills in the privileged handlers that the helper owns. Hooks
// supplied by an embedding caller are left untouched. CA hooks are only wired
// when a CA was loaded; without one they keep returning NOT_IMPLEMENTED.
func (s *Server) wireService(service Service) Service {
	if s.authority != nil {
		if service.CAFingerprint == nil {
			service.CAFingerprint = s.authority.Fingerprint
		}
		if service.EnsureCA == nil {
			service.EnsureCA = s.ensureCA
		}
		if service.SignCertificate == nil {
			service.SignCertificate = s.signCertificate
		}
		if service.InstallTrust == nil {
			service.InstallTrust = s.installTrust
		}
		if service.RemoveTrust == nil {
			service.RemoveTrust = s.removeTrust
		}
		if service.TrustStatus == nil {
			service.TrustStatus = s.trustStatus
		}
	}
	if service.SetForwarders == nil {
		service.SetForwarders = s.setForwarders
	}
	if s.vhosts != nil {
		if service.VhostStatus == nil {
			service.VhostStatus = s.vhosts.Status
		}
		if service.VhostSync == nil {
			service.VhostSync = s.vhosts.Sync
		}
		if service.VhostSetMode == nil {
			service.VhostSetMode = s.vhosts.SetMode
		}
		if service.VhostRemove == nil {
			service.VhostRemove = s.vhosts.Remove
		}
	}
	if service.Uninstall == nil {
		service.Uninstall = s.uninstall
	}
	return service
}

func (s *Server) ensureCA(ctx context.Context) (helperproto.EnsureCAResponse, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.EnsureCAResponse{}, err
	}
	if s.authority == nil {
		return helperproto.EnsureCAResponse{}, helperproto.ErrNotImplemented
	}
	return helperproto.EnsureCAResponse{
		RootPEM:     s.authority.RootPEM(),
		Fingerprint: s.authority.Fingerprint(),
	}, nil
}

func (s *Server) signCertificate(ctx context.Context, req helperproto.SignRequest) (helperproto.SignResponse, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.SignResponse{}, err
	}
	if s.authority == nil {
		return helperproto.SignResponse{}, helperproto.ErrNotImplemented
	}
	if len(req.Domains) == 0 {
		return helperproto.SignResponse{}, fmt.Errorf("%w: at least one domain is required", helperproto.ErrInvalidRequest)
	}
	for _, d := range req.Domains {
		if err := domain.ValidateDomain(d); err != nil {
			return helperproto.SignResponse{}, fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
		}
	}
	certPEM, serial, notAfter, err := s.authority.SignCSR(req.CSR, req.Domains, maxLeafValidity)
	if err != nil {
		if errors.Is(err, errInvalidSigningRequest) {
			return helperproto.SignResponse{}, fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
		}
		return helperproto.SignResponse{}, err
	}
	return helperproto.SignResponse{CertPEM: certPEM, Serial: serial, NotAfter: notAfter}, nil
}

func (s *Server) installTrust(ctx context.Context, req helperproto.TrustRequest) (helperproto.TrustResponse, error) {
	home, err := s.authorizeHome(req.UserHome)
	if err != nil {
		return helperproto.TrustResponse{}, err
	}
	targets, err := s.newTrustManager(home).Install(ctx)
	if err != nil {
		return helperproto.TrustResponse{}, err
	}
	return helperproto.TrustResponse{Targets: targets}, nil
}

func (s *Server) removeTrust(ctx context.Context, req helperproto.TrustRequest) (helperproto.TrustResponse, error) {
	home, err := s.authorizeHome(req.UserHome)
	if err != nil {
		return helperproto.TrustResponse{}, err
	}
	targets, err := s.newTrustManager(home).Remove(ctx)
	if err != nil {
		return helperproto.TrustResponse{}, err
	}
	return helperproto.TrustResponse{Targets: targets}, nil
}

func (s *Server) trustStatus(ctx context.Context, req helperproto.TrustRequest) (helperproto.TrustResponse, error) {
	home, err := s.authorizeHome(req.UserHome)
	if err != nil {
		return helperproto.TrustResponse{}, err
	}
	targets, err := s.newTrustManager(home).Status(ctx)
	if err != nil {
		return helperproto.TrustResponse{}, err
	}
	return helperproto.TrustResponse{Targets: targets}, nil
}

func (s *Server) newTrustManager(home string) *TrustManager {
	return NewTrustManager(s.authority.RootPEM(), home, s.cfg.Runner, s.cfg.TrustDir)
}

// forwarderPorts resolves the configured listen and target ports and rejects
// configurations that could bind a loop onto itself. Listen ports come only
// from the helper's configuration; an RPC cannot choose them.
func (s *Server) forwarderPorts() (httpListen, httpsListen, httpTarget, httpsTarget int, err error) {
	httpListen = s.cfg.ForwardHTTPPort
	if httpListen == 0 {
		httpListen = defaultForwardHTTPPort
	}
	httpsListen = s.cfg.ForwardHTTPSPort
	if httpsListen == 0 {
		httpsListen = defaultForwardHTTPSPort
	}
	httpTarget = s.cfg.ProxyHTTPPort
	if httpTarget == 0 {
		httpTarget = defaultProxyHTTPPort
	}
	httpsTarget = s.cfg.ProxyHTTPSPort
	if httpsTarget == 0 {
		httpsTarget = defaultProxyHTTPSPort
	}
	ports := []struct {
		name string
		port int
	}{
		{"forward_http_port", httpListen},
		{"forward_https_port", httpsListen},
		{"proxy_http_port", httpTarget},
		{"proxy_https_port", httpsTarget},
	}
	for _, p := range ports {
		if p.port < 1 || p.port > 65535 {
			return 0, 0, 0, 0, fmt.Errorf("%s %d must be between 1 and 65535", p.name, p.port)
		}
	}
	if httpListen == httpsListen {
		return 0, 0, 0, 0, fmt.Errorf("forward_http_port and forward_https_port must differ, both are %d", httpListen)
	}
	if httpTarget == httpsTarget {
		return 0, 0, 0, 0, fmt.Errorf("proxy_http_port and proxy_https_port must differ, both are %d", httpTarget)
	}
	if httpListen == httpTarget || httpsListen == httpsTarget || httpListen == httpsTarget || httpsListen == httpTarget {
		return 0, 0, 0, 0, errors.New("forwarder listen and proxy target ports must all differ")
	}
	return httpListen, httpsListen, httpTarget, httpsTarget, nil
}

// setForwarders starts or stops the fixed privileged listeners. The request
// may only name the configured proxy ports; listen ports and targets are
// never taken from the request, so a caller cannot forward arbitrary traffic
// or bind arbitrary privileged ports.
func (s *Server) setForwarders(ctx context.Context, req helperproto.ForwardersRequest) error {
	httpListen, httpsListen, httpTarget, httpsTarget, err := s.forwarderPorts()
	if err != nil {
		return fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
	}
	if req.HTTPPort != 0 && req.HTTPPort != httpTarget {
		return fmt.Errorf("%w: http_port %d does not match the configured proxy HTTP port %d",
			helperproto.ErrInvalidRequest, req.HTTPPort, httpTarget)
	}
	if req.HTTPSPort != 0 && req.HTTPSPort != httpsTarget {
		return fmt.Errorf("%w: https_port %d does not match the configured proxy HTTPS port %d",
			helperproto.ErrInvalidRequest, req.HTTPSPort, httpsTarget)
	}

	s.forwardersMu.Lock()
	defer s.forwardersMu.Unlock()

	if !req.Enable {
		if s.forwarders == nil {
			return nil
		}
		if err := s.forwarders.Stop(ctx); err != nil {
			return fmt.Errorf("stop forwarders: %w", err)
		}
		s.forwarders = nil
		s.forwardersKey = ""
		return nil
	}

	key := fmt.Sprintf("%d>%d|%d>%d", httpListen, httpTarget, httpsListen, httpsTarget)
	if s.forwarders != nil {
		if s.forwardersKey == key {
			return nil
		}
		if err := s.forwarders.Stop(ctx); err != nil {
			return fmt.Errorf("restart forwarders: %w", err)
		}
		s.forwarders = nil
		s.forwardersKey = ""
	}

	set := forwarder.New(
		net.JoinHostPort("127.0.0.1", strconv.Itoa(httpListen)),
		net.JoinHostPort("127.0.0.1", strconv.Itoa(httpsListen)),
		net.JoinHostPort("127.0.0.1", strconv.Itoa(httpTarget)),
		net.JoinHostPort("127.0.0.1", strconv.Itoa(httpsTarget)),
		s.logger,
	)
	if err := set.Start(ctx); err != nil {
		return fmt.Errorf("start forwarders: %w", err)
	}
	s.forwarders = set
	s.forwardersKey = key
	return nil
}

// uninstall reverses the changes the helper owns: privileged listeners, the
// allowed user's trust, and the managed hosts block. Every step is
// best-effort and idempotent; errors from independent steps are joined.
func (s *Server) uninstall(ctx context.Context, _ helperproto.UninstallRequest) error {
	var errs []error

	// Remove managed vhosts before stopping the forwarders so the external
	// server is reloaded without our bridge first.
	if s.vhosts != nil {
		if err := s.vhosts.RemoveForUninstall(ctx); err != nil {
			errs = append(errs, fmt.Errorf("remove vhosts: %w", err))
		}
	}

	s.forwardersMu.Lock()
	stoppedForwarders := s.forwarders != nil
	if s.forwarders != nil {
		if err := s.forwarders.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop forwarders: %w", err))
		}
		s.forwarders = nil
		s.forwardersKey = ""
	}
	s.forwardersMu.Unlock()

	var targets []Target
	if s.authority != nil {
		home, err := s.allowedHome()
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve allowed user home: %w", err))
		} else {
			targets, err = s.newTrustManager(home).Remove(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("remove trust: %w", err))
			}
		}
	}

	hostsCleared := false
	if s.cfg.HostsPath != "" {
		if _, err := s.syncHosts(nil); err != nil {
			errs = append(errs, fmt.Errorf("clear hosts block: %w", err))
		} else {
			hostsCleared = true
		}
	}

	failedTargets := 0
	for _, target := range targets {
		if !target.OK {
			failedTargets++
			s.logger.Warn("helper: trust removal target failed", "target", target.Name, "detail", target.Detail)
		}
	}
	if failedTargets > 0 {
		errs = append(errs, fmt.Errorf("%d trust target(s) could not be removed", failedTargets))
	}
	s.logger.Info("helper: uninstall complete",
		"forwarders_stopped", stoppedForwarders,
		"hosts_cleared", hostsCleared,
		"trust_targets", len(targets),
		"trust_failures", failedTargets)
	return errors.Join(errs...)
}

// authorizeHome rejects trust requests for any directory other than the
// allowed user's home. The helper must never write into another user's
// browser or trust stores.
func (s *Server) authorizeHome(requested string) (string, error) {
	if requested == "" {
		return "", fmt.Errorf("%w: user_home is required", helperproto.ErrInvalidRequest)
	}
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("%w: user_home must be an absolute path", helperproto.ErrInvalidRequest)
	}
	allowed, err := s.allowedHome()
	if err != nil {
		return "", err
	}
	requested = filepath.Clean(requested)
	allowed = filepath.Clean(allowed)
	if requested != allowed {
		return "", fmt.Errorf("%w: user_home does not match the allowed user's home directory", helperproto.ErrUnauthorized)
	}
	return requested, nil
}

// allowedHome returns the home directory that privileged trust operations may
// touch. It is the configured override or the allowed UID's account home.
func (s *Server) allowedHome() (string, error) {
	allowed := s.cfg.AllowHome
	if allowed == "" {
		account, err := user.LookupId(strconv.Itoa(s.cfg.AllowUID))
		if err != nil {
			return "", fmt.Errorf("resolve home directory for UID %d: %w", s.cfg.AllowUID, err)
		}
		allowed = account.HomeDir
	}
	if allowed == "" {
		return "", errors.New("the allowed user has no home directory")
	}
	return filepath.Clean(allowed), nil
}

// Start creates the socket and begins serving. It returns once the listener is
// ready; serving continues in the background until Close is called or ctx is
// cancelled.
func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.listener != nil {
		return errors.New("helper: server already started")
	}
	socketPath := s.cfg.SocketPath
	if err := prepareSocketDir(socketPath, s.cfg.AllowUID); err != nil {
		return err
	}
	if err := removeStaleSocket(socketPath); err != nil {
		return err
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("helper: listen on %s: %w", socketPath, err)
	}
	cleanup := func() {
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("helper: set socket mode: %w", err)
	}
	if err := os.Chown(socketPath, s.cfg.AllowUID, -1); err != nil {
		// Only root can hand a socket to another user. A non-root helper still
		// starts so peer UID authentication can reject the mismatch; its
		// socket necessarily remains owned by the current user.
		if os.Geteuid() == 0 {
			cleanup()
			return fmt.Errorf("helper: set socket owner to UID %d: %w", s.cfg.AllowUID, err)
		}
		s.logger.Warn("helper: cannot change socket owner", "uid", s.cfg.AllowUID, "error", err)
	}

	srv := &http.Server{
		Handler:           s.handler(),
		ConnContext:       s.connContext,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// A systemd service restart must restore the privileged forwarders without
	// waiting for a CLI call. Start binds them before the HTTP server is
	// published so a bind failure is a visible startup failure (and systemd
	// restarts or reports it) rather than a silently missing 80/443 path.
	autoForward := s.cfg.AutoForward
	if autoForward && s.vhosts != nil {
		mode, modeErr := s.vhosts.persistedMode()
		switch {
		case modeErr != nil:
			s.logger.Warn("helper: cannot read vhost state; enabling automatic forwarders", "error", modeErr)
		case mode == vhostModeNginx || mode == vhostModeApache:
			autoForward = false
			s.logger.Info("helper: skipping automatic forwarders because an external server owns the privileged ports", "mode", mode)
		}
	}
	if autoForward {
		httpListen, httpsListen, httpTarget, httpsTarget, portErr := s.forwarderPorts()
		if portErr != nil {
			cleanup()
			return fmt.Errorf("helper: resolve automatic forwarder ports: %w", portErr)
		}
		if err := s.setForwarders(ctx, helperproto.ForwardersRequest{Enable: true}); err != nil {
			cleanup()
			return fmt.Errorf(
				"helper: enable automatic forwarders on %d/%d to %d/%d: %w (check that the ports are free or disable auto_forward)",
				httpListen, httpsListen, httpTarget, httpsTarget, err,
			)
		}
		s.logger.Info("helper: automatic forwarders enabled",
			"http", httpListen, "https", httpsListen,
			"proxy_http", httpTarget, "proxy_https", httpsTarget)
	}

	s.listener = ln
	s.httpServer = srv
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("helper server stopped", "error", err)
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	return nil
}

// Close stops the server and removes the socket. It is safe to call more than
// once.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.forwardersMu.Lock()
		if s.forwarders != nil {
			_ = s.forwarders.Stop(context.Background())
			s.forwarders = nil
			s.forwardersKey = ""
		}
		s.forwardersMu.Unlock()
		if s.httpServer != nil {
			s.closeErr = s.httpServer.Close()
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		if s.done != nil {
			<-s.done
		}
		s.removeSocket()
	})
	return s.closeErr
}

func (s *Server) removeSocket() {
	path := s.cfg.SocketPath
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	if info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(path)
	}
}

func (s *Server) connContext(ctx context.Context, conn net.Conn) context.Context {
	uid, err := peerUID(conn)
	if err != nil {
		s.logger.Warn("helper: cannot read peer credentials", "error", err)
		uid = -1
	}
	return context.WithValue(ctx, peerUIDKey{}, uid)
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+helperproto.PathStatus, s.handleStatus)
	mux.HandleFunc("POST "+helperproto.PathHostsSync, s.handleHostsSync)
	mux.HandleFunc("POST "+helperproto.PathEnsureCA, s.handleEnsureCA)
	mux.HandleFunc("POST "+helperproto.PathSign, s.handleSign)
	mux.HandleFunc("POST "+helperproto.PathTrustInstall, s.handleTrustInstall)
	mux.HandleFunc("POST "+helperproto.PathTrustRemove, s.handleTrustRemove)
	mux.HandleFunc("POST "+helperproto.PathTrustStatus, s.handleTrustStatus)
	mux.HandleFunc("POST "+helperproto.PathForwarders, s.handleForwarders)
	mux.HandleFunc("GET "+helperproto.PathVhostsStatus, s.handleVhostsStatus)
	mux.HandleFunc("POST "+helperproto.PathVhostsSync, s.handleVhostsSync)
	mux.HandleFunc("POST "+helperproto.PathVhostsMode, s.handleVhostsMode)
	mux.HandleFunc("DELETE "+helperproto.PathVhosts, s.handleVhostsRemove)
	mux.HandleFunc("POST "+helperproto.PathUninstall, s.handleUninstall)
	return s.authenticate(mux)
}

// authenticate rejects callers whose SO_PEERCRED UID does not match AllowUID
// and requests that do not carry the current protocol version. Both checks run
// before any handler reads the request body.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := r.Context().Value(peerUIDKey{}).(int)
		if !ok || uid != s.cfg.AllowUID {
			s.writeError(w, http.StatusForbidden, helperproto.CodeUnauthorized,
				"peer user is not allowed to use the helper")
			return
		}
		if got := r.Header.Get(helperproto.ProtocolHeader); got != helperproto.Version {
			w.Header().Set(helperproto.ProtocolHeader, helperproto.Version)
			s.writeError(w, http.StatusUpgradeRequired, helperproto.CodeProtocolMismatch,
				fmt.Sprintf("unsupported protocol version %q; supported version is %s", got, helperproto.Version))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := helperproto.StatusResponse{Version: helperproto.Version}
	if s.cfg.Service.CAFingerprint != nil {
		resp.CAFingerprint = s.cfg.Service.CAFingerprint()
	}
	if s.cfg.HostsPath != "" {
		if data, err := os.ReadFile(s.cfg.HostsPath); err == nil {
			_, resp.HostsSynced = hosts.Parse(string(data))
		}
	}
	s.writeJSON(w, http.StatusOK, resp)
}

const maxHostsDomains = 1000

func (s *Server) handleHostsSync(w http.ResponseWriter, r *http.Request) {
	var req helperproto.HostsSyncRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if len(req.Domains) > maxHostsDomains {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest,
			fmt.Sprintf("too many hosts domains: %d (max %d)", len(req.Domains), maxHostsDomains))
		return
	}
	for _, d := range req.Domains {
		if err := domain.ValidateDomain(d); err != nil {
			s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
			return
		}
	}
	changed, err := s.syncHosts(req.Domains)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, helperproto.HostsSyncResponse{Changed: changed})
}

// syncHosts replaces the managed block with domains. An empty list leaves an
// empty managed block, which is how uninstall removes the mappings without
// touching the rest of the hosts file.
func (s *Server) syncHosts(domains []string) (bool, error) {
	if s.cfg.HostsPath == "" {
		return false, errors.New("hosts path is not configured")
	}

	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()

	existing, err := os.ReadFile(s.cfg.HostsPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("read hosts file: %w", err)
	}
	rendered, err := hosts.Render(string(existing), domains)
	if err != nil {
		return false, err
	}
	if err := hosts.WriteAtomic(s.cfg.HostsPath, []byte(rendered), 0); err != nil {
		return false, err
	}
	return string(existing) != rendered, nil
}

func (s *Server) handleEnsureCA(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.EnsureCA == nil {
		s.notImplemented(w, "EnsureCA")
		return
	}
	resp, err := s.cfg.Service.EnsureCA(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.SignCertificate == nil {
		s.notImplemented(w, "SignCertificate")
		return
	}
	var req helperproto.SignRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	resp, err := s.cfg.Service.SignCertificate(r.Context(), req)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTrustInstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.InstallTrust == nil {
		s.notImplemented(w, "InstallTrust")
		return
	}
	s.handleTrust(w, r, s.cfg.Service.InstallTrust)
}

func (s *Server) handleTrustRemove(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.RemoveTrust == nil {
		s.notImplemented(w, "RemoveTrust")
		return
	}
	s.handleTrust(w, r, s.cfg.Service.RemoveTrust)
}

func (s *Server) handleTrustStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.TrustStatus == nil {
		s.notImplemented(w, "TrustStatus")
		return
	}
	s.handleTrust(w, r, s.cfg.Service.TrustStatus)
}

func (s *Server) handleTrust(w http.ResponseWriter, r *http.Request, fn func(context.Context, helperproto.TrustRequest) (helperproto.TrustResponse, error)) {
	var req helperproto.TrustRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	resp, err := fn(r.Context(), req)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleForwarders(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.SetForwarders == nil {
		s.notImplemented(w, "SetForwarders")
		return
	}
	var req helperproto.ForwardersRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.SetForwarders(r.Context(), req); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVhostsStatus(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.VhostStatus == nil {
		s.notImplemented(w, "VhostStatus")
		return
	}
	resp, err := s.cfg.Service.VhostStatus(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleVhostsSync(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.VhostSync == nil {
		s.notImplemented(w, "VhostSync")
		return
	}
	var req helperproto.VHostSyncRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	resp, err := s.cfg.Service.VhostSync(r.Context(), req)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleVhostsMode(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.VhostSetMode == nil {
		s.notImplemented(w, "VhostSetMode")
		return
	}
	var req helperproto.VhostModeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	switch req.Mode {
	case vhostModeBuiltin:
	case vhostModeNginx, vhostModeApache:
		if req.Sync == nil {
			s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest,
				fmt.Sprintf("sync is required when switching to %s", req.Mode))
			return
		}
		req.Sync.Mode = req.Mode
	default:
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest,
			fmt.Sprintf("unsupported serving mode %q", req.Mode))
		return
	}
	syncReq := helperproto.VHostSyncRequest{}
	if req.Sync != nil {
		syncReq = *req.Sync
	}
	resp, err := s.cfg.Service.VhostSetMode(r.Context(), req.Mode, syncReq)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleVhostsRemove(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.VhostRemove == nil {
		s.notImplemented(w, "VhostRemove")
		return
	}
	if err := s.cfg.Service.VhostRemove(r.Context()); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Service.Uninstall == nil {
		s.notImplemented(w, "Uninstall")
		return
	}
	var req helperproto.UninstallRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if err := s.cfg.Service.Uninstall(r.Context(), req); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) notImplemented(w http.ResponseWriter, operation string) {
	s.writeError(w, http.StatusNotImplemented, helperproto.CodeNotImplemented,
		operation+" is not implemented")
}

func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, helperproto.ErrNotImplemented):
		s.writeError(w, http.StatusNotImplemented, helperproto.CodeNotImplemented, err.Error())
	case errors.Is(err, helperproto.ErrInvalidRequest):
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
	case errors.Is(err, helperproto.ErrUnauthorized):
		s.writeError(w, http.StatusForbidden, helperproto.CodeUnauthorized, err.Error())
	case errors.Is(err, helperproto.ErrServingModeUnavailable):
		s.writeError(w, http.StatusConflict, helperproto.CodeServingModeUnavailable, err.Error())
	case errors.Is(err, helperproto.ErrVhostIncludeMissing):
		s.writeError(w, http.StatusConflict, helperproto.CodeVhostIncludeMissing, err.Error())
	case errors.Is(err, helperproto.ErrVhostSyncBusy):
		s.writeError(w, http.StatusConflict, helperproto.CodeVhostSyncBusy, err.Error())
	case errors.Is(err, helperproto.ErrVhostConfigInvalid):
		s.writeError(w, http.StatusUnprocessableEntity, helperproto.CodeVhostConfigInvalid, err.Error())
	case errors.Is(err, helperproto.ErrServerReloadFailed):
		s.writeError(w, http.StatusBadGateway, helperproto.CodeServerReloadFailed, err.Error())
	case errors.Is(err, helperproto.ErrVhostSyncFailed):
		s.writeError(w, http.StatusInternalServerError, helperproto.CodeVhostSyncFailed, err.Error())
	default:
		s.writeError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	s.writeJSON(w, status, helperproto.ErrorResponse{Code: code, Message: message})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		s.logger.Debug("helper: write response", "error", err)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return fmt.Errorf("decode request: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body contains trailing data")
	}
	return nil
}

func prepareSocketDir(socketPath string, allowUID int) error {
	dir := filepath.Dir(socketPath)
	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("helper: socket parent %s is not a directory", dir)
		}
		// Never widen a pre-existing directory. Verify the allowed user can
		// traverse it instead; a private 0700 directory owned by that user is
		// valid, while root-only 0700 is rejected.
		if info.Mode()&os.ModeSticky == 0 {
			perm := info.Mode().Perm()
			accessible := perm&0o001 != 0
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				if int(stat.Uid) == allowUID {
					accessible = perm&0o100 != 0
				} else if perm&0o010 != 0 && allowUID > 0 {
					if account, err := user.LookupId(strconv.Itoa(allowUID)); err == nil {
						if groups, err := account.GroupIds(); err == nil {
							for _, group := range groups {
								if group == strconv.FormatUint(uint64(stat.Gid), 10) {
									accessible = true
									break
								}
							}
						}
					}
				}
			}
			if !accessible {
				return fmt.Errorf("helper: socket parent %s is not traversable by the allowed user", dir)
			}
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("helper: stat socket directory %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("helper: create socket directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return fmt.Errorf("helper: set socket directory mode: %w", err)
	}
	return nil
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("helper: refusing to replace non-socket %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("helper: remove stale socket %s: %w", socketPath, err)
		}
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("helper: stat socket %s: %w", socketPath, err)
	}
}
