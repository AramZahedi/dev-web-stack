# Windows 10/11 + WSL2 Implementation — Post-Completion Review Findings

**Target Project:** `dev-web-stack`
**Reviewed Commit:** `6d7bdaa` (`master`, tag `v0.2.0`, clean working tree, in sync with `origin/master`)
**Review Date:** 2026-10-28
**Review Scope:** In-depth verification of the declared-finished Windows 10/11 + WSL2 implementation: real gate runs on Windows and in WSL2 ext4, live probing of the installed instance on the review machine, and source-level audit of installer, daemon, API, release, and test paths.
**Policy Notice:** Review was read-only. No tracked files were modified. This document is the deliverable; it is written for a follow-up agent that will **verify and fix** each finding.

> **Line-number caveat:** all `file:line` references are accurate at commit `6d7bdaa`. They will drift as fixes land — re-grep before editing.

---


# Round 3 — Additional Findings (2026-10-28, remediation in progress)

**Scope:** Re-audit after observing remediation changes; identify remaining issues.

**Gates run in round 3:**
| Gate | Result |
|---|---|
| `go build ./...` | PASS |
| `go test ./internal/api/... ./internal/proxy/... ./internal/sites/... ./internal/daemon/... ./internal/doctor/...` | PASS |

## Round-3 findings summary

| Severity | IDs | Areas |
|---|---|---|
| **High** | R3-H9 | NTFS 8.3 short name bypass still present (unfixed) |
| **Medium** | R3-M13 | Uninstaller hosts regex still broad + BOM under PS 5.1 |
| **Medium** | R3-M14 | Installer DEVSTACK_TEST_* escapes not fully gated |
| **Low** | R3-L4 | Log redaction gaps remain |

---


## R3-H9 — NTFS 8.3 short names still bypass dotfile denylist (UNFIXED)

**Status:** Confirmed unfixed. `internal/proxy/php.go` still only denies dotfile segments from the raw request path and does not re-check after `filepath.EvalSymlinks`. On NTFS with 8.3 enabled, `/ENV~1` resolves to `/.env` after symlink evaluation and passes containment — served. This is R2-H8; remediation did not touch proxy/php.go.

**Impact:** High — credential disclosure on Windows-hosted PHP sites.

**Fix:** After resolving candidates with `filepath.EvalSymlinks`, re-apply segment denylist against the resolved relative path components (deny if any resolved segment starts with `.` or matches backup suffixes). Add Windows short-name regression tests.

**Acceptance:** `GET /ENV~1` and `GET /GIT~1/config` return 404 on Windows-hosted sites.


## R3-M13 — Uninstaller hosts cleanup issues remain

**Status:** `uninstall.ps1` still uses regex matching `(?i)^#\s*BEGIN\s+devstack` / `(?i)^#\s*END\s+devstack` (broader than exact match) and rewrites with BOM-prone UTF-8 encoding under PS 5.1.

**Fix:** Match exact trimmed markers `# BEGIN devstack` / `# END devstack`, write with UTF8 without BOM. Remove only exact block.

**Acceptance:** No overreach, no BOM introduced.


## R3-M14 — Installer DEVSTACK_TEST_* escapes

**Status:** Some test escapes remain unconditionally honored in non-sandbox paths.

**Fix:** Gate all DEVSTACK_TEST_* behind explicit sandbox/test flag and log when overrides active.

**Acceptance:** No test escapes honored in normal installs.


## R3-L4 — Log redaction pattern gaps

**Status:** Still only matches names containing `key|token|secret|password`. `DB_PASS`, `MYSQL_PASS`, `PGPASSWORD` etc not caught.

**Fix:** Expand pattern to include common abbreviations or explicit list.

**Acceptance:** Common DB password env vars redacted.


**Verdict update:** Remediation has addressed critical issues (C1 boundary improved, C2 wired, C3 upgrade logic improved, C4 reboot handling improved, firewall scoping improved). R3-H9 remains a high severity unfixed issue. Overall still not 100% bullet-proof.
