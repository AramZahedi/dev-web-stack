package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewPathsIncludesServiceLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "devstack")
	p := NewPaths(root)
	if want := filepath.Join(root, "projects"); p.Projects != want {
		t.Errorf("Projects = %q, want %q", p.Projects, want)
	}
	if p.ProjectsRoot != p.Projects {
		t.Errorf("ProjectsRoot = %q, want %q", p.ProjectsRoot, p.Projects)
	}
	if want := filepath.Join(root, "services"); p.Services != want {
		t.Errorf("Services = %q, want %q", p.Services, want)
	}
	if want := filepath.Join(root, "services", "compose.yaml"); p.ServiceCompose != want {
		t.Errorf("ServiceCompose = %q, want %q", p.ServiceCompose, want)
	}
	if want := filepath.Join(root, "secrets"); p.Secrets != want {
		t.Errorf("Secrets = %q, want %q", p.Secrets, want)
	}
	if want := filepath.Join(root, "certs"); p.Certs != want {
		t.Errorf("Certs = %q, want %q", p.Certs, want)
	}
	if want := filepath.Join(root, "backups"); p.Backups != want {
		t.Errorf("Backups = %q, want %q", p.Backups, want)
	}
	if want := filepath.Join(root, "tools"); p.Tools != want {
		t.Errorf("Tools = %q, want %q", p.Tools, want)
	}
	if want := filepath.Join(root, "config", "cli"); p.CLI != want {
		t.Errorf("CLI = %q, want %q", p.CLI, want)
	}
}

func TestEnsureCreatesLayoutAndSecretMode(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.Projects, p.Services, p.Secrets, p.Certs, p.Backups, p.Tools, p.CLI} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("expected directory %s to exist: %v", dir, err)
		}
	}
	info, err := os.Stat(p.Secrets)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("secrets mode = %o, want 0700", info.Mode().Perm())
	}
}
