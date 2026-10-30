package daemon

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardAuthTokenDisabledDoesNotCreateSecret(t *testing.T) {
	home := t.TempDir()
	token, err := dashboardAuthToken(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
	if _, err := os.Stat(filepath.Join(home, "system", "secrets", "api-token")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("api-token created while disabled: %v", err)
	}
}

func TestDashboardAuthTokenEnabledCreatesSecret(t *testing.T) {
	home := t.TempDir()
	token, err := dashboardAuthToken(home, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars", len(token))
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "system", "secrets", "api-token")); err != nil {
		t.Fatal(err)
	}
}
