# dev-web-stack M1 — Plan 2: PHP Subsystem

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add PHP 8.2–8.5 to dev-web-stack with revision-pinned relocatable builds, one `php-fpm` master per running site, a composed per-site `php.ini`, per-site extension toggles, FastCGI through the built-in proxy and native nginx/Apache vhosts, CLI/API/UI management, Doctor diagnostics, and a CI build pipeline that publishes only checksum-verified PHP artifacts.

**Architecture:** Sites store a PHP version line (`8.4`) and an exact build ID (`php@8.4.4-r1`). The daemon resolves the installed build, composes `php.ini` from the build base, version defaults, site overrides, a raw site section, and a managed extension block; it renders one `php-fpm.conf` and starts a per-site master on an allocated port in 43000–43999 through the existing supervisor. The built-in proxy speaks FastCGI directly; external mode renders `fastcgi_pass`/`proxy:fcgi` vhosts to the same port. Version, ini, and extension changes use validate-then-swap: render and validate, start a second master on a new port, health-check it, publish the route/vhost, then drain the old master; any failure leaves the old master serving.

**Tech Stack:** Go 1.26+, `modernc.org/sqlite`, existing `internal/supervisor`, `internal/backend`, `internal/proxy`, `internal/vhost`, `internal/helper`, Svelte 5 + TypeScript + Vite, php-fpm 8.2–8.5, FastCGI, nginx/Apache (external mode only).

**Spec:** `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md` §10.2 and §11 (primary), plus §§7, 8, 12–15, 18–19, and 21. External serving follows `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`.

**Depends on:** `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md` (implemented). Plan 2 preserves every Plan 1 behavior: host allowlist, CSRF/session checks, peer-UID helper, vhost validation, supervisor semantics, and the dashboard design system.

> **Implementation status (2026-10-24):** Tasks 1–8 are complete and reviewed. Task 9 is the next task. Tasks 9–12 remain. See `docs/superpowers/status/2026-10-23-plan-2-php-handoff.md`.

> **Implementation rulings made while executing Tasks 5–6:**
> - Task 5 adds `proxy.KindPHP` and `Route.DocumentRoot`/`Route.FrontController` in `internal/proxy/router.go` so the Task 5 routes compile; FastCGI transport stays in Task 7.
> - FPM `error_log` is `/proc/self/fd/2`; the supervisor supplies a pipe, so FPM output merges into the site log stream with source `fpm`. A generation-local error-log file does not meet the log contract.
> - `phpSiteDir` rejects `"."` (and any non-local/separator id) so `removePHPConfigLocked` can never remove the sites root.
> - Task 6 allocates the new FastCGI port **before** composing/validating a running apply, because the FPM render and config hash embed the port. The plan's prose order cannot work.
> - A successful running apply stops both the `default` generation and the previous hash generation (when different), then releases the old port. A failed stop keeps the old port reserved and reports an event.
> - A target config hash equal to the current hash is a no-op apply; a stopped site with no port allocates and persists one for rendering without starting a master.
> - `startPHPLocked` is idempotent after a staged apply: it does not start a second `default` master when the site and backend report running.
> - `SavePHPDefaults` reports per-site failures in `PHPDefaultsResult.Sites`; only invalid defaults or a failed defaults-store write returns an error. `CheckRuntimeRemoval` returns `store.ErrNotFound` for an absent build.
> - Task 7 consumes the `proxy.KindPHP`/route fields added in Task 5; it adds the FastCGI transport and dispatch only. `HTTP_X_FORWARDED_FOR/PROTO/HOST` are synthesized from the actual connection/TLS/Host rather than trusted from the client, and `HTTP_HOST` is `req.Host` including any port.
> - Task 8 uses case-insensitive PHP matching in native nginx/Apache vhosts (`location ~*`, `(?i)\.php$`) and places nginx dotfile/source-backup denials before the PHP regex. The brief's case-sensitive sample strings are superseded so `.PHP` cannot fall through to static. Unchanged static vhosts keep Plan 1's ordinary-file behavior.

**Build-artifact dependency (honest gate):** `internal/catalog/catalog.json` contains **no PHP entries until a release publishes real artifacts**. The CI matrix in Task 11 builds, smoke-tests, checksums, SBOMs, and uploads each archive; only then does the release job generate a `php@<version>-r<revision>` entry from the published URL and checksum. Unit and integration tests use fixture catalogs and tarballs; acceptance uses this machine's real `/usr/local/php` when present and skips cleanly when absent. No production PHP URL or checksum is invented in this plan.

## Global Constraints

- Go 1.26+; module path `github.com/AramZahedi/dev-web-stack`.
- Target: systemd-based Linux x86_64, glibc 2.35+; PHP builds are produced on Ubuntu 22.04, x86_64 only. No Docker/Podman at runtime.
- Supported PHP lines: **8.2, 8.3, 8.4, 8.5**. Build IDs carry a revision, for example `8.4.12-r3`; sites pin the build ID, not just the version.
- PHP tarballs are relocatable and contain `bin/php`, `sbin/php-fpm`, `lib/php/extensions/*.so`, bundled shared libraries, and a base `php.ini`. `RPATH=$ORIGIN/../lib`. Approximate footprint: 150–300 MB per PHP build.
- Runtime catalog: embedded, no remote fetch. Every URL is HTTPS, every archive is pinned by SHA-256 and size, and extraction keeps the existing traversal/symlink guards. Installing requires explicit confirmation; `--yes` skips the prompt. Offline install from a local tarball with a matching checksum is supported. A PHP install runs `php-fpm -v` and a trivial FastCGI request before registration; failure leaves no registered runtime or files.
- One `php-fpm` master per running site. Config is `sites/<id>/<config-hash>/php-fpm.conf`, listening on an allocated backend TCP port in **43000–43999** (loopback on Linux; WSL NAT interface on Windows).
- FPM pool: `pm = dynamic`, `pm.max_children = 5`, `pm.start_servers = 2`, `pm.min_spare_servers = 1`, `pm.max_spare_servers = 3`, `catch_workers_output = yes`, `clear_env = no`.
- FPM readiness deadline is 30s. Site stop grace is 10s. Supervisor backoff is `1s, 2s, 4s, 8s, 16s, 30s`; crash after 5 failures in 5 minutes.
- `php.ini` composition order: build base `php.ini` → PHP version defaults → site key/value overrides → raw site section (comments and unknown directives preserved) → managed extension block. The managed extension block and `extension_dir` are read-only in the raw editor.
- Required baseline ini values: `display_errors=On`, `error_reporting=E_ALL`, `memory_limit=256M`, `upload_max_filesize=64M`, `post_max_size=64M`, `max_execution_time=120`, `date.timezone=UTC`, `opcache.enable=1`, `opcache.validate_timestamps=1`, `opcache.revalidate_freq=0`.
- Standard extensions enabled by default except `ffi`: `bcmath, bz2, calendar, curl, dba, dom, exif, ffi, fileinfo, filter, ftp, gd, gettext, gmp, iconv, intl, ldap, mbstring, mysqli, opcache, openssl, pcntl, pdo, pdo_mysql, pdo_pgsql, pdo_sqlite, pgsql, phar, posix, readline, session, simplexml, soap, sockets, sodium, sqlite3, sysvmsg, sysvsem, sysvshm, tidy, tokenizer, xml, xmlreader, xmlwriter, xsl, zip, zlib`.
- Extras disabled by default: `redis, apcu, xdebug, imagick, memcached, ssh2, yaml, igbinary, msgpack, grpc, pcov`; `imap` only for 8.2/8.3. `xdebug` uses `zend_extension` and defaults off; `opcache` is a standard `zend_extension` and defaults on.
- Built-in modules are always on and cannot be toggled: `core, ctype, date, filter, hash, json, pcre, random, spl, standard`.
- Validation runs the build's `php -c <generated> -r 'exit(0);'` and `php-fpm -t`; errors block apply with line numbers where available. A failed apply leaves the old master serving.
- PHP extension updates mean installing a newer PHP build. No independent PECL updates in M1. Dependency order is catalog metadata (for example `igbinary` before `redis`).
- `devstack php --site <name> -- ...` runs the site build with its generated ini; it is not Composer or a package manager.
- PHP vhost ports are **43000–43999**; Node HTTP ports remain **41000–42999**. Unknown hosts, TLS, force-HTTPS, 503, and 404 behavior are unchanged from Plan 1.
- Logs: rotate at 10 MB, keep 5 files, 5,000-line ring per site; FPM master and pool output merge into the site stream with source `fpm`.
- Security: no arbitrary commands or paths; the daemon never runs as root; the helper peer-UID check, vhost validation, document-root containment, and API Host/CSRF checks are unchanged. Secrets and keys stay `0600`; logs redact `*KEY*`, `*TOKEN*`, `*SECRET*`, `*PASSWORD*`.
- The dashboard keeps the Plan 1 design system (tokens, spacing, focus states, light/dark, keyboard navigation).
- Every task ends with a green test run and one commit. No TODO/TBD placeholders.

## Review Focus

1. **Ini and FPM config injection or invalid syntax.** A site-supplied key/value, raw line, path, env value, or extension name containing a newline, NUL, managed marker, `extension=`, `zend_extension=`, or `extension_dir` must be rejected before any file is written; a syntactically invalid ini must fail with a line where PHP reports one, and the old master must keep serving. Pinned by Task 2 `TestComposeRejectsInjection`, Task 3 `TestRenderFPMConfigRejectsUnsafeValues`, and Task 6 `TestApplyPHPInvalidIniLeavesOldMasterServing`.
2. **FastCGI framing, streaming, and cancellation.** Large params and request bodies must be chunked correctly, headers split across STDOUT records must parse, STDERR must be bounded and surfaced, END_REQUEST/protocol status must be honoured, and cancelling the request must close the connection rather than hang. Pinned by Task 3 `TestFastCGIChunksParamsAndStreamsBody` and `TestFastCGIContextCancellationClosesConnection`.
3. **SCRIPT_FILENAME, PATH_INFO, and PHP source containment.** Traversal, percent-encoded traversal, symlinks escaping the document root, dotfiles, `.php/extra` requests, and editor/PHP source backups (`.PHP`, `.php~`, `.php.bak`) must never execute or disclose a file outside the root; front-controller requests must pass the original path as `PATH_INFO`, and existing non-PHP files must be served directly. Pinned by Task 7 `TestPHPProxyRejectsTraversalAndSymlinkEscape`, `TestPHPProxyNeverServesPHPSource`, and `TestPHPProxyFrontControllerParams`.
4. **Validate-then-swap atomicity.** A failed lint, failed `php-fpm -t`, failed health request, failed store write, failed route rebuild, or failed vhost sync must stop and discard the new master, keep the old row/route/vhost, and release the new FastCGI port; concurrent applies must not leak processes or ports. Pinned by Task 6 `TestApplyPHPVersionSwitchRollsBackOnHealthFailure`, `TestApplyPHPVhostFailureKeepsOldMaster`, and `TestConcurrentApplySerializes`.
5. **Runtime identity, integrity, and in-use removal.** Two revisions of one version must install to distinct directories; a checksum mismatch or fake production URL must be rejected; removing a build referenced by a site must fail with `PHP_VERSION_IN_USE` and list the sites, while removing an unreferenced older revision of the same line succeeds. Pinned by Task 1 `TestInstallKeepsPHPRevisionsSeparate`, Task 1 `TestPHPEntryRequiresPinnedHTTPSArtifact`, and Task 6 `TestCheckRuntimeRemovalBlocksReferencedBuild`.

---

## File Structure

New packages and files, grouped by responsibility. Files that change together live together; each package owns one boundary.

- `internal/model/model.go` — `Site.PHPBuild`, `Site.PHPIniRaw`, `Site.PHPConfigHash`, `PHPDefaults`, `SitePHPConfig`.
- `internal/store/migrations/0003_php_build.sql` — add the three site columns and `site_php_config.applied_at`.
- `internal/store/store.go` — persist the new columns; `php_defaults` and `site_php_config` CRUD.
- `internal/catalog/catalog.go` — revision-aware `Entry`, `ParseID`, `FormatID`, `ForKindVersion` (newest revision), `NewestForLine`.
- `internal/catalog/php.go`, `internal/catalog/php.json` — embedded extension metadata, built-ins, per-version ini/extension defaults, dependency resolution.
- `internal/runtime/manager.go` — revision-aware install paths, `EnsureBuild`, `InstalledForLine`, `NewManagerWithCatalog` test seam.
- `internal/phpini/` — `ini.go` (composition and markers), `validate.go` (override/raw validation and PHP lint), tests.
- `internal/fcgi/` — `records.go` (record encoding/decoding), `client.go` (dial, request, response, streaming), fake server tests.
- `internal/phpfpm/` — `config.go` (typed config), `render.go` (`php-fpm.conf`), `validate.go` (`php-fpm -t`), tests.
- `internal/backend/backend.go` — PHP fields in `SiteSpec`, `EnsureRuntimeBuild`, `PHPStager` interface.
- `internal/backend/local.go`, `internal/backend/php.go` — PHP prepare/start/stop/status/logs/exec, staging generations, health probe.
- `internal/sites/service.go` — PHP create/start/stop/delete, front-controller update, deps.
- `internal/sites/php_config.go` — file layout, ini/FPM composition, hash, atomic writes, extension-dir discovery.
- `internal/sites/php.go` — `PHPChange`, `PHPInfo`, defaults, extension resolution, `PHPExec`, `ValidatePHP`.
- `internal/sites/php_apply.go` — validate-then-swap, defaults application, migration, in-use checks, pruning.
- `internal/sites/vhosts.go`, `internal/sites/routes.go` — PHP vhost port and proxy route fields.
- `internal/proxy/router.go`, `handler.go`, `php.go` — `KindPHP`, PHP route fields, FastCGI request construction, static-file passthrough, path containment.
- `internal/vhost/vhost.go`, `nginx.go`, `apache.go` — PHP validation and templates (`fastcgi_pass`, `proxy:fcgi`).
- `internal/helper/vhosts.go` — accept and validate the PHP FastCGI port range; docroot containment for PHP.
- `internal/doctor/doctor.go`, `checks_php.go` — PHP build, config, port, and master checks.
- `internal/api/server.go`, `handlers_sites.go`, `handlers_php.go` — PHP routes, error mapping, OpenAPI.
- `internal/client/client.go` — PHP settings, defaults, migration, exec.
- `internal/cli/site.go`, `php.go`, `runtime.go` — `--kind php`, `site php`, `site ini`, `site ext`, `php --site`, `runtime migrate`.
- `internal/daemon/daemon.go` — wire PHP manifest, commands, doctor validator, and runtime catalog seam.
- `ui/src/lib/api.ts`, `types.ts`, `mock.ts` — PHP API types/functions.
- `ui/src/routes/Runtimes.svelte`, `AddSite.svelte`, `SiteDetail.svelte` — PHP builds, creation, settings.
- `ui/src/lib/components/PHPIniEditor.svelte`, `ExtensionToggles.svelte`, `PHPDefaultsDrawer.svelte` — editor and toggles.
- `build/php/` — `build.sh`, `smoke.sh`, `latest-patch.sh`, `extensions.lock`, `base-php.ini`, `README.md`.
- `cmd/phpcatalog/main.go` — generate one catalog entry from a published artifact and checksum.
- `cmd/php-smoke/main.go` — FPM startup and FastCGI smoke request used by CI.
- `.github/workflows/php.yml` — 8.2–8.5 matrix, SBOM, checksums, smoke gate, artifact upload, catalog fragment.
- `test/php/acceptance_test.go` — real-FPM acceptance when `/usr/local/php` exists.
- `docs/php.md`, `docs/vhosts.md`, `README.md` — user and operator docs.

---

### Task 1: PHP build pinning, store schema, revisioned catalog, and extension metadata

**Files:**
- Create: `internal/store/migrations/0003_php_build.sql`
- Modify: `internal/model/model.go`, `internal/model/model_test.go`
- Modify: `internal/store/store.go`, `internal/store/store_test.go`
- Modify: `internal/catalog/catalog.go`, `internal/catalog/catalog_test.go`
- Create: `internal/catalog/php.go`, `internal/catalog/php.json`, `internal/catalog/php_test.go`
- Modify: `internal/runtime/manager.go`, `internal/runtime/manager_test.go`

**Interfaces:**
- Produces model fields: `Site.PHPBuild string`, `Site.PHPIniRaw string`, `Site.PHPConfigHash string`.
- Produces:

```go
type PHPDefaults struct {
    Version    string            `json:"version"`
    Ini        map[string]string `json:"ini"`
    Extensions map[string]bool   `json:"extensions"`
}
type SitePHPConfig struct {
    SiteID     string            `json:"site_id"`
    Ini        map[string]string `json:"ini"`
    Extensions map[string]bool   `json:"extensions"`
    ConfigHash string            `json:"config_hash"`
    AppliedAt  time.Time         `json:"applied_at"`
}
```

- Produces store methods:

```go
func (s *Store) GetPHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error)
func (s *Store) SavePHPDefaults(ctx context.Context, d model.PHPDefaults) error
func (s *Store) ListPHPDefaults(ctx context.Context) ([]model.PHPDefaults, error)
func (s *Store) SaveSitePHPConfig(ctx context.Context, cfg model.SitePHPConfig) error
func (s *Store) GetSitePHPConfig(ctx context.Context, siteID string) (model.SitePHPConfig, error)
```

- Produces catalog types and functions:

```go
type Entry struct {
    ID, Kind, Version, Build, URL, SHA256 string
    Revision int
    Size     int64
}
type ID struct { Kind, Version string; Revision int }
func ParseID(raw string) (ID, error)
func FormatID(kind, version string, revision int) string
func (c Catalog) ForKindVersion(kind, version string) (Entry, bool) // newest revision
func (c Catalog) NewestForLine(kind, line string) (Entry, bool)
func LineOf(version string) string // "8.4.4" -> "8.4"

type ExtensionType string
const ( ExtensionShared ExtensionType = "extension"; ExtensionZend ExtensionType = "zend_extension" )
type Extension struct {
    Name     string        `json:"name"`
    Type     ExtensionType `json:"type"`
    SO       string        `json:"so"`
    Default  bool          `json:"default"`
    Standard bool          `json:"standard"`
    Versions []string      `json:"versions"`
    Requires []string      `json:"requires,omitempty"`
}
type ResolvedExtension struct {
    Name       string        `json:"name"`
    Type       ExtensionType `json:"type"`
    SO         string        `json:"so"`
    Required   bool          `json:"required"`
    RequiredBy []string      `json:"required_by,omitempty"`
}
type PHPVersionDefaults struct {
    Ini        map[string]string `json:"ini"`
    Extensions map[string]bool   `json:"extensions"`
}
type PHPManifest struct {
    Builtin    []string                      `json:"builtin"`
    Extensions []Extension                   `json:"extensions"`
    Versions   map[string]PHPVersionDefaults `json:"versions"`
}
func LoadPHP() (PHPManifest, error)
func ParsePHP(data []byte) (PHPManifest, error)
func (m PHPManifest) DefaultsFor(version string) (PHPVersionDefaults, bool)
func (m PHPManifest) Resolve(version string, overrides map[string]bool) ([]ResolvedExtension, error)
func (m PHPManifest) ResolveWithDefaults(version string, defaults, overrides map[string]bool) ([]ResolvedExtension, error)
```

- Produces runtime functions:

```go
func NewManagerWithCatalog(stores *store.Store, home string, client *http.Client, cat catalog.Catalog) *Manager
func (m *Manager) EnsureBuild(ctx context.Context, buildID string) (model.RuntimeBuild, error)
func (m *Manager) InstalledForLine(ctx context.Context, kind, line string) ([]model.RuntimeBuild, error)
```

- [ ] **Step 1: Write the failing store tests**

```go
func TestSitePHPFieldsRoundTrip(t *testing.T) {
    s := openTemp(t)
    site := model.Site{ID: "s1", Name: "blog", Domain: "blog.test", Kind: model.SitePHP,
        PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1",
        PHPIniRaw: "memory_limit = 512M\n", PHPConfigHash: "abc123",
        PHPExtensions: map[string]bool{"xdebug": true},
        PHPIni:        map[string]string{"memory_limit": "512M"}}
    if err := s.CreateSite(ctx, site); err != nil { t.Fatal(err) }
    got, err := s.GetSite(ctx, "s1")
    if err != nil { t.Fatal(err) }
    if got.PHPBuild != site.PHPBuild || got.PHPIniRaw != site.PHPIniRaw || got.PHPConfigHash != site.PHPConfigHash {
        t.Fatalf("PHP fields = %+v", got)
    }
}
func TestPHPDefaultsAndAppliedConfigRoundTrip(t *testing.T) {
    s := openTemp(t)
    if _, err := s.GetPHPDefaults(ctx, "8.4"); !errors.Is(err, ErrNotFound) {
        t.Fatalf("missing defaults error = %v", err)
    }
    d := model.PHPDefaults{Version: "8.4", Ini: map[string]string{"memory_limit": "256M"}, Extensions: map[string]bool{"xdebug": false}}
    if err := s.SavePHPDefaults(ctx, d); err != nil { t.Fatal(err) }
    got, err := s.GetPHPDefaults(ctx, "8.4")
    if err != nil || got.Ini["memory_limit"] != "256M" || got.Extensions["xdebug"] != false { t.Fatalf("%+v %v", got, err) }
    cfg := model.SitePHPConfig{SiteID: "s1", Ini: d.Ini, Extensions: d.Extensions, ConfigHash: "hash", AppliedAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
    if err := s.SaveSitePHPConfig(ctx, cfg); err != nil { t.Fatal(err) }
    gotCfg, err := s.GetSitePHPConfig(ctx, "s1")
    if err != nil || gotCfg.ConfigHash != "hash" || !gotCfg.AppliedAt.Equal(cfg.AppliedAt) { t.Fatalf("%+v %v", gotCfg, err) }
}
func TestMigration0003UpgradesExistingDatabase(t *testing.T) {
    // Build a database with migrations 0001 and 0002, insert a static site,
    // then Open it and assert php_build, php_ini_raw, and php_config_hash are "".
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store -run 'TestSitePHP|TestPHPDefaults|TestMigration0003' -v`
Expected: FAIL with `undefined: model.PHPDefaults` and missing columns.

- [ ] **Step 3: Implement migration, model, and store**

Create `0003_php_build.sql`:

```sql
ALTER TABLE sites ADD COLUMN php_build TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN php_ini_raw TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN php_config_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE site_php_config ADD COLUMN applied_at INTEGER NOT NULL DEFAULT 0;
```

Add the model fields and types exactly as in Interfaces. Extend `siteColumns`, insert arguments, update SQL, and `scanSite`. Store JSON for `PHPDefaults.Ini/Extensions` in `php_defaults`; store `SitePHPConfig` in `site_php_config` including `applied_at`; return `ErrNotFound` for missing rows. Preserve the existing forward-only migration runner and idempotent open.

- [ ] **Step 4: Run store tests**

Run: `go test ./internal/store`
Expected: PASS.

- [ ] **Step 5: Write the failing catalog and runtime tests**

```go
func TestParseIDRevisions(t *testing.T) {
    for _, tc := range []struct{ raw, kind, version string; rev int; ok bool }{
        {"node@24.22.1", "node", "24.22.1", 0, true},
        {"php@8.4.4-r1", "php", "8.4.4", 1, true},
        {"php@8.4.4", "", "", 0, false},
        {"node@24.22.1-r1", "", "", 0, false},
    } {
        got, err := ParseID(tc.raw)
        if tc.ok == (err != nil) { t.Fatalf("%q: err = %v", tc.raw, err) }
        if tc.ok && (got.Kind != tc.kind || got.Version != tc.version || got.Revision != tc.rev) { t.Fatalf("%q = %+v", tc.raw, got) }
    }
}
func TestCatalogSelectsNewestRevision(t *testing.T) {
    cat := catalogFor(t, phpEntry("8.4.4", 1), phpEntry("8.4.4", 2), phpEntry("8.4.3", 9))
    e, ok := cat.ForKindVersion("php", "8.4.4")
    if !ok || e.Revision != 2 { t.Fatalf("ForKindVersion = %+v", e) }
    e, ok = cat.NewestForLine("php", "8.4")
    if !ok || e.Revision != 2 || e.Version != "8.4.4" { t.Fatalf("NewestForLine = %+v", e) }
}
func TestPHPEntryRequiresPinnedHTTPSArtifact(t *testing.T) {
    // http URL, wrong filename, missing revision, and uppercase sha256 must fail.
}
func TestPHPManifestDefaultsAndDependencyOrder(t *testing.T) {
    m, err := LoadPHP()
    if err != nil { t.Fatal(err) }
    d, ok := m.DefaultsFor("8.4")
    if !ok || d.Ini["memory_limit"] != "256M" || d.Ini["opcache.revalidate_freq"] != "0" { t.Fatalf("%+v", d) }
    resolved, err := m.Resolve("8.4", map[string]bool{"redis": true, "xdebug": true})
    if err != nil { t.Fatal(err) }
    index := func(name string) int { /* return position or -1 */ }
    if index("igbinary") < 0 || index("igbinary") > index("redis") { t.Fatalf("order = %+v", resolved) }
    if index("xdebug") < 0 || index("opcache") < 0 { t.Fatalf("zend extensions = %+v", resolved) }
    if _, err := m.Resolve("8.4", map[string]bool{"imap": true}); err == nil { t.Fatal("imap must be unavailable on 8.4") }
    if _, err := m.Resolve("8.4", map[string]bool{"core": true}); err == nil { t.Fatal("built-in module must not be toggleable") }
}
func TestInstallKeepsPHPRevisionsSeparate(t *testing.T) {
    archive := fixtureData(t, "valid.tar.gz")
    srv := newFixtureServer(t, archive)
    cat := catalogFor(t, phpEntryFor(archive, srv.URL+"/php-8.4.4-r1-linux-x64.tar.gz", 1),
        phpEntryFor(archive, srv.URL+"/php-8.4.4-r2-linux-x64.tar.gz", 2))
    m, _, home, _ := newManagerWithCatalogFor(t, cat, srv.Client())
    b1, err := m.Install(ctx, "php@8.4.4-r1"); if err != nil { t.Fatal(err) }
    b2, err := m.Install(ctx, "php@8.4.4-r2"); if err != nil { t.Fatal(err) }
    if b1.Path == b2.Path { t.Fatalf("both revisions installed at %s", b1.Path) }
    if filepath.Base(b1.Path) != "8.4.4-r1-linux-x64" { t.Fatalf("path = %s", b1.Path) }
}
```

`phpEntry`/`phpEntryFor` are test helpers; `newManagerWithCatalogFor` wraps `NewManagerWithCatalog`.

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/catalog ./internal/runtime`
Expected: FAIL (`ParseID` undefined; catalog rejects PHP; revision paths collide).

- [ ] **Step 7: Implement the revisioned catalog, PHP metadata, and runtime paths**

`ParseID` accepts exactly `node@<major>.<minor>.<patch>` and `php@<major>.<minor>.<patch>-r<revision>` with revision ≥ 1. `FormatID` is the inverse. `Entry.ID` must equal `FormatID`; `Revision` is 0 for Node and ≥ 1 for PHP. `ForKindVersion` selects the highest revision for an exact version; `NewestForLine` compares numeric version components then revision. Keep `Find` exact.

PHP URL filename is `php-<version>-r<revision>-linux-x64.tar.gz`; Node remains `node-v<version>-linux-x64.tar.gz`. Keep HTTPS, no query/fragment, lowercase SHA-256, positive size.

Populate `php.json` with `builtin`, every standard and extra extension from Global Constraints, `Default` values (`ffi` false, extras false, `opcache` true, `xdebug` false), `Type` (opcache/xdebug `zend_extension`, others `extension`), `SO` (`<name>.so`), `Versions` (all four lines except `imap` = 8.2/8.3), and `Requires` (`redis`/`apcu` require `igbinary`; keep the dependency graph acyclic). `filter` appears in both the spec's standard list and its built-in list; classify it as built-in, do not emit an extension toggle for it, and keep the remaining standard list intact. Add the baseline ini values and the same seeded defaults for 8.2, 8.3, 8.4, 8.5. `Resolve` starts from per-version defaults, applies explicit overrides, auto-includes dependencies, rejects unknown names, built-ins, unsupported versions, and dependency cycles, and returns dependencies before dependents. `ResolveWithDefaults` starts from the embedded per-version defaults, overlays the supplied defaults map, then applies overrides with the same rules, so an edited `php_defaults` row changes every site that inherits.

`runtime.targetPath` uses `<version>-r<revision>-<build>` when revision > 0 and keeps `<version>-<build>` for Node. Update `checkBuildPath` to require the exact revisioned leaf derived from `catalog.ParseID(build.ID)`. `EnsureBuild` returns an intact installed row or installs the exact catalog entry. `InstalledForLine` returns installed builds for a line sorted newest version/revision first.

- [ ] **Step 8: Run catalog and runtime tests**

Run: `go test ./internal/catalog ./internal/runtime`
Expected: PASS.

- [ ] **Step 9: Run the full suite and commit**

Run: `go test ./...`
Expected: PASS (existing catalog/runtime tests updated for the new `Entry.Revision` field and PHP acceptance).

```bash
git add internal/model internal/store internal/catalog internal/runtime
git commit -m "feat: pin PHP builds by revision and add extension metadata"
```

---

### Task 2: Compose, validate, and preserve per-site `php.ini`

**Files:**
- Create: `internal/phpini/ini.go`, `internal/phpini/validate.go`, `internal/phpini/ini_test.go`

**Interfaces:**
- Produces:

```go
const (
    BeginDefaultsMarker   = "; BEGIN devstack version defaults"
    EndDefaultsMarker     = "; END devstack version defaults"
    BeginSiteMarker       = "; BEGIN devstack site overrides"
    EndSiteMarker         = "; END devstack site overrides"
    BeginExtensionsMarker = "; BEGIN devstack extensions (managed)"
    EndExtensionsMarker   = "; END devstack extensions (managed)"
)
type Extension struct { Name string; Type string; SO string }
type Config struct {
    Base            []byte
    VersionDefaults map[string]string
    Overrides       map[string]string
    Raw             string
    Extensions      []Extension
    ExtensionDir    string
    CAFile          string
}
func Compose(cfg Config) ([]byte, error)
func ExtractSiteSection(generated []byte) (string, error)
func Parse(data []byte) (map[string]string, error)
func ValidateOverrides(values map[string]string) error
func ValidateRaw(raw string) error
var ErrInvalidConfig = errors.New("phpini: invalid configuration")
type ValidationError struct { Line int; Message string }
func (e *ValidationError) Error() string
func (e *ValidationError) Unwrap() error
type LintError struct { Line int; Output string }
func (e *LintError) Error() string
func (e *LintError) Unwrap() error
type Runner interface { Run(ctx context.Context, name string, args, env []string) ([]byte, error) }
type ExecRunner struct{}
func Lint(ctx context.Context, runner Runner, phpBin, iniPath string) error
```

- [ ] **Step 1: Write the failing composition and validation tests**

```go
func TestComposeOrderManagedBlockAndCAAliases(t *testing.T) {
    got, err := Compose(Config{
        Base:            []byte("; build base\nmemory_limit = 128M\n"),
        VersionDefaults: map[string]string{"memory_limit": "256M", "date.timezone": "UTC"},
        Overrides:       map[string]string{"memory_limit": "512M"},
        Raw:             "; site comment\nauto_prepend_file = \"/srv/bootstrap.php\"\n",
        Extensions:      []Extension{{Name: "opcache", Type: "zend_extension", SO: "opcache.so"}},
        ExtensionDir:    "/opt/php/lib/php/extensions",
        CAFile:          "/home/u/.devstack/pki/ca-bundle.crt",
    })
    if err != nil { t.Fatal(err) }
    text := string(got)
    baseAt := strings.Index(text, "; build base")
    siteAt := strings.Index(text, BeginSiteMarker)
    extAt := strings.Index(text, BeginExtensionsMarker)
    if !(baseAt >= 0 && baseAt < siteAt && siteAt < extAt) { t.Fatalf("order wrong:\n%s", text) }
    for _, want := range []string{
        "memory_limit = 512M", "; site comment", "auto_prepend_file = \"/srv/bootstrap.php\"",
        "extension_dir = \"/opt/php/lib/php/extensions\"", "zend_extension=opcache.so",
        "curl.cainfo = \"/home/u/.devstack/pki/ca-bundle.crt\"", "openssl.cafile = \"/home/u/.devstack/pki/ca-bundle.crt\"",
    } { if !strings.Contains(text, want) { t.Fatalf("missing %q in:\n%s", want, text) } }
}
func TestComposeRejectsInjection(t *testing.T) {
    bad := []Config{
        {Overrides: map[string]string{"memory_limit": "1G\nextension=evil.so"}},
        {Overrides: map[string]string{"extension": "evil.so"}},
        {Overrides: map[string]string{"extension_dir": "/tmp"}},
        {Raw: BeginExtensionsMarker},
        {Raw: "zend_extension=evil.so\n"},
        {Raw: "extension_dir = \"/tmp\"\n"},
    }
    for _, cfg := range bad { if _, err := Compose(cfg); err == nil { t.Fatalf("accepted %+v", cfg) } }
}
func TestExtractSiteSectionRoundTrip(t *testing.T) {
    generated, _ := Compose(Config{Raw: "; keep\ncustom.foo = bar\n"})
    raw, err := ExtractSiteSection(generated)
    if err != nil || !strings.Contains(raw, "custom.foo = bar") { t.Fatalf("%q %v", raw, err) }
    again, err := Compose(Config{Raw: raw})
    if err != nil || !strings.Contains(string(again), "custom.foo = bar") { t.Fatalf("%v", err) }
}
func TestLintReportsLine(t *testing.T) {
    runner := fakeRunner{out: []byte("PHP: syntax error in /tmp/php.ini on line 12")}
    err := Lint(ctx, runner, "/opt/php/bin/php", "/tmp/php.ini")
    var lint *LintError
    if !errors.As(err, &lint) || lint.Line != 12 { t.Fatalf("err = %v", err) }
    if !errors.Is(err, ErrInvalidConfig) { t.Fatal("lint error must wrap ErrInvalidConfig") }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/phpini`
Expected: FAIL (undefined: Compose).

- [ ] **Step 3: Implement composition and validation**

`Compose` normalizes the base to a trailing newline, appends the defaults section (sorted keys), the site section (`Overrides` sorted, then `Raw` verbatim), and the managed extension section. The managed section starts with `extension_dir` and then one `extension=<so>` or `zend_extension=<so>` line per resolved extension in dependency order. Add `curl.cainfo` and `openssl.cafile` from `CAFile` only when the effective defaults/overrides do not already set them.

`ValidateOverrides` accepts keys matching `^[A-Za-z0-9_.-]+$`, rejects NUL/CR/LF in values, and rejects `extension`, `zend_extension`, `extension_dir`, and any devstack marker. `ValidateRaw` parses line by line: blank lines and `;` comments are preserved; `[section]` headers are accepted; a directive line must contain `=` and a valid key; managed markers and extension directives are rejected with a line-numbered `ValidationError`. `Parse` returns the last value for each key and ignores comments/sections. `Lint` runs the build's `php -c <ini> -r 'exit(0);'` with `PHP_INI_SCAN_DIR=` and parses `on line <n>` from output; a zero exit with no warning is success. Every validation failure in `Compose`, `ValidateOverrides`, `ValidateRaw`, and `Lint` wraps `ErrInvalidConfig`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/phpini`
Expected: PASS.

- [ ] **Step 5: Run the real PHP lint smoke check**

Run: `go test ./internal/phpini -run TestLintRealPHP -v`
Expected: PASS when `php` is on `PATH`; otherwise SKIP with a clear message.

- [ ] **Step 6: Commit**

```bash
git add internal/phpini
git commit -m "feat: compose and validate per-site php.ini"
```

---

### Task 3: FastCGI client and `php-fpm` configuration

**Files:**
- Create: `internal/fcgi/records.go`, `internal/fcgi/client.go`, `internal/fcgi/client_test.go`, `internal/fcgi/testserver_test.go`
- Create: `internal/phpfpm/config.go`, `internal/phpfpm/render.go`, `internal/phpfpm/validate.go`, `internal/phpfpm/config_test.go`

**Interfaces:**
- Produces FastCGI:

```go
type Request struct {
    Params     map[string]string
    Body       io.Reader
    BodyLength int64 // -1 when unknown
}
type Response struct {
    StatusCode int
    Status     string
    Header     http.Header
    Body       io.ReadCloser
}
type Client struct { /* ... */ }
func Dial(ctx context.Context, addr string) (*Client, error)
func (c *Client) Do(ctx context.Context, req Request) (*Response, error)
func (c *Client) Close() error
```

- Produces php-fpm config:

```go
type Config struct {
    SiteID       string
    BuildRoot    string
    PHPIni       string
    ListenHost   string
    ListenPort   int
    DocumentRoot string
    ErrorLog     string
    PIDFile      string
    User, Group  string
    Env          map[string]string
}
func Render(cfg Config) ([]byte, error)
func Argv(fpmBin, configPath, iniPath string) []string
var ErrConfigInvalid = errors.New("phpfpm: invalid configuration")
func Validate(ctx context.Context, fpmBin, configPath, iniPath string) error
```

- [ ] **Step 1: Write the failing FastCGI tests**

```go
func TestFastCGIClientRoundTrip(t *testing.T) {
    addr, stop := startFakeFCGI(t, func(params map[string]string, body []byte) (int, http.Header, []byte) {
        if params["SCRIPT_FILENAME"] != "/srv/index.php" || string(body) != "ping" { t.Errorf("%+v %q", params, body) }
        return 200, http.Header{"Content-Type": {"text/plain"}}, []byte("pong")
    })
    defer stop()
    c, err := Dial(ctx, addr); if err != nil { t.Fatal(err) }; defer c.Close()
    resp, err := c.Do(ctx, Request{Params: map[string]string{"SCRIPT_FILENAME": "/srv/index.php"}, Body: strings.NewReader("ping"), BodyLength: 4})
    if err != nil { t.Fatal(err) }; defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    if resp.StatusCode != 200 || string(body) != "pong" { t.Fatalf("%d %q", resp.StatusCode, body) }
}
func TestFastCGIChunksParamsAndStreamsBody(t *testing.T) {
    // 70 KiB param value and 256 KiB request body; fake server reassembles both
    // and returns a 200 with a 256 KiB response. Assert exact lengths and bytes.
}
func TestFastCGIReportsProtocolErrorAndStderr(t *testing.T) {
    // Fake server sends END_REQUEST with protocol status 2 and 128 bytes of
    // stderr. Do must return an error that includes the stderr text.
}
func TestFastCGIContextCancellationClosesConnection(t *testing.T) {
    // Fake server reads but never answers. Cancel the context; Do must return
    // context.Canceled within one second and the connection must be closed.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/fcgi`
Expected: FAIL (undefined: Dial).

- [ ] **Step 3: Implement FastCGI records and client**

Use version 1, request ID 1, responder role, no keep-connection. Encode `BEGIN_REQUEST`, `PARAMS` (name/value lengths 1 byte below 128, 4 bytes otherwise; split records at 65,535 bytes), `STDIN` (stream in 32 KiB chunks; send an empty terminator), and `ABORT_REQUEST` on cancellation. Decode `STDOUT`, `STDERR`, `END_REQUEST`. Parse CGI headers from the STDOUT stream (`Status:` optional, default 200), require a blank line, and expose the body as an `io.ReadCloser` fed by a pipe. Bound captured stderr to 64 KiB. Honour context cancellation by setting a deadline and closing the connection. Protocol status 0 is success; 1, 2, and 3 are errors naming the status.

- [ ] **Step 4: Run FastCGI tests**

Run: `go test ./internal/fcgi`
Expected: PASS.

- [ ] **Step 5: Write the failing php-fpm config tests**

```go
func TestRenderFPMConfigMatchesSpec(t *testing.T) {
    got, err := Render(Config{
        SiteID: "site", BuildRoot: "/opt/php", PHPIni: "/home/u/.devstack/sites/site/abc/php.ini",
        ListenHost: "127.0.0.1", ListenPort: 43001, DocumentRoot: "/home/u/blog/public",
        ErrorLog: "/proc/self/fd/2", PIDFile: "/home/u/.devstack/sites/site/abc/php-fpm.pid",
        Env: map[string]string{"APP_ENV": "local"},
    })
    if err != nil { t.Fatal(err) }
    for _, want := range []string{
        "pm = dynamic", "pm.max_children = 5", "pm.start_servers = 2",
        "pm.min_spare_servers = 1", "pm.max_spare_servers = 3",
        "catch_workers_output = yes", "clear_env = no",
        "listen = 127.0.0.1:43001", "env[APP_ENV] = \"local\"",
        "chdir = \"/home/u/blog/public\"", "daemonize = no",
    } { if !strings.Contains(string(got), want) { t.Fatalf("missing %q in:\n%s", want, got) } }
}
func TestRenderFPMConfigRejectsUnsafeValues(t *testing.T) {
    // port 41000, port 43999 valid, env value with newline, docroot with newline, empty listen host.
}
func TestValidateRealFPM(t *testing.T) {
    // skip when /usr/local/php/sbin/php-fpm is absent; render to a temp dir with
    // a temp php.ini and assert Validate returns nil.
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/phpfpm`
Expected: FAIL (undefined: Render).

- [ ] **Step 7: Implement php-fpm rendering and validation**

Render the `[global]` section with `pid`, `error_log`, `daemonize = no`, `log_level = notice`; the `[www]` section with `listen`, `pm = dynamic`, the exact five pool values, `catch_workers_output = yes`, `clear_env = no`, `chdir`, `security.limit_extensions = .php`, optional `user`/`group`, and sorted `env[key] = "value"` lines. Quote and escape paths and env values; reject NUL, CR/LF, non-IP listen hosts, ports outside 43000–43999, and relative paths. `Argv` returns `[fpmBin, "--nodaemonize", "--fpm-config", configPath, "-c", iniPath]`. `Validate` runs `fpmBin -t -y configPath -c iniPath` with `PHP_INI_SCAN_DIR=` and a 10s timeout, wraps failures in `ErrConfigInvalid`, and includes the trimmed output.

- [ ] **Step 8: Run provider tests**

Run: `go test ./internal/fcgi ./internal/phpfpm`
Expected: PASS; the real-FPM test SKIPs when the binary is absent.

- [ ] **Step 9: Commit**

```bash
git add internal/fcgi internal/phpfpm
git commit -m "feat: add FastCGI client and php-fpm configuration"
```

---

### Task 4: Local backend PHP process path and build resolution

**Files:**
- Modify: `internal/backend/backend.go`, `internal/backend/local.go`
- Create: `internal/backend/php.go`, `internal/backend/php_test.go`
- Modify: `internal/runtime/manager.go`, `internal/runtime/manager_test.go`
- Create: `internal/runtime/php_smoke.go`, `internal/runtime/php_smoke_test.go`
- Modify: `internal/backend/conformance/conformance.go`

**Interfaces:**
- Produces `SiteSpec` PHP fields: `PHPVersion`, `PHPBuild`, `PHPBuildPath`, `PHPIniPath`, `FPMConfigPath`, `PHPHealthScript string`.
- Produces an unexported package hook `var phpSmoke = smokePHPBuild`; `smokePHPBuild(ctx context.Context, buildPath string) error` runs `bin/php -v`, `sbin/php-fpm -v`, starts FPM on a free loopback port with a temporary ini/config/health script, sends one FastCGI request, and stops the process group.
- Produces `RuntimeProvider.EnsureBuild(ctx, buildID string) (model.RuntimeBuild, error)`.
- Produces `Backend.EnsureRuntimeBuild(ctx, buildID string) (model.RuntimeBuild, error)`.
- Produces staging:

```go
type PHPStager interface {
    PreparePHP(ctx context.Context, siteID, generation string, spec SiteSpec) (PreparedSite, error)
    StartPHP(ctx context.Context, siteID, generation string) (ProcessInfo, error)
    StopPHP(ctx context.Context, siteID, generation string) error
}
```

- [ ] **Step 1: Write the failing real-FPM backend tests**

```go
func TestLocalPHPStartsFPMAndServesHealth(t *testing.T) {
    root := systemPHP(t) // skip when /usr/local/php/sbin/php-fpm is absent
    b, home := newTestLocal(t)
    spec := phpTestSpec(t, home, root, "php@8.4.4-r1", freePort(t))
    if _, err := b.Prepare(ctx, spec); err != nil { t.Fatal(err) }
    if _, err := b.Start(ctx, spec.ID); err != nil { t.Fatal(err) }
    defer b.Stop(ctx, spec.ID, true)
    resp, err := fastcgiHealth(ctx, spec.FastCGIPort, spec.PHPHealthScript)
    if err != nil || resp != "ok" { t.Fatalf("%q %v", resp, err) }
}
func TestLocalPHPStageRunsAlongsideDefault(t *testing.T) {
    // Start the default generation; PreparePHP/StartPHP a second generation on
    // another port; both health probes return "ok"; StopPHP leaves the default
    // serving and Stop stops every generation.
}
func TestLocalPHPExecUsesGeneratedIni(t *testing.T) {
    // Exec with argv [".../bin/php", "-r", "echo ini_get('memory_limit');"] returns "256M".
}
func TestEnsureBuildInstallsPinnedRevision(t *testing.T) {
    // Fixture catalog + tarball; EnsureBuild returns the exact ID and path.
}
func TestPHPInstallRunsSmokeAndLeavesNoRuntimeOnFailure(t *testing.T) {
    // Fixture PHP entry pointing at valid.tar.gz. Replace phpSmoke with a
    // function returning errors.New("fpm failed"). Install must fail, List must
    // be empty, and runtimes/php/<version>-r<revision>-linux-x64 must not exist.
    // A second case with phpSmoke returning nil registers the build.
}
```

`phpTestSpec` writes base ini, generated ini, FPM config, and `health.php` with `<?php echo "ok";` under the temp home; `fastcgiHealth` dials the port and requests the script.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backend -run 'TestLocalPHP|TestEnsureBuild' -v`
Expected: FAIL (PHP sites are rejected by `Prepare`).

- [ ] **Step 3: Implement backend PHP support**

Add the PHP fields to `SiteSpec` and the two `EnsureBuild` methods. `Local.Prepare` for PHP validates `PHPBuild` is non-empty, `PHPBuildPath` contains executable `bin/php` and `sbin/php-fpm`, `PHPIniPath`/`FPMConfigPath`/`PHPHealthScript` are absolute clean paths, and `FastCGIPort` is 43000–43999. It stores the prepared spec under the site ID with generation `default`.

In `runtime.installEntry`, after extraction and manifest writing but before the atomic rename, call `phpSmoke` for `entry.Kind == "php"` against the temporary directory. On error remove the temporary directory and return the error without upserting a runtime row. `smokePHPBuild` writes a temporary `php.ini`, `php-fpm.conf`, and `health.php`, starts the extracted `sbin/php-fpm` with `--nodaemonize`, waits for TCP, sends a FastCGI GET for the health script, requires `200 ok`, and stops the process group; a missing binary or failed request is an error.

`Start` for PHP starts the supervisor with `ProcessSpec.ID = siteID` and argv from `phpfpm.Argv`, env from the site plus `PATH=<build>/bin:$PATH` and `PHP_INI_SCAN_DIR=`, working directory `<BackendPath>/<DocumentRoot>`, TCP readiness, the existing default restart policy, and logs writers with source `fpm`. After TCP readiness it polls the FastCGI health script every 100ms for up to 30s; any failure stops the process and returns a readiness error containing the last 200 log lines.

`PreparePHP`/`StartPHP` use `ProcessSpec.ID = siteID + "#" + generation`, except generation `default`, which uses the bare site ID so Plan 1's supervisor records and `Stop(siteID)` keep working. Store each generation in a per-site map and set the latest started generation as active. `StopPHP` stops one generation; stopping the active generation promotes the most recently started remaining generation back to active, so a failed swap leaves the old master as the reported process. `Stop` stops every generation, including the default. `Status` reports the active generation. `Exec` for PHP resolves the build's `bin/php`, prepends the build `bin` to `PATH`, sets `PHP_INI_SCAN_DIR=`, and runs in the site working directory. `Logs` stays unchanged.

- [ ] **Step 4: Run backend tests**

Run: `go test ./internal/backend ./internal/runtime`
Expected: PASS (real-FPM tests run here; they SKIP when the binary is absent).

- [ ] **Step 5: Run the conformance suite**

Run: `go test ./internal/backend/conformance ./internal/backend`
Expected: PASS; the conformance suite compiles with the new `EnsureRuntimeBuild` method and still covers Prepare/Start/Stop/Status/Logs/Exec/Health.

- [ ] **Step 6: Commit**

```bash
git add internal/backend internal/runtime
git commit -m "feat: run per-site php-fpm masters in the local backend"
```

---

### Task 5: PHP site creation, start/stop, deletion, and vhost publication

**Files:**
- Modify: `internal/sites/service.go`, `internal/sites/fakes_test.go`, `internal/sites/service_test.go`
- Create: `internal/sites/php_config.go`, `internal/sites/php.go`, `internal/sites/php_test.go`
- Modify: `internal/sites/routes.go`, `internal/sites/vhosts.go`, `internal/sites/serving_mode_test.go`

**Interfaces:**
- Produces:

```go
type CreateRequest struct {
    Name, Path, Kind, NodeVersion, Command, DocumentRoot string
    PHPVersion, PHPBuild string
    PHPExtensions map[string]bool
    PHPIni        map[string]string
    PHPIniRaw     string
    WWW, TLS, ForceHTTPS, SPAFallback, FrontController bool
    Env map[string]string
}
type UpdateRequest struct {
    Domain string
    WWW, TLS, ForceHTTPS, SPAFallback, FrontController *bool
    Command, DocumentRoot string
    Env map[string]string
}
type PHPCommands interface {
    Lint(ctx context.Context, phpBin, iniPath string) error
    ValidateFPM(ctx context.Context, fpmBin, configPath, iniPath string) error
}
type Deps struct {
    /* existing fields */
    PHP         PHPCommands
    PHPBackend  backend.PHPStager
    PHPManifest *catalog.PHPManifest
}
var ErrRuntimeMissing = errors.New("sites: PHP runtime is not installed")
func (s *Service) PHPExec(ctx context.Context, ref string, argv []string) (backend.Result, error)
func (s *Service) ValidatePHP(ctx context.Context, site model.Site) error
```

- [ ] **Step 1: Write the failing lifecycle tests**

```go
func TestCreatePHPSiteStartsFPMAndPinsBuild(t *testing.T) {
    svc, fakes := newTestService(t)
    build := fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
    site, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php",
        PHPVersion: "8.4", PHPBuild: build.ID, DocumentRoot: "."})
    if err != nil { t.Fatal(err) }
    if site.PHPBuild != build.ID || site.PHPConfigHash == "" { t.Fatalf("%+v", site) }
    if site.FastCGIPort < 43000 || site.FastCGIPort > 43999 { t.Fatalf("port = %d", site.FastCGIPort) }
    spec, ok := fakes.backend.lastPrepared()
    if !ok || spec.PHPBuild != build.ID || spec.PHPIniPath == "" || spec.FPMConfigPath == "" { t.Fatalf("%+v", spec) }
    if fakes.php.lintCalls == 0 || fakes.php.fpmCalls == 0 { t.Fatal("validation was not run") }
}
func TestCreatePHPRequiresInstalledBuild(t *testing.T) {
    svc, fakes := newTestService(t)
    if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4"}); !errors.Is(err, ErrRuntimeMissing) { t.Fatalf("err = %v", err) }
    if sites, _ := svc.List(ctx); len(sites) != 0 { t.Fatal("site persisted") }
}
func TestCreatePHPRollsBackWhenValidationFails(t *testing.T) {
    svc, fakes := newTestService(t); fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
    fakes.php.lintErr = &phpini.ValidationError{Line: 8, Message: "syntax error"}
    if _, err := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1"}); err == nil { t.Fatal("want error") }
    if sites, _ := svc.List(ctx); len(sites) != 0 { t.Fatal("site persisted") }
    if fakes.backend.started["site"] { t.Fatal("FPM started") }
}
func TestDeletePHPRemovesConfigDirAndPort(t *testing.T) {
    // Create, remember port and config dir, Delete, assert dir gone and
    // allocating the same port succeeds (or the allocator no longer reports it reserved).
}
func TestPHPVhostUsesFastCGIPort(t *testing.T) {
    svc, fakes := newTestService(t); fakes.config.ServingMode = "nginx"
    fakes.installPHPBuild("php@8.4.4-r1", "8.4.4")
    site, _ := svc.Create(ctx, CreateRequest{Name: "blog", Path: fakes.root, Kind: "php", PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1"})
    if got := fakes.vhosts.lastSite.BackendPort; got != site.FastCGIPort { t.Fatalf("vhost port = %d, want %d", got, site.FastCGIPort) }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sites -run 'TestCreatePHP|TestDeletePHP|TestPHPVhost' -v`
Expected: FAIL (PHP kind rejected).

- [ ] **Step 3: Implement PHP lifecycle and config files**

`newSite` accepts `php`; requires a version line in `8.2|8.3|8.4|8.5`; resolves `PHPBuild` from the installed store rows (exact ID, or newest `Version`/revision matching the line when empty), verifies the build kind is `php`, verifies `catalog.LineOf(build.Version)` matches, validates the document root, rejects PHP-only fields on other kinds, rejects `FrontController` on static/Node, resolves extensions with `PHPManifest.Resolve`, and validates ini/raw with `phpini`. `Catalog`-missing builds return `ErrRuntimeMissing`.

`composePHPFiles(site, build)` returns an in-memory `phpFiles` with a SHA-256 config hash over the generated ini and FPM config. It reads the build base `<build>/etc/php.ini` when present, version defaults from `php_defaults` (seeding from `PHPManifest.DefaultsFor` on first use), resolves extensions with `ResolveWithDefaults(version, defaults.Extensions, site.PHPExtensions)` so edited version defaults take effect, composes ini, discovers `extension_dir` under `<build>/lib/php/extensions` (direct `.so` files or one architecture subdirectory), and renders FPM with the site's FastCGI port. `writePHPFiles` writes an immutable generation at `sites/<id>/<hash>/` with `php.ini`, `php-fpm.conf`, and `health.php` in a `0700` directory with `0600` config files and `0644` health script, atomically. `commitPHPFiles` copies the active generation to `sites/<id>/php.ini`, `sites/<id>/php-fpm.conf`, and `sites/<id>/health.php` after a successful create/start/apply, so the documented per-site path always describes the active master while the running master keeps its immutable generation file.

`Create` allocates a FastCGI port for PHP before hosts sync, composes/validates/writes configs, saves the applied `SitePHPConfig` snapshot, then persists the row and starts the backend. Every failure path rolls back the row, certificate, hosts, vhosts, config directory, backend process, and ports. `startLocked` for PHP reuses the stored `PHPConfigHash` when the files exist; otherwise it recomposes, validates, updates the hash, and saves the applied config snapshot. `Delete` and `deleteExternalLocked` stop the FPM master, release both ports, remove `sites/<id>` after the row is deleted, and prune only that directory. `restoreExternalDelete` restart logic is extended to PHP (prepare/start the old build and config), alongside the existing Node branch.

`PHPExec` resolves the site, ensures its current config files exist (composing them if needed), calls `Backend.Prepare`, then `Backend.Exec` with the build's `bin/php -c <ini>`; it works for stopped sites and never starts a master. Forwarded argv is rejected when it contains `-c`, `--php-ini`, `-n`, or `--no-php-ini`, so a diagnostic run cannot bypass the generated ini.

`UpdateRequest.FrontController` is accepted for PHP only and updates the row, routes, and vhosts without restarting FPM. A PHP document-root update is validated with the same relative/exists/escape check as static sites. `proxyKind` returns `proxy.KindPHP`; `rebuildRoutesLocked` uses `FastCGIPort` as the upstream and sets `DocumentRoot`/`FrontController` on the route. `vhostSiteSpecLocked` uses `FastCGIPort` for PHP. `absoluteDocumentRoot` resolves `.` to `BackendPath` for PHP as well as static.

`NewService` installs a default `PHPCommands` when `Deps.PHP` is nil: it runs `phpini.Lint` with an `exec`-backed runner and `phpfpm.Validate` with the build's `sbin/php-fpm` and a 10s timeout. A nil `PHPManifest` is loaded lazily with `catalog.LoadPHP()` on the first PHP operation; an invalid embedded manifest is returned as an actionable error rather than panicking. Add `installPHPBuild(id, version)` to `fakes_test.go` (upserts a `model.RuntimeBuild`), a `fakePHPCommands` with `lintErr`/`fpmErr` and call counters, and the two `PHPStager` methods on `fakeBackend`.

- [ ] **Step 4: Run sites tests**

Run: `go test ./internal/sites`
Expected: PASS (all Plan 1 tests plus PHP lifecycle tests).

- [ ] **Step 5: Commit**

```bash
git add internal/sites
git commit -m "feat: create and run per-site PHP sites"
```

---

### Task 6: Validate-then-swap for PHP version, build, ini, and extensions

**Files:**
- Create: `internal/sites/php_apply.go`, `internal/sites/php_apply_test.go`
- Modify: `internal/sites/php.go`, `internal/sites/service.go`, `internal/sites/fakes_test.go`

**Interfaces:**
- Produces:

```go
type PHPChange struct {
    Version    string
    Build      string
    Extensions map[string]bool
    Ini        map[string]string
    IniRaw     *string
}
type PHPInfo struct {
    Site                model.Site             `json:"site"`
    Build               model.RuntimeBuild     `json:"build"`
    Defaults            model.PHPDefaults      `json:"defaults"`
    Extensions          []catalog.ResolvedExtension `json:"extensions"`
    AvailableExtensions []catalog.Extension    `json:"available_extensions"`
    Builtins            []string               `json:"builtins"`
    Ini                 map[string]string      `json:"ini"`
    IniRaw              string                 `json:"ini_raw"`
    GeneratedIni        string                 `json:"generated_ini"`
    FPMConfig           string                 `json:"fpm_config"`
    ConfigHash          string                 `json:"config_hash"`
}
type PHPMigrationRequest struct { FromVersion, ToVersion, ToBuild string }
type PHPMigrationResult struct { Site string `json:"site"`; OK bool `json:"ok"`; Error string `json:"error,omitempty"` }
type PHPDefaultsResult struct { Defaults model.PHPDefaults `json:"defaults"`; Sites []PHPMigrationResult `json:"sites"` }
var ErrRuntimeInUse = errors.New("sites: PHP runtime is in use")
type RuntimeInUseError struct { BuildID string; Sites []string }
func (e *RuntimeInUseError) Error() string
func (e *RuntimeInUseError) Unwrap() error
func (s *Service) ApplyPHP(ctx context.Context, ref string, change PHPChange) (model.Site, error)
func (s *Service) PHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error)
func (s *Service) SavePHPDefaults(ctx context.Context, d model.PHPDefaults) (PHPDefaultsResult, error)
func (s *Service) MigratePHP(ctx context.Context, req PHPMigrationRequest) ([]PHPMigrationResult, error)
func (s *Service) CheckRuntimeRemoval(ctx context.Context, buildID string) error
```

- [ ] **Step 1: Write the failing apply tests**

```go
func TestApplyPHPIniChangeOnlyAffectsOneSite(t *testing.T) {
    // Create two PHP sites with the same build. Apply memory_limit=512M to the
    // first. Assert its PHPConfigHash changed, the second's did not, and only
    // the first staged a new FPM generation.
}
func TestApplyPHPVersionSwitchPromotesAndDrains(t *testing.T) {
    // Seed r1 and r2 builds. Apply r2. Assert the site row has r2, the route
    // upstream is the new FastCGI port, the r2 generation started, and the r1
    // generation was stopped only after the row and route were updated.
}
func TestApplyPHPVersionSwitchRollsBackOnHealthFailure(t *testing.T) {
    // fakes.backend.startPHPErr = errors.New("health timeout"); Apply r2 fails;
    // assert site.PHPBuild and FastCGIPort are unchanged, the old generation is
    // still active, and the new port can be allocated again.
}
func TestApplyPHPInvalidIniLeavesOldMasterServing(t *testing.T) {
    // fakes.php.lintErr = &phpini.LintError{Line: 4, Output: "syntax error"};
    // Apply fails with an error containing "line 4"; old generation active.
}
func TestApplyPHPVhostFailureKeepsOldMaster(t *testing.T) {
    // Running site + nginx mode; fakes.vhosts.err = errors.New("bad config");
    // Apply fails; row, route, and old generation unchanged.
}
func TestSavePHPDefaultsRollsBackPerSite(t *testing.T) {
    // Two running sites on 8.4; fake FPM validation fails for the second.
    // SavePHPDefaults returns one OK and one failed; the failed site keeps its
    // old PHPConfigHash; the defaults row is saved. A second case changes the
    // xdebug default to true and asserts both successful sites now load it.
}
func TestMigratePHPAndCheckRuntimeRemoval(t *testing.T) {
    // Migrate 8.4 -> 8.5 with seeded builds; results list every site.
    // CheckRuntimeRemoval(r1) blocks with the site names while one site pins r1.
    // After migrating, removing r1 succeeds (or returns nil from the checker).
}
func TestConcurrentApplySerializes(t *testing.T) {
    // Two goroutines call ApplyPHP for the same site; one completes, the other
    // observes the new config. No duplicate stage generations or leaked ports.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sites -run 'TestApplyPHP|TestSavePHPDefaults|TestMigratePHP|TestCheckRuntime' -v`
Expected: FAIL (undefined: ApplyPHP).

- [ ] **Step 3: Implement apply, defaults, migration, and in-use policy**

`ApplyPHP` merges the change with the current row: empty version/build keeps current; an empty build with a new version selects the newest installed build for that line; an explicit build must be installed and its `catalog.LineOf` must match the target version. It validates extensions and ini, computes the new files/hash, and for a stopped site writes the new config directory and updates the row, `site_php_config`, and `PHPConfigHash` without starting a master. `checkDeps` requires `Deps.PHPBackend` only when a running PHP site needs a staged master; a stopped site needs only config validation and the store.

For a running site, it writes the new config directory, runs `PHPCommands.Lint` and `ValidateFPM`, allocates a new FastCGI port, calls `s.deps.PHPBackend.PreparePHP`/`StartPHP` with generation = config hash, and waits for the backend health probe. Only then does it update the site row (`PHPVersion`, `PHPBuild`, `PHPIni`, `PHPExtensions`, `PHPIniRaw`, `PHPConfigHash`, `FastCGIPort`), save the applied config, rebuild routes, and sync vhosts. After the route and vhost publish succeeds it calls `commitPHPFiles` (best effort; a copy failure is recorded as an event and does not roll back the healthy new master), stops the old generation, releases the old port, and prunes all but the current and previous config directories. On any failure before publish it stops the new generation (which promotes the old generation back to active), releases the new port, removes the uncommitted generation directory, restores the old row/route/vhost, and returns the validation output. A failure to stop the drained old generation after publish is reported but does not roll back the new healthy master; the next stop/start removes the leftover. The service mutex serializes applies.

`PHPInfo` returns the row, installed build, version defaults, effective extensions with dependencies, available extensions for the version, built-ins, site ini/raw, generated ini and FPM text, and config hash. If the current config files are missing it composes them in memory without writing.

`PHPDefaults` seeds from `PHPManifest.DefaultsFor` when the store row is absent. `SavePHPDefaults` validates the version and maps, fills any missing ini/extension entries from the embedded version defaults so a partial payload cannot erase the baseline, saves, then applies the new defaults to every **running** PHP site on that line by calling the internal apply path with the site's current overrides; each site rolls back independently and its result is reported. `MigratePHP` resolves the target build (explicit or newest installed), lists sites whose `PHPVersion` equals `FromVersion`, and applies each one, returning per-site results.

`CheckRuntimeRemoval` reads the build and lists sites. A site blocks removal when `site.PHPBuild == buildID`; a legacy site with an empty `PHPBuild` blocks when `site.PHPVersion == catalog.LineOf(build.Version)`. It returns `*RuntimeInUseError` with sorted site names. `ErrRuntimeMissing` is returned when no installed build matches a line.

`ValidatePHP` composes the current files in a temporary directory under `sites/<id>`, runs both validators, and removes the directory; Doctor uses it without starting a master.

- [ ] **Step 4: Run sites tests**

Run: `go test ./internal/sites -race`
Expected: PASS, race detector clean.

- [ ] **Step 5: Commit**

```bash
git add internal/sites
git commit -m "feat: apply PHP version, ini, and extension changes with rollback"
```

---

### Task 7: Built-in proxy PHP route and FastCGI transport

**Files:**
- Modify: `internal/proxy/router.go`, `internal/proxy/handler.go`
- Create: `internal/proxy/php.go`, `internal/proxy/php_test.go`
- Modify: `internal/sites/routes.go`, `internal/sites/service_test.go`, `internal/proxy/router_test.go`

**Interfaces:**
- Produces:

```go
const KindPHP Kind = "php"
type Route struct {
    SiteID, Host string
    Kind Kind
    Upstream string
    DocumentRoot string
    FrontController bool
    ForceHTTPS, TLS bool
}
type fcgiDialer func(ctx context.Context, addr string) (fcgiClient, error)
type fcgiClient interface {
    Do(ctx context.Context, req fcgi.Request) (*fcgi.Response, error)
    Close() error
}
```

`NewRouter` sets `fcgiDial = fcgi.Dial`; proxy tests replace it. No exported test seam is added to the public API.

- [ ] **Step 1: Write the failing proxy tests**

```go
func TestPHPProxyFrontControllerParams(t *testing.T) {
    root := t.TempDir()
    if err := os.WriteFile(filepath.Join(root, "index.php"), []byte("<?php echo 'ok';"), 0o644); err != nil { t.Fatal(err) }
    r := NewRouter()
    r.Replace(Snapshot{Routes: map[string]Route{"blog.test": {
        SiteID: "s1", Host: "blog.test", Kind: KindPHP, Upstream: "127.0.0.1:43001",
        DocumentRoot: root, FrontController: true, TLS: true,
    }}})
    r.fcgiDial = func(context.Context, string) (fcgiClient, error) { return captureClient(t), nil }
    // GET /users/42?x=1 must send SCRIPT_FILENAME=<root>/index.php, SCRIPT_NAME=/index.php,
    // PATH_INFO=/users/42, QUERY_STRING=x=1, REQUEST_URI=/users/42?x=1, HTTPS=on.
}
func TestPHPProxyServesExistingFilesDirectly(t *testing.T) {
    // index.html and style.css are returned without a FastCGI call; a .php file
    // is executed with SCRIPT_FILENAME pointing at that file.
}
func TestPHPProxyDirectoryPrefersIndexPHPThenIndexHTML(t *testing.T) { /* ... */ }
func TestPHPProxyRejectsTraversalAndSymlinkEscape(t *testing.T) {
    // /../secret, /%2e%2e/secret, /link-outside, and /.env must be 404 with no
    // FastCGI call and no file contents.
}
func TestPHPProxyNeverServesPHPSource(t *testing.T) {
    // app.PHP, app.php~, app.php.bak, and app.php.orig must never return the
    // source bytes: they are routed to FPM when the extension folds to .php,
    // otherwise denied with 404.
}
func TestPHPProxyPreservesPathInfoForPHPFile(t *testing.T) {
    // /app.php/extra/path executes app.php with PATH_INFO=/extra/path.
}
func TestPHPProxyStripsHSTSAndReturnsBranded502(t *testing.T) {
    // FPM response with Strict-Transport-Security is stripped; a dial failure
    // returns 502 with the site name and no HSTS.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy -run 'TestPHPProxy' -v`
Expected: FAIL (undefined: KindPHP).

- [ ] **Step 3: Implement PHP routing and request construction**

Add `KindPHP` and the route fields. `Router.serveHTTP` dispatches `KindPHP` to `servePHP`; `reverseProxy` rejects PHP. Build FastCGI params: all CGI/1.1 standard values, `SCRIPT_FILENAME`, `SCRIPT_NAME`, `PATH_INFO`, `DOCUMENT_ROOT`, `REQUEST_URI`, `QUERY_STRING`, `REQUEST_METHOD`, `SERVER_NAME` (host without port), `SERVER_PORT`, `HTTPS=on` for TLS, `REMOTE_ADDR`, `REMOTE_PORT`, `CONTENT_TYPE`, `CONTENT_LENGTH`, `HTTP_*` headers (skipping hop-by-hop headers plus `Content-Type` and `Content-Length`, which have dedicated params), and `HTTP_X_FORWARDED_FOR/PROTO/HOST`. Body streams from `r.Body`; unknown length uses `BodyLength = -1`.

Resolve the request path with a dedicated function: reject NUL and any `..` segment; try the full file; if it is a directory, try `index.php` then `index.html`; if the path matches `(.+\.php)(/.*)?` and the script exists, execute it with `PATH_INFO`; if no file exists and `FrontController` is true, execute `<docroot>/index.php` with the cleaned path as `PATH_INFO` and `/index.php` as `SCRIPT_NAME`; otherwise 404. Resolve symlinks with `filepath.EvalSymlinks` and require the result to remain under the resolved document root. Any path whose lowercased final extension is `.php` is routed to FPM, never served as static; names ending in `.php~`, `.php.bak`, `.php.orig`, `.php.save`, or `.php.old` are denied with 404 so an editor backup cannot disclose source. Serve other existing files with `http.ServeContent`, `Cache-Control: no-store`, no directory listing, and dotfile denial.

Copy the FastCGI response status, headers, and body; drop hop-by-hop headers, the CGI `Status` header, and `Strict-Transport-Security`; suppress the body for HEAD. A dial/read error returns the existing branded 502 page naming the site. Use a 120s request deadline.

- [ ] **Step 4: Run proxy tests**

Run: `go test ./internal/proxy`
Expected: PASS (Plan 1 routing/TLS tests plus PHP tests).

- [ ] **Step 5: Update site routes and run the sites tests**

`proxyKind` maps `model.SitePHP` to `proxy.KindPHP`. `rebuildRoutesLocked` publishes PHP routes only when `FastCGIPort > 0`, uses `net.JoinHostPort("127.0.0.1", ...)`, and sets `DocumentRoot` to `absoluteDocumentRoot(site)` and `FrontController`. Add a sites test asserting the route's upstream and docroot.

Run: `go test ./internal/sites ./internal/proxy`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/proxy internal/sites/routes.go internal/sites/service_test.go
git commit -m "feat: proxy PHP requests over FastCGI"
```

---

### Task 8: PHP nginx/Apache vhosts, helper validation, and Doctor checks

**Files:**
- Modify: `internal/vhost/vhost.go`, `internal/vhost/nginx.go`, `internal/vhost/apache.go`
- Modify: `internal/vhost/nginx_test.go`, `internal/vhost/apache_test.go`, `internal/vhost/vhost_test.go`
- Create: `internal/vhost/testdata/nginx/php-front.conf`, `internal/vhost/testdata/apache/php.conf`
- Modify: `internal/helper/vhosts.go`, `internal/helper/vhosts_test.go`
- Create: `internal/doctor/checks_php.go`, `internal/doctor/checks_php_test.go`
- Modify: `internal/doctor/doctor.go`, `internal/doctor/checks.go`, `internal/doctor/doctor_test.go`

**Interfaces:**
- Produces vhost validation: PHP running requires `BackendPort` in 43000–43999; PHP stopped may use 0 or a valid port; PHP requires a document root when running; `FrontController` is allowed only for PHP.
- Produces `vhost.ErrPHPUnsupported` removal; PHP is now a supported kind.
- Produces Doctor:

```go
type PHPValidator interface {
    ValidatePHP(ctx context.Context, site model.Site) error
}
type Deps struct { /* existing fields */ PHP PHPValidator }
```

Doctor check IDs: `php-builds`, `php-config`, `php-ports`, `php-master`.

- [ ] **Step 1: Write the failing vhost tests**

```go
func TestRenderNginxPHPFrontController(t *testing.T) {
    s := phpSite()
    s.FrontController = true
    got, err := RenderSite(ServerNginx, s)
    if err != nil { t.Fatal(err) }
    for _, want := range []string{
        "location ~ ^(.+\\.php)(/.*)?$", "fastcgi_pass 127.0.0.1:43001;",
        "fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;",
        "fastcgi_param PATH_INFO $fastcgi_path_info;",
        "location @devstack_front_controller",
        "fastcgi_param SCRIPT_FILENAME \"/home/u/blog/public/index.php\";",
        "fastcgi_param PATH_INFO $uri;", "try_files $uri $uri/ @devstack_front_controller;",
    } { if !strings.Contains(string(got), want) { t.Fatalf("missing %q in:\n%s", want, got) } }
}
func TestRenderApachePHP(t *testing.T) {
    s := phpSite()
    s.FrontController = true
    got, err := RenderSite(ServerApache, s)
    if err != nil { t.Fatal(err) }
    for _, want := range []string{
        "SetHandler \"proxy:fcgi://127.0.0.1:43001\"",
        "DirectoryIndex index.php index.html",
        "RewriteRule ^(.*)$ /index.php/$1 [L,QSA]",
    } { if !strings.Contains(string(got), want) { t.Fatalf("missing %q in:\n%s", want, got) } }
}
func TestPHPVhostDeniesSourceBackups(t *testing.T) {
    for _, server := range []Server{ServerNginx, ServerApache} {
        got, err := RenderSite(server, phpSite())
        if err != nil { t.Fatal(err) }
        text := string(got)
        if server == ServerNginx && !strings.Contains(text, `location ~* \.php(?:~|\.(?:bak|orig|save|old|swp|tmp))$`) { t.Fatalf("nginx source backup denial missing:\n%s", text) }
        if server == ServerApache && !strings.Contains(text, `(?i)\.php(~|\.(bak|orig|save|old|swp|tmp))$`) { t.Fatalf("apache source backup denial missing:\n%s", text) }
    }
}
func TestValidateSiteSpecPHP(t *testing.T) {
    valid := phpSite()
    if err := ValidateSiteSpec(valid); err != nil { t.Fatal(err) }
    badPort := valid; badPort.BackendPort = 41001
    if err := ValidateSiteSpec(badPort); err == nil { t.Fatal("accepted Node port for PHP") }
    stopped := valid; stopped.DesiredState = model.DesiredStopped; stopped.BackendPort = 0
    if err := ValidateSiteSpec(stopped); err != nil { t.Fatal(err) }
    noRoot := valid; noRoot.DocumentRoot = ""
    if err := ValidateSiteSpec(noRoot); err == nil { t.Fatal("running PHP site has no document root") }
    staticFront := staticSite(); staticFront.FrontController = true
    if err := ValidateSiteSpec(staticFront); err == nil { t.Fatal("static front controller accepted") }
}
```

Add `phpSite()` returning `SiteSpec{ID:"php1", Name:"blog", Domain:"blog.test", Kind:model.SitePHP, DocumentRoot:"/home/u/blog/public", BackendPort:43001, DesiredState:model.DesiredRunning, TLS:true, CertPEM:testCertPEM, KeyPEM:testKeyPEM}`. Replace the existing `ErrPHPUnsupported` assertion in `vhost_test.go` with the valid PHP case.

- [ ] **Step 2: Run vhost tests to verify they fail**

Run: `go test ./internal/vhost -run 'TestRenderNginxPHP|TestRenderApachePHP|TestValidateSiteSpecPHP' -v`
Expected: FAIL (`ErrPHPUnsupported`).

- [ ] **Step 3: Implement PHP templates and validation**

Nginx: `root`, `index index.php index.html`, `autoindex off`; non-front `location / { try_files $uri $uri/ =404; }`; front `try_files $uri $uri/ @devstack_front_controller`. Add the `.php` regex location with `fastcgi_split_path_info`, `try_files $fastcgi_script_name =404`, `fastcgi_pass`, `fastcgi_index index.php`, `fastcgi_read_timeout 120s`, `fastcgi_buffering off`, and explicit CGI params (`SCRIPT_FILENAME`, `SCRIPT_NAME`, `PATH_INFO`, `DOCUMENT_ROOT`, `QUERY_STRING`, `REQUEST_METHOD`, `REQUEST_URI`, `SERVER_PROTOCOL`, `GATEWAY_INTERFACE`, `HTTPS`). The front-controller named location uses the fixed index path and `PATH_INFO $uri`. Reuse the existing dotfile and `/_devstack` denial.

Apache: `DocumentRoot`, `DirectoryIndex index.php index.html`, `<Directory>` with `Options -Indexes`, `AllowOverride None`, `Require all granted`; `<FilesMatch "\.php$"> SetHandler "proxy:fcgi://127.0.0.1:<port>" </FilesMatch>`; front-controller rewrite conditions `!-f`, `!-d`, and `RewriteRule ^(.*)$ /index.php/$1 [L,QSA]`; dotfile and `/_devstack` denial. Add a source-backup denial (`<FilesMatch "(?i)\.php(~|\.(bak|orig|save|old|swp|tmp))$"> Require all denied </FilesMatch>`) in both servers, and add the nginx equivalent `location ~* \.php(?:~|\.(?:bak|orig|save|old|swp|tmp))$ { deny all; }` before the static/PHP locations. Use quoted paths.

Update `ValidateSiteSpec` and helper `validateSite` for PHP. The helper resolves and contains the PHP document root exactly like static, and accepts only 43000–43999 for a running PHP `BackendPort`. Golden files cover nginx front-controller PHP and Apache PHP.

- [ ] **Step 4: Run vhost and helper tests**

Run: `go test ./internal/vhost ./internal/helper`
Expected: PASS; helper tests that need a real nginx/Apache still skip when absent.

- [ ] **Step 5: Write the failing Doctor tests**

```go
func TestDoctorReportsPHPConfigFailure(t *testing.T) {
    svc, fakes := newTestDoctor(t)
    fakes.addPHPSite(t, "php@8.4.4-r1", "8.4.4")
    fakes.php.err = errors.New("php.ini line 9: syntax error")
    rep, _ := svc.Run(ctx)
    if hasCheck(rep, "php-config", StatusOK) { t.Fatalf("%+v", rep.Checks) }
}
func TestDoctorSkipsPHPWhenUnused(t *testing.T) {
    svc, _ := newTestDoctor(t)
    rep, _ := svc.Run(ctx)
    for _, id := range []string{"php-builds", "php-config", "php-ports", "php-master"} {
        if !hasCheck(rep, id, StatusSkipped) { t.Fatalf("%s not skipped: %+v", id, rep.Checks) }
    }
}
```

- [ ] **Step 6: Implement Doctor PHP checks**

`php-builds` lists installed PHP runtimes and verifies `bin/php`, `sbin/php-fpm`, and an extension directory exist; if PHP sites exist but no build is installed, it fails. `php-config` calls `ValidatePHP` for every PHP site and reports the first three failures. `php-ports` verifies every running PHP site has a unique 43000–43999 port. `php-master` dials `127.0.0.1:<port>` for running sites with a 500ms timeout. All four checks are skipped when no PHP build and no PHP site exist. Remediation names `devstack site restart <name>`, `devstack runtime install <id>`, or `devstack site php <name> <version>` as appropriate. `Repair` does not rewrite site configs; it re-runs the checks.

Wire `Deps.PHP` in the daemon to the site service (Task 11) and add `fakes.php` plus `addPHPSite` to the doctor test fakes.

- [ ] **Step 7: Run Doctor tests**

Run: `go test ./internal/doctor`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/vhost internal/helper internal/doctor
git commit -m "feat: add PHP vhost templates and Doctor checks"
```

---

### Task 9: REST API, typed client, and CLI for PHP

**Files:**
- Modify: `internal/api/server.go`, `internal/api/handlers_sites.go`, `internal/api/api_test.go`, `api/openapi.yaml`
- Create: `internal/api/handlers_php.go`
- Modify: `internal/client/client.go`, `internal/client/client_test.go`
- Modify: `internal/cli/root.go`, `internal/cli/site.go`, `internal/cli/runtime.go`, `internal/cli/cli_test.go`
- Create: `internal/cli/php.go`
- Modify: `internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`

**Interfaces:**
- Produces API routes:

```text
GET    /api/v1/sites/{ref}/php
PUT    /api/v1/sites/{ref}/php
POST   /api/v1/sites/{ref}/php/exec
GET    /api/v1/php/extensions?version=8.4
GET    /api/v1/php/defaults/{version}
PUT    /api/v1/php/defaults/{version}
POST   /api/v1/php/migrate
```

- Produces request shapes:

```go
type phpSettingsRequest struct {
    Version    string            `json:"version"`
    Build      string            `json:"build"`
    Extensions map[string]bool   `json:"extensions"`
    Ini        map[string]string `json:"ini"`
    IniRaw     *string           `json:"ini_raw"`
}
type phpExecRequest struct { Argv []string `json:"argv"` }
type phpExecResponse struct {
    ExitCode int    `json:"exit_code"`
    Stdout   string `json:"stdout"`
    Stderr   string `json:"stderr"`
}
type phpMigrationRequest struct {
    FromVersion string `json:"from_version"`
    ToVersion   string `json:"to_version"`
    ToBuild     string `json:"to_build"`
}
```

- Produces client methods:

```go
func (c *Client) PHPInfo(ctx context.Context, ref string) (sites.PHPInfo, error)
func (c *Client) ApplyPHP(ctx context.Context, ref string, change sites.PHPChange) (model.Site, error)
func (c *Client) PHPExtensions(ctx context.Context, version string) ([]catalog.Extension, error)
func (c *Client) PHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error)
func (c *Client) SavePHPDefaults(ctx context.Context, d model.PHPDefaults) (sites.PHPDefaultsResult, error)
func (c *Client) MigratePHP(ctx context.Context, req sites.PHPMigrationRequest) ([]sites.PHPMigrationResult, error)
func (c *Client) PHPExec(ctx context.Context, ref string, argv []string) (backend.Result, error)
```

- Produces CLI commands: `site add --kind php --php 8.4 [--php-build php@8.4.4-r1] [--front-controller]`, `site php <name> [version] [--build ID] [--list]`, `site ini <name> [--set k=v]... [--unset k]... [--raw TEXT|--raw-file PATH] [--apply]`, `site ext <name> [--enable ext]... [--disable ext]... [--inherit ext]...`, `runtime migrate <from> --to <to>`, `php --site <name> -- <args...>`.
- API error code `PHP_VERSION_IN_USE` (409); `PHP_CONFIG_INVALID` (422) includes `line` and `output` details.

- [ ] **Step 1: Write the failing API tests**

```go
func TestAPIPHPApplyRequiresCSRFAndReturnsConfigError(t *testing.T) {
    h, fake := newTestAPIWithFakes(t)
    rec := httptest.NewRecorder()
    h.ServeHTTP(rec, httptest.NewRequest("PUT", "http://127.0.0.1/api/v1/sites/blog/php", strings.NewReader(`{"ini":{"memory_limit":"bad"}}`)))
    if rec.Code != http.StatusForbidden { t.Fatalf("csrf code = %d", rec.Code) }
    rec = httptest.NewRecorder()
    req := authedRequest("PUT", "http://127.0.0.1/api/v1/sites/blog/php", `{"ini":{"memory_limit":"bad"}}`)
    fake.phpErr = &phpini.LintError{Line: 7, Output: "syntax error"}
    h.ServeHTTP(rec, req)
    if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `"PHP_CONFIG_INVALID"`) || !strings.Contains(rec.Body.String(), `"line":7`) { t.Fatalf("%d %s", rec.Code, rec.Body.String()) }
}
func TestAPIRemoveRuntimeInUseReturnsPHPVersionInUse(t *testing.T) {
    // fake.CheckRuntimeRemoval returns *sites.RuntimeInUseError{Sites:["blog","shop"]};
    // DELETE /api/v1/runtimes/php@8.4.4-r1 returns 409 with code and the site list.
}
func TestAPIPHPExecReturnsOutput(t *testing.T) {
    // POST .../php/exec with {"argv":["-v"]} returns exit_code/stdout/stderr.
}
```

- [ ] **Step 2: Run API tests to verify they fail**

Run: `go test ./internal/api -run 'TestAPIPHP|TestAPIRemoveRuntimeInUse' -v`
Expected: FAIL (404 routes).

- [ ] **Step 3: Implement API and error mapping**

Add the routes to `apiRoutes`, the site service interface methods, and handlers. `PUT /php` decodes `phpSettingsRequest` and calls `ApplyPHP`; `GET /php` returns `PHPInfo`; `POST /php/exec` calls `PHPExec`; `GET /php/extensions` returns the manifest extensions for `version` (400 for an unsupported line); defaults handlers call `PHPDefaults`/`SavePHPDefaults`; `php/migrate` calls `MigratePHP`. The create payload accepts `php_version`, `php_build`, `php_extensions`, `php_ini`, `php_ini_raw`, and `front_controller`; the update payload accepts `front_controller` and `document_root`, while PHP version/ini/extension changes use `PUT /sites/{ref}/php`.

In `classifyError`, map `sites.ErrRuntimeInUse` to 409 `PHP_VERSION_IN_USE` with `details.sites`, `sites.ErrRuntimeMissing` to 409 `RUNTIME_MISSING`, `phpini.ErrInvalidConfig`/`phpfpm.ErrConfigInvalid` to 422 `PHP_CONFIG_INVALID` with `details.line`/`details.output`, and `store.ErrNotFound` unchanged. Update `api/openapi.yaml` with every route and schema.

- [ ] **Step 4: Run API tests**

Run: `go test ./internal/api`
Expected: PASS.

- [ ] **Step 5: Write the failing client and CLI tests**

```go
func TestClientPHPApplyAndExec(t *testing.T) {
    // Fake socket API: assert PUT /api/v1/sites/blog/php carries the change and
    // POST /api/v1/sites/blog/php/exec returns exit_code/stdout/stderr.
}
func TestCLISitePHPShowsAndSwitches(t *testing.T) {
    fake := &fakeClient{site: model.Site{Name: "blog", Kind: model.SitePHP, PHPVersion: "8.4", PHPBuild: "php@8.4.4-r1"}}
    out := runCLI(t, fake, "site", "php", "blog")
    if !strings.Contains(out, "8.4") || !strings.Contains(out, "php@8.4.4-r1") { t.Fatalf("output = %q", out) }
    runCLI(t, fake, "site", "php", "blog", "8.5", "--build", "php@8.5.0-r1")
    if fake.phpChange.Version != "8.5" || fake.phpChange.Build != "php@8.5.0-r1" { t.Fatalf("%+v", fake.phpChange) }
}
func TestCLIPHPExecPassesArgsAfterDash(t *testing.T) {
    fake := &fakeClient{phpResult: backend.Result{ExitCode: 0, Stdout: "PHP 8.4.4\n"}}
    out := runCLI(t, fake, "php", "--site", "blog", "--", "-v")
    if !strings.Contains(out, "PHP 8.4.4") || !reflect.DeepEqual(fake.phpArgv, []string{"-v"}) { t.Fatalf("%q %+v", out, fake.phpArgv) }
}
func TestCLISiteExtToggle(t *testing.T) {
    fake := &fakeClient{site: model.Site{Name: "blog", Kind: model.SitePHP}}
    runCLI(t, fake, "site", "ext", "blog", "--enable", "xdebug", "--yes")
    if fake.phpChange.Extensions["xdebug"] != true { t.Fatalf("%+v", fake.phpChange) }
}
```

- [ ] **Step 6: Implement client and CLI**

Client methods map one-to-one to the routes and use the existing `doJSON` error envelope. CLI `site add` accepts `--kind php`, requires `--php`, rejects Node flags for PHP, and uses `resolvePHP` to pick the newest installed build for the requested line or the newest catalog entry, prompting with version, size, and SHA-256 before an install (unless `--yes`). `site show` adds PHP version, build ID, front-controller, and override counts. `site php` prints version/build and, with arguments, confirms the validate-then-swap impact (unless `--yes`) and calls `ApplyPHP`; `--list` prints installed and available builds for the line. `site ini` with no mutation prints `GeneratedIni` and the site section; mutations call `ApplyPHP` with the merged maps/raw and require confirmation unless `--yes`. `site ext` with no flags lists effective and default state; `--enable`/`--disable`/`--inherit` map to explicit true/false/absent overrides and apply immediately. `runtime migrate` accepts a line (`8.4`) or build ID for `from`/`--to`, uses `catalog.ParseID` plus `catalog.LineOf` when the value contains `@`, confirms the affected site list unless `--yes`, calls `MigratePHP`, and prints per-site results. `php --site` requires `--` and forwards the trailing argv.

All commands support `--json`; human output uses `text/tabwriter`. Update `runtime install` to look up the requested catalog entry and print version, size, revision, and SHA-256 in the confirmation prompt (for Node and PHP), satisfying the Plan 1 install-consent rule. Add remediation for `PHP_VERSION_IN_USE` (run `devstack site php <name> <version>` or `devstack runtime migrate`) and `PHP_CONFIG_INVALID` (open `devstack site ini <name>` and fix the reported line). Wire `sites.Deps.PHPManifest`, `sites.Deps.PHP`, `sites.Deps.PHPBackend`, and `doctor.Deps.PHP` in the daemon; `runtimes := runtimemgr.NewManager(...)` stays the production constructor.

- [ ] **Step 7: Run client, CLI, daemon, and API tests**

Run: `go test ./internal/client ./internal/cli ./internal/daemon ./internal/api`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/api internal/client internal/cli internal/daemon api/openapi.yaml
git commit -m "feat: expose PHP settings and runtime migration in the API and CLI"
```

---

### Task 10: Dashboard PHP screens and editing

**Files:**
- Modify: `ui/src/lib/types.ts`, `ui/src/lib/api.ts`, `ui/src/lib/mock.ts`, `ui/src/lib/api.test.ts`
- Modify: `ui/src/routes/Runtimes.svelte`, `ui/src/routes/AddSite.svelte`, `ui/src/routes/SiteDetail.svelte`
- Create: `ui/src/routes/Runtimes.test.ts`
- Create: `ui/src/lib/components/PHPIniEditor.svelte`, `ui/src/lib/components/PHPIniEditor.test.ts`
- Create: `ui/src/lib/components/ExtensionToggles.svelte`, `ui/src/lib/components/ExtensionToggles.test.ts`
- Create: `ui/src/lib/components/PHPDefaultsDrawer.svelte`
- Modify: `ui/tests/e2e.spec.ts` (PHP flow when mock or fixture backend is available)

**Interfaces:**
- Produces types: `PHPInfo`, `PHPChange`, `PHPDefaults`, `PHPExtension`, `PHPMigrationResult`, `PHPDefaultsResult`, `PHPRuntimeBuild` (extends `RuntimeBuild` with `revision` and `line`).
- Produces API functions: `getPHPInfo`, `applyPHP`, `phpExtensions`, `getPHPDefaults`, `savePHPDefaults`, `migratePHP`, `phpExec`.
- Produces components:
  - `PHPIniEditor`: props `{ ini: Record<string,string>; raw: string; generated: string; onApply(change): Promise<void> }`. Rows plus raw site-section editor; `generated` preview shows the managed block in a read-only `<pre>`; line errors render with the reported line number.
  - `ExtensionToggles`: props `{ extensions: PHPExtension[]; builtins: string[]; value: Record<string, boolean>; onchange(value) }`. Three states: inherit, on, off; built-ins are always-on and disabled.
  - `PHPDefaultsDrawer`: version selector, ini rows + extension toggles, affected-site list, save confirmation.

- [ ] **Step 1: Write the failing UI tests**

```ts
it('applies PHP settings with the CSRF token and no managed block', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse({ csrf_token: 'csrf-php' }))
    .mockResolvedValueOnce(jsonResponse({ name: 'blog', kind: 'php' }));
  vi.stubGlobal('fetch', fetchMock);
  await applyPHP('blog', { version: '8.4', ini: { memory_limit: '512M' }, ini_raw: '; keep\n' });
  const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit];
  expect(url).toBe('/api/v1/sites/blog/php');
  expect(init.method).toBe('PUT');
  expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('csrf-php');
  expect(String(init.body)).not.toContain('devstack extensions (managed)');
});
```

`PHPIniEditor.test.ts`: render with `raw: 'memory_limit = 512M\n'` and a generated preview containing the managed block; assert the managed preview is not editable, editing the raw textarea calls `onApply` with the edited raw only, and a rejected apply shows `PHP_CONFIG_INVALID` with `line 4`.

`ExtensionToggles.test.ts`: `xdebug` defaults off, `opcache` is on and `zend_extension`; clicking inherit removes the override, clicking on/off sets true/false; built-in `core` has no enabled toggle.

`Runtimes` remove flow test: mock `removeRuntime` rejecting with `PHP_VERSION_IN_USE` and sites `blog, shop`; assert the migration dialog lists both, calls `migratePHP` with the selected target build, and only retries removal after an all-OK result.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && npm test -- --run src/lib/api.test.ts src/lib/components/PHPIniEditor.test.ts src/lib/components/ExtensionToggles.test.ts src/routes/Runtimes.test.ts`
Expected: FAIL (missing exports/components/dialog).

- [ ] **Step 3: Implement types, API, and components**

Add the types and API functions using the existing `request` helper so CSRF and `same-origin` behavior are inherited. Add mock data for PHP builds/sites/defaults. Build the three components with existing tokens and components (`Card`, `Field`, `Button`, `Toggle`, `Badge`, `ConfirmDialog`); keyboard navigation, labels, and focus rings are required. The raw editor sends only the site section; the generated preview is read-only at the managed block. Errors show code, message, line, and output.

Runtimes page: group `Node` and `PHP`; show build ID and revision; install/remove PHP builds; mark a newer catalog revision of an installed line as an available update and link to the site switch action; disable remove when `used_by` is non-empty with a tooltip; add a "Defaults" action per PHP line opening `PHPDefaultsDrawer`. When a PHP remove returns `PHP_VERSION_IN_USE`, show the affected site names and a target-build selector, then call `migratePHP` and retry removal only after every site reports OK; a failed migration leaves the build installed and shows the per-site error.

AddSite: add `php` to the kind selector; after choosing PHP, show installed PHP builds grouped by line, document root, front-controller toggle, domain/TLS options, and a note that ini/extensions can be edited after creation. Submit includes `php_version`, `php_build`, and `front_controller`.

SiteDetail: add a PHP panel with build/version, FPM port, `PHPIniEditor`, `ExtensionToggles`, and a version/build switch confirmation that warns about validate-then-swap and rollback. Show `PHP_CONFIG_INVALID` output. A stopped site shows "changes apply on next start".

- [ ] **Step 4: Run UI tests and build**

Run: `cd ui && npm test -- --run && npm run build && cd .. && go test ./ui ./internal/api`
Expected: PASS and embedded assets build.

- [ ] **Step 5: Run the Playwright PHP flow**

Extend `ui/tests/e2e.spec.ts` with: open Add Site, choose PHP, select a PHP build, create `blog.test`, open Site detail, toggle `xdebug`, set `memory_limit=512M`, apply, see the success state, switch to a second build, and see the rollback message when the fake backend rejects. Skip the flow when no PHP fixture is configured, with a clear message.

Run: `cd ui && npx playwright test -g "PHP"`
Expected: PASS or SKIP with the fixture message.

- [ ] **Step 6: Commit**

```bash
git add ui
git commit -m "feat: add PHP runtime, ini, and extension controls to the dashboard"
```

---

### Task 11: PHP build pipeline, CI matrix, catalog generator, and docs

**Files:**
- Create: `build/php/build.sh`, `build/php/smoke.sh`, `build/php/latest-patch.sh`, `build/php/update-lock.sh`, `build/php/extensions.lock`, `build/php/base-php.ini`, `build/php/README.md`
- Create: `cmd/phpcatalog/main.go`, `cmd/phpcatalog/main_test.go`
- Create: `cmd/php-smoke/main.go`
- Create: `.github/workflows/php.yml`
- Modify: `Makefile`
- Create: `docs/php.md`
- Modify: `docs/vhosts.md`, `README.md`

**Interfaces:**
- Produces `build/php/build.sh` accepting `--line 8.2|8.3|8.4|8.5`, `--version <x.y.z>`, `--revision <n>`, `--output <dir>`. It downloads the pinned PHP source over HTTPS, verifies the source checksum from `build/php/extensions.lock` (PHP and PECL entries), configures a relocatable prefix with FPM and shared extensions, installs, bundles shared libraries, sets `RPATH=$ORIGIN/../lib` with `patchelf`, writes `etc/php.ini` from `base-php.ini`, and produces `php-<version>-r<revision>-linux-x64.tar.gz`.
- Produces `build/php/smoke.sh <tarball>`: extract to a temp prefix, run `php -v`, `php-fpm -v`, `php -m`, load every extension from `extensions.lock`, start FPM with a temp config, and issue a FastCGI request via `cmd/php-smoke`; any failure exits non-zero and the archive is not published.
- Produces `cmd/phpcatalog`:

```go
type Options struct {
    Version  string
    Revision int
    Artifact string
    SHA256   string
    Size     int64
    BaseURL  string
    Output   string
}
func Generate(opts Options) (catalog.Entry, error)
```

`Generate` verifies the artifact size and SHA-256, requires an HTTPS base URL, writes exactly one `catalog.Entry` with ID `php@<version>-r<revision>` and filename `php-<version>-r<revision>-linux-x64.tar.gz`, and never edits `catalog.json`. It prints the entry to stdout when `Output` is empty.
- Produces `.github/workflows/php.yml`: matrix `line: [8.2, 8.3, 8.4, 8.5]` on `ubuntu-22.04`, source cache, `build.sh`, `smoke.sh`, SBOM (`syft`), SHA-256, artifact upload, and a dependent `php-catalog` job that downloads all four artifacts and runs `go run ./cmd/phpcatalog` for each. The catalog job uploads `php-catalog.json` as a release artifact; merging into `internal/catalog/catalog.json` is a release step after the artifacts are published, not a commit in CI.

- [ ] **Step 1: Write the failing catalog generator tests**

```go
func TestGenerateVerifiesArtifactAndBuildsEntry(t *testing.T) {
    dir := t.TempDir()
    artifact := filepath.Join(dir, "php-8.4.4-r1-linux-x64.tar.gz")
    if err := os.WriteFile(artifact, []byte("archive"), 0o644); err != nil { t.Fatal(err) }
    sum := sha256.Sum256([]byte("archive"))
    entry, err := Generate(Options{Version: "8.4.4", Revision: 1, Artifact: artifact,
        SHA256: hex.EncodeToString(sum[:]), Size: 7, BaseURL: "https://downloads.example.test/php"})
    if err != nil { t.Fatal(err) }
    if entry.ID != "php@8.4.4-r1" || entry.Revision != 1 { t.Fatalf("%+v", entry) }
    if entry.URL != "https://downloads.example.test/php/php-8.4.4-r1-linux-x64.tar.gz" { t.Fatalf("url = %s", entry.URL) }
}
func TestGenerateRejectsMismatchAndNonHTTPS(t *testing.T) {
    // wrong size, wrong sha, http base URL, revision 0, and a base URL with a
    // query must all fail.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/phpcatalog`
Expected: FAIL (undefined: Generate).

- [ ] **Step 3: Implement the generator and smoke command**

Implement `Generate` with the same validation rules as `internal/catalog` (HTTPS, no query/fragment, lowercase SHA-256, positive size, PHP revision ≥ 1). `cmd/php-smoke` accepts `--root <prefix> --port <n>`, renders a temp FPM config with `internal/phpfpm`, starts `sbin/php-fpm`, sends a FastCGI request to a temp `health.php`, asserts `200 ok`, and stops the process group. It never runs as root.

- [ ] **Step 4: Add the build scripts**

`build/php/latest-patch.sh <line>` prints the newest stable patch version for the line from the PHP releases JSON over HTTPS. `build/php/update-lock.sh` resolves the PHP source checksum and every PECL package checksum, writes `extensions.lock` deterministically, and refuses to write an entry without a checksum. Run it once for the four selected patch versions and commit the resulting lock; the lock is the only place exact upstream PHP/PECL versions and checksums enter the repository. `build.sh` refuses to run when any required lock entry is absent. `build.sh` uses `--prefix`, `--enable-fpm`, `--enable-mbstring`, `--enable-intl`, `--with-curl`, `--with-openssl`, `--with-libxml`, `--with-zlib`, `--enable-sockets`, `--enable-soap`, `--enable-zip`, `--with-pdo-mysql`, `--with-pdo-pgsql`, `--with-pgsql`, `--with-mysqli`, `--enable-gd`, and the shared-extension flags for the standard and extra sets. Build PECL extensions from `extensions.lock`, install each `.so` into `lib/php/extensions/`, bundle `ldd` dependencies into `lib/`, and set `RPATH` on binaries and `.so` files. The base `php.ini` contains `[PHP]`, no `extension_dir`, and no extension lines.

`build/php/smoke.sh` extracts the tarball, verifies every `.so` loads with `php -d extension=<so> -m` (or `zend_extension` for opcache/xdebug), runs the FPM smoke request, and prints the tested versions. CI runs it before upload.

- [ ] **Step 5: Add CI and Makefile targets**

`Makefile`: `php-build`, `php-smoke`, `php-catalog`, and `php-test` targets. `.github/workflows/php.yml` runs the matrix, installs build dependencies, caches source, runs `bash -n build/php/*.sh`, `go test ./cmd/phpcatalog`, `build.sh`, `smoke.sh`, `syft`, `sha256sum`, uploads `php-<version>-r<revision>-linux-x64.tar.gz` and its `.sha256`, then the dependent catalog job. The workflow fails if any extension does not load or the FastCGI request fails. It does not commit catalog changes.

- [ ] **Step 6: Write docs**

`docs/php.md` covers: installing a PHP build, creating a PHP site, switching version/build, editing ini (rows and raw), extension toggles and defaults, `devstack php`, runtime migration/removal, FPM port/readiness, logs, troubleshooting (`PHP_CONFIG_INVALID`, `PHP_VERSION_IN_USE`, `READY_TIMEOUT`), and the release-artifact gate. `docs/vhosts.md` adds PHP prerequisites (`fastcgi_pass` for nginx, `mod_proxy_fcgi` for Apache), the 43000–43999 port range, and PHP vhost preview. `README.md` updates the status and limitations.

- [ ] **Step 7: Verify scripts, generator, and docs**

Run: `bash -n build/php/build.sh build/php/smoke.sh build/php/latest-patch.sh build/php/update-lock.sh && go test ./cmd/phpcatalog && go test ./...`
Expected: PASS. If `shellcheck` is installed, also run `shellcheck build/php/*.sh` and fix findings.

- [ ] **Step 8: Commit**

```bash
git add build/php cmd/phpcatalog cmd/php-smoke .github/workflows/php.yml Makefile docs/php.md docs/vhosts.md README.md
git commit -m "feat: add PHP build pipeline, catalog generation, and docs"
```

---

### Task 12: Plan 2 acceptance with real FPM, FastCGI, ini/extensions, rollback, and external templates

**Files:**
- Create: `test/php/acceptance_test.go`
- Create: `docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-acceptance.md`
- Modify: `README.md` (status/limitations), `docs/php.md` (acceptance notes)

**Interfaces:**
- No production interfaces. The acceptance test uses the real packages and skips cleanly when `/usr/local/php` (or `DEVSTACK_PHP_ROOT`) lacks `bin/php` or `sbin/php-fpm`.
- The test reads `PHP_VERSION` from the system binary, registers a `model.RuntimeBuild` row with ID `php@<PHP_VERSION>-r1`, and builds a filtered `catalog.PHPManifest` from `php -m` plus the `.so` files present, so the test exercises real extensions without pretending the system build matches the production all-shared layout.

- [ ] **Step 1: Write the acceptance test**

```go
func TestPHPSubsystemAcceptance(t *testing.T) {
    root := systemPHPRoot(t) // skip when absent
    // 1. Start a real backend FPM master; direct FastCGI GET /index.php returns
    //    PHP_VERSION, memory_limit=256M, and the expected extension state.
    // 2. Create two sites; enable xdebug on one; assert only that site reports it.
    // 3. Apply memory_limit=512M to one site; assert the other still reports 256M.
    // 4. Apply an ini containing a malformed line (no "="); assert a
    //    *phpini.ValidationError with that line and the old master still answers.
    // 5. Switch to a build row whose path lacks sbin/php-fpm; assert rollback and
    //    that the old master still answers.
    // 6. Render nginx and Apache PHP vhosts; assert fastcgi_pass/proxy:fcgi and
    //    PATH_INFO/SCRIPT_FILENAME semantics.
    // 7. Stop/start the site; assert the same build ID and a healthy master.
}
```

Use the real `sites.Service`, `backend.Local`, `proxy.Router`, `store.Store`, `logs.Store`, `certs.Manager` with a test signer, and no-op hosts/vhosts fakes. Use `fcgi.Dial` for direct requests and `httptest` against `router.Handler()` for proxy requests with `Host: blog.test`.

- [ ] **Step 2: Run the acceptance test**

Run: `DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -v`
Expected: PASS on this machine (PHP 8.4.4 at `/usr/local/php`); SKIP with a printed reason when the root is absent.

- [ ] **Step 3: Run the automated acceptance suite**

Run:

```bash
go test ./... -race
go test ./internal/backend ./internal/sites ./internal/proxy ./internal/phpfpm ./internal/phpini -v
make build
```

Expected: PASS; record the PHP version, build ID, FPM ports, and test command output in the acceptance report.

- [ ] **Step 4: Run the external template checks**

Run: `go test ./internal/vhost -run 'TestRenderNginxPHP|TestRenderApachePHP' -v`
Expected: PASS. If nginx and Apache are installed, also run each generated PHP vhost through `nginx -t` or `apachectl configtest` in a temporary prefix with high listen ports; record the result. A skipped real-server check is recorded as skipped, not passed.

- [ ] **Step 5: Write the acceptance report**

Copy the Plan 2 subset of spec §21 into `docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-acceptance.md`: real 8.4 per-site version, per-site ini, xdebug isolation, invalid ini rejected with line info while the old master serves, add/remove/update/migrate, removal blocked while referenced, switch with rollback, FastCGI request, external nginx/Apache templates, Doctor PHP checks, and docs. Record each as pass/fail/skip with the exact command and output. The real 8.2 and 8.5 side-by-side check in §21 is a **release gate** that requires published artifacts: record it as blocked with a linked Task 11 artifact dependency, not as passed. State explicitly that production PHP catalog publication is pending real CI artifacts; tests used fixture catalogs and the system PHP 8.4.

- [ ] **Step 6: Fix or file every failure before marking the plan complete**

No task is complete with a failing acceptance check. Either fix it and rerun, or record a blocking issue with a linked follow-up task. Do not mark the build-artifact gate as passed without published checksummed archives.

- [ ] **Step 7: Commit the acceptance report**

```bash
git add test/php docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-acceptance.md README.md docs/php.md
git commit -m "docs: record plan 2 acceptance results"
```

---

## Plan series and deferred work

- **Plan 1 — foundation (implemented):** helper, CA/trust, hosts, static/Node sites, proxy, dashboard, external vhosts.
- **Plan 2 — PHP (this plan):** build pipeline, revisioned catalog, per-site FPM, php.ini, extensions, FastCGI, PHP vhosts, CLI/API/UI, Doctor, acceptance.
- **Plan 3 — Windows/WSL:** agent, provisioning, path translation, Windows helper (named pipe, cert store, Firefox policy), Windows install, backend conformance. PHP's WSL pool uses `user = devstack`, `group = devstack`, and the NAT-interface listen address; the agent owns process supervision there.
- **Plan 4 — packaging/release:** signed binaries/packages, release workflow, upgrade, diagnostics bundle, docs polish, full M1 acceptance, and publication of the real PHP catalog entries produced by the Task 11 pipeline.
