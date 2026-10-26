//go:build windows

package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/AramZahedi/dev-web-stack/internal/ipc"
)

// Endpoint returns a stable per-home named pipe. The hash keeps two users or
// two roots from colliding and avoids exposing a filesystem path in the pipe
// namespace.
func Endpoint(home string) ipc.Endpoint {
	abs, err := filepath.Abs(home)
	if err != nil {
		abs = home
	}
	sum := sha256.Sum256([]byte(abs))
	return ipc.NamedPipeEndpoint("devstack-" + hex.EncodeToString(sum[:8]))
}
