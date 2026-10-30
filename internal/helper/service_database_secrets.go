//go:build !windows

package helper

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

const (
	databaseSecretDirMode  fs.FileMode = 0o700
	databaseSecretFileMode fs.FileMode = 0o600
	maxDatabaseSecretBytes             = 4096
)

var databaseUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// databaseSecretPath validates the service and opaque user ID and returns the
// secret path under the helper service tree. It never follows symlinks.
func (c *DockerServiceController) databaseSecretPath(serviceID, userID string) (string, error) {
	if _, err := c.service(services.ID(serviceID)); err != nil {
		return "", fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	if !databaseUserIDPattern.MatchString(userID) {
		return "", fmt.Errorf("%w: invalid database user ID %q", databases.ErrInvalidRequest, userID)
	}
	dir := filepath.Join(c.root, "services", serviceID, "secrets", "database-users")
	path := filepath.Join(dir, userID)
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: invalid database user ID %q", databases.ErrInvalidRequest, userID)
	}
	if err := rejectSymlinkPath(filepath.Join(c.root, "services"), dir); err != nil {
		return "", fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	return path, nil
}

// rejectSymlinkPath rejects any symlinked component between root and target.
// It starts after root so a symlinked devstack root itself is allowed.
func rejectSymlinkPath(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("path escapes its root")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink", current)
		}
	}
	return nil
}

func (c *DockerServiceController) ensureDatabaseSecretDir(serviceID string) (string, error) {
	path, err := c.databaseSecretPath(serviceID, "placeholder")
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, databaseSecretDirMode); err != nil {
		return "", fmt.Errorf("create database user secret directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%w: database user secret directory is not a directory", databases.ErrInvalidRequest)
	}
	if err := os.Chmod(dir, databaseSecretDirMode); err != nil {
		return "", fmt.Errorf("set database user secret directory mode: %w", err)
	}
	return dir, nil
}

// writeDatabaseSecret atomically stores a database user's password with 0600
// permissions. The directory is 0700 and symlinks are rejected.
func (c *DockerServiceController) writeDatabaseSecret(serviceID, userID, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: database user secret is empty", databases.ErrInvalidRequest)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%w: database user secret contains a control character", databases.ErrInvalidRequest)
	}
	path, err := c.databaseSecretPath(serviceID, userID)
	if err != nil {
		return err
	}
	dir, err := c.ensureDatabaseSecretDir(serviceID)
	if err != nil {
		return err
	}
	tmp, err := stageAtomicFile(path, []byte(value), databaseSecretFileMode)
	if err != nil {
		return fmt.Errorf("stage database user secret: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("publish database user secret: %w", err)
	}
	syncDir(dir)
	return nil
}

// readDatabaseSecret reads a database user's password. Only a regular 0600
// file is accepted; symlinks and group/other-readable files are rejected.
func (c *DockerServiceController) readDatabaseSecret(serviceID, userID string) (string, error) {
	path, err := c.databaseSecretPath(serviceID, userID)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("%w: database user %q has no stored password", databases.ErrPasswordMissing, userID)
	case err != nil:
		return "", fmt.Errorf("inspect database user secret: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: database user secret is not a regular file", databases.ErrPasswordMissing)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: database user secret permissions are too broad", databases.ErrPasswordMissing)
	}
	if info.Size() > maxDatabaseSecretBytes {
		return "", fmt.Errorf("%w: database user secret is too large", databases.ErrPasswordMissing)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read database user secret: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("%w: database user secret is empty", databases.ErrPasswordMissing)
	}
	return value, nil
}

func (c *DockerServiceController) databaseSecretExists(serviceID, userID string) bool {
	path, err := c.databaseSecretPath(serviceID, userID)
	if err != nil {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

// deleteDatabaseSecret removes a stored password. A missing secret is not an
// error; symlinks are never followed.
func (c *DockerServiceController) deleteDatabaseSecret(serviceID, userID string) error {
	path, err := c.databaseSecretPath(serviceID, userID)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect database user secret: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: database user secret is not a regular file", databases.ErrInvalidRequest)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove database user secret: %w", err)
	}
	syncDir(filepath.Dir(path))
	return nil
}
