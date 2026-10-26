package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"go.yaml.in/yaml/v3"
)

// updateProxyTargets updates the loopback targets used by the privileged
// forwarders. The privileged listen ports never change here. The helper socket
// is already restricted to AllowUID; this RPC cannot choose a listen address,
// bind a privileged port, or forward non-loopback traffic. When the helper
// has a writable configuration file, the two keys are persisted atomically
// before the in-memory targets change, so a helper restart keeps forwarding
// toward the daemon's current ports. A write failure is reported in the
// response after the live targets move so an already-running daemon can still
// reconcile 80/443.
func (s *Server) updateProxyTargets(_ context.Context, req helperproto.ProxyTargetsRequest) (helperproto.ProxyTargetsResponse, error) {
	// Serialize the file write and in-memory swap so two same-UID callers
	// cannot leave the restart config and running targets disagreeing.
	s.proxyUpdateMu.Lock()
	defer s.proxyUpdateMu.Unlock()

	httpListen, httpsListen := s.forwardListenPorts()
	if err := validateForwarderPorts(httpListen, httpsListen, req.HTTPPort, req.HTTPSPort); err != nil {
		return helperproto.ProxyTargetsResponse{}, fmt.Errorf("%w: %v", helperproto.ErrInvalidRequest, err)
	}

	var persistErr error
	if path := s.cfg.ProxyConfigPath; path != "" {
		persistErr = persistProxyTargets(path, req.HTTPPort, req.HTTPSPort)
	}

	// Move the live targets even when persistence fails. Serving-mode
	// reconciliation runs next and must not be left pointing at the old, now
	// dead daemon port. The response still reports the write failure so the
	// daemon logs that a helper restart would need a manual config repair.
	s.proxyMu.Lock()
	s.proxyHTTPPort = req.HTTPPort
	s.proxyHTTPSPort = req.HTTPSPort
	s.proxyMu.Unlock()

	response := helperproto.ProxyTargetsResponse{
		HTTPPort:   req.HTTPPort,
		HTTPSPort:  req.HTTPSPort,
		Persisted:  persistErr == nil && s.cfg.ProxyConfigPath != "",
		ConfigPath: s.cfg.ProxyConfigPath,
	}
	if persistErr != nil {
		response.PersistenceError = fmt.Sprintf("persist proxy targets in %s: %v", s.cfg.ProxyConfigPath, persistErr)
	}
	return response, nil
}

// persistProxyTargets rewrites only the two proxy target keys in the helper
// YAML. Comments and unrelated keys are preserved by editing the yaml.Node
// tree instead of decoding into a fixed struct and re-marshaling defaults.
func persistProxyTargets(path string, httpPort, httpsPort int) error {
	// Follow a configuration symlink and rewrite its target. Renaming over the
	// symlink itself would silently turn a config-management link into a
	// regular file.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("configuration path is not a regular file")
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}

	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("parse YAML: %w", err)
	}
	if len(document.Content) == 0 {
		document.Kind = yaml.DocumentNode
		document.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := document.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Value = ""
	}
	if root.Kind != yaml.MappingNode {
		return errors.New("configuration root is not a YAML mapping")
	}
	existingHTTP, haveHTTP := mapInt(root, "proxy_http_port")
	existingHTTPS, haveHTTPS := mapInt(root, "proxy_https_port")
	if haveHTTP && haveHTTPS && existingHTTP == httpPort && existingHTTPS == httpsPort {
		return nil
	}
	setMappingInt(root, "proxy_http_port", httpPort)
	setMappingInt(root, "proxy_https_port", httpsPort)

	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("encode YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("encode YAML: %w", err)
	}
	return writeAtomicFile(resolved, out.Bytes(), info.Mode().Perm())
}

func mapInt(mapping *yaml.Node, key string) (int, bool) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		value, err := strconv.Atoi(mapping.Content[i+1].Value)
		return value, err == nil
	}
	return 0, false
}

func setMappingInt(mapping *yaml.Node, key string, value int) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			node := mapping.Content[i+1]
			node.Kind = yaml.ScalarNode
			node.Tag = "!!int"
			node.Value = strconv.Itoa(value)
			node.Style = 0
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)},
	)
}
