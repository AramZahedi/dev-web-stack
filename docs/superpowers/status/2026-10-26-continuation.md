# Continuation State — 2026-10-26

This is the current-state record for resuming the service-platform work. Read it with
[handoff.md](../handoff.md), [architecture.md](../architecture.md), and the
[Next Batch Plan](../plans/2026-10-26-service-platform-next-batch.md).

## 1. Repository state

- **Code baseline:** `master` at `cbc1605` (Wave 2 merged plus action-color and installed-state fixes).
- **Documentation baseline:** `8c57b2e` (this file and the Next Batch Plan).
- **Wave 1 merge:** `b4be089`.
- **Wave 2 merge:** `ec2dca4`.
- **Last green GitHub CI:** run `40222677123` at `cb443d8`. No automatic CI runs after the
  workflow triggers were disabled.
- **CI policy:** automatic GitHub Actions runs are now disabled. `ci.yml`, `php.yml`,
  and `release.yml` are manual `workflow_dispatch` only. Pushes, pull requests, and tags
  do not start workflows. Manual dispatch is still possible if explicitly invoked.
- **Worktrees/branches:** all implementation worktrees and feature branches were removed
  after the Wave 2 merge. Working tree is clean except for the documentation/CI-disable
  commit containing this file.
- **Tests at baseline:** Go unit/race/vet/build, UI check/unit/Playwright/build,
  installer/release tests, and the Wave 1/2 live Docker smoke suites pass.

> The code baseline is `cb443d8`. Documentation and CI-disable changes after that should
> be docs-only; rebuild binaries only when code changes.

## 2. What is implemented

### M1 foundation
Native Linux CLI/daemon/helper; static/Node/PHP sites; per-site PHP/Node runtimes;
builtin proxy and native nginx/Apache modes; local CA/trust/hosts; embedded Svelte UI;
SQLite; checksummed releases/installer/upgrade.

### Wave 0
Root layout, service catalog/state schema `0004`, config manifest/diff/backup/conflict
engine, Compose renderer, fixed helper RPC skeleton, site contracts `0005`, read-only
service API/CLI/UI skeleton.

### Wave 1
- Pinned nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis containers.
- Transactional lifecycle: install, exclusive activation, health/port checks, rollback,
  restart, validate/reload, logs, backup-before-delete.
- Host-owned DB config/data, random 0600 credentials, admin bootstrap.
- Docker nginx/Apache vhosts: static/PHP/Node, TLS, force HTTPS, SPA, PHP controller,
  Apache `.htaccess`, builtin switch, dashboard vhost.
- Global PHP/Composer, site shell/exec, native builds.
- Service API/CLI and live Docker smoke.

### Wave 2
- phpMyAdmin 5.2.3 on loopback 8082, cookie auth, MySQL-family follow/restart.
- MySQL/MariaDB/PostgreSQL credential rotation with safe SQL/stdin, rollback, owner-
  preserving secrets, phpMyAdmin restart.
- Dashboard service actions, credentials, phpMyAdmin, build profile/run/logs, site
  serving fields, PHP/Composer/tooling, degraded state.
- Pure bounded performance engine (`internal/performance`) with nginx/Apache/PHP/host
  formulas, includes, apply/reset plans, benchmark plan. **Not wired to helper/API/UI.**

## 3. Current placeholders and truthful status

The Services page still shows a “Wave 2 backend pending” section for:

- service config manifest/read/diff/apply/reset;
- performance profiles/apply/host tuning/benchmarks.

Settings still had an ImageMagick CLI placeholder. That tool is **descoped**; remove it.
The rest of the service lifecycle and dashboard build/tooling panels are live.

The dashboard is not stale. The binary was rebuilt from `cb443d8`, and the placeholder
is literal UI copy. Do not replace it with fake controls; remove it only when the backend
exists.

## 4. Current local test runtime

This is ephemeral and not part of the repository state.

- **Dashboard:** `http://127.0.0.1:7070`
- **Daemon:** running with `--home /home/aram/.devstack`
- **Binary version:** `v0.1.0-83-gcb443d8`
- **Helper:** running as root with temporary state:
  - socket: `/run/devstack-helper/helper.sock`
  - state: `/tmp/opencode/devstack-runtime/state`
  - hosts: `/tmp/opencode/devstack-runtime/hosts`
  - trust: `/tmp/opencode/devstack-runtime/trust`
  - vhosts: `/tmp/opencode/devstack-runtime/vhosts`
  - logs: `/tmp/opencode/devstack-runtime/helper.log`
  - main daemon log: `/tmp/devstack-daemon.log`
- **Why `/home/aram/.devstack`:** devstack home resolution is `--home`, then
  `$DEVSTACK_HOME`, then `~/.devstack`. It is the default test home, not a system install.
  The binaries remain in the repo `dist/`; helper was not installed as a system service.
  Recommended production-like root is `/data/devstack`, but no migration was performed.
- **Hosts/trust were intentionally not modified,** so `https://devstack.test` is not
  trusted in this runtime. Use the loopback dashboard/API.
- **Host port conflicts:** host nginx owns `80/443`, MySQL `3306`, PostgreSQL `5432`, and
  Redis `6379`. Docker service activation for those engines/ports will fail with
  `SERVICE_CONFLICT`; installs are still safe.
- phpMyAdmin is installed but cannot activate until MySQL or MariaDB is active.
- Apache shows installed because its image is present; Nginx is not installed.

Stop the test runtime:

```sh
./dist/devstack --home "$HOME/.devstack" daemon stop
sudo kill "$(sudo cat /tmp/opencode/devstack-runtime/helper.pid)"
```

Restart the helper (current build):

```sh
sudo /data/wwwroot/development/python/dev-web-stack/dist/devstack-helper \
  --socket /run/devstack-helper/helper.sock \
  --state-dir /tmp/opencode/devstack-runtime/state \
  --hosts /tmp/opencode/devstack-runtime/hosts \
  --trust-dir /tmp/opencode/devstack-runtime/trust \
  --allow-uid "$(id -u)" --auto-forward=false \
  --proxy-http 8080 --proxy-https 8443 \
  --vhost-root /tmp/opencode/devstack-runtime/vhosts \
  --vhost-state /tmp/opencode/devstack-runtime/vhosts/state.json \
  --service-root "$HOME/.devstack" --tool-root "$HOME/.devstack" \
  --tool-bin-dir "$HOME/.devstack/tools/bin"
```

Then `./dist/devstack --home "$HOME/.devstack" daemon run`.

## 5. Decisions made after Wave 2

1. **ImageMagick CLI is removed from scope.** No Settings card, catalog entry, tool RPC,
   or image digest. PHP’s `imagick` extension remains.
2. **Automatic GitHub CI is disabled.** All workflows are manual dispatch; no push, PR,
   or tag triggers.
3. **Next batch scope** is service config editing, performance apply/reset/benchmark and
   dashboard, site terminal, editable HTTP/HTTPS/dashboard ports, and two UI fixes:
   remove ImageMagick and add Native tools spacing.
4. **Ports in Settings mean daemon listener ports:** `http_port` (8080), `https_port`
   (8443), `ui_port` (7070). Public 80/443 remain helper/Docker-owned. A port change is
   persisted and marked restart-required; `ui_port` is not hot-rebound inside the request.
5. **Service config scope** is only `<root>/services/<id>/config`. No data/logs/secrets/
   certs/backups/arbitrary host files.
6. **Terminal scope** is one registered site, installing-user only, no sudo/root, no Docker
   socket, site shell environment, PTY over authenticated WebSocket.
7. **Performance host tuning** is explicit opt-in, helper-applied to fixed paths, with
   backup/reset/verification. Benchmark results must be measured; no synthetic RPS.

Full implementation details, APIs, security rules, ordering, and acceptance gates are in
[the Next Batch Plan](../plans/2026-10-26-service-platform-next-batch.md).

## 6. Remaining work after the next batch

- External-site bind mounts for Docker web servers (sites outside `projects/` currently
  use builtin/native serving).
- Full Wave 3 acceptance and benchmark evidence on a real host.
- Any M4 release/signing/diagnostics work remains out of scope.

## 7. Resume procedure

1. Read the [Next Batch Plan](../plans/2026-10-26-service-platform-next-batch.md).
2. Start with **Phase 0** (settings ports, UI cleanup, API contracts) on an integration
   branch.
3. Then run service-config, performance, and terminal backend lanes in isolated
   worktrees; maximum three implementation worktrees.
4. Integrate UI after backend contracts are stable.
5. Run the full verification gate, independent review/fix rounds, merge to `master`.
6. Record the merge SHA and local test evidence here; do not wait for GitHub CI because
   automatic workflows are disabled.

## 8. Useful commands

```sh
make build
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
go build ./...
(cd ui && npm ci && npm run check && npm test -- --run && npm run build)
bash test/release/installer_test.sh
bash test/release/workflow_test.sh
sg docker -c 'DEVSTACK_DOCKER_TESTS=1 bash test/docker/wave1-databases.sh'
sg docker -c 'DEVSTACK_DOCKER_TESTS=1 bash test/docker/wave1-web.sh'
sg docker -c 'DEVSTACK_DOCKER_TESTS=1 bash test/docker/wave2-phpmyadmin.sh'
```

## 9. Important repository pointers

- `internal/serviceconfig/manager.go` — manifest/conflict/backup primitives.
- `internal/performance/` — pure engine; not wired.
- `internal/servicecontrol/manager.go` — service lifecycle/preparers.
- `internal/dockerweb/` — Docker vhost renderer/provider.
- `internal/sites/` — site lifecycle, PHP/Node, vhost, shell/exec.
- `internal/helper/` — privileged fixed-target operations.
- `internal/api/server.go` — interfaces/routes/settings.
- `ui/src/routes/Services.svelte` — lifecycle + pending placeholders.
- `ui/src/routes/Settings.svelte` — ports/tooling/ImageMagick placeholder.
- `ui/src/routes/SiteDetail.svelte` — build/serving fields; future terminal.
- `docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md`
  — authoritative product design.
