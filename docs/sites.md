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

## Add a Node site

Node sites must expose an HTTP server on the port provided in `PORT` and bind
to loopback. devstack injects `PORT`, a runtime-specific `PATH`, and
`NODE_EXTRA_CA_CERTS` pointing at the machine CA bundle.

```sh
cd "$HOME/projects/api"
# package.json should provide a start/dev script, for example:
#   "dev": "node server.js"
# and server.js should call app.listen(process.env.PORT, "127.0.0.1").

devstack site add \
  --name api \
  --kind node \
  --path "$HOME/projects/api" \
  --node 22 \
  --command "npm run dev"
```

`--node` accepts a catalog prefix such as `22` or `24` (Plan 1 ships Node
22.23.4 and 24.22.1) or an exact version. If the runtime is not installed,
`site add` prompts to download it; pass `--yes` to install without prompting:

```sh
devstack site add --name api --kind node --path "$HOME/projects/api" \
  --node 24 --command "npm run dev" --yes
```

Node's HMR WebSocket traffic is proxied by the same host route. devstack sets
`PORT` after applying `--env` values, so a site environment entry named `PORT`
has no effect. Make the dev server read `process.env.PORT`; a command that
hard-codes another port (for example `PORT=3000 npm run dev`) bypasses the
allocated route and will not be reachable through devstack.

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
| `--kind` | `static` (default) or `node` |
| `--node` | Node version or catalog prefix (Node sites only) |
| `--command` | Start command (Node sites only) |
| `--document-root` | Static document root relative to `--path`, or absolute |
| `--www` | Also serve `www.<name>.test` |
| `--no-https` | Serve HTTP only |
| `--force-https` | Redirect HTTP to HTTPS |
| `--spa` | Serve `index.html` for non-file paths |
| `--env KEY=VALUE` | Site environment variable; repeatable |
| `--yes` | Install a missing Node runtime without prompting |

Changing a site's flags is not exposed as a separate Plan 1 command; remove and
re-add the site if you need different options. Removing never deletes project
files.

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
4. Confirm the daemon's proxy ports in `~/.devstack/config.yaml` match the
   helper's `proxy_*` values in `/etc/devstack/helper.yaml` exactly, then run
   `devstack init`. A mismatch makes `init` reject the forwarder configuration.

## Plan 1 limitations

- Static and Node sites only; PHP is a later milestone.
- Builtin serving is the default; optional native nginx/Apache vhosts are
  available for static and Node sites. PHP vhosts arrive in Plan 2.
- One backend process per site; multi-process Node clusters are not managed.
- Site updates are remove-and-re-add; project files are always preserved.
- The dashboard and all site domains use the local CA; there is no per-site
  public certificate support.
