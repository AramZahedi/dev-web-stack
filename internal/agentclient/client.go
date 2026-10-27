// Package agentclient is the typed HTTPS client for the managed WSL agent.
// It requires a bearer token and pins the agent certificate fingerprint.
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

const maxBody = 16 << 20

// Config configures a client.
type Config struct {
	BaseURL   string
	Token     string
	PinSHA256 string
	Timeout   time.Duration
}

// API is the complete typed agent surface. A fake agent and the real client
// both implement it; adding an endpoint without adding it here is a compile
// error at the call site that owns the contract.
type API interface {
	Health(context.Context) (agentproto.HealthResponse, error)
	Version(context.Context) (agentproto.VersionResponse, error)
	Capabilities(context.Context) (agentproto.CapabilitiesResponse, error)
	AllocatePorts(context.Context, agentproto.PortRequest) (agentproto.PortResponse, error)
	ReleasePorts(context.Context, agentproto.PortRequest) error
	ValidatePorts(context.Context, agentproto.PortRequest) (agentproto.PortValidateResponse, error)
	PrepareSite(context.Context, agentproto.SitePrepareRequest) (agentproto.SitePrepareResponse, error)
	StartSite(context.Context, string, agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error)
	StopSite(context.Context, string, agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error)
	SiteStatus(context.Context, string) (agentproto.SiteStatusResponse, error)
	SiteLogs(context.Context, string, agentproto.SiteLogsRequest) (io.ReadCloser, error)
	ExecSite(context.Context, string, agentproto.SiteExecRequest) (agentproto.SiteExecResponse, error)
	PreparePHP(context.Context, string, agentproto.PHPPrepareRequest) (agentproto.SitePrepareResponse, error)
	StartPHP(context.Context, string, agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error)
	StopPHP(context.Context, string, agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error)
	BuildSite(context.Context, string, agentproto.BuildRequest) (agentproto.BuildResponse, error)
	TerminalTicket(context.Context, string, agentproto.TerminalTicketRequest) (agentproto.TerminalTicketResponse, error)
	InstallRuntime(context.Context, agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error)
	RemoveRuntime(context.Context, agentproto.RuntimeRequest) error
	ListRuntimes(context.Context) (agentproto.RuntimeListResponse, error)
	ServiceAction(context.Context, agentproto.ServiceRequest) (agentproto.ServiceResponse, error)
	ServiceStatus(context.Context, string) (agentproto.ServiceResponse, error)
	ServiceLogs(context.Context, string, agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error)
	ServiceConfig(context.Context, string) (agentproto.ServiceConfigResponse, error)
	ServiceContent(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error)
	ServiceApply(context.Context, agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error)
	ServiceReset(context.Context, agentproto.ServiceConfigRequest) error
	ServiceCredential(context.Context, agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error)
	WebTargets(context.Context, agentproto.WebTargetsRequest) error
	SyncWeb(context.Context, agentproto.WebTargetsRequest) (agentproto.WebSyncResponse, error)
	SyncHosts(context.Context, agentproto.HostsRequest) (agentproto.HostsResponse, error)
	EnsureCert(context.Context, agentproto.CertRequest) (agentproto.CertResponse, error)
	CA(context.Context) (agentproto.CAResponse, error)
	InstallCA(context.Context, []byte) error
	ValidatePath(context.Context, agentproto.PathValidateRequest) (agentproto.PathValidateResponse, error)
	CopySource(context.Context, agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error)
}

// Client is safe for concurrent use.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

var _ API = (*Client)(nil)

// New validates the HTTPS endpoint, token, and pin, and builds a pinned
// transport. It never accepts plain HTTP.
func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("agentclient: base URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("agentclient: base URL must be an https:// URL")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("agentclient: token is empty")
	}
	if len(cfg.PinSHA256) != 64 {
		return nil, errors.New("agentclient: certificate pin must be 64 hex characters")
	}
	tlsConfig, err := agent.PinnedConfig(cfg.PinSHA256)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	transport := &http.Transport{
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        8,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &Client{
		baseURL: base,
		token:   cfg.Token,
		http:    &http.Client{Transport: transport, Timeout: timeout},
	}, nil
}

// Close releases idle connections.
func (c *Client) Close() error {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("agentclient: encode %s: %w", path, err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("agentclient: request %s: %w", path, err)
	}
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if isTimeout(err) {
			return fmt.Errorf("%w: %v", agentproto.ErrTimeout, err)
		}
		return fmt.Errorf("agentclient: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("agentclient: read %s: %w", path, err)
	}
	if len(data) > maxBody {
		return fmt.Errorf("agentclient: %s response exceeds %d bytes", path, maxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return wireError(resp.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("agentclient: decode %s: %w", path, err)
		}
	}
	return nil
}

func (c *Client) open(ctx context.Context, method, path string, in any) (io.ReadCloser, error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if isTimeout(err) {
			return nil, fmt.Errorf("%w: %v", agentproto.ErrTimeout, err)
		}
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, wireError(resp.StatusCode, data)
	}
	return resp.Body, nil
}

func wireError(status int, data []byte) error {
	var wire agentproto.ErrorResponse
	_ = json.Unmarshal(data, &wire)
	if wire.Code == "" {
		wire.Code = codeForStatus(status)
	}
	if wire.Message == "" {
		wire.Message = http.StatusText(status)
	}
	return &agentproto.Error{Code: wire.Code, Message: wire.Message, Status: status}
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return agentproto.CodeUnauthenticated
	case http.StatusForbidden:
		return agentproto.CodeForbidden
	case http.StatusNotFound:
		return agentproto.CodeNotFound
	case http.StatusConflict:
		return agentproto.CodeConflict
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return agentproto.CodeTimeout
	case http.StatusUpgradeRequired:
		return agentproto.CodeVersionMismatch
	case http.StatusUnprocessableEntity:
		return agentproto.CodeUnsupported
	default:
		return agentproto.CodeInternal
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// Health returns the agent liveness response.
func (c *Client) Health(ctx context.Context) (agentproto.HealthResponse, error) {
	var out agentproto.HealthResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathHealth, nil, &out)
	return out, err
}

// Version returns the agent protocol and build version.
func (c *Client) Version(ctx context.Context) (agentproto.VersionResponse, error) {
	var out agentproto.VersionResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathVersion, nil, &out)
	return out, err
}

// Capabilities lists optional agent features.
func (c *Client) Capabilities(ctx context.Context) (agentproto.CapabilitiesResponse, error) {
	var out agentproto.CapabilitiesResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathCapabilities, nil, &out)
	return out, err
}

// AllocatePorts asks the agent to bind or reuse site ports.
func (c *Client) AllocatePorts(ctx context.Context, req agentproto.PortRequest) (agentproto.PortResponse, error) {
	var out agentproto.PortResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathPortsAllocate, req, &out)
	return out, err
}

// ReleasePorts releases site ports in the agent.
func (c *Client) ReleasePorts(ctx context.Context, req agentproto.PortRequest) error {
	return c.do(ctx, http.MethodPost, agentproto.PathPortsRelease, req, nil)
}

// ValidatePorts checks that requested ports are free.
func (c *Client) ValidatePorts(ctx context.Context, req agentproto.PortRequest) (agentproto.PortValidateResponse, error) {
	var out agentproto.PortValidateResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathPortsValidate, req, &out)
	return out, err
}

// PrepareSite validates and stages a site.
func (c *Client) PrepareSite(ctx context.Context, req agentproto.SitePrepareRequest) (agentproto.SitePrepareResponse, error) {
	var out agentproto.SitePrepareResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSitePrepareFor(req.Site.ID), req, &out)
	return out, err
}

// StartSite starts a prepared site.
func (c *Client) StartSite(ctx context.Context, id string, req agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	var out agentproto.SiteActionResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSiteStartFor(id), req, &out)
	return out, err
}

// StopSite stops a site.
func (c *Client) StopSite(ctx context.Context, id string, req agentproto.SiteActionRequest) (agentproto.SiteActionResponse, error) {
	var out agentproto.SiteActionResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSiteStopFor(id), req, &out)
	return out, err
}

// SiteStatus returns the current site status.
func (c *Client) SiteStatus(ctx context.Context, id string) (agentproto.SiteStatusResponse, error) {
	var out agentproto.SiteStatusResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathSiteStatusFor(id), nil, &out)
	return out, err
}

// SiteLogs opens a finite or streaming site log response.
func (c *Client) SiteLogs(ctx context.Context, id string, req agentproto.SiteLogsRequest) (io.ReadCloser, error) {
	path := agentproto.PathSiteLogsFor(id)
	if req.Follow || req.Tail > 0 {
		query := url.Values{}
		if req.Follow {
			query.Set("follow", "true")
		}
		if req.Tail > 0 {
			query.Set("tail", fmt.Sprintf("%d", req.Tail))
		}
		path += "?" + query.Encode()
	}
	return c.open(ctx, http.MethodGet, path, nil)
}

// ExecSite runs argv in the site environment.
func (c *Client) ExecSite(ctx context.Context, id string, req agentproto.SiteExecRequest) (agentproto.SiteExecResponse, error) {
	var out agentproto.SiteExecResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSiteExecFor(id), req, &out)
	return out, err
}

// PreparePHP stages a PHP generation.
func (c *Client) PreparePHP(ctx context.Context, id string, req agentproto.PHPPrepareRequest) (agentproto.SitePrepareResponse, error) {
	var out agentproto.SitePrepareResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSitePHPPrepareFor(id), req, &out)
	return out, err
}

// StartPHP starts a PHP generation.
func (c *Client) StartPHP(ctx context.Context, id string, req agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	var out agentproto.SiteActionResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSitePHPStartFor(id), req, &out)
	return out, err
}

// StopPHP stops a PHP generation.
func (c *Client) StopPHP(ctx context.Context, id string, req agentproto.PHPActionRequest) (agentproto.SiteActionResponse, error) {
	var out agentproto.SiteActionResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSitePHPStopFor(id), req, &out)
	return out, err
}

// BuildSite runs the site build command.
func (c *Client) BuildSite(ctx context.Context, id string, req agentproto.BuildRequest) (agentproto.BuildResponse, error) {
	var out agentproto.BuildResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSiteBuildFor(id), req, &out)
	return out, err
}

// TerminalTicket requests a one-time terminal ticket.
func (c *Client) TerminalTicket(ctx context.Context, id string, req agentproto.TerminalTicketRequest) (agentproto.TerminalTicketResponse, error) {
	var out agentproto.TerminalTicketResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSiteTerminalFor(id), req, &out)
	return out, err
}

// InstallRuntime installs or returns a runtime build.
func (c *Client) InstallRuntime(ctx context.Context, req agentproto.RuntimeRequest) (agentproto.RuntimeResponse, error) {
	var out agentproto.RuntimeResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathRuntimes, req, &out)
	return out, err
}

// RemoveRuntime deletes an installed runtime.
func (c *Client) RemoveRuntime(ctx context.Context, req agentproto.RuntimeRequest) error {
	return c.do(ctx, http.MethodPost, agentproto.PathRuntimesRemove, req, nil)
}

// ListRuntimes lists installed runtimes.
func (c *Client) ListRuntimes(ctx context.Context) (agentproto.RuntimeListResponse, error) {
	var out agentproto.RuntimeListResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathRuntimesList, nil, &out)
	return out, err
}

// ServiceAction performs one fixed service lifecycle action.
func (c *Client) ServiceAction(ctx context.Context, req agentproto.ServiceRequest) (agentproto.ServiceResponse, error) {
	var out agentproto.ServiceResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathServiceActionFor(req.ID), req, &out)
	return out, err
}

// ServiceStatus reports one service's state.
func (c *Client) ServiceStatus(ctx context.Context, id string) (agentproto.ServiceResponse, error) {
	var out agentproto.ServiceResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathServiceStatusFor(id), nil, &out)
	return out, err
}

// ServiceLogs returns a finite service log snapshot.
func (c *Client) ServiceLogs(ctx context.Context, id string, req agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error) {
	var out agentproto.ServiceLogsResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathServiceLogsFor(id), req, &out)
	return out, err
}

// ServiceConfig lists managed config metadata.
func (c *Client) ServiceConfig(ctx context.Context, id string) (agentproto.ServiceConfigResponse, error) {
	var out agentproto.ServiceConfigResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathServiceConfigFor(id), nil, &out)
	return out, err
}

// ServiceContent returns one allowed config file.
func (c *Client) ServiceContent(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	var out agentproto.ServiceContentResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathServiceContentFor(req.ID), req, &out)
	return out, err
}

// ServiceApply writes one config file.
func (c *Client) ServiceApply(ctx context.Context, req agentproto.ServiceConfigRequest) (agentproto.ServiceContentResponse, error) {
	var out agentproto.ServiceContentResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathServiceApplyFor(req.ID), req, &out)
	return out, err
}

// ServiceReset restores one config file from its baseline.
func (c *Client) ServiceReset(ctx context.Context, req agentproto.ServiceConfigRequest) error {
	return c.do(ctx, http.MethodPost, agentproto.PathServiceResetFor(req.ID), req, nil)
}

// ServiceCredential returns one service credential.
func (c *Client) ServiceCredential(ctx context.Context, req agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error) {
	var out agentproto.ServiceCredentialResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathServiceCredentialFor(req.ID), req, &out)
	return out, err
}

// WebTargets replaces the agent's private web targets.
func (c *Client) WebTargets(ctx context.Context, req agentproto.WebTargetsRequest) error {
	return c.do(ctx, http.MethodPost, agentproto.PathWebTargets, req, nil)
}

// SyncWeb applies the active web target set.
func (c *Client) SyncWeb(ctx context.Context, req agentproto.WebTargetsRequest) (agentproto.WebSyncResponse, error) {
	var out agentproto.WebSyncResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathWebSync, req, &out)
	return out, err
}

// SyncHosts replaces the WSL hosts block.
func (c *Client) SyncHosts(ctx context.Context, req agentproto.HostsRequest) (agentproto.HostsResponse, error) {
	var out agentproto.HostsResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathHosts, req, &out)
	return out, err
}

// EnsureCert issues or reloads one leaf certificate.
func (c *Client) EnsureCert(ctx context.Context, req agentproto.CertRequest) (agentproto.CertResponse, error) {
	var out agentproto.CertResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathCerts, req, &out)
	return out, err
}

// CA returns the agent's CA bundle and fingerprint.
func (c *Client) CA(ctx context.Context) (agentproto.CAResponse, error) {
	var out agentproto.CAResponse
	err := c.do(ctx, http.MethodGet, agentproto.PathCA, nil, &out)
	return out, err
}

// InstallCA installs the daemon root certificate as the WSL trust anchor.
func (c *Client) InstallCA(ctx context.Context, certPEM []byte) error {
	return c.do(ctx, http.MethodPost, agentproto.PathCAInstall, agentproto.CAInstallRequest{CertPEM: certPEM}, nil)
}

// ValidatePath translates and checks one source path.
func (c *Client) ValidatePath(ctx context.Context, req agentproto.PathValidateRequest) (agentproto.PathValidateResponse, error) {
	var out agentproto.PathValidateResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathPathsValidate, req, &out)
	return out, err
}

// CopySource moves a project with checksum and ownership verification.
func (c *Client) CopySource(ctx context.Context, req agentproto.SourceCopyRequest) (agentproto.SourceCopyResponse, error) {
	var out agentproto.SourceCopyResponse
	err := c.do(ctx, http.MethodPost, agentproto.PathSourceCopy, req, &out)
	return out, err
}
