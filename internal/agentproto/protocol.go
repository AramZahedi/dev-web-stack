package agentproto

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

// ProtocolVersion is the only version this build speaks. It is sent on every
// request and checked before any handler runs.
const ProtocolVersion = "v1"

// Wire headers.
const (
	HeaderProtocol = "X-Devstack-Agent-Protocol"
	HeaderAuth     = "Authorization"
	BearerPrefix   = "Bearer "
)

// Endpoint paths. The {id} forms are documented templates; the *For helpers
// build concrete escaped paths.
const (
	PathHealth       = "/v1/health"
	PathVersion      = "/v1/version"
	PathCapabilities = "/v1/capabilities"

	PathPortsAllocate = "/v1/ports/allocate"
	PathPortsRelease  = "/v1/ports/release"
	PathPortsValidate = "/v1/ports/validate"

	PathSites = "/v1/sites"

	PathRuntimes       = "/v1/runtimes"
	PathRuntimesRemove = "/v1/runtimes/remove"
	PathRuntimesList   = "/v1/runtimes/list"

	PathServices      = "/v1/services"
	PathWeb           = "/v1/web"
	PathWebTargets    = "/v1/web/targets"
	PathWebSync       = "/v1/web/sync"
	PathHosts         = "/v1/hosts"
	PathCerts         = "/v1/certs"
	PathCA            = "/v1/ca"
	PathCAInstall     = "/v1/ca/install"
	PathPathsValidate = "/v1/paths/validate"
	PathSourceCopy    = "/v1/source/copy"
	PathFS            = "/v1/fs"
)

// Site-scoped path helpers.
func PathSitePrepareFor(id string) string  { return sitePath(id, "prepare") }
func PathSiteStartFor(id string) string    { return sitePath(id, "start") }
func PathSiteStopFor(id string) string     { return sitePath(id, "stop") }
func PathSiteStatusFor(id string) string   { return sitePath(id, "status") }
func PathSiteLogsFor(id string) string     { return sitePath(id, "logs") }
func PathSiteExecFor(id string) string     { return sitePath(id, "exec") }
func PathSiteBuildFor(id string) string    { return sitePath(id, "build") }
func PathSiteTerminalFor(id string) string { return sitePath(id, "terminal") }

func PathSitePHPPrepareFor(id string) string { return sitePath(id, "php/prepare") }
func PathSitePHPStartFor(id string) string   { return sitePath(id, "php/start") }
func PathSitePHPStopFor(id string) string    { return sitePath(id, "php/stop") }

func PathServiceActionFor(id string) string { return "/v1/services/" + url.PathEscape(id) + "/action" }
func PathServiceStatusFor(id string) string { return "/v1/services/" + url.PathEscape(id) + "/status" }
func PathServiceLogsFor(id string) string   { return "/v1/services/" + url.PathEscape(id) + "/logs" }
func PathServiceConfigFor(id string) string { return "/v1/services/" + url.PathEscape(id) + "/config" }
func PathServiceContentFor(id string) string {
	return "/v1/services/" + url.PathEscape(id) + "/content"
}
func PathServiceApplyFor(id string) string { return "/v1/services/" + url.PathEscape(id) + "/apply" }
func PathServiceResetFor(id string) string { return "/v1/services/" + url.PathEscape(id) + "/reset" }
func PathServiceCredentialFor(id string) string {
	return "/v1/services/" + url.PathEscape(id) + "/credential"
}

func sitePath(id, suffix string) string {
	return "/v1/sites/" + url.PathEscape(id) + "/" + suffix
}

// Error codes.
const (
	CodeUnauthenticated = "unauthenticated"
	CodeForbidden       = "forbidden"
	CodeVersionMismatch = "version_mismatch"
	CodeCertPinMismatch = "cert_pin_mismatch"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeInvalidRequest  = "invalid_request"
	CodeInternal        = "internal"
	CodeTimeout         = "timeout"
	CodeUnsupported     = "unsupported"
)

// Sentinel errors shared by the agent, client, and callers.
var (
	ErrUnauthenticated = errors.New("agent: unauthenticated")
	ErrForbidden       = errors.New("agent: forbidden")
	ErrVersionMismatch = errors.New("agent: protocol version mismatch")
	ErrCertPinMismatch = errors.New("agent: certificate pin mismatch")
	ErrNotFound        = errors.New("agent: not found")
	ErrConflict        = errors.New("agent: conflict")
	ErrInvalidRequest  = errors.New("agent: invalid request")
	ErrInternal        = errors.New("agent: internal error")
	ErrTimeout         = errors.New("agent: timeout")
	ErrUnsupported     = errors.New("agent: unsupported operation")
)

// Error is the structured agent error response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return "agent: error"
	}
	return fmt.Sprintf("agent: %s (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// Unwrap maps a wire code to its sentinel.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.Code {
	case CodeUnauthenticated:
		return ErrUnauthenticated
	case CodeForbidden:
		return ErrForbidden
	case CodeVersionMismatch:
		return ErrVersionMismatch
	case CodeCertPinMismatch:
		return ErrCertPinMismatch
	case CodeNotFound:
		return ErrNotFound
	case CodeConflict:
		return ErrConflict
	case CodeInvalidRequest:
		return ErrInvalidRequest
	case CodeInternal:
		return ErrInternal
	case CodeTimeout:
		return ErrTimeout
	case CodeUnsupported:
		return ErrUnsupported
	default:
		return nil
	}
}

// ErrorResponse is the JSON body of a failure.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// VersionResponse reports the protocol and agent build.
type VersionResponse struct {
	Protocol string `json:"protocol"`
	Agent    string `json:"agent"`
}

// HealthResponse is the unauthenticated liveness probe result.
type HealthResponse struct {
	Version      string   `json:"version"`
	Distro       string   `json:"distro"`
	Root         string   `json:"root"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// CapabilitiesResponse lists optional agent features.
type CapabilitiesResponse struct {
	Capabilities []string `json:"capabilities"`
}

// PortRequest allocates or validates site backend ports.
type PortRequest struct {
	SiteID           string `json:"site_id"`
	Kind             string `json:"kind"`
	HTTPPort         int    `json:"http_port,omitempty"`
	FastCGIPort      int    `json:"fastcgi_port,omitempty"`
	RequestedHTTP    int    `json:"requested_http_port,omitempty"`
	RequestedFastCGI int    `json:"requested_fastcgi_port,omitempty"`
}

// PortResponse reports the ports the agent bound.
type PortResponse struct {
	HTTPPort    int  `json:"http_port"`
	FastCGIPort int  `json:"fastcgi_port"`
	Reused      bool `json:"reused,omitempty"`
}

// PortValidateResponse reports whether the requested ports are free.
type PortValidateResponse struct {
	Valid bool `json:"valid"`
}

// SiteSpec is the agent-visible description of one site.
type SiteSpec struct {
	ID                string            `json:"id"`
	Kind              string            `json:"kind"`
	Domain            string            `json:"domain"`
	HostPath          string            `json:"host_path"`
	BackendPath       string            `json:"backend_path"`
	DocumentRoot      string            `json:"document_root"`
	ProxyDocumentRoot string            `json:"proxy_document_root"`
	Command           string            `json:"command"`
	Env               map[string]string `json:"env,omitempty"`
	HTTPPort          int               `json:"http_port"`
	FastCGIPort       int               `json:"fastcgi_port"`
	SPAFallback       bool              `json:"spa_fallback"`
	FrontController   bool              `json:"front_controller"`
	RuntimeVersion    string            `json:"runtime_version,omitempty"`
	PHPVersion        string            `json:"php_version,omitempty"`
	PHPBuild          string            `json:"php_build,omitempty"`
	PHPBuildPath      string            `json:"php_build_path,omitempty"`
	PHPIniPath        string            `json:"php_ini_path,omitempty"`
	FPMConfigPath     string            `json:"fpm_config_path,omitempty"`
	PHPHealthScript   string            `json:"php_health_script,omitempty"`
	SourceMode        string            `json:"source_mode,omitempty"`
}

// SitePrepareRequest prepares (validates and stages) a site.
type SitePrepareRequest struct {
	Site SiteSpec `json:"site"`
}

// SitePrepareResponse reports where the prepared site is reachable.
type SitePrepareResponse struct {
	BackendHost string `json:"backend_host"`
	HTTPPort    int    `json:"http_port"`
	FastCGIPort int    `json:"fastcgi_port"`
}

// SiteActionRequest starts or stops a site.
type SiteActionRequest struct {
	Graceful bool `json:"graceful,omitempty"`
}

// SiteActionResponse reports a lifecycle result.
type SiteActionResponse struct {
	Status  string `json:"status"`
	PID     int    `json:"pid,omitempty"`
	Message string `json:"message,omitempty"`
}

// SiteStatusResponse is the current site state.
type SiteStatusResponse struct {
	Status      string `json:"status"`
	PID         int    `json:"pid,omitempty"`
	Message     string `json:"message,omitempty"`
	HTTPPort    int    `json:"http_port,omitempty"`
	FastCGIPort int    `json:"fastcgi_port,omitempty"`
}

// SiteLogsRequest selects a finite snapshot or a stream.
type SiteLogsRequest struct {
	Follow bool `json:"follow"`
	Tail   int  `json:"tail,omitempty"`
}

// SiteExecRequest runs an argv in the site environment.
type SiteExecRequest struct {
	Argv      []string          `json:"argv"`
	Env       map[string]string `json:"env,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
}

// SiteExecResponse is the bounded command result.
type SiteExecResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// PHPPrepareRequest stages a PHP generation.
type PHPPrepareRequest struct {
	Generation string   `json:"generation"`
	Site       SiteSpec `json:"site"`
}

// PHPActionRequest starts or stops one PHP generation.
type PHPActionRequest struct {
	Generation string `json:"generation"`
}

// BuildRequest runs the site build command in WSL.
type BuildRequest struct {
	Command        string            `json:"command"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutMS      int               `json:"timeout_ms,omitempty"`
	BackendPath    string            `json:"backend_path,omitempty"`
	RuntimeVersion string            `json:"runtime_version,omitempty"`
}

// BuildResponse is the build result.
type BuildResponse struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// TerminalTicketRequest asks for a one-time terminal ticket.
type TerminalTicketRequest struct {
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

// TerminalTicketResponse is the WebSocket endpoint and one-time ticket.
type TerminalTicketResponse struct {
	URL       string    `json:"url"`
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RuntimeArtifact pins one downloadable runtime archive. The agent treats every
// field as untrusted and revalidates it before downloading; the checksum and
// size are checked again against the bytes that actually arrive.
type RuntimeArtifact struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Build   string `json:"build"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// RuntimeRequest installs or removes a runtime build. Artifact, when set,
// pins a release discovered outside the embedded catalog and takes precedence
// over the catalog selector fields.
type RuntimeRequest struct {
	Kind     string           `json:"kind,omitempty"`
	Version  string           `json:"version,omitempty"`
	BuildID  string           `json:"build_id,omitempty"`
	Artifact *RuntimeArtifact `json:"artifact,omitempty"`
}

// RuntimeResponse describes one installed runtime.
type RuntimeResponse struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	Backend  string `json:"backend"`
	Path     string `json:"path"`
	Checksum string `json:"checksum"`
}

// RuntimeListResponse lists installed runtimes.
type RuntimeListResponse struct {
	Runtimes []RuntimeResponse `json:"runtimes"`
}

// ServiceRequest performs one fixed service lifecycle action.
type ServiceRequest struct {
	ID     string            `json:"id"`
	Action string            `json:"action"`
	Config map[string]string `json:"config,omitempty"`
}

// ServiceResponse reports service state.
type ServiceResponse struct {
	ID      string         `json:"id"`
	State   string         `json:"state"`
	Ports   map[string]int `json:"ports,omitempty"`
	Message string         `json:"message,omitempty"`
}

// ServiceListResponse lists services.
type ServiceListResponse struct {
	Services []ServiceResponse `json:"services"`
}

// ServiceConfigRequest reads, applies, or resets one service config file.
type ServiceConfigRequest struct {
	ID      string `json:"id"`
	Rel     string `json:"rel,omitempty"`
	Content string `json:"content,omitempty"`
}

// ServiceConfigFile is one managed config file's metadata.
type ServiceConfigFile struct {
	Path     string `json:"path"`
	Class    string `json:"class"`
	Hash     string `json:"hash"`
	Modified bool   `json:"modified"`
	Detached bool   `json:"detached"`
}

// ServiceConfigResponse lists metadata without content.
type ServiceConfigResponse struct {
	Files []ServiceConfigFile `json:"files"`
}

// ServiceContentResponse carries one allowed config file.
type ServiceContentResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Hash    string `json:"hash"`
}

// ServiceCredentialRequest selects one credential field.
type ServiceCredentialRequest struct {
	ID    string `json:"id"`
	Field string `json:"field,omitempty"`
}

// ServiceCredentialResponse carries one credential value.
type ServiceCredentialResponse struct {
	ID    string `json:"id"`
	User  string `json:"user"`
	Value string `json:"value"`
}

// ServiceLogsRequest selects a service log snapshot.
type ServiceLogsRequest struct {
	Tail int `json:"tail,omitempty"`
}

// ServiceLogsResponse is a finite log snapshot.
type ServiceLogsResponse struct {
	Lines []string `json:"lines"`
}

// WebTarget is one private HTTP target the daemon may proxy to.
type WebTarget struct {
	SiteID  string `json:"site_id"`
	Domain  string `json:"domain"`
	Kind    string `json:"kind"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	TLS     bool   `json:"tls"`
}

// WebTargetsRequest replaces the agent's target set.
type WebTargetsRequest struct {
	Targets []WebTarget `json:"targets"`
}

// WebSyncResponse reports a vhost sync.
type WebSyncResponse struct {
	Changed int `json:"changed"`
}

// HostsRequest replaces the WSL hosts block.
type HostsRequest struct {
	Domains []string `json:"domains"`
}

// HostsResponse reports whether the hosts file changed.
type HostsResponse struct {
	Changed bool `json:"changed"`
}

// CertRequest issues or reloads one leaf certificate.
type CertRequest struct {
	Owner   string   `json:"owner"`
	Domains []string `json:"domains"`
}

// CertResponse carries leaf certificate material.
type CertResponse struct {
	CertPEM  []byte    `json:"cert_pem"`
	KeyPEM   []byte    `json:"key_pem,omitempty"`
	NotAfter time.Time `json:"not_after"`
}

// CAResponse carries the root certificate.
type CAResponse struct {
	CertPEM     []byte `json:"cert_pem"`
	Fingerprint string `json:"fingerprint"`
}

// CAInstallRequest installs the Windows daemon's root certificate as the WSL
// trust anchor used by site and terminal environments.
type CAInstallRequest struct {
	CertPEM []byte `json:"cert_pem"`
}

// PathValidateRequest checks one source path.
type PathValidateRequest struct {
	Mode string `json:"mode"`
	Path string `json:"path"`
}

// PathValidateResponse reports translated and resolved paths.
type PathValidateResponse struct {
	HostPath    string `json:"host_path"`
	BackendPath string `json:"backend_path"`
	Exists      bool   `json:"exists"`
}

// FSEntry represents one directory entry in a WSL filesystem listing.
type FSEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FSListing represents a directory listing response from WSL.
type FSListing struct {
	Path    string    `json:"path"`
	Entries []FSEntry `json:"entries"`
}

// SourceCopyRequest moves a project between Windows and WSL with verification.
type SourceCopyRequest struct {
	Mode         string `json:"mode"`
	Source       string `json:"source"`
	Destination  string `json:"destination"`
	RemoveSource bool   `json:"remove_source,omitempty"`
}

// SourceCopyResponse reports the verified copy.
type SourceCopyResponse struct {
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
