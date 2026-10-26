# Windows 11 + WSL2 Support Design

- **Date:** 2026-10-26
- **Status:** Approved for planning; implementation not started
- **Scope:** Windows 11 22H2+ x86_64, managed Debian 12 WSL2 distro, native Windows daemon/helper, native Linux PHP/Node inside WSL, Docker service containers
- **Supersedes:** the Windows/WSL sections of the 2026-10-22 M1 design. That document remains historical for the released Linux product.
- **Implementation plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)

## 1. Goal

Add a first-class Windows development path without changing the Linux product's behavior:

- Windows owns the user experience, machine integration, database, and public ports.
- WSL2 owns Linux runtimes and Linux-only PHP extensions.
- Docker owns optional infrastructure services.
- Projects can live on Windows drives or in WSL; both are supported per site.
- No Docker PHP and no native Windows PHP.

The same architecture applies to Linux natively: one daemon/helper pair and the same logical data layout, without the WSL agent.

## 2. Non-goals

- Windows 10, Windows arm64, macOS, or Linux arm64.
- Native Windows PHP, PHP-FPM, nginx, or Apache.
- Docker-based PHP.
- WSL mirrored networking mode.
- Automatic migration from Linux `~/.devstack`.
- Signed Windows packages or automatic updates; unsigned installers are documented.
- Windows external nginx/Apache installed outside Docker.
- Running Docker services outside the managed WSL Docker Engine (Docker Desktop is not the supported substrate).

## 3. Process model

```text
┌──────────────────────────── Windows host ────────────────────────────┐
│                                                                      │
│  devstack CLI / dashboard                                             │
│          │ named pipe                                                │
│          ▼                                                            │
│  devstack daemon (user)                                              │
│  API · UI · SQLite · routes · builtin proxy · orchestration           │
│          │ named pipe                         │ HTTPS/HTTP to agent  │
│          ▼                                    │                       │
│  devstack-helper (LocalSystem)                 │                       │
│  CA · trust · hosts · firewall · 80/443 ───────┼──► WSL distro IP     │
│                                                │                       │
└────────────────────────────────────────────────┼───────────────────────┘
                                                 │
┌────────────────────── managed Debian 12 WSL2 ──┼───────────────────────┐
│                                                ▼                       │
│  devstack-agent (systemd)                                             │
│  auth · health · ports · site supervision · logs · Docker control      │
│       │                                                               │
│       ├── native PHP-FPM (Linux extensions)                           │
│       ├── native Node runtimes                                        │
│       ├── static backend                                              │
│       └── Docker Engine                                               │
│             ├── nginx / Apache                                        │
│             ├── MySQL / MariaDB / PostgreSQL / Redis                  │
│             └── phpMyAdmin                                            │
│                                                                       │
└───────────────────────────────────────────────────────────────────────┘
```

### Windows daemon

Unprivileged per-user process:

- REST API, embedded dashboard, CLI socket/transport.
- SQLite source of truth, control-plane config, logs, backups.
- Builtin reverse proxy and route table.
- Windows-side site/route orchestration.
- Agent client and helper client.
- Does not load PHP/Node or read the CA private key.

### Windows helper

LocalSystem service:

- Named pipe `\\.\pipe\devstack-helper` with ACL limited to the installing SID, SYSTEM, and Administrators.
- Local CA and leaf signing; CA private key never leaves `C:\devstack\system`.
- Windows certificate store, Edge/Chrome inheritance, Firefox enterprise policy.
- Windows hosts file managed block.
- Windows firewall rules for required loopback/WSL paths.
- Privileged 80/443 TCP forwarders.
- Install, repair, upgrade, and uninstall primitives.
- Fixed protocol-like operations only: no arbitrary paths, commands, or certificates from the daemon.

### devstack-agent

Linux systemd service inside the managed distro:

- Bearer-token authenticated HTTP API on the WSL NAT interface.
- Health, version, capability, and path information.
- Linux PHP-FPM and Node runtime installation, validation, supervision.
- Static backend processes.
- Site generation/validate/swap/rollback behavior.
- Log capture/streaming and port allocation.
- Docker Engine and Compose control for the fixed service catalog.
- Agent state/config under `/data/devstack`.

### Linux native

The existing daemon + helper pair remains. It uses the same `/data/devstack` layout and Docker on the host. No agent and no WSL dependency.

## 4. Platform roots and project modes

### Fixed roots

| Role | Linux / WSL | Windows |
|---|---|---|
| Application/data root | `/data/devstack` | `C:\devstack` |
| Default project root | `/data/devstack/projects` | `C:\devstack\projects` |

`C:\devstack` is fixed. `/data/devstack` is fixed for new installs; the code may still accept an internal override for tests and existing Linux installs.

Linux compatibility: existing installs keep their detected helper paths (`/etc/devstack/helper.yaml`, `/var/lib/devstack-helper`) and are not moved silently. New Linux installs use `/data/devstack/system` for helper config/CA and `/data/devstack/bin` for the helper binary. The installer must detect and preserve the legacy layout.

### Per-site project source

Add a site-level `source_mode` distinct from the execution backend:

- `local`: native Linux project source; existing Linux behavior.
- `wsl`: project source under WSL ext4; default for PHP and Node on Windows.
- `windows`: project source on NTFS; default for static sites on Windows; supported for all kinds with warnings.

Windows path mapping:

- `windows`: `C:\...` ↔ `/mnt/c/...`
- `wsl`: `\\wsl.localhost\devstack\...` ↔ `/...`

Rules:

- Accept absolute drive-letter paths and managed `\\wsl.localhost\<distro>\...` paths only.
- Reject arbitrary UNC paths, device paths, and cross-boundary symlinks/junctions.
- Windows mode uses `/mnt/c`; show a performance and watcher warning at creation.
- Switching modes is an explicit **copy-and-verify** operation, never an in-place rebind:
  - site must be stopped;
  - destination must be absent/empty;
  - copy to a temporary sibling, verify file count/checksums and permissions, then switch the row;
  - default keeps the source; `--remove-source` deletes it only after verification;
  - failure leaves the original site row and source untouched;
  - no automatic overwrite/merge in v1.
- No devstack-created Windows path starts with a dot. Project-owned `.git`, `.idea`, `.env`, `.htaccess` are unaffected.
- Project files are never stored under the protected `system` subtree.

## 5. File layout

### Windows

```text
C:\devstack\
├── bin\
│   ├── devstack.exe
│   └── devstack-ctl.exe              # optional CLI helper
├── config.yaml
├── state.db
├── pki\                              # canonical leaf certificates
├── sites\                            # control-plane site snapshots
├── services\                         # optional canonical service snapshots
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

ACL requirements:

- `C:\devstack`: installing user can read/write the user-facing tree; no inherited write/delete on `system`.
- `C:\devstack\system`: SYSTEM and Administrators full control; installing user no write/delete. The user cannot replace the service binary or CA key.
- `state.db`, `config.yaml`, `pki`, `sites`, and `services` are user ACLs.
- `wsl\...\ext4.vhdx` is user-writable; document that live VHDX copies are not safe backups.
- The installer creates the root and ACLs with elevation.

### Linux and WSL

```text
/data/devstack/
├── bin\
│   └── devstack-agent                  # agent binary in WSL
├── config.yaml                         # Linux daemon config
├── state.db                            # Linux daemon SQLite
├── agent.yaml                          # WSL agent config
├── agent.token                         # WSL agent bearer token, 0600
├── pki\                                # Linux leaf certs
├── runtimes\
│   ├── php\...
│   └── node\...
├── sites\
│   └── <id>\<generation>\
├── services\
│   └── <service-id>\
│       ├── config\
│       └── data\
├── vhosts\
├── tls\                                # leaf copies for Linux containers
├── projects\
├── logs\
├── backups\
├── cache\
├── run\
├── secrets\
├── certs\
├── tools\
├── docker\                             # dedicated WSL Docker data-root
└── system\                             # Linux helper only
    ├── helper.yaml
    └── pki\
        ├── rootCA.key
        └── rootCA.pem
```

Ownership:

- Native Linux: `/data/devstack/system` root-only; daemon/runtime directories owned by the installing user; `secrets` 0700.
- WSL: agent runs as root for Docker and process isolation; site workers use the `devstack` user; `agent.token` 0600; project source owned by the installing Windows user's WSL UID.
- Docker service data under `/data/devstack/services/<id>/data` is ext4 and never an NTFS bind mount.
- Native Linux Docker is the host's shared engine. Its `/var/lib/docker` remains an OS-managed exception; devstack service data still lives under `/data/devstack/services`.
- In dedicated WSL Docker, `data-root` is `/data/devstack/docker`.

### Paths exposed to Windows

```text
\\wsl.localhost\devstack\data\devstack\...
```

PhpStorm 2026.2+ opens WSL projects natively. Windows-mode projects remain normal NTFS projects and use a WSL PHP interpreter with path mapping.

## 6. Data and configuration

### Windows SQLite

- `state.db` on NTFS only. Never place SQLite on `/mnt/c` or `\\wsl.localhost`.
- Windows daemon owns sites, services metadata, cert metadata, settings, events, benchmarks, and applied performance state.

### Configuration additions

- `source_mode` on sites: `local`, `windows`, or `wsl`.
- `projects_root` for the default project directory.
- `wsl_distro` used by the managed distro.
- Agent endpoint/discovery metadata; the Windows daemon stores the bearer token with Windows DPAPI-backed user protection, while the agent stores its copy at `/data/devstack/agent.token` mode 0600.
- Windows helper configuration lives in `C:\devstack\system\helper.yaml` and is written only by the helper/installer.
- No dot-prefixed Windows names.

### Certificate custody

- CA private key: Windows helper `C:\devstack\system\pki`; Linux helper `/data/devstack/system/pki`.
- Leaf certificates: Windows/Linux control plane `pki`.
- WSL containers receive only leaf copies under `/data/devstack/tls`, mode 0700 directories/0600 keys.
- CA private key never enters WSL or Docker.
- Windows trust: LocalMachine/CurrentUser certificate store as appropriate, Edge/Chrome inheritance, Firefox enterprise policy.
- Linux trust: existing OS/browser trust path.
- WSL curl/Node/PHP trust: CA public bundle copied to the agent runtime.
- WSL DNS/hosts: the agent manages a devstack block in `/etc/hosts` and sets `[network] generateHosts=false` in the managed distro, preserving localhost entries and unrelated user entries. This lets Composer, curl, Node, PHP, and terminal sessions resolve `.test` names inside WSL.

## 7. Networking and public ports

- Windows helper owns Windows 80/443.
- Linux helper owns Linux 80/443.
- Builtin mode:
  - Windows: helper → Windows daemon 8080/8443 → WSL PHP/Node/static.
  - Linux: helper → Linux daemon → native backends.
- Docker nginx/Apache mode:
  - Web containers use host networking inside WSL/Linux.
  - Windows helper forwards 80/443 to the nginx/Apache private ports inside WSL.
  - The daemon/agent updates helper targets atomically when ports or the distro IP change.
  - Linux keeps existing native/Docker behavior.
- Agent:
  - Listens on the WSL NAT interface, not loopback only.
  - Bearer token required on every request.
  - Windows daemon tries WSL localhost forwarding then the distro IP.
  - Mirrored networking is rejected.
- Docker:
  - Dedicated Docker Engine inside WSL.
  - Host networking for web server containers so they can reach native PHP-FPM/Node on WSL loopback.
  - Database/Redis/phpMyAdmin remain loopback-only inside WSL.
- Windows access to WSL service ports:
  - The agent exposes a fixed loopback-service relay on the WSL NAT interface for enabled service ports (MySQL/MariaDB, PostgreSQL, Redis, phpMyAdmin).
  - The Windows helper forwards selected Windows loopback ports to the agent relay and creates loopback-only firewall rules.
  - Windows clients connect to `127.0.0.1:<port>`; nothing is exposed to the LAN.
  - The relay accepts only catalog service IDs/ports and forwards only to container loopback.

## 8. Runtime and feature behavior

### PHP

- Native Linux PHP-FPM in WSL/Linux.
- Existing Linux PHP builds and extension catalog remain the basis.
- Every catalog PHP build must pass a Debian 12 WSL compatibility gate (`php -v`, FPM start, each supported extension load). The WSL rootfs includes required system libraries.
- If an existing build fails the gate, the release must publish a WSL-compatible variant and catalog entry; Lane F owns that fallback and its artifacts.
- Linux-only extensions are supported.
- Per-site FPM pool sizing remains persisted and applied through validate/swap.
- No Docker PHP.

### Node/static

- Native Linux Node inside WSL/Linux.
- Static backend inside WSL/Linux.
- Windows-mode projects are read through `/mnt/c` with performance warnings.

### Terminal

- Site terminal runs through the WSL/Linux agent PTY.
- Windows daemon proxies WebSocket/HTTP; no Windows ConPTY requirement for site shells.
- Same ticket, origin, UID, size, and teardown rules as Linux.

### Performance

- Web/PHP profiles and benchmark history remain.
- Host tuning is disabled on Windows (no sysctl/limits equivalent).
- Windows resource collection reports Windows CPU/memory; agent reports distro resources.
- FPM pool settings apply inside WSL.
- Benchmarks run from Windows through the selected public route.

### Services

- Existing service catalog and Compose renderer.
- Docker Engine inside WSL owns container lifecycle.
- Configs/data/secrets under `/data/devstack/services`.
- Database data ext4 only.
- phpMyAdmin loopback.
- Credentials/rotation and service switching preserved.
- Docker PHP is explicitly excluded.

## 9. Windows installation and lifecycle

- **WSL baseline:** Windows 11 22H2+ x86_64 with WSL 2.0.0 or newer and systemd support. The installer does **not** enable Windows optional features, install WSL, or reboot. It validates `wsl.exe --version`, WSL2, virtualization, and systemd support; if missing, it prints the exact `wsl --install`/`wsl --update` commands and exits. It uses `--version 2` on import and does not change the user's default distro.
- **Managed distro identity:** a distro named `devstack` is managed only if it contains `/data/devstack/managed.json` with `managed_by=devstack` and a generated install ID. A pre-existing distro without the marker is refused, never adopted or replaced. The error instructs the user to rename/remove it or select another distro name.
- `install.ps1` requires elevation for root creation, helper service, ACLs, and firewall.
- Installer:
  - creates `C:\devstack` and protected `system` ACLs;
  - installs daemon/CLI for the user;
  - installs helper service;
  - writes helper config;
  - imports/updates the managed WSL distro at `C:\devstack\wsl`;
  - installs the agent and Docker Engine;
  - registers per-user daemon startup;
  - runs `devstack init`.
- Daemon runs at user logon via a per-user scheduled task/startup mechanism, not as a Windows service.
- Helper starts automatically as a Windows service.
- Upgrade preserves `config.yaml`, `state.db`, `pki`, `projects`, `services`, `system`, and the VHDX; binaries/rootfs/agent are replaceable.
- Uninstall:
  - stops daemon/agent/containers;
  - backs up SQLite/config if requested;
  - removes helper service/firewall/hosts/trust;
  - leaves the WSL distro and WSL-mode projects by default;
  - `--remove-wsl` requires either `--export-wsl-projects <path>` with a verified archive or an explicit `--discard-wsl-projects` confirmation;
  - never deletes Windows projects.
- WSL distro export is the backup unit for Linux runtime state and Docker data.

## 10. Failure and security rules

- Named pipe peer SID must equal the installing user; pipe ACL excludes other users.
- Agent token is required; no unauthenticated routes.
- WSL agent is reachable only over NAT; no public interface.
- Docker socket is not exposed to Windows users; only the agent controls the dedicated engine.
- No arbitrary commands/paths/certificates from the daemon.
- No installed file set uses dot-prefixed names on Windows.
- Cross-boundary symlinks are rejected.
- Hosts/trust/CA only through the helper.
- SQLite and CA never on 9P/NTFS bind paths.
- Service data never on NTFS.
- WSL start failures produce `WSL_UNAVAILABLE`; agent auth failures `AGENT_UNAUTHENTICATED`; missing runtimes `RUNTIME_MISSING`; bind failures `PORT_IN_USE`; config/reload failures reuse existing codes.

## 11. Testing and acceptance

### Test isolation

- Lane implementers run only fakes/unit tests and cross-compiles concurrently.
- Real Windows host resources are serialized by the controller: one helper service/named pipe, one real `C:\devstack` install, one managed distro, one 80/443 owner, one hosts/trust mutation, one Docker engine at a time.
- Test-mode runs may use a lane-unique root (`C:\devstack-test\<lane>`), named-pipe suffix, distro name (`devstack-test-<lane>`), high ports, and no hosts/trust/firewall writes. These are never used as production paths.
- Linux regressions run in WSL/Linux; Windows-tagged tests run on the Windows host.

### Automated tests

- Windows cross-compilation and unit tests.
- Named-pipe protocol/ACL tests.
- Path translation/source-mode table tests.
- Helper CA/trust/hosts tests with fakes.
- WSL provisioning contract tests with fake `wsl.exe`.
- Agent HTTP/protocol/auth tests.
- Docker Compose/renderer tests.
- TDD for every production change.

Manual Windows acceptance on a fresh Windows 11 machine:

1. Install without manual WSL/Docker setup beyond prerequisites.
2. `doctor` reports helper, CA, hosts, agent, Docker, and runtimes.
3. Create a static site from `C:\devstack\projects`; open trusted `.test` HTTPS.
4. Create a static site from WSL; open trusted `.test` HTTPS.
5. Create PHP/Node sites from both source modes; verify extension isolation and FPM.
6. Install/activate nginx and MySQL; verify config editing, credentials, phpMyAdmin, and service switching.
7. Restart daemon, helper, WSL, and Windows; sites recover.
8. Change daemon HTTP/HTTPS ports; helper targets persist and 80/443 follow.
9. Upgrade; projects/state survive.
10. Uninstall; trust/hosts/firewall cleaned; projects remain.

Linux regression gate:

- Existing Linux full gate, PHP acceptance, Linux E2E, and Docker smokes remain green.
- Existing Linux installs are detected and not auto-migrated.

## 12. Documentation deliverables

- This design.
- The implementation plan with five parallel lanes.
- Windows install/upgrade/uninstall, WSL layout, project modes, PhpStorm, trust, Docker, troubleshooting.
- `.gitattributes` forcing LF for Linux scripts and build files.
- README/handoff status updated to "Windows/WSL planned or in progress", never "supported" until acceptance passes.
