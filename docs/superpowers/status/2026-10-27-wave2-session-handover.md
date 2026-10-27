# Wave 2 Session Handover & Complete Status Documentation

**Date:** 2026-10-27  
**Repository:** `c:\Users\Aram\Projects\Go\dev-web-stack`  
**Current Active Branch:** `feature/windows-wsl` (`bd72e28`)  
**Mandatory Subagent Rule:** All subagents for development and review MUST use Gemini 3.8 Flash (`Model: "flash"`). No other model is permitted.

---

## 1. Executive Status

Every task across **Wave 0**, **Wave 1**, and **Wave 2 (Lanes F, G, H, I, J)** has been fully implemented, rigorously tested via TDD, and independently reviewed and approved by dedicated reviewer subagents.

- **Wave 0 & Wave 1:** 100% complete, reviewed, and merged into `feature/windows-wsl`.
- **Wave 2 Lane G (Installers, Lifecycle, Prerequisites, Rootfs):** 100% complete, reviewed, and merged into `feature/windows-wsl` (Commit: `04dec2f`).
- **Wave 2 Lanes F, H, I, J (Parallel Parity Lanes):** 100% complete, tested, and reviewed with **APPROVED** verdicts on their respective lane branches.
- **Next Immediate Action upon Resume:** Execute **Task M1 (Integration 2)** — Merge lanes in order: **Lane F → Lane H → Lane I → Lane J** into `feature/windows-wsl`, verify tests after each merge, and execute `test/windows/vertical_slice.ps1` on the host machine.

---

## 2. Authoritative Branch & Commit Map

| Lane | Branch | Worktree Path | Tip SHA | Review Status | Key Deliverables |
|---|---|---|---|:---:|---|
| **Base / Integration** | `feature/windows-wsl` | `C:\Users\Aram\Projects\Go\dev-web-stack` | `bd72e28` | **MERGED & GREEN** | Wave 0 + T0.11 + Wave 1 (A-E) + Wave 2 Lane G + Plans |
| **Lane G (Installer & RootFS)** | `lane/windows-installer` | *(merged & worktree removed)* | `89407a5` | **APPROVED & MERGED** | `install.ps1`, `uninstall.ps1`, Debian 12 WSL import, helper service, DPAPI `devstack init`, Linux `install.sh` parity |
| **Lane F (Tooling Parity)** | `lane/wsl-tooling-parity` | `C:\Users\Aram\Projects\Go\devstack-lanes\tooling-parity` | `70dab64` | **APPROVED** | WSL PHP catalog gate, FPM pool generation (`fpm.conf`), Node runtime/builds, PTY WebSocket terminal, `.cmd` shims |
| **Lane H (Docker Parity)** | `lane/wsl-docker-parity` | `C:\Users\Aram\Projects\Go\devstack-lanes\docker-parity` | `8f0150b` | **APPROVED** | Compose lifecycles on ext4, database/Redis/phpMyAdmin services, Windows private web serving (18080), proxy router target updates |
| **Lane I (Performance & UI)** | `lane/windows-perf-ui` | `C:\Users\Aram\Projects\Go\devstack-lanes\perf-ui` | `ad6d824` | **APPROVED** | Windows PDH/MemoryStatus resource collector, host tuning disabled banner, public route `.test` benchmarks, service config editor via agent, UI source mode badges |
| **Lane J (Release & Docs)** | `lane/windows-acceptance-docs` | `C:\Users\Aram\Projects\Go\devstack-lanes\acceptance-docs` | `1818a75` | **APPROVED** | Windows release packaging (zip/tar.gz with SPDX 2.3 SBOM), manual CI matrix, complete 5-part docs suite, enhanced `vertical_slice.ps1` |

---

## 3. Detailed Implementation Records per Lane

### Lane G: Cross-Platform Installers, Lifecycle, RootFS, & Prerequisites
- **Files:** `install.ps1`, `uninstall.ps1`, `test/windows/installer_test.ps1`, `test/windows/lifecycle_test.ps1`, `internal/cli/init_windows.go`, `internal/cli/init.go`, `install.sh`, `build/systemd/devstack-helper.service`.
- **System Phase (`install.ps1`):**
  - Elevation detection returning exit code 2 when unprivileged.
  - OS check requiring 64-bit architecture and Windows 10 22H2 Build 19045+ or Windows 11.
  - Hardware virtualization detection in BIOS/firmware.
  - Windows optional features (`Microsoft-Windows-Subsystem-Linux`, `VirtualMachinePlatform`) with DISM detection of reboot requirement (exit 3010 without `-AllowReboot`; scheduled resume task on reboot with `-AllowReboot`).
  - Layout creation on local NTFS: `C:\devstack\bin`, `C:\devstack\system` (protected DACL: SYSTEM and Administrators FullControl, Users ReadAndExecute, inheritance disabled), `C:\devstack\wsl\devstack`.
  - Staging binaries to `bin` and prepending `C:\devstack\bin` to System PATH.
  - Staging `uninstall.ps1` to `C:\devstack\uninstall.ps1`.
  - Helper service installation (`devstack-helper service install --config C:\devstack\system\helper.yaml`) and service start.
  - Inbound Windows Firewall rule `devstack-daemon-proxy` for TCP ports 8080/8443 scoped to the WSL NAT subnet.
- **WSL Managed Distro Phase:**
  - Verifies `wsl.exe --version` >= 2.0.0 and `.wslconfig` NAT networking mode (rejects `mirrored` mode).
  - Pinned Debian 12 rootfs SHA-256 verification against `build/wsl/versions.lock`.
  - Imports distro `devstack` with `--version 2`.
  - Configures `/etc/wsl.conf` with `[boot] systemd=true` and verifies PID 1 is `systemd`.
  - Creates `/data/devstack/managed.json` marker.
  - Installs Docker Engine with data-root at `/data/devstack/docker`.
  - Stages Linux `devstack-agent` binary and installs `/etc/systemd/system/devstack-agent.service` (`build/wsl/install-agent.sh`).
  - Records installed versions in `install-manifest.json`.
- **User Phase (`devstack init` on Windows):**
  - Generates 32-byte cryptographic bearer token and ECDSA P-256 TLS identity.
  - Persists secrets in Windows DPAPI `securestore` (`agent-token`, `agent-pin`).
  - Securely transfers `agent.token` (0600), `agent.crt` (0644), `agent.key` (0600), and `agent.yaml` (0600) into WSL `/data/devstack/`.
  - Restarts `devstack-agent.service` in WSL.
  - Pushes helper root CA to WSL agent via `agentClient.InstallCA(ctx, ca.RootPEM)`.
  - Synchronizes domains to both Windows `hosts` (via helper) and WSL `/etc/hosts` (via agent).
  - Starts Windows daemon and verifies localhost forwarding probe.
- **Lifecycle (Upgrade, Rollback, Uninstall):**
  - `install.ps1 -Upgrade`: backs up `/data/devstack` to `/data/devstack-backup.tar.gz`, stages updated binaries, verifies agent health, and automatically rolls back on failure.
  - `uninstall.ps1`: removes CA trust, cleans hosts block, removes firewall rule, deletes helper service, removes scheduled task, preserves WSL distro and projects by default (unless `-PurgeDistro`), purges secrets if `-PurgeData`, and cleans System PATH.
- **Linux Parity (`install.sh`):**
  - Defaults new installs to `/data/devstack` layout while preserving legacy `~/.devstack` and `/etc/devstack` (Decision S69).

### Lane F: WSL PHP/Node/FPM, Terminal, Builds, and Tooling Parity
- **Files:** `internal/agent/runtime.go`, `internal/agent/php.go`, `internal/agent/fpm.go`, `internal/agent/terminal.go`, `internal/agent/build.go`, `internal/tooling/windows.go`, `internal/tooling/php.go`, `internal/tooling/composer.go`.
- **PHP/FPM Parity:** Agent runtime install/remove/list (`/v1/runtimes`), `CheckDebian12PHPCompatibility` verifying `php -v` and `php-fpm -v` execution, `PreparePHP` generating `/data/devstack/sites/<site>/fpm.conf`, and `StartPHP`/`StopPHP` zero-downtime generation swaps.
- **Node & Builds:** Detection of npm, pnpm, and yarn lockfiles; Node runtime PATH injection; Windows source mode (`/mnt/c`) performance warning logging.
- **Site Terminal & PTY:** WebSocket upgrade at `GET /v1/sites/{ref}/terminal`, single-use 30-second ticket authorization, PTY shell spawning (`github.com/creack/pty`), terminal resize frames, text frame relay, and graceful process cleanup.
- **Tooling Shims:** `EnsureWindowsToolShims` and `EnsureWindowsSiteShims` generating `php.cmd`, `composer.cmd`, `shell.cmd`, `exec.cmd` with safe POSIX quoting for paths containing spaces.

### Lane H: Docker Service Parity & Private Web Serving
- **Files:** `internal/agentdocker/docker.go`, `internal/agentweb/module.go`, `internal/agentrelay/service_relay.go`, `internal/servicecontrol/*`, `internal/dockerweb/*`, `internal/services/*`, `internal/proxy/router.go`.
- **Compose Parity:** Generates `/data/devstack/services/compose.yaml` with pinned digests; controls container lifecycles (`install`, `up`, `down`, `stop`, `restart`, `rotate-credentials`).
- **Database & Services:** Parity for MySQL, MariaDB, PostgreSQL, Redis, phpMyAdmin with volume mounts in `/data/devstack/services/<service>/`.
- **Credential Rotation:** Live in-container rotation (`ALTER USER`/`ALTER ROLE`) with 15-second rollback and atomic 0600 secret updates.
- **Private Web Serving & Proxy Updates:** Web vhosts rendered on HTTP port 18080 in WSL; `Router.UpdateDockerWebRoutes` updates router targets to `127.0.0.1:18080` while preserving host dashboard routes (`devstack.test`) and TLS certificates.

### Lane I: Performance, Benchmarks, Config, Ports, and UI Parity
- **Files:** `internal/performance/collector_windows.go`, `internal/performance/collect_limits_windows.go`, `internal/performance/benchmarks.go`, `internal/performance/service/service.go`, `internal/servicecontrol/config.go`, `internal/daemon/windows.go`, `ui/src/lib/format.ts`, `ui/src/lib/components/PerformancePanel.svelte`, `ui/src/routes/Sites.svelte`, `ui/src/routes/SiteDetail.svelte`, `ui/src/routes/Settings.svelte`.
- **Resource Collector:** Windows physical RAM collection via `GlobalMemoryStatusEx`, CPU count, 65,536 handle quota, empty sysctl table.
- **Host Tuning Banner:** Sets `HostTuning.Available: false` on Windows; skips host sysctl/limits tuning on `Apply` and `Reset` without error; renders clear banner in UI Performance panel.
- **Benchmarks:** Targets `PublicRouteURL(domain)` (`https://<domain>.test/`) on Windows; custom dialer resolves `.test` domains through local loopback forwarders.
- **Service Config via Agent:** `AgentConfigAPI` delegates config reading, editing, diffing, applying, and resetting through WSL agent, avoiding host NTFS filesystem writes.
- **UI Parity:** Added source mode badges (`Windows drive` / `WSL distro`) to Sites list and SiteDetail views; reactive listener ports editing form with change detection and restart warnings.

### Lane J: Acceptance, Release, and Documentation
- **Files:** `build/release-windows.sh`, `build/release-windows.ps1`, `Makefile`, `.github/workflows/ci.yml`, `docs/install-windows.md`, `docs/wsl.md`, `docs/windows-paths.md`, `docs/windows-networking.md`, `docs/troubleshooting.md`, `test/windows/vertical_slice.ps1`, `docs/README.md`, `docs/handoff.md`.
- **Release Packaging:** Builds `devstack_<version>_windows_amd64.zip` and `.tar.gz` containing `devstack.exe`, `devstack-helper.exe`, `devstack-agent` (Linux ELF), `debian-12-wsl-rootfs.tar.gz`, `install.ps1`, `uninstall.ps1`, checksums, and SPDX 2.3 SBOM (`devstack.spdx.json`).
- **CI Matrix:** Added manual `workflow_dispatch` matrix for Windows cross-build and native Windows runner (`windows-2022`).
- **Documentation:** Complete, truthful documentation covering installation, WSL architecture, UNC/path translation, networking/firewalls, and troubleshooting.
- **Vertical Slice:** Enhanced `test/windows/vertical_slice.ps1` testing build >= 19045, elevation, WSL systemd, Docker, services, DPAPI secrets, static Windows/WSL sites, PHP-FPM execution, tooling shims, dual HTTPS, and doctor diagnostics.

---

## 4. Test Verification Summary

Every suite was verified with 100% pass rates:

1. **Windows Installer Tests (`test/windows/installer_test.ps1`):**
   - **92 checks, 0 failures — ALL PASS**.
2. **Windows Lifecycle Tests (`test/windows/lifecycle_test.ps1`):**
   - **43 checks, 0 failures — ALL PASS**.
3. **Linux Installer Tests (`test/release/installer_test.sh` in WSL):**
   - **18 checks, 0 failures — ALL PASS**.
4. **Windows Release Packaging Tests (`test/release/release_windows_test.sh` in WSL):**
   - **ALL PASS** (verifies PE `MZ` headers, Linux `ELF` header, SPDX 2.3 SBOM, internal checksums, corruption detection).
5. **CI Workflow Tests (`test/release/workflow_test.sh` in WSL):**
   - **54 assertions passed — ALL PASS**.
6. **Go Unit & Integration Test Suites:**
   - `internal/cli` (including 13 Windows init unit tests): **PASS**.
   - `internal/agent`, `internal/tooling`: **PASS**.
   - `internal/agentdocker`, `internal/agentrelay`, `internal/agentweb`, `internal/servicecontrol`, `internal/dockerweb`, `internal/services`: **PASS**.
   - `internal/performance`, `internal/performance/service`: **PASS**.
   - Entire repository (`go test ./...`): **PASS across all 55+ packages**.
   - Static analysis (`go vet ./...`): **0 errors, 0 warnings**.
7. **UI Test Suite (Vitest):**
   - **15 test files, 118 tests passed — ALL PASS**.

---

## 5. Next Steps to Resume (Task M1: Integration 2)

When resuming the session, execute **Task M1 (Integration 2)** following these exact sequential steps:

### Step 1: Merge Lanes in Sequence into `feature/windows-wsl`
From the main repository directory `C:\Users\Aram\Projects\Go\dev-web-stack`:
```powershell
# 1. Merge Lane F (Tooling Parity)
git -c safe.directory=* merge lane/wsl-tooling-parity --no-ff -m "merge(wsl-tooling): integrate Wave 2 Lane F (PHP/Node/FPM, terminal, tooling parity)"
go test ./internal/agent/... ./internal/tooling/...

# 2. Merge Lane H (Docker Parity)
git -c safe.directory=* merge lane/wsl-docker-parity --no-ff -m "merge(wsl-docker): integrate Wave 2 Lane H (Docker services & private web serving)"
go test ./internal/agentdocker/... ./internal/agentrelay/... ./internal/dockerweb/...

# 3. Merge Lane I (Performance & UI)
git -c safe.directory=* merge lane/windows-perf-ui --no-ff -m "merge(perf-ui): integrate Wave 2 Lane I (performance, benchmarks, config, UI parity)"
go test ./internal/performance/... ./internal/servicecontrol/...
cd ui; npm test -- --run; cd ..

# 4. Merge Lane J (Release & Docs)
git -c safe.directory=* merge lane/windows-acceptance-docs --no-ff -m "merge(release-docs): integrate Wave 2 Lane J (release packaging, CI, docs, vertical slice)"
```

### Step 2: Remove Lane Worktrees
```powershell
git -c safe.directory=* worktree remove C:\Users\Aram\Projects\Go\devstack-lanes\tooling-parity
git -c safe.directory=* worktree remove C:\Users\Aram\Projects\Go\devstack-lanes\docker-parity
git -c safe.directory=* worktree remove C:\Users\Aram\Projects\Go\devstack-lanes\perf-ui
git -c safe.directory=* worktree remove C:\Users\Aram\Projects\Go\devstack-lanes\acceptance-docs
```

### Step 3: Run Full Validation Suite
```powershell
# Windows Host:
go test ./...
go vet ./...
pwsh test/windows/installer_test.ps1
pwsh test/windows/lifecycle_test.ps1

# WSL ext4:
wsl.exe -d Ubuntu -- bash -lc "cd /mnt/c/Users/Aram/Projects/Go/dev-web-stack && go test ./... && bash test/release/release_windows_test.sh && bash test/release/installer_test.sh"
```

### Step 4: Execute Live Host Vertical Slice
Run elevated in PowerShell on the Windows host:
```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File test/windows/vertical_slice.ps1
```
Verify all gates pass (Windows build, Administrator elevation, WSL 2.0.0+, managed distro systemd, Docker Engine, helper service, daemon status, Windows static site, WSL static site, live PHP-FPM, dual-direction HTTPS, and doctor diagnostics).
