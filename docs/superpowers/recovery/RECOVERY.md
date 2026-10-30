# Lane D recovery — interrupted site dashboard fixer

- **Session:** `ses_e719a01ddffeyoDt8AE42k2TZG`
- **Outcome:** failed — provider quota `429 GoUsageLimitError` (`retryAfterMs` 781000)
- **Branch:** `lane/db-ui-site`
- **HEAD at interruption:** `642623422271e4f206c406fb6bc5ddb18fcb47b0` plus uncommitted WIP
- **Base:** `44c9d6812439a27c4e3ba060e364a197a781e929`
- **Full transcript:** `ses_e719a01ddffeyoDt8AE42k2TZG.md` (readable) and `ses_e719a01ddffeyoDt8AE42k2TZG.json.gz` (raw)

## What is committed

No new commits since `6426234` in this fix round. The previous round’s fixes are all committed through `6426234` (external-password removal, mock links, URL sanitization, primary links, shared users, services-load handling, conflict detection, tests).

## Uncommitted WIP (must not be lost)

This is a **partial implementation of Important 1** (managed user with `password_managed:false`):

- `ui/src/lib/components/site/databasePickerTypes.ts`: added `userManaged: boolean` to `DatabasePickerSummary`.
- `ui/src/lib/components/site/DatabasePicker.svelte`: `sync()` sets `userManaged`; the metadata-only note now distinguishes a managed user with an unknown password (“Reset it after creation…”) from a genuinely external user (“External password entry is not available in v1…”).
- `ui/src/lib/components/site/DatabaseReviewRow.svelte`: shows “Password unknown. Reset it from the site's Databases card after creation.” when `summary.userManaged`.
- `ui/src/lib/components/site/SiteDatabaseCard.svelte`: renamed `isManaged` → `passwordStored`; renders `SecretField` only when a password is stored; renders the Reset button whenever `isManagedUser(link)` is true, including managed users with an unknown password; adds “Replaces the current password.” helper text.
- `ui/src/routes/AddSite.svelte`: added `let createdUserManaged = $state(false);` — **not used yet**.

The last action was adding `createdUserManaged`; the session died immediately after.

## Exact next steps

1. Finish **Important 1**:
   - Capture the submitted `databaseRequest` and `databaseSummary` before `await api.createSite`.
   - Set `createdUserManaged` when opening the post-create modal: `Boolean(databaseSummary?.userManaged || databaseRequest?.new_user)`.
   - In the post-create modal, branch:
     - `createdLink.password_managed` → reveal flow;
     - `createdUserManaged` → “Password unknown. Reset it from the site's Databases card.”;
     - else → external/metadata-only copy.
   - Add tests for `managed:true, password_managed:false` in Site Detail, the Add Site review, and the post-create modal.
2. Fix **Important 2**: `LinkDatabaseDialog.svelte:46-49` `$effect` reads `validation` and clears it immediately. Use `untrack` or a previous-value snapshot that tracks only `value`; add an invalid-submission test asserting the message stays visible.
3. Fix the remaining Minors from the D re-audit:
   - Reject protected names for **new** database/user names in Add Site and the link dialog.
   - Harden `sanitizeDatabaseUrl`: parse the authority up to `/`, `?`, or `#`; handle scheme-less `user:pass@host`.
   - Broaden `isSecretEnvKey` for `PASSPHRASE`, `PASSCODE`, `DSN`.
   - Guard `resetPassword` with the reveal generation counter; always clear `revealLoading`; clear `managedOverrides` when `password_managed` changes.
   - Post-create link matching: no `candidates[0]` fallback; show a “could not load” toast and fix the `blog` fixture.
   - Capture `dbRequest` before submit (also needed for Important 1).
   - Disable link or pass `primary:false` when links failed to load.
   - Picker retry must not reset user-entered names; add a `loadServices` generation guard.
   - Label “Rotate password” (or keep “Reset password” with the new helper text).
   - Mock: update `database.linked_sites`/`user.linked_sites`; `mock.deleteSite` removes links.
   - Add the missing tests: full `.env` equality, metadata-only `DATABASE_URL` omission, enabled DB request cleared by a services failure, password-bearing Add Site review fixture.
4. Run the full gate from `ui/`:
   - `npm run check` (0 errors, 0 warnings)
   - focused Vitest for the changed files
   - `npm test -- --run`
   - `npm run build`
   - `git status --short` clean
5. Commit each logical fix; update `.superpowers/lane-report.md`; report the new HEAD SHA and gate results. Do not merge/push/rebase.

## Last known gate

The previous round ended green at `6426234` (212 tests). The uncommitted Important 1 changes have **not** been checked or tested.
