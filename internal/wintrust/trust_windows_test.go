//go:build windows

package wintrust

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandCall struct {
	name string
	args []string
}

type recordingRunner struct {
	calls  []commandCall
	policy string
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, commandCall{name: name, args: append([]string(nil), args...)})
	if name == "reg.exe" {
		switch args[0] {
		case "query":
			if r.policy == "" {
				return []byte("ERROR: The system was unable to find the specified registry value."), errors.New("missing value")
			}
			return []byte("ImportEnterpriseRoots REG_DWORD " + r.policy), nil
		case "add":
			r.policy = "0x1"
		case "delete":
			r.policy = ""
		}
	}
	return nil, nil
}

func TestProtectSystemRestrictsSystemTreeToSystemAndAdministrators(t *testing.T) {
	var protectedPath, protectedSDDL string
	manager := NewManager(&recordingRunner{}, func(path, sddl string) error {
		protectedPath, protectedSDDL = path, sddl
		return nil
	})
	if err := manager.ProtectSystem(context.Background(), DefaultSystemDir); err != nil {
		t.Fatalf("ProtectSystem: %v", err)
	}
	if protectedPath != DefaultSystemDir {
		t.Fatalf("protected path = %q", protectedPath)
	}
	for _, principal := range []string{"SY", "BA"} {
		if !strings.Contains(protectedSDDL, ";;;"+principal+")") {
			t.Errorf("protected DACL lacks principal %s: %s", principal, protectedSDDL)
		}
	}
	if !strings.HasPrefix(protectedSDDL, "O:SYD:P") {
		t.Fatalf("DACL is not protected and SYSTEM-owned: %s", protectedSDDL)
	}
}

func TestProtectSystemRejectsAnyOtherDirectory(t *testing.T) {
	runner := &recordingRunner{}
	err := NewManager(runner, func(string, string) error { t.Fatal("ACL setter called for unsafe path"); return nil }).ProtectSystem(context.Background(), `C:\devstack\projects`)
	if err == nil {
		t.Fatal("ProtectSystem accepted a non-system directory")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unsafe path ran commands: %+v", runner.calls)
	}
}

func TestProtectCAStorageAppliesExactACLOnlyToFixedCAPaths(t *testing.T) {
	var protected []string
	manager := NewManager(&recordingRunner{}, func(path, _ string) error {
		protected = append(protected, path)
		return nil
	})
	if err := manager.ProtectCAPath(context.Background(), DefaultCADir); err != nil {
		t.Fatalf("ProtectCAPath: %v", err)
	}
	if len(protected) != 1 || protected[0] != DefaultCADir {
		t.Fatalf("protected paths = %v", protected)
	}
	if err := manager.ProtectCAPath(context.Background(), `C:\devstack\system\secrets`); err == nil {
		t.Fatal("ProtectCAPath accepted an arbitrary system path")
	}
	if len(protected) != 1 {
		t.Fatalf("unsafe path was protected: %v", protected)
	}
}

func TestValidateExistingCAFileRejectsReparsePoints(t *testing.T) {
	if err := validateExistingCAFile(DefaultRootCertificate, 0, 0); err != nil {
		t.Fatalf("regular inherited file: %v", err)
	}
	if err := validateExistingCAFile(DefaultRootCertificate, os.ModeSymlink, 0x400); err == nil {
		t.Fatal("CA storage accepted a reparse-point file")
	}
	if err := validateExistingCAFile(`C:\devstack\projects\other.key`, 0, 0); err == nil {
		t.Fatal("CA storage accepted a path outside the fixed CA files")
	}
}

func TestRootStoreAndFirefoxPolicyCommands(t *testing.T) {
	install, err := RootInstallArgs(DefaultRootCertificate)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(install, " ") != "-addstore -f Root "+DefaultRootCertificate {
		t.Fatalf("install args = %q", strings.Join(install, " "))
	}
	remove, err := RootRemoveArgs("0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(strings.Join(remove, " "), "-delstore Root 0123456789abcdef0123456789abcdef01234567") {
		t.Fatalf("remove args = %q", strings.Join(remove, " "))
	}
	if _, err := RootRemoveArgs("not-a-thumbprint"); err == nil {
		t.Fatal("RootRemoveArgs accepted an invalid thumbprint")
	}
	if got := FirefoxInstallArgs(); len(got) == 0 || !strings.Contains(strings.Join(got, " "), "ImportEnterpriseRoots") {
		t.Fatalf("Firefox install args = %v", got)
	}
	if got := FirefoxRemoveArgs(); len(got) == 0 || !strings.Contains(strings.Join(got, " "), "ImportEnterpriseRoots") {
		t.Fatalf("Firefox remove args = %v", got)
	}
}

func TestTrustManagerInstallsAndRemovesRootAndFirefoxPolicy(t *testing.T) {
	runner := &recordingRunner{}
	manager := NewManager(runner)
	manager.policyStatePath = filepath.Join(t.TempDir(), "firefox-policy-managed")
	if err := manager.InstallRoot(context.Background(), DefaultRootCertificate); err != nil {
		t.Fatal(err)
	}
	if err := manager.InstallFirefoxPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.RemoveFirefoxPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 5 || runner.calls[0].name != "certutil.exe" || runner.calls[1].args[0] != "query" || runner.calls[2].args[0] != "add" || runner.calls[3].args[0] != "query" || runner.calls[4].args[0] != "delete" {
		t.Fatalf("calls = %+v", runner.calls)
	}
}

func TestFirefoxPolicyPreservesExistingAdminPolicyOnInstallAndRemove(t *testing.T) {
	runner := &recordingRunner{policy: "0x1"}
	manager := NewManager(runner)
	manager.policyStatePath = filepath.Join(t.TempDir(), "firefox-policy-managed")
	if err := manager.InstallFirefoxPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.RemoveFirefoxPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.policy != "0x1" {
		t.Fatalf("pre-existing policy was removed/changed: %q", runner.policy)
	}
	for _, call := range runner.calls {
		if len(call.args) > 0 && (call.args[0] == "add" || call.args[0] == "delete") {
			t.Fatalf("pre-existing policy was modified: %+v", runner.calls)
		}
	}
}

func TestFirefoxPolicyDoesNotOverwriteExistingDisabledPolicy(t *testing.T) {
	runner := &recordingRunner{policy: "0x0"}
	manager := NewManager(runner)
	manager.policyStatePath = filepath.Join(t.TempDir(), "firefox-policy-managed")
	if err := manager.InstallFirefoxPolicy(context.Background()); err == nil {
		t.Fatal("InstallFirefoxPolicy overwrote an existing disabled policy")
	}
	if runner.policy != "0x0" {
		t.Fatalf("existing policy changed to %q", runner.policy)
	}
}
