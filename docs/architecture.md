# Current architecture

- **Status:** describes the implemented Linux M1 system, not the planned service
  platform.
- **Last reviewed:** 2026-10-25
- **Planned extension:** [service containers design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)

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

### Current Docker image

- The repository `Dockerfile` builds a Fedora 44 multi-stage image.
- The runtime container has no systemd, nginx, Apache, MySQL, PostgreSQL, or phpMyAdmin.
- The helper and daemon run in the container; loopback listeners are bridged to the
  container's public ports by `socat`.
- It is a **builtin-only** image. Native external nginx/Apache mode cannot work in the
  container layout.
- Current published host mappings:
  - `127.0.0.1:8080` → container 80
  - `127.0.0.1:8443` → container 443
  - `127.0.0.1:7070` → container 7070
- The Docker image is separate from the planned native service-container milestone.

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

Migrations live in `internal/store/migrations/`. The store is pure-Go
`modernc.org/sqlite`; no CGO is required.

### Managed hosts block

The helper writes one block to `/etc/hosts`:

```text
# BEGIN devstack
127.0.0.1 site.test
...
# END devstack
```

Unrelated hosts lines are preserved. Docker mounts the host file at `/host-hosts` and the
helper writes an in-place fallback for single-file bind mounts.

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

- PHP front controllers are currently hardcoded to `index.php` under the document root.
- The Go proxy resolves existing files first and routes other requests to `index.php`,
  preserving `REQUEST_URI`, query strings, method, and body.
- nginx and Apache native vhosts generate equivalent front-controller rules.
- Apache currently uses `AllowOverride None`; the planned service platform changes this
  to support project `.htaccess` in Apache mode.
- A configurable PHP front-controller filename and static SPA fallback filename are
  planned, not implemented.

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
- settings
- vhosts: status/mode/sync/preview

The dashboard is embedded from `ui/dist` and served by the daemon. API mutations use a
session cookie and CSRF token.

The planned service-platform routes (services/builds/credentials/terminal) are in the
design spec and are not implemented.

## 8. CLI surface (current)

```text
devstack init
devstack doctor [--repair] [--json]
devstack trust install|repair|remove|status
devstack daemon run|status|stop
devstack site add|list|show|start|stop|restart|remove|logs|open
devstack php --site <name> -- <args...>
devstack php <name> [version]
devstack php ini <name> ...
devstack php ext <name> ...
devstack runtime list|install|remove|migrate
devstack vhost status|use|sync|preview|path
devstack ui
devstack version
devstack uninstall --yes
```

There is no `devstack site update`, `devstack shell`, `devstack exec`, `devstack service`,
global PHP shim, Composer manager, build profile, or dashboard terminal in the current
code. These are planned.

## 9. Package map

| Package | Responsibility |
|---|---|
| `cmd/devstack` | CLI/daemon entrypoint |
| `cmd/devstack-helper` | privileged helper entrypoint |
| `internal/api` | HTTP API, dashboard hosting, session/CSRF |
| `internal/backend` | site execution backends and local process launch |
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
| `internal/phpfpm` | PHP-FPM process control |
| `internal/phpini` | generated php.ini rendering/validation |
| `internal/proxy` | host router, reverse proxy, PHP FastCGI proxy |
| `internal/runtime` | download, verify, extract, install/remove runtimes |
| `internal/sites` | site lifecycle, PHP/FPM generations, routes, vhosts |
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
- The Docker image is built locally and is not published to a registry.

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
- Database services are not part of the current product; the planned service platform
  binds them to loopback.

## 12. Current limitations

- Linux x86_64 only; Windows/WSL/macOS/arm64 are out of scope.
- External nginx/Apache mode requires the server to be installed and configured by the
  administrator; devstack does not install packages.
- Docker image is builtin-only and cannot run external nginx/Apache mode.
- The Docker image has no database/Redis/phpMyAdmin services.
- The Docker UI offers mode controls that can return a 7071/7070 validation error.
- Browser trust in the Docker path is manual.
- M1 manual Linux gates (real root 80/443 install, browser trust, live UI) were not fully
  run.
- The planned service platform, global PHP/Composer, build profiles, dashboard terminal,
  and `.htaccess` support are only in the design spec.

## 13. Where to go next

- For current behavior and commands: [install](install.md), [sites](sites.md),
  [PHP](php.md), [vhosts](vhosts.md), [TLS](tls.md).
- For the next milestone: [planned services](planned-services.md) and the
  [full design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md).
- For continuation state: [handoff](handoff.md).
