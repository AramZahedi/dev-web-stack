package testcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestCreateCSR(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, err := CreateCSR(key, []string{"blog.test", "www.blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		t.Fatalf("CSR PEM block = %+v", block)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatal(err)
	}
	if len(csr.DNSNames) != 2 || csr.DNSNames[0] != "blog.test" || csr.DNSNames[1] != "www.blog.test" {
		t.Fatalf("DNSNames = %v", csr.DNSNames)
	}
	if csr.Subject.CommonName != "blog.test" {
		t.Fatalf("CN = %q", csr.Subject.CommonName)
	}
}

func TestCreateCSRRejectsNilKey(t *testing.T) {
	if _, err := CreateCSR(nil, []string{"blog.test"}); err == nil {
		t.Fatal("CreateCSR(nil) = nil error")
	}
}

func TestParseCertificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := ParseCertificate(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "test" {
		t.Fatalf("CN = %q", cert.Subject.CommonName)
	}
}

func TestParseCertificateRejectsGarbage(t *testing.T) {
	if _, err := ParseCertificate([]byte("not pem")); err == nil {
		t.Fatal("ParseCertificate(garbage) = nil error")
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte{1, 2, 3}})
	if _, err := ParseCertificate(keyPEM); err == nil {
		t.Fatal("ParseCertificate(non-certificate block) = nil error")
	}
}
