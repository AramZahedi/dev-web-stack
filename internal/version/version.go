// Package version reports the devstack build version.
package version

var (
	// Version is the release version, injected at build time with
	// -ldflags "-X .../internal/version.Version=<version>".
	Version = "dev"
	// Commit is the git commit the binary was built from, injected at build
	// time with -ldflags "-X .../internal/version.Commit=<sha>".
	Commit = ""
)

// String formats the version for humans, including the commit when known.
func String() string {
	if Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
