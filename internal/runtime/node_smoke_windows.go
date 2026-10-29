//go:build windows

package runtime

// nodeSmoke is skipped on Windows. Node runtime archives are Linux ELF
// binaries and cannot execute on the Windows host; production Windows installs
// run inside the WSL agent, which uses the Linux implementation of this check.
// Keeping the skip in a build-tagged file leaves the non-Windows smoke
// mandatory and cannot silently weaken it.
func nodeSmoke(buildRoot, expectedVersion string) error {
	return nil
}
