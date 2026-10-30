package agentclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
)

func TestAgentClientDatabaseCRUD(t *testing.T) {
	srv, pin := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(agentproto.HeaderAuth) != "Bearer secret" {
			writeJSON(w, http.StatusUnauthorized, agentproto.ErrorResponse{Code: agentproto.CodeUnauthenticated, Message: "token"})
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/services/mysql/databases" {
			writeJSON(w, http.StatusNotFound, agentproto.ErrorResponse{Code: agentproto.CodeNotFound, Message: "path"})
			return
		}
		writeJSON(w, http.StatusOK, dbproto.DatabaseListResponse{Databases: []dbproto.DatabaseInfo{{Name: "app"}}})
	}))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	got, err := c.ListDatabases(context.Background(), "mysql")
	if err != nil || len(got) != 1 || got[0].Name != "app" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestAgentClientCreateDatabaseSendsBody(t *testing.T) {
	var received dbproto.DatabaseCreateRequest
	srv, pin := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/services/mysql/databases" {
			writeJSON(w, http.StatusNotFound, agentproto.ErrorResponse{Code: agentproto.CodeNotFound})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			writeJSON(w, http.StatusBadRequest, agentproto.ErrorResponse{Code: agentproto.CodeInvalidRequest, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, dbproto.DatabaseResponse{Database: dbproto.DatabaseInfo{Name: received.Name}})
	}))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	got, err := c.CreateDatabase(context.Background(), "mysql", dbproto.DatabaseCreateRequest{ID: "mysql", Name: "app"})
	if err != nil || got.Name != "app" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if received.ID != "mysql" || received.Name != "app" {
		t.Fatalf("received = %+v", received)
	}
}

func TestAgentClientImportUsesRawBodyAndFilename(t *testing.T) {
	var body []byte
	var contentType, filename, mode string
	srv, pin := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/services/mysql/databases/app/import" {
			writeJSON(w, http.StatusNotFound, agentproto.ErrorResponse{Code: agentproto.CodeNotFound})
			return
		}
		body, _ = io.ReadAll(r.Body)
		contentType = r.Header.Get("Content-Type")
		filename = r.Header.Get("X-Devstack-Filename")
		mode = r.URL.Query().Get("mode")
		writeJSON(w, http.StatusOK, dbproto.JobResponse{Job: dbproto.JobInfo{ID: "job_1", Status: "succeeded"}})
	}))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	job, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "dump.sql.gz", dbproto.DatabaseImportRequest{Mode: "merge"})
	if err != nil || job.ID != "job_1" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if string(body) != "dump" || contentType != "application/gzip" || filename != "dump.sql.gz" || mode != "merge" {
		t.Fatalf("body=%q content-type=%q filename=%q mode=%q", body, contentType, filename, mode)
	}
}

func TestAgentClientExportStreams(t *testing.T) {
	srv, pin := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/services/mysql/databases/app/export" {
			writeJSON(w, http.StatusNotFound, agentproto.ErrorResponse{Code: agentproto.CodeNotFound})
			return
		}
		w.Header().Set("Content-Type", "application/sql")
		_, _ = w.Write([]byte("dump"))
	}))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	body, err := c.ExportDatabase(context.Background(), "mysql", "app")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	_ = body.Close()
	if string(data) != "dump" {
		t.Fatalf("export = %q", data)
	}
}

func TestAgentClientResolveSiteEnv(t *testing.T) {
	srv, pin := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/services/mysql/database-env" {
			writeJSON(w, http.StatusNotFound, agentproto.ErrorResponse{Code: agentproto.CodeNotFound})
			return
		}
		writeJSON(w, http.StatusOK, dbproto.SiteEnvResponse{Env: map[string]string{"DB_DATABASE": "app"}})
	}))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	env, err := c.ResolveSiteEnv(context.Background(), "mysql", dbproto.SiteEnvRequest{ServiceID: "mysql"})
	if err != nil || env["DB_DATABASE"] != "app" {
		t.Fatalf("env=%v err=%v", env, err)
	}
}

func TestAgentClientDownloadBackup(t *testing.T) {
	srv, pin := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/services/mysql/backups/app%2Fdump.sql.gz/download" {
			writeJSON(w, http.StatusNotFound, agentproto.ErrorResponse{Code: agentproto.CodeNotFound})
			return
		}
		_, _ = w.Write([]byte("backup"))
	}))
	c, err := New(Config{BaseURL: srv.URL, Token: "secret", PinSHA256: pin})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	body, err := c.DownloadBackup(context.Background(), "mysql", "app/dump.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	_ = body.Close()
	if string(data) != "backup" {
		t.Fatalf("download = %q", data)
	}
}
