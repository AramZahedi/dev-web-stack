package wslbackend

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

type fakeAgent struct {
	agentclient.API
	prepareRequest agentproto.SitePrepareRequest
	prepareResult  agentproto.SitePrepareResponse
	prepareError   error
	allocated      []agentproto.PortRequest
	released       []agentproto.PortRequest
	portResult     agentproto.PortResponse
	started        []string
	stopped        []string
	status         agentproto.SiteStatusResponse
	logs           io.ReadCloser
	execRequest    agentproto.SiteExecRequest
	execResult     agentproto.SiteExecResponse
}

func (f *fakeAgent) PrepareSite(_ context.Context, req agentproto.SitePrepareRequest) (agentproto.SitePrepareResponse, error) {
	f.prepareRequest = req
	return f.prepareResult, f.prepareError
}
func (f *fakeAgent) AllocatePorts(_ context.Context, req agentproto.PortRequest) (agentproto.PortResponse, error) {
	f.allocated = append(f.allocated, req)
	return f.portResult, nil
}
func (f *fakeAgent) ReleasePorts(_ context.Context, req agentproto.PortRequest) error {
	f.released = append(f.released, req)
	return nil
}
func (f *fakeAgent) StartSite(_ context.Context, id string, _ agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	f.started = append(f.started, id)
	return agentproto.SiteActionResponse{Status: "running", PID: 41}, nil
}
func (f *fakeAgent) StopSite(_ context.Context, id string, _ agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	f.stopped = append(f.stopped, id)
	return agentproto.SiteActionResponse{Status: "stopped"}, nil
}
func (f *fakeAgent) SiteStatus(context.Context, string) (agentproto.SiteStatusResponse, error) {
	return f.status, nil
}
func (f *fakeAgent) SiteLogs(context.Context, string, agentproto.SiteLogsRequest) (io.ReadCloser, error) {
	return f.logs, nil
}
func (f *fakeAgent) ExecSite(_ context.Context, _ string, req agentproto.SiteExecRequest) (agentproto.SiteExecResponse, error) {
	f.execRequest = req
	return f.execResult, nil
}

func TestPrepareMapsBackendAndWindowsProxyDocumentRoots(t *testing.T) {
	agent := &fakeAgent{prepareResult: agentproto.SitePrepareResponse{
		BackendHost: "127.0.0.1", HTTPPort: 41812, FastCGIPort: 43101,
	}}
	backendClient := New(agent)
	prepared, err := backendClient.Prepare(context.Background(), backend.SiteSpec{
		ID: "site-id", Kind: "php", HostPath: `C:\devstack\projects\shop`,
		BackendPath: "/mnt/c/devstack/projects/shop", DocumentRoot: "public",
		HTTPPort: 41812, FastCGIPort: 43101, FrontController: true,
		PHPVersion: "8.3", PHPBuild: "php-8.3-test", PHPBuildPath: "/data/devstack/runtimes/php/test",
		PHPIniPath: "/data/devstack/sites/site-id/php.ini", FPMConfigPath: "/data/devstack/sites/site-id/fpm.conf",
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared.BackendHost != "127.0.0.1" || prepared.HTTPPort != 41812 || prepared.FastCGIPort != 43101 {
		t.Fatalf("PreparedSite = %+v", prepared)
	}
	site := agent.prepareRequest.Site
	if site.BackendPath != "/mnt/c/devstack/projects/shop" || site.DocumentRoot != "/mnt/c/devstack/projects/shop/public" {
		t.Fatalf("agent backend paths = backend %q document %q", site.BackendPath, site.DocumentRoot)
	}
	if site.ProxyDocumentRoot != `C:\devstack\projects\shop\public` {
		t.Fatalf("ProxyDocumentRoot = %q", site.ProxyDocumentRoot)
	}
	if site.FastCGIPort != 43101 || site.PHPBuildPath == "" || !site.FrontController {
		t.Fatalf("agent site spec lost PHP configuration: %+v", site)
	}
}

func TestPrepareSurfacesTypedAgentAuthenticationFailure(t *testing.T) {
	want := &agentproto.Error{Code: agentproto.CodeUnauthenticated, Message: "bad token", Status: 401}
	_, err := New(&fakeAgent{prepareError: want}).Prepare(context.Background(), backend.SiteSpec{ID: "site"})
	if !errors.Is(err, agentproto.ErrUnauthenticated) {
		t.Fatalf("Prepare error = %v, want typed unauthenticated error", err)
	}
}

func TestLifecycleStatusLogsAndExecUseAgent(t *testing.T) {
	logReader := io.NopCloser(strings.NewReader("agent log"))
	agent := &fakeAgent{
		status: agentproto.SiteStatusResponse{Status: "running", PID: 77},
		logs: logReader, execResult: agentproto.SiteExecResponse{ExitCode: 3, Stdout: "out", Stderr: "err"},
	}
	b := New(agent)
	if info, err := b.Start(context.Background(), "site"); err != nil || info.PID != 41 || len(agent.started) != 1 {
		t.Fatalf("Start = %+v, %v; calls=%v", info, err, agent.started)
	}
	if err := b.Stop(context.Background(), "site", true); err != nil || len(agent.stopped) != 1 {
		t.Fatalf("Stop = %v; calls=%v", err, agent.stopped)
	}
	status, err := b.Status(context.Background(), "site")
	if err != nil || status.State != "running" || status.PID != 77 {
		t.Fatalf("Status = %+v, %v", status, err)
	}
	logs, err := b.Logs(context.Background(), "site", true)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	data, err := io.ReadAll(logs)
	if err != nil || string(data) != "agent log" {
		t.Fatalf("Logs content = %q, %v", data, err)
	}
	result, err := b.Exec(context.Background(), "site", []string{"php", "-v"}, map[string]string{"A": "B"})
	if err != nil || result.ExitCode != 3 || result.Stdout != "out" || result.Stderr != "err" || agent.execRequest.Env["A"] != "B" {
		t.Fatalf("Exec = %+v, %v; request=%+v", result, err, agent.execRequest)
	}
}

func TestPortProviderAllocatesAndReleasesThroughAgentAndReusesSitePort(t *testing.T) {
	agent := &fakeAgent{portResult: agentproto.PortResponse{HTTPPort: 41812, Reused: true}}
	provider := NewPortProvider(agent)
	first, err := provider.Allocate(context.Background(), "site", domain.PortHTTP)
	if err != nil || first != 41812 {
		t.Fatalf("first Allocate = %d, %v", first, err)
	}
	second, err := provider.Allocate(context.Background(), "site", domain.PortHTTP)
	if err != nil || second != first {
		t.Fatalf("reused Allocate = %d, %v", second, err)
	}
	if len(agent.allocated) != 1 {
		t.Fatalf("agent allocations = %d, want one cached allocation", len(agent.allocated))
	}
	if agent.allocated[0].Kind != "http" || agent.allocated[0].SiteID != "site" {
		t.Fatalf("allocation request = %+v", agent.allocated[0])
	}
	provider.Release(context.Background(), "site", first)
	if len(agent.released) != 1 || agent.released[0].HTTPPort != first || agent.released[0].RequestedHTTP != 0 {
		t.Fatalf("agent release requests = %+v, want allocated HTTPPort %d", agent.released, first)
	}
}
