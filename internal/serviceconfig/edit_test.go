package serviceconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriteUserBacksUpWithoutUpdatingManifestHash(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "conf.d/generated/app.conf"
	generated := []byte("server { listen 80; }\n")
	if _, err := m.WriteGenerated(rel, generated, 0o644, "initial"); err != nil {
		t.Fatal(err)
	}

	user := []byte("server { listen 8080; # edited\n}\n")
	backup, err := m.WriteUser(rel, user, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("WriteUser did not return a backup path")
	}
	backupBytes, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(backupBytes) != string(generated) {
		t.Fatalf("backup content = %q, want %q", backupBytes, generated)
	}

	status, err := m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Modified {
		t.Fatalf("status = %+v, want modified=true after a user write", status)
	}
	if !status.Generated {
		t.Fatalf("status = %+v, want generated class preserved", status)
	}

	disk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != string(user) {
		t.Fatalf("disk content = %q, want %q", disk, user)
	}
}

func TestWriteUserCreatesMissingFileWithoutBackup(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "conf.d/custom.conf"
	backup, err := m.WriteUser(rel, []byte("custom\n"), 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if backup != "" {
		t.Fatalf("backup = %q, want empty for a new file", backup)
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 0640", info.Mode().Perm())
	}

	status, err := m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if status.Class != ClassCustom || !status.Modified {
		t.Fatalf("status = %+v, want a custom modified file", status)
	}
}

func TestManagersForSameRootShareManifestLock(t *testing.T) {
	root := t.TempDir()
	first, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.mu != second.mu {
		t.Fatal("two managers for the same root do not share a lock")
	}

	var wg sync.WaitGroup
	for i, manager := range []*Manager{first, second} {
		wg.Add(1)
		go func(i int, manager *Manager) {
			defer wg.Done()
			rel := fmt.Sprintf("conf.d/generated/%d.conf", i)
			if _, err := manager.WriteGenerated(rel, []byte("server {}\n"), 0o644, "test"); err != nil {
				t.Errorf("WriteGenerated %d: %v", i, err)
			}
		}(i, manager)
	}
	wg.Wait()
	manifest, err := loadManifest(filepath.Join(root, ".config-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 2 {
		t.Fatalf("manifest entries = %d, want 2", len(manifest.Entries))
	}
}

func TestLatestBaselineTracksGeneratedContent(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	rel := "conf.d/generated/app.conf"
	generated := []byte("server { listen 80; }\n")
	if _, err := m.WriteGenerated(rel, generated, 0o644, "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline, err := m.LatestBaseline(rel)
	if err != nil {
		t.Fatal(err)
	}
	if baseline == "" {
		t.Fatal("generated file has no baseline")
	}
	data, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(generated) {
		t.Fatalf("baseline = %q, want generated content", data)
	}
}

func TestLatestBackupDoesNotMatchDottedSiblingFiles(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"nginx.conf", "nginx.conf.1"} {
		full := filepath.Join(root, rel)
		if err := os.WriteFile(full, []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fixed := time.Date(2026, 10, 26, 11, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return fixed }
	first, err := m.Backup("nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Backup("nginx.conf.1")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("collision backup reused the first path")
	}
	latest, err := m.LatestBackup("nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	if latest != first {
		t.Fatalf("LatestBackup(nginx.conf) = %q, want %q (not %q)", latest, first, second)
	}
}

func TestLatestBackupReturnsNewestVersion(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "nginx.conf"
	full := filepath.Join(root, rel)
	if err := os.WriteFile(full, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return first }
	firstBackup, err := m.Backup(rel)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(full, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return first.Add(time.Minute) }
	secondBackup, err := m.Backup(rel)
	if err != nil {
		t.Fatal(err)
	}
	if secondBackup == firstBackup {
		t.Fatal("second backup reused the first path")
	}

	newest, err := m.LatestBackup(rel)
	if err != nil {
		t.Fatal(err)
	}
	if newest != secondBackup {
		t.Fatalf("LatestBackup = %q, want %q", newest, secondBackup)
	}
	data, err := os.ReadFile(newest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "two\n" {
		t.Fatalf("newest backup content = %q, want two", data)
	}

	missing, err := m.LatestBackup("conf.d/missing.conf")
	if err != nil {
		t.Fatal(err)
	}
	if missing != "" {
		t.Fatalf("missing LatestBackup = %q, want empty", missing)
	}
}

func TestEntryAndRestoreEntryPreserveTracking(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "conf.d/generated/app.conf"
	if _, err := m.WriteGenerated(rel, []byte("generated\n"), 0o644, "initial"); err != nil {
		t.Fatal(err)
	}
	entry, tracked, err := m.Entry(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !tracked {
		t.Fatal("expected a tracked entry")
	}
	if err := m.KeepMine(rel); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Detached {
		t.Fatal("expected KeepMine to detach")
	}

	if err := m.RestoreEntry(rel, entry, true); err != nil {
		t.Fatal(err)
	}
	status, err = m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if status.Detached {
		t.Fatal("RestoreEntry did not restore the detached flag")
	}

	if err := m.RestoreEntry(rel, entry, false); err != nil {
		t.Fatal(err)
	}
	status, err = m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if status.Class != ClassCustom {
		t.Fatalf("class = %q, want custom after removing tracking", status.Class)
	}
}

func TestRestoreFileWritesWithoutTouchingManifest(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "nginx.conf"
	seeded := []byte("seed\n")
	if _, err := m.SeedBase(rel, seeded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreFile(rel, []byte("rolled back\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entry, tracked, err := m.Entry(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !tracked || entry.Hash != sha256Hex(seeded) {
		t.Fatalf("manifest entry changed during RestoreFile: %+v tracked=%t", entry, tracked)
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "rolled back\n" {
		t.Fatalf("content = %q", data)
	}
}

func TestMergeWithBackupReturnsPreviousContent(t *testing.T) {
	root := t.TempDir()
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	rel := "conf.d/generated/app.conf"
	generated := []byte("generated\n")
	if _, err := m.WriteGenerated(rel, generated, 0o644, "initial"); err != nil {
		t.Fatal(err)
	}
	merged := []byte("merged\n")
	backup, err := m.MergeWithBackup(rel, merged)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("MergeWithBackup returned no backup")
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(generated) {
		t.Fatalf("backup = %q, want %q", data, generated)
	}
	status, err := m.Status(rel)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Detached {
		t.Fatal("merge did not detach the file")
	}
}
