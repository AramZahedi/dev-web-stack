//go:build !windows

package daemon

import (
	"path/filepath"

	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

// Endpoint returns the daemon's local control endpoint.
func Endpoint(home string) ipc.Endpoint {
	return ipc.UnixEndpoint(filepath.Join(home, "run", "daemon.sock"))
}
