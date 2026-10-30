# Node Toolchain Handoff - Design Knowledge and Current State

- **Date:** 2026-10-30
- **Status:** Implemented and live-E2E tested 2026-10-30
- **Base commit:** `a36c6b6` (`fix: harden WSL browsing, lifecycle, and startup paths`)
- **Design:** [Per-Site Node Toolchains and Global Node Default](../specs/2026-10-30-node-toolchain-design.md)
- **Plan:** [Node toolchain implementation plan](../plans/2026-10-30-node-toolchain.md)
- **E2E evidence:** [Node toolchain E2E report](../audits/2026-10-30-node-toolchain-e2e.md)
- **Next gate:** final commit and push

This handoff records everything learned while shaping the design so a fresh
agent can plan or implement without re-discovering the codebase.

---

## 1. What was requested

1. First-class per-site Node settings for every site kind, not only Node server
   sites.
2. A per-site Node version that `node`, `npm`, and `npx` use inside
   `devstack shell`, `devstack exec`, and the dashboard terminal.
3. Install all official Node versions from the dashboard and CLI.
4. Default discovery shows the newest patch of each major; older minors/patches
   are available on demand but not normally shown.
5. A machine-wide/global default Node version, like the PHP CLI default.
6. The architecture choice was delegated: embedded-only catalog, live official
   index, or a devstack-published refreshable catalog. The user accepted the
   recommendation: **live official index + SHA-256 verification + embedded
   fallback**.

The user’s explicit behavioral choice:

- Node server sites must pin an explicit version.
- Static and PHP sites may inherit the global default (or pin explicitly).
- Every selector shows `Inherit (global: vX)` only for the optional cases.

---

## 2. Current behavior and gaps

### 2.1 Site model

- `model.Site.NodeVersion` already exists (`internal/model/model.go`).
- It is required only for `Kind == node`.
- `sites.CreateRequest` accepts `NodeVersion`.
- `sites.UpdateRequest` does **not** contain `NodeVersion`, so the PATCH API
  cannot change an existing site’s Node pin.
- `BuildProfile.NodeVersion` is a separate, already-required build-time value.
- `internal/sites/backendSpec` maps `RuntimeVersion` only when the kind is Node:
  ```go
  runtimeVersion := ""
  if site.Kind == model.SiteNode {
      runtimeVersion = site.NodeVersion
  }
  ```
  This is why static and PHP site terminals currently lack a Node toolchain.

### 2.2 Shells and terminals

- `internal/agent/runtime.go` `Runtime.sitePathEnv` prepends the Node bin only
  from `spec.RuntimeVersion`; because of `backendSpec`, only Node-kind sites get
  it.
- `internal/agent/terminal.go` uses `sitePathEnv` for the PTY.
- `internal/agent/build.go` `ExecSite` uses `sitePathEnv`.
- `internal/tooling/siteenv.go` resolves Node builds for Node sites and static
  build profiles on Linux, writes per-site shims, and sets `DEVSTACK_*` values.
- `internal/tooling/windows.go` `WindowsSiteEnvironment` uses
  `site.NodeVersion`, then `site.Build.NodeVersion`, and constructs
  `/data/devstack/runtimes/node@<value>/bin` directly.
  - A stored line such as `24` becomes the nonexistent `node@24` path when the
    installed build is `node@24.22.1`. The agent resolver handles lines; this
    Windows path builder does not.
- The dashboard terminal for Node sites works (verified live: Node 24.22.1 and
  npm 11.19.1). Static build and PHP terminals do not get Node unless a global
  `node` happens to be on `PATH`.

### 2.3 Runtime catalog and installation

- `internal/catalog/catalog.json` is embedded and contains only:
  - `node@24.22.1`
  - `node@22.23.4`
  - PHP 8.2.35, 8.3.36, 8.4.27, 8.5.12.
- `docs/decisions.md` C8 requires a checksum-pinned embedded PHP/Node catalog;
  `docs/development.md` says entries are embedded and never fetched.
- `internal/runtime.Manager` owns download, checksum, extraction, atomic rename,
  registration, verification, and removal.
- On Windows/WSL, runtime installation goes through the agent runtime API; the
  builder review found that the agent install request carries only a build ID,
  so a live-discovered entry cannot reach it without a protocol extension.
- The runtime page/API (`/api/v1/runtimes/available`, `devstack runtime list`)
  exposes only embedded catalog entries and has no refresh/filter/discovery
  model.
- There is no Node smoke test after extraction; only PHP has an FPM smoke gate.

### 2.4 PHP global default as the reference model

- Setting key: `php_default` (`internal/tooling/php.go`).
- `ResolveDefault` selects an exact build or line, otherwise the newest
  installed PHP build.
- Windows shims: `<home>/bin/php.cmd` and `composer.cmd`.
- WSL shims: `<home>/tools/php-default/bin`.
- Linux global wrappers are helper-managed in `/usr/local/bin` via
  `helperproto.ToolPHPDefault`; Composer has a similar managed-tool path.
- Dashboard Settings has a full "PHP CLI default" card with status, resolved
  build, shim directory, global ini, Save/Clear.
- CLI: `devstack php default [line|build|--status|--clear]`.
- Node needs the same model, including the helper-managed Linux wrappers; a
  home-only shim would not satisfy the user’s “system-wide” requirement.

### 2.5 Removal and in-use checks

- `sites.CheckRuntimeRemoval` blocks PHP builds referenced by PHP sites and Node
  builds referenced by Node-kind sites.
- It does not consider static build profiles, future all-kind Node pins, or the
  global default.
- `RuntimeInUseError` now names Node/PHP runtime correctly (commit `6af38d2`).

### 2.6 UI

- `AddSite.svelte` has a Node version selector for Node sites and a build Node
  selector for static build profiles.
- `SiteDetail.svelte` shows the build Node version but not an editable Node
  toolchain for Node/PHP/static sites.
- `Runtimes.svelte` lists embedded available/installed builds with plain
  install/remove actions.
- `Settings.svelte` has the PHP default card but no Node equivalent.
- There is no shared Node version picker or “show all versions” interaction.

---

## 3. Chosen architecture

### 3.1 Discovery

- Primary source: `https://nodejs.org/dist/index.json`.
- Eligibility: stable semver only, `linux-x64` file present, official HTTPS
  tarball URL under `https://nodejs.org/dist/v<version>/`.
- Trust: per-release `https://nodejs.org/dist/v<version>/SHASUMS256.txt`, exactly
  one matching lowercase SHA-256 line. The index is a hint; the checksum is
  authoritative.
- Embedded catalog remains the offline fallback; live entries win for the same
  version.
- Index refresh is one request. Checksums and sizes are lazy metadata fetched
  by the install preflight (`GET /api/v1/runtimes/node/releases/{version}`) or
  at install time.
- Cache under `<home>/cache/node/`; index TTL 6 hours; explicit refresh only;
  atomic `0600` writes; stale/offline states are explicit.
- No network in tests, daemon startup, or normal site operations.

### 3.2 Version selection

- Site create/update: resolve against **installed** builds only. Never persist
  an uninstalled version. If no match, offer install-first.
- `runtime install`: resolve against **discovered** releases.
  - `node@22` → newest stable in major 22
  - `node@22.11` → newest patch in minor 22.11
  - `node@22.11.0` → exact
- Store exact `major.minor.patch`.
- Site pickers default to installed builds; discovery is a secondary “install
  another version” view.

### 3.3 Shell/build resolution

Shell/terminal/exec:
1. site `node_version`
2. global Node default
3. clear “no Node toolchain” error (never silently choose another build)

Build execution:
1. `build.node_version` (required for a build profile)
2. legacy fallback: site `node_version`
3. global default

`build.node_version` is **not** the shell toolchain. A static build site with
no site pin inherits the global default, matching the picker label.

### 3.4 Global default

- Store setting `node_default`; exact build preferred, line accepted for
  compatibility.
- Resolve configured value, otherwise newest installed compatible Node build.
- Linux: helper-managed `/usr/local/bin/node`, `npm`, `npx` wrappers, like PHP.
- Windows: `<home>/bin/node.cmd`, `npm.cmd`, `npx.cmd`.
- WSL: `<home>/tools/node-default/bin`.
- Dashboard Settings card and `devstack node default [<version>|--status|--clear]`.
- Explicit default is never overwritten by installing a build.
- Does not change Node server site pins.

### 3.5 Editing and API

- `UpdateSiteRequest.node_version` becomes nullable/pointer to distinguish
  “not supplied” from “clear to inherit”.
- Clearing is rejected for Node-kind sites.
- CLI: `devstack site node <name> [<version>|--clear]`.
- Node server version changes require a stopped site; shell/build changes take
  effect on the next shell/build.
- Runtime removal blocks site pins, build pins, and the global default.

### 3.6 Dashboard/CLI discovery UX

- Runtimes page: one row per major (newest patch), LTS/Security badges,
  installed state, bundled npm; per-major “Show all versions” discloses every
  stable release.
- Refresh action reports cache age and stale/offline/error state.
- Install confirmation fetches checksum/size lazily and shows security warnings
  for old releases.
- CLI `runtime available [--kind node] [--major N] [--all] [--refresh] [--offline]`.
- CLI `runtime install node@...` uses the same discovery/resolution.

### 3.7 Installation/verification pipeline

Daemon owns discovery/cache. WSL agent owns distro downloads and filesystem
mutation. The daemon resolves and pins an entry; on Windows it sends the full
pinned entry (ID/version/URL/SHA-256/size) to the agent; the agent revalidates
host, size cap, checksum, and ID. On Linux the local runtime manager consumes
the same entry. Both use the existing verified download/extract path, then run
`node --version` (and require `npm` when packaged) before registration. No
build is registered on failure.

---

## 4. Approaches rejected

### Embedded catalog only

Would require embedding hundreds of 30–60 MB archives, make the binary huge,
and require a devstack release for every Node release. It cannot honestly claim
“all versions.” Rejected.

### Devstack-published refreshable catalog

A signed `node-catalog.json` release asset would preserve one trust root and
one refresh path, but new Node releases lag until republishing. Kept as a
possible future mirror, not the primary source.

### Live index without checksum files

Rejected. The index does not pin archive contents; only the official
`SHASUMS256.txt` does. Checksum verification is mandatory.

---

## 5. Builder review and resolutions

A fresh builder review raised six ambiguities. The spec now settles them:

1. **Installed vs discovered resolution** — sites use installed builds;
   `runtime install` uses discovery. Site forms offer install-first.
2. **Windows/WSL install ownership** — daemon discovers/pins; agent downloads
   and revalidates via an extended runtime-install request. No second agent
   catalog or cache.
3. **Checksum/size timing** — lazy per-release preflight; one index request for
   summaries; no hundreds of requests on refresh.
4. **Build profile vs shell** — build profile is build-only; shell uses site pin
   then global default.
5. **System-wide global default** — helper-managed `/usr/local/bin` wrappers on
   Linux, `.cmd` shims on Windows, WSL shims.
6. **Editing** — add `site node <name> <version>` / `--clear`; nullable API
   field.

---

## 6. Risks and open questions

- **Supply chain:** nodejs.org TLS + SHASUMS is a different trust root from the
  embedded catalog. The design mitigates with strict host allowlists, checksum
  verification on both daemon and agent, bounded downloads, and no redirect
  off-host.
- **Old Node compatibility:** very old releases may not run on Debian 12
  (glibc/OpenSSL). The install smoke (`node --version`) fails closed.
- **Native modules:** switching Node versions can break native addons. UI warns
  about `npm rebuild`; devstack does not run it automatically.
- **Cache freshness vs offline:** stale data must be clearly marked; embedded
  fallback is intentionally limited to released LTS entries.
- **Agent protocol:** extending the runtime install request is a daemon/agent
  contract change and needs protocol/version tests.
- **Helper wrappers:** Node wrappers add root-owned files on Linux; target
  validation, conflict handling, uninstall, and tests must mirror `php-default`.
- **Migration:** existing line-valued pins must normalize only when an installed
  exact build exists; unresolved values stay visible and actionable.
- **Implementation is not started**; the task-by-task plan is written and awaits
  review/execution-mode selection.
- **Exact JSON/OpenAPI shapes** are left to the plan.
- **Prerelease toggle** is out of scope.

---

## 7. Implementation plan phases

The task-by-task plan is [2026-10-30-node-toolchain.md](../plans/2026-10-30-node-toolchain.md).
It uses parallel worktree lanes: Wave 1 runs discovery/runtime and global-default
lanes concurrently; Wave 2 runs daemon/API and site-toolchain lanes; Wave 3 runs
the dashboard lane; the main agent merges each reviewed lane in order and runs
Task 15 on `master`. Its phases are:

1. **Docs and contracts:** spec links, OpenAPI schemas/routes, decisions C8
   amendment, CLI/UI copy.
2. **Discovery core:** `internal/nodereleases` parser/cache/selection/preflight,
   strict fixtures, no-network tests.
3. **Runtime install:** pinned-entry plumbing through the runtime manager and
   agent protocol; Node smoke; atomic registration; removal protections.
4. **Site model/API/CLI:** nullable updates, exact normalization, all-kind
   resolution, `site node`, in-use checks, migration.
5. **Global default:** store setting, resolver, helper `node-default` wrappers,
   Windows/WSL shims, `node default` CLI/API, Settings card.
6. **Shell/agent integration:** `backendSpec`, `sitePathEnv`, Windows environment
   resolution, terminal/exec/env variables for all kinds.
7. **Dashboard:** shared picker, Add/Site Detail/build integration, Runtimes
   grouping/disclosure/refresh/install preflight, mocks/tests.
8. **Verification:** unit, API, CLI, UI, WSL race, live E2E with current and
   older Node versions, offline/stale cache, removal/default conflicts,
   installer/lifecycle/helper suites.

---

## 8. Files most likely to change

- `internal/model/model.go`, `internal/sites/service.go`, `internal/sites/php_apply.go`
- `internal/catalog/*`, new `internal/nodereleases/*`, `internal/runtime/*`
- `internal/agentproto/*`, `internal/agent/*`, `internal/backend/wsl/*`
- `internal/tooling/*`, `internal/helper/*`, `internal/helperproto/*`
- `internal/api/handlers_runtime.go`, `handlers_tooling.go`, `server.go`
- `internal/cli/runtime.go`, new `internal/cli/node.go`, `site.go`
- `ui/src/routes/Runtimes.svelte`, `AddSite.svelte`, `SiteDetail.svelte`,
  `Settings.svelte`, `ui/src/lib/api.ts`, `mock.ts`, `types.ts`, new picker
- `api/openapi.yaml`
- `docs/sites.md`, `docs/architecture.md`, `docs/decisions.md`,
  `docs/development.md`, `docs/handoff.md`

---

## 9. Verification expectations

- Discovery unit tests use local fixtures, never nodejs.org.
- Install tests cover checksum mismatch, missing size, bad archive, traversal,
  symlink, duplicate checksum lines, and Node smoke failure.
- API tests cover summary/filter/all/refresh/preflight/default/site update.
- CLI tests cover `node@22`, `node@22.11`, exact, offline, `site node`,
  `node default`.
- UI tests cover major grouping, disclosure, inherit state, install-first, and
  Settings default.
- WSL `-race` and native suites stay green.
- Live E2E installs one current and one older Node, verifies shell/terminal
  `node -v`/`npm -v`, switches a site pin, and tests removal/default conflicts.
- Installer/lifecycle/helper tests cover the new Linux wrappers and Windows
  shims.

---

## 10. Governing documents after this design

- [Per-Site Node Toolchains and Global Node Default](../specs/2026-10-30-node-toolchain-design.md)
- [M1 design](../specs/2026-10-22-dev-web-stack-m1-design.md) — historical,
  Node parts superseded
- [Windows/WSL design](../specs/2026-10-26-windows-wsl-design.md) — Node parts
  superseded
- [decisions.md](../../decisions.md) — C8 needs amendment when implemented
- [sites.md](../../sites.md) — update after implementation
- [development.md](../../development.md) — runtime-catalog policy update after
  implementation
- [architecture.md](../../architecture.md) — add live Node discovery and global
  default after implementation
