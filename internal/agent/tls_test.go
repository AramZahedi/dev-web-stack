package agent_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

func TestGenerateAndLoadIdentity(t *testing.T) {
	dir := t.TempDir()
	identity, err := agent.GenerateTLSIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Pin == "" || len(identity.Pin) != 64 {
		t.Fatalf("pin = %q", identity.Pin)
	}
	loaded, err := agent.LoadTLSIdentity(identity.CertPath, identity.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Pin != identity.Pin {
		t.Fatalf("loaded pin = %q, want %q", loaded.Pin, identity.Pin)
	}
	pair, err := tls.X509KeyPair(loaded.CertPEM, loaded.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	srv.StartTLS()
	defer srv.Close()
	block, _ := pem.Decode(loaded.CertPEM)
	if block == nil {
		t.Fatal("no certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.Raw)
	if identity.Pin != hex.EncodeToString(sum[:]) {
		t.Fatalf("pin does not match certificate")
	}
}

func TestPinnedConfigDiagnostics(t *testing.T) {
	dir := t.TempDir()
	identity, err := agent.GenerateTLSIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := agent.PinnedConfig(identity.Pin)
	if err != nil {
		t.Fatal(err)
	}

	block, _ := pem.Decode(identity.CertPEM)
	if block == nil {
		t.Fatal("failed to decode cert PEM")
	}

	// 1. Valid certificate matches pin
	if err := cfg.VerifyPeerCertificate([][]byte{block.Bytes}, nil); err != nil {
		t.Fatalf("valid certificate failed verification: %v", err)
	}

	// 2. Empty rawCerts
	err = cfg.VerifyPeerCertificate(nil, nil)
	if err == nil || !errors.Is(err, agentproto.ErrCertPinMismatch) {
		t.Fatalf("expected ErrCertPinMismatch on empty certs, got %v", err)
	}
	if !strings.Contains(err.Error(), "no peer certificate presented") {
		t.Errorf("error %q should mention no peer certificate presented", err.Error())
	}

	// 3. Pin mismatch
	wrongCfg, err := agent.PinnedConfig("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	err = wrongCfg.VerifyPeerCertificate([][]byte{block.Bytes}, nil)
	if err == nil || !errors.Is(err, agentproto.ErrCertPinMismatch) {
		t.Fatalf("expected ErrCertPinMismatch on wrong pin, got %v", err)
	}
	if !strings.Contains(err.Error(), identity.Pin) || !strings.Contains(err.Error(), "rotated") {
		t.Errorf("error %q should contain diagnostic message with pins and rotation hint", err.Error())
	}

	// 4. Expired certificate
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "expired-test"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour), // expired yesterday
	}
	expiredDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	expiredSum := sha256.Sum256(expiredDER)
	expiredPin := hex.EncodeToString(expiredSum[:])
	expiredCfg, err := agent.PinnedConfig(expiredPin)
	if err != nil {
		t.Fatal(err)
	}
	err = expiredCfg.VerifyPeerCertificate([][]byte{expiredDER}, nil)
	if err == nil || !strings.Contains(err.Error(), "certificate expired") {
		t.Fatalf("expected expiration diagnostic error, got: %v", err)
	}
}

func TestLoadTLSIdentityExpiredCert(t *testing.T) {
	dir := t.TempDir()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "expired-agent"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "expired.crt")
	keyPath := filepath.Join(dir, "expired.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = agent.LoadTLSIdentity(certPath, keyPath)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error on LoadTLSIdentity, got %v", err)
	}
}

func TestPinnedConfigTrimsWhitespace(t *testing.T) {
	validPin := strings.Repeat("a", 64)
	whitespacePins := []string{
		"  " + validPin + "  ",
		"\r\n" + validPin + "\n",
		"\t" + validPin + "\r",
	}
	for _, pin := range whitespacePins {
		cfg, err := agent.PinnedConfig(pin)
		if err != nil {
			t.Fatalf("PinnedConfig(%q) failed: %v", pin, err)
		}
		if cfg == nil {
			t.Fatalf("PinnedConfig(%q) returned nil config", pin)
		}
	}
}
