package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRoot(t *testing.T) {
	if got := DefaultRoot("linux"); got != "/data/devstack" {
		t.Fatalf("linux root = %q", got)
	}
	userHome, _ := os.UserHomeDir()
	expectedWindows := filepath.Join(userHome, ".devstack")
	if userHome == "" {
		expectedWindows = filepath.Join(os.Getenv("USERPROFILE"), ".devstack")
	}
	if got := DefaultRoot("windows"); got != expectedWindows {
		t.Fatalf("windows root = %q, want %q", got, expectedWindows)
	}
	if got := DefaultRoot("darwin"); got != "/data/devstack" {
		t.Fatalf("darwin root = %q", got)
	}
}

func TestDefaultProjectsRoot(t *testing.T) {
	if got := DefaultProjectsRoot("/data/devstack", "linux"); got != "/data/devstack/projects" {
		t.Fatalf("linux projects = %q", got)
	}
	winRoot := `C:\Users\Test\.devstack`
	expectedProjects := `C:\Users\Test\.devstack\projects`
	if got := DefaultProjectsRoot(winRoot, "windows"); got != expectedProjects {
		t.Fatalf("windows projects = %q, want %q", got, expectedProjects)
	}
}

func TestLegacyWindowsRootDetection(t *testing.T) {
	defaultRoot := t.TempDir()
	legacyRoot := t.TempDir()
	if _, ok := legacyWindowsRoot(defaultRoot, legacyRoot); ok {
		t.Fatal("empty roots were treated as a legacy install")
	}
	if err := os.MkdirAll(filepath.Join(legacyRoot, "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyRoot, "system", "install-manifest.json"), []byte(`{"stage":"complete"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := legacyWindowsRoot(defaultRoot, legacyRoot)
	if !ok || got != legacyRoot {
		t.Fatalf("legacyWindowsRoot = %q, %v; want %q, true", got, ok, legacyRoot)
	}
	if err := os.MkdirAll(filepath.Join(defaultRoot, "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, "system", "install-manifest.json"), []byte(`{"stage":"complete"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := legacyWindowsRoot(defaultRoot, legacyRoot); ok || got != "" {
		t.Fatalf("legacyWindowsRoot with completed default = %q, %v; want no legacy", got, ok)
	}
}

func TestValidateDevstackName(t *testing.T) {
	for _, name := range []string{"config.yaml", "state.db", "config-manifest.json", "serving-provider", "devstack-tool-php"} {
		if err := ValidateDevstackName(name); err != nil {
			t.Fatalf("ValidateDevstackName(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", ".config-manifest.json", ".serving-provider", ".secret-abc", ".tmp-1"} {
		if err := ValidateDevstackName(name); err == nil {
			t.Fatalf("ValidateDevstackName(%q) accepted a dot name", name)
		}
	}
	// Project-owned dot paths are not devstack-generated names; only the
	// basename policy is enforced here.
	if err := ValidateDevstackName("project"); err != nil {
		t.Fatalf("plain project name rejected: %v", err)
	}
}

func TestTempName(t *testing.T) {
	if got := TempName("state.db"); got != "state.db.tmp" {
		t.Fatalf("TempName = %q", got)
	}
	if got := TempName(".hidden"); got != "hidden.tmp" {
		t.Fatalf("TempName dot = %q", got)
	}
}
