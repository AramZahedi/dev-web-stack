//go:build windows

package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/ipc"
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
	cfg      Config
	logger   *slog.Logger
	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
	done     chan struct{}
	close    sync.Once
	closeErr error
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
	endpoint := ipc.NamedPipeEndpoint(cfg.PipeName)
	if err := endpoint.Validate(); err != nil {
		return nil, fmt.Errorf("helper: invalid named pipe: %w", err)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{cfg: cfg, logger: cfg.Logger}, nil
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
		writeWindowsJSON(w, http.StatusOK, helperproto.StatusResponse{Version: helperproto.Version})
	})
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

func writeWindowsJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeWindowsError(w http.ResponseWriter, status int, code, message string) {
	writeWindowsJSON(w, status, helperproto.ErrorResponse{Code: code, Message: message})
}
