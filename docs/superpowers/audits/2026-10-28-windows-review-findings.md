# Windows 10/11 + WSL2 Implementation — Post-Completion Review Findings

**Target Project:** `dev-web-stack`
**Reviewed Commit:** `6d7bdaa` (`master`, tag `v0.2.0`, clean working tree, in sync with `origin/master`); Round 4 re-baselined at `6f74dcc`
**Review Date:** 2026-10-28
**Review Scope:** In-depth verification of the declared-finished Windows 10/11 + WSL2 implementation: real gate runs on Windows and in WSL2 ext4, live probing of the installed instance on the review machine, and source-level audit of installer, daemon, API, release, and test paths.
**Policy Notice:** Rounds 1-2 were conducted read-only on tracked product files. A concurrent remediation agent then committed fixes; this document was restored and extended by the audit (Rounds 3-4). This document and `AGENTS.md` are the only files intentionally added/edited here. It is written for a follow-up agent that will **verify and fix** each finding.

> **Line-number caveat:** all `file:line` references are accurate at commit `6d7bdaa`. They will drift as fixes land — re-grep before editing.

---

## Verdict

**The implementation is NOT 100% bullet-proof.**

- **5 Critical (release-blocking)** defects were found; several are live-reproduced on the review machine, not just theoretical.
- **6 High**, **8 Medium** defects and a set of unverified claims round out the list.
- The two most urgent are **C1** (an authentication boundary failure reachable from WSL — and from the LAN on fresh installs) and **C3** (an upgrade failure path that can irreversibly destroy all WSL data while reporting success).

> **Round 4 update (HEAD `6f74dcc`, 2026-10-29):** The counts above are the historical Round 1-2 result. The remediation fixed most Round 1-3 defects, but Round 4 adds two new Critical findings: **R4-C1** (the shipped release payload omits the WSL provisioning assets) and **R4-C2** (runtime-delete path traversal to a root `os.RemoveAll`). See the post-remediation status section and Round 4 below. The verdict remains **NOT 100% bullet-proof**.
>
> **Handoff:** the one-screen current-state section is at the end: [Current Status and Handoff](#current-status-and-handoff-2026-10-29).

### Findings Summary Matrix

| Severity | Count | Primary Impact Areas |
|---|---|---|
| **Critical** | 5 | Unauthenticated remote control API via public proxy; Windows daemon missing Services/Terminal/Build wiring (all 503); upgrade backup failure undetectable + rollback deletes live data; reboot-resume runs as SYSTEM (breaks per-user WSL + helper auth); release scripts fabricate and sign a placeholder rootfs |
| **High** | 6 | Firewall rule can be unscoped on fresh installs; installer swallows helper/agent failures and installs a fake agent stub; native Windows tests red while docs claim green; doctor false warnings on healthy installs; `.cmd` shims don't translate Windows paths; no daemon autostart + stale uninstaller claims |
| **Medium** | 8 | Docs self-contradictions; Windows 10 22H2 completely unverified; vertical-slice gates non-enforcing; installer tests mock-only; IPC PID-reuse TOCTOU; TLS pin leaf-only; release build mutates tracked `payload.zip`; upgrade leaves daemon stopped |
| **Low / Notes** | — | See "Solid areas and design observations" |

---

## Table of Contents

1. [Review basis: gates actually run](#1-review-basis-gates-actually-run)
2. [Live environment facts (review machine)](#2-live-environment-facts-review-machine)
3. [Critical findings](#3-critical-findings)
4. [High-severity findings](#4-high-severity-findings)
5. [Medium findings](#5-medium-findings)
6. [Solid areas and design observations](#6-solid-areas-and-design-observations)
7. [Unverified claims / test-matrix gaps](#7-unverified-claims--test-matrix-gaps)
8. [Remediation roadmap and acceptance criteria for the fixing agent](#8-remediation-roadmap-and-acceptance-criteria-for-the-fixing-agent)
9. [Verification command appendix](#9-verification-command-appendix)

---

## 1. Review basis: gates actually run

Every gate below was executed during this review. Do not re-claim green without re-running them.

| Gate | Result | Notes |
|---|---|---|
| Windows `go build ./...` | **PASS** | |
| Windows `go vet ./...` | **PASS** | |
| Linux `go test ./...` in WSL ext4 (copy of HEAD at `/data/devstack/review`) | **PASS** | |
| WSL `go test -race` on daemon, proxy, sites, agent, agentrelay, agentdocker, wsl, pathmode, store, ipc, tooling, agentclient, helper | **PASS** (exit 0, no data races) | |
| Windows native `go test ./...` | **FAIL** | `internal/phpini`: `TestLintRealPHP`, `TestComposeManagedExtensionsGlobalAfterPathSection`. Every other package passes. See H3. |
| `ui`: `npm run check` | **PASS** | 0 errors, 0 warnings |
| `ui`: `npm test -- --run` | **PASS** | 118/118 |
| `ui`: `npm run build` | **PASS** | |
| `test/windows/installer_test.ps1` | **PASS** | 92 checks, 0 failures — but mock/sandbox paths only (see M4) |
| `test/windows/vertical_slice.ps1` | **NOT re-run** | State-mutating; claims reconciled against live machine instead (see §7) |
| `devstack doctor --json` (live) | `ok: true` overall | But `tools` and `ports` checks produce **false warnings** on a healthy install (H4) |

---

## 2. Live environment facts (review machine)

Recorded so the fixing agent can re-verify; re-check each item, it may change.

- Windows 11 Pro, build `10.0.26200.0` (x64).
- `DevstackHelper` service: Running, `AUTO_START`, runs as LocalSystem, binpath
  `C:\devstack\bin\devstack-helper.exe service run --config C:\devstack\system\helper.yaml --allow-sid S-1-5-21-…-1000`
  (the interactive user's SID — correct here because this install did **not** use the reboot-resume path).
- Daemon: `devstack.exe daemon run`, PID owned by the interactive user, started from a `pwsh.exe` parent whose PATH **predates** the installer's Machine PATH update (this is the root cause of the doctor `tools` false warning — H4).
- WSL: managed `devstack` distro (Debian 12, WSL2, `ext4.vhdx` ≈ 2.16 GB), registered **per-user** in `HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss` with `BasePath C:\devstack\wsl\devstack`. SYSTEM hive (`HKU\S-1-5-18`) has no distro.
- Listeners at review time:
  - `127.0.0.1:80`, `127.0.0.1:443` — helper forwarder
  - `127.0.0.1:7070` — dashboard (daemon)
  - `[::]:8080`, `[::]:8443` — public proxy (daemon). Source pins `0.0.0.0` (`internal/proxy/windows_listen.go:15-19`); the running binary showed dual-stack `::`.
- Firewall rule `devstack-daemon-proxy`: Enabled, Inbound Allow, TCP 8080+8443, Profile **Domain,Private**, `RemoteAddress 172.25.48.0/20` (WSL subnet), `InterfaceAlias vEthernet (WSL (Hyper-V firewall))`. Scoped **only because the WSL adapter already existed at install time** (H1).
- `C:\devstack\bin\php.cmd` / `composer.cmd` pass `%*` verbatim to `wsl.exe -d devstack --exec …` (H5).
- Uninstall defaults preserve data; `-PurgeDistro` / `-PurgeData` are opt-in.

---

## 3. Critical findings

### C1 — The public proxy exposes the full dashboard API, and authentication is self-service

**Status: live-verified. Security boundary failure.**

**Code evidence**

- Public proxy is wildcard-bound by design: `internal/proxy/windows_listen.go:15-19` returns `0.0.0.0:<port>`.
- The Windows daemon routes `Host: devstack.test` on that public listener to the dashboard API at `127.0.0.1:UIPort`, **preserving the Host header**: `internal/daemon/windows.go:596-612` (route map at `607-609`).
- The API Host allowlist explicitly includes `devstack.test`: `internal/api/middleware.go:15` (guard at `:51`).
- The API's security model states "the API is loopback-only" (`internal/api/middleware.go:122-127`) — an assumption the proxy breaks.
- `GET /api/v1/session` mints a session cookie + CSRF token for **any** caller: `internal/api/server.go:517-534`.
- Read-only endpoints are intentionally unauthenticated: `internal/api/middleware.go:146-151`.
- Mutations require only the cookie + CSRF token that `/session` just handed out: `internal/api/middleware.go:153-163`. CSRF is not a boundary when the server issues both values on request.
- The cookie is set with `Secure: secureRequest(r)` (`internal/api/server.go:529`), so over the plaintext HTTP listener (8080, also fronted by helper port 80) the session cookie is non-Secure.

**Live proof (review machine)**

```text
> curl.exe -s -D - -H "Host: devstack.test" http://127.0.0.1:8080/api/v1/session
HTTP/1.1 200 OK
Set-Cookie: devstack_session=<redacted>; Path=/; HttpOnly; SameSite=Strict
{"csrf_token":"<redacted>"}

> curl.exe -s -o - -w "%{http_code}\n" -H "Host: devstack.test" http://127.0.0.1:8080/api/v1/sites
200   (empty list, but the endpoint answered unauthenticated)

> curl.exe -s -o - -w "%{http_code}\n" -H "Host: devstack.test" http://127.0.0.1:8080/api/v1/services
200   (full service catalog)

> POST /api/v1/sites with cookie+CSRF, body {}   -> 400 INVALID_REQUEST "name, path, and kind are required"
> POST /api/v1/sites with cookie, no CSRF header -> 403
```

The `400` proves the CSRF/session check **passed** and the handler executed; the `403` shows the only barrier is the token `/session` issues for free.

Reachability beyond loopback — from inside any WSL distro via the host gateway:

```text
> wsl -- bash -lc "curl -s -o /dev/null -w '%{http_code}' -H 'Host: devstack.test' http://172.25.48.1:8080/api/v1/session"
200
> … /api/v1/sites  -> 200
> … /              -> 200  (dashboard UI itself)
```

**Impact**

- Any process inside WSL — i.e. project code, npm/Composer postinstall scripts, test runners — can fully control the Windows daemon: create/delete/restart sites, change source modes, change settings, browse directories.
- `POST /api/v1/sites` accepts **any existing `C:\…` path**: `internal/pathmode/pathmode.go:177-208` rejects only drive roots, UNC/device paths, ADS, non-`C:` drives, and `..`. A site rooted at `C:\Users\<user>` can then be served over its vhost, turning the exposure into **file disclosure** (subject to the dotfile policy). Note the filesystem-browser API itself only lists directories and never returns file contents (`internal/api/handlers_fs.go:15-21`).
- On the LAN: this machine's rule is currently scoped to the WSL subnet, but on **fresh installs the rule can be created unscoped** (H1) — making the API reachable from any device on a Private/Domain network. The HTTP path also leaks session cookies in cleartext (no `Secure`, no HTTPS-only redirect on the API path).
- **The only thing preventing a root-PTY exploit on Windows today is C2**: terminal tickets currently return 503 because the Windows daemon never wires `Terminal`. On a build where C2 is fixed, the same unauthenticated chain issues a ticket and opens `/bin/bash` as **root** in WSL (`build/wsl/agent.service:8-10`, `internal/agent/terminal.go:104-134`).
- Service-credential reveal/rotate routes exist (`internal/api/server.go:474-475`) and are mutation-gated only — again bypassable via the self-issued session.

**Fix (recommended)**

Do not multiplex the control API onto the WSL/LAN-facing listener without a real authentication boundary. Options, in preference order:

1. Require a per-install bearer secret (or an unforgeable signed header) for **every** API request including `GET /session`; the CLI/UI obtain it via a privileged local channel (named pipe with peer-verified SID).
2. Split site traffic from control traffic: dashboard API bound to `127.0.0.1` only; WSL ingress gets a separate authenticated relay.
3. Make the proxy add an internal identity header only for loopback-originated requests and reject all other `devstack.test` traffic at the proxy.

Additionally: set `Secure` whenever serving TLS; consider making the dashboard HTTPS-only and redirecting HTTP; make `/session` require proof of local UI bootstrap.

**Acceptance criteria:** with no cookie/token, `GET /api/v1/session` must not return a usable credential to a non-local-caller; requests to `http://<host-gateway>:8080/api/v1/*` from WSL must fail closed; a regression test must assert this.

---

### C2 — Windows daemon is missing Services, Terminal, and Build wiring

**Status: live-verified (503s). Functional parity broken.**

**Code evidence**

`internal/daemon/windows.go:232-241` builds the API with only:

```go
apiServer := api.NewServer(st, siteService, runtimeService, doctorService, logStore, adapter, api.Config{
    UserHome:      cfg.Home,
    MaxBodyBytes:  int64(cfg.MaxRequestBodyMB) << 20,
    Listeners:     api.ListenerPorts{HTTPPort: cfg.HTTPPort, HTTPSPort: cfg.HTTPSPort, UIPort: cfg.UIPort},
    Vhosts:        siteService,
    ServiceConfig: serviceConfigService,
    Tooling:       toolingService,
    Performance:   performanceService,
    Logger:        logger,
})
```

The Linux daemon passes the real subsystems (`internal/daemon/daemon.go:382-386`):

```go
Services: serviceManager,
Build:    buildService,
Terminal: terminalManager,
```

When `Services` is nil, the API falls back to `readOnlyServiceService`, whose own comment says "Actions fail with 503" (`internal/api/server.go:233-268`). That is why `GET /api/v1/services` *looks* healthy while every action fails — a trap for both users and testers.

**Live proof**

```text
POST /api/v1/services/bogus/install      -> 503 {"code":"SERVICE_UNAVAILABLE","message":"service manager is not configured"}
POST /api/v1/sites/bogus/terminal/session-> 503 {"code":"SERVICE_UNAVAILABLE","message":"site terminal is not available"}
POST /api/v1/sites/bogus/build           -> 503 {"code":"SERVICE_UNAVAILABLE","message":"build service is not available"}
```

**Impact**

The Windows dashboard cannot install/start/stop/restart/remove/inspect Docker services, cannot open a site terminal, and cannot run a site build. The UI Services page renders cards from the read-only catalog, so the breakage is invisible until the user clicks an action. The acceptance suite never exercised any of these paths, which is why "verification" passed.

**Fix (recommended)**

- Construct and wire the real `servicecontrol.Manager`, `terminal.Manager`, and build service in `internal/daemon/windows.go`, mirroring `internal/daemon/daemon.go:245-386`.
- **Or** explicitly gate those routes/pages as unsupported on Windows and remove parity claims — do not ship silent 503s.
- Add service lifecycle, terminal-session, and build cases to `test/windows/vertical_slice.ps1`.

**Acceptance criteria:** on Windows, `POST /api/v1/services/<id>/install` performs a real install; `POST /api/v1/sites/<ref>/terminal/session` returns a usable ticket; `POST /api/v1/sites/<ref>/build` produces build logs. All covered by automated tests.

---

### C3 — Upgrade backup failure is undetectable; rollback can destroy the only copy of all WSL data

**Status: proven by code reading + WSL shell experiment. Highest data-loss risk in the product.**

**Code evidence (the failure chain)**

1. `install.ps1:491` — the backup ends in `|| true`, so the shell always exits 0:

   ```powershell
   & wsl.exe -d devstack -u root -- sh -c "tar -czf /data/devstack-backup.tar.gz -C /data devstack && cp /data/devstack/bin/devstack-agent /data/devstack/bin/devstack-agent.bak 2>/dev/null || true"
   if ($LASTEXITCODE -ne 0) { … exit 1 … }   # line 492 — DEAD CODE
   ```

   Verified in WSL that `sh -c "tar -czf /nonexistent/dir/x.tar.gz /etc || true"` exits **0** after tar fails. Disk-full, permission, or missing-directory failures produce no backup and no error.

2. `install.ps1:542` sets `$upgradeFailed = $true` when the agent copy fails — but `install.ps1:549` **unconditionally resets it to `$false`** before the health check. A failed agent copy is ignored; the upgrade can report success with the old agent still running.

3. `install.ps1:584` — rollback deletes the live tree before any verified restore, with no exit-code checks:

   ```powershell
   & wsl.exe -d devstack -u root -- sh -c "rm -rf /data/devstack && tar -xzf /data/devstack-backup.tar.gz -C /data && rm -f /data/devstack-backup.tar.gz"
   ```

   If the archive is missing or truncated (which step 1 makes undetectable), `rm -rf` still succeeds, extraction fails, and `install.ps1:593` unconditionally prints:

   ```text
   Upgrade rolled back successfully to previous version.
   ```

   All WSL site data, service data/secrets, runtimes, and logs are gone.

4. **Ordering bug:** the Windows-binary staging (section 8, `install.ps1:342-373`) happens *before* the WSL backup (section 10.1, `install.ps1:463+`). `docs/install-windows.md:249-255` documents the opposite order.

5. **Running-helper replacement bug:** `install.ps1:323-338` stops only the daemon (`daemon stop`, `Stop-Process -Name devstack -Force`); it never stops the `DevstackHelper` service before `Copy-Item`/`go build -o` replaces `devstack-helper.exe` (lines 359-369). Replacing a running service image on Windows fails with a sharing violation; with `$ErrorActionPreference = "Stop"` (line 39) the script aborts with a stack trace **and no rollback runs**.

6. **Weak health check:** `install.ps1:555-567` verifies only `systemctl is-active`. `docs/install-windows.md:254` claims a "health probe against the agent HTTPS API" — the code does no such probe.

7. `build/wsl/install-agent.sh:37` starts the agent with `|| true` when credentials are not yet provisioned (fresh install), so a broken agent still yields exit 0, and `install.ps1:1022` sets `agent_installed = $true` regardless.

**Impact**

A failed or partial backup followed by a failed health check **permanently deletes all WSL data** while reporting success. Separately, every real upgrade hits (or will hit) the running-helper sharing violation and aborts uncleanly mid-way, leaving mixed old/new binaries.

**Fix (recommended)**

- Remove `|| true`; check the exit code; validate the archive after creation (`tar -tzf` member count + size floor) before proceeding.
- Extract to a staging directory, verify success, **then** atomically swap `/data/devstack`. Never `rm -rf` the live tree before a complete, verified restore.
- Do not reset `$upgradeFailed`; accumulate staging failures.
- Stop `DevstackHelper` before replacing its binary; restart it afterward (or use a service-aware replacement mechanism).
- Move the WSL backup before any Windows binary staging.
- Replace `systemctl is-active` with a real agent HTTPS health probe (the agent serves one).
- Fail the manifest write (`stage = complete`, `agent_installed = true`) unless every step actually succeeded.

**⚠ For the fixing agent:** do **not** test the destructive paths against the live `/data/devstack`. Use `-TestSandbox` / `DEVSTACK_TEST_MOCK_WSL` mocks, or snapshot/clone the distro first.

**Acceptance criteria:** fault-injected tests for backup failure, extraction failure, agent health failure, and helper-binary replacement all restore prior state with correct exit codes and no data loss; docs updated to match the implemented order.

---

### C4 — Reboot resume runs as SYSTEM with no arguments, breaking per-user WSL registration and helper auth

**Status: proven by code reading + live registry inspection. Installability defect.**

**Code evidence**

- `install.ps1:285-288`:

  ```powershell
  $action = New-ScheduledTaskAction -Execute $psExe -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
  $trigger = New-ScheduledTaskTrigger -AtLogOn
  $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
  Register-ScheduledTask -TaskName $resumeTaskName …
  ```

  **No installer arguments are preserved.** The first run exits `3010` at line 293 — *before* the helper configuration block at `install.ps1:305-313`.

- On the resumed run, executing as SYSTEM:
  - `install.ps1:306-313` writes `allow_sid: "<SYSTEM SID>"` into `helper.yaml` (it doesn't exist yet on a fresh install).
  - `install.ps1:812-825` runs `wsl --import` as SYSTEM, registering the distro in **SYSTEM's** per-user WSL registry (`HKU\S-1-5-18`), invisible to the installing user. Live machine confirms per-user registration is what a working install looks like (`HKCU\...\Lxss` contains `devstack`; SYSTEM hive does not).
  - The helper service is then installed with the SYSTEM SID; `internal/helper/peer_windows.go:16-22` requires the peer SID to **exactly equal** `allow_sid`, so the installing user is rejected — even though the pipe DACL includes Administrators (`internal/helper/peer_windows.go:12-14`).
- Custom arguments are lost on resume: `-InstallDir`, `-RootFSPath`, `-SkipWSL`, `-Upgrade`. A user who installed to `D:\devstack` gets a resumed run against `C:\devstack` defaults.
- The resume task is only unregistered on the success path (`install.ps1:296-299`); if the resumed run fails, it re-runs at every logon.

**Impact**

`-AllowReboot` can produce an installation whose distro is invisible to the user (`wsl -l` doesn't show it) and whose helper rejects the user's CLI. The user must manually repair. This path has never been executed for real (see §7).

**Fix (recommended)**

- Run the resume task as the **interactive user** with the original argument list (persist them to a file and re-inject), or
- Complete the WSL import and helper configuration *before* requesting the reboot, or
- If a SYSTEM task is unavoidable: persist the interactive user's SID and full original args; on resume, register the distro and write `allow_sid` for that user, then verify `wsl -l` shows the distro in the user's context and a helper call succeeds.

**Acceptance criteria:** an end-to-end test (mocked reboot) that resumes with the original args and ends with the distro visible to the installing user and a successful helper call from that user.

---

### C5 — Release scripts fabricate a placeholder rootfs and sign it as real

**Status: proven by code reading. Release-integrity defect.**

**Code evidence**

- `build/release-windows.ps1:113-130`: if no real rootfs is found, a "Minimal rootfs placeholder" is created (temp dir containing only `etc/wsl.conf` and an empty `data/devstack`), tarred, SHA-256 hashed, and written into the release manifest and `.sha256` sidecar. A `-RootFSPath` pointing at a nonexistent file silently falls through to this branch.
- `build/release-windows.sh:199-220`: identical fallback. Its `--mock-rootfs` flag is parsed at line 72 but **never used** — there is no way to distinguish a deliberate mock from an accidentally missing artifact.
- `build/wsl/versions.lock:7`: `rootfs_sha256=release-manifest` — not a hash, so the installer falls through to the sidecar (or to nothing).
- `install.ps1:755-773`: when no expected hash can be resolved (user-supplied rootfs without sidecar), it still logs:

  ```text
  RootFS archive checksum verified (<hash>).
  ```

  The "verified" message is unconditional even when nothing was verified.

**Impact**

A release built without the real rootfs silently ships an installer that imports a nonfunctional distro (the systemd PID-1 check at `install.ps1:875-892` will fail at best). The checksum "matches" because it was computed over the placeholder. Both the release gate and the install-time gate are false assurances.

**Fix (recommended)**

- Fail the release if the real rootfs is missing, unless an explicit `--mock-rootfs`-equivalent flag is set; make the mock unmistakable in the artifact name/manifest.
- Wire `--mock-rootfs` in `release-windows.sh` (currently dead) or remove it.
- Pin a real SHA-256 in `build/wsl/versions.lock`; enforce `rootfs_minimum_bytes=1073741824` at release time and install time.
- Log "checksum verified" only when an expected hash was actually resolved and matched; otherwise warn/fail.

---

## 4. High-severity findings

### H1 — Firewall rule can be created unscoped on fresh installs; never repaired afterward

`install.ps1:415-451`:

- The WSL adapter/subnet is detected **at rule-creation time only** (`:416-422`). If absent (typical on a clean machine, because section 10 runs *before* the WSL import in section 11 at `:812+`), the rule is created with **no `InterfaceAlias` and no `RemoteAddress`** → ports 8080/8443 allowed from **any** address on Private/Domain networks.
- The rule is never repaired after the distro is imported.
- The `netsh` fallback at `:450` is unscoped when `$wslSubnet` is null and only uses `profile=private` (drops Domain).
- `-SkipWSL` installs exit after section 10 (`:454`), so a **no-WSL install still creates the 8080/8443 rule**; if the user later runs the daemon, the dashboard is LAN-reachable (compounding C1).
- Live machine is scoped **only** because the adapter pre-existed.

**Fix:** create/repair the rule after distro import; fail closed if the WSL subnet cannot be determined; never fall back to an unscoped rule; assert the scope in the vertical slice.

### H2 — Installer swallows helper/agent failures and can install a fake agent

- `install.ps1:393-399`: `devstack-helper service install`, `Start-Service`, and `sc start` exit codes are unchecked; a failed helper still yields `stage = complete`.
- `install.ps1:949-961` (fresh install) and `:509-525` (upgrade): `go build` failures are swallowed (`catch {}` / note-only; native command failures don't throw), and if no binary exists a **fake stub** is written:

  ```powershell
  Set-Content -Path $stagedAgentLinux -Value "#!/bin/sh`necho devstack-agent`n"
  ```

  The stub is copied into WSL and installed as `devstack-agent`. `build/wsl/install-agent.sh:37` swallows the start failure on fresh installs; `install.ps1:1022` sets `agent_installed = $true` regardless.

**Fix:** check every native exit code; fail on missing agent binaries; delete the stub fallback; verify `systemctl is-active` **and** a real agent health endpoint before writing the manifest.

### H3 — Native Windows `go test ./...` is red; the acceptance doc claims green

- Failing tests: `internal/phpini/ini_test.go:490` (`TestLintRealPHP`) and `:581` (`TestComposeManagedExtensionsGlobalAfterPathSection`).
- `findPHP` (`ini_test.go:615`) resolves `C:\devstack\bin\php.cmd` on an installed machine; the shim passes Windows paths verbatim to WSL PHP (H5), so `php -c C:\…\php.ini` cannot open the file. This is a test-portability bug triggered by the product's own shims — it will fail for any developer with devstack installed, i.e. the target user.
- `docs/superpowers/status/2026-10-27-windows-implementation-resume.md` is self-contradictory: line 86 records native Windows failures across ~21 packages; §11 line 505 claims `go test ./...` "PASS (Exit 0, 100% green)" at commit `dcb49b6` — which is **4 commits behind** the shipped HEAD `6d7bdaa` (which adds the standalone installer and release changes).

**Fix:** partition real-PHP tests with build tags, or translate paths when the resolved PHP is a WSL shim; re-run the suite at the tagged commit and record the true result.

### H4 — Doctor produces false warnings on a healthy installation

Live `devstack doctor --json` (healthy install):

```json
{"id":"tools","status":"warn","detail":"`php` does not resolve on PATH; managed shim is C:\\devstack\\bin\\php.cmd; `composer` does not resolve on PATH; …"}
{"id":"ports","status":"warn","detail":"port 80 is free; … port 443 is free; … port 7070 is free; … could not probe port 8080 …; could not probe port 8443 …"}
```

But `where php` / `where composer` resolve correctly in a fresh shell (verified with a Go `exec.LookPath` probe), and ports 80/443/7070 **are** bound.

Root causes:

- `internal/doctor/checks_tools.go:31-33,42` uses `exec.LookPath` **inside the daemon process**. The daemon inherits the PATH of whatever shell launched it; here that shell predated the installer's Machine PATH update. There is no PATH-refresh mechanism and the check never consults the persisted Machine/User PATH.
- `internal/doctor/checks.go:338-375` uses a bind probe that cannot determine ownership: a wildcard bind succeeding while a specific-address listener owns the port yields "port is free" (80/443/7070), and a dual-stack `::` listener yields "could not probe" (8080/8443).
- `TestCheckToolsRecognizesWindowsCmdShimsWithoutExecuteBits` is hard-`t.Skip`ped (`internal/doctor/checks_tools_test.go:95`), so the Windows shim-resolution path is never tested.

**Fix:** resolve against persisted Machine+User PATH (and/or restart the daemon after PATH changes); detect listeners on any address/port combination instead of a bare bind probe; unskip and fix the Windows shim test.

### H5 — Windows `.cmd` shims do not translate Windows paths

- `internal/tooling/windows.go:34-36` writes `wsl.exe -d <distro> --exec php %*` — args passed verbatim.
- Site shims (`windows.go:82-86`) likewise pass `"$@"` verbatim inside `bash -lc`.
- Consequence: `php -c C:\path\to\php.ini`, IDE integrations, and any script passing absolute Windows paths fail inside WSL. This is the direct cause of the H3 test failures and a likely user-facing issue.
- The vertical slice only runs `php.cmd -v` (`test/windows/vertical_slice.ps1:252-256`), so it's never caught.

**Fix:** detect drive-letter arguments and convert with `wslpath` (or ship a small translation helper binary); add argv/path-translation tests.

### H6 — No daemon autostart; uninstaller references a task that doesn't exist; upgrade leaves the daemon down

- `build/windows/devstack-daemon-task.ps1` (registers a per-user logon task) exists but is **never invoked** by `install.ps1` — only referenced by `test/windows/installer_test.ps1:30,250-254`.
- After a reboot, the daemon starts only when a CLI command calls `EnsureRunning` (`internal/daemon/windows.go:413+`); the dashboard is down until then. `docs/install-windows.md:224-227` instructs a one-time manual `devstack daemon start` and never mentions reboots.
- `uninstall.ps1:237-247` unregisters the `devstack-daemon` scheduled task, which the installer never creates (header claims at `uninstall.ps1:6-8`).
- The upgrade path stops the daemon (`install.ps1:323-330`) and **never restarts it** on success; the dashboard stays down until the user manually starts it. Docs don't mention this.

**Fix:** register the per-user logon task during install (or explicitly document the manual-start-after-reboot behavior), restart the daemon (or instruct) after upgrade, and remove the stale uninstaller claim.

---

## 5. Medium findings

### M1 — Documentation self-contradictions

- `docs/handoff.md:18` says Windows implementation "**has not started**"; `:264` and `:325` say "Windows/WSL is planned but not implemented" — while `:8` and `:109-121` describe it as delivered. Internally inconsistent and stale.
- The acceptance status doc records evidence at commit `dcb49b6` (4 commits before the shipped tag) and contradicts itself on Windows test status (line 86 vs line 505).
- `docs/install-windows.md:249-255` describes an upgrade workflow (backup-first, HTTPS API health probe, helper restart) that the code does not implement (see C3).

### M2 — Windows 10 22H2 is completely unverified

The product floor is Windows 10 22H2 build 19045 (`docs/decisions.md` S73, reaffirmed in the acceptance doc "Recorded rulings"), but **all** acceptance evidence is Windows 11 build 26200. No Windows 10 install, upgrade, terminal, service, or reboot evidence exists anywhere.

### M3 — Vertical-slice gates are non-enforcing and coverage is missing

`test/windows/vertical_slice.ps1`:

- `:253-258` — passes with `Pass "… (optional before full tooling install)"` when `php.cmd` is **missing**.
- `:261-263` — only checks `composer.cmd` **existence**, never execution.
- No test for: service install/start/stop/remove; terminal session; site build; lifecycle (reboot/autostart); upgrade failure + rollback; firewall scope; foreign-SID pipe rejection. Several checks are pass-without-artifact ("optional") rather than hard gates.

### M4 — Installer tests are mock-only

`test/windows/installer_test.ps1` (92 checks, 0 failures) simulates WSL (`DEVSTACK_TEST_MOCK_WSL`, sandboxes) and never exercises: real backup/rollback, helper-binary replacement against a running service, firewall scoping, reboot-resume, or live distro import. It validates script *text and mock flow*, not real behavior.

### M5 — IPC peer identity has a PID-reuse TOCTOU

`internal/ipc/ipc_windows.go:47-62` resolves the pipe client PID and then opens the process to query its token. A PID reused between those steps is a theoretical impersonation vector; the pipe DACL bounds the impact. Prefer `ImpersonateNamedPipeClient` or deriving identity from the pipe handle directly.

### M6 — TLS pinning is leaf-only, with long-lived certs

`internal/agent/tls.go:132-143` uses `InsecureSkipVerify: true` plus a leaf SHA-256 pin, with no validity/hostname/EKU checks; certificates are valid for 10 years (`tls.go:59`). Low practical risk (pin is exact), but expiry/rotation produces confusing failures and there is no revocation story.

### M7 — Release build mutates a tracked file (`cmd/devstack-installer/payload.zip`)

`build/release-windows.ps1:226-240` copies the release zip into the tracked `cmd/devstack-installer/payload.zip` (repo version is a 22-byte empty-zip sentinel consumed by `locatePayload`, `cmd/devstack-installer/installer.go:133+`), builds `devstack-installer.exe`, and restores the sentinel in `finally`. A hard-killed build leaves the full release payload in the repo; a later plain `go build` then embeds a stale release. (`release-windows.sh:406-417` has the same pattern.)

### M8 — Upgrade leaves the daemon stopped and helper possibly stale

Beyond C3: on upgrade success the installer does not restart the daemon or verify the helper is running the new binary; `service install` + `Start-Service` on an already-running service is a no-op/error that is swallowed (`install.ps1:393-399`). The user is left with a down dashboard and no documented step.

---

## 6. Solid areas and design observations

Things that held up under review (do not regress these while fixing):

- `go build` / `go vet` green on Windows; full Linux suite green in ext4; race detector clean on 13 packages.
- UI type-check (0/0), 118 unit tests, production build all green.
- `internal/pathmode` is thorough: rejects UNC/device paths, ADS, `..`, non-`C:` drives; resolves symlinks/reparse ancestors for containment (`pathmode.go:137-155,177-208`).
- Helper pipe DACL is protected; normal-path authorization requires the exact user SID (`internal/helper/peer_windows.go:12-22`).
- Daemon control pipe is user-SID-scoped; `PeerPolicy.Authorize` always allowing SYSTEM (`internal/ipc/ipc.go:99-108`) is reasonable for service maintenance but is what C4 weaponizes when `allow_sid` is mis-provisioned.
- `internal/agentrelay` binds `127.0.0.1` by default and enforces a fixed service-port allowlist.
- Uninstaller preserves data by default; purge is opt-in.
- The live firewall rule is correctly scoped on this machine (because the adapter pre-existed — see H1 for the fresh-install case).
- The installer sandbox test suite is a useful regression net as far as it goes (M4 describes its limits).

---

## 7. Unverified claims / test-matrix gaps

These claims are **not** backed by evidence at the shipped commit. Treat them as unproven until tested:

1. **Windows 10 22H2 (19045)** — never tested at all (M2).
2. **Clean-machine install** — the live machine was already provisioned; no first-run-from-scratch evidence.
3. **Real reboot-resume** (`-AllowReboot`) — never executed; C4 is code-proven only.
4. **Real upgrade + rollback fault injection** — never executed on a real distro; C3 is code-proven plus a WSL shell experiment.
5. **Firewall scoping on a fresh install** — untested; H1 suggests it will be unscoped.
6. **`vertical_slice.ps1` at the shipped commit** — not re-run in this review (state-mutating); the acceptance doc's evidence predates HEAD by 4 commits and covers none of C2's broken paths.
7. **"All Windows tests pass"** — false at HEAD (H3).
8. **Foreign-SID pipe rejection** — untested on Windows.
9. **LAN reachability** — the review machine's rule is scoped, so LAN exposure was not directly observed; it is inferred from H1's fresh-install code path plus C1.

---

## 8. Remediation roadmap and acceptance criteria for the fixing agent

Work in this order. Each item lists what "done" means.

| # | Item | Done when |
|---|---|---|
| 1 | **C1** auth boundary on the dashboard API + public proxy | `GET /api/v1/session` returns no usable credential to non-local callers; WSL gateway requests to `/api/v1/*` fail closed; regression test added |
| 2 | **C3** transactional upgrade | Fault-injected tests (backup fail, extract fail, health fail, helper replace) all restore prior state, correct exit codes, no data loss; docs match code |
| 3 | **C4** user-context resume | Mocked reboot-resume test ends with distro visible to the installing user and a successful helper call from that user |
| 4 | **C2** wire Windows Services/Terminal/Build | Live endpoints perform real work; vertical slice covers all three |
| 5 | **C5** fail-closed release | Release without a real rootfs fails loudly; real pinned SHA-256; size floor enforced; mock path unmistakable |
| 6 | **H1** firewall fail-closed | Rule always scoped to the WSL subnet/interface; created/repaired after distro import; unscoped fallback removed; slice asserts scope |
| 7 | **H2** exit-code hygiene | Every native invocation checked; stub deleted; manifest only written on verified success |
| 8 | **H3** test fixes | Windows `go test ./...` green at the tagged commit (partition/translate), recorded in docs |
| 9 | **H4** doctor accuracy | No false warnings on a healthy install; Windows shim test unskipped and passing |
| 10 | **H5** shim path translation | `php.cmd -c C:\…` works; argv/path tests added |
| 11 | **H6** lifecycle | Autostart registered or documented; daemon restarted after upgrade; uninstaller claims corrected |
| 12 | **M1–M8** | Docs reconciled; Windows 10 matrix scheduled; vertical slice hardened; IPC/TLS hardening decisions recorded; payload.zip handling made kill-safe |
| 13 | **Re-run all gates** | `go build/vet` (Windows), `go test ./...` (Windows + Linux ext4), `-race` subset, UI check/test/build, installer_test.ps1, updated vertical slice — all green **at the tagged commit**, with results recorded |

**Testing safety rules for the fixing agent:**

- Never run destructive upgrade/rollback tests against the live `/data/devstack` — use `-TestSandbox`, `DEVSTACK_TEST_MOCK_WSL`, or clone/snapshot the distro first.
- The public-proxy probes (C1) are read-only except the single `POST /api/v1/sites` CSRF probe with body `{}`, which was verified to fail validation without creating anything. Prefer reproducing with an invalid payload.
- The review left a disposable Linux copy at `/data/devstack/review` (WSL ext4) for test runs.

---

## 9. Verification command appendix

Exact commands used to produce the live evidence (redacted where personal). Run these to confirm each finding before/after fixing.

```powershell
# C1: session minting via public proxy (Host-preserved)
curl.exe -s -D - -H "Host: devstack.test" http://127.0.0.1:8080/api/v1/session
curl.exe -s -o - -w "%{http_code}\n" -H "Host: devstack.test" http://127.0.0.1:8080/api/v1/sites
curl.exe -s -o - -w "%{http_code}\n" -H "Host: devstack.test" http://127.0.0.1:8080/api/v1/services

# C1: CSRF acceptance proof (invalid payload → 400 means CSRF passed; no CSRF → 403)
$cookie = "devstack_session=<value-from-session-response>"
$csrf   = <csrf_token-from-session-response>
curl.exe -s -w "`nstatus=%{http_code}`n" -X POST -H "Host: devstack.test" -H "Cookie: $cookie" `
  -H "X-CSRF-Token: $csrf" -H "Content-Type: application/json" --data "{}" http://127.0.0.1:8080/api/v1/sites

# C1: reachability from WSL via host gateway
wsl -- bash -lc "curl -s -o /dev/null -w 'session=%{http_code}\n' -H 'Host: devstack.test' http://172.25.48.1:8080/api/v1/session"

# C2: 503 wiring gaps
# (with a session cookie + CSRF as above)
curl.exe -s -w "`nstatus=%{http_code}`n" -X POST -H "Host: devstack.test" -H "Cookie: $cookie" `
  -H "X-CSRF-Token: $csrf" -H "Content-Type: application/json" --data "{}" http://127.0.0.1:8080/api/v1/services/bogus/install
curl.exe -s -w "`nstatus=%{http_code}`n" -X POST -H "Host: devstack.test" -H "Cookie: $cookie" `
  -H "X-CSRF-Token: $csrf" -H "Content-Type: application/json" --data "{}" http://127.0.0.1:8080/api/v1/sites/bogus/terminal/session
curl.exe -s -w "`nstatus=%{http_code}`n" -X POST -H "Host: devstack.test" -H "Cookie: $cookie" `
  -H "X-CSRF-Token: $csrf" -H "Content-Type: application/json" --data "{}" http://127.0.0.1:8080/api/v1/sites/bogus/build

# C3: prove `|| true` masks tar failure (WSL, non-destructive)
wsl -- bash -c 'sh -c "tar -czf /nonexistent/dir/x.tar.gz /etc || true"; echo exit=$?'   # → exit=0

# C4: per-user WSL registration facts
Get-ChildItem HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss |
  ForEach-Object { $p = Get-ItemProperty $_.PSPath; "$($_.PSChildName) $($p.DistributionName) $($p.BasePath)" }
sc.exe qc DevstackHelper    # inspect --allow-sid in binpath

# H1: firewall rule scope
Get-NetFirewallRule -Name devstack-daemon-proxy |
  Select-Object DisplayName,Enabled,Profile,Direction,Action
Get-NetFirewallRule -Name devstack-daemon-proxy | Get-NetFirewallAddressFilter
Get-NetFirewallRule -Name devstack-daemon-proxy | Get-NetFirewallPortFilter

# H4: doctor false warnings + fresh-shell PATH resolution
go run ./cmd/devstack doctor --json
cmd /c "where php & where composer"

# H3: the failing native tests
go test ./internal/phpini -run "TestLintRealPHP|TestComposeManagedExtensionsGlobalAfterPathSection" -v

# H1/H6: listeners and autostart reality
Get-NetTCPConnection -State Listen | Where-Object { $_.LocalPort -in 80,443,7070,8080,8443 } |
  Select-Object LocalAddress,LocalPort,OwningProcess
Get-ScheduledTask | Where-Object { $_.TaskName -match "devstack" }
```

---

# Round 2 — Deeper Audit (2026-10-28, same commit `6d7bdaa`)

**Scope of round 2:** areas round 1 skimmed — Windows helper RPC surface (SYSTEM context), root WSL agent auth/terminal/docker, wsl.exe argument construction, config injection, daemon Windows lifecycle internals, install.ps1 DACL/wsl.conf/test-escapes, uninstall.ps1 hosts handling, proxy router/PHP transport internals, UI XSS, CSRF internals, securestore, logs redaction, certs renewal.

**Gates actually run in round 2:**

| Gate | Result |
|---|---|
| Native Windows `go test -race ./...` | **NOT RUNNABLE** — `go: -race requires cgo` (no Windows C toolchain). Race coverage stays WSL-only. Test-infra note, not a product bug. |
| `go test ./internal/phpini -run TestComposeManagedExtensionsGlobalAfterPathSection -v` | FAIL — root cause fully attributed (see R2-resolved below); **not** a `Compose` bug. |
| Live 8.3-short-name resolution probe (scratch dir + Go probe replicating `resolvePHPTarget`) | **Reproduced** — see H7 below. |
| Live WSL probes of the installed PHP build | `opcache.so` location + compiled-in `extension_dir` inspected — see M10. |

## Round-2 findings summary

| Severity | IDs | Areas |
|---|---|---|
| **High** | R2-H7 | Windows serving-mode switch (CLI/UI) silently fake-succeeds; docker-web serving is dead code on Windows |
| **High** | R2-H8 | NTFS 8.3 short names bypass the proxy's dotfile/backup denylist → `.env`/`.git` disclosure on Windows-hosted sites |
| **Medium** | R2-M9 | Uninstaller hosts cleanup: regex overreach + UTF-8 BOM rewrite under Windows PowerShell 5.1 |
| **Medium** | R2-M10 | Relocated PHP runtime has a compiled-in `extension_dir` pointing at a nonexistent `/tmp` staging path |
| **Medium** | R2-M11 | Installer honors `DEVSTACK_TEST_*` environment overrides in real (non-sandbox) installs |
| **Medium** | R2-M12 | Rootfs has no default non-root user; every shim-driven PHP/composer run executes as root |
| **Low** | R2-L1 | CSRF secret fallback uses nanotime+pointer hash if crypto/rand fails (should fail closed) |
| **Low** | R2-L2 | Log redaction misses secret names without the literal substrings (`DB_PASS`, `MYSQL_PASS`) |
| **Low** | R2-L3 | Agent terminal WS disables Origin checking (`InsecureSkipVerify`); ticket carried in query string (defense-in-depth only) |

---

## R2-H7 — Windows serving-mode switching fake-succeeds; docker-web serving is dead code on Windows

**Status: proven by code reading; user-reachable via `devstack serving nginx`.**

The Windows daemon's helper adapter silently converts every vhost failure into a fabricated success:

```go
// internal/daemon/windows.go:731-759 (commit 6d7bdaa)
func (h *helperAdapter) VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error) {
    c := h.get()
    if c == nil { return builtin/ConfigOK:true, nil }          // no helper → "builtin, OK"
    resp, err := c.VhostStatus(ctx)
    if err != nil { return builtin/ConfigOK:true, nil }        // any error → "builtin, OK"
    return resp, nil
}
func (h *helperAdapter) SyncVhosts(...) error {
    ...
    _ = c.SyncVhosts(ctx, req)                                 // error discarded
    return nil
}
func (h *helperAdapter) SetServingMode(...) (helperproto.VhostStatusResponse, error) {
    ...
    resp, err := c.SetServingMode(ctx, mode, req)
    if err != nil { return builtin/ConfigOK:true, nil }        // failure → success
    return resp, nil
}
```

Meanwhile the Windows helper exposes **no vhost routes at all**: `internal/helper/winserver.go` mounts only Status, HostsSync, EnsureCA, Sign, Trust*, Forwarders, ProxyTargets, Uninstall — `vhosts.go`/`SetServingMode` handlers are `//go:build !windows`. So every `c.SetServingMode`/`c.VhostStatus` call from the daemon fails, and the adapter lies about it.

Additionally, `dockerweb.NewWindowsProvider` is constructed **only in tests** (`internal/dockerweb/windows_provider_test.go`); neither the Windows daemon nor any production path wires it, so `UpdateDockerWebRoutes` and the whole Windows Docker-web switch (`internal/dockerweb/windows_provider.go`) is unreachable dead code on Windows.

**Impact chain (all real, no helper defects needed):**

1. User runs `devstack serving nginx` (or clicks the UI equivalent): `internal/cli/vhost.go:105` → daemon API → `sites.SetServingMode` (`internal/sites/vhosts.go:263+`) → `helperModeLocked` reads the adapter's fabricated `builtin/OK` → `switchBuiltinToExternal` → adapter "succeeds" → `sites.SetServingMode` persists `serving_mode: nginx` in config and store believing the helper switched. **Builtin keeps serving; nothing changed on the system.**
2. Every daemon boot: `ReconcileServingMode` (`internal/daemon/windows.go:366`) re-reads the fabricated status, sees `builtin ≠ nginx`, calls `SetServingMode(nginx)` again → silent fake success forever.
3. `doctor`'s vhost check reads `VhostStatus` → `ConfigOK: true` always — never warns about the drift (compounds H4's false warnings).
4. `SyncVhosts` failures are discarded (`_ =`), so even config-sync activity reports success.

This is worse than C2's honest 503s: it is **fabricated success**, and it leaves persisted state (`serving_mode: nginx`) disagreeing with reality.

**Fix (recommended):** delete the error-swallowing branches and surface real errors (the sites service already handles `ErrNotImplemented` for old helpers); either implement vhost routes on the Windows helper or make the API/CLI reject serving-mode switching on Windows with a clear "not supported on Windows" error; persist nothing on unsupported paths.

**Acceptance criteria:** `devstack serving nginx` on Windows fails with an explicit unsupported error (or actually switches); no adapter method fabricates success; a regression test asserts error propagation.

---

## R2-H8 — NTFS 8.3 short names bypass the proxy dotfile/backup denylist (`.env`/`.git` disclosure)

**Status: live-reproduced on the review machine (scratch dir; no live site touched).**

`internal/proxy/php.go` resolves PHP-route targets as follows:

1. `resolvePHPTarget` (`php.go:281-295`) rejects dotfile segments and backup suffixes **by inspecting the raw request path segments** (`strings.HasPrefix(segment, ".")`, `phpBackupSuffixes`).
2. `evalPHPPath` (`php.go:432-450`) then resolves the candidate with `filepath.EvalSymlinks` and checks only **containment** (`phpPathWithin`) — it never re-applies the denylist to the *resolved* name.

On NTFS volumes with 8.3 name generation enabled (default on `C:`), short aliases resolve to the real dotfile:

```text
> cmd /c dir /x %TEMP%\ds83
10/28/2026  01:46 AM    <DIR>          GIT~1        .git
10/28/2026  01:46 AM                21 ENV~1        .env
```

A Go probe replicating the exact resolution logic (dot-segment precheck → `filepath.EvalSymlinks` → `filepath.Rel` containment):

```text
$ go run main.go C:\...\ds83 ENV~1
resolved: C:\...\ds83\.env
containment: true
=> evalPHPPath would return phpFound (file served)

$ go run main.go C:\...\ds83 GIT~1\config
resolved: C:\...\ds83\.git\config
containment: true
=> evalPHPPath would return phpFound (file served)
```

**Impact:** for any site whose Windows-visible document root (`ProxyDocumentRoot`) is on a local drive with 8.3 names enabled — i.e. every windows-source site under `C:\Users\...` (round 1 C1 documents that any existing `C:\` path may become a site root) — a request for `/ENV~1` serves `.env` (DB credentials) and `/GIT~1/config` serves `.git` contents. The denylist on backup files (`index.php~` → `INDE~1`-style aliases) is bypassed the same way. WSL-hosted roots (`\\wsl.localhost\...`, 9P) are unaffected — no 8.3 aliases there.

**Fix (recommended):** after `EvalSymlinks`, re-run the same segment denylist against the **resolved** path's segments (or require the resolved relative path to equal the cleaned request path modulo case); equivalently, deny any resolved name whose final component starts with `.` or matches the backup suffixes. Add Windows regression tests with generated short names (`fsutil file setshortname` or dir /x).

**Acceptance criteria:** `GET /ENV~1` and `GET /GIT~1/config` return 404 against a Windows-hosted test site; a unit test covers short-name aliases.

---

## R2-M9 — Uninstaller hosts cleanup: regex overreach + BOM rewrite

`uninstall.ps1:160-193`:

1. **Regex overreach.** The installer/Go helper write the exact markers `# BEGIN devstack` / `# END devstack` (`internal/hosts/hosts.go:22-25` — the Go parser even matches by *exact trimmed line equality*). The uninstaller instead matches `(?i)^#\s*BEGIN\s+devstack` / `(?i)^#\s*END\s+devstack`, which also matches any user comment such as `# BEGIN devstack-experimental` or a lowercase `# begin devstack`. A stray user line then starts block deletion, dropping all unrelated hosts entries until the next `END`-ish line (or, if no END exists, **to end of file**). The Go-side parser's strictness (rejects malformed blocks) is not mirrored here.
2. **UTF-8 BOM.** `Set-Content -Path $hostsPath -Value $cleanedText -Encoding utf8` — under Windows PowerShell 5.1, `-Encoding utf8` writes a **UTF-8 BOM**. A BOM at the top of `C:\Windows\System32\drivers\etc\hosts` corrupts the first line's parsing (the BOM bytes prefix the first entry). Any third-party tool entry at the top of the file breaks after an uninstall run under `powershell.exe` 5.1 (the default shell when scripts are launched from Explorer/right-click or legacy task entries). The installer's Go helper path is unaffected (raw byte writes).
3. Minor: the rewrite also normalizes every line ending and trims trailing whitespace — acceptable, but combined with (2) it makes the whole-file rewrite riskier than needed. Prefer reading/writing raw bytes and removing only the exact marker-delimited block (the Go `hosts.Render` logic already exists — consider doing the cleanup from the helper, or at minimum `utf8NoBOM`).

**Acceptance criteria:** uninstaller removes exactly the `# BEGIN devstack`/`# END devstack` block; hosts file after uninstall has no BOM and is byte-identical outside the removed block; test runs under both PowerShell 5.1 and 7.x.

---

## R2-M10 — Shipped PHP runtime's compiled-in `extension_dir` points at a nonexistent `/tmp` staging path

**Status: live-verified on the installed runtime.**

```text
> wsl -d devstack -- php -i | grep ^extension_dir
extension_dir => /tmp/devstack-php-build-xhqbqd/php-8.3.36-r1-linux-x64/lib/php/extensions/no-debug-non-zts-20230831

> wsl -d devstack -- bash -lc 'ls /tmp/devstack-php-build-* 2>/dev/null'
(no output — directory does not exist)

> wsl -d devstack -- bash -lc 'ls /data/devstack/runtimes/php@8.3.36-r1/lib/php/extensions/'
opcache.so   (flat — no API subdirectory)
```

The runtime was compiled under a `/tmp/devstack-php-build-*` prefix and relocated to `/data/devstack/runtimes/...`. The product flow compensates: `tooling.php.extensionDir` (`internal/tooling/php.go:384-406`) detects the flat layout and devstack always composes `extension_dir` into site/global inis, so normal site traffic works. But **any PHP invocation that does not load devstack's ini cannot load any shared extension** — a bare `php -r`, an IDE-launched `php` with a custom `-c`, or `php -n`. Error messages reference the bogus `/tmp` path (confusing diagnostics; observed in round 2's opcache probe). `doctor`'s `verifyPHPBuild` (`internal/doctor/checks_php.go:163-200`) only checks `.so` presence and would not catch a build whose compiled-in dir is wrong.

**Fix (recommended):** build with a stable prefix (e.g. `-prefix /data/devstack/runtimes/php@<id>`) or patch `extension_dir` into the build's `php.ini` defaults / use `--with-config-file-scan-dir` under the runtime tree; add a doctor check comparing the compiled-in extension_dir against the installed layout.

---

## R2-M11 — Installer honors `DEVSTACK_TEST_*` environment overrides in real installs

The test hooks are gated in some places by `$isSandbox` but several apply **unconditionally**, including in real installs:

- `install.ps1:939-941` — `DEVSTACK_TEST_ROOTFS_SHA256` **overrides the expected rootfs checksum** in a real install (a user with this env var set "verifies" any artifact against its own hash).
- `install.ps1:106-113` — `DEVSTACK_TEST_ELEVATED=true/false` overrides the elevation check (a non-admin shell can be told it is elevated; later steps then fail confusingly, or an unintended elevation posture is assumed).
- `install.ps1:130-131, 142-143` — `DEVSTACK_TEST_OS_ARCH` / `DEVSTACK_TEST_OS_BUILD` override OS detection (a 32-bit OS claiming `AMD64` proceeds).
- `install.ps1:159-161` — `DEVSTACK_TEST_VIRTUALIZATION` overrides the virtualization check.
- `install.ps1:814-818` — `DEVSTACK_TEST_WSL_VERSION` overrides WSL presence/version detection.
- `install.ps1:1091-1093` — `DEVSTACK_TEST_SYSTEMD_PID1` overrides the systemd PID-1 verification.
- `install.ps1:967-973` — `DEVSTACK_TEST_DISTRO_EXISTS` overrides the managed/unmanaged distro check.

Any environment pollution (CI leftovers, another tool, malware) silently disables these install-time safety gates. The mock-WSL switch (`DEVSTACK_TEST_MOCK_WSL`) is at least partially gated by `$isSandbox` in most branches, but the ones above are not.

**Fix:** require `-TestSandbox` (or an explicit opt-in flag) before honoring ANY `DEVSTACK_TEST_*` variable; log loudly when test overrides are active.

---

## R2-M12 — Rootfs has no default non-root user; shims run PHP/Composer as root

`build/wsl/build-rootfs.sh` creates a Debian rootfs with systemd only — **no regular user is created, and neither the build-time nor the installer-written `/etc/wsl.conf` has a `[user]` section** (`install.ps1:1040-1049`; `build-rootfs.sh:48-60`). `wsl --import` without a default-user configuration defaults to `root`. Consequently:

- `wsl.exe -d devstack --exec php` / `composer` (the shims from `internal/tooling/windows.go:34-36`) run as **root** in the distro.
- Composer `post-install-cmd` scripts and any site code executed through the shims run as root.
- Combined with the agent's root privileges (C1's terminal impact), everything inside the distro executes with maximum privilege; there is no least-privilege boundary inside WSL at all.

The threat model already treats the distro as root-owned (the agent runs as root by design, `build/wsl/agent.service:9`), but the shim path makes *ordinary project workflows* run as root, not just management actions.

**Fix (recommended):** either create a `devstack` user in the rootfs + `[user] default=devstack` in wsl.conf and run shims/agent under it (larger change to the auth model), or at minimum document explicitly that everything in the managed distro runs as root and keep file ownership expectations (site files owned by root) in the docs.

---

## R2-L1 — CSRF secret fallback should fail closed

`internal/api/server.go:371-374`:

```go
if _, err := rand.Read(s.secret[:]); err != nil {
    s.secret = sha256.Sum256([]byte(fmt.Sprintf("%d-%p", time.Now().UnixNano(), s)))
}
```

If `crypto/rand` fails, the CSRF secret degrades to a SHA-256 of a nanosecond timestamp plus a pointer value — predictable to an attacker who can approximate boot/process layout. On Windows this branch is effectively unreachable (`ProcessPrng` does not fail in practice), but the correct behavior is to abort startup (return an error), not to continue with a weak secret. Same pattern should be checked in any other token-generation fallback.

---

## R2-L2 — Log redaction misses common secret names

`internal/logs/redact.go:11-19` only matches names containing the literal substrings `key|token|secret|password`. Environment-assignment names that abbreviate are **not** redacted in site build/service logs: `DB_PASS=...`, `MYSQL_PASS=...`, `DB_PWD=...`, `PG_PASS=...` etc. pass through. (The test suite only covers names containing the full words.) Either extend the pattern to `(?:password|passwd|pass|pwd|token|secret|key)` or maintain an explicit denylist of common credential variable names.

---

## R2-L3 — Agent terminal WS hardening notes

`internal/agent/terminal.go`:

- `websocket.AcceptOptions{InsecureSkipVerify: true}` (terminal.go:127) disables Origin checking on the agent-side upgrade. Defense-in-depth holds (bearer token wraps all routes per `internal/agent/auth.go`, plus a one-time ticket), so this is hardening, not a bypass: recommend enabling Origin verification or at least documenting why it is skipped.
- The ticket travels in the URL query string (`terminal.go:107`) — it can appear in access logs/proxies between the dashboard and agent. Tickets are single-use with a 30 s TTL and are consumed atomically (`consumeTerminalTicket`, terminal.go:87-99 — mutex + delete before validation; no race), which bounds the impact. Prefer a header or the `Sec-WebSocket-Protocol` channel if the client stack allows.
- PTY size/resize values (`uint16` cols/rows) are passed to `pty.Setsize` unvalidated — harmless (kernel clamps), noting for completeness.
- No per-site or global cap on concurrent PTY sessions; an unauthenticated-ticket flood is bounded by the ticket-mint rate (authenticated mutation), so impact is minor.

---

## Round-2 resolved leads (do NOT "fix" these — they verified correct)

Documented explicitly so the fixing agent does not burn time or introduce regressions:

1. **`phpini.Compose` is correct.** The suspected opcache path-scoping bug does not exist: `Compose` writes a `[PHP]` reset both after the build base (ini.go:117) and after the site/raw section before the managed extensions block (ini.go:150-151). The Windows test failure is fully attributable to H5 (the shim passes the `-c C:\Users\...` ini path verbatim, so WSL PHP never loads the file) plus the probe config omitting `extension_dir` against the relocated build (M10). Evidence: the failure dump shows the correct `[PHP]` reset in the composed output; loading the same ini via a translated `/mnt/c/...` path in WSL parses it (opcache then fails only because of M10's missing default extension dir, which the real product flow always sets via `tooling.php.composerExtensions`).
2. **Windows helper RPC surface is sound.** Every route is gated by exact-SID peer authorization + protocol header (winserver.go middleware); HostsSync validates all domains and caps at 1000; Sign verifies the SAN set equals the requested set; Trust install/remove write only fixed paths; Uninstall performs only fixed deprovisioning operations (forwarders off, hosts clear, Firefox policy/root/firewall removal).
3. **Agent auth is solid.** Empty token rejected; `authenticate` (constant-time compare) wraps every mounted route including Health/Capabilities and all modules; module mount paths must live under `/v1/`; token/TLS-key/dir permissions enforced (0600/0700); TLS MinVersion 1.2; listener addresses are loopback + private NAT only.
4. **wsl.exe argument construction is injection-safe** at every audited call site: scripts travel as argv or base64 (`wsl.go` validate/probe, `cli/init_windows.go` transfer — base64 blobs into a fixed `sh` template), user data is POSIX-single-quoted (`tooling.BuildWSLExecArgs`, site shims), distro names are pattern-validated, site names are `[a-z0-9-]`. The only verbatim-passing path is the global tool shim `%*` (H5).
5. **Agent ingress is fixed-target.** The WSL-side ingress proxies only WSL 127.0.0.1:80/443 → discovered private gateway 8080/8443, validates the gateway is private, and refreshes atomically on gateway change. No request-controllable target.
6. **Proxy HTTP transport is clean.** Branded error pages use `html/template` (auto-escaped); client `X-Forwarded-*` headers are stripped and re-synthesized for both HTTP and FastCGI upstreams; upstream URLs must be `host:port` with no scheme/path; hop-by-hop headers (including Connection-listed) are dropped in both directions; request-body ceilings enforced with `http.MaxBytesReader` and handled as branded 413s.
7. **Daemon Windows lifecycle is sound.** The lifetime `LockFileEx` lock is acquired before listeners; the readiness marker is removed on dial failure (`readyWindows` self-heals a crashed daemon's stale marker by probing the control pipe); the control pipe DACL is the current user's SID only; `/control/stop`-style routes live only on the pipe server, while the TCP listener serves the API handler (which carries the socket-auth wrapper for non-control paths).
8. **UI has zero `@html`/`innerHTML`/`eval` sinks** (full source scan; xterm usage is import-only).
9. **Certs manager is well-formed**: 90-day leaves, 30-day renewal window, independent per-cert renewal with old files kept on failure, 0600 keys/0644 certs, load-concurrency-safe.
10. **Logs redaction** covers the common forms it targets (Authorization, quoted JSON secrets, URI userinfo, cookies) with a test suite — see R2-L2 for the naming-pattern gap.

---

## Round-2 additions to the remediation roadmap

| # | Item | Done when |
|---|---|---|
| 14 | **R2-H7** error truthfulness | No adapter method fabricates success; `serving` on Windows fails explicitly or works; regression test for error propagation |
| 15 | **R2-H8** 8.3-name bypass | Resolved-path denylist re-check; Windows short-name regression test; `ENV~1`/`GIT~1` return 404 |
| 16 | **R2-M9** uninstaller hosts | Exact-marker removal; no BOM under PS 5.1; byte-identical outside block |
| 17 | **R2-M10** runtime relocation | Compiled-in `extension_dir` valid or always-compensated; doctor check added |
| 18 | **R2-M11** test-escape gating | All `DEVSTACK_TEST_*` honored only under `-TestSandbox`/explicit flag; loud logging |
| 19 | **R2-M12** rootfs user model | Non-root default user (or explicit documented root-only design with ownership notes) |
| 20 | **R2-L1–L3** hardening | fail-closed secret init; redaction name-pattern extension; terminal WS origin/ticket hardening decisions recorded |

## Round-2 verification commands

```powershell
# R2-H8: reproduce the 8.3 bypass (scratch dir, no live site touched)
$root = "$env:TEMP\ds83"; New-Item -ItemType Directory -Force -Path "$root\.git" | Out-Null
Set-Content -Path "$root\.env" -Value "DB_PASSWORD=hunter2"
cmd /c "dir /x $root"            # shows ENV~1 / GIT~1
# then request /ENV~1 against a windows-source site's vhost → serves .env

# R2-H7: observe the fake success (run BEFORE fixing)
devstack serving nginx          # "succeeds"; then:
devstack serving status         # reports builtin/OK while config says nginx
go run ./cmd/devstack doctor --json   # vhost check passes falsely

# R2-M10: compiled-in extension_dir points at /tmp staging
wsl.exe -d devstack --exec php -i | Select-String extension_dir
wsl.exe -d devstack --exec bash -lc "ls /tmp/devstack-php-build-*"

# R2-M9: BOM proof under PS 5.1
powershell.exe -NoProfile -Command "'x' | Set-Content -Path $env:TEMP\bom.txt -Encoding utf8"
Format-Hex $env:TEMP\bom.txt | Select-Object -First 1    # EF BB BF prefix

# R2-M11: escape honored in real installs
$env:DEVSTACK_TEST_OS_BUILD = "999999"; install.ps1 -DryRun   # OS check bypassed
```

---

*End of round 2. Verdict unchanged: NOT 100% bullet-proof — round 2 adds two High findings (fabricated serving-mode success; 8.3 dotfile bypass) and four Medium findings to the round-1 list. Total after round 2: 7 High, 12 Medium, plus low/hardening notes.*


---

## Post-remediation status at HEAD `6f74dcc` (2026-10-29)

Rounds 1-2 were written against `6d7bdaa`. Between that review and this update, the concurrent remediation agent committed 43 commits (through `6f74dcc`). The observations below are the current disposition; they do **not** rewrite the historical evidence in Rounds 1-2.

### Gates actually re-run at HEAD

| Gate | Result |
|---|---|
| `go build ./...` | **PASS** (exit 0) |
| `go vet ./...` | **PASS** (exit 0) |
| `go test ./...` (native Windows) | **PASS** (all packages; previous `internal/phpini` failures are gone) |
| `go test ./internal/proxy` (NTFS 8.3/ADS regression set) | **PASS** |
| `npm --prefix ui test -- --run` | **PASS** (15 files, 120 tests) |
| `npm --prefix ui run check` | **PASS** (0 errors, 0 warnings) |
| `npm --prefix ui run build` | **PASS** |
| `test/windows/installer_test.ps1` | **PASS** (188 checks, 0 failures, mock/sandbox only) |
| `build/release-windows.ps1 -Version 0.0.0-audit -SkipUI -MockRootFS` | **PASS** (artifact produced; contents inspected - see R4-C1) |

Native Windows `go test -race ./...` still requires CGO and was not run; race coverage remains WSL-only. The gates above are fresh runs, not claims from the remediation agent.

### Disposition of earlier findings

| Finding | Current status | Notes |
|---|---|---|
| **C1** remote API boundary | **Partially fixed** | API now rejects non-loopback callers (`internal/api/middleware.go`), proxy rejects non-loopback `devstack.test`, and tests cover it. The caller has no Windows user identity, so a *different local user* can still mint a session; see R4-H1. |
| **C2** missing Services/Terminal/Build wiring | **Partially fixed, still broken** | Managers are constructed and injected (`internal/daemon/windows.go:243-299`), but terminal and build are nonfunctional on Windows (R4-H2). |
| **C3** upgrade backup/rollback | **Partially fixed** | Backup now happens before Windows staging, archive is validated, and extraction is staged. The rollback shell script still has no exit-code check and prints success unconditionally (R4-H5). |
| **C4** reboot resume as SYSTEM | **Code changed, not re-executed** | Resume task, SID persistence, and argument dedup were added; the live reboot path was not repeated. |
| **C5** placeholder rootfs / release integrity | **Mostly fixed** | Release fails closed on missing/short rootfs; `rootfs_sha256` is a real digest. The shipped payload still omits the WSL provisioning assets (R4-C1). |
| **H1** firewall scope | **Code changed** | Rule creation moved after WSL import and fails closed on unknown subnet. Fresh-install proof was not repeated. |
| **H2** fake agent stub | **Fixed in code** | Installer now refuses to install a placeholder agent and checks build exit codes. |
| **H3/H5** shim path translation | **Fixed, tested** | Windows drive and WSL UNC translation was added; tests pass. |
| **H4** doctor false warnings | **Fixed, tested** | Port, PATH, tool, and PHP-build checks were reworked; native tests pass. |
| **H6** daemon autostart | **Fixed in code** | Logon task + hidden VBS runner added. |
| **R2-H7** fake serving-mode success | **Fixed** | Windows rejects external serving modes and surfaces helper errors; tests added. |
| **R2-H8 / R3-H9** NTFS 8.3 bypass | **Fixed** | Resolved-path segment denylist + ADS/trailing-punctuation checks; `internal/proxy` regression tests pass. |
| **R2-M9** uninstaller hosts block | **Fixed** | Exact markers, no-BOM write, and tests. |
| **R2-M10** PHP `extension_dir` | **Doctor fixed** | Doctor now validates the installed layout; runtime build prefix itself is unchanged. |
| **R2-M11 / R3-M14** test escapes | **Partially fixed** | Installer escapes mostly go through `Get-TestOverride`/sandbox; `uninstall.ps1` still reads `DEVSTACK_TEST_MOCK_WSL` directly (R4-L1). |
| **R2-M12** root-only WSL | **Not fixed** | Rootfs still has no non-root user; agent/FPM/PHP exec run as root. This is the privilege substrate for R4-H1. |
| **R2-L2 / R3-L4** log redaction | **Fixed** | Password abbreviations added; tests added. |
| **R3-M13** hosts cleanup | **Fixed** | Exact markers/no BOM. |

---


> **Editorial note (2026-10-29):** This section records the round-3 snapshot. The post-remediation disposition table above is authoritative. R3-H9, R3-M13 and R3-L4 have since been fixed; R3-M14 is only partially closed.

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

# Round 4 - New and Uncaught Findings (2026-10-29, HEAD `6f74dcc`)

**Scope:** A fresh pass over the post-remediation HEAD. This round looks for defects that were not in Rounds 1-3, plus residuals where a remediation claim is only partially true. It is not a re-litigation of findings that the gates now disprove.

**Method:** source review of the changed and high-risk paths, fresh gate runs, targeted temporary tests (removed afterward), sandbox-only installer fault injection, and inspection of the actual release artifact. No live upgrade, uninstall, or destructive operation was run against `/data/devstack`.

**Gates actually run in Round 4:**

| Gate | Result |
|---|---|
| `go build ./...` | **PASS** (exit 0) |
| `go vet ./...` | **PASS** (exit 0) |
| `go test ./...` (native Windows) | **PASS** (all packages) |
| `npm --prefix ui test -- --run` | **PASS** (120/120) |
| `npm --prefix ui run check` | **PASS** (0 errors, 0 warnings) |
| `npm --prefix ui run build` | **PASS** |
| `test/windows/installer_test.ps1` | **PASS** (188/188, mock/sandbox only) |
| `build/release-windows.ps1 -Version 0.0.0-audit -SkipUI -MockRootFS` | **PASS** (artifact built; contents inspected) |

`go test -race ./...` was not run natively because it requires CGO on Windows. Race coverage remains WSL-only.

## Round-4 summary

| Severity | IDs | Areas |
|---|---|---|
| **Critical** | R4-C1, R4-C2 | Shipped release payload omits WSL assets; runtime removal path traversal deletes arbitrary root-owned WSL paths |
| **High** | R4-H1 - R4-H6 | Local-user control API/root PHP execution; terminal/build still dead on Windows; helper CA in LocalSystem profile; standalone installer default split-brain; C3 rollback still unchecked; upgrade self-copy abort |
| **Medium** | R4-M1 - R4-M6 | Missing-rootfs installer crash; uninstaller Firefox policy residue; source-mode switch dead; runtime checksum fail-open; FPM self-kill/PID reuse; service-group TOCTOU |
| **Low** | R4-L1 | `DEVSTACK_TEST_MOCK_WSL` still honored outside sandbox in uninstaller |

---

## R4-C1 - The shipped release payload omits the WSL provisioning assets, so a fresh standalone install skips Docker and never installs the agent service

**Status: live artifact inspection. Release-blocking.**

`build/release-windows.ps1` stages the binaries, `install.ps1`, `uninstall.ps1`, the daemon task/VBS, README/LICENSE, the rootfs and the SBOM (`:105-120`). `build/release-windows.sh` stages the same set (`:199-207`). Neither copies `build/wsl/`.

The produced ZIP contains exactly these files (verified from the current release build):

```text
checksums.txt
debian-12-wsl-rootfs.tar.gz
debian-12-wsl-rootfs.tar.gz.sha256
devstack-agent
devstack-daemon-task.ps1
devstack-daemon.vbs
devstack-helper.exe
devstack.exe
devstack.spdx.json
install.ps1
LICENSE
README.md
uninstall.ps1
```

The installer, however, looks for all WSL provisioning files under `$repoRoot\build\wsl\`:

```powershell
# install.ps1 (HEAD)
$dockerDaemonJsonSrc = Join-Path $repoRoot "build\wsl\docker-daemon.json"   # :1243
$installDockerScript = Join-Path $repoRoot "build\wsl\install-docker.sh"     # :1244
...
$agentServiceSrc = Join-Path $repoRoot "build\wsl\agent.service"             # :1311
$installAgentSrc = Join-Path $repoRoot "build\wsl\install-agent.sh"          # :1312
```

In a standalone install, `$repoRoot` is the temporary payload-extraction directory. Both paths are absent, so:

- Docker installation is silently skipped because the enclosing `if (Test-Path $installDockerScript)` is false (`:1262`).
- The agent systemd unit and install script are skipped because `if (Test-Path $agentServiceSrc)` / `if (Test-Path $installAgentSrc)` are false (`:1321-1348`). Only the agent binary is copied.
- The post-install health probe is conditional on `/data/devstack/agent.yaml` and `agent.token`. On a fresh rootfs these do not exist yet, so the probe exits 0 and the installer reports success.
- The manifest still records `agent_installed = $true` (`install.ps1:1459`).
- The documented next step, `devstack init`, writes the agent credentials and then runs `systemctl restart devstack-agent.service` (`internal/cli/init_windows.go:261`). The unit was never installed, so init fails.

The release contract test does not catch this: its required-member list is exactly the same incomplete set (`test/release/release_windows_test.sh:69`). It asserts the archive is internally consistent, not that the installer can use it.

**Impact:** The primary distribution artifact (`devstack-installer.exe` / `devstack-setup.exe` / the release ZIP) cannot produce a working fresh install: Docker is absent and the agent service does not exist. The installer nevertheless exits 0 and writes a success manifest. This is the highest-impact release defect in Round 4.

**Fix:** Stage `build/wsl/install-docker.sh`, `build/wsl/install-agent.sh`, `build/wsl/agent.service`, and `build/wsl/docker-daemon.json` under `build/wsl/` in the release stage (or move the assets to the stage root and teach `install.ps1` both locations). Add them to the release-member test and to the internal checksums. Add a fresh-install-from-ZIP acceptance test that reaches `devstack init`.

**Acceptance:** A ZIP built by either release script contains all four assets; extracting it and running `install.ps1` creates/enables `devstack-agent.service` and installs Docker; `devstack init` then starts the service and succeeds; the manifest is false if either provisioning step failed.

**Verification:** list the ZIP members and compare with `install.ps1:1243-1350`; run the release build and inspect its output.

---

## R4-C2 - `DELETE /api/v1/runtimes/{id}` reaches `agent.RemoveRuntime`, which joins the raw path and deletes it as root in WSL

**Status: code-proven end-to-end; the deletion primitive was reproduced with a temporary test.**

The Windows runtime service forwards the path parameter verbatim:

```go
// internal/api/handlers_runtime.go:67-87
buildID := strings.TrimSpace(r.PathValue("id"))
...
if err := s.runtimes.Remove(r.Context(), buildID); err != nil { ... }
```

```go
// internal/daemon/windows.go:1698-1708
func (s *windowsRuntimeService) Remove(ctx context.Context, buildID string) error {
    ...
    if err := s.backend.RemoveRuntime(ctx, buildID); err != nil { ... }
```

```go
// internal/backend/wsl/backend.go
func (b *Backend) RemoveRuntime(ctx context.Context, buildID string) error {
    ...
    return b.Client.RemoveRuntime(ctx, agentproto.RuntimeRequest{BuildID: buildID})
}
```

The agent then does no validation:

```go
// internal/agent/php.go:142-158
id := strings.TrimSpace(req.BuildID)
...
targetDir := filepath.Join(r.root, "runtimes", id)
_ = os.RemoveAll(targetDir)
```

`filepath.Join` resolves `..` on the Linux agent. A request whose `{id}` is `..%2F..%2F..%2Fetc` becomes `../../../etc`, and the agent deletes `/etc` (or any other root-owned WSL directory) as root. Go 1.22's `PathValue` decodes percent-encoded slashes in a single segment; verified with a small `httptest` program:

```text
DELETE /api/v1/runtimes/..%2F..%2Fetc -> 200 id="../../etc"
```

The deletion primitive was verified against a temporary victim directory with a temporary test in `internal/agent`:

```text
PROVEN: RemoveRuntime("..\\..\\audit-victim") deleted
C:\Users\Aram\AppData\Local\Temp\TestAuditRemoveRuntimeTraversal...\audit-victim-001
outside the runtime root
```

The API is loopback-only but the dashboard session is self-issued (`GET /api/v1/session` is auth-exempt); any local process can obtain the cookie/CSRF and invoke the route. The same applies to the CLI. The local `runtime.Manager` validates installed rows, but the Windows backend bypasses that manager and calls the agent directly. `s.sites.CheckRuntimeRemoval` returns `store.ErrNotFound` for an unknown ID; the handler deliberately treats that as success, so the traversal ID is not blocked.

**Impact:** Local privilege escalation / arbitrary deletion: a local user can remove `/etc`, `/data/devstack` (all sites, service data, secrets, runtimes), or any other root-owned WSL path. This is a destructive root primitive, not just a malformed-ID error.

**Fix:** Validate `BuildID` before any path construction: accept only IDs present in `r.runtimes` (or `catalog.ParseID`) and use the stored `RuntimeResponse.Path`; never build a path from the request. Add an explicit containment check (`filepath.Rel` under `r.root/runtimes`) as defense in depth. Reject `/`, `\`, `..`, NUL and encoded separators at the API and agent boundaries. Add a regression test for `..%2F..%2Fetc` and for an unknown runtime ID.

**Acceptance:** `DELETE /api/v1/runtimes/..%2F..%2Fetc` returns 400/404 and deletes nothing; an installed runtime still deletes only its own directory; the agent rejects any ID not in its runtime map.

---

## R4-H1 - The loopback control API has no caller identity; a different local Windows user can drive the installer's daemon and execute PHP as root in WSL

**Status: code-proven. Threat-model gap left by the C1 fix.**

C1's remediation correctly rejects non-loopback requests and `devstack.test` from WSL/LAN. It does not establish *which local user* is calling:

```go
// internal/api/middleware.go:143-148
if !isAPIPath(r.URL.Path) || socketAuthenticated(r.Context()) || isAuthExempt(r.URL.Path) {
    next.ServeHTTP(w, r)
    return
}
```

`GET /api/v1/session` is exempt (`internal/api/middleware.go:273-276`, route at `internal/api/server.go:412`). The listener is bound to `127.0.0.1` (`internal/daemon/windows.go:305`), which every local Windows user can reach. The session and CSRF token are handed out to any loopback caller; there is no peer-SID check and no per-install bearer secret. The helper's named pipe is SID-scoped, but the daemon is the deputy that already has that access.

The daemon exposes a root-execution primitive without dropping privileges:

```go
// internal/api/server.go:429
{http.MethodPost, "/api/v1/sites/{ref}/php/exec", s.handlePHPExec},
```

`PHPExec` accepts arbitrary argv except `-n`/`--no-php-ini`, `--php-ini`, and `-c` (`internal/sites/php.go:724-780`). `-r` is allowed, so `{"argv":["-r","system('id')"]}` executes arbitrary PHP. The Windows site backend forwards this to the agent:

```go
// internal/agent/build.go:146-166
func (r *Runtime) ExecSite(...) {
    ...
    cmd := exec.CommandContext(cmdCtx, req.Argv[0], req.Argv[1:]...)
    cmd.Dir = site.spec.BackendPath
```

The agent service runs as root:

```ini
# build/wsl/agent.service:9-13
User=root
Group=root
ExecStart=/data/devstack/bin/devstack-agent --config /data/devstack/agent.yaml
```

The same applies to the agent's `BuildSite` (`/bin/sh -c` as root) and terminal (root PTY) endpoints if they are ever reached from the daemon. A standard user can create a PHP site under `C:\Users\Public` (or their own home), install/choose a PHP runtime through the same self-issued session, and then call `/php/exec`. No Windows admin token is required.

**Impact:** On a multi-user Windows machine, this is a local privilege boundary failure: any local user can control the installer user's daemon and run code as root inside the WSL distro, read/write `/data/devstack` secrets, and delete root-owned state. On a single-user machine it still means the dashboard's "local only" check is not an authentication boundary.

**Fix (in preference order):** (1) require a per-install bearer secret for every API request, including `/session`, delivered to the UI/CLI over the peer-verified named pipe; or (2) verify the TCP peer SID against the installing user in the API listener; or (3) split the dashboard API onto a SID-restricted transport and keep only site traffic on the public proxy. Separately, run PHP-FPM/agent execution as a non-root WSL user.

**Acceptance:** A second local user's session request and mutation are rejected (403), while the installing user's UI/CLI still works; `/php/exec` cannot run as root; a regression test uses a foreign SID/token.

---

## R4-H2 - Windows terminal and build are wired but remain nonfunctional (C2 is only partially closed)

**Status: live local proof.**

The Windows daemon now constructs the services (`internal/daemon/windows.go:243-256`), but the implementations are still Unix-only:

**Terminal.** `terminal.Manager.resolveShell` looks for `$SHELL`, `/bin/bash`, then `/bin/sh` (`internal/terminal/terminal.go:342-365`). Windows has none of those by default. A temporary test at HEAD produced:

```text
resolveShell() = "", terminal: no usable shell (tried "/bin/bash", /bin/bash, /bin/sh)
```

Even if a shell is found, `terminal/process_windows.go:5-7` states: "The Windows terminal is an agent-proxy stub until the WSL terminal transport lands; there is no ConPTY process group to signal here." The session code uses `creack/pty` (`internal/terminal/session.go:185`), which is not a Windows ConPTY implementation. The WSL agent already has a terminal endpoint (`internal/agent/terminal.go`), but the Windows daemon does not route to it.

**Build.** The Windows daemon constructs `build.NewService` with the default runner (`internal/daemon/windows.go:243`). That service always invokes `/bin/sh -c` (`internal/build/build.go:190`) and its `resolveNodeBuild` skips any runtime whose `Backend` is not `model.BackendLocal` (`internal/build/build.go:423`). The Windows runtime service returns every build with `Backend: model.BackendWSL` (`internal/daemon/windows.go` `List`). A temporary test produced:

```text
resolveNodeBuild(WSL node) err = build: required Node runtime is not installed: Node 20
```

A Windows static build therefore fails before it can run (and would use `/bin/sh` if it got that far). The agent has a working `BuildSite` implementation (`internal/agent/build.go:47-120`), but it is not wired.

**Impact:** The dashboard's Terminal and Build actions still fail on Windows, despite C2 being marked "wired". The Windows UI advertises both. This is a functional parity failure and keeps the terminal 503/500 class of findings alive in a different form.

**Fix:** Route both actions through the agent's authenticated endpoints (terminal ticket + `/v1/sites/{id}/build`) or implement a Windows ConPTY/PowerShell runner and a Windows-source build runner. Do not construct the Unix-only services on Windows without an adapter. Add Windows E2E tests for a real terminal and a real static build.

**Acceptance:** On Windows, a dashboard terminal opens a shell in the site directory; a static site build reaches the Node binary and produces `dist`; both are covered by an E2E/vertical-slice test.

---

## R4-H3 - Helper CA/trust paths resolve inside the LocalSystem profile, not the installed `%USERPROFILE%\.devstack`

**Status: code-proven plus live SYSTEM-environment proof.**

The new platform default is context-dependent:

```go
// internal/platform/platform.go:25-28
if home, err := os.UserHomeDir(); err == nil && home != "" {
    return filepath.Join(home, ".devstack")
}
```

The helper's package-level paths are computed from that same function:

```go
// internal/helper/windows_config.go:17-19
DefaultWindowsSystemDir = filepath.Join(platform.DefaultRoot("windows"), "system")
DefaultWindowsCADir     = filepath.Join(platform.DefaultRoot("windows"), "system", "pki")
DefaultWindowsRootCert  = filepath.Join(platform.DefaultRoot("windows"), "system", "pki", "root.crt")
```

The helper service runs as `LocalSystem` (`sc qc DevstackHelper`: `SERVICE_START_NAME: LocalSystem`). A one-shot SYSTEM task on the review machine printed:

```text
USERDOMAIN=WORKGROUP
USERNAME=ARAM-PC$
USERPROFILE=C:\Windows\system32\config\systemprofile
```

`ensureAuthority` uses the package-level CA dir and ignores the configured one:

```go
// internal/helper/winserver.go:465
authority, err := LoadOrCreateAuthority(DefaultWindowsCADir, time.Now)
```

```go
// internal/helper/winserver.go:318
if err := s.trust.InstallRoot(r.Context(), DefaultWindowsRootCert); err != nil { ... }
```

So a fresh helper creates its CA private key and root certificate under `C:\Windows\system32\config\systemprofile\.devstack\system\pki`, while `install.ps1` creates/protects `%USERPROFILE%\.devstack\system` and writes `helper.yaml` there. `uninstall.ps1` looks for `$systemDir\pki\root.crt` and the Firefox marker under the user's target directory (`uninstall.ps1:132-152`), so it does not find the helper's actual CA/key or policy marker. The broad `Cert:\LocalMachine\Root` subject fallback can remove the trusted root, but the private key and Firefox policy state remain outside the purge path. A custom `-InstallDir` makes the mismatch permanent.

**Impact:** The helper's private CA key is created outside the installer-managed tree, is not covered by the documented `%USERPROFILE%\.devstack\system` DACL, survives `uninstall.ps1 -PurgeData`, and can leave a machine-trusted root/policy behind. It also breaks any future code that expects the CA at the installer's path.

**Fix:** Make the helper system/CA paths explicit and persistent. Add `system_dir`/`ca_dir` to the Windows helper config (or derive them from `--config`'s directory), pass them from `install.ps1`, and change `ensureAuthority`/`InstallRoot` to use `s.cfg.CADir`/`s.cfg.RootCertPath`. Add a test that runs the helper with a different `USERPROFILE` and asserts the CA lands under the configured system dir. Make uninstall remove the configured CA and Firefox policy.

**Acceptance:** A fresh helper creates `C:\Users\<installer>\.devstack\system\pki\root.key|root.crt`; uninstall removes the key, root trust, and Firefox policy; a SYSTEM-context test cannot redirect them to the system profile.

---

## R4-H4 - The standalone installer still defaults to `C:\devstack`, while the daemon, helper and docs now default to `%USERPROFILE%\.devstack`

**Status: code-proven; the unit test enshrines the old default.**

```go
// cmd/devstack-installer/installer.go:34
fs.StringVar(&opts.InstallDir, "install-dir", "C:\\devstack", "Installation directory on Windows")
```

```go
// cmd/devstack-installer/installer_test.go:22-24
name: "defaults", args: []string{}, wantDir: "C:\\devstack",
```

`main.go` always passes `-InstallDir` to `install.ps1` when it is non-empty (`buildPowerShellArgs`, `installer.go:125-127`), so the standalone artifact's default wins over install.ps1's new default:

```powershell
# install.ps1:99
if ([string]::IsNullOrEmpty($InstallDir)) { ... Join-Path $userProf ".devstack" }
```

The daemon is registered by `build/windows/devstack-daemon-task.ps1` with a VBS that runs `devstack.exe daemon run` and no `--home` (`:21`), so it uses `platform.DefaultRoot("windows")` = `%USERPROFILE%\.devstack` (`internal/daemon/windows.go:566`, `internal/cli/root.go:366`). The helper config, manifest and WSL install directory are under `C:\devstack`, while the daemon's SQLite DB, config, logs, projects root and readiness marker are under `%USERPROFILE%\.devstack`.

**Impact:** A fresh install from the main artifact creates a split-brain installation: the helper and staged files are in `C:\devstack`, the daemon/CLI look in the user profile, docs say user profile, and `install.ps1 -Upgrade`/uninstall paths may target the wrong tree. `devstack doctor` and `devstack uninstall` can report/remove the wrong location.

**Fix:** Change the installer default to `%USERPROFILE%\.devstack` (computed with the invoking user's profile), update the unit test, and only pass `-InstallDir` when the user explicitly supplied it. Keep a legacy detection for existing `C:\devstack` installs; pass `--home` explicitly to the daemon when a custom target is used. Update README and docs.

**Acceptance:** `devstack-installer.exe` with no flags installs into `%USERPROFILE%\.devstack`; the daemon and helper share that home; an existing `C:\devstack` install is detected/upgraded explicitly; the default is covered by a test.

---

## R4-H5 - C3 is not fully closed: the rollback script has no exit-code check and prints "rolled back successfully" even when nothing was restored

**Status: live sandbox proof.**

The rollback branch in `install.ps1` is still fire-and-forget:

```powershell
# install.ps1:718-733
& wsl.exe -d devstack -u root -- tar -tzf /data/devstack-backup.tar.gz 2>&1 | Out-Null
if ($LASTEXITCODE -eq 0) {
    $rollbackScript = @"
tar -tzf ... && \
rm -rf /data/devstack.rollback-tmp && \
...
mv /data/devstack.rollback-tmp/devstack /data/devstack && \
...
"@
    $rollbackScript -replace "`r`n", "`n" | & wsl.exe -d devstack -u root -- sh
}
& wsl.exe -d devstack -u root -- systemctl restart devstack-agent.service 2>$null
...
Write-InstallerError "Upgrade rolled back successfully to previous version."   # :746
exit 1
```

There is no `$LASTEXITCODE` check after the `sh` pipeline, and the success message is unconditional even when the archive is missing or the `mv` chain fails. A sandbox run with `DEVSTACK_TEST_UPGRADE_FAIL=true` and no backup archive produced:

```text
devstack-agent health check failed. Initiating rollback...
Upgrade rolled back successfully to previous version.
EXIT=1
```

Nothing was restored. The `tar -tzf` failure branch skips the rollback entirely and still prints success. The `mv /data/devstack /data/devstack.failed-tmp` + `mv .../devstack /data/devstack` ordering is safer than the old `rm -rf`, but a failure between the two moves leaves no live `/data/devstack` and the script does not notice.

**Impact:** A failed upgrade can still lose or strand all WSL data while reporting a successful rollback. The C3 acceptance criterion ("fault-injected extraction failure restores prior state") is not met.

**Fix:** Make every rollback step fail-closed: run a single `sh -e` script with an explicit `set -e`, verify the archive, stage the restore, and check `$LASTEXITCODE` before printing success. On any failure, preserve `/data/devstack.failed-tmp` and print a precise recovery path with a non-zero exit; never claim success. Add a fault-injected test for missing/truncated archive and for a failed second `mv`.

**Acceptance:** Missing/truncated archive, failed extraction, and failed swap all leave the prior data recoverable and produce a failure message; success is printed only after a verified restore.

---

## R4-H6 - Upgrade/reinstall aborts with "Cannot overwrite the item ... with itself" when only the installed agent binary is available

**Status: live sandbox proof.**

The candidate list includes the destination itself:

```powershell
# install.ps1:603-612
$stagedAgentLinux = Join-Path $binDir "devstack-agent"
$prebuiltAgentLinux = @(
    (Join-Path $repoRoot "devstack-agent"),
    (Join-Path $repoRoot "build\wsl\devstack-agent"),
    (Join-Path $repoRoot "bin\linux_amd64\devstack-agent"),
    (Join-Path $binDir "devstack-agent")          # destination
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if ($null -ne $prebuiltAgentLinux) {
    Copy-Item -Path $prebuiltAgentLinux -Destination $stagedAgentLinux -Force
}
```

When the repo has no prebuilt agent (a source checkout) but the install already has `bin\devstack-agent`, the last candidate is selected and `Copy-Item` copies the file onto itself. PowerShell throws, `$ErrorActionPreference = "Stop"` aborts, and the upgrade's rollback path is never entered. A sandbox reproduction:

```text
devstack installer: Starting upgrade flow...
devstack installer: Stopping daemon for upgrade...
Copy-Item: Cannot overwrite the item ...\bin\devstack-agent with itself.
EXIT=1
```

This is also reachable on a reinstall/repair, not just `-Upgrade`, and it happens after the daemon has been stopped.

**Impact:** Source-checkout upgrades and repairs abort after stopping the daemon, with mixed staging state and no rollback. The user must manually rebuild/copy the agent.

**Fix:** Exclude the destination from the candidate list, or compare resolved paths before copying; if the only available file is the installed one, build the agent from `cmd/devstack-agent` or skip the self-copy. Add a sandbox test with only `$binDir\devstack-agent` present.

**Acceptance:** Re-running the installer/upgrade with only the installed agent binary succeeds and either rebuilds or reuses it without a PowerShell error; a regression test covers the self-copy case.

---

## R4-M1 - A fresh install with no rootfs archive crashes on `Test-Path $null` instead of reaching the intended error/mock branch

**Status: live proof in a temporary copy of the installer.**

```powershell
# install.ps1:1037-1041
$defaultCandidate = @((Join-Path $repoRoot "debian-12-wsl-rootfs.tar.gz"),
                      (Join-Path $repoRoot "build\wsl\debian-12-wsl-rootfs.tar.gz")) |
    Where-Object { Test-Path $_ } | Select-Object -First 1
if (Test-Path $defaultCandidate) { $effectiveRootFS = $defaultCandidate }
```

When neither candidate exists, `$defaultCandidate` is `$null`. `Test-Path $null` throws with `$ErrorActionPreference = "Stop"`:

```text
Test-Path: Value cannot be null. (Parameter 'The provided Path argument was null or an empty collection.')
```

The surrounding code intends a friendly error at `:1050` or a mock rootfs at `:945`. Reproduced in a temporary repository with `install.ps1` + `versions.lock` but no rootfs.

**Impact:** A source checkout without a built/downloaded rootfs fails with an unhandled PowerShell error before the intended message. Automated bootstrap scripts see an opaque failure.

**Fix:** Guard with `if ($defaultCandidate -and (Test-Path -LiteralPath $defaultCandidate))`; use `-LiteralPath` throughout for user-supplied paths.

**Acceptance:** Running `install.ps1 -DryRun` (or a sandbox install) with no rootfs prints the documented error and exits 2, not a null `Test-Path` stack trace.

---

## R4-M2 - The uninstaller leaves the Firefox enterprise-root policy (and potentially helper-managed firewall rules) behind

**Status: live registry inspection; code-proven.**

`wintrust.InstallFirefoxPolicy` writes:

```text
HKLM\Software\Policies\Mozilla\Firefox\Certificates
    ImportEnterpriseRoots = 1
```

(`internal/wintrust/trust_windows.go:28`, `:199-217`, `:305-309`). On the review machine that key is present with `ImportEnterpriseRoots = 1`.

`uninstall.ps1` only deletes the marker file under the *user's* `$systemDir`:

```powershell
$firefoxMarker = Join-Path $systemDir "firefox-policy-managed"
if (Test-Path $firefoxMarker) {
    Remove-Item -Path $firefoxMarker -Force -ErrorAction SilentlyContinue
}
```

It never deletes the registry value and never calls the helper's `Uninstall` RPC (the helper is simply stopped and `sc delete`d at `:221-238`). With the new helper CA path (R4-H3) the marker is in the LocalSystem profile anyway, so this branch is doubly ineffective.

The helper's own `winfirewall` rules (`Devstack WSL Proxy HTTP/HTTPS`, `internal/winfirewall/firewall_windows.go`) are also created/removed by `helper.Uninstall`, not by `uninstall.ps1`; the script removes only `devstack-daemon-proxy`. The current helper config does not set `WSLSubnet`, so it may not create those rules today, but any future/config change leaves them.

**Impact:** Uninstall leaves a machine-wide Firefox policy that imports enterprise roots, and can leave inbound firewall rules for the proxy ports. The user is told the product is uninstalled but policy/network changes persist.

**Fix:** Call the helper's `Uninstall` RPC before stopping the service, or replicate its full cleanup: `reg delete HKLM\Software\Policies\Mozilla\Firefox\Certificates /v ImportEnterpriseRoots /f` (only when devstack owns it), remove both `Devstack WSL Proxy` rules, and remove the marker from the actual helper system dir. Add an uninstall residue test.

**Acceptance:** After uninstall, the Firefox policy value and both helper firewall rules are gone; the root CA is gone; no devstack-owned marker remains in either profile.

---

## R4-M3 - Source-mode switching is dead in production: `SourceCopier` is never assigned, so the UI/API always returns "unavailable"

**Status: code-proven; only tests set the dependency.**

```go
// internal/sites/source_copy.go:32-33
if s.deps.SourceCopier == nil {
    return model.Site{}, ErrSourceCopyUnavailable
}
```

A repository-wide search finds no production assignment of `SourceCopier` (only `internal/sites/source_copy_test.go` sets it). `internal/wsl.CopySource` is implemented but only used by tests (`internal/wsl/wsl_test.go`). The Windows daemon's `sites.NewService` (`internal/daemon/windows.go:166`) does not set `SourceCopier`; neither does the Linux daemon.

The UI advertises the feature:

```text
Changing source modes requires a stopped site and an agent-verified copy.
```

(`ui/src/lib/components/ProjectSourceMode.svelte`). The API route exists at `internal/api/server.go:416`.

**Impact:** Users can select a source mode in the UI and receive a failure; the "agent-verified copy" path is untested in production. This is a functional gap in the Windows parity work, not just a UI polish issue.

**Fix:** Wire a `SourceCopier` implementation: a local implementation for Linux, and an agent endpoint (mount `/v1/source/copy` with `Runtime.CopySource`) for Windows. Add a real E2E test for a stopped site switching Windows→WSL→Windows. Until wired, hide the control and return a clear "not implemented" rather than an unavailable error.

**Acceptance:** A stopped static site can switch Windows↔WSL, the copy is verified by SHA-256, the source is retained, and the site row/route reflect the new path.

---

## R4-M4 - Agent runtime download checksum verification fails open on read errors and accepts an empty expected hash

**Status: code-proven.**

```go
// internal/agent/php.go:469-478
if expectedSHA256 != "" {
    data, err := os.ReadFile(tmpFile)
    if err == nil {
        sum := sha256.Sum256(data)
        ...
        if !strings.EqualFold(hexSum, expectedSHA256) {
            return fmt.Errorf("checksum mismatch: ...")
        }
    }
}
cmdTar := exec.CommandContext(ctx, tarBin, "-xz", "--strip-components=1", "-C", destDir, "-f", tmpFile)
```

If `os.ReadFile` fails (file removed/replaced, permissions, disk error), the checksum is silently skipped and the tar is extracted. If the catalog entry has no `expectedSHA256`, no checksum is required at all. The temporary path is predictable: `os.TempDir()/devstack-runtime-<basename(url)>`, and `curl -o` follows an existing symlink. The agent runs as root, so a local process able to write `/tmp` can influence the file; the same pattern is an unnecessary root-write primitive.

**Impact:** A corrupted or tampered runtime archive can be extracted as root; a pre-created symlink can redirect the download. The runtime manager in `internal/runtime` verifies checksums strictly, so this agent path is inconsistent with the rest of the product.

**Fix:** Require a non-empty SHA-256, return `os.ReadFile` errors, create the temp file with `os.CreateTemp` in a root-owned `0700` directory, use `O_EXCL`/no-follow semantics, verify before extraction, and extract to a staged directory then rename. Add tests for missing hash, read failure, checksum mismatch, and symlink pre-creation.

**Acceptance:** No archive is extracted unless its SHA-256 matches a non-empty expected value; a pre-existing symlink at the temp path cannot redirect the write; the agent cleans up on every failure.

---

## R4-M5 - Stopping a simulated PHP-FPM can signal the agent's own process group; real FPM stop does not guard against PID reuse

**Status: code-proven; live process-group fact confirmed.**

```go
// internal/agent/fpm.go (fallback when sbin/php-fpm is absent)
proc.listener = l
proc.pid = os.Getpid()          // :160
```

```go
// internal/agent/fpm.go:220-240
func (p *fpmProcess) stop() {
    ...
    killFPMProcess(p.cmd, p.pid) // :238
}
```

```go
// internal/agent/process_unix.go:22-40
_ = syscall.Kill(-pid, syscall.SIGQUIT)
...
_ = syscall.Kill(-pid, syscall.SIGTERM)
...
_ = syscall.Kill(-pid, syscall.SIGKILL)
```

The live agent has `PID == PGID == SID`:

```text
PID    PPID    PGID     SID COMMAND
71774       1   71774   71774 devstack-agent
```

If a PHP build lacks `sbin/php-fpm`, `startFPM` falls back to the simulator with `pid = os.Getpid()`. Stopping that site sends SIGQUIT/SIGTERM/SIGKILL to `-71774`, i.e. the agent's own process group, killing the agent (and any children in the group). The fallback is reachable on a corrupted/partial runtime or test configuration. Separately, the real-FPM path stores the child PID and `stop()` does not check `p.done` before signaling; if the child already exited and its PID was reused, the group signal can target an unrelated process group.

**Impact:** Agent self-termination / DoS and a PID-reuse safety bug. The first is a direct regression from the new process-group kill (the previous code only called `cmd.Process.Kill()` when `cmd` was non-nil).

**Fix:** In `killFPMProcess`, require `cmd != nil && cmd.Process != nil` and a recorded child PGID; never signal a simulated process. Check `p.done`/`cmd.ProcessState` before signaling and use a pidfd/process handle where available. Add a test that stops the simulated FPM without killing the test process.

**Acceptance:** Stopping a simulated PHP-FPM leaves the agent alive; stopping an already-exited FPM does not signal any process group; real FPM children are still reaped.

---

## R4-M6 - Service group exclusivity is a check-then-act race (no Manager lock)

**Status: code-proven.**

```go
// internal/servicecontrol/manager.go:199-217
func (m *Manager) Activate(ctx context.Context, id services.ID) error {
    ...
    if cause := m.checkActivationDependencies(ctx, svc); cause != nil { ... }
    if cause := prep.PrepareActivate(ctx, svc); cause != nil { ... }
    if cause := m.helper.Activate(ctx, string(id)); cause != nil { ... }
    ...
}
```

`Manager` has no mutex (`type Manager struct` has `root/store/helper/catalog/preparers/now` only). `checkActivationDependencies` reads helper status (`:331-370`) and the action happens in a separate RPC. Two concurrent `Activate` calls for nginx and apache (or mysql and mariadb) can both observe the other as inactive and both proceed. The services bind the same ports (80/443 or 3306), so one may fail at the helper; the persisted state can still record the wrong active member, and the helper may briefly run both.

**Impact:** The new exclusivity rule is not reliable under concurrent dashboard/CLI actions. Port conflicts produce confusing errors; persisted state can disagree with the running container.

**Fix:** Serialize activation/deactivation per Manager (or per exclusive group) with a mutex, and re-check status inside the same critical section before calling the helper. Add a concurrent activation test with a fake helper that delays `Status`.

**Acceptance:** Concurrent activation of two members of an exclusive group yields one success and one `ErrServiceConflict`, with no second container started.

---

## R4-L1 - `uninstall.ps1` still honors `DEVSTACK_TEST_MOCK_WSL` outside a sandbox

**Status: code-proven; residual of R3-M14.**

```powershell
# uninstall.ps1:197
if (-not $DryRun -and -not $isSandbox -and ($env:DEVSTACK_TEST_MOCK_WSL -ne "true")) {
    ... remove managed hosts block inside WSL ...
}

# uninstall.ps1:269
} elseif ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
    ... delete the local wsl distro directory instead of unregistering ...
```

The installer now routes most overrides through `Get-TestOverride`/`$isSandbox`, but the uninstaller reads this one directly. Setting the variable in a real elevated uninstall suppresses the WSL hosts cleanup and changes the `-PurgeDistro` behavior. It is a test escape in production code.

**Fix:** Route every `DEVSTACK_TEST_*` read through an `$isSandbox`-gated helper, as the installer does. Log each accepted override.

**Acceptance:** No `DEVSTACK_TEST_*` variable changes a non-sandbox uninstall; a test asserts the gating.

---

## Round-4 verdict

**Still NOT 100% bullet-proof.**

Round 4 found two new Critical defects:

1. **R4-C1** - the shipped release payload omits the WSL provisioning assets, so the main installer cannot produce a working fresh install (Docker absent, agent service absent, `devstack init` fails) while reporting success.
2. **R4-C2** - the runtime-delete route reaches a root `os.RemoveAll` with an unvalidated path, allowing arbitrary deletion in WSL.

The remediation fixed the prior 8.3 bypass and several Round 1-3 issues (gates are green), but the C2/C3 fixes are partial (R4-H2/R4-H5), the new platform root introduced a LocalSystem CA path regression (R4-H3) and a standalone-installer split-brain (R4-H4), and the control API still lacks caller identity (R4-H1). The release artifact and the runtime-delete path should block any "bullet-proof" claim.

**Recommended order for the fixing agent:** R4-C2 (destructive security) -> R4-C1 (release blocker) -> R4-H1/R4-H2 (security/functionality) -> R4-H3/R4-H4 (install correctness) -> R4-H5/R4-H6 (data safety/upgrade) -> Medium findings.

**Safety:** Do not test R4-C2, R4-H5, or R4-H6 against the live `/data/devstack`. Use a temporary agent root, `-TestSandbox`, or a cloned distro. R4-C2's temporary test was deleted after proof; no repo source was changed.

---

# Current Status and Handoff (2026-10-29)

This section is the one-screen handoff for the next agent. The findings themselves are in Rounds 1-4 above; this section records the state of the repository, the review machine, what is verified, what is not, and what should happen next.

## Repository state

- **Branch:** `master`
- **HEAD before the audit commit:** `6f74dcc` (`fix(services): enforce group exclusivity, canonicalize postgres ids, seed web configs, and handle socket symlinks in backup`)
- **Original review baseline:** `6d7bdaa` (`v0.2.0`); 43 commits landed between that baseline and `6f74dcc`.
- **Product source changes by this audit:** none. The audit is read-only on product code. Temporary test files used for proof were deleted.
- **Files intentionally added/edited by the audit:** this document and `AGENTS.md` (the model policy requested by the user). The audit commit is docs-only.
- **Working tree at the start of Round 4:** clean (`git status --porcelain` empty). The previous remediation work is committed, not unstaged.

## Gate evidence at HEAD

These were fresh runs during Round 4, not copied from a remediation report:

| Gate | Result |
|---|---|
| `go build ./...` | PASS (exit 0) |
| `go vet ./...` | PASS (exit 0) |
| `go test ./...` (native Windows) | PASS (all packages; previous `internal/phpini` failures fixed) |
| `go test ./internal/proxy` (8.3/ADS regression set) | PASS |
| `npm --prefix ui test -- --run` | PASS (15 files, 120 tests) |
| `npm --prefix ui run check` | PASS (0 errors, 0 warnings) |
| `npm --prefix ui run build` | PASS |
| `test/windows/installer_test.ps1` | PASS (188 checks, 0 failures; mock/sandbox paths only) |
| `build/release-windows.ps1 -Version 0.0.0-audit -SkipUI -MockRootFS` | PASS; artifact built and inspected. The inspection found R4-C1. |

**Not run:** native Windows `go test -race ./...` (requires CGO); live upgrade/rollback; live uninstall; real reboot-resume; fresh Windows 10 22H2 install; multi-user SID test; fresh-install-from-ZIP E2E. These are explicit gaps, not green claims.

## Live review machine (do not confuse with HEAD)

The installed instance on this machine is **legacy**, not a fresh install of HEAD:

- Windows 11 Pro, build `10.0.26300.0` (x64).
- Existing install root: `C:\devstack`, not `%USERPROFILE%\.devstack`.
- `DevstackHelper` service: Running, Automatic, `LocalSystem`; binary `C:\devstack\bin\devstack-helper.exe`; config `C:\devstack\system\helper.yaml`; `allow_sid` is the installing user's SID.
- `C:\devstack\system\pki\root.crt` exists (old helper CA location). `%USERPROFILE%\.devstack\system\pki\root.crt` does not exist. This is exactly why R4-H3 matters for the next fresh install.
- `C:\devstack\bin\devstack.exe` and `C:\devstack\bin\devstack-agent` exist. `%USERPROFILE%\.devstack\bin\devstack-agent` does not.
- The dashboard daemon was **not running** during Round 4: nothing listens on `127.0.0.1:7070`; nothing on 8080/8443. Ports 80/443 are held by the helper forwarder (PID 13596), and 7443 is the WSL localhost relay (PID 20996).
- Firewall rule `devstack-daemon-proxy`: Enabled, Inbound Allow, Domain+Private, TCP 8080/8443, `RemoteAddress 172.25.48.0/20`.
- WSL adapter: `vEthernet (WSL (Hyper-V firewall))`, `172.25.48.1/20`.
- WSL distros visible: `Ubuntu`, `FedoraLinux-44`, `devstack`.
- `devstack-agent` runs as root with `PID == PGID == SID` (PID 71774 at review time); the default `wsl -d devstack` user is root. There is no non-root user in the rootfs. This is the R2-M12/R4-H1 substrate.
- PHP runtime compiled-in `extension_dir` still points at the nonexistent `/tmp/devstack-php-build-xhqbqd/...` path; normal site traffic works because devstack composes `extension_dir`, but bare PHP invocations are affected (R2-M10).
- The release artifact used for R4-C1 was built into `%TEMP%\opencode\release-audit`; it is not part of the repo.

**Do not run destructive upgrade/rollback or runtime-delete tests against this live `/data/devstack`.** Use `-TestSandbox`, `DEVSTACK_TEST_MOCK_WSL`, a temporary agent root, or a cloned distro.

## Remediation disposition (summary)

Rounds 1-2/3 findings are mostly fixed in code and covered by tests:

- C1 API/proxy loopback boundary; R2-H7 serving-mode errors; R2-H8/R3-H9 8.3/ADS dotfile bypass; R2-M9 hosts cleanup; R2-M10 doctor; R2-L2/R3-L4 redaction; H3/H5 shims; H4 doctor; H6 autostart; M5 IPC PID handling; M6 TLS pin documentation.
- C2 is only partially fixed (terminal/build still dead: R4-H2).
- C3 is only partially fixed (rollback success still unchecked: R4-H5).
- C4 changed but not re-executed.
- C5 rootfs build is fail-closed and pinned, but the release payload is incomplete (R4-C1).
- R2-M12 (root-only WSL) is not fixed.
- R3-M14 is only partially fixed (R4-L1).

## Current top risks (in priority order)

1. **R4-C2** - arbitrary root-owned WSL deletion via runtime ID path traversal. Fix before any other release work.
2. **R4-C1** - the shipped release payload cannot produce a working fresh install (Docker/agent service missing). Fix before calling the release installable.
3. **R4-H1** - no local caller identity on the control API; a second Windows user can drive root PHP exec in WSL.
4. **R4-H2** - terminal/build nonfunctional on Windows.
5. **R4-H3** - helper CA/keys under the LocalSystem profile; uninstall/purge does not manage them.
6. **R4-H4** - standalone installer defaults to `C:\devstack`, daemon/CLI default to `%USERPROFILE%\.devstack`; split-brain.
7. **R4-H5** - rollback failure still prints success.
8. **R4-H6** - upgrade/reinstall self-copy abort.
9. Medium findings R4-M1..R4-M6 and low R4-L1.

## Verification / reproduction notes

- R4-C1: list the release ZIP members and compare with `install.ps1:1243-1350`; a fresh install from the ZIP should create `devstack-agent.service` and install Docker. The current release test only checks the incomplete member list.
- R4-C2: `DELETE /api/v1/runtimes/..%2F..%2Fetc`; the agent `RemoveRuntime` joins the raw ID. A temporary test proved `RemoveRuntime` deletes outside the runtime root; the test was removed.
- R4-H1: `GET /api/v1/session` is auth-exempt on loopback; `POST /api/v1/sites/{ref}/php/exec` accepts `-r`; agent `ExecSite` runs as root. Test with a second local Windows user/SID.
- R4-H2: `terminal.Manager.resolveShell` returns "no usable shell"; `build.Service` uses `/bin/sh` and rejects `BackendWSL` runtimes. Temporary tests produced both errors.
- R4-H3: run a SYSTEM-context process; `USERPROFILE=C:\Windows\system32\config\systemprofile`; the helper's `DefaultWindowsCADir` resolves there. A one-shot SYSTEM scheduled task was used for proof and removed.
- R4-H4: `cmd/devstack-installer` default `C:\devstack`; daemon `platform.DefaultRoot` / `devstack-daemon-task.ps1` default `%USERPROFILE%\.devstack`.
- R4-H5: sandbox `install.ps1 -Upgrade` with `DEVSTACK_TEST_UPGRADE_FAIL=true` and no backup archive prints "Upgrade rolled back successfully" and exits 1.
- R4-H6: sandbox `install.ps1 -Upgrade` with only `$binDir\devstack-agent` present throws "Cannot overwrite the item ... with itself."
- R4-M2: `reg query HKLM\Software\Policies\Mozilla\Firefox\Certificates /v ImportEnterpriseRoots` currently returns `0x1`; uninstaller code does not delete it.
- R4-M3: no production assignment of `sites.Deps.SourceCopier`; only tests set it.
- R4-M5: `wsl -d devstack -u root -- ps -eo pid,ppid,pgid,sid,comm` shows agent PID == PGID; the simulated FPM path uses `os.Getpid()`.

## What the next agent should do

1. Fix R4-C2 and add the traversal regression test; do not test it against live `/data/devstack`.
2. Fix R4-C1 and make the release test assert the WSL assets and a fresh-install E2E.
3. Fix R4-H1/H2: SID-bound API or per-install bearer; route terminal/build through the agent or implement Windows runners.
4. Fix R4-H3/H4: explicit helper system dir and standalone installer default/home propagation.
5. Fix R4-H5/H6 and add fault-injected upgrade/rollback tests.
6. Work through R4-M1..M6 and R4-L1.
7. Re-run the full gate matrix; do not claim green without fresh output. Run real upgrade/rollback only in a sandbox/clone.
8. Update this document's status table as fixes land; keep Round 4 as the current record until a Round 5 re-review.

## Audit artifacts

- This document: restored Rounds 1-2 (835 lines recovered from the session database), historical Round 3, and Round 4 (current HEAD).
- `AGENTS.md`: project model policy (`opencode-go/deepseek-v4.1-flash#max`).
- Prior remediation commits: `e4b0574` (C1), `d2890b1` (platform root), `6f74dcc` (services); full list in `git log 6d7bdaa..6f74dcc`.
- The audit made no product-code changes and did not push.

---

# Remediation Completion (2026-10-29)

All Round-4 findings are implemented in the remediation commit on top of `835372c`:

| Finding | Change | Evidence |
|---|---|---|
| R4-C1 | Both release scripts stage `build/wsl/{agent.service,install-agent.sh,install-docker.sh,docker-daemon.json}`; internal checksums recurse; release test requires them and verifies the installer references them. | `test/release/release_windows_test.sh` PASS; ZIP inspection |
| R4-C2 | API rejects separators/NUL/`..`; Windows runtime service checks the installed set; agent deletes only the registered, contained `Path`. | `TestRemoveRuntimeRejectsPathTraversalID`, `TestRemoveRuntimeUsesRegisteredPathAndRejectsTraversal` |
| R4-H1 | Per-install dashboard token in `securestore` (DPAPI/0600), API auth gate + `/auth` HttpOnly cookie, CLI `ui` fragment bootstrap, UI locked banner, OpenAPI updated. The public-proxy path is covered because the API validates the cookie, not the proxy peer. | `TestAuthTokenGateProtectsDashboardAPI`, `TestAuthBootstrapRejectsWrongTokenAndSetsSecureCookie`, UI `authenticateFromFragment` tests |
| R4-H2 | Windows build calls the agent `BuildSite` and writes host build logs; Windows terminal issues an agent ticket and bridges the browser WebSocket to the agent PTY. | `TestWindowsBuildService*`, `TestWindowsTerminalService*` |
| R4-H3 | Helper derives system/CA/root paths from `--config`, supports explicit `system_dir`/`ca_dir`/`root_cert`, and the installer writes them. | `TestWindowsHelperDerivesSystemAndCAPathsFromConfigPath`, `TestWindowsConfigLoadsSystemAndCAPaths` |
| R4-H4 | Standalone installer defaults to `%USERPROFILE%\.devstack` with legacy `C:\devstack` detection for upgrade/uninstall. | `TestParseFlags` |
| R4-H5 | Rollback uses a fail-closed `set -e` script, checks every exit status, and prints success only after a verified restore. | Missing-backup fault injection in `installer_test.ps1` |
| R4-H6 | `Select-PrebuiltAgent` excludes the destination; repair/upgrade no longer self-copies. | Static guard in `installer_test.ps1` |
| R4-M1 | Null rootfs guard; dry-run without a rootfs exits cleanly. | Installer test 35 |
| R4-M2 | Uninstaller removes the devstack-owned Firefox policy, all three firewall rules, `api-token`, and the legacy LocalSystem CA. | Installer test 36 |
| R4-M3 | Agent `/v1/source/copy` + `Runtime.CopySource`; Windows agent adapter and Linux local copier wired into sites. | `TestAgentCopySource*` |
| R4-M4 | Runtime archives require a 32-byte hex checksum, verification is fail-closed, staging is private and atomic. | `TestDownloadAndExtractArchive*` |
| R4-M5 | Simulated FPM has no PID; only a live child is signaled, and never after it is reaped. | Native + WSL FPM tests |
| R4-M6 | Lifecycle mutations serialize behind a Manager mutex. | `TestConcurrentActivationOfExclusiveGroupSerializes` |
| R4-L1 | `uninstall.ps1` reads `DEVSTACK_TEST_MOCK_WSL` only through the sandbox-gated helper. | Installer test 36 |

Additional Windows lifecycle fixes made while addressing the user-reported "WSL turns off / visible terminal" problems:

- Every `wsl.exe` launched by the daemon, CLI tooling, and tooling package uses `CREATE_NO_WINDOW`; no new console flashes.
- The installer starts the `devstack-daemon` logon task after WSL provisioning (and on `-SkipWSL`), and the VBS/task passes `--home` so the daemon uses the install home.
- WSL wake timeout raised to 30 seconds; keepalive restarts immediately and logs an unexpected exit.
- The daemon keepalive remains the `sleep infinity` child held for the daemon lifetime; when the daemon is stopped, WSL idle shutdown is expected.

Gates run after remediation:

- `go build ./...`, `go vet ./...`, native `go test ./...`: PASS.
- `npm --prefix ui test -- --run`: 123/123 PASS.
- `npm --prefix ui run check`: 0 errors/warnings.
- `npm --prefix ui run build`: PASS.
- `test/windows/installer_test.ps1`: 205 checks, 0 failures.
- `test/release/release_windows_test.sh`: PASS, including the staged WSL assets.

Known residual: the managed distro still runs the agent/PHP execution as root (R2-M12). The install-token gate now prevents other local users from reaching it, but privilege separation inside the distro is still a hardening item for the Round-5 audit.
