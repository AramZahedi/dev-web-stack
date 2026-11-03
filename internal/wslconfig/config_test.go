package wslconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/wslconfig"
)

func TestApplyMergesAndPreserves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wslconfig")
	original := "[wsl2]\nmemory=8GB\nlocalhostForwarding=true\n\n[experimental]\nautoMemoryReclaim=gradual\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	backup, restart, err := wslconfig.Apply(path, filepath.Join(dir, "backup", ".wslconfig"), wslconfig.Values{
		MemoryMB: 12288, Processors: 8, SwapMB: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !restart || backup == "" {
		t.Fatalf("restart=%t backup=%q", restart, backup)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"memory=12288MB", "processors=8", "swap=4096MB", "localhostForwarding=true", "[experimental]", "autoMemoryReclaim=gradual"} {
		if !strings.Contains(text, want) {
			t.Errorf("merged config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "memory=8GB") {
		t.Errorf("old memory value was not replaced:\n%s", text)
	}
}

func TestApplySameValuesReportsNoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wslconfig")
	values := wslconfig.Values{MemoryMB: 8192, Processors: 8, SwapMB: 2048}
	if _, changed, err := wslconfig.Apply(path, filepath.Join(dir, "backup", ".wslconfig"), values); err != nil || !changed {
		t.Fatalf("first apply changed=%t err=%v", changed, err)
	}
	if _, changed, err := wslconfig.Apply(path, filepath.Join(dir, "backup", ".wslconfig"), values); err != nil || changed {
		t.Fatalf("identical apply changed=%t err=%v, want no change", changed, err)
	}
}

func TestApplyCreatesSectionAndResetRestores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wslconfig")
	original := "[experimental]\nautoMemoryReclaim=gradual\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup", ".wslconfig")
	if _, _, err := wslconfig.Apply(path, backup, wslconfig.Values{MemoryMB: 8192, Processors: 4, SwapMB: 0}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[wsl2]") || !strings.Contains(string(data), "swap=0MB") {
		t.Fatalf("created config missing managed keys:\n%s", data)
	}
	if err := wslconfig.Reset(path, backup); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("reset config = %q, want %q", data, original)
	}
}

func TestResetWithoutBackupRemovesOnlyManagedKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".wslconfig")
	if err := os.WriteFile(path, []byte("[wsl2]\nmemory=8GB\nprocessors=4\nlocalhostForwarding=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := wslconfig.Reset(path, filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "memory=") || strings.Contains(text, "processors=") {
		t.Fatalf("managed keys were not removed:\n%s", text)
	}
	if !strings.Contains(text, "localhostForwarding=true") {
		t.Fatalf("unrelated key was removed:\n%s", text)
	}
}
