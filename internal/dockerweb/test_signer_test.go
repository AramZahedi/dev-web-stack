package dockerweb

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

// testCASigner is a minimal in-memory CA for the real certs.Manager adapter
// test. It signs the CSR produced by certs.Manager and returns a leaf chain.
type testCASigner struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
	pem  []byte
}

func newTestCASigner(t *testing.T) *testCASigner {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "dockerweb test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCASigner{
		key:  key,
		cert: cert,
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

func (s *testCASigner) RootPEM() []byte {
	return append([]byte(nil), s.pem...)
}

func (s *testCASigner) SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.SignResponse{}, err
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return helperproto.SignResponse{}, errors.New("test signer: CSR is not PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	if err := csr.CheckSignature(); err != nil {
		return helperproto.SignResponse{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domains[0]},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(90 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string(nil), domains...),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, s.cert, csr.PublicKey, s.key)
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	return helperproto.SignResponse{
		CertPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Serial:   serial.Text(16),
		NotAfter: cert.NotAfter,
	}, nil
}
