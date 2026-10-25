//go:build linux

package terminal

import "syscall"

// signalGroup signals the process group created by the PTY session. creack/pty
// starts the shell with Setsid, so its process group ID equals its PID. A
// negative PID targets every descendant still in that group.
func signalGroup(pid int, force bool) {
	if pid <= 0 {
		return
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, signal)
}
