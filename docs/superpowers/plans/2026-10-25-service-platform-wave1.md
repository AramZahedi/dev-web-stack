# Service Platform Wave 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development
> (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps
> use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the service platform usable: database containers, Docker nginx/Apache
serving, native PHP/Composer/site shells, and native static builds, with API/CLI parity and
strict active-service conflict handling.

**Architecture:** Wave 0 froze the root layout, service state, Compose projection, config
conflict engine, and site contracts. Wave 1 first extends the service control plane in
three serial tasks (contracts, helper controller, manager/API/CLI). It then runs three
independent lanes in isolated worktrees: databases, web servers, and developer tooling.
The lanes merge through `integration/services-wave-1`; Wave 2 adds phpMyAdmin, performance,
and the dashboard panels.

**Tech Stack:** Go 1.26, SQLite (`modernc.org/sqlite`), Svelte 5/Vite/TypeScript, Docker
Engine + Compose, systemd, native PHP/Node runtimes, shell scripts.

**Spec:** `docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md`

## Global Constraints

- devstack itself stays native; Docker is only for optional service containers.
- No native `dnf`/`apt` or systemd management for nginx, Apache, databases, Redis, or
  phpMyAdmin.
- Every optional service is a separate container; no all-in-one tools container.
- Exactly one web server (nginx/Apache) and one MySQL-family service (MySQL/MariaDB) may
  be active at a time. PostgreSQL and Redis are independent.
- Data services and phpMyAdmin bind to `127.0.0.1` by default. Web-server containers use
  Docker host networking and own 80/443.
- Native CA/trust is authoritative; containers receive leaf certificates only.
- No silent config overwrite; generated files use conflict/diff/backup/validation.
- `/data/devstack` is the recommended configurable root; existing `~/.devstack` remains
  valid. No automatic root migration.
- PHP and Node remain native. PHP `imagick` stays bundled in the PHP runtime.
- Global `php`/`composer` use the machine-wide CLI default outside a site shell; site
  shells override with the site PHP/ini.
- Dashboard parity: every operational task has a dashboard and CLI path. Wave 1 must
  expose API/CLI for every operation; Wave 2 completes the dashboard panels.
- Existing M1 behavior and the native external provider remain compatible.
- Linux x86_64 only; Windows/WSL Plan 3 remains deferred.
- No invented image digests, runtime checksums, or URLs. Pinned values must come from real
  published artifacts/manifests.
- No secret, service data, site data, certificate, or runtime archive in git.
- Maximum three implementation worktrees active at once. No task branch merges directly
  to `master`.

## Review Focus

These are the failure modes most likely to hurt a user and must be pinned by tests in the
owning task:

1. A service switch leaves 80/443 or 3306 owned by both the old and new service, or by a
   native process.
2. A database starts with a missing/empty secret file, reuses shared credentials, or
   exposes a port beyond loopback.
3. Docker vhost generation serves source files, the wrong framework route, or the
   dashboard is stranded after switching to a container web server.
4. Global PHP/Composer silently uses the wrong PHP, or `devstack shell` leaks the global
   default into a site.
5. A failed build or config apply deletes the last good output/config instead of
   restoring it.

---

## Serial prerequisite P1: Service contracts v2

**Files:**
- Modify: `internal/services/catalog.go`
- Create: `internal/services/catalog_composition.go` (catalog lane stubs)
- Modify: `internal/services/compose.go`
- Modify: `internal/services/state.go`
- Modify: `internal/services/coordinator.go`
- Modify: `internal/store/services.go`
- Tests: `internal/services/*_test.go`, `internal/store/service_state_test.go`

**Interfaces:**
- `Service` gains:
  - `Environment map[string]string`
  - `Healthcheck *Healthcheck`
  - `ValidateCommand []string`
  - `ReloadCommand []string`
  - `SecurityOpts []string`
  - `CapAdd []string`
  - `Ulimits []Ulimit`
- `Healthcheck` fields: `Test []string`, `Interval`, `Timeout`, `StartPeriod time.Duration`,
  `Retries int`.
- `Ulimit` fields: `Name string`, `Soft int`, `Hard int`.
- `Mount` gains `Area MountArea` and `HostPath string`; existing `HostSubdir` remains.
  Areas: `service`, `secrets`, `certs`, `projects`, `absolute`.
- `DefaultCatalog()` is composed from `webServices()`, `databaseServices()`, and
  `standaloneServices()`; each lives in its own file so lanes do not edit `catalog.go`.
- `RenderCompose` enforces `repository@sha256:<64 hex>`, rejects unknown dependencies,
  renders healthcheck/environment/security options/capabilities/ulimits, preserves
  deterministic ordering, and emits `:z` for SELinux mounts.
- `StateInstalledButStopped State = "installed_but_stopped"`.
- `View` gains `Health string`, `ContainerID string`, `Installed bool`.
- `services.ErrNotFound` is returned by the store for a missing service state, so
  `Coordinator.Get` never compares error strings.

**Tests:**
- Compose rejects a floating/`latest` image and accepts real pinned fixtures.
- Compose renders host-network web services without published ports, loopback DB ports,
  secrets/certs/projects mounts, healthchecks, and `:z`.
- Catalog rejects duplicate IDs, unknown dependencies, and an invalid group.
- Coordinator returns `services.ErrNotFound` for unknown IDs without string matching.
- Store round-trips every new state and preserves records for unknown catalog IDs.

**Commit:** `feat: extend service catalog and compose contracts`

---

## Serial prerequisite P2: Docker service controller

**Files:**
- Modify: `internal/helper/service_docker.go`
- Create: `internal/helper/service_backup.go`
- Modify: `internal/helper/services.go`
- Modify: `internal/helper/server.go`
- Modify: `internal/helperproto/services.go`
- Modify: `internal/helperclient/client.go`
- Tests: helper/service tests

**Interfaces:**
- `ServiceController` adds `Restart`, `Validate`, `Reload`.
- Helper routes:
  - `POST /v1/services/restart`
  - `POST /v1/services/validate`
  - `POST /v1/services/reload`
- `DockerServiceController` uses only fixed argv:
  - `docker compose -p <hash> -f <root>/services/compose.yaml ps -a --format json`
  - `docker image inspect <catalog image>`
  - `pull`, `up -d --no-deps`, `stop`, `restart`, `rm -f`
  - `exec -T <id> <catalog ValidateCommand|ReloadCommand>`
- `Status` reports not installed, installed, installed_but_stopped, starting/active, error;
  health, container ID, ports, last error; Docker/Compose errors are returned, never
  silently converted into “not installed”.
- `Install` creates catalog-owned `services/<id>/{config,data,logs}` directories and pulls
  the image; it never activates.
- `Activate`: verifies the catalog ID/image; stops every active member of the same
  exclusive group; checks required host ports are free; starts the target and waits for
  health/port ownership; on failure stops the target, restarts the previous active member,
  and returns a structured error.
- `Deactivate` stops only the target; web-server deactivation safety is enforced by the
  daemon provider layer.
- `Uninstall(deleteData=true)` creates a verified tar.gz under
  `backups/services/<id>/<timestamp>/` before removing only catalog-owned service and
  secret directories. If backup/verification fails, nothing is deleted.
- `Validate`/`Reload` refuse when no catalog command exists.
- Add clock/sleeper/probe seams so unit tests do not sleep or need Docker.

**Tests:**
- Exact argv for every operation; reject unknown IDs; no shell strings.
- Status maps running/created/exited/image-only states correctly.
- Activation of MySQL stops active MariaDB and restarts it on target failure.
- A failed health check rolls back; a native port conflict is reported without killing it.
- Delete-data aborts on backup failure and creates a non-empty archive on success.
- Validate/reload use catalog commands only.

**Commit:** `feat: add transactional docker service control plane`

---

## Serial prerequisite P3: Service manager, API, CLI, provider config

**Files:**
- Create: `internal/servicecontrol/manager.go`
- Create: `internal/servicecontrol/preparer.go`
- Create: `internal/servicecontrol/manager_test.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/handlers_services.go`
- Modify: `internal/api/handlers_services_test.go`
- Modify: `internal/client/client.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/service.go`
- Modify: `internal/cli/service_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/daemon/daemon.go`
- Modify: `ui/src/lib/types.ts`, `ui/src/lib/api.ts`, `ui/src/lib/mock.ts`
- Tests as needed.

**Interfaces:**
- `config.Config.ServingProvider string` (`native` default, `docker` supported).
  Validation: `docker` requires `serving_mode` nginx/apache; existing configs without the
  field default to native.
- `servicecontrol.Manager`:
  - Deps: root, store, helper RPC, catalog, paths, config, preparer registry, clock.
  - `List/Get` merge catalog + helper status + stored state.
  - `Install/Activate/Deactivate/Restart/Uninstall/Logs/Credentials/RotateCredentials`.
  - `Preparer` interface invoked before install/activate and on rollback:
    `PrepareInstall`, `PrepareActivate`, `CleanupActivate`.
  - Persist `last_error`/`last_operation`; never persist secrets in service records.
- API:
  - `POST /api/v1/services/{id}/install|activate|deactivate|restart`
  - `DELETE /api/v1/services/{id}` with `delete_data=false` default
  - `GET /api/v1/services/{id}/logs`
  - `POST /api/v1/services/{id}/credentials/reveal|rotate`
  - Existing session/CSRF rules.
- CLI:
  - `service install|activate|deactivate|restart|uninstall|logs|credentials`.
  - `--delete-data`, `--yes`, `--json` behave consistently with existing CLI.
- UI types/API functions expose health, container ID, installed state; Wave 2 adds the
  full action UI.
- Daemon wires the manager with the real helper, root paths, and provider hooks.

**Tests:**
- Manager saves installed/active/error state and never leaks credentials in list JSON.
- API action routes reject missing IDs, require CSRF, and map helper errors.
- CLI command parsing and JSON output.
- Config legacy `serving_provider` defaults to native; docker+builtin is rejected.

**Commit:** `feat: add service manager api and cli`

---

## Phase A: Database services (parallel lane A)

### Task A1: Catalog, credentials, and config seeds

**Files:**
- Create: `internal/services/catalog_database.go`
- Create: `internal/credentials/credentials.go`
- Create: `internal/servicecontrol/database.go`
- Create: `internal/credentials/credentials_test.go`
- Create: `internal/servicecontrol/database_test.go`
- Test fixtures under `internal/servicecontrol/testdata/`

**Exact catalog:**
- mysql: `mysql:8.4.13@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69`
- mariadb: `mariadb:11.8.9@sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa`
- postgresql: `postgres:18.6@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6`
- redis: `redis:8.8.3@sha256:418e397fd48d66e450af13baab123397f2d2580b07ef310c3a5dbe96c3d731f1`
- MySQL/MariaDB/PostgreSQL ports loopback 3306/3306/5432; Redis 6379.
- Secrets under `secrets/<engine>/...`, mode 0600; directories 0700.
- Credentials are 32 random bytes encoded hex; refuse overwrite; data-directory/secret
  inconsistency is an explicit error.
- Database config seeds:
  - MySQL/MariaDB `config/devstack.cnf` with `skip-name-resolve`, `max_connections`,
    `character-set-server=utf8mb4`, and no `bind-address=127.0.0.1` inside the container.
  - PostgreSQL `config/postgresql.conf` + `pg_hba.conf` + bootstrap shell that reads the
    devstack password secret; data in `/var/lib/postgresql/data`.
  - Redis `config/redis.conf` with loopback publishing and appendonly yes.
  - Compose environment uses `*_PASSWORD_FILE`/mounted secret files, never literal values.

### Task A2: Activation, switching, and live smoke

**Files:**
- Modify: `internal/servicecontrol/database.go`
- Create: `test/docker/wave1-databases.sh`
- Tests: fake helper/runner and optional Docker smoke.

**Behavior:**
- Activating MySQL/MariaDB stops the other engine through P2; never both active.
- Data survives deactivate/reactivate; secrets survive container replacement.
- PostgreSQL and Redis can run alongside the active MySQL family.
- Deletion requires backup; no direct `RemoveAll`.
- Live smoke uses `sg docker -c` or direct Docker only when available; high host ports for
  tests; verify connection with native `psql`/`redis-cli` if installed, otherwise container
  client. Never touch real `/etc/hosts` or ports 3306/5432/6379 in tests.

**Commit:** `feat: add database service containers and credentials`

---

## Phase B: Docker web servers (parallel lane B)

### Task B1: Web catalog and container vhost renderer

**Files:**
- Create: `internal/services/catalog_web.go`
- Create: `internal/dockerweb/render.go`
- Create: `internal/dockerweb/render_test.go`
- Create: `internal/dockerweb/config.go`
- Create: `internal/dockerweb/config_test.go`

**Exact catalog:**
- nginx: `nginx:1.30.6@sha256:a7675fcee220f59b4a99f7098c06a0d5d999c2c0ee37c0211efb780536e5cb20`
  — host network, ports 80/443 for status, mounts config/conf.d/logs/projects/certs.
- apache: `httpd:2.4.69@sha256:a30cd4cd57f3f3c3be9b2d16384d2488839996bb14c67df2b51f1ecbb3e584a1`
  — host network, generated full `httpd.conf`, generated/custom conf.d, logs/projects/certs.
- Use official images; do not invent a wrapper image. If the official Apache image cannot
  load the required modules, the task must fail and report before adding a wrapper.

**Renderer:**
- Deterministic nginx/Apache vhosts for static/PHP/Node, TLS, force HTTPS, stopped 503,
  branded default 404, and `devstack.test` dashboard vhost.
- Container document root `/sites/<id>`; native PHP `SCRIPT_FILENAME` is the host project
  path; Node proxies `127.0.0.1:<port>` with WebSocket headers.
- Static SPA fallback uses `spa_fallback_file`; PHP front controller uses
  `front_controller_file`; Apache `AllowOverride` follows `apache_allow_override`.
- Apache `.htaccess` is authoritative when mode is `htaccess`/`auto` and file exists;
  generated fallback only when absent. `.htaccess` is never part of the config manifest.
- Generated files include service/site/hash header; no user directives interpolated.
- Config writer seeds base config only when absent, uses the F4 manifest, backups, and
  refuses to overwrite modified generated files.

### Task B2: Docker serving provider and switching

**Files:**
- Create: `internal/dockerweb/provider.go`
- Create: `internal/dockerweb/provider_test.go`
- Create: `internal/servicecontrol/web.go`
- Modify: `internal/sites/service.go`, `internal/sites/vhosts.go`
- Modify: `internal/helper` only through existing service hooks; no arbitrary commands.

**Behavior:**
- `serving_provider=docker` routes nginx/apache activation through the service manager.
- Activation stops builtin forwarders and the native provider conflict, writes configs,
  starts the container, verifies health and 80/443 ownership, and rolls back on failure.
- Switching Docker → builtin stops the container, restores forwarders/proxy, and keeps
  sites/certs.
- Deactivating/uninstalling the active Docker web server is refused until another serving
  mode is active.
- The dashboard vhost is mandatory; switching must not strand `devstack.test`.
- Native provider behavior and all M1 vhost tests remain unchanged when provider=native.

### Task B3: Laravel fixture, `.htaccess`, and live web smoke

**Files:**
- Create: `test/fixtures/laravel-min/public/{index.php,.htaccess}`
- Create: `test/docker/wave1-web.sh`
- Tests: renderer/provider with a fake helper and an optional live Docker run.

**Acceptance:**
- Root route, deep route, query string, POST, static asset, framework 404.
- Apache runs once with `.htaccess` and once with it disabled.
- HTTPS/forwarded headers work; no HSTS; stopped site returns branded 503.
- Static Nuxt-style output served with SPA fallback; missing assets stay 404.

**Commit:** `feat: add docker web servers and vhost parity`

---

## Phase C: Developer tooling (parallel lane C)

### Task C1: Global PHP and Composer

**Files:**
- Create: `internal/tooling/php.go`
- Create: `internal/tooling/composer.go`
- Create: `internal/tooling/*_test.go`
- Create: `internal/helper/tool.go`
- Modify: `internal/helper/server.go`, `internal/helperproto/*`, `internal/helperclient/*`
- Modify: `internal/cli/php.go`, `internal/cli/root.go`
- Modify: `internal/api/server.go`, handlers, daemon wiring.

**Behavior:**
- Global PHP default: line or exact installed build; persisted setting; no silent download.
- Global `config/cli/php.ini` generated from the selected build defaults; edit via
  API/CLI; `php-config`/`phpize` shims when present.
- `/usr/local/bin/php` and `/usr/local/bin/composer` wrappers installed by helper; existing
  files require `--force` and are backed up first.
- Composer 2.10.3 PHAR from
  `https://getcomposer.org/download/2.10.3/composer.phar`
  SHA-256 `7a2d379d5b8ffdaa028580ef26494c36d2feef4b178d3dd1473a4dbc5e17c8d6`; no
  fallback to another PHP inside a site; wrapper executes PHAR with the selected PHP.
- Doctor reports PATH shadowing and missing shims.

### Task C2: Site shell, exec, and native static builds

**Files:**
- Create: `internal/tooling/siteenv.go`
- Create: `internal/tooling/siteenv_test.go`
- Create: `internal/build/build.go`
- Create: `internal/build/build_test.go`
- Modify: `internal/cli/site.go`, `internal/cli/root.go`
- Modify: `internal/api/server.go`, handlers
- Modify: `internal/sites/service.go` only where create/start semantics need build output.

**Behavior:**
- `devstack shell [--site]` resolves by cwd or explicit site; ambiguous/unregistered lists
  sites; prepends site PHP/Node shims; sets `PHPRC`, `DEVSTACK_SITE`, `DEVSTACK_HOME`;
  never changes global shell.
- `devstack exec [--site] -- <cmd...>` runs with the same environment.
- `devstack shell-init` prints exports only.
- Build: `devstack site build <name>` runs `build.command` with the selected Node runtime
  in the project root; validates output inside the project; snapshots prior output; on
  failure restores it and marks the build failed; streams logs; API/CLI parity.
- A static site whose configured output is missing is created stopped, not falsely running.
- Nuxt/Vite-style fixture builds with the selected Node; no service-container dependency.

**Commit:** `feat: add global php composer shell and static builds`

---

## Integration and exit

1. Merge P1 → P2 → P3 into `integration/services-wave-1`, running focused tests after each.
2. Create A/B/C worktrees from the merged tip; implement and review each task.
3. Merge lanes in order A → B → C, running focused tests after each.
4. Run `go vet ./...`, `go test ./... -count=1`, `go test ./... -race -count=1`,
   UI check/unit/build, `make build`, shell syntax/shellcheck, and Docker smoke when
   accessible.
5. Merge `integration/services-wave-1` to `master`, push, wait for CI green.
6. Update `docs/handoff.md` and `docs/architecture.md`; remove worktrees; write the Wave 1
   status report.

**Stop conditions:** real Docker unavailable for a required live gate; a required image
digest/checksum cannot be verified; a destructive operation would touch real host data;
or a task cannot be implemented without changing the approved design.

## Handoff to Wave 2

Wave 2 consumes, not redefines: service manager API/CLI and helper controller; database
credentials/secrets and active engine state; Docker web provider and generated config
paths; tooling/shell/build APIs; `serviceconfig.Manager` conflict flow. Wave 2 adds
phpMyAdmin, performance profiles/host tuning/benchmarks, and the complete dashboard panels.
