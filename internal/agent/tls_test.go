package agent_test

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net/http/httptest"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
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
