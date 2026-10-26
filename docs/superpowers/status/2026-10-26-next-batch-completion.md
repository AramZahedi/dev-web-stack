# Next Batch Completion — 2026-10-26

- **Status:** implemented, reviewed, locally verified, merged to `master`.
- **Integration branch:** `integration/service-platform-next-batch`.
- **Code head before documentation:** `4477f06`; the merge commit is the current
  `master` tip at the time this file is read (`git rev-parse HEAD`).
- **Plan:** [Service Platform Next Batch](../plans/2026-10-26-service-platform-next-batch.md)
- **Continuation:** [Continuation State](2026-10-26-continuation.md)

## 1. Delivered

### Editable listener ports

- `PATCH /api/v1/settings` accepts optional `http_port`, `https_port`, and `ui_port`.
- Values are validated for range, reserved backend range, distinctness, and loopback
  bind feasibility. A port owned by an unmanaged process returns `409 PORT_IN_USE`.
- `config.yaml` is saved atomically and store settings mirror the effective values.
- `restart_required` remains true after a page reload until the daemon restarts and
  rebinds the listeners. The daemon clears it during startup.
- `devstack daemon restart` stops and starts the daemon using the persisted config.
- Native nginx/Apache dashboard vhosts and the helper accept a configurable UI backend
  port instead of hardcoding 7070.
- Settings exposes the three ports with a restart banner. Public 80/443 remain
  helper/Docker-owned.
- ImageMagick CLI was removed from Settings, requirements, UI tests, and backend catalog
  identity. PHP's bundled `imagick` extension remains.
- Native tools has explicit vertical spacing at desktop and 375px.

### Service configuration

Backend: `internal/servicecontrol/config.go`, backed by `internal/serviceconfig` and the
privileged service lifecycle.

```text
GET  /api/v1/services/{id}/config
GET  /api/v1/services/{id}/config/content?path=...
POST /api/v1/services/{id}/config/diff
POST /api/v1/services/{id}/config/apply
POST /api/v1/services/{id}/config/reset
```

CLI:

```text
devstack service config list <id>
devstack service config show <id> <path>
devstack service config diff <id> <path> --file <new-file>
devstack service config apply <id> <path> --file <new-file> [--yes] [--resolution keep|use|merge]
devstack service config reset <id> <path> [--yes]
```

Security and behavior:

- Scope is only `<root>/services/<id>/config`; data, logs, secrets, certs, sockets,
  backups, dotfiles, and arbitrary host paths are denied.
- Absolute paths, traversal, backslashes, NUL/invalid UTF-8, symlinks, non-regular files,
  and files over 1 MiB are rejected.
- File roots come from catalog config mounts plus safe existing regular files.
- Listing never returns contents; responses never expose secrets. The single-file
  content endpoint requires the dashboard session, while list metadata remains a
  read-only loopback API.
- Tracked generated conflicts require explicit `keep`, `use`, or `merge`. Keep writes
  nothing and detaches; use writes the submitted editor text as generated; merge writes
  it detached. The service renderer does not expose a separate generated baseline, so the
  dashboard labels the actions by what they actually do.
- Apply backs up, writes atomically, validates through the catalog command when active,
  reloads only after validation, and restores the previous file/manifest entry on
  validation or reload failure. Inactive services report `validation_pending`, `live=false`.
- Reset restores the newest backup; it refuses when no backup exists and never deletes a
  custom file.
- The config manifest now lives at `services/<id>/config/.config-manifest.json`, shared
  with Docker nginx/Apache generated vhosts. Existing manifests at the service root are
  migrated by stripping the `config/` prefix; the source manifest is left untouched.
- Dashboard: Services → Configuration. File classes/markers, editor, diff, apply/reset,
  backup path, validation-pending, conflict choices, and inactive-service warning.

### Performance

Backend: `internal/performance`, `internal/performance/service`, helper fixed-path
performance RPC.

```text
GET  /api/v1/performance
PUT  /api/v1/performance/profile
GET  /api/v1/performance/preview
POST /api/v1/performance/apply
POST /api/v1/performance/reset
POST /api/v1/performance/benchmarks
GET  /api/v1/performance/benchmarks
GET  /api/v1/performance/benchmarks/{id}
```

Behavior:

- Measured CPU/cgroup quota, memory, page size, `RLIMIT_NOFILE`, user/group, and
  allowlisted sysctl values.
- Balanced, throughput, and validated custom profiles persisted in the store.
- Preview renders nginx/Apache/PHP values, managed include contents, host file diffs,
  warnings, and the active server without writing anything.
- Apply requires `confirm:true`. Host tuning is a separate `host_tuning:true` opt-in.
- Writes only the active web server's managed includes; skips web includes for
  database-only installs; merges OPcache/realpath ini values through the existing
  PHP validate-then-swap path.
- Helper derives fixed host paths itself:
  `/etc/sysctl.d/99-devstack-performance.conf` and
  `/etc/security/limits.d/99-devstack-performance.conf`. It backs up under
  `<root>/backups/performance/`, applies `sysctl --system`, verifies values, and rolls
  back on failure. No arbitrary path, command, or shell fragment crosses the RPC.
- Reset restores prior host files and safe include snippets, restores PHP ini values,
  reloads catalog services, and clears applied state.
- Benchmarks use a real measured built-in Go HTTP generator, labelled with tool/version
  and limitations. Results include measured RPS, p50/p95/p99, errors, duration, target,
  profile, timestamps, and stored `result.json` + `raw.log` under
  `logs/performance/<timestamp>/`. Failed/canceled runs never synthesize a measurement.
- Dashboard: Performance panel with resources, profile selector/custom JSON, preview,
  typed apply/reset confirmation, host-tuning opt-in, per-step results, and benchmark
  history/details. No results exist before a run.

### Site terminal

Backend: `internal/terminal` with `creack/pty` and `coder/websocket`.

```text
POST /api/v1/sites/{ref}/terminal/session  -> {ticket, expires_at}
GET  /api/v1/sites/{ref}/terminal?ticket=... (WebSocket)
```

- 30-second, one-use, cryptographically random ticket bound to site ID and installing
  UID. Issuance requires the normal session and CSRF.
- Origin and Host are both checked against the loopback dashboard allowlist.
- Runs as the installing user in the site project root with site PHP/Node shims,
  `PHPRC`, and `DEVSTACK_SITE`/`DEVSTACK_HOME`/`DEVSTACK_PROJECT`.
- Never root or sudo; rejects root shells and symlink-escaped project/document roots.
- PTY limits: 4 total, 2 per site/user, 30-minute idle, 4-hour absolute, 4 MiB output,
  resize handling, process-group teardown, audit start/stop only.
- Protocol: JSON `input`/`resize` client frames; JSON `output`/`exit`/`error` server
  frames. Binary input is accepted as raw bytes.
- Dashboard: Site detail → Terminal with xterm.js, fit/resize, reconnect, close, error
  state, and 375px coverage. No terminal auto-starts on page load.

## 2. Verification evidence

Commands run on the integration branch:

```text
go test ./... -count=1                         # pass
go test ./... -race -count=1                   # pass
go vet ./...                                   # clean
go build ./...                                 # clean
cd ui && npm ci && npm run check               # 0 errors, 0 warnings
cd ui && npm test -- --run                     # 13 files, 107 tests pass
cd ui && npm run test:e2e                      # 31 Playwright tests pass
make build                                     # UI + CLI + helper build
bash test/release/installer_test.sh            # all installer checks pass
bash test/release/workflow_test.sh             # all workflow checks pass
sg docker -c 'DEVSTACK_DOCKER_TESTS=1 bash test/docker/wave1-web.sh'       # pass
sg docker -c 'DEVSTACK_DOCKER_TESTS=1 bash test/docker/wave1-databases.sh' # pass
sg docker -c 'DEVSTACK_DOCKER_TESTS=1 bash test/docker/wave2-phpmyadmin.sh' # pass
```

Live smoke on the test runtime:

- Settings PATCH moved the dashboard listener from 7070 to a temporary free port,
  returned `restart_required: true`, `devstack daemon restart` started the dashboard on
  the new port, and a second PATCH/restart restored 7070 with
  `restart_required: false`.
- `GET /api/v1/performance` returned measured host resources and a balanced profile.
- `GET /api/v1/performance/preview` returned managed nginx/Apache files and both fixed
  host diffs without writing. The dashboard preview also surfaced the no-active-web-server
  warning and duplicate warning strings without a client error.
- A real 1-second, 4-connection benchmark against a registered static site completed with
  **23,696 measured req/s**, p50 0.14 ms, p95 0.33 ms, p99 0.43 ms, and 0 errors using
  `devstack-go-http-load` v1. The result was stored under `logs/performance/`; the test
  site and result were removed from the runtime afterward.
- Site-scoped terminal connected through the real WebSocket/PTY, `pwd` returned the
  registered site project root, and Close exited the session.
- Service configuration listed base files, diffed a proposed edit, applied it with
  `validation_pending` while inactive, reset from backup, and uninstalled cleanly.
- The real dashboard loaded the new Performance and Configuration/Terminal surfaces.

The test home was returned to its clean state: no registered sites, no installed
services, no generated `services/compose.yaml`.

## 3. Remaining limitations

1. **PHP-FPM pool sizing is generated but not merged into live FPM masters.** OPcache and
   realpath ini values are applied through the PHP validate-then-swap path. Per-site
   `pm.max_children`/spare-server values remain in
   `services/php/performance/` fragments because the site model has no persisted
   pool-sizing contract. The performance result includes a warning.
2. **Host tuning was tested with destination-root overrides and a fake runner, not by
   changing this host's `/etc`.** The helper owns fixed paths, backup, verification, and
   rollback. A real root acceptance run remains an explicit host gate.
3. **Benchmarks use the built-in Go generator,** not wrk/hey, because no verified pinned
   benchmark image is part of the product. Results are measured and labelled; they are
   not directly comparable to wrk/hey numbers.
4. **Performance-managed includes are reserved.** They are hidden from the service-config
   editor (`conf.d/performance/` and `conf.d/performance.conf`) so an editor write cannot
   be silently replaced by the next profile apply.
5. **Pre-change nginx/Apache base configs** do not gain the managed performance include
   lines automatically because base files are user-owned. A fresh install seeds them;
   apply reports a profile conflict until an existing base config adds them.
6. **Terminal descendants that deliberately create a new session/process group** can
   escape group teardown. Shell foreground/background jobs are killed. There is no
   background ticket janitor; expired tickets are pruned on issuance/consumption.
7. **A stopped site still gets a terminal** if its project directory is valid; terminal
   availability is not tied to the site's serving status.
8. **External-site Docker bind mounts remain a follow-up.** Docker web servers serve
   sites under the managed projects root.
9. **Live Wave 3 benchmark/performance evidence on a real host remains open.**

## 4. Merge and cleanup record

- Integration work was done in an isolated worktree.
- Backend lanes ran in separate worktrees (`lane/service-config`, `lane/performance`,
  `lane/terminal`) and were merged into the integration branch.
- Independent lane gates passed before merge; the combined branch passed the full local
  gate and a fresh-context whole-branch review.
- Worktrees and lane branches were removed after the final merge.
- Automatic GitHub Actions remain disabled; the local gate is authoritative.
