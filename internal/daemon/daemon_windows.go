//go:build windows

package daemon

import (
	"context"
	"errors"
	"path/filepath"
)

// DefaultHelperSocket is the production helper endpoint on Linux. The Windows
// helper uses a named pipe; Lane A and Lane C replace this value with the
// platform transport.
const DefaultHelperSocket = `\\.\pipe\devstack-helper`

// ErrWindowsDaemonPending is returned until Lane C wires the Windows daemon,
// public proxy, and WSL backend.
var ErrWindowsDaemonPending = errors.New("daemon: the Windows daemon is not implemented yet")

// Options configures one daemon run.
type Options struct {
	Home         string
	HelperSocket string
	HostsPath    string
	HTTPPort     int
	HTTPSPort    int
	UIPort       int
	LogLevel     string
}

// SocketPath returns the Windows named-pipe address for home.
func SocketPath(home string) string {
	return Endpoint(home).Address
}

// PidPath returns the daemon pid file path below home.
func PidPath(home string) string {
	return filepath.Join(home, "run", "daemon.pid")
}

// StartTicksPath is unused on Windows; it remains for CLI compatibility.
func StartTicksPath(home string) string {
	return filepath.Join(home, "run", "daemon.start")
}

// Run reports that the Windows daemon must be wired by Lane C.
func Run(context.Context, Options) error { return ErrWindowsDaemonPending }

// EnsureRunning reports that the Windows daemon must be wired by Lane C.
func EnsureRunning(context.Context, Options) error { return ErrWindowsDaemonPending }
