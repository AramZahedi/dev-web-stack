//go:build !windows

package helper

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// backupService creates and verifies a tar.gz containing the catalog-owned
// service and secret trees. It returns an empty path when neither tree exists.
func (c *DockerServiceController) backupService(id services.ID) (string, error) {
	serviceDir := filepath.Join(c.root, "services", string(id))
	secretDir := filepath.Join(c.root, "secrets", string(id))

	var paths []string
	for _, path := range []string{serviceDir, secretDir} {
		_, err := os.Lstat(path)
		switch {
		case err == nil:
			paths = append(paths, path)
		case errors.Is(err, os.ErrNotExist):
		default:
			return "", fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	if len(paths) == 0 {
		return "", nil
	}

	backupDir := filepath.Join(c.root, "backups", "services", string(id))
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	now := c.Now
	if now == nil {
		now = time.Now
	}
	timestamp := now().UTC().Format("20060102T150405.000000000Z")
	dest := filepath.Join(backupDir, timestamp+".tar.gz")
	for suffix := 2; ; suffix++ {
		_, statErr := os.Lstat(dest)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", fmt.Errorf("check backup destination: %w", statErr)
		}
		dest = filepath.Join(backupDir, fmt.Sprintf("%s-%d.tar.gz", timestamp, suffix))
	}

	tmp, err := os.CreateTemp(backupDir, "backup-*.tar.gz.tmp")
	if err != nil {
		return "", fmt.Errorf("create backup archive: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if err := writeBackupArchive(tmp, c.root, paths); err != nil {
		cleanup()
		return "", fmt.Errorf("write backup archive: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("sync backup archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close backup archive: %w", err)
	}

	if err := verifyBackupArchive(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("publish backup archive: %w", err)
	}
	syncDir(backupDir)
	return dest, nil
}

func writeBackupArchive(w io.Writer, root string, paths []string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	for _, base := range paths {
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}

			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}

			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(rel)
			if info.IsDir() {
				header.Name += "/"
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			if !info.Mode().IsRegular() {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		})
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return err
		}
	}

	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

func verifyBackupArchive(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat backup archive: %w", err)
	}
	if info.Size() == 0 {
		return errors.New("backup archive is empty")
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open backup archive: %w", err)
	}
	defer func() { _ = file.Close() }()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("verify backup gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	entries := 0
	tr := tar.NewReader(gz)
	for {
		_, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("verify backup tar: %w", err)
		}
		entries++
	}
	if entries == 0 {
		return errors.New("backup archive contains no entries")
	}
	return nil
}
