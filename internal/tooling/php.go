package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
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

	// CLIIniRawSettingKey stores advanced CLI-only php.ini directives. They are
	// rendered after the shared per-version defaults so they can override them
	// without changing site configuration.
	CLIIniRawSettingKey = "php_cli_ini_raw"

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

// WindowsShimDir returns the directory holding public Windows command shims.
func WindowsShimDir(paths config.Paths) string {
	return filepath.Join(paths.Home, "bin")
}

// WindowsPHPShimPath returns the public Windows php.cmd shim path.
func WindowsPHPShimPath(paths config.Paths) string {
	return filepath.Join(WindowsShimDir(paths), "php.cmd")
}

// SiteShimDir returns the per-site shim directory prepended to a project
// shell's PATH. The component is the validated site name, matching
// DEVSTACK_SITE and the Composer wrapper's site-shim check.
func SiteShimDir(paths config.Paths, siteName string) string {
	return filepath.Join(paths.Tools, "sites", siteName, "bin")
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

func isCompatibleBackend(b model.BackendKind) bool {
	if runtime.GOOS == "windows" {
		return b == model.BackendWSL || b == model.BackendLocal || b == ""
	}
	return b == model.BackendLocal || b == ""
}

// installedPHPBuilds returns installed PHP builds, newest first.
func installedPHPBuilds(ctx context.Context, st *store.Store) ([]model.RuntimeBuild, error) {
	builds, err := st.ListRuntimes(ctx)
	if err != nil {
		return nil, fmt.Errorf("tooling: list runtimes: %w", err)
	}
	php := make([]model.RuntimeBuild, 0, len(builds))
	for _, build := range builds {
		if build.Kind == "php" && isCompatibleBackend(build.Backend) {
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
			if build.Kind == "php" && build.ID == buildID && isCompatibleBackend(build.Backend) {
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
		if build.Kind != "php" || catalog.LineOf(build.Version) != line || !isCompatibleBackend(build.Backend) {
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
		if build.Kind != "node" || !isCompatibleBackend(build.Backend) {
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

// ensureGlobalIniLocked renders and atomically writes the machine-wide CLI
// ini. The caller must hold s.mu.
func (s *Service) ensureGlobalIniLocked(ctx context.Context, build model.RuntimeBuild) error {
	path := GlobalIniPath(s.deps.Paths)
	raw, err := s.loadOrMigrateCLIRawLocked(ctx, path)
	if err != nil {
		return err
	}
	content, err := s.renderGlobalIniLocked(ctx, build, raw)
	if err != nil {
		return err
	}
	return WriteGlobalIni(build, path, content)
}

// loadOrMigrateCLIRawLocked returns the CLI-only raw directives. On first use,
// it extracts the raw section from an existing generated ini (or preserves a
// wholly manual file) so upgrading never discards user edits.
func (s *Service) loadOrMigrateCLIRawLocked(ctx context.Context, path string) (string, error) {
	value, err := s.deps.Store.GetSetting(ctx, CLIIniRawSettingKey)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return "", fmt.Errorf("tooling: read %s: %w", CLIIniRawSettingKey, err)
	}
	raw := ""
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		if section, extractErr := phpini.ExtractSiteSection(data); extractErr == nil {
			raw = section
		} else {
			raw = string(data)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("tooling: read existing global php.ini: %w", readErr)
	}
	if err := s.deps.Store.SetSetting(ctx, CLIIniRawSettingKey, raw); err != nil {
		return "", fmt.Errorf("tooling: migrate %s: %w", CLIIniRawSettingKey, err)
	}
	return raw, nil
}

// renderGlobalIniLocked composes the CLI ini from the build base, the shared
// PHP line defaults, the CLI-only raw section, and the resolved extension
// block. Composer's required extensions are forced on so `composer` keeps
// working even if a version profile disables them.
func (s *Service) renderGlobalIniLocked(ctx context.Context, build model.RuntimeBuild, raw string) ([]byte, error) {
	if strings.TrimSpace(build.Path) == "" {
		return nil, errors.New("tooling: PHP build path is empty")
	}
	version := catalog.LineOf(build.Version)
	defaults, err := s.phpDefaultsForLocked(ctx, version)
	if err != nil {
		return nil, err
	}
	manifest, err := catalog.LoadPHP()
	if err != nil {
		return nil, err
	}
	builtin := make(map[string]bool)
	for _, name := range manifest.BuiltinsFor(version) {
		builtin[name] = true
	}
	composer := make(map[string]bool, 3)
	for _, name := range []string{"phar", "openssl", "mbstring"} {
		if builtin[name] {
			continue
		}
		extension, ok := manifestExtension(manifest, name)
		if !ok || !extensionSupportsVersion(extension, version) {
			continue
		}
		composer[name] = true
	}
	resolved, err := manifest.ResolveWithDefaults(version, defaults.Extensions, composer)
	if err != nil {
		return nil, fmt.Errorf("tooling: resolve PHP %s CLI extensions: %w", version, err)
	}
	extensions := make([]phpini.Extension, 0, len(resolved))
	for _, extension := range resolved {
		extensions = append(extensions, phpini.Extension{
			Name: extension.Name,
			Type: string(extension.Type),
			SO:   extension.SO,
		})
	}
	base, err := readCLIBaseIni(build, s.deps.WSLDistro)
	if err != nil {
		return nil, err
	}
	extensionDir, err := phpCLIExtensionDir(build, s.deps.WSLDistro)
	if err != nil {
		return nil, err
	}
	content, err := phpini.Compose(phpini.Config{
		Base:            base,
		VersionDefaults: defaults.Ini,
		Raw:             raw,
		Extensions:      extensions,
		ExtensionDir:    extensionDir,
	})
	if err != nil {
		return nil, fmt.Errorf("tooling: compose global php.ini: %w", err)
	}
	return content, nil
}

// phpDefaultsForLocked returns the shared per-version defaults, seeding the
// store from the embedded manifest on first use. Built-in extensions are
// removed from stored rows so a profile written before a line made a module
// built-in cannot break CLI rendering.
func (s *Service) phpDefaultsForLocked(ctx context.Context, version string) (model.PHPDefaults, error) {
	defaults, err := s.deps.Store.GetPHPDefaults(ctx, version)
	if err == nil {
		return s.sanitizeCLIDefaultsLocked(ctx, defaults, version)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return model.PHPDefaults{}, fmt.Errorf("tooling: read PHP %s defaults: %w", version, err)
	}
	manifest, err := catalog.LoadPHP()
	if err != nil {
		return model.PHPDefaults{}, err
	}
	seeded, ok := manifest.DefaultsFor(version)
	if !ok {
		return model.PHPDefaults{}, fmt.Errorf("tooling: no PHP defaults for version %q", version)
	}
	defaults = model.PHPDefaults{
		Version:    version,
		Ini:        seeded.Ini,
		Extensions: seeded.Extensions,
	}
	if defaults.Ini == nil {
		defaults.Ini = map[string]string{}
	}
	if defaults.Extensions == nil {
		defaults.Extensions = map[string]bool{}
	}
	if err := s.deps.Store.SavePHPDefaults(ctx, defaults); err != nil {
		return model.PHPDefaults{}, fmt.Errorf("tooling: seed PHP %s defaults: %w", version, err)
	}
	return defaults, nil
}

func (s *Service) sanitizeCLIDefaultsLocked(ctx context.Context, defaults model.PHPDefaults, version string) (model.PHPDefaults, error) {
	manifest, err := catalog.LoadPHP()
	if err != nil {
		return model.PHPDefaults{}, err
	}
	changed := false
	for _, name := range manifest.BuiltinsFor(version) {
		if _, ok := defaults.Extensions[name]; !ok {
			continue
		}
		delete(defaults.Extensions, name)
		changed = true
	}
	if changed {
		if err := s.deps.Store.SavePHPDefaults(ctx, defaults); err != nil {
			return model.PHPDefaults{}, fmt.Errorf("tooling: sanitize PHP %s defaults: %w", version, err)
		}
	}
	return defaults, nil
}

func manifestExtension(manifest catalog.PHPManifest, name string) (catalog.Extension, bool) {
	for _, extension := range manifest.Extensions {
		if extension.Name == name {
			return extension, true
		}
	}
	return catalog.Extension{}, false
}

func extensionSupportsVersion(extension catalog.Extension, version string) bool {
	for _, supported := range extension.Versions {
		if supported == version {
			return true
		}
	}
	return false
}

// readCLIBaseIni reads the selected build's base php.ini. A missing base is
// valid: the embedded defaults still produce a complete file.
func readCLIBaseIni(build model.RuntimeBuild, distro string) ([]byte, error) {
	path := buildHostPath(build, joinBuildPath(build, "etc", "php.ini"), distro)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tooling: read PHP build base ini: %w", err)
	}
	return data, nil
}

// phpCLIExtensionDir locates the build's extension directory using the same
// relocatable layout the site PHP renderer accepts. The returned path is the
// POSIX path PHP inside WSL must load, not the Windows UNC path used to inspect
// the build from the daemon host.
func phpCLIExtensionDir(build model.RuntimeBuild, distro string) (string, error) {
	root := joinBuildPath(build, "lib", "php", "extensions")
	entries, err := os.ReadDir(buildHostPath(build, root, distro))
	if err != nil {
		return "", fmt.Errorf("tooling: PHP build %q extension directory: %w", build.ID, err)
	}
	var subdirectories []string
	for _, entry := range entries {
		if entry.IsDir() {
			subdirectories = append(subdirectories, entry.Name())
			continue
		}
		if strings.HasSuffix(entry.Name(), ".so") {
			return root, nil
		}
	}
	if len(subdirectories) == 1 {
		return joinBuildPath(build, "lib", "php", "extensions", subdirectories[0]), nil
	}
	return "", fmt.Errorf("tooling: PHP build %q has no unambiguous extension directory under %s", build.ID, root)
}

// joinBuildPath joins a build path in the path style the build records. WSL
// builds always store POSIX paths; local builds use the host style.
func joinBuildPath(build model.RuntimeBuild, parts ...string) string {
	if build.Backend == model.BackendWSL || strings.HasPrefix(strings.TrimSpace(build.Path), "/") {
		return path.Join(append([]string{build.Path}, parts...)...)
	}
	return filepath.Join(append([]string{build.Path}, parts...)...)
}

// buildHostPath maps a build-owned path to the daemon host. On Windows, WSL
// builds are read through the distro's UNC share while PHP keeps seeing the
// recorded POSIX path.
func buildHostPath(build model.RuntimeBuild, value, distro string) string {
	if runtime.GOOS != "windows" {
		return filepath.FromSlash(value)
	}
	if build.Backend != model.BackendWSL && !strings.HasPrefix(strings.TrimSpace(build.Path), "/") {
		return filepath.FromSlash(value)
	}
	if strings.TrimSpace(distro) == "" {
		distro = "devstack"
	}
	return wslSharePrefix(distro) + strings.ReplaceAll(value, "/", `\`)
}

func wslSharePrefix(distro string) string {
	for _, root := range []string{`\\wsl.localhost\`, `\\wsl$\`} {
		candidate := root + distro
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return `\\wsl.localhost\` + distro
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
	tmp, err := os.CreateTemp(filepath.Dir(path), "php.ini-*.tmp")
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

// EnsurePHPShims writes wrappers for php, php-config, and phpize when the
// selected build provides each binary. The php wrapper executes the absolute
// build binary with the global ini; it never resolves back to the shim.
func EnsurePHPShims(build model.RuntimeBuild, paths config.Paths, iniPath string) ([]string, error) {
	if build.Backend == model.BackendWSL {
		return nil, nil
	}
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
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*.tmp")
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
