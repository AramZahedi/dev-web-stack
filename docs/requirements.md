# Requirements and user concerns

- **Last reviewed:** 2026-10-26
- **Status labels:** **Implemented** = in the current code/release; **Partial** = a working
  core exists but a named follow-up remains; **Planned** = in the design but not built;
  **Deferred** = explicitly out of scope.
- **Detailed design:** [service containers and performance](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)

## 1. Product intent

devstack is a local development platform for a single Linux user. It should make local
sites available at trusted `.test` hostnames, manage PHP/Node runtimes, and eventually
manage optional infrastructure services without requiring the user to hand-edit Docker or
system service configuration.

The base application is installed natively. Docker is an optional tool for service
containers, not the required runtime for devstack itself.

## 2. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| R1 | Linux x86_64, systemd-based native install | Implemented |
| R2 | Static, Node, and PHP sites with `.test` HTTPS | Implemented |
| R3 | Per-site PHP version/build, ini, extensions | Implemented |
| R4 | Per-site Node command and process supervision | Implemented |
| R5 | Builtin Go proxy plus native nginx/Apache modes | Implemented |
| R6 | Local CA, OS/browser trust, managed hosts block | Implemented |
| R7 | CLI, embedded dashboard, daemon, privileged helper | Implemented |
| R8 | Optional Docker tools: nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis, phpMyAdmin | Implemented — ImageMagick CLI descoped |
| R9 | One active web server and one active MySQL-family service at a time | Implemented |
| R10 | Host-owned config/data/certs at a single root | Implemented |
| R11 | Editable configs with conflict/diff protection | Implemented — safe config API/CLI/UI, diff/apply/reset, keep/use/merge, validation/reload rollback |
| R12 | Native CA/trust even when tools run in containers | Implemented — leaf certs mounted, CA host-only |
| R13 | phpMyAdmin exposed on a localhost port | Implemented — loopback 8082, cookie auth, active DB follow |
| R14 | Random per-engine database credentials | Implemented — generate/reveal/rotate, admin grants, owner-preserving 0600 |
| R15 | Native PHP `imagick` (ImageMagick CLI descoped) | Implemented — PHP `imagick` bundled; Docker CLI removed from product scope |
| R16 | Configurable PHP front-controller file and mode | Implemented |
| R17 | Apache project `.htaccess` support | Implemented — Docker Apache |
| R18 | Nuxt/Vite static-build profiles and SPA fallback | Implemented — native build executor |
| R19 | Full dashboard parity with CLI | Implemented — service actions/credentials, config, performance, benchmarks, terminal, build/tooling/settings |
| R20 | Global `php` and `composer`, with a machine-wide PHP CLI default | Implemented |
| R21 | Auto-tuned nginx/Apache/PHP performance and host tuning | Partial — preview/apply/reset, live per-site PHP-FPM pooling, fixed host tuning, and measured benchmarks are implemented; real root host tuning remains a gate |
| R22 | Project build/benchmark/terminal workflows in the dashboard | Implemented — build, performance benchmark history, and site terminal |
| R23 | Existing native external nginx/Apache mode remains available | Required compatibility |
| R24 | No automatic root migration from `~/.devstack` | Planned decision |
| R25 | Keep work committed/pushed and CI green | Process |
| R26 | Windows 11 native daemon/helper with managed WSL2 agent and Docker services | Planned — design and five-lane plan written; not implemented |

## 3. Hard constraints

1. **Native devstack.** The base product must run on the host, not inside a container.
   Docker is used only for optional service containers.
2. **No native package installation for services.** The new service platform must not
   run `dnf`/`apt` or manage service systemd units for nginx, Apache, databases, Redis, or
   phpMyAdmin. Devstack installs its own binaries and native PHP/Node runtimes only.
3. **Separate service containers.** Every optional infrastructure service is its own
   container. No all-in-one tools container.
4. **Host bind mounts.** Configuration, site files, database data, certificates, secrets,
   and backups live on the host under the configurable devstack root.
5. **One active member per conflict group.** nginx/Apache cannot both own 80/443;
   MySQL/MariaDB cannot both own 3306. PostgreSQL and Redis are independent.
6. **Loopback-only data services.** MySQL, MariaDB, PostgreSQL, Redis, and phpMyAdmin bind
   to `127.0.0.1` by default.
7. **No Docker socket in service containers.** The privileged helper controls a fixed
   Compose project; the daemon remains unprivileged.
8. **Native CA.** Certificates are issued and trusted on the host. Containers receive
   leaf certificates only.
9. **No silent config overwrite.** Generated files are conflict-protected.
10. **No unbounded performance values.** Compute from resources and benchmark; host
    tuning must be reversible.
11. **Existing installs remain valid.** `~/.devstack` installs and native external modes
    continue to work.
12. **M1 platform scope.** Linux only today. Windows 11 + WSL2 is planned in
    [the Windows design](superpowers/specs/2026-10-26-windows-wsl-design.md); no Windows
    support is claimed until its acceptance plan passes.
13. **No invented runtime data.** PHP/Node catalog entries must correspond to real
    published checksums and sizes.
14. **Dashboard-first usability.** A user must be able to perform every operational task
    without opening a terminal, aside from editing application source code.

## 4. User requirements and concerns recorded during design

### 4.1 Environment and installation

- The project is meant to be installed locally on the user's machine, not only in Docker.
- Docker is acceptable for optional tools, but the devstack application itself remains
  native.
- Prefer one root directory, recommended `/data/devstack`, for all project/tool state.
- Configs and data should be host files, not hidden inside container images.
- `/data/wwwroot`-style paths are acceptable but should be configurable.
- The old standalone devstack Docker image was retired; Docker is reserved for optional service containers.

### 4.2 Sites and serving

- Static, PHP, and Node sites must remain simple to create.
- PHP sites need per-project PHP version/build, generated `php.ini`, and extension
  choices.
- Laravel-style applications must route all non-file traffic through their front
  controller.
- Apache projects must honor `.htaccess`; the earlier `AllowOverride None` assumption was
  explicitly rejected.
- Front-controller filename should be configurable.
- Static SPAs may need an `index.html` fallback; it should be configurable and must not
  swallow missing assets.
- Nuxt/Vite builds produce static output, often `.output/public` or `dist`, and must be
  servable without a Node server at runtime.
- Nuxt SSR should use a Node site with the generated server command.
- nginx and Apache must both be installable, but only one may be active.
- Existing native external serving must not be broken by the new Docker service layer.

### 4.3 Configuration

- Users must be able to edit every service config.
- Generated site vhosts must be protected from silent replacement.
- Config conflict UI must support keep mine/detach, use generated, merge, and cancel.
- Base configs are user-owned after seeding.
- Custom includes are never overwritten.
- `.htaccess` is project-owned and must be editable from the dashboard.
- Config changes must validate before reload/restart and roll back on failure.
- Performance settings must be separate from user-owned base config where possible.

### 4.4 Certificates

- The CA and trust installation must happen on the native machine.
- Container replacements must not affect the CA or browser trust.
- Containers must never receive the CA private key.
- Browser/OS trust must be explicit and diagnosable; not silently assumed.

### 4.5 Databases and credentials

- Databases are optional tools; they are not part of M1.
- Each engine gets separate random credentials; no shared `root`/`root`.
- MySQL/MariaDB root is local/container-only; phpMyAdmin uses a `devstack` admin account.
- PostgreSQL has a `postgres` superuser and a `devstack` role/database.
- Redis has no password by default on loopback; optional later.
- Credentials must be revealable/copyable/rotatable from the dashboard and CLI.
- Credentials must never appear in logs, list responses, or page HTML until revealed.
- Data deletion must back up first and be explicitly confirmed.

### 4.6 Performance

- Optimize nginx and Apache for maximum safe local requests/sec.
- Generate settings from CPU/RAM/fd limits, not fixed maximum constants.
- Optimize PHP-FPM as part of the budget; PHP can be the bottleneck.
- Host-level tuning is wanted, including file descriptors, backlog, and port range.
- Host tuning must be applied by the helper with backup and reset.
- Use automatic epoll selection; do not force `use epoll`.
- Use version-aware nginx HTTP/2 syntax.
- Benchmark rather than claiming a fixed RPS number.

### 4.7 Developer experience

- `php artisan` and Composer must use the project’s PHP version, ini, and extensions when
  run in the project context.
- `devstack shell` and `devstack exec` are the explicit project context.
- Global `php` and `composer` must work machine-wide with a configurable PHP CLI default.
- Global CLI default should use a generated global ini; Composer must use it.
- Site shells override the global default so per-project versions remain correct.
- Composer is installed globally as a managed native PHAR.
- Nuxt/build commands should be runnable natively with the site’s Node runtime.
- All operational tasks should be doable from the dashboard.

## 5. Dashboard requirements

The dashboard must expose:

- site create/edit, document root, SPA fallback, front-controller file/mode, Apache
  override policy, `.htaccess` editor;
- build profile, Build action, build logs;
- start/stop/restart/open;
- service install/activate/deactivate/uninstall;
- service config editor/diff/apply/reset;
- performance preview/apply/reset/benchmark history and site terminal;
- credentials reveal/copy/rotate;
- phpMyAdmin launch;
- Composer tool management;
- performance apply/reset/benchmark;
- site terminal/command runner with site PHP/Node context;
- editable listener ports with restart-required handling;
- Doctor/trust repair;
- logs and health.

The CLI remains an alternative interface, not a prerequisite.

## 6. Explicit non-goals

- Windows, WSL, macOS, arm64, or multi-user shared workstations.
- Native packages/systemd for optional services.
- Running nginx and Apache simultaneously.
- Running MySQL and MariaDB simultaneously.
- Production DB replication, HA, major-version migration, or scheduled backups.
- Arbitrary user-supplied Compose files, images, commands, ports, or bind mounts.
- Running devstack itself in Docker as the service-platform target.
- General IDE/code editing in the dashboard.
- Absolute RPS guarantees across hardware.
- Auto-updating devstack, signing releases, diagnostics bundles, or arm64 in this
  milestone.

## 7. Acceptance criteria for the planned service platform

- Dashboard and CLI can install/activate both web servers and both MySQL-family engines,
  with strict conflict enforcement.
- Services have host-owned config/data and survive container recreation.
- Native CA trust remains valid across service container changes.
- A Laravel fixture works through nginx and Apache, including `.htaccess`, deep routes,
  POST, static assets, query strings, and framework 404s.
- A Nuxt/Vite fixture can be built from the dashboard and served from its output
  directory.
- Global `php`/`composer` use the selected CLI default outside site shells; site shells
  use the site PHP.
- Config edit/conflict/apply/rollback works without CLI.
- Credentials can be revealed/rotated from the dashboard without leaking into logs.
- Throughput profile passes config validation and hosts a benchmark; reset restores host
  settings.
- Existing M1 site/runtime/release tests remain green.

## 8. Traceability

| Requirement area | Detailed location |
|---|---|
| Service topology/state | design §5, §7–§8 |
| Filesystem/config/conflicts | design §6, §10 |
| Web servers/vhosts/front controller/.htaccess | design §9 |
| CA/trust | design §11 |
| Performance | design §12 |
| API/CLI/dashboard | design §13 |
| Security | design §15 |
| Compatibility | design §16 |
| Tests/acceptance | design §17 |
| Builder review resolutions | design §19 |
| Current implementation | [architecture](architecture.md) |

## 9. Open product decisions

These are tracked in the design’s “Open Builder Decisions” section; they do not change the
architecture unless the user overrides them:

- exact image tags and digests;
- exact Apache/nginx wrapper image contents;
- exact worker/MPM/PHP formulas and caps;
- benchmark concurrency and workloads;
- phpMyAdmin configuration mechanism;
- how much legacy native provider UI remains;
- optional Redis password;
- exact Composer version and PHAR digest.
