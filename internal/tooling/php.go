package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

const (
	// DefaultPHPSettingKey is the store setting holding the machine-wide PHP
	// CLI default. It is either a major.minor line or an exact build id.
	DefaultPHPSettingKey = "php_default"

	// ComposerVersion, ComposerURL, ComposerSHA256, and ComposerSize pin the
	// managed Composer PHAR. There is deliberately no fallback URL or version.
	ComposerVersion = "2.10.3"
	ComposerURL     = "https://getcomposer.org/download/2.10.3/composer.phar"
	ComposerSHA256  = "7a2d379d5b8ffdaa028580ef26494c36d2feef4b178d3dd1473a4dbc5e17c8d6"
	ComposerSize    = int64(3642137)
)

var (
	// ErrNoPHPInstalled reports that no managed PHP build is installed.
	ErrNoPHPInstalled = errors.New("tooling: no PHP build is installed")
	// ErrPHPDefaultNotInstalled reports that the requested PHP default does
	// not name an installed build.
	ErrPHPDefaultNotInstalled = errors.New("tooling: the requested PHP default is not installed")
	// ErrUnsupportedPHPDefault reports a malformed PHP default value.
	ErrUnsupportedPHPDefault = errors.New("tooling: invalid PHP default")
	// ErrRuntimeMissing reports that a site's selected runtime is not installed.
	ErrRuntimeMissing = errors.New("tooling: required runtime is not installed")
	// ErrComposerChecksum reports a Composer download whose size or SHA-256
	// does not match the pinned values.
	ErrComposerChecksum = errors.New("tooling: composer checksum mismatch")
	// ErrUnsupportedTool reports a tool id outside the managed set.
	ErrUnsupportedTool = errors.New("tooling: unsupported tool")
)

// GlobalIniPath returns the machine-wide CLI php.ini path.
func GlobalIniPath(paths config.Paths) string {
	return filepath.Join(paths.CLI, "php.ini")
}

// ShimDir returns the directory holding the global PHP CLI shims.
func ShimDir(paths config.Paths) string {
	return filepath.Join(paths.Tools, "php-default", "bin")
}

// SiteShimDir returns the per-site shim directory prepended to a project
// shell's PATH.
func SiteShimDir(paths config.Paths, siteID string) string {
	return filepath.Join(paths.Tools, "sites", siteID, "bin")
}

// ComposerDir returns the managed Composer directory.
func ComposerDir(paths config.Paths) string {
	return filepath.Join(paths.Tools, "composer")
}

// ComposerPHARPath returns the managed Composer PHAR path.
func ComposerPHARPath(paths config.Paths) string {
	return filepath.Join(ComposerDir(paths), "composer.phar")
}

// ComposerBinDir returns the directory holding the managed Composer wrapper.
func ComposerBinDir(paths config.Paths) string {
	return filepath.Join(ComposerDir(paths), "bin")
}

// ComposerWrapperPath returns the managed Composer wrapper path.
func ComposerWrapperPath(paths config.Paths) string {
	return filepath.Join(ComposerBinDir(paths), "composer")
}

// ResolveDefault returns the machine-wide PHP CLI build. A store setting may
// name an exact build id or a major.minor line; a line resolves to the newest
// installed build on that line. Without a setting the newest installed PHP
// build is used. ResolveDefault never downloads.
func ResolveDefault(ctx context.Context, st *store.Store) (model.RuntimeBuild, error) {
	if st == nil {
		return model.RuntimeBuild{}, errors.New("tooling: no store configured")
	}
	builds, err := installedPHPBuilds(ctx, st)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	setting := ""
	if value, err := st.GetSetting(ctx, DefaultPHPSettingKey); err == nil {
		setting = strings.TrimSpace(value)
	} else if !errors.Is(err, store.ErrNotFound) {
		return model.RuntimeBuild{}, fmt.Errorf("tooling: read %s: %w", DefaultPHPSettingKey, err)
	}
	if setting == "" {
		if len(builds) == 0 {
			return model.RuntimeBuild{}, ErrNoPHPInstalled
		}
		return builds[0], nil
	}
	build, ok, err := resolvePHPSelection(builds, setting)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	if !ok {
		return model.RuntimeBuild{}, fmt.Errorf("%w: %s", ErrPHPDefaultNotInstalled, setting)
	}
	return build, nil
}

// installedPHPBuilds returns installed PHP builds, newest first.
func installedPHPBuilds(ctx context.Context, st *store.Store) ([]model.RuntimeBuild, error) {
	builds, err := st.ListRuntimes(ctx)
	if err != nil {
		return nil, fmt.Errorf("tooling: list runtimes: %w", err)
	}
	php := make([]model.RuntimeBuild, 0, len(builds))
	for _, build := range builds {
		if build.Kind == "php" && build.Backend == model.BackendLocal {
			php = append(php, build)
		}
	}
	sort.SliceStable(php, func(i, j int) bool {
		if cmp := compareVersions(php[i].Version, php[j].Version); cmp != 0 {
			return cmp > 0
		}
		return buildRevision(php[i].ID) > buildRevision(php[j].ID)
	})
	return php, nil
}

// resolvePHPSelection resolves a line or exact build id against builds.
func resolvePHPSelection(builds []model.RuntimeBuild, value string) (model.RuntimeBuild, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return model.RuntimeBuild{}, false, nil
	}
	if isLine(value) {
		for _, build := range builds {
			if catalog.LineOf(build.Version) == value {
				return build, true, nil
			}
		}
		return model.RuntimeBuild{}, false, nil
	}
	id := strings.TrimPrefix(value, "php@")
	if parsed, err := catalog.ParseID("php@" + id); err == nil && parsed.Kind == "php" {
		for _, build := range builds {
			if build.ID == "php@"+id {
				return build, true, nil
			}
		}
		return model.RuntimeBuild{}, false, nil
	}
	return model.RuntimeBuild{}, false, fmt.Errorf("%w: %q (want a line such as 8.4 or an installed build id)", ErrUnsupportedPHPDefault, value)
}

// ResolvePHPBuild resolves a site's pinned PHP build from installed builds.
func ResolvePHPBuild(builds []model.RuntimeBuild, buildID, version string) (model.RuntimeBuild, error) {
	buildID = strings.TrimSpace(buildID)
	if buildID != "" {
		for _, build := range builds {
			if build.Kind == "php" && build.ID == buildID && (build.Backend == "" || build.Backend == model.BackendLocal) {
				return build, nil
			}
		}
		return model.RuntimeBuild{}, fmt.Errorf("%w: %s", ErrRuntimeMissing, buildID)
	}
	line := strings.TrimSpace(version)
	if line == "" {
		return model.RuntimeBuild{}, fmt.Errorf("%w: PHP version is empty", ErrRuntimeMissing)
	}
	var best model.RuntimeBuild
	found := false
	for _, build := range builds {
		if build.Kind != "php" || catalog.LineOf(build.Version) != line || (build.Backend != "" && build.Backend != model.BackendLocal) {
			continue
		}
		if !found || compareVersions(build.Version, best.Version) > 0 {
			best, found = build, true
		}
	}
	if !found {
		return model.RuntimeBuild{}, fmt.Errorf("%w: PHP %s", ErrRuntimeMissing, line)
	}
	return best, nil
}

// ResolveNodeBuild resolves a site's Node build. A full version matches
// exactly; a major line such as "22" selects the newest installed build on
// that line.
func ResolveNodeBuild(builds []model.RuntimeBuild, requested string) (model.RuntimeBuild, error) {
	requested = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(requested), "node@"))
	if requested == "" {
		return model.RuntimeBuild{}, fmt.Errorf("%w: Node version is empty", ErrRuntimeMissing)
	}
	var best model.RuntimeBuild
	found := false
	for _, build := range builds {
		if build.Kind != "node" || (build.Backend != "" && build.Backend != model.BackendLocal) {
			continue
		}
		match := build.Version == requested ||
			(isLine(requested) && catalog.LineOf(build.Version) == requested) ||
			(!strings.Contains(requested, ".") && strings.HasPrefix(build.Version, requested+"."))
		if !match {
			continue
		}
		if !found || compareVersions(build.Version, best.Version) > 0 {
			best, found = build, true
		}
	}
	if !found {
		return model.RuntimeBuild{}, fmt.Errorf("%w: Node %s", ErrRuntimeMissing, requested)
	}
	return best, nil
}

func isLine(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

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
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
	}
	return 0
}

func buildRevision(id string) int {
	parsed, err := catalog.ParseID(id)
	if err != nil {
		return 0
	}
	return parsed.Revision
}

// EnsureGlobalIni composes and atomically writes the machine-wide CLI ini from
// the selected build's base ini. It loads the Composer-required extensions when
// the build provides them and never applies site overrides.
func EnsureGlobalIni(build model.RuntimeBuild, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("tooling: global ini path is empty")
	}
	if strings.TrimSpace(build.Path) == "" {
		return errors.New("tooling: PHP build path is empty")
	}
	base, err := os.ReadFile(filepath.Join(build.Path, "etc", "php.ini"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("tooling: read PHP build base ini: %w", err)
	}
	extDir, extensions := composerExtensions(build.Path)
	content, err := phpini.Compose(phpini.Config{
		Base:         base,
		Extensions:   extensions,
		ExtensionDir: extDir,
	})
	if err != nil {
		return fmt.Errorf("tooling: compose global php.ini: %w", err)
	}
	return WriteGlobalIni(build, path, content)
}

// WriteGlobalIni validates and atomically writes the global CLI ini with mode
// 0644.
func WriteGlobalIni(_ model.RuntimeBuild, path string, content []byte) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("tooling: global ini path is empty")
	}
	if _, err := phpini.Parse(content); err != nil {
		return fmt.Errorf("tooling: invalid global php.ini: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("tooling: create global ini directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".php.ini-*")
	if err != nil {
		return fmt.Errorf("tooling: create temporary global ini: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tooling: set global ini mode: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tooling: write global ini: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tooling: sync global ini: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("tooling: close global ini: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("tooling: replace global ini: %w", err)
	}
	return nil
}

// ReadGlobalIni returns the global ini content and its path.
func ReadGlobalIni(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tooling: read global php.ini: %w", err)
	}
	return data, nil
}

// composerExtensions resolves the extensions Composer needs from an installed
// build. Built-in modules that have no shared object are simply omitted.
func composerExtensions(buildPath string) (string, []phpini.Extension) {
	extDir := extensionDir(buildPath)
	if extDir == "" {
		return "", nil
	}
	var extensions []phpini.Extension
	for _, name := range []string{"phar", "openssl", "mbstring"} {
		if info, err := os.Stat(filepath.Join(extDir, name+".so")); err == nil && info.Mode().IsRegular() {
			extensions = append(extensions, phpini.Extension{Name: name, Type: "extension", SO: name + ".so"})
		}
	}
	return extDir, extensions
}

// extensionDir locates the build's extension directory using the same
// relocatable layout the site PHP renderer accepts.
func extensionDir(buildPath string) string {
	root := filepath.Join(buildPath, "lib", "php", "extensions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var subdirectories []string
	for _, entry := range entries {
		if entry.IsDir() {
			subdirectories = append(subdirectories, entry.Name())
			continue
		}
		if strings.HasSuffix(entry.Name(), ".so") {
			return filepath.Clean(root)
		}
	}
	if len(subdirectories) == 1 {
		return filepath.Join(root, subdirectories[0])
	}
	return ""
}

// EnsurePHPShims writes wrappers for php, php-config, and phpize when the
// selected build provides each binary. The php wrapper executes the absolute
// build binary with the global ini; it never resolves back to the shim.
func EnsurePHPShims(build model.RuntimeBuild, paths config.Paths, iniPath string) ([]string, error) {
	if strings.TrimSpace(build.Path) == "" {
		return nil, errors.New("tooling: PHP build path is empty")
	}
	if !filepath.IsAbs(build.Path) {
		return nil, fmt.Errorf("tooling: PHP build path %q is not absolute", build.Path)
	}
	dir := ShimDir(paths)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("tooling: create PHP shim directory: %w", err)
	}
	installed := make([]string, 0, 3)
	expected := make(map[string]bool, 3)
	for _, name := range []string{"php", "php-config", "phpize"} {
		binary := filepath.Join(build.Path, "bin", name)
		info, err := os.Stat(binary)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("tooling: inspect PHP binary %s: %w", binary, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		var script string
		if name == "php" {
			script = "#!/bin/sh\nexec " + shellQuote(binary) + " -c " + shellQuote(iniPath) + " \"$@\"\n"
		} else {
			script = "#!/bin/sh\nexec " + shellQuote(binary) + " \"$@\"\n"
		}
		if err := writeScriptAtomic(filepath.Join(dir, name), script); err != nil {
			return nil, err
		}
		expected[name] = true
		installed = append(installed, name)
	}
	for _, name := range []string{"php", "php-config", "phpize"} {
		if expected[name] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("tooling: remove stale PHP shim %s: %w", name, err)
		}
	}
	if err := writeAtomicFile(filepath.Join(paths.Tools, "php-default", "VERSION"), []byte(build.ID+"\n"), 0o644); err != nil {
		return nil, err
	}
	return installed, nil
}

// writeScriptAtomic writes an executable shell script.
func writeScriptAtomic(path, content string) error {
	return writeAtomicFile(path, []byte(content), 0o755)
}

// writeAtomicFile replaces path atomically with mode.
func writeAtomicFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("tooling: create directory %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("tooling: create temporary %s: %w", path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tooling: set mode on %s: %w", path, err)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tooling: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("tooling: sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("tooling: close %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("tooling: replace %s: %w", path, err)
	}
	return nil
}

// shellQuote renders value as a single-quoted POSIX shell word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
