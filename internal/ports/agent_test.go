package ports

import (
	"context"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

type agentFake struct {
	agentclient.API
	allocations []agentproto.PortRequest
	releases    []agentproto.PortRequest
	response    agentproto.PortResponse
}

func (f *agentFake) AllocatePorts(_ context.Context, req agentproto.PortRequest) (agentproto.PortResponse, error) {
	f.allocations = append(f.allocations, req)
	return f.response, nil
}
func (f *agentFake) ReleasePorts(_ context.Context, req agentproto.PortRequest) error {
	f.releases = append(f.releases, req)
	return nil
}

func TestAgentProviderAllocatesCachesAndReleasesAgentPorts(t *testing.T) {
	agent := &agentFake{response: agentproto.PortResponse{HTTPPort: 41800, FastCGIPort: 43100, Reused: true}}
	provider := NewAgentProvider(agent)
	for i := 0; i < 2; i++ {
		port, err := provider.Allocate(context.Background(), "site", domain.PortHTTP)
		if err != nil || port != 41800 {
			t.Fatalf("Allocate = %d, %v", port, err)
		}
	}
	if len(agent.allocations) != 1 {
		t.Fatalf("allocation calls = %d, want one", len(agent.allocations))
	}
	if err := provider.Reserve(context.Background(), "other", 43100); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if got := agent.allocations[1]; got.Kind != "fastcgi" || got.RequestedFastCGI != 43100 {
		t.Fatalf("reserve request = %+v", got)
	}
	provider.Release(context.Background(), "site", 41800)
	if len(agent.releases) != 1 || agent.releases[0].HTTPPort != 41800 {
		t.Fatalf("release requests = %+v", agent.releases)
	}
}

func TestAgentProviderRejectsPortsOutsideWSLRanges(t *testing.T) {
	provider := NewAgentProvider(&agentFake{})
	if err := provider.Reserve(context.Background(), "site", 12345); err == nil {
		t.Fatal("Reserve accepted a port outside WSL allocation ranges")
	}
}
