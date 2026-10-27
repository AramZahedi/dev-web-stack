//go:build windows

package daemon

import "path/filepath"

// DefaultHelperSocket is the production helper endpoint on Linux. The Windows
// helper uses a named pipe; Lane A and Lane C replace this value with the
// platform transport.
const DefaultHelperSocket = `\\.\pipe\devstack-helper`

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
