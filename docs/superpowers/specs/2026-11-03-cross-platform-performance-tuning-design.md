# Cross-platform performance tuning (Windows + WSL)

## Intent

Every performance profile — Balanced, Throughput, and Custom — applies across
all three execution layers:

1. WSL service configuration: nginx, Apache, PHP-FPM, OPcache, and per-site PHP
   settings.
2. WSL operating system: sysctl values, file-descriptor limits, and the WSL2 VM
   resources.
3. Windows operating system: the Windows equivalents of the same tuning posture,
   plus reversible developer-path exclusions.

The selected profile is the source of truth. A profile is not considered applied
unless every required layer succeeds. Optional layers report warnings instead of
blocking the profile.

Throughput becomes the default selection for a new/unset performance profile,
and host tuning is selected by default. Applying still requires the existing
explicit confirmation; the confirmation lists every WSL and Windows change.

## Current state

- Service includes are already written into WSL under
  `\\wsl$\<distro>\data\devstack\services\...`.
- PHP-FPM and per-site PHP settings are already applied through the agent.
- Host tuning is disabled on Windows (`DisableHostTuning: true`).
- The Windows helper does not expose performance RPCs; the performance handlers
  are Linux-only.
- WSL still has stock values:
  `net.core.somaxconn=4096`, `net.core.netdev_max_backlog=1000`,
  `net.ipv4.ip_local_port_range=32768 60999`, and no devstack limits file.
- The Windows side has no devstack registry, power-plan, Defender, or
  `.wslconfig` tuning.

## Approaches considered

### A. Two privileged tuners, one orchestrator (chosen)

- WSL tuning is applied by a new authenticated agent module running as root in
  the distro.
- Windows tuning is applied by a new Windows-helper performance manager running
  as the privileged service.
- `.wslconfig` is written by the user-level daemon because it is a per-user file.
- The daemon performance service previews, applies, verifies, rolls back, and
  resets all layers.

This keeps privileged operations in the components that already own them and
keeps fixed paths/allowlists on both sides.

### B. WSL tuning through `wsl.exe` from the Windows helper

Rejected: it duplicates the agent’s root authority, bypasses the authenticated
agent boundary, and makes WSL state harder to verify.

### C. Windows tuning through the user daemon with elevation prompts

Rejected: the daemon is unprivileged and must not prompt for elevation or write
protected registry keys. The Windows helper already exists for this.

## Profile mapping

### WSL operating system

Reuse the existing allowlisted values and fixed paths:

```text
/etc/sysctl.d/99-devstack-performance.conf
/etc/security/limits.d/99-devstack-performance.conf
```

- Balanced and Throughput use the existing computed `HostValues`.
- Custom uses the custom host values after the same validation.
- The agent backs up existing files under
  `/data/devstack/backups/performance/<timestamp>/host/`, writes atomically,
  runs `sysctl --system`, verifies every key from `/proc/sys`, and rolls back on
  failure.
- Reset restores previous files or removes the managed files, then runs
  `sysctl --system`.
- Limits apply to new sessions; the result reports that explicitly.

### Windows operating system

All values are allowlisted; the helper never accepts arbitrary registry keys,
commands, or paths.

| Setting | Balanced | Throughput | Custom |
|---|---|---|---|
| Power plan | Balanced | High performance | Explicit `power_plan`, default Balanced |
| TCP dynamic port range | Windows default | 32768–60999 | Explicit range, clamped to 16384–65535 |
| `TcpTimedWaitDelay` | System default | 30 s | Explicit 30–300, default 30 |
| `NetworkThrottlingIndex` | System default | Disabled (`0xffffffff`) | Explicit, default disabled |
| `SystemResponsiveness` | System default | 0 | Explicit 0–100, default 0 |
| NTFS last-access updates | System default | Disabled | Explicit, default disabled |
| NTFS 8.3 names | System default | Disabled | Explicit, default disabled |
| Defender exclusions | Devstack/project/WSL paths | Same | Same, with an opt-out |
| `.wslconfig` memory | 50% of RAM, min 4 GB | 80% of RAM, leave 4 GB free | Explicit MB, clamped |
| `.wslconfig` processors | Physical cores | Logical processors | Explicit count, clamped |
| `.wslconfig` swap | 0 | 25% of memory, max 16 GB | Explicit MB, clamped |

Windows application details:

- Capture the current power-plan GUID with `powercfg /getactivescheme`; restore
  it on reset.
- Capture the current IPv4/IPv6 TCP and UDP dynamic port ranges with
  `netsh int ... show dynamicport`; restore them on reset.
- Capture registry values before writing; restore the exact previous value or
  delete the value if it did not exist.
- Verify every value by reading it back. Report `restart_required` for values
  that Windows only reads at boot.
- Defender exclusions use `Add-MpPreference`/`Remove-MpPreference` for fixed
  devstack paths plus the WSL VHDX path. Only paths added by devstack are
  removed on reset. If Defender is disabled or unavailable, record a warning and
  continue.
- `.wslconfig` is merged, not replaced. Other sections and keys are preserved.
  The previous file is backed up. The daemon reports `wsl_restart_required` and
  offers a separate restart action; it does not shut down WSL in the middle of
  the apply request.

### Not included

- Windows services, page file, kernel memory flags, scheduler priority
  separation, TCP congestion provider, NIC/RSS settings, and driver-specific
  registry values.
- Arbitrary user-supplied registry paths or commands.
- Automatic reboot. The result reports when a reboot is required.

## Components

### Performance engine

- Add `WindowsHostValues` and `WSLVMValues` to the computed `Values`.
- Add profile mapping and validation with safe clamps.
- Extend custom-profile validation so explicit Windows/WSL-VM values are
  accepted; omitted values fall back to the Throughput defaults.
- Keep the existing Linux `HostValues` unchanged for Linux installs.

### WSL agent

New authenticated module, e.g. `internal/agentperf`:

```text
POST /v1/performance/status
POST /v1/performance/apply
POST /v1/performance/reset
```

- Fixed paths, allowlisted keys, atomic writes, `sysctl --system`, /proc
  verification, backup, rollback, and reset.
- Never accepts a path, command, or free-form directive list.
- Returns file results, verified keys, backup directory, and steps.

### Windows helper

Add a Windows performance manager and routes:

```text
GET  /v1/performance/status
POST /v1/performance/apply
POST /v1/performance/reset
```

- Fixed allowlist and fixed registry/command targets.
- Backup state under `<helper SystemDir>/backups/performance/<timestamp>/`.
- Verify by read-back; report restart requirements.
- Reset restores captured power plan, port ranges, registry values, and
  devstack-added Defender exclusions.

### Daemon performance service

- Replace the single `HostHelper` with an orchestrated set of host tuners:
  WSL agent, Windows helper, and `.wslconfig`.
- Preview shows separate WSL and Windows sections with a diff for each.
- Apply order:
  1. write service includes,
  2. apply PHP config,
  3. apply WSL host tuning,
  4. apply Windows helper tuning,
  5. merge `.wslconfig`,
  6. validate/reload the active web server,
  7. persist the applied state.
- Any required-layer failure rolls back every completed layer in reverse order.
  Optional failures are warnings in the result and status.
- Reset reverses the same layers and removes only devstack-managed values.
- Persist per-layer results so status survives daemon restarts.

### Dashboard

- Default a new/unset profile to Throughput and pre-select host tuning.
- Replace “Host tuning unavailable on Windows” with two status cards:
  - WSL kernel and limits,
  - Windows host and WSL VM.
- Show changed files/settings, verification steps, backup location, warnings,
  and restart/reboot requirements.
- Custom mode exposes the Windows and WSL-VM fields.
- The apply confirmation lists both layers and warns that Defender exclusions
  and `.wslconfig` changes are machine-wide.

## Safety and failure handling

- Every Windows value has an exact backup and reset path.
- No setting is applied without read-back verification.
- If Windows tuning fails after WSL tuning succeeded, WSL is reset before the
  error is returned.
- If `.wslconfig` cannot be merged, the file is left untouched and the layer is
  reported as a warning.
- If Defender is unavailable, exclusions are skipped with a warning.
- If a required reboot or WSL restart is pending, the UI and status show it
  until the reset or restart.

## Acceptance

- Throughput apply writes the WSL sysctl/limits files, applies them live, writes
  every Windows setting, adds the devstack Defender exclusions, and merges
  `.wslconfig`.
- Balanced applies conservative Windows values and still covers every layer.
- Custom applies explicit Windows/WSL-VM values; omitted values fall back safely.
- Status reports each layer separately with paths, verification, backups, and
  restart requirements.
- Reset restores WSL sysctls/limits, the Windows power plan, port ranges,
  registry values, removes devstack-only Defender exclusions, and restores the
  previous `.wslconfig`.
- A failure in any required layer leaves the previous state intact.
- Windows Performance panel no longer says host tuning is unavailable.
- Throughput is the default for a fresh profile, and host tuning is selected by
  default.
- Unit tests cover mapping, validation, previews, rollback, and reset. Live
  verification covers the real WSL distro and the real Windows host.
