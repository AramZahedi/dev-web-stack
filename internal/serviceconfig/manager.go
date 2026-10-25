package serviceconfig

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status describes the tracking and drift state of one config file.
type Status struct {
	Path      string
	Class     Class
	Generated bool
	Modified  bool
	Detached  bool
	Hash      string
}

// Conflict describes a collision between user edits and proposed generated content.
type Conflict struct {
	Path     string
	Current  []byte
	Proposed []byte
	Diff     string
	Reason   string
}

// WriteResult is the outcome of attempting to write generated config content.
type WriteResult struct {
	Changed  bool
	Conflict *Conflict
}

// Manager orchestrates config file seeding, generation, diffing, and conflict resolution.
type Manager struct {
	mu           sync.Mutex
	root         string
	manifestPath string
	now          func() time.Time
}

// New returns a config Manager for root.
func New(root string) (*Manager, error) {
	if root == "" {
		return nil, fmt.Errorf("root path cannot be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", root, err)
	}

	return &Manager{
		root:         abs,
		manifestPath: filepath.Join(abs, ".config-manifest.json"),
		now:          time.Now,
	}, nil
}

// cleanRel validates and cleans rel, ensuring it is a relative path that stays inside root.
func (m *Manager) cleanRel(rel string) (string, string, error) {
	if rel == "" {
		return "", "", fmt.Errorf("config path cannot be empty")
	}
	if filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("config path must be relative: %q", rel)
	}

	// Normalize slashes
	cleaned := filepath.Clean(filepath.ToSlash(rel))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "", fmt.Errorf("config path escapes root: %q", rel)
	}

	full := filepath.Join(m.root, filepath.FromSlash(cleaned))
	if !strings.HasPrefix(full, m.root+string(filepath.Separator)) && full != m.root {
		return "", "", fmt.Errorf("config path escapes root boundary: %q", rel)
	}

	return cleaned, full, nil
}

// Status returns the tracking and modification status for rel.
func (m *Manager) Status(rel string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return Status{}, err
	}

	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return Status{}, err
	}

	entry, tracked := manifest.Entries[cleaned]
	content, readErr := os.ReadFile(full)
	exists := readErr == nil

	currentHash := ""
	if exists {
		currentHash = sha256Hex(content)
	}

	if !tracked {
		return Status{
			Path:      cleaned,
			Class:     ClassCustom,
			Generated: false,
			Modified:  exists,
			Detached:  false,
			Hash:      currentHash,
		}, nil
	}

	modified := false
	if exists && entry.Hash != "" && currentHash != entry.Hash {
		modified = true
	}

	return Status{
		Path:      cleaned,
		Class:     entry.Class,
		Generated: entry.Class == ClassGenerated,
		Modified:  modified,
		Detached:  entry.Detached,
		Hash:      currentHash,
	}, nil
}

// SeedBase writes a base config file if it does not already exist.
// Returns (true, nil) if written; (false, nil) if the file already existed.
func (m *Manager) SeedBase(rel string, data []byte, mode os.FileMode) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(full); err == nil {
		// File already exists; do not overwrite
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %s: %w", full, err)
	}

	if err := atomicWrite(full, data, mode); err != nil {
		return false, err
	}

	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return false, err
	}

	manifest.Entries[cleaned] = ManifestEntry{
		Path:      cleaned,
		Class:     ClassBase,
		Hash:      sha256Hex(data),
		Detached:  false,
		Reason:    "seeded base config",
		UpdatedAt: m.now().UTC(),
	}

	if err := saveManifest(m.manifestPath, manifest); err != nil {
		return false, err
	}

	return true, nil
}

// WriteGenerated updates or creates a generated config file.
// If local user modifications are detected, it returns a conflict without touching the file.
func (m *Manager) WriteGenerated(rel string, data []byte, mode os.FileMode, reason string) (WriteResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return WriteResult{}, err
	}

	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return WriteResult{}, err
	}

	entry, tracked := manifest.Entries[cleaned]
	content, readErr := os.ReadFile(full)
	exists := readErr == nil

	newHash := sha256Hex(data)

	if exists {
		// If user explicitly chose to keep their local edits
		if tracked && entry.Detached {
			return WriteResult{Changed: false, Conflict: nil}, nil
		}

		currentHash := sha256Hex(content)
		if currentHash == newHash {
			// Content is already up to date
			return WriteResult{Changed: false, Conflict: nil}, nil
		}

		// Conflict check: if the file was tracked and the disk contents don't match the last tracked hash
		if tracked && entry.Hash != "" && currentHash != entry.Hash {
			diff := UnifiedDiff(cleaned, content, data)
			return WriteResult{
				Changed: false,
				Conflict: &Conflict{
					Path:     cleaned,
					Current:  content,
					Proposed: data,
					Diff:     diff,
					Reason:   reason,
				},
			}, nil
		}
	}

	// Write file atomically
	if err := atomicWrite(full, data, mode); err != nil {
		return WriteResult{}, err
	}

	manifest.Entries[cleaned] = ManifestEntry{
		Path:      cleaned,
		Class:     ClassGenerated,
		Hash:      newHash,
		Detached:  false,
		Reason:    reason,
		UpdatedAt: m.now().UTC(),
	}

	if err := saveManifest(m.manifestPath, manifest); err != nil {
		return WriteResult{}, err
	}

	return WriteResult{Changed: true, Conflict: nil}, nil
}

// KeepMine marks a config file as detached so future generated writes will not overwrite it.
func (m *Manager) KeepMine(rel string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, _, err := m.cleanRel(rel)
	if err != nil {
		return err
	}

	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return err
	}

	entry, ok := manifest.Entries[cleaned]
	if !ok {
		entry = ManifestEntry{
			Path:  cleaned,
			Class: ClassGenerated,
		}
	}
	entry.Detached = true
	entry.UpdatedAt = m.now().UTC()
	manifest.Entries[cleaned] = entry

	return saveManifest(m.manifestPath, manifest)
}

// UseGenerated creates a backup of the current file and applies the proposed generated content.
func (m *Manager) UseGenerated(rel string, proposed []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return "", err
	}

	backupPath, err := m.backupLocked(cleaned, full)
	if err != nil {
		return "", fmt.Errorf("backup before use-generated: %w", err)
	}

	mode := os.FileMode(0o644)
	if fi, err := os.Stat(full); err == nil {
		mode = fi.Mode().Perm()
	}

	if err := atomicWrite(full, proposed, mode); err != nil {
		return "", err
	}

	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return "", err
	}

	manifest.Entries[cleaned] = ManifestEntry{
		Path:      cleaned,
		Class:     ClassGenerated,
		Hash:      sha256Hex(proposed),
		Detached:  false,
		Reason:    "applied generated after conflict",
		UpdatedAt: m.now().UTC(),
	}

	if err := saveManifest(m.manifestPath, manifest); err != nil {
		return "", err
	}

	return backupPath, nil
}

// Merge creates a backup and writes merged content.
func (m *Manager) Merge(rel string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return err
	}

	if _, err := m.backupLocked(cleaned, full); err != nil {
		return fmt.Errorf("backup before merge: %w", err)
	}

	mode := os.FileMode(0o644)
	if fi, err := os.Stat(full); err == nil {
		mode = fi.Mode().Perm()
	}

	if err := atomicWrite(full, data, mode); err != nil {
		return err
	}

	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return err
	}

	manifest.Entries[cleaned] = ManifestEntry{
		Path:      cleaned,
		Class:     ClassGenerated,
		Hash:      sha256Hex(data),
		Detached:  false,
		Reason:    "user merged conflict",
		UpdatedAt: m.now().UTC(),
	}

	return saveManifest(m.manifestPath, manifest)
}

// Backup creates a timestamped copy under <root>/backups/config/<timestamp>/...
func (m *Manager) Backup(rel string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return "", err
	}

	return m.backupLocked(cleaned, full)
}

func (m *Manager) backupLocked(rel, full string) (string, error) {
	content, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read %s for backup: %w", full, err)
	}

	ts := m.now().UTC().Format("20060102T150405Z")
	baseDest := filepath.Join(m.root, "backups", "config", ts, filepath.FromSlash(rel))

	dest := baseDest
	counter := 1
	for {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		dest = fmt.Sprintf("%s.%d", baseDest, counter)
		counter++
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	mode := os.FileMode(0o600)
	if fi, err := os.Stat(full); err == nil {
		mode = fi.Mode().Perm()
	}

	if err := atomicWrite(dest, content, mode); err != nil {
		return "", fmt.Errorf("write backup %s: %w", dest, err)
	}

	return dest, nil
}

func atomicWrite(dest string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("atomic rename to %s: %w", dest, err)
	}

	return nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
