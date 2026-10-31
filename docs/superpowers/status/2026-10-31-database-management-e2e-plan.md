# Database management - exhaustive E2E plan

- **Status:** planned only. No live E2E has been started.
- **Implementation under test:** record the exact `master` SHA and binary hashes at the start of the run. The plan was written after `8124175` (phpMyAdmin `db` alias fix); do not assume that SHA is still HEAD.
- **Scope:** Windows daemon + WSL agent, Linux helper, MySQL 8.4, MariaDB 11.8, PostgreSQL 18, Redis 8.8, phpMyAdmin 5.2.3, CLI, HTTP API, and the Svelte dashboard.
- **Rule:** every run uses binaries built from the recorded SHA. Do not use the installed v0.3.1 binary, a pre-merge build, or a stale `dist/gate` binary.

## What "bulletproof" means here

No finite test suite can prove 100% correctness. For this plan, "bulletproof" means:

1. Every **P0** scenario passes in every required environment.
2. Every **P1** scenario passes, or has a written, accepted residual risk.
3. Every destructive action has a tested recovery path.
4. No password, DSN, secret file body, or install token appears in evidence, logs, `state.db`, SSE, or the DOM.
5. The Windows/WSL and Linux helper paths produce equivalent user-visible results.
6. The run is reproducible from a recorded SHA, fixture set, and command transcript.
7. The residual-risk register is complete and signed off.

The plan intentionally includes tests that are expected to fail or return documented errors. A negative test passes only when the documented error code and the unchanged state are both verified.

## Priority definitions

| Priority | Meaning | Release rule |
|---|---|---|
| **P0** | Data loss, credential leak, install/upgrade failure, login failure, destructive-action failure | Must pass; no exceptions |
| **P1** | Core database/user/site/backup/Redis/dashboard flow | Must pass or have an accepted residual risk |
| **P2** | Important edge case, regression, scale, or parity gap | Fix or record with owner |
| **P3** | Exploratory/adversarial, time-boxed | Record findings; not a gate |

## Required environments

| ID | Environment | Daemon | Privileged path | Purpose |
|---|---|---|---|---|
| **WIN** | Windows 11 24H2 + WSL Debian 12 | fresh `devstack.exe` | WSL agent | primary Windows flow |
| **WIN-REAL** | Existing real install `C:\Users\Aram\.devstack` | fresh binary | WSL agent | upgrade, repair, read-only smoke; destructive only with backup/rollback and approval |
| **LIN-DEB** | Debian 12 (`devstack` distro or a disposable clone) | fresh Linux `devstack` | Linux `devstack-helper` | helper parity |
| **LIN-UBU** | Ubuntu 26.04 (current machine) | fresh Linux `devstack` | Linux `devstack-helper` | distro parity; Docker/sqlite3 setup required |
| **LIN-FED** | Fedora 44 (current machine) | fresh Linux `devstack` | Linux `devstack-helper` | distro parity; Docker/Go/sqlite3 setup required; record unsupported if not |
| **UI-MOCK** | Vite dev server, `VITE_API_MODE=mock` | in-page mock API | mocked | deterministic UI regression |
| **UI-LIVE** | Fresh daemon, dashboard auth enabled | same as WIN or LIN | real | live dashboard smoke |
| **CLI-PS5** | Windows PowerShell 5.1 | n/a | n/a | CLI compatibility |
| **CLI-PS7** | PowerShell 7 | n/a | n/a | CLI compatibility |
| **CLI-BASH** | Debian/Ubuntu bash | n/a | n/a | CLI compatibility |

Prefer a disposable home or the R07 helper handover for destructive scenarios. **WIN-REAL** is a fallback only after a verified baseline backup/rollback and explicit approval; never run a destructive scenario against it without a rehearsed rollback. Use a disposable Linux home for **LIN-***.

### Isolation constraints that the plan must respect

- The Windows helper is a **singleton** service (`DevstackHelper`) pointed at `C:\Users\Aram\.devstack\system\helper.yaml`. A disposable Windows home cannot have its own helper service at the same time.
- The Windows daemon talks to the fixed helper pipe and the WSL agent uses the fixed `/data/devstack` root. `install.ps1 -TestSandbox` is an installer-mechanics sandbox; it skips the live daemon/helper and is **not** a live-service E2E environment.
- Service ports are fixed by the catalog: MySQL/MariaDB 3306, PostgreSQL 5432, Redis 6379, phpMyAdmin 8082. Two homes cannot run the same service on the same machine at the same time.
- The real install currently has MySQL and phpMyAdmin active on 3306/8082. Quiesce them before any disposable-home service scenario, or run the scenario against **WIN-REAL** with a verified backup/rollback.
- The Linux helper can run as a foreground process with a custom `--config` and socket (see `test/e2e/linux.sh`), but the Docker engine and fixed service ports are still shared per distro. Use a disposable distro/clone and a disposable home, not the real `/data/devstack` agent.
- `-TestSandbox` is only for E2E-13 installer mechanics. Live service tests need a real helper handover or **WIN-REAL** with backup.

## Machine readiness audit (2026-10-31)

This audit records what is already available on this machine and what must be installed or built before the plan can run. Re-run it at execution time; do not treat this snapshot as permanent.

| Item | Status | Evidence / gap |
|---|---|---|
| Windows host | Ready | Windows 11 build 26300, elevated shell available |
| PowerShell 5.1 / 7 | Ready | 5.1.26100.9711 and 7.5.4 |
| WSL Debian 12 (`devstack`) | Ready | systemd and Docker present; this is the real install |
| WSL Ubuntu 26.04 | Partial | systemd and Go present; **Docker and sqlite3 missing** |
| WSL Fedora 44 | Partial | systemd present; **Docker, Go, and sqlite3 missing** |
| Windows helper | Ready but singleton | `DevstackHelper` points at `C:\Users\Aram\.devstack`; only one live helper at a time |
| Real install services | Active | MySQL 3306 and phpMyAdmin 8082 are running; quiesce before disposable-home tests |
| Docker images | Partial | MySQL and phpMyAdmin are cached; MariaDB, PostgreSQL, and Redis images must be pulled |
| Playwright | Partial | Chromium installed; **Firefox and WebKit are not installed** |
| Database E2E harness | Missing | No `test/e2e/database-management/` fixtures, scripts, result tracker, or secret scanner |
| Database Playwright specs | Missing | `ui/tests/e2e.spec.ts` has no database/backup/Redis/phpMyAdmin scenarios; live-dashboard config is not written |
| sqlite3 | Missing | Not on Windows, Debian, Ubuntu, or Fedora; audit-table checks need an install or a Go/WSL reader |
| Previous binary for upgrade | Ready | `dist/update-20261031/*.pre-upgrade` and the `v0.3.1` tag |
| Admin/root access | Ready | Windows elevated; WSL root available |
| Disk / RAM | Ready | ~214 GiB free, 31.2 GiB RAM; re-check before the 1 GiB import and 100-object scale tests |
| Network | Unverified | Docker pulls, Playwright browser downloads, and apt/dnf installs need connectivity |

### Readiness tasks (must pass before the P0/P1 run)

- [x] **R01:** Install Playwright Firefox and WebKit; record versions and browser download sizes. *(Cycle 0: Firefox 157.0 / build v1555, 346.3 MiB; WebKit 27.2 / build v2370, 169.6 MiB; both headless smokes pass.)*
- [ ] **R02:** Install `sqlite3` on Windows and in the Linux test distros, or document a supported Go/WSL audit reader. Do not rely on a missing tool. *(Partial, cycle 0: Ubuntu 26.04 has sqlite3 3.46.1 and read a copy of `state.db` with `integrity_check=ok`; native Windows and Fedora readers are still absent.)*
- [ ] **R03:** Install Docker in Ubuntu 26.04 and Fedora 44, or create a disposable Debian 12 clone with Docker. Verify `systemctl` and `docker info`. *(Partial, cycle 0: Ubuntu 26.04 has Docker 29.1.3 + Compose 2.40.3, systemd running, `docker info` healthy; Fedora 44 still lacks Docker.)*
- [x] **R04:** Pull MariaDB, PostgreSQL, and Redis images; record digests and sizes. *(Cycle 0: MariaDB 11.8.9 `f16d7a34...` 458 MB, PostgreSQL 18.6 `edb0c08d...` 650 MB, Redis 8.8.3 `418e397f...` 209 MB in the Ubuntu engine.)*
- [x] **R05:** Create `test/e2e/database-management/` fixtures, seed/verify scripts, result tracker, secret scanner, cleanup, and rollback scripts. *(Cycle 1: harness, fixtures, tracker, scanner, cleanup, rollback, and isolation docs created; R12 smoke PASS.)*
- [x] **R06:** Write the database-management Playwright specs and a live-dashboard Playwright config/spec. *(Cycle 2: 17 database-management mock tests and the 32 existing dashboard tests pass on Chromium, Firefox and WebKit (147/147); live config/spec added and skipped by default; full live auth remains BLOCKED because the real install has dashboard auth disabled and no test token was supplied.)*
- [x] **R07:** Choose and document the Windows live-service isolation model:
  - **A (preferred):** stop the real daemon/helper, reconfigure the singleton helper to a disposable home, run tests, restore the real service. Test the handover on a disposable home first.
  - **B (fallback):** use **WIN-REAL** with the verified baseline backup/rollback and explicit approval.
  - **C:** use `-TestSandbox` only for installer mechanics, never for live service flows.
  *(Cycle 1: option A documented with gated `windows/handover-plan.ps1`, `windows/rollback.ps1`, and `windows/read-only-check.ps1`; handover not executed. Windows destructive DB tests remain BLOCKED because the WSL agent's `/data/devstack` root is not isolated.)*
- [x] **R08:** Choose and document the Linux isolation model: disposable distro/clone, disposable home, foreground helper with custom socket, separate Docker network/ports. *(Cycle 1: Ubuntu WSL distro, disposable `/tmp/devstack-e2e-*` home, unique `devstack-e2e-*` network, no published ports by default; documented in `test/e2e/database-management/README.md`.)*
- [ ] **R09:** Verify the helper binary starts on Ubuntu 26.04 and Fedora 44, or record those distros as unsupported and remove them from the gate.
- [x] **R10:** Verify network access for image pulls, browser downloads, and package installs; record failures as blocked prerequisites. *(Cycle 0: Ubuntu apt installs, Playwright CDN downloads, and Docker Hub digest pulls all succeeded.)*
- [x] **R11:** Define the shell variables used by the plan (`$ds`, `$home`, `$stamp`, `$svc`, `$id`) and the previous-binary source for E2E-14. *(Cycle 0: definitions and hashes recorded in `.superpowers/e2e-progress.md`; previous source is `dist/update-20261031/*.pre-upgrade` / `v0.3.1` at `40df5c0`.)*
- [x] **R12:** Dry-run every harness script and the cleanup/rollback script before the first destructive scenario. *(Cycle 1: `HARNESS-SMOKE` PASS; seed/verify idempotency, secret scanner self-test, cleanup, and disposable-home snapshot/rollback verified. Evidence: `dist/e2e/database-management/evidence/20261031t011321z-2677/HARNESS-SMOKE/`.)*

### What is executable today

These checks can run now without the missing harness:

- Windows real-install read-only checks: `doctor`, `service list`, `db list`/`db user list` on inactive services, `--help`, error-code checks.
- phpMyAdmin login against the repaired real install (MySQL 3306 + phpMyAdmin 8082).
- Existing mock Playwright suite on Chromium (`cd ui; npm run test:e2e`).
- Existing Go unit/integration/race suites and `test/e2e/linux.sh`.
- CLI security checks that do not require fixtures.

These cannot run until the readiness tasks pass:

- Fresh Windows disposable-home live service tests (helper singleton/ports).
- Linux helper parity on Ubuntu/Fedora (Docker/Go missing).
- Firefox/WebKit dashboard checks.
- Database/backup/Redis/phpMyAdmin Playwright scenarios.
- Audit-table assertions (sqlite3 missing).
- Scale/1 GiB tests until images and fixtures are ready.
- Any scenario that assumes the E2E harness scripts exist.

## Test data and fixtures

Create these before the scenario run. Keep them under `dist/e2e/database-management/fixtures/`.

### Databases

| Fixture | Purpose |
|---|---|
| `e2e_app` | normal database with marker rows |
| `e2e_empty` | zero-table database |
| `e2e_unicode` | Unicode/emoji identifiers and data |
| `e2e_reserved` | reserved-word and quoted identifiers |
| `e2e_long_63` / `e2e_long_128` | maximum-length identifiers |
| `e2e_case` | case-sensitivity checks |
| `e2e_fk` | foreign keys, triggers, views |
| `e2e_routines` | stored procedures/functions/events (engine-permitting) |
| `e2e_binary` | BLOB/binary data |
| `e2e_restore_*` | restore targets |

### Users

| Fixture | Purpose |
|---|---|
| `e2e_user` | managed user with generated password |
| `e2e_user_ro` | read-only grant |
| `e2e_user_admin` | admin grant |
| `e2e_external` | metadata-only external user |
| `e2e_user_unicode` | Unicode username |
| `e2e_user_case` | case-sensitivity checks |
| `e2e_user_host_local` | MySQL/MariaDB `localhost` host scope |
| `e2e_user_host_net` | MySQL/MariaDB `10.%` host scope |

### Sites

| Fixture | Purpose |
|---|---|
| `e2e-static` | minimal static site |
| `e2e-php` | PHP site with a DB link |
| `e2e-node` | Node site with a DB link |
| `e2e-multi` | site with two database links, one primary |
| `e2e-conflict` | MySQL-linked site used for MariaDB start blocking |

### SQL dumps

| File | Purpose |
|---|---|
| `seed.sql` | marker tables and rows |
| `seed.sql.gz` | gzip path |
| `seed-unicode.sql` | Unicode/emoji data |
| `seed-binary.sql` | binary/BLOB data |
| `seed-fk.sql` | foreign keys, triggers, views |
| `seed-routines.sql` | routines/events where supported |
| `seed-partial.sql` | intentionally truncated stream |
| `invalid.sql` | syntax error |
| `large.sql` / `large.sql.gz` | near-1 GiB boundary; use a generated file, not a checked-in blob |

### Redis fixtures

- `e2e_cache_ro` (read-only ACL user)
- `e2e_cache_full` (full access, still no `CONFIG`/`FLUSHALL`)
- `e2e_cache_unicode`
- a custom-rule user to verify preservation
- an existing `users.acl` file for migration tests
- a foreign `aclfile` configuration for rejection tests

### Dashboard fixtures

- mock seed data for every engine
- a stopped service, an inactive service, an empty list, a missing linked DB, a missing linked user
- a failed job, a canceled job, a running job
- a protected database/user, a linked user, a metadata-only link
- long names, Unicode names, and error payloads

## Harness tasks (do these before any scenario)

### E2E-H01 - Build provenance and binary hashes

- [ ] Record `git rev-parse HEAD`, `git status --short`, and the build command.
- [ ] Build Windows, Linux helper, and Linux agent binaries from that SHA with `CGO_ENABLED=0`, `-trimpath`, and `-ldflags "-s -w"`.
- [ ] Record SHA-256 for every binary.
- [ ] Confirm the installed binary under test is the freshly built one, not the service binary.

### E2E-H02 - Disposable homes and baselines

- [ ] Create a disposable Windows home `C:\Users\Aram\.devstack-e2e`.
- [ ] Create disposable Linux homes under `/tmp/devstack-e2e-*`.
- [ ] Back up **WIN-REAL** `state.db`, `config.yaml`, the hosts block, and the installed binaries before any upgrade test.
- [ ] Record the baseline service list, database list, user list, site list, and backup list for every environment.

### E2E-H03 - Fixture creation

- [ ] Create every database/user/site fixture listed above.
- [ ] Create every SQL dump, including the near-1 GiB file and the truncated/invalid files.
- [ ] Create the Redis ACL fixtures.
- [ ] Verify fixture creation is idempotent or cleanly resettable.

### E2E-H04 - Result tracker

- [ ] Create `dist/e2e/database-management/results.csv` with columns: `test_id, priority, environment, result, evidence_path, notes, owner`.
- [ ] Every scenario must have a row. `PASS`, `FAIL`, `SKIP`, and `BLOCKED` are distinct; never convert a skip into a pass.
- [ ] Record the command transcript and timestamps per scenario.

### E2E-H05 - Secret scanner

- [ ] Write a scanner that searches evidence, logs, `state.db`, SSE captures, DOM snapshots, and Playwright storage for:
  - generated passwords
  - `DB_PASSWORD=` / `DATABASE_URL=` userinfo
  - install tokens
  - secret-file contents
  - Redis ACL passwords
- [ ] Run the scanner after every destructive or reveal scenario and at the end of the run.

### E2E-H06 - Evidence capture

- [ ] Capture command output, exit codes, API responses, dashboard screenshots, Playwright traces, job JSON, audit rows, and filesystem listings.
- [ ] Redact secrets before writing evidence. Keep raw secrets only in the platform secret store.
- [ ] Record the exact binary SHA and environment for every evidence file.

### E2E-H07 - Cleanup and rollback

- [ ] Write a cleanup script that removes only `e2e_` databases, users, sites, links, backups, containers, and temp files.
- [ ] Write a rollback script for **WIN-REAL**: stop the daemon/helper, restore the backed-up binaries and `state.db`, restore the hosts block, and restart.
- [ ] Verify the rollback script on a disposable home before touching **WIN-REAL**.

### E2E-H08 - Browser and CLI matrix

- [ ] Playwright projects for Chromium, Firefox, and WebKit.
- [ ] Viewports: desktop 1440x900, tablet 768x1024, mobile 390x844.
- [ ] CLI runs under PowerShell 5.1, PowerShell 7, and bash.
- [ ] Record browser/CLI versions.

## Environment selection for scenario execution

- **WIN:** prefer the R07 option A helper handover to a disposable home for destructive scenarios. Use **WIN-REAL** only as the R07 option B fallback with the verified baseline backup/rollback and explicit approval. `-TestSandbox` is only for installer mechanics.
- **LIN:** use a disposable distro/clone and a disposable home. Never run destructive scenarios against the real `/data/devstack` agent.
- **UI-LIVE:** point Playwright at the daemon under test with dashboard auth enabled; do not reuse a browser profile from the real dashboard.
- **UI-MOCK:** use a fresh browser context per scenario; mock state must not leak between tests.
- If an environment cannot be isolated, mark the scenario **BLOCKED** rather than running it against shared state.

## Scenario catalog

| ID | Area | Priority | WIN | LIN | UI | Notes |
|---|---|---|---|---|---|---|
| E2E-00 | Preflight and baseline | P0 | yes | yes | - | SHA, backups, fixtures |
| E2E-01 | Windows service lifecycle | P0 | yes | - | - | install/activate/doctor |
| E2E-02 | Service-wide DB/user management | P0 | yes | yes | yes | all engines |
| E2E-03 | Add Site new DB/user | P0 | yes | yes | yes | env injection |
| E2E-04 | Existing user warning / metadata-only | P0 | yes | yes | yes | no auto-grant |
| E2E-05 | Site deletion preserves data | P0 | yes | yes | yes | re-link |
| E2E-06 | Wrong-engine start blocking | P0 | yes | yes | yes | MySQL/MariaDB |
| E2E-07 | Backup/restore/export/import | P0 | yes | yes | yes | failure recovery |
| E2E-08 | Redis users/password/ACL | P0 | yes | yes | yes | persistence |
| E2E-09 | CLI security and limitations | P0 | yes | yes | - | no secrets |
| E2E-10 | Dashboard Playwright | P0 | - | - | yes | mock + live |
| E2E-11 | Linux helper parity | P0 | - | yes | - | Debian/Ubuntu |
| E2E-12 | Cross-cutting security | P0 | yes | yes | yes | API/UI/CLI |
| E2E-13 | Fresh install (disposable) | P0 | yes | yes | - | installer |
| E2E-14 | Upgrade, repair, rollback | P0 | yes | yes | - | preservation |
| E2E-15 | Service edge cases and ports | P1 | yes | yes | yes | conflicts |
| E2E-16 | DB/user adversarial matrix | P1 | yes | yes | yes | names/grants |
| E2E-17 | Site integration adversarial | P1 | yes | yes | yes | multi-link |
| E2E-18 | Backup/import adversarial | P1 | yes | yes | yes | scale/failure |
| E2E-19 | Redis adversarial | P1 | yes | yes | yes | ACL edge cases |
| E2E-20 | phpMyAdmin login matrix | P0 | yes | yes | yes | fresh/legacy/switch |
| E2E-21 | Dashboard exhaustive | P1 | - | - | yes | all states |
| E2E-22 | API/CLI security adversarial | P0 | yes | yes | - | injection/auth |
| E2E-23 | Failure injection and recovery | P0 | yes | yes | yes | infra loss |
| E2E-24 | Scale and performance | P2 | yes | yes | yes | 1 GiB/100 objects |
| E2E-25 | Cross-platform parity | P1 | yes | yes | - | normalized compare |
| E2E-26 | Regression/adjacent features | P1 | yes | yes | yes | sites/services |
| E2E-27 | Audit, SSE, redaction | P1 | yes | yes | yes | observability |
| E2E-28 | Cleanup, exit, sign-off | P0 | yes | yes | yes | evidence |

## Group 0 - Install, upgrade, and service lifecycle

### E2E-00 - Preflight and baseline

**Priority:** P0. **Environments:** WIN, LIN.

- [ ] Confirm the working tree is clean and the recorded SHA matches the binaries.
- [ ] Record OS build, WSL version, Docker version, engine versions, and browser versions.
- [ ] Back up **WIN-REAL** before any upgrade/repair test.
- [ ] Record the baseline `service list --json`, `db list --json`, `db user list --json`, `site list --json`, `db backup list --json`, `doctor --json`.
- [ ] Confirm the evidence directory is empty and the secret scanner runs.
- [ ] Confirm the disposable homes are empty and isolated.

**Expected:** every baseline is recorded; no real-install mutation has occurred.

### E2E-13 - Fresh install (installer mechanics and live install)

**Priority:** P0. **Environments:** WIN, LIN.

Installer mechanics (no live service claims):

- [ ] Run `install.ps1 -DryRun -TestSandbox <path>` and inspect the planned operations.
- [ ] Run the sandbox install; verify layout, manifest, binary staging, mock WSL files, and uninstaller.
- [ ] Verify the sandbox does not touch the real helper service, WSL agent, hosts, or trust stores.
- [ ] Repeat the sandbox flow on Linux if the installer supports it.

Live install (requires R07/R08 isolation):

- [ ] Perform the R07 option A helper handover to a disposable home; use **WIN-REAL** with backup/rollback only as the approved fallback.
- [ ] Install the helper service and WSL agent for the selected live home.
- [ ] Run `doctor`; fix hosts/trust only inside the selected environment.
- [ ] Install/activate MySQL, PostgreSQL, Redis, and phpMyAdmin.
- [ ] Verify the manifest stage, `agent_installed`, `upgraded_at`, and binary hashes.
- [ ] Verify no real-install paths are touched when using the handover model.

**Expected:** installer mechanics are verified in the sandbox; live install is healthy in the isolated live environment; phpMyAdmin config contains `db` host.

### E2E-14 - Upgrade, repair, and rollback

**Priority:** P0. **Environments:** WIN-REAL, LIN.

- [ ] Prepare the live environment with data: databases, users, grants, passwords, backups, sites, links, Redis users, phpMyAdmin config. Use the R07 option A handover home; use **WIN-REAL** with backup/rollback only as the approved fallback. Use a disposable Linux distro/home.
- [ ] Run the upgrade from the previous binary (`dist/update-20261031/*.pre-upgrade` or the `v0.3.1` tag) to the recorded SHA.
- [ ] Verify every data object and password still works.
- [ ] Verify phpMyAdmin login after upgrade.
- [ ] Verify a legacy/broken phpMyAdmin config is repaired via the documented `service config apply` path, then login works.
- [ ] Verify a user-owned phpMyAdmin config is not overwritten by the upgrade.
- [ ] Inject upgrade failures: missing agent binary, agent health failure, helper start failure, WSL copy failure.
- [ ] Verify rollback restores Windows binaries, WSL data, helper service, agent health, and the previous config.
- [ ] Verify the manifest and `doctor` after success and after rollback.
- [ ] Run the same upgrade/rollback on Linux helper.

**Expected:** no data loss; no credential loss; rollback is complete; broken phpMyAdmin config has a working repair path.

### E2E-01 - Windows service lifecycle

**Priority:** P0. **Environments:** WIN.

- [ ] Install and activate MySQL, PostgreSQL, Redis, and phpMyAdmin.
- [ ] Verify ports: MySQL 3306, PostgreSQL 5432, Redis 6379, phpMyAdmin 8082, all loopback.
- [ ] Deactivate/activate/restart/stop each service; verify health and container state.
- [ ] Verify `doctor` after each transition.
- [ ] Verify phpMyAdmin login with MySQL active and with MySQL stopped (clear error, no crash).
- [ ] Verify service uninstall keep-data and delete-data semantics.
- [ ] Verify concurrent service operations do not corrupt state.

**Expected:** all lifecycle transitions are clean; no orphan containers; phpMyAdmin login works when MySQL is active.

### E2E-15 - Service edge cases and ports

**Priority:** P1. **Environments:** WIN, LIN.

- [ ] MySQL and MariaDB are mutually exclusive on 3306; activating one deactivates the other with a warning.
- [ ] Starting a MySQL-linked site while MariaDB is active is blocked (E2E-06).
- [ ] PostgreSQL on 5432 does not conflict with the MySQL family.
- [ ] Redis on 6379 does not conflict with SQL services.
- [ ] Port already in use by an unrelated process produces a clear error.
- [ ] Service config apply/diff/reset works and preserves user edits.
- [ ] Service credentials reveal/rotate works for MySQL/PostgreSQL/Redis.
- [ ] Docker daemon down, WSL agent down, helper down produce clear errors and recover after restart.
- [ ] Uninstall with linked sites warns and preserves data by default.

**Expected:** conflicts fail closed with actionable messages; no silent data deletion.

## Group 1 - Service-wide databases and users

### E2E-02 - Service-wide database and user management

**Priority:** P0. **Environments:** WIN, LIN, UI.

Run the full matrix on MySQL and PostgreSQL; repeat on MariaDB after E2E-06.

- [ ] Create databases on MySQL, MariaDB, and PostgreSQL.
- [ ] Create managed users with `read_only`, `read_write`, `read_write_schema`, `admin`, and `custom` grants.
- [ ] Create users with MySQL/MariaDB host scopes `%`, `localhost`, `127.0.0.1`, `10.%`.
- [ ] List databases/users; assert no password, secret, or URL userinfo appears.
- [ ] Reveal a password once; verify the warning and audit row.
- [ ] Rotate a password; verify the old password fails and the new one works.
- [ ] Grant and revoke access; verify engine grants and metadata agree.
- [ ] Drop a database with and without `--backup-first`.
- [ ] Rename PostgreSQL; verify links/grants update.
- [ ] Verify MySQL/MariaDB rename fails before the API call with the documented code.
- [ ] Verify protected objects: `mysql`, `information_schema`, `performance_schema`, `sys`, `postgres`, `template0`, `template1`, `root`, `postgres`, `mysql.sys`, `mysql.session`, `mysql.infoschema`, Redis `default`.
- [ ] Verify a user linked to a site cannot be deleted; the error lists the linked sites.
- [ ] Verify deleting a site leaves the user/database/grants/password/backups intact (E2E-05).
- [ ] Verify CLI, API, and dashboard produce the same result.

**Expected:** all CRUD/grants/passwords behave consistently; protected and linked objects fail closed.

### E2E-16 - Database/user adversarial matrix

**Priority:** P1. **Environments:** WIN, LIN, UI.

- [ ] Names: `e2e_app`, `E2E_App`, `e2e-app`, `e2e.app`, `e2e app` (reject), `e2e;drop`, `e2e'`, `e2e"`, ``e2e` ``, `../e2e`, `e2e/../x`, 63/64/128 characters, Unicode, emoji, reserved words (`select`, `order`, `user`), and system names.
- [ ] Hosts: `%`, `localhost`, `127.0.0.1`, `10.%`, `::1`, empty, whitespace, `%foo`, wildcard injection.
- [ ] Grants: valid presets, custom privileges, wildcard database names, missing database, protected database, duplicate grant, revoke non-existent grant.
- [ ] Passwords: reveal twice, rotate, old password invalidation, metadata-only, `--set` fails with `DATABASE_PASSWORD_SET_UNSUPPORTED`, external password flag fails with `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED`.
- [ ] Concurrency: two creates with the same name, create+drop, rotate+delete, grant+revoke, link+delete user.
- [ ] Stale metadata: drop a DB externally, list flags it missing and offers remove-link; drop a user externally; list flags it.
- [ ] Verify every mutation writes an audit row and emits the documented SSE event.
- [ ] Verify list JSON, CLI table output, and dashboard do not leak secrets.

**Expected:** invalid input is rejected before engine changes; concurrent operations have one winner and a clear conflict; stale metadata never recreates objects.

## Group 2 - Site integration

### E2E-03 - Add Site with a new database and new user

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] Create a static project.
- [ ] Add a site with a new database and new user.
- [ ] Verify the review step shows database, user, grant, host, port; no password; password-generated copy.
- [ ] Verify runtime env (`DB_CONNECTION`, `DB_HOST`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`) without printing the password.
- [ ] Verify `DB_PASSWORD` is present for a managed user and absent for metadata-only.
- [ ] Verify `state.db`, persisted site env, and `php-fpm.conf` do not contain the password.
- [ ] Verify Site Detail masks the password until reveal/copy; reveal is audited.
- [ ] Verify terminal/exec env sees the same values.

**Expected:** site is created; env is dynamic; no secret is persisted in site metadata.

### E2E-04 - Existing user warning and metadata-only link

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] Create an existing user with no grant on the target database.
- [ ] Link it from Add Site or Site Detail; verify the warning and no auto-grant.
- [ ] Verify **Manage database user** opens the user editor.
- [ ] Create a metadata-only external user link; verify `DB_PASSWORD` and `DATABASE_URL` are absent.
- [ ] Verify Site Detail shows "Password not managed" and the **Set password** action.
- [ ] Verify `--database-user-password-stdin` fails with `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED` without reading stdin.
- [ ] Verify the user can be granted access explicitly and the site restarts cleanly.

**Expected:** existing users are never auto-granted; metadata-only links are honest about missing credentials.

### E2E-05 - Site deletion preserves service-wide data

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] Seed a marker table/row in the linked database.
- [ ] Remove the site.
- [ ] Verify the site and links are gone; database, user, grants, password, backups, and marker row remain.
- [ ] Recreate a site and link the same database/user; the marker row is readable.
- [ ] Uninstall the service with keep-data; verify data/users/backups survive.
- [ ] Uninstall with delete-data; verify data deletion happens first, then links are removed.
- [ ] Simulate data-deletion failure; verify links are not removed and the error is actionable.

**Expected:** site deletion never cascades to service-wide data; delete-data is transactional.

### E2E-06 - Wrong-engine start blocking

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] Keep a MySQL-linked site.
- [ ] Deactivate MySQL, install/activate MariaDB on 3306.
- [ ] Try to start the site; verify the clear "MySQL linked but MariaDB is active" error and no container start.
- [ ] Deactivate MariaDB, reactivate MySQL, start successfully.
- [ ] Repeat from the dashboard and API.
- [ ] Verify the site status and linked-site impact warning are accurate.

**Expected:** wrong-engine starts fail closed with a clear explanation; no silent cross-engine connection.

### E2E-17 - Site integration adversarial

**Priority:** P1. **Environments:** WIN, LIN, UI.

- [ ] Multiple links: two databases on one site; one primary; env namespacing; switch primary; unlink one.
- [ ] Reserved env keys: user-set `DB_HOST`/`DB_PASSWORD`; verify link value wins and the UI warns.
- [ ] Add Site validation: duplicate DB/user names, protected identifiers, invalid names, missing service, inactive service, invalid grant, existing user with grant flag (reject).
- [ ] Concurrent link/delete, drop linked DB, delete linked user, two links same DB.
- [ ] Site Detail reveal/copy/set-password/unlink; route changes clear revealed secrets.
- [ ] Password/grant change shows "Restart linked sites to apply"; explicit restart works.
- [ ] Linked site stop/start behavior for overwrite restore/replace import.
- [ ] Verify no password in Add Site review, Site Detail initial render, or browser storage.

**Expected:** multi-link and stale-state cases are deterministic; secrets stay masked.

## Group 3 - Backup, restore, export, and import

### E2E-07 - Backup, restore, export, import

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] Seed known rows in `e2e_app`.
- [ ] Create a manual backup; verify metadata, size, SHA-256, gzip integrity.
- [ ] List, download, and delete backups; verify stopped-service behavior.
- [ ] Restore to a new target; verify marker rows and original unchanged.
- [ ] Overwrite restore: wrong typed confirmation rejected; correct confirmation creates a pre-restore backup first.
- [ ] Stop linked sites before overwrite; verify the UI/CLI refuses otherwise.
- [ ] Export to file and stdout; verify gzip and checksum.
- [ ] Import merge and replace; verify pre-import backup for replace.
- [ ] Import `.sql` and `.sql.gz`; verify staging cleanup and 0600 permissions.
- [ ] Invalid/truncated dump: job fails, target flagged incomplete, pre-op backup restores.
- [ ] Cancel a long job; verify canceled state and no orphan container/process.
- [ ] Daemon restart mid-job: verify reconciliation marks stale jobs failed.
- [ ] Verify Redis rejects backup/restore/import/export with `DATABASE_UNSUPPORTED`.

**Expected:** backup/restore/import are recoverable and never silently destroy the only copy.

### E2E-18 - Backup/import adversarial and scale

**Priority:** P1. **Environments:** WIN, LIN, UI.

- [ ] Backup kinds: manual, pre_drop, pre_restore, pre_import; list/filter.
- [ ] Restore target exists; overwrite without pre-restore backup; target is protected; target is a linked DB.
- [ ] Import near-1 GiB file; verify memory, disk, timeout, and cleanup.
- [ ] Import file over 1 GiB; verify `PAYLOAD_TOO_LARGE`.
- [ ] Import invalid gzip/truncated stream/partial multipart; verify failure and target flag.
- [ ] Import with triggers/views/procedures/events/foreign keys/Unicode/binary data; verify content and charset.
- [ ] Export while service stopped; verify inactive-service error.
- [ ] Export to a path with `..`, absolute path, symlink, or unwritable directory; verify rejection.
- [ ] Disk-full simulation: verify job failure, no corrupt metadata, cleanup.
- [ ] Concurrent backup + import + restore on one service; verify serialization.
- [ ] Backup delete typed-confirmation gap (known limitation): verify current behavior and record residual risk.

**Expected:** every failure leaves a usable pre-op backup or a clearly flagged target; no partial file is treated as valid.

## Group 4 - Redis

### E2E-08 - Redis users, password, and ACL persistence

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] Verify only Users and Password tabs; no Databases/Backups.
- [ ] Create read-only and full users.
- [ ] Reveal/rotate passwords; list JSON contains no password.
- [ ] First mutation before ACL persistence returns `CONFIRMATION_REQUIRED` / HTTP 409; no config change or restart.
- [ ] Enable ACL persistence explicitly; verify `redis.conf` `aclfile`, `users.acl`, one restart, users survive.
- [ ] Retry the mutation; succeeds.
- [ ] Verify `full` cannot run `CONFIG` or `FLUSHALL`.
- [ ] Verify Redis DB/backup commands fail with `DATABASE_UNSUPPORTED`.
- [ ] Verify default password route works and is audited.
- [ ] Verify site links to Redis are rejected.

**Expected:** Redis ACL changes are durable only after explicit confirmation; no SQL features leak into Redis.

### E2E-19 - Redis adversarial

**Priority:** P1. **Environments:** WIN, LIN, UI.

- [ ] Existing `users.acl` with custom rules; verify migration preserves them.
- [ ] Foreign `aclfile` config; verify rejection and no destructive rewrite.
- [ ] Migration failure/rollback; verify config backup and Redis recovery.
- [ ] Idempotent enable; verify no second restart.
- [ ] ACL file permissions and ownership.
- [ ] Unicode/case/long usernames; protected `default`; duplicate user.
- [ ] Concurrent user create/delete/password rotate.
- [ ] Redis restart while users exist; verify login/ACL persistence.
- [ ] Verify no password in `redis.conf`, `users.acl` (hashes only), logs, or evidence.

**Expected:** ACL migration is safe, idempotent, and preserves custom rules.

## Group 5 - phpMyAdmin

### E2E-20 - phpMyAdmin login matrix

**Priority:** P0. **Environments:** WIN, LIN, UI-LIVE.

- [ ] Fresh install phpMyAdmin after MySQL; verify `config.user.inc.php` contains:
  - `$cfg['Servers'][1]['host'] = 'db';`
  - `$cfg['Servers'][1]['port'] = '3306';`
  - `$cfg['Servers'][1]['auth_type'] = 'cookie';`
  - no credentials.
- [ ] Verify `PMA_HOST`/`PMA_PORT` env and `PMA_ARBITRARY=0`.
- [ ] Log in with the `devstack` MySQL credentials; verify the dashboard, `Log out`, and `Server version`.
- [ ] Wrong password: verify a clear access-denied error, no crash.
- [ ] Switch MySQL to MariaDB; verify phpMyAdmin login still works via `db`.
- [ ] Restart phpMyAdmin; verify login still works.
- [ ] Stop MySQL; verify a clear connection error, not a hang or 500.
- [ ] Legacy/broken config (old agent template) in a disposable home: verify the documented `service config apply` repair and login.
- [ ] Upgrade with an existing user-owned config: verify it is not overwritten; document the repair path.
- [ ] Verify phpMyAdmin is loopback-only and not exposed publicly.
- [ ] Verify the phpMyAdmin healthcheck and container restart behavior.
- [ ] Repeat on Linux helper.
- [ ] Playwright live login and screenshot.

**Expected:** phpMyAdmin login works on fresh installs, after engine switches, after restarts, and after the documented legacy-config repair.

## Group 6 - Dashboard

### E2E-10 - Dashboard Playwright (mock and live)

**Priority:** P0. **Environments:** UI-MOCK, UI-LIVE.

Mock mode:

- [ ] Add database-management scenarios to `ui/tests/e2e.spec.ts` or a sibling spec.
- [ ] Service detail: Databases, Users, Backups for MySQL/PostgreSQL; Redis only Users/Password.
- [ ] Create/drop database; create/edit/delete user; grants; reveal/copy; password state.
- [ ] Backup create/list/download/delete; restore/import/export; job progress/toasts; failure/canceled jobs.
- [ ] Add Site step 3; review no password; create; Site Detail card; reveal masking; delete copy.
- [ ] Redis ACL persistence confirm dialog.
- [ ] Protected objects, linked users, metadata-only links, missing DB/user.
- [ ] Loading, empty, error, and stale-data states.
- [ ] Keyboard navigation, Escape/focus restore, skip link.
- [ ] Responsive desktop/tablet/mobile.
- [ ] Zero console errors and no unhandled network failures.
- [ ] No secrets in DOM, localStorage, sessionStorage, or URLs.

Live mode:

- [ ] Start the daemon from the recorded binary with dashboard auth enabled.
- [ ] Open the dashboard with the install token in the URL fragment.
- [ ] Exercise the same core flows against real engines.
- [ ] Verify session/CSRF behavior, expiry, and logout.
- [ ] Save screenshots and the Playwright report.

### E2E-21 - Dashboard exhaustive states

**Priority:** P1. **Environments:** UI-MOCK, UI-LIVE.

- [ ] Every dialog: create/drop/rename/restore/import/delete backup/delete user/unlink/ACL persistence.
- [ ] Typed confirmations: wrong text disabled, correct text enables, cancel is safe.
- [ ] Job toasts: queued/running/succeeded/failed/canceled; polling and SSE updates.
- [ ] Overlapping responses: slow list + refresh; stale generation guard; no flicker/regression.
- [ ] Network failure: banner, retry, no partial mutation.
- [ ] Browser refresh mid-job; job recovery from persisted state.
- [ ] Route changes clear revealed secrets and dialogs.
- [ ] Cross-browser Chromium/Firefox/WebKit; responsive viewports.
- [ ] Accessibility: labels, roles, focus trap, contrast, keyboard-only.
- [ ] No secrets in browser storage or console.

**Expected:** the dashboard is deterministic under slow/failing/overlapping requests and never leaks secrets.

## Group 7 - CLI, API, and security

### E2E-09 - CLI security and documented limitations

**Priority:** P0. **Environments:** WIN, LIN, CLI-PS5/7/BASH.

- [ ] `db list --json` and `db user list --json` contain no passwords for every engine.
- [ ] `db user password --reveal` warns once and audits.
- [ ] `db user password --set` fails with `DATABASE_PASSWORD_SET_UNSUPPORTED` before any API call and never reads stdin.
- [ ] `site add --database-user-password-stdin` fails with `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED` before any API call.
- [ ] `db rename --service mysql|mariadb` fails before the API call with the documented remediation.
- [ ] Destructive commands require `--yes`; wrong/missing `--yes` leaves state unchanged.
- [ ] Export to stdout is binary-clean; progress goes to stderr.
- [ ] No password in argv, shell history, process list, logs, or JSON errors.
- [ ] Exit codes and error codes are stable for every documented failure.

**Expected:** the CLI never leaks secrets and fails fast on unsupported operations.

### E2E-12 - Cross-cutting security

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] API without install token -> 401/403; mutation without CSRF -> rejected.
- [ ] Unauthorized reveal -> denied and audited.
- [ ] IDOR: access another service/site/user/backup/job ID.
- [ ] Method not allowed, malformed JSON, duplicate fields, huge body, oversized headers.
- [ ] Import/backup path traversal, absolute paths, symlinks, `..`.
- [ ] SQL/name injection through database/user/grant/host fields.
- [ ] Protected objects cannot be dropped/deleted or have admin grants reduced.
- [ ] Linked user cannot be deleted until links are removed.
- [ ] Wrong-engine start blocked from API and UI.
- [ ] Audit rows for create/drop/rename/reveal/rotate/grant/backup/restore/import/link/unlink.
- [ ] SSE events use documented names and contain no secrets.
- [ ] Install token, CSRF cookie flags, session expiry, logout.
- [ ] phpMyAdmin `PMA_ARBITRARY=0`; no public exposure.

### E2E-22 - API/CLI adversarial

**Priority:** P0. **Environments:** WIN, LIN.

- [ ] Fuzz identifiers, hosts, grants, filenames, multipart boundaries, and JSON types.
- [ ] Replay reveal/rotate/delete requests; verify audit and idempotency expectations.
- [ ] Concurrent API mutations on one service; verify serialization and no lost updates.
- [ ] Body size boundary: 1 GiB import, just over 1 GiB, chunked/truncated.
- [ ] SSE reconnect/replay; verify no secret and no duplicate terminal events.
- [ ] Rate/timeout behavior; verify clear errors and no orphan jobs.
- [ ] Token rotation/restart; verify old token rejected.
- [ ] CORS/host-header/loopback checks.

**Expected:** adversarial input is rejected or safely handled; no secret or state corruption.

## Group 8 - Failure, scale, parity, regression

### E2E-23 - Failure injection and recovery

**Priority:** P0. **Environments:** WIN, LIN, UI.

- [ ] Docker daemon stop/start during service operation.
- [ ] WSL agent stop/restart during database/backup job.
- [ ] Linux helper stop/restart during operation.
- [ ] MySQL/PostgreSQL/Redis crash/restart during a job.
- [ ] Disk full during backup/import/restore.
- [ ] Permission denied on backup/import/secret paths.
- [ ] Network timeout/connection reset during stream.
- [ ] Daemon restart mid-job; verify reconciliation and no stuck UI.
- [ ] Service restart mid-job; verify job failure/cancel and target flag.
- [ ] Cancel long job; verify no orphan container/process/temp file.
- [ ] External DB/user drop; verify missing-link handling.
- [ ] Port conflict; config drift; wrong engine.
- [ ] Concurrent operations; verify locks and clear conflicts.

**Expected:** every failure produces a clear error, a recoverable state, and no silent data loss.

### E2E-24 - Scale and performance

**Priority:** P2. **Environments:** WIN, LIN, UI.

- [ ] 100 databases and 100 users on one service.
- [ ] 100 sites/links; dashboard list performance.
- [ ] Near-1 GiB import and large export.
- [ ] Large table with many rows and indexes.
- [ ] Concurrent backup/import/restore jobs.
- [ ] Measure duration, memory, disk, and UI responsiveness.
- [ ] Verify no OOM, timeout, orphan temp file, or stuck job.
- [ ] Record thresholds and any performance regressions.

### E2E-25 - Cross-platform parity

**Priority:** P1. **Environments:** WIN, LIN-DEB, LIN-UBU, LIN-FED.

- [ ] Run the same core scenarios on Windows/WSL and Linux helper.
- [ ] Compare normalized JSON: service/database/user/site/backup/job shapes.
- [ ] Compare env injection, secret storage, backup tree, file modes, audit rows.
- [ ] Verify Windows paths never leak into Linux results and vice versa.
- [ ] Verify WSL agent and Linux helper RPC parity for every database operation.
- [ ] Verify CRLF/LF, symlinks, permissions, and `0600` secret files.
- [ ] Record distro-specific failures as residual risks.

### E2E-26 - Regression and adjacent features

**Priority:** P1. **Environments:** WIN, LIN, UI.

- [ ] Static, PHP, and Node site create/start/stop/delete.
- [ ] Terminal and exec env with database links.
- [ ] PHP config/FPM, Node runtime, Composer/tooling.
- [ ] Hosts/trust repair, serving modes (builtin/nginx/apache), proxy/TLS.
- [ ] Service install/activate/deactivate/uninstall.
- [ ] Existing Playwright suite (`ui/tests/e2e.spec.ts`).
- [ ] `test/e2e/linux.sh` and `test/acceptance/m1.sh`.
- [ ] Ensure database-management changes did not regress existing flows.

### E2E-27 - Audit, SSE, and redaction

**Priority:** P1. **Environments:** WIN, LIN, UI.

- [ ] Every mutation writes the expected audit action/result.
- [ ] Every documented SSE event is emitted once with the expected name.
- [ ] Logs, job messages, backup errors, and SSE payloads are redacted.
- [ ] `state.db` contains no passwords or DSN userinfo.
- [ ] Evidence files pass the secret scanner.
- [ ] Audit retention/pruning behaves as designed.

### E2E-28 - Cleanup, exit criteria, and sign-off

**Priority:** P0. **Environments:** all.

- [ ] Remove all `e2e_` databases/users/sites/links/backups/containers/temp files.
- [ ] Restore **WIN-REAL** from the baseline if any scenario changed it.
- [ ] Run the secret scanner on all evidence.
- [ ] Complete `results.csv`; every scenario has PASS/FAIL/SKIP/BLOCKED and evidence.
- [ ] Write `residual-risks.md` with owner, impact, and mitigation.
- [ ] Record final commits, binary hashes, and environment versions.
- [ ] Sign off only if all P0 pass and all P1 pass or are accepted.

## Coverage matrix

| Feature | WIN | LIN-DEB | LIN-UBU | UI-MOCK | UI-LIVE |
|---|---|---|---|---|---|
| Install/fresh | E2E-13 | E2E-13 | E2E-13 | - | - |
| Upgrade/rollback | E2E-14 | E2E-14 | E2E-14 | - | - |
| Service lifecycle | E2E-01/15 | E2E-01/15 | E2E-01/15 | E2E-10 | E2E-10 |
| DB CRUD | E2E-02/16 | E2E-02/16 | E2E-02/16 | E2E-10/21 | E2E-10 |
| Users/grants/passwords | E2E-02/16 | E2E-02/16 | E2E-02/16 | E2E-10/21 | E2E-10 |
| Add Site | E2E-03/04/17 | E2E-03/04/17 | E2E-03/04/17 | E2E-10/21 | E2E-10 |
| Site deletion | E2E-05 | E2E-05 | E2E-05 | E2E-10 | E2E-10 |
| Wrong engine | E2E-06 | E2E-06 | E2E-06 | E2E-10 | E2E-10 |
| Backup/restore | E2E-07/18 | E2E-07/18 | E2E-07/18 | E2E-10/21 | E2E-10 |
| Export/import | E2E-07/18 | E2E-07/18 | E2E-07/18 | E2E-10/21 | E2E-10 |
| Redis users/ACL | E2E-08/19 | E2E-08/19 | E2E-08/19 | E2E-10/21 | E2E-10 |
| phpMyAdmin | E2E-20 | E2E-20 | E2E-20 | - | E2E-20 |
| CLI | E2E-09/22 | E2E-09/22 | E2E-09/22 | - | - |
| API/security | E2E-12/22 | E2E-12/22 | E2E-12/22 | E2E-21 | E2E-21 |
| Failure recovery | E2E-23 | E2E-23 | E2E-23 | E2E-21 | E2E-21 |
| Scale | E2E-24 | E2E-24 | E2E-24 | E2E-24 | E2E-24 |
| Parity | E2E-25 | E2E-25 | E2E-25 | - | - |
| Regression | E2E-26 | E2E-26 | E2E-26 | E2E-26 | E2E-26 |
| Audit/redaction | E2E-27 | E2E-27 | E2E-27 | E2E-27 | E2E-27 |
| Cleanup/sign-off | E2E-28 | E2E-28 | E2E-28 | E2E-28 | E2E-28 |

## Exit criteria

The run is complete only when:

1. Every **P0** row in `results.csv` is `PASS`.
2. Every **P1** row is `PASS` or has an accepted residual risk with an owner and mitigation.
3. Every destructive scenario has a verified recovery path.
4. The secret scanner finds zero unredacted secrets.
5. Windows/WSL and Linux parity comparisons are recorded; unexplained differences are residual risks.
6. The evidence index, command transcript, binary hashes, and final SHA are committed.
7. `residual-risks.md` is complete and reviewed.

## Known limitations and expected non-goals

These are documented v1 limitations. They must be tested for the documented failure, not treated as E2E failures:

- External-user password entry is deferred; metadata-only links work; `--database-user-password-stdin` fails with `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED`.
- `db user password --set` is deferred and fails with `DATABASE_PASSWORD_SET_UNSUPPORTED`.
- MySQL and MariaDB have no safe native rename; use backup + create + import.
- Redis has no logical databases, backups, restore, import, or export.
- Backup delete lacks a client-supplied typed confirmation token.
- Windows terminal environment can be stale after linking until the terminal is re-prepared.
- No overlapping-response test exists yet for the dashboard `loadServices` generation guard; E2E-21 adds it.
- Existing broken phpMyAdmin configs are user-owned and are not auto-overwritten; E2E-20 verifies the documented `service config apply` repair path.
- A daemon restart with no sites can clear the managed hosts block; E2E-01/15 records the behavior and the `doctor --repair` recovery.
- No finite test suite proves 100% correctness; the exit criteria above define the measurable bar.

## Not started

This document is a plan. No live E2E command, container change, site mutation, backup, restore, import, Redis restart, phpMyAdmin login test, or Playwright run has been executed as part of updating it.
