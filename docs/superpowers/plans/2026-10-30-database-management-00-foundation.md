# Database Management Foundation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Execute this plan in the main checkout before creating any lane worktree. Steps use checkbox (`- [ ]`) syntax for tracking. This is the only serial phase.

**Goal:** Freeze the migration, persistence model, domain contracts, shared wire protocol, engine contract, Add Site request field, OpenAPI contract, and shared UI primitives that all six parallel lanes build against.

**Architecture:** The foundation defines contracts and pure shared types only. Concrete engine SQL lives in the agent lane; helper execution lives in the helper lane; the daemon service and API live in the daemon lane. The UI and CLI lanes consume the frozen contracts. No lane may edit foundation-owned files without a controller amendment.

**Tech Stack:** Go 1.26, SQLite migrations, Svelte 5 + TypeScript, existing OpenAPI/UI test harnesses.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Master plan:** `docs/superpowers/plans/2026-10-30-database-management-master.md`

## Global Constraints

- Databases are service-wide; site deletion removes only links.
- Passwords are never stored in SQLite or `site.Env`.
- MySQL/MariaDB rename is unsupported; PostgreSQL rename is supported.
- Redis has ACL users/password only; no logical databases or backups.
- No arbitrary SQL endpoint; every operation is typed and whitelisted.
- Every new API route must be documented in `api/openapi.yaml`.
- Migration `0008` must be forward-only and must not modify existing site rows.
- The foundation must leave the tree green before the freeze SHA is recorded.

## Review Focus

- The migration must preserve existing sites and create the six tables with the stated foreign keys.
- The shared wire types must not contain password fields in list/detail responses.
- The `dbengine` contract must make MySQL/MariaDB rename fail explicitly, not silently.
- The Add Site request must carry an optional database object without changing the existing response type.
- The shared UI `SecretField` must not render a secret until the parent explicitly sets `revealed`.

## Files Owned

- Create: `internal/store/migrations/0008_databases.sql`
- Modify: `internal/store/store_test.go` (migration version/table assertions)
- Create: `internal/model/database.go`
- Create: `internal/databases/types.go`
- Create: `internal/databases/contracts.go`
- Create: `internal/dbproto/types.go`
- Create: `internal/dbproto/paths.go`
- Create: `internal/dbengine/contracts.go`
- Modify: `internal/sites/service.go` (only `CreateRequest.Database`)
- Modify: `api/openapi.yaml`
- Create: `ui/src/lib/databaseTypes.ts`
- Create: `ui/src/lib/databaseApi.ts`
- Create: `ui/src/lib/databaseMock.ts`
- Create: `ui/src/lib/components/SecretField.svelte`
- Create: `ui/src/lib/components/DatabaseConnectionCard.svelte`
- Modify: `ui/src/lib/api.ts` (export `request`/`isMockMode` only if not already exported)
- Test: colocated `*_test.go` / `*.test.ts`

Do not implement engine SQL, agent/helper routes, daemon handlers, UI tabs, or CLI commands here.

---

### Task 1: Migration 0008 and schema assertions

**Files:**
- Create: `internal/store/migrations/0008_databases.sql`
- Modify: `internal/store/store_test.go`

**Interfaces:**
- Produces tables: `database_users`, `database_links`, `database_user_grants`, `database_backups`, `database_jobs`, `audit_events`.

- [ ] **Step 1: Write the failing migration test**

Add to `store_test.go`:

```go
func TestMigration0008AddsDatabaseTables(t *testing.T) {
    s := openTemp(t)
    for _, table := range []string{
        "database_users", "database_links", "database_user_grants",
        "database_backups", "database_jobs", "audit_events",
    } {
        var name string
        err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
        if err != nil || name != table {
            t.Fatalf("table %s missing: %v", table, err)
        }
    }
}
```

Update `TestMigration0006...` and `TestSchemaAndPragmas` expected max version from `7` to `8`, and add the six tables to the table list in `TestSchemaAndPragmas`.

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/store -run 'TestMigration0008|TestSchemaAndPragmas' -count=1`
Expected: FAIL, max version 7 / missing table.

- [ ] **Step 3: Write the migration**

Create `0008_databases.sql` in this order: `database_users`, `database_links`, `database_user_grants`, `database_backups`, `database_jobs`, `audit_events`. Use `CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS`. Required columns and constraints:

- `database_users`: `id TEXT PRIMARY KEY`, `service_id`, `engine`, `username`, `host DEFAULT ''`, `managed_by_devstack INTEGER DEFAULT 0`, `password_managed INTEGER DEFAULT 0`, `enabled INTEGER DEFAULT 1`, `access_level DEFAULT ''`, `created_at INTEGER DEFAULT 0`, `last_password_rotated_at INTEGER DEFAULT 0`, `last_seen_at INTEGER DEFAULT 0`, `UNIQUE(service_id, username, host)`.
- `database_links`: `id TEXT PRIMARY KEY`, `site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE`, `service_id`, `database_name`, `user_id TEXT NOT NULL REFERENCES database_users(id) ON DELETE RESTRICT`, `is_primary INTEGER DEFAULT 0`, `created_at INTEGER DEFAULT 0`, `UNIQUE(site_id, service_id, database_name)`; partial unique index `database_links_primary_idx ON database_links(site_id) WHERE is_primary = 1`.
- `database_user_grants`: `user_id TEXT NOT NULL REFERENCES database_users(id) ON DELETE CASCADE`, `database_name`, `preset`, `custom_json DEFAULT '[]'`, `updated_at INTEGER DEFAULT 0`, `PRIMARY KEY(user_id, database_name)`.
- `database_backups`: `id TEXT PRIMARY KEY`, `service_id`, `engine`, `database_name`, `path`, `size_bytes INTEGER DEFAULT 0`, `sha256 DEFAULT ''`, `kind`, `status`, `created_at INTEGER DEFAULT 0`, `error DEFAULT ''`; index on `(service_id, database_name)`.
- `database_jobs`: `id TEXT PRIMARY KEY`, `service_id`, `database_name`, `kind`, `status`, `progress INTEGER DEFAULT 0`, `message DEFAULT ''`, `backup_id DEFAULT ''`, `created_at INTEGER DEFAULT 0`, `updated_at INTEGER DEFAULT 0`; index on `created_at`.
- `audit_events`: `id INTEGER PRIMARY KEY AUTOINCREMENT`, `actor DEFAULT ''`, `service_id DEFAULT ''`, `site_id DEFAULT ''`, `object_type DEFAULT ''`, `object_name DEFAULT ''`, `action DEFAULT ''`, `result DEFAULT ''`, `detail_json DEFAULT '{}'`, `created_at INTEGER DEFAULT 0`; index on `created_at`.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/store -run 'TestMigration0008|TestSchemaAndPragmas|TestMigration0006' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/store/migrations/0008_databases.sql internal/store/store_test.go
git commit -m "feat(store): add database management schema"
```

---

### Task 2: Persistence and view model types

**Files:**
- Create: `internal/model/database.go`

**Interfaces:**
- Produces persisted records and API view types used by the store, daemon, client, and UI contract.

- [ ] **Step 1: Write the type compile test**

Create `internal/model/database_test.go`:

```go
func TestDatabaseModelsJSONShape(t *testing.T) {
    link := DatabaseLink{ID: "lnk_1", SiteID: "site_1", ServiceID: "mysql", Database: "app", Username: "app", Primary: true}
    raw, err := json.Marshal(link)
    if err != nil { t.Fatal(err) }
    if strings.Contains(string(raw), "password\":") { t.Fatalf("link leaks a password field: %s", raw) }
    if !strings.Contains(string(raw), `"database":"app"`) { t.Fatalf("link JSON = %s", raw) }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/model -run TestDatabaseModelsJSONShape -count=1`
Expected: FAIL, types undefined.

- [ ] **Step 3: Implement the types**

Define in `internal/model/database.go`:

```go
type DatabaseEngine string
const (
    DatabaseMySQL DatabaseEngine = "mysql"
    DatabaseMariaDB DatabaseEngine = "mariadb"
    DatabasePostgreSQL DatabaseEngine = "postgres"
    DatabaseRedis DatabaseEngine = "redis"
)

type DatabaseGrantPreset string
const (
    GrantReadOnly DatabaseGrantPreset = "read_only"
    GrantReadWrite DatabaseGrantPreset = "read_write"
    GrantReadWriteSchema DatabaseGrantPreset = "read_write_schema"
    GrantAdmin DatabaseGrantPreset = "admin"
    GrantCustom DatabaseGrantPreset = "custom"
)

type DatabaseSiteRef struct { ID string `json:"id"`; Name string `json:"name"` }
type Database struct { Name, ServiceID string; Engine DatabaseEngine; SizeBytes int64; TableCount int; CreatedAt time.Time; LinkedSites []DatabaseSiteRef; Managed bool; Missing bool }
type DatabaseGrant struct { Database string; Preset DatabaseGrantPreset; Privileges []string }
type DatabaseUser struct { ID, ServiceID string; Engine DatabaseEngine; Username, Host string; Enabled, Managed, PasswordManaged bool; AccessLevel string; Grants []DatabaseGrant; LinkedSites []DatabaseSiteRef; CreatedAt time.Time; LastPasswordRotatedAt *time.Time }
type DatabaseConnection struct { Engine DatabaseEngine; Host string; Port int; Database, Username string; PasswordManaged bool; URL string; Env map[string]string }
type DatabaseLink struct { ID, SiteID, SiteName, ServiceID string; Engine DatabaseEngine; Database, UserID, Username, Host string; Primary, PasswordManaged, Available bool; Error string; Connection DatabaseConnection }
type DatabaseBackupKind string
const ( BackupManual DatabaseBackupKind = "manual"; BackupPreDrop = "pre_drop"; BackupPreRestore = "pre_restore"; BackupPreImport = "pre_import" )
type DatabaseBackupStatus string
const ( BackupReady = "ready"; BackupRunning = "running"; BackupFailed = "failed" )
type DatabaseBackup struct { ID, ServiceID string; Engine DatabaseEngine; Database string; SizeBytes int64; SHA256 string; Kind DatabaseBackupKind; Status DatabaseBackupStatus; CreatedAt time.Time; Error string }
type DatabaseJobKind string
const ( JobBackup = "backup"; JobRestore = "restore"; JobImport = "import" )
type DatabaseJobStatus string
const ( JobQueued = "queued"; JobRunning = "running"; JobSucceeded = "succeeded"; JobFailed = "failed"; JobCanceled = "canceled" )
type DatabaseJob struct { ID, ServiceID, Database string; Kind DatabaseJobKind; Status DatabaseJobStatus; Progress int; Message, BackupID string; CreatedAt, UpdatedAt time.Time }
type AuditEvent struct { ID int64; Actor, ServiceID, SiteID, ObjectType, ObjectName, Action, Result, DetailJSON string; CreatedAt time.Time }
```

Add persisted record types with the same fields plus `DatabaseLinkRecord`, `DatabaseUserRecord`, `DatabaseGrantRecord`, `DatabaseBackupRecord`, `DatabaseJobRecord`, `AuditEventRecord`. Use `int64` Unix seconds for `CreatedAt`/`UpdatedAt` in records; convert at the store boundary. JSON tags are snake_case for view types and omitted where empty.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/model -run TestDatabaseModelsJSONShape -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/model/database.go internal/model/database_test.go
git commit -m "feat(model): add database management types"
```

---

### Task 3: Domain contracts and requests

**Files:**
- Create: `internal/databases/types.go`
- Create: `internal/databases/contracts.go`

**Interfaces:**
- Produces the request/response types and the `Platform` and `Service` interfaces used by the daemon lane.

- [ ] **Step 1: Write the compile test**

Create `internal/databases/contracts_test.go`:

```go
func TestSiteDatabaseRequestShape(t *testing.T) {
    req := SiteDatabaseRequest{ServiceID: "mysql", NewDatabase: &CreateDatabaseRequest{Name: "app"}, NewUser: &CreateUserRequest{Username: "app"}}
    if req.ServiceID != "mysql" || req.NewDatabase.Name != "app" || req.NewUser.Username != "app" {
        t.Fatalf("request = %+v", req)
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/databases -run TestSiteDatabaseRequestShape -count=1`
Expected: FAIL, package/types missing.

- [ ] **Step 3: Implement the contracts**

`types.go`:

```go
type CreateDatabaseRequest struct { Name, Charset, Collation string }
type RenameDatabaseRequest struct { Name string }
type DropDatabaseRequest struct { Confirm string; BackupFirst bool }
type CreateUserRequest struct { Username, Host string; Grants []model.DatabaseGrant; AccessLevel string }
type UpdateUserRequest struct { Enabled *bool; Grants []model.DatabaseGrant; AccessLevel string }
type DeleteUserRequest struct { Confirm string; ReassignOwned bool }
type CreateBackupRequest struct { Kind model.DatabaseBackupKind }
type RestoreBackupRequest struct { Target string; Overwrite bool; Confirm string; PreRestoreBackup bool }
type ImportMode string
const ( ImportMerge ImportMode = "merge"; ImportReplace ImportMode = "replace" )
type ImportRequest struct { Mode ImportMode; Confirm string; PreImportBackup bool }
type LinkDatabaseRequest struct { ServiceID, Database, UserID, Password string; Primary bool }
type SiteDatabaseRequest struct { ServiceID, Database string; NewDatabase *CreateDatabaseRequest; UserID string; NewUser *CreateUserRequest; Password string; Grant model.DatabaseGrantPreset; Primary bool }
type ProvisionedDatabase struct { Database model.Database; User model.DatabaseUser; Warning string; Request SiteDatabaseRequest }
type SiteDatabaseResult struct { Link model.DatabaseLink; Database model.Database; User model.DatabaseUser; Warning string }
type Secret struct { Value string `json:"value"` }
```

`contracts.go`:

```go
var (
    ErrNotFound = errors.New("database: not found")
    ErrConflict = errors.New("database: conflict")
    ErrInvalidRequest = errors.New("database: invalid request")
    ErrProtected = errors.New("database: protected object")
    ErrUnsupported = errors.New("database: unsupported operation")
    ErrServiceInactive = errors.New("database: service is not active")
    ErrLinkedSites = errors.New("database: user is linked to sites")
    ErrPasswordMissing = errors.New("database: password is not managed")
    ErrJobNotFound = errors.New("database: job not found")
)

type Platform interface {
    ListDatabases(ctx context.Context, serviceID string) ([]model.Database, error)
    CreateDatabase(ctx context.Context, serviceID string, req CreateDatabaseRequest) (model.Database, error)
    RenameDatabase(ctx context.Context, serviceID, name string, req RenameDatabaseRequest) (model.Database, error)
    DropDatabase(ctx context.Context, serviceID, name string, req DropDatabaseRequest) error
    ListUsers(ctx context.Context, serviceID string) ([]model.DatabaseUser, error)
    CreateUser(ctx context.Context, serviceID string, req CreateUserRequest) (model.DatabaseUser, error)
    UpdateUser(ctx context.Context, serviceID, userID string, req UpdateUserRequest) (model.DatabaseUser, error)
    DeleteUser(ctx context.Context, serviceID, userID string, req DeleteUserRequest) error
    SetUserPassword(ctx context.Context, serviceID, userID string) (Secret, error)
    RevealUserPassword(ctx context.Context, serviceID, userID string) (Secret, error)
    ListBackups(ctx context.Context, serviceID string) ([]model.DatabaseBackup, error)
    CreateBackup(ctx context.Context, serviceID, database string, req CreateBackupRequest) (model.DatabaseJob, error)
    DeleteBackup(ctx context.Context, serviceID, backupID string) error
    RestoreBackup(ctx context.Context, serviceID, backupID string, req RestoreBackupRequest) (model.DatabaseJob, error)
    ImportDatabase(ctx context.Context, serviceID, database string, file io.Reader, filename string, req ImportRequest) (model.DatabaseJob, error)
    ExportDatabase(ctx context.Context, serviceID, database string) (io.ReadCloser, error)
    DownloadBackup(ctx context.Context, serviceID, backupID string) (io.ReadCloser, error)
    GetJob(ctx context.Context, jobID string) (model.DatabaseJob, error)
    CancelJob(ctx context.Context, jobID string) error
    ResolveSiteEnv(ctx context.Context, siteID string, links []model.DatabaseLink) (map[string]string, error)
}

type Service interface {
    // Same service-scoped methods as Platform, plus:
    ListSiteLinks(ctx context.Context, siteRef string) ([]model.DatabaseLink, error)
    LinkDatabase(ctx context.Context, siteRef string, req LinkDatabaseRequest) (model.DatabaseLink, error)
    UnlinkDatabase(ctx context.Context, siteRef, linkID string) error
    CreateSiteDatabase(ctx context.Context, siteRef string, req SiteDatabaseRequest) (SiteDatabaseResult, error)
}
```

`Service` should not duplicate the whole `Platform`; use embedding:

```go
type Service interface {
    Platform
    ListSiteLinks(...)
    LinkDatabase(...)
    UnlinkDatabase(...)
    CreateSiteDatabase(...)
}
```

- [ ] **Step 4: Re-run**

Run: `go test ./internal/databases -run TestSiteDatabaseRequestShape -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/databases
git commit -m "feat(databases): define domain contracts"
```

---

### Task 4: Shared wire protocol

**Files:**
- Create: `internal/dbproto/types.go`
- Create: `internal/dbproto/paths.go`
- Test: `internal/dbproto/paths_test.go`

**Interfaces:**
- Produces the request/response DTOs and path helpers used by both the agent and helper transports.

- [ ] **Step 1: Write the failing path test**

```go
func TestPathsEscapeSegments(t *testing.T) {
    if got := ServiceDatabase("mysql", "app db"); got != "/v1/services/mysql/databases/app%20db" {
        t.Fatalf("path = %q", got)
    }
    if got := ServiceUser("mysql", "usr/1"); got != "/v1/services/mysql/users/usr%2F1" {
        t.Fatalf("path = %q", got)
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/dbproto -run TestPathsEscapeSegments -count=1`
Expected: FAIL, package missing.

- [ ] **Step 3: Implement the DTOs and paths**

Define JSON DTOs mirroring the model view types: `DatabaseInfo`, `UserInfo`, `GrantInfo`, `BackupInfo`, `JobInfo`, `LinkInfo`, `ConnectionInfo`, `SecretValue`, plus request types:

```go
type DatabaseListRequest struct { ID string `json:"id"` }
type DatabaseCreateRequest struct { ID, Name, Charset, Collation string }
type DatabaseRenameRequest struct { ID, Name, NewName string }
type DatabaseDropRequest struct { ID, Name, Confirm string }
type UserListRequest struct { ID string }
type UserCreateRequest struct { ID, Username, Host string; Grants []GrantInfo; AccessLevel string }
type UserUpdateRequest struct { ID, UserID string; Enabled *bool; Grants []GrantInfo; AccessLevel string }
type UserDeleteRequest struct { ID, UserID, Confirm string; ReassignOwned bool }
type UserPasswordRequest struct { ID, UserID string }
type UserRevealRequest struct { ID, UserID string }
type BackupListRequest struct { ID string }
type BackupCreateRequest struct { ID, Database string; Kind string }
type BackupDeleteRequest struct { ID, BackupID string }
type BackupRestoreRequest struct { ID, BackupID, Target, Confirm string; Overwrite, PreRestoreBackup bool }
type DatabaseImportRequest struct { ID, Database string; Mode, Confirm string; PreImportBackup bool }
type DatabaseExportRequest struct { ID, Database string }
type JobGetRequest struct { JobID string }
type JobCancelRequest struct { JobID string }
type SiteEnvRequest struct { SiteID string; Links []LinkInfo }
```

Responses: `DatabaseListResponse`, `DatabaseResponse`, `UserListResponse`, `UserResponse`, `SecretResponse`, `BackupListResponse`, `JobResponse`, `LinkListResponse`, `LinkResponse`, `SiteEnvResponse`, `DeleteResponse`.

Paths in `paths.go` (all dynamic segments `url.PathEscape`):

```go
func ServiceDatabases(id string) string
func ServiceDatabase(id, name string) string
func ServiceUsers(id string) string
func ServiceUser(id, userID string) string
func ServiceUserPassword(id, userID string) string
func ServiceUserReveal(id, userID string) string
func ServiceDatabaseBackups(id, name string) string
func ServiceBackup(id, backupID string) string
func ServiceBackupDownload(id, backupID string) string
func ServiceBackupRestore(id, backupID string) string
func ServiceDatabaseImport(id, name string) string
func ServiceDatabaseExport(id, name string) string
func Job(jobID string) string
func SiteEnv(siteID string) string
```

Query constants: `QueryMode`, `QueryConfirm`, `QueryPreImportBackup`, `QueryTarget`, `QueryOverwrite`, `QueryPreRestoreBackup`.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/dbproto -run TestPathsEscapeSegments -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/dbproto
git commit -m "feat(dbproto): add shared database wire contract"
```

---

### Task 5: Engine contract

**Files:**
- Create: `internal/dbengine/contracts.go`
- Test: `internal/dbengine/contracts_test.go`

**Interfaces:**
- Produces `Engine`, `Registry`, `RedisACL`, `ErrUnsupported`, shared validation/quoting helpers.
- Lane A implements concrete MySQL/MariaDB/PostgreSQL/Redis engines and `DefaultRegistry()`.
- Lane H consumes the interface; lane B does not import `dbengine`.

- [ ] **Step 1: Write the failing validation test**

```go
func TestValidateIdentifierRejectsUnsafeNames(t *testing.T) {
    for _, name := range []string{"", "app;drop", "app`db", "app\n", "../app", "app db"} {
        if err := ValidateIdentifier(name); err == nil {
            t.Fatalf("ValidateIdentifier(%q) = nil", name)
        }
    }
    if err := ValidateIdentifier("app_1"); err != nil {
        t.Fatalf("valid name rejected: %v", err)
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/dbengine -run TestValidateIdentifierRejectsUnsafeNames -count=1`
Expected: FAIL, package missing.

- [ ] **Step 3: Implement the contract**

```go
var (
    ErrUnsupported = errors.New("dbengine: unsupported operation")
    ErrInvalidIdentifier = errors.New("dbengine: invalid identifier")
    ErrProtected = errors.New("dbengine: protected object")
)

type Engine interface {
    ID() model.DatabaseEngine
    ValidateDatabaseName(string) error
    ValidateUsername(string) error
    ValidateHost(string) error
    ProtectedDatabase(string) bool
    ProtectedUser(string, string) bool
    RenameSupported() bool
    ListDatabasesCommand() ([]string, error)
    ParseDatabases([]byte) ([]model.Database, error)
    CreateDatabaseSQL(name, charset, collation string) (string, error)
    DropDatabaseSQL(name string) (string, error)
    RenameDatabaseSQL(name, newName string) (string, error)
    ListUsersCommand() ([]string, error)
    ParseUsers([]byte) ([]model.DatabaseUser, error)
    ListGrantsCommand(username, host string) ([]string, error)
    ParseGrants([]byte) ([]model.DatabaseGrant, error)
    CreateUserSQL(username, host, password string) (string, error)
    SetPasswordSQL(username, host, password string) (string, error)
    DeleteUserSQL(username, host string) (string, error)
    GrantSQL(username, host, database string, grant model.DatabaseGrant) (string, error)
    RevokeSQL(username, host, database string, grant model.DatabaseGrant) (string, error)
    TerminateSessionsSQL(database string) (string, error)
    DumpCommand(database string) ([]string, error)
    RestoreCommand(database string) ([]string, error)
}

type RedisACL interface {
    ListUsersCommand() []string
    SetUserCommand(username, password, accessLevel string, enabled bool) []string
    DeleteUserCommand(username string) []string
    SaveCommand() []string
    ACLFile() string
}

type Registry interface { For(engine model.DatabaseEngine) (Engine, error) }

// DefaultRegistry is set by the concrete engine package (agent lane) at init.
// The helper lane calls it; if nil, database operations return ErrUnsupported.
var DefaultRegistry func() Registry

func ValidateIdentifier(name string) error
func QuoteIdentifier(engine model.DatabaseEngine, name string) (string, error)
func QuoteLiteral(value string) string
```

`ValidateIdentifier` accepts `^[A-Za-z_][A-Za-z0-9_]*$` and length 1-63. `QuoteIdentifier` doubles the engine quote character after validation; MySQL/MariaDB use backticks, PostgreSQL/Redis use double quotes. `QuoteLiteral` doubles single quotes and rejects NUL/newlines. Add `SystemDatabases`/`SystemUsers` maps for reuse.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/dbengine -run TestValidateIdentifierRejectsUnsafeNames -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/dbengine
git commit -m "feat(dbengine): define engine contract and validation"
```

---

### Task 6: Add Site request field

**Files:**
- Modify: `internal/sites/service.go:42-56`
- Test: `internal/sites/service_test.go`

**Interfaces:**
- Produces `CreateRequest.Database *databases.SiteDatabaseRequest`.
- The response type stays `model.Site`; no response shape change.

- [ ] **Step 1: Write the failing test**

```go
func TestCreateRequestCarriesOptionalDatabase(t *testing.T) {
    req := CreateRequest{Name: "blog", Database: &databases.SiteDatabaseRequest{ServiceID: "mysql", Database: "app", UserID: "usr_app"}}
    if req.Database == nil || req.Database.ServiceID != "mysql" {
        t.Fatalf("database = %+v", req.Database)
    }
}
```

- [ ] **Step 2: Run and watch fail**

Run: `go test ./internal/sites -run TestCreateRequestCarriesOptionalDatabase -count=1`
Expected: FAIL, field missing.

- [ ] **Step 3: Add the field**

Add `Database *databases.SiteDatabaseRequest \`json:"database,omitempty"\`` to `CreateRequest` and import `internal/databases`. Do not change `Create` behavior in this task; the daemon lane implements provisioning. The field is optional and nil-safe.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/sites -run TestCreateRequestCarriesOptionalDatabase -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/sites/service.go internal/sites/service_test.go
git commit -m "feat(sites): carry optional database request"
```

---

### Task 7: OpenAPI contract

**Files:**
- Modify: `api/openapi.yaml`

**Interfaces:**
- Documents every database route and the Add Site `database` field.

- [ ] **Step 1: Add the paths and schemas**

Add the service-scoped paths from the design and the site link paths:

```text
GET/POST     /api/v1/services/{id}/databases
GET/PATCH/DELETE /api/v1/services/{id}/databases/{name}
GET/POST     /api/v1/services/{id}/users
GET/PATCH/DELETE /api/v1/services/{id}/users/{userID}
POST         /api/v1/services/{id}/users/{userID}/password
POST         /api/v1/services/{id}/users/{userID}/reveal
GET          /api/v1/services/{id}/backups
POST         /api/v1/services/{id}/databases/{name}/backups
GET          /api/v1/services/{id}/backups/{backupID}/download
DELETE       /api/v1/services/{id}/backups/{backupID}
POST         /api/v1/services/{id}/backups/{backupID}/restore
POST         /api/v1/services/{id}/databases/{name}/import
GET          /api/v1/services/{id}/databases/{name}/export
GET/DELETE   /api/v1/services/{id}/jobs/{jobID}
POST         /api/v1/services/{id}/password
GET/POST     /api/v1/sites/{ref}/databases
DELETE       /api/v1/sites/{ref}/databases/{linkID}
GET          /api/v1/sites/{ref}/database-env
```

Add schemas: `Database`, `DatabaseUser`, `DatabaseGrant`, `DatabaseBackup`, `DatabaseJob`, `DatabaseLink`, `DatabaseConnection`, `DatabaseSecret`, `CreateDatabaseRequest`, `RenameDatabaseRequest`, `DropDatabaseRequest`, `CreateDatabaseUserRequest`, `UpdateDatabaseUserRequest`, `DeleteDatabaseUserRequest`, `CreateDatabaseBackupRequest`, `RestoreDatabaseBackupRequest`, `ImportDatabaseRequest`, `LinkDatabaseRequest`, `SiteDatabaseRequest`, `SiteDatabaseResult`. Extend `CreateSiteRequest` with `database: $ref: '#/components/schemas/SiteDatabaseRequest'` (optional). No schema may contain a `password` property except `DatabaseSecret`; list responses must reference the no-password schemas.

- [ ] **Step 2: Validate**

Run: `go test ./internal/api -run TestOpenAPIDocumentParsesAndDocumentsRoutes -count=1`
Expected: PASS (extra documented paths are allowed; B adds matching routes later).

- [ ] **Step 3: Commit**

```powershell
git add api/openapi.yaml
git commit -m "docs(api): define database management contract"
```

---

### Task 8: UI types, API client, and mock

**Files:**
- Create: `ui/src/lib/databaseTypes.ts`
- Create: `ui/src/lib/databaseApi.ts`
- Create: `ui/src/lib/databaseMock.ts`
- Modify: `ui/src/lib/api.ts` (export `request`/`isMockMode` only if needed)
- Test: `ui/src/lib/databaseApi.test.ts`

**Interfaces:**
- Produces all types/functions consumed by the two UI lanes.

- [ ] **Step 1: Write the failing API test**

```ts
it('lists databases in mock mode and resets state', async () => {
  resetMockDatabaseStateForTests();
  const result = await listServiceDatabases('mysql');
  expect(result.databases.map((d) => d.name)).toContain('app');
  const created = await createServiceDatabase('mysql', { name: 'blog' });
  expect(created.name).toBe('blog');
  expect((await listServiceDatabases('mysql')).databases.map((d) => d.name)).toContain('blog');
});

it('reveals a user secret only through the reveal call', async () => {
  const secret = await revealServiceUserPassword('mysql', 'usr_app');
  expect(secret.value).toBe('mock_db_password');
});
```

- [ ] **Step 2: Run and watch fail**

Run: `cd ui; npx vitest run src/lib/databaseApi.test.ts`
Expected: FAIL, module missing.

- [ ] **Step 3: Implement the contract**

`databaseTypes.ts`: mirror the JSON view types from the spec, including `Database`, `DatabaseUser`, `DatabaseGrant`, `DatabaseBackup`, `DatabaseJob`, `DatabaseLink`, `DatabaseConnection`, `SiteDatabaseLink`, `SiteDatabaseResult`, `Secret`, request bodies, `DatabaseEngine`, `GrantPreset`, `DatabaseJobStatus`. Extend `CreateSiteRequest` in `ui/src/lib/types.ts` with `database?: CreateSiteDatabaseRequest` (foundation-owned edit).

`databaseApi.ts`: implement the functions listed in the UI lane plans:

```ts
listServiceDatabases, createServiceDatabase, getServiceDatabase,
renameServiceDatabase, dropServiceDatabase, listServiceUsers,
createServiceUser, updateServiceUser, deleteServiceUser,
setServiceUserPassword, revealServiceUserPassword, listServiceBackups,
createServiceBackup, deleteServiceBackup, restoreServiceBackup,
downloadServiceBackup, importServiceDatabase, exportServiceDatabase,
getDatabaseJob, cancelDatabaseJob, listSiteDatabases, linkSiteDatabase,
unlinkSiteDatabase, createSiteDatabase
```

Each function checks `isMockMode` and delegates to `mockDatabaseApi`; otherwise uses `request<T>` with `servicePath`/`sitePath` or a local path builder. Use `POST` for reveal/password, `PATCH` for rename/update, `DELETE` for drops. Import uses `FormData`; download/export return `Blob`.

`databaseMock.ts`: seeded MySQL (`app`, `blog` databases; `app` managed user; `devstack` protected), PostgreSQL (`app`), Redis (`cache` read-only, `default`). Implement create/drop/user/password/backup/link mutations and `resetMockDatabaseStateForTests()`. Never include a password in list/detail fixtures; only `revealServiceUserPassword` returns `mock_db_password`.

- [ ] **Step 4: Re-run**

Run: `cd ui; npx vitest run src/lib/databaseApi.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/lib/databaseTypes.ts ui/src/lib/databaseApi.ts ui/src/lib/databaseMock.ts ui/src/lib/databaseApi.test.ts ui/src/lib/types.ts ui/src/lib/api.ts
git commit -m "feat(ui): add database API contract and mocks"
```

---

### Task 9: Shared secret and connection components

**Files:**
- Create: `ui/src/lib/components/SecretField.svelte`
- Create: `ui/src/lib/components/DatabaseConnectionCard.svelte`
- Test: `ui/src/lib/components/SecretField.test.ts`
- Test: `ui/src/lib/components/DatabaseConnectionCard.test.ts`

**Interfaces:**
- `SecretField`: `{ label: string; value?: string; revealed: boolean; loading?: boolean; error?: string; onreveal: () => void; onhide: () => void; oncopy?: () => void }`.
- `DatabaseConnectionCard`: `{ connection: DatabaseConnection; title?: string; showEnv?: boolean; children?: Snippet }`.

- [ ] **Step 1: Write the failing tests**

```ts
it('does not render the secret until revealed', async () => {
  const { rerender } = render(SecretField, { props: { label: 'Password', value: 's3cret', revealed: false, onreveal: vi.fn(), onhide: vi.fn() } });
  expect(screen.queryByText('s3cret')).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: /reveal password/i }));
  rerender({ value: 's3cret', revealed: true, onreveal: vi.fn(), onhide: vi.fn(), label: 'Password' });
  expect(screen.getByText('s3cret')).toBeInTheDocument();
});

it('shows host, port, database, username, and env snippet', () => {
  render(DatabaseConnectionCard, { props: { connection: MYSQL_CONNECTION, showEnv: true } });
  expect(screen.getByText('127.0.0.1:3306')).toBeInTheDocument();
  expect(screen.getByText('app')).toBeInTheDocument();
  expect(screen.getByText('app_user')).toBeInTheDocument();
  expect(screen.getByText(/DB_DATABASE=app/)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run and watch fail**

Run: `cd ui; npx vitest run src/lib/components/SecretField.test.ts src/lib/components/DatabaseConnectionCard.test.ts`
Expected: FAIL, components missing.

- [ ] **Step 3: Implement the components**

`SecretField` renders a label, `••••••••` when `!revealed`, the value when `revealed`, and Reveal/Hide/Copy buttons. It calls `onreveal` only from the Reveal button and never fetches. It clears nothing itself; the parent controls `revealed`.

`DatabaseConnectionCard` renders engine badge, `host:port`, database, username, password state (`Managed`/`Not managed`), optional `DATABASE_URL`, and an optional env `<pre>` built from `connection.env`. It never renders a password. Use existing `Badge` and `Button`.

- [ ] **Step 4: Re-run**

Run: `cd ui; npx vitest run src/lib/components/SecretField.test.ts src/lib/components/DatabaseConnectionCard.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add ui/src/lib/components/SecretField.svelte ui/src/lib/components/SecretField.test.ts ui/src/lib/components/DatabaseConnectionCard.svelte ui/src/lib/components/DatabaseConnectionCard.test.ts
git commit -m "feat(ui): add shared database secret components"
```

---

### Task 10: Foundation freeze gate

- [ ] **Step 1: Run the full foundation gate**

```powershell
go test ./... -count=1
go vet ./...
go build ./...
cd ui; npm run check; npm test -- --run; npm run build; cd ..
git status --short
```

Expected: all pass; clean tree after the final commit.

- [ ] **Step 2: Commit any remaining foundation files**

```powershell
git add internal/store internal/model internal/databases internal/dbproto internal/dbengine internal/sites/service.go api/openapi.yaml ui/src/lib
git commit -m "chore(db): freeze database management foundation"
```

- [ ] **Step 3: Record the freeze SHA**

```powershell
$base = git rev-parse HEAD
$base | Set-Content .superpowers\db-foundation-sha.txt
Write-Output "foundation=$base"
```

- [ ] **Step 4: Stop**

Do not create worktrees or dispatch lanes in this plan. The controller proceeds to the master plan's worktree creation and lane dispatch.
