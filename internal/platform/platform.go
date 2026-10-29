// Package platform defines the operating-system capabilities and path policy
// shared by the daemon, helper, CLI, and WSL agent.
package platform

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// LinuxRoot is the application root for new Linux and WSL installs.
	LinuxRoot = "/data/devstack"
)

// IsWindows reports whether goos is Windows.
func IsWindows(goos string) bool { return goos == "windows" }

// DefaultRoot returns the platform application root. On Windows, it defaults
// to .devstack in the user profile. Existing Linux installs at ~/.devstack are
// detected by config.resolveHome and preserved; LinuxRoot is the default for a
// new install.
func DefaultRoot(goos string) string {
	if IsWindows(goos) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".devstack")
		}
		if profile := os.Getenv("USERPROFILE"); profile != "" {
			return filepath.Join(profile, ".devstack")
		}
		return `C:\Users\Default\.devstack`
	}
	return LinuxRoot
}

// Join joins path elements with the separator for goos. It is deliberately
// independent of the host so tests can assert the Windows layout on Linux.
func Join(goos string, parts ...string) string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		clean = append(clean, part)
	}
	if len(clean) == 0 {
		return ""
	}
	if IsWindows(goos) {
		return strings.Join(clean, `\`)
	}
	return path.Join(clean...)
}

// LegacyWindowsRoot returns the pre-profile C:\devstack installation when it
// is still the authoritative install. The new default home always wins when it
// contains a completed install manifest.
func LegacyWindowsRoot() (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	return legacyWindowsRoot(DefaultRoot("windows"), `C:\devstack`)
}

func legacyWindowsRoot(defaultRoot, legacyRoot string) (string, bool) {
	if InstallManifestExists(defaultRoot) {
		return "", false
	}
	if InstallManifestExists(legacyRoot) {
		return legacyRoot, true
	}
	return "", false
}

// InstallManifestExists reports whether dir contains a completed installer
// manifest at system\install-manifest.json.
func InstallManifestExists(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "system", "install-manifest.json"))
	return err == nil && !info.IsDir()
}

// DefaultProjectsRoot returns the default projects directory below root.
func DefaultProjectsRoot(root, goos string) string {
	return Join(goos, root, "projects")
}

// SystemDir returns the protected devstack system directory. It owns the CA
// key and other machine-local material that must never live in WSL or Docker.
func SystemDir(root, goos string) string {
	return Join(goos, root, "system")
}

// ValidateDevstackName rejects names that devstack itself must never create on
// Windows: empty, dot, dot-dot, and any basename starting with a dot. Project
// content (for example .git or .env) is outside this policy.
func ValidateDevstackName(name string) error {
	if name == "" {
		return fmt.Errorf("platform: empty devstack file name")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("platform: invalid devstack file name %q", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("platform: devstack-created name %q must not start with a dot", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("platform: devstack file name %q must be a basename", name)
	}
	return nil
}

// TempName converts base into a non-dot temporary basename. Two concurrent
// writers still need a unique suffix (CreateTemp adds one).
func TempName(base string) string {
	base = strings.TrimLeft(base, ".")
	if base == "" {
		base = "tmp"
	}
	return base + ".tmp"
}
