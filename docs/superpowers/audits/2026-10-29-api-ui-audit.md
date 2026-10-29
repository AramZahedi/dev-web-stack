# API, Daemon, Helper, CLI, and Dashboard Audit — 2026-10-29

**Worktree:** `C:\Users\Aram\.local\share\opencode\worktree\dc072e\audit-api-ui`
**Branch:** `audit/api-ui` (based on `d9c2c39`, the Round-4 remediation commit)
**Scope:** `internal/api`, `internal/daemon`, `internal/proxy`, `internal/helper`,
`cmd/devstack-helper`, `internal/ipc`, `internal/securestore`, `internal/cli`,
`ui/**`, `api/openapi.yaml`, `internal/winfirewall`, `internal/wintrust`,
`internal/winhosts`.
**Out of scope (read only):** `internal/agent`, `internal/sites`, `internal/build`,
`internal/terminal`, installer/release scripts. Anything needing a change there is
listed under cross-area issues.

**Method:** source review of the authentication middleware and all API routes;
Windows daemon bootstrap/lifecycle; helper service and trust plumbing; CLI
command tree; dashboard API client and route error/loading paths; targeted
failing tests first where the bug was testable; full gate runs. No live daemon
was started against the real `%USERPROFILE%\.devstack` / `C:\devstack` install.
Package tests use `t.TempDir()` homes and free high ports; no live upgrade,
uninstall, or destructive operation was run.

---

## Verdict

Several Round-4 remediation claims were incomplete on Windows. The most serious
was that the helper still used the **LocalSystem process profile** for its
system/CA/root fixed-path checks, so a fresh service either failed to start or
rejected the installer's root certificate. That made `devstack init` trust
installation fail on a correctly provisioned install. The service registration
also ignored the `--config` path used by custom installs. Both are fixed and
covered by tests.

Remaining residual risks are primarily the known root-only WSL execution model
and installer/service-upgrade behavior outside this audit's file scope.

---

## Findings and fixes

| # | Severity | Area | Status |
|---|---|---|---|
| 1 | High | wintrust/helper configured CA system paths ignored | Fixed |
| 2 | High | helper service install ignored `--config` | Fixed |
| 3 | Medium-High | Windows `daemon stop` could kill an unrelated stale PID | Fixed |
| 4 | Medium | WSL keepalive hot restart loop | Fixed |
| 5 | Medium | `devstack init` wrote agent credentials to ambient `DEVSTACK_HOME` | Fixed |
| 6 | Medium | Legacy `C:\devstack` install not detected by CLI/daemon | Fixed |
| 7 | Medium | Windows `EnsureRunning` left a failed daemon running | Fixed |
| 8 | Medium | Windows public-proxy shutdown could hang forever | Fixed |
| 9 | Medium | Windows public-port changes broke helper forwarders/firewall | Fixed (rejected + UI gated) |
| 10 | Medium | Missing helper made hosts sync report success | Fixed |
| 11 | Low-Medium | Non-GET unknown API paths returned plain-text errors | Fixed |
| 12 | Low | Dashboard pages blanked when one independent API call failed | Fixed |
| 13 | Low | Service config content was session-gated but client did not ensure session | Fixed |
| 14 | Low | SSE stream opened while dashboard was locked | Fixed |
| 15 | Low | CLI `php` did not propagate PHP's exit code | Fixed |
| 16 | Low | CLI uninstall elevated command hardcoded `C:\devstack` | Fixed |
| 17 | Low | Helper firewall rules used Private profile only | Fixed |
| 18 | Low | Build log tail was unbounded | Fixed |
| 19 | Low | `site source --remove-source` success message always said "kept" | Fixed |
| 20 | Low | OpenAPI claimed terminal never runs as root (false on Windows) | Fixed docs |

### 1. Helper hardcoded the process-profile system/CA/root paths (High)

**Evidence.** `internal/wintrust/trust_windows.go` built `DefaultSystemDir`,
`DefaultCADir`, and `DefaultRootCertificate` from
`platform.DefaultRoot("windows")` at package init. The helper runs as
`LocalSystem`, whose `%USERPROFILE%` is
`C:\Windows\system32\config\systemprofile`, not the installer's profile.
`EnsureStorage` and `ProtectSystem` inspected/protected that default path, while
`RootInstallArgs` refused any certificate path other than the default root
certificate. `helper.NewServer` correctly derived `SystemDir`/`CADir` from
`--config` for CA loading, but `wintrust` never used those values. On a real
service start this meant:

- `EnsureStorage` failed because `...\systemprofile\.devstack\system` was never
  created by the installer; or
- `handleTrustInstall` rejected the configured
  `%USERPROFILE%\.devstack\system\pki\root.crt` and returned 500.

The same mismatch put the Firefox ownership marker under the wrong profile and
made `uninstall.ps1`'s marker/lookup ineffective.

**Fix.** `wintrust.Manager` now owns configurable `systemDir`, `caDir`, and
`rootCertPath`. `ConfigurePaths` validates absolute paths and updates the
Firefox marker location unless a test supplied a custom state path.
`ProtectSystem`, `ProtectCAPath`, `EnsureStorage`, `caFiles`, and
`validateExistingCAFile` compare against the configured values.
`InstallRoot` uses `RootInstallArgsAt` with the configured root certificate;
`RootInstallArgs` remains the default wrapper for compatibility.
`helper.NewServer` always calls `ConfigurePaths(cfg.SystemDir, cfg.CADir,
cfg.RootCertPath)`, including for injected managers.

**Tests.** `TestConfiguredPathsReplaceProcessProfileDefaults`,
`TestRootInstallArgsUseConfiguredCertificate`,
`TestManagerInstallsConfiguredRootAndProtectsConfiguredCA` (wintrust), and
`TestWindowsHelperTrustInstallUsesConfiguredRootCert` (helper). The helper test
installs trust through the real handler with `SystemDir`/`CADir`/`RootCertPath`
under a temp tree and asserts `certutil` receives that root.

### 2. Helper service registration ignored `--config` (High)

**Evidence.** `cmd/devstack-helper/service_windows.go` persisted the config at
`opts.configPath` but built the service binpath with
`helper.DefaultWindowsConfigPath`. A custom `-InstallDir`, or any explicit
`--config`, therefore started the service against a different (often missing)
config. The derived system/CA paths then pointed at the LocalSystem profile
again, and the persisted `system_dir`/`ca_dir`/`root_cert` were ignored.

**Fix.** `windowsServiceInstallArgs(executable, configPath, allowSID)` and
`installWindowsService` take and validate the absolute config path; `service
install` passes `opts.configPath` and rejects relative paths. The service now
runs `devstack-helper.exe service run --config <the persisted file>
--allow-sid <sid>`.

**Tests.** `TestBuildWindowsServiceInstallCommandUsesLocalSystem` now uses a
custom path and asserts it appears in `binPath` and that the profile default does
not; `TestBuildWindowsServiceInstallCommandRejectsRelativeConfig`.

### 3. Windows daemon stop could force-kill an unrelated stale PID (Medium-High)

**Evidence.** `internal/cli/daemon_windows.go` read `daemon.pid` and, after the
10-second stop timeout, called `proc.Kill()` on whatever PID the file named. A
stale file whose PID had been reused could kill an unrelated process. Unlike
Linux, Windows had no start-time/command-line identity check.

**Fix.** Added `processIsDevstack(pid)` using
`QueryFullProcessImageName` and only treats `devstack.exe` as the daemon. The
stale/absent-daemon path removes the pid file and returns without signalling. A
force-kill is allowed only when the home's control pipe was reachable at entry
and the image still matches at timeout. The diagnostic remains a timeout error
rather than a cross-install kill.

**Test.** `TestWindowsDaemonStopDoesNotKillAnUnrelatedStalePID` starts a
`ping.exe` victim, writes its PID to the pid file, and asserts
`stopDaemonPlatform` returns `(false, nil)`, leaves the victim alive, and removes
the stale file.

### 4. WSL keepalive respawned every 250 ms forever (Medium)

**Evidence.** `runWSLKeepalive` launched
`wsl.exe ... /bin/sleep infinity`; if the distro was missing or WSL was disabled,
the command exited immediately and the loop restarted after 250 ms, logging an
info line each time. That is a sustained process/CPU/log-spam loop.

**Fix.** Extracted `keepaliveBackoff` with 250 ms initial delay, exponential
growth to a 30 s cap, and reset after a 30 s healthy run. Start failures and
immediate exits back off; only the first failure logs at warn/info, later
attempts log at debug. `TestWSLKeepaliveBackoffEscalatesAndCaps` and
`TestWSLKeepaliveBackoffNeverHotLoops` cover it.

### 5. `devstack init` used ambient `DEVSTACK_HOME` for agent credentials (Medium)

**Evidence.** `ensureAgentCredentials` called `securestore.Load`/`Save`, which
resolve the store from `DEVSTACK_HOME` when no explicit home is supplied. It only
set the environment when empty. With `devstack init --home A` and an inherited
`DEVSTACK_HOME=B`, credentials were written to B; the daemon for A then rejected
the securestore home mismatch, or a different install's agent used the token.

**Fix.** Switched the function to `securestore.LoadAt(home, ...)` /
`SaveAt(home, ...)` and removed the environment mutation. The daemon now also
sets `DEVSTACK_HOME` to its resolved `cfg.Home` unconditionally at startup, so a
`--home` override wins over the launching shell.

**Test.** `TestEnsureAgentCredentialsUsesResolvedHomeNotEnvironment` sets
`DEVSTACK_HOME` to a different temp home, runs `ensureAgentCredentials` for the
resolved home, and asserts the token exists only under the resolved home.

### 6. Legacy `C:\devstack` installs were not detected by CLI/daemon (Medium)

**Evidence.** The standalone installer detects an existing
`C:\devstack\system\install-manifest.json` for upgrade/uninstall
(`cmd/devstack-installer`), but `cli.resolveHome` and
`daemon.loadWindowsConfig` fell back to `%USERPROFILE%\.devstack`. On the review
machine both trees exist; running `devstack` from the legacy install would start
a second, empty daemon on the profile home instead of talking to the installed
one.

**Fix.** Added `daemon.LegacyWindowsHome()`. It returns `C:\devstack` only when
that tree has an install manifest and the new-default home does not. The CLI and
Windows daemon resolve through it before the platform default. Explicit `--home`
and `DEVSTACK_HOME` still win.

**Test.** `TestInstallManifestDetectionRequiresARegularFile` covers the manifest
check and rejects a directory with that name.

### 7. `EnsureRunning` left a failed Windows daemon running (Medium)

**Evidence.** If `waitWindowsReady` timed out or was cancelled, `EnsureRunning`
returned the error without killing the child it had just spawned. A retry then
hit `ErrAlreadyRunning`, and the user saw a failed start with a live daemon.

**Fix.** On a readiness error the spawned process is killed and its `Wait` is
reaped with a 2 s bound before returning the original error.

### 8. Public-proxy shutdown used an unbounded background context (Medium)

**Evidence.** `Run` deferred `publicProxy.Stop(context.Background())`. A stuck
upstream or streaming response could block `Shutdown` (and the deferred
`waitForDone`) forever, so `devstack daemon stop` or service stop could hang
after the API listeners closed.

**Fix.** `stopWindowsProxy` uses a fresh 5 s timeout, closes active connections
on timeout, and logs the residual error.

### 9. Windows public-port changes silently broke 80/443 (Medium)

**Evidence.** Settings accepts `http_port`/`https_port` changes on every
platform and tells the user the helper proxy targets update automatically. The
Windows helper only forwards 80/443 to fixed `127.0.0.1:8080/8443`; the installer
firewall rule also targets 8080/8443. After a restart the daemon listened on the
new ports while the helper kept forwarding to the old dead ports, taking the
dashboard and every site offline.

**Fix.** `api.Config.PublicPortsFixed` is set by the Windows daemon;
`PATCH /settings` rejects changed public ports with a clear 400, while allowing
unchanged values and the dashboard port. `GET /settings` exposes
`public_ports_fixed`, the UI disables the two fields and explains why, and
OpenAPI documents the field/rejection.

**Tests.** `TestPatchSettingsRejectsFixedWindowsPublicPorts` and the Windows
Settings dashboard test asserting the HTTP/HTTPS inputs are disabled and the
dashboard port is editable.

### 10. `SyncHosts` reported success with no helper (Medium)

**Evidence.** `helperAdapter.SyncHosts` returned `nil` when no helper client was
connected. `sites.Create` then persisted a site whose domain was never added to
the Windows hosts file, and `devstack init`/the dashboard reported success.

**Fix.** It now returns an error wrapping
`servicecontrol.ErrHelperUnavailable`, which maps to HTTP 503
`HELPER_UNAVAILABLE` and a CLI remediation. `TestWindowsHelperAdapterRejectsSilentHostsSyncWithoutHelper`.

### 11. Unknown non-GET API paths returned plain text (Low-Medium)

**Evidence.** `GET /api/v1/<unknown>` returned the JSON envelope through
`handleAPINotFound`, but `POST/PUT/PATCH/DELETE /api/v1/<unknown>` reached
net/http's plain-text 404/405. API clients expect the stable
`{code,message,details}` shape.

**Fix.** `methodErrorWriter` now intercepts net/http's plain-text 404 as well as
405 for API paths and emits `NOT_FOUND` / `METHOD_NOT_ALLOWED` JSON. Handler
404s that already wrote JSON are not intercepted. Covered by
`TestAPIMethodMismatchReturnsJSON` and the unknown-path cases in
`TestSPAFallbackAndAssetMIME`.

### 12. Dashboard pages blanked on one independent API failure (Low)

**Evidence.** `Sites.load` used `Promise.all([listSites, listRuntimes])`; on
Windows the runtime inventory lives in the WSL agent, so an agent outage made
the whole Sites page show an error and no sites even though `/sites` succeeded.
`Settings.load` did the same with `getSettings` + `getTrust`, so a stopped helper
blanked all local settings. `AddSite` coupled settings and runtime loading.

**Fix.** Sites loads sites fatally and runtimes non-fatally, showing “Runtime
inventory unavailable” for the count. Settings loads config fatally and trust
non-fatally with a localized “Could not read trust status” banner and Retry.
AddSite loads settings and runtimes independently so static sites remain
creatable. Tests: `keeps sites visible when the runtime inventory is
unavailable`, `keeps local settings visible when the helper trust status is
unavailable`.

### 13. Service config content did not establish the dashboard session (Low)

**Evidence.** `GET /api/v1/services/{id}/config/content` is intentionally
session-gated in `authenticate`, but the UI client used the generic GET path,
which only sends the install-token cookie. On a deep link before
`initSession` completed (or after the session cookie was lost), the first read
returned 403.

**Fix.** `getServiceConfigContent` awaits `fetchSession()` and retries once after
a 403, matching the existing `/fs` behavior. Test:
`establishes a session before reading session-gated config content`.

### 14. SSE opened while the dashboard was locked (Low)

**Evidence.** `App.svelte` called `openEvents` unconditionally while
`initSession` was still running. A locked dashboard therefore opened an
EventSource that received 403 and retried indefinitely.

**Fix.** `App` awaits `initSession` and only starts the event stream when
`$session.authenticated` is true.

### 15. CLI `php` lost PHP's exit code (Low)

**Evidence.** A non-zero `PHPExec` result was converted to a generic
`PHP_EXEC_FAILED` CLI error, so the process exited 1 instead of PHP's status.

**Fix.** Return `ExitError{Code: result.ExitCode, Err: ...}`; `main` already
uses `cli.ExitCode`. The test harness now mirrors `main`'s exit-code handling,
and the `php` failure test asserts exit code 2 for a PHP exit 2.

### 16. CLI uninstall pointed at `C:\devstack` for every install (Low)

**Fix.** `elevatedRemovalCommand(home)` builds
`<home>\uninstall.ps1`; custom and default profile installs both get the right
script. Updated `TestCLIUninstallWindowsElevatedCommand`.

### 17. Helper firewall rules were Private-only (Low)

`winfirewall.Enable` used `profile=private`, while the installer's
daemon-proxy rule uses `private,domain`. Domain-joined WSL adapters could fall
outside the helper rules. Changed to `profile=private,domain` and asserted it.

### 18. Build log tail was unbounded (Low)

`GET /sites/{ref}/build/logs?tail=` accepted any non-negative integer. Capped at
1000, matching service logs.

### 19. `site source --remove-source` message (Low)

The success text always said the original source was kept even when removal was
requested. It now reports `kept`/`removed` based on the flag.

### 20. OpenAPI terminal root claim (Low)

OpenAPI stated the terminal never runs as root. That is true on Linux, but the
Windows terminal is bridged to the WSL agent PTY, which runs as root inside the
managed distro. The description now says so explicitly.

---

## Tests and gate evidence

All commands were run from the worktree on Windows (Go 1.27.0, Node 24.22.1,
npm 11.19.1).

| Gate | Result |
|---|---|
| `go build ./...` | PASS (exit 0) |
| `go vet ./...` | PASS (exit 0) |
| `go test ./...` | PASS (all packages) |
| `go test ./internal/api ./internal/cli ./internal/daemon ./internal/helper ./internal/wintrust ./internal/winfirewall ./cmd/devstack-helper` | PASS |
| `npm --prefix ui test -- --run` | PASS — 15 files, 126 tests |
| `npm --prefix ui run check` | PASS — 0 errors, 0 warnings |
| `npm --prefix ui run build` | PASS |

Additional focused tests added by this audit:

- API: `TestEveryAPIRouteRequiresTheInstallToken` (all routes, query-string token
  rejection, terminal WebSocket without token), plus the unknown-path JSON
  assertions and settings fixed-port rejection.
- Helper: configured trust install, configured service config path.
- wintrust: configured storage/ACL/root paths.
- daemon: backoff, install-manifest detection, missing-helper hosts sync.
- CLI: stale-PID stop safety, securestore home, PHP exit code, uninstall command.
- UI: runtime-inventory resilience, trust-resilience, fixed public ports,
  service-config session bootstrap.

Not run: native Windows `go test -race ./...` (requires CGO/toolchain);
`test/windows/installer_test.ps1` and `vertical_slice.ps1` (out of scope and
state-mutating); live upgrade/uninstall or any destructive operation against the
real install.

---

## Files changed

Product code:

- `internal/wintrust/trust_windows.go` (+ tests)
- `internal/helper/winserver.go` (+ Windows server test)
- `cmd/devstack-helper/service_windows.go` (+ tests)
- `internal/daemon/windows.go`, `internal/daemon/legacy.go` (+ tests)
- `internal/cli/daemon_windows.go`, `process_windows.go`, `init_windows.go`,
  `root.go`, `php.go`, `site.go`, `uninstall.go` (+ tests)
- `internal/api/server.go`, `middleware.go`, `handlers_settings.go`,
  `handlers_tooling.go` (+ tests)
- `internal/winfirewall/firewall_windows.go` (+ test)
- `ui/src/lib/api.ts`, `api.test.ts`, `types.ts`
- `ui/src/App.svelte`, `ui/src/routes/Sites.svelte` (+ test),
  `Settings.svelte` (+ test), `AddSite.svelte`
- `api/openapi.yaml`

No product changes were made under `internal/agent`, `internal/sites`,
`internal/build`, `internal/terminal`, or the installer/release scripts. The
initial worktree contained a pre-existing line-ending-only edit to
`test/windows/installer_test.ps1`; it is deliberately not part of this commit so
the branch stays inside the requested scope (it remains an unstaged worktree
change from another session).

---

## Residual risks and cross-area issues

1. **Root-only WSL execution remains (R2-M12/R4-H1 substrate).** The agent,
   PHP-FPM, site exec, build, and terminal run as root in the managed distro.
   On Windows the dashboard terminal is a root shell inside WSL; OpenAPI now says
   so. The per-install install-token gate prevents other local users from using
   the dashboard to reach it, but any process running as the installing user can
   still read the DPAPI-protected token and exercise root code. A non-root distro
   user/agent is still the right long-term fix.
2. **No caller SID check on the loopback API.** R4-H1's per-install token is the
   boundary. A same-user process can read the token from the securestore; a
   different local user cannot unseal the DPAPI blob but can still reach the
   loopback listener and gets 403. This is documented as the intended model, not
   a full multi-user boundary.
3. **`agentPorts` does not lazily re-acquire an agent client.** If the Windows
   daemon starts before `devstack init` provisions agent credentials, the port
   provider remains `Client == nil` until the daemon restarts, even though the
   other agent adapters load credentials lazily. Normal install order
   (init provisions credentials before the daemon starts) avoids this. A lazy
   provider wrapper in `internal/daemon` is a reasonable follow-up.
4. **Legacy detection is duplicated only in the CLI and Windows daemon.**
   `internal/config.resolveHome`, `internal/client.resolveHome`, and
   `securestore.storeDirAt("")` still know only about `~/.devstack`; they are
   bypassed by this audit's explicit-home paths, but direct library callers can
   still split homes on a legacy `C:\devstack` install. The shared resolution
   belongs in `internal/config`/`platform` (outside this scope).
5. **Installer/service upgrade path.** `install.ps1` only registers the helper
   service when it does not already exist. An existing service with the old
   hardcoded binpath is not re-registered on upgrade, so the installer should
   compare/update `sc qc DevstackHelper` in the installer area. Similarly, the
   uninstaller does not invoke the helper's `Uninstall` RPC (R4-M2); the CLI
   `devstack uninstall` does.
6. **Helper service recovery.** The Windows service is installed without SCM
   failure/recovery actions; a helper crash leaves 80/443 and trust operations
   down until a reboot or manual start. Installer-owned.
7. **Session/cookie hardening.** The session cookie is host-scoped but sibling
   `.test` sites can potentially set a `Domain=test` cookie with the same name;
   CSRF is still HMAC-bound to the session secret, so this is cookie tossing/DoS
   rather than a request-forgery bypass. `__Host-` cookies are not possible on
   plaintext loopback. Low priority.
8. **Windows settings vs. helper target ports.** The API now rejects public-port
   changes, but a hand-edited `config.yaml` can still set other ports and strand
   80/443. The daemon logs the helper target warning; a startup validation could
   fail closed in a future change.
9. **`windowsAgentServiceAdapter.ServiceStatus` still maps agent failures to
   `not_installed`.** This keeps the service catalog readable while the agent is
   down but can show a false “not installed” state. R2-H7's “no fabricated
   success” principle suggests surfacing an unavailable state instead.
10. **OpenAPI/settings schema** now exposes `public_ports_fixed`; generated
    clients in other repositories should regenerate.

---

## Commits

- A single audit commit on `audit/api-ui` on top of `d9c2c39` (not pushed, not
  merged). The branch is intentionally not fast-forwarded into `master`.
