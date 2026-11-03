# Cross-Platform Performance Tuning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply Balanced, Throughput, and Custom profiles to WSL services, the WSL kernel/limits, the WSL2 VM, and the Windows host, with automatic application, backup, verification, and reset.

**Architecture:** The existing performance service keeps orchestration and service includes. A Windows composite host tuner calls the authenticated WSL agent for sysctl/limits, the Windows helper for power/TCP/registry/Defender, and a daemon-side `.wslconfig` manager for WSL VM resources. Any required failure rolls back completed layers.

**Tech Stack:** Go, WSL agent HTTP API, Windows helper named pipe, PowerShell/netsh/powercfg/fsutil, INI `.wslconfig`.

**Spec:** `docs/superpowers/specs/2026-11-03-cross-platform-performance-tuning-design.md`

## Global Constraints

- Every profile applies every layer: WSL services, WSL OS, Windows OS.
- Throughput is the default profile for a fresh/unset state.
- Host tuning is selected by default; the apply confirmation remains explicit.
- Every Windows value is allowlisted, backed up, verified, and reset.
- No arbitrary registry paths, commands, paths, or services.
- Defender exclusions and `.wslconfig` are machine-wide changes and must be listed in the confirmation.
- `.wslconfig` changes report a WSL restart requirement; no automatic reboot.

## Review Focus

- A partial Windows failure must not leave WSL tuning applied without its matching Windows layer.
- A reset must restore the exact previous power plan, port range, registry values, and `.wslconfig`.
- Custom profiles must not silently discard Windows values.
- Defender being unavailable must be a warning, not a failed profile.
- Existing Linux-only host tuning must keep working unchanged.

---

### Task 1: Profile mapping and validation

**Files:**
- Create: `internal/performance/windows.go`
- Modify: `internal/performance/values.go`
- Test: `internal/performance/windows_test.go`

**Interfaces:**
- `type WindowsHostValues struct { PowerPlan string; DynamicPortMin, DynamicPortMax int; TCPTimedWaitDelaySeconds int; NetworkThrottlingIndex uint32; SystemResponsiveness uint32; DisableLastAccess, Disable8dot3, DefenderExclusions bool; WSLMemoryMB, WSLProcessors, WSLSwapMB int }`
- `Values.Windows WindowsHostValues`
- `func computeWindowsHost(res Resources, profile Profile) (WindowsHostValues, error)`
- `func (w WindowsHostValues) Validate() error`

- [ ] **Step 1: Write failing tests** for Balanced (Balanced power plan, default ports, no throttling disable), Throughput (High performance, ports 32768–60999, timed-wait 30, throttling disabled, last-access/8.3 disabled, memory 80%, logical processors), and Custom fallback/validation.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement the struct, mapping, clamps, and validation.**
- [ ] **Step 4: Run `go test ./internal/performance -count=1`; expect PASS.**
- [ ] **Step 5: Commit** `feat(performance): map profiles to Windows host values`.

### Task 2: Agent performance protocol and client

**Files:**
- Create: `internal/agentproto/performance.go`
- Modify: `internal/agentclient/client.go`
- Test: `internal/agentclient/client_test.go`

**Interfaces:**
- `agentproto.PerformanceHostValues`, `PerformanceWindowsValues`, `WSLVMValues`, `PerformanceApplyRequest`, `PerformanceStatusResponse`, `PerformanceApplyResponse`, `PerformanceResetRequest`, `PerformanceResetResponse`, `PerformanceTargetStatus`.
- `agentclient.Client.PerformanceStatus/Apply/Reset`.

- [ ] **Step 1: Write failing client tests** using an httptest agent server that asserts the endpoint and decoded request.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Add types and methods.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(agentproto): add performance tuning contract`.

### Task 3: WSL agent performance module

**Files:**
- Create: `internal/agentperf/module.go`
- Create: `internal/agentperf/apply.go`
- Test: `internal/agentperf/apply_test.go`
- Modify: `cmd/devstack-agent/main.go`

**Interfaces:**
- `agentperf.New(Config{Root, SysctlPath, LimitsPath, Runner}) (*Module, error)`
- Module implements `agentapi.Module` on `/v1/performance/`.
- Apply writes fixed files, backs up under `<root>/backups/performance/<timestamp>/host/`, runs `sysctl --system`, verifies `/proc/sys`, rolls back on failure.
- Reset restores/removes fixed files and runs `sysctl --system`.

- [ ] **Step 1: Write failing tests** with a temp root and fake runner for apply, verification failure/rollback, status, and reset.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Mount the module and commit** `feat(agent): apply WSL host tuning`.

### Task 4: Windows helper performance manager

**Files:**
- Create: `internal/helper/performance_windows.go`
- Modify: `internal/helper/winserver.go`
- Modify: `internal/helperproto/performance.go`
- Test: `internal/helper/performance_windows_test.go`

**Interfaces:**
- `helperproto.PerformanceWindowsValues` and `PerformanceWSLVMValues` added to `PerformanceApplyRequest`.
- `helperproto.PerformanceTargetStatus` and `Targets []PerformanceTargetStatus` added to status/apply/reset responses.
- Windows manager uses a `PerformanceCommandRunner` seam.
- Apply: power plan, IPv4/IPv6 TCP/UDP dynamic ports, `TcpTimedWaitDelay`, `NetworkThrottlingIndex`, `SystemResponsiveness`, NTFS last-access/8.3, Defender exclusions.
- Backup JSON under `<SystemDir>/backups/performance/<timestamp>/windows.json`.
- Verify by read-back; reset restores exact prior state and removes only devstack-added exclusions.

- [ ] **Step 1: Write failing tests** with a fake runner and temp SystemDir for apply commands, verification, reset, and idempotence.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement manager and routes.**
- [ ] **Step 4: Run `go test ./internal/helper -count=1` on Windows; expect PASS.**
- [ ] **Step 5: Commit** `feat(helper): apply Windows host tuning`.

### Task 5: `.wslconfig` manager

**Files:**
- Create: `internal/wslconfig/config.go`
- Test: `internal/wslconfig/config_test.go`

**Interfaces:**
- `func Apply(path string, values WSLVMValues) (backup string, restartRequired bool, err error)`
- `func Reset(path string) error`
- Preserves unrelated sections/keys; replaces only `[wsl2] memory`, `processors`, `swap`.

- [ ] **Step 1: Write failing tests** for merge, preserve, backup, reset, and invalid values.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(wslconfig): manage WSL VM resources`.

### Task 6: Daemon composite host tuner

**Files:**
- Create: `internal/daemon/windows_performance.go`
- Modify: `internal/daemon/windows.go`
- Modify: `internal/performance/service/service.go`
- Test: `internal/daemon/windows_performance_test.go`

**Interfaces:**
- `windowsPerformanceHost` implements `perfservice.HostHelper`.
- It calls the WSL agent, Windows helper, and `.wslconfig` manager; status combines targets; apply rolls back completed layers on required failure; reset reverses.
- `windowsPerformanceServiceConfig` sets `DisableHostTuning: false` and `Helper` to the composite.
- Default profile becomes `throughput` when no stored profile exists.

- [ ] **Step 1: Write failing tests** with fake WSL/Windows tuners for combined status, apply order, rollback, reset, and Defender warning.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go test ./internal/daemon ./internal/performance/... -count=1`; expect PASS.**
- [ ] **Step 5: Commit** `feat(daemon): orchestrate Windows and WSL tuning`.

### Task 7: Defaults and dashboard

**Files:**
- Modify: `ui/src/lib/types.ts`
- Modify: `ui/src/lib/components/PerformancePanel.svelte`
- Modify: `ui/src/lib/components/PerformancePanel.test.ts`
- Modify: `internal/performance/service/service.go`
- Test: `internal/performance/service/service_windows_test.go`

**Interfaces:**
- Fresh profile defaults to Throughput.
- Host tuning checkbox defaults checked when available.
- Windows status shows WSL and Windows target details instead of “unavailable”.
- Apply confirmation lists both layers and restart requirements.

- [ ] **Step 1: Write failing service and UI tests.**
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run Go and UI tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(ui): default to throughput and show both tuning layers`.

### Task 8: Live tuning verification

- [ ] **Step 1: Apply Throughput with host tuning.**
- [ ] **Step 2: Verify WSL sysctl/limits files and live values.**
- [ ] **Step 3: Verify Windows power plan, dynamic ports, registry values, Defender exclusions, and `.wslconfig`.**
- [ ] **Step 4: Apply Balanced; verify conservative values.**
- [ ] **Step 5: Apply Custom; verify explicit overrides.**
- [ ] **Step 6: Reset; verify every layer restored.**
- [ ] **Step 7: Commit any fixes found.**
