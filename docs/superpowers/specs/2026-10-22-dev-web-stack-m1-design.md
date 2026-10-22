# dev-web-stack — M1 Design: Foundation & Sites

- **Status:** Draft for review
- **Date:** 2026-10-22
- **Scope:** Milestone 1 only
- **Target platforms:** systemd-based Linux x86_64 (glibc 2.35+) and Windows 11 x86_64 with WSL2
- **Vhost extension:** `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`

## 1. Summary

`dev-web-stack` is a from-scratch local web development platform for a team, delivered as a Go CLI, a local daemon, an embedded web dashboard, and a narrowly privileged helper service. M1 makes local vhosts work end to end: create a site from a directory, pick a runtime, and reach it at `https://project.test` with a trusted certificate. Supported runtimes are static files, PHP 8.2–8.5 with per-vhost versions, php.ini, and extensions, and Node 22/24 proxied to a dev server.

On Linux, sites run directly on the host. On Windows, a dedicated WSL2 distro runs sites and runtimes while the native Windows app owns the UI, proxy, and OS integration. Teammates install once; after that, vhosts, certificates, and runtimes are managed entirely from the CLI or dashboard.

No Docker or Podman is used. We own the supervisor, proxy, routing, TLS, runtime catalog, and site lifecycle. Optional external serving is available through helper-managed native nginx or Apache vhosts; the built-in proxy remains the default.

## 2. Goals and success criteria

**Goals**

1. Create, start, stop, and delete local sites from CLI and UI.
2. Serve each site at `https://<name>.test` with a locally trusted certificate and no browser warnings.
3. Support static, PHP, and Node runtimes, with multiple PHP versions coexisting.
4. Give each PHP vhost its own version, php.ini, and extension set.
5. Make install, diagnostics, trust repair, and uninstall predictable on Linux and Windows.
6. Keep the privileged surface small and auditable.
7. Offer an optional helper-managed nginx/Apache serving mode with native vhosts, safe mode switching, and rollback.

**Success criteria**

- A new teammate completes install, `devstack init`, and their first trusted site in under 15 minutes.
- Switching a PHP site between 8.2 and 8.5 does not affect other sites.
- Enabling Xdebug on one site does not load it on any other site.
- Browsers and managed Node/PHP processes trust the local CA; OS trust is installed for command-line tools that use the system store, and `devstack trust status` reports the exact state of every target.
- Uninstall reverts every system change without touching project files.

## 3. Non-goals for M1

- Databases (MySQL/Postgres), Redis service, mail catcher, backups (M2).
- Team-shared `devstack.yaml`, lockfiles, one-command onboarding (M3).
- Log aggregation beyond per-site logs, diagnostics bundles, auto-update, signed MSI/deb/rpm packaging (M4).
- macOS, Linux arm64, Windows arm64, Windows 10.
- Custom TLDs other than `.test`.
- Remote or shared environments, tunneling, container-based stacks.
- Multi-user shared workstations; M1 assumes one installing user per machine.
- Composer, WP-CLI, or other per-project tooling installation.
- Per-site domain aliases beyond an optional `www.` alias.

## 4. Decisions

| Area | Decision |
|---|---|
| Architecture | Monolith + execution adapters (reviewed option A) |
| Language | Go, one module, single static binaries where possible |
| UI | Embedded Svelte 5 + TypeScript SPA built with Vite |
| State | SQLite (`modernc.org/sqlite`, pure Go) + YAML machine config |
| Local domains | `<name>.test`, optional `www.<name>.test` |
| Privilege | Small helper service; daemon and UI unprivileged |
| External serving | Optional helper-managed native nginx/Apache vhosts; one active `serving_mode` (`builtin` default); PHP vhosts in Plan 2 |
| Windows | Managed Debian 12 WSL2 distro running a `devstack-agent` service |
| PHP | Our own relocatable builds, PHP 8.2–8.5, shared extensions |
| PHP isolation | One `php-fpm` master per running site |
| Node | Official LTS builds, 22 and 24, proxy to a user-defined dev server command |
| TLS | Local CA, per-vhost leaf certificates, automatic OS/browser trust |
| Proxy ports | Unprivileged `127.0.0.1:8080` (HTTP) and `:8443` (HTTPS); helper forwards 80/443 |
| Authoring | CLI first, dashboard over the same API; spec-driven, TDD |

### Refinements made while writing this spec

1. **CA key custody moved into the helper.** The reviewed design had the root CA key in the daemon's user account. That would let any process running as the same user request an arbitrary root certificate install through the helper, which is a local privilege-escalation path. The helper now generates and holds the CA key root-only and signs CSRs for `.test` names. The daemon never sees the key.
2. **TLS is provisioned for every site, not optional.** Every site gets a trusted certificate. The per-site TLS toggle only decides whether HTTPS serves content or redirects to HTTP (`tls.enabled`), and a separate `force_https` toggle controls HTTP→HTTPS redirects. This guarantees a user never lands on a certificate warning page by accident. HSTS is never sent.
3. **The helper RPC surface is larger than the four calls described during design review.** CA generation/signing, trust install/remove, forwarding, and uninstall moved into the helper because they are inherently privileged. The guarantee is unchanged: no arbitrary commands, paths, certificates, or ports.

## 5. Architecture

### 5.1 Processes

```
┌──────────────┐   Unix socket / named pipe   ┌─────────────────────────────┐
│ devstack CLI │ ───────────────────────────► │ devstackd (unprivileged)    │
└──────────────┘                              │ API · UI · SQLite · proxy    │
        │  browser                            │ supervisor · catalog         │
        └──────────► https://devstack.test (80/443 → proxy)   └──────────┬──────────────────┘
                                                          │ Backend interface
                          ┌───────────────────────────────┴─────────────┐
                          │ localBackend (Linux)                        │
                          │ wslBackend → devstack-agent inside WSL2      │
                          └──────────────────────────────────────────────┘

┌──────────────────────────────┐
│ devstack-helper (root/SYSTEM)│  hosts block · 80→8080 / 443→8443
│ narrow authenticated RPC     │  CA generation/signing · trust stores
└──────────────────────────────┘
```

**`devstack` (CLI)** — user-facing command. Talks to the daemon over a local socket. Starts the daemon if it is not running. Supports human and `--json` output.

**`devstackd`** (run as `devstack daemon`) — unprivileged user process. Owns the REST API, embedded UI, SQLite state, reverse proxy, supervisor, runtime catalog/downloader, certificate store, and logs. Runs as a systemd user unit on Linux and a per-user startup process on Windows. Never runs as root/SYSTEM.

**`devstack-helper`** — root/SYSTEM service. Implements exactly these RPCs:

| RPC | Purpose |
|---|---|
| `Status` | Versions, listener state, CA fingerprint, last hosts sync |
| `EnsureCA` | Create the local root CA if absent; return its certificate and fingerprint |
| `SignCertificate` | Sign a CSR whose SANs are all within an allowed `.test` suffix |
| `InstallTrust` / `RemoveTrust` | Install/remove the helper's own root CA in OS and browser stores |
| `SyncHosts` | Atomically replace the managed hosts block with a validated list |
| `SetForwarders` | Enable/disable the fixed 80/443 listeners, forwarding to the configured loopback proxy ports (targets validated, loopback-only) |
| `VhostStatus` / `SyncVhosts` / `SetServingMode` / `RemoveVhosts` | Report, install, switch, and remove helper-managed native nginx/Apache vhosts (validated `VHostSpec` only) |
| `Uninstall` | Reverse all system changes after a confirmation token |

The helper accepts no file paths, commands, or arbitrary certificates. It renders vhosts only from validated site data and runs only fixed config-test/reload commands. It verifies peer credentials: the installing UID on Linux (`SO_PEERCRED`) and the installing SID on Windows (named-pipe client token). It listens on `/run/devstack-helper/helper.sock` (Linux) and `\\.\pipe\devstack-helper` (Windows). Sockets are `0600` and owned by that user; the Windows pipe ACL grants only that SID plus SYSTEM/Administrators.

**`devstack-agent`** — Linux binary inside the WSL distro, run as a systemd service. Exposes the backend API on the distro's NAT interface with a per-install bearer token. Owns process supervision, PHP masters, site processes, log capture, and port allocation inside the distro. The Windows daemon reaches it through WSL localhost forwarding, falling back to the distro IP if forwarding is unhealthy. M1 requires WSL NAT networking (not mirrored); the agent is never reachable from outside the WSL NAT.

**External server (optional)** — when `serving_mode` is `nginx` or `apache`, the helper installs native vhosts under `/etc/devstack/vhosts`, copies site certificates into `/etc/devstack/tls`, tests the config, and reloads the server. The external server owns 80/443; the built-in proxy listeners and 80/443 forwarders are inactive in this mode. Static sites are served directly by the external server; Node (and later PHP) traffic goes to supervised loopback backends.

**Site servers** — every running site has a backend endpoint:

- static: an internal `devstack serve-static` process bound to a backend-local port
- PHP: a per-site `php-fpm` master on a backend-local port
- Node: the user's dev server command on an allocated backend-local port

The proxy connects to these endpoints over loopback on Linux or through WSL networking on Windows.

### 5.2 Backend interface

One Go interface with `local` and `wsl` implementations:

```go
type Backend interface {
    Prepare(ctx context.Context, spec SiteSpec) (PreparedSite, error)
    Start(ctx context.Context, id SiteID) (ProcessInfo, error)
    Stop(ctx context.Context, id SiteID, graceful bool) error
    Status(ctx context.Context, id SiteID) (Status, error)
    Logs(ctx context.Context, id SiteID, follow bool) (io.ReadCloser, error)
    Exec(ctx context.Context, id SiteID, argv []string, env map[string]string) (Result, error)
    EnsureRuntime(ctx context.Context, kind RuntimeKind, build BuildID) (RuntimeInfo, error)
    RemoveRuntime(ctx context.Context, build BuildID) error
    InstallCA(ctx context.Context, pem []byte) error
    Health(ctx context.Context) error
}
```

A single conformance test suite runs against both implementations. Interface boundaries must not leak host paths; all paths are expressed in the backend's namespace and translated at the edge.

The interface intentionally leaves room for future remote backends (M4+) without changing the core.

### 5.3 Repository layout

```
cmd/devstack/            CLI + daemon entrypoint
cmd/devstack-helper/     privileged helper
cmd/devstack-agent/      WSL agent + static file server
internal/api/            REST + SSE handlers
internal/proxy/          router, TLS/SNI, FastCGI, WebSocket passthrough
internal/backend/        interface, local, wsl client, conformance helpers
internal/supervisor/     process lifecycle, readiness, backoff, reconciliation
internal/vhost/          native nginx/Apache vhost specs and renderers
internal/php/            builds, FPM config, ini composition, extensions
internal/runtime/        catalog, downloads, checksums, install/remove
internal/trust/          CA client, trust-store adapters, verification
internal/hosts/          hosts block parse/render/validate
internal/store/          SQLite schema, migrations, queries
internal/config/         machine config, paths, env overrides
internal/catalog/        embedded manifests for PHP/Node/extensions
ui/                      Svelte SPA (embedded via go:embed)
build/                   packaging, systemd units, PowerShell, PHP CI
docs/                    user docs and specs
test/                    integration, e2e, fixtures
```

## 6. Data and configuration

**Machine root:** `$DEVSTACK_HOME`, defaulting to `~/.devstack` on Linux and `%USERPROFILE%\.devstack` on Windows. The WSL agent uses `/var/lib/devstack` (root-owned) inside the distro.

```
config.yaml            machine settings (ports, TLD, WSL distro)
state.db               sites, runtimes, certificates metadata, settings
pki/                   per-site keys and certificates
runtimes/              installed PHP and Node builds (Linux backend)
sites/<id>/            generated php.ini, FPM config, runtime metadata
logs/<id>/             rotated site logs
run/                   sockets, pid files
cache/                 verified download cache
```

External serving adds root-owned helper state outside `$DEVSTACK_HOME`:
`/etc/devstack/vhosts/{nginx,apache}/current/*.conf`, `/etc/devstack/tls/<owner>.{crt,key}`,
and one bridge include in the server's standard include directory. The helper records the
mode, release, bridge path, and last error in `/var/lib/devstack-helper/vhosts.json` (0600).

**`config.yaml` defaults:**

```yaml
http_port: 8080
https_port: 8443
ui_port: 7070
wsl_distro: devstack
auto_start_sites: true
graceful_stop_seconds: 10
max_request_body_mb: 1024
log_max_mb: 10
log_keep: 5
serving_mode: builtin   # builtin | nginx | apache
```

Ports are configurable. `devstack doctor` fails with an actionable message when a port is occupied, including the owning process when it can be determined.

**Database:** SQLite with embedded, forward-only migrations. Tables:

- `sites` — id (UUIDv7), name, domain, www_alias, backend, host_path, backend_path, kind, runtime build, command, env JSON, document_root, tls/force_https, status, active ports, timestamps
- `site_php_config` — site id, ini overrides JSON, extension override JSON, last applied config hash
- `php_defaults` — PHP version, default ini directives JSON, default extension map JSON
- `runtimes` — build id, kind, version, backend, install path, checksum, installed_at
- `certs` — site id, serial, domains JSON, not_after, key path, cert path
- `settings` — key/value (helper fingerprint, agent token `0600`, schema version)
- `events` — recent state transitions for diagnostics

Secrets (agent token, helper session) are stored `0600` and never logged.

## 7. Site model and lifecycle

```go
type Site struct {
    ID           string
    Name         string
    Domain       string            // name.test
    WWW          bool
    Backend      BackendKind       // local | wsl
    HostPath     string            // path as the user sees it
    BackendPath  string            // path inside the execution namespace
    Kind         SiteKind          // static | php | node
    PHPVersion   string            // when Kind == php
    PHPExtensions map[string]bool  // overrides; absent = version default
    PHPIni       map[string]string // site overrides
    NodeVersion  string            // when Kind == node
    Command      string            // node start command
    Env          map[string]string
    DocumentRoot string            // relative to project root
    FrontController bool           // php: route non-file requests to index.php
    SPAFallback  bool              // static
    TLS          bool              // serve HTTPS content
    ForceHTTPS   bool
    DesiredState Desired           // running | stopped
}
```

**Create flow**

1. Validate name (`[a-z0-9-]`, no leading/trailing hyphen, 63 chars), domain (default `<name>.test`, lowercase, 63-char label, 253 total), and path (absolute, exists, directory, readable).
2. Translate the host path to a backend path. On Windows accept drive-letter paths and `\\wsl.localhost\devstack\...`; reject other UNC paths. Resolve symlinks for validation.
3. Require a healthy helper; sync the new domain and optional `www` alias into the hosts block. If this fails, creation fails with `HOSTS_SYNC_FAILED` and no site is persisted.
4. Allocate backend ports: 41000–42999 for HTTP upstreams (static/Node), 43000–43999 for FastCGI.
5. Ensure the runtime build exists (download on demand only after explicit consent; otherwise `RUNTIME_MISSING`).
6. Write the site row, generate configs, hot-reload the proxy route table (builtin) or sync the external vhost set (nginx/Apache).
7. Start the site if `auto_start_sites`, wait for the kind's readiness deadline (30s, 60s for Node), and return the URL and status.
8. In external mode, a static site has no backend process: the vhost is the serving mechanism, and `Start`/`Stop` install a running or branded-503 vhost. Node keeps its supervised backend.

**Delete flow:** stop the site, remove its hosts entries and route, delete certificates and generated configs, keep logs until `devstack prune`, never delete project files. Deletion requires confirmation in the UI and `--yes` in the CLI.

**Rename/domain change:** validate, re-sync hosts, issue a new certificate, update routes atomically, then remove the old hosts entry and certificate. A failed certificate issuance rolls back to the old domain.

**Start/stop:** idempotent. Stop sends SIGTERM, waits `graceful_stop_seconds`, then SIGKILL. A site's state is `stopped | starting | running | stopping | crashed | error`.

## 8. Proxy and routing

- `serving_mode` selects the serving path. `builtin` uses the Go proxy described here. `nginx`/`apache` replace it with helper-managed native vhosts (see the external vhost spec): the external server owns 80/443, static sites are served from their roots, Node is proxied to its loopback port, and PHP uses FastCGI in Plan 2. The API/UI listener on 7070 is unchanged.
- HTTP listener `127.0.0.1:8080`, HTTPS `127.0.0.1:8443`. The helper forwards host port 80/443 to these; on Linux the helper owns the listeners so the daemon stays unprivileged.
- Exact `Host` match (lowercased, port stripped) against the route table. Optional `www.` alias is a separate route with the same site. Unknown hosts return a branded 404 page; `devstack.test` routes to the dashboard.
- Routes reload atomically via an immutable snapshot behind `atomic.Pointer`; no proxy restart on site changes.
- Transports:
  - static: HTTP to the internal static server; range requests, correct MIME, no caching by default, optional SPA fallback
  - PHP: FastCGI to the site's FPM master, with `SCRIPT_FILENAME` in backend path, `PATH_INFO`, query string, and standard params. Existing files are served directly; directories try `index.php` then `index.html`; when `front_controller` is on, non-file requests go to `index.php` with the original path as `PATH_INFO`.
  - Node: HTTP/WebSocket reverse proxy to the allocated port, which is injected into the process as `PORT`; a command that ignores it fails readiness with guidance
- WebSockets and SSE pass through for Node sites. HTTP/2 is enabled on the TLS listener.
- Timeouts: 30s read-header, 120s idle, 60s upstream response header (120s for Node), 1 GiB request body (configurable). Node upstreams get the longer timeout to tolerate dev-server restarts.
- Access logs are per-site, off by default, toggleable for debugging.
- `X-Forwarded-For`, `X-Forwarded-Proto`, and `X-Forwarded-Host` are set; the original `Host` is preserved.
- The proxy never follows redirects or rewrites application responses.

## 9. TLS and trust

**Certificate authority**

- Helper generates an ECDSA P-256 root CA on first init (10 years) and stores the key root-only: `/var/lib/devstack-helper/pki` on Linux, `%ProgramData%\devstack\pki` on Windows.
- The daemon generates per-site ECDSA P-256 keys (`0600`) and sends CSRs to `SignCertificate`.
- The helper signs only CSRs whose every SAN is `devstack.test` or a specific hostname label ending in `.test`; wildcards and all other names are rejected.
- Leaf certificates are valid for 90 days, renewed at 30 days remaining by a daily job. Each site certificate includes the primary domain and the `www` alias when enabled. Renewal hot-swaps the SNI certificate map; no restart.
- The dashboard has its own leaf for `devstack.test`. Unknown hosts use it.
- The helper returns the root certificate to the daemon for chain serving; the private key never leaves the helper.
- In external mode, the daemon sends each site's leaf and key PEM with the vhost sync. The helper verifies the chain, SANs, key match, and expiry, then writes `/etc/devstack/tls/<owner>.{crt,key}` (key `0600` root, cert `0644`) for the external server to read. Renewal triggers a full vhost sync; a failed renewal keeps the old certificate and leaves the vhost unchanged.

**Trust installation**

| Target | How |
|---|---|
| Linux OS | `/etc/pki/ca-trust/source/anchors` + `update-ca-trust` (Fedora) or `/usr/local/share/ca-certificates` + `update-ca-certificates` (Debian/Ubuntu) |
| Linux Chrome | NSS user DB `~/.pki/nssdb` via `certutil`; `devstack init` offers to install `nss-tools`/`libnss3-tools` when missing |
| Linux Firefox | NSS profile injection for discovered profiles; a system `policies.json` enables `security.enterprise_roots.enabled` as the durable path |
| Windows OS | `LocalMachine\Root` via the certificate store API |
| Windows Edge/Chrome | Inherit the Windows store |
| Windows Firefox | Enterprise policy `security.enterprise_roots.enabled=1` under `HKLM\SOFTWARE\Policies\Mozilla\Firefox` |
| WSL | Agent installs the root into the distro trust bundle |

**Runtime trust inside sites**

- Node processes receive `NODE_EXTRA_CA_CERTS` pointing at a generated bundle (system roots + devstack root).
- Generated php.ini sets `curl.cainfo` and `openssl.cafile` to the same bundle.
- Site env also receives `SSL_CERT_FILE`, `CURL_CA_BUNDLE`, and `REQUESTS_CA_BUNDLE` for common toolchains, unless the site overrides them.

**Behavior**

- HTTPS is always provisioned. `tls.enabled` (default true) controls whether HTTPS serves content; when false, HTTPS responds 308 to the HTTP URL for the same site, still using a valid certificate.
- `force_https` (default false) redirects HTTP to HTTPS and is ignored when `tls.enabled` is false. HSTS is never emitted.
- `devstack trust status` reports each target separately (OS, Chrome, Firefox, Node, PHP, and WSL on Windows) with pass/fail and remediation. `devstack trust repair` re-runs installation and is safe to repeat.
- Browser restarts may be required; Doctor says so explicitly rather than claiming success.
- Uninstall removes only our CA (matched by fingerprint), our NSS nicknames, our Firefox policy value if we set it, and our hosts block.

**Known limitations:** profile-less or isolated browser installs (some Snap/Flatpak builds) can bypass system trust; Doctor detects this and explains the manual step. New Firefox profiles created later require `devstack trust repair`.

## 10. Runtime management

### 10.1 Catalog and artifacts

- The catalog ships embedded in the binary: PHP 8.2–8.5 build IDs, Node 22/24 LTS, URLs, SHA-256 checksums, sizes, extension metadata, and minimum OS requirements.
- Downloads go to `cache/`, are checksum-verified before extraction, and extract with a path-traversal and symlink guard into a temporary directory before an atomic rename.
- No remote catalog fetch in M1. Adding a new PHP build requires a new app release (auto-update is M4). Offline install from a local tarball with a matching checksum is supported.
- Installing a runtime requires explicit confirmation showing version, size, and checksum. `--yes` skips the prompt.
- Approximate footprint: 150–300 MB per PHP build, 40–60 MB per Node build.
- `devstack runtime verify` re-checks installed file hashes on demand.

### 10.2 PHP builds

Maintained in `build/php/` and produced by CI (see §19):

- Relocatable tarballs containing `bin/php`, `sbin/php-fpm`, `lib/php/extensions/*.so`, bundled shared libraries, and a base `php.ini`.
- Built on Ubuntu 22.04 (glibc 2.35 baseline) with OpenSSL, libxml2, libcurl, and other dependencies bundled and `RPATH=$ORIGIN/../lib`.
- Versions 8.2, 8.3, 8.4, 8.5; x86_64 only.
- Each build carries a revision (`8.4.12-r3`) so extension updates are traceable. Sites pin a build ID, not just a version.
- Smoke tests gate publication: `php -v`, `php-fpm -v`, `php -m`, a load test for every extension, FPM startup, and a FastCGI request.

### 10.3 Node and static

- Node builds are official `nodejs.org` Linux x64 tarballs for 22 LTS and 24 LTS, checksum-pinned.
- The site command runs with the selected Node's `bin` first in `PATH`, plus npm/corepack.
- Node sites fail readiness if the port does not open within 60 seconds; the log tail is shown.
- Static sites run `devstack serve-static` (internal subcommand) with root, SPA fallback, MIME, range, no directory listing, and path-traversal protection.

## 11. PHP subsystem

### 11.1 Versions

- **Add:** choose from the embedded catalog, download and verify, register, and run a smoke test (`php-fpm -v` and a trivial request). Failure leaves no registered runtime.
- **Remove:** blocked with `VERSION_IN_USE` while any site references the version or build. The CLI/UI can migrate those sites to another version first; migration is validate-then-swap per site.
- **Update:** install a newer build for the same version. Sites remain pinned to their current build until switched. The UI shows available updates and a per-site switch action. Rolling back to a retained older build is supported until it is removed.
- Version defaults (ini directives and extension defaults) live per PHP version and are editable; every site starts from them.

### 11.2 Per-site FPM master

Each running PHP site gets its own `php-fpm` master:

- Generated config under `sites/<id>/php-fpm.conf`, listening on an allocated backend TCP port (43000–43999; loopback on Linux, WSL NAT interface on Windows). TCP is used uniformly so the Windows proxy can reach FPM through WSL forwarding or the distro IP.
- `pm = dynamic`, `pm.max_children = 5`, `pm.start_servers = 2`, `pm.min_spare_servers = 1`, `pm.max_spare_servers = 3`, `catch_workers_output = yes`, `clear_env = no`.
- On Linux, the master runs as the current user. Inside WSL, the agent starts the root master with pool `user = devstack`, `group = devstack`.
- Env vars from the site are injected into the pool. `PATH` includes the selected Node/PHP build paths where relevant.
- The built-in proxy talks FastCGI directly; in external mode nginx/Apache talk FastCGI to the same per-site port. No nginx/Apache is required in builtin mode.
- Stopping the site stops its master. No idle masters are kept.

**Apply strategy (validate-then-swap):** render config → run `php-fpm -t` → start a new master on a new port with the new php.ini → issue a health request → atomically update the site's active FPM port → drain and stop the old master. Any failure leaves the old master serving and returns the validation output.

### 11.3 php.ini

- Composition order: build base `php.ini` → PHP version defaults → site overrides. A complete file is generated; no surprise scan directories.
- Baseline dev defaults: `display_errors=On`, `error_reporting=E_ALL`, `memory_limit=256M`, `upload_max_filesize=64M`, `post_max_size=64M`, `max_execution_time=120`, `date.timezone=UTC`, `opcache.enable=1`, `opcache.validate_timestamps=1`, `opcache.revalidate_freq=0`.
- UI offers key/value rows plus a raw editor for the same generated file; unknown directives and comments are preserved. The managed extension block is read-only in the raw editor and is controlled by the extension toggles.
- Validation runs the build's `php -c <generated> -r 'exit(0);'` and captures warnings/fatals, then `php-fpm -t`. Errors block apply with line numbers where available.
- Changing version defaults applies to all running sites on that version via validate-then-swap, with per-site rollback on failure. The UI lists affected sites in the confirmation.
- Site ini applies only to that site.

### 11.4 Extensions

- All toggleable extensions are compiled shared (`.so`) in every PHP build and merely toggled in php.ini; built-in modules are compiled in. Extension metadata (name, type `extension`/`zend_extension`, `.so` filename, dependencies, default, per-version availability) lives in the catalog.
- Standard extensions are enabled by default except `ffi`; extras are disabled by default. `xdebug` uses `zend_extension` and defaults off. `opcache` is a standard `zend_extension` and defaults on.
- Standard: bcmath, bz2, calendar, curl, dba, dom, exif, ffi, fileinfo, filter, ftp, gd, gettext, gmp, iconv, intl, ldap, mbstring, mysqli, opcache, openssl, pcntl, pdo, pdo_mysql, pdo_pgsql, pdo_sqlite, pgsql, phar, posix, readline, session, simplexml, soap, sockets, sodium, sqlite3, sysvmsg, sysvsem, sysvshm, tidy, tokenizer, xml, xmlreader, xmlwriter, xsl, zip, zlib.
- Extras: redis, apcu, xdebug, imagick, memcached, ssh2, yaml, igbinary, msgpack, grpc, pcov; imap only for 8.2/8.3.
- Truly built-in modules (core, ctype, date, filter, hash, json, pcre, random, spl, standard) are shown as always-on and cannot be toggled.
- Enabling/disabling applies only to that site's master. Version defaults can be changed and applied to affected running sites.
- "Updating an extension" means installing a newer PHP build (§11.1). No independent PECL updates in M1.
- Dependency ordering (for example `igbinary` before `redis`) is encoded in catalog metadata.

### 11.5 PHP CLI for diagnostics

`devstack php --site <name> -- ...` runs the site's PHP build with its generated php.ini for diagnostics. It is not a package manager or Composer wrapper.

## 12. Supervisor, logs, and reconciliation

- The supervisor starts processes in their own process group, tracks PID + start time, and reaps children.
- Readiness: TCP connect for Node/static/FPM plus an optional HTTP probe. Deadline 30s (60s for Node); failures return `READY_TIMEOUT` with the log tail.
- Restart policy: on unexpected exit, backoff 1s, 2s, 4s, 8s, 16s, 30s; after 5 failures in 5 minutes the site becomes `crashed` and stops restarting. Manual start resets the counter.
- Logs: agent/daemon capture stdout/stderr, write `logs/<site-id>/current.log`, rotate at `log_max_mb`, keep `log_keep`, and expose a 5,000-line ring buffer plus SSE follow. PHP master and pool logs are merged into the same stream. Logs are labeled by source (app, fpm, proxy, supervisor).
- Reconciliation on daemon start: load desired states, verify agent health, adopt processes whose PID + start time match, stop orphans that belong to us, and start sites marked `running`. A brief WSL outage marks affected sites `error` with a retry action, not `crashed`.
- State changes emit events to connected UI clients and are stored in `events` (last 500) for diagnostics.
- Deleting a site or uninstalling stops all of its processes before removing files.

## 13. CLI

```
devstack init                     install/verify helper, CA, hosts, agent (Windows), directories
devstack doctor                   full diagnostic report; --json
devstack test backend             run local/WSL backend conformance (developer command)
devstack daemon [run|status|stop]
devstack site add                 --name --path --kind static|php|node [--php 8.4] [--node 22] [--command ...]
devstack site list                table or --json
devstack site show <name>
devstack site start|stop|restart <name>
devstack site remove <name>       --yes
devstack site logs <name>         --follow
devstack site open <name>         opens the URL in the default browser
devstack site php <name> [ver]    show/switch PHP version
devstack site ini <name>          edit/apply php.ini overrides
devstack site ext <name> [--enable|--disable <ext>]
devstack prune                    delete logs for removed sites
devstack runtime list             installed and available
devstack runtime install <id>     e.g. php@8.5 or node@24, with confirmation
devstack runtime remove <id>      blocked while in use
devstack runtime verify           re-check installed file hashes
devstack php --site <name> -- ... run the site's PHP CLI
devstack trust status|repair|remove
devstack vhost status|use|sync|preview|path   native nginx/Apache serving (see vhost spec)
devstack ui                       open the dashboard
devstack uninstall [--wsl]        remove integration; --yes; project files untouched
devstack version
```

All commands support `--json` where meaningful. Errors carry stable codes and a remediation line.

## 14. UI

Embedded Svelte 5 SPA served by the daemon, available at `https://devstack.test` (canonical) and `http://127.0.0.1:7070` (diagnostic fallback). It binds loopback only; each origin bootstraps its own host-scoped session. The add-site wizard includes a server-side directory browser rooted at the user's home and mounted drives (loopback-only, authenticated) because browsers cannot reveal absolute paths. Screens:

- **Onboarding/Doctor:** helper, CA/trust per target, hosts, ports, WSL, runtime integrity; one-click repair.
- **Sites:** live status, runtime badges, URL, start/stop/restart, add-site wizard (path, kind, version, domain, command), logs drawer.
- **Site detail:** status, URL and certificate info, PHP version selector, extension toggles (inherit/on/off), ini editor (rows + raw) with validate and apply, env vars, delete.
- **Runtimes:** installed PHP/Node builds, available versions, add/remove/update, per-version defaults for ini and extensions.
- **Settings:** ports, auto-start, log retention, serving mode (builtin/nginx/Apache) with detection, config-test state, sync and vhost preview, uninstall/cleanup actions.
- **Site detail:** a "Preview vhost" action shows the generated nginx or Apache config without installing it.

Logs stream over SSE with pause/resume and download. Destructive actions require typed confirmation. The UI follows system light/dark and is keyboard navigable.

## 15. API and security

- REST JSON under `/api/v1`; SSE for events and logs. The OpenAPI document is generated and checked in.
- Vhost endpoints: `GET /vhosts/status`, `POST /vhosts/mode`, `POST /vhosts/sync`, and `GET /vhosts/preview?site=&server=`. Mode changes require the same CSRF/session checks as other mutations.
- Auth: the CLI connects over the Unix socket/named pipe (peer credentials). The UI gets a `HttpOnly`, `SameSite=Strict` session cookie from a loopback-only bootstrap endpoint; mutations require a CSRF token. Requests must carry an allowed `Host` (`127.0.0.1`, `localhost`, `devstack.test`).
- The daemon binds loopback only. No remote access, no CORS.
- The helper RPC is JSON over a Unix socket / named pipe with peer-credential checks and a version handshake; incompatible versions refuse to proceed with a clear error.
- Commands are executed as argv arrays. Node start commands are user-authored and run through `/bin/sh -lc` as the site user; they are never executed as root/SYSTEM.
- The helper renders external vhosts only from validated site data (domains, roots, ports, certificate PEM); it accepts no arbitrary config text, paths, commands, or ports. Document roots are symlink-resolved and confined to the registered site path, and template interpolation is newline/NUL-safe.
- Artifacts are checksum-verified and extracted with traversal/symlink guards.
- Secrets and private keys are `0600`; logs redact `Authorization`, cookies, and env values matching `*KEY*`, `*TOKEN*`, `*SECRET*`, `*PASSWORD*`.
- The UI is local-only; no telemetry.

## 16. Windows and WSL2

**Requirements:** Windows 11 22H2+, virtualization enabled, WSL2 installed with systemd support, and WSL NAT networking (mirrored mode is rejected). `devstack init` checks and explains how to enable missing prerequisites; it does not silently enable Windows features or reboot.

**Provisioning**

1. Download a pinned Debian 12 rootfs tarball and verify its checksum.
2. `wsl --import devstack <LOCALAPPDATA>\devstack\wsl <rootfs> --version 2`. If the distro already exists, validate it and reuse rather than re-importing.
3. Write `/etc/wsl.conf` (`systemd=true`, metadata automount), create the `devstack` user, install the agent binary and `devstack-agent.service`, set a bearer token (`0600`).
4. Start the distro, wait for agent health (with retries), and store the token host-side in a `0600` file; the agent stores only its hash.
5. Install the CA into the distro trust bundle and configure the runtime CA environment.

**Paths**

- `C:\Users\...` maps to `/mnt/c/Users/...`; other drives map similarly.
- `\\wsl.localhost\devstack\home\...` maps to `/home/...`.
- UNC paths other than the managed distro are rejected.
- The UI stores and shows the host path. Backend paths are internal.
- Warning shown for `/mnt/c` sites: file watching and many-small-file operations are slower than the distro filesystem; Node watchers may need polling.

**Networking**

- The Windows daemon tries `127.0.0.1` first (WSL localhost forwarding) for the agent and site backends.
- Health checks detect forwarding failures; the daemon falls back to the distro IP obtained from `wsl hostname -I`.
- To make that fallback possible, the agent and site backends bind the WSL NAT interface rather than loopback only. The agent requires its bearer token. Site upstreams are plain HTTP/FastCGI with no authentication, so M1 requires WSL NAT networking; mirrored networking mode is rejected at init because it could expose dev servers to the LAN.
- Only the daemon's loopback ports (UI, proxy) and the helper's 80/443 listeners exist on the Windows host.
- Site routes store the backend host (always `127.0.0.1` on Linux; the distro IP or forwarded `127.0.0.1` on Windows). Routes are refreshed when the distro IP changes.
- Starting a stopped distro is automatic before site operations, with a bounded wait and a clear `WSL_UNAVAILABLE` error.

**Distro lifecycle:** `devstack uninstall --wsl` (explicit confirmation) unregisters the distro after stopping sites. Project files on Windows drives are untouched; files stored only inside the distro are listed for backup before removal.

## 17. Install, upgrade, and uninstall

**Linux install (`install.sh`)**

- Downloads the release binary + checksums, verifies, installs CLI/daemon to `~/.local/bin` and helper to `/usr/local/lib/devstack`, installs helper and user units, then prompts for elevation.
- `devstack init` starts the helper, ensures the CA, installs trust, syncs hosts, starts the daemon, and runs Doctor.
- `devstack init` detects missing `certutil` and offers the exact detected package-manager command with consent; neither the installer nor the helper installs packages silently.

**Windows install (`install.ps1`)**

- Installs to `%LOCALAPPDATA%\Programs\devstack`, registers the helper service (elevation), adds the daemon to per-user startup, then runs `devstack init` to provision WSL.
- Unsigned binaries may trigger SmartScreen in M1; signing is M4. Internal distribution notes this.

**External serving (opt-in):** `devstack vhost use nginx|apache` requires the server, required Apache modules, and a supported include layout to be present already; nothing is installed or enabled silently. `install.sh` does not select a mode. The first switch writes the bridge include, tests the config, and reloads; switching back restores the forwarders. `docs/vhosts.md` documents distro-specific prerequisites and removal.

**Upgrade:** replace binaries, restart daemon, run migrations, verify helper/agent protocol versions, re-sync hosts and forwarders. Downgrades are not supported; the DB backup is kept before migration.

**Uninstall (`devstack uninstall`)**

1. Stop all sites and the daemon.
2. Remove hosts block, CA from all trust stores, Firefox policy, NSS entries, forwarders, helper-managed vhosts/bridge/TLS files, and the helper's CA key/state.
3. Remove system/user units and binaries (helper removal requires elevation).
4. Optionally remove the WSL distro and `~/.devstack` (logs, runtimes, config). Project files are never removed.
5. Print a checklist of what was and was not removed.

## 18. Error handling

- Every error has a stable code, a human message, and a remediation hint. The UI shows the code and a copyable details panel.
- Proxy error pages identify the site and state: stopped, crashed, starting, misconfigured, or backend unreachable, with the last log lines.
- Representative codes: `HELPER_UNAVAILABLE`, `HOSTS_SYNC_FAILED`, `PORT_IN_USE`, `DOMAIN_CONFLICT`, `READY_TIMEOUT`, `PHP_CONFIG_INVALID`, `PHP_VERSION_IN_USE`, `RUNTIME_MISSING`, `RUNTIME_CORRUPT`, `TRUST_MISSING`, `WSL_UNAVAILABLE`, `AGENT_UNAUTHENTICATED`, `BACKEND_UNAVAILABLE`, `SERVING_MODE_UNAVAILABLE`, `VHOST_CONFIG_INVALID`, `VHOST_SYNC_FAILED`, `VHOST_INCLUDE_MISSING`, `SERVER_RELOAD_FAILED`, `VHOST_SYNC_BUSY`.
- Partial failures roll back: a failed site create leaves no route/hosts row; a failed PHP apply keeps the old master; a failed cert issuance keeps the old cert/domain.
- Doctor checks: helper status/version, listener ports, hosts block contents, CA fingerprint and each trust target, WSL/agent health, runtime checksums, port allocation conflicts, and free disk space.

## 19. Testing

**Unit:** SNI selection, hosts parse/render/validate, php.ini composition, extension precedence/dependency order, port allocation, catalog checksums, archive extraction guards, supervisor state machine, path translation, API auth/CSRF/Host checks, config migration, vhost template rendering, vhost validation/injection guards, mode-switch rollback.

**Integration (Linux):** real static/Node/PHP processes behind the proxy; TLS handshake verified against the generated chain; `php -m` assertions per site and extension toggle; ini effects via probe scripts; failed `php-fpm -t` leaves the old master serving; WebSocket echo through the proxy; log rotation. External mode: real nginx and Apache config tests on a temp prefix, static/Node traffic on high ports, WebSocket echo, the TLS/force-HTTPS/503/404 matrix, and builtin→nginx→builtin plus nginx→apache switch rollback.

**Backend conformance:** one suite for `localBackend` and `wslBackend`, covering start/stop/exec/logs/status/port allocation/runtime install/CA install. Linux in CI; WSL nightly on a self-hosted Windows runner and on demand via `devstack test backend`.

**UI:** Playwright against a fake backend for all flows, plus a real-backend smoke test (create static site, start, assert URL, open logs). Accessibility checks for keyboard navigation and labels.

**Security:** helper refuses unauthenticated peers and out-of-scope SANs; trust removal rejects a CA that does not match our fingerprint; extraction rejects traversal/symlinks; UI rejects foreign `Host`/CSRF; site commands never run privileged.

**Manual platform matrix per release:** Ubuntu 22.04, Ubuntu 24.04, Debian 12, Fedora 44, Windows 11 + WSL2. Browser trust checked in Chrome, Edge, and Firefox.

**TDD:** core logic (proxy routing, ini composition, hosts, catalog, supervisor) is developed test-first.

## 20. CI and release

- **App CI:** Linux containers (Ubuntu 22.04, Ubuntu 24.04, Debian 12, Fedora 44) run unit/integration tests, `go test -race`, `go vet`, lint, UI build/lint/test, and Playwright. The Ubuntu job installs `nginx` and `apache2` so external-vhost config-test and serving tests run rather than skip. Windows runners compile and run unit + helper/cert-store tests. WSL conformance runs nightly self-hosted.
- **PHP builds:** separate matrix for 8.2–8.5 with caching, SBOM generation, checksums, and smoke tests; publication only if every extension loads and FPM serves a request.
- **Release:** GoReleaser produces `devstack`, `devstack-helper`, and `devstack-agent` per platform with SHA-256 manifests; `install.sh`/`install.ps1` are attached. M1 ships unsigned binaries; signed OS packages are M4.
- **Versioning:** semantic version for the app; build IDs for PHP/Node artifacts. Helper and agent report protocol versions; mismatches fail closed with an upgrade instruction.

## 21. Acceptance criteria

1. Fresh Linux install → `devstack init` → Doctor reports every applicable trust target (OS, Chrome, Firefox, Node, PHP) green; on Windows it also reports WSL.
2. A static site is reachable at `https://project.test` with no browser warning; HTTP and HTTPS behavior matches the TLS/force-HTTPS settings.
3. PHP 8.2 and 8.5 sites run side by side; each reports its own version; a per-site ini change is visible only on that site; Xdebug enabled on one site is absent from the other.
4. Invalid php.ini is rejected with line information and the running master keeps serving; valid changes swap without terminating in-flight requests.
5. Adding and removing PHP versions works; removal is blocked while referenced and offers migration; switching a site's PHP version is validate-then-swap with rollback.
6. A Node site proxies HTTP and HMR WebSockets; a crashed dev server shows logs and backoff and can be restarted.
7. Start/stop/restart work from CLI and UI; logs stream; daemon restart reconciles healthy processes, stops orphans, and preserves desired state.
8. Fresh Windows install provisions the WSL distro and agent; static/PHP/Node flows and browser trust checks pass; paths on `C:` and inside the distro both work.
9. `devstack trust remove` and `devstack uninstall` revert hosts, CA, listeners, units, and processes; project files are untouched; Doctor diagnoses each intentionally broken precondition.
10. CI is green, race detector clean, helper RPC and archive-extraction security tests pass, and no privileged process writes into user-owned config.
11. Docs cover install, first site, PHP versions/ini/extensions, TLS repair, WSL paths/performance, upgrades, and uninstall.
12. External serving works end to end: `devstack vhost use nginx` (and Apache) installs native vhosts, tests/reloads the server, serves static and Node sites at trusted HTTPS, and `devstack vhost use builtin` restores the built-in proxy and forwarders. A deliberately broken vhost config leaves the previous mode serving. `devstack uninstall` removes our vhosts, bridge, and TLS files without touching unrelated vhosts.

## 22. Risks and mitigations

| Risk | Mitigation |
|---|---|
| WSL localhost forwarding flakes after Windows updates | Retries, distro-IP fallback, agent health checks, Doctor guidance |
| PHP build matrix cost and extension coverage | x86_64 only, tiered standard/extras, build cache, per-build smoke gates |
| Browser trust variance (Snap/Flatpak, new Firefox profiles) | Per-target verification, repair command, explicit limitations in Doctor |
| Port 80/443 already occupied (IIS, existing Apache/nginx) | Preflight with owning-process hint; no auto-stop; configurable ports |
| Privileged helper becomes an escalation path | CA key held by helper, `.test`-only SANs, peer auth, narrow RPC, no exec, security tests |
| WSL filesystem performance for `/mnt/c` projects | Warning at create time, polling hints, recommend distro filesystem |
| Scope creep into M2–M4 | Boundary in §3; later milestones require their own specs |
| Unsigned Windows binaries trigger SmartScreen | Documented for M1; signing in M4 |

## 23. Roadmap after M1

- **M2 — Services & data:** MySQL/Postgres/Redis/Mailpit, shared instances, per-site databases, backups.
- **M3 — Team environments:** `devstack.yaml`, lockfile, `devstack up/init` from a repo, version pinning and onboarding.
- **M4 — Ops & distribution:** log aggregation, diagnostics bundles, auto-update, signed installers, remote catalog, arm64.

## Appendix A — Example CLI session

```bash
devstack init
devstack runtime install php@8.5
devstack site add --name blog --path ~/code/blog --kind php --php 8.5
# ✓ https://blog.test  (PHP 8.5.12-r1, FPM on 127.0.0.1:43001)

devstack site ext blog --enable xdebug
devstack site ini blog --set memory_limit=512M
devstack site restart blog

devstack site add --name shop --path ~/code/shop --kind node --node 24 --command "npm run dev"
# ✓ https://shop.test  (Node 24.9.0, proxy to 127.0.0.1:41003)

devstack doctor
```

## Appendix B — Site creation API shape

```json
POST /api/v1/sites
{
  "name": "blog",
  "domain": "blog.test",
  "www": false,
  "host_path": "/home/aram/code/blog",
  "kind": "php",
  "php_version": "8.5",
  "document_root": "public",
  "tls": true,
  "force_https": false,
  "env": {"APP_ENV": "local"}
}
```

Response includes `id`, `url`, `status`, `backend`, `backend_path`, and active ports.

## Appendix C — Out-of-scope items explicitly deferred

| Item | Milestone |
|---|---|
| MySQL, Postgres, Redis, Mailpit | M2 |
| Backups/restore | M2 |
| `devstack.yaml`, lockfile, team onboarding | M3 |
| Log aggregation/diagnostics bundle | M4 |
| Auto-update, remote catalog, signed MSI/deb/rpm | M4 |
| arm64, macOS, Windows 10 | Post-M4 / not planned |
| Docker/Podman integration | Not planned |
| Custom TLDs, DNS server | Not planned |
