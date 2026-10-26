# Current architecture

- **Status:** describes the implemented Linux M1 system, Waves 0–2, and the next
  service-platform batch: config editing, performance apply/reset/benchmarks, site
  terminal, and editable listener ports. ImageMagick CLI is descoped; external-site
  Docker mounts and real host-tuning acceptance remain follow-ups.
- **Last reviewed:** 2026-10-27
- **Planned extension:** [service containers design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)
- **Planned Windows/WSL extension:** [Windows design, revised 2026-10-27](superpowers/specs/2026-10-26-windows-wsl-design.md)

## 1. Purpose

devstack is a local web-development platform for Linux. It creates and serves static,
Node, and PHP sites at `.test` hostnames with locally trusted TLS. It is designed to be
installed once by one user, then managed through a CLI and an embedded dashboard without
requiring Docker for the base product.

The base product **does not install packages**. It installs its own binaries and
privileged helper, downloads checksum-pinned PHP/Node runtimes, and can optionally
configure an already-installed native nginx or Apache.

## 2. Process model

```text
┌──────────────┐        Unix socket         ┌────────────────────────────────┐
│ devstack CLI │ ─────────────────────────► │ devstack daemon (user)         │
└──────────────┘                            │ API · UI · SQLite · proxy      │
        │                                   │ supervisor · runtime catalog   │
        │                                   └───────┬───────────────┬────────┘
        │                                           │               │
        │ browser                                   │               │
        ▼                                           │               ▼
  https://devstack.test                             │      native PHP-FPM / Node /
  (80/443 → helper → 8080/8443)                     │      static backend processes
                                                    │
                                    ┌───────────────▼────────────────┐
                                    │ devstack-helper (root)          │
                                    │ CA · trust · hosts · forwarding │
                                    │ native nginx/Apache vhosts      │
                                    └───────────────┬────────────────┘
                                                    │
                                      native nginx/Apache (optional)
```

### CLI — `devstack`

- Cobra-based command tree in `internal/cli`.
- Talks to the daemon over a Unix socket/client (`internal/client`).
- Can fall back to starting/stopping the daemon.
- Writes human tables or `--json`.
- Does not run as root. `install.sh` uses `sudo` only for helper files/units.

### Daemon — `devstack daemon run`

- Unprivileged per-user process.
- Owns the REST API, embedded dashboard, SQLite store, router/proxy, certificate
  store, runtime downloads, site supervisors, logs, and event stream.
- Listens by default on:
  - `127.0.0.1:8080` HTTP proxy
  - `127.0.0.1:8443` HTTPS proxy
  - `127.0.0.1:7070` dashboard
- Runs as a systemd user unit when available:
  `~/.config/systemd/user/devstack-daemon.service`.
- Can be run in the foreground for debugging:
  `devstack daemon run --log-level debug`.

### Helper — `devstack-helper`

- Root systemd service:
  `/etc/systemd/system/devstack-helper.service`.
- Socket: `/run/devstack-helper/helper.sock`, mode `0600`, peer UID checked against
  `allow_uid`.
- State: `/var/lib/devstack-helper`.
- Config: `/etc/devstack/helper.yaml`.
- Owns:
  - CA generation and certificate signing;
  - OS/browser trust-store changes;
  - managed `/etc/hosts` block;
  - privileged 80/443 forwarders;
  - native nginx/Apache vhost installation and reload;
  - uninstall cleanup.
- It accepts validated protocol messages only; it does not run arbitrary commands or
  read arbitrary paths.

## 3. Request paths

### Builtin mode (default)

1. Helper listens on 80/443 and forwards TCP to daemon proxy ports 8080/8443.
2. The daemon's host router looks up the site by `Host`.
3. Static sites and Node sites run as loopback HTTP backend processes on ports
   41000–42999; the Go reverse proxy forwards to them.
4. PHP sites run as per-site `php-fpm` masters on ports 43000–43999; the Go proxy speaks
   FastCGI directly.
5. The daemon serves the dashboard at 7070 (and optionally through the `devstack.test`
   vhost).

### Native external nginx/Apache mode

- The helper stops the builtin forwarders, generates native vhosts, tests the server
  config, reloads the server, and records the active mode.
- Static files are served directly by nginx/Apache.
- Node and PHP requests proxy/fastcgi to the same loopback backends.
- Only one native server can own 80/443. nginx and Apache cannot be active together.
- The native server must already be installed; devstack does not install it.

### Standalone Docker image (retired)

The old devstack-in-Docker image was removed on 2026-10-25. It is not part of the
current implementation. Docker remains a requirement only for the planned separate
service containers. There is no supported devstack application container in this
repository now.

## 4. State and storage

### Default native layout

`DEVSTACK_HOME` defaults to `~/.devstack`:

```text
~/.devstack/
  config.yaml          machine config
  state.db             SQLite state
  pki/                 native CA material
  runtimes/            installed PHP/Node builds
  sites/               generated per-site PHP/FPM configuration
  projects/            default managed project root
  services/            generated Compose, web/database config, containers' data/logs
  certs/               leaf certificates and keys
  secrets/             service credentials, mode 0700
  tools/               managed CLI tool roots
  config/cli/          global CLI configuration
  logs/                site and daemon logs
  run/                 daemon socket, pid files, runtime state
  cache/               downloads/cache
  backups/             pre-migration and operational backups
```

Site project files are **not** stored in `sites/` today. Users create sites from arbitrary
paths; the generated per-site configuration is under `sites/`.

### SQLite tables (current)

- `sites`
- `runtimes`
- `certs`
- `settings`
- `events`
- `php_defaults`
- `site_php_config`
- `services` (catalog-backed lifecycle/state for the six Wave 1 service containers)

Site rows also carry the front-controller, `.htaccess`, SPA-fallback, and build profile
columns. Migrations live in `internal/store/migrations/`. The store is pure-Go
`modernc.org/sqlite`; no CGO is required.

### Managed hosts block

The helper writes one block to `/etc/hosts`:

```text
# BEGIN devstack
127.0.0.1 site.test
...
# END devstack
```

Unrelated hosts lines are preserved.

### Service containers (Wave 1)

- The embedded catalog pins nginx, Apache httpd, MySQL, MariaDB, PostgreSQL, Redis, and
  phpMyAdmin 5.2.3 by digest. ImageMagick CLI is descoped; PHP `imagick` remains.
- phpMyAdmin is a standalone loopback:8082 cookie-auth container that follows the active
  MySQL/MariaDB engine through the fixed `db` Compose alias; activating it without an
  active MySQL family is rejected.
- Credentials can be rotated for MySQL/MariaDB/PostgreSQL through the helper; the account
  update runs on stdin, the host secret is owner-preserving 0600, and active phpMyAdmin is
  restarted after rotation.
- A pure `internal/performance` engine computes bounded nginx/Apache/PHP/host values and
  apply/reset/benchmark plans; host application and dashboard wiring are pending.
- The unprivileged daemon owns the service manager/API/CLI and writes database
  config/credentials under the root. The root helper owns the fixed Compose project:
  install/pull, exclusive activation, health/port checks, rollback, restart,
  validate/reload, logs, and backup-verified data deletion.
- MySQL/MariaDB are exclusive; PostgreSQL and Redis are independent. Data services bind
  `127.0.0.1`; web containers use Docker host networking for 80/443.
- The active Docker nginx/Apache owns serving through `/sites`, `/certs`, and generated
  config. `serving_provider=docker` is distinct from the legacy native provider; switching
  to builtin stops the container and restores the proxy/forwarders.
- Service credentials are random, mode 0600, revealed only through explicit API/CLI; the
  `devstack` database accounts have admin DDL grants.
- Managed Composer and global PHP/Composer shims live under `tools/` and `/usr/local/bin`;
  static builds run as the user with a selected Node runtime.

## 5. TLS and trust

- The helper owns the local CA private key; it never leaves the helper/root state.
- The daemon requests certificates through the helper for validated `.test` SANs.
- Leaf certificates and keys are stored under the devstack PKI area and used by the
  proxy or copied into the native vhost TLS directory.
- `devstack init` / `devstack trust repair` install the root CA into:
  - the OS trust store;
  - Chrome/Chromium NSS where available;
  - Firefox profiles where available.
- HSTS is deliberately never emitted.
- Browser trust is a host concern. A container’s CA is not automatically trusted by the
  host browser.

## 6. Runtimes and sites

### PHP

- PHP 8.2–8.5 are checksum-pinned builds in the embedded runtime catalog.
- Each site pins a PHP line and exact build.
- Each running site gets its own native `php-fpm` master on a loopback FastCGI port.
- Per-site generated `php.ini`, extension toggles, OPcache/Xdebug settings, and rollback
  are supported.
- The `imagick` extension is bundled with the PHP build, including its ImageMagick 6
  libraries; no host ImageMagick library is needed for PHP.
- `devstack php --site <name> -- <args...>` runs the site’s PHP CLI with the site ini.

### Node

- Node LTS builds are checksum-pinned and installed under `runtimes/`.
- A Node site runs a user-defined command; the daemon supervises it and proxies HTTP and
  WebSocket traffic.
- Node package managers are the project’s responsibility; devstack provides the Node
  runtime.

### Static

- A static site runs a loopback static file server.
- `--spa` serves `index.html` for missing non-file routes; missing asset paths remain
  404.
- Directory listings are never generated.
- Dotfiles and source/backup files are blocked.

### Front controllers

- Builtin/native front controllers default to `index.php`; the site model supports a
  configurable relative PHP controller file, mode (`auto|generated|htaccess`), Apache
  override policy, and static SPA fallback file.
- Docker nginx/Apache vhosts generate matching rules; Apache honors project `.htaccess`
  and `AllowOverride` for Docker sites. The legacy native provider still uses its existing
  `AllowOverride None` behavior.
- The Go proxy resolves existing files first and routes other requests to the configured
  controller, preserving `REQUEST_URI`, query strings, method, body, and PATH_INFO.
- Static SPA fallback is separate from the PHP controller and never swallows missing
  asset requests.

## 7. API surface (current)

The daemon exposes the following route groups under `/api/v1`:

- session/health
- sites: list/create/get/update/delete/start/stop/restart
- PHP: info/apply/exec/extensions/defaults/migrate
- logs/event stream
- runtimes: list/available/install/remove
- trust: status/repair/remove
- doctor: status/run/repair
- filesystem browser: directory listing only
- settings: read and PATCH listener ports (`http_port`, `https_port`, `ui_port`)
- vhosts: status/mode/sync/preview
- services: list/get, install/activate/deactivate/restart/uninstall, logs, credentials
  reveal/rotate
- service configuration: list/content/diff/apply/reset under
  `services/<id>/config`
- performance: status/profile/preview/apply/reset and measured benchmark history
- terminal: one-use ticket issuance plus ticket-authenticated WebSocket PTY
- tooling: PHP default/ini/status and managed tool install/remove
- builds: run a site build and read build logs

The dashboard is embedded from `ui/dist` and served by the daemon. API mutations use a
session cookie and CSRF token.

The dashboard exposes service lifecycle/credential/phpMyAdmin actions, service
configuration editing/diff/apply/reset, performance preview/apply/reset/benchmarks,
build profile/run/log panels, grouped service cards, global PHP/Composer/tool management,
editable listener ports, and the site-scoped terminal.

## 8. CLI surface (current)

```text
devstack init
devstack doctor [--repair] [--json]
devstack trust install|repair|remove|status
devstack daemon run|status|stop|restart
devstack site add|list|show|start|stop|restart|remove|logs|open|php|ini|ext
devstack php --site <name> -- <args...>
devstack php <name> [version]
devstack php ini <name> ...
devstack php ext <name> ...
devstack runtime list|install|remove|migrate
devstack vhost status|use|sync|preview|path
devstack service list|status <id>|install|activate|deactivate|restart|uninstall|logs|credentials
devstack service config list|show|diff|apply|reset <id> ...
devstack shell [--site <name>]
devstack exec [--site <name>] -- <command...>
devstack shell-init [--site <name>]
devstack site build <name>
devstack site update <name> ...
devstack php default <line|build>|--status|--clear
devstack tool list|install|remove|update
devstack ui
devstack version
devstack uninstall --yes
```

The dashboard service page supports lifecycle/credential actions, service configuration,
performance, build profiles, tooling, and grouped status. Site detail includes the
site-scoped terminal. CLI/API build, shell, credentials, configuration, performance, and
service lifecycle paths exist.

## 9. Package map

| Package | Responsibility |
|---|---|
| `cmd/devstack` | CLI/daemon entrypoint |
| `cmd/devstack-helper` | privileged helper entrypoint |
| `internal/api` | HTTP API, dashboard hosting, session/CSRF |
| `internal/backend` | site execution backends and local process launch |
| `internal/build` | native static-build executor, output validation/snapshot/logs |
| `internal/credentials` | random service credentials, 0600/0700 storage, path guards |
| `internal/dockerweb` | Docker nginx/Apache vhost renderer, config sync, provider switch |
| `internal/catalog` | embedded Node/PHP runtime catalog and PHP extension manifest |
| `internal/certs` | CA/leaf certificate issue, verify, renew, delete |
| `internal/cli` | Cobra command tree |
| `internal/client` | daemon client |
| `internal/config` | machine config and directory layout |
| `internal/daemon` | service wiring and startup/shutdown |
| `internal/doctor` | diagnostics and repair checks |
| `internal/domain` | hostname validation |
| `internal/fcgi` | FastCGI transport |
| `internal/forwarder` | privileged 80/443 loopback forwarders |
| `internal/helper` | root operations and vhost manager |
| `internal/helperclient` | helper RPC client |
| `internal/helperproto` | helper wire types and errors |
| `internal/hosts` | managed hosts block |
| `internal/logs` | log stores/streams/redaction |
| `internal/model` | domain models |
| `internal/performance` | bounded nginx/Apache/PHP/host profile engine, measured load generator, plans |
| `internal/performance/service` | profile/host-tuning/apply/reset/benchmark orchestration |
| `internal/phpfpm` | PHP-FPM process control |
| `internal/phpini` | generated php.ini rendering/validation |
| `internal/proxy` | host router, reverse proxy, PHP FastCGI proxy |
| `internal/runtime` | download, verify, extract, install/remove runtimes |
| `internal/serviceconfig` | config manifest, conflict detection, diff, backups |
| `internal/servicecontrol` | service lifecycle manager, preparers, config service, Docker/web switches |
| `internal/services` | service catalog, Compose rendering, state views |
| `internal/sites` | site lifecycle, PHP/FPM generations, routes, vhosts |
| `internal/terminal` | ticket-authenticated site PTY sessions and WebSocket protocol |
| `internal/tooling` | global PHP/Composer shims, site shell/exec environment |
| `internal/staticserve` | safe static file serving and SPA fallback |
| `internal/store` | SQLite persistence and migrations |
| `internal/supervisor` | child process supervision |
| `internal/testcert` | test certificates |
| `internal/trust` | OS/browser trust stores |
| `internal/vhost` | native nginx/Apache rendering |
| `internal/version` | version metadata |
| `ui/` | Svelte 5 dashboard |
| `build/php/` | PHP build/smoke/catalog scripts |
| `test/` | E2E, release, PHP, and acceptance tests |

## 10. Build and release

- Go module: `github.com/AramZahedi/dev-web-stack`; Go 1.26.
- UI: Node 22/npm 10, Svelte/Vite, embedded into the daemon.
- `make build` builds UI then both binaries.
- Release archives are unsigned Linux amd64 tarballs with SHA-256 checksums.
- `install.sh` installs from source, a local archive, or a published release.
- Helper/system units and a per-user daemon unit are installed by the installer.
- The app release `v0.1.0` and PHP runtime release `php-runtimes-2026-10-25` are
  published.

## 11. Security model (current)

- Daemon is unprivileged; helper is root.
- Helper peer UID is checked; only the installing UID may call it.
- Helper protocol is versioned and validates site/vhost data.
- No arbitrary shell commands or file paths cross the helper boundary.
- CA private key is helper-owned.
- Site paths are validated and symlink-escaped paths are rejected.
- Static serving uses `os.Root` containment.
- PHP source/backup files and dotfiles are denied.
- HSTS is stripped even if an upstream sends it.
- `/api/v1/fs` directory browsing requires an active dashboard session, fails closed
  when the user home cannot be resolved, and is confined to the user's home directory.
- Client `Proxy` headers are stripped as hop-by-hop headers before FastCGI/CGI dispatch.
- The helper's `validateBackendOwnership` requires vhost backend paths to be owned by
  `allow_uid` (or located under the allowed home) before installing native vhosts.
- Service containers run from a digest-pinned catalog; requests carry only catalog IDs
  and validated options. No service container receives the Docker socket.
- Database/Redis/phpMyAdmin ports bind `127.0.0.1`. Web containers use host networking
  only for 80/443.
- Service credentials are 0600/0700, revealed only by an audited explicit action, and
  redacted from logs/list responses. Docker web files and certificates are mounted
  read-only; generated config uses conflict/backup rules.
- Native CA/trust remains host-only; containers receive leaf certs under `/certs`.
- Shell/exec and builds run as the installing user, never root; build output is confined
  to the project root and snapshotted/restored on failure.
- Service config access is confined to `services/<id>/config`, rejects symlinks and
  sensitive/reserved names, caps files at 1 MiB, and validates/reloads through catalog
  commands with file+manifest rollback. File contents require the dashboard session;
  list metadata remains a read-only API.
- Performance host tuning uses a fixed-target helper RPC with typed values, fixed
  `/etc/sysctl.d` and `/etc/security/limits.d` paths, backups, verification, and rollback.
  No host path or shell fragment comes from the API.
- Terminal tickets are short-lived, one-use, site/UID-bound, and Origin/Host checked;
  sessions run as the installing user with no root, sudo, or Docker socket, kill the PTY
  process group on disconnect, and cap sessions/output/idle time.

## 12. Current limitations

- Linux x86_64 is the implemented platform. Windows 11 + WSL2 has a revised
  design and plan (2026-10-27) but is not implemented; see the
  [Windows design](superpowers/specs/2026-10-26-windows-wsl-design.md) and
  [review resolution](superpowers/status/2026-10-27-windows-plan-review-resolution.md).
  macOS and arm64 remain out of scope.
- External nginx/Apache mode requires the server to be installed and configured by the
  administrator; devstack does not install packages.
- M1 manual Linux gates (real root 80/443 install, browser trust, live UI) were not fully
  run.
- Waves 0–1, Wave 2, and the next service-platform batch are implemented.
- Still pending: external-site Docker mounts, real root host-tuning acceptance, and
  live Wave 3 benchmark evidence. Per-site PHP-FPM pool sizing and OPcache/realpath ini
  values are persisted and applied through the site validate/swap flow.
- Docker web servers currently serve only sites under the managed `projects/` root.
- The official MySQL entrypoint can retain password values in the container process
  environment; Compose files/logs do not contain them.

## 13. Where to go next

- For current behavior and commands: [install](install.md), [sites](sites.md),
  [PHP](php.md), [vhosts](vhosts.md), [TLS](tls.md).
- For the next milestone: [planned services](planned-services.md) and the
  [full design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md).
- For continuation state: [handoff](handoff.md).

## 14. Planned Windows/WSL architecture (not implemented)

The approved Windows design keeps Windows in charge of the public entry and
host integration, and uses a managed Debian 12 WSL2 agent for Linux site
execution:

- Windows helper owns 80/443 and always forwards to the Windows daemon proxy.
- The Windows daemon is the single public HTTP/TLS router, dashboard host, and
  route table. It binds loopback plus the WSL host adapter; the WSL-facing
  listener is firewall-restricted.
- The WSL agent runs a fixed 80/443 ingress to the Windows daemon. WSL
  `/etc/hosts` maps `.test` names to `127.0.0.1`.
- Windows-mode and WSL-mode site backends run in the agent. The agent allocates
  WSL ports; the daemon persists and routes them.
- WSL ext4 is canonical for generated site/service data, secrets, runtimes,
  logs, and Docker data. Windows keeps SQLite metadata, config, leaf certs,
  daemon logs, cache, and the VHDX.
- Docker web servers in WSL use private HTTP ports; the daemon owns public TLS
  and the dashboard route.
- The agent API uses HTTPS with a pinned certificate and bearer token. Helper
  and daemon control pipes are ACL-restricted and SID-checked.
- Global PHP/Composer, shells, and builds run in WSL; Windows gets `.cmd` shims.

See the [Windows design](superpowers/specs/2026-10-26-windows-wsl-design.md) and
[revised implementation plan](superpowers/plans/2026-10-26-windows-wsl.md). Do
not treat this section as implemented behavior.
