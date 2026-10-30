# Database management - full E2E plan

- **Status:** planned only. No live E2E has been started.
- **Implementation:** merged to `master` at `8411b56`; the merge tree is identical to the gated `integration/database-management` tree (`6c4e19e488baa5db883eb55f64de4f1fab79138e`).
- **Scope:** Windows daemon + WSL agent, Linux helper, MySQL, MariaDB, PostgreSQL, Redis, CLI, HTTP API, and the Svelte dashboard.
- **Rule:** every run uses binaries built from the final `master` SHA. Do not use the installed v0.3.1 binary or a pre-merge build.

## Why this plan exists

The implementation gates prove the code compiles, the unit/integration suites pass, and the race detector is clean. They do not prove the user-facing flows against real engines and real filesystems. This plan closes that gap with disposable environments, explicit evidence, and a pass/fail record for each scenario.

The plan intentionally separates:

1. **Windows/WSL E2E** - the real daemon and agent path on this machine.
2. **Linux helper E2E** - a disposable Linux environment, because Windows tests cannot prove Linux helper parity.
3. **Dashboard E2E** - deterministic mock-mode Playwright plus a live dashboard smoke run.

## Preconditions

- [ ] `git status --short` is clean and `git rev-parse HEAD` matches the recorded final SHA.
- [ ] Build outputs exist or are rebuilt from that SHA:
  - `dist/e2e/database-management/windows/devstack.exe`
  - `dist/e2e/database-management/windows/devstack-helper.exe`
  - `dist/e2e/database-management/windows/devstack-agent.exe`
  - `dist/e2e/database-management/linux/devstack`
  - `dist/e2e/database-management/linux/devstack-helper`
  - `dist/e2e/database-management/linux/devstack-agent`
- [ ] Docker is running and the four database service images can be pulled.
- [ ] A disposable WSL distro or Linux host is available for the helper run.
- [ ] The real install is backed up before any Windows scenario:
  - `state.db`
  - `config.yaml`
  - the hosts-file block managed by devstack
  - the current `db list` / `db user list` output for every service
- [ ] Evidence directory exists: `dist/e2e/database-management/`.
- [ ] No secret is written to evidence. Passwords are only checked for presence/absence and are redacted from transcripts.

## Environment matrix

| Environment | Daemon | Privileged path | Services | Purpose |
|---|---|---|---|---|
| Windows + WSL | fresh `devstack.exe` | WSL agent | MySQL, MariaDB, PostgreSQL, Redis | real Windows user flow |
| Linux (disposable WSL distro or host) | fresh Linux `devstack` | Linux `devstack-helper` | MySQL, PostgreSQL, Redis | helper parity |
| Dashboard mock | Vite dev server | in-page mock API | mocked | deterministic UI regression |
| Dashboard live | fresh daemon | same as Windows/Linux | real | end-to-end dashboard smoke |

## Evidence layout

```text
dist/e2e/database-management/
  windows/
    commands.txt
    db-list-before.json
    db-list-after.json
    site-env.txt
    backup-list.json
    screenshots/
    playwright-report/
  linux/
    commands.txt
    helper.log
    secret-permissions.txt
    backup-tree.txt
  dashboard/
    mock-playwright-report/
    live-screenshots/
  final/
    commits.txt
    gate-summary.txt
    residual-risks.md
```

## Safety rules

- Never print a password, `DATABASE_URL`, or a secret file body in command output or evidence.
- Use `--json` for machine-readable assertions, but redact `password`, `secret`, and URL userinfo fields before saving.
- Take a baseline backup before destructive scenarios.
- Use disposable databases/users/sites with the `e2e_` prefix.
- Stop linked sites before overwrite restore or replace import, as the UI requires.
- Do not uninstall a service with `--delete-data` until the keep-data scenario has passed and the baseline backup is verified.
- Cleanup removes only `e2e_` objects created by this run.

## Scenario E2E-00 - Preflight and baseline

- [ ] Record the final SHA, branch, and binary hashes:

```powershell
git rev-parse HEAD
Get-FileHash dist\e2e\database-management\windows\devstack.exe -Algorithm SHA256
Get-FileHash dist\e2e\database-management\linux\devstack -Algorithm SHA256
```

- [ ] Back up the real install and record the baseline:

```powershell
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
Copy-Item C:\Users\Aram\.devstack\state.db "dist\e2e\database-management\windows\state-$stamp.db"
Copy-Item C:\Users\Aram\.devstack\config.yaml "dist\e2e\database-management\windows\config-$stamp.yaml"
& $ds --home C:\Users\Aram\.devstack db list --service mysql --json > dist\e2e\database-management\windows\db-list-before.json
```

- [ ] Confirm the installed binary version is not the one under test:

```powershell
& $ds --version
```

Expected: the run uses the freshly built binary path, and the evidence records both versions.

## Scenario E2E-01 - Windows service lifecycle

- [ ] Install and activate the SQL services with the merged binary:

```powershell
& $ds --home C:\Users\Aram\.devstack service install mysql
& $ds --home C:\Users\Aram\.devstack service activate mysql
& $ds --home C:\Users\Aram\.devstack service install postgresql
& $ds --home C:\Users\Aram\.devstack service activate postgresql
& $ds --home C:\Users\Aram\.devstack service install redis
& $ds --home C:\Users\Aram\.devstack service activate redis
& $ds --home C:\Users\Aram\.devstack doctor
```

- [ ] Record `service list --json` and confirm MySQL is on 3306, PostgreSQL on 5432, Redis on 6379.
- [ ] Confirm the dashboard opens at the configured UI port and the API health endpoint is reachable.

Expected: all three services are active and healthy; doctor reports no database-management failures.

## Scenario E2E-02 - Service-wide database and user management

Run on MySQL and PostgreSQL. Repeat the read-only checks on MariaDB after E2E-05.

- [ ] Create databases:

```powershell
& $ds --home C:\Users\Aram\.devstack db create --service mysql --name e2e_app
& $ds --home C:\Users\Aram\.devstack db create --service postgresql --name e2e_app
& $ds --home C:\Users\Aram\.devstack db list --service mysql --json
& $ds --home C:\Users\Aram\.devstack db list --service postgresql --json
```

- [ ] Create users with grants:

```powershell
& $ds --home C:\Users\Aram\.devstack db user create --service mysql --name e2e_user --grant e2e_app:read_write_schema
& $ds --home C:\Users\Aram\.devstack db user create --service postgresql --name e2e_user --grant e2e_app:read_write_schema
& $ds --home C:\Users\Aram\.devstack db user list --service mysql --json
& $ds --home C:\Users\Aram\.devstack db user list --service postgresql --json
```

- [ ] Assert list JSON contains no `password`, `secret`, or URL userinfo.
- [ ] Reveal once, confirm the warning, and verify the audit row:

```powershell
& $ds --home C:\Users\Aram\.devstack db user password --service mysql --user-id <id> --reveal
# There is no audit CLI/API in v1; inspect the read-only audit table directly.
sqlite3 C:\Users\Aram\.devstack\state.db "select action, result from audit_events order by id desc limit 10;"
```

- [ ] Rotate the password, verify the old password no longer works and the new secret is returned only once.
- [ ] Add and remove a grant; verify the engine grant set and the metadata agree.
- [ ] Protected objects:
  - drop `mysql` -> `DATABASE_PROTECTED` / HTTP 409
  - delete `root` -> protected
  - delete a user linked to a site -> `DATABASE_CONFLICT` with the linked-site list
- [ ] Rename:
  - MySQL `db rename` -> `DATABASE_UNSUPPORTED` before the API call
  - PostgreSQL `db rename` -> succeeds; links/grants references update
- [ ] Drop with a pre-drop backup:

```powershell
& $ds --home C:\Users\Aram\.devstack db drop --service mysql --name e2e_app --backup-first --yes
```

Expected: `e2e_app` is gone, the pre-drop backup exists, and no other database/user/backup is removed.

## Scenario E2E-03 - Add Site with a new database and new user

- [ ] Create a minimal static project at `C:\Users\Aram\.devstack\projects\e2e-db`.
- [ ] Add the site with a new database and new user:

```powershell
& $ds --home C:\Users\Aram\.devstack site add `
  --name e2e-db --path C:\Users\Aram\.devstack\projects\e2e-db --kind static `
  --database-service mysql --database-new e2e_site `
  --database-user-new e2e_site_user --database-grant read_write_schema
```

- [ ] Verify the review step:
  - database, user, grant, host, port are shown
  - no password is shown
  - copy says the password is generated on submit
- [ ] Verify runtime env without printing the secret:

```powershell
& $ds --home C:\Users\Aram\.devstack exec --site e2e-db -- sh -c 'printf "%s|%s|%s|%s" "$DB_CONNECTION" "$DB_HOST" "$DB_PORT" "$DB_DATABASE"'
```

Expected: `mysql|127.0.0.1|3306|e2e_site` (or the engine-specific connection value). `DB_PASSWORD` is present but is not printed.
- [ ] Verify `state.db` and the persisted site env do not contain the password.
- [ ] Verify Site Detail masks the password until the explicit reveal action, and that reveal/copy creates an audit row.

## Scenario E2E-04 - Existing user warning and metadata-only link

- [ ] Create a second user with no grant on `e2e_site`.
- [ ] Link it from Add Site or Site Detail and confirm:
  - the UI warns that the user has no access
  - no grant is created automatically
  - the **Manage database user** action opens the user editor
- [ ] Create a metadata-only external user link (no password):
  - `DB_PASSWORD` and `DATABASE_URL` are absent from the site env
  - Site Detail shows "Password not managed"
  - the **Set password** action is present but the v1 limitation is documented
- [ ] Confirm `site add --database-user-password-stdin` fails with `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED` without reading stdin or calling the API.

## Scenario E2E-05 - Site deletion preserves service-wide data

- [ ] Seed a marker table/row in `e2e_site`.
- [ ] Remove the site:

```powershell
& $ds --home C:\Users\Aram\.devstack site remove e2e-db --yes
```

- [ ] Verify:
  - the site and its links are gone
  - `e2e_site` still exists
  - `e2e_site_user` still exists with the same grants
  - the password still works
  - the backup metadata and backup file still exist
  - the marker row still exists
- [ ] Recreate a site and link the same database/user; the marker row is still readable.
- [ ] Uninstall with keep-data and verify the database/user/backup survive.
- [ ] Only after that, uninstall with delete-data and verify:
  - the data deletion runs first
  - links are removed only after deletion succeeds
  - warnings name the affected sites and backups

## Scenario E2E-06 - Wrong-engine start blocking

- [ ] Keep an `e2e_site` link to MySQL.
- [ ] Deactivate MySQL, install/activate MariaDB on 3306.
- [ ] Try to start the MySQL-linked site:

```powershell
& $ds --home C:\Users\Aram\.devstack site start e2e-db
```

Expected: start is blocked with a clear "MySQL linked but MariaDB is active on 3306" error. No container is started for the site.
- [ ] Deactivate MariaDB, reactivate MySQL, and start the site successfully.
- [ ] Repeat the guard check from the dashboard: the site start action shows the same explanation.

## Scenario E2E-07 - Backup, restore, export, import

- [ ] Seed known rows in `e2e_app` / `e2e_site`.
- [ ] Create a backup and record its metadata:

```powershell
& $ds --home C:\Users\Aram\.devstack db backup create --service mysql --database e2e_app
& $ds --home C:\Users\Aram\.devstack db backup list --service mysql --json
```

- [ ] Download the backup and verify:
  - gzip opens
  - SHA-256 matches the metadata
  - size matches
- [ ] Restore to a new target:

```powershell
& $ds --home C:\Users\Aram\.devstack db backup restore --service mysql --backup-id <id> --target e2e_app_restore
```

Expected: `e2e_app_restore` exists with the marker rows; `e2e_app` is unchanged.
- [ ] Overwrite restore:
  - wrong confirmation -> rejected, no data change
  - correct confirmation -> pre-restore backup is created first
  - stop linked sites first and verify the UI/CLI refuses otherwise
- [ ] Export and import merge:

```powershell
& $ds --home C:\Users\Aram\.devstack db export --service mysql --database e2e_app --output dist\e2e\database-management\windows\e2e_app.sql.gz
& $ds --home C:\Users\Aram\.devstack db import --service mysql --database e2e_app_restore --file dist\e2e\database-management\windows\e2e_app.sql.gz
```

- [ ] Import replace:
  - requires `--yes`
  - creates a pre-import backup first
  - drops/recreates the target only after the backup succeeds
- [ ] Failure path:
  - import a deliberately invalid dump
  - job ends `failed`
  - target is flagged "restore incomplete"
  - the pre-operation backup is offered and restores successfully
- [ ] Cancellation path:
  - start a long import/backup
  - cancel it
  - job ends `canceled`; the target/backup state is recorded
- [ ] Stopped-service behavior:
  - stop MySQL
  - backup list/download/delete still work
  - backup create, restore, import, and export fail with the inactive-service error
  - restart MySQL

## Scenario E2E-08 - Redis users, password, and ACL persistence

- [ ] Open `/services/redis` and confirm only **Users** and **Password** tabs exist; no Databases or Backups tab.
- [ ] Create a `read-only` user and a `full` user:

```powershell
& $ds --home C:\Users\Aram\.devstack db user create --service redis --name e2e_cache_ro --access-level read-only
& $ds --home C:\Users\Aram\.devstack db user create --service redis --name e2e_cache_full --access-level full
```

- [ ] Reveal and rotate each password; verify list JSON never contains a password.
- [ ] First user/password mutation before ACL persistence:
  - API returns `CONFIRMATION_REQUIRED` / HTTP 409
  - no `redis.conf` change and no restart occurred
- [ ] Enable ACL persistence explicitly:

```powershell
# UI: Users tab -> Enable ACL persistence -> confirm
# API equivalent (the first "redis" is the service id; the second is the literal route segment).
# Add the install-token and CSRF headers used by the dashboard session.
curl.exe -X POST "http://127.0.0.1:<ui-port>/api/v1/services/redis/redis/acl-file" `
  -H "Content-Type: application/json" -H "X-Devstack-Token: <install-token>" `
  -d '{"confirm":true}'
```

- [ ] Verify:
  - `redis.conf` contains `aclfile`
  - `users.acl` exists and is seeded from the live ACL state
  - Redis restarted exactly once
  - the users and passwords survive the restart
  - retrying the mutation succeeds
- [ ] Verify `full` can run normal commands but cannot run `CONFIG` or `FLUSHALL`.
- [ ] Verify Redis database/backup/restore/import/export commands fail with `DATABASE_UNSUPPORTED`.
- [ ] Verify the default Redis password route works and is protected/audited.

## Scenario E2E-09 - CLI security and documented limitations

- [ ] `db list --json` and `db user list --json` contain no passwords for every engine.
- [ ] `db user password --reveal` prints the one-time warning and writes an audit row.
- [ ] `db user password --set` fails with `DATABASE_PASSWORD_SET_UNSUPPORTED` before any API call and never reads stdin.
- [ ] `site add --database-user-password-stdin` fails with `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED` before any API call.
- [ ] `db rename --service mysql` and `--service mariadb` fail before the API call with the backup/create/import remediation.
- [ ] No command places a password in argv; `ps`/process inspection and shell history show no secret.
- [ ] A failed command's stderr and JSON error never include a password or DSN userinfo.

## Scenario E2E-10 - Dashboard Playwright

### Mock-mode suite

- [ ] Add database-management scenarios to `ui/tests/e2e.spec.ts` (or a sibling spec):
  - `/services/mysql` shows Databases, Users, Backups.
  - create a database; create a user; reveal a password; confirm the masked state.
  - create a backup; observe the job toast; download it.
  - Add Site step 3 shows the database panel; review has no password.
  - Site Detail shows the database card; reveal is masked until clicked.
  - delete copy says databases remain.
  - `/services/redis` shows Users and Password only; ACL persistence confirm dialog appears.
- [ ] Run:

```powershell
cd ui
npm run test:e2e
```

Expected: all scenarios pass with zero browser console errors; the report is saved under `dist/e2e/database-management/dashboard/mock-playwright-report/`.

### Live dashboard smoke

- [ ] Start the daemon from the fresh binary with a disposable home and dashboard auth enabled.
- [ ] Open the dashboard with the install token in the URL fragment (`devstack ui` or `http://127.0.0.1:<ui-port>/#token=<token>`).
- [ ] Playwright checks:
  - service detail tabs load with real data
  - create database/user and reveal flows work
  - backup job progress and toast work
  - Add Site creates a site with a new database/user
  - Site Detail delete copy says databases remain
  - Redis ACL persistence confirmation works
  - zero console errors and no unhandled network failures
- [ ] Save screenshots and the Playwright report under `dist/e2e/database-management/dashboard/live-screenshots/`.

## Scenario E2E-11 - Linux helper parity

Use a disposable WSL distro or Linux host. Do not touch the Windows install.

- [ ] Build from the final SHA:

```bash
go build -o /tmp/devstack-e2e/devstack ./cmd/devstack
go build -o /tmp/devstack-e2e/devstack-helper ./cmd/devstack-helper
go build -o /tmp/devstack-e2e/devstack-agent ./cmd/devstack-agent
```

- [ ] Start the helper and initialize a disposable home:

```bash
sudo /tmp/devstack-e2e/devstack-helper --help
/tmp/devstack-e2e/devstack --home /tmp/devstack-e2e/home init --yes
```

- [ ] Install/activate MySQL, PostgreSQL, and Redis.
- [ ] Repeat:
  - database/user create, list, reveal, rotate, grants
  - Add Site with a new database/user
  - env injection (`DB_*`, `DATABASE_URL`)
  - site deletion leaves the database/user/grants/backups
  - backup/restore/export/import
  - Redis users/password and ACL persistence confirmation
- [ ] Verify helper-side invariants:
  - secret files are mode `0600` under the agent root
  - backup/import paths are under the agent backups tree
  - no symlink or traversal path is accepted
  - `state.db` contains no password
  - logs/SSE payloads contain no password
- [ ] Run the focused Linux race suite:

```bash
cd /path/to/repo
go test ./internal/helper ./internal/helperclient ./internal/agentdocker -race -count=1
```

Expected: identical user-facing behavior to Windows, with Linux-specific paths and helper RPCs.

## Scenario E2E-12 - Cross-cutting security checks

- [ ] API without the install token -> 401/403; mutation without CSRF -> rejected.
- [ ] Reveal without authorization -> denied and audited.
- [ ] Names containing `../`, absolute paths, quotes, semicolons, and wildcards are rejected or safely quoted.
- [ ] Import/backup path traversal is rejected; symlinks are rejected.
- [ ] Protected databases/users cannot be dropped/deleted or have admin grants reduced.
- [ ] A user linked to a site cannot be deleted until the link is removed.
- [ ] Wrong-engine start is blocked (repeat from the API and dashboard).
- [ ] Audit rows exist for create/drop/rename/reveal/rotate/grant/backup/restore/import/link/unlink.
- [ ] SSE events use the documented names and contain no secrets.

## Pass/fail criteria

A scenario passes only when:

- the command exits 0 and the expected state is observed;
- the negative case fails with the documented error code;
- no password appears in output, logs, `state.db`, or evidence;
- the dashboard has no console errors for the touched flow;
- the evidence file names the binary SHA and the environment.

The E2E run is complete when every scenario above is checked, every failure is either fixed test-first or recorded as a residual risk, and the evidence index is committed.

## Known limitations and expected non-goals

These are documented v1 limitations, not E2E failures:

- External-user password entry is deferred. Metadata-only links work; `--database-user-password-stdin` fails with `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED`.
- `db user password --set` is deferred and fails with `DATABASE_PASSWORD_SET_UNSUPPORTED`.
- MySQL and MariaDB have no safe native rename; use backup + create + import.
- Redis has no logical databases, backups, restore, import, or export.
- Backup delete lacks a client-supplied typed confirmation token.
- Windows terminal environment can be stale after linking until the terminal is re-prepared.
- No overlapping-response test exists for the dashboard's `loadServices` generation guard.

## Execution order

1. E2E-00 preflight and baseline.
2. E2E-01 Windows services.
3. E2E-02 SQL CRUD and grants.
4. E2E-03 / E2E-04 Add Site and existing-user flows.
5. E2E-05 site deletion and uninstall semantics.
6. E2E-06 wrong-engine blocking.
7. E2E-07 backup/restore/export/import.
8. E2E-08 Redis.
9. E2E-09 CLI security.
10. E2E-10 dashboard mock and live.
11. E2E-11 Linux helper.
12. E2E-12 cross-cutting security.
13. Cleanup, evidence index, and residual-risk report.

## Cleanup

- [ ] Remove only `e2e_` databases, users, sites, and backups created by the run.
- [ ] Stop and remove disposable Linux containers and the disposable home.
- [ ] Restore the real install only if a scenario changed it; otherwise keep the baseline backup as evidence.
- [ ] Delete temporary SQL dumps and staged import files.
- [ ] Confirm `git status --short` is clean and no evidence file contains a secret.

## Not started

This document is a plan. No live E2E command, container change, site mutation, backup, restore, import, Redis restart, or Playwright run has been executed as part of writing it.
