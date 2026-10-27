//go:build windows

package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/winfirewall"
	"github.com/AramZahedi/dev-web-stack/internal/wintrust"
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

func TestWindowsForwardersHTTPHandler(t *testing.T) {
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

	// 1. Enable forwarders with valid fixed ports 8080/8443
	reqBody, _ := json.Marshal(helperproto.ForwardersRequest{Enable: true, HTTPPort: 8080, HTTPSPort: 8443})
	req := httptest.NewRequest(http.MethodPost, helperproto.PathForwarders, bytes.NewReader(reqBody))
	req.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	req = req.WithContext(context.WithValue(req.Context(), peerSIDKey{}, testHelperSID))
	w := httptest.NewRecorder()
	server.handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if fake.starts != 1 {
		t.Fatalf("expected 1 forwarder start, got %d", fake.starts)
	}

	// 2. Reject non-loopback / non-fixed port forwarders request
	badBody, _ := json.Marshal(helperproto.ForwardersRequest{Enable: true, HTTPPort: 9080, HTTPSPort: 8443})
	badReq := httptest.NewRequest(http.MethodPost, helperproto.PathForwarders, bytes.NewReader(badBody))
	badReq.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	badReq = badReq.WithContext(context.WithValue(badReq.Context(), peerSIDKey{}, testHelperSID))
	w = httptest.NewRecorder()
	server.handler().ServeHTTP(w, badReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for non-fixed forwarder port, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Disable forwarders
	disableBody, _ := json.Marshal(helperproto.ForwardersRequest{Enable: false})
	disableReq := httptest.NewRequest(http.MethodPost, helperproto.PathForwarders, bytes.NewReader(disableBody))
	disableReq.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	disableReq = disableReq.WithContext(context.WithValue(disableReq.Context(), peerSIDKey{}, testHelperSID))
	w = httptest.NewRecorder()
	server.handler().ServeHTTP(w, disableReq)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content on disable, got %d: %s", w.Code, w.Body.String())
	}
	if fake.stops != 1 {
		t.Fatalf("expected 1 forwarder stop, got %d", fake.stops)
	}
}

func TestWindowsProxyTargetsHTTPHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "helper.yaml")
	if err := os.WriteFile(path, []byte("allow_sid: "+testHelperSID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{AllowSID: testHelperSID, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}

	// Valid update: 8080 and 8443
	reqBody, _ := json.Marshal(helperproto.ProxyTargetsRequest{HTTPPort: 8080, HTTPSPort: 8443})
	req := httptest.NewRequest(http.MethodPost, helperproto.PathProxyTargets, bytes.NewReader(reqBody))
	req.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	req = req.WithContext(context.WithValue(req.Context(), peerSIDKey{}, testHelperSID))
	w := httptest.NewRecorder()
	server.handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
	var resp helperproto.ProxyTargetsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Persisted || resp.HTTPPort != 8080 || resp.HTTPSPort != 8443 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy_http_port: 8080") || !strings.Contains(string(data), "proxy_https_port: 8443") {
		t.Fatalf("file not updated properly: %s", string(data))
	}

	// Invalid update: non-fixed ports
	badBody, _ := json.Marshal(helperproto.ProxyTargetsRequest{HTTPPort: 9080, HTTPSPort: 8443})
	badReq := httptest.NewRequest(http.MethodPost, helperproto.PathProxyTargets, bytes.NewReader(badBody))
	badReq.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	badReq = badReq.WithContext(context.WithValue(badReq.Context(), peerSIDKey{}, testHelperSID))
	w = httptest.NewRecorder()
	server.handler().ServeHTTP(w, badReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for non-fixed proxy targets, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWindowsForwardersStopOnServerCloseAndUninstall(t *testing.T) {
	// Test forwarders stop on Close
	fake1 := &fakeWindowsForwarders{}
	server1, err := NewServer(Config{
		AllowSID: testHelperSID,
		forwarderFactory: func(_, _, _, _ string) (windowsForwarders, error) {
			return fake1, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server1.setForwarders(context.Background(), helperproto.ForwardersRequest{Enable: true, HTTPPort: 8080, HTTPSPort: 8443}); err != nil {
		t.Fatal(err)
	}
	if fake1.starts != 1 {
		t.Fatalf("starts = %d, want 1", fake1.starts)
	}
	if err := server1.Close(); err != nil {
		t.Fatal(err)
	}
	if fake1.stops != 1 {
		t.Fatalf("stops on close = %d, want 1", fake1.stops)
	}

	// Test forwarders stop on Uninstall
	fake2 := &fakeWindowsForwarders{}
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &helperTrustRunner{}
	server2, err := NewServer(Config{
		AllowSID:        testHelperSID,
		HostsPath:       hostsPath,
		Runner:          runner,
		TrustManager:    wintrust.NewManagerWithStatePath(runner, nil, filepath.Join(t.TempDir(), "firefox-policy")),
		FirewallManager: winfirewall.NewManager(runner),
		forwarderFactory: func(_, _, _, _ string) (windowsForwarders, error) {
			return fake2, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server2.setForwarders(context.Background(), helperproto.ForwardersRequest{Enable: true, HTTPPort: 8080, HTTPSPort: 8443}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, helperproto.PathUninstall, nil)
	req.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	req = req.WithContext(context.WithValue(req.Context(), peerSIDKey{}, testHelperSID))
	w := httptest.NewRecorder()
	server2.handler().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("uninstall response status = %d: %s", w.Code, w.Body.String())
	}
	if fake2.stops != 1 {
		t.Fatalf("stops on uninstall = %d, want 1", fake2.stops)
	}
}

func TestWindowsProxyTargetsPersistenceCreatesMissingParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "system", "helper.yaml")
	if err := persistWindowsProxyTargets(path, 8080, 8443); err != nil {
		t.Fatalf("persist in missing directory: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy_http_port: 8080") || !strings.Contains(string(data), "proxy_https_port: 8443") {
		t.Fatalf("persisted content = %s", string(data))
	}
}

func TestWindowsProxyPersistenceRejectsSymlinks(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "real_helper.yaml")
	if err := os.WriteFile(target, []byte("allow_sid: "+testHelperSID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(tempDir, "symlink_helper.yaml")
	if err := os.Symlink(target, symlink); err != nil {
		t.Skip("symlinks not supported without elevated privileges")
	}
	if err := persistWindowsProxyTargets(symlink, 8080, 8443); err == nil {
		t.Fatal("expected persistWindowsProxyTargets to reject symlink")
	}
}
