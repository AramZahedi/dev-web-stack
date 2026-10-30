# Lane B recovery — interrupted daemon fixer

- **Session:** `ses_e719a01e6ffeLENsj3LX9TRMtQ`
- **Outcome:** failed — provider quota `429 GoUsageLimitError` (`retryAfterMs` 777000)
- **Branch:** `lane/db-daemon`
- **HEAD at interruption:** `86366acebf54e7c5c3b6aa94ee2407bd10af4683` plus uncommitted WIP
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **Full transcript:** `ses_e719a01e6ffeLENsj3LX9TRMtQ.md` (readable) and `ses_e719a01e6ffeLENsj3LX9TRMtQ.json.gz` (raw)

## What is committed

Commits made after the previous audit (`2d76a84`):

| Commit | Finding | Summary |
|---|---|---|
| `1c8f336` | C-1 | Resolve site env with global primary context; non-primary links use `DB_<SLUG>_*`. |
| `269b185` | I-1 | Run backup/restore/import jobs asynchronously with a cancel map and staged imports. |
| `5c69d42` | I-2 | Broaden redaction at job and backup boundaries. |
| `86366ac` | I-3 | Surface Add Site database link warnings (`model.Site.Warnings`). |

## Uncommitted WIP (must not be lost)

`internal/databases/impact.go`:

- `DeleteServiceMetadata` now counts links before deletion, audits `removed_links`, and emits `database.link.changed` when links were removed.

`internal/api/handlers_services.go`:

- Removed the separate `DeleteServiceLinks` call; only `DeleteServiceMetadata` runs.
- Collects a warning when cleanup fails and writes `uninstallResponse{UninstallResult: result, Warnings: warnings}`.

**Compile blocker:** `uninstallResponse` is referenced but not defined yet. Add it, e.g.:

```go
type uninstallResponse struct {
    servicecontrol.UninstallResult
    Warnings []string `json:"warnings,omitempty"`
}
```

The last action was editing `handlers_services.go`; the session died immediately after.

## Exact next steps

1. Finish **I-4**:
   - Add `uninstallResponse`.
   - Update `TestUninstallService` expectations: only `DeleteServiceMetadata` is called (links are deleted inside it).
   - Add a failure-path test where `DeleteServiceMetadata` fails; expect 200 with a `warnings` entry.
   - Add a fake `metadataErr` to the API fixture if needed.
   - Verify `DeleteServiceMetadata` is one transaction and still deletes links/users/grants/backups/jobs.
2. Implement **I-5** (not started): hold the service lock across the stopped-site check + pre-operation backup + destructive platform call. Add a lock-held `createBackupLocked`; avoid nested locking/deadlock; add a concurrency test.
3. Fix the **pending env-persistence** issue: `internal/sites/service.go:1774-1779` still merges resolved `databaseEnv` into `spec.Env` for non-PHP sites. Use `spec.RuntimeEnv` for Linux process env and `SiteSpec.DatabaseLinks` for Windows normal starts; reserve `/database-env` for explicit resolution. Add tests proving `sites.json`/`php-fpm.conf` never contain a DB password and that Node/static sites still receive DB env on both platforms.
4. Fix the remaining Minors from the B re-audit:
   - `applyDatabaseLinks` silently ignores unmarshal errors.
   - `SetDefaultUserPassword` not Redis-only.
   - `SetUserPassword` does not audit platform failures.
   - Record/service-ID ownership checks.
   - Backup metadata loses `Path`/`Kind`; uses request ctx.
   - `platformJobs` unbounded.
   - Rename reference failure leaves stale links.
   - `ListBackups` doesn’t prune externally deleted metadata.
   - `CreateDatabase`/`ListDatabases` don’t reject non-SQL services.
   - Linking protected databases allowed.
   - Empty chunked body treated as JSON.
   - Import extension not validated.
   - Link default port.
   - Stored `PasswordManaged` in link views.
   - PHP compose resolves env before the site row exists.
   - Terminal env failure fails the terminal.
   - Impact OpenAPI `$ref`.
   - Backup delete server-side confirm.
   - Fake-store concurrency test; SSE test only 3 names; Add Site warning test; daemon wiring test.
   - Windows bridge direct assignment at integration.
5. Run the full gate:
   - `go test ./internal/store ./internal/databases ./internal/servicecontrol ./internal/api ./internal/sites ./internal/tooling ./internal/terminal ./internal/phpfpm ./internal/daemon -count=1`
   - `go test ./... -count=1`, `go vet ./...`, `go build ./...`
   - `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./internal/daemon ./internal/terminal ./internal/backend/...`
   - `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/backend`
6. Commit each logical fix; update `.superpowers/lane-report.md`; report the new HEAD SHA and gate results. Do not merge/push/rebase.

## Last known gate

The last green gate was before this fix round. The uncommitted I-4 changes have **not** been compiled or gated; the missing `uninstallResponse` type is the immediate compile blocker.
