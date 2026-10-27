# Windows 10/11 + WSL2 Audit Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:dispatching-parallel-agents and superpowers:subagent-driven-development to implement this plan task-by-task across isolated lanes. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remediate all 19 audit findings (5 Critical, 6 High, 8 Medium) identified in `docs/superpowers/audits/2026-10-28-windows-review-findings.md`, restore complete functional parity, harden the security boundary, fix the installer lifecycle and rollback mechanisms, make native Windows tests 100% green, and update all specs and plans.

**Architecture:** 
1. **Security & Boundary (C1, M5):** Restrict dashboard API access to loopback-only clients or require a cryptographically verified local token so the public `0.0.0.0:8080/8443` proxy cannot be abused to self-mint session tokens from WSL or LAN; harden named-pipe peer identity checks.
2. **Parity & Tooling (C2, H3, H4, H5):** Wire real `Services`, `Terminal`, and `Build` managers in `internal/daemon/windows.go`; implement Windows path conversion in `.cmd` shims; fix doctor port and shim checks; resolve `internal/phpini` test failures.
3. **Installer & Lifecycle (C3, C4, H1, H2, H6, M4, M8):** Ensure transactional rollback with verified backup before live modification, stop running helper before binary replace, preserve arguments and run reboot-resume in user context, defer firewall rule creation until after WSL adapter is ready, remove fake agent stubs, and register daemon autostart logon task.
4. **Release & Packaging (C5, M6, M7):** Disallow placeholder rootfs fallbacks in production release builds, enforce rootfs size floors and real hash verification, make payload embedding in `devstack-installer` kill-safe.
5. **Specs, Documentation & Verification (M1, M2, M3):** Reconcile specs and handoff docs, harden `vertical_slice.ps1` to test services/terminal/build and tooling, run full multi-platform test suite.

---

## Lane Breakdown for Parallel Execution

- **Lane A (Security Boundary & Proxy):** C1, M5
- **Lane B (Daemon Parity & Tooling Shims & Doctor):** C2, H3, H4, H5
- **Lane C (Installer, Uninstaller & Service Lifecycle):** C3, C4, H1, H2, H6, M4, M8
- **Lane D (Packaging, Release & TLS):** C5, M6, M7
- **Lane E (Specs, Plans & Hardened Vertical Slice):** M1, M2, M3, specs and plans updates

---

## Lane Breakdown for Parallel Execution

- **Lane A (Security Boundary & Proxy):** C1, M5
- **Lane B (Daemon Parity & Tooling Shims & Doctor):** C2, H3, H4, H5
- **Lane C (Installer, Uninstaller & Service Lifecycle):** C3, C4, H1, H2, H6, M4, M8
- **Lane D (Packaging, Release & TLS):** C5, M6, M7
- **Lane E (Specs, Plans & Hardened Vertical Slice):** M1, M2, M3, specs and plans updates
- **Lane Round 2 (Deeper Audit Remediation):** R2-H7, R2-H8, R2-M9, R2-M10, R2-M11, R2-M12, R2-L1, R2-L2, R2-L3

---

## Detailed Task Breakdown

### Lane A: Security Boundary & Proxy (C1, M5)
- [x] **Task A1 (C1): Restrict Dashboard API & Enforce Authentication Boundary**
  - Files: `internal/api/middleware.go`, `internal/proxy/handler.go`, `internal/api/server.go`, `internal/api/security_boundary_test.go`, `internal/proxy/security_boundary_test.go`
  - Enforce loopback check on `/api/v1/*` routes; reject non-loopback requests to dashboard API with 403 Forbidden.
  - Reject requests targeting `devstack.test` through the public proxy with 403 Forbidden.
  - Set `Secure: true` on cookies when served over TLS, and reject cleartext API session requests.
  - Added regression tests proving `http://<wsl-gateway>:8080/api/v1/session` and `/api/v1/sites` fail closed.
- [x] **Task A2 (M5): Harden IPC Peer Identity Resolution**
  - Files: `internal/ipc/ipc_windows.go`, `internal/ipc/ipc_windows_test.go`
  - Mitigate PID reuse TOCTOU when validating pipe peer tokens on Windows by using `windows.ImpersonateNamedPipeClient` directly on the pipe handle.

### Lane B: Daemon Parity, Tooling Shims & Doctor (C2, H3, H4, H5)
- [x] **Task B1 (C2): Wire Services, Terminal, and Build in Windows Daemon**
  - Files: `internal/daemon/windows.go`
  - Mirrored Linux daemon initialization: wired `servicecontrol.Manager`, `terminal.Manager`, and build service into `api.Config`.
  - Ensures `POST /api/v1/services/<id>/install`, `POST /api/v1/sites/<ref>/terminal/session`, and `POST /api/v1/sites/<ref>/build` perform real actions instead of returning 503.
- [x] **Task B2 (H5 & H3): Implement Windows Path Translation in `.cmd` Shims**
  - Files: `internal/tooling/windows.go`
  - Updated `php.cmd` and `composer.cmd` templates to convert Windows path arguments (e.g. `C:\...`) to `/mnt/c/...` or Linux paths using `wslpath` before passing to WSL `php` / `composer`.
  - Verified `go test ./internal/phpini` passes 100% when `C:\devstack\bin` is in `PATH`.
- [x] **Task B3 (H4): Fix Doctor Accuracy & Unskip Shim Tests**
  - Files: `internal/doctor/checks.go`, `internal/doctor/checks_tools.go`, `internal/doctor/checks_tools_windows.go`, `internal/doctor/checks_tools_other.go`, `internal/doctor/checks_tools_test.go`, `internal/doctor/doctor_test.go`
  - Updated port checks to detect Windows `WSAEADDRINUSE` (errno 10048) and verify active listening/responses, eliminating false probe warnings on healthy devstack/helper listeners.
  - Updated `checks_tools.go` to inspect persistent Machine/User Registry PATH and `binDir` shims if in-process `exec.LookPath` is stale.
  - Unskipped and passed `TestCheckToolsRecognizesWindowsCmdShimsWithoutExecuteBits`.

### Lane C: Installer, Uninstaller & Service Lifecycle (C3, C4, H1, H2, H6, M4, M8)
- [x] **Task C1 (C3): Transactional Upgrade, Backup Verification & Helper Replacement**
  - Files: `install.ps1`
  - Removed `|| true` on WSL backup tar command; check exit codes and verify archive integrity (`tar -tzf`) before proceeding.
  - Fixed failure tracking: removed unconditional reset `$upgradeFailed = $false`.
  - Reordered upgrade steps: perform WSL backup *before* Windows binary staging.
  - Staged rollback: extract to `.rollback-tmp` before atomic swap.
  - Stopped `DevstackHelper` service before replacing `devstack-helper.exe` to prevent file sharing violations, then restarted it.
- [x] **Task C2 (C4): Fix Reboot-Resume Execution Context**
  - Files: `install.ps1`
  - Captured interactive user SID and CLI args in `resume-state.json`. Resumed run operates in user context; helper config receives user SID rather than SYSTEM SID.
  - Cleaned up scheduled tasks reliably on failure as well as success.
- [x] **Task C3 (H1): Defer & Scope Firewall Rule Creation**
  - Files: `install.ps1`
  - Moved firewall rule creation to *after* WSL distro import when the `vEthernet (WSL)` adapter and subnet are actively assigned.
  - Fail closed if WSL subnet cannot be determined; never leave an open, unscoped inbound allow rule.
  - Do not create inbound proxy firewall rules when `-SkipWSL` is specified.
- [x] **Task C4 (H2): Exit-Code Hygiene & Delete Fake Agent Stub**
  - Files: `install.ps1`, `build/wsl/install-agent.sh`
  - Checked exit codes on `devstack-helper service install` and service start.
  - Deleted fake shell script fallback; abort cleanly if real agent binary is missing.
  - Verified real agent health probe before writing manifest `agent_installed = true`.
- [x] **Task C5 (H6 & M8): Daemon Autostart & Upgrade Restart**
  - Files: `install.ps1`, `uninstall.ps1`, `build/windows/devstack-daemon-task.ps1`
  - Registered logon scheduled task during installation using `devstack-daemon-task.ps1`.
  - In upgrade flow, restart user daemon upon successful completion.
  - Ensured `uninstall.ps1` cleanly unregisters the logon task.
- [x] **Task C6 (M4): Update Installer Regression Suite**
  - Files: `test/windows/installer_test.ps1`
  - Added test assertions for backup failure detection, rollback safety, helper stop/start during upgrade, firewall scoping, and resume task configuration (expanded to 168 checks).

### Lane D: Packaging, Release Scripts & TLS Pinning (C5, M6, M7)
- [x] **Task D1 (C5): Fail-Closed Release Packaging & Pinned Hash**
  - Files: `build/release-windows.ps1`, `build/release-windows.sh`, `build/wsl/versions.lock`, `install.ps1`
  - Disallowed silent creation of mock placeholder rootfs in release scripts unless an explicit `--mock-rootfs` development flag is supplied.
  - Enforced rootfs size floor (`1073741824` bytes) and verified real checksum.
  - Updated `install.ps1` to only print "RootFS archive checksum verified" when an expected hash was genuinely matched.
- [x] **Task D2 (M7): Safe Payload Embedding for Standalone Installer**
  - Files: `build/release-windows.ps1`, `build/release-windows.sh`, `cmd/devstack-installer/installer.go`, `cmd/devstack-installer/installer_test.go`
  - Stage installer build in temporary directory `dist/.installer-stage-*`, preserving tracked empty `cmd/devstack-installer/payload.zip` intact even on aborted builds.
- [x] **Task D3 (M6): TLS Pinning Documentation & Validity**
  - Files: `internal/agent/tls.go`, `internal/agent/tls_test.go`
  - Documented pinning rationale, 10-year leaf validity, rotation lifecycle, and error handling.

### Lane E: Specs, Plans, Handoff & Hardened Vertical Slice (M1, M2, M3)
- [x] **Task E1 (M1 & Specs/Plans Update): Reconcile Specifications, Plans & Handoff**
  - Files: `docs/handoff.md`, `README.md`, `docs/install-windows.md`, `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`, `docs/superpowers/plans/2026-10-26-windows-wsl.md`
  - Removed stale "implementation has not started" phrases; updated architecture docs to reflect the completed and hardened implementation.
  - Documented Windows 10 22H2 verification gap and test matrix requirements (M2).
- [x] **Task E2 (M3): Harden Vertical Slice Gate**
  - Files: `test/windows/vertical_slice.ps1`
  - Made `php.cmd` and `composer.cmd` execution mandatory (no soft passes).
  - Added vertical slice steps for service lifecycle, terminal session ticket issuance, and site build triggers.

### Lane Round 2: Deeper Audit Remediation (R2-H7 to R2-L3)
- [x] **Task R2-1 (R2-H7): Truthful Serving Mode Switch on Windows**
  - Files: `internal/daemon/windows.go`, `internal/daemon/windows_test.go`, `internal/sites/vhosts.go`, `internal/sites/vhosts_windows_test.go`, `internal/cli/vhost.go`, `internal/cli/vhost_test.go`
  - Eliminate error swallowing in `helperAdapter`; surface `ErrNotImplemented` rather than fabricating `ConfigOK: true`.
  - Rejection with clear error `"external serving modes (nginx, caddy, apache) are not supported on Windows; builtin mode is active"` in CLI and sites service.
- [x] **Task R2-2 (R2-H8): 8.3 Short-Name Bypass Protection**
  - Files: `internal/proxy/php.go`, `internal/proxy/php_test.go`
  - In `evalPHPPath`, inspect every segment of resolved relative path against dotfile prefixes (`.`) and backup suffixes (`~`, `.bak`), preventing 8.3 short names from leaking `.env` or `.git`.
- [x] **Task R2-3 (R2-M9): Uninstaller Hosts Cleanup & UTF-8 BOM Elimination**
  - Files: `uninstall.ps1`
  - Replaced loose regex with exact string matching (`# BEGIN devstack` and `# END devstack`).
  - Used `[System.IO.File]::WriteAllText($hostsPath, $cleanedText, (New-Object System.Text.UTF8Encoding($false)))` to eliminate BOM on PowerShell 5.1.
- [x] **Task R2-4 (R2-M10): PHP Runtime `extension_dir` Verification & Warnings**
  - Files: `internal/doctor/checks_php.go`, `internal/doctor/checks_php_test.go`
  - Validated `lib/php/extensions` directory for WSL backend builds.
  - Warns in `devstack doctor` when compiled-in `extension_dir` points to a nonexistent `/tmp` directory without compensation in `etc/php.ini`.
- [x] **Task R2-5 (R2-M11 & R2-M12): Environment Overrides Sandbox Isolation & Execution Model Docs**
  - Files: `install.ps1`, `docs/install-windows.md`
  - Route all `DEVSTACK_TEST_*` environment lookups through `Get-TestOverride`, strictly ignoring them unless `-TestSandbox` is passed.
  - Documented root execution model and NTFS/WSL cross-boundary file permissions.
- [x] **Task R2-6 (R2-L1, R2-L2, R2-L3): API, Logs, and Terminal Hardening**
  - Files: `internal/api/server.go`, `internal/logs/redact.go`, `internal/logs/redact_test.go`, `internal/agent/terminal.go`, `internal/agent/runtime_test.go`
  - Fail closed (panic) on `rand.Read` failure during secret initialization.
  - Extended redaction patterns to match `passwd|pass|pwd` variants.
  - Supported `X-Terminal-Ticket` and `Sec-WebSocket-Protocol` headers for terminal ticket auth.

---

## Final Verification & Gate Execution
- [x] Run `go vet ./...` and `go test ./...` on Windows (including `internal/phpini` with `C:\devstack\bin` in PATH): 100% PASS.
- [x] Run `pwsh -File test/windows/installer_test.ps1`: 168/168 checks PASSED.
- [x] Run `npm --prefix ui run check` and `npm --prefix ui test -- --run`: 118/118 tests PASSED (0 errors, 0 warnings).
- [x] Re-run audit verification appendix checks (C1 probe, C2 wiring, H4 doctor output): doctor reports `ok: true`, `ports: ok`, `tools: ok`.
