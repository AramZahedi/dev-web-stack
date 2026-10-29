// Package config manages the machine-wide devstack settings and the on-disk
// layout under $DEVSTACK_HOME.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/platform"
	"go.yaml.in/yaml/v3"
)

// wslDistroPattern is the same shape the WSL tooling accepts. Validating it
// here keeps a malformed config from reaching wsl.exe arguments or UNC paths.
var wslDistroPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Backend port ranges reserved for site upstreams and FastCGI pools. They are
// allocated by the daemon, so they must never appear in config.yaml.
const (
	reservedPortLow  = 41000
	reservedPortHigh = 43999
)

// Serving modes. Exactly one mode is active; builtin keeps the Go proxy and
// external modes delegate to the privileged helper's nginx or Apache vhosts.
const (
	ServingModeBuiltin = "builtin"
	ServingModeNginx   = "nginx"
	ServingModeApache  = "apache"
)

// Serving providers. Native keeps the existing host-installed web servers;
// docker serves through the managed nginx or Apache service containers.
const (
	ServingProviderNative = "native"
	ServingProviderDocker = "docker"
)

// ValidServingMode reports whether mode is one of the supported serving modes.
func ValidServingMode(mode string) bool {
	switch mode {
	case ServingModeBuiltin, ServingModeNginx, ServingModeApache:
		return true
	default:
		return false
	}
}

// ValidServingProvider reports whether provider is one of the supported
// serving providers.
func ValidServingProvider(provider string) bool {
	switch provider {
	case ServingProviderNative, ServingProviderDocker:
		return true
	default:
		return false
	}
}

// Config holds the machine-level settings stored in config.yaml.
type Config struct {
	// Home is the resolved machine root. It is not stored in config.yaml.
	Home string `yaml:"-"`

	HTTPPort  int `yaml:"http_port"`
	HTTPSPort int `yaml:"https_port"`
	UIPort    int `yaml:"ui_port"`
	AgentPort int `yaml:"agent_port"`

	WSLDistro       string `yaml:"wsl_distro"`
	ProjectsRoot    string `yaml:"projects_root"`
	AutoStartSites  bool   `yaml:"auto_start_sites"`
	ServingMode     string `yaml:"serving_mode"`
	ServingProvider string `yaml:"serving_provider"`

	GracefulStopSeconds int `yaml:"graceful_stop_seconds"`
	MaxRequestBodyMB    int `yaml:"max_request_body_mb"`
	LogMaxMB            int `yaml:"log_max_mb"`
	LogKeep             int `yaml:"log_keep"`
}

// Default returns the built-in machine configuration. Home is left empty; Load
// fills it in.
func Default() Config {
	return Config{
		HTTPPort:            8080,
		HTTPSPort:           8443,
		UIPort:              7070,
		AgentPort:           7443,
		WSLDistro:           "devstack",
		AutoStartSites:      true,
		ServingMode:         ServingModeBuiltin,
		ServingProvider:     ServingProviderNative,
		GracefulStopSeconds: 10,
		MaxRequestBodyMB:    1024,
		LogMaxMB:            10,
		LogKeep:             5,
	}
}

// Load resolves the machine root, ensures the directory layout exists, and
// reads config.yaml. A missing file is created with Default values. Fields
// absent from an existing file keep their default values. An explicit home
// takes precedence over $DEVSTACK_HOME; otherwise the environment variable is
// used, and finally ~/.devstack.
func Load(home string) (Config, error) {
	resolved, err := resolveHome(home)
	if err != nil {
		return Config{}, err
	}

	paths := NewPaths(resolved)
	if err := paths.Ensure(); err != nil {
		return Config{}, err
	}

	cfg := Default()
	cfg.Home = resolved
	cfg.ProjectsRoot = platform.Join(runtime.GOOS, resolved, "projects")

	data, err := os.ReadFile(paths.Config)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse %s: %w", paths.Config, err)
		}
		cfg.Home = resolved
		if cfg.ProjectsRoot == "" {
			cfg.ProjectsRoot = platform.Join(runtime.GOOS, resolved, "projects")
		}
	case errors.Is(err, os.ErrNotExist):
		if err := cfg.Save(); err != nil {
			return Config{}, err
		}
	default:
		return Config{}, fmt.Errorf("read %s: %w", paths.Config, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", paths.Config, err)
	}
	return cfg, nil
}

// Validate checks that the configured ports are usable and non-overlapping and
// that the stop timeout is positive.
func (c Config) Validate() error {
	ports := []struct {
		name string
		port int
	}{
		{"http_port", c.HTTPPort},
		{"https_port", c.HTTPSPort},
		{"ui_port", c.UIPort},
		{"agent_port", c.AgentPort},
	}
	seen := make(map[int]string, len(ports))
	for _, p := range ports {
		if p.port < 1 || p.port > 65535 {
			return fmt.Errorf("%s %d must be between 1 and 65535", p.name, p.port)
		}
		if p.port >= reservedPortLow && p.port <= reservedPortHigh {
			return fmt.Errorf("%s %d is inside the reserved backend range %d-%d", p.name, p.port, reservedPortLow, reservedPortHigh)
		}
		if other, ok := seen[p.port]; ok {
			return fmt.Errorf("%s and %s must differ, both are %d", other, p.name, p.port)
		}
		seen[p.port] = p.name
	}
	if c.GracefulStopSeconds <= 0 {
		return fmt.Errorf("graceful_stop_seconds must be greater than 0, got %d", c.GracefulStopSeconds)
	}
	distro := strings.TrimSpace(c.WSLDistro)
	if !wslDistroPattern.MatchString(distro) {
		return fmt.Errorf("wsl_distro %q must start with a letter or digit and contain only letters, digits, '.', '_' or '-'", c.WSLDistro)
	}
	// logs.New treats zero as a test-only rotate-on-every-write hook. A
	// persisted config must never select that mode accidentally.
	if c.LogMaxMB < 1 || c.LogMaxMB > 1<<20 {
		return fmt.Errorf("log_max_mb must be between 1 and %d, got %d", 1<<20, c.LogMaxMB)
	}
	if c.LogKeep < 0 || c.LogKeep > 10000 {
		return fmt.Errorf("log_keep must be between 0 and 10000, got %d", c.LogKeep)
	}
	if c.MaxRequestBodyMB < 1 || c.MaxRequestBodyMB > 1<<20 {
		return fmt.Errorf("max_request_body_mb must be between 1 and %d, got %d", 1<<20, c.MaxRequestBodyMB)
	}
	if !ValidServingMode(c.ServingMode) {
		return fmt.Errorf("serving_mode %q must be one of %s, %s, or %s", c.ServingMode,
			ServingModeBuiltin, ServingModeNginx, ServingModeApache)
	}
	if !ValidServingProvider(c.ServingProvider) {
		return fmt.Errorf("serving_provider %q must be one of %s or %s", c.ServingProvider,
			ServingProviderNative, ServingProviderDocker)
	}
	if c.ServingProvider == ServingProviderDocker && c.ServingMode == ServingModeBuiltin {
		return fmt.Errorf("serving_provider %s requires serving_mode %s or %s, got %s",
			ServingProviderDocker, ServingModeNginx, ServingModeApache, c.ServingMode)
	}
	if c.ProjectsRoot != "" && !filepath.IsAbs(c.ProjectsRoot) {
		return fmt.Errorf("projects_root %q must be an absolute path", c.ProjectsRoot)
	}
	return nil
}

// Save validates the configuration and atomically writes config.yaml with mode
// 0600. The file is written to a temporary file in the machine root and then
// renamed into place, so a crash never leaves a partially written config.
func (c Config) Save() error {
	if c.Home == "" {
		return errors.New("config home is not set")
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to save invalid config: %w", err)
	}
	if err := os.MkdirAll(c.Home, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", c.Home, err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp, err := os.CreateTemp(c.Home, "config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best effort: remove the temporary file if it was not renamed.
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set config mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(c.Home, "config.yaml")); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	tmpName = ""
	return nil
}

// resolveHome applies the override precedence and returns an absolute path:
// explicit home, DEVSTACK_HOME, a detected legacy ~/.devstack, then the
// platform default. Legacy Linux installs are never moved.
func resolveHome(home string) (string, error) {
	if strings.TrimSpace(home) == "" && strings.TrimSpace(os.Getenv("DEVSTACK_HOME")) == "" {
		if legacy, ok := platform.LegacyWindowsRoot(); ok {
			home = legacy
		}
	}
	userHome, _ := os.UserHomeDir()
	return resolveHomeFrom(home, os.Getenv("DEVSTACK_HOME"), userHome, func(candidate string) bool {
		info, err := os.Stat(candidate)
		return err == nil && info.IsDir()
	})
}

func resolveHomeFrom(home, envHome, userHome string, legacyExists func(string) bool) (string, error) {
	home = strings.TrimSpace(home)
	if home == "" {
		home = strings.TrimSpace(envHome)
	}
	if home == "" && userHome != "" && legacyExists != nil {
		legacy := filepath.Join(userHome, ".devstack")
		if legacyExists(legacy) {
			home = legacy
		}
	}
	if home == "" {
		home = platform.DefaultRoot(runtime.GOOS)
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve devstack home %q: %w", home, err)
	}
	return abs, nil
}
