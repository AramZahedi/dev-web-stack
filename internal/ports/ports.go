// Package ports is the placement seam for site backend ports. Linux allocates
// on the daemon host; Windows delegates allocation to the WSL agent.
package ports

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

// ErrNoAllocator is returned by a Local provider with no allocator.
var ErrNoAllocator = errors.New("ports: local allocator is nil")

// ErrNoAgent is returned when the WSL agent port provider has no client.
var ErrNoAgent = errors.New("ports: agent client is nil")

// Provider allocates, releases, and reserves backend ports for one site.
type Provider interface {
	Allocate(ctx context.Context, siteID string, kind domain.PortKind) (int, error)
	Release(ctx context.Context, siteID string, port int)
	Reserve(ctx context.Context, siteID string, port int) error
}

// Local wraps the existing host allocator.
type Local struct {
	Allocator *domain.PortAllocator
}

// NewLocal returns a host allocator provider.
func NewLocal(allocator *domain.PortAllocator) *Local {
	return &Local{Allocator: allocator}
}

// Allocate hands out one host loopback port.
func (l *Local) Allocate(_ context.Context, _ string, kind domain.PortKind) (int, error) {
	if l == nil || l.Allocator == nil {
		return 0, ErrNoAllocator
	}
	return l.Allocator.Allocate(kind)
}

// Release frees one host port.
func (l *Local) Release(_ context.Context, _ string, port int) {
	if l != nil && l.Allocator != nil {
		l.Allocator.Release(port)
	}
}

// Reserve restores a persisted host allocation.
func (l *Local) Reserve(_ context.Context, _ string, port int) error {
	if l == nil || l.Allocator == nil {
		return ErrNoAllocator
	}
	return l.Allocator.Reserve(port)
}

// AgentProvider delegates site-port ownership to the WSL agent. It caches
// successful allocations for idempotent prepare paths and restores persisted
// allocations after daemon restarts.
type AgentProvider struct {
	Client agentclient.API
	mu     sync.Mutex
	ports  map[string]map[domain.PortKind]int
}

func NewAgentProvider(client agentclient.API) *AgentProvider {
	return &AgentProvider{Client: client, ports: make(map[string]map[domain.PortKind]int)}
}

func (p *AgentProvider) Allocate(ctx context.Context, siteID string, kind domain.PortKind) (int, error) {
	if p == nil || p.Client == nil {
		return 0, ErrNoAgent
	}
	if !validKind(kind) {
		return 0, fmt.Errorf("ports: unsupported port kind %s", kind)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if cached := p.ports[siteID][kind]; cached != 0 {
		return cached, nil
	}
	resp, err := p.Client.AllocatePorts(ctx, agentPortRequest(siteID, kind, 0))
	if err != nil {
		return 0, err
	}
	port := agentPortValue(resp, kind)
	if !validPort(kind, port) {
		return 0, fmt.Errorf("ports: agent returned invalid %s port %d", kind, port)
	}
	if p.ports[siteID] == nil {
		p.ports[siteID] = make(map[domain.PortKind]int)
	}
	p.ports[siteID][kind] = port
	return port, nil
}

func (p *AgentProvider) Reserve(ctx context.Context, siteID string, port int) error {
	if p == nil || p.Client == nil {
		return ErrNoAgent
	}
	kind, ok := agentKindForPort(port)
	if !ok {
		return fmt.Errorf("ports: port %d is outside WSL backend ranges", port)
	}
	p.mu.Lock()
	if p.ports[siteID][kind] == port {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	resp, err := p.Client.AllocatePorts(ctx, agentPortRequest(siteID, kind, port))
	if err != nil {
		return err
	}
	if got := agentPortValue(resp, kind); got != port {
		return fmt.Errorf("ports: agent restored port %d as %d", port, got)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ports[siteID] == nil {
		p.ports[siteID] = make(map[domain.PortKind]int)
	}
	p.ports[siteID][kind] = port
	return nil
}

func (p *AgentProvider) Release(ctx context.Context, siteID string, port int) {
	if p == nil || p.Client == nil || port == 0 {
		return
	}
	kind, ok := agentKindForPort(port)
	if !ok {
		return
	}
	p.mu.Lock()
	if p.ports[siteID][kind] == port {
		delete(p.ports[siteID], kind)
	}
	p.mu.Unlock()
	req := agentproto.PortRequest{SiteID: siteID, Kind: kind.String()}
	if kind == domain.PortHTTP {
		req.HTTPPort = port
	} else {
		req.FastCGIPort = port
	}
	_ = p.Client.ReleasePorts(ctx, req)
}

func validKind(kind domain.PortKind) bool {
	return kind == domain.PortHTTP || kind == domain.PortFastCGI
}

func validPort(kind domain.PortKind, port int) bool {
	if kind == domain.PortHTTP {
		return port >= 41000 && port <= 42999
	}
	return kind == domain.PortFastCGI && port >= 43000 && port <= 43999
}

func agentKindForPort(port int) (domain.PortKind, bool) {
	if port >= 41000 && port <= 42999 {
		return domain.PortHTTP, true
	}
	if port >= 43000 && port <= 43999 {
		return domain.PortFastCGI, true
	}
	return 0, false
}

func agentPortRequest(siteID string, kind domain.PortKind, requested int) agentproto.PortRequest {
	req := agentproto.PortRequest{SiteID: siteID, Kind: kind.String()}
	if requested > 0 {
		if kind == domain.PortHTTP {
			req.RequestedHTTP = requested
		} else {
			req.RequestedFastCGI = requested
		}
	}
	return req
}

func agentPortValue(resp agentproto.PortResponse, kind domain.PortKind) int {
	if kind == domain.PortHTTP {
		return resp.HTTPPort
	}
	return resp.FastCGIPort
}

var _ Provider = (*AgentProvider)(nil)
