package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/platform"
)

// LegacyWindowsHome returns the pre-2026-10 Windows install root when it is
// still the authoritative installation. The standalone installer and the
// logon task detect C:\devstack on upgrade, but the CLI previously fell back
// to %USERPROFILE%\.devstack and would start a second, empty daemon. A manifest
// in the new-default home always wins; otherwise an existing legacy manifest is
// adopted.
func LegacyWindowsHome() (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	defaultHome := platform.DefaultRoot("windows")
	if installManifestExists(defaultHome) {
		return "", false
	}
	const legacy = `C:\devstack`
	if installManifestExists(legacy) {
		return legacy, true
	}
	return "", false
}

func installManifestExists(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "system", "install-manifest.json"))
	return err == nil && !info.IsDir()
}
