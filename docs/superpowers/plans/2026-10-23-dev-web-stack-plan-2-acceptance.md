# Plan 2 Acceptance Report

- **Date:** 2026-10-24
- **Base revision:** `65ad1bf` (`docs: record plan 2 status through task 11`), plus
  the acceptance test and documentation changes in this commit.
- **Plan:** [Plan 2 — PHP](2026-10-23-dev-web-stack-plan-2-php.md)
- **Scope:** the Plan 2 subset of M1 design §21 plus the rollback, template, and
  Doctor checks named in Task 12.
- **Overall result:** **automated acceptance PASS for the Plan 2 paths on
  system PHP 8.4.4; one Apache real-config subtest SKIP.** The managed-artifact
  gate (real PHP 8.2 and 8.5 side by side, production catalog publication) is
  **BLOCKED** on the Task 11 CI artifacts. This report does not claim production
  PHP installation works.

## Environment

| Item | Value |
|---|---|
| OS | Fedora Linux 44, x86_64 |
| User | `uid=1000` (unprivileged; PHP acceptance skips as root) |
| PHP | **8.4.4** at `/usr/local/php` (`bin/php`, `sbin/php-fpm`) |
| PHP extension dir | `/usr/local/php/lib/php/extensions/no-debug-non-zts-20240924` |
| Filtered shared extensions | `imagick`, `opcache`, `pcov`, `pdo_pgsql`, `redis`, `xdebug`, `zip` |
| Filtered built-in modules | manifest built-ins plus lowercase `php -m` modules not represented by those shared extensions (including `pdo`, `session`, `libxml`) |
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
| Real acceptance test | `DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -v` | **PASS** in 0.85 s; one Apache subtest **SKIP** |
| Absent-root skip | `DEVSTACK_PHP_ROOT=/nonexistent go test ./test/php -run TestPHPSubsystemAcceptance -v` | **SKIP** with `bin/php is not available under /nonexistent` |
| Race detector | `go test ./... -race` | **PASS**; all packages `ok`, including `test/php` in 2.07 s |
| Focused PHP packages | `go test ./internal/backend ./internal/sites ./internal/proxy ./internal/phpfpm ./internal/phpini -v` | **PASS**; five packages `ok` |
| Build | `make build` | **PASS**; dashboard + `dist/devstack` + `dist/devstack-helper` built |
| Vhost templates | `go test ./internal/vhost -run 'TestRenderNginxPHP\|TestRenderApachePHP' -v` | **PASS**; 9 tests |
| Doctor PHP checks | `go test ./internal/doctor -run 'TestDoctorPHP\|TestDoctorReportsPHPConfigFailure\|TestDoctorSkipsPHPWhenUnused' -v` | **PASS** |

### Acceptance test output summary

```text
acceptance: PHP 8.4.4 build php@8.4.4-r1 at /usr/local/php
acceptance: extension dir /usr/local/php/lib/php/extensions/no-debug-non-zts-20240924
acceptance: filtered extensions imagick,opcache,pcov,pdo_pgsql,redis,xdebug,zip
acceptance: blog site id=... build=php@8.4.4-r1 fastcgi=43000
acceptance: api site id=... build=php@8.4.4-r1 fastcgi=43001
acceptance: xdebug isolation verified on blog port 43002; api port 43001 unaffected
acceptance: per-site memory_limit verified; blog=43003 api=43001
acceptance: malformed raw ini rejected at line 2: sites: compose php.ini for "blog": phpini: line 2: expected a key = value directive
acceptance: incomplete build switch rejected: sites: validate php-fpm.conf for "blog": phpfpm: invalid configuration: fork/exec .../php-incomplete/sbin/php-fpm: no such file or directory
acceptance: candidate port 43999 was released after the failed switch
nginx -t: ... syntax is ok
nginx -t: ... test is successful
Apache/2.4.69 (Fedora Linux) is installed but mod_ssl is not loaded; HTTPS PHP vhost configtest skipped
acceptance: built-in proxy served blog.test through FastCGI with 512M and xdebug
acceptance: updated blog document root=public2 front_controller=true
acceptance: removal of php@8.4.4-r1 blocked by [api blog]
acceptance: migrated both sites to php@8.4.4-r2; php@8.4.4-r1 is no longer referenced
acceptance: deleted api site; config dir removed and port 43001 released
acceptance: stop/start kept build php@8.4.4-r2 and healthy master on port 43003
acceptance: doctor php-builds: PHP builds php@8.4.4-r1 (8.4.4), php@8.4.4-r2 (8.4.4) provide php, php-fpm, and extension modules
acceptance: doctor php-config: PHP configuration validated for 1 site(s)
acceptance: doctor php-ports: 1 running PHP site(s) have unique FastCGI ports in 43000-43999
acceptance: doctor php-master: all 1 running PHP masters accept FastCGI connections on 127.0.0.1
```

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
| 5 | Add/remove/update/migrate | **PASS** | Steps 1, 8; second site created, document root/front controller updated, both sites migrated, second site deleted with config dir and port released | Build *rows* are fixture rows; see the artifact gate below |
| 6 | Removal blocked while referenced | **PASS** | Step 8; `CheckRuntimeRemoval(php@8.4.4-r1)` returns `*sites.RuntimeInUseError` naming `[api blog]`; returns nil after migration | Blocking is by build ID, not only by line |
| 7 | Switch with rollback | **PASS** | Step 5; switch to a candidate without `sbin/php-fpm` fails in FPM validation; row stays `php@8.4.4-r1`; old master answers; candidate port 43999 is released and reusable | Validate-then-swap path exercised with the real backend |
| 8 | FastCGI request direct to FPM | **PASS** | Step 1 and later steps; `fcgi.Dial` GET `/index.php` returns version, ini, Xdebug state | Real supervised `php-fpm` master, not a fake |
| 9 | FastCGI request through the built-in proxy | **PASS** | Step 7; `router.Handler()` with `Host: blog.test` via `httptest` returns 8.4.4, `512M`, and Xdebug loaded | Route is published by the real site service |
| 10 | nginx PHP vhost template | **PASS** | Steps 6; `fastcgi_pass`, `SCRIPT_FILENAME`, `PATH_INFO`, directory index, and source-backup denial strings present | Real `nginx -t` against a temp prefix, certs, and high ports 18080/18443: syntax ok, test successful |
| 11 | Apache PHP vhost template | **PASS** (template) / **SKIP** (real configtest) | `proxy:fcgi`, `DirectoryIndex`, and source-backup denial strings present | Real `httpd -t` skipped: Apache 2.4.69 is installed but `mod_ssl` is not loaded |
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
- The Apache HTTPS config test remains a manual/skip item on this workstation
  because `mod_ssl` is absent. It is not counted as a pass.

## Known limitations and follow-ups

1. **Artifact gate:** publish and merge the Task 11 8.2/8.5 artifacts, then run
   the real side-by-side gate. This is Plan 4/release work.
2. **In-flight continuity:** add a concurrent request during a successful apply
   if the release gate needs explicit evidence that no in-flight request is
   terminated.
3. **Apache real configtest:** rerun on a host with `mod_ssl` and `proxy_fcgi`
   loaded. The template strings and golden tests pass here; the real HTTPS
   config test is skipped.
4. **Apache SCRIPT_FILENAME/PATH_INFO:** mod_proxy_fcgi supplies these CGI
   parameters at runtime; the Apache template itself does not contain those
   literal strings. nginx asserts them explicitly.
5. **Fixture manifest scope:** `mongodb` and `swoole` appear as built-ins only
   because the embedded manifest does not catalog them while the system `php -m`
   reports them. They are not loaded by the generated devstack ini.
