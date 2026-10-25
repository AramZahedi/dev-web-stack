# Planned service platform

> **Implementation status (2026-10-26):** Waves 0–2 and the next service-platform batch
> are implemented: six pinned service containers, phpMyAdmin, Docker nginx/Apache,
> database credentials/rotation/admin bootstrap, global PHP/Composer, site shell/exec,
> static builds, service configuration editing, performance preview/apply/reset and
> measured benchmarks, site-scoped terminal, editable listener ports, and the dashboard
> surfaces. ImageMagick CLI is descoped. Remaining: external-site Docker mounts, real
> root host-tuning/benchmark acceptance, and merged PHP-FPM pool sizing. This document
> remains the product overview, not a promise that every host gate is passed.

- **Status:** Waves 0–2 and the next batch implemented; remaining host gates tracked in known issues.
- **Design status:** Implemented for Waves 0–2 and the next batch.
- **Detailed specification:** [service containers, editable config, and throughput](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)
- **Current implemented architecture:** [architecture](architecture.md)

This document is the readable overview. The design spec is authoritative for details.

## 1. What changes

The next milestone turns devstack into a native devstack installation with optional
Docker-backed infrastructure services:

```text
Native host
├── devstack CLI / daemon / helper
├── PHP-FPM and Node site backends
├── native CA, trust, hosts, runtime catalog
└── Docker service layer
    ├── nginx OR apache        (one active)
    ├── mysql OR mariadb       (one active)
    ├── postgresql
    ├── redis
    └── phpmyadmin             (loopback :8082)
```

The base product remains native. Docker is not required for builtin serving or PHP/Node
sites.

## 2. Service matrix

| Service | Group | Port | Notes |
|---|---|---|---|
| nginx | web | 80/443 | Host network; only one web server active |
| apache | web | 80/443 | Host network; `.htaccess` supported |
| mysql | mysql-family | 3306 | Loopback only |
| mariadb | mysql-family | 3306 | Loopback only; separate data from MySQL |
| postgresql | standalone | 5432 | Loopback only |
| redis | standalone | 6379 | Loopback only; no password by default |
| phpmyadmin | standalone | 8082 | Follows active MySQL family; cookie login |

Every service has `installed` and `active` states. Installing does not activate.
Activation is transactional and rolls back on failure.

## 3. Host layout

Recommended, configurable root:

```text
/data/devstack/
  config.yaml
  state.db
  projects/                 # site files
  sites/                    # existing generated per-site configs
  certs/                    # leaf certs
  pki/                      # native CA, root-only key
  secrets/                  # DB credentials
  services/
    compose.yaml
    nginx/{config,logs}
    apache/{config,logs}
    mysql/{config,data}
    mariadb/{config,data}
    postgresql/{config,data}
    redis/{config,data}
    phpmyadmin/config
  runtimes/
  logs/
  backups/
```

Existing `~/.devstack` installs are not moved automatically.

## 4. Configuration model

Three classes:

1. **Base service config** — seeded, then user-owned. Never silently regenerated.
2. **Generated site vhosts** — derived from site state. Editable, but conflict-protected.
3. **Custom includes** — always user-owned.

When a generated file changed locally and devstack needs to regenerate it:

- the site mutation is blocked;
- a diff is shown;
- user chooses keep/detach, use generated, merge, or cancel;
- edits are backed up before replacement;
- configs are validated in-container before apply/reload.

Apache `.htaccess` is project-owned; `AllowOverride All` is the compatibility default.
Front-controller modes are `auto`, `generated`, and `htaccess`. The PHP front-controller
file is configurable; static SPA fallback is a separate configurable file.

## 5. Performance

Resource-aware throughput profile:

- nginx: worker processes/connections/fd limits, keepalive, sendfile,
  `open_file_cache`, reuseport, version-aware HTTP/2; no forced `use epoll`.
- Apache: MPM event only, computed `MaxRequestWorkers`/`ServerLimit`/threads, keepalive,
  FastCGI rather than `mod_php`.
- PHP-FPM: global memory/CPU budget divided across active PHP sites; OPcache/realpath.
- Host tuning: file descriptors, somaxconn, netdev backlog, ephemeral port range; helper
  applies with backup and `devstack performance reset`.
- Benchmark suite: nginx/Apache static, PHP, Node, HTTP/HTTPS; results recorded. No fixed
  RPS CI gate.
- `.htaccess` has a measured per-directory overhead; Apache `none`/`fileinfo` can be
  selected for performance-only sites.

## 6. Front controllers and builds

### PHP/Laravel

```sh
devstack site add \
  --name app \
  --kind php \
  --path /data/devstack/projects/app \
  --document-root public \
  --front-controller \
  --php 8.4
```

- Existing static files are served directly.
- Missing files route to `public/index.php`.
- Original URI/query/method/body preserved.
- `front_controller_file` can be `index.php` or a custom `.php`.
- Apache `.htaccess` is honored.
- Docker Apache receives a live Laravel acceptance test through `mod_proxy_fcgi`.

### Static SPA / Nuxt

```sh
devstack site add \
  --name app \
  --kind static \
  --path /data/devstack/projects/app \
  --document-root .output/public \
  --spa \
  --build-node 22 \
  --build-command "npm ci && npm run generate"
devstack site build app
```

- Nuxt 3 output is usually `.output/public`; Nuxt 2/Vite often `dist`; Next export `out`.
- Output is explicit; no guessing.
- `--spa` returns `index.html` for unknown non-file routes; missing assets stay 404.
- A custom `spa_fallback_file` is supported.
- Static output is served without a Node process.
- Nuxt SSR uses a Node site running `.output/server/index.mjs`.

## 7. Global PHP and Composer

- Global `/usr/local/bin/php` shim to the selected PHP build.
- Global `/usr/local/bin/composer` wrapper around a pinned Composer PHAR.
- Global CLI ini at `/data/devstack/config/cli/php.ini`.
- `devstack php default 8.4` or an exact build.
- Outside a site shell, bare `php`/`composer` use the global default.
- Inside `devstack shell`/`devstack exec --site`, site PHP/ini/extensions win.
- Existing `/usr/local/bin/php` or `composer` detected and replaced only with explicit
  confirmation.
- Dashboard Settings can set the default and edit the global CLI ini.

## 8. Database credentials

Per-engine random credentials, no shared `root`/`root`:

```text
secrets/mysql/root_password
secrets/mysql/devstack_password
secrets/mariadb/root_password
secrets/mariadb/devstack_password
secrets/postgresql/postgres_password
secrets/postgresql/devstack_password
```

- MySQL/MariaDB root is local/container-only; phpMyAdmin uses `devstack`.
- PostgreSQL superuser is `postgres`; `devstack` role/database.
- Redis no auth by default on loopback.
- Credentials reveal/copy/rotate from CLI and dashboard; never in logs/list responses.
- Data deletion backs up and verifies first.

## 9. Dashboard parity

The dashboard can perform every operational task:

- create/edit sites, document root, SPA/front-controller modes, `.htaccess`;
- build profiles, build action/logs;
- site terminal/command runner with site PHP/Node context;
- install/activate/deactivate services;
- config editor/diff/apply/reset;
- credential reveal/copy/rotate;
- phpMyAdmin launch;
- Composer/PHP tooling;
- performance apply/reset/benchmark;
- trust/Doctor repair and logs.

CLI remains an alternative interface to the same API.

## 10. Security model

- Helper runs root; daemon unprivileged.
- Helper controls only catalog-defined Compose operations.
- No Docker socket in service containers.
- Images pinned by digest.
- DB/Redis/phpMyAdmin bind loopback.
- No arbitrary image/command/path/port from users.
- CA private key never in containers.
- Browser trust never modified by containers.
- Config edits validated before activation.
- Dashboard terminal runs as user, site-scoped, no sudo, localhost-only by default.
- Uninstall preserves data unless explicitly deleted after backup.

## 11. Compatibility

- Builtin Go proxy remains.
- Native external nginx/Apache provider remains (manually installed).
- New Docker service activation uses `serving_provider=docker`.
- Existing `~/.devstack` continues.
- No automatic root migration.
- The old standalone devstack Docker image is retired; Docker is reserved for service containers.

## 12. Suggested delivery order

1. Root layout, service catalog/state machine, helper Compose RPC.
2. Database/Redis/phpMyAdmin containers and credential storage.
3. nginx/Apache containers and full vhost parity.
4. Config ownership/editor/conflict flow and validation.
5. Native CA mounts and trust verification.
6. Performance profile, host tuning, PHP-FPM budgets, benchmarks.
7. `devstack shell`, global PHP/Composer tooling. ImageMagick CLI is descoped and has no catalog entry, helper RPC, wrapper, or dashboard row.
8. Dashboard UI, terminal, builds, CLI parity.
9. Acceptance: Laravel/Apache `.htaccess`, Nuxt build, credentials, config conflicts,
   service switching, performance, security.

## 13. What is not part of this milestone

- Windows/WSL/macOS/arm64.
- Native service packages/systemd units.
- Production DB HA/backups/replication.
- Arbitrary Compose/custom containers.
- Devstack-in-Docker service platform.
- General source-code IDE in the dashboard.
- Automatic `~/.devstack` migration.

## 14. Open implementation choices

See [decisions](decisions.md) and design §20:

- image tags/digests;
- Apache/nginx wrapper Dockerfiles;
- resource formulas/caps;
- benchmark workload;
- phpMyAdmin config mechanism;
- legacy provider UI amount;
- Redis optional password;
- Composer version/digest.
