//go:build !windows

package platform

import (
	"os/exec"
	"syscall"
)

// ConfigureProcess puts Unix children in their own process group and keeps
// the daemon's terminal detached.
func ConfigureProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
