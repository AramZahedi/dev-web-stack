# Lane E final audit — PASS

- **Audit session:** `ses_e71070964ffeI50fd2z7Ale13Q`
- **Branch:** `lane/db-cli`
- **Range audited:** `44c9d6812439a27c4e3ba060e364a197a781e929..bb063ed6b6ada3b3049d4cbd813bc2869830320f` (code at `80a41fcedce71b334d46bf6c136634ca7d2b05bf`; `bb063ed` is recovery docs only)
- **Date:** 2026-10-31

## Verdicts

| Dimension | Verdict |
|---|---|
| Spec compliance | **PASS** — the planned CLI surface is present; all ten fixes and the core invariants hold; the deferred password paths are honest, early, secret-free, and documented. |
| Code quality | **PASS** — no Critical or Important findings; tests exercise real behavior; Windows and WSL/Linux gates are green; tree clean. |

**The lane is clean for merge.** The Minor observations below are optional polish and do not block merge.

## Fix verification

| # | Fix | Result | Evidence |
|---|---|---|---|
| 1 | `decodeOne`/`decodeStrict` reject `null`, `{}`, empty wrapped objects, and extra wrapper keys; valid resources still decode | PASS | `internal/client/databases.go:296-343`; `internal/client/databases_test.go:78-117` |
| 2 | Underscore secrets and DSN userinfo are redacted; realistic `p@ssw0rd` fixture | PASS | `internal/cli/db.go:23-30,1301-1374`; `db_test.go:413-435`; `site_db_test.go:278-330` |
| 3 | `--privilege` without `--grant` returns `INVALID_FLAGS` | PASS | `internal/cli/db.go:1117-1123`; `db_test.go:92-102` |
| 4 | `backup.Error`/`job.Message` redacted in JSON and table output | PASS | `internal/cli/db.go:543-553,1267-1299`; `db_test.go:272-316` |
| 5 | Actionable remediations for the seven database error codes | PASS | `internal/cli/root.go:333-346`; `cli_test.go:2062-2095` |
| 6 | Windows protected DACL (current user + SYSTEM), Unix 0600, `SE_DACL_PROTECTED` test | PASS | `internal/tooling/wsl_env_windows.go:16-41`; `wsl_env_other.go:8-10`; `wsl_env_windows_test.go:11-36` |
| 7 | Dead `readPasswordLine` removed | PASS | `050807d`; no code references remain |
| 8 | Missing tests added (access-level/custom grant, backup delete, explicit false, Redis link, realistic secret) | PASS | `db_test.go:116-165`; `site_db_test.go:376-402` |
| 9 | `DATABASE_NOT_FOUND` degrades `siteShellContext`; unexpected errors propagate | PASS | `internal/cli/tooling.go:377-386`; `site_db_test.go:433-472` |
| 10 | WSL gate includes `./internal/tooling`; lane report corrected | PASS | `.superpowers/lane-report.md:70,74`; WSL run included `./internal/tooling` |

## Core invariants

- **No password or `DATABASE_URL` in Windows/WSL argv or script text:** PASS. `BuildWSLExecArgs` rejects secret keys (`internal/tooling/windows.go:443-496`); `execWSLSite` uses the env-file transport (`internal/cli/tooling.go:483-501`); argv-capture tests assert secrets are absent.
- **Temp env file path-only, sourced, trapped/cleaned, 0600/DACL:** PASS. `NewWSLEnvFile` applies Unix 0600 / Windows protected DACL (`internal/tooling/windows.go:354-392`); the script sources, removes, and traps (`397-437`); a real bash test verifies source and removal.
- **External-password deferral:** PASS. All three `--password-stdin` paths return `DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED` before `clientReady` with valid flags, never read stdin, and never call the API (`internal/cli/db.go:858-863,1020-1027`; `internal/cli/site_db.go:78-83`; `internal/cli/site.go:297-306`).
- **`db user password --set`:** PASS. Explicit `DATABASE_PASSWORD_SET_UNSUPPORTED` before `clientReady`, no stdin read, no API (`internal/cli/db.go:398-408`); `--rotate` and default rotation work (`409-447`).
- **`db password` rejects non-Redis services:** PASS (`internal/cli/db.go:948-954`; test `db_test.go:437-447`).
- **No secret in list/JSON output except explicit reveal:** PASS. Lists render only password state; link/backup/job output is sanitized; reveal paths are explicit (`internal/cli/db.go:543-553,1301-1398`).

## Gate results

| Command | Result |
|---|---|
| `go test ./internal/cli ./internal/client ./internal/tooling -count=1` (Windows) | PASS |
| `go test ./... -count=1` | PASS |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| WSL/Linux `go test ./internal/cli ./internal/client ./internal/tooling -count=1` | PASS (Ubuntu WSL, Go 1.26.8) |
| `gofmt -l internal/cli internal/client internal/tooling` | Clean |
| `git diff --check` | Clean |
| `git status --porcelain` | Clean |

Note: the `devstack` WSL distro has no Go on PATH; the Linux gate was run in the Go-equipped `Ubuntu` WSL distro, matching the lane report's Go 1.26.8 environment.

## Minor observations (non-blocking)

1. **Flag-specific errors can be preempted by required-flag validation.** With required flags present, the stable deferred/flag-specific codes are returned before `clientReady`; if a required flag is missing, `INVALID_ARGUMENT` wins first. The security invariant still holds (no stdin read, no API call). Optional: validate the deferred condition first or document that required flags win.
2. **Lane report documentation drift.** The report's HEAD field now lists both code HEAD `80a41fc` and branch HEAD `bb063ed`; the audit status and residual-risk sections now record this clean final audit.
3. **Windows DACL test asserts ACE count, not SID identity.** `internal/tooling/wsl_env_windows_test.go:34` verifies `SE_DACL_PROTECTED` and `AceCount == 2`, but not that the ACEs are exactly SYSTEM and the current user. `ipc.PipeSDDL` is separately tested for SID composition.
4. **Pattern-based redaction remains best-effort for exotic server-supplied text.** Realistic `p@ssw0rd`, URL/DSN userinfo, query parameters, and `password=` forms are covered. Structured error redaction would be defense-in-depth only; the server must not put secrets in error text.

## Declined to judge

Live daemon/agent/engine integration, server-side identifier validation and grant SQL, dashboard behavior, and the controller-deferred user-supplied password transport are outside this lane's audit scope. The audit judged only that the deferral errors are honest, early, secret-free, and documented, which passes.

## Outcome

No code changes were made by this audit. The lane remains at `bb063ed` (code `80a41fc`) and is ready for the controller's merge phase.
