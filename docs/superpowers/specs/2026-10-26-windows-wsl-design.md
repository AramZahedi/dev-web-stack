# Windows 11 + WSL2 Support Design

- **Date:** 2026-10-26
- **Revised:** 2026-10-27 after the in-depth plan review
- **Status:** Revised and internally consistent; implementation **not started**
- **Approval:** Architecture approved; revised plan awaits execution approval
- **Scope:** Windows 11 22H2+ x86_64, managed Debian 12 WSL2 distro, native Windows daemon/helper, native Linux PHP/Node inside WSL, Docker service containers
- **Supersedes:** the Windows/WSL sections of the 2026-10-22 M1 design. That document remains historical for the released Linux product.
- **Implementation plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Review resolution record:** [Windows plan review resolution](../status/2026-10-27-windows-plan-review-resolution.md)

> **Status rule:** Windows support is planned, not implemented. Do not claim it
> is supported, shipped, or accepted until the Windows acceptance matrix in the
> implementation plan has recorded evidence.

## 0. Revision 2026-10-27 — resolved review findings

This revision resolves the cross-boundary contracts that the first planning
pass left ambiguous. The binding decisions are:

1. **Windows always owns the public entry.** The Windows helper forwards
   ports 80/443 to the Windows daemon proxy on 8080/8443. The helper never
   forwards directly to a WSL web server.
2. **The Windows daemon is the single public HTTP/TLS router.** It serves the
   dashboard and routes site hosts to WSL backends (builtin mode) or to the
   active WSL Docker web server on a private HTTP port (Docker mode). Public
   TLS terminates in the daemon with the native CA certificate.
3. **The WSL agent runs a fixed ingress on WSL 80/443.** WSL `/etc/hosts`
   maps `.test` names to `127.0.0.1`; the ingress forwards raw TCP to the
   Windows daemon proxy through the WSL host gateway. This makes `.test`
   names work from inside WSL in both builtin and Docker modes without
   exposing the Windows API directly.
4. **Windows→WSL backend traffic uses WSL localhost forwarding.** Site
   backends bind `127.0.0.1` inside WSL and the Windows daemon reaches them at
   `127.0.0.1:<port>`. The agent API additionally listens on the distro IP for
   control fallback. The doctor validates localhost forwarding and reports a
   typed failure when it is unavailable.
5. **All Windows site backends run in the WSL agent.** Static, Node, and PHP
   sites use the agent; the Windows daemon does not run a local site
   supervisor. Windows-mode static source is read through `/mnt/c` with the
   documented performance warning.
6. **The agent owns WSL port allocation.** It allocates HTTP/FastCGI and
   private web-server ports, binds and validates them, and returns them in
   `PreparedSite`. The Windows daemon persists the returned ports. Linux keeps
   the existing daemon-side allocator.
7. **WSL ext4 is canonical for generated/site/service data.** Service
   configuration, service data, service secrets, generated PHP/FPM configs,
   runtimes, site/service logs, and Docker data live under `/data/devstack` in
   WSL. Windows keeps SQLite metadata, control-plane config, leaf certificates,
   daemon logs, cache, and the VHDX. The daemon proxies service config,
   credential, log, and web-server operations through the agent.
8. **Every WSL path is validated and copied by the agent.** Windows cannot
   reliably inspect WSL symlinks, ownership, or permissions over
   `\\wsl.localhost`. Source-mode validation, copy, checksum, and ownership
   verification are agent operations.
9. **The agent API uses HTTPS with a pinned agent certificate and a bearer
   token.** The token and certificate fingerprint are protected with Windows
   DPAPI; the agent stores its token at `/data/devstack/agent.token` (0600)
   and its key material under `/data/devstack/agent-tls` (0600, root-only).
10. **Windows named pipes are ACL-restricted and SID-checked.** Both the
    helper pipe and the daemon control pipe grant only the installing user,
    SYSTEM, and (for the helper) Administrators. The server verifies the peer
    SID; the API's socket-auth bypass is used only after that verification.
11. **The Windows helper exposes only host integration operations.** CA,
    trust, hosts, firewall, forwarder targets, and health/version. Service,
    tool, vhost, Docker, and host-performance RPCs are not served on Windows.
12. **Global `php`/`composer`, `devstack shell`, `devstack exec`, and site
    builds run in WSL.** Windows gets managed `.cmd` shims that invoke
    `wsl.exe` in the managed distro. There is no native Windows PHP and no
    Docker PHP.
13. **No devstack-created Windows basename starts with a dot.** Internal
    temporary files, manifests, state markers, and backups are renamed. Only
    project-owned files such as `.git`, `.env`, `.idea`, and `.htaccess` may
    start with a dot.
14. **Docker web servers in WSL listen on private HTTP ports.** They still use
    host networking so they can reach native PHP-FPM/Node on WSL loopback, but
    they do not own public 80/443 on Windows. The Windows daemon terminates
    TLS and proxies HTTP to the private port. The dashboard remains a daemon
    route, so a container replacement cannot strand `devstack.test`.
15. **New Linux installs use `/data/devstack` without moving existing
    installs.** Explicit `--home`/`DEVSTACK_HOME` wins. A detected legacy
    `~/.devstack` or legacy helper layout is preserved. `/data/devstack` is
    root-owned at the top; user-facing subdirectories are owned by the
    installing user; `system` is root-only.
16. **WSL and Docker artifacts are pinned and published.** The Debian 12
    rootfs, Docker Engine/Compose, agent binary, and PHP/Node artifacts are
    checksum-pinned, released with checksums/SBOMs, and verified by the
    installer.

The detailed contracts follow. Where this document conflicts with the
2026-10-26 planning handoff, this revision wins.

## 1. Goal

Add a first-class Windows development path without changing the Linux
product's behavior:

- Windows owns the user experience, machine integration, SQLite metadata, and
  public ports.
- WSL2 owns Linux runtimes, site execution, and Linux-only PHP extensions.
- Docker owns optional infrastructure services.
- Projects can live on Windows drives or in WSL; both are supported per site.
- No Docker PHP and no native Windows PHP.

The same logical architecture applies to Linux natively: one daemon/helper
pair and the same data layout, without the WSL agent.

## 2. Non-goals

- Windows 10, Windows arm64, macOS, or Linux arm64.
- Native Windows PHP, PHP-FPM, nginx, or Apache.
- Docker-based PHP.
- WSL mirrored networking mode.
- Automatic migration from Linux `~/.devstack`.
- Signed Windows packages or automatic updates; unsigned installers are
  documented.
- Windows external nginx/Apache installed outside Docker.
- Running Docker services outside the managed WSL Docker Engine (Docker
  Desktop is not the supported substrate).
- Exposing the daemon control API or the helper RPC surface to WSL, the LAN,
  or the public network.

## 3. Process model

```text
┌──────────────────────────── Windows host ────────────────────────────┐
│                                                                      │
│  devstack CLI / dashboard                                             │
│          │ named pipe (ACL + SID checked)                            │
│          ▼                                                            │
│  devstack daemon (user)                                              │
│  API · UI · SQLite · routes · builtin proxy · orchestration           │
│          │ named pipe                         │ HTTPS + pinned cert   │
│          ▼                                    │ + bearer token        │
│  devstack-helper (LocalSystem)                 │                      │
│  CA · trust · hosts · firewall · 80/443 ───────┼──► Windows daemon     │
│                                                │    8080/8443          │
│                                                │                       │
└────────────────────────────────────────────────┼───────────────────────┘
                                                 │
┌────────────────────── managed Debian 12 WSL2 ──┼───────────────────────┐
│                                                ▼                       │
│  devstack-agent (systemd, root)                                       │
│  auth · health · ports · ingress · site supervision · logs · Docker   │
│       │                                                               │
│       ├── native PHP-FPM (Linux extensions)                           │
│       ├── native Node runtimes                                        │
│       ├── static backend                                              │
│       └── Docker Engine                                               │
│             ├── nginx / Apache (private HTTP port)                    │
│             ├── MySQL / MariaDB / PostgreSQL / Redis                  │
│             └── phpMyAdmin                                            │
│                                                                       │
│  WSL ingress 80/443 ──► Windows host gateway:8080/8443                 │
└───────────────────────────────────────────────────────────────────────┘
```

### Windows daemon

Unprivileged per-user process:

- REST API, embedded dashboard, CLI named-pipe transport.
- SQLite source of truth, control-plane config, daemon logs, backups.
- Public builtin HTTP/HTTPS proxy and route table.
- Agent client and helper client.
- Does not load PHP/Node, does not run site backends, and does not read the
  CA private key.

### Windows helper

LocalSystem Windows service:

- Named pipe `\\.\pipe\devstack-helper`, ACL limited to the installing SID,
  SYSTEM, and Administrators.
- Local CA and leaf signing; CA private key never leaves
  `C:\devstack\system`.
- Windows certificate store, Edge/Chrome inheritance, Firefox enterprise
  policy.
- Windows hosts file managed block.
- Windows firewall rules for the WSL ingress/firewall paths.
- Privileged 80/443 TCP forwarders, always targeting the Windows daemon
  proxy.
- Install, repair, upgrade, and uninstall primitives.
- Fixed operations only: no arbitrary paths, commands, certificates, service
  containers, tools, or host performance tuning.

### devstack-agent

Linux systemd service inside the managed Debian 12 distro:

- HTTPS API on the WSL NAT interface and on loopback, with a pinned
  self-signed certificate and bearer-token authentication.
- Health, version, capabilities, and path information.
- WSL ingress on 80/443 forwarding to the Windows daemon proxy.
- Linux PHP-FPM, Node, and static backend execution.
- Port allocation and validation inside WSL.
- Runtime install/validate/swap/rollback and per-site FPM configuration.
- Site/service logs, backups, and terminal/PTY sessions.
- Docker Engine and Compose control for the fixed service catalog.
- WSL `/etc/hosts` management.
- Agent state/config under `/data/devstack`.
- Never receives the CA private key.

### Linux native

The existing daemon + helper pair remains. It uses the revised
`/data/devstack` layout for new installs and Docker on the host. No agent and
no WSL dependency. Existing installs are detected and preserved.

## 4. Platform roots and project modes

### Fixed roots

| Role | Linux / WSL | Windows |
|---|---|---|
| Application/data root | `/data/devstack` | `C:\devstack` |
| Default project root | `/data/devstack/projects` | `C:\devstack\projects` |

`C:\devstack` is fixed. `/data/devstack` is fixed for new Linux installs;
`--home` and `DEVSTACK_HOME` remain supported for tests, CI, and existing
installs.

### Root precedence and legacy detection

1. An explicit `--home` wins.
2. `DEVSTACK_HOME` wins when set.
3. If `${HOME}/.devstack/config.yaml` or `${HOME}/.devstack/state.db` exists,
   use `${HOME}/.devstack` and preserve it. Do not move or rewrite it.
4. On Linux, otherwise use `/data/devstack`.
5. On Windows, otherwise use `C:\devstack`.

A root must be writable by the daemon user. If `/data/devstack` exists but is
not writable, startup fails with remediation (`DEVSTACK_HOME` or a fresh
install) rather than silently falling back.

### Ownership and protection

- `/data/devstack` is created by the installer as root:root, mode 0755.
- User-facing subdirectories (`projects`, `runtimes`, `sites`, `services`,
  `logs`, `backups`, `cache`, `run`, `secrets`, `certs`, `tools`, `vhosts`,
  `tls`, `docker`) are owned by the installing user or the WSL `devstack`
  user as appropriate.
- `/data/devstack/system` is root:root, mode 0750. The installer creates it
  before the helper starts. The helper binary remains outside the
  user-writable tree (`/usr/local/lib/devstack/devstack-helper` on native
  Linux, `C:\devstack\system\bin\devstack-helper.exe` on Windows, and
  `/data/devstack/bin/devstack-agent` on WSL).
- A user must not be able to rename or replace `system` by owning its parent.
  The installer sets the parent ownership/mode accordingly.
- `C:\devstack` is user-accessible for the user-facing tree, but
  `C:\devstack\system` gives the user no write, delete, or ownership change.
- No devstack-created Windows basename starts with a dot. Project-owned
  `.git`, `.idea`, `.env`, and `.htaccess` are unaffected.

### Per-site project source

Add a site-level `source_mode` distinct from the execution backend:

- `local`: native Linux project source; existing Linux behavior.
- `wsl`: project source under WSL ext4; default for PHP and Node on Windows.
- `windows`: project source on NTFS; default for static sites on Windows;
  supported for all kinds with warnings.

Path contract:

| Mode | Windows-visible path (`host_path`) | WSL backend path (`backend_path`) |
|---|---|---|
| `local` | Linux host path | same Linux path |
| `windows` | `C:\...` | `/mnt/c/...` |
| `wsl` | `\\wsl.localhost\<distro>\...` | `/...` |

Rules:

- Accept absolute drive-letter paths and managed
  `\\wsl.localhost\<distro>\...` paths only. Accept the `\\wsl$` alias only
  if the configured distro matches and the path canonicalizes to the managed
  distro.
- Reject arbitrary UNC paths, device paths, alternate data streams, and
  cross-boundary symlinks/junctions.
- Windows mode uses `/mnt/c`; show a performance and watcher warning at
  creation.
- Switching modes is an explicit **agent-verified copy operation**, never an
  in-place rebind:
  - site must be stopped;
  - destination must be absent/empty;
  - copy to a temporary sibling as the WSL `devstack` user;
  - verify file count, SHA-256 manifest, Linux ownership/mode, and absence of
    escaping symlinks;
  - switch the site row only after verification;
  - default keeps the source; `--remove-source` deletes it only after
    verification;
  - failure leaves the original site row and source untouched;
  - no automatic overwrite/merge in v1.
- No devstack-created Windows path starts with a dot. Project-owned files are
  unaffected.
- Project files are never stored under the protected `system` subtree.
- The Windows daemon derives `proxy_document_root` (a Windows-visible
  absolute document root) from `host_path` and the relative `document_root`
  for PHP static-file resolution. The agent uses `backend_path` for FPM and
  Node. Both are validated independently.

## 5. File layout

### Windows

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
├── logs\                             # daemon/UI logs
├── backups\                          # state/config backups
├── cache\                            # rootfs and downloads
├── run\                              # pid, readiness, locks
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

There are **no canonical `sites\`, `services\`, `secrets\`, `certs\`, or
`tools\` trees on Windows**. Generated site/service data, service secrets,
runtimes, and Docker data live in WSL. Control-plane site/service metadata is
in `state.db`; the daemon proxies content/credential operations to the agent.
This avoids two sources of truth and NTFS bind mounts.

ACL requirements:

- `C:\devstack`: installing user can read/write the user-facing tree; no
  inherited write/delete on `system`.
- `C:\devstack\system`: SYSTEM and Administrators full control; installing
  user no write, delete, or ownership change. The user cannot replace the
  service binary or CA key.
- `state.db`, `config.yaml`, `pki`, and `projects` are user ACLs.
- `wsl\...\ext4.vhdx` is user-writable; document that live VHDX copies are not
  safe backups.
- The installer creates the root, ACLs, and scheduled task with elevation.

### Native Linux

```text
/data/devstack/                       # root:root 0755
├── bin\                               # user-facing tools/agent links if any
├── config.yaml                        # user-owned
├── state.db                           # user-owned
├── pki\                               # user-owned leaf certs
├── runtimes\
├── sites\
├── services\
│   └── <service-id>\
│       ├── config\
│       └── data\
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
└── system\                            # root:root 0750
    ├── helper.yaml
    └── pki\
        ├── rootCA.key
        └── rootCA.pem
```

- Native Linux Docker data remains `/var/lib/docker` because it is a shared
  host engine. Devstack service data still lives under
  `/data/devstack/services`.
- The helper binary is `/usr/local/lib/devstack/devstack-helper` on new
  installs. The helper config/CA move to `/data/devstack/system`; existing
  `/etc/devstack/helper.yaml` and `/var/lib/devstack-helper` installs are
  detected and preserved.
- The systemd helper unit and the `install.sh` embedded unit must use the same
  paths and remain in sync.

### WSL

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
│   ├── php\...
│   └── node\...
├── sites\
│   └── <id>\<generation>\
├── services\
│   └── <service-id>\
│       ├── config\
│       └── data\
├── vhosts\
├── tls\                              # leaf copies, 0700 directories/0600 keys
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

The Windows daemon never uses this UNC path for SQLite, the CA, or Docker
data. It may use it for project-file checks and PHP static-file resolution
after agent-side path validation.

## 6. Data and configuration

### Windows SQLite

- `state.db` on NTFS only. Never place SQLite on `/mnt/c` or
  `\\wsl.localhost`.
- Windows daemon owns sites, services metadata, cert metadata, settings,
  events, benchmarks, and applied performance state.
- Site/service content that must be read by WSL or containers is canonical in
  WSL; the daemon holds only metadata and control-plane references.

### Configuration additions

- `source_mode` on sites: `local`, `windows`, or `wsl`.
- `projects_root` for the default project directory.
- `wsl_distro` used by the managed distro.
- Agent endpoint/discovery metadata. The Windows daemon stores the bearer
  token and pinned certificate fingerprint with Windows DPAPI-backed user
  protection; the agent stores the token and TLS key under `/data/devstack`
  with 0600/0700 permissions.
- Windows helper configuration lives in `C:\devstack\system\helper.yaml` and
  is written only by the helper/installer. It uses `allow_sid`, not `allow_uid`.
- No dot-prefixed Windows names.

### Canonical data ownership

| Data | Canonical location | Owner |
|---|---|---|
| Site/service metadata, events, benchmarks | Windows `state.db` (or Linux `state.db`) | daemon |
| Site source (WSL mode) | WSL `/data/devstack/projects` | site user |
| Site source (Windows mode) | `C:\devstack\projects` | installing user |
| Generated PHP/FPM configs | WSL `/data/devstack/sites` | agent/site user |
| Service config | WSL `/data/devstack/services/<id>/config` | agent |
| Service data | WSL `/data/devstack/services/<id>/data` | agent |
| Service secrets | WSL `/data/devstack/services/<id>/secrets` (0700) | agent |
| Runtime builds | WSL `/data/devstack/runtimes` | agent |
| Docker data | WSL `/data/devstack/docker` | Docker Engine |
| Leaf certificates | Windows `pki` (and Linux `pki`) | daemon/helper |
| WSL leaf copies | WSL `/data/devstack/tls` | agent |
| Daemon/UI logs | Windows `logs` | daemon |
| Site/service logs | WSL `/data/devstack/logs` | agent |
| Backups | Windows `backups` for state/config; WSL `backups` for site/service data | each owner |

The daemon exposes service config/content/diff/apply/reset, credential
reveal/rotate, service logs, and web-server config operations by proxying to
the agent. It never duplicates canonical service data on Windows.

### Certificate custody

- CA private key: Windows helper `C:\devstack\system\pki`; Linux helper
  `/data/devstack/system/pki`.
- Leaf certificates: Windows/Linux control plane `pki`.
- WSL containers receive only leaf copies under `/data/devstack/tls`, mode
  0700 directories/0600 keys.
- The agent has a separate self-signed TLS identity under
  `/data/devstack/agent-tls`; this is not the CA.
- CA private key never enters WSL or Docker.
- Windows trust: LocalMachine/CurrentUser certificate store as appropriate,
  Edge/Chrome inheritance, Firefox enterprise policy.
- Linux trust: existing OS/browser trust path.
- WSL curl/Node/PHP trust: the CA public bundle is copied to the agent runtime
  and injected into site/terminal environments (`SSL_CERT_FILE`,
  `NODE_EXTRA_CA_CERTS`, PHP `openssl.cafile`).
- WSL DNS/hosts: the agent manages a devstack block in `/etc/hosts` and sets
  `[network] generateHosts=false` in the managed distro, preserving localhost
  entries and unrelated user entries. `.test` names map to `127.0.0.1`; the
  agent ingress listens on WSL 80/443. The agent also maintains a fixed
  `devstack-windows.internal` entry for the WSL host gateway used by the
  ingress target.

## 7. Networking and public ports

### Public entry

- Windows helper owns Windows 80/443 and always forwards to the Windows
  daemon proxy at 8080/8443. It never forwards to a WSL address.
- Linux helper owns Linux 80/443 and keeps the existing builtin/Docker
  behavior.
- The Windows daemon proxy is the public HTTP/TLS router:
  - dashboard host `devstack.test` -> `127.0.0.1:<ui_port>`;
  - builtin site hosts -> agent-allocated WSL backend ports;
  - Docker-provider site hosts -> the active WSL web server's private HTTP
    port.
- Public TLS terminates in the Windows daemon with the native leaf
  certificate. The daemon enforces force-HTTPS and HSTS stripping. Docker
  web-server containers do not terminate public TLS on Windows.

### Windows daemon proxy listeners

- Bind `127.0.0.1:8080` and `127.0.0.1:8443` for Windows local traffic.
- Additionally bind the WSL host-adapter address (or `0.0.0.0` with a
  firewall restricted to the WSL subnet) on 8080/8443 for the WSL ingress.
- The API/dashboard listener remains 127.0.0.1:7070; it is reached through
  the proxy's `devstack.test` route. The control API is not bound to the WSL
  adapter.
- Windows firewall rules allow only the WSL subnet to reach 8080/8443 and
  deny LAN/public access. The public 80/443 forwarders remain helper-owned.

### WSL ingress

- The agent binds WSL 80/443 as root.
- It forwards raw TCP to the Windows host gateway on 8080/8443. The target is
  a fixed private address discovered from the WSL default route or configured
  in `agent.yaml`; it is never caller-supplied.
- The ingress restarts when the Windows host gateway address changes.
- WSL `/etc/hosts` maps every devstack `.test` name to `127.0.0.1`, so WSL
  curl/Composer/Node/PHP/terminal sessions use the ingress.
- The ingress is fixed-target, root-owned, and has no arbitrary forward
  operation.

### Windows→WSL backend traffic

- WSL localhost forwarding is required for Windows to reach agent/site
  backend ports bound to `127.0.0.1` inside WSL.
- The agent API additionally listens on the distro IP for control fallback.
  The Windows daemon tries `127.0.0.1` first, then the distro IP, for agent
  control requests.
- Site backends bind `127.0.0.1`; the Windows daemon uses the returned
  `PreparedSite.BackendHost` and ports. The default is `127.0.0.1`.
- The doctor checks localhost forwarding and reports
  `WSL_LOCALHOST_FORWARDING_UNAVAILABLE` with remediation if disabled.
  Serving fails closed rather than silently trying an unauthenticated relay.

### Docker service containers

- Docker Engine runs inside the managed WSL distro.
- Data-root: `/data/devstack/docker`.
- Web-server containers use host networking so they can reach native
  PHP-FPM/Node on WSL loopback, but listen on a **private HTTP port** (for
  example 18080). They do not bind public 80/443.
- The Windows daemon proxies public site traffic to the active web server's
  private HTTP port. The dashboard is a daemon route, not a container vhost.
- Database/Redis/phpMyAdmin remain loopback-only inside WSL.
- Windows access to WSL service ports:
  - The agent exposes a fixed loopback-service relay on the WSL NAT
    interface for enabled service ports (MySQL/MariaDB, PostgreSQL, Redis,
    phpMyAdmin).
  - The Windows helper forwards selected Windows loopback ports to the agent
    relay with loopback-only firewall rules.
  - Windows clients connect to `127.0.0.1:<port>`; nothing is exposed to the
    LAN.
  - The relay accepts only catalog service IDs/ports and forwards only to
    container loopback.
- The Linux Docker provider keeps its existing direct public 80/443
  behavior. The private-HTTP provider is a Windows/WSL adapter.

### Agent transport

- HTTPS only, with a self-signed agent certificate generated during
  provisioning.
- The Windows daemon pins the certificate SHA-256 fingerprint and stores it
  alongside the bearer token in DPAPI.
- Every request carries `Authorization: Bearer <token>` and the protocol
  version header.
- The agent listens on loopback and the WSL NAT interface; mirrored
  networking is rejected.
- The token and TLS private key never appear in process arguments.

## 8. Runtime and feature behavior

### PHP

- Native Linux PHP-FPM in WSL/Linux; no native Windows PHP and no Docker PHP.
- Existing Linux PHP builds and extension catalog remain the basis.
- Every catalog PHP build must pass a Debian 12 WSL compatibility gate
  (`php -v`, FPM start, each supported extension load). The WSL rootfs
  includes required system libraries.
- If an existing build fails the gate, the release must publish a
  WSL-compatible variant and catalog entry; Lane F owns that fallback and its
  artifacts.
- The agent owns runtime install/select/validate/swap and generates the
  per-site PHP/FPM configuration in WSL. The Windows daemon sends the site
  spec and persists the returned generation/ports.
- Per-site FPM pool sizing remains persisted and applied through
  validate/swap.
- Windows-mode PHP source is read through `/mnt/c` with warnings; FPM runs in
  WSL.
- `devstack php --site ... -- ...` and Composer run in WSL. Windows `.cmd`
  shims invoke the WSL environment.

### Node and static

- Native Linux Node inside WSL/Linux.
- Static and Node backends run in the WSL agent for Windows sites.
- Windows-mode static source is read through `/mnt/c` with performance
  warnings.
- Static builds run in WSL with the site's selected Node runtime. Windows
  gets no native site build executor.
- Node HMR WebSockets traverse the Windows daemon proxy, WSL ingress, or
  Docker web server as appropriate; the existing WebSocket proxy tests are
  extended to the Windows path.

### Terminal

- Site terminal runs through the WSL agent PTY.
- The Windows daemon issues the existing one-use ticket and proxies the
  WebSocket; no Windows ConPTY requirement for site shells.
- Same ticket, origin, UID/site binding, size, and teardown rules as Linux.
- Terminal sessions run as the WSL site user, never root, with the CA bundle
  and site PHP/Node environment.

### Performance

- Web/PHP profiles and benchmark history remain.
- Host tuning is disabled on Windows; the helper rejects host-tuning RPCs.
- Windows resource collection reports Windows CPU/memory; the agent reports
  distro resources. FPM pool settings apply inside WSL.
- Benchmarks run from Windows through the selected public route and are
  labelled with the measured tool/version and limitations.

### Services

- Existing service catalog and Compose renderer.
- Docker Engine inside WSL owns container lifecycle.
- Configs/data/secrets under `/data/devstack/services` on ext4 only.
- phpMyAdmin loopback.
- Credentials/rotation and service switching preserved; windows daemon
  proxies reveal/rotate to the agent.
- Docker PHP is explicitly excluded.
- Docker web servers on Windows expose a private HTTP port and are fronted by
  the Windows daemon. The Linux Docker provider remains public.
- Service configuration editing uses the WSL canonical tree. Windows keeps
  no editable duplicate.

### Tooling, shells, and builds

- Global `php` and `composer` are WSL-managed tools. Windows gets
  `C:\devstack\bin\php.cmd` and `composer.cmd` shims that invoke the managed
  distro and the machine-wide default or site context.
- `devstack shell` and `devstack exec` translate the site path and launch
  `wsl.exe -d <distro> -- bash -lc ...` with the site environment.
- `devstack shell-init` remains POSIX shell output for WSL shells; Windows
  users use `devstack shell`/`exec` or the generated `.cmd` shims.
- Site builds run in WSL via the agent.
- Dashboard tooling/build/terminal actions route to the agent.

## 9. Windows installation and lifecycle

### WSL baseline

- Windows 11 22H2+ x86_64.
- WSL 2.0.0+ with systemd support.
- Installer validates `wsl.exe --version`, WSL2, virtualization, systemd
  support, localhost forwarding, and disk space.
- It does **not** enable Windows optional features, install WSL, or reboot.
- Missing prerequisites produce exact `wsl --install`/`wsl --update`
  instructions and exit.

### Install phases

The installer is split so per-user WSL state is not created by a different
administrator account:

1. **Elevated system phase (`install.ps1`):**
   - validate prerequisites;
   - create `C:\devstack` and `system` ACLs;
   - install daemon/CLI binaries and helper service;
   - write helper config;
   - create firewall rules and the per-user logon task for the installing
     user;
   - add `C:\devstack\bin` to the user PATH.
2. **User phase (`devstack init`):**
   - import/update the managed WSL distro at `C:\devstack\wsl`;
   - install/verify the agent and Docker Engine in WSL;
   - generate/rotate the agent token and TLS identity, store the DPAPI copy;
   - write the WSL agent config, hosts block, and CA bundle;
   - start the helper/daemon/agent and run diagnostics.

`install.ps1` requires elevation for the system phase. The user phase runs as
the installing user. If the installer was launched as a different
administrator account, it must hand the user phase to the installing user
rather than importing the distro under the wrong account.

### Managed distro identity

- Marker `/data/devstack/managed.json` with `managed_by=devstack` and a
  generated install ID.
- A pre-existing `devstack` distro without the marker is refused, never
  adopted or replaced. The error instructs the user to rename/remove it or
  select another distro name.
- The managed distro is imported with `--version 2`; the default distro is
  not changed.
- `wsl --shutdown` is required for consistent VHDX backup/export; the
  installer/upgrade documents this.

### Upgrade

- Preserve `config.yaml`, `state.db`, `pki`, `projects`, `system`, and the
  VHDX.
- Replace binaries and the agent.
- For rootfs updates, export the old distro as a backup, import the new
  pinned rootfs into a temporary distro, restore `/data/devstack` data as the
  site user, verify, swap, and roll back on failure. Do not copy a running
  VHDX.
- Run `devstack doctor` and the Windows acceptance subset after upgrade.

### Uninstall

- Stop daemon/agent/containers.
- Back up SQLite/config if requested.
- Remove helper service, firewall rules, hosts block, and trust entries.
- Preserve the WSL distro and WSL-mode projects by default.
- `--remove-wsl` requires either `--export-wsl-projects <path>` with a
  verified archive or explicit `--discard-wsl-projects` confirmation.
- Never delete Windows projects by default. State/config removal requires an
  explicit purge flag.
- Document what remains in `C:\devstack` and what the user must delete
  manually.

### Artifact supply chain

- Pin the Debian 12 rootfs URL and SHA-256, Docker Engine/Compose versions,
  agent binary, and PHP/Node artifacts in a lock file.
- The release workflow builds and publishes the rootfs, agent binary, Windows
  daemon/helper archive, checksums, and SBOMs.
- The installer downloads over HTTPS and verifies checksums before import.
- A release test imports the rootfs, starts the agent, runs Docker
  `hello-world`/Compose, and verifies the agent TLS/token path.

## 10. Failure and security rules

- Named pipe peer SID must equal the installing user; pipe ACL excludes other
  users. Both the helper and the daemon control pipe enforce this.
- Agent token and pinned certificate are required; no unauthenticated routes.
- WSL agent is reachable only over NAT/loopback; no public interface.
- Windows daemon proxy binds the WSL adapter only for the ingress; firewall
  restricts it to the WSL subnet.
- Docker socket is not exposed to Windows users; only the agent controls the
  dedicated engine.
- No arbitrary commands/paths/certificates/services from the daemon to the
  helper.
- WSL path validation/copy is agent-side; Windows-side UNC checks are never
  the sole authorization.
- No installed file set uses dot-prefixed names on Windows.
- Cross-boundary symlinks/junctions are rejected.
- Hosts/trust/CA only through the helper. Docker gets leaf certs only.
- SQLite and the CA never live on 9P/NTFS bind paths. Service data never lives
  on NTFS.
- The Windows helper serves only host-integration RPCs; host tuning and
  Docker/tool/vhost operations are rejected.
- WSL start failures produce `WSL_UNAVAILABLE`; agent auth failures
  `AGENT_UNAUTHENTICATED`; certificate pin failures
  `AGENT_CERT_UNTRUSTED`; missing localhost forwarding
  `WSL_LOCALHOST_FORWARDING_UNAVAILABLE`; missing runtimes
  `RUNTIME_MISSING`; bind failures `PORT_IN_USE`; source copy failures
  `SOURCE_COPY_FAILED`; path escapes `PATH_ESCAPE`.
- The local dashboard/API is single-user. WSL site code is user code and is
  considered inside the local trust boundary; do not treat it as an untrusted
  multi-tenant boundary.

## 11. Testing and acceptance

### Test isolation

- Lane implementers run unit tests, fakes, and cross-compiles concurrently.
- Real Windows host resources are controller-serialized: one helper
  service/pipe, one production `C:\devstack` install, one managed distro,
  one 80/443 owner, one hosts/trust mutation, and one Docker engine at a
  time.
- Test-mode runs may use a lane-unique root (`C:\devstack-test\<lane>`),
  named-pipe suffix, distro name (`devstack-test-<lane>`), high ports, and no
  hosts/trust/firewall writes. These are never used as production paths.
- Linux regressions run in WSL ext4 or a Linux checkout, not `/mnt/c`.
- Windows-tagged tests run on the Windows host.

### Automated tests

- Windows cross-compilation and Windows-tagged unit tests.
- Named-pipe protocol, ACL, and peer-SID tests for helper and daemon.
- Agent HTTPS/pinned-cert/token/protocol tests, plus fake-agent tests.
- Path translation/source-mode table tests, including `\\wsl$`, UNC/device
  rejection, case-insensitive containment, junctions, and symlink escapes.
- Agent-side source copy/checksum/ownership/rollback tests.
- WSL provisioning contract tests with fake `wsl.exe`.
- Agent port allocation and restart/reuse tests.
- Static/Node/PHP agent backend tests.
- WSL ingress target discovery/restart tests.
- Docker private-web-port renderer and service relay tests.
- Windows daemon proxy routing to agent backends and Docker web targets.
- Windows `.cmd` shim/shell/exec/build tests.
- WSL hosts/TLS/CA-bundle tests with fakes.
- Linux regression suite at the same commit.

The five Wave 1 lanes start concurrently from the frozen foundation. Lane D
owns separate agent Docker/relay/web packages implementing the frozen
`agentapi.Module` seam, so it does not edit Lane B's agent server. The
A→B→C→D→E merge order is a module-mounting dependency, not a serialized
development order.

### Manual Windows acceptance on a fresh Windows 11 machine

1. Install with only the documented Windows/WSL prerequisites; no manual WSL
   setup beyond prerequisites.
2. `devstack doctor` reports helper, CA, hosts, firewall, agent, Docker,
   localhost forwarding, and runtimes.
3. Create a static site from `C:\devstack\projects`; open trusted `.test`
   HTTPS from Windows.
4. Create a static site from WSL; open trusted `.test` HTTPS from Windows.
5. From inside WSL, `curl https://<site>.test` and
   `curl https://devstack.test` succeed in builtin mode.
6. Create PHP/Node sites from both source modes; verify extension isolation,
   FPM, WebSocket HMR, and the site terminal.
7. Install/activate nginx and MySQL; verify private-port Docker serving,
   config editing, credentials, phpMyAdmin, and service switching. Repeat
   with Apache and MariaDB.
8. Switch source mode for a stopped site; verify copy/checksums/ownership,
   source retention, and failure rollback.
9. Restart daemon, helper, WSL, and Windows; sites and the dashboard recover.
10. Change daemon HTTP/HTTPS/UI ports; helper targets and WSL ingress recover.
11. Upgrade; projects/state/VHDX survive and the rootfs/agent are replaced.
12. Uninstall; trust/hosts/firewall cleaned; projects and WSL data preserved
    by default.
13. Verify no devstack-created Windows name starts with a dot.
14. Verify a second local user cannot call the helper/daemon pipes.

### Linux regression gate

- Existing Linux full gate, PHP acceptance, Linux E2E, and Docker smokes
  remain green.
- Existing Linux installs are detected and not auto-migrated.
- New Linux installs use the `/data/devstack` layout.
- Upgrade from the released `v0.1.0` layout preserves config, state, CA,
  services, and projects.

## 12. Documentation deliverables

- This revised design.
- The revised implementation plan with contracts, Wave 0, lanes, and the
  expanded acceptance matrix.
- The 2026-10-27 review resolution record.
- Windows install/upgrade/uninstall, WSL layout, project modes, PhpStorm,
  trust, Docker, networking, troubleshooting, and global tooling docs.
- `.gitattributes` forcing LF for Linux scripts, Go, SQL, YAML, Makefile, and
  WSL scripts.
- README/handoff status updated to “Windows/WSL planned or in progress,”
  never “supported” until acceptance passes.
