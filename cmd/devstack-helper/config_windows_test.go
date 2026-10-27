//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsConfigLoadsOnlyFixedProxyTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	if err := os.WriteFile(path, []byte("allow_sid: "+allowedHelperSID+"\nproxy_http_port: 8080\nproxy_https_port: 8443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := parseWindowsOptions([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if opts.allowSID != allowedHelperSID || opts.proxyHTTPPort != 8080 || opts.proxyHTTPSPort != 8443 || opts.configPath != path {
		t.Fatalf("options = %+v", opts)
	}
	for _, invalid := range []string{
		"allow_sid: " + allowedHelperSID + "\nproxy_http_port: 9080\nproxy_https_port: 9443\n",
		"allow_sid: " + allowedHelperSID + "\nunix_socket: C:\\\\temp\\\\helper.sock\n",
	} {
		if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := parseWindowsOptions([]string{"--config", path}); err == nil {
			t.Errorf("accepted invalid Windows config %q", invalid)
		}
	}
}

func TestWindowsConfigPersistsInstallingSIDAndFixedPorts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	opts := windowsOptions{configPath: path, allowSID: allowedHelperSID, pipe: `\\.\pipe\devstack-helper`, proxyHTTPPort: 8080, proxyHTTPSPort: 8443}
	if err := persistWindowsOptions(opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{allowedHelperSID, "proxy_http_port: 8080", "proxy_https_port: 8443"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config does not contain %q: %s", want, data)
		}
	}
}

func TestWindowsConfigPersistencePreservesExistingComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	if err := os.WriteFile(path, []byte("# configured by installer\nallow_sid: S-1-5-21-1-2-3-1001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := windowsOptions{configPath: path, allowSID: allowedHelperSID, pipe: `\\.\pipe\devstack-helper`, proxyHTTPPort: 8080, proxyHTTPSPort: 8443}
	if err := persistWindowsOptions(opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# configured by installer") {
		t.Fatalf("config comment was dropped: %s", data)
	}
}
