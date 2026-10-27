package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
)

const testName = "php-8.4.4-r1-linux-x64.tar.gz"

func artifact(t *testing.T, dir, name, content string) (string, string, int64) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:]), int64(len(content))
}

func validOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	path, sum, size := artifact(t, dir, testName, "archive")
	return Options{
		Version:  "8.4.4",
		Revision: 1,
		Artifact: path,
		SHA256:   sum,
		Size:     size,
		BaseURL:  "https://downloads.example.test/php",
	}
}

func TestGenerateVerifiesArtifactAndBuildsEntry(t *testing.T) {
	opts := validOptions(t)
	entry, err := Generate(opts)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "php@8.4.4-r1" || entry.Revision != 1 {
		t.Fatalf("%+v", entry)
	}
	if entry.Kind != "php" || entry.Version != "8.4.4" || entry.Build != "linux-x64" {
		t.Fatalf("%+v", entry)
	}
	if entry.URL != "https://downloads.example.test/php/"+testName {
		t.Fatalf("url = %s", entry.URL)
	}
	if entry.SHA256 != opts.SHA256 || entry.Size != opts.Size {
		t.Fatalf("%+v", entry)
	}
}

func TestGenerateAcceptsOneTrailingBaseSlashWithoutDoubling(t *testing.T) {
	opts := validOptions(t)
	opts.BaseURL += "/"
	entry, err := Generate(opts)
	if err != nil {
		t.Fatal(err)
	}
	if entry.URL != "https://downloads.example.test/php/"+testName {
		t.Fatalf("url = %s", entry.URL)
	}
	if strings.Contains(strings.TrimPrefix(entry.URL, "https://"), "//") {
		t.Fatalf("url has a double separator: %s", entry.URL)
	}
}

func TestGenerateWritesExactlyOneEntryToOutput(t *testing.T) {
	opts := validOptions(t)
	dir := t.TempDir()
	opts.Output = filepath.Join(dir, "entry.json")
	entry, err := Generate(opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(opts.Output)
	if err != nil {
		t.Fatal(err)
	}
	var got catalog.Entry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("output is not a single entry: %v\n%s", err, data)
	}
	if got != entry {
		t.Fatalf("output = %+v, want %+v", got, entry)
	}
	if bytes.Contains(data, []byte(`"entries"`)) {
		t.Fatalf("output must not be a catalog wrapper:\n%s", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "entry.json" {
		t.Fatalf("output directory = %v, want only entry.json", entries)
	}
}

func TestGenerateNeverEditsTheEmbeddedCatalog(t *testing.T) {
	path := filepath.Join("..", "..", "internal", "catalog", "catalog.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := validOptions(t)
	if _, err := Generate(opts); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("internal/catalog/catalog.json was modified")
	}
}

func TestGenerateRejections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Options)
		want   string
	}{
		{"missing version", func(o *Options) { o.Version = "" }, "version"},
		{"line version", func(o *Options) { o.Version = "8.4" }, "version"},
		{"two component suffix", func(o *Options) { o.Version = "8.4.4.1" }, "version"},
		{"leading v", func(o *Options) { o.Version = "v8.4.4" }, "version"},
		{"zero revision", func(o *Options) { o.Revision = 0 }, "revision"},
		{"negative revision", func(o *Options) { o.Revision = -1 }, "revision"},
		{"wrong revision in name", func(o *Options) {
			o.Artifact = filepath.Join(filepath.Dir(o.Artifact), "php-8.4.4-r2-linux-x64.tar.gz")
		}, "artifact basename"},
		{"wrong build in name", func(o *Options) {
			o.Artifact = filepath.Join(filepath.Dir(o.Artifact), "php-8.4.4-r1-linux-arm64.tar.gz")
		}, "artifact basename"},
		{"empty artifact", func(o *Options) { o.Artifact = "" }, "artifact basename"},
		{"zero size", func(o *Options) { o.Size = 0 }, "size"},
		{"negative size", func(o *Options) { o.Size = -1 }, "size"},
		{"size mismatch", func(o *Options) { o.Size++ }, "size"},
		{"uppercase sha", func(o *Options) { o.SHA256 = strings.ToUpper(o.SHA256) }, "sha256"},
		{"short sha", func(o *Options) { o.SHA256 = o.SHA256[:63] }, "sha256"},
		{"non hex sha", func(o *Options) { o.SHA256 = strings.Repeat("z", 64) }, "sha256"},
		{"sha mismatch", func(o *Options) { o.SHA256 = strings.Repeat("a", 64) }, "sha256"},
		{"empty base url", func(o *Options) { o.BaseURL = "" }, "base url"},
		{"http base url", func(o *Options) { o.BaseURL = "http://downloads.example.test/php" }, "https"},
		{"ftp base url", func(o *Options) { o.BaseURL = "ftp://downloads.example.test/php" }, "https"},
		{"relative base url", func(o *Options) { o.BaseURL = "downloads.example.test/php" }, "https"},
		{"query base url", func(o *Options) { o.BaseURL = "https://downloads.example.test/php?token=1" }, "query"},
		{"fragment base url", func(o *Options) { o.BaseURL = "https://downloads.example.test/php#frag" }, "fragment"},
		{"empty query base url", func(o *Options) { o.BaseURL = "https://downloads.example.test/php?" }, "query"},
		{"userinfo base url", func(o *Options) { o.BaseURL = "https://user:pass@downloads.example.test/php" }, "userinfo"},
		{"missing host", func(o *Options) { o.BaseURL = "https:///php" }, "host"},
		{"double separator", func(o *Options) { o.BaseURL = "https://downloads.example.test/php//" }, "double"},
		{"missing artifact", func(o *Options) { o.Artifact = filepath.Join(t.TempDir(), testName) }, "artifact"},
		{"directory artifact", func(o *Options) {
			dir := t.TempDir()
			o.Artifact = filepath.Join(dir, testName)
			if err := os.Mkdir(o.Artifact, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "regular file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := validOptions(t)
			tt.mutate(&opts)
			_, err := Generate(opts)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestGenerateRejectsUnwritableOutput(t *testing.T) {
	opts := validOptions(t)
	opts.Output = t.TempDir()
	if _, err := Generate(opts); err == nil {
		t.Fatal("expected a directory output error")
	}
}

func TestGenerateRejectsProductionCatalogOutput(t *testing.T) {
	opts := validOptions(t)
	for _, output := range []string{
		"internal/catalog/catalog.json",
		"./internal/catalog/catalog.json",
		"../internal/catalog/catalog.json",
		filepath.Join(t.TempDir(), "repo", "internal", "catalog", "catalog.json"),
	} {
		opts.Output = output
		_, err := Generate(opts)
		if err == nil || !strings.Contains(err.Error(), "catalog.json") {
			t.Fatalf("output %q error = %v, want a production catalog refusal", output, err)
		}
		if _, statErr := os.Stat(output); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("output %q was created despite the guard: %v", output, statErr)
		}
	}
}

func TestRunPrintsOneEntryToStdout(t *testing.T) {
	opts := validOptions(t)
	var stdout, stderr bytes.Buffer
	args := []string{
		"--version", opts.Version,
		"--revision", "1",
		"--artifact", opts.Artifact,
		"--sha256", opts.SHA256,
		"--size", "7",
		"--base-url", opts.BaseURL,
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}
	var entry catalog.Entry
	if err := json.Unmarshal(stdout.Bytes(), &entry); err != nil {
		t.Fatalf("stdout is not one entry: %v\n%s", err, stdout.String())
	}
	if entry.ID != "php@8.4.4-r1" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestRunWritesOnlyTheOutputFile(t *testing.T) {
	opts := validOptions(t)
	output := filepath.Join(t.TempDir(), "entry.json")
	var stdout, stderr bytes.Buffer
	args := []string{
		"--version", opts.Version,
		"--revision", "1",
		"--artifact", opts.Artifact,
		"--sha256", opts.SHA256,
		"--size", "7",
		"--base-url", opts.BaseURL,
		"--output", output,
	}
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing when --output is set", stdout.String())
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
}
