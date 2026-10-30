# Daemon Database API and Site Integration Lane Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task in an isolated worktree. Steps use checkbox (`- [ ]`) syntax for tracking. Do not merge; the controller merges after audit.

**Goal:** Implement SQLite persistence, the daemon database service, both platform adapters, backup/job orchestration, site links and dynamic env, Add Site provisioning, the HTTP API, and daemon wiring.

**Architecture:** `internal/databases.Manager` owns metadata, links, jobs, and audit, and delegates engine operations to a `databases.Platform`. `AgentDocker` implements `Platform` on Windows via `agentclient.DatabaseAPI`; `HelperDocker` implements it on Linux via `helperclient.DatabaseAPI`. Site links are resolved dynamically into site process/terminal env and never written to `site.Env`. The API exposes typed service and site routes; the daemon injects the right platform per OS.

**Tech Stack:** Go, SQLite, existing store/API/sites/servicecontrol/daemon packages, `agentclient`, `helperclient`, `dbproto`.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Foundation:** `docs/superpowers/plans/2026-10-30-database-management-00-foundation.md`

**Base:** the frozen foundation SHA recorded in `.superpowers/db-foundation-sha.txt`.

## Global Constraints

- Databases are service-wide; site deletion removes only links.
- No SQL console or arbitrary SQL; all operations go through typed platform methods.
- Passwords never enter SQLite, `site.Env`, logs, list responses, or `php-fpm.conf`. On Linux the daemon may hold a secret transiently in memory to set the site process environment; never persist or log it.
- MySQL/MariaDB rename returns the engine-limitation error; PostgreSQL rename is supported.
- Redis has users/password only; no logical databases, backups, import, or export.
- Destructive operations require typed confirmation; linked sites must be stopped; sessions are terminated by the platform.
- Backup list/download/delete work when the service is installed but stopped; backup/restore/import/export require active.
- Every mutation and reveal writes a durable `audit_events` row.
- Every new route must be documented in `api/openapi.yaml` and must pass the install-token boundary test.
- Existing store/API/sites/servicecontrol/daemon tests must stay green.

## Review Focus

- A site deleted while linked leaves every database, user, grant, password, backup, and row untouched; only the link disappears.
- A metadata-only external user link omits `DB_PASSWORD` and `DATABASE_URL` and never invents a password.
- Selecting an existing user without access shows a warning and never auto-grants during site creation.
- A MySQL-linked site cannot start while MariaDB is active on 3306, and vice versa.
- A failed overwrite restore or replace-import leaves a clearly flagged target and a usable pre-operation backup.

## Files Owned

- Create: `internal/store/databases.go`
- Create: `internal/store/databases_test.go`
- Create: `internal/databases/manager.go`
- Create: `internal/databases/links.go`
- Create: `internal/databases/site_create.go`
- Create: `internal/databases/jobs.go`
- Create: `internal/databases/audit.go`
- Create: `internal/databases/manager_test.go`
- Create: `internal/servicecontrol/helper_docker.go`
- Modify: `internal/servicecontrol/agent_docker.go` (database platform methods)
- Create: `internal/servicecontrol/database_platform_test.go`
- Create: `internal/api/handlers_databases.go`
- Create: `internal/api/handlers_databases_test.go`
- Modify: `internal/api/server.go` (interface, Config/Server fields, routes, error codes/classify)
- Modify: `internal/api/handlers_sites.go` (`createSiteRequest.Database`)
- Modify: `internal/api/security_boundary_test.go` (placeholder replacer)
- Modify: `internal/sites/service.go` (link guard, env resolver, Add Site orchestration)
- Modify: `internal/sites/php_config.go` (runtime-only env, no secret in config)
- Modify: `internal/tooling/siteenv.go` (`ExtraEnv`)
- Modify: `internal/terminal/terminal.go` (resolver)
- Modify: `internal/phpfpm/render.go` (runtime env, no secret render)
- Modify: `internal/daemon/daemon.go` (Linux wiring)
- Modify: `internal/daemon/windows.go` (Windows wiring)
- Modify: `internal/backend/backend.go` (`SiteSpec.DatabaseLinks`)
- Modify: `internal/backend/wsl/backend.go` (link metadata to agent)
- Test: colocated `*_test.go`

Do not edit `internal/dbengine/contracts.go`, `internal/dbproto`, `internal/model/database.go`, `internal/databases/types.go`, `internal/databases/contracts.go`, `api/openapi.yaml`, `internal/agentdocker`, `internal/helper`, or any UI/CLI file. Request a foundation amendment if a contract is missing.

## Interfaces

Consumes from foundation:

```go
model.Database, model.DatabaseUser, model.DatabaseLink, model.DatabaseBackup,
model.DatabaseJob, model.AuditEvent
databases.Platform, databases.Service, databases.SiteDatabaseRequest,
databases.CreateDatabaseRequest, ...
```

Consumes from lanes A/H at integration:

```go
agentclient.DatabaseAPI   // Windows
helperclient.DatabaseAPI  // Linux
```

Produces for the controller: the complete daemon/API/site integration.

---

### Task 1: Store methods

**Files:**
- Create: `internal/store/databases.go`
- Create: `internal/store/databases_test.go`

**Interfaces:**
- Produces store methods for links, users, grants, backups, jobs, audit.

- [ ] **Step 1: Write the failing store tests**

```go
func TestDatabaseLinkRoundTripAndCascade(t *testing.T) {
    s := openTemp(t)
    site := testSite("site_1", "blog")
    if err := s.CreateSite(context.Background(), site); err != nil { t.Fatal(err) }
    user := model.DatabaseUserRecord{ID: "usr_app", ServiceID: "mysql", Engine: model.DatabaseMySQL, Username: "app", Host: "%"}
    if err := s.UpsertDatabaseUser(context.Background(), user); err != nil { t.Fatal(err) }
    link := model.DatabaseLinkRecord{ID: "lnk_1", SiteID: site.ID, ServiceID: "mysql", DatabaseName: "app", UserID: user.ID, Primary: true}
    if err := s.CreateDatabaseLink(context.Background(), link); err != nil { t.Fatal(err) }
    got, err := s.ListDatabaseLinksBySite(context.Background(), site.ID)
    if err != nil || len(got) != 1 || got[0].DatabaseName != "app" { t.Fatalf("links=%v err=%v", got, err) }
    if err := s.DeleteSite(context.Background(), site.ID); err != nil { t.Fatal(err) }
    got, _ = s.ListDatabaseLinksBySite(context.Background(), site.ID)
    if len(got) != 0 { t.Fatalf("links survived site delete: %v", got) }
}

func TestAuditEventRetention(t *testing.T) {
    s := openTemp(t)
    for i := 0; i < 10005; i++ {
        if err := s.AddAuditEvent(context.Background(), model.AuditEventRecord{Action: "test", Result: "ok"}); err != nil { t.Fatal(err) }
    }
    n, _ := s.CountAuditEvents(context.Background())
    if n != 10000 { t.Fatalf("audit count=%d", n) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/store -run 'TestDatabaseLink|TestAuditEventRetention' -count=1`
Expected: FAIL, methods missing.

- [ ] **Step 3: Implement the store methods**

Follow `internal/store/services.go` patterns: column constants, scanners, `requireAffected`, `BeginTx` for multi-row operations. Methods:

- Links: `CreateDatabaseLink`, `GetDatabaseLink`, `ListDatabaseLinksBySite`, `ListDatabaseLinksByService`, `DeleteDatabaseLink`, `DeleteDatabaseLinksByService`, `SetPrimaryDatabaseLink`.
- Users: `UpsertDatabaseUser`, `GetDatabaseUser`, `ListDatabaseUsers`, `DeleteDatabaseUser`, `SetDatabaseUserPasswordState`.
- Grants: `SaveDatabaseGrant`, `ListDatabaseGrants`, `DeleteDatabaseGrant`.
- Backups: `CreateDatabaseBackup`, `GetDatabaseBackup`, `ListDatabaseBackups`, `UpdateDatabaseBackup`, `DeleteDatabaseBackup`.
- Jobs: `CreateDatabaseJob`, `GetDatabaseJob`, `UpdateDatabaseJob`, `ListDatabaseJobs`.
- Audit: `AddAuditEvent`, `ListAuditEvents`, `CountAuditEvents`; prune to newest 10,000 in the same transaction as insert.

Use Unix seconds for timestamps. `database_links` FK cascade handles site deletion; add a test. `DeleteDatabaseUser` returns `ErrConflict`/foreign-key error if links exist; the manager maps it to `databases.ErrLinkedSites`.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/store -run 'TestDatabase|TestAuditEventRetention' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/store/databases.go internal/store/databases_test.go
git commit -m "feat(store): add database management persistence"
```

---

### Task 2: Platform adapters and manager core

**Files:**
- Create: `internal/servicecontrol/helper_docker.go`
- Modify: `internal/servicecontrol/agent_docker.go`
- Create: `internal/servicecontrol/database_platform_test.go`
- Create: `internal/databases/manager.go`
- Create: `internal/databases/audit.go`
- Create: `internal/databases/manager_test.go`

**Interfaces:**
- `AgentDocker` and `HelperDocker` implement `databases.Platform` by mapping `dbproto` to `model` types.
- `databases.NewManager(deps)` returns `*Manager` implementing `databases.Service`.

- [ ] **Step 1: Write the failing adapter/manager tests**

```go
func TestAgentDockerMapsDatabaseList(t *testing.T) {
    api := &fakeAgentDatabaseAPI{databases: []dbproto.DatabaseInfo{{Name: "app", Engine: "mysql"}}}
    a := NewAgentDocker(api, "/data/devstack")
    got, err := a.ListDatabases(context.Background(), "mysql")
    if err != nil || len(got) != 1 || got[0].Name != "app" { t.Fatalf("got=%v err=%v", got, err) }
}

func TestManagerListMergesLinks(t *testing.T) {
    m := newTestManager(t)
    m.platform.databases = []model.Database{{Name: "app", ServiceID: "mysql", Engine: model.DatabaseMySQL}}
    m.store.links = []model.DatabaseLinkRecord{{ID: "lnk_1", SiteID: "site_1", SiteName: "blog", ServiceID: "mysql", DatabaseName: "app"}}
    got, err := m.ListDatabases(context.Background(), "mysql")
    if err != nil || len(got) != 1 || len(got[0].LinkedSites) != 1 || got[0].LinkedSites[0].Name != "blog" {
        t.Fatalf("got=%+v err=%v", got, err)
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/servicecontrol -run TestAgentDockerMapsDatabaseList -count=1; go test ./internal/databases -run TestManagerListMergesLinks -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement adapters and manager**

`AgentDocker`/`HelperDocker`: add the `databases.Platform` methods. Each maps `dbproto` DTOs to `model` and wraps errors into the `databases` sentinels. Validate `postgresql` → `postgres` with the existing `agentAPIID`/helper mapping. Pass through streaming readers. `ResolveSiteEnv` calls the platform's `database-env` endpoint and returns the env map; it never logs or persists the values.

`Manager`:

```go
type Deps struct {
    Store Store
    Platform Platform
    Services ServiceState
    Sites SiteResolver
    Events EventSink
    Audit AuditSink
    Logger *slog.Logger
    Now func() time.Time
    NewID func() string
}
```

`Store` is a narrow interface over the Task 1 methods; `ServiceState` exposes `Get(id)`/`List()` for active/exclusive checks; `SiteResolver` resolves site refs and names; `AuditSink` writes `audit_events`; `EventSink` writes diagnostic events/SSE.

Implement list/create/drop/rename/users/password/grants with metadata merge, protected-object checks, linked-site blocking, and audit. On every user/database list, upsert discovered engine objects into `database_users`/metadata with a stable opaque ID (hash of service + username + host if no stored ID), marking `managed_by_devstack=false` unless already known; this is what lets an external user be linked. Never log a secret. `RevealUserPassword` writes an audit row and returns the value once.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/servicecontrol -run TestAgentDockerMapsDatabase -count=1; go test ./internal/databases -run 'TestManager' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/servicecontrol/agent_docker.go internal/servicecontrol/helper_docker.go internal/servicecontrol/database_platform_test.go internal/databases/manager.go internal/databases/audit.go internal/databases/manager_test.go
git commit -m "feat(daemon): add database platform adapters and manager"
```

---

### Task 3: Backup and job orchestration

**Files:**
- Create: `internal/databases/jobs.go`
- Modify: `internal/databases/manager.go`
- Test: `internal/databases/manager_test.go`

**Interfaces:**
- Produces `ListBackups`, `CreateBackup`, `DeleteBackup`, `RestoreBackup`, `ImportDatabase`, `ExportDatabase`, `DownloadBackup`, `GetJob`, `CancelJob` on `*Manager`.

- [ ] **Step 1: Write the failing job tests**

```go
func TestCreateBackupRunsJobAndAudits(t *testing.T) {
    m := newTestManager(t)
    m.platform.job = model.DatabaseJob{ID: "job_1", Status: model.JobSucceeded, BackupID: "bkp_1"}
    job, err := m.CreateBackup(context.Background(), "mysql", "app", databases.CreateBackupRequest{Kind: model.BackupManual})
    if err != nil || job.Status != model.JobSucceeded { t.Fatalf("job=%+v err=%v", job, err) }
    if len(m.audit.events) != 1 || m.audit.events[0].Action != "database.backup.created" { t.Fatalf("audit=%+v", m.audit.events) }
}

func TestRestoreOverwriteRequiresConfirm(t *testing.T) {
    m := newTestManager(t)
    _, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", databases.RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "wrong"})
    if !errors.Is(err, databases.ErrConflict) { t.Fatalf("err=%v", err) }
}

func TestRestoreFailureKeepsPreRestoreBackup(t *testing.T) {
    m := newTestManager(t)
    m.platform.restoreErr = errors.New("restore failed")
    job, err := m.RestoreBackup(context.Background(), "mysql", "bkp_1", databases.RestoreBackupRequest{Target: "app", Overwrite: true, Confirm: "app", PreRestoreBackup: true})
    if err == nil || job.Status != model.JobFailed { t.Fatalf("job=%+v err=%v", job, err) }
    if job.BackupID == "" { t.Fatal("pre-restore backup id missing") }
    if len(m.store.backups) == 0 { t.Fatal("pre-restore backup not stored") }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/databases -run 'TestCreateBackup|TestRestoreOverwrite' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement jobs**

`Manager` creates a `database_jobs` row (`queued`), starts a goroutine with a cancellable context, updates to `running`, calls the platform, stores the returned backup metadata, updates `succeeded`/`failed`/`canceled`, and emits `database.job.updated` plus an audit row. Keep a `map[jobID]context.CancelFunc` guarded by a mutex for `CancelJob`.

- Pre-restore/pre-import backup: if requested, call `CreateBackup` first and use its backup ID; fail the operation if the backup fails.
- Overwrite restore: require `Confirm == Target`; the platform drops/recreates.
- Replace import: require `Confirm == Database`; offer pre-import backup.
- On failure/cancel, mark the job and emit an event; do not auto-rollback. The UI can restore the pre-operation backup.
- `ExportDatabase`/`DownloadBackup` return the platform reader directly; no job row for export/download.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/databases -run 'TestCreateBackup|TestRestore|TestImport|TestJob' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/databases/jobs.go internal/databases/manager.go internal/databases/manager_test.go
git commit -m "feat(daemon): orchestrate database backup and restore jobs"
```

---

### Task 4: Site links, dynamic env, and lifecycle guards

**Files:**
- Create: `internal/databases/links.go`
- Modify: `internal/sites/service.go`
- Modify: `internal/sites/php_config.go`
- Modify: `internal/tooling/siteenv.go`
- Modify: `internal/terminal/terminal.go`
- Modify: `internal/phpfpm/render.go`
- Modify: `internal/backend/wsl/backend.go`
- Test: `internal/databases/manager_test.go`, `internal/sites/service_test.go`, `internal/tooling/siteenv_test.go`, `internal/terminal/terminal_test.go`

**Interfaces:**
- Produces `ListSiteLinks`, `LinkDatabase`, `UnlinkDatabase` on `*Manager`.
- Adds `sites.Deps.DatabaseEnv func(ctx, model.Site) (map[string]string, error)` and `sites.Deps.DatabaseGuard func(ctx, model.Site) error`.
- Adds `tooling.EnvDeps.ExtraEnv map[string]string`.
- Adds `agentproto.SiteSpec.DatabaseLinks`/`backend.SiteSpec.DatabaseLinks` (foundation amendment if not present).

- [ ] **Step 1: Write the failing link/env tests**

```go
func TestLinkDatabaseRejectsMissingAccessWithoutGrant(t *testing.T) {
    m := newTestManager(t)
    m.platform.users = []model.DatabaseUser{{ID: "usr_app", Username: "app"}}
    _, err := m.LinkDatabase(context.Background(), "blog", databases.LinkDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"})
    if err != nil { t.Fatal(err) }
    if len(m.platform.grantCalls) != 0 { t.Fatalf("auto-granted: %v", m.platform.grantCalls) }
}

func TestSiteEnvUsesLinkValuesAndOmitsMetadataPassword(t *testing.T) {
    env, err := databases.SiteEnv([]model.DatabaseLink{{Primary: true, Database: "app", Username: "app", PasswordManaged: false, Connection: model.DatabaseConnection{Host: "127.0.0.1", Port: 3306, Database: "app", Username: "app"}}})
    if err != nil { t.Fatal(err) }
    if env["DB_HOST"] != "127.0.0.1" || env["DB_DATABASE"] != "app" { t.Fatalf("env=%v", env) }
    if _, ok := env["DB_PASSWORD"]; ok { t.Fatalf("metadata-only link has DB_PASSWORD: %v", env) }
    if _, ok := env["DATABASE_URL"]; ok { t.Fatalf("metadata-only link has DATABASE_URL: %v", env) }
}

func TestSiteStartBlocksWrongExclusiveEngine(t *testing.T) {
    svc := newTestServiceWithDatabaseGuard(t, errors.New("mysql linked, mariadb active"))
    if err := svc.Start(context.Background(), "blog"); err == nil { t.Fatal("start succeeded with wrong engine") }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/databases -run 'TestLinkDatabase|TestSiteEnv' -count=1; go test ./internal/sites -run TestSiteStartBlocks -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement links and env**

`links.go`: link CRUD with site ref resolution; enforce one primary per site; create a link only after the user exists; never auto-grant; if the user has no grant on the database, set `Warning`/`Error` on the link (the UI shows the warning). `UnlinkDatabase` deletes only the link.

`databases.SiteEnv(links []model.DatabaseLink) (map[string]string, error)`:
- primary link: `DB_CONNECTION`, `DB_HOST=127.0.0.1`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD` only when `PasswordManaged`, `DATABASE_URL` only when password managed.
- additional links: `DB_<SLUG>_*`.
- link values win over `site.Env`; return an error on duplicate primary links.

`sites.Deps.DatabaseEnv`/`DatabaseGuard`: nil-safe. In `backendSpecFor`, merge the resolver env after `site.Env` (do not mutate the persisted site). In `Create`/`Start`, call the guard; block only on a conflicting exclusive-group engine, warn on a stopped exact service. In `Delete`, rely on the FK cascade; emit `database.link.changed`.

`tooling.EnvDeps.ExtraEnv`: merge after `site.Env`, before `DEVSTACK_*`; link values win. Pass from `terminal.Manager.environment`.

PHP FPM: add `RuntimeEnv map[string]string` to `phpfpm.Config` and pass it to the FPM master process environment; do not write DB secrets into `env[...]` in `php-fpm.conf`. `sites/php_config.go` passes the resolved DB env as `RuntimeEnv`, not `Env`.

Windows agent: add `DatabaseLinks []DatabaseLinkSpec` to `backend.SiteSpec` and `agentproto.SiteSpec`; `backend/wsl.toAgentSite` maps it. The agent lane resolves secrets into `SiteSpec.Env` at prepare/terminal time. The daemon never sends passwords.

Linux: `HelperDocker.ResolveSiteEnv` calls `helperclient.ResolveSiteEnv`; the daemon receives the map transiently and passes it to `backendSpecFor`/`terminal`/PHP runtime. Never persist or log it.

CLI/exec parity: add `Manager.SiteDatabaseEnv(ctx, siteRef)` and a `GET /api/v1/sites/{ref}/database-env` route that returns the resolved env with `Cache-Control: no-store` and writes an audit row. The CLI `siteShellContext` merges it into `tooling.BuildSiteEnv`/`WindowsSiteEnvironment` for `devstack exec`, `shell`, and `shell-init`. On Windows this calls the agent `database-env` endpoint; on Linux it calls the helper. Never include the values in logs or list responses.

Service uninstall: when `servicecontrol.Manager.Uninstall(..., deleteData=true)` succeeds for a database service, delete its links and emit `database.link.changed`; warn before deletion that links will be removed. Keep-data preserves links and data. Deactivation/switch only warns and leaves links intact; the site start guard handles the wrong-engine case.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/databases ./internal/sites ./internal/tooling ./internal/terminal ./internal/phpfpm -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/databases/links.go internal/sites internal/tooling/siteenv.go internal/terminal/terminal.go internal/phpfpm/render.go internal/backend/wsl/backend.go
git commit -m "feat(sites): link databases and inject dynamic env"
```

---

### Task 5: Add Site orchestration

**Files:**
- Create: `internal/databases/site_create.go`
- Modify: `internal/sites/service.go`
- Modify: `internal/api/handlers_sites.go`
- Test: `internal/databases/manager_test.go`, `internal/sites/service_test.go`, `internal/api/handlers_sites_test.go`

**Interfaces:**
- `Manager.CreateSiteDatabase(ctx, siteRef, req)` provisions a database/user and creates the link.
- `sites.Service.Create` calls the provisioner before/after site creation per the order below.

- [ ] **Step 1: Write the failing Add Site tests**

```go
func TestAddSiteCreatesDatabaseUserAndLink(t *testing.T) {
    m := newTestManager(t)
    result, err := m.CreateSiteDatabase(context.Background(), "blog", databases.SiteDatabaseRequest{
        ServiceID: "mysql",
        NewDatabase: &databases.CreateDatabaseRequest{Name: "blog"},
        NewUser: &databases.CreateUserRequest{Username: "blog_user"},
        Grant: model.GrantReadWriteSchema,
        Primary: true,
    })
    if err != nil { t.Fatal(err) }
    if result.Database.Name != "blog" || result.User.Username != "blog_user" { t.Fatalf("result=%+v", result) }
    if len(m.platform.grantCalls) != 1 { t.Fatalf("grant calls=%v", m.platform.grantCalls) }
}

func TestAddSiteExistingUserWithoutAccessWarns(t *testing.T) {
    m := newTestManager(t)
    m.platform.users = []model.DatabaseUser{{ID: "usr_app", Username: "app"}}
    m.platform.grants = nil
    result, err := m.CreateSiteDatabase(context.Background(), "blog", databases.SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"})
    if err != nil { t.Fatal(err) }
    if result.Warning == "" { t.Fatal("missing access warning") }
    if len(m.platform.grantCalls) != 0 { t.Fatalf("auto-granted: %v", m.platform.grantCalls) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/databases -run 'TestAddSite' -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement the orchestration**

Split the manager surface into two calls so provisioning can happen before the site row exists:

```go
ProvisionSiteDatabase(ctx, req SiteDatabaseRequest) (ProvisionedDatabase, error)
LinkProvisioned(ctx, siteID string, provisioned ProvisionedDatabase) (SiteDatabaseResult, error)
CreateSiteDatabase(ctx, siteRef string, req SiteDatabaseRequest) (SiteDatabaseResult, error) // provision + link for an existing site
```

`ProvisionedDatabase` carries the created/reused database, user, warning, and the original request (including an optional external password). `LinkProvisioned` creates the link and never re-grants.

Order in `sites.Service.Create`:

1. Validate the site and database selection.
2. `ProvisionSiteDatabase` first (new database/user or existing selection); if this fails, no site row is created.
3. Create the site row and hosts/cert as today.
4. `LinkProvisioned` with the new site ID.
5. Start the site.

If site creation fails after provisioning, keep the database/user (service-wide), return the site error with `details.warning` naming the created database/user; do not drop them. If linking fails after the site row is created, keep the site and return the warning; the UI can retry the link.

For a new user: default grant `read_write_schema` on the selected database. For an existing user: never grant; compute access and set `Warning` if missing. `Password` is used only for an external user and stored by the platform; blank means metadata-only.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/databases ./internal/sites ./internal/api -run 'TestAddSite|TestCreateSite' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/databases/site_create.go internal/sites/service.go internal/api/handlers_sites.go internal/databases/manager_test.go internal/sites/service_test.go internal/api/handlers_sites_test.go
git commit -m "feat(sites): provision databases during Add Site"
```

---

### Task 6: HTTP API

**Files:**
- Create: `internal/api/handlers_databases.go`
- Create: `internal/api/handlers_databases_test.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/security_boundary_test.go`

**Interfaces:**
- Adds `DatabaseService` interface, `Config.Databases`, `Server.databases`, routes, handlers, and stable error codes.

- [ ] **Step 1: Write the failing API tests**

```go
func TestDatabaseRoutes(t *testing.T) {
    fx := newFixture(t, Config{Databases: fakeDatabaseService{}})
    rec := fx.request(t, http.MethodGet, "/api/v1/services/mysql/databases", nil)
    if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
    rec = fx.request(t, http.MethodPost, "/api/v1/services/mysql/databases", map[string]any{"name": "app"})
    if rec.Code != http.StatusCreated { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
}

func TestDatabaseRevealIsAuditedAndNoStore(t *testing.T) {
    fx := newFixture(t, Config{Databases: fakeDatabaseService{secret: "s3cret"}})
    rec := fx.request(t, http.MethodPost, "/api/v1/services/mysql/users/usr_app/reveal", nil)
    if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "s3cret") { t.Fatalf("body=%s", rec.Body.String()) }
    if rec.Header().Get("Cache-Control") != "no-store" { t.Fatalf("cache=%q", rec.Header().Get("Cache-Control")) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/api -run 'TestDatabase' -count=1`
Expected: FAIL, routes missing.

- [ ] **Step 3: Implement the API**

Add `DatabaseService` near `ServiceConfigService` with the `databases.Service` methods. Add `Databases DatabaseService` to `Config` and `databases` to `Server`; assign in `NewServer`.

Add routes in `apiRoutes()` exactly as documented in OpenAPI, including `GET /api/v1/sites/{ref}/database-env` for CLI/exec env resolution. Handlers follow the existing pattern: nil-check → path/body validation → service call → `writeServiceError` → `writeJSON` (201 create, 202 job, 204 delete). List responses use envelopes and normalize nil. Reveal/password/env handlers call the service and audit; never log the value and always set `Cache-Control: no-store`. Import uses `r.ParseMultipartForm`/`MultipartReader` with the 1 GiB body limit; export/download stream with `Content-Disposition` and `no-store`.

Add stable codes and `classifyError` mappings: `DATABASE_NOT_FOUND`, `DATABASE_CONFLICT`, `DATABASE_PROTECTED`, `DATABASE_UNSUPPORTED`, `DATABASE_IN_USE`, `DATABASE_SERVICE_INACTIVE`, `DATABASE_PASSWORD_MISSING`, `DATABASE_JOB_NOT_FOUND`.

Update `security_boundary_test.go`'s placeholder replacer for `{name}`, `{userID}`, `{backupID}`, `{jobID}`, `{linkID}`. Run the OpenAPI/security tests.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/api -run 'TestDatabase|TestOpenAPI|TestEveryAPIRouteRequiresTheInstallToken' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/api/handlers_databases.go internal/api/handlers_databases_test.go internal/api/server.go internal/api/security_boundary_test.go
git commit -m "feat(api): expose database management routes"
```

---

### Task 7: Daemon wiring

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/windows.go`
- Test: `internal/daemon/*_test.go`

**Interfaces:**
- Constructs `databases.Manager` with `AgentDocker` on Windows and `HelperDocker` on Linux; passes it to `api.Config.Databases` and `sites.Deps.DatabaseEnv`/`DatabaseGuard`.

- [ ] **Step 1: Write the failing wiring tests**

Add compile/wiring tests that construct the daemon config with a fake platform and assert `api.Config.Databases` is non-nil and `sites.Deps.DatabaseEnv` is set. Follow existing daemon test seams.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/daemon -run 'TestDatabaseWiring' -count=1`
Expected: FAIL, fields missing.

- [ ] **Step 3: Wire both platforms**

Linux `daemon.go`: after `serviceManager` and `siteService`, construct `helperDocker := servicecontrol.NewHelperDocker(helperclient...)` (the concrete helper client is already available), `databaseManager := databases.NewManager(...)`, set `siteService` deps, and pass `Databases: databaseManager` to `api.NewServer`.

Windows `windows.go`: after `agentDocker` and `serviceManager`, construct `databases.NewManager` with `agentDocker` (or a dedicated `AgentDatabasePlatform`), set `siteService` deps, and pass `Databases` to the API. Ensure the Windows agent client's `DatabaseAPI` is wired.

The manager's `ServiceState` uses the existing `serviceManager`; `SiteResolver` uses the store/site service; `Audit` uses the store; `Events` uses the store's diagnostic event sink.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/daemon -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/daemon/daemon.go internal/daemon/windows.go internal/daemon
git commit -m "feat(daemon): wire database management on both platforms"
```

---

### Task 8: Lane audit and fix cycle

- [ ] **Step 1: Run the lane gate**

```powershell
go test ./internal/store ./internal/databases ./internal/servicecontrol ./internal/api ./internal/sites ./internal/tooling ./internal/terminal ./internal/phpfpm ./internal/daemon -count=1
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
git add internal/store internal/databases internal/servicecontrol internal/api internal/sites internal/tooling internal/terminal internal/phpfpm internal/daemon internal/backend/wsl
git commit -m "chore(daemon): finalize database API and site integration lane"
```

Stop and report the branch, HEAD SHA, lane report path, and audit verdicts to the controller.
