//go:build windows

package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestEnsureSourceRootWindows(t *testing.T) {
	dir := t.TempDir()
	if err := ensureSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: dir}); err != nil {
		t.Fatalf("existing directory: %v", err)
	}

	missing := filepath.Join(dir, "nested", "app")
	if err := ensureSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: missing}); err != nil {
		t.Fatalf("missing directory was not created: %v", err)
	}
	if info, err := os.Stat(missing); err != nil || !info.IsDir() {
		t.Fatalf("created directory = %v, %v", info, err)
	}

	file := filepath.Join(dir, "file.txt")
	if writeErr := os.WriteFile(file, []byte("x"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	err := ensureSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: file})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file path error = %v", err)
	}

	if err := ensureSourceRoot(model.Site{SourceMode: model.SourceLocal, HostPath: missing}); err != nil {
		t.Fatalf("local mode should be validated elsewhere: %v", err)
	}
}
