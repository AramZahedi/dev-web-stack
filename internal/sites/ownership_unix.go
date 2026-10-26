//go:build !windows

package sites

import (
	"fmt"
	"os"
	"syscall"
)

// validateVhostBackendOwnership mirrors the helper's ownership boundary so one
// root-owned or foreign backend degrades to a 503 instead of poisoning the
// entire external vhost sync.
func validateVhostBackendOwnership(path string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat backend path %q: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("backend path %q has no ownership metadata", path)
	}
	if int(stat.Uid) == os.Geteuid() {
		return nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && vhostPathContained(path, home) {
		return nil
	}
	return fmt.Errorf("backend path %q is not owned by the devstack user", path)
}
