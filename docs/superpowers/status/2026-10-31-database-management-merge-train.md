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
| Merge Lane D | pending | — | — |
| Merge Lane E | pending | — | — |
| Redis ACL confirmation path | pending | — | — |
| Merged registry verification | pending | — | — |
| Full merged-tree gate | pending | — | — |
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
