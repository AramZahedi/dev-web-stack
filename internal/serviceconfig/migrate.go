package serviceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrateManifest copies a legacy manifest from oldRoot into newRoot, keeping
// only entries whose paths start with prefix and stripping that prefix. It is
// used when a manager moves from a service root to its config root. The
// migration is idempotent: an existing destination manifest always wins and
// the source is left untouched.
func MigrateManifest(oldRoot, newRoot, prefix string) error {
	if oldRoot == "" || newRoot == "" {
		return errors.New("serviceconfig: migration roots cannot be empty")
	}
	oldPath := filepath.Join(oldRoot, ".config-manifest.json")
	newPath := filepath.Join(newRoot, ".config-manifest.json")
	if _, err := os.Stat(newPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat destination manifest %s: %w", newPath, err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat source manifest %s: %w", oldPath, err)
	}

	legacy, err := loadManifest(oldPath)
	if err != nil {
		return fmt.Errorf("load legacy manifest: %w", err)
	}
	migrated := newManifest()
	for key, entry := range legacy.Entries {
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		newKey := strings.TrimPrefix(key, prefix)
		if newKey == "" || filepath.IsAbs(newKey) {
			continue
		}
		entry.Path = newKey
		migrated.Entries[newKey] = entry
	}
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		return fmt.Errorf("create destination config root: %w", err)
	}
	if err := saveManifest(newPath, migrated); err != nil {
		return fmt.Errorf("write migrated manifest: %w", err)
	}
	return nil
}
