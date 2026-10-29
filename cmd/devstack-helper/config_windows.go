//go:build windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/helper"
	"go.yaml.in/yaml/v3"
)

type windowsFileConfig struct {
	AllowSID       *string `yaml:"allow_sid"`
	Pipe           *string `yaml:"pipe"`
	SystemDir      *string `yaml:"system_dir"`
	CADir          *string `yaml:"ca_dir"`
	RootCert       *string `yaml:"root_cert"`
	ProxyHTTPPort  *int    `yaml:"proxy_http_port"`
	ProxyHTTPSPort *int    `yaml:"proxy_https_port"`
}

func loadWindowsFileConfig(path string) (windowsFileConfig, error) {
	var cfg windowsFileConfig
	info, err := os.Lstat(path)
	if err != nil {
		return cfg, fmt.Errorf("read helper config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return cfg, fmt.Errorf("read helper config %s: Windows helper config must be a regular non-symlink file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read helper config %s: %w", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return windowsFileConfig{}, nil
		}
		return windowsFileConfig{}, fmt.Errorf("parse helper config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return windowsFileConfig{}, fmt.Errorf("parse helper config %s: multiple YAML documents are not supported", path)
	}
	return cfg, nil
}

func (cfg windowsFileConfig) apply(opts *windowsOptions) {
	if cfg.AllowSID != nil {
		opts.allowSID = *cfg.AllowSID
	}
	if cfg.Pipe != nil {
		opts.pipe = *cfg.Pipe
	}
	if cfg.SystemDir != nil {
		opts.systemDir = *cfg.SystemDir
	}
	if cfg.CADir != nil {
		opts.caDir = *cfg.CADir
	}
	if cfg.RootCert != nil {
		opts.rootCert = *cfg.RootCert
	}
	if cfg.ProxyHTTPPort != nil {
		opts.proxyHTTPPort = *cfg.ProxyHTTPPort
	}
	if cfg.ProxyHTTPSPort != nil {
		opts.proxyHTTPSPort = *cfg.ProxyHTTPSPort
	}
}

func configPathFromWindowsArgs(args []string) (string, error) {
	path := helper.DefaultWindowsConfigPath
	seen := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--config":
			if seen || i+1 >= len(args) {
				return "", errors.New("--config requires exactly one file path")
			}
			seen = true
			path = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--config="):
			if seen || strings.TrimPrefix(args[i], "--config=") == "" {
				return "", errors.New("--config requires exactly one file path")
			}
			seen = true
			path = strings.TrimPrefix(args[i], "--config=")
		}
	}
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", errors.New("helper config path must be absolute")
	}
	return path, nil
}

func persistWindowsOptions(opts windowsOptions) error {
	if opts.configPath == "" || opts.allowSID == "" || opts.proxyHTTPPort != helper.DefaultWindowsProxyHTTP || opts.proxyHTTPSPort != helper.DefaultWindowsProxyHTTPS {
		return errors.New("invalid Windows helper configuration")
	}
	if _, err := helper.NewServer(helper.Config{AllowSID: opts.allowSID, PipeName: opts.pipe, ConfigPath: opts.configPath,
		ProxyHTTPPort: opts.proxyHTTPPort, ProxyHTTPSPort: opts.proxyHTTPSPort}); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	var existing []byte
	if info, err := os.Lstat(opts.configPath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Windows helper config must be a regular non-symlink file")
		}
		mode = info.Mode().Perm()
		existing, err = os.ReadFile(opts.configPath)
		if err != nil {
			return err
		}
		if _, err := loadWindowsFileConfig(opts.configPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(existing))
	if err := decoder.Decode(&document); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse helper config for update: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Windows helper config does not support multiple YAML documents")
		}
		return fmt.Errorf("parse trailing helper config: %w", err)
	}
	if len(document.Content) == 0 {
		document.Kind = yaml.DocumentNode
		document.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := document.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		root.Kind, root.Tag, root.Value = yaml.MappingNode, "!!map", ""
	}
	if root.Kind != yaml.MappingNode {
		return errors.New("Windows helper config root is not a YAML mapping")
	}
	setWindowsConfigString(root, "allow_sid", opts.allowSID)
	setWindowsConfigString(root, "pipe", opts.pipe)
	if opts.systemDir != "" {
		setWindowsConfigString(root, "system_dir", opts.systemDir)
	}
	if opts.caDir != "" {
		setWindowsConfigString(root, "ca_dir", opts.caDir)
	}
	if opts.rootCert != "" {
		setWindowsConfigString(root, "root_cert", opts.rootCert)
	}
	setWindowsConfigInt(root, "proxy_http_port", opts.proxyHTTPPort)
	setWindowsConfigInt(root, "proxy_https_port", opts.proxyHTTPSPort)
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return writeWindowsOptionsFile(opts.configPath, output.Bytes(), mode)
}

func setWindowsConfigString(root *yaml.Node, key, value string) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1].Kind = yaml.ScalarNode
			root.Content[i+1].Tag = "!!str"
			root.Content[i+1].Value = value
			root.Content[i+1].Style = 0
			return
		}
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func setWindowsConfigInt(root *yaml.Node, key string, value int) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1].Kind = yaml.ScalarNode
			root.Content[i+1].Tag = "!!int"
			root.Content[i+1].Value = fmt.Sprint(value)
			root.Content[i+1].Style = 0
			return
		}
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(value)})
}

func writeWindowsOptionsFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create helper config directory: %w", err)
	}
	file, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create helper config temporary file: %w", err)
	}
	tmpPath := file.Name()
	cleanup := func() { _ = file.Close(); _ = os.Remove(tmpPath) }
	if _, err := file.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace helper config: %w", err)
	}
	return nil
}
