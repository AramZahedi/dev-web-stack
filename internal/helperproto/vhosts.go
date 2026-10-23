package helperproto

import (
	"errors"
	"time"
)

// Vhost routes.
const (
	PathVhostsStatus = "/v1/vhosts/status"
	PathVhostsSync   = "/v1/vhosts/sync"
	PathVhostsMode   = "/v1/vhosts/mode"
	PathVhosts       = "/v1/vhosts"
)

// Stable error codes returned by the vhost operations.
const (
	CodeServingModeUnavailable = "SERVING_MODE_UNAVAILABLE"
	CodeVhostConfigInvalid     = "VHOST_CONFIG_INVALID"
	CodeVhostSyncFailed        = "VHOST_SYNC_FAILED"
	CodeVhostIncludeMissing    = "VHOST_INCLUDE_MISSING"
	CodeServerReloadFailed     = "SERVER_RELOAD_FAILED"
	CodeVhostSyncBusy          = "VHOST_SYNC_BUSY"
)

// Sentinel errors matched by clients. The helper maps each to a stable code
// and HTTP status.
var (
	ErrServingModeUnavailable = errors.New("helper: serving mode unavailable")
	ErrVhostConfigInvalid     = errors.New("helper: vhost config is invalid")
	ErrVhostSyncFailed        = errors.New("helper: vhost sync failed")
	ErrVhostIncludeMissing    = errors.New("helper: vhost include is missing or unsupported")
	ErrServerReloadFailed     = errors.New("helper: server reload failed")
	ErrVhostSyncBusy          = errors.New("helper: a vhost sync is already running")
)

// VHostSite describes one site to install as a native server vhost.
type VHostSite struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Domain          string `json:"domain"`
	BackendPath     string `json:"backend_path"`
	WWW             bool   `json:"www"`
	Kind            string `json:"kind"`
	DocumentRoot    string `json:"document_root"`
	BackendPort     int    `json:"backend_port"`
	TLS             bool   `json:"tls"`
	ForceHTTPS      bool   `json:"force_https"`
	SPAFallback     bool   `json:"spa_fallback"`
	FrontController bool   `json:"front_controller"`
	DesiredState    string `json:"desired_state"`
	CertPEM         []byte `json:"cert_pem,omitempty"`
	KeyPEM          []byte `json:"key_pem,omitempty"`
}

// VHostDashboard describes the dashboard vhost.
type VHostDashboard struct {
	Domain      string `json:"domain"`
	BackendPort int    `json:"backend_port"`
	CertPEM     []byte `json:"cert_pem,omitempty"`
	KeyPEM      []byte `json:"key_pem,omitempty"`
}

// VHostSyncRequest installs a complete vhost set.
type VHostSyncRequest struct {
	Mode         string         `json:"mode"`
	MaxBodyBytes int64          `json:"max_body_bytes"`
	Sites        []VHostSite    `json:"sites"`
	Dashboard    VHostDashboard `json:"dashboard"`
	RequestedAt  time.Time      `json:"requested_at"`
}

// VhostModeRequest switches the serving mode. Sync is required for nginx and
// apache and ignored for builtin.
type VhostModeRequest struct {
	Mode string            `json:"mode"`
	Sync *VHostSyncRequest `json:"sync,omitempty"`
}

// VhostStatusResponse reports the persisted vhost state.
type VhostStatusResponse struct {
	Mode           string    `json:"mode"`
	Server         string    `json:"server"`
	ServerVersion  string    `json:"server_version"`
	BridgePath     string    `json:"bridge_path"`
	IncludePresent bool      `json:"include_present"`
	CurrentRelease string    `json:"current_release"`
	ConfigOK       bool      `json:"config_ok"`
	LastError      string    `json:"last_error,omitempty"`
	LastSync       time.Time `json:"last_sync"`
}
