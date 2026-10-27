package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migration is one embedded, forward-only schema change.
type migration struct {
	version int
	name    string
	script  string
}

// migrate applies every embedded migration that has not been recorded yet.
// Migrations are forward-only and are applied in ascending version order.
//
// Before applying pending migrations to a database that already has applied
// migrations, migrate writes a consistent backup with VACUUM INTO. The backup
// lives in a sibling backups/ directory and is rotated to the newest
// migrationBackupKeep files. If the backup cannot be written, no migration is
// applied; an upgrade must fail closed rather than risk unbacked data.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	migrations, err := s.migrationList()
	if err != nil {
		return err
	}

	pending, appliedCount, err := s.pendingMigrations(ctx, migrations)
	if err != nil {
		return err
	}
	if len(pending) > 0 && appliedCount > 0 {
		if err := s.backupBeforeMigration(ctx); err != nil {
			return fmt.Errorf("pre-migration backup: %w", err)
		}
	}

	for _, m := range pending {
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// migrationList returns the test override when one is set, otherwise the
// embedded production migrations.
func (s *Store) migrationList() ([]migration, error) {
	if s.migrations != nil {
		return s.migrations, nil
	}
	return loadMigrations()
}

// pendingMigrations returns migrations that have not been recorded, and the
// number of already-applied migrations. The applied count distinguishes a
// fresh database (no backup needed) from an existing one.
func (s *Store) pendingMigrations(ctx context.Context, migrations []migration) ([]migration, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, 0, fmt.Errorf("list applied migrations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, 0, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list applied migrations: %w", err)
	}

	pending := make([]migration, 0, len(migrations))
	for _, m := range migrations {
		if !applied[m.version] {
			pending = append(pending, m)
		}
	}
	return pending, len(applied), nil
}

const (
	// migrationBackupKeep is the number of pre-migration backups retained.
	migrationBackupKeep = 3
	// migrationBackupTimestampFormat sorts lexicographically and is readable
	// as a UTC timestamp. Nanoseconds avoid collisions between rapid starts.
	migrationBackupTimestampFormat = "20060102T150405.000000000Z"
)

// migrationBackupFilename returns the documented backup name for dbPath at at.
func migrationBackupFilename(dbPath string, at time.Time) string {
	return filepath.Base(dbPath) + ".pre-migrate-" + at.UTC().Format(migrationBackupTimestampFormat) + ".bak"
}

// backupBeforeMigration writes a consistent copy of the database into the
// sibling backups/ directory and prunes old copies. VACUUM INTO is used while
// the store has a single connection and no open write transaction, so the copy
// contains a consistent snapshot including the WAL.
func (s *Store) backupBeforeMigration(ctx context.Context) error {
	if s.path == "" {
		return fmt.Errorf("database path is empty")
	}
	dir := filepath.Join(filepath.Dir(s.path), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup directory %s: %w", dir, err)
	}

	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	target := filepath.Join(dir, migrationBackupFilename(s.path, now))
	temporary := target + ".tmp"
	// VACUUM INTO refuses to overwrite its destination. A stale temp file from
	// an interrupted previous run is safe to discard; the final target is never
	// touched until the new snapshot is complete.
	if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale backup temporary %s: %w", temporary, err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO `+sqliteStringLiteral(temporary)); err != nil {
		// Remove only the partial temporary file. An existing valid target is
		// left exactly as it was.
		_ = os.Remove(temporary)
		return fmt.Errorf("write backup %s: %w", target, err)
	}
	if err := commitBackup(temporary, target); err != nil {
		return fmt.Errorf("finalize backup %s: %w", target, err)
	}
	if err := pruneMigrationBackups(dir, filepath.Base(s.path)); err != nil {
		return err
	}
	return nil
}

// commitBackup makes the finished temporary snapshot private and durable, then
// renames it over the target. On any failure it removes the temporary file and
// never removes or truncates an existing target.
func commitBackup(temporary, target string) error {
	return commitBackupRename(temporary, target, os.Rename)
}

// commitBackupRename is commitBackup with an injectable rename for failure
// tests.
func commitBackupRename(temporary, target string, rename func(string, string) error) error {
	discard := func() { _ = os.Remove(temporary) }

	if err := os.Chmod(temporary, 0o600); err != nil {
		discard()
		return fmt.Errorf("restrict backup %s: %w", temporary, err)
	}
	// Windows rejects FlushFileBuffers on a read-only handle. Open read-write
	// so Sync works on both platforms; the file is already private.
	file, err := os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		discard()
		return fmt.Errorf("open backup %s for sync: %w", temporary, err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		discard()
		return fmt.Errorf("sync backup %s: %w", temporary, syncErr)
	}
	if closeErr != nil {
		discard()
		return fmt.Errorf("close backup %s: %w", temporary, closeErr)
	}
	if err := rename(temporary, target); err != nil {
		discard()
		return fmt.Errorf("replace backup %s: %w", target, err)
	}
	// Make the rename durable where the filesystem supports directory fsync.
	if directory, err := os.Open(filepath.Dir(target)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

// pruneMigrationBackups keeps the newest keep backups for dbBase. It never
// removes unrelated files, and returns an error instead of deleting more
// backups when the directory cannot be listed or a removal fails.
func pruneMigrationBackups(dir, dbBase string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list migration backups in %s: %w", dir, err)
	}
	prefix := dbBase + ".pre-migrate-"
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) ||
			!strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		names = append(names, name)
	}
	if len(names) <= migrationBackupKeep {
		return nil
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-migrationBackupKeep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("prune migration backup %s: %w", name, err)
		}
	}
	return nil
}

// sqliteStringLiteral quotes a value for the VACUUM INTO target. The driver
// does not consistently accept a bound parameter there, so escape quotes
// explicitly; this is not used for user-facing SQL.
func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// applyMigration runs one migration and records it in a single transaction, so
// a crash leaves either the whole migration applied or none of it.
func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %04d: %w", m.version, err)
	}
	defer func() {
		// No-op after a successful commit; otherwise release the transaction.
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, m.script); err != nil {
		return fmt.Errorf("apply migration %04d (%s): %w", m.version, m.name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("record migration %04d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %04d: %w", m.version, err)
	}
	return nil
}

// loadMigrations reads and validates the embedded migration files. File names
// must look like 0001_name.sql; versions must be positive and unique.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			return nil, err
		}
		if other, ok := seen[version]; ok {
			return nil, fmt.Errorf("duplicate migration version %d in %s and %s", version, other, entry.Name())
		}
		seen[version] = entry.Name()

		script, err := fs.ReadFile(migrationFS, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{
			version: version,
			name:    entry.Name(),
			script:  string(script),
		})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no embedded migrations found")
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	return migrations, nil
}

// migrationVersion parses the numeric prefix of a migration file name.
func migrationVersion(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %s: name must be <version>_<description>.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %s: invalid version prefix %q", name, prefix)
	}
	return version, nil
}
