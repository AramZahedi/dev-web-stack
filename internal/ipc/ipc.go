// Package ipc provides the local control transport shared by the daemon, the
// privileged helper, and the CLI. Linux uses Unix-domain sockets; Windows uses
// named pipes with an explicit ACL and peer-SID verification.
package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"
)

const (
	// NetworkUnix is the Unix-domain socket transport.
	NetworkUnix = "unix"
	// NetworkNamedPipe is the Windows named-pipe transport.
	NetworkNamedPipe = "npipe"

	namedPipePrefix = `\\.\pipe\`

	// SIDLocalSystem and SIDAdministrators are the well-known Windows SIDs
	// that are always allowed to reach privileged helper control pipes when
	// the corresponding policy grants them.
	SIDLocalSystem    = "S-1-5-18"
	SIDAdministrators = "S-1-5-32-544"
)

var (
	// ErrInvalidEndpoint is returned for an empty, relative, or unknown
	// control endpoint.
	ErrInvalidEndpoint = errors.New("ipc: invalid endpoint")
	// ErrPeerUnauthorized is returned when a connected peer does not match the
	// configured UID/SID policy.
	ErrPeerUnauthorized = errors.New("ipc: peer is not authorized")
	// ErrUnsupported is returned when a transport is not available on the
	// current platform.
	ErrUnsupported = errors.New("ipc: transport is not supported on this platform")
)

// Endpoint identifies one local control transport.
type Endpoint struct {
	Network string
	Address string
}

// UnixEndpoint returns a Unix-domain socket endpoint.
func UnixEndpoint(path string) Endpoint {
	return Endpoint{Network: NetworkUnix, Address: path}
}

// NamedPipeEndpoint returns a Windows named-pipe endpoint. The name may be
// supplied with or without the \\.\pipe\ prefix.
func NamedPipeEndpoint(name string) Endpoint {
	name = strings.TrimPrefix(name, namedPipePrefix)
	return Endpoint{Network: NetworkNamedPipe, Address: namedPipePrefix + name}
}

// EndpointFromAddress classifies a configured address. Named-pipe addresses
// use the Windows prefix; everything else is a Unix socket path.
func EndpointFromAddress(address string) Endpoint {
	if strings.HasPrefix(address, namedPipePrefix) {
		return Endpoint{Network: NetworkNamedPipe, Address: address}
	}
	return Endpoint{Network: NetworkUnix, Address: address}
}

// Validate rejects empty, relative, or unknown endpoints before any listener
// or dialer touches the operating system.
func (e Endpoint) Validate() error {
	switch e.Network {
	case NetworkUnix:
		if strings.TrimSpace(e.Address) == "" {
			return fmt.Errorf("%w: empty Unix socket path", ErrInvalidEndpoint)
		}
		if !strings.HasPrefix(e.Address, "/") && !filepath.IsAbs(e.Address) {
			return fmt.Errorf("%w: Unix socket path %q must be absolute", ErrInvalidEndpoint, e.Address)
		}
		return nil
	case NetworkNamedPipe:
		if !strings.HasPrefix(e.Address, namedPipePrefix) || strings.TrimPrefix(e.Address, namedPipePrefix) == "" {
			return fmt.Errorf("%w: named pipe %q must look like %s<name>", ErrInvalidEndpoint, e.Address, namedPipePrefix)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown network %q", ErrInvalidEndpoint, e.Network)
	}
}

// PeerIdentity is the authenticated identity of the process on the other end
// of a control connection. Exactly one field is populated per platform.
type PeerIdentity struct {
	UID int
	SID string
}

// PeerPolicy is the allowlist applied to accepted control connections.
type PeerPolicy struct {
	AllowUID            int
	AllowSID            string
	AllowAdministrators bool
}

// Authorize checks a peer identity against the policy. SYSTEM is always
// allowed; Administrators is allowed only when the policy opts in.
func (p PeerPolicy) Authorize(peer PeerIdentity) error {
	if p.AllowUID > 0 && peer.UID == p.AllowUID {
		return nil
	}
	if peer.SID != "" {
		if p.AllowSID != "" && strings.EqualFold(peer.SID, p.AllowSID) {
			return nil
		}
		if strings.EqualFold(peer.SID, SIDLocalSystem) {
			return nil
		}
		if p.AllowAdministrators && strings.EqualFold(peer.SID, SIDAdministrators) {
			return nil
		}
	}
	return ErrPeerUnauthorized
}

type listenConfig struct {
	policy PeerPolicy
}

// Option configures a listener.
type Option func(*listenConfig)

// WithAllowUID permits one Unix UID to connect.
func WithAllowUID(uid int) Option {
	return func(c *listenConfig) { c.policy.AllowUID = uid }
}

// WithAllowSID permits one Windows user SID to connect and sets the pipe ACL.
func WithAllowSID(sid string) Option {
	return func(c *listenConfig) { c.policy.AllowSID = sid }
}

// WithAdministrators includes the Administrators group in the pipe ACL and
// peer policy. Helper control pipes use it; daemon control pipes do not.
func WithAdministrators(allow bool) Option {
	return func(c *listenConfig) { c.policy.AllowAdministrators = allow }
}

func newListenConfig(opts []Option) listenConfig {
	var cfg listenConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// Listen opens a control listener and verifies the identity of every accepted
// peer before returning the connection to the HTTP server.
func Listen(endpoint Endpoint, opts ...Option) (net.Listener, error) {
	if err := endpoint.Validate(); err != nil {
		return nil, err
	}
	cfg := newListenConfig(opts)
	ln, err := listen(endpoint, cfg)
	if err != nil {
		return nil, err
	}
	return verifyListener(ln, cfg.policy), nil
}

// Dial connects to a control endpoint with a bounded timeout.
func Dial(ctx context.Context, endpoint Endpoint, timeout time.Duration) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := endpoint.Validate(); err != nil {
		return nil, err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return dial(ctx, endpoint)
}

type peerListener struct {
	net.Listener
	policy PeerPolicy
}

func verifyListener(ln net.Listener, policy PeerPolicy) net.Listener {
	// An empty policy is only used by tests and by endpoints whose transport
	// already enforces access (for example a named pipe with no SDDL).
	if policy.AllowUID == 0 && policy.AllowSID == "" && !policy.AllowAdministrators {
		return ln
	}
	return &peerListener{Listener: ln, policy: policy}
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		peer, err := PeerIdentityOf(conn)
		if err == nil {
			err = l.policy.Authorize(peer)
		}
		if err != nil {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}
