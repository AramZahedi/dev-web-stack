package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantDir  string
		wantDry  bool
		wantY    bool
		wantUnin bool
	}{
		{
			name:     "defaults",
			args:     []string{},
			wantDir:  "C:\\devstack",
			wantDry:  false,
			wantY:    false,
			wantUnin: false,
		},
		{
			name:     "custom install dir and dry-run",
			args:     []string{"--install-dir", "D:\\tools\\devstack", "--dry-run", "-y"},
			wantDir:  "D:\\tools\\devstack",
			wantDry:  true,
			wantY:    true,
			wantUnin: false,
		},
		{
			name:     "uninstall with purge options",
			args:     []string{"--uninstall", "--install-dir", "D:\\devstack", "--purge-distro", "--purge-data"},
			wantDir:  "D:\\devstack",
			wantDry:  false,
			wantY:    false,
			wantUnin: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseFlags(tc.args)
			if err != nil {
				t.Fatalf("parseFlags(%v) error: %v", tc.args, err)
			}
			if opts.InstallDir != tc.wantDir {
				t.Errorf("InstallDir = %q, want %q", opts.InstallDir, tc.wantDir)
			}
			if opts.DryRun != tc.wantDry {
				t.Errorf("DryRun = %v, want %v", opts.DryRun, tc.wantDry)
			}
			if opts.Unattended != tc.wantY {
				t.Errorf("Unattended = %v, want %v", opts.Unattended, tc.wantY)
			}
			if opts.Uninstall != tc.wantUnin {
				t.Errorf("Uninstall = %v, want %v", opts.Uninstall, tc.wantUnin)
			}
		})
	}
}

func TestBuildPowerShellArgs(t *testing.T) {
	opts := InstallerOptions{
		InstallDir:  "C:\\custom-devstack",
		AllowReboot: true,
		SkipWSL:     true,
		DryRun:      true,
		Upgrade:     true,
	}

	args := buildPowerShellArgs(opts, "C:\\temp\\install.ps1")
	cmdStr := strings.Join(args, " ")

	expectedTokens := []string{
		"-ExecutionPolicy Bypass",
		"-File C:\\temp\\install.ps1",
		"-InstallDir C:\\custom-devstack",
		"-AllowReboot",
		"-SkipWSL",
		"-DryRun",
		"-Upgrade",
	}

	for _, token := range expectedTokens {
		if !strings.Contains(cmdStr, token) {
			t.Errorf("PowerShell args %q missing expected token %q", cmdStr, token)
		}
	}
}

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Add file
	fw, err := zw.Create("bin/devstack.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("mock-binary"))

	// Add second file
	sw, err := zw.Create("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = sw.Write([]byte("Write-Host 'hello'"))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	rawBytes := buf.Bytes()
	if err := extractZip(bytes.NewReader(rawBytes), int64(len(rawBytes)), destDir); err != nil {
		t.Fatalf("extractZip error: %v", err)
	}

	binContent, err := os.ReadFile(filepath.Join(destDir, "bin", "devstack.exe"))
	if err != nil || string(binContent) != "mock-binary" {
		t.Fatalf("extracted bin content mismatch: %q, err: %v", string(binContent), err)
	}

	psContent, err := os.ReadFile(filepath.Join(destDir, "install.ps1"))
	if err != nil || string(psContent) != "Write-Host 'hello'" {
		t.Fatalf("extracted ps content mismatch: %q, err: %v", string(psContent), err)
	}
}

func TestBuildPowerShellArgsUninstall(t *testing.T) {
	opts := InstallerOptions{
		InstallDir:  "D:\\custom-devstack",
		Uninstall:   true,
		PurgeDistro: true,
		PurgeData:   true,
		DryRun:      true,
	}

	args := buildPowerShellArgs(opts, "C:\\temp\\uninstall.ps1")
	cmdStr := strings.Join(args, " ")

	expectedTokens := []string{
		"-ExecutionPolicy Bypass",
		"-File C:\\temp\\uninstall.ps1",
		"-InstallDir D:\\custom-devstack",
		"-PurgeDistro",
		"-PurgeData",
		"-DryRun",
	}

	for _, token := range expectedTokens {
		if !strings.Contains(cmdStr, token) {
			t.Errorf("PowerShell args %q missing expected token %q", cmdStr, token)
		}
	}
}

func TestExtractZipRejectsEscapingPaths(t *testing.T) {
	escapingPaths := []string{
		"../outside.txt",
		"nested/../../outside.txt",
		"C:drive-relative.txt",
		"C:/absolute-drive.txt",
	}

	for _, p := range escapingPaths {
		t.Run(p, func(t *testing.T) {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)

			fw, err := zw.Create(p)
			if err != nil {
				// Some path names may be rejected by zip writer directly, which is also fine
				return
			}
			_, _ = fw.Write([]byte("escaped"))
			_ = zw.Close()

			destDir := t.TempDir()
			rawBytes := buf.Bytes()
			err = extractZip(bytes.NewReader(rawBytes), int64(len(rawBytes)), destDir)
			if err == nil {
				t.Fatalf("expected extractZip to reject escaping path %q, got nil", p)
			}
		})
	}
}

func TestLocatePayload(t *testing.T) {
	tempDir := t.TempDir()
	zipPath := filepath.Join(tempDir, "test-payload.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("hello payload"))
	_ = zw.Close()

	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Explicit -zip flag
	opts := InstallerOptions{ZipPath: zipPath}
	reader, size, err := locatePayload(opts)
	if err != nil {
		t.Fatalf("locatePayload with -zip failed: %v", err)
	}
	if size != int64(buf.Len()) {
		t.Errorf("got size %d, want %d", size, buf.Len())
	}
	if closer, ok := reader.(interface{ Close() error }); ok {
		_ = closer.Close()
	}

	// 2. DEVSTACK_PAYLOAD_ZIP environment variable
	t.Setenv("DEVSTACK_PAYLOAD_ZIP", zipPath)
	reader2, size2, err := locatePayload(InstallerOptions{})
	if err != nil {
		t.Fatalf("locatePayload with DEVSTACK_PAYLOAD_ZIP failed: %v", err)
	}
	if size2 != int64(buf.Len()) {
		t.Errorf("got size %d, want %d", size2, buf.Len())
	}
	if closer, ok := reader2.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
