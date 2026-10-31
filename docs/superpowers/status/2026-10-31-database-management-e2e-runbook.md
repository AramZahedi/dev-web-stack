# Database management E2E - subagent runbook

- **Purpose:** run the exhaustive E2E plan in fresh subagent contexts so the main session does not grow with full test transcripts.
- **Plan:** `docs/superpowers/status/2026-10-31-database-management-e2e-plan.md`
- **Model for every subagent:** `opencode-go/deepseek-v4.1-flash#max`
- **Status:** cycles 0 (readiness), 1 (harness/fixtures), 2 (Playwright specs), 3 (Windows read-only), 4 (Linux core), 5 (Linux sites), 6 (Linux backup/import), 7 (Linux Redis/phpMyAdmin) and 8 (dashboard mock) done. **Paused after cycle 8 by user instruction; cycles 9-15 remain pending.** No destructive real-install scenario has been started.
- **Main-session rule:** one batch = one spawn, one close. The subagent returns a short summary, evidence paths, and a status update; the main session records it and spawns the next batch.

## Hard constraints for every subagent

1. Read the plan and this runbook before acting.
2. Do not make destructive changes to the real install `C:\Users\Aram\.devstack`, its helper service, its WSL agent, or its live MySQL/phpMyAdmin data.
3. Destructive database/site/backup tests run only in a disposable Linux environment or a rehearsed disposable Windows handover.
4. Never print or persist a password, DSN, secret file body, install token, or GitHub token. Redact evidence.
5. Use non-interactive commands. If a step needs human approval, a GUI prompt, or a decision, record `BLOCKED` and continue with the next safe step.
6. Do not ask the user questions; the user is AFK.
7. Update `docs/superpowers/status/2026-10-31-database-management-e2e-runbook.md` status for the batch and append details to `.superpowers/e2e-progress.md`.
8. Return a concise summary: batch ID, status, what changed, commands/evidence paths, failures/blockers, and the recommended next batch.

## Bug-fix rule for every subagent

Every subagent must fix, test, and commit any bug or issue it finds in the
area it is testing, instead of only reporting it.

1. Reproduce it with a failing test or a minimal command.
2. Fix the root cause, not the test.
3. Add or update a regression test.
4. Run the focused test plus the nearest broader gate.
5. Commit with a conventional message (`fix(scope): ...`) that names the bug.
6. Push the commit if the remote is reachable. If the push fails, record the
   exact error and leave the commit local; do not rewrite history.
7. Record the commit SHA, test evidence, and any residual risk in the runbook
   and `.superpowers/e2e-progress.md`.

If a finding cannot be fixed safely without a design decision or a destructive
real-install change, the subagent must record it as `BLOCKED` with the exact
reason, commit any safe partial fix, and continue with the next safe step. It
must not hide, downgrade, or defer a finding without a ledger entry.

## Release and shutdown rule

Release is a separate final batch after every E2E batch is `done` or accepted
as `BLOCKED` with a residual-risk entry.

1. Confirm the working tree is clean and `master` is pushed.
2. Use **v0.4.0** for the database-management feature unless the final gate
   shows a breaking change that requires a different semver.
3. Run the full release gate: `go test ./... -race -count=1`, `go vet ./...`,
   UI `npm run check` / `npm test` / `npm run build`, `make release-test`, and
   `make release-windows-test`.
4. Build Linux and Windows amd64 artifacts from the final commit.
5. Create and push annotated tag `v0.4.0`; run the manual GitHub Actions
   release workflow with that tag.
6. Upload the Windows assets and `install.ps1` to the same release; replace
   `checksums.txt` with a combined manifest covering every asset.
7. Re-download every asset and verify its SHA-256 through the GitHub API.
8. Update the release body with the feature summary, E2E evidence, and commit
   SHA. Verify the release is public, non-draft, non-prerelease, and complete.

If any release step fails, do not shut down. Record the failure, leave the
machine on, and report what remains. If release verification passes, run
`shutdown.exe /s /t 60` as the final action after the final report is pushed.

## Batch queue and cycle count

Recommended batched run: **16 spawn/close cycles** if the retry batch is needed (15 E2E batches plus one release batch); **15** if no retry is needed (14 E2E batches plus release). One subagent per E2E scenario would be **33-35 cycles**; one-per-suite is unnecessary overhead.

| Cycle | Batch | Scope | Environment | Destructive? | Status |
|---|---|---|---|---|---|
| 0 | Readiness setup | R01-R04, R11-R12: tools, Docker/images, variables, dry-runs | WIN/LIN | No real-install mutation | done |
| 1 | Harness and fixtures | R05, R07, R08: fixtures, seed/verify scripts, tracker, secret scanner, cleanup/rollback, isolation model | WIN/LIN | No real-install mutation | done |
| 2 | Playwright specs | R06: database-management specs, live-dashboard config, browser install | UI | No | done |
| 3 | Windows read-only | E2E-00, E2E-09 read-only, E2E-12 read-only, E2E-20 real-install login | WIN-REAL read-only | No | done (denied-reveal audit fix `9347f25`; hosts repair deferred; live re-check after upgrade) |
| 4 | Linux core | E2E-01, E2E-02, E2E-16 | LIN disposable | Yes, disposable | done (E2E-01 BLOCKED: real install owns 3306/8082; E2E-02/16 PASS) |
| 5 | Linux sites | E2E-03, E2E-04, E2E-05, E2E-06, E2E-17 | LIN disposable | Yes, disposable | done (E2E-03/04/05/17 PASS; E2E-06 guard PASS with product 3306 activation BLOCKED; 4 fixes; secret scan clean; real phpMyAdmin 8082 timeout is a controller blocker) |
| 6 | Backup/import | E2E-07, E2E-18 | LIN disposable | Yes, disposable | done (E2E-07 130 checks PASS; E2E-18 115 checks PASS; 3 product fixes; secret scan clean; real phpMyAdmin 8082 timeout is a controller blocker) |
| 7 | Redis/phpMyAdmin | E2E-08, E2E-19, E2E-20 Linux | LIN disposable | Yes, disposable | done (E2E-08 59 checks, E2E-19 58, E2E-20 89 PASS; 1 helper fix `b919ee8`; secret scan clean; fixed-port sub-step BLOCKED) |
| 8 | Dashboard mock | E2E-10, E2E-21 mock | UI-MOCK | No | done (186/186 across Chromium/Firefox/WebKit; 2 UI fixes `b1f9828`; secret scan clean) |
| 9 | Dashboard live | E2E-10, E2E-21 live | UI-LIVE | Read-only/limited | pending |
| 10 | Failure injection | E2E-23 | LIN disposable | Yes, disposable | pending |
| 11 | Scale/performance | E2E-24 | LIN disposable | Yes, disposable | pending |
| 12 | Parity/regression/audit | E2E-25, E2E-26, E2E-27 | WIN/LIN/UI | Mixed, disposable | pending |
| 13 | Closeout | E2E-28: cleanup, results, residual risks, evidence index | all | Cleanup only | pending |
| 14 | Retry/fix verification | Failed P0/P1 from cycles 0-13 | as needed | as needed | pending |
| 15 | Release | Full gate, build Linux/Windows, tag, publish, verify assets | WIN/LIN/UI | No real-install mutation | pending |

## Pause state after cycle 8

- **Paused:** 2026-10-31, by user instruction. Do not spawn a new subagent
  until the user explicitly resumes the run.
- **Last verified HEAD:** `5462e94` (`docs(e2e): close cycle 8 and record pause
  state`), clean and equal to `origin/master`. No unmerged branches or extra
  worktrees.
- **Real install:** MySQL 8.4.13 active; phpMyAdmin 5.2.3 active; both
  loopback-only. `http://127.0.0.1:8082/` returns HTTP 200 after the controller
  restarted only the `devstack` Docker daemon with `live-restore`.
- **Completed:** cycles 0-8; E2E-00, 02-10, 12, 16-21 have passing evidence;
  E2E-01 and the fixed-port sub-steps of E2E-06/E2E-20 are `BLOCKED` because
  the real install owns 3306/8082.
- **Pending:** cycle 9 live dashboard, 10 failure injection, 11 scale/performance,
  12 parity/regression/audit, 13 closeout, 14 retry/fix verification, and 15
  release. Release and shutdown have not started; the machine stays on.

## Safe subset while approval is unavailable

If a batch would require a destructive real-install change and no disposable environment is ready, mark it `BLOCKED` and skip. The safe autonomous subset is cycles 0, 1, 2, 3, 8, and 13 (six E2E cycles). Cycles 4-7, 10-12 can run once a disposable Linux environment is ready; cycle 9 only if the live dashboard is read-only; cycle 14 only for safe retries. Cycle 15 (release) runs only after the P0 gate passes or every P0 failure is explicitly accepted as a residual risk; otherwise it records `BLOCKED` and the machine stays on.

## Resume procedure after a context loss

1. Read this runbook and `docs/superpowers/status/2026-10-31-database-management-e2e-plan.md`.
2. Read `.superpowers/e2e-progress.md` if it exists.
3. Find the first `pending` or `in_progress` batch.
4. Spawn exactly one subagent for that batch with the model above.
5. When it returns, update its status row, record the summary, and spawn the next batch.
6. Never spawn two batches that mutate the same Docker engine/ports at the same time.
7. After cycle 15 passes, verify the GitHub release and only then schedule shutdown.

## Progress ledger

| Cycle | Started | Finished | Result | Evidence | Notes |
|---|---|---|---|---|---|
| 0 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/readiness/cycle-0/` | R01-R04 and R11 ready; R12 tool smokes done, harness dry-runs deferred to cycle 1; real install untouched. |
| 1 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/readiness/cycle-1/`, `dist/e2e/database-management/evidence/20261031t011321z-2677/HARNESS-SMOKE/` | R05/R07/R08 harness built; MySQL 8.4.13 + phpMyAdmin 5.2.3 pulled in Ubuntu engine; R12 harness smoke PASS at harness commit `4bc2e11`; real install untouched. Windows destructive DB tests remain BLOCKED on WSL `/data/devstack` isolation. |
| 2 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/readiness/cycle-2/` | R06 complete: 17 database-management mock tests + 32 existing dashboard tests pass on Chromium/Firefox/WebKit (147/147); live config/spec added and skipped by default; full live auth BLOCKED on auth-disabled real install/no test token. |
| 3 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/readiness/cycle-3/` | E2E-00/09/12/20 read-only PASS; phpMyAdmin 5.2.3 login/logout/restart PASS; denied-reveal audit gap fixed in `9347f25`, recorded in `d2cfa73`; final doctor hosts check failed (empty managed block, known limitation), repair deferred; live re-check pending upgrade. |
| 4 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/evidence/20261031t200000z/` | E2E-02 (97 checks) and E2E-16 (82 checks) PASS; E2E-01 BLOCKED on fixed 3306/8082 with equivalent no-port lifecycle verified; 8 fixes/test commits; final secret scan clean. |
| 5 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/evidence/20261031t223000z/`, `dist/e2e/database-management/evidence/20261031t230000z/C5-UI/` | E2E-03 (40 checks), E2E-04 (33), E2E-05 (40), E2E-06 (19, product 3306 activation BLOCKED), E2E-17 (58) PASS; live dashboard 4/4 PASS; final secret scan clean; real phpMyAdmin 8082 timeout is a controller blocker. Commits `0c982ad`, `0d9f7ac`, `6a0ca66`, `b6e2e9f`. |
| 6 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/evidence/20261101t150000z/E2E-07/`, `dist/e2e/database-management/evidence/20261101t100000z/E2E-18/` | E2E-07 130/130 PASS and E2E-18 115/115 PASS; manual/pre-op backup kinds, stopped-service list/download/delete, overwrite restore with pre-restore backup, linked-site gate, gzip export file/stdout, merge/replace import with pre-import backup, staging 0600/cleanup, invalid/truncated dumps, cancel and daemon-restart reconciliation, Redis unsupported, near-1 GiB and over-1 GiB, bounded tmpfs disk-full, complex SQL, concurrency; 3 product fixes (`b9053c2`, `eaf7626`, `1b906ac`); final secret scan clean; real phpMyAdmin 8082 timeout remains a controller blocker. |
| 7 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/evidence/20261101t230000z/E2E-08/`, `.../E2E-19/`, `.../E2E-20/` | E2E-08 59/59, E2E-19 58/58, E2E-20 89/89 PASS; Redis ACL mode/foreign/rollback/duplicate fixes in `b919ee8`; test/scripts `6c3cb98`; live Redis tabs and phpMyAdmin Playwright matrix PASS; secret scan clean; fixed 3306/8082 activation BLOCKED with no-port/high-port equivalent. |
| 8 | 2026-10-31 | 2026-10-31 | done | `dist/e2e/database-management/evidence/20261031T082709Z/` | E2E-10/E2E-21 mock matrix PASS 186/186 (62 per browser); 30 database-management + 32 existing dashboard tests per browser; two UI fixes (`b1f9828`): persisted active-job resume after refresh and Redis ACL error inside the dialog; test/docs `125ad25`; secret scan clean; live dashboard remains cycle 9. |
| 9 | - | - | pending | - | - |
| 10 | - | - | pending | - | - |
| 11 | - | - | pending | - | - |
| 12 | - | - | pending | - | - |
| 13 | - | - | pending | - | - |
| 14 | - | - | pending | - | - |
| 15 | - | - | pending | - | - |

**Final pushed HEAD (cycle 8):** `31b89f7` (`docs(e2e): record cycle 8
head`). Product fix `b1f9828`; test/docs `125ad25`.

**Controller blocker (cycle 5, still open in cycles 6-7):** the real phpMyAdmin
at `http://127.0.0.1:8082/` timed out before cycle 5, after cycle 5, after
cycle 6, and again after cycle 7 terminated the disposable Ubuntu distro. A
`127.0.0.1:8082` listener still exists but does not answer within 10-15 s. No
real-install object was modified. The controller should restore the real WSL
port-forward/relay state; cycles 5-7 evidence is complete and every disposable
environment is clean.
