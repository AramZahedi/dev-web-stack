# devstack

**A local web development stack for Windows and Linux.** devstack gives you a
complete, batteries-included environment for static, Node.js, and PHP projects
at trusted `https://<name>.test` URLs, managed by a Go CLI and a local
dashboard.

> [!IMPORTANT]
> **devstack is for local development only.** It is not a hosting panel, not a
> reverse proxy for public websites, and not hardened for internet-facing
> traffic. Keep it on your machine and do not expose its ports or dashboard to
> the internet.

> [!NOTE]
> **Current release:** `v0.5.0` (2026-11-03).
>
> - **Windows 10 22H2+ / Windows 11 x64 with WSL2:** fully implemented and
>   live-verified. Includes a standalone installer, native Windows helper
>   service, user daemon, managed Debian 12 WSL2 distribution, Docker
>   nginx/Apache switching, PHP/Node runtimes, and cross-platform tuning.
> - **Linux x86_64 with systemd:** implemented and released. Includes the
>   built-in proxy, native vhosts, managed Docker services, PHP/Node runtimes,
>   and systemd helper/daemon units.
> - **PHP 8.2–8.5** and **Node LTS** runtimes are installable from the embedded
>   catalog with side-by-side execution.
> - **Managed services:** nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis, and
>   phpMyAdmin.
>
> Release downloads: <https://github.com/AramZahedi/dev-web-stack/releases/tag/v0.5.0>
>
> The release binaries and installers are **unsigned**. Windows SmartScreen may
> show an unknown-publisher warning. Verify the SHA-256 checksum before running
> anything.

---

## Contents

- [What devstack does](#what-devstack-does)
- [Supported platforms](#supported-platforms)
- [Requirements](#requirements)
- [Install on Windows](#install-on-windows)
- [Install on Linux](#install-on-linux)
- [Build from source](#build-from-source)
- [First run and verification](#first-run-and-verification)
- [Your first site](#your-first-site)
- [Core concepts](#core-concepts)
- [Dashboard guide](#dashboard-guide)
- [CLI reference](#cli-reference)
- [Common workflows](#common-workflows)
- [Configuration and data locations](#configuration-and-data-locations)
- [TLS, certificates, and hosts](#tls-certificates-and-hosts)
- [Upgrades and rollback](#upgrades-and-rollback)
- [Uninstall](#uninstall)
- [Troubleshooting](#troubleshooting)
- [Security model and local-only scope](#security-model-and-local-only-scope)
- [Development and release](#development-and-release)
- [Documentation](#documentation)
- [Status and known limitations](#status-and-known-limitations)
- [License](#license)

---

## What devstack does

devstack replaces the “run five terminals and remember eight ports” part of
local web development with one consistent workflow:

- **Trusted local HTTPS** for every project at `https://<name>.test` — no
  browser certificate warnings.
- **Static, Node.js, and PHP sites** managed from one CLI and dashboard.
- **Per-site PHP-FPM masters** with per-site `php.ini`, extension, OPcache, and
  FPM pool settings.
- **Per-site Node toolchains** with exact version pins, a machine-wide default,
  and `node`/`npm`/`npx` available inside site shells and the terminal.
- **Managed Docker services**: nginx, Apache, MySQL, MariaDB, PostgreSQL,
  Redis, and phpMyAdmin.
- **Website server switching** between built-in, nginx, and Apache — including
  automatic switching and automatic fallback to built-in when the active server
  is deactivated or uninstalled.
- **Performance profiles** (Balanced, Throughput, Custom) for service
  containers, WSL, the WSL VM, and Windows host tuning.
- **Database tooling**: databases, users, grants, site links, dumps, imports,
  backups, restores, and credentials rotation.
- **Local development tools**: diagnostics, trusted CA repair, site shells,
  command execution with the site environment, logs, and a browser terminal.
- **A local dashboard** at `https://devstack.test` for the same operations.

## Supported platforms

| Platform | Status | Notes |
|---|---|---|
| Windows 10 22H2 (build 19045+) / Windows 11 x64 + WSL2 | Fully supported | Windows source mode and WSL source mode; managed Debian 12 WSL2 distro; Docker services; managed installer. |
| Linux x86_64 with systemd as PID 1 | Supported | Built-in proxy and native or Docker web serving; helper + user daemon services. |
| macOS | Not supported | No release artifact or supported runtime. |
| Windows on ARM | Not supported | The Windows release is x64 only. |
| Linux arm64 | Not supported | No release artifact. |

Windows requires **NAT networking** for WSL2. `networkingMode=mirrored` is not
supported because devstack depends on WSL localhost forwarding.

## Requirements

### Windows

| Requirement | Minimum |
|---|---|
| Operating system | Windows 10 22H2 build 19045+ or Windows 11, x64 |
| Virtualization | Intel VT-x or AMD-V/SVM enabled in BIOS/UEFI |
| WSL | WSL 2.0.0 or newer (`wsl --version`) |
| WSL networking | NAT (the default) |
| Disk | About 10 GB free for Windows binaries, the WSL disk, and Docker images |
| Privileges | Administrator for installation and upgrades; normal user for daily work |
| Ports | 80 and 443 free on `127.0.0.1`; IIS/W3SVC must not own them |

### Linux

| Requirement | Minimum |
|---|---|
| Operating system | Linux x86_64 with systemd as PID 1 |
| Privileges | Normal user for `install.sh`; `sudo` is used for the privileged helper |
| Ports | 80 and 443 free |
| Runtime for release installs | `bash`, `tar`, `sha256sum` or `shasum`, `curl`, `sudo` |
| Runtime for source builds | Go 1.26+, Node.js 22 / npm 10, `make`, `git` |
| Browser trust (optional) | NSS tools (`nss-tools` on Fedora/RHEL, `libnss3-tools` on Debian/Ubuntu) for Chrome/Firefox stores |

---

## Install on Windows

### Option A — Standalone installer (recommended)

Download from the [v0.5.0 release](https://github.com/AramZahedi/dev-web-stack/releases/tag/v0.5.0):

- `devstack-installer.exe` (or the identical `devstack-setup.exe`)
- `checksums.txt`

Verify the checksum in PowerShell:

```powershell
$expected = (Get-Content .\checksums.txt | Select-String 'devstack-installer.exe').Line.Split()[0]
$actual = (Get-FileHash .\devstack-installer.exe -Algorithm SHA256).Hash.ToLower()
if ($actual -ne $expected) { throw 'Checksum mismatch!' }
```

Run it by double-clicking, or from a terminal:

```powershell
.\devstack-installer.exe
```

The installer requests Administrator elevation through UAC, then:

1. verifies the embedded payload,
2. installs `devstack.exe` and the `DevstackHelper` Windows service,
3. creates `%USERPROFILE%\.devstack`,
4. imports the managed Debian 12 WSL2 distribution,
5. starts Docker and the WSL agent,
6. installs the helper/daemon startup tasks,
7. regenerates the local CA, hosts block, and browser trust,
8. runs the agent health check.

Installer flags:

| Flag | Meaning |
|---|---|
| `-y`, `--unattended` | No prompts; do not pause on exit |
| `--install-dir <path>` | Custom install root (default `%USERPROFILE%\.devstack`) |
| `--dry-run` | Validate the environment without changing the machine |
| `--allow-reboot` | Allow a reboot when optional Windows features require it |
| `--skip-wsl` | Install only the Windows CLI/helper; skip the WSL distro |
| `--upgrade` | Upgrade an existing installation with rollback support |
| `--uninstall` | Launch uninstallation |

### Option B — Release ZIP

1. Download `devstack_0.5.0_windows_amd64.zip` and `checksums.txt`.
2. Verify the archive:

   ```powershell
   $expected = (Get-Content .\checksums.txt | Select-String 'devstack_0.5.0_windows_amd64.zip').Line.Split()[0]
   $actual = (Get-FileHash .\devstack_0.5.0_windows_amd64.zip -Algorithm SHA256).Hash.ToLower()
   if ($actual -ne $expected) { throw 'Checksum mismatch!' }
   ```

3. Extract and install from an **elevated** PowerShell:

   ```powershell
   Expand-Archive .\devstack_0.5.0_windows_amd64.zip -DestinationPath .\devstack-installer
   cd .\devstack-installer
   .\install.ps1
   ```

`install.ps1` parameters:

| Parameter | Meaning |
|---|---|
| `-InstallDir <path>` | Install root |
| `-RootFSPath <path>` | Explicit WSL rootfs archive |
| `-AllowReboot` | Allow a reboot if Windows features require it |
| `-SkipWSL` | Skip the WSL distro/agent |
| `-DryRun` | Simulate without writing |
| `-Upgrade` | Upgrade an existing install |
| `-TestSandbox <path>` | Isolated test install |

### The `C:\devstack` navigation folder

On a standard install, devstack creates a real `C:\devstack` folder with four
links and a `README.txt`:

| Link | Target |
|---|---|
| `C:\devstack\Windows-Install` | `%USERPROFILE%\.devstack` |
| `C:\devstack\Windows-Projects` | `%USERPROFILE%\.devstack\projects` |
| `C:\devstack\WSL-Install` | `\\wsl.localhost\devstack\data\devstack` |
| `C:\devstack\WSL-Projects` | `\\wsl.localhost\devstack\data\devstack\projects` |

These links are navigation only. **They are not valid site paths.** Use the
canonical target path, or pick a folder in the dashboard browser.

## Install on Linux

### Option A — Published release

```sh
curl -fLO https://github.com/AramZahedi/dev-web-stack/releases/download/v0.5.0/devstack_0.5.0_linux_amd64.tar.gz
curl -fLO https://github.com/AramZahedi/dev-web-stack/releases/download/v0.5.0/checksums.txt
sha256sum -c checksums.txt
tar -xzf devstack_0.5.0_linux_amd64.tar.gz
cd devstack_0.5.0_linux_amd64
./install.sh
devstack init
```

Or let the installer download and verify it for you:

```sh
./install.sh --release 0.5.0
devstack init
```

Install from a local archive/checksum pair:

```sh
./install.sh \
  --archive devstack_0.5.0_linux_amd64.tar.gz \
  --checksums checksums.txt
```

Install from a mirror (HTTPS only):

```sh
./install.sh --release 0.5.0 --base-url https://artifacts.example.test/devstack
```

The installer runs as your normal user and uses `sudo` only for the helper
service, privileged forwarders, CA trust, and system paths. It verifies the
SHA-256, rejects unsafe archive members, checks the packaged version, and never
pipes a remote script into a shell.

### Option B — Build from source

```sh
make build
./install.sh
devstack init
```

## Build from source

### Windows

```powershell
git clone https://github.com/AramZahedi/dev-web-stack.git
cd dev-web-stack
make build-windows
make build-agent
.\install.ps1
```

Requirements: Go 1.26+, Node.js 22 / npm 10, Git, and an elevated PowerShell for
the install step.

### Linux

```sh
git clone https://github.com/AramZahedi/dev-web-stack.git
cd dev-web-stack
make build
./install.sh
devstack init
```

`make build` rebuilds `ui/dist` first, then writes `dist/devstack` and
`dist/devstack-helper`. The dashboard is embedded in the daemon binary.

## First run and verification

After installation, open a **new** terminal so the updated `PATH` is loaded.

```powershell
# Windows
devstack version
devstack doctor
Get-Service DevstackHelper
```

```sh
# Linux
devstack version
devstack doctor
systemctl status devstack-helper
```

`devstack doctor` checks the helper, local CA, hosts block, ports, runtimes,
WSL distro, WSL agent, Docker engine, localhost forwarding, serving mode,
vhost configuration, PHP builds, PHP masters, and disk space.

Start the daemon if it is not already running:

```powershell
# Windows
devstack daemon start
```

```sh
# Linux
devstack daemon start
```

Open the dashboard:

<https://devstack.test>

The first `devstack init` (or the Windows installer) creates a local CA, adds it
to the OS trust store, updates browser trust stores when possible, and writes the
managed `devstack.test` hosts entry.

---

## Your first site

### Static site

```sh
mkdir -p "$HOME/.devstack/projects/blog"
cat >"$HOME/.devstack/projects/blog/index.html" <<'HTML'
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Blog</title></head>
<body><h1>Hello from devstack</h1></body>
</html>
HTML

devstack site add --name blog --path "$HOME/.devstack/projects/blog"
```

Open <https://blog.test>.

Useful variants:

```sh
devstack site add --name docs --path "$HOME/.devstack/projects/docs" --document-root public
devstack site add --name app --path "$HOME/.devstack/projects/app" --document-root dist --spa
```

### Node.js site

Node sites receive an allocated `PORT` and must listen on loopback:

```js
// server.js
const http = require('node:http');
const port = Number(process.env.PORT);
http.createServer((req, res) => {
  res.end('Hello from Node');
}).listen(port, '127.0.0.1');
```

```sh
devstack site add \
  --name api \
  --kind node \
  --path "$HOME/.devstack/projects/api" \
  --node 22 \
  --command "node server.js"
```

Open <https://api.test>. Node's HMR WebSocket traffic is proxied by the same
route. Do not hard-code a port such as `PORT=3000`; read `process.env.PORT`.

### PHP site

```sh
mkdir -p "$HOME/.devstack/projects/mysite"
cat >"$HOME/.devstack/projects/mysite/index.php" <<'PHP'
<?php
header('Content-Type: text/plain');
echo 'Hello from PHP ', PHP_VERSION, "\n";
PHP

devstack site add \
  --name mysite \
  --kind php \
  --path "$HOME/.devstack/projects/mysite" \
  --php 8.4 \
  --front-controller
```

Open <https://mysite.test>. The PHP site runs on its own PHP-FPM master.

### Site options

| Flag | Meaning |
|---|---|
| `--name` | Site name; the domain becomes `<name>.test` |
| `--path` | Project directory; must be a strict descendant of the managed projects root |
| `--kind` | `static` (default), `php`, or `node` |
| `--php` | PHP line, for example `8.4` |
| `--php-build` | Exact PHP build, for example `php@8.4.27-r1` |
| `--node` | Node version or line (`22`, `22.11`, `22.11.0`) |
| `--command` | Node start command |
| `--source-mode` | `windows` or `wsl` on Windows; `local` on Linux |
| `--document-root` | Relative document root inside the project |
| `--www` | Also serve `www.<name>.test` |
| `--no-https` | HTTP only |
| `--force-https` | Redirect HTTP to HTTPS |
| `--spa` | Serve `index.html` for non-file paths |
| `--front-controller` | Route non-file PHP requests to `index.php` |
| `--env KEY=VALUE` | Site environment variable; repeatable |
| `--yes` | Install missing runtimes without prompting |

---

## Core concepts

### Sites and domains

A **site** is a project folder exposed at `https://<name>.test` (optionally
`www.<name>.test`). Site names are lowercase DNS labels using letters, digits,
and hyphens. Every site gets a certificate from the local CA and an entry in the
managed hosts block.

`devstack.test` is reserved for the dashboard, and `.test` domains are intended
to stay local.

### Source modes

A site’s source lives in exactly one managed root.

| Mode | Windows path | Linux path | Recommended for |
|---|---|---|---|
| `windows` | `%USERPROFILE%\.devstack\projects\<site>` (or configured root) | — | Projects edited by Windows tools that need native NTFS file watching |
| `wsl` | `\\wsl.localhost\devstack\data\devstack\projects\<site>` | — | Large PHP/Node projects that benefit from ext4 I/O |
| `local` | — | `~/.devstack/projects/<site>` (or configured root) | Native Linux development |

The managed projects root is a **hard boundary**. Paths outside it, the root
itself, `..` traversal, and symlinks that resolve outside are rejected. This is
enforced at create, source-copy, start, and reconcile time.

To copy a stopped site to another source mode:

```sh
devstack site source blog --mode wsl --path "\\wsl.localhost\devstack\data\devstack\projects\blog"
```

The copy is verified with a SHA-256 manifest and rolled back on failure. Source
files are retained by default; `--remove-source` only works when the staged
agent supports verified source removal.

### Serving modes and providers

devstack can serve sites through three paths:

| Mode | Provider | Where | How it works |
|---|---|---|---|
| `builtin` | `native` | Windows and Linux | The daemon owns public HTTP/HTTPS and routes directly to backends |
| `nginx` / `apache` | `docker` | Windows and Linux | Managed Docker web server listens privately and the daemon terminates public TLS |
| `nginx` / `apache` | `native` | Linux only | You installed and manage the host nginx/Apache; devstack writes vhosts |

On **Windows**, public 80/443 always belong to the daemon/helper. Managed
Docker nginx and Apache listen only on `127.0.0.1:18080` inside the WSL network,
and the daemon proxies site traffic to them.

Switching is automatic:

```sh
devstack vhost use nginx --yes
devstack vhost use apache --yes
devstack vhost use builtin --yes
```

- Switching directly between nginx and Apache stops the old container,
  activates the target, and rolls back to the old server on failure.
- Deactivating or uninstalling the active web service automatically returns
  serving to `builtin` before the container stops.
- The dashboard disables nginx/Apache choices when that service is not
  installed.
- New or changed site vhosts are validated and gracefully reloaded without
  restarting the web container.

Useful commands:

```sh
devstack vhost status
devstack vhost path
devstack vhost preview blog --server nginx
devstack vhost sync
```

### Runtimes

Runtimes are installed side by side. A site can pin an exact build; otherwise
PHP uses the newest installed build on the requested line, and static/PHP sites
inherit the global Node default.

```sh
devstack runtime list
devstack runtime available --kind node
devstack runtime install php@8.4.27-r1
devstack runtime install node@22
devstack runtime migrate 8.3 --to 8.4
devstack php default --status
devstack node default --status
```

Inside `devstack shell`, `devstack exec`, and the dashboard terminal,
`php`, `node`, `npm`, and `npx` resolve to the site’s runtime. On Windows, the
same tools are available as `.cmd` shims in `%USERPROFILE%\.devstack\bin`.

### Services

Services are optional Docker containers managed by devstack. They are not
required for a static site, but they provide the usual local infrastructure.

| Service | Group | Default endpoint |
|---|---|---|
| nginx | web | private 18080 (Docker) |
| Apache | web | private 18080 (Docker) |
| MySQL 8.4 | MySQL family | loopback 3306 |
| MariaDB 11.8 | MySQL family | loopback 3306 |
| PostgreSQL 18 | standalone | loopback 5432 |
| Redis 8.8 | standalone | loopback 6379 |
| phpMyAdmin 5.2 | standalone | loopback 8082, opened with a signed one-time dashboard signon |

Only one web server and one MySQL-family engine can be active at a time.
Database, Redis, and phpMyAdmin ports bind loopback only. Web containers use
host networking on Linux and the private port on Windows.

### Databases

Database workflows are available from the dashboard or the CLI:

```sh
devstack db create --service mysql --name app
devstack db list --service mysql
devstack db export --service mysql --database app --output ./app.sql
devstack db import --service mysql --database app --file ./app.sql
devstack db backup create --service mysql --database app
devstack db backup list --service mysql
devstack db backup restore --service mysql --backup-id <backup-id> --yes
```

Users, grants, passwords, and site links:

```sh
devstack db user create --service mysql --name app
devstack db user list --service mysql
devstack db user grant --service mysql --user-id <user-id> --database app --preset read_write_schema
devstack db user password --service mysql --user-id <user-id> --reveal
devstack db link --service mysql --database app --user-id <user-id> --site blog --primary
devstack site db list blog
devstack site db unlink blog <link-id> --yes
```

Credentials are generated locally and are never printed unless you explicitly
reveal or rotate them:

```sh
devstack service credentials mysql --reveal
devstack service credentials mysql --rotate
```

### TLS and hosts

devstack creates an offline local root CA. The helper alone can mint leaf
certificates. `devstack init`, `devstack doctor --repair`, and the Windows
installer install the root into:

- the Windows Local Machine Root store,
- Mozilla Firefox NSS databases when available,
- the managed WSL Debian CA store.

The managed hosts block looks like:

```text
# BEGIN devstack
127.0.0.1 devstack.test
127.0.0.1 blog.test
# END devstack
```

Do not edit inside the block. Repair it with:

```sh
devstack trust status
devstack trust repair
devstack doctor --repair
```

---

## Dashboard guide

Open <https://devstack.test>. The dashboard is served by the local daemon and
is restricted to loopback access.

| Area | What you can do |
|---|---|
| **Overview** | Daemon health, serving mode, site counts, recent activity, quick actions |
| **Sites** | Add, start, stop, restart, remove, open, inspect, and search sites |
| **Site detail** | Domain, source mode, status, runtime pins, logs, database links, terminal, PHP settings, builds |
| **Add Site** | Choose static/Node/PHP, Windows/WSL/local source, folder, document root, SPA/front controller, TLS, www alias, runtime, environment, database provisioning |
| **Settings** | HTTP/HTTPS/dashboard ports, serving mode, default runtimes, global PHP ini, trust repair, tooling |
| **Services** | Install, activate, deactivate, restart, uninstall, inspect status, logs, config, credentials |
| **Service configuration** | Browse/diff/apply/reset service config files with conflict protection |
| **Performance** | Preview/apply Balanced, Throughput, or Custom; host tuning; reset; benchmark history |
| **phpMyAdmin** | One-time signed signon to the active MySQL/MariaDB service |
| **Terminal** | Per-site shell with the site runtime environment; local-only, audited |
| **Trust / Diagnostics** | CA status, browser trust repair, doctor output, restart-required notices |

The dashboard uses a local session cookie and CSRF protection. Optional token
authentication can be enabled with `dashboard_auth: true` or:

```sh
devstack init --dashboard-auth
```

---

## CLI reference

Global flags work on every command:

| Flag | Meaning |
|---|---|
| `--home <path>` | Use a different devstack home directory |
| `--json` | Emit machine-readable JSON on stdout |
| `--yes` | Assume yes for confirmation prompts |

### Daemon

```sh
devstack daemon status
devstack daemon start
devstack daemon stop
devstack daemon restart
devstack daemon run
```

`daemon run` runs in the foreground and is intended for debugging.

### Sites

```sh
devstack site add ...
devstack site list
devstack site show blog
devstack site open blog
devstack site start blog
devstack site stop blog
devstack site restart blog
devstack site remove blog --yes
devstack site logs blog --tail 100 --follow
devstack site build blog
```

PHP-specific:

```sh
devstack site php blog 8.4
devstack site php blog --build php@8.5.12-r1
devstack site php blog --list
devstack site ini blog --set memory_limit=512M --set display_errors=1
devstack site ini blog --unset display_errors --apply
devstack site ext blog --enable imagick --disable xdebug
devstack site ext blog --inherit imagick
```

Node-specific:

```sh
devstack site node blog 22.11.0
devstack site node blog --clear
devstack node default 22.11.0
devstack node default --status
devstack node default --clear
```

### Shells and execution

```sh
devstack shell --site blog
devstack exec --site blog -- php artisan migrate
devstack exec --site blog -- npm run build
devstack shell-init --site blog
devstack php --site blog -- -v
```

`--site` may be omitted when the current directory is inside a registered site.

### Runtimes

```sh
devstack runtime list
devstack runtime available
devstack runtime available --kind node --all --refresh
devstack runtime install php@8.4.27-r1
devstack runtime install node@22
devstack runtime migrate 8.3 --to 8.4
devstack runtime remove php@8.3.36-r1 --yes
```

### Services

```sh
devstack service list
devstack service status apache
devstack service install apache
devstack service activate apache
devstack service deactivate apache
devstack service restart apache
devstack service uninstall apache --yes
devstack service uninstall apache --yes --delete-data
devstack service logs apache --tail 200
devstack service credentials mysql --reveal
devstack service credentials mysql --rotate
```

Configuration:

```sh
devstack service config list apache
devstack service config show apache httpd.conf
devstack service config diff apache httpd.conf --file .\httpd.conf
devstack service config apply apache httpd.conf --file .\httpd.conf --yes
devstack service config reset apache httpd.conf --yes
```

Uninstall keeps service data by default. `--delete-data` creates and verifies a
backup before deleting service data.

### Databases

```sh
devstack db list --service mysql
devstack db create --service mysql --name app
devstack db rename --service postgres --name app --to app2
devstack db drop --service mysql --name app --backup-first --yes
devstack db export --service mysql --database app --output .\app.sql
devstack db import --service mysql --database app --file .\app.sql
devstack db password --service redis --reveal
devstack db password --service redis --rotate
```

Backups:

```sh
devstack db backup create --service mysql --database app
devstack db backup list --service mysql
devstack db backup download --service mysql --backup-id <backup-id> --output .\app-backup.sql
devstack db backup restore --service mysql --backup-id <backup-id> --yes
devstack db backup delete --service mysql --backup-id <backup-id> --yes
```

Users and links:

```sh
devstack db user create --service mysql --name app
devstack db user list --service mysql
devstack db user grant --service mysql --user-id <user-id> --database app --preset read_write_schema
devstack db user password --service mysql --user-id <user-id> --reveal
devstack db user password --service mysql --user-id <user-id> --rotate
devstack db link --service mysql --database app --user-id <user-id> --site blog --primary
devstack site db list blog
devstack site db unlink blog <link-id> --yes
```

Database link passwords are process environment only; they are not written to
site configuration or logs.

### Serving and vhosts

```sh
devstack vhost status
devstack vhost path
devstack vhost preview blog --server nginx
devstack vhost sync
devstack vhost use nginx --yes
devstack vhost use apache --yes
devstack vhost use builtin --yes
```

### Trust and diagnostics

```sh
devstack trust status
devstack trust repair
devstack trust remove --yes
devstack doctor
devstack doctor --repair
devstack doctor --json
```

### Tools

```sh
devstack tool list
devstack tool install composer
devstack tool update composer
devstack tool remove composer --yes
```

### Dashboard and version

```sh
devstack ui
devstack version
devstack version --json
```

---

## Common workflows

### Switch from built-in to nginx or Apache

```sh
devstack service install apache --yes
devstack service activate apache --yes
devstack vhost use apache --yes
devstack vhost status
```

Switch directly back and forth without a built-in round trip:

```sh
devstack vhost use nginx --yes
devstack vhost use apache --yes
```

If the active web service is deactivated or uninstalled, devstack automatically
falls back to built-in and keeps sites available.

### Use a local Laravel project

```sh
devstack site add \
  --name laravel \
  --kind php \
  --path "\\wsl.localhost\devstack\data\devstack\projects\laravel" \
  --source-mode wsl \
  --php 8.5 \
  --front-controller \
  --database-service mysql \
  --database-new laravel \
  --database-user-new laravel
```

Then:

```sh
devstack shell --site laravel
php artisan migrate
npm run dev
```

### Serve a built SPA

```sh
devstack site add \
  --name dashboard \
  --path "$HOME/.devstack/projects/dashboard" \
  --document-root dist \
  --spa
```

Run the production build with `devstack exec --site dashboard -- npm run build`,
then open the site.

### Use a site-specific database

```sh
devstack site add --name blog --kind php --path "$HOME/.devstack/projects/blog" --php 8.4 --database-service mysql --database-new blog --database-user-new blog
devstack site db list blog
```

The database link exports connection values into the site environment. Site
configuration stores the link, not the password.

### Tune performance

Open **Performance** in the dashboard, or use the API/CLI surfaces behind it:

1. Choose **Balanced**, **Throughput**, or **Custom**.
2. Preview the generated nginx/Apache/PHP values and host-file diff.
3. Apply with host tuning if you want machine-wide changes.
4. Reset to restore the previous host files and managed includes.

Tuning can target:

- managed service containers,
- WSL sysctl and limits,
- WSL VM memory/processors/swap through `.wslconfig`,
- Windows power plan, TCP settings, registry, NTFS, and optional Defender
  exclusions.

Host tuning is global and `.wslconfig` changes require a WSL restart.

### Inspect logs

```sh
devstack site logs blog --tail 200
devstack site logs blog --follow
devstack service logs apache --tail 200
```

Windows daemon and helper logs live under `%USERPROFILE%\.devstack\logs` and
`%USERPROFILE%\.devstack\system`. Linux logs live under the helper/daemon
service paths and `~/.devstack/logs` as applicable.

### Repair a site that will not start

```sh
devstack doctor
devstack doctor --repair
devstack site show blog --json
devstack site logs blog --tail 200
```

Common causes are a missing project folder, a path outside the managed projects
root, a port conflict, a stopped runtime, or a stopped service.

---

## Configuration and data locations

### Windows

```text
%USERPROFILE%\.devstack\
  bin\                       devstack.exe, devstack-helper.exe, shims
  config.yaml                daemon configuration
  state.db                   SQLite state
  pki\                       site/dashboard certificates
  logs\                      daemon/log files
  system\                    helper config, CA, DPAPI secrets, manifest
  projects\                  default Windows-mode projects root
  wsl\devstack\ext4.vhdx     managed WSL2 disk

\\wsl.localhost\devstack\data\devstack\
  bin\                       devstack-agent
  system\                    agent TLS, token, pin
  docker\                    Docker data-root
  services\                  service config/data/logs
  sites\                     generated PHP-FPM configuration
  projects\                  default WSL-mode projects root
```

### Linux

```text
~/.devstack\
  bin\
  config.yaml
  state.db
  pki\
  logs\
  projects\                  default local projects root

/usr/local/lib/devstack\     helper installation
/etc/devstack/helper.yaml    helper configuration
```

`projects_root` can be changed in `config.yaml`. The configured root remains the
hard boundary for new sites.

### Main configuration

```yaml
http_port: 8080
https_port: 8443
ui_port: 7070
agent_port: 7443

wsl_distro: devstack
projects_root: C:\Users\you\.devstack\projects
auto_start_sites: true
dashboard_auth: false

serving_mode: builtin
serving_provider: native

graceful_stop_seconds: 10
max_request_body_mb: 1024
log_max_mb: 10
log_keep: 5
```

Changing listener ports requires a daemon restart. The dashboard reports
`restart_required` until then.

---

## TLS, certificates, and hosts

- The CA private key is stored under the protected system directory and is
  accessible only to the helper/administrators.
- Site certificates are minted automatically when a site is added; the daemon
  terminates public TLS.
- Hosts entries are managed between `# BEGIN devstack` and `# END devstack`.
- Browser trust can drift after browser updates. Repair with
  `devstack trust repair` and restart the browser.
- For command-line clients that do not use the OS trust store, point them at the
  CA bundle or use the site shell, which sets the relevant environment.
- Do not expose the local CA private key or the helper pipe to untrusted users.

---

## Upgrades and rollback

### Windows

```powershell
Expand-Archive .\devstack_0.5.0_windows_amd64.zip -DestinationPath .\upgrade
cd .\upgrade
.\install.ps1 -Upgrade
```

The upgrade:

1. verifies an existing installation,
2. stops the daemon and helper,
3. backs up binaries and the WSL `/data/devstack` state,
4. stages new binaries and the agent,
5. health-checks the agent,
6. restarts the daemon,
7. rolls back binaries and WSL state if any step fails.

Always keep the previous installer/archive until the new version passes
`devstack doctor`.

### Linux

```sh
./install.sh --upgrade --release 0.5.0
devstack doctor
```

Database migrations take a SQLite snapshot before the first pending migration.
Downgrades are unsupported; restore from a backup if needed.

## Uninstall

### Windows

Run from an elevated PowerShell:

```powershell
cd "$env:USERPROFILE\.devstack"
.\uninstall.ps1
```

The default uninstall removes the helper service, daemon startup task, hosts
block, trust, firewall rule, and installed files. It preserves the WSL distro
and user projects unless purge flags are passed. Review the script's output and
keep backups of anything you need.

### Linux

```sh
devstack uninstall --yes
```

This removes the integration, trust, hosts entries, and helper forwarders. It
does not delete project files.

---

## Troubleshooting

Start with:

```sh
devstack doctor
devstack doctor --json
devstack doctor --repair
```

### Port 80 or 443 is already in use

```powershell
Get-NetTCPConnection -LocalPort 80,443 | Select-Object LocalPort,OwningProcess
Get-Process -Id (Get-NetTCPConnection -LocalPort 80).OwningProcess
```

Stop or reassign IIS/W3SVC, SQL Server Reporting Services, or any other listener,
then restart devstack.

### WSL will not start or localhost forwarding fails

```powershell
wsl --version
wsl --update
wsl --shutdown
devstack daemon restart
```

Check `%USERPROFILE%\.wslconfig` for `localhostForwarding=false` or
`networkingMode=mirrored`; remove those settings. VPN and endpoint-protection
software can also block WSL forwarding.

### Browser shows a certificate warning

```sh
devstack trust repair
devstack doctor
```

Close and reopen the browser. For Firefox, ensure enterprise roots are enabled
or run the trust repair again.

### A site is unreachable

```sh
devstack site show blog --json
devstack site logs blog --tail 200
devstack doctor
```

Check that:

- the site is running,
- its source path still exists under the managed projects root,
- its runtime is installed,
- its database/service dependency is active,
- the active serving mode is healthy.

### A service will not activate

```sh
devstack service status nginx
devstack service logs nginx --tail 200
devstack service list
```

Only one web server and one MySQL-family engine may be active at a time. Stop
the conflicting service first, or let devstack perform the automatic switch.

### WSL `.wslconfig` changes require a restart

```powershell
wsl --shutdown
devstack daemon restart
```

The Windows tuning UI reports `restart_required` until this happens.

---

## Security model and local-only scope

devstack is intentionally a **local development tool**:

- The dashboard and control API bind loopback.
- The helper and daemon pipes are ACL-restricted to the installing user/SID.
- The dashboard uses a session cookie and CSRF protection; optional token auth
  is available.
- Database, Redis, and phpMyAdmin ports bind loopback.
- The managed WSL distro runs as root and is a full-power local Linux
  environment. Treat anything running in a site as trusted local code.
- The local CA can mint certificates trusted by your machine. Protect the
  helper/system directories and do not share them.
- phpMyAdmin opens through a short-lived, single-use signed dashboard signon and
  is not an unauthenticated public endpoint.
- Release artifacts are unsigned. Verify SHA-256 checksums and do not trust an
  archive from an untrusted mirror.
- **Do not port-forward, reverse-proxy, or expose 80/443, 8080, 8443, 7070, or
  the service ports to the internet.** Use a proper production platform for
  public sites.

### Windows specifics

The installer creates a scoped inbound firewall rule for the WSL subnet on the
daemon proxy ports. It does not make the dashboard public.

### Linux specifics

The helper runs as root; the user daemon does not. The installer uses `sudo`
only for privileged setup and forwarder management.

---

## Development and release

```sh
make build       # UI + dist/devstack + dist/devstack-helper
make test        # go test ./...
make lint        # golangci-lint
make ui          # UI only (npm ci && npm run build)
make e2e         # Linux end-to-end
make acceptance  # full unprivileged Linux acceptance
make release-test
make release-windows-test
```

Useful checks:

```sh
go test ./...
go vet ./...
go test ./... -race
cd ui && npm run check && npm run test -- --run && npm run build
```

Release packaging:

```sh
# Linux
bash build/release.sh --version 0.5.0 --skip-ui

# Windows
bash build/release-windows.sh \
  --version 0.5.0 \
  --skip-ui \
  --rootfs build/wsl/debian-12-wsl-rootfs.tar.gz
```

The release workflow is manual-dispatch only, and GoReleaser publishes the
Linux archive. The Windows zip, Windows tarball, and installers are produced by
`build/release-windows.sh` and attached to the GitHub release manually. See
[docs/release.md](docs/release.md) for the complete release checklist, checksums,
and the Windows packaging steps.

### Dashboard development

```sh
cd ui
npm ci
npm run dev          # http://127.0.0.1:5173 with the mock API
```

Set `VITE_API_MODE=api` and `DEVSTACK_API_ORIGIN` (default
`http://127.0.0.1:7070`) to proxy to a running daemon.

---

## Documentation

| Guide | Contents |
|---|---|
| [Documentation index](docs/README.md) | Map of user guides, architecture, and historical plans |
| [Windows install](docs/install-windows.md) | Detailed Windows installation, layout, upgrades, uninstall |
| [Linux install](docs/install.md) | Detailed Linux installation, release archives, systemd |
| [Upgrade](docs/upgrade.md) | Upgrade preflight, database backup, recovery, rollback limits |
| [Release](docs/release.md) | Tag, packaging, checksums, GitHub release workflow, Windows release process |
| [Sites](docs/sites.md) | Static, Node, and PHP sites; lifecycle; domains; logs |
| [PHP](docs/php.md) | PHP builds, per-site ini/extensions, FPM, migration |
| [Vhosts](docs/vhosts.md) | Native nginx/Apache, mode switching, preview, TLS custody |
| [Services](docs/services.md) | Service lifecycle, config editor, performance, benchmarks, terminal |
| [TLS and trust](docs/tls.md) | Local CA, browser trust, repair, curl/Node usage |
| [WSL](docs/wsl.md) | Managed Debian distro, systemd, Docker Engine, agent |
| [Windows paths](docs/windows-paths.md) | Source modes, UNC translation, shims, no-dot policy |
| [Windows networking](docs/windows-networking.md) | Ports, firewall, WSL ingress, trusted HTTPS |
| [Troubleshooting](docs/troubleshooting.md) | Common errors, doctor codes, repair steps |
| [Known issues](docs/known-issues.md) | Current limitations, deferred coverage, and open risks |
| [Architecture](docs/architecture.md) | Process model, package map, state, API, design decisions |
| [Development](docs/development.md) | Build/test/debug, package boundaries, migrations |
| [Requirements](docs/requirements.md) | Product requirements and traceability |
| [Decisions](docs/decisions.md) | Decision log and rejected alternatives |

Historical design and implementation records live under
[`docs/superpowers/`](docs/superpowers/).

---

## Status and known limitations

devstack v0.5.0 is ready for local development on the tested Windows/WSL2 and
Linux configurations. It is **not** a production web host.

Important current limitations:

- Windows VM-only installer rows and some Linux manual gates are not part of the
  automated matrix yet.
- Database E2E cycles 9–15 were paused during the latest acceptance campaign;
  database unit/integration tests and live dashboard flows pass, but the full
  matrix is not complete.
- Host tuning is global and opt-in. `.wslconfig` changes require a WSL restart.
  Defender exclusions are best-effort when the Defender module is unavailable.
- Releases are unsigned; Windows will show an unknown-publisher warning unless
  you build/sign your own.
- Automatic GitHub CI is disabled; workflows are manual-dispatch only.
- The Windows release is built manually; GoReleaser currently publishes Linux
  only.
- macOS and arm64 are not supported.
- No auto-update and no diagnostics bundle. `devstack doctor --json` is the
  support surface.

See [docs/known-issues.md](docs/known-issues.md) for the complete, current list.

---

## License

MIT. See [LICENSE](LICENSE).

Copyright (c) 2026 Aram Zahedi.
