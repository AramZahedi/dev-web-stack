# Windows Docker nginx/Apache Serving Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make nginx and Apache first-class serving modes on Windows through the managed Docker containers, with full vhost parity and automatic default activation when a web server is already active.

**Architecture:** The Windows daemon keeps public 80/443/TLS and routes site hosts to the WSL Docker web server on `127.0.0.1:18080`. A Windows provider switch activates the Docker service, an agent-backed syncer renders full private vhosts, and `sites.Service` delegates Windows external mode changes to the service manager.

**Tech Stack:** Go, Svelte, Docker, WSL agent, Windows named-pipe helper.

**Spec:** `docs/superpowers/specs/2026-11-03-windows-docker-serving-design.md`

## Global Constraints

- Public 80/443 and public TLS remain owned by the Windows daemon.
- Docker nginx/Apache listen on WSL loopback `18080` only.
- The dashboard `devstack.test` route is never rendered into a container vhost.
- No native nginx/Apache packages on Windows.
- Direct nginx ↔ Apache switches require builtin in between.
- Every generated vhost must use `/sites/<relative>` for container document roots and the WSL path for FastCGI `SCRIPT_FILENAME`.
- Existing Linux behavior must not change.

## Review Focus

- A Laravel route that is not a physical file must reach `public/index.php` through both Apache and nginx.
- A project `.htaccess` must be honored by Apache according to `apache_allow_override`.
- A stopped site must return 503, not a stale vhost.
- A deleted site must not leave a generated vhost behind.
- Switching back to builtin must restore direct routes even when a site is added during the switch.

---

### Task 1: Full private vhost renderer

**Files:**
- Modify: `internal/dockerweb/render.go`
- Test: `internal/dockerweb/render_test.go`

**Interfaces:**
- Produces: `func RenderPrivateSite(server Server, projects string, s SiteSpec, maxBody int64) ([]byte, error)`
- Produces: `const PrivateHTTPPort = 18080`
- Produces: `func (s SiteSpec) effectivePrivate() SiteSpec` (internal helper)

- [ ] **Step 1: Write failing tests** for nginx and Apache private PHP/static/node vhosts. Assert:
  - `listen 18080` / `<VirtualHost *:18080>`.
  - Apache `DocumentRoot "/sites/<rel>"` and `<Directory "/sites/<rel>">`.
  - nginx `root /sites/<rel>;`.
  - `SCRIPT_FILENAME` uses the WSL host path.
  - No `listen 443`, `ssl_certificate`, or `devstack.test`.
  - Front controller `FallbackResource` for Apache and `@devstack_front_controller` for nginx.
  - `.htaccess` policy appears as `AllowOverride all|fileinfo|none`.
  - Stopped site returns 503.
- [ ] **Step 2: Run tests; expect missing function failures.**
- [ ] **Step 3: Implement `PrivateHTTPPort` and `RenderPrivateSite`** by validating the site, deriving the container root, forcing `TLS=false`/`ForceHTTPS=false`, and calling new private nginx/Apache body helpers that render only the serving vhost (no TLS/redirect/dashboard).
- [ ] **Step 4: Run `go test ./internal/dockerweb -count=1`; expect PASS.**
- [ ] **Step 5: Commit** `feat(dockerweb): render full private html vhosts`.

### Task 2: Typed private web targets

**Files:**
- Modify: `internal/agentproto/protocol.go`
- Modify: `internal/services/agent_targets.go`
- Test: `internal/services/agent_targets_test.go`

**Interfaces:**
- `WebTarget` adds `WWW bool`, `HostDocumentRoot string`, `FrontController bool`, `FrontControllerFile string`, `FrontControllerMode string`, `SPAFallback bool`, `SPAFallbackFile string`, `ApacheAllowOverride string`, `DesiredState string`.
- `WebTargetsRequest` adds `Server string`.
- `AgentTargetSource.PrivateWebTargets(ctx, server)` populates all fields from `model.Site`.

- [ ] **Step 1: Write failing tests** that assert a PHP site target carries the document root, front-controller fields, Apache override, desired state, and `www` alias; assert `Server` is set.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Add the fields and populate them.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(agentproto): carry full site vhost metadata`.

### Task 3: Agent private renderer

**Files:**
- Modify: `internal/agentweb/web_targets.go`
- Modify: `internal/agentweb/module.go`
- Test: `internal/agentweb/web_targets_test.go`

**Interfaces:**
- `Sync` reads `request.Server`, rejects empty/unsupported servers, and writes only that server’s `conf.d/generated`.
- Uses `dockerweb.RenderPrivateSite`.
- Removes generated `.conf` files not present in the target set.
- Rejects duplicate IDs and unsafe paths before writing.

- [ ] **Step 1: Write failing tests** for server-specific sync, Apache `/sites` paths, front-controller output, stale removal, and rejection of `htaccess` mode on nginx.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Replace the stub renderer with `dockerweb.RenderPrivateSite`; implement server validation and stale removal.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(agentweb): render full private vhosts`.

### Task 4: Windows agent syncer and status

**Files:**
- Modify: `internal/dockerweb/windows_provider.go`
- Test: `internal/dockerweb/windows_provider_test.go`

**Interfaces:**
- `AgentWindowsPrivateSyncer` implements `EnsurePrivateBase`, `SyncPrivate`, `Sync(ctx, server) error`, and `DockerStatus() (time.Time, bool, string)`.
- Sync sends `WebTargetsRequest{Server: server, Targets: ...}`.
- Nil agent or target errors set `config_ok=false` and `last_error`.

- [ ] **Step 1: Write failing tests** for successful sync/status, agent error/status, and `Sync` server propagation.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Add status fields and methods.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(dockerweb): track Windows private sync status`.

### Task 5: Windows provider switch

**Files:**
- Modify: `internal/dockerweb/windows_provider.go`
- Test: `internal/dockerweb/windows_provider_test.go`

**Interfaces:**
- `WindowsProviderConfig` adds `Switch ProviderSwitch`.
- `ProviderSwitch` has `StopActiveServer(ctx, server string) error` and `StartActiveServer(ctx, server string) error`.
- `StopActiveServer`/`StartActiveServer` use `Switch` when set; otherwise fall back to `Services`.
- `Prepare` rejects direct switching from nginx to Apache while Docker is active.

- [ ] **Step 1: Write failing tests** that assert a builtin → Apache commit routes to 18080, builtin switch uses the switch path, and direct nginx → Apache is rejected.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(dockerweb): wire Windows provider switch`.

### Task 6: `sites` Windows serving delegation

**Files:**
- Modify: `internal/sites/service.go`
- Modify: `internal/sites/vhosts.go`
- Modify: `internal/sites/routes.go`
- Test: `internal/sites/vhosts_windows_test.go`
- Test: `internal/sites/docker_provider_test.go`

**Interfaces:**
- `Deps` adds `DockerService DockerServiceActivator`, `DockerRoute DockerRoute`, `DockerRouteHost string`, `DockerRoutePort int`.
- `DockerServiceActivator` has `ActivateServingMode(ctx, mode string) error`.
- `DockerRoute` has `UpdateDockerWebRoutes(ctx, active bool, host string, port int) error`.
- `SetServingMode` on Windows external modes delegates before taking `s.mu`.
- `SaveServingState` permits external when `provider == "docker"`.
- `ReconcileServingMode` does not disable forwarders for Windows Docker.
- `rebuildRoutesLocked` redirects to the private route when Docker is healthy; suppresses during builtin switch.

- [ ] **Step 1: Write failing tests** for delegation, same-mode idempotence, direct nginx ↔ Apache rejection, docker state save, and route redirect.
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(sites): delegate Windows Docker serving`.

### Task 7: Daemon wiring and default activation

**Files:**
- Modify: `internal/daemon/windows.go`
- Test: `internal/daemon/windows_test.go`

**Interfaces:**
- Build `webControl`, `webSwitch`, `agentSyncer`, `windowsProvider`, and a `windowsDockerActivator`.
- Override the nginx/apache preparers with `WebPreparer` after `siteService` exists.
- Set `DockerSyncer`, `DockerSwitch`, `DockerRoute`, `DockerRouteHost=127.0.0.1`, `DockerRoutePort=18080`.
- After bootstrap, if `serving_mode=builtin` and exactly one of nginx/apache is active, activate it automatically.

- [ ] **Step 1: Add a pure `windowsWebWiring` helper test** that asserts the provider, activator, and route values are wired.
- [ ] **Step 2: Run test; expect failure.**
- [ ] **Step 3: Implement the wiring.**
- [ ] **Step 4: Run `go test ./internal/daemon -count=1`; expect PASS.**
- [ ] **Step 5: Commit** `feat(daemon): enable Windows Docker web serving`.

### Task 8: Settings and CLI presentation

**Files:**
- Modify: `ui/src/routes/Settings.svelte`
- Modify: `ui/src/routes/Settings.test.ts`
- Modify: `internal/cli/vhost.go`
- Test: `internal/cli/vhost_test.go`

**Interfaces:**
- Windows mode hints say Docker container, not native vhosts.
- `devstack vhost use nginx|apache` succeeds through the daemon when the service is installed; service-not-installed errors name the Services action.
- Native Linux behavior remains unchanged.

- [ ] **Step 1: Write failing UI and CLI tests.**
- [ ] **Step 2: Run tests; expect failures.**
- [ ] **Step 3: Implement hints and error mapping.**
- [ ] **Step 4: Run tests; expect PASS.**
- [ ] **Step 5: Commit** `feat(ui): present Windows Docker serving`.

### Task 9: Live Windows verification

- [ ] **Step 1: Build `devstack.exe` and upgrade the real install.**
- [ ] **Step 2: Activate Apache; verify generated vhosts and `/sites` mount.**
- [ ] **Step 3: Verify `https://laravel-new.test` returns 200 with Apache headers and a front-controller route.**
- [ ] **Step 4: Switch to builtin; verify Apache stops and the site still returns 200.**
- [ ] **Step 5: Install/activate nginx; verify the same site, then switch back to builtin.**
- [ ] **Step 6: Run `devstack doctor`.**
- [ ] **Step 7: Commit any fixes found.**
