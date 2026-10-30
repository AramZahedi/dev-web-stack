# Lane A audit findings — fix round required

- **Audit session:** `ses_e712ea182ffeoLGhwVNelOCWLX` (completed)
- **Branch:** `lane/db-agent`
- **HEAD at audit:** `8b774573f39cd3929316a9a8bf46e619d08fd706`
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`

The audit passed the earlier C1–C6 and most I1–I11 fixes, but found one new Critical and seven new Important issues. The full audit text is in the controller transcript; this file is the actionable summary.

## New Critical

**New-C1 — PostgreSQL grants/revokes run against `postgres`, not the target database.**
`internal/agentdocker/database_exec.go:21-26` always uses `psql ... --dbname=postgres`; `CreateUser`/`UpdateUser` run `GrantSQL`/`RevokeSQL` through it. `GRANT CONNECT ON DATABASE` is cluster-level and works, but schema/table/default-privilege statements are database-local and land in `postgres`. New PG users get no schema/table privileges; `GrantAdmin` over-grants `postgres`; `UpdateUser` cannot revoke stale target privileges.
Fix: execute each grant/revoke with `psql --dbname=<grant.Database>` (or an explicit `\connect`) per target database. Add a test asserting the `--dbname` target for every preset and for revokes.

## New Important

1. **PG enable/disable rejects dotted/dashed roles.** `database_users.go:420-446` uses `QuoteIdentifier`, which rejects `app-user`/`app.user`; use `dbengine.QuoteExistingIdentifier`/`quotePostgresRole`. Add a PG `app-user` enable/disable test.
2. **Backup symlinked parent directories are followed.** `databaseBackupPath`/`openRegularBackup` only check the final component; a symlinked `backups/databases/<service>/<database>` escapes the tree for list/delete/download/restore. Lstat/verify every component or `EvalSymlinks` and verify containment. Add a WSL symlink test.
3. **PG inventory/grant scripts mask initial `psql` failure.** `psql ... | while ...` and `for db in $(psql ...)` exit 0 when the first `psql` fails, returning empty success. Capture the query output and check status (`dbs=$(psql ...) || exit 1`). Add failure tests.
4. **Redis enable/disable rewrites custom ACL users to read-only.** `UpdateRedisUser` calls `SetUserRulesCommand`, which starts with `-@all`; custom users lose their rules. For `AccessLevel == "custom"`, issue only `ACL SETUSER <user> on|off` or return `ErrUnsupported`; add a custom-rule test.
5. **Password-bearing SQL errors are not redacted.** `RunInput` includes combined output; `CreateUser`/`SetUserPassword` errors can echo `IDENTIFIED BY '...'`/`PASSWORD '...'`. Redact those fragments from runner errors; add a regression test.
6. **Redis `-x` delivery drops the final ACL rule.** `redisCommandWithSecret` removes the password placeholder and prepends `-x`; `redis-cli -x` replaces the last argument, so the last rule is replaced by the password. Append a non-secret placeholder that `-x` replaces, or otherwise keep every rule. Add a test asserting the post-`-x` argv contains all rules.
7. **Cross-lane MariaDB `DB_CONNECTION` divergence.** Lane A emits `mariadb`; Lane B emits `mysql` for metadata-only MariaDB links. Align (prefer `mariadb`) and add a cross-lane test.

## Minors

- `/database-env` lacks `Cache-Control: no-store`.
- `RunStream` stderr is unbounded.
- PostgreSQL predefined `pg_*` roles are listed and unprotected.
- `ImportDatabase` ignores `PreImportBackup` in merge mode.
- Redis ACL migration assumes any existing `aclfile` points to `/data/users.acl`.
- `UpdateUser` does not roll back an enabled/disabled change when grant updates fail.
- `SetRedisUser` ignores `req.Grants` instead of rejecting it.
- Lane B `stableUserID` fallback uses `digest[:12]`; Lane A uses `digest[:8]`. Align if a platform ever returns an empty ID.

## Gate

- `go test ./internal/dbengine ./internal/agentdocker ./internal/agentclient ./internal/agentproto ./internal/agent -count=1`
- `go test ./... -count=1`, `go vet ./...`, `go build ./...`
- WSL/Linux race: `go test -race ./internal/dbengine ./internal/agentdocker ./internal/agentclient ./internal/agentproto` and targeted `-race ./internal/agent` (`TestPrepareSite|TestTerminal|TestDatabase|TestBuild|TestPHP|TestExec`)
- `git status --short` clean
