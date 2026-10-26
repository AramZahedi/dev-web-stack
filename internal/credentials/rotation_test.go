//go:build !windows

package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNewSecret(t *testing.T) {
	first, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret() = %v", err)
	}
	second, err := NewSecret()
	if err != nil {
		t.Fatalf("NewSecret() = %v", err)
	}
	for _, value := range []string{first, second} {
		if !secretPattern.MatchString(value) {
			t.Errorf("NewSecret() = %q, want 64 lowercase hex characters", value)
		}
	}
	if first == second {
		t.Fatal("NewSecret() returned the same value twice")
	}
}

func TestStoreReplace(t *testing.T) {
	root := t.TempDir()
	store := &Store{Root: root}
	oldValue, err := store.Write("mysql", "devstack_password")
	if err != nil {
		t.Fatalf("Write() = %v", err)
	}
	replacement, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Replace("mysql", "devstack_password", replacement); err != nil {
		t.Fatalf("Replace() = %v", err)
	}
	got, err := store.Read("mysql", "devstack_password")
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if got != replacement {
		t.Fatalf("Read() = %q, want replacement", got)
	}
	if got == oldValue {
		t.Fatal("Replace() retained the old value")
	}
	requireMode(t, filepath.Join(root, "secrets", "mysql"), SecretDirMode)
	requireMode(t, filepath.Join(root, "secrets", "mysql", "devstack_password"), SecretFileMode)
}

func TestStoreWriteChownsToOwner(t *testing.T) {
	root := t.TempDir()
	ownerUID := os.Getuid()
	if os.Geteuid() == 0 {
		ownerUID = 65534
	}
	store := &Store{Root: root, OwnerUID: ownerUID}
	if _, err := store.Write("postgresql", "devstack_password"); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	requireOwner(t, filepath.Join(root, "secrets", "postgresql", "devstack_password"), ownerUID)
	requireOwner(t, filepath.Join(root, "secrets", "postgresql"), ownerUID)
	requireOwner(t, filepath.Join(root, "secrets"), ownerUID)
}

func TestStoreReplaceChownsToOwner(t *testing.T) {
	root := t.TempDir()
	seed := &Store{Root: root}
	if _, err := seed.Write("mysql", "devstack_password"); err != nil {
		t.Fatal(err)
	}

	ownerUID := os.Getuid()
	if os.Geteuid() == 0 {
		ownerUID = 65534
	}
	store := &Store{Root: root, OwnerUID: ownerUID}
	replacement, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Replace("mysql", "devstack_password", replacement); err != nil {
		t.Fatalf("Replace() = %v", err)
	}

	path := filepath.Join(root, "secrets", "mysql", "devstack_password")
	requireMode(t, path, SecretFileMode)
	requireOwner(t, path, ownerUID)
	requireOwner(t, filepath.Join(root, "secrets", "mysql"), ownerUID)
	requireMode(t, filepath.Join(root, "secrets", "mysql"), SecretDirMode)
}

func TestStoreReplaceRejectsInvalidInput(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	tests := []struct {
		name    string
		engine  string
		secret  string
		value   string
		wantErr error
	}{
		{name: "invalid engine", engine: "../mysql", secret: "devstack_password", value: strings.Repeat("a", 64), wantErr: ErrInvalidName},
		{name: "invalid secret", engine: "mysql", secret: "../password", value: strings.Repeat("a", 64), wantErr: ErrInvalidName},
		{name: "empty value", engine: "mysql", secret: "devstack_password", value: "", wantErr: ErrEmptySecret},
		{name: "whitespace value", engine: "mysql", secret: "devstack_password", value: " " + strings.Repeat("a", 63), wantErr: ErrInvalidSecretValue},
		{name: "newline value", engine: "mysql", secret: "devstack_password", value: strings.Repeat("a", 63) + "\n", wantErr: ErrInvalidSecretValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Replace(tt.engine, tt.secret, tt.value)
			if err == nil {
				t.Fatal("Replace() = nil, want an error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Replace() error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	missing := &Store{}
	if err := missing.Replace("mysql", "devstack_password", strings.Repeat("a", 64)); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("Replace() error = %v, want ErrInvalidStore", err)
	}
}

func requireOwner(t *testing.T, path string, wantUID int) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat %s: unsupported Sys() type %T", path, info.Sys())
	}
	if int(stat.Uid) != wantUID {
		t.Errorf("%s owner uid = %d, want %d", path, stat.Uid, wantUID)
	}
}
