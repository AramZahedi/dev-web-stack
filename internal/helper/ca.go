package helper

import (
	"bytes"
	"crypto"
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
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

const (
	rootKeyFile     = "root.key"
	rootCertFile    = "root.crt"
	rootCommonName  = "devstack local CA"
	maxLeafValidity = 90 * 24 * time.Hour
)

var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

// Authority is the helper's local certificate authority. It owns the CA
// private key; callers only ever receive the root certificate.
type Authority struct {
	dir         string
	now         func() time.Time
	cert        *x509.Certificate
	key         crypto.Signer
	certPEM     []byte
	fingerprint string
}

// LoadOrCreateAuthority loads the CA in dir, creating it first when both
// root.key and root.crt are absent. Corrupt or mismatched state is refused
// rather than overwritten.
func LoadOrCreateAuthority(dir string, now func() time.Time) (*Authority, error) {
	if dir == "" {
		return nil, errors.New("helper: CA directory is empty")
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("helper: create CA directory %s: %w", dir, err)
	}

	keyPath := filepath.Join(dir, rootKeyFile)
	certPath := filepath.Join(dir, rootCertFile)
	keyPEM, keyExists, err := readOptionalFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("helper: read CA key: %w", err)
	}
	certPEM, certExists, err := readOptionalFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("helper: read CA certificate: %w", err)
	}

	switch {
	case !keyExists && !certExists:
		return createAuthority(dir, now)
	case keyExists && certExists:
		return loadAuthority(dir, now, keyPEM, certPEM)
	default:
		return nil, fmt.Errorf(
			"helper: CA state in %s is incomplete (%s exists=%t, %s exists=%t); refusing to overwrite it",
			dir, rootKeyFile, keyExists, rootCertFile, certExists)
	}
}

// RootPEM returns a copy of the root certificate in PEM form.
func (a *Authority) RootPEM() []byte {
	if a == nil {
		return nil
	}
	return append([]byte(nil), a.certPEM...)
}

// Fingerprint returns the lowercase SHA-256 hex fingerprint of the root
// certificate.
func (a *Authority) Fingerprint() string {
	if a == nil {
		return ""
	}
	return a.fingerprint
}

// KeyPath returns the filesystem path of the CA private key.
func (a *Authority) KeyPath() string {
	if a == nil {
		return ""
	}
	return filepath.Join(a.dir, rootKeyFile)
}

// SignCSR signs csrPEM for exactly the requested domains. It returns the leaf
// certificate PEM, its serial number in lowercase hex, and its expiry.
func (a *Authority) SignCSR(csrPEM []byte, domains []string, ttl time.Duration) ([]byte, string, time.Time, error) {
	if a == nil || a.cert == nil || a.key == nil {
		return nil, "", time.Time{}, errors.New("helper: certificate authority is not initialized")
	}
	if ttl <= 0 {
		return nil, "", time.Time{}, fmt.Errorf("helper: certificate validity %v must be positive", ttl)
	}
	if ttl > maxLeafValidity {
		return nil, "", time.Time{}, fmt.Errorf("helper: certificate validity %v exceeds the %v maximum", ttl, maxLeafValidity)
	}
	wanted, err := normalizeDomains(domains)
	if err != nil {
		return nil, "", time.Time{}, err
	}

	block, rest := pem.Decode(csrPEM)
	if block == nil {
		return nil, "", time.Time{}, errors.New("helper: CSR is not PEM encoded")
	}
	if block.Type != "CERTIFICATE REQUEST" && block.Type != "NEW CERTIFICATE REQUEST" {
		return nil, "", time.Time{}, fmt.Errorf("helper: unexpected PEM block type %q for a CSR", block.Type)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, "", time.Time{}, errors.New("helper: CSR PEM contains trailing data")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("helper: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", time.Time{}, fmt.Errorf("helper: CSR signature is invalid: %w", err)
	}
	got, err := sanDNSNames(csr.Extensions, "certificate request")
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if !sameStringSet(got, wanted) {
		return nil, "", time.Time{}, fmt.Errorf(
			"helper: CSR subjectAltName set %v does not match requested domains %v", sortedCopy(got), sortedCopy(wanted))
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, "", time.Time{}, err
	}
	now := time.Now()
	if a.now != nil {
		now = a.now()
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC().Truncate(time.Second)
	notAfter := now.Add(ttl)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: leafCommonName(wanted[0])},
		NotBefore:             now,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              wanted,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, csr.PublicKey, a.key)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("helper: sign certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), serial.Text(16), notAfter, nil
}

// VerifyLeaf verifies that certPEM is a currently valid server certificate
// issued by this authority for exactly domains and that keyPEM matches it.
func (a *Authority) VerifyLeaf(certPEM, keyPEM []byte, domains []string) error {
	if a == nil || a.cert == nil {
		return errors.New("helper: certificate authority is not initialized")
	}
	cert, err := parseLeafCertificatePEM(certPEM)
	if err != nil {
		return err
	}
	signer, err := decodePrivateKeyPEM(keyPEM)
	if err != nil {
		return err
	}
	if !publicKeysEqual(cert.PublicKey, signer.Public()) {
		return errors.New("helper: private key does not match the certificate")
	}
	if cert.IsCA {
		return errors.New("helper: certificate is a CA certificate, not a leaf")
	}
	wanted, err := normalizeDomains(domains)
	if err != nil {
		return err
	}
	got, err := sanDNSNames(cert.Extensions, "certificate")
	if err != nil {
		return err
	}
	if !sameStringSet(got, wanted) {
		return fmt.Errorf(
			"helper: certificate subjectAltName set %v does not match requested domains %v", sortedCopy(got), sortedCopy(wanted))
	}

	now := time.Now()
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("helper: certificate is not valid before %v", cert.NotBefore)
	}
	if !now.Before(cert.NotAfter) {
		return fmt.Errorf("helper: certificate expired at %v", cert.NotAfter)
	}
	roots := x509.NewCertPool()
	roots.AddCert(a.cert)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots:       roots,
		DNSName:     wanted[0],
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: now,
	}); err != nil {
		return fmt.Errorf("helper: verify certificate chain: %w", err)
	}
	return nil
}

func createAuthority(dir string, now func() time.Time) (*Authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("helper: generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	current := now()
	if current.IsZero() {
		current = time.Now()
	}
	current = current.UTC().Truncate(time.Second)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: rootCommonName},
		NotBefore:             current,
		NotAfter:              current.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("helper: create root certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("helper: parse generated root certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("helper: marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := writeAuthorityFiles(dir, keyPEM, certPEM); err != nil {
		return nil, err
	}
	return newAuthority(dir, now, cert, key, certPEM), nil
}

func loadAuthority(dir string, now func() time.Time, keyPEM, certPEM []byte) (*Authority, error) {
	cert, err := parseCertificatePEM(certPEM)
	if err != nil {
		return nil, fmt.Errorf("helper: CA certificate is corrupt: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("helper: CA certificate is not a CA certificate")
	}
	signer, err := decodePrivateKeyPEM(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("helper: CA key is corrupt: %w", err)
	}
	key, ok := signer.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("helper: CA key is %T, want ECDSA", signer)
	}
	if !publicKeysEqual(cert.PublicKey, &key.PublicKey) {
		return nil, errors.New("helper: CA private key does not match the root certificate")
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		return nil, fmt.Errorf("helper: CA root certificate is not self-signed correctly: %w", err)
	}
	return newAuthority(dir, now, cert, key, certPEM), nil
}

func newAuthority(dir string, now func() time.Time, cert *x509.Certificate, key *ecdsa.PrivateKey, certPEM []byte) *Authority {
	sum := sha256.Sum256(cert.Raw)
	return &Authority{
		dir:         dir,
		now:         now,
		cert:        cert,
		key:         key,
		certPEM:     append([]byte(nil), certPEM...),
		fingerprint: hex.EncodeToString(sum[:]),
	}
}

func writeAuthorityFiles(dir string, keyPEM, certPEM []byte) error {
	keyPath := filepath.Join(dir, rootKeyFile)
	certPath := filepath.Join(dir, rootCertFile)
	keyTmp, err := stageAtomicFile(keyPath, keyPEM, 0o600)
	if err != nil {
		return fmt.Errorf("helper: stage CA key: %w", err)
	}
	certTmp, err := stageAtomicFile(certPath, certPEM, 0o644)
	if err != nil {
		_ = os.Remove(keyTmp)
		return fmt.Errorf("helper: stage CA certificate: %w", err)
	}
	if err := os.Rename(keyTmp, keyPath); err != nil {
		_ = os.Remove(keyTmp)
		_ = os.Remove(certTmp)
		return fmt.Errorf("helper: install CA key: %w", err)
	}
	if err := os.Rename(certTmp, certPath); err != nil {
		// Do not leave a key without its certificate behind.
		_ = os.Remove(keyPath)
		_ = os.Remove(certTmp)
		return fmt.Errorf("helper: install CA certificate: %w", err)
	}
	syncDir(dir)
	return nil
}

func readOptionalFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return data, true, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	default:
		return nil, false, err
	}
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("helper: generate serial number: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	return serial, nil
}

func normalizeDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, errors.New("helper: no domains requested")
	}
	out := make([]string, 0, len(domains))
	seen := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		if err := domain.ValidateDomain(d); err != nil {
			return nil, fmt.Errorf("helper: invalid domain %q: %w", d, err)
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out, nil
}

// sanDNSNames extracts the DNS subjectAltName entries from extensions and
// rejects every other GeneralName type. Only DNS names are valid for local
// development certificates.
func sanDNSNames(extensions []pkix.Extension, kind string) ([]string, error) {
	var san *pkix.Extension
	for i := range extensions {
		if !extensions[i].Id.Equal(oidSubjectAltName) {
			continue
		}
		if san != nil {
			return nil, fmt.Errorf("helper: %s has multiple subjectAltName extensions", kind)
		}
		san = &extensions[i]
	}
	if san == nil {
		return nil, fmt.Errorf("helper: %s has no subjectAltName extension", kind)
	}

	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(san.Value, &sequence)
	if err != nil {
		return nil, fmt.Errorf("helper: %s subjectAltName is malformed: %w", kind, err)
	}
	if len(rest) != 0 || sequence.Class != asn1.ClassUniversal ||
		sequence.Tag != asn1.TagSequence || !sequence.IsCompound {
		return nil, fmt.Errorf("helper: %s subjectAltName is not a SEQUENCE", kind)
	}

	var names []string
	data := sequence.Bytes
	for len(data) > 0 {
		var name asn1.RawValue
		data, err = asn1.Unmarshal(data, &name)
		if err != nil {
			return nil, fmt.Errorf("helper: %s subjectAltName is malformed: %w", kind, err)
		}
		if name.Class != asn1.ClassContextSpecific {
			return nil, fmt.Errorf("helper: %s subjectAltName contains an unsupported general name", kind)
		}
		if name.Tag != 2 {
			return nil, fmt.Errorf(
				"helper: %s subjectAltName contains unsupported type %d; only DNS names are allowed", kind, name.Tag)
		}
		if name.IsCompound {
			return nil, fmt.Errorf("helper: %s has a malformed DNS subjectAltName", kind)
		}
		names = append(names, string(name.Bytes))
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("helper: %s subjectAltName has no DNS names", kind)
	}
	for _, name := range names {
		if err := domain.ValidateDomain(name); err != nil {
			return nil, fmt.Errorf("helper: %s subjectAltName %q is not allowed: %w", kind, name, err)
		}
	}
	return names, nil
}

func sameStringSet(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		set[v] = struct{}{}
	}
	other := make(map[string]struct{}, len(b))
	for _, v := range b {
		other[v] = struct{}{}
	}
	if len(set) != len(other) {
		return false
	}
	for v := range set {
		if _, ok := other[v]; !ok {
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

func leafCommonName(domain string) string {
	if len(domain) <= 64 {
		return domain
	}
	return ""
}

func parseCertificatePEM(certPEM []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("helper: certificate is not PEM encoded")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("helper: unexpected PEM block type %q for a certificate", block.Type)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("helper: certificate PEM contains trailing data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("helper: parse certificate: %w", err)
	}
	return cert, nil
}

// parseLeafCertificatePEM parses the first certificate in a PEM bundle. Leaf
// files may also carry the root certificate as a convenience for servers.
func parseLeafCertificatePEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("helper: certificate is not PEM encoded")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("helper: unexpected PEM block type %q for a certificate", block.Type)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("helper: parse certificate: %w", err)
	}
	return cert, nil
}

func decodePrivateKeyPEM(keyPEM []byte) (crypto.Signer, error) {
	rest := keyPEM
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			return nil, errors.New("helper: private key is not PEM encoded")
		}
		rest = remaining
		switch block.Type {
		case "EC PARAMETERS":
			continue
		case "EC PRIVATE KEY":
			key, err := x509.ParseECPrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("helper: parse EC private key: %w", err)
			}
			return key, nil
		case "RSA PRIVATE KEY":
			key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("helper: parse RSA private key: %w", err)
			}
			return key, nil
		case "PRIVATE KEY":
			parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("helper: parse PKCS#8 private key: %w", err)
			}
			signer, ok := parsed.(crypto.Signer)
			if !ok {
				return nil, fmt.Errorf("helper: PKCS#8 key is %T, want a signer", parsed)
			}
			return signer, nil
		default:
			return nil, fmt.Errorf("helper: unexpected PEM block type %q for a private key", block.Type)
		}
	}
}

func publicKeysEqual(a, b any) bool {
	aDER, err := x509.MarshalPKIXPublicKey(a)
	if err != nil {
		return false
	}
	bDER, err := x509.MarshalPKIXPublicKey(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aDER, bDER)
}
