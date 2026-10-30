# Database Management CLI Lane Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in an isolated worktree. Steps use checkbox (`- [ ]`) syntax for tracking. Do not merge; the controller merges after audit.

**Goal:** Add full CLI parity for service-wide databases, users, grants, backups, import/export, site links, and Redis users/password, plus database options on `site add`.

**Architecture:** Extend the existing API client with typed database methods and raw/multipart streaming helpers, add a top-level `devstack db` command group, extend `site add`, and add `site db list/link/unlink`. All commands use the frozen HTTP/OpenAPI contract and existing output/confirmation conventions.

**Tech Stack:** Go, Cobra, existing `internal/client` helpers, `internal/cli` test harness, `internal/databases` domain types.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Foundation:** `docs/superpowers/plans/2026-10-30-database-management-00-foundation.md`

**Base:** the frozen foundation SHA recorded in `.superpowers/db-foundation-sha.txt`.

## Global Constraints

- Databases are service-wide; `site db unlink` removes only the link.
- No SQL console or arbitrary SQL input.
- MySQL/MariaDB rename is rejected with an engine-limitation error; PostgreSQL rename is supported.
- Redis has ACL users/password only; no logical database management, backup, import, or export.
- Passwords never appear in argv, list output, or logs. Use stdin/prompt for setting external-user passwords; reveal only with an explicit `--reveal` and a warning.
- Destructive commands require `--yes` or an interactive confirmation. A declined confirmation makes no API call and returns `CONFIRMATION_REQUIRED`.
- JSON success output goes through `a.writeJSON`; failures write nothing to stdout. Lists normalize `nil` to `[]`.
- Every new API route must be documented in `api/openapi.yaml` or the API OpenAPI test fails.
- `site add` existing-user selection never auto-grants; missing access is reported, not fixed.
- All existing CLI/client tests must stay green.

## Review Focus

- `db drop`, `db user delete`, overwrite restore, replace-import, and `site db unlink` must make no API call after a declined confirmation.
- JSON list output must never contain a password; only an explicit `--reveal` may print one, with a warning.
- `db rename` must fail before the API call for MySQL/MariaDB.
- `site add --database-user ...` without access must not add a grant to the payload.
- A metadata-only external user link must be representable without a password flag and must not prompt for one.

## Files Owned

- Create: `internal/client/databases.go`
- Create: `internal/client/databases_test.go`
- Modify: `internal/client/client.go` (add raw/multipart/download helpers only)
- Modify: `internal/client/client_test.go` (fake API routes for DB tests)
- Create: `internal/cli/db.go`
- Create: `internal/cli/db_test.go`
- Create: `internal/cli/site_db.go`
- Create: `internal/cli/site_db_test.go`
- Modify: `internal/cli/root.go` (`Client` interface and `newDBCmd` registration)
- Modify: `internal/cli/site.go` (`site add` flags/validation and `site db` registration)
- Modify: `internal/cli/cli_test.go` (`fakeClient` methods/fields)
- Modify: `internal/cli/site_test.go` (site add database payload tests)

Do not edit `internal/api`, `internal/databases`, `internal/store`, `internal/agent*`, or any UI file. Request a foundation amendment if a contract is missing.

## Interfaces

Consumes from foundation:

```go
// internal/databases/types.go
type Database, DatabaseUser, DatabaseGrant, DatabaseBackup, DatabaseJob,
     SiteDatabaseLink, DatabaseConnection, Secret, GrantPreset, Engine
type CreateDatabaseRequest, CreateUserRequest, UpdateUserRequest,
     CreateBackupRequest, RestoreBackupRequest, ImportRequest,
     LinkDatabaseRequest, SiteDatabaseRequest
```

Produces for the controller: `internal/client` database methods, `devstack db` group, `site add` database flags, `site db` commands.

---

### Task 1: Client methods and streaming helpers

**Files:**
- Create: `internal/client/databases.go`
- Create: `internal/client/databases_test.go`
- Modify: `internal/client/client.go` (raw/multipart/download helpers)
- Modify: `internal/client/client_test.go` (fake API routes)

**Interfaces:**
- Produces on `*client.Client`:

```go
ListServiceDatabases(ctx, serviceID string) ([]databases.Database, error)
CreateServiceDatabase(ctx, serviceID string, req databases.CreateDatabaseRequest) (databases.Database, error)
GetServiceDatabase(ctx, serviceID, name string) (databases.Database, error)
RenameServiceDatabase(ctx, serviceID, name string, req databases.RenameDatabaseRequest) (databases.Database, error)
DropServiceDatabase(ctx, serviceID, name string, req databases.DropDatabaseRequest) error
ListServiceUsers(ctx, serviceID string) ([]databases.DatabaseUser, error)
CreateServiceUser(ctx, serviceID string, req databases.CreateUserRequest) (databases.DatabaseUser, error)
GetServiceUser(ctx, serviceID, userID string) (databases.DatabaseUser, error)
UpdateServiceUser(ctx, serviceID, userID string, req databases.UpdateUserRequest) (databases.DatabaseUser, error)
DeleteServiceUser(ctx, serviceID, userID string, req databases.DeleteUserRequest) error
SetServiceUserPassword(ctx, serviceID, userID string) (databases.Secret, error)
RevealServiceUserPassword(ctx, serviceID, userID string) (databases.Secret, error)
ListServiceBackups(ctx, serviceID string) ([]databases.DatabaseBackup, error)
CreateServiceBackup(ctx, serviceID, database string, req databases.CreateBackupRequest) (databases.DatabaseJob, error)
DeleteServiceBackup(ctx, serviceID, backupID string) error
RestoreServiceBackup(ctx, serviceID, backupID string, req databases.RestoreBackupRequest) (databases.DatabaseJob, error)
DownloadServiceBackup(ctx, serviceID, backupID string) (io.ReadCloser, error)
ImportServiceDatabase(ctx, serviceID, database string, file io.Reader, filename string, req databases.ImportRequest) (databases.DatabaseJob, error)
ExportServiceDatabase(ctx, serviceID, database string) (io.ReadCloser, error)
GetDatabaseJob(ctx, jobID string) (databases.DatabaseJob, error)
CancelDatabaseJob(ctx, jobID string) error
ListSiteDatabases(ctx, ref string) ([]databases.SiteDatabaseLink, error)
LinkSiteDatabase(ctx, ref string, req databases.LinkDatabaseRequest) (databases.SiteDatabaseLink, error)
UnlinkSiteDatabase(ctx, ref, linkID string) error
CreateSiteDatabase(ctx, ref string, req databases.SiteDatabaseRequest) (databases.SiteDatabaseResult, error)
SiteDatabaseEnv(ctx, ref string) (map[string]string, error)
```

- Produces helpers on `*client.Client`: `doRaw(ctx, method, path string, body io.Reader, contentType string) (*http.Response, error)` and `doMultipart(ctx, path string, fields map[string]string, filename string, file io.Reader) (*http.Response, error)`.

- [ ] **Step 1: Write the failing client tests**

Add fake API routes for `/api/v1/services/mysql/databases`, `/users`, `/backups`, `/jobs`, and site links. Assert method/path/body with `api.last`.

```go
func TestClientDatabaseCRUDAndReveal(t *testing.T) {
    api := &fakeAPI{}
    c, home := startFakeAPI(t, api)
    _ = home
    dbs, err := c.ListServiceDatabases(context.Background(), "mysql")
    if err != nil || len(dbs) != 1 || dbs[0].Name != "app" { t.Fatalf("dbs=%v err=%v", dbs, err) }
    api.last(t, http.MethodGet, "/api/v1/services/mysql/databases")
    secret, err := c.RevealServiceUserPassword(context.Background(), "mysql", "usr_app")
    if err != nil || secret.Value != "s3cret" { t.Fatalf("secret=%v err=%v", secret, err) }
    api.last(t, http.MethodPost, "/api/v1/services/mysql/users/usr_app/reveal")
}

func TestClientDatabaseStreaming(t *testing.T) {
    api := &fakeAPI{}
    c, _ := startFakeAPI(t, api)
    body, err := c.ExportServiceDatabase(context.Background(), "mysql", "app")
    if err != nil { t.Fatal(err) }
    data, _ := io.ReadAll(body)
    if string(data) != "dump" { t.Fatalf("export=%q", data) }
    api.last(t, http.MethodGet, "/api/v1/services/mysql/databases/app/export")
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/client -run 'TestClientDatabase' -count=1`
Expected: FAIL, methods not defined.

- [ ] **Step 3: Implement the client**

Add typed methods in `internal/client/databases.go` using `doJSON` for normal calls, `doJSONNoTimeout` for backup/restore/import job creation, and `doStream`/`doRaw` for download/export/import. Use `url.PathEscape` for every path value. Decode envelopes:

```go
type databaseListEnvelope struct { Databases []databases.Database `json:"databases"` }
type userListEnvelope struct { Users []databases.DatabaseUser `json:"users"` }
type backupListEnvelope struct { Backups []databases.DatabaseBackup `json:"backups"` }
type linkListEnvelope struct { Links []databases.SiteDatabaseLink `json:"links"` }
type jobEnvelope struct { Job databases.DatabaseJob `json:"job"` }
```

Normalize nil slices to `[]` before returning. Add `doRaw`/`doMultipart` to `client.go`: build the request, set `Content-Type`, send it, decode errors on non-2xx, and return the response. `doMultipart` uses `multipart.NewWriter`, writes the `file` field with the provided filename, and writes scalar fields. `DownloadServiceBackup` and `ExportServiceDatabase` call `doStream` and return the body.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/client -run 'TestClientDatabase' -count=1`
Expected: PASS (Linux/WSL for the socket-based fake API; on Windows run the compile check `go test ./internal/client -run '^$'`).

- [ ] **Step 5: Commit**

```powershell
git add internal/client/databases.go internal/client/databases_test.go internal/client/client.go internal/client/client_test.go
git commit -m "feat(client): add database management methods"
```

---

### Task 2: `devstack db` command group

**Files:**
- Create: `internal/cli/db.go`
- Create: `internal/cli/db_test.go`
- Modify: `internal/cli/root.go` (`Client` interface, `newDBCmd` registration)
- Modify: `internal/cli/cli_test.go` (`fakeClient` fields/methods)

**Interfaces:**
- Produces `func (a *App) newDBCmd() *cobra.Command` with subcommands: `list`, `create`, `rename`, `drop`, `user`, `backup`, `export`, `import`, `link`, `unlink`, `password`.
- Extends `cli.Client` with the Task 1 methods.

- [ ] **Step 1: Write the failing command tests**

```go
func TestDBListJSON(t *testing.T) {
    fake := &fakeClient{databaseList: []databases.Database{{Name: "app", Engine: databases.EngineMySQL, SizeBytes: 12 << 20}}}
    out := runCLI(t, fake, "db", "list", "--service", "mysql", "--json")
    var got struct{ Databases []databases.Database `json:"databases"` }
    if err := json.Unmarshal([]byte(out), &got); err != nil { t.Fatal(err) }
    if len(got.Databases) != 1 || got.Databases[0].Name != "app" { t.Fatalf("databases=%+v", got.Databases) }
    if strings.Contains(strings.ToLower(out), "password") { t.Fatalf("list leaked a password field: %s", out) }
}

func TestDBRenameRejectsMySQLBeforeAPICall(t *testing.T) {
    fake := &fakeClient{}
    code, _, stderr := runCLIExit(t, fake, "db", "rename", "--service", "mysql", "--name", "app", "--to", "app2")
    if code == 0 || !strings.Contains(stderr, "engine") { t.Fatalf("code=%d stderr=%s", code, stderr) }
    if fake.databaseRenamed { t.Fatal("rename API called for mysql") }
}

func TestDBDropRequiresYes(t *testing.T) {
    fake := &fakeClient{}
    code, _, stderr := runCLIExit(t, fake, "db", "drop", "--service", "mysql", "--name", "app")
    if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") { t.Fatalf("code=%d stderr=%s", code, stderr) }
    if fake.databaseDropped { t.Fatal("drop API called without --yes") }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/cli -run 'TestDB' -count=1`
Expected: FAIL, unknown command `db`.

- [ ] **Step 3: Implement the group**

Register `app.newDBCmd()` in `NewRootCmd`. Build commands with `RunE` and `a.clientReady(ctx)` after flag validation. Flags:

- `db list --service <id>`
- `db create --service <id> --name <name> [--charset] [--collation]`
- `db rename --service <id> --name <name> --to <new>`
- `db drop --service <id> --name <name> [--backup-first] --yes`
- `db user list --service <id>`
- `db user create --service <id> --name <name> [--host] [--grant <db>:<preset>] [--access-level read-only|full]`
- `db user delete --service <id> --user-id <id> --yes`
- `db user password --service <id> --user-id <id> [--reveal] [--set] [--yes]` (`--set` reads the new password from stdin; default rotates with confirmation)
- `db user grant --service <id> --user-id <id> --database <db> --preset <preset>`
- `db backup list --service <id>`
- `db backup create --service <id> --database <db>`
- `db backup download --service <id> --backup-id <id> --output <file>`
- `db backup restore --service <id> --backup-id <id> [--target <db>] [--overwrite] --yes`
- `db export --service <id> --database <db> --output <file>`
- `db import --service <id> --database <db> --file <file> [--replace] --yes`
- `db link --site <ref> --service <id> --database <db> --user-id <id>`
- `db unlink --site <ref> --link-id <id> --yes`
- `db password --service redis [--user default] [--reveal]` (sets/rotates with confirmation; no password in argv)

`db rename` checks `service == "mysql" || service == "mariadb"` and returns an engine-limitation error before `clientReady`. `db drop`, `db user delete`, overwrite restore, replace import, and unlink use the hard `--yes` gate; print a warning and return `CONFIRMATION_REQUIRED` with `--yes` remediation. Normal success uses `a.writeJSON` in JSON mode and `writeTable` otherwise. Lists never print passwords. `db user password --reveal` prints the existing warning line before the secret. `db user password --set` reads a line from stdin (no echo in tests) and never accepts a password flag.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/cli -run 'TestDB' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/cli/db.go internal/cli/db_test.go internal/cli/root.go internal/cli/cli_test.go
git commit -m "feat(cli): add database command group"
```

---

### Task 3: `site add` database flags and `site db` commands

**Files:**
- Modify: `internal/cli/site.go`
- Create: `internal/cli/site_db.go`
- Create: `internal/cli/site_db_test.go`
- Modify: `internal/cli/site_test.go`

**Interfaces:**
- Extends `siteAddOptions` and `sites.CreateRequest` via the foundation contract.
- Produces `func (a *App) newSiteDBCmd() *cobra.Command` with `list`, `link`, `unlink`.

- [ ] **Step 1: Write the failing tests**

```go
func TestSiteAddDatabasePayload(t *testing.T) {
    fake := &fakeClient{}
    runCLI(t, fake, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "php",
        "--database-service", "mysql", "--database-new", "blog",
        "--database-user-new", "blog_user", "--database-grant", "read_write_schema")
    if fake.addReq.Database == nil { t.Fatal("database payload missing") }
    if fake.addReq.Database.NewDatabase.Name != "blog" { t.Fatalf("db=%+v", fake.addReq.Database) }
    if fake.addReq.Database.NewUser.Username != "blog_user" { t.Fatalf("user=%+v", fake.addReq.Database) }
}

func TestSiteAddExistingUserDoesNotGrant(t *testing.T) {
    fake := &fakeClient{}
    runCLI(t, fake, "site", "add", "--name", "blog", "--path", `C:\projects\blog`, "--kind", "php",
        "--database-service", "mysql", "--database", "app", "--database-user", "usr_app")
    if fake.addReq.Database.Grant != "" { t.Fatalf("auto-granted %q", fake.addReq.Database.Grant) }
    if fake.addReq.Database.NewUser != nil { t.Fatal("created a user for an existing user selection") }
}

func TestSiteDBUnlinkRequiresYes(t *testing.T) {
    fake := &fakeClient{}
    code, _, stderr := runCLIExit(t, fake, "site", "db", "unlink", "blog", "link_1")
    if code == 0 || !strings.Contains(stderr, "CONFIRMATION_REQUIRED") { t.Fatalf("code=%d stderr=%s", code, stderr) }
    if fake.siteUnlinked { t.Fatal("unlink API called without --yes") }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/cli -run 'TestSite.*Database|TestSiteDB' -count=1`
Expected: FAIL, flags/commands missing.

- [ ] **Step 3: Implement the flags and commands**

Add to `siteAddOptions`: `DatabaseService`, `Database`, `DatabaseNew`, `DatabaseUserID`, `DatabaseUserNew`, `DatabaseUserPasswordStdin`, `DatabaseGrant`. Add flags:

```go
--database-service
--database
--database-new
--database-user
--database-user-new
--database-user-password-stdin
--database-grant
```

Validation before `clientReady`:

- database flags require `--database-service`;
- `--database` and `--database-new` are mutually exclusive;
- `--database-user` and `--database-user-new` are mutually exclusive;
- `--database-user-password-stdin` is only valid with `--database-user`; read one line from stdin and set `Database.Password`; never accept a password flag;
- selecting `--database-user` does not set `Grant`; the server reports missing access;
- `--database-grant` defaults to `read_write_schema` only for `--database-user-new`.

Map the options into `sites.CreateRequest.Database` (foundation type). In `client.AddSite` and `createSitePayload`, include the `database` object only when non-nil. Do not change the response type: `AddSite` still returns `model.Site`; after creation, print the site URL and a note to run `devstack site db list <site>` for connection details. If the API returns a database-provisioning warning in error details, print it to stderr and preserve the exit code.

`site db` commands:

- `site db list <ref>` → `ListSiteDatabases`, table with engine, host:port, database, username, primary, password state; never a password.
- `site db link <ref> --service <id> --database <db> --user-id <id> [--primary]` → `LinkSiteDatabase`.
- `site db unlink <ref> <link-id> --yes` → `UnlinkSiteDatabase`; hard `--yes` gate.

Register `newSiteDBCmd()` in `newSiteCmd`’s `AddCommand` list.

CLI env parity: in `siteShellContext` (`internal/cli/tooling.go:285`), call `SiteDatabaseEnv(ctx, ref)` and merge the result into `tooling.BuildSiteEnv`/`WindowsSiteEnvironment` with link values winning. This makes `devstack exec`, `shell`, and `shell-init` see `DB_*`/`DATABASE_URL`. The API response is audited and `no-store`; never print or log the values except through the command's own environment.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/cli -run 'TestSite.*Database|TestSiteDB' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/cli/site.go internal/cli/site_db.go internal/cli/site_db_test.go internal/cli/site_test.go
git commit -m "feat(cli): add site database flags and commands"
```

---

### Task 4: Lane audit and fix cycle

- [ ] **Step 1: Run the lane gate**

```powershell
go test ./internal/cli ./internal/client -count=1
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
git add internal/client internal/cli
git commit -m "chore(cli): finalize database management lane"
```

Stop and report the branch, HEAD SHA, lane report path, and audit verdicts to the controller.
