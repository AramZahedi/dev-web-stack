# Windows/WSL Agent Database Adapters Lane Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in an isolated worktree. Steps use checkbox (`- [ ]`) syntax for tracking. Do not merge; the controller merges after audit.

**Goal:** Implement the Windows/WSL agent path for database/user management, grants, Redis ACL, streaming backup/restore/import/export, and the typed agent client.

**Architecture:** Concrete engine SQL/validation/parsing lives in `internal/dbengine` and is shared with the Linux helper lane. The agent executes fixed `docker compose exec -T` commands, stores database-user secrets under its service tree, and streams dumps/imports. The daemon talks to it through a typed `agentclient` surface. This lane owns no daemon API, UI, or CLI code.

**Tech Stack:** Go, Docker Compose exec, MySQL/MariaDB/PostgreSQL/Redis CLI tools, `crypto/rand`, gzip, SQLite-free agent execution.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Foundation:** `docs/superpowers/plans/2026-10-30-database-management-00-foundation.md`

**Base:** the frozen foundation SHA recorded in `.superpowers/db-foundation-sha.txt`.

## Global Constraints

- Only fixed, validated commands; no generic SQL endpoint and no shell interpolation of user input.
- Passwords never appear in argv; use stdin or container secret files.
- Database-user secrets live under `<agent-root>/services/<service>/secrets/database-users/<userID>`, 0700 directory / 0600 file, symlink and traversal rejected.
- Backups live under `<agent-root>/backups/databases/<service>/<database>/<timestamp>.sql.gz`; imports stage under the same tree and are deleted after the job.
- MySQL/MariaDB rename returns `dbengine.ErrUnsupported`; PostgreSQL rename uses `ALTER DATABASE`.
- Redis `full` excludes admin/dangerous commands; Redis has no logical database or backup operations.
- Mutations require the service to be installed and active. Backup list/download/delete may work while stopped.
- Long streams must not hold the global `opMu`; use per-service locks and honor request cancellation.
- Agent routes are behind the existing bearer/protocol auth; do not add an unauthenticated route.
- Existing agent/service tests must stay green.

## Review Focus

- A malicious database/user/host name must be rejected before any command is built.
- A password must never appear in a fake runner's argv or in an error string; it must be on stdin or in a private file.
- MySQL/MariaDB rename must fail before any command is executed.
- A failed restore/import must leave a recoverable backup/staging state and a clear error, not a silent partial success.
- Redis `full` must not grant `CONFIG`, `FLUSHALL`, `ACL`, or other admin/dangerous commands.

## Files Owned

- Create: `internal/dbengine/mysql.go`
- Create: `internal/dbengine/mariadb.go`
- Create: `internal/dbengine/postgres.go`
- Create: `internal/dbengine/redis.go`
- Create: `internal/dbengine/registry.go`
- Create: `internal/dbengine/engine_test.go`
- Create: `internal/agentdocker/database.go`
- Create: `internal/agentdocker/database_exec.go`
- Create: `internal/agentdocker/database_secrets.go`
- Create: `internal/agentdocker/database_databases.go`
- Create: `internal/agentdocker/database_users.go`
- Create: `internal/agentdocker/database_redis.go`
- Create: `internal/agentdocker/database_backup.go`
- Create: `internal/agentdocker/database_stream.go`
- Create: `internal/agentdocker/database_http.go`
- Modify: `internal/agentdocker/docker.go` (route registration and streaming runner interfaces only)
- Create: `internal/agentclient/databases.go`
- Create: `internal/agentclient/databases_test.go`
- Modify: `internal/agentproto/protocol.go` (only `SiteSpec.DatabaseLinks`)
- Modify: `internal/agent/runtime.go` (resolve link secrets into site env)
- Modify: `internal/agent/terminal.go` (terminal env uses resolved site env)
- Test: `internal/agent/runtime_test.go`, `internal/agent/terminal_test.go`
- Test: colocated `*_test.go` in `internal/dbengine` and `internal/agentdocker`

Do not edit `internal/dbengine/contracts.go`, `internal/dbproto`, `internal/databases`, `internal/store`, `internal/api`, `internal/helper*`, or any UI/CLI file. Request a foundation amendment if a contract is missing.

## Interfaces

Consumes from foundation:

```go
// internal/dbengine/contracts.go
type Engine interface { ... }
type RedisACL interface { ... }
type Registry interface { For(model.DatabaseEngine) (Engine, error) }
var ErrUnsupported, ErrInvalidIdentifier, ErrProtected error

// internal/dbproto
DatabaseInfo, UserInfo, GrantInfo, BackupInfo, JobInfo, LinkInfo,
DatabaseListRequest, DatabaseCreateRequest, ... path helpers
```

Produces for the controller:

```go
// registry.go sets this variable in init; tests call it.
dbengine.DefaultRegistry func() dbengine.Registry
type agentclient.DatabaseAPI interface { ... } // implemented by *agentclient.Client
```

The daemon lane consumes `agentclient.DatabaseAPI` and the concrete `*agentclient.Client`.

---

### Task 1: Concrete engine implementations

**Files:**
- Create: `internal/dbengine/mysql.go`
- Create: `internal/dbengine/mariadb.go`
- Create: `internal/dbengine/postgres.go`
- Create: `internal/dbengine/redis.go`
- Create: `internal/dbengine/registry.go`
- Test: `internal/dbengine/engine_test.go`

**Interfaces:**
- Produces `NewMySQL()`, `NewMariaDB()`, `NewPostgreSQL()`, `NewRedis()`, and sets `dbengine.DefaultRegistry` in `registry.go`'s `init`.
- Implements every `dbengine.Engine` method from the foundation.

- [ ] **Step 1: Write the failing engine tests**

```go
func TestMySQLRenameUnsupported(t *testing.T) {
    e := NewMySQL()
    if e.RenameSupported() { t.Fatal("mysql rename reported supported") }
    if _, err := e.RenameDatabaseSQL("app", "app2"); !errors.Is(err, ErrUnsupported) {
        t.Fatalf("rename err = %v, want ErrUnsupported", err)
    }
}

func TestPostgresRenameSQL(t *testing.T) {
    e := NewPostgreSQL()
    sql, err := e.RenameDatabaseSQL("app", "app2")
    if err != nil || sql != `ALTER DATABASE "app" RENAME TO "app2"` {
        t.Fatalf("sql=%q err=%v", sql, err)
    }
}

func TestIdentifiersRejectInjection(t *testing.T) {
    for _, e := range []Engine{NewMySQL(), NewMariaDB(), NewPostgreSQL()} {
        if err := e.ValidateDatabaseName("app; DROP DATABASE mysql"); err == nil {
            t.Fatalf("%s accepted injection", e.ID())
        }
        if err := e.ValidateUsername("app' --"); err == nil {
            t.Fatalf("%s accepted user injection", e.ID())
        }
    }
}

func TestRedisFullExcludesAdmin(t *testing.T) {
    e := NewRedis().(RedisACL)
    cmd := strings.Join(e.SetUserCommand("cache", "pw", "full", true), " ")
    for _, forbidden := range []string{"+@admin", "+@dangerous", "+config", "+flushall"} {
        if strings.Contains(strings.ToLower(cmd), forbidden) {
            t.Fatalf("full ACL grants %s: %s", forbidden, cmd)
        }
    }
    if !strings.Contains(cmd, "+@all") || !strings.Contains(cmd, "-@admin") || !strings.Contains(cmd, "-@dangerous") {
        t.Fatalf("full ACL = %s", cmd)
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/dbengine -run 'TestMySQLRenameUnsupported|TestPostgresRenameSQL|TestIdentifiersRejectInjection|TestRedisFullExcludesAdmin' -count=1`
Expected: FAIL, constructors missing.

- [ ] **Step 3: Implement the engines**

MySQL/MariaDB:

- list databases via `information_schema.SCHEMATA` plus table/size aggregation; output tab-separated.
- create/drop database; protect `mysql`, `information_schema`, `performance_schema`, `sys`, `devstack`.
- users via `mysql.user`; protect `root`, `mysql.sys`, `mysql.session`, `mysql.infoschema`, `devstack`; host is part of identity.
- grants via `SHOW GRANTS FOR 'user'@'host'`; map presets to `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `ALTER`, `DROP`, `INDEX`, `REFERENCES`, `CREATE TEMPORARY TABLES`, `LOCK TABLES`, `EXECUTE`, `CREATE VIEW`, `SHOW VIEW`, `TRIGGER` as appropriate.
- create/alter/drop user; `IDENTIFIED BY` uses `QuoteLiteral` on a generated/external password.
- terminate sessions SQL from `information_schema.PROCESSLIST`.
- dump/restore commands: `mysqldump`/`mariadb-dump` with `--single-transaction --routines --triggers --events --databases <db>`; restore `mysql`/`mariadb` `<db>`.
- `RenameSupported() = false`.

PostgreSQL:

- list databases via `pg_database` and `pg_database_size`; users via `pg_roles` where `rolcanlogin`; protect `postgres`, `template0`, `template1`, `devstack`.
- create/drop/rename database; create/alter/drop role; grants for connect, schema usage, table/sequence privileges, default privileges.
- terminate sessions via `pg_stat_activity` and `pg_terminate_backend`.
- dump `pg_dump --no-owner --no-acl --format=plain <db>`; restore `psql --set=ON_ERROR_STOP=1 <db>`.
- `RenameSupported() = true`.

Redis:

- implement `Engine` methods that return `ErrUnsupported` for logical databases, grants, dumps, and restores.
- implement `RedisACL`: `ACL LIST`, `ACL SETUSER`, `ACL DELUSER`, `ACL SAVE`; access levels:
  - `read-only`: `reset on >password ~* +@read -@admin -@dangerous` plus connection commands.
  - `full`: `reset on >password ~* +@all -@admin -@dangerous` plus explicit exclusions for `CONFIG`, `DEBUG`, `SHUTDOWN`, `FLUSHALL`, `FLUSHDB`, `ACL`, `MODULE`, `REPLICAOF`, `SLAVEOF`, `MIGRATE`, `SAVE`, `BGSAVE`, `BGREWRITEAOF`, `SWAPDB`, `CLUSTER`, `FAILOVER`.
- `ACLFile() = "/data/users.acl"`.

Registry: `registry.go` sets `dbengine.DefaultRegistry = func() Registry { ... }` returning a map for `mysql`, `mariadb`, `postgres`, `redis`; unknown engine returns `ErrUnsupported`.

- [ ] **Step 4: Re-run the engine tests**

Run: `go test ./internal/dbengine -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/dbengine
git commit -m "feat(dbengine): implement SQL and Redis engines"
```

---

### Task 2: Agent execution, locks, and secrets

**Files:**
- Create: `internal/agentdocker/database.go`
- Create: `internal/agentdocker/database_exec.go`
- Create: `internal/agentdocker/database_secrets.go`
- Modify: `internal/agentdocker/docker.go` (add `StreamRunner`/`InputStreamRunner` interfaces and implementations)
- Test: `internal/agentdocker/database_test.go`

**Interfaces:**
- Produces `DatabaseManager` on `*Docker` with a registry, per-service locks, command helpers, and secret read/write.
- Adds `StreamRunner`/`InputStreamRunner`; `execRunner` implements them.

- [ ] **Step 1: Write the failing tests**

```go
func TestDatabaseSecretRejectsTraversal(t *testing.T) {
    d := newTestDocker(t)
    if err := d.writeDatabaseSecret("mysql", "../root", "x"); err == nil {
        t.Fatal("accepted traversal user ID")
    }
}

func TestDatabaseSecretIsPrivate(t *testing.T) {
    d := newTestDocker(t)
    if err := d.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil { t.Fatal(err) }
    path := filepath.Join(d.root, "services", "mysql", "secrets", "database-users", "usr_app")
    info, err := os.Stat(path)
    if err != nil || info.Mode().Perm() != 0o600 { t.Fatalf("mode=%v err=%v", info.Mode(), err) }
}

func TestPerServiceLocksAreIndependent(t *testing.T) {
    d := newTestDocker(t)
    if d.databaseLock("mysql") == d.databaseLock("postgres") {
        t.Fatal("services share a database lock")
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/agentdocker -run 'TestDatabaseSecret|TestPerServiceLocks' -count=1`
Expected: FAIL, methods missing.

- [ ] **Step 3: Implement execution and secrets**

Add to `docker.go`:

```go
type StreamRunner interface {
    RunStream(ctx context.Context, name string, args ...string) (io.ReadCloser, error)
}
type InputStreamRunner interface {
    RunInputStream(ctx context.Context, input io.Reader, name string, args ...string) error
}
```

Implement both on `execRunner` with `exec.CommandContext`, `StdoutPipe`/`StdinPipe`, `WaitDelay = 3 * time.Second`, and context cancellation. Keep the existing `Run`/`RunInput` behavior unchanged.

`database.go`: add `databaseRegistry dbengine.Registry`, `databaseLocks sync.Map`, `databaseLock(serviceID) *sync.Mutex`, and `composeExecStream`/`composeExecInput` helpers that call `docker compose --project-name devstack --file <root>/services/compose.yaml exec -T <service> ...`. Do not hold `opMu` for database operations.

`database_exec.go`: helpers `runDatabaseSQL(ctx, serviceID, command []string, input []byte) ([]byte, error)` and `runDatabaseStream`/`runDatabaseInput`; validate service ID and active status before mutation. Use the existing `Status` method.

`database_secrets.go`:

```go
func (d *Docker) databaseSecretPath(serviceID, userID string) (string, error)
func (d *Docker) writeDatabaseSecret(serviceID, userID, value string) error
func (d *Docker) readDatabaseSecret(serviceID, userID string) (string, error)
func (d *Docker) deleteDatabaseSecret(serviceID, userID string) error
```

Validate `serviceID` against the fixed catalog and `userID` with `^[A-Za-z0-9_-]{1,64}$`; reject `.`/`..`/slashes; ensure the directory is 0700 and not a symlink; write atomically with 0600; read only a regular 0600 file.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/agentdocker -run 'TestDatabaseSecret|TestPerServiceLocks' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/agentdocker/database.go internal/agentdocker/database_exec.go internal/agentdocker/database_secrets.go internal/agentdocker/docker.go internal/agentdocker/database_test.go
git commit -m "feat(agent): add database execution and secret storage"
```

---

### Task 3: Database and user operations

**Files:**
- Create: `internal/agentdocker/database_databases.go`
- Create: `internal/agentdocker/database_users.go`
- Test: `internal/agentdocker/database_operations_test.go`

**Interfaces:**
- Produces `ListDatabases`, `CreateDatabase`, `RenameDatabase`, `DropDatabase`, `ListUsers`, `CreateUser`, `UpdateUser`, `DeleteUser`, `SetUserPassword`, `RevealUserPassword` on `*Docker`.

- [ ] **Step 1: Write the failing operation tests**

```go
func TestCreateDatabaseUsesValidatedSQL(t *testing.T) {
    d, runner := newDatabaseTestDocker(t)
    _, err := d.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app"})
    if err != nil { t.Fatal(err) }
    if !runner.contains("CREATE DATABASE `app`") { t.Fatalf("calls=%v", runner.calls) }
}

func TestCreateDatabaseRejectsInjection(t *testing.T) {
    d, runner := newDatabaseTestDocker(t)
    _, err := d.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app; DROP DATABASE mysql"})
    if err == nil || len(runner.calls) != 0 { t.Fatalf("err=%v calls=%v", err, runner.calls) }
}

func TestCreateUserPasswordIsNotInArgv(t *testing.T) {
    d, runner := newDatabaseTestDocker(t)
    user, err := d.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app", Host: "%"})
    if err != nil { t.Fatal(err) }
    if user.PasswordManaged != true { t.Fatalf("user=%+v", user) }
    for _, call := range runner.calls {
        if strings.Contains(strings.Join(call.args, " "), "s3cret") { t.Fatalf("password in argv: %v", call.args) }
    }
    if !runner.stdinContains("IDENTIFIED BY") { t.Fatal("password SQL not sent on stdin") }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/agentdocker -run 'TestCreateDatabase|TestCreateUserPassword' -count=1`
Expected: FAIL, methods missing.

- [ ] **Step 3: Implement the operations**

For each operation:

1. Resolve the service and engine; check active for mutations.
2. Validate identifiers via `dbengine.Engine`; reject protected objects with `databases.ErrProtected`.
3. Build the SQL/command from the engine; run it via `composeExecInput` with SQL on stdin where a password is involved.
4. Parse list output with the engine parser; merge devstack metadata only where needed (agent does not own SQLite).
5. For `CreateUser`, generate a password with `credentials.NewSecret()`, run `CREATE USER ... IDENTIFIED BY ...` on stdin, then write the secret. If the secret write fails, return an error and mark the response `PasswordManaged=false`; do not report success.
6. For `SetUserPassword`, generate, run `ALTER USER`, then write the secret; on failure, return an error and keep the old secret if the engine change did not apply.
7. For `DeleteUser`, check linked-site metadata? The agent does not have SQLite; the daemon enforces linked-site blocking. The agent accepts `Confirm` and `ReassignOwned`.
8. For PostgreSQL delete with `ReassignOwned`, run `REASSIGN OWNED BY ... TO ...` per database before `DROP ROLE`; only when explicitly requested.
9. For `DropDatabase`, require `Confirm == name`, terminate sessions, then drop. If `BackupFirst`, the daemon orchestrates the backup before calling drop; the agent does not create it implicitly.
10. For `RenameDatabase`, check `RenameSupported()` first; MySQL/MariaDB return `dbengine.ErrUnsupported` without a command.
11. For `RevealUserPassword`, read the agent secret; if missing, return `databases.ErrPasswordMissing`.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/agentdocker -run 'TestCreateDatabase|TestCreateUserPassword|TestRename|TestDrop|TestReveal' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/agentdocker/database_databases.go internal/agentdocker/database_users.go internal/agentdocker/database_operations_test.go
git commit -m "feat(agent): implement database and user operations"
```

---

### Task 4: Backup, restore, import, and export

**Files:**
- Create: `internal/agentdocker/database_backup.go`
- Create: `internal/agentdocker/database_stream.go`
- Test: `internal/agentdocker/database_backup_test.go`

**Interfaces:**
- Produces `ListBackups`, `CreateBackup`, `DeleteBackup`, `RestoreBackup`, `ImportDatabase`, `ExportDatabase`, `DownloadBackup` on `*Docker`.

- [ ] **Step 1: Write the failing backup tests**

```go
func TestCreateBackupWritesGzipAndMetadata(t *testing.T) {
    d, runner := newDatabaseTestDocker(t)
    runner.streamOutput = []byte("CREATE TABLE t (id int);\n")
    job, err := d.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
    if err != nil || job.Status != model.JobSucceeded { t.Fatalf("job=%+v err=%v", job, err) }
    path := filepath.Join(d.root, "backups", "databases", "mysql", "app")
    matches, _ := filepath.Glob(filepath.Join(path, "*.sql.gz"))
    if len(matches) != 1 { t.Fatalf("backups=%v", matches) }
    data, _ := os.ReadFile(matches[0])
    if len(data) == 0 { t.Fatal("empty backup") }
}

func TestImportRejectsOverOneGiB(t *testing.T) {
    d, _ := newDatabaseTestDocker(t)
    err := d.ImportDatabase(context.Background(), "mysql", "app", io.LimitReader(zeroReader{}, (1<<30)+1), "big.sql", databases.ImportRequest{Mode: databases.ImportMerge})
    if !errors.Is(err, databases.ErrInvalidRequest) { t.Fatalf("err=%v", err) }
}

func TestExportStreamsWithoutBuffering(t *testing.T) {
    d, runner := newDatabaseTestDocker(t)
    runner.streamOutput = []byte("dump")
    body, err := d.ExportDatabase(context.Background(), "mysql", "app")
    if err != nil { t.Fatal(err) }
    data, _ := io.ReadAll(body)
    if string(data) != "dump" { t.Fatalf("export=%q", data) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/agentdocker -run 'TestCreateBackup|TestImportRejects|TestExportStreams' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement the streaming path**

Backups:

- Path `<root>/backups/databases/<service>/<database>/<UTC timestamp>.sql.gz`; directory 0700, file 0600.
- Run the engine `DumpCommand`, wrap with the root secret env (`MYSQL_PWD`/`MARIADB_PWD`/`PGPASSWORD`) via `sh -c` and `cat /run/secrets/...`; stream stdout through `gzip.NewWriter` to a temp file, `Sync`, `Close`, rename.
- Compute SHA-256 and size; return a `model.DatabaseJob` with `Status: succeeded`, `BackupID` set by the daemon (the agent returns a stable file name; the daemon stores metadata).
- `ListBackups` walks the service backup tree, parses filenames, returns metadata; it does not require active.
- `DeleteBackup` validates the backup ID/path under the service tree and removes the file; no traversal.
- `DownloadBackup` streams the file; no active requirement.

Restore/import:

- Require active. `RestoreBackup` reads the backup file, optionally creates a pre-restore backup (the daemon orchestrates; the agent can create one if `PreRestoreBackup`), terminates sessions, and pipes the decompressed dump to the engine restore command. Overwrite drops/recreates the target first; typed `Confirm` must equal the target. New-target restore creates a database named `Target` and imports.
- `ImportDatabase` reads the upload into a 0600 staging file under `backups/databases/<service>/.staging/<random>.sql.gz`, enforces 1 GiB with `http.MaxBytesReader` at the handler and a counting reader here, optionally creates a pre-import backup, then restores with `Merge` or `Replace`. Delete the staging file in `defer`.
- `ExportDatabase` streams the dump directly; no file and no active requirement? Require active.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/agentdocker -run 'TestCreateBackup|TestImportRejects|TestExportStreams|TestRestore|TestDeleteBackup' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/agentdocker/database_backup.go internal/agentdocker/database_stream.go internal/agentdocker/database_backup_test.go
git commit -m "feat(agent): stream database backups and imports"
```

---

### Task 5: Redis ACL operations

**Files:**
- Create: `internal/agentdocker/database_redis.go`
- Test: `internal/agentdocker/database_redis_test.go`

**Interfaces:**
- Produces `ListRedisUsers`, `SetRedisUser`, `DeleteRedisUser`, `SetRedisPassword`, `RevealRedisPassword`, `EnableRedisACLFile` on `*Docker`.

- [ ] **Step 1: Write the failing Redis tests**

```go
func TestRedisFullDoesNotGrantAdmin(t *testing.T) {
    d, runner := newDatabaseTestDocker(t)
    _, err := d.SetRedisUser(context.Background(), databases.CreateUserRequest{Username: "cache", AccessLevel: "full"})
    if err != nil { t.Fatal(err) }
    joined := runner.joinedCalls()
    if strings.Contains(joined, "+@admin") || strings.Contains(joined, "+@dangerous") {
        t.Fatalf("admin grant: %s", joined)
    }
    if !strings.Contains(joined, "ACL SETUSER") || !strings.Contains(joined, "ACL SAVE") {
        t.Fatalf("calls=%s", joined)
    }
}

func TestRedisACLFileIsEnabledOnce(t *testing.T) {
    d, runner := newDatabaseTestDocker(t)
    if err := d.EnableRedisACLFile(context.Background()); err != nil { t.Fatal(err) }
    if !strings.Contains(runner.joinedCalls(), "aclfile /data/users.acl") { t.Fatalf("calls=%s", runner.joinedCalls()) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/agentdocker -run 'TestRedisFull|TestRedisACLFile' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement Redis ACL**

- `ListRedisUsers` runs `ACL LIST`, parses users, maps `on`/`off`, password presence, and access level (`full` if `+@all`, `read-only` if `+@read`, otherwise `custom`).
- `SetRedisUser` runs `ACL SETUSER <user> reset on >password ~* +@all -@admin -@dangerous ...` for full or `+@read` for read-only; `ACL SAVE` after. Password on stdin? `redis-cli ACL SETUSER` takes the password as an argument. To avoid argv, use `redis-cli -x`? `ACL SETUSER` reads arguments, not stdin. The agent can write a temporary ACL file and `ACL LOAD`, or use `redis-cli --pipe` with a RESP command. Simpler: use `redis-cli -a <admin>`? The password would be in argv. Better: use `redis-cli` with `REDISCLI_AUTH` env from the container secret file, and pass the new user password in a `RESP` payload on stdin via `redis-cli --pipe`. Implement `ACL SETUSER` as a RESP array on stdin; no password in argv.
- `DeleteRedisUser` runs `ACL DELUSER` (username is not secret) and `ACL SAVE`; protect `default`.
- `SetRedisPassword` for `default` uses `ACL SETUSER default reset on >password ~* +@all -@admin -@dangerous` or `CONFIG SET requirepass`? Prefer ACL. `RevealRedisPassword` reads the agent secret for the Redis user.
- `EnableRedisACLFile` backs up `redis.conf`, adds `aclfile /data/users.acl` if absent, seeds `users.acl` from `ACL LIST`, and restarts Redis. Require explicit confirmation at the daemon layer; the agent method is idempotent.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/agentdocker -run 'TestRedis' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/agentdocker/database_redis.go internal/agentdocker/database_redis_test.go
git commit -m "feat(agent): add Redis ACL management"
```

---

### Task 6: Agent HTTP routes and typed client

**Files:**
- Create: `internal/agentdocker/database_http.go`
- Modify: `internal/agentdocker/docker.go` (`Handler()` route registration only)
- Create: `internal/agentclient/databases.go`
- Create: `internal/agentclient/databases_test.go`
- Test: `internal/agentdocker/database_http_test.go`

**Interfaces:**
- Produces the agent routes under `/v1/services/{id}/...` and `agentclient.DatabaseAPI`.

- [ ] **Step 1: Write the failing route/client tests**

```go
func TestModuleMountsDatabaseRoutes(t *testing.T) {
    d := newTestDocker(t)
    mux := http.NewServeMux()
    d.Handler().ServeHTTP(mux, httptest.NewRequest(http.MethodGet, "/v1/services/mysql/databases", nil))
    // Assert the route is registered and rejects a missing/invalid body with 400.
}
```

```go
func TestAgentClientDatabaseCRUD(t *testing.T) {
    srv := newTLSServer(t, authHandler(t, testToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, dbproto.DatabaseListResponse{Databases: []dbproto.DatabaseInfo{{Name: "app"}}})
    })))
    c := newTestClient(t, srv.URL)
    got, err := c.ListDatabases(context.Background(), "mysql")
    if err != nil || len(got) != 1 || got[0].Name != "app" { t.Fatalf("got=%v err=%v", got, err) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/agentdocker -run TestModuleMountsDatabaseRoutes -count=1; go test ./internal/agentclient -run TestAgentClientDatabaseCRUD -count=1`
Expected: FAIL.

- [ ] **Step 3: Register routes and implement the client**

In `Docker.Handler()`, add:

```go
mux.HandleFunc("GET /v1/services/{id}/databases", d.handleDatabaseList)
mux.HandleFunc("POST /v1/services/{id}/databases", d.handleDatabaseCreate)
mux.HandleFunc("PATCH /v1/services/{id}/databases/{name}", d.handleDatabaseRename)
mux.HandleFunc("DELETE /v1/services/{id}/databases/{name}", d.handleDatabaseDrop)
mux.HandleFunc("GET /v1/services/{id}/users", d.handleUserList)
mux.HandleFunc("POST /v1/services/{id}/users", d.handleUserCreate)
mux.HandleFunc("PATCH /v1/services/{id}/users/{userID}", d.handleUserUpdate)
mux.HandleFunc("DELETE /v1/services/{id}/users/{userID}", d.handleUserDelete)
mux.HandleFunc("POST /v1/services/{id}/users/{userID}/password", d.handleUserPassword)
mux.HandleFunc("POST /v1/services/{id}/users/{userID}/reveal", d.handleUserReveal)
mux.HandleFunc("GET /v1/services/{id}/backups", d.handleBackupList)
mux.HandleFunc("POST /v1/services/{id}/databases/{name}/backups", d.handleBackupCreate)
mux.HandleFunc("GET /v1/services/{id}/backups/{backupID}/download", d.handleBackupDownload)
mux.HandleFunc("DELETE /v1/services/{id}/backups/{backupID}", d.handleBackupDelete)
mux.HandleFunc("POST /v1/services/{id}/backups/{backupID}/restore", d.handleBackupRestore)
mux.HandleFunc("POST /v1/services/{id}/databases/{name}/import", d.handleDatabaseImport)
mux.HandleFunc("GET /v1/services/{id}/databases/{name}/export", d.handleDatabaseExport)
mux.HandleFunc("POST /v1/services/{id}/database-env", d.handleDatabaseEnv)
```

`handleDatabaseEnv` accepts `dbproto.SiteEnvRequest`, reads the requested links' secrets for that service, and returns `dbproto.SiteEnvResponse` with `DB_*`/`DATABASE_URL`. It never returns a secret for a metadata-only link and never logs the values.

Handlers use `decodeRequest` for JSON (1 MiB), validate path/body IDs, map errors to 400/404/409/422, and stream import/export/download with `http.MaxBytesReader` (1 GiB) and `Content-Disposition`. Import reads query `mode`, `confirm`, `pre_import_backup`.

`agentclient.DatabaseAPI`:

```go
type DatabaseAPI interface {
    ListDatabases(ctx, serviceID string) ([]dbproto.DatabaseInfo, error)
    CreateDatabase(ctx, serviceID string, req dbproto.DatabaseCreateRequest) (dbproto.DatabaseInfo, error)
    RenameDatabase(ctx, serviceID, name string, req dbproto.DatabaseRenameRequest) (dbproto.DatabaseInfo, error)
    DropDatabase(ctx, serviceID, name string, req dbproto.DatabaseDropRequest) error
    ListUsers(ctx, serviceID string) ([]dbproto.UserInfo, error)
    CreateUser(ctx, serviceID string, req dbproto.UserCreateRequest) (dbproto.UserInfo, error)
    UpdateUser(ctx, serviceID, userID string, req dbproto.UserUpdateRequest) (dbproto.UserInfo, error)
    DeleteUser(ctx, serviceID, userID string, req dbproto.UserDeleteRequest) error
    SetUserPassword(ctx, serviceID, userID string) (dbproto.SecretValue, error)
    RevealUserPassword(ctx, serviceID, userID string) (dbproto.SecretValue, error)
    ListBackups(ctx, serviceID string) ([]dbproto.BackupInfo, error)
    CreateBackup(ctx, serviceID, database string, req dbproto.BackupCreateRequest) (dbproto.JobInfo, error)
    DeleteBackup(ctx, serviceID, backupID string) error
    RestoreBackup(ctx, serviceID, backupID string, req dbproto.BackupRestoreRequest) (dbproto.JobInfo, error)
    ImportDatabase(ctx, serviceID, database string, file io.Reader, filename string, req dbproto.DatabaseImportRequest) (dbproto.JobInfo, error)
    ExportDatabase(ctx, serviceID, database string) (io.ReadCloser, error)
    DownloadBackup(ctx, serviceID, backupID string) (io.ReadCloser, error)
    GetJob(ctx, jobID string) (dbproto.JobInfo, error)
    CancelJob(ctx, jobID string) error
    ResolveSiteEnv(ctx, serviceID string, req dbproto.SiteEnvRequest) (map[string]string, error)
}
```

Implement on `*Client` with `do` for metadata, `doLong` for backup/restore/import, and a new `openStream` helper that sets protocol/auth headers and returns the body. `ImportDatabase` uses a raw-body request with `Content-Type: application/gzip` or `text/plain`, query params, and the filename header. `DownloadBackup`/`ExportDatabase` use `openStream` GET.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/agentdocker -run 'TestModuleMountsDatabaseRoutes|TestDatabase' -count=1; go test ./internal/agentclient -run TestAgentClientDatabase -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/agentdocker/database_http.go internal/agentdocker/docker.go internal/agentdocker/database_http_test.go internal/agentclient/databases.go internal/agentclient/databases_test.go
git commit -m "feat(agent): expose typed database routes and client"
```

---

### Task 7: Agent site env resolution

**Files:**
- Modify: `internal/agentproto/protocol.go` (add `DatabaseLinks []DatabaseLinkSpec` to `SiteSpec`)
- Modify: `internal/agent/runtime.go`
- Modify: `internal/agent/terminal.go`
- Test: `internal/agent/runtime_test.go`
- Test: `internal/agent/terminal_test.go`

**Interfaces:**
- Consumes `SiteSpec.DatabaseLinks` populated by the daemon lane.
- Produces resolved `DB_*`/`DATABASE_URL` values in `SiteSpec.Env` before the runtime prepares a site; terminals inherit them.

- [ ] **Step 1: Write the failing env tests**

```go
func TestPrepareSiteInjectsManagedDatabaseEnv(t *testing.T) {
    rt, root := newAgentRuntimeFixture(t)
    writeAgentDBSecret(t, root, "mysql", "usr_app", "s3cret")
    spec := agentproto.SiteSpec{
        Name: "blog",
        DatabaseLinks: []agentproto.DatabaseLinkSpec{{ServiceID: "mysql", Database: "app", UserID: "usr_app", Username: "app", Primary: true, PasswordManaged: true}},
    }
    if err := rt.PrepareSite(context.Background(), spec); err != nil { t.Fatal(err) }
    env := rt.Environment(spec.Name)
    if env["DB_PASSWORD"] != "s3cret" || env["DB_DATABASE"] != "app" { t.Fatalf("env=%v", env) }
}

func TestPrepareSiteOmitsMetadataPassword(t *testing.T) {
    rt, _ := newAgentRuntimeFixture(t)
    spec := agentproto.SiteSpec{Name: "blog", DatabaseLinks: []agentproto.DatabaseLinkSpec{{ServiceID: "mysql", Database: "app", Username: "app", Primary: true, PasswordManaged: false}}}
    if err := rt.PrepareSite(context.Background(), spec); err != nil { t.Fatal(err) }
    env := rt.Environment(spec.Name)
    if _, ok := env["DB_PASSWORD"]; ok { t.Fatalf("metadata-only env has password: %v", env) }
    if _, ok := env["DATABASE_URL"]; ok { t.Fatalf("metadata-only env has URL: %v", env) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/agent -run 'TestPrepareSite.*Database' -count=1`
Expected: FAIL, field/methods missing.

- [ ] **Step 3: Implement resolution**

Add to `agentproto.SiteSpec`:

```go
type DatabaseLinkSpec struct {
    ServiceID, Engine, Database, UserID, Username, Host string
    Port int
    Primary, PasswordManaged bool
}
```

In `agent.Runtime.PrepareSite`, after applying `spec.Env`, resolve each link:

- For a managed link, read `<agent-root>/services/<service>/secrets/database-users/<userID>` with the same private-file checks as `agentdocker`; build `DB_CONNECTION`, `DB_HOST=127.0.0.1`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD`, `DATABASE_URL`.
- For a metadata-only link, omit `DB_PASSWORD` and `DATABASE_URL`.
- Primary link wins; additional links use `DB_<SLUG>_*`.
- Link values override `spec.Env`; do not write them back to the daemon.
- Store the merged env in `runtimeSite` so `terminalEnvironment` inherits it.
- If a secret is missing for a `PasswordManaged` link, fail the prepare with a clear error; do not silently omit the password.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/agent -run 'TestPrepareSite.*Database|TestTerminal.*Database' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/agentproto/protocol.go internal/agent/runtime.go internal/agent/terminal.go internal/agent/runtime_test.go internal/agent/terminal_test.go
git commit -m "feat(agent): resolve site database env from secrets"
```

---

### Task 8: Lane audit and fix cycle

- [ ] **Step 1: Run the lane gate**

```powershell
go test ./internal/dbengine ./internal/agentdocker ./internal/agentclient ./internal/agentproto -count=1
go test ./... -count=1
go vet ./...
go build ./...
git status --short
```

Expected: all pass; clean tree after the final commit.

- [ ] **Step 2: Fresh audit**

Dispatch a fresh reviewer with the `superpowers:requesting-code-review` skill. It reads the spec, this plan, the foundation contract, and `git diff $base..HEAD`; runs the lane gate; returns spec-compliance and code-quality verdicts with Critical/Important/Minor findings.

- [ ] **Step 3: Fix and re-audit**

Fix every Critical/Important finding with a failing test first. Commit. Re-dispatch a fresh reviewer. Repeat until no Critical/Important findings.

- [ ] **Step 4: Write the lane report**

Write `.superpowers/lane-report.md` with branch, base SHA, commits, test commands/results, audit verdicts, residual risks, and any Ruling. Do not merge.

- [ ] **Step 5: Commit the final lane state**

```powershell
git add internal/dbengine internal/agentdocker internal/agentclient
git commit -m "chore(agent): finalize database adapter lane"
```

Stop and report the branch, HEAD SHA, lane report path, and audit verdicts to the controller.
