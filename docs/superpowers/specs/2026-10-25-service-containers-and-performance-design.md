# dev-web-stack — Managed Service Containers, Editable Configuration, and Throughput: Design

- **Status:** Approved for implementation (2026-10-25)
- **Date:** 2026-10-25
- **Scope:** Native Linux devstack plus optional Docker-backed services
- **Related:** `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`,
  `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`

## 1. Summary

Devstack remains a native application: CLI, daemon, helper, SQLite state, PHP/Node
runtimes, and certificates all run on the host. Optional infrastructure services —
nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis, phpMyAdmin, and ImageMagick CLI — run
as **separate Docker containers**.

Service configuration and persistent service data live on the host under one configurable
root, mounted into the containers. Nginx and Apache are installed together but only one
may be active. MySQL and MariaDB are installed together but only one may be active.
PostgreSQL, Redis, phpMyAdmin, and ImageMagick are independent, with phpMyAdmin requiring
an active MySQL-family service.

Users may edit every service configuration file. Devstack treats generated site vhosts
as managed files, detects local edits, and never overwrites them silently. A structured
diff/conflict flow lets the user keep, replace, merge, or detach a config.

The design also adds a generated, resource-aware throughput profile for nginx, Apache,
PHP-FPM, Docker limits, and optional host kernel settings. The target is measured
throughput, not unbounded static maximum values.

The dashboard is a first-class client for every operational task. The CLI remains an
alternative interface. PHP and Composer are also available natively machine-wide, with a
configurable global PHP CLI default; `devstack shell` overrides that default per site.

## 2. Goals

1. Install, activate, deactivate, and uninstall optional services from the dashboard and
   CLI without invoking Docker by hand.
2. Enforce exclusive groups: one web server and one MySQL-family database can be active.
3. Keep all service configs, data, certificates, logs, secrets, and backups under one
   host-owned root with explicit container bind mounts.
4. Make every service config editable, while preserving generated site vhosts through a
   conflict model rather than silent replacement.
5. Keep the CA, leaf certificate issuance, and OS/browser trust native to the host.
6. Generate high-throughput nginx/Apache/PHP-FPM configuration from host resources and
   benchmark it against the built-in proxy.
7. Keep service ports local to the host by default, with no Docker socket exposed to
   service containers.
8. Provide Doctor and UI visibility for installed/active state, port conflicts, config
   validation, container health, and data paths.
9. Give the dashboard parity with every operational CLI action, including builds,
   `.htaccess`, service configs, credentials, and project commands.
10. Provide native static-build support for Nuxt/Vite-style projects, including explicit
    output directories, build profiles, and SPA fallback.
11. Provide machine-wide `php` and `composer` commands with a configurable PHP CLI
    default, while keeping per-site PHP overrides in `devstack shell`.

### Success criteria

- A user with Docker installed can install nginx and Apache, activate one, and switch
  later without editing Docker Compose files.
- MySQL and MariaDB can both be installed; activating one stops the other and changes
  port 3306 ownership atomically.
- A site can be served through nginx or Apache from files under the managed projects
  root, with PHP-FPM and Node still running natively.
- Editing a generated vhost and then changing the site produces a visible conflict, not
  a silent overwrite.
- `devstack init` and `devstack trust repair` make the native CA trusted by the OS and
  supported browsers; containers never install trust.
- A throughput profile passes configuration validation and serves static, PHP, and Node
  workloads without errors; benchmark results are recorded with the profile.
- `devstack services uninstall` preserves data unless deletion is explicitly requested.
- A Nuxt/Vite-style static site can be configured, built, and served entirely from the
  dashboard with the correct Node version and output directory.
- Bare `php` and `composer` use the selected global PHP CLI default outside a site shell;
  inside `devstack shell`, the site's exact PHP version and ini take precedence.
- A Laravel-style Apache site uses its project `.htaccess` without disabling devstack's
  safety checks or requiring CLI edits.

### Non-goals

- Windows, WSL, macOS, arm64, or multi-user shared workstations.
- Installing service packages natively via `dnf`/`apt` or managing service systemd units.
- Running nginx and Apache simultaneously, or MySQL and MariaDB simultaneously.
- Production database operations: replication, major-version migration, automatic
  backups, point-in-time recovery, or remote access.
- Arbitrary user-provided Docker Compose files, images, commands, ports, or bind mounts.
- Running the devstack application itself in Docker as part of this design. The old
  standalone devstack-in-Docker image is retired; Docker is reserved for service
  containers.
- Replacing the existing PHP/Node runtime catalogs or native PHP-FPM supervision.
- Guaranteeing a particular requests/sec number across different hardware.

### Anti-goals

- Deleting service data on uninstall by default.
- Overwriting user-edited configs without recovery.
- Mounting the Docker socket into any service container.
- Using unbounded `worker_connections`, `MaxRequestWorkers`, or PHP-FPM child counts.
- Applying host kernel tuning without a backup and a reset path.
- Exposing databases, Redis, or phpMyAdmin beyond loopback.
- Treating service containers as the source of truth for host configuration.

## 3. Constraints

- Native devstack requires systemd-based Linux x86_64 and Docker Engine with the Compose
  plugin for service tools. Rootful Docker is required; rootless Docker is out of scope.
- The daemon remains unprivileged. The privileged helper owns Docker orchestration, CA
  key custody, trust installation, and host tuning.
- PHP/Node backends remain native. Web-server containers use host networking so they can
  bind 80/443 and reach native PHP-FPM and Node listeners on `127.0.0.1`.
- Container images are pinned by digest in an embedded service catalog. The catalog is
  updated only by a new devstack release.
- Host file paths are not visible in containers except through explicit mounts.
- Existing native external nginx/Apache serving is retained for compatibility but is not
  installed or managed by the new Services page.
- Existing site paths may be outside the managed projects root; those sites continue to
  work in builtin mode and require explicit bind mounts to be served by Docker web
  servers.

## 4. Approaches Considered

### A. Native packages and systemd for every service — rejected

Install nginx, Apache, databases, Redis, and ImageMagick with `dnf`/`apt`, then manage
systemd units. This shares the distro's versions and feels native, but creates
cross-distro package drift, service-name differences, conflicts, SELinux issues, and a
large privileged package-install surface. The user explicitly chose separate service
containers.

### B. One all-in-one tools container — rejected

Install all services in one container under a supervisor. This gives a single control
plane, but databases, web servers, PHP, and Redis share a lifecycle and filesystem;
major-version upgrades and crashes affect everything. It also requires running databases
without systemd and amplifies image size and attack surface.

### C. Separate Docker service containers — selected

Use official or thin wrapper containers per service, pinned by digest, with host bind
mounts. This gives isolation, predictable upgrades, native Docker health checks, and
separate data directories. The cost is a controller and a stricter configuration model,
both handled here.

### Docker control plane alternatives

- **Daemon using the user's Docker group — rejected.** It requires the unprivileged
  devstack daemon to hold a root-equivalent Docker socket/group membership.
- **Dedicated root tools daemon — viable but redundant.** It duplicates the existing
  privileged helper's authentication and lifecycle.
- **Extend the existing privileged helper with fixed Compose operations — selected.**
  The helper already runs as root, has a narrow peer-UID-checked Unix socket, and owns
  privileged operations. It can execute only catalog-defined Compose commands against a
  fixed project; the daemon sends service IDs and actions, never arbitrary commands.

## 5. Architecture

```text
┌────────────────────────────────────────────────────────────────────┐
│ Native host                                                        │
│                                                                    │
│  devstack CLI / embedded UI                                        │
│          │                                                         │
│          ▼                                                         │
│  devstackd (unprivileged)                                          │
│   sites · API · SQLite · proxy · PHP/Node supervisors              │
│          │ Unix socket, peer UID checked                           │
│          ▼                                                         │
│  devstack-helper (root)                                            │
│   CA · trust · hosts · Docker Compose controller · host tuning     │
│          │                                                         │
│          ├──────────────► docker compose project                    │
│          │                 ├─ nginx OR apache (host network)        │
│          │                 ├─ mysql OR mariadb (loopback)           │
│          │                 ├─ postgresql (loopback)                  │
│          │                 ├─ redis (loopback)                       │
│          │                 ├─ phpmyadmin (loopback :8082)            │
│          │                 └─ imagemagick CLI (one-shot)             │
│          │                                                         │
│          └──────────────► native CA / trust stores                  │
│                                                                    │
│  Native PHP-FPM / Node backends ◄── web-server container            │
│  Site files under /data/devstack/projects                           │
└────────────────────────────────────────────────────────────────────┘
```

### 5.1 Serving modes

The existing `serving_mode` values remain:

- `builtin` — native Go proxy and helper 80/443 forwarders.
- `nginx` — nginx owns 80/443.
- `apache` — Apache owns 80/443.

A new `serving_provider` field distinguishes:

- `native` — legacy manually installed nginx/Apache, existing behavior.
- `docker` — devstack-managed service container.

Existing configs without `serving_provider` are treated as `native` for compatibility.
New service-container activations set `serving_provider=docker`.

Only one mode/provider may be active. Switching to a Docker web server:

1. stops the builtin forwarders,
2. stops any active native nginx/Apache service if the user explicitly confirms the
   conflict,
3. starts the selected web-server container with host networking,
4. waits for health and port ownership,
5. rolls back to the previous mode if any step fails.

The existing native vhost helper remains unchanged for `serving_provider=native`. The
Docker provider generates its own config tree and does not call the native vhost manager.

### 5.2 Native backends and web-server containers

- PHP-FPM masters remain native and listen on `127.0.0.1` in the existing FastCGI port
  range.
- Node site processes remain native and listen on `127.0.0.1`.
- The nginx/Apache container uses Docker `network_mode: host`, so it can bind 80/443 and
  reach native upstreams on loopback.
- The container sees site files at `/sites`; PHP-FPM expects host paths. Generated
  FastCGI configuration therefore uses:
  - container `root`/`DocumentRoot` = `/sites/<site-id>`,
  - native `SCRIPT_FILENAME` = `<project-host-path>/...`.
- Node proxy upstreams use `127.0.0.1:<port>`.

### 5.3 Native PHP CLI and project shell

PHP remains native. The managed PHP builds live under the devstack root, not in service
containers. A site's PHP CLI must use the same build and generated `php.ini` as its
PHP-FPM master so extensions, OPcache, and per-site settings match.

The existing site-aware runner remains:

```sh
devstack php --site app -- artisan migrate
```

Add a project shell:

```sh
cd /data/devstack/projects/app
devstack shell
php artisan migrate
```

`devstack shell`:

1. Resolves the site by `--site` or by matching the current directory to a registered
   site host path. If no site matches, it exits with the list of registered sites. If
   more than one site shares the directory, it requires `--site` rather than guessing.
2. Prepends a devstack-controlled shim directory to `PATH` so `php` resolves to the
   site's exact build.
3. Sets `PHPRC` or the runtime's equivalent ini override to the site's generated
   `php.ini`, ensuring the same extensions and settings as FPM.
4. Exports `DEVSTACK_SITE`, `DEVSTACK_HOME`, and the site project root.
5. Leaves the user's shell and system PHP untouched outside the shell.

Both commands use the selected site context:

```sh
devstack shell
php -v                 # exact site PHP build
php -m                 # exact site extensions
php artisan migrate    # site PHP + site ini
composer install       # Composer runs on the same site PHP
```

The shell's PHP shim and the global Composer wrapper must both call the site's PHP with
the generated ini. Composer must not silently fall back to a different PHP when it is
invoked inside a site context.

Add a one-shot equivalent:

```sh
devstack exec --site app -- php artisan migrate
```

A machine-wide bare-`php` shim is provided by §5.6. Site shells put the site PHP ahead of
it; outside a site shell, bare `php` uses the configured global CLI default.
`devstack shell-init` may emit exports for users who intentionally use `direnv` or a
similar directory hook.

### 5.4 Static build projects (Nuxt, Vite, Next static export)

A static frontend build is a **static site** at serve time, even if it needs Node to build.

Example for Nuxt 3 static generation:

```sh
devstack site add \
  --name app \
  --kind static \
  --path /data/devstack/projects/app \
  --document-root .output/public \
  --spa \
  --build-node 22 \
  --build-command "npm ci && npm run generate"
```

The actual output directory is explicit because frameworks differ:

- Nuxt 3 `nuxi generate`: usually `.output/public`
- Nuxt 2 `nuxt generate`: usually `dist`
- Vite: usually `dist`
- Next static export: usually `out`
- Other tools: whatever the project config produces

The site model gains an optional build profile:

```yaml
build:
  node_version: "22"
  command: "npm ci && npm run build"
  output: "dist"          # must remain inside the project
  env:
    NODE_ENV: production
```

Behavior:

- `devstack site build <name>` runs the command natively with the selected Node runtime,
  in the project directory, as the installing user.
- `npm`, `npx`, and `node` resolve to the selected devstack Node build; the build does not
  run in a service container or PHP runtime.
- If the configured output directory does not exist when the site is created, the site is
  created stopped; the user builds it, then starts it.
- Devstack snapshots the existing output directory before a rebuild when it is large
  enough to be practical; if the build fails, the previous snapshot is restored. If a
  snapshot is not possible, the failure is reported and the site state is marked
  degraded rather than pretending the build succeeded.
- Build output is verified inside the project. If the build fails or the output is
  missing, the site is not marked as successfully rebuilt.
- The static backend serves the configured document root directly. A successful build
  does not require a PHP-FPM or Node server process.
- `--spa` is used for client-side-routed builds where unknown non-file paths should
  return `index.html`. Pre-rendered/SSG builds that have real HTML routes can leave it
  off so unknown routes remain 404s.
- A custom SPA fallback file can be set with `--spa-fallback-file`; it defaults to
  `index.html` and must be a static file inside the document root.
- Build logs are streamed to the site build log and the dashboard.
- The dashboard gets a **Build** action and shows the build command, Node version, output
  directory, last result, and duration.

For Nuxt SSR (`nuxt build` producing `.output/server/index.mjs`), use a **Node site**
instead, with the generated server command and the site's `PORT` environment. The static
build profile is only for builds whose output is files.

`devstack shell` also exposes the selected Node runtime for static sites with a build
profile, so `npm`, `npx`, and `node` work in the project directory.

### 5.5 Managed Composer

Composer is not bundled with the PHP runtime, but it is installable as a managed native
CLI tool:

```sh
devstack tool install composer
composer install
composer require laravel/framework
```

The installer downloads a pinned Composer PHAR from the embedded tool catalog, verifies
its SHA-256, stores it under `/data/devstack/tools/composer/`, and installs a
`/usr/local/bin/composer` wrapper through the helper. The wrapper:

1. Uses `DEVSTACK_SITE` when it is set by `devstack shell`/`devstack exec`, with that
   site's native PHP build and generated `php.ini`.
2. Otherwise uses the machine-wide PHP CLI default.
3. If no CLI default is configured, uses the newest installed devstack PHP build, then
   system `php` as a fallback.
4. Preserves the standard `COMPOSER_HOME`/cache behavior unless the user overrides it.
5. Executes the PHAR, never a shell-built command string.

`devstack tool update composer` updates the pinned PHAR; `devstack tool remove composer`
removes only the managed wrapper and PHAR. A pre-existing `/usr/local/bin/composer` is
detected and requires an explicit `--force` before replacement.

Database clients connect to the service containers through `127.0.0.1` (`3306` for
MySQL/MariaDB, `5432` for PostgreSQL, `6379` for Redis), so Laravel/Artisan migrations
work without containerizing PHP.

### 5.6 Global PHP CLI default

A machine-wide CLI default makes bare `php` and `composer` work outside a site shell.

Setting a default:

```sh
devstack php default 8.4
devstack php default php@8.4.27-r1
devstack php default --status
devstack php default --clear
```

The line form selects the newest installed build on that line; the exact-build form is
recommended for reproducibility. If no build is installed, devstack offers to install
one from the catalog; it does not silently download.

The helper installs a managed shim:

```text
/usr/local/bin/php -> /data/devstack/tools/php-default/bin/php
/usr/local/bin/composer -> /data/devstack/tools/composer/bin/composer
```

The actual `php` wrapper sets the global CLI ini and executes the selected build's
binary. `php-config` and `phpize` shims are installed alongside it when present in the
build, so native extension builds use the same default PHP.

A global CLI ini lives at:

```text
/data/devstack/config/cli/php.ini
```

It is generated from the selected PHP version's standard defaults with extras off unless
the user enables them. The dashboard can edit the global CLI ini and extension toggles.
Composer uses this PHP by default. At minimum the global profile loads the extensions
Composer needs: `phar`, `openssl` (when present), `mbstring` where required, and the
normal standard extension set.

Precedence:

1. `devstack shell` / `devstack exec --site`: the site's exact PHP build and site
   `php.ini`. This keeps per-project PHP versions and extensions correct.
2. Bare `php` outside a devstack shell: the machine-wide CLI default.
3. Bare `composer` outside a devstack shell: the machine-wide CLI default.
4. Inside a `devstack shell`: the Composer wrapper uses `DEVSTACK_SITE` and the site
   PHP, not the global default.
5. If no global default is set: newest installed devstack PHP, then system `php`.

The wrapper never calls itself; it executes the absolute runtime path. Existing
`/usr/local/bin/php` or `/usr/local/bin/composer` files are detected, backed up, and
replaced only after `--force`/confirmation. Doctor verifies that `command -v php` and
`command -v composer` resolve to the devstack-managed shims; if another earlier `PATH`
entry wins, it reports the exact path conflict.

`devstack.php.default` and the global CLI ini are editable from the dashboard's Settings
page. The CLI and dashboard call the same API. The global default is machine-wide for shell users
managed by the installing devstack installation; it is not a per-site setting and does
not change a running site's PHP.

## 6. Host Layout

The root is configurable at install (`--root`) with `/data/devstack` as the recommended
default:

```text
/data/devstack/
  config.yaml                 # devstack machine configuration
  state.db                    # devstack SQLite state
  pki/                        # native CA; private key root-only
  runtimes/                   # existing PHP/Node runtimes
  sites/                      # existing generated per-site configs (internal)
  projects/                   # default managed site files
  certs/                      # leaf certificates and keys
  secrets/                    # service credentials, 0700 / 0600
  services/
    compose.yaml              # generated, not user-edited
    nginx/
      config/
        nginx.conf
        conf.d/
          generated/          # generated site vhosts
          custom/             # user-owned includes
      logs/
    apache/
      config/
      logs/
    mysql/
      config/
      data/
    mariadb/
      config/
      data/
    postgresql/
      config/
      data/
    redis/
      config/
      data/
    phpmyadmin/
      config/
    imagemagick/
  logs/
  run/
  cache/
  backups/
    config/
    services/
    performance/
```

Rules:

- `/data/devstack` is owned by the installing user, mode `0755`, so container users can
  traverse it; the helper runs as root and can write service data and configs.
- Config files are `0644`; directories `0755`. Secret files are `0600`; `secrets/` is
  `0700`.
- CA private key is root-owned `0600`; leaf keys are `0600` but readable by the web-server
  container's root process through the bind mount.
- `pki/` is root-owned `0700`; `secrets/` is installing-user-owned `0700`.
- Database data directories are owned by the container image's service UID, not the
  installing user. They are never edited as text.
- `sites/` remains the existing internal generated-config directory. User project files
  live in `projects/` to avoid changing the meaning of the existing layout.
- Existing installations using `~/.devstack` keep working. Moving an existing install to
  `/data/devstack` is not automated in this milestone; new installs choose the root with
  `--root`.

### 6.1 Site visibility

- New sites default to `projects/<name>`.
- Sites under `projects/` are visible to Docker web servers through a single read-only
  bind mount at `/sites`.
- A site outside `projects/` is mounted individually read-only at
  `/sites/<site-id>`; the generated Compose file is refreshed when such a site is added
  or removed. This preserves existing projects without exposing the entire home
  directory.
- Docker web-server activation refuses to proceed if any required host path contains
  characters that cannot be represented safely in Compose, or if the path cannot be
  resolved without symlink escapes.

## 7. Service Catalog and State Machine

The service catalog is embedded in the devstack binary:

```go
type Service struct {
    ID          string   // nginx, apache, mysql, ...
    DisplayName string
    Group       string   // web, mysql-family, standalone
    Image       string   // pinned repository@sha256:...
    Ports       []Port   // host bind, container port, loopback-only flag
    Profiles    []string // compose profiles
    DependsOn   []string
    ConfigDirs  []string // host -> container mount mappings
    DataDirs    []string
    Healthcheck Healthcheck
}
```

Initial catalog:

| ID | Group | Image family | Default port |
|---|---|---|---|
| `nginx` | web | official nginx, thin wrapper if required | 80/443 |
| `apache` | web | httpd, thin wrapper | 80/443 |
| `mysql` | mysql-family | official MySQL 8.4 | 3306 |
| `mariadb` | mysql-family | official MariaDB 11.8 | 3306 |
| `postgresql` | standalone | official PostgreSQL 18 | 5432 |
| `redis` | standalone | official Redis | 6379 |
| `phpmyadmin` | standalone | official phpMyAdmin 5 | 8082 |
| `imagemagick` | standalone | thin Fedora/UBI CLI image | none |

Exact image tags and digests are pinned in the embedded catalog and updated only by a
devstack release. The implementation must not use floating `latest` tags.

### 7.1 States

- `not_installed` — no image/config scaffold.
- `installed` — image present, config scaffold and data directories prepared.
- `starting` — container created, health check pending.
- `active` — container healthy and expected port owned.
- `stopping` — stop requested.
- `error` — last operation failed; last error and logs are retained.
- `installed_but_stopped` — image/config present, container not running.

“Installed” and “active” are separate in the UI and API.

### 7.2 Exclusive activation

Activation is serialized by the helper:

1. Validate service ID against the catalog.
2. Verify Docker and Compose are available.
3. Validate required configuration and data paths.
4. Check the conflict group:
   - web: `nginx` vs `apache`.
   - mysql-family: `mysql` vs `mariadb`.
5. Stop the conflicting active service and wait for its port to be released.
6. Prepare directories, secrets, generated configs, and the Compose project.
7. Start the target service and wait for health plus port ownership.
8. On failure: stop the target, restore the previous service and its config, and return a
   structured error.

Switching MySQL/MariaDB also restarts phpMyAdmin so its configured host/database user
matches the active service. Data directories are never shared between MySQL and MariaDB.

### 7.3 Install and uninstall

- Install pulls the pinned image and creates config/data directories. It does not
  automatically activate.
- Uninstall stops and removes the container. Images may be removed with
  `--remove-image`.
- Data is preserved by default. `--delete-data` is a separate, explicitly confirmed
  operation with a backup path displayed before deletion.
- Uninstall never removes the native CA, trust entries, or site projects.

## 8. Docker Orchestration

### 8.1 Helper RPC

The helper protocol gains service endpoints:

| Route | Purpose |
|---|---|
| `GET /v1/services/status` | Catalog, installed/active state, health, ports, data paths, last error |
| `POST /v1/services/install` | Pull image and scaffold directories |
| `POST /v1/services/activate` | Activate one service with conflict handling |
| `POST /v1/services/deactivate` | Stop one service |
| `POST /v1/services/uninstall` | Remove container/image; keep or delete data |
| `GET /v1/services/logs` | Bounded container log tail |
| `POST /v1/services/restart` | Restart a service |
| `GET /v1/services/config` | Config tree metadata and validation state |
| `POST /v1/performance/apply` | Apply throughput profile and host tuning |
| `POST /v1/performance/reset` | Revert host tuning |

Requests contain catalog IDs and validated options such as `delete_data`; they never
contain image names, Compose YAML, commands, host paths, or port numbers provided by the
daemon.

The helper executes a fixed set of operations:

```text
docker compose -p devstack-services -f <root>/services/compose.yaml pull <id>
docker compose -p devstack-services -f <root>/services/compose.yaml up -d --no-deps <id>
docker compose -p devstack-services -f <root>/services/compose.yaml stop <id>
docker compose -p devstack-services -f <root>/services/compose.yaml rm -f <id>
docker compose -p devstack-services -f <root>/services/compose.yaml ps --format json
```

The implementation must use an argument vector, never a shell, and must validate every
service ID against the embedded catalog.

### 8.2 Compose project

- One generated Compose file per devstack root, owned by the installing user with mode
  `0644`; the helper does not require it to be user-editable.
- Project name is derived from a hash of the root, not from a user-controlled string.
- All containers carry `devstack.service=<id>` and `devstack.root=<hash>` labels.
- Devstack never lists, stops, or removes containers from other Compose projects.
- The generated file is written atomically and backed up before replacement.
- Network:
  - Web-server containers: `network_mode: host`.
  - MySQL/MariaDB/PostgreSQL/Redis: bridge network, published only on `127.0.0.1`.
  - phpMyAdmin: bridge network, published only on `127.0.0.1:8082`.
  - phpMyAdmin connects to the active MySQL-family service through a fixed Compose
    network alias `db`; only the active container receives that alias.
- No service container mounts `/var/run/docker.sock`.
- `no-new-privileges` is set where the image supports it. Web-server containers add only
  the privileges required to bind 80/443.

### 8.3 Health and port ownership

Health checks use image health checks where available and a fixed TCP or HTTP probe where
not. After activation, the helper verifies:

- container state is running and healthy,
- expected host port is owned by the container or host process,
- config validation passed,
- required data/config mounts exist and are readable.

A failed health check leaves the service inactive and reports logs.

## 9. Web-Server Containers

### 9.1 Images and config

Nginx uses the official image plus host-mounted `nginx.conf`, `conf.d`, certs, and logs.
Apache uses a thin devstack wrapper image based on a pinned httpd version so it has a
stable `IncludeOptional` directory and required modules, while all generated and custom
vhosts remain host files.

Mounts:

| Host | Container | Mode |
|---|---|---|
| `projects/` | `/sites` | read-only |
| `certs/` | `/certs` | read-only |
| `services/nginx/config/nginx.conf` | `/etc/nginx/nginx.conf` | read-only |
| `services/nginx/config/conf.d/` | `/etc/nginx/conf.d/` | read-only |
| `services/nginx/logs/` | `/var/log/nginx` | read-write |
| `services/apache/config/` | wrapper include directory | read-only |
| `services/apache/logs/` | `/usr/local/apache2/logs` | read-write |

Configs must refer to container paths (`/sites`, `/certs`, `/etc/nginx/...`) while the
dashboard shows the corresponding host paths.

### 9.2 Vhost generation

- Generated per-site files live under `config/conf.d/generated/`.
- User includes live under `config/conf.d/custom/`.
- Generated files carry a header with the service, site ID, generation time, and content
  hash.
- The generator is deterministic for the same site state and profile.
- Static sites use the container `/sites` root.
- PHP sites use `/sites` for static checks and the native host path for
  `SCRIPT_FILENAME`.
- Node sites proxy to `127.0.0.1:<port>` with HTTP/1.1 keepalive and WebSocket upgrade
  headers.
- TLS certificates come from native `certs/`, mounted read-only.
- No user-supplied directives are interpolated into generated files; custom directives
  go in `custom/`.

### 9.3 Framework front controllers (Laravel, Symfony, etc.)

A framework site is configured with its public directory as the document root and the
front controller enabled:

```sh
devstack site add \
  --name app \
  --kind php \
  --path /data/devstack/projects/app \
  --document-root public \
  --front-controller \
  --php 8.4
```

Behavior is the same in all three serving paths:

- Existing files under `public/` (CSS, JS, images, `robots.txt`) are served directly.
- A request that does not map to a file is routed to `public/index.php`.
- The original request URI and query string are preserved.
- `SCRIPT_NAME` is `/index.php`; `SCRIPT_FILENAME` is the absolute
  `<project>/public/index.php`; the original path is passed as `PATH_INFO`.
- The HTTP method, body, headers, and uploaded files are preserved.
- A missing `public/index.php` is a 404, never a directory listing or source file.

Serving implementations:

- **Built-in Go proxy:** resolves the existing file first, then targets the configured
  `<docroot>/<front_controller_file>` with the cleaned request path as `PATH_INFO` and
  the original `REQUEST_URI` preserved. For static sites, the SPA fallback targets
  `<docroot>/<spa_fallback_file>`.
- **nginx:** `try_files $uri $uri/ @devstack_front_controller`; the named location sends
  the configured PHP front-controller file to FastCGI with `PATH_INFO $uri`. Static files
  never enter the front controller. Static SPA fallback uses
  `try_files $uri $uri/ /<spa_fallback_file>`.
- **Apache:** the generated fallback targets the configured PHP front-controller file
  when `.htaccess` is absent; static SPA fallback uses
  `FallbackResource /<spa_fallback_file>`. `.htaccess` remains authoritative for
  Apache sites when enabled.
- **Apache with `.htaccess`:** the generated `Directory` enables `AllowOverride All`
  (or the per-site override policy), so the project’s `.htaccess` is authoritative.
  Laravel’s standard `public/.htaccess` therefore works without devstack duplicating its
  rewrite rules. Devstack still sets `DirectoryIndex index.php` and preserves the
  original URI/query string. If no `.htaccess` exists, devstack generates a
  front-controller fallback (`FallbackResource /index.php`, or an equivalent rewrite to
  `index.php`) so the site still routes correctly.
- **Apache `.htaccess` policy:** per-site `apache_allow_override` is `all` by default for
  compatibility, with `fileinfo` and `none` available as stricter/performance settings.
  Changing it requires a config apply. `.htaccess` files are project-owned: they are not
  part of the service config manifest and are not overwritten by devstack. Invalid
  `.htaccess` directives surface at request time; devstack performs a live probe after
  apply and reports the Apache error log.
- **Apache config test:** `apachectl configtest` does not fully validate `.htaccess`
  files. A live HTTP probe plus error-log inspection is required after changing
  `.htaccess`-sensitive settings.

The Docker provider must pass a live Laravel-style acceptance case through both nginx and
Apache: root route, deep route, route with query parameters, POST, static asset, and
framework-generated 404. It must also verify `https`/`http` forwarded headers so
Laravel's URL generation and secure-cookie behavior work behind the container web server.
The Apache case must run once with Laravel’s `.htaccess` and once with `.htaccess`
disabled to prove both the project-owned and generated fallback paths.

Custom rewrites beyond the framework front controller belong in a detached/custom vhost
file or the project’s `.htaccess` when Apache is active, and are subject to the config
conflict model for generated files.

#### Front-controller settings

The site model gains:

```yaml
front_controller: true
front_controller_file: index.php   # relative to document_root
front_controller_mode: auto        # auto | generated | htaccess
apache_allow_override: all         # all | fileinfo | none

spa_fallback: false
spa_fallback_file: index.html      # relative to document_root
```

`front_controller_file` is for PHP sites only. It must be a relative path to an existing
regular `.php` file inside the document root; traversal, NUL bytes, symlink escapes, and
dotfile paths are rejected. It defaults to `index.php`, so Laravel/Symfony remain
unchanged. It may be changed to a custom bootstrap such as `app.php`.

`spa_fallback_file` is for static sites only. It must be an existing regular static file
inside the document root; it is not executed as PHP. It defaults to `index.html`. The SPA
fallback applies only when a requested path does not exist and does not look like a
static asset, so CSS/JS/image requests still return their normal 404 instead of being
replaced with the SPA shell.

The two are intentionally separate:

- A PHP application that routes through `index.php` uses `front_controller_file`.
- A static SPA that routes through `index.html` uses `spa_fallback_file`.
- An HTML file is never treated as a PHP front controller.
- A Node application owns its own routing; neither file setting applies.

- `auto`: Apache uses `.htaccess` when it exists and override is not `none`; otherwise
  devstack generates the fallback. nginx/builtin always generate the fallback.
- `generated`: devstack always generates the fallback; `.htaccess` may still contain
  additional directives but must not duplicate the front-controller rewrite.
- `htaccess`: only valid for Apache; requires a project `.htaccess`; devstack does not
  generate a front-controller fallback.
- `apache_allow_override` controls Apache's `AllowOverride` value. `all` is the
  compatibility default; `fileinfo` supports rewrite/header rules but not all override
  classes; `none` disables project `.htaccess` handling.
- Switching modes is a site update and triggers vhost regeneration and a config test.
- `.htaccess` files are not part of the service config manifest; they are project files
  and are shown in the dashboard as project-local Apache configuration.

This gives Laravel the normal `public/.htaccess` behavior while keeping nginx and the
built-in proxy framework-correct.

### 9.4 Legacy native provider

The existing native external nginx/Apache mode remains available through
`serving_provider=native` and the existing helper vhost implementation. The new Services
page only manages Docker provider services. A native server already occupying 80/443 is
detected during Docker activation, and activation refuses with instructions; devstack
does not stop the native service.

## 10. Configuration Ownership and Conflicts

### 10.1 Config classes

1. **Base service configuration** — main nginx/Apache config, database config, Redis
   config. Seeded by devstack, then user-owned. Not regenerated on normal operation.
2. **Generated site vhosts** — derived from devstack site state. Managed, but editable.
3. **Custom includes** — always user-owned, never overwritten.

### 10.2 Tracking

- A manifest under `services/<id>/.config-manifest.json` records:
  - path,
  - class (`base`, `generated`, `custom`),
  - last generated/seeded SHA-256,
  - last write time,
  - generated-from revision,
  - detach status.
- Before any generated write, the helper compares the current file hash with the
  manifest. Unchanged files are replaced atomically. Changed files are not overwritten.
- Direct filesystem edits and dashboard edits are treated identically.
- File writes use a temporary file, fsync, and rename. The previous content is stored
  under `backups/config/<timestamp>/`.

### 10.3 Conflict flow

When a site state change needs to regenerate a modified generated file:

1. The site mutation is rejected with `CONFIG_CONFLICT` before the site state is
   committed, unless the mutation does not affect that file (start/stop/log operations).
2. The conflict record contains the current file, proposed content, unified diff, site,
   reason, and both hashes.
3. The existing edited file keeps serving; no half-applied site state is persisted.
4. The UI/CLI offers:
   - **Keep mine** — mark the file custom/detached; devstack no longer regenerates it.
   - **Use generated** — back up the current file, replace it, and retry the site change.
   - **Merge** — open an editor with both contents and save a new file.
   - **Cancel** — leave all state unchanged.
5. After resolution, the site mutation is retried explicitly.

Site deletion with a modified vhost asks whether to keep the file as an orphan custom
vhost or remove it after backup. A kept orphan is visible in Doctor.

### 10.4 Applying edits

- Base config edits are applied by an explicit `Apply` action.
- The helper validates the config in the target container:
  - `nginx -t`,
  - `apachectl configtest`,
  - MySQL/MariaDB config validation,
  - PostgreSQL config validation,
  - Redis configuration startup check.
- Active services reload when possible; otherwise the container restarts.
- If validation/startup fails, the previous config is restored and the last error is
  shown with logs.
- An inactive service validates on the next activation.
- Database configuration changes declare whether they need a reload or restart; the UI
  shows this before applying.

### 10.5 Base config upgrades

Devstack never replaces a user-edited base config during an upgrade. If a new release
ships a new baseline, it writes `<file>.new`, shows a diff, and offers merge or reset.
Custom includes are never touched.

## 11. Certificates and Trust

- The existing native helper CA remains the only CA.
- CA private key stays under `pki/` root-only; no container sees it.
- Leaf certificates and keys are written to `certs/` and mounted read-only into the
  active web-server container.
- Certificate renewal updates the host files and reloads the web server.
- Native `devstack init` / `devstack trust repair` install the root CA into:
  - the OS trust store,
  - Chrome/Chromium NSS,
  - Firefox profiles when present.
- Containers never run trust commands and never receive write access to host trust
  stores.
- phpMyAdmin is exposed over loopback HTTP by default (`http://127.0.0.1:8082`) to avoid
  a separate certificate flow. A later option may serve it through the active web server.
- The web server uses the native leaf certs for `https://<site>.test`; browser trust is a
  host concern and remains independent of container replacement.

## 12. Throughput Profile

### 12.1 Principles

- Generate values from measured host resources, not fixed maximum constants.
- Prefer one nginx worker per available CPU, bounded by cgroup CPU quota.
- Total file descriptors, not `worker_connections` alone, bound concurrency.
- PHP-FPM memory is budgeted globally across active PHP sites.
- Every profile is validated and benchmarked; no RPS claim without measurements.
- Host kernel tuning is applied by the helper with a backup and a reset command.

### 12.2 Profile selection

`performance_profile` values:

- `balanced` — conservative local-development defaults.
- `throughput` — maximum safe resource use for the active web server and PHP sites.
- `custom` — user-edited generated values; devstack stops auto-regenerating them until
  reset.

New service-container web servers use `throughput` unless the user selects another
profile. The profile is stored in `config.yaml` and shown in the UI.

### 12.3 Generated nginx settings

The performance profile is written into the managed includes referenced by the seeded
`nginx.conf`, not over the user-owned base file. The generator emits values like:

```nginx
worker_processes auto;
worker_cpu_affinity auto;
worker_rlimit_nofile <min(hard_limit, max(4096, worker_connections * 2 + 1024))>;
pcre_jit on;

events {
    worker_connections <computed, typically 4096-8192>;
    multi_accept on;
}

http {
    sendfile on;
    tcp_nopush on;
    tcp_nodelay on;
    keepalive_timeout 30s;
    keepalive_requests 10000;
    open_file_cache max=<computed> inactive=60s;
    open_file_cache_valid 30s;
    open_file_cache_min_uses 2;
    open_file_cache_errors on;
    access_log <path> combined buffer=64k flush=5s;
    error_log <path> warn;
}
```

Additional rules:

- Do not emit `use epoll;`; nginx automatically selects epoll on Linux.
- Use `reuseport` on 80/443 listeners.
- Use HTTP/2 with version-aware syntax:
  - nginx >= 1.25.1: `http2 on;`
  - older nginx: `listen 443 ssl http2;`
- Use `fastcgi_keep_conn on` and upstream keepalive for PHP/Node.
- Use `proxy_http_version 1.1` and `proxy_set_header Connection ""` for Node HTTP
  upstreams.
- Gzip is profile-controlled. The throughput profile starts with gzip off for local
  loopback measurements; the balanced profile may use gzip level 1-3. Level 9 is not a
  default.
- Access logs are buffered; debug logging is off.

### 12.4 Generated Apache settings

- MPM event only; prefork is rejected.
- Required modules: proxy, proxy_http, proxy_fcgi, proxy_wstunnel, ssl, headers, rewrite,
  and optional http2.
- Values are computed and validated:
  - `ThreadsPerChild` (default 25 or 32),
  - `ServerLimit = ceil(MaxRequestWorkers / ThreadsPerChild)`,
  - `MaxRequestWorkers` is a multiple of `ThreadsPerChild`,
  - `StartServers`, `MinSpareThreads`, `MaxSpareThreads` derived from CPU and limit,
  - `AsyncRequestWorkerFactor` stays at 2 unless `mod_status` data justifies otherwise.
- `KeepAlive On`, `MaxKeepAliveRequests 10000`, `KeepAliveTimeout 3`.
- `HostnameLookups Off`, `EnableSendfile On`, `MaxConnectionsPerChild 0`.
- PHP uses FastCGI, not `mod_php`.
- `ListenBacklog` follows the host `somaxconn` after tuning.
- Apache `.htaccess` support is a compatibility choice, not the fastest path. With
  `apache_allow_override=all`, Apache performs per-directory `.htaccess` lookups and
  parses override files; benchmark it against `none`/`fileinfo`. Laravel projects that
  need `.htaccess` keep `all`; performance-only projects can select `none`.

### 12.5 PHP-FPM and OPcache

- Compute a global PHP child budget from available memory and CPU.
- Divide the budget across active PHP sites; do not give every site the same
  `pm.max_children`.
- `throughput` uses `pm = static` with the computed budget; `balanced` uses `dynamic`.
- Set `listen.backlog` to the tuned host backlog.
- OPcache:
  - enabled,
  - memory/interned strings/max accelerated files scaled to the runtime,
  - `validate_timestamps=1` for development,
  - production-like `0` is an explicit option that requires cache reset after code
    changes.
- `realpath_cache_size=4096K`, `realpath_cache_ttl=600` in throughput.
- PHP `imagick` is already bundled in the devstack PHP builds; no host ImageMagick
  library is required.

### 12.6 Node

PHP/Node runtimes remain native. Node site supervision is unchanged. Cluster mode is a
per-site application concern; devstack does not multiply a user's Node process behind
their back. Document `UV_THREADPOOL_SIZE` and Node cluster options in the performance
guide.

### 12.7 Host tuning

Applied only by the helper, with backups under `backups/performance/`:

```text
/etc/sysctl.d/99-devstack-performance.conf
/etc/security/limits.d/99-devstack-performance.conf
```

Initial values, computed/capped per host:

- `fs.file-max` — large enough for the configured web/PHP concurrency.
- `net.core.somaxconn` — aligned with nginx `backlog`/Apache `ListenBacklog`.
- `net.core.netdev_max_backlog` — raised for high connection rates.
- `net.ipv4.ip_local_port_range` — widened for proxy upstreams.
- `nofile` soft/hard limits for the devstack user, daemon, helper, and Docker containers.

The helper runs `sysctl --system` after writing and verifies the applied values. A
`devstack performance reset` removes only devstack-owned files and restores saved
values. Global kernel changes are shown in the UI before the first apply.

Docker containers receive matching `ulimits` and, for web servers, host networking.
Host tuning is not applied by a database-only install.

### 12.8 Benchmarking

A benchmark command runs a pinned `wrk`/`hey` container or binary with host networking:

- nginx static file,
- nginx → PHP-FPM,
- nginx → Node,
- Apache static file,
- Apache → PHP-FPM,
- HTTP and HTTPS separately,
- keepalive on/off where relevant.

Each run records requests/sec, p50/p95/p99 latency, errors, CPU, memory, profile, and
container image digests. Results go under `logs/performance/<timestamp>/`. Acceptance
checks functional correctness and config validity; RPS is reported relative to the
built-in proxy and prior runs, not as a fixed CI threshold.

## 13. API, CLI, and Dashboard

### 13.1 API

New routes:

```text
GET    /api/v1/services
POST   /api/v1/services/{id}/install
POST   /api/v1/services/{id}/activate
POST   /api/v1/services/{id}/deactivate
POST   /api/v1/services/{id}/restart
DELETE /api/v1/services/{id}
GET    /api/v1/services/{id}/logs
GET    /api/v1/services/{id}/config
PUT    /api/v1/services/{id}/config
GET    /api/v1/services/{id}/config/diff
POST   /api/v1/services/{id}/config/apply
POST   /api/v1/services/{id}/credentials/reveal
POST   /api/v1/services/{id}/credentials/rotate
POST   /api/v1/performance/apply
POST   /api/v1/performance/reset

PATCH  /api/v1/sites/{ref}              # document root, SPA/front-controller/build settings
POST   /api/v1/sites/{ref}/build
GET    /api/v1/sites/{ref}/build/logs
GET    /api/v1/sites/{ref}/files?path=.htaccess
PUT    /api/v1/sites/{ref}/files?path=.htaccess
GET    /api/v1/sites/{ref}/terminal      # WebSocket/PTY, site-scoped
GET    /api/v1/php/default
PUT    /api/v1/php/default
GET    /api/v1/php/default/ini
PUT    /api/v1/php/default/ini
```

All mutations require the existing session/CSRF protections. Service IDs are catalog
IDs, never paths. File access is limited to approved project config files and validated
against the site root; the generic filesystem browser remains read-only.

### 13.2 CLI

```sh
devstack service list
devstack service install nginx
devstack service activate nginx
devstack service deactivate apache
devstack service uninstall mysql
devstack service logs postgresql
devstack service credentials mysql
devstack service credentials rotate mysql --yes
devstack site update app --document-root public --front-controller
devstack site update app --front-controller-file index.php
devstack site update app --front-controller-mode auto
devstack site update app --apache-allow-override all
devstack site update app --spa --spa-fallback-file index.html
devstack site update app --build-node 22 --build-command "npm ci && npm run build"
devstack site build app
devstack service config nginx
devstack service config diff nginx
devstack service config apply nginx
devstack service config reset nginx
devstack performance apply
devstack performance reset
devstack performance benchmark
devstack shell [--site <name>]
devstack exec [--site <name>] -- <command...>
devstack shell-init
devstack tool list
devstack tool install composer
devstack tool update composer
devstack tool remove composer
devstack php default 8.4
devstack php default php@8.4.27-r1
devstack php default --status
devstack php default --clear
```

Existing `devstack vhost` commands continue to serve the legacy native provider.

### 13.3 Dashboard

The dashboard must provide full operational parity with the CLI. The CLI is an
alternative interface, not a prerequisite.

Add a **Services** page:

- grouped cards: Web servers, MySQL family, Standalone;
- installed/active badges and versions;
- Activate/Deactivate with conflict explanation;
- port, data path, config path, and container status;
- Open phpMyAdmin for the active MySQL family;
- Config editor with managed/modified/conflict markers and diff view;
- Apply/validate/reset actions;
- credential reveal/copy/rotate with an explicit confirmation;
- logs and health;
- throughput profile and benchmark results.

Add these site-level dashboard capabilities:

- create and edit static, PHP, and Node sites;
- choose document root and SPA fallback;
- set front-controller file/mode and Apache `.htaccess` policy;
- edit `.htaccess` for Apache sites with a warning that nginx/builtin ignore it;
- configure build Node version, build command, output directory, and environment;
- run a build and stream the build log;
- start/stop/restart/open the site;
- open a site-scoped terminal;
- edit service configs and resolve config conflicts;
- reveal/copy/rotate database credentials;
- install/update/remove Composer and ImageMagick;
- set the machine-wide PHP CLI default and edit its global `php.ini`;
- apply/reset performance profiles and run benchmarks.

Add a **Terminal** panel:

- WebSocket-backed PTY attached to a site context;
- working directory is the site project root;
- environment matches `devstack shell`: site PHP/Node runtime on `PATH`, `PHPRC`/site
  ini for PHP, and site environment variables;
- runs as the installing user, never root;
- `sudo` is not available;
- command input/output is not written to the service logs; an audit line records the
  command, exit status, and duration;
- disabled automatically if the daemon is not bound to localhost unless the user
  explicitly enables remote terminal access.

The existing serving-mode control is updated to show provider (`native` or `docker`) and
to route Docker selections through the Services activation flow.

### 13.4 Dashboard parity table

| Operation | Dashboard | CLI |
|---|---|---|
| Site create/edit/path/document root | Yes | Yes |
| SPA fallback and fallback filename | Yes | Yes |
| PHP front controller file/mode | Yes | Yes |
| Apache `.htaccess` policy/editor | Yes | Yes |
| Build profile and run build | Yes | Yes |
| Project terminal / Artisan / Composer / npm | Yes | Yes |
| Service install/activate/deactivate | Yes | Yes |
| Service config edit/diff/apply/reset | Yes | Yes |
| Service credentials reveal/rotate | Yes | Yes |
| phpMyAdmin launch | Yes | Via URL |
| Performance apply/reset/benchmark | Yes | Yes |
| Composer/ImageMagick tool management | Yes | Yes |
| Global PHP CLI default and global ini | Yes | Yes |
| Trust/Doctor/repair | Yes | Yes |

## 14. Doctor and Observability

Doctor adds checks for:

- Docker daemon and Compose availability;
- pinned image presence/digest;
- installed/active state;
- web/mysql-family exclusivity;
- port ownership and bind address;
- config manifest conflicts;
- generated config validation;
- data/config mount readability and ownership;
- certificate presence and trust;
- phpMyAdmin dependency on active MySQL family;
- native server conflict on 80/443;
- performance profile and host tuning state;
- container health and recent errors.

Container logs are retrieved through the helper with bounded tails. Devstack does not
mount the Docker socket into the UI/daemon.

## 15. Security

- The helper authenticates peers by UID and accepts only catalog service IDs.
- No service request may contain an arbitrary image, compose file, command, host path,
  bind mount, or port.
- Images are pinned by digest. Pulls use HTTPS.
- Docker orchestration uses fixed Compose operations and a project isolated by labels.
- The Docker socket is never mounted into service containers.
- Database, Redis, and phpMyAdmin ports bind only to `127.0.0.1`.
- Web servers bind 80/443 because that is their purpose; they do not expose other
  service ports.
- Secrets are generated randomly, stored `0600` under `secrets/`, and never written to
  logs.
- Config edits are validated in the target container before activation.
- The CA private key remains root-only; containers receive leaf certs only.
- Host tuning is limited to a fixed allowlist and backed up before applying.
- Uninstall preserves data by default and requires explicit confirmation for deletion.
- Dashboard terminals run as the installing user with no `sudo`, are scoped to the site
  directory, require the existing session/CSRF-authenticated WebSocket handshake, and are
  disabled for non-local bind addresses unless explicitly enabled.
- Project file editing is limited to approved config files such as `.htaccess`; it never
  exposes arbitrary host files, symlink escapes, or the CA/secrets directories.
- Credential reveal is an explicit, audited action. Passwords are never included in
  service list responses, logs, or page HTML until the user requests the reveal.

## 16. Compatibility and Migration

- Existing `~/.devstack` installs continue to work.
- `/data/devstack` is the recommended new root, selected with `--root` for new installs;
  existing data is not moved automatically.
- `serving_provider` defaults to `native` when absent. Existing external nginx/Apache
  configs remain valid.
- New Docker service activation sets `serving_provider=docker` and stops the builtin
  forwarders through the existing helper flow.
- Existing sites outside `projects/` remain usable in builtin/native modes. Docker web
  servers mount them individually read-only as described in §6.1.
- The old standalone devstack-in-Docker image is retired. Docker is used only for the
  separate service containers described here.

## 17. Testing and Acceptance

### Unit tests

- Service catalog validation and digest pinning.
- State machine: install → activate → conflict → rollback → deactivate → uninstall.
- Exclusive groups and port ownership.
- Compose generation with fixed service IDs and safe paths.
- Config manifest hashing, direct-edit detection, conflict records, diff, keep/use/merge.
- Base config seeding and upgrade `.new` behavior.
- nginx/Apache template generation for static, PHP, Node, TLS, redirects, and version-
  aware HTTP/2.
- Performance calculations (worker count, connection limits, Apache MPM values, PHP-FPM
  budget).
- Host tuning file generation and reset.
- Certificate mount paths and trust behavior.
- Site-shell PHP selection: `php -v`, `php -m`, `php --ini`, and a marker extension must
  match the site's PHP build; Composer must run with the same PHP and ini.
- Ambiguous or unregistered project directories fail with a clear site list; `--site`
  overrides the directory lookup.
- Global CLI default: outside a site shell, `php -v`, `php --ini`, `php -m`, and
  `composer` use the selected default build and global ini; inside a site shell with a
  different PHP version, both use the site build instead.
- Existing `/usr/local/bin/php`/`composer` conflicts are detected, backed up, and require
  `--force`; Doctor reports PATH entries that shadow the managed shims.

### Integration tests

- Fake Docker/Compose command runner: no arbitrary command or service escapes.
- Compose file rendering and network/port rules.
- Container health failure rollback.
- MySQL/MariaDB switch and phpMyAdmin reconciliation.
- Config conflict blocks a site mutation and retry succeeds after resolution.
- Web server switch keeps sites/certs.
- Root migration and legacy provider compatibility.

### Host acceptance (Fedora 44)

1. Fresh native devstack with Docker installed.
2. Install nginx and Apache; activate each; only one owns 80/443.
3. Install MySQL and MariaDB; activate each; only one owns 3306; data persists.
4. Activate PostgreSQL and Redis; connect from a native PHP site.
5. Open phpMyAdmin on `127.0.0.1:8082`; it follows the active MySQL-family service.
6. Create static, PHP, and Node sites under `projects/`; serve through nginx and Apache.
7. Configure a Nuxt-style static site entirely from the dashboard: document root,
   SPA fallback, build Node version, build command, output, build, and start.
8. Configure a Laravel-style PHP site entirely from the dashboard: `public` document
   root, front-controller file/mode, `.htaccess`, PHP version, extensions, and ini.
9. Edit `.htaccess` and service configs from the dashboard; verify apply, validation,
   and rollback.
10. Run `php artisan migrate`, `composer install`, and `npm run build` from the dashboard
    terminal/command runner with the correct site runtime.
11. Reveal/copy/rotate database credentials from the dashboard; confirm they are absent
    from logs and list responses.
12. Edit a generated vhost; change the site; verify conflict, keep/use/merge, and retry.
13. Verify native CA/trust for HTTPS sites; containers never modify trust stores.
14. Apply throughput profile and host tuning; verify sysctl/limits; run benchmark;
    reset and verify restoration.
15. Uninstall services; data remains; explicit delete removes it.
16. Doctor reports every failure mode with a remediation.

### Benchmark acceptance

- No HTTP errors during each benchmark.
- Static, PHP, and Node responses are correct through both web servers.
- Config validation passes after tuning.
- Results are saved with hardware/profile metadata. No absolute RPS threshold is a CI
  gate.

## 18. Decisions Made Here

| Area | Decision |
|---|---|
| Tools installation | Separate Docker service containers; no native package/systemd management. |
| Devstack runtime | Native Linux application; no devstack-in-Docker requirement for this feature. |
| Root | `/data/devstack` recommended and configurable; existing `~/.devstack` preserved. |
| Config ownership | All configs editable; generated vhosts conflict-protected; Keep mine detaches. |
| Conflict behavior | Block affected site mutation until resolved; never silently overwrite. |
| Web servers | nginx and Apache installed, one active; Docker provider uses host networking. |
| MySQL family | MySQL and MariaDB installed, one active; phpMyAdmin follows the active one. |
| Containers | Separate service containers, pinned by digest, no Docker socket mounts. |
| Docker control | Privileged helper executes fixed Compose operations; daemon remains unprivileged. |
| PHP/Node | Native runtimes and native PHP-FPM/Node backends; never service containers. |
| PHP CLI | `devstack shell`/`devstack exec` use the site PHP build and generated ini; bare system `php` is not replaced by default. |
| Composer | Native managed PHAR with a global `/usr/local/bin/composer` wrapper; uses site PHP inside `devstack shell`, otherwise the machine-wide CLI default. |
| Global PHP CLI | Machine-wide `/usr/local/bin/php` shim and global CLI ini; site shells override it. |
| Dashboard parity | All operational tasks are available in the dashboard, including builds, `.htaccess`, credentials, and a site terminal; CLI remains an alternative. |
| Certificates | Native CA/trust; containers receive leaf certs only. |
| phpMyAdmin | Loopback HTTP on 8082. |
| ImageMagick | CLI container; PHP `imagick` remains bundled in the PHP runtime. |
| Performance | Auto-tuned throughput profile; host tuning via helper with backup/reset. |
| nginx epoll | Rely on automatic Linux epoll detection; no explicit `use epoll;`. |
| HTTP/2 | Version-aware nginx syntax; benchmarked. |
| Gzip | Profile-controlled, not fixed at level 9. |
| Legacy native serving | Retained as `serving_provider=native`; not installed by the Services page. |

## 19. Builder Review Resolutions

These decisions resolve ambiguities found during the independent design review.

### 19.1 Ownership, traversal, and SELinux

- `/data/devstack` is mode `0755`, owned by the installing user, so container users can
  traverse it. Sensitive subtrees are stricter:
  - `pki/` is root-owned `0700`;
  - `secrets/` is installing-user-owned `0700`;
  - database data directories are owned by the image's service UID and not readable by
    other users where the image permits it.
- Config directories are `0755`, config files `0644`, and secret files `0600`.
- The helper creates/chowns service data directories before first container start.
- Bind mounts that require SELinux relabeling use Docker `:z`/`:Z` labels. Fedora 44 with
  Docker rootful is the supported target. Rootless Docker and userns-remap are out of
  scope.
- Doctor checks directory traversal, ownership, SELinux labels, and container mount
  readability.

### 19.2 Performance config layering

Performance settings do not rewrite a user-owned base config directly.

- `nginx.conf` is seeded once and includes managed files:
  - main-context performance include,
  - an `events` include,
  - an `http` performance include.
- Apache's base config includes a managed performance file in its `conf.d` directory.
- `performance apply` writes only the managed include files. It does not rewrite
  `nginx.conf` or the Apache base config.
- If the user removes the include lines from a base config, the next apply reports a
  profile conflict; it does not edit the base file.
- The config manifest tracks performance includes as generated files and applies the same
  conflict rules.

### 19.3 PHP-FPM budget ownership

- The daemon owns the site inventory, PHP process budgets, and per-site FPM configs.
- The helper owns host tuning, web-server performance includes, and Docker restart/
  validation commands.
- `devstack performance apply` is orchestrated by the daemon:
  1. daemon computes the global and per-site PHP budget;
  2. daemon asks the helper to apply host and web-server settings;
  3. daemon restarts/switches PHP-FPM generations with rollback;
  4. daemon updates state only after every step succeeds.
- The helper RPC carries no site inventory.

### 19.4 Config RPC and validation commands

- The daemon writes config files and the config manifest, because they live in the
  user-owned root.
- The helper exposes catalog-defined validation/reload/log operations only:
  - `POST /v1/services/config/validate` with a service ID;
  - `POST /v1/services/config/reload` with a service ID;
  - `GET /v1/services/logs` with a service ID and bounded tail.
- The helper runs only fixed commands from the catalog, for example
  `docker compose exec -T nginx nginx -t` or
  `docker compose exec -T apache apachectl configtest`.
- No request may carry a command, path, image, or mount.

### 19.5 Database bootstrap and credentials

Each database engine has its own data directory and secrets. On first activation:

- MySQL: random root password; a `devstack` admin account with a random password and
  access limited to local/Compose networks; a `devstack` database.
- MariaDB: the same model with its own separate credentials and data directory.
- PostgreSQL: random `postgres` password; a `devstack` role and `devstack` database.
- Redis: no password by default because the port binds to `127.0.0.1`; an optional
  password can be generated later.
- phpMyAdmin uses cookie authentication. It does not embed credentials or auto-login.
  The dashboard shows/copies the active engine's credentials after explicit reveal.
- Switching MySQL/MariaDB restarts phpMyAdmin with the active engine's network alias and
  credentials.
- No per-site databases or users are created automatically. Users create them through
  phpMyAdmin or SQL.
- Secret files are `0600`; Compose receives them through environment interpolation or
  mounted secret files, never through a world-readable Compose file.

The generated files are:

```text
secrets/
  mysql/
    root_password
    devstack_password
  mariadb/
    root_password
    devstack_password
  postgresql/
    postgres_password
    devstack_password
  redis/
    password                 # absent unless the optional password is enabled
```

MySQL/MariaDB `root` is restricted to the database container/local socket. The
`devstack` admin account is the account used by phpMyAdmin and normal local
administration. PostgreSQL exposes the `postgres` superuser and `devstack` role with
their respective passwords.

Retrieve credentials only from the native host:

```sh
devstack service credentials mysql
devstack service credentials mariadb
devstack service credentials postgresql
```

The CLI requires an explicit command; plain `devstack service list` and logs never print
passwords. The dashboard has a reveal/copy action with a warning. Credentials are never
placed in generated nginx/Apache configs or web-accessible files.

If a data directory already exists but its secret file is missing, devstack does not
silently generate a new password. It reports an inconsistency and requires an explicit
rotation/reset flow. Credentials survive deactivation, container replacement, and
upgrades; deleting the data directory with `--delete-data` deletes its secrets with it.

Database credential rotation is an explicit operation:

```sh
devstack service credentials rotate mysql --yes
```

It updates the database account and the stored secret, then restarts dependent
containers. The old password is not retained.

### 19.6 Web-server parity

The Docker provider must match the native vhost feature set:

- static, PHP, and Node sites;
- SPA fallback and front-controller routing;
- `www` alias;
- force-HTTPS and TLS-disabled redirects;
- stopped/crashed branded 503;
- branded default 404;
- `devstack.test` dashboard vhost;
- HTTP/2 and TLS certificate reload.

The dashboard vhost is mandatory; switching to a Docker web server must not make the
dashboard unreachable on 80/443.

### 19.7 Provider transitions

- Docker → builtin: stop the active web-server container, restore builtin forwarders.
- Docker → native: stop the container, then require the user to start the existing native
  server; devstack does not manage its systemd unit.
- Deactivating or uninstalling the active web server is refused until another serving
  mode is active.
- A native nginx/Apache already owning 80/443 is detected, but the helper does not stop
  it. Activation returns a conflict with exact instructions.
- Any failed transition restores the previous serving mode and config.

### 19.8 Data deletion

`--delete-data` performs, in order:

1. stop the service,
2. create a compressed backup of its config, secrets, and data under
   `backups/services/<id>/<timestamp>/`,
3. verify the backup exists and is non-empty,
4. delete only the catalog-owned directories,
5. record the backup path in the operation result.

If backup or verification fails, deletion aborts.

### 19.9 Wrapper image supply chain

- Apache and any required nginx wrapper images are built by the devstack release pipeline
  from pinned base digests and published as part of the devstack release artifacts.
- The service catalog pins their digests.
- A locally built wrapper is a development-only override and is not accepted by the
  production catalog.
- Offline install is supported when the pinned images are already present locally;
  otherwise the user must pull them.

### 19.10 Root migration

Changing an existing installation from `~/.devstack` to `/data/devstack` is out of scope
for this milestone. New installs may choose the root with `--root`; existing installs
continue in place. No automatic move of the CA, SQLite state, runtimes, certificates, or
service data is performed.

### 19.11 ImageMagick CLI

ImageMagick is a managed CLI tool container, not a service:

```sh
devstack tool install imagemagick
devstack tool run imagemagick -- magick identify image.png
```

`tool run` mounts the current registered site's project root at `/work` read-write and
sets the working directory to the matching subdirectory. If run outside a site, it mounts
the current directory. It refuses paths outside the allowed site/current root. The PHP
`imagick` extension remains bundled in the native PHP runtime.

## 20. Open Builder Decisions

These may be settled during planning without changing the product behavior:

- Exact initial image tags/digests for each service.
- Exact thin-wrapper Dockerfile for Apache (and nginx if the official image cannot
  support the required config mounts cleanly).
- Exact formula/caps for worker/MPM/PHP values per CPU/memory class.
- Benchmark concurrency levels and test payloads.
- Whether phpMyAdmin needs a tiny generated config file or can use environment variables
  for all settings.
- How much of the legacy native provider remains in the dashboard versus CLI-only.
- Whether Redis gets an optional password in the first implementation.
- The exact Composer version and PHAR digest.
