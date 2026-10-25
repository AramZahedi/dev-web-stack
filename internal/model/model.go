// Package model holds the shared types persisted by the daemon and exchanged
// with the CLI and dashboard.
package model

import "time"

// SiteKind identifies how a site is served.
type SiteKind string

const (
	SiteStatic SiteKind = "static"
	SitePHP    SiteKind = "php"
	SiteNode   SiteKind = "node"
)

// BackendKind identifies the execution namespace a site runs in.
type BackendKind string

const (
	BackendLocal BackendKind = "local"
	BackendWSL   BackendKind = "wsl"
)

// DesiredState is the lifecycle state the supervisor should converge on.
type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
)

// SiteStatus is the observed lifecycle state of a site.
type SiteStatus string

const (
	StatusStopped  SiteStatus = "stopped"
	StatusStarting SiteStatus = "starting"
	StatusRunning  SiteStatus = "running"
	StatusStopping SiteStatus = "stopping"
	StatusCrashed  SiteStatus = "crashed"
	StatusError    SiteStatus = "error"
)

// BuildProfile configures an optional client-side asset build.
type BuildProfile struct {
	NodeVersion string            `json:"node_version,omitempty"`
	Command     string            `json:"command,omitempty"`
	Output      string            `json:"output,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// Site is a managed local web project.
type Site struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Domain      string      `json:"domain"`
	WWW         bool        `json:"www"`
	Backend     BackendKind `json:"backend"`
	HostPath    string      `json:"host_path"`
	BackendPath string      `json:"backend_path"`
	Kind        SiteKind    `json:"kind"`

	PHPVersion    string            `json:"php_version"`
	PHPBuild      string            `json:"php_build"`
	PHPExtensions map[string]bool   `json:"php_extensions"`
	PHPIni        map[string]string `json:"php_ini"`
	PHPIniRaw     string            `json:"php_ini_raw"`
	PHPConfigHash string            `json:"php_config_hash"`

	NodeVersion string            `json:"node_version"`
	Command     string            `json:"command"`
	Env         map[string]string `json:"env"`

	DocumentRoot        string        `json:"document_root"`
	FrontController     bool          `json:"front_controller"`
	FrontControllerFile string        `json:"front_controller_file"`
	FrontControllerMode string        `json:"front_controller_mode"`
	ApacheAllowOverride string        `json:"apache_allow_override"`
	SPAFallback         bool          `json:"spa_fallback"`
	SPAFallbackFile     string        `json:"spa_fallback_file"`
	Build               *BuildProfile `json:"build,omitempty"`

	TLS        bool `json:"tls"`
	ForceHTTPS bool `json:"force_https"`

	DesiredState DesiredState `json:"desired_state"`
	Status       SiteStatus   `json:"status"`

	// HTTPPort and FastCGIPort are the allocated backend ports; zero when the
	// site is not running or the kind does not use that transport.
	HTTPPort    int `json:"http_port"`
	FastCGIPort int `json:"fastcgi_port"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PHPDefaults holds the editable per-version php.ini directives and extension
// defaults. Every site on the version inherits these unless it overrides them.
type PHPDefaults struct {
	Version    string            `json:"version"`
	Ini        map[string]string `json:"ini"`
	Extensions map[string]bool   `json:"extensions"`
}

// SitePHPConfig is the last applied per-site PHP configuration snapshot.
type SitePHPConfig struct {
	SiteID     string            `json:"site_id"`
	Ini        map[string]string `json:"ini"`
	Extensions map[string]bool   `json:"extensions"`
	ConfigHash string            `json:"config_hash"`
	AppliedAt  time.Time         `json:"applied_at"`
}

// RuntimeBuild is an installed PHP or Node distribution.
type RuntimeBuild struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Version     string      `json:"version"`
	Backend     BackendKind `json:"backend"`
	Path        string      `json:"path"`
	Checksum    string      `json:"checksum"`
	InstalledAt time.Time   `json:"installed_at"`
}

// CertRecord is the metadata for a site certificate issued by the local CA.
type CertRecord struct {
	SiteID   string    `json:"site_id"`
	Serial   string    `json:"serial"`
	Domains  []string  `json:"domains"`
	NotAfter time.Time `json:"not_after"`
	KeyPath  string    `json:"key_path"`
	CertPath string    `json:"cert_path"`
}

// Event records a recent state transition for diagnostics.
type Event struct {
	ID        int64     `json:"id"`
	SiteID    string    `json:"site_id,omitempty"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}
