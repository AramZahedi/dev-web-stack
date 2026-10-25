# Service Platform Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps
> use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Freeze the root layout, service registry, Compose control plane, config-conflict
engine, site contract fields, and service API/CLI/UI extension points so the independent
service lanes can run in parallel.

**Architecture:** This is Wave 0 of the
[program plan](2026-10-25-service-platform-program.md). It uses safe parallel phases
(0A: F1/F2/F4, 0B: F3/F5, 0C: F6). It does not install real services; it builds the
contracts and control plane that later waves use.

**Tech Stack:** Go 1.26, SQLite (`modernc.org/sqlite`), Svelte 5/Vite/TypeScript, Docker
Compose command runner, systemd helper.

**Spec:** `docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md`

## Global Constraints

- Native devstack; Docker only for optional service containers.
- No native package/systemd management for services.
- `/data/devstack` is the recommended configurable root.
- One active web server and one active MySQL-family service; PostgreSQL/Redis independent.
- Config/data/certs/secrets/backups are host-owned.
- Native CA/trust; containers receive leaf certs only.
- No silent config overwrite.
- Native PHP/Node; global PHP/Composer are separate later lanes.
- Dashboard and CLI share the same API; no CLI-only operational tasks.
- Linux only; Windows/WSL Plan 3 deferred.
- No invented image digests/checksums.

## Review Focus

1. A future generated config can be overwritten without a conflict record.
2. A future service switch can leave conflicting ports active or no rollback.
3. New site fields can be lost or misinterpreted by store/API/UI.
4. The helper Compose boundary can accept an arbitrary command/ID/path.
5. The foundation can merge while leaving M1 tests or the UI red.

---

## Wave 0 ownership and parallel order

Wave 0 is split into safe phases. The maximum is three implementation worktrees.

| Phase | Task | Depends on | Branch | Owns |
|---|---|---|---|---|
| 0A | F1 | none | `feat/services-foundation-f1` | `internal/config` layout/permissions |
| 0A | F2 | none | `feat/services-foundation-f2` | `internal/services` catalog/state + SQLite service state |
| 0A | F4 | none | `feat/services-foundation-f4` | `internal/serviceconfig` manifest/conflict engine |
| 0B | F3 | F2 | `feat/services-foundation-f3` | Compose rendering, helper RPC/client, fixed Docker runner |
| 0B | F5 | F2 | `feat/services-foundation-f5` | `internal/model`/store/site API contract fields |
| 0C | F6 | F1, F2, F5 | `feat/services-foundation-f6` | service API/CLI/UI read-only skeleton |

Integration branches:

- `integration/services-foundation` is created from `master`.
- 0A tasks branch from `master`, merge in order F1 → F2 → F4 (safe order).
- 0B tasks branch from the merged 0A tip, merge F3 → F5.
- F6 branches from the merged 0B tip.
- After F6, run the Wave 0 checkpoint and merge the integration branch to `master`.

Do not start Wave 1 until all tasks are merged and the checkpoint is green.

Hot files F1/F2/F5/F6 are foundation-owned. F3 and F5 must not edit each other's files.
F6 is serial because it edits API/UI hot files after F5.

---

### Task F1: Root layout and service paths

**Parallel group:** 0A · **Depends on:** none · **Branch:** `feat/services-foundation-f1`

**Files:**
- Modify: `internal/config/paths.go`
- Test: `internal/config/paths_test.go` (create)
- Modify: `.gitignore`

**Interfaces:**
- Produces: `config.Paths` fields:
  - `Projects string`
  - `Services string`
  - `ServiceCompose string`
  - `Secrets string`
  - `Certs string`
  - `Backups string`
  - `Tools string`
  - `CLI string`
- Produces: `config.NewPaths(home string) Paths` populates the fields.
- Produces: `Paths.Ensure() error` creates all directories with the documented modes.

- [ ] **Step 1: Write the failing test**

Create `internal/config/paths_test.go`:

```go
func TestNewPathsIncludesServiceLayout(t *testing.T) {
	p := NewPaths("/data/devstack")
	if p.Projects != "/data/devstack/projects" { ... }
	if p.Services != "/data/devstack/services" { ... }
	if p.ServiceCompose != "/data/devstack/services/compose.yaml" { ... }
	if p.Secrets != "/data/devstack/secrets" { ... }
	if p.Certs != "/data/devstack/certs" { ... }
	if p.Backups != "/data/devstack/backups" { ... }
	if p.Tools != "/data/devstack/tools" { ... }
	if p.CLI != "/data/devstack/config/cli" { ... }
}

func TestEnsureCreatesLayoutAndSecretMode(t *testing.T) {
	root := t.TempDir()
	p := NewPaths(root)
	if err := p.Ensure(); err != nil { t.Fatal(err) }
	for _, dir := range []string{p.Projects, p.Services, p.Secrets, p.Certs, p.Backups, p.Tools, p.CLI} {
		if _, err := os.Stat(dir); err != nil { t.Fatal(err) }
	}
	info, err := os.Stat(p.Secrets)
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0o700 { t.Fatalf("secrets mode = %o", info.Mode().Perm()) }
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config -run 'TestNewPathsIncludesServiceLayout|TestEnsureCreatesLayoutAndSecretMode' -count=1`

Expected: compile failure for the missing fields.

- [ ] **Step 3: Implement the paths**

Add the fields to `Paths` and `NewPaths` using `filepath.Join(home, ...)`. Extend the
normal `Ensure` directory list with `Projects`, `Services`, `Certs`, `Backups`, `Tools`,
and `CLI` at `0755`. Create `Secrets` at `0700` and enforce its mode with
`os.Chmod(p.Secrets, 0o700)` after `MkdirAll`.

Add `.worktrees/` to `.gitignore`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/paths.go internal/config/paths_test.go .gitignore
git commit -m "feat: add service root layout paths"
```

---

### Task F2: Service catalog and state store

**Parallel group:** 0A · **Depends on:** none · **Branch:** `feat/services-foundation-f2`

**Files:**
- Create: `internal/services/catalog.go`
- Test: `internal/services/catalog_test.go`
- Create: `internal/services/state.go`
- Test: `internal/services/state_test.go`
- Create: `internal/store/migrations/0003_services.sql`
- Modify: `internal/store/store.go`
- Test: `internal/store/service_state_test.go` (create)

**Interfaces:**
- Produces:

```go
package services

type ID string
const (
	IDNginx ID = "nginx"
	IDApache ID = "apache"
	IDMySQL ID = "mysql"
	IDMariaDB ID = "mariadb"
	IDPostgreSQL ID = "postgresql"
	IDRedis ID = "redis"
	IDPHPMyAdmin ID = "phpmyadmin"
	IDImageMagick ID = "imagemagick"
)

type Group string
const (
	GroupWeb Group = "web"
	GroupMySQLFamily Group = "mysql-family"
	GroupStandalone Group = "standalone"
)

type State string
const (
	StateNotInstalled State = "not_installed"
	StateInstalled State = "installed"
	StateStarting State = "starting"
	StateActive State = "active"
	StateStopping State = "stopping"
	StateError State = "error"
)

type Port struct {
	Host int
	Container int
	Protocol string // "http", "https", "tcp"
	Loopback bool
}

type Mount struct {
	HostSubdir string
	ContainerPath string
	ReadOnly bool
}

type Service struct {
	ID ID
	DisplayName string
	Group Group
	Image string
	NetworkMode string // "host", "bridge", ""
	Ports []Port
	Config []Mount
	Data []Mount
	DependsOn []ID
}

type Catalog struct {
	Services []Service
}

type Record struct {
	ID ID
	State State
	Image string
	Active bool
	LastError string
	LastOperation string
	UpdatedAt time.Time
}

type View struct {
	ID ID
	DisplayName string
	Group Group
	State State
	Active bool
	Image string
	Ports []Port
	LastError string
	ConfigPath string
	DataPath string
}
```

- Produces:
  - `func DefaultCatalog() Catalog` (initially empty; Wave 1 adds entries)
  - `func (c Catalog) Get(id ID) (Service, bool)`
  - `func (c Catalog) Validate() error` (unique IDs; valid group/state; required image only
    when an entry is present; ports/IDs validated)
  - `func (g Group) Exclusive() bool` (web and mysql-family true)
- Produces store methods:
  - `SaveServiceState(ctx context.Context, rec services.Record) error`
  - `GetServiceState(ctx context.Context, id services.ID) (services.Record, error)`
  - `ListServiceStates(ctx context.Context) ([]services.Record, error)`
  - `DeleteServiceState(ctx context.Context, id services.ID) error`

- [ ] **Step 1: Write the failing catalog tests**

Tests:

```go
func TestCatalogValidateRejectsDuplicateIDs(t *testing.T) { ... }
func TestCatalogValidateRejectsWebServiceWithoutPort(t *testing.T) { ... }
func TestGroupsExclusive(t *testing.T) {
	if !GroupWeb.Exclusive() || !GroupMySQLFamily.Exclusive() { t.Fatal(...) }
	if GroupStandalone.Exclusive() { t.Fatal(...) }
}
```

- [ ] **Step 2: Write the failing store test**

Create `internal/store/service_state_test.go`:

```go
func TestServiceStateRoundTrip(t *testing.T) {
	st := newTestStore(t)
	rec := services.Record{
		ID: services.IDMySQL, State: services.StateActive, Image: "fixture@sha256:...",
		Active: true, LastOperation: "activate",
	}
	if err := st.SaveServiceState(ctx, rec); err != nil { t.Fatal(err) }
	got, err := st.GetServiceState(ctx, services.IDMySQL)
	...
	if got.State != rec.State || !got.Active { ... }
	list, err := st.ListServiceStates(ctx)
	...
	if err := st.DeleteServiceState(ctx, services.IDMySQL); err != nil { ... }
}
```

Use a real SHA-256-shaped fixture string in the test, not a fake URL.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/services ./internal/store -run 'TestCatalog|TestServiceState' -count=1`

Expected: package/type or undefined-method failures.

- [ ] **Step 4: Implement catalog/state and migration**

Migration `0003_services.sql`:

```sql
CREATE TABLE IF NOT EXISTS services (
    id              TEXT PRIMARY KEY,
    state           TEXT NOT NULL DEFAULT 'not_installed',
    image           TEXT NOT NULL DEFAULT '',
    active          INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    last_operation  TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL DEFAULT 0
);
```

Implement store methods using the existing `scan`/`requireAffected` patterns. Do not
invent a production catalog; `DefaultCatalog` returns an empty `Catalog{}` until Wave 1
adds real digest-pinned entries.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/services ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/services internal/store/migrations/0003_services.sql internal/store/store.go internal/store/service_state_test.go
git commit -m "feat: add service catalog and state registry"
```

---

### Task F3: Compose rendering and fixed helper control plane

**Parallel group:** 0B · **Depends on:** F2 · **Branch:** `feat/services-foundation-f3`

**Files:**
- Create: `internal/services/compose.go`
- Test: `internal/services/compose_test.go`
- Create: `internal/helperproto/services.go`
- Create: `internal/helper/services.go`
- Test: `internal/helper/services_test.go`
- Create: `internal/helper/service_docker.go`
- Modify: `internal/helper/server.go`
- Modify: `internal/helperclient/client.go`
- Modify: `internal/helper/server.go` `Config` and `Service` extension points

**Interfaces:**
- Produces `services.RenderCompose(catalog Catalog, root string) ([]byte, error)`:

  The generated YAML contains every catalog service with:
  - container name `devstack-<id>`;
  - labels `devstack.service=<id>` and `devstack.root=<root-hash>`;
  - image;
  - network mode;
  - config/data bind mounts rooted at `root/services/<id>`;
  - loopback `127.0.0.1:<host>:<container>` ports;
  - `restart: unless-stopped`;
  - no Docker socket mount;
  - deterministic service ordering.

  Reject empty image, duplicate IDs, unsafe relative mounts, non-loopback data ports, and
  paths escaping `root`.

- Produces helper RPC types:

```go
const (
	PathServicesStatus = "/v1/services/status"
	PathServicesInstall = "/v1/services/install"
	PathServicesActivate = "/v1/services/activate"
	PathServicesDeactivate = "/v1/services/deactivate"
	PathServicesUninstall = "/v1/services/uninstall"
	PathServicesLogs = "/v1/services/logs"
)

type ServiceStatus struct {
	ID string `json:"id"`
	State string `json:"state"`
	Active bool `json:"active"`
	Image string `json:"image"`
	ContainerID string `json:"container_id,omitempty"`
	Health string `json:"health,omitempty"`
	LastError string `json:"last_error,omitempty"`
	Ports []ServicePortStatus `json:"ports,omitempty"`
}

type ServicePortStatus struct { Host, Container int; Protocol string; Loopback bool }
type ServiceActionRequest struct { ID string `json:"id"`; DeleteData bool `json:"delete_data,omitempty"` }
type ServiceLogsRequest struct { ID string `json:"id"`; Tail int `json:"tail,omitempty"` }
type ServiceLogsResponse struct { Logs string `json:"logs"` }
```

- Produces helper `Config` field `ServiceRoot string` and `Service` hooks:
  - `ServiceStatus func(context.Context) ([]helperproto.ServiceStatus, error)`
  - `ServiceInstall func(context.Context, string) error`
  - `ServiceActivate func(context.Context, string) error`
  - `ServiceDeactivate func(context.Context, string) error`
  - `ServiceUninstall func(context.Context, string, bool) error`
  - `ServiceLogs func(context.Context, string, int) (string, error)`
- Produces the controller boundary:

```go
type ServiceController interface {
	Status(ctx context.Context) ([]helperproto.ServiceStatus, error)
	Install(ctx context.Context, id services.ID) error
	Activate(ctx context.Context, id services.ID) error
	Deactivate(ctx context.Context, id services.ID) error
	Uninstall(ctx context.Context, id services.ID, deleteData bool) error
	Logs(ctx context.Context, id services.ID, tail int) (string, error)
}

func NewDockerServiceController(
	root string,
	catalog services.Catalog,
	runner CommandRunner,
) *DockerServiceController
```

  The Docker controller writes `<root>/services/compose.yaml`, computes the project name
  from the root hash, and runs only these fixed commands:

  ```text
  docker compose -p devstack-<hash> -f <root>/services/compose.yaml pull <id>
  docker compose -p devstack-<hash> -f <root>/services/compose.yaml up -d --no-deps <id>
  docker compose -p devstack-<hash> -f <root>/services/compose.yaml stop <id>
  docker compose -p devstack-<hash> -f <root>/services/compose.yaml rm -f <id>
  docker compose -p devstack-<hash> -f <root>/services/compose.yaml ps --format json
  docker compose -p devstack-<hash> -f <root>/services/compose.yaml logs --tail <n> <id>
  ```

  Validate `id` against the injected catalog before constructing any argv. Use argument
  vectors, never a shell.
- Produces helperclient methods with the same names.

- [ ] **Step 1: Write failing compose test**

Fixture catalog with one `nginx`-shaped service (fixture image is a valid-format digest
string supplied by the test, not a production claim). Assert:

```go
data, err := services.RenderCompose(cat, "/data/devstack")
// contains "container_name: devstack-nginx"
// contains "127.0.0.1:8080:80"
// contains "/data/devstack/services/nginx/config:/etc/nginx/conf.d:ro"
// does not contain "/var/run/docker.sock"
```

Also test escaping mount rejection.

- [ ] **Step 2: Write failing helper RPC tests**

Use a fake service hook set. Assert:

- `Status` returns the fake status;
- install/activate/deactivate/uninstall pass only a catalog-valid ID;
- unknown IDs return `INVALID_REQUEST`;
- logs tail is passed through;
- no arbitrary command string is accepted.

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/services ./internal/helper ./internal/helperclient -run 'TestRenderCompose|TestService' -count=1`

Expected: undefined types/methods.

- [ ] **Step 4: Implement**

- Render YAML deterministically. Use a root hash (`sha256` of absolute root, first 12
  hex chars) in the Compose project name; do not accept a user project name.
- Implement fixed `docker compose` command construction with argument arrays, not a shell.
- Add helper routes and client methods.
- Do not wire a production catalog yet; use empty catalog.
- Do not add install/activate API routes yet; F6 adds read-only listing.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/services ./internal/helper ./internal/helperclient -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/services/compose.go internal/services/compose_test.go \
  internal/helperproto/services.go internal/helper/services.go \
  internal/helper/service_docker.go internal/helper/server.go \
  internal/helperclient/client.go internal/helper/services_test.go
git commit -m "feat: add fixed compose service control plane"
```

---

### Task F4: Config manifest and conflict engine

**Parallel group:** 0A · **Depends on:** none · **Branch:** `feat/services-foundation-f4`

**Files:**
- Create: `internal/serviceconfig/manifest.go`
- Create: `internal/serviceconfig/manager.go`
- Test: `internal/serviceconfig/manager_test.go`
- Create: `internal/serviceconfig/diff.go`
- Test: `internal/serviceconfig/diff_test.go`

**Interfaces:**
- Produces:

```go
type Class string
const (
	ClassBase Class = "base"
	ClassGenerated Class = "generated"
	ClassCustom Class = "custom"
)

type Status struct {
	Path string
	Class Class
	Generated bool
	Modified bool
	Detached bool
	Hash string
}

type Conflict struct {
	Path string
	Current []byte
	Proposed []byte
	Diff string
	Reason string
}

type WriteResult struct {
	Changed bool
	Conflict *Conflict
}

type Manager struct { /* root, manifest path, clock */ }

func New(root string) (*Manager, error)
func (m *Manager) Status(rel string) (Status, error)
func (m *Manager) SeedBase(rel string, data []byte, mode os.FileMode) (bool, error)
func (m *Manager) WriteGenerated(rel string, data []byte, mode os.FileMode, reason string) (WriteResult, error)
func (m *Manager) KeepMine(rel string) error
func (m *Manager) UseGenerated(rel string, proposed []byte) (string, error)
func (m *Manager) Merge(rel string, data []byte) error
func (m *Manager) Backup(rel string) (string, error)
```

- Produces manifest JSON under `<root>/.config-manifest.json` with version 1 and entries.

- [ ] **Step 1: Write failing tests**

Test cases:

1. `SeedBase` writes a missing file and records class base/hash.
2. `SeedBase` does not overwrite an existing user file.
3. `WriteGenerated` replaces an unmodified generated file.
4. Direct user modification is detected by hash; `WriteGenerated` returns a conflict,
   leaves current bytes untouched, and includes a non-empty unified diff.
5. `KeepMine` marks the file detached; subsequent generated writes do not modify it.
6. `UseGenerated` creates a timestamped backup and replaces only after backup success.
7. Relative path traversal (`../x`, absolute paths) is rejected.
8. Writes are atomic (temp file in the same directory + rename) and preserve mode.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/serviceconfig -count=1`

Expected: package does not exist.

- [ ] **Step 3: Implement**

- Store paths as slash-relative paths; clean and contain them under root.
- Use SHA-256 for content hashes.
- Use `os.OpenRoot` or canonical path checks to prevent escapes.
- Backups go under `<root>/backups/config/<UTC timestamp>/...`, with collision-safe
  suffixes.
- Keep the manifest mode `0600`.
- Diff may use a simple line-based unified diff helper in `diff.go`; do not add an
  external dependency.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/serviceconfig -race -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/serviceconfig
git commit -m "feat: add editable config conflict engine"
```

---

### Task F5: Freeze site config contracts

**Parallel group:** 0B · **Depends on:** F2 · **Branch:** `feat/services-foundation-f5`

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/model/model_test.go`
- Create: `internal/store/migrations/0004_site_contracts.sql`
- Modify: `internal/store/store.go`
- Test: `internal/store/site_contracts_test.go` (create)
- Modify: `internal/sites/service.go`
- Modify: `internal/sites/service_test.go`
- Modify: `internal/api/handlers_sites.go`
- Modify: `internal/api/handlers_sites_test.go`
- Modify: `ui/src/lib/types.ts`

**Interfaces:**
- Produces `model.BuildProfile`:

```go
type BuildProfile struct {
	NodeVersion string            `json:"node_version,omitempty"`
	Command     string            `json:"command,omitempty"`
	Output      string            `json:"output,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}
```

- Adds to `model.Site`:
  - `FrontControllerFile string` JSON `front_controller_file`
  - `FrontControllerMode string` JSON `front_controller_mode`
  - `ApacheAllowOverride string` JSON `apache_allow_override`
  - `SPAFallbackFile string` JSON `spa_fallback_file`
  - `Build *BuildProfile` JSON `build,omitempty`
- Adds matching fields to `sites.CreateRequest`, `sites.UpdateRequest`, API request/response
  structs, and UI `Site`/`CreateSiteRequest`/`UpdateSiteRequest`.
- Defaults:
  - `front_controller_file = "index.php"`
  - `front_controller_mode = "auto"`
  - `apache_allow_override = "all"`
  - `spa_fallback_file = "index.html"`
  - `build = nil`
- Validation:
  - front-controller file: relative path, `.php`, no `..`, no dot segments;
  - modes: `auto`, `generated`, `htaccess`;
  - override: `all`, `fileinfo`, `none`;
  - SPA file: relative, no `..`, not `.php`;
  - build output: relative, no `..`, inside project;
  - build command/Node version required together when build is present.

- [ ] **Step 1: Write failing model/store tests**

- Create a site with non-default values; `CreateSite` then `GetSite` must round-trip all
  fields.
- `GetSiteByName`/`ListSites` must preserve build JSON.
- Default creation uses the documented defaults.
- Invalid traversal/mode/file values fail in `sites.Create`/`sites.Update`.
- API create/update JSON accepts and returns the fields.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/model ./internal/store ./internal/sites ./internal/api -run 'Contract|BuildProfile|FrontController' -count=1`

Expected: missing fields/columns.

- [ ] **Step 3: Implement**

Migration `0004_site_contracts.sql`:

```sql
ALTER TABLE sites ADD COLUMN front_controller_file TEXT NOT NULL DEFAULT 'index.php';
ALTER TABLE sites ADD COLUMN front_controller_mode TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE sites ADD COLUMN apache_allow_override TEXT NOT NULL DEFAULT 'all';
ALTER TABLE sites ADD COLUMN spa_fallback_file TEXT NOT NULL DEFAULT 'index.html';
ALTER TABLE sites ADD COLUMN build_json TEXT NOT NULL DEFAULT '{}';
```

Update `siteColumns`, insert/update/scan helpers, defaults, validation, API structs, and
UI types. Do not add build execution or vhost rendering here; only the contract and
validation.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/model ./internal/store ./internal/sites ./internal/api -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model internal/store internal/sites internal/api ui/src/lib/types.ts
git commit -m "feat: freeze site front-controller and build contracts"
```

---

### Task F6: Read-only service API/CLI/UI skeleton

**Parallel group:** 0C · **Depends on:** F1, F2, F5 · **Branch:** `feat/services-foundation-f6`

**Files:**
- Modify: `internal/api/server.go`
- Create: `internal/api/handlers_services.go`
- Create: `internal/api/handlers_services_test.go`
- Modify: `internal/client/client.go`
- Create or modify: `internal/cli/service.go`
- Test: `internal/cli/cli_test.go`
- Modify: `internal/cli/root.go`
- Modify: `ui/src/lib/types.ts`
- Modify: `ui/src/lib/api.ts`
- Modify: `ui/src/lib/mock.ts`
- Create: `ui/src/routes/Services.svelte`
- Create: `ui/src/routes/Services.test.ts`
- Modify: `ui/src/App.svelte`
- Modify: `ui/src/lib/components/Sidebar.svelte`

**Interfaces:**
- Produces API:
  - `GET /api/v1/services` → `{services: []services.View}`
  - `GET /api/v1/services/{id}` → `services.View`
  - The view combines catalog metadata and stored state; unknown catalog ID is 404.
- Produces server interface:

```go
type ServiceService interface {
	List(ctx context.Context) ([]services.View, error)
	Get(ctx context.Context, id services.ID) (services.View, error)
}
```

  The daemon wires it from the catalog and store. `api.NewServer` accepts it through
  `Config.Services` so Wave 1 does not edit the constructor signature again.
- CLI:
  - `devstack service list`
  - `devstack service status <id>`
  - `devstack service` root command registered.
  - Client interface methods:
    - `ListServices(ctx context.Context) ([]services.View, error)`
    - `GetService(ctx context.Context, id string) (services.View, error)`
- UI:
  - `ServiceState` and `ServiceView` types mirroring `services.View`.
  - `api.listServices()` / `api.getService(id)`.
  - `/services` route and sidebar item.
  - Empty state is valid until Wave 1 adds catalog entries.
  - Read-only in this task; install/activate buttons are disabled with a clear “not
    available in this build” state or omitted until Wave 1.

- [ ] **Step 1: Write failing API test**

- Catalog fixture with one service and one stored state; list returns it.
- Unknown service GET returns 404.
- Empty catalog returns an empty list.

- [ ] **Step 2: Write failing CLI test**

Fake daemon client returns one service; `devstack service list` prints NAME/GROUP/STATE/
ACTIVE/PORT headings; `--json` emits the array; unknown ID is an error.

- [ ] **Step 3: Write failing UI test**

Mock API returns a service; Services page renders the name/group/state; empty state renders
without errors.

- [ ] **Step 4: Run to verify failure**

Run: `go test ./internal/api ./internal/cli -run 'Service' -count=1`
Run: `cd ui && npm test -- --run src/routes/Services.test.ts`

Expected: missing routes/types.

- [ ] **Step 5: Implement API/CLI/UI skeleton**

Add the read-only routes and client methods. Register the CLI root command. Add the UI route
and sidebar link. Reuse existing session/CSRF behavior. Do not add install/activate calls
until Wave 1.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/api ./internal/cli -count=1
cd ui && npm run check && npm test -- --run src/routes/Services.test.ts && npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/api internal/client internal/cli ui
git commit -m "feat: add read-only service dashboard skeleton"
```

---

## Wave 0 integration checkpoint

After F1–F6 are merged into `integration/services-foundation`:

- [ ] `go vet ./...`
- [ ] `go test ./... -count=1`
- [ ] `go test ./... -race -count=1`
- [ ] `(cd ui && npm run check && npm test -- --run && npm run build)`
- [ ] `make build`
- [ ] `bash -n test/acceptance/m1.sh test/release/*.sh install.sh`
- [ ] `shellcheck --severity=error test/acceptance/m1.sh test/release/*.sh install.sh`
- [ ] verify no service/site data, `.worktrees`, certificates, or runtime archives
  are staged
- [ ] merge the foundation branch to `master`, push, and record the SHA in
  `docs/handoff.md`
- [ ] run CI green

## Handoff to Wave 1

Wave 1 plans are written after the foundation merge. They must consume, not re-define:

- `config.Paths` service layout
- `services.ID`, `Group`, `State`, `Service`, `Catalog`, `Record`
- helper `Service*` RPC/client methods
- `serviceconfig.Manager` conflict API
- site contract fields and build profile
- service API/CLI/UI extension points

Do not edit foundation-owned hot files directly in Wave 1 unless the task plan says so or
the integration owner coordinates it.
