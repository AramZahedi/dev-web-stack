# dev-web-stack — Plan 2 (PHP) Handoff / Status

- **Date:** 2026-10-24
- **Branch:** `plan-2-php` (created from `plan-1-foundation` at `4f7ab97`)
- **Remote:** `origin` (`git@github.com:AramZahedi/dev-web-stack.git`)
- **Head at this handoff:** `b9f38c8` (`fix: close plan 2 final review findings`)
- **Origin `plan-2-php` before this handoff:** `c1e5f33`; this handoff adds the next
  docs commit and must be pushed.
- **Origin `plan-1-foundation`:** `4f7ab97` (Plan 1 complete, pushed).
- **Working tree at handoff:** clean except this handoff/plan doc edit.
- **Verification at `b9f38c8`:** `go test ./...` PASS (all packages, including the
  real-FPM `test/php` acceptance test). The final fix report also records
  `go test ./... -race`, focused suites, UI Vitest/check/build, Playwright 19/19,
  `make build`, and no FPM listeners on 43000–43999.
- **Plan 1:** Tasks 1–26 complete; automated acceptance PASS; manual
  browser/root/Apache/uninstall checks remain blocked.
- **Plan 2:** Tasks 1–12 complete and task-reviewed; automated acceptance PASS on
  system PHP 8.4.4. **Final whole-branch review is not clean** — see §2. Do not
  merge `plan-2-php` until the two open Important findings are fixed and
  re-reviewed.
- **Plan 3 (Windows/WSL):** **skipped/deferred by user request.** Keep the Windows
  build for later. Do not start Plan 3.
- **Next implementation plan:** Plan 4 (packaging/release), Linux-only for now.
  Write it after closing the Plan 2 open findings (or at the user's direction).

---

## 1. Exact next step

### 1.1 Close the two open final-review findings

Start from `plan-2-php` at this handoff tip, run `go test ./...` (passes), then
dispatch one fix implementer on **`opencode-go/deepseek-v4.1-flash#max`** with the
two findings below. The original findings list and fix report live in the
git-ignored workspace:

- `.superpowers/sdd/2026-10-23-dev-web-stack-plan-2-php/final-review-fixes.md`
- `.superpowers/sdd/2026-10-23-dev-web-stack-plan-2-php/final-review-fix-report.md`

They will not exist on another machine; this handoff carries the essential detail.

#### Open Important A — retained generation/port can still be pruned

The final fix retains a staged generation and its port when `StopPHP` fails
(`internal/sites/php_apply.go:236-244`), but a later successful apply prunes every
hash directory except its own current/previous row hashes
(`internal/sites/php_apply.go:300-308`, `:370-402`). The retained generation is not
a row hash, so the still-live master's directory can be deleted. The existing
regression test (`TestApplyPHPRollbackRetainsGenerationWhenStopFails`,
`internal/sites/php_apply_test.go:1362-1397`) stops at the retention assertion and
never applies again.

Also: `internal/sites/service.go:946-951` uses the cleanup context in
`restoreSiteStateLocked`, but `internal/sites/service.go:987-992` still releases
`current.FastCGIPort` when `previous.FastCGIPort == 0` even after that stop returned
an error. No test covers that path.

Required fix:
- Track retained live generations/ports (for example a service-level keep set) and
  exclude them from every later prune; or skip pruning while any retained live
  generation exists.
- Only release a port after the corresponding stop succeeds; if the stop fails,
  retain the port and return/join the error.
- Add tests: failed stop then a later successful apply must **not** delete the
  retained generation; `restoreSiteStateLocked` must not release a port when its
  stop failed.

#### Open Important B — `PHPExec` on a running site can repin the row without swapping FPM

The stopped-site defaults fix (`internal/sites/php.go:562-596`) recomposes and, when
the hash differs, writes a new generation and persists `PHPConfigHash = files.Hash`
(`:575-588`). For a **running** site the `default` master keeps the old generation;
`PHPExec` does not start a master. A later `ApplyPHP` probe
(`internal/sites/php_apply.go:154-164`) sees the row hash already equals the composed
hash and takes `persistPHPRowOnlyLocked`, so the new defaults never reach the running
master.

Required fix (choose one, with a regression test):
- For a running site, `PHPExec` should compose/use a temporary/current generation
  without persisting `PHPConfigHash`; the next Start/Apply must still see the old
  stored hash and swap/recompose; or
- Stage and swap the master before persisting the new hash; or
- Persist the new hash but mark the site “restart required” and ensure Apply does not
  treat the master as current.

Add a test: running site, edit version defaults, run `PHPExec` (new ini must be used
for the exec), then `ApplyPHP`/restart must actually apply the new config to the
serving master. The existing `TestStoppedPHPStartAndExecPickUpEditedDefaults`
(`internal/sites/php_apply_test.go:1255`) only covers a stopped site.

#### Open Minor

- `internal/sites/php_apply.go:197-200`: the hash-equality branch releases the
  allocated port before `persistPHPRowOnlyLocked` writes the row with that port. The
  branch is currently unreachable because the FPM hash includes the listen port, but
  fix the ordering or clear the row port.
- `ui/src/routes/Runtimes.svelte:772`: the migration list's `aria-label` still says
  “Sites using the build” although it now lists every site on the PHP line. Make it
  match the visible line-wide wording.
- Out-of-scope/knowledge: legacy PHP rows with empty `php_version` can appear in the
  migration dialog because `CheckRuntimeRemoval` reports any site pinning the build,
  while `MigratePHP` skips rows whose `PHPVersion` is empty. Fix in a later CLI/UI
  pass if it matters.

After fixing:
- Run `go test ./... -race -count=1`, the real acceptance,
  `go test ./internal/sites`, UI tests/check/build, and the PHP/Runtimes Playwright
  tests.
- Dispatch a scoped re-review (same model) against the fix diff; all findings must be
  ADDRESSED with no new Critical/Important breakage.
- Update this handoff/plan status before merges.

### 1.2 Then write and execute Plan 4 (packaging/release), skipping Plan 3

Plan 4 scope from the plan series (Linux-only while Windows is deferred):

- Signed binaries/packages and a release workflow.
- Upgrade path and versioning.
- Diagnostics bundle.
- Documentation polish.
- Full M1 acceptance (including the existing Plan 1/Plan 2 automated gates).
- Publication of real PHP catalog entries **only after** the Task 11 CI pipeline has
  published checksummed artifacts. No invented PHP URLs/checksums.
- Do **not** implement Windows/WSL (Plan 3); the WSL agent, named-pipe helper, Windows
  trust stores, and `install.ps1` are explicitly deferred.

Plan 4 does not exist yet. Write it from the M1 design/spec and the Plan 2 artifact
gate, then execute with one implementer per task, task reviews, and a final
whole-branch review. Use **`opencode-go/deepseek-v4.1-flash#max`** for every subagent.

---

## 2. Final whole-branch review status

The final review covered `4f7ab97..c1e5f33` (31 commits, ~1.4 MB diff) and returned
**“Ready to merge with fixes.”** One Critical and seven Important findings were sent
to one fix wave. `b9f38c8` (`fix: close plan 2 final review findings`) addressed most
of them, but the scoped re-review found:

| Finding | Status |
|---|---|
| **C1** Built-in proxy source disclosure via `.php.tmp`/`.php.swp` | **ADDRESSED** — suffixes added; proxy/native parity helper + tests. |
| **I1** Cleanup context/port ownership/prune live generation | **PARTIALLY ADDRESSED / OPEN** — contexts and enumeration fixed; retained live generation can still be pruned by a later apply; `restoreSiteStateLocked` can release a port after a failed stop; tests missing. |
| **I2** `memcached` missing igbinary dependency | **ADDRESSED** — `requires:["igbinary"]` + tests. |
| **I3** Stopped sites don't pick up edited defaults | **ADDRESSED for stopped sites** — compose/compare before reuse; stopped test passes. |
| **I4** 8.5 stale opcache override / poisoned defaults | **ADDRESSED** — overrides dropped on line switch; stored defaults/site maps sanitized; tests. |
| **I5** Dashboard Inherit baseline | **ADDRESSED** — `defaults` prop; SiteDetail/drawer pass edited baseline; tests. |
| **I6** Migration dialog moves unlisted same-line sites | **ADDRESSED** — union with `phpSitesOn(line)`, same-line target default, tests. |
| **I7** Apache HTTP configtest gated on mod_ssl | **ADDRESSED** — HTTP requires only `proxy_fcgi_module`; HTTP PASS, HTTPS SKIP. |
| Minor: hash-branch port leak | ADDRESSED for release, but ordering minor remains (see §1.1). |
| Minor: builtin membership validation | ADDRESSED. |
| Minor: Line 0 / AddSite focus | ADDRESSED. |
| Minor: start store-failure cleanup | ADDRESSED. |
| Minor: absent runtime removal idempotence | ADDRESSED. |
| **New Important** | **OPEN** — running-site `PHPExec` repins `PHPConfigHash` without swapping the master; later ApplyPHP can see a false no-op. |
| New Minor | `Runtimes` migration list ARIA label stale; hash-branch release-before-persist ordering. |

Do not mark Plan 2 merge-complete until A and B above are fixed and re-reviewed.
The acceptance results themselves remain valid for the code at `b9f38c8`; the open
issues are edge/cross-path defects, not acceptance failures.

---

## 3. Plan 2 task status and commits

| Task | Status | Commits |
|---|---|---|
| Plan 2 plan | Committed | `2a02cdb` |
| 1. PHP build pinning/store/catalog/extension metadata | Complete, reviewed | `8f1da67`, `f1ed063` |
| 2. `php.ini` composition/validation | Complete, reviewed | `d0c50ff`, `0499c52` |
| 3. FastCGI client + php-fpm config | Complete, reviewed | `376f5cb`, `926bfd0` |
| 4. Local backend PHP process/build resolution | Complete, reviewed | `78e0f17`, `1b9221a` |
| Handoff before Task 5 | Committed/pushed | `c0ff9d8` |
| 5. PHP site create/start/stop/delete/vhost | Complete, reviewed | `5af2298`, `d739759` |
| Docs through Task 6 | Committed | `7166a69` |
| 6. Validate-then-swap version/ini/extensions | Complete, reviewed | `9f8f795` |
| 7. Built-in proxy PHP/FastCGI transport | Complete, reviewed | `bdcfaaf` |
| Docs through Task 7 | Committed | `ae56eb2` |
| 8. PHP nginx/Apache vhosts/helper/Doctor | Complete, reviewed | `9fa1911` |
| Docs through Task 8 | Committed | `e2d16b3` |
| 9. REST API/typed client/CLI/daemon | Complete, reviewed | `5126eba`, `70fc7d7` |
| Docs through Task 9 | Committed | `2aeab8d` |
| 10. Dashboard PHP screens/editing | Complete, reviewed | `0efb5ca`, `e8796ff` |
| Docs through Task 10 | Committed | `f8dc656` |
| 11. PHP build pipeline/CI/catalog generator/docs | Complete, reviewed | `3a22864`, `ae9d4b6` |
| Docs through Task 11 | Committed | `65ad1bf` |
| 12. Plan 2 acceptance | Complete, reviewed | `d2b37e9`, `a8db13f` |
| Plan 2 completion docs | Committed | `c1e5f33` |
| Final review fix wave | Committed, re-review open | `b9f38c8` |

Original handoff after Task 4: `c0ff9d8`. Plan 1 tip: `4f7ab97`.

---

## 4. What is implemented (condensed)

### Task 1 — Build pinning, store, revisioned catalog, extensions
- `model.Site.PHPBuild`, `PHPIniRaw`, `PHPConfigHash`; migration `0003_php_build.sql`
  (`sites.php_build`, `php_ini_raw`, `php_config_hash`, `site_php_config.applied_at`).
- Revisioned IDs `php@8.4.4-r1`; `ParseID`/`FormatID`/`ForKindVersion`/`NewestForLine`;
  Node IDs keep revision 0.
- Embedded `internal/catalog/php.json` extension manifest + per-version defaults.
- `runtime.Manager.EnsureBuild` is store-first: an intact installed build is returned
  even if the current catalog no longer lists it; catalog install/repair only for
  missing/stale paths.
- Production `internal/catalog/catalog.json` still has **no PHP entries**.

### Task 2 — `php.ini`
- `internal/phpini`: `Compose`, `ExtractSiteSection`, `Parse`, `ValidateOverrides`,
  `ValidateRaw`, `Lint` (real PHP), `ErrInvalidConfig`, `ValidationError`, `LintError`.
- Order: base → `[PHP]` reset → sorted version defaults → site overrides → raw →
  `[PHP]` reset → managed extension block.
- Managed block: `extension_dir`, `extension=`, `zend_extension=`, markers rejected
  from raw/overrides. CA aliases (`curl.cainfo`/`openssl.cafile`) emitted unless the
  effective defaults/overrides set them.

### Task 3 — FastCGI + php-fpm config
- `internal/fcgi`: v1 responder, chunked params, STDIN, response streaming, bounded
  headers (256 KiB/line, 1 MiB total), bounded stderr, cancellation, protocol status.
- `internal/phpfpm`: exact pool (`dynamic`, max 5, start 2, min spare 1, max spare 3,
  `catch_workers_output=yes`, `clear_env=no`), quoted paths/env (literal `$`),
  `listen` 43000–43999, `Validate` rejects malformed ini even when FPM exits 0.
- `error_log=/proc/self/fd/2`; supervisor supplies a pipe so FPM output reaches the
  redacted site stream with source `fpm`.

### Task 4 — Local backend
- `backend.SiteSpec` PHP fields; `EnsureRuntimeBuild`; `PHPStager`
  (`PreparePHP`/`StartPHP`/`StopPHP`).
- Default generation = bare site ID; other generations = `<siteID>#<generation>`.
- Per-site FPM master; health poll 30s overall, 2s per FastCGI attempt; failure stops
  the master, releases the port, includes log tail.
- `Exec` runs build `bin/php`, build bin first on PATH, `PHP_INI_SCAN_DIR=`,
  `PHPRC=<generated ini>`; explicit `-c` wins.

### Task 5 — Site lifecycle
- `CreateRequest`/`UpdateRequest` PHP fields; `PHPBackend` fallback to `Backend`;
  default `PHPCommands`; lazy manifest load; `ErrRuntimeMissing`.
- Installed-store-only build resolution; exact ID or newest version/revision for line.
- FastCGI port allocated before hosts; compose/validate/write immutable
  `sites/<id>/<hash>/` generation (`0700`/`0600`/`0644`); commit stable copies;
  create/start/stop/delete rollback; Delete stops FPM, releases both ports, removes
  only `sites/<id>`.
- `PHPExec` (stopped sites, generated ini, bypass flags rejected); `ValidatePHP`
  temp compose; front-controller/document-root updates.
- `proxy.KindPHP` and route fields added early; sites routes use `FastCGIPort`,
  absolute docroot, front controller. FPM log path `/proc/self/fd/2`. `phpSiteDir`
  rejects `.`.

### Task 6 — Apply/defaults/migrate/removal
- `ApplyPHP` merges version/build/extensions/ini/raw; running site allocates new port
  **before** composing (hash includes port), writes/validates, stages `PreparePHP`/
  `StartPHP` on generation=hash, publishes row/snapshot/routes/vhosts, commits stable
  copies, stops default + previous hash generation, releases old port only after
  successful stops, prunes current+previous.
- No-op when hash matches; stopped apply allocates/persists a port but starts nothing;
  `SavePHPDefaults` fills missing keys from embedded baseline and applies to running
  sites independently; `MigratePHP` line-wide; `CheckRuntimeRemoval` blocks exact
  builds and legacy empty-build rows by line.
- `startPHPLocked` idempotent after staged apply.

### Task 7 — Built-in proxy
- `KindPHP` dispatch to `servePHP`; FastCGI 120s deadline; full CGI params; synthesized
  `HTTP_HOST`/`X-Forwarded-*`; body length exact/-1.
- Path resolution: NUL/`..`/dotfiles rejected; EvalSymlinks containment; directories
  `index.php` then `index.html`; `.php` case-folded; `PATH_INFO`; front controller;
  backup suffixes denied; static files via `http.ServeContent` no-store.
- Response strips hop-by-hop/Status/HSTS; HEAD body suppressed; branded 502.

### Task 8 — Native vhosts/helper/Doctor
- PHP vhost validation: running requires docroot + 43000–43999; stopped may use 0 and
  omit root; FrontController PHP-only; `ErrPHPUnsupported` removed.
- nginx `location ~*` PHP, deny dotfiles/backups before PHP, front-controller named
  location, fixed `SCRIPT_FILENAME`, `PATH_INFO $uri`.
- Apache `(?i)\.php$` `SetHandler proxy:fcgi`, `DirectoryIndex`, front-controller
  rewrite inside `<Directory>`, backup/dotfile/`_devstack` denial.
- Helper already resolves/contains running PHP docroots via `vhost.ValidateSiteSpec`
  + existing path checks; tests added.
- Doctor `php-builds`, `php-config`, `php-ports`, `php-master`; skip when unused;
  `Deps.PHP` wired to site service.

### Task 9 — API/client/CLI/daemon
- Seven routes under `/api/v1` (site PHP info/apply/exec, extensions, defaults,
  migrate); CSRF/session/host checks inherited; errors map `PHP_VERSION_IN_USE` 409,
  `RUNTIME_MISSING` 409, `PHP_CONFIG_INVALID` 422 with line/output/field/message.
- Runtime delete calls `CheckRuntimeRemoval`; absent build 404 handled idempotently
  after final fix.
- Typed client methods; create/update PHP fields; CLI `site add --kind php`,
  `site php`, `site ini`, `site ext`, `runtime migrate`, `php --site`; install prompt
  includes version/size/revision/SHA; `--json`; remediation.
- Daemon loads `catalog.LoadPHP()` once, passes manifest, PHPBackend, PHPCommands.

### Task 10 — Dashboard
- Types/API functions; PHP runtime grouping, defaults drawer, build switch, ini editor,
  extension toggles; `used_by` derived from `php_build`/line; migration dialog lists
  all line sites; managed block read-only; `PHP_CONFIG_INVALID` line/output.
- Inherit labels use edited defaults baseline after final fix.
- Playwright PHP flow in mock mode.

### Task 11 — Build pipeline/CI/catalog/docs
- `build/php/` scripts: `build.sh`, `smoke.sh`, `latest-patch.sh`, `update-lock.sh`,
  `extensions.lock`, `base-php.ini`, README.
- Verified PHP source pins: 8.2.35, 8.3.36, 8.4.27, 8.5.12 (real SHA-256 in lock).
- Verified PECL pins (redis, apcu, xdebug, imagick, memcached, ssh2, yaml, igbinary,
  msgpack, grpc, pcov); `imap` deliberately blocked/documented (c-client / version
  constraints). `igbinary 3.2.17RC1` is used because it supports PHP 8.5.
- PHP 8.5 has built-in OPcache, no `opcache.so`; `php.json` has
  `builtin_by_version: {"8.5":["opcache"]}` and omits opcache from 8.5 defaults.
- `cmd/phpcatalog` verifies artifact name/size/SHA/base URL, emits one entry, refuses
  to overwrite `internal/catalog/catalog.json`; `cmd/php-smoke` runs a real FPM
  FastCGI health, refuses root.
- CI matrix 8.2–8.5 on ubuntu-22.04 with deps, shell checks, build/smoke, SBOM,
  checksum, upload, dependent catalog job. Release base URL is required; CI does not
  commit catalog changes. No published PHP artifacts in this environment.
- Docs state the artifact gate.

### Task 12 — Acceptance
- `test/php/acceptance_test.go` uses real system PHP 8.4.4, real FPM/backend/sites/
  store/logs/certs/router, filtered manifest from present `.so` files + real built-ins.
- Verified: per-site 8.4, per-site ini, xdebug isolation, malformed ini line 2 with
  old master serving, failed switch rollback, candidate port OS-level free, delete/
  stop/StopAll port-free checks, proxy FastCGI, nginx `-t`, Apache HTTP configtest
  PASS (HTTPS SKIP: no mod_ssl), Doctor PHP checks OK, stop/start.
- Acceptance report:
  `docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-acceptance.md`.
- System PHP: 8.4.4 at `/usr/local/php`; extension dir
  `/usr/local/php/lib/php/extensions/no-debug-non-zts-20240924`; filtered shared
  extensions observed: imagick, opcache, pcov, pdo_pgsql, redis, xdebug, zip.
- FPM ports observed: 43000 (blog), 43001 (api), 43002 (blog after xdebug),
  43003 (blog after ini), failed-switch candidate 43999.

---

## 5. Blockers / honest gaps

1. **Plan 2 final review open** — the two Important findings in §1.1; not merge-ready.
2. **Production PHP artifacts** — `internal/catalog/catalog.json` has no PHP entries.
   Task 11 CI has not published checksummed tarballs; the real 8.2/8.5 side-by-side
   is BLOCKED. Do not invent URLs/checksums.
3. **Apache HTTPS** — `mod_ssl` is absent on this workstation; Apache HTTP PHP config
   test passes, HTTPS vhost is SKIP. Real Apache serving remains manual.
4. **In-flight request continuity across a successful swap** — not measured.
5. **Cross-build migration** — acceptance migrates a same-path `php@8.4.4-r2`
   revision; it does not prove switching between different real builds (blocked on
   artifacts).
6. **`imap`** — build pipeline blocked/documented; UI may still offer the toggle
   until a later cleanup; enabling it will fail validation.
7. **Plan 1 manual blockers** — browser trust, fresh root init/uninstall/80/443, real
   Apache serving, live UI daemon, browser HMR/WebSocket remain manual.
8. **Windows/WSL** — entirely deferred; no agent, named pipe, Windows trust, or
   `install.ps1`.

---

## 6. Verification / environment

Host: Fedora Linux 44 x86_64. Go 1.27.3; Node 22.23.1/npm 10.9.8.
PHP 8.4.4 CLI `/usr/local/php/bin/php`; FPM `/usr/local/php/sbin/php-fpm`.
nginx 1.28.0; Apache 2.4.69 (no mod_ssl); system nginx owns 80/443.

At `b9f38c8`:
- `go test ./...` PASS.
- `go test ./... -race -count=1` PASS (fix report).
- `DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -v -count=1` PASS.
- `go test ./internal/backend ./internal/sites ./internal/proxy ./internal/phpfpm ./internal/phpini -v` PASS.
- `go test ./internal/vhost ./internal/helper ./internal/doctor` PASS.
- `cd ui && npm test -- --run` PASS (51 tests), `npm run check` PASS, `npm run build` PASS,
  `npx playwright test` 19/19 PASS.
- `make build` PASS.
- No FPM listeners left on 43000–43999 after acceptance.

Useful commands:

```sh
go test ./...
go test -race ./...
go test ./test/php -run TestPHPSubsystemAcceptance -v -count=1
go test ./internal/backend ./internal/sites ./internal/proxy ./internal/phpfpm ./internal/phpini -v
cd ui && npm ci && npm test -- --run && npm run check && npm run build
cd ui && npx playwright test
make build
```

The local SDD ledger/reports are git-ignored under
`.superpowers/sdd/2026-10-23-dev-web-stack-plan-2-php/`. The committed handoff is the
portable record. On another machine, regenerate briefs/review packages from the plan
and git history.

---

## 7. Repository map (current)

```
cmd/devstack/            CLI + daemon
cmd/devstack-helper/     privileged helper
cmd/phpcatalog/          PHP catalog entry generator
cmd/php-smoke/           FPM/FastCGI artifact smoke command
build/php/               PHP build/smoke/lock/CI scripts + base ini
internal/config/         config/paths (serving_mode; external requires 7070)
internal/model/          Site (PHP fields), RuntimeBuild, PHPDefaults, SitePHPConfig
internal/store/          SQLite migrations 0001-0003; PHP defaults/config
internal/logs/           rotation/ring/SSE/redaction
internal/supervisor/     process groups/readiness/backoff
internal/backend/        local static/Node/PHP; PHPStager; runtime smoke
internal/helperproto/    RPC + vhost types
internal/helper/         auth, hosts, CA/trust, forwarders, vhost manager
internal/helperclient/   helper RPC client
internal/forwarder/      80/443 forwarders
internal/proxy/          router/TLS/HTTP upstream; PHP FastCGI transport
internal/staticserve/    static handler
internal/certs/          certs/renewal/PEM
internal/catalog/        Node catalog + php.json; no PHP entries in catalog.json
internal/runtime/        downloads/extraction/verify; revisioned PHP; phpSmoke
internal/vhost/          nginx/Apache renderers incl. PHP templates
internal/sites/          lifecycle, PHP config/apply/exec/validation, routes/vhosts
internal/doctor/         diagnostics incl. PHP checks
internal/api/            REST/SSE/auth/UI; PHP handlers
internal/daemon/         startup/shutdown/reconcile; PHP wiring
internal/client/         Unix-socket client; PHP methods
internal/cli/            Cobra commands; PHP site/ini/ext/runtime/php
internal/phpini/         php.ini compose/validate
internal/fcgi/           FastCGI client/records
internal/phpfpm/         php-fpm config/render/validate
ui/                      Svelte 5 dashboard + mock/real API + Playwright
test/php/                real-FPM acceptance
api/openapi.yaml         API contract incl. PHP routes
build/systemd/           units
docs/                    install/sites/tls/vhosts/php + superpowers plans/specs
test/e2e/linux.sh        Plan 1 builtin + high-port nginx E2E
install.sh               Linux installer
```

Defaults: `~/.devstack`; proxy 8080/8443; UI/API 7070; helper socket
`/run/devstack-helper/helper.sock`; helper state `/var/lib/devstack-helper`;
vhost tree `/etc/devstack/vhosts`; TLS `/etc/devstack/tls`; Node 41000–42999;
FastCGI 43000–43999.

---

## 8. Plan 3 / Plan 4

- **Plan 3 — Windows/WSL: SKIPPED by user request.** Keep for later. Do not implement
  the WSL agent, named-pipe helper, Windows cert/trust stores, Firefox policy,
  `install.ps1`, or WSL lifecycle.
- **Plan 4 — packaging/release (next):** signed binaries/packages, release workflow,
  upgrade, diagnostics bundle, docs polish, full M1 acceptance, and publication of
  the real PHP catalog entries after CI artifacts exist. Linux-only while Windows is
  deferred. No invented production PHP URL/checksum. Write the plan first; then
  execute with subagent-driven development.

---

## 9. Commit/push state

Before this handoff, `plan-2-php` was at `b9f38c8` (one commit ahead of origin
`c1e5f33`). This handoff adds a docs commit on top of:

```
b9f38c8 fix: close plan 2 final review findings
c1e5f33 docs: record plan 2 completion and acceptance
a8db13f fix: prove PHP acceptance cleanup and built-ins
d2b37e9 docs: record plan 2 acceptance results
65ad1bf docs: record plan 2 status through task 11
ae9d4b6 fix: make PHP 8.5 artifacts and smoke checks trustworthy
3a22864 feat: add PHP build pipeline, catalog generation, and docs
f8dc656 docs: record plan 2 status through task 10
e8796ff fix: keep PHP defaults safe when loading fails
0efb5ca feat: add PHP runtime, ini, and extension controls to the dashboard
2aeab8d docs: record plan 2 status through task 9
70fc7d7 fix: classify PHP config and exec argument errors
5126eba feat: expose PHP settings and runtime migration in the API and CLI
e2d16b3 docs: record plan 2 status through task 8
9fa1911 feat: add PHP vhost templates and Doctor checks
ae56eb2 docs: record plan 2 status through task 7
bdcfaaf feat: proxy PHP requests over FastCGI
7166a69 docs: record plan 2 implementation rulings through task 6
9f8f795 feat: apply PHP version, ini, and extension changes with rollback
d739759 fix: keep PHP logs and updates consistent
5af2298 feat: create and run per-site PHP sites
c0ff9d8 docs: update plan 2 PHP handoff through task 4
1b9221a fix: bound PHP FastCGI health attempts by the readiness deadline
78e0f17 feat: run per-site php-fpm masters in the local backend
926bfd0 fix: bound FastCGI headers and escape FPM values
988d4d8 docs: add plan 2 PHP handoff
376f5cb feat: add FastCGI client and php-fpm configuration
0499c52 fix: scope php.ini managed blocks and CA aliases
d0c50ff feat: compose and validate per-site php.ini
f1ed063 fix: return intact installed PHP builds before catalog lookup
8f1da67 feat: pin PHP builds by revision and add extension metadata
2a02cdb docs: add plan 2 PHP implementation plan
```

After committing/pushing this handoff:

```sh
git push origin plan-2-php
git fetch origin
git rev-list --left-right --count origin/plan-2-php...HEAD   # expect 0 0
git status --short --branch                                  # clean
```

Paused at the user's request. Next: close the two open final-review findings
(§1.1), re-review, then write/execute Plan 4. Do not start Plan 3.
