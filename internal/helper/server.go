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
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/hosts"
)

const maxRequestBody = 8 << 20

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
	Uninstall       func(context.Context, helperproto.UninstallRequest) error
}

// Server is a helper RPC server listening on a Unix-domain socket.
type Server struct {
	cfg    Config
	logger *slog.Logger

	authority *Authority

	hostsMu sync.Mutex

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
	if cfg.CADir != "" {
		authority, err := LoadOrCreateAuthority(cfg.CADir, time.Now)
		if err != nil {
			return nil, fmt.Errorf("helper: load CA: %w", err)
		}
		s.authority = authority
		s.cfg.Service = s.wireService(s.cfg.Service)
	}
	return s, nil
}

// wireService fills in the CA and trust handlers that the helper owns. Hooks
// supplied by an embedding caller are left untouched.
func (s *Server) wireService(service Service) Service {
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
	requested = filepath.Clean(requested)
	allowed = filepath.Clean(allowed)
	if requested != allowed {
		return "", fmt.Errorf("%w: user_home does not match the allowed user's home directory", helperproto.ErrUnauthorized)
	}
	return requested, nil
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
	if err := prepareSocketDir(socketPath); err != nil {
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

func (s *Server) handleHostsSync(w http.ResponseWriter, r *http.Request) {
	var req helperproto.HostsSyncRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	for _, d := range req.Domains {
		if err := domain.ValidateDomain(d); err != nil {
			s.writeError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
			return
		}
	}
	if s.cfg.HostsPath == "" {
		s.writeError(w, http.StatusInternalServerError, helperproto.CodeInternal, "hosts path is not configured")
		return
	}

	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()

	existing, err := os.ReadFile(s.cfg.HostsPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.writeError(w, http.StatusInternalServerError, helperproto.CodeInternal,
			fmt.Sprintf("read hosts file: %v", err))
		return
	}
	rendered, err := hosts.Render(string(existing), req.Domains)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	if err := hosts.WriteAtomic(s.cfg.HostsPath, []byte(rendered), 0); err != nil {
		s.writeError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, helperproto.HostsSyncResponse{Changed: string(existing) != rendered})
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

func prepareSocketDir(socketPath string) error {
	dir := filepath.Dir(socketPath)
	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("helper: socket parent %s is not a directory", dir)
		}
		// Keep a shared sticky directory (for example /tmp) unchanged, but
		// make a dedicated helper directory traversable.
		if info.Mode()&os.ModeSticky == 0 {
			if err := os.Chmod(dir, 0o755); err != nil {
				return fmt.Errorf("helper: set socket directory mode: %w", err)
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
