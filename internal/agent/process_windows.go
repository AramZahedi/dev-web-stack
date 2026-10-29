//go:build windows

package agent

import "os/exec"

func configureFPMCmd(cmd *exec.Cmd) {}

func killFPMProcess(cmd *exec.Cmd, pid int) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
