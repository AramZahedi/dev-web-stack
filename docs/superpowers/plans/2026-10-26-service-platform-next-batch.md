# Service Platform Next Batch Plan

- **Date:** 2026-10-26
- **Baseline:** code at `e2ced65` (Wave 2 merged; install/activate color fix); docs at `8c57b2e`. Last automatic CI green was `cb443d8`; automatic CI is now disabled.
- **Scope owner:** next implementation batch
- **Status:** planned, not implemented

## 1. Scope

This batch completes the operational gaps that are currently visible as
“Wave 2 backend pending”:

1. Service config manifest, read/diff/apply/reset API, CLI, and dashboard editor.
2. Performance profile API, apply/reset, host tuning, benchmark runs/results, and dashboard.
3. Site-scoped browser terminal/command runner.
4. Editable HTTP, HTTPS, and dashboard ports in Settings.
5. UI cleanup: remove ImageMagick CLI from Settings and add spacing above Native tools.

External-site Docker mounts remain a separate follow-up unless it becomes necessary for
the above acceptance. ImageMagick CLI is **removed from the product/UI scope** per user
decision; PHP’s bundled `imagick` extension remains available per site.

## 2. Decisions and assumptions

- **ImageMagick:** no Settings card, no catalog/tool endpoint, no pinned image. Remove it
  from requirements/known issues rather than leaving a permanent placeholder.
- **Ports:** Settings will edit the daemon’s listener ports:
  - `http_port` — current proxy HTTP listener, default `8080`;
  - `https_port` — current proxy HTTPS listener, default `8443`;
  - `ui_port` — dashboard/API listener, default `7070`.
  Public `80/443` remain helper/Docker-owned. If public ports must also be editable,
  that is a separate helper-config/rebind operation.
- A port change is persisted, validated, and marked **restart required**; `ui_port`
  cannot be rebound safely inside the request that is using the old listener.
- Service config is limited to `<root>/services/<id>/config`; data, logs, secrets,
  certificates, backups, and arbitrary host paths are out of bounds.
- Performance host tuning is explicit opt-in. No silent sysctl/limits changes.
- Benchmark results must be measured, never synthesized. Use a pinned real tool if a
  verified image/digest is available; otherwise use a clearly labelled built-in Go HTTP
  load generator.
- Terminal is scoped to one registered site, runs as the installing user, and never
  invokes `sudo` or root.
- No change to the Wave 1/2 service lifecycle or Docker/container model.

## 3. Serial Phase 0 — Contracts, ports, and UI cleanup

**Goal:** freeze API shapes, make ports editable, remove ImageMagick, and fix the Native
tools spacing before parallel work.

### Backend/contracts

- Add `PATCH /api/v1/settings` accepting:
  - `http_port`, `https_port`, `ui_port` as optional integers;
  - no partial field zeroing; omitted fields unchanged.
- Validation:
  - range 1–65535;
  - ports distinct;
  - bind feasibility checked without leaving a listener;
  - reject ports owned by unmanaged services with a clear conflict.
- Persist to `config.yaml` atomically and update store settings as needed.
- Return `restart_required: true` when a listener would change.
- Add `devstack daemon restart` (stop + run) so changing `ui_port` has a supported path.
- Update helper forwarder targets when helper forwarding is enabled, or explicitly report
  that `devstack init`/helper restart is required. Do not silently leave 80/443 pointed at
  the wrong proxy port.
- Add OpenAPI schemas for config, performance, terminal, and settings mutations.

### UI cleanup

- Remove the ImageMagick tool row and its pending badge from `Settings.svelte`.
- Add vertical spacing/margin above the Native tools card. Cover at desktop and 375px in
  Playwright/visual assertions.
- Add port inputs and a restart-required banner in Settings.
- Keep service config/performance placeholders until their APIs land; then remove them.

### Tests

- Config validation, `PATCH /settings` CSRF/validation/restart flags.
- UI settings form, spacing regression, no ImageMagick.
- CLI `daemon restart` and port-change test using temporary home/listeners.

## 4. Phase 1 — Parallel backend lanes

Use three isolated worktrees, each with a fresh implementer and independent review.
Lane A and Lane B must not edit the same API/daemon hot files without a serialized
contract; the integration owner resolves only mechanical wiring.

### Lane A — Service config

**Package/service**

- Add a config service around `internal/serviceconfig.Manager` for each catalog service.
- Resolve only `<root>/services/<id>/config`.
- Manifest root is the service root so the existing `.config-manifest.json`, backup, and
  keep/use/merge behavior are reused.
- File policy:
  - regular files only;
  - no symlink escapes;
  - max 1 MiB per file;
  - deny `.env`, secrets, data, logs, certificates, sockets, and backups;
  - allow base/generated/custom config entries from the manifest plus safe existing files.

**API**

```text
GET  /api/v1/services/{id}/config
GET  /api/v1/services/{id}/config/content?path=...
POST /api/v1/services/{id}/config/diff          {path, content}
POST /api/v1/services/{id}/config/apply         {path, content, resolution?}
POST /api/v1/services/{id}/config/reset         {path}
```

- `GET config` returns path, class, generated/modified/detached, hash, size, and conflict
  summary; never file contents by default.
- `diff` returns unified diff plus conflict metadata.
- `apply`:
  - backs up the current file;
  - uses atomic write;
  - resolves tracked conflicts only with an explicit `keep|use|merge` choice;
  - validates through the helper’s catalog validation command when active;
  - reloads only after successful validation;
  - rolls back on validation/reload failure;
  - audits the action without logging file contents.
- `reset` restores the managed baseline/last known generated content or backup; it never
  deletes untracked custom files without an explicit separate confirmation.
- Inactive service: write is allowed only after syntax validation against a temporary
  validation path/container if available; otherwise mark `validation_pending` and require
  activation/validation before serving. Do not claim a config is live when it is not.

**CLI**

```text
devstack service config list <id>
devstack service config show <id> <path>
devstack service config diff <id> <path> --file <new-file>
devstack service config apply <id> <path> --file <new-file> [--yes]
devstack service config reset <id> <path> [--yes]
```

**UI**

- Services → service card → Configuration.
- File list with managed/generated/custom/modified/conflict markers.
- Editor with diff preview, apply/reset, backup note, validation state, and rollback error.
- Disable editing for non-installed services; show “validation pending” for inactive ones.

**Tests**

- Path traversal/symlink/size/secret rejection.
- Conflict keep/use/merge, backup, rollback on validation/reload failure.
- API CSRF and no-secret response tests.
- UI unit tests and Playwright conflict/apply flow.
- Live Docker nginx/Apache config validation and reload.

### Lane B — Performance

**Service/API**

```text
GET  /api/v1/performance
PUT  /api/v1/performance/profile        {profile:"balanced|throughput|custom", custom?}
GET  /api/v1/performance/preview
POST /api/v1/performance/apply          {confirm:true}
POST /api/v1/performance/reset          {confirm:true}
POST /api/v1/performance/benchmarks     {site, duration_s?, connections?}
GET  /api/v1/performance/benchmarks
GET  /api/v1/performance/benchmarks/{id}
```

- Collect CPU/cgroup quota, memory, page size, `RLIMIT_NOFILE`, and existing allowlisted
  sysctl values in the daemon.
- Persist profile and custom values; revalidate custom values on every apply.
- Preview returns nginx/Apache/PHP values, include contents, host tuning diff, and warnings
  without writing anything.
- Apply:
  - requires an active web server; skip host tuning for database-only installs;
  - writes only the active server’s generated performance includes;
  - merges PHP pool/OPcache settings through the existing validate-then-swap flow;
  - helper writes fixed host paths and runs `sysctl --system` with backup/verify/rollback;
  - reloads only catalog services; never arbitrary commands;
  - returns per-step results and a rollback path.
- Reset restores the prior host files and built-in performance include snippets, then
  reloads/validates.
- Benchmark:
  - accept a registered site and safe duration/connections;
  - use a verified pinned tool image if one is available; otherwise use the built-in Go
    HTTP generator and label the tool/version/limitations;
  - store `result.json` and raw output under `logs/performance/<timestamp>/`;
  - report measured RPS, p50/p95/p99, errors, duration, target, profile, and tool;
  - no generated or estimated RPS.
- Host tuning is never applied by merely opening the panel.

**Helper**

- Add a fixed-target performance RPC. The daemon sends validated profile values/metadata;
  the helper derives paths itself:
  - `/etc/sysctl.d/99-devstack-performance.conf`
  - `/etc/security/limits.d/99-devstack-performance.conf`
  - web/performance includes under the service config root
- No arbitrary host paths or shell fragments from the API.
- Backups under `<root>/backups/performance/`.
- Verify applied values and roll back on failure.

**UI**

- Performance panel: resources, profile selector/custom editor, preview diff, warnings,
  apply/reset with typed confirmation, benchmark form, history/results.
- Show host-wide impact explicitly before apply.
- No benchmark results when no run exists.

**Tests**

- Pure engine cases already exist; add API/helper integration tests with fake runner.
- Destination-root override in tests; never write real `/etc` in CI.
- Optional root/live acceptance script for apply/reset/benchmark, guarded by env.
- UI tests for confirmation/no-fake-results.

### Lane C — Site terminal

**Backend**

- Add PTY session manager using `creack/pty` (new dependency; verify module checksum).
- Ticket flow:

```text
POST /api/v1/sites/{ref}/terminal/session   -> {ticket, expires_at}
GET  /api/v1/sites/{ref}/terminal?ticket=... (WebSocket upgrade)
```

- Ticket is short-lived (≤30 s), one-use, bound to UID and site; issuance requires the
  normal session + CSRF.
- Resolve site by name/id; reject unknown/unregistered sites and symlink-escaped project
  paths.
- Environment must match site shell/exec:
  - site PHP/Node shims first in PATH;
  - `PHPRC` for site PHP;
  - `DEVSTACK_SITE`, `DEVSTACK_HOME`, `DEVSTACK_PROJECT`;
  - working directory = site project/document root.
- Never root, never `sudo`, never Docker socket, no arbitrary daemon command.
- PTY limits: one/few sessions per site/user, idle timeout, output cap, resize handling,
  kill process group on disconnect, audit start/stop only (not keystrokes).
- Reject cross-origin/Host violations; do not expose the socket outside the loopback UI.

**UI**

- SiteDetail → Terminal panel.
- xterm.js (add dependency) with fit/resize, reconnect/close, clear error state.
- Disabled/clear message when the site is unavailable.
- Mobile/keyboard accessibility and 375px layout.

**Tests**

- Ticket one-use/expiry/UID/site binding.
- Origin/Host/CSRF negatives.
- Environment precedence (site PHP beats global; Node shim).
- cwd escape rejection, no sudo/root, idle/output limits, disconnect cleanup.
- UI component and Playwright command execution.

## 5. Phase 2 — Dashboard integration

Single UI lane after backend merge:

- Service config editor/diff/apply/reset, replacing the Services placeholder.
- Performance panel, replacing the Services placeholder.
- Terminal panel in SiteDetail.
- Ports and restart banner in Settings.
- Remove ImageMagick everywhere.
- Native tools spacing.
- Remove only placeholders whose APIs are actually implemented; leave no fake controls.
- Update API client/types/mocks and E2E coverage.

## 6. Phase 3 — Verification and exit

Required commands:

```sh
go test ./... -count=1
go test ./... -race -count=1
go vet ./...
go build ./...
cd ui && npm ci && npm run check && npm test -- --run && npm run build
bash test/release/installer_test.sh
```

Additional gates:

- Docker smoke for service config on nginx/Apache.
- Performance apply/reset with temporary/overridden paths; optional root acceptance.
- Benchmark run against a real local site and stored result.
- Terminal PTY E2E with a real shell.
- Settings port-change/restart test.
- Shellcheck/bash syntax.
- Independent lane reviews, fix rounds, whole-branch review.
- Merge to `master`, push, wait for CI green, record SHA/run, remove worktrees/branches.

## 7. Risks and stop conditions

- **Host tuning**: global sysctl/limits can affect the machine. Must require explicit
  confirmation, backup, verification, and reset. Stop if a safe rollback cannot be proven.
- **Port editing**: changing the UI port can strand the current browser; use
  restart-required, bind checks, and documented recovery. Do not silently rebind.
- **Terminal**: arbitrary commands are intentional but must remain user-scoped; no root,
  no daemon-side command injection, no cross-site tickets.
- **Benchmark**: no verified tool image means no comparable RPS claim; use the labelled
  built-in generator or stop and report.
- **Config editing**: never expose secrets/data; never overwrite modified files without
  explicit conflict resolution.
- If Docker/live privileged acceptance is unavailable, run unit/fake tests and mark the
  live gate as blocked rather than skipped silently.
