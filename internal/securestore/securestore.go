// Package securestore persists the agent token and certificate pin. Windows
// uses DPAPI plus an owner-only file; Linux and WSL use a 0600 file in the
// system secrets directory.
package securestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/platform"
)

func storeDir() (string, error) {
	home := strings.TrimSpace(os.Getenv("DEVSTACK_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("securestore: resolve home: %w", err)
		}
		home = filepath.Join(userHome, ".devstack")
	}
	return filepath.Join(home, "system", "secrets"), nil
}

func validateName(name string) error {
	if err := platform.ValidateDevstackName(name); err != nil {
		return fmt.Errorf("securestore: %w", err)
	}
	return nil
}

// Save writes a secret under name. The file is always 0600; on Windows the
// bytes are DPAPI-protected first.
func Save(name string, secret []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir, err := storeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("securestore: create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("securestore: chmod %s: %w", dir, err)
	}
	sealed, err := seal(secret)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	temp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return fmt.Errorf("securestore: create %s: %w", path, err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("securestore: chmod %s: %w", tempName, err)
	}
	if _, err := temp.Write(sealed); err != nil {
		_ = temp.Close()
		return fmt.Errorf("securestore: write %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("securestore: sync %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("securestore: close %s: %w", path, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("securestore: install %s: %w", path, err)
	}
	return nil
}

// Load reads and unseals a secret. A missing secret returns the underlying
// os.ErrNotExist so callers can provision on first start.
func Load(name string) ([]byte, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	dir, err := storeDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	return open(data)
}

// Delete removes a stored secret. Deleting a missing secret succeeds.
func Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	dir, err := storeDir()
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("securestore: delete %s: %w", name, err)
	}
	return nil
}
