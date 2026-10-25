// Package helperclient talks to the privileged devstack helper over its
// Unix-domain HTTP socket.
package helperclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

const (
	baseURL         = "http://devstack-helper"
	maxResponseBody = 8 << 20
)

// Sentinel errors are aliases of the protocol errors so callers can match
// either package with errors.Is.
var (
	ErrUnauthorized     = helperproto.ErrUnauthorized
	ErrProtocolMismatch = helperproto.ErrProtocolMismatch
	ErrInvalidRequest   = helperproto.ErrInvalidRequest
	ErrNotImplemented   = helperproto.ErrNotImplemented
	ErrInternal         = helperproto.ErrInternal
	ErrServiceConflict  = helperproto.ErrServiceConflict

	ErrServingModeUnavailable = helperproto.ErrServingModeUnavailable
	ErrVhostConfigInvalid     = helperproto.ErrVhostConfigInvalid
	ErrVhostSyncFailed        = helperproto.ErrVhostSyncFailed
	ErrVhostIncludeMissing    = helperproto.ErrVhostIncludeMissing
	ErrServerReloadFailed     = helperproto.ErrServerReloadFailed
	ErrVhostSyncBusy          = helperproto.ErrVhostSyncBusy

	ErrToolConflict = helperproto.ErrToolConflict
)

// Error is a structured helper error response.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("helper: %s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// Unwrap maps the response code to the corresponding sentinel error.
func (e *Error) Unwrap() error {
	switch e.Code {
	case helperproto.CodeUnauthorized:
		return helperproto.ErrUnauthorized
	case helperproto.CodeProtocolMismatch:
		return helperproto.ErrProtocolMismatch
	case helperproto.CodeInvalidRequest:
		return helperproto.ErrInvalidRequest
	case helperproto.CodeNotImplemented:
		return helperproto.ErrNotImplemented
	case helperproto.CodeInternal:
		return helperproto.ErrInternal
	case helperproto.CodeServiceConflict:
		return helperproto.ErrServiceConflict
	case helperproto.CodeServingModeUnavailable:
		return helperproto.ErrServingModeUnavailable
	case helperproto.CodeVhostConfigInvalid:
		return helperproto.ErrVhostConfigInvalid
	case helperproto.CodeVhostSyncFailed:
		return helperproto.ErrVhostSyncFailed
	case helperproto.CodeVhostIncludeMissing:
		return helperproto.ErrVhostIncludeMissing
	case helperproto.CodeServerReloadFailed:
		return helperproto.ErrServerReloadFailed
	case helperproto.CodeVhostSyncBusy:
		return helperproto.ErrVhostSyncBusy
	case helperproto.CodeToolConflict:
		return helperproto.ErrToolConflict
	default:
		return nil
	}
}

// Client is a helper RPC client. It is safe for concurrent use.
type Client struct {
	socketPath string
	http       *http.Client

	mu      sync.RWMutex
	rootPEM []byte
}

// Dial returns a client for the helper listening at socketPath. No connection
// is made until the first request.
func Dial(socketPath string) (*Client, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("helperclient: socket path is empty")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: 90 * time.Second,
	}
	return &Client{
		socketPath: socketPath,
		http:       &http.Client{Transport: transport},
	}, nil
}

// Close releases idle connections held by the client.
func (c *Client) Close() error {
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}

// Status reports the helper protocol version, CA fingerprint, and hosts state.
func (c *Client) Status(ctx context.Context) (helperproto.StatusResponse, error) {
	var resp helperproto.StatusResponse
	err := c.do(ctx, http.MethodGet, helperproto.PathStatus, nil, &resp)
	return resp, err
}

// SyncHosts replaces the helper's managed hosts block with domains.
func (c *Client) SyncHosts(ctx context.Context, domains []string) error {
	req := helperproto.HostsSyncRequest{Domains: domains}
	var resp helperproto.HostsSyncResponse
	return c.do(ctx, http.MethodPost, helperproto.PathHostsSync, req, &resp)
}

// EnsureCA creates or loads the helper's local certificate authority and
// caches its root certificate for RootPEM.
func (c *Client) EnsureCA(ctx context.Context) (helperproto.EnsureCAResponse, error) {
	var resp helperproto.EnsureCAResponse
	if err := c.do(ctx, http.MethodPost, helperproto.PathEnsureCA, nil, &resp); err != nil {
		return helperproto.EnsureCAResponse{}, err
	}
	if len(resp.RootPEM) > 0 {
		c.mu.Lock()
		c.rootPEM = append([]byte(nil), resp.RootPEM...)
		c.mu.Unlock()
	}
	return resp, nil
}

// RootPEM returns a copy of the root certificate cached by EnsureCA, or nil
// when EnsureCA has not succeeded.
func (c *Client) RootPEM() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rootPEM == nil {
		return nil
	}
	return append([]byte(nil), c.rootPEM...)
}

// SignCertificate asks the helper to sign csrPEM for domains.
func (c *Client) SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error) {
	var resp helperproto.SignResponse
	req := helperproto.SignRequest{CSR: csrPEM, Domains: domains}
	err := c.do(ctx, http.MethodPost, helperproto.PathSign, req, &resp)
	return resp, err
}

// InstallTrust installs the helper CA into the trust stores of userHome.
func (c *Client) InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error) {
	return c.trust(ctx, helperproto.PathTrustInstall, userHome)
}

// RemoveTrust removes the helper CA from the trust stores of userHome.
func (c *Client) RemoveTrust(ctx context.Context, userHome string) ([]trust.Target, error) {
	return c.trust(ctx, helperproto.PathTrustRemove, userHome)
}

// TrustStatus reports the helper CA's presence in the trust stores of userHome.
func (c *Client) TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error) {
	return c.trust(ctx, helperproto.PathTrustStatus, userHome)
}

func (c *Client) trust(ctx context.Context, path, userHome string) ([]trust.Target, error) {
	var resp helperproto.TrustResponse
	req := helperproto.TrustRequest{UserHome: userHome}
	if err := c.do(ctx, http.MethodPost, path, req, &resp); err != nil {
		return nil, err
	}
	return resp.Targets, nil
}

// SetForwarders starts or stops the privileged 80/443 forwarders. httpPort and
// httpsPort are the helper's configured loopback proxy ports; the privileged
// listen ports come from the helper's configuration.
func (c *Client) SetForwarders(ctx context.Context, httpPort, httpsPort int, enable bool) error {
	req := helperproto.ForwardersRequest{HTTPPort: httpPort, HTTPSPort: httpsPort, Enable: enable}
	return c.do(ctx, http.MethodPost, helperproto.PathForwarders, req, nil)
}

// Uninstall removes the helper's hosts entries, trust, and forwarders.
func (c *Client) Uninstall(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, helperproto.PathUninstall, helperproto.UninstallRequest{}, nil)
}

// VhostStatus reports the helper's persisted vhost mode and last sync state.
func (c *Client) VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	var resp helperproto.VhostStatusResponse
	err := c.do(ctx, http.MethodGet, helperproto.PathVhostsStatus, nil, &resp)
	return resp, err
}

// SyncVhosts installs a complete vhost set for the active external server.
func (c *Client) SyncVhosts(ctx context.Context, req helperproto.VHostSyncRequest) error {
	return c.do(ctx, http.MethodPost, helperproto.PathVhostsSync, req, nil)
}

// SetServingMode switches the helper's serving mode. External modes require a
// complete sync request; builtin ignores it.
func (c *Client) SetServingMode(ctx context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error) {
	payload := helperproto.VhostModeRequest{Mode: mode, Sync: &req}
	var resp helperproto.VhostStatusResponse
	err := c.do(ctx, http.MethodPost, helperproto.PathVhostsMode, payload, &resp)
	return resp, err
}

// RemoveVhosts deletes helper-managed vhosts, bridge files, and TLS material.
func (c *Client) RemoveVhosts(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, helperproto.PathVhosts, nil, nil)
}

// ServiceStatus retrieves the current operational status of all services.
func (c *Client) ServiceStatus(ctx context.Context) ([]helperproto.ServiceStatus, error) {
	var resp []helperproto.ServiceStatus
	err := c.do(ctx, http.MethodGet, helperproto.PathServicesStatus, nil, &resp)
	return resp, err
}

// ServiceInstall instructs the helper to pull the image and scaffold a service.
func (c *Client) ServiceInstall(ctx context.Context, id string) error {
	req := helperproto.ServiceActionRequest{ID: id}
	return c.do(ctx, http.MethodPost, helperproto.PathServicesInstall, req, nil)
}

// ServiceActivate starts the specified service container.
func (c *Client) ServiceActivate(ctx context.Context, id string) error {
	req := helperproto.ServiceActionRequest{ID: id}
	return c.do(ctx, http.MethodPost, helperproto.PathServicesActivate, req, nil)
}

// ServiceDeactivate stops the specified service container.
func (c *Client) ServiceDeactivate(ctx context.Context, id string) error {
	req := helperproto.ServiceActionRequest{ID: id}
	return c.do(ctx, http.MethodPost, helperproto.PathServicesDeactivate, req, nil)
}

// ServiceUninstall stops and removes the specified service container, optionally deleting its data.
func (c *Client) ServiceUninstall(ctx context.Context, id string, deleteData bool) error {
	req := helperproto.ServiceActionRequest{ID: id, DeleteData: deleteData}
	return c.do(ctx, http.MethodPost, helperproto.PathServicesUninstall, req, nil)
}

// ServiceRestart restarts the specified service container and waits for health.
func (c *Client) ServiceRestart(ctx context.Context, id string) error {
	req := helperproto.ServiceActionRequest{ID: id}
	return c.do(ctx, http.MethodPost, helperproto.PathServicesRestart, req, nil)
}

// ServiceValidate runs the catalog validation command inside the service container.
func (c *Client) ServiceValidate(ctx context.Context, id string) error {
	req := helperproto.ServiceActionRequest{ID: id}
	return c.do(ctx, http.MethodPost, helperproto.PathServicesValidate, req, nil)
}

// ServiceReload runs the catalog reload command inside the service container.
func (c *Client) ServiceReload(ctx context.Context, id string) error {
	req := helperproto.ServiceActionRequest{ID: id}
	return c.do(ctx, http.MethodPost, helperproto.PathServicesReload, req, nil)
}

// ServiceLogs retrieves bounded container logs for the specified service.
func (c *Client) ServiceLogs(ctx context.Context, id string, tail int) (string, error) {
	path := fmt.Sprintf("%s?id=%s", helperproto.PathServicesLogs, url.QueryEscape(id))
	if tail > 0 {
		path += fmt.Sprintf("&tail=%d", tail)
	}
	var resp helperproto.ServiceLogsResponse
	err := c.do(ctx, http.MethodGet, path, nil, &resp)
	return resp.Logs, err
}

// ToolStatus reports the managed PHP and Composer shims.
func (c *Client) ToolStatus(ctx context.Context) (helperproto.ToolStatusResponse, error) {
	var resp helperproto.ToolStatusResponse
	err := c.do(ctx, http.MethodGet, helperproto.PathToolsStatus, nil, &resp)
	return resp, err
}

// ToolInstall links one managed tool into the system bin directory.
func (c *Client) ToolInstall(ctx context.Context, id string, force bool) (helperproto.ToolStatus, error) {
	req := helperproto.ToolInstallRequest{ID: id, Force: force}
	var resp helperproto.ToolResponse
	err := c.do(ctx, http.MethodPost, helperproto.PathToolsInstall, req, &resp)
	return resp.Tool, err
}

// ToolRemove removes one managed tool's system link and managed files.
func (c *Client) ToolRemove(ctx context.Context, id string) (helperproto.ToolStatus, error) {
	req := helperproto.ToolRemoveRequest{ID: id}
	var resp helperproto.ToolResponse
	err := c.do(ctx, http.MethodPost, helperproto.PathToolsRemove, req, &resp)
	return resp.Tool, err
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("helperclient: encode %s request: %w", path, err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return fmt.Errorf("helperclient: build %s request: %w", path, err)
	}
	req.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("helperclient: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return decodeError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("helperclient: decode %s response: %w", path, err)
	}
	return nil
}

func decodeError(resp *http.Response) error {
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	payload := helperproto.ErrorResponse{}
	if len(bytes.TrimSpace(data)) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	if payload.Code == "" {
		payload.Code = codeForStatus(resp.StatusCode)
	}
	if payload.Message == "" {
		if readErr != nil {
			payload.Message = readErr.Error()
		} else {
			payload.Message = strings.TrimSpace(string(data))
		}
		if payload.Message == "" {
			payload.Message = http.StatusText(resp.StatusCode)
		}
	}
	return &Error{Status: resp.StatusCode, Code: payload.Code, Message: payload.Message}
}

func codeForStatus(status int) string {
	switch status {
	case http.StatusForbidden:
		return helperproto.CodeUnauthorized
	case http.StatusUpgradeRequired:
		return helperproto.CodeProtocolMismatch
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return helperproto.CodeInvalidRequest
	case http.StatusNotImplemented:
		return helperproto.CodeNotImplemented
	default:
		return helperproto.CodeInternal
	}
}
