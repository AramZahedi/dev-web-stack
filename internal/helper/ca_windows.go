//go:build windows

package helper

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/domain"
)

const (
	rootKeyFile     = "root.key"
	rootCertFile    = "root.crt"
	rootCommonName  = "devstack local CA"
	maxLeafValidity = 90 * 24 * time.Hour
)

type Authority struct {
	dir         string
	cert        *x509.Certificate
	key         *ecdsa.PrivateKey
	certPEM     []byte
	fingerprint string
	thumbprint  string
}

func LoadOrCreateAuthority(dir string, now func() time.Time) (*Authority, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("helper: CA directory is empty")
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("helper: create CA directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("helper: CA directory must be a real directory")
	}
	keyPath := filepath.Join(dir, rootKeyFile)
	certPath := filepath.Join(dir, rootCertFile)
	keyPEM, keyErr := os.ReadFile(keyPath)
	certPEM, certErr := os.ReadFile(certPath)
	keyMissing, certMissing := errors.Is(keyErr, os.ErrNotExist), errors.Is(certErr, os.ErrNotExist)
	switch {
	case keyMissing && certMissing:
		return createWindowsAuthority(dir, now())
	case keyErr != nil:
		return nil, fmt.Errorf("helper: read CA key: %w", keyErr)
	case certErr != nil:
		return nil, fmt.Errorf("helper: read CA certificate: %w", certErr)
	default:
		return loadWindowsAuthority(dir, keyPEM, certPEM)
	}
}

func (a *Authority) RootPEM() []byte     { return append([]byte(nil), a.certPEM...) }
func (a *Authority) Fingerprint() string { return a.fingerprint }
func (a *Authority) Thumbprint() string  { return a.thumbprint }
func (a *Authority) KeyPath() string     { return filepath.Join(a.dir, rootKeyFile) }

func (a *Authority) SignCSR(csrPEM []byte, domains []string, ttl time.Duration) ([]byte, string, time.Time, error) {
	if a == nil || a.cert == nil || a.key == nil {
		return nil, "", time.Time{}, errors.New("helper: certificate authority is not initialized")
	}
	if ttl <= 0 || ttl > maxLeafValidity {
		return nil, "", time.Time{}, fmt.Errorf("helper: certificate validity must be between 0 and %s", maxLeafValidity)
	}
	wanted, err := normalizeWindowsDomains(domains)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, "", time.Time{}, errors.New("helper: invalid PEM certificate request")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("helper: parse certificate request: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", time.Time{}, fmt.Errorf("helper: invalid certificate request signature: %w", err)
	}
	requested, err := normalizeWindowsDomains(csr.DNSNames)
	if err != nil || !equalWindowsDomains(wanted, requested) {
		return nil, "", time.Time{}, errors.New("helper: CSR DNS names must exactly match requested domains")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("helper: generate certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	now := time.Now().UTC().Truncate(time.Second)
	notAfter := now.Add(ttl)
	certificate := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: wanted[0]},
		NotBefore:             now,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              wanted,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, a.cert, csr.PublicKey, a.key)
	if err != nil {
		return nil, "", time.Time{}, fmt.Errorf("helper: sign certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), serial.Text(16), notAfter, nil
}

func createWindowsAuthority(dir string, now time.Time) (*Authority, error) {
	if now.IsZero() {
		now = time.Now()
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("helper: generate CA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("helper: generate CA serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	now = now.UTC().Truncate(time.Second)
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: rootCommonName},
		NotBefore:             now,
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("helper: create root certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("helper: parse root certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("helper: marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := writeNewWindowsCAFiles(dir, keyPEM, certPEM); err != nil {
		return nil, err
	}
	return newWindowsAuthority(dir, cert, key, certPEM), nil
}

func loadWindowsAuthority(dir string, keyPEM, certPEM []byte) (*Authority, error) {
	keyInfo, err := os.Lstat(filepath.Join(dir, rootKeyFile))
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("helper: CA private key must be a regular file")
	}
	certInfo, err := os.Lstat(filepath.Join(dir, rootCertFile))
	if err != nil || !certInfo.Mode().IsRegular() || certInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("helper: CA root certificate must be a regular file")
	}
	keyBlock, rest := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("helper: CA private key is not valid EC PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil || key.Curve != elliptic.P256() {
		return nil, errors.New("helper: CA private key must be ECDSA P-256")
	}
	certBlock, rest := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("helper: root certificate is not valid PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.Subject.CommonName != rootCommonName {
		return nil, errors.New("helper: root certificate is invalid")
	}
	if !bytes.Equal(mustMarshalPublicKey(cert.PublicKey), mustMarshalPublicKey(&key.PublicKey)) {
		return nil, errors.New("helper: CA private key does not match root certificate")
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		return nil, fmt.Errorf("helper: CA root is not self-signed: %w", err)
	}
	return newWindowsAuthority(dir, cert, key, certPEM), nil
}

func writeNewWindowsCAFiles(dir string, keyPEM, certPEM []byte) error {
	keyPath := filepath.Join(dir, rootKeyFile)
	if err := writeNewFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("helper: install CA private key: %w", err)
	}
	if err := writeNewFile(filepath.Join(dir, rootCertFile), certPEM, 0o644); err != nil {
		_ = os.Remove(keyPath)
		return fmt.Errorf("helper: install CA root certificate: %w", err)
	}
	return nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}

func newWindowsAuthority(dir string, cert *x509.Certificate, key *ecdsa.PrivateKey, certPEM []byte) *Authority {
	sha := sha256.Sum256(cert.Raw)
	thumb := sha1.Sum(cert.Raw)
	return &Authority{
		dir: dir, cert: cert, key: key, certPEM: append([]byte(nil), certPEM...),
		fingerprint: hex.EncodeToString(sha[:]), thumbprint: hex.EncodeToString(thumb[:]),
	}
}

func normalizeWindowsDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, errors.New("helper: at least one DNS name is required")
	}
	seen := make(map[string]bool, len(domains))
	for _, name := range domains {
		if err := domain.ValidateDomain(name); err != nil {
			return nil, fmt.Errorf("helper: invalid DNS name %q: %w", name, err)
		}
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func equalWindowsDomains(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustMarshalPublicKey(value any) []byte {
	encoded, _ := x509.MarshalPKIXPublicKey(value)
	return encoded
}
