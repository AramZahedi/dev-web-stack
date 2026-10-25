# Managed Service Platform Implementation Program

> **For agentic workers:** This is the program-level roadmap. Detailed task plans are
> written per wave. The first detailed plan is
> `2026-10-25-service-platform-foundation.md`. Use the worktree/merge mechanism in this
> document for every wave.

**Goal:** Implement the approved service-container platform with strict active-service
conflicts, host-owned config/data/certs, editable configs, Laravel/Nuxt support, global
PHP/Composer, dashboard parity, and measured performance tuning.

**Architecture:** Native devstack; optional infrastructure services run as separate Docker
containers controlled by the privileged helper through a fixed Compose project. A serial
foundation freezes cross-lane contracts, then independent workstreams run in isolated
worktrees and merge through a per-wave integration branch.

**Tech Stack:** Go 1.26, SQLite (`modernc.org/sqlite`), Svelte 5/Vite/TypeScript, Docker
Engine + Compose, systemd, native PHP/Node runtimes, shell scripts.

**Spec:** `docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md`

## Global Constraints

- devstack itself stays native; Docker is only for optional service containers.
- No native `dnf`/`apt` or systemd management for nginx, Apache, databases, Redis, or
  phpMyAdmin.
- Every optional service is a separate container; no all-in-one tools container.
- `/data/devstack` is the recommended configurable root; host-owned config, site,
  database, certificate, secret, and backup data.
- Exactly one web server (nginx/Apache) and one MySQL-family service (MySQL/MariaDB) may
  be active at a time. PostgreSQL and Redis are independent.
- Data services and phpMyAdmin bind to `127.0.0.1` by default.
- Native CA/trust is authoritative; containers receive leaf certs only.
- No silent config overwrite; generated files use conflict/diff/backup/validation.
- PHP and Node remain native. PHP `imagick` is bundled in the PHP runtime.
- Global `php`/`composer` use the machine-wide CLI default outside a site shell; site
  shells override with the site PHP/ini.
- Dashboard parity: every operational task has a dashboard and CLI path.
- Keep existing M1 behavior and the legacy native external provider compatible.
- Linux x86_64 only; Windows/WSL Plan 3 remains deferred.
- No invented image digests, runtime checksums, or URLs. Pinned values must come from real
  published artifacts/manifests.
- No secret, `.docker-data/`, `projects/`, certificate, or runtime archive in git.

## Review Focus

These are the failure modes most likely to hurt a user and must be pinned by tests in the
owning task:

1. A config edit is silently overwritten, or a conflict blocks a site without a clear
   resolution path.
2. A failed service switch leaves 80/443 or 3306 owned by the wrong process, or both
   conflicting services active.
3. A web-server container switch breaks CA trust or serves the dashboard/site with the
   wrong certificate.
4. Laravel/Apache `.htaccess` routing or Nuxt build output is served incorrectly
   (framework 404 vs SPA shell, static assets, path info).
5. A database credential leaks into logs/UI, or data deletion proceeds without a verified
   backup.

## 1. Why the previous plans were slow

The prior plans were high quality but serial:

- every task rebuilt the same UI/Go tree;
- shared files (`api/server.go`, `cli/root.go`, `config`, UI routing) were edited by many
  tasks;
- each task had a full review/fix loop before the next task started;
- integration happened continuously, so each merge could disturb the next task.

This program keeps every TDD/review/fix/re-review gate but changes scheduling:

- contract-first foundation;
- right-sized tasks;
- parallel independent lanes in worktrees;
- a serial merge queue;
- one full integration gate per wave.

## 2. Wave and dependency graph

```text
Wave 0  Foundation (serial)
  F1 root layout
  F2 service registry/store
  F3 Compose/helper control plane
  F4 config manifest/conflict engine
  F5 site/global contract freeze
  F6 service API/CLI/UI skeleton
            │
            ▼
Wave 1  Parallel lanes (branch from integration/services-wave-1)
  Lane A  Database services + credentials
  Lane B  Web servers + vhost parity + .htaccess/front controller
  Lane C  Developer tooling: global PHP/Composer/shell/build profiles
            │
            ▼
Wave 2  Parallel follow-ups (branch from integration/services-wave-2)
  Lane A2 phpMyAdmin + credential UI/rotation
  Lane B2 Performance profiles + host tuning + benchmark
  Lane C2 Dashboard completion: config diff UI, build/terminal UI, service controls
            │
            ▼
Wave 3  Integration and acceptance (one lane)
  full dashboard-only acceptance, Laravel/Nuxt live fixtures, security,
  performance, service switching, M1 regression
```

Waves 1 and 2 task plans are written **after** the prior wave’s interfaces are merged.
This is deliberate: planning exact signatures before the contracts exist creates rework.

## 3. Plan artifacts

| Artifact | Purpose |
|---|---|
| This program plan | mechanism, waves, ownership, merge protocol |
| `2026-10-25-service-platform-foundation.md` | detailed Wave 0 tasks, files, interfaces, tests |
| Future `*-database-services.md` | Wave 1 Lane A detailed tasks |
| Future `*-web-servers.md` | Wave 1 Lane B detailed tasks |
| Future `*-developer-tooling.md` | Wave 1 Lane C detailed tasks |
| Future `*-performance-dashboard.md` | Wave 2 detailed tasks |
| Future `*-acceptance.md` | Wave 3 integration evidence |

Each future plan must begin with the writing-plans header, Global Constraints copied from
this document/spec, Review Focus, exact files/interfaces, TDD steps, and merge order.

## 4. Worktree and branch protocol

### 4.1 Preparation

- `.worktrees/` is git-ignored.
- Use the native OpenCode worktree tool (`tools.opencode.worktree_create`) when executing.
  Do not create phantom worktrees with raw `git worktree add` if the native tool is
  available.
- Each worktree is named after its task/branch:
  `services-foundation-f1`, `services-db-mysql`, `services-web-vhosts`.
- Branch names:
  - `feat/services-foundation-f1`
  - `feat/services-db-mysql`
  - `feat/services-web-vhosts`
  - `feat/services-dev-global-php`
- Integration branches:
  - `integration/services-wave-1`
  - `integration/services-wave-2`
  - `integration/services-wave-3`
- Task branches branch from the current wave integration branch, not from a moving
  `master`.

### 4.2 Parallel limit

- Maximum **three implementation worktrees** active at once.
- Maximum **three task reviewers** at once.
- The controller/merge owner is serial.
- Do not parallelize tasks that edit the same owned file.
- Do not run live Docker integration tests in parallel on shared ports; unit tests may run
  in parallel.

### 4.3 Task loop

For each task:

1. Record `BASE=$(git rev-parse HEAD)` in the task worktree.
2. Dispatch one implementer with the task brief and global constraints.
3. Implementer writes failing test, runs it, implements, runs tests, commits, self-reviews.
4. Controller generates a review package for `BASE..HEAD`.
5. Dispatch a task reviewer with brief + report + diff package.
6. Fix/re-review loop until spec and quality are clean (no skipped gates).
7. Merge the task into the wave integration branch through the merge queue.

Implementers never dispatch their own reviewers. Reviewers do not edit code.

### 4.4 Merge queue

1. Rebase task branch onto current wave integration branch.
2. Run the task’s focused tests.
3. Merge into the wave branch.
4. Run the affected package tests plus shared quick tests.
5. If conflict:
   - owned-file conflict: task implementer rebases/fixes;
   - hot-file conflict: integration owner resolves;
   - scoped re-review of the resolution.
6. After all lanes merge: full suite, build, UI checks, Docker smoke.
7. Merge wave branch to `master`; push; record SHA in the handoff/status doc.
8. Remove task worktrees and branches; keep failure logs until wave close.

No task branch merges directly to `master`.

## 5. File ownership and hot files

Foundation owns the hot files and freezes extension points:

- `internal/api/server.go` route registration;
- `internal/cli/root.go` command roots;
- `internal/config/*` layout/settings;
- `internal/store/migrations/*` schema order;
- `internal/model/model.go` shared site/build types;
- `ui/src/App.svelte`, `ui/src/lib/api.ts`, `ui/src/lib/types.ts`, sidebar/nav;
- `Makefile`, `go.mod`, `README.md` documentation index.

Lane ownership after foundation:

| Lane | Owns |
|---|---|
| A Database | `internal/services/database_*`, DB-specific compose/config, credential store, DB tests |
| B Web servers | web-server compose/config, vhost generator/provider, `.htaccess`, front-controller rendering, web tests |
| C Developer tooling | global PHP/Composer shims, `devstack shell`/`exec`, build profile executor, tool tests |
| A2 | phpMyAdmin container, credential UI/handlers |
| B2 | performance generator, host tuning, benchmark, PHP budget |
| C2 | dashboard components/routes for services/config/builds/terminal |

If a task must edit a hot file, the integration owner makes the change in a small
pre-merge commit or the task becomes serial.

## 6. Test isolation rules

- Every worktree uses a temporary `DEVSTACK_HOME` outside the repo.
- Every Docker integration test uses a unique Compose project name and high ports.
- No test edits the real `/etc/hosts`, trust stores, 80/443, or system systemd services.
- Host tuning tests are unit tests; live sysctl application is serialized in Wave 2/3.
- Database tests use temporary data directories and unique ports.
- Global PHP/Composer tests use a temporary PATH/shim root; do not replace
  `/usr/local/bin` during unit tests.
- Laravel/Nuxt fixtures are local test fixtures; no external network except pinned
  images/artifacts already required by the test.

## 7. Review protocol

Every task review requires both verdicts:

1. **Spec compliance** — does it implement the task’s exact requirements?
2. **Task quality** — correctness, tests, security boundary, no YAGNI, no unsafe paths.

The fix loop is mandatory. A task cannot be marked complete with open Critical/Important
issues. A task reviewer may report “cannot verify from diff”; the controller resolves it
before merge.

The final whole-branch review covers the entire wave, not just the last task. It runs on
the most capable available model and includes cross-lane integration risks.

## 8. Wave task catalog

Detailed task plans are per wave. The catalog is intentionally specific about boundaries
but not implementation signatures until the prior wave freezes them.

### Wave 0 — Foundation (serial)

| Task | Deliverable |
|---|---|
| F1 | Root layout, new paths, permissions |
| F2 | Service catalog/state types and SQLite state |
| F3 | Compose rendering, fixed command runner, helper service RPC |
| F4 | Config manifest, hash/conflict/backup/atomic-write engine |
| F5 | Site contract fields, migrations, API types for front controller/.htaccess/build |
| F6 | Service API/CLI/UI skeleton and read-only service listing |

### Wave 1 — Parallel lanes

**Lane A — Database services**
- MySQL and MariaDB container lifecycle, activation conflict, data bind mounts.
- PostgreSQL and Redis containers.
- Random credential generation/storage/reveal/rotate primitives.
- DB config base files and validation.
- No phpMyAdmin yet (Wave 2, depends on active MySQL family).

**Lane B — Web servers**
- nginx and Apache container lifecycle, host networking, compose config.
- Port ownership switch from builtin/native to Docker and rollback.
- Full vhost parity: static/PHP/Node, TLS, force HTTPS, stopped 503, default 404,
  dashboard vhost.
- `.htaccess` policy, `AllowOverride`, front-controller file/mode.
- Live Laravel-style Apache/nginx fixture.

**Lane C — Developer tooling**
- Global `/usr/local/bin/php` shim, PHP CLI default, global ini.
- Global Composer PHAR wrapper and conflict backup.
- `devstack shell`, `devstack exec --site`, `devstack shell-init`.
- Static build profile fields/executor for Nuxt/Vite; output validation.
- No service-container dependencies.

### Wave 2 — Follow-ups

**Lane A2**
- phpMyAdmin container, cookie login, active DB alias, credentials UI, rotation API/UI.
- Data deletion backup/verify.

**Lane B2**
- Throughput profile generation for nginx/Apache/PHP-FPM.
- Host sysctl/limits apply/reset via helper.
- Benchmark harness and result storage.
- Config include layers separate from user base config.

**Lane C2**
- Dashboard Services page completion.
- Config editor/diff/conflict UI.
- Build profile/UI, build logs.
- Site terminal/command runner.
- Credential reveal/copy/rotate UI, tool management, performance UI.
- CLI parity for all operations.

### Wave 3 — Integration

- Full dashboard-only acceptance.
- Laravel `.htaccess` and front-controller live tests.
- Nuxt static build and SPA/SSG behavior.
- Service switch/rollback/ports/health.
- CA/trust across container replacement.
- Credential leakage and data deletion.
- Performance profile apply/benchmark/reset.
- M1 regression suite.
- Documentation/handoff update.

## 9. Wave exit criteria

A wave is mergeable only when:

- every task has a clean task review;
- wave integration tests pass;
- `go test ./... -race -count=1` passes;
- UI check/unit/build passes when UI changed;
- Docker smoke passes when services changed;
- existing M1 acceptance remains green;
- docs/handoff updated;
- `master` CI green after push.

## 10. Risk register

| Risk | Mitigation |
|---|---|
| Shared-file merge conflicts | foundation owns hot files; per-lane ownership; serial merge queue |
| Blocking review loops | exact task briefs/interfaces/tests; scoped re-review; no open Critical/Important |
| Docker port/data collisions | unique Compose project names, high ports, temp roots; serial live tests |
| Hidden cross-lane dependency | contract freeze; interface tests; wave integration gate |
| Agent context loss | task briefs/reports/review packages as files; ledger per wave |
| Performance changes destabilize host | helper apply with backup/reset; serialized live test |
| CA/trust regression | native CA tests; container replacement acceptance |
| Credential leak | never in list/logs/HTML; reveal endpoint tests |
| Worktree sprawl | remove completed worktrees; one plan workspace; check `git worktree list` |
| Scope creep | spec is authority; new requirements go back through design/planning |

## 11. Execution handoff

Wave 0 starts only after the design is approved. The foundation plan is the first
execution plan; wave plans 1+ are written after foundation merge and before their work
begins. Do not implement service features before their wave plan exists.

The controller must keep a wave ledger in the plan workspace with:

- task/base/head commits;
- review result;
- merge order;
- rulings;
- worktree paths;
- CI/wave gate outcomes.

If context is lost, trust the ledger and `git log` over memory.
