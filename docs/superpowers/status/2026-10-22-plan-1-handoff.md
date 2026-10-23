# dev-web-stack — Plan 1 Handoff / Status

- **Date:** 2026-10-23
- **Branch:** `plan-1-foundation`
- **Remote:** `origin` (`git@github.com:AramZahedi/dev-web-stack.git`)
- **Pre-handoff head:** `a5ff5e6` (Task 20 fix; reviewed clean). This document adds the handoff commit on top.
- **Base:** `ad7baa4` (`origin/plan-1-foundation` at the start of this run)
- **Working tree:** clean except ignored `.playwright-mcp/` local artifacts.
- **State:** Plan 1 Tasks **1–20 complete and reviewed**. Plan 1 Tasks **21–26 (external nginx/Apache vhosts) not started.** Plans 2–4 not started.

This is the recovery map for another agent or developer continuing on a different machine.
**Do not redo Tasks 1–20.** Their commits exist on `plan-1-foundation`.

---

## 1. What the project is

`dev-web-stack` is a local web development stack for Linux, written in Go, with a
Svelte dashboard. It serves static and Node sites at trusted `https://<name>.test`
URLs, with a CLI, an unprivileged daemon, a narrowly privileged helper, a local CA,
managed hosts entries, and optional external nginx/Apache vhosts (specified but not
yet implemented). PHP is Plan 2, Windows/WSL is Plan 3, packaging is Plan 4.

Target: systemd Linux x86_64 (glibc 2.35+), one installing user per machine.
No Docker/Podman. The built-in Go proxy is the default serving path.

---

## 2. How to resume

```sh
git fetch origin
git switch plan-1-foundation
git pull --ff-only
go version                 # must be 1.26+; this run used 1.27.3
node --version             # 22.x; this run used v22.23.1 / npm 10.9.8
go test ./...
cd ui && npm ci && npm test && npm run check && npm run build && cd ..
make build
make e2e                   # uses high ports and temp dirs; no root
```

Then read, in this order:

1. `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md`
2. `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`
3. `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`
4. this document

The next implementation task is **Plan 1 Task 21 — Vhost spec model and nginx/Apache
renderers**. Use the Subagent-Driven Development workflow (one implementer + one fresh
task reviewer per task, fix loop, one commit per task). The SDD ledger/workspace is
git-ignored and local to the previous machine; create a fresh one:

```sh
bash <superpowers>/skills/subagent-driven-development/scripts/sdd-workspace \
  docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md
bash <superpowers>/skills/subagent-driven-development/scripts/task-brief \
  docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md 21
```

**Subagent model requirement from the user:** use only
`opencode-go/deepseek-v4.1-flash` with the `max` variant
(`opencode-go/deepseek-v4.1-flash#max`) for all subagents. Do not use another model.

Task numbering note: the old Task 21 acceptance pass is now **Task 26**. Tasks 21–25
are the external-vhost work. Any older reference to “Task 21 acceptance” means Task 26.

---

## 3. Completed tasks and commits

| Task | Commit(s) | Scope |
|---|---|---|
| 1 | `3a57470` | Go module, Cobra CLI, `version`, helper stub, Makefile, CI, README |
| 2 | `1381efd` | `internal/config` (YAML, `$DEVSTACK_HOME`, paths), `internal/model` |
| 3 | `1201645` | domain validation, project paths, port allocator |
| 4 | `0451938` | SQLite store + embedded migrations |
| 5 | `56d5248` | rotating logs, ring buffer, SSE hub, redaction |
| 6 | `7464136`, `2adb116` | supervisor, process groups, backoff, orphan cleanup |
| 7 | `847ed39`, `6eabccd` | backend interface + local backend, minimal `serve-static` |
| 8 | `5bb38f2` | helper Unix-socket RPC, peer-UID auth, managed hosts block |
| 9 | `001d4c3`, `ee59876` | helper CA, CSR signing, Linux trust install, privilege-dropped certutil |
| 10 | `c1ee660` | privileged 80/443 TCP forwarders, helper wiring, uninstall |
| 11 | `9d905e1` | host-routing proxy, TLS server, secure static handler, WebSocket/SSE |
| 12 | `34eea63` | certificate manager, SNI, renewal, TLS verification, schema v2 |
| 13 | `e9f7bdc`, `78dd875` | runtime catalog, verified Node downloads, safe extraction, manifest verify |
| 14 | `865f2ea` | site lifecycle service, create/update/delete rollback, routes, dashboard cert |
| 15 | `1b696a5`, `5616190` | Doctor checks + repair, diagnostic ports/trust/CA |
| 16 | `635d014`, `8966c1e` | authenticated REST API, SSE, fs browser, settings, UI hosting |
| 17 | `567e7c4`, `e79b12e` | daemon wiring, Unix socket, API client, helper entrypoint, lifecycle lock |
| 18 | `1fc5eb6`, `7426392` | full CLI, JSON/human output, init/uninstall, runtime/trust/site commands |
| 19 | `1d7a2bd` (UI), `a747770`, `afacbd6` | designed Svelte dashboard, embedding gate, Playwright, CI |
| 20 | `9017fd0`, `a5ff5e6` | install.sh, systemd units, helper config, docs, E2E, auto-forwarders |

Additional docs commits: `a37ab7e` (external vhost spec + Plan 1 extension),
`6fa0efc` (serve-static dependency fix), `afa9820` (UI review fixes).

---

## 4. Current verification

At `a5ff5e6` (re-confirmed after Task 20):

- `go test ./...` — **PASS**
- `go test -race ./...` — **PASS** (Task 20)
- `go vet ./...` — **PASS**
- `make build` — **PASS** (builds UI, `dist/devstack`, `dist/devstack-helper`)
- `make e2e` — **PASS**:
  ```text
  e2e: helper forwarders .../..., proxy .../..., ui ...
  e2e: helper ready with automatic forwarders
  e2e: daemon ready (pid ...)
  e2e: static site served at https://blog.test:<https>
  e2e: static site served through helper forwarder https://blog.test:<forward-https>
  e2e: helper restarted and forwarder restored automatically
  e2e: daemon stopped cleanly
  e2e: PASS (site survived daemon restart; one serve-static process group)
  ```
- UI: `npm test` 18/18, `svelte-check` 0 errors/0 warnings, Vite build OK,
  Playwright 15/15 in Chromium (Task 19).
- `bash -n install.sh test/e2e/linux.sh` — PASS; `shellcheck --severity=error` — PASS.
- `systemd-analyze verify` on rewritten unit copies — PASS; `install.sh --dry-run` — PASS.

Not verified in this environment: a real root install, browser trust on Chrome/Firefox,
real 80/443 binding, PHP, Windows/WSL, external vhosts.

---

## 5. Current architecture and repository map

```
cmd/devstack/            CLI + daemon entrypoint
cmd/devstack-helper/     privileged helper entrypoint (flags + --config YAML)
internal/config/         $DEVSTACK_HOME config, paths
internal/model/          Site, RuntimeBuild, CertRecord, Event
internal/domain/         name/domain/path validation, port allocator
internal/store/          SQLite + migrations (schema v2)
internal/logs/           rotating logs, ring buffer, SSE hub, redaction
internal/supervisor/     process groups, readiness, backoff, StopAll, orphan cleanup
internal/backend/        Backend interface + local process/static/Node backend
internal/helperproto/    helper RPC request/response types, protocol version
internal/helper/         Unix-socket RPC, auth, hosts, CA/signing, trust, forwarders
internal/helperclient/   helper RPC client
internal/hosts/          managed hosts block render/parse/atomic write
internal/trust/          trust target type
internal/testcert/       test CSR/cert helpers
internal/forwarder/      privileged TCP forwarders
internal/proxy/          host router, TLS/SNI, reverse proxy, HTTP/2, WebSocket
internal/staticserve/    secure static handler (os.OpenRoot, SPA, ranges)
internal/certs/          per-site certs, chain verification, renewal
internal/catalog/        embedded Node catalog (real pinned checksums)
internal/runtime/        downloads, extraction, manifest verification, Node installs
internal/sites/          site lifecycle service, rollback, route snapshot
internal/doctor/         diagnostics + repair
internal/api/            REST/SSE/auth/fs browser/UI hosting, OpenAPI
internal/daemon/         daemon startup/shutdown, readiness, lifetime lock
internal/client/         Unix-socket API client
internal/cli/            Cobra command tree
internal/version/        ldflags version
ui/                      Svelte 5 dashboard, mock/real API, Playwright
api/openapi.yaml         API contract
build/systemd/           helper + user daemon units
build/helper.yaml.example
docs/install.md sites.md tls.md
test/e2e/linux.sh        full local E2E
install.sh               Linux installer
```

### Defaults and paths

- State root: `$DEVSTACK_HOME`, default `~/.devstack`.
- `config.yaml`, `state.db`, `pki/`, `runtimes/`, `logs/`, `run/`, `cache/`.
- Proxy: `127.0.0.1:8080` HTTP, `127.0.0.1:8443` HTTPS; UI/API `127.0.0.1:7070`.
- Helper: `/run/devstack-helper/helper.sock`; state `/var/lib/devstack-helper`;
  config `/etc/devstack/helper.yaml`; fixed 80/443 forwarders.
- Daemon socket: `<home>/run/daemon.sock`; pid `<home>/run/daemon.pid`;
  ready marker `<home>/run/daemon.ready`; lifetime lock `<home>/run/daemon.lock`.
- Backend ports: HTTP 41000–42999; FastCGI 43000–43999 (reserved for Plan 2).
- Certificates: ECDSA P-256; root CA 10 years; leaves 90 days; renewal at 30 days.
- Hosts markers: `# BEGIN devstack` / `# END devstack`; only `127.0.0.1 <domain>`.
- Logs: rotate 10 MB, keep 5, 5,000-line ring.

### Key established interfaces

- `config.Load(home)`, `Config`, `Paths`, `Ensure`.
- `store.Open`, site/runtime/cert/settings/event CRUD, `ErrNotFound`.
- `supervisor.NewManager(runDir, logger)`, `Start`, `Stop`, `Status`, `StopAll`,
  `CleanupOrphans`; `StopGrace`; empty `Ready.TCPAddr` = ready immediately;
  zero `RestartPolicy` = no restart.
- `backend.NewLocal(cfg, store, logs, runtimeProvider, selfBin)`, `Backend` methods,
  `Local.StopAll`.
- `helper.NewServer(Config)`, `Start`, `Close`; `Config.AutoForward` restores
  forwarders; `helperclient.Dial`.
- `proxy.NewRouter`, `Replace(Snapshot)`, `Lookup`, `Certificate`, `Handler`;
  `proxy.NewServer`.
- `certs.NewManager(store, signer, dir, now)`, `Ensure`, `RenewDue`, `Load`, `Delete`,
  `DashboardOwner`.
- `sites.NewService(Deps)`, `Create`, `Update`, `Get`, `List`, `Start`, `Stop`,
  `Restart`, `Delete`, `RebuildRoutes`.
- `doctor.NewService(Deps)`, `Run`, `Repair`.
- `api.NewServer(...)`, `Handler`, `WithSocketAuth`.
- `daemon.Run`, `EnsureRunning`, `SocketPath`, `PidPath`; `client.New` + methods.
- `cli.NewRootCmd(newClient)`.

---

## 6. Decisions and rulings made during implementation

These are deliberate; carry them forward unless a later spec overrides them.

1. **Supervisor failure threshold.** `RestartPolicy.MaxRestarts` is the failure
   count within `Window`; `MaxRestarts=5` crashes on the 5th failure (4 restarts),
   `0` crashes on the first. Rationale: the spec says “after 5 failures in 5
   minutes,” and zero must mean no restarts. Cost if wrong: the field name reads
   like “restarts.”
2. **UI pulled forward.** The Svelte dashboard was built before the numbered task at
   `1d7a2bd`, using `ui-ux-pro-max`; Task 19 added embedding/Playwright. Cost: small
   adjustments when the real API landed.
3. **External vhost scope.** Helper-managed, one global `serving_mode`
   (`builtin|nginx|apache`), native vhosts (static root, Node proxy, PHP FastCGI in
   Plan 2), helper-owned `/etc/devstack` tree + bridge include, safe mode switch with
   rollback. Approved by the user. Implemented in spec/plan only.
4. **Plan renumbering.** Vhost tasks are Plan 1 Tasks 21–25; acceptance is Task 26.
   Cost: older “Task 21 acceptance” references mean Task 26.
5. **`serve-static` dependency.** Task 7 added a minimal `serve-static` subcommand;
   Task 11 replaced it with `internal/staticserve`. The plan now says so.
6. **API read auth.** Read-only GETs and SSE are allowed without a session on the
   loopback/Host-checked API; mutations require session + CSRF. Socket auth bypasses
   cookie/CSRF but not Host. Rationale: the dashboard’s first load raced session
   bootstrap; the API is loopback-only. Cost: local processes can read site/log data.
7. **Certificate schema v2.** Migration `0002_cert_owner.sql` removes the
   `certs.site_id -> sites(id)` foreign key, because certificates are issued before
   the site row is persisted and the dashboard certificate has no site row.
   `DeleteSite` deletes the cert row explicitly in the same transaction.
8. **Certificate ownership.** One cert per owner (`site ID` or `__dashboard__`);
   domain rename re-issues and rollback re-ensures the old SANs rather than deleting
   the owner cert. Task 14 populates the dashboard cert in the route snapshot; Task 17
   calls `RebuildRoutes` after `RenewDue`.
9. **Doctor diagnostics.** Ports are diagnostic: occupied 80/443/7070 is normal,
   free is a warning, `EACCES` is a warning; Doctor never tells the user to stop the
   helper/daemon. Browser targets marked “not installed” are warnings, not trust
   successes. All remediations run as the invoking user (no `sudo devstack`).
10. **Daemon lifecycle.** `<home>/run/daemon.lock` is an exclusive lifetime lock;
    `<home>/run/daemon.ready` is written only after full reconciliation;
    `EnsureRunning` prefers the installed user unit and falls back to a detached
    spawn; shutdown uses `StopAll` regardless of desired state and never changes
    desired state; socket/pid removal is inode/content-checked.
11. **CLI `site logs`.** Defaults to a snapshot (`follow=false`); `--follow` is
    opt-in; `--json` emits JSONL log objects. Trust JSON failures return non-zero.
    Uninstall prints elevated instructions even on failure and labels partial results
    incomplete.
12. **Install/CI.** `make build` builds the UI first; CI builds UI and runs Playwright
    before Go tests; embed tests hard-fail in CI if `ui/dist/index.html` is missing.
    `install.sh` preserves an existing `/etc/devstack/helper.yaml` unless
    `--force-config`; helper `auto_forward: true` restores 80/443 on start.
13. **Helper security.** Peer UID checked before body; protocol header; no arbitrary
    commands/paths/ports/config text; CA key root-only; certutil runs as the home
    owner; trust removal fingerprint-gated; document roots confined.

---

## 7. What remains — Plan 1 Tasks 21–26

All are specified in `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md`
and `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`.

- **Task 21:** `internal/vhost` model + nginx/Apache renderers + golden tests
  (static running/stopped/SPA/TLS matrix, Node, dashboard, default 404, 503;
  validation/injection guards; PHP returns `ErrPHPUnsupported`).
- **Task 22:** helper vhost manager: atomic releases, bridge include, TLS file
  installation/verification, fixed config test/reload, rollback, RPC
  `GET/POST/DELETE /v1/vhosts*`, `--vhost-root/--vhost-state/--nginx-include-dir/
  --apache-include-dir`, security tests.
- **Task 23:** daemon `serving_mode` integration: external static sites have no
  process, stopped sites serve 503, Node/PHP backends unchanged; sync on every
  site/cert change; API `/api/v1/vhosts/status|mode|sync|preview`; client methods.
- **Task 24:** CLI `vhost status|use|sync|preview|path` + Doctor vhost checks.
- **Task 25:** Settings → Serving UI, site vhost preview, `docs/vhosts.md`, E2E
  builtin→nginx→builtin.
- **Task 26:** updated Plan 1 acceptance pass, including external mode, uninstall,
  and rollback.

PHP vhost templates and FastCGI wiring belong to **Plan 2** when FPM lands.

After Plan 1: Plan 2 PHP, Plan 3 Windows/WSL, Plan 4 packaging/release. Those plans
have not been written yet; the M1 spec and the Plan 1 series section describe scope.

---

## 8. Known limitations and deferred minor findings

These are non-blocking for the completed tasks but should be triaged during Task 26
or the final review.

- **Supervisor:** pid-file rewrite can untrack a surviving group after a cleanup
  failure; Stop budget is 1s short of the worst case; `ErrWaitDelay` can replace
  `LastError`; `Window <= 0` disables failure pruning.
- **Backend:** `Stop(graceful=false)` is advisory; `Logs(follow=true)` misses the
  in-memory tail; `Health` does not check `selfBin`/CA; `Prepare` ID validation is
  minimal; re-`Prepare` while running can leave the old process active;
  `CleanupOrphans` is not exposed on `Local` (daemon uses a separate manager).
- **Helper:** `OPTIONS *` bypasses auth (no privileged handler); no read/write
  timeouts; socket lifecycle ownership edge; parent-dir chmod side effect; internal
  error detail leakage; lenient JSON/413; undrained bodies; strict hosts parse/status
  consistency; atomic-write metadata.
- **CA/trust:** hardlink chown edge; `RunAs` falls back to root if a runner lacks the
  optional interface; `Status`/`Remove` perform ownership repair; `/tmp` certificate
  staging; root-only NSS test branch not exercised.
- **Forwarder:** non-deterministic active-connection Stop test; vacuous partial-bind
  test; `Server.Close` vs `SetForwarders` race; Stop early return; copy errors do not
  full-close the peer; listen/target validation broader than helper use; disable
  requires current ports; EADDRINUSE is message-only; accept-loop backoff.
- **Proxy/static:** symlink escape test does not request through the link;
  protocol-relative 301 Location; Stop can block on SSE without a deadline; inbound
  forwarding headers retained; router errors bypass configured logger.
- **Certificates:** manager does not enforce the 90-day cap; file/store crash window;
  `Delete` deletes the row even if file removal fails; key read TOCTOU; nil receiver
  panic; multi-root chain; orphan cert edges; global write lock across signing.
- **Runtime:** verify/extract TOCTOU; manifest unauthenticated; `Ensure` treats a
  manifest-only tree as intact; absolute-path test weak; symlinked-home locking;
  crash residue; stale same-ID row removed before new verify; catalog host not pinned.
- **Sites:** rollback uses the canceled context; rollback deletes by ID if the insert
  failed (collision); delete is not transactional after hosts sync; `Get`/`List`
  fail on status-persist errors; snapshot omits zero-port/missing-cert sites;
  `NewService` reservation is best-effort; `Update` cannot clear command/docroot.
- **Doctor:** occupied ports attributed to expected service (no owner detection);
  duplicate hosts entries collapse; UTF-8 truncation in error details; duplicate
  `VerifyAll`; test gaps.
- **API:** OpenAPI still advertises stale 401/optional details and an unused 405
  component; `codeUnauthorized` unused; hardcoded body default; error classification
  is string-based; helper errors can leak detail; events SSE can skip >200-event
  bursts; fs confinement TOCTOU; `UpdateSiteRequest` drops `name`/`node_version`.
- **Daemon/client:** readiness lock-probe race; 30s client timeout may be short for
  runtime downloads/long repairs; `stopBackends` store list has no deadline;
  `daemon stop` pid identity is argv[0]-based; log snapshots are ring-only.
- **CLI:** `--follow --tail` uses separate snapshot/live requests (gap); elevated
  install paths assume Task 20 layout; prompts can block on an open non-TTY pipe.
- **UI:** PathBrowser initial focus falls back to close; mobile drawer has no full
  focus trap; npm audit reports dev-tooling advisories.
- **Install:** hosts in-place fallback when `/etc/hosts` parent is read-only;
  Debian anchor dir is best-effort; headless upgrade can leave an old detached
  daemon; two docs leftovers (XDG uninstall path, allow_uid wording).

---

## 9. Environment notes from this run

- Go 1.27.3 was installed system-wide at `/usr/local/go` (checksum-verified
  `go1.27.3.linux-amd64.tar.gz`); `go`/`gofmt` symlinked in `/usr/local/bin`.
- Node v22.23.1, npm 10.9.8; Playwright Chromium installed for UI e2e.
- Host: Fedora Linux 44 (Workstation), x86_64.
- `git` remote uses SSH; push was authorized by the user for this handoff.
- `.playwright-mcp/` is local UI smoke evidence and is git-ignored.
- `make e2e` uses high ports and temp `HOME`/hosts/CA/trust; it does not bind 80/443
  or touch real system files.
- The helper can start unprivileged with high ports; production units run it as root
  and `auto_forward: true` restores 80/443.

---

## 10. Push state

After this document is committed, push the branch:

```sh
git push origin plan-1-foundation
```

The previous remote head before this run was `ad7baa4`; the handoff tip includes all
Tasks 6–20 and the vhost spec/plan updates. Verify with:

```sh
git fetch origin
git rev-list --left-right --count origin/plan-1-foundation...HEAD   # expect 0 0
```
