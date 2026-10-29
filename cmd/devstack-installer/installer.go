package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type InstallerOptions struct {
	InstallDir    string
	InstallDirSet bool
	RootFSPath    string
	AllowReboot   bool
	SkipWSL       bool
	DryRun        bool
	Upgrade       bool
	Uninstall     bool
	Unattended    bool
	ZipPath       string
	Help          bool
	PurgeDistro   bool
	PurgeData     bool
}

func defaultInstallDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".devstack")
	}
	if profile := strings.TrimSpace(os.Getenv("USERPROFILE")); profile != "" {
		return filepath.Join(profile, ".devstack")
	}
	return `C:\devstack`
}

func installManifestExists(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "system", "install-manifest.json"))
	return err == nil && !info.IsDir()
}

// legacyInstallDir is the pre-profile default. It is a variable so tests can
// point the legacy-detection paths at a temporary directory.
var legacyInstallDir = `C:\devstack`

func validateFreshInstallTarget(opts InstallerOptions) error {
	if opts.InstallDirSet || opts.Upgrade || opts.Uninstall || opts.DryRun {
		return nil
	}
	if installManifestExists(opts.InstallDir) || !installManifestExists(legacyInstallDir) {
		return nil
	}
	return fmt.Errorf("an existing legacy devstack installation was found at %s; run with --upgrade to upgrade it, or pass --install-dir to choose a different location", legacyInstallDir)
}

func parseFlags(args []string) (InstallerOptions, error) {
	var opts InstallerOptions
	fs := flag.NewFlagSet("devstack-installer", flag.ContinueOnError)
	fs.StringVar(&opts.InstallDir, "install-dir", defaultInstallDir(), "Installation directory on Windows")
	fs.StringVar(&opts.RootFSPath, "rootfs", "", "Path to debian-12-wsl-rootfs.tar.gz archive")
	fs.BoolVar(&opts.AllowReboot, "allow-reboot", false, "Allow automatic reboot if Windows features require a restart")
	fs.BoolVar(&opts.SkipWSL, "skip-wsl", false, "Skip WSL optional feature enablement and distro provisioning")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Simulate checks and installation steps without altering system")
	fs.BoolVar(&opts.Upgrade, "upgrade", false, "Upgrade existing devstack installation")
	fs.BoolVar(&opts.Uninstall, "uninstall", false, "Uninstall devstack and remove system services")
	fs.BoolVar(&opts.Unattended, "y", false, "Non-interactive unattended mode; do not prompt on completion")
	fs.BoolVar(&opts.Unattended, "unattended", false, "Non-interactive unattended mode; do not prompt on completion")
	fs.StringVar(&opts.ZipPath, "zip", "", "Path to external release ZIP archive if not using embedded payload")
	fs.BoolVar(&opts.Help, "h", false, "Show help message")
	fs.BoolVar(&opts.Help, "help", false, "Show help message")
	fs.BoolVar(&opts.PurgeDistro, "purge-distro", false, "Unregister and remove the managed WSL distro 'devstack' (used with --uninstall)")
	fs.BoolVar(&opts.PurgeData, "purge-data", false, "Delete DPAPI secrets and remove installation directory (used with --uninstall)")

	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "install-dir" {
			opts.InstallDirSet = true
		}
	})
	// Existing installs from the pre-profile layout are upgraded/uninstalled in
	// place unless -install-dir was explicit.
	if !opts.InstallDirSet && (opts.Upgrade || opts.Uninstall) &&
		!installManifestExists(opts.InstallDir) && installManifestExists(legacyInstallDir) {
		opts.InstallDir = legacyInstallDir
	}
	return opts, nil
}

func extractZip(reader io.ReaderAt, size int64, destDir string) error {
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return fmt.Errorf("read zip archive: %w", err)
	}

	cleanDest := filepath.Clean(destDir)

	for _, file := range zr.File {
		// Reject symlinks and special files to prevent arbitrary file overwrites via link traversal
		if file.Mode()&os.ModeType != 0 && !file.FileInfo().IsDir() {
			return fmt.Errorf("zip entry is not a regular file or directory: %q", file.Name)
		}

		// Normalize entry name
		name := strings.TrimPrefix(filepath.ToSlash(file.Name), "/")
		cleanName := filepath.Clean(filepath.FromSlash(name))

		// Check for path traversal, absolute paths, or Windows drive/volume specifications
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) || filepath.VolumeName(cleanName) != "" {
			return fmt.Errorf("zip entry escapes destination: %q", file.Name)
		}

		targetPath := filepath.Join(cleanDest, cleanName)

		// Verify targetPath is strictly inside cleanDest
		rel, err := filepath.Rel(cleanDest, targetPath)
		if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
			return fmt.Errorf("zip entry escapes destination: %q", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("open zip entry %q: %w", file.Name, err)
		}

		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err != nil {
			_ = rc.Close()
			return fmt.Errorf("create target file %q: %w", targetPath, err)
		}

		_, copyErr := io.Copy(out, rc)
		_ = rc.Close()
		_ = out.Close()
		if copyErr != nil {
			return fmt.Errorf("write target file %q: %w", targetPath, copyErr)
		}
	}
	return nil
}

func buildPowerShellArgs(opts InstallerOptions, scriptPath string) []string {
	args := []string{
		"-NoProfile",
		"-ExecutionPolicy", "Bypass",
		"-File", scriptPath,
	}

	if opts.InstallDir != "" {
		args = append(args, "-InstallDir", opts.InstallDir)
	}
	if opts.RootFSPath != "" && !opts.Uninstall {
		args = append(args, "-RootFSPath", opts.RootFSPath)
	}
	if opts.AllowReboot && !opts.Uninstall {
		args = append(args, "-AllowReboot")
	}
	if opts.SkipWSL && !opts.Uninstall {
		args = append(args, "-SkipWSL")
	}
	if opts.DryRun {
		args = append(args, "-DryRun")
	}
	if opts.Upgrade && !opts.Uninstall {
		args = append(args, "-Upgrade")
	}
	if opts.PurgeDistro && opts.Uninstall {
		args = append(args, "-PurgeDistro")
	}
	if opts.PurgeData && opts.Uninstall {
		args = append(args, "-PurgeData")
	}
	return args
}

func findPowerShell() (string, error) {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("neither pwsh.exe nor powershell.exe found in PATH")
}

func locatePayload(opts InstallerOptions) (io.ReaderAt, int64, error) {
	// 1. Explicit -zip flag wins over both the embedded payload and the test
	// environment override so an operator can validate another artifact.
	if opts.ZipPath != "" {
		fi, err := os.Stat(opts.ZipPath)
		if err != nil {
			return nil, 0, fmt.Errorf("zip path %q: %w", opts.ZipPath, err)
		}
		f, err := os.Open(opts.ZipPath)
		if err != nil {
			return nil, 0, err
		}
		return f, fi.Size(), nil
	}

	// 2. Environment variable override (e.g. for release packaging validation or testing)
	if envPath := os.Getenv("DEVSTACK_PAYLOAD_ZIP"); envPath != "" {
		if fi, err := os.Stat(envPath); err == nil && fi.Size() > 22 {
			f, err := os.Open(envPath)
			if err == nil {
				return f, fi.Size(), nil
			}
		}
	}

	// 3. Embedded payload
	if len(embeddedPayload) > 22 {
		return bytes.NewReader(embeddedPayload), int64(len(embeddedPayload)), nil
	}

	// 4. Search for devstack_*_windows_amd64.zip in executable dir and cwd
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if matches, _ := filepath.Glob(filepath.Join(exeDir, "devstack_*_windows_amd64.zip")); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
		if matches, _ := filepath.Glob(filepath.Join(exeDir, "..", "dist", "release", "devstack_*_windows_amd64.zip")); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if matches, _ := filepath.Glob(filepath.Join(cwd, "devstack_*_windows_amd64.zip")); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
		if matches, _ := filepath.Glob(filepath.Join(cwd, "dist", "release", "devstack_*_windows_amd64.zip")); len(matches) > 0 {
			candidates = append(candidates, matches...)
		}
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && fi.Size() > 22 {
			f, err := os.Open(cand)
			if err == nil {
				return f, fi.Size(), nil
			}
		}
	}

	return nil, 0, errors.New("no release payload found; please provide -zip <path-to-devstack-windows.zip>")
}
