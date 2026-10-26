// Package ports is the placement seam for site backend ports. Linux allocates
// on the daemon host; Windows delegates allocation to the WSL agent.
package ports

import (
	"context"
	"errors"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

// ErrNoAllocator is returned by a Local provider with no allocator.
var ErrNoAllocator = errors.New("ports: local allocator is nil")

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
