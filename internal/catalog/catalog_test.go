package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLoadProductionCatalog(t *testing.T) {
	cat, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Entries) != 6 {
		t.Fatalf("Load returned %d entries, want 6", len(cat.Entries))
	}

	want := map[string]struct {
		url    string
		sha256 string
		size   int64
	}{
		"node@24.22.1": {
			url:    "https://nodejs.org/dist/v24.22.1/node-v24.22.1-linux-x64.tar.gz",
			sha256: "eb6728e77c576a35162c122c45ccadf224d89ff00abc8dae46cc2897c6002d7c",
			size:   58290700,
		},
		"node@22.23.4": {
			url:    "https://nodejs.org/dist/v22.23.4/node-v22.23.4-linux-x64.tar.gz",
			sha256: "b8492284c76a54baf58d61668dd0e41ee0fc98adabf9503d30f05d08f55b4406",
			size:   56807066,
		},
	}
	wantPHP := map[string]struct {
		url    string
		sha256 string
		size   int64
	}{
		"php@8.2.35-r1": {
			url:    "https://github.com/AramZahedi/dev-web-stack/releases/download/php-runtimes-2026-10-25/php-8.2.35-r1-linux-x64.tar.gz",
			sha256: "d46d33864e44abb45cb7fe93b6d17f21e8c95dbb011946be6fa94e18f2328d8a",
			size:   240637304,
		},
		"php@8.3.36-r1": {
			url:    "https://github.com/AramZahedi/dev-web-stack/releases/download/php-runtimes-2026-10-25/php-8.3.36-r1-linux-x64.tar.gz",
			sha256: "13cb17fc011b25ed252a931d17adbd173106eabee1baf8e25699be75a83e7baa",
			size:   240885758,
		},
		"php@8.4.27-r1": {
			url:    "https://github.com/AramZahedi/dev-web-stack/releases/download/php-runtimes-2026-10-25/php-8.4.27-r1-linux-x64.tar.gz",
			sha256: "e9b11892703d4788094960ee20fc0aa67e046a3de7d92c209da9cffa11f6678c",
			size:   242291453,
		},
		"php@8.5.12-r1": {
			url:    "https://github.com/AramZahedi/dev-web-stack/releases/download/php-runtimes-2026-10-25/php-8.5.12-r1-linux-x64.tar.gz",
			sha256: "5de8588f20f4238ef843b67b5738060e8ed42c76aa84fbb81a8900dd32fd2f57",
			size:   245214512,
		},
	}

	for _, entry := range cat.Entries {
		if entry.Kind == "php" {
			expected, ok := wantPHP[entry.ID]
			if !ok {
				t.Fatalf("unexpected PHP catalog entry %q", entry.ID)
			}
			if entry.Build != "linux-x64" || entry.Revision != 1 {
				t.Errorf("%s build/revision = %q/%d", entry.ID, entry.Build, entry.Revision)
			}
			if entry.URL != expected.url || entry.SHA256 != expected.sha256 || entry.Size != expected.size {
				t.Errorf("%s PHP artifact metadata does not match", entry.ID)
			}
			if entry.ID != "php@"+entry.Version+"-r1" {
				t.Errorf("%s id does not encode php@version-r1", entry.ID)
			}
			continue
		}
		expected, ok := want[entry.ID]
		if !ok {
			t.Fatalf("unexpected catalog entry %q", entry.ID)
		}
		if entry.Kind != "node" {
			t.Errorf("%s kind = %q, want node", entry.ID, entry.Kind)
		}
		if entry.Build != "linux-x64" {
			t.Errorf("%s build = %q, want linux-x64", entry.ID, entry.Build)
		}
		if entry.URL != expected.url {
			t.Errorf("%s url = %q, want %q", entry.ID, entry.URL, expected.url)
		}
		if entry.SHA256 != expected.sha256 {
			t.Errorf("%s sha256 = %q, want %q", entry.ID, entry.SHA256, expected.sha256)
		}
		if entry.Size != expected.size {
			t.Errorf("%s size = %d, want %d", entry.ID, entry.Size, expected.size)
		}
		if entry.ID != entry.Kind+"@"+entry.Version {
			t.Errorf("%s id does not encode kind@version", entry.ID)
		}
		if strings.Contains(strings.ToLower(entry.ID), "test") {
			t.Errorf("production catalog contains test-only entry %q", entry.ID)
		}
	}

	entry, ok := cat.Find("node@24.22.1")
	if !ok || entry.Version != "24.22.1" {
		t.Fatalf("Find(node@24.22.1) = %+v, %v", entry, ok)
	}
	if _, ok := cat.Find("node@999.0.0"); ok {
		t.Fatal("Find returned a missing entry")
	}

	entry, ok = cat.ForKindVersion("node", "22.23.4")
	if !ok || entry.ID != "node@22.23.4" {
		t.Fatalf("ForKindVersion(node, 22.23.4) = %+v, %v", entry, ok)
	}
	if _, ok := cat.ForKindVersion("node", "20.0.0"); ok {
		t.Fatal("ForKindVersion returned a missing version")
	}
	if _, ok := cat.ForKindVersion("php", "8.3.0"); ok {
		t.Fatal("ForKindVersion returned a missing kind")
	}
}

func TestLoadFromValidatesEntries(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Entry)
		wantErr string
	}{
		{name: "valid", mutate: func(*Entry) {}},
		{name: "wrong kind", mutate: func(e *Entry) { e.Kind = "ruby"; e.ID = "ruby@" + e.Version }, wantErr: "kind"},
		{name: "kind case", mutate: func(e *Entry) { e.Kind = "Node" }, wantErr: "kind"},
		{name: "bad version", mutate: func(e *Entry) { e.Version = "v24.99.0" }, wantErr: "version"},
		{name: "short version", mutate: func(e *Entry) { e.Version = "24.99" }, wantErr: "version"},
		{name: "id mismatch", mutate: func(e *Entry) { e.ID = "node@24.99.1" }, wantErr: "id"},
		{name: "bad build", mutate: func(e *Entry) { e.Build = "linux-arm64" }, wantErr: "build"},
		{name: "unsafe build", mutate: func(e *Entry) { e.Build = "../escape" }, wantErr: "build"},
		{name: "http url", mutate: func(e *Entry) { e.URL = strings.Replace(e.URL, "https://", "http://", 1) }, wantErr: "https"},
		{name: "wrong filename", mutate: func(e *Entry) {
			e.URL = strings.Replace(e.URL, "node-v24.99.0-linux-x64.tar.gz", "node-v24.99.0-linux-arm64.tar.gz", 1)
		}, wantErr: "filename"},
		{name: "missing checksum", mutate: func(e *Entry) { e.SHA256 = "" }, wantErr: "sha256"},
		{name: "short checksum", mutate: func(e *Entry) { e.SHA256 = "abcd" }, wantErr: "sha256"},
		{name: "uppercase checksum", mutate: func(e *Entry) { e.SHA256 = strings.ToUpper(e.SHA256) }, wantErr: "sha256"},
		{name: "zero size", mutate: func(e *Entry) { e.Size = 0 }, wantErr: "size"},
		{name: "negative size", mutate: func(e *Entry) { e.Size = -1 }, wantErr: "size"},
		{name: "empty id", mutate: func(e *Entry) { e.ID = "" }, wantErr: "id"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := validEntry()
			test.mutate(&entry)
			_, err := loadEntries(entry)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadFrom: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("LoadFrom accepted invalid entry, want error containing %q", test.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.wantErr)) {
				t.Fatalf("LoadFrom error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadFromRejectsDuplicateEntriesAndEmptyCatalog(t *testing.T) {
	entry := validEntry()
	if _, err := loadEntries(entry, entry); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate entries error = %v, want duplicate", err)
	}
	if _, err := LoadFrom(bytes.NewReader([]byte(`{"entries":[]}`))); err == nil {
		t.Fatal("LoadFrom accepted an empty catalog")
	}
	if _, err := LoadFrom(bytes.NewReader([]byte(`not json`))); err == nil {
		t.Fatal("LoadFrom accepted malformed JSON")
	}
}

func TestParseIDRevisions(t *testing.T) {
	for _, tc := range []struct {
		raw, kind, version string
		rev                int
		ok                 bool
	}{
		{"node@24.22.1", "node", "24.22.1", 0, true},
		{"php@8.4.4-r1", "php", "8.4.4", 1, true},
		{"php@8.4.4-r12", "php", "8.4.4", 12, true},
		{"php@8.4.4", "", "", 0, false},
		{"node@24.22.1-r1", "", "", 0, false},
		{"php@8.4.4-r0", "", "", 0, false},
		{"php@8.4.4-r01", "", "", 0, false},
		{"ruby@3.4.0", "", "", 0, false},
		{"php@8.4-r1", "", "", 0, false},
		{"", "", "", 0, false},
	} {
		got, err := ParseID(tc.raw)
		if tc.ok == (err != nil) {
			t.Fatalf("%q: err = %v, want ok = %v", tc.raw, err, tc.ok)
		}
		if tc.ok && (got.Kind != tc.kind || got.Version != tc.version || got.Revision != tc.rev) {
			t.Fatalf("%q = %+v", tc.raw, got)
		}
	}

	if got := FormatID("php", "8.4.4", 1); got != "php@8.4.4-r1" {
		t.Fatalf("FormatID(php) = %q", got)
	}
	if got := FormatID("node", "24.22.1", 0); got != "node@24.22.1" {
		t.Fatalf("FormatID(node) = %q", got)
	}
}

func TestCatalogSelectsNewestRevision(t *testing.T) {
	cat := catalogFor(t, phpEntry("8.4.4", 1), phpEntry("8.4.4", 2), phpEntry("8.4.3", 9))
	e, ok := cat.ForKindVersion("php", "8.4.4")
	if !ok || e.Revision != 2 {
		t.Fatalf("ForKindVersion = %+v, %v", e, ok)
	}
	e, ok = cat.NewestForLine("php", "8.4")
	if !ok || e.Revision != 2 || e.Version != "8.4.4" {
		t.Fatalf("NewestForLine = %+v, %v", e, ok)
	}
	e, ok = cat.ForKindVersion("php", "8.4.3")
	if !ok || e.Revision != 9 {
		t.Fatalf("ForKindVersion older version = %+v, %v", e, ok)
	}
	if _, ok := cat.NewestForLine("php", "8.5"); ok {
		t.Fatal("NewestForLine returned a missing line")
	}
	if _, ok := cat.NewestForLine("node", "8.4"); ok {
		t.Fatal("NewestForLine returned a missing kind")
	}
}

func TestPHPEntryRequiresPinnedHTTPSArtifact(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Entry)
		wantErr string
	}{
		{name: "valid", mutate: func(*Entry) {}},
		{name: "http url", mutate: func(e *Entry) {
			e.URL = strings.Replace(e.URL, "https://", "http://", 1)
		}, wantErr: "https"},
		{name: "wrong filename", mutate: func(e *Entry) {
			e.URL = "https://example.test/dist/php-8.4.4-linux-x64.tar.gz"
		}, wantErr: "filename"},
		{name: "node filename", mutate: func(e *Entry) {
			e.URL = "https://example.test/dist/node-v8.4.4-linux-x64.tar.gz"
		}, wantErr: "filename"},
		{name: "missing revision", mutate: func(e *Entry) {
			e.ID = "php@8.4.4"
			e.Revision = 0
			e.URL = "https://example.test/dist/php-8.4.4-linux-x64.tar.gz"
		}, wantErr: "revision"},
		{name: "zero revision", mutate: func(e *Entry) {
			e.Revision = 0
			e.URL = "https://example.test/dist/php-8.4.4-linux-x64.tar.gz"
		}, wantErr: "revision"},
		{name: "revision mismatch", mutate: func(e *Entry) {
			e.Revision = 2
		}, wantErr: "id"},
		{name: "uppercase sha256", mutate: func(e *Entry) {
			e.SHA256 = strings.ToUpper(e.SHA256)
		}, wantErr: "sha256"},
		{name: "query string", mutate: func(e *Entry) {
			e.URL += "?download=1"
		}, wantErr: "query"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := phpEntry("8.4.4", 1)
			test.mutate(&entry)
			_, err := loadEntries(entry)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("loadEntries: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("loadEntries accepted invalid PHP entry, want error containing %q", test.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), test.wantErr) {
				t.Fatalf("loadEntries error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestNodeEntryRejectsRevision(t *testing.T) {
	entry := validEntry()
	entry.Revision = 1
	entry.ID = "node@24.99.0-r1"
	if _, err := loadEntries(entry); err == nil || !strings.Contains(err.Error(), "revision") {
		t.Fatalf("loadEntries error = %v, want node revision rejection", err)
	}
}

func TestLineOf(t *testing.T) {
	for version, want := range map[string]string{
		"8.4.4":   "8.4",
		"8.2.29":  "8.2",
		"24.22.1": "24.22",
		"8.4":     "8.4",
	} {
		if got := LineOf(version); got != want {
			t.Errorf("LineOf(%q) = %q, want %q", version, got, want)
		}
	}
}

func phpEntry(version string, revision int) Entry {
	return Entry{
		ID:       FormatID("php", version, revision),
		Kind:     "php",
		Version:  version,
		Build:    "linux-x64",
		Revision: revision,
		URL:      fmt.Sprintf("https://example.test/dist/php-%s-r%d-linux-x64.tar.gz", version, revision),
		SHA256:   strings.Repeat("b", 64),
		Size:     1234,
	}
}

func catalogFor(t *testing.T, entries ...Entry) Catalog {
	t.Helper()
	cat, err := loadEntries(entries...)
	if err != nil {
		t.Fatalf("loadEntries: %v", err)
	}
	return cat
}

func TestParseMatchesLoad(t *testing.T) {
	fromParse, err := parse([]byte(catalogJSON))
	if err != nil {
		t.Fatalf("parse embedded catalog: %v", err)
	}
	fromLoad, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(fromParse.Entries) != len(fromLoad.Entries) {
		t.Fatalf("parse entries = %d, Load entries = %d", len(fromParse.Entries), len(fromLoad.Entries))
	}
	for i := range fromParse.Entries {
		if fromParse.Entries[i] != fromLoad.Entries[i] {
			t.Errorf("entry %d differs: parse %+v, Load %+v", i, fromParse.Entries[i], fromLoad.Entries[i])
		}
	}
}

func validEntry() Entry {
	return Entry{
		ID:      "node@24.99.0",
		Kind:    "node",
		Version: "24.99.0",
		Build:   "linux-x64",
		URL:     "https://nodejs.org/dist/v24.99.0/node-v24.99.0-linux-x64.tar.gz",
		SHA256:  strings.Repeat("a", 64),
		Size:    1234,
	}
}

func loadEntries(entries ...Entry) (Catalog, error) {
	data, err := json.Marshal(Catalog{Entries: entries})
	if err != nil {
		return Catalog{}, err
	}
	return LoadFrom(bytes.NewReader(data))
}
