//go:build !windows

package helper

import (
	"io/fs"
	"os"
	"path/filepath"
)

// writeAtomicFile writes data to path through a same-directory temporary file,
// fsyncs it, and renames it into place. The temporary file is created with
// perm.
func writeAtomicFile(path string, data []byte, perm fs.FileMode) error {
	tmp, err := stageAtomicFile(path, data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// stageAtomicFile writes data to a temporary file next to path and returns the
// temporary path. The caller must rename or remove it.
func stageAtomicFile(path string, data []byte, perm fs.FileMode) (string, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	if err := f.Chmod(perm); err != nil {
		cleanup()
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// syncDir fsyncs a directory so freshly renamed entries survive a crash. It is
// best-effort because some filesystems do not support directory fsync.
func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}
