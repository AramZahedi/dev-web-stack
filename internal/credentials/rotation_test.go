package credentials

import (
	"errors"
	"path/filepath"
	"strings"
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

func TestStoreReplaceRejectsInvalidInput(t *testing.T) {
	store := &Store{Root: t.TempDir()}
	tests := []struct {
		name   string
		engine string
		secret string
		value  string
	}{
		{name: "invalid engine", engine: "../mysql", secret: "devstack_password", value: strings.Repeat("a", 64)},
		{name: "invalid secret", engine: "mysql", secret: "../password", value: strings.Repeat("a", 64)},
		{name: "empty value", engine: "mysql", secret: "devstack_password", value: ""},
		{name: "whitespace value", engine: "mysql", secret: "devstack_password", value: " " + strings.Repeat("a", 63)},
		{name: "newline value", engine: "mysql", secret: "devstack_password", value: strings.Repeat("a", 63) + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := store.Replace(tt.engine, tt.secret, tt.value); err == nil {
				t.Fatal("Replace() = nil, want an error")
			}
		})
	}

	missing := &Store{}
	if err := missing.Replace("mysql", "devstack_password", strings.Repeat("a", 64)); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("Replace() error = %v, want ErrInvalidStore", err)
	}
}
