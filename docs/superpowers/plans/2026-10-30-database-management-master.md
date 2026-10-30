# Database Management Parallel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development for the controller and superpowers:executing-plans for each lane implementer. Steps use checkbox (`- [ ]`) syntax for tracking. The controller executes the foundation, dispatches the lanes, audits, and merges. Lane implementers never merge.

**Goal:** Ship service-wide database and user management, optional site database links in Add Site, connection details on Site Detail, SQL backup/restore/import/export, Redis ACL users/password management, and CLI parity.

**Architecture:** A frozen foundation defines the migration, domain contracts, shared engine SQL/validation/parsing, shared wire protocol, HTTP/OpenAPI contract, and shared UI primitives. Six lanes then work in isolated worktrees against those contracts: Windows/WSL agent adapters, Linux helper adapters, daemon/API/site integration, dashboard service management, dashboard site flows, and CLI. Each lane runs implementation → fresh audit → fix → re-audit until no Critical/Important findings. The controller merges lanes in dependency order and runs the full integration and live E2E gate on both platforms.

**Tech Stack:** Go daemon/helper/agent, SQLite migrations, Docker Compose services (MySQL 8.4, MariaDB 11.8, PostgreSQL 18, Redis 8.8), Svelte dashboard, Cobra CLI, PowerShell/Go/Playwright tests.

**Spec:** `docs/superpowers/specs/2026-10-30-database-management-design.md`

**Design commit:** `fd11af2` (`docs: design service-wide database management`)

## Why this is parallel

The feature has one hard serial dependency: the shared contract. Everything else divides cleanly by file ownership and test surface. Freezing the contract before branching prevents five agents from inventing five different request shapes, table names, and env-variable rules.

The lanes are:

| Lane | Branch | Worktree | Plan | Owns | Merge order |
|---|---|---|---|---|---|
| F | `master` | main checkout | this plan + `...-00-foundation.md` | migration, domain contracts, shared engine SQL/validation/parsing, shared wire protocol, OpenAPI, UI types/API/mock, shared UI primitives | first (frozen base) |
| A | `lane/db-agent` | `..\devstack-lanes\db-agent` | `...-01-agent-adapters.md` | Windows/WSL agent execution, agent HTTP routes, agent client, streaming backups, Redis ACL | 1 |
| H | `lane/db-helper` | `..\devstack-lanes\db-helper` | `...-02-helper-adapters.md` | Linux helper execution, helperproto, helperclient, helper routes | 2 |
| B | `lane/db-daemon` | `..\devstack-lanes\db-daemon` | `...-03-daemon-api-sites.md` | store methods, daemon database service, API routes, site links/env/Add Site orchestration, both platform adapters/wiring | 3 |
| C | `lane/db-ui-service` | `..\devstack-lanes\db-ui-service` | `...-04-dashboard-service.md` | service detail route, Databases/Users/Backups/Redis tabs, service components | 4 |
| D | `lane/db-ui-site` | `..\devstack-lanes\db-ui-site` | `...-05-dashboard-site.md` | Add Site database panel, review/success state, Site Detail databases card | 5 |
| E | `lane/db-cli` | `..\devstack-lanes\db-cli` | `...-06-cli.md` | client methods, `devstack db` group, `site add` database flags, `site db` commands | 6 |
| I | `master` | main checkout | `...-07-integration.md` | merges, cross-lane tests, live E2E on Windows/WSL and Linux, docs, final audit/fix | after A–E |

## Parallel execution protocol

### 1. Foundation gate (controller, main checkout)

Execute `...-00-foundation.md` in the main checkout. It is the only serial phase. It ends with:

```powershell
go test ./... -count=1
go vet ./...
go build ./...
cd ui; npm run check; npm test -- --run; cd ..
git status --short
```

All must pass and the tree must be clean. Commit the foundation, record the SHA, and freeze it:

```powershell
$base = git rev-parse HEAD
$base | Set-Content .superpowers\db-foundation-sha.txt
```

No lane starts before this commit. No lane may edit the foundation-owned files listed in `...-00-foundation.md`; if a contract change is unavoidable, the lane stops, records a Ruling with the cost of being wrong, and the controller applies the change to `master` and merges it into the affected lanes before work continues.

### 2. Create six worktrees from the frozen base

```powershell
git worktree add ..\devstack-lanes\db-agent        -b lane/db-agent        $base
git worktree add ..\devstack-lanes\db-helper       -b lane/db-helper       $base
git worktree add ..\devstack-lanes\db-daemon       -b lane/db-daemon       $base
git worktree add ..\devstack-lanes\db-ui-service   -b lane/db-ui-service   $base
git worktree add ..\devstack-lanes\db-ui-site      -b lane/db-ui-site      $base
git worktree add ..\devstack-lanes\db-cli          -b lane/db-cli          $base
git worktree list
```

Use one implementer subagent per lane, all started from the same `$base`. Pass the exact model `opencode-go/deepseek-v4.1-flash#max` to every subagent, per `AGENTS.md`.

### 3. Active-subagent cap

Hard cap: **8 active subagents at any time**, including implementers, auditors, and fixers.

- Six lane implementers may run concurrently.
- A lane auditor starts only after that lane's implementer finishes and the lane gate passes.
- An implementer fixing audit findings counts as active.
- Do not dispatch a seventh lane; there are only six.
- With six implementers running, at most two audits/fixers may also be active. Queue the rest.

### 4. Per-lane cycle (implementation → audit → fix → re-audit)

Each lane follows this exact cycle in its own worktree:

1. **Implement** the lane plan task-by-task with TDD. Commit per task. Do not merge.
2. **Lane gate:** run the lane's focused tests, `go test ./... -count=1` (or the UI equivalent), `go vet ./...`, and `git status --short`.
3. **Audit:** dispatch a fresh reviewer (new session, no implementation history) with the `superpowers:requesting-code-review` skill. The reviewer reads the spec, the lane plan, the foundation contract, and the diff `$base..HEAD`. It returns two verdicts: spec compliance and code quality, with Critical/Important/Minor findings. It runs the lane tests itself.
4. **Fix:** the same implementer fixes every Critical/Important finding with a failing test first. Re-run the lane gate. Commit the fixes.
5. **Re-audit:** a fresh reviewer checks the fix commit and the full lane diff. Repeat steps 4–5 until the audit has no Critical/Important findings.
6. **Lane report:** write `<worktree>\.superpowers\lane-report.md` with branch, base SHA, commits, test commands/results, audit verdicts, residual risks, and any Ruling/deviation. Commit the lane code (the report is gitignored).
7. **Stop.** Do not merge, rebase, or push. The controller merges.

Minor findings may be fixed or recorded in the lane report; they do not block the merge unless they are security or data-loss risks, which are promoted to Important.

### 5. Controller-only merge protocol

The controller merges in order A → H → B → C → D → E. Before each merge:

```powershell
git status --short          # must be clean
git log -1 --oneline        # must be the previous merge or foundation
```

Merge with a merge commit and run the lane gate immediately after:

```powershell
git merge --no-ff lane/db-agent -m "merge: database agent adapters"
go test ./internal/agentdocker ./internal/agentclient ./internal/dbproto ./internal/dbengine -count=1
```

Then H:

```powershell
git merge --no-ff lane/db-helper -m "merge: database Linux helper adapters"
go test ./internal/helper ./internal/helperclient ./internal/helperproto -count=1
```

Then B:

```powershell
git merge --no-ff lane/db-daemon -m "merge: database daemon API and site links"
go test ./internal/databases ./internal/store ./internal/api ./internal/sites ./internal/servicecontrol ./internal/daemon -count=1
```

Then C and D:

```powershell
git merge --no-ff lane/db-ui-service -m "merge: database service dashboard"
git merge --no-ff lane/db-ui-site -m "merge: database site dashboard"
cd ui; npm run check; npm test -- --run; cd ..
```

Then E:

```powershell
git merge --no-ff lane/db-cli -m "merge: database CLI"
go test ./internal/cli ./internal/client -count=1
```

After every merge, run `go build ./...` and `go vet ./...`. After C/D/E, also run `go test ./... -count=1` and `npm run build`.

Conflict rules:

- Foundation-owned files: controller resolves and records the resolution in the integration log.
- Lane-owned files: the lane owner is consulted; if the owner is gone, the controller resolves with a failing test first.
- A lane that edited another lane's files is rejected and sent back, even if the tests pass. File ownership is part of the contract.
- Never resolve a conflict by deleting a test or weakening a security check.

### 6. Integration and live E2E

After all six merges, execute `...-07-integration.md`. It covers:

- Full Go race suite, vet, build, UI check/test/build.
- Cross-lane contract tests (daemon ↔ agent fake, daemon ↔ helper fake, UI ↔ API mock, CLI ↔ API fake).
- Live Windows/WSL E2E on the real `devstack` install: install/activate each service, create databases/users, link a site, verify env, delete the site and prove the database remains, backup/restore/import/export, Redis ACL/password, wrong-engine start blocking, uninstall behavior.
- Live Linux helper E2E in WSL or a Linux checkout: the same create/link/env/backup/restore path through the helper.
- A final fresh audit of the merged branch; fix and re-audit until no Critical/Important findings.
- Documentation updates and evidence capture.

## Shared contract rules

The foundation freezes these and no lane edits them:

- `internal/store/migrations/0008_databases.sql`
- `internal/model/database.go`
- `internal/databases/types.go`
- `internal/databases/contracts.go`
- `internal/dbproto/types.go`
- `internal/dbproto/paths.go`
- `internal/dbengine/contracts.go` (engine interface, registry, shared validation/quoting)
- `internal/sites/service.go` (only the `CreateRequest.Database` field)
- `api/openapi.yaml` (database paths and schemas, plus the Add Site database field)
- `ui/src/lib/databaseTypes.ts`
- `ui/src/lib/databaseApi.ts`
- `ui/src/lib/databaseMock.ts`
- `ui/src/lib/components/SecretField.svelte`
- `ui/src/lib/components/DatabaseConnectionCard.svelte`

Use the existing `ConfirmDialog` with `requireText`; do not add a second typed-confirmation component.

Lane-owned files are listed in each lane plan. If a lane needs a new shared field or function, it must stop and request a foundation amendment. The controller adds the amendment to `master`, commits it, and merges `master` into the affected lane branches before they continue. This is the only allowed serialization point.

## Global constraints

- Databases are service-wide; site deletion removes only links.
- No SQL console, arbitrary SQL, or shell interpolation.
- MySQL/MariaDB are mutually exclusive on 3306; a site linked to one must not silently connect to the other.
- MySQL/MariaDB database rename is not supported; PostgreSQL rename only.
- Database-user secrets are platform-owned under `services/<service>/secrets/database-users/<user-id>`, never in SQLite.
- On Windows the agent resolves secrets and injects site env directly. On Linux the daemon may receive a secret transiently in memory to set the site process environment; it must never persist, log, or put it in `site.Env`, `php-fpm.conf`, or a diagnostic event.
- Passwords never appear in argv, logs, list responses, or diagnostic events.
- Destructive operations require typed confirmation and, where specified, a pre-operation backup.
- Linked sites must be stopped before destructive operations; sessions are terminated by the adapter.
- Redis `full` excludes admin/dangerous commands; Redis users are not site-linkable in v1.
- Backup list/download/delete work when the service is installed but stopped; backup/restore/import/export require active.
- Every mutation and reveal writes a durable `audit_events` row.
- The dashboard remains loopback-only, session/CSRF protected, and audited.
- All lane tests must be deterministic and must not mutate the real `C:\Users\Aram\.devstack` install. Live E2E is controller-only.

## Review focus

These are the failure modes most likely to bite a user and are not fully exercised by ordinary happy-path tests. Each lane plan pins the matching test in the owning task.

- A site deleted while linked leaves the database, user, grants, password, backups, and data untouched; only the link disappears.
- A metadata-only external user link omits `DB_PASSWORD` and `DATABASE_URL` and never invents a password.
- Selecting an existing user without access shows a warning and never auto-grants during site creation.
- A MySQL-linked site cannot start while MariaDB is active on 3306, and vice versa.
- A failed overwrite restore or replace-import leaves a clearly flagged target and a usable pre-operation backup, not a silent half-state.

## Definition of done

- Foundation and all six lane branches are merged into `master` in the stated order.
- Every lane has a lane report and a clean final audit (no Critical/Important).
- `go test ./... -race -count=1`, `go vet ./...`, `go build ./...`, UI `npm run check`, `npm test -- --run`, and `npm run build` pass on the merged tree.
- Live E2E evidence is recorded under `dist/e2e/database-management/` for both Windows/WSL and Linux.
- The final audit has no Critical/Important findings.
- Documentation is updated: `docs/services.md`, `docs/cli.md` (or the CLI reference), and `api/openapi.yaml`.
- `git status --short` is clean and the final merge SHA is recorded in the integration log.
