//go:build windows

package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestValidateSourceRootWindows(t *testing.T) {
	dir := t.TempDir()
	if err := validateSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: dir}); err != nil {
		t.Fatalf("existing directory: %v", err)
	}

	missing := filepath.Join(dir, "missing")
	err := validateSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: missing})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing directory error = %v", err)
	}

	file := filepath.Join(dir, "file.txt")
	if writeErr := os.WriteFile(file, []byte("x"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	err = validateSourceRoot(model.Site{SourceMode: model.SourceWindows, HostPath: file})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file path error = %v", err)
	}

	if err := validateSourceRoot(model.Site{SourceMode: model.SourceLocal, HostPath: missing}); err != nil {
		t.Fatalf("local mode should be validated elsewhere: %v", err)
	}
}
