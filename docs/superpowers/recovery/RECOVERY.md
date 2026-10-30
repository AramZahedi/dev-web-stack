# Lane C recovery — interrupted service dashboard fixer

- **Session:** `ses_e719a01dfffegm5gvPgEfSBPby`
- **Outcome:** failed — provider quota `429 GoUsageLimitError` (`retryAfterMs` 768000)
- **Branch:** `lane/db-ui-service`
- **HEAD at interruption:** `1a4adc6f47f206586030e86637391434bf0e5c93` plus uncommitted WIP
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **Full transcript:** `ses_e719a01dfffegm5gvPgEfSBPby.md` (readable) and `ses_e719a01dfffegm5gvPgEfSBPby.json.gz` (raw)

## What is committed

Commits made after the previous audit (`045c3dc`):

| Commit | Finding | Summary |
|---|---|---|
| `44d7214` | Critical 1 | Block rename Enter submissions while linked sites are running. |
| `b22d4da` | Important 2 | Toast terminal database jobs discovered by polling. |
| `e29f036` | Important 3 | Disable backup and export for protected databases. |
| `5cd165e` | Important 4 | Exclude protected database grants from edits and payloads. |
| `f761ed9` | Important 5 | Fail closed on overwrite restore when inventory is unavailable. |
| `491903f` | Important 6 | Treat `stopping` sites as not stopped in destructive gates. |
| `8d3b624` | Important 7 | Persist incomplete restore recovery across reloads (`sessionStorage`). |
| `3d0842d` | Important 8 | Always create a pre-import backup for replace imports. |
| `da29d81` | Important 10 | Assert destructive restore/import payloads end to end. |
| `be967ee` | Minors | Keep active jobs and inventory failures visible in the deactivate warning. |
| `1a4adc6` | Minor | Render the service header before inventory calls finish. |

## Uncommitted WIP (must not be lost)

`ui/src/lib/components/service/RedisTab.test.ts` adds two tests for the Redis reveal-refresh minor:

1. `clears a revealed default password when the user inventory refreshes`
2. `keeps a freshly reset password visible through its own refresh`

The first test is **RED** (the revealed secret is not cleared on refresh). The implementation has not been written yet. The session died immediately after the RED run.

The intended implementation (from the transcript reasoning):

- Add `let keepRevealedOnce = false;`
- Add an `$effect` that reads `users` and clears `revealed`, `revealedVisible`, and `revealError`; if `keepRevealedOnce` is true, reset it and skip the clear once.
- Set `keepRevealedOnce = true` in `resetDefault` before `onRefresh()` so the freshly reset secret survives its own refresh.
- Combine loading: `SecretField loading={revealLoading || resetLoading}`; disable Reset while `revealLoading`.
- Add/keep tests for the reset-preserve path.

## Exact next steps

1. Implement the RedisTab reveal-refresh minor above; make both tests green; commit.
2. Work through the remaining Minor findings from the C re-audit:
   - Deactivate warning under-reports when inventory failed (partially addressed by `be967ee`; verify).
   - Deactivate warning only sees the active tab’s job.
   - Restart prompt is session-local (persist or clearly label).
   - Terminal new job can leave a previous poll timer running.
   - Backup fallback options do not filter missing databases.
   - `confirmDrop` lacks a function-level gate re-check.
   - Reset dialog “Reveal” after Hide is not a new audited reveal (document).
   - Deactivate dialog persists across service change.
   - Initial skeleton waits for all inventory calls (partially addressed by `1a4adc6`).
3. Run the full gate from `ui/`:
   - `npm run check` (0 errors, 0 warnings)
   - focused Vitest for the changed files
   - `npm test -- --run`
   - `npm run build`
   - `git status --short` clean
4. Commit each logical fix; update `.superpowers/lane-report.md`; report the new HEAD SHA and gate results. Do not merge/push/rebase.

## Last known gate

`npm run check` passed at the last run; the focused RedisTab RED test failed as expected. The full suite has not been run since the uncommitted test was added.
