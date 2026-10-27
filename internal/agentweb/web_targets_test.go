package agentweb_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentapi"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/agentweb"
)

func TestSyncWritesHTTPOnlyVhostsWithoutDashboard(t *testing.T) {
	root := t.TempDir()
	module, err := agentweb.New(agentweb.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	request := agentproto.WebTargetsRequest{Targets: []agentproto.WebTarget{{
		SiteID: "shop", Domain: "shop.test", Kind: "node", Address: "127.0.0.1", Port: 41000, TLS: false,
	}}}
	if _, err := module.Sync(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for server, baseName := range map[string]string{"nginx": "nginx.conf", "apache": "httpd.conf"} {
		base, err := os.ReadFile(filepath.Join(root, "services", server, "config", baseName))
		if err != nil {
			t.Fatalf("read %s private base config: %v", server, err)
		}
		text := strings.ToLower(string(base))
		if server == "apache" && !strings.Contains(text, "listen 18080") {
			t.Errorf("Apache base does not listen on private HTTP port: %s", base)
		}
		if strings.Contains(text, "listen 443") || strings.Contains(text, "listen 8443") || strings.Contains(text, "sslcertificate") {
			t.Errorf("%s private base contains public TLS listener/certificate config: %s", server, base)
		}
	}
	project := filepath.Join(module.Root(), "projects", "shop")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	targets := []agentproto.WebTarget{
		request.Targets[0],
		{SiteID: "php", Domain: "php.test", Kind: "php", Address: project, Port: 43000},
		{SiteID: "static", Domain: "static.test", Kind: "static", Address: project},
	}
	for _, server := range []string{"nginx", "apache"} {
		for _, target := range targets {
			config, err := module.RenderedVhost(server, target)
			if err != nil {
				t.Fatal(err)
			}
			text := strings.ToLower(string(config))
			if !strings.Contains(text, "18080") || !strings.Contains(text, target.Domain) {
				t.Errorf("%s private vhost is missing private listener/site: %s", server, config)
			}
			if strings.Contains(text, "listen 443") || strings.Contains(text, "listen 8443") || strings.Contains(text, "ssl_certificate") || strings.Contains(text, "devstack.test") {
				t.Errorf("%s private vhost contains public TLS or dashboard config: %s", server, config)
			}
		}
	}
}

func TestWebModuleMountsTargetsAndRejectsPublicOrArbitraryTargets(t *testing.T) {
	module, err := agentweb.New(agentweb.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	agentapi.Mount(mux, module)
	valid := agentproto.WebTargetsRequest{Targets: []agentproto.WebTarget{{
		SiteID: "shop", Domain: "shop.test", Kind: "node", Address: "127.0.0.1", Port: 41000,
	}}}
	body, _ := json.Marshal(valid)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, agentproto.PathWebTargets, bytes.NewReader(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("mount target response = %d %s", recorder.Code, recorder.Body.String())
	}
	badRecorder := httptest.NewRecorder()
	mux.ServeHTTP(badRecorder, httptest.NewRequest(http.MethodPost, agentproto.PathWebTargets, strings.NewReader(`{"targets":[]} trailing`)))
	if badRecorder.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON response = %d, want 400", badRecorder.Code)
	}
	for _, target := range []agentproto.WebTarget{
		{SiteID: "shop", Domain: "shop.test", Kind: "node", Address: "0.0.0.0", Port: 41000},
		{SiteID: "dashboard", Domain: "devstack.test", Kind: "node", Address: "127.0.0.1", Port: 41000},
		{SiteID: "shop", Domain: "shop.test", Kind: "node", Address: "127.0.0.1", Port: 7},
		{SiteID: "missing", Domain: "missing.test", Kind: "static", Address: filepath.Join(module.Root(), "projects", "missing")},
		{SiteID: "escape", Domain: "escape.test", Kind: "static", Address: filepath.Join(module.Root(), "outside")},
	} {
		if err := module.ValidateTarget(target); err == nil {
			t.Errorf("ValidateTarget(%+v) accepted a target outside the private fixed contract", target)
		}
	}
}

func TestCopyLeafCertificateKeepsOnlyLeafMaterialInWSL(t *testing.T) {
	module, err := agentweb.New(agentweb.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM := leafMaterial(t)
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		t.Fatal("generated fixture certificate is empty")
	}
	if err := module.CopyLeafCertificate(context.Background(), "shop", certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(module.Root(), "tls")
	for name, want := range map[string]os.FileMode{"shop.crt": 0o600, "shop.key": 0o600} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != want {
				t.Errorf("%s mode = %#o, want %#o", name, info.Mode().Perm(), want)
			}
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("TLS directory mode = %#o, want 0700", info.Mode().Perm())
	}
	if err := module.CopyLeafCertificate(context.Background(), "../rootCA", certPEM, keyPEM); err == nil {
		t.Fatal("certificate owner path traversal was accepted")
	}
	if err := module.CopyLeafCertificate(context.Background(), "__dashboard__", certPEM, keyPEM); err == nil {
		t.Fatal("dashboard certificate copy was accepted for a daemon-owned route")
	}
}

func leafMaterial(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "shop.test"},
		DNSNames:     []string{"shop.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}
