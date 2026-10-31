# Database management E2E - subagent runbook

- **Purpose:** run the exhaustive E2E plan in fresh subagent contexts so the main session does not grow with full test transcripts.
- **Plan:** `docs/superpowers/status/2026-10-31-database-management-e2e-plan.md`
- **Model for every subagent:** `opencode-go/deepseek-v4.1-flash#max`
- **Status:** readiness batch pending. No live E2E has been started.
- **Main-session rule:** one batch = one spawn, one close. The subagent returns a short summary, evidence paths, and a status update; the main session records it and spawns the next batch.

## Hard constraints for every subagent

1. Read the plan and this runbook before acting.
2. Do not make destructive changes to the real install `C:\Users\Aram\.devstack`, its helper service, its WSL agent, or its live MySQL/phpMyAdmin data.
3. Destructive database/site/backup tests run only in a disposable Linux environment or a rehearsed disposable Windows handover.
4. Never print or persist a password, DSN, secret file body, or install token. Redact evidence.
5. Use non-interactive commands. If a step needs human approval, a GUI prompt, or a decision, record `BLOCKED` and continue with the next safe step.
6. Do not ask the user questions; the user is AFK.
7. Update `docs/superpowers/status/2026-10-31-database-management-e2e-runbook.md` status for the batch and append details to `.superpowers/e2e-progress.md`.
8. Return a concise summary: batch ID, status, what changed, commands/evidence paths, failures/blockers, and the recommended next batch.

## Batch queue and cycle count

Recommended batched run: **15 spawn/close cycles** (14 if no retry is needed). One subagent per E2E scenario would be **33-35 cycles**; one-per-suite is unnecessary overhead.

| Cycle | Batch | Scope | Environment | Destructive? | Status |
|---|---|---|---|---|---|
| 0 | Readiness setup | R01-R04, R11-R12: tools, Docker/images, variables, dry-runs | WIN/LIN | No real-install mutation | pending |
| 1 | Harness and fixtures | R05, R07, R08: fixtures, seed/verify scripts, tracker, secret scanner, cleanup/rollback, isolation model | WIN/LIN | No real-install mutation | pending |
| 2 | Playwright specs | R06: database-management specs, live-dashboard config, browser install | UI | No | pending |
| 3 | Windows read-only | E2E-00, E2E-09 read-only, E2E-12 read-only, E2E-20 real-install login | WIN-REAL read-only | No | pending |
| 4 | Linux core | E2E-01, E2E-02, E2E-16 | LIN disposable | Yes, disposable | pending |
| 5 | Linux sites | E2E-03, E2E-04, E2E-05, E2E-06, E2E-17 | LIN disposable | Yes, disposable | pending |
| 6 | Backup/import | E2E-07, E2E-18 | LIN disposable | Yes, disposable | pending |
| 7 | Redis/phpMyAdmin | E2E-08, E2E-19, E2E-20 Linux | LIN disposable | Yes, disposable | pending |
| 8 | Dashboard mock | E2E-10, E2E-21 mock | UI-MOCK | No | pending |
| 9 | Dashboard live | E2E-10, E2E-21 live | UI-LIVE | Read-only/limited | pending |
| 10 | Failure injection | E2E-23 | LIN disposable | Yes, disposable | pending |
| 11 | Scale/performance | E2E-24 | LIN disposable | Yes, disposable | pending |
| 12 | Parity/regression/audit | E2E-25, E2E-26, E2E-27 | WIN/LIN/UI | Mixed, disposable | pending |
| 13 | Closeout | E2E-28: cleanup, results, residual risks, evidence index | all | Cleanup only | pending |
| 14 | Retry/fix verification | Failed P0/P1 from cycles 0-13 | as needed | as needed | pending |

## Safe subset while approval is unavailable

If a batch would require a destructive real-install change and no disposable environment is ready, mark it `BLOCKED` and skip. The safe autonomous subset is cycles 0, 1, 2, 3, 8, and 13 (six cycles). Cycles 4-7, 10-12 can run once a disposable Linux environment is ready; cycle 9 only if the live dashboard is read-only; cycle 14 only for safe retries.

## Resume procedure after a context loss

1. Read this runbook and `docs/superpowers/status/2026-10-31-database-management-e2e-plan.md`.
2. Read `.superpowers/e2e-progress.md` if it exists.
3. Find the first `pending` or `in_progress` batch.
4. Spawn exactly one subagent for that batch with the model above.
5. When it returns, update its status row, record the summary, and spawn the next batch.
6. Never spawn two batches that mutate the same Docker engine/ports at the same time.

## Progress ledger

| Cycle | Started | Finished | Result | Evidence | Notes |
|---|---|---|---|---|---|
| 0 | - | - | pending | - | - |
| 1 | - | - | pending | - | - |
| 2 | - | - | pending | - | - |
| 3 | - | - | pending | - | - |
| 4 | - | - | pending | - | - |
| 5 | - | - | pending | - | - |
| 6 | - | - | pending | - | - |
| 7 | - | - | pending | - | - |
| 8 | - | - | pending | - | - |
| 9 | - | - | pending | - | - |
| 10 | - | - | pending | - | - |
| 11 | - | - | pending | - | - |
| 12 | - | - | pending | - | - |
| 13 | - | - | pending | - | - |
| 14 | - | - | pending | - | - |
