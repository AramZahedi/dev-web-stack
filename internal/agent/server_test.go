package agent_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

const testToken = "test-agent-secret-token"

func newTestAgent(t *testing.T) (*agent.Server, agent.TLSIdentity) {
	t.Helper()
	root := t.TempDir()
	tlsDir := filepath.Join(root, "agent-tls")
	identity, err := agent.GenerateTLSIdentity(tlsDir)
	if err != nil {
		t.Fatal(err)
	}
	config := agent.Config{
		Root: root, Distro: "devstack", Version: "test-build", Port: 0,
		TokenFile: filepath.Join(root, "agent.token"),
		TLSDir:    tlsDir, TLSCertFile: identity.CertPath, TLSKeyFile: identity.KeyPath,
	}
	server, err := agent.NewServer(config, testToken, identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server, identity
}

func TestAgentAuthRequiresBearerAndProtocolVersion(t *testing.T) {
	server, identity := newTestAgent(t)
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	for _, tc := range []struct {
		name     string
		token    string
		protocol string
		wantCode string
	}{
		{name: "missing token", protocol: agentproto.ProtocolVersion, wantCode: agentproto.CodeUnauthenticated},
		{name: "wrong token", token: "not-the-token", protocol: agentproto.ProtocolVersion, wantCode: agentproto.CodeUnauthenticated},
		{name: "wrong protocol", token: testToken, protocol: "v99", wantCode: agentproto.CodeVersionMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, r.url+agentproto.PathHealth, nil)
			if tc.token != "" {
				request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+tc.token)
			}
			request.Header.Set(agentproto.HeaderProtocol, tc.protocol)
			response, err := r.client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var body agentproto.ErrorResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.wantCode {
				t.Fatalf("error code = %q, want %q", body.Code, tc.wantCode)
			}
		})
	}
}

func TestAgentHealthAndVersionRequireAuthAndReportConfig(t *testing.T) {
	server, identity := newTestAgent(t)
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	request, _ := http.NewRequest(http.MethodGet, r.url+agentproto.PathHealth, nil)
	request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	response, err := r.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var health agentproto.HealthResponse
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Distro != "devstack" || health.Root == "" || health.Version != "test-build" {
		t.Fatalf("health = %+v", health)
	}

	version := authorizedGet(t, r, agentproto.PathVersion)
	var reported agentproto.VersionResponse
	if err := json.NewDecoder(version.Body).Decode(&reported); err != nil {
		t.Fatal(err)
	}
	_ = version.Body.Close()
	if reported.Protocol != agentproto.ProtocolVersion || reported.Agent != "test-build" {
		t.Fatalf("version = %+v", reported)
	}
}

func TestAgentHTTPSRejectsCertificatePinMismatch(t *testing.T) {
	server, identity := newTestAgent(t)
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	request, _ := http.NewRequest(http.MethodGet, r.url+agentproto.PathHealth, nil)
	request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	transport.CloseIdleConnections()

	wrongPin, err := agent.PinnedConfig("0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	client = &http.Client{Transport: &http.Transport{TLSClientConfig: wrongPin}, Timeout: time.Second}
	_, err = client.Get(r.url + agentproto.PathHealth)
	if err == nil || !errors.Is(err, agentproto.ErrCertPinMismatch) {
		t.Fatalf("wrong pin error = %v, want ErrCertPinMismatch", err)
	}
}

func TestAgentServesLoopbackAndDistroListeners(t *testing.T) {
	server, identity := newTestAgent(t)
	r := startAgentTestServer(t, server, identity, 2)
	defer r.close()
	if len(r.urls) != 2 {
		t.Fatalf("listener URLs = %v, want two", r.urls)
	}
	for _, base := range r.urls {
		request, _ := http.NewRequest(http.MethodGet, base+agentproto.PathHealth, nil)
		request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
		request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
		response, err := r.client.Do(request)
		if err != nil {
			t.Fatalf("GET %s: %v", base, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", base, response.StatusCode)
		}
	}
}

func TestAgentListenAddressesAreLoopbackAndPrivateNAT(t *testing.T) {
	root := t.TempDir()
	identity, err := agent.GenerateTLSIdentity(filepath.Join(root, "agent-tls"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := agent.NewServer(agent.Config{Root: root, Distro: "devstack", DistroIP: "172.18.0.4"}, testToken, identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := server.ListenAddresses()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1:7843", "172.18.0.4:7843"}
	if len(addresses) != len(want) || addresses[0] != want[0] || addresses[1] != want[1] {
		t.Fatalf("listen addresses = %v, want %v", addresses, want)
	}
	server, err = agent.NewServer(agent.Config{Root: root, Distro: "devstack", DistroIP: "203.0.113.7"}, testToken, identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.ListenAddresses(); err == nil {
		t.Fatal("public distro address was accepted")
	}
}

func TestAgentMountsFeatureModulesBehindAuth(t *testing.T) {
	root := t.TempDir()
	identity, err := agent.GenerateTLSIdentity(filepath.Join(root, "agent-tls"))
	if err != nil {
		t.Fatal(err)
	}
	config := agent.Config{Root: root, Distro: "devstack", Version: "test", Port: 0}
	server, err := agent.NewServer(config, testToken, identity, []agentapi.Module{testModule{}})
	if err != nil {
		t.Fatal(err)
	}
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()
	request, _ := http.NewRequest(http.MethodGet, r.url+"/v1/test-module/ready", nil)
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	response, err := r.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated module status = %d, want 401", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodGet, r.url+"/v1/test-module/ready", nil)
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	response, err = r.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("authenticated module status = %d, want 204", response.StatusCode)
	}
}

func TestAgentConfigAndSecretFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the WSL agent enforces POSIX mode bits; Windows uses installer ACLs")
	}
	if os.Geteuid() != 0 {
		t.Skip("loading the production WSL agent files requires root ownership")
	}
	root := t.TempDir()
	tlsDir := filepath.Join(root, "agent-tls")
	if err := os.Mkdir(tlsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	identity, err := agent.GenerateTLSIdentity(tlsDir)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(root, "agent.token")
	if err := os.WriteFile(tokenPath, []byte(testToken), 0o600); err != nil {
		t.Fatal(err)
	}
	config := agent.Config{Root: root, Distro: "devstack", Version: "test", Port: 0, TokenFile: tokenPath, TLSDir: tlsDir, TLSCertFile: identity.CertPath, TLSKeyFile: identity.KeyPath}
	configPath := filepath.Join(root, "agent.yaml")
	if err := agent.WriteConfig(configPath, config); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.NewServerFromFiles(configPath, []agentapi.Module(nil)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tokenPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.NewServerFromFiles(configPath, nil); err == nil {
		t.Fatal("server loaded a group/world-readable token")
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(identity.KeyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.NewServerFromFiles(configPath, nil); err == nil {
		t.Fatal("server loaded a group/world-readable private key")
	}
	if err := os.Chmod(identity.KeyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.NewServerFromFiles(configPath, nil); err == nil {
		t.Fatal("server loaded a group/world-readable config")
	}
}

type testAgentRun struct {
	url    string
	urls   []string
	client *http.Client
	close  func()
}

type testModule struct{}

func (testModule) Name() string      { return "test" }
func (testModule) MountPath() string { return "/v1/test-module" }
func (testModule) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func startAgentTestServer(t *testing.T, server *agent.Server, identity agent.TLSIdentity, listeners int) testAgentRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var list []net.Listener
	var urls []string
	for i := 0; i < listeners; i++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		list = append(list, listener)
		urls = append(urls, "https://"+listener.Addr().String())
	}
	go func() { _ = server.Serve(ctx, list...) }()
	clientTLS, err := agent.PinnedConfig(identity.Pin)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}, Timeout: 2 * time.Second}
	base := urls[0]
	if _, err := url.Parse(base); err != nil {
		cancel()
		t.Fatal(err)
	}
	return testAgentRun{url: base, urls: urls, client: client, close: func() {
		cancel()
		client.CloseIdleConnections()
	}}
}

func authorizedGet(t *testing.T, run testAgentRun, path string) *http.Response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, run.url+path, nil)
	request.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	request.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	response, err := run.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("GET %s status = %d", path, response.StatusCode)
	}
	return response
}
