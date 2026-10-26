# Glossary

| Term | Meaning |
|---|---|
| **devstack** | The native CLI/daemon/helper application, and informally the whole product. |
| **daemon / devstackd** | Unprivileged per-user process that owns the API, UI, SQLite state, proxy, supervisors, and runtime catalog. |
| **helper / devstack-helper** | Root service that owns CA signing, trust, hosts, 80/443 forwarding, native vhosts, and uninstall. |
| **site** | A registered local project served at one `.test` hostname. Static, PHP, or Node. |
| **project root** | The user’s source directory for a site, e.g. `projects/app`. |
| **document root** | The directory served publicly, e.g. `public/`, `dist/`, `.output/public/`. |
| **backend** | The loopback process serving a site: static server, Node process, or PHP-FPM master. |
| **runtime** | A checksum-pinned PHP or Node build installed under `runtimes/`. |
| **serving mode** | `builtin`, `nginx`, or `apache`; which component owns public 80/443. |
| **serving provider** | Distinction between `native` (manually installed nginx/Apache) and `docker` (devstack-managed service container). |
| **builtin mode** | Native Go proxy plus helper 80/443 forwarders; the current default. |
| **native external mode** | Existing nginx/Apache integration using a server installed by the administrator. |
| **front controller** | A single PHP entry file (normally `index.php`) that receives requests not mapped to real files. |
| **SPA fallback** | Static-site behavior returning `index.html` for missing non-file routes; assets still 404. |
| **`.htaccess`** | Per-directory Apache configuration, honored only in Apache mode; project-owned, not part of devstack’s generated config manifest. |
| **managed config** | A generated devstack config file tracked by hash and protected by conflict handling. |
| **base config** | A service main config seeded once, then user-owned. |
| **custom include** | A user-owned config file never overwritten by devstack. |
| **conflict** | State where a managed file changed locally and devstack needs to regenerate it; resolved by keep/detach, use generated, merge, or cancel. |
| **service** | Optional Docker infrastructure component (nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis, phpMyAdmin). ImageMagick CLI is descoped. |
| **service config** | An editable file under `services/<id>/config`, classified as base, generated, or custom and tracked by the config manifest. |
| **config manifest** | `.config-manifest.json` at the service config root, recording classes/hashes/detach state and shared with Docker web vhosts. |
| **listener port** | A loopback daemon port (`http_port`, `https_port`, `ui_port`) changed in Settings and applied by daemon restart. |
| **terminal ticket** | A short-lived, one-use, site/UID-bound capability that authorizes one terminal WebSocket. |
| **PTY** | Pseudo-terminal used by the site terminal, with process-group teardown and output/idle limits. |
| **installed** | Image/config/data exists; the service may be stopped. |
| **active** | Container is running and healthy, and owns its expected port. |
| **conflict group** | Mutually exclusive services: nginx/Apache for 80/443; MySQL/MariaDB for 3306. |
| **host network** | Docker `network_mode: host`; web-server containers bind host 80/443 and reach native loopback backends. |
| **build profile** | Site settings for native Node build: Node version, command, output directory, env. |
| **global CLI default** | Machine-wide PHP build/ini used by bare `php` and `composer` outside a site shell. |
| **site shell** | `devstack shell`/`devstack exec --site` context that overrides the global PHP default with the site’s PHP/ini. |
| **managed hosts block** | The `# BEGIN devstack` / `# END devstack` section in `/etc/hosts`. |
| **CA** | Local certificate authority held by the helper; root private key never enters site containers. |
| **leaf certificate** | Per-site certificate signed by the devstack CA. |
| **host tuning** | Reversible kernel `sysctl`/limits changes applied by the helper only when explicitly opted in. |
| **benchmark** | Measured HTTP throughput/latency run stored under `logs/performance/`; never estimated. |
| **M1** | The released Linux foundation/static/Node/PHP/release milestone. |
| **M4** | Later signed packages/installers, diagnostics bundle, auto-update, remote catalog, arm64, log aggregation. |
| **Plan 3** | Deferred Windows/WSL work. |
