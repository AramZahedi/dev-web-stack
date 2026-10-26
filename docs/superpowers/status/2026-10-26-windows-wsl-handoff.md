# Windows 11 + WSL2 Knowledge Handoff

- **Date:** 2026-10-26
- **Status:** architecture and implementation plan complete; implementation not started
- **Current Linux baseline:** `10db2e5`
- **Current `master`:** the commit containing this file; run `git rev-parse HEAD`
- **Design:** [Windows 11 + WSL2 design](../specs/2026-10-26-windows-wsl-design.md)
- **Plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Planning checkpoint:** [Windows planning checkpoint](2026-10-26-windows-planning.md)
- **Windows startup guide:** [Next agent on Windows](2026-10-26-windows-next-agent.md)

This document is the consolidated memory of the Windows/WSL design discussion. It records the decisions, rejected alternatives, caveats, unresolved risks, and the exact execution model so work can resume without the original conversation.

## 1. Product direction

The user wants first-class Windows support, not merely running the Linux product inside a WSL shell.

The chosen architecture is:

- **Native Windows daemon** for the UI/API, SQLite, routing, builtin proxy, and orchestration.
- **Native Windows helper** running as LocalSystem for Windows hosts, trust stores, CA, firewall, and 80/443.
- **Managed Debian 12 WSL2 agent** for Linux PHP-FPM, Node, static backends, logs, ports, and Docker services.
- **Docker only for infrastructure services**: nginx/Apache, MySQL/MariaDB, PostgreSQL, Redis, phpMyAdmin.
- **No Docker PHP.**
- **No native Windows PHP.**
- **No Docker Desktop requirement.** Docker Engine runs inside the managed WSL distro.
- Linux continues to use the existing native daemon/helper pair without an agent.

The reason for native Windows daemon/helper plus a WSL agent is to keep Windows OS integration native while using Linux for PHP/Node. PHP remains native Linux PHP-FPM inside WSL, so Linux-only PHP extensions work.

## 2. Why this architecture

### Chosen

- Windows UX, file paths, install, services, trust, hosts, and ports are native.
- PHP and Node use Linux semantics and Linux runtimes.
- Linux-only PHP extensions are available without Docker PHP.
- Docker remains optional and isolated for services.
- The Linux product and Windows product share most Go code and the service/performance architecture.
- Project source can be on NTFS or WSL ext4, per site.

### Rejected alternatives

| Alternative | Why rejected |
|---|---|
| WSL-only devstack install | Windows hosts/trust/80/443/install integration would not work; browser `.test` experience requires manual Windows changes. |
| Native Windows PHP | PHP-FPM is not available on Windows; many Linux-only extensions would be unavailable; process model would differ substantially. |
| Docker PHP | Not needed because WSL already runs Linux PHP-FPM; adds image pipeline, per-site container complexity, and Docker networking/file-mount problems. |
| Docker Desktop as the supported substrate | Licensing, cross-VM networking, and bind-mount differences; dedicated Docker Engine in the managed WSL distro is more predictable. |
| Single shared `/data` physical root across Windows and WSL | SQLite/CA on 9P is unsafe; Linux/Docker data on NTFS is slow/unreliable. |
| Everything under `/usr/local/devstack` | Works technically but conflicts with Linux packaging/ownership conventions and doesn't solve Windows. Final choice is `/data/devstack` on Linux/WSL. |
| Dot-prefixed Windows paths | User requirement: no devstack-created Windows names starting with a dot. |
| Hard/symlink bridge from `C:\` to WSL | Hard links cannot cross filesystems or link directories; symlinks/junctions break tools, Docker, permissions, backups, and PhpStorm WSL mode. |
| Defaulting all projects to Windows folders | Much slower PHP/Node I/O on `/mnt/c`; unreliable watchers; Docker bind-mount issues. WSL is the PHP/Node default. |
| Auto-migrating existing Linux installs | Risk to CA, SQLite, runtimes, services, and projects; legacy installs are detected and preserved. |

## 3. Process responsibilities

### Windows daemon

- Unprivileged per-user process.
- REST API, embedded Svelte dashboard, CLI transport.
- SQLite source of truth at `C:\devstack\state.db`.
- Windows control-plane config `C:\devstack\config.yaml`.
- Logs, backups, leaf certificate metadata, route table.
- Builtin reverse proxy and public service mode.
- Agent client and helper client.
- Starts at user logon via per-user startup/scheduled task.
- Never loads PHP/Node and never reads the CA private key.

### Windows helper

- LocalSystem Windows service.
- Named pipe `\\.\pipe\devstack-helper`, ACL restricted to installing SID + SYSTEM + Administrators.
- SID peer verification.
- Local CA and leaf signing.
- Windows certificate store, Edge/Chrome inheritance, Firefox enterprise policy.
- Windows hosts file managed block.
- Windows firewall rules.
- Privileged 80/443 TCP forwarders.
- Install/repair/upgrade/uninstall primitives.
- Fixed operations only; no arbitrary paths, commands, or certificates from daemon.

### WSL agent

- systemd service in managed Debian 12 WSL2 distro.
- Bearer-token HTTP API on the WSL NAT interface.
- Health, version, capabilities, path info.
- Native Linux PHP-FPM, Node, static backends.
- Runtime install/validate/swap/rollback, FPM pool sizing.
- Logs, ports, terminal/PTY.
- Docker Engine/Compose control for the fixed service catalog.
- WSL `/etc/hosts` management.
- State/config under `/data/devstack`.
- Never receives the CA private key.

### Linux native

- Existing daemon/helper architecture.
- Same `/data/devstack` logical layout for new installs.
- Host Docker for services.
- No WSL agent.

## 4. Platform roots and path layouts

### Fixed roots

| Role | Linux/WSL | Windows |
|---|---|---|
| Application/data root | `/data/devstack` | `C:\devstack` |
| Default project root | `/data/devstack/projects` | `C:\devstack\projects` |

`C:\devstack` is fixed by user requirement. `/data/devstack` is fixed for new Linux installs; internal overrides remain for tests and legacy installs.

### Windows layout

```text
C:\devstack\
├── bin\
│   ├── devstack.exe
│   └── devstack-ctl.exe
├── config.yaml
├── state.db
├── pki\                              # leaf certs
├── sites\
├── services\
├── projects\
├── logs\
├── backups\
├── cache\
│   └── debian-12-rootfs.tar.gz
├── run\
├── wsl\
│   └── devstack\
│       └── ext4.vhdx
└── system\                           # SYSTEM/Administrators only
    ├── bin\
    │   └── devstack-helper.exe
    ├── helper.yaml
    └── pki\
        ├── rootCA.key
        └── rootCA.pem
```

Rules:

- No devstack-created dot-prefixed names on Windows.
- Project-owned files such as `.git`, `.idea`, `.env`, `.htaccess` are unaffected.
- `C:\devstack` is created by an elevated installer with explicit ACLs.
- User has normal access to the user-facing tree.
- `C:\devstack\system` is protected; user cannot write/delete the helper binary or CA key.
- SQLite never moves to `/mnt/c` or `\\wsl.localhost`.
- The VHDX is not safe to copy while WSL is running.

### Linux layout

```text
/data/devstack/
├── bin\
├── config.yaml
├── state.db
├── pki\
├── runtimes\
├── sites\
├── services\
├── vhosts\
├── tls\
├── projects\
├── logs\
├── backups\
├── cache\
├── run\
├── secrets\
├── certs\
├── tools\
├── docker\              # WSL dedicated Docker data-root; Linux shared Docker exception
└── system\              # Linux helper only
    ├── helper.yaml
    └── pki\
        ├── rootCA.key
        └── rootCA.pem
```

Linux compatibility:

- Existing `~/.devstack`, `/etc/devstack/helper.yaml`, and `/var/lib/devstack-helper` installs are detected and preserved.
- No automatic migration.
- New Linux installs use `/data/devstack` and helper `system` layout.
- Native Linux Docker remains `/var/lib/docker` because it is a shared host engine; devstack service data still lives under `/data/devstack/services/<id>/data`.

### WSL layout

```text
/data/devstack/
├── bin\
│   └── devstack-agent
├── agent.yaml
├── agent.token                       # 0600
├── managed.json                      # managed_by=devstack, install id
├── runtimes\
├── sites\
├── services\
├── vhosts\
├── tls\
├── projects\
├── logs\
├── backups\
├── cache\
├── run\
└── docker\                            # Docker data-root
```

Windows sees WSL content through:

```text
\\wsl.localhost\devstack\data\devstack\...
```

The Windows daemon never uses this UNC path for SQLite, CA, or Docker data.

## 5. Project source modes

Per-site `source_mode`:

- `local`: existing Linux native source.
- `wsl`: source inside WSL ext4; default for PHP and Node on Windows.
- `windows`: source on NTFS; default for static sites on Windows; supported for all kinds with warnings.

Path translation:

- Windows: `C:\...` ↔ `/mnt/c/...`
- WSL: `\\wsl.localhost\devstack\...` ↔ `/...`
- Reject arbitrary UNC/device paths and cross-boundary symlinks/junctions.

### Windows-mode caveats

- WSL reaches source through `/mnt/c` (9P).
- Slower metadata-heavy operations; PHP cold starts, Composer, PHPUnit, framework boots, Git, npm installs, and watchers are affected.
- OPcache hides some steady-state cost; cold and CLI workloads remain much slower.
- Windows Defender can worsen it; exclusions are optional and a security trade-off.
- Docker bind mounts from `/mnt/c` are slower/less reliable.
- NTFS case-insensitive/reserved-name behavior applies.
- Executable bits and symlinks differ.
- Use WSL PHP interpreter in PhpStorm; line endings need `.gitattributes`.

### WSL-mode caveats

- Source lives in ext4 VHDX, exposed via `\\wsl.localhost`.
- Best PHP/Node/FPM/Docker performance and Linux permissions.
- PhpStorm 2026.2+ Native WSL mode is recommended.
- Older PhpStorm can use Remote Development/Gateway or direct UNC with 9P performance.
- Windows tools that do not handle UNC paths can be slow/limited.
- Git should run in WSL; PhpStorm normally selects WSL Git.
- Backups must include the VHDX or an exported project archive.

### Source mode switching

- Explicit copy-and-verify operation, not rebind.
- Site must be stopped.
- Destination absent/empty.
- Copy to temporary sibling; verify count/checksums/permissions; switch site row.
- Keep source by default; `--remove-source` only after verification.
- Failure leaves original row/source untouched.
- No automatic overwrite/merge in v1.

## 6. PhpStorm and development tools

PhpStorm can open WSL projects:

- PhpStorm 2026.2+ opens `\\wsl.localhost\...` in Native WSL mode.
- Configure PHP CLI interpreter via WSL, selecting the `devstack` distro and the installed PHP binary.
- Composer/PHPUnit/quality tools run inside WSL.
- Older PhpStorm can use Remote Development or direct UNC with performance caveats.
- Xdebug from WSL to PhpStorm on Windows needs host/path mapping configuration; document as setup, not automatic.
- Windows-mode projects open normally and can use a WSL PHP interpreter, but `/mnt/c` performance/watchers are worse.
- Use VS Code Remote WSL or WSL Git for best WSL-mode behavior.

## 7. PHP runtime and extensions

- Windows PHP sites use native Linux PHP-FPM inside WSL.
- Linux-only extensions are available because the runtime is Linux.
- Existing Linux PHP builds are the basis, but every artifact must pass a Debian 12 WSL compatibility gate:
  - `php -v`
  - FPM startup
  - supported extension load
  - required system libraries present
- If an existing build fails, the release must publish a WSL-compatible variant and catalog entry.
- Per-site PHP version/build/ini/extensions and FPM pool sizing remain.
- No Docker PHP.
- No native Windows PHP.
- CA public bundle for WSL curl/Node/PHP is copied to the agent runtime.

## 8. Docker and services

- Docker Engine runs inside the managed WSL distro.
- Data-root: `/data/devstack/docker`.
- No Docker Desktop requirement.
- Service catalog remains nginx/Apache, MySQL/MariaDB, PostgreSQL, Redis, phpMyAdmin.
- Docker PHP is excluded.
- Web containers use host networking inside WSL so they reach native PHP-FPM/Node on WSL loopback.
- Service data lives under `/data/devstack/services/<id>/data` on ext4.
- Configs under `/data/devstack/services/<id>/config`.
- Database/Redis data never on NTFS.
- Credentials/rotation/phpMyAdmin preserve current behavior.
- Windows access to WSL service ports:
  - Agent exposes a fixed loopback-service relay on the WSL NAT interface for enabled services (DB/Redis/phpMyAdmin).
  - Windows helper forwards selected Windows loopback ports to the agent relay with loopback-only firewall rules.
  - Windows clients use `127.0.0.1:<port>`; no LAN exposure.
  - No arbitrary ports/services allowed.

## 9. Networking and public ports

- Windows helper owns Windows 80/443.
- Linux helper owns Linux 80/443.
- Builtin mode:
  - Windows: helper → Windows daemon 8080/8443 → WSL PHP/Node/static.
  - Linux: helper → Linux daemon → native backends.
- Docker web-server mode:
  - Web containers use host networking in WSL.
  - Windows helper forwards 80/443 to nginx/Apache private ports inside WSL.
  - Daemon/agent updates helper targets atomically on port/distro IP changes.
- WSL agent:
  - NAT interface only.
  - Bearer token on every request.
  - Windows daemon tries localhost forwarding first, then distro IP.
  - Mirrored networking is rejected.
- Service relay allows Windows 127.0.0.1 access to WSL-only services.
- Public ports never point directly at WSL service loopback.

## 10. Windows install, WSL lifecycle, upgrade, uninstall

### WSL baseline

- Windows 11 22H2+ x64.
- WSL 2.0.0+ with systemd support.
- Installer validates `wsl.exe --version`, WSL2, virtualization, and systemd.
- It does **not** enable Windows optional features, install WSL, or reboot.
- Missing prerequisites produce exact `wsl --install`/`wsl --update` instructions and exit.
- Import uses `--version 2`; default distro is not changed.
- Managed distro can be created at `C:\devstack\wsl\devstack`.

### Managed distro identity

- Marker `/data/devstack/managed.json` with `managed_by=devstack` and install ID.
- A pre-existing `devstack` distro without the marker is refused; never adopted/replaced.
- Error tells user to rename/remove it or select another distro name.
- Uninstall leaves WSL distro/projects by default.
- `--remove-wsl` requires verified project export or explicit `--discard-wsl-projects`.
- No Windows project is deleted.

### Installer

- `install.ps1` requires elevation for root/ACL/helper service/firewall.
- Creates `C:\devstack`, `system` ACLs, helper service, daemon/CLI, WSL import, Docker Engine, per-user startup.
- Runs `devstack init`.
- Unsigned; SmartScreen documented.
- Windows binaries/artifacts are not yet produced.

### Upgrade

- Preserve `config.yaml`, `state.db`, `pki`, `projects`, `services`, `system`, and VHDX.
- Replace binaries/rootfs/agent.
- WSL distro export is the backup unit for runtime state/Docker data.
- Running VHDX must not be file-copied.

### Uninstall

- Stop daemon/agent/containers.
- Back up SQLite/config if requested.
- Remove helper service/firewall/hosts/trust.
- Preserve Windows projects and WSL distro by default.
- Optional WSL removal only with export or explicit discard.
- Never delete projects by default.

## 11. Security boundaries

- Windows pipe ACL: installing SID + SYSTEM + Administrators.
- SID checked on every request.
- CA private key only in Windows/Linux protected `system` subtree.
- WSL agent token:
  - Windows daemon: DPAPI-backed user protection.
  - Agent: `/data/devstack/agent.token` 0600.
- Agent only on WSL NAT; no public interface.
- No Docker socket exposed to Windows users; agent controls dedicated engine.
- No arbitrary commands/paths/certificates.
- No cross-boundary symlinks/junctions.
- Hosts/trust/CA only through helper.
- SQLite/CA never on 9P/NTFS bind paths.
- Service data never on NTFS.
- Docker web/data containers do not receive CA private key.
- Docker PHP excluded.

## 12. Data model and migrations

- `model.Site.SourceMode`: `local`, `windows`, `wsl`.
- Migration `0007_site_source_mode.sql`: `source_mode TEXT NOT NULL DEFAULT 'local'`.
- Existing Linux sites become `local`.
- Store round-trip includes source mode.
- Config `projects_root`, WSL distro, agent metadata.
- No dot-prefixed Windows paths.

## 13. Testing and performance facts

- Five lanes can implement in parallel with isolated worktrees, fakes, unique test roots/distros/ports.
- Real Windows machine-wide resources are serialized by the controller:
  - helper service/pipe
  - production `C:\devstack`
  - managed `devstack` distro
  - 80/443 owner
  - hosts/trust mutations
  - Docker engine
- Windows-tagged tests need a Windows host; Linux regression runs in WSL/Linux.
- WSL ext4 is required for Docker/PHP data; `/mnt/c` is 9P.
- Published WSL benchmarks: `/mnt/c` cross-filesystem operations are often 3–25× slower for metadata-heavy work; `git status`/npm can be 10–50× slower. Warm OPcache narrows PHP steady-state, but cold/CLI workloads remain much slower.
- Database/Redis data must not be on NTFS.
- Host tuning is disabled on Windows; no sysctl/limits equivalent.

## 14. Plan execution summary

- **Wave 0 (sequential):** repo hygiene, IPC, roots, source mode, agent protocol/token, WSL runner, daemon transport, platform capabilities, foundation freeze.
- **Wave 1 (five parallel lanes):**
  1. Windows helper
  2. WSL provisioning/agent
  3. Windows daemon/public proxy
  4. Docker services
  5. Site source/UI/CLI
- **Integration 1:** merge, static-site vertical slice from Windows and WSL paths.
- **Wave 2 (five parallel lanes):** PHP/Node/terminal, installers/lifecycle, Docker parity, performance/config/UI, packaging/acceptance.
- **Integration 2:** merge, full Windows acceptance, Linux regression, final review.
- Max **5 active subagents**; TDD; isolated worktrees; fresh reviewers; controller-only merge; focused tests after each merge.
- Plan document has exact task briefs and dispatch templates.

## 15. Builder-check questions and resolutions

The independent builder check raised eight gaps. All are resolved in the design/plan:

1. WSL baseline/installer scope → Windows 11 22H2+, WSL 2.0.0+ systemd; installer validates, never enables/reboots.
2. Five-lane machine resources → lanes use isolated test roots/fakes; real helper/distro/ports/trust/Docker tests are serialized.
3. Windows reach to WSL service loopback → agent service relay + helper loopback forwarding.
4. WSL projects on uninstall → distro/projects preserved by default; removal requires verified export/discard.
5. Existing `devstack` distro → managed marker required; unmarked distro refused.
6. Linux PHP builds on Debian 12 → compatibility gate and WSL-variant fallback.
7. `.test` inside WSL → agent-managed `/etc/hosts`, `generateHosts=false`.
8. Source mode switching → stopped-site copy-and-verify, keep source unless explicit remove.

## 16. Open questions / risks

These are not blockers for Wave 0, but must be resolved during the owning lane:

- Exact pinned WSL rootfs package set and Docker Engine/Compose versions.
- Whether all existing PHP catalog artifacts pass Debian 12; if not, which variants are published.
- Exact Windows loopback port defaults for DB/Redis/phpMyAdmin and conflict handling.
- WSL 2.0.0 test coverage across older Windows patch levels; a higher minimum may be required.
- PhpStorm version coverage for Native WSL mode and Xdebug path mapping.
- Windows unsigned/SmartScreen UX and code-signing timeline.
- Installer behavior when another service owns 80/443.
- WSL VHDX backup/restore UX and disk compaction.
- Final Windows acceptance hardware matrix.
- Existing Linux helper path migration UX (new installs vs legacy).
- Docker service health/metrics parity on Windows host.

## 17. What must not be claimed yet

- Windows support is not implemented.
- No Windows artifact, installer, agent, named-pipe helper, or release exists.
- No Windows/WSL acceptance has run.
- The Linux full gate remains the only verified platform.
- Do not start implementation without reviewing the plan and beginning Wave 0.

## 18. Source-of-truth documents

- [Design](../specs/2026-10-26-windows-wsl-design.md)
- [Implementation plan](../plans/2026-10-26-windows-wsl.md)
- [Planning checkpoint](2026-10-26-windows-planning.md)
- [Linux handoff](../../handoff.md)
- [Known issues](../../known-issues.md)
