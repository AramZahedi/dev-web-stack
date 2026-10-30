//go:build !windows

package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestClientDatabaseCRUDAndReveal(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	dbs, err := c.ListServiceDatabases(ctx, "mysql")
	if err != nil || len(dbs) != 1 || dbs[0].Name != "app" {
		t.Fatalf("dbs=%v err=%v", dbs, err)
	}
	api.last(t, http.MethodGet, "/api/v1/services/mysql/databases")

	created, err := c.CreateServiceDatabase(ctx, "mysql", databases.CreateDatabaseRequest{Name: "blog"})
	if err != nil || created.Name != "blog" {
		t.Fatalf("created=%v err=%v", created, err)
	}
	create := api.last(t, http.MethodPost, "/api/v1/services/mysql/databases")
	if !strings.Contains(create.body, `"name":"blog"`) {
		t.Fatalf("create body = %s", create.body)
	}

	got, err := c.GetServiceDatabase(ctx, "mysql", "app db")
	if err != nil || got.Name != "app" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	api.last(t, http.MethodGet, "/api/v1/services/mysql/databases/app db")

	if _, err := c.RenameServiceDatabase(ctx, "mysql", "app", databases.RenameDatabaseRequest{Name: "app2"}); err != nil {
		t.Fatal(err)
	}
	rename := api.last(t, http.MethodPatch, "/api/v1/services/mysql/databases/app")
	if !strings.Contains(rename.body, `"name":"app2"`) {
		t.Fatalf("rename body = %s", rename.body)
	}

	if err := c.DropServiceDatabase(ctx, "mysql", "app", databases.DropDatabaseRequest{Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	drop := api.last(t, http.MethodDelete, "/api/v1/services/mysql/databases/app")
	if !strings.Contains(drop.body, `"confirm":"app"`) {
		t.Fatalf("drop body = %s", drop.body)
	}

	secret, err := c.RevealServiceUserPassword(ctx, "mysql", "usr_app")
	if err != nil || secret.Value != "s3cret" {
		t.Fatalf("secret=%v err=%v", secret, err)
	}
	api.last(t, http.MethodPost, "/api/v1/services/mysql/users/usr_app/reveal")

	rotated, err := c.SetServiceUserPassword(ctx, "mysql", "usr_app")
	if err != nil || rotated.Value != "new-secret" {
		t.Fatalf("rotated=%v err=%v", rotated, err)
	}
	api.last(t, http.MethodPost, "/api/v1/services/mysql/users/usr_app/password")
}

func TestClientDatabaseWrongEnvelopeFailsLoudly(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}, wrongDatabaseEnvelope: true}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if _, err := c.GetServiceDatabase(context.Background(), "mysql", "app"); err == nil {
		t.Fatal("wrong response envelope returned a zero database without an error")
	}
}

func TestClientDatabaseStreaming(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	body, err := c.ExportServiceDatabase(ctx, "mysql", "app")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	_ = body.Close()
	if string(data) != "dump" {
		t.Fatalf("export=%q", data)
	}
	api.last(t, http.MethodGet, "/api/v1/services/mysql/databases/app/export")

	backup, err := c.DownloadServiceBackup(ctx, "mysql", "bkp_1")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(backup)
	_ = backup.Close()
	if string(data) != "backup-data" {
		t.Fatalf("backup=%q", data)
	}
	api.last(t, http.MethodGet, "/api/v1/services/mysql/backups/bkp_1/download")
}

func TestClientDatabaseUsersBackupsAndJobs(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	users, err := c.ListServiceUsers(ctx, "mysql")
	if err != nil || len(users) != 1 || users[0].ID != "usr_app" {
		t.Fatalf("users=%v err=%v", users, err)
	}
	api.last(t, http.MethodGet, "/api/v1/services/mysql/users")

	created, err := c.CreateServiceUser(ctx, "mysql", databases.CreateUserRequest{Username: "app"})
	if err != nil || created.Username != "app" {
		t.Fatalf("created=%v err=%v", created, err)
	}
	create := api.last(t, http.MethodPost, "/api/v1/services/mysql/users")
	if !strings.Contains(create.body, `"username":"app"`) {
		t.Fatalf("create body = %s", create.body)
	}

	enabled := false
	updated, err := c.UpdateServiceUser(ctx, "mysql", "usr_app", databases.UpdateUserRequest{Enabled: &enabled})
	if err != nil || updated.ID != "usr_app" {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	update := api.last(t, http.MethodPatch, "/api/v1/services/mysql/users/usr_app")
	if !strings.Contains(update.body, `"enabled":false`) {
		t.Fatalf("update body = %s", update.body)
	}

	if err := c.DeleteServiceUser(ctx, "mysql", "usr_app", databases.DeleteUserRequest{Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodDelete, "/api/v1/services/mysql/users/usr_app")

	backups, err := c.ListServiceBackups(ctx, "mysql")
	if err != nil || len(backups) != 1 || backups[0].ID != "bkp_1" {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
	api.last(t, http.MethodGet, "/api/v1/services/mysql/backups")

	job, err := c.CreateServiceBackup(ctx, "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil || job.ID != "job_1" {
		t.Fatalf("job=%v err=%v", job, err)
	}
	api.last(t, http.MethodPost, "/api/v1/services/mysql/databases/app/backups")

	if _, err := c.RestoreServiceBackup(ctx, "mysql", "bkp_1", databases.RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodPost, "/api/v1/services/mysql/backups/bkp_1/restore")

	if err := c.DeleteServiceBackup(ctx, "mysql", "bkp_1"); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodDelete, "/api/v1/services/mysql/backups/bkp_1")

	gotJob, err := c.GetDatabaseJob(ctx, "job_1")
	if err != nil || gotJob.ID != "job_1" {
		t.Fatalf("job=%v err=%v", gotJob, err)
	}
	api.last(t, http.MethodGet, "/api/v1/jobs/job_1")

	if err := c.CancelDatabaseJob(ctx, "job_1"); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodDelete, "/api/v1/jobs/job_1")
}

func TestClientImportStreamsMultipart(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	job, err := c.ImportServiceDatabase(context.Background(), "mysql", "app", strings.NewReader("INSERT INTO t VALUES (1);\n"), "dump.sql", databases.ImportRequest{
		Mode: databases.ImportMerge, PreImportBackup: true,
	})
	if err != nil || job.ID != "job_1" {
		t.Fatalf("job=%v err=%v", job, err)
	}
	req := api.last(t, http.MethodPost, "/api/v1/services/mysql/databases/app/import")
	for _, want := range []string{`name="file"`, "filename=\"dump.sql\"", `name="mode"`, "merge", `name="pre_import_backup"`, "true", "INSERT INTO t"} {
		if !strings.Contains(req.body, want) {
			t.Fatalf("multipart body missing %q: %s", want, req.body)
		}
	}
}

func TestClientSiteDatabaseLinksAndEnv(t *testing.T) {
	home := t.TempDir()
	api := &fakeAPI{sites: map[string]model.Site{}}
	startFakeAPI(t, home, api)
	c, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	links, err := c.ListSiteDatabases(ctx, "blog")
	if err != nil || len(links) != 1 || links[0].ID != "lnk_1" {
		t.Fatalf("links=%v err=%v", links, err)
	}
	api.last(t, http.MethodGet, "/api/v1/sites/blog/databases")

	linked, err := c.LinkSiteDatabase(ctx, "blog", databases.LinkDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"})
	if err != nil || linked.ID != "lnk_1" {
		t.Fatalf("linked=%v err=%v", linked, err)
	}
	linkReq := api.last(t, http.MethodPost, "/api/v1/sites/blog/databases")
	if !strings.Contains(linkReq.body, `"user_id":"usr_app"`) {
		t.Fatalf("link body = %s", linkReq.body)
	}

	result, err := c.CreateSiteDatabase(ctx, "blog", databases.SiteDatabaseRequest{
		ServiceID:   "mysql",
		NewDatabase: &databases.CreateDatabaseRequest{Name: "app"},
		NewUser:     &databases.CreateUserRequest{Username: "app"},
	})
	if err != nil || result.Link.ID != "lnk_1" || result.Database.Name != "app" || result.User.ID != "usr_app" {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	if err := c.UnlinkSiteDatabase(ctx, "blog", "lnk_1"); err != nil {
		t.Fatal(err)
	}
	api.last(t, http.MethodDelete, "/api/v1/sites/blog/databases/lnk_1")

	env, err := c.SiteDatabaseEnv(ctx, "blog")
	if err != nil || env["DB_HOST"] != "127.0.0.1" || env["DB_DATABASE"] != "app" {
		t.Fatalf("env=%v err=%v", env, err)
	}
	api.last(t, http.MethodGet, "/api/v1/sites/blog/database-env")

	secret, err := c.SetServicePassword(ctx, "redis")
	if err != nil || secret.Value != "redis-secret" {
		t.Fatalf("secret=%v err=%v", secret, err)
	}
	api.last(t, http.MethodPost, "/api/v1/services/redis/password")
}
