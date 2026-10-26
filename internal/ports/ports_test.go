package ports

import (
	"context"
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

func TestLocalProviderAllocatesAndReleases(t *testing.T) {
	p := NewLocal(domain.NewPortAllocator())
	port, err := p.Allocate(context.Background(), "site", domain.PortHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if port < 41000 || port > 42999 {
		t.Fatalf("port = %d", port)
	}
	p.Release(context.Background(), "site", port)
	if err := p.Reserve(context.Background(), "site", port); err != nil {
		t.Fatalf("Reserve after Release: %v", err)
	}
	if err := p.Reserve(context.Background(), "site", port); err == nil {
		t.Fatal("duplicate Reserve succeeded")
	}
	p.Release(context.Background(), "site", port)
}

type fakeProvider struct{}

func (fakeProvider) Allocate(context.Context, string, domain.PortKind) (int, error) {
	return 42000, nil
}
func (fakeProvider) Release(context.Context, string, int)       {}
func (fakeProvider) Reserve(context.Context, string, int) error { return nil }

func TestProviderContract(t *testing.T) {
	var p Provider = fakeProvider{}
	port, err := p.Allocate(context.Background(), "x", domain.PortHTTP)
	if err != nil || port != 42000 {
		t.Fatalf("Allocate = %d, %v", port, err)
	}
	if _, err := NewLocal(nil).Allocate(context.Background(), "x", domain.PortHTTP); !errors.Is(err, ErrNoAllocator) {
		t.Fatalf("nil allocator error = %v", err)
	}
}
