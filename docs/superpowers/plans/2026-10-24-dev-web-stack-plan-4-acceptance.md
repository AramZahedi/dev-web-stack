# Plan 4 Linux M1 Acceptance Report

- **Date:** 2026-10-25
- **Branch:** `master` (release tag `v0.1.0`)
- **Base revision:** `a0e0e4f` (`Merge branch 'plan-2-php' into plan-4-release`)
- **Acceptance/docs commit:** `dfb0fc1` — `docs: record Linux M1 release acceptance`
- **Clean-checkout fix:** `6cc78ce` — `fix: make M1 acceptance reproducible from a clean checkout`
- **Final-review fix:** `8bd6dc5` — `fix: verify catalog artifacts and isolate release tests`
- **Post-M1 audit fixes:** `c8eef74`, `a19b5be` — verified audit remediation and independent-review follow-up
- **PHP runtime release:** `php-runtimes-2026-10-25` — published 8.2.35, 8.3.36, 8.4.27, 8.5.12 archives with checksums/SPDX
- **App release:** `v0.1.0` — publishable archive, checksums, and installer
- **Plan:** [Plan 4 — Release Packaging, Upgrade, and M1 Acceptance (Linux)](2026-10-24-dev-web-stack-plan-4-release.md)
- **Spec:** [M1 design §21](../specs/2026-10-22-dev-web-stack-m1-design.md#21-acceptance-criteria)
- **Handoff:** [2026-10-24 M1 handoff](../status/2026-10-24-m1-handoff.md)
- **Runner:** `test/acceptance/m1.sh` (`make acceptance`)

## Overall result

**The implemented Linux paths pass automated acceptance (19 PASS, 0 FAIL, 1 SKIP,
1 BLOCKED). Full M1 acceptance is not achieved only because Windows/WSL is
deferred and the manual browser/root checks were not run.** The Windows/WSL item
is deferred by user request. Real PHP 8.2/8.3/8.4/8.5 artifacts are now
published, the production catalog has all four entries, Apache HTTPS served a
real TLS request, and the in-flight PHP swap test completed with zero failed
requests. The release is merged to `master`, tagged `v0.1.0`, and published;
app CI and the release workflow are green.

| Result | Count | Meaning |
|---|---:|---|
| PASS | 19 | Executed in this run and verified |
| FAIL | 0 | No phase failed |
| SKIP | 1 | Manual browser/root checks were not run |
| BLOCKED | 1 | Windows/WSL is deferred by user request (Plan 3) |

A SKIP or BLOCKED phase is never counted as a pass. The runner exits 0 only
because no phase FAILed; it prints the blocked/skip gates in the same table.

The post-M1 audit fix waves changed code covered by this report and added the
real artifact, Apache HTTPS, and in-flight swap phases. The final runner result
is 19 PASS / 0 FAIL / 1 SKIP / 1 BLOCKED. The audit's external findings and
residual accepted risks are recorded in the M1 handoff §1.1 and the committed
audit disposition ledger.

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
| Apache | `httpd 2.4.69` (Fedora); `ssl_module` and `proxy_fcgi_module` loaded; `mod_ssl` installed for the live HTTPS gate |
| Git | `2.56.0`; branch `master`; release tag `v0.1.0`; PHP runtime tag `php-runtimes-2026-10-25` |
| Release state | App release `v0.1.0` published with archive, checksums, and installer; PHP runtime release published with four archives, checksums, and SPDX SBOMs |
| CI state | PHP build/smoke workflow run `40135286787` PASS for 8.2.35, 8.3.36, 8.4.27, and 8.5.12; app CI PASS at `4f33469` and `749e651` (run `40140220531`); release workflow `40139716377` PASS; one earlier master run hit a timing-only supervisor test flake, then passed after the test poll was hardened |

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
  malformed schema, or an unexpected entry kind FAILs. With four published
  PHP entries (8.2–8.5), each must have an HTTPS URL, a 64-character SHA-256,
  and a positive size. A valid Node-only catalog remains a BLOCKED condition,
  but the released catalog is no longer Node-only.

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

The final run, after the PHP runtime release and catalog merge, produced this
result:

```text
STATUS     PHASE                               SECS NOTE
---------- ---------------------------------- ---- ----------------------------------------------
PASS       go vet                                0
PASS       make build                            6
PASS       go test                              60
PASS       go test race                         69
PASS       release archive test                  7
PASS       ui check                              4
PASS       ui unit tests                        11
PASS       ui build                              5
PASS       ui Playwright                        31
PASS       installer test                        0
PASS       release workflow test                 0
PASS       installer dry-run                     0
PASS       linux E2E                             2 e2e: SKIP node vhost check (no installed Node runtime)
PASS       real PHP acceptance                  15
PASS       FPM listener check                    0
PASS       PHP catalog gate                      0 4 published PHP entries (8.2-8.5) have HTTPS URLs, checksums, and sizes
PASS       PHP 8.2/8.5 side-by-side             45 downloaded 8.2.35/8.5.12 from the release and served both side by side
BLOCKED    Windows/WSL                           0 deferred by user request (Plan 3)
PASS       Apache HTTPS                          0 Apache HTTPS PHP vhost served a real TLS request
SKIP       manual checks                         0 browser trust/root install/uninstall/live UI
PASS       in-flight continuity                  0 PHP in-flight swap: 89 requests succeeded, 0 failed

TOTAL: 19 PASS, 0 FAIL, 1 SKIP, 1 BLOCKED
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
| Real PHP | `DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run 'TestPHPSubsystemAcceptance|TestPHPInflightSwapContinuity' -count=1 -v` | PASS; system PHP 8.4.4, live Apache HTTPS, and 89/89 requests during a PHP swap |
| PHP 8.2/8.5 | `DEVSTACK_PHP_82_ROOT=... DEVSTACK_PHP_85_ROOT=... go test ./test/php -run TestPHPCrossLineAcceptance -count=1 -v` | PASS; 8.2.35 and 8.5.12 served side by side |
| Catalog | `go run ./cmd/catalogmerge ... --check` | PASS; 2 Node entries preserved, 4 PHP entries for lines 8.2–8.5 validated |
| Apache HTTPS | live `httpd` TLS vhost on a high port with a real request through PHP-FPM | PASS; `Apache HTTPS PHP service served ... over TLS` |
| FPM listeners | `ss -H -ltn '( sport >= :43000 and sport <= :43999 )'` | PASS; no rows after the run |

## Earlier clean-checkout verification (historical)

The clean-checkout fix wave was verified from a clean `git worktree` at
`8bd6dc5`, not from the long-lived checkout (which has build outputs and node
modules). This is historical evidence for the checkout-independence fix, not
the final release result:

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

Historical result at `8bd6dc5`: **15 PASS, 0 FAIL, 3 SKIP, 3 BLOCKED**, exit 0.
The final result is the 19 PASS run above. The `release archive test` phase used
a temporary build directory and left `dist/` untouched;
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
| Four valid PHP entries with HTTPS/checksum/size metadata | **PASS** |
| PHP entry without a valid checksum or size | **FAIL** |
| Invalid JSON | **FAIL** |

The gate no longer silently treats a missing catalog as BLOCKED, and the
production catalog now exercises the four-entry PASS branch.

## M1 §21 item-by-item mapping

Status values are deliberately mixed where one criterion contains both an
automated subset and a manual/blocked subset; neither half is presented as the
other.

| # | M1 §21 criterion | Status | Evidence and honest limits |
|---|---|---|---|
| 1 | Fresh Linux install → `devstack init` → Doctor green for every applicable trust target; Windows also reports WSL | **MANUAL/BLOCKED** (Linux root/trust); **DEFERRED** (WSL) | Installer dry-run and the fake-sudo installer test PASS, but no real root install, `devstack init`, trust-store write, or live `doctor` run was performed. `devstack doctor --json` exists as the support surface; a green doctor report is not claimed. Windows/WSL is Plan 3. |
| 2 | Static site at `https://project.test` with no browser warning; HTTP/HTTPS follows TLS/force-HTTPS settings | **PASS (automated)** / **MANUAL** (browser warning) | E2E served the static site through the builtin proxy and the privileged helper forwarder over HTTPS with the daemon CA. The nginx E2E verified a force-HTTPS 308 redirect to the configured high HTTPS port. No real browser warning check was run. |
| 3 | PHP 8.2 and 8.5 side by side; each reports its version; per-site ini; Xdebug only on one site | **PASS** (versions) / **PASS** (per-site ini + Xdebug on 8.4) | Real published artifacts 8.2.35 and 8.5.12 ran side by side and each reported its own `PHP_VERSION`. The system 8.4.4 acceptance separately proves per-site `memory_limit`, Xdebug isolation, and FastCGI checks; the 8.2/8.5 cross-line test does not repeat the Xdebug isolation assertion. |
| 4 | Invalid php.ini rejected with line information while the old master keeps serving; valid changes swap without terminating in-flight requests | **PASS** (invalid ini + in-flight) | Malformed raw ini was rejected at line 2; the old master kept serving and the route/vhost stayed on the old port. The in-flight test drove 89 requests through the proxy while a successful PHP generation swap was applied: **89 succeeded, 0 failed**. A 2-second drain grace lets queued connections finish after the route snapshot moves to the new generation. |
| 5 | Add/remove PHP versions; removal blocked while referenced; switching is validate-then-swap with rollback | **PASS** (real 8.2/8.5 start; fixture removal/migration) | Real 8.2.35 and 8.5.12 artifacts were registered and served side by side. The system-PHP acceptance blocked removal of a referenced build, migrated both sites to `php@8.4.4-r2` on the same path, and exercised a failed switch with rollback and port release. Removal/migration is proven on the fixture, not repeated across the real 8.2/8.5 builds. |
| 6 | Node site proxies HTTP and HMR WebSockets; crashed dev server shows logs/backoff and can be restarted | **PASS (automated)** / **MANUAL** (browser HMR) | `TestRouterWebSocketEcho` and the supervisor crash/backoff/restart tests PASS. The E2E script contains a Node vhost check, but this environment had no installed Node runtime, so that E2E step was explicitly skipped; no browser HMR session was run. |
| 7 | Start/stop/restart from CLI and UI; logs stream; daemon restart reconciles healthy processes, stops orphans, preserves desired state | **PASS (automated)** / **MANUAL** (live UI) | E2E stopped and restarted the daemon, verified the site and forwarder recovered, and verified no `serve-static` orphan remained. Playwright covers the UI in mock mode (19/19). A live browser session against a real daemon was not run. |
| 8 | Fresh Windows install provisions the WSL distro and agent; static/PHP/Node and trust checks pass; `C:` and distro paths both work | **DEFERRED** | Plan 3 (Windows/WSL) is deferred by user request. There is no agent, named-pipe helper, `install.ps1`, Windows trust-store work, or WSL lifecycle in this branch. |
| 9 | `devstack trust remove` and `devstack uninstall` revert hosts, CA, listeners, units, and processes; project files untouched; Doctor diagnoses broken preconditions | **MANUAL/BLOCKED** | The unprivileged runner does not write trust stores, `/etc/hosts`, units, or privileged listeners. Unit-level trust/uninstall tests exist, but the real root install/uninstall and each intentionally broken precondition were not run. |
| 10 | CI green, race clean, helper RPC and archive-extraction security tests pass, no privileged process writes user config | **PASS (race + named security tests + app/release/PHP CI)** / **NOT MEASURED (privileged config writes)** | `go test ./... -race` PASS. `TestHelperRejectsWrongUID` PASS; `TestExtractionRejectsTraversalAndSymlink` (parent traversal, absolute path, absolute/escaping/ancestor/chain symlinks, write-through chain, wrapper-relative escape, hardlink, device), `TestExtractionRejectsSymlinkChainWithoutWritingOutside`, and `TestInstallVerifiesChecksumAndExtractsSafely` PASS; installer archive checks PASS. PHP build/smoke CI run `40135286787` PASS for 8.2–8.5; app CI PASS at `4f33469` and `749e651`; release workflow `40139716377` PASS. No test proves the “no privileged process writes user config” subcriterion; it is not claimed. |
| 11 | Docs cover install, first site, PHP versions/ini/extensions, TLS repair, WSL paths/performance, upgrades, uninstall | **PASS (Linux)** / **DEFERRED** (WSL) | README and `docs/install.md`, `upgrade.md`, `release.md`, `php.md`, and `vhosts.md` were updated for release install, upgrade backup, Linux-only/unsigned scope, the PHP catalog gate, `doctor --json`, and no diagnostics bundle. WSL documentation is intentionally absent until Plan 3. |
| 12 | External serving works end to end for nginx/Apache; broken vhost leaves the previous mode serving; uninstall removes our vhosts/bridge/TLS without touching unrelated vhosts | **PASS (nginx + Apache HTTPS automated)** / **MANUAL** (Apache mode switch, uninstall) | Real nginx ran from a temporary prefix on high ports; `builtin → nginx → builtin` served the static site, applied the force-HTTPS redirect, and restored the forwarder. Rollback tests `TestSetModeRestoresOnFailure` and `TestSetModeExternalRestoresOldBridgeOnNewFailure` PASS. With `mod_ssl` installed, the rendered Apache PHP vhost passed `httpd -t` over HTTP and HTTPS, then a live Apache TLS listener served a real HTTPS request through PHP-FPM. Real Apache mode switching and privileged vhost/uninstall cleanup remain manual. |

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
because that would modify real units, config, and privileged paths.

The published release was also exercised against GitHub:

```text
$ gh release view v0.1.0 --json assets
devstack_0.1.0_linux_amd64.tar.gz  10739212
checksums.txt                            100
install.sh                             23378

$ ./install.sh --dry-run --release 0.1.0
install: checksum verified for devstack_0.1.0_linux_amd64.tar.gz
install: archive members verified; extracting to a temporary directory
install: dry run: no files or services will be changed
```

That run contacted the public `v0.1.0` release and verified the real archive
checksum. The downloaded archive was also extracted and inspected:

```text
$ ./devstack version --json
{"version":"0.1.0","commit":"4f33469","string":"0.1.0 (4f33469)","protocol":"1","os":"linux","arch":"amd64","go_version":"go1.26.0"}

$ strings devstack | grep 'php@8.2.35-r1\|php@8.5.12-r1'
"id": "php@8.2.35-r1",
"id": "php@8.5.12-r1",
```

The release binary therefore contains the PHP catalog, not just the source
tree. A full non-dry installation was still not performed on the host.

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
Apache HTTPS PHP configtest: Syntax OK
Apache HTTPS PHP service served blog.test over TLS
PHP in-flight swap: 89 requests succeeded, 0 failed
acceptance: doctor php-builds/php-config/php-ports/php-master all ok
cleanup: blog FastCGI ports 43000/43002/43003 and api port 43001 are free after StopAll
```

The cross-line test used the published archives:

```text
PHP cross-line acceptance: 8.2.35 and 8.5.12 served side by side
cleanup: cross82 FastCGI port 43000 is free after StopAll
cleanup: cross85 FastCGI port 43001 is free after StopAll
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

The production catalog now contains four PHP entries and preserves the two Node
entries:

```sh
jq -r '.entries[] | [.kind,.id,.version] | @tsv' internal/catalog/catalog.json
# node  node@24.22.1  24.22.1
# node  node@22.23.4  22.23.4
# php   php@8.2.35-r1 8.2.35
# php   php@8.3.36-r1 8.3.36
# php   php@8.4.27-r1 8.4.27
# php   php@8.5.12-r1 8.5.12
```

## Remaining gates (not passes)

1. **Windows/WSL — DEFERRED by user request.** Plan 3 is not started. No
   Windows binary, agent, named pipe, `install.ps1`, WSL lifecycle, or Windows
   trust-store work exists.
2. **Browser trust, root install/uninstall on 80/443, live UI checks — MANUAL.**
   These require changing host trust stores, privileged ports, or a live
   browser session and were not performed by the unprivileged runner.
3. **Privileged process config writes — NOT MEASURED.** No test proves the
   “no privileged process writes user config” subcriterion; it is not claimed.
4. **Manual multi-distro matrix — NOT RUN.** Ubuntu 22.04/24.04, Debian 12,
   Fedora 44, and Windows/WSL were not exercised as a full manual matrix;
   Fedora 44 was the automated acceptance host.

The app release `v0.1.0`, the PHP runtime release
`php-runtimes-2026-10-25`, app CI, and the tag-driven release workflow are now
published/observed. Do not infer Windows or manual-gate passes from them.

## Failures found and fixed during acceptance

The first full `make acceptance` run failed two phases (`go test` and
`go test race`) in `internal/helper` and `internal/hosts`. The cause was the
runner's global `umask 077`, which changed the modes of files created by tests
that assert `0644`/`0640` preservation. The runner no longer changes the umask;
`mktemp -d` already creates its log directory as `0700`. The second full run
at that stage passed with 15 PASS, 0 FAIL. No product code or test assertion was
changed to make the run pass.

The Task 4 review then found a clean-checkout dependency-order bug: UI phases ran
before the only `npm ci` path, and the release archive test could leave a
test-version `dist/devstack`. The fix makes `make build` precede the UI phases
and gives `build/release.sh` a `--build-dir` option that the release test points
at a temporary directory, so `dist/devstack` is never touched. The catalog gate
now preflights `jq` and FAILs for a missing parser, invalid JSON, a malformed
schema, an unexpected kind, or a PHP-bearing catalog. The clean-tree run above
verifies the fixes. The runner also prints each phase's log basename and the
kept log directory.

The final release wave found two additional issues while executing the new
gates:

- The first in-flight load run returned 502s for requests already queued on the
  old FastCGI listener: 14/52 failed. The fix adds a 2-second drain grace after
  the route snapshot moves to the new generation, before the old master is
  stopped. The rerun passed with **89/89** requests.
- The live Apache HTTPS test initially included Fedora’s system `ssl.conf`,
  which requires a localhost certificate and was unrelated to the rendered
  vhost. The test now writes a minimal Apache config, includes the host module
  files, and starts the rendered TLS vhost on a high port. It served a real
  HTTPS request through PHP-FPM.
- `TestLoadProductionCatalog` still expected the Node-only catalog. It now
  asserts six entries: two Node builds and four PHP builds with exact URLs,
  checksums, sizes, build, and revision.
- A master CI run hit a timing-only supervisor assertion that checked the pid
  file immediately after the crash status. The test now polls for pid-file
  removal; the supervisor behavior was not weakened.

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
bash install.sh --dry-run --release 0.1.0
E2E_SKIP_BUILD=1 bash test/e2e/linux.sh
DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run 'TestPHPSubsystemAcceptance|TestPHPInflightSwapContinuity' -count=1 -v
DEVSTACK_PHP_82_ROOT=/tmp/opencode/php-8.2 DEVSTACK_PHP_85_ROOT=/tmp/opencode/php-8.5 go test ./test/php -run TestPHPCrossLineAcceptance -count=1 -v
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
- `docs/release.md`: published `v0.1.0`, PHP runtime release, no diagnostics
  bundle, unsigned scope, and links to this report and the handoff.
- `docs/php.md`: published 8.2–8.5 catalog entries, real side-by-side evidence,
  and the in-flight drain behavior.
- `docs/vhosts.md`: Windows/WSL deferred, live Apache HTTPS evidence, manual
  Apache serving, and how the acceptance runner reports E2E skips.

## Final statement

This is an honest Linux acceptance result for the implemented M1 scope, not a
claim that M1 is complete. Windows/WSL and the manual trust/root/UI gates remain
open. The Linux release is merged, tagged, and published, and the automated
gates above are green.
