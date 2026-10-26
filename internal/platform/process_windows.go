//go:build windows

package platform

import (
	"os/exec"
	"syscall"
)

// ConfigureProcess hides Windows console windows for background children.
func ConfigureProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
