//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
	"time"
)

func configureFPMCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killFPMProcess(cmd *exec.Cmd, pid int, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil || pid <= 0 {
		return
	}
	if done != nil {
		select {
		case <-done:
			// The child was already reaped; its PID may have been reused.
			return
		default:
		}
	}
	// Signal the process group with SIGQUIT first (gracefully stops FPM master and workers)
	_ = syscall.Kill(-pid, syscall.SIGQUIT)
	t := time.NewTimer(250 * time.Millisecond)
	select {
	case <-t.C:
	}
	// Signal with SIGTERM if still running
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	t2 := time.NewTimer(250 * time.Millisecond)
	select {
	case <-t2.C:
	}
	// Force kill entire process group
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
