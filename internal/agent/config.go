package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	DefaultAgentRoot           = "/data/devstack"
	DefaultAgentPort           = 7843
	DefaultTerminalIdleSeconds = 1800
	DefaultTerminalMaxSeconds  = 14400
	maxTerminalIdleSeconds     = 86400
	maxTerminalSessionSeconds  = 604800
)

// Config is the root-owned, non-secret agent configuration. Credentials are
// loaded from the separate protected token and TLS files.
type Config struct {
	Root         string   `yaml:"root"`
	Distro       string   `yaml:"distro"`
	Version      string   `yaml:"version"`
	Port         int      `yaml:"port"`
	DistroIP     string   `yaml:"distro_ip,omitempty"`
	TokenFile    string   `yaml:"token_file,omitempty"`
	TLSDir       string   `yaml:"tls_dir,omitempty"`
	TLSCertFile  string   `yaml:"tls_cert_file,omitempty"`
	TLSKeyFile   string   `yaml:"tls_key_file,omitempty"`
	Capabilities []string `yaml:"capabilities,omitempty"`

	// TerminalIdleSeconds closes an idle site terminal and
	// TerminalMaxSeconds caps a single session. Zero selects the defaults.
	TerminalIdleSeconds int `yaml:"terminal_idle_seconds,omitempty"`
	TerminalMaxSeconds  int `yaml:"terminal_max_seconds,omitempty"`
}

// DefaultConfig returns the installed agent's standard path and listener
// configuration. DistroIP is discovered from WSL's default route at startup.
func DefaultConfig() Config {
	return Config{
		Root: DefaultAgentRoot, Distro: "devstack", Version: "dev",
		Port:        DefaultAgentPort,
		TokenFile:   filepath.Join(DefaultAgentRoot, "agent.token"),
		TLSDir:      filepath.Join(DefaultAgentRoot, "agent-tls"),
		TLSCertFile: filepath.Join(DefaultAgentRoot, "agent-tls", "agent.crt"),
		TLSKeyFile:  filepath.Join(DefaultAgentRoot, "agent-tls", "agent.key"),
	}
}

// LoadConfig reads a root-owned 0600 YAML configuration file.
func LoadConfig(path string) (Config, error) {
	if err := requirePrivateFile(path, 0o600); err != nil {
		return Config{}, fmt.Errorf("agent: config file: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("agent: read config: %w", err)
	}
	config := Config{}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("agent: decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("agent: config must contain one YAML document")
	}
	if err := config.applyDefaultsAndValidate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// WriteConfig atomically writes the non-secret config with owner-only mode.
func WriteConfig(path string, config Config) error {
	if err := config.applyDefaultsAndValidate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("agent: encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("agent: create config directory: %w", err)
	}
	return writePrivateFile(path, data, 0o600)
}

func (c *Config) applyDefaultsAndValidate() error {
	if c.Root == "" {
		c.Root = DefaultAgentRoot
	}
	if !filepath.IsAbs(c.Root) {
		return errors.New("agent: root must be absolute")
	}
	if c.Distro == "" {
		c.Distro = "devstack"
	}
	if c.Version == "" {
		c.Version = "dev"
	}
	if c.Port == 0 {
		c.Port = DefaultAgentPort
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("agent: invalid listen port %d", c.Port)
	}
	if c.TokenFile == "" {
		c.TokenFile = filepath.Join(c.Root, "agent.token")
	}
	if c.TLSDir == "" {
		c.TLSDir = filepath.Join(c.Root, "agent-tls")
	}
	if c.TLSCertFile == "" {
		c.TLSCertFile = filepath.Join(c.TLSDir, "agent.crt")
	}
	if c.TLSKeyFile == "" {
		c.TLSKeyFile = filepath.Join(c.TLSDir, "agent.key")
	}
	for label, path := range map[string]string{"token": c.TokenFile, "TLS directory": c.TLSDir, "TLS certificate": c.TLSCertFile, "TLS key": c.TLSKeyFile} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("agent: %s path must be absolute", label)
		}
		if !withinRoot(c.Root, path) {
			return fmt.Errorf("agent: %s path must remain under %s", label, c.Root)
		}
	}
	if c.TLSDir != filepath.Dir(c.TLSCertFile) || c.TLSDir != filepath.Dir(c.TLSKeyFile) {
		return errors.New("agent: TLS certificate and key must be directly inside the TLS directory")
	}
	if c.TerminalIdleSeconds == 0 {
		c.TerminalIdleSeconds = DefaultTerminalIdleSeconds
	}
	if c.TerminalIdleSeconds < 1 || c.TerminalIdleSeconds > maxTerminalIdleSeconds {
		return fmt.Errorf("agent: terminal_idle_seconds must be between 1 and %d", maxTerminalIdleSeconds)
	}
	if c.TerminalMaxSeconds == 0 {
		c.TerminalMaxSeconds = DefaultTerminalMaxSeconds
	}
	if c.TerminalMaxSeconds < 1 || c.TerminalMaxSeconds > maxTerminalSessionSeconds {
		return fmt.Errorf("agent: terminal_max_seconds must be between 1 and %d", maxTerminalSessionSeconds)
	}
	return nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
