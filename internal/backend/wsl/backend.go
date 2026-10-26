// Package wslbackend adapts the authenticated WSL agent API to the daemon's
// backend and port-placement seams.
package wslbackend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var (
	ErrNilClient = errors.New("backend/wsl: agent client is nil")
	// ErrCAInstallUnsupported marks the missing frozen-agent operation for
	// installing the Windows daemon's public CA into WSL trust bundles.
	ErrCAInstallUnsupported = errors.New("backend/wsl: frozen agent API has no install-CA operation")
)

// Backend implements the daemon backend contract using the pinned-HTTPS
// agentclient.API. The API client itself owns bearer authentication, timeout,
// and certificate-pin errors.
type Backend struct {
	Client agentclient.API
}

func New(client agentclient.API) *Backend { return &Backend{Client: client} }

func (b *Backend) Prepare(ctx context.Context, spec backend.SiteSpec) (backend.PreparedSite, error) {
	if err := b.check(); err != nil {
		return backend.PreparedSite{}, err
	}
	resp, err := b.Client.PrepareSite(ctx, agentproto.SitePrepareRequest{Site: toAgentSite(spec)})
	if err != nil {
		return backend.PreparedSite{}, err
	}
	return preparedSite(resp, spec), nil
}

func (b *Backend) Start(ctx context.Context, id string) (backend.ProcessInfo, error) {
	if err := b.check(); err != nil {
		return backend.ProcessInfo{}, err
	}
	resp, err := b.Client.StartSite(ctx, id, agentproto.SiteActionRequest{})
	if err != nil {
		return backend.ProcessInfo{}, err
	}
	return processInfo(resp), nil
}

func (b *Backend) Stop(ctx context.Context, id string, graceful bool) error {
	if err := b.check(); err != nil {
		return err
	}
	_, err := b.Client.StopSite(ctx, id, agentproto.SiteActionRequest{Graceful: graceful})
	return err
}

func (b *Backend) Status(ctx context.Context, id string) (backend.Status, error) {
	if err := b.check(); err != nil {
		return backend.Status{}, err
	}
	resp, err := b.Client.SiteStatus(ctx, id)
	if err != nil {
		return backend.Status{}, err
	}
	return backend.Status{State: model.SiteStatus(resp.Status), PID: resp.PID, Message: resp.Message}, nil
}

func (b *Backend) Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	return b.Client.SiteLogs(ctx, id, agentproto.SiteLogsRequest{Follow: follow})
}

func (b *Backend) Exec(ctx context.Context, id string, argv []string, env map[string]string) (backend.Result, error) {
	if err := b.check(); err != nil {
		return backend.Result{}, err
	}
	resp, err := b.Client.ExecSite(ctx, id, agentproto.SiteExecRequest{Argv: argv, Env: env})
	if err != nil {
		return backend.Result{}, err
	}
	return backend.Result{ExitCode: resp.ExitCode, Stdout: resp.Stdout, Stderr: resp.Stderr}, nil
}

func (b *Backend) EnsureRuntime(ctx context.Context, kind, version string) (model.RuntimeBuild, error) {
	if err := b.check(); err != nil {
		return model.RuntimeBuild{}, err
	}
	resp, err := b.Client.InstallRuntime(ctx, agentproto.RuntimeRequest{Kind: kind, Version: version})
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	return runtimeBuild(resp), nil
}

func (b *Backend) EnsureRuntimeBuild(ctx context.Context, buildID string) (model.RuntimeBuild, error) {
	if err := b.check(); err != nil {
		return model.RuntimeBuild{}, err
	}
	resp, err := b.Client.InstallRuntime(ctx, agentproto.RuntimeRequest{BuildID: buildID})
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	return runtimeBuild(resp), nil
}

func (b *Backend) RemoveRuntime(ctx context.Context, buildID string) error {
	if err := b.check(); err != nil {
		return err
	}
	return b.Client.RemoveRuntime(ctx, agentproto.RuntimeRequest{BuildID: buildID})
}

func (b *Backend) InstallCA(context.Context, []byte) error { return ErrCAInstallUnsupported }

func (b *Backend) Health(ctx context.Context) error {
	if err := b.check(); err != nil {
		return err
	}
	_, err := b.Client.Health(ctx)
	return err
}

func (b *Backend) PreparePHP(ctx context.Context, siteID, generation string, spec backend.SiteSpec) (backend.PreparedSite, error) {
	if err := b.check(); err != nil {
		return backend.PreparedSite{}, err
	}
	resp, err := b.Client.PreparePHP(ctx, siteID, agentproto.PHPPrepareRequest{
		Generation: generation,
		Site:       toAgentSite(spec),
	})
	if err != nil {
		return backend.PreparedSite{}, err
	}
	return preparedSite(resp, spec), nil
}

func (b *Backend) StartPHP(ctx context.Context, siteID, generation string) (backend.ProcessInfo, error) {
	if err := b.check(); err != nil {
		return backend.ProcessInfo{}, err
	}
	resp, err := b.Client.StartPHP(ctx, siteID, agentproto.PHPActionRequest{Generation: generation})
	if err != nil {
		return backend.ProcessInfo{}, err
	}
	return processInfo(resp), nil
}

func (b *Backend) StopPHP(ctx context.Context, siteID, generation string) error {
	if err := b.check(); err != nil {
		return err
	}
	_, err := b.Client.StopPHP(ctx, siteID, agentproto.PHPActionRequest{Generation: generation})
	return err
}

func (b *Backend) check() error {
	if b == nil || b.Client == nil {
		return ErrNilClient
	}
	return nil
}

func toAgentSite(spec backend.SiteSpec) agentproto.SiteSpec {
	documentRoot := strings.TrimSpace(spec.DocumentRoot)
	backendDocumentRoot := strings.TrimSpace(spec.BackendPath)
	if documentRoot != "" && documentRoot != "." {
		backendDocumentRoot = path.Join(backendDocumentRoot, filepathToSlash(documentRoot))
	}
	proxyRoot := strings.TrimRight(spec.HostPath, `\/`)
	if documentRoot != "" && documentRoot != "." {
		proxyRoot += `\` + strings.ReplaceAll(filepathToSlash(documentRoot), "/", `\`)
	}
	mode := ""
	switch {
	case strings.HasPrefix(strings.ToLower(spec.HostPath), `\\wsl.localhost\`), strings.HasPrefix(strings.ToLower(spec.HostPath), `\\wsl$\`):
		mode = string(model.SourceWSL)
	case len(spec.HostPath) >= 3 && spec.HostPath[1] == ':' && (spec.HostPath[2] == '\\' || spec.HostPath[2] == '/'):
		mode = string(model.SourceWindows)
	}
	return agentproto.SiteSpec{
		ID: spec.ID, Kind: spec.Kind, HostPath: spec.HostPath, BackendPath: spec.BackendPath,
		DocumentRoot: backendDocumentRoot, ProxyDocumentRoot: proxyRoot, SourceMode: mode,
		Command: spec.Command, Env: cloneMap(spec.Env), HTTPPort: spec.HTTPPort, FastCGIPort: spec.FastCGIPort,
		SPAFallback: spec.SPAFallback, FrontController: spec.FrontController, RuntimeVersion: spec.RuntimeVersion,
		PHPVersion: spec.PHPVersion, PHPBuild: spec.PHPBuild, PHPBuildPath: spec.PHPBuildPath,
		PHPIniPath: spec.PHPIniPath, FPMConfigPath: spec.FPMConfigPath, PHPHealthScript: spec.PHPHealthScript,
	}
}

func filepathToSlash(value string) string { return strings.ReplaceAll(value, `\`, "/") }

func preparedSite(resp agentproto.SitePrepareResponse, spec backend.SiteSpec) backend.PreparedSite {
	host := strings.TrimSpace(resp.BackendHost)
	if host == "" {
		host = "127.0.0.1"
	}
	httpPort := resp.HTTPPort
	if httpPort == 0 {
		httpPort = spec.HTTPPort
	}
	fastCGIPort := resp.FastCGIPort
	if fastCGIPort == 0 {
		fastCGIPort = spec.FastCGIPort
	}
	return backend.PreparedSite{BackendHost: host, HTTPPort: httpPort, FastCGIPort: fastCGIPort}
}

func processInfo(resp agentproto.SiteActionResponse) backend.ProcessInfo {
	return backend.ProcessInfo{PID: resp.PID, StartedAt: time.Now()}
}

func runtimeBuild(resp agentproto.RuntimeResponse) model.RuntimeBuild {
	return model.RuntimeBuild{
		ID: resp.ID, Kind: resp.Kind, Version: resp.Version, Backend: model.BackendWSL,
		Path: resp.Path, Checksum: resp.Checksum,
	}
}

func cloneMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

var _ backend.Backend = (*Backend)(nil)
var _ backend.PHPStager = (*Backend)(nil)

// PortProvider delegates allocation to the WSL agent and caches an allocation
// per site/kind so repeated prepare paths reuse the persisted WSL socket.
type PortProvider struct {
	client agentclient.API
	mu     sync.Mutex
	ports  map[string]map[domain.PortKind]int
}

func NewPortProvider(client agentclient.API) *PortProvider {
	return &PortProvider{client: client, ports: make(map[string]map[domain.PortKind]int)}
}

func (p *PortProvider) Allocate(ctx context.Context, siteID string, kind domain.PortKind) (int, error) {
	if p == nil || p.client == nil {
		return 0, ErrNilClient
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if byKind := p.ports[siteID]; byKind != nil {
		if port := byKind[kind]; port != 0 {
			return port, nil
		}
	}
	resp, err := p.client.AllocatePorts(ctx, portRequest(siteID, kind, 0))
	if err != nil {
		return 0, err
	}
	port := responsePort(resp, kind)
	if !validAgentPort(port, kind) {
		return 0, fmt.Errorf("backend/wsl: agent returned invalid %s port %d", kind, port)
	}
	if p.ports[siteID] == nil {
		p.ports[siteID] = make(map[domain.PortKind]int)
	}
	p.ports[siteID][kind] = port
	return port, nil
}

func (p *PortProvider) Reserve(ctx context.Context, siteID string, port int) error {
	if p == nil || p.client == nil {
		return ErrNilClient
	}
	kind := domain.PortHTTP
	if port >= 43000 && port <= 43999 {
		kind = domain.PortFastCGI
	}
	if !validAgentPort(port, kind) {
		return fmt.Errorf("backend/wsl: port %d is outside the %s allocation range", port, kind)
	}
	resp, err := p.client.AllocatePorts(ctx, portRequest(siteID, kind, port))
	if err != nil {
		return err
	}
	if got := responsePort(resp, kind); got != port {
		return fmt.Errorf("backend/wsl: agent restored port %d as %d", port, got)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ports[siteID] == nil {
		p.ports[siteID] = make(map[domain.PortKind]int)
	}
	p.ports[siteID][kind] = port
	return nil
}

func (p *PortProvider) Release(ctx context.Context, siteID string, port int) {
	if p == nil || p.client == nil || port == 0 {
		return
	}
	kind := domain.PortHTTP
	if port >= 43000 && port <= 43999 {
		kind = domain.PortFastCGI
	}
	p.mu.Lock()
	if p.ports[siteID][kind] == port {
		delete(p.ports[siteID], kind)
	}
	p.mu.Unlock()
	req := agentproto.PortRequest{SiteID: siteID, Kind: kind.String()}
	if kind == domain.PortFastCGI {
		req.FastCGIPort = port
	} else {
		req.HTTPPort = port
	}
	_ = p.client.ReleasePorts(ctx, req)
}

func portRequest(siteID string, kind domain.PortKind, requested int) agentproto.PortRequest {
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

func responsePort(resp agentproto.PortResponse, kind domain.PortKind) int {
	if kind == domain.PortFastCGI {
		return resp.FastCGIPort
	}
	return resp.HTTPPort
}

func validAgentPort(port int, kind domain.PortKind) bool {
	if kind == domain.PortFastCGI {
		return port >= 43000 && port <= 43999
	}
	return kind == domain.PortHTTP && port >= 41000 && port <= 42999
}

var _ interface {
	Allocate(context.Context, string, domain.PortKind) (int, error)
	Release(context.Context, string, int)
	Reserve(context.Context, string, int) error
} = (*PortProvider)(nil)
