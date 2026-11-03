# Windows Docker nginx/Apache serving

## Intent

Make `serving_mode: nginx|apache` work on Windows by using the existing managed
Docker web-server containers, not native Windows packages.

The Windows daemon remains the only public entry point on 80/443 and the only
owner of public TLS. Docker nginx/Apache serve HTTP on WSL loopback port 18080;
the daemon routes every non-dashboard site host to that private endpoint.
Selecting nginx/Apache in Settings or `devstack vhost use` activates the
corresponding Docker service. Selecting builtin stops it and restores direct
daemon routing.

## Why this is needed

- Native nginx/Apache is Linux-only: the Windows helper has no vhost RPCs.
- `internal/dockerweb/windows_provider.go` implements the intended private-HTTP
  switch, but `NewWindowsProvider` is constructed only in tests.
- `internal/agentweb` implements a minimal vhost renderer that does not support
  front controllers, `.htaccess`, SPA fallbacks, stopped-site 503 pages, or the
  full deny rules. Its Apache branch also writes the WSL path as
  `DocumentRoot` instead of the container's `/sites/...` mount.
- As a result, an active Apache container serves only its default page and the
  daemon keeps serving sites through the builtin proxy.

## Approach

Complete the existing Windows provider and make the public control surfaces use
it. Reuse the full `dockerweb` renderer for private vhosts instead of extending
the stub renderer.

### 1. Private vhost rendering

Add an exported private renderer in `internal/dockerweb`:

- Render one site for HTTP on port 18080.
- Never render a TLS listener, redirect-to-HTTPS vhost, or dashboard vhost.
- Reuse the existing nginx/Apache site bodies so front controller, `.htaccess`,
  SPA fallback, source-backup denies, dotfile denies, and forwarded headers keep
  parity with Linux.
- Map document roots to `/sites/<relative-to-projects>` for both nginx and
  Apache; keep the WSL path for FastCGI `SCRIPT_FILENAME`.
- Render a 503 vhost for a stopped site.
- Return a clear error when a site requests Apache-only
  `front_controller_mode=htaccess` while nginx is active.

Replace the minimal renderer in `internal/agentweb` with this renderer. The agent
receives the active server name plus typed site targets and writes only that
server's generated tree. Stale generated `.conf` files are removed after a
successful sync.

### 2. Typed targets

Extend `agentproto.WebTarget` with the rendering fields already present on
`model.Site`:

- `www`
- `document_root` (WSL path)
- `front_controller`, `front_controller_file`, `front_controller_mode`
- `spa_fallback`, `spa_fallback_file`
- `apache_allow_override`
- `desired_state`

Add `server` to `agentproto.WebTargetsRequest`. `services.AgentTargetSource`
populates the new fields from the site row. The agent validates all paths and
domain names before writing anything.

### 3. Windows provider wiring

In `internal/daemon/windows.go`:

- Create `WebServiceControl` and `WebSwitch` adapters around the service manager.
- Build a `WindowsProvider` with:
  - the agent-backed private syncer,
  - the service control adapter,
  - the provider-switch adapter (bypasses the active-server deactivation guard),
  - the public proxy router,
  - private host `127.0.0.1`, port `18080`.
- Register `WebPreparer` for nginx and Apache, overriding the plain agent Docker
  preparer for those two IDs.
- Set `siteService` dependencies:
  - `DockerSyncer` = agent-backed syncer,
  - `DockerSwitch` = Windows provider,
  - `DockerRoute` = public router plus private host/port.
- Expose an activator to `sites.Service` that maps a serving mode to
  `serviceManager.Activate(nginx|apache)`.

`WindowsProvider` gets a `Switch` dependency for stop/start. Its
`StopActiveServer` and `StartActiveServer` must use the provider-switch path, not
the guarded `Deactivate`, or switching back to builtin would be refused.

### 4. Serving-mode behavior

`internal/sites`:

- Allow `SaveServingState(mode, "docker")` on Windows. The existing Windows
  guard only applies to native external modes.
- In `SetServingMode` on Windows:
  - `builtin` keeps the existing Docker-to-builtin path.
  - `nginx`/`apache` calls the injected activator when the current provider is
    native/builtin.
  - the same Docker server is idempotent: sync vhosts and return.
  - a different Docker server while one is active switches automatically: stop
    the old container, reset the saved state, activate the target, and restart
    the old container if activation fails.
  - a site-driven vhost sync validates and gracefully reloads the running
    container, so newly added sites do not wait for a container restart.
  - deactivating or uninstalling the active Docker web server first switches
    serving back to `builtin`; the service operation then proceeds instead of
    being refused.
  - the dashboard disables nginx/Apache mode options whose service is not
    installed and keeps them enabled when the service list cannot be read.
- `ReconcileServingMode` for Docker on Windows must not disable the helper
  forwarders or stop the public proxy. It syncs the private targets and keeps the
  daemon public entry alive.
- `rebuildRoutesLocked` re-applies the 18080 redirect after every route rebuild
  when Docker is active and the syncer reports a healthy config. During the
  builtin switch it suppresses that redirect so the restored direct routes win.
- `VhostStatus` reports the Docker mode, last sync, and config health through
  `DockerSyncer`.

Settings keeps using `setServingMode`; no API contract change is needed. On
Windows the mode hints say "Docker container" instead of "Native vhosts". The CLI
`devstack vhost use nginx|apache` therefore works through the same activator.

### 5. Activation and rollback

Activation flow:

1. Settings or CLI calls `SetServingMode(nginx|apache)`.
2. The Windows activator calls `serviceManager.Activate`.
3. `WebPreparer` calls `WindowsProvider.Prepare`.
4. The provider sends typed targets to the WSL agent; the agent renders private
   vhosts and base config.
5. The helper starts the container.
6. `WindowsProvider.Commit` validates, reloads, points the public router at
   `127.0.0.1:18080`, and saves `mode/docker`.
7. `devstack vhost status` reports the Docker mode and sync health.

Any failure rolls back the container, router, and saved state. Switching back to
builtin stops the container through `WebSwitch`, restores the router snapshot,
rebuilds direct routes with the redirect suppressed, saves `builtin/native`, and
keeps the public proxy running throughout.

## Non-goals

- Native nginx/Apache packages on Windows or in WSL.
- Switching directly between nginx and Apache without passing through builtin.
- Docker-served sites outside the managed WSL projects root.
- Changing the public 80/443 ownership model or public TLS.
- Performance host tuning. That is a separate Windows/WSL tuning design.

## Constraints

- The service manager mutex is non-reentrant. Provider callbacks must receive the
  transition context from the manager, and the `sites` switch path must not hold
  the sites mutex while activating a service.
- The public proxy and helper forwarders stay alive in both modes.
- Router snapshots are the rollback source of truth; every redirect update must
  preserve the dashboard route.
- The WSL agent remains the only writer of WSL service data and vhosts.

## Acceptance

- `devstack vhost use apache --yes` on Windows activates Docker Apache, renders
  full vhosts, and routes site traffic through it.
- A Laravel PHP site returns 200 through `https://<site>.test` with Apache
  response headers, front-controller routes, and `.htaccess` behavior.
- A static and a Node site work through the same Apache container.
- `devstack vhost use nginx --yes` works after switching back to builtin; nginx
  serves the same sites without public TLS or dashboard vhosts in the container.
- `devstack vhost use builtin --yes` stops the active container and restores
  direct routing without downtime beyond the switch.
- `devstack vhost status` and Settings show the active Docker server, last sync,
  and config health.
- `devstack doctor` is green in both modes.
- Unit tests cover private rendering, path mapping, target validation, provider
  switch ordering, state guards, route redirect/reconciliation, and CLI/API
  behavior.
