package agentdocker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestCreateDatabaseUsesValidatedSQL(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	created, err := d.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "app" || created.ServiceID != "mysql" || created.Engine != model.DatabaseMySQL {
		t.Fatalf("created = %+v", created)
	}
	if !runner.stdinContains("CREATE DATABASE `app`") {
		t.Fatalf("SQL was not sent on stdin: %s", runner.joinedCalls())
	}
}

func TestCreateDatabaseRejectsInjectionBeforeAnyCommand(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	_, err := d.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app; DROP DATABASE mysql"})
	if err == nil {
		t.Fatal("CreateDatabase accepted an injected name")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("rejected create still invoked Docker: %v", runner.joinedCalls())
	}
}

func TestCreateDatabaseRejectsProtectedName(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	_, err := d.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "mysql"})
	if !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("err = %v, want ErrProtected", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("protected create invoked Docker: %v", runner.joinedCalls())
	}
}

func TestCreateUserPasswordIsNotInArgv(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	user, err := d.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app", Host: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if !user.PasswordManaged || !user.Managed || user.ID == "" {
		t.Fatalf("user = %+v", user)
	}
	if !runner.stdinContains("IDENTIFIED BY") {
		t.Fatal("password SQL not sent on stdin")
	}
	secret, err := d.readDatabaseSecret("mysql", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.args, " "), secret) {
			t.Fatalf("password appeared in argv: %v", call.args)
		}
		if strings.Contains(string(call.input), secret) && !strings.Contains(string(call.input), "IDENTIFIED BY") {
			t.Fatalf("password leaked into an unrelated stdin: %v", call.args)
		}
	}
	if secret == "" {
		t.Fatal("empty stored password")
	}
}

func TestCreateUserRejectsInjectionAndProtectedNames(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	if _, err := d.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app' --"}); err == nil {
		t.Fatal("accepted an injected username")
	}
	if _, err := d.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "root"}); !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("root create err = %v, want ErrProtected", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("rejected user create invoked Docker: %v", runner.joinedCalls())
	}
}

func TestRenameDatabaseMySQLUnsupported(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	_, err := d.RenameDatabase(context.Background(), "mysql", "app", databases.RenameDatabaseRequest{Name: "app2"})
	if !errors.Is(err, dbengine.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("MySQL rename invoked Docker: %v", runner.joinedCalls())
	}
}

func TestRenameDatabasePostgres(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	renamed, err := d.RenameDatabase(context.Background(), "postgres", "app", databases.RenameDatabaseRequest{Name: "app2"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "app2" || renamed.ServiceID != "postgres" {
		t.Fatalf("renamed = %+v", renamed)
	}
	if !runner.stdinContains(`ALTER DATABASE "app" RENAME TO "app2";`) {
		t.Fatalf("rename SQL = %s", runner.joinedCalls())
	}
}

func TestDropDatabaseRequiresConfirmAndTerminatesSessions(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["information_schema.PROCESSLIST"] = []byte("KILL 42;\n")
	if err := d.DropDatabase(context.Background(), "mysql", "app", databases.DropDatabaseRequest{Confirm: "wrong"}); err == nil {
		t.Fatal("DropDatabase accepted the wrong confirmation")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("rejected drop invoked Docker: %v", runner.joinedCalls())
	}
	if err := d.DropDatabase(context.Background(), "mysql", "app", databases.DropDatabaseRequest{Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("information_schema.PROCESSLIST") {
		t.Fatal("drop did not terminate sessions")
	}
	if !runner.stdinContains("DROP DATABASE `app`") {
		t.Fatalf("drop SQL missing: %s", runner.joinedCalls())
	}
}

func TestListDatabasesParsesEngineOutput(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["information_schema.SCHEMATA"] = []byte("app\t1024\t3\nblog\t0\t0\n")
	got, err := d.ListDatabases(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "app" || got[0].SizeBytes != 1024 || got[0].TableCount != 3 {
		t.Fatalf("databases = %+v", got)
	}
}

func TestListUsersAddsStableIDsAndGrants(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\nroot\tlocalhost\tY\n")
	runner.runOutputs["SHOW GRANTS"] = []byte("GRANT SELECT ON `app`.* TO `app`@`%`\n")
	got, err := d.ListUsers(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Username != "app" || got[0].Host != "%" {
		t.Fatalf("users = %+v", got)
	}
	if got[0].ID != databaseUserID("mysql", "app", "%") {
		t.Fatalf("user ID = %q", got[0].ID)
	}
	if len(got[0].Grants) != 1 || got[0].Grants[0].Database != "app" {
		t.Fatalf("grants = %+v", got[0].Grants)
	}
}

func TestRevealUserPassword(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	userID := databaseUserID("mysql", "app", "%")
	if err := d.writeDatabaseSecret("mysql", userID, "s3cret"); err != nil {
		t.Fatal(err)
	}
	secret, err := d.RevealUserPassword(context.Background(), "mysql", userID)
	if err != nil || secret.Value != "s3cret" {
		t.Fatalf("secret = %+v, %v", secret, err)
	}
	if _, err := d.RevealUserPassword(context.Background(), "mysql", databaseUserID("mysql", "missing", "%")); !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("missing secret err = %v", err)
	}
}

func TestSetUserPasswordRotatesSecret(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	userID := databaseUserID("mysql", "app", "%")
	if err := d.writeDatabaseSecret("mysql", userID, "old-secret"); err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\n")
	secret, err := d.SetUserPassword(context.Background(), "mysql", userID)
	if err != nil {
		t.Fatal(err)
	}
	if secret.Value == "" || secret.Value == "old-secret" {
		t.Fatalf("secret = %+v", secret)
	}
	stored, err := d.readDatabaseSecret("mysql", userID)
	if err != nil || stored != secret.Value {
		t.Fatalf("stored = %q, %v", stored, err)
	}
	if !runner.stdinContains("ALTER USER 'app'@'%' IDENTIFIED BY") {
		t.Fatal("ALTER USER was not sent on stdin")
	}
	for _, call := range runner.calls {
		if strings.Contains(strings.Join(call.args, " "), secret.Value) {
			t.Fatalf("password appeared in argv: %v", call.args)
		}
	}
}

func TestDeleteUserRejectsProtectedAndRequiresConfirm(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\nroot\tlocalhost\tY\n")
	rootID := databaseUserID("mysql", "root", "localhost")
	if err := d.DeleteUser(context.Background(), "mysql", rootID, databases.DeleteUserRequest{Confirm: "root"}); !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("root delete err = %v, want ErrProtected", err)
	}
	appID := databaseUserID("mysql", "app", "%")
	if err := d.DeleteUser(context.Background(), "mysql", appID, databases.DeleteUserRequest{Confirm: "wrong"}); err == nil {
		t.Fatal("DeleteUser accepted the wrong confirmation")
	}
	if err := d.DeleteUser(context.Background(), "mysql", appID, databases.DeleteUserRequest{Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("DROP USER 'app'@'%'") {
		t.Fatalf("drop user SQL missing: %s", runner.joinedCalls())
	}
}

func TestUpdateUserAppliesGrantsAndLock(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	userID := databaseUserID("mysql", "app", "%")
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\n")
	runner.runOutputs["SHOW GRANTS"] = []byte("GRANT SELECT ON `app`.* TO `app`@`%`\n")
	enabled := false
	updated, err := d.UpdateUser(context.Background(), "mysql", userID, databases.UpdateUserRequest{
		Enabled: &enabled,
		Grants:  []model.DatabaseGrant{{Database: "app", Preset: model.GrantReadWrite}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != userID {
		t.Fatalf("updated = %+v", updated)
	}
	if !runner.stdinContains("ACCOUNT LOCK") {
		t.Fatal("enabled=false did not lock the account")
	}
	if !runner.stdinContains("GRANT SELECT, INSERT, UPDATE, DELETE ON `app`.*") {
		t.Fatalf("grant SQL missing: %s", runner.joinedCalls())
	}
}

func TestPostgresGrantsAndRevokesUseTargetDatabase(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	grants := []model.DatabaseGrant{
		{Database: "app_ro", Preset: model.GrantReadOnly},
		{Database: "app_rw", Preset: model.GrantReadWrite},
		{Database: "app_rws", Preset: model.GrantReadWriteSchema},
		{Database: "app_admin", Preset: model.GrantAdmin},
		{Database: "app_custom", Preset: model.GrantCustom, Privileges: []string{"CONNECT", "SELECT"}},
	}
	if _, err := d.CreateUser(context.Background(), "postgres", databases.CreateUserRequest{
		Username: "app", Grants: grants,
	}); err != nil {
		t.Fatal(err)
	}
	assertPostgresStatementsTargetDatabase(t, runner, "GRANT ", grants)

	// Revokes must also run in each target database, not in the postgres
	// maintenance database.
	runner.runOutputs["pg_roles"] = []byte("app\tt\tf\n")
	runner.runOutputs["information_schema.role_table_grants"] = []byte("app_ro\tSELECT\napp_ro\tCONNECT\napp_rw\tCONNECT\n")
	runner.calls = nil
	if _, err := d.UpdateUser(context.Background(), "postgres", databaseUserID("postgres", "app", ""), databases.UpdateUserRequest{
		Grants: []model.DatabaseGrant{},
	}); err != nil {
		t.Fatal(err)
	}
	assertPostgresStatementsTargetDatabase(t, runner, "REVOKE ", []model.DatabaseGrant{
		{Database: "app_ro", Preset: model.GrantAdmin},
		{Database: "app_rw", Preset: model.GrantAdmin},
	})
}

func assertPostgresStatementsTargetDatabase(t *testing.T, runner *fakeDatabaseRunner, verb string, grants []model.DatabaseGrant) {
	t.Helper()
	seen := make(map[string]bool)
	for _, call := range runner.calls {
		input := string(call.input)
		if !strings.Contains(input, verb) {
			continue
		}
		joined := strings.Join(call.args, " ")
		database := postgresDatabaseFromSQL(t, input)
		if database == "" {
			t.Fatalf("%s statement has no target database:\n%s", verb, input)
		}
		if !strings.Contains(joined, "--dbname="+database) {
			t.Fatalf("%s for %s did not target --dbname=%s: %v", verb, database, database, call.args)
		}
		if strings.Contains(joined, "--dbname=postgres") {
			t.Fatalf("%s for %s ran against postgres: %v", verb, database, call.args)
		}
		seen[database] = true
	}
	for _, grant := range grants {
		if !seen[grant.Database] {
			t.Fatalf("%s for %s was not executed (seen=%v)", verb, grant.Database, seen)
		}
	}
}

func postgresDatabaseFromSQL(t *testing.T, sql string) string {
	t.Helper()
	match := regexp.MustCompile(`ON DATABASE "([^"]+)"`).FindStringSubmatch(sql)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func TestPostgresEnableDisableSupportsDottedAndDashedRoles(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["pg_roles"] = []byte("app-user\tf\tf\napp.user\tt\tf\n")
	enabled := true
	if _, err := d.UpdateUser(context.Background(), "postgres", databaseUserID("postgres", "app-user", ""), databases.UpdateUserRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains(`ALTER ROLE "app-user" WITH LOGIN;`) {
		t.Fatalf("dashed role enable SQL missing: %s", runner.joinedCalls())
	}
	disabled := false
	if _, err := d.UpdateUser(context.Background(), "postgres", databaseUserID("postgres", "app.user", ""), databases.UpdateUserRequest{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains(`ALTER ROLE "app.user" WITH NOLOGIN;`) {
		t.Fatalf("dotted role disable SQL missing: %s", runner.joinedCalls())
	}
}

func TestSetUserPasswordRejectsProtected(t *testing.T) {
	d, _ := newDatabaseTestDocker(t)
	rootID := databaseUserID("mysql", "root", "localhost")
	if _, err := d.SetUserPassword(context.Background(), "mysql", rootID); !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("root password reset err = %v, want ErrProtected", err)
	}
}

func TestCreateUserRollsBackOnGrantFailure(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.failOnInput = "GRANT "
	_, err := d.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{
		Username: "app",
		Host:     "%",
		Grants:   []model.DatabaseGrant{{Database: "app", Preset: model.GrantReadOnly}},
	})
	if err == nil {
		t.Fatal("CreateUser ignored a grant failure")
	}
	path := filepath.Join(d.root, "services", "mysql", "secrets", "database-users", databaseUserID("mysql", "app", "%"))
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed create left a secret at %s: %v", path, statErr)
	}
}

func TestListUsersSupportsDottedAndDashedNames(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["mysql.user"] = []byte("app-user\t%\tN\napp.user\t%\tN\n")
	runner.runOutputs["SHOW GRANTS"] = []byte("GRANT SELECT ON `app`.* TO `app-user`@`%`\n")
	users, err := d.ListUsers(context.Background(), "mysql")
	if err != nil || len(users) != 2 {
		t.Fatalf("users = %+v, %v", users, err)
	}
	enabled := false
	if _, err := d.UpdateUser(context.Background(), "mysql", databaseUserID("mysql", "app-user", "%"), databases.UpdateUserRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("ALTER USER 'app-user'@'%' ACCOUNT LOCK") {
		t.Fatalf("dashed user update SQL missing: %s", runner.joinedCalls())
	}
	if err := d.DeleteUser(context.Background(), "mysql", databaseUserID("mysql", "app.user", "%"), databases.DeleteUserRequest{Confirm: "app.user"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("DROP USER 'app.user'@'%'") {
		t.Fatalf("dotted user delete SQL missing: %s", runner.joinedCalls())
	}
}

func TestListUsersPropagatesGrantErrors(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\n")
	runner.failOnArgs = "SHOW GRANTS"
	if _, err := d.ListUsers(context.Background(), "mysql"); err == nil {
		t.Fatal("ListUsers swallowed a grant discovery failure")
	}
}

func TestUpdateUserRollsBackGrantsOnPartialFailure(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	userID := databaseUserID("mysql", "app", "%")
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\n")
	runner.runOutputs["SHOW GRANTS"] = []byte("GRANT SELECT ON `app`.* TO `app`@`%`\n")
	runner.failOnInput = "GRANT SELECT ON `blog`"
	_, err := d.UpdateUser(context.Background(), "mysql", userID, databases.UpdateUserRequest{
		Grants: []model.DatabaseGrant{
			{Database: "app", Preset: model.GrantReadWrite},
			{Database: "blog", Preset: model.GrantReadOnly},
		},
	})
	if err == nil {
		t.Fatal("UpdateUser ignored a partial grant failure")
	}
	if !runner.stdinContains("GRANT SELECT ON `app`.*") {
		t.Fatalf("existing app grant was not restored: %s", runner.joinedCalls())
	}
}

func TestSetUserPasswordDeletesStaleSecretOnWriteFailure(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	userID := databaseUserID("mysql", "app", "%")
	if err := d.writeDatabaseSecret("mysql", userID, "old-secret"); err != nil {
		t.Fatal(err)
	}
	runner.runOutputs["mysql.user"] = []byte("app\t%\tN\n")
	d.secretWriter = func(string, string, string) error { return errors.New("disk full") }
	if _, err := d.SetUserPassword(context.Background(), "mysql", userID); err == nil {
		t.Fatal("SetUserPassword ignored a secret write failure")
	}
	if _, err := d.readDatabaseSecret("mysql", userID); !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("stale secret is still revealable: %v", err)
	}
}

func TestCreateUserRollsBackOnSecretWriteFailure(t *testing.T) {
	d, runner := newDatabaseTestDocker(t)
	d.secretWriter = func(string, string, string) error { return errors.New("disk full") }
	if _, err := d.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app", Host: "%"}); err == nil {
		t.Fatal("CreateUser ignored a secret write failure")
	}
	if !runner.stdinContains("DROP USER 'app'@'%'") {
		t.Fatalf("failed create did not drop the engine user: %s", runner.joinedCalls())
	}
	if _, err := d.readDatabaseSecret("mysql", databaseUserID("mysql", "app", "%")); !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("failed create left a secret: %v", err)
	}
}
