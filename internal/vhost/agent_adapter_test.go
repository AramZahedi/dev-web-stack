package vhost_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/vhost"
)

type fakeLeafCopier struct {
	owner string
	cert  []byte
	key   []byte
}

func (f *fakeLeafCopier) CopyLeafCertificate(_ context.Context, owner string, cert, key []byte) error {
	f.owner = owner
	f.cert = append([]byte(nil), cert...)
	f.key = append([]byte(nil), key...)
	return nil
}

func TestAgentCertificateAdapterCopiesOnlyValidatedLeafMaterial(t *testing.T) {
	copier := &fakeLeafCopier{}
	adapter, err := vhost.NewAgentCertificateAdapter(copier)
	if err != nil {
		t.Fatal(err)
	}
	cert, key := adapterCert(t, false)
	if err := adapter.CopyLeafCertificate(context.Background(), "shop", cert, key); err != nil {
		t.Fatal(err)
	}
	if copier.owner != "shop" || string(copier.cert) != string(cert) || string(copier.key) != string(key) {
		t.Fatal("leaf certificate copy did not preserve the requested material")
	}
	caCert, caKey := adapterCert(t, true)
	if err := adapter.CopyLeafCertificate(context.Background(), "rootCA", caCert, caKey); err == nil {
		t.Fatal("CA material was accepted for a WSL leaf copy")
	}
	if err := adapter.CopyLeafCertificate(context.Background(), "../escape", cert, key); err == nil {
		t.Fatal("certificate owner traversal was accepted")
	}
	if err := adapter.CopyLeafCertificate(context.Background(), "__dashboard__", cert, key); err == nil {
		t.Fatal("daemon dashboard certificate was accepted for a container copy")
	}
}

func adapterCert(t *testing.T, isCA bool) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "shop.test"},
		DNSNames:              []string{"shop.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		IsCA:                  isCA,
		BasicConstraintsValid: true,
	}
	if isCA {
		template.KeyUsage |= x509.KeyUsageCertSign
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
