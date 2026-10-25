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

	ForwardHTTPPort  *int  `yaml:"forward_http_port"`
	ForwardHTTPSPort *int  `yaml:"forward_https_port"`
	ProxyHTTPPort    *int  `yaml:"proxy_http_port"`
	ProxyHTTPSPort   *int  `yaml:"proxy_https_port"`
	AutoForward      *bool `yaml:"auto_forward"`

	VhostRoot        *string `yaml:"vhost_root"`
	VhostState       *string `yaml:"vhost_state"`
	VhostHTTPPort    *int    `yaml:"vhost_http_port"`
	VhostHTTPSPort   *int    `yaml:"vhost_https_port"`
	NginxIncludeDir  *string `yaml:"nginx_include_dir"`
	ApacheIncludeDir *string `yaml:"apache_include_dir"`

	ToolRoot   *string `yaml:"tool_root"`
	ToolBinDir *string `yaml:"tool_bin_dir"`

	LogLevel *string `yaml:"log_level"`
}

// findConfigPath scans args for --config without consuming or reordering the
// rest of the command line. Only one config file may be supplied; accepting
// two would make the pre-scan and flag parse disagree about which one wins.
func findConfigPath(args []string) (string, error) {
	found := ""
	seen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return found, nil
		case arg == "--config" || arg == "-config":
			if i+1 >= len(args) {
				return "", errors.New("--config requires a file path")
			}
			if seen {
				return "", errors.New("--config may be specified only once")
			}
			seen = true
			found = args[i+1]
		case arg == "--config=" || arg == "-config=":
			return "", errors.New("--config requires a file path")
		case strings.HasPrefix(arg, "--config="):
			if seen {
				return "", errors.New("--config may be specified only once")
			}
			seen = true
			found = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-config="):
			if seen {
				return "", errors.New("--config may be specified only once")
			}
			seen = true
			found = strings.TrimPrefix(arg, "-config=")
		}
	}
	return found, nil
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
	if cfg.AutoForward != nil {
		opts.autoForward = *cfg.AutoForward
	}
	if cfg.VhostRoot != nil {
		opts.vhostRoot = *cfg.VhostRoot
	}
	if cfg.VhostState != nil {
		opts.vhostState = *cfg.VhostState
	}
	if cfg.VhostHTTPPort != nil {
		opts.vhostHTTPPort = *cfg.VhostHTTPPort
	}
	if cfg.VhostHTTPSPort != nil {
		opts.vhostHTTPSPort = *cfg.VhostHTTPSPort
	}
	if cfg.NginxIncludeDir != nil {
		opts.nginxIncludeDir = *cfg.NginxIncludeDir
	}
	if cfg.ApacheIncludeDir != nil {
		opts.apacheIncludeDir = *cfg.ApacheIncludeDir
	}
	if cfg.ToolRoot != nil {
		opts.toolRoot = *cfg.ToolRoot
	}
	if cfg.ToolBinDir != nil {
		opts.toolBinDir = *cfg.ToolBinDir
	}
	if cfg.LogLevel != nil {
		opts.logLevel = *cfg.LogLevel
	}
}
