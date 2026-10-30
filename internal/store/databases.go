package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// ErrConflict reports a uniqueness or foreign-key conflict. Callers map it to
// the domain-specific conflict sentinel (for example a linked database user).
var ErrConflict = errors.New("conflict")

const databaseLinkColumns = `id, site_id, service_id, database_name, user_id, is_primary, created_at`

const databaseUserColumns = `id, service_id, engine, username, host, managed_by_devstack,
	password_managed, enabled, access_level, created_at, last_password_rotated_at, last_seen_at`

const databaseGrantColumns = `user_id, database_name, preset, custom_json, updated_at`

const databaseBackupColumns = `id, service_id, engine, database_name, path, size_bytes,
	sha256, kind, status, created_at, error`

const databaseJobColumns = `id, service_id, database_name, kind, status, progress,
	message, backup_id, created_at, updated_at`

const auditEventColumns = `id, actor, service_id, site_id, object_type, object_name,
	action, result, detail_json, created_at`

// CreateDatabaseLink inserts a site/database link. The site and user foreign
// keys must already exist.
func (s *Store) CreateDatabaseLink(ctx context.Context, link model.DatabaseLinkRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO database_links (`+databaseLinkColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		link.ID, link.SiteID, link.ServiceID, link.DatabaseName, link.UserID,
		boolInt(link.Primary), link.CreatedAt)
	if err != nil {
		return fmt.Errorf("create database link %q: %w", link.ID, err)
	}
	return nil
}

// GetDatabaseLink returns one link by id.
func (s *Store) GetDatabaseLink(ctx context.Context, id string) (model.DatabaseLinkRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+databaseLinkColumns+`
		FROM database_links WHERE id = ?`, id)
	link, err := scanDatabaseLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DatabaseLinkRecord{}, ErrNotFound
	}
	if err != nil {
		return model.DatabaseLinkRecord{}, fmt.Errorf("get database link %q: %w", id, err)
	}
	return link, nil
}

// ListDatabaseLinksBySite returns every link owned by a site with the site name
// populated for display.
func (s *Store) ListDatabaseLinksBySite(ctx context.Context, siteID string) ([]model.DatabaseLinkRecord, error) {
	return s.listDatabaseLinks(ctx, `WHERE l.site_id = ? ORDER BY l.created_at, l.id`, siteID)
}

// ListDatabaseLinksByService returns every link that points at a service.
func (s *Store) ListDatabaseLinksByService(ctx context.Context, serviceID string) ([]model.DatabaseLinkRecord, error) {
	return s.listDatabaseLinks(ctx, `WHERE l.service_id = ? ORDER BY l.created_at, l.id`, serviceID)
}

func (s *Store) listDatabaseLinks(ctx context.Context, where string, args ...any) ([]model.DatabaseLinkRecord, error) {
	query := `SELECT l.id, l.site_id, l.service_id, l.database_name, l.user_id,
		l.is_primary, l.created_at, COALESCE(s.name, '')
		FROM database_links l LEFT JOIN sites s ON s.id = l.site_id ` + where
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list database links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	links := []model.DatabaseLinkRecord{}
	for rows.Next() {
		link, err := scanDatabaseLinkWithSite(rows)
		if err != nil {
			return nil, fmt.Errorf("list database links: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list database links: %w", err)
	}
	return links, nil
}

// DeleteDatabaseLink removes one link. It returns ErrNotFound when absent.
func (s *Store) DeleteDatabaseLink(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM database_links WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete database link %q: %w", id, err)
	}
	return requireAffected(res, "database link", id)
}

// DeleteDatabaseLinksByService removes every link that points at a service and
// returns the number removed. It is used after an uninstall with delete-data.
func (s *Store) DeleteDatabaseLinksByService(ctx context.Context, serviceID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM database_links WHERE service_id = ?`, serviceID)
	if err != nil {
		return 0, fmt.Errorf("delete database links for service %q: %w", serviceID, err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted database links for service %q: %w", serviceID, err)
	}
	return removed, nil
}

// SetPrimaryDatabaseLink makes linkID the site's primary link. The partial
// unique index requires clearing the previous primary in the same transaction.
func (s *Store) SetPrimaryDatabaseLink(ctx context.Context, siteID, linkID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set primary database link %q: begin transaction: %w", linkID, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE database_links SET is_primary = 0 WHERE site_id = ?`, siteID); err != nil {
		return fmt.Errorf("clear primary database link for site %q: %w", siteID, err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE database_links SET is_primary = 1 WHERE id = ? AND site_id = ?`, linkID, siteID)
	if err != nil {
		return fmt.Errorf("set primary database link %q: %w", linkID, err)
	}
	if err := requireAffected(res, "database link", linkID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set primary database link %q: commit: %w", linkID, err)
	}
	return nil
}

// RenameDatabaseReferences updates site links and persisted grant intent after
// a PostgreSQL rename so existing sites keep their connection.
func (s *Store) RenameDatabaseReferences(ctx context.Context, serviceID, oldName, newName string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rename database references %q: begin transaction: %w", oldName, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE database_links SET database_name = ?
		WHERE service_id = ? AND database_name = ?`, newName, serviceID, oldName); err != nil {
		return fmt.Errorf("rename database links %q: %w", oldName, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE database_user_grants SET database_name = ?
		WHERE database_name = ? AND user_id IN (
			SELECT id FROM database_users WHERE service_id = ?
		)`, newName, oldName, serviceID); err != nil {
		return fmt.Errorf("rename database grants %q: %w", oldName, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rename database references %q: commit: %w", oldName, err)
	}
	return nil
}

// UpsertDatabaseUser inserts or updates a discovered or managed user.
func (s *Store) UpsertDatabaseUser(ctx context.Context, user model.DatabaseUserRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO database_users (`+databaseUserColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			service_id = excluded.service_id,
			engine = excluded.engine,
			username = excluded.username,
			host = excluded.host,
			managed_by_devstack = excluded.managed_by_devstack,
			password_managed = excluded.password_managed,
			enabled = excluded.enabled,
			access_level = excluded.access_level,
			created_at = excluded.created_at,
			last_password_rotated_at = excluded.last_password_rotated_at,
			last_seen_at = excluded.last_seen_at`,
		user.ID, user.ServiceID, string(user.Engine), user.Username, user.Host,
		boolInt(user.ManagedByDevstack), boolInt(user.PasswordManaged), boolInt(user.Enabled),
		user.AccessLevel, user.CreatedAt, user.LastPasswordRotatedAt, user.LastSeenAt)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("upsert database user %q: %w", user.ID, ErrConflict)
		}
		return fmt.Errorf("upsert database user %q: %w", user.ID, err)
	}
	return nil
}

// GetDatabaseUser returns one user by opaque id.
func (s *Store) GetDatabaseUser(ctx context.Context, id string) (model.DatabaseUserRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+databaseUserColumns+`
		FROM database_users WHERE id = ?`, id)
	user, err := scanDatabaseUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DatabaseUserRecord{}, ErrNotFound
	}
	if err != nil {
		return model.DatabaseUserRecord{}, fmt.Errorf("get database user %q: %w", id, err)
	}
	return user, nil
}

// ListDatabaseUsers returns every user for a service ordered by username/host.
func (s *Store) ListDatabaseUsers(ctx context.Context, serviceID string) ([]model.DatabaseUserRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+databaseUserColumns+`
		FROM database_users WHERE service_id = ? ORDER BY username, host, id`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list database users for service %q: %w", serviceID, err)
	}
	defer func() { _ = rows.Close() }()

	users := []model.DatabaseUserRecord{}
	for rows.Next() {
		user, err := scanDatabaseUser(rows)
		if err != nil {
			return nil, fmt.Errorf("list database users for service %q: %w", serviceID, err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list database users for service %q: %w", serviceID, err)
	}
	return users, nil
}

// DeleteDatabaseUser removes a user. A user referenced by a site link cannot be
// deleted; the foreign key restriction is reported as ErrConflict.
func (s *Store) DeleteDatabaseUser(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM database_users WHERE id = ?`, id)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("delete database user %q: %w", id, ErrConflict)
		}
		return fmt.Errorf("delete database user %q: %w", id, err)
	}
	return requireAffected(res, "database user", id)
}

// SetDatabaseUserPasswordState records whether a usable secret exists.
func (s *Store) SetDatabaseUserPasswordState(ctx context.Context, id string, managed bool, rotatedAt int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE database_users
		SET password_managed = ?, last_password_rotated_at = ? WHERE id = ?`,
		boolInt(managed), rotatedAt, id)
	if err != nil {
		return fmt.Errorf("set database user %q password state: %w", id, err)
	}
	return requireAffected(res, "database user", id)
}

// SaveDatabaseGrant upserts the devstack intent for one user/database pair.
func (s *Store) SaveDatabaseGrant(ctx context.Context, grant model.DatabaseGrantRecord) error {
	if grant.CustomJSON == "" {
		grant.CustomJSON = "[]"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO database_user_grants (`+databaseGrantColumns+`)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id, database_name) DO UPDATE SET
			preset = excluded.preset,
			custom_json = excluded.custom_json,
			updated_at = excluded.updated_at`,
		grant.UserID, grant.DatabaseName, grant.Preset, grant.CustomJSON, grant.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save database grant for user %q database %q: %w", grant.UserID, grant.DatabaseName, err)
	}
	return nil
}

// ListDatabaseGrants returns the persisted intent rows for a user.
func (s *Store) ListDatabaseGrants(ctx context.Context, userID string) ([]model.DatabaseGrantRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+databaseGrantColumns+`
		FROM database_user_grants WHERE user_id = ? ORDER BY database_name`, userID)
	if err != nil {
		return nil, fmt.Errorf("list database grants for user %q: %w", userID, err)
	}
	defer func() { _ = rows.Close() }()

	grants := []model.DatabaseGrantRecord{}
	for rows.Next() {
		var grant model.DatabaseGrantRecord
		if err := rows.Scan(&grant.UserID, &grant.DatabaseName, &grant.Preset, &grant.CustomJSON, &grant.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list database grants for user %q: %w", userID, err)
		}
		grants = append(grants, grant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list database grants for user %q: %w", userID, err)
	}
	return grants, nil
}

// DeleteDatabaseGrant removes one persisted intent row.
func (s *Store) DeleteDatabaseGrant(ctx context.Context, userID, databaseName string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM database_user_grants
		WHERE user_id = ? AND database_name = ?`, userID, databaseName)
	if err != nil {
		return fmt.Errorf("delete database grant for user %q database %q: %w", userID, databaseName, err)
	}
	return requireAffected(res, "database grant", userID+"/"+databaseName)
}

// CreateDatabaseBackup records a logical dump.
func (s *Store) CreateDatabaseBackup(ctx context.Context, backup model.DatabaseBackupRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO database_backups (`+databaseBackupColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		backup.ID, backup.ServiceID, string(backup.Engine), backup.DatabaseName,
		backup.Path, backup.SizeBytes, backup.SHA256, backup.Kind, backup.Status,
		backup.CreatedAt, backup.Error)
	if err != nil {
		return fmt.Errorf("create database backup %q: %w", backup.ID, err)
	}
	return nil
}

// GetDatabaseBackup returns one backup by id.
func (s *Store) GetDatabaseBackup(ctx context.Context, id string) (model.DatabaseBackupRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+databaseBackupColumns+`
		FROM database_backups WHERE id = ?`, id)
	backup, err := scanDatabaseBackup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DatabaseBackupRecord{}, ErrNotFound
	}
	if err != nil {
		return model.DatabaseBackupRecord{}, fmt.Errorf("get database backup %q: %w", id, err)
	}
	return backup, nil
}

// ListDatabaseBackups returns every backup for a service, newest first.
func (s *Store) ListDatabaseBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackupRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+databaseBackupColumns+`
		FROM database_backups WHERE service_id = ?
		ORDER BY created_at DESC, id DESC`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list database backups for service %q: %w", serviceID, err)
	}
	defer func() { _ = rows.Close() }()

	backups := []model.DatabaseBackupRecord{}
	for rows.Next() {
		backup, err := scanDatabaseBackup(rows)
		if err != nil {
			return nil, fmt.Errorf("list database backups for service %q: %w", serviceID, err)
		}
		backups = append(backups, backup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list database backups for service %q: %w", serviceID, err)
	}
	return backups, nil
}

// UpdateDatabaseBackup replaces a backup record. It returns ErrNotFound when
// the id does not exist.
func (s *Store) UpdateDatabaseBackup(ctx context.Context, backup model.DatabaseBackupRecord) error {
	res, err := s.db.ExecContext(ctx, `UPDATE database_backups SET
		service_id = ?, engine = ?, database_name = ?, path = ?, size_bytes = ?,
		sha256 = ?, kind = ?, status = ?, created_at = ?, error = ?
		WHERE id = ?`,
		backup.ServiceID, string(backup.Engine), backup.DatabaseName, backup.Path,
		backup.SizeBytes, backup.SHA256, backup.Kind, backup.Status, backup.CreatedAt,
		backup.Error, backup.ID)
	if err != nil {
		return fmt.Errorf("update database backup %q: %w", backup.ID, err)
	}
	return requireAffected(res, "database backup", backup.ID)
}

// DeleteDatabaseBackup removes one backup record.
func (s *Store) DeleteDatabaseBackup(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM database_backups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete database backup %q: %w", id, err)
	}
	return requireAffected(res, "database backup", id)
}

// CreateDatabaseJob records a queued job.
func (s *Store) CreateDatabaseJob(ctx context.Context, job model.DatabaseJobRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO database_jobs (`+databaseJobColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.ServiceID, job.DatabaseName, job.Kind, job.Status,
		job.Progress, job.Message, job.BackupID, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create database job %q: %w", job.ID, err)
	}
	return nil
}

// GetDatabaseJob returns one job by id.
func (s *Store) GetDatabaseJob(ctx context.Context, id string) (model.DatabaseJobRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+databaseJobColumns+`
		FROM database_jobs WHERE id = ?`, id)
	job, err := scanDatabaseJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DatabaseJobRecord{}, ErrNotFound
	}
	if err != nil {
		return model.DatabaseJobRecord{}, fmt.Errorf("get database job %q: %w", id, err)
	}
	return job, nil
}

// UpdateDatabaseJob replaces a job record. It returns ErrNotFound when absent.
func (s *Store) UpdateDatabaseJob(ctx context.Context, job model.DatabaseJobRecord) error {
	res, err := s.db.ExecContext(ctx, `UPDATE database_jobs SET
		service_id = ?, database_name = ?, kind = ?, status = ?, progress = ?,
		message = ?, backup_id = ?, created_at = ?, updated_at = ?
		WHERE id = ?`,
		job.ServiceID, job.DatabaseName, job.Kind, job.Status, job.Progress,
		job.Message, job.BackupID, job.CreatedAt, job.UpdatedAt, job.ID)
	if err != nil {
		return fmt.Errorf("update database job %q: %w", job.ID, err)
	}
	return requireAffected(res, "database job", job.ID)
}

// ListDatabaseJobs returns a service's jobs, newest first.
func (s *Store) ListDatabaseJobs(ctx context.Context, serviceID string) ([]model.DatabaseJobRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+databaseJobColumns+`
		FROM database_jobs WHERE service_id = ?
		ORDER BY created_at DESC, id DESC`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list database jobs for service %q: %w", serviceID, err)
	}
	defer func() { _ = rows.Close() }()

	jobs := []model.DatabaseJobRecord{}
	for rows.Next() {
		job, err := scanDatabaseJob(rows)
		if err != nil {
			return nil, fmt.Errorf("list database jobs for service %q: %w", serviceID, err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list database jobs for service %q: %w", serviceID, err)
	}
	return jobs, nil
}

// AddAuditEvent appends a durable audit row and prunes the table to the newest
// 10,000 rows in the same transaction. A zero CreatedAt is replaced with the
// current UTC time.
func (s *Store) AddAuditEvent(ctx context.Context, event model.AuditEventRecord) error {
	createdAt := event.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().UTC().Unix()
	}
	detail := event.DetailJSON
	if strings.TrimSpace(detail) == "" {
		detail = "{}"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("add audit event %q: begin transaction: %w", event.Action, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events
		(actor, service_id, site_id, object_type, object_name, action, result, detail_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.Actor, event.ServiceID, event.SiteID, event.ObjectType, event.ObjectName,
		event.Action, event.Result, detail, createdAt); err != nil {
		return fmt.Errorf("add audit event %q: %w", event.Action, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE id NOT IN (
		SELECT id FROM audit_events ORDER BY created_at DESC, id DESC LIMIT 10000)`); err != nil {
		return fmt.Errorf("prune audit events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("add audit event %q: commit: %w", event.Action, err)
	}
	return nil
}

// ListAuditEvents returns audit rows newest first. A limit of zero or less
// returns every row.
func (s *Store) ListAuditEvents(ctx context.Context, limit int) ([]model.AuditEventRecord, error) {
	query := `SELECT ` + auditEventColumns + ` FROM audit_events
		ORDER BY created_at DESC, id DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := []model.AuditEventRecord{}
	for rows.Next() {
		var event model.AuditEventRecord
		if err := rows.Scan(&event.ID, &event.Actor, &event.ServiceID, &event.SiteID,
			&event.ObjectType, &event.ObjectName, &event.Action, &event.Result,
			&event.DetailJSON, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("list audit events: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	return events, nil
}

// CountAuditEvents returns the number of retained audit rows.
func (s *Store) CountAuditEvents(ctx context.Context) (int64, error) {
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count audit events: %w", err)
	}
	return count, nil
}

func scanDatabaseLink(scanner rowScanner) (model.DatabaseLinkRecord, error) {
	var (
		link      model.DatabaseLinkRecord
		primary   int
		createdAt int64
	)
	if err := scanner.Scan(&link.ID, &link.SiteID, &link.ServiceID, &link.DatabaseName,
		&link.UserID, &primary, &createdAt); err != nil {
		return model.DatabaseLinkRecord{}, err
	}
	link.Primary = primary != 0
	link.CreatedAt = createdAt
	return link, nil
}

func scanDatabaseLinkWithSite(scanner rowScanner) (model.DatabaseLinkRecord, error) {
	var (
		link      model.DatabaseLinkRecord
		primary   int
		createdAt int64
	)
	if err := scanner.Scan(&link.ID, &link.SiteID, &link.ServiceID, &link.DatabaseName,
		&link.UserID, &primary, &createdAt, &link.SiteName); err != nil {
		return model.DatabaseLinkRecord{}, err
	}
	link.Primary = primary != 0
	link.CreatedAt = createdAt
	return link, nil
}

func scanDatabaseUser(scanner rowScanner) (model.DatabaseUserRecord, error) {
	var (
		user                  model.DatabaseUserRecord
		engine                string
		managed, password, on int
	)
	if err := scanner.Scan(&user.ID, &user.ServiceID, &engine, &user.Username, &user.Host,
		&managed, &password, &on, &user.AccessLevel, &user.CreatedAt,
		&user.LastPasswordRotatedAt, &user.LastSeenAt); err != nil {
		return model.DatabaseUserRecord{}, err
	}
	user.Engine = model.DatabaseEngine(engine)
	user.ManagedByDevstack = managed != 0
	user.PasswordManaged = password != 0
	user.Enabled = on != 0
	return user, nil
}

func scanDatabaseBackup(scanner rowScanner) (model.DatabaseBackupRecord, error) {
	var (
		backup model.DatabaseBackupRecord
		engine string
	)
	if err := scanner.Scan(&backup.ID, &backup.ServiceID, &engine, &backup.DatabaseName,
		&backup.Path, &backup.SizeBytes, &backup.SHA256, &backup.Kind, &backup.Status,
		&backup.CreatedAt, &backup.Error); err != nil {
		return model.DatabaseBackupRecord{}, err
	}
	backup.Engine = model.DatabaseEngine(engine)
	return backup, nil
}

func scanDatabaseJob(scanner rowScanner) (model.DatabaseJobRecord, error) {
	var job model.DatabaseJobRecord
	if err := scanner.Scan(&job.ID, &job.ServiceID, &job.DatabaseName, &job.Kind,
		&job.Status, &job.Progress, &job.Message, &job.BackupID,
		&job.CreatedAt, &job.UpdatedAt); err != nil {
		return model.DatabaseJobRecord{}, err
	}
	return job, nil
}

// isConstraintError reports a SQLite uniqueness or foreign-key failure.
func isConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "constraint failed") ||
		strings.Contains(message, "foreign key") ||
		strings.Contains(message, "unique constraint")
}
