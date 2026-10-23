// Package catalog holds the checksum-pinned runtime distributions that
// devstack can install. The catalog is embedded in the binary and is never
// fetched from the network, so installing a known runtime cannot be redirected
// by a compromised or unavailable catalog server.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Entry describes one downloadable runtime build. SHA256 and Size pin the exact
// archive that Install accepts; Build identifies the platform-specific archive
// layout (for example linux-x64).
type Entry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Build   string `json:"build"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// Catalog is the complete set of runtime builds shipped with this binary.
type Catalog struct {
	Entries []Entry `json:"entries"`
}

//go:embed catalog.json
var catalogJSON []byte

var (
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	checksumPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Load parses the embedded production catalog.
func Load() (Catalog, error) {
	cat, err := parse(catalogJSON)
	if err != nil {
		return Catalog{}, fmt.Errorf("catalog: load embedded catalog: %w", err)
	}
	return cat, nil
}

// LoadFrom parses a catalog from r. It exists so callers and tests can load a
// pinned catalog without touching the embedded production data.
func LoadFrom(r io.Reader) (Catalog, error) {
	if r == nil {
		return Catalog{}, fmt.Errorf("catalog: nil reader")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return Catalog{}, fmt.Errorf("catalog: read: %w", err)
	}
	cat, err := parse(data)
	if err != nil {
		return Catalog{}, fmt.Errorf("catalog: parse: %w", err)
	}
	return cat, nil
}

// Find returns the entry with the given id.
func (c Catalog) Find(id string) (Entry, bool) {
	for _, entry := range c.Entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Entry{}, false
}

// ForKindVersion returns the entry for a runtime kind and exact version.
func (c Catalog) ForKindVersion(kind, version string) (Entry, bool) {
	for _, entry := range c.Entries {
		if entry.Kind == kind && entry.Version == version {
			return entry, true
		}
	}
	return Entry{}, false
}

// parse decodes and validates a catalog document. Every entry is checked so a
// malformed or tampered catalog fails at startup rather than after a download.
func parse(data []byte) (Catalog, error) {
	var document struct {
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return Catalog{}, fmt.Errorf("decode catalog: %w", err)
	}
	if len(document.Entries) == 0 {
		return Catalog{}, fmt.Errorf("catalog has no entries")
	}

	seenIDs := make(map[string]struct{}, len(document.Entries))
	seenVersions := make(map[string]struct{}, len(document.Entries))
	for i, entry := range document.Entries {
		if err := validateEntry(entry); err != nil {
			return Catalog{}, fmt.Errorf("entry %d (%q): %w", i, entry.ID, err)
		}
		if _, ok := seenIDs[entry.ID]; ok {
			return Catalog{}, fmt.Errorf("duplicate entry %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}

		key := entry.Kind + "@" + entry.Version
		if _, ok := seenVersions[key]; ok {
			return Catalog{}, fmt.Errorf("duplicate entry %q", key)
		}
		seenVersions[key] = struct{}{}
	}
	return Catalog{Entries: document.Entries}, nil
}

// validateEntry enforces the invariants the installer relies on: one supported
// kind, an exact upstream archive name, HTTPS, and a lowercase SHA-256.
func validateEntry(entry Entry) error {
	if entry.Kind != "node" {
		return fmt.Errorf("unsupported kind %q (want node)", entry.Kind)
	}
	if !versionPattern.MatchString(entry.Version) {
		return fmt.Errorf("invalid version %q", entry.Version)
	}
	if entry.ID != entry.Kind+"@"+entry.Version {
		return fmt.Errorf("id %q does not match kind@version %q", entry.ID, entry.Kind+"@"+entry.Version)
	}
	if entry.Build != "linux-x64" {
		return fmt.Errorf("unsupported build %q (want linux-x64)", entry.Build)
	}
	if !checksumPattern.MatchString(entry.SHA256) {
		return fmt.Errorf("invalid sha256 %q (want 64 lowercase hex characters)", entry.SHA256)
	}
	if entry.Size <= 0 {
		return fmt.Errorf("invalid size %d", entry.Size)
	}

	parsed, err := url.Parse(entry.URL)
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", entry.URL, err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("url %q must use https", entry.URL)
	}
	if parsed.Host == "" {
		return fmt.Errorf("url %q has no host", entry.URL)
	}
	wantName := fmt.Sprintf("node-v%s-%s.tar.gz", entry.Version, entry.Build)
	if path.Base(parsed.Path) != wantName {
		return fmt.Errorf("url filename %q does not match version/build (want %q)", path.Base(parsed.Path), wantName)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("url %q must not contain a query or fragment", entry.URL)
	}
	if strings.ContainsRune(entry.URL, 0) {
		return fmt.Errorf("url contains NUL")
	}
	return nil
}
