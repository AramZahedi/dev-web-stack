//go:build !windows

package cli

import "syscall"

// probeProcess checks process existence without delivering a signal.
func probeProcess(pid int) error {
	return syscall.Kill(pid, 0)
}
