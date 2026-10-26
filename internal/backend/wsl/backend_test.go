package wslbackend

import (
	"context"
	"errors"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
)

func TestSkeletonReturnsTypedNotImplemented(t *testing.T) {
	b := New(nil)
	if _, err := b.Prepare(context.Background(), backend.SiteSpec{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Prepare error = %v, want ErrNotImplemented", err)
	}
	if err := b.Health(context.Background()); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Health error = %v, want ErrNotImplemented", err)
	}
}
