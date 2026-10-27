// Package wslbackend adapts the authenticated WSL agent API to the daemon's
// backend and port-placement seams.
package wslbackend

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var (
	ErrNilClient = errors.New("backend/wsl: agent client is nil")
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

func (b *Backend) InstallCA(ctx context.Context, pem []byte) error {
	if err := b.check(); err != nil {
		return err
	}
	return b.Client.InstallCA(ctx, pem)
}

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

func toWSLPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") {
		return p
	}
	if strings.HasPrefix(strings.ToLower(p), `\\wsl.localhost\`) || strings.HasPrefix(strings.ToLower(p), `\\wsl$\`) {
		slash := strings.ReplaceAll(p, `\`, "/")
		parts := strings.Split(strings.TrimPrefix(slash, "//"), "/")
		if len(parts) >= 2 {
			return "/" + strings.Join(parts[2:], "/")
		}
	}
	if len(p) >= 2 && p[1] == ':' {
		drive := strings.ToLower(string(p[0]))
		rest := strings.ReplaceAll(p[2:], `\`, "/")
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return "/mnt/" + drive + rest
	}
	return strings.ReplaceAll(p, `\`, "/")
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
		ID: spec.ID, Kind: spec.Kind, Domain: spec.Domain, HostPath: spec.HostPath, BackendPath: toWSLPath(spec.BackendPath),
		DocumentRoot: toWSLPath(backendDocumentRoot), ProxyDocumentRoot: proxyRoot, SourceMode: mode,
		Command: spec.Command, Env: cloneMap(spec.Env), HTTPPort: spec.HTTPPort, FastCGIPort: spec.FastCGIPort,
		SPAFallback: spec.SPAFallback, FrontController: spec.FrontController, RuntimeVersion: spec.RuntimeVersion,
		PHPVersion: spec.PHPVersion, PHPBuild: spec.PHPBuild, PHPBuildPath: toWSLPath(spec.PHPBuildPath),
		PHPIniPath: toWSLPath(spec.PHPIniPath), FPMConfigPath: toWSLPath(spec.FPMConfigPath), PHPHealthScript: toWSLPath(spec.PHPHealthScript),
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
