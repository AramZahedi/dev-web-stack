# dev-web-stack — External nginx/Apache Vhosts: Design

- **Status:** Implemented in v0.1.0
- **Date:** 2026-10-22
- **Scope:** M1 extension (Plan 1 tasks 21–26; PHP templates in Plan 2)
- **Related:** `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`,
  `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md`

## 1. Summary

Add an optional, helper-managed external serving mode. In addition to the built-in
Go proxy, a machine can run sites through native **nginx** or **Apache** vhosts.
Exactly one serving mode is active: `builtin` (default), `nginx`, or `apache`.

The privileged helper owns vhost rendering, installation, certificates, config tests,
reloads, and rollback. It accepts a validated site specification — never arbitrary
config text, file paths, commands, or ports. The built-in proxy remains the default and
keeps the current 80/443 forwarders; external mode releases those ports and lets the
selected server own them.

## 2. Goals

1. Generate native nginx and Apache vhosts for static and Node sites in Plan 1, and PHP
   sites in Plan 2.
2. Switch serving modes explicitly and reversibly, with config-test and reload rollback.
3. Keep TLS, hosts, logs, supervision, CLI, API, UI, and Doctor behavior consistent
   across modes.
4. Keep the privileged surface narrow: the helper renders only from validated data and
   runs only fixed reload/test commands.
5. Leave all unrelated server configuration untouched; uninstall removes only our files.

**Non-goals:** installing nginx/Apache or their modules; enabling modules automatically;
per-site serving modes; running nginx and Apache simultaneously; Windows external
serving; user-supplied custom directives; editing user-managed vhosts; PHP vhosts before
Plan 2.

## 3. Decisions

| Area | Decision |
|---|---|
| Modes | `serving_mode: builtin \| nginx \| apache`; default `builtin`; one active. |
| Ownership | Helper renders and installs vhosts, certificates, bridge includes, and reloads the server. |
| Vhost style | Native: static `root`/`try_files`, Node HTTP+WebSocket proxy, PHP FastCGI (Plan 2). |
| Static lifecycle | External mode has no `serve-static` process; the vhost is the serving mechanism. |
| TLS | Per-site devstack leaf certs copied into a root-owned tree; key `0600`, cert `0644`. |
| Ports | External server owns 80/443; built-in proxy listeners do not start; backend ports stay 41000–42999 (HTTP) and 43000–43999 (FastCGI). |
| Files | `/etc/devstack/vhosts/<server>/current/*.conf`, `/etc/devstack/tls/`, one bridge file in the server's include directory. |
| Sync | Versioned release + atomic `current` symlink + config test + reload; rollback on failure. |
| Include | One bridge file only (`devstack-vhosts.conf`); no arbitrary edits to main configs. |
| Dashboard | `devstack.test` gets a TLS vhost to `127.0.0.1:7070`. |
| UI/API | Serving status, mode switch, sync, and preview exposed through CLI, API, and dashboard. |
| PHP | Template and FPM wiring land in Plan 2; Plan 1 returns a clear unsupported-kind error. |

## 4. Architecture

```
                      ┌────────────────────────────────────────────┐
 devstack CLI / UI ──►│ devstackd (unprivileged)                   │
                      │  sites · certs · backends · API · UI        │
                      └───────────────┬────────────────────────────┘
                                      │ Unix socket (peer UID checked)
                      ┌───────────────▼────────────────────────────┐
                      │ devstack-helper (root)                     │
                      │  validates VHostSpec · renders templates   │
                      │  writes /etc/devstack · tests · reloads    │
                      └───────┬───────────────────────┬────────────┘
                              │                       │
                    ┌─────────▼─────────┐   ┌─────────▼─────────┐
                    │ nginx or Apache   │   │ forwarders (only  │
                    │ 80/443 native     │   │ in builtin mode)  │
                    └─────────┬─────────┘   └───────────────────┘
                              │
                    static root / Node port / PHP FPM port
```

- The daemon remains the source of truth for sites, desired state, and certificates.
- In external mode, the daemon sends a complete `VHostSyncRequest` to the helper after
  every site/cert change and on startup. The helper renders the whole set, installs a new
  release, tests, and reloads. A failed sync never leaves the server serving a half-written
  set.
- The helper records mode, release, bridge path, and last error in
  `/var/lib/devstack-helper/vhosts.json` (`0600`). The daemon reconciles this state on start.

## 5. Filesystem layout

```
/etc/devstack/
  vhosts/
    nginx/
      current -> releases/20261022T214500Z
      releases/20261022T214500Z/
        blog-1a2b3c4d.conf
        shop-5e6f7a8b.conf
        _default.conf
        _devstack.test.conf
        _maintenance.html
    apache/
      current -> releases/...
      releases/...
  tls/
    <owner>.crt        0644 root:root
    <owner>.key        0600 root:root
```

- Directories `0755`; generated configs `0644`; TLS directory `0700`.
- `<owner>` is a site ID or `__dashboard__`; filenames use `<slug>-<id8>.conf` and are
  validated/sanitized by the helper.
- Releases are immutable. `current` is swapped atomically with `rename(2)`; the previous
  release is retained until the new one passes config test and reload, then pruned to the
  last two.
- Bridge files (ours only, removed on uninstall):
  - nginx: `/etc/nginx/conf.d/devstack-vhosts.conf` containing
    `include /etc/devstack/vhosts/nginx/current/*.conf;`
  - Apache Debian: `/etc/apache2/conf-enabled/devstack-vhosts.conf`
  - Apache RHEL: `/etc/httpd/conf.d/devstack-vhosts.conf`
    containing `IncludeOptional /etc/devstack/vhosts/apache/current/*.conf`
- The helper verifies the bridge directory is actually included by the server. If the
  distro layout is unsupported, it returns `VHOST_INCLUDE_MISSING` with exact instructions;
  it never edits the main server config.

## 6. Helper RPC

Protocol version stays `1`; new endpoints:

| RPC | Purpose |
|---|---|
| `GET /v1/vhosts/status` | Mode, server, version, bridge path, include present, current release, config-test state, last error, last sync. |
| `POST /v1/vhosts/sync` | Install a complete `VHostSyncRequest` and reload. |
| `POST /v1/vhosts/mode` | Switch `builtin`/`nginx`/`apache`, then sync or remove. |
| `DELETE /v1/vhosts` | Remove our vhosts, bridge, and TLS material; restore builtin forwarders. |

`VHostSyncRequest` (JSON, size-capped at 8 MiB):

```go
type VHostSite struct {
    ID, Name, Domain string
    WWW              bool
    Kind             string // static | node (php in Plan 2)
    DocumentRoot     string // absolute host path, symlink-resolved
    BackendPort      int    // Node HTTP or PHP FastCGI; 0 for static
    TLS              bool
    ForceHTTPS       bool
    SPAFallback      bool
    FrontController  bool
    DesiredState     string // running | stopped
    CertPEM, KeyPEM  []byte
}

type VHostDashboard struct {
    Domain      string // devstack.test
    BackendPort int    // 127.0.0.1:7070
    CertPEM, KeyPEM []byte
}

type VHostSyncRequest struct {
    Mode          string // nginx | apache
    MaxBodyBytes  int64
    Sites         []VHostSite
    Dashboard     VHostDashboard
    RequestedAt   time.Time
}
```

The helper validates every field before rendering. It never reads a path supplied by the
daemon except the validated document root (opened with `os.OpenRoot`) and never executes a
command supplied by the daemon.

## 7. Validation rules

- Mode is one of `nginx`, `apache`; server binary found via fixed `exec.LookPath` names.
- Domains: `domain.ValidateDomain`, `.test` only, no wildcards, no newlines, lowercase.
- Site ID/name: `[a-z0-9-]`, bounded length; certificate owner matches the site.
- Document root: absolute, exists, directory, symlink-resolved, and inside the site's
  recorded backend path; reject `..` and NUL.
- Backend port: `0` for static; `41000–42999` for Node; `43000–43999` for PHP; loopback
  target only. The dashboard backend is fixed at `127.0.0.1:7070`.
- Cert/key: PEM parses; ECDSA P-256 key; public keys match; leaf chains to the helper CA;
  SAN set equals the site's domains (`name.test` plus `www.` when enabled); not expired;
  key PEM is never logged.
- Config text is rendered from templates only; every interpolated string is validated or
  escaped. No user-supplied directives.
- Sync requests are serialized by a mutex; concurrent requests return `VHOST_SYNC_BUSY`.
- Unknown files in our release directories are ignored and never deleted; only files with
  our generated header are managed.

## 8. Vhost behavior

### 8.1 Matrix

| Site state | HTTP :80 | HTTPS :443 |
|---|---|---|
| `tls=true`, `force_https=true` | 308 to `https://<host><uri>` | serve |
| `tls=true`, `force_https=false` | serve | serve |
| `tls=false` | serve | 308 to `http://<host><uri>` |
| `desired_state=stopped` | branded 503 | branded 503 (with valid cert) |
| crashed/unreachable | branded 503 via `error_page` | branded 503 |

- `www` is a second `server_name`/`ServerAlias` in the same vhost; the certificate SANs
  include both names.
- Unknown hosts reach a branded default 404 vhost served with the dashboard certificate.
- No HSTS is ever emitted. Static responses use `Cache-Control: no-store`.

### 8.2 Nginx

- Static: `root <docroot>; index index.html; location / { try_files $uri $uri/ ... }`;
  SPA fallback to `/index.html` when enabled; `autoindex off`; deny dotfiles and
  `/_devstack` internals.
- Node: `proxy_pass http://127.0.0.1:<port>;` with `proxy_http_version 1.1`,
  `Upgrade`/`Connection` passthrough, `Host`, `X-Forwarded-For/Proto/Host`,
  `proxy_read_timeout 120s`, `client_max_body_size` from config.
- PHP (Plan 2): `fastcgi_pass 127.0.0.1:<port>;` with `SCRIPT_FILENAME`, `PATH_INFO`,
  query string, and front-controller `try_files`.
- TLS: `listen 443 ssl; http2 on;` TLS 1.2/1.3, `ssl_certificate
  /etc/devstack/tls/<owner>.crt`, `ssl_certificate_key ...key`.
- Maintenance: `error_page 502 503 504 =503 /_devstack/503.html;` with an internal
  location; stopped sites return 503 directly.

### 8.3 Apache

- Required modules: `ssl`, `proxy`, `proxy_http`, `proxy_wstunnel`, `headers`, `rewrite`.
  Detect with the fixed `apachectl -M` command; if missing, fail with `a2enmod`/package
  remediation and do not enable anything automatically.
- Static: `DocumentRoot`, `<Directory>` `Require all granted`, `Options -Indexes`,
  `FallbackResource /index.html` when SPA fallback is on, dotfile `<FilesMatch>` denial,
  `Header always set Cache-Control "no-store"`.
- Node: `ProxyPass / http://127.0.0.1:<port>/ upgrade=websocket connectiontimeout=5
  timeout=120` plus `ProxyPassReverse`; forwarded headers via `RequestHeader`.
- PHP (Plan 2): `<FilesMatch \.php$>` with `SetHandler "proxy:fcgi://127.0.0.1:<port>"`,
  `ProxyFCGISetEnvIf` for `SCRIPT_FILENAME`, and front-controller rewrite.
- TLS: `SSLEngine on`, `SSLCertificateFile`, `SSLCertificateKeyFile`, TLS 1.2/1.3.
- Maintenance: `ErrorDocument 503 /_devstack/503.html` with an `Alias` to the shared file.

## 9. Sync and mode-switch algorithm

```
Sync(req):
  validate request + certs
  render all configs into releases/<utc-timestamp>/
  write TLS files atomically (temp + fsync + rename, 0600 key / 0644 cert)
  ensure bridge file exists and points at current/
  atomically swap current -> new release
  run fixed config test
  if test fails: swap current back, re-test, return VHOST_CONFIG_INVALID (stderr scrubbed)
  run fixed reload
  if reload fails: swap current back, reload, return SERVER_RELOAD_FAILED
  prune releases older than the last two
  persist status
```

- Switching `builtin → nginx|apache`: stop forwarders, write bridge + release, test,
  reload, persist mode. If any step fails, restore forwarders and the previous mode.
- Switching `nginx|apache → builtin`: remove bridge, release tree, and TLS material,
  reload the server, then restore forwarders. If removal/reload fails, keep the mode and
  report; never leave 80/443 unowned.
- Switching between nginx and Apache: disable the old server's bridge/release first, then
  enable the new one; on failure restore the old.
- The daemon treats a failed sync during a site mutation like a hosts-sync failure:
  stop the backend, delete the new cert, restore hosts, and return the server error.

## 10. TLS and certificates

- The daemon continues to own per-site keys/certs and renewal.
- On sync, it sends the current leaf + key PEM for each site and the dashboard.
- The helper verifies the chain against its own CA, the SAN set, key match, and expiry,
  then writes `/etc/devstack/tls/<owner>.{crt,key}`. Keys are root-only; the server master
  process reads them at startup/reload.
- Renewal triggers a full sync; the vhost content hash avoids a reload when nothing else
  changed. Failed renewal keeps the old cert and does not touch the vhost.
- Uninstall/`use builtin` deletes only files in `/etc/devstack/tls` that the helper wrote.

## 11. Daemon integration

- `config.Config` gains `ServingMode string` (`builtin` default); validation accepts the
  three modes. The mode is persisted in `config.yaml` and mirrored in the helper state.
- `sites.Service` gains a `Vhosts` dependency and a serving-mode branch:
  - `builtin`: today's behavior (proxy routes, `serve-static` process for static).
  - `nginx|apache`: static sites have no backend process; `Start` validates the root and
    syncs a running vhost, `Stop` syncs a 503 vhost, and status is derived from desired
    state + root readability. Node/PHP keep their supervised backends.
  - Every create/update/delete/start/stop and cert renewal calls `SyncVhosts` when the mode
    is external; a sync failure rolls the mutation back.
- Daemon startup: read helper vhost status; if it disagrees with `serving_mode`, reconcile
  (daemon config wins), then sync the full set and start desired backends.
- The API/UI listener on `127.0.0.1:7070` stays unchanged in every mode.

## 12. CLI, API, and UI

**CLI**

```
devstack vhost status [--json]
devstack vhost use nginx|apache|builtin [--yes]
devstack vhost sync
devstack vhost preview <site> [--server nginx|apache]
devstack vhost path
```

**API**

```
GET  /api/v1/vhosts/status
POST /api/v1/vhosts/mode        {"mode":"nginx|apache|builtin"}
POST /api/v1/vhosts/sync
GET  /api/v1/vhosts/preview?site=<ref>&server=nginx|apache
```

**UI**

- Settings → Serving: current mode, detected server/version, bridge/include state,
  config-test state, last sync, switch buttons, Sync now, preview drawer.
- Site detail: "Preview vhost" action for nginx and Apache.
- Doctor: serving mode, server installed/running, bridge present, config test, release
  matches the registered sites, TLS key permissions.

## 13. Error codes

| Code | Meaning |
|---|---|
| `SERVING_MODE_UNAVAILABLE` | Server binary, service, modules, or include layout is missing/unsupported. |
| `VHOST_CONFIG_INVALID` | Rendered config failed the server's config test; previous release restored. |
| `VHOST_SYNC_FAILED` | Install/atomic swap/prune failed; previous release restored. |
| `VHOST_INCLUDE_MISSING` | The server does not include our bridge directory. |
| `SERVER_RELOAD_FAILED` | Config test passed but reload failed; previous release restored. |
| `VHOST_SYNC_BUSY` | A sync is already running. |

Every error carries a human message and remediation; server stderr is scrubbed of secrets
and truncated.

## 14. Security

- Peer UID checked on every helper call; the vhost RPCs are not exempt.
- No arbitrary paths, commands, ports, or template text from the daemon.
- Vhost files are root-owned; site keys are `0600`; the daemon never needs to read
  `/etc/devstack/tls`.
- Document roots are resolved and checked for traversal/symlink escape; the helper also
  refuses roots outside the site's registered backend path.
- Config rendering escapes server names, paths, and headers; newline/NUL injection tests
  are mandatory.
- The bridge file is the only file written outside `/etc/devstack`; it is marker-owned and
  removed on uninstall. If a user edits it, Doctor reports drift and refuses to overwrite.
- Reload/test commands are fixed per server and mode; no shell interpolation.
- Symlink swaps and TLS writes use temp files, fsync, and rename; no partial configs.

## 15. Testing

- Golden-file tests for nginx and Apache: static running/stopped, SPA on/off, TLS on/off,
  force-HTTPS on/off, www on/off, Node, dashboard, default 404, maintenance 503.
- Validation tests: bad domains, traversal/symlink roots, out-of-range ports, wrong SANs,
  mismatched keys, expired certs, newline injection, oversized payloads, concurrent sync.
- Helper tests with a fake runner: bridge writes, atomic release swap, config-test failure
  rollback, reload failure rollback, pruning, state persistence, permissions.
- Real-server tests (skip when not installed): `nginx -t` / `apachectl configtest` on a
  temp prefix, then serve static and Node traffic on high ports; WebSocket echo through
  nginx and Apache; 503/404/redirect matrix.
- Mode-switch tests: builtin→nginx→builtin, nginx→apache, failure at each step; assert
  80/443 ownership, forwarder state, and old config restored.
- E2E: helper + daemon in a temp root, switch to nginx if present, create static and Node
  sites, curl with `--resolve` and the devstack CA, switch back, verify forwarders.
- CI adds nginx and Apache packages to the Ubuntu job; tests skip cleanly elsewhere.

## 16. Plan changes

- **Plan 1** gains:
  - Task 21: `internal/vhost` model + nginx/Apache renderers + golden tests.
  - Task 22: helper vhost manager, bridge, releases, config test/reload/rollback, RPC,
    TLS validation, security tests.
  - Task 23: daemon serving-mode integration, static lifecycle branch, API endpoints.
  - Task 24: CLI `vhost` commands + Doctor checks.
  - Task 25: UI Serving screen, site vhost preview, docs, E2E.
  - Task 26: acceptance pass (renumbered from Task 21), now including external mode.
- **Plan 2** gains PHP vhost templates and FPM FastCGI wiring when PHP lands.
- **M1 spec** removes the “Apache/nginx configuration generation” non-goal, adds the
  serving-mode decision and cross-references, and updates CLI/UI/API/security/install/
  testing/acceptance sections.

## Appendix A — Example nginx vhost (Node, TLS, force HTTPS)

```nginx
# Generated by devstack. Do not edit.
server {
    listen 80;
    server_name shop.test www.shop.test;
    return 308 https://$host$request_uri;
}

server {
    listen 443 ssl;
    http2 on;
    server_name shop.test www.shop.test;

    ssl_certificate     /etc/devstack/tls/site-5e6f7a8b.crt;
    ssl_certificate_key /etc/devstack/tls/site-5e6f7a8b.key;
    ssl_protocols TLSv1.2 TLSv1.3;

    client_max_body_size 1024m;
    location / {
        proxy_pass http://127.0.0.1:41003;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_read_timeout 120s;
    }
}
```

## Appendix B — Example Apache vhost (static, SPA, TLS)

```apache
# Generated by devstack. Do not edit.
<VirtualHost *:80>
    ServerName blog.test
    ServerAlias www.blog.test
    Redirect permanent / https://blog.test/
</VirtualHost>

<VirtualHost *:443>
    ServerName blog.test
    ServerAlias www.blog.test
    DocumentRoot "/home/aram/code/blog/public"
    <Directory "/home/aram/code/blog/public">
        Options -Indexes
        AllowOverride None
        Require all granted
        FallbackResource /index.html
    </Directory>
    <FilesMatch "^\.">
        Require all denied
    </FilesMatch>
    Header always set Cache-Control "no-store"
    SSLEngine on
    SSLCertificateFile /etc/devstack/tls/site-1a2b3c4d.crt
    SSLCertificateKeyFile /etc/devstack/tls/site-1a2b3c4d.key
    SSLProtocol -all +TLSv1.2 +TLSv1.3
</VirtualHost>
```

## 17. Implementation notes (2026-10-23)

These are the binding corrections that emerged during implementation. They refine,
but do not replace, the design above.

- `VHostSite` includes `BackendPath`; document roots are resolved and must be
  contained inside it. Static sites require a readable root while running; a
  stopped static vhost may omit a missing root and still serve the branded 503.
- `tls=false` sites use the dashboard certificate for their HTTPS listener and
  redirect to HTTP; the helper always installs the dashboard certificate.
- nginx renders `listen <https> ssl http2;` rather than `http2 on;` so nginx 1.24
  (Ubuntu 24.04) and newer both pass the config test.
- Apache module detection uses `httpd -M`/`apache2 -M` with `apachectl -M`
  fallback because Fedora's wrapper rejects `-M`.
- `POST /v1/vhosts/mode` takes `{"mode":"builtin|nginx|apache","sync":{...}}`;
  `sync` is required for external targets.
- `VhostStatusResponse` reports `tls_dir_mode` and `tls_files` (name, mode,
  regular-file flag) so the unprivileged Doctor can inspect TLS custody without
  reading the root-only directory. It also reports the live mode after a runtime
  switch.
- External mode requires `ui_port: 7070`, matching the helper's fixed dashboard
  backend. `Config.Validate` rejects other UI ports while external.
- The helper suppresses `auto_forward` when persisted state says nginx/Apache,
  so a helper restart never steals 80/443 from the external server.
- `vhost.Set` and the helper config expose optional high listen ports for the
  unprivileged E2E. Production defaults remain 80/443; redirect targets include a
  non-default port, including Apache mixed source/target ports.
- The helper status carries the bridge content hash; a user-edited bridge is
  never overwritten or removed in sync, teardown, or mode switches.
- Before rendering nginx, the helper inspects `nginx -T` (with a fixed
  configuration-directory scan as fallback) for a foreign `default_server` on
  the rendered listen ports, ignoring its own release tree. When one exists,
  `_default.conf` omits the marker and status/Doctor report
  `default_server_conflict`; the switch still succeeds.
- When the helper runs as root, running static roots are preflighted for server
  worker traversal/read (world bits or a POSIX ACL) and the sync returns the
  failing directory with a `chmod`/worker remediation. Non-root helper tests
  and E2E skip this check.
- A site whose document root, backend path, or certificate cannot be used is
  rendered by the daemon as a stopped 503 vhost and produces a `site.error`
  event instead of failing the whole sync.
- Daemon startup publishes the API/socket before reconciling vhosts. Reconcile
  failures are logged and stored in `serving_mode_error`, so `vhost use
  builtin`, Doctor, and site removal remain available.
- Helper status reports `service_active` (fixed `systemctl is-active` probe),
  `tls_dir`, and `default_server_conflict`. Doctor uses these for mode-aware
  port, service, and TLS-directory checks.
- Apache static vhosts deny dot path segments as well as dotfile basenames so
  `/project/.git/config` cannot be served.
