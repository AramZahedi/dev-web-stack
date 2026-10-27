// Package model holds the shared types persisted by the daemon and exchanged
// with the CLI and dashboard.
package model

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

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

// SourceMode identifies where a site's project files live relative to the
// Windows daemon. Linux only uses SourceLocal.
type SourceMode string

const (
	SourceLocal   SourceMode = "local"
	SourceWindows SourceMode = "windows"
	SourceWSL     SourceMode = "wsl"
)

// ValidSourceMode reports whether mode is one of the supported source modes.
func ValidSourceMode(mode SourceMode) bool {
	switch mode {
	case SourceLocal, SourceWindows, SourceWSL:
		return true
	default:
		return false
	}
}

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
	StatusDegraded SiteStatus = "degraded"
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
	SourceMode  SourceMode  `json:"source_mode"`
	HostPath    string      `json:"host_path"`
	BackendPath string      `json:"backend_path"`
	Kind        SiteKind    `json:"kind"`

	PHPVersion    string            `json:"php_version"`
	PHPBuild      string            `json:"php_build"`
	PHPExtensions map[string]bool   `json:"php_extensions"`
	PHPIni        map[string]string `json:"php_ini"`
	PHPIniRaw     string            `json:"php_ini_raw"`
	PHPConfigHash string            `json:"php_config_hash"`
	PHPFPM        *PHPFPMBudget     `json:"php_fpm,omitempty"`

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

// PHPFPMBudget is the persisted pool sizing for one PHP site's php-fpm
// master. A nil budget (or a zero value) leaves the renderer's conservative
// defaults in place. Mode is "dynamic" or "static"; empty means dynamic.
type PHPFPMBudget struct {
	Mode            string `json:"mode,omitempty"`
	MaxChildren     int    `json:"max_children,omitempty"`
	StartServers    int    `json:"start_servers,omitempty"`
	MinSpareServers int    `json:"min_spare_servers,omitempty"`
	MaxSpareServers int    `json:"max_spare_servers,omitempty"`
	ListenBacklog   int    `json:"listen_backlog,omitempty"`
}

// IsZero reports whether the budget carries no pool override.
func (b PHPFPMBudget) IsZero() bool {
	return b.Mode == "" && b.MaxChildren == 0 && b.StartServers == 0 &&
		b.MinSpareServers == 0 && b.MaxSpareServers == 0 && b.ListenBacklog == 0
}

// Validate rejects pool values that php-fpm cannot use. An empty or zero budget
// is valid because it selects the renderer defaults.
func (b PHPFPMBudget) Validate() error {
	if b.IsZero() {
		return nil
	}
	mode := b.Mode
	if mode == "" {
		mode = "dynamic"
	}
	if mode != "dynamic" && mode != "static" {
		return fmt.Errorf("PHP-FPM mode %q must be dynamic or static", b.Mode)
	}
	if b.MaxChildren < 1 || b.MaxChildren > 100000 {
		return fmt.Errorf("PHP-FPM max_children %d must be between 1 and 100000", b.MaxChildren)
	}
	for _, field := range []struct {
		name  string
		value int
	}{
		{"start_servers", b.StartServers},
		{"min_spare_servers", b.MinSpareServers},
		{"max_spare_servers", b.MaxSpareServers},
	} {
		if field.value < 0 || field.value > b.MaxChildren {
			return fmt.Errorf("PHP-FPM %s %d must be between 0 and max_children %d", field.name, field.value, b.MaxChildren)
		}
	}
	if b.MaxSpareServers > 0 && b.MinSpareServers > b.MaxSpareServers {
		return fmt.Errorf("PHP-FPM min_spare_servers %d must not exceed max_spare_servers %d", b.MinSpareServers, b.MaxSpareServers)
	}
	if mode == "dynamic" {
		if b.StartServers > 0 && b.MinSpareServers > 0 && b.StartServers < b.MinSpareServers {
			return fmt.Errorf("PHP-FPM start_servers %d must not be below min_spare_servers %d", b.StartServers, b.MinSpareServers)
		}
		if b.StartServers > 0 && b.MaxSpareServers > 0 && b.StartServers > b.MaxSpareServers {
			return fmt.Errorf("PHP-FPM start_servers %d must not exceed max_spare_servers %d", b.StartServers, b.MaxSpareServers)
		}
	}
	if b.ListenBacklog < 0 || b.ListenBacklog > 65535 {
		return fmt.Errorf("PHP-FPM listen_backlog %d must be between 0 and 65535", b.ListenBacklog)
	}
	return nil
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

// ValidateSiteContracts validates the site contract fields:
// front-controller file, mode, apache allow override, SPA fallback file, and build profile.
func ValidateSiteContracts(fcFile, fcMode, override, spaFile string, build *BuildProfile) error {
	if fcFile != "" {
		if filepath.IsAbs(fcFile) || strings.HasPrefix(fcFile, "/") || strings.HasPrefix(fcFile, "\\") {
			return errors.New("sites: front controller file must be a relative path")
		}
		if strings.ContainsRune(fcFile, '\x00') {
			return errors.New("sites: invalid front controller file: contains NUL bytes")
		}
		if !strings.HasSuffix(strings.ToLower(fcFile), ".php") {
			return errors.New("sites: front controller file must end with .php")
		}
		clean := filepath.Clean(fcFile)
		if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, "..") {
			return errors.New("sites: front controller file cannot traverse outside root")
		}
		for _, seg := range strings.FieldsFunc(fcFile, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
				return fmt.Errorf("sites: front controller file contains invalid dot segment %q", seg)
			}
		}
	}

	if fcMode != "" {
		switch fcMode {
		case "auto", "generated", "htaccess":
		default:
			return fmt.Errorf("sites: invalid front controller mode %q (must be auto, generated, or htaccess)", fcMode)
		}
	}

	if override != "" {
		switch override {
		case "all", "fileinfo", "none":
		default:
			return fmt.Errorf("sites: invalid apache allow override %q (must be all, fileinfo, or none)", override)
		}
	}

	if spaFile != "" {
		if filepath.IsAbs(spaFile) || strings.HasPrefix(spaFile, "/") || strings.HasPrefix(spaFile, "\\") {
			return errors.New("sites: spa fallback file must be a relative path")
		}
		if strings.ContainsRune(spaFile, '\x00') {
			return errors.New("sites: invalid spa fallback file: contains NUL bytes")
		}
		if strings.HasSuffix(strings.ToLower(spaFile), ".php") {
			return errors.New("sites: spa fallback file cannot be a .php file")
		}
		clean := filepath.Clean(spaFile)
		if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, "..") {
			return errors.New("sites: spa fallback file cannot traverse outside root")
		}
		for _, seg := range strings.FieldsFunc(spaFile, func(r rune) bool { return r == '/' || r == '\\' }) {
			if seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
				return fmt.Errorf("sites: spa fallback file contains invalid dot segment %q", seg)
			}
		}
	}

	if build != nil {
		cmd := strings.TrimSpace(build.Command)
		nodeVer := strings.TrimSpace(build.NodeVersion)
		out := strings.TrimSpace(build.Output)
		// If build profile has any content, both command and node_version are required together
		if cmd != "" || nodeVer != "" || out != "" || len(build.Env) > 0 {
			if cmd == "" || nodeVer == "" {
				return errors.New("sites: build command and node version are required together (invalid build profile)")
			}
		}
		if out != "" {
			if filepath.IsAbs(out) || strings.HasPrefix(out, "/") || strings.HasPrefix(out, "\\") {
				return errors.New("sites: build output must be a relative path")
			}
			if strings.ContainsRune(out, '\x00') {
				return errors.New("sites: invalid build output: contains NUL bytes")
			}
			clean := filepath.Clean(out)
			if strings.HasPrefix(clean, "..") || strings.Contains(clean, "..") {
				return errors.New("sites: build output cannot traverse outside project")
			}
		}
	}

	return nil
}
