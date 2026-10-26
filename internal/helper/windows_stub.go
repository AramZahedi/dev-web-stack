//go:build windows

package helper

import "errors"

// ErrWindowsHelperPending is returned until Lane A replaces this stub with the
// LocalSystem helper implementation (named pipe, CA/trust/hosts/firewall,
// forwarders, health).
var ErrWindowsHelperPending = errors.New("helper: the Windows LocalSystem helper is not implemented yet")

// Config is the Windows helper configuration surface. Lane A adds the pipe,
// SID, CA, trust, hosts, firewall, and forwarder fields.
type Config struct{}

// Server is the Windows helper server stub.
type Server struct{}

// NewServer returns the typed not-implemented error required by the frozen
// foundation contract.
func NewServer(Config) (*Server, error) {
	return nil, ErrWindowsHelperPending
}
