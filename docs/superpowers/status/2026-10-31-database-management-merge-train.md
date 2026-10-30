# Database management merge train — live status

- **Branch:** `integration/database-management`
- **Base:** `1464451` (`master`, after the lane completion handoff)
- **Started:** 2026-10-31
- **Flow:** controller-only; targeted review of each lane's final fix commits; no new subagents.
- **Exception:** A, B, C, and D have no fresh independent audit; the user authorized the cheaper targeted-review flow.

## Progress

| Step | State | Commit | Gate |
|---|---|---|---|
| Lane B cross-lane fixes (`mariadb`, `digest[:8]`) | done on `lane/db-daemon` | `2588655` | `go test ./internal/databases` PASS |
| Targeted review Lane A | done | — | no Critical/Important |
| Merge Lane A | done | `768cb73` | agent/dbengine packages PASS; build + vet PASS |
| Merge Lane H | done | `74eb07c` | helper/helperclient/helperproto/dbengine PASS; build + vet PASS |
| Merge Lane B | done | `ed725a9` | store/databases/servicecontrol/api/sites/tooling/terminal/phpfpm/daemon PASS; build + vet PASS |
| Windows bridge direct assignment | done | `c40f230` | wsl backend PASS; build + vet PASS |
| Merge Lane C | done | `18b5194` | svelte-check 0/0; 77 focused tests; 267 full tests; build PASS |
| Merge Lane D | done | `f0c5e0f` | svelte-check 0/0; 79 focused tests; 329 full tests; build PASS |
| Merge Lane E | done | `d6af29a` | CLI/client/tooling PASS; full Go suite PASS; vet + build PASS |
| Redis ACL confirmation path | done | `c948272` | manager/servicecontrol/API tests PASS; UI 330 tests PASS; build + vet PASS |
| Merged registry verification | done | — | dbengine/helper/helperclient/agentdocker/agentclient + databases/servicecontrol + API + sites PASS |
| Full merged-tree gate | done | `f60e1a8` | Go race/vet/build PASS; UI 0/0 + 330 tests + build PASS; 5 binaries built |
| Full E2E plan | pending | — | — |

## Step log

### Step 1 — Lane B cross-lane fixes

- `dbConnectionValue` now returns `mariadb` for MariaDB.
- `stableUserID` now uses `digest[:8]`, matching Lane A's `usr_` + 16-hex shape.
- New `internal/databases/audit_test.go` covers both; RED first, then GREEN.
- Commit `2588655` on `lane/db-daemon`.

### Step 2 — Lane A targeted review and merge

- Reviewed `8b77457..9edb8a2`: target-database PostgreSQL grants/revokes, dotted/dashed roles, symlink-safe backup paths, failing `psql` propagation, Redis custom-rule preservation, SQL secret redaction, Redis `-x` rule retention, and MariaDB connection pinning.
- No Critical or Important findings in the reviewed range.
- Merge commit `768cb73` (`merge: database agent adapters`).
- Gate: `go test ./internal/dbengine ./internal/agentdocker ./internal/agentclient ./internal/agentproto ./internal/agent -count=1` PASS; `go build ./...` PASS; `go vet ./...` PASS.

### Step 3 — Lane H targeted review and merge

- Reviewed `2611c2e..bcc7bba`: helper-side SQL user IDs, MySQL/MariaDB `KILL` execution, full gzip validation, partial-stream rejection, wildcard grant handling, Redis ACL migration confirmation, structural Redis errors, existing `users.acl` mode checks, and strict JSON decoding.
- No Critical or Important findings in the reviewed range.
- **Foundation resolution:** accepted the additive `databases.ErrConfirmationRequired` and `databases.RedisACLFilePlatform` in `internal/databases/contracts.go`. The change adds a typed error and an optional interface; it does not alter existing signatures. Cost if wrong: contract drift, mitigated by the additive-only shape and the confirmation tests.
- **Documentation conflict resolution:** every lane writes `docs/superpowers/recovery/FINAL-STATUS.md`; kept A as `FINAL-STATUS-agent.md` and H as `FINAL-STATUS-helper.md`.
- Merge commit `74eb07c` (`merge: database Linux helper adapters`).
- Gate: `go test ./internal/helper ./internal/helperclient ./internal/helperproto ./internal/dbengine -count=1` PASS; `go build ./...` PASS; `go vet ./...` PASS.

### Step 4 — Lane B targeted review and merge; Windows bridge

- Reviewed `2d76a84..2588655`: transactional uninstall cleanup, async backup/restore/import jobs with service locking, env persistence through `RuntimeEnv`, ownership checks, protected links, import validation, backup pruning, live `PasswordManaged`, rename rollback, terminal degradation, and the two cross-lane fixes.
- No Critical or Important findings in the reviewed range.
- **Documentation conflict resolution:** kept H as `RECOVERY-helper.md` and B as `RECOVERY-daemon.md`.
- Merge commit `ed725a9` (`merge: database daemon API and site links`).
- Gate: `go test ./internal/store ./internal/databases ./internal/servicecontrol ./internal/api ./internal/sites ./internal/tooling ./internal/terminal ./internal/phpfpm ./internal/daemon -count=1` PASS; `go build ./...` PASS; `go vet ./...` PASS.
- Replaced the JSON `database_links` bridge in `internal/backend/wsl/backend.go` with a direct `agentproto.DatabaseLinkSpec` assignment now that Lane A exposes `SiteSpec.DatabaseLinks`. Commit `c40f230`; `go test ./internal/backend/wsl` PASS; build + vet PASS.

### Step 5 — Lane C targeted review and merge

- Reviewed `045c3dc..34b743e`: rename/drop linked-site gates, protected backup/export handling, fail-closed overwrite restore, `stopping` site handling, persistent restore recovery, forced replace-import backup, job toasts, Redis reveal refresh, page-local restart prompts, and deactivate-dialog reset.
- No Critical or Important findings in the reviewed range.
- Merge commit `18b5194` (`merge: database service dashboard`).
- Gate: `npm run check` 0 errors/0 warnings; focused Vitest 5 files/77 tests PASS; `npm test -- --run` 27 files/267 tests PASS; `npm run build` PASS (existing >500 kB chunk warning only).

### Step 6 — Lane D targeted review and merge

- Reviewed `6426234..b48d787`: managed-user password recovery, link-dialog validation persistence, protected identifiers, URL/secret sanitizing, reveal/reset races, exact post-create matching, link-load failure handling, picker retry preservation, mock link sync, and metadata-only URL omission.
- No Critical or Important findings in the reviewed range.
- **Documentation conflict resolution:** kept B as `FINAL-STATUS-daemon.md`, D as `FINAL-STATUS-site.md`, C as `RECOVERY-service.md`, and D as `RECOVERY-site.md`.
- Merge commit `f0c5e0f` (`merge: database site dashboard`).
- Gate: `npm run check` 0 errors/0 warnings; focused Vitest 2 files/79 tests PASS; `npm test -- --run` 30 files/329 tests PASS; `npm run build` PASS (existing >500 kB chunk warning only).

### Step 7 — Lane E merge

- Lane E already passed its final independent audit (`44c9d68..bb063ed`, code `80a41fc`).
- Merge commit `d6af29a` (`merge: database CLI`).
- Gate: `go test ./internal/cli ./internal/client ./internal/tooling -count=1` PASS; `go test ./... -count=1` PASS; `go vet ./...` PASS; `go build ./...` PASS.

### Step 8 — Redis ACL persistence confirmation path

- Added `Manager.EnableRedisACLFile(ctx, serviceID, confirm)` with an optional platform interface; it validates Redis/active state, audits, and emits.
- Added `databasePlatform.EnableRedisACLFile`: the helper uses the confirmed RPC; the Windows agent requires `confirm=true` before its migration call; unsupported platforms fail closed.
- Added `POST /api/v1/services/{id}/redis/acl-file` with `{confirm}` and `CONFIRMATION_REQUIRED`/HTTP 409 mapping; documented in `api/openapi.yaml`.
- UI: `databaseApi.enableRedisACLFile`, an ACL persistence card in the Redis Users tab with an explicit confirm dialog, and a clear “open the Redis tab and enable it” message when a user mutation hits the confirmation gate.
- Tests: manager confirmation test, helper/agent adapter tests, API route test, RedisTab confirm-dialog test. RED→GREEN for each new behavior.
- Commit `c948272`.
- Gate: `go test ./internal/databases ./internal/servicecontrol ./internal/api -count=1` PASS; `go build ./...` PASS; `go vet ./...` PASS; `npm run check` 0/0; `npm test -- --run` 30 files/330 tests PASS; `npm run build` PASS.

### Step 9 — Merged registry and cross-lane contract verification

- `internal/dbengine` `TestRegistryResolvesEngines` covers MySQL, MariaDB, PostgreSQL, and Redis through `DefaultRegistry`; helper, helperclient, and agent adapters all resolve the same engines.
- Helper and agent database adapters: `go test ./internal/dbengine ./internal/helper ./internal/helperclient ./internal/agentdocker ./internal/agentclient -count=1` PASS.
- Manager/platform adapters: `go test ./internal/databases ./internal/servicecontrol -run 'TestAgentDocker|TestHelperDocker|TestManager' -count=1` PASS.
- API routes, OpenAPI, and install-token boundary: `go test ./internal/api -run 'TestDatabase|TestOpenAPI|TestEveryAPIRouteRequiresTheInstallToken' -count=1` PASS.
- Site lifecycle and database links: `go test ./internal/sites -run 'TestCreate|TestDelete|TestStart|TestDatabase' -count=1` PASS.
- No code change was needed; the merged registry and adapters agree.

### Step 10 — Full merged-tree gate

- First `go test ./... -race -count=1` run failed with data races in `internal/databases` tests. Every report pointed at test-only doubles: background jobs wrote `fakeStore`, `fakeAudit`, and `fakeEvents` while tests read them. Production store and manager code was not implicated.
- Fix (test-only): added mutex protection to all `fakeStore`, `fakeAudit`, and `fakeEvents` methods; `waitJob` now also waits for the job goroutine's deferred cleanup, giving audit/event writes a happens-before edge with assertions. Commit `f60e1a8`.
- `go test ./... -race -count=1` PASS (race_exit=0, no `DATA RACE`, no failing tests).
- `go vet ./...` PASS; `go build ./...` PASS.
- `go test ./internal/api -run 'TestOpenAPI|TestEveryAPIRouteRequiresTheInstallToken|TestSecurity|TestDatabase' -count=1` PASS.
- UI: `npm run check` 0 errors/0 warnings; `npm test -- --run` 30 files/330 tests PASS; `npm run build` PASS (existing >500 kB chunk warning only).
- Deployable binaries built: `dist/gate/devstack.exe`, `dist/gate/devstack-helper.exe`, `dist/gate/devstack-linux-amd64`, `dist/gate/devstack-helper-linux-amd64`, `dist/gate/devstack-agent-linux-amd64`.
