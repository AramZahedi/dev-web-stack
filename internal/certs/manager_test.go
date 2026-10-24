package certs

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

var errTestSigner = errors.New("test signer is down")

// testClock is the shared clock used by the manager and its test signer.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// testSigner is an in-memory certificate authority implementing Signer.
type testSigner struct {
	clock *testClock

	mu      sync.Mutex
	caKey   *ecdsa.PrivateKey
	caCert  *x509.Certificate
	caPEM   []byte
	ttl     time.Duration
	calls   int
	err     error
	domains []string  // when set, overrides the SANs in the issued leaf
	expires time.Time // when set, overrides the leaf expiry
	lastCSR []byte
}

func newTestSigner(t *testing.T, clock *testClock) *testSigner {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	now := clock.Now().UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "devstack certs test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testSigner{
		clock:  clock,
		caKey:  key,
		caCert: cert,
		caPEM: pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: der,
		}),
		ttl: 90 * 24 * time.Hour,
	}
}

func (s *testSigner) RootPEM() []byte {
	return append([]byte(nil), s.caPEM...)
}

func (s *testSigner) SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error) {
	if err := ctx.Err(); err != nil {
		return helperproto.SignResponse{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return helperproto.SignResponse{}, s.err
	}
	s.calls++
	s.lastCSR = append([]byte(nil), csrPEM...)

	sanDomains := domains
	if s.domains != nil {
		sanDomains = s.domains
	}
	notAfter := s.clock.Now().Add(s.ttl)
	if !s.expires.IsZero() {
		notAfter = s.expires
	}
	return s.signCSRLocked(csrPEM, sanDomains, notAfter)
}

// issueForTest signs csrPEM for sanDomains with an explicit expiry.
func (s *testSigner) issueForTest(t *testing.T, csrPEM []byte, sanDomains []string, notAfter time.Time) helperproto.SignResponse {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	resp, err := s.signCSRLocked(csrPEM, sanDomains, notAfter)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func (s *testSigner) signCSRLocked(csrPEM []byte, domains []string, notAfter time.Time) (helperproto.SignResponse, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return helperproto.SignResponse{}, errors.New("test signer: CSR is not PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return helperproto.SignResponse{}, fmt.Errorf("test signer: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return helperproto.SignResponse{}, fmt.Errorf("test signer: CSR signature: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return helperproto.SignResponse{}, err
	}
	notAfter = notAfter.UTC().Truncate(time.Second)
	notBefore := s.clock.Now().UTC().Truncate(time.Second)
	if !notBefore.Before(notAfter) {
		notBefore = notAfter.Add(-90 * 24 * time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domains[0]},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string(nil), domains...),
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, s.caCert, csr.PublicKey, s.caKey)
	if err != nil {
		return helperproto.SignResponse{}, fmt.Errorf("test signer: sign: %w", err)
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

func (s *testSigner) advance(d time.Duration) {
	s.clock.Advance(d)
}

func (s *testSigner) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *testSigner) setDomains(domains []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.domains = domains
}

func (s *testSigner) setExpiry(expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expires = expires
}

func (s *testSigner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *testSigner) now() time.Time {
	return s.clock.Now()
}

func newTestCertManager(t *testing.T) (*Manager, *testSigner) {
	t.Helper()
	stores, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stores.Close()
	})
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	signer := newTestSigner(t, clock)
	dir := filepath.Join(t.TempDir(), "pki")
	return NewManager(stores, signer, dir, clock.Now), signer
}

// writeTestCert writes a certificate with certDomains and notAfter through the
// manager's test signer. The persisted record deliberately claims the canonical
// blog.test domain, so a certDomains value of anything else exercises the SAN
// mismatch check in Load.
func writeTestCert(t *testing.T, m *Manager, owner string, certDomains []string, notAfter time.Time) model.CertRecord {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, err := createTestCSR(key, certDomains)
	if err != nil {
		t.Fatal(err)
	}
	signer, ok := m.signer.(*testSigner)
	if !ok {
		t.Fatalf("manager signer is %T, want *testSigner", m.signer)
	}
	resp := signer.issueForTest(t, csrPEM, certDomains, notAfter)

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	chain := append(append([]byte(nil), resp.CertPEM...), signer.RootPEM()...)
	keyPath := m.keyPath(owner)
	certPath := m.certPath(owner)
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, chain, 0o644); err != nil {
		t.Fatal(err)
	}
	rec := model.CertRecord{
		SiteID:   owner,
		Serial:   resp.Serial,
		Domains:  []string{"blog.test"},
		NotAfter: resp.NotAfter,
		KeyPath:  keyPath,
		CertPath: certPath,
	}
	if err := m.stores.SaveCert(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func createTestCSR(key *ecdsa.PrivateKey, domains []string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{DNSNames: append([]string(nil), domains...)}
	if len(domains) > 0 {
		tmpl.Subject = pkix.Name{CommonName: domains[0]}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseLeafPEM(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestPEMReturnsCertificateAndPrivateKey(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestCertManager(t)

	rec, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := m.PEM(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(certPEM, readFile(t, rec.CertPath)) {
		t.Fatal("certificate PEM does not match the issued file")
	}
	if !bytes.Equal(keyPEM, readFile(t, rec.KeyPath)) {
		t.Fatal("key PEM does not match the issued file")
	}
	parseLeafPEM(t, certPEM)
	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("key PEM block = %v", block)
	}
}

func TestPEMFallsBackToCanonicalPathsAndRejectsNonRegularFiles(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestCertManager(t)

	rec, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	withoutPaths := rec
	withoutPaths.KeyPath = ""
	withoutPaths.CertPath = ""
	if _, _, err := m.PEM(withoutPaths); err != nil {
		t.Fatalf("canonical fallback: %v", err)
	}

	if err := os.Remove(rec.CertPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.PEM(rec); err == nil {
		t.Fatal("PEM succeeded with a missing certificate file")
	}

	if err := os.Symlink(rec.KeyPath, rec.CertPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.PEM(rec); err == nil {
		t.Fatal("PEM accepted a symlink as a certificate file")
	}
}

func TestEnsureWritesSecureKeyAndVerifiesChain(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)

	rec, err := m.Ensure(ctx, "site1", []string{"blog.test", "www.blog.test"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(rec.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v, want 0600", info.Mode().Perm())
	}
	certInfo, err := os.Stat(rec.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	if certInfo.Mode().Perm() != 0o644 {
		t.Fatalf("cert mode = %v, want 0644", certInfo.Mode().Perm())
	}

	cert, err := m.Load(rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.Certificate) < 2 {
		t.Fatal("chain missing root")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	root := parseLeafPEM(t, signer.RootPEM())
	gotRoot, err := x509.ParseCertificate(cert.Certificate[1])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotRoot.Raw, root.Raw) {
		t.Fatal("served chain does not end with the signer root")
	}

	roots := x509.NewCertPool()
	roots.AddCert(root)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		DNSName:     "blog.test",
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: signer.now(),
	}); err != nil {
		t.Fatalf("leaf does not verify against the signer root: %v", err)
	}

	if !sameStringSet(leaf.DNSNames, []string{"blog.test", "www.blog.test"}) {
		t.Fatalf("leaf SANs = %v", leaf.DNSNames)
	}
	if rec.Serial != leaf.SerialNumber.Text(16) {
		t.Fatalf("record serial = %q, cert serial = %q", rec.Serial, leaf.SerialNumber.Text(16))
	}
	if !rec.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("record NotAfter = %v, cert NotAfter = %v", rec.NotAfter, leaf.NotAfter)
	}
	if !sameStringSet(rec.Domains, []string{"blog.test", "www.blog.test"}) {
		t.Fatalf("record domains = %v", rec.Domains)
	}

	keyPEM := readFile(t, rec.KeyPath)
	block, _ := pem.Decode(keyPEM)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		t.Fatalf("key PEM block = %v", block)
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if key.Curve != elliptic.P256() {
		t.Fatalf("key curve = %v, want P-256", key.Curve)
	}
	if !key.PublicKey.Equal(leaf.PublicKey) {
		t.Fatal("private key does not match the leaf")
	}

	stored, err := m.stores.GetCertBySite(ctx, "site1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Serial != rec.Serial || !stored.NotAfter.Equal(rec.NotAfter) {
		t.Fatalf("stored record = %+v, want %+v", stored, rec)
	}
}

func TestEnsureReusesValidCertificateAndReissuesOnChanges(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)

	first, err := m.Ensure(ctx, "site1", []string{"blog.test", "www.blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	if signer.callCount() != 1 {
		t.Fatalf("signer calls = %d, want 1", signer.callCount())
	}

	second, err := m.Ensure(ctx, "site1", []string{"www.blog.test", "blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Serial != first.Serial {
		t.Fatalf("valid certificate was reissued: serial %q -> %q", first.Serial, second.Serial)
	}
	if signer.callCount() != 1 {
		t.Fatalf("signer calls after reuse = %d, want 1", signer.callCount())
	}

	changed, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Serial == first.Serial {
		t.Fatal("domain change did not reissue the certificate")
	}
	if signer.callCount() != 2 {
		t.Fatalf("signer calls after domain change = %d, want 2", signer.callCount())
	}
	if !sameStringSet(changed.Domains, []string{"blog.test"}) {
		t.Fatalf("reissued domains = %v", changed.Domains)
	}

	// A loosened key mode must be repaired by reissuing, not reused.
	if err := os.Chmod(changed.KeyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	repaired, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Serial == changed.Serial {
		t.Fatal("certificate with a loose key was reused")
	}
	if signer.callCount() != 3 {
		t.Fatalf("signer calls after loose key = %d, want 3", signer.callCount())
	}
	info, err := os.Stat(repaired.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("repaired key mode = %v, want 0600", info.Mode().Perm())
	}

	// Removing a certificate file forces a reissue even when the record is valid.
	if err := os.Remove(repaired.CertPath); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(ctx, "site1", []string{"blog.test"}); err != nil {
		t.Fatal(err)
	}
	if signer.callCount() != 4 {
		t.Fatalf("signer calls after missing cert = %d, want 4", signer.callCount())
	}
}

func TestEnsureSignerFailureLeavesNoFilesOrRecord(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)
	signer.setError(errTestSigner)

	if _, err := m.Ensure(ctx, "site1", []string{"blog.test"}); !errors.Is(err, errTestSigner) {
		t.Fatalf("Ensure error = %v, want the signer failure", err)
	}
	for _, path := range []string{m.keyPath("site1"), m.certPath("site1")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s exists after a signer failure (err=%v)", path, err)
		}
	}
	if _, err := m.stores.GetCertBySite(ctx, "site1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("certificate row exists after a signer failure: %v", err)
	}
	certs, err := m.stores.ListCerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != 0 {
		t.Fatalf("store has %d certificates after a signer failure", len(certs))
	}

	signer.setError(nil)
	if _, err := m.Ensure(ctx, "site1", []string{"blog.test"}); err != nil {
		t.Fatalf("Ensure after signer recovery: %v", err)
	}
}

func TestEnsureRejectsInvalidOwnersAndDomains(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)

	badOwners := []string{"", ".", "..", "../escape", "a/b", `a\b`, "a\x00b", "a\nb"}
	for _, owner := range badOwners {
		if _, err := m.Ensure(ctx, owner, []string{"blog.test"}); err == nil {
			t.Fatalf("Ensure accepted owner %q", owner)
		}
	}
	badDomains := [][]string{
		nil,
		{},
		{"evil.com"},
		{"*.blog.test"},
		{"BLOG.TEST"},
		{"blog_test.test"},
		{""},
		{"devstack.test"},
		{"blog.test", "devstack.test"},
	}
	for _, domains := range badDomains {
		if _, err := m.Ensure(ctx, "site1", domains); err == nil {
			t.Fatalf("Ensure accepted domains %v", domains)
		}
	}
	if got := signer.callCount(); got != 0 {
		t.Fatalf("signer was called %d times for invalid input", got)
	}
	if _, err := m.stores.GetCertBySite(ctx, "site1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid input persisted a certificate: %v", err)
	}

	if _, err := m.Ensure(ctx, DashboardOwner, []string{"devstack.test"}); err != nil {
		t.Fatalf("dashboard certificate: %v", err)
	}
	for _, domains := range [][]string{{"other.test"}, {"devstack.test", "blog.test"}} {
		if _, err := m.Ensure(ctx, DashboardOwner, domains); err == nil {
			t.Fatalf("dashboard owner accepted domains %v", domains)
		}
	}
}

func TestEnsureRejectsInvalidSignerResponses(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)

	signer.setDomains([]string{"blog.test", "evil.test"})
	if _, err := m.Ensure(ctx, "site1", []string{"blog.test"}); err == nil {
		t.Fatal("Ensure accepted a response with unexpected SANs")
	} else if !strings.Contains(err.Error(), "SAN") && !strings.Contains(err.Error(), "subjectAltName") {
		t.Fatalf("error does not describe the SAN mismatch: %v", err)
	}
	signer.setDomains(nil)
	for _, path := range []string{m.keyPath("site1"), m.certPath("site1")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s exists after a bad signer response", path)
		}
	}
	if _, err := m.stores.GetCertBySite(ctx, "site1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bad signer response persisted a row: %v", err)
	}

	signer.setExpiry(signer.now().Add(-time.Hour))
	if _, err := m.Ensure(ctx, "site2", []string{"blog.test"}); err == nil {
		t.Fatal("Ensure accepted an already-expired response")
	}
	signer.setExpiry(time.Time{})
}

func TestLoadVerifiesChainAgainstSignerRootOnly(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)
	rec, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}

	// The served chain must contain the configured root, not an arbitrary one.
	rogue := newTestSigner(t, signer.clock)
	leafPEM := readFile(t, rec.CertPath)
	leafBlock, _ := pem.Decode(leafPEM)
	tampered := append(pem.EncodeToMemory(leafBlock), rogue.RootPEM()...)
	if err := os.WriteFile(rec.CertPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(rec); err == nil {
		t.Fatal("Load accepted a chain whose root is not the configured root")
	}

	// A leaf issued by a different CA, even with the right key and SANs, must fail.
	keyPEM := readFile(t, rec.KeyPath)
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		t.Fatal("key is not PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, err := createTestCSR(key, []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := rogue.issueForTest(t, csrPEM, []string{"blog.test"}, signer.now().Add(90*24*time.Hour))
	foreignChain := append(append([]byte(nil), foreign.CertPEM...), rogue.RootPEM()...)
	if err := os.WriteFile(rec.CertPath, foreignChain, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(rec); err == nil {
		t.Fatal("Load accepted a leaf signed by a foreign CA")
	}

	// A leaf without the root appended is not a complete served chain.
	if _, err := m.Ensure(ctx, "site2", []string{"blog.test"}); err != nil {
		t.Fatal(err)
	}
	rec2, err := m.stores.GetCertBySite(ctx, "site2")
	if err != nil {
		t.Fatal(err)
	}
	chain := readFile(t, rec2.CertPath)
	onlyLeaf, _ := pem.Decode(chain)
	if err := os.WriteFile(rec2.CertPath, pem.EncodeToMemory(onlyLeaf), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(rec2); err == nil {
		t.Fatal("Load accepted a chain without the root")
	}
}

func TestRenewDueRefreshesExpiringCert(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)

	rec, err := m.Ensure(ctx, "site1", []string{"blog.test", "www.blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	oldNotAfter := rec.NotAfter
	signer.advance(61 * 24 * time.Hour)

	renewed, err := m.RenewDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewed) != 1 {
		t.Fatalf("RenewDue returned %d records, want 1", len(renewed))
	}
	if !renewed[0].NotAfter.After(oldNotAfter) {
		t.Fatalf("renewed NotAfter %v is not later than %v", renewed[0].NotAfter, oldNotAfter)
	}
	if signer.callCount() != 2 {
		t.Fatalf("signer calls = %d, want 2", signer.callCount())
	}
	stored, err := m.stores.GetCertBySite(ctx, "site1")
	if err != nil {
		t.Fatal(err)
	}
	if !stored.NotAfter.Equal(renewed[0].NotAfter) {
		t.Fatalf("stored NotAfter = %v, renewed = %v", stored.NotAfter, renewed[0].NotAfter)
	}
	if _, err := m.Load(renewed[0]); err != nil {
		t.Fatalf("renewed certificate does not load: %v", err)
	}
}

func TestRenewDueHonoursThirtyDayWindow(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)

	if _, err := m.Ensure(ctx, "site1", []string{"blog.test"}); err != nil {
		t.Fatal(err)
	}
	signer.advance(10 * 24 * time.Hour)
	renewed, err := m.RenewDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewed) != 0 {
		t.Fatalf("RenewDue renewed %d certificates with 80 days left", len(renewed))
	}

	// Exactly 30 days remaining is not yet due (< is strict).
	signer.advance(50 * 24 * time.Hour)
	renewed, err = m.RenewDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewed) != 0 {
		t.Fatalf("RenewDue renewed %d certificates at exactly 30 days", len(renewed))
	}

	signer.advance(time.Second)
	renewed, err = m.RenewDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewed) != 1 {
		t.Fatalf("RenewDue returned %d records inside the window, want 1", len(renewed))
	}
}

func TestRenewDueKeepsOldCertificateOnFailure(t *testing.T) {
	ctx := context.Background()
	m, signer := newTestCertManager(t)

	rec, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	oldCert := readFile(t, rec.CertPath)
	oldKey := readFile(t, rec.KeyPath)
	signer.advance(61 * 24 * time.Hour)
	signer.setError(errTestSigner)

	renewed, err := m.RenewDue(ctx)
	if !errors.Is(err, errTestSigner) {
		t.Fatalf("RenewDue error = %v, want the signer failure", err)
	}
	if len(renewed) != 0 {
		t.Fatalf("RenewDue returned %d records after a failure", len(renewed))
	}

	stored, err := m.stores.GetCertBySite(ctx, "site1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Serial != rec.Serial || !stored.NotAfter.Equal(rec.NotAfter) {
		t.Fatalf("stored record changed on failed renewal: %+v", stored)
	}
	if got := readFile(t, rec.CertPath); !bytes.Equal(got, oldCert) {
		t.Fatal("certificate file changed on failed renewal")
	}
	if got := readFile(t, rec.KeyPath); !bytes.Equal(got, oldKey) {
		t.Fatal("key file changed on failed renewal")
	}
	if _, err := m.Load(stored); err != nil {
		t.Fatalf("old certificate no longer loads: %v", err)
	}

	signer.setError(nil)
	renewed, err = m.RenewDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(renewed) != 1 {
		t.Fatalf("RenewDue after recovery returned %d records", len(renewed))
	}
	if !renewed[0].NotAfter.After(rec.NotAfter) {
		t.Fatal("recovered renewal did not extend expiry")
	}
}

func TestLoadRejectsExpiredWrongSANAndLooseKey(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestCertManager(t)

	expired := writeTestCert(t, m, "expired", []string{"blog.test"}, time.Now().Add(-time.Hour))
	if _, err := m.Load(expired); err == nil {
		t.Fatal("expired cert accepted")
	}

	good, err := m.Ensure(ctx, "good", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}

	wrong := writeTestCert(t, m, "wrong", []string{"other.test"}, time.Now().Add(90*24*time.Hour))
	if _, err := m.Load(wrong); err == nil {
		t.Fatal("wrong SAN accepted")
	}

	if err := os.Chmod(good.KeyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(good); err == nil {
		t.Fatal("loose key mode accepted")
	}
	if err := os.Chmod(good.KeyPath, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(good); err == nil {
		t.Fatal("read-only key mode accepted")
	}
	if err := os.Chmod(good.KeyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(good); err != nil {
		t.Fatalf("restored key no longer loads: %v", err)
	}
}

func TestLoadRejectsDanglingAndMismatchedRecords(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestCertManager(t)

	rec, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}

	missingKey := rec
	missingKey.KeyPath = filepath.Join(m.dir, "missing.key")
	if _, err := m.Load(missingKey); err == nil {
		t.Fatal("Load accepted a missing key path")
	}

	swapped := rec
	swapped.KeyPath, swapped.CertPath = rec.CertPath, rec.KeyPath
	if _, err := m.Load(swapped); err == nil {
		t.Fatal("Load accepted swapped key and cert paths")
	}

	foreignPath := rec
	foreignPath.KeyPath = filepath.Join(t.TempDir(), "site1.key")
	if _, err := m.Load(foreignPath); err == nil {
		t.Fatal("Load accepted a non-canonical key path")
	}

	stale := rec
	stale.NotAfter = rec.NotAfter.Add(time.Hour)
	if _, err := m.Load(stale); err == nil {
		t.Fatal("Load accepted a record whose NotAfter disagrees with the cert")
	}

	staleSerial := rec
	staleSerial.Serial = "deadbeef"
	if _, err := m.Load(staleSerial); err == nil {
		t.Fatal("Load accepted a record whose serial disagrees with the cert")
	}

	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherDER, err := x509.MarshalECPrivateKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec.KeyPath, pem.EncodeToMemory(&pem.Block{
		Type: "EC PRIVATE KEY", Bytes: otherDER,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load(rec); err == nil {
		t.Fatal("Load accepted a key that does not match the certificate")
	}
}

func TestDeleteRemovesCertificateAndRecord(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestCertManager(t)

	rec, err := m.Ensure(ctx, "site1", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "site1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{rec.KeyPath, rec.CertPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still exists after Delete", path)
		}
	}
	if _, err := m.stores.GetCertBySite(ctx, "site1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("certificate row survived Delete: %v", err)
	}
	if err := m.Delete(ctx, "site1"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}

	rec, err = m.Ensure(ctx, "site2", []string{"blog.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(rec.KeyPath); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "site2"); err != nil {
		t.Fatalf("Delete with a missing file: %v", err)
	}

	if err := m.Delete(ctx, "../escape"); err == nil {
		t.Fatal("Delete accepted an invalid owner")
	}
}

func TestEnsureDashboardCertificate(t *testing.T) {
	ctx := context.Background()
	m, _ := newTestCertManager(t)

	rec, err := m.Ensure(ctx, DashboardOwner, []string{"devstack.test"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.SiteID != DashboardOwner {
		t.Fatalf("dashboard record owner = %q", rec.SiteID)
	}
	if !sameStringSet(rec.Domains, []string{"devstack.test"}) {
		t.Fatalf("dashboard domains = %v", rec.Domains)
	}
	cert, err := m.Load(rec)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(leaf.DNSNames, []string{"devstack.test"}) {
		t.Fatalf("dashboard SANs = %v", leaf.DNSNames)
	}
	if filepath.Base(rec.KeyPath) != DashboardOwner+".key" {
		t.Fatalf("dashboard key path = %q", rec.KeyPath)
	}
}
