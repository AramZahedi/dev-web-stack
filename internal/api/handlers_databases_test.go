package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

type fakeDatabaseService struct {
	databasesList []model.Database
	users         []model.DatabaseUser
	backups       []model.DatabaseBackup
	links         []model.DatabaseLink
	job           model.DatabaseJob
	secret        string
	env           map[string]string
	err           error

	lastCreate      databases.CreateDatabaseRequest
	lastRename      databases.RenameDatabaseRequest
	lastLink        databases.LinkDatabaseRequest
	lastSiteReq     databases.SiteDatabaseRequest
	importedFile    string
	importedMode    string
	canceledJob     string
	deletedLinks    bool
	deletedMetadata bool
}

func (f *fakeDatabaseService) ListDatabases(context.Context, string) ([]model.Database, error) {
	return f.databasesList, f.err
}
func (f *fakeDatabaseService) CreateDatabase(_ context.Context, serviceID string, req databases.CreateDatabaseRequest) (model.Database, error) {
	f.lastCreate = req
	if f.err != nil {
		return model.Database{}, f.err
	}
	return model.Database{Name: req.Name, ServiceID: serviceID, Engine: model.DatabaseMySQL}, nil
}
func (f *fakeDatabaseService) RenameDatabase(_ context.Context, serviceID, name string, req databases.RenameDatabaseRequest) (model.Database, error) {
	f.lastRename = req
	return model.Database{Name: req.Name, ServiceID: serviceID}, f.err
}
func (f *fakeDatabaseService) DropDatabase(context.Context, string, string, databases.DropDatabaseRequest) error {
	return f.err
}
func (f *fakeDatabaseService) ListUsers(context.Context, string) ([]model.DatabaseUser, error) {
	return f.users, f.err
}
func (f *fakeDatabaseService) CreateUser(_ context.Context, serviceID string, req databases.CreateUserRequest) (model.DatabaseUser, error) {
	return model.DatabaseUser{ID: "usr_new", ServiceID: serviceID, Username: req.Username}, f.err
}
func (f *fakeDatabaseService) UpdateUser(_ context.Context, serviceID, userID string, _ databases.UpdateUserRequest) (model.DatabaseUser, error) {
	return model.DatabaseUser{ID: userID, ServiceID: serviceID}, f.err
}
func (f *fakeDatabaseService) DeleteUser(context.Context, string, string, databases.DeleteUserRequest) error {
	return f.err
}
func (f *fakeDatabaseService) SetUserPassword(context.Context, string, string) (databases.Secret, error) {
	return databases.Secret{Value: f.secret}, f.err
}
func (f *fakeDatabaseService) SetDefaultUserPassword(context.Context, string) (databases.Secret, error) {
	return databases.Secret{Value: f.secret}, f.err
}
func (f *fakeDatabaseService) RevealUserPassword(context.Context, string, string) (databases.Secret, error) {
	return databases.Secret{Value: f.secret}, f.err
}
func (f *fakeDatabaseService) ListBackups(context.Context, string) ([]model.DatabaseBackup, error) {
	return f.backups, f.err
}
func (f *fakeDatabaseService) CreateBackup(context.Context, string, string, databases.CreateBackupRequest) (model.DatabaseJob, error) {
	return f.job, f.err
}
func (f *fakeDatabaseService) DeleteBackup(context.Context, string, string) error {
	return f.err
}
func (f *fakeDatabaseService) RestoreBackup(context.Context, string, string, databases.RestoreBackupRequest) (model.DatabaseJob, error) {
	return f.job, f.err
}
func (f *fakeDatabaseService) ImportDatabase(_ context.Context, _ string, _ string, file io.Reader, _ string, req databases.ImportRequest) (model.DatabaseJob, error) {
	data, _ := io.ReadAll(file)
	f.importedFile = string(data)
	f.importedMode = string(req.Mode)
	return f.job, f.err
}
func (f *fakeDatabaseService) ExportDatabase(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("dump")), f.err
}
func (f *fakeDatabaseService) DownloadBackup(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("backup")), f.err
}
func (f *fakeDatabaseService) GetJob(context.Context, string) (model.DatabaseJob, error) {
	return f.job, f.err
}
func (f *fakeDatabaseService) CancelJob(_ context.Context, jobID string) error {
	f.canceledJob = jobID
	return f.err
}
func (f *fakeDatabaseService) ResolveSiteEnv(context.Context, string, []model.DatabaseLink) (map[string]string, error) {
	return f.env, f.err
}
func (f *fakeDatabaseService) ListSiteLinks(context.Context, string) ([]model.DatabaseLink, error) {
	return f.links, f.err
}
func (f *fakeDatabaseService) LinkDatabase(_ context.Context, _ string, req databases.LinkDatabaseRequest) (model.DatabaseLink, error) {
	f.lastLink = req
	return model.DatabaseLink{ID: "lnk_1", ServiceID: req.ServiceID, Database: req.Database}, f.err
}
func (f *fakeDatabaseService) UnlinkDatabase(context.Context, string, string) error {
	return f.err
}
func (f *fakeDatabaseService) ProvisionSiteDatabase(context.Context, databases.SiteDatabaseRequest) (databases.ProvisionedDatabase, error) {
	return databases.ProvisionedDatabase{}, f.err
}
func (f *fakeDatabaseService) LinkProvisioned(context.Context, string, databases.ProvisionedDatabase) (databases.SiteDatabaseResult, error) {
	return databases.SiteDatabaseResult{}, f.err
}
func (f *fakeDatabaseService) CreateSiteDatabase(_ context.Context, _ string, req databases.SiteDatabaseRequest) (databases.SiteDatabaseResult, error) {
	f.lastSiteReq = req
	return databases.SiteDatabaseResult{
		Link:     model.DatabaseLink{ID: "lnk_1", Database: "app"},
		Database: model.Database{Name: "app"},
		User:     model.DatabaseUser{ID: "usr_app", Username: "app"},
		Warning:  "no access",
	}, f.err
}
func (f *fakeDatabaseService) SiteDatabaseEnv(context.Context, string) (map[string]string, error) {
	return f.env, f.err
}
func (f *fakeDatabaseService) DeleteServiceLinks(context.Context, string) error {
	f.deletedLinks = true
	return f.err
}
func (f *fakeDatabaseService) DeleteServiceMetadata(context.Context, string) error {
	f.deletedMetadata = true
	return f.err
}
func (f *fakeDatabaseService) Impact(_ context.Context, serviceID string) (databases.ServiceImpact, error) {
	return databases.ServiceImpact{
		ServiceID:   serviceID,
		LinkedSites: []model.DatabaseSiteRef{{ID: "site_1", Name: "blog"}},
		Backups:     []model.DatabaseBackup{{ID: "bkp_1"}},
		RunningJobs: []model.DatabaseJob{{ID: "job_1", Status: model.JobRunning}},
	}, f.err
}

func TestDatabaseRoutes(t *testing.T) {
	fake := &fakeDatabaseService{databasesList: []model.Database{{Name: "app", ServiceID: "mysql"}}}
	fx := newFixture(t, Config{Databases: fake})
	rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/databases", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"databases"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = fx.request(t, http.MethodPost, "/api/v1/services/mysql/databases", strings.NewReader(`{"name":"app"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCreate.Name != "app" {
		t.Fatalf("create request = %+v", fake.lastCreate)
	}
}

func TestDatabaseRevealIsAuditedAndNoStore(t *testing.T) {
	fx := newFixture(t, Config{Databases: &fakeDatabaseService{secret: "s3cret"}})
	rec := fx.request(t, http.MethodPost, "/api/v1/services/mysql/users/usr_app/reveal", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("body=%s", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache=%q", rec.Header().Get("Cache-Control"))
	}
}

func TestDatabaseErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{databases.ErrNotFound, http.StatusNotFound, "DATABASE_NOT_FOUND"},
		{databases.ErrConflict, http.StatusConflict, "DATABASE_CONFLICT"},
		{databases.ErrProtected, http.StatusConflict, "DATABASE_PROTECTED"},
		{databases.ErrUnsupported, http.StatusBadRequest, "DATABASE_UNSUPPORTED"},
		{databases.ErrServiceInactive, http.StatusConflict, "DATABASE_SERVICE_INACTIVE"},
		{databases.ErrLinkedSites, http.StatusConflict, "DATABASE_IN_USE"},
		{databases.ErrPasswordMissing, http.StatusConflict, "DATABASE_PASSWORD_MISSING"},
		{databases.ErrJobNotFound, http.StatusNotFound, "DATABASE_JOB_NOT_FOUND"},
	}
	for _, tc := range cases {
		fx := newFixture(t, Config{Databases: &fakeDatabaseService{err: tc.err}})
		rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/databases", nil)
		assertError(t, rec, tc.status, tc.code)
	}
}

func TestDatabaseImportMultipart(t *testing.T) {
	fake := &fakeDatabaseService{job: model.DatabaseJob{ID: "job_1", Status: model.JobSucceeded}}
	fx := newFixture(t, Config{Databases: fake})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "dump.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "select 1;"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("mode", "merge"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/services/mysql/databases/app/import", &body)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: fx.session})
	req.Header.Set("X-CSRF-Token", fx.server.csrfFor(fx.session))
	rec := httptest.NewRecorder()
	fx.raw.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.importedFile != "select 1;" || fake.importedMode != "merge" {
		t.Fatalf("imported file=%q mode=%q", fake.importedFile, fake.importedMode)
	}
}

func TestDatabaseExportAndDownloadStream(t *testing.T) {
	fx := newFixture(t, Config{Databases: &fakeDatabaseService{}})
	for _, path := range []string{
		"/api/v1/services/mysql/databases/app/export",
		"/api/v1/services/mysql/backups/bkp_1/download",
	} {
		rec := fx.request(t, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s cache=%q", path, rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("Content-Disposition") == "" {
			t.Fatalf("%s missing Content-Disposition", path)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s empty body", path)
		}
	}
}

func TestSiteDatabaseRoutes(t *testing.T) {
	fake := &fakeDatabaseService{
		links: []model.DatabaseLink{{ID: "lnk_1", Database: "app"}},
		env:   map[string]string{"DB_DATABASE": "app"},
	}
	fx := newFixture(t, Config{Databases: fake})
	rec := fx.request(t, http.MethodGet, "/api/v1/sites/blog/databases", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"links"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = fx.request(t, http.MethodPost, "/api/v1/sites/blog/databases", strings.NewReader(`{"service_id":"mysql","database":"app","user_id":"usr_app"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("link status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastLink.UserID != "usr_app" {
		t.Fatalf("link request = %+v", fake.lastLink)
	}
	rec = fx.request(t, http.MethodPost, "/api/v1/sites/blog/databases", strings.NewReader(`{"service_id":"mysql","new_database":{"name":"blog"},"new_user":{"username":"blog_user"}}`))
	if rec.Code != http.StatusCreated || fake.lastSiteReq.NewDatabase == nil {
		t.Fatalf("create status=%d body=%s req=%+v", rec.Code, rec.Body.String(), fake.lastSiteReq)
	}
	rec = fx.request(t, http.MethodDelete, "/api/v1/sites/blog/databases/lnk_1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = fx.request(t, http.MethodGet, "/api/v1/sites/blog/database-env", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), `"DB_DATABASE"`) {
		t.Fatalf("env status=%d cache=%q body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
}

func TestDatabaseJobRoutes(t *testing.T) {
	fake := &fakeDatabaseService{job: model.DatabaseJob{ID: "job_1", Status: model.JobRunning}}
	fx := newFixture(t, Config{Databases: fake})
	rec := fx.request(t, http.MethodGet, "/api/v1/jobs/job_1", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"job_1"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = fx.request(t, http.MethodDelete, "/api/v1/jobs/job_1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.canceledJob != "job_1" {
		t.Fatalf("canceled job = %q", fake.canceledJob)
	}
}

func TestDatabaseRoutesUnavailableWithoutService(t *testing.T) {
	fx := newFixture(t, Config{})
	rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/databases", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateSiteCarriesDatabaseRequest(t *testing.T) {
	fake := &fakeSiteService{sites: make(map[string]model.Site)}
	fx := newFixture(t, Config{})
	fx.server.sites = fake
	rec := fx.request(t, http.MethodPost, "/api/v1/sites", strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static","database":{"service_id":"mysql","database":"app","user_id":"usr_app"}}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.created) != 1 || fake.created[0].Database == nil || fake.created[0].Database.Database != "app" {
		t.Fatalf("created = %+v", fake.created)
	}
}

func TestRenameDatabasePassesConfirm(t *testing.T) {
	fake := &fakeDatabaseService{}
	fx := newFixture(t, Config{Databases: fake})
	rec := fx.request(t, http.MethodPatch, "/api/v1/services/postgresql/databases/old", strings.NewReader(`{"name":"new","confirm":"old"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastRename.Confirm != "old" || fake.lastRename.Name != "new" {
		t.Fatalf("rename request = %+v", fake.lastRename)
	}
}

func TestRedisDefaultPasswordRoute(t *testing.T) {
	fake := &fakeDatabaseService{secret: "redis-secret"}
	fx := newFixture(t, Config{Databases: fake})
	rec := fx.request(t, http.MethodPost, "/api/v1/services/redis/password", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "redis-secret") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache=%q", rec.Header().Get("Cache-Control"))
	}
}

func TestDatabaseImpactRoute(t *testing.T) {
	fx := newFixture(t, Config{Databases: &fakeDatabaseService{}})
	rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/database-impact", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{`"linked_sites"`, `"backups"`, `"running_jobs"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("missing %s in %s", want, rec.Body.String())
		}
	}
}

func TestUninstallDeleteDataCleansDatabaseMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		delete bool
		want   bool
	}{
		{name: "delete-data", delete: true, want: true},
		{name: "keep-data", delete: false, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeDatabaseService{}
			fx := newFixture(t, Config{
				Databases: db,
				Services:  &fakeServiceCoordinator{},
			})
			body := `{"delete_data":false}`
			if tc.delete {
				body = `{"delete_data":true}`
			}
			rec := fx.request(t, http.MethodDelete, "/api/v1/services/mysql", strings.NewReader(body))
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if db.deletedLinks != tc.want || db.deletedMetadata != tc.want {
				t.Fatalf("cleanup links=%v metadata=%v, want %v", db.deletedLinks, db.deletedMetadata, tc.want)
			}
		})
	}
}

func TestDatabaseServiceErrorDoesNotLeakSecret(t *testing.T) {
	fx := newFixture(t, Config{Databases: &fakeDatabaseService{err: errors.New("connection failed with password s3cret")}})
	rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/databases", nil)
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("response leaked secret: %s", rec.Body.String())
	}
}
