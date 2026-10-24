package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/catalog"
)

func checksumFor(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func nodeEntry(version string) catalog.Entry {
	return catalog.Entry{
		ID:      "node@" + version,
		Kind:    "node",
		Version: version,
		Build:   "linux-x64",
		URL:     fmt.Sprintf("https://nodejs.org/dist/v%s/node-v%s-linux-x64.tar.gz", version, version),
		SHA256:  checksumFor("node-" + version),
		Size:    1000,
	}
}

// artifactContent is the synthetic bytes for one PHP artifact. Tests write the
// same bytes into --artifacts so the entry's checksum and size are real.
func artifactContent(version string, revision int) []byte {
	return []byte(fmt.Sprintf("php artifact %s revision %d\n", version, revision))
}

func phpEntry(version string, revision int) catalog.Entry {
	content := artifactContent(version, revision)
	sum := sha256.Sum256(content)
	return catalog.Entry{
		ID:       catalog.FormatID("php", version, revision),
		Kind:     "php",
		Version:  version,
		Build:    "linux-x64",
		Revision: revision,
		URL: fmt.Sprintf(
			"https://releases.example.invalid/php/php-%s-r%d-linux-x64.tar.gz",
			version,
			revision,
		),
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(content)),
	}
}

// writeArtifacts creates the artifact directory catalogmerge must verify. The
// entries' URLs determine the exact filenames; the bytes determine the
// checksum and size.
func writeArtifacts(t *testing.T, entries []catalog.Entry) string {
	t.Helper()
	dir := tempDir(t)
	for _, entry := range entries {
		name := filepath.Base(entry.URL)
		if err := os.WriteFile(filepath.Join(dir, name), artifactContent(entry.Version, entry.Revision), 0o644); err != nil {
			t.Fatalf("write artifact %s: %v", name, err)
		}
	}
	return dir
}

func artifactPath(dir string, entry catalog.Entry) string {
	return filepath.Join(dir, filepath.Base(entry.URL))
}

// validPHPEntries returns a complete 8.2/8.3/8.4/8.5 set in a deliberately
// shuffled order so the tests exercise deterministic sorting.
func validPHPEntries() []catalog.Entry {
	return []catalog.Entry{
		phpEntry("8.4.2", 1),
		phpEntry("8.5.0", 1),
		phpEntry("8.2.29", 1),
		phpEntry("8.3.15", 1),
		phpEntry("8.4.2", 2),
	}
}

func cloneEntries(entries []catalog.Entry) []catalog.Entry {
	return append([]catalog.Entry(nil), entries...)
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeCatalog(t *testing.T, path string, entries []catalog.Entry) {
	t.Helper()
	writeJSONFile(t, path, catalog.Catalog{Entries: entries})
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func runMerge(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

// tempDir uses a neutral prefix so a failure message that includes the file
// path cannot accidentally satisfy an assertion about the error text.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "catalogmerge-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func entryIDs(entries []catalog.Entry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestMergePreservesNodesAndSortsPHP(t *testing.T) {
	current := catalog.Catalog{Entries: []catalog.Entry{
		nodeEntry("24.22.1"),
		phpEntry("8.2.0", 9), // stale build that the merge must replace
		nodeEntry("22.23.4"),
	}}
	incoming := catalog.Catalog{Entries: validPHPEntries()}
	artifacts := writeArtifacts(t, incoming.Entries)

	merged, err := mergeCatalogs(current, incoming, artifacts)
	if err != nil {
		t.Fatalf("mergeCatalogs: %v", err)
	}

	wantIDs := []string{
		"node@24.22.1",
		"node@22.23.4",
		"php@8.2.29-r1",
		"php@8.3.15-r1",
		"php@8.4.2-r1",
		"php@8.4.2-r2",
		"php@8.5.0-r1",
	}
	gotIDs := entryIDs(merged.Entries)
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("merged ids = %v, want %v", gotIDs, wantIDs)
	}
	if _, ok := merged.Find("php@8.2.0-r9"); ok {
		t.Fatalf("stale PHP entry survived the merge")
	}

	first, err := json.Marshal(merged)
	if err != nil {
		t.Fatalf("marshal first merge: %v", err)
	}
	second, err := mergeCatalogs(current, incoming, artifacts)
	if err != nil {
		t.Fatalf("second mergeCatalogs: %v", err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second merge: %v", err)
	}
	if !bytes.Equal(first, secondJSON) {
		t.Fatalf("merge output is not deterministic:\n%s\n%s", first, secondJSON)
	}
}

func TestRunWritesDeterministicCatalog(t *testing.T) {
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")
	outputPath := filepath.Join(dir, "merged.json")

	writeCatalog(t, currentPath, []catalog.Entry{
		nodeEntry("24.22.1"),
		nodeEntry("22.23.4"),
	})
	incoming := validPHPEntries()
	writeCatalog(t, incomingPath, incoming)
	artifacts := writeArtifacts(t, incoming)

	stdout, stderr, err := runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", artifacts,
		"--output", outputPath,
	)
	if err != nil {
		t.Fatalf("run: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "wrote") {
		t.Fatalf("stdout does not report the write: %q", stdout)
	}

	first := readFile(t, outputPath)
	if !bytes.HasSuffix(first, []byte("\n")) {
		t.Fatalf("output does not end with a newline")
	}

	stdout, stderr, err = runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", artifacts,
		"--output", outputPath,
	)
	if err != nil {
		t.Fatalf("second run: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "wrote") {
		t.Fatalf("second stdout does not report the write: %q", stdout)
	}
	if second := readFile(t, outputPath); !bytes.Equal(first, second) {
		t.Fatalf("output is not deterministic:\n%s\n%s", first, second)
	}

	loaded, err := catalog.LoadFrom(bytes.NewReader(first))
	if err != nil {
		t.Fatalf("merged catalog does not load: %v", err)
	}
	wantIDs := "node@24.22.1,node@22.23.4,php@8.2.29-r1,php@8.3.15-r1,php@8.4.2-r1,php@8.4.2-r2,php@8.5.0-r1"
	if got := strings.Join(entryIDs(loaded.Entries), ","); got != wantIDs {
		t.Fatalf("merged ids = %s, want %s", got, wantIDs)
	}

	var document struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	for _, entry := range document.Entries {
		_, hasRevision := entry["revision"]
		if entry["kind"] == "node" && hasRevision {
			t.Fatalf("node entry %v unexpectedly carries a revision", entry["id"])
		}
		if entry["kind"] == "php" && entry["revision"] != float64(1) && entry["revision"] != float64(2) {
			t.Fatalf("php entry %v has an unexpected revision: %v", entry["id"], entry["revision"])
		}
	}
}

func TestRunDefaultsOutputToCatalog(t *testing.T) {
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")

	writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
	incoming := validPHPEntries()
	writeCatalog(t, incomingPath, incoming)
	artifacts := writeArtifacts(t, incoming)
	before := readFile(t, currentPath)

	if _, stderr, err := runMerge(t, "--catalog", currentPath, "--php-catalog", incomingPath, "--artifacts", artifacts); err != nil {
		t.Fatalf("run: %v (stderr: %s)", err, stderr)
	}
	after := readFile(t, currentPath)
	if bytes.Equal(before, after) {
		t.Fatalf("run without --output did not update the catalog")
	}
	loaded, err := catalog.LoadFrom(bytes.NewReader(after))
	if err != nil {
		t.Fatalf("updated catalog does not load: %v", err)
	}
	if _, ok := loaded.Find("php@8.5.0-r1"); !ok {
		t.Fatalf("updated catalog does not contain the 8.5 build: %v", entryIDs(loaded.Entries))
	}
}

func TestCheckValidatesWithoutWriting(t *testing.T) {
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")
	outputPath := filepath.Join(dir, "out.json")

	writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
	incoming := validPHPEntries()
	writeCatalog(t, incomingPath, incoming)
	artifacts := writeArtifacts(t, incoming)
	catalogBefore := readFile(t, currentPath)
	sentinel := []byte("do not replace me\n")
	if err := os.WriteFile(outputPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	stdout, stderr, err := runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", artifacts,
		"--output", outputPath,
		"--check",
	)
	if err != nil {
		t.Fatalf("check: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "check ok") {
		t.Fatalf("check output does not report success: %q", stdout)
	}
	if after := readFile(t, outputPath); !bytes.Equal(after, sentinel) {
		t.Fatalf("--check wrote the output file: %q", after)
	}
	if after := readFile(t, currentPath); !bytes.Equal(after, catalogBefore) {
		t.Fatalf("--check wrote the current catalog")
	}
}

func TestMissingRequiredLine(t *testing.T) {
	for _, line := range []string{"8.2", "8.3", "8.4", "8.5"} {
		t.Run(line, func(t *testing.T) {
			entries := validPHPEntries()
			artifacts := writeArtifacts(t, entries)
			filtered := make([]catalog.Entry, 0, len(entries))
			for _, entry := range entries {
				if catalog.LineOf(entry.Version) != line {
					filtered = append(filtered, entry)
				}
			}

			dir := tempDir(t)
			currentPath := filepath.Join(dir, "current.json")
			incomingPath := filepath.Join(dir, "incoming.json")
			outputPath := filepath.Join(dir, "out.json")
			writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
			writeCatalog(t, incomingPath, filtered)
			sentinel := []byte("do not replace me\n")
			if err := os.WriteFile(outputPath, sentinel, 0o644); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}

			_, _, err := runMerge(t,
				"--catalog", currentPath,
				"--php-catalog", incomingPath,
				"--artifacts", artifacts,
				"--output", outputPath,
			)
			if err == nil {
				t.Fatalf("merge accepted a catalog without the %s line", line)
			}
			if !strings.Contains(err.Error(), line) {
				t.Fatalf("error %q does not name the missing line %s", err, line)
			}
			if after := readFile(t, outputPath); !bytes.Equal(after, sentinel) {
				t.Fatalf("missing %s line wrote partial output: %q", line, after)
			}
		})
	}
}

func TestInvalidPHPCatalogLeavesOutputUntouched(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func([]catalog.Entry) []catalog.Entry
	}{
		{
			name: "duplicate id",
			want: "duplicate",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				return append(entries, entries[0])
			},
		},
		{
			name: "http url",
			want: "https",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].URL = strings.Replace(entries[0].URL, "https://", "http://", 1)
				return entries
			},
		},
		{
			name: "uppercase checksum",
			want: "sha256",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].SHA256 = strings.Repeat("A", 64)
				return entries
			},
		},
		{
			name: "short checksum",
			want: "sha256",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].SHA256 = entries[0].SHA256[:63]
				return entries
			},
		},
		{
			name: "zero size",
			want: "size",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].Size = 0
				return entries
			},
		},
		{
			name: "wrong basename",
			want: "basename",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].URL = "https://releases.example.invalid/php/php-9.9.9-r1-linux-x64.tar.gz"
				return entries
			},
		},
		{
			name: "kind is not php",
			want: "php",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0] = nodeEntry("8.4.2")
				return entries
			},
		},
		{
			name: "wrong build",
			want: "build",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].Build = "linux-arm64"
				return entries
			},
		},
		{
			name: "zero revision",
			want: "revision",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].Revision = 0
				entries[0].ID = catalog.FormatID("php", entries[0].Version, 0)
				return entries
			},
		},
		{
			name: "id does not match revision",
			want: "id",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].ID = catalog.FormatID("php", entries[0].Version, entries[0].Revision+1)
				return entries
			},
		},
		{
			name: "directory url",
			want: "directory",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].URL += "/"
				return entries
			},
		},
		{
			name: "url query",
			want: "query",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].URL += "?download=1"
				return entries
			},
		},
		{
			name: "empty catalog",
			want: "no entries",
			mutate: func([]catalog.Entry) []catalog.Entry {
				return nil
			},
		},
		{
			name: "unsupported php line",
			want: "unsupported PHP line",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				return append(entries, phpEntry("9.0.0", 1))
			},
		},
		{
			name: "empty hostname",
			want: "hostname",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].URL = strings.Replace(entries[0].URL, "releases.example.invalid", ":443", 1)
				return entries
			},
		},
		{
			name: "double slash path",
			want: "empty path segment",
			mutate: func(entries []catalog.Entry) []catalog.Entry {
				entries[0].URL = strings.Replace(entries[0].URL, "/php/", "//php/", 1)
				return entries
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempDir(t)
			currentPath := filepath.Join(dir, "current.json")
			incomingPath := filepath.Join(dir, "incoming.json")
			outputPath := filepath.Join(dir, "out.json")

			writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
			writeCatalog(t, incomingPath, tc.mutate(cloneEntries(validPHPEntries())))
			artifacts := writeArtifacts(t, validPHPEntries())
			sentinel := []byte("do not replace me\n")
			if err := os.WriteFile(outputPath, sentinel, 0o644); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}

			_, _, err := runMerge(t,
				"--catalog", currentPath,
				"--php-catalog", incomingPath,
				"--artifacts", artifacts,
				"--output", outputPath,
			)
			if err == nil {
				t.Fatalf("invalid PHP catalog was accepted")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if after := readFile(t, outputPath); !bytes.Equal(after, sentinel) {
				t.Fatalf("invalid PHP catalog wrote partial output: %q", after)
			}
			temporary, err := filepath.Glob(filepath.Join(dir, ".catalogmerge-*"))
			if err != nil {
				t.Fatalf("glob temporary files: %v", err)
			}
			if len(temporary) != 0 {
				t.Fatalf("invalid PHP catalog left temporary files: %v", temporary)
			}
		})
	}
}

func TestMalformedPHPCatalogFailsBeforeWriting(t *testing.T) {
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")
	outputPath := filepath.Join(dir, "out.json")

	writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
	artifacts := writeArtifacts(t, validPHPEntries())
	if err := os.WriteFile(incomingPath, []byte(`{"entries":[{"id":`), 0o644); err != nil {
		t.Fatalf("write incoming catalog: %v", err)
	}
	sentinel := []byte("do not replace me\n")
	if err := os.WriteFile(outputPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	_, _, err := runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", artifacts,
		"--output", outputPath,
	)
	if err == nil {
		t.Fatalf("malformed PHP catalog was accepted")
	}
	if after := readFile(t, outputPath); !bytes.Equal(after, sentinel) {
		t.Fatalf("malformed PHP catalog wrote output: %q", after)
	}
}

func TestInvalidCurrentCatalogFailsBeforeWriting(t *testing.T) {
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")
	outputPath := filepath.Join(dir, "out.json")

	if err := os.WriteFile(currentPath, []byte("{\"entries\":["), 0o644); err != nil {
		t.Fatalf("write current catalog: %v", err)
	}
	writeCatalog(t, incomingPath, validPHPEntries())
	artifacts := writeArtifacts(t, validPHPEntries())
	sentinel := []byte("do not replace me\n")
	if err := os.WriteFile(outputPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	_, _, err := runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", artifacts,
		"--output", outputPath,
	)
	if err == nil {
		t.Fatalf("malformed current catalog was accepted")
	}
	if after := readFile(t, outputPath); !bytes.Equal(after, sentinel) {
		t.Fatalf("malformed current catalog wrote output: %q", after)
	}
}

func TestWriteFailureLeavesExistingOutputIntact(t *testing.T) {
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")
	outputPath := filepath.Join(dir, "catalog-output")

	writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
	writeCatalog(t, incomingPath, validPHPEntries())
	artifacts := writeArtifacts(t, validPHPEntries())
	if err := os.MkdirAll(outputPath, 0o755); err != nil {
		t.Fatalf("create output directory: %v", err)
	}
	marker := filepath.Join(outputPath, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	_, _, err := runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", artifacts,
		"--output", outputPath,
	)
	if err == nil {
		t.Fatalf("writing over an existing directory succeeded")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("write failure removed existing output contents: %v", statErr)
	}
	temporary, globErr := filepath.Glob(filepath.Join(dir, ".catalogmerge-*"))
	if globErr != nil {
		t.Fatalf("glob temporary files: %v", globErr)
	}
	if len(temporary) != 0 {
		t.Fatalf("write failure left temporary files: %v", temporary)
	}
}

func TestArtifactContractRejectsBadArtifacts(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		prepare func(t *testing.T, entries []catalog.Entry, artifacts string) []catalog.Entry
	}{
		{
			name: "checksum mismatch",
			want: "sha256",
			prepare: func(t *testing.T, entries []catalog.Entry, _ string) []catalog.Entry {
				entries[0].SHA256 = strings.Repeat("0", 64)
				return entries
			},
		},
		{
			name: "size mismatch",
			want: "size",
			prepare: func(t *testing.T, entries []catalog.Entry, _ string) []catalog.Entry {
				entries[0].Size++
				return entries
			},
		},
		{
			name: "missing artifact",
			want: "missing",
			prepare: func(t *testing.T, entries []catalog.Entry, artifacts string) []catalog.Entry {
				if err := os.Remove(artifactPath(artifacts, entries[0])); err != nil {
					t.Fatalf("remove artifact: %v", err)
				}
				return entries
			},
		},
		{
			name: "unexpected extra artifact",
			want: "unexpected",
			prepare: func(t *testing.T, entries []catalog.Entry, artifacts string) []catalog.Entry {
				if err := os.WriteFile(filepath.Join(artifacts, "php-8.1.0-r1-linux-x64.tar.gz"), []byte("stray\n"), 0o644); err != nil {
					t.Fatalf("write extra artifact: %v", err)
				}
				return entries
			},
		},
		{
			name: "artifact is a directory",
			want: "regular file",
			prepare: func(t *testing.T, entries []catalog.Entry, artifacts string) []catalog.Entry {
				path := artifactPath(artifacts, entries[0])
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove artifact: %v", err)
				}
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatalf("create artifact directory: %v", err)
				}
				return entries
			},
		},
		{
			name: "artifact is a symlink",
			want: "regular file",
			prepare: func(t *testing.T, entries []catalog.Entry, artifacts string) []catalog.Entry {
				path := artifactPath(artifacts, entries[0])
				target := artifactPath(artifacts, entries[1])
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove artifact: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create artifact symlink: %v", err)
				}
				return entries
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempDir(t)
			currentPath := filepath.Join(dir, "current.json")
			incomingPath := filepath.Join(dir, "incoming.json")
			outputPath := filepath.Join(dir, "out.json")
			writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
			entries := validPHPEntries()
			artifacts := writeArtifacts(t, entries)
			entries = tc.prepare(t, entries, artifacts)
			writeCatalog(t, incomingPath, entries)
			sentinel := []byte("do not replace me\n")
			if err := os.WriteFile(outputPath, sentinel, 0o644); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}

			_, _, err := runMerge(t,
				"--catalog", currentPath,
				"--php-catalog", incomingPath,
				"--artifacts", artifacts,
				"--output", outputPath,
			)
			if err == nil {
				t.Fatalf("bad artifact was accepted")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
			if after := readFile(t, outputPath); !bytes.Equal(after, sentinel) {
				t.Fatalf("bad artifact wrote partial output: %q", after)
			}
		})
	}
}

func TestUnreadableArtifactRejected(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 artifact")
	}
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")
	outputPath := filepath.Join(dir, "out.json")
	writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
	entries := validPHPEntries()
	writeCatalog(t, incomingPath, entries)
	artifacts := writeArtifacts(t, entries)
	path := artifactPath(artifacts, entries[0])
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod artifact: %v", err)
	}
	sentinel := []byte("do not replace me\n")
	if err := os.WriteFile(outputPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	_, _, err := runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", artifacts,
		"--output", outputPath,
	)
	if err == nil {
		t.Fatalf("unreadable artifact was accepted")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Fatalf("error %q does not report a read failure", err)
	}
	if after := readFile(t, outputPath); !bytes.Equal(after, sentinel) {
		t.Fatalf("unreadable artifact wrote partial output: %q", after)
	}
}

func TestMissingArtifactsDirectoryRejected(t *testing.T) {
	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	incomingPath := filepath.Join(dir, "incoming.json")
	outputPath := filepath.Join(dir, "out.json")
	writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})
	writeCatalog(t, incomingPath, validPHPEntries())

	_, _, err := runMerge(t,
		"--catalog", currentPath,
		"--php-catalog", incomingPath,
		"--artifacts", filepath.Join(dir, "does-not-exist"),
		"--output", outputPath,
	)
	if err == nil {
		t.Fatalf("missing artifacts directory was accepted")
	}
	if !strings.Contains(err.Error(), "artifacts directory") {
		t.Fatalf("error %q does not name the artifacts directory", err)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("missing artifacts directory created output: %v", statErr)
	}
}

func TestRunRequiresInputs(t *testing.T) {
	if _, _, err := runMerge(t); err == nil {
		t.Fatalf("run without flags succeeded")
	} else if !strings.Contains(err.Error(), "--catalog") {
		t.Fatalf("error %q does not mention --catalog", err)
	}

	dir := tempDir(t)
	currentPath := filepath.Join(dir, "current.json")
	writeCatalog(t, currentPath, []catalog.Entry{nodeEntry("24.22.1")})

	if _, _, err := runMerge(t, "--catalog", currentPath); err == nil {
		t.Fatalf("run without --php-catalog succeeded")
	} else if !strings.Contains(err.Error(), "--php-catalog") {
		t.Fatalf("error %q does not mention --php-catalog", err)
	}

	incomingPath := filepath.Join(dir, "incoming.json")
	writeCatalog(t, incomingPath, validPHPEntries())
	if _, _, err := runMerge(t, "--catalog", currentPath, "--php-catalog", incomingPath); err == nil {
		t.Fatalf("run without --artifacts succeeded")
	} else if !strings.Contains(err.Error(), "--artifacts") {
		t.Fatalf("error %q does not mention --artifacts", err)
	}
}
