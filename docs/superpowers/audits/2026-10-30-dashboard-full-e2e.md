# Full dashboard and CLI E2E audit — 2026-10-30

- **Scope:** every dashboard and CLI action in the binding plan
  [`2026-10-30-dashboard-full-e2e-coverage.md`](../plans/2026-10-30-dashboard-full-e2e-coverage.md), Tasks 0–14.
- **Host:** Windows 10 22H2+ (build 19045+), managed Debian 12 WSL2 distro `devstack`,
  profile home `C:\Users\Aram\.devstack` (`C:\devstack` is the compatibility junction).
- **Final implementation commits:** `3975450` (terminal) and `353fa06` (events);
  Task 13 coverage record `a3eef16`. The report commit is the commit that adds this
  file (`git log -1 -- docs/superpowers/audits/2026-10-30-dashboard-full-e2e.md`).
- **Result:** **PASS** — 15/15 tasks, 19 product findings fixed with
  failing-test-first evidence, review, redeploy, and rerun; all Go/UI/installer/
  lifecycle/release/vertical-slice gates green; baseline restored.

## Executive summary

Every action in the plan was exercised live against the real Windows daemon,
helper, managed WSL agent, Docker services, PHP/Node runtimes, built-in proxy, and
Svelte dashboard. Destructive tasks (service uninstall with data deletion, trust
removal, full uninstall/init, performance reset, site/runtime removal) were
backed up or covered by an explicit restore path, and the final state matches the
Task 0 baseline.

- Sites: none.
- Services: all seven `not_installed`.
- Serving: built-in; native nginx/Apache intentionally unsupported on Windows.
- Node default: `24.22.1`; PHP default: `php@8.4.27-r1`; Composer `2.10.3`.
- Performance: `throughput`, host tuning not applied (disabled on Windows).
- Trust/hosts: healthy; only `devstack.test` in the managed hosts block.
- Ports: 80/443 helper forwarders, 7070 dashboard, 8080/8443 built-in proxy.
- `doctor`: `ok=true`, 0 failures, 0 warnings, 9 expected built-in/vhost skips.
- Fresh dashboard sweep: Sites, Services, Runtimes, Doctor, Settings all render
  with **0 console errors and 0 warnings**.

## Task results

Evidence lives under `dist/e2e/full-dashboard/` (intentionally untracked). Each
task directory contains command output, JSON/API captures, dashboard snapshots,
and where applicable a `PROGRESS.md` with the exact commands and assertions.

| Task | Result | Key commands / assertions | Evidence |
| --- | --- | --- | --- |
| 0 — Preflight, backup, baseline | **PASS** | `doctor --json`; baseline JSON for sites/services/runtimes/defaults/trust/vhost; backup of `state.db`, `config.yaml`, `system/helper.yaml`, hosts, binaries | `task-0/` |
| 1 — Add Site (PHP/WSL modes) | **PASS** | `devstack site add --kind php --source-mode windows|wsl …`; `site add --kind node --command …`; validation errors; four detail pages served expected bodies | `task-1/README.md`, `task-1/*-detail.png`, `task-1/*-body.txt` |
| 2 — PHP config, ini, extensions, preview | **PASS** | Dashboard config/ini/ext save + `site php`, `site ini`, `site ext`, `php --site`, phpinfo; FPM bugs fixed and task rerun | `task-2/PROGRESS.md`, `task-2/site-after-*.json`, `task-2/rerun-*` |
| 3 — Source-mode copy/switch | **PASS** | Windows→WSL and WSL→Windows copy on stopped sites; running-site guard; CLI parity; original source preserved | `task-3/PROGRESS.md`, `task-3/step*` |
| 4 — PHP runtime install/remove/migrate | **PASS** | `runtime install php@8.5.12-r1`; two-site migration 8.4↔8.5; in-use removal protection; remove and cleanup | `task-4/PROGRESS.md`, `task-4/php-available.json`, `task-4/step*` |
| 5 — Services full lifecycle | **PASS** | All seven services install/activate/config/restart/deactivate/uninstall keep-data/reinstall/delete-data; credentials; logs; exclusivity | `task-5/PROGRESS.md`, `task-5/step*` |
| 6 — Settings listener ports | **PASS** | Dashboard port save + restart-required flow; CLI/API parity; invalid/conflict inputs; restore `7070`; fixed Windows 8080/8443 note | `task-6/PROGRESS.md`, `task-6/step*` |
| 7 — Trust repair/remove/restore | **PASS** | `trust status/repair/remove`; dashboard remove/restore; helper-stopped and foreign-policy cases; `init` restore | `task-7/PROGRESS.md`, `task-7/step*` |
| 8 — PHP CLI default, global ini, Composer, tools | **PASS** | `php default` set/clear/fallback; global `php.ini` loaded by shims; Composer update/remove/reinstall; conflicts; final restore | `task-8/PROGRESS.md`, `task-8/step*` |
| 9 — Serving mode | **PASS (Windows scope)** | Builtin verified end-to-end; nginx/Apache switch returns `NOT_SUPPORTED` and leaves builtin active; fixtures/listeners restored | `task-9/PROGRESS.md`, `task-9/step*` |
| 10 — Performance preview/apply/reset/benchmark | **PASS** | Balanced/Throughput/Custom previews; validation; apply with live nginx; reset; host-tuning guard; two benchmarks; cleanup | `task-10/PROGRESS.md`, `task-10/step*` |
| 11 — Doctor repair | **PASS** | Baseline, dashboard Run checks/Repair, CLI repair twice, reversible hosts drift; final green | `task-11/PROGRESS.md`, `task-11/step*` |
| 12 — Remaining CLI groups | **PASS** | Trust, vhost, service, PHP/site ini/ext, runtime migrate, UI token, completion, full uninstall/init, root/help/error paths, daemon lifecycle | `task-12/PROGRESS.md`, `task-12/step*` |
| 13 — SSE/events and terminal edges | **PASS** | Logs live/pause/resume/clear/download/restart/reopen; events matrix + daemon-restart reconnect; terminal env/PHPRC/PATH/resize/Ctrl+C/reconnect/stopped site/idle timeout | `task-13/PROGRESS.md`, `task-13/step*` |
| 14 — Final regression, cleanup, report | **PASS** | Baseline restore, Go/UI/installer/lifecycle/release/vertical-slice gates, live assertions, dashboard sweep, final cleanup | `task-14/`, this report |

## Product findings fixed

Every finding received a failing test first (or a captured failing live state),
a minimal fix, independent review, redeployment, and a task rerun. `PASS` below
means the reviewer found no remaining Critical/Important issue.

| # | Task | Root cause | Tests / evidence | Fix | Review |
| --- | --- | --- | --- | --- | --- |
| 1 | 2 | Applying PHP settings drained old FPM generations through the agent's stop-all branch and killed the newly published master. | `TestAgentPHPStopUnknownGenerationDoesNotStopActiveMaster`, `TestAgentPHPStopDrainedGenerationKeepsDesiredRunning`, `TestAgentPHPStopDefaultAfterHashSwapKeepsSiteRunning`, `TestStopPHPProcessStopsEveryGeneration` | `703eec9` | PASS |
| 2 | 2 | `devstack php --site` re-prepared the active generation and orphaned the live master (status stopped / HTTP 503). | `TestAgentPHPPreparePreservesRunningGeneration` | `367b9cd` | PASS |
| 3 | 4 | Agent PHP install skipped the base-ini `extension_dir` compensation, leaving 8.5 pointed at `/tmp` and doctor warning. | `TestAgentInstallPHPRuntimeWritesCompensatingBaseIni` | `b863496` | PASS |
| 4 | 4 | In-use PHP builds had a disabled Remove button, so the migration modal was unreachable. | UI test `opens migration for an in-use PHP build without a failed removal` | `89d45d3` | PASS |
| 5 | 5 | Agent config apply reported success without `nginx -t`/reload; invalid config could go live. | `TestWebServiceApplyValidatesAndReloadsWhenActive`, `TestWebServiceApplyRollsBackWhenValidationFails` | `9400519` | PASS |
| 6 | 5 | Agent config reset restored the backup without validating/reloading it. | `TestWebServiceResetReloadsRestoredConfigWhenActive`, `TestWebServiceResetKeepsCurrentConfigWhenValidationFails` | `1dc2b16` | PASS |
| 7 | 6 | Windows startup did not mirror applied listener ports into the store, so `restart_required` stayed true after a successful restart. | `TestWindowsRunPersistsAppliedPortsAndClearsRestartRequired`, `TestWindowsSettingsArePersistedBeforeDashboardServes` | `562f117` | PASS |
| 8 | 7 | Dashboard rendered the trust-remove action response instead of refreshing status, so it still showed Trusted. | UI test `refreshes trust status after removing the CA instead of trusting the action response` | `64faaa7` | PASS |
| 9 | 8 | Windows PHP/Composer shims never passed `-c`, so the global CLI ini was ignored (`Loaded Configuration File: (none)`). | `TestEnsureGlobalWindowsToolShimsForBuildUsesGlobalCLIIni`, `TestWindowsPathToWSLPath`, `TestServiceSaveGlobalIniRefreshesWindowsCLIShims` | `1326662` | PASS |
| 10 | 8 | Composer removal left the WSL deployment and local PHAR behind. | Tooling removal/reinstall tests and live evidence | `29e72ca` | PASS |
| 11 | 8 | A foreign Composer link was not reported as a tool conflict. | Foreign-link conflict tests | `4848a0c` | PASS |
| 12 | 10 | Windows service manager passed names instead of service IDs to the web validator. | Service-control validator tests | `dde9da7` | PASS |
| 13 | 10 | Performance includes were written to the Windows home instead of the live WSL service tree. | Apply-path tests with WSL root | `48f3487` | PASS |
| 14 | 10 | Reset created scaffolds for uninstalled inactive web servers. | Reset include tests | `3feced7` | PASS |
| 15 | 12 | Agent config listings omitted file sizes, so `service config list` reported 0 while `config show` reported the real size. | `TestServiceConfigReadApplyResetAndBackupStayAgentSide`, `TestConfigServiceWithAgent` | `14110a4` | PASS |
| 16 | 13 | Normal site creation never emitted `site.created`; only the missing-build branch did. | `TestCreateEmitsOneSiteCreatedEvent`, `TestCreateFailureEmitsNoSiteCreatedEvent`, `TestCreateMissingBuildOutputKeepsSiteCreatedMessage` | `353fa06` | PASS after revision |
| 17 | 13 | Runtime install/remove emitted no `runtime.*` events. | `TestRuntimeInstallAndRemoveEmitEvents` | `353fa06` | PASS |
| 18 | 13 | Dashboard terminal used the site ID and omitted `DEVSTACK_PROJECT`, `DEVSTACK_HOME`, and `PHPRC`; PHP loaded 128M instead of the site 256M. | `TestTerminalEnvironmentMatchesSiteShellContract`, daemon ticket test | `3975450` | PASS |
| 19 | 13 | WSL agent terminal had no idle or absolute limit despite the UI promise. | `TestAgentTerminalClosesAtIdleAndMaxDuration`, `TestAgentTerminalInputResetsIdleTimer`, config tests | `3975450` | PASS |

Failing/passing evidence is in each task directory (`failing-test-before-*.txt`,
`passing-tests-after-*.txt`, live before/after snapshots).

## Destructive actions and restoration

| Task | Destructive action | Backup / restore | Final state |
| --- | --- | --- | --- |
| 0 | Baseline snapshot | `task-0/backup/` (state.db + WAL/SHM, config, helper.yaml, hosts, binaries) | baseline recorded |
| 1–4 | Site/runtime fixtures created and removed | Project files preserved by `site remove`; 8.5 runtime removed after migrations | sites empty, PHP 8.4 default |
| 5 | All seven services installed, activated, uninstalled with `--delete-data` | Service data is per-service and intentionally deleted; final uninstall verified | all `not_installed` |
| 6 | Dashboard port changed and daemon restarted | Config restored byte-identical to Task 0; daemon restarted on 7070 | 7070/8080/8443 |
| 7 | Trust removed, hosts restored | `trust repair`/`init` restored OS + Firefox trust; hosts hash unchanged | trusted, hosts clean |
| 8 | PHP default changed, Composer removed | PHP default restored to 8.4.27; Composer reinstalled | exact shims |
| 9 | nginx/Apache mode attempts | Intentionally rejected; builtin stayed active; fixtures removed | builtin |
| 10 | Performance apply and reset | Reset dialog `RESET`; backups kept; host tuning skipped on Windows | throughput, host not applied |
| 11 | Hosts drift injected | Doctor repair restored the managed block | hosts clean |
| 12 | Full `uninstall --yes` then `init --yes` | `task-12/uninstall-backup/`; OS-level uninstall assertions ran before any auto-starting CLI | daemon/helper/agent/dashboard restored; logon task re-registered |
| 13 | Fixtures and terminals; short idle limit | Sites and WSL dirs removed; agent `agent.yaml` restored and restarted | no orphan PTYs; doctor green |
| 14 | Vertical-slice fixtures | `vertical_slice.ps1` cleaned its three sites, scratch root, hosts, and routes | sites empty |

The dashboard/API token was preserved throughout. During Task 13, a mistyped
`init --home C:\Users\Aram` (PowerShell `$home` is read-only) rotated the WSL
agent bearer token/TLS pin into the accidental home. The DPAPI-protected
`agent-token`/`agent-pin` were copied to the real home, the agent reused the
matching credentials, `init` succeeded, and the accidental scaffold was removed.
The user dashboard token, state database, sites, services, and runtimes were not
affected. This is the only baseline secret change and is recorded here for
transparency.

## Final gate results

All commands were run from the repository root on 2026-10-30. Full output is in
`dist/e2e/full-dashboard/task-14/`.

| Gate | Result | Evidence |
| --- | --- | --- |
| `gofmt -l .` | clean (no output) | `gate-gofmt.txt` |
| `go build ./...` | exit 0 | `gate-go-build.txt` |
| `go vet ./...` | exit 0 | `gate-go-vet.txt` |
| `go test ./...` (CGO disabled) | exit 0 | `gate-go-test.txt` |
| `npm --prefix ui test -- --run` | 16 files / 164 tests passed | `gate-ui-test.txt` |
| `npm --prefix ui run check` | 0 errors / 0 warnings | `gate-ui-check.txt` |
| `npm --prefix ui run build` | Vite build succeeded | `gate-ui-build.txt` |
| `pwsh test/windows/installer_test.ps1` | 280 checks, 0 failures | `gate-installer-pwsh.txt` |
| `powershell.exe test/windows/installer_test.ps1` | 280 checks, 0 failures | `gate-installer-powershell.txt` |
| `pwsh test/windows/lifecycle_test.ps1` | 41 tests, 0 failures | `gate-lifecycle-pwsh.txt` |
| `powershell.exe test/windows/lifecycle_test.ps1` | 41 tests, 0 failures | `gate-lifecycle-powershell.txt` |
| `pwsh test/release/release_windows_ps_test.ps1` | 41 checks, 0 failures | `gate-release-pwsh.txt` |
| `$env:DEVSTACK_HOME='C:\Users\Aram\.devstack'; pwsh test/windows/vertical_slice.ps1` | all acceptance gates passed, fixtures cleaned | `gate-vertical-slice.txt` |

UI commands must run with Windows Node and `C:\Program Files\nodejs` first in
`PATH`; otherwise npm lifecycle scripts resolve the devstack Node shim and load
the Linux Rollup/esbuild binaries. The exact invocation used:

```powershell
$env:PATH = 'C:\Program Files\nodejs;' + $env:PATH
& 'C:\Program Files\nodejs\node.exe' 'C:\Program Files\nodejs\node_modules\npm\bin\npm-cli.js' --prefix ui test -- --run
```

## Final live assertions

- `doctor --json`: `ok=true`, 24 checks, 0 fail, 0 warn, 9 skipped
  (built-in-serving vhost and no-site PHP checks) — `final-doctor.json`.
- `site list --json`: `[]` — `final-sites.json`.
- `service list --json`: nginx, apache, mysql, mariadb, postgresql, redis,
  phpmyadmin all `not_installed` — `final-services.json`.
- `node default --status --json`: `24.22.1`, shim installed — `final-node-default.json`.
- `php default --status --json`: `php@8.4.27-r1`, shim installed — `final-php-default.json`.
- Wrappers: `php.cmd -v` → PHP 8.4.27, `composer.cmd --version` → 2.10.3,
  `node.cmd -v` → v24.22.1.
- Listeners: 80/443 helper, 7070/8080/8443 daemon; performance `throughput`,
  `host_tuning.applied=false`.
- Dashboard sweep (fresh tab, `?v=187`): Sites `0 sites`, Services
  `0 installed / 0 active`, Runtimes `3 installed` (2 Node, 1 PHP), Doctor
  `All systems healthy`, Settings builtin/fixed proxy ports; every route
  **0 console errors / 0 warnings** (`console-*.log`, `dashboard-*.png`).

## Remaining caveats and environment skips

- **Native race suite unavailable:** no gcc/cgo on this host and no Go toolchain
  inside WSL. Timer/constructor race analysis is manual; the standard suite and
  repeated timer tests pass.
- **Doctor skips:** 9 checks are skipped because built-in serving is active
  (external vhost checks) and/or no PHP site is registered. This is expected and
  reported by the UI as "15 of 24 checks passed, 9 skipped".
- **Windows serving scope:** native nginx/Apache mode switching is intentionally
  unsupported on Windows; the guard returns a clear error and keeps builtin
  active (Task 9). nginx/Apache containers still report loopback `18080:18080`
  and 80/443 remain owned by the helper.
- **Plan assertions that are N/A by design:** PHP SPA fallback is not exposed
  (Task 2); PHP-with-no-installed-build validation would require removing the
  live PHP build (Task 1); same-value listener save is disabled client-side
  (Task 6); vhost mode switching is N/A on Windows (Task 12).
- **Terminal scope:** the dashboard terminal opens for a stopped site because it
  is a project shell, not a web-runtime process. On Windows it runs as root
  inside the managed WSL distro; the UI states this explicitly.
- **Source-built version string:** the final daemon/helper/agent were built from
  the reviewed commits for deployment and report `vdev`; release packaging gates
  are independent and green.

A fuller backlog of residual E2E gaps (clean-machine install, real reboot,
failure injection, load, multi-user, race, security fuzzing, and areas covered
only by earlier runs) is maintained in the
[E2E coverage gaps document](2026-10-30-e2e-coverage-gaps.md).

## Evidence map

- `dist/e2e/full-dashboard/task-0/` — baseline, backup, preflight.
- `dist/e2e/full-dashboard/task-1/` … `task-13/` — per-task live evidence,
  `PROGRESS.md`, failing/passing tests, dashboard snapshots.
- `dist/e2e/full-dashboard/task-14/` — gate logs, live assertions, dashboard
  screenshots, console logs.
- `.superpowers/sdd/2026-10-30-dashboard-full-e2e-coverage/progress.md` —
  execution ledger.
- `docs/superpowers/plans/2026-10-30-dashboard-full-e2e-coverage.md` — binding
  checklist, all tasks checked.
