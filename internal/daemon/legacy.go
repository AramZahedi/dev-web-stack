package daemon

import (
	"github.com/AramZahedi/dev-web-stack/internal/platform"
)

// LegacyWindowsHome returns the pre-2026-10 Windows install root when it is
// still the authoritative installation. The standalone installer and the
// logon task detect C:\devstack on upgrade, but the CLI previously fell back
// to %USERPROFILE%\.devstack and would start a second, empty daemon. A manifest
// in the new-default home always wins; otherwise an existing legacy manifest is
// adopted.
func LegacyWindowsHome() (string, bool) {
	return platform.LegacyWindowsRoot()
}

// installManifestExists is kept as a package-local seam for daemon tests.
func installManifestExists(dir string) bool {
	return platform.InstallManifestExists(dir)
}
