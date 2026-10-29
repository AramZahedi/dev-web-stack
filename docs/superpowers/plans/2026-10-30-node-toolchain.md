# Per-Site Node Toolchains and Global Node Default Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Node a first-class per-site toolchain for every site kind, install any official Linux x64 Node release, and add a PHP-style machine-wide Node default.

**Architecture:** The daemon owns a cached official Node index and resolves exact versions. The existing runtime manager and WSL agent install from fully pinned entries with SHA-256 verification. Sites store exact installed pins; static/PHP sites may inherit the global default. Shell/terminal/build paths resolve through one server-side toolchain resolver.

**Tech Stack:** Go 1.26, `net/http`, `httptest`, SQLite settings, Svelte 5, TypeScript, Vitest, PowerShell installer/lifecycle tests, Debian 12 WSL.

**Spec:** `docs/superpowers/specs/2026-10-30-node-toolchain-design.md`

## Global Constraints

- Node releases are `linux-x64` only; no native Windows Node.
- Every install is SHA-256 verified against the official `SHASUMS256.txt`; the index alone never authorizes an install.
- Site create/update stores exact versions and never stores an uninstalled Node build.
- Node server sites require an explicit pin; static/PHP sites may inherit and show `Inherit global default (vX)`.
- Shell/terminal/exec resolution is site pin → global default → explicit error. Build resolution is `build.node_version` → legacy site pin → global default.
- The embedded catalog remains the offline fallback; live entries win for the same version.
- Index cache TTL is 6 hours; refresh is explicit; tests and startup perform no discovery network calls.
- Cache and checksum writes are atomic, private (`0700`/`0600`), and never contain secrets.
- Existing runtime IDs stay `node@<major>.<minor>.<patch>`.
- Do not change PHP behavior except where the shared tooling abstraction requires it.
- Run `gofmt`, `go build ./...`, `go vet ./...`, the native and WSL test suites, and the UI checks before claiming completion.

## Review Focus

1. **Malformed or hostile discovery data** — redirects, duplicate checksum lines, bad semver, non-linux-x64 files, missing size, oversized archives. Every one must fail closed.
2. **Offline/stale cache behavior** — a cached or embedded list must remain usable, visibly stale, and never silently substitute a different version.
3. **Version drift** — line values (`22`, `22.11`) versus exact builds, build-profile versus shell resolution, and inherited defaults changing without a site restart.
4. **Global-default privilege boundary** — helper wrappers must only target registered builds under the managed runtime root; no arbitrary path or command.
5. **Site update/removal conflicts** — Node kind cannot clear its pin; static/PHP can; removal of a build used by a site, build profile, or the default is blocked with the exact owner.

---

### Task 1: Node release parser and selection

**Files:**
- Create: `internal/nodereleases/releases.go`
- Create: `internal/nodereleases/releases_test.go`
- Create: `internal/nodereleases/testdata/index.json`

**Interfaces:**
- Produces: `Release`, `Major`, `ParseIndex([]byte) ([]Release, error)`, `GroupByMajor([]Release) []Major`, `Select([]Release, string) (Release, error)`.

- [ ] **Step 1: Write the failing parser tests**

Cover: stable semver only, `linux-x64` required, duplicate version rejection, LTS/security/npm fields, major grouping with newest patch first, and `Select("22")`, `Select("22.11")`, `Select("22.11.0")`, invalid/unknown selections.

```go
func TestParseIndexFiltersStableLinuxX64Releases(t *testing.T)
func TestGroupByMajorPutsNewestPatchFirst(t *testing.T)
func TestSelectResolvesMajorMinorAndExact(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/nodereleases -run 'TestParseIndex|TestGroupByMajor|TestSelect' -v`
Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement the parser and selectors**

`Release` carries `Version, Date, LTS, Security, NPM, Modules, File`. `Major` carries `Major, Latest, Releases`. `ParseIndex` rejects malformed JSON, non-`v` versions, prerelease suffixes, duplicate versions, and entries without `linux-x64`. `Select` accepts `major`, `major.minor`, or exact and returns a clear `ErrReleaseNotFound`.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/nodereleases -run 'TestParseIndex|TestGroupByMajor|TestSelect' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodereleases
git commit -m "feat: parse and select official node releases"
```

---

### Task 2: Index cache, refresh, and lazy preflight

**Files:**
- Create: `internal/nodereleases/client.go`
- Create: `internal/nodereleases/cache.go`
- Create: `internal/nodereleases/client_test.go`
- Create: `internal/nodereleases/cache_test.go`
- Create: `internal/nodereleases/testdata/SHASUMS256.txt`

**Interfaces:**
- Consumes: Task 1 parser/selection.
- Produces: `Config`, `ListOptions`, `Snapshot`, `Preflight`, `New(Config) (*Client, error)`, `(*Client) List(context.Context, ListOptions) (Snapshot, error)`, `(*Client) Refresh(context.Context) (Snapshot, error)`, `(*Client) Preflight(context.Context, string) (Preflight, error)`.

- [ ] **Step 1: Write failing discovery tests**

Use `httptest` for the index, checksum file, and HEAD. Assert:
- first call fetches once and writes `cache/node/index.json`;
- a fresh cached call performs no request;
- a failed refresh serves cache with `Stale` and `Warning`;
- no cache + offline returns embedded catalog entries and `Source=embedded`;
- preflight requires exactly one matching checksum line and positive `Content-Length`;
- an off-host redirect or oversized archive fails closed.

```go
func TestListCachesIndexAndServesFreshCache(t *testing.T)
func TestListServesStaleCacheAfterRefreshFailure(t *testing.T)
func TestListOfflineFallsBackToEmbeddedCatalog(t *testing.T)
func TestPreflightRequiresChecksumAndPositiveSize(t *testing.T)
func TestPreflightRejectsOffHostRedirect(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/nodereleases -run 'TestList|TestPreflight' -v`
Expected: FAIL because `Client` is undefined.

- [ ] **Step 3: Implement the client and cache**

`Snapshot` carries `Majors`, `All`, `Source` (`live`, `cache`, `embedded`), `CachedAt`, `Stale`, `Warning`. Cache paths are `<home>/cache/node/index.json`, `shasums/<version>.txt`, and `sizes/<version>.json`. Restrict the index and archive host to `nodejs.org`; require HTTPS; cap the download at the existing 2 GiB archive limit and Node-specific smaller ceiling if configured. `Preflight` returns the exact `catalog.Entry` for the runtime manager.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/nodereleases -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nodereleases
git commit -m "feat: cache and verify official node releases"
```

---

### Task 3: Runtime manager pinned entries and Node smoke

**Files:**
- Modify: `internal/runtime/manager.go`
- Create: `internal/runtime/node_smoke.go`
- Create: `internal/runtime/node_smoke_test.go`
- Modify: `internal/runtime/manager_test.go`

**Interfaces:**
- Produces: `(*Manager) InstallEntry(context.Context, catalog.Entry) (model.RuntimeBuild, error)`.
- Produces: `nodeSmoke(buildRoot, expectedVersion string) error`.

- [ ] **Step 1: Write failing tests**

```go
func TestInstallEntryUsesPinnedArchive(t *testing.T)
func TestNodeSmokeRejectsVersionMismatch(t *testing.T)
func TestInstallEntryVerifiesNodeVersionBeforeRegistration(t *testing.T)
```

Use the existing archive fixtures and a fake `bin/node` that prints the expected version. A failing smoke must leave no row and no target directory.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/runtime -run 'TestInstallEntry|TestNodeSmoke' -v`
Expected: FAIL because `InstallEntry` and `nodeSmoke` are undefined.

- [ ] **Step 3: Implement the entry path**

`InstallEntry` delegates to the existing `installEntry(ctx, entry, "")`. After extraction and before registration, Node entries run `nodeSmoke`. `nodeSmoke` executes `<temp>/bin/node --version`, trims a leading `v`, and requires exact equality with `entry.Version`; it also requires `bin/npm` when present. The existing size/checksum/extraction guards must remain untouched.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/runtime -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime
git commit -m "feat: install pinned node entries with version smoke"
```

---

### Task 4: Agent pinned-artifact installation

**Files:**
- Modify: `internal/agentproto/protocol.go`
- Modify: `internal/agent/php.go`
- Modify: `internal/agent/runtime_http.go`
- Create: `internal/agent/runtime_install_test.go`
- Modify: `internal/agentclient/client_test.go`

**Interfaces:**
- Produces: `RuntimeArtifact{ID, Kind, Version, Build, URL, SHA256 string, Size int64}`; `RuntimeRequest.Artifact *RuntimeArtifact`.
- Agent `InstallRuntime` accepts a pinned artifact and revalidates it before using the existing download/extract path.

- [ ] **Step 1: Write failing agent tests**

```go
func TestAgentInstallRuntimeAcceptsPinnedArtifact(t *testing.T)
func TestAgentInstallRuntimeRejectsUnsafeArtifact(t *testing.T)
```

Reject missing fields, non-`nodejs.org` hosts, non-HTTPS URLs, `node` IDs with revisions, checksum not 64 hex characters, non-positive size, and `..` in the ID.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/agent -run 'TestAgentInstallRuntime' -v`
Expected: FAIL because `RuntimeArtifact` is undefined.

- [ ] **Step 3: Implement the protocol and agent path**

When `Artifact` is present, construct a `catalog.Entry` after validation and reuse the same verified download/extraction flow as the embedded path. Never trust the daemon’s checksum alone: the agent re-hashes and checks size. Leave the embedded `BuildID` path working for existing clients.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/agent ./internal/agentclient -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agentproto internal/agent internal/agentclient
git commit -m "feat: accept pinned node artifacts in the wsl agent"
```

---

### Task 5: Daemon discovery wiring and Windows runtime service

**Files:**
- Modify: `internal/daemon/daemon.go`
- Modify: `internal/daemon/windows.go`
- Create: `internal/daemon/node_releases_test.go`

**Interfaces:**
- Consumes: Task 2 client; Task 3 manager; Task 4 agent protocol.
- Produces: a daemon-owned `*nodereleases.Client` and runtime service methods `NodeReleases`, `NodeRelease`, `RefreshNodeReleases`.

- [ ] **Step 1: Write failing daemon tests**

```go
func TestLocalDaemonWiresNodeReleaseClient(t *testing.T)
func TestWindowsRuntimeServiceInstallsNodeSelectorFromDiscovery(t *testing.T)
func TestWindowsRuntimeServiceSendsPinnedArtifact(t *testing.T)
```

Use a fake release client and fake agent; assert the artifact fields match the preflight entry and that no agent call happens when preflight fails.

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/daemon -run 'TestLocalDaemonWiresNodeReleaseClient|TestWindowsRuntimeService' -v`
Expected: FAIL because the service methods and wiring are missing.

- [ ] **Step 3: Wire the source and Windows install path**

Construct the client from `cfg.Home` and cache paths. Extend `windowsRuntimeService` to resolve `node@...` selectors through discovery/preflight, then call `agent.InstallRuntime` with `RuntimeArtifact`. Keep the embedded PHP path unchanged. Local daemons use `runtime.Manager.InstallEntry`.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/daemon -run 'TestLocalDaemonWiresNodeReleaseClient|TestWindowsRuntimeService' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon
git commit -m "feat: wire node discovery into daemon runtime services"
```

---

### Task 6: API discovery, refresh, and preflight endpoints

**Files:**
- Modify: `internal/api/handlers_runtime.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/api_test.go`
- Modify: `internal/client/client.go`
- Modify: `internal/client/client_test.go`
- Modify: `api/openapi.yaml`

**Interfaces:**
- Consumes: Task 5 runtime service.
- Produces: `GET /api/v1/runtimes/node/releases`, `GET /api/v1/runtimes/node/releases/{version}`, `POST /api/v1/runtimes/node/refresh`.
- Client: `NodeReleases(ctx, major string, all, offline bool) (nodereleases.Snapshot, error)`, `NodeRelease(ctx, version string) (nodereleases.Preflight, error)`, `RefreshNodeReleases(ctx) (nodereleases.Snapshot, error)`.

- [ ] **Step 1: Write failing API/client tests**

```go
func TestNodeReleasesDefaultsToLatestPerMajor(t *testing.T)
func TestNodeReleasesMajorAllAndRefresh(t *testing.T)
func TestNodeReleasePreflightReturnsChecksumAndSize(t *testing.T)
func TestNodeReleaseEndpointsRequireAuth(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/api ./internal/client -run 'TestNodeRelease' -v`
Expected: FAIL because the routes/methods are missing.

- [ ] **Step 3: Implement handlers and client methods**

Parse `major`, `all`, `refresh`, and `offline` query flags. Return typed errors for offline/preflight failures. Add OpenAPI paths and schemas. Do not expose cache paths or raw fetch errors containing URLs with credentials (there are none, but keep messages sanitized).

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/api ./internal/client -run 'TestNodeRelease' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api internal/client api/openapi.yaml
git commit -m "feat: expose node release discovery and preflight api"
```

---

### Task 7: CLI runtime discovery and selector installs

**Files:**
- Modify: `internal/cli/runtime.go`
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: Task 6 client.
- Produces: `runtime available [--kind node] [--major N] [--all] [--refresh] [--offline]`; selector-aware `runtime install`.

- [ ] **Step 1: Write failing CLI tests**

```go
func TestRuntimeAvailableShowsLatestNodePerMajor(t *testing.T)
func TestRuntimeAvailableMajorAllShowsOlderReleases(t *testing.T)
func TestRuntimeInstallNodeSelectorUsesPreflight(t *testing.T)
func TestRuntimeInstallNodeSelectorRejectsUnknown(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/cli -run 'TestRuntimeAvailableNode|TestRuntimeInstallNodeSelector' -v`
Expected: FAIL because the flags/commands are missing.

- [ ] **Step 3: Implement the CLI**

Text output groups Node majors and prints PHP entries separately. `--all` expands a major. Install confirmation shows the preflight version, npm, size, and checksum prefix. Keep existing exact embedded IDs working.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/cli -run 'TestRuntimeAvailableNode|TestRuntimeInstallNodeSelector' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat: add node release discovery to runtime cli"
```

---

### Task 8: Global Node default resolution and shims

**Files:**
- Create: `internal/tooling/node.go`
- Create: `internal/tooling/node_test.go`
- Modify: `internal/tooling/windows.go`
- Modify: `internal/tooling/windows_test.go`
- Modify: `internal/config/config.go` (only if a new path helper is needed)

**Interfaces:**
- Produces: `DefaultNodeSettingKey`, `NodeDefaultStatus`, `ResolveNodeDefault(ctx, *store.Store) (model.RuntimeBuild, error)`, `EnsureNodeShims(paths, build) error`, `WindowsNodeShimDir(paths) string`.
- Status fields: `Configured`, `Build *model.RuntimeBuild`, `ShimDir`, `ShimInstalled`, `Source`.

- [ ] **Step 1: Write failing tooling tests**

```go
func TestResolveNodeDefaultUsesExplicitThenNewest(t *testing.T)
func TestResolveNodeDefaultRejectsUninstalledBuild(t *testing.T)
func TestEnsureNodeShimsWritesNodeNpmNpx(t *testing.T)
func TestWindowsNodeShimUsesResolvedExactBuild(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/tooling -run 'TestResolveNodeDefault|TestEnsureNodeShims|TestWindowsNodeShim' -v`
Expected: FAIL because the Node default functions are missing.

- [ ] **Step 3: Implement resolution and shims**

Mirror `php.go`: exact setting or newest compatible installed build; no download. WSL shims point at the exact `bin` directory for `node`, `npm`, `npx`. Windows `.cmd` shims call the managed distro with the exact build path. Emit `DEVSTACK_NODE_VERSION` and `DEVSTACK_NODE_SOURCE`.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/tooling -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tooling
git commit -m "feat: add global node default resolution and shims"
```

---

### Task 9: Helper-managed Linux Node wrappers

**Files:**
- Modify: `internal/helperproto/protocol.go`
- Modify: `internal/helper/tool.go`
- Modify: `internal/helper/tool_test.go`
- Modify: `internal/daemon/toolbin.go`
- Modify: `internal/daemon/toolbin_test.go`
- Modify: `internal/daemon/windows.go`

**Interfaces:**
- Consumes: Task 8 global default resolution.
- Produces: `helperproto.ToolNodeDefault`; helper install/remove/status for `node`, `npm`, `npx` wrappers with the same target validation as `php-default`.
- Windows daemon manages `node.cmd`, `npm.cmd`, `npx.cmd` in the public shim directory.

- [ ] **Step 1: Write failing tests**

```go
func TestHelperInstallsNodeDefaultWrappers(t *testing.T)
func TestHelperNodeDefaultRejectsEscapingTarget(t *testing.T)
func TestWindowsToolServiceManagesNodeShims(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/helper ./internal/daemon -run 'NodeDefault|NodeShims' -v`
Expected: FAIL because the tool id and wrappers are missing.

- [ ] **Step 3: Implement the tool surface**

Resolve the target only from the registered Node default under `<home>/runtimes`, require the exact build directory shape, write wrappers atomically in `/usr/local/bin` (or configured tool bin), and remove only wrappers owned by devstack. Reuse the PHP conflict/backup/status behavior. Windows uses `.cmd` shims and the same conflict rules.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/helper ./internal/daemon -run 'NodeDefault|NodeShims' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/helperproto internal/helper internal/daemon
git commit -m "feat: install system-wide node default wrappers"
```

---

### Task 10: Node default API and CLI

**Files:**
- Modify: `internal/api/handlers_tooling.go`
- Modify: `internal/api/server.go`
- Modify: `internal/api/handlers_tooling_test.go`
- Modify: `internal/client/client.go`
- Modify: `internal/client/client_test.go`
- Create: `internal/cli/node.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `api/openapi.yaml`

**Interfaces:**
- Consumes: Task 8 tooling core and Task 9 helper wrappers.
- Produces: `GET/PUT /api/v1/node/default`; `NodeDefault`, `SetNodeDefault`, `ClearNodeDefault`.
- CLI: `devstack node default [<version>|--status|--clear]`; JSON output matches `NodeDefaultStatus`.

- [ ] **Step 1: Write failing tests**

```go
func TestNodeDefaultAPIStatusSetClear(t *testing.T)
func TestNodeDefaultCLITableAndJSON(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/api ./internal/client ./internal/cli -run 'TestNodeDefault' -v`
Expected: FAIL because the endpoints/command are missing.

- [ ] **Step 3: Implement the API and CLI**

Return the resolved build, configured value, shim directory/status, and source. Set validates an installed build; clear removes the setting and re-resolves the newest installed fallback. Update OpenAPI and error remediation.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/api ./internal/client ./internal/cli -run 'TestNodeDefault' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api internal/client internal/cli api/openapi.yaml
git commit -m "feat: expose node default api and cli"
```

---

### Task 11: Site toolchain resolution, update API, and terminal inheritance

**Files:**
- Modify: `internal/model/model.go`
- Modify: `internal/sites/service.go`
- Modify: `internal/sites/php_apply.go`
- Modify: `internal/api/handlers_sites.go`
- Modify: `internal/api/server.go`
- Modify: `internal/client/client.go`
- Modify: `internal/client/client_test.go`
- Modify: `internal/agentproto/protocol.go`
- Modify: `internal/agent/terminal.go`
- Modify: `internal/agent/runtime.go`
- Modify: `internal/tooling/siteenv.go`
- Modify: `internal/tooling/windows.go`
- Modify: `internal/tooling/siteenv_test.go`
- Modify: `internal/tooling/windows_test.go`
- Create: `internal/sites/node_toolchain_test.go`
- Modify: `internal/api/api_test.go`
- Modify: `internal/agent/terminal_test.go`

**Interfaces:**
- Consumes: Task 8 global default resolution.
- Produces: `model.NodeToolchain{Version, BuildID, Source, Installed}`; `(*sites.Service) ResolveNodeToolchain(ctx, model.Site) (model.NodeToolchain, error)`.
- `UpdateRequest.NodeVersion *string`; API PATCH accepts `node_version` and `null`.
- `TerminalTicketRequest.NodeVersion string`; agent ticket uses it.

- [ ] **Step 1: Write failing tests**

```go
func TestResolveNodeToolchainPrefersSiteThenGlobal(t *testing.T)
func TestStaticBuildProfileDoesNotOverrideShellNode(t *testing.T)
func TestBuildSiteEnvUsesSitePinThenGlobal(t *testing.T)
func TestWindowsSiteEnvironmentUsesExactResolvedBuild(t *testing.T)
func TestSiteUpdateRequiresExplicitNodeVersion(t *testing.T)
func TestStaticSiteCanClearNodeVersionToInherit(t *testing.T)
func TestTerminalTicketUsesDaemonResolvedNodeVersion(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/sites ./internal/api ./internal/agent ./internal/tooling -run 'NodeToolchain|NodeVersion|TerminalTicket|BuildSiteEnv|WindowsSiteEnvironment' -v`
Expected: FAIL because the resolver, pointer field, and ticket field are missing.

- [ ] **Step 3: Implement resolution and updates**

`ResolveNodeToolchain` validates exact installed builds, resolves an empty static/PHP value through the global default, and returns a clear error when neither exists. `backendSpec` receives the resolved exact version. `BuildSiteEnv` uses the site pin then the global default; `WindowsSiteEnvironment` receives the same exact resolved version, never a raw line. Node kind rejects clearing; static/PHP accept it. Terminal tickets carry the resolved exact version; the agent prefers it and falls back to the spec. Populate the site API `node_toolchain` summary. Update the commit's `git add` to include `internal/tooling`.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/sites ./internal/api ./internal/agent ./internal/tooling -run 'NodeToolchain|NodeVersion|TerminalTicket|BuildSiteEnv|WindowsSiteEnvironment' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model internal/sites internal/api internal/client internal/agentproto internal/agent internal/tooling
git commit -m "feat: resolve per-site node toolchains for all site kinds"
```

---

### Task 12: CLI site Node editing and all-kind creation

**Files:**
- Modify: `internal/cli/site.go`
- Modify: `internal/cli/tooling.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: Task 11 API.
- Produces: `devstack site node <name> <version>` and `--clear`; `site add --node` for static/PHP.

- [ ] **Step 1: Write failing tests**

```go
func TestSiteNodeSetsExactInstalledVersion(t *testing.T)
func TestSiteNodeClearInheritsGlobal(t *testing.T)
func TestSiteNodeClearRejectedForNodeKind(t *testing.T)
func TestSiteAddAcceptsNodeForStaticAndPHP(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/cli -run 'TestSiteNode|TestSiteAddAcceptsNode' -v`
Expected: FAIL because the command and validation are missing.

- [ ] **Step 3: Implement the CLI**

`site node` resolves against installed builds; if absent, it offers the discovered match and installs after confirmation. `--clear` maps to `null` in PATCH. `site add` passes the Node pin for every kind; Node kind still requires it.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `go test ./internal/cli -run 'TestSiteNode|TestSiteAddAcceptsNode' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat: edit node pins for every site kind from the cli"
```

---

### Task 13: Dashboard Node picker and site screens

**Files:**
- Create: `ui/src/lib/components/NodeVersionPicker.svelte`
- Create: `ui/src/lib/components/NodeVersionPicker.test.ts`
- Modify: `ui/src/routes/AddSite.svelte`
- Modify: `ui/src/routes/SiteDetail.svelte`
- Modify: `ui/src/lib/api.ts`
- Modify: `ui/src/lib/mock.ts`
- Modify: `ui/src/lib/types.ts`
- Modify: `ui/src/routes/SiteDetail.test.ts`
- Modify: `ui/src/routes/AddSite.test.ts`

**Interfaces:**
- Consumes: Task 11 site fields/API; Task 6 release API.
- Produces: shared picker supporting installed builds, inherit mode, and install-first.

- [ ] **Step 1: Write failing UI tests**

```ts
it('defaults static sites to inherit and shows the global version')
it('requires an explicit version for node sites')
it('expands a major to reveal older minor and patch versions')
it('offers install-first when the selected version is not installed')
it('shows the resolved toolchain source on site detail')
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `npm --prefix ui test -- --run src/lib/components/NodeVersionPicker.test.ts src/routes/AddSite.test.ts src/routes/SiteDetail.test.ts`
Expected: FAIL because the component and fields are missing.

- [ ] **Step 3: Implement the picker and screens**

Add `node_version` and `node_toolchain` to types/mock/API. Picker groups installed builds by major, supports `inheritLabel`, and exposes `oninstall` for missing versions. Add Site Detail editing with the ABI/rebuild warning; clear is hidden/rejected for Node kind.

- [ ] **Step 4: Run the tests and verify they pass**

Run: `npm --prefix ui test -- --run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui
git commit -m "feat: add per-site node picker to the dashboard"
```

---

### Task 14: Dashboard Runtimes and Settings

**Files:**
- Modify: `ui/src/routes/Runtimes.svelte`
- Modify: `ui/src/routes/Runtimes.test.ts`
- Modify: `ui/src/routes/Settings.svelte`
- Modify: `ui/src/routes/Settings.test.ts`
- Modify: `ui/src/lib/api.ts`
- Modify: `ui/src/lib/mock.ts`
- Modify: `ui/src/lib/types.ts`

**Interfaces:**
- Consumes: Task 6 discovery API; Task 10 default API.
- Produces: major-grouped Runtimes UI with disclosure/refresh/preflight; Node default card.

- [ ] **Step 1: Write failing UI tests**

```ts
it('shows only the newest patch per major by default')
it('expands a major to show every stable release')
it('lazily loads checksum and size in the install dialog')
it('shows stale/offline refresh state')
it('saves and clears the node cli default')
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `npm --prefix ui test -- --run src/routes/Runtimes.test.ts src/routes/Settings.test.ts`
Expected: FAIL because the controls and API methods are missing.

- [ ] **Step 3: Implement the screens**

Runtimes: major rows, LTS/security/installed badges, disclosure, refresh, install preflight. Settings: Node CLI default card parallel to PHP, with status, resolved build, shim state, Save/Clear, install-more link. Keep loading/empty/error states.

- [ ] **Step 4: Run the UI checks**

Run: `npm --prefix ui test -- --run`
Run: `npm --prefix ui run check`
Run: `npm --prefix ui run build`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add ui
git commit -m "feat: add node discovery and default settings to the dashboard"
```

---

### Task 15: In-use checks, migration, OpenAPI, docs, and E2E

**Files:**
- Modify: `internal/sites/php_apply.go`
- Modify: `internal/sites/php_apply_test.go`
- Modify: `internal/runtime/manager.go` (startup normalization hook if needed)
- Create: `internal/sites/node_migration_test.go`
- Modify: `api/openapi.yaml`
- Modify: `docs/README.md`
- Modify: `docs/sites.md`
- Modify: `docs/architecture.md`
- Modify: `docs/decisions.md`
- Modify: `docs/development.md`
- Modify: `docs/superpowers/audits/2026-10-29-e2e-report.md` (new E2E section)
- Modify: `docs/superpowers/status/2026-10-30-node-toolchain-handoff.md`

**Interfaces:**
- Produces: kind-aware `RuntimeInUseError` covering site pins, build pins, and `node_default`; exact normalization of line-valued site/build pins; final docs/OpenAPI.

- [ ] **Step 1: Write failing tests**

```go
func TestCheckRuntimeRemovalBlocksNodeBuildForEverySiteKind(t *testing.T)
func TestCheckRuntimeRemovalBlocksGlobalNodeDefault(t *testing.T)
func TestNormalizeNodeVersionsStoresExactInstalledBuilds(t *testing.T)
func TestNormalizeNodeVersionsLeavesUnresolvedValuesVisible(t *testing.T)
```

- [ ] **Step 2: Run the tests and verify they fail**

Run: `go test ./internal/sites -run 'TestCheckRuntimeRemovalBlocksNode|TestNormalizeNodeVersions' -v`
Expected: FAIL because all-kind/default checks and normalization are missing.

- [ ] **Step 3: Implement checks, migration, and docs**

Block removal with the owning site names or `global default`; do not block on an absent row. Normalize only when an installed exact build exists. Update OpenAPI and user/architecture/decision docs; mark C8 amended and C19/C20 implemented. Record the new E2E evidence.

- [ ] **Step 4: Run the full verification matrix**

Run: `gofmt -l .`
Run: `go build ./...`
Run: `go vet ./...`
Run: `go test ./...`
Run as an unprivileged WSL user: `go test -race -p 1 ./...`
Run: `npm --prefix ui test -- --run`
Run: `npm --prefix ui run check`
Run: `npm --prefix ui run build`
Run: `pwsh -NoProfile -File test/windows/installer_test.ps1`
Run: `pwsh -NoProfile -File test/windows/lifecycle_test.ps1`
Run: `pwsh -NoProfile -File test/release/release_windows_ps_test.ps1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal api docs
git commit -m "feat: finish node toolchain migration and documentation"
```
