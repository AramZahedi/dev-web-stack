package securestore

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveLoadDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSTACK_HOME", home)
	secret := []byte("agent-token-0123456789")
	if err := Save("agent-token", secret); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "system", "secrets", "agent-token")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if bytes.Contains(data, secret) {
			t.Fatal("DPAPI file contains the plaintext secret")
		}
	} else {
		if !bytes.Equal(data, secret) {
			t.Fatalf("stored secret = %q", data)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 600", info.Mode().Perm())
		}
	}
	loaded, err := Load("agent-token")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, secret) {
		t.Fatalf("loaded = %q", loaded)
	}
	if err := Delete("agent-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("agent-token"); !os.IsNotExist(err) {
		t.Fatalf("Load after delete = %v", err)
	}
}

func TestRejectsUnsafeNames(t *testing.T) {
	t.Setenv("DEVSTACK_HOME", t.TempDir())
	for _, name := range []string{"", ".", "..", "../x", `a\b`, "a/b", ".secret"} {
		if err := Save(name, []byte("x")); err == nil {
			t.Fatalf("Save(%q) succeeded", name)
		}
	}
}

func TestLoadRejectsCorruptCiphertext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSTACK_HOME", home)
	dir := filepath.Join(home, "system", "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad"), []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if _, err := Load("bad"); err == nil {
			t.Fatal("corrupt DPAPI blob accepted")
		}
	}
}
