package main

import (
	"context"
	"net"
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

func TestServerStartsWithTempDirsAndHighPorts(t *testing.T) {
	dir := t.TempDir()
	hosts := filepath.Join(dir, "hosts")
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := options{
		socket:         filepath.Join(dir, "run", "helper.sock"),
		stateDir:       filepath.Join(dir, "state"),
		hosts:          hosts,
		trustDir:       filepath.Join(dir, "trust"),
		allowUID:       os.Getuid(),
		httpPort:       helperFreePort(t),
		httpsPort:      helperFreePort(t),
		proxyHTTPPort:  helperFreePort(t),
		proxyHTTPSPort: helperFreePort(t),
		logLevel:       "error",
	}
	logger, err := newLogger(opts.logLevel)
	if err != nil {
		t.Fatal(err)
	}
	server, err := newHelperServer(opts, logger)
	if err != nil {
		t.Fatalf("new helper server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatalf("start helper server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	info, err := os.Stat(opts.socket)
	if err != nil {
		t.Fatalf("stat helper socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("helper socket mode = %v", info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("helper socket permissions = %04o, want 0600", perm)
	}
	if _, err := os.Stat(filepath.Join(opts.stateDir, "pki")); err != nil {
		t.Fatalf("CA directory was not created: %v", err)
	}
}

func helperFreePort(t *testing.T) int {
	t.Helper()
	for {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		if port >= 41000 && port <= 43999 {
			continue
		}
		return port
	}
}
