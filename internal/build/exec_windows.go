//go:build windows

package build

import (
	"os/exec"
	"time"
)

// configureBuildCommand keeps the default process-local cancellation on
// Windows, where there is no POSIX process group to signal.
func configureBuildCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.WaitDelay = 2 * time.Second
}
