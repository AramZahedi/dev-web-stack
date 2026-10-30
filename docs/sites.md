# Sites

devstack serves each site at `https://<name>.test` (and optionally
`https://www.<name>.test`) through its builtin proxy. A site is created and
started in one command. Static sites are served by a supervised internal static
server; Node sites run your development command on an allocated loopback port.

Complete [installation](install.md) and `devstack init` first. The daemon must
be running for site commands; they start it automatically when needed.

The examples below use `$HOME/projects/...`; replace those paths with existing
project directories on your machine. Create the example static directory with
`mkdir -p` before adding the site.

## Add a static site

```sh
mkdir -p "$HOME/projects/blog"
cat >"$HOME/projects/blog/index.html" <<'HTML'
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Blog</title></head>
<body><h1>Hello from devstack</h1></body>
</html>
HTML

devstack site add --name blog --path "$HOME/projects/blog"
```

The site is registered as `blog.test` and started immediately:

```text
Created site blog
URL:      https://blog.test
Status:   running
```

Open <https://blog.test>. The hosts block and certificate are created
automatically.

Serve a subdirectory with `--document-root`:

```sh
devstack site add --name docs --path "$HOME/projects/docs" --document-root public
```

Serve `index.html` for client-side routes with `--spa`:

```sh
devstack site add --name app --path "$HOME/projects/app" --document-root dist --spa
```

Add `--www` to serve `www.blog.test` as an alias, `--no-https` for HTTP only, or
`--force-https` to redirect HTTP requests to HTTPS.

## Node toolchains

Every site kind has a Node toolchain. Node server sites require an explicit
version; static and PHP sites may inherit the machine-wide default. `node`,
`npm`, and `npx` in `devstack shell`, `devstack exec`, and the dashboard
terminal resolve in this order:

1. the site's exact `node_version`;
2. the global Node CLI default;
3. a clear "no Node toolchain" error.

The build profile's `build.node_version` is build-only and never overrides a
site shell. Node server sites must expose an HTTP server on the port provided in
`PORT` and bind to loopback. devstack injects `PORT`, a runtime-specific
`PATH`, and `NODE_EXTRA_CA_CERTS` pointing at the machine CA bundle.

```sh
cd "$HOME/projects/api"
# package.json should provide a start/dev script, for example:
#   "dev": "node server.js"
# and server.js should call app.listen(process.env.PORT, "127.0.0.1").

devstack site add \
  --name api \
  --kind node \
  --path "$HOME/projects/api" \
  --node 22.11.0 \
  --command "npm run dev"
```

`--node` accepts an installed exact version, `major.minor`, or major. If the
release is not installed, `site add` resolves it against the live official
Node index, verifies the official SHA-256, installs it after confirmation, and
stores the exact installed version. Pass `--yes` to install without prompting:

```sh
devstack site add --name api --kind node --path "$HOME/projects/api" \
  --node 22 --command "npm run dev" --yes
```

Static and PHP sites may omit `--node` and inherit the global default:

```sh
devstack site add --name docs --kind static --path "$HOME/projects/docs"
devstack site node docs 22.11.0      # pin an installed version
devstack site node docs --clear      # return to the global default
```

Node server sites reject `--clear`. Site version changes take effect in the
next shell/build; restart a running Node server site after changing its pin.

Node's HMR WebSocket traffic is proxied by the same host route. devstack sets
`PORT` after applying `--env` values, so a site environment entry named `PORT`
has no effect. Make the dev server read `process.env.PORT`; a command that
hard-codes another port (for example `PORT=3000 npm run dev`) bypasses the
allocated route and will not be reachable through devstack.

## Node releases and the global default

`devstack runtime available --kind node` shows the newest patch of each major
from `https://nodejs.org/dist/index.json`. Add `--all` to reveal older minors
and patches, `--major N` to filter, `--refresh` to force a fetch, or `--offline`
to use the cache/embedded fallback. `devstack runtime install node@22`,
`node@22.11`, or `node@22.11.0` resolves against the live index and verifies the
official `SHASUMS256.txt` before installing.

The global default mirrors PHP:

```sh
devstack node default --status
devstack node default 22.11.0
devstack node default --clear
```

It publishes helper-managed `node`, `npm`, and `npx` wrappers in
`/usr/local/bin` on Linux, `.cmd` shims in the user bin directory on Windows,
and WSL shims under `tools/node-default/bin`. Installing a build never changes
an explicit default.

## Commands

| Command | Purpose |
|---|---|
| `devstack site add ...` | Validate, register, and start a site |
| `devstack site list` | Show all sites, URLs, kinds, and status |
| `devstack site show <name>` | Show one site as a table or `--json` |
| `devstack site start <name>` | Start a stopped site |
| `devstack site stop <name>` | Stop a site and remember it should stay stopped |
| `devstack site restart <name>` | Stop then start a site |
| `devstack site remove <name>` | Unregister and stop a site; project files stay |
| `devstack site logs <name> [--tail N] [--follow]` | Read or stream site output |
| `devstack site open <name>` | Open the site URL in the default browser |
| `devstack site node <name> [<version>|--clear]` | Show, pin, or clear the site's Node toolchain |
| `devstack node default [<version>|--status|--clear]` | Manage the machine-wide Node CLI default |
| `devstack runtime available [--kind node] [--major N] [--all] [--refresh] [--offline]` | List official Node releases and other catalog builds |

`--json` is available on the management commands and prints JSON on stdout.
`site logs --json` emits one JSON object per line rather than a single document;
`daemon run` ignores the flag because it is a foreground service. The dashboard
at <https://devstack.test> exposes the same lifecycle actions.

A stopped site keeps its desired state as stopped, so a daemon restart does not
start it. Running sites are restarted automatically and reconciled after a
daemon restart.

## Options for `site add`

| Flag | Meaning |
|---|---|
| `--name` | Site name; the domain is `<name>.test` |
| `--path` | Absolute project directory |
| `--kind` | `static` (default), `php`, or `node` |
| `--php` | PHP version line, for example `8.4` (PHP sites only) |
| `--php-build` | Exact PHP build id, for example `php@8.4.27-r1` (PHP sites only) |
| `--front-controller` | Route non-file requests to `index.php` (PHP sites only) |
| `--node` | Exact installed Node version or line (`22`, `22.11`); optional for static/PHP |
| `--source-mode` | `windows` or `wsl`; Node/PHP default to WSL on Windows |
| `--command` | Start command (Node sites only) |
| `--document-root` | Document root relative to `--path`, or absolute within `--path` |
| `--www` | Also serve `www.<name>.test` |
| `--no-https` | Serve HTTP only |
| `--force-https` | Redirect HTTP to HTTPS |
| `--spa` | Serve `index.html` for non-file paths |
| `--env KEY=VALUE` | Site environment variable; repeatable |
| `--yes` | Install a missing PHP or Node runtime without prompting |

Use `devstack site node` to change the Node pin. Other flags are not exposed as
a separate update command; remove and re-add the site if you need different
options. Removing never deletes project files.

## Add a PHP site

PHP sites run on per-site `php-fpm` masters. Specify the PHP line:

```sh
mkdir -p "$HOME/projects/mysite"
echo '<?php phpinfo();' > "$HOME/projects/mysite/index.php"

devstack site add \
  --name mysite \
  --kind php \
  --path "$HOME/projects/mysite" \
  --php 8.4 \
  --front-controller
```

Use `--php-build` to pin an exact build such as `php@8.4.27-r1`, or `--php` to
select the newest installed build on that line. A PHP document root must remain
inside `--path`; absolute paths outside the project are rejected. See
[PHP](php.md) for `php.ini` configuration and extension toggles.

## Domains and hosts

Site names become lowercase DNS labels; use letters, digits, and hyphens. Every
site gets a `*.test` domain, and the dashboard owns the reserved
`devstack.test` domain. Do not use `.local`, real public domains, or names with
slashes or whitespace.

The helper owns the block between the following markers in `/etc/hosts`:

```text
# BEGIN devstack
127.0.0.1 devstack.test
127.0.0.1 blog.test
# END devstack
```

Do not edit inside the block. `devstack init` and `devstack doctor --repair`
rebuild it from the database. Malformed markers require a manual fix before
repair can proceed.

## Logs and troubleshooting

```sh
devstack site logs blog --tail 100
devstack site logs api --follow
devstack doctor
```

Site logs live under `~/.devstack/logs/<site-id>/current.log`. The daemon runs
under the user unit, so its output goes to `journalctl --user -u
devstack-daemon`; `~/.devstack/run/daemon.log` is used only by the detached
fallback. The helper log is in `journalctl -u devstack-helper`.

If a site says running but the URL fails:

1. Run `devstack doctor`.
2. Check `devstack site show <name>` for the allocated backend port and status.
3. Check `devstack site logs <name>` for a backend crash.
4. Restart the daemon once (`devstack daemon restart`). It synchronizes the
   helper's `proxy_*` targets in `/etc/devstack/helper.yaml` and reconciles the
   privileged forwarders.

## Limitations

- PHP sites need an installed PHP build from the runtime catalog (see
  [PHP](php.md)).
- An absolute `--document-root` must resolve inside the site's `--path`;
  symlink escapes and outside paths are rejected.
- Builtin serving is the default; optional native nginx/Apache vhosts are
  available for static, Node, and PHP sites.
- One backend process per site; multi-process Node clusters are not managed.
- Site updates are remove-and-re-add; project files are always preserved.
- The dashboard and all site domains use the local CA; there is no per-site
  public certificate support.
