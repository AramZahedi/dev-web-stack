package tooling

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var (
	wslDistroPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	envNamePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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
	for _, tool := range []string{"php", "composer"} {
		path := filepath.Join(binDir, tool+".cmd")
		content := fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d \"%s\" --exec %s %%*\r\nexit /b %%ERRORLEVEL%%\r\n", distro, tool)
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

	shims := map[string]string{
		"php.cmd":      fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d \"%s\" -- bash -lc \"%s && exec php \\\"$@\\\"\" -- %%*\r\nexit /b %%ERRORLEVEL%%\r\n", distro, baseScript.String()),
		"composer.cmd": fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d \"%s\" -- bash -lc \"%s && exec composer \\\"$@\\\"\" -- %%*\r\nexit /b %%ERRORLEVEL%%\r\n", distro, baseScript.String()),
		"shell.cmd":    fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d \"%s\" -- bash -lc \"%s && exec \\\"${SHELL:-/bin/bash}\\\" -i\"\r\nexit /b %%ERRORLEVEL%%\r\n", distro, baseScript.String()),
		"exec.cmd":     fmt.Sprintf("@echo off\r\nsetlocal DisableDelayedExpansion\r\nwsl.exe -d \"%s\" -- bash -lc \"%s && exec \\\"$@\\\"\" -- %%*\r\nexit /b %%ERRORLEVEL%%\r\n", distro, baseScript.String()),
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
	return EnsureWindowsToolShims(filepath.Join(home, "bin"), distro)
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

// WindowsSiteEnvironment builds the small, Linux-native environment that the
// Windows CLI passes into WSL. Runtime-specific setup remains agent-owned.
func WindowsSiteEnvironment(site model.Site) map[string]string {
	values := make(map[string]string, len(site.Env)+6)
	for key, value := range site.Env {
		values[key] = value
	}
	values["DEVSTACK_SITE"] = site.Name
	values["DEVSTACK_HOME"] = "/data/devstack"
	values["DEVSTACK_PROJECT"] = site.BackendPath
	values["PATH"] = "/data/devstack/tools/sites/" + site.Name + "/bin:/data/devstack/bin:/usr/local/bin:/usr/bin:/bin"
	if site.PHPBuild != "" {
		values["DEVSTACK_PHP_BUILD"] = site.PHPBuild
	}
	if site.NodeVersion != "" {
		values["DEVSTACK_NODE_VERSION"] = site.NodeVersion
	}
	return values
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
