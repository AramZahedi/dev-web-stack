# dev-web-stack — Plan 2 (PHP) Handoff / Status

- **Date:** 2026-10-24
- **Branch:** `plan-2-php` (created from `plan-1-foundation` at `4f7ab97`)
- **Remote:** `origin` (`git@github.com:AramZahedi/dev-web-stack.git`)
- **Head before this handoff commit:** `6add708` (`fix: retain stopped PHP row port on cleanup`)
- **Origin `plan-2-php` (actual remote tip):** `fec5917`
  (`fix: load standard PHP modules in PECL smoke checks`). It includes this
  handoff and the two approved CI smoke fixes (`0baf4af`, `fec5917`), and is
  merged into `plan-4-release` at `a0e0e4f`.
- **Origin `plan-1-foundation`:** `4f7ab97` (Plan 1 complete, pushed).
- **Working tree at handoff:** clean except this handoff/plan doc edit.
- **Verification at `6add708`:** `go test ./... -race -count=1` PASS (all packages,
  including the real-FPM `test/php` acceptance test); UI Vitest 51/51 and
  `npm run check` PASS at `039f0b0`; the residual fix report records real acceptance,
  Playwright 19/19, `make build`, and no FPM listeners on 43000–43999.
- **Plan 1:** Tasks 1–26 complete; automated acceptance PASS; manual
  browser/root/Apache/uninstall checks remain blocked.
- **Plan 2:** Tasks 1–12 complete and task-reviewed; automated acceptance PASS on
  system PHP 8.4.4. Final whole-branch review and both residual re-reviews are clean
  (one Minor parked). **Plan 2 is merge-ready.**
- **Plan 3 (Windows/WSL):** **skipped/deferred by user request.** Keep the Windows
  build for later. Do not start Plan 3.
- **Next implementation plan:** Plan 4 (packaging/release), Linux-only for now.

---

## 1. Exact next step

**Plan 2 is merge-ready.** All final-review Critical/Important findings are closed and
re-reviewed. The next implementation step is to write and execute **Plan 4
(packaging/release), Linux-only**, while **Plan 3 (Windows/WSL) stays skipped**.

### 1.1 Final-review residuals (resolved)

The final whole-branch review of `4f7ab97..c1e5f33` found one Critical and seven
Important issues. `b9f38c8` fixed most; the scoped re-review then found two Important
residuals. They were closed by:

- `039f0b0` — `fix: close plan 2 final review residuals`
  - Added a service-owned retained-generation/port map.
  - Every failed cleanup stop is recorded; pruning is skipped while retained
    generations exist.
  - A later apply stops retained generations with the cleanup context and releases
    each port only after a successful stop.
  - `restoreSiteStateLocked` releases a FastCGI port only after a successful stop.
  - Running-site `PHPExec` uses the newly composed ini without persisting
    `PHPConfigHash`, so the next Apply cannot false-no-op.
  - Fixed the hash-branch port ordering and the Runtimes ARIA label.
- `6add708` — `fix: retain stopped PHP row port on cleanup`
  - `Stop` no longer frees the stopped row’s own FastCGI port; the row keeps it for
    reuse.
  - External delete releases retained ports only after the row deletion commits; a
    failed delete restores the row with its port still reserved.
  - The current-hash retain branch releases only non-row ports.

Scoped re-review verdict: all named findings **ADDRESSED**, no new Critical/Important
breakage. One new **Minor** was parked (non-blocking):

- When a retained generation equals the new current hash, the keep branch can return
  a non-row port for release even if that generation is still bound. Impact is limited
  because `Allocate` probes listeners and the row points at the new port. A safer
  future fix is to keep holding the entry/port until the backend confirms the process
  is stopped. Ledger: `Final residual fix: new Minor parked`.

Historical local artifacts (git-ignored):
`.superpowers/sdd/2026-10-23-dev-web-stack-plan-2-php/final-review-fixes.md`,
`final-review-fix-report.md`, `residual-fixes.md`, `residual-fix-report.md`.

### 1.2 Write and execute Plan 4 (packaging/release), skipping Plan 3

Plan 4 scope from the plan series (Linux-only while Windows is deferred):

- Release binaries and checksum manifests, with a release workflow.
- Upgrade path and versioning/protocol compatibility.
- Diagnostics bundle or support-oriented Doctor output, subject to the M1 spec.
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
to one fix wave. `b9f38c8` fixed most; the residual wave (`039f0b0`, `6add708`) closed
the rest.

| Finding | Status |
|---|---|
| **C1** Built-in proxy source disclosure via `.php.tmp`/`.php.swp` | **ADDRESSED** — suffixes added; proxy/native parity helper + tests. |
| **I1** Cleanup context/port ownership/prune live generation | **ADDRESSED** — retained map, later-apply drain, prune guard, `restoreSiteStateLocked` port gate, tests. |
| **I2** `memcached` missing igbinary dependency | **ADDRESSED** — `requires:["igbinary"]` + tests. |
| **I3** Stopped sites don't pick up edited defaults | **ADDRESSED** — compose/compare before reuse; stopped test passes. |
| **I4** 8.5 stale opcache override / poisoned defaults | **ADDRESSED** — overrides dropped on line switch; stored defaults/site maps sanitized; tests. |
| **I5** Dashboard Inherit baseline | **ADDRESSED** — `defaults` prop; SiteDetail/drawer pass edited baseline; tests. |
| **I6** Migration dialog moves unlisted same-line sites | **ADDRESSED** — union with `phpSitesOn(line)`, same-line target default, tests. |
| **I7** Apache HTTP configtest gated on mod_ssl | **ADDRESSED** — HTTP requires only `proxy_fcgi_module`; HTTP PASS, HTTPS SKIP. |
| Minor: hash-branch port leak | **ADDRESSED** — persist-before-release ordering + test. |
| Minor: builtin membership validation | **ADDRESSED**. |
| Minor: Line 0 / AddSite focus | **ADDRESSED**. |
| Minor: start store-failure cleanup | **ADDRESSED** — retained on failed stop. |
| Minor: absent runtime removal idempotence | **ADDRESSED**. |
| **New Important (first re-review)** | **ADDRESSED** — running-site `PHPExec` no longer repins the master; next Apply swaps. |
| **New Important (second re-review)** | **ADDRESSED** — `Stop`/failed delete keep the row-owned FastCGI port reserved. |
| **New Minor (parked)** | Current-hash retained branch can release a still-bound non-row port; non-blocking, see §1.1. |

All acceptance results remain valid at `6add708`; the full `go test ./... -race` and
UI unit/check gate pass. No Plan 2 merge blocker remains.

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
| Final review fix wave | Committed | `b9f38c8` |
| Residual review fixes | Committed, re-reviewed clean | `039f0b0`, `6add708` |

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

1. **Production PHP artifacts** — `internal/catalog/catalog.json` has no PHP entries.
   Task 11 CI has not published checksummed tarballs; the real 8.2/8.5 side-by-side
   is BLOCKED. Do not invent URLs/checksums.
2. **Apache HTTPS** — `mod_ssl` is absent on this workstation; Apache HTTP PHP config
   test passes, HTTPS vhost is SKIP. Real Apache serving remains manual.
3. **In-flight request continuity across a successful swap** — not measured.
4. **Cross-build migration** — acceptance migrates a same-path `php@8.4.4-r2`
   revision; it does not prove switching between different real builds (blocked on
   artifacts).
5. **`imap`** — build pipeline blocked/documented; UI may still offer the toggle
   until a later cleanup; enabling it will fail validation.
6. **Plan 1 manual blockers** — browser trust, fresh root init/uninstall/80/443, real
   Apache serving, live UI daemon, browser HMR/WebSocket remain manual.
7. **Windows/WSL** — entirely deferred; no agent, named pipe, Windows trust, or
   `install.ps1`.
8. **Parked Plan 2 Minor** — a retained generation equal to the new current hash can
   have a still-bound non-row port released without a confirmed stop. Non-blocking;
   see §1.1. Do not let it grow into a port leak if the area is touched again.

---

## 6. Verification / environment

Host: Fedora Linux 44 x86_64. Go 1.27.3; Node 22.23.1/npm 10.9.8.
PHP 8.4.4 CLI `/usr/local/php/bin/php`; FPM `/usr/local/php/sbin/php-fpm`.
nginx 1.28.0; Apache 2.4.69 (no mod_ssl); system nginx owns 80/443.

At `6add708`:
- `go test ./... -race -count=1` PASS (controller run, includes real-FPM acceptance).
- `go test ./...` PASS.
- `DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -v -count=1` PASS.
- `go test ./internal/backend ./internal/sites ./internal/proxy ./internal/phpfpm ./internal/phpini -v` PASS.
- `go test ./internal/vhost ./internal/helper ./internal/doctor` PASS.
- `cd ui && npm test -- --run` PASS (51 tests), `npm run check` PASS, `npm run build` PASS,
  `npx playwright test` 19/19 PASS (at `039f0b0`; `6add708` is Go-only).
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

At handoff time, `plan-2-php` was at `6add708`, two commits ahead of the then
origin `5ec96fd`. The branch was then pushed with this handoff and the approved
CI smoke fixes; its actual remote tip is now **`fec5917`**. The history below is
the handoff-time snapshot, before those two smoke fixes:

```
6add708 fix: retain stopped PHP row port on cleanup
039f0b0 fix: close plan 2 final review residuals
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

Current state (verified against `origin`):

```sh
git rev-parse origin/plan-2-php        # fec5917
git rev-parse plan-4-release           # local fix-wave tip; origin is 6cc78ce until push
git log --oneline -1 origin/plan-2-php # fix: load standard PHP modules in PECL smoke checks
```

Plan 2 is complete, merge-ready, and merged into `plan-4-release`. Plan 4 is
implemented for the Linux M1 scope. Do not start Plan 3 (Windows/WSL).
