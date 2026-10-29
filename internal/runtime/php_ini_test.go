package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePHPBaseIniWritesCompensatingExtensionDir(t *testing.T) {
	temp := t.TempDir()
	arch := filepath.Join(temp, "lib", "php", "extensions", "no-debug-non-zts-20240924")
	if err := os.MkdirAll(arch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arch, "opcache.so"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(temp, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(temp, "etc", "php.ini")
	if err := os.WriteFile(ini, []byte("[PHP]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "runtimes", "php@8.4.27-r1")

	if err := ensurePHPBaseIni(temp, target); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal(err)
	}
	want := `extension_dir = "` + filepath.ToSlash(filepath.Join(target, "lib", "php", "extensions", "no-debug-non-zts-20240924")) + `"`
	if !strings.Contains(string(first), want) {
		t.Fatalf("php.ini = %q, missing %q", first, want)
	}
	if err := ensurePHPBaseIni(temp, target); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(ini)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(second), "extension_dir ="); got != 1 {
		t.Fatalf("php.ini has %d extension_dir lines:\n%s", got, second)
	}
}
