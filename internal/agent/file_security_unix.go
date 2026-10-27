//go:build !windows

package agent

import (
	"fmt"
	"os"
	"syscall"
)

func isRootProcess() bool { return os.Geteuid() == 0 }

func requirePrivateFile(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular non-symlink file", path)
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("%s mode is %04o, want %04o", path, info.Mode().Perm(), mode)
	}
	return requireRootOwner(path, info)
}

func requirePrivateDir(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a directory or is a symlink", path)
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("%s mode is %04o, want %04o", path, info.Mode().Perm(), mode)
	}
	return requireRootOwner(path, info)
}

func requireRootOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return fmt.Errorf("%s must be owned by root", path)
	}
	return nil
}
