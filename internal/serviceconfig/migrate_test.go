package serviceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateManifestStripsConfigPrefixAndPreservesEntries(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := filepath.Join(oldRoot, "config")
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	old := newManifest()
	old.Entries["config/nginx.conf"] = ManifestEntry{
		Path:     "config/nginx.conf",
		Class:    ClassBase,
		Hash:     "abc",
		Detached: true,
		Reason:   "seeded base config",
	}
	old.Entries["data/private.db"] = ManifestEntry{Path: "data/private.db", Class: ClassGenerated}
	if err := saveManifest(filepath.Join(oldRoot, "config-manifest.json"), old); err != nil {
		t.Fatal(err)
	}

	if err := MigrateManifest(oldRoot, newRoot, "config/"); err != nil {
		t.Fatalf("MigrateManifest: %v", err)
	}
	migrated, err := loadManifest(filepath.Join(newRoot, "config-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := migrated.Entries["nginx.conf"]
	if !ok {
		t.Fatalf("migrated entries = %+v, want nginx.conf", migrated.Entries)
	}
	if entry.Path != "nginx.conf" || entry.Class != ClassBase || !entry.Detached || entry.Hash != "abc" {
		t.Fatalf("migrated entry = %+v", entry)
	}
	if _, ok := migrated.Entries["data/private.db"]; ok {
		t.Fatal("migration copied an entry outside the config prefix")
	}

	// A second call must not overwrite a manifest that already exists.
	if err := os.WriteFile(filepath.Join(newRoot, "config-manifest.json"), []byte(`{"version":1,"entries":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateManifest(oldRoot, newRoot, "config/"); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Join(newRoot, "config-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != `{"version":1,"entries":{}}` {
		t.Fatalf("second migration overwrote the destination: %s", again)
	}
}

func TestMigrateManifestIsNoOpWithoutOldManifest(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := filepath.Join(oldRoot, "config")
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MigrateManifest(oldRoot, newRoot, "config/"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(newRoot, "config-manifest.json")); !os.IsNotExist(err) {
		t.Fatalf("migration created a manifest without a source: %v", err)
	}
}

func TestNewManagerMigratesLegacyManifestName(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, legacyManifestName)
	if err := saveManifest(legacy, newManifest()); err != nil {
		t.Fatal(err)
	}
	if _, err := New(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, manifestName)); err != nil {
		t.Fatalf("new manifest missing after migration: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy manifest still present: %v", err)
	}
}

func TestMigrateManifestReadsLegacySourceName(t *testing.T) {
	oldRoot := t.TempDir()
	newRoot := t.TempDir()
	legacy := filepath.Join(oldRoot, legacyManifestName)
	m := newManifest()
	m.Entries["foo.conf"] = ManifestEntry{Path: "foo.conf", Class: ClassCustom, Hash: "x"}
	if err := saveManifest(legacy, m); err != nil {
		t.Fatal(err)
	}
	if err := MigrateManifest(oldRoot, newRoot, ""); err != nil {
		t.Fatal(err)
	}
	migrated, err := loadManifest(filepath.Join(newRoot, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := migrated.Entries["foo.conf"]; !ok {
		t.Fatal("legacy entry was not migrated")
	}
}
