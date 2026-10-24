package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrInvalidExtension is the sentinel wrapped by every extension-selection
// error: unknown or built-in names, extensions unavailable on the requested
// line, and dependency failures. An unsupported PHP version is not an
// extension error.
var ErrInvalidExtension = errors.New("catalog: invalid PHP extension")

// ExtensionType is the php.ini directive that loads an extension.
type ExtensionType string

const (
	ExtensionShared ExtensionType = "extension"
	ExtensionZend   ExtensionType = "zend_extension"
)

// Extension describes one toggleable shared extension.
type Extension struct {
	Name     string        `json:"name"`
	Type     ExtensionType `json:"type"`
	SO       string        `json:"so"`
	Default  bool          `json:"default"`
	Standard bool          `json:"standard"`
	Versions []string      `json:"versions"`
	Requires []string      `json:"requires,omitempty"`
}

// ResolvedExtension is one extension selected for a version and site.
type ResolvedExtension struct {
	Name       string        `json:"name"`
	Type       ExtensionType `json:"type"`
	SO         string        `json:"so"`
	Required   bool          `json:"required"`
	RequiredBy []string      `json:"required_by,omitempty"`
}

// PHPVersionDefaults is the editable baseline for one PHP version line.
type PHPVersionDefaults struct {
	Ini        map[string]string `json:"ini"`
	Extensions map[string]bool   `json:"extensions"`
}

// PHPManifest is the embedded PHP extension and per-version defaults catalog.
// BuiltinByVersion marks modules that are compiled into a specific PHP line
// (for example OPcache in PHP 8.5) and therefore cannot be toggled there.
type PHPManifest struct {
	Builtin          []string                      `json:"builtin"`
	BuiltinByVersion map[string][]string           `json:"builtin_by_version,omitempty"`
	Extensions       []Extension                   `json:"extensions"`
	Versions         map[string]PHPVersionDefaults `json:"versions"`
}

// BuiltinsFor returns the global built-in modules followed by the built-ins
// specific to version. Duplicates are removed and the global order is kept
// before the per-version order.
func (m PHPManifest) BuiltinsFor(version string) []string {
	out := make([]string, 0, len(m.Builtin)+len(m.BuiltinByVersion[version]))
	seen := make(map[string]bool, cap(out))
	for _, name := range m.Builtin {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range m.BuiltinByVersion[version] {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (m PHPManifest) builtinSet(version string) map[string]bool {
	names := m.BuiltinsFor(version)
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// phpLines are the supported PHP major.minor lines.
var phpLines = []string{"8.2", "8.3", "8.4", "8.5"}

var extensionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

//go:embed php.json
var phpJSON []byte

// LoadPHP parses the embedded PHP manifest.
func LoadPHP() (PHPManifest, error) {
	m, err := ParsePHP(phpJSON)
	if err != nil {
		return PHPManifest{}, fmt.Errorf("php catalog: load embedded manifest: %w", err)
	}
	return m, nil
}

// ParsePHP decodes and validates a PHP manifest. A malformed manifest fails at
// startup rather than after a site is created.
func ParsePHP(data []byte) (PHPManifest, error) {
	var m PHPManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return PHPManifest{}, fmt.Errorf("decode: %w", err)
	}
	if err := m.validate(); err != nil {
		return PHPManifest{}, err
	}
	return m, nil
}

// DefaultsFor returns a copy of the baseline ini and extension defaults for a
// version line.
func (m PHPManifest) DefaultsFor(version string) (PHPVersionDefaults, bool) {
	d, ok := m.Versions[version]
	if !ok {
		return PHPVersionDefaults{}, false
	}
	return PHPVersionDefaults{
		Ini:        copyStringMap(d.Ini),
		Extensions: copyBoolMap(d.Extensions),
	}, true
}

// Resolve starts from the embedded per-version defaults and applies explicit
// overrides. Dependencies are included automatically and appear before their
// dependents.
func (m PHPManifest) Resolve(version string, overrides map[string]bool) ([]ResolvedExtension, error) {
	d, ok := m.DefaultsFor(version)
	if !ok {
		return nil, fmt.Errorf("unsupported php version %q", version)
	}
	return m.resolve(version, d.Extensions, overrides)
}

// ResolveWithDefaults starts from the embedded per-version defaults, overlays
// the supplied defaults map (typically an edited php_defaults row), then
// applies explicit site overrides with the same rules as Resolve.
func (m PHPManifest) ResolveWithDefaults(version string, defaults, overrides map[string]bool) ([]ResolvedExtension, error) {
	d, ok := m.DefaultsFor(version)
	if !ok {
		return nil, fmt.Errorf("unsupported php version %q", version)
	}
	base := copyBoolMap(d.Extensions)
	for name, enabled := range defaults {
		base[name] = enabled
	}
	return m.resolve(version, base, overrides)
}

func (m PHPManifest) resolve(version string, base, overrides map[string]bool) ([]ResolvedExtension, error) {
	if _, ok := m.Versions[version]; !ok {
		return nil, fmt.Errorf("unsupported php version %q", version)
	}
	extensions := make(map[string]Extension, len(m.Extensions))
	builtin := m.builtinSet(version)
	for _, ext := range m.Extensions {
		extensions[ext.Name] = ext
	}

	validate := func(name string) (Extension, error) {
		if builtin[name] {
			return Extension{}, fmt.Errorf("%w: built-in module %q cannot be toggled", ErrInvalidExtension, name)
		}
		ext, ok := extensions[name]
		if !ok {
			return Extension{}, fmt.Errorf("%w: unknown php extension %q", ErrInvalidExtension, name)
		}
		if !supportsVersion(ext, version) {
			return Extension{}, fmt.Errorf("%w: extension %q is not available on PHP %s", ErrInvalidExtension, name, version)
		}
		return ext, nil
	}

	enabled := make(map[string]bool, len(base)+len(overrides))
	for name, on := range base {
		if _, err := validate(name); err != nil {
			return nil, err
		}
		enabled[name] = on
	}
	for name, on := range overrides {
		if _, err := validate(name); err != nil {
			return nil, err
		}
		enabled[name] = on
	}

	explicit := make(map[string]bool, len(enabled))
	for name, on := range enabled {
		if on {
			explicit[name] = true
		}
	}

	const (
		visiting = iota + 1
		visited
	)
	state := make(map[string]int, len(extensions))
	order := make([]Extension, 0, len(enabled))
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case visiting:
			return fmt.Errorf("%w: php extension dependency cycle: %s", ErrInvalidExtension, strings.Join(append(path, name), " -> "))
		case visited:
			return nil
		}
		ext, ok := extensions[name]
		if !ok {
			return fmt.Errorf("%w: unknown php extension %q", ErrInvalidExtension, name)
		}
		state[name] = visiting
		for _, dependency := range ext.Requires {
			dep, ok := extensions[dependency]
			if !ok {
				return fmt.Errorf("%w: extension %q requires unknown extension %q", ErrInvalidExtension, name, dependency)
			}
			if !supportsVersion(dep, version) {
				return fmt.Errorf("%w: extension %q requires %q, which is not available on PHP %s", ErrInvalidExtension, name, dependency, version)
			}
			enabled[dependency] = true
			if err := visit(dependency, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = visited
		order = append(order, ext)
		return nil
	}

	for _, ext := range m.Extensions {
		if !enabled[ext.Name] {
			continue
		}
		if err := visit(ext.Name, nil); err != nil {
			return nil, err
		}
	}

	requiredBy := make(map[string][]string, len(order))
	for _, ext := range m.Extensions {
		if !enabled[ext.Name] {
			continue
		}
		for _, dependency := range ext.Requires {
			requiredBy[dependency] = append(requiredBy[dependency], ext.Name)
		}
	}

	resolved := make([]ResolvedExtension, 0, len(order))
	for _, ext := range order {
		resolved = append(resolved, ResolvedExtension{
			Name:       ext.Name,
			Type:       ext.Type,
			SO:         ext.SO,
			Required:   !explicit[ext.Name],
			RequiredBy: requiredBy[ext.Name],
		})
	}
	return resolved, nil
}

func (m PHPManifest) validate() error {
	if len(m.Builtin) == 0 {
		return fmt.Errorf("manifest has no built-in modules")
	}
	builtin := make(map[string]bool, len(m.Builtin))
	for _, name := range m.Builtin {
		if !extensionNamePattern.MatchString(name) {
			return fmt.Errorf("invalid built-in module name %q", name)
		}
		if builtin[name] {
			return fmt.Errorf("duplicate built-in module %q", name)
		}
		builtin[name] = true
	}
	if len(m.Versions) == 0 {
		return fmt.Errorf("manifest has no PHP versions")
	}
	for version := range m.Versions {
		if !isPHPLine(version) {
			return fmt.Errorf("unsupported PHP version %q", version)
		}
	}
	for version, names := range m.BuiltinByVersion {
		if !isPHPLine(version) {
			return fmt.Errorf("builtin_by_version has unsupported PHP version %q", version)
		}
		if _, ok := m.Versions[version]; !ok {
			return fmt.Errorf("builtin_by_version has PHP version %q without defaults", version)
		}
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			if !extensionNamePattern.MatchString(name) {
				return fmt.Errorf("builtin_by_version[%s] has invalid module name %q", version, name)
			}
			if seen[name] {
				return fmt.Errorf("builtin_by_version[%s] has duplicate module %q", version, name)
			}
			seen[name] = true
		}
	}
	if len(m.Extensions) == 0 {
		return fmt.Errorf("manifest has no extensions")
	}

	extensions := make(map[string]Extension, len(m.Extensions))
	for _, ext := range m.Extensions {
		if !extensionNamePattern.MatchString(ext.Name) {
			return fmt.Errorf("invalid extension name %q", ext.Name)
		}
		if builtin[ext.Name] {
			return fmt.Errorf("extension %q is also a built-in module", ext.Name)
		}
		if _, ok := extensions[ext.Name]; ok {
			return fmt.Errorf("duplicate extension %q", ext.Name)
		}
		if ext.Type != ExtensionShared && ext.Type != ExtensionZend {
			return fmt.Errorf("extension %q has invalid type %q", ext.Name, ext.Type)
		}
		if ext.SO != ext.Name+".so" || strings.ContainsAny(ext.SO, `/\`) {
			return fmt.Errorf("extension %q has invalid so %q (want %q)", ext.Name, ext.SO, ext.Name+".so")
		}
		if len(ext.Versions) == 0 {
			return fmt.Errorf("extension %q lists no versions", ext.Name)
		}
		seenVersions := make(map[string]bool, len(ext.Versions))
		for _, version := range ext.Versions {
			if !isPHPLine(version) {
				return fmt.Errorf("extension %q has unsupported version %q", ext.Name, version)
			}
			if _, ok := m.Versions[version]; !ok {
				return fmt.Errorf("extension %q lists version %q without defaults", ext.Name, version)
			}
			if seenVersions[version] {
				return fmt.Errorf("extension %q lists duplicate version %q", ext.Name, version)
			}
			seenVersions[version] = true
		}
		extensions[ext.Name] = ext
	}

	for version, names := range m.BuiltinByVersion {
		for _, name := range names {
			if builtin[name] {
				// Re-listing a global built-in for one line is redundant but
				// harmless: BuiltinsFor deduplicates it.
				continue
			}
			ext, ok := extensions[name]
			if !ok {
				return fmt.Errorf("builtin_by_version[%s] names unknown module %q", version, name)
			}
			if !supportsVersion(ext, version) {
				return fmt.Errorf("builtin_by_version[%s] marks extension %q built-in, but it does not support that version", version, name)
			}
		}
	}
	for _, ext := range m.Extensions {
		for _, dependency := range ext.Requires {
			if builtin[dependency] {
				return fmt.Errorf("extension %q requires built-in module %q", ext.Name, dependency)
			}
			dep, ok := extensions[dependency]
			if !ok {
				return fmt.Errorf("extension %q requires unknown extension %q", ext.Name, dependency)
			}
			for _, version := range ext.Versions {
				if !supportsVersion(dep, version) {
					return fmt.Errorf("extension %q requires %q, which is not available on PHP %s", ext.Name, dependency, version)
				}
			}
		}
	}
	if cycle := dependencyCycle(m.Extensions); cycle != "" {
		return fmt.Errorf("php extension dependency cycle: %s", cycle)
	}

	for version, defaults := range m.Versions {
		versionBuiltins := m.builtinSet(version)
		for name := range defaults.Extensions {
			if versionBuiltins[name] {
				return fmt.Errorf("version %s defaults cannot toggle built-in module %q", version, name)
			}
			ext, ok := extensions[name]
			if !ok {
				return fmt.Errorf("version %s defaults reference unknown extension %q", version, name)
			}
			if !supportsVersion(ext, version) {
				return fmt.Errorf("version %s defaults reference extension %q, which is not available", version, name)
			}
		}
	}
	return nil
}

func dependencyCycle(extensions []Extension) string {
	byName := make(map[string]Extension, len(extensions))
	for _, ext := range extensions {
		byName[ext.Name] = ext
	}
	const (
		visiting = iota + 1
		visited
	)
	state := make(map[string]int, len(extensions))
	var visit func(name string, path []string) string
	visit = func(name string, path []string) string {
		switch state[name] {
		case visiting:
			return strings.Join(append(path, name), " -> ")
		case visited:
			return ""
		}
		state[name] = visiting
		for _, dependency := range byName[name].Requires {
			if _, ok := byName[dependency]; !ok {
				continue
			}
			if cycle := visit(dependency, append(path, name)); cycle != "" {
				return cycle
			}
		}
		state[name] = visited
		return ""
	}
	for _, ext := range extensions {
		if cycle := visit(ext.Name, nil); cycle != "" {
			return cycle
		}
	}
	return ""
}

func supportsVersion(ext Extension, version string) bool {
	for _, candidate := range ext.Versions {
		if candidate == version {
			return true
		}
	}
	return false
}

func isPHPLine(version string) bool {
	for _, line := range phpLines {
		if line == version {
			return true
		}
	}
	return false
}

func copyStringMap(source map[string]string) map[string]string {
	copy := make(map[string]string, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func copyBoolMap(source map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
