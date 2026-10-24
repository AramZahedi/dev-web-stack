# External nginx and Apache vhosts

devstack serves sites through one of three modes:

| Mode | Ports 80/443 owner | When to use it |
|---|---|---|
| `builtin` (default) | The privileged helper's forwarders and the devstack Go proxy | You want the zero-configuration default |
| `nginx` | A locally installed nginx | Your project expects nginx behavior or config |
| `apache` | A locally installed Apache HTTP Server | Your project expects Apache behavior or modules |

Exactly one mode is active. The dashboard, API, TLS, hosts entries, logs, and site
lifecycle work the same way in every mode.

External serving is Linux only. Static, Node, and PHP sites are supported. A PHP
site additionally needs an installed PHP build; the production catalog is gated
until release artifacts exist (see [PHP](php.md)). There is no per-site serving
mode and nginx and Apache cannot run at the same time on ports 80 and 443.

## Prerequisites

devstack never installs, starts, enables, or configures nginx or Apache. Install
the server with your distribution's package manager first, and make sure its
service can start.

```sh
# Check the installed server and version
nginx -v
apache2 -v        # Debian / Ubuntu
httpd -v          # Fedora / RHEL

# Check the service
systemctl status nginx
systemctl status apache2
```

The selected server must own ports 80 and 443 after the switch. Stop any other
service that is using them. The built-in forwarders are stopped as part of the
mode switch, so they do not need to be stopped by hand. The server's systemd
service must also be active; `devstack doctor` checks it and reports the exact
`systemctl start` command when it is not.

### Apache modules

Apache needs these modules:

- `ssl`
- `proxy`
- `proxy_http`
- `proxy_wstunnel`
- `headers`
- `rewrite`

PHP vhosts additionally need **`proxy_fcgi`** (`mod_proxy_fcgi`), which forwards
requests to the per-site php-fpm master. devstack's module preflight checks the
six base modules above; enable `proxy_fcgi` before syncing PHP sites. devstack
detects the base modules with `apachectl -M` and refuses to switch when one is
missing. It does not enable modules automatically. On Debian/Ubuntu, an
administrator can enable them explicitly:

```sh
sudo a2enmod ssl proxy proxy_http proxy_wstunnel headers rewrite proxy_fcgi
sudo systemctl reload apache2
```

On Fedora/RHEL the modules are normally built in or already loaded; verify with
`apachectl -M`.

### Dashboard port

External mode requires the dashboard to listen on its default port **7070**:

```yaml
# ~/.devstack/config.yaml
ui_port: 7070
```

The generated `devstack.test` vhost always proxies to `127.0.0.1:7070`. A sync
with another `ui_port` fails with a validation error instead of publishing a
broken dashboard. Restart the daemon after changing the port, then sync again.

### PHP prerequisites

A PHP site is served externally through the same loopback php-fpm master that
the built-in proxy uses:

- devstack owns the FPM master and listens on `127.0.0.1:<port>` in
  **43000-43999**; the external server never starts or supervises PHP.
- nginx uses `fastcgi_pass 127.0.0.1:<port>` (FastCGI is built into nginx; no
  extra nginx module is required).
- Apache uses `proxy:fcgi://127.0.0.1:<port>` and requires `mod_proxy_fcgi`.
- The nginx vhost sets `SCRIPT_FILENAME`, `SCRIPT_NAME`, `PATH_INFO`,
  `DOCUMENT_ROOT`, and the standard CGI/HTTP parameters explicitly. Apache's
  `mod_proxy_fcgi` derives them from the mapped filesystem path. A request that
  does not map to a `.php` file is rejected instead of being passed to FPM.
- A PHP site needs an installed PHP build before its vhost can be published.
  The production artifact gate is documented in [PHP](php.md).

Preview the generated vhost without writing anything:

```sh
devstack vhost preview app
devstack vhost preview app --server apache
```

A stopped PHP site publishes a branded 503 vhost, the same as a stopped static
or Node site. Fix the PHP build or its generated configuration, then start the
site and run `devstack vhost sync`.

## File layout

The helper writes only its own files. It never edits the server's main
configuration, user-managed vhosts, or arbitrary directives.

```text
/etc/devstack/
  vhosts/
    nginx/
      current -> releases/20261023T120000Z
      releases/
        20261023T120000Z/
          blog-1a2b3c4d.conf
          shop-5e6f7a8b.conf
          _default.conf
          _devstack.test.conf
          _maintenance.html
    apache/
      current -> releases/...
      releases/...
  tls/
    <owner>.crt        root-owned, 0644
    <owner>.key        root-owned, 0600
```

- Release directories are immutable. `current` is swapped atomically and the
  previous release is kept until the new set passes the config test and reload.
- Generated configs are root-owned `0644`; the TLS directory is `0700`.
- The helper persists mode, release, bridge path, config state, last sync, and
  last error in `/var/lib/devstack-helper/vhosts.json` (`0600`).

### Bridge include

One bridge file connects the server to the helper's `current` directory:

| Server | Bridge file | Contents |
|---|---|---|
| nginx | `/etc/nginx/conf.d/devstack-vhosts.conf` | `include /etc/devstack/vhosts/nginx/current/*.conf;` |
| Apache (Debian/Ubuntu) | `/etc/apache2/conf-enabled/devstack-vhosts.conf` | `IncludeOptional /etc/devstack/vhosts/apache/current/*.conf` |
| Apache (Fedora/RHEL) | `/etc/httpd/conf.d/devstack-vhosts.conf` | `IncludeOptional /etc/devstack/vhosts/apache/current/*.conf` |

The helper checks that the bridge directory is actually included by the server.
If the distribution uses an unsupported layout, the switch fails with
`VHOST_INCLUDE_MISSING` and returns instructions instead of rewriting the main
configuration.

The installed helper unit grants write access to these bridge directories
(`-/etc/nginx/conf.d`, `-/etc/apache2/conf-enabled`, `-/etc/httpd/conf.d`) in
addition to `/etc/devstack`. The leading `-` keeps the unit valid when a server
or a custom include directory is absent. If you edit the unit, keep these
entries or external mode cannot write the bridge under `ProtectSystem=strict`.

### Default server conflicts

Stock nginx packages usually install a default site that declares
`listen 80 default_server;`. nginx permits only one default server per listen
port, so emitting our own marker would make the config test fail. Before each
sync the helper inspects `nginx -T` (falling back to a scan of the fixed nginx
configuration directories) and ignores markers that come from devstack's own
release tree. When a foreign marker exists:

- `_default.conf` is rendered without `default_server`;
- `devstack vhost status` reports `default_server_conflict: true`;
- `devstack doctor` warns under `vhost-default-server`.

Unknown host names then receive the distribution's default page instead of the
devstack branded 404. To restore the branded page, remove `default_server` from
the conflicting vhost (usually the distribution's default site) and run
`devstack vhost sync`. This is a warning, not a mode-switch failure; the
selected sites still work normally.

## TLS custody

The daemon continues to issue and renew per-site certificates. On every sync it
sends the current leaf certificate and key to the helper, which verifies the
chain, SAN set, key match, and expiry before writing:

- `/etc/devstack/tls/<owner>.crt` — `0644`, root-owned
- `/etc/devstack/tls/<owner>.key` — `0600`, root-owned
- `/etc/devstack/tls/` — `0700`

The unprivileged daemon never reads this directory. Only the server master
process needs the keys, and it reads them at startup or reload. Do not relax
these permissions; Doctor reports unexpected modes. Switching back to builtin
removes only the certificate files the helper wrote.

## Switch modes

```sh
devstack vhost status
devstack vhost use nginx --yes
devstack vhost use apache --yes
devstack vhost use builtin --yes
```

`--yes` skips the confirmation prompt. Switch only when you can tolerate a brief
interruption: the selected server reloads and every site may be unavailable for
a moment.

### What each switch does

**builtin → nginx or Apache**

1. The helper stops the built-in 80/443 forwarders.
2. It writes the bridge include and a complete vhost release.
3. It runs the server's config test and reloads the server.
4. On success it persists the new mode. On any failure it restores the previous
   mode and the forwarders.

**nginx or Apache → builtin**

1. The helper removes the bridge include and its release tree.
2. It reloads the server so the vhosts disappear.
3. It restores the built-in proxy and 80/443 forwarders.
4. If removal or reload fails, it keeps the external mode and reports the error;
   devstack never leaves ports 80/443 unowned.

**nginx → Apache or Apache → nginx**

The old server's bridge and release are removed first, then the new server is
configured, tested, and reloaded. A failure restores the old server.

The dashboard's Settings → Serving card exposes the same switch with an impact
confirmation. Site detail has a **Preview vhost** action.

## Sync, preview, status, and paths

```sh
devstack vhost status                 # mode, server, include, config, release, last sync/error
devstack vhost status --json
devstack vhost sync                   # reinstall the full set and reload
devstack vhost preview blog           # active external server, or nginx in builtin mode
devstack vhost preview blog --server apache
devstack vhost path                   # current release and bridge locations
```

`devstack vhost sync` is a no-op in builtin mode. In external mode it renders the
complete set from the registered sites and certificates, writes a new release,
tests it, reloads, and prunes old releases. Site create, update, delete, start,
stop, and certificate renewal trigger the same sync automatically.

One broken site does not block the rest of the set. A running site whose
document root, backend path, or certificate is missing or outside its recorded
backend is published as a stopped vhost (branded 503, no TLS redirect), a
`site.error` event is recorded, and every healthy site is still synced. Fix the
path or certificate, then run `devstack vhost sync`. A stopped Node site may
omit `backend_path` and its backend port because its vhost only serves the 503
page.

The root worker-readability preflight described under [Custom document
roots](#custom-document-roots) is different: permissions are a server
configuration problem, so the sync fails with the exact directory and a
`chmod`/worker remediation instead of silently serving 503. Fix the permission
and resync.

Preview renders from the site record without writing anything. It never exposes
key material: only certificate paths appear in the generated text. The dashboard
drawer shows nginx and Apache tabs with a copy button.

`devstack vhost status` also reports service-active, TLS-directory, and
default-server-conflict fields. Doctor uses the same status: in external mode it
expects 80/443 to belong to the selected server, expects the built-in proxy
ports (8080/8443 by default) to be free, warns about a foreign
`default_server`, and reads TLS modes from the reported helper directory.

## Rollback behavior

Every sync follows the same safety sequence:

1. Render and write a new release.
2. Write TLS files atomically.
3. Point `current` at the new release.
4. Run the fixed config test for the server.
5. Reload the server.
6. Prune old releases only after success.

A failed config test or reload restores the previous `current` release and tests
it again. The returned error keeps the previous vhosts serving, records
`last_error`, and leaves `config_ok` false. The same protection applies to mode
switches, which also restore the built-in forwarders when they fail.

## Troubleshooting

### `VHOST_INCLUDE_MISSING`

The server does not include the bridge directory. Check the main configuration:

- nginx: `nginx.conf` must include `/etc/nginx/conf.d/*.conf`.
- Apache Debian: `apache2.conf` must include `conf-enabled/*.conf`.
- Apache RHEL: `httpd.conf` must include `conf.d/*.conf`.

Devstack will not edit the main configuration. Add the include yourself if your
distribution uses a custom layout, then run `devstack vhost sync`. Confirm with
`devstack vhost path` and `nginx -t` or `apachectl configtest`.

### `VHOST_CONFIG_INVALID`

The rendered set failed the server's config test, and the previous release was
restored. Run the test directly to see the server output:

```sh
sudo nginx -t
sudo apachectl configtest
```

Check that site document roots still exist and are readable, then run
`devstack vhost status`, `devstack doctor`, and `devstack vhost sync`. If the
server was recently upgraded, confirm the Apache modules are still loaded.

### `SERVER_RELOAD_FAILED`

The config test passed but the reload did not. The previous release is active.
Inspect the service and its logs:

```sh
systemctl status nginx
journalctl -u nginx -n 100
systemctl status apache2
journalctl -u apache2 -n 100
```

Fix the service problem and retry the sync. If the server cannot be recovered,
switch back with `devstack vhost use builtin --yes`.

### Daemon starts but serving mode is broken

Daemon startup reconciles the helper before starting sites, but a failed
reconcile is non-fatal: the dashboard/API, Doctor, site removal, and
`devstack vhost use builtin` stay available. The failure is logged, stored in
the `serving_mode_error` setting, and visible through the helper/Doctor status.
Fix the vhost problem and run `devstack vhost sync`, or return to builtin with:

```sh
devstack vhost use builtin --yes
```

### `VHOST_SYNC_BUSY`

Only one sync or mode switch runs at a time. Wait for the current operation to
finish, then retry. Do not run several `devstack vhost sync` commands in
parallel.

### Permissions

The helper runs as root and owns `/etc/devstack`, its releases, and the TLS
directory. Do not `chmod` or `chown` those files to make the daemon read them.
Check the helper:

```sh
systemctl status devstack-helper
journalctl -u devstack-helper -n 100
```

TLS keys should be `0600`, certificates `0644`, and `/etc/devstack/tls` `0700`.
`devstack doctor` reports unexpected modes.

### Custom document roots

A document root may be relative to the project or absolute, but it must:

- exist and be a directory,
- be readable by the server,
- resolve without a symlink escape outside the site's registered backend path,
- not contain `..` or NUL bytes.

The helper runs as root, so it also checks that the **server worker** can reach
the root. For a running static site, every ancestor directory must grant world
execute (`o+x`) and the root itself must grant world read and execute (`o+rx`).
A POSIX access ACL that grants the same access to a group or named user is
accepted. When the check fails, the sync returns an actionable message such as:

```text
the nginx/Apache worker cannot traverse directory "/home/you" (mode 0700);
grant world execute (for example `sudo chmod o+x /home/you`), or run the server
worker as the site owner, then run `devstack vhost sync` again
```

On Fedora and RHEL, home directories are commonly `0700`; either grant
`o+x` on the home (not the whole tree) or configure nginx/Apache to run its
worker as the site owner. The check is enforced only when the helper runs as
root; unprivileged helper tests and the E2E suite skip it because they cannot
know the distribution's worker account.

If a root moved, update the site (or remove and re-add it) before the next sync.
A failed sync restores the previous vhosts but does not change the site record.

### Uninstall

```sh
devstack uninstall --yes
```

Uninstall removes the helper's vhost releases, bridge includes, and TLS files
before stopping the forwarders. It does not remove nginx or Apache packages,
their main configuration, or project files. To return to the built-in proxy
before uninstalling, run `devstack vhost use builtin --yes` first.

## Testing external mode

The Linux E2E script (`test/e2e/linux.sh`) runs the full builtin flow first,
then exercises a real `builtin → nginx → builtin` switch when nginx is installed
and the dashboard port is free. It never uses root, a system service, or a
system configuration file:

- nginx runs from a temporary prefix on high HTTP/HTTPS ports;
- the helper is pointed at temporary vhost, state, bridge, and TLS trees;
- test-only `nginx` and `systemctl` wrappers in the temporary `bin` directory
  add the prefix/config flags and translate `systemctl reload nginx` into
  `nginx -s reload`;
- the static site is fetched through the nginx vhost with `--resolve`,
  `--noproxy '*'`, and the daemon CA, then again through the builtin forwarder;
- when an installed Node runtime exists, a Node site is served through the same
  nginx vhost; otherwise that check is skipped with a message.

The dashboard vhost always targets `127.0.0.1:7070`, so external mode requires
`ui_port: 7070`. If 7070 is already in use, the script runs the builtin flow on
a high UI port and skips the external steps with an explicit message.

The high listen ports used by the test are a test seam, not a production
option: `vhost_http_port`/`vhost_https_port` (helper flags
`--vhost-http-port`/`--vhost-https-port`) default to 80/443 and production
always renders those ports. Do not change them on a real installation unless
you are deliberately running the server unprivileged for a test.

## Limitations

- PHP sites require an installed PHP build. The production PHP catalog stays
  empty until release artifacts are published, so a PHP site cannot be created
  from a shipped release yet (see [PHP](php.md)).
- Apache PHP vhosts need `proxy_fcgi`; devstack does not enable it for you.
- External serving is Linux only. Windows and WSL use the built-in proxy.
- There is no per-site mode: all sites share one serving mode.
- nginx and Apache cannot serve simultaneously on ports 80 and 443.
- devstack does not install servers or modules, generate custom directives, or
  edit user-managed vhosts.
- In builtin mode, preview defaults to nginx because no external server is
  active.
