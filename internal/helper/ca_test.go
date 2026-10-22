package helper

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/testcert"
)

func newTestAuthority(t *testing.T) *Authority {
	t.Helper()
	a, err := LoadOrCreateAuthority(t.TempDir(), time.Now)
	if err != nil {
		t.Fatalf("LoadOrCreateAuthority: %v", err)
	}
	return a
}

func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func encodeECPrivateKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func makeTestCSR(t *testing.T, key *ecdsa.PrivateKey, dnsNames ...string) []byte {
	t.Helper()
	csr, err := testcert.CreateCSR(key, dnsNames)
	if err != nil {
		t.Fatalf("CreateCSR: %v", err)
	}
	return csr
}

func makeCSRFromTemplate(t *testing.T, key *ecdsa.PrivateKey, tmpl *x509.CertificateRequest) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func sanExtensionFor(t *testing.T, names ...asn1.RawValue) pkix.Extension {
	t.Helper()
	value, err := asn1.Marshal(names)
	if err != nil {
		t.Fatalf("marshal SAN: %v", err)
	}
	return pkix.Extension{
		Id:    asn1.ObjectIdentifier{2, 5, 29, 17},
		Value: value,
	}
}

func dnsGeneralName(name string) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 2, Bytes: []byte(name)}
}

func otherGeneralName(tag int, compound bool) asn1.RawValue {
	return asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        tag,
		IsCompound: compound,
		Bytes:      []byte{0x2a, 0x03},
	}
}

func parseTestCertificate(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	cert, err := testcert.ParseCertificate(certPEM)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	return cert
}

func TestCAIssuesVerifiableLeaf(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	domains := []string{"blog.test", "www.blog.test"}
	csr := makeTestCSR(t, key, domains...)

	certPEM, serial, notAfter, err := a.SignCSR(csr, domains, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("SignCSR: %v", err)
	}
	if serial == "" || notAfter.IsZero() {
		t.Fatalf("serial = %q, notAfter = %v", serial, notAfter)
	}
	if time.Until(notAfter) < 89*24*time.Hour {
		t.Fatalf("notAfter = %v is too soon", notAfter)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(a.RootPEM()) {
		t.Fatal("root PEM did not parse")
	}
	leaf := parseTestCertificate(t, certPEM)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "blog.test",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("verify leaf: %v", err)
	}
	if got, want := leaf.DNSNames, domains; !sameStringSet(got, want) {
		t.Fatalf("leaf SANs = %v, want %v", got, want)
	}
	if leaf.IsCA {
		t.Fatal("leaf is a CA certificate")
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || leaf.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Fatalf("leaf key usage = %v", leaf.KeyUsage)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("leaf extended key usage = %v", leaf.ExtKeyUsage)
	}
}

func TestCAKeyMode(t *testing.T) {
	a := newTestAuthority(t)
	info, err := os.Stat(a.KeyPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, want 0600", info.Mode().Perm())
	}
	certInfo, err := os.Stat(filepath.Join(filepath.Dir(a.KeyPath()), "root.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if certInfo.Mode().Perm() != 0o644 {
		t.Fatalf("cert mode = %v, want 0644", certInfo.Mode().Perm())
	}
}

func TestRootCertificateProperties(t *testing.T) {
	a := newTestAuthority(t)
	root := parseTestCertificate(t, a.RootPEM())

	if root.Subject.CommonName != "devstack local CA" {
		t.Fatalf("CN = %q", root.Subject.CommonName)
	}
	if !root.IsCA || !root.BasicConstraintsValid {
		t.Fatalf("IsCA = %v, BasicConstraintsValid = %v", root.IsCA, root.BasicConstraintsValid)
	}
	if !root.MaxPathLenZero || root.MaxPathLen != 0 {
		t.Fatalf("MaxPathLenZero = %v, MaxPathLen = %d", root.MaxPathLenZero, root.MaxPathLen)
	}
	if want := x509.KeyUsageCertSign | x509.KeyUsageCRLSign; root.KeyUsage != want {
		t.Fatalf("key usage = %v, want %v", root.KeyUsage, want)
	}
	if root.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatalf("public key algorithm = %v", root.PublicKeyAlgorithm)
	}
	pub, ok := root.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key type = %T", root.PublicKey)
	}
	if pub.Curve != elliptic.P256() {
		t.Fatalf("curve = %v, want P-256", pub.Curve)
	}
	span := root.NotAfter.Sub(root.NotBefore)
	if span < 9*365*24*time.Hour || span > 10*366*24*time.Hour {
		t.Fatalf("root validity = %v, want about 10 years", span)
	}
	if root.SerialNumber == nil || root.SerialNumber.Sign() <= 0 {
		t.Fatalf("root serial = %v", root.SerialNumber)
	}

	sum := sha256.Sum256(root.Raw)
	want := hex.EncodeToString(sum[:])
	if a.Fingerprint() != want {
		t.Fatalf("fingerprint = %q, want %q", a.Fingerprint(), want)
	}
	if len(a.Fingerprint()) != 64 || a.Fingerprint() != strings.ToLower(a.Fingerprint()) {
		t.Fatalf("fingerprint is not 64 lowercase hex characters: %q", a.Fingerprint())
	}
}

func TestLoadOrCreateAuthorityReloadsExisting(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateAuthority(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	keyBefore, err := os.ReadFile(first.KeyPath())
	if err != nil {
		t.Fatal(err)
	}
	certBefore, err := os.ReadFile(filepath.Join(dir, "root.crt"))
	if err != nil {
		t.Fatal(err)
	}

	second, err := LoadOrCreateAuthority(dir, time.Now)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if second.Fingerprint() != first.Fingerprint() {
		t.Fatalf("fingerprint changed on reload: %q -> %q", first.Fingerprint(), second.Fingerprint())
	}
	if !bytes.Equal(second.RootPEM(), first.RootPEM()) {
		t.Fatal("root certificate changed on reload")
	}
	keyAfter, err := os.ReadFile(second.KeyPath())
	if err != nil {
		t.Fatal(err)
	}
	certAfter, err := os.ReadFile(filepath.Join(dir, "root.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keyBefore, keyAfter) || !bytes.Equal(certBefore, certAfter) {
		t.Fatal("CA files were rewritten on reload")
	}
}

func TestLoadOrCreateAuthorityRejectsMismatchedKey(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreateAuthority(dir, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	other := newTestKey(t)
	if err := os.WriteFile(a.KeyPath(), encodeECPrivateKey(t, other), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateAuthority(dir, time.Now); err == nil {
		t.Fatal("mismatched key and certificate accepted")
	}
}

func TestLoadOrCreateAuthorityRejectsCorruptState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string, a *Authority)
	}{
		{
			name: "corrupt certificate",
			mutate: func(t *testing.T, dir string, _ *Authority) {
				if err := os.WriteFile(filepath.Join(dir, "root.crt"), []byte("not a certificate"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "corrupt key",
			mutate: func(t *testing.T, _ string, a *Authority) {
				if err := os.WriteFile(a.KeyPath(), []byte("not a key"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing key",
			mutate: func(t *testing.T, _ string, a *Authority) {
				if err := os.Remove(a.KeyPath()); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing certificate",
			mutate: func(t *testing.T, dir string, _ *Authority) {
				if err := os.Remove(filepath.Join(dir, "root.crt")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			a, err := LoadOrCreateAuthority(dir, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, dir, a)
			if _, err := LoadOrCreateAuthority(dir, time.Now); err == nil {
				t.Fatal("corrupt CA state accepted")
			}
			// A refused load must not replace or "repair" the state.
			_, keyStatErr := os.Stat(a.KeyPath())
			_, certStatErr := os.Stat(filepath.Join(dir, "root.crt"))
			switch tt.name {
			case "missing key":
				if !errors.Is(keyStatErr, os.ErrNotExist) {
					t.Fatalf("missing key was unexpectedly recreated: %v", keyStatErr)
				}
				if certStatErr != nil {
					t.Fatalf("root.crt was removed: %v", certStatErr)
				}
			case "missing certificate":
				if keyStatErr != nil {
					t.Fatalf("root.key was removed: %v", keyStatErr)
				}
				if !errors.Is(certStatErr, os.ErrNotExist) {
					t.Fatalf("missing certificate was unexpectedly recreated: %v", certStatErr)
				}
			default:
				if keyStatErr != nil {
					t.Fatalf("root.key was removed: %v", keyStatErr)
				}
				if certStatErr != nil {
					t.Fatalf("root.crt was removed: %v", certStatErr)
				}
			}
		})
	}
}

func TestSignCSRRejectsNonTestSAN(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	for _, name := range []string{"evil.com", "blog.dev", "*.blog.test", "127.0.0.1", "Blog.test"} {
		csr := makeTestCSR(t, key, name)
		if _, _, _, err := a.SignCSR(csr, []string{name}, 90*24*time.Hour); err == nil {
			t.Errorf("SignCSR accepted SAN %q", name)
		}
	}
}

func TestSignCSRAllowsDevstackTest(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	csr := makeTestCSR(t, key, "devstack.test")
	certPEM, _, _, err := a.SignCSR(csr, []string{"devstack.test"}, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("SignCSR(devstack.test): %v", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(a.RootPEM())
	leaf := parseTestCertificate(t, certPEM)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   "devstack.test",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("verify devstack.test leaf: %v", err)
	}
}

func TestSignCSRRequiresExactSANSet(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	csr := makeTestCSR(t, key, "blog.test", "www.blog.test")
	tests := []struct {
		name    string
		domains []string
	}{
		{"missing CSR name", []string{"blog.test"}},
		{"extra requested name", []string{"blog.test", "www.blog.test", "api.blog.test"}},
		{"different name", []string{"blog.test", "other.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := a.SignCSR(csr, tt.domains, 90*24*time.Hour); err == nil {
				t.Fatal("SAN set mismatch accepted")
			}
		})
	}
}

func TestSignCSRRejectsUnsupportedSANTypes(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	dns := []string{"blog.test"}
	uri, err := url.Parse("https://blog.test/")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		csr  []byte
	}{
		{
			"ip",
			makeCSRFromTemplate(t, key, &x509.CertificateRequest{
				DNSNames:    dns,
				IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
			}),
		},
		{
			"email",
			makeCSRFromTemplate(t, key, &x509.CertificateRequest{
				DNSNames:       dns,
				EmailAddresses: []string{"admin@blog.test"},
			}),
		},
		{
			"uri",
			makeCSRFromTemplate(t, key, &x509.CertificateRequest{
				DNSNames: dns,
				URIs:     []*url.URL{uri},
			}),
		},
		{
			"other name",
			makeCSRFromTemplate(t, key, &x509.CertificateRequest{
				ExtraExtensions: []pkix.Extension{sanExtensionFor(t,
					dnsGeneralName("blog.test"),
					otherGeneralName(0, true),
				)},
			}),
		},
		{
			"directory name",
			makeCSRFromTemplate(t, key, &x509.CertificateRequest{
				ExtraExtensions: []pkix.Extension{sanExtensionFor(t,
					dnsGeneralName("blog.test"),
					otherGeneralName(4, true),
				)},
			}),
		},
		{
			"registered id",
			makeCSRFromTemplate(t, key, &x509.CertificateRequest{
				ExtraExtensions: []pkix.Extension{sanExtensionFor(t,
					dnsGeneralName("blog.test"),
					otherGeneralName(8, false),
				)},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := a.SignCSR(tt.csr, dns, 90*24*time.Hour); err == nil {
				t.Fatalf("SignCSR accepted %s SAN", tt.name)
			}
		})
	}
}

func TestSignCSRRejectsMissingSAN(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	csr := makeTestCSR(t, key)
	if _, _, _, err := a.SignCSR(csr, []string{"blog.test"}, 90*24*time.Hour); err == nil {
		t.Fatal("CSR without SAN accepted")
	}
}

func TestSignCSRRejectsTamperedSignature(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	csr := makeTestCSR(t, key, "blog.test")
	block, _ := pem.Decode(csr)
	if block == nil {
		t.Fatal("CSR did not decode")
	}
	tampered := append([]byte(nil), block.Bytes...)
	tampered[len(tampered)-1] ^= 0xff
	badPEM := pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: tampered})
	if _, _, _, err := a.SignCSR(badPEM, []string{"blog.test"}, 90*24*time.Hour); err == nil {
		t.Fatal("CSR with a tampered signature accepted")
	}
}

func TestSignCSRRejectsInvalidTTL(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	csr := makeTestCSR(t, key, "blog.test")
	for _, ttl := range []time.Duration{0, -time.Hour, 91 * 24 * time.Hour} {
		if _, _, _, err := a.SignCSR(csr, []string{"blog.test"}, ttl); err == nil {
			t.Errorf("SignCSR accepted TTL %v", ttl)
		}
	}
}

func TestSignCSRRejectsInvalidCSRPEM(t *testing.T) {
	a := newTestAuthority(t)
	if _, _, _, err := a.SignCSR([]byte("not a csr"), []string{"blog.test"}, 90*24*time.Hour); err == nil {
		t.Fatal("invalid CSR PEM accepted")
	}
}

func TestVerifyLeaf(t *testing.T) {
	a := newTestAuthority(t)
	key := newTestKey(t)
	domains := []string{"blog.test", "www.blog.test"}
	csr := makeTestCSR(t, key, domains...)
	leafPEM, _, _, err := a.SignCSR(csr, domains, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := encodeECPrivateKey(t, key)

	if err := a.VerifyLeaf(leafPEM, keyPEM, domains); err != nil {
		t.Fatalf("VerifyLeaf: %v", err)
	}
	// A certificate bundle that also carries the root is accepted.
	chain := append(append([]byte(nil), leafPEM...), a.RootPEM()...)
	if err := a.VerifyLeaf(chain, keyPEM, domains); err != nil {
		t.Fatalf("VerifyLeaf with root appended: %v", err)
	}
	// SAN set comparison is order-independent.
	if err := a.VerifyLeaf(leafPEM, keyPEM, []string{"www.blog.test", "blog.test"}); err != nil {
		t.Fatalf("VerifyLeaf with reordered domains: %v", err)
	}

	otherKey := newTestKey(t)
	if err := a.VerifyLeaf(leafPEM, encodeECPrivateKey(t, otherKey), domains); err == nil {
		t.Fatal("VerifyLeaf accepted a mismatched private key")
	}
	if err := a.VerifyLeaf(leafPEM, keyPEM, []string{"blog.test"}); err == nil {
		t.Fatal("VerifyLeaf accepted a SAN set mismatch")
	}
	if err := a.VerifyLeaf(leafPEM, keyPEM, []string{"blog.test", "www.blog.test", "api.blog.test"}); err == nil {
		t.Fatal("VerifyLeaf accepted an extra domain")
	}
	otherAuthority := newTestAuthority(t)
	if err := otherAuthority.VerifyLeaf(leafPEM, keyPEM, domains); err == nil {
		t.Fatal("VerifyLeaf accepted a certificate from another authority")
	}
}

func TestVerifyLeafExpiry(t *testing.T) {
	key := newTestKey(t)
	domains := []string{"blog.test"}
	csr := makeTestCSR(t, key, domains...)
	keyPEM := encodeECPrivateKey(t, key)

	past := time.Now().Add(-100 * 24 * time.Hour)
	expiredAuthority, err := LoadOrCreateAuthority(t.TempDir(), func() time.Time { return past })
	if err != nil {
		t.Fatal(err)
	}
	expiredPEM, _, _, err := expiredAuthority.SignCSR(csr, domains, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := expiredAuthority.VerifyLeaf(expiredPEM, keyPEM, domains); err == nil {
		t.Fatal("VerifyLeaf accepted an expired certificate")
	}

	future := time.Now().Add(100 * 24 * time.Hour)
	futureAuthority, err := LoadOrCreateAuthority(t.TempDir(), func() time.Time { return future })
	if err != nil {
		t.Fatal(err)
	}
	futurePEM, _, _, err := futureAuthority.SignCSR(csr, domains, 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := futureAuthority.VerifyLeaf(futurePEM, keyPEM, domains); err == nil {
		t.Fatal("VerifyLeaf accepted a not-yet-valid certificate")
	}

	// A short-lived but currently valid certificate is accepted.
	shortAuthority := newTestAuthority(t)
	shortPEM, _, notAfter, err := shortAuthority.SignCSR(csr, domains, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(notAfter) > time.Hour {
		t.Fatalf("short leaf notAfter = %v", notAfter)
	}
	if err := shortAuthority.VerifyLeaf(shortPEM, keyPEM, domains); err != nil {
		t.Fatalf("VerifyLeaf rejected a valid short-lived certificate: %v", err)
	}
}
