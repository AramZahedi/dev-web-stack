package model

import "time"

// DatabaseEngine identifies the engine that owns a database or user.
type DatabaseEngine string

const (
	DatabaseMySQL      DatabaseEngine = "mysql"
	DatabaseMariaDB    DatabaseEngine = "mariadb"
	DatabasePostgreSQL DatabaseEngine = "postgres"
	DatabaseRedis      DatabaseEngine = "redis"
)

// DatabaseGrantPreset is a database-scoped privilege preset.
type DatabaseGrantPreset string

const (
	GrantReadOnly        DatabaseGrantPreset = "read_only"
	GrantReadWrite       DatabaseGrantPreset = "read_write"
	GrantReadWriteSchema DatabaseGrantPreset = "read_write_schema"
	GrantAdmin           DatabaseGrantPreset = "admin"
	GrantCustom          DatabaseGrantPreset = "custom"
)

// DatabaseSiteRef is a compact site reference embedded in database views.
type DatabaseSiteRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Database is a service-wide database inventory record.
type Database struct {
	Name        string            `json:"name"`
	ServiceID   string            `json:"service_id"`
	Engine      DatabaseEngine    `json:"engine"`
	SizeBytes   int64             `json:"size_bytes"`
	TableCount  int               `json:"table_count"`
	CreatedAt   time.Time         `json:"created_at,omitempty"`
	LinkedSites []DatabaseSiteRef `json:"linked_sites,omitempty"`
	Managed     bool              `json:"managed"`
	Missing     bool              `json:"missing,omitempty"`
}

// DatabaseGrant is one database-scoped grant summary.
type DatabaseGrant struct {
	Database   string              `json:"database"`
	Preset     DatabaseGrantPreset `json:"preset"`
	Privileges []string            `json:"privileges,omitempty"`
}

// DatabaseUser is a service-wide engine user.
type DatabaseUser struct {
	ID                    string            `json:"id"`
	ServiceID             string            `json:"service_id"`
	Engine                DatabaseEngine    `json:"engine"`
	Username              string            `json:"username"`
	Host                  string            `json:"host,omitempty"`
	Enabled               bool              `json:"enabled"`
	Managed               bool              `json:"managed"`
	PasswordManaged       bool              `json:"password_managed"`
	AccessLevel           string            `json:"access_level,omitempty"`
	Grants                []DatabaseGrant   `json:"grants,omitempty"`
	LinkedSites           []DatabaseSiteRef `json:"linked_sites,omitempty"`
	CreatedAt             time.Time         `json:"created_at,omitempty"`
	LastPasswordRotatedAt *time.Time        `json:"last_password_rotated_at,omitempty"`
}

// DatabaseConnection is the client-facing connection detail for a link.
type DatabaseConnection struct {
	Engine          DatabaseEngine    `json:"engine"`
	Host            string            `json:"host"`
	Port            int               `json:"port"`
	Database        string            `json:"database"`
	Username        string            `json:"username"`
	PasswordManaged bool              `json:"password_managed"`
	URL             string            `json:"url,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
}

// DatabaseLink attaches a site to a service-wide database and user.
type DatabaseLink struct {
	ID              string             `json:"id"`
	SiteID          string             `json:"site_id"`
	SiteName        string             `json:"site_name,omitempty"`
	ServiceID       string             `json:"service_id"`
	Engine          DatabaseEngine     `json:"engine"`
	Database        string             `json:"database"`
	UserID          string             `json:"user_id"`
	Username        string             `json:"username"`
	Host            string             `json:"host,omitempty"`
	Primary         bool               `json:"primary"`
	PasswordManaged bool               `json:"password_managed"`
	Available       bool               `json:"available"`
	Error           string             `json:"error,omitempty"`
	Connection      DatabaseConnection `json:"connection"`
}

// DatabaseBackupKind describes why a backup was created.
type DatabaseBackupKind string

const (
	BackupManual     DatabaseBackupKind = "manual"
	BackupPreDrop    DatabaseBackupKind = "pre_drop"
	BackupPreRestore DatabaseBackupKind = "pre_restore"
	BackupPreImport  DatabaseBackupKind = "pre_import"
)

// DatabaseBackupStatus is the readiness of a backup file.
type DatabaseBackupStatus string

const (
	BackupReady   DatabaseBackupStatus = "ready"
	BackupRunning DatabaseBackupStatus = "running"
	BackupFailed  DatabaseBackupStatus = "failed"
)

// DatabaseBackup is a logical SQL dump record.
type DatabaseBackup struct {
	ID        string               `json:"id"`
	ServiceID string               `json:"service_id"`
	Engine    DatabaseEngine       `json:"engine"`
	Database  string               `json:"database"`
	SizeBytes int64                `json:"size_bytes"`
	SHA256    string               `json:"sha256"`
	Kind      DatabaseBackupKind   `json:"kind"`
	Status    DatabaseBackupStatus `json:"status"`
	CreatedAt time.Time            `json:"created_at"`
	Error     string               `json:"error,omitempty"`
}

// DatabaseJobKind is the operation a database job performs.
type DatabaseJobKind string

const (
	JobBackup  DatabaseJobKind = "backup"
	JobRestore DatabaseJobKind = "restore"
	JobImport  DatabaseJobKind = "import"
)

// DatabaseJobStatus is the lifecycle state of a database job.
type DatabaseJobStatus string

const (
	JobQueued    DatabaseJobStatus = "queued"
	JobRunning   DatabaseJobStatus = "running"
	JobSucceeded DatabaseJobStatus = "succeeded"
	JobFailed    DatabaseJobStatus = "failed"
	JobCanceled  DatabaseJobStatus = "canceled"
)

// DatabaseJob tracks a long-running backup, restore, or import.
type DatabaseJob struct {
	ID        string            `json:"id"`
	ServiceID string            `json:"service_id"`
	Database  string            `json:"database"`
	Kind      DatabaseJobKind   `json:"kind"`
	Status    DatabaseJobStatus `json:"status"`
	Progress  int               `json:"progress"`
	Message   string            `json:"message,omitempty"`
	BackupID  string            `json:"backup_id,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// AuditEvent is a durable, structured security/audit record.
type AuditEvent struct {
	ID         int64     `json:"id"`
	Actor      string    `json:"actor,omitempty"`
	ServiceID  string    `json:"service_id,omitempty"`
	SiteID     string    `json:"site_id,omitempty"`
	ObjectType string    `json:"object_type,omitempty"`
	ObjectName string    `json:"object_name,omitempty"`
	Action     string    `json:"action"`
	Result     string    `json:"result"`
	DetailJSON string    `json:"detail_json,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// DatabaseUserRecord is the persisted form of a database user.
type DatabaseUserRecord struct {
	ID                    string         `json:"id"`
	ServiceID             string         `json:"service_id"`
	Engine                DatabaseEngine `json:"engine"`
	Username              string         `json:"username"`
	Host                  string         `json:"host,omitempty"`
	ManagedByDevstack     bool           `json:"managed_by_devstack"`
	PasswordManaged       bool           `json:"password_managed"`
	Enabled               bool           `json:"enabled"`
	AccessLevel           string         `json:"access_level,omitempty"`
	CreatedAt             int64          `json:"created_at"`
	LastPasswordRotatedAt int64          `json:"last_password_rotated_at,omitempty"`
	LastSeenAt            int64          `json:"last_seen_at,omitempty"`
}

// DatabaseLinkRecord is the persisted form of a site/database link.
type DatabaseLinkRecord struct {
	ID           string `json:"id"`
	SiteID       string `json:"site_id"`
	SiteName     string `json:"site_name,omitempty"`
	ServiceID    string `json:"service_id"`
	DatabaseName string `json:"database_name"`
	UserID       string `json:"user_id"`
	Primary      bool   `json:"is_primary"`
	CreatedAt    int64  `json:"created_at"`
}

// DatabaseGrantRecord is the persisted intent for one user/database grant.
type DatabaseGrantRecord struct {
	UserID       string `json:"user_id"`
	DatabaseName string `json:"database_name"`
	Preset       string `json:"preset"`
	CustomJSON   string `json:"custom_json"`
	UpdatedAt    int64  `json:"updated_at"`
}

// DatabaseBackupRecord is the persisted form of a backup.
type DatabaseBackupRecord struct {
	ID           string         `json:"id"`
	ServiceID    string         `json:"service_id"`
	Engine       DatabaseEngine `json:"engine"`
	DatabaseName string         `json:"database_name"`
	Path         string         `json:"path"`
	SizeBytes    int64          `json:"size_bytes"`
	SHA256       string         `json:"sha256"`
	Kind         string         `json:"kind"`
	Status       string         `json:"status"`
	CreatedAt    int64          `json:"created_at"`
	Error        string         `json:"error,omitempty"`
}

// DatabaseJobRecord is the persisted form of a job.
type DatabaseJobRecord struct {
	ID           string `json:"id"`
	ServiceID    string `json:"service_id"`
	DatabaseName string `json:"database_name"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	Progress     int    `json:"progress"`
	Message      string `json:"message,omitempty"`
	BackupID     string `json:"backup_id,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// AuditEventRecord is the persisted form of an audit event.
type AuditEventRecord struct {
	ID         int64  `json:"id"`
	Actor      string `json:"actor,omitempty"`
	ServiceID  string `json:"service_id,omitempty"`
	SiteID     string `json:"site_id,omitempty"`
	ObjectType string `json:"object_type,omitempty"`
	ObjectName string `json:"object_name,omitempty"`
	Action     string `json:"action"`
	Result     string `json:"result"`
	DetailJSON string `json:"detail_json,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}
