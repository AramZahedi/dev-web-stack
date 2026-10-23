// Package certs issues, stores, loads, renews, and deletes the per-site leaf
// certificates served by the proxy. Private keys never leave this package's
// directory and the signing key never leaves the privileged helper.
package certs

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/store"
)

const (
	// DashboardOwner is the certificate-store owner used for the dashboard's
	// reserved devstack.test certificate. It is not a site id.
	DashboardOwner = "__dashboard__"

	// dashboardDomain is the only name the dashboard certificate may cover.
	dashboardDomain = "devstack.test"

	// renewalWindow is the remaining lifetime at which RenewDue reissues a
	// certificate. Leaves are issued with at most 90 days of validity.
	renewalWindow = 30 * 24 * time.Hour

	keyFileMode  fs.FileMode = 0o600
	certFileMode fs.FileMode = 0o644
)

// Signer signs a PEM certificate request for exactly domains and exposes the
// root certificate that must be the only trust anchor for the resulting chain.
type Signer interface {
	SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error)
	RootPEM() []byte
}

// Manager owns the on-disk certificate material and its store metadata.
// Mutating operations are serialized; Load may run concurrently with them and
// never observes a half-replaced key/certificate pair.
type Manager struct {
	stores *store.Store
	signer Signer
	dir    string
	now    func() time.Time

	mu sync.RWMutex
}

// NewManager returns a certificate manager rooted at dir. The directory is
// created on the first Ensure. A nil now falls back to time.Now.
func NewManager(stores *store.Store, signer Signer, dir string, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		} else {
			dir = filepath.Clean(dir)
		}
	}
	return &Manager{
		stores: stores,
		signer: signer,
		dir:    dir,
		now:    now,
	}
}

// Ensure returns a currently valid certificate for owner and domains. It
// reuses an existing record only when the files still load, match the requested
// SAN set, and have not expired; otherwise it issues and stores a new leaf.
func (m *Manager) Ensure(ctx context.Context, owner string, domains []string) (model.CertRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	owner, wanted, err := m.prepare(owner, domains)
	if err != nil {
		return model.CertRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.CertRecord{}, err
	}

	rec, err := m.stores.GetCertBySite(ctx, owner)
	switch {
	case err == nil:
		if rec.Serial != "" && sameStringSet(rec.Domains, wanted) {
			if _, loadErr := m.loadLocked(rec); loadErr == nil {
				return rec, nil
			}
		}
	case errors.Is(err, store.ErrNotFound):
		// Issue a fresh certificate below.
	default:
		return model.CertRecord{}, fmt.Errorf("certs: read certificate for %q: %w", owner, err)
	}
	return m.issueLocked(ctx, owner, wanted)
}

// RenewDue reissues every stored certificate whose expiry is inside the
// 30-day renewal window. Certificates are renewed independently; a failure
// keeps the old files and record and is reported in the returned error.
func (m *Manager) RenewDue(ctx context.Context) ([]model.CertRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkWritable(); err != nil {
		return nil, err
	}
	certs, err := m.stores.ListCerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("certs: list certificates: %w", err)
	}
	cutoff := m.currentTime().Add(renewalWindow)
	renewed := make([]model.CertRecord, 0, len(certs))
	var errs []error
	for _, rec := range certs {
		if !rec.NotAfter.Before(cutoff) {
			continue
		}
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		owner, wanted, err := m.prepare(rec.SiteID, rec.Domains)
		if err != nil {
			errs = append(errs, fmt.Errorf("certs: renew %q: %w", rec.SiteID, err))
			continue
		}
		updated, err := m.issueLocked(ctx, owner, wanted)
		if err != nil {
			errs = append(errs, fmt.Errorf("certs: renew %q: %w", rec.SiteID, err))
			continue
		}
		renewed = append(renewed, updated)
	}
	return renewed, errors.Join(errs...)
}

// Load verifies and returns the certificate described by rec. It requires a
// canonical path pair, a regular key file with exactly mode 0600, an ECDSA
// P-256 key matching the leaf, the exact record SAN set, the record serial and
// expiry, a complete leaf-plus-root chain, and a chain that verifies against
// Signer.RootPEM as its only root.
func (m *Manager) Load(rec model.CertRecord) (*tls.Certificate, error) {
	if m == nil {
		return nil, errors.New("certs: nil manager")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loadLocked(rec)
}

// Delete removes the certificate files and store row for owner. Missing files
// or a missing row are not errors, so Delete is idempotent.
func (m *Manager) Delete(ctx context.Context, owner string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m == nil {
		return errors.New("certs: nil manager")
	}
	if err := validateOwner(owner); err != nil {
		return err
	}
	if m.stores == nil {
		return errors.New("certs: store is required")
	}
	if m.dir == "" {
		return errors.New("certs: certificate directory is empty")
	}

	paths := []string{m.keyPath(owner), m.certPath(owner)}
	rec, err := m.stores.GetCertBySite(ctx, owner)
	switch {
	case err == nil:
		for _, path := range []string{rec.KeyPath, rec.CertPath} {
			if path != "" && filepath.Dir(filepath.Clean(path)) == m.dir {
				paths = append(paths, path)
			}
		}
	case errors.Is(err, store.ErrNotFound):
		// The canonical paths still need removal for an orphaned row.
	default:
		return fmt.Errorf("certs: read certificate for %q: %w", owner, err)
	}

	var errs []error
	for _, path := range uniquePaths(paths) {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("certs: remove %s: %w", path, err))
		}
	}
	if err := m.stores.DeleteCertBySite(ctx, owner); err != nil && !errors.Is(err, store.ErrNotFound) {
		errs = append(errs, fmt.Errorf("certs: delete certificate row for %q: %w", owner, err))
	}
	if len(errs) == 0 {
		syncDir(m.dir)
	}
	return errors.Join(errs...)
}

// prepare validates an owner/domain request and returns the canonical owner
// and deduplicated SAN list.
func (m *Manager) prepare(owner string, domains []string) (string, []string, error) {
	if err := m.checkWritable(); err != nil {
		return "", nil, err
	}
	if err := validateOwner(owner); err != nil {
		return "", nil, err
	}
	wanted, err := validateDomains(owner, domains)
	if err != nil {
		return "", nil, err
	}
	return owner, wanted, nil
}

func (m *Manager) checkWritable() error {
	if m == nil {
		return errors.New("certs: nil manager")
	}
	if m.stores == nil {
		return errors.New("certs: store is required")
	}
	if m.signer == nil {
		return errors.New("certs: signer is required")
	}
	if m.dir == "" {
		return errors.New("certs: certificate directory is empty")
	}
	return nil
}

// issueLocked performs one issuance. The caller must hold m.mu for writing.
func (m *Manager) issueLocked(ctx context.Context, owner string, domains []string) (model.CertRecord, error) {
	owner, wanted, err := m.prepare(owner, domains)
	if err != nil {
		return model.CertRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.CertRecord{}, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: generate key for %q: %w", owner, err)
	}
	csrPEM, err := createCSR(key, wanted)
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: create CSR for %q: %w", owner, err)
	}
	rootPEM := m.signer.RootPEM()
	resp, err := m.signer.SignCertificate(ctx, csrPEM, wanted)
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: sign certificate for %q: %w", owner, err)
	}
	leaf, roots, err := verifyIssued(resp, &key.PublicKey, wanted, m.currentTime(), rootPEM)
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: verify issued certificate for %q: %w", owner, err)
	}
	if err := ctx.Err(); err != nil {
		return model.CertRecord{}, err
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: marshal key for %q: %w", owner, err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if len(keyPEM) == 0 {
		return model.CertRecord{}, fmt.Errorf("certs: encode key for %q", owner)
	}
	certPEM := encodeChain(leaf, roots)

	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: create certificate directory %s: %w", m.dir, err)
	}
	keyPath := m.keyPath(owner)
	certPath := m.certPath(owner)
	oldKey, err := snapshotFile(keyPath)
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: inspect existing key for %q: %w", owner, err)
	}
	oldCert, err := snapshotFile(certPath)
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: inspect existing certificate for %q: %w", owner, err)
	}
	if err := installFiles(keyPath, keyPEM, certPath, certPEM, oldKey); err != nil {
		return model.CertRecord{}, fmt.Errorf("certs: write certificate for %q: %w", owner, err)
	}

	rec := model.CertRecord{
		SiteID:   owner,
		Serial:   strings.TrimSpace(resp.Serial),
		Domains:  append([]string(nil), wanted...),
		NotAfter: leaf.NotAfter.UTC(),
		KeyPath:  keyPath,
		CertPath: certPath,
	}
	if err := m.stores.SaveCert(ctx, rec); err != nil {
		rollback := errors.Join(oldKey.restore(), oldCert.restore())
		return model.CertRecord{}, errors.Join(
			fmt.Errorf("certs: save certificate for %q: %w", owner, err), rollback)
	}
	return rec, nil
}

// loadLocked verifies rec. The caller must hold m.mu.
func (m *Manager) loadLocked(rec model.CertRecord) (*tls.Certificate, error) {
	if m == nil {
		return nil, errors.New("certs: nil manager")
	}
	if m.signer == nil {
		return nil, errors.New("certs: signer is required")
	}
	if m.dir == "" {
		return nil, errors.New("certs: certificate directory is empty")
	}
	if err := validateOwner(rec.SiteID); err != nil {
		return nil, err
	}
	domains, err := validateDomains(rec.SiteID, rec.Domains)
	if err != nil {
		return nil, err
	}
	keyPath := m.keyPath(rec.SiteID)
	certPath := m.certPath(rec.SiteID)
	if rec.KeyPath == "" || filepath.Clean(rec.KeyPath) != keyPath {
		return nil, fmt.Errorf("certs: key path %q for %q is not canonical (%q)", rec.KeyPath, rec.SiteID, keyPath)
	}
	if rec.CertPath == "" || filepath.Clean(rec.CertPath) != certPath {
		return nil, fmt.Errorf("certs: certificate path %q for %q is not canonical (%q)", rec.CertPath, rec.SiteID, certPath)
	}

	key, err := readPrivateKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("certs: load key for %q: %w", rec.SiteID, err)
	}
	chainPEM, err := readRegularFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("certs: load certificate for %q: %w", rec.SiteID, err)
	}
	chain, err := parseCertificatePEM(chainPEM)
	if err != nil {
		return nil, fmt.Errorf("certs: parse certificate for %q: %w", rec.SiteID, err)
	}
	if len(chain) < 2 {
		return nil, fmt.Errorf("certs: certificate for %q does not include the root", rec.SiteID)
	}
	leaf := chain[0]
	if leaf.IsCA {
		return nil, fmt.Errorf("certs: certificate for %q is a CA certificate", rec.SiteID)
	}
	if !key.PublicKey.Equal(leaf.PublicKey) {
		return nil, fmt.Errorf("certs: private key for %q does not match the certificate", rec.SiteID)
	}
	if err := checkLeafSANs(leaf, domains); err != nil {
		return nil, fmt.Errorf("certs: certificate for %q: %w", rec.SiteID, err)
	}
	if err := checkRecordMetadata(rec, leaf); err != nil {
		return nil, err
	}

	rootPEM := m.signer.RootPEM()
	roots, rootCerts, err := parseRootPEM(rootPEM)
	if err != nil {
		return nil, fmt.Errorf("certs: parse signer root: %w", err)
	}
	if err := checkServedRoots(chain[1:], rootCerts); err != nil {
		return nil, fmt.Errorf("certs: certificate for %q: %w", rec.SiteID, err)
	}

	now := m.currentTime()
	if now.Before(leaf.NotBefore) {
		return nil, fmt.Errorf("certs: certificate for %q is not valid before %v", rec.SiteID, leaf.NotBefore)
	}
	if !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("certs: certificate for %q expired at %v", rec.SiteID, leaf.NotAfter)
	}

	intermediates := x509.NewCertPool()
	for _, cert := range chain[1:] {
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       domains[0],
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime:   now,
	}); err != nil {
		return nil, fmt.Errorf("certs: verify certificate for %q: %w", rec.SiteID, err)
	}

	raw := make([][]byte, len(chain))
	for i, cert := range chain {
		raw[i] = cert.Raw
	}
	return &tls.Certificate{
		Certificate: raw,
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

func (m *Manager) currentTime() time.Time {
	if m != nil && m.now != nil {
		if now := m.now(); !now.IsZero() {
			return now.UTC()
		}
	}
	return time.Now().UTC()
}

func (m *Manager) keyPath(owner string) string {
	return filepath.Join(m.dir, owner+".key")
}

func (m *Manager) certPath(owner string) string {
	return filepath.Join(m.dir, owner+".crt")
}

// validateOwner rejects ids that could escape the certificate directory or
// create ambiguous file names.
func validateOwner(owner string) error {
	switch {
	case owner == "":
		return errors.New("certs: empty certificate owner")
	case owner == "." || owner == "..":
		return fmt.Errorf("certs: invalid certificate owner %q", owner)
	case strings.ContainsAny(owner, `/\`) || strings.ContainsRune(owner, 0):
		return fmt.Errorf("certs: invalid certificate owner %q", owner)
	case filepath.Base(owner) != owner:
		return fmt.Errorf("certs: invalid certificate owner %q", owner)
	}
	for _, r := range owner {
		if unicode.IsControl(r) {
			return fmt.Errorf("certs: invalid certificate owner %q", owner)
		}
	}
	return nil
}

// validateDomains normalizes a SAN request. Site certificates use the
// site-domain rules (devstack.test is reserved); the dashboard certificate must
// cover exactly devstack.test.
func validateDomains(owner string, domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, errors.New("certs: no domains requested")
	}
	out := make([]string, 0, len(domains))
	seen := make(map[string]struct{}, len(domains))
	for _, name := range domains {
		var err error
		if owner == DashboardOwner {
			err = domain.ValidateDomain(name)
		} else {
			err = domain.ValidateSiteDomain(name)
		}
		if err != nil {
			return nil, fmt.Errorf("certs: invalid domain %q for %q: %w", name, owner, err)
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if owner == DashboardOwner && (len(out) != 1 || out[0] != dashboardDomain) {
		return nil, fmt.Errorf("certs: dashboard certificate must cover exactly %q", dashboardDomain)
	}
	return out, nil
}

func createCSR(key *ecdsa.PrivateKey, domains []string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{
		DNSNames: append([]string(nil), domains...),
	}
	if len(domains) > 0 && len(domains[0]) <= 64 {
		tmpl.Subject = pkix.Name{CommonName: domains[0]}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// verifyIssued checks a signing response against the CSR key, requested SANs,
// validity window, and root bundle. It returns the parsed leaf and the root
// certificates in the order they should be served.
func verifyIssued(resp helperproto.SignResponse, publicKey *ecdsa.PublicKey, domains []string, now time.Time, rootPEM []byte) (*x509.Certificate, []*x509.Certificate, error) {
	if len(bytes.TrimSpace(resp.CertPEM)) == 0 {
		return nil, nil, errors.New("signer returned no certificate")
	}
	leaf, err := parseLeafResponse(resp.CertPEM)
	if err != nil {
		return nil, nil, err
	}
	if leaf.IsCA {
		return nil, nil, errors.New("signer returned a CA certificate instead of a leaf")
	}
	if !publicKey.Equal(leaf.PublicKey) {
		return nil, nil, errors.New("certificate public key does not match the generated key")
	}
	if err := checkLeafSANs(leaf, domains); err != nil {
		return nil, nil, err
	}
	if now.Before(leaf.NotBefore) {
		return nil, nil, fmt.Errorf("certificate is not valid before %v", leaf.NotBefore)
	}
	if !now.Before(leaf.NotAfter) {
		return nil, nil, fmt.Errorf("certificate expired at %v", leaf.NotAfter)
	}
	if resp.NotAfter.IsZero() {
		return nil, nil, errors.New("signer returned an empty NotAfter")
	}
	if diff := leaf.NotAfter.Sub(resp.NotAfter); diff < -time.Second || diff > time.Second {
		return nil, nil, fmt.Errorf("signer NotAfter %v does not match certificate expiry %v", resp.NotAfter, leaf.NotAfter)
	}
	serial, ok := new(big.Int).SetString(strings.TrimSpace(resp.Serial), 16)
	if !ok || serial.Cmp(leaf.SerialNumber) != 0 {
		return nil, nil, fmt.Errorf("signer serial %q does not match certificate serial %s", resp.Serial, leaf.SerialNumber.Text(16))
	}

	roots, rootCerts, err := parseRootPEM(rootPEM)
	if err != nil {
		return nil, nil, err
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		DNSName:     domains[0],
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: now,
	}); err != nil {
		return nil, nil, fmt.Errorf("verify certificate chain: %w", err)
	}
	return leaf, rootCerts, nil
}

func checkLeafSANs(leaf *x509.Certificate, domains []string) error {
	if len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 || len(leaf.URIs) != 0 {
		return errors.New("certificate contains non-DNS subjectAltName entries")
	}
	if !sameStringSet(leaf.DNSNames, domains) {
		return fmt.Errorf("certificate subjectAltName set %v does not match requested domains %v",
			sortedCopy(leaf.DNSNames), sortedCopy(domains))
	}
	return nil
}

func checkRecordMetadata(rec model.CertRecord, leaf *x509.Certificate) error {
	serial, ok := new(big.Int).SetString(strings.TrimSpace(rec.Serial), 16)
	if !ok || serial.Cmp(leaf.SerialNumber) != 0 {
		return fmt.Errorf("certs: record serial %q does not match certificate serial %s",
			rec.Serial, leaf.SerialNumber.Text(16))
	}
	if rec.NotAfter.IsZero() || !rec.NotAfter.Equal(leaf.NotAfter) {
		return fmt.Errorf("certs: record expiry %v does not match certificate expiry %v", rec.NotAfter, leaf.NotAfter)
	}
	return nil
}

// checkServedRoots requires the served chain to consist only of certificates
// from the configured root bundle. A leaf-plus-arbitrary-root chain would
// verify locally against RootPEM but fail for clients that trust only RootPEM.
func checkServedRoots(served, roots []*x509.Certificate) error {
	allowed := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		allowed[string(root.Raw)] = struct{}{}
	}
	for _, cert := range served {
		if _, ok := allowed[string(cert.Raw)]; !ok {
			return fmt.Errorf("served chain contains %q which is not a configured root", cert.Subject.CommonName)
		}
	}
	return nil
}

func encodeChain(leaf *x509.Certificate, roots []*x509.Certificate) []byte {
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})
	for _, root := range roots {
		_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: root.Raw})
	}
	return buf.Bytes()
}

func parseLeafResponse(data []byte) (*x509.Certificate, error) {
	certs, err := parseCertificatePEM(data)
	if err != nil {
		return nil, err
	}
	if len(certs) != 1 {
		return nil, fmt.Errorf("signer returned %d certificates, want exactly one leaf", len(certs))
	}
	return certs[0], nil
}

func parseRootPEM(data []byte) (*x509.CertPool, []*x509.Certificate, error) {
	certs, err := parseCertificatePEM(data)
	if err != nil {
		return nil, nil, err
	}
	if len(certs) == 0 {
		return nil, nil, errors.New("signer returned an empty root bundle")
	}
	pool := x509.NewCertPool()
	for _, cert := range certs {
		if !cert.IsCA {
			return nil, nil, fmt.Errorf("root bundle contains non-CA certificate %q", cert.Subject.CommonName)
		}
		pool.AddCert(cert)
	}
	return pool, certs, nil
}

// parseCertificatePEM decodes one or more CERTIFICATE blocks and rejects every
// other PEM block type, trailing text, and malformed certificates.
func parseCertificatePEM(data []byte) ([]*x509.Certificate, error) {
	rest := bytes.TrimSpace(data)
	var certs []*x509.Certificate
	for len(rest) > 0 {
		if !bytes.HasPrefix(rest, []byte("-----BEGIN ")) {
			return nil, errors.New("certificate PEM contains trailing data")
		}
		block, remaining := pem.Decode(rest)
		if block == nil {
			return nil, errors.New("certificate PEM is malformed")
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unexpected PEM block type %q in a certificate chain", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse certificate: %w", err)
		}
		certs = append(certs, cert)
		rest = bytes.TrimSpace(remaining)
	}
	return certs, nil
}

func readPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := readRegularFile(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm() != keyFileMode {
		return nil, fmt.Errorf("key mode is %04o; want 0600", info.Mode().Perm())
	}

	rest := data
	var key *ecdsa.PrivateKey
	for len(bytes.TrimSpace(rest)) > 0 {
		block, remaining := pem.Decode(rest)
		if block == nil {
			return nil, errors.New("private key is not PEM encoded")
		}
		rest = remaining
		switch block.Type {
		case "EC PARAMETERS":
			continue
		case "EC PRIVATE KEY":
			if key != nil {
				return nil, errors.New("private key file contains multiple keys")
			}
			parsed, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse EC private key: %w", err)
			}
			key = parsed
		case "PRIVATE KEY":
			if key != nil {
				return nil, errors.New("private key file contains multiple keys")
			}
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("parse PKCS#8 private key: %w", err)
			}
			var ok bool
			key, ok = parsed.(*ecdsa.PrivateKey)
			if !ok {
				return nil, fmt.Errorf("PKCS#8 key is %T, want ECDSA", parsed)
			}
		default:
			return nil, fmt.Errorf("unexpected PEM block type %q for a private key", block.Type)
		}
	}
	if key == nil {
		return nil, errors.New("private key file contains no key")
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("private key is not ECDSA P-256")
	}
	return key, nil
}

// readRegularFile reads path after confirming it is a regular file. Rejecting
// symlinks and special files keeps certificate material inside the manager
// directory.
func readRegularFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.ReadFile(path)
}

type fileSnapshot struct {
	path   string
	data   []byte
	mode   fs.FileMode
	exists bool
}

func snapshotFile(path string) (fileSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fileSnapshot{path: path}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{
		path:   path,
		data:   data,
		mode:   info.Mode().Perm(),
		exists: true,
	}, nil
}

func (s fileSnapshot) restore() error {
	if !s.exists {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeAtomicFile(s.path, s.data, s.mode)
}

// installFiles atomically replaces the key and certificate pair. Both files are
// staged first; if the certificate rename fails, the previous key is restored
// so the on-disk pair is never mismatched.
func installFiles(keyPath string, keyPEM []byte, certPath string, certPEM []byte, oldKey fileSnapshot) error {
	keyTmp, err := stageFile(keyPath, keyPEM, keyFileMode)
	if err != nil {
		return err
	}
	certTmp, err := stageFile(certPath, certPEM, certFileMode)
	if err != nil {
		_ = os.Remove(keyTmp)
		return err
	}
	if err := os.Rename(keyTmp, keyPath); err != nil {
		_ = os.Remove(keyTmp)
		_ = os.Remove(certTmp)
		return err
	}
	if err := os.Rename(certTmp, certPath); err != nil {
		_ = os.Remove(certTmp)
		return errors.Join(err, oldKey.restore())
	}
	syncDir(filepath.Dir(keyPath))
	return nil
}

func stageFile(path string, data []byte, perm fs.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	if err := f.Chmod(perm); err != nil {
		cleanup()
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func writeAtomicFile(path string, data []byte, perm fs.FileMode) error {
	tmp, err := stageFile(path, data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

func syncDir(dir string) {
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

func sameStringSet(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, value := range a {
		set[value] = struct{}{}
	}
	other := make(map[string]struct{}, len(b))
	for _, value := range b {
		other[value] = struct{}{}
	}
	if len(set) != len(other) {
		return false
	}
	for value := range set {
		if _, ok := other[value]; !ok {
			return false
		}
	}
	return true
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
