//go:build windows

package agent

import "os/exec"

func configureFPMCmd(cmd *exec.Cmd) {}

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
