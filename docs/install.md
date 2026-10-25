# Install devstack

devstack is a local web development stack for Linux. It serves static, Node, and
PHP sites at trusted `https://<name>.test` URLs and manages the daemon, helper,
hosts entries, and local certificate authority.

This document covers the Linux install. External nginx/Apache serving is
available. PHP 8.2–8.5 builds are published in the `v0.1.0` catalog (see
[PHP](php.md)). M1 is Linux-only; Windows/WSL (Plan 3) is deferred by user
request. Release archives are unsigned and checksum-verified. See
[Sites](sites.md), [Vhosts](vhosts.md), and
[TLS and trust](tls.md) for day-to-day use, and [Release](release.md) for the
packaging and publication path.

## Supported systems

- Linux x86_64 with systemd as PID 1.
- One installing user per machine. The helper accepts calls from that UID only.
- Built and tested on Fedora 44; CI runs on Ubuntu 24.04.

The helper installs to `/usr/local/lib/devstack` and runs as root. The daemon
runs as your user and never needs root.

## Prerequisites

To build from source:

- Go 1.26 or newer (the module declares Go 1.26).
- Node.js 22 and npm 10 for the dashboard.
- `make`, `curl`, `bash`, and coreutils.
- `golangci-lint` only for `make lint`.

To install a published release instead of building, you do not need Go or Node.
You need `bash`, `tar`, `sha256sum` (or `shasum`), `sudo`, and `curl` when you
use `--release`.

To trust the local CA in Chrome and Firefox, install NSS tools:

```sh
# Fedora / RHEL
sudo dnf install nss-tools

# Debian / Ubuntu
sudo apt install libnss3-tools
```

`devstack init` reports browser trust targets as "not installed" when
`certutil` is absent; the operating-system trust store is still updated. Install
the tools and run `devstack trust repair` afterwards.

Ports 80 and 443 must be free for the helper's forwarders. The daemon uses
loopback ports 8080 (HTTP), 8443 (HTTPS), and 7070 (dashboard) by default. If
those daemon ports are taken, edit `~/.devstack/config.yaml` before running
`devstack init`.

## Build

From the repository root:

```sh
make build
```

This builds the dashboard first, then writes `dist/devstack` and
`dist/devstack-helper`. The helper binary embeds no UI and does not need npm.

## Install

The installer must run as your normal user, not as root. It uses `sudo` for the
root-owned helper files and system unit.

There are three sources: the local `dist/` build (default), a local release
archive, and a release downloaded from GitHub.

```sh
# From this checkout after `make build`:
./install.sh

# From a published release (downloads over HTTPS):
./install.sh --release 0.1.0

# From a release archive you already downloaded:
./install.sh --archive devstack_0.1.0_linux_amd64.tar.gz --checksums checksums.txt

# From a mirror or CI artifact host (HTTPS only):
./install.sh --release 0.1.0 --base-url https://artifacts.example.test/devstack
```

`--release` downloads `devstack_<version>_linux_amd64.tar.gz` and
`checksums.txt` from
`https://github.com/AramZahedi/dev-web-stack/releases/download/v<version>/`.
`--base-url` replaces that root and must start with `https://`; there is no
plain-HTTP fallback. `--archive` and `--checksums` must be supplied together and
cannot be combined with `--release`.

`v0.1.0` is published, so `--release 0.1.0` downloads and verifies the public
archive. For local development you can still use the source install or a local
archive produced with `make release VERSION=<semver>`. Release archives are
**unsigned**; the SHA-256 manifest detects corruption and tampering in transit,
not a compromised release host.

Before any file is written, the installer:

1. Requires exactly one lowercase SHA-256 line for the archive in
   `checksums.txt` and compares it with the archive bytes.
2. Lists the archive and rejects absolute paths, `..` components, duplicate or
   unexpected members, and every non-regular member, including symlinks and
   hardlinks.
3. Extracts with `tar -xzf --no-same-owner --no-same-permissions` into a fresh
   temporary directory.
4. Checks that `devstack`, `devstack-helper`, `install.sh`, and `README.md` are
   present and that `devstack version --json` reports the requested release.
   A checksum or identity failure aborts before any `sudo` or `install`
   command.

The installer never pipes a remote script into a shell. The archive's
`install.sh` is verified as a member but is not executed. Run the installer from
your checkout, or use the copy in the archive with `--archive` and `--checksums`
pointing at the downloaded tarball and manifest.

It is safe to re-run and performs these steps:

| What | Where |
|---|---|
| CLI and daemon binary | `~/.local/bin/devstack` |
| Privileged helper binary | `/usr/local/lib/devstack/devstack-helper` |
| Helper configuration | `/etc/devstack/helper.yaml` |
| Helper system unit | `/etc/systemd/system/devstack-helper.service` |
| Per-user daemon unit | `${XDG_CONFIG_HOME:-~/.config}/systemd/user/devstack-daemon.service` |

The installer writes `allow_uid` in the helper config using your UID, installs
the helper unit, enables and restarts it, installs the user unit, and enables it
when a user systemd session is available. It reloads/restarts the
`devstack-daemon` user unit only when that session exists and the unit was
already active or this is an upgrade; without a session it prints how to start
the daemon later. An existing
`/etc/devstack/helper.yaml` is kept by default; pass `--force-config` to replace
it with the generated defaults, or `--keep-config` to make that behavior
explicit. The installer does not install packages, configure nginx/Apache, or
run `devstack init`.

Preview the steps without changing anything:

```sh
./install.sh --dry-run
```

`--dry-run` still checks the release checksum, member safety, extraction, and
packaged version; it only prints the install/service commands instead of
running them.

If `~/.local/bin` is not on `PATH`, add it in your shell profile:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

## First run

```sh
devstack init
```

`init` asks the helper for its CA, installs the CA in the operating-system and
browser trust stores, writes the managed hosts block, starts the daemon
(through the installed user unit when available), and runs the diagnostic
checks. In builtin mode it also re-applies the privileged 80/443 forwarders.
When `config.yaml` or the helper already selects nginx/Apache, `init` leaves the
forwarders off so the external server keeps 80/443, and asks the daemon to sync
the vhost set. It prints the dashboard URL and a loopback fallback.

Open <https://devstack.test>. If anything fails, run:

```sh
devstack doctor
```

`devstack doctor --repair` re-applies the safe parts of the install (trust,
hosts, runtime verification) without deleting data.

`devstack doctor --json` is the support snapshot to attach to a bug report. M1
does not produce a diagnostics zip/bundle, and there is no automatic update
check or self-update. Capture the JSON and the relevant service logs manually.

## Services and autostart

The helper is a system service:

```sh
sudo systemctl status devstack-helper
sudo systemctl restart devstack-helper
sudo journalctl -u devstack-helper -f
```

The daemon is a per-user unit. The installer enables it but intentionally does
not start it on first install; `devstack init` starts the unit when a user
systemd manager is available, so trust and hosts are ready first. If no user
manager is available (for example a headless SSH or container session), the CLI
falls back to the detached `devstack daemon run` process in the background.

```sh
systemctl --user status devstack-daemon
systemctl --user restart devstack-daemon
journalctl --user -u devstack-daemon -f
```

To keep the daemon running after you log out, enable lingering for your user:

```sh
sudo loginctl enable-linger "$USER"
```

The daemon can also be run in the foreground for debugging:

```sh
devstack daemon run --log-level debug
```

Do not run two daemons for the same home. `devstack daemon status` reports
whether the control socket is live. If `systemctl --user` is unavailable (no
`XDG_RUNTIME_DIR` or no user manager), the unit cannot be started; run the
foreground command under your own supervisor, or start the unit from a login
session.

## Configuration

`/etc/devstack/helper.yaml` is root-owned. The unit passes it with `--config`;
command-line flags override file values. The shipped example is
`build/helper.yaml.example`.

| Key | Default | Meaning |
|---|---|---|
| `socket` | `/run/devstack-helper/helper.sock` | Helper Unix socket |
| `state_dir` | `/var/lib/devstack-helper` | CA key material and helper state |
| `hosts` | `/etc/hosts` | Hosts file containing the managed block |
| `trust_dir` | auto-detect | CA anchor directory override |
| `allow_uid` | installer's UID | Only this user may call the helper |
| `forward_http_port` | `80` | Privileged HTTP forwarder |
| `forward_https_port` | `443` | Privileged HTTPS forwarder |
| `proxy_http_port` | `8080` | Daemon loopback HTTP proxy |
| `proxy_https_port` | `8443` | Daemon loopback HTTPS proxy |
| `auto_forward` | `true` | Bind 80/443 at helper startup unless a Docker web server marker disables it |
| `service_root` | devstack home | Host root for the Compose/service config/data tree; empty disables Docker service RPCs |
| `tool_root` | devstack home | Host root for managed PHP/Composer tools and shims |
| `tool_bin_dir` | `/usr/local/bin` | System bin directory receiving php/composer links |
| `log_level` | `info` | `debug`, `info`, `warn`, or `error` |

After changing the helper config:

```sh
sudo systemctl restart devstack-helper
devstack init
```

`auto_forward: true` makes the helper restore the forwarders on every start.
If another server owns 80/443, set it to `false`; `devstack init` then fails at
`SetForwarders` until the ports are free or the configuration is changed.
The helper's `proxy_http_port` and `proxy_https_port` must be exactly the
daemon's `http_port` and `https_port`; `devstack init` rejects a mismatch.

The daemon's own settings live in `~/.devstack/config.yaml` (`http_port`,
`https_port`, `ui_port`, log rotation, and request limits). The daemon creates
the file with defaults on first start. Keep the helper proxy ports aligned with
the daemon HTTP/HTTPS ports when you change either side.

## Logs

- Helper: `journalctl -u devstack-helper`
- Daemon under systemd: `journalctl --user -u devstack-daemon`
- Daemon started detached by `devstack init`: `~/.devstack/run/daemon.log`
- Sites: `devstack site logs <name> --tail 100`, `--follow` to stream, or
  `~/.devstack/logs/<site-id>/current.log`
- Diagnostic snapshot: `devstack doctor --json`

The daemon log is append-only for the current run. Site logs rotate according to
`log_max_mb` and `log_keep`. When the user unit is active, its stdout and stderr
go to the journal; `daemon.log` is only used by the detached fallback.

## Upgrade

Upgrades are forward-only. Install the new release with the same installer
entry point you used originally, keeping the existing helper configuration:

```sh
./install.sh --upgrade --release 0.2.0
# or, from a source checkout:
make build && ./install.sh --upgrade
# or, from a downloaded archive:
./install.sh --upgrade --archive devstack_0.2.0_linux_amd64.tar.gz --checksums checksums.txt
```

`--upgrade` requires an existing `~/.local/bin/devstack`, stops the user daemon
(through `systemctl --user` or the detached `devstack daemon stop` path) before
replacing binaries, aborts if the stop fails, keeps `/etc/devstack/helper.yaml`
unless `--force-config` is given, and restarts the helper and daemon. It does
not run `devstack init`; run it as the follow-up so hosts, forwarders, trust,
and the daemon protocol are re-synced:

```sh
devstack init
devstack doctor
```

Before the first schema migration of an existing database, the store writes a
consistent SQLite backup with `VACUUM INTO` to
`${DEVSTACK_HOME:-~/.devstack}/backups/state.db.pre-migrate-<UTC timestamp>.bak`.
It keeps the three newest backups and refuses to migrate if the backup cannot be
written or old backups cannot be rotated. A fresh database skips the backup
because there is no state to preserve.

Downgrades are not supported. The database schema is forward-only; restoring an
older binary against a migrated database is not a supported path. Keep
`~/.devstack` and the pre-migration backup if you need to recover data. See the
[upgrade guide](upgrade.md) for preflight, protocol-mismatch remediation,
verification, and rollback limits.

## Uninstall

First remove the integration (trust, hosts block, forwarders) and stop the
daemon:

```sh
devstack uninstall --yes
```

Project files are never removed. The command prints the remaining elevated
cleanup command, which removes the helper unit and system binary:

```sh
sudo systemctl disable --now devstack-helper
sudo rm -f /etc/systemd/system/devstack-helper.service
sudo rm -rf /usr/local/lib/devstack
sudo systemctl daemon-reload
```

Then remove the user unit and optionally the local state:

```sh
systemctl --user disable --now devstack-daemon
rm -f ~/.config/systemd/user/devstack-daemon.service
systemctl --user daemon-reload
```

`~/.devstack` contains the SQLite database, pre-migration backups, site logs,
installed Node runtimes, and the daemon CA bundle. Remove it only when you no
longer need that state.
`/etc/devstack/helper.yaml` and `/var/lib/devstack-helper` can be removed after
the helper is disabled.

## Troubleshooting

**The helper does not start.** Check that ports 80 and 443 are free and that
your user's UID in `/etc/devstack/helper.yaml` matches `id -u`. With
`auto_forward: true`, a bind failure is a startup failure. Set it to `false` if
another server owns those ports:

```sh
sudo systemctl status devstack-helper --no-pager
sudo journalctl -u devstack-helper -n 100 --no-pager
ss -ltnp 'sport = :80 or sport = :443'
```

**`devstack init` says the helper is unavailable.** Start or restart the helper
and verify the socket exists:

```sh
sudo systemctl restart devstack-helper
ls -l /run/devstack-helper/helper.sock
```

**Certificate warning in a browser.** See [TLS and trust](tls.md), then run
`devstack doctor --repair`. Restart Chrome or Firefox after trust changes.

**Hosts entries are missing.** Run `devstack init` or `devstack doctor --repair`.
Do not edit the block between the `# BEGIN devstack` and `# END devstack`
markers by hand.

## Limitations

- Builtin serving is the default; optional native nginx/Apache vhosts are
  available for static, Node, and PHP sites. The external server must already be
  installed and own 80/443; Apache PHP vhosts additionally need `proxy_fcgi`.
- Static, Node, and PHP sites are supported. PHP sites need an installed build;
  `v0.1.0` embeds PHP 8.2.35, 8.3.36, 8.4.27, and 8.5.12 entries from the
  published runtime release (see [PHP](php.md)).
- Linux x86_64 and systemd only. Windows/WSL (Plan 3) is **deferred by user
  request**; there is no `devstack-agent`, `install.ps1`, or Windows release.
- Release archives are unsigned and Linux x86_64 only. Signed packages and
  installers are M4 work.
- M1 has no diagnostics bundle/zip, auto-update, remote catalog, arm64 build, or
  log aggregation. `devstack doctor --json` is the supported diagnostic surface.
- The helper, CA, hosts block, and trust targets are machine-global but scoped
  to the single installing UID.
- The installer can use locally built binaries or a GitHub release archive. It
  does not install OS packages or configure a system nginx/Apache.
- Browser trust installation, root install/uninstall on 80/443, and live UI
  checks are manual acceptance items; the unprivileged acceptance runner does
  not perform them. Apache HTTPS is skipped when `mod_ssl` is unavailable.
