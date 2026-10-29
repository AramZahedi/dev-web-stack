# Per-Site Node Toolchains and Global Node Default

- **Date:** 2026-10-30
- **Status:** Proposed
- **Scope:** Node runtime discovery/install, per-site Node selection for every site kind, global Node CLI default, dashboard and CLI surfaces
- **Supersedes:** the Node-specific parts of the runtime-catalog section in the 2026-10-22 M1 design and the Node option rows in `docs/sites.md`
- **Implementation plan:** [2026-10-30 node toolchain plan](../plans/2026-10-30-node-toolchain.md)

## 1. Intent and why

Node is currently a second-class runtime outside Node sites. Only Node-kind
sites carry a version, it cannot be changed after creation, static build sites
and PHP sites do not get a Node toolchain in the dashboard terminal, and the
runtime catalog contains just two embedded Node builds. The global `node`,
`npm`, and `npx` commands are unmanaged.

The goal is to make Node a first-class per-site toolchain and a machine-wide
default, matching the PHP model:

- every site can pin the Node build its shells, terminals, and builds use;
- Node server sites always pin explicitly so their serving runtime is stable;
- users can install any official Node release, discovered from the official
  index and verified before extraction;
- the default dashboard view shows only the newest release of each major, with
  older minors/patches available on demand;
- a machine-wide default provides `node`, `npm`, and `npx` outside site shells
  and is inherited by sites that do not pin Node.

## 2. Goals

1. Add an optional `node_version` to every site kind; Node server sites require
   it, static/PHP sites may inherit the global default.
2. Store exact Node versions, not lines, and resolve every display, shell,
   terminal, build, and validation path against the exact installed build.
3. Make the same Node selection available when creating and editing a site,
   including static build profiles.
4. Discover all official Linux x64 Node releases from
   `https://nodejs.org/dist/index.json`, verify each archive against the
   release's official `SHASUMS256.txt`, and cache the discovery result.
5. Show the newest patch of each major by default; disclose all versions of a
   major on demand, including older minors and patches.
6. Install any discovered version from the dashboard, CLI, or API.
7. Add a machine-wide Node default with `node`/`npm`/`npx` shims, Settings UI,
   and a CLI command, mirroring the PHP CLI default.
8. Preserve the embedded catalog as an offline fallback and keep installs
   impossible without a verified checksum.
9. Protect pinned Node builds from removal with the same in-use checks PHP has.
10. Normalize existing line-valued Node selections without changing behavior
    unexpectedly.

## 3. Non-goals

- Windows-native Node. The daemon and agent remain WSL/Linux-first; Windows
  commands continue to invoke the managed distro.
- Replacing nvm, fnm, Volta, or Corepack. devstack manages toolchains, not
  shell-version managers.
- Automatically tracking `.nvmrc`, `.node-version`, or `package.json engines`.
  Those can be a future opt-in, but the per-site value is authoritative here.
- Installing non-official builds, nightly/prerelease builds by default, or
  arbitrary user-supplied catalogs.
- Managing global npm packages or per-project npm caches.
- Auto-updating installed runtimes or silently changing a site's runtime.
- Windows 10 22H2 and clean-machine verification, which remain separate
  environment work.

## 4. Anti-goals

- A site's Node version changing because the global default changed.
- Installing an archive whose SHA-256 was not verified, or trusting an unsigned
  index field without the official checksum file.
- Hiding older versions so completely that a user cannot reproduce an old
  project.
- A dashboard that only works online. Embedded fallback and cached discovery
  must keep the app usable.
- A separate Node catalog implementation that drifts from the PHP runtime
  manager.

## 5. Constraints

- Node is executed inside the managed Debian 12 WSL distro (Windows) or the
  local Linux host; only `linux-x64` tarballs are eligible.
- Runtime IDs remain `node@<major>.<minor>.<patch>`.
- The existing runtime manager owns download, checksum, extraction, atomic
  rename, registration, verification, and removal. Discovery must feed it, not
  bypass it.
- PHP default behavior is the reference: store setting, exact build resolution,
  newest-installed fallback, Windows `.cmd` shims, WSL shims, and a Settings
  card.
- Existing sites and stored settings must upgrade without a schema-breaking
  migration. `model.Site.NodeVersion` already exists.
- The agent is the source of truth for Linux paths and installed runtimes;
  Windows shell resolution must not concatenate guessed paths.
- All network access is HTTPS, host-restricted, bounded, and never performed
  during unit tests.

## 6. Approaches considered

### A. Expand the embedded catalog only

Add every historical Node release to `internal/catalog/catalog.json`.

- Pro: keeps the current "no network catalog" invariant and works offline.
- Con: hundreds of releases at 30–60 MB each make the binary and update cadence
  impractical; new Node releases require a devstack release; the "all versions"
  promise becomes a curated subset in practice.
- Rejected.

### B. Live official Node index with per-release checksum verification (chosen)

Fetch `https://nodejs.org/dist/index.json`, map each stable release's
`linux-x64` file, fetch `https://nodejs.org/dist/v<version>/SHASUMS256.txt`, and
verify the archive before extraction. Cache the index and checksum data; keep
the embedded catalog as fallback.

- Pro: genuinely all official releases; current data without a devstack
  release; existing verified-install pipeline reused; matches how Node users
  already obtain binaries.
- Con: depends on nodejs.org at discovery/install time; the trust root moves
  from the devstack binary to nodejs.org plus TLS/checksum; needs caching,
  refresh, and strict parsing.
- Chosen.

### C. Devstack-published refreshable catalog

Publish a signed/checksummed `node-catalog.json` as a devstack release asset and
refresh from it.

- Pro: one trust root and one refresh path for Node and PHP.
- Con: new Node releases lag until the catalog is republished; a separate
  publishing pipeline; still needs an embedded fallback.
- Could be added later as a mirror, but not the primary source.

## 7. Decisions

### 7.1 Source and trust

- Primary discovery is the official Node index over HTTPS, restricted to
  `nodejs.org` (no redirect to another host).
- A release is eligible only when:
  - the version is stable semver (`major.minor.patch`, no prerelease suffix);
  - `files` contains `linux-x64`;
  - the per-release `SHASUMS256.txt` contains exactly one line for
    `node-v<version>-linux-x64.tar.gz` with a 64-character lowercase SHA-256;
  - the tarball's HTTPS URL is under `https://nodejs.org/dist/v<version>/`;
  - the download is bounded by the configured maximum.
- The checksum is authoritative. The index is a discovery hint, never a
  substitute for `SHASUMS256.txt`.
- The embedded catalog remains the offline fallback and contains at least the
  latest LTS lines shipped with the release. Live entries take precedence for
  the same version.
- Checksum data is cached with the index; cached checksums are revalidated when
  the index is refreshed.

### 7.2 Discovery data and grouping

- A release record carries: exact version, major/minor/patch, release date,
  LTS name or false, security flag, bundled npm version, module ABI, download
  filename, and URL. SHA-256 and size are **lazy metadata** fetched when the
  user opens the install confirmation or installs the release, not during the
  index refresh.
- Size is obtained from an HTTPS `HEAD`/`Content-Length` check before install
  and recorded with the checksum. A missing or non-positive size fails the
  install with a clear error; the archive is still checksum-verified.
- The default summary returns one row per major: the newest stable release.
  Rows show `LATEST`, `LTS`, and `SECURITY` badges where applicable. Size and
  checksum are shown as “checked at install” until the preflight request loads
  them.
- A major can be expanded to list every stable release in that major, newest
  first, including every minor and patch.
- Prerelease/nightly builds are excluded. An explicit future flag can add them,
  but this design does not.
- Older majors are not hidden; they are labelled as older releases and carry
  the same warnings. We do not invent an EOL date that the Node index does not
  provide.

### 7.3 Cache and refresh

- Index cache path: `<home>/cache/node/index.json`; checksum cache:
  `<home>/cache/node/shasums/<version>.txt`; size cache:
  `<home>/cache/node/sizes/<version>.json`.
- Index refresh is one HTTPS request. Checksums and sizes are fetched lazily:
  only when the install confirmation is opened or an install starts. A summary
  refresh never performs hundreds of per-release requests.
- Default index freshness: 6 hours. A cached index is served immediately and
  refreshed in the background only when an explicit action requests it.
- `POST /api/v1/runtimes/node/refresh` forces an index refresh and returns the
  new summary, `cached_at`, and any warning. It does not prefetch checksums.
- `GET /api/v1/runtimes/node/releases/{version}` is the preflight endpoint. It
  returns the official checksum, size, URL, npm version, and security flags,
  fetching missing metadata and caching it for the install.
- If refresh fails:
  - with a cache: serve the cache and mark it stale with the fetch error;
  - without a cache: serve the embedded fallback and mark it as offline.
- Cache writes are atomic (`0700` directory, `0600` files) and never contain
  credentials.
- No discovery request is made during daemon startup, tests, or site
  operations. Discovery is lazy and explicit.

### 7.4 Version selection and resolution

- Persist exact versions. The UI/API accepts a major (`22`), a minor (`22.11`),
  an exact version (`22.11.0`), or an installed build ID; resolution stores the
  exact `major.minor.patch`.
- **Site create/update always resolves against installed builds.** A site can
  never be saved with an uninstalled Node version. If the requested selection
  has no installed match, the form/API offers to install the newest discovered
  release for that selection first
  (`NODE_VERSION_NOT_INSTALLED`), then saves the exact installed build.
- **`runtime install` resolves against discovered releases.** `node@22`
  installs the newest stable release in major 22; `node@22.11` installs the
  newest patch in minor 22.11; an exact version installs exactly. This is the
  only command that installs a release not already present.
- Site pickers list installed builds by default and expose an explicit
  “Install another version” action that uses the discovery view. This keeps
  site creation usable offline and avoids forms depending on a live index.
- Resolution order for shell/terminal/exec:
  1. site `node_version` (exact);
  2. the global Node default;
  3. no Node toolchain (clear error), never a silent different build.
  `build.node_version` is **not** the shell toolchain; a static project with a
  build profile and no site pin inherits the global default, exactly as the
  “Inherit global default” label says.
- Resolution order for build execution:
  1. `build.node_version` (required for a build profile);
  2. for legacy profiles without one, the site `node_version`;
  3. the global Node default.
- Node server sites must have an explicit `node_version`. Creation may prefill
  it from the global default, but the stored value is a pin.
- Static/PHP site selectors show `Inherit global default (vX)`; Node server
  selectors do not.
- Changing a Node server's pinned version requires a stopped site and a
  restart, matching PHP build changes. Shell/build resolution changes apply to
  the next shell or build.
- `UpdateSiteRequest.node_version` is a nullable/pointer field so “not
  supplied” is distinct from “clear to inherit”. Clearing is allowed only for
  static/PHP; Node-kind updates reject it.

### 7.5 Global Node default

- Store setting key: `node_default`; value is an exact build ID (preferred) or
  a version line for compatibility.
- Resolution mirrors PHP: explicit setting, otherwise the newest installed
  compatible Node build; if none, the shims are absent and status is
  "not configured".
- `node`, `npm`, and `npx` shims are written to the same public locations as
  PHP:
  - Linux: the helper installs managed wrappers in `/usr/local/bin` (or the
    configured tool bin directory), exactly like `php`/`php-config`.
  - Windows: `node.cmd`, `npm.cmd`, and `npx.cmd` in `<home>/bin`, added to the
    user PATH by the installer.
  - WSL: `<home>/tools/node-default/bin`, used by site/global shells.
- The helper's managed-tool surface gains a `node-default` id with the same
  target/wrapper/status/conflict protections as `php-default`; wrappers resolve
  only registered Node builds under `<home>/runtimes`.
- Windows shims call the managed distro and the selected build; they do not run
  a native Windows Node.
- The global default is used outside sites and inherited by sites without a
  pin. It does not change a Node server site's pinned runtime.
- Dashboard Settings gets a "Node CLI default" card parallel to PHP: current
  status, resolved build, shim directory, shim installed, global version
  warnings, Save/Clear, and a link to install more versions.
- CLI: `devstack node default [<version>|--status|--clear]` mirrors
  `devstack php default`. `--json` returns the same status model.
- Global default changes take effect for new shells; existing shells keep their
  environment until reopened.

### 7.6 Site model and API

- `model.Site.NodeVersion` remains the per-site toolchain field:
  - Node kind: required, exact;
  - static/php: optional, exact when set;
  - empty means inherit (static/php only).
- `CreateSiteRequest` and `UpdateSiteRequest` accept `node_version` for all
  kinds. Update validates kind-specific rules and stopped/restart requirements.
- `BuildProfile.NodeVersion` remains the build execution pin and is normalized
  to exact.
- `GET /api/v1/sites/{ref}` and list responses expose `node_version` and the
  resolved `node_toolchain` summary (exact version, source: site/build/global,
  installed bool) without leaking paths.
- Validation errors:
  - `NODE_VERSION_REQUIRED` for Node kind without a pin;
  - `NODE_VERSION_NOT_INSTALLED` with the exact version and install
    remediation;
  - `NODE_VERSION_UNAVAILABLE` when discovery/install is required but offline.
- Runtime removal:
  - block when any site's `node_version` or `build.node_version` references the
    build;
  - block when `node_default` references it, with remediation to change or
    clear the default first;
  - return the same structured `RuntimeInUseError` with kind-aware wording.

### 7.7 Dashboard

**Runtimes page**

- Node section grouped by major, newest major first.
- Default rows show the newest patch per major with LTS/Security badges,
  installed state, bundled npm version, and size.
- Each major row has "Show all versions"; expanding lists every stable release
  in that major, grouped by minor, with install/installed actions.
- "Refresh available versions" button reports cache age, online/offline/stale
  state, and errors without removing cached choices.
- Install action opens a confirmation that lazily loads the official checksum
  and size, then shows version, npm version, size, checksum prefix, and the
  security warning for older releases.
- A bulk action can install the newest patch of each selected major; it is
  explicit, never automatic, and performs one preflight per selected major.

**Add Site and Site Detail**

- Shared `NodeVersionPicker` component used by Add Site, site configuration,
  build profile, and Settings.
- The picker’s default list is **installed builds**, grouped by major; a
  secondary “Install another version” action opens the discovery view.
- Discovery view: major rows with latest patch; “Show all versions” discloses
  every stable release in that major.
- Static/PHP: `Inherit global default (vX)` is the default choice; explicit
  installed versions are available.
- Node: a version is required; the global default is only a prefill.
- Saving a site never stores an uninstalled version. If a selection is missing,
  the picker offers to install it first and then selects the exact build.
- Site Detail shows the resolved toolchain source and the command used by
  `node -v` in the terminal; changing it shows the ABI/rebuild warning.
- Build profile keeps its own required Node selector, defaulting to the site
  toolchain or global default. It is independent of the shell toolchain.

**Settings**

- Node CLI default card parallel to PHP, with status, resolved build, shims,
  Save/Clear, and install-more link.
- Global default changes take effect for new shells; existing shells keep their
  environment until reopened.

### 7.8 CLI

- `devstack runtime available [--kind node] [--major N] [--all] [--refresh] [--offline]`
  - default: newest patch per major plus PHP entries;
  - `--major 22`: latest patch in that major;
  - `--major 22 --all`: every stable release in that major;
  - `--refresh`: force an index refresh;
  - `--offline`: cache/embedded only.
- `devstack runtime install node@22`, `node@22.11`, `node@22.11.0`, or a build
  ID; resolves against discovered releases, then installs the resolved archive
  after confirmation unless `--yes`.
- `devstack node default [<version>|--status|--clear]`.
- `devstack site add --node ...` accepts the same forms for any kind:
  - Node kind requires it;
  - static/PHP omit it to inherit, or set it explicitly;
  - values resolve against installed builds; if absent, the command offers to
    install the newest discovered match first.
- `devstack site node <name> [<version>|--clear]` edits or clears an existing
  pin; `--clear` is rejected for Node-kind sites. This mirrors
  `devstack site php`.
- `devstack site show` includes the exact pin and resolved toolchain source.
- JSON shapes are stable and documented in OpenAPI.

### 7.9 Agent and shell integration

- `backendSpec` sends the site's exact Node version for every kind, not only
  Node kind. The daemon resolves an inherited global default before preparing
  the site.
- `Runtime.sitePathEnv` prepends the selected Node bin for every site that
  resolves one.
- Terminal tickets carry the daemon-resolved exact Node version, so a new
  terminal for an inheriting site uses the current global default without
  restarting the site. `HandleTerminalWebSocket` prefers the ticket value and
  falls back to the site spec.
- `WindowsSiteEnvironment` receives a resolved exact runtime from the CLI’s
  site context (via the Node default status); it must never concatenate
  `node@22` when the installed build is `node@22.11.0`.
- Site API responses include a `node_toolchain` summary (exact version, source,
  installed) so UI and CLI agree on what a shell will use.
- `devstack shell`, `shell-init`, and `exec` expose
  `DEVSTACK_NODE_VERSION`, `DEVSTACK_NODE_SOURCE`, and the resolved `PATH`.
- `node -v`, `npm -v`, and `npx --version` in a site shell must return the
  selected build.
- Per-site shims remain for compatibility; they point at the exact build and
  are refreshed when the site version changes.

### 7.10 Install and verification pipeline

The daemon owns discovery and cache state. The WSL agent owns downloads and
filesystem mutations inside the distro; it must not run its own index discovery
or keep a second cache.

1. The daemon resolves the requested version selection to a release record and
   fetches the release's `SHASUMS256.txt` (or uses validated cached metadata).
2. The daemon verifies the checksum line and constructs a fully pinned
   `catalog.Entry`.
3. The daemon HEADs the tarball, requires a positive `Content-Length`, and
   records it with the checksum.
4. On Windows, the daemon sends the pinned entry in the agent runtime-install
   request (ID, kind, version, URL, SHA-256, size). The agent revalidates the
   URL host, size ceiling, checksum, and ID before downloading; neither side
   trusts the other's verification alone.
5. On Linux, the local runtime manager consumes the same pinned entry.
6. Download to the existing cache/temp path with the existing size ceiling.
7. Verify SHA-256 and size while streaming; reject mismatches.
8. Extract with the existing traversal/symlink guards.
9. Run `<build>/bin/node --version` and require the exact version; require
   `bin/npm` for the packaged npm version when present.
10. Register the build atomically; on any failure remove the temp tree and
    leave no row.
11. If the new build is eligible and no global default is configured, it does
    not become the default automatically. The newest-installed fallback is
    computed dynamically; an explicit default is never overwritten.

**Protocol impact:** `agentproto.RuntimeInstallRequest` (or the existing
runtime request) gains the pinned entry fields. This is a protocol extension,
not a second catalog. The daemon and agent ship together; the agent rejects
unknown or incomplete entries. The agent cache remains archive-only.

### 7.11 Migration and compatibility

- On startup, normalize stored site and build Node values that resolve to an
  installed build; persist the exact version. Unresolvable values are left
  unchanged and surfaced in doctor/UI.
- Existing Node sites already have explicit values; they keep them.
- Existing static build profiles keep their build version; the site toolchain
  remains empty (inherit), so shells gain the build/global Node instead of
  none.
- Existing Node builds installed from the embedded catalog remain valid and
  are preferred for the same exact version.
- Changing a Node version warns that native modules may need `npm rebuild`;
  devstack does not run it automatically.
- Old Node majors may not run on Debian 12. The install step fails closed with
  the binary's error rather than registering a broken build.

### 7.12 Security and failure behavior

- Host allowlist and HTTPS for index, checksums, and archives.
- Reject redirects off `nodejs.org`, userinfo URLs, query strings, fragments,
  non-linux-x64 files, oversize archives, malformed JSON, duplicate versions,
  invalid semver, and checksum files with unmatched or duplicate lines.
- Cache files are private; no secrets are stored.
- Offline states are explicit; no silent use of a different version.
- Removal and default changes are audited with the existing event/log
  mechanisms.
- The global default cannot point at an uninstalled build.

## 8. Components and interfaces

- `internal/nodereleases` (new): official index client, strict parser, index
  cache, version selection (`major`, `minor`, exact), lazy checksum/size
  preflight, and release summaries. Owned by the daemon.
- `internal/runtime`: accepts a pinned `catalog.Entry` from the discovery
  layer; installs through the existing verified pipeline; adds Node smoke
  validation; exposes installed Node builds to tooling and sites.
- `internal/catalog`: keeps embedded entries and shared `Entry`/ID types;
  gains live-entry conversion helpers, not network code.
- `internal/store`: `node_default` setting; no schema change expected.
- `internal/tooling`: Node default resolution, Windows/WSL shims, status, and
  global environment; parallel to `php.go`.
- `internal/helper` / `helperproto`: managed `node-default` wrappers for
  `/usr/local/bin`, with the same target/status/conflict protections as
  `php-default`.
- `internal/sites`: model/update validation, exact normalization, resolution
  source, in-use checks, build handling, `site node` service methods.
- `internal/agent` / `agentproto` / `backend/wsl`: runtime-install request
  carries the pinned entry; site specs carry the exact Node toolchain for all
  kinds; terminal PATH resolution uses it.
- `internal/api`: discovery summary, refresh, preflight, Node default, and site
  update endpoints; OpenAPI schemas.
- `internal/cli`: `runtime available`, extended `runtime install`,
  `node default`, `site node`, and site flags.
- `ui`: `NodeVersionPicker.svelte`, Runtimes major grouping, Settings card,
  Site Detail/Add Site integration, tests/mocks.

## 9. Acceptance criteria

1. A static site with no Node pin inherits the global default; `devstack shell`
   and the dashboard terminal report the same `node -v`, even when the site has
   a separate build-profile Node.
2. A PHP site can pin Node and gets it in its shell/build tooling; clearing the
   pin inherits the global default.
3. A Node site cannot be created or updated without an explicit exact version,
   and `site node <name> --clear` is rejected for Node kind.
4. Changing a site's Node version updates shells immediately and the server
   runtime after a restart; removing the build is blocked with the site named.
5. The global default persists, falls back to newest installed, and drives
   `node`, `npm`, and `npx` outside sites on Windows and Linux through managed
   shims.
6. The Runtimes page shows one row per major by default; expanding a major
   reveals all stable releases, including older minors/patches.
7. Installing `node@22`, `node@22.11`, and `node@22.11.0` resolves to the
   expected exact builds; every archive is SHA-256 verified.
8. Site create/update never saves an uninstalled version; the picker offers to
   install a missing selection first.
9. Discovery works from cache and embedded fallback when nodejs.org is
   unreachable; refresh errors are visible and non-destructive. Default
   summaries make one index request; checksum/size metadata loads lazily.
10. Existing sites/settings upgrade without data loss; line values normalize
    where possible.
11. Runtime removal protects site pins, build pins, and the global default.
12. `go test`, WSL `-race`, UI tests/check/build, and installer/lifecycle
    suites remain green.
13. Documentation and OpenAPI describe the new commands, routes, resolution
    order, cache behavior, and security model.

## 10. Builder review resolutions

The design review raised six ambiguities; they are settled as follows:

1. **Site selection uses installed builds; `runtime install` uses discovery.**
   Site create/update never stores an uninstalled version and offers an
   install-first action. `runtime install node@22` and friends resolve against
   discovered releases.
2. **The daemon owns discovery; the agent owns WSL installs.** The pinned entry
   travels in the agent runtime-install request; the agent revalidates it.
3. **Checksums and sizes are lazy.** Index refresh is one request; the install
   dialog/preflight fetches per-release metadata; bulk install does one
   preflight per selected major.
4. **Build-profile Node is build-only.** Shell/terminal resolution is site pin,
   then global default. The build profile has its own required selector.
5. **The global default is fully system-wide.** Linux uses helper-managed
   `/usr/local/bin` wrappers like PHP; Windows uses `.cmd` shims; WSL uses
   `tools/node-default/bin`.
6. **Existing sites are editable from the CLI.** `site node <name> <version>`
   and `--clear` mirror `site php`; the API uses a nullable update field.

## 11. Open decisions left to the builder

- Exact JSON response and cache file shapes, within the constraints above.
- UI copy and the exact disclosure interaction.
- Whether a future prerelease toggle is worth adding.
- The precise migration order for normalizing stored values.
