//go:build !windows

package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"go.yaml.in/yaml/v3"
)

func TestUpdateProxyTargetsPersistsAndUpdatesMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	original := `# keep this comment
socket: /run/devstack-helper/helper.sock
proxy_http_port: 8080
proxy_https_port: 8443
# forwarders stay privileged
forward_http_port: 80
forward_https_port: 443
`
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: Config{
		ForwardHTTPPort: 18080, ForwardHTTPSPort: 18443,
		ProxyHTTPPort: 19080, ProxyHTTPSPort: 19443,
		ProxyConfigPath: path,
	}}

	resp, err := server.updateProxyTargets(context.Background(), helperproto.ProxyTargetsRequest{
		HTTPPort: 20080, HTTPSPort: 20443,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Persisted || resp.ConfigPath != path {
		t.Fatalf("response = %+v", resp)
	}
	httpListen, httpsListen, httpTarget, httpsTarget, err := server.forwarderPorts()
	if err != nil {
		t.Fatal(err)
	}
	if httpListen != 18080 || httpsListen != 18443 || httpTarget != 20080 || httpsTarget != 20443 {
		t.Fatalf("ports = %d/%d -> %d/%d", httpListen, httpsListen, httpTarget, httpsTarget)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# keep this comment", "# forwarders stay privileged", "proxy_http_port: 20080", "proxy_https_port: 20443"} {
		if !strings.Contains(text, want) {
			t.Fatalf("persisted config missing %q:\n%s", want, text)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %o, want 640", got)
	}
	var decoded map[string]any
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("persisted YAML is invalid: %v", err)
	}
	if decoded["proxy_http_port"] != 20080 || decoded["proxy_https_port"] != 20443 {
		t.Fatalf("decoded targets = %v", decoded)
	}
	if decoded["forward_http_port"] != 80 || decoded["forward_https_port"] != 443 {
		t.Fatalf("privileged ports changed: %v", decoded)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".helper.yaml.tmp-*")); len(matches) != 0 {
		t.Fatalf("temporary files left behind: %v", matches)
	}
}

func TestUpdateProxyTargetsAddsMissingKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	if err := os.WriteFile(path, []byte("socket: /run/helper.sock\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: Config{
		ForwardHTTPPort: 18080, ForwardHTTPSPort: 18443,
		ProxyConfigPath: path,
	}}
	if _, err := server.updateProxyTargets(context.Background(), helperproto.ProxyTargetsRequest{
		HTTPPort: 20080, HTTPSPort: 20443,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"socket: /run/helper.sock", "proxy_http_port: 20080", "proxy_https_port: 20443"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("persisted config missing %q:\n%s", want, data)
		}
	}
}

func TestUpdateProxyTargetsSkipsUnchangedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	original := []byte("# keep blank-line formatting\n\nproxy_http_port: 20080\nproxy_https_port: 20443\n\nforward_http_port: 18080\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: Config{
		ForwardHTTPPort: 18080, ForwardHTTPSPort: 18443,
		ProxyConfigPath: path,
	}}
	if _, err := server.updateProxyTargets(context.Background(), helperproto.ProxyTargetsRequest{
		HTTPPort: 20080, HTTPSPort: 20443,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Fatalf("unchanged targets rewrote the file:\n%s", data)
	}
}

func TestUpdateProxyTargetsPreservesConfigSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-helper.yaml")
	if err := os.WriteFile(target, []byte("proxy_http_port: 8080\nproxy_https_port: 8443\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "helper.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: Config{
		ForwardHTTPPort: 18080, ForwardHTTPSPort: 18443,
		ProxyConfigPath: link,
	}}
	if _, err := server.updateProxyTargets(context.Background(), helperproto.ProxyTargetsRequest{
		HTTPPort: 20080, HTTPSPort: 20443,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symlink was replaced by %v", info.Mode())
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy_http_port: 20080") ||
		!strings.Contains(string(data), "proxy_https_port: 20443") {
		t.Fatalf("symlink target was not rewritten:\n%s", data)
	}
}

func TestUpdateProxyTargetsWithoutConfigPathUpdatesMemoryOnly(t *testing.T) {
	server := &Server{cfg: Config{
		ForwardHTTPPort: 18080, ForwardHTTPSPort: 18443,
		ProxyHTTPPort: 19080, ProxyHTTPSPort: 19443,
	}}
	resp, err := server.updateProxyTargets(context.Background(), helperproto.ProxyTargetsRequest{
		HTTPPort: 20080, HTTPSPort: 20443,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Persisted {
		t.Fatalf("response reported persistence without a config path: %+v", resp)
	}
	_, _, httpTarget, httpsTarget, err := server.forwarderPorts()
	if err != nil {
		t.Fatal(err)
	}
	if httpTarget != 20080 || httpsTarget != 20443 {
		t.Fatalf("targets = %d/%d", httpTarget, httpsTarget)
	}
}

func TestUpdateProxyTargetsRejectsInvalidPortsBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	original := []byte("proxy_http_port: 8080\nproxy_https_port: 8443\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: Config{
		ForwardHTTPPort: 18080, ForwardHTTPSPort: 18443,
		ProxyHTTPPort: 19080, ProxyHTTPSPort: 19443,
		ProxyConfigPath: path,
	}}
	cases := []helperproto.ProxyTargetsRequest{
		{HTTPPort: 20080, HTTPSPort: 20080},
		{HTTPPort: 18080, HTTPSPort: 20443},
		{HTTPPort: 20443, HTTPSPort: 18080},
		{HTTPPort: 0, HTTPSPort: 20443},
		{HTTPPort: 70000, HTTPSPort: 20443},
	}
	for _, req := range cases {
		if _, err := server.updateProxyTargets(context.Background(), req); !errors.Is(err, helperproto.ErrInvalidRequest) {
			t.Fatalf("request %+v error = %v, want ErrInvalidRequest", req, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(original) {
			t.Fatalf("invalid request %+v changed the config:\n%s", req, data)
		}
		_, _, httpTarget, httpsTarget, err := server.forwarderPorts()
		if err != nil {
			t.Fatal(err)
		}
		if httpTarget != 19080 || httpsTarget != 19443 {
			t.Fatalf("invalid request %+v changed memory targets to %d/%d", req, httpTarget, httpsTarget)
		}
	}
}

func TestUpdateProxyTargetsReportsPersistenceErrorsButMovesMemory(t *testing.T) {
	server := &Server{cfg: Config{
		ForwardHTTPPort: 18080, ForwardHTTPSPort: 18443,
		ProxyHTTPPort: 19080, ProxyHTTPSPort: 19443,
		ProxyConfigPath: filepath.Join(t.TempDir(), "missing.yaml"),
	}}
	resp, err := server.updateProxyTargets(context.Background(), helperproto.ProxyTargetsRequest{
		HTTPPort: 20080, HTTPSPort: 20443,
	})
	if err != nil {
		t.Fatalf("persistence failure returned an RPC error: %v", err)
	}
	if resp.Persisted || resp.PersistenceError == "" || resp.HTTPPort != 20080 || resp.HTTPSPort != 20443 {
		t.Fatalf("response = %+v", resp)
	}
	_, _, httpTarget, httpsTarget, portErr := server.forwarderPorts()
	if portErr != nil {
		t.Fatal(portErr)
	}
	if httpTarget != 20080 || httpsTarget != 20443 {
		t.Fatalf("failed persistence left memory targets at %d/%d", httpTarget, httpsTarget)
	}
}
