package agentdocker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestRedisUserRoutesDispatchToACL(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\n")
	body, _ := json.Marshal(dbproto.UserCreateRequest{ID: "redis", Username: "cache", AccessLevel: "read-only"})
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/redis/users", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create = %d %s", recorder.Code, recorder.Body.String())
	}
	var created dbproto.UserResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.User.Username != "cache" || !created.User.PasswordManaged {
		t.Fatalf("created = %+v", created.User)
	}
	if !runner.contains("ACL SETUSER") {
		t.Fatalf("redis create did not call ACL SETUSER: %s", runner.joinedCalls())
	}
	userID := created.User.ID

	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\nuser cache on #abc ~* +@read\n")
	recorder = serveDatabase(t, d, http.MethodGet, "/v1/services/redis/users", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list = %d %s", recorder.Code, recorder.Body.String())
	}
	var list dbproto.UserListResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Users) != 2 {
		t.Fatalf("users = %+v", list.Users)
	}

	enabled := false
	updateBody, _ := json.Marshal(dbproto.UserUpdateRequest{ID: "redis", UserID: userID, Enabled: &enabled})
	recorder = serveDatabase(t, d, http.MethodPatch, dbproto.ServiceUser("redis", userID), updateBody)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update = %d %s", recorder.Code, recorder.Body.String())
	}
	if !runner.contains("ACL SETUSER cache off") {
		t.Fatalf("redis update did not disable the user: %s", runner.joinedCalls())
	}

	revealBody, _ := json.Marshal(dbproto.UserRevealRequest{ID: "redis", UserID: userID})
	recorder = serveDatabase(t, d, http.MethodPost, dbproto.ServiceUserReveal("redis", userID), revealBody)
	if recorder.Code != http.StatusOK {
		t.Fatalf("reveal = %d %s", recorder.Code, recorder.Body.String())
	}
	var secret dbproto.SecretResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &secret); err != nil || secret.Value == "" {
		t.Fatalf("secret = %+v, %v", secret, err)
	}

	deleteBody, _ := json.Marshal(dbproto.UserDeleteRequest{ID: "redis", UserID: userID, Confirm: "wrong"})
	recorder = serveDatabase(t, d, http.MethodDelete, dbproto.ServiceUser("redis", userID), deleteBody)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong delete confirm = %d %s", recorder.Code, recorder.Body.String())
	}
	deleteBody, _ = json.Marshal(dbproto.UserDeleteRequest{ID: "redis", UserID: userID, Confirm: "cache"})
	recorder = serveDatabase(t, d, http.MethodDelete, dbproto.ServiceUser("redis", userID), deleteBody)
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", recorder.Code, recorder.Body.String())
	}
	if !runner.contains("ACL DELUSER cache") {
		t.Fatalf("redis delete did not call ACL DELUSER: %s", runner.joinedCalls())
	}
}

func TestRedisPasswordRoutes(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\n")
	body, _ := json.Marshal(dbproto.UserPasswordRequest{ID: "redis"})
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/redis/password", body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("default password = %d %s", recorder.Code, recorder.Body.String())
	}
	var defaultSecret dbproto.SecretResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &defaultSecret); err != nil || defaultSecret.Value == "" {
		t.Fatalf("default secret = %+v, %v", defaultSecret, err)
	}
	if _, err := d.readDatabaseSecret("redis", databaseUserID("redis", "default", "")); err != nil {
		t.Fatalf("default secret not stored: %v", err)
	}

	if _, err := d.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache"}); err != nil {
		t.Fatal(err)
	}
	cacheID := databaseUserID("redis", "cache", "")
	runner.runOutputs["ACL LIST"] = []byte("user default on #hash ~* &* +@all\nuser cache on #abc ~* +@read\n")
	body, _ = json.Marshal(dbproto.UserPasswordRequest{ID: "redis", UserID: cacheID})
	recorder = serveDatabase(t, d, http.MethodPost, dbproto.ServiceUserPassword("redis", cacheID), body)
	if recorder.Code != http.StatusOK {
		t.Fatalf("user password = %d %s", recorder.Code, recorder.Body.String())
	}
	if !runner.contains("ACL SETUSER cache") {
		t.Fatalf("redis user password did not call ACL SETUSER: %s", runner.joinedCalls())
	}
}

func TestRedisACLFileRoute(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	configDir := filepath.Join(d.root, "services", "redis", "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "redis.conf"), []byte("appendonly yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["ACL LIST"] = []byte("user default on nopass ~* &* +@all\n")
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/redis/redis/acl-file", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("acl-file = %d %s", recorder.Code, recorder.Body.String())
	}
	config, err := os.ReadFile(filepath.Join(configDir, "redis.conf"))
	if err != nil || !strings.Contains(string(config), "aclfile /data/users.acl") {
		t.Fatalf("config = %s, %v", config, err)
	}
}

func TestMySQLRenameRouteReturns400(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	body, _ := json.Marshal(dbproto.DatabaseRenameRequest{ID: "mysql", Name: "app", NewName: "app2"})
	recorder := serveDatabase(t, d, http.MethodPatch, dbproto.ServiceDatabase("mysql", "app"), body)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("MySQL rename invoked Docker: %v", runner.joinedCalls())
	}
}

func TestDatabaseRoutesRejectCallerPassword(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	body := []byte(`{"id":"mysql","username":"app","password":"s3cret"}`)
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/mysql/users", body)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unsupported") {
		t.Fatalf("password was not rejected as unsupported: %s", recorder.Body.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("caller password invoked Docker: %v", runner.joinedCalls())
	}
}

func TestDatabaseRoutesRejectUnknownFields(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	body := []byte(`{"id":"mysql","name":"app","unexpected":true}`)
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/mysql/databases", body)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unknown field invoked Docker: %v", runner.joinedCalls())
	}
}

func TestDatabaseEnvRouteRejectsNestedCallerPassword(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	body := []byte(`{"service_id":"mysql","links":[{"service_id":"mysql","database":"app","username":"app","password":"s3cret"}]}`)
	recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/mysql/database-env", body)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("nested caller password invoked Docker: %v", runner.joinedCalls())
	}
}

func TestDatabaseEnvRouteUsesFrameworkConnectionNames(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	cases := []struct {
		service string
		engine  model.DatabaseEngine
		want    string
	}{
		{"mysql", model.DatabaseMySQL, "mysql"},
		{"mariadb", model.DatabaseMariaDB, "mariadb"},
		{"postgres", model.DatabasePostgreSQL, "pgsql"},
		{"redis", model.DatabaseRedis, "redis"},
	}
	for _, tc := range cases {
		t.Run(tc.service, func(t *testing.T) {
			request := dbproto.SiteEnvRequest{
				ServiceID: tc.service,
				Links: []dbproto.LinkInfo{{
					ServiceID: tc.service, Engine: tc.engine, Database: "app", Username: "app", Primary: true,
				}},
			}
			body, _ := json.Marshal(request)
			recorder := serveDatabase(t, d, http.MethodPost, "/v1/services/"+tc.service+"/database-env", body)
			if recorder.Code != http.StatusOK {
				t.Fatalf("env = %d %s", recorder.Code, recorder.Body.String())
			}
			var response dbproto.SiteEnvResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Env["DB_CONNECTION"] != tc.want {
				t.Fatalf("DB_CONNECTION = %q, want %q (env=%v)", response.Env["DB_CONNECTION"], tc.want, response.Env)
			}
		})
	}
}
