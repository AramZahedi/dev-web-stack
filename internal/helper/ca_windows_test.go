//go:build windows

package helper

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadOrCreateWindowsAuthorityKeepsKeyInConfiguredCADirectory(t *testing.T) {
	dir := t.TempDir()
	authority, err := LoadOrCreateAuthority(dir, time.Now)
	if err != nil {
		t.Fatalf("LoadOrCreateAuthority: %v", err)
	}
	if got, want := authority.KeyPath(), filepath.Join(dir, "root.key"); got != want {
		t.Fatalf("key path = %q; want %q", got, want)
	}
	for _, name := range []string{"root.key", "root.crt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("CA file %s missing: %v", name, err)
		}
	}
	if len(authority.RootPEM()) == 0 || authority.Fingerprint() == "" {
		t.Fatal("authority did not expose its public root and fingerprint")
	}
}

func TestWindowsAuthoritySignsOnlyRequestedDNSNames(t *testing.T) {
	authority, err := LoadOrCreateAuthority(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "site.test"}, DNSNames: []string{"site.test"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	csr := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	certPEM, _, _, err := authority.SignCSR(csr, []string{"site.test"}, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("SignCSR: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || len(cert.DNSNames) != 1 || cert.DNSNames[0] != "site.test" {
		t.Fatalf("signed certificate domains = %v, err=%v", cert, err)
	}
	if _, _, _, err := authority.SignCSR(csr, []string{"other.test"}, time.Hour); err == nil {
		t.Fatal("authority signed a domain not present in the CSR")
	}
}
