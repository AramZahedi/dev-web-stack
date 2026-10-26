package sites

import (
	"context"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

func TestBackendHostDefaultAndRecorded(t *testing.T) {
	s := &Service{backendHosts: map[string]string{}}
	if got := s.backendHost("missing"); got != "127.0.0.1" {
		t.Fatalf("default backend host = %q", got)
	}
	s.recordBackendHost("site", backend.PreparedSite{BackendHost: "10.0.0.9"})
	if got := s.backendHost("site"); got != "10.0.0.9" {
		t.Fatalf("recorded backend host = %q", got)
	}
	s.recordBackendHost("empty", backend.PreparedSite{})
	if got := s.backendHost("empty"); got != "127.0.0.1" {
		t.Fatalf("empty prepared host = %q", got)
	}
}

type staticPortProvider struct{ port int }

func (p staticPortProvider) Allocate(context.Context, string, domain.PortKind) (int, error) {
	return p.port, nil
}
func (staticPortProvider) Release(context.Context, string, int) {}
func (staticPortProvider) Reserve(context.Context, string, int) error {
	return nil
}

func TestPortProviderSeam(t *testing.T) {
	s := &Service{deps: Deps{PortProvider: staticPortProvider{port: 42001}}}
	port, err := s.allocatePort(context.Background(), "site", domain.PortHTTP)
	if err != nil {
		t.Fatal(err)
	}
	if port != 42001 {
		t.Fatalf("port = %d", port)
	}
}
