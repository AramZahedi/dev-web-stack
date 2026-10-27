package serviceconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// manifestName is the non-dot manifest name for new installs. Windows
	// never creates a dot-prefixed devstack path.
	manifestName = "config-manifest.json"
	// legacyManifestName is read and migrated once, never written again.
	legacyManifestName = ".config-manifest.json"
)

// Class distinguishes config file ownership and lifecycle.
type Class string

const (
	ClassBase      Class = "base"
	ClassGenerated Class = "generated"
	ClassCustom    Class = "custom"
)

// ManifestEntry records tracking metadata for one config file.
type ManifestEntry struct {
	Path      string    `json:"path"`
	Class     Class     `json:"class"`
	Hash      string    `json:"hash"`
	Detached  bool      `json:"detached,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Manifest tracks all managed config files below root.
type Manifest struct {
	Version int                      `json:"version"`
	Entries map[string]ManifestEntry `json:"entries"`
}

func newManifest() *Manifest {
	return &Manifest{
		Version: 1,
		Entries: make(map[string]ManifestEntry),
	}
}

func loadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newManifest(), nil
		}
		return nil, fmt.Errorf("read config manifest: %w", err)
	}

	m := newManifest()
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("decode config manifest: %w", err)
	}
	if m.Entries == nil {
		m.Entries = make(map[string]ManifestEntry)
	}
	return m, nil
}

func saveManifest(path string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config manifest: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create manifest dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "config-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp manifest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp manifest: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomic save manifest: %w", err)
	}
	return nil
}
