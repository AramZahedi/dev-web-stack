# Windows 11 + WSL2 Knowledge Handoff

- **Date:** 2026-10-26
- **Revised:** 2026-10-27 after the in-depth plan review
- **Status:** architecture and implementation plan revised; implementation not started
- **Current Linux baseline:** `10db2e5`
- **Current `master`:** the commit containing this file; run `git rev-parse HEAD`
- **Design:** [Windows 11 + WSL2 design](../specs/2026-10-26-windows-wsl-design.md)
- **Plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Review resolution:** [Windows plan review resolution](2026-10-27-windows-plan-review-resolution.md)
- **Planning checkpoint:** [Windows planning checkpoint](2026-10-26-windows-planning.md)
- **Windows startup guide:** [Next agent on Windows](2026-10-26-windows-next-agent.md)

This document is the consolidated memory of the Windows/WSL design discussion.
The 2026-10-27 revision resolves the network, storage, backend, path, protocol,
and tooling gaps found in the first planning pass. It records the decisions,
rejected alternatives, caveats, unresolved risks, and the exact execution
model so work can resume without the original conversation.

## 1. Product direction

The user wants first-class Windows support, not merely running the Linux product
inside a WSL shell.

The chosen architecture is:

- **Native Windows daemon** for the UI/API, SQLite, public proxy, routing, and
  orchestration.
- **Native Windows helper** running as LocalSystem for Windows hosts, trust
  stores, CA, firewall, and 80/443.
- **Managed Debian 12 WSL2 agent** for Linux PHP-FPM, Node, static backends,
  logs, ports, the WSL `.test` ingress, and Docker services.
- **Docker only for infrastructure services**: nginx/Apache, MySQL/MariaDB,
  PostgreSQL, Redis, phpMyAdmin.
- **No Docker PHP.**
- **No native Windows PHP.**
- **No Docker Desktop requirement.** Docker Engine runs inside the managed WSL
  distro.
- Linux continues to use the existing native daemon/helper pair without an
  agent.

The reason for native Windows daemon/helper plus a WSL agent is to keep Windows
OS integration native while using Linux for PHP/Node. PHP remains native Linux
PHP-FPM inside WSL, so Linux-only PHP extensions work.

## 2. Why this architecture

### Chosen

- Windows UX, file paths, install, services, trust, hosts, and ports are
  native.
- PHP and Node use Linux semantics and Linux runtimes.
- Linux-only PHP extensions are available without Docker PHP.
- Docker remains optional and isolated for services.
- The Linux product and Windows product share most Go code and the
  service/performance architecture.
- Project source can be on NTFS or WSL ext4, per site.
- Windows always owns public 80/443, so browser trust and port ownership stay
  predictable.

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
| Helper forwarding directly to WSL nginx/Apache | Would expose a dynamic non-loopback target through a root forwarder and strand the dashboard/API. The daemon is the single public router instead. |
| WSL mirrored networking | Port-conflict and Docker behavior are less predictable; the design uses NAT plus an explicit ingress. |

## 3. Process responsibilities

### Windows daemon

- Unprivileged per-user process.
- REST API, embedded Svelte dashboard, CLI named-pipe transport.
- SQLite source of truth at `C:\devstack\state.db`.
- Windows control-plane config `C:\devstack\config.yaml`.
- Daemon logs, backups, leaf certificate metadata, route table.
- Public builtin HTTP/HTTPS proxy and dashboard.
- Agent client and helper client.
- Starts at user logon via a limited-privilege per-user task.
- Never loads PHP/Node, never runs a site supervisor, and never reads the CA
  private key.

### Windows helper

- LocalSystem Windows service.
- Named pipe `\\.\pipe\devstack-helper`, ACL restricted to the installing SID
  + SYSTEM + Administrators.
- SID peer verification.
- Local CA and leaf signing.
- Windows certificate store, Edge/Chrome inheritance, Firefox enterprise
  policy.
- Windows hosts file managed block.
- Windows firewall rules.
- Privileged 80/443 TCP forwarders, always targeting the Windows daemon proxy.
- Install/repair/upgrade/uninstall primitives.
- Only CA/trust/hosts/firewall/forwarder/health operations. No service, tool,
  vhost, Docker, or host-performance RPCs.

### WSL agent

- systemd service in the managed Debian 12 WSL2 distro, running as root.
- HTTPS API with a pinned self-signed certificate and bearer token.
- Dual listeners: loopback for WSL localhost forwarding and the distro NAT IP
  for control fallback.
- Health, version, capabilities, path information.
- Native Linux PHP-FPM, Node, and static backends.
- WSL port allocation/validation.
- Runtime install/validate/swap/rollback, per-site FPM configuration.
- WSL `.test` ingress on 80/443 to the Windows daemon proxy.
- Logs, backups, terminal/PTY.
- Docker Engine and Compose control for the fixed service catalog.
- WSL `/etc/hosts` management.
- State/config under `/data/devstack`.
- Never receives the CA private key.

### Linux native

- Existing daemon + helper architecture.
- Same `/data/devstack` logical layout for new installs.
- Host Docker for services.
- No agent and no WSL dependency.
- Existing `~/.devstack` and legacy helper installs are preserved.

## 4. Platform roots and path layouts

### Fixed roots

| Role | Linux / WSL | Windows |
|---|---|---|
| Application/data root | `/data/devstack` | `C:\devstack` |
| Default project root | `/data/devstack/projects` | `C:\devstack\projects` |

`C:\devstack` is fixed by user requirement. `/data/devstack` is fixed for new
Linux installs; `--home` and `DEVSTACK_HOME` remain supported for tests, CI,
and legacy installs.

### Root precedence

1. Explicit `--home`.
2. `DEVSTACK_HOME`.
3. A detected legacy `${HOME}/.devstack` (`config.yaml` or `state.db` exists).
4. Platform default: `/data/devstack` on Linux, `C:\devstack` on Windows.

A root that exists but is not writable fails with remediation rather than
silently falling back.

### Windows layout

```text
C:\devstack\
├── bin\
│   ├── devstack.exe
│   ├── devstack-ctl.exe              # optional CLI helper
│   ├── php.cmd                       # managed WSL shim
│   └── composer.cmd                  # managed WSL shim
├── config.yaml
├── state.db
├── pki\                              # canonical leaf certificates
├── projects\                         # Windows-mode project source
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

There are no canonical Windows `sites\`, `services\`, `secrets\`, `certs\`, or
`tools\` trees. Generated site/service data, service secrets, runtimes, and
Docker data live in WSL. `state.db` holds metadata; the daemon proxies
content/credential/log operations to the agent.

Rules:

- No devstack-created dot-prefixed names on Windows.
- Project-owned `.git`, `.idea`, `.env`, `.htaccess` are unaffected.
- `C:\devstack` is created by an elevated installer with explicit ACLs.
- The user has normal access to the user-facing tree.
- `C:\devstack\system` is protected; the user cannot write, delete, or change
  ownership of the helper binary or CA key.
- SQLite never moves to `/mnt/c` or `\\wsl.localhost`.
- The VHDX is not safe to copy while WSL is running.

### Native Linux layout

```text
/data/devstack/                       # root:root 0755
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
├── secrets\                           # 0700
├── certs\
├── tools\
└── system\                            # root:root 0750, helper only
    ├── helper.yaml
    └── pki\
        ├── rootCA.key
        └── rootCA.pem
```

- New installs use the `/data/devstack` layout.
- Existing `~/.devstack`, `/etc/devstack/helper.yaml`, and
  `/var/lib/devstack-helper` installs are detected and preserved.
- The helper binary remains root-owned at
  `/usr/local/lib/devstack/devstack-helper` (not in the user-writable tree).
- Native Linux Docker data remains `/var/lib/docker`; devstack service data
  still lives under `/data/devstack/services`.
- The systemd helper unit and the `install.sh` embedded unit must be updated
  together.

### WSL layout

```text
/data/devstack/                       # root:root 0755
├── bin\
│   └── devstack-agent
├── agent.yaml
├── agent.token                       # 0600
├── agent-tls\                        # 0700, root-only
│   ├── agent.key
│   └── agent.crt
├── managed.json                      # managed_by=devstack, install id
├── runtimes\
├── sites\
├── services\
├── vhosts\
├── tls\                              # leaf copies, 0700/0600
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

The Windows daemon never uses this UNC path for SQLite, the CA, or Docker data.
It may use it for project-file checks and PHP static-file resolution after
agent-side path validation.

### Project source modes

Per-site `source_mode`:

- `local`: existing Linux native source.
- `wsl`: project source inside WSL ext4; default for PHP and Node on Windows.
- `windows`: project source on NTFS; default for static sites on Windows;
  supported for all kinds with warnings.

Path contract:

- Windows: `C:\...` ↔ `/mnt/c/...`
- WSL: `\\wsl.localhost\<distro>\...` ↔ `/...`
- `host_path` is the Windows-visible path; `backend_path` is the WSL path.
- `proxy_document_root` is the Windows-visible absolute document root used by
  the Go proxy for PHP file checks.

Rules:

- Accept absolute drive-letter paths and managed
  `\\wsl.localhost\<distro>\...` paths only.
- Reject arbitrary UNC/device paths, alternate data streams,
  symlinks/junctions that cross the boundary.
- Windows mode uses `/mnt/c`; show performance/watcher warnings.
- Source-mode switching is an explicit **agent-verified tar copy**:
  - site stopped;
  - destination absent/empty;
  - temporary sibling, SHA-256 manifest, file-count check;
  - Linux ownership/mode verification;
  - no escaping symlinks;
  - switch the row only after verification;
  - keep the source unless `--remove-source`;
  - failure leaves the original row/source untouched;
  - no automatic overwrite/merge in v1.

### Windows-mode caveats

- WSL reaches source through `/mnt/c` (9P).
- Slower metadata-heavy operations; PHP cold starts, Composer, PHPUnit,
  framework boots, Git, npm installs, and watchers are affected.
- OPcache hides some steady-state cost; cold and CLI workloads remain much
  slower.
- Windows Defender can worsen it; exclusions are optional and a security
  trade-off.
- Docker bind mounts from `/mnt/c` are slower/less reliable.
- NTFS case-insensitive/reserved-name behavior applies.
- Executable bits and symlinks differ.
- Use WSL PHP interpreter in PhpStorm; line endings need `.gitattributes`.

### WSL-mode caveats

- Source lives in ext4 VHDX, exposed via `\\wsl.localhost`.
- Best PHP/Node/FPM/Docker performance and Linux permissions.
- PhpStorm 2026.2+ Native WSL mode is recommended.
- Older PhpStorm can use Remote Development/Gateway or direct UNC with 9P
  performance.
- Windows tools that do not handle UNC paths can be slow/limited.
- Git should run in WSL; PhpStorm normally selects WSL Git.
- Backups must include the VHDX or an exported project archive.

## 5. Networking and public ports

### Windows public entry

```text
Windows browser
  -> Windows helper 80/443
  -> Windows daemon proxy 8080/8443
  -> builtin WSL backend 127.0.0.1:<agent-port>
     or active Docker web server 127.0.0.1:<private-port>
     or dashboard 127.0.0.1:<ui-port>
```

- The helper never forwards directly to WSL.
- The daemon terminates public TLS with the native CA certificate.
- The dashboard is a daemon route; Docker containers do not own
  `devstack.test`.
- The daemon proxy binds loopback plus the WSL host-adapter address; the
  firewall restricts that listener to the WSL subnet.
- The API/control listener remains on Windows loopback only.

### WSL ingress

```text
WSL client
  -> WSL /etc/hosts: <site>.test -> 127.0.0.1
  -> agent ingress 80/443
  -> Windows host gateway:8080/8443
  -> Windows daemon proxy
  -> same routes as Windows
```

- The agent ingress is a fixed, root-owned TCP forwarder.
- The target is discovered from the WSL default route or configured in
  `agent.yaml`; it is never caller-supplied.
- The ingress restarts when the host gateway address changes.
- This makes `curl https://<site>.test` and `curl https://devstack.test` work
  inside WSL in both builtin and Docker modes.

### Windows→WSL backend traffic

- WSL localhost forwarding is required for Windows to reach agent/site backend
  ports bound to `127.0.0.1` inside WSL.
- The agent API additionally listens on the distro IP for control fallback.
- The Windows daemon tries `127.0.0.1` first, then the distro IP, for agent
  control requests.
- Site backends bind `127.0.0.1`; the daemon uses the returned
  `PreparedSite.BackendHost` and ports.
- The doctor checks localhost forwarding and reports
  `WSL_LOCALHOST_FORWARDING_UNAVAILABLE` with remediation if disabled. Serving
  fails closed rather than silently trying an unauthenticated relay.

### Docker web servers

- Docker Engine runs inside the managed WSL distro.
- Data-root: `/data/devstack/docker`.
- Web-server containers use host networking so they can reach native
  PHP-FPM/Node on WSL loopback, but listen on a **private HTTP port** (default
  18080). They do not bind public 80/443 on Windows.
- The Windows daemon proxies public site traffic to the active web server's
  private port.
- The dashboard remains a daemon route. There is no container dashboard vhost
  on Windows.
- Only one web server is active at a time. Linux keeps its existing direct
  public Docker provider.

### Agent transport

- HTTPS only, with a self-signed agent certificate generated during
  provisioning.
- The Windows daemon pins the certificate SHA-256 fingerprint and stores it
  alongside the bearer token in DPAPI.
- Every request carries `Authorization: Bearer <token>` and the protocol
  version header.
- The agent listens on loopback and the WSL NAT interface; mirrored networking
  is rejected.
- Token and TLS private key never appear in process arguments.
- The token is `/data/devstack/agent.token` (0600); TLS material is
  `/data/devstack/agent-tls` (0700/0600, root-only).

### Service relay

- The agent exposes a fixed loopback-service relay on the WSL NAT interface for
  enabled services (MySQL/MariaDB, PostgreSQL, Redis, phpMyAdmin).
- The Windows helper forwards selected Windows loopback ports to the agent
  relay with loopback-only firewall rules.
- Windows clients use `127.0.0.1:<port>`; nothing is exposed to the LAN.
- The relay accepts only catalog service IDs/ports.

## 6. PHP, Node, static, terminal, performance

### PHP

- Windows PHP sites use native Linux PHP-FPM inside WSL.
- No Docker PHP and no native Windows PHP.
- Existing Linux PHP builds are the basis, but every artifact must pass a
  Debian 12 WSL compatibility gate:
  - `php -v`
  - FPM startup
  - supported extension load
  - required system libraries present
- If an existing build fails, the release must publish a WSL-compatible
  variant and catalog entry.
- Per-site PHP version/build/ini/extensions and FPM pool sizing remain.
- The agent owns runtime install/select/validate/swap and generates per-site
  PHP/FPM configuration in WSL.
- The Windows daemon sends the site spec and persists the returned
  generation/ports.
- Windows-mode PHP source is read through `/mnt/c` with warnings.
- `devstack php --site ... -- ...` and Composer run in WSL.

### Node and static

- Native Linux Node inside WSL/Linux.
- Static and Node backends run in the WSL agent for Windows sites.
- Windows-mode static source is read through `/mnt/c` with performance
  warnings.
- Static builds run in WSL with the selected Node runtime.
- Node HMR WebSockets traverse the Windows daemon proxy and/or WSL ingress.

### Terminal

- Site terminal runs through the WSL agent PTY.
- The Windows daemon proxies WebSocket/HTTP; no Windows ConPTY requirement for
  site shells.
- Same ticket, origin, UID/site binding, size, and teardown rules as Linux.
- Sessions run as the WSL site user, never root.

### Performance

- Web/PHP profiles and benchmark history remain.
- Host tuning is disabled on Windows; the helper rejects host-tuning RPCs.
- Windows resource collection reports Windows CPU/memory; the agent reports
  distro resources.
- FPM pool settings apply inside WSL.
- Benchmarks run from Windows through the selected public route.

### Tooling, shells, builds

- Global `php` and `composer` are WSL-managed tools.
- Windows gets `C:\devstack\bin\php.cmd` and `composer.cmd` shims that invoke
  the managed distro.
- `devstack shell` and `devstack exec` translate the site path and launch
  `wsl.exe -d <distro> -- bash -lc ...` with the site environment.
- `devstack shell-init` remains POSIX shell output for WSL shells; Windows
  users use `devstack shell`/`exec` or the `.cmd` shims.
- Site builds run in WSL via the agent.
- Dashboard tooling/build/terminal actions route to the agent.

## 7. Docker and services

- Docker Engine runs inside the managed WSL distro.
- Data-root: `/data/devstack/docker`.
- No Docker Desktop requirement.
- Service catalog remains nginx/Apache, MySQL/MariaDB, PostgreSQL, Redis,
  phpMyAdmin.
- Docker PHP is excluded.
- Web containers use host networking inside WSL so they reach native
  PHP-FPM/Node on WSL loopback, but listen on private HTTP ports.
- Service config/data/secrets live under `/data/devstack/services` on ext4.
- Database/Redis data never on NTFS.
- Credentials/rotation/phpMyAdmin preserve current behavior, with the daemon
  proxying reveal/rotate to the agent.
- Linux Docker keeps its existing direct public 80/443 provider.
- Windows Docker web serving is private-port, daemon-fronted, and
  HTTP-only inside WSL; public TLS is the daemon's responsibility.

## 8. Windows installation, WSL lifecycle, upgrade, uninstall

### WSL baseline

- Windows 11 22H2+ x64.
- WSL 2.0.0+ with systemd support.
- Installer validates `wsl.exe --version`, WSL2, virtualization, systemd
  support, localhost forwarding, and disk space.
- It does **not** enable Windows optional features, install WSL, or reboot.
- Missing prerequisites produce exact `wsl --install`/`wsl --update`
  instructions and exit.

### Install phases

1. **Elevated system phase (`install.ps1`):**
   - create `C:\devstack` and protected `system` ACLs;
   - install daemon/CLI binaries and helper service;
   - write helper config;
   - create firewall rules and the per-user logon task;
   - add `C:\devstack\bin` to the user PATH.
2. **User phase (`devstack init`):**
   - import/update the managed WSL distro at `C:\devstack\wsl`;
   - install/verify the agent and Docker Engine;
   - generate/rotate the agent token and TLS identity; store the DPAPI copy;
   - write the WSL agent config, hosts block, and CA bundle;
   - start helper/daemon/agent and run diagnostics.

`install.ps1` requires elevation for the system phase. The user phase runs as
the installing user. If the installer was launched as a different
administrator account, it must hand the user phase to the installing user
rather than importing the distro under the wrong account.

### Managed distro identity

- Marker `/data/devstack/managed.json` with `managed_by=devstack` and an
  install ID.
- A pre-existing `devstack` distro without the marker is refused; never
  adopted/replaced.
- The managed distro can be created at `C:\devstack\wsl\devstack`.
- Import uses `--version 2`; the default distro is not changed.
- `wsl --shutdown` is required for consistent VHDX backup/export.

### Artifact supply chain

- Pin the Debian 12 rootfs URL/SHA-256, Docker Engine/Compose versions, agent
  binary, and PHP/Node artifacts in `build/wsl/versions.lock`.
- Release builds/publishes the rootfs, agent, Windows archive, checksums, and
  SBOMs.
- The installer downloads over HTTPS and verifies checksums before import.
- A release test imports the rootfs, starts the agent, runs Docker
  `hello-world`/Compose, and verifies the agent TLS/token path.

### Upgrade

- Preserve `config.yaml`, `state.db`, `pki`, `projects`, `services`, `system`,
  and the VHDX.
- Replace binaries and the agent.
- For rootfs updates, export the old distro as a backup, import the new pinned
  rootfs into a temporary distro, restore `/data/devstack` data as the site
  user, verify, swap, and roll back on failure. Do not copy a running VHDX.
- Run `devstack doctor` and the Windows acceptance subset after upgrade.

### Uninstall

- Stop daemon/agent/containers.
- Back up SQLite/config if requested.
- Remove helper service, firewall rules, hosts block, and trust entries.
- Preserve the WSL distro and WSL-mode projects by default.
- `--remove-wsl` requires either `--export-wsl-projects <path>` with a verified
  archive or explicit `--discard-wsl-projects` confirmation.
- Never delete Windows projects by default. State/config removal requires an
  explicit purge flag.
- Document what remains in `C:\devstack` and what the user must delete
  manually.

## 9. Security boundaries

- Windows helper pipe ACL: installing SID + SYSTEM + Administrators.
- Windows daemon control pipe ACL: installing SID + SYSTEM.
- SID checked on every request; the API socket-auth bypass is applied only
  after verification.
- CA private key only in Windows/Linux protected `system` subtree.
- Agent token:
  - Windows daemon: DPAPI-backed user protection.
  - Agent: `/data/devstack/agent.token` 0600.
- Agent TLS:
  - self-signed certificate generated during provisioning;
  - private key in `/data/devstack/agent-tls` 0600/0700, root-only;
  - Windows daemon pins the SHA-256 fingerprint.
- Agent only on WSL NAT/loopback; no public interface.
- No Docker socket exposed to Windows users; agent controls the dedicated
  engine.
- No arbitrary commands/paths/certificates from the daemon to the helper.
- WSL path validation/copy is agent-side; Windows UNC checks are never the sole
  authorization.
- No cross-boundary symlinks/junctions.
- Hosts/trust/CA only through the helper.
- SQLite/CA never on 9P/NTFS bind paths.
- Service data never on NTFS.
- Windows Docker web containers do not receive the CA private key and do not
  own public 80/443.
- The local dashboard/API is single-user. WSL site code is user code and is
  inside the local trust boundary; do not treat it as a multi-tenant boundary.
- No devstack-created Windows basename starts with a dot.

## 10. Data model and migrations

- `model.Site.SourceMode`: `local`, `windows`, `wsl`.
- Migration `0007_site_source_mode.sql`: `source_mode TEXT NOT NULL DEFAULT
  'local'`.
- Existing Linux sites become `local`.
- Store round-trip includes source mode and the host/backend path contract.
- Config additions: `projects_root`, `wsl_distro`, agent metadata.
- Windows `state.db` is the only SQLite database; WSL has no SQLite.
- Service/site canonical data lives in WSL ext4; Windows stores metadata.
- No dot-prefixed Windows paths.

## 11. Testing and performance facts

- Five lanes can implement in parallel with isolated worktrees, fakes, unique
  test roots/distros/ports.
- Real Windows machine-wide resources are serialized by the controller:
  - helper service/pipe
  - production `C:\devstack`
  - managed `devstack` distro
  - 80/443 owner
  - hosts/trust/firewall mutations
  - WSL ingress/firewall
  - Docker engine
- Windows-tagged tests need a Windows host; Linux regression runs in WSL ext4
  or a Linux checkout.
- WSL ext4 is required for Docker/PHP data; `/mnt/c` is 9P.
- Published WSL benchmarks: `/mnt/c` cross-filesystem operations are often
  3–25× slower for metadata-heavy work; `git status`/npm can be 10–50× slower.
  Warm OPcache narrows PHP steady-state, but cold/CLI workloads remain much
  slower.
- Database/Redis data must not be on NTFS.
- Host tuning is disabled on Windows; no sysctl/limits equivalent.
- Localhost forwarding is required for Windows→WSL backend traffic; the doctor
  verifies it.
- `.test` access from WSL is tested in both builtin and Docker modes.

## 12. Plan execution summary

- **Wave 0 (sequential T0.1–T0.10):** toolchain/CRLF, IPC/pipe ACLs, roots/
  layout/no-dot policy, source mode/path contract, complete agent protocol/
  TLS, WSL runner/path/copy/forwarding, daemon transport/lifecycle, platform
  seams, backend/port/route/PHP-path contract, foundation freeze.
- **Wave 1 (five concurrent lanes, all start from the frozen foundation):**
  1. Windows helper
  2. WSL provisioning/agent/ingress
  3. Windows daemon/public proxy/WSL backend
  4. Docker services/private web adapter
  5. Site source/UI/CLI/doctor
- **Parallelism seam:** Lane D owns separate `internal/agentdocker`,
  `internal/agentrelay`, and `internal/agentweb` packages implementing the
  frozen `agentapi.Module` contract. It does not edit Lane B’s agent server.
  Lane C uses the fake agent/agentclient and does not wait for B.
- **Integration 1:** merge A→B→C→D→E, static-site vertical slice from Windows
  and WSL paths, WSL curl/dashboard, source-mode switch.
- **Wave 2 (five parallel parity lanes):**
  1. PHP/Node/FPM/terminal/build/tooling
  2. installers/lifecycle/rootfs/upgrade/uninstall
  3. Docker parity/private web
  4. performance/config/ports/UI
  5. packaging/acceptance/docs
- **Integration 2:** merge, full Windows acceptance, Linux regression, final
  review.
- Max **5 active subagents**; TDD; isolated worktrees; fresh reviewers;
  controller-only merge; focused tests after each merge.
- The A→B→C→D→E merge order is a module-mounting dependency, not a
  serialized development order.

## 13. Builder-check questions and resolutions

1. WSL baseline/installer scope → Windows 11 22H2+, WSL 2.0.0+ systemd;
   installer validates, never enables/reboots.
2. Five-lane machine resources → lanes use isolated test roots/fakes; real
   helper/distro/ports/trust/Docker/ingress tests are serialized.
3. Windows reach to WSL service loopback → agent service relay + helper
   loopback forwarding.
4. WSL projects on uninstall → distro/projects preserved by default; removal
   requires verified export/discard.
5. Existing `devstack` distro → managed marker required; unmarked distro
   refused.
6. Linux PHP builds on Debian 12 → compatibility gate and WSL-variant
   fallback.
7. `.test` inside WSL → agent-managed `/etc/hosts` with
   `generateHosts=false` and a fixed 80/443 ingress.
8. Source mode switching → stopped-site, agent-verified tar copy-and-verify;
   source kept unless explicit remove.
9. Network topology → Windows daemon is the single public router; WSL ingress
   forwards to it; Docker web servers use private HTTP ports.
10. Backend/ports/routes → agent owns WSL ports; daemon uses `BackendHost` and
    `ProxyDocumentRoot`.
11. Canonical storage → WSL ext4 for service/site data; Windows metadata only.
12. Agent protocol → complete typed protocol frozen in T0.5.
13. Security → pipe ACL/SID, pinned TLS + token, reduced helper surface.
14. Windows tooling → `.cmd` shims to WSL; no native PHP.
15. Linux layout → `/data/devstack` for new installs, legacy preserved,
    `v0.1.0` upgrade tested.

## 14. Open questions / risks

These are implementation-time risks, not architecture blockers:

- Exact pinned WSL rootfs package set and Docker Engine/Compose versions.
- Whether all existing PHP catalog artifacts pass Debian 12; if not, which
  variants are published.
- Exact Windows loopback port defaults for DB/Redis/phpMyAdmin and conflict
  handling.
- WSL 2.0.0 test coverage across older Windows patch levels; a higher minimum
  may be required.
- PhpStorm version coverage for Native WSL mode and Xdebug path mapping.
- Windows unsigned/SmartScreen UX and code-signing timeline.
- Installer behavior when another service owns 80/443.
- WSL VHDX backup/restore UX and disk compaction.
- Final Windows acceptance hardware matrix.
- Existing Linux helper path migration UX (new installs vs legacy).
- Docker service health/metrics parity on Windows host.
- Localhost forwarding availability across WSL configurations; doctor must
  fail clearly.
- Rootfs upgrade time/disk usage; export/reimport rollback.

## 15. What must not be claimed yet

- Windows support is not implemented.
- No Windows artifact, installer, agent, named-pipe helper, WSL ingress, or
  release exists.
- No Windows/WSL acceptance has run.
- The Linux full gate remains the only verified platform.
- Do not start implementation without reviewing the revised plan and beginning
  Wave 0 T0.1.
- Do not claim Docker web serving on Windows works until the private-port
  provider and dashboard route are tested.
- Do not claim global `php`/`composer` on Windows works until the `.cmd` shims
  and WSL environment are tested.
- Do not claim the `/data/devstack` Linux layout works until the installer,
  systemd unit, and `v0.1.0` upgrade test pass.

## 16. Source-of-truth documents

- [Design](../specs/2026-10-26-windows-wsl-design.md)
- [Implementation plan](../plans/2026-10-26-windows-wsl.md)
- [Review resolution](2026-10-27-windows-plan-review-resolution.md)
- [Planning checkpoint](2026-10-26-windows-planning.md)
- [Windows startup guide](2026-10-26-windows-next-agent.md)
- [Linux handoff](../../handoff.md)
- [Known issues](../../known-issues.md)

## 17. Revision history

- **2026-10-26:** Initial architecture/design/planning handoff.
- **2026-10-27:** Resolved the in-depth review findings: public-entry/WSL
  ingress, backend/port/route/PHP-path contracts, canonical WSL storage,
  agent TLS/token, helper/daemon pipe security, no-dot policy, Windows
  tooling, Linux `/data/devstack` compatibility, pinned rootfs/Docker
  artifacts, expanded acceptance matrix, and Wave 0 T0.1–T0.10.
