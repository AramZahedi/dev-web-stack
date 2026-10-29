package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
)

// TLSIdentity is the agent's self-signed server identity and its pinned
// SHA-256 certificate fingerprint (lowercase hex).
type TLSIdentity struct {
	CertPath string
	KeyPath  string
	CertPEM  []byte
	KeyPEM   []byte
	Pin      string
}

// GenerateTLSIdentity creates a P-256 self-signed certificate for the agent.
// The key is written 0600 and the certificate 0644. Provisioning copies both
// into WSL as root-owned files.
//
// Design Decision (M6 - 10-Year Certificate Validity & Exact Leaf Pinning):
//  1. 10-Year Validity: The agent is a host-local service operating strictly across
//     the Windows host to WSL2 boundary via loopback/virtual network. It does not
//     rely on an external CA, public PKI, or ACME renewal daemon inside WSL.
//     A 10-year validity window avoids arbitrary expiration outages on long-lived
//     development machines, hibernated environments, and offline developer setups.
//  2. Exact Leaf Fingerprint Pinning: Rather than relying on CA trust chains or
//     hostname/EKU checks (which can be vulnerable to rogue local roots or loopback
//     spoofing), the daemon uses InsecureSkipVerify: true paired with a strict
//     VerifyPeerCertificate hook that checks the SHA-256 digest of the exact leaf
//     certificate. This enforces a Trust-On-First-Use (TOFU) / provisioned secret model
//     where only the exact certificate provisioned into the distro is accepted.
//  3. Diagnostics: VerifyPeerCertificate explicitly validates the certificate validity
//     window (NotBefore/NotAfter) and returns descriptive errors if the clock is skewed,
//     the certificate has expired, or the leaf pin has changed (e.g. due to manual rotation).
func GenerateTLSIdentity(dir string) (TLSIdentity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: create TLS directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: protect TLS directory %s: %w", dir, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: generate TLS key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: generate serial: %w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "devstack-agent", Organization: []string{"devstack"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "devstack-agent"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: create TLS certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: encode TLS key: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certPath := filepath.Join(dir, "agent.crt")
	keyPath := filepath.Join(dir, "agent.key")
	if err := writePrivateFile(certPath, certPEM, 0o644); err != nil {
		return TLSIdentity{}, err
	}
	if err := writePrivateFile(keyPath, keyPEM, 0o600); err != nil {
		return TLSIdentity{}, err
	}
	pin := sha256.Sum256(der)
	return TLSIdentity{
		CertPath: certPath,
		KeyPath:  keyPath,
		CertPEM:  certPEM,
		KeyPEM:   keyPEM,
		Pin:      hex.EncodeToString(pin[:]),
	}, nil
}

// LoadTLSIdentity loads and validates an existing agent identity.
func LoadTLSIdentity(certPath, keyPath string) (TLSIdentity, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: read TLS certificate %s: %w", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: read TLS key %s: %w", keyPath, err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return TLSIdentity{}, fmt.Errorf("agent: invalid TLS identity: %w", err)
	}
	block, _ := pem.Decode(certPEM)
	if block != nil && block.Type == "CERTIFICATE" {
		if cert, parseErr := x509.ParseCertificate(block.Bytes); parseErr == nil {
			now := time.Now()
			if now.Before(cert.NotBefore) {
				return TLSIdentity{}, fmt.Errorf("agent: certificate %s is not yet valid (valid from %s, system time is %s); please verify system clock", certPath, cert.NotBefore.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
			}
			if now.After(cert.NotAfter) {
				return TLSIdentity{}, fmt.Errorf("agent: certificate %s expired on %s (system time is %s); certificate rotation required", certPath, cert.NotAfter.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
			}
		}
	}
	pin, err := FingerprintCertPEM(certPEM)
	if err != nil {
		return TLSIdentity{}, err
	}
	return TLSIdentity{CertPath: certPath, KeyPath: keyPath, CertPEM: certPEM, KeyPEM: keyPEM, Pin: pin}, nil
}

// FingerprintCertPEM returns the SHA-256 fingerprint of the first certificate
// in a PEM bundle.
func FingerprintCertPEM(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("agent: no PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("agent: parse certificate: %w", err)
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// PinnedConfig returns a TLS config that accepts exactly the pinned
// self-signed certificate. ServerName verification is intentionally replaced
// by the stronger leaf SHA-256 fingerprint pin.
//
// If peer verification fails, descriptive diagnostic messages are returned to
// distinguish pin mismatches (e.g., following an uncoordinated certificate rotation)
// from certificate expiration or clock skew.
func PinnedConfig(pin string) (*tls.Config, error) {
	pin = strings.TrimSpace(pin)
	if len(pin) != 64 {
		return nil, errors.New("agent: certificate pin must be 64 hex characters")
	}
	if _, err := hex.DecodeString(pin); err != nil {
		return nil, fmt.Errorf("agent: certificate pin: %w", err)
	}
	pin = strings.ToLower(pin)
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // verification is the exact certificate pin below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("%w: no peer certificate presented by server", agentproto.ErrCertPinMismatch)
			}
			sum := sha256.Sum256(rawCerts[0])
			gotPin := hex.EncodeToString(sum[:])
			if subtle.ConstantTimeCompare([]byte(gotPin), []byte(pin)) != 1 {
				return fmt.Errorf("%w: server certificate fingerprint %s does not match pinned fingerprint %s (certificate may have been regenerated or rotated; re-provision or update agent TLS pin in config)", agentproto.ErrCertPinMismatch, gotPin, pin)
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("agent: parse peer certificate: %w", err)
			}
			now := time.Now()
			if now.Before(cert.NotBefore) {
				return fmt.Errorf("agent: certificate is not yet valid (valid from %s, system time is %s); please verify system clock", cert.NotBefore.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
			}
			if now.After(cert.NotAfter) {
				return fmt.Errorf("agent: certificate expired on %s (system time is %s); certificate rotation required", cert.NotAfter.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
			}
			return nil
		},
	}, nil
}

func writePrivateFile(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("agent: create %s: %w", path, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("agent: chmod %s: %w", name, err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("agent: write %s: %w", name, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("agent: sync %s: %w", name, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("agent: close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("agent: install %s: %w", path, err)
	}
	return nil
}
