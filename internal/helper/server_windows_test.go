//go:build windows

package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/ipc"
	"github.com/AramZahedi/dev-web-stack/internal/winhosts"
	"github.com/AramZahedi/dev-web-stack/internal/wintrust"
)

const testHelperSID = "S-1-5-21-123456789-123456789-123456789-1001"

func TestNewWindowsServerRequiresAllowedSID(t *testing.T) {
	if _, err := NewServer(Config{AllowSID: ""}); err == nil {
		t.Fatal("NewServer accepted an empty allowed SID")
	}
	if _, err := NewServer(Config{AllowSID: testHelperSID}); err != nil {
		t.Fatalf("NewServer rejected valid SID: %v", err)
	}
}

type helperTrustRunner struct {
	calls  []string
	policy bool
}

func (r *helperTrustRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "reg.exe" {
		switch args[0] {
		case "query":
			if r.policy {
				return []byte("ImportEnterpriseRoots REG_DWORD 0x1"), nil
			}
			return []byte("ERROR: The system was unable to find the specified registry value."), errors.New("missing value")
		case "add":
			r.policy = true
		}
	}
	return nil, nil
}

func TestWindowsHelperTrustInstallUsesFixedMachineRootAndFirefoxPolicy(t *testing.T) {
	authority, err := LoadOrCreateAuthority(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner := &helperTrustRunner{}
	server, err := NewServer(Config{
		AllowSID:     testHelperSID,
		Authority:    authority,
		TrustManager: wintrust.NewManagerWithStatePath(runner, nil, filepath.Join(t.TempDir(), "firefox-policy-managed")),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, helperproto.PathTrustInstall, strings.NewReader(`{"user_home":"C:\\Users\\not-accepted-as-a-path"}`))
	request.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	request = request.WithContext(context.WithValue(request.Context(), peerSIDKey{}, testHelperSID))
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("trust install status = %d; body=%s", response.Code, response.Body.String())
	}
	if len(runner.calls) != 3 || !strings.Contains(runner.calls[0], "certutil.exe -addstore -f Root "+DefaultWindowsRootCert) || !strings.Contains(runner.calls[1], "reg.exe query HKLM") || !strings.Contains(runner.calls[2], "reg.exe add HKLM") {
		t.Fatalf("trust commands = %v", runner.calls)
	}
}

func TestWindowsHelperDerivesSystemAndCAPathsFromConfigPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "custom", "system", "helper.yaml")
	server, err := NewServer(Config{AllowSID: testHelperSID, ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	systemDir := filepath.Dir(configPath)
	if server.cfg.SystemDir != systemDir {
		t.Fatalf("SystemDir = %q, want %q", server.cfg.SystemDir, systemDir)
	}
	if server.cfg.CADir != filepath.Join(systemDir, "pki") {
		t.Fatalf("CADir = %q", server.cfg.CADir)
	}
	if server.cfg.RootCertPath != filepath.Join(systemDir, "pki", "root.crt") {
		t.Fatalf("RootCertPath = %q", server.cfg.RootCertPath)
	}
}

func TestWindowsHelperHonorsExplicitSystemPaths(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{
		AllowSID:     testHelperSID,
		ConfigPath:   filepath.Join(root, "config", "helper.yaml"),
		SystemDir:    filepath.Join(root, "system"),
		CADir:        filepath.Join(root, "ca"),
		RootCertPath: filepath.Join(root, "ca", "custom-root.crt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.cfg.SystemDir != filepath.Join(root, "system") ||
		server.cfg.CADir != filepath.Join(root, "ca") ||
		server.cfg.RootCertPath != filepath.Join(root, "ca", "custom-root.crt") {
		t.Fatalf("config = %+v", server.cfg)
	}
}

func TestWindowsHelperHostsSyncUsesManagedBlockAndRejectsOtherOperations(t *testing.T) {
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(hostsPath, []byte("127.0.0.1 localhost\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{AllowSID: testHelperSID, HostsManager: winhosts.NewManager(hostsPath)})
	if err != nil {
		t.Fatal(err)
	}
	requestBody, err := json.Marshal(helperproto.HostsSyncRequest{Domains: []string{"site.test"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, helperproto.PathHostsSync, bytes.NewReader(requestBody))
	request.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	request = request.WithContext(context.WithValue(request.Context(), peerSIDKey{}, testHelperSID))
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("hosts sync status = %d; body=%s", response.Code, response.Body.String())
	}
	data, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("127.0.0.1 site.test")) || !bytes.Contains(data, []byte("127.0.0.1 localhost")) {
		t.Fatalf("hosts file = %q", data)
	}

	forbidden := httptest.NewRequest(http.MethodGet, "/v1/services/status", nil)
	forbidden.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	forbidden = forbidden.WithContext(context.WithValue(forbidden.Context(), peerSIDKey{}, testHelperSID))
	response = httptest.NewRecorder()
	server.handler().ServeHTTP(response, forbidden)
	if response.Code != http.StatusNotFound && response.Code != http.StatusNotImplemented {
		t.Fatalf("service RPC status = %d, want rejected", response.Code)
	}
}

func TestAuthorizePeerSIDRequiresExactConfiguredSID(t *testing.T) {
	if err := authorizePeerSID(testHelperSID, ipc.PeerIdentity{SID: testHelperSID}); err != nil {
		t.Fatalf("configured SID was rejected: %v", err)
	}
	for _, peer := range []ipc.PeerIdentity{{SID: "S-1-5-21-foreign"}, {SID: ""}, {SID: ipc.SIDLocalSystem}, {SID: ipc.SIDAdministrators}} {
		if err := authorizePeerSID(testHelperSID, peer); err == nil {
			t.Errorf("peer SID %q was accepted", peer.SID)
		}
	}
}

func TestHelperPipeSDDLGrantsOnlyRequiredPrincipals(t *testing.T) {
	sddl, err := helperPipeSDDL(testHelperSID)
	if err != nil {
		t.Fatalf("helperPipeSDDL: %v", err)
	}
	want := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + testHelperSID + ")"
	if sddl != want {
		t.Fatalf("SDDL = %q, want %q", sddl, want)
	}
	for _, forbidden := range []string{"WD", "AU", "AN"} {
		if strings.Contains(sddl, forbidden) {
			t.Errorf("SDDL includes broad principal %q: %s", forbidden, sddl)
		}
	}
}

type failingForwarders struct {
	stopErr error
}

func (f *failingForwarders) Start(context.Context) error { return nil }
func (f *failingForwarders) Stop(context.Context) error  { return f.stopErr }

func TestWindowsServerClosePreservesForwarderStopError(t *testing.T) {
	expectedErr := errors.New("simulated forwarder stop error")
	server, err := NewServer(Config{
		AllowSID: testHelperSID,
		forwarderFactory: func(httpListen, httpsListen, httpTarget, httpsTarget string) (windowsForwarders, error) {
			return &failingForwarders{stopErr: expectedErr}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.forwarders = &failingForwarders{stopErr: expectedErr}
	server.forwardersKey = "active"

	closeErr := server.Close()
	if closeErr == nil {
		t.Fatal("expected error from server.Close(), got nil")
	}
	if !errors.Is(closeErr, expectedErr) && !strings.Contains(closeErr.Error(), expectedErr.Error()) {
		t.Fatalf("server.Close() error = %v, want to contain %v", closeErr, expectedErr)
	}
}

func TestWindowsServerUninstallRemovesRootWithUncachedAuthority(t *testing.T) {
	tempDir := t.TempDir()
	authority, err := LoadOrCreateAuthority(tempDir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	rootCertPath := filepath.Join(tempDir, rootCertFile)

	runner := &helperTrustRunner{}
	server, err := NewServer(Config{
		AllowSID:     testHelperSID,
		RootCertPath: rootCertPath,
		TrustManager: wintrust.NewManagerWithStatePath(runner, nil, filepath.Join(tempDir, "firefox-policy-managed")),
	})
	if err != nil {
		t.Fatal(err)
	}
	// server.authority is nil initially
	if server.authority != nil {
		t.Fatal("server.authority expected to be nil before EnsureAuthority")
	}

	request := httptest.NewRequest(http.MethodPost, helperproto.PathUninstall, strings.NewReader(`{}`))
	request.Header.Set(helperproto.ProtocolHeader, helperproto.Version)
	request = request.WithContext(context.WithValue(request.Context(), peerSIDKey{}, testHelperSID))
	response := httptest.NewRecorder()
	server.handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("uninstall response = %d: %s", response.Code, response.Body.String())
	}
	// Verify certutil was called with the authority's thumbprint to remove the root
	expectedCall := "certutil.exe -delstore Root " + strings.ToUpper(authority.Thumbprint())
	removedCert := false
	for _, call := range runner.calls {
		if strings.Contains(call, expectedCall) {
			removedCert = true
			break
		}
	}
	if !removedCert {
		t.Fatalf("uninstall did not invoke certutil to remove root certificate with thumbprint %s; calls = %v", authority.Thumbprint(), runner.calls)
	}
}
