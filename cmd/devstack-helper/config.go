package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// fileConfig is the on-disk helper configuration read by --config. Pointer
// fields distinguish an omitted key from an explicit empty value or zero, so
// a partial file keeps the built-in defaults for everything it omits.
//
// The systemd unit always passes --config /etc/devstack/helper.yaml. Command
// line flags are parsed after the file and therefore override it.
type fileConfig struct {
	Socket   *string `yaml:"socket"`
	StateDir *string `yaml:"state_dir"`
	Hosts    *string `yaml:"hosts"`
	TrustDir *string `yaml:"trust_dir"`
	AllowUID *int    `yaml:"allow_uid"`

	ForwardHTTPPort  *int `yaml:"forward_http_port"`
	ForwardHTTPSPort *int `yaml:"forward_https_port"`
	ProxyHTTPPort    *int `yaml:"proxy_http_port"`
	ProxyHTTPSPort   *int `yaml:"proxy_https_port"`

	LogLevel *string `yaml:"log_level"`
}

// findConfigPath scans args for --config without consuming or reordering the
// rest of the command line. The actual flag parse still validates the value
// and rejects unknown flags.
func findConfigPath(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return "", nil
		case arg == "--config" || arg == "-config":
			if i+1 >= len(args) {
				return "", errors.New("--config requires a file path")
			}
			return args[i+1], nil
		case arg == "--config=" || arg == "-config=":
			return "", errors.New("--config requires a file path")
		case strings.HasPrefix(arg, "--config="):
			return strings.TrimPrefix(arg, "--config="), nil
		case strings.HasPrefix(arg, "-config="):
			return strings.TrimPrefix(arg, "-config="), nil
		}
	}
	return "", nil
}

// loadFileConfig reads and strictly decodes a YAML helper config. An empty
// file is valid and leaves every default in place. Unknown keys are rejected
// so a typo in the privileged config fails at startup instead of being ignored.
func loadFileConfig(path string) (fileConfig, error) {
	var cfg fileConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return fileConfig{}, nil
		}
		return fileConfig{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fileConfig{}, fmt.Errorf("parse config %s: multiple YAML documents are not supported", path)
		}
		return fileConfig{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// apply copies the keys present in the file over opts. Omitted keys do not
// change opts.
func (cfg fileConfig) apply(opts *options) {
	if cfg.Socket != nil {
		opts.socket = *cfg.Socket
	}
	if cfg.StateDir != nil {
		opts.stateDir = *cfg.StateDir
	}
	if cfg.Hosts != nil {
		opts.hosts = *cfg.Hosts
	}
	if cfg.TrustDir != nil {
		opts.trustDir = *cfg.TrustDir
	}
	if cfg.AllowUID != nil {
		opts.allowUID = *cfg.AllowUID
	}
	if cfg.ForwardHTTPPort != nil {
		opts.httpPort = *cfg.ForwardHTTPPort
	}
	if cfg.ForwardHTTPSPort != nil {
		opts.httpsPort = *cfg.ForwardHTTPSPort
	}
	if cfg.ProxyHTTPPort != nil {
		opts.proxyHTTPPort = *cfg.ProxyHTTPPort
	}
	if cfg.ProxyHTTPSPort != nil {
		opts.proxyHTTPSPort = *cfg.ProxyHTTPSPort
	}
	if cfg.LogLevel != nil {
		opts.logLevel = *cfg.LogLevel
	}
}
