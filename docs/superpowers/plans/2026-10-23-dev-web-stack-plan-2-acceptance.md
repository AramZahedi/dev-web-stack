# Plan 2 Acceptance Report

> **Later completion note (2026-10-25):** the managed-artifact and production
> catalog gates recorded here as BLOCKED were completed in Plan 4. Real 8.2.35,
> 8.3.36, 8.4.27, and 8.5.12 archives are published, the catalog contains them,
> Apache HTTPS and in-flight PHP continuity pass, and the final Linux acceptance
> is 19 PASS / 0 FAIL / 1 SKIP / 1 BLOCKED. See the
> [Plan 4 acceptance report](2026-10-24-dev-web-stack-plan-4-acceptance.md).

- **Date:** 2026-10-24
- **Base revision:** `65ad1bf` (`docs: record plan 2 status through task 11`), plus
  the acceptance test, review-fix, and documentation commits in this change set.
- **Plan:** [Plan 2 — PHP](2026-10-23-dev-web-stack-plan-2-php.md)
- **Scope:** the Plan 2 subset of M1 design §21 plus the rollback, template, and
  Doctor checks named in Task 12.
- **Overall result:** **automated acceptance PASS for the Plan 2 paths on
  system PHP 8.4.4; Apache HTTP PHP configtest PASS, Apache HTTPS vhost SKIP
  (no `mod_ssl`).** The managed-artifact gate (real PHP 8.2 and 8.5 side by
  side, production catalog publication) is **BLOCKED** on the Task 11 CI
  artifacts. This report does not claim production PHP installation works.
- **Post-acceptance verification:** the final-review fixes (`b9f38c8`, `039f0b0`,
  `6add708`) were followed by a full `go test ./... -race -count=1` rerun at
  `6add708` (PASS, including the real-FPM acceptance) and UI Vitest/check at
  `039f0b0` (51 tests PASS, 0 errors). The acceptance paths themselves are
  unchanged; the artifact and Apache HTTPS gates below remain the only blockers.

## Environment

| Item | Value |
|---|---|
| OS | Fedora Linux 44, x86_64 |
| User | `uid=1000` (unprivileged; PHP acceptance skips as root) |
| PHP | **8.4.4** at `/usr/local/php` (`bin/php`, `sbin/php-fpm`) |
| PHP extension dir | `/usr/local/php/lib/php/extensions/no-debug-non-zts-20240924` |
| Filtered shared extensions | `imagick`, `opcache`, `pcov`, `pdo_pgsql`, `redis`, `xdebug`, `zip` |
| Filtered built-in modules | manifest built-ins plus lowercased `php -m` modules with no matching `.so` in the extension dir and no filtered shared entry (for example `pdo`, `session`, `libxml`); uncatalogued shared modules `mongodb` and `swoole` are excluded |
| Uncatalogued shared modules | `mongodb.so`, `swoole.so` exist on the system and load through `php.d`; the embedded manifest does not catalog them, so devstack does not classify or load them |
| Build rows used | `php@8.4.4-r1` (system path), `php@8.4.4-r2` (valid revision on the same path after the rollback check) |
| FPM ports observed | blog `43000`, api `43001`, blog after Xdebug `43002`, blog after ini `43003`, failed-switch candidate `43999` |
| nginx | 1.28.0 at `/usr/local/nginx/sbin/nginx` |
| Apache | httpd 2.4.69 (Fedora); `mod_ssl` **not loaded** |
| Git state | clean after the acceptance commit |

The acceptance test intentionally builds a filtered manifest from the embedded
fixture catalog and the real system layout:

- only extensions whose `.so` exists **and** whose manifest entry supports 8.4;
- requires are kept only when the dependency is also present;
- `Versions` contains one `8.4` entry with the manifest ini defaults and the
  present shared extensions;
- absent `.so` files are not invented;
- the fixture manifest is round-tripped through `catalog.ParsePHP` so the
  production validation invariants are enforced.

No production catalog entries were added. The shipped PHP catalog remains empty
until checksummed artifacts are published.

## Commands and results

| Check | Command | Result |
|---|---|---|
| Real acceptance test | `DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -v` | **PASS**; Apache HTTP PHP configtest **PASS**, Apache HTTPS vhost **SKIP** (`ssl_module` absent) |
| Repeatability | `go test ./test/php -run TestPHPSubsystemAcceptance -count=2` | **PASS**; two consecutive iterations in 1.85 s |
| Absent-root skip | `DEVSTACK_PHP_ROOT=/nonexistent go test ./test/php -run TestPHPSubsystemAcceptance -v` | **SKIP** with `bin/php is not available under /nonexistent` |
| Race detector | `go test ./... -race` | **PASS**; all packages `ok`, including `test/php` in 2.09 s |
| Focused PHP packages | `go test ./internal/backend ./internal/sites ./internal/proxy ./internal/phpfpm ./internal/phpini -v` | **PASS**; five packages `ok` |
| Build | `make build` | **PASS**; dashboard + `dist/devstack` + `dist/devstack-helper` built |
| Vhost templates | `go test ./internal/vhost -run 'TestRenderNginxPHP\|TestRenderApachePHP' -v` | **PASS**; 9 tests |
| Doctor PHP checks | `go test ./internal/doctor -run 'TestDoctorPHP\|TestDoctorReportsPHPConfigFailure\|TestDoctorSkipsPHPWhenUnused' -v` | **PASS** |

### Acceptance test output summary

```text
acceptance: PHP 8.4.4 build php@8.4.4-r1 at /usr/local/php
acceptance: extension dir /usr/local/php/lib/php/extensions/no-debug-non-zts-20240924
acceptance: uncatalogued shared modules not loaded by the generated ini: mongodb,swoole
acceptance: filtered extensions imagick,opcache,pcov,pdo_pgsql,redis,xdebug,zip
acceptance: blog site id=... build=php@8.4.4-r1 fastcgi=43000
acceptance: api site id=... build=php@8.4.4-r1 fastcgi=43001
acceptance: xdebug isolation verified on blog port 43002; api port 43001 unaffected
acceptance: per-site memory_limit verified; blog=43003 api=43001
acceptance: malformed raw ini rejected at line 2: sites: compose php.ini for "blog": phpini: line 2: expected a key = value directive
acceptance: incomplete build switch rejected: sites: validate php-fpm.conf for "blog": phpfpm: invalid configuration: fork/exec .../php-incomplete/sbin/php-fpm: no such file or directory
acceptance: failed-switch candidate FastCGI port 43999 is free (bind 127.0.0.1:43999 succeeded)
acceptance: candidate port 43999 was released after the failed switch
nginx -t: ... syntax is ok
nginx -t: ... test is successful
Apache HTTP PHP configtest: Syntax OK
Apache HTTPS PHP vhost configtest: SKIP (ssl_module is not loaded; HTTP PHP vhost passed)
acceptance: built-in proxy served blog.test through FastCGI with 512M and xdebug
acceptance: updated blog document root=public2 front_controller=true
acceptance: removal of php@8.4.4-r1 blocked by [api blog]
acceptance: migrated both sites to php@8.4.4-r2; php@8.4.4-r1 is no longer referenced
acceptance: deleted api FastCGI port 43001 is free (bind 127.0.0.1:43001 succeeded)
acceptance: deleted api site; config dir removed and port 43001 released
acceptance: stopped blog FastCGI port 43003 is free (bind 127.0.0.1:43003 succeeded)
acceptance: stop/start kept build php@8.4.4-r2 and healthy master on port 43003
acceptance: doctor php-builds: PHP builds php@8.4.4-r1 (8.4.4), php@8.4.4-r2 (8.4.4) provide php, php-fpm, and extension modules
acceptance: doctor php-config: PHP configuration validated for 1 site(s)
acceptance: doctor php-ports: 1 running PHP site(s) have unique FastCGI ports in 43000-43999
acceptance: doctor php-master: all 1 running PHP masters accept FastCGI connections on 127.0.0.1
cleanup: blog FastCGI port 43000 is free after StopAll (127.0.0.1 bind succeeded)
cleanup: api FastCGI port 43001 is free after StopAll (127.0.0.1 bind succeeded)
cleanup: blog FastCGI port 43002 is free after StopAll (127.0.0.1 bind succeeded)
cleanup: blog FastCGI port 43003 is free after StopAll (127.0.0.1 bind succeeded)
```

### Port-free and process evidence

The acceptance test now proves listener shutdown at the kernel level, not only in
the in-memory port allocator. After the failed switch, after site deletion, after
site stop, and after `local.StopAll`, it binds `127.0.0.1:<port>` with a bounded
retry and fails if the bind never succeeds. The exact evidence lines are:

```text
acceptance: failed-switch candidate FastCGI port 43999 is free (bind 127.0.0.1:43999 succeeded)
acceptance: deleted api FastCGI port 43001 is free (bind 127.0.0.1:43001 succeeded)
acceptance: stopped blog FastCGI port 43003 is free (bind 127.0.0.1:43003 succeeded)
cleanup: blog FastCGI port 43000 is free after StopAll (127.0.0.1 bind succeeded)
cleanup: api FastCGI port 43001 is free after StopAll (127.0.0.1 bind succeeded)
cleanup: blog FastCGI port 43002 is free after StopAll (127.0.0.1 bind succeeded)
cleanup: blog FastCGI port 43003 is free after StopAll (127.0.0.1 bind succeeded)
```

Post-run process and listener checks:

```text
$ pgrep -a php-fpm
1397 php-fpm: master process (/usr/local/php/etc/php-fpm.conf)
... (pre-existing system FPM master and pools; no acceptance-spawned master)
$ ss -ltn '( sport >= :43000 and sport <= :43999 )'
State  Recv-Q  Send-Q  Local Address:Port  Peer Address:Port
(no rows)
```

Cleanup now reports delete/`StopAll`/log-close/store-close failures with
`t.Errorf`, then checks every tracked site port after `StopAll`. A leaked FPM
master or listener fails the acceptance test.

### Migration scope

The successful migration used `php@8.4.4-r2` with the **same install path**
(`/usr/local/php`). It proves revision pinning, row migration, and removal
bookkeeping, but it does **not** prove a cross-build or cross-line migration.
That evidence is part of the blocked real 8.2/8.5 artifact gate below.

## Plan 2 acceptance checklist

Status values: **PASS** = verified in this run; **SKIP** = intentionally not run
and reported as skipped; **BLOCKED** = requires the published Task 11 artifacts
or a different environment.

| # | Check | Result | Command / evidence | Notes |
|---|---|---|---|---|
| 1 | Real PHP 8.4 per-site version | **PASS** | Acceptance test step 1; direct `fcgi.Dial` response `PHP_VERSION=8.4.4` on port 43000 | System build pinned as `php@8.4.4-r1`; FastCGI port inside 43000-43999 |
| 2 | Per-site `php.ini` | **PASS** | Steps 3–4; blog reports `memory_limit=512M`, api reports `256M` | The change survived a staged master swap and a rejected later edit |
| 3 | Xdebug isolation | **PASS** | Step 2; blog reports `xdebug=on`, api reports `xdebug=off` | `xdebug.so` present in the filtered manifest; API master unaffected |
| 4 | Invalid ini rejected with line information while the old master serves | **PASS** | Step 4; `*phpini.ValidationError` line 2; old row/hash/port unchanged; old master still answers `512M`; route and vhost still point at the old port | No route or vhost regression after the failed apply |
| 5 | Add/remove/update/migrate | **PASS** | Steps 1, 8; second site created, document root/front controller updated, both sites migrated, second site deleted with config dir and port released | Build *rows* are fixture rows and migration uses r2 on the **same install path**; this proves revision migration, not cross-build/cross-line migration (blocked below) |
| 6 | Removal blocked while referenced | **PASS** | Step 8; `CheckRuntimeRemoval(php@8.4.4-r1)` returns `*sites.RuntimeInUseError` naming `[api blog]`; returns nil after migration | Blocking is by build ID, not only by line |
| 7 | Switch with rollback | **PASS** | Step 5; switch to a candidate without `sbin/php-fpm` fails in FPM validation; row stays `php@8.4.4-r1`; old master answers; `net.Listen` on 43999 succeeds, and the allocator can reserve it again | Validate-then-swap path exercised with the real backend; kernel bind proves the candidate listener is gone |
| 8 | FastCGI request direct to FPM | **PASS** | Step 1 and later steps; `fcgi.Dial` GET `/index.php` returns version, ini, Xdebug state | Real supervised `php-fpm` master, not a fake |
| 9 | FastCGI request through the built-in proxy | **PASS** | Step 7; `router.Handler()` with `Host: blog.test` via `httptest` returns 8.4.4, `512M`, and Xdebug loaded | Route is published by the real site service |
| 10 | nginx PHP vhost template | **PASS** | Steps 6; `fastcgi_pass`, `SCRIPT_FILENAME`, `PATH_INFO`, directory index, and source-backup denial strings present | Real `nginx -t` against a temp prefix, high ports 18080/18443, and the **TLS branch with the real site certificate plus the dashboard certificate**: syntax ok, test successful |
| 11 | Apache PHP vhost template | **PASS** (HTTP real configtest) / **SKIP** (HTTPS vhost) | `proxy:fcgi`, `DirectoryIndex`, and source-backup denial strings present; real `httpd -t -f <main> -c Include <php-http-vhost>` reports `Syntax OK` on high port 18080 | Apache 2.4.69 has `proxy_fcgi_module` but no `ssl_module`, so only the HTTPS vhost is skipped. The HTTP check needs no TLS and uses `-c Include` after the main config; the HTTPS branch is exercised only when `ssl_module` is loaded |
| 12 | Doctor PHP checks | **PASS** | Acceptance test final step; `php-builds`, `php-config`, `php-ports`, `php-master` all `ok` | Doctor used the real store, real PHP validator, and live FPM port |
| 13 | Docs | **PASS** | `README.md`, `docs/php.md`, and this report | Documentation states the fixture/system-PHP basis and the artifact gate |
| 14 | Real PHP 8.2 and 8.5 side by side | **BLOCKED** | Requires checksummed archives and catalog entries from the [Task 11 CI pipeline](../../../.github/workflows/php.yml) | No 8.2/8.5 artifacts are published; the local manifest is 8.4-only fixture data |
| 15 | Production PHP catalog publication | **BLOCKED** | `internal/catalog/catalog.json` still has no PHP entries | Publication happens only after real artifacts are uploaded and catalog entries are merged |
| 16 | In-flight requests survive a valid swap | **SKIP** | Not measured | The acceptance test applies changes while idle; it verifies validate-then-swap and rollback, not concurrent request continuity |

## Failures found and fixed during acceptance

1. The first filtered manifest kept every `Versions` entry on each extension while
   only defining `8.4` defaults, so `catalog.ParsePHP` correctly rejected it
   (`extension "opcache" lists version "8.2" without defaults`). The test now
   narrows each kept extension to `8.4`, which also proves the parser's
   invariants are active.
2. The first real `nginx -t` run failed because nginx opened its default
   `logs/error.log` and `logs/access.log` under the temp prefix before applying
   the test config. The test now creates `<prefix>/logs`; nginx reports
   `syntax is ok` and `test is successful`. The `listen ... http2` deprecation
   warnings are non-fatal and come from the production template.
3. The Apache vhost assertion initially expected an unquoted `DocumentRoot`.
   The template correctly quotes the path; the assertion now matches the
   rendered form.
4. Acceptance review found that the candidate-port "released" check only used
   the in-memory allocator, which never binds. The test now binds
   `127.0.0.1:43999` (bounded retry) after rollback and logs the result, while
   keeping the allocator reservation as a second check.
5. Delete, stop, and `StopAll` cleanup previously did not prove the FPM listener
   was gone. The test now binds the old API port after delete, the old blog port
   after stop, and every tracked site port after `StopAll`, with bounded retry.
6. Cleanup failures for delete/`StopAll`/`logs.Close`/`store.Close` were only
   logged. They now call `t.Errorf`, and cleanup checks every tracked port after
   `StopAll`; a leaked master or listener fails acceptance.
7. The filtered manifest classified `mongodb` and `swoole` as built-ins even
   though their `.so` files exist and the system `php.d` loads them. The filter
   now excludes any module with a matching `.so` and logs them as uncatalogued
   shared modules that the generated devstack ini does not load.

No product code changes were required by the acceptance run.

## Blocked release gate

The real 8.2/8.5 side-by-side criterion in M1 §21 item 3 is a release gate, not
an implementation gap in the paths exercised here. It requires:

1. the Task 11 PHP workflow to build and smoke-test 8.2 and 8.5 archives;
2. checksums and SBOMs to be published at a real HTTPS asset host;
3. `cmd/phpcatalog` output to be merged into `internal/catalog/catalog.json`; and
4. a rerun of this acceptance shape with real 8.2 and 8.5 installs, each
   reporting its own version and extensions.

Until then:

- **Production PHP catalog publication is pending real CI artifacts.**
- Tests used fixture catalog rows plus system PHP 8.4.4.
- `devstack runtime list`/`runtime install` in a release build still cannot
  install PHP; the limitation is documented in `README.md` and `docs/php.md`.
- The Apache **HTTPS** config test remains a manual/skip item on this
  workstation because `mod_ssl` is absent. It is not counted as a pass. The
  Apache HTTP PHP vhost is tested for real and passes.

## Known limitations and follow-ups

1. **Artifact gate:** publish and merge the Task 11 8.2/8.5 artifacts, then run
   the real side-by-side gate. This is Plan 4/release work.
2. **In-flight continuity:** add a concurrent request during a successful apply
   if the release gate needs explicit evidence that no in-flight request is
   terminated.
3. **Apache HTTPS real configtest:** rerun on a host with `mod_ssl` and
   `proxy_fcgi` loaded. The HTTP PHP vhost, template strings, and golden tests
   pass here; the real HTTPS config test is skipped.
4. **Apache SCRIPT_FILENAME/PATH_INFO:** mod_proxy_fcgi supplies these CGI
   parameters at runtime; the Apache template itself does not contain those
   literal strings. nginx asserts them explicitly.
5. **Uncatalogued shared modules:** `mongodb.so` and `swoole.so` are present in
   the system extension directory and are loaded by the system `php.d`, but the
   embedded manifest does not catalog them. The filtered manifest excludes them
   from built-ins, the generated devstack ini does not load them, and they are
   not site extension toggles.
