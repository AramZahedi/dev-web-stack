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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

const (
	baseURL         = "http://devstack.test"
	maxResponseBody = 8 << 20
	dialTimeout     = 5 * time.Second
	defaultTimeout  = 30 * time.Second
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
	socket  string
	http    *http.Client
	timeout time.Duration
}

// New returns a client for the daemon at home. An empty home follows
// $DEVSTACK_HOME, then ~/.devstack, matching config.Load. It verifies that the
// control socket exists and is a socket; it does not connect yet.
func New(home string) (*Client, error) {
	resolved, err := resolveHome(home)
	if err != nil {
		return nil, err
	}
	socket := daemon.SocketPath(resolved)
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
		socket:  socket,
		http:    &http.Client{Transport: transport},
		timeout: defaultTimeout,
	}, nil
}

// resolveHome applies the same precedence as config.Load without creating
// anything.
func resolveHome(home string) (string, error) {
	if home == "" {
		home = os.Getenv("DEVSTACK_HOME")
	}
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		home = filepath.Join(userHome, ".devstack")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve devstack home %q: %w", home, err)
	}
	return abs, nil
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
		Name:            req.Name,
		Path:            req.Path,
		Kind:            req.Kind,
		NodeVersion:     req.NodeVersion,
		Command:         req.Command,
		DocumentRoot:    req.DocumentRoot,
		WWW:             req.WWW,
		TLS:             req.TLS,
		ForceHTTPS:      req.ForceHTTPS,
		SPAFallback:     req.SPAFallback,
		Env:             req.Env,
		PHPVersion:      req.PHPVersion,
		PHPBuild:        req.PHPBuild,
		PHPExtensions:   req.PHPExtensions,
		PHPIni:          req.PHPIni,
		PHPIniRaw:       req.PHPIniRaw,
		FrontController: req.FrontController,
	}
	var site model.Site
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/sites", payload, &site)
	return site, err
}

// UpdateSite applies a partial site update.
func (c *Client) UpdateSite(ctx context.Context, ref string, req sites.UpdateRequest) (model.Site, error) {
	payload := updateSitePayload{
		Domain:          req.Domain,
		WWW:             req.WWW,
		TLS:             req.TLS,
		ForceHTTPS:      req.ForceHTTPS,
		SPAFallback:     req.SPAFallback,
		FrontController: req.FrontController,
		Command:         req.Command,
		DocumentRoot:    req.DocumentRoot,
	}
	if req.Env != nil {
		env := req.Env
		payload.Env = &env
	}
	var site model.Site
	err := c.doJSON(ctx, http.MethodPatch, "/api/v1/sites/"+url.PathEscape(ref), payload, &site)
	return site, err
}

// PHPInfo returns a site's effective PHP configuration.
func (c *Client) PHPInfo(ctx context.Context, ref string) (sites.PHPInfo, error) {
	var info sites.PHPInfo
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/sites/"+url.PathEscape(ref)+"/php", nil, &info)
	return info, err
}

// ApplyPHP merges and applies a site PHP configuration change.
func (c *Client) ApplyPHP(ctx context.Context, ref string, change sites.PHPChange) (model.Site, error) {
	payload := phpSettingsPayload{
		Version:    change.Version,
		Build:      change.Build,
		Extensions: change.Extensions,
		Ini:        change.Ini,
		IniRaw:     change.IniRaw,
	}
	var site model.Site
	err := c.doJSON(ctx, http.MethodPut, "/api/v1/sites/"+url.PathEscape(ref)+"/php", payload, &site)
	return site, err
}

// PHPExtensions lists the catalog extensions available on a PHP line.
func (c *Client) PHPExtensions(ctx context.Context, version string) ([]catalog.Extension, error) {
	query := url.Values{}
	query.Set("version", version)
	var extensions []catalog.Extension
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/php/extensions?"+query.Encode(), nil, &extensions); err != nil {
		return nil, err
	}
	if extensions == nil {
		extensions = []catalog.Extension{}
	}
	return extensions, nil
}

// PHPDefaults returns the editable defaults for a PHP line.
func (c *Client) PHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error) {
	var defaults model.PHPDefaults
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/php/defaults/"+url.PathEscape(version), nil, &defaults)
	return defaults, err
}

// SavePHPDefaults saves the defaults for a PHP line and returns the per-site
// apply outcome.
func (c *Client) SavePHPDefaults(ctx context.Context, d model.PHPDefaults) (sites.PHPDefaultsResult, error) {
	payload := phpDefaultsPayload{Version: d.Version, Ini: d.Ini, Extensions: d.Extensions}
	var result sites.PHPDefaultsResult
	err := c.doJSON(ctx, http.MethodPut, "/api/v1/php/defaults/"+url.PathEscape(d.Version), payload, &result)
	return result, err
}

// MigratePHP moves every site on a PHP line to a target build.
func (c *Client) MigratePHP(ctx context.Context, req sites.PHPMigrationRequest) ([]sites.PHPMigrationResult, error) {
	payload := phpMigrationPayload{
		FromVersion: req.FromVersion,
		ToVersion:   req.ToVersion,
		ToBuild:     req.ToBuild,
	}
	var results []sites.PHPMigrationResult
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/php/migrate", payload, &results); err != nil {
		return nil, err
	}
	if results == nil {
		results = []sites.PHPMigrationResult{}
	}
	return results, nil
}

// PHPExec runs the site's PHP binary with the generated php.ini and the
// supplied argv.
func (c *Client) PHPExec(ctx context.Context, ref string, argv []string) (backend.Result, error) {
	payload := phpExecPayload{Argv: argv}
	if payload.Argv == nil {
		payload.Argv = []string{}
	}
	var response phpExecResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/sites/"+url.PathEscape(ref)+"/php/exec", payload, &response); err != nil {
		return backend.Result{}, err
	}
	return backend.Result{ExitCode: response.ExitCode, Stdout: response.Stdout, Stderr: response.Stderr}, nil
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

// VhostStatus reports the helper-managed external serving state.
func (c *Client) VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	var status helperproto.VhostStatusResponse
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/vhosts/status", nil, &status)
	return status, err
}

// SetServingMode switches the serving mode and returns the refreshed status.
func (c *Client) SetServingMode(ctx context.Context, mode string) (helperproto.VhostStatusResponse, error) {
	var status helperproto.VhostStatusResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/vhosts/mode", map[string]string{"mode": mode}, &status)
	return status, err
}

// SyncVhosts reinstalls the complete external vhost set and returns the
// refreshed status.
func (c *Client) SyncVhosts(ctx context.Context) (helperproto.VhostStatusResponse, error) {
	var status helperproto.VhostStatusResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/vhosts/sync", nil, &status)
	return status, err
}

// PreviewVhost renders one site's nginx or Apache configuration.
func (c *Client) PreviewVhost(ctx context.Context, ref, server string) (string, error) {
	query := url.Values{}
	query.Set("site", ref)
	query.Set("server", server)
	var response struct {
		Server  string `json:"server"`
		Site    string `json:"site"`
		Content string `json:"content"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/vhosts/preview?"+query.Encode(), nil, &response); err != nil {
		return "", err
	}
	return response.Content, nil
}

type listServicesEnvelope struct {
	Services []services.View `json:"services"`
}

// ListServices returns every known service view.
func (c *Client) ListServices(ctx context.Context) ([]services.View, error) {
	var env listServicesEnvelope
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/services", nil, &env); err != nil {
		return nil, err
	}
	if env.Services == nil {
		env.Services = []services.View{}
	}
	return env.Services, nil
}

// GetService returns one service view by ID.
func (c *Client) GetService(ctx context.Context, id string) (services.View, error) {
	var view services.View
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/services/"+url.PathEscape(id), nil, &view)
	return view, err
}

// Logs opens the site's log stream. follow=false requests a finite SSE
// snapshot of the recent lines; follow=true keeps the live subscription open
// until the caller closes the body or cancels ctx.
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
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp, err := c.do(requestCtx, method, path, in)
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
	Name            string            `json:"name"`
	Path            string            `json:"path"`
	Kind            string            `json:"kind"`
	NodeVersion     string            `json:"node_version,omitempty"`
	Command         string            `json:"command,omitempty"`
	DocumentRoot    string            `json:"document_root,omitempty"`
	WWW             bool              `json:"www"`
	TLS             bool              `json:"tls"`
	ForceHTTPS      bool              `json:"force_https"`
	SPAFallback     bool              `json:"spa_fallback"`
	Env             map[string]string `json:"env,omitempty"`
	PHPVersion      string            `json:"php_version,omitempty"`
	PHPBuild        string            `json:"php_build,omitempty"`
	PHPExtensions   map[string]bool   `json:"php_extensions,omitempty"`
	PHPIni          map[string]string `json:"php_ini,omitempty"`
	PHPIniRaw       string            `json:"php_ini_raw,omitempty"`
	FrontController bool              `json:"front_controller"`
}

type updateSitePayload struct {
	Domain          string             `json:"domain,omitempty"`
	WWW             *bool              `json:"www,omitempty"`
	TLS             *bool              `json:"tls,omitempty"`
	ForceHTTPS      *bool              `json:"force_https,omitempty"`
	SPAFallback     *bool              `json:"spa_fallback,omitempty"`
	FrontController *bool              `json:"front_controller,omitempty"`
	Command         string             `json:"command,omitempty"`
	DocumentRoot    string             `json:"document_root,omitempty"`
	Env             *map[string]string `json:"env,omitempty"`
}

// phpSettingsPayload is the transport shape of sites.PHPChange. Nil maps and
// a nil IniRaw keep the current overrides.
type phpSettingsPayload struct {
	Version    string            `json:"version,omitempty"`
	Build      string            `json:"build,omitempty"`
	Extensions map[string]bool   `json:"extensions"`
	Ini        map[string]string `json:"ini"`
	IniRaw     *string           `json:"ini_raw,omitempty"`
}

type phpExecPayload struct {
	Argv []string `json:"argv"`
}

type phpExecResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type phpDefaultsPayload struct {
	Version    string            `json:"version,omitempty"`
	Ini        map[string]string `json:"ini,omitempty"`
	Extensions map[string]bool   `json:"extensions,omitempty"`
}

type phpMigrationPayload struct {
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version,omitempty"`
	ToBuild     string `json:"to_build,omitempty"`
}
