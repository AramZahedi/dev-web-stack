package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

var ErrIngressNotStarted = errors.New("agent: ingress is not started")

// Ingress is a fixed TCP proxy from WSL 80/443 to the Windows host gateway's
// daemon listeners at 8080/8443. No request can alter either target port.
type Ingress struct {
	mu              sync.Mutex
	discoverGateway func(context.Context) (net.IP, error)
	listen          func(network, address string) (net.Listener, error)
	dial            func(context.Context, string, string) (net.Conn, error)
	gateway         net.IP
	parent          context.Context
	cancel          context.CancelFunc
	listeners       map[int]net.Listener
	started         bool
	wg              sync.WaitGroup
}

func NewIngress() *Ingress {
	dialer := &net.Dialer{}
	return &Ingress{
		discoverGateway: DiscoverWindowsGateway,
		listen:          net.Listen,
		dial:            dialer.DialContext,
		listeners:       make(map[int]net.Listener),
	}
}

// Start discovers the Windows host gateway and opens only the fixed 80/443
// listeners. Tests replace Listen with a fake bound to high loopback ports.
func (i *Ingress) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	gateway, err := i.discoverGateway(ctx)
	if err != nil {
		return err
	}
	if !validPrivateGateway(gateway) {
		return errors.New("agent: ingress host gateway must be a private WSL gateway address")
	}
	i.mu.Lock()
	if i.started {
		same := i.gateway.Equal(gateway)
		i.mu.Unlock()
		if same {
			return nil
		}
		return i.Refresh(ctx)
	}
	defer i.mu.Unlock()
	return i.startLocked(ctx, gateway)
}

// Refresh rediscovers the host gateway. A changed address tears down and
// recreates both fixed listeners so no accepted stream keeps a stale target.
func (i *Ingress) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	gateway, err := i.discoverGateway(ctx)
	if err != nil {
		return err
	}
	if !validPrivateGateway(gateway) {
		return errors.New("agent: ingress host gateway must be a private WSL gateway address")
	}
	i.mu.Lock()
	if i.started && i.gateway.Equal(gateway) {
		i.mu.Unlock()
		return nil
	}
	i.stopLocked()
	i.mu.Unlock()
	i.wg.Wait()
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.startLocked(ctx, gateway)
}

// Stop closes listeners and active relays. It is idempotent.
func (i *Ingress) Stop() error {
	i.mu.Lock()
	if !i.started {
		i.mu.Unlock()
		return nil
	}
	i.stopLocked()
	i.mu.Unlock()
	i.wg.Wait()
	return nil
}

// Gateway returns the currently configured fixed Windows host gateway.
func (i *Ingress) Gateway() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.gateway.String()
}

func (i *Ingress) startLocked(parent context.Context, gateway net.IP) error {
	listeners := make(map[int]net.Listener, 2)
	for _, port := range []int{80, 443} {
		// Devstack .test names resolve to 127.0.0.1 inside WSL. Binding only
		// loopback avoids exposing this fixed proxy to a mirrored/LAN interface.
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		listener, err := i.listen("tcp4", address)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return fmt.Errorf("agent: listen on fixed ingress %s: %w", address, err)
		}
		listeners[port] = listener
	}
	runCtx, cancel := context.WithCancel(parent)
	i.gateway = append(net.IP(nil), gateway...)
	i.parent = parent
	i.cancel = cancel
	i.listeners = listeners
	i.started = true
	for port, listener := range listeners {
		targetPort, _ := ingressTargetPort(port)
		gatewayText := gateway.String()
		i.wg.Add(1)
		go i.acceptLoop(runCtx, listener, gatewayText, targetPort)
	}
	return nil
}

func (i *Ingress) stopLocked() {
	if i.cancel != nil {
		i.cancel()
	}
	for _, listener := range i.listeners {
		_ = listener.Close()
	}
	i.listeners = make(map[int]net.Listener)
	i.started = false
	i.cancel = nil
}

func (i *Ingress) acceptLoop(ctx context.Context, listener net.Listener, gateway string, targetPort int) {
	defer i.wg.Done()
	for {
		client, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		i.wg.Add(1)
		go func(client net.Conn) {
			defer i.wg.Done()
			i.relay(ctx, client, gateway, targetPort)
		}(client)
	}
}

func (i *Ingress) relay(ctx context.Context, client net.Conn, gateway string, targetPort int) {
	defer client.Close()
	upstream, err := i.dial(ctx, "tcp4", net.JoinHostPort(gateway, strconv.Itoa(targetPort)))
	if err != nil {
		return
	}
	defer upstream.Close()
	// Stop/Refresh cancels the run context. Without this watcher an idle
	// keep-alive stream keeps io.Copy blocked and wg.Wait (Stop, Refresh) hangs
	// until the peer disconnects.
	relayDone := make(chan struct{})
	defer close(relayDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
			_ = upstream.Close()
		case <-relayDone:
		}
	}()
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, client)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, upstream)
	if tcp, ok := client.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func ingressTargetPort(listenPort int) (int, error) {
	switch listenPort {
	case 80:
		return 8080, nil
	case 443:
		return 8443, nil
	default:
		return 0, fmt.Errorf("agent: unsupported ingress listener port %d", listenPort)
	}
}

// DiscoverWindowsGateway reads the WSL default route without accepting an
// address from the daemon or an ingress request.
func DiscoverWindowsGateway(ctx context.Context) (net.IP, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return nil, fmt.Errorf("agent: discover Windows host gateway: %w", err)
	}
	return parseWindowsGateway(out)
}

func parseWindowsGateway(output []byte) (net.IP, error) {
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "default" && fields[1] == "via" {
			ip := net.ParseIP(fields[2])
			if validPrivateGateway(ip) {
				return ip, nil
			}
		}
	}
	return nil, errors.New("agent: WSL default route has no private gateway")
}
