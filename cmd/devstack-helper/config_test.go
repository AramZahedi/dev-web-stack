package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHelperConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "helper.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseOptionsReadsConfigFile(t *testing.T) {
	path := writeHelperConfig(t, `
socket: /tmp/config/helper.sock
state_dir: /tmp/config/state
hosts: /tmp/config/hosts
trust_dir: /tmp/config/trust
allow_uid: 4242
forward_http_port: 20080
forward_https_port: 20443
proxy_http_port: 21080
proxy_https_port: 21443
auto_forward: false
log_level: debug
`)

	opts, err := parseOptions([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	want := options{
		configPath:     path,
		socket:         "/tmp/config/helper.sock",
		stateDir:       "/tmp/config/state",
		hosts:          "/tmp/config/hosts",
		trustDir:       "/tmp/config/trust",
		allowUID:       4242,
		httpPort:       20080,
		httpsPort:      20443,
		proxyHTTPPort:  21080,
		proxyHTTPSPort: 21443,
		autoForward:    false,
		logLevel:       "debug",
	}
	if opts != want {
		t.Fatalf("options = %+v, want %+v", opts, want)
	}
}

func TestParseOptionsConfigEqualsForm(t *testing.T) {
	path := writeHelperConfig(t, "socket: /tmp/equals.sock\n")
	opts, err := parseOptions([]string{"--config=" + path})
	if err != nil {
		t.Fatal(err)
	}
	if opts.socket != "/tmp/equals.sock" {
		t.Fatalf("socket = %q", opts.socket)
	}
	if opts.configPath != path {
		t.Fatalf("configPath = %q, want %q", opts.configPath, path)
	}
}

func TestParseOptionsFlagsOverrideConfig(t *testing.T) {
	path := writeHelperConfig(t, `
socket: /tmp/config/helper.sock
state_dir: /tmp/config/state
allow_uid: 4242
proxy_http_port: 21080
proxy_https_port: 21443
auto_forward: false
log_level: warn
`)

	opts, err := parseOptions([]string{
		"--config", path,
		"--socket", "/tmp/flag/helper.sock",
		"--allow-uid", "77",
		"--proxy-http", "22080",
		"--auto-forward=true",
		"--log-level", "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.socket != "/tmp/flag/helper.sock" {
		t.Fatalf("socket = %q", opts.socket)
	}
	if opts.allowUID != 77 {
		t.Fatalf("allowUID = %d", opts.allowUID)
	}
	if opts.proxyHTTPPort != 22080 {
		t.Fatalf("proxyHTTPPort = %d", opts.proxyHTTPPort)
	}
	if opts.logLevel != "error" {
		t.Fatalf("logLevel = %q", opts.logLevel)
	}
	if !opts.autoForward {
		t.Fatal("autoForward = false, want the explicit flag value true")
	}
	// Omitted flags keep the values from the file.
	if opts.stateDir != "/tmp/config/state" {
		t.Fatalf("stateDir = %q", opts.stateDir)
	}
	if opts.proxyHTTPSPort != 21443 {
		t.Fatalf("proxyHTTPSPort = %d", opts.proxyHTTPSPort)
	}
}

func TestParseOptionsEmptyConfigKeepsDefaults(t *testing.T) {
	path := writeHelperConfig(t, "")
	opts, err := parseOptions([]string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if opts.socket != defaultSocket || opts.stateDir != defaultStateDir || opts.allowUID != os.Getuid() {
		t.Fatalf("defaults not applied: %+v", opts)
	}
	if !opts.autoForward {
		t.Fatal("autoForward = false, want true by default")
	}
}

func TestParseOptionsMissingConfigFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if _, err := parseOptions([]string{"--config", missing}); err == nil {
		t.Fatal("parseOptions accepted a missing config file")
	}
}

func TestParseOptionsMalformedConfigFile(t *testing.T) {
	path := writeHelperConfig(t, "socket: [\n")
	if _, err := parseOptions([]string{"--config", path}); err == nil {
		t.Fatal("parseOptions accepted malformed YAML")
	}
}

func TestParseOptionsUnknownConfigField(t *testing.T) {
	path := writeHelperConfig(t, "sockett: /tmp/typo.sock\n")
	_, err := parseOptions([]string{"--config", path})
	if err == nil {
		t.Fatal("parseOptions accepted an unknown config field")
	}
	if !strings.Contains(err.Error(), "sockett") {
		t.Fatalf("error %q does not name the unknown field", err)
	}
}

func TestParseOptionsMultipleConfigDocuments(t *testing.T) {
	path := writeHelperConfig(t, "socket: /tmp/one.sock\n---\nsocket: /tmp/two.sock\n")
	if _, err := parseOptions([]string{"--config", path}); err == nil {
		t.Fatal("parseOptions accepted multiple YAML documents")
	}
}

func TestParseOptionsConfigNeedsPath(t *testing.T) {
	for _, args := range [][]string{{"--config"}, {"--config="}} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%v) accepted an empty config path", args)
		}
	}
}

func TestParseOptionsRejectsDuplicateConfig(t *testing.T) {
	path := writeHelperConfig(t, "socket: /tmp/duplicate.sock\n")
	if _, err := parseOptions([]string{"--config", path, "--config", path}); err == nil {
		t.Fatal("parseOptions accepted duplicate --config flags")
	}
	if _, err := parseOptions([]string{"--config=" + path, "-config=" + path}); err == nil {
		t.Fatal("parseOptions accepted duplicate config flags in equals form")
	}
}

func TestParseOptionsZeroValueErrorsNameTheYAMLKey(t *testing.T) {
	tests := []struct {
		name string
		body string
		key  string
	}{
		{"socket", "socket: \"\"\n", "socket"},
		{"state dir", "state_dir: \"\"\n", "state_dir"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeHelperConfig(t, tt.body)
			_, err := parseOptions([]string{"--config", path})
			if err == nil {
				t.Fatalf("parseOptions accepted empty %s", tt.key)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("error %q does not name %s", err, tt.key)
			}
		})
	}
}

func TestFindConfigPath(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"none", []string{"--socket", "/tmp/s"}, ""},
		{"separate", []string{"--socket", "/tmp/s", "--config", "/tmp/h.yaml"}, "/tmp/h.yaml"},
		{"equals", []string{"--config=/tmp/h.yaml", "--socket", "/tmp/s"}, "/tmp/h.yaml"},
		{"single dash", []string{"-config", "/tmp/h.yaml"}, "/tmp/h.yaml"},
		{"single dash equals", []string{"-config=/tmp/h.yaml"}, "/tmp/h.yaml"},
		{"after terminator", []string{"--", "--config", "/tmp/h.yaml"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findConfigPath(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("findConfigPath(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
