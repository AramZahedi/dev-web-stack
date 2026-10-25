// Package helperproto defines the JSON protocol shared by the privileged
// helper and its clients.
package helperproto

import (
	"errors"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

const (
	// Version is the helper protocol version carried by every request and
	// advertised in protocol-mismatch responses.
	Version = "1"
	// ProtocolHeader is the HTTP header carrying Version.
	ProtocolHeader = "X-Devstack-Protocol"
)

// Helper routes.
const (
	PathStatus       = "/v1/status"
	PathHostsSync    = "/v1/hosts/sync"
	PathEnsureCA     = "/v1/ca/ensure"
	PathSign         = "/v1/ca/sign"
	PathTrustInstall = "/v1/trust/install"
	PathTrustRemove  = "/v1/trust/remove"
	PathTrustStatus  = "/v1/trust/status"
	PathForwarders   = "/v1/forwarders"
	PathUninstall    = "/v1/uninstall"
)

// Stable error codes returned in ErrorResponse.
const (
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeProtocolMismatch = "PROTOCOL_MISMATCH"
	CodeInvalidRequest   = "INVALID_REQUEST"
	CodeNotImplemented   = "NOT_IMPLEMENTED"
	CodeInternal         = "INTERNAL"
	CodeServiceConflict  = "SERVICE_CONFLICT"
)

// Sentinel errors matched by clients. Servers and privileged operation
// implementations may wrap these to choose the response code.
var (
	ErrUnauthorized     = errors.New("helper: unauthorized")
	ErrProtocolMismatch = errors.New("helper: protocol mismatch")
	ErrInvalidRequest   = errors.New("helper: invalid request")
	ErrNotImplemented   = errors.New("helper: not implemented")
	ErrInternal         = errors.New("helper: internal error")
	ErrServiceConflict  = errors.New("helper: service conflict")
)

// ErrorResponse is the body of every helper error response.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusRequest is the request body for Status. It is empty.
type StatusRequest struct{}

// StatusResponse reports the helper protocol version, the active CA
// fingerprint (if a CA exists), and whether the managed hosts block is present.
type StatusResponse struct {
	Version       string `json:"version"`
	CAFingerprint string `json:"ca_fingerprint"`
	HostsSynced   bool   `json:"hosts_synced"`
}

// HostsSyncRequest asks the helper to replace the managed hosts block.
type HostsSyncRequest struct {
	Domains []string `json:"domains"`
}

// HostsSyncResponse reports whether the hosts file changed.
type HostsSyncResponse struct {
	Changed bool `json:"changed"`
}

// EnsureCAResponse contains the root certificate and its SHA-256 fingerprint.
type EnsureCAResponse struct {
	RootPEM     []byte `json:"root_pem"`
	Fingerprint string `json:"fingerprint"`
}

// SignRequest asks the helper to sign a certificate for the given DNS names.
type SignRequest struct {
	CSR     []byte   `json:"csr"`
	Domains []string `json:"domains"`
}

// SignResponse contains the issued leaf certificate.
type SignResponse struct {
	CertPEM  []byte    `json:"cert_pem"`
	Serial   string    `json:"serial"`
	NotAfter time.Time `json:"not_after"`
}

// TrustRequest identifies the user whose trust stores should be managed.
type TrustRequest struct {
	UserHome string `json:"user_home"`
}

// TrustResponse contains one result per trust target.
type TrustResponse struct {
	Targets []trust.Target `json:"targets"`
}

// ForwardersRequest enables or disables the privileged listeners. HTTPPort and
// HTTPSPort must name the helper's configured loopback proxy ports; zero uses
// the configured values. The privileged listen ports are fixed by the helper's
// configuration and are never taken from the request.
type ForwardersRequest struct {
	HTTPPort  int  `json:"http_port"`
	HTTPSPort int  `json:"https_port"`
	Enable    bool `json:"enable"`
}

// UninstallRequest asks the helper to remove everything it installed.
type UninstallRequest struct{}
