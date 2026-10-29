//go:build windows

package agent

import (
	"os/exec"
	"time"
)

func configureFPMCmd(cmd *exec.Cmd) {}

// configureBuildCmd keeps the default process-local cancellation on Windows:
// the agent's child is cmd.exe and no POSIX process group exists.
func configureBuildCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.WaitDelay = 2 * time.Second
}

func killFPMProcess(cmd *exec.Cmd, pid int, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil || pid <= 0 {
		return
	}
	if done != nil {
		select {
		case <-done:
			return
		default:
		}
	}
	_ = cmd.Process.Kill()
}
