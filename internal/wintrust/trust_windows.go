//go:build windows

// Package wintrust owns the Windows machine trust store and the protected
// system directory used for the helper's CA.
package wintrust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/platform"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
	"golang.org/x/sys/windows"
)

var (
	DefaultSystemDir       = filepath.Join(platform.DefaultRoot("windows"), "system")
	DefaultCADir           = filepath.Join(platform.DefaultRoot("windows"), "system", "pki")
	DefaultRootCertificate = filepath.Join(platform.DefaultRoot("windows"), "system", "pki", "root.crt")
)

const (
	firefoxPolicyKey = `HKLM\Software\Policies\Mozilla\Firefox\Certificates`
)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// Manager performs fixed Windows certificate and ACL operations.
//
// The system directory, CA directory, and root certificate are configurable
// because the helper service runs as LocalSystem: the process profile is not
// the installing user's profile. Every fixed-path check compares against these
// configured paths, never the package defaults evaluated in the service's
// profile.
type Manager struct {
	runner            CommandRunner
	aclApply          ACLSetter
	systemDir         string
	caDir             string
	rootCertPath      string
	policyStatePath   string
	policyStateCustom bool
}

// ACLSetter applies a protected security descriptor. Tests inject a recorder
// so they never change machine ACLs.
type ACLSetter func(path, sddl string) error

func NewManager(runner CommandRunner, aclSetters ...ACLSetter) *Manager {
	if runner == nil {
		runner = execRunner{}
	}
	manager := &Manager{
		runner: runner, aclApply: applyDirectoryACL,
		systemDir:       DefaultSystemDir,
		caDir:           DefaultCADir,
		rootCertPath:    DefaultRootCertificate,
		policyStatePath: filepath.Join(DefaultSystemDir, "firefox-policy-managed"),
	}
	if len(aclSetters) > 0 && aclSetters[0] != nil {
		manager.aclApply = aclSetters[0]
	}
	return manager
}

// ConfigurePaths points this manager at one install tree. The helper calls it
// with the values derived from --config so that a LocalSystem service operates
// on the installer's protected system directory instead of its own profile.
// An empty CA directory or root certificate path is derived below systemDir.
func (m *Manager) ConfigurePaths(systemDir, caDir, rootCertPath string) error {
	systemDir = strings.TrimSpace(systemDir)
	caDir = strings.TrimSpace(caDir)
	rootCertPath = strings.TrimSpace(rootCertPath)
	if systemDir == "" {
		return errors.New("wintrust: system directory is empty")
	}
	if !filepath.IsAbs(systemDir) {
		return fmt.Errorf("wintrust: system directory %q must be absolute", systemDir)
	}
	if caDir == "" {
		caDir = filepath.Join(systemDir, "pki")
	}
	if !filepath.IsAbs(caDir) {
		return fmt.Errorf("wintrust: CA directory %q must be absolute", caDir)
	}
	if rootCertPath == "" {
		rootCertPath = filepath.Join(caDir, "root.crt")
	}
	if !filepath.IsAbs(rootCertPath) {
		return fmt.Errorf("wintrust: root certificate %q must be absolute", rootCertPath)
	}
	m.systemDir = filepath.Clean(systemDir)
	m.caDir = filepath.Clean(caDir)
	m.rootCertPath = filepath.Clean(rootCertPath)
	if !m.policyStateCustom {
		m.policyStatePath = filepath.Join(m.systemDir, "firefox-policy-managed")
	}
	return nil
}

// Paths reports the configured system, CA, and root-certificate paths.
func (m *Manager) Paths() (systemDir, caDir, rootCertPath string) {
	if m == nil {
		return DefaultSystemDir, DefaultCADir, DefaultRootCertificate
	}
	return m.systemDir, m.caDir, m.rootCertPath
}

// NewManagerWithStatePath is used by the helper's unit tests to keep Firefox
// policy ownership metadata outside the machine system directory.
func NewManagerWithStatePath(runner CommandRunner, aclSetter ACLSetter, statePath string) *Manager {
	manager := NewManager(runner, aclSetter)
	if statePath != "" {
		manager.policyStatePath = statePath
		manager.policyStateCustom = true
	}
	return manager
}

func (m *Manager) ProtectSystem(ctx context.Context, systemDir string) error {
	if !sameWindowsPath(systemDir, m.systemDir) {
		return fmt.Errorf("wintrust: refusing to protect non-system path %q", systemDir)
	}
	return m.protectPath(ctx, m.systemDir, ProtectionSDDL())
}

func (m *Manager) ProtectCAPath(ctx context.Context, path string) error {
	fixed := []string{m.caDir, filepath.Join(m.caDir, "root.key"), m.rootCertPath}
	for _, candidate := range fixed {
		if sameWindowsPath(path, candidate) {
			sddl := ProtectionSDDL()
			if !sameWindowsPath(candidate, m.caDir) {
				sddl = ProtectionFileSDDL()
			}
			return m.protectPath(ctx, candidate, sddl)
		}
	}
	return fmt.Errorf("wintrust: refusing to protect non-CA path %q", path)
}

func (m *Manager) protectPath(ctx context.Context, path, sddl string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.aclApply(path, sddl); err != nil {
		return fmt.Errorf("protect Windows path %s: %w", path, err)
	}
	return nil
}

// ProtectionSDDL creates an exact, protected DACL. The directory is owned by
// SYSTEM, and only SYSTEM and Administrators receive full control.
func ProtectionSDDL() string {
	return "O:SYD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
}

func ProtectionFileSDDL() string {
	return "O:SYD:P(A;;FA;;;SY)(A;;FA;;;BA)"
}

func applyDirectoryACL(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
}

func (m *Manager) InstallRoot(ctx context.Context, certificatePath string) error {
	args, err := RootInstallArgsAt(certificatePath, m.rootCertPath)
	if err != nil {
		return err
	}
	if _, err := m.runner.Run(ctx, "certutil.exe", args...); err != nil {
		return fmt.Errorf("install Windows machine root certificate: %w", err)
	}
	return nil
}

func RootInstallArgs(certificatePath string) ([]string, error) {
	return RootInstallArgsAt(certificatePath, DefaultRootCertificate)
}

// RootInstallArgsAt validates that certificatePath is the configured root
// certificate. Callers that run in the LocalSystem profile must use their
// configured path rather than the package default.
func RootInstallArgsAt(certificatePath, configuredRootCertificate string) ([]string, error) {
	configuredRootCertificate = strings.TrimSpace(configuredRootCertificate)
	if configuredRootCertificate == "" {
		return nil, errors.New("wintrust: configured root certificate path is empty")
	}
	if !sameWindowsPath(certificatePath, configuredRootCertificate) {
		return nil, fmt.Errorf("wintrust: refusing to install root certificate from %q", certificatePath)
	}
	return []string{"-addstore", "-f", "Root", configuredRootCertificate}, nil
}

func (m *Manager) RemoveRoot(ctx context.Context, sha1Thumbprint string) error {
	args, err := RootRemoveArgs(sha1Thumbprint)
	if err != nil {
		return err
	}
	if _, err := m.runner.Run(ctx, "certutil.exe", args...); err != nil {
		return fmt.Errorf("remove Windows machine root certificate: %w", err)
	}
	return nil
}

func (m *Manager) RootStatus(ctx context.Context, sha1Thumbprint string) (trust.Target, error) {
	if _, err := RootRemoveArgs(sha1Thumbprint); err != nil {
		return trust.Target{}, err
	}
	output, err := m.runner.Run(ctx, "certutil.exe", "-store", "Root", strings.ToUpper(sha1Thumbprint))
	if err != nil {
		return trust.Target{Name: "Windows machine root store", OK: false, Detail: strings.TrimSpace(string(output))}, nil
	}
	compact := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(string(output), " ", ""), "\r", ""))
	fingerprint := strings.ToUpper(strings.ReplaceAll(sha1Thumbprint, " ", ""))
	ok := strings.Contains(compact, fingerprint)
	detail := "not installed"
	if ok {
		detail = "installed"
	}
	return trust.Target{Name: "Windows machine root store", OK: ok, Detail: detail}, nil
}

func RootRemoveArgs(sha1Thumbprint string) ([]string, error) {
	value := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(sha1Thumbprint), " ", ""))
	if len(value) != 40 {
		return nil, errors.New("wintrust: root certificate thumbprint must contain 40 hexadecimal characters")
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'F') {
			return nil, errors.New("wintrust: root certificate thumbprint is not hexadecimal")
		}
	}
	return []string{"-delstore", "Root", value}, nil
}

func (m *Manager) InstallFirefoxPolicy(ctx context.Context) error {
	exists, enabled, err := m.queryFirefoxPolicy(ctx)
	if err != nil {
		return err
	}
	if exists {
		if enabled {
			return nil
		}
		return errors.New("wintrust: Firefox enterprise root-import policy is already set to a different value")
	}
	if _, err := m.runner.Run(ctx, "reg.exe", FirefoxInstallArgs()...); err != nil {
		return fmt.Errorf("enable Firefox enterprise root import policy: %w", err)
	}
	if err := m.markFirefoxPolicyOwned(); err != nil {
		_, _ = m.runner.Run(ctx, "reg.exe", FirefoxRemoveArgs()...)
		return fmt.Errorf("record Firefox policy ownership: %w", err)
	}
	return nil
}

func (m *Manager) RemoveFirefoxPolicy(ctx context.Context) error {
	marker, err := os.ReadFile(m.policyStatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Firefox policy ownership: %w", err)
	}
	if strings.TrimSpace(string(marker)) != "devstack-owned" {
		return errors.New("wintrust: Firefox policy ownership marker is invalid; refusing removal")
	}
	exists, enabled, err := m.queryFirefoxPolicy(ctx)
	if err != nil {
		return err
	}
	if exists && enabled {
		if _, err := m.runner.Run(ctx, "reg.exe", FirefoxRemoveArgs()...); err != nil {
			return fmt.Errorf("remove Firefox enterprise root import policy: %w", err)
		}
	}
	if err := os.Remove(m.policyStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Firefox policy ownership marker: %w", err)
	}
	return nil
}

func (m *Manager) queryFirefoxPolicy(ctx context.Context) (exists, enabled bool, err error) {
	output, runErr := m.runner.Run(ctx, "reg.exe", "query", firefoxPolicyKey, "/v", "ImportEnterpriseRoots")
	if runErr != nil {
		text := strings.ToLower(string(output) + " " + runErr.Error())
		if strings.Contains(text, "unable to find") || strings.Contains(text, "cannot find") || strings.Contains(text, "not found") {
			return false, false, nil
		}
		return false, false, fmt.Errorf("query Firefox enterprise root-import policy: %w", runErr)
	}
	fields := strings.Fields(string(output))
	for i, field := range fields {
		if strings.EqualFold(field, "ImportEnterpriseRoots") && i+2 < len(fields) {
			value := strings.ToLower(fields[i+2])
			return true, value == "0x1" || value == "0x00000001" || value == "1", nil
		}
	}
	return false, false, errors.New("wintrust: Firefox policy query returned an unrecognized value")
}

func (m *Manager) markFirefoxPolicyOwned() error {
	file, err := os.OpenFile(m.policyStatePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		data, readErr := os.ReadFile(m.policyStatePath)
		if readErr == nil && strings.TrimSpace(string(data)) == "devstack-owned" {
			return nil
		}
		return errors.New("ownership marker path already exists")
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("devstack-owned\n")
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(m.policyStatePath)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(m.policyStatePath)
		return closeErr
	}
	return nil
}

func (m *Manager) FirefoxPolicyStatus(ctx context.Context) (trust.Target, error) {
	exists, enabled, err := m.queryFirefoxPolicy(ctx)
	if err != nil {
		return trust.Target{Name: "Firefox enterprise policy", OK: false, Detail: err.Error()}, nil
	}
	detail := "disabled"
	if !exists {
		detail = "not configured"
	} else if enabled {
		detail = "enabled"
	}
	return trust.Target{Name: "Firefox enterprise policy", OK: exists && enabled, Detail: detail}, nil
}

func FirefoxInstallArgs() []string {
	return []string{"add", firefoxPolicyKey, "/v", "ImportEnterpriseRoots", "/t", "REG_DWORD", "/d", "1", "/f"}
}

func FirefoxRemoveArgs() []string {
	return []string{"delete", firefoxPolicyKey, "/v", "ImportEnterpriseRoots", "/f"}
}

// EnsureStorage creates the configured CA directory before protecting its
// parent and the CA files.
func (m *Manager) EnsureStorage(ctx context.Context) error {
	info, err := os.Lstat(m.systemDir)
	if err != nil {
		return fmt.Errorf("inspect protected system directory (installer must create it first): %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("wintrust: protected system path must be a non-reparse directory")
	}
	attributes, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(m.systemDir))
	if err != nil {
		return fmt.Errorf("inspect protected system directory attributes: %w", err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("wintrust: protected system path must not be a reparse point")
	}
	if err := m.ProtectSystem(ctx, m.systemDir); err != nil {
		return err
	}
	caInfo, err := os.Lstat(m.caDir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(m.caDir, 0o700); err != nil {
			return fmt.Errorf("create Windows CA directory: %w", err)
		}
		caInfo, err = os.Lstat(m.caDir)
	}
	if err != nil {
		return fmt.Errorf("inspect Windows CA directory: %w", err)
	}
	caAttributes, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(m.caDir))
	if err != nil {
		return fmt.Errorf("inspect Windows CA directory attributes: %w", err)
	}
	if !caInfo.IsDir() || caInfo.Mode()&os.ModeSymlink != 0 || caAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("wintrust: CA directory must be a non-reparse directory")
	}
	if err := m.ProtectCAPath(ctx, m.caDir); err != nil {
		return err
	}
	for _, path := range m.caFiles() {
		if info, err := os.Lstat(path); err == nil {
			attributes, attrErr := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
			if attrErr != nil {
				return fmt.Errorf("inspect Windows CA file %s attributes: %w", path, attrErr)
			}
			if err := m.validateExistingCAFile(path, info.Mode(), attributes); err != nil {
				return err
			}
			if err := m.ProtectCAPath(ctx, path); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect Windows CA file %s: %w", path, err)
		}
	}
	return nil
}

// caFiles returns the distinct CA files this manager may protect.
func (m *Manager) caFiles() []string {
	candidates := []string{filepath.Join(m.caDir, "root.key"), filepath.Join(m.caDir, "root.crt"), m.rootCertPath}
	out := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		key := strings.ToLower(filepath.Clean(candidate))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func (m *Manager) validateExistingCAFile(path string, mode os.FileMode, attributes uint32) error {
	if !sameWindowsPath(path, filepath.Join(m.caDir, "root.key")) && !sameWindowsPath(path, m.rootCertPath) {
		return fmt.Errorf("wintrust: refusing to protect non-CA file %q", path)
	}
	if mode&os.ModeType != 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("wintrust: existing CA files must be regular non-reparse files")
	}
	return nil
}

func sameWindowsPath(a, b string) bool {
	clean := func(value string) string {
		value = strings.ReplaceAll(value, "/", `\`)
		return strings.TrimRight(filepath.Clean(value), `\`)
	}
	return strings.EqualFold(clean(a), clean(b))
}
