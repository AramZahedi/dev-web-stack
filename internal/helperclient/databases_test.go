//go:build !windows

package helperclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
)

func TestHelperClientDatabaseCRUD(t *testing.T) {
	var requests []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if got := r.Header.Get(helperproto.ProtocolHeader); got != helperproto.Version {
			t.Errorf("protocol header = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/services/mysql/databases":
			_ = json.NewEncoder(w).Encode(dbproto.DatabaseListResponse{Databases: []dbproto.DatabaseInfo{{Name: "app"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/services/mysql/databases":
			var req dbproto.DatabaseCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create: %v", err)
			}
			if req.ID != "mysql" || req.Name != "blog" {
				t.Errorf("create request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.DatabaseResponse{Database: dbproto.DatabaseInfo{Name: "blog"}})
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == "/v1/services/mysql/databases/app%20db":
			var req dbproto.DatabaseRenameRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode rename: %v", err)
			}
			if req.Name != "app db" || req.NewName != "app2" {
				t.Errorf("rename request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.DatabaseResponse{Database: dbproto.DatabaseInfo{Name: "app2"}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/services/mysql/databases/app":
			var req dbproto.DatabaseDropRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode drop: %v", err)
			}
			if req.Confirm != "app" {
				t.Errorf("drop request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.DeleteResponse{Deleted: true})
		default:
			http.NotFound(w, r)
		}
	})
	client := newUnixClient(t, handler)
	ctx := context.Background()

	list, err := client.ListDatabases(ctx, "mysql")
	if err != nil || len(list) != 1 || list[0].Name != "app" {
		t.Fatalf("list=%v err=%v", list, err)
	}
	created, err := client.CreateDatabase(ctx, "mysql", dbproto.DatabaseCreateRequest{Name: "blog"})
	if err != nil || created.Name != "blog" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	renamed, err := client.RenameDatabase(ctx, "mysql", "app db", dbproto.DatabaseRenameRequest{NewName: "app2"})
	if err != nil || renamed.Name != "app2" {
		t.Fatalf("renamed=%+v err=%v", renamed, err)
	}
	if err := client.DropDatabase(ctx, "mysql", "app", dbproto.DatabaseDropRequest{Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 4 {
		t.Fatalf("requests=%v", requests)
	}
}

func TestHelperClientUsersAndSecrets(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/services/mysql/users":
			_ = json.NewEncoder(w).Encode(dbproto.UserListResponse{Users: []dbproto.UserInfo{{ID: "usr_app", Username: "app"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/services/mysql/users":
			var req dbproto.UserCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create user: %v", err)
			}
			if req.ID != "mysql" || req.Username != "app" {
				t.Errorf("create user request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.UserResponse{User: dbproto.UserInfo{ID: "usr_app", Username: "app"}})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/services/mysql/users/usr_app":
			var req dbproto.UserUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode update user: %v", err)
			}
			if req.UserID != "usr_app" || req.AccessLevel != "full" {
				t.Errorf("update user request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.UserResponse{User: dbproto.UserInfo{ID: "usr_app", Username: "app"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/services/mysql/users/usr_app/password":
			_ = json.NewEncoder(w).Encode(dbproto.SecretResponse{Value: "generated"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/services/mysql/users/usr_app/reveal":
			_ = json.NewEncoder(w).Encode(dbproto.SecretResponse{Value: "revealed"})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/services/mysql/users/usr_app":
			var req dbproto.UserDeleteRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode delete user: %v", err)
			}
			if req.Confirm != "app" {
				t.Errorf("delete user request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.DeleteResponse{Deleted: true})
		default:
			http.NotFound(w, r)
		}
	})
	client := newUnixClient(t, handler)
	ctx := context.Background()

	users, err := client.ListUsers(ctx, "mysql")
	if err != nil || len(users) != 1 {
		t.Fatalf("users=%v err=%v", users, err)
	}
	created, err := client.CreateUser(ctx, "mysql", dbproto.UserCreateRequest{Username: "app"})
	if err != nil || created.ID != "usr_app" {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	updated, err := client.UpdateUser(ctx, "mysql", "usr_app", dbproto.UserUpdateRequest{AccessLevel: "full"})
	if err != nil || updated.ID != "usr_app" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	secret, err := client.SetUserPassword(ctx, "mysql", "usr_app")
	if err != nil || secret.Value != "generated" {
		t.Fatalf("secret=%+v err=%v", secret, err)
	}
	revealed, err := client.RevealUserPassword(ctx, "mysql", "usr_app")
	if err != nil || revealed.Value != "revealed" {
		t.Fatalf("revealed=%+v err=%v", revealed, err)
	}
	if err := client.DeleteUser(ctx, "mysql", "usr_app", dbproto.UserDeleteRequest{Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
}

func TestHelperClientBackupsAndStreams(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/services/mysql/backups":
			_ = json.NewEncoder(w).Encode(dbproto.BackupListResponse{Backups: []dbproto.BackupInfo{{ID: "app--b.sql.gz"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/services/mysql/databases/app/backups":
			var req dbproto.BackupCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode backup: %v", err)
			}
			if req.Database != "app" || req.Kind != "manual" {
				t.Errorf("backup request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.JobResponse{Job: dbproto.JobInfo{ID: "job_1", BackupID: "app--b.sql.gz"}})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/services/mysql/backups/app--b.sql.gz":
			_ = json.NewEncoder(w).Encode(dbproto.DeleteResponse{Deleted: true})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/services/mysql/backups/app--b.sql.gz/restore":
			var req dbproto.BackupRestoreRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode restore: %v", err)
			}
			if req.Target != "app2" {
				t.Errorf("restore request = %+v", req)
			}
			_ = json.NewEncoder(w).Encode(dbproto.JobResponse{Job: dbproto.JobInfo{ID: "job_2"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/services/mysql/backups/app--b.sql.gz/download":
			_, _ = w.Write([]byte("backup-bytes"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/services/mysql/databases/app/export":
			_, _ = w.Write([]byte("export-bytes"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/services/mysql/databases/app/import":
			query := r.URL.Query()
			if query.Get(dbproto.QueryMode) != "replace" || query.Get(dbproto.QueryConfirm) != "app" || query.Get(dbproto.QueryPreImportBackup) != "true" {
				t.Errorf("import query = %v", query)
			}
			if r.Header.Get("X-Devstack-Filename") != "app.sql" {
				t.Errorf("filename header = %q", r.Header.Get("X-Devstack-Filename"))
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != "import-bytes" {
				t.Errorf("import body = %q", body)
			}
			_ = json.NewEncoder(w).Encode(dbproto.JobResponse{Job: dbproto.JobInfo{ID: "job_3"}})
		default:
			http.NotFound(w, r)
		}
	})
	client := newUnixClient(t, handler)
	ctx := context.Background()

	backups, err := client.ListBackups(ctx, "mysql")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
	job, err := client.CreateBackup(ctx, "mysql", "app", dbproto.BackupCreateRequest{Kind: "manual"})
	if err != nil || job.ID != "job_1" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if err := client.DeleteBackup(ctx, "mysql", "app--b.sql.gz"); err != nil {
		t.Fatal(err)
	}
	restore, err := client.RestoreBackup(ctx, "mysql", "app--b.sql.gz", dbproto.BackupRestoreRequest{Target: "app2"})
	if err != nil || restore.ID != "job_2" {
		t.Fatalf("restore=%+v err=%v", restore, err)
	}
	download, err := client.DownloadBackup(ctx, "mysql", "app--b.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(download)
	_ = download.Close()
	if string(data) != "backup-bytes" {
		t.Fatalf("download=%q", data)
	}
	export, err := client.ExportDatabase(ctx, "mysql", "app")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(export)
	_ = export.Close()
	if string(data) != "export-bytes" {
		t.Fatalf("export=%q", data)
	}
	imported, err := client.ImportDatabase(ctx, "mysql", "app", strings.NewReader("import-bytes"), "app.sql", dbproto.DatabaseImportRequest{
		Mode:            "replace",
		Confirm:         "app",
		PreImportBackup: true,
	})
	if err != nil || imported.ID != "job_3" {
		t.Fatalf("import=%+v err=%v", imported, err)
	}
}

func TestHelperClientResolveSiteEnv(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/services/mysql/database-env" {
			http.NotFound(w, r)
			return
		}
		var req dbproto.SiteEnvRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode env: %v", err)
		}
		if req.ServiceID != "mysql" || len(req.Links) != 1 || req.Links[0].Database != "app" {
			t.Errorf("env request = %+v", req)
		}
		_ = json.NewEncoder(w).Encode(dbproto.SiteEnvResponse{Env: map[string]string{"DB_DATABASE": "app"}})
	})
	client := newUnixClient(t, handler)
	env, err := client.ResolveSiteEnv(context.Background(), "mysql", dbproto.SiteEnvRequest{
		Links: []dbproto.LinkInfo{{ServiceID: "mysql", Database: "app", Username: "app", Primary: true}},
	})
	if err != nil || env["DB_DATABASE"] != "app" {
		t.Fatalf("env=%v err=%v", env, err)
	}
}

func TestHelperClientDatabaseInvalidRequestMapping(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(helperproto.ErrorResponse{Code: helperproto.CodeDatabaseInvalidRequest, Message: "bad database request"})
	})
	client := newUnixClient(t, handler)
	_, err := client.CreateDatabase(context.Background(), "mysql", dbproto.DatabaseCreateRequest{Name: "app"})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	var helperErr *Error
	if !errors.As(err, &helperErr) || helperErr.Status != http.StatusBadRequest || helperErr.Code != helperproto.CodeDatabaseInvalidRequest {
		t.Fatalf("helper error = %+v", helperErr)
	}
}

func TestHelperClientBareHTTPErrorsAreNotDatabaseSentinels(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})
			client := newUnixClient(t, handler)
			_, err := client.ListDatabases(context.Background(), "mysql")
			if err == nil {
				t.Fatal("request succeeded")
			}
			if errors.Is(err, databases.ErrNotFound) || errors.Is(err, databases.ErrConflict) {
				t.Fatalf("bare HTTP %d was mapped to a database sentinel: %v", status, err)
			}
			var helperErr *Error
			if !errors.As(err, &helperErr) || helperErr.Status != status || helperErr.Code != helperproto.CodeInternal {
				t.Fatalf("helper error = %+v", helperErr)
			}
		})
	}
}

func TestHelperClientDatabaseErrorMapping(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(helperproto.ErrorResponse{Code: helperproto.CodeServiceInactive, Message: "inactive"})
	})
	client := newUnixClient(t, handler)
	_, err := client.ListDatabases(context.Background(), "mysql")
	if !errors.Is(err, databases.ErrServiceInactive) {
		t.Fatalf("err=%v", err)
	}
	var helperErr *Error
	if !errors.As(err, &helperErr) || helperErr.Status != http.StatusConflict || helperErr.Code != helperproto.CodeServiceInactive {
		t.Fatalf("helper error = %+v", helperErr)
	}
}

func TestHelperClientDatabasePathsEscapeSegments(t *testing.T) {
	var paths []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		_ = json.NewEncoder(w).Encode(dbproto.DatabaseResponse{Database: dbproto.DatabaseInfo{Name: "app"}})
	})
	client := newUnixClient(t, handler)
	_, _ = client.RenameDatabase(context.Background(), "mysql", "app db", dbproto.DatabaseRenameRequest{NewName: "app2"})
	_, _ = client.CreateDatabase(context.Background(), "mysql", dbproto.DatabaseCreateRequest{Name: "app db"})
	_, _ = client.ListBackups(context.Background(), "mysql")
	if len(paths) != 3 {
		t.Fatalf("paths=%v", paths)
	}
	if paths[0] != "/v1/services/mysql/databases/app%20db" {
		t.Fatalf("rename path = %q", paths[0])
	}
	if paths[2] != "/v1/services/mysql/backups" {
		t.Fatalf("backups path = %q", paths[2])
	}
}
