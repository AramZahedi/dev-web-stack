# Project handoff

- **Date:** 2026-10-25
- **Repository:** `https://github.com/AramZahedi/dev-web-stack`
- **Handoff commit:** the commit containing this file; run `git rev-parse HEAD` for the exact SHA.
- **Branch:** `master`
- **Product status:** Linux M1 released as `v0.1.0`; Wave 0 foundation and Wave 1 service/database/web/tooling work are implemented, reviewed, and merged into the integration branch.
- **Wave 0 Merge SHA:** `04d1a2d5f111b33995961a90b19cf8df6abaa489`
- **Wave 0 CI run:** `https://github.com/AramZahedi/dev-web-stack/actions/runs/40200118349` (green)
- **Wave 1 integration branch:** `integration/services-wave-1`
- **Wave 1 merge SHA / CI:** filled by the final merge/CI record in this file.
- **Next step:** Wave 2 — phpMyAdmin, performance/host tuning, and the dashboard action/config/build/terminal panels. See the
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
  - service lifecycle API/CLI, credential reveal (rotation deferred), and read-only
    dashboard service listing.
  - Live Docker smoke suites for databases and nginx/Apache pass.

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

Wave 1 implements the core service containers, web serving, database credentials, native
shells, Composer, and static builds. Wave 2 still adds:

- the phpMyAdmin container and credential rotation/UI;
- ImageMagick CLI tool management;
- resource-aware nginx/Apache/PHP performance profiles, reversible host tuning, and
  benchmark storage;
- full dashboard actions for services, config diff/apply, credentials, builds, and
  scoped command/terminal execution;
- external-site bind mounts for Docker web servers (sites outside the managed
  `projects/` root currently require builtin or native serving).

**Do not tell users the Wave 2 items are shipped.**

## 3. Current reference workstation

This is the machine state at handoff. It is not required on the next machine.

- Fedora Linux 44 x86_64, user `aram`, uid/gid 1000.
- Repo: `/data/wwwroot/development/python/dev-web-stack`.
- Go 1.27.3, Node 22.23.1/npm 10.9.8 (observed), Docker Engine 29.8.2.
- Docker installed/enabled and the user is in the `docker` group. Docker is retained for
  the future service-container milestone; no devstack container runs now.
- Host nginx owns 80/443; it is unrelated to devstack.
- No native `~/.devstack`; `devstack-helper` is inactive.
- The old `devstack` container, `devstack:0.1.0-docker`/`latest` images, `.docker-data`,
  test `projects/`, and the devstack `/etc/hosts` block were removed on 2026-10-25.
- Removed runtime state was backed up at:
  `/tmp/opencode/devstack-old-docker-20261025T111246Z/`
  including hosts, projects, CA/helper state, daemon state, and the old CA fingerprint.
- Existing host commands:
  - `/usr/local/bin/composer` (user-owned) already exists;
  - `/usr/local/php/bin/php` is on `PATH`; `/usr/local/bin/php` is absent.
  The planned global PHP/Composer shims must detect and handle these conflicts.
- No database services, Redis, or phpMyAdmin are installed for devstack.
- No test sites are registered on the host.

## 4. Known issues

See [known issues](known-issues.md). Important current points:

1. Wave 2 remains: phpMyAdmin, ImageMagick CLI, performance/host tuning, credential
   rotation, external-site Docker mounts, and full dashboard action/terminal panels.
2. The dashboard service page is read-only; lifecycle/config/build/credential actions are
   API/CLI only until Wave 2.
3. Manual Linux M1 gates remain open.
4. No automatic `~/.devstack` → `/data/devstack` migration.
5. Existing helper configs need `service_root`, `tool_root`, and `tool_bin_dir` (and a
   restart) for service containers and global PHP/Composer; fresh installs get them from
   `install.sh`.
6. The official MySQL entrypoint retains its password values in the final container
   process environment; Compose files and logs do not contain them. This is documented.
7. The old standalone Docker image is retired; do not recreate it.

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

Waves 0 and 1 are complete. Do not re-run their kickoffs.

1. Read the [Wave 1 plan](superpowers/plans/2026-10-25-service-platform-wave1.md) for the
   implemented contracts, and the design spec for Wave 2 behavior.
2. Plan and execute Wave 2: phpMyAdmin/credential rotation, performance profiles and
   reversible host tuning, ImageMagick CLI, external-site Docker mounts, and the remaining
   dashboard action/config/build/terminal panels.
3. Use the [program plan](superpowers/plans/2026-10-25-service-platform-program.md) for the
   worktree/merge mechanism and Wave 2–3 parallelism.
4. Preserve existing M1 and Wave 1 behavior/tests.
5. Keep the native external provider compatible.
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

- M1 app CI and release workflow passed at the release tip.
- PHP build/smoke CI passed for all four PHP lines.
- The current docs commit may have a docs-only CI run; check:
  `gh run list --repo AramZahedi/dev-web-stack --workflow ci.yml --limit 3`.
- No release tag should be moved or reused.
- PHP catalog entries must never be invented; only real published checksums/sizes.

## 10. Open items for the next agent

- Wave 0 foundation (F1–F6) is complete and verified green on CI.
- Review Wave 0 implementation and proceed with Wave 1 planning.
- Choose the open implementation details listed in the design §20 and decisions §4.
- Run the manual Linux M1 gates if claiming full M1.
- Do not start Windows/WSL, arm64, signed packages, auto-update, or M4 work.
- Do not recreate the old standalone Docker image.
