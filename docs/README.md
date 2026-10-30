# devstack documentation

This directory is the starting point for both users and contributors. It contains the
current implementation documentation, the handoff for continuing the project on another
machine, and the design/plan for the next milestone.

## Start here

| Document | Purpose |
|---|---|
| [Handoff](handoff.md) | Current repo/release state, reference workstation, known issues, and how to resume elsewhere |
| [Architecture](architecture.md) | How the implemented system works today, package map, data model, and current limitations |
| [Requirements](requirements.md) | Product requirements, user concerns, decisions, and traceability to the design |
| [Decision log](decisions.md) | Implemented and planned decisions with rationale and rejected alternatives |
| [Services guide](services.md) | Service lifecycle, config editing, performance/host tuning, benchmarks, and the site terminal |
| [Planned service platform](planned-services.md) | High-level overview of the service platform; detailed spec linked below |
| [Known issues](known-issues.md) | Current bugs, parked work, and limitations that should not be mistaken for completed features |
| [Glossary](glossary.md) | Project terminology: sites, modes, services, conflicts, providers |
| [Developer guide](development.md) | build/test/debug, package boundaries, migrations, release, commit discipline |

## Current design and plans

- [Per-Site Node Toolchains and Global Node Default](superpowers/specs/2026-10-30-node-toolchain-design.md)
  - implemented 2026-10-30; live Node discovery, per-site Node for every kind,
    global Node default, and dashboard/CLI installation.
- [Node toolchain handoff](superpowers/status/2026-10-30-node-toolchain-handoff.md)
  - implementation status, findings, risks, and verification evidence.
- [Node toolchain implementation plan](superpowers/plans/2026-10-30-node-toolchain.md)
  - executed task-by-task TDD plan.
- [Node toolchain E2E report](superpowers/audits/2026-10-30-node-toolchain-e2e.md)
  - live dashboard/CLI coverage, bugs found and fixed, and remaining caveats.
- [Service containers, editable config, and performance design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)
  — Waves 0–2 and the next batch implemented; remaining host gates noted.
- [Windows 11 + WSL2 design](superpowers/specs/2026-10-26-windows-wsl-design.md)
  — revised 2026-10-27; planned native Windows daemon/helper, managed WSL2
  agent, WSL ingress, and Docker services; not implemented.
- [Windows/WSL implementation plan](superpowers/plans/2026-10-26-windows-wsl.md)
  — revised five-lane TDD plan, frozen contracts, Wave 0 T0.1–T0.10, merge
  protocol, and Windows acceptance matrix.
- [Windows plan review resolution](superpowers/status/2026-10-27-windows-plan-review-resolution.md)
  — the 2026-10-27 review findings and resolved contracts.
- [Windows/WSL planning checkpoint](superpowers/status/2026-10-26-windows-planning.md)
  — locked decisions, execution shape, and the planning pause point.
- [Windows/WSL knowledge handoff](superpowers/status/2026-10-26-windows-wsl-handoff.md)
  — consolidated decisions, rejected alternatives, caveats, risks, and open questions.
- [Next agent on Windows](superpowers/status/2026-10-26-windows-next-agent.md)
  — environment checklist, toolchain/CRLF prerequisites, Wave 0 start, and
  five-lane dependency rules.
- [M1 design](superpowers/specs/2026-10-22-dev-web-stack-m1-design.md)
  — released Linux foundation.
- [External vhosts design](superpowers/specs/2026-10-22-external-vhosts-design.md)
  — native nginx/Apache mode.
- [Service platform program plan](superpowers/plans/2026-10-25-service-platform-program.md)
  — parallel worktree/merge mechanism and wave dependency graph.
- [Service platform foundation plan](superpowers/plans/2026-10-25-service-platform-foundation.md)
  — detailed Wave 0 implementation tasks.
- [Wave 0 kickoff](superpowers/plans/2026-10-25-service-platform-wave0-kickoff.md)
  — foundation execution record.
- [Wave 1 plan](superpowers/plans/2026-10-25-service-platform-wave1.md)
  — service lifecycle, database/web-server/tooling implementation.
- [Next Batch Plan](superpowers/plans/2026-10-26-service-platform-next-batch.md)
  — config editor, performance, terminal, ports, UI cleanup.
- [Next Batch Completion](superpowers/status/2026-10-26-next-batch-completion.md)
  — delivered scope, verification evidence, and remaining limitations.
- [Wave 0 review](superpowers/status/2026-10-25-wave0-review.md),
  [Wave 1 handoff](superpowers/status/2026-10-25-wave1-handoff.md),
  [Wave 2 handoff](superpowers/status/2026-10-25-wave2-handoff.md),
  [Next Batch Completion](superpowers/status/2026-10-26-next-batch-completion.md), and
  [Continuation State](superpowers/status/2026-10-26-continuation.md)
  — verification and remaining work.
- [Documentation audit findings](../DOCUMENTATION_AUDIT_FINDINGS.md)
  — reviewed and resolved on 2026-10-25.
- Implementation plans and acceptance reports live in
  [`superpowers/plans/`](superpowers/plans/).
- Historical plan handoffs and the released M1 handoff live in
  [`superpowers/status/`](superpowers/status/).
- Security audit material lives in [`superpowers/audits/`](superpowers/audits/).

## User guides

The M1 user documentation covers the released Linux product, and the Windows documentation covers the Windows 10/11 and WSL2 environment:

| Guide | Platform | Contents |
|---|---|---|
| [Linux Install](install.md) | Linux | Prerequisites, installer, release archives, systemd, config, logs, uninstall |
| [Windows Install](install-windows.md) | Windows | Prerequisites, `install.ps1`, layout, verification, upgrades, `uninstall.ps1` |
| [WSL Managed Distro](wsl.md) | Windows/WSL | Managed Debian 12 distro, systemd, Docker Engine, agent, token/TLS security |
| [Windows Paths & Tooling](windows-paths.md) | Windows/WSL | Dual source modes (NTFS vs ext4), UNC paths, translation, `.cmd` shims, no-dot policy |
| [Windows Networking](windows-networking.md) | Windows/WSL | Loopback proxy, ports, WSL ingress, firewall scoping, trusted HTTPS |
| [Troubleshooting](troubleshooting.md) | All | Error codes, doctor diagnoses, port conflicts, automated repair |
| [Upgrade](upgrade.md) | Linux | Preflight, stop/install/start, database backup, protocol mismatch, verification, rollback limits |
| [Release](release.md) | All | Tags, archives/checksums, GoReleaser workflow, Windows release packager, release checklist |
| [Sites](sites.md) | All | First static site, Node sites, lifecycle commands, limitations |
| [PHP](php.md) | All | PHP builds, sites, ini and extensions, migration, FPM ports, troubleshooting |
| [Vhosts](vhosts.md) | Linux | Native nginx/Apache serving, PHP FastCGI prerequisites, mode switching, TLS custody |
| [TLS and trust](tls.md) | All | Local CA, browser trust, repair, curl/Node usage |

## Implementation status in one sentence

The Linux M1 native application is implemented and released as `v0.1.0`. Service
platform Waves 0–2 are implemented on Linux. Windows 10 22H2+ / Windows 11
platform parity is implemented across Wave 2 (Lanes F, G, H, I, J) with native
Windows helper and user daemon, managed Debian 12 WSL2 agent, dedicated Docker
Engine, dual source modes, `.cmd` tooling shims, release packaging
(`build/release-windows.sh`), and the vertical slice acceptance suite
(`test/windows/vertical_slice.ps1`). Full multi-machine acceptance evidence is
pending before declaring Windows general availability. Automatic GitHub CI is
disabled; workflows are manual-dispatch only.
