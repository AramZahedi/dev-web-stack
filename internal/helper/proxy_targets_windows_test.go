//go:build windows

package helper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestWindowsForwardersAlwaysUseLoopbackDaemonProxyPorts(t *testing.T) {
	httpListen, httpsListen, httpTarget, httpsTarget, err := windowsForwarderAddresses(
		"127.0.0.1:8080", "127.0.0.1:8443")
	if err != nil {
		t.Fatal(err)
	}
	if httpListen != "127.0.0.1:80" || httpsListen != "127.0.0.1:443" || httpTarget != "127.0.0.1:8080" || httpsTarget != "127.0.0.1:8443" {
		t.Fatalf("forwarder addresses = %q, %q -> %q, %q", httpListen, httpsListen, httpTarget, httpsTarget)
	}
}

func TestWindowsForwardersRejectNonLoopbackOrWrongDaemonTargets(t *testing.T) {
	for _, targets := range [][2]string{{"10.0.0.7:8080", "127.0.0.1:8443"}, {"127.0.0.1:8080", "127.0.0.1:9443"}} {
		if _, _, _, _, err := windowsForwarderAddresses(targets[0], targets[1]); err == nil {
			t.Errorf("accepted unsafe targets %v", targets)
		}
	}
}

type fakeWindowsForwarders struct{ starts, stops int }

func (f *fakeWindowsForwarders) Start(context.Context) error { f.starts++; return nil }
func (f *fakeWindowsForwarders) Stop(context.Context) error  { f.stops++; return nil }

func TestWindowsForwardersReconcileAndRestartWithFake(t *testing.T) {
	fake := &fakeWindowsForwarders{}
	server, err := NewServer(Config{
		AllowSID: testHelperSID,
		forwarderFactory: func(_, _, _, _ string) (windowsForwarders, error) {
			return fake, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := helperproto.ForwardersRequest{Enable: true, HTTPPort: 8080, HTTPSPort: 8443}
	if err := server.setForwarders(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := server.setForwarders(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if fake.starts != 1 {
		t.Fatalf("reconcile started %d forwarder sets; want 1", fake.starts)
	}
	if err := server.setForwarders(context.Background(), helperproto.ForwardersRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := server.setForwarders(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if fake.starts != 2 || fake.stops != 1 {
		t.Fatalf("forwarder lifecycle starts=%d stops=%d; want 2/1", fake.starts, fake.stops)
	}
}

func TestWindowsProxyTargetsPersistFixedPortsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	if err := os.WriteFile(path, []byte("allow_sid: "+testHelperSID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{AllowSID: testHelperSID, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.updateProxyTargets(context.Background(), helperproto.ProxyTargetsRequest{HTTPPort: 8080, HTTPSPort: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Persisted || response.HTTPPort != 8080 || response.HTTPSPort != 8443 {
		t.Fatalf("response = %+v", response)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"proxy_http_port: 8080", "proxy_https_port: 8443", "allow_sid: " + testHelperSID} {
		if !strings.Contains(string(data), setting) {
			t.Errorf("config lost %q: %s", setting, data)
		}
	}
}

func TestWindowsProxyTargetsRejectAnyPortsOtherThanFixedDaemonPorts(t *testing.T) {
	server, err := NewServer(Config{AllowSID: testHelperSID})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []helperproto.ProxyTargetsRequest{{HTTPPort: 9080, HTTPSPort: 9443}, {HTTPPort: 8080, HTTPSPort: 8080}} {
		if _, err := server.updateProxyTargets(context.Background(), request); err == nil {
			t.Errorf("accepted non-fixed daemon proxy ports: %+v", request)
		}
	}
}

func TestWindowsProxyPersistenceRejectsMultipleDocumentsWithoutTruncatingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	original := "allow_sid: " + testHelperSID + "\n---\nsecond_document: preserve-me\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := persistWindowsProxyTargets(path, 8080, 8443); err == nil {
		t.Fatal("proxy persistence accepted multiple YAML documents")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("config was modified after rejected update: %q", data)
	}
}
