# Windows/WSL planning checkpoint

- **Date:** 2026-10-26
- **Revised:** 2026-10-27 after the in-depth review
- **Status:** Design and implementation plan revised and internally consistent; implementation not started
- **Approval:** Architecture approved; revised plan awaiting execution approval
- **Linux baseline:** `10db2e5` (clean, pushed)
- **Current `master`:** the commit containing this file; run `git rev-parse HEAD`
- **Design:** [Windows 11 + WSL2 design](../specs/2026-10-26-windows-wsl-design.md)
- **Plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Review resolution:** [Windows plan review resolution](2026-10-27-windows-plan-review-resolution.md)
- **Knowledge handoff:** [Windows/WSL knowledge handoff](2026-10-26-windows-wsl-handoff.md)
- **Windows start guide:** [Next agent on Windows](2026-10-26-windows-next-agent.md)

## Decisions locked

- Windows 11 x86_64; managed Debian 12 WSL2 distro.
- Native Windows daemon + LocalSystem helper + WSL agent. Linux uses the
  existing daemon/helper without an agent.
- Native Linux PHP-FPM in WSL/Linux; no Docker PHP and no native Windows PHP.
- Docker Engine inside the managed WSL distro; Docker only for nginx/Apache,
  databases, Redis, and phpMyAdmin.
- Fixed roots: `/data/devstack` on Linux/WSL, `C:\devstack` on Windows.
- Default projects: `/data/devstack/projects` on Linux/WSL,
  `C:\devstack\projects` on Windows.
- Per-site source modes: `local`, `windows`, `wsl`; PHP/Node default to WSL,
  static to Windows.
- No devstack-created Windows names start with a dot.
- CA private key stays in the protected `system` subtree; SQLite stays on
  Windows NTFS; Docker/database data stays on ext4.
- **Windows public entry is always the helper → Windows daemon proxy.**
  The helper never forwards to WSL.
- **The Windows daemon terminates public TLS and routes Docker web traffic to
  a private HTTP port in WSL.** The dashboard is a daemon route.
- **The WSL agent runs a fixed 80/443 ingress to the Windows daemon via the
  WSL host gateway.** WSL `.test` names map to `127.0.0.1`.
- **Windows→WSL backend traffic requires WSL localhost forwarding.** The agent
  API also listens on the distro IP for control fallback.
- **All Windows site backends run in WSL.** The agent owns WSL port allocation.
- **WSL ext4 is canonical for generated site/service data, service secrets,
  runtimes, logs, and Docker data.** Windows keeps metadata, config, leaf
  certs, daemon logs, cache, and the VHDX.
- **The agent API uses HTTPS with a pinned certificate and bearer token.**
- **Helper and daemon control pipes are ACL-restricted and SID-checked.**
- **Global PHP/Composer, shells, and builds run in WSL.** Windows gets `.cmd`
  shims.
- **Windows Docker web servers listen on private HTTP ports; the daemon owns
  public TLS.**
- Existing Linux installs are not auto-migrated. New Linux installs use
  `/data/devstack` with a root-owned top-level root and `system` subtree.

## Builder-check resolutions

- WSL baseline: Windows 11 22H2+ x64, WSL 2.0.0+ with systemd. The installer
  validates but does not enable features or reboot.
- Managed distro identity is proven by `/data/devstack/managed.json`; unmarked
  `devstack` distros are refused.
- Five implementers may edit in parallel, but real machine-wide Windows
  resources are controller-serialized; lanes use isolated test roots/distro
  names/ports.
- Windows access to WSL database/Redis/phpMyAdmin ports goes through an agent
  loopback relay and helper loopback forwarding.
- WSL-mode projects are preserved by default on uninstall; removing WSL
  requires verified export or explicit discard.
- Existing Linux PHP builds must pass a Debian 12 WSL compatibility gate; a
  WSL-compatible variant is published if needed.
- The agent manages WSL `/etc/hosts` with `generateHosts=false` so `.test`
  names resolve inside WSL; the agent ingress makes them reachable.
- Source-mode changes are stopped-site, agent-verified tar copy operations;
  source is kept unless `--remove-source` is explicit.
- The WSL rootfs, Docker Engine/Compose, agent, and PHP/Node artifacts are
  pinned and published with checksums/SBOMs.

## Execution design

- Wave 0 foundation is sequential and now includes T0.1–T0.10.
- Wave 1 keeps five concurrent lanes: Windows helper, WSL agent, Windows
  daemon, Docker services, site source/UI. All five start from the frozen
  foundation. Lane D owns separate `internal/agentdocker`,
  `internal/agentrelay`, and `internal/agentweb` packages implementing the
  frozen `agentapi.Module` seam, so it does not edit Lane B’s agent server.
- Merge order is A→B→C→D→E because the integrated agent server mounts Lane D’s
  modules. This is a merge dependency, not a serialized start.
- Wave 2 parity lanes: PHP/Node/terminal/build/tooling, installer/lifecycle,
  Docker parity, performance/config/UI, packaging/acceptance.
- Maximum **5 active subagents** at any time; up to five implementers can work
  concurrently, then up to five reviewers.
- Each lane uses TDD, isolated worktrees, fresh reviews, and controller-owned
  merges.
- Full instructions are in the plan’s Parallel Execution Protocol.

## Pre-implementation checks

The first session must fix the checkout/tooling before T0.1:

- `core.autocrlf=false`, renormalize, and verify `bash -n` in Git Bash/WSL.
- `make` available or PowerShell equivalents documented.
- Node 22 for the UI/CI baseline.
- Working Go module downloads.
- WSL 2.0.0+ with systemd and a separate dev distro.
- Docker Engine inside WSL for controller tests.
- Administrator access for helper/service/ACL/80/443 tests.

## Pause point

No lane worktree, production code, or implementation subagent has been started.
The revised plan is the source of truth. Wait for human approval of the revised
design and plan before Wave 0 T0.1.
