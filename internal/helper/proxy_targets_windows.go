//go:build windows

package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"go.yaml.in/yaml/v3"
)

func (s *Server) setForwarders(ctx context.Context, request helperproto.ForwardersRequest) error {
	if request.HTTPPort != 0 && request.HTTPPort != DefaultWindowsProxyHTTP {
		return fmt.Errorf("%w: Windows helper HTTP target is fixed at 127.0.0.1:%d", helperproto.ErrInvalidRequest, DefaultWindowsProxyHTTP)
	}
	if request.HTTPSPort != 0 && request.HTTPSPort != DefaultWindowsProxyHTTPS {
		return fmt.Errorf("%w: Windows helper HTTPS target is fixed at 127.0.0.1:%d", helperproto.ErrInvalidRequest, DefaultWindowsProxyHTTPS)
	}
	s.forwardersMu.Lock()
	defer s.forwardersMu.Unlock()
	if !request.Enable {
		if s.forwarders == nil {
			return nil
		}
		if err := s.forwarders.Stop(ctx); err != nil {
			return fmt.Errorf("stop Windows public forwarders: %w", err)
		}
		s.forwarders = nil
		s.forwardersKey = ""
		return nil
	}
	s.proxyMu.RLock()
	httpPort, httpsPort := s.proxyHTTPPort, s.proxyHTTPSPort
	s.proxyMu.RUnlock()
	if httpPort != DefaultWindowsProxyHTTP || httpsPort != DefaultWindowsProxyHTTPS {
		return fmt.Errorf("helper: Windows daemon proxy targets are not the fixed loopback ports")
	}
	httpListen, httpsListen, httpTarget, httpsTarget, err := windowsForwarderAddresses(
		net.JoinHostPort("127.0.0.1", strconv.Itoa(httpPort)),
		net.JoinHostPort("127.0.0.1", strconv.Itoa(httpsPort)))
	if err != nil {
		return err
	}
	key := strings.Join([]string{httpListen, httpsListen, httpTarget, httpsTarget}, "|")
	if s.forwarders != nil && s.forwardersKey == key {
		return nil
	}
	if s.forwarders != nil {
		if err := s.forwarders.Stop(ctx); err != nil {
			return fmt.Errorf("restart Windows public forwarders: %w", err)
		}
		s.forwarders = nil
		s.forwardersKey = ""
	}
	forwarders, err := s.forwarderFactory(httpListen, httpsListen, httpTarget, httpsTarget)
	if err != nil {
		return fmt.Errorf("construct Windows public forwarders: %w", err)
	}
	if err := forwarders.Start(ctx); err != nil {
		return fmt.Errorf("start Windows public forwarders: %w", err)
	}
	s.forwarders = forwarders
	s.forwardersKey = key
	return nil
}

func (s *Server) updateProxyTargets(_ context.Context, request helperproto.ProxyTargetsRequest) (helperproto.ProxyTargetsResponse, error) {
	s.proxyUpdateMu.Lock()
	defer s.proxyUpdateMu.Unlock()
	if request.HTTPPort != DefaultWindowsProxyHTTP || request.HTTPSPort != DefaultWindowsProxyHTTPS {
		return helperproto.ProxyTargetsResponse{}, fmt.Errorf("%w: Windows targets are fixed at 127.0.0.1:%d and 127.0.0.1:%d",
			helperproto.ErrInvalidRequest, DefaultWindowsProxyHTTP, DefaultWindowsProxyHTTPS)
	}
	persistErr := persistWindowsProxyTargets(s.cfg.ConfigPath, request.HTTPPort, request.HTTPSPort)
	// These values are intentionally fixed. Persistence makes restart state
	// explicit and repairs an older or partially written helper config.
	s.proxyMu.Lock()
	s.proxyHTTPPort, s.proxyHTTPSPort = DefaultWindowsProxyHTTP, DefaultWindowsProxyHTTPS
	s.proxyMu.Unlock()
	response := helperproto.ProxyTargetsResponse{
		HTTPPort: DefaultWindowsProxyHTTP, HTTPSPort: DefaultWindowsProxyHTTPS,
		Persisted: persistErr == nil, ConfigPath: s.cfg.ConfigPath,
	}
	if persistErr != nil {
		response.PersistenceError = fmt.Sprintf("persist Windows proxy targets in %s: %v", s.cfg.ConfigPath, persistErr)
	}
	return response, nil
}

func persistWindowsProxyTargets(path string, httpPort, httpsPort int) error {
	if path == "" {
		return errors.New("helper: Windows helper config path is empty")
	}
	if httpPort != DefaultWindowsProxyHTTP || httpsPort != DefaultWindowsProxyHTTPS {
		return errors.New("helper: only the fixed Windows daemon proxy ports may be persisted")
	}
	info, err := os.Lstat(path)
	var data []byte
	mode := os.FileMode(0o600)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("helper: Windows helper config must be a regular non-symlink file")
		}
		mode = info.Mode().Perm()
		data, err = os.ReadFile(path)
		if err != nil {
			return err
		}
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse Windows helper config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Windows helper config does not support multiple YAML documents")
		}
		return fmt.Errorf("parse trailing Windows helper config: %w", err)
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
	setWindowsConfigInt(root, "proxy_http_port", httpPort)
	setWindowsConfigInt(root, "proxy_https_port", httpsPort)
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return writeWindowsHelperConfig(path, output.Bytes(), mode)
}

func setWindowsConfigInt(root *yaml.Node, key string, value int) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			root.Content[i+1].Kind = yaml.ScalarNode
			root.Content[i+1].Tag = "!!int"
			root.Content[i+1].Value = strconv.Itoa(value)
			root.Content[i+1].Style = 0
			return
		}
	}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)})
}

func writeWindowsHelperConfig(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create helper config temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	remove := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		remove()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		remove()
		return err
	}
	if err := tmp.Sync(); err != nil {
		remove()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace helper config: %w", err)
	}
	return nil
}
