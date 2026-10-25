package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/credentials"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// ErrCredentialsRotationUnavailable reports that credential rotation cannot
// run because the privileged helper is not wired into this process.
var ErrCredentialsRotationUnavailable = errors.New("database credential rotation requires the privileged helper")

// CredentialRotator is the privileged helper surface that updates a live
// database account from host secrets. Passwords never cross this interface.
type CredentialRotator interface {
	RotateServiceCredentials(ctx context.Context, id string) error
}

// DatabasePreparer implements Preparer for the MySQL-family, PostgreSQL, and
// Redis catalog services. It owns their base config seeds and credentials, and
// it never overwrites a config file after the first seed.
type DatabasePreparer struct {
	Root    string
	Store   *credentials.Store
	Rotator CredentialRotator
}

type databaseEngine struct {
	id      services.ID
	port    int
	secrets []string
}

var databaseEngines = []databaseEngine{
	{id: services.IDMySQL, port: 3306, secrets: []string{"root_password", "devstack_password"}},
	{id: services.IDMariaDB, port: 3306, secrets: []string{"root_password", "devstack_password"}},
	{id: services.IDPostgreSQL, port: 5432, secrets: []string{"postgres_password", "devstack_password"}},
	// Redis has no password by default because it publishes on loopback only.
	{id: services.IDRedis, port: 6379},
}

// NewDatabasePreparer returns a preparer rooted at the devstack home. An
// optional rotator wires the privileged helper credential rotation RPC.
func NewDatabasePreparer(root string, rotators ...CredentialRotator) *DatabasePreparer {
	clean := strings.TrimSpace(root)
	if clean != "" {
		clean = filepath.Clean(clean)
		if abs, err := filepath.Abs(clean); err == nil {
			clean = abs
		}
	}
	preparer := &DatabasePreparer{Root: clean}
	if len(rotators) > 0 {
		preparer.Rotator = rotators[0]
	}
	return preparer
}

// NewDatabasePreparers returns the preparer registry for every database service.
func NewDatabasePreparers(root string, rotators ...CredentialRotator) map[services.ID]Preparer {
	prep := NewDatabasePreparer(root, rotators...)
	out := make(map[services.ID]Preparer, len(databaseEngines))
	for _, engine := range databaseEngines {
		out[engine.id] = prep
	}
	return out
}

// DatabasePreparerIDs returns the catalog IDs handled by DatabasePreparer.
func DatabasePreparerIDs() []services.ID {
	out := make([]services.ID, 0, len(databaseEngines))
	for _, engine := range databaseEngines {
		out = append(out, engine.id)
	}
	return out
}

// IsDatabaseService reports whether id has database config and credential handling.
func IsDatabaseService(id services.ID) bool {
	_, ok := databaseEngineFor(id)
	return ok
}

// PrepareInstall creates the service directories, seeds base config files only
// when absent, and creates any missing credentials.
func (p *DatabasePreparer) PrepareInstall(ctx context.Context, svc services.Service) error {
	return p.prepare(ctx, svc)
}

// PrepareActivate repeats the idempotent install preparation before a start.
func (p *DatabasePreparer) PrepareActivate(ctx context.Context, svc services.Service) error {
	return p.prepare(ctx, svc)
}

// CommitActivate is a no-op: database files and secrets are committed during
// preparation and container startup owns the rest.
func (p *DatabasePreparer) CommitActivate(context.Context, services.Service) error {
	return nil
}

// CleanupActivate is a no-op: preparation only creates missing files and never
// deletes a data directory or a credential.
func (p *DatabasePreparer) CleanupActivate(context.Context, services.Service, error) error {
	return nil
}

// Credentials returns the explicit devstack administrator credentials. Passwords
// are only returned from this explicit operation.
func (p *DatabasePreparer) Credentials(ctx context.Context, id services.ID) (Credentials, error) {
	if err := ctx.Err(); err != nil {
		return Credentials{}, err
	}
	engine, ok := databaseEngineFor(id)
	if !ok {
		return Credentials{}, fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, id)
	}
	if id == services.IDRedis {
		return Credentials{}, fmt.Errorf("%w: service %q has no password by default", ErrCredentialsUnsupported, id)
	}
	if err := p.validateRoot(); err != nil {
		return Credentials{}, err
	}
	password, err := p.store().Read(string(id), "devstack_password")
	if err != nil {
		return Credentials{}, fmt.Errorf("read service %q credentials: %w", id, err)
	}
	return Credentials{
		Engine:   string(id),
		Username: "devstack",
		Password: password,
		Database: "devstack",
		Host:     "127.0.0.1",
		Port:     engine.port,
	}, nil
}

// RotateCredentials asks the privileged helper to update the live database
// account and its host secret, then returns the new explicit credential bundle.
// A failure leaves both the account and the stored secret on the old value.
func (p *DatabasePreparer) RotateCredentials(ctx context.Context, id services.ID) (Credentials, error) {
	if err := ctx.Err(); err != nil {
		return Credentials{}, err
	}
	if !IsDatabaseService(id) || id == services.IDRedis {
		return Credentials{}, fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, id)
	}
	if err := p.validateRoot(); err != nil {
		return Credentials{}, err
	}
	if p.Rotator == nil {
		return Credentials{}, fmt.Errorf("%w: service %q", ErrCredentialsRotationUnavailable, id)
	}
	if err := p.Rotator.RotateServiceCredentials(ctx, string(id)); err != nil {
		return Credentials{}, fmt.Errorf("rotate service %q credentials: %w", id, err)
	}
	return p.Credentials(ctx, id)
}

func (p *DatabasePreparer) prepare(ctx context.Context, svc services.Service) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine, ok := databaseEngineFor(svc.ID)
	if !ok {
		return fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, svc.ID)
	}
	if err := p.validateRoot(); err != nil {
		return err
	}

	serviceDir := filepath.Join(p.Root, "services", string(svc.ID))
	for _, sub := range []string{"config", "logs"} {
		if err := os.MkdirAll(filepath.Join(serviceDir, sub), 0o755); err != nil {
			return fmt.Errorf("create %s %s directory: %w", svc.ID, sub, err)
		}
	}
	if svc.ID == services.IDPostgreSQL || svc.ID == services.IDMySQL || svc.ID == services.IDMariaDB {
		if err := os.MkdirAll(filepath.Join(serviceDir, "config", "init"), 0o755); err != nil {
			return fmt.Errorf("create %s init directory: %w", svc.ID, err)
		}
	}

	if err := p.seedConfigs(svc.ID, serviceDir); err != nil {
		return err
	}

	// Scaffold the data directory before generating secrets. A crash between
	// the two steps then leaves an empty, retryable data directory instead of
	// secrets that cannot be safely associated with a missing data tree.
	dataDir := filepath.Join(serviceDir, "data")
	dataExists := false
	if _, statErr := os.Lstat(dataDir); statErr == nil {
		dataExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect %s data directory: %w", svc.ID, statErr)
	}
	if !dataExists && len(engine.secrets) > 0 {
		for _, name := range engine.secrets {
			_, statErr := os.Lstat(p.store().Path(string(svc.ID), name))
			if statErr == nil {
				return fmt.Errorf("prepare %s credentials: %w: engine %q",
					svc.ID, credentials.ErrSecretWithoutData, svc.ID)
			}
		}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create %s data directory: %w", svc.ID, err)
	}
	if len(engine.secrets) > 0 {
		if _, err := p.store().Ensure(string(svc.ID), dataDir, engine.secrets...); err != nil {
			return fmt.Errorf("prepare %s credentials: %w", svc.ID, err)
		}
	}
	return nil
}

func (p *DatabasePreparer) validateRoot() error {
	if p == nil || strings.TrimSpace(p.Root) == "" {
		return fmt.Errorf("%w: database preparer root is not configured", ErrCredentialsUnsupported)
	}
	return nil
}

func (p *DatabasePreparer) store() *credentials.Store {
	if p.Store != nil {
		return p.Store
	}
	return &credentials.Store{Root: p.Root}
}

func databaseEngineFor(id services.ID) (databaseEngine, bool) {
	for _, engine := range databaseEngines {
		if engine.id == id {
			return engine, true
		}
	}
	return databaseEngine{}, false
}

func (p *DatabasePreparer) seedConfigs(id services.ID, serviceDir string) error {
	switch id {
	case services.IDMySQL:
		if err := writeFileIfAbsent(filepath.Join(serviceDir, "config", "devstack.cnf"), mysqlDevstackConfig, 0o644); err != nil {
			return err
		}
		return writeFileIfAbsent(filepath.Join(serviceDir, "config", "init", "10-devstack-admin.sql"), mysqlAdminGrant, 0o644)
	case services.IDMariaDB:
		if err := writeFileIfAbsent(filepath.Join(serviceDir, "config", "devstack.cnf"), mysqlDevstackConfig, 0o644); err != nil {
			return err
		}
		return writeFileIfAbsent(filepath.Join(serviceDir, "config", "init", "10-devstack-admin.sql"), mysqlAdminGrant, 0o644)
	case services.IDPostgreSQL:
		for _, file := range []struct {
			path    string
			content string
			mode    os.FileMode
		}{
			{filepath.Join(serviceDir, "config", "postgresql.conf"), postgresqlConfig, 0o644},
			{filepath.Join(serviceDir, "config", "pg_hba.conf"), postgresqlHBA, 0o644},
			{filepath.Join(serviceDir, "config", "init", "10-devstack.sh"), postgresqlInitScript, 0o755},
		} {
			if err := writeFileIfAbsent(file.path, file.content, file.mode); err != nil {
				return err
			}
		}
		return nil
	case services.IDRedis:
		return writeFileIfAbsent(filepath.Join(serviceDir, "config", "redis.conf"), redisConfig, 0o644)
	default:
		return fmt.Errorf("%w: service %q", ErrCredentialsUnsupported, id)
	}
}

// writeFileIfAbsent seeds a file only when it does not exist. Existing files,
// including user edits, are never replaced. It is also safe against a racing
// writer because O_EXCL is the final authority.
func writeFileIfAbsent(path, content string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular config file %s", path)
		}
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("inspect config file %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory for %s: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create config file %s: %w", path, err)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(mode); err != nil {
		cleanup()
		return fmt.Errorf("set config file mode %s: %w", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		cleanup()
		return fmt.Errorf("write config file %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync config file %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close config file %s: %w", path, err)
	}
	return nil
}

const mysqlDevstackConfig = `# Generated by devstack as a safe starting point.
# This file is user-owned after the first install; devstack never overwrites it.
[mysqld]
skip-name-resolve
max_connections=256
character-set-server=utf8mb4
collation-server=utf8mb4_unicode_ci
`

const mysqlAdminGrant = `-- Generated by devstack as a safe starting point.
-- The official entrypoint creates the devstack account with access only to
-- the devstack database; promote it to a local database administrator.
GRANT ALL PRIVILEGES ON *.* TO 'devstack'@'%' WITH GRANT OPTION;
FLUSH PRIVILEGES;
`

const postgresqlConfig = `# Generated by devstack as a safe starting point.
# This file is user-owned after the first install; devstack never overwrites it.
# The container starts postgres with -c config_file=/etc/postgresql/postgresql.conf.
listen_addresses = '*'
port = 5432
data_directory = '/var/lib/postgresql/data'
hba_file = '/etc/postgresql/pg_hba.conf'
`

const postgresqlHBA = `# Generated by devstack as a safe starting point.
# This file is user-owned after the first install; devstack never overwrites it.
# Local socket access is limited to the container. Published TCP access uses SCRAM.
local   all             postgres                                trust
local   all             all                                     trust
host    all             all             127.0.0.1/32            scram-sha-256
host    all             all             ::1/128                 scram-sha-256
host    all             all             0.0.0.0/0               scram-sha-256
`

const postgresqlInitScript = `#!/bin/sh
# Generated by devstack. Creates the devstack role and database idempotently.
set -eu

if [ -z "${POSTGRES_USER:-}" ]; then
	POSTGRES_USER=postgres
fi
if [ -z "${POSTGRES_DEVSTACK_PASSWORD:-}" ]; then
	echo "devstack bootstrap: POSTGRES_DEVSTACK_PASSWORD is not set" >&2
	exit 1
fi

password="$POSTGRES_DEVSTACK_PASSWORD"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<EOSQL
\set ds_password '$password'
SELECT format('CREATE ROLE devstack LOGIN CREATEDB CREATEROLE PASSWORD %L', :'ds_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'devstack')
\gexec
SELECT format('ALTER ROLE devstack WITH LOGIN CREATEDB CREATEROLE PASSWORD %L', :'ds_password')
\gexec
SELECT format('CREATE DATABASE devstack OWNER devstack')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'devstack')
\gexec
EOSQL
`

const redisConfig = `# Generated by devstack as a safe starting point.
# This file is user-owned after the first install; devstack never overwrites it.
# Redis publishes on 127.0.0.1 only, so no password is enabled by default.
appendonly yes
appendfilename "appendonly.aof"
dir /data
protected-mode no
`
