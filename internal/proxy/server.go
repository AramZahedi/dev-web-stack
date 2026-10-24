package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	readHeaderTimeout = 30 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownTimeout   = 10 * time.Second

	// defaultMaxBodyBytes is the proxy-wide request body ceiling when the
	// caller does not supply one. It matches config.Default's 1024 MB.
	defaultMaxBodyBytes int64 = 1 << 30
)

// ServerConfig configures the HTTP and HTTPS proxy listeners. At least one of
// HTTPAddr or HTTPSAddr must be set; an empty address disables that listener.
// MaxBodyBytes is the request body ceiling and defaults to 1 GiB when zero or
// negative.
type ServerConfig struct {
	HTTPAddr     string
	HTTPSAddr    string
	Router       *Router
	MaxBodyBytes int64
	Logger       *slog.Logger
}

// Server runs the proxy listeners. Start binds both addresses before serving
// and returns once they are ready; Stop (or cancelling Start's context) shuts
// them down.
type Server struct {
	cfg    ServerConfig
	logger *slog.Logger

	mu        sync.Mutex
	started   bool
	wg        sync.WaitGroup
	done      chan struct{}
	httpSrv   *http.Server
	httpsSrv  *http.Server
	httpLn    net.Listener
	httpsLn   net.Listener
	httpAddr  string
	httpsAddr string
}

// NewServer returns a server for cfg. It does not bind until Start.
func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{cfg: cfg, logger: logger}
}

// Start binds the configured listeners and begins serving in the background.
// Bind errors are returned synchronously and no partial listener set is left
// open.
// ErrAlreadyStarted reports a duplicate Start call. Callers that ensure the
// built-in proxy is running can treat it as success.
var ErrAlreadyStarted = errors.New("proxy: server already started")

func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return ErrAlreadyStarted
	}
	if s.cfg.Router == nil {
		return errors.New("proxy: router is required")
	}
	httpAddr := strings.TrimSpace(s.cfg.HTTPAddr)
	httpsAddr := strings.TrimSpace(s.cfg.HTTPSAddr)
	if httpAddr == "" && httpsAddr == "" {
		return errors.New("proxy: at least one listen address is required")
	}

	handler := s.cfg.Router.Handler()
	maxBodyBytes := s.cfg.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	handler = limitedBody(handler, maxBodyBytes)

	httpSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	httpsSrv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		TLSConfig: &tls.Config{
			GetCertificate: s.cfg.Router.Certificate,
			MinVersion:     tls.VersionTLS12,
			NextProtos:     []string{"h2", "http/1.1"},
		},
	}

	var httpLn, httpsLn net.Listener
	if httpAddr != "" {
		ln, err := net.Listen("tcp", httpAddr)
		if err != nil {
			return fmt.Errorf("proxy: listen on %s: %w", httpAddr, err)
		}
		httpLn = ln
	}
	if httpsAddr != "" {
		ln, err := net.Listen("tcp", httpsAddr)
		if err != nil {
			if httpLn != nil {
				_ = httpLn.Close()
			}
			return fmt.Errorf("proxy: listen on %s: %w", httpsAddr, err)
		}
		httpsLn = ln
	}

	s.httpSrv = httpSrv
	s.httpsSrv = httpsSrv
	s.httpLn = httpLn
	s.httpsLn = httpsLn
	s.httpAddr = listenerAddr(httpLn)
	s.httpsAddr = listenerAddr(httpsLn)
	s.done = make(chan struct{})
	s.started = true

	if httpLn != nil {
		s.wg.Add(1)
		go s.serve(httpSrv, httpLn, "http")
	}
	if httpsLn != nil {
		s.wg.Add(1)
		go s.serveTLS(httpsSrv, httpsLn, "https")
	}
	done := s.done
	go func() {
		s.wg.Wait()
		close(done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()
			if err := s.Stop(stopCtx); err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("proxy: shutdown after context cancellation", "error", err)
			}
		case <-done:
		}
	}()
	return nil
}

// Stop gracefully shuts down both listeners. It is idempotent and may be
// called concurrently with a context-triggered shutdown.
func (s *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	httpSrv, httpsSrv := s.httpSrv, s.httpsSrv
	done := s.done
	wasStarted := s.started
	s.started = false
	s.mu.Unlock()

	if !wasStarted {
		if done == nil {
			return nil
		}
		return waitForDone(done, ctx)
	}

	var errs []error
	for _, srv := range []*http.Server{httpSrv, httpsSrv} {
		if srv == nil {
			continue
		}
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
			// Active connections (for example a stalled SSE stream) must not
			// keep the process alive past the shutdown budget.
			_ = srv.Close()
		}
	}
	if done != nil {
		if err := waitForDone(done, ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if s.cfg.Router != nil {
		s.cfg.Router.CloseIdleConnections()
	}
	return errors.Join(errs...)
}

// HTTPAddr returns the bound HTTP address, or "" when HTTP is disabled or the
// server has not started.
func (s *Server) HTTPAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.httpAddr
}

// HTTPSAddr returns the bound HTTPS address, or "" when HTTPS is disabled or
// the server has not started.
func (s *Server) HTTPSAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.httpsAddr
}

func (s *Server) serve(srv *http.Server, ln net.Listener, scheme string) {
	defer s.wg.Done()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.Error("proxy: listener stopped", "scheme", scheme, "addr", ln.Addr().String(), "error", err)
	}
}

func (s *Server) serveTLS(srv *http.Server, ln net.Listener, scheme string) {
	defer s.wg.Done()
	if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.logger.Error("proxy: listener stopped", "scheme", scheme, "addr", ln.Addr().String(), "error", err)
	}
}

func listenerAddr(ln net.Listener) string {
	if ln == nil {
		return ""
	}
	return ln.Addr().String()
}

func waitForDone(done <-chan struct{}, ctx context.Context) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
