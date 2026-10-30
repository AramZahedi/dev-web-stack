package servicecontrol

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeDatabaseAPI struct {
	databases []dbproto.DatabaseInfo
	users     []dbproto.UserInfo
	secret    dbproto.SecretValue
	env       map[string]string
	job       dbproto.JobInfo
	err       error

	lastService string
	lastUser    string
	lastReq     any
	lastFile    string
}

func (f *fakeDatabaseAPI) ListDatabases(context.Context, string) ([]dbproto.DatabaseInfo, error) {
	return f.databases, f.err
}

func (f *fakeDatabaseAPI) CreateDatabase(_ context.Context, serviceID string, req dbproto.DatabaseCreateRequest) (dbproto.DatabaseInfo, error) {
	f.lastService, f.lastReq = serviceID, req
	return dbproto.DatabaseInfo{Name: req.Name, ServiceID: serviceID, Engine: model.DatabaseMySQL}, f.err
}

func (f *fakeDatabaseAPI) RenameDatabase(_ context.Context, serviceID, name string, req dbproto.DatabaseRenameRequest) (dbproto.DatabaseInfo, error) {
	f.lastService, f.lastReq = serviceID, req
	return dbproto.DatabaseInfo{Name: req.NewName, ServiceID: serviceID}, f.err
}

func (f *fakeDatabaseAPI) DropDatabase(_ context.Context, serviceID, name string, req dbproto.DatabaseDropRequest) error {
	f.lastService, f.lastReq = serviceID, req
	return f.err
}

func (f *fakeDatabaseAPI) ListUsers(context.Context, string) ([]dbproto.UserInfo, error) {
	return f.users, f.err
}

func (f *fakeDatabaseAPI) CreateUser(_ context.Context, serviceID string, req dbproto.UserCreateRequest) (dbproto.UserInfo, error) {
	f.lastService, f.lastReq = serviceID, req
	return dbproto.UserInfo{ID: "usr_1", ServiceID: serviceID, Username: req.Username}, f.err
}

func (f *fakeDatabaseAPI) UpdateUser(_ context.Context, serviceID, userID string, req dbproto.UserUpdateRequest) (dbproto.UserInfo, error) {
	f.lastService, f.lastUser, f.lastReq = serviceID, userID, req
	return dbproto.UserInfo{ID: userID, ServiceID: serviceID}, f.err
}

func (f *fakeDatabaseAPI) DeleteUser(_ context.Context, serviceID, userID string, req dbproto.UserDeleteRequest) error {
	f.lastService, f.lastUser, f.lastReq = serviceID, userID, req
	return f.err
}

func (f *fakeDatabaseAPI) SetUserPassword(_ context.Context, serviceID, userID string) (dbproto.SecretValue, error) {
	f.lastService, f.lastUser = serviceID, userID
	return f.secret, f.err
}

func (f *fakeDatabaseAPI) RevealUserPassword(_ context.Context, serviceID, userID string) (dbproto.SecretValue, error) {
	f.lastService, f.lastUser = serviceID, userID
	return f.secret, f.err
}

func (f *fakeDatabaseAPI) ListBackups(context.Context, string) ([]dbproto.BackupInfo, error) {
	return nil, f.err
}

func (f *fakeDatabaseAPI) CreateBackup(_ context.Context, serviceID, database string, req dbproto.BackupCreateRequest) (dbproto.JobInfo, error) {
	f.lastService, f.lastReq = serviceID, req
	return f.job, f.err
}

func (f *fakeDatabaseAPI) DeleteBackup(context.Context, string, string) error { return f.err }

func (f *fakeDatabaseAPI) RestoreBackup(_ context.Context, serviceID, backupID string, req dbproto.BackupRestoreRequest) (dbproto.JobInfo, error) {
	f.lastService, f.lastReq = serviceID, req
	return f.job, f.err
}

func (f *fakeDatabaseAPI) ImportDatabase(_ context.Context, serviceID, database string, file io.Reader, filename string, req dbproto.DatabaseImportRequest) (dbproto.JobInfo, error) {
	f.lastService, f.lastFile, f.lastReq = serviceID, filename, req
	_, _ = io.ReadAll(file)
	return f.job, f.err
}

func (f *fakeDatabaseAPI) ExportDatabase(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("dump")), f.err
}

func (f *fakeDatabaseAPI) DownloadBackup(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("backup")), f.err
}

func (f *fakeDatabaseAPI) GetJob(context.Context, string) (dbproto.JobInfo, error) {
	return f.job, f.err
}

func (f *fakeDatabaseAPI) CancelJob(context.Context, string) error { return f.err }

func (f *fakeDatabaseAPI) ResolveSiteEnv(_ context.Context, serviceID string, req dbproto.SiteEnvRequest) (map[string]string, error) {
	f.lastService, f.lastReq = serviceID, req
	return f.env, f.err
}

// serviceAgentAPI stubs let the fake satisfy NewAgentDocker's service API.
func (f *fakeDatabaseAPI) ServiceAction(context.Context, agentproto.ServiceRequest) (agentproto.ServiceResponse, error) {
	return agentproto.ServiceResponse{}, nil
}
func (f *fakeDatabaseAPI) ServiceStatus(context.Context, string) (agentproto.ServiceResponse, error) {
	return agentproto.ServiceResponse{}, nil
}
func (f *fakeDatabaseAPI) ServiceLogs(context.Context, string, agentproto.ServiceLogsRequest) (agentproto.ServiceLogsResponse, error) {
	return agentproto.ServiceLogsResponse{}, nil
}
func (f *fakeDatabaseAPI) ServiceCredential(context.Context, agentproto.ServiceCredentialRequest) (agentproto.ServiceCredentialResponse, error) {
	return agentproto.ServiceCredentialResponse{}, nil
}

func TestAgentDockerMapsDatabaseList(t *testing.T) {
	api := &fakeDatabaseAPI{databases: []dbproto.DatabaseInfo{{Name: "app", Engine: "mysql"}}}
	a, err := NewAgentDocker(api, "/data/devstack")
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.ListDatabases(context.Background(), "mysql")
	if err != nil || len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestAgentDockerMapsPostgresServiceID(t *testing.T) {
	api := &fakeDatabaseAPI{databases: []dbproto.DatabaseInfo{{Name: "app", Engine: "postgres"}}}
	a, _ := NewAgentDocker(api, "/data/devstack")
	if _, err := a.ListDatabases(context.Background(), "postgresql"); err != nil {
		t.Fatal(err)
	}
	if api.lastService != "" {
		t.Fatalf("ListDatabases did not record service; last=%q", api.lastService)
	}
	// The adapter must send the agent ID "postgres" for the catalog ID.
	if _, err := a.CreateDatabase(context.Background(), "postgresql", databases.CreateDatabaseRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	if api.lastService != "postgres" {
		t.Fatalf("service id = %q, want postgres", api.lastService)
	}
}

func TestAgentDockerMapsCreateAndReveal(t *testing.T) {
	api := &fakeDatabaseAPI{secret: dbproto.SecretValue{Value: "s3cret"}}
	a, _ := NewAgentDocker(api, "/data/devstack")
	db, err := a.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app", Charset: "utf8mb4"})
	if err != nil || db.Name != "app" {
		t.Fatalf("db=%+v err=%v", db, err)
	}
	req, ok := api.lastReq.(dbproto.DatabaseCreateRequest)
	if !ok || req.Charset != "utf8mb4" {
		t.Fatalf("request = %#v", api.lastReq)
	}
	secret, err := a.RevealUserPassword(context.Background(), "mysql", "usr_1")
	if err != nil || secret.Value != "s3cret" {
		t.Fatalf("secret=%+v err=%v", secret, err)
	}
	if api.lastUser != "usr_1" {
		t.Fatalf("user id = %q", api.lastUser)
	}
}

func TestAgentDockerResolveSiteEnv(t *testing.T) {
	api := &fakeDatabaseAPI{env: map[string]string{"DB_DATABASE": "app", "DB_PASSWORD": "s3cret"}}
	a, _ := NewAgentDocker(api, "/data/devstack")
	env, err := a.ResolveSiteEnv(context.Background(), "mysql", []model.DatabaseLink{{ID: "lnk_1", Database: "app", UserID: "usr_1", Username: "app", Primary: true}})
	if err != nil || env["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("env=%v err=%v", env, err)
	}
	req, ok := api.lastReq.(dbproto.SiteEnvRequest)
	if !ok || len(req.Links) != 1 || req.Links[0].UserID != "usr_1" {
		t.Fatalf("request = %#v", api.lastReq)
	}
}

func TestAgentDockerMapsNotFound(t *testing.T) {
	api := &fakeDatabaseAPI{err: agentproto.ErrNotFound}
	a, _ := NewAgentDocker(api, "/data/devstack")
	if _, err := a.ListDatabases(context.Background(), "mysql"); !errors.Is(err, databases.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestHelperDockerMapsDatabaseList(t *testing.T) {
	api := &fakeDatabaseAPI{databases: []dbproto.DatabaseInfo{{Name: "app", Engine: "mysql"}}}
	h := NewHelperDockerAPI(api)
	got, err := h.ListDatabases(context.Background(), "mysql")
	if err != nil || len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestHelperDockerRequiresProvider(t *testing.T) {
	h := NewHelperDockerAPI(nil)
	if _, err := h.ListDatabases(context.Background(), "mysql"); !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
