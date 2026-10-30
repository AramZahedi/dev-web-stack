package databases

import (
	"context"
	"errors"
	"io"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

var (
	ErrNotFound        = errors.New("database: not found")
	ErrConflict        = errors.New("database: conflict")
	ErrInvalidRequest  = errors.New("database: invalid request")
	ErrProtected       = errors.New("database: protected object")
	ErrUnsupported     = errors.New("database: unsupported operation")
	ErrServiceInactive = errors.New("database: service is not active")
	ErrLinkedSites     = errors.New("database: user is linked to sites")
	ErrPasswordMissing = errors.New("database: password is not managed")
	ErrJobNotFound     = errors.New("database: job not found")
)

// Platform is the privileged engine surface implemented by the Windows agent
// adapter and the Linux helper adapter.
type Platform interface {
	ListDatabases(ctx context.Context, serviceID string) ([]model.Database, error)
	CreateDatabase(ctx context.Context, serviceID string, req CreateDatabaseRequest) (model.Database, error)
	RenameDatabase(ctx context.Context, serviceID, name string, req RenameDatabaseRequest) (model.Database, error)
	DropDatabase(ctx context.Context, serviceID, name string, req DropDatabaseRequest) error
	ListUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error)
	CreateUser(ctx context.Context, serviceID string, req CreateUserRequest) (model.DatabaseUser, error)
	UpdateUser(ctx context.Context, serviceID, userID string, req UpdateUserRequest) (model.DatabaseUser, error)
	DeleteUser(ctx context.Context, serviceID, userID string, req DeleteUserRequest) error
	SetUserPassword(ctx context.Context, serviceID, userID string) (Secret, error)
	RevealUserPassword(ctx context.Context, serviceID, userID string) (Secret, error)
	ListBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackup, error)
	CreateBackup(ctx context.Context, serviceID, database string, req CreateBackupRequest) (model.DatabaseJob, error)
	DeleteBackup(ctx context.Context, serviceID, backupID string) error
	RestoreBackup(ctx context.Context, serviceID, backupID string, req RestoreBackupRequest) (model.DatabaseJob, error)
	ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req ImportRequest) (model.DatabaseJob, error)
	ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error)
	DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error)
	GetJob(ctx context.Context, jobID string) (model.DatabaseJob, error)
	CancelJob(ctx context.Context, jobID string) error
	ResolveSiteEnv(ctx context.Context, serviceID string, links []model.DatabaseLink) (map[string]string, error)
}

// Service is the daemon-facing database service used by the API.
type Service interface {
	Platform
	ListSiteLinks(ctx context.Context, siteRef string) ([]model.DatabaseLink, error)
	LinkDatabase(ctx context.Context, siteRef string, req LinkDatabaseRequest) (model.DatabaseLink, error)
	UnlinkDatabase(ctx context.Context, siteRef, linkID string) error
	ProvisionSiteDatabase(ctx context.Context, req SiteDatabaseRequest) (ProvisionedDatabase, error)
	LinkProvisioned(ctx context.Context, siteID string, provisioned ProvisionedDatabase) (SiteDatabaseResult, error)
	CreateSiteDatabase(ctx context.Context, siteRef string, req SiteDatabaseRequest) (SiteDatabaseResult, error)
	SiteDatabaseEnv(ctx context.Context, siteRef string) (map[string]string, error)
}
