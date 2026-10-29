package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ensurePHPBaseIni makes the build's base php.ini usable on its own. The
// upstream build records a compiled-in extension_dir under the temporary build
// directory; devstack composes per-site configs, but doctor also expects the
// build base to compensate. Rewrite it to the final installed path.
func ensurePHPBaseIni(tempRoot, target string) error {
	extDir, ok := buildExtensionDir(tempRoot)
	if !ok {
		return nil
	}
	rel, err := filepath.Rel(tempRoot, extDir)
	if err != nil {
		return err
	}
	finalDir := filepath.ToSlash(filepath.Join(target, rel))
	iniPath := filepath.Join(tempRoot, "etc", "php.ini")
	data, err := os.ReadFile(iniPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)
	line := `extension_dir = "` + finalDir + `"`
	re := regexp.MustCompile(`(?im)^[ \t]*extension_dir[ \t]*=.*$`)
	if re.MatchString(content) {
		content = re.ReplaceAllString(content, line)
	} else {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += line + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(iniPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(iniPath, []byte(content), 0o644)
}

func buildExtensionDir(root string) (string, bool) {
	base := filepath.Join(root, "lib", "php", "extensions")
	if hasPHPModule(base) {
		return base, true
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", false
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(base, entry.Name())
		if hasPHPModule(candidate) {
			dirs = append(dirs, candidate)
		}
	}
	if len(dirs) == 0 {
		return "", false
	}
	sort.Strings(dirs)
	return dirs[0], true
}

func hasPHPModule(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".so") {
			return true
		}
	}
	return false
}
