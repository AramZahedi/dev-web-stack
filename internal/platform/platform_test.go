package platform

import "testing"

func TestDefaultRoot(t *testing.T) {
	if got := DefaultRoot("linux"); got != "/data/devstack" {
		t.Fatalf("linux root = %q", got)
	}
	if got := DefaultRoot("windows"); got != `C:\devstack` {
		t.Fatalf("windows root = %q", got)
	}
	if got := DefaultRoot("darwin"); got != "/data/devstack" {
		t.Fatalf("darwin root = %q", got)
	}
}

func TestDefaultProjectsRoot(t *testing.T) {
	if got := DefaultProjectsRoot("/data/devstack", "linux"); got != "/data/devstack/projects" {
		t.Fatalf("linux projects = %q", got)
	}
	if got := DefaultProjectsRoot(`C:\devstack`, "windows"); got != `C:\devstack\projects` {
		t.Fatalf("windows projects = %q", got)
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
