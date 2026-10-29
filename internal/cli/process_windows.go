//go:build windows

package cli

import (
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// probeProcess checks process existence with a query-only handle. Windows has
// no signal 0; an inaccessible or missing process maps to ESRCH so the caller's
// existing error classification stays correct.
// processRunning reports whether the Windows process is still active.
func processRunning(pid int) bool {
	return probeProcess(pid) == nil
}

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

// processIsDevstack reports whether pid is a live devstack daemon. A stale pid
// file must never authorize a force-kill of an unrelated process on Windows,
// where there is no /proc start-time identity to compare.
func processIsDevstack(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return false
	}
	base := strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
	return base == "devstack.exe"
}
