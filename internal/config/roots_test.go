package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/platform"
)

func TestResolveHomePrecedence(t *testing.T) {
	legacy := func(string) bool { return true }
	noLegacy := func(string) bool { return false }
	base := t.TempDir()
	explicit := filepath.Join(base, "explicit")
	envHome := filepath.Join(base, "env")
	userHome := filepath.Join(base, "user")
	wantLegacy, err := filepath.Abs(filepath.Join(userHome, ".devstack"))
	if err != nil {
		t.Fatal(err)
	}
	wantExplicit, _ := filepath.Abs(explicit)
	wantEnv, _ := filepath.Abs(envHome)
	tests := []struct {
		name     string
		explicit string
		env      string
		userHome string
		legacy   func(string) bool
		want     string
	}{
		{"explicit wins", explicit, envHome, userHome, legacy, wantExplicit},
		{"env wins", "", envHome, userHome, legacy, wantEnv},
		{"legacy detected", "", "", userHome, legacy, wantLegacy},
		{"platform default", "", "", userHome, noLegacy, platform.DefaultRoot(runtime.GOOS)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveHomeFrom(tt.explicit, tt.env, tt.userHome, tt.legacy)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("resolveHomeFrom = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProjectsRootDefaultsBelowHome(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "projects"); cfg.ProjectsRoot != want {
		t.Fatalf("ProjectsRoot = %q, want %q", cfg.ProjectsRoot, want)
	}
}

func TestProjectsRootRoundTrip(t *testing.T) {
	home := t.TempDir()
	cfg, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(t.TempDir(), "code")
	cfg.ProjectsRoot = custom
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectsRoot != custom {
		t.Fatalf("ProjectsRoot = %q, want %q", got.ProjectsRoot, custom)
	}
}

func TestSystemDirectoryIsProtected(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if got, want := p.System, filepath.Join(root, "system"); got != want {
		t.Fatalf("System = %q, want %q", got, want)
	}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p.System)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("system mode = %o, want 0700", info.Mode().Perm())
	}
}
