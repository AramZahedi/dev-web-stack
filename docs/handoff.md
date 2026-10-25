# Project handoff

- **Date:** 2026-10-26
- **Repository:** `https://github.com/AramZahedi/dev-web-stack`
- **Handoff commit:** the commit containing this file; run `git rev-parse HEAD` for the exact SHA.
- **Branch:** `master`
- **Product status:** Linux M1 released as `v0.1.0`; Waves 0–1 and the Wave 2 phpMyAdmin/dashboard implementation are merged. The performance engine is implemented but not wired. The next batch is documented in the [Next Batch Plan](superpowers/plans/2026-10-26-service-platform-next-batch.md): service config editing, performance apply/reset/benchmark + dashboard, site terminal, editable listener ports, and UI cleanup. ImageMagick CLI is descoped. Automatic GitHub Actions are disabled; workflows are manual-dispatch only.
- **Wave 0 Merge SHA:** `04d1a2d5f111b33995961a90b19cf8df6abaa489`
- **Wave 0 CI run:** `https://github.com/AramZahedi/dev-web-stack/actions/runs/40200118349` (green)
- **Wave 1 merge SHA:** `b4be089429b3f63cb7ec94d5c6bf484a4db34da2`
- **Wave 1 CI run:** `https://github.com/AramZahedi/dev-web-stack/actions/runs/40214920181` (green)
- **Wave 2 merge SHA:** `ec2dca4523bf40d931d3e674001d6e832e5e1903`
- **Wave 2 CI run:** `https://github.com/AramZahedi/dev-web-stack/actions/runs/40222270689` (green; includes the renewal-test race fix)
- **Post-Wave-2 code:** `e2ced65` (install/activate action color distinction); documentation continuation at `8c57b2e`.
- **Next step:** execute the [Next Batch Plan](superpowers/plans/2026-10-26-service-platform-next-batch.md) from [Continuation State](superpowers/status/2026-10-26-continuation.md). See also the
  [Wave 1 plan](superpowers/plans/2026-10-25-service-platform-wave1.md),
  [Wave 0 review](superpowers/status/2026-10-25-wave0-review.md), and
  [service-platform design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md).

This is the continue-on-another-machine document. Read it first, then
[architecture](architecture.md), [requirements](requirements.md), [decisions](decisions.md),
and the [service-platform design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md).

## 1. What is implemented

- Native Linux CLI/daemon/helper.
- Static, Node, and PHP sites with `.test` hostnames and TLS.
- PHP 8.2–8.5 checksum-pinned runtime catalog; per-site PHP version/build, ini, extensions,
  FPM masters, rollback.
- Node LTS runtime catalog; per-site command supervision; HTTP/WebSocket proxy.
- Builtin Go proxy and optional native nginx/Apache vhost modes.
- Local CA, OS/browser trust install/repair, managed hosts block.
- Embedded Svelte dashboard.
- SQLite state, migrations, backups before migration.
- Checksummed unsigned Linux release, installer, upgrade path.
- Existing tests and CI pass for M1.
- Wave 0 Foundation (tasks F1–F6): root layout/permissions (`/data/devstack`), service catalog & state store with SQLite migration `0004_services.sql`, config manifest/diff/backup/conflict engine, Compose rendering & fixed privileged helper control plane, site config contracts & build profiles with SQLite migration `0005_site_contracts.sql`, and read-only service API/CLI/UI skeleton.
- Wave 1 service platform:
  - pinned Docker catalog entries for nginx 1.30.6, Apache httpd 2.4.69, MySQL 8.4.13,
    MariaDB 11.8.9, PostgreSQL 18.6, and Redis 8.8.3, all digest-pinned;
  - transactional helper controller with exclusive web/MySQL-family activation, health
    waits, port ownership checks, rollback, restart, validate/reload, logs, and
    backup-verified data deletion;
  - host-owned database config/data/secrets under the devstack root, random 0600
    credentials, admin DDL bootstrap, MySQL/MariaDB/PostgreSQL/Redis lifecycle;
  - Docker nginx/Apache provider with container vhosts for static, PHP, and Node sites,
    TLS/force-HTTPS, stopped 503, default 404, dashboard vhost, SPA fallback,
    configurable PHP front controllers, Apache `.htaccess`, and Docker→Docker/builtin
    switching;
  - native site shell/exec with site PHP/ini/Node precedence;
  - global PHP CLI default/ini/shims and managed Composer 2.10.3 PHAR;
  - native static build profiles/executor with output validation, snapshot restore, build
    logs, and degraded status;
  - service lifecycle API/CLI and credential reveal.
  - Live Docker smoke suites for databases and nginx/Apache pass.
- Wave 2 service-platform work:
  - phpMyAdmin 5.2.3 container on loopback 8082, cookie auth, MySQL-family dependency,
    active-engine follow/restart, and live Docker smoke;
  - database credential rotation for MySQL/MariaDB/PostgreSQL through the helper, with
    admin-account SQL on stdin, rollback, owner-preserving 0600 secrets, and active
    phpMyAdmin restart;
  - dashboard parity for service actions/credentials/phpMyAdmin, build profiles/run/logs,
    front-controller/`.htaccess` policy/SPA fields, global PHP/Composer/tool management,
    grouped service cards, and degraded status;
  - pure bounded performance profile engine (nginx/Apache/PHP/host formulas, includes,
    apply/reset plans, benchmark plan) — not yet wired to host tuning/UI.

The old standalone devstack-in-Docker image is **retired**. It was removed from the repo
and reference machine on 2026-10-25. Docker is reserved for the separate service
containers.

### Released artifacts

- App `v0.1.0`: `https://github.com/AramZahedi/dev-web-stack/releases/tag/v0.1.0`
  — tag `4f33469`; archive and checksums.
- PHP runtime `php-runtimes-2026-10-25`:
  `https://github.com/AramZahedi/dev-web-stack/releases/tag/php-runtimes-2026-10-25`
  — PHP 8.2.35, 8.3.36, 8.4.27, 8.5.12 with checksums/SBOMs.

## 2. What is designed but not implemented

The next milestone is fully described in:

```text
docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md
```

Waves 0–1 and the Wave 2 phpMyAdmin/dashboard work are implemented. The next batch is
planned in detail:

- service config manifest/read/diff/apply/reset API, CLI, and dashboard editor;
- performance profile API, apply/reset, helper host tuning, benchmark results, dashboard;
- site-scoped PTY terminal/command runner;
- editable HTTP/HTTPS/dashboard ports in Settings, with restart-required handling;
- remove the ImageMagick CLI placeholder (descoped), fix Native tools spacing.

Remaining after that: external-site bind mounts for Docker web servers (sites outside the
managed `projects/` root currently require builtin/native serving) and full Wave 3
acceptance/benchmark evidence.

**Do not tell users these items are shipped until implemented and verified.**

## 3. Current reference workstation

This is the machine state at handoff. It is not required on the next machine.

- Fedora Linux 44 x86_64, user `aram`, uid/gid 1000.
- Repo: `/data/wwwroot/development/python/dev-web-stack`.
- Go 1.27.3, Node 22.23.1/npm 10.9.8 (observed), Docker Engine 29.8.2.
- Docker installed/enabled and the user is in the `docker` group. Docker is retained for
  the future service-container milestone; no devstack container runs now.
- Host nginx owns 80/443; MySQL/PostgreSQL/Redis also own their default ports.
- Current test daemon: `./dist/devstack --home /home/aram/.devstack` at
  `http://127.0.0.1:7070` (version `v0.1.0-83-gcb443d8`).
- Current test helper: root process with temporary state under
  `/tmp/opencode/devstack-runtime`; hosts/trust are intentionally not modified.
- This is a test runtime, not a system install. See
  [Continuation State](superpowers/status/2026-10-26-continuation.md) for commands.
- `/usr/local/bin/composer` exists user-owned; `/usr/local/bin/php` is absent.
- Apache image is installed, Nginx is not; phpMyAdmin is installed but inactive because
  no MySQL-family service is active.
- No test sites are registered.

## 4. Known issues

See [known issues](known-issues.md). Important current points:

1. Service config editing, performance apply/reset/benchmark/UI, and site terminal are not
   implemented yet; the Services placeholders are truthful. See the Next Batch Plan.
2. ImageMagick CLI is descoped. PHP `imagick` remains available per site.
3. Automatic GitHub Actions are disabled. Workflows are manual `workflow_dispatch` only.
4. Manual Linux M1 gates remain open.
5. No automatic `~/.devstack` → `/data/devstack` migration.
6. `install.sh` adds missing `service_root`, `tool_root`, and `tool_bin_dir` keys to an
   existing helper config and restarts the helper; verify keys for manually managed configs.
7. The official MySQL entrypoint retains password values in the final container process
   environment; Compose files and logs do not contain them.
8. The old standalone Docker image is retired; do not recreate it.

## 5. Resume on another machine

### 5.1 Clone and inspect

```sh
git clone git@github.com:AramZahedi/dev-web-stack.git
cd dev-web-stack
git log --oneline -10
git status
```

Read in this order:

1. `docs/handoff.md` (this file)
2. `docs/architecture.md`
3. `docs/requirements.md`
4. `docs/decisions.md`
5. `docs/known-issues.md`
6. `docs/planned-services.md`
7. the full design spec
8. `docs/install.md`, `docs/sites.md`, `docs/php.md`, `docs/vhosts.md`, `docs/tls.md`
   for the released product

### 5.2 Build and test

```sh
make build
go test ./... -count=1
go test ./... -race -count=1
(cd ui && npm ci && npm run check && npm test -- --run && npm run build)
```

Optional full acceptance:

```sh
make acceptance
```

M1 acceptance observed **19 PASS, 0 FAIL, 1 SKIP, 1 BLOCKED**; see
[the M1 handoff](superpowers/status/2026-10-24-m1-handoff.md). The block is Windows/WSL
(deferred); the skip is manual trust/root/UI checks.

### 5.3 Native install

```sh
make build
./install.sh
devstack init
devstack doctor
```

This installs devstack, the helper, units, CA, hosts block, and starts the daemon. It
does not install nginx/Apache or packages. Use `--release 0.1.0` to install the published
archive instead.

### 5.4 Old Docker image

The old image is retired and removed. Do not recreate or test it. If another machine still
has it:

```sh
docker rm -f devstack
docker rmi devstack:0.1.0-docker devstack:latest
```

Docker Engine remains installed for the new service containers.

### 5.5 Next implementation work

Waves 0–2 are complete and merged. Do not re-run their kickoffs.

1. Read [Continuation State](superpowers/status/2026-10-26-continuation.md) and the
   [Next Batch Plan](superpowers/plans/2026-10-26-service-platform-next-batch.md).
2. Execute Phase 0 (port settings, contracts, UI cleanup), then the service-config,
   performance, and terminal backend lanes, then dashboard integration.
3. Preserve existing M1/Wave 0–2 behavior and tests. ImageMagick CLI is out of scope.
4. Keep the native external provider compatible.
5. Do not rely on GitHub CI; workflows are manual-only. Run the local gate in the plan.
6. Do not start Windows/WSL Plan 3 without explicit user instruction.

## 6. Current requirements/constraints to preserve

- Native devstack; Docker only for optional service containers.
- No native package/systemd management for tools.
- Separate service containers, not all-in-one.
- One active web server and one active MySQL-family service; both installed allowed.
- Host-owned `/data/devstack` (configurable) for config/data/certs/secrets/backups.
- Native CA/trust; containers get leaf certs only.
- All configs editable; generated vhosts conflict-protected; never silent overwrite.
- Apache `.htaccess` must work; Laravel front controller; configurable front-controller
  file; Nuxt static builds with configurable output/SPA fallback.
- Global `php`/`composer` with a machine-wide default; site shells override.
- Dashboard parity; CLI optional.
- Random per-engine DB credentials; phpMyAdmin cookie login.
- Resource-aware performance, host tuning with backup/reset.
- Existing native external mode retained; no automatic root migration.
- Linux only; Windows/WSL deferred.

## 7. Data and secrets policy

- Do not commit service data, site projects, runtime archives, certificates, passwords, or
  generated configs.
- The retired Docker CA/state backup is under `/tmp/opencode/`; treat it as sensitive and
  do not copy it into the repo. The old CA was not imported into the host trust stores.
- Database credentials are generated under `<devstack-home>/secrets/<engine>/` with
  `0600` files and `0700` directories. The reference host has no devstack database
  credentials because no service container was activated there.
- Credentials are revealed only through the explicit API/CLI path; list responses and
  logs never include them.
- The official MySQL entrypoint can retain `MYSQL_ROOT_PASSWORD`/`MYSQL_PASSWORD` in the
  final container process environment; Compose never contains literal passwords. Treat
  container-local process inspection as sensitive.

## 8. Useful commands

```sh
# status
git status
devstack daemon status
devstack doctor

# logs
journalctl --user -u devstack-daemon -f
sudo journalctl -u devstack-helper -f

# tests
make test
make lint
make ui
make e2e
make acceptance
go test ./... -race -count=1

# release
make release VERSION=0.2.0
make release-test
```

## 9. Release/CI notes

- Automatic GitHub Actions are disabled: no push/PR/tag triggers. Workflows are
  `workflow_dispatch` only (`ci.yml`, `php.yml`, `release.yml`).
- Last green automatic run before disabling: `40222677123` at `cb443d8`.
- Run the local verification gate in the Next Batch Plan; do not wait for CI.
- No release tag should be moved or reused. Releases are manual dispatch only.
- PHP catalog entries must never be invented; only real published checksums/sizes.

## 10. Open items for the next agent

- Execute the Next Batch Plan; its first task is editable listener ports + API contracts.
- Wire the existing performance engine, service config, and terminal per the plan.
- Remove ImageMagick from requirements/UI (done in docs; code/UI removal pending).
- Keep automatic CI disabled; run the local gate manually.
- Run the manual Linux M1 gates if claiming full M1.
- Do not start Windows/WSL, arm64, signed packages, auto-update, or M4 work.
- Do not recreate the old standalone Docker image.
