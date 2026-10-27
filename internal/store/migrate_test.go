package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func openStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

func migrationRecorded(t *testing.T, s *Store, version int) bool {
	t.Helper()
	var found int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&found); err != nil {
		t.Fatalf("check migration %d: %v", version, err)
	}
	return found > 0
}

func migrationBackupFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "backups", "*.pre-migrate-*.bak"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	return files
}

func TestFreshDatabaseSkipsMigrationBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s := openStoreAt(t, path)

	if _, err := os.Stat(filepath.Join(dir, "backups")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh database created a backups directory: %v", err)
	}
	if files := migrationBackupFiles(t, dir); len(files) != 0 {
		t.Fatalf("fresh database created backups: %v", files)
	}

	// A normal reopen has no pending migrations and must not start creating
	// backups on every daemon start.
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	s2 := openStoreAt(t, path)
	_ = s2
	if files := migrationBackupFiles(t, dir); len(files) != 0 {
		t.Fatalf("reopen without pending migrations created backups: %v", files)
	}
}

func TestPendingMigrationBackupBeforeApply(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s := openStoreAt(t, path)
	if err := s.SetSetting(ctx, "keep", "value"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	embedded, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	s.migrations = append(embedded, migration{
		version: 9999,
		name:    "9999_probe.sql",
		script:  `CREATE TABLE pre_migration_probe (id INTEGER PRIMARY KEY);`,
	})
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate with pending migration: %v", err)
	}

	files := migrationBackupFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("backup files = %v, want exactly one", files)
	}
	backup := files[0]
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("backup is not a regular file: %s", info.Mode())
	}
	if info.Size() == 0 {
		t.Fatal("backup is empty")
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("backup mode = %o, want 600", got)
	}
	if !strings.Contains(filepath.Base(backup), "state.db.pre-migrate-") {
		t.Fatalf("unexpected backup name %s", filepath.Base(backup))
	}

	backupDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(backup)+"?mode=ro")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() {
		_ = backupDB.Close()
	}()
	var value string
	if err := backupDB.QueryRow(
		`SELECT value FROM settings WHERE key = 'keep'`).Scan(&value); err != nil {
		t.Fatalf("read seeded setting from backup: %v", err)
	}
	if value != "value" {
		t.Fatalf("backup setting = %q, want value", value)
	}
	var probes int
	if err := backupDB.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'pre_migration_probe'`).Scan(&probes); err != nil {
		t.Fatalf("inspect backup schema: %v", err)
	}
	if probes != 0 {
		t.Fatal("backup already contains the pending migration's table; it was taken after migration")
	}

	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'pre_migration_probe'`).Scan(&probes); err != nil {
		t.Fatalf("inspect live schema: %v", err)
	}
	if probes != 1 {
		t.Fatal("the pending migration was not applied to the live database")
	}
	if !migrationRecorded(t, s, 9999) {
		t.Fatal("migration 9999 was not recorded")
	}
}

func TestMigrationBackupFailureBlocksMigration(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s := openStoreAt(t, path)
	if err := s.SetSetting(ctx, "keep", "value"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	// A regular file where the backup directory belongs makes every backup
	// attempt fail, without changing permissions outside the temp directory.
	if err := os.WriteFile(filepath.Join(dir, "backups"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("block backup directory: %v", err)
	}

	embedded, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	s.migrations = append(embedded, migration{
		version: 9998,
		name:    "9998_probe.sql",
		script:  `CREATE TABLE blocked_probe (id INTEGER PRIMARY KEY);`,
	})
	err = s.migrate(ctx)
	if err == nil {
		t.Fatal("migrate succeeded without writing the required backup")
	}
	if !strings.Contains(err.Error(), "backup") {
		t.Fatalf("migrate error does not mention the backup: %v", err)
	}

	if migrationRecorded(t, s, 9998) {
		t.Fatal("migration was recorded even though the backup failed")
	}
	var probes int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'blocked_probe'`).Scan(&probes); err != nil {
		t.Fatalf("inspect live schema: %v", err)
	}
	if probes != 0 {
		t.Fatal("migration ran even though the backup failed")
	}
	if value, err := s.GetSetting(ctx, "keep"); err != nil || value != "value" {
		t.Fatalf("existing data changed after a failed migration: %q, %v", value, err)
	}
}

func TestMigrationBackupRetentionKeepsThree(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s := openStoreAt(t, path)

	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		offset := time.Duration(i) * time.Second
		s.now = func() time.Time { return base.Add(offset) }
		if err := s.backupBeforeMigration(ctx); err != nil {
			t.Fatalf("backup %d: %v", i+1, err)
		}
	}

	files := migrationBackupFiles(t, dir)
	if len(files) != migrationBackupKeep {
		t.Fatalf("backup count = %d, want %d: %v", len(files), migrationBackupKeep, files)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", migrationBackupFilename(path, base))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest backup was not pruned: %v", err)
	}
	for i := 1; i < 4; i++ {
		name := migrationBackupFilename(path, base.Add(time.Duration(i)*time.Second))
		if _, err := os.Stat(filepath.Join(dir, "backups", name)); err != nil {
			t.Fatalf("expected backup %s: %v", name, err)
		}
	}
}

func TestBackupCommitKeepsExistingTargetOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.db.pre-migrate-test.bak")
	temporary := target + ".tmp"
	if err := os.WriteFile(target, []byte("existing valid backup\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(temporary, []byte("partial snapshot\n"), 0o600); err != nil {
		t.Fatalf("write temporary: %v", err)
	}

	err := commitBackupRename(temporary, target, func(string, string) error {
		return errors.New("injected rename failure")
	})
	if err == nil {
		t.Fatal("commitBackupRename succeeded despite the injected rename failure")
	}
	content, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read existing target: %v", readErr)
	}
	if string(content) != "existing valid backup\n" {
		t.Fatalf("existing target changed after a failed rename: %q", content)
	}
	if _, statErr := os.Stat(temporary); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed commit left the temporary file behind: %v", statErr)
	}
}

func TestBackupCommitSets0600AndRemovesTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.db.pre-migrate-test.bak")
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, []byte("snapshot\n"), 0o644); err != nil {
		t.Fatalf("write temporary: %v", err)
	}

	if err := commitBackup(temporary, target); err != nil {
		t.Fatalf("commitBackup: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("target mode = %o, want 600", got)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(content) != "snapshot\n" {
		t.Fatalf("target content = %q", content)
	}
	if _, statErr := os.Stat(temporary); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("successful commit left the temporary file behind: %v", statErr)
	}
}

func TestBackupReplacesStaleTempFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s := openStoreAt(t, path)
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	target := filepath.Join(dir, "backups", migrationBackupFilename(path, base))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("create backups dir: %v", err)
	}
	if err := os.WriteFile(target+".tmp", []byte("stale partial snapshot\n"), 0o600); err != nil {
		t.Fatalf("write stale temporary: %v", err)
	}
	s.now = func() time.Time { return base }

	if err := s.backupBeforeMigration(ctx); err != nil {
		t.Fatalf("backupBeforeMigration: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("backup target missing: %v", err)
	}
	if _, err := os.Stat(target + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temporary file was not cleaned up: %v", err)
	}
}

func TestMigrationBackupRetentionIgnoresTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatalf("create backups dir: %v", err)
	}
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "state.db")
	for i := 0; i < 4; i++ {
		name := migrationBackupFilename(path, base.Add(time.Duration(i)*time.Second))
		if err := os.WriteFile(filepath.Join(backups, name), []byte("backup\n"), 0o600); err != nil {
			t.Fatalf("write backup: %v", err)
		}
	}
	orphan := migrationBackupFilename(path, base.Add(10*time.Second)) + ".tmp"
	if err := os.WriteFile(filepath.Join(backups, orphan), []byte("partial\n"), 0o600); err != nil {
		t.Fatalf("write orphan temporary: %v", err)
	}

	if err := pruneMigrationBackups(backups, filepath.Base(path)); err != nil {
		t.Fatalf("pruneMigrationBackups: %v", err)
	}
	if files := migrationBackupFiles(t, dir); len(files) != migrationBackupKeep {
		t.Fatalf("backup count = %d, want %d: %v", len(files), migrationBackupKeep, files)
	}
	if _, err := os.Stat(filepath.Join(backups, orphan)); err != nil {
		t.Fatalf("temporary file was removed by retention: %v", err)
	}
}
