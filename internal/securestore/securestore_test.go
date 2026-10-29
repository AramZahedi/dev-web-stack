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

func TestLoadOrCreateAtPersistsAndReloads(t *testing.T) {
	home := t.TempDir()
	first, err := LoadOrCreateAt(home, "api-token", 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("token length = %d, want 32", len(first))
	}
	second, err := LoadOrCreateAt(home, "api-token", 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("LoadOrCreateAt regenerated an existing token")
	}
	if _, err := os.Stat(filepath.Join(home, "system", "secrets", "api-token")); err != nil {
		t.Fatalf("token file: %v", err)
	}
}

func TestLoadOrCreateAtReplacesShortSecret(t *testing.T) {
	home := t.TempDir()
	if err := SaveAt(home, "api-token", []byte("short")); err != nil {
		t.Fatal(err)
	}
	token, err := LoadOrCreateAt(home, "api-token", 32)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 32 {
		t.Fatalf("token length = %d, want 32", len(token))
	}
	stored, err := LoadAt(home, "api-token")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, token) {
		t.Fatal("replacement token was not persisted")
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

func TestEmptySecretHandling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSTACK_HOME", home)
	dir := filepath.Join(home, "system", "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Zero-length file
	if err := os.WriteFile(filepath.Join(dir, "empty-file"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("empty-file"); err == nil {
		t.Fatal("expected error reading empty DPAPI secret file, got nil")
	}
}
