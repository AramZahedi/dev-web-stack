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
	"strconv"
	"strings"
)

// Entry describes one downloadable runtime build. SHA256 and Size pin the exact
// archive that Install accepts; Build identifies the platform-specific archive
// layout (for example linux-x64). Revision is zero for Node builds and at least
// one for PHP builds, which can be republished at the same version.
type Entry struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	Build    string `json:"build"`
	Revision int    `json:"revision,omitempty"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// ID is the parsed form of a runtime build identifier.
type ID struct {
	Kind     string
	Version  string
	Revision int
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
	idPattern       = regexp.MustCompile(`^(node|php)@([0-9]+\.[0-9]+\.[0-9]+)(?:-r([1-9][0-9]*))?$`)
)

// ParseID validates a runtime build identifier. Node builds never carry a
// revision; PHP builds always carry a revision of at least one.
func ParseID(raw string) (ID, error) {
	match := idPattern.FindStringSubmatch(raw)
	if match == nil {
		return ID{}, fmt.Errorf("catalog: invalid runtime id %q", raw)
	}
	kind, version, revision := match[1], match[2], match[3]
	if kind == "node" {
		if revision != "" {
			return ID{}, fmt.Errorf("catalog: node id %q must not carry a revision", raw)
		}
		return ID{Kind: kind, Version: version}, nil
	}
	if revision == "" {
		return ID{}, fmt.Errorf("catalog: php id %q must carry a revision", raw)
	}
	number, err := strconv.Atoi(revision)
	if err != nil || number < 1 {
		return ID{}, fmt.Errorf("catalog: php id %q has an invalid revision", raw)
	}
	return ID{Kind: kind, Version: version, Revision: number}, nil
}

// FormatID is the inverse of ParseID for a valid kind, version, and revision.
func FormatID(kind, version string, revision int) string {
	if kind == "php" && revision > 0 {
		return fmt.Sprintf("%s@%s-r%d", kind, version, revision)
	}
	return kind + "@" + version
}

// LineOf returns the major.minor line of a runtime version, for example
// "8.4.4" -> "8.4".
func LineOf(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return version
}

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

// ForKindVersion returns the newest revision of a runtime kind and exact
// version.
func (c Catalog) ForKindVersion(kind, version string) (Entry, bool) {
	var (
		best  Entry
		found bool
	)
	for _, entry := range c.Entries {
		if entry.Kind != kind || entry.Version != version {
			continue
		}
		if !found || entry.Revision > best.Revision {
			best, found = entry, true
		}
	}
	return best, found
}

// NewestForLine returns the newest version (and, within that version, the
// newest revision) for a runtime kind and major.minor line.
func (c Catalog) NewestForLine(kind, line string) (Entry, bool) {
	var (
		best  Entry
		found bool
	)
	for _, entry := range c.Entries {
		if entry.Kind != kind || LineOf(entry.Version) != line {
			continue
		}
		if !found {
			best, found = entry, true
			continue
		}
		switch compareVersions(entry.Version, best.Version) {
		case 1:
			best = entry
		case 0:
			if entry.Revision > best.Revision {
				best = entry
			}
		}
	}
	return best, found
}

// compareVersions compares dotted numeric versions component by component.
func compareVersions(a, b string) int {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		var av, bv int
		if i < len(ap) {
			av, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bv, _ = strconv.Atoi(bp[i])
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
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
	for i, entry := range document.Entries {
		if err := validateEntry(entry); err != nil {
			return Catalog{}, fmt.Errorf("entry %d (%q): %w", i, entry.ID, err)
		}
		if _, ok := seenIDs[entry.ID]; ok {
			return Catalog{}, fmt.Errorf("duplicate entry %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
	}
	return Catalog{Entries: document.Entries}, nil
}

// validateEntry enforces the invariants the installer relies on: a supported
// kind, a revision encoded in the id, an exact upstream archive name, HTTPS,
// and a lowercase SHA-256.
func validateEntry(entry Entry) error {
	switch entry.Kind {
	case "node", "php":
	default:
		return fmt.Errorf("unsupported kind %q (want node or php)", entry.Kind)
	}
	if !versionPattern.MatchString(entry.Version) {
		return fmt.Errorf("invalid version %q", entry.Version)
	}
	parsed, err := ParseID(entry.ID)
	if err != nil {
		return err
	}
	if parsed.Kind != entry.Kind || parsed.Version != entry.Version {
		return fmt.Errorf("id %q does not match kind@version %q", entry.ID, entry.Kind+"@"+entry.Version)
	}
	if parsed.Revision != entry.Revision {
		return fmt.Errorf("id %q does not match revision %d", entry.ID, entry.Revision)
	}
	if entry.Kind == "php" && entry.Revision < 1 {
		return fmt.Errorf("php entry %q requires a positive revision", entry.ID)
	}
	if entry.Kind == "node" && entry.Revision != 0 {
		return fmt.Errorf("node entry %q must not carry a revision", entry.ID)
	}
	if entry.ID != FormatID(entry.Kind, entry.Version, entry.Revision) {
		return fmt.Errorf("id %q does not match kind@version and revision", entry.ID)
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

	parsedURL, err := url.Parse(entry.URL)
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", entry.URL, err)
	}
	if parsedURL.Scheme != "https" {
		return fmt.Errorf("url %q must use https", entry.URL)
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("url %q has no host", entry.URL)
	}
	wantName := fmt.Sprintf("node-v%s-%s.tar.gz", entry.Version, entry.Build)
	if entry.Kind == "php" {
		wantName = fmt.Sprintf("php-%s-r%d-%s.tar.gz", entry.Version, entry.Revision, entry.Build)
	}
	if path.Base(parsedURL.Path) != wantName {
		return fmt.Errorf("url filename %q does not match version/build (want %q)", path.Base(parsedURL.Path), wantName)
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return fmt.Errorf("url %q must not contain a query or fragment", entry.URL)
	}
	if strings.ContainsRune(entry.URL, 0) {
		return fmt.Errorf("url contains NUL")
	}
	return nil
}
