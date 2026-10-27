//go:build windows

package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOpenWindowsDrivePath guards the SQLite URI for a C:\ path. A file://
// URI with a drive letter in the authority is rejected by modernc sqlite.
func TestOpenWindowsDrivePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer func() { _ = s.Close() }()
	if err := s.SetSetting(context.Background(), "dsn", "ok"); err != nil {
		t.Fatal(err)
	}
}

// TestOpenWindowsPathWithSpaces verifies that SQLite DSN encoding properly
// preserves directories with spaces and punctuation on Windows.
func TestOpenWindowsPathWithSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "My Sites & Projects (x86)", "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer func() { _ = s.Close() }()
	if err := s.SetSetting(context.Background(), "dsn", "ok"); err != nil {
		t.Fatal(err)
	}
}
