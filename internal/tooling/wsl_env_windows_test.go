//go:build windows

package tooling

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestNewWSLEnvFileHasProtectedWindowsDACL(t *testing.T) {
	file, err := NewWSLEnvFile(map[string]string{"DB_PASSWORD": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Cleanup()

	descriptor, err := windows.GetNamedSecurityInfo(file.Path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("env file DACL is not protected from inheritance")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 2 {
		t.Fatalf("env file DACL ACE count = %v, want 2 (current user + SYSTEM)", dacl)
	}
}
