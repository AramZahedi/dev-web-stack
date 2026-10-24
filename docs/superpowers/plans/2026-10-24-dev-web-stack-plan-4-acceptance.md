# Plan 4 Linux M1 Acceptance Report

- **Date:** 2026-10-24
- **Branch:** `plan-4-release`
- **Base revision:** `a0e0e4f` (`Merge branch 'plan-2-php' into plan-4-release`)
- **Acceptance/docs commit:** `dfb0fc1` — `docs: record Linux M1 release acceptance`
- **Clean-checkout fix:** `6cc78ce` — `fix: make M1 acceptance reproducible from a clean checkout`
- **Final-review fix:** `8bd6dc5` — `fix: verify catalog artifacts and isolate release tests`
- **Plan:** [Plan 4 — Release Packaging, Upgrade, and M1 Acceptance (Linux)](2026-10-24-dev-web-stack-plan-4-release.md)
- **Spec:** [M1 design §21](../specs/2026-10-22-dev-web-stack-m1-design.md#21-acceptance-criteria)
- **Handoff:** [2026-10-24 M1 handoff](../status/2026-10-24-m1-handoff.md)
- **Runner:** `test/acceptance/m1.sh` (`make acceptance`)

## Overall result

**The implemented Linux paths pass automated acceptance (15 PASS, 0 FAIL, 3 SKIP,
3 BLOCKED). Full M1 acceptance is not achieved.** The Windows/WSL item is
deferred by user request, real PHP 8.2/8.5 artifacts and the production PHP
catalog are blocked, Apache HTTPS is skipped without `mod_ssl`, browser
trust/root install/uninstall are manual, and in-flight request continuity is not
measured. This report does not claim a merged, tagged, published, or app/release
CI-green release. No release tag exists and no GitHub release was published from
this branch. The separate PHP build/smoke workflow was observed green at
`fec5917` (run `40088938373`).

| Result | Count | Meaning |
|---|---:|---|
| PASS | 15 | Executed in this run and verified |
| FAIL | 0 | No phase failed |
| SKIP | 3 | Intentionally not run, with the reason recorded |
| BLOCKED | 3 | Requires a deferred plan, published artifacts, or a different environment |

A SKIP or BLOCKED phase is never counted as a pass. The runner exits 0 only
because no phase FAILed; it prints the blocked/skip gates in the same table.

## Environment

| Item | Value |
|---|---|
| OS | Fedora Linux 44 (Workstation Edition), kernel `7.2.9-200.fc44.x86_64`, x86_64 |
| User | `uid=1000` (`aram`), unprivileged; the runner refuses to run as root |
| Go | `go1.27.3 linux/amd64`; module declares Go `1.26.0` |
| Node / npm | `v22.23.1` / `10.9.8` |
| PHP | `8.4.4` CLI and `php-fpm` at `/usr/local/php` |
| PHP extension dir | `/usr/local/php/lib/php/extensions/no-debug-non-zts-20240924` |
| nginx | `1.28.0` at `/usr/local/nginx/sbin/nginx` |
| Apache | `httpd 2.4.69` (Fedora); `ssl_module` absent, `proxy_fcgi_module` loaded |
| Git | `2.56.0`; branch `plan-4-release`; **no tags** |
| Release state | No tag, no published archive, no GitHub release from this branch |
| CI state | PHP build/smoke workflow run `40088938373` at `fec5917` PASS (8.2/8.3/8.4/8.5 build + smoke); catalog job skipped (no `PHP_DOWNLOAD_BASE_URL`). App CI and the tag-driven release workflow were not observed; the workflow contract test checks the checked-in YAML. |

The pre-existing system PHP-FPM master (`pid 1397`, listening outside the
43000-43999 range) was present before the run and is unrelated to acceptance.
No acceptance-spawned FPM masters or listeners remained afterwards.

## Runner design

`test/acceptance/m1.sh [--quick] [--skip-playwright]`:

- refuses to run as root;
- checks for required tools (`go`, `npm`, `node`, `make`, `bash`, `tar`, `curl`,
  `sha256sum`, `mktemp`, `jq`);
- runs every phase into a temporary log, records status, duration, and the log
  basename, and prints the last 80 log lines when a phase fails;
- prints the temporary log directory at the end of the table; logs are kept
  under `${TMPDIR:-/tmp}/devstack-acceptance.*` for inspection;
- returns `0` only when no phase is `FAIL`; `SKIP` and `BLOCKED` are printed and
  counted but do not fail the run;
- never calls `devstack init`, never performs a non-sandboxed install, never
  writes `/etc/hosts` or a trust store, and never binds 80/443. The installer
  test does execute `install.sh` non-dry-run, but only with fake
  `sudo`/`systemctl` commands and temporary install roots;
- requires `jq` for the catalog gate. A missing parser, invalid JSON, a
  malformed schema, an unexpected entry kind, or a PHP-bearing catalog all
  FAIL; only a valid, Node-only catalog is BLOCKED on the unpublished PHP
  artifacts.

`--quick` skips only the race phase. `--skip-playwright` skips only Playwright.
Neither option converts a skipped gate into a pass.

The phase order is deliberate for a clean checkout:

1. `make build` is the only `npm ci` path, so it precedes the UI phases and
   builds the dashboard on a clean tree.
2. The release archive test uses a temporary `--build-dir`, so it never
   rewrites or creates `dist/devstack`; the final binaries from `make build`
   remain in place for the installer, E2E, and real-PHP phases.
3. UI check/unit/build/Playwright, the installer/workflow tests, installer
   dry-run, E2E, and real-PHP acceptance run against those final binaries.

This order makes `make acceptance` work when `ui/node_modules` and `ui/dist`
are absent, and `make release-test` cannot leave a test-version binary in
`dist/`. A clean-checkout run was performed after the fix; see
[Clean-checkout verification](#clean-checkout-verification).

## Full acceptance result

Command:

```sh
make acceptance
```

A clean `git worktree` (no `ui/node_modules`, only `ui/dist/.gitkeep`, no
`dist/`) produced this result in about 84 seconds:

```text
STATUS     PHASE                               SECS NOTE
---------- ---------------------------------- ---- ----------------------------------------------
PASS       go vet                                1
PASS       go test                               5
PASS       go test race                         13
PASS       release archive test                  7
PASS       make build                            6
PASS       ui check                              4
PASS       ui unit tests                        11
PASS       ui build                              5
PASS       ui Playwright                        31
PASS       installer test                        1
PASS       release workflow test                 0
PASS       installer dry-run                     0
PASS       linux E2E                             2 e2e: SKIP node vhost check (no installed Node runtime)
PASS       real PHP acceptance                   1
PASS       FPM listener check                    0
BLOCKED    PHP catalog gate                      0 catalog is valid and Node-only (0 PHP entries)
BLOCKED    PHP 8.2/8.5 side-by-side              0 needs published checksummed artifacts
BLOCKED    Windows/WSL                           0 deferred by user request (Plan 3)
SKIP       Apache HTTPS                          0 ssl_module is not loaded
SKIP       manual checks                         0 browser trust/root install/uninstall/live UI
SKIP       in-flight continuity                  0 not measured

TOTAL: 15 PASS, 0 FAIL, 3 SKIP, 3 BLOCKED
RESULT: PASS (no FAIL; SKIP/BLOCKED are not passes)
```

The release archive test runs with a temporary `--build-dir` for both its
explicit-output and default-output cases, so it never writes `dist/devstack` or
`dist/devstack-helper`. `make build` still runs before the UI, installer, E2E,
and PHP phases because it is the only `npm ci` path; the UI phases therefore see
its result, and the run cannot finish with a test-version `dist/devstack`.

The E2E phase passed both the builtin flow and the real nginx high-port flow;
its only internal skip was the Node vhost check because no Node runtime was
installed in the temporary home. The UI Playwright suite passed 19/19.

### Focused evidence

| Check | Command | Result |
|---|---|---|
| Vet | `go vet ./...` | PASS, no output |
| Unit/integration | `go test ./... -count=1` | PASS, all 38 packages; `test/php` real-FPM acceptance included |
| Race | `go test ./... -race -count=1` | PASS, all packages, including `test/php` |
| UI type check | `(cd ui && npm run check)` | PASS, 0 errors |
| UI unit | `(cd ui && npm test -- --run)` | PASS, 9 files, **51 tests** |
| UI build | `(cd ui && npm run build)` | PASS |
| Playwright | `(cd ui && npm run test:e2e)` | PASS, **19/19** with bundled Chromium |
| Build | `make build` | PASS; dashboard embedded, `dist/devstack` and `dist/devstack-helper` built |
| Release archive | `bash test/release/release_test.sh` | PASS; exact members, checksum verification, corrupt-archive rejection, version/protocol check, reproducibility, and no `dist/` writes |
| Installer | `bash test/release/installer_test.sh` | PASS; 14 checks, including upgrade dry-run and failed-stop abort |
| Release workflow contract | `bash test/release/workflow_test.sh` | PASS; static workflow/config assertions |
| Installer dry-run | `bash install.sh --dry-run` | PASS; no files or services changed |
| Linux E2E | `E2E_SKIP_BUILD=1 bash test/e2e/linux.sh` | PASS; builtin proxy/forwarders, helper restart, daemon stop/restart, real nginx switch and back |
| Real PHP | `DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -count=1 -v` | PASS; system PHP 8.4.4, ports 43000-43003, candidate 43999 released |
| FPM listeners | `ss -H -ltn '( sport >= :43000 and sport <= :43999 )'` | PASS; no rows after the run |
| Fast-path options | `bash test/acceptance/m1.sh --quick --skip-playwright` | PASS; 13 PASS, 0 FAIL, 5 SKIP, 3 BLOCKED |

The `--quick --skip-playwright` run proves the skip flags are explicit and do
not alter the pass/fail result. It still ran unit tests, UI tests/build, release
tests, E2E, real PHP acceptance, and the listener check.

## Clean-checkout verification

The fix wave was verified from a clean `git worktree` at the final fix commit,
not from the long-lived checkout (which has build outputs and node modules):

```sh
clean=/tmp/opencode/devstack-clean-acceptance
rm -rf "$clean"
git worktree add --detach "$clean" HEAD
test ! -d "$clean/ui/node_modules"                  # absent before the run
test -z "$(find "$clean/ui/dist" -mindepth 1 ! -name .gitkeep -print -quit)"
test ! -d "$clean/dist"                             # absent before the run
(cd "$clean" && bash test/acceptance/m1.sh)
git worktree remove --force "$clean"
```

The run was executed in a detached worktree at `8bd6dc5`; the worktree was
removed afterwards and the main checkout was not used for this evidence.

Result: **15 PASS, 0 FAIL, 3 SKIP, 3 BLOCKED**, exit 0. The `release archive
test` phase used a temporary build directory and left `dist/` untouched;
`make build` ran `npm ci` and built the dashboard before the UI phases. After
the run:

```text
$ ./dist/devstack version --json
{"version":"dev","string":"dev","protocol":"1","os":"linux","arch":"amd64","go_version":"go1.27.3"}
```

The final binary was not `0.0.0-test` or `0.0.0-defaultpath`. `ui/node_modules`
and `ui/dist` were created by the build and the UI/Playwright phases passed.
This is the evidence that `make acceptance` no longer depends on a pre-existing
`ui/node_modules` or `ui/dist`.

The PHP catalog gate's negative branches were exercised with a temporary
catalog before the run:

| Catalog state | Gate result |
|---|---|
| Valid Node-only (`{"entries":[{"kind":"node"}]}`) | **BLOCKED** |
| Missing `internal/catalog/catalog.json` | **FAIL** |
| Contains a PHP entry | **FAIL** |
| Invalid JSON | **FAIL** |

The gate no longer silently treats a missing or PHP-bearing catalog as
BLOCKED.

## M1 §21 item-by-item mapping

Status values are deliberately mixed where one criterion contains both an
automated subset and a manual/blocked subset; neither half is presented as the
other.

| # | M1 §21 criterion | Status | Evidence and honest limits |
|---|---|---|---|
| 1 | Fresh Linux install → `devstack init` → Doctor green for every applicable trust target; Windows also reports WSL | **MANUAL/BLOCKED** (Linux root/trust); **DEFERRED** (WSL) | Installer dry-run and the fake-sudo installer test PASS, but no real root install, `devstack init`, trust-store write, or live `doctor` run was performed. `devstack doctor --json` exists as the support surface; a green doctor report is not claimed. Windows/WSL is Plan 3. |
| 2 | Static site at `https://project.test` with no browser warning; HTTP/HTTPS follows TLS/force-HTTPS settings | **PASS (automated)** / **MANUAL** (browser warning) | E2E served the static site through the builtin proxy and the privileged helper forwarder over HTTPS with the daemon CA. The nginx E2E verified a force-HTTPS 308 redirect to the configured high HTTPS port. No real browser warning check was run. |
| 3 | PHP 8.2 and 8.5 side by side; each reports its version; per-site ini; Xdebug only on one site | **BLOCKED** (8.2/8.5) / **PASS** (8.4 subset) | System PHP 8.4.4 passed per-site version, `memory_limit`, Xdebug isolation, and FastCGI checks. The real 8.2/8.5 side-by-side gate is blocked on published checksummed artifacts and merged catalog entries; no 8.2/8.5 artifact was invented. |
| 4 | Invalid php.ini rejected with line information while the old master keeps serving; valid changes swap without terminating in-flight requests | **PASS** (invalid ini) / **NOT MEASURED** (in-flight) | Malformed raw ini was rejected at line 2; the old master kept serving and the route/vhost stayed on the old port. The tests prove validate-then-swap and rollback, not concurrent request continuity. In-flight continuity is **NOT MEASURED**. |
| 5 | Add/remove PHP versions; removal blocked while referenced; switching is validate-then-swap with rollback | **PASS** (fixture 8.4 subset) / **BLOCKED** (real builds) | The system-PHP acceptance created two sites, edited a document root/front controller, blocked removal of a referenced build, migrated both sites to `php@8.4.4-r2` on the same path, and exercised a failed switch with rollback and port release. This is revision migration, not cross-build/cross-line migration; that remains blocked on real 8.2/8.5 artifacts. |
| 6 | Node site proxies HTTP and HMR WebSockets; crashed dev server shows logs/backoff and can be restarted | **PASS (automated)** / **MANUAL** (browser HMR) | `TestRouterWebSocketEcho` and the supervisor crash/backoff/restart tests PASS. The E2E script contains a Node vhost check, but this environment had no installed Node runtime, so that E2E step was explicitly skipped; no browser HMR session was run. |
| 7 | Start/stop/restart from CLI and UI; logs stream; daemon restart reconciles healthy processes, stops orphans, preserves desired state | **PASS (automated)** / **MANUAL** (live UI) | E2E stopped and restarted the daemon, verified the site and forwarder recovered, and verified no `serve-static` orphan remained. Playwright covers the UI in mock mode (19/19). A live browser session against a real daemon was not run. |
| 8 | Fresh Windows install provisions the WSL distro and agent; static/PHP/Node and trust checks pass; `C:` and distro paths both work | **DEFERRED** | Plan 3 (Windows/WSL) is deferred by user request. There is no agent, named-pipe helper, `install.ps1`, Windows trust-store work, or WSL lifecycle in this branch. |
| 9 | `devstack trust remove` and `devstack uninstall` revert hosts, CA, listeners, units, and processes; project files untouched; Doctor diagnoses broken preconditions | **MANUAL/BLOCKED** | The unprivileged runner does not write trust stores, `/etc/hosts`, units, or privileged listeners. Unit-level trust/uninstall tests exist, but the real root install/uninstall and each intentionally broken precondition were not run. |
| 10 | CI green, race clean, helper RPC and archive-extraction security tests pass, no privileged process writes user config | **PASS (race + named security tests + PHP CI)** / **NOT VERIFIED (app/release CI)** / **NOT MEASURED (privileged config writes)** | `go test ./... -race` PASS. `TestHelperRejectsWrongUID` PASS; `TestExtractionRejectsTraversalAndSymlink` (parent traversal, absolute path, absolute/escaping/ancestor/chain symlinks, write-through chain, wrapper-relative escape, hardlink, device), `TestExtractionRejectsSymlinkChainWithoutWritingOutside`, and `TestInstallVerifiesChecksumAndExtractsSafely` PASS; installer archive checks PASS. PHP build/smoke CI run `40088938373` at `fec5917` PASS for 8.2–8.5; app CI and the release workflow were not observed. No test proves the “no privileged process writes user config” subcriterion; it is not claimed. |
| 11 | Docs cover install, first site, PHP versions/ini/extensions, TLS repair, WSL paths/performance, upgrades, uninstall | **PASS (Linux)** / **DEFERRED** (WSL) | README and `docs/install.md`, `upgrade.md`, `release.md`, `php.md`, and `vhosts.md` were updated for release install, upgrade backup, Linux-only/unsigned scope, the PHP catalog gate, `doctor --json`, and no diagnostics bundle. WSL documentation is intentionally absent until Plan 3. |
| 12 | External serving works end to end for nginx/Apache; broken vhost leaves the previous mode serving; uninstall removes our vhosts/bridge/TLS without touching unrelated vhosts | **PASS (nginx automated)** / **SKIP** (Apache HTTPS) / **MANUAL** (Apache serving, uninstall) | Real nginx ran from a temporary prefix on high ports; `builtin → nginx → builtin` served the static site, applied the force-HTTPS redirect, and restored the forwarder. The Node vhost step was skipped because no Node runtime was installed. Rollback tests `TestSetModeRestoresOnFailure` and `TestSetModeExternalRestoresOldBridgeOnNewFailure` PASS. The Apache HTTP PHP vhost passed real `httpd -t`; Apache HTTPS was **SKIP** because `ssl_module` is not loaded. Real Apache serving and privileged vhost/uninstall cleanup remain manual. |

## Release artifact, checksum, and installer verification

The release contract test builds a throwaway archive and checks the exact
contract:

```text
archive: /tmp/devstack-release-test.sKMDfF/out/devstack_0.0.0-test_linux_amd64.tar.gz
sha256:  45a973aeecd07ba7cb0cc6cad92312ef1111bd096420bf97a3be5d13c7e8684c
release-test: ok: archive members are exactly: devstack devstack-helper install.sh README.md LICENSE
release-test: ok: no devstack-agent, Windows, or darwin members
release-test: ok: packaged install.sh and both binaries are executable
release-test: ok: packaged devstack reports version 0.0.0-test and protocol 1
release-test: ok: checksums.txt verifies the archive
release-test: ok: checksum verification rejects a corrupted archive
release-test: ok: leading v is normalised in the archive filename
release-test: ok: two builds of 0.0.0-test are byte-identical (45a973aeecd07ba7cb0cc6cad92312ef1111bd096420bf97a3be5d13c7e8684c)
release-test: ok: invalid versions and path traversal are rejected
release-test: ok: default output is dist/release from an unrelated working directory
release-test: ok: build outputs are isolated; dist/devstack is unchanged or absent
release-test: all release archive contract checks passed
```

These digests are from the clean-worktree run at `8bd6dc5`. The commit SHA is
embedded in the binary by ldflags, so rebuilding at another commit produces a
different archive digest even with identical inputs; the reproducibility test
compares two builds at the same commit.

A separate explicit artifact was built with the local packager and installed in
dry-run mode:

```sh
bash build/release.sh --version 0.0.0-acceptance --skip-ui \
  --output /tmp/opencode/release-evidence \
  --build-dir /tmp/opencode/release-build
sha256sum -c /tmp/opencode/release-evidence/checksums.txt
tar -tzf /tmp/opencode/release-evidence/devstack_0.0.0-acceptance_linux_amd64.tar.gz
bash install.sh --dry-run \
  --archive /tmp/opencode/release-evidence/devstack_0.0.0-acceptance_linux_amd64.tar.gz \
  --checksums /tmp/opencode/release-evidence/checksums.txt
```

Observed:

```text
devstack_0.0.0-acceptance_linux_amd64.tar.gz: OK
devstack
devstack-helper
install.sh
README.md
LICENSE
{"version":"0.0.0-acceptance","commit":"a0e0e4f","string":"0.0.0-acceptance (a0e0e4f)","protocol":"1","os":"linux","arch":"amd64","go_version":"go1.27.3"}
install: checksum verified for devstack_0.0.0-acceptance_linux_amd64.tar.gz
install: archive members verified; extracting to a temporary directory
install: dry run: no files or services will be changed
```

The archive SHA-256 observed for that build was
`6c4366f12be4fbf075ebafc29956313611d47b82cfd533d6f2924d7f4122f20a`.
As above, rebuilding at a different commit changes the digest because the
commit is embedded in the binary.
The installer test additionally rejects a corrupted checksum, traversal,
absolute paths, symlinks, hardlinks, a wrong packaged version, invalid option
combinations, and a failed daemon stop before binary replacement. The only
non-dry installer execution is the test's sandboxed fake-`sudo`/`systemctl`
install, which writes into temporary roots. No host install was performed,
because that would modify real units, config, and privileged paths. The HTTPS
release-download mode is exercised with a fake `curl` serving local fixtures;
no real release server was contacted.

## Upgrade and database-backup verification

- `install.sh --upgrade --dry-run` prints the stop/backup/init plan without
  writing. The installer test also proves that a failed daemon stop aborts
  before binary replacement.
- The store's pre-migration backup tests all PASS:

  ```text
  --- PASS: TestFreshDatabaseSkipsMigrationBackup
  --- PASS: TestPendingMigrationBackupBeforeApply
  --- PASS: TestMigrationBackupFailureBlocksMigration
  --- PASS: TestMigrationBackupRetentionKeepsThree
  ```

  This verifies the `VACUUM INTO` snapshot before pending migrations, skip on a
  fresh database, fail-closed behavior when the backup cannot be written, and
  three-file retention. The documented path is
  `${DEVSTACK_HOME:-~/.devstack}/backups/state.db.pre-migrate-<UTC timestamp>.bak`.
- A real upgrade of an existing installation was **not** run. It would replace
  host binaries/units and exercise the host's existing state; the acceptance
  runner uses only the dry-run and sandboxed fake-sudo paths.
- Downgrades are unsupported: migrations are forward-only. Restoring a
  pre-migration backup is a deliberate recovery operation, not an upgrade path.

## PHP artifact and version evidence

The real system-PHP acceptance passed against **PHP 8.4.4**:

```text
acceptance: PHP 8.4.4 build php@8.4.4-r1 at /usr/local/php
acceptance: blog site ... fastcgi=43000
acceptance: api site ... fastcgi=43001
acceptance: xdebug isolation verified on blog port 43002; api port 43001 unaffected
acceptance: per-site memory_limit verified; blog=43003 api=43001
acceptance: malformed raw ini rejected at line 2
acceptance: failed-switch candidate FastCGI port 43999 is free
acceptance: built-in proxy served blog.test through FastCGI with 512M and xdebug
acceptance: removal of php@8.4.4-r1 blocked by [api blog]
acceptance: migrated both sites to php@8.4.4-r2
nginx -t: ... syntax is ok ... test is successful
Apache HTTP PHP configtest: Syntax OK
Apache HTTPS PHP vhost configtest: SKIP (ssl_module is not loaded; HTTP PHP vhost passed)
acceptance: doctor php-builds/php-config/php-ports/php-master all ok
cleanup: blog FastCGI ports 43000/43002/43003 and api port 43001 are free after StopAll
```

After the run:

```text
$ ss -H -ltn '( sport >= :43000 and sport <= :43999 )'
(no rows)
$ pgrep -a php-fpm | head
1397 php-fpm: master process (/usr/local/php/etc/php-fpm.conf)
```

The only FPM master is the pre-existing system one; no acceptance-spawned
listener remains.

The production catalog still contains **zero PHP entries**:

```sh
grep -c '"kind"[[:space:]]*:[[:space:]]*"php"' internal/catalog/catalog.json
# 0
```

## Remaining gates (not passes)

1. **Windows/WSL — DEFERRED by user request.** Plan 3 is not started. No
   Windows binary, agent, named pipe, `install.ps1`, WSL lifecycle, or Windows
   trust-store work exists.
2. **Real PHP 8.2 and 8.5 — BLOCKED.** The CI build pipeline has not published
   checksummed 8.2/8.5 archives in this environment. The acceptance used system
   PHP 8.4.4 with fixture catalog rows; this does not prove production PHP
   installation or cross-line migration.
3. **Production PHP catalog — BLOCKED/EMPTY.** `internal/catalog/catalog.json`
   remains Node-only. Do not invent URLs, checksums, sizes, or entries. The
   gated `make php-catalog-merge` step opens only after all four real archives
   are published at a real HTTPS host.
4. **Apache HTTPS — SKIP.** `ssl_module` is not loaded on this workstation.
   Apache HTTP PHP configtest passed; Apache HTTPS did not run. Real Apache
   serving also remains manual.
5. **Browser trust, root install/uninstall on 80/443, live UI checks — MANUAL /
   BLOCKED.** These require changing host trust stores, privileged ports, or a
   live browser session and were not performed by the unprivileged runner.
6. **In-flight request continuity — NOT MEASURED.** The valid-swap path does not
   have a concurrent-request assertion.
7. **App/release CI — NOT VERIFIED.** The release/installer/workflow tests
   validate the checked-in contracts. The PHP build/smoke workflow run
   `40088938373` at `fec5917` was observed green for all four lines; app CI and
   the tag-driven release workflow were not observed. The manual multi-distro
   matrix (Ubuntu 22.04/24.04, Debian 12, Fedora 44, Windows/WSL) was not run.
8. **Release publication — NOT DONE.** No tag, archive, or GitHub release was
   published. The installer's `--release` path returns 404 until one exists.

## Failures found and fixed during acceptance

The first full `make acceptance` run failed two phases (`go test` and
`go test race`) in `internal/helper` and `internal/hosts`. The cause was the
runner's global `umask 077`, which changed the modes of files created by tests
that assert `0644`/`0640` preservation. The runner no longer changes the umask;
`mktemp -d` already creates its log directory as `0700`. The second full run
passed with 15 PASS, 0 FAIL. No product code or test assertion was changed to
make the run pass.

The Task 4 review then found a clean-checkout dependency-order bug: UI phases ran
before the only `npm ci` path, and the release archive test could leave a
test-version `dist/devstack`. The fix makes `make build` precede the UI phases
and gives `build/release.sh` a `--build-dir` option that the release test points
at a temporary directory, so `dist/devstack` is never touched. The catalog gate
now preflights `jq` and FAILs for a missing parser, invalid JSON, a malformed
schema, an unexpected kind, or a PHP-bearing catalog. The clean-tree run above
verifies the fixes. The runner also prints each phase's log basename and the
kept log directory.

## Final verification commands

```sh
bash -n test/acceptance/m1.sh test/release/*.sh install.sh build/release.sh
shellcheck --severity=error test/acceptance/m1.sh test/release/*.sh install.sh build/release.sh
make acceptance
git diff --check
```

Focused commands used for the evidence above:

```sh
go vet ./...
go test ./... -count=1
go test ./... -race -count=1
(cd ui && npm run check)
(cd ui && npm test -- --run)
(cd ui && npm run build)
(cd ui && npm run test:e2e)
make build
bash test/release/release_test.sh
bash test/release/installer_test.sh
bash test/release/workflow_test.sh
bash install.sh --dry-run
E2E_SKIP_BUILD=1 bash test/e2e/linux.sh
DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -count=1 -v
go test ./internal/store -run 'Migrate|Backup' -count=1 -v
ss -H -ltn '( sport >= :43000 and sport <= :43999 )'
```

## Documentation updates in this task

- `README.md`: M1 status, release/upgrade path, Linux-only/unsigned scope,
  support surface, acceptance runner, and honest remaining gates.
- `docs/install.md`: release mode caveat, unsigned archives, `doctor --json`,
  no diagnostics bundle/auto-update, and manual limitations.
- `docs/upgrade.md`: unsigned placeholder releases, no auto-update/bundle,
  existing backup/downgrade guidance retained.
- `docs/release.md`: no-published-release state, no diagnostics bundle, workflow
  validation is not a CI-green claim, links to this report and the handoff.
- `docs/php.md`: Plan 4 acceptance rerun, 8.2/8.5 BLOCKED, in-flight NOT
  MEASURED.
- `docs/vhosts.md`: Windows/WSL deferred, Apache HTTPS skip, manual Apache
  serving, and how the acceptance runner reports E2E skips.

## Final statement

This is an honest Linux acceptance result for the implemented M1 scope, not a
claim that M1 is complete. The remaining gates above are real release or
environment blockers. The branch is ready for the controller's whole-branch
review and decision; it must not be merged or published without explicit user
approval.
