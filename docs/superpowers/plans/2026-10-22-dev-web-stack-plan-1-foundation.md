# dev-web-stack M1 — Plan 1: Foundation, Static/Node Sites, Trusted TLS, and External Vhosts

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a working Linux slice of dev-web-stack: install the privileged helper, initialize the local CA and hosts block, run the daemon and dashboard, serve static and Node sites at trusted `https://<name>.test` URLs, and optionally serve them through helper-managed native nginx or Apache vhosts.

**Architecture:** One Go module builds `devstack` (CLI + daemon) and `devstack-helper` (root service). The unprivileged daemon owns SQLite state, the reverse proxy, the supervisor, the dashboard, and site orchestration. The helper owns hosts edits, CA key custody/signing, trust-store installation, fixed 80/443 forwarders, and validated native vhost generation/installation. Sites are served by supervised backend processes on loopback ports in builtin mode; in external mode nginx/Apache own 80/443, static sites are served from their roots, and Node traffic is proxied to its loopback backend.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, `spf13/cobra`, stdlib `net/http`/`crypto/tls`/`crypto/x509`, Svelte 5 + TypeScript + Vite, systemd, nginx/Apache (optional external serving), Linux x86_64.

**Specs:** `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md` (Plan 1 implements §§5–9, 10.1, 10.3, 12–15, and 17–19 for static/Node on Linux, including install/uninstall but not upgrade; PHP is Plan 2, Windows/WSL is Plan 3, packaging is Plan 4.) and `docs/superpowers/specs/2026-10-22-external-vhosts-design.md` (helper-managed nginx/Apache vhosts; static/Node in Plan 1, PHP in Plan 2).

## Global Constraints

- Go 1.26+; module path `github.com/AramZahedi/dev-web-stack`.
- Target: systemd-based Linux x86_64, glibc 2.35+, one installing user per machine.
- No Docker/Podman, no nginx/Apache integration.
- Local domains are `<name>.test`; `devstack.test` is reserved for the dashboard.
- Proxy listens on `127.0.0.1:8080` (HTTP) and `127.0.0.1:8443` (HTTPS); UI on `127.0.0.1:7070`; helper forwards 80/443 to the proxy.
- Backend ports: `41000–42999` for HTTP upstreams, `43000–43999` for FastCGI (FastCGI is used in Plan 2; reserve it now).
- State root: `$DEVSTACK_HOME`, default `~/.devstack`. Helper state: `/var/lib/devstack-helper`; helper socket: `/run/devstack-helper/helper.sock`.
- SQLite driver is `modernc.org/sqlite` (pure Go). No cgo.
- CA: ECDSA P-256 root, 10 years, key `0600`, owned by the helper; leaf certs 90 days, renewed at 30 days remaining; SANs restricted to `.test` names and `devstack.test`; no wildcards.
- Helper RPC accepts no commands, file paths, arbitrary certs, or arbitrary ports. Peer UID/SID checked on every call.
- Hosts block markers: `# BEGIN devstack` and `# END devstack`; only `127.0.0.1 <domain>` lines inside; unrelated content preserved; atomic replace.
- Logs: rotate at 10 MB, keep 5 files, 5,000-line in-memory ring per site.
- Supervisor: readiness 30s (Node 60s), stop grace 10s, backoff `1s, 2s, 4s, 8s, 16s, 30s`, crash after 5 failures in 5 minutes.
- Serving mode: `serving_mode: builtin | nginx | apache`, default `builtin`; exactly one active. External mode releases the 80/443 forwarders; the built-in proxy listeners do not start.
- External files: `/etc/devstack/vhosts/<server>/current/*.conf`, `/etc/devstack/tls/<owner>.{crt,key}` (key `0600` root, cert `0644`), one bridge include in the server's standard include directory; helper state in `/var/lib/devstack-helper/vhosts.json` (`0600`).
- The helper renders vhosts only from validated site specs; no arbitrary config text, paths, commands, or ports. Every sync runs the fixed config test and reload, and rolls back on failure.
- External static sites have no `serve-static` process; the vhost is the serving mechanism and a stopped site serves a branded 503. Node keeps its supervised backend. PHP vhosts and FastCGI templates are Plan 2.
- Every task ends with a green test run and a commit. No TODO/TBD placeholders.

## Review Focus

1. **Foreign `Host`, `Origin`, and CSRF/DNS rebinding:** a request with `Host: evil.test` or a cross-origin mutation must never reach an authenticated API action, and the proxy must only serve registered routes.
2. **Hosts file safety:** unrelated lines, comments, and permissions survive sync/remove; a crash mid-write must not truncate the file.
3. **Process adoption and orphans:** daemon restart with running sites must not duplicate processes or leak process groups; stopped sites stay stopped.
4. **Certificate integrity:** wrong SANs, expired certs, mismatched keys, or permissive key modes must be rejected and reported; renewal must not brick a serving site.
5. **Filesystem boundaries:** static requests, the fs browser, and archive extraction must not read outside the intended root via `..` or symlinks.
6. **Vhost injection and containment:** helper-rendered nginx/Apache configs must escape every interpolated value, reject traversal/symlink roots and out-of-range ports, and never accept arbitrary config text or commands.
7. **Mode-switch safety:** a failed nginx/Apache config test or reload must restore the previous release and serving mode; 80/443 must never be left unowned, and builtin↔external switches must be reversible.
8. **Vhost TLS custody:** site keys under `/etc/devstack/tls` are root-only, certificates match the site SANs, and renewal never bricks a serving vhost.

---

## Task 1: Module scaffold, version command, CI skeleton

**Files:**
- Create: `go.mod`, `.gitignore`, `.golangci.yml`, `Makefile`
- Create: `cmd/devstack/main.go`, `cmd/devstack-helper/main.go`
- Create: `internal/version/version.go`, `internal/version/version_test.go`
- Create: `.github/workflows/ci.yml`
- Create: `README.md`

**Interfaces:**
- Produces: `version.Version string`, `version.Commit string`, `version.String() string`.
- Produces: `devstack version` printing `version.String()`.

- [ ] **Step 1: Write the failing test**

Run `go mod init github.com/AramZahedi/dev-web-stack` first, then write:

```go
func TestString(t *testing.T) {
    oldV, oldC := Version, Commit
    t.Cleanup(func() { Version, Commit = oldV, oldC })
    Version, Commit = "1.2.3", "abc123"
    if got := String(); got != "1.2.3 (abc123)" {
        t.Fatalf("String() = %q", got)
    }
    Commit = ""
    if got := String(); got != "1.2.3" {
        t.Fatalf("String() = %q", got)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/version`
Expected: FAIL (undefined: Version).

- [ ] **Step 3: Implement scaffold**

Initialize the module and packages, add ldflags-injectable version vars, a Cobra root command with `version`, a helper `main` that prints a not-implemented error, a Makefile (`build`, `test`, `lint`, `ui`), and a CI workflow that runs `go test ./...` and builds. `.gitignore` excludes `dist/`, `ui/dist/*` (except `ui/dist/.gitkeep`), `*.db`, and `.idea/`.

- [ ] **Step 4: Run tests and build**

Run: `go test ./... && go build ./...`
Expected: PASS and successful build.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum .gitignore .golangci.yml Makefile cmd internal .github README.md
git commit -m "chore: scaffold dev-web-stack module"
```

---

## Task 2: Machine config, paths, and shared model

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`
- Create: `internal/config/paths.go`
- Create: `internal/model/model.go`

**Interfaces:**
- Produces:

```go
type Config struct {
    Home string `yaml:"-"`; HTTPPort, HTTPSPort, UIPort int
    WSLDistro string
    AutoStartSites bool
    GracefulStopSeconds, MaxRequestBodyMB, LogMaxMB, LogKeep int
}
func Default() Config
func Load(home string) (Config, error)
func (c Config) Validate() error
func (c Config) Save() error
type Paths struct { Home, Config, DB, PKI, Runtimes, Sites, Logs, Run, Cache string }
func NewPaths(home string) Paths
func (p Paths) Ensure() error
```

- Produces model types: `SiteKind` (`static`, `php`, `node`), `DesiredState` (`running`, `stopped`), `SiteStatus` (`stopped`, `starting`, `running`, `stopping`, `crashed`, `error`), `Site`, `RuntimeBuild`, `CertRecord`, `Event` exactly as named in the spec (§6–7). Constants: `SiteStatic`, `SitePHP`, `SiteNode`; `DesiredRunning`, `DesiredStopped`; `StatusStopped`, `StatusStarting`, `StatusRunning`, `StatusStopping`, `StatusCrashed`, `StatusError`. `Site.Env` is `map[string]string`.

- [ ] **Step 1: Write failing tests**

```go
func TestLoadCreatesDefaults(t *testing.T) {
    home := t.TempDir()
    cfg, err := Load(home)
    if err != nil { t.Fatal(err) }
    if cfg.HTTPPort != 8080 || cfg.HTTPSPort != 8443 || cfg.UIPort != 7070 { t.Fatalf("ports = %+v", cfg) }
    if _, err := os.Stat(filepath.Join(home, "config.yaml")); err != nil { t.Fatal(err) }
}
func TestValidateRejectsDuplicatePorts(t *testing.T) {
    cfg := Default(); cfg.HTTPSPort = cfg.HTTPPort
    if err := cfg.Validate(); err == nil { t.Fatal("want error") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config`
Expected: FAIL (undefined: Load).

- [ ] **Step 3: Implement**

Load YAML with defaults, `DEVSTACK_HOME` override, create the home and subdirectories on `Load`/`Paths.Ensure`, validate all ports in `1–65535`, distinct, outside `41000–43999`, and `GracefulStopSeconds > 0`. `Save` writes atomically (`tmp` + rename, `0600`). Define the model structs with JSON tags; `Site.Env` marshals to JSON in the store.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config ./internal/model`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/model
git commit -m "feat: add machine config, paths, and core model"
```

---

## Task 3: Domain validation and port allocation

**Files:**
- Create: `internal/domain/domain.go`, `internal/domain/domain_test.go`
- Create: `internal/domain/ports.go`, `internal/domain/ports_test.go`

**Interfaces:**
- Produces:

```go
func NormalizeName(s string) (string, error)
func DomainFor(name string) string
func ValidateDomain(d string) error          // accepts any valid *.test, including devstack.test
func ValidateSiteDomain(d string) error      // ValidateDomain + rejects devstack.test
func ValidateProjectPath(p string) (string, error)
type PortKind int
const ( PortHTTP PortKind = iota; PortFastCGI )
func NewPortAllocator(reserved ...int) *PortAllocator
func (p *PortAllocator) Allocate(kind PortKind) (int, error)
func (p *PortAllocator) Reserve(port int) error
func (p *PortAllocator) Release(port int)
```

- [ ] **Step 1: Write failing tests**

```go
func TestValidateDomain(t *testing.T) {
    good := []string{"blog.test", "my-shop.test", "www.blog.test", "devstack.test"}
    for _, d := range good { if err := ValidateDomain(d); err != nil { t.Errorf("%s: %v", d, err) } }
    bad := []string{"blog.dev", "Blog.test", "blog..test", "-bad.test", "a_b.test"}
    for _, d := range bad { if err := ValidateDomain(d); err == nil { t.Errorf("%s: want error", d) } }
    if err := ValidateSiteDomain("devstack.test"); err == nil { t.Fatal("devstack.test must be reserved for sites") }
}
func TestAllocateRangesAndCollisions(t *testing.T) {
    a := NewPortAllocator()
    p1, _ := a.Allocate(PortHTTP)
    p2, _ := a.Allocate(PortHTTP)
    if p1 < 41000 || p1 > 42999 || p1 == p2 { t.Fatalf("ports %d %d", p1, p2) }
    f, _ := a.Allocate(PortFastCGI)
    if f < 43000 || f > 43999 { t.Fatalf("fastcgi %d", f) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/domain`
Expected: FAIL (undefined: ValidateDomain).

- [ ] **Step 3: Implement**

Rules from spec §7: lowercase labels, 1–63 chars, alnum/hyphen, no leading/trailing hyphen, total ≤253, must end in `.test`, reject `devstack.test`, reject non-ASCII. `NormalizeName` lowercases and rejects path separators/whitespace. `DomainFor` returns name + `.test`. `ValidateProjectPath` requires an absolute existing directory readable by the daemon and resolves symlinks for validation. Port allocator checks `net.Listen("tcp", "127.0.0.1:<port>")` before reserving, skips reserved ports, and is safe for concurrent use.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/domain`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain
git commit -m "feat: add domain validation and backend port allocator"
```

---

## Task 4: SQLite store and migrations

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrate.go`, `internal/store/store_test.go`
- Create: `internal/store/migrations/0001_init.sql` (embedded)

**Interfaces:**
- Produces:

```go
type Store struct { /* ... */ }
func Open(path string) (*Store, error)
func (s *Store) Close() error
func (s *Store) CreateSite(ctx context.Context, site model.Site) error
func (s *Store) UpdateSite(ctx context.Context, site model.Site) error
func (s *Store) GetSite(ctx context.Context, id string) (model.Site, error)
func (s *Store) GetSiteByName(ctx context.Context, name string) (model.Site, error)
func (s *Store) GetSiteByDomain(ctx context.Context, domain string) (model.Site, error)
func (s *Store) ListSites(ctx context.Context) ([]model.Site, error)
func (s *Store) DeleteSite(ctx context.Context, id string) error
func (s *Store) UpsertRuntime(ctx context.Context, b model.RuntimeBuild) error
func (s *Store) GetRuntime(ctx context.Context, buildID string) (model.RuntimeBuild, error)
func (s *Store) ListRuntimes(ctx context.Context) ([]model.RuntimeBuild, error)
func (s *Store) DeleteRuntime(ctx context.Context, buildID string) error
func (s *Store) SaveCert(ctx context.Context, c model.CertRecord) error
func (s *Store) GetCertBySite(ctx context.Context, siteID string) (model.CertRecord, error)
func (s *Store) ListCerts(ctx context.Context) ([]model.CertRecord, error)
func (s *Store) DeleteCertBySite(ctx context.Context, siteID string) error
func (s *Store) SetSetting(ctx context.Context, key, value string) error
func (s *Store) GetSetting(ctx context.Context, key string) (string, error)
func (s *Store) AddEvent(ctx context.Context, e model.Event) error
func (s *Store) ListEvents(ctx context.Context, limit int) ([]model.Event, error)
var ErrNotFound = errors.New("not found")
```

- [ ] **Step 1: Write failing tests**

```go
func TestSiteRoundTripAndUniqueDomain(t *testing.T) {
    s := openTemp(t)
    site := model.Site{ID: "s1", Name: "blog", Domain: "blog.test", Kind: model.SiteStatic, Env: map[string]string{"A":"B"}}
    if err := s.CreateSite(ctx, site); err != nil { t.Fatal(err) }
    got, err := s.GetSiteByDomain(ctx, "blog.test")
    if err != nil || got.Env["A"] != "B" { t.Fatalf("%+v %v", got, err) }
    site.ID, site.Name = "s2", "other"
    if err := s.CreateSite(ctx, site); err == nil { t.Fatal("want unique domain error") }
}
func TestMigrationsIdempotent(t *testing.T) {
    dir := t.TempDir(); p := filepath.Join(dir, "state.db")
    s1, _ := Open(p); s1.Close()
    s2, err := Open(p); if err != nil { t.Fatal(err) }; s2.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store`
Expected: FAIL (undefined: Open).

- [ ] **Step 3: Implement**

Open with `modernc.org/sqlite`, `_pragma=busy_timeout(5000)`, `_pragma=foreign_keys(1)`, and WAL. Embed migrations with `//go:embed` and track `schema_migrations`. Create tables exactly as spec §6, including `php_defaults` and `site_php_config` for Plan 2. Store `sites.env_json`, `certs.domains_json`; timestamps as Unix seconds. `DeleteSite` also deletes its cert row in one transaction. Return `ErrNotFound` for `sql.ErrNoRows`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat: add SQLite store and schema migrations"
```

---

## Task 5: Log store, rotation, ring buffer, and SSE hub

**Files:**
- Create: `internal/logs/store.go`, `internal/logs/hub.go`, `internal/logs/redact.go`, `internal/logs/store_test.go`

**Interfaces:**
- Produces:

```go
type Line struct { Time time.Time; SiteID, Source, Text string }
type Store struct { /* ... */ }
func New(dir string, maxMB, keep int) (*Store, error)
func (s *Store) Writer(siteID, source string) io.Writer
func (s *Store) Tail(siteID string, n int) ([]Line, error)
func (s *Store) Subscribe(siteID string) (<-chan Line, func())
func (s *Store) Close() error
func Redact(text string) string
```

- [ ] **Step 1: Write failing tests**

```go
func TestRotateAndTail(t *testing.T) {
    s, _ := New(t.TempDir(), 0, 2) // 0 MB => rotate after each write in test hook
    w := s.Writer("site1", "app")
    io.WriteString(w, "one\n")
    io.WriteString(w, "two\n")
    lines, _ := s.Tail("site1", 2)
    if len(lines) != 2 || lines[1].Text != "two" { t.Fatalf("%+v", lines) }
}
func TestRedactSecrets(t *testing.T) {
    in := "APP_KEY=abc SECRET_TOKEN=xyz password=hunter2"
    out := Redact(in)
    for _, secret := range []string{"abc", "xyz", "hunter2"} {
        if strings.Contains(out, secret) { t.Fatalf("leaked %q in %q", secret, out) }
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/logs`
Expected: FAIL (undefined: New).

- [ ] **Step 3: Implement**

Per-site `logs/<id>/current.log`; rotate at `maxMB` (using a test-configurable byte threshold), keep `keep`, reopen atomically. Keep a 5,000-entry ring per site; `Tail` returns the last n. `Subscribe` fans out to buffered channels and unsubscribes cleanly. Redact keys matching `(?i)(key|token|secret|password)=...` and `Authorization: ...`. Writes are line-oriented; the writer splits on newlines and stamps time/source.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/logs`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/logs
git commit -m "feat: add rotating site logs and SSE hub"
```

---

## Task 6: Supervisor, process groups, backoff, and orphan cleanup

**Files:**
- Create: `internal/supervisor/supervisor.go`, `internal/supervisor/runner.go`, `internal/supervisor/cleanup.go`, `internal/supervisor/supervisor_test.go`

**Interfaces:**
- Produces:

```go
type ReadyCheck struct { TCPAddr string; Timeout time.Duration }
type RestartPolicy struct { MaxRestarts int; Window time.Duration; Backoff []time.Duration }
type ProcessSpec struct {
    ID string; Argv []string; Env map[string]string; Dir string
    Ready ReadyCheck; Restart RestartPolicy
    Stdout, Stderr io.Writer
}
type Status struct { State model.SiteStatus; PID int; StartedAt time.Time; Restarts int; LastError string }
type Manager struct { /* ... */ }
func NewManager(runDir string, logger *slog.Logger) *Manager
func (m *Manager) Start(ctx context.Context, spec ProcessSpec) (Status, error)
func (m *Manager) Stop(ctx context.Context, id string) error
func (m *Manager) Status(id string) (Status, bool)
func (m *Manager) CleanupOrphans() error
```

- [ ] **Step 1: Write failing tests**

```go
func TestStartStopsProcessGroup(t *testing.T) {
    m := NewManager(t.TempDir(), slog.Default())
    st, err := m.Start(ctx, ProcessSpec{ID: "x", Argv: []string{"sh", "-c", "sleep 30"}, Ready: ReadyCheck{Timeout: time.Second}})
    if err != nil { t.Fatal(err) }
    if err := m.Stop(ctx, "x"); err != nil { t.Fatal(err) }
    if syscall.Kill(-st.PID, 0) == nil { t.Fatal("process group still alive") }
}
func TestReadinessTimeoutKillsProcess(t *testing.T) {
    m := NewManager(t.TempDir(), slog.Default())
    _, err := m.Start(ctx, ProcessSpec{ID: "x", Argv: []string{"sleep", "30"}, Ready: ReadyCheck{TCPAddr: "127.0.0.1:1", Timeout: 50 * time.Millisecond}})
    if !errors.Is(err, ErrReadyTimeout) { t.Fatalf("err = %v", err) }
}
func TestCleanupOrphansKillsOnlyOurProcesses(t *testing.T) {
    dir := t.TempDir()
    m := NewManager(dir, slog.Default())
    st, err := m.Start(ctx, ProcessSpec{ID: "orphan", Argv: []string{"sleep", "30"}, Ready: ReadyCheck{Timeout: time.Second}})
    if err != nil { t.Fatal(err) }
    m2 := NewManager(dir, slog.Default())
    if err := m2.CleanupOrphans(); err != nil { t.Fatal(err) }
    if syscall.Kill(-st.PID, 0) == nil { t.Fatal("orphan survived cleanup") }
    unrelated := exec.Command("sleep", "30"); unrelated.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
    if err := unrelated.Start(); err != nil { t.Fatal(err) }
    defer syscall.Kill(-unrelated.Process.Pid, syscall.SIGKILL)
    if err := m2.CleanupOrphans(); err != nil { t.Fatal(err) }
    if err := syscall.Kill(-unrelated.Process.Pid, 0); err != nil { t.Fatal("unrelated process was killed") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/supervisor`
Expected: FAIL (undefined: NewManager).

- [ ] **Step 3: Implement**

Start with `Setpgid: true`, pipe stdout/stderr, and write `run/<id>.json` containing PID + `/proc/<pid>/stat` field 22 (start time). Wait in a goroutine; on unexpected exit apply backoff, restart until the cap within the window, then set `crashed`. Readiness polls TCP every 100ms until timeout; timeout kills the group and returns `ErrReadyTimeout`. Stop sends SIGTERM to `-PID`, waits the grace period (10s default; test-configurable), then SIGKILL. `CleanupOrphans` reads pid files, verifies start time, kills only matching process groups, and removes stale files. Expose `ErrReadyTimeout`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/supervisor`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/supervisor
git commit -m "feat: add process supervisor with backoff and orphan cleanup"
```

---

## Task 7: Backend interface and local backend

**Files:**
- Create: `internal/backend/backend.go`, `internal/backend/local.go`, `internal/backend/local_test.go`
- Create: `internal/backend/conformance/conformance.go`

**Interfaces:**
- Produces:

```go
type SiteSpec struct {
    ID, Kind, HostPath, BackendPath, DocumentRoot, Command string
    Env map[string]string
    HTTPPort, FastCGIPort int
    SPAFallback, FrontController bool
    RuntimeVersion string
}
type PreparedSite struct { BackendHost string; HTTPPort, FastCGIPort int }
type ProcessInfo struct { PID int; StartedAt time.Time }
type Status struct { State model.SiteStatus; PID int; Message string }
type Result struct { ExitCode int; Stdout, Stderr string }
type RuntimeProvider interface {
    Ensure(ctx context.Context, kind, version string) (model.RuntimeBuild, error)
    Remove(ctx context.Context, buildID string) error
}
type Backend interface {
    Prepare(ctx context.Context, spec SiteSpec) (PreparedSite, error)
    Start(ctx context.Context, id string) (ProcessInfo, error)
    Stop(ctx context.Context, id string, graceful bool) error
    Status(ctx context.Context, id string) (Status, error)
    Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error)
    Exec(ctx context.Context, id string, argv []string, env map[string]string) (Result, error)
    EnsureRuntime(ctx context.Context, kind, version string) (model.RuntimeBuild, error)
    RemoveRuntime(ctx context.Context, buildID string) error
    InstallCA(ctx context.Context, pem []byte) error
    Health(ctx context.Context) error
}
func NewLocal(cfg config.Config, stores *store.Store, logs *logs.Store, runtimes RuntimeProvider, selfBin string) (*Local, error)
```

`selfBin` is the current `devstack` executable; tests build `./cmd/devstack` into a temp path so the `serve-static` subprocess works. Production passes `os.Executable()`.

- [ ] **Step 1: Write failing tests**

```go
func TestLocalStaticSite(t *testing.T) {
    b, root := newTestLocal(t)
    rootFile(t, root, "index.html", "hello")
    spec := SiteSpec{ID: "s1", Kind: "static", HostPath: root, BackendPath: root, HTTPPort: freePort(t), SPAFallback: false}
    if _, err := b.Prepare(ctx, spec); err != nil { t.Fatal(err) }
    if _, err := b.Start(ctx, "s1"); err != nil { t.Fatal(err) }
    resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", spec.HTTPPort))
    if err != nil || readBody(resp) != "hello" { t.Fatalf("%v %q", err, readBody(resp)) }
    _ = b.Stop(ctx, "s1", true)
}
func TestLocalNodeCommandGetsPort(t *testing.T) {
    b, root := newTestLocal(t)
    spec := SiteSpec{ID: "n1", Kind: "node", HostPath: root, BackendPath: root, HTTPPort: freePort(t),
        Command: `python3 -m http.server "$PORT"`}
    if _, err := b.Prepare(ctx, spec); err != nil { t.Fatal(err) }
    if _, err := b.Start(ctx, "n1"); err != nil { t.Fatal(err) }
    defer b.Stop(ctx, "n1", true)
    resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", spec.HTTPPort))
    if err != nil || resp.StatusCode != 200 { t.Fatalf("%v %v", err, resp) }
}
```

Skip if `python3` is absent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backend`
Expected: FAIL (undefined: NewLocal).

- [ ] **Step 3: Implement**

`Local` holds a `supervisor.Manager`, a static-spec map, `store`, and `RuntimeProvider`. Static `Start` launches `selfBin` with `serve-static --root <docroot> --addr 127.0.0.1:<port> [--spa]`; Node launches `/bin/sh -lc <Command>` with env: `PATH=<node>/bin:$PATH`, `PORT=<port>`, `NODE_EXTRA_CA_CERTS=<home>/pki/ca-bundle.crt`, and site env. Readiness is TCP on `127.0.0.1:<port>`. `Stop` delegates to the supervisor. `Logs` returns a pipe fed by the logs store. `Exec` runs argv as the daemon user with the site env. `InstallCA` writes a combined system+devstack bundle to `pki/ca-bundle.crt` (`0600`). Add a `conformance.Suite` used by Plan 3's WSL backend later; it exercises Prepare/Start/Stop/Status/Logs/Exec/Health.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/backend`
Expected: PASS (Node test skips when `python3` is unavailable).

- [ ] **Step 5: Commit**

```bash
git add internal/backend
git commit -m "feat: add backend interface and local process backend"
```

---

## Task 8: Helper RPC transport and managed hosts block

**Files:**
- Create: `internal/helperproto/types.go`
- Create: `internal/helper/server.go`, `internal/helperclient/client.go`
- Create: `internal/helper/auth_linux.go`
- Create: `internal/hosts/hosts.go`, `internal/hosts/hosts_test.go`
- Create: `internal/trust/types.go`
- Create: `internal/helper/server_test.go`

**Interfaces:**
- Produces:

```go
const Version = "1"
type StatusResponse struct { Version, CAFingerprint string; HostsSynced bool }
type HostsSyncRequest struct { Domains []string }
type HostsSyncResponse struct { Changed bool }
type EnsureCAResponse struct { RootPEM []byte; Fingerprint string }
type SignRequest struct { CSR []byte; Domains []string }
type SignResponse struct { CertPEM []byte; Serial string; NotAfter time.Time }
type TrustRequest struct { UserHome string }
type TrustResponse struct { Targets []trust.Target }
type ForwardersRequest struct { HTTPPort, HTTPSPort int; Enable bool }
type UninstallRequest struct{}
type StatusRequest struct{}

type Client struct { /* ... */ }
func Dial(socketPath string) (*Client, error)
func (c *Client) Status(ctx context.Context) (StatusResponse, error)
func (c *Client) SyncHosts(ctx context.Context, domains []string) error
func (c *Client) EnsureCA(ctx context.Context) (EnsureCAResponse, error)
func (c *Client) RootPEM() []byte
func (c *Client) SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (SignResponse, error)
func (c *Client) InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error)
func (c *Client) RemoveTrust(ctx context.Context, userHome string) ([]trust.Target, error)
func (c *Client) TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error)
func (c *Client) SetForwarders(ctx context.Context, httpPort, httpsPort int, enable bool) error
func (c *Client) Uninstall(ctx context.Context) error
```

- Produces `internal/trust`: `type Target struct { Name string; OK bool; Detail string }`.

- Produces hosts functions:

```go
const BeginMarker = "# BEGIN devstack"; const EndMarker = "# END devstack"
func Render(existing string, domains []string) (string, error)
func Parse(content string) (domains []string, found bool)
func WriteAtomic(path string, data []byte, mode fs.FileMode) error
```

- [ ] **Step 1: Write failing tests**

```go
func TestRenderPreservesUnrelatedLines(t *testing.T) {
    existing := "127.0.0.1 localhost\n# keep me\n# BEGIN devstack\n127.0.0.1 old.test\n# END devstack\n10.0.0.1 intranet\n"
    got, err := Render(existing, []string{"blog.test", "www.blog.test"})
    if err != nil { t.Fatal(err) }
    for _, s := range []string{"# keep me", "127.0.0.1 localhost", "10.0.0.1 intranet", "127.0.0.1 blog.test", "127.0.0.1 www.blog.test"} {
        if !strings.Contains(got, s) { t.Fatalf("missing %q in %q", s, got) }
    }
    if strings.Contains(got, "old.test") { t.Fatal("old block not replaced") }
}
func TestRenderRejectsBadDomains(t *testing.T) {
    if _, err := Render("", []string{"evil.com"}); err == nil { t.Fatal("want error") }
}
func TestHelperRejectsWrongUID(t *testing.T) {
    // Start the server with AllowUID = os.Getuid()+1 and assert a request over the socket fails with ErrUnauthorized.
}
func TestWriteAtomicPreservesMode(t *testing.T) {
    p := filepath.Join(t.TempDir(), "hosts")
    os.WriteFile(p, []byte("old"), 0640)
    if err := WriteAtomic(p, []byte("new"), 0640); err != nil { t.Fatal(err) }
    info, _ := os.Stat(p)
    if info.Mode().Perm() != 0640 { t.Fatalf("mode = %v", info.Mode().Perm()) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hosts ./internal/helper`
Expected: FAIL (undefined: Render).

- [ ] **Step 3: Implement**

Unix-socket HTTP JSON server with `GET /v1/status`, `POST /v1/hosts/sync`, plus handlers for CA/trust/forwarders (implemented in Tasks 9–10; return `ErrNotImplemented` until then). Every request carries `X-Devstack-Protocol: 1`; mismatches return 426 with the supported version. Authenticate every request with `SO_PEERCRED` against `AllowUID`; unknown UIDs get 403 before reading the body. Socket `0600`, parent dir `0755`, socket owned by the allowed user. Hosts `Render` replaces only the marked block, sorts/dedupes domains, validates each with `domain.ValidateDomain` (so `devstack.test` is allowed), and always emits `127.0.0.1 <domain>`. `WriteAtomic` writes a temp file in the same directory, fsyncs, preserves the original mode (default `0644`), and renames.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/hosts ./internal/helper`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/helperproto internal/helper internal/hosts
git commit -m "feat: add helper RPC transport and managed hosts block"
```

---

## Task 9: Helper CA, certificate signing, and Linux trust installation

**Files:**
- Create: `internal/helper/ca.go`, `internal/helper/trust_linux.go`, `internal/helper/trust.go`, `internal/helper/ca_test.go`, `internal/helper/trust_test.go`
- Create: `internal/testcert/testcert.go`
- Modify: `internal/helper/server.go`

**Interfaces:**
- Produces:

```go
type Authority struct { /* ... */ }
func LoadOrCreateAuthority(dir string, now func() time.Time) (*Authority, error)
func (a *Authority) RootPEM() []byte
func (a *Authority) Fingerprint() string
func (a *Authority) KeyPath() string
func (a *Authority) SignCSR(csrPEM []byte, domains []string, ttl time.Duration) (certPEM []byte, serial string, notAfter time.Time, err error)

type Target struct { Name string; OK bool; Detail string }
type CommandRunner interface { Run(ctx context.Context, name string, args ...string) ([]byte, error) }
type TrustManager struct { /* ... */ }
func NewTrustManager(rootPEM []byte, userHome string, runner CommandRunner, anchorDir string) *TrustManager
func (m *TrustManager) Install(ctx context.Context) ([]Target, error)
func (m *TrustManager) Remove(ctx context.Context) ([]Target, error)
func (m *TrustManager) Status(ctx context.Context) ([]Target, error)
```

- [ ] **Step 1: Write failing tests**

```go
func TestCAIssuesVerifiableLeaf(t *testing.T) {
    a, _ := LoadOrCreateAuthority(t.TempDir(), time.Now)
    key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
    csr, _ := testcert.CreateCSR(key, []string{"blog.test", "www.blog.test"})
    pemBytes, serial, notAfter, err := a.SignCSR(csr, []string{"blog.test", "www.blog.test"}, 90*24*time.Hour)
    if err != nil || serial == "" || time.Until(notAfter) < 89*24*time.Hour { t.Fatalf("%v", err) }
    roots := x509.NewCertPool(); roots.AppendCertsFromPEM(a.RootPEM())
    leaf, _ := testcert.ParseCertificate(pemBytes)
    if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "blog.test", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil { t.Fatal(err) }
}
func TestCAKeyMode(t *testing.T) {
    a, _ := LoadOrCreateAuthority(t.TempDir(), time.Now)
    info, _ := os.Stat(a.KeyPath())
    if info.Mode().Perm() != 0600 { t.Fatalf("key mode = %v", info.Mode().Perm()) }
}
func TestSignerRejectsNonTestSAN(t *testing.T) {
    // CSR with SAN evil.com must return an error.
}
func TestTrustInstallWritesAnchorAndCallsUpdate(t *testing.T) {
    runner := &fakeRunner{}
    tm := NewTrustManager(rootPEM, home, runner, t.TempDir())
    targets, err := tm.Install(ctx)
    if err != nil || len(targets) == 0 { t.Fatal(err) }
    if _, err := os.Stat(filepath.Join(anchorDir, "devstack-root.crt")); err != nil { t.Fatal(err) }
    if !runner.called("update-ca-certificates") { t.Fatal("update command not called") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/helper`
Expected: FAIL (undefined: LoadOrCreateAuthority).

- [ ] **Step 3: Implement**

CA: generate ECDSA P-256 key, self-signed root with `CN=devstack local CA`, `IsCA`, `KeyUsageCertSign|CRLSign`, `MaxPathLenZero`, 10 years; write `root.key` `0600` and `root.crt`. `SignCSR` parses/verifies the CSR, requires exactly the requested DNS SANs (set equality), rejects wildcards/IPs/emails/URIs and any name not ending `.test` or equal to `devstack.test`, sets 90-day validity, random 128-bit serial, `ExtKeyUsageServerAuth`, and returns PEM. Use a small internal `certutil` test helper for CSR creation.

Trust: detect Fedora (`/etc/pki/ca-trust/source/anchors` + `update-ca-trust extract`) or Debian (`/usr/local/share/ca-certificates` + `update-ca-certificates`); write `devstack-root.crt` atomically; run with the `CommandRunner`. NSS: if `certutil` exists, create `~/.pki/nssdb` when missing and add/remove nickname `devstack local CA`; also parse `~/.mozilla/firefox/profiles.ini` and update each profile DB. `Status` re-checks the anchor file and fingerprint. Removal only deletes a cert matching our SHA-256 fingerprint. Return per-target results; a missing browser is `OK=true, Detail="not installed"`. The helper's `InstallTrust`/`RemoveTrust` handler rejects a `UserHome` that does not match the allowed UID's home directory. Wire `EnsureCA`, `SignCertificate`, `InstallTrust`, `RemoveTrust`, and `TrustStatus` handlers to the authority and trust manager.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/helper`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/helper
git commit -m "feat: add helper CA, signing, and Linux trust installation"
```

---

## Task 10: Privileged 80/443 forwarders

**Files:**
- Create: `internal/forwarder/forwarder.go`, `internal/forwarder/forwarder_test.go`
- Modify: `internal/helper/server.go`

**Interfaces:**
- Produces:

```go
type ListenerStatus struct { Name, Listen, Target string; OK bool; Error string }
type Set struct { /* ... */ }
func New(httpListen, httpsListen, httpTarget, httpsTarget string, logger *slog.Logger) *Set
func (s *Set) Start(ctx context.Context) error
func (s *Set) Stop(ctx context.Context) error
func (s *Set) Status() []ListenerStatus
```

- [ ] **Step 1: Write failing test**

```go
func TestForwarderPassesTrafficAndDetectsConflict(t *testing.T) {
    target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
    defer target.Close()
    lp := freePort(t)
    f := New(fmt.Sprintf("127.0.0.1:%d", lp), "", target.Listener.Addr().String(), "", slog.Default())
    if err := f.Start(ctx); err != nil { t.Fatal(err) }
    defer f.Stop(ctx)
    resp, _ := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", lp))
    if readBody(resp) != "ok" { t.Fatal("forward failed") }
    duplicate := New(fmt.Sprintf("127.0.0.1:%d", lp), "", target.Listener.Addr().String(), "", slog.Default())
    if err := duplicate.Start(ctx); err == nil { t.Fatal("want bind conflict") }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/forwarder`
Expected: FAIL (undefined: New).

- [ ] **Step 3: Implement**

Accept loops with TCP listeners; on accept, dial the target and copy bidirectionally, closing both on EOF. Track errors; stop closes listeners and waits for goroutines. `SetForwarders` RPC starts/stops the set with the configured source ports (production 80/443; tests high ports) and loopback target ports only, returning a clear `EADDRINUSE` error with the listen address. Implement the helper's `Uninstall` RPC as: stop forwarders, remove trust for the allowed user, sync an empty hosts block, and return.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/forwarder ./internal/helper`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/forwarder internal/helper
git commit -m "feat: add privileged TCP forwarders for ports 80 and 443"
```

---

## Task 11: Proxy router, static server, and request semantics

**Files:**
- Create: `internal/proxy/router.go`, `internal/proxy/server.go`, `internal/proxy/handler.go`, `internal/proxy/router_test.go`, `internal/proxy/server_test.go`
- Create: `internal/staticserve/handler.go`, `internal/staticserve/handler_test.go`
- Create: `cmd/devstack/serve_static.go`

**Interfaces:**
- Produces:

```go
type Kind string
const ( KindStatic Kind = "static"; KindNode Kind = "node" )
type Route struct {
    SiteID, Host string; Kind Kind; Upstream string
    ForceHTTPS, TLS bool
}
type Snapshot struct { Routes map[string]Route; Certs map[string]*tls.Certificate; DefaultCert *tls.Certificate }
func NewRouter() *Router
func (r *Router) Replace(s Snapshot)
func (r *Router) Lookup(host string) (Route, bool)
func (r *Router) Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
func (r *Router) Handler() http.Handler

type ServerConfig struct { HTTPAddr, HTTPSAddr string; Router *Router; MaxBodyBytes int64; Logger *slog.Logger }
func NewServer(cfg ServerConfig) *Server
func (s *Server) Start(ctx context.Context) error
func (s *Server) Stop(ctx context.Context) error

func NewStaticHandler(root string, spaFallback bool) (*StaticHandler, error)
```

- [ ] **Step 1: Write failing tests**

```go
func TestRouterHostMatchAndUnknownHost(t *testing.T) {
    r := NewRouter()
    r.Replace(Snapshot{Routes: map[string]Route{"blog.test": {SiteID: "1", Host: "blog.test", Kind: KindNode, Upstream: "127.0.0.1:41001"}}})
    if _, ok := r.Lookup("BLOG.TEST"); !ok { t.Fatal("case-insensitive lookup failed") }
    req := httptest.NewRequest("GET", "http://evil.test/", nil)
    rec := httptest.NewRecorder()
    r.Handler().ServeHTTP(rec, req)
    if rec.Code != http.StatusNotFound { t.Fatalf("code = %d", rec.Code) }
}
func TestStaticHandlerRejectsTraversalAndServesIndex(t *testing.T) {
    root := t.TempDir(); os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0644)
    h, _ := NewStaticHandler(root, false)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
    if rec.Body.String() != "ok" { t.Fatal("index not served") }
    for _, p := range []string{"/../secret", "/%2e%2e/secret", "/link-outside"} {
        rec = httptest.NewRecorder()
        h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
        if rec.Code == 200 { t.Fatalf("traversal %q served", p) }
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy ./internal/staticserve`
Expected: FAIL (undefined: NewRouter).

- [ ] **Step 3: Implement**

Router: lowercase host lookup (strip port), immutable snapshot, per-route reverse proxy with `X-Forwarded-For/Proto/Host`, 30s read-header/120s idle, body limit from config, unknown host branded 404, upstream failure branded 502 page naming the site and suggesting `devstack site logs`, `ForceHTTPS` 308, `TLS=false` HTTPS→HTTP 308, never emit HSTS. Enable HTTP/2 on the TLS listener. Node reverse proxy must pass WebSocket upgrades (use `httputil.ReverseProxy`; add a test with a test WebSocket server). Optional per-site access logging is off by default and writes through the logs store when enabled. Static handler: `os.OpenRoot` for symlink/traversal containment, `index.html` for directories, no listing, MIME, ranges via `http.ServeContent`, `Cache-Control: no-store`, optional SPA fallback to `index.html` for non-file paths, 404 for missing. Add `serve-static` subcommand using `--root`, `--addr`, `--spa`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/proxy ./internal/staticserve`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy internal/staticserve cmd/devstack
git commit -m "feat: add host-routing proxy and secure static server"
```

---

## Task 12: Certificate manager, SNI, renewal, and TLS semantics

**Files:**
- Create: `internal/certs/manager.go`, `internal/certs/manager_test.go`
- Modify: `internal/proxy/server.go`, `internal/proxy/router.go`

**Interfaces:**
- Produces:

```go
type Signer interface {
    SignCertificate(ctx context.Context, csrPEM []byte, domains []string) (helperproto.SignResponse, error)
    RootPEM() []byte
}
type Manager struct { /* ... */ }
func NewManager(stores *store.Store, signer Signer, dir string, now func() time.Time) *Manager
func (m *Manager) Ensure(ctx context.Context, owner string, domains []string) (model.CertRecord, error)
func (m *Manager) RenewDue(ctx context.Context) ([]model.CertRecord, error)
func (m *Manager) Load(rec model.CertRecord) (*tls.Certificate, error)
func (m *Manager) Delete(ctx context.Context, owner string) error
const DashboardOwner = "__dashboard__"
```

- [ ] **Step 1: Write failing tests**

```go
func TestEnsureWritesSecureKeyAndVerifiesChain(t *testing.T) {
    m, signer := newTestCertManager(t)
    rec, err := m.Ensure(ctx, "site1", []string{"blog.test", "www.blog.test"})
    if err != nil { t.Fatal(err) }
    info, _ := os.Stat(rec.KeyPath)
    if info.Mode().Perm() != 0600 { t.Fatalf("key mode = %v", info.Mode().Perm()) }
    cert, err := m.Load(rec)
    if err != nil { t.Fatal(err) }
    if len(cert.Certificate) < 2 { t.Fatal("chain missing root") }
    // Verify with signer.RootPEM() as the only root and DNSName blog.test.
}
func TestRenewDueRefreshesExpiringCert(t *testing.T) {
    // Issue with now, advance now by 61 days, RenewDue must return the cert and a later NotAfter.
}
func TestLoadRejectsExpiredWrongSANAndLooseKey(t *testing.T) {
    m, _ := newTestCertManager(t)
    expired := writeTestCert(t, m, "expired", []string{"blog.test"}, time.Now().Add(-time.Hour))
    if _, err := m.Load(expired); err == nil { t.Fatal("expired cert accepted") }
    good, err := m.Ensure(ctx, "good", []string{"blog.test"})
    if err != nil { t.Fatal(err) }
    wrong := writeTestCert(t, m, "wrong", []string{"other.test"}, time.Now().Add(90*24*time.Hour))
    if _, err := m.Load(wrong); err == nil { t.Fatal("wrong SAN accepted") }
    if err := os.Chmod(good.KeyPath, 0644); err != nil { t.Fatal(err) }
    if _, err := m.Load(good); err == nil { t.Fatal("loose key mode accepted") }
}
```

Define test-only helper `writeTestCert` (writes an expired or mismatched leaf via the test signer) in `manager_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/certs`
Expected: FAIL (undefined: NewManager).

- [ ] **Step 3: Implement**

Generate an ECDSA P-256 key and CSR with the requested DNS SANs; sign via `Signer`; reject responses with wrong SANs or validity; write `<owner>.key` `0600`, `<owner>.crt` `0644` (leaf+root chain), persist `CertRecord`. `Load` re-parses and verifies the chain against `Signer.RootPEM()`. `RenewDue` selects certs with `NotAfter < now+30d`, reissues, and keeps the old cert on failure. Proxy `Router.Certificate` selects by SNI host, falls back to `DefaultCert`; `Snapshot` includes the dashboard cert. Add tests that a key with non-`0600` modes, an expired cert, and a cert whose SAN does not match are rejected by `Load`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/certs ./internal/proxy`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/certs internal/proxy
git commit -m "feat: add per-site certificate manager and SNI"
```

---

## Task 13: Runtime catalog, downloader, and Node versions

**Files:**
- Create: `internal/catalog/catalog.go`, `internal/catalog/catalog.json`, `internal/catalog/catalog_test.go`
- Create: `internal/runtime/manager.go`, `internal/runtime/download.go`, `internal/runtime/manager_test.go`
- Create: `internal/runtime/testdata/` fixture tarballs

**Interfaces:**
- Produces:

```go
type Entry struct { ID, Kind, Version, Build, URL, SHA256 string; Size int64 }
type Catalog struct { Entries []Entry }
func Load() (Catalog, error)
func (c Catalog) Find(id string) (Entry, bool)
func (c Catalog) ForKindVersion(kind, version string) (Entry, bool)

type Manager struct { /* ... */ }
func NewManager(stores *store.Store, home string, client *http.Client) *Manager
func (m *Manager) List(ctx context.Context) ([]model.RuntimeBuild, error)
func (m *Manager) Install(ctx context.Context, id string) (model.RuntimeBuild, error)
func (m *Manager) InstallFile(ctx context.Context, id, path string) (model.RuntimeBuild, error)
func (m *Manager) Remove(ctx context.Context, buildID string) error
func (m *Manager) Ensure(ctx context.Context, kind, version string) (model.RuntimeBuild, error)
func (m *Manager) Verify(ctx context.Context, buildID string) error
func (m *Manager) Available() []Entry
```

- [ ] **Step 1: Write failing tests**

```go
func TestInstallVerifiesChecksumAndExtractsSafely(t *testing.T) {
    m, srv := newTestRuntimeManager(t, fixtureTar("bin/node", "#!/bin/sh\n"))
    build, err := m.Install(ctx, "node-test")
    if err != nil { t.Fatal(err) }
    if _, err := os.Stat(filepath.Join(build.Path, "bin", "node")); err != nil { t.Fatal(err) }
}
func TestChecksumMismatchLeavesNoRuntime(t *testing.T) {
    m, srv := newTestRuntimeManager(t, corruptTar())
    if _, err := m.Install(ctx, "node-test"); err == nil { t.Fatal("want checksum error") }
    if builds, _ := m.List(ctx); len(builds) != 0 { t.Fatal("runtime registered") }
}
func TestExtractionRejectsTraversalAndSymlink(t *testing.T) {
    // Fixture tar contains ../escape and a symlink to /etc/passwd; Install must fail and write nothing outside the target.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/runtime ./internal/catalog`
Expected: FAIL (undefined: NewManager).

- [ ] **Step 3: Implement**

Embed `catalog.json`. Populate Node 22 and 24 LTS entries from `https://nodejs.org/dist/index.json` and verify each SHA-256 against `SHASUMS256.txt`; record URL, size, and checksum. Include one test-only fixture entry pointing at the httptest server. Download to `cache/<id>.part`, hash while streaming, verify, extract `.tar.gz` into a temp dir under `runtimes/`, rejecting `..`, absolute paths, and symlinks; atomically rename to `runtimes/<kind>/<version>-<build>`; persist the build. Support `InstallFile(ctx, id, path)` for offline tarballs with the same checksum/extraction path. `Remove` deletes files and the row; `Ensure` returns an installed matching build or installs the catalog entry; `Verify` re-hashes installed files. `Available` lists catalog entries.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/runtime ./internal/catalog`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog internal/runtime
git commit -m "feat: add runtime catalog, verified downloads, and Node installs"
```

---

## Task 14: Site service — create, start, stop, delete, routes, and rollback

**Files:**
- Create: `internal/sites/service.go`, `internal/sites/routes.go`, `internal/sites/service_test.go`
- Create: `internal/sites/fakes_test.go`

**Interfaces:**
- Produces:

```go
type CreateRequest struct {
    Name, Path, Kind, NodeVersion, Command, DocumentRoot string
    WWW, TLS, ForceHTTPS, SPAFallback bool
    Env map[string]string
}
type UpdateRequest struct {
    Domain string; WWW, TLS, ForceHTTPS, SPAFallback *bool
    Command, DocumentRoot string; Env map[string]string
}
type Service struct { /* ... */ }
func NewService(deps Deps) *Service
type Deps struct {
    Store *store.Store; Backend backend.Backend; Helper Hosts; Certs Certs
    Router *proxy.Router; Ports *domain.PortAllocator; Config config.Config; Logs *logs.Store
    Now func() time.Time; NewID func() string
}
type Hosts interface { SyncHosts(ctx context.Context, domains []string) error }
type Certs interface {
    Ensure(ctx context.Context, owner string, domains []string) (model.CertRecord, error)
    Load(rec model.CertRecord) (*tls.Certificate, error)
    Delete(ctx context.Context, owner string) error
}
func (s *Service) Create(ctx context.Context, req CreateRequest) (model.Site, error)
func (s *Service) Update(ctx context.Context, ref string, req UpdateRequest) (model.Site, error)
func (s *Service) Get(ctx context.Context, ref string) (model.Site, error)
func (s *Service) List(ctx context.Context) ([]model.Site, error)
func (s *Service) Start(ctx context.Context, ref string) error
func (s *Service) Stop(ctx context.Context, ref string) error
func (s *Service) Restart(ctx context.Context, ref string) error
func (s *Service) Delete(ctx context.Context, ref string) error
func (s *Service) RebuildRoutes(ctx context.Context) error
```

- [ ] **Step 1: Write failing tests**

```go
func TestCreateStaticSiteSyncsHostsAndPublishesRoute(t *testing.T) {
    svc, fakes := newTestService(t)
    site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static", TLS: true, WWW: true})
    if err != nil { t.Fatal(err) }
    if site.Domain != "blog.test" || site.Status != model.StatusRunning { t.Fatalf("%+v", site) }
    if !fakes.hosts.calledWith("blog.test") || !fakes.hosts.calledWith("www.blog.test") { t.Fatal("hosts not synced") }
    if _, ok := fakes.router.Lookup("blog.test"); !ok { t.Fatal("route missing") }
    if _, ok := fakes.router.Lookup("www.blog.test"); !ok { t.Fatal("www route missing") }
    if fakes.certs.ensured["site"] == "" { t.Fatal("cert not issued") }
}
func TestCreateRollsBackWhenHostsSyncFails(t *testing.T) {
    svc, fakes := newTestService(t); fakes.hosts.err = errors.New("boom")
    if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err == nil { t.Fatal("want error") }
    if sites, _ := svc.List(ctx); len(sites) != 0 { t.Fatal("site persisted") }
}
func TestUpdateDomainRollsBackOnCertFailure(t *testing.T) {
    svc, fakes := newTestService(t)
    site, _ := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
    fakes.certs.err = errors.New("signer down")
    if _, err := svc.Update(ctx, site.Name, UpdateRequest{Domain: "new.test"}); err == nil { t.Fatal("want error") }
    got, _ := svc.Get(ctx, site.Name)
    if got.Domain != "blog.test" { t.Fatalf("domain changed to %q", got.Domain) }
    if !fakes.hosts.calledWith("blog.test") { t.Fatal("old domain not restored") }
}
func TestDeleteRemovesHostsCertAndRoute(t *testing.T) {
    svc, fakes := newTestService(t)
    site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"})
    if err != nil { t.Fatal(err) }
    if err := svc.Delete(ctx, site.Name); err != nil { t.Fatal(err) }
    if _, ok := fakes.router.Lookup("blog.test"); ok { t.Fatal("route still present") }
    if fakes.hosts.has("blog.test") { t.Fatal("hosts entry still present") }
    if _, err := fakes.store.GetSite(ctx, site.ID); !errors.Is(err, store.ErrNotFound) { t.Fatalf("site row remains: %v", err) }
    if !fakes.backend.stopped["site"] { t.Fatal("backend not stopped") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sites`
Expected: FAIL (undefined: NewService).

- [ ] **Step 3: Implement**

`Create`: validate name/domain/path with `ValidateSiteDomain`; reject duplicates; allocate an HTTP port; compute domains (`<name>.test` + optional `www`); ensure the runtime for Node via `Backend.EnsureRuntime`; require all existing domains plus new ones **plus `devstack.test`** to sync through `Hosts`; issue cert **before** persisting (rollback helper sync on failure); persist site `starting`; call `Backend.Prepare`; start backend; update ports/status/running; `RebuildRoutes`. On any failure, stop backend, delete cert, re-sync old domains, delete row, and return the original error. For static sites ensure the document root exists (default `.`); Node requires a version and command. `Update` supports domain rename: validate, union old+new domains, sync hosts, issue the new cert, persist/update routes, then remove the old cert and re-sync hosts without the old domain; any failure restores the old domain/cert/routes. `Start` calls `Backend.Prepare` (idempotent) then `Backend.Start`, sets desired/status, rebuilds routes; `Stop` stops, sets desired `stopped`, rebuilds routes; `Delete` stops, syncs remaining domains plus `devstack.test`, deletes cert/backend state/row, rebuilds routes. `Get`/`List` refresh status from `Backend.Status` and persist changes. On `NewService`, reserve every existing site port in the allocator. `RebuildRoutes` ensures the dashboard cert (`Certs.Ensure(DashboardOwner, []string{"devstack.test"})`), builds one snapshot from all sites (primary + `www` alias) + certs + the dashboard route (`devstack.test` → `127.0.0.1:7070`), and calls `Router.Replace`; `DefaultCert` is the dashboard cert. Ref resolution accepts ID or name. Keep route host lowercase. Persist an event on every status change.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/sites`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sites
git commit -m "feat: add site lifecycle service with rollback and routes"
```

---

## Task 15: Doctor checks and repair

**Files:**
- Create: `internal/doctor/doctor.go`, `internal/doctor/checks.go`, `internal/doctor/doctor_test.go`

**Interfaces:**
- Produces:

```go
type Status string
const ( StatusOK Status = "ok"; StatusWarn = "warn"; StatusFail = "fail"; StatusSkipped = "skipped" )
type Check struct { ID, Title string; Status Status; Detail, Remediation string }
type Report struct { Checks []Check; OK bool }
type Service struct { /* ... */ }
func NewService(deps Deps) *Service
type Deps struct {
    Config config.Config; Home string; Store *store.Store; Helper Helper; Trust Trust
    Ports PortChecker; Runtimes RuntimeChecker; Disk DiskChecker
}
type Helper interface {
    Status(ctx context.Context) (helperproto.StatusResponse, error)
    SyncHosts(ctx context.Context, domains []string) error
    InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error)
}
type Trust interface { TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error) }
type PortChecker interface { Listenable(addr string) error }
type RuntimeChecker interface { VerifyAll(ctx context.Context) error }
type DiskChecker interface { FreeBytes(path string) (uint64, error) }
func (s *Service) Run(ctx context.Context) (Report, error)
func (s *Service) Repair(ctx context.Context) (Report, error)
```

- [ ] **Step 1: Write failing tests**

```go
func TestReportFailsWhenHelperUnavailable(t *testing.T) {
    svc, fakes := newTestDoctor(t); fakes.helper.err = errors.New("down")
    rep, err := svc.Run(ctx)
    if err != nil { t.Fatal(err) }
    if rep.OK { t.Fatal("want not OK") }
    if !hasCheck(rep, "helper", StatusFail) { t.Fatalf("%+v", rep.Checks) }
}
func TestRepairResyncsHostsAndTrust(t *testing.T) {
    svc, fakes := newTestDoctor(t); fakes.helper.hostsMissing = true
    rep, _ := svc.Repair(ctx)
    if !fakes.helper.synced || !fakes.trust.installed { t.Fatal("repair did not run") }
    if !rep.OK { t.Fatalf("repair report: %+v", rep.Checks) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/doctor`
Expected: FAIL (undefined: NewService).

- [ ] **Step 3: Implement**

Checks: helper status/version, hosts block matches registered domains, CA fingerprint/trust targets, ports 80/443/8080/8443/7070 state, runtime integrity, free disk >1 GB. Each check returns remediation text. `Repair` calls helper trust install, resyncs hosts from the store, re-verifies runtimes, and reruns checks. WSL check returns `StatusSkipped` until Plan 3. `OK` is true only when no check is `fail`; warnings (browser not installed) do not fail.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/doctor`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/doctor
git commit -m "feat: add doctor checks and repair actions"
```

---

## Task 16: REST API, auth, SSE, fs browser, and dashboard hosting

**Files:**
- Create: `internal/api/server.go`, `internal/api/middleware.go`, `internal/api/handlers_sites.go`, `internal/api/handlers_runtime.go`, `internal/api/handlers_doctor.go`, `internal/api/handlers_fs.go`, `internal/api/sse.go`, `internal/api/api_test.go`
- Create: `api/openapi.yaml`
- Create: `ui/embed.go`, `ui/dist/.gitkeep` (placeholder until Task 19)

**Interfaces:**
- Produces:

```go
type SiteService interface {
    Create(ctx context.Context, req sites.CreateRequest) (model.Site, error)
    Update(ctx context.Context, ref string, req sites.UpdateRequest) (model.Site, error)
    Get(ctx context.Context, ref string) (model.Site, error)
    List(ctx context.Context) ([]model.Site, error)
    Start(ctx context.Context, ref string) error
    Stop(ctx context.Context, ref string) error
    Restart(ctx context.Context, ref string) error
    Delete(ctx context.Context, ref string) error
}
type RuntimeService interface {
    List(ctx context.Context) ([]model.RuntimeBuild, error)
    Install(ctx context.Context, id string) (model.RuntimeBuild, error)
    Remove(ctx context.Context, buildID string) error
    Available() []catalog.Entry
}
type DoctorService interface {
    Run(ctx context.Context) (doctor.Report, error)
    Repair(ctx context.Context) (doctor.Report, error)
}
type HelperClient interface {
    Status(ctx context.Context) (helperproto.StatusResponse, error)
    SyncHosts(ctx context.Context, domains []string) error
    InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error)
    RemoveTrust(ctx context.Context, userHome string) ([]trust.Target, error)
    TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error)
}
type Config struct { UI fs.FS; UserHome string; MaxBodyBytes int64; AllowedHosts []string; Logger *slog.Logger }
func NewServer(st *store.Store, sites SiteService, runtimes RuntimeService, doc DoctorService, logs *logs.Store, helper HelperClient, cfg Config) *Server
func (s *Server) Handler() http.Handler
func WithSocketAuth(next http.Handler) http.Handler
```

- [ ] **Step 1: Write failing tests**

```go
func TestAPIRejectsForeignHostAndMissingCSRF(t *testing.T) {
    h := newTestAPI(t)
    req := httptest.NewRequest("GET", "http://127.0.0.1/api/v1/sites", nil)
    req.Host = "evil.test"
    rec := httptest.NewRecorder(); h.ServeHTTP(rec, req)
    if rec.Code != http.StatusForbidden { t.Fatalf("foreign host code = %d", rec.Code) }

    rec = httptest.NewRecorder()
    h.ServeHTTP(rec, httptest.NewRequest("POST", "http://127.0.0.1/api/v1/sites", strings.NewReader(`{"name":"blog","path":"/tmp","kind":"static"}`)))
    if rec.Code != http.StatusForbidden { t.Fatalf("csrf code = %d", rec.Code) }
}
func TestCreateSiteAndList(t *testing.T) {
    h, fake := newTestAPIWithFakes(t)
    // GET /api/v1/session, then POST with cookie + X-CSRF-Token; assert 201 and list contains blog.test.
}
func TestLogsSSEStreamsLines(t *testing.T) {
    h, fake := newTestAPIWithFakes(t)
    srv := httptest.NewServer(h); defer srv.Close()
    ctx, cancel := context.WithCancel(context.Background()); defer cancel()
    req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/sites/s1/logs/stream", nil)
    // Add the session cookie; the exact helper is provided by newTestAPIWithFakes.
    resp, err := http.DefaultClient.Do(req)
    if err != nil { t.Fatal(err) }
    defer resp.Body.Close()
    io.WriteString(fake.logs.Writer("s1", "app"), "hello\n")
    buf := make([]byte, 256)
    n, _ := resp.Body.Read(buf)
    if !strings.Contains(string(buf[:n]), "hello") { t.Fatalf("stream = %q", buf[:n]) }
}
func TestFSBrowserListsDirsAndRejectsFiles(t *testing.T) {
    h := newTestAPI(t)
    dir := t.TempDir()
    if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0644); err != nil { t.Fatal(err) }
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/fs?path="+url.QueryEscape(dir), nil))
    if rec.Code != 200 || !strings.Contains(rec.Body.String(), "sub") || strings.Contains(rec.Body.String(), "nope") {
        t.Fatalf("browse response = %d %q", rec.Code, rec.Body.String())
    }
    rec = httptest.NewRecorder()
    h.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/v1/fs?path="+url.QueryEscape(filepath.Join(dir, "secret.txt")), nil))
    if rec.Code != http.StatusBadRequest { t.Fatalf("file browse code = %d", rec.Code) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api`
Expected: FAIL (undefined: NewServer).

- [ ] **Step 3: Implement**

Routes under `/api/v1`: `GET health`, `GET session`, sites CRUD (POST/GET/PATCH/DELETE) + start/stop/restart, `GET sites/{id}/logs/stream`, `GET events` (SSE polling the store every second for new event IDs), runtimes list/install/remove, trust status/repair/remove, doctor run/repair, `GET fs?path=`, settings. UI served at `/` and SPA fallback. Host allowlist (`127.0.0.1`, `localhost`, `devstack.test`, with any port) is enforced on every request; foreign Host → 403 before handlers. Session cookie `devstack_session` (HttpOnly, SameSite=Strict, Secure when TLS) plus CSRF token returned by `GET /session`; all mutating methods require `X-CSRF-Token` for cookie auth. `WithSocketAuth` marks requests authenticated with no CSRF (CLI). JSON error envelope `{code,message,details}` with stable codes (`INVALID_REQUEST`, `NOT_FOUND`, `DOMAIN_CONFLICT`, `HOSTS_SYNC_FAILED`, `RUNTIME_MISSING`, `PHP_CONFIG_INVALID`, `TRUST_MISSING`, `PORT_IN_USE`, `INTERNAL`). SSE sends `data: <json>\n\n` with keepalives. fs browser accepts absolute directories only, returns sorted dirs (name, path), rejects non-directories and unreadable paths; never returns file contents. `api/openapi.yaml` documents every route.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api api ui/embed.go
git commit -m "feat: add authenticated REST API, SSE, and fs browser"
```

---

## Task 17: Daemon wiring, socket server, and API client

**Files:**
- Create: `internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`
- Create: `internal/client/client.go`, `internal/client/client_test.go`
- Modify: `cmd/devstack/main.go`, `cmd/devstack-helper/main.go`

**Interfaces:**
- Produces:

```go
type Options struct { Home, HelperSocket string; HTTPPort, HTTPSPort, UIPort int; LogLevel string }
func Run(ctx context.Context, opts Options) error
func EnsureRunning(ctx context.Context, opts Options) error
func SocketPath(home string) string
func PidPath(home string) string

type Client struct { /* ... */ }
func New(home string) (*Client, error)
func (c *Client) ListSites(ctx) ([]model.Site, error)
func (c *Client) GetSite(ctx, ref string) (model.Site, error)
func (c *Client) AddSite(ctx, req sites.CreateRequest) (model.Site, error)
func (c *Client) UpdateSite(ctx, ref string, req sites.UpdateRequest) (model.Site, error)
func (c *Client) SiteAction(ctx, ref, action string) error
func (c *Client) DeleteSite(ctx, ref string) error
func (c *Client) Logs(ctx, ref string, follow bool) (io.ReadCloser, error)
func (c *Client) Doctor(ctx) (doctor.Report, error)
func (c *Client) Repair(ctx) (doctor.Report, error)
func (c *Client) TrustStatus(ctx) ([]trust.Target, error)
func (c *Client) TrustRepair(ctx) ([]trust.Target, error)
func (c *Client) TrustRemove(ctx) ([]trust.Target, error)
func (c *Client) Runtimes(ctx) ([]model.RuntimeBuild, error)
func (c *Client) AvailableRuntimes(ctx) ([]catalog.Entry, error)
func (c *Client) InstallRuntime(ctx, id string) (model.RuntimeBuild, error)
func (c *Client) RemoveRuntime(ctx, buildID string) error
```

- [ ] **Step 1: Write failing tests**

```go
func TestDaemonServesAndRestartsCleanly(t *testing.T) {
    opts := Options{Home: t.TempDir(), HelperSocket: startTestHelper(t), HTTPPort: freePort(t), HTTPSPort: freePort(t), UIPort: freePort(t)}
    ctx, cancel := context.WithCancel(context.Background()); defer cancel()
    done := make(chan error, 1); go func() { done <- Run(ctx, opts) }()
    waitForSocket(t, SocketPath(opts.Home))
    // GET /api/v1/health over the unix socket with WithSocketAuth; assert 200.
    cancel(); if err := <-done; err != nil { t.Fatal(err) }
}
func TestClientSitesCRUD(t *testing.T) {
    home := t.TempDir()
    startFakeAPI(t, home) // serves POST/GET/DELETE /api/v1/sites over SocketPath(home)
    c, err := New(home)
    if err != nil { t.Fatal(err) }
    site, err := c.AddSite(ctx, sites.CreateRequest{Name: "blog", Path: t.TempDir(), Kind: "static"})
    if err != nil { t.Fatal(err) }
    list, err := c.ListSites(ctx)
    if err != nil || len(list) != 1 || list[0].Domain != "blog.test" { t.Fatalf("%+v %v", list, err) }
    if err := c.DeleteSite(ctx, site.ID); err != nil { t.Fatal(err) }
    if list, _ = c.ListSites(ctx); len(list) != 0 { t.Fatalf("site not deleted: %+v", list) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/daemon ./internal/client`
Expected: FAIL (undefined: Run).

- [ ] **Step 3: Implement**

Daemon `Run`: load config (CLI port overrides), ensure dirs, open store/logs, dial helper with retry, call `EnsureCA` and `backend.InstallCA(rootPEM)`, build runtime/certs/local backend/sites/doctor/API, start proxy, start API on `127.0.0.1:<ui>` and Unix socket, write pid file, `CleanupOrphans`, ensure the dashboard certificate, `RebuildRoutes`, call `Backend.Prepare` then start sites whose desired state is running, start daily renewal ticker, and shut down cleanly on signal. On shutdown, stop supervised processes without changing each site's desired state, so running sites come back on the next launch. `EnsureRunning` pings the socket, otherwise spawns `devstack daemon run` detached with logs in `run/daemon.log`, waits up to 10s. `Client` uses a Unix-socket HTTP transport with `WithSocketAuth` on the server; methods map one-to-one to the Task 16 routes and stream logs via the returned body. Helper `main` serves `ServerConfig` from flags (`--socket`, `--state-dir`, `--hosts`, `--trust-dir`, `--allow-uid`, `--http-port`, `--https-port`, `--proxy-http`, `--proxy-https`) and implements `Uninstall` by removing trust, clearing the hosts block, and stopping forwarders.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/daemon ./internal/client`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon internal/client cmd
git commit -m "feat: wire daemon and unix-socket API client"
```

---

## Task 18: CLI commands and output

**Files:**
- Create: `internal/cli/root.go`, `internal/cli/site.go`, `internal/cli/runtime.go`, `internal/cli/doctor.go`, `internal/cli/daemon.go`, `internal/cli/init.go`, `internal/cli/uninstall.go`, `internal/cli/cli_test.go`
- Modify: `cmd/devstack/main.go`

**Interfaces:**
- Produces:

```go
type Client interface {
    ListSites(ctx context.Context) ([]model.Site, error)
    GetSite(ctx context.Context, ref string) (model.Site, error)
    AddSite(ctx context.Context, req sites.CreateRequest) (model.Site, error)
    UpdateSite(ctx context.Context, ref string, req sites.UpdateRequest) (model.Site, error)
    SiteAction(ctx context.Context, ref, action string) error
    DeleteSite(ctx context.Context, ref string) error
    Logs(ctx context.Context, ref string, follow bool) (io.ReadCloser, error)
    Doctor(ctx context.Context) (doctor.Report, error)
    Repair(ctx context.Context) (doctor.Report, error)
    TrustStatus(ctx context.Context) ([]trust.Target, error)
    TrustRepair(ctx context.Context) ([]trust.Target, error)
    TrustRemove(ctx context.Context) ([]trust.Target, error)
    Runtimes(ctx context.Context) ([]model.RuntimeBuild, error)
    AvailableRuntimes(ctx context.Context) ([]catalog.Entry, error)
    InstallRuntime(ctx context.Context, id string) (model.RuntimeBuild, error)
    RemoveRuntime(ctx context.Context, buildID string) error
}
func NewRootCmd(newClient func(home string) (Client, error)) *cobra.Command
```

- [ ] **Step 1: Write failing tests**

```go
func TestCLIAddAndList(t *testing.T) {
    fake := &fakeClient{sites: []model.Site{{Name: "blog", Domain: "blog.test", Status: model.StatusRunning}}}
    out := runCLI(t, fake, "site", "list")
    if !strings.Contains(out, "blog") || !strings.Contains(out, "https://blog.test") { t.Fatalf("output = %q", out) }
}
func TestCLIJSONOutputAndExitCode(t *testing.T) {
    fake := &fakeClient{sites: []model.Site{{Name: "blog", Domain: "blog.test"}}}
    out := runCLI(t, fake, "site", "list", "--json")
    var got []model.Site
    if err := json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 { t.Fatalf("%q %v", out, err) }
    fake.err = errors.New("boom")
    if code := runCLIExit(t, fake, "site", "start", "blog"); code == 0 { t.Fatal("want non-zero exit") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli`
Expected: FAIL (undefined: NewRootCmd).

- [ ] **Step 3: Implement**

Commands from spec §13 (Plan 1 subset): `init`, `doctor`, `daemon run|status|stop`, `site add|list|show|start|stop|restart|remove|logs|open`, `runtime list|install|remove`, `trust status|repair|remove`, `ui`, `uninstall`, `version`; global `--home`, `--json`. Site, doctor, runtime, trust, and ui commands call `daemon.EnsureRunning` before using the client. `site add` supports `--name --path --kind static|node --node 22|24 --command --www --no-https --force-https --spa --env KEY=VALUE`, and prompts before installing a missing Node runtime unless `--yes`. `init` and `uninstall` dial the helper directly with `helperclient` for `SetForwarders`, `EnsureCA`, `InstallTrust`, `RemoveTrust`, and `SyncHosts`, then use the daemon client for the rest. `init` syncs `devstack.test` plus existing sites, starts the daemon, runs doctor, and prints the dashboard URL. `uninstall` stops the daemon, removes trust, clears the hosts block, disables forwarders, then prints the elevated command to remove units/binaries; project files are untouched. Human output uses `text/tabwriter`; errors print code, message, remediation, and exit non-zero; `--json` emits only JSON on stdout.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli cmd/devstack
git commit -m "feat: add CLI commands and human/JSON output"
```

---

## Task 19: Dashboard UI

**Files:**
- Existing (built ahead of the plan at `1d7a2bd`): `ui/package.json`, `ui/vite.config.ts`, `ui/tsconfig.json`, `ui/index.html`, `ui/src/**`, `ui/src/lib/api.test.ts`
- Create: `ui/tests/e2e.spec.ts`, `ui/playwright.config.ts`
- Modify: `ui/embed.go`, `internal/api/server.go` (serve embedded assets)

**Interfaces:**
- Produces: embedded `ui.FS()` via `//go:embed all:dist`; dashboard served at `/` with SPA fallback and static assets under `/assets/`.
- Produces API client `api.ts` functions mirroring Task 16 routes; CSRF from `GET /api/v1/session`.

- [ ] **Step 1: Write failing embedding and Playwright tests**

```go
func TestUIEmbedServesIndex(t *testing.T) {
    // GET / on a server with the embedded FS returns index.html; GET /api/v1/health
    // still routes to the API; GET /assets/<hashed>.js returns the asset with a JS MIME type.
}
```
Playwright: visit dashboard, see Sites, open Add Site, browse a temp directory, submit, see `https://blog.test`, open detail, assert logs panel and start/stop buttons.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./ui ./internal/api` and `cd ui && npx playwright test`
Expected: FAIL (missing embed and Playwright config).

- [ ] **Step 3: Implement**

The Svelte 5 dashboard already exists (Sites, Add Site, Site detail, Runtimes, Doctor, Settings; mock/real API; CSRF; SSE; light/dark; responsive). Review it against the design brief, then add `ui/embed.go` with `//go:embed all:dist`, serve `index.html` for non-API routes and assets under `/assets/`, and add the Playwright config using the daemon from Task 17 with a temp home. Record any UI gaps for Task 25, which adds the Serving screen and vhost preview.

- [ ] **Step 4: Run tests and build**

Run: `cd ui && npm ci && npm test && npm run build && cd .. && go test ./... && go build ./...`
Expected: PASS and embedded assets build.

- [ ] **Step 5: Commit**

```bash
git add ui internal/api
git commit -m "feat: embed dashboard and add Playwright coverage"
```

---

## Task 20: Linux install, systemd units, docs, and CI

**Files:**
- Create: `build/systemd/devstack-helper.service`, `build/systemd/devstack-daemon.service`
- Create: `build/helper.yaml.example`
- Create: `install.sh`
- Create: `docs/install.md`, `docs/sites.md`, `docs/tls.md`
- Modify: `Makefile`, `.github/workflows/ci.yml`, `README.md`
- Create: `test/e2e/linux.sh`

**Interfaces:**
- Produces `make build` outputs `dist/devstack` and `dist/devstack-helper`.
- Produces `install.sh` that installs binaries, writes helper config with the invoking UID, installs/enables units (via sudo), and prints next steps.
- Produces `test/e2e/linux.sh` returning non-zero on failure.

- [ ] **Step 1: Write the failing E2E script**

```bash
#!/usr/bin/env bash
set -euo pipefail
# Build, start helper with temp state/hosts/trust dirs and high ports,
# start daemon with temp home and high ports, create a static site,
# curl https://blog.test with --resolve and the CA, restart daemon,
# assert the site still serves and exactly one process group exists.
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash test/e2e/linux.sh`
Expected: FAIL (missing install/build wiring).

- [ ] **Step 3: Implement**

Makefile targets: `build`, `test`, `lint`, `ui`, `e2e`. Units: helper runs `/usr/local/lib/devstack/devstack-helper --config /etc/devstack/helper.yaml` as root; daemon runs as the user with `Restart=on-failure`. `install.sh` refuses non-systemd systems, copies binaries to `/usr/local/lib/devstack` and `~/.local/bin`, writes `/etc/devstack/helper.yaml` with `allow_uid` and forwarder config, installs units, enables helper, and reminds the user to run `devstack init`. CI runs `go test -race ./...`, `go vet`, `go build ./...`, UI install/test/build, and the E2E script in an Ubuntu 24.04 container with high ports (trust-install checks skipped unless root). Docs cover install, first site, Node, TLS/trust, logs, uninstall, and the Plan 1 limitations (PHP and Windows arrive in later plans).

- [ ] **Step 4: Run verification**

Run: `make build && go test -race ./... && bash test/e2e/linux.sh`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add build install.sh docs Makefile .github README.md test/e2e
git commit -m "feat: add Linux install, systemd units, docs, and CI"
```

---

## Task 21: Vhost spec model and nginx/Apache renderers

**Files:**
- Create: `internal/vhost/vhost.go`, `internal/vhost/nginx.go`, `internal/vhost/apache.go`
- Create: `internal/vhost/nginx_test.go`, `internal/vhost/apache_test.go`
- Create: `internal/vhost/testdata/` golden files

**Interfaces:**
- Produces:

```go
type Server string
const ( ServerNginx Server = "nginx"; ServerApache Server = "apache" )
type Mode string
const ( ModeBuiltin Mode = "builtin"; ModeNginx Mode = "nginx"; ModeApache Mode = "apache" )
type SiteSpec struct {
    ID, Name, Domain string
    WWW bool
    Kind model.SiteKind
    DocumentRoot string
    BackendPort int
    TLS, ForceHTTPS, SPAFallback, FrontController bool
    DesiredState model.DesiredState
    CertPEM, KeyPEM []byte
    CertNotAfter time.Time
}
type DashboardSpec struct { Domain string; UIPort int; CertPEM, KeyPEM []byte }
type Set struct { Server Server; Sites []SiteSpec; Dashboard DashboardSpec; MaxBodyBytes int64 }
func Render(set Set) (map[string][]byte, error)
func RenderSite(server Server, s SiteSpec) ([]byte, error)
func ValidateSiteSpec(s SiteSpec) error
var ErrPHPUnsupported = errors.New("PHP vhosts arrive with Plan 2")
```

- [ ] **Step 1: Write failing tests**

```go
func TestRenderNginxStaticMatrix(t *testing.T) {
    base := SiteSpec{ID: "s1", Name: "blog", Domain: "blog.test", WWW: true,
        Kind: model.SiteStatic, DocumentRoot: "/home/u/blog/public",
        DesiredState: model.DesiredRunning, TLS: true, CertPEM: certPEM, KeyPEM: keyPEM}
    for _, tc := range []struct{ name string; mutate func(*SiteSpec); want ...string }{
        {"force-https", func(s *SiteSpec) { s.ForceHTTPS = true }, "return 308 https://$host$request_uri"},
        {"no-force", func(s *SiteSpec) {}, "root /home/u/blog/public"},
        {"spa", func(s *SiteSpec) { s.SPAFallback = true }, "try_files $uri $uri/ /index.html"},
        {"stopped", func(s *SiteSpec) { s.DesiredState = model.DesiredStopped }, "return 503"},
    } { /* render, compare golden file */ }
}
func TestRenderRejectsTraversalAndInjection(t *testing.T) {
    bad := []SiteSpec{
        {ID: "x", Domain: "evil.com", Kind: model.SiteStatic, DocumentRoot: "/tmp"},
        {ID: "x", Domain: "blog.test", Kind: model.SiteStatic, DocumentRoot: "/tmp/../etc"},
        {ID: "x\nserver", Domain: "blog.test", Kind: model.SiteStatic, DocumentRoot: "/tmp"},
        {ID: "x", Domain: "blog.test", Kind: model.SiteNode, BackendPort: 80},
        {ID: "x", Domain: "blog.test", Kind: model.SitePHP, DocumentRoot: "/tmp"},
    }
    for _, s := range bad { if err := ValidateSiteSpec(s); err == nil { t.Fatalf("accepted %+v", s) } }
}
func TestRenderEscapesApachePaths(t *testing.T) {
    // A document root containing spaces/quotes must render as one quoted argument,
    // and a newline must be rejected before rendering.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/vhost`
Expected: FAIL (undefined: Render).

- [ ] **Step 3: Implement**

Render nginx and Apache vhosts from `Set`: static `root`/`DocumentRoot` + `try_files`/`FallbackResource`, Node proxy to `BackendPort` with WebSocket upgrade and forwarded headers, dashboard vhost to `127.0.0.1:<UIPort>`, default 404, branded 503, and the full TLS/force-HTTPS matrix from the vhost spec. PHP returns `ErrPHPUnsupported`. Validate domains with `domain.ValidateDomain` (allow `devstack.test`), roots as absolute/symlink-resolved directories, ports in `41000–42999`, IDs/names `[a-z0-9-]`, and reject newlines/NUL/quotes in every interpolated value. Escape Apache paths as quoted arguments. Emit a generated-file header. Golden files cover static running/stopped, SPA, TLS on/off, force-HTTPS, www, Node, dashboard, default 404, and maintenance 503.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/vhost`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/vhost
git commit -m "feat: add nginx and Apache vhost renderers"
```

---

## Task 22: Helper vhost manager, bridge, atomic releases, and RPC

**Files:**
- Create: `internal/helper/vhosts.go`, `internal/helper/vhost_linux.go`, `internal/helper/vhosts_test.go`
- Create: `internal/helperproto/vhosts.go`
- Modify: `internal/helper/server.go`, `internal/helper/ca.go`
- Modify: `internal/helperproto/types.go`
- Modify: `cmd/devstack-helper/main.go` (flags `--vhost-root`, `--vhost-state`, `--nginx-include-dir`, `--apache-include-dir`; defaults `/etc/devstack`, `/var/lib/devstack-helper/vhosts.json`, auto-detect, auto-detect)

**Interfaces:**
- Produces:

```go
type VhostManager struct { /* ... */ }
func NewVhostManager(root, statePath string, authority *Authority, runner CommandRunner, logger *slog.Logger) *VhostManager
func (m *VhostManager) Sync(ctx context.Context, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error)
func (m *VhostManager) Status(ctx context.Context) (helperproto.VhostStatusResponse, error)
func (m *VhostManager) SetMode(ctx context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error)
func (m *VhostManager) Remove(ctx context.Context) error
func (a *Authority) VerifyLeaf(certPEM, keyPEM []byte, domains []string) error
```

- Produces RPCs: `GET /v1/vhosts/status`, `POST /v1/vhosts/sync`, `POST /v1/vhosts/mode`, `DELETE /v1/vhosts`, using `helperproto.VHostSyncRequest`, `VHostSite`, `VhostStatusResponse`.

- [ ] **Step 1: Write failing tests**

```go
func TestSyncWritesReleaseAndBridge(t *testing.T) {
    root := t.TempDir(); runner := &fakeRunner{}
    m := NewVhostManager(root, filepath.Join(root, "vhosts.json"), testAuthority(t), runner, slog.Default())
    st, err := m.Sync(ctx, validNginxRequest(t))
    if err != nil { t.Fatal(err) }
    if st.ConfigOK != true || st.CurrentRelease == "" { t.Fatalf("%+v", st) }
    if _, err := os.Stat(filepath.Join(root, "nginx", "current", "blog-test.conf")); err != nil { t.Fatal(err) }
    if !runner.called("nginx", "-t") { t.Fatal("config test not run") }
    if !runner.called("systemctl", "reload", "nginx") { t.Fatal("reload not run") }
}
func TestSyncRollsBackOnConfigFailure(t *testing.T) {
    // First sync succeeds; second render is forced to fail the fake config test.
    // Assert current still points at the first release and the error is VHOST_CONFIG_INVALID.
}
func TestSyncRejectsWrongSANAndLooseKey(t *testing.T) {
    // Cert SAN other.test, a 0644 key, and an expired cert must all fail before any write.
}
func TestSetModeBuiltinRestoresForwarders(t *testing.T) {
    // Switch to nginx, then builtin: bridge/release removed, SetForwarders(true) called.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/helper`
Expected: FAIL (undefined: NewVhostManager).

- [ ] **Step 3: Implement**

`Sync` validates the request (mode, domains, IDs, roots, ports, PEMs), renders via `internal/vhost`, writes a timestamped release under `<root>/<server>/releases/`, writes TLS files atomically (key `0600`, cert `0644`, fsynced), ensures the bridge include in the detected standard directory, swaps `current` with `os.Rename`, runs the fixed config test (`nginx -t` or `apachectl configtest`), reloads (`systemctl reload nginx|apache2|httpd`), and prunes to the last two releases. On test or reload failure, swap `current` back, re-run the test, and return `VHOST_CONFIG_INVALID`/`SERVER_RELOAD_FAILED` with scrubbed stderr. `SetMode` stops/starts forwarders through the existing `SetForwarders` path and performs the same rollback; `Remove` deletes only our bridge/releases/TLS files and restores forwarders. Detect the server binary, version, include directory, and Apache modules; unsupported layouts return `VHOST_INCLUDE_MISSING`/`SERVING_MODE_UNAVAILABLE`. Serialize syncs with a mutex. Reject payloads over 8 MiB. `Authority.VerifyLeaf` checks chain, key match, SAN set, and expiry.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/helper`
Expected: PASS (real nginx/Apache subtests skip when absent).

- [ ] **Step 5: Commit**

```bash
git add internal/helper internal/helperproto
git commit -m "feat: add helper-managed nginx and Apache vhosts"
```

---

## Task 23: Daemon serving-mode integration, static lifecycle, and API

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/sites/service.go`, `internal/sites/service_test.go`
- Modify: `internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`
- Create: `internal/api/handlers_vhosts.go`
- Modify: `internal/api/server.go`, `internal/api/api_test.go`
- Modify: `internal/client/client.go`, `internal/client/client_test.go`

**Interfaces:**
- Produces `Config.ServingMode string` (`builtin|nginx|apache`, default `builtin`, validated in `Validate`).
- Produces:

```go
type Vhosts interface {
    VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error)
    SyncVhosts(ctx context.Context, req helperproto.VHostSyncRequest) error
    SetServingMode(ctx context.Context, mode string, req helperproto.VHostSyncRequest) (helperproto.VhostStatusResponse, error)
    RemoveVhosts(ctx context.Context) error
}
func (s *Service) SetServingMode(ctx context.Context, mode string) error
func (s *Service) SyncVhosts(ctx context.Context) error
```

- Produces routes: `GET /api/v1/vhosts/status`, `POST /api/v1/vhosts/mode`, `POST /api/v1/vhosts/sync`, `GET /api/v1/vhosts/preview?site=&server=`.
- Produces client methods: `VhostStatus`, `SetServingMode`, `SyncVhosts`, `PreviewVhost`.

- [ ] **Step 1: Write failing tests**

```go
func TestExternalStaticStartsWithoutProcess(t *testing.T) {
    svc, fakes := newTestService(t); fakes.vhosts.mode = "nginx"
    site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static", TLS: true})
    if err != nil { t.Fatal(err) }
    if site.Status != model.StatusRunning || site.HTTPPort != 0 { t.Fatalf("%+v", site) }
    if fakes.backend.started["site"] { t.Fatal("static backend started in external mode") }
    if !fakes.vhosts.synced["blog.test"] { t.Fatal("vhost not synced") }
}
func TestCreateRollsBackWhenVhostSyncFails(t *testing.T) {
    svc, fakes := newTestService(t); fakes.vhosts.mode = "nginx"; fakes.vhosts.err = errors.New("bad config")
    if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "static"}); err == nil { t.Fatal("want error") }
    if sites, _ := svc.List(ctx); len(sites) != 0 { t.Fatal("site persisted") }
    if fakes.backend.started["site"] { t.Fatal("backend left running") }
}
func TestVhostModeRequiresCSRF(t *testing.T) {
    h := newTestAPI(t)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, httptest.NewRequest("POST", "http://127.0.0.1/api/v1/vhosts/mode", strings.NewReader(`{"mode":"nginx"}`)))
    if rec.Code != http.StatusForbidden { t.Fatalf("code = %d", rec.Code) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config ./internal/sites ./internal/api ./internal/client`
Expected: FAIL (undefined: ServingMode / SyncVhosts).

- [ ] **Step 3: Implement**

Add `serving_mode` to config defaults/validation. In external mode, static sites skip `Backend.Prepare`/`Start`; `Start` validates the root and syncs a running vhost, `Stop` syncs a 503 vhost, and `Get`/`List` derive status from desired state + root readability. Node/PHP keep supervised backends; every mutation and cert renewal calls `SyncVhosts`, and a failure rolls back like a hosts failure. Daemon startup reconciles helper state with config, syncs the full set, and skips the proxy listeners in external mode (API/UI stays on 7070). Add the four API handlers with session/CSRF/Host checks and the client methods. `PreviewVhost` renders without installing.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config ./internal/sites ./internal/api ./internal/client ./internal/daemon`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config internal/sites internal/api internal/client internal/daemon
git commit -m "feat: add external serving mode to daemon and API"
```

---

## Task 24: CLI vhost commands and Doctor checks

**Files:**
- Create: `internal/cli/vhost.go`
- Modify: `internal/cli/root.go`, `internal/cli/cli_test.go`
- Modify: `internal/doctor/checks.go`, `internal/doctor/doctor.go`, `internal/doctor/doctor_test.go`

**Interfaces:**
- Produces CLI: `devstack vhost status`, `vhost use nginx|apache|builtin [--yes]`, `vhost sync`, `vhost preview <site> [--server nginx|apache]`, `vhost path`; all support `--json`.
- Produces Doctor checks: `serving-mode`, `vhost-server`, `vhost-include`, `vhost-config`, `vhost-freshness`, `vhost-tls` with `ok|warn|fail|skipped` and remediation.

- [ ] **Step 1: Write failing tests**

```go
func TestCLIVhostStatusAndUse(t *testing.T) {
    fake := &fakeClient{vhostStatus: helperproto.VhostStatusResponse{Mode: "builtin", ServerVersion: "1.26.1"}}
    out := runCLI(t, fake, "vhost", "status")
    if !strings.Contains(out, "builtin") { t.Fatalf("output = %q", out) }
    out = runCLI(t, fake, "vhost", "use", "nginx", "--yes")
    if !fake.servingModeSet || fake.servingMode != "nginx" { t.Fatal("mode not switched") }
}
func TestDoctorReportsVhostConfigFailure(t *testing.T) {
    svc, fakes := newTestDoctor(t); fakes.vhosts.status.ConfigOK = false
    rep, _ := svc.Run(ctx)
    if hasCheck(rep, "vhost-config", StatusOK) { t.Fatalf("%+v", rep.Checks) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli ./internal/doctor`
Expected: FAIL (undefined: vhost command / vhost check).

- [ ] **Step 3: Implement**

Add the vhost command tree with confirmation for mode changes, human/JSON output, and daemon auto-start. Doctor checks the mode, server binary/version/service state, bridge include presence, config-test result, release freshness against registered sites, and TLS key modes; warnings for a stopped unused server. `vhost path` prints `/etc/devstack/vhosts/<server>/current` and the bridge path.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli ./internal/doctor`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli internal/doctor
git commit -m "feat: add vhost CLI and Doctor checks"
```

---

## Task 25: Serving UI, vhost preview, docs, and E2E

**Files:**
- Modify: `ui/src/routes/Settings.svelte`, `ui/src/routes/SiteDetail.svelte`
- Modify: `ui/src/lib/api.ts`, `ui/src/lib/types.ts`, `ui/src/lib/mock.ts`, `ui/src/lib/components/AppShell.svelte`
- Create: `docs/vhosts.md`
- Modify: `README.md`, `test/e2e/linux.sh`, `Makefile`, `.github/workflows/ci.yml`

**Interfaces:**
- Produces a Settings → Serving panel: mode selector, server/version, include/config-test state, last sync, Sync now, switch confirmation, preview drawer.
- Produces a Site detail "Preview vhost" action.
- Produces `docs/vhosts.md` covering prerequisites, include layout, mode switching, TLS, troubleshooting, and uninstall.
- Produces E2E coverage for builtin→nginx→builtin when nginx is installed.

- [ ] **Step 1: Write failing tests**

```ts
test('vhost mode switch sends CSRF and confirms impact', async () => {
  const calls: RequestInit[] = []
  global.fetch = vi.fn(async (_u, init) => { calls.push(init!); return new Response(JSON.stringify({ mode: 'nginx' }), { status: 200 }) })
  await api.setServingMode('nginx')
  expect((calls[0].headers as Record<string,string>)['X-CSRF-Token']).toBe('csrf')
})
```

Extend `test/e2e/linux.sh`: after the builtin HTTPS checks, run `devstack vhost use nginx --yes` (skip with a clear message if nginx is absent), assert `nginx -t`, curl a static and a Node site through the vhost, then `devstack vhost use builtin --yes` and assert the forwarders/proxy serve again.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npm test` and `bash test/e2e/linux.sh`
Expected: FAIL (missing vhost API/UI and E2E steps).

- [ ] **Step 3: Implement**

Add the Serving panel and preview drawer using the design-system tokens; wire the API/mock client and CSRF. Cover loading, empty, error, and confirmation states; show the impact of a mode switch (80/443 ownership, reload, temporary downtime). Add the docs and E2E steps. CI installs nginx/apache for the Ubuntu job.

- [ ] **Step 4: Run tests and build**

Run: `cd ui && npm test && npm run build && cd .. && go test ./... && go build ./... && bash test/e2e/linux.sh`
Expected: PASS (nginx-specific E2E skips cleanly when unavailable).

- [ ] **Step 5: Commit**

```bash
git add ui docs/vhosts.md README.md test/e2e Makefile .github
git commit -m "feat: add vhost serving UI, docs, and E2E"
```

---

## Task 26: Plan 1 acceptance pass

**Files:**
- Create: `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md`
- Modify: `README.md` (status/limitations)

**Interfaces:**
- No new code interfaces. Produces a recorded acceptance report.

- [ ] **Step 1: Write the acceptance checklist**

Copy the Plan 1 subset of spec §21: init/trust/hosts green; static site at trusted HTTPS; Node site with HMR WebSocket; start/stop/restart from CLI and UI; daemon restart reconciliation; uninstall/trust remove; docs. Add external serving: `devstack vhost use nginx` and `use apache` install native vhosts, serve static and Node at trusted HTTPS, preview vhosts, roll back a deliberately broken config, and `use builtin` restores the proxy/forwarders; uninstall removes our vhosts without touching unrelated configs.

- [ ] **Step 2: Run the automated acceptance script**

Run: `make build && bash test/e2e/linux.sh`
Expected: PASS; record output hashes/versions. The script exercises builtin mode and, when nginx is installed, builtin→nginx→builtin including static and Node traffic.

- [ ] **Step 3: Run the manual checks on a Fedora 44 workstation**

Verify Chrome and Firefox show no certificate warning for a static and a Node site, `devstack trust status` matches each browser, UI flows work, external nginx and Apache modes serve both site kinds after a mode switch and reload, and `devstack uninstall` restores hosts/CA/forwarders and removes our vhosts/bridge/TLS files. Record results.

- [ ] **Step 4: Fix or file every failure before marking the plan complete**

No task is complete with a failing manual check; either fix it and rerun, or record a blocking issue with a linked follow-up task.

- [ ] **Step 5: Commit the acceptance report**

```bash
git add docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md README.md
git commit -m "docs: record plan 1 acceptance results"
```

---

## Plan series and deferred work

- **Plan 1 extension — external vhosts:** Tasks 21–26 add helper-managed native nginx/Apache vhosts for static and Node sites, mode switching with rollback, CLI/API/UI, Doctor checks, docs, and acceptance. Spec: `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`.
- **Plan 2 — PHP:** PHP build pipeline (8.2–8.5, extensions), version management, per-site FPM, php.ini and extension toggles, FastCGI transport, PHP UI, plus the PHP vhost template and FastCGI wiring in the external-vhost subsystem (nginx `fastcgi_pass`, Apache `proxy:fcgi`).
- **Plan 3 — Windows/WSL:** agent, provisioning, path translation, Windows helper (named pipe, cert store, Firefox policy), Windows install, backend conformance. External nginx/Apache serving is Linux-only in M1.
- **Plan 4 — Packaging/release:** signed binaries/packages, release workflow, upgrade, diagnostics bundle, docs polish, full M1 acceptance.
