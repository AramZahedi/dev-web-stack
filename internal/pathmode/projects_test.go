package pathmode

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestManagedProjectsRoot(t *testing.T) {
	home := t.TempDir()

	t.Run("windows configured root wins", func(t *testing.T) {
		got, err := ManagedProjectsRoot(model.SourceWindows, `C:\custom\projects`, `C:\Users\me`)
		if err != nil {
			t.Fatal(err)
		}
		if got != `C:\custom\projects` {
			t.Fatalf("root = %q", got)
		}
	})

	t.Run("windows falls back to home projects", func(t *testing.T) {
		got, err := ManagedProjectsRoot(model.SourceWindows, "", home)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "projects")
		if got != want {
			t.Fatalf("root = %q, want %q", got, want)
		}
	})

	t.Run("local falls back to home projects", func(t *testing.T) {
		got, err := ManagedProjectsRoot(model.SourceLocal, "", home)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "projects")
		if got != want {
			t.Fatalf("root = %q, want %q", got, want)
		}
	})

	t.Run("wsl is fixed", func(t *testing.T) {
		got, err := ManagedProjectsRoot(model.SourceWSL, `C:\ignored`, `C:\ignored-home`)
		if err != nil {
			t.Fatal(err)
		}
		if got != WSLProjectsRoot {
			t.Fatalf("root = %q, want %q", got, WSLProjectsRoot)
		}
	})

	t.Run("missing configuration is rejected", func(t *testing.T) {
		if _, err := ManagedProjectsRoot(model.SourceWindows, "", ""); !errors.Is(err, ErrOutsideProjectsRoot) {
			t.Fatalf("err = %v, want ErrOutsideProjectsRoot", err)
		}
	})
}

func TestValidateManagedProjectPathWSL(t *testing.T) {
	root := WSLProjectsRoot

	for _, tc := range []struct {
		name    string
		backend string
		wantErr bool
	}{
		{name: "child", backend: root + "/app"},
		{name: "colon child", backend: root + "/app:v2"},
		{name: "nested child", backend: root + "/team/app"},
		{name: "root itself", backend: root, wantErr: true},
		{name: "root with trailing slash", backend: root + "/", wantErr: true},
		{name: "sibling prefix", backend: root + "-old/app", wantErr: true},
		{name: "sibling component", backend: "/data/devstack/projects2/app", wantErr: true},
		{name: "backslash alias", backend: root + `\app`, wantErr: true},
		{name: "outside", backend: "/data/devstack/system", wantErr: true},
		{name: "empty", backend: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManagedProjectPath(model.SourceWSL, "", tc.backend, root)
			if tc.wantErr {
				if !errors.Is(err, ErrOutsideProjectsRoot) {
					t.Fatalf("err = %v, want ErrOutsideProjectsRoot", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateManagedProjectPathWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path containment")
	}

	root := filepath.Join(t.TempDir(), "projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "app")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("child", func(t *testing.T) {
		if err := ValidateManagedProjectPath(model.SourceWindows, child, "", root); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("case insensitive child", func(t *testing.T) {
		upper := filepath.Join(filepath.Dir(root), "PROJECTS", "APP")
		if err := ValidateManagedProjectPath(model.SourceWindows, upper, "", root); err != nil {
			t.Fatalf("case variant rejected: %v", err)
		}
	})

	t.Run("root itself", func(t *testing.T) {
		if err := ValidateManagedProjectPath(model.SourceWindows, root, "", root); !errors.Is(err, ErrOutsideProjectsRoot) {
			t.Fatalf("err = %v, want ErrOutsideProjectsRoot", err)
		}
	})

	t.Run("sibling prefix", func(t *testing.T) {
		sibling := root + "-old"
		if err := ValidateManagedProjectPath(model.SourceWindows, sibling, "", root); !errors.Is(err, ErrOutsideProjectsRoot) {
			t.Fatalf("err = %v, want ErrOutsideProjectsRoot", err)
		}
	})

	t.Run("symlink escape", func(t *testing.T) {
		outside := t.TempDir()
		escape := filepath.Join(root, "escape")
		if err := os.Symlink(outside, escape); err != nil {
			t.Skipf("cannot create symlink: %v", err)
		}
		target := filepath.Join(escape, "app")
		if err := ValidateManagedProjectPath(model.SourceWindows, target, "", root); !errors.Is(err, ErrOutsideProjectsRoot) {
			t.Fatalf("err = %v, want ErrOutsideProjectsRoot", err)
		}
	})
}

func TestValidateManagedProjectPathRejectsRootAlias(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	// The lexical path is a strict child, but it resolves to the root itself.
	if err := ValidateManagedProjectPath(model.SourceLocal, alias, "", root); !errors.Is(err, ErrOutsideProjectsRoot) {
		t.Fatalf("root alias error = %v, want ErrOutsideProjectsRoot", err)
	}
	if err := ValidateStrictlyContainedPath(root, alias, runtime.GOOS == "windows"); err == nil {
		t.Fatal("ValidateStrictlyContainedPath accepted a root alias")
	}
}

func TestValidateManagedProjectPathAllowsSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real-projects")
	child := filepath.Join(realRoot, "app")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(base, "projects")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	// A lexical child of the symlinked root is accepted.
	if err := ValidateManagedProjectPath(model.SourceLocal, filepath.Join(linkRoot, "app"), "", linkRoot); err != nil {
		t.Fatalf("lexical child of a symlinked root was rejected: %v", err)
	}
	// Local mode canonicalizes the project path before validation, so the
	// resolved child must also be accepted.
	if err := ValidateManagedProjectPath(model.SourceLocal, child, "", linkRoot); err != nil {
		t.Fatalf("resolved child of a symlinked root was rejected: %v", err)
	}
	// The root itself stays invalid in both spellings.
	for _, target := range []string{linkRoot, realRoot} {
		if err := ValidateManagedProjectPath(model.SourceLocal, target, "", linkRoot); !errors.Is(err, ErrOutsideProjectsRoot) {
			t.Fatalf("target %q: err = %v, want ErrOutsideProjectsRoot", target, err)
		}
	}
}

func TestInferSourceMode(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		platform string
		want     model.SourceMode
	}{
		{name: "drive on windows", host: `C:\Users\dev\.devstack\projects\app`, platform: "windows", want: model.SourceWindows},
		{name: "drive on linux", host: `D:\projects\app`, platform: "linux", want: model.SourceWindows},
		{name: "UNC on windows", host: `\\wsl.localhost\devstack\data\devstack\projects\app`, platform: "windows", want: model.SourceWSL},
		{name: "legacy wsl dollar UNC", host: `\\wsl$\devstack\data\devstack\projects\app`, platform: "windows", want: model.SourceWSL},
		{name: "linux local", host: "/home/dev/.devstack/projects/app", platform: "linux", want: model.SourceLocal},
		{name: "windows fallback", host: "/home/dev/.devstack/projects/app", platform: "windows", want: model.SourceWindows},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InferSourceMode(tt.host, tt.platform); got != tt.want {
				t.Fatalf("InferSourceMode(%q, %q) = %q, want %q", tt.host, tt.platform, got, tt.want)
			}
		})
	}
}
