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
		{name: "nested child", backend: root + "/team/app"},
		{name: "root itself", backend: root, wantErr: true},
		{name: "root with trailing slash", backend: root + "/", wantErr: true},
		{name: "sibling prefix", backend: root + "-old/app", wantErr: true},
		{name: "sibling component", backend: "/data/devstack/projects2/app", wantErr: true},
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
