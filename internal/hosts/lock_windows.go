//go:build windows

package hosts

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on the hosts file for the in-place
// fallback. Windows has no flock; LockFileEx provides the same serialization
// between helper processes and other writers that cooperate with byte-range
// locks. The whole file is covered by a single-byte range because Windows
// locks are mandatory and range based.
func lockFile(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped)
}

func unlockFile(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, overlapped)
}
