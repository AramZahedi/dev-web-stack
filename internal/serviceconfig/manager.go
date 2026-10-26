package serviceconfig

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
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
	mu           *sync.Mutex
	root         string
	manifestPath string
	now          func() time.Time
}

// managerLocks serializes all Manager instances that point at the same root.
// Docker web sync and the service-config API each create their own Manager,
// but they share one manifest; a per-instance mutex is not enough.
var managerLocks sync.Map

// New returns a config Manager for root.
func New(root string) (*Manager, error) {
	if root == "" {
		return nil, fmt.Errorf("root path cannot be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root %q: %w", root, err)
	}

	lock, _ := managerLocks.LoadOrStore(abs, &sync.Mutex{})
	return &Manager{
		mu:           lock.(*sync.Mutex),
		root:         abs,
		manifestPath: filepath.Join(abs, ".config-manifest.json"),
		now:          time.Now,
	}, nil
}

// cleanRel validates and cleans rel, ensuring it is a relative path that stays inside root
// and does not traverse symlinks pointing outside root.
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

	if cleaned == ".config-manifest.json" || cleaned == "backups" || strings.HasPrefix(cleaned, "backups/") {
		return "", "", fmt.Errorf("config path %q is reserved", rel)
	}

	full := filepath.Join(m.root, filepath.FromSlash(cleaned))
	if !strings.HasPrefix(full, m.root+string(filepath.Separator)) && full != m.root {
		return "", "", fmt.Errorf("config path escapes root boundary: %q", rel)
	}

	realRoot, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return "", "", fmt.Errorf("resolve root symlinks %q: %w", m.root, err)
	}

	// Walk from m.root down to full checking any existing symlinks
	curr := m.root
	parts := strings.Split(filepath.FromSlash(cleaned), string(filepath.Separator))
	for _, part := range parts {
		curr = filepath.Join(curr, part)
		fi, err := os.Lstat(curr)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return "", "", fmt.Errorf("lstat %q: %w", curr, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(curr)
			if err != nil {
				return "", "", fmt.Errorf("eval symlink %q: %w", curr, err)
			}
			if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
				return "", "", fmt.Errorf("symlink %q escapes root to %q", curr, resolved)
			}
		}
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

	manifest, err := loadManifest(m.manifestPath)
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
	if err := m.writeBaselineLocked(cleaned, data); err != nil {
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
		// If user explicitly chose to keep their local edits or merged them
		if tracked && entry.Detached {
			return WriteResult{Changed: false, Conflict: nil}, nil
		}

		currentHash := sha256Hex(content)
		if currentHash == newHash {
			// Content is already up to date
			return WriteResult{Changed: false, Conflict: nil}, nil
		}

		// Base configs are user-owned after seeding and cannot be overwritten by generated config
		if tracked && entry.Class == ClassBase {
			diff := UnifiedDiff(cleaned, content, data)
			return WriteResult{
				Changed: false,
				Conflict: &Conflict{
					Path:     cleaned,
					Current:  content,
					Proposed: data,
					Diff:     diff,
					Reason:   "base config cannot be overwritten by generated config",
				},
			}, nil
		}

		// Conflict check:
		// 1) Untracked pre-existing file on disk (custom user file)
		// 2) Tracked file where current disk content differs from tracked hash
		if !tracked || (entry.Hash != "" && currentHash != entry.Hash) {
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
	if err := m.writeBaselineLocked(cleaned, data); err != nil {
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

// WriteUser writes user-owned content and backs up the current file. It does
// not change the manifest hash, so a later generated write detects the edit
// exactly like a direct filesystem change. The returned backup path is empty
// when the file did not exist before.
func (m *Manager) WriteUser(rel string, data []byte, mode os.FileMode) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return "", err
	}

	backupPath := ""
	if fi, statErr := os.Lstat(full); statErr == nil {
		backupPath, err = m.backupLocked(cleaned, full)
		if err != nil {
			return "", err
		}
		if fi.Mode().Perm() != 0 {
			mode = fi.Mode().Perm()
		}
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect %s: %w", full, statErr)
	}
	if mode == 0 {
		mode = 0o644
	}
	if err := atomicWrite(full, data, mode); err != nil {
		return "", err
	}
	return backupPath, nil
}

// RestoreFile writes bytes without changing manifest metadata or creating a
// backup. It is used to restore a previous version after a failed validation or
// reload.
func (m *Manager) RestoreFile(rel string, data []byte, mode os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, full, err := m.cleanRel(rel)
	if err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	return atomicWrite(full, data, mode)
}

// Entry returns the manifest entry for rel and whether it is tracked.
func (m *Manager) Entry(rel string) (ManifestEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, _, err := m.cleanRel(rel)
	if err != nil {
		return ManifestEntry{}, false, err
	}
	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return ManifestEntry{}, false, err
	}
	entry, ok := manifest.Entries[cleaned]
	return entry, ok, nil
}

// RestoreEntry restores or removes a manifest entry after a rolled-back write.
// A tracked entry is written as-is; an untracked entry is deleted.
func (m *Manager) RestoreEntry(rel string, entry ManifestEntry, tracked bool) error {
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
	if tracked {
		entry.Path = cleaned
		manifest.Entries[cleaned] = entry
	} else {
		delete(manifest.Entries, cleaned)
	}
	return saveManifest(m.manifestPath, manifest)
}

// LatestBaseline returns the last content written by the generated/base flow for
// rel, or an empty string when no baseline was recorded. It lets reset restore
// the managed baseline when no ordinary backup exists.
func (m *Manager) LatestBaseline(rel string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleaned, _, err := m.cleanRel(rel)
	if err != nil {
		return "", err
	}
	path := filepath.Join(m.root, "backups", "baseline", filepath.FromSlash(cleaned))
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect config baseline: %w", err)
	}
	return "", nil
}

func (m *Manager) writeBaselineLocked(rel string, data []byte) error {
	path := filepath.Join(m.root, "backups", "baseline", filepath.FromSlash(rel))
	if err := atomicWrite(path, data, 0o600); err != nil {
		return fmt.Errorf("write config baseline: %w", err)
	}
	return nil
}

// LatestBackup returns the newest backup file for rel, or an empty string when
// no backup exists.
func (m *Manager) LatestBackup(rel string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, _, err := m.cleanRel(rel)
	if err != nil {
		return "", err
	}
	base := filepath.Join(m.root, "backups", "config")
	info, err := os.Stat(base)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("inspect config backups: %w", err)
	}
	if !info.IsDir() {
		return "", nil
	}

	newest := ""
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) < 2 {
			return nil
		}
		candidate := strings.Join(parts[1:], "/")
		if candidate != cleaned {
			return nil
		}
		if newest == "" || path > newest {
			newest = path
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scan config backups: %w", err)
	}
	return newest, nil
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

	manifest, err := loadManifest(m.manifestPath)
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
	if err := m.writeBaselineLocked(cleaned, proposed); err != nil {
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
// It marks the entry detached so subsequent site mutation retries do not clobber it.
func (m *Manager) Merge(rel string, data []byte) error {
	_, err := m.MergeWithBackup(rel, data)
	return err
}

// MergeWithBackup is Merge with the backup path returned for audit and
// rollback reporting.
func (m *Manager) MergeWithBackup(rel string, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return "", err
	}

	manifest, err := loadManifest(m.manifestPath)
	if err != nil {
		return "", err
	}

	backupPath, err := m.backupLocked(cleaned, full)
	if err != nil {
		return "", fmt.Errorf("backup before merge: %w", err)
	}

	mode := os.FileMode(0o644)
	if fi, err := os.Stat(full); err == nil {
		mode = fi.Mode().Perm()
	}

	if err := atomicWrite(full, data, mode); err != nil {
		return "", err
	}

	manifest.Entries[cleaned] = ManifestEntry{
		Path:      cleaned,
		Class:     ClassGenerated,
		Hash:      sha256Hex(data),
		Detached:  true,
		Reason:    "user merged conflict",
		UpdatedAt: m.now().UTC(),
	}

	if err := saveManifest(m.manifestPath, manifest); err != nil {
		return "", err
	}
	return backupPath, nil
}

// Backup creates a timestamped copy under <root>/backups/config/<timestamp>/...
func (m *Manager) Backup(rel string) (string, error) {
	return m.backupUnder(rel, "config")
}

// BackupReset creates the audit copy taken immediately before a reset. It is
// deliberately stored outside backups/config so it can never become the source
// of a later LatestBackup call (which would make repeated resets toggle).
func (m *Manager) BackupReset(rel string) (string, error) {
	return m.backupUnder(rel, "config-reset")
}

func (m *Manager) backupUnder(rel, area string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned, full, err := m.cleanRel(rel)
	if err != nil {
		return "", err
	}
	return m.backupLockedAt(cleaned, full, area)
}

func (m *Manager) backupLocked(rel, full string) (string, error) {
	return m.backupLockedAt(rel, full, "config")
}

func (m *Manager) backupLockedAt(rel, full, area string) (string, error) {
	content, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read %s for backup: %w", full, err)
	}

	ts := m.now().UTC().Format("20060102T150405Z")
	baseDir := filepath.Join(m.root, "backups", area, ts)
	destDir := baseDir
	counter := 1
	for {
		_, err := os.Stat(destDir)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("stat backup directory %s: %w", destDir, err)
		}
		destDir = fmt.Sprintf("%s-%d", baseDir, counter)
		counter++
	}
	dest := filepath.Join(destDir, filepath.FromSlash(rel))

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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
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
