# dev-web-stack — Plan 2 (PHP) Handoff / Status

- **Date:** 2026-10-24
- **Branch:** `plan-2-php` (created from `plan-1-foundation` at `4f7ab97`)
- **Remote:** `origin` (`git@github.com:AramZahedi/dev-web-stack.git`)
- **Head before this handoff commit:** `1b9221a` (`fix: bound PHP FastCGI health attempts by the readiness deadline`)
- **Origin `plan-2-php` before this handoff:** `988d4d8`; this handoff adds the next commit and must be pushed.
- **Origin `plan-1-foundation`:** `4f7ab97` (Plan 1 complete, pushed).
- **Working tree at handoff:** clean.
- **Paused before:** Plan 2 **Task 5**. Task 5 has not been started.
- **Plan 1:** Tasks 1–26 complete and reviewed; automated acceptance PASS;
  manual browser/root/Apache/uninstall checks blocked.

## Plan 2 status

| Task | Status | Commits |
|---|---|---|
| 1. PHP build pinning/store/catalog/extension metadata | **Complete, reviewed** | `8f1da67`, `f1ed063` |
| 2. `php.ini` composition/validation | **Complete, reviewed** | `d0c50ff`, `0499c52` |
| 3. FastCGI client + php-fpm config | **Complete, reviewed** | `376f5cb`, `926bfd0` |
| 4. Local backend PHP process/build resolution | **Complete, reviewed** | `78e0f17`, `1b9221a` |
| 5. PHP site create/start/stop/delete/vhost publication | **Not started** | — |
| 6. Validate-then-swap version/ini/extensions | Not started | — |
| 7. Built-in proxy PHP/FastCGI transport | Not started | — |
| 8. PHP nginx/Apache vhosts/helper/Doctor | Not started | — |
| 9. REST API, typed client, CLI | Not started | — |
| 10. Dashboard PHP screens/editing | Not started | — |
| 11. PHP build pipeline/CI/catalog generator/docs | Not started | — |
| 12. Plan 2 acceptance | Not started | — |

**Do not redo Tasks 1–4.** The next step is Task 5 only.

---

## 1. Exact next step — implement Plan 2 Task 5

Start from the current `plan-2-php` tip (after this handoff is pushed):

```sh
cd /path/to/dev-web-stack
git fetch origin
git switch plan-2-php
git pull --ff-only
go test ./...                       # should pass
BASE=$(git rev-parse HEAD)          # record this for the Task 5 review package
```

Generate the Task 5 brief:

```sh
bash <superpowers>/skills/subagent-driven-development/scripts/task-brief \
  docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md 5
```

Dispatch **one** implementer with:

- The printed Task 5 brief as the single source of requirements.
- Context from Tasks 1–4 (interfaces below), not the whole session.
- Report path:
  `.superpowers/sdd/2026-10-23-dev-web-stack-plan-2-php/task-5-report.md`
- Model **`opencode-go/deepseek-v4.1-flash#max`**.
- No subagents/reviewer for the implementer.
- Commit at the end; then generate the review package:

```sh
bash <superpowers>/skills/subagent-driven-development/scripts/review-package \
  docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md \
  "$BASE" "$(git rev-parse HEAD)"
```

Dispatch a fresh task reviewer on **`opencode-go/deepseek-v4.1-flash#max`**
with the task brief, implementer report, diff package, Global Constraints,
and Review Focus. Run the fix loop (max five rounds) before Task 6.

---

## 2. What is implemented (Tasks 1–4)

### Task 1 — PHP build pinning, store schema, revisioned catalog, extension metadata

Commits: `8f1da67` (`feat: pin PHP builds by revision and add extension metadata`),
`f1ed063` (`fix: return intact installed PHP builds before catalog lookup`).

- `internal/model`: `Site.PHPBuild`, `PHPIniRaw`, `PHPConfigHash`; existing
  `PHPVersion`, `PHPIni`, `PHPExtensions` retained.
- `internal/store/migrations/0003_php_build.sql`: `sites.php_build`,
  `sites.php_ini_raw`, `sites.php_config_hash`, `site_php_config.applied_at`.
  Forward-only migration; upgrade from a real 0001/0002 DB is tested.
- Store methods for PHP defaults and per-site applied config; JSON persistence.
- Revisioned catalog IDs:
  - Node: `node@24.22.1` (revision 0).
  - PHP: `php@8.4.4-r1` (revision ≥ 1).
  - `ParseID`/`FormatID`; `ForKindVersion` newest revision;
    `NewestForLine` newest version then revision; `Find` exact.
- `internal/catalog/php.json` + `php.go`: embedded PHP extension manifest:
  builtins, standard/extras, `.so` names, defaults, supported versions,
  dependencies; `filter` is built-in; `imap` only 8.2/8.3; opcache/xdebug zend;
  redis/apcu require igbinary; unknown/builtin/unsupported/cycle rejection.
- Runtime paths: PHP `<version>-r<revision>-<build>`; Node paths preserved.
  `NewManagerWithCatalog` is the fixture seam. `EnsureBuild` is **store-first**:
  an intact installed build is returned even if the current catalog no longer
  lists it; catalog install/repair only for missing/stale paths.
- **Production `internal/catalog/catalog.json` still contains no PHP entries.**
  Do not invent PHP URLs/checksums. CI Task 11 must publish real artifacts.

Deferred minors: `Ensure` revision ambiguity; `InstalledForLine` does not
filter backend/verify; `ParsePHP` does not require every supported line; some
false-value assertions; manifest test gaps; `manager.go` ~1,507 lines/duplicate
comparator; `intactInstalledBuild` trusts manifest presence without full
`Verify`.

### Task 2 — `php.ini` composition and validation

Commits: `d0c50ff` (`feat: compose and validate per-site php.ini`), `0499c52`
(`fix: scope php.ini managed blocks and CA aliases`).

- `internal/phpini`:
  - `Compose`, `ExtractSiteSection`, `Parse`, `ValidateOverrides`,
    `ValidateRaw`, `ErrInvalidConfig`, `ValidationError`, `LintError`,
    `Runner`, `ExecRunner`, `Lint`.
  - Order: base → `[PHP]` reset → sorted version defaults → sorted site
    overrides → raw verbatim → `[PHP]` reset → managed extension block.
  - Managed block: `extension_dir`, `extension=`, `zend_extension=`; these and
    the markers are rejected from raw/overrides.
  - `curl.cainfo`/`openssl.cafile`: emitted unless **effective** version
    defaults/overrides set them; base/raw do not suppress them.
  - Injection rejection: NUL/CR/LF, invalid keys, managed directives/markers.
  - Real PHP lint with `PHP_INI_SCAN_DIR=`; 10s timeout; missing/non-regular ini
    is rejected before invoking PHP; line-number parsing.
- `ExtractSiteSection` returns the sorted overrides **plus** raw text. Callers
  must not re-supply the same overrides when feeding it back as `Config.Raw`.

Deferred minors: `#` comments/CRLF raw rejected; `Parse` returns quoted
literals; real OPcache probe assumes shared opcache.

### Task 3 — FastCGI client and php-fpm configuration

Commits: `376f5cb` (`feat: add FastCGI client and php-fpm configuration`),
`926bfd0` (`fix: bound FastCGI headers and escape FPM values`).

- `internal/fcgi`:
  - FastCGI v1 responder, one request per connection; `BEGIN_REQUEST`,
    chunked params (`nameLen,valueLen,name,value`; 1-byte/4-byte lengths),
    STDIN 32 KiB chunks, empty terminators, `ABORT_REQUEST` on cancel,
    `STDOUT`/`STDERR`/`END_REQUEST`.
  - CGI `Status:` default 200, headers, blank line; protocol status 0/1/2/3;
    body streamed; bounded stderr (64 KiB); context cancellation closes the
    connection and returns the context error.
  - **Header bounds:** per-line 256 KiB, total 1 MiB. A newline-free over-cap
    line is rejected before unbounded buffering. `readHeaderLine` is the
    bounded reader.
  - Real php-fpm interop test guards the name/value order.
- `internal/phpfpm`:
  - `Config`, `Render`, `Argv`, `ErrConfigInvalid`, `Validate`.
  - `[global]` pid/error_log/daemonize=no/log_level=notice; `[www]` listen,
    dynamic pool exact values, catch output, clear_env=no, chdir,
    security.limit_extensions=.php, optional user/group, sorted env.
  - Quote/escape paths/env, including literal `$`; reject NUL/CR/LF, non-IP
    listen hosts, ports outside 43000–43999, relative paths.
  - `Argv`: `[fpmBin, "--nodaemonize", "--fpm-config", configPath, "-c", iniPath]`.
  - `Validate`: 10s, `PHP_INI_SCAN_DIR=`, missing file pre-check, real-FPM
    output scan rejecting `PHP:` syntax errors / `ERROR:` even when php-fpm
    exits 0; accept notice-only success.
- Task 3 concerns: ABORT best effort; `/proc/self/fd/2` cannot be opened by this
  machine's php-fpm (pass a real log path in Task 4+); `Response.Status` is the
  reason phrase, `StatusCode` numeric.

Deferred minors: stdin goroutine can remain blocked on caller `Body`; invalid
UTF-8 path bytes replaced; empty `Status:` treated as absent; some test timer/
deadlock/timeout robustness; test-only helpers in `records.go`; large
`client_test.go`; unanchored `] ERROR:` match; real `-tt` test lacks timeout.

### Task 4 — Local backend PHP process path and build resolution

Commits: `78e0f17` (`feat: run per-site php-fpm masters in the local backend`),
`1b9221a` (`fix: bound PHP FastCGI health attempts by the readiness deadline`).

- `backend.SiteSpec` gained: `PHPVersion`, `PHPBuild`, `PHPBuildPath`,
  `PHPIniPath`, `FPMConfigPath`, `PHPHealthScript`.
- `RuntimeProvider.EnsureBuild(ctx, buildID)` and
  `Backend.EnsureRuntimeBuild(ctx, buildID)`; `Local` delegates to the runtime
  provider.
- `backend.PHPStager`:
  `PreparePHP`, `StartPHP`, `StopPHP`.
- `internal/backend/php.go`:
  - `Prepare` for PHP stores generation `default`; `PreparePHP` stages other
    generations in memory.
  - Supervisor IDs: default = bare site ID; other = `<siteID>#<generation>`.
    Latest successfully started generation is active; `StopPHP` promotes the
    most recently started remaining generation; `Stop` stops every generation.
  - Validation: non-empty build ID; executable `bin/php`/`sbin/php-fpm`;
    clean absolute ini/FPM/health paths; FastCGI port 43000–43999; safe
    generation; site ID/kind; document root exists.
  - Start: `phpfpm.Argv`, build `bin` first on PATH,
    `PHP_INI_SCAN_DIR=`, document-root cwd, TCP readiness, FastCGI health poll
    up to 30s, logs source `fpm`. Failure stops the master, releases the port,
    and includes the last 200 log lines.
  - **Health deadline fix:** overall 30s context; each FastCGI attempt gets a
    2s budget capped by the remaining overall budget and is canceled after the
    attempt; an unresponsive master fails within the deadline.
  - `Exec`: build `bin/php`, build bin first on PATH, `PHP_INI_SCAN_DIR=`,
    `PHPRC=<generated ini>` (Task 5's explicit `-c` still wins), site cwd.
- `internal/runtime/php_smoke.go`:
  - `var phpSmoke = smokePHPBuild` test hook.
  - Runs `bin/php -v`, `sbin/php-fpm -v`, starts a temp FPM on a free
    43000–43999 port with temp ini/config/health, sends one FastCGI request,
    requires `200 ok`, stops the process group.
  - `installEntry`: smoke after extraction/manifest, before atomic rename;
    failure removes temp tree, no runtime row. Success registers.
- Conformance/fakes updated for the new methods; `internal/sites/fakes_test.go`
  has the new backend method/seams.
- Task 4 concerns: `PHPRC` is used for the brief's bare `php -r` test; smoke
  port bind/release has a test-only race; staged non-default generations are
  in-memory only across daemon restarts; `StopPHP` retains the prepared entry
  (stopped), promotion ignores it; `phpSmoke` is a global hook (tests restore
  it, no `t.Parallel()` in affected tests). Install lock is held during smoke.

Deferred minors: health-failure stop error ignored and state cleared on stop
failure; duplicate default stop/single context across generations; `PreparePHP`
can overwrite a running generation's spec; `Exec` prefers prepared over staged;
unsafe site IDs accepted until Start; mismatch branches untested; failure test
lacks deferred cleanup; test port bind/release races; always-nil
`phpProcessSpec` error; large `php.go`/`php_test.go`/`php_smoke.go`; caller-
cancel error classification in `waitPHPHealth`.

---

## 3. Plan 2 binding architecture and rulings

- Sites pin exact build IDs (`PHPBuild`), not just versions. `PHPVersion` is the
  line/version; `PHPBuild` is the revisioned ID.
- One `php-fpm` master per running site. Config is
  `sites/<id>/<config-hash>/php-fpm.conf`; FastCGI TCP port 43000–43999;
  Node HTTP ports remain 41000–42999.
- FPM exact pool: dynamic, max_children 5, start_servers 2, min_spare 1,
  max_spare 3; readiness 30s; stop grace 10s; restart policy as Plan 1.
- `php.ini` order and markers as Task 2; managed extension block/dir read-only in
  the raw editor. Required baseline values follow spec §11.3.
- Extensions: standard defaults except `ffi`; extras default off; `imap`
  8.2/8.3 only; opcache/xdebug zend; redis/apcu require igbinary; dependency
  order from catalog metadata; no independent PECL updates in M1.
- Validation: `php -c <ini> -r 'exit(0);'` + `php-fpm -t`; errors block apply;
  failed apply leaves old master serving.
- `phpSmoke` must pass before a PHP build is registered.
- **Artifact gate:** production `catalog.json` has no PHP entries until CI
  publishes checksummed archives. Task 11 builds/smoke-tests/checksums/SBOMs/
  uploads; release job generates entries. Tests use fixture catalogs/tarballs;
  acceptance may use `/usr/local/php` and must mark managed-artifact checks
  blocked if no published build exists.
- Preflight rulings already recorded:
  - Interface changes update every compile-time fake in the same task.
  - No invented production PHP URLs/checksums.
  - Task 12 may mark managed-artifact checks blocked if CI artifacts are absent.
  - `PHPIni`/`PHPIniRaw` persist together; raw is authoritative for comments/
    unknown directives; managed extension block is generated only by
    `phpini.Compose`.
  - `filter` is built-in and not toggleable.
- All subagents must use `opencode-go/deepseek-v4.1-flash#max`.

---

## 4. Plan 1 baseline and manual blockers

Plan 1 is pushed at `4f7ab97` on `plan-1-foundation`:

- Go CLI/daemon/helper, SQLite v2 (now v3), logs/SSE, supervisor, builtin
  proxy/static, Node runtimes, certs/SNI, hosts/CA/trust, forwarders, Doctor,
  REST API, Svelte dashboard, Linux install/systemd, CI, real high-port nginx
  E2E.
- Optional helper-managed native nginx/Apache vhosts with one `serving_mode`,
  atomic releases, bridge hashes, mode switching, CLI/API/UI/Doctor, high-port
  E2E.
- Automated acceptance PASS: `go test ./...`, `go test -race ./...`, `go vet`,
  `make build`, E2E, UI 27 tests + 18 Playwright, shellcheck, install dry-run,
  systemd verify.
- **Manual acceptance blocked/partial:** real Chrome/Firefox trust (no
  certutil/CA install), fresh init/uninstall/root 80/443 (active system nginx,
  system changes), real Apache (`mod_ssl` absent), live UI against a real
  daemon, browser HMR/WebSocket. See
  `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md`.
- Deep details: `docs/superpowers/status/2026-10-22-plan-1-handoff.md`.

---

## 5. Environment and verification

- Host: Fedora Linux 44 x86_64.
- Go 1.27.3; Node 22.23.1/npm 10.9.8.
- PHP 8.4.4 CLI `/usr/local/php/bin/php`; FPM
  `/usr/local/php/sbin/php-fpm`; extension dir under
  `/usr/local/php/lib/php/extensions/...`.
- nginx 1.28.0; Apache 2.4.69 installed but no `mod_ssl`; system nginx owns
  80/443 on this workstation.
- At `1b9221a`: **`go test ./...` PASS**.
- Task 4 targeted/full/race/vet/gofmt and real-FPM tests: PASS.
- Task 3 targeted/race/vet and real-FPM validation: PASS.
- Task 2 real-PHP lint: PASS.
- No Plan 2 E2E exists yet; Plan 1 E2E still covers builtin + external nginx.

Commands:

```sh
go test ./...
go test -race ./internal/fcgi ./internal/phpfpm ./internal/backend ./internal/runtime
go vet ./...
cd ui && npm ci && npm test && npm run check && npm run build
make build
E2E_SKIP_BUILD=1 bash test/e2e/linux.sh   # Plan 1 builtin + nginx vhost E2E
```

The local SDD ledger/reports are git-ignored under
`.superpowers/sdd/2026-10-23-dev-web-stack-plan-2-php/`. They will not exist on
another machine; regenerate briefs/reports from the plan and git history.

---

## 6. Repository map (current)

```
cmd/devstack/            CLI + daemon
cmd/devstack-helper/     privileged helper
internal/config/         config/paths (serving_mode; external requires 7070)
internal/model/          Site (PHPBuild/PHPIniRaw/PHPConfigHash), RuntimeBuild
internal/store/          SQLite + migrations 0001-0003; PHP defaults/site config
internal/logs/           rotation/ring/SSE/redaction
internal/supervisor/     process groups/readiness/backoff
internal/backend/        local static/Node/PHP; PHPStager; runtime smoke
internal/helperproto/    RPC + vhost types
internal/helper/         auth, hosts, CA/trust, forwarders, vhost manager
internal/helperclient/   helper RPC client
internal/forwarder/      80/443 forwarders
internal/proxy/          router/TLS/HTTP upstream; PHP FastCGI Task 7
internal/staticserve/    static handler
internal/certs/          certs/renewal/PEM
internal/catalog/        Node catalog + php.json manifest; no production PHP entries
internal/runtime/        downloads/extraction/verify; revisioned PHP; phpSmoke
internal/vhost/          nginx/Apache renderers; PHP templates Task 8
internal/sites/          lifecycle/external mode; PHP lifecycle Tasks 5-6
internal/doctor/         diagnostics/repair
internal/api/            REST/SSE/auth/UI
internal/daemon/         startup/shutdown/reconcile
internal/client/         Unix-socket client
internal/cli/            Cobra commands
internal/phpini/         php.ini compose/validate
internal/fcgi/           FastCGI client/records
internal/phpfpm/         php-fpm config/render/validate
ui/                      Svelte 5 dashboard + mock/real API + Playwright
api/openapi.yaml         API contract
build/systemd/           units
docs/                    install/sites/tls/vhosts + superpowers plans/specs
test/e2e/linux.sh        Plan 1 builtin + high-port nginx E2E
install.sh               Linux installer
```

Defaults: `~/.devstack`; proxy 8080/8443; UI/API 7070; helper socket
`/run/devstack-helper/helper.sock`; helper state `/var/lib/devstack-helper`;
vhost tree `/etc/devstack/vhosts`; TLS `/etc/devstack/tls`; Node 41000–42999;
FastCGI 43000–43999.

---

## 7. Remaining work

### Plan 2

- **Task 5:** PHP site creation/start/stop/deletion, build resolution,
  FastCGI port allocation, store/route update, vhost publication. This is the
  next task. It consumes `PHPStager`, `SiteSpec` PHP fields, runtime
  `EnsureBuild`, and Plan 1 site/vhost services.
- **Task 6:** validate-then-swap version/build/ini/extensions; new master,
  health, store/route/vhost atomicity, rollback; port/process cleanup.
- **Task 7:** built-in proxy PHP route and FastCGI transport; SCRIPT_FILENAME/
  PATH_INFO/index/front controller; source containment.
- **Task 8:** PHP nginx `fastcgi_pass` and Apache `proxy:fcgi` vhosts, helper
  validation, Doctor.
- **Task 9:** REST API, typed client, CLI (`site`, `php --site`, runtimes).
- **Task 10:** dashboard PHP screens/editing, design-system compliant.
- **Task 11:** `build/php/` relocatable builds 8.2–8.5, CI matrix, smoke tests,
  checksums/SBOM, catalog generator, docs.
- **Task 12:** Plan 2 acceptance with real FPM/FastCGI/ini/extensions/version
  switch rollback and external templates; mark managed-artifact portions blocked
  if no published PHP build exists.

### Plan 3 — Windows/WSL2

Not written. Scope: managed Debian 12 WSL distro, `devstack-agent`, process/PHP
FPM inside WSL, path translation, Windows helper named pipe, cert/trust stores,
Firefox policy, `install.ps1`, WSL lifecycle, conformance. External vhosts are
Linux-only.

### Plan 4 — Packaging/release

Not written. Scope: signed binaries/packages, release workflow, upgrade,
diagnostics bundle, docs polish, full M1 acceptance.

---

## 8. Push state

Before this handoff, `plan-2-php` was at `988d4d8` on the remote. This handoff
adds one docs commit on top of:

```
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

Paused — do not start Task 5 until the next session resumes.
