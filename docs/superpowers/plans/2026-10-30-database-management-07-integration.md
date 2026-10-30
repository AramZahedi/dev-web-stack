# Database Management Integration Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for the controller, plus fresh `superpowers:requesting-code-review` audits. Steps use checkbox (`- [ ]`) syntax for tracking. This plan runs only after all six lane reports are clean.

**Goal:** Merge the six database-management lanes into `master`, prove cross-lane contracts, run live Windows/WSL and Linux helper E2E, fix every Critical/Important audit finding, and record the evidence.

**Architecture:** The controller merges A → H → B → C → D → E, running focused gates after each merge and the full gate after all six. Integration tests exercise the real daemon/agent/helper boundaries, then live E2E proves the user flows. A final fresh audit reviews the merged tree; findings go back to the owning lane or the controller and the audit repeats.

**Tech Stack:** Go, SQLite, Svelte/Vitest/Playwright, PowerShell, WSL/Linux helper, Docker Compose, `devstack` CLI/API.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Master:** `docs/superpowers/plans/2026-10-30-database-management-master.md`

**Preconditions:**
- `master` contains the frozen foundation SHA from `.superpowers/db-foundation-sha.txt`.
- Six lane worktrees exist; each has a `.superpowers/lane-report.md` with a clean final audit.
- The main checkout is clean and on `master`.

## Global Constraints

- Do not merge a lane with an open Critical/Important audit finding.
- Do not resolve a conflict by deleting a test, weakening a security check, or dropping a lane's owned file.
- Do not run live E2E against a stale binary; build from the merged tree.
- Do not mutate the real install without a baseline backup and a cleanup plan.
- Live Windows/WSL and Linux helper tests are separate; do not claim Linux parity from Windows tests alone.
- Passwords must never appear in command output, logs, evidence files, or the terminal transcript used for evidence.

## Review Focus

- A site deleted while linked leaves the database, user, grants, password, backups, and data untouched.
- A metadata-only external user link omits `DB_PASSWORD` and `DATABASE_URL`.
- An existing user without access is warned, never auto-granted.
- A MySQL-linked site cannot start while MariaDB is active on 3306.
- A failed overwrite restore/replace-import leaves a flagged target and a usable pre-operation backup.

---

### Task 1: Merge the agent and helper lanes

- [ ] **Step 1: Verify the lane reports and clean trees**

```powershell
$base = Get-Content .superpowers\db-foundation-sha.txt
foreach ($lane in @('db-agent','db-helper','db-daemon','db-ui-service','db-ui-site','db-cli')) {
  $wt = "..\devstack-lanes\$lane"
  git -C $wt status --short
  Get-Content "$wt\.superpowers\lane-report.md"
}
```

Expected: every lane report has a clean final audit; every worktree is clean. If a lane is dirty or has an open finding, stop and send it back.

- [ ] **Step 2: Merge the agent lane**

```powershell
git status --short
git merge --no-ff lane/db-agent -m "merge: database agent adapters"
go test ./internal/dbengine ./internal/agentdocker ./internal/agentclient ./internal/agentproto ./internal/agent -count=1
go build ./...
go vet ./...
```

Expected: PASS.

- [ ] **Step 3: Merge the helper lane**

```powershell
git merge --no-ff lane/db-helper -m "merge: database Linux helper adapters"
go test ./internal/helper ./internal/helperclient ./internal/helperproto ./internal/dbengine -count=1
go build ./...
go vet ./...
```

Run the helper tests in WSL/Linux if the Windows host skips `!windows` files. Expected: PASS.

- [ ] **Step 4: Record the merge SHAs**

```powershell
git log --oneline -5 | Set-Content .superpowers\db-merge-agent-helper.txt
```

---

### Task 2: Merge the daemon lane and prove cross-lane contracts

- [ ] **Step 1: Merge the daemon lane**

```powershell
git merge --no-ff lane/db-daemon -m "merge: database daemon API and site links"
go test ./internal/store ./internal/databases ./internal/servicecontrol ./internal/api ./internal/sites ./internal/tooling ./internal/terminal ./internal/phpfpm ./internal/daemon -count=1
go build ./...
go vet ./...
```

Expected: PASS. If the agent/helper client interfaces do not line up, fix the adapter signatures in the daemon lane, not the lane contracts.

- [ ] **Step 2: Run the cross-lane contract tests**

```powershell
go test ./internal/databases ./internal/servicecontrol -run 'TestAgentDocker|TestHelperDocker|TestManager' -count=1
go test ./internal/api -run 'TestDatabase|TestOpenAPI|TestEveryAPIRouteRequiresTheInstallToken' -count=1
go test ./internal/sites -run 'TestCreate|TestDelete|TestStart|TestDatabase' -count=1
```

Expected: PASS. These prove the daemon maps `dbproto` correctly, the API is documented and token-protected, and site links/env behave.

- [ ] **Step 3: Verify the platform parity table**

Write `.superpowers/db-platform-parity.md` with one row per operation (list/create/drop/rename/users/grants/password/backup/restore/import/export/Redis) and columns Windows agent / Linux helper. Every row must be `implemented + tested` or an explicit design exception (MySQL/MariaDB rename, Redis databases/backups). Any unexplained gap blocks the merge.

---

### Task 3: Merge the dashboard lanes

- [ ] **Step 1: Merge the service dashboard lane**

```powershell
git merge --no-ff lane/db-ui-service -m "merge: database service dashboard"
cd ui
npm run check
npx vitest run src/routes/ServiceDetail.test.ts src/lib/components/service/DatabaseTab.test.ts src/lib/components/service/UsersTab.test.ts src/lib/components/service/BackupsTab.test.ts src/lib/components/service/RedisTab.test.ts
npm test -- --run
npm run build
cd ..
```

Expected: PASS, zero Svelte check errors.

- [ ] **Step 2: Merge the site dashboard lane**

```powershell
git merge --no-ff lane/db-ui-site -m "merge: database site dashboard"
cd ui
npm run check
npx vitest run src/routes/AddSite.test.ts src/routes/SiteDetail.test.ts
npm test -- --run
npm run build
cd ..
```

Expected: PASS. Pay special attention to the Add Site review not containing a password and the Site Detail delete copy saying databases remain.

- [ ] **Step 3: Run the UI contract checks**

```powershell
cd ui
npx vitest run src/lib/databaseApi.test.ts src/lib/components/SecretField.test.ts src/lib/components/DatabaseConnectionCard.test.ts
cd ..
```

Expected: PASS.

---

### Task 4: Merge the CLI lane

- [ ] **Step 1: Merge the CLI lane**

```powershell
git merge --no-ff lane/db-cli -m "merge: database CLI"
go test ./internal/cli ./internal/client -count=1
go test ./... -count=1
go vet ./...
go build ./...
```

Expected: PASS.

- [ ] **Step 2: Run the CLI contract checks**

```powershell
go test ./internal/cli -run 'TestDB|TestSite.*Database|TestSiteDB' -count=1
go test ./internal/client -run 'TestClientDatabase' -count=1
```

Expected: PASS. Confirm no list command prints a password and `db rename --service mysql` fails before the API call.

---

### Task 5: Full merged-tree gate

- [ ] **Step 1: Run the full Go and UI gates**

```powershell
go test ./... -race -count=1
go vet ./...
go build ./...
cd ui
npm ci
npm run check
npm test -- --run
npm run build
cd ..
git status --short
```

Expected: all pass; clean tree.

- [ ] **Step 2: Run the OpenAPI and security tests**

```powershell
go test ./internal/api -run 'TestOpenAPIDocumentParsesAndDocumentsRoutes|TestEveryAPIRouteRequiresTheInstallToken' -count=1
```

Expected: PASS.

- [ ] **Step 3: Build the deployable binaries**

```powershell
go build -o dist\e2e\database-management\devstack.exe .\cmd\devstack
go build -o dist\e2e\database-management\devstack-agent.exe .\cmd\devstack-agent
```

For Linux, build in WSL/Linux:

```bash
go build -o dist/e2e/database-management/devstack ./cmd/devstack
go build -o dist/e2e/database-management/devstack-helper ./cmd/devstack-helper
```

Expected: binaries exist. Do not use a stale installed binary.

---

### Task 6: Live Windows/WSL E2E

Use the real install at `C:\Users\Aram\.devstack` and the merged binaries. Back up `state.db`, `config.yaml`, and the hosts block before starting. Record evidence under `dist/e2e/database-management/windows/`.

- [ ] **Step 1: Install/activate the SQL services**

```powershell
$ds = 'dist\e2e\database-management\devstack.exe'
& $ds --home C:\Users\Aram\.devstack service install mysql
& $ds --home C:\Users\Aram\.devstack service activate mysql
& $ds --home C:\Users\Aram\.devstack service install postgresql
& $ds --home C:\Users\Aram\.devstack service activate postgresql
& $ds --home C:\Users\Aram\.devstack service install redis
& $ds --home C:\Users\Aram\.devstack service activate redis
& $ds --home C:\Users\Aram\.devstack doctor
```

Expected: services active; doctor green.

- [ ] **Step 2: Create databases/users/grants via CLI**

```powershell
& $ds --home C:\Users\Aram\.devstack db create --service mysql --name e2e_app
& $ds --home C:\Users\Aram\.devstack db user create --service mysql --name e2e_user --grant e2e_app:read_write_schema
& $ds --home C:\Users\Aram\.devstack db list --service mysql --json
& $ds --home C:\Users\Aram\.devstack db user list --service mysql --json
```

Expected: `e2e_app` and `e2e_user` exist; JSON contains no password. Reveal explicitly and confirm a warning:

```powershell
& $ds --home C:\Users\Aram\.devstack db user password --service mysql --user-id <id> --reveal
```

- [ ] **Step 3: Create a site with a new database/user**

```powershell
& $ds --home C:\Users\Aram\.devstack site add --name e2e-db --path C:\Users\Aram\.devstack\projects\e2e-db --kind static --database-service mysql --database-new e2e_site --database-user-new e2e_site_user --database-grant read_write_schema
& $ds --home C:\Users\Aram\.devstack exec --site e2e-db -- sh -c 'printf "%s|%s|%s|%s" "$DB_HOST" "$DB_PORT" "$DB_DATABASE" "$DB_USERNAME"'
```

Expected: site created; env prints `127.0.0.1|3306|e2e_site|e2e_site_user`. Verify `DB_PASSWORD` is set only for the managed user; do not print it in evidence.

- [ ] **Step 4: Prove site deletion leaves the database**

```powershell
& $ds --home C:\Users\Aram\.devstack site remove e2e-db --yes
& $ds --home C:\Users\Aram\.devstack db list --service mysql --json
```

Expected: site gone; `e2e_site` still listed; user and grants still exist.

- [ ] **Step 5: Prove wrong-engine start blocking**

```powershell
& $ds --home C:\Users\Aram\.devstack service deactivate mysql
& $ds --home C:\Users\Aram\.devstack service install mariadb
& $ds --home C:\Users\Aram\.devstack service activate mariadb
# Create a site linked to mysql, then try to start it while mariadb is active.
& $ds --home C:\Users\Aram\.devstack site add --name e2e-conflict --path C:\Users\Aram\.devstack\projects\e2e-conflict --kind static --database-service mysql --database e2e_site --database-user <id>
& $ds --home C:\Users\Aram\.devstack site start e2e-conflict
```

Expected: start is blocked with a clear “MySQL linked but MariaDB is active on 3306” error. Deactivate MariaDB and reactivate MySQL afterward.

- [ ] **Step 6: Backup/restore/import/export**

```powershell
& $ds --home C:\Users\Aram\.devstack db backup create --service mysql --database e2e_site
& $ds --home C:\Users\Aram\.devstack db backup list --service mysql --json
& $ds --home C:\Users\Aram\.devstack db export --service mysql --database e2e_site --output dist\e2e\database-management\windows\e2e_site.sql.gz
& $ds --home C:\Users\Aram\.devstack db backup restore --service mysql --backup-id <id> --target e2e_site_restore
& $ds --home C:\Users\Aram\.devstack db list --service mysql --json
```

Expected: backup exists; export is non-empty; restore creates `e2e_site_restore`; original unchanged. Test overwrite with a wrong typed confirmation first, then the correct one, and confirm the pre-restore backup exists.

- [ ] **Step 7: Redis ACL/password**

```powershell
& $ds --home C:\Users\Aram\.devstack db user create --service redis --name cache_user --access-level read-only
& $ds --home C:\Users\Aram\.devstack db user list --service redis --json
& $ds --home C:\Users\Aram\.devstack db user password --service redis --user-id <id> --reveal
```

Expected: user exists; `full` (if tested) does not grant `CONFIG`/`FLUSHALL`; no database/backup commands work for Redis.

- [ ] **Step 8: Dashboard E2E**

Start the dashboard with the merged binary and use Playwright:

- `/services/mysql` shows Databases/Users/Backups; create a database, create a user, reveal a password, run a backup, download it.
- `/services/redis` shows Users/Password only.
- Add Site step 3 shows the database panel; review has no password; after create, the reveal works.
- Site Detail shows the Databases card; reveal is masked until clicked; delete copy says databases remain.
- Zero console errors.

Record screenshots, page snapshots, and command output under `dist/e2e/database-management/windows/`.

---

### Task 7: Live Linux helper E2E

Use a disposable Linux environment (a WSL distro or Linux host) with the merged `devstack` and `devstack-helper` binaries. Do not touch the Windows install.

- [ ] **Step 1: Build and start the helper**

```bash
go build -o /tmp/devstack ./cmd/devstack
go build -o /tmp/devstack-helper ./cmd/devstack-helper
sudo /tmp/devstack-helper --help
```

Expected: helper starts and exposes the existing service RPC plus the new database routes.

- [ ] **Step 2: Run the same create/link/env/backup path**

Use a disposable home and a test project:

```bash
/tmp/devstack --home /tmp/devstack-e2e init --yes
/tmp/devstack --home /tmp/devstack-e2e service install mysql
/tmp/devstack --home /tmp/devstack-e2e service activate mysql
/tmp/devstack --home /tmp/devstack-e2e db create --service mysql --name e2e_app
/tmp/devstack --home /tmp/devstack-e2e db user create --service mysql --name e2e_user --grant e2e_app:read_write_schema
/tmp/devstack --home /tmp/devstack-e2e site add --name e2e-db --path /tmp/e2e-db --kind static --database-service mysql --database-new e2e_site --database-user-new e2e_site_user
/tmp/devstack --home /tmp/devstack-e2e exec --site e2e-db -- sh -c 'printf "%s|%s" "$DB_HOST" "$DB_DATABASE"'
```

Expected: helper path produces the same results as Windows, including secret storage and env injection. Run backup/restore/export and the site-delete-leaves-database check.

- [ ] **Step 3: Record Linux evidence**

Store command output and `go test ./internal/helper ./internal/helperclient -count=1` under `dist/e2e/database-management/linux/`.

---

### Task 8: Final audit and fix cycle

- [ ] **Step 1: Dispatch a fresh whole-branch audit**

Use `superpowers:requesting-code-review` with the spec, master plan, all lane plans, and `git diff $base..HEAD`. Two verdicts: spec compliance and code quality. Critical/Important/Minor findings.

- [ ] **Step 2: Fix and re-audit**

Fix every Critical/Important finding with a failing test first. If the finding belongs to a lane, use the same implementer session or a fresh fixer in the main checkout; do not reopen a lane worktree unless the fix is isolated. Commit. Re-dispatch a fresh reviewer. Repeat until no Critical/Important findings.

- [ ] **Step 3: Run the full gate again**

```powershell
go test ./... -race -count=1
go vet ./...
go build ./...
cd ui; npm run check; npm test -- --run; npm run build; cd ..
git status --short
```

Expected: all pass; clean tree.

---

### Task 9: Documentation, evidence, and closeout

- [ ] **Step 1: Update documentation**

- `docs/services.md`: database/user/backup/Redis sections and the service detail route.
- `docs/cli.md` (or the CLI reference): `devstack db`, `site db`, and `site add` database flags.
- `docs/known-issues.md`: any residual Minor finding or platform limitation.
- `docs/superpowers/audits/2026-10-30-database-management.md`: implementation, audit/fix rounds, E2E evidence, and residual risks.

- [ ] **Step 2: Record the final merge and evidence**

```powershell
git log --oneline -12 | Set-Content dist\e2e\database-management\final-commits.txt
git status --short
git rev-parse HEAD
```

Expected: clean tree; final SHA recorded. Do not push unless the user asks.

- [ ] **Step 3: Closeout report**

Report to the user:

- merged lane SHAs and final SHA;
- test counts and live E2E results for Windows/WSL and Linux;
- audit verdicts and residual Minor findings;
- evidence paths;
- any platform limitation (MySQL/MariaDB rename, Redis no backups/logical DBs).
