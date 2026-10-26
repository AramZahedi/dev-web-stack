//go:build !windows

package hosts

import (
	"os"
	"syscall"
)

// lockFile takes an advisory exclusive lock on the hosts file for the
// in-place fallback. Unix uses flock so concurrent helper writes serialize.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
