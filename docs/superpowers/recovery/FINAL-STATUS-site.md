# Lane D final status ÔÇö implementation complete, fresh audit not dispatched

- **Branch:** `lane/db-ui-site`
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **HEAD:** `b48d7874a6f9c84e2886032fa9df966331d3e1ef`
- **Worktree:** clean
- **Date:** 2026-10-31

## Completed work

This round resumed the quota-killed session. The WIP is preserved in `20f1137`; the completed fixes are:

| Finding | Commit | Resolution |
|---|---|---|
| Important 1 ÔÇö managed user with unknown password | `6bb3911` | `DatabasePickerSummary.userManaged` distinguishes a managed-unknown user from an external user in the picker and Add Site review. `SiteDatabaseCard` separates `passwordStored` from `isManagedUser` and offers Reset with ÔÇ£Password unknown. Reset it to generate a new password.ÔÇØ Add Site captures the request/summary before `createSite`, sets `createdUserManaged`, and shows the managed-unknown post-create copy. Tests cover Site Detail, review, and post-create. |
| Important 2 ÔÇö self-clearing link validation | `f510de7` | The `$effect` tracks only `value` through a previous-value snapshot; an invalid submission stays visible until the selection changes. Test covers it. |
| Protected new identifiers | `6f1222b` | Add Site validation and `LinkDatabaseDialog.submit` reject protected database/user names for new names. |
| URL/secret sanitizing | `75917de` | `sanitizeDatabaseUrl` parses only the authority (up to `/`, `?`, `#`), handles the last `@` in that slice and scheme-less `user:pass@host`; `isSecretEnvKey` covers `PASS`, `PWD`, `PASSPHRASE`, `PASSCODE`, and `DSN`. |
| Reveal/reset races | `a88a7d0` | `resetPassword` uses the reveal generation counter; `revealLoading` always clears; `managedOverrides` clears when `password_managed` changes. |
| Post-create matching | `72af37b` | No `candidates[0]` fallback; a missing exact link shows ÔÇ£Could not load the created databaseÔÇØ. |
| Link-load failure | `f7939c3` | Link action is disabled while links fail to load; the dialog receives `primary:false` rather than guessing. |
| Picker retry | `b74f4eb` | Retry preserves typed database/user names; `loadServices` has a generation guard. |
| Mock link state | `298d098` | Mock `linked_sites` updates on link/unlink; `mock.deleteSite` removes links. |
| Metadata-only snippets | `f82670a` | Database URL keys are omitted from metadata-only `.env` snippets. |
| Edge-case tests | `a560af1` | Exact `.env` equality, metadata-only URL omission, enabled-request-cleared-by-services-failure, and stored-password review tests. |
| Style | `b48d787` | Trailing blank line removed. |

The reset label remains ÔÇ£Reset passwordÔÇØ with the approved helper text ÔÇ£Replaces the current password.ÔÇØ No listed Minor was deferred.

## Gate (from `ui/`)

| Command | Result |
|---|---|
| `npm run check` | PASS ÔÇö 0 errors, 0 warnings |
| Focused Vitest | PASS ÔÇö 6 files, 113 tests |
| `npm test -- --run` | PASS ÔÇö 22 files, 232 tests |
| `npm run build` | PASS ÔÇö only the existing >500 kB chunk warning |
| `git status --short` | clean |

## Open items

- **Fresh audit not dispatched.** The controller directive forbids spawning further subagents, so this lane has implementation and self-run gates but no independent audit of this fix round.
- The `loadServices` generation guard has no dedicated overlapping-response test.
- Metadata-only snippets omit every database URL key (`DATABASE_URL` and namespaced `*_DATABASE_URL`), slightly broader than the specÔÇÖs `DATABASE_URL`; deliberate defense in depth.
- `DatabaseConnection.env`/`url` are trusted to omit passwords per the HTTP contract; the shared `DatabaseConnectionCard` is foundation-owned and renders the URL it receives.
- The ÔÇ£Manage userÔÇØ and ÔÇ£ActivateÔÇØ links target `#/services/:id`, which Lane C provides; the master plan merges C before D.
- No live E2E was run; live E2E is controller-only and requires freshly built merged binaries.
