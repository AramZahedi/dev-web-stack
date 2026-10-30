package servicecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// DatabasePlatformAPI is the narrow transport surface shared by the Windows
// agent client and the Linux helper client. Both concrete clients implement it;
// the daemon adapters depend only on this interface so each lane can land
// independently.
type DatabasePlatformAPI interface {
	ListDatabases(ctx context.Context, serviceID string) ([]dbproto.DatabaseInfo, error)
	CreateDatabase(ctx context.Context, serviceID string, req dbproto.DatabaseCreateRequest) (dbproto.DatabaseInfo, error)
	RenameDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseRenameRequest) (dbproto.DatabaseInfo, error)
	DropDatabase(ctx context.Context, serviceID, name string, req dbproto.DatabaseDropRequest) error
	ListUsers(ctx context.Context, serviceID string) ([]dbproto.UserInfo, error)
	CreateUser(ctx context.Context, serviceID string, req dbproto.UserCreateRequest) (dbproto.UserInfo, error)
	UpdateUser(ctx context.Context, serviceID, userID string, req dbproto.UserUpdateRequest) (dbproto.UserInfo, error)
	DeleteUser(ctx context.Context, serviceID, userID string, req dbproto.UserDeleteRequest) error
	SetUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error)
	RevealUserPassword(ctx context.Context, serviceID, userID string) (dbproto.SecretValue, error)
	ListBackups(ctx context.Context, serviceID string) ([]dbproto.BackupInfo, error)
	CreateBackup(ctx context.Context, serviceID, database string, req dbproto.BackupCreateRequest) (dbproto.JobInfo, error)
	DeleteBackup(ctx context.Context, serviceID, backupID string) error
	RestoreBackup(ctx context.Context, serviceID, backupID string, req dbproto.BackupRestoreRequest) (dbproto.JobInfo, error)
	ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req dbproto.DatabaseImportRequest) (dbproto.JobInfo, error)
	ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error)
	DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error)
	GetJob(ctx context.Context, jobID string) (dbproto.JobInfo, error)
	CancelJob(ctx context.Context, jobID string) error
	ResolveSiteEnv(ctx context.Context, serviceID string, req dbproto.SiteEnvRequest) (map[string]string, error)
}

// DatabaseAPIProvider resolves the transport lazily. The Windows adapter uses
// it to wake the WSL agent before the first database call.
type DatabaseAPIProvider func(context.Context) (DatabasePlatformAPI, error)

// databasePlatform maps the shared wire DTOs onto the daemon's databases
// platform contract. It contains no engine logic.
type databasePlatform struct {
	provider DatabaseAPIProvider
}

// NewHelperDocker creates the Linux privileged-helper adapter.
func NewHelperDocker(provider DatabaseAPIProvider) (*HelperDocker, error) {
	if provider == nil {
		return nil, errors.New("servicecontrol: helper database API provider is required")
	}
	return &HelperDocker{databasePlatform: &databasePlatform{provider: provider}}, nil
}

// NewHelperDockerAPI creates a helper adapter from a concrete API.
func NewHelperDockerAPI(api DatabasePlatformAPI) *HelperDocker {
	return &HelperDocker{databasePlatform: &databasePlatform{
		provider: func(context.Context) (DatabasePlatformAPI, error) { return api, nil },
	}}
}

// HelperDocker adapts the Linux helper client to databases.Platform.
type HelperDocker struct {
	*databasePlatform
}

var _ databases.Platform = (*HelperDocker)(nil)

func (p *databasePlatform) api(ctx context.Context) (DatabasePlatformAPI, error) {
	if p == nil || p.provider == nil {
		return nil, fmt.Errorf("%w: database platform is not configured", databases.ErrUnsupported)
	}
	api, err := p.provider(ctx)
	if err != nil {
		return nil, err
	}
	if api == nil {
		return nil, fmt.Errorf("%w: database platform is not configured", databases.ErrUnsupported)
	}
	return api, nil
}

// databasePlatformServiceID maps catalog IDs to the transport IDs.
func databasePlatformServiceID(serviceID string) string {
	return agentAPIID(strings.TrimSpace(strings.ToLower(serviceID)))
}

// databaseEngineForService maps a transport service ID to an engine.
func databaseEngineForService(serviceID string) model.DatabaseEngine {
	switch databasePlatformServiceID(serviceID) {
	case "mysql":
		return model.DatabaseMySQL
	case "mariadb":
		return model.DatabaseMariaDB
	case "postgres":
		return model.DatabasePostgreSQL
	case "redis":
		return model.DatabaseRedis
	default:
		return ""
	}
}

// mapDatabasePlatformError translates agent/helper transport errors into the
// daemon's stable database sentinels.
func mapDatabasePlatformError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, agentproto.ErrNotFound):
		return fmt.Errorf("%w: %v", databases.ErrNotFound, err)
	case errors.Is(err, agentproto.ErrConflict):
		return fmt.Errorf("%w: %v", databases.ErrConflict, err)
	case errors.Is(err, agentproto.ErrInvalidRequest), errors.Is(err, helperproto.ErrInvalidRequest):
		return fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	case errors.Is(err, agentproto.ErrForbidden):
		return fmt.Errorf("%w: %v", databases.ErrProtected, err)
	case errors.Is(err, agentproto.ErrUnsupported):
		return fmt.Errorf("%w: %v", databases.ErrUnsupported, err)
	case errors.Is(err, helperproto.ErrNotImplemented):
		return fmt.Errorf("%w: %v", databases.ErrUnsupported, err)
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "not found"), strings.Contains(lower, "does not exist"):
		return fmt.Errorf("%w: %v", databases.ErrNotFound, err)
	case strings.Contains(lower, "protected"), strings.Contains(lower, "system object"):
		return fmt.Errorf("%w: %v", databases.ErrProtected, err)
	case strings.Contains(lower, "not active"), strings.Contains(lower, "inactive"), strings.Contains(lower, "not installed"):
		return fmt.Errorf("%w: %v", databases.ErrServiceInactive, err)
	case strings.Contains(lower, "linked"):
		return fmt.Errorf("%w: %v", databases.ErrLinkedSites, err)
	case strings.Contains(lower, "password is not managed"), strings.Contains(lower, "password missing"):
		return fmt.Errorf("%w: %v", databases.ErrPasswordMissing, err)
	case strings.Contains(lower, "unsupported"), strings.Contains(lower, "not supported"):
		return fmt.Errorf("%w: %v", databases.ErrUnsupported, err)
	case strings.Contains(lower, "already exists"), strings.Contains(lower, "conflict"):
		return fmt.Errorf("%w: %v", databases.ErrConflict, err)
	case strings.Contains(lower, "invalid"):
		return fmt.Errorf("%w: %v", databases.ErrInvalidRequest, err)
	}
	return err
}

func (p *databasePlatform) ListDatabases(ctx context.Context, serviceID string) ([]model.Database, error) {
	api, err := p.api(ctx)
	if err != nil {
		return nil, err
	}
	databasesList, err := api.ListDatabases(ctx, databasePlatformServiceID(serviceID))
	if err != nil {
		return nil, mapDatabasePlatformError(err)
	}
	engine := databaseEngineForService(serviceID)
	for i := range databasesList {
		if databasesList[i].ServiceID == "" {
			databasesList[i].ServiceID = serviceID
		}
		if databasesList[i].Engine == "" {
			databasesList[i].Engine = engine
		}
	}
	return databasesList, nil
}

func (p *databasePlatform) CreateDatabase(ctx context.Context, serviceID string, req databases.CreateDatabaseRequest) (model.Database, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.Database{}, err
	}
	response, err := api.CreateDatabase(ctx, databasePlatformServiceID(serviceID), dbproto.DatabaseCreateRequest{
		ID: databasePlatformServiceID(serviceID), Name: req.Name, Charset: req.Charset, Collation: req.Collation,
	})
	if err != nil {
		return model.Database{}, mapDatabasePlatformError(err)
	}
	if response.ServiceID == "" {
		response.ServiceID = serviceID
	}
	if response.Engine == "" {
		response.Engine = databaseEngineForService(serviceID)
	}
	return response, nil
}

func (p *databasePlatform) RenameDatabase(ctx context.Context, serviceID, name string, req databases.RenameDatabaseRequest) (model.Database, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.Database{}, err
	}
	response, err := api.RenameDatabase(ctx, databasePlatformServiceID(serviceID), name, dbproto.DatabaseRenameRequest{
		ID: databasePlatformServiceID(serviceID), Name: name, NewName: req.Name,
	})
	if err != nil {
		return model.Database{}, mapDatabasePlatformError(err)
	}
	if response.ServiceID == "" {
		response.ServiceID = serviceID
	}
	if response.Engine == "" {
		response.Engine = databaseEngineForService(serviceID)
	}
	return response, nil
}

func (p *databasePlatform) DropDatabase(ctx context.Context, serviceID, name string, req databases.DropDatabaseRequest) error {
	api, err := p.api(ctx)
	if err != nil {
		return err
	}
	return mapDatabasePlatformError(api.DropDatabase(ctx, databasePlatformServiceID(serviceID), name, dbproto.DatabaseDropRequest{
		ID: databasePlatformServiceID(serviceID), Name: name, Confirm: req.Confirm,
	}))
}

func (p *databasePlatform) ListUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error) {
	api, err := p.api(ctx)
	if err != nil {
		return nil, err
	}
	users, err := api.ListUsers(ctx, databasePlatformServiceID(serviceID))
	if err != nil {
		return nil, mapDatabasePlatformError(err)
	}
	engine := databaseEngineForService(serviceID)
	for i := range users {
		if users[i].ServiceID == "" {
			users[i].ServiceID = serviceID
		}
		if users[i].Engine == "" {
			users[i].Engine = engine
		}
	}
	return users, nil
}

func (p *databasePlatform) CreateUser(ctx context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	response, err := api.CreateUser(ctx, databasePlatformServiceID(serviceID), dbproto.UserCreateRequest{
		ID: databasePlatformServiceID(serviceID), Username: req.Username, Host: req.Host,
		Grants: req.Grants, AccessLevel: req.AccessLevel,
	})
	if err != nil {
		return model.DatabaseUser{}, mapDatabasePlatformError(err)
	}
	if response.ServiceID == "" {
		response.ServiceID = serviceID
	}
	if response.Engine == "" {
		response.Engine = databaseEngineForService(serviceID)
	}
	return response, nil
}

func (p *databasePlatform) UpdateUser(ctx context.Context, serviceID, userID string, req databases.UpdateUserRequest) (model.DatabaseUser, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.DatabaseUser{}, err
	}
	response, err := api.UpdateUser(ctx, databasePlatformServiceID(serviceID), userID, dbproto.UserUpdateRequest{
		ID: databasePlatformServiceID(serviceID), UserID: userID, Enabled: req.Enabled,
		Grants: req.Grants, AccessLevel: req.AccessLevel,
	})
	if err != nil {
		return model.DatabaseUser{}, mapDatabasePlatformError(err)
	}
	if response.ServiceID == "" {
		response.ServiceID = serviceID
	}
	if response.Engine == "" {
		response.Engine = databaseEngineForService(serviceID)
	}
	return response, nil
}

func (p *databasePlatform) DeleteUser(ctx context.Context, serviceID, userID string, req databases.DeleteUserRequest) error {
	api, err := p.api(ctx)
	if err != nil {
		return err
	}
	return mapDatabasePlatformError(api.DeleteUser(ctx, databasePlatformServiceID(serviceID), userID, dbproto.UserDeleteRequest{
		ID: databasePlatformServiceID(serviceID), UserID: userID, Confirm: req.Confirm, ReassignOwned: req.ReassignOwned,
	}))
}

func (p *databasePlatform) SetUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	api, err := p.api(ctx)
	if err != nil {
		return databases.Secret{}, err
	}
	response, err := api.SetUserPassword(ctx, databasePlatformServiceID(serviceID), userID)
	if err != nil {
		return databases.Secret{}, mapDatabasePlatformError(err)
	}
	return databases.Secret{Value: response.Value}, nil
}

func (p *databasePlatform) RevealUserPassword(ctx context.Context, serviceID, userID string) (databases.Secret, error) {
	api, err := p.api(ctx)
	if err != nil {
		return databases.Secret{}, err
	}
	response, err := api.RevealUserPassword(ctx, databasePlatformServiceID(serviceID), userID)
	if err != nil {
		return databases.Secret{}, mapDatabasePlatformError(err)
	}
	return databases.Secret{Value: response.Value}, nil
}

func (p *databasePlatform) ListBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackup, error) {
	api, err := p.api(ctx)
	if err != nil {
		return nil, err
	}
	backups, err := api.ListBackups(ctx, databasePlatformServiceID(serviceID))
	if err != nil {
		return nil, mapDatabasePlatformError(err)
	}
	engine := databaseEngineForService(serviceID)
	for i := range backups {
		if backups[i].ServiceID == "" {
			backups[i].ServiceID = serviceID
		}
		if backups[i].Engine == "" {
			backups[i].Engine = engine
		}
	}
	return backups, nil
}

func (p *databasePlatform) CreateBackup(ctx context.Context, serviceID, database string, req databases.CreateBackupRequest) (model.DatabaseJob, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	job, err := api.CreateBackup(ctx, databasePlatformServiceID(serviceID), database, dbproto.BackupCreateRequest{
		ID: databasePlatformServiceID(serviceID), Database: database, Kind: string(req.Kind),
	})
	if err != nil {
		return model.DatabaseJob{}, mapDatabasePlatformError(err)
	}
	if job.ServiceID == "" {
		job.ServiceID = serviceID
	}
	if job.Database == "" {
		job.Database = database
	}
	return job, nil
}

func (p *databasePlatform) DeleteBackup(ctx context.Context, serviceID, backupID string) error {
	api, err := p.api(ctx)
	if err != nil {
		return err
	}
	return mapDatabasePlatformError(api.DeleteBackup(ctx, databasePlatformServiceID(serviceID), backupID))
}

func (p *databasePlatform) RestoreBackup(ctx context.Context, serviceID, backupID string, req databases.RestoreBackupRequest) (model.DatabaseJob, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	job, err := api.RestoreBackup(ctx, databasePlatformServiceID(serviceID), backupID, dbproto.BackupRestoreRequest{
		ID: databasePlatformServiceID(serviceID), BackupID: backupID, Target: req.Target,
		Overwrite: req.Overwrite, Confirm: req.Confirm, PreRestoreBackup: req.PreRestoreBackup,
	})
	if err != nil {
		return model.DatabaseJob{}, mapDatabasePlatformError(err)
	}
	if job.ServiceID == "" {
		job.ServiceID = serviceID
	}
	return job, nil
}

func (p *databasePlatform) ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req databases.ImportRequest) (model.DatabaseJob, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	job, err := api.ImportDatabase(ctx, databasePlatformServiceID(serviceID), database, file, filename, dbproto.DatabaseImportRequest{
		ID: databasePlatformServiceID(serviceID), Database: database, Mode: string(req.Mode),
		Confirm: req.Confirm, PreImportBackup: req.PreImportBackup,
	})
	if err != nil {
		return model.DatabaseJob{}, mapDatabasePlatformError(err)
	}
	if job.ServiceID == "" {
		job.ServiceID = serviceID
	}
	if job.Database == "" {
		job.Database = database
	}
	return job, nil
}

func (p *databasePlatform) ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error) {
	api, err := p.api(ctx)
	if err != nil {
		return nil, err
	}
	reader, err := api.ExportDatabase(ctx, databasePlatformServiceID(serviceID), database)
	return reader, mapDatabasePlatformError(err)
}

func (p *databasePlatform) DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error) {
	api, err := p.api(ctx)
	if err != nil {
		return nil, err
	}
	reader, err := api.DownloadBackup(ctx, databasePlatformServiceID(serviceID), backupID)
	return reader, mapDatabasePlatformError(err)
}

func (p *databasePlatform) GetJob(ctx context.Context, jobID string) (model.DatabaseJob, error) {
	api, err := p.api(ctx)
	if err != nil {
		return model.DatabaseJob{}, err
	}
	job, err := api.GetJob(ctx, jobID)
	if err != nil {
		return model.DatabaseJob{}, mapDatabasePlatformError(err)
	}
	return job, nil
}

func (p *databasePlatform) CancelJob(ctx context.Context, jobID string) error {
	api, err := p.api(ctx)
	if err != nil {
		return err
	}
	return mapDatabasePlatformError(api.CancelJob(ctx, jobID))
}

func (p *databasePlatform) ResolveSiteEnv(ctx context.Context, serviceID string, links []model.DatabaseLink) (map[string]string, error) {
	api, err := p.api(ctx)
	if err != nil {
		return nil, err
	}
	env, err := api.ResolveSiteEnv(ctx, databasePlatformServiceID(serviceID), dbproto.SiteEnvRequest{
		ServiceID: databasePlatformServiceID(serviceID),
		Links:     links,
	})
	if err != nil {
		return nil, mapDatabasePlatformError(err)
	}
	return env, nil
}
