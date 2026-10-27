package agent

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/wsl"
)

const (
	hostsBlockStart = "# BEGIN DEVSTACK MANAGED HOSTS"
	hostsBlockEnd   = "# END DEVSTACK MANAGED HOSTS"
	windowsHostName = "devstack-windows.internal"
)

var (
	ErrRootRequired    = errors.New("agent: root privileges are required")
	ErrInvalidCABundle = errors.New("agent: invalid CA certificate bundle")
	domainPattern      = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*\.test$`)
)

// HostsManager replaces only its marked /etc/hosts section and disables WSL
// generated hosts entries. Both changes require root in the managed distro.
type HostsManager struct {
	HostsPath     string
	WSLConfigPath string
	isRoot        func() bool
}

func NewHostsManager(hostsPath, wslConfigPath string) *HostsManager {
	if hostsPath == "" {
		hostsPath = "/etc/hosts"
	}
	if wslConfigPath == "" {
		wslConfigPath = "/etc/wsl.conf"
	}
	return &HostsManager{HostsPath: hostsPath, WSLConfigPath: wslConfigPath, isRoot: isRootProcess}
}

// Sync atomically updates sorted .test mappings and the WSL generateHosts
// setting. The only non-loopback entry is the fixed host-gateway name.
func (m *HostsManager) Sync(domains []string, gateway string) (bool, error) {
	if m == nil || m.isRoot == nil || !m.isRoot() {
		return false, ErrRootRequired
	}
	validated, err := validateTestDomains(domains)
	if err != nil {
		return false, err
	}
	gatewayIP := net.ParseIP(strings.TrimSpace(gateway))
	if !validPrivateGateway(gatewayIP) {
		return false, errors.New("agent: host gateway must be a private WSL gateway address")
	}
	oldHosts, hostMode, err := readManagedFile(m.HostsPath, 0o644)
	if err != nil {
		return false, err
	}
	newHosts, err := replaceHostsBlock(oldHosts, validated, gatewayIP.String())
	if err != nil {
		return false, err
	}
	oldConfig, configMode, err := readManagedFile(m.WSLConfigPath, 0o644)
	if err != nil {
		return false, err
	}
	newConfig := wsl.DisableGeneratedHosts(string(oldConfig))
	changed := string(oldHosts) != string(newHosts) || string(oldConfig) != newConfig
	if !changed {
		return false, nil
	}
	if string(oldHosts) != string(newHosts) {
		if err := writeManagedFile(m.HostsPath, newHosts, hostMode); err != nil {
			return false, fmt.Errorf("agent: update managed hosts: %w", err)
		}
	}
	if string(oldConfig) != newConfig {
		if err := writeManagedFile(m.WSLConfigPath, []byte(newConfig), configMode); err != nil {
			return false, fmt.Errorf("agent: disable generated hosts: %w", err)
		}
	}
	return true, nil
}

// Remove uninstalls the managed hosts block while preserving localhost and
// every line outside the marked block.
func (m *HostsManager) Remove() error {
	if m == nil || m.isRoot == nil || !m.isRoot() {
		return ErrRootRequired
	}
	data, mode, err := readManagedFile(m.HostsPath, 0o644)
	if err != nil {
		return err
	}
	updated, changed, err := removeHostsBlock(data)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return writeManagedFile(m.HostsPath, updated, mode)
}

func validateTestDomains(domains []string) ([]string, error) {
	seen := make(map[string]struct{}, len(domains))
	for _, input := range domains {
		domain := strings.ToLower(strings.TrimSpace(input))
		if !domainPattern.MatchString(domain) || len(domain) > 253 {
			return nil, fmt.Errorf("agent: invalid .test domain %q", input)
		}
		seen[domain] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for domain := range seen {
		out = append(out, domain)
	}
	sort.Strings(out)
	return out, nil
}

func validPrivateGateway(ip net.IP) bool {
	return ip != nil && ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

func replaceHostsBlock(input []byte, domains []string, gateway string) ([]byte, error) {
	lines := splitFileLines(input)
	block := []string{hostsBlockStart}
	for _, domain := range domains {
		block = append(block, "127.0.0.1 "+domain)
	}
	block = append(block, gateway+" "+windowsHostName, hostsBlockEnd)
	updated := make([]string, 0, len(lines)+len(block)+1)
	inside, found, inserted := false, false, false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case hostsBlockStart:
			if inside || found {
				return nil, errors.New("agent: malformed duplicate managed hosts block")
			}
			inside, found = true, true
			updated = append(updated, block...)
			inserted = true
		case hostsBlockEnd:
			if !inside {
				return nil, errors.New("agent: managed hosts end marker without start")
			}
			inside = false
		default:
			if !inside {
				updated = append(updated, line)
			}
		}
	}
	if inside {
		return nil, errors.New("agent: managed hosts start marker without end")
	}
	if !inserted {
		for len(updated) > 0 && updated[len(updated)-1] == "" {
			updated = updated[:len(updated)-1]
		}
		if len(updated) > 0 {
			updated = append(updated, "")
		}
		updated = append(updated, block...)
	}
	return []byte(strings.Join(updated, "\n") + "\n"), nil
}

func removeHostsBlock(input []byte) ([]byte, bool, error) {
	lines := splitFileLines(input)
	updated := make([]string, 0, len(lines))
	inside, found := false, false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case hostsBlockStart:
			if inside || found {
				return nil, false, errors.New("agent: malformed duplicate managed hosts block")
			}
			inside, found = true, true
		case hostsBlockEnd:
			if !inside {
				return nil, false, errors.New("agent: managed hosts end marker without start")
			}
			inside = false
		default:
			if !inside {
				updated = append(updated, line)
			}
		}
	}
	if inside {
		return nil, false, errors.New("agent: managed hosts start marker without end")
	}
	if !found {
		return input, false, nil
	}
	for len(updated) > 0 && updated[len(updated)-1] == "" {
		updated = updated[:len(updated)-1]
	}
	if len(updated) == 0 {
		return nil, true, nil
	}
	return []byte(strings.Join(updated, "\n") + "\n"), true, nil
}

func splitFileLines(data []byte) []string {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func readManagedFile(path string, defaultMode os.FileMode) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, defaultMode, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("agent: managed file %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	return data, info.Mode().Perm(), err
}

func writeManagedFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writePrivateFile(path, data, mode)
}

// InstallCABundle accepts public CA certificates only and installs a shared
// curl/Node/PHP trust bundle. It never stores a private CA key.
func InstallCABundle(root string, bundle []byte) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("agent: CA root must be absolute")
	}
	if err := validateCABundle(bundle); err != nil {
		return "", err
	}
	certDir := filepath.Join(root, "certs")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return "", fmt.Errorf("agent: create CA directory: %w", err)
	}
	if err := os.Chmod(certDir, 0o755); err != nil {
		return "", fmt.Errorf("agent: protect CA directory: %w", err)
	}
	path := filepath.Join(certDir, "ca-bundle.crt")
	if err := writePrivateFile(path, bundle, 0o644); err != nil {
		return "", err
	}
	phpDir := filepath.Join(certDir, "php-conf.d")
	if err := os.MkdirAll(phpDir, 0o755); err != nil {
		return "", fmt.Errorf("agent: create PHP CA configuration directory: %w", err)
	}
	phpConfig := []byte("openssl.cafile=" + path + "\ncurl.cainfo=" + path + "\n")
	if err := writePrivateFile(filepath.Join(phpDir, "99-devstack-ca.ini"), phpConfig, 0o644); err != nil {
		return "", err
	}
	profileScript := fmt.Sprintf("# Managed by devstack agent\nexport SSL_CERT_FILE=%q\nexport CURL_CA_BUNDLE=%q\nexport NODE_EXTRA_CA_CERTS=%q\nif [ -z \"${PHP_INI_SCAN_DIR:-}\" ]; then\n    export PHP_INI_SCAN_DIR=\":%s\"\nelse\n    case \":$PHP_INI_SCAN_DIR:\" in\n        *\":%s:\"*) ;;\n        *) export PHP_INI_SCAN_DIR=\"$PHP_INI_SCAN_DIR:%s\" ;;\n    esac\nfi\n", path, path, path, phpDir, phpDir, phpDir)
	_ = writePrivateFile(filepath.Join(certDir, "ca-env.sh"), []byte(profileScript), 0o644)
	if _, err := os.Stat("/etc/profile.d"); err == nil {
		_ = writePrivateFile("/etc/profile.d/devstack-ca.sh", []byte(profileScript), 0o644)
	}
	return path, nil
}

// CAEnvironment injects certificate trust into curl, Node, and PHP without
// discarding the caller's existing environment.
func CAEnvironment(root string, base map[string]string) (map[string]string, error) {
	path := filepath.Join(root, "certs", "ca-bundle.crt")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("agent: read CA bundle: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("agent: CA bundle is not a regular file")
	}
	if err := validateCABundleFile(path); err != nil {
		return nil, err
	}
	env := make(map[string]string, len(base)+4)
	for key, value := range base {
		env[key] = value
	}
	env["SSL_CERT_FILE"] = path
	env["CURL_CA_BUNDLE"] = path
	env["NODE_EXTRA_CA_CERTS"] = path
	phpDir := filepath.Join(root, "certs", "php-conf.d")
	if current := env["PHP_INI_SCAN_DIR"]; current != "" {
		env["PHP_INI_SCAN_DIR"] = current + string(os.PathListSeparator) + phpDir
	} else {
		env["PHP_INI_SCAN_DIR"] = string(os.PathListSeparator) + phpDir
	}
	return env, nil
}

func validateCABundleFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return validateCABundle(data)
}

func validateCABundle(bundle []byte) error {
	remaining := bundle
	count := 0
	for len(strings.TrimSpace(string(remaining))) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" {
			return ErrInvalidCABundle
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid {
			return ErrInvalidCABundle
		}
		count++
		remaining = rest
	}
	if count == 0 {
		return ErrInvalidCABundle
	}
	return nil
}

// CAResponse returns the currently installed public root bundle and its
// SHA-256 fingerprint for the frozen agent protocol's read-only CA endpoint.
func CAResponse(root string) (agentproto.CAResponse, error) {
	path := filepath.Join(root, "certs", "ca-bundle.crt")
	data, err := os.ReadFile(path)
	if err != nil {
		return agentproto.CAResponse{}, fmt.Errorf("agent: CA bundle unavailable: %w", err)
	}
	if err := validateCABundle(data); err != nil {
		return agentproto.CAResponse{}, err
	}
	fingerprint, err := FingerprintCertPEM(data)
	if err != nil {
		return agentproto.CAResponse{}, err
	}
	return agentproto.CAResponse{CertPEM: data, Fingerprint: fingerprint}, nil
}
