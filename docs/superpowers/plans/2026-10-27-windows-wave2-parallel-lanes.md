# Wave 2 Parallel Parity Lanes (F, H, I, J) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement each lane task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver complete platform parity for Windows/WSL across PHP/Node/terminal/tooling (Lane F), Docker services & private web serving (Lane H), performance/benchmarks/ports/UI (Lane I), and release packaging/acceptance/documentation (Lane J).

**Architecture:** Each lane operates in an isolated worktree branched from `feature/windows-wsl` (which includes Wave 0, Wave 1, and Lane G's installer and managed distro infrastructure). Implementer and Reviewer subagents run strictly with Gemini 3.8 Flash (`Model: "flash"`).

**Tech Stack:** Go 1.24, TypeScript / Svelte 5, Vite, WSL2, Debian 12, Docker Engine, Docker Compose, Windows Service Controller, DPAPI.

**Spec:** `docs/superpowers/plans/2026-10-26-windows-wsl.md` (§Wave 2 Lanes F, H, I, J) and `docs/decisions.md`.

## Global Constraints

- Subagent Model Rule: All subagents dispatched for execution and review MUST use model `flash` (Gemini 3.8 Flash) strictly per user mandate.
- All Windows paths must avoid dot-prefixed basenames (Decision S70).
- Windows SQLite database must reside on local NTFS filesystem with exclusive flock (Decision S68).
- Service and container data reside exclusively on WSL ext4 `/data/devstack/` (Decision S64).
- Inbound Windows Firewall rules remain scoped to the WSL NAT subnet (Decision S72).

---

## Lane F — PHP/Node/FPM, Terminal, Builds, and Tooling Parity

**Branch:** `lane/wsl-tooling-parity`  
**Worktree:** `C:\Users\Aram\Projects\Go\devstack-lanes\tooling-parity`  
**Files:**
- Modify: `internal/agent/runtime.go`, `internal/agent/php.go`, `internal/agent/fpm.go`, `internal/agent/terminal.go`, `internal/agent/build.go`
- Modify: `internal/tooling/windows.go`, `internal/tooling/php.go`, `internal/tooling/composer.go`
- Tests: `internal/agent/runtime_test.go`, `internal/tooling/windows_test.go`

### Tasks for Lane F:
- [ ] **Task F1 (WSL PHP/FPM Parity):** Implement agent PHP runtime catalog compatibility check on Debian 12, FPM pool configuration generation in `/data/devstack/sites/<site>/fpm.conf`, and pool process management.
- [ ] **Task F2 (WSL Node & Build Parity):** Implement agent Node runtime execution, npm/pnpm/yarn detection, and background project builds for Windows and WSL source modes.
- [ ] **Task F3 (Site Terminal & PTY):** Implement site-scoped PTY through agent WebSocket (`GET /v1/sites/{ref}/terminal`), ticket authentication, resize, and clean process teardown.
- [ ] **Task F4 (Windows Tooling Shims):** Implement Windows `.cmd` wrappers for global PHP/Composer and site shell execution with proper argument quoting and space handling.
- [ ] **Task F5 (Verification & Review):** Run tests, dispatch Lane F reviewer subagent (`Model: "flash"`), commit `feat: add WSL PHP, Node, terminal, build, and tooling parity`.

---

## Lane H — Docker Service Parity & Private Web Serving

**Branch:** `lane/wsl-docker-parity`  
**Worktree:** `C:\Users\Aram\Projects\Go\devstack-lanes\docker-parity`  
**Files:**
- Modify: `internal/agentdocker/docker.go`, `internal/agentweb/module.go`, `internal/agentrelay/service_relay.go`
- Modify: `internal/servicecontrol/*`, `internal/dockerweb/*`, `internal/services/*`
- Tests: `internal/agentdocker/docker_test.go`, `internal/agentrelay/service_relay_test.go`

### Tasks for Lane H:
- [ ] **Task H1 (Compose Engine Parity):** Verify Compose file rendering, container lifecycles, and credential management inside WSL ext4 `/data/devstack/docker`.
- [ ] **Task H2 (Database & Cache Services):** Enable MySQL, MariaDB, PostgreSQL, Redis, and phpMyAdmin with volume storage in `/data/devstack/services/`.
- [ ] **Task H3 (Private Web Serving):** Implement Windows private HTTP port serving (18080) for Docker web vhosts, daemon TLS termination, and loopback relay.
- [ ] **Task H4 (Service Target Updates):** Update proxy router targets when Docker web services start or stop.
- [ ] **Task H5 (Verification & Review):** Run tests, dispatch Lane H reviewer subagent (`Model: "flash"`), commit `feat: complete WSL docker services`.

---

## Lane I — Performance, Benchmarks, Config, Ports, and UI

**Branch:** `lane/windows-perf-ui`  
**Worktree:** `C:\Users\Aram\Projects\Go\devstack-lanes\perf-ui`  
**Files:**
- Modify: `internal/performance/collector_windows.go`, `internal/performance/benchmarks.go`
- Modify: `internal/servicecontrol/config.go`, `internal/api/handlers_service_config.go`
- Modify: `ui/src/lib/components/*`, `ui/src/routes/*`
- Tests: `internal/performance/*_test.go`, UI vitest suite

### Tasks for Lane I:
- [ ] **Task I1 (Windows Resource Collector):** Collect CPU/memory metrics via Windows PDH / GlobalMemoryStatusEx, disable host sysctl tuning on Windows with clear banner.
- [ ] **Task I2 (Benchmarks via Public Route):** Implement HTTP benchmarking from Windows host targeting `https://<site>.test/` with stored results.
- [ ] **Task I3 (Service Config Management):** Implement config editing, diff, apply, and rollback for services via WSL agent.
- [ ] **Task I4 (Listener Port Management & UI):** Support customizable ports (HTTP, HTTPS, UI) with helper target updates and WSL ingress sync.
- [ ] **Task I5 (Verification & Review):** Run tests, dispatch Lane I reviewer subagent (`Model: "flash"`), commit `feat: add windows performance, config, ports, and UI parity`.

---

## Lane J — Acceptance, Release, and Documentation

**Branch:** `lane/windows-acceptance-docs`  
**Worktree:** `C:\Users\Aram\Projects\Go\devstack-lanes\acceptance-docs`  
**Files:**
- Modify: `.github/workflows/ci.yml`, `Makefile`
- Create: `docs/install-windows.md`, `docs/wsl.md`, `docs/windows-paths.md`, `docs/windows-networking.md`, `docs/troubleshooting.md`
- Modify: `test/windows/vertical_slice.ps1`, `docs/README.md`, `docs/handoff.md`

### Tasks for Lane J:
- [ ] **Task J1 (Release Packaging):** Update Makefile and release scripts to build Windows release archive containing `devstack.exe`, `devstack-helper.exe`, `devstack-agent` (Linux ELF), `install.ps1`, `uninstall.ps1`, and checksums.
- [ ] **Task J2 (CI Workflow):** Add manual-dispatch `workflow_dispatch` Windows build/test matrix in `.github/workflows/ci.yml`.
- [ ] **Task J3 (Documentation Suite):** Author complete documentation for Windows installation, WSL integration, UNC/path translation, networking/firewall, and troubleshooting.
- [ ] **Task J4 (Live Vertical Slice Validation):** Refine `test/windows/vertical_slice.ps1` to test the full integrated stack.
- [ ] **Task J5 (Verification & Review):** Run tests, dispatch Lane J reviewer subagent (`Model: "flash"`), commit `docs: add windows acceptance and release`.
