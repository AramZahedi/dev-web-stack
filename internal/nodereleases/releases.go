// Package nodereleases discovers official Node.js releases. It strictly parses
// the release index, caches it privately, resolves version selections, and
// preflights installs against the official per-release checksum file.
package nodereleases

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ErrReleaseNotFound reports a selection that is malformed or does not match
// any eligible release. Callers can surface it as an actionable "version not
// available" message without parsing strings.
var ErrReleaseNotFound = errors.New("nodereleases: release not found")

// Release is one stable official Node.js release with a Linux x64 archive.
type Release struct {
	Version  string `json:"version"`
	Date     string `json:"date"`
	LTS      string `json:"lts"`
	Security bool   `json:"security"`
	NPM      string `json:"npm"`
	Modules  string `json:"modules"`
	File     string `json:"file"`
}

// Major groups the releases of one Node.js major version. Latest is the newest
// release in the group, which is also the first element of Releases.
type Major struct {
	Major    int       `json:"major"`
	Latest   Release   `json:"latest"`
	Releases []Release `json:"releases"`
}

// indexedVersionPattern matches a semver core with an optional prerelease or
// build-metadata suffix. Only entries without a suffix are eligible.
var indexedVersionPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`,
)

// indexEntry is the subset of a nodejs.org/dist/index.json entry that
// discovery uses. Unknown fields such as v8, uv, zlib, and openssl are ignored.
type indexEntry struct {
	Version  string   `json:"version"`
	Date     string   `json:"date"`
	Files    []string `json:"files"`
	NPM      string   `json:"npm"`
	Modules  string   `json:"modules"`
	LTS      indexLTS `json:"lts"`
	Security bool     `json:"security"`
}

// indexLTS decodes the official lts field, which is either false or an LTS
// codename such as "Jod".
type indexLTS string

func (l *indexLTS) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch value := value.(type) {
	case nil:
		*l = ""
	case bool:
		if value {
			return fmt.Errorf("lts must be a string or false")
		}
		*l = ""
	case string:
		*l = indexLTS(value)
	default:
		return fmt.Errorf("lts must be a string or false")
	}
	return nil
}

// ParseIndex decodes the official Node.js release index and returns the stable
// Linux x64 releases in index order. Prerelease, build-metadata, and
// non-linux-x64 entries are skipped. Malformed JSON, non-array documents,
// invalid versions, invalid lts values, and duplicate eligible versions are
// rejected.
func ParseIndex(data []byte) ([]Release, error) {
	var entries []indexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("nodereleases: decode index: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("nodereleases: decode index: expected a JSON array")
	}

	releases := make([]Release, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		version, stable, err := parseIndexedVersion(entry.Version)
		if err != nil {
			return nil, fmt.Errorf("nodereleases: entry %d (%q): %w", i, entry.Version, err)
		}
		if !stable {
			continue
		}
		if !slices.Contains(entry.Files, "linux-x64") {
			continue
		}
		if _, ok := seen[version]; ok {
			return nil, fmt.Errorf("nodereleases: duplicate release version %q", version)
		}
		seen[version] = struct{}{}
		releases = append(releases, Release{
			Version:  version,
			Date:     entry.Date,
			LTS:      string(entry.LTS),
			Security: entry.Security,
			NPM:      entry.NPM,
			Modules:  entry.Modules,
			File:     "node-v" + version + "-linux-x64.tar.gz",
		})
	}
	return releases, nil
}

// parseIndexedVersion strips the leading v and reports whether the version is
// stable. Prerelease and build-metadata versions are valid but ineligible;
// malformed versions are errors.
func parseIndexedVersion(raw string) (string, bool, error) {
	if !strings.HasPrefix(raw, "v") {
		return "", false, fmt.Errorf("version must start with v")
	}
	version := strings.TrimPrefix(raw, "v")
	if !indexedVersionPattern.MatchString(version) {
		return "", false, fmt.Errorf("version is not valid stable semver")
	}
	return version, !strings.ContainsAny(version, "-+"), nil
}

// GroupByMajor groups releases by major version. Majors are newest first and
// each major's releases are newest patch first. Releases with invalid versions
// are ignored.
func GroupByMajor(releases []Release) []Major {
	grouped := make(map[int][]Release)
	for _, release := range releases {
		major, _, _, ok := splitVersion(release.Version)
		if !ok {
			continue
		}
		grouped[major] = append(grouped[major], release)
	}

	majors := make([]Major, 0, len(grouped))
	for major, group := range grouped {
		sort.Slice(group, func(i, j int) bool {
			return compareVersions(group[i].Version, group[j].Version) > 0
		})
		majors = append(majors, Major{
			Major:    major,
			Latest:   group[0],
			Releases: group,
		})
	}
	sort.Slice(majors, func(i, j int) bool {
		return majors[i].Major > majors[j].Major
	})
	return majors
}

// Select resolves a bare major ("22"), major.minor ("22.11"), or exact
// ("22.11.0") selection to the newest matching release. Invalid or unknown
// selections return ErrReleaseNotFound.
func Select(releases []Release, selector string) (Release, error) {
	major, minor, patch, parts, ok := parseSelector(selector)
	if !ok {
		return Release{}, fmt.Errorf("nodereleases: invalid selection %q: %w", selector, ErrReleaseNotFound)
	}

	var (
		best  Release
		found bool
	)
	for _, release := range releases {
		releaseMajor, releaseMinor, releasePatch, ok := splitVersion(release.Version)
		if !ok || releaseMajor != major {
			continue
		}
		if parts >= 2 && releaseMinor != minor {
			continue
		}
		if parts == 3 && releasePatch != patch {
			continue
		}
		if !found || compareVersions(release.Version, best.Version) > 0 {
			best, found = release, true
		}
	}
	if !found {
		return Release{}, fmt.Errorf("nodereleases: no release matches %q: %w", selector, ErrReleaseNotFound)
	}
	return best, nil
}

// parseSelector parses a bare numeric selection of one to three components.
// Leading zeros and non-numeric components are rejected.
func parseSelector(selector string) (major, minor, patch, parts int, ok bool) {
	fields := strings.Split(selector, ".")
	if len(fields) < 1 || len(fields) > 3 {
		return 0, 0, 0, 0, false
	}
	numbers := make([]int, len(fields))
	for i, field := range fields {
		number, valid := parseDigits(field)
		if !valid {
			return 0, 0, 0, 0, false
		}
		numbers[i] = number
	}
	switch len(numbers) {
	case 1:
		return numbers[0], 0, 0, 1, true
	case 2:
		return numbers[0], numbers[1], 0, 2, true
	default:
		return numbers[0], numbers[1], numbers[2], 3, true
	}
}

// splitVersion parses a bare stable semver core.
func splitVersion(version string) (major, minor, patch int, ok bool) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	if major, ok = parseDigits(parts[0]); !ok {
		return 0, 0, 0, false
	}
	if minor, ok = parseDigits(parts[1]); !ok {
		return 0, 0, 0, false
	}
	if patch, ok = parseDigits(parts[2]); !ok {
		return 0, 0, 0, false
	}
	return major, minor, patch, true
}

// parseDigits parses a non-negative decimal component without signs, spaces,
// leading zeros, or overflow.
func parseDigits(value string) (int, bool) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return number, true
}

// compareVersions compares two valid stable semver cores component by
// component.
func compareVersions(a, b string) int {
	aMajor, aMinor, aPatch, aOK := splitVersion(a)
	bMajor, bMinor, bPatch, bOK := splitVersion(b)
	if !aOK || !bOK {
		switch {
		case !aOK && !bOK:
			return 0
		case !aOK:
			return -1
		default:
			return 1
		}
	}
	switch {
	case aMajor != bMajor:
		return compareInts(aMajor, bMajor)
	case aMinor != bMinor:
		return compareInts(aMinor, bMinor)
	default:
		return compareInts(aPatch, bPatch)
	}
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
