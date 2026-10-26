//go:build windows

package cli

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// probeProcess checks process existence with a query-only handle. Windows has
// no signal 0; an inaccessible or missing process maps to ESRCH so the caller's
// existing error classification stays correct.
func probeProcess(pid int) error {
	if pid <= 0 {
		return syscall.ESRCH
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return syscall.ESRCH
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return syscall.ESRCH
	}
	const stillActive = 259 // STATUS_PENDING / STILL_ACTIVE
	if code == stillActive {
		return nil
	}
	return syscall.ESRCH
}
