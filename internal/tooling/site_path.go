package tooling

import (
	"path/filepath"
	"strings"
)

// canonicalSitePath normalizes the two Windows UNC spellings WSL exposes for
// the same distro. `\\wsl$\<distro>` and `\\wsl.localhost\<distro>` are the same
// location, but Windows reports the cwd using whichever spelling the user
// entered. Site matching must compare them as one path.
func canonicalSitePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = normalizeWSLUNCPath(value)
	return filepath.Clean(value)
}

func normalizeWSLUNCPath(value string) string {
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, `\\?\unc\`) {
		value = `\\` + value[len(`\\?\unc\`):]
		lower = strings.ToLower(value)
	}
	for _, prefix := range []string{`\\wsl$\`, `//wsl$/`} {
		if strings.HasPrefix(lower, prefix) {
			return `\\wsl.localhost\` + value[len(prefix):]
		}
	}
	for _, prefix := range []string{`\\wsl.localhost\`, `//wsl.localhost/`} {
		if strings.HasPrefix(lower, prefix) {
			return `\\wsl.localhost\` + value[len(prefix):]
		}
	}
	return value
}
