package agentdocker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func serveDatabase(t *testing.T, d *Docker, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	d.Handler().ServeHTTP(recorder, req)
	return recorder
}

func TestDatabaseRoutesListAndCreate(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["information_schema.SCHEMATA"] = []byte("app\t1024\t3\n")
	recorder := serveDatabase(t, d, http.MethodGet, "/v1/services/mysql/databases", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list = %d %s", recorder.Code, recorder.Body.String())
	}
	var list dbproto.DatabaseListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Databases) != 1 || list.Databases[0].Name != "app" {
		t.Fatalf("list = %+v", list)
	}

	body, _ := json.Marshal(dbproto.DatabaseCreateRequest{ID: "mysql", Name: "blog"})
	recorder = serveDatabase(t, d, http.MethodPost, "/v1/services/mysql/databases", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create = %d %s", recorder.Code, recorder.Body.String())
	}
	var created dbproto.DatabaseResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Database.Name != "blog" {
		t.Fatalf("created = %+v", created)
	}
	if !runner.stdinContains("CREATE DATABASE `blog`") {
		t.Fatal("create route did not execute SQL")
	}
}

func TestDatabaseRouteRejectsMismatchedID(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	body, _ := json.Marshal(dbproto.DatabaseCreateRequest{ID: "postgres", Name: "blog"})
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/mysql/databases", body)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("mismatched ID invoked Docker: %v", runner.joinedCalls())
	}
}

func TestDatabaseDropRouteRequiresConfirm(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	body, _ := json.Marshal(dbproto.DatabaseDropRequest{ID: "mysql", Name: "app", Confirm: "wrong"})
	recorder := serveDatabase(t, d, http.MethodDelete, "/v1/services/mysql/databases/app", body)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	if runner.stdinContains("DROP DATABASE") {
		t.Fatal("rejected drop executed SQL")
	}
}

func TestDatabaseRouteUnknownService(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	recorder := serveDatabase(t, d, http.MethodGet, "/v1/services/nginx/databases", nil)
	if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestBackupDownloadRouteStreams(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.streamOutput = []byte("dump")
	job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := serveDatabase(t, d, http.MethodGet, dbproto.ServiceBackupDownload("mysql", job.BackupID), nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("download = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() == 0 {
		t.Fatal("empty backup download")
	}
	if !strings.Contains(recorder.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("content disposition = %q", recorder.Header().Get("Content-Disposition"))
	}
}

func TestDatabaseEnvRouteResolvesManagedSecret(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	if err := d.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	request := dbproto.SiteEnvRequest{
		ServiceID: "mysql",
		Links: []dbproto.LinkInfo{{
			ServiceID:       "mysql",
			Engine:          model.DatabaseMySQL,
			Database:        "app",
			UserID:          "usr_app",
			Username:        "app",
			Primary:         true,
			PasswordManaged: true,
			Connection:      model.DatabaseConnection{Port: 3306},
		}},
	}
	body, _ := json.Marshal(request)
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/mysql/database-env", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("env = %d %s", recorder.Code, recorder.Body.String())
	}
	var response dbproto.SiteEnvResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Env["DB_PASSWORD"] != "s3cret" || response.Env["DB_DATABASE"] != "app" {
		t.Fatalf("env = %v", response.Env)
	}
	if !strings.HasPrefix(response.Env["DATABASE_URL"], "mysql://app:s3cret@127.0.0.1:3306/app") {
		t.Fatalf("DATABASE_URL = %q", response.Env["DATABASE_URL"])
	}
}

func TestDatabaseEnvRouteOmitsMetadataPassword(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	request := dbproto.SiteEnvRequest{
		ServiceID: "mysql",
		Links: []dbproto.LinkInfo{{
			ServiceID: "mysql",
			Engine:    model.DatabaseMySQL,
			Database:  "app",
			Username:  "app",
			Primary:   true,
		}},
	}
	body, _ := json.Marshal(request)
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/mysql/database-env", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("env = %d %s", recorder.Code, recorder.Body.String())
	}
	var response dbproto.SiteEnvResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if _, ok := response.Env["DB_PASSWORD"]; ok {
		t.Fatalf("metadata-only env has a password: %v", response.Env)
	}
	if _, ok := response.Env["DATABASE_URL"]; ok {
		t.Fatalf("metadata-only env has a URL: %v", response.Env)
	}
}

func TestDatabaseImportRouteUsesQueryAndFilename(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/services/mysql/databases/app/import?mode=merge", bytes.NewReader(gzipBytes(t, "CREATE TABLE t (id int);\n")))
	req.Header.Set("X-Devstack-Filename", "dump.sql.gz")
	req.Header.Set("Content-Type", "application/gzip")
	recorder := httptest.NewRecorder()
	d.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("import = %d %s", recorder.Code, recorder.Body.String())
	}
	var response dbproto.JobResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Job.Status != model.JobSucceeded {
		t.Fatalf("job = %+v", response.Job)
	}
	if !runner.stdinContains("CREATE TABLE t (id int);") {
		t.Fatal("import route did not stream the dump")
	}
}
