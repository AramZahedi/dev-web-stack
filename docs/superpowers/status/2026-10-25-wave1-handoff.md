# Service Platform Wave 1 Handoff

- **Date:** 2026-10-25
- **Integration branch:** `integration/services-wave-1`
- **Integration tip at handoff:** captured by the commit containing this file
- **Wave 1 status:** implementation, independent lane reviews, fix rounds, integration
  suite, UI suite, installer tests, and live Docker smoke suites pass.
- **Next wave:** Wave 2 (phpMyAdmin, ImageMagick CLI, performance/host tuning, credential
  rotation, external-site Docker mounts, dashboard action/config/build/terminal panels).

## Delivered

### Serial foundation
- Extended service catalog/Compose contracts: digest validation, long-form bind volumes,
  host-network scoping, healthchecks, environment, entrypoint/command, SELinux, ulimits,
  service `installed_but_stopped`, status/health fields.
- Transactional helper controller: install/pull, exclusive activation, health/port checks,
  rollback, restart, validate/reload, logs, verified backup before delete-data.
- Daemon service manager, lifecycle API, CLI actions, credential reveal/rotate hooks,
  audited credential access, structured `SERVICE_CONFLICT`/`HELPER_ERROR`, backup paths,
  and serving-provider config.

### Database lane
- MySQL 8.4.13, MariaDB 11.8.9, PostgreSQL 18.6, Redis 8.8.3 pinned by digest.
- Loopback-only bridge ports, no-new-privileges, SELinux `:z`.
- Host-owned config/data, 0600/0700 random credentials, data/secret consistency checks.
- MySQL/MariaDB admin grants, PostgreSQL CREATEDB/CREATEROLE admin bootstrap.
- Live smoke: all four engines, admin DDL, secret login, persistence, cleanup.

### Web-server lane
- nginx 1.30.6 and httpd 2.4.69 host-network containers using the official images.
- Full container vhosts: static/PHP/Node, TLS, force-HTTPS, stopped 503, default 404,
  dashboard vhost, SPA fallback, configurable PHP front controller, Apache `.htaccess`.
- PHP host-path mapping for container roots; FastCGI Cookie/Auth/Host/UA/Accept headers.
- Base seed only, generated vhost conflict/backup/detach, custom includes untouched.
- Docker provider prepare/commit/rollback, Docker→Docker switching, explicit
  Docker→builtin switch, native-server port probe, guard on direct deactivation.
- Live smoke: nginx and Apache config tests and Laravel-style routes, SPA, 503, dashboard,
  force-HTTPS/www, Node proxy.

### Developer-tooling lane
- Global PHP CLI default/ini/shims and Composer 2.10.3 pinned PHAR/wrapper.
- Site shell, `exec`, `shell-init`, and site PHP/Node precedence.
- Native static build executor with Node runtime selection, output containment, snapshot
  restore, logs, 4xx setup errors, and persisted `degraded` status.
- Installer/unit generation uses the actual devstack home and optional systemd paths.
- Live/unit evidence: shim/Composer execution tests, build tests, release installer test.

## Verification evidence

- `go test ./... -count=1` — pass.
- `go test ./... -race -count=1` — pass.
- `go vet ./...` — clean.
- UI `npm ci`, `npm run check`, `npm test -- --run` (62 tests), `npm run build` — pass.
- `make build` — pass.
- `bash test/release/installer_test.sh` — pass.
- `sg docker -c 'DEVSTACK_DOCKER_TESTS=1 ./test/docker/wave1-databases.sh'` — pass.
- `sg docker -c 'DEVSTACK_DOCKER_TESTS=1 ./test/docker/wave1-web.sh'` — pass.
- Shell syntax/shellcheck — pass.

## Known Wave 1 limitations

1. Dashboard service page is read-only; Wave 2 adds action/config/build/terminal panels.
2. phpMyAdmin, ImageMagick CLI, performance/host tuning/benchmarks, and credential
   rotation are Wave 2.
3. Docker web servers serve sites under the managed `projects/` root only; external sites
   remain on builtin/native serving until Wave 2 mounts.
4. Existing helper config needs `tool_root`/`tool_bin_dir` and a restart to enable global
   PHP/Composer; fresh installs get it from `install.sh`.
5. The official MySQL entrypoint can retain password values in the final container process
   environment; Compose files/logs never contain them and ports are loopback-only.
6. No live root install/systemd activation was performed on the reference host; installer
   and container smokes were used.

## Review trail

- Wave 0 review: [Wave 0 review](2026-10-25-wave0-review.md).
- Wave 1 plans: [plan](../plans/2026-10-25-service-platform-wave1.md).
- Lane reviews and fix rounds are recorded in the SDD ledger/workspace; each lane had a
  fresh implementer and independent review, with live smoke or explicit skip evidence.
