// Package testcert provides small certificate helpers for tests. It is not
// used by production code.
package testcert

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
)

// CreateCSR returns a PEM-encoded PKCS#10 certificate request for dnsNames,
// signed with key.
func CreateCSR(key *ecdsa.PrivateKey, dnsNames []string) ([]byte, error) {
	if key == nil {
		return nil, errors.New("testcert: private key is nil")
	}
	tmpl := &x509.CertificateRequest{
		DNSNames: append([]string(nil), dnsNames...),
	}
	if len(dnsNames) > 0 && len(dnsNames[0]) <= 64 {
		tmpl.Subject = pkix.Name{CommonName: dnsNames[0]}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, fmt.Errorf("testcert: create CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// ParseCertificate parses the first certificate in pemBytes.
func ParseCertificate(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("testcert: no PEM block found")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("testcert: unexpected PEM block type %q", block.Type)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("testcert: parse certificate: %w", err)
	}
	return cert, nil
}
