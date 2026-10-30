# E2E coverage gaps and follow-up backlog

- **Date:** 2026-10-30
- **Status:** open backlog; does not invalidate the completed plan
- **Scope:** residual gaps after the [full dashboard/CLI E2E audit](2026-10-30-dashboard-full-e2e.md).
  The binding plan is 15/15 PASS; this document records what was intentionally
  skipped, covered only by earlier runs, tested only below the E2E level, or not
  reproducible on this host.
- **Use:** prioritize additional live E2E work before declaring Windows general
  availability or a future release fully covered.

## Bottom line

The operational dashboard/CLI surface is covered. Not covered E2E are:
clean-machine installation, real reboot/resume, destructive installer
rollback/purge, failure injection, load/soak, multi-user isolation, race
coverage, cross-browser/accessibility, and independent security fuzzing.

## 1. N/A or unsupported by design

These were not tested because the product intentionally rejects or does not
expose the behavior on Windows. The rejection path itself was verified.

| Gap | Why | What was tested instead |
| --- | --- | --- |
| Native nginx/Apache serving on Windows | External serving modes are intentionally unsupported; builtin is the only Windows mode | `vhost use nginx/apache` returns `NOT_SUPPORTED`, builtin stays active; previews and sync work |
| PHP site SPA fallback | Not exposed for PHP sites in this build | Static/Node SPA behavior covered elsewhere; PHP fallback field remains N/A |
| PHP site creation with no installed PHP build | Would require removing the live PHP 8.4 runtime used by fixtures | Code path covered by unit tests only |
| Same-value listener-port save | The dashboard disables Save when nothing changed | Invalid, out-of-range, and conflicting values tested; unchanged state enforced client-side |
| Host tuning on Windows | sysctl/limits tuning is disabled on Windows | Performance profile/preview/apply/reset tested; `host_tuning.applied=false` |
| Web containers owning ports 80/443 | 80/443 are owned by the helper/builtin proxy | Containers verified on loopback `18080:18080`; helper ownership verified |
| Dashboard terminal as a non-root user | On Windows the agent runs the PTY as root inside the managed WSL distro by design | Root shell, site env, PATH, PHP ini, resize, reconnect, idle timeout verified; UI states root scope |
| Vhost mode switching from the CLI | N/A on Windows | `CONFIRMATION_REQUIRED` without `--yes`; `NOT_SUPPORTED` with it |

## 2. Covered earlier, not re-run in the 2026-10-30 pass

These passed in the [2026-10-29 E2E report](2026-10-29-e2e-report.md) or the
[Node toolchain E2E report](2026-10-30-node-toolchain-e2e.md), but were not
freshly exercised after the latest commits. Unit/UI gates still pass; a future
release should re-run them live.

| Area | Prior coverage | Why it matters now |
| --- | --- | --- |
| Dashboard auth-on mode | Token bootstrap, session cookie, CSRF, unauthenticated 403, logout/expiry | The 2026-10-30 run used the default auth-off configuration |
| Add Site options | WWW alias, force-HTTPS on, SPA, env vars, TLS on/off | The recent run exercised the default HTTPS/force-off/www-off path |
| Build profiles | Add profile, run build, output refresh, log capture, failure restore | Build/output code was not changed by the latest fixes, but no fresh live rerun |
| Site configuration | Build profile, env, SPA fields, front controller, document root | Recent coverage focused on PHP directives/ini/extensions |

## 3. Only unit/integration-tested, not live E2E

| Gap | Current coverage | Needed live E2E |
| --- | --- | --- |
| Terminal absolute max duration | `TestAgentTerminalClosesAtIdleAndMaxDuration` (max-duration subtest) | Set `terminal_max_seconds` to a short value live and verify clean close/reconnect |
| PTY-output-triggered idle reset | `TestAgentTerminalInputResetsIdleTimer`; output activity unit-covered | Run a command that emits output near the idle boundary and verify the session stays open |
| SSE event types beyond the captured matrix | `site.created/running/stopped/deleted`, `runtime.installed/removed`, `service.config.*` captured live | `site.updated`, doctor/trust refresh events, service install/uninstall events (where emitted) |
| SSE edge cases | Single browser client, one reconnect after daemon restart | Two concurrent clients, expired-session 403, slow-client backpressure, reconnect storms |
| Certificate expiry/renewal | Unit tests around identity loading/expiry | Live renewal rotation, pinned-fingerprint update, dashboard/HTTPS recovery |
| Service/container failure recovery | Happy-path lifecycle and config validation/rollback | Kill a container mid-operation, Docker daemon restart, helper restart during apply |
| Doctor repair classes | Hosts-drift repair exercised live | Port, trust, runtime, vhost, and PHP-config repairs each with injected drift |
| Hard daemon crash and reboot auto-start | Graceful `daemon restart`; logon task registered and Ready | SIGKILL/crash recovery and a real Windows reboot with logon-task startup |
| Config downgrade compatibility | New agent YAML fields are exercised by current agent tests | Upgrade then downgrade: older agent `KnownFields(true)` rejects new `terminal_*` fields; define/verify a support policy |
| Accessibility and responsive layout | Playwright snapshots at 900x650 and 1200x800 for terminal resize | Keyboard-only navigation, screen-reader labels, mobile/tablet layouts, contrast |
| Cross-browser dashboard | Chromium via Playwright MCP; Firefox trust policy checked at OS level | Firefox/WebKit UI flows, especially terminal, SSE, and downloads |

## 4. Host-level / not reproducible here

| Gap | Why it remains open | How to close it |
| --- | --- | --- |
| Clean-machine install from the release ZIP | Installer gates use `-TestSandbox`/`-DryRun`; no fresh WSL import on a blank machine | Dedicated clean Windows VM: run the released `install.ps1`, verify helper/daemon/agent/dashboard/sites |
| Real `uninstall.ps1` default and `-PurgeData/-PurgeDistro` | Lifecycle suite runs in sandboxes; the 2026-10-30 uninstall was the `devstack uninstall` CLI, not the PowerShell uninstaller | Throwaway VM with snapshots; verify services, task, firewall, trust, hosts, securestore, distro, PATH |
| Real reboot/resume | Logon task was re-registered after Task 12 but the host was not rebooted | Reboot twice: first-run task registration, second-run auto-start; verify daemon/helper/agent, sites, dashboard |
| Upgrade rollback and purge | Task 0 ran a real `install.ps1 -Upgrade`, but rollback/purge paths are mocked in the lifecycle suite | VM with populated state, inject upgrade failure, verify binary/data rollback and health |
| Windows 10 22H2 and other Windows builds | Validated on the current Windows 11 host only (`OS build 26300`) | Run the acceptance suite on Windows 10 22H2+ and a second Windows 11 build/locale |
| Multi-user/SID isolation | Single-user host; `allow_sid` configured for the installing user only | Second local user: helper access, dashboard session, terminal/API authorization, securestore isolation |
| Native `-race` suite | No gcc/cgo on Windows; no Go toolchain in WSL | Linux/WSL host with Go + gcc: `go test -race -p 1 ./...` (or the planned coverage host) |
| Network variants | No VPN/proxy/IPv6-only run; WSL localhost forwarding outage not simulated | Matrix: VPN, corporate proxy, IPv6-only, firewall change, forwarding-down and one-request 502 retry |
| Load/soak and scale | Benchmarks are synthetic; no long-lived soak | Many sites/services, sustained proxy traffic, memory/FD growth, log rotation under load |
| Disk-full/corrupt-state recovery | Not injected live | Fill disk, corrupt `state.db`, interrupt a migration, verify backup/restore diagnostics |
| Independent security review/fuzzing | No external review or fuzzing campaign; statement coverage was 73.9% in the earlier audit | Security review + fuzz targets for API inputs, path translation, config parsing, terminal tickets |

## 5. Known residuals outside this backlog

These are known product/security limitations rather than missing test cases.
They should be tracked separately from E2E coverage.

- **WSL file API path confinement:** `/api/v1/fs` can browse arbitrary absolute
  paths and `../../etc` directory listings; it is not confined to project/home
  roots. Documented in the 2026-10-29 report.
- **Root execution inside the managed WSL distro:** agent, PHP-FPM/Node workers,
  builds, and terminals run as root. Architectural and documented, not a test gap.
- **Provider-switch vs activation interleaving (`A-2`):** still needs a
  cross-package lifecycle coordinator; not covered by the dashboard E2E plan.
- **Forwarding-outage 502 window:** a request arriving during a longer WSL
  forwarding outage can receive a 502 after the 1.5 s retry window; a retry
  succeeds.
- **IPv6 loopback tests:** unit tests skip themselves when IPv6 loopback is
  unavailable on the host; environment-dependent, not an E2E assertion.

## 6. Suggested closure order

1. **P1 — release-blocking host lifecycle:** clean-machine install, real reboot,
   real `uninstall.ps1` default/purge, populated upgrade rollback.
2. **P2 — recovery and destructive failure paths:** Docker/WSL/helper outages,
   hard daemon crash, certificate renewal, doctor repair classes.
3. **P3 — recent-feature live revalidation:** auth-on, WWW/force-HTTPS/SPA/env,
   build profiles, terminal max duration/output idle reset, extra SSE events.
4. **P4 — platform/quality matrix:** Windows 10/11 variants, multi-user/SID,
   `-race`, network matrix, load/soak, cross-browser/accessibility, security
   review/fuzzing.

## References

- [Full dashboard/CLI E2E audit](2026-10-30-dashboard-full-e2e.md)
- [Binding coverage plan](../plans/2026-10-30-dashboard-full-e2e-coverage.md)
- [2026-10-29 E2E report](2026-10-29-e2e-report.md) — “Blocked / not reproducible here”
- [Node toolchain E2E report](2026-10-30-node-toolchain-e2e.md)
- Live evidence: `dist/e2e/full-dashboard/task-0` … `task-14`
