package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
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
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		applied, err := s.migrationApplied(ctx, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// migrationApplied reports whether version has already been recorded.
func (s *Store) migrationApplied(ctx context.Context, version int) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}
	return found > 0, nil
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
