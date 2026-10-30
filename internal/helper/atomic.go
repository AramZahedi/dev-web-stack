//go:build !windows

package helper

import (
	"errors"
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

// writeFileInPlace truncates and rewrites an existing file without replacing
// its inode. This is required for files bind-mounted into a container as a
// single file: an atomic rename would leave the container on the old inode.
func writeFileInPlace(path string, data []byte, defaultPerm fs.FileMode) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return writeAtomicFile(path, data, defaultPerm)
	}
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	cleanup := func() { _ = file.Close() }
	if _, err := file.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
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
