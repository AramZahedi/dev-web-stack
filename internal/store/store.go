// Package store persists devstack state in a local SQLite database. It owns the
// schema migrations and exposes typed CRUD helpers for the shared model types.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Store is a handle to the devstack SQLite database.
type Store struct {
	db   *sql.DB
	path string

	// now is overridable in tests so backup retention can use deterministic
	// timestamps. Production stores always use time.Now.
	now func() time.Time

	// migrations is overridable in tests to simulate a pending schema change
	// without adding fake production migration files.
	migrations []migration
}

// Open opens (and creates when needed) the SQLite database at path, applies the
// embedded migrations, and returns a ready store. The database uses a 5 second
// busy timeout, enforced foreign keys, and WAL journaling.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve database path %q: %w", path, err)
	}

	// The database contains site environment variables and PHP snapshots.
	// Create it 0600 before SQLite opens it so the WAL/SHM siblings inherit
	// owner-only permissions instead of the process umask.
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, fmt.Errorf("store: create database directory: %w", err)
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create database %s: %w", abs, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: close database %s: %w", abs, err)
	}
	if err := os.Chmod(abs, 0o600); err != nil {
		return nil, fmt.Errorf("store: set database permissions %s: %w", abs, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Chmod(abs+suffix, 0o600)
	}

	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	query := dsn.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	dsn.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", abs, err)
	}
	// SQLite allows one writer at a time. Serialising access avoids
	// SQLITE_BUSY between our own connections while the busy timeout covers
	// other processes touching the same file.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, path: abs}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate %s: %w", abs, err)
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

const siteColumns = `id, name, domain, www_alias, backend, host_path, backend_path, kind,
	php_version, php_extensions_json, php_ini_json, php_build, php_ini_raw, php_config_hash,
	node_version, command, env_json,
	document_root, front_controller, spa_fallback, tls, force_https, desired_state,
	status, http_port, fastcgi_port, created_at, updated_at,
	front_controller_file, front_controller_mode, apache_allow_override, spa_fallback_file, build_json`

// CreateSite inserts a new site.
func (s *Store) CreateSite(ctx context.Context, site model.Site) error {
	args, err := siteInsertArgs(site)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sites (`+siteColumns+`) VALUES (
		?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args...)
	if err != nil {
		return fmt.Errorf("create site %q: %w", site.ID, err)
	}
	return nil
}

// CreateSiteWithPHPConfig inserts a PHP site and its applied configuration
// snapshot in one transaction. A crash between the two writes must not leave a
// site row without the configuration it was created with.
func (s *Store) CreateSiteWithPHPConfig(ctx context.Context, site model.Site, cfg model.SitePHPConfig) error {
	args, err := siteInsertArgs(site)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create site %q: begin transaction: %w", site.ID, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sites (`+siteColumns+`) VALUES (
		?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args...); err != nil {
		return fmt.Errorf("create site %q: %w", site.ID, err)
	}
	if err := saveSitePHPConfig(ctx, tx, cfg); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create site %q: commit: %w", site.ID, err)
	}
	return nil
}

// UpdateSite replaces the mutable fields of an existing site. The creation
// timestamp is preserved. It returns ErrNotFound when the site does not exist.
func (s *Store) UpdateSite(ctx context.Context, site model.Site) error {
	phpExtensions, phpIni, env, err := marshalSiteFields(site)
	if err != nil {
		return err
	}
	fcFile := site.FrontControllerFile
	if fcFile == "" {
		fcFile = "index.php"
	}
	fcMode := site.FrontControllerMode
	if fcMode == "" {
		fcMode = "auto"
	}
	override := site.ApacheAllowOverride
	if override == "" {
		override = "all"
	}
	spaFile := site.SPAFallbackFile
	if spaFile == "" {
		spaFile = "index.html"
	}
	buildJSON := "{}"
	if site.Build != nil {
		b, err := json.Marshal(site.Build)
		if err != nil {
			return fmt.Errorf("encode site %q build: %w", site.ID, err)
		}
		buildJSON = string(b)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE sites SET
		name = ?, domain = ?, www_alias = ?, backend = ?, host_path = ?, backend_path = ?,
		kind = ?, php_version = ?, php_extensions_json = ?, php_ini_json = ?,
		php_build = ?, php_ini_raw = ?, php_config_hash = ?,
		node_version = ?, command = ?, env_json = ?, document_root = ?,
		front_controller = ?, spa_fallback = ?, tls = ?, force_https = ?,
		desired_state = ?, status = ?, http_port = ?, fastcgi_port = ?,
		updated_at = ?,
		front_controller_file = ?, front_controller_mode = ?, apache_allow_override = ?,
		spa_fallback_file = ?, build_json = ?
		WHERE id = ?`,
		site.Name, site.Domain, boolInt(site.WWW), string(site.Backend),
		site.HostPath, site.BackendPath, string(site.Kind), site.PHPVersion,
		phpExtensions, phpIni, site.PHPBuild, site.PHPIniRaw, site.PHPConfigHash,
		site.NodeVersion, site.Command, env,
		site.DocumentRoot, boolInt(site.FrontController), boolInt(site.SPAFallback),
		boolInt(site.TLS), boolInt(site.ForceHTTPS), string(site.DesiredState),
		string(site.Status), site.HTTPPort, site.FastCGIPort,
		site.UpdatedAt.Unix(),
		fcFile, fcMode, override, spaFile, buildJSON,
		site.ID)
	if err != nil {
		return fmt.Errorf("update site %q: %w", site.ID, err)
	}
	return requireAffected(res, "site", site.ID)
}

// GetSite returns the site with the given id.
func (s *Store) GetSite(ctx context.Context, id string) (model.Site, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE id = ?`, id)
	return scanSite(row, "id", id)
}

// GetSiteByName returns the site with the given unique name.
func (s *Store) GetSiteByName(ctx context.Context, name string) (model.Site, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE name = ?`, name)
	return scanSite(row, "name", name)
}

// GetSiteByDomain returns the site with the given unique domain.
func (s *Store) GetSiteByDomain(ctx context.Context, domain string) (model.Site, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE domain = ?`, domain)
	return scanSite(row, "domain", domain)
}

// ListSites returns every site ordered by name.
func (s *Store) ListSites(ctx context.Context) ([]model.Site, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+siteColumns+` FROM sites ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	sites := []model.Site{}
	for rows.Next() {
		site, err := scanSite(rows, "", "")
		if err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	return sites, nil
}

// DeleteSite removes a site and its certificate metadata in one transaction.
// It returns ErrNotFound when the site does not exist.
func (s *Store) DeleteSite(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete site %q: %w", id, err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM certs WHERE site_id = ?`, id); err != nil {
		return fmt.Errorf("delete site %q certificate: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete site %q: %w", id, err)
	}
	if err := requireAffected(res, "site", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete site %q: %w", id, err)
	}
	return nil
}

const runtimeColumns = `id, kind, version, backend, path, checksum, installed_at`

// UpsertRuntime inserts a runtime build or replaces it when the id exists.
func (s *Store) UpsertRuntime(ctx context.Context, build model.RuntimeBuild) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO runtimes (`+runtimeColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind, version = excluded.version,
			backend = excluded.backend, path = excluded.path,
			checksum = excluded.checksum, installed_at = excluded.installed_at`,
		build.ID, build.Kind, build.Version, string(build.Backend), build.Path,
		build.Checksum, build.InstalledAt.Unix())
	if err != nil {
		return fmt.Errorf("upsert runtime %q: %w", build.ID, err)
	}
	return nil
}

// GetRuntime returns the runtime build with the given id.
func (s *Store) GetRuntime(ctx context.Context, buildID string) (model.RuntimeBuild, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+runtimeColumns+` FROM runtimes WHERE id = ?`, buildID)
	build, err := scanRuntime(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.RuntimeBuild{}, ErrNotFound
	}
	if err != nil {
		return model.RuntimeBuild{}, fmt.Errorf("get runtime %q: %w", buildID, err)
	}
	return build, nil
}

// ListRuntimes returns every installed runtime ordered by kind and version.
func (s *Store) ListRuntimes(ctx context.Context) ([]model.RuntimeBuild, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runtimeColumns+` FROM runtimes ORDER BY kind, version`)
	if err != nil {
		return nil, fmt.Errorf("list runtimes: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	builds := []model.RuntimeBuild{}
	for rows.Next() {
		build, err := scanRuntime(rows)
		if err != nil {
			return nil, fmt.Errorf("list runtimes: %w", err)
		}
		builds = append(builds, build)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runtimes: %w", err)
	}
	return builds, nil
}

// DeleteRuntime removes a runtime build. It returns ErrNotFound when absent.
func (s *Store) DeleteRuntime(ctx context.Context, buildID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM runtimes WHERE id = ?`, buildID)
	if err != nil {
		return fmt.Errorf("delete runtime %q: %w", buildID, err)
	}
	return requireAffected(res, "runtime", buildID)
}

const certColumns = `site_id, serial, domains_json, not_after, key_path, cert_path`

// SaveCert inserts or replaces the certificate metadata for a site.
func (s *Store) SaveCert(ctx context.Context, cert model.CertRecord) error {
	domains, err := json.Marshal(cert.Domains)
	if err != nil {
		return fmt.Errorf("encode certificate domains: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO certs (`+certColumns+`)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(site_id) DO UPDATE SET
			serial = excluded.serial, domains_json = excluded.domains_json,
			not_after = excluded.not_after, key_path = excluded.key_path,
			cert_path = excluded.cert_path`,
		cert.SiteID, cert.Serial, string(domains), cert.NotAfter.Unix(),
		cert.KeyPath, cert.CertPath)
	if err != nil {
		return fmt.Errorf("save certificate for site %q: %w", cert.SiteID, err)
	}
	return nil
}

// GetCertBySite returns the certificate metadata for a site.
func (s *Store) GetCertBySite(ctx context.Context, siteID string) (model.CertRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+certColumns+` FROM certs WHERE site_id = ?`, siteID)
	cert, err := scanCert(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CertRecord{}, ErrNotFound
	}
	if err != nil {
		return model.CertRecord{}, fmt.Errorf("get certificate for site %q: %w", siteID, err)
	}
	return cert, nil
}

// ListCerts returns certificate metadata for every site ordered by site id.
func (s *Store) ListCerts(ctx context.Context) ([]model.CertRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+certColumns+` FROM certs ORDER BY site_id`)
	if err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	certs := []model.CertRecord{}
	for rows.Next() {
		cert, err := scanCert(rows)
		if err != nil {
			return nil, fmt.Errorf("list certificates: %w", err)
		}
		certs = append(certs, cert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list certificates: %w", err)
	}
	return certs, nil
}

// DeleteCertBySite removes a site's certificate metadata. It returns
// ErrNotFound when no certificate is recorded for the site.
func (s *Store) DeleteCertBySite(ctx context.Context, siteID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM certs WHERE site_id = ?`, siteID)
	if err != nil {
		return fmt.Errorf("delete certificate for site %q: %w", siteID, err)
	}
	return requireAffected(res, "certificate", siteID)
}

const phpDefaultsColumns = `php_version, ini_json, extensions_json`

// SavePHPDefaults inserts or replaces the editable defaults for a PHP version
// line. Nil maps are stored as empty JSON objects.
func (s *Store) SavePHPDefaults(ctx context.Context, d model.PHPDefaults) error {
	if d.Version == "" {
		return errors.New("save php defaults: empty version")
	}
	ini, err := marshalPHPStringMap(d.Ini)
	if err != nil {
		return fmt.Errorf("encode php %s ini defaults: %w", d.Version, err)
	}
	extensions, err := marshalPHPBoolMap(d.Extensions)
	if err != nil {
		return fmt.Errorf("encode php %s extension defaults: %w", d.Version, err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO php_defaults (`+phpDefaultsColumns+`)
		VALUES (?, ?, ?)
		ON CONFLICT(php_version) DO UPDATE SET
			ini_json = excluded.ini_json,
			extensions_json = excluded.extensions_json`,
		d.Version, ini, extensions)
	if err != nil {
		return fmt.Errorf("save php %s defaults: %w", d.Version, err)
	}
	return nil
}

// GetPHPDefaults returns the defaults for a PHP version line. It returns
// ErrNotFound when the version has no saved defaults yet.
func (s *Store) GetPHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+phpDefaultsColumns+` FROM php_defaults WHERE php_version = ?`, version)
	d, err := scanPHPDefaults(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.PHPDefaults{}, ErrNotFound
	}
	if err != nil {
		return model.PHPDefaults{}, fmt.Errorf("get php %s defaults: %w", version, err)
	}
	return d, nil
}

// ListPHPDefaults returns all saved version defaults ordered by version line.
func (s *Store) ListPHPDefaults(ctx context.Context) ([]model.PHPDefaults, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+phpDefaultsColumns+` FROM php_defaults ORDER BY php_version`)
	if err != nil {
		return nil, fmt.Errorf("list php defaults: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	defaults := []model.PHPDefaults{}
	for rows.Next() {
		d, err := scanPHPDefaults(rows)
		if err != nil {
			return nil, fmt.Errorf("list php defaults: %w", err)
		}
		defaults = append(defaults, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list php defaults: %w", err)
	}
	return defaults, nil
}

const sitePHPConfigColumns = `site_id, ini_overrides_json, extension_overrides_json, config_hash, applied_at`

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// SaveSitePHPConfig inserts or replaces the applied PHP configuration snapshot
// for a site.
func (s *Store) SaveSitePHPConfig(ctx context.Context, cfg model.SitePHPConfig) error {
	return saveSitePHPConfig(ctx, s.db, cfg)
}

func saveSitePHPConfig(ctx context.Context, db sqlExecer, cfg model.SitePHPConfig) error {
	if cfg.SiteID == "" {
		return errors.New("save site php config: empty site id")
	}
	ini, err := marshalPHPStringMap(cfg.Ini)
	if err != nil {
		return fmt.Errorf("encode site %q php ini: %w", cfg.SiteID, err)
	}
	extensions, err := marshalPHPBoolMap(cfg.Extensions)
	if err != nil {
		return fmt.Errorf("encode site %q php extensions: %w", cfg.SiteID, err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO site_php_config (`+sitePHPConfigColumns+`)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(site_id) DO UPDATE SET
			ini_overrides_json = excluded.ini_overrides_json,
			extension_overrides_json = excluded.extension_overrides_json,
			config_hash = excluded.config_hash,
			applied_at = excluded.applied_at`,
		cfg.SiteID, ini, extensions, cfg.ConfigHash, cfg.AppliedAt.Unix())
	if err != nil {
		return fmt.Errorf("save site %q php config: %w", cfg.SiteID, err)
	}
	return nil
}

// GetSitePHPConfig returns the applied PHP configuration snapshot for a site.
// It returns ErrNotFound when no snapshot has been saved.
func (s *Store) GetSitePHPConfig(ctx context.Context, siteID string) (model.SitePHPConfig, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+sitePHPConfigColumns+` FROM site_php_config WHERE site_id = ?`, siteID)
	cfg, err := scanSitePHPConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SitePHPConfig{}, ErrNotFound
	}
	if err != nil {
		return model.SitePHPConfig{}, fmt.Errorf("get site %q php config: %w", siteID, err)
	}
	return cfg, nil
}

func scanPHPDefaults(scanner rowScanner) (model.PHPDefaults, error) {
	var (
		d                  model.PHPDefaults
		ini, extensionsRaw string
	)
	if err := scanner.Scan(&d.Version, &ini, &extensionsRaw); err != nil {
		return model.PHPDefaults{}, err
	}
	if err := json.Unmarshal([]byte(ini), &d.Ini); err != nil {
		return model.PHPDefaults{}, fmt.Errorf("decode php %s ini defaults: %w", d.Version, err)
	}
	if err := json.Unmarshal([]byte(extensionsRaw), &d.Extensions); err != nil {
		return model.PHPDefaults{}, fmt.Errorf("decode php %s extension defaults: %w", d.Version, err)
	}
	if d.Ini == nil {
		d.Ini = map[string]string{}
	}
	if d.Extensions == nil {
		d.Extensions = map[string]bool{}
	}
	return d, nil
}

func scanSitePHPConfig(scanner rowScanner) (model.SitePHPConfig, error) {
	var (
		cfg                model.SitePHPConfig
		ini, extensionsRaw string
		appliedAt          int64
	)
	if err := scanner.Scan(&cfg.SiteID, &ini, &extensionsRaw, &cfg.ConfigHash, &appliedAt); err != nil {
		return model.SitePHPConfig{}, err
	}
	if err := json.Unmarshal([]byte(ini), &cfg.Ini); err != nil {
		return model.SitePHPConfig{}, fmt.Errorf("decode site %q php ini: %w", cfg.SiteID, err)
	}
	if err := json.Unmarshal([]byte(extensionsRaw), &cfg.Extensions); err != nil {
		return model.SitePHPConfig{}, fmt.Errorf("decode site %q php extensions: %w", cfg.SiteID, err)
	}
	if cfg.Ini == nil {
		cfg.Ini = map[string]string{}
	}
	if cfg.Extensions == nil {
		cfg.Extensions = map[string]bool{}
	}
	cfg.AppliedAt = time.Unix(appliedAt, 0).UTC()
	return cfg, nil
}

func marshalPHPStringMap(value map[string]string) (string, error) {
	if value == nil {
		value = map[string]string{}
	}
	return marshalJSON(value)
}

func marshalPHPBoolMap(value map[string]bool) (string, error) {
	if value == nil {
		value = map[string]bool{}
	}
	return marshalJSON(value)
}

// SetSetting writes a key/value setting, replacing an existing value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// GetSetting returns the value for key. It returns ErrNotFound when the key is
// absent; an existing empty value is returned as an empty string.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return value, nil
}

// DeleteSetting removes a setting. Deleting an absent key is a no-op.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	return nil
}

// AddEvent appends a diagnostic event. A zero CreatedAt is replaced with the
// current UTC time. The generated event id is not written back to e.
func (s *Store) AddEvent(ctx context.Context, e model.Event) error {
	createdAt := e.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events (site_id, type, message, created_at)
		VALUES (?, ?, ?, ?)`, e.SiteID, e.Type, e.Message, createdAt.Unix())
	if err != nil {
		return fmt.Errorf("add event %q: %w", e.Type, err)
	}
	// The event feed is bounded to the most recent 500 rows by the spec.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id NOT IN (
		SELECT id FROM events ORDER BY created_at DESC, id DESC LIMIT 500)`); err != nil {
		return fmt.Errorf("prune events: %w", err)
	}
	return nil
}

// ListEvents returns the most recent events first. A limit of zero or less
// returns all events.
func (s *Store) ListEvents(ctx context.Context, limit int) ([]model.Event, error) {
	query := `SELECT id, site_id, type, message, created_at FROM events
		ORDER BY created_at DESC, id DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	events := []model.Event{}
	for rows.Next() {
		var (
			event     model.Event
			createdAt int64
		)
		if err := rows.Scan(&event.ID, &event.SiteID, &event.Type, &event.Message, &createdAt); err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		event.CreatedAt = time.Unix(createdAt, 0).UTC()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

// siteInsertArgs encodes a site into the column order used by CreateSite.
func siteInsertArgs(site model.Site) ([]any, error) {
	phpExtensions, phpIni, env, err := marshalSiteFields(site)
	if err != nil {
		return nil, err
	}
	fcFile := site.FrontControllerFile
	if fcFile == "" {
		fcFile = "index.php"
	}
	fcMode := site.FrontControllerMode
	if fcMode == "" {
		fcMode = "auto"
	}
	override := site.ApacheAllowOverride
	if override == "" {
		override = "all"
	}
	spaFile := site.SPAFallbackFile
	if spaFile == "" {
		spaFile = "index.html"
	}
	buildJSON := "{}"
	if site.Build != nil {
		b, err := json.Marshal(site.Build)
		if err != nil {
			return nil, fmt.Errorf("encode site %q build: %w", site.ID, err)
		}
		buildJSON = string(b)
	}
	return []any{
		site.ID, site.Name, site.Domain, boolInt(site.WWW), string(site.Backend),
		site.HostPath, site.BackendPath, string(site.Kind), site.PHPVersion,
		phpExtensions, phpIni, site.PHPBuild, site.PHPIniRaw, site.PHPConfigHash,
		site.NodeVersion, site.Command, env,
		site.DocumentRoot, boolInt(site.FrontController), boolInt(site.SPAFallback),
		boolInt(site.TLS), boolInt(site.ForceHTTPS), string(site.DesiredState),
		string(site.Status), site.HTTPPort, site.FastCGIPort,
		site.CreatedAt.Unix(), site.UpdatedAt.Unix(),
		fcFile, fcMode, override, spaFile, buildJSON,
	}, nil
}

// marshalSiteFields encodes the site's JSON columns.
func marshalSiteFields(site model.Site) (phpExtensions, phpIni, env string, err error) {
	if phpExtensions, err = marshalJSON(site.PHPExtensions); err != nil {
		return "", "", "", fmt.Errorf("encode site %q php extensions: %w", site.ID, err)
	}
	if phpIni, err = marshalJSON(site.PHPIni); err != nil {
		return "", "", "", fmt.Errorf("encode site %q php ini: %w", site.ID, err)
	}
	if env, err = marshalJSON(site.Env); err != nil {
		return "", "", "", fmt.Errorf("encode site %q env: %w", site.ID, err)
	}
	return phpExtensions, phpIni, env, nil
}

func marshalJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSite decodes one site row. label/value identify the row in error
// messages; both are empty for list scans.
func scanSite(scanner rowScanner, label, value string) (model.Site, error) {
	var (
		site                              model.Site
		www, frontController, spaFallback int
		tlsEnabled, forceHTTPS            int
		phpExtensions, phpIni, env        string
		createdAt, updatedAt              int64
		buildJSON                         string
	)
	err := scanner.Scan(
		&site.ID, &site.Name, &site.Domain, &www, &site.Backend, &site.HostPath,
		&site.BackendPath, &site.Kind, &site.PHPVersion, &phpExtensions, &phpIni,
		&site.PHPBuild, &site.PHPIniRaw, &site.PHPConfigHash,
		&site.NodeVersion, &site.Command, &env, &site.DocumentRoot,
		&frontController, &spaFallback, &tlsEnabled, &forceHTTPS,
		&site.DesiredState, &site.Status, &site.HTTPPort, &site.FastCGIPort,
		&createdAt, &updatedAt,
		&site.FrontControllerFile, &site.FrontControllerMode,
		&site.ApacheAllowOverride, &site.SPAFallbackFile, &buildJSON,
	)
	if err != nil {
		if label != "" && errors.Is(err, sql.ErrNoRows) {
			return model.Site{}, ErrNotFound
		}
		if label != "" {
			return model.Site{}, fmt.Errorf("get site by %s %q: %w", label, value, err)
		}
		return model.Site{}, err
	}

	site.WWW = www != 0
	site.FrontController = frontController != 0
	site.SPAFallback = spaFallback != 0
	site.TLS = tlsEnabled != 0
	site.ForceHTTPS = forceHTTPS != 0
	site.CreatedAt = time.Unix(createdAt, 0).UTC()
	site.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	if site.FrontControllerFile == "" {
		site.FrontControllerFile = "index.php"
	}
	if site.FrontControllerMode == "" {
		site.FrontControllerMode = "auto"
	}
	if site.ApacheAllowOverride == "" {
		site.ApacheAllowOverride = "all"
	}
	if site.SPAFallbackFile == "" {
		site.SPAFallbackFile = "index.html"
	}

	if err := json.Unmarshal([]byte(phpExtensions), &site.PHPExtensions); err != nil {
		return model.Site{}, fmt.Errorf("decode site %q php extensions: %w", site.ID, err)
	}
	if err := json.Unmarshal([]byte(phpIni), &site.PHPIni); err != nil {
		return model.Site{}, fmt.Errorf("decode site %q php ini: %w", site.ID, err)
	}
	if err := json.Unmarshal([]byte(env), &site.Env); err != nil {
		return model.Site{}, fmt.Errorf("decode site %q env: %w", site.ID, err)
	}
	if buildJSON != "" && buildJSON != "{}" && buildJSON != "null" {
		var profile model.BuildProfile
		if err := json.Unmarshal([]byte(buildJSON), &profile); err != nil {
			return model.Site{}, fmt.Errorf("decode site %q build: %w", site.ID, err)
		}
		if profile.NodeVersion != "" || profile.Command != "" || profile.Output != "" || len(profile.Env) > 0 {
			site.Build = &profile
		}
	}
	return site, nil
}

func scanRuntime(scanner rowScanner) (model.RuntimeBuild, error) {
	var (
		build       model.RuntimeBuild
		installedAt int64
	)
	err := scanner.Scan(&build.ID, &build.Kind, &build.Version, &build.Backend,
		&build.Path, &build.Checksum, &installedAt)
	if err != nil {
		return model.RuntimeBuild{}, err
	}
	build.InstalledAt = time.Unix(installedAt, 0).UTC()
	return build, nil
}

func scanCert(scanner rowScanner) (model.CertRecord, error) {
	var (
		cert     model.CertRecord
		domains  string
		notAfter int64
	)
	err := scanner.Scan(&cert.SiteID, &cert.Serial, &domains, &notAfter,
		&cert.KeyPath, &cert.CertPath)
	if err != nil {
		return model.CertRecord{}, err
	}
	if err := json.Unmarshal([]byte(domains), &cert.Domains); err != nil {
		return model.CertRecord{}, fmt.Errorf("decode certificate domains for site %q: %w", cert.SiteID, err)
	}
	cert.NotAfter = time.Unix(notAfter, 0).UTC()
	return cert, nil
}

// requireAffected converts a zero-row mutation result into ErrNotFound.
func requireAffected(res sql.Result, kind, id string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count affected %s rows: %w", kind, err)
	}
	if affected == 0 {
		return fmt.Errorf("%s %q: %w", kind, id, ErrNotFound)
	}
	return nil
}

// boolInt encodes a bool as the integer SQLite stores.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
