package helperproto

import (
	"net/url"

	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
)

// Database wire types intentionally alias dbproto so the helper, agent,
// daemon, and clients cannot drift.
type (
	DatabaseListResponse  = dbproto.DatabaseListResponse
	DatabaseResponse      = dbproto.DatabaseResponse
	DatabaseListRequest   = dbproto.DatabaseListRequest
	DatabaseCreateRequest = dbproto.DatabaseCreateRequest
	DatabaseRenameRequest = dbproto.DatabaseRenameRequest
	DatabaseDropRequest   = dbproto.DatabaseDropRequest

	UserListResponse    = dbproto.UserListResponse
	UserResponse        = dbproto.UserResponse
	UserListRequest     = dbproto.UserListRequest
	UserCreateRequest   = dbproto.UserCreateRequest
	UserUpdateRequest   = dbproto.UserUpdateRequest
	UserDeleteRequest   = dbproto.UserDeleteRequest
	UserPasswordRequest = dbproto.UserPasswordRequest
	UserRevealRequest   = dbproto.UserRevealRequest

	BackupListResponse   = dbproto.BackupListResponse
	BackupListRequest    = dbproto.BackupListRequest
	BackupCreateRequest  = dbproto.BackupCreateRequest
	BackupDeleteRequest  = dbproto.BackupDeleteRequest
	BackupRestoreRequest = dbproto.BackupRestoreRequest

	DatabaseImportRequest = dbproto.DatabaseImportRequest
	DatabaseExportRequest = dbproto.DatabaseExportRequest
	JobGetRequest         = dbproto.JobGetRequest
	JobCancelRequest      = dbproto.JobCancelRequest
	JobResponse           = dbproto.JobResponse

	SiteEnvRequest  = dbproto.SiteEnvRequest
	SiteEnvResponse = dbproto.SiteEnvResponse
	SecretResponse  = dbproto.SecretResponse
	DeleteResponse  = dbproto.DeleteResponse
)

// Helper database routes. The {id}, {name}, {userID}, {backupID}, and {jobID}
// placeholders are Go 1.22 ServeMux wildcards.
const (
	PathServiceDatabases      = "/v1/services/{id}/databases"
	PathServiceDatabase       = "/v1/services/{id}/databases/{name}"
	PathServiceUsers          = "/v1/services/{id}/users"
	PathServiceUser           = "/v1/services/{id}/users/{userID}"
	PathServiceUserPassword   = "/v1/services/{id}/users/{userID}/password"
	PathServiceUserReveal     = "/v1/services/{id}/users/{userID}/reveal"
	PathServiceBackups        = "/v1/services/{id}/backups"
	PathServiceDatabaseBackup = "/v1/services/{id}/databases/{name}/backups"
	PathServiceBackup         = "/v1/services/{id}/backups/{backupID}"
	PathServiceBackupDownload = "/v1/services/{id}/backups/{backupID}/download"
	PathServiceBackupRestore  = "/v1/services/{id}/backups/{backupID}/restore"
	PathServiceDatabaseImport = "/v1/services/{id}/databases/{name}/import"
	PathServiceDatabaseExport = "/v1/services/{id}/databases/{name}/export"
	PathServiceJob            = "/v1/services/{id}/jobs/{jobID}"
	PathServiceDatabaseEnv    = "/v1/services/{id}/database-env"
	PathServicePassword       = "/v1/services/{id}/password"
	PathServiceRedisACLFile   = "/v1/services/{id}/redis/acl-file"
)

// RedisACLFileRequest asks the helper to migrate Redis to ACL-file
// persistence. Confirm must be true when the migration is needed.
type RedisACLFileRequest struct {
	Confirm bool `json:"confirm"`
}

// RedisACLFileResponse reports that Redis now persists ACL changes in its ACL
// file.
type RedisACLFileResponse struct {
	Enabled bool `json:"enabled"`
}

// Additional stable error codes for database operations. They extend the
// generic helper codes without changing existing responses.
const (
	CodeDatabaseInvalidRequest = "DATABASE_INVALID_REQUEST"
	CodeNotFound               = "NOT_FOUND"
	CodeConflict               = "CONFLICT"
	CodeProtected              = "PROTECTED"
	CodeUnsupported            = "UNSUPPORTED"
	CodeServiceInactive        = "SERVICE_INACTIVE"
	CodePasswordMissing        = "PASSWORD_MISSING"
	CodeJobNotFound            = "JOB_NOT_FOUND"
	CodeConfirmationRequired   = "CONFIRMATION_REQUIRED"
)

// ServiceBackups returns the helper path listing every backup of a service.
// dbproto has per-database backup paths but no service-wide list path.
func ServiceBackups(id string) string {
	return "/v1/services/" + url.PathEscape(id) + "/backups"
}

// ServicePassword returns the Redis default-user password path.
func ServicePassword(id string) string {
	return "/v1/services/" + url.PathEscape(id) + "/password"
}

// ServiceRedisACLFile returns the Redis ACL-file migration path.
func ServiceRedisACLFile(id string) string {
	return "/v1/services/" + url.PathEscape(id) + "/redis/acl-file"
}
