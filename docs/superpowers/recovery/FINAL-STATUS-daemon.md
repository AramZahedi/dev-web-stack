# Lane B final status ÔÇö implementation complete, fresh audit not dispatched

- **Branch:** `lane/db-daemon`
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **HEAD:** `a769e10adfef23395509452b22c7b2fc0c3d6c83`
- **Worktree:** clean
- **Date:** 2026-10-31

## Completed work

This round resumed the quota-killed session and completed the remaining B re-audit findings.

| Finding | Commit | Resolution |
|---|---|---|
| I-4 uninstall cleanup | `93fd1aa` | Added `uninstallResponse` embedding `servicecontrol.UninstallResult` plus `Warnings`. Uninstall calls only `DeleteServiceMetadata`; links/users/grants/backups/jobs are deleted in one store transaction, and failures return HTTP 200 with a redacted warning. A SQLite trigger test proves rollback, and API tests cover the single-call path and redacted warning. |
| I-5 destructive critical section | `a3acd3e` | `DropDatabase`, `RestoreBackup`, and `ImportDatabase` hold the service lock across guard + pre-operation backup + platform call; `RenameDatabase`'s stopped-site check moved under the same lock. `createBackupLocked` avoids nested locking. A concurrency test blocks the pre-drop backup and proves a concurrent link cannot enter until drop completes. |
| Env persistence | `9241a5f` | `backendSpecFor` writes resolved DB env to `RuntimeEnv`, never `Env`. Linux process/exec env merges `RuntimeEnv` for static/Node/PHP; Windows uses password-free `DatabaseLinks`; PHP config composition waits for the site row. Tests prove the persisted site and `php-fpm.conf` contain no DB password while Linux Node/static process/exec env does. |

Remaining Minors fixed across `764b825`, `78d8b48`, `53e89cc`, `d516df4`, `c389e83`, `bde363c`, and `a769e10`: link encoding errors surface; `SetDefaultUserPassword` is Redis-only; `SetUserPassword` audits platform failures with redaction; user/backup mutations verify service ownership; backup metadata preserves `Path`/`Kind` and uses a detached context; `ListBackups` prunes externally deleted metadata; non-SQL service rejection; protected database link/provision rejection; empty optional bodies accepted; import filenames limited to `.sql`/`.sql.gz`; live `PasswordManaged` state; rename rollback on reference failure; terminal degradation when DB env fails; impact OpenAPI `$ref`; all SSE event names covered; Add Site warning, fake-store concurrency, and daemon wiring tests present; completed jobs release cancellation state.

## Gate

- `go test ./internal/store ./internal/databases ./internal/servicecontrol ./internal/api ./internal/sites ./internal/tooling ./internal/terminal ./internal/phpfpm ./internal/daemon -count=1` ÔÇö PASS
- `go test ./... -count=1` ÔÇö PASS
- `go vet ./...` ÔÇö PASS
- `go build ./...` ÔÇö PASS
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./internal/daemon ./internal/terminal ./internal/backend/...` ÔÇö PASS
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/backend` ÔÇö PASS
- `git status --short` ÔÇö clean

## Open items

- **Fresh audit not dispatched.** The controller directive is to spawn no further subagents, so this lane has implementation and self-run gates but no independent audit of this fix round.
- **Two Lane A cross-lane fixes are still open in this worktree:**
  - `internal/databases/audit.go` `dbConnectionValue` still returns `mysql` for MariaDB; it must return `mariadb` to match Lane A.
  - `internal/databases/audit.go` `stableUserID` still uses `digest[:12]`; it must use `digest[:8]` to match Lane A's `usr_` + 16-hex user ID shape.
- **Deferred Minors:**
  1. Backup delete does not require a client-supplied confirmation token; the server verifies ownership only. A typed-confirmation token is an API contract change needing matching CLI/dashboard updates.
  2. `backend/wsl` still uses the JSON `database_links` bridge because `agentproto.SiteSpec` does not yet expose the field; replace with direct assignment during integration once Lane A lands it.
  3. Windows terminal environment can be stale after linking; requires a Windows terminal re-prepare hook in the agent/terminal lane.
