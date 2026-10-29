//go:build !windows

package build

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// configureBuildCommand puts the shell in its own process group and kills the
// whole group when the build context is cancelled. A build script spawns
// npm/pnpm/vite children; killing only /bin/sh leaves them running and holding
// the project directory open.
func configureBuildCommand(cmd *exec.Cmd) {
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
