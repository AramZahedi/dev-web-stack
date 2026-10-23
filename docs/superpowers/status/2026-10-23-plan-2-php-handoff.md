# dev-web-stack — Plan 2 (PHP) Handoff / Status

- **Date:** 2026-10-23
- **Branch:** `plan-2-php` (created from `plan-1-foundation` at `4f7ab97`)
- **Remote:** `origin` (`git@github.com:AramZahedi/dev-web-stack.git`)
- **Head before this handoff commit:** `376f5cb` (`feat: add FastCGI client and php-fpm configuration`)
- **Origin `plan-1-foundation`:** `4f7ab97` (Plan 1 complete, pushed)
- **Working tree:** clean.
- **Plan 2 state:**
  - **Task 1 — complete and reviewed** (`8f1da67` + `f1ed063`)
  - **Task 2 — complete and reviewed** (`d0c50ff` + `0499c52`)
  - **Task 3 — implemented and committed (`376f5cb`), NOT YET REVIEWED**
  - **Tasks 4–12 — not started**
- **Plan 1 state:** Tasks 1–26 complete and reviewed; automated acceptance PASS;
  manual browser/root/Apache/uninstall checks blocked. See
  `docs/superpowers/status/2026-10-22-plan-1-handoff.md`.

**Do not redo Plan 1 or Plan 2 Tasks 1–2.** The next step is the Task 3 review.
Do not start Task 4 until Task 3’s review/fix loop is closed.

---

## 1. Exact next step — review Plan 2 Task 3

The Task 3 implementer committed, but no fresh reviewer has seen it.

```sh
cd /path/to/dev-web-stack
git fetch origin
git switch plan-2-php
git pull --ff-only
go test ./...                     # should pass
```

Generate the review package:

```sh
bash <superpowers>/skills/subagent-driven-development/scripts/review-package \
  docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md \
  0499c52 376f5cb
```

Then dispatch a **fresh task reviewer** with:

- Task brief: regenerate with
  `bash <superpowers>/skills/subagent-driven-development/scripts/task-brief \
  docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md 3`
  (the previous local brief/report are git-ignored and will not exist on another
  machine).
- Diff package: the path printed above.
- Requirements: Plan 2 Task 3 + Global Constraints + Review Focus (especially
  FastCGI framing/cancellation, FPM config injection, real-FPM validation).
- Implementer’s known concerns (below).
- Model: **`opencode-go/deepseek-v4.1-flash#max`**.
- Reviewer must not mutate the tree or dispatch subagents.

If the review finds Critical/Important issues, resume the original Task 3
implementer if that session exists; otherwise dispatch a fresh implementer with
the task brief, the review findings, and the report file. After fixes, run one
scoped re-review of the fix diff. Only then mark Task 3 complete and start Task 4.

### Task 3 implementer report (the local git-ignored report is gone on another machine)

Commit: `376f5cb330a45315e614b878b243fe3f7f753463`.
Files: `internal/fcgi/{records.go,client.go,records_test.go,client_test.go,testserver_test.go}`,
`internal/phpfpm/{config.go,render.go,validate.go,config_test.go}`.

Implemented:
- FastCGI v1 responder/client, one request per connection; chunked PARAMS/STDIN;
  STDOUT/STDERR/END_REQUEST; streamed body; CGI status/header parsing; protocol
  errors; 64 KiB stderr cap; cancellation closes the connection and returns
  `context.Canceled` in <1s; 1 MiB header cap; body-length enforcement.
- `phpfpm.Config/Render/Argv/Validate`: `[global]` pid/error_log/daemonize=no/
  log_level=notice; `[www]` listen, dynamic pool exact values, catch output,
  clear_env=no, chdir, security.limit_extensions=.php, optional user/group,
  sorted env; quoting/escaping; rejects newline/NUL/relative paths/bad ports
  (43000–43999)/non-IP hosts; `Argv` exact; `Validate` runs
  `fpmBin -t -y conf -c ini` with `PHP_INI_SCAN_DIR=` and 10s timeout.
- 38 top-level tests (72 with subtests), no skips; real `/usr/local/php/sbin/php-fpm`
  validation passed. Coverage fcgi 86.1%, phpfpm 92.9%.
- `go test ./...`, `go test -race` on both packages, `go vet`, gofmt: PASS.

Known Task 3 notes:
1. FastCGI name-value ordering was initially wrong; real php-fpm/stdlib interop
   caught it. The committed implementation is `nameLen, valueLen, name, value`
   and has a permanent stdlib interop test.
2. ABORT_REQUEST is best effort if the peer reset the connection; the connection
   still closes and `Do` returns the context error.
3. `/proc/self/fd/2` is accepted by `Render` but this machine’s php-fpm cannot
   open it; real validation uses a temp log file. Task 4 should pass a real log
   path when wiring site logs.
4. `Validate` pre-checks both ini/config files because php-fpm exits 0 for a
   missing `-c` file.
5. `Response.Status` is the reason phrase (`OK`), `StatusCode` is numeric; Task 7
   uses `StatusCode`.

---

## 2. Plan 2 task status and key decisions

Plan file: `docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-php.md`
(12 tasks, 1549 lines). Spec: M1 §10.2/§11. Branch `plan-2-php`.

| Task | Status | Commit(s) |
|---|---|---|
| 1. PHP build pinning/store/catalog/extension metadata | Complete, reviewed | `8f1da67`, `f1ed063` |
| 2. `php.ini` composition/validation | Complete, reviewed | `d0c50ff`, `0499c52` |
| 3. FastCGI client + php-fpm config | **Implemented, unreviewed** | `376f5cb` |
| 4. Local backend PHP process path/build resolution | Not started | — |
| 5. PHP site creation/start/stop/delete/vhost publication | Not started | — |
| 6. Validate-then-swap version/ini/extensions | Not started | — |
| 7. Built-in proxy PHP/FastCGI transport | Not started | — |
| 8. PHP nginx/Apache vhosts, helper validation, Doctor | Not started | — |
| 9. REST API, typed client, CLI | Not started | — |
| 10. Dashboard PHP screens/editing | Not started | — |
| 11. PHP build pipeline/CI/catalog generator/docs | Not started | — |
| 12. Plan 2 acceptance | Not started | — |

**Global decisions already made/ruled:**
- Sites store `PHPVersion` (line/version) and a new `PHPBuild` exact build ID
  (`php@8.4.4-r1`); migration `0003_php_build.sql` also adds `PHPIniRaw` and
  `PHPConfigHash` and `site_php_config.applied_at`.
- Revisioned catalog: Node `node@24.22.1` revision 0; PHP `php@x.y.z-rN`, N≥1.
  `ForKindVersion` newest revision; `NewestForLine` newest version/revision.
- **Production `catalog.json` has no PHP entries yet.** PHP artifacts must be
  built/published by CI (Task 11) before real URLs/checksums are added. Do not
  invent them. Tests use fixture catalogs/tarballs; the local machine has real
  PHP 8.4.4 at `/usr/local/php` (CLI + `sbin/php-fpm`).
- Embedded `php.json` has built-ins/standard/extras, defaults, versions,
  `filter` as built-in only, `imap` 8.2/8.3 only, opcache/xdebug zend, redis/apcu
  require igbinary, dependency ordering/cycle rejection.
- `EnsureBuild` is store-first: an intact installed build is returned even if the
  current catalog no longer lists it.
- `php.ini` order: base → `[PHP]` reset → version defaults → site overrides →
  raw → `[PHP]` reset → managed extension block. CA aliases are emitted unless
  effective defaults/overrides set them. Managed extension/dir lines are never
  accepted from raw. `ExtractSiteSection` returns overrides+raw (callers must not
  re-supply the same overrides).
- FPM: one master per running site, TCP 43000–43999, exact pool values, health
  probe, validate-then-swap; build base/version defaults/site overrides.
- FastCGI is implemented in-tree; no new dependency.
- Build artifacts are a CI/release gate; acceptance may use local real PHP/FPM.
- All subagents must use `opencode-go/deepseek-v4.1-flash#max`.

### Task 1 deferred minors (not blockers)
`Ensure` revision ambiguity; `InstalledForLine` does not filter backend/verify;
`ParsePHP` does not require every supported line; some false-value assertions;
manifest test gaps; manager file ~1,507 lines/duplicate comparator;
`intactInstalledBuild` trusts manifest presence without full `Verify`.

### Task 2 deferred minors
`ExtractSiteSection` returns overrides+raw; `#` comments and CRLF raw are
rejected; `Parse` returns quoted literals; real OPcache probe assumes shared
opcache; the top of the task report is pre-fix.

---

## 3. Plan 1 — completed baseline

Plan 1 is implemented and pushed at `4f7ab97` on `plan-1-foundation`:

- Go CLI/daemon/helper, SQLite v2, logs/SSE, supervisor, builtin proxy/static,
  Node runtimes, certs/SNI, hosts/CA/trust, forwarders, Doctor, REST API,
  Svelte dashboard, Linux install/systemd, CI, real high-port nginx E2E.
- Optional helper-managed native nginx/Apache vhosts with `serving_mode`,
  atomic releases, bridge hashes, mode switching, CLI/API/UI/Doctor, and
  high-port E2E.
- Automated acceptance PASS: `go test ./...`, `go test -race ./...`, `go vet`,
  `make build`, E2E, UI 27 tests + 18 Playwright, shellcheck, install dry-run,
  systemd verify.
- **Manual acceptance blocked/partial:** real Chrome/Firefox trust (no
  certutil/CA install), fresh init/uninstall/root 80/443 (active system nginx,
  system changes), real Apache (`mod_ssl` absent), live UI against a real daemon,
  and browser HMR/WebSocket. See `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md`.
- Plan 1 final review found four production blockers (systemd bridge paths,
  nginx default_server, worker readability, fatal reconcile) and five important
  gaps; all were fixed in `d2db8f8`/`70844ca` and re-reviewed clean.

Deep Plan 1 details and environment: `docs/superpowers/status/2026-10-22-plan-1-handoff.md`.

---

## 4. Environment and verification

- Host: Fedora Linux 44 x86_64.
- Go 1.27.3; Node 22.23.1/npm 10.9.8.
- PHP 8.4.4 CLI at `/usr/local/php/bin/php`; FPM at
  `/usr/local/php/sbin/php-fpm`; extension dir under
  `/usr/local/php/lib/php/extensions/...`.
- nginx 1.28.0 available; Apache 2.4.69 installed but no `mod_ssl`; system nginx
  owns 80/443 on this workstation.
- Current `go test ./...` at `376f5cb`: **PASS**.
- Task 3 targeted/race/vet/gofmt: PASS per report.
- Plan 1 E2E previously passed; no Plan 2 E2E exists yet.

Useful commands:

```sh
go test ./...
go test -race ./internal/fcgi ./internal/phpfpm
go vet ./...
cd ui && npm ci && npm test && npm run check && npm run build
make build
E2E_SKIP_BUILD=1 bash test/e2e/linux.sh   # Plan 1 builtin + nginx vhost E2E
```

---

## 5. Repository map (current)

```
cmd/devstack/            CLI + daemon
cmd/devstack-helper/     privileged helper
internal/config/         config/paths (serving_mode; external requires 7070)
internal/model/          Site (PHPBuild/PHPIniRaw/PHPConfigHash now), RuntimeBuild, ...
internal/store/          SQLite + migrations 0001-0003; PHP defaults/site config
internal/logs/           rotation/ring/SSE/redaction
internal/supervisor/     process groups/readiness/backoff
internal/backend/        local static/Node; PHP task 4
internal/helperproto/    RPC + vhost types
internal/helper/         auth, hosts, CA/trust, forwarders, vhost manager
internal/helperclient/   helper RPC client
internal/forwarder/      80/443 forwarders
internal/proxy/          router/TLS/HTTP upstream; PHP FastCGI task 7
internal/staticserve/    static handler
internal/certs/          certs/renewal/PEM
internal/catalog/        Node catalog + php.json manifest (no production PHP entries)
internal/runtime/        downloads/extraction/verify; revisioned PHP builds
internal/vhost/          nginx/Apache renderers (PHP templates task 8)
internal/sites/          lifecycle/external mode; PHP lifecycle tasks 5-6
internal/doctor/         diagnostics/repair
internal/api/            REST/SSE/auth/UI
internal/daemon/         startup/shutdown/reconcile
internal/client/         Unix-socket client
internal/cli/            Cobra commands
internal/phpini/         php.ini compose/validate (Task 2)
internal/fcgi/           FastCGI client/records (Task 3)
internal/phpfpm/         php-fpm config/render/validate (Task 3)
ui/                      Svelte 5 dashboard + mock/real API + Playwright
api/openapi.yaml         API contract
build/systemd/           units
docs/                    install/sites/tls/vhosts + superpowers plans/specs
test/e2e/linux.sh        Plan 1 builtin + high-port nginx E2E
install.sh               Linux installer
```

---

## 6. Remaining work

### Plan 2 (PHP)
Tasks 4–12 in the plan. Critical upcoming interfaces:
- Task 4 changes `RuntimeProvider`/`Backend` and `SiteSpec`; update every
  compile-time fake in that same task.
- Task 5/6 need `PHPCommands`, `PHPStager`, `PHPManifest`, config generation,
  validate-then-swap, and rollback.
- Task 7 `KindPHP` + FastCGI route; Task 8 PHP vhost templates/helper/Doctor.
- Task 9 API/client/CLI; Task 10 UI; Task 11 build pipeline/CI; Task 12 acceptance.
- Production PHP catalog publication is a release gate, not something to fake.

### Plan 3 — Windows/WSL2
Not written. Scope: managed Debian 12 WSL distro, `devstack-agent`, process/PHP
FPM inside WSL, path translation, Windows helper named pipe, cert/trust stores,
Firefox policy, `install.ps1`, WSL lifecycle, conformance. External vhosts are
Linux-only.

### Plan 4 — Packaging/release
Not written. Scope: signed binaries/packages, release workflow, upgrade,
diagnostics bundle, docs polish, full M1 acceptance.

---

## 7. Push state

Before this handoff, only `plan-1-foundation` was on the remote (`4f7ab97`).
`plan-2-php` must be pushed with the handoff commit:

```sh
git push -u origin plan-2-php
git fetch origin
git rev-list --left-right --count origin/plan-2-php...HEAD   # expect 0 0
```

The handoff commit is the tip after `376f5cb`; use `git log` for its SHA. The
local SDD ledger/reports under `.superpowers/sdd/2026-10-23-dev-web-stack-plan-2-php/`
are git-ignored and will not exist on another machine; the plan and this handoff
carry the state.
