//go:build !windows

package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeCommandRunner struct {
	mu           sync.Mutex
	calls        [][]string
	stdin        [][]byte
	streamOutput []byte
	psState      string
	handle       func(name string, args []string) ([]byte, error)
	handleInput  func(name string, args []string, input []byte) ([]byte, error)
}

func (f *fakeCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	handle := f.handle
	f.mu.Unlock()
	if handle != nil {
		return handle(name, args)
	}
	return f.defaultRun(name, args)
}

func (f *fakeCommandRunner) RunInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.stdin = append(f.stdin, append([]byte(nil), input...))
	handle := f.handleInput
	f.mu.Unlock()
	if handle != nil {
		return handle(name, args, input)
	}
	return f.defaultInput(name, args, input)
}

func (f *fakeCommandRunner) RunInputStream(ctx context.Context, input io.Reader, name string, args ...string) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	_, err = f.RunInput(ctx, data, name, args...)
	return err
}

func (f *fakeCommandRunner) RunStream(ctx context.Context, name string, args ...string) (io.ReadCloser, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	output := append([]byte(nil), f.streamOutput...)
	f.mu.Unlock()
	return io.NopCloser(bytes.NewReader(output)), nil
}

func (f *fakeCommandRunner) defaultRun(name string, args []string) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, " ps "):
		state := f.psState
		if state == "" {
			state = "running"
		}
		return []byte(`[{"Service":"mysql","State":"` + state + `","Health":"healthy"},{"Service":"mariadb","State":"` + state + `","Health":"healthy"},{"Service":"postgresql","State":"` + state + `","Health":"healthy"},{"Service":"redis","State":"` + state + `","Health":"healthy"}]`), nil
	case strings.Contains(joined, "list-databases"):
		return []byte("app\t1024\t2\nblog\t2048\t3\n"), nil
	case strings.Contains(joined, "list-users"):
		return []byte("usr_app\tapp\t%\n"), nil
	case strings.Contains(joined, "list-grants"):
		return []byte("app\tread_write_schema\n"), nil
	case strings.Contains(joined, "ACL LIST"):
		return []byte("user default on nopass ~* &* +@all\nuser cache on #hash ~* +@read -@admin -@dangerous\n"), nil
	case strings.Contains(joined, "restart"):
		return []byte("ok"), nil
	default:
		return []byte("ok"), nil
	}
}

func (f *fakeCommandRunner) defaultInput(name string, args []string, input []byte) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "ACL LIST"):
		return []byte("user default on nopass ~* &* +@all\nuser cache on #hash ~* +@read -@admin -@dangerous\n"), nil
	default:
		return []byte("OK"), nil
	}
}

func (f *fakeCommandRunner) snapshot() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	for i := range f.calls {
		out[i] = append([]string(nil), f.calls[i]...)
	}
	return out
}

func (f *fakeCommandRunner) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
	f.stdin = nil
}

func (f *fakeCommandRunner) joinedCalls() string {
	var parts []string
	for _, call := range f.snapshot() {
		parts = append(parts, strings.Join(call, " "))
	}
	return strings.Join(parts, "\n")
}

func (f *fakeCommandRunner) joinedCallsContains(value string) bool {
	return strings.Contains(f.joinedCalls(), value)
}

func (f *fakeCommandRunner) contains(value string) bool {
	return f.joinedCallsContains(value)
}

func (f *fakeCommandRunner) stdinContains(value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, input := range f.stdin {
		if strings.Contains(string(input), value) {
			return true
		}
	}
	return false
}

type fakeDatabaseEngine struct {
	id     model.DatabaseEngine
	rename bool
}

func (e *fakeDatabaseEngine) ID() model.DatabaseEngine { return e.id }

func (e *fakeDatabaseEngine) ValidateDatabaseName(name string) error {
	return dbengine.ValidateIdentifier(name)
}

func (e *fakeDatabaseEngine) ValidateUsername(name string) error {
	return dbengine.ValidateIdentifier(name)
}

func (e *fakeDatabaseEngine) ValidateHost(host string) error {
	if host == "%" || host == "" {
		return nil
	}
	return dbengine.ValidateIdentifier(host)
}

func (e *fakeDatabaseEngine) ProtectedDatabase(name string) bool {
	return dbengine.SystemDatabases[e.id][name]
}

func (e *fakeDatabaseEngine) ProtectedUser(username, host string) bool {
	return dbengine.SystemUsers[e.id][username]
}

func (e *fakeDatabaseEngine) RenameSupported() bool { return e.rename }

func (e *fakeDatabaseEngine) ListDatabasesCommand() ([]string, error) {
	return []string{"list-databases"}, nil
}

func (e *fakeDatabaseEngine) ParseDatabases(data []byte) ([]model.Database, error) {
	var out []model.Database
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 0 || fields[0] == "" {
			continue
		}
		out = append(out, model.Database{Name: fields[0]})
	}
	return out, nil
}

func (e *fakeDatabaseEngine) CreateDatabaseSQL(name, charset, collation string) (string, error) {
	quoted, err := dbengine.QuoteIdentifier(e.id, name)
	if err != nil {
		return "", err
	}
	return "CREATE DATABASE " + quoted, nil
}

func (e *fakeDatabaseEngine) DropDatabaseSQL(name string) (string, error) {
	quoted, err := dbengine.QuoteIdentifier(e.id, name)
	if err != nil {
		return "", err
	}
	return "DROP DATABASE " + quoted, nil
}

func (e *fakeDatabaseEngine) RenameDatabaseSQL(name, newName string) (string, error) {
	if !e.rename {
		return "", dbengine.ErrUnsupported
	}
	oldQuoted, err := dbengine.QuoteIdentifier(e.id, name)
	if err != nil {
		return "", err
	}
	newQuoted, err := dbengine.QuoteIdentifier(e.id, newName)
	if err != nil {
		return "", err
	}
	return "ALTER DATABASE " + oldQuoted + " RENAME TO " + newQuoted, nil
}

func (e *fakeDatabaseEngine) ListUsersCommand() ([]string, error) {
	return []string{"list-users"}, nil
}

func (e *fakeDatabaseEngine) ParseUsers(data []byte) ([]model.DatabaseUser, error) {
	var out []model.DatabaseUser
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || fields[0] == "" {
			continue
		}
		out = append(out, model.DatabaseUser{ID: fields[0], Username: fields[1], Host: fields[2], Enabled: true})
	}
	return out, nil
}

func (e *fakeDatabaseEngine) ListGrantsCommand(username, host string) ([]string, error) {
	return []string{"list-grants"}, nil
}

func (e *fakeDatabaseEngine) ParseGrants(data []byte) ([]model.DatabaseGrant, error) {
	var out []model.DatabaseGrant
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		out = append(out, model.DatabaseGrant{Database: fields[0], Preset: model.DatabaseGrantPreset(fields[1])})
	}
	return out, nil
}

func (e *fakeDatabaseEngine) CreateUserSQL(username, host, password string) (string, error) {
	return "CREATE USER " + dbengine.QuoteLiteral(username) + " IDENTIFIED BY " + dbengine.QuoteLiteral(password), nil
}

func (e *fakeDatabaseEngine) SetPasswordSQL(username, host, password string) (string, error) {
	return "ALTER USER " + dbengine.QuoteLiteral(username) + " IDENTIFIED BY " + dbengine.QuoteLiteral(password), nil
}

func (e *fakeDatabaseEngine) DeleteUserSQL(username, host string) (string, error) {
	return "DROP USER " + dbengine.QuoteLiteral(username), nil
}

func (e *fakeDatabaseEngine) GrantSQL(username, host, database string, grant model.DatabaseGrant) (string, error) {
	return "GRANT " + string(grant.Preset) + " ON " + database, nil
}

func (e *fakeDatabaseEngine) RevokeSQL(username, host, database string, grant model.DatabaseGrant) (string, error) {
	return "REVOKE " + string(grant.Preset) + " ON " + database, nil
}

func (e *fakeDatabaseEngine) TerminateSessionsSQL(database string) (string, error) {
	return "KILL SESSIONS " + database, nil
}

func (e *fakeDatabaseEngine) DumpCommand(database string) ([]string, error) {
	return []string{"dump", database}, nil
}

func (e *fakeDatabaseEngine) RestoreCommand(database string) ([]string, error) {
	return []string{"restore", database}, nil
}

type fakeRegistry struct {
	engines map[model.DatabaseEngine]dbengine.Engine
}

func (r fakeRegistry) For(engine model.DatabaseEngine) (dbengine.Engine, error) {
	if found, ok := r.engines[engine]; ok {
		return found, nil
	}
	return nil, dbengine.ErrUnsupported
}

func databaseHelperFixture(t *testing.T) (*DockerServiceController, string) {
	t.Helper()
	root := t.TempDir()
	runner := &fakeCommandRunner{streamOutput: []byte("CREATE TABLE t (id int);\n")}
	ctrl := NewDockerServiceController(root, services.DefaultCatalog(), runner)
	ctrl.Now = func() time.Time {
		return time.Date(2026, 10, 30, 12, 0, 0, 123456789, time.UTC)
	}
	ctrl.databaseRegistry = fakeRegistry{engines: map[model.DatabaseEngine]dbengine.Engine{
		model.DatabaseMySQL:      &fakeDatabaseEngine{id: model.DatabaseMySQL},
		model.DatabaseMariaDB:    &fakeDatabaseEngine{id: model.DatabaseMariaDB},
		model.DatabasePostgreSQL: &fakeDatabaseEngine{id: model.DatabasePostgreSQL, rename: true},
	}}
	return ctrl, root
}

func TestHelperDatabaseSecretIsPrivate(t *testing.T) {
	c, root := databaseHelperFixture(t)
	if err := c.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "services", "mysql", "secrets", "database-users", "usr_app")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode(), err)
	}
}

func TestHelperDatabaseSecretRejectsTraversal(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	for _, userID := range []string{"../root", "..", "a/b", ""} {
		if err := c.writeDatabaseSecret("mysql", userID, "x"); err == nil {
			t.Fatalf("accepted traversal user ID %q", userID)
		}
	}
}

func TestHelperUsesItsOwnComposeProject(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	if _, err := c.ListDatabases(context.Background(), "mysql"); err != nil {
		t.Fatal(err)
	}
	joined := runner.joinedCalls()
	if !strings.Contains(joined, "devstack-") || strings.Contains(joined, "--project-name devstack ") {
		t.Fatalf("compose calls = %s", joined)
	}
}

func TestHelperCreateDatabaseUsesValidatedSQL(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	if _, err := c.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("CREATE DATABASE `app`") {
		t.Fatalf("calls=%v stdin=%q", runner.snapshot(), runner.stdin)
	}
}

func TestHelperCreateDatabaseRejectsInjection(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	_, err := c.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app; DROP DATABASE mysql"})
	if !errors.Is(err, databases.ErrInvalidRequest) || !errors.Is(err, dbengine.ErrInvalidIdentifier) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperRenameRejectsMySQL(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	_, err := c.RenameDatabase(context.Background(), "mysql", "app", databases.RenameDatabaseRequest{Name: "app2"})
	if !errors.Is(err, dbengine.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperRenamePostgreSQL(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	if _, err := c.RenameDatabase(context.Background(), "postgresql", "app", databases.RenameDatabaseRequest{Name: "app2"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains(`ALTER DATABASE "app" RENAME TO "app2"`) {
		t.Fatalf("stdin=%q", runner.stdin)
	}
}

func TestHelperCreateUserPasswordOnStdin(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	user, err := c.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app", Host: "%"})
	if err != nil {
		t.Fatal(err)
	}
	if !user.PasswordManaged {
		t.Fatalf("user=%+v", user)
	}
	if !runner.stdinContains("IDENTIFIED BY") {
		t.Fatal("password SQL not on stdin")
	}
	if runner.joinedCallsContains("s3cret") {
		t.Fatal("password in argv")
	}
	if _, err := c.readDatabaseSecret("mysql", user.ID); err != nil {
		t.Fatalf("stored secret: %v", err)
	}
}

func TestHelperDropRequiresConfirmation(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	err := c.DropDatabase(context.Background(), "mysql", "app", databases.DropDatabaseRequest{Confirm: "wrong"})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperDropProtectsDevstack(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	err := c.DropDatabase(context.Background(), "mysql", "devstack", databases.DropDatabaseRequest{Confirm: "devstack"})
	if !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperRevealReadsSecret(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	if err := c.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	secret, err := c.RevealUserPassword(context.Background(), "mysql", "usr_app")
	if err != nil || secret.Value != "s3cret" {
		t.Fatalf("secret=%+v err=%v", secret, err)
	}
}

func TestHelperCreateBackupWritesGzip(t *testing.T) {
	c, root := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("CREATE TABLE t (id int);\n")
	job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "backups", "databases", "mysql", "app", "*.sql.gz"))
	if len(matches) != 1 {
		t.Fatalf("backups=%v", matches)
	}
	file, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gz)
	if err != nil || string(data) != "CREATE TABLE t (id int);\n" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	list, err := c.ListBackups(context.Background(), "mysql")
	if err != nil || len(list) != 1 || list[0].ID != job.BackupID {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	download, err := c.DownloadBackup(context.Background(), "mysql", job.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(download); err != nil {
		t.Fatal(err)
	}
	_ = download.Close()
	if err := c.DeleteBackup(context.Background(), "mysql", job.BackupID); err != nil {
		t.Fatal(err)
	}
	if list, _ := c.ListBackups(context.Background(), "mysql"); len(list) != 0 {
		t.Fatalf("backup was not deleted: %+v", list)
	}
}

func TestHelperImportStagingIsDeleted(t *testing.T) {
	c, root := databaseHelperFixture(t)
	_, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "app.sql", databases.ImportRequest{Mode: databases.ImportMerge})
	if err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "backups", "databases", "mysql", ".staging", "*"))
	if len(matches) != 0 {
		t.Fatalf("staging left behind: %v", matches)
	}
}

func TestHelperImportReplaceRequiresConfirmation(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	_, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "app.sql", databases.ImportRequest{Mode: databases.ImportReplace})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
}

func TestHelperRestoreOverwriteRequiresConfirmation(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	runner.reset()
	_, err = c.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{
		Target: "app", Overwrite: true, Confirm: "wrong",
	})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if runner.joinedCallsContains("DROP DATABASE") {
		t.Fatalf("drop ran without confirmation: %s", runner.joinedCalls())
	}
}

func TestHelperImportReplaceDropsAndCreates(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	_, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "app.sql", databases.ImportRequest{
		Mode: databases.ImportReplace, Confirm: "app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("DROP DATABASE `app`") || !runner.stdinContains("CREATE DATABASE `app`") {
		t.Fatalf("stdin=%q", runner.stdin)
	}
}

func TestHelperImportPreImportBackup(t *testing.T) {
	c, root := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	_, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "app.sql", databases.ImportRequest{
		Mode: databases.ImportMerge, PreImportBackup: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "backups", "databases", "mysql", "app", "*.sql.gz"))
	if len(matches) != 1 {
		t.Fatalf("pre-import backups=%v", matches)
	}
}

func TestHelperUpdateSQLEnabledUnsupported(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	enabled := false
	_, err := c.UpdateUser(context.Background(), "mysql", "usr_app", databases.UpdateUserRequest{Enabled: &enabled})
	if !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestHelperDeleteReassignMySQLUnsupported(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	err := c.DeleteUser(context.Background(), "mysql", "usr_app", databases.DeleteUserRequest{Confirm: "app", ReassignOwned: true})
	if !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperGrantsProtectSystemDatabase(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	_, err := c.UpdateUser(context.Background(), "mysql", "usr_app", databases.UpdateUserRequest{
		Grants: []model.DatabaseGrant{{Database: "devstack", Preset: model.GrantReadWrite}},
	})
	if !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("err=%v", err)
	}
	if runner.stdinContains("GRANT") {
		t.Fatalf("grant ran on a protected database: %q", runner.stdin)
	}
}

func TestHelperRestoreBackupToNewDatabase(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	restore, err := c.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{Target: "app2"})
	if err != nil || restore.Status != model.JobSucceeded {
		t.Fatalf("job=%+v err=%v", restore, err)
	}
	if !runner.stdinContains("CREATE DATABASE `app2`") {
		t.Fatalf("stdin=%q", runner.stdin)
	}
	defaultRestore, err := c.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{})
	if err != nil || defaultRestore.Status != model.JobSucceeded {
		t.Fatalf("default restore=%+v err=%v", defaultRestore, err)
	}
	if !strings.HasPrefix(defaultRestore.Database, "app_restore_") {
		t.Fatalf("default target=%q", defaultRestore.Database)
	}
}

func TestHelperListBackupsWorksWhenStopped(t *testing.T) {
	c, root := databaseHelperFixture(t)
	dir := filepath.Join(root, "backups", "databases", "mysql", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20261030T120000.000000000Z.sql.gz")
	if err := os.WriteFile(path, []byte("gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := c.runner.(*fakeCommandRunner)
	runner.psState = "exited"
	runner.reset()
	list, err := c.ListBackups(context.Background(), "mysql")
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("backup list ran a command: %v", runner.calls)
	}
}

func TestHelperBackupRejectsTraversal(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	for _, id := range []string{"../secret", "app/../../secret", "app--../../secret.sql.gz", ""} {
		if _, err := c.DownloadBackup(context.Background(), "mysql", id); !errors.Is(err, databases.ErrInvalidRequest) {
			t.Fatalf("DownloadBackup(%q) err=%v", id, err)
		}
	}
}

func TestHelperResolveSiteEnvOmitsMetadataPassword(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	env, err := c.ResolveSiteEnv(context.Background(), "mysql", []model.DatabaseLink{{
		ServiceID: "mysql", Engine: model.DatabaseMySQL, Database: "app", Username: "app", Primary: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_HOST"] != "127.0.0.1" || env["DB_PORT"] != "3306" || env["DB_DATABASE"] != "app" || env["DB_USERNAME"] != "app" {
		t.Fatalf("env=%v", env)
	}
	if _, ok := env["DB_PASSWORD"]; ok {
		t.Fatalf("metadata-only env has password: %v", env)
	}
	if _, ok := env["DATABASE_URL"]; ok {
		t.Fatalf("metadata-only env has URL: %v", env)
	}
}

func TestHelperResolveSiteEnvManagedPassword(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	if err := c.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	env, err := c.ResolveSiteEnv(context.Background(), "mysql", []model.DatabaseLink{{
		ServiceID: "mysql", Engine: model.DatabaseMySQL, Database: "app", UserID: "usr_app", Username: "app", Primary: true, PasswordManaged: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if env["DB_PASSWORD"] != "s3cret" {
		t.Fatalf("env=%v", env)
	}
	if !strings.Contains(env["DATABASE_URL"], "s3cret") {
		t.Fatalf("url=%q", env["DATABASE_URL"])
	}
}

func TestHelperResolveSiteEnvMissingSecretFails(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	_, err := c.ResolveSiteEnv(context.Background(), "mysql", []model.DatabaseLink{{
		ServiceID: "mysql", Database: "app", UserID: "usr_missing", Username: "app", Primary: true, PasswordManaged: true,
	}})
	if !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("err=%v", err)
	}
}

func TestHelperRedisFullExcludesAdmin(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	runner := c.runner.(*fakeCommandRunner)
	_, err := c.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "full"})
	if err != nil {
		t.Fatal(err)
	}
	joined := runner.joinedCalls()
	if strings.Contains(joined, "+@admin") || strings.Contains(joined, "+@dangerous") {
		t.Fatalf("admin grant: %s", joined)
	}
	if !strings.Contains(joined, "ACL SETUSER") || !strings.Contains(joined, "ACL SAVE") {
		t.Fatalf("calls=%s", joined)
	}
	if strings.Contains(joined, "reset on >") {
		t.Fatalf("password leaked into argv: %s", joined)
	}
}

func TestHelperRedisACLFileIsIdempotent(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	runner := c.runner.(*fakeCommandRunner)
	if err := c.EnableRedisACLFile(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	if err := c.EnableRedisACLFile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != before {
		t.Fatalf("second call repeated config work: %v", runner.calls[before:])
	}
	config, err := os.ReadFile(filepath.Join(root, "services", "redis", "config", "redis.conf"))
	if err != nil || !strings.Contains(string(config), redisACLFileLine) {
		t.Fatalf("config=%q err=%v", config, err)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "redis", "data", "users.acl")); err != nil {
		t.Fatalf("users.acl missing: %v", err)
	}
}

func TestHelperRedisPasswordOnStdin(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	runner := c.runner.(*fakeCommandRunner)
	secret, err := c.SetUserPassword(context.Background(), "redis", redisUserID("cache"))
	if err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("ACL SETUSER cache reset") || !runner.stdinContains(">"+secret.Value) {
		t.Fatalf("stdin=%q", runner.stdin)
	}
	if runner.joinedCallsContains(secret.Value) {
		t.Fatal("password in argv")
	}
	stored, err := c.readDatabaseSecret("redis", redisUserID("cache"))
	if err != nil || stored != secret.Value {
		t.Fatalf("stored=%q err=%v", stored, err)
	}
}

func TestHelperRedisUpdateResetsRules(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	runner := c.runner.(*fakeCommandRunner)
	if _, err := c.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "full"}); err != nil {
		t.Fatal(err)
	}
	runner.reset()
	if _, err := c.UpdateUser(context.Background(), "redis", redisUserID("cache"), databases.UpdateUserRequest{AccessLevel: "read-only"}); err != nil {
		t.Fatal(err)
	}
	joined := runner.joinedCalls()
	if !strings.Contains(joined, "-@all") || !strings.Contains(joined, "+@read") {
		t.Fatalf("rules=%s", joined)
	}
	if strings.Contains(joined, "+@all") {
		t.Fatalf("downgrade kept +@all: %s", joined)
	}
}

func TestHelperRedisProtectsDefault(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	runner := c.runner.(*fakeCommandRunner)
	err := c.DeleteUser(context.Background(), "redis", redisUserID("default"), databases.DeleteUserRequest{Confirm: "default"})
	if !errors.Is(err, databases.ErrProtected) {
		t.Fatalf("err=%v", err)
	}
	if runner.joinedCallsContains("ACL DELUSER") {
		t.Fatalf("default user was deleted: %s", runner.joinedCalls())
	}
}

func writeRedisConfig(t *testing.T, root string) {
	t.Helper()
	configDir := filepath.Join(root, "services", "redis", "config")
	dataDir := filepath.Join(root, "services", "redis", "data")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "redis.conf"), []byte("port 6379\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
