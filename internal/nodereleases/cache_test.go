package nodereleases

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestChecksumForFileRequiresExactlyOneMatchingLine(t *testing.T) {
	other := strings.Repeat("b", 64) + "  node-v19.9.0-darwin-arm64.tar.gz\n"
	valid := fixtureSHA256 + "  node-v19.9.0-linux-x64.tar.gz\n"

	tests := []struct {
		name     string
		data     string
		filename string
		want     string
		wantErr  string
	}{
		{
			name:     "valid line among other platforms",
			data:     valid + other,
			filename: "node-v19.9.0-linux-x64.tar.gz",
			want:     fixtureSHA256,
		},
		{
			name:     "no matching line",
			data:     other,
			filename: "node-v19.9.0-linux-x64.tar.gz",
			wantErr:  "exactly one",
		},
		{
			name:     "duplicate matching line",
			data:     valid + valid,
			filename: "node-v19.9.0-linux-x64.tar.gz",
			wantErr:  "exactly one",
		},
		{
			name:     "uppercase checksum",
			data:     strings.ToUpper(fixtureSHA256) + "  node-v19.9.0-linux-x64.tar.gz\n",
			filename: "node-v19.9.0-linux-x64.tar.gz",
			wantErr:  "lowercase",
		},
		{
			name:     "short checksum",
			data:     "abc123  node-v19.9.0-linux-x64.tar.gz\n",
			filename: "node-v19.9.0-linux-x64.tar.gz",
			wantErr:  "64 lowercase",
		},
		{
			name:     "malformed line",
			data:     "not a checksum line\n",
			filename: "node-v19.9.0-linux-x64.tar.gz",
			wantErr:  "checksum",
		},
		{
			name:     "duplicate unrelated filename",
			data:     other + other,
			filename: "node-v19.9.0-linux-x64.tar.gz",
			wantErr:  "more than once",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := checksumForFile([]byte(test.data), test.filename)
			if test.wantErr != "" {
				if err == nil {
					t.Fatalf("checksumForFile accepted %s", test.name)
				}
				if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.wantErr)) {
					t.Fatalf("checksumForFile error = %q, want it to contain %q", err, test.wantErr)
				}
				if got != "" {
					t.Errorf("checksumForFile returned %q with an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("checksumForFile: %v", err)
			}
			if got != test.want {
				t.Errorf("checksumForFile = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCacheWritesPrivateFiles(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	client := newTestClient(t, home, officialClient(f.Server), func() time.Time { return now })

	if _, err := client.List(context.Background(), ListOptions{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := client.Preflight(context.Background(), fixtureVersion); err != nil {
		t.Fatalf("Preflight: %v", err)
	}

	paths := []struct {
		path string
		dir  bool
		mode os.FileMode
	}{
		{path: filepath.Join(home, "cache", "node"), dir: true, mode: 0o700},
		{path: filepath.Join(home, "cache", "node", "index.json"), mode: 0o600},
		{path: filepath.Join(home, "cache", "node", "shasums"), dir: true, mode: 0o700},
		{path: filepath.Join(home, "cache", "node", "shasums", fixtureVersion+".txt"), mode: 0o600},
		{path: filepath.Join(home, "cache", "node", "sizes"), dir: true, mode: 0o700},
		{path: filepath.Join(home, "cache", "node", "sizes", fixtureVersion+".json"), mode: 0o600},
	}
	for _, entry := range paths {
		info, err := os.Stat(entry.path)
		if err != nil {
			t.Fatalf("stat %s: %v", entry.path, err)
		}
		if entry.dir && !info.IsDir() {
			t.Errorf("%s is not a directory", entry.path)
		}
		if !entry.dir && !info.Mode().IsRegular() {
			t.Errorf("%s is not a regular file", entry.path)
		}
		if runtime.GOOS == "windows" {
			continue
		}
		if got := info.Mode().Perm(); got != entry.mode {
			t.Errorf("%s mode = %o, want %o", entry.path, got, entry.mode)
		}
	}
}

func TestListIgnoresCorruptIndexCache(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "malformed json", data: `{"cached_at":`},
		{name: "empty releases", data: `{"cached_at":"2026-10-30T12:00:00Z","releases":[]}`},
		{name: "invalid release", data: `{"cached_at":"2026-10-30T12:00:00Z","releases":[{"version":"v19.9.0"}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			cacheDir := filepath.Join(home, "cache", "node")
			if err := os.MkdirAll(cacheDir, 0o700); err != nil {
				t.Fatalf("mkdir cache: %v", err)
			}
			if err := os.WriteFile(filepath.Join(cacheDir, "index.json"), []byte(test.data), 0o600); err != nil {
				t.Fatalf("write cache: %v", err)
			}

			client := newTestClient(t, home, officialClientForFailure(), nil)
			snapshot, err := client.List(context.Background(), ListOptions{Offline: true})
			if err != nil {
				t.Fatalf("offline List: %v", err)
			}
			if snapshot.Source != SourceEmbedded {
				t.Fatalf("source = %q, want %q for a corrupt cache", snapshot.Source, SourceEmbedded)
			}
			assertHasVersion(t, snapshot.All, "24.22.1")
		})
	}
}
