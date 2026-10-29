//go:build !windows

package agent

import (
	"errors"
	"os"
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

// configureBuildCmd makes a build/exec command its own process-group leader and
// kills the whole group when its context is cancelled. Shelling out to
// npm/pnpm/vite spawns grandchildren; killing only the shell leaves them
// running and holding the project's files open.
func configureBuildCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
}

func killFPMProcess(cmd *exec.Cmd, pid int, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil || pid <= 0 || cmd.Process.Pid != pid {
		return
	}
	// The child is reaped by the waiter goroutine. Once done closes, its PID
	// (and process group ID) can be reused; never signal it again.
	exited := func() bool {
		if done == nil {
			return false
		}
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	if exited() {
		return
	}
	// Signal the process group with SIGQUIT first (gracefully stops FPM master and workers)
	_ = syscall.Kill(-pid, syscall.SIGQUIT)
	t := time.NewTimer(250 * time.Millisecond)
	select {
	case <-t.C:
	case <-done:
		t.Stop()
		return
	}
	// Signal with SIGTERM if still running
	if exited() {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	t2 := time.NewTimer(250 * time.Millisecond)
	select {
	case <-t2.C:
	case <-done:
		t2.Stop()
		return
	}
	// Force kill entire process group
	if !exited() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
