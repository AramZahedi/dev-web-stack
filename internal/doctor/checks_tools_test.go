package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

func TestCheckToolsWarnsWhenBareCommandsDoNotResolveToManagedShims(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Managed artifacts exist, but /usr/local/bin is not on PATH first.
	for _, path := range []string{
		filepath.Join(tooling.ShimDir(paths), "php"),
		filepath.Join(home, "tools", "composer", "bin", "composer"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := &Service{deps: Deps{
		Config:     config.Config{Home: home},
		ToolBinDir: binDir,
		LookPath: func(name string) (string, error) {
			switch name {
			case "php":
				return "/usr/bin/php", nil
			case "composer":
				return "", errors.New("not found")
			}
			return "", errors.New("not found")
		},
	}}
	check := svc.checkTools()
	if check.Status != StatusWarn {
		t.Fatalf("status = %q detail=%q", check.Status, check.Detail)
	}
	for _, want := range []string{"/usr/bin/php", filepath.Join(binDir, "php"), "composer"} {
		if !strings.Contains(check.Detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, check.Detail)
		}
	}
}

func TestCheckToolsOKWhenManagedShimsWin(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	phpTarget := filepath.Join(tooling.ShimDir(paths), "php")
	composerTarget := filepath.Join(home, "tools", "composer", "bin", "composer")
	for _, path := range []string{phpTarget, composerTarget} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"php": phpTarget, "composer": composerTarget} {
		if err := os.Symlink(target, filepath.Join(binDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	svc := &Service{deps: Deps{
		Config:     config.Config{Home: home},
		ToolBinDir: binDir,
		LookPath: func(name string) (string, error) {
			return filepath.Join(binDir, name), nil
		},
	}}
	check := svc.checkTools()
	if check.Status != StatusOK {
		t.Fatalf("status = %q detail=%q", check.Status, check.Detail)
	}
}
