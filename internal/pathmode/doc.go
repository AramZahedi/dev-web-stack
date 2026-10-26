// Package pathmode translates and validates project paths across Linux,
// Windows, and the managed WSL distro.
//
// Linux sites use local absolute paths. Windows sites use the C: drive and
// translate it to /mnt/c; WSL sites use the configured distro's
// \\wsl.localhost or \\wsl$ UNC path and translate it to a Linux absolute path.
// The legacy UNC alias is normalized only after the distro matches the
// configured managed distro. UNC shares, device paths, alternate data streams,
// parent components, and paths that escape through symlinks or Windows
// reparse-point ancestors are rejected. Windows containment is
// case-insensitive and component-aware. A dot-prefixed project basename is
// rejected only when devstack is creating that directory; project-owned dot
// files and directories remain valid.
package pathmode
