# Project handoff

- **Date:** 2026-10-25
- **Repository:** `https://github.com/AramZahedi/dev-web-stack`
- **Handoff commit:** the commit containing this file; run `git rev-parse HEAD` for the exact SHA.
- **Branch:** `master`
- **Product status:** Linux M1 released as `v0.1.0`; the managed Docker service platform is
  designed and approved for implementation, but **not implemented**.
- **Next step:** execute Wave 0 using the kickoff and foundation plan links below.

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

The old standalone devstack-in-Docker image is **retired**. It was removed from the repo
and reference machine on 2026-10-25. Docker is reserved for the planned separate service
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

It adds:

- separate Docker containers for nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis,
  phpMyAdmin, and ImageMagick CLI;
- a privileged-helper-controlled fixed Compose project;
- `/data/devstack` host-owned config/data/certs/secrets;
- one-active enforcement for web servers and MySQL/MariaDB;
- editable configs with conflict/diff/backup/validation;
- native CA/trust with leaf certs mounted into web servers;
- Apache `.htaccess`, configurable front controllers, Laravel support;
- Nuxt/Vite static build profiles, output directories, SPA fallback;
- global PHP and Composer with a machine-wide CLI default;
- dashboard parity, builds, credentials, config editor, project terminal;
- resource-aware nginx/Apache/PHP performance and reversible host tuning;
- full dashboard vhost and web-server parity.

**No code for these features exists yet.** Do not tell users it is shipped.

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

1. The service platform is not implemented.
2. Manual Linux M1 gates remain open.
3. No automatic `~/.devstack` → `/data/devstack` migration.
4. The old standalone Docker image is retired; do not recreate it.

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

1. Read the [Wave 0 kickoff](superpowers/plans/2026-10-25-service-platform-wave0-kickoff.md).
2. Execute the [foundation plan](superpowers/plans/2026-10-25-service-platform-foundation.md)
   in one serial worktree.
3. Use the [program plan](superpowers/plans/2026-10-25-service-platform-program.md) for the
   parallel worktree/merge mechanism in Waves 1–3.
4. Preserve existing M1 behavior and tests.
5. Keep the existing native external provider compatible.
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
- The planned database credentials will live under `/data/devstack/secrets` with `0600`;
  they are not present in the current release.
- The reference host has no devstack database credentials.

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

- Execute Wave 0.
- Choose the open implementation details listed in the design §20 and decisions §4.
- Run the manual Linux M1 gates if claiming full M1.
- Do not start Windows/WSL, arm64, signed packages, auto-update, or M4 work.
- Do not recreate the old standalone Docker image.
