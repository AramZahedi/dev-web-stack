//go:build windows

package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/ipc"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
	"github.com/AramZahedi/dev-web-stack/internal/winfirewall"
	"github.com/AramZahedi/dev-web-stack/internal/winhosts"
	"github.com/AramZahedi/dev-web-stack/internal/wintrust"
)

var (
	ErrUnauthorized     = helperproto.ErrUnauthorized
	ErrProtocolMismatch = helperproto.ErrProtocolMismatch
	ErrInvalidRequest   = helperproto.ErrInvalidRequest
	ErrNotImplemented   = helperproto.ErrNotImplemented
	ErrInternal         = helperproto.ErrInternal
)

type peerSIDKey struct{}

// Server is the Windows named-pipe helper. It deliberately exposes only the
// host-integration protocol and never has a UID or Unix-socket path.
type Server struct {
	cfg       Config
	logger    *slog.Logger
	mu        sync.Mutex
	listener  net.Listener
	http      *http.Server
	done      chan struct{}
	close     sync.Once
	closeErr  error
	hosts     *winhosts.Manager
	trust     *wintrust.Manager
	firewall  *winfirewall.Manager
	caMu      sync.Mutex
	authority *Authority
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.AllowSID == "" {
		return nil, errors.New("helper: allowed Windows SID is empty")
	}
	if _, err := helperPipeSDDL(cfg.AllowSID); err != nil {
		return nil, fmt.Errorf("helper: invalid allowed Windows SID: %w", err)
	}
	if cfg.PipeName == "" {
		cfg.PipeName = DefaultWindowsPipe
	}
	if cfg.SystemDir != "" && !strings.EqualFold(filepath.Clean(cfg.SystemDir), filepath.Clean(DefaultWindowsSystemDir)) {
		return nil, fmt.Errorf("helper: system directory must be %s", DefaultWindowsSystemDir)
	}
	if cfg.CADir != "" && !strings.EqualFold(filepath.Clean(cfg.CADir), filepath.Clean(DefaultWindowsCADir)) {
		return nil, fmt.Errorf("helper: CA directory must be %s", DefaultWindowsCADir)
	}
	if cfg.HostsPath == "" {
		cfg.HostsPath = DefaultWindowsHostsPath
	}
	endpoint := ipc.NamedPipeEndpoint(cfg.PipeName)
	if err := endpoint.Validate(); err != nil {
		return nil, fmt.Errorf("helper: invalid named pipe: %w", err)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	hosts := cfg.HostsManager
	if hosts == nil {
		hosts = winhosts.NewManager(cfg.HostsPath)
	}
	trustManager := cfg.TrustManager
	if trustManager == nil {
		trustManager = wintrust.NewManager(cfg.Runner)
	}
	firewall := cfg.FirewallManager
	if firewall == nil {
		firewall = winfirewall.NewManager(cfg.Runner)
	}
	return &Server{cfg: cfg, logger: cfg.Logger, hosts: hosts, trust: trustManager, firewall: firewall, authority: cfg.Authority}, nil
}

func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return errors.New("helper: server already started")
	}
	if _, err := s.ensureAuthority(ctx); err != nil {
		return fmt.Errorf("helper: initialize CA: %w", err)
	}
	if s.cfg.WSLSubnet != "" {
		if err := s.firewall.Enable(ctx, s.cfg.WSLSubnet); err != nil {
			return fmt.Errorf("helper: configure WSL proxy firewall rules: %w", err)
		}
	}
	listener, err := ipc.Listen(ipc.NamedPipeEndpoint(s.cfg.PipeName), ipc.WithAllowSID(s.cfg.AllowSID), ipc.WithAdministrators(true))
	if err != nil {
		return fmt.Errorf("helper: listen on %s: %w", s.cfg.PipeName, err)
	}
	srv := &http.Server{
		Handler:           s.handler(),
		ConnContext:       s.connContext,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	s.listener = listener
	s.http = srv
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		if serveErr := srv.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.logger.Error("Windows helper stopped", "error", serveErr)
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

func (s *Server) Close() error {
	s.close.Do(func() {
		s.mu.Lock()
		srv, listener, done := s.http, s.listener, s.done
		s.mu.Unlock()
		if srv != nil {
			s.closeErr = srv.Close()
		}
		if listener != nil {
			_ = listener.Close()
		}
		if done != nil {
			<-done
		}
	})
	return s.closeErr
}

func (s *Server) connContext(ctx context.Context, conn net.Conn) context.Context {
	peer, err := ipc.PeerIdentityOf(conn)
	if err != nil {
		s.logger.Warn("helper: cannot read named-pipe peer SID", "error", err)
		peer = ipc.PeerIdentity{}
	}
	return context.WithValue(ctx, peerSIDKey{}, peer.SID)
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+helperproto.PathStatus, func(w http.ResponseWriter, _ *http.Request) {
		response := helperproto.StatusResponse{Version: helperproto.Version}
		if s.authority != nil {
			response.CAFingerprint = s.authority.Fingerprint()
		}
		_, response.HostsSynced, _ = s.hosts.Status()
		writeWindowsJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("POST "+helperproto.PathHostsSync, s.handleHostsSync)
	mux.HandleFunc("POST "+helperproto.PathEnsureCA, s.handleEnsureCA)
	mux.HandleFunc("POST "+helperproto.PathSign, s.handleSign)
	mux.HandleFunc("POST "+helperproto.PathTrustInstall, s.handleTrustInstall)
	mux.HandleFunc("POST "+helperproto.PathTrustRemove, s.handleTrustRemove)
	mux.HandleFunc("POST "+helperproto.PathTrustStatus, s.handleTrustStatus)
	mux.HandleFunc("POST "+helperproto.PathUninstall, s.handleUninstall)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peerSID, _ := r.Context().Value(peerSIDKey{}).(string)
		if err := authorizePeerSID(s.cfg.AllowSID, ipc.PeerIdentity{SID: peerSID}); err != nil {
			writeWindowsError(w, http.StatusForbidden, helperproto.CodeUnauthorized, err.Error())
			return
		}
		if got := r.Header.Get(helperproto.ProtocolHeader); got != helperproto.Version {
			w.Header().Set(helperproto.ProtocolHeader, helperproto.Version)
			writeWindowsError(w, http.StatusUpgradeRequired, helperproto.CodeProtocolMismatch,
				fmt.Sprintf("unsupported protocol version %q; supported version is %s", got, helperproto.Version))
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) handleHostsSync(w http.ResponseWriter, r *http.Request) {
	var request helperproto.HostsSyncRequest
	if err := decodeWindowsJSON(w, r, &request); err != nil {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if len(request.Domains) > 1000 {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, "too many hosts domains")
		return
	}
	for _, name := range request.Domains {
		if err := domain.ValidateDomain(name); err != nil {
			writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
			return
		}
	}
	changed, err := s.hosts.Sync(request.Domains)
	if err != nil {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	writeWindowsJSON(w, http.StatusOK, helperproto.HostsSyncResponse{Changed: changed})
}

func (s *Server) handleEnsureCA(w http.ResponseWriter, r *http.Request) {
	authority, err := s.ensureAuthority(r.Context())
	if err != nil {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	writeWindowsJSON(w, http.StatusOK, helperproto.EnsureCAResponse{RootPEM: authority.RootPEM(), Fingerprint: authority.Fingerprint()})
}

func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	var request helperproto.SignRequest
	if err := decodeWindowsJSON(w, r, &request); err != nil {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	authority, err := s.ensureAuthority(r.Context())
	if err != nil {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	certPEM, serial, expires, err := authority.SignCSR(request.CSR, request.Domains, maxLeafValidity)
	if err != nil {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	writeWindowsJSON(w, http.StatusOK, helperproto.SignResponse{CertPEM: certPEM, Serial: serial, NotAfter: expires})
}

func (s *Server) handleTrustInstall(w http.ResponseWriter, r *http.Request) {
	var request helperproto.TrustRequest
	if err := decodeWindowsJSON(w, r, &request); err != nil {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	authority, err := s.ensureAuthority(r.Context())
	if err != nil {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	if err := s.trust.InstallRoot(r.Context(), DefaultWindowsRootCert); err != nil {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	if err := s.trust.InstallFirefoxPolicy(r.Context()); err != nil {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, err.Error())
		return
	}
	writeWindowsJSON(w, http.StatusOK, helperproto.TrustResponse{Targets: []trust.Target{
		{Name: "Windows machine root store", OK: true, Detail: "installed " + authority.Fingerprint()},
		{Name: "Firefox enterprise policy", OK: true, Detail: "enabled Windows enterprise root import"},
	}})
}

func (s *Server) handleTrustRemove(w http.ResponseWriter, r *http.Request) {
	var request helperproto.TrustRequest
	if err := decodeWindowsJSON(w, r, &request); err != nil {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var failures []string
	if s.authority != nil {
		if err := s.trust.RemoveRoot(r.Context(), s.authority.Thumbprint()); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if err := s.trust.RemoveFirefoxPolicy(r.Context()); err != nil {
		failures = append(failures, err.Error())
	}
	if len(failures) > 0 {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, strings.Join(failures, "; "))
		return
	}
	writeWindowsJSON(w, http.StatusOK, helperproto.TrustResponse{Targets: []trust.Target{
		{Name: "Windows machine root store", OK: true, Detail: "removed"},
		{Name: "Firefox enterprise policy", OK: true, Detail: "removed"},
	}})
}

func (s *Server) handleTrustStatus(w http.ResponseWriter, r *http.Request) {
	var request helperproto.TrustRequest
	if err := decodeWindowsJSON(w, r, &request); err != nil {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	if s.authority == nil {
		writeWindowsJSON(w, http.StatusOK, helperproto.TrustResponse{Targets: []trust.Target{
			{Name: "Windows machine root store", OK: false, Detail: "CA not initialized"},
			{Name: "Firefox enterprise policy", OK: false, Detail: "CA not initialized"},
		}})
		return
	}
	root, rootErr := s.trust.RootStatus(r.Context(), s.authority.Thumbprint())
	firefox, firefoxErr := s.trust.FirefoxPolicyStatus(r.Context())
	if rootErr != nil {
		root.Detail = rootErr.Error()
	}
	if firefoxErr != nil {
		firefox.Detail = firefoxErr.Error()
	}
	writeWindowsJSON(w, http.StatusOK, helperproto.TrustResponse{Targets: []trust.Target{root, firefox}})
}

func (s *Server) handleUninstall(w http.ResponseWriter, r *http.Request) {
	if _, err := decodeWindowsJSONOptional(w, r); err != nil {
		writeWindowsError(w, http.StatusBadRequest, helperproto.CodeInvalidRequest, err.Error())
		return
	}
	var failures []string
	if _, err := s.hosts.Sync(nil); err != nil {
		failures = append(failures, "hosts: "+err.Error())
	}
	if err := s.trust.RemoveFirefoxPolicy(r.Context()); err != nil {
		failures = append(failures, "Firefox policy: "+err.Error())
	}
	if s.authority != nil {
		if err := s.trust.RemoveRoot(r.Context(), s.authority.Thumbprint()); err != nil {
			failures = append(failures, "root trust: "+err.Error())
		}
	}
	if err := s.firewall.Remove(r.Context()); err != nil {
		failures = append(failures, "firewall: "+err.Error())
	}
	if len(failures) > 0 {
		writeWindowsError(w, http.StatusInternalServerError, helperproto.CodeInternal, strings.Join(failures, "; "))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ensureAuthority(ctx context.Context) (*Authority, error) {
	s.caMu.Lock()
	defer s.caMu.Unlock()
	if s.authority != nil {
		return s.authority, nil
	}
	if err := s.trust.EnsureStorage(ctx); err != nil {
		return nil, err
	}
	authority, err := LoadOrCreateAuthority(DefaultWindowsCADir, time.Now)
	if err != nil {
		return nil, err
	}
	s.authority = authority
	return authority, nil
}

func decodeWindowsJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body contains trailing data")
	}
	return nil
}

func decodeWindowsJSONOptional(w http.ResponseWriter, r *http.Request) (helperproto.UninstallRequest, error) {
	var request helperproto.UninstallRequest
	if r.Body == nil || r.ContentLength == 0 {
		return request, nil
	}
	err := decodeWindowsJSON(w, r, &request)
	return request, err
}

func writeWindowsJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeWindowsError(w http.ResponseWriter, status int, code, message string) {
	writeWindowsJSON(w, status, helperproto.ErrorResponse{Code: code, Message: message})
}
