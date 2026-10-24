package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

// Target is the result of installing, removing, or checking one trust store or
// browser profile.
type Target = trust.Target

// CommandRunner runs an external command and returns its combined output.
// Implementations that can drop privileges should also implement
// UserCommandRunner.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// UserCommandRunner is an optional extension implemented by runners that can
// execute a command as another user. The helper uses it for certutil so root
// never follows user-controlled NSS database paths with root privileges.
type UserCommandRunner interface {
	RunAs(ctx context.Context, uid, gid int, name string, args ...string) ([]byte, error)
}

// TrustManager installs, removes, and checks the helper's root CA in the
// system trust store and the user's NSS databases.
type TrustManager struct {
	rootPEM         []byte
	rootFingerprint string
	rootErr         error
	homeErr         error

	userHome string
	runner   CommandRunner

	ownerUID int
	ownerGID int
	ownerErr error

	anchorDir string
	fedora    bool
	systemOK  bool
}

// NewTrustManager returns a TrustManager for userHome. An empty anchorDir
// asks for Linux distribution detection; a non-empty anchorDir overrides it
// (used by tests and by the helper's --trust-dir flag). A nil runner uses a
// real os/exec runner.
func NewTrustManager(rootPEM []byte, userHome string, runner CommandRunner, anchorDir string) *TrustManager {
	if runner == nil {
		runner = execRunner{}
	}
	home := ""
	if userHome != "" {
		home = filepath.Clean(userHome)
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
	}
	m := &TrustManager{
		rootPEM:  append([]byte(nil), rootPEM...),
		userHome: home,
		runner:   runner,
	}
	if home == "" {
		m.homeErr = errors.New("user home directory is empty")
	} else {
		m.resolveHomeOwner()
	}
	cert, err := parseRootCertificatePEM(rootPEM)
	if err != nil {
		m.rootErr = err
	} else {
		m.rootFingerprint = certificateFingerprint(cert)
	}
	m.anchorDir, m.fedora, m.systemOK = detectAnchorDir(anchorDir)
	return m
}

// Install installs the root CA into every available trust target. Individual
// target failures are reported in the returned slice; the error is reserved
// for an unusable root certificate.
func (m *TrustManager) Install(ctx context.Context) ([]Target, error) {
	if err := m.configError(); err != nil {
		return nil, err
	}
	targets := []Target{m.installAnchor(ctx)}
	targets = append(targets, m.installNSS(ctx)...)
	return targets, nil
}

// Remove removes only the CA whose fingerprint matches this manager's root.
func (m *TrustManager) Remove(ctx context.Context) ([]Target, error) {
	if err := m.configError(); err != nil {
		return nil, err
	}
	targets := []Target{m.removeAnchor(ctx)}
	targets = append(targets, m.removeNSS(ctx)...)
	return targets, nil
}

// Status reports whether this manager's root is present in each trust target.
func (m *TrustManager) Status(ctx context.Context) ([]Target, error) {
	if err := m.configError(); err != nil {
		return nil, err
	}
	targets := []Target{m.statusAnchor()}
	targets = append(targets, m.statusNSS(ctx)...)
	return targets, nil
}

func (m *TrustManager) configError() error {
	if m.rootErr != nil {
		return fmt.Errorf("helper: invalid root certificate: %w", m.rootErr)
	}
	if m.homeErr != nil {
		return fmt.Errorf("helper: %w", m.homeErr)
	}
	if m.ownerErr != nil {
		return fmt.Errorf("helper: %w", m.ownerErr)
	}
	return nil
}

func (m *TrustManager) installAnchor(ctx context.Context) Target {
	if !m.systemOK {
		return Target{Name: "OS", OK: false, Detail: "unsupported Linux distribution: no CA anchor directory found"}
	}
	if err := os.MkdirAll(m.anchorDir, 0o755); err != nil {
		return Target{Name: "OS", OK: false, Detail: "create anchor directory: " + detailError(err)}
	}
	path := filepath.Join(m.anchorDir, anchorFileName)
	if err := writeAtomicFile(path, m.rootPEM, 0o644); err != nil {
		return Target{Name: "OS", OK: false, Detail: "write anchor: " + detailError(err)}
	}
	name, args := anchorUpdateCommand(m.fedora)
	if _, err := m.runner.Run(ctx, name, args...); err != nil {
		return Target{Name: "OS", OK: false, Detail: "anchor installed but " + name + " failed: " + detailError(err)}
	}
	return Target{Name: "OS", OK: true, Detail: "installed"}
}

func (m *TrustManager) removeAnchor(ctx context.Context) Target {
	if !m.systemOK {
		return Target{Name: "OS", OK: false, Detail: "unsupported Linux distribution: no CA anchor directory found"}
	}
	path := filepath.Join(m.anchorDir, anchorFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Target{Name: "OS", OK: true, Detail: "not installed"}
	}
	if err != nil {
		return Target{Name: "OS", OK: false, Detail: "read anchor: " + detailError(err)}
	}
	fingerprint, err := anchorFingerprint(data)
	if err != nil {
		return Target{Name: "OS", OK: false, Detail: "anchor is not a single valid certificate; left in place: " + detailError(err)}
	}
	if fingerprint != m.rootFingerprint {
		return Target{Name: "OS", OK: false, Detail: "anchor has a different fingerprint; left in place"}
	}
	if err := os.Remove(path); err != nil {
		return Target{Name: "OS", OK: false, Detail: "remove anchor: " + detailError(err)}
	}
	name, args := anchorUpdateCommand(m.fedora)
	if _, err := m.runner.Run(ctx, name, args...); err != nil {
		return Target{Name: "OS", OK: false, Detail: "anchor removed but " + name + " failed: " + detailError(err)}
	}
	return Target{Name: "OS", OK: true, Detail: "removed"}
}

func (m *TrustManager) statusAnchor() Target {
	if !m.systemOK {
		return Target{Name: "OS", OK: false, Detail: "unsupported Linux distribution: no CA anchor directory found"}
	}
	path := filepath.Join(m.anchorDir, anchorFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Target{Name: "OS", OK: false, Detail: "not installed"}
	}
	if err != nil {
		return Target{Name: "OS", OK: false, Detail: "read anchor: " + detailError(err)}
	}
	fingerprint, err := anchorFingerprint(data)
	if err != nil {
		return Target{Name: "OS", OK: false, Detail: "invalid anchor: " + detailError(err)}
	}
	if fingerprint != m.rootFingerprint {
		return Target{Name: "OS", OK: false, Detail: "a different certificate is installed"}
	}
	return Target{Name: "OS", OK: true, Detail: "installed"}
}

func parseRootCertificatePEM(rootPEM []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(rootPEM)
	if block == nil {
		return nil, errors.New("root certificate is not PEM encoded")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("unexpected PEM block type %q", block.Type)
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("root certificate PEM contains trailing data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA {
		return nil, errors.New("root certificate is not a CA certificate")
	}
	return cert, nil
}

func anchorFingerprint(data []byte) (string, error) {
	certs, err := parseCertificatesPEM(data)
	if err != nil {
		return "", err
	}
	if len(certs) != 1 {
		return "", fmt.Errorf("anchor contains %d certificates; expected 1", len(certs))
	}
	return certificateFingerprint(certs[0]), nil
}

func parseCertificatesPEM(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		block, remaining := pem.Decode(rest)
		if block == nil {
			if len(bytes.TrimSpace(rest)) != 0 {
				return nil, errors.New("trailing data after PEM blocks")
			}
			break
		}
		rest = remaining
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unexpected PEM block type %q", block.Type)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no certificates found")
	}
	return certs, nil
}

func certificateFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func detailError(err error) string {
	message := strings.TrimSpace(err.Error())
	const limit = 300
	if len(message) > limit {
		message = message[:limit] + "..."
	}
	return message
}

// execRunner is the production CommandRunner. Tests inject a fake instead.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runCombined(exec.CommandContext(ctx, name, args...))
}

func runCombined(cmd *exec.Cmd) ([]byte, error) {
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return output, fmt.Errorf("%s: %w: %s", cmd.Path, err, message)
		}
		return output, fmt.Errorf("%s: %w", cmd.Path, err)
	}
	return output, nil
}
