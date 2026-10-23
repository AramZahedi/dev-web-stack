package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOptionsFlags(t *testing.T) {
	opts, err := parseOptions([]string{
		"--socket", "/tmp/helper.sock",
		"--state-dir", "/tmp/helper-state",
		"--hosts", "/tmp/hosts",
		"--trust-dir", "/tmp/trust",
		"--allow-uid", "1234",
		"--http-port", "8081",
		"--https-port", "8444",
		"--proxy-http", "9080",
		"--proxy-https", "9443",
		"--log-level", "debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.socket != "/tmp/helper.sock" ||
		opts.stateDir != "/tmp/helper-state" ||
		opts.hosts != "/tmp/hosts" ||
		opts.trustDir != "/tmp/trust" ||
		opts.allowUID != 1234 ||
		opts.httpPort != 8081 ||
		opts.httpsPort != 8444 ||
		opts.proxyHTTPPort != 9080 ||
		opts.proxyHTTPSPort != 9443 ||
		opts.logLevel != "debug" {
		t.Fatalf("options = %+v", opts)
	}
	if got, want := opts.caDir(), filepath.Join("/tmp/helper-state", "pki"); got != want {
		t.Fatalf("caDir = %q, want %q", got, want)
	}
}

func TestParseOptionsDefaults(t *testing.T) {
	opts, err := parseOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.socket != "/run/devstack-helper/helper.sock" {
		t.Fatalf("socket = %q", opts.socket)
	}
	if opts.stateDir != "/var/lib/devstack-helper" {
		t.Fatalf("state dir = %q", opts.stateDir)
	}
	if opts.hosts != "/etc/hosts" {
		t.Fatalf("hosts = %q", opts.hosts)
	}
	if opts.allowUID != os.Getuid() {
		t.Fatalf("allow uid = %d, want %d", opts.allowUID, os.Getuid())
	}
	if opts.httpPort != 80 || opts.httpsPort != 443 || opts.proxyHTTPPort != 8080 || opts.proxyHTTPSPort != 8443 {
		t.Fatalf("ports = %+v", opts)
	}
	if opts.logLevel != "info" {
		t.Fatalf("log level = %q", opts.logLevel)
	}
}

func TestParseOptionsRejectsUnknownFlag(t *testing.T) {
	if _, err := parseOptions([]string{"--nope"}); err == nil {
		t.Fatal("parseOptions accepted an unknown flag")
	}
}
