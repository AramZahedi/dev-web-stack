package agent_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentclient"
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

func testCABundle(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(8),
		Subject:               pkix.Name{CommonName: "devstack-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestAgentInstallCAAndCAResponse(t *testing.T) {
	server, identity := newTestAgent(t)
	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	client, err := agentclient.New(agentclient.Config{
		BaseURL:   r.url,
		Token:     testToken,
		PinSHA256: identity.Pin,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. Before installation, CA() should return not found
	_, err = client.CA(ctx)
	if !errors.Is(err, agentproto.ErrNotFound) {
		t.Fatalf("CA() before install = %v, want ErrNotFound", err)
	}

	// 2. Install invalid CA (garbage or private key)
	err = client.InstallCA(ctx, []byte("-----BEGIN PRIVATE KEY-----\ninvalid\n-----END PRIVATE KEY-----"))
	if !errors.Is(err, agentproto.ErrInvalidRequest) {
		t.Fatalf("InstallCA() with invalid CA = %v, want ErrInvalidRequest", err)
	}

	// 3. Install valid CA bundle
	bundle := testCABundle(t)
	if err := client.InstallCA(ctx, bundle); err != nil {
		t.Fatalf("InstallCA() = %v", err)
	}

	// 4. CA() should now return the bundle and valid fingerprint
	caResp, err := client.CA(ctx)
	if err != nil {
		t.Fatalf("CA() after install = %v", err)
	}
	if string(caResp.CertPEM) != string(bundle) {
		t.Fatalf("CA() cert PEM mismatch")
	}
	wantFingerprint, err := agent.FingerprintCertPEM(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if caResp.Fingerprint != wantFingerprint {
		t.Fatalf("fingerprint = %q, want %q", caResp.Fingerprint, wantFingerprint)
	}
}

func TestAgentInstallCAReachesSiteAndTerminalEnvironments(t *testing.T) {
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

	rt, err := agent.NewRuntime(root)
	if err != nil {
		t.Fatal(err)
	}

	server, err := agent.NewServerWithRuntime(config, testToken, identity, nil, rt)
	if err != nil {
		t.Fatal(err)
	}

	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	client, err := agentclient.New(agentclient.Config{
		BaseURL:   r.url,
		Token:     testToken,
		PinSHA256: identity.Pin,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Generate a CA key and certificate
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "devstack-integration-ca"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// 2. Generate a server certificate signed by this CA for 127.0.0.1
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(101),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER})

	serverTLSCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Start a mock target HTTPS service using this CA-signed certificate
	targetMux := http.NewServeMux()
	targetMux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer targetListener.Close()

	targetServer := &http.Server{
		Handler:   targetMux,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{serverTLSCert}},
	}
	go func() { _ = targetServer.ServeTLS(targetListener, "", "") }()
	defer targetServer.Close()

	targetURL := "https://" + targetListener.Addr().String() + "/ping"

	// 4. Inject CA via InstallCA (simulating Windows daemon)
	ctx := context.Background()
	if err := client.InstallCA(ctx, caPEM); err != nil {
		t.Fatalf("InstallCA failed: %v", err)
	}

	// 5. Verify the CA bundle file exists on disk
	caBundlePath := filepath.Join(root, "certs", "ca-bundle.crt")
	caData, err := os.ReadFile(caBundlePath)
	if err != nil {
		t.Fatalf("read ca bundle: %v", err)
	}
	if string(caData) != string(caPEM) {
		t.Fatal("ca bundle content mismatch")
	}

	// 6. Verify PHP configuration: both openssl.cafile and curl.cainfo are set
	phpIniPath := filepath.Join(root, "certs", "php-conf.d", "99-devstack-ca.ini")
	phpIniData, err := os.ReadFile(phpIniPath)
	if err != nil {
		t.Fatalf("read php ini: %v", err)
	}
	phpIniStr := string(phpIniData)
	if !strings.Contains(phpIniStr, "openssl.cafile="+caBundlePath) {
		t.Fatalf("missing openssl.cafile in php ini: %s", phpIniStr)
	}
	if !strings.Contains(phpIniStr, "curl.cainfo="+caBundlePath) {
		t.Fatalf("missing curl.cainfo in php ini: %s", phpIniStr)
	}

	// 7. Verify site environment injection via runtime.PrepareSite
	docRoot := filepath.Join(root, "sites", "site1", "public")
	if err := os.MkdirAll(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := agentproto.SiteSpec{
		ID:           "site1",
		Kind:         "static",
		BackendPath:  docRoot,
		DocumentRoot: docRoot,
		HTTPPort:     41080,
	}
	prep, err := rt.PrepareSite(ctx, spec)
	if err != nil {
		t.Fatalf("PrepareSite: %v", err)
	}
	if prep.HTTPPort != 41080 {
		t.Fatalf("HTTPPort = %d, want 41080", prep.HTTPPort)
	}

	// 8. Verify terminal and site environment injection
	termEnv, err := rt.Environment(map[string]string{"TERM": "xterm-256color", "PATH": "/usr/bin"})
	if err != nil {
		t.Fatalf("Environment: %v", err)
	}
	if termEnv["TERM"] != "xterm-256color" || termEnv["PATH"] != "/usr/bin" {
		t.Fatalf("lost existing env: %v", termEnv)
	}
	if termEnv["SSL_CERT_FILE"] != caBundlePath {
		t.Fatalf("SSL_CERT_FILE = %q, want %q", termEnv["SSL_CERT_FILE"], caBundlePath)
	}
	if termEnv["CURL_CA_BUNDLE"] != caBundlePath {
		t.Fatalf("CURL_CA_BUNDLE = %q, want %q", termEnv["CURL_CA_BUNDLE"], caBundlePath)
	}
	if termEnv["NODE_EXTRA_CA_CERTS"] != caBundlePath {
		t.Fatalf("NODE_EXTRA_CA_CERTS = %q, want %q", termEnv["NODE_EXTRA_CA_CERTS"], caBundlePath)
	}
	if !strings.Contains(termEnv["PHP_INI_SCAN_DIR"], filepath.Join(root, "certs", "php-conf.d")) {
		t.Fatalf("PHP_INI_SCAN_DIR = %q", termEnv["PHP_INI_SCAN_DIR"])
	}

	// 9. Prove cryptographic verification:
	// A client using this CA bundle trusts targetServer
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("failed to append CA PEM to pool")
	}
	trustedClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
		Timeout: 2 * time.Second,
	}
	resp, err := trustedClient.Get(targetURL)
	if err != nil {
		t.Fatalf("trusted client GET failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// An untrusted client (empty pool) fails TLS verification
	untrustedClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()},
		},
		Timeout: 2 * time.Second,
	}
	_, err = untrustedClient.Get(targetURL)
	if err == nil {
		t.Fatal("untrusted client succeeded unexpectedly")
	}

	// 10. If curl executable is available, verify curl CLI with SSL_CERT_FILE / CURL_CA_BUNDLE and --cacert
	if curlPath, err := exec.LookPath("curl"); err == nil {
		args := []string{"-sS", "-f", "--cacert", caBundlePath}
		if runtime.GOOS == "windows" {
			args = append(args, "--ssl-no-revoke")
		}
		args = append(args, targetURL)
		cmd := exec.Command(curlPath, args...)
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env,
			"SSL_CERT_FILE="+caBundlePath,
			"CURL_CA_BUNDLE="+caBundlePath,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("curl with injected CA env failed: %s (err: %v)", string(out), err)
		} else if strings.TrimSpace(string(out)) != "pong" {
			t.Fatalf("curl output = %q, want 'pong'", string(out))
		}
	}
}

type fakeRelayModule struct {
	mountPath string
}

func (m fakeRelayModule) Name() string      { return "service-relay" }
func (m fakeRelayModule) MountPath() string { return m.mountPath }
func (m fakeRelayModule) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"running","service":"mysql"}`))
	})
	mux.HandleFunc("/action", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	return http.StripPrefix("/v1/services", mux)
}

type fakeDockerModule struct{}

func (fakeDockerModule) Name() string      { return "docker-control" }
func (fakeDockerModule) MountPath() string { return "/v1/docker" }
func (fakeDockerModule) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ps", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"containers":[]}`))
	})
	return http.StripPrefix("/v1/docker", mux)
}

func TestAgentMountsExternalModulesCleanly(t *testing.T) {
	root := t.TempDir()
	tlsDir := filepath.Join(root, "agent-tls")
	identity, err := agent.GenerateTLSIdentity(tlsDir)
	if err != nil {
		t.Fatal(err)
	}
	config := agent.Config{Root: root, Distro: "devstack", Version: "test", Port: 0}

	relayMod := fakeRelayModule{mountPath: "/v1/services"}
	dockerMod := fakeDockerModule{}

	// Verify server mounts multiple modules cleanly
	server, err := agent.NewServer(config, testToken, identity, []agentapi.Module{relayMod, dockerMod})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	r := startAgentTestServer(t, server, identity, 1)
	defer r.close()

	// 1. Unauthenticated request to /v1/services/status returns 401
	req, _ := http.NewRequest(http.MethodGet, r.url+"/v1/services/status", nil)
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", resp.StatusCode)
	}

	// 2. Request with wrong token returns 401
	req, _ = http.NewRequest(http.MethodGet, r.url+"/v1/services/status", nil)
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+"wrong")
	resp, err = r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", resp.StatusCode)
	}

	// 3. Request missing protocol header returns 426 Upgrade Required
	req, _ = http.NewRequest(http.MethodGet, r.url+"/v1/services/status", nil)
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	resp, err = r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("missing protocol status = %d, want 426", resp.StatusCode)
	}

	// 4. Authenticated request to /v1/services/status reaches handler
	req, _ = http.NewRequest(http.MethodGet, r.url+"/v1/services/status", nil)
	req.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	req.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	resp, err = r.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var relayResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&relayResp); err != nil {
		t.Fatal(err)
	}
	if relayResp["service"] != "mysql" || relayResp["status"] != "running" {
		t.Fatalf("unexpected relay response: %v", relayResp)
	}

	// 5. Authenticated request to /v1/docker/ps reaches docker handler
	reqDocker, _ := http.NewRequest(http.MethodGet, r.url+"/v1/docker/ps", nil)
	reqDocker.Header.Set(agentproto.HeaderProtocol, agentproto.ProtocolVersion)
	reqDocker.Header.Set(agentproto.HeaderAuth, agentproto.BearerPrefix+testToken)
	respDocker, err := r.client.Do(reqDocker)
	if err != nil {
		t.Fatal(err)
	}
	_ = respDocker.Body.Close()
	if respDocker.StatusCode != http.StatusOK {
		t.Fatalf("docker status = %d, want 200", respDocker.StatusCode)
	}

	// 6. Test rejection of invalid modules
	badModules := [][]agentapi.Module{
		{nil},
		{fakeRelayModule{mountPath: "/not-v1"}},
		{fakeRelayModule{mountPath: "v1/no-leading-slash"}},
	}
	for i, bm := range badModules {
		_, err := agent.NewServer(config, testToken, identity, bm)
		if err == nil {
			t.Fatalf("badModule[%d] unexpectedly accepted", i)
		}
	}
}
