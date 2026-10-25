# Services, configuration, performance, and terminal

This guide covers the service platform surfaces added after the Wave 2 dashboard.
Services are optional Docker containers; devstack itself stays native.

## Service lifecycle

Install, activate, deactivate, restart, and uninstall services from **Services** or the
CLI:

```sh
devstack service list
devstack service status nginx
devstack service install nginx
devstack service activate nginx
devstack service deactivate nginx
devstack service restart nginx
devstack service uninstall nginx --yes [--delete-data]
devstack service logs nginx --tail 200
devstack service credentials nginx --reveal
```

- Installing pulls the pinned image and creates
  `services/<id>/{config,data,logs}`. A cached image alone is not installed.
- One web server (nginx or Apache) and one MySQL-family engine (MySQL or MariaDB) can be
  active at a time.
- Activation is transactional: dependency/port checks, competitor stop, target start,
  health wait, rollback on failure.
- Web-server containers use host networking for 80/443; databases, Redis, and phpMyAdmin
  bind loopback.
- Uninstall keeps data unless `--delete-data` is explicit; deletion is backed up and
  verified first.

## Editable listener ports

Settings edits the daemon's loopback listener ports, not public 80/443:

```yaml
# ~/.devstack/config.yaml
http_port: 8080
https_port: 8443
ui_port: 7070
```

The dashboard validates range, reserved backend range, distinctness, and bind
feasibility. A saved change returns `restart_required` and remains visibly pending until:

```sh
devstack daemon restart
```

The daemon reconciles builtin helper forwarders with the new HTTP/HTTPS ports on
startup. Native nginx/Apache dashboard vhosts use the configured UI port. Public 80/443
remain owned by the helper or the active web-server container.

## Service configuration

Open **Services → Configuration** on an installed service, or use the CLI:

```sh
devstack service config list nginx
devstack service config show nginx nginx.conf
devstack service config diff nginx nginx.conf --file ./nginx.conf
devstack service config apply nginx nginx.conf --file ./nginx.conf --yes
devstack service config reset nginx nginx.conf --yes
```

Scope and safety:

- Only `<devstack-home>/services/<id>/config` is exposed.
- Data, logs, secrets, certificates, sockets, backups, dotfiles, absolute paths,
  traversal, symlinks, non-regular files, and files over 1 MiB are rejected.
- The file list shows `base`, `generated`, and `custom` classes plus modified/detached
  and conflict markers. Contents are fetched one file at a time.
- Generated files with local edits are conflicts. Choose **Keep mine**, **Use proposed**,
  or **Use merged editor content**. The API requires an explicit
  `keep|use|merge` resolution.
- Apply backs up, writes atomically, validates when the service is active, and reloads
  only after validation. Failed validation/reload restores the previous file and manifest
  entry.
- Inactive services save with `validation_pending`; the dashboard never claims the file
  is live.
- Reset restores the newest backup and refuses when no backup exists.
- The config manifest is `services/<id>/config/.config-manifest.json` and is shared with
  Docker nginx/Apache generated vhosts. Legacy manifests at the service root are migrated
  automatically.

### Generated web vhosts

Docker nginx/Apache site vhosts are generated under `config/conf.d/generated/`. Do not
edit generated files by hand unless you intend to detach them; change the site or service
instead. User includes belong under `config/conf.d/custom/`.

## Performance

Open the **Performance** panel on Services. It shows measured CPU/cgroup quota, memory,
open-file limits, the current profile, host-tuning state, and benchmark history.

```sh
curl -s http://127.0.0.1:7070/api/v1/performance
curl -s http://127.0.0.1:7070/api/v1/performance/preview
```

Workflow:

1. Choose **Balanced**, **Throughput**, or **Custom** (validated JSON values).
2. **Preview** the generated nginx/Apache/PHP values and both fixed host-file diffs.
3. **Apply profile** and type `APPLY`. Host tuning is a separate checkbox and changes the
   whole machine.
4. **Reset** and type `RESET` to restore prior host files and safe include snippets.

Apply requires an active nginx/Apache service for web includes; database-only installs
skip them. OPcache/realpath ini values are merged through the PHP validate-then-swap
path. PHP-FPM `pm.*` pool sizing is generated but not yet merged into live FPM masters;
the result includes a warning. See [known issues](known-issues.md#k12--performance-limitations).

Host tuning uses only:

```text
/etc/sysctl.d/99-devstack-performance.conf
/etc/security/limits.d/99-devstack-performance.conf
```

The helper derives those paths itself, backs up under
`<devstack-home>/backups/performance/`, runs `sysctl --system`, verifies values, and rolls
back on failure. It never accepts a path or shell fragment from the API. Real root
apply/reset remains an explicit host acceptance gate.

### Benchmarks

The benchmark form selects a registered site, duration (1–3600 s), and connections
(1–100000). devstack uses its built-in Go HTTP generator by default because no verified
wrk/hey image is pinned; the tool and limitations are stored with every result. Results
include measured RPS, p50/p95/p99, errors, duration, target, profile, and timestamps.
`result.json` and `raw.log` live under `logs/performance/<timestamp>/`. Failed or canceled
runs never synthesize a measurement.

## Site terminal

Open a site and choose **Start terminal** in the Terminal card. The terminal:

- runs only for a registered site, as the installing user, in the project root;
- puts the site PHP/Node shims first in `PATH` and sets `PHPRC`, `DEVSTACK_SITE`,
  `DEVSTACK_HOME`, and `DEVSTACK_PROJECT`;
- never uses root, sudo, or the Docker socket;
- limits sessions (4 total, 2 per site/user), idle time (30 min), duration (4 h), and
  output (4 MiB);
- kills the shell process group on disconnect and audits start/stop only, never
  keystrokes or output.

Tickets are one-use, expire in 30 seconds, and are bound to the site and installing UID.
The WebSocket is allowed only from an allowed loopback Origin/Host. Terminal availability
does not require the site to be running, only a valid project directory. See
[known issues](known-issues.md#k13--terminal-limitations).

## Related

- [Install](install.md) — ports, helper alignment, system services.
- [Vhosts](vhosts.md) — native nginx/Apache mode and dashboard port.
- [Known issues](known-issues.md) — remaining host gates.
- [Next Batch Completion](superpowers/status/2026-10-26-next-batch-completion.md) —
  implementation evidence.
