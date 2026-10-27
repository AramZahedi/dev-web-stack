// Package forwarder owns fixed TCP listeners that forward traffic to the
// unprivileged proxy's loopback ports. It is used by the privileged helper for
// host ports 80 and 443, which the daemon cannot bind itself.
package forwarder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dialTimeout   = 10 * time.Second
	acceptBackoff = 50 * time.Millisecond
)

// ListenerStatus describes one configured forwarder. Listen is the configured
// address before Start and the bound address while running (they differ only
// when port 0 was requested). Error carries the most recent listener or relay
// error, if any.
type ListenerStatus struct {
	Name   string
	Listen string
	Target string
	OK     bool
	Error  string
}

type spec struct {
	name   string
	listen string
	target string
}

type listener struct {
	spec spec
	ln   net.Listener

	mu      sync.Mutex
	lastErr string
}

func (l *listener) setError(err error) {
	if err == nil {
		return
	}
	l.mu.Lock()
	l.lastErr = err.Error()
	l.mu.Unlock()
}

func (l *listener) errorText() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastErr
}

// Set is a group of fixed forwarders. It is safe for concurrent use. A Set is
// reusable after Stop.
type Set struct {
	logger *slog.Logger
	specs  []spec

	mu        sync.Mutex
	running   bool
	stopping  bool
	listeners []*listener
	runCtx    context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	connMu  sync.Mutex
	closing bool
	conns   map[net.Conn]struct{}
}

// New returns a forwarder set. An empty listen address disables that listener.
// Targets must be loopback addresses; Start rejects anything else.
func New(httpListen, httpsListen, httpTarget, httpsTarget string, logger *slog.Logger) *Set {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Set{logger: logger}
	if httpListen != "" {
		s.specs = append(s.specs, spec{name: "http", listen: httpListen, target: httpTarget})
	}
	if httpsListen != "" {
		s.specs = append(s.specs, spec{name: "https", listen: httpsListen, target: httpsTarget})
	}
	return s
}

// Start validates the configuration, binds every listener, and begins
// accepting connections. The listeners live until Stop is called; ctx only
// bounds startup. If any listener cannot bind, Start closes the listeners it
// already opened and returns an error that wraps syscall.EADDRINUSE (including
// the listen address) for address conflicts.
func (s *Set) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return errors.New("forwarder: already started")
	}
	if s.stopping {
		return errors.New("forwarder: still stopping")
	}
	for _, sp := range s.specs {
		if err := validateListen(sp.listen); err != nil {
			return fmt.Errorf("forwarder: %s listener: %w", sp.name, err)
		}
		if err := validateTarget(sp.target); err != nil {
			return fmt.Errorf("forwarder: %s target: %w", sp.name, err)
		}
	}

	listeners := make([]*listener, 0, len(s.specs))
	for _, sp := range s.specs {
		var lc net.ListenConfig
		ln, err := lc.Listen(ctx, "tcp", sp.listen)
		if err != nil {
			for _, open := range listeners {
				_ = open.ln.Close()
			}
			return fmt.Errorf("forwarder: listen on %s: %w", sp.listen, mapListenErr(err))
		}
		listeners = append(listeners, &listener{spec: sp, ln: ln})
	}

	s.listeners = listeners
	s.running = true
	s.connMu.Lock()
	s.closing = false
	s.conns = make(map[net.Conn]struct{})
	s.connMu.Unlock()
	s.runCtx, s.cancel = context.WithCancel(context.Background())
	for _, l := range listeners {
		s.wg.Add(1)
		go s.acceptLoop(l)
	}
	s.logger.Info("forwarder: started", "listeners", len(listeners))
	return nil
}

// Stop closes every listener and active connection and waits for the accept
// and relay goroutines to exit. It is safe to call more than once.
func (s *Set) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.stopping = true
	listeners := s.listeners
	cancel := s.cancel
	s.listeners = nil
	s.cancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, l := range listeners {
		_ = l.ln.Close()
	}
	s.connMu.Lock()
	s.closing = true
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.connMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// The goroutines are already unwinding. Finish releasing the set in
		// the background so a later Start cannot race the wait group, then
		// report the caller's deadline.
		go func() {
			<-done
			s.finishStop()
		}()
		return ctx.Err()
	}

	s.finishStop()
	s.logger.Info("forwarder: stopped", "listeners", len(listeners))
	return nil
}

func (s *Set) finishStop() {
	s.connMu.Lock()
	s.conns = nil
	s.closing = false
	s.connMu.Unlock()
	s.mu.Lock()
	s.stopping = false
	s.mu.Unlock()
}

// Status returns one entry per configured listener in HTTP, HTTPS order. The
// returned slice is a copy and is safe to modify.
func (s *Set) Status() []ListenerStatus {
	s.mu.Lock()
	running := s.running
	listeners := append([]*listener(nil), s.listeners...)
	s.mu.Unlock()

	out := make([]ListenerStatus, 0, len(s.specs))
	for i, sp := range s.specs {
		status := ListenerStatus{Name: sp.name, Listen: sp.listen, Target: sp.target}
		if running && i < len(listeners) {
			l := listeners[i]
			status.Listen = l.ln.Addr().String()
			status.OK = true
			status.Error = l.errorText()
		} else {
			status.Error = "stopped"
		}
		out = append(out, status)
	}
	return out
}

func (s *Set) acceptLoop(l *listener) {
	defer s.wg.Done()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if s.runCtx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			l.setError(fmt.Errorf("accept on %s: %w", l.spec.listen, err))
			s.logger.Error("forwarder: accept failed", "listen", l.spec.listen, "error", err)
			// Back off so a persistent accept error cannot spin the CPU.
			select {
			case <-s.runCtx.Done():
				return
			case <-time.After(acceptBackoff):
			}
			continue
		}
		s.wg.Add(1)
		go s.handle(l, conn)
	}
}

func (s *Set) handle(l *listener, client net.Conn) {
	defer s.wg.Done()
	if !s.track(client) {
		_ = client.Close()
		return
	}

	dialer := net.Dialer{Timeout: dialTimeout}
	upstream, err := dialer.DialContext(s.runCtx, "tcp", l.spec.target)
	if err != nil {
		if s.runCtx.Err() == nil {
			l.setError(fmt.Errorf("dial target %s: %w", l.spec.target, err))
			s.logger.Error("forwarder: dial target failed", "target", l.spec.target, "error", err)
		}
		s.untrack(client)
		_ = client.Close()
		return
	}
	if !s.track(upstream) {
		_ = upstream.Close()
		s.untrack(client)
		_ = client.Close()
		return
	}
	s.relay(l, client, upstream)
}

func (s *Set) relay(l *listener, client, upstream net.Conn) {
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		s.untrack(client)
		s.untrack(upstream)
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	copySide := func(dst, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err != nil && s.runCtx.Err() == nil && !errors.Is(err, net.ErrClosed) {
			l.setError(fmt.Errorf("copy %s -> %s: %w", src.RemoteAddr(), dst.RemoteAddr(), err))
		}
		closeWrite(dst)
	}
	go copySide(upstream, client)
	go copySide(client, upstream)
	wg.Wait()
}

func (s *Set) track(conn net.Conn) bool {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.closing {
		return false
	}
	s.conns[conn] = struct{}{}
	return true
}

func (s *Set) untrack(conn net.Conn) {
	s.connMu.Lock()
	delete(s.conns, conn)
	s.connMu.Unlock()
}

func closeWrite(conn net.Conn) {
	type closeWriter interface {
		CloseWrite() error
	}
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

func validateListen(addr string) error {
	host, _, err := splitAddress(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if host != "" {
		if ip := net.ParseIP(host); ip == nil {
			return fmt.Errorf("listen address %q: host must be an IP address", addr)
		}
	}
	return nil
}

func validateTarget(addr string) error {
	host, port, err := splitAddress(addr)
	if err != nil {
		return fmt.Errorf("target %q: %w", addr, err)
	}
	if port == 0 {
		return fmt.Errorf("target %q: port must be between 1 and 65535", addr)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("target %q: host must be a loopback IP address", addr)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("target %q: address is not loopback", addr)
	}
	return nil
}

func splitAddress(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, fmt.Errorf("port %q is not numeric", portText)
	}
	if port < 0 || port > 65535 {
		return "", 0, fmt.Errorf("port %d is outside 0-65535", port)
	}
	return host, port, nil
}
