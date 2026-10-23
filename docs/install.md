# Install devstack

devstack is a local web development stack for Linux. It serves static and Node
sites at trusted `https://<name>.test` URLs and manages the daemon, helper,
hosts entries, and local certificate authority.

This document covers the Plan 1 Linux install. PHP, Windows/WSL, and external
nginx/Apache vhosts arrive in later plans. See [Sites](sites.md) and
[TLS and trust](tls.md) for day-to-day use.

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

```sh
./install.sh
```

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
when a user systemd session is available. An existing
`/etc/devstack/helper.yaml` is kept by default; pass `--force-config` to replace
it with the generated defaults, or `--keep-config` to make that behavior
explicit. The installer does not install packages, configure nginx/Apache, or
run `devstack init`.

On an upgrade, the installer restarts an active user unit, or stops a detached
daemon left by an earlier `devstack init` before `devstack init` starts the unit
again. A headless session without a user systemd manager keeps the detached
fallback used by the CLI.

Preview the steps without changing anything:

```sh
./install.sh --dry-run
```

If `~/.local/bin` is not on `PATH`, add it in your shell profile:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

## First run

```sh
devstack init
```

`init` asks the helper for its CA, re-applies the privileged 80/443
forwarders, installs the CA in the operating-system and browser trust stores,
writes the managed hosts block, starts the daemon (through the installed user
unit when available), and runs the diagnostic checks. It prints the dashboard
URL and a loopback fallback.

Open <https://devstack.test>. If anything fails, run:

```sh
devstack doctor
```

`devstack doctor --repair` re-applies the safe parts of the install (trust,
hosts, runtime verification) without deleting data.

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
| `auto_forward` | `true` | Bind 80/443 at helper startup |
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

1. Build the new binaries.
2. Run `./install.sh` again. It replaces the binaries and units, keeps an
   existing helper config unless `--force-config` is used, and restarts an
   active user daemon unit.
3. Run `devstack init` to re-apply trust, hosts, and forwarders.
4. Run `devstack doctor`.

Downgrades are not supported. Back up `~/.devstack` if you need to preserve site
metadata and certificates.

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

`~/.devstack` contains the SQLite database, site logs, installed Node runtimes,
and the daemon CA bundle. Remove it only when you no longer need that state.
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

## Plan 1 limitations

- Builtin reverse proxy only. nginx and Apache vhosts are not available yet;
  the CI image installs both for upcoming work.
- Static and Node sites only. PHP support is planned for a later milestone.
- Linux x86_64 and systemd only. Windows/WSL support is planned later.
- The helper, CA, hosts block, and trust targets are machine-global but scoped
  to the single installing UID.
- Installer copies locally built binaries; it does not download release
  artifacts or install OS packages.
