# Windows/WSL planning checkpoint

- **Date:** 2026-10-26
- **Status:** design and implementation plan complete; implementation not started
- **Approval:** architecture approved; plan awaiting execution approval
- **Linux baseline:** `10db2e5` (clean, pushed)
- **Design:** [Windows 11 + WSL2 design](../specs/2026-10-26-windows-wsl-design.md)
- **Plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Knowledge handoff:** [Windows/WSL knowledge handoff](2026-10-26-windows-wsl-handoff.md)
- **Windows start guide:** [Next agent on Windows](2026-10-26-windows-next-agent.md)

## Decisions locked

- Windows 11 x86_64; managed Debian 12 WSL2 distro.
- Native Windows daemon + LocalSystem helper + WSL agent. Linux uses the existing daemon/helper without an agent.
- Native Linux PHP-FPM in WSL/Linux; no Docker PHP and no native Windows PHP.
- Docker Engine inside the managed WSL distro; Docker only for nginx/Apache, databases, Redis, and phpMyAdmin.
- Fixed roots: `/data/devstack` on Linux/WSL, `C:\devstack` on Windows.
- Default projects: `/data/devstack/projects` on Linux/WSL, `C:\devstack\projects` on Windows.
- Per-site source modes: `local`, `windows`, `wsl`; PHP/Node default to WSL, static to Windows.
- No devstack-created Windows names start with a dot.
- CA private key stays in the protected `system` subtree; SQLite stays on Windows NTFS; Docker/database data stays on ext4.
- Windows helper owns 80/443; WSL Docker web servers sit behind helper-forwarded private ports.
- Symlink/junction bridging between Windows and WSL is rejected.
- Existing Linux installs are not auto-migrated.

## Builder-check resolutions

- WSL baseline: Windows 11 22H2+ x64, WSL 2.0.0+ with systemd. The installer validates but does not enable features or reboot.
- Managed distro identity is proven by `/data/devstack/managed.json`; unmarked `devstack` distros are refused.
- Five implementers may edit in parallel, but real machine-wide Windows resources are controller-serialized; lanes use isolated test roots/distro names/ports.
- Windows access to WSL database/Redis/phpMyAdmin ports goes through an agent loopback relay and helper loopback forwarding.
- WSL-mode projects are preserved by default on uninstall; removing WSL requires verified export or explicit discard.
- Existing Linux PHP builds must pass a Debian 12 WSL compatibility gate; a WSL-compatible variant is published if needed.
- The agent manages WSL `/etc/hosts` with `generateHosts=false` so `.test` names resolve inside WSL.
- Source-mode changes are stopped-site copy-and-verify operations; source is kept unless `--remove-source` is explicit.

## Execution design

- Wave 0 foundation is sequential.
- Wave 1 has five parallel lanes: Windows helper, WSL agent, Windows daemon, Docker services, site source/UI.
- Wave 2 has five parity lanes: PHP/Node/terminal, installer/lifecycle, Docker parity, performance/config/UI, packaging/acceptance.
- Maximum **5 active subagents** at any time.
- Each lane uses TDD, isolated worktrees, fresh reviews, and controller-owned merges.
- Full instructions are in the plan's Parallel Execution Protocol.

## Pause point

No lane worktree, production code, or implementation subagent has been started. Wait for human approval of the design and plan before Wave 0.
