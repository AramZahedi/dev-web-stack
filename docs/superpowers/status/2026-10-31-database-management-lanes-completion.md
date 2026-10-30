# Database management parallel lanes — completion handoff

- **Date:** 2026-10-31
- **Foundation:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **State:** all six lane subagents finished; all lane worktrees clean; **nothing merged, pushed, or rebased**
- **Subagent directive:** no further subagents were spawned after the six continuations. Lanes A, B, C, and D therefore have implementation and self-run gates but **no fresh independent audit** of their final fix rounds. Lane E has a clean final audit.

## Lane heads

| Lane | Branch | Code HEAD | Docs HEAD | Final state |
|---|---|---|---|---|
| A — Windows/WSL agent | `lane/db-agent` | `9edb8a2` | `945c715` | Implementation complete; second audit findings fixed; no fresh re-audit |
| H — Linux helper | `lane/db-helper` | `bcc7bba` | `920edc1` | Implementation complete; re-audit findings fixed; no fresh audit |
| B — daemon/store/API | `lane/db-daemon` | `a769e10` | `823a9d4` | Implementation complete; re-audit findings fixed; no fresh audit; 2 Lane A cross-lane items open |
| C — service dashboard | `lane/db-ui-service` | `34b743e` | `34b743e` | Implementation complete; re-audit findings fixed; no fresh audit |
| D — Add Site / Site Detail | `lane/db-ui-site` | `b48d787` | `c54787d` | Implementation complete; re-audit findings fixed; no fresh audit |
| E — CLI | `lane/db-cli` | `80a41fc` | `053ed93` | **Final audit PASS**; no Critical/Important; ready for merge |

Every lane worktree reports `git status --porcelain` empty.

## What each lane completed

### Lane A — agent adapters

Fixed the second audit's new Critical and seven Important findings:

- PostgreSQL grants/revokes now run in the target database (`psql --dbname=<grant.Database>`), including rollback and restore paths.
- PostgreSQL enable/disable accepts existing dotted/dashed roles via `QuoteExistingIdentifier`.
- Backup paths reject symlinked parents at every component; listing skips symlinked entries.
- PostgreSQL inventory/grant scripts fail when the initial query fails.
- Redis custom ACL rules are preserved on enable/disable.
- Password-bearing SQL is redacted from runner output, jobs, and HTTP errors before truncation.
- Redis `-x` keeps every ACL rule and rejects multiple password arguments.
- MariaDB `DB_CONNECTION` is pinned to `mariadb`.

Minors fixed: `/database-env` no-store, bounded stream stderr, PG `pg_*` protection, merge-mode pre-import backup, foreign Redis `aclfile` rejection, enablement rollback on grant failure, Redis `Grants` rejection, and cross-lane user-ID shape pinning.

Gate: focused tests, full suite, vet, build, WSL race suites, and targeted agent race all pass.

### Lane H — Linux helper

Fixed C1 (helper-side SQL user IDs), C2 (execute MySQL/MariaDB `KILL` rows), I1 (full gzip validation before destructive work), I2 (partial stream reads), I3 (wildcard grants and engine-error mapping), and I4 (Redis ACL migration confirmation gate with `CONFIRMATION_REQUIRED`/HTTP 409 before any write or restart). All requested Minors and missing tests were addressed.

Gate: Windows lane/full tests, vet, build, and gofmt pass; WSL/Linux lane tests, race, vet, and build pass. Full Linux `go test ./...` has only the pre-existing base failures in `internal/agent`, `internal/api`, `internal/sites`, and `internal/wsl`.

### Lane B — daemon, store, API

Fixed I-4 (transactional uninstall metadata cleanup with warnings), I-5 (service lock across stopped-site guard + pre-operation backup + platform call), and env persistence (`RuntimeEnv` for Linux process/exec env; `DatabaseLinks` for Windows; no DB password in persisted site env or `php-fpm.conf`). The remaining Minors were fixed, including ownership checks, protected links, import extension validation, backup pruning, live `PasswordManaged`, rename rollback, terminal degradation, impact OpenAPI `$ref`, and all SSE event names.

Gate: lane packages, full suite, vet, build, Linux cross-build, and Linux backend test compile all pass.

### Lane C — service dashboard

Fixed the Critical rename gate and all Important findings: terminal job toasts, protected database backup/export/grant handling, fail-closed overwrite restore, `stopping` site gating, persistent restore recovery, forced pre-import backup, and destructive payload tests. The Redis reveal-refresh behavior was implemented and tested. Remaining Minors were fixed or verified, including backup fallback filtering, `confirmDrop` re-check, page-local restart prompts, reset-reveal documentation, and deactivate-dialog reset.

Gate: `svelte-check` 0/0, focused 70 tests, full 267 tests, production build.

### Lane D — Add Site and Site Detail

Fixed Important 1 (managed user with unknown password: distinct copy, Reset action, post-create handling) and Important 2 (link-dialog validation no longer clears itself). Minors fixed: protected new identifiers, URL/secret sanitizing, reveal/reset races, exact post-create matching, link-load failure handling, picker retry preservation, mock link synchronization, and metadata-only URL omission.

Gate: `svelte-check` 0/0, focused 113 tests, full 232 tests, production build.

### Lane E — CLI

Final audit of `44c9d68..bb063ed` (code `80a41fc`) **passed spec compliance and code quality with no Critical or Important findings**. The ten fixes and all core invariants were verified: no secrets in argv/script/errors, protected env-file transport, fail-fast external-password deferral, explicit `--set` rejection, Redis-only `db password`, and sanitized list/JSON output.

Gate: Windows and WSL/Linux lane tests, full suite, vet, build, gofmt, and `git diff --check` all pass.

## Open items before merge

1. **Fresh audits for A, B, C, and D are missing.** The original merge rule requires a clean independent audit. Because no further subagents may be spawned, these lanes need either:
   - a controller-run audit without subagents, or
   - explicit user authorization to merge on implementation + self-run gates.
   Lane E is already clean.
2. **Lane B cross-lane fixes still open:**
   - `internal/databases/audit.go` `dbConnectionValue` still returns `mysql` for MariaDB; it must return `mariadb`.
   - `stableUserID` still uses `digest[:12]`; it must use `digest[:8]` to match Lane A's `usr_` + 16-hex shape.
3. **Integration dependencies:**
   - Lane H's C1 expects Lane A's concrete `internal/dbengine` registry; verify the merged registry and merged binary.
   - Lane B's Windows bridge still uses the JSON `database_links` bridge; replace with a direct `agentproto.DatabaseLinkSpec` assignment once Lane A lands it.
   - Lane B must call `helperclient.EnableRedisACLFile(ctx, "redis", true)` after explicit user confirmation before Redis mutations.
4. **Deferred Minors:**
   - Backup delete lacks a client-supplied typed confirmation token (API contract change).
   - Windows terminal environment can be stale after linking (needs a re-prepare hook).
   - Lane C's `loadServices` generation guard has no overlapping-response test.
5. **Live E2E not run.** It must use freshly built merged binaries, not the installed v0.3.1 binary.

## Merge order

`A → H → B → C → D → E`, then the integration plan (`docs/superpowers/plans/2026-10-30-database-management-07-integration.md`).

## Recovery artifacts

Each lane branch contains `docs/superpowers/recovery/` with:

- `RECOVERY.md` — the interrupted-session state and next steps.
- `<session>.md` and `<session>.json.gz` — the full readable and raw transcripts.
- `FINAL-STATUS.md` — the final implementation state, gate, and open items.
- Lane A also has `AUDIT-FINDINGS.md`; Lane E also has `FINAL-AUDIT.md`.

The controller status map is `.superpowers/db-lanes-status.md` (gitignored).
