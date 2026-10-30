package tooling

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/platform"
)

var (
	wslDistroPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	envNamePattern          = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	nodeExactVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// EnsureWindowsToolShims creates the managed Windows command wrappers for
// PHP and Composer. Both wrappers invoke the selected WSL distro directly.
func EnsureWindowsToolShims(binDir, distro string) error {
	if !wslDistroPattern.MatchString(strings.TrimSpace(distro)) {
		return fmt.Errorf("tooling: invalid WSL distro name %q", distro)
	}
	if strings.TrimSpace(binDir) == "" {
		return errors.New("tooling: Windows shim directory is empty")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("tooling: create Windows shim directory: %w", err)
	}
	toolArgTranslate := `args=(); for a in \"$@\"; do a_norm=\"${a//\\//}\"; if [[ \"$a_norm\" =~ ^([a-zA-Z]):/(.*) ]]; then drive=\"${BASH_REMATCH[1],,}\"; rest=\"${BASH_REMATCH[2]}\"; args+=(\"/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=([a-zA-Z]):/(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; drive=\"${BASH_REMATCH[2],,}\"; rest=\"${BASH_REMATCH[3]}\"; args+=(\"$opt=/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then rest=\"${BASH_REMATCH[2]}\"; if [[ -z \"$rest\" ]]; then args+=(\"/\"); else args+=(\"/$rest\"); fi; elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; rest=\"${BASH_REMATCH[3]}\"; if [[ -z \"$rest\" ]]; then args+=(\"$opt=/\"); else args+=(\"$opt=/$rest\"); fi; elif [[ \"$a_norm\" =~ ^/[uU]sers/(.*) ]]; then rest=\"${BASH_REMATCH[1]}\"; args+=(\"/mnt/c/Users/$rest\"); else args+=(\"$a\"); fi; done;`
	for _, tool := range []string{"php", "composer"} {
		path := filepath.Join(binDir, tool+".cmd")
		content := fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s --exec bash -c \"%s exec %s \\\"${args[@]}\\\"\" _ %%*\r\n", distro, toolArgTranslate, tool)
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, []byte(content), 0o600); err != nil {
			return fmt.Errorf("tooling: write %s: %w", temporary, err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: replace %s: %w", path, err)
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: install %s: %w", path, err)
		}
	}
	return nil
}

// EnsureWindowsSiteShims creates site-scoped Windows command wrappers for
// PHP, Composer, shell, and exec. Each wrapper invokes WSL within the site's
// backend directory and environment, properly quoting paths and variables.
func EnsureWindowsSiteShims(binDir, distro string, site model.Site) error {
	if !wslDistroPattern.MatchString(strings.TrimSpace(distro)) {
		return fmt.Errorf("tooling: invalid WSL distro name %q", distro)
	}
	if strings.TrimSpace(binDir) == "" {
		return errors.New("tooling: Windows site shim directory is empty")
	}
	if err := validateEnvironment(site.Env); err != nil {
		return fmt.Errorf("tooling: site %q: %w", site.Name, err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("tooling: create Windows site shim directory: %w", err)
	}
	env := WindowsSiteEnvironment(site)
	var baseScript strings.Builder
	baseScript.WriteString("cd -- ")
	baseScript.WriteString(posixQuote(site.BackendPath))
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		baseScript.WriteString(" && export ")
		baseScript.WriteString(k)
		baseScript.WriteByte('=')
		baseScript.WriteString(posixQuote(env[k]))
	}

	siteArgTranslate := `args=(); for a in \"$@\"; do a_norm=\"${a//\\//}\"; if [[ \"$a_norm\" =~ ^([a-zA-Z]):/(.*) ]]; then drive=\"${BASH_REMATCH[1],,}\"; rest=\"${BASH_REMATCH[2]}\"; args+=(\"/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=([a-zA-Z]):/(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; drive=\"${BASH_REMATCH[2],,}\"; rest=\"${BASH_REMATCH[3]}\"; args+=(\"$opt=/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then rest=\"${BASH_REMATCH[2]}\"; if [[ -z \"$rest\" ]]; then args+=(\"/\"); else args+=(\"/$rest\"); fi; elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; rest=\"${BASH_REMATCH[3]}\"; if [[ -z \"$rest\" ]]; then args+=(\"$opt=/\"); else args+=(\"$opt=/$rest\"); fi; elif [[ \"$a_norm\" =~ ^/[uU]sers/(.*) ]]; then rest=\"${BASH_REMATCH[1]}\"; args+=(\"/mnt/c/Users/$rest\"); else args+=(\"$a\"); fi; done;`
	shims := map[string]string{
		"php.cmd":      fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s -- bash -lc \"%s && %s exec php \\\"${args[@]}\\\"\" -- %%*\r\n", distro, baseScript.String(), siteArgTranslate),
		"composer.cmd": fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s -- bash -lc \"%s && %s exec composer \\\"${args[@]}\\\"\" -- %%*\r\n", distro, baseScript.String(), siteArgTranslate),
		"shell.cmd":    fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s -- bash -lc \"%s && exec \\\"${SHELL:-/bin/bash}\\\" -i\"\r\n", distro, baseScript.String()),
		"exec.cmd":     fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s -- bash -lc \"%s && %s exec \\\"${args[@]}\\\"\" -- %%*\r\n", distro, baseScript.String(), siteArgTranslate),
	}

	for tool, content := range shims {
		path := filepath.Join(binDir, tool)
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, []byte(content), 0o600); err != nil {
			return fmt.Errorf("tooling: write %s: %w", temporary, err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: replace %s: %w", path, err)
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: install %s: %w", path, err)
		}
	}
	return nil
}

// EnsureGlobalWindowsToolShims installs wrappers in the public Windows bin
// directory using the persisted distro or the managed-distro default.
func EnsureGlobalWindowsToolShims(home, distro string) error {
	return EnsureGlobalWindowsToolShimsForBuild(home, distro, "")
}

// EnsureGlobalWindowsToolShimsForBuild is the preferred form: it pins the
// Windows php.cmd and composer.cmd wrappers to the resolved PHP build instead
// of whatever `php` happens to be first in the WSL PATH.
func EnsureGlobalWindowsToolShimsForBuild(home, distro, phpBuildPath string) error {
	if strings.TrimSpace(home) == "" {
		return errors.New("tooling: devstack home is empty")
	}
	if strings.TrimSpace(distro) == "" {
		var err error
		distro, err = ReadWSLDistro(home)
		if err != nil {
			return err
		}
	}
	binDir := filepath.Join(home, "bin")
	iniPath := filepath.Join(home, "config", "cli", "php.ini")
	if phpBuildPath = strings.TrimSpace(phpBuildPath); phpBuildPath != "" {
		if !strings.HasPrefix(phpBuildPath, "/") {
			return fmt.Errorf("tooling: PHP build path %q is not a WSL path", phpBuildPath)
		}
		root := strings.TrimRight(phpBuildPath, "/")
		return ensureExactWindowsPHPShims(binDir, distro, root+"/bin/php", root+"/bin", iniPath)
	}
	if phpBin, phpBinDir, ok := windowsPHPDefaultTarget(home); ok {
		return ensureExactWindowsPHPShims(binDir, distro, phpBin, phpBinDir, iniPath)
	}
	return EnsureWindowsToolShims(binDir, distro)
}

// windowsPathToWSLPath maps an absolute Windows drive path to its WSL mount
// path. POSIX paths pass through so the shim builder remains testable off
// Windows.
func windowsPathToWSLPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("tooling: path is empty")
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(normalized, "/") && !strings.HasPrefix(normalized, "//") {
		return path.Clean(normalized), nil
	}
	if len(normalized) < 3 || normalized[1] != ':' || normalized[2] != '/' {
		return "", fmt.Errorf("tooling: %q is not an absolute drive path", value)
	}
	drive := normalized[0]
	if !((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) {
		return "", fmt.Errorf("tooling: %q has no drive letter", value)
	}
	rest := path.Clean(normalized[3:])
	if rest == "." {
		rest = ""
	}
	if rest == ".." || strings.HasPrefix(rest, "../") {
		return "", fmt.Errorf("tooling: %q escapes the drive root", value)
	}
	return "/mnt/" + strings.ToLower(string(drive)) + "/" + rest, nil
}

// windowsPHPDefaultTarget reads the registered PHP default and returns the
// exact WSL binary and bin directory. The Windows shim must not depend on an
// ambient WSL PATH, which can retain a symlink to a removed build.
func windowsPHPDefaultTarget(home string) (bin, binDir string, ok bool) {
	data, err := os.ReadFile(filepath.Join(home, "tools", "php-default", "VERSION"))
	if err != nil {
		return "", "", false
	}
	id := strings.TrimSpace(string(data))
	parsed, err := catalog.ParseID(id)
	if err != nil || parsed.Kind != "php" || id != catalog.FormatID("php", parsed.Version, parsed.Revision) {
		return "", "", false
	}
	root := "/data/devstack/runtimes/" + id + "/bin"
	return root + "/php", root, true
}

func ensureExactWindowsPHPShims(binDir, distro, phpBin, phpBinDir, iniPath string) error {
	if !wslDistroPattern.MatchString(strings.TrimSpace(distro)) {
		return fmt.Errorf("tooling: invalid WSL distro name %q", distro)
	}
	if strings.TrimSpace(binDir) == "" {
		return errors.New("tooling: Windows shim directory is empty")
	}
	wslIni, err := windowsPathToWSLPath(iniPath)
	if err != nil {
		return fmt.Errorf("tooling: map global CLI php.ini path: %w", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("tooling: create Windows shim directory: %w", err)
	}
	const argTranslate = `args=(); for a in \"$@\"; do a_norm=\"${a//\\//}\"; if [[ \"$a_norm\" =~ ^([a-zA-Z]):/(.*) ]]; then drive=\"${BASH_REMATCH[1],,}\"; rest=\"${BASH_REMATCH[2]}\"; args+=(\"/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=([a-zA-Z]):/(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; drive=\"${BASH_REMATCH[2],,}\"; rest=\"${BASH_REMATCH[3]}\"; args+=(\"$opt=/mnt/$drive/$rest\"); elif [[ \"$a_norm\" =~ ^//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then rest=\"${BASH_REMATCH[2]}\"; if [[ -z \"$rest\" ]]; then args+=(\"/\"); else args+=(\"/$rest\"); fi; elif [[ \"$a_norm\" =~ ^([a-zA-Z0-9_.-]+)=//wsl(\.localhost|\$)/[^/]+/?(.*) ]]; then opt=\"${BASH_REMATCH[1]}\"; rest=\"${BASH_REMATCH[3]}\"; if [[ -z \"$rest\" ]]; then args+=(\"$opt=/\"); else args+=(\"$opt=/$rest\"); fi; elif [[ \"$a_norm\" =~ ^/[uU]sers/(.*) ]]; then rest=\"${BASH_REMATCH[1]}\"; args+=(\"/mnt/c/Users/$rest\"); else args+=(\"$a\"); fi; done;`
	phpExec := strings.ReplaceAll("export PATH="+posixQuote(phpBinDir)+":\"$PATH\" && exec "+posixQuote(phpBin)+
		" -c "+posixQuote(wslIni)+" \"${args[@]}\"", `"`, `\"`)
	extDir := strings.TrimSuffix(phpBinDir, "/bin") + "/lib/php/extensions"
	composerExec := strings.ReplaceAll("exec "+posixQuote(phpBin)+
		" -c "+posixQuote(wslIni)+
		" -d extension_dir="+posixQuote(extDir)+
		" -d extension=phar.so -d extension=mbstring.so -d extension=openssl.so -d extension=curl.so "+
		posixQuote("/data/devstack/tools/composer/composer.phar")+" \"${args[@]}\"", `"`, `\"`)
	shims := map[string]string{
		"php.cmd":      fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s --exec bash -c \"%s %s\" _ %%*\r\n", distro, argTranslate, phpExec),
		"composer.cmd": fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d %s --exec bash -c \"%s %s\" _ %%*\r\n", distro, argTranslate, composerExec),
	}
	for tool, content := range shims {
		path := filepath.Join(binDir, tool)
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, []byte(content), 0o600); err != nil {
			return fmt.Errorf("tooling: write %s: %w", temporary, err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: replace %s: %w", path, err)
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("tooling: install %s: %w", path, err)
		}
	}
	return nil
}

// ReadWSLDistro reads only the persisted distro name and never creates or
// modifies Windows machine state.
func ReadWSLDistro(home string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("tooling: devstack home is empty")
	}
	distro := "devstack"
	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("tooling: read WSL distro config: %w", err)
	}
	if err == nil {
		var cfg struct {
			WSLDistro string `yaml:"wsl_distro"`
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return "", fmt.Errorf("tooling: parse WSL distro config: %w", err)
		}
		if strings.TrimSpace(cfg.WSLDistro) != "" {
			distro = strings.TrimSpace(cfg.WSLDistro)
		}
	}
	if !wslDistroPattern.MatchString(distro) {
		return "", fmt.Errorf("tooling: invalid WSL distro name %q", distro)
	}
	return distro, nil
}

// WindowsSiteEnvironmentForNode builds the small, Linux-native environment
// that the Windows CLI passes into WSL for the daemon-resolved exact Node
// build. A raw version line such as "22" is ignored rather than converted into
// a node@22 runtime path that does not exist. build.node_version is never
// consulted because it selects the build profile, not the shell toolchain.
func WindowsSiteEnvironmentForNode(site model.Site, nodeVersion string) map[string]string {
	return windowsSiteEnvironment(site, exactNodeVersion(nodeVersion))
}

// WindowsSiteEnvironment is the legacy wrapper for callers not yet wired to
// the daemon resolver. It keeps preferring the site pin, then the build
// profile, so existing callers continue to resolve a Node runtime.
func WindowsSiteEnvironment(site model.Site) map[string]string {
	nodeVersion := strings.TrimPrefix(strings.TrimSpace(site.NodeVersion), "node@")
	if nodeVersion == "" && site.Build != nil {
		nodeVersion = strings.TrimPrefix(strings.TrimSpace(site.Build.NodeVersion), "node@")
	}
	return windowsSiteEnvironment(site, nodeVersion)
}

func windowsSiteEnvironment(site model.Site, nodeVersion string) map[string]string {
	values := make(map[string]string, len(site.Env)+6)
	for key, value := range site.Env {
		values[key] = value
	}
	values["DEVSTACK_SITE"] = site.Name
	values["DEVSTACK_HOME"] = "/data/devstack"
	values["DEVSTACK_PROJECT"] = site.BackendPath

	// The agent does not materialize per-site shim directories on Windows, so
	// point PATH at the exact runtime builds as well as the conventional shim
	// directory (kept first for forward compatibility).
	pathDirs := []string{"/data/devstack/tools/sites/" + site.Name + "/bin"}
	if site.PHPBuild != "" {
		pathDirs = append(pathDirs, "/data/devstack/runtimes/"+site.PHPBuild+"/bin")
	}
	if nodeVersion != "" {
		pathDirs = append(pathDirs, "/data/devstack/runtimes/node@"+nodeVersion+"/bin")
	}
	pathDirs = append(pathDirs, "/data/devstack/bin", "/usr/local/bin", "/usr/bin", "/bin")
	values["PATH"] = strings.Join(pathDirs, ":")

	if site.PHPBuild != "" {
		values["DEVSTACK_PHP_BUILD"] = site.PHPBuild
	}
	if nodeVersion != "" {
		values["DEVSTACK_NODE_VERSION"] = nodeVersion
	}
	return values
}

// exactNodeVersion returns major.minor.patch or an empty string. The resolver
// is the only component allowed to turn a version line into a build, so this
// function refuses anything else instead of guessing.
func exactNodeVersion(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "node@")
	if !nodeExactVersionPattern.MatchString(version) {
		return ""
	}
	return version
}

// BuildWSLExecArgs returns arguments for wsl.exe to launch a command in the
// selected distro with a validated working directory and shell-quoted env.
func BuildWSLExecArgs(distro, backendPath string, env map[string]string, argv []string, interactive bool) ([]string, error) {
	if !wslDistroPattern.MatchString(strings.TrimSpace(distro)) {
		return nil, fmt.Errorf("tooling: invalid WSL distro name %q", distro)
	}
	if !strings.HasPrefix(backendPath, "/") || strings.HasPrefix(backendPath, "//") || hasParentSegment(backendPath) || strings.ContainsAny(backendPath, "\x00\r\n") {
		return nil, fmt.Errorf("tooling: invalid WSL project path %q", backendPath)
	}
	if !interactive && len(argv) == 0 {
		return nil, errors.New("tooling: WSL exec requires a command")
	}
	var script strings.Builder
	script.WriteString("cd -- ")
	script.WriteString(posixQuote(backendPath))
	keys := make([]string, 0, len(env))
	for key, value := range env {
		if !envNamePattern.MatchString(key) || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("tooling: invalid WSL environment entry %q", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		script.WriteString(" && export ")
		script.WriteString(key)
		script.WriteByte('=')
		script.WriteString(posixQuote(env[key]))
	}
	script.WriteString(" && exec ")
	if interactive {
		script.WriteString(`"${SHELL:-/bin/bash}" -i`)
	} else {
		for i, arg := range argv {
			if strings.ContainsRune(arg, 0) {
				return nil, errors.New("tooling: WSL command argument contains NUL")
			}
			if i > 0 {
				script.WriteByte(' ')
			}
			script.WriteString(posixQuote(arg))
		}
	}
	return []string{"-d", distro, "--", "bash", "-lc", script.String()}, nil
}

func posixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func hasParentSegment(value string) bool {
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// WSLComposerWrapperScript returns the robust POSIX shell wrapper executed
// inside WSL. It inspects the active PHP binary and dynamically injects the
// Composer-required extensions (phar, mbstring, openssl, curl) from the runtime.
func WSLComposerWrapperScript() string {
	return `#!/bin/sh
PHP_BIN="/usr/local/bin/php"
if [ -e "$PHP_BIN" ]; then
    REAL_PHP="$(readlink -f "$PHP_BIN")"
    PHP_DIR="$(dirname "$(dirname "$REAL_PHP")")"
    EXT_DIR="$PHP_DIR/lib/php/extensions"
    if [ -d "$EXT_DIR" ]; then
        exec "$PHP_BIN" -d extension_dir="$EXT_DIR" -d extension=phar.so -d extension=mbstring.so -d extension=openssl.so -d extension=curl.so /data/devstack/tools/composer/composer.phar "$@"
    fi
fi
exec php /data/devstack/tools/composer/composer.phar "$@"
`
}

// ProvisionWSLComposerFiles lays down the composer artifacts on the target
// WSL filesystem (typically accessed via UNC path or temp directory in tests).
func ProvisionWSLComposerFiles(wslDataDir string, pharData []byte, version string) error {
	if strings.TrimSpace(wslDataDir) == "" {
		return errors.New("tooling: WSL data directory is empty")
	}
	toolsDir := filepath.Join(wslDataDir, "tools", "composer")
	binDir := filepath.Join(toolsDir, "bin")
	globalBinDir := filepath.Join(wslDataDir, "bin")
	for _, dir := range []string{toolsDir, binDir, globalBinDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("tooling: create WSL directory %s: %w", dir, err)
		}
	}

	pharPath := filepath.Join(toolsDir, "composer.phar")
	if err := os.WriteFile(pharPath, pharData, 0o644); err != nil {
		return fmt.Errorf("tooling: write WSL composer.phar: %w", err)
	}

	versionPath := filepath.Join(toolsDir, "VERSION")
	if err := os.WriteFile(versionPath, []byte(strings.TrimSpace(version)+"\n"), 0o644); err != nil {
		return fmt.Errorf("tooling: write WSL composer VERSION: %w", err)
	}

	wrapperScript := []byte(WSLComposerWrapperScript())
	for _, target := range []string{
		filepath.Join(binDir, "composer"),
		filepath.Join(globalBinDir, "composer"),
	} {
		if err := os.WriteFile(target, wrapperScript, 0o755); err != nil {
			return fmt.Errorf("tooling: write WSL composer wrapper %s: %w", target, err)
		}
	}
	return nil
}

// ProvisionWSLComposer deploys Composer into the managed WSL distro.
func ProvisionWSLComposer(ctx context.Context, distro string, source composerSource, localPharPath string) error {
	if distro == "" {
		distro = "devstack"
	}
	if !wslDistroPattern.MatchString(distro) {
		return fmt.Errorf("tooling: invalid WSL distro name %q", distro)
	}
	// Locate UNC path to /data/devstack
	wslDataDir := fmt.Sprintf(`\\wsl.localhost\%s\data\devstack`, distro)
	if _, err := os.Stat(wslDataDir); err != nil {
		wslDataDir = fmt.Sprintf(`\\wsl$\%s\data\devstack`, distro)
		if _, err := os.Stat(wslDataDir); err != nil {
			return fmt.Errorf("tooling: WSL data directory not accessible: %w", err)
		}
	}

	pharData, err := os.ReadFile(localPharPath)
	if err != nil {
		return fmt.Errorf("tooling: read local composer.phar: %w", err)
	}

	if err := ProvisionWSLComposerFiles(wslDataDir, pharData, source.Version); err != nil {
		return err
	}

	// Symlink into /usr/local/bin/composer inside WSL if wsl.exe is available
	cmd := exec.CommandContext(ctx, "wsl.exe", "-d", distro, "--exec", "bash", "-c", "mkdir -p /usr/local/bin && ln -sf /data/devstack/bin/composer /usr/local/bin/composer && chmod +x /usr/local/bin/composer /data/devstack/bin/composer /data/devstack/tools/composer/bin/composer")
	platform.ConfigureProcess(cmd)
	_ = cmd.Run()
	return nil
}
