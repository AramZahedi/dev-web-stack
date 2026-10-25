# Project handoff

- **Date:** 2026-10-25
- **Repository:** `https://github.com/AramZahedi/dev-web-stack`
- **Handoff commit:** the commit containing this file; run `git rev-parse HEAD` for the exact SHA.
- **Branch:** `master`
- **Product status:** Linux M1 released as `v0.1.0`; the managed Docker service platform is designed and **not implemented**.
- **Next step:** approve the service-platform design, then write and execute the implementation plan.

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
- Fedora 44 Docker image, builtin-only, tested locally with host ports 8080/8443/7070.
- Existing tests and CI pass for M1. The latest docs-only spec commit may be checked with
  `gh run list`.

### Released artifacts

- App `v0.1.0`: `https://github.com/AramZahedi/dev-web-stack/releases/tag/v0.1.0`
  — tag `4f33469`; archive and checksums.
- PHP runtime `php-runtimes-2026-10-25`:
  `https://github.com/AramZahedi/dev-web-stack/releases/tag/php-runtimes-2026-10-25`
  — PHP 8.2.35, 8.3.36, 8.4.27, 8.5.12 with checksums/SBOMs.
- Docker image: local `devstack:0.1.0-docker` and `devstack:latest`; not pushed to a
  registry.

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
- Docker installed/enabled; user `aram` in `docker` group (existing shell may need
  `sudo docker` until re-login).
- Host nginx owns 80/443; it is unrelated to the container.
- No native `~/.devstack`; `devstack-helper` is inactive.
- Running Docker container:

  ```text
  devstack  devstack:0.1.0-docker  Up (healthy)
  127.0.0.1:8080 -> 80
  127.0.0.1:8443 -> 443
  127.0.0.1:7070 -> 7070
  ```

- Docker image sizes: 342 MB.
- Host `/etc/hosts` devstack block (test artifacts):

  ```text
  # BEGIN devstack
  127.0.0.1 blog.test
  127.0.0.1 devstack.test
  127.0.0.1 nodeapp.test
  127.0.0.1 phpapp.test
  127.0.0.1 secure.test
  # END devstack
  ```

- Hosts backup from before the Docker test:
  `/tmp/opencode/hosts.before-devstack-container` (temporary; may disappear).
- Persistent Docker data: `.docker-data/` (~1.5 GB), ignored by git:
  - daemon state, CA, logs, SQLite;
  - PHP 8.4.27, PHP 8.5.12, Node 22.23.4.
- Test projects in `projects/`: `blog`, `php`, `nodeapp`, `secure` (ignored by git).
- Reference container CA fingerprint:
  `67:CB:B2:8C:F0:89:37:5E:0F:82:C5:DA:15:59:FD:69:D4:5A:FD:A9:F8:92:5D:5F:3F:30:03:11:DC:CF:DC:66`.
- Existing host commands:
  - `/usr/local/bin/composer` (user-owned) already exists;
  - `/usr/local/php/bin/php` is on `PATH`; `/usr/local/bin/php` is absent.
  The planned global PHP/Composer shims must detect and handle these conflicts.
- No database services, Redis, or phpMyAdmin are installed for devstack.

### Docker image test evidence

The local image was verified with:

- Fedora 44;
- dashboard health on 7070;
- static site on host 8080/8443;
- PHP 8.4.27 and Node 22.23.4 sites;
- HTTPS certificate verification with the exported CA;
- force-HTTPS redirect preserving port 8443;
- container restart/persistence;
- graceful stop/start;
- an alternate build on host ports 18080/18443/17070;
- custom public-port build args.

The Docker image is builtin-only and does not include the planned service containers.

## 4. Known issues

See [known issues](known-issues.md) for details. The important ones:

1. Docker external nginx switch fails:
   `dashboard backend port must be 7070, got 7071`. This is a Docker internal-port
   mismatch; Docker docs already say builtin-only. Use native devstack for external mode.
2. Docker CA is not trusted in host browsers.
3. Docker dashboard link ports are baked at build time.
4. Host ports are localhost-only by default.
5. Manual Linux M1 gates remain open.
6. The service platform is not implemented.
7. No automatic `~/.devstack` → `/data/devstack` migration.

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

### 5.4 Docker image

```sh
docker compose up -d --build
docker compose ps
docker compose logs -f devstack
```

UI: `http://localhost:7070`; sites: `http://<name>.test:8080`,
`https://<name>.test:8443`. Do not copy `.docker-data` to a new machine unless you intend
to preserve its CA/state; a fresh root is usually safer. The Docker image is not the
service-platform implementation.

### 5.5 Next implementation work

1. Approve the service-platform design.
2. Write the implementation plan with the `writing-plans` workflow.
3. Implement in the order in §11 of the design: root/service state/helper Compose;
   databases/credentials; web servers/vhost parity; config conflicts; CA; performance;
   PHP/Composer/builds; dashboard/CLI; acceptance.
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

- Do not commit `.docker-data/`, `projects/`, runtime archives, certificates, passwords,
  or generated configs.
- Current Docker test CA private key lives in `.docker-data/helper/pki/`; do not copy it
  into the repo or share it.
- The planned database credentials will live under `/data/devstack/secrets` with `0600`;
  they are not present in the current release.
- The reference host has no devstack database credentials.
- If you preserve a CA/state volume on a new machine, treat it as sensitive and update
  trust stores deliberately.

## 8. Useful commands

```sh
# status
git status
docker compose ps
devstack daemon status
devstack doctor

# logs
docker compose logs -f devstack
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

# Docker cleanup (reference machine only)
docker compose down
# optionally remove .docker-data and the devstack hosts block manually
```

## 9. Release/CI notes

- M1 app CI and release workflow passed at the release tip.
- PHP build/smoke CI passed for all four PHP lines.
- The current docs commit may have a docs-only CI run; check:
  `gh run list --repo AramZahedi/dev-web-stack --workflow ci.yml --limit 3`.
- No release tag should be moved or reused.
- PHP catalog entries must never be invented; only real published checksums/sizes.

## 10. Open items for the next agent

- Approve/execute the service-platform plan.
- Choose the open implementation details listed in the design §20 and decisions §4.
- Resolve Docker 7071/7070 mode UX separately; do not conflate it with the native
  service-platform design.
- Run the manual Linux M1 gates if claiming full M1.
- Do not start Windows/WSL, arm64, signed packages, auto-update, or M4 work.
