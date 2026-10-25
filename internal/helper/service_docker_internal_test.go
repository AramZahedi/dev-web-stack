package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func TestParsePSOutputRejectsMalformedJSON(t *testing.T) {
	cases := map[string]string{
		"truncated array":      `[{"Service":"nginx"`,
		"trailing array comma": `[{"Service":"nginx"},]`,
		"truncated line":       "{\"Service\":\"nginx\"}\n{\"Service\":",
		"plain text line":      "warning: something happened",
		"non-object line":      `["nginx"]`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			containers, err := parsePSOutput([]byte(input))
			if err == nil {
				t.Fatalf("parsePSOutput(%q) = %+v, want error", input, containers)
			}
			if containers != nil {
				t.Fatalf("parsePSOutput returned containers with error: %+v", containers)
			}
		})
	}
}

func TestParsePSOutputAcceptsArrayAndLineForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "empty", input: " \n\t ", want: 0},
		{name: "array", input: `[{"Service":"nginx","State":"running"}]`, want: 1},
		{name: "line form", input: "{\"Service\":\"nginx\"}\n{\"Service\":\"redis\"}\n", want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containers, err := parsePSOutput([]byte(tt.input))
			if err != nil {
				t.Fatalf("parsePSOutput: %v", err)
			}
			if len(containers) != tt.want {
				t.Fatalf("parsePSOutput returned %d containers, want %d", len(containers), tt.want)
			}
		})
	}
}

func TestBackupServiceFilenamesAreUniqueWithinSecond(t *testing.T) {
	root := t.TempDir()
	controller := NewDockerServiceController(root, services.Catalog{}, nil)
	fixed := time.Date(2026, 10, 25, 12, 34, 56, 123456789, time.UTC)
	controller.Now = func() time.Time { return fixed }

	dataFile := filepath.Join(root, "services", "mysql", "data", "db.bin")
	if err := os.MkdirAll(filepath.Dir(dataFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataFile, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := controller.backupService(services.IDMySQL)
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	second, err := controller.backupService(services.IDMySQL)
	if err != nil {
		t.Fatalf("second backup: %v", err)
	}
	if first == second {
		t.Fatalf("backup paths are identical: %q", first)
	}
	for _, path := range []string{first, second} {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("backup %q missing or empty: %v", path, err)
		}
		if !strings.HasPrefix(filepath.Base(path), "20261025T123456.123456789Z") {
			t.Errorf("backup %q does not carry the injected second", path)
		}
	}
}
