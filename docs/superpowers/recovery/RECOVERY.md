# Lane H recovery — interrupted helper fixer

- **Session:** `ses_e719a01e8ffejairZT6YJPB5cK`
- **Outcome:** failed — provider quota `429 GoUsageLimitError` (`retryAfterMs` 777000)
- **Branch:** `lane/db-helper`
- **HEAD at interruption:** `ee6f7f26e1dd64b2c393646ca584ba298d68db24` plus uncommitted WIP
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **Full transcript:** `ses_e719a01e8ffejairZT6YJPB5cK.md` (readable) and `ses_e719a01e8ffejairZT6YJPB5cK.json.gz` (raw)

## What is committed

Commits made after the previous audit (`2611c2e`):

| Commit | Finding | Summary |
|---|---|---|
| `3247fdf` | C1 | Derive SQL user IDs for the real engine registry (same `usr_<sha256[:8]>` scheme as Lane A). |
| `0189cd0` | C2 | Execute MySQL/MariaDB session-termination `KILL <id>` rows instead of discarding the `SELECT CONCAT(...)` result. |
| `12279df` | I1 | Fully validate gzip streams (CRC/trailer) before destructive restore/import. |
| `ee6f7f2` | I2 | Reject partial stream reads before sending HTTP 200 on export/download. |

## Uncommitted WIP (must not be lost)

`internal/helper/service_database.go`:

- Added `engineError(err)` mapping `dbengine.ErrUnsupported` → `databases.ErrUnsupported` and `dbengine.ErrInvalidIdentifier` → `databases.ErrInvalidRequest`.
- Wrapped engine errors in `ListDatabases`, `CreateDatabase`, `RenameDatabase`, `DropDatabase`, `terminateSessions`, `CreateUser`, `DeleteUser`, `SetUserPassword`, `rollbackCreatedUser`, `applyUserGrants`.
- Added `isGrantableScope(engine, database)` and skip/reject wildcard scopes (`app%`); desired global scopes return `ErrInvalidRequest`.

`internal/helper/service_database_test.go`:

- Added `grantErr` to `fakeDatabaseEngine`.
- `TestHelperGrantDiffSkipsWildcardScopes`
- `TestHelperGrantDiffRejectsDesiredGlobalScope`
- `TestHelperGrantEngineErrorIsDomainError`

This is the **I3 fix** plus the “wrap engine errors as domain errors” minor. It has not been compiled/tested yet. The last action was an `edit` wrapping engine errors; the session died immediately after.

## Exact next steps

1. Finish/compile the uncommitted I3 work:
   - `gofmt` the two files.
   - `go test ./internal/helper -count=1` (Windows compiles; Linux/WSL runs the real DB tests).
   - Ensure `isGrantableScope` is used consistently in both the current-grant skip and the desired-grant validation, and that `RevokeSQL` is never called for `%` scopes.
2. Implement **I4 — Redis ACL migration confirmation gate** (not started):
   - Add an optional `confirm` field to the Redis mutation RPCs and/or a dedicated `EnableRedisACLFile` RPC with `confirm`.
   - If migration is needed and `confirm` is false, return a typed confirmation-required error **before any change or restart**.
   - Keep existing signatures working; default false must fail closed when migration is needed.
   - Add tests for unconfirmed and confirmed migration.
3. Fix the remaining Minors from the H re-audit:
   - `ListBackups` nil → empty slice when the tree is absent.
   - Tighten `redisOutputError` to structural `(error) NOAUTH`/`WRONGPASS`.
   - Validate backup `kind` enum.
   - Enforce `users.acl` 0600 on existing files and reject directories.
   - Validate `ResolveSiteEnv` link database/username identifiers.
   - Reject unknown JSON fields / a supplied `password` field in `decodeJSON`.
   - Fix `helperclient.codeForStatus` 404/409 over-mapping.
   - Wrap remaining `dbengine.ErrInvalidIdentifier`/`ErrUnsupported` as domain errors.
   - Log/debug grant-listing errors.
   - Preserve custom Redis rules on enable-only updates.
   - Fix the `RunInputStream` comment.
   - Document `DropDatabase` `BackupFirst` orchestration.
   - Return `ErrConflict` when Redis `CreateUser` targets an existing name.
   - Return the created user shape when `redisApplyUserRules` fails.
   - Add the missing tests (real-registry IDs/session, truncated gzip, `n>0` stream error, desired-global-grant rejection, import failure job, DB-route peer-UID, `noauth` username, existing ACL mode).
4. Run the full gate:
   - Windows: `go test ./internal/helper ./internal/helperclient ./internal/helperproto ./internal/dbengine -count=1`, `go test ./... -count=1`, `go vet ./...`, `go build ./...`
   - Linux/WSL ext4: lane package gate, `go test -race ./internal/helper ./internal/helperclient -count=1`, `go vet ./...`, `go build ./...`
   - Note pre-existing Linux failures that reproduce at base.
5. Commit each logical fix; update `.superpowers/lane-report.md`; report the new HEAD SHA and gate results. Do not merge/push/rebase.

## Last known gate

The last green gate was before this fix round: Windows and WSL lane packages, race, vet, and build all passed. The uncommitted I3 changes have **not** been gated.
