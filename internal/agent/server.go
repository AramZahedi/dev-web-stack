package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// Server exposes authenticated agent endpoints over HTTPS. Its handler is
// shared by loopback and WSL NAT listeners.
type Server struct {
	config    Config
	handler   http.Handler
	tlsConfig *tls.Config
}

// NewServer creates the authenticated API handler and TLS configuration.
func NewServer(config Config, token string, identity TLSIdentity, modules []agentapi.Module) (*Server, error) {
	return NewServerWithRuntime(config, token, identity, modules, nil)
}

// NewServerWithRuntime mounts the static backend and port API when runtime is
// non-nil. The module list is still mounted behind the same auth middleware.
func NewServerWithRuntime(config Config, token string, identity TLSIdentity, modules []agentapi.Module, runtime *Runtime) (*Server, error) {
	if err := config.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("agent: bearer token is empty")
	}
	cert, err := tls.X509KeyPair(identity.CertPEM, identity.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("agent: load HTTPS identity: %w", err)
	}
	server := &Server{config: config, tlsConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}}
	mux := http.NewServeMux()
	mux.HandleFunc(agentproto.PathHealth, server.handleHealth)
	mux.HandleFunc(agentproto.PathVersion, server.handleVersion)
	mux.HandleFunc(agentproto.PathCapabilities, server.handleCapabilities)
	if runtime != nil {
		server.mountRuntime(mux, runtime)
	}
	for _, module := range modules {
		if module == nil || module.Handler() == nil || !strings.HasPrefix(module.MountPath(), "/v1/") {
			return nil, errors.New("agent: invalid module: modules must be non-nil and mounted below /v1/")
		}
		agentapi.Mount(mux, module)
	}
	server.handler = authenticate(token, mux)
	return server, nil
}

// NewServerFromFiles loads the root-only config, bearer token, and TLS identity.
func NewServerFromFiles(configPath string, modules []agentapi.Module) (*Server, error) {
	return NewServerFromFilesWithRuntime(configPath, modules, nil)
}

// NewServerFromFilesWithRuntime loads protected agent files and mounts the
// static site/port runtime when supplied.
func NewServerFromFilesWithRuntime(configPath string, modules []agentapi.Module, runtime *Runtime) (*Server, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	if err := requirePrivateDir(config.TLSDir, 0o700); err != nil {
		return nil, fmt.Errorf("agent: TLS directory: %w", err)
	}
	if err := requirePrivateFile(config.TokenFile, 0o600); err != nil {
		return nil, fmt.Errorf("agent: token file: %w", err)
	}
	if err := requirePrivateFile(config.TLSKeyFile, 0o600); err != nil {
		return nil, fmt.Errorf("agent: TLS key: %w", err)
	}
	if err := requirePrivateFile(config.TLSCertFile, 0o644); err != nil {
		return nil, fmt.Errorf("agent: TLS certificate: %w", err)
	}
	token, err := os.ReadFile(config.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("agent: read token: %w", err)
	}
	identity, err := LoadTLSIdentity(config.TLSCertFile, config.TLSKeyFile)
	if err != nil {
		return nil, err
	}
	return NewServerWithRuntime(config, strings.TrimSpace(string(token)), identity, modules, runtime)
}

// Handler returns the authenticated HTTP handler for tests and embedding.
func (s *Server) Handler() http.Handler { return s.handler }

// Serve wraps each supplied listener in TLS and serves the same API on them.
// It is useful for loopback/NAT operation and permits fake listeners in tests.
func (s *Server) Serve(ctx context.Context, listeners ...net.Listener) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(listeners) == 0 {
		return errors.New("agent: Serve requires at least one listener")
	}
	for _, listener := range listeners {
		if listener == nil {
			return errors.New("agent: nil listener")
		}
	}
	server := &http.Server{Handler: s.handler, TLSConfig: s.tlsConfig, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	results := make(chan error, len(listeners))
	var wg sync.WaitGroup
	for _, listener := range listeners {
		wg.Add(1)
		go func(listener net.Listener) {
			defer wg.Done()
			results <- server.Serve(tls.NewListener(listener, s.tlsConfig.Clone()))
		}(listener)
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		wg.Wait()
		return nil
	case err := <-results:
		_ = server.Close()
		wg.Wait()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("agent: HTTPS listener: %w", err)
	}
}

// ListenAddresses returns the two fixed control endpoints: loopback and the
// distro's private NAT address, both on the same non-privileged port.
func (s *Server) ListenAddresses() ([]string, error) {
	ip := net.ParseIP(strings.TrimSpace(s.config.DistroIP))
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return nil, errors.New("agent: distro_ip must be a private WSL NAT address")
	}
	port := fmt.Sprintf("%d", s.config.Port)
	return []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort(ip.String(), port)}, nil
}

// ListenAndServe discovers a private address when the config does not pin one,
// opens the fixed listeners, and serves until ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if s.config.DistroIP == "" {
		ip, err := DiscoverDistroIP()
		if err != nil {
			return err
		}
		s.config.DistroIP = ip.String()
	}
	addresses, err := s.ListenAddresses()
	if err != nil {
		return err
	}
	listeners := make([]net.Listener, 0, len(addresses))
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return fmt.Errorf("agent: listen on %s: %w", address, err)
		}
		listeners = append(listeners, listener)
	}
	return s.Serve(ctx, listeners...)
}

// DiscoverDistroIP discovers the private IPv4 address selected for WSL's
// default route without sending application data to the probe address.
func DiscoverDistroIP() (net.IP, error) {
	conn, err := net.Dial("udp4", "192.0.2.1:53")
	if err != nil {
		return nil, fmt.Errorf("agent: discover WSL NAT address: %w", err)
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil || !addr.IP.IsPrivate() || addr.IP.IsLoopback() || addr.IP.IsLinkLocalUnicast() {
		return nil, errors.New("agent: no private IPv4 address on WSL default route")
	}
	return addr.IP, nil
}
