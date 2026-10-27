//go:build windows

package agent

import "os"

func isRootProcess() bool { return false }

// Agent credentials are used only in the WSL service, where Unix ownership
// and mode checks are enforced. Windows host files are protected by the
// installer ACL contract rather than POSIX mode bits.
func requirePrivateFile(path string, _ os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return os.ErrPermission
	}
	return nil
}

func requirePrivateDir(path string, _ os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return os.ErrPermission
	}
	return nil
}
