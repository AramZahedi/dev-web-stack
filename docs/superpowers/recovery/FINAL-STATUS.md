# Lane A final status — audit fixes complete, fresh re-audit not dispatched

- **Branch:** `lane/db-agent`
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **HEAD:** `9edb8a2f90614f1303a8a03ea092921634eca3b1`
- **Worktree:** clean
- **Date:** 2026-10-31

## Completed work

The second audit of `8b77457` found one new Critical and seven new Important issues. This round fixes all of them with regression tests, plus the safe Minors.

| Finding | Commit | Resolution |
|---|---|---|
| New-C1 PG grants/revokes against `postgres` | `54dde46` | Added `databaseSQLCommandFor`, which points `psql` at `--dbname=<grant.Database>`. All grant, revoke, rollback, and restore paths use it; role-level DDL stays on `postgres`. Test asserts the target database for all five presets and for revokes. |
| N1 dotted/dashed PG roles | `f64cedd` | `databaseSetEnabledSQL` uses `dbengine.QuoteExistingIdentifier`; covered by a PG `app-user`/`app.user` test. |
| N2 symlinked backup parents | `0d75b84` | `ensureNoSymlinkComponents` validates every component from the agent root through the service/database directory and final file. `ListBackups` skips symlinked entries; `databaseBackupPath` rejects them. WSL tests cover symlinked database and service directories. |
| N3 masked `psql` failures | `e4be0b6` | Inventory and grant scripts capture `dbs=$(psql ...) || exit 1`. WSL tests with a failing fake `psql` assert non-zero exits. |
| N4 Redis custom ACL rewrite | `541d25f` | Custom users get only `ACL SETUSER <user> on|off` via `dbengine.SetUserEnabledCommand`; test asserts no `-@all`/`+@read`/`reset` rewrite and no password rotation. |
| N5 password-bearing SQL errors | `18c5fb6`, `9edb8a2` | `redactSQLSecrets` covers `IDENTIFIED BY`, `IDENTIFIED WITH … BY`, and `PASSWORD`; applied at the runner, SQL/input paths, job messages, and HTTP errors before truncation. `redactedError` preserves `errors.Is`. Tests cover failed password set, HTTP body, failed job, and the real `execRunner`. |
| N6 Redis `-x` rule loss | `c7da8e1` | `redisCommandWithSecret` keeps every rule and rejects multiple password arguments. Tests assert the post-`-x` argv contains every rule, including the final `-failover`, with the password only on stdin. No placeholder was added because `redis-cli -x` appends stdin to the command. |
| N7 cross-lane MariaDB name | `0a20402` | Lane A emits `mariadb` from both the agent runtime and `/database-env`; `TestCrossLaneMariaDBConnectionName` and `TestCrossLaneMariaDBConnectionNameRoute` pin it. |

Minors fixed: `/database-env` no-store (`7a13ee4`), bounded `RunStream` stderr (`607cf1f`), PG `pg_*` protection (`d718018`), merge-mode `PreImportBackup` (`3bbba87`), foreign Redis `aclfile` rejection (`571889c`), enabled-state rollback on grant failure (`281f525`), Redis `Grants` rejection (`8f4dcfa`), and cross-lane user-ID shape pinned (`062d31e`).

## Gate

- Focused lane tests — PASS
- `go test ./... -count=1` — PASS
- `go vet ./...` — PASS
- `go build ./...` — PASS
- WSL race (`./internal/dbengine ./internal/agentdocker ./internal/agentclient ./internal/agentproto`) — PASS
- Targeted WSL agent race (`TestPrepareSite|TestTerminal|TestDatabase|TestBuild|TestPHP|TestExec`) — PASS
- `git status --short` — clean

## Open items

- **Fresh re-audit not dispatched.** The controller directive is to spawn no further subagents, so this lane has implementation and self-run gates but no independent audit of `54dde46..9edb8a2`.
- **Deferred to Lane B (outside this worktree):**
  - `internal/databases/audit.go` still maps MariaDB to `mysql` in `dbConnectionValue`; it must return `mariadb`.
  - `stableUserID` fallback must use `digest[:8]`, not `digest[:12]`, to match Lane A's `usr_` + 16-hex shape.
- **Integration:** Lane H consumes Lane A's concrete `internal/dbengine` registry; the merged binary must be verified.
- **Known limitation:** MySQL/MariaDB dumps omit `--databases`, so restore-to-new-target cannot be redirected by `USE`; session termination returns `KILL <id>;` rows that the agent executes individually.
- External-password storage remains deferred to v1.1; the agent rejects caller-supplied password fields with `ErrUnsupported`.
