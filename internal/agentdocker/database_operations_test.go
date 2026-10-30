package agentdocker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
