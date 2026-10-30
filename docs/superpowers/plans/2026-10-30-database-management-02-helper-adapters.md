# Linux Helper Database Adapters Lane Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in an isolated worktree. Steps use checkbox (`- [ ]`) syntax for tracking. Do not merge; the controller merges after audit.

**Goal:** Implement the Linux privileged-helper path for database/user management, grants, Redis ACL, streaming backup/restore/import/export, and the helper RPC/client surface.

**Architecture:** The helper reuses the frozen `dbengine` interface and `dbproto` wire contract. It executes fixed `docker compose` commands with the helper’s project name, stores database-user secrets under the helper’s service tree, and streams dumps/imports. The daemon talks to it through `helperclient`. This lane owns no daemon API, UI, or CLI code.

**Tech Stack:** Go, Linux-only helper (`//go:build !windows`), Docker Compose, Unix socket RPC, MySQL/MariaDB/PostgreSQL/Redis CLI tools.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Foundation:** `docs/superpowers/plans/2026-10-30-database-management-00-foundation.md`

**Base:** the frozen foundation SHA recorded in `.superpowers/db-foundation-sha.txt`.

## Global Constraints

- The helper is Linux-only; every new file that imports Linux-only packages carries `//go:build !windows`.
- Only fixed, validated commands; no generic SQL endpoint and no shell interpolation of user input.
- Passwords never appear in argv; use stdin or private secret files.
- Database-user secrets live under `<helper-root>/services/<service>/secrets/database-users/<userID>`, 0700 directory / 0600 file, symlink and traversal rejected.
- Backups live under `<helper-root>/backups/databases/<service>/<database>/<timestamp>.sql.gz`; imports stage under the same tree and are deleted after the job.
- The helper’s Compose project name is `devstack-<root-hash>`; do not reuse the agent’s fixed `devstack` project name.
- MySQL/MariaDB rename returns `dbengine.ErrUnsupported`; PostgreSQL rename uses `ALTER DATABASE`.
- Redis `full` excludes admin/dangerous commands; Redis has no logical database or backup operations.
- Helper RPC requires matching peer UID and `X-Devstack-Protocol: 1`; no unauthenticated route.
- Existing helper/helperclient tests must stay green.

## Review Focus

- A malicious database/user/host name must be rejected before any command is built.
- A password must never appear in a fake runner's argv or in an error string; it must be on stdin or in a private file.
- The helper must use its own Compose project name and must not accidentally target the agent’s containers.
- A failed restore/import must leave a recoverable backup/staging state and a clear error.
- Redis `full` must not grant `CONFIG`, `FLUSHALL`, `ACL`, or other admin/dangerous commands.

## Files Owned

- Create: `internal/helper/service_database.go`
- Create: `internal/helper/service_database_exec.go`
- Create: `internal/helper/service_database_secrets.go`
- Create: `internal/helper/service_database_backup.go`
- Create: `internal/helper/service_database_redis.go`
- Create: `internal/helper/service_database_test.go`
- Modify: `internal/helper/service_docker.go` (`ServiceController` interface, streaming runner interfaces, registry field)
- Modify: `internal/helper/services.go` (database RPC handlers)
- Modify: `internal/helper/server.go` (route hooks)
- Create: `internal/helperproto/databases.go`
- Create: `internal/helperclient/databases.go`
- Create: `internal/helperclient/databases_test.go`
- Test: colocated `*_test.go` in `internal/helper` and `internal/helperproto`

Do not edit `internal/dbengine/contracts.go`, `internal/dbproto`, `internal/databases`, `internal/store`, `internal/api`, `internal/agent*`, or any UI/CLI file. Request a foundation amendment if a contract is missing.

## Interfaces

Consumes from foundation:

```go
// internal/dbengine/contracts.go
type Engine interface { ... }
type Registry interface { For(model.DatabaseEngine) (Engine, error) }
var DefaultRegistry func() Registry
var ErrUnsupported, ErrInvalidIdentifier, ErrProtected error

// internal/dbproto
DatabaseInfo, UserInfo, GrantInfo, BackupInfo, JobInfo, ... path helpers
```

Produces for the controller:

```go
type helperclient.DatabaseAPI interface { ... } // implemented by *helperclient.Client
```

The daemon lane consumes `helperclient.DatabaseAPI` on Linux and `agentclient.DatabaseAPI` on Windows.

---

### Task 1: Helper database execution and secrets

**Files:**
- Create: `internal/helper/service_database.go`
- Create: `internal/helper/service_database_exec.go`
- Create: `internal/helper/service_database_secrets.go`
- Modify: `internal/helper/service_docker.go`
- Test: `internal/helper/service_database_test.go`

**Interfaces:**
- Extends `ServiceController` with the database methods from `databases.Platform` (helper-side types).
- Adds `StreamCommandRunner`/`InputStreamCommandRunner` and a `dbengine.Registry` field.
- Produces `databaseLock`, `databaseSecretPath`, `writeDatabaseSecret`, `readDatabaseSecret`, `deleteDatabaseSecret`.

- [ ] **Step 1: Write the failing helper tests**

```go
func TestHelperDatabaseSecretIsPrivate(t *testing.T) {
    c, root := databaseHelperFixture(t)
    if err := c.writeDatabaseSecret("mysql", "usr_app", "s3cret"); err != nil { t.Fatal(err) }
    path := filepath.Join(root, "services", "mysql", "secrets", "database-users", "usr_app")
    info, err := os.Stat(path)
    if err != nil || info.Mode().Perm() != 0o600 { t.Fatalf("mode=%v err=%v", info.Mode(), err) }
}

func TestHelperUsesItsOwnComposeProject(t *testing.T) {
    c, _ := databaseHelperFixture(t)
    runner := c.runner.(*fakeCommandRunner)
    _, _ = c.ListDatabases(context.Background(), "mysql")
    joined := runner.joinedCalls()
    if !strings.Contains(joined, "devstack-") || strings.Contains(joined, "--project-name devstack ") {
        t.Fatalf("compose calls = %s", joined)
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/helper -run 'TestHelperDatabaseSecret|TestHelperUsesItsOwnComposeProject' -count=1`
Expected: FAIL, methods missing.

- [ ] **Step 3: Implement execution and secrets**

Extend `ServiceController` in `service_docker.go` with the database methods. Add `databaseRegistry dbengine.Registry` to `DockerServiceController`; default to `dbengine.DefaultRegistry` when set, otherwise database operations return `ErrUnsupported`. Add `databaseLocks sync.Map` and `databaseLock(serviceID)`.

`service_database_exec.go`:

- `composeDatabase(ctx, serviceID, args...)` prefixes `docker compose --project-name <project> --file <root>/services/compose.yaml exec -T <service>`.
- `runDatabaseSQL`, `runDatabaseStream`, `runDatabaseInput` use the existing command runner; add streaming interfaces in `service_docker.go` and implement them on the real runner.
- Validate service IDs against the catalog and check active state for mutations.

`service_database_secrets.go`: same rules as the agent, rooted at the helper root. Use atomic temp+rename, 0700/0600, reject symlinks and traversal. The helper process runs as the privileged user; do not chown to the caller.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/helper -run 'TestHelperDatabaseSecret|TestHelperUsesItsOwnComposeProject' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/helper/service_database.go internal/helper/service_database_exec.go internal/helper/service_database_secrets.go internal/helper/service_docker.go internal/helper/service_database_test.go
git commit -m "feat(helper): add database execution and secret storage"
```

---

### Task 2: Helper database and user operations

**Files:**
- Modify: `internal/helper/service_database.go` (operations)
- Test: `internal/helper/service_database_test.go`

**Interfaces:**
- Implements `ListDatabases`, `CreateDatabase`, `RenameDatabase`, `DropDatabase`, `ListUsers`, `CreateUser`, `UpdateUser`, `DeleteUser`, `SetUserPassword`, `RevealUserPassword` on `*DockerServiceController`.

- [ ] **Step 1: Write the failing operation tests**

```go
func TestHelperCreateDatabaseUsesValidatedSQL(t *testing.T) {
    c, _ := databaseHelperFixture(t)
    runner := c.runner.(*fakeCommandRunner)
    _, err := c.CreateDatabase(context.Background(), "mysql", databases.CreateDatabaseRequest{Name: "app"})
    if err != nil { t.Fatal(err) }
    if !runner.contains("CREATE DATABASE `app`") { t.Fatalf("calls=%v", runner.calls) }
}

func TestHelperRenameRejectsMySQL(t *testing.T) {
    c, _ := databaseHelperFixture(t)
    runner := c.runner.(*fakeCommandRunner)
    _, err := c.RenameDatabase(context.Background(), "mysql", "app", databases.RenameDatabaseRequest{Name: "app2"})
    if !errors.Is(err, dbengine.ErrUnsupported) { t.Fatalf("err=%v", err) }
    if len(runner.calls) != 0 { t.Fatalf("calls=%v", runner.calls) }
}

func TestHelperCreateUserPasswordOnStdin(t *testing.T) {
    c, _ := databaseHelperFixture(t)
    runner := c.runner.(*fakeCommandRunner)
    _, err := c.CreateUser(context.Background(), "mysql", databases.CreateUserRequest{Username: "app", Host: "%"})
    if err != nil { t.Fatal(err) }
    if !runner.stdinContains("IDENTIFIED BY") { t.Fatal("password SQL not on stdin") }
    if runner.joinedCallsContains("s3cret") { t.Fatal("password in argv") }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/helper -run 'TestHelperCreateDatabase|TestHelperRename|TestHelperCreateUser' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement the operations**

Mirror the agent operation semantics from `...-01-agent-adapters.md` Task 3, using the helper’s runner and `dbengine`:

- validate identifiers/protected objects before command construction;
- check active for mutations;
- passwords generated with `credentials.NewSecret`, sent on stdin, then stored atomically;
- PostgreSQL `ReassignOwned` explicit only;
- drop requires `Confirm == name`, terminates sessions;
- rename checks `RenameSupported()` first;
- reveal reads the private secret.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/helper -run 'TestHelperCreateDatabase|TestHelperRename|TestHelperCreateUser|TestHelperDrop|TestHelperReveal' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/helper/service_database.go internal/helper/service_database_test.go
git commit -m "feat(helper): implement database and user operations"
```

---

### Task 3: Helper backup, restore, import, and export

**Files:**
- Create: `internal/helper/service_database_backup.go`
- Test: `internal/helper/service_database_test.go`

**Interfaces:**
- Implements `ListBackups`, `CreateBackup`, `DeleteBackup`, `RestoreBackup`, `ImportDatabase`, `ExportDatabase`, `DownloadBackup`.

- [ ] **Step 1: Write the failing backup tests**

```go
func TestHelperCreateBackupWritesGzip(t *testing.T) {
    c, root := databaseHelperFixture(t)
    runner := c.runner.(*fakeCommandRunner)
    runner.streamOutput = []byte("CREATE TABLE t (id int);\n")
    job, err := c.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
    if err != nil || job.Status != model.JobSucceeded { t.Fatalf("job=%+v err=%v", job, err) }
    matches, _ := filepath.Glob(filepath.Join(root, "backups", "databases", "mysql", "app", "*.sql.gz"))
    if len(matches) != 1 { t.Fatalf("backups=%v", matches) }
}

func TestHelperImportStagingIsDeleted(t *testing.T) {
    c, root := databaseHelperFixture(t)
    _, err := c.ImportDatabase(context.Background(), "mysql", "app", strings.NewReader("dump"), "app.sql", databases.ImportRequest{Mode: databases.ImportMerge})
    if err != nil { t.Fatal(err) }
    matches, _ := filepath.Glob(filepath.Join(root, "backups", "databases", "mysql", ".staging", "*"))
    if len(matches) != 0 { t.Fatalf("staging left behind: %v", matches) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/helper -run 'TestHelperCreateBackup|TestHelperImportStaging' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement the streaming path**

Mirror `...-01-agent-adapters.md` Task 4 with the helper runner:

- backup path under `<helper-root>/backups/databases/...`, 0700/0600, gzip, SHA-256, size;
- root secret env from `/run/secrets/...` or the helper secret tree; never argv;
- restore/import require active; overwrite/replace require `Confirm == target`;
- import staging under `.staging`, 1 GiB limit, delete in `defer`;
- export/download stream without buffering;
- backup list/download/delete work while stopped.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/helper -run 'TestHelperCreateBackup|TestHelperImportStaging|TestHelperRestore|TestHelperExport|TestHelperDownload' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/helper/service_database_backup.go internal/helper/service_database_test.go
git commit -m "feat(helper): stream database backups and imports"
```

---

### Task 4: Helper Redis ACL

**Files:**
- Create: `internal/helper/service_database_redis.go`
- Test: `internal/helper/service_database_test.go`

**Interfaces:**
- Implements Redis user/password operations and ACL-file enablement.

- [ ] **Step 1: Write the failing Redis tests**

```go
func TestHelperRedisFullExcludesAdmin(t *testing.T) {
    c, _ := databaseHelperFixture(t)
    runner := c.runner.(*fakeCommandRunner)
    _, err := c.SetRedisUser(context.Background(), databases.CreateUserRequest{Username: "cache", AccessLevel: "full"})
    if err != nil { t.Fatal(err) }
    joined := runner.joinedCalls()
    if strings.Contains(joined, "+@admin") || strings.Contains(joined, "+@dangerous") {
        t.Fatalf("admin grant: %s", joined)
    }
    if !strings.Contains(joined, "ACL SETUSER") || !strings.Contains(joined, "ACL SAVE") {
        t.Fatalf("calls=%s", joined)
    }
}

func TestHelperRedisACLFileIsIdempotent(t *testing.T) {
    c, _ := databaseHelperFixture(t)
    runner := c.runner.(*fakeCommandRunner)
    if err := c.EnableRedisACLFile(context.Background()); err != nil { t.Fatal(err) }
    before := len(runner.calls)
    if err := c.EnableRedisACLFile(context.Background()); err != nil { t.Fatal(err) }
    if len(runner.calls) != before { t.Fatalf("second call repeated config work: %v", runner.calls[before:]) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/helper -run 'TestHelperRedis' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement Redis ACL**

Mirror `...-01-agent-adapters.md` Task 5:

- `ACL LIST` parsing; `ACL SETUSER` via RESP on stdin to avoid the password in argv; `ACL SAVE`;
- `full` = `+@all -@admin -@dangerous` plus explicit exclusions; `read-only` = `+@read` plus exclusions;
- protect `default` from deletion; allow password reset;
- `EnableRedisACLFile` backs up `redis.conf`, adds `aclfile /data/users.acl` once, seeds `users.acl` from `ACL LIST`, restarts Redis; idempotent.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/helper -run 'TestHelperRedis' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/helper/service_database_redis.go internal/helper/service_database_test.go
git commit -m "feat(helper): add Redis ACL management"
```

---

### Task 5: Helper RPC and client

**Files:**
- Create: `internal/helperproto/databases.go`
- Create: `internal/helperclient/databases.go`
- Create: `internal/helperclient/databases_test.go`
- Modify: `internal/helper/services.go`
- Modify: `internal/helper/server.go`
- Test: `internal/helper/service_database_rpc_test.go`

**Interfaces:**
- Produces helper routes `/v1/services/{id}/...` and `helperclient.DatabaseAPI`.

- [ ] **Step 1: Write the failing RPC/client tests**

```go
func TestServiceDatabaseListEndpoint(t *testing.T) {
    fx := newHelperFixture(t)
    rec := fx.request(t, http.MethodGet, "/v1/services/mysql/databases", nil)
    if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
}

func TestHelperClientDatabaseCRUD(t *testing.T) {
    srv := newHelperTestServer(t, func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, dbproto.DatabaseListResponse{Databases: []dbproto.DatabaseInfo{{Name: "app"}}})
    })
    c := newHelperTestClient(t, srv)
    got, err := c.ListDatabases(context.Background(), "mysql")
    if err != nil || len(got) != 1 || got[0].Name != "app" { t.Fatalf("got=%v err=%v", got, err) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/helper -run TestServiceDatabaseListEndpoint -count=1; go test ./internal/helperclient -run TestHelperClientDatabaseCRUD -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement the RPC and client**

`helperproto/databases.go`: path constants and type aliases to `dbproto` (`type DatabaseListResponse = dbproto.DatabaseListResponse`, etc.). Use the same paths as the agent so the daemon adapter can share mapping code. Add `POST /v1/services/{id}/database-env` with `dbproto.SiteEnvRequest`/`SiteEnvResponse`; the helper reads the requested links' secrets and returns `DB_*`/`DATABASE_URL`, omitting password values for metadata-only links.

`helper/services.go`: add handler methods for each route. Decode JSON with the existing `decodeJSON` (8 MiB); for import, use a raw body with a 1 GiB limit and query params. Validate path/body IDs. Map errors to helper error codes.

`helper/server.go`: wire the routes in `handler()` behind the existing `authenticate` middleware. The helper’s service hooks already expose the controller; add the database methods to the interface and wire them.

`helperclient/databases.go`: implement `DatabaseAPI` on `*Client` using the existing Unix-socket request helper; add streaming helpers for import/export/download and `ResolveSiteEnv` (`POST /v1/services/{id}/database-env`). Set `X-Devstack-Protocol: 1` on every request. Use `url.PathEscape` for dynamic segments.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/helper -run TestServiceDatabase -count=1; go test ./internal/helperclient -run TestHelperClientDatabase -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/helperproto/databases.go internal/helperclient/databases.go internal/helperclient/databases_test.go internal/helper/services.go internal/helper/server.go internal/helper/service_database_rpc_test.go
git commit -m "feat(helper): expose database RPC and client"
```

---

### Task 6: Lane audit and fix cycle

- [ ] **Step 1: Run the lane gate**

```powershell
go test ./internal/helper ./internal/helperclient ./internal/helperproto ./internal/dbengine -count=1
go test ./... -count=1
go vet ./...
go build ./...
git status --short
```

Expected: all pass; clean tree after the final commit. The helper package is Linux-only; run the helper tests in WSL/Linux or use `go test -run '^$'` on Windows for compilation.

- [ ] **Step 2: Fresh audit**

Dispatch a fresh reviewer with the `superpowers:requesting-code-review` skill. It reads the spec, this plan, the foundation contract, and `git diff $base..HEAD`; runs the lane gate; returns spec-compliance and code-quality verdicts with Critical/Important/Minor findings.

- [ ] **Step 3: Fix and re-audit**

Fix every Critical/Important finding with a failing test first. Commit. Re-dispatch a fresh reviewer. Repeat until no Critical/Important findings.

- [ ] **Step 4: Write the lane report**

Write `.superpowers/lane-report.md` with branch, base SHA, commits, test commands/results, audit verdicts, residual risks, and any Ruling. Do not merge.

- [ ] **Step 5: Commit the final lane state**

```powershell
git add internal/helper internal/helperclient internal/helperproto
git commit -m "chore(helper): finalize database adapter lane"
```

Stop and report the branch, HEAD SHA, lane report path, and audit verdicts to the controller.
