package dbproto

import "github.com/AramZahedi/dev-web-stack/internal/model"

// Wire views intentionally reuse the shared model types so the agent, helper,
// daemon, client, and UI cannot drift.
type (
	DatabaseInfo   = model.Database
	UserInfo       = model.DatabaseUser
	GrantInfo      = model.DatabaseGrant
	BackupInfo     = model.DatabaseBackup
	JobInfo        = model.DatabaseJob
	LinkInfo       = model.DatabaseLink
	ConnectionInfo = model.DatabaseConnection
)

// SecretValue is the only wire type that carries a password.
type SecretValue struct {
	Value string `json:"value"`
}

type DatabaseListRequest struct {
	ID string `json:"id"`
}

type DatabaseCreateRequest struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
}

type DatabaseRenameRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	NewName string `json:"new_name"`
}

type DatabaseDropRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Confirm string `json:"confirm"`
}

type UserListRequest struct {
	ID string `json:"id"`
}

type UserCreateRequest struct {
	ID          string      `json:"id"`
	Username    string      `json:"username"`
	Host        string      `json:"host,omitempty"`
	Grants      []GrantInfo `json:"grants,omitempty"`
	AccessLevel string      `json:"access_level,omitempty"`
}

type UserUpdateRequest struct {
	ID          string      `json:"id"`
	UserID      string      `json:"user_id"`
	Enabled     *bool       `json:"enabled,omitempty"`
	Grants      []GrantInfo `json:"grants,omitempty"`
	AccessLevel string      `json:"access_level,omitempty"`
}

type UserDeleteRequest struct {
	ID            string `json:"id"`
	UserID        string `json:"user_id"`
	Confirm       string `json:"confirm"`
	ReassignOwned bool   `json:"reassign_owned,omitempty"`
}

type UserPasswordRequest struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
}

type UserRevealRequest struct {
	ID     string `json:"id"`
	UserID string `json:"user_id"`
}

type BackupListRequest struct {
	ID string `json:"id"`
}

type BackupCreateRequest struct {
	ID       string `json:"id"`
	Database string `json:"database"`
	Kind     string `json:"kind,omitempty"`
}

type BackupDeleteRequest struct {
	ID       string `json:"id"`
	BackupID string `json:"backup_id"`
}

type BackupRestoreRequest struct {
	ID               string `json:"id"`
	BackupID         string `json:"backup_id"`
	Target           string `json:"target,omitempty"`
	Overwrite        bool   `json:"overwrite,omitempty"`
	Confirm          string `json:"confirm,omitempty"`
	PreRestoreBackup bool   `json:"pre_restore_backup,omitempty"`
}

type DatabaseImportRequest struct {
	ID              string `json:"id"`
	Database        string `json:"database"`
	Mode            string `json:"mode,omitempty"`
	Confirm         string `json:"confirm,omitempty"`
	PreImportBackup bool   `json:"pre_import_backup,omitempty"`
}

type DatabaseExportRequest struct {
	ID       string `json:"id"`
	Database string `json:"database"`
}

type JobGetRequest struct {
	JobID string `json:"job_id"`
}

type JobCancelRequest struct {
	JobID string `json:"job_id"`
}

type SiteEnvRequest struct {
	ServiceID string     `json:"service_id"`
	Links     []LinkInfo `json:"links"`
}

type DatabaseListResponse struct {
	Databases []DatabaseInfo `json:"databases"`
}

type DatabaseResponse struct {
	Database DatabaseInfo `json:"database"`
}

type UserListResponse struct {
	Users []UserInfo `json:"users"`
}

type UserResponse struct {
	User UserInfo `json:"user"`
}

type SecretResponse struct {
	Value string `json:"value"`
}

type BackupListResponse struct {
	Backups []BackupInfo `json:"backups"`
}

type JobResponse struct {
	Job JobInfo `json:"job"`
}

type LinkListResponse struct {
	Links []LinkInfo `json:"links"`
}

type LinkResponse struct {
	Link LinkInfo `json:"link"`
}

type SiteEnvResponse struct {
	Env map[string]string `json:"env"`
}

type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}

const (
	QueryMode            = "mode"
	QueryConfirm         = "confirm"
	QueryPreImportBackup = "pre_import_backup"
	QueryTarget          = "target"
	QueryOverwrite       = "overwrite"
	QueryPreRestore      = "pre_restore_backup"
)
