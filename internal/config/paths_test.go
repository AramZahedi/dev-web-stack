package config

import (
	"os"
	"testing"
)

func TestNewPathsIncludesServiceLayout(t *testing.T) {
	p := NewPaths("/data/devstack")
	if p.Projects != "/data/devstack/projects" {
		t.Errorf("Projects = %q, want %q", p.Projects, "/data/devstack/projects")
	}
	if p.Services != "/data/devstack/services" {
		t.Errorf("Services = %q, want %q", p.Services, "/data/devstack/services")
	}
	if p.ServiceCompose != "/data/devstack/services/compose.yaml" {
		t.Errorf("ServiceCompose = %q, want %q", p.ServiceCompose, "/data/devstack/services/compose.yaml")
	}
	if p.Secrets != "/data/devstack/secrets" {
		t.Errorf("Secrets = %q, want %q", p.Secrets, "/data/devstack/secrets")
	}
	if p.Certs != "/data/devstack/certs" {
		t.Errorf("Certs = %q, want %q", p.Certs, "/data/devstack/certs")
	}
	if p.Backups != "/data/devstack/backups" {
		t.Errorf("Backups = %q, want %q", p.Backups, "/data/devstack/backups")
	}
	if p.Tools != "/data/devstack/tools" {
		t.Errorf("Tools = %q, want %q", p.Tools, "/data/devstack/tools")
	}
	if p.CLI != "/data/devstack/config/cli" {
		t.Errorf("CLI = %q, want %q", p.CLI, "/data/devstack/config/cli")
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
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("secrets mode = %o, want 0700", info.Mode().Perm())
	}
}
