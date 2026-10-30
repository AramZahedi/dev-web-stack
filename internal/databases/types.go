package databases

import "github.com/AramZahedi/dev-web-stack/internal/model"

// CreateDatabaseRequest creates a database on a service.
type CreateDatabaseRequest struct {
	Name      string `json:"name"`
	Charset   string `json:"charset,omitempty"`
	Collation string `json:"collation,omitempty"`
}

// RenameDatabaseRequest renames a PostgreSQL database after typed confirmation.
type RenameDatabaseRequest struct {
	Name    string `json:"name"`
	Confirm string `json:"confirm"`
}

// DropDatabaseRequest drops a database after explicit confirmation.
type DropDatabaseRequest struct {
	Confirm     string `json:"confirm"`
	BackupFirst bool   `json:"backup_first,omitempty"`
}

// CreateUserRequest creates an engine user.
type CreateUserRequest struct {
	Username    string                `json:"username"`
	Host        string                `json:"host,omitempty"`
	Grants      []model.DatabaseGrant `json:"grants,omitempty"`
	AccessLevel string                `json:"access_level,omitempty"`
}

// UpdateUserRequest updates enabled state, grants, or Redis access level.
type UpdateUserRequest struct {
	Enabled     *bool                 `json:"enabled,omitempty"`
	Grants      []model.DatabaseGrant `json:"grants,omitempty"`
	AccessLevel string                `json:"access_level,omitempty"`
}

// DeleteUserRequest deletes a user after explicit confirmation.
type DeleteUserRequest struct {
	Confirm       string `json:"confirm"`
	ReassignOwned bool   `json:"reassign_owned,omitempty"`
}

// CreateBackupRequest starts a logical backup.
type CreateBackupRequest struct {
	Kind model.DatabaseBackupKind `json:"kind,omitempty"`
}

// RestoreBackupRequest restores a backup to a target database.
type RestoreBackupRequest struct {
	Target           string `json:"target,omitempty"`
	Overwrite        bool   `json:"overwrite,omitempty"`
	Confirm          string `json:"confirm,omitempty"`
	PreRestoreBackup bool   `json:"pre_restore_backup,omitempty"`
}

// ImportMode selects merge or replace behavior for SQL imports.
type ImportMode string

const (
	ImportMerge   ImportMode = "merge"
	ImportReplace ImportMode = "replace"
)

// ImportRequest imports a SQL dump into an existing database.
type ImportRequest struct {
	Mode            ImportMode `json:"mode,omitempty"`
	Confirm         string     `json:"confirm,omitempty"`
	PreImportBackup bool       `json:"pre_import_backup,omitempty"`
}

// LinkDatabaseRequest links an existing site to a database and user.
type LinkDatabaseRequest struct {
	ServiceID string `json:"service_id"`
	Database  string `json:"database"`
	UserID    string `json:"user_id"`
	Password  string `json:"password,omitempty"`
	Primary   bool   `json:"primary,omitempty"`
}

// SiteDatabaseRequest creates or links a database during Add Site.
type SiteDatabaseRequest struct {
	ServiceID   string                    `json:"service_id"`
	Database    string                    `json:"database,omitempty"`
	NewDatabase *CreateDatabaseRequest    `json:"new_database,omitempty"`
	UserID      string                    `json:"user_id,omitempty"`
	NewUser     *CreateUserRequest        `json:"new_user,omitempty"`
	Password    string                    `json:"password,omitempty"`
	Grant       model.DatabaseGrantPreset `json:"grant,omitempty"`
	Primary     bool                      `json:"primary,omitempty"`
}

// ProvisionedDatabase is a database/user pair created before a site row exists.
type ProvisionedDatabase struct {
	Database model.Database      `json:"database"`
	User     model.DatabaseUser  `json:"user"`
	Warning  string              `json:"warning,omitempty"`
	Request  SiteDatabaseRequest `json:"request"`
}

// SiteDatabaseResult is the link plus the database/user it points at.
type SiteDatabaseResult struct {
	Link     model.DatabaseLink `json:"link"`
	Database model.Database     `json:"database"`
	User     model.DatabaseUser `json:"user"`
	Warning  string             `json:"warning,omitempty"`
}

// Secret is a one-time password response.
type Secret struct {
	Value string `json:"value"`
}
