package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadProductionCatalog(t *testing.T) {
	cat, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.Entries) != 2 {
		t.Fatalf("Load returned %d entries, want 2", len(cat.Entries))
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

	for _, entry := range cat.Entries {
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
		{name: "wrong kind", mutate: func(e *Entry) { e.Kind = "php"; e.ID = "php@" + e.Version }, wantErr: "kind"},
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
