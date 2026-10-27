package servicecontrol_test

import (
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
)

func TestAgentPathsKeepServiceStateUnderWSLExt4(t *testing.T) {
	paths, err := servicecontrol.NewAgentPaths("/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	got, err := paths.ForService("postgres")
	if err != nil {
		t.Fatal(err)
	}
	want := servicecontrol.AgentServicePaths{
		Config:  "/data/devstack/services/postgres/config",
		Data:    "/data/devstack/services/postgres/data",
		Logs:    "/data/devstack/services/postgres/logs",
		Secrets: "/data/devstack/services/postgres/secrets",
	}
	if got != want {
		t.Fatalf("ForService(postgres) = %+v, want %+v", got, want)
	}
}

func TestAgentPathsRejectWindowsRootsAndUnknownIDs(t *testing.T) {
	for _, root := range []string{`C:\devstack`, `\\wsl.localhost\devstack\data\devstack`} {
		if _, err := servicecontrol.NewAgentPaths(root); err == nil {
			t.Errorf("NewAgentPaths(%q) accepted a Windows/UNC path", root)
		}
	}
	paths, err := servicecontrol.NewAgentPaths("/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../../outside", "postgresql", "arbitrary"} {
		if _, err := paths.ForService(id); err == nil {
			t.Errorf("ForService(%q) accepted an ID outside the fixed catalog", id)
		}
	}
}

func TestAgentPathsNormalizeRootAndProtectSecretDirectory(t *testing.T) {
	paths, err := servicecontrol.NewAgentPaths("/data/devstack/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := paths.ForService("mysql")
	if err != nil {
		t.Fatal(err)
	}
	if got.Secrets != "/data/devstack/services/mysql/secrets" {
		t.Fatalf("secrets path = %q", got.Secrets)
	}
	if mode := paths.SecretDirectoryMode(); mode != 0o700 {
		t.Fatalf("secret directory mode = %#o, want 0700", mode)
	}
}
