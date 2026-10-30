package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/client"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestSiteAddDatabasePayload(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	runCLI(t, fake, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static",
		"--database-service", "mysql", "--database-new", "blog",
		"--database-user-new", "blog_user", "--database-grant", "read_write_schema")
	if fake.addReq.Database == nil {
		t.Fatal("database payload missing")
	}
	if fake.addReq.Database.ServiceID != "mysql" {
		t.Fatalf("service=%q", fake.addReq.Database.ServiceID)
	}
	if fake.addReq.Database.NewDatabase == nil || fake.addReq.Database.NewDatabase.Name != "blog" {
		t.Fatalf("db=%+v", fake.addReq.Database)
	}
	if fake.addReq.Database.NewUser == nil || fake.addReq.Database.NewUser.Username != "blog_user" {
		t.Fatalf("user=%+v", fake.addReq.Database)
	}
	if fake.addReq.Database.Grant != model.GrantReadWriteSchema {
		t.Fatalf("grant=%q", fake.addReq.Database.Grant)
	}
	if !fake.addReq.Database.Primary {
		t.Fatal("site add database link should be primary")
	}
}

func TestSiteAddExistingUserDoesNotGrant(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	runCLI(t, fake, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static",
		"--database-service", "mysql", "--database", "app", "--database-user", "usr_app")
	if fake.addReq.Database == nil {
		t.Fatal("database payload missing")
	}
	if fake.addReq.Database.Grant != "" {
		t.Fatalf("auto-granted %q", fake.addReq.Database.Grant)
	}
	if fake.addReq.Database.NewUser != nil {
		t.Fatal("created a user for an existing user selection")
	}
	if fake.addReq.Database.Database != "app" || fake.addReq.Database.UserID != "usr_app" {
		t.Fatalf("database=%+v", fake.addReq.Database)
	}
}

func TestSiteAddExternalPasswordStdinUnsupported(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	reader := &trackingReader{data: "s3cret\n"}
	code, stdout, stderr := runCLIExitReader(t, fake, reader, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static",
		"--database-service", "mysql", "--database", "app", "--database-user", "usr_app", "--database-user-password-stdin")
	if code == 0 || !strings.Contains(stderr, "DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if reader.read {
		t.Fatal("external password stdin was read")
	}
	if strings.Contains(stdout, "s3cret") || strings.Contains(stderr, "s3cret") {
		t.Fatalf("piped password leaked: stdout=%s stderr=%s", stdout, stderr)
	}
	if fake.addReq.Name != "" || fake.lastHome != "" {
		t.Fatal("site add called the daemon for an unsupported external password")
	}
}

func TestSiteAddExplicitFalsePasswordStdinDoesNotRead(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	reader := &trackingReader{data: "s3cret\n"}
	code, stdout, stderr := runCLIExitReader(t, fake, reader, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static",
		"--database-service", "mysql", "--database", "app", "--database-user", "usr_app", "--database-user-password-stdin=false")
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if reader.read {
		t.Fatal("explicitly false password-stdin flag still read stdin")
	}
	if fake.addReq.Database == nil || fake.addReq.Database.Password != "" {
		t.Fatalf("database=%+v", fake.addReq.Database)
	}
}

func TestSiteAddMetadataOnlyDoesNotPromptForPassword(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, stdout, stderr := runCLIExitInput(t, fake, "", "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static",
		"--database-service", "mysql", "--database", "app", "--database-user", "usr_app")
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if fake.addReq.Database == nil || fake.addReq.Database.Password != "" {
		t.Fatalf("database=%+v", fake.addReq.Database)
	}
	if strings.Contains(stdout, "assword") {
		t.Fatalf("metadata-only add prompted for a password: %s", stdout)
	}
}

func TestSiteAddDatabaseFlagsRequireService(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static", "--database", "app")
	if code == 0 || !strings.Contains(stderr, "database-service") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.addReq.Name != "" {
		t.Fatal("AddSite API called with an invalid database selection")
	}
}

func TestSiteAddExistingUserRejectsGrant(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static",
		"--database-service", "mysql", "--database", "app", "--database-user", "usr_app", "--database-grant", "read_write_schema")
	if code == 0 || !strings.Contains(stderr, "grant") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.addReq.Name != "" {
		t.Fatal("AddSite API called with an auto-grant for an existing user")
	}
}

func TestSiteAddDatabaseWarningPrintedToStderr(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{err: &client.Error{
		Code:    "SITE_CREATE_FAILED",
		Message: "site creation failed",
		Details: map[string]any{"warning": "database blog was created and remains available"},
	}}
	code, _, stderr := runCLIExit(t, fake, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "static",
		"--database-service", "mysql", "--database-new", "blog", "--database-user-new", "blog_user")
	if code == 0 {
		t.Fatal("site add should preserve the API failure exit code")
	}
	if !strings.Contains(stderr, "database blog was created") {
		t.Fatalf("warning missing from stderr: %s", stderr)
	}
}

func TestSiteDBListJSON(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseLinks: []model.DatabaseLink{{
		ID: "lnk_1", SiteID: "site-1", SiteName: "blog", ServiceID: "mysql", Engine: model.DatabaseMySQL,
		Database: "app", UserID: "usr_app", Username: "app", Primary: true, PasswordManaged: true, Available: true,
		Connection: model.DatabaseConnection{Engine: model.DatabaseMySQL, Host: "127.0.0.1", Port: 3306, Database: "app", Username: "app", PasswordManaged: true},
	}}}
	out := runCLI(t, fake, "site", "db", "list", "blog", "--json")
	var got struct {
		Links []model.DatabaseLink `json:"links"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 1 || got.Links[0].ID != "lnk_1" {
		t.Fatalf("links=%+v", got.Links)
	}
	if strings.Contains(out, "s3cret") {
		t.Fatalf("link list leaked a secret: %s", out)
	}
}

func TestSiteDBListJSONRedactsConnectionPasswords(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseLinks: []model.DatabaseLink{{
		ID: "lnk_1", SiteID: "site-1", ServiceID: "mysql", Engine: model.DatabaseMySQL,
		Database: "app", Username: "app", PasswordManaged: true,
		Connection: model.DatabaseConnection{
			Engine: model.DatabaseMySQL, Host: "127.0.0.1", Port: 3306, Database: "app", Username: "app",
			PasswordManaged: true,
			URL:             "mysql://app:s3cret@127.0.0.1:3306/app",
			Env: map[string]string{
				"DB_PASSWORD":      "s3cret",
				"DATABASE_URL":     "mysql://app:s3cret@127.0.0.1:3306/app",
				"DB_HOST":          "127.0.0.1",
				"DB_BLOG_PASSWORD": "blog-secret",
				"DB_BLOG_URL":      "mysql://blog:blog-secret@127.0.0.1:3306/blog",
			},
		},
	}}}
	out := runCLI(t, fake, "site", "db", "list", "blog", "--json")
	if strings.Contains(out, "s3cret") || strings.Contains(out, "blog-secret") {
		t.Fatalf("link list leaked a connection password: %s", out)
	}
	if !strings.Contains(out, "mysql://app@127.0.0.1:3306/app") || !strings.Contains(out, "mysql://blog@127.0.0.1:3306/blog") {
		t.Fatalf("link list did not keep redacted URLs: %s", out)
	}
	if strings.Contains(out, "DB_PASSWORD") || strings.Contains(out, "DB_BLOG_PASSWORD") {
		t.Fatalf("link list leaked a password env key: %s", out)
	}
}

func TestSiteDBListHumanShowsLinkID(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseLinks: []model.DatabaseLink{{
		ID: "lnk_1", SiteID: "site-1", SiteName: "blog", ServiceID: "mysql", Engine: model.DatabaseMySQL,
		Database: "app", Username: "app", Primary: true, PasswordManaged: true, Available: true,
		Connection: model.DatabaseConnection{Engine: model.DatabaseMySQL, Host: "127.0.0.1", Port: 3306, Database: "app", Username: "app"},
	}}}
	out := runCLI(t, fake, "site", "db", "list", "blog")
	if !strings.Contains(out, "LINK") {
		t.Fatalf("link column missing: %s", out)
	}
	if !strings.Contains(out, "lnk_1") {
		t.Fatalf("link id missing: %s", out)
	}
}

func TestSiteDBLinkPayload(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{databaseLinked: model.DatabaseLink{ID: "lnk_1", Database: "app"}}
	runCLI(t, fake, "site", "db", "link", "blog", "--service", "mysql", "--database", "app", "--user-id", "usr_app", "--primary")
	if fake.databaseLinkReq.ServiceID != "mysql" || fake.databaseLinkReq.Database != "app" || fake.databaseLinkReq.UserID != "usr_app" || !fake.databaseLinkReq.Primary {
		t.Fatalf("link req=%+v", fake.databaseLinkReq)
	}
}

func TestSiteDBLinkExternalPasswordStdinUnsupported(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	reader := &trackingReader{data: "s3cret\n"}
	code, stdout, stderr := runCLIExitReader(t, fake, reader, "site", "db", "link", "blog", "--service", "mysql", "--database", "app", "--user-id", "usr_app", "--password-stdin")
	if code == 0 || !strings.Contains(stderr, "DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if reader.read {
		t.Fatal("external password stdin was read")
	}
	if strings.Contains(stdout, "s3cret") || strings.Contains(stderr, "s3cret") {
		t.Fatalf("piped password leaked: stdout=%s stderr=%s", stdout, stderr)
	}
	if fake.databaseLinkReq.UserID != "" || fake.lastHome != "" {
		t.Fatal("site db link called the daemon for an unsupported external password")
	}
}

func TestSiteDBUnlinkRequiresYes(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{}
	code, _, stderr := runCLIExit(t, fake, "site", "db", "unlink", "blog", "link_1")
	if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if fake.siteDatabaseUnlinked {
		t.Fatal("unlink API called without --yes")
	}
	runCLI(t, fake, "site", "db", "unlink", "blog", "link_1", "--yes")
	if !fake.siteDatabaseUnlinked || fake.siteDatabaseUnlinkRef != "blog" || fake.siteDatabaseUnlinkID != "link_1" {
		t.Fatalf("unlink=%v ref=%q id=%q", fake.siteDatabaseUnlinked, fake.siteDatabaseUnlinkRef, fake.siteDatabaseUnlinkID)
	}
}

func shellContextSite() model.Site {
	return model.Site{
		ID: "site-1", Name: "blog", Kind: model.SiteStatic, HostPath: "/home/aram/blog", BackendPath: "/home/aram/blog",
		NodeToolchain: &model.NodeToolchain{
			Version: "22.11.0", BuildID: "node@22.11.0", Source: model.NodeSourceGlobal, Installed: true,
		},
	}
}

func shellContextRuntimes() []model.RuntimeBuild {
	return []model.RuntimeBuild{{ID: "node@22.11.0", Kind: "node", Version: "22.11.0"}}
}

func TestSiteShellContextDegradesWhenDatabaseEnvUnavailable(t *testing.T) {
	useTestSeams(t)
	for _, code := range []string{"NOT_FOUND", "DATABASE_PASSWORD_MISSING", "DATABASE_SERVICE_INACTIVE"} {
		t.Run(code, func(t *testing.T) {
			fake := &fakeClient{
				sites:              []model.Site{shellContextSite()},
				runtimes:           shellContextRuntimes(),
				siteDatabaseEnvErr: &client.Error{Code: code, Message: "database environment unavailable"},
			}
			app := &App{home: t.TempDir()}
			var warn strings.Builder
			_, env, _, err := app.siteShellContext(context.Background(), fake, "blog", &warn)
			if err != nil {
				t.Fatalf("siteShellContext returned an error for %s: %v", code, err)
			}
			if env.Values["DEVSTACK_SITE"] != "blog" {
				t.Fatalf("base environment missing: %v", env.Values)
			}
			if _, ok := env.Values["DB_HOST"]; ok {
				t.Fatalf("database env leaked into degraded environment: %v", env.Values)
			}
			if !strings.Contains(warn.String(), code) {
				t.Fatalf("warning missing %s: %q", code, warn.String())
			}
		})
	}
}

func TestSiteShellContextPropagatesUnexpectedDatabaseEnvErrors(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		sites:              []model.Site{shellContextSite()},
		runtimes:           shellContextRuntimes(),
		siteDatabaseEnvErr: &client.Error{Code: "INTERNAL", Message: "unexpected failure"},
	}
	app := &App{home: t.TempDir()}
	if _, _, _, err := app.siteShellContext(context.Background(), fake, "blog", new(strings.Builder)); err == nil {
		t.Fatal("unexpected database env error was swallowed")
	}
}

func TestSiteShellContextMergesDatabaseEnv(t *testing.T) {
	useTestSeams(t)
	fake := &fakeClient{
		sites:    []model.Site{shellContextSite()},
		runtimes: shellContextRuntimes(),
		siteDatabaseEnv: map[string]string{
			"DB_HOST": "127.0.0.1", "DB_DATABASE": "app", "DB_PASSWORD": "s3cret",
		},
	}
	app := &App{home: t.TempDir()}
	_, env, _, err := app.siteShellContext(context.Background(), fake, "blog", new(strings.Builder))
	if err != nil {
		t.Fatal(err)
	}
	if env.Values["DB_HOST"] != "127.0.0.1" || env.Values["DB_DATABASE"] != "app" || env.Values["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("env=%v", env.Values)
	}
}
