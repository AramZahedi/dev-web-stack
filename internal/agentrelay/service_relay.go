// Package agentrelay implements an authenticated loopback service relay module
// for the WSL agent. It forwards Windows/host connections for database, cache,
// and admin services to their respective container loopback ports inside WSL.
package agentrelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// Allowed database, cache, and admin service ports.
// Only catalog-defined loopback service ports are permitted.
const (
	PortMySQL         = 3306
	PortMariaDB       = 3306
	PortPostgres      = 5432
	PortRedis         = 6379
	PortPhpMyAdmin    = 8080
	PortPhpMyAdminAlt = 8082
)

var allowedPorts = map[int]string{
	PortMySQL:         "mysql",
	PortPostgres:      "postgresql",
	PortRedis:         "redis",
	PortPhpMyAdmin:    "phpmyadmin",
	PortPhpMyAdminAlt: "phpmyadmin",
}

var serviceDefaultPorts = map[string]int{
	string(services.IDMySQL):      PortMySQL,
	string(services.IDMariaDB):    PortMariaDB,
	string(services.IDPostgreSQL): PortPostgres,
	"postgres":                    PortPostgres,
	string(services.IDRedis):      PortRedis,
	string(services.IDPHPMyAdmin): PortPhpMyAdmin,
}

// ErrDisallowedPort is returned when attempting to relay an arbitrary or unsupported port.
var ErrDisallowedPort = errors.New("agentrelay: port is not in the allowed service port list")

// ErrNonLoopbackTarget is returned when attempting to relay to a non-loopback destination.
var ErrNonLoopbackTarget = errors.New("agentrelay: target host must be a loopback address")

// IsAllowedPort returns true if port is on the fixed service allowlist.
func IsAllowedPort(port int) bool {
	_, ok := allowedPorts[port]
	return ok
}

// ValidatePort checks whether port is in the allowlist.
func ValidatePort(port int) error {
	if !IsAllowedPort(port) {
		return fmt.Errorf("%w: port %d", ErrDisallowedPort, port)
	}
	return nil
}

// PortForService resolves the canonical port for a service name or ID.
func PortForService(serviceName string) (int, error) {
	key := strings.ToLower(strings.TrimSpace(serviceName))
	port, ok := serviceDefaultPorts[key]
	if !ok {
		return 0, fmt.Errorf("agentrelay: unknown service %q", serviceName)
	}
	return port, nil
}

// AllowedPortList returns a sorted slice of all allowed ports.
func AllowedPortList() []int {
	ports := make([]int, 0, len(allowedPorts))
	for p := range allowedPorts {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}

// isLoopback checks if an address or host string represents a loopback address.
func isLoopback(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "" || h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Config configures the service relay module.
type Config struct {
	// ListenIP is the IP on which to bind relay listeners.
	// Defaults to "127.0.0.1" if empty.
	ListenIP string

	// TargetHost is the loopback host to forward to.
	// Must be a loopback address. Defaults to "127.0.0.1" if empty.
	TargetHost string

	// Logger optional logger.
	Logger *slog.Logger
}

type relayListener struct {
	port       int
	serviceID  string
	targetHost string
	targetPort int
	ln         net.Listener
	cancel     context.CancelFunc

	connMu sync.Mutex
	conns  map[net.Conn]struct{}
}

func (rl *relayListener) track(c net.Conn) {
	rl.connMu.Lock()
	defer rl.connMu.Unlock()
	rl.conns[c] = struct{}{}
}

func (rl *relayListener) untrack(c net.Conn) {
	rl.connMu.Lock()
	defer rl.connMu.Unlock()
	delete(rl.conns, c)
}

func (rl *relayListener) closeConns() {
	rl.connMu.Lock()
	defer rl.connMu.Unlock()
	for c := range rl.conns {
		_ = c.Close()
	}
	rl.conns = make(map[net.Conn]struct{})
}

// ServiceRelay is the agent module that forwards traffic on allowed service ports.
type ServiceRelay struct {
	cfg Config

	mu      sync.Mutex
	relays  map[int]*relayListener
	closing bool
}

var _ agentapi.Module = (*ServiceRelay)(nil)

// New constructs a new ServiceRelay module with validated loopback targets.
func New(cfg Config) (*ServiceRelay, error) {
	if cfg.ListenIP == "" {
		cfg.ListenIP = "127.0.0.1"
	}
	if cfg.TargetHost == "" {
		cfg.TargetHost = "127.0.0.1"
	}
	if !isLoopback(cfg.TargetHost) {
		return nil, fmt.Errorf("%w: %q", ErrNonLoopbackTarget, cfg.TargetHost)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &ServiceRelay{
		cfg:    cfg,
		relays: make(map[int]*relayListener),
	}, nil
}

// Name implements agentapi.Module.
func (*ServiceRelay) Name() string { return "service-relay" }

// MountPath implements agentapi.Module.
func (*ServiceRelay) MountPath() string { return "/v1/relay/" }

// Handler implements agentapi.Module and serves the HTTP management API.
func (r *ServiceRelay) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/relay/status", r.handleStatus)
	mux.HandleFunc("GET /v1/relay/allowlist", r.handleAllowlist)
	mux.HandleFunc("POST /v1/relay/start", r.handleStart)
	mux.HandleFunc("POST /v1/relay/stop", r.handleStop)
	mux.HandleFunc("POST /v1/relay/sync", r.handleSync)
	return mux
}

// StartRelay starts relaying on the specified allowed port.
func (r *ServiceRelay) StartRelay(ctx context.Context, port int) error {
	if err := ValidatePort(port); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closing {
		return errors.New("agentrelay: service relay is shutting down")
	}

	if _, exists := r.relays[port]; exists {
		return nil // already running
	}

	listenAddr := net.JoinHostPort(r.cfg.ListenIP, strconv.Itoa(port))
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("agentrelay: listen on %s: %w", listenAddr, err)
	}

	rlCtx, cancel := context.WithCancel(context.Background())
	rl := &relayListener{
		port:       port,
		serviceID:  allowedPorts[port],
		targetHost: r.cfg.TargetHost,
		targetPort: port,
		ln:         ln,
		cancel:     cancel,
		conns:      make(map[net.Conn]struct{}),
	}
	r.relays[port] = rl

	go r.serveListener(rlCtx, rl)
	return nil
}

// ForwardListener runs the relay loop on an externally supplied net.Listener.
// Used for testing or custom bindings with allowlisted port validation.
func (r *ServiceRelay) ForwardListener(ctx context.Context, ln net.Listener, port, targetPort int) error {
	if err := ValidatePort(port); err != nil {
		return err
	}

	rl := &relayListener{
		port:       port,
		serviceID:  allowedPorts[port],
		targetHost: r.cfg.TargetHost,
		targetPort: targetPort,
		ln:         ln,
		conns:      make(map[net.Conn]struct{}),
	}

	r.mu.Lock()
	r.relays[port] = rl
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.relays, port)
		r.mu.Unlock()
		rl.closeConns()
	}()

	return r.runAcceptLoop(ctx, rl)
}

func (r *ServiceRelay) serveListener(ctx context.Context, rl *relayListener) {
	defer func() {
		_ = rl.ln.Close()
		rl.closeConns()
		r.mu.Lock()
		delete(r.relays, rl.port)
		r.mu.Unlock()
	}()

	_ = r.runAcceptLoop(ctx, rl)
}

func (r *ServiceRelay) runAcceptLoop(ctx context.Context, rl *relayListener) error {
	for {
		client, err := rl.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			r.cfg.Logger.Error("agentrelay: accept error", "port", rl.port, "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}

		go r.handleConn(ctx, rl, client)
	}
}

func (r *ServiceRelay) handleConn(ctx context.Context, rl *relayListener, client net.Conn) {
	rl.track(client)
	defer func() {
		rl.untrack(client)
		_ = client.Close()
	}()

	targetAddr := net.JoinHostPort(rl.targetHost, strconv.Itoa(rl.targetPort))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	upstream, err := dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		r.cfg.Logger.Error("agentrelay: dial upstream container failed", "target", targetAddr, "error", err)
		return
	}

	rl.track(upstream)
	defer func() {
		rl.untrack(upstream)
		_ = upstream.Close()
	}()

	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	go func() {
		<-connCtx.Done()
		_ = client.Close()
		_ = upstream.Close()
	}()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, err := io.Copy(upstream, client)
		if err != nil {
			connCancel()
		} else {
			closeWrite(upstream)
		}
	}()

	go func() {
		defer wg.Done()
		_, err := io.Copy(client, upstream)
		if err != nil {
			connCancel()
		} else {
			closeWrite(client)
		}
	}()

	wg.Wait()
}

type closeWriter interface {
	CloseWrite() error
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

// StopRelay stops relaying for a specific port.
func (r *ServiceRelay) StopRelay(port int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rl, ok := r.relays[port]
	if !ok {
		return nil
	}

	if rl.cancel != nil {
		rl.cancel()
	}
	_ = rl.ln.Close()
	rl.closeConns()
	delete(r.relays, port)
	return nil
}

// Close closes all active relays and listener instances.
func (r *ServiceRelay) Close() error {
	r.mu.Lock()
	r.closing = true
	relays := make([]*relayListener, 0, len(r.relays))
	for _, rl := range r.relays {
		relays = append(relays, rl)
	}
	r.relays = make(map[int]*relayListener)
	r.mu.Unlock()

	for _, rl := range relays {
		if rl.cancel != nil {
			rl.cancel()
		}
		_ = rl.ln.Close()
		rl.closeConns()
	}
	return nil
}

// RelayStatus contains status information for a relay port.
type RelayStatus struct {
	ServiceID string `json:"service_id"`
	Port      int    `json:"port"`
	Listen    string `json:"listen"`
	Target    string `json:"target"`
	Active    bool   `json:"active"`
	Error     string `json:"error,omitempty"`
}

// Status returns status information for all allowed service ports.
func (r *ServiceRelay) Status() []RelayStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]RelayStatus, 0, len(allowedPorts))
	for _, port := range AllowedPortList() {
		st := RelayStatus{
			ServiceID: allowedPorts[port],
			Port:      port,
			Listen:    net.JoinHostPort(r.cfg.ListenIP, strconv.Itoa(port)),
			Target:    net.JoinHostPort(r.cfg.TargetHost, strconv.Itoa(port)),
			Active:    false,
		}
		if rl, ok := r.relays[port]; ok {
			st.Active = true
			if rl.ln != nil {
				st.Listen = rl.ln.Addr().String()
			}
		}
		out = append(out, st)
	}
	return out
}

// RelayRequest is the JSON payload for starting or stopping a relay.
type RelayRequest struct {
	Service string `json:"service,omitempty"`
	Port    int    `json:"port,omitempty"`
}

// SyncRelayRequest is the JSON payload for synchronizing active relays.
type SyncRelayRequest struct {
	Services []string `json:"services,omitempty"`
	Ports    []int    `json:"ports,omitempty"`
}

// StatusResponse wraps relay status records.
type StatusResponse struct {
	Relays []RelayStatus `json:"relays"`
}

// AllowlistResponse describes the allowed ports and default services.
type AllowlistResponse struct {
	Ports    []int          `json:"ports"`
	Services map[string]int `json:"services"`
}

func (r *ServiceRelay) handleStatus(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, StatusResponse{Relays: r.Status()})
}

func (r *ServiceRelay) handleAllowlist(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, AllowlistResponse{
		Ports:    AllowedPortList(),
		Services: serviceDefaultPorts,
	})
}

func (r *ServiceRelay) handleStart(w http.ResponseWriter, req *http.Request) {
	var body RelayRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	port := body.Port
	if port == 0 && body.Service != "" {
		p, err := PortForService(body.Service)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		port = p
	}

	if err := ValidatePort(port); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := r.StartRelay(req.Context(), port); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "started", "port": port})
}

func (r *ServiceRelay) handleStop(w http.ResponseWriter, req *http.Request) {
	var body RelayRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	port := body.Port
	if port == 0 && body.Service != "" {
		p, err := PortForService(body.Service)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		port = p
	}

	if err := ValidatePort(port); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := r.StopRelay(port); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "stopped", "port": port})
}

func (r *ServiceRelay) handleSync(w http.ResponseWriter, req *http.Request) {
	var body SyncRelayRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	targetPorts := make(map[int]struct{})
	for _, p := range body.Ports {
		if err := ValidatePort(p); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		targetPorts[p] = struct{}{}
	}

	for _, s := range body.Services {
		p, err := PortForService(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		targetPorts[p] = struct{}{}
	}

	for _, p := range AllowedPortList() {
		if _, shouldRun := targetPorts[p]; shouldRun {
			_ = r.StartRelay(req.Context(), p)
		} else {
			_ = r.StopRelay(p)
		}
	}

	writeJSON(w, http.StatusOK, StatusResponse{Relays: r.Status()})
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
