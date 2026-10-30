//go:build !windows

package tooling

import "os"

// secureWSLEnvFile enforces the POSIX 0600 mode for the temp env file.
func secureWSLEnvFile(path string) error {
	return os.Chmod(path, 0o600)
}
