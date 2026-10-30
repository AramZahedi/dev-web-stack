# Lane H final status ÔÇö implementation complete, fresh audit not dispatched

- **Branch:** `lane/db-helper`
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **HEAD:** `bcc7bbaa35c11e6d27abc2e03efc5b849496ecc0`
- **Worktree:** clean
- **Date:** 2026-10-31

## Completed work

This round resumed the quota-killed session `ses_e719a01e8ffejairZT6YJPB5cK`. It verified the preserved I3 WIP and completed the remaining re-audit findings.

| Finding | Commit | Resolution |
|---|---|---|
| C1 SQL user IDs | `3247fdf` | `ListUsers` derives `usr_<sha256[:8]>` from `(serviceID, username, normalized host)`; MySQL/MariaDB empty hosts normalize to `%`; rollback uses the same helper. Test injects `dbengine.DefaultRegistry` with empty-ID fakes and exercises list/update/password/reveal/delete. |
| C2 MySQL/MariaDB KILL rows | `0189cd0` | `terminateSessions` executes `SELECT CONCAT('KILL ', ...)` rows, requires a `KILL ` prefix, and runs each statement. Covered for drop, restore-overwrite, and replace-import. |
| I1 full gzip validation | `12279df` | gzip is fully decompressed (CRC/trailer) before any drop; truncated-gzip tests assert no `DROP DATABASE`. |
| I2 partial stream reads | `ee6f7f2` | `writeStream` rejects any non-EOF first-read error, including `n > 0`, before committing 200. |
| I3 wildcard scopes / engine errors | `ec94f39`, `9eb6d13`, `f7ccd83` | Current wildcard scopes such as `app%` are never revoked; desired global/wildcard scopes return `ErrInvalidRequest`; `engineError` preserves both engine and domain sentinels. |
| I4 Redis ACL migration confirmation | `934efe7` | Added `POST /v1/services/{id}/redis/acl-file` with `{confirm: bool}` and `EnableRedisACLFileConfirmed`. Migration without confirmation returns `databases.ErrConfirmationRequired` (`CONFIRMATION_REQUIRED`, HTTP 409) before any config write, ACL seed, or restart. Redis mutations use the unconfirmed path and fail closed until confirmed. |

All remaining Minors were addressed: empty backup listing, backup-kind validation, structural Redis errors, existing `users.acl` 0600/directory rejection, site-env identifier validation, unknown-field/password rejection, bare 404/409 mapping, remaining engine wrapping, grant-error logging, enable-only Redis rule preservation, `RunInputStream` comment, `DropDatabase` `BackupFirst` documentation, Redis create conflict, and the created-user shape on rule failure. Missing tests for DB-route peer UID, `noauth` username, existing ACL mode, and desired wildcard-grant rejection were added.

## Gate

**Windows**

- `go test ./internal/helper ./internal/helperclient ./internal/helperproto ./internal/dbengine -count=1` ÔÇö PASS
- `go test ./... -count=1` ÔÇö PASS
- `go vet ./...` ÔÇö PASS
- `go build ./...` ÔÇö PASS
- `gofmt -l` ÔÇö clean

**Linux/WSL ext4**

- Lane package tests ÔÇö PASS
- `go test -race ./internal/helper ./internal/helperclient -count=1` ÔÇö PASS
- `go vet ./...` ÔÇö PASS
- `go build ./...` ÔÇö PASS

Full Linux `go test ./...` remains red only for the pre-existing base failures: `internal/agent` (`TestAgentConfigTerminalLimitsRoundTrip`), `internal/api` (`TestTerminalWebSocketEndToEnd`), `internal/sites` (two Node tests), and `internal/wsl` (`TestDefaultInstallDirUsesProfileHome`).

## Open items

- **Fresh audit not dispatched.** The controller directive is to spawn no further subagents, so this lane has implementation and self-run gates but no new independent audit round. The prior re-audit findings are all resolved in the commits above.
- **Merge dependency:** Lane A's concrete `internal/dbengine` registry is not present in this worktree. C1's test uses an injected `DefaultRegistry` fake with empty IDs; integration must verify the real registry and the merged binary.
- **Lane B integration:** the daemon must call `helperclient.EnableRedisACLFile(ctx, "redis", true)` (or POST the new RPC) after explicit user confirmation before Redis mutations can proceed.
- The 1 GiB import test still asserts the limit constant and exercises `copyLimited` with a small limit.
