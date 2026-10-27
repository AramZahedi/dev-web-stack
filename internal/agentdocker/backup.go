package agentdocker

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func (d *Docker) backupServiceData(id services.ID) (string, error) {
	serviceRoot := filepath.Join(d.root, "services", string(id))
	info, err := os.Lstat(serviceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("agentdocker: service state root is not a real directory")
	}
	backupDir := filepath.Join(d.root, "backups", "services", string(id))
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", fmt.Errorf("create service backup directory: %w", err)
	}
	if err := os.Chmod(backupDir, 0o700); err != nil {
		return "", fmt.Errorf("secure service backup directory: %w", err)
	}
	tmp, err := os.CreateTemp(backupDir, "service-*.tar.gz.tmp")
	if err != nil {
		return "", fmt.Errorf("create service backup: %w", err)
	}
	tempName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tempName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("secure temporary backup: %w", err)
	}
	gz := gzip.NewWriter(tmp)
	tarWriter := tar.NewWriter(gz)
	err = filepath.WalkDir(serviceRoot, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == serviceRoot {
			return nil
		}
		if err := os.Chmod(backupDir, 0o700); err != nil {
			return err
		}
		fileInfo, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 || (!fileInfo.IsDir() && !fileInfo.Mode().IsRegular()) {
			return fmt.Errorf("refusing special service backup entry %s", name)
		}
		rel, err := filepath.Rel(serviceRoot, name)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(fileInfo, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if fileInfo.IsDir() {
			return nil
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
	if err != nil {
		_ = tarWriter.Close()
		_ = gz.Close()
		_ = tmp.Close()
		return "", fmt.Errorf("archive service state: %w", err)
	}
	if err := errors.Join(tarWriter.Close(), gz.Close(), tmp.Sync(), tmp.Close()); err != nil {
		return "", fmt.Errorf("finalize service backup: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupPath := filepath.Join(backupDir, "service-"+stamp+".tar.gz")
	if _, err := os.Stat(backupPath); err == nil {
		backupPath = filepath.Join(backupDir, fmt.Sprintf("service-%s-%d.tar.gz", stamp, time.Now().UnixNano()))
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(tempName, backupPath); err != nil {
		return "", fmt.Errorf("publish service backup: %w", err)
	}
	keep = true
	return backupPath, nil
}

func backupRelativeName(name string) string { return strings.TrimPrefix(filepath.ToSlash(name), "./") }
