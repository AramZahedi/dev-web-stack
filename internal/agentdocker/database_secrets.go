package agentdocker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
)

var databaseUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// databaseSecretDir returns <root>/services/<service>/secrets/database-users.
// With create set, the directory and its parents are created and chmodded to
// 0700. Without create, every component must already be a real directory and
// never a symlink.
func (d *Docker) databaseSecretDir(serviceID string, create bool) (string, error) {
	service, err := d.service(serviceID)
	if err != nil {
		return "", err
	}
	if _, ok := databaseEngineID(service.ID); !ok {
		return "", fmt.Errorf("agentdocker: %s is not a database service", service.ID)
	}
	secrets := filepath.Join(d.root, "services", string(service.ID), "secrets")
	dir := filepath.Join(secrets, "database-users")
	if create {
		if err := ensureRealDirectory(secrets, 0o700); err != nil {
			return "", fmt.Errorf("agentdocker: secure %s secrets: %w", service.ID, err)
		}
		if err := ensureRealDirectory(dir, 0o700); err != nil {
			return "", fmt.Errorf("agentdocker: secure %s database secrets: %w", service.ID, err)
		}
		return dir, nil
	}
	for _, path := range []string{secrets, dir} {
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("agentdocker: %s is not a real directory", path)
		}
	}
	return dir, nil
}

// databaseSecretPath validates a user ID and returns the private secret path.
// It never creates directories.
func (d *Docker) databaseSecretPath(serviceID, userID string) (string, error) {
	if !databaseUserIDPattern.MatchString(userID) {
		return "", fmt.Errorf("agentdocker: invalid database user ID %q", userID)
	}
	dir, err := d.databaseSecretDir(serviceID, false)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, userID), nil
}

func (d *Docker) writeDatabaseSecret(serviceID, userID, value string) error {
	if !databaseUserIDPattern.MatchString(userID) {
		return fmt.Errorf("agentdocker: invalid database user ID %q", userID)
	}
	if strings.ContainsAny(value, "\r\n") || value == "" {
		return errors.New("agentdocker: database secret contains control characters")
	}
	dir, err := d.databaseSecretDir(serviceID, true)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, userID)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("agentdocker: refusing unsafe database secret %s", userID)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agentdocker: inspect database secret: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".database-secret-*.tmp")
	if err != nil {
		return fmt.Errorf("agentdocker: create database secret: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: secure database secret: %w", err)
	}
	if _, err := tmp.WriteString(value + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: write database secret: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agentdocker: sync database secret: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agentdocker: close database secret: %w", err)
	}
	// Windows cannot rename over an existing file. Production runs on Linux;
	// removing first keeps the Windows test host working without weakening the
	// final 0600 file.
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("agentdocker: publish database secret: %w", err)
	}
	return nil
}

func (d *Docker) readDatabaseSecret(serviceID, userID string) (string, error) {
	path, err := d.databaseSecretPath(serviceID, userID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", databases.ErrPasswordMissing
		}
		return "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", databases.ErrPasswordMissing
	}
	if err != nil {
		return "", fmt.Errorf("agentdocker: inspect database secret: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("agentdocker: database secret %s is not a regular file", userID)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("agentdocker: database secret %s has mode %04o", userID, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("agentdocker: read database secret: %w", err)
	}
	value := strings.TrimRight(string(data), "\r\n")
	if value == "" {
		return "", databases.ErrPasswordMissing
	}
	return value, nil
}

func (d *Docker) deleteDatabaseSecret(serviceID, userID string) error {
	path, err := d.databaseSecretPath(serviceID, userID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agentdocker: delete database secret: %w", err)
	}
	return nil
}

func (d *Docker) databaseSecretExists(serviceID, userID string) bool {
	_, err := d.readDatabaseSecret(serviceID, userID)
	return err == nil
}
