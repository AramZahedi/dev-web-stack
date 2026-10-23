// Package client is the CLI's typed client for the devstack daemon's Unix-socket
// REST API. Calls are authenticated by the socket itself: the daemon wraps the
// Unix listener in api.WithSocketAuth, so no session cookie or CSRF token is
// required. The host allowlist still applies, so every request uses an allowed
// Host.
package client

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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

const (
	baseURL         = "http://devstack.test"
	maxResponseBody = 8 << 20
	dialTimeout     = 5 * time.Second
)

// Error is a structured API error response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

func (e *Error) Error() string {
	if e == nil {
		return "devstack API error"
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// Client talks to one daemon over its Unix socket. It is safe for concurrent
// use.
type Client struct {
	socket string
	http   *http.Client
}

// New returns a client for the daemon at home. It verifies that the control
// socket exists and is a socket; it does not connect yet.
func New(home string) (*Client, error) {
	socket := daemon.SocketPath(home)
	info, err := os.Stat(socket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("devstack daemon is not running: socket %s does not exist", socket)
		}
		return nil, fmt.Errorf("devstack daemon socket %s: %w", socket, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("devstack daemon socket %s is not a Unix socket", socket)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: dialTimeout}
			return dialer.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Client{
		socket: socket,
		http:   &http.Client{Transport: transport},
	}, nil
}

// Close releases idle connections held by the client.
func (c *Client) Close() error {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
	return nil
}

// SocketPath returns the control socket the client uses.
func (c *Client) SocketPath() string {
	if c == nil {
		return ""
	}
	return c.socket
}

// ListSites returns every registered site.
func (c *Client) ListSites(ctx context.Context) ([]model.Site, error) {
	var sites []model.Site
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/sites", nil, &sites); err != nil {
		return nil, err
	}
	if sites == nil {
		sites = []model.Site{}
	}
	return sites, nil
}

// GetSite returns one site by id or name.
func (c *Client) GetSite(ctx context.Context, ref string) (model.Site, error) {
	var site model.Site
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/sites/"+url.PathEscape(ref), nil, &site)
	return site, err
}

// AddSite creates and starts a site.
func (c *Client) AddSite(ctx context.Context, req sites.CreateRequest) (model.Site, error) {
	payload := createSitePayload{
		Name:         req.Name,
		Path:         req.Path,
		Kind:         req.Kind,
		NodeVersion:  req.NodeVersion,
		Command:      req.Command,
		DocumentRoot: req.DocumentRoot,
		WWW:          req.WWW,
		TLS:          req.TLS,
		ForceHTTPS:   req.ForceHTTPS,
		SPAFallback:  req.SPAFallback,
		Env:          req.Env,
	}
	var site model.Site
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/sites", payload, &site)
	return site, err
}

// UpdateSite applies a partial site update.
func (c *Client) UpdateSite(ctx context.Context, ref string, req sites.UpdateRequest) (model.Site, error) {
	payload := updateSitePayload{
		Domain:       req.Domain,
		WWW:          req.WWW,
		TLS:          req.TLS,
		ForceHTTPS:   req.ForceHTTPS,
		SPAFallback:  req.SPAFallback,
		Command:      req.Command,
		DocumentRoot: req.DocumentRoot,
	}
	if req.Env != nil {
		env := req.Env
		payload.Env = &env
	}
	var site model.Site
	err := c.doJSON(ctx, http.MethodPatch, "/api/v1/sites/"+url.PathEscape(ref), payload, &site)
	return site, err
}

// SiteAction starts, stops, or restarts a site.
func (c *Client) SiteAction(ctx context.Context, ref, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("client: unsupported site action %q", action)
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/sites/"+url.PathEscape(ref)+"/"+action, nil, nil)
}

// DeleteSite removes a site.
func (c *Client) DeleteSite(ctx context.Context, ref string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/sites/"+url.PathEscape(ref), nil, nil)
}

// Logs opens the site's log stream. The API exposes a live SSE stream; the
// boolean is forwarded as the follow query value so a future snapshot mode can
// be added without changing this method.
func (c *Client) Logs(ctx context.Context, ref string, follow bool) (io.ReadCloser, error) {
	path := "/api/v1/sites/" + url.PathEscape(ref) + "/logs/stream?follow=" + strconv.FormatBool(follow)
	return c.doStream(ctx, http.MethodGet, path)
}

// Doctor runs the diagnostic checks.
func (c *Client) Doctor(ctx context.Context) (doctor.Report, error) {
	var report doctor.Report
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/doctor", nil, &report)
	if report.Checks == nil {
		report.Checks = []doctor.Check{}
	}
	return report, err
}

// Repair re-applies the safe parts of the installation and reruns doctor.
func (c *Client) Repair(ctx context.Context) (doctor.Report, error) {
	var report doctor.Report
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/doctor/repair", nil, &report)
	if report.Checks == nil {
		report.Checks = []doctor.Check{}
	}
	return report, err
}

// TrustStatus reports the helper CA's trust state.
func (c *Client) TrustStatus(ctx context.Context) ([]trust.Target, error) {
	return c.trust(ctx, http.MethodGet, "/api/v1/trust")
}

// TrustRepair installs the helper CA into the detected stores.
func (c *Client) TrustRepair(ctx context.Context) ([]trust.Target, error) {
	return c.trust(ctx, http.MethodPost, "/api/v1/trust/repair")
}

// TrustRemove removes the helper CA from the detected stores.
func (c *Client) TrustRemove(ctx context.Context) ([]trust.Target, error) {
	return c.trust(ctx, http.MethodDelete, "/api/v1/trust")
}

func (c *Client) trust(ctx context.Context, method, path string) ([]trust.Target, error) {
	var targets []trust.Target
	if err := c.doJSON(ctx, method, path, nil, &targets); err != nil {
		return nil, err
	}
	if targets == nil {
		targets = []trust.Target{}
	}
	return targets, nil
}

// Runtimes lists installed runtime builds.
func (c *Client) Runtimes(ctx context.Context) ([]model.RuntimeBuild, error) {
	var builds []model.RuntimeBuild
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/runtimes", nil, &builds); err != nil {
		return nil, err
	}
	if builds == nil {
		builds = []model.RuntimeBuild{}
	}
	return builds, nil
}

// AvailableRuntimes lists the catalog entries compiled into the daemon.
func (c *Client) AvailableRuntimes(ctx context.Context) ([]catalog.Entry, error) {
	var entries []catalog.Entry
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/runtimes/available", nil, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []catalog.Entry{}
	}
	return entries, nil
}

// InstallRuntime installs a catalog runtime build.
func (c *Client) InstallRuntime(ctx context.Context, id string) (model.RuntimeBuild, error) {
	var build model.RuntimeBuild
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/runtimes/install", map[string]string{"id": id}, &build)
	return build, err
}

// RemoveRuntime deletes an installed runtime build.
func (c *Client) RemoveRuntime(ctx context.Context, buildID string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/runtimes/"+url.PathEscape(buildID), nil, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	resp, err := c.do(ctx, method, path, in)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNoContent || out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("client: decode %s response: %w", path, err)
	}
	return nil
}

func (c *Client) doStream(ctx context.Context, method, path string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, method, path, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) do(ctx context.Context, method, path string, in any) (*http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("client: encode %s request: %w", path, err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("client: build %s request: %w", path, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client: %s %s: %w", method, path, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		return nil, decodeError(resp)
	}
	return resp, nil
}

// decodeError maps the API's stable error envelope onto Error. Non-JSON error
// responses still produce a useful client error.
func decodeError(resp *http.Response) error {
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload Error
	if len(bytes.TrimSpace(data)) > 0 {
		_ = json.Unmarshal(data, &payload)
	}
	if payload.Code == "" && payload.Message == "" {
		payload.Code = "HTTP_" + strconv.Itoa(resp.StatusCode)
		if readErr != nil {
			payload.Message = readErr.Error()
		} else {
			payload.Message = strings.TrimSpace(string(data))
		}
		if payload.Message == "" {
			payload.Message = http.StatusText(resp.StatusCode)
		}
	}
	return &payload
}

type createSitePayload struct {
	Name         string            `json:"name"`
	Path         string            `json:"path"`
	Kind         string            `json:"kind"`
	NodeVersion  string            `json:"node_version,omitempty"`
	Command      string            `json:"command,omitempty"`
	DocumentRoot string            `json:"document_root,omitempty"`
	WWW          bool              `json:"www"`
	TLS          bool              `json:"tls"`
	ForceHTTPS   bool              `json:"force_https"`
	SPAFallback  bool              `json:"spa_fallback"`
	Env          map[string]string `json:"env,omitempty"`
}

type updateSitePayload struct {
	Domain       string             `json:"domain,omitempty"`
	WWW          *bool              `json:"www,omitempty"`
	TLS          *bool              `json:"tls,omitempty"`
	ForceHTTPS   *bool              `json:"force_https,omitempty"`
	SPAFallback  *bool              `json:"spa_fallback,omitempty"`
	Command      string             `json:"command,omitempty"`
	DocumentRoot string             `json:"document_root,omitempty"`
	Env          *map[string]string `json:"env,omitempty"`
}
