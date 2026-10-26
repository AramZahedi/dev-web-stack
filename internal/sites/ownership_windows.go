//go:build windows

package sites

import "os"

// validateVhostBackendOwnership is an agent-side check on Windows. The daemon
// cannot resolve Linux UIDs from a Windows path, and Windows-mode backends are
// only reached through the authenticated WSL agent. The helper still enforces
// ACL ownership at the filesystem boundary; this function only confirms the
// path exists before the daemon renders a vhost for it.
func validateVhostBackendOwnership(path string) error {
	_, err := os.Stat(path)
	return err
}
