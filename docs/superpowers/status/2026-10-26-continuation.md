# Continuation State — 2026-10-26

This is the current-state record for resuming the service-platform work. Read it with
[handoff.md](../../handoff.md), [architecture.md](../../architecture.md), and the
[Next Batch Plan](../plans/2026-10-26-service-platform-next-batch.md).

## 1. Repository state

- **Code baseline:** next-batch code `5386c22`, merged to `master` as `3c0ddc7`.
- **Current runtime build:** rebuilt from the merged next-batch branch after verification; run `./dist/devstack version` for the exact build string.
- **Next batch:** **implemented, reviewed, and merged**; see
  [Next Batch Completion](2026-10-26-next-batch-completion.md) for scope, evidence, and
  remaining host limitations.
- **Wave 1 merge:** `b4be089`.
- **Wave 2 merge:** `ec2dca4`.
- **Last green GitHub CI:** run `40222677123` at `cb443d8`. No automatic CI runs after the
  workflow triggers were disabled.
- **CI policy:** automatic GitHub Actions runs are now disabled. `ci.yml`, `php.yml`,
  and `release.yml` are manual `workflow_dispatch` only. Pushes, pull requests, and tags
  do not start workflows. Manual dispatch is still possible if explicitly invoked.
- **Worktrees/branches:** service-config, performance, and terminal lane worktrees were
  merged into the integration branch and removed after the final merge. The worktree is
  clean.
- **Tests at final gate:** Go unit/race/vet/build, UI check/107-unit/31-Playwright/build,
  installer/workflow tests, and the earlier Wave 1/2 live Docker smoke suites pass.

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
  formulas, includes, apply/reset plans, benchmark plan.

### Next batch (2026-10-26)
- Editable `http_port`/`https_port`/`ui_port` with atomic persistence, bind checks,
  durable `restart_required`, dynamic UI vhost port, and `devstack daemon restart`.
- Service config manifest/read/content/diff/apply/reset API, CLI, and dashboard editor;
  config-root manifest shared with Docker nginx/Apache and migrated from the legacy
  service-root manifest.
- Performance resources/profile/preview/apply/reset API, fixed-path helper host tuning
  with backup/verify/rollback, measured benchmark storage/history, and dashboard.
- Site-scoped PTY terminal with one-use ticket/WebSocket, site PHP/Node environment,
  process-group teardown, limits, and xterm.js panel.
- Settings cleanup: ImageMagick removed; Native tools spacing fixed.
- See [Next Batch Completion](2026-10-26-next-batch-completion.md).

## 3. Current truthful status

The Services page shows live service lifecycle, configuration, performance, and benchmark
surfaces. Site detail shows the site-scoped terminal. Settings exposes editable listener
ports and no ImageMagick row. There are no “Wave 2 backend pending” placeholders.

Remaining limitations are listed in
[Next Batch Completion](2026-10-26-next-batch-completion.md) and
[Known issues](../../known-issues.md): PHP-FPM pool sizing not merged into live masters,
real root host-tuning acceptance, benchmark evidence on a real host, external-site Docker
mounts, and live Wave 3 acceptance.

## 4. Current local test runtime

This is ephemeral and not part of the repository state.

- **Dashboard:** `http://127.0.0.1:7070`
- **Daemon:** running with `--home /home/aram/.devstack`
- **Binary version:** rebuild from the merged branch and run `./dist/devstack version`
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
- **All catalog services currently show `not_installed`.** `~/.devstack/services` is
  empty. The smoke install of nginx and the temporary site were removed; no
  `compose.yaml` remains.
- phpMyAdmin requires MySQL or MariaDB to be active before activation.
- Cached Docker images no longer count as installed. Loading Services no longer creates
  `services/compose.yaml` as a side effect.
- Live smoke evidence for the next batch is recorded in
  [Next Batch Completion](2026-10-26-next-batch-completion.md).

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

## 4a. Recent fixes after Wave 2

- Install action is blue (`info`); Activate remains green (`primary`).
- A service is installed only when `services/<id>/{config,data,logs}` exists. Cached
  images alone are not installed. `Install` pulls first, then scaffolds; `Activate`
  rejects uninstalled services.
- Services status no longer creates `compose.yaml` on page load.
- Redundant Installed/Active state badges are hidden when the explicit status badge
  already covers them.

## 5. Decisions made after Wave 2

1. **ImageMagick CLI is removed from scope.** No Settings card, catalog entry, tool RPC,
   or image digest. PHP’s `imagick` extension remains.
2. **Automatic GitHub CI is disabled.** All workflows are manual dispatch; no push, PR,
   or tag triggers.
3. **Ports in Settings mean daemon listener ports:** `http_port` (8080), `https_port`
   (8443), `ui_port` (7070). Public 80/443 remain helper/Docker-owned. A port change is
   persisted and marked restart-required; `ui_port` is not hot-rebound inside the request.
   `devstack daemon restart` applies it.
4. **Service config scope** is only `<root>/services/<id>/config`. The manifest lives at
   the config root and is shared/migrated with Docker nginx/Apache generated vhosts.
5. **Terminal scope** is one registered site, installing-user only, no sudo/root, no Docker
   socket, site shell environment, PTY over a one-use ticket/WebSocket. Origin/Host are
   checked; limits and process-group teardown apply.
6. **Performance host tuning** is explicit opt-in, helper-applied to fixed paths, with
   backup/reset/verification. Benchmark results are measured by the labelled built-in Go
   generator; no synthetic RPS.
7. **PHP-FPM pool sizing remains a limitation.** OPcache/realpath ini values are applied;
   per-site `pm.*` values are generated but not merged into live masters.

Full implementation details, APIs, security rules, ordering, and acceptance gates are in
[the Next Batch Plan](../plans/2026-10-26-service-platform-next-batch.md).

## 6. Remaining work after the next batch

- External-site bind mounts for Docker web servers (sites outside `projects/` currently
  use builtin/native serving).
- Real root host-tuning acceptance and benchmark evidence on a real host.
- Merge PHP-FPM pool sizing into the persisted site/FPM contract.
- Full Wave 3 acceptance; M4 release/signing/diagnostics remains out of scope.

## 7. Resume procedure

1. Read the [Next Batch Completion](2026-10-26-next-batch-completion.md) and
   [Known issues](../../known-issues.md).
2. Work the remaining host gates or external-site mounts; do not re-run completed waves.
3. Preserve the merged behavior and run the full local gate before claiming completion.
4. Do not wait for GitHub CI; automatic workflows are disabled.

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
- `internal/performance/` and `internal/performance/service/` — profile engine and
  preview/apply/reset/benchmark orchestration.
- `internal/terminal/` — site PTY ticket/session manager and WebSocket protocol.
- `internal/servicecontrol/manager.go` — service lifecycle/preparers.
- `internal/dockerweb/` — Docker vhost renderer/provider.
- `internal/sites/` — site lifecycle, PHP/Node, vhost, shell/exec.
- `internal/helper/` — privileged fixed-target operations.
- `internal/api/server.go` — interfaces/routes/settings.
- `ui/src/routes/Services.svelte` — lifecycle, configuration, performance, benchmarks.
- `ui/src/routes/Settings.svelte` — editable ports, tooling, no ImageMagick.
- `ui/src/routes/SiteDetail.svelte` — build/serving fields and terminal.
- `docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md`
  — authoritative product design.
