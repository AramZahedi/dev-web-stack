//go:build !windows

package helper

import (
	"bytes"
	"compress/gzip"
	"context"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dbengine"
	"github.com/AramZahedi/dev-web-stack/internal/dbproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

type fakeCommandRunner struct {
	mu           sync.Mutex
	calls        [][]string
	stdin        [][]byte
	streamOutput []byte
	psState      string
	aclList      string
	databaseList string
	grantList    string
	handle       func(name string, args []string) ([]byte, error)
	handleInput  func(name string, args []string, input []byte) ([]byte, error)
}

func (f *fakeCommandRunner) databaseListOutput() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.databaseList != "" {
		return []byte(f.databaseList)
	}
	return []byte("app\t1024\t2\nblog\t2048\t3\n")
}

func (f *fakeCommandRunner) grantListOutput() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.grantList != "" {
		return []byte(f.grantList)
	}
	return []byte("app\tread_write_schema\n")
}

func (f *fakeCommandRunner) aclListOutput() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.aclList != "" {
		return []byte(f.aclList)
	}
	return []byte("user default on nopass ~* &* +@all\nuser cache on #hash ~* +@read -@admin -@dangerous\n")
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
		return f.databaseListOutput(), nil
	case strings.Contains(joined, "list-users"):
		return []byte("usr_app\tapp\t%\n"), nil
	case strings.Contains(joined, "list-grants"):
		return f.grantListOutput(), nil
	case strings.Contains(joined, "ACL LIST"):
		return f.aclListOutput(), nil
	case strings.Contains(joined, "restart"):
		return []byte("ok"), nil
	default:
		return []byte("ok"), nil
	}
}

func (f *fakeCommandRunner) defaultInput(name string, args []string, input []byte) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "ACL LIST") || strings.Contains(string(input), "ACL LIST"):
		return append([]byte("OK\n"), f.aclListOutput()...), nil
	case strings.Contains(string(input), "SELECT CONCAT('KILL '"):
		return []byte("KILL 11;\nKILL 12;\n"), nil
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
	id           model.DatabaseEngine
	rename       bool
	emptyUserIDs bool
	userHost     string
	userHostSet  bool
	grantErr     error
	listUsersErr error
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
	if e.listUsersErr != nil {
		return nil, e.listUsersErr
	}
	return []string{"list-users"}, nil
}

func (e *fakeDatabaseEngine) ParseUsers(data []byte) ([]model.DatabaseUser, error) {
	var out []model.DatabaseUser
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || fields[0] == "" {
			continue
		}
		host := fields[2]
		if e.userHostSet {
			host = e.userHost
		}
		id := fields[0]
		if e.emptyUserIDs {
			id = ""
		}
		out = append(out, model.DatabaseUser{ID: id, Username: fields[1], Host: host, Enabled: true})
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
	if e.grantErr != nil {
		return "", e.grantErr
	}
	if err := dbengine.ValidateIdentifier(database); err != nil {
		return "", err
	}
	return "GRANT " + string(grant.Preset) + " ON " + database, nil
}

func (e *fakeDatabaseEngine) RevokeSQL(username, host, database string, grant model.DatabaseGrant) (string, error) {
	if err := dbengine.ValidateIdentifier(database); err != nil {
		return "", err
	}
	return "REVOKE " + string(grant.Preset) + " ON " + database, nil
}

func (e *fakeDatabaseEngine) TerminateSessionsSQL(database string) (string, error) {
	return "SELECT CONCAT('KILL ', id, ';') FROM information_schema.PROCESSLIST WHERE DB = " + dbengine.QuoteLiteral(database), nil
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

func TestHelperRejectsTopLevelServicesSymlink(t *testing.T) {
	c, root := databaseHelperFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "services")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := c.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err == nil {
		t.Fatal("writeDatabaseSecret followed a top-level services symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "mysql", "secrets", "database-users", "usr_app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret escaped the helper root: %v", err)
	}
}

func TestHelperRejectsTopLevelBackupsSymlink(t *testing.T) {
	c, root := databaseHelperFixture(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "databases", "mysql", "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "databases", "mysql", "app", "20261030T120000.000000000Z.sql.gz"), []byte("gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "backups")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := c.ListBackups(context.Background(), "mysql"); err == nil {
		t.Fatal("ListBackups followed a top-level backups symlink")
	}
	if _, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual}); err == nil {
		t.Fatal("CreateBackup followed a top-level backups symlink")
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

func TestLimitedBufferCapsOutput(t *testing.T) {
	var buffer limitedBuffer
	buffer.limit = 1024
	if _, err := buffer.Write(bytes.Repeat([]byte("x"), 10000)); err != nil {
		t.Fatal(err)
	}
	text := buffer.String()
	if len(text) > 1024+len("\n[output truncated]") {
		t.Fatalf("limited buffer retained %d bytes", len(text))
	}
	if !strings.Contains(text, "output truncated") {
		t.Fatalf("truncation marker missing: %q", text)
	}
}

func TestRunInputStreamCapsErrorOutput(t *testing.T) {
	err := (execRunner{}).RunInputStream(context.Background(), strings.NewReader(""), "sh", "-c", `yes err | head -c 200000 >&2; exit 1`)
	if err == nil {
		t.Fatal("RunInputStream succeeded, want exit error")
	}
	if len(err.Error()) > 70<<10 {
		t.Fatalf("error output was not capped: %d bytes", len(err.Error()))
	}
}

func TestHelperListBackupsReturnsEmptySlice(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	backups, err := c.ListBackups(context.Background(), "mysql")
	if err != nil || backups == nil || len(backups) != 0 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
}

func TestHelperCreateBackupRejectsUnknownKind(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	_, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: "bogus"})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperListReturnsNonNilSlices(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.databaseList = "\n"
	list, err := c.ListDatabases(context.Background(), "mysql")
	if err != nil || list == nil {
		t.Fatalf("databases=%v err=%v", list, err)
	}
	runner.handle = func(name string, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "list-users") {
			return nil, nil
		}
		return runner.defaultRun(name, args)
	}
	users, err := c.ListUsers(context.Background(), "mysql")
	if err != nil || users == nil {
		t.Fatalf("users=%v err=%v", users, err)
	}
}

func TestHelperDatabaseSecretExistsRejectsBroadMode(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	if err := c.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if !c.databaseSecretExists("mysql", "usr_app") {
		t.Fatal("private secret was not detected")
	}
	path, err := c.databaseSecretPath("mysql", "usr_app")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if c.databaseSecretExists("mysql", "usr_app") {
		t.Fatal("world-readable secret was treated as managed")
	}
}

func TestDatabaseImportLimitIsOneGiB(t *testing.T) {
	if maxDatabaseImportBytes != 1<<30 {
		t.Fatalf("import limit = %d", maxDatabaseImportBytes)
	}
	var buffer bytes.Buffer
	if _, err := copyLimited(&buffer, strings.NewReader("12345"), 4); !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("copyLimited err=%v", err)
	}
}

func TestHelperSQLUserIDsFromDefaultRegistry(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	c.databaseRegistry = nil
	previous := dbengine.DefaultRegistry
	dbengine.DefaultRegistry = func() dbengine.Registry {
		return fakeRegistry{engines: map[model.DatabaseEngine]dbengine.Engine{
			model.DatabaseMySQL:      &fakeDatabaseEngine{id: model.DatabaseMySQL, emptyUserIDs: true},
			model.DatabasePostgreSQL: &fakeDatabaseEngine{id: model.DatabasePostgreSQL, rename: true, emptyUserIDs: true, userHostSet: true, userHost: ""},
		}}
	}
	t.Cleanup(func() { dbengine.DefaultRegistry = previous })

	wantID := databaseUserID("mysql", model.DatabaseMySQL, "app", "%")
	users, err := c.ListUsers(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != wantID || users[0].Host != "%" {
		t.Fatalf("users = %+v, want ID %q", users, wantID)
	}
	if _, err := c.UpdateUser(context.Background(), "mysql", wantID, databases.UpdateUserRequest{
		Grants: []model.DatabaseGrant{{Database: "app", Preset: model.GrantReadWrite}},
	}); err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	secret, err := c.SetUserPassword(context.Background(), "mysql", wantID)
	if err != nil || secret.Value == "" {
		t.Fatalf("SetUserPassword secret=%+v err=%v", secret, err)
	}
	revealed, err := c.RevealUserPassword(context.Background(), "mysql", wantID)
	if err != nil || revealed.Value != secret.Value {
		t.Fatalf("RevealUserPassword secret=%+v err=%v", revealed, err)
	}
	if err := c.DeleteUser(context.Background(), "mysql", wantID, databases.DeleteUserRequest{Confirm: "app"}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	pgWant := databaseUserID("postgresql", model.DatabasePostgreSQL, "app", "")
	pgUsers, err := c.ListUsers(context.Background(), "postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if len(pgUsers) != 1 || pgUsers[0].ID != pgWant || pgUsers[0].Host != "" {
		t.Fatalf("postgres users = %+v, want ID %q", pgUsers, pgWant)
	}
}

func TestHelperUsesDefaultRegistryFallback(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	c.databaseRegistry = nil
	previous := dbengine.DefaultRegistry
	dbengine.DefaultRegistry = func() dbengine.Registry {
		return fakeRegistry{engines: map[model.DatabaseEngine]dbengine.Engine{
			model.DatabaseMySQL: &fakeDatabaseEngine{id: model.DatabaseMySQL},
		}}
	}
	t.Cleanup(func() { dbengine.DefaultRegistry = previous })
	list, err := c.ListDatabases(context.Background(), "mysql")
	if err != nil || len(list) == 0 {
		t.Fatalf("default registry fallback list=%v err=%v", list, err)
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
	secret, err := c.readDatabaseSecret("mysql", user.ID)
	if err != nil {
		t.Fatalf("stored secret: %v", err)
	}
	if runner.joinedCallsContains(secret) {
		t.Fatal("password in argv")
	}
}

func TestHelperCreateUserRollsBackWhenLookupFails(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.handle = func(name string, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "list-users") {
			return nil, nil
		}
		return runner.defaultRun(name, args)
	}
	_, err := c.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app", Host: "%"})
	if err == nil {
		t.Fatal("CreateUser succeeded without a resolvable user ID")
	}
	if !runner.stdinContains("DROP USER") {
		t.Fatalf("created user was not rolled back: %q", runner.stdin)
	}
}

func TestHelperCreateUserSecretFailureReturnsUser(t *testing.T) {
	c, root := databaseHelperFixture(t)
	if err := os.WriteFile(filepath.Join(root, "services"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := c.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app", Host: "%"})
	if err == nil {
		t.Fatal("CreateUser succeeded without storing a password")
	}
	if user.ID == "" || user.PasswordManaged || user.Username != "app" {
		t.Fatalf("user = %+v", user)
	}
}

func TestHelperRedisCreateUserSecretFailureReturnsUser(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	confirmRedisACLFile(t, c)
	if err := os.WriteFile(filepath.Join(root, "services", "redis", "secrets"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, err := c.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "read-only"})
	if err == nil {
		t.Fatal("SetRedisUser succeeded without storing a password")
	}
	if user.ID != redisUserID("cache") || user.PasswordManaged || user.Username != "cache" {
		t.Fatalf("user = %+v", user)
	}
}

func TestHelperTerminatesMySQLSessions(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	if err := c.DropDatabase(context.Background(), "mysql", "app", databases.DropDatabaseRequest{Confirm: "app"}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("KILL 11;") || !runner.stdinContains("KILL 12;") {
		t.Fatalf("session KILL statements not executed: %q", runner.stdin)
	}
	if !runner.stdinContains("DROP DATABASE `app`") {
		t.Fatalf("drop did not run after session termination: %q", runner.stdin)
	}
}

func TestHelperRejectsUnsafeSessionTerminationOutput(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.handleInput = func(name string, args []string, input []byte) ([]byte, error) {
		if strings.Contains(string(input), "SELECT CONCAT('KILL '") {
			return []byte("not a kill statement\n"), nil
		}
		return []byte("OK"), nil
	}
	err := c.DropDatabase(context.Background(), "mysql", "app", databases.DropDatabaseRequest{Confirm: "app"})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if runner.stdinContains("DROP DATABASE") {
		t.Fatalf("drop ran after invalid session output: %q", runner.stdin)
	}
}

func TestHelperRestoreOverwriteTerminatesSessions(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	runner.reset()
	if _, err := c.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{
		Target: "app", Overwrite: true, Confirm: "app",
	}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("KILL 11;") || !runner.stdinContains("KILL 12;") {
		t.Fatalf("restore overwrite did not terminate sessions: %q", runner.stdin)
	}
}

func TestHelperImportReplaceTerminatesSessions(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	if _, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "app.sql", databases.ImportRequest{
		Mode: databases.ImportReplace, Confirm: "app",
	}); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("KILL 11;") || !runner.stdinContains("KILL 12;") {
		t.Fatalf("replace import did not terminate sessions: %q", runner.stdin)
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

func TestHelperListBackupsReportsKind(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupPreRestore})
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListBackups(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != job.BackupID || list[0].Kind != model.BackupPreRestore {
		t.Fatalf("backup list = %+v", list)
	}
}

func TestHelperRestoreCorruptBackupDoesNotDrop(t *testing.T) {
	c, root := databaseHelperFixture(t)
	dir := filepath.Join(root, "backups", "databases", "mysql", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20261030T120000.000000000Z.manual.sql.gz")
	if err := os.WriteFile(path, []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	_, err := c.RestoreBackup(context.Background(), "mysql", "app--20261030T120000.000000000Z.manual.sql.gz", databases.RestoreBackupRequest{
		Target: "app", Overwrite: true, Confirm: "app",
	})
	if err == nil {
		t.Fatal("corrupt backup restore succeeded")
	}
	if runner.stdinContains("DROP DATABASE") || runner.joinedCallsContains("DROP DATABASE") {
		t.Fatalf("target dropped before backup validation: %s", runner.joinedCalls())
	}
}

func TestHelperRestoreForcesPreRestoreBackup(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{
		Target: "app", Overwrite: true, Confirm: "app", PreRestoreBackup: false,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListBackups(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	preRestore := 0
	for _, backup := range list {
		if backup.Kind == model.BackupPreRestore {
			preRestore++
		}
	}
	if preRestore != 1 {
		t.Fatalf("pre-restore backups = %d, list=%+v", preRestore, list)
	}
}

func TestHelperRestoreFailureCarriesPreRestoreBackupID(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
	if err != nil {
		t.Fatal(err)
	}
	runner.handleInput = func(name string, args []string, input []byte) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "restore") {
			return nil, errors.New("restore failed")
		}
		return []byte("OK"), nil
	}
	failed, err := c.RestoreBackup(context.Background(), "mysql", job.BackupID, databases.RestoreBackupRequest{
		Target: "app", Overwrite: true, Confirm: "app",
	})
	if err == nil || failed.Status != model.JobFailed {
		t.Fatalf("job=%+v err=%v", failed, err)
	}
	list, err := c.ListBackups(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, backup := range list {
		if backup.ID == failed.BackupID && backup.Kind == model.BackupPreRestore {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed job backup=%q is not a pre_restore backup: %+v", failed.BackupID, list)
	}
}

func TestHelperImportReplaceForcesPreImportBackup(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	job, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "app.sql", databases.ImportRequest{
		Mode: databases.ImportReplace, Confirm: "app", PreImportBackup: false,
	})
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	list, err := c.ListBackups(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Kind != model.BackupPreImport || job.BackupID != list[0].ID {
		t.Fatalf("job backup=%q list=%+v", job.BackupID, list)
	}
}

func TestHelperImportCorruptGzipDoesNotDrop(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	_, err := c.ImportDatabase(context.Background(), "mysql", "app", bytes.NewReader([]byte{0x1f, 0x8b, 0x00}), "bad.sql.gz", databases.ImportRequest{
		Mode: databases.ImportReplace, Confirm: "app",
	})
	if err == nil {
		t.Fatal("corrupt gzip import succeeded")
	}
	if runner.stdinContains("DROP DATABASE") || runner.joinedCallsContains("DROP DATABASE") {
		t.Fatalf("target dropped before import validation: %s", runner.joinedCalls())
	}
}

func TestHelperRestoreDefaultTargetLength(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	longName := "d" + strings.Repeat("a", 59)
	dir := filepath.Join(c.root, "backups", "databases", "mysql", longName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20261030T120000.000000000Z.manual.sql.gz"), gzipBytes(t, "dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := c.RestoreBackup(context.Background(), "mysql", longName+"--20261030T120000.000000000Z.manual.sql.gz", databases.RestoreBackupRequest{})
	if err != nil || job.Status != model.JobSucceeded {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if len(job.Database) > 63 {
		t.Fatalf("default target %q is too long", job.Database)
	}
	if err := dbengine.ValidateIdentifier(job.Database); err != nil {
		t.Fatalf("default target %q invalid: %v", job.Database, err)
	}
}

func TestHelperListBackupsHonorsCanceledContext(t *testing.T) {
	c, root := databaseHelperFixture(t)
	dir := filepath.Join(root, "backups", "databases", "mysql", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "20261030T120000.000000000Z.sql.gz"), []byte("gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListBackups(ctx, "mysql"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestHelperRestoreTruncatedGzipDoesNotDrop(t *testing.T) {
	c, root := databaseHelperFixture(t)
	dir := filepath.Join(root, "backups", "databases", "mysql", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20261030T120000.000000000Z.manual.sql.gz")
	if err := os.WriteFile(path, truncatedGzip(t, 32<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	_, err := c.RestoreBackup(context.Background(), "mysql", "app--20261030T120000.000000000Z.manual.sql.gz", databases.RestoreBackupRequest{
		Target: "app", Overwrite: true, Confirm: "app",
	})
	if err == nil {
		t.Fatal("truncated gzip restore succeeded")
	}
	if runner.stdinContains("DROP DATABASE") {
		t.Fatalf("target dropped before full gzip validation: %q", runner.stdin)
	}
}

func TestHelperImportTruncatedGzipDoesNotDrop(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	_, err := c.ImportDatabase(context.Background(), "mysql", "app", bytes.NewReader(truncatedGzip(t, 32<<10)), "bad.sql.gz", databases.ImportRequest{
		Mode: databases.ImportReplace, Confirm: "app",
	})
	if err == nil {
		t.Fatal("truncated gzip import succeeded")
	}
	if runner.stdinContains("DROP DATABASE") {
		t.Fatalf("target dropped before full gzip validation: %q", runner.stdin)
	}
}

func TestValidateRestoreReaderRejectsDataWithError(t *testing.T) {
	reader := &dataErrorReader{}
	if _, err := validateRestoreReader(reader); err == nil {
		t.Fatal("validateRestoreReader accepted n>0 with an error")
	}
}

type dataErrorReader struct{}

func (r *dataErrorReader) Read(p []byte) (int, error) {
	copy(p, "partial")
	return 7, errors.New("truncated stream")
}

func TestHelperImportFailureCarriesPreImportBackupID(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.streamOutput = []byte("dump")
	runner.handleInput = func(name string, args []string, input []byte) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "restore") {
			return nil, errors.New("malformed SQL")
		}
		return []byte("OK"), nil
	}
	failed, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("not sql"), "bad.sql", databases.ImportRequest{
		Mode: databases.ImportReplace, Confirm: "app",
	})
	if err == nil || failed.Status != model.JobFailed {
		t.Fatalf("job=%+v err=%v", failed, err)
	}
	list, err := c.ListBackups(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, backup := range list {
		if backup.ID == failed.BackupID && backup.Kind == model.BackupPreImport {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed import backup=%q is not a pre_import backup: %+v", failed.BackupID, list)
	}
}

func truncatedGzip(t *testing.T, size int) []byte {
	t.Helper()
	payload := make([]byte, size)
	if _, err := cryptorand.Read(payload); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	if _, err := gz.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	return data[:len(data)-8]
}

func gzipBytes(t *testing.T, value string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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

func TestHelperCreateDatabaseRejectsUnsafeCharset(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	_, err := c.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{
		Name: "app", Charset: "utf8mb4; DROP DATABASE mysql",
	})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperRunDatabaseSQLInRejectsInjection(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	if _, err := c.runDatabaseSQLIn(context.Background(), "postgresql", "app=evil", "SELECT 1"); !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperReassignOwnedValidatesDatabaseNames(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.databaseList = "app=evil\t0\t0\n"
	runner.reset()
	err := c.DeleteUser(context.Background(), "postgresql", "usr_app", databases.DeleteUserRequest{
		Confirm: "app", ReassignOwned: true,
	})
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if runner.joinedCallsContains("--dbname=app=evil") {
		t.Fatalf("unsafe database name reached psql: %s", runner.joinedCalls())
	}
}

func TestHelperGrantDiffSkipsGlobalScopes(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.grantList = "*\tread_write\napp\tread_write_schema\n"
	runner.reset()
	engine := &fakeDatabaseEngine{id: model.DatabaseMySQL}
	user := model.DatabaseUser{Username: "app", Host: "%"}
	if err := c.applyUserGrants(context.Background(), "mysql", engine, user, []model.DatabaseGrant{
		{Database: "app", Preset: model.GrantReadWrite},
	}); err != nil {
		t.Fatal(err)
	}
	if runner.stdinContains("ON *") || runner.stdinContains("REVOKE read_write ON *") {
		t.Fatalf("global grant was revoked: %q", runner.stdin)
	}
}

func TestHelperGrantDiffSkipsWildcardScopes(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.grantList = "app%\tread_write\napp\tread_write_schema\n"
	runner.reset()
	engine := &fakeDatabaseEngine{id: model.DatabaseMySQL}
	user := model.DatabaseUser{Username: "app", Host: "%"}
	if err := c.applyUserGrants(context.Background(), "mysql", engine, user, []model.DatabaseGrant{
		{Database: "app", Preset: model.GrantReadWrite},
	}); err != nil {
		t.Fatal(err)
	}
	if runner.stdinContains("ON app%") || runner.stdinContains("REVOKE read_write ON app%") {
		t.Fatalf("wildcard grant was revoked: %q", runner.stdin)
	}
}

func TestHelperGrantDiffRejectsDesiredGlobalScope(t *testing.T) {
	for _, scope := range []string{"*", "%", "*.*", "app%"} {
		t.Run(scope, func(t *testing.T) {
			c, _ := databaseHelperFixture(t)
			runner := c.runner.(*fakeCommandRunner)
			runner.reset()
			engine := &fakeDatabaseEngine{id: model.DatabaseMySQL}
			user := model.DatabaseUser{Username: "app", Host: "%"}
			err := c.applyUserGrants(context.Background(), "mysql", engine, user, []model.DatabaseGrant{
				{Database: scope, Preset: model.GrantReadWrite},
			})
			if !errors.Is(err, databases.ErrInvalidRequest) {
				t.Fatalf("err=%v", err)
			}
			if runner.stdinContains("GRANT") || runner.stdinContains("REVOKE") {
				t.Fatalf("scope %q reached SQL: %q", scope, runner.stdin)
			}
		})
	}
}

func TestHelperListUsersEngineErrorIsDomainError(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	engine := &fakeDatabaseEngine{id: model.DatabaseMySQL, listUsersErr: dbengine.ErrUnsupported}
	c.databaseRegistry = fakeRegistry{engines: map[model.DatabaseEngine]dbengine.Engine{model.DatabaseMySQL: engine}}
	_, err := c.ListUsers(context.Background(), "mysql")
	if !errors.Is(err, databases.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
}

func TestHelperGrantEngineErrorIsDomainError(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.grantList = "app\tread_write_schema\n"
	engine := &fakeDatabaseEngine{id: model.DatabaseMySQL, grantErr: dbengine.ErrInvalidIdentifier}
	user := model.DatabaseUser{Username: "app", Host: "%"}
	err := c.applyUserGrants(context.Background(), "mysql", engine, user, []model.DatabaseGrant{
		{Database: "app", Preset: model.GrantReadWrite},
	})
	if !errors.Is(err, databases.ErrInvalidRequest) || !errors.Is(err, dbengine.ErrInvalidIdentifier) {
		t.Fatalf("err=%v", err)
	}
}

func TestHelperGrantDiffReadsUnderServiceLock(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.grantList = "app\tread_write_schema\n"
	var mu sync.Mutex
	active := 0
	overlap := false
	track := func() func() {
		mu.Lock()
		active++
		if active > 1 {
			overlap = true
		}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		return func() {
			mu.Lock()
			active--
			mu.Unlock()
		}
	}
	runner.handle = func(name string, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "list-grants") {
			defer track()()
		}
		return []byte("app\tread_write_schema\n"), nil
	}
	runner.handleInput = func(name string, args []string, input []byte) ([]byte, error) {
		if strings.Contains(string(input), "GRANT") || strings.Contains(string(input), "REVOKE") {
			defer track()()
		}
		return []byte("OK"), nil
	}
	engine := &fakeDatabaseEngine{id: model.DatabaseMySQL}
	user := model.DatabaseUser{Username: "app", Host: "%"}
	var wg sync.WaitGroup
	for _, preset := range []model.DatabaseGrantPreset{model.GrantReadWrite, model.GrantAdmin} {
		wg.Add(1)
		go func(preset model.DatabaseGrantPreset) {
			defer wg.Done()
			if err := c.applyUserGrants(context.Background(), "mysql", engine, user, []model.DatabaseGrant{{Database: "app", Preset: preset}}); err != nil {
				t.Errorf("applyUserGrants: %v", err)
			}
		}(preset)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if overlap {
		t.Fatal("grant diff read/apply overlapped across service lock")
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

func TestHelperResolveSiteEnvValidatesLinkIdentifiers(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	for _, link := range []model.DatabaseLink{
		{ServiceID: "mysql", Database: "app; DROP DATABASE mysql", Username: "app"},
		{ServiceID: "mysql", Database: "app", Username: "bad user"},
	} {
		if _, err := c.ResolveSiteEnv(context.Background(), "mysql", []model.DatabaseLink{link}); !errors.Is(err, databases.ErrInvalidRequest) {
			t.Fatalf("link %+v err=%v", link, err)
		}
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	for _, body := range []string{
		`{"name":"app","password":"caller-supplied"}`,
		`{"name":"app","bogus":true}`,
	} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var dst dbproto.DatabaseCreateRequest
		if err := decodeJSON(httptest.NewRecorder(), req, &dst); err == nil {
			t.Fatalf("unknown field was accepted: %s", body)
		}
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

func TestHelperRedisDefaultPasswordPreservesACLRules(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	confirmRedisACLFile(t, c)
	runner := c.runner.(*fakeCommandRunner)
	runner.aclList = "user default on nopass ~* &* +@all\nuser cache on #hash ~* +@read -@admin -@dangerous\n"
	secret, err := c.SetUserPassword(context.Background(), "redis", redisUserID("default"))
	if err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("ACL SETUSER default resetpass >") {
		t.Fatalf("default password reset command = %q", runner.stdin)
	}
	joined := runner.joinedCalls()
	for _, forbidden := range []string{"-acl", "-@admin", "-@dangerous", "+@read", "+@all"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("default password reset applied application rules %q: %s", forbidden, joined)
		}
	}
	if !runner.stdinContains("ACL SAVE") {
		t.Fatalf("ACL SAVE missing: %s", joined)
	}
	stored, err := c.readDatabaseSecret("redis", redisUserID("default"))
	if err != nil || stored != secret.Value {
		t.Fatalf("stored=%q err=%v", stored, err)
	}
	runner.reset()
	if _, err := c.ListRedisUsers(context.Background(), "redis"); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("AUTH default " + secret.Value) {
		t.Fatalf("default AUTH did not use the stored secret: %q", runner.stdin)
	}
}

func TestHelperRedisDefaultPasswordSecretCompatibility(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	if err := c.writeDatabaseSecret("redis", "default", "legacy-secret"); err != nil {
		t.Fatal(err)
	}
	value, ok, err := c.redisDefaultPassword("redis")
	if err != nil || !ok || value != "legacy-secret" {
		t.Fatalf("legacy default secret = %q/%v/%v", value, ok, err)
	}
	if err := c.writeDatabaseSecret("redis", redisUserID("default"), "has space"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.redisDefaultPassword("redis"); !errors.Is(err, databases.ErrPasswordMissing) {
		t.Fatalf("whitespace default secret err=%v", err)
	}
}

func TestHelperRedisExternalUserFlags(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.aclList = "user default on nopass ~* &* +@all\nuser external on #hash ~* +@read\n"
	users, err := c.ListRedisUsers(context.Background(), "redis")
	if err != nil {
		t.Fatal(err)
	}
	var external model.DatabaseUser
	for _, user := range users {
		if user.Username == "external" {
			external = user
		}
	}
	if external.ID == "" || external.Managed || external.PasswordManaged {
		t.Fatalf("external user flags = %+v", external)
	}
	if err := c.writeDatabaseSecret("redis", redisUserID("external"), "s3cret"); err != nil {
		t.Fatal(err)
	}
	users, err = c.ListRedisUsers(context.Background(), "redis")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.Username == "external" {
			if !user.Managed || !user.PasswordManaged {
				t.Fatalf("managed external user flags = %+v", user)
			}
			return
		}
	}
	t.Fatal("external user missing after secret write")
}

func TestHelperRedisPasswordResetPreservesCustomRules(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	confirmRedisACLFile(t, c)
	runner := c.runner.(*fakeCommandRunner)
	runner.aclList = "user default on nopass ~* &* +@all\nuser custom on #hash ~custom* +get +set\n"
	if _, err := c.SetUserPassword(context.Background(), "redis", redisUserID("custom")); err != nil {
		t.Fatal(err)
	}
	if !runner.stdinContains("ACL SETUSER custom resetpass >") {
		t.Fatalf("custom password command = %q", runner.stdin)
	}
	joined := runner.joinedCalls()
	for _, forbidden := range []string{"+@all", "+@read", "-acl", "-@admin"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("custom reset broadened rules %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "ACL SAVE") {
		t.Fatalf("ACL SAVE missing: %s", joined)
	}
}

func TestHelperRedisACLListSkipsUnsafeUsernames(t *testing.T) {
	c, _ := databaseHelperFixture(t)
	runner := c.runner.(*fakeCommandRunner)
	runner.aclList = "user default on nopass ~* &* +@all\nuser 123bad on #hash ~* +@read\n"
	users, err := c.ListRedisUsers(context.Background(), "redis")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.Username == "123bad" || strings.Contains(user.Username, " ") {
			t.Fatalf("unsafe ACL username was returned: %+v", user)
		}
	}
}

func TestRedisOutputErrorDoesNotMatchUsername(t *testing.T) {
	for _, value := range []string{
		"user err on #hash ~* +@read\n",
		"user noauth on #hash ~* +@read\n",
		"value wrongpass\n",
	} {
		if err := redisOutputError([]byte(value)); err != nil {
			t.Fatalf("user data false positive %q: %v", value, err)
		}
	}
	for _, value := range []string{
		"(error) ERR unknown command\n",
		"(error) NOAUTH Authentication required.\n",
		"(error) WRONGPASS invalid username-password pair\n",
		"err unknown command\n",
	} {
		if err := redisOutputError([]byte(value)); err == nil {
			t.Fatalf("redis error output was accepted: %q", value)
		}
	}
}

func TestHelperRedisACLFileRequiresConfirmation(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	configPath := filepath.Join(root, "services", "redis", "config", "redis.conf")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	err = c.EnableRedisACLFile(context.Background())
	if !errors.Is(err, databases.ErrConfirmationRequired) {
		t.Fatalf("err=%v, want ErrConfirmationRequired", err)
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil || string(after) != string(before) {
		t.Fatalf("redis.conf changed before confirmation: %q err=%v", after, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, "services", "redis", "data", "users.acl")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("users.acl created before confirmation: %v", statErr)
	}
	if runner.joinedCallsContains("restart") {
		t.Fatalf("Redis restarted before confirmation: %s", runner.joinedCalls())
	}
}

func TestHelperRedisACLFileConfirmedMigrates(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	if err := c.EnableRedisACLFileConfirmed(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(root, "services", "redis", "config", "redis.conf"))
	if err != nil || !strings.Contains(string(config), redisACLFileLine) {
		t.Fatalf("config=%q err=%v", config, err)
	}
	aclInfo, err := os.Stat(filepath.Join(root, "services", "redis", "data", "users.acl"))
	if err != nil || aclInfo.Mode().Perm() != 0o600 {
		t.Fatalf("users.acl mode=%v err=%v", aclInfo, err)
	}
	if !runner.joinedCallsContains("restart") {
		t.Fatalf("confirmed migration did not restart Redis: %s", runner.joinedCalls())
	}
}

func TestHelperRedisExistingACLFileModeIsTightened(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	configPath := filepath.Join(root, "services", "redis", "config", "redis.conf")
	if err := os.WriteFile(configPath, []byte("port 6379\n"+redisACLFileLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aclPath := filepath.Join(root, "services", "redis", "data", "users.acl")
	if err := os.WriteFile(aclPath, []byte("user default on nopass ~* +@all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	if err := c.EnableRedisACLFile(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(aclPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("users.acl mode=%v err=%v", info, err)
	}
	if runner.joinedCallsContains("restart") {
		t.Fatalf("mode-only repair restarted Redis: %s", runner.joinedCalls())
	}
}

func TestHelperRedisACLFileRejectsDirectory(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	aclPath := filepath.Join(root, "services", "redis", "data", "users.acl")
	if err := os.MkdirAll(aclPath, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	err := c.EnableRedisACLFileConfirmed(context.Background(), true)
	if !errors.Is(err, databases.ErrInvalidRequest) {
		t.Fatalf("err=%v", err)
	}
	if runner.joinedCallsContains("restart") || runner.stdinContains("ACL LIST") {
		t.Fatalf("directory ACL file was migrated: %s", runner.joinedCalls())
	}
}

func TestHelperRedisMutationFailsClosedBeforeMigration(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	runner := c.runner.(*fakeCommandRunner)
	runner.reset()
	_, err := c.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache"})
	if !errors.Is(err, databases.ErrConfirmationRequired) {
		t.Fatalf("err=%v, want ErrConfirmationRequired", err)
	}
	if runner.stdinContains("ACL SETUSER") || runner.joinedCallsContains("restart") {
		t.Fatalf("mutation ran before migration confirmation: %s", runner.joinedCalls())
	}
}

func TestHelperRedisACLFilePreservesConfigInodeAndMode(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	configPath := filepath.Join(root, "services", "redis", "config", "redis.conf")
	if err := os.Chmod(configPath, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnableRedisACLFileConfirmed(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeStat, ok := before.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no syscall stat")
	}
	afterStat, ok := after.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no syscall stat")
	}
	if beforeStat.Ino != afterStat.Ino {
		t.Fatalf("redis.conf inode changed: %d -> %d", beforeStat.Ino, afterStat.Ino)
	}
	if after.Mode().Perm() != 0o600 {
		t.Fatalf("redis.conf mode = %o", after.Mode().Perm())
	}
	aclInfo, err := os.Stat(filepath.Join(root, "services", "redis", "data", "users.acl"))
	if err != nil || aclInfo.Mode().Perm() != 0o600 {
		t.Fatalf("users.acl mode=%v err=%v", aclInfo.Mode(), err)
	}
}

func TestHelperRedisCreateUserConflictsOnExistingName(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	confirmRedisACLFile(t, c)
	runner := c.runner.(*fakeCommandRunner)
	runner.aclList = "user default on nopass ~* &* +@all\nuser cache on #hash ~* +@read\n"
	runner.reset()
	_, err := c.CreateUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache"})
	if !errors.Is(err, databases.ErrConflict) {
		t.Fatalf("err=%v", err)
	}
	if runner.stdinContains("ACL SETUSER cache") {
		t.Fatalf("existing Redis user was overwritten: %q", runner.stdin)
	}
}

func TestHelperRedisCreateUserRuleFailureReturnsShape(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	confirmRedisACLFile(t, c)
	runner := c.runner.(*fakeCommandRunner)
	runner.handle = func(name string, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "ACL SETUSER cache -@all") {
			return nil, errors.New("ACL rule update failed")
		}
		return runner.defaultRun(name, args)
	}
	user, err := c.SetRedisUser(context.Background(), "redis", databases.CreateUserRequest{Username: "cache", AccessLevel: "read-only"})
	if err == nil {
		t.Fatal("SetRedisUser succeeded despite a rule failure")
	}
	if user.ID != redisUserID("cache") || user.Username != "cache" || !user.PasswordManaged {
		t.Fatalf("created user shape = %+v", user)
	}
}

func TestHelperRedisEnableOnlyPreservesCustomRules(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	confirmRedisACLFile(t, c)
	runner := c.runner.(*fakeCommandRunner)
	runner.aclList = "user default on nopass ~* &* +@all\nuser custom on #hash ~custom* +get +set\n"
	runner.reset()
	enabled := false
	if _, err := c.UpdateUser(context.Background(), "redis", redisUserID("custom"), databases.UpdateUserRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	joined := runner.joinedCalls()
	if !strings.Contains(joined, "ACL SETUSER custom off") {
		t.Fatalf("enable-only update did not toggle the user: %s", joined)
	}
	for _, forbidden := range []string{"-@all", "+@read", "+@all", "resetkeys"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("enable-only update reset custom rules %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "ACL SAVE") {
		t.Fatalf("ACL SAVE missing: %s", joined)
	}
}

func TestHelperRedisFullExcludesAdmin(t *testing.T) {
	c, root := databaseHelperFixture(t)
	writeRedisConfig(t, root)
	confirmRedisACLFile(t, c)
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
	if err := c.EnableRedisACLFileConfirmed(context.Background(), true); err != nil {
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
	confirmRedisACLFile(t, c)
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
	confirmRedisACLFile(t, c)
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

func confirmRedisACLFile(t *testing.T, c *DockerServiceController) {
	t.Helper()
	if err := c.EnableRedisACLFileConfirmed(context.Background(), true); err != nil {
		t.Fatal(err)
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
