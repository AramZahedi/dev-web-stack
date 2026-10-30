package nodereleases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// indexCacheDocument is the private on-disk shape of the release index cache.
// Only releases parsed from the official index are stored; embedded catalog
// entries are merged at read time so binary updates take effect immediately.
type indexCacheDocument struct {
	CachedAt time.Time `json:"cached_at"`
	Releases []Release `json:"releases"`
}

// sizeCacheDocument is the private on-disk shape of a cached archive size.
type sizeCacheDocument struct {
	Size int64 `json:"size"`
}

// cacheLayout resolves the private cache paths below home.
type cacheLayout struct {
	index   string
	shasums string
	sizes   string
}

func newCacheLayout(home string) cacheLayout {
	root := filepath.Join(home, "cache", "node")
	return cacheLayout{
		index:   filepath.Join(root, "index.json"),
		shasums: filepath.Join(root, "shasums"),
		sizes:   filepath.Join(root, "sizes"),
	}
}

// readIndex loads a cached index. Missing, unreadable, malformed, or
// inconsistent documents are treated as absent so discovery can refetch them.
func (l cacheLayout) readIndex() (indexCacheDocument, bool) {
	data, err := os.ReadFile(l.index)
	if err != nil {
		return indexCacheDocument{}, false
	}
	var document indexCacheDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return indexCacheDocument{}, false
	}
	if document.CachedAt.IsZero() || len(document.Releases) == 0 {
		return indexCacheDocument{}, false
	}
	if !validCachedReleases(document.Releases) {
		return indexCacheDocument{}, false
	}
	return document, true
}

// writeIndex atomically stores the official releases and their fetch time.
func (l cacheLayout) writeIndex(cachedAt time.Time, releases []Release) error {
	document := indexCacheDocument{CachedAt: cachedAt, Releases: releases}
	data, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("nodereleases: encode index cache: %w", err)
	}
	if err := writeCacheFile(l.index, append(data, '\n')); err != nil {
		return fmt.Errorf("nodereleases: cache index: %w", err)
	}
	return nil
}

// readChecksum returns a cached checksum only when the cached document still
// contains exactly one valid line for the requested archive.
func (l cacheLayout) readChecksum(version, filename string) (string, bool) {
	path, err := l.checksumPath(version)
	if err != nil {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	checksum, err := checksumForFile(data, filename)
	if err != nil {
		return "", false
	}
	return checksum, true
}

// writeChecksums atomically stores the official SHASUMS256.txt document.
func (l cacheLayout) writeChecksums(version string, data []byte) error {
	path, err := l.checksumPath(version)
	if err != nil {
		return err
	}
	if err := writeCacheFile(path, data); err != nil {
		return fmt.Errorf("nodereleases: cache checksums for %s: %w", version, err)
	}
	return nil
}

// readSize returns a cached archive size only when it is positive and within
// the download limit.
func (l cacheLayout) readSize(version string) (int64, bool) {
	path, err := l.sizePath(version)
	if err != nil {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var document sizeCacheDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return 0, false
	}
	if document.Size <= 0 || document.Size > MaxArchiveSize {
		return 0, false
	}
	return document.Size, true
}

// writeSize atomically stores a verified archive size.
func (l cacheLayout) writeSize(version string, size int64) error {
	path, err := l.sizePath(version)
	if err != nil {
		return err
	}
	data, err := json.Marshal(sizeCacheDocument{Size: size})
	if err != nil {
		return fmt.Errorf("nodereleases: encode size cache: %w", err)
	}
	if err := writeCacheFile(path, append(data, '\n')); err != nil {
		return fmt.Errorf("nodereleases: cache size for %s: %w", version, err)
	}
	return nil
}

func (l cacheLayout) checksumPath(version string) (string, error) {
	if _, _, _, ok := splitVersion(version); !ok {
		return "", fmt.Errorf("nodereleases: invalid version %q", version)
	}
	return filepath.Join(l.shasums, version+".txt"), nil
}

func (l cacheLayout) sizePath(version string) (string, error) {
	if _, _, _, ok := splitVersion(version); !ok {
		return "", fmt.Errorf("nodereleases: invalid version %q", version)
	}
	return filepath.Join(l.sizes, version+".json"), nil
}

// invalidateMetadata drops lazily fetched checksum and size documents so the
// next Preflight revalidates them against a refreshed index.
func (l cacheLayout) invalidateMetadata() error {
	for _, dir := range []string{l.shasums, l.sizes} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	return nil
}

// validCachedReleases checks the invariants discovery needs from a cached
// entry without trusting the document's origin.
func validCachedReleases(releases []Release) bool {
	seen := make(map[string]struct{}, len(releases))
	for _, release := range releases {
		if _, _, _, ok := splitVersion(release.Version); !ok {
			return false
		}
		if release.File != archiveName(release.Version) {
			return false
		}
		if _, duplicate := seen[release.Version]; duplicate {
			return false
		}
		seen[release.Version] = struct{}{}
	}
	return true
}

// writeCacheFile replaces path atomically inside a private directory. The
// temporary file is created in the destination directory so the rename stays
// on one filesystem.
func writeCacheFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	// MkdirAll leaves an existing directory's mode alone; tighten it so cache
	// contents remain private even after an older release created it.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set %s permissions: %w", dir, err)
	}

	temporary, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()

	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set %s permissions: %w", name, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	return nil
}

var checksumLinePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// checksumForFile extracts the single authoritative SHA-256 for filename.
// Every non-empty line must be a well-formed "<sha256>  <name>" record and no
// filename may occur twice; the target must occur exactly once.
func checksumForFile(data []byte, filename string) (string, error) {
	if filename == "" || strings.ContainsAny(filename, "\r\n") {
		return "", fmt.Errorf("nodereleases: invalid checksum filename %q", filename)
	}

	var (
		checksum string
		matches  int
	)
	seen := make(map[string]struct{})
	for index, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return "", fmt.Errorf("nodereleases: checksum line %d is malformed", index+1)
		}
		hash, name := fields[0], fields[1]
		if !checksumLinePattern.MatchString(hash) {
			return "", fmt.Errorf(
				"nodereleases: checksum line %d has an invalid SHA-256 (want 64 lowercase hex characters)",
				index+1,
			)
		}
		if _, duplicate := seen[name]; duplicate && name != filename {
			return "", fmt.Errorf("nodereleases: checksum file lists %q more than once", name)
		}
		seen[name] = struct{}{}
		if name == filename {
			matches++
			checksum = hash
		}
	}
	if matches != 1 {
		return "", fmt.Errorf(
			"nodereleases: checksum file must contain exactly one line for %q, found %d",
			filename, matches,
		)
	}
	return checksum, nil
}
