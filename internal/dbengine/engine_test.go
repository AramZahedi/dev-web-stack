package dbengine

import (
	"errors"
	"strings"
	"testing"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

func TestMySQLRenameUnsupported(t *testing.T) {
	for _, e := range []Engine{NewMySQL(), NewMariaDB()} {
		if e.RenameSupported() {
			t.Fatalf("%s rename reported supported", e.ID())
		}
		if _, err := e.RenameDatabaseSQL("app", "app2"); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s rename err = %v, want ErrUnsupported", e.ID(), err)
		}
	}
}

func TestPostgresRenameSQL(t *testing.T) {
	e := NewPostgreSQL()
	if !e.RenameSupported() {
		t.Fatal("postgres rename reported unsupported")
	}
	sql, err := e.RenameDatabaseSQL("app", "app2")
	if err != nil || sql != `ALTER DATABASE "app" RENAME TO "app2";` {
		t.Fatalf("sql=%q err=%v", sql, err)
	}
}

func TestIdentifiersRejectInjection(t *testing.T) {
	for _, e := range []Engine{NewMySQL(), NewMariaDB(), NewPostgreSQL()} {
		if err := e.ValidateDatabaseName("app; DROP DATABASE mysql"); err == nil {
			t.Fatalf("%s accepted database injection", e.ID())
		}
		if err := e.ValidateUsername("app' --"); err == nil {
			t.Fatalf("%s accepted user injection", e.ID())
		}
	}
}

func TestMySQLHostScopeValidation(t *testing.T) {
	e := NewMySQL()
	for _, host := range []string{"%", "localhost", "127.0.0.1", "10.%", "::1"} {
		if err := e.ValidateHost(host); err != nil {
			t.Fatalf("ValidateHost(%q) = %v", host, err)
		}
	}
	for _, host := range []string{"app' --", "host/../x", "host name", "host;drop"} {
		if err := e.ValidateHost(host); err == nil {
			t.Fatalf("ValidateHost(%q) = nil, want rejection", host)
		}
	}
}

func TestProtectedObjects(t *testing.T) {
	for _, e := range []Engine{NewMySQL(), NewMariaDB(), NewPostgreSQL()} {
		for name := range SystemDatabases[e.ID()] {
			if !e.ProtectedDatabase(name) {
				t.Fatalf("%s does not protect database %q", e.ID(), name)
			}
		}
		for name := range SystemUsers[e.ID()] {
			if !e.ProtectedUser(name, "") {
				t.Fatalf("%s does not protect user %q", e.ID(), name)
			}
		}
	}
}

func TestMySQLCreateDatabaseSQL(t *testing.T) {
	e := NewMySQL()
	sql, err := e.CreateDatabaseSQL("app", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "CREATE DATABASE `app`") || !strings.Contains(sql, "utf8mb4") {
		t.Fatalf("create SQL = %q", sql)
	}
	if _, err := e.CreateDatabaseSQL("app;drop", "", ""); err == nil {
		t.Fatal("create accepted an injected name")
	}
	if _, err := e.CreateDatabaseSQL("app", "utf8mb4; drop", ""); err == nil {
		t.Fatal("create accepted an injected charset")
	}
}

func TestMySQLGrantSQLMapping(t *testing.T) {
	e := NewMySQL()
	cases := []struct {
		preset model.DatabaseGrantPreset
		want   []string
		deny   []string
	}{
		{model.GrantReadOnly, []string{"GRANT SELECT ON `app`.* TO 'usr'@'%'"}, []string{"INSERT", "DROP"}},
		{model.GrantReadWrite, []string{"GRANT SELECT, INSERT, UPDATE, DELETE ON `app`.*"}, []string{"CREATE", "DROP"}},
		{model.GrantReadWriteSchema, []string{"GRANT ", "CREATE", "ALTER", "DROP", "INDEX", "REFERENCES", "ON `app`.*"}, nil},
		{model.GrantAdmin, []string{"GRANT ALL PRIVILEGES ON `app`.*"}, nil},
	}
	for _, tc := range cases {
		sql, err := e.GrantSQL("usr", "%", "app", model.DatabaseGrant{Database: "app", Preset: tc.preset})
		if err != nil {
			t.Fatalf("%s: %v", tc.preset, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(sql, want) {
				t.Fatalf("%s SQL %q missing %q", tc.preset, sql, want)
			}
		}
		for _, deny := range tc.deny {
			if strings.Contains(sql, deny) {
				t.Fatalf("%s SQL %q contains %q", tc.preset, sql, deny)
			}
		}
	}
	if _, err := e.GrantSQL("usr", "%", "app", model.DatabaseGrant{Database: "app", Preset: model.GrantCustom, Privileges: []string{"SELECT; DROP DATABASE mysql"}}); err == nil {
		t.Fatal("custom grant accepted an unlisted privilege")
	}
}

func TestPostgresGrantSQLMapping(t *testing.T) {
	e := NewPostgreSQL()
	sql, err := e.GrantSQL("app", "", "appdb", model.DatabaseGrant{Database: "appdb", Preset: model.GrantReadWriteSchema})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`GRANT CONNECT ON DATABASE "appdb" TO "app"`,
		`GRANT USAGE ON SCHEMA public TO "app"`,
		`GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON ALL TABLES IN SCHEMA public TO "app"`,
		`GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO "app"`,
		`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER ON TABLES TO "app"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("read/write/schema SQL missing %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "SUPERUSER") || strings.Contains(sql, "ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO \"app\";\nGRANT ALL ON SCHEMA") {
		t.Fatalf("database-scoped grant leaked global privileges:\n%s", sql)
	}
}

func TestParseMySQLDatabasesAndUsers(t *testing.T) {
	e := NewMySQL()
	dbs, err := e.ParseDatabases([]byte("app\t1024\t3\nblog\t0\t0\n"))
	if err != nil || len(dbs) != 2 {
		t.Fatalf("databases = %+v err=%v", dbs, err)
	}
	if dbs[0].Name != "app" || dbs[0].SizeBytes != 1024 || dbs[0].TableCount != 3 {
		t.Fatalf("database = %+v", dbs[0])
	}
	users, err := e.ParseUsers([]byte("app\t%\tN\nroot\tlocalhost\tY\n"))
	if err != nil || len(users) != 1 {
		t.Fatalf("users = %+v err=%v", users, err)
	}
	if users[0].Username != "app" || users[0].Host != "%" || !users[0].Enabled {
		t.Fatalf("user = %+v", users[0])
	}
}

func TestParseMySQLGrants(t *testing.T) {
	e := NewMySQL()
	grants, err := e.ParseGrants([]byte("GRANT USAGE ON *.* TO `app`@`%`\nGRANT SELECT ON `app`.* TO `app`@`%`\nGRANT INSERT, UPDATE ON `app`.* TO `app`@`%`\nGRANT ALL PRIVILEGES ON `blog`.* TO `app`@`%`\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 {
		t.Fatalf("grants = %+v", grants)
	}
	if grants[0].Database != "app" || grants[0].Preset != model.GrantReadWrite {
		t.Fatalf("app grant = %+v", grants[0])
	}
	if grants[1].Database != "blog" || grants[1].Preset != model.GrantAdmin {
		t.Fatalf("blog grant = %+v", grants[1])
	}
}

func TestRedisFullExcludesAdmin(t *testing.T) {
	e := NewRedisACL()
	cmd := strings.Join(e.SetUserCommand("cache", "pw", "full", true), " ")
	lower := strings.ToLower(cmd)
	for _, forbidden := range []string{"+@admin", "+@dangerous", "+config", "+flushall"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("full ACL grants %s: %s", forbidden, cmd)
		}
	}
	for _, want := range []string{"+@all", "-@admin", "-@dangerous", "-config", "-flushall", "-acl"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("full ACL missing %s: %s", want, cmd)
		}
	}
	if !strings.HasSuffix(cmd, ">pw") {
		t.Fatalf("password must be the last argument for stdin delivery: %s", cmd)
	}
}

func TestRedisReadOnlyACL(t *testing.T) {
	e := NewRedisACL()
	cmd := strings.ToLower(strings.Join(e.SetUserCommand("cache", "pw", "read-only", true), " "))
	if !strings.Contains(cmd, "+@read") || !strings.Contains(cmd, "-@admin") || !strings.Contains(cmd, "-@dangerous") {
		t.Fatalf("read-only ACL = %s", cmd)
	}
	if strings.Contains(cmd, "+@all") {
		t.Fatalf("read-only ACL grants all: %s", cmd)
	}
}

func TestRegistryResolvesEngines(t *testing.T) {
	factory := DefaultRegistry
	if factory == nil {
		t.Fatal("DefaultRegistry is nil")
	}
	registry := factory()
	for _, engine := range []model.DatabaseEngine{model.DatabaseMySQL, model.DatabaseMariaDB, model.DatabasePostgreSQL, model.DatabaseRedis} {
		got, err := registry.For(engine)
		if err != nil || got.ID() != engine {
			t.Fatalf("For(%s) = %v, %v", engine, got, err)
		}
	}
	if _, err := registry.For("oracle"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown engine err = %v", err)
	}
}

func TestPostgresDumpCommandUsesAdminUser(t *testing.T) {
	e := NewPostgreSQL()
	command, err := e.DumpCommand("app")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "--username=postgres") {
		t.Fatalf("pg_dump command does not authenticate as postgres: %s", joined)
	}
	if !strings.Contains(joined, "--no-owner") || !strings.Contains(joined, "--no-acl") {
		t.Fatalf("pg_dump flags changed: %s", joined)
	}
}

func TestRedisDefaultAdminKeepsACL(t *testing.T) {
	e := NewRedisACL()
	cmd := strings.ToLower(strings.Join(e.SetUserCommand("default", "pw", "full", true), " "))
	if !strings.Contains(cmd, "+@all") || !strings.Contains(cmd, "+acl") {
		t.Fatalf("default user lost admin/ACL access: %s", cmd)
	}
	for _, forbidden := range []string{"-acl", "-@admin", "-@dangerous", "-config"} {
		if strings.Contains(cmd, forbidden) {
			t.Fatalf("default user has app-user restriction %s: %s", forbidden, cmd)
		}
	}
}

func TestMySQLParseUsersHonorsAccountLocked(t *testing.T) {
	e := NewMySQL()
	users, err := e.ParseUsers([]byte("app\t%\tN\tY\nopen\t%\tN\tN\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %+v", users)
	}
	if users[0].Enabled {
		t.Fatalf("locked user reported enabled: %+v", users[0])
	}
	if !users[1].Enabled {
		t.Fatalf("unlocked user reported disabled: %+v", users[1])
	}
}

func TestMySQLListUsersSelectsAccountLocked(t *testing.T) {
	mysqlCmd, err := NewMySQL().ListUsersCommand()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(mysqlCmd, " "), "account_locked") {
		t.Fatalf("mysql user query omits account_locked: %v", mysqlCmd)
	}
	mariaCmd, err := NewMariaDB().ListUsersCommand()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(mariaCmd, " "), "account_locked") {
		t.Fatalf("mariadb user query uses a mysql-only column: %v", mariaCmd)
	}
}

func TestPostgresListUsersIncludesDisabledRoles(t *testing.T) {
	e := NewPostgreSQL()
	command, err := e.ListUsersCommand()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	if strings.Contains(joined, "WHERE rolcanlogin") {
		t.Fatalf("postgres user query hides disabled roles: %s", joined)
	}
	users, err := e.ParseUsers([]byte("app\tt\tf\nolduser\tf\tf\npostgres\tt\tt\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "app" || !users[0].Enabled {
		t.Fatalf("users = %+v", users)
	}
	if users[1].Username != "olduser" || users[1].Enabled {
		t.Fatalf("disabled role = %+v", users[1])
	}
}

func TestExistingUsernamesAllowDotsAndDashes(t *testing.T) {
	for _, e := range []Engine{NewMySQL(), NewMariaDB(), NewPostgreSQL()} {
		if err := e.ValidateUsername("app-user"); err == nil {
			t.Fatalf("%s accepted a dot/dash name for a new user", e.ID())
		}
	}
	mysql := NewMySQL()
	if _, err := mysql.GrantSQL("app-user", "%", "app", model.DatabaseGrant{Database: "app", Preset: model.GrantReadOnly}); err != nil {
		t.Fatalf("mysql grant for existing dashed user: %v", err)
	}
	if _, err := mysql.SetPasswordSQL("app.user", "%", "pw"); err != nil {
		t.Fatalf("mysql password for existing dotted user: %v", err)
	}
	if _, err := mysql.DeleteUserSQL("app-user", "%"); err != nil {
		t.Fatalf("mysql delete for existing dashed user: %v", err)
	}
	pg := NewPostgreSQL()
	if _, err := pg.GrantSQL("app-user", "", "app", model.DatabaseGrant{Database: "app", Preset: model.GrantReadOnly}); err != nil {
		t.Fatalf("postgres grant for existing dashed role: %v", err)
	}
	if _, err := pg.SetPasswordSQL("app.user", "", "pw"); err != nil {
		t.Fatalf("postgres password for existing dotted role: %v", err)
	}
	if _, err := pg.DeleteUserSQL("app-user", ""); err != nil {
		t.Fatalf("postgres delete for existing dashed role: %v", err)
	}
	if err := ValidateExistingUsername(model.DatabaseMySQL, "app-user"); err != nil {
		t.Fatalf("existing username validation rejected app-user: %v", err)
	}
	if err := ValidateExistingUsername(model.DatabaseMySQL, "app;drop"); err == nil {
		t.Fatal("existing username validation accepted injection")
	}
}

func TestPostgresListDatabasesCountsTables(t *testing.T) {
	e := NewPostgreSQL()
	command, err := e.ListDatabasesCommand()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, "pg_class") || !strings.Contains(joined, "count(") {
		t.Fatalf("postgres database inventory does not count tables: %s", joined)
	}
	if strings.Contains(joined, ", 0 FROM pg_database") {
		t.Fatalf("postgres database inventory still hardcodes zero tables: %s", joined)
	}
	databases, err := e.ParseDatabases([]byte("app\t1024\t7\n"))
	if err != nil || len(databases) != 1 || databases[0].TableCount != 7 {
		t.Fatalf("databases = %+v, %v", databases, err)
	}
}
