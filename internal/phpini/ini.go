// Package phpini composes, preserves, and validates the per-site php.ini that
// devstack hands to a PHP build. Generated files carry stable markers so the
// site-owned raw section can be edited and round-tripped without losing
// comments or unknown directives, while the managed extension block stays
// under devstack's control.
package phpini

import (
	"fmt"
	"sort"
	"strings"
)

// Section markers around generated php.ini regions. They are comments to PHP
// and must never appear in site-supplied overrides or raw text.
const (
	BeginDefaultsMarker   = "; BEGIN devstack version defaults"
	EndDefaultsMarker     = "; END devstack version defaults"
	BeginSiteMarker       = "; BEGIN devstack site overrides"
	EndSiteMarker         = "; END devstack site overrides"
	BeginExtensionsMarker = "; BEGIN devstack extensions (managed)"
	EndExtensionsMarker   = "; END devstack extensions (managed)"
)

// Extension is one resolved shared extension. Type is either "extension" or
// "zend_extension"; SO is the shared-object filename or path.
type Extension struct {
	Name string
	Type string
	SO   string
}

// Config describes the inputs to a generated site php.ini. Base is the build's
// php.ini, VersionDefaults are the editable per-version baselines, Overrides
// are site key/value rows, Raw is the site-owned raw section, and Extensions is
// the resolved extension list in dependency order.
type Config struct {
	Base            []byte
	VersionDefaults map[string]string
	Overrides       map[string]string
	Raw             string
	Extensions      []Extension
	ExtensionDir    string
	CAFile          string
}

// Compose renders a complete php.ini in the mandated order: build base,
// version defaults, site overrides plus the verbatim raw section, and finally
// the managed extension block. Every site-supplied input is validated before
// any bytes are returned.
func Compose(cfg Config) ([]byte, error) {
	if err := ValidateOverrides(cfg.VersionDefaults); err != nil {
		return nil, err
	}
	if err := ValidateOverrides(cfg.Overrides); err != nil {
		return nil, err
	}
	if err := ValidateRaw(cfg.Raw); err != nil {
		return nil, err
	}
	extensions, err := validateExtensions(cfg.Extensions)
	if err != nil {
		return nil, err
	}
	if err := validateManagedPath("extension directory", cfg.ExtensionDir); err != nil {
		return nil, err
	}
	if err := validateManagedPath("CA file", cfg.CAFile); err != nil {
		return nil, err
	}

	// Add the CA aliases unless the effective version defaults or site
	// overrides already set them. Base and raw are deliberately excluded: a
	// build base must not suppress the devstack CA bundle, and raw is written
	// after the defaults so it can still override naturally.
	set := make(map[string]bool, len(cfg.VersionDefaults)+len(cfg.Overrides))
	for key := range cfg.VersionDefaults {
		set[key] = true
	}
	for key := range cfg.Overrides {
		set[key] = true
	}

	var b strings.Builder

	if base := normalizeBase(cfg.Base); len(base) > 0 {
		b.Write(base)
	}

	// PHP sections such as [PATH=...] and [HOST=...] scope every following
	// directive. Reset to the global [PHP] section so a section at the end of
	// the build base cannot swallow the generated defaults and site overrides.
	b.WriteString("[PHP]\n")

	defaults := make(map[string]string, len(cfg.VersionDefaults)+2)
	for key, value := range cfg.VersionDefaults {
		defaults[key] = value
	}
	if cfg.CAFile != "" {
		// curl and openssl read these independently and each is skipped
		// independently when a lower layer already sets it.
		for _, key := range []string{"curl.cainfo", "openssl.cafile"} {
			if !set[key] {
				defaults[key] = quoteIni(cfg.CAFile)
			}
		}
	}
	b.WriteString(BeginDefaultsMarker + "\n")
	writeSorted(&b, defaults)
	b.WriteString(EndDefaultsMarker + "\n")

	b.WriteString(BeginSiteMarker + "\n")
	writeSorted(&b, cfg.Overrides)
	if cfg.Raw != "" {
		b.WriteString(cfg.Raw)
		if !strings.HasSuffix(cfg.Raw, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString(EndSiteMarker + "\n")

	// Raw may end inside [PATH=...] or [HOST=...]; scope it back to [PHP] so
	// the managed extension block is always global. Without this reset PHP
	// silently treats extension_dir and every extension line as path-scoped.
	b.WriteString("[PHP]\n")
	b.WriteString(BeginExtensionsMarker + "\n")
	if cfg.ExtensionDir != "" {
		fmt.Fprintf(&b, "extension_dir = %s\n", quoteIni(cfg.ExtensionDir))
	}
	for _, extension := range extensions {
		fmt.Fprintf(&b, "%s=%s\n", extension.Type, extension.SO)
	}
	b.WriteString(EndExtensionsMarker + "\n")

	return []byte(b.String()), nil
}

// ExtractSiteSection returns the text between the site markers of a generated
// php.ini. The returned text is suitable for feeding back into Config.Raw.
func ExtractSiteSection(generated []byte) (string, error) {
	lines := strings.SplitAfter(string(generated), "\n")
	begin, end := -1, -1
	for index, line := range lines {
		switch strings.TrimRight(line, "\r\n") {
		case BeginSiteMarker:
			if begin >= 0 {
				return "", fmt.Errorf("%w: duplicate %q marker", ErrInvalidConfig, BeginSiteMarker)
			}
			begin = index
		case EndSiteMarker:
			if begin < 0 {
				return "", fmt.Errorf("%w: %q before %q", ErrInvalidConfig, EndSiteMarker, BeginSiteMarker)
			}
			if end < 0 {
				end = index
			}
		}
	}
	if begin < 0 || end < 0 {
		return "", fmt.Errorf("%w: site section markers not found", ErrInvalidConfig)
	}
	extracted := strings.Join(lines[begin+1:end], "")
	extracted = strings.ReplaceAll(extracted, "\r\n", "\n")
	extracted = strings.ReplaceAll(extracted, "\r", "\n")
	return extracted, nil
}

// Parse returns the last value of each directive in data. Blank lines,
// comments, section headers, and inline comments are ignored. A malformed
// directive line fails with a line-numbered ValidationError.
func Parse(data []byte) (map[string]string, error) {
	return parseValues(data)
}

func parseValues(data []byte) (map[string]string, error) {
	values := make(map[string]string)
	for index, line := range strings.Split(string(data), "\n") {
		number := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if err := validateSectionHeader(stripInlineComment(trimmed)); err != nil {
				return nil, &ValidationError{Line: number, Message: err.Error()}
			}
			continue
		}
		key, value, ok := splitDirective(trimmed)
		if !ok || !directiveKeyPattern.MatchString(key) {
			return nil, &ValidationError{Line: number, Message: "expected a key = value directive"}
		}
		values[key] = value
	}
	return values, nil
}

func normalizeBase(base []byte) []byte {
	if len(base) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(base), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return []byte(text + "\n")
}

func writeSorted(b *strings.Builder, values map[string]string) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString(" = ")
		b.WriteString(values[key])
		b.WriteByte('\n')
	}
}

func validateExtensions(extensions []Extension) ([]Extension, error) {
	validated := make([]Extension, 0, len(extensions))
	for index, extension := range extensions {
		if !extensionNamePattern.MatchString(extension.Name) {
			return nil, &ValidationError{
				Message: fmt.Sprintf("extension %d has invalid name %q", index+1, extension.Name),
			}
		}
		kind, err := normalizeExtensionType(extension.Type)
		if err != nil {
			return nil, &ValidationError{
				Message: fmt.Sprintf("extension %q: %v", extension.Name, err),
			}
		}
		if !extensionSOPattern.MatchString(extension.SO) {
			return nil, &ValidationError{
				Message: fmt.Sprintf("extension %q has invalid shared object %q", extension.Name, extension.SO),
			}
		}
		validated = append(validated, Extension{Name: extension.Name, Type: kind, SO: extension.SO})
	}
	return validated, nil
}

func normalizeExtensionType(kind string) (string, error) {
	switch kind {
	case "extension":
		return "extension", nil
	case "zend_extension":
		return "zend_extension", nil
	default:
		return "", fmt.Errorf("unsupported extension type %q", kind)
	}
}

func validateManagedPath(kind, value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return &ValidationError{Message: fmt.Sprintf("%s contains a NUL or newline", kind)}
	}
	return nil
}

func quoteIni(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + escaped + `"`
}
