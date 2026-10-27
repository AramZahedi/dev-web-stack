# Wave 2 Lane G — Installers, Trust, Upgrade, Uninstall, and RootFS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the cross-platform installer lifecycle for Windows/WSL and Linux: a resumable Windows/WSL prerequisite installer (`install.ps1`), managed Debian 12 WSL distro provisioning, helper service registration, firewall configuration, secure `devstack init` user-phase provisioning with DPAPI token/TLS binding, safe transactional upgrade/rollback, thorough uninstall, and parity updates for Linux `install.sh`.

**Architecture:** A two-phase installation model: (1) elevated system phase (`install.ps1`) managing Windows features, WSL2, `C:\devstack` layout, helper service, and firewall; and (2) user phase (`devstack init`) establishing secure communication with the managed WSL Debian 12 distro, injecting DPAPI credentials, syncing CA trust and hosts, and validating localhost forwarding.

**Tech Stack:** PowerShell (5.1 & Core), Go 1.24, WSL 2.0.0+, Debian 12 Bookworm, systemd, Docker Engine, Windows Service Controller (SCM), Windows Filtering Platform (Firewall), DPAPI (Data Protection API).

**Spec:** `docs/superpowers/plans/2026-10-26-windows-wsl.md` (§Wave 2 Lane G) and `docs/decisions.md` (S69, S70, S71, S72, S73, S74).

## Global Constraints

- Supported Windows floor is Windows 10 22H2 (Build 19045) x64 and Windows 11. No Windows 11-only APIs.
- The Windows installer owns a complete, resumable prerequisite manifest (`install-manifest.json`) for Windows and WSL.
- Reboot-required feature changes use `-AllowReboot` or explicit prompt, schedule resume, and never reboot silently.
- Directory layout: fixed `C:\devstack` on NTFS; `system` directory protected with SYSTEM/Administrators DACL; `bin` directory on System PATH; distro in `C:\devstack\wsl\devstack`.
- No devstack-created Windows basename starts with a dot.
- Managed WSL distro name is `devstack`, running Debian 12 with systemd as PID 1 and Docker Engine rooted at `/data/devstack/docker`.
- Agent credentials: generated on host, stored in Windows DPAPI `securestore` (`agent-token`, `agent-pin`), and pushed to WSL `/data/devstack` with 0600/0700 permissions.
- Inbound Windows firewall rules for proxy ports 8080/8443 are scoped strictly to the WSL NAT subnet.
- All subagents dispatched for execution MUST use model `flash` (Gemini 3.8 Flash) strictly per user mandate.

## Review Focus

1. **Reboot handling:** If WSL or VirtualMachinePlatform is newly enabled and requires a reboot, ensure `install.ps1` saves resume state in `install-manifest.json` and does NOT reboot without `-AllowReboot`.
2. **Elevation verification:** Ensure `install.ps1` reliably detects non-elevated prompts and either elevates or fails with exit code 2 and actionable instructions.
3. **WSL networking mode check:** Enforce rejection of `.wslconfig` `networkingMode=mirrored` or non-NAT configurations before provisioning.
4. **Credential security:** Ensure DPAPI protects `agent-token` and `agent-pin` with current-user scope, and WSL `/data/devstack` files are strictly root `0600` / `0700`.
5. **Safe upgrade and uninstall:** Upgrade must verify new agent health before removing old distro state; uninstall must preserve project files and user data by default.

---

### Task G1: Windows Prerequisites, Layout, & Helper Service Installer (`install.ps1` - System Phase)

**Files:**
- Create: `install.ps1`
- Create: `test/windows/installer_test.ps1`
- Modify: `build/windows/devstack-daemon-task.ps1`

**Interfaces:**
- `install.ps1 [-InstallDir <path>] [-RootFSPath <path>] [-AllowReboot] [-SkipWSL] [-DryRun]`
- Exit codes: 0 (Success), 1 (General error), 2 (Elevation / prerequisite missing), 3010 (Reboot required)
- Manifest file: `C:\devstack\system\install-manifest.json` tracking prerequisite check states.

- [x] **Step 1:** Write failing tests in `test/windows/installer_test.ps1` covering elevation check, OS build validation (<19045 rejected, ≥19045 accepted), virtualization check, manifest creation, and dry-run output.
- [x] **Step 2:** Run `pwsh test/windows/installer_test.ps1` and verify RED.
- [x] **Step 3:** Implement elevation validation and self-elevation logic in `install.ps1`.
- [x] **Step 4:** Implement Windows build (≥ 19045 x64) and hardware virtualization detection.
- [x] **Step 5:** Implement Windows optional feature enablement (`Microsoft-Windows-Subsystem-Linux`, `VirtualMachinePlatform`) with DISM/Enable-WindowsOptionalFeature, reboot requirement detection (exit code 3010 / RestartNeeded), and resume scheduling with `-AllowReboot`.
- [x] **Step 6:** Implement `C:\devstack` layout creation: `C:\devstack\bin`, `C:\devstack\system`, `C:\devstack\wsl\devstack` with protected DACL (SYSTEM + Administrators FullControl; Users ReadAndExecute).
- [x] **Step 7:** Implement binary staging (`devstack.exe`, `devstack-helper.exe` to `C:\devstack\bin`) and add `C:\devstack\bin` to System PATH.
- [x] **Step 8:** Implement helper service installation (`devstack-helper service install --config C:\devstack\system\helper.yaml`) and service startup via Windows Service Controller.
- [x] **Step 9:** Implement inbound Windows Firewall rule for proxy ports 8080 and 8443 (TCP) scoped to WSL subnet.
- [x] **Step 10:** Update `build/windows/devstack-daemon-task.ps1` to reference `C:\devstack\bin\devstack.exe` and register user logon task.
- [x] **Step 11:** Run `pwsh test/windows/installer_test.ps1` and verify GREEN.
- [x] **Step 12:** Commit `feat(installer): add Windows system phase and prerequisite orchestrator`.

---

### Task G2: WSL Managed Distro, Rootfs, & Agent Service Provisioning (`install.ps1` - WSL Phase)

**Files:**
- Modify: `install.ps1`
- Modify: `test/windows/installer_test.ps1`
- Create: `build/wsl/install-agent.sh`
- Reference: `build/wsl/versions.lock`, `build/wsl/build-rootfs.sh`, `internal/wsl/provision.go`

**Interfaces:**
- Managed distro: `devstack` imported at `C:\devstack\wsl\devstack` with `--version 2`.
- Marker: `/data/devstack/managed.json`.
- Systemd: PID 1 verification.
- Services: Docker Engine with `--data-root /data/devstack/docker` and `devstack-agent.service`.

- [ ] **Step 1:** Write failing tests in `test/windows/installer_test.ps1` for WSL version check (≥ 2.0.0), `.wslconfig` NAT validation, rootfs checksum verification against `versions.lock`, distro import, and systemd validation.
- [ ] **Step 2:** Run `pwsh test/windows/installer_test.ps1` and verify RED.
- [ ] **Step 3:** Implement WSL version verification (`wsl.exe --version` ≥ 2.0.0) and automated update via `wsl.exe --update`.
- [ ] **Step 4:** Implement `.wslconfig` inspection enforcing NAT networking (rejecting mirrored mode).
- [ ] **Step 5:** Implement rootfs archive acquisition/verification (SHA-256 matching `build/wsl/versions.lock`).
- [ ] **Step 6:** Implement managed distro import (`wsl.exe --import devstack C:\devstack\wsl\devstack <rootfs.tar.gz> --version 2`).
- [ ] **Step 7:** Configure `/etc/wsl.conf` with `[boot] systemd=true` and initialize `/data/devstack/managed.json`.
- [ ] **Step 8:** Implement Docker Engine setup inside the distro with data-root `/data/devstack/docker` using `build/wsl/install-docker.sh`.
- [ ] **Step 9:** Stage Linux `devstack-agent` binary and install systemd unit `build/wsl/agent.service` at `/etc/systemd/system/devstack-agent.service`.
- [ ] **Step 10:** Update `C:\devstack\system\install-manifest.json` with recorded versions of WSL, Debian, systemd, Docker, and agent.
- [ ] **Step 11:** Run `pwsh test/windows/installer_test.ps1` and verify GREEN.
- [ ] **Step 12:** Commit `feat(installer): add WSL distro import and agent service provisioning`.

---

### Task G3: `devstack init` Windows/WSL User Phase Integration

**Files:**
- Modify: `internal/cli/init.go`
- Create: `internal/cli/init_windows.go`
- Create: `internal/cli/init_windows_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- `devstack init` executes on Windows host:
  1. Dials `devstack-helper` named pipe.
  2. Ensures helper CA and installs Windows user trust store.
  3. Verifies managed WSL distro `devstack` is running.
  4. Generates agent bearer token & TLS identity, stores in Windows DPAPI `securestore` (`agent-token`, `agent-pin`).
  5. Transfers token, TLS certificate, key, and `agent.yaml` into WSL `/data/devstack/` with owner-only 0600 permissions.
  6. Starts/restarts `devstack-agent.service` in WSL.
  7. Installs root CA into WSL agent via `agentclient.InstallCA`.
  8. Synchronizes domains to Windows `hosts` (via helper) and WSL `/etc/hosts` (via agent).
  9. Starts Windows daemon (`ensureRunning`).
  10. Verifies localhost forwarding probe and doctor health.

- [ ] **Step 1:** Write failing tests in `internal/cli/init_windows_test.go` mocking helper, securestore, WSL runner, and agent client to verify the complete sequence.
- [ ] **Step 2:** Run `go test ./internal/cli -run TestWindowsInit` and verify RED.
- [ ] **Step 3:** Implement Windows-specific init helper in `internal/cli/init_windows.go` to handle agent credentials generation, DPAPI storage, and WSL filesystem transfer.
- [ ] **Step 4:** Wire CA bundle installation to WSL agent and dual-hosts synchronization (`helper.SyncHosts` for Windows and agent `POST /v1/hosts` for WSL).
- [ ] **Step 5:** Wire localhost forwarding probe (`wsl.ProbeLocalhostForwarding`) and daemon readiness verification into `init.go`.
- [ ] **Step 6:** Run `go test ./internal/cli` natively on Windows and verify GREEN.
- [ ] **Step 7:** Commit `feat(cli): wire devstack init user phase for Windows and WSL`.

---

### Task G4: Lifecycle Operations — Upgrade, Rollback, & Uninstall

**Files:**
- Create: `uninstall.ps1`
- Modify: `install.ps1`
- Create: `test/windows/lifecycle_test.ps1`
- Modify: `internal/cli/root.go` (Windows uninstall subcommand wiring)

**Interfaces:**
- `install.ps1 -Upgrade`
- `uninstall.ps1 [-PurgeDistro] [-PurgeData] [-Force]`
- `devstack uninstall` on Windows.

- [ ] **Step 1:** Write failing tests in `test/windows/lifecycle_test.ps1` testing upgrade transactional rollback (simulating agent failure), clean uninstall, and distro retention.
- [ ] **Step 2:** Run `pwsh test/windows/lifecycle_test.ps1` and verify RED.
- [ ] **Step 3:** Implement upgrade flow in `install.ps1 -Upgrade`: backup `/data/devstack`, stage new binaries, replace agent/distro, verify health, and rollback on error.
- [ ] **Step 4:** Implement `uninstall.ps1`:
  - Remove trust from Windows root store via `devstack-helper`.
  - Remove hosts entries.
  - Remove Windows Firewall rules (`devstack-daemon-proxy`).
  - Stop and unregister `devstack-helper` Windows Service.
  - Remove scheduled task `devstack-daemon`.
  - Distro cleanup: keep WSL distro and user data by default; remove only if `-PurgeDistro` is specified.
  - Remove `C:\devstack\bin` from System PATH.
- [ ] **Step 5:** Wire `devstack uninstall` CLI command on Windows to execute equivalent cleanup.
- [ ] **Step 6:** Run `pwsh test/windows/lifecycle_test.ps1` and verify GREEN.
- [ ] **Step 7:** Commit `feat(installer): add upgrade, rollback, and uninstall lifecycle`.

---

### Task G5: Linux Installer Parity & Cross-Platform Verification

**Files:**
- Modify: `install.sh`
- Modify: `build/systemd/devstack-helper.service`
- Modify: `test/release/installer_test.sh`

**Interfaces:**
- Support `/data/devstack` roots and root-only `system` layout while preserving legacy `~/.devstack` and `/etc/devstack` installs (Decision S69).
- `test/release/installer_test.sh` passes 100%.

- [ ] **Step 1:** Write failing test in `test/release/installer_test.sh` asserting new installs use `/data/devstack` for root layout while legacy installs are preserved.
- [ ] **Step 2:** Run `bash test/release/installer_test.sh` in WSL and verify RED.
- [ ] **Step 3:** Update `install.sh` and `build/systemd/devstack-helper.service` with `/data/devstack` default and legacy migration logic.
- [ ] **Step 4:** Run `bash test/release/installer_test.sh` in WSL and verify GREEN.
- [ ] **Step 5:** Run full Windows native suite (`go test ./...`) and Linux regression suite in WSL.
- [ ] **Step 6:** Commit `feat(installer): update Linux install.sh for /data/devstack layout and test parity`.

---

### Task G6: Lane G Review, Verification, and Integration

- [ ] **Step 1:** Run native Windows `go test ./...`, `go vet ./...`, and `go build ./...`.
- [ ] **Step 2:** Dispatch independent reviewer subagent (using `Model: "flash"`).
- [ ] **Step 3:** Fix any review findings with TDD (RED -> GREEN).
- [ ] **Step 4:** Merge `lane/windows-installer` into `feature/windows-wsl`.
- [ ] **Step 5:** Run `test/windows/vertical_slice.ps1` elevated on host machine to verify live green gate.
