package databases

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

// systemDatabases are engine objects devstack never creates, drops, or renames.
var systemDatabases = map[string]struct{}{
	"mysql":              {},
	"information_schema": {},
	"performance_schema": {},
	"sys":                {},
	"postgres":           {},
	"template0":          {},
	"template1":          {},
	"devstack":           {},
}

// systemUsers are engine accounts devstack never deletes or downgrades.
var systemUsers = map[string]struct{}{
	"root":             {},
	"postgres":         {},
	"mysql.sys":        {},
	"mysql.session":    {},
	"mysql.infoschema": {},
	"devstack":         {},
	"default":          {},
}

// canonicalServiceID normalizes the PostgreSQL service ID used by the catalog
// and the agent/helper transports.
func canonicalServiceID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "postgres" {
		return string(services.IDPostgreSQL)
	}
	return id
}

// engineForService maps a catalog service ID to its database engine.
func engineForService(serviceID string) model.DatabaseEngine {
	switch canonicalServiceID(serviceID) {
	case string(services.IDMySQL):
		return model.DatabaseMySQL
	case string(services.IDMariaDB):
		return model.DatabaseMariaDB
	case string(services.IDPostgreSQL):
		return model.DatabasePostgreSQL
	case string(services.IDRedis):
		return model.DatabaseRedis
	default:
		return ""
	}
}

// isSQLService reports whether a service supports SQL databases and backups.
func isSQLService(serviceID string) bool {
	switch engineForService(serviceID) {
	case model.DatabaseMySQL, model.DatabaseMariaDB, model.DatabasePostgreSQL:
		return true
	default:
		return false
	}
}

// isRedisService reports whether a service is Redis.
func isRedisService(serviceID string) bool {
	return engineForService(serviceID) == model.DatabaseRedis
}

// defaultServicePort returns the loopback host port for an engine when the
// catalog view does not expose one.
func defaultServicePort(engine model.DatabaseEngine) int {
	switch engine {
	case model.DatabaseMySQL, model.DatabaseMariaDB:
		return 3306
	case model.DatabasePostgreSQL:
		return 5432
	case model.DatabaseRedis:
		return 6379
	default:
		return 0
	}
}

// servicePort selects the loopback host port from a catalog view.
func servicePort(view services.View, engine model.DatabaseEngine) int {
	want := defaultServicePort(engine)
	for _, port := range view.Ports {
		if port.Loopback && port.Host > 0 && (port.Container == want || want == 0) {
			return port.Host
		}
	}
	for _, port := range view.Ports {
		if port.Host > 0 {
			return port.Host
		}
	}
	return want
}

// connectionScheme returns the DATABASE_URL scheme for an engine.
func connectionScheme(engine model.DatabaseEngine) string {
	switch engine {
	case model.DatabaseMySQL, model.DatabaseMariaDB:
		return "mysql"
	case model.DatabasePostgreSQL:
		return "postgresql"
	case model.DatabaseRedis:
		return "redis"
	default:
		return ""
	}
}

// dbConnectionValue returns the Laravel-style DB_CONNECTION value.
func dbConnectionValue(engine model.DatabaseEngine) string {
	switch engine {
	case model.DatabaseMySQL:
		return "mysql"
	case model.DatabaseMariaDB:
		return "mariadb"
	case model.DatabasePostgreSQL:
		return "pgsql"
	case model.DatabaseRedis:
		return "redis"
	default:
		return ""
	}
}

// stableUserID derives an opaque, deterministic user ID from the engine
// identity. The same externally-created user always receives the same ID.
func stableUserID(serviceID, username, host string) string {
	digest := sha256.Sum256([]byte(canonicalServiceID(serviceID) + "\x00" + username + "\x00" + host))
	return "usr_" + hex.EncodeToString(digest[:8])
}

// protectedDatabase reports whether a name is a system database.
func protectedDatabase(name string) bool {
	_, ok := systemDatabases[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// protectedUser reports whether a username is a system or admin account.
func protectedUser(username string) bool {
	_, ok := systemUsers[strings.ToLower(strings.TrimSpace(username))]
	return ok
}

// audit records one durable mutation. It never includes secret values.
func (m *Manager) audit(ctx context.Context, action, serviceID, siteID, objectType, objectName, result string, detail map[string]any) {
	if m.deps.Audit == nil {
		return
	}
	detailJSON := "{}"
	if len(detail) > 0 {
		if raw, err := json.Marshal(detail); err == nil {
			detailJSON = RedactString(string(raw))
		}
	}
	_ = m.deps.Audit.AddAuditEvent(ctx, model.AuditEventRecord{
		Actor:      "daemon",
		ServiceID:  serviceID,
		SiteID:     siteID,
		ObjectType: objectType,
		ObjectName: objectName,
		Action:     action,
		Result:     result,
		DetailJSON: detailJSON,
		CreatedAt:  m.now().Unix(),
	})
}

// emit records one diagnostic/SSE event. It never includes secret values.
func (m *Manager) emit(ctx context.Context, siteID, typ, message string) {
	if m.deps.Events == nil {
		return
	}
	_ = m.deps.Events.AddEvent(ctx, model.Event{
		SiteID:    siteID,
		Type:      typ,
		Message:   RedactString(message),
		CreatedAt: m.now(),
	})
}

// now returns the manager clock in UTC.
func (m *Manager) now() time.Time {
	if m.deps.Now != nil {
		if value := m.deps.Now(); !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Now().UTC()
}

// newID returns a fresh opaque identifier.
func (m *Manager) newID(prefix string) string {
	value := ""
	if m.deps.NewID != nil {
		value = strings.TrimSpace(m.deps.NewID())
	}
	if value == "" {
		var raw [12]byte
		if _, err := rand.Read(raw[:]); err == nil {
			value = hex.EncodeToString(raw[:])
		} else {
			value = fmt.Sprintf("%d", time.Now().UTC().UnixNano())
		}
	}
	if prefix == "" {
		return value
	}
	return prefix + "_" + value
}

// cloneStringMap copies a string map.
func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// recordToJob converts a persisted job into its view.
func recordToJob(rec model.DatabaseJobRecord) model.DatabaseJob {
	return model.DatabaseJob{
		ID:        rec.ID,
		ServiceID: rec.ServiceID,
		Database:  rec.DatabaseName,
		Kind:      model.DatabaseJobKind(rec.Kind),
		Status:    model.DatabaseJobStatus(rec.Status),
		Progress:  rec.Progress,
		Message:   RedactString(rec.Message),
		BackupID:  rec.BackupID,
		CreatedAt: time.Unix(rec.CreatedAt, 0).UTC(),
		UpdatedAt: time.Unix(rec.UpdatedAt, 0).UTC(),
	}
}

// jobToRecord converts a job view into its persisted form.
func jobToRecord(job model.DatabaseJob) model.DatabaseJobRecord {
	return model.DatabaseJobRecord{
		ID:           job.ID,
		ServiceID:    job.ServiceID,
		DatabaseName: job.Database,
		Kind:         string(job.Kind),
		Status:       string(job.Status),
		Progress:     job.Progress,
		Message:      job.Message,
		BackupID:     job.BackupID,
		CreatedAt:    job.CreatedAt.Unix(),
		UpdatedAt:    job.UpdatedAt.Unix(),
	}
}

// backupFromView converts a platform backup view into its persisted form.
func backupFromView(backup model.DatabaseBackup) model.DatabaseBackupRecord {
	createdAt := backup.CreatedAt.Unix()
	if backup.CreatedAt.IsZero() {
		createdAt = 0
	}
	return model.DatabaseBackupRecord{
		ID:           backup.ID,
		ServiceID:    backup.ServiceID,
		Engine:       backup.Engine,
		DatabaseName: backup.Database,
		SizeBytes:    backup.SizeBytes,
		SHA256:       backup.SHA256,
		Kind:         string(backup.Kind),
		Status:       string(backup.Status),
		CreatedAt:    createdAt,
		Error:        RedactString(backup.Error),
	}
}

// backupToView converts a persisted backup into its view.
func backupToView(rec model.DatabaseBackupRecord) model.DatabaseBackup {
	return model.DatabaseBackup{
		ID:        rec.ID,
		ServiceID: rec.ServiceID,
		Engine:    rec.Engine,
		Database:  rec.DatabaseName,
		SizeBytes: rec.SizeBytes,
		SHA256:    rec.SHA256,
		Kind:      model.DatabaseBackupKind(rec.Kind),
		Status:    model.DatabaseBackupStatus(rec.Status),
		CreatedAt: time.Unix(rec.CreatedAt, 0).UTC(),
		Error:     RedactString(rec.Error),
	}
}

// safeErrorMessage returns an error message safe to store in job/audit rows.
// Secret-looking values are redacted by the API layer; job messages contain
// only engine diagnostics.
func safeErrorMessage(err error) string {
	return RedactError(err)
}

// unixTime converts persisted Unix seconds to UTC.
func unixTime(seconds int64) time.Time {
	return time.Unix(seconds, 0).UTC()
}

// decodePrivileges decodes the custom grant JSON. Malformed or empty values
// produce a nil slice rather than failing a list response.
func decodePrivileges(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var privileges []string
	if err := json.Unmarshal([]byte(raw), &privileges); err != nil {
		return nil
	}
	return privileges
}
