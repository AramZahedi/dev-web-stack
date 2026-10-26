# devstack documentation

This directory is the starting point for both users and contributors. It contains the
current implementation documentation, the handoff for continuing the project on another
machine, and the design for the next service-platform milestone.

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

- [Service containers, editable config, and performance design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)
  — Waves 0–2 and the next batch implemented; remaining host gates noted.
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

The M1 user documentation remains authoritative for the released native product:

| Guide | Contents |
|---|---|
| [Install](install.md) | prerequisites, installer, release archives, systemd, config, logs, uninstall |
| [Upgrade](upgrade.md) | preflight, stop/install/start, database backup, protocol mismatch, verification, rollback limits |
| [Release](release.md) | tags, archive/checksums, GoReleaser workflow, release checklist, PHP catalog gate |
| [Sites](sites.md) | first static site, Node sites, lifecycle commands, limitations |
| [PHP](php.md) | PHP builds, sites, ini and extensions, migration, FPM ports, troubleshooting |
| [Vhosts](vhosts.md) | native nginx/Apache serving, PHP FastCGI prerequisites, mode switching, TLS custody |
| [TLS and trust](tls.md) | local CA, browser trust, repair, curl/Node usage |

## Implementation status in one sentence

The Linux M1 native application is implemented and released as `v0.1.0`. The old
standalone devstack-in-Docker image was retired on 2026-10-25. Waves 0–2 and the next
service-platform batch are implemented: Docker nginx/Apache, MySQL/MariaDB/PostgreSQL/Redis,
phpMyAdmin, credentials/rotation, global PHP/Composer, site shell/exec, static builds,
service configuration editing, performance preview/apply/reset/benchmarks, site-scoped
terminal, editable listener ports, and the dashboard surfaces. Remaining work is
external-site Docker mounts, real root host-tuning acceptance, and live benchmark
evidence. Per-site PHP-FPM pool sizing is persisted and applied to live masters.
ImageMagick CLI is descoped. Automatic GitHub CI is disabled; workflows are
manual-dispatch only.
