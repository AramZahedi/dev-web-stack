# Known issues and current limitations

- **Last reviewed:** 2026-11-03
- **Current release:** `v0.4.0`
- **Scope:** Windows 10/11 + WSL2, Linux x86_64 with systemd, managed Docker
  services, cross-platform performance tuning, and local-only operation.

> **devstack is a local development tool.** It is not intended to host public or
> internet-facing websites. Do not expose the dashboard, daemon listeners,
> service ports, helper pipe, or local CA private key to untrusted users or
> networks.

## Status at a glance

| Area | Status | Evidence / limitation |
|---|---|---|
| Windows 10 22H2+ / Windows 11 x64 + WSL2 | Supported | Standalone installer, helper service, user daemon, managed Debian 12 WSL2 distro, Docker services, PHP/Node, Docker nginx/Apache switching, trusted TLS. Live vertical slice passed 100% on the tested host. |
| Linux x86_64 + systemd | Supported | Built-in proxy, managed Docker services, native and Docker vhosts, PHP/Node runtimes, installer/upgrade, and release packaging. Automated acceptance passed on Fedora 44; some manual root/browser gates remain. |
| macOS / Windows ARM / Linux arm64 | Not supported | No release artifact or supported runtime. |
| Public/internet-facing hosting | Not supported | Local development only. No hardening for untrusted networks, hostile tenants, or public exposure. |
| Release artifacts | Published | `v0.4.0` publishes Linux and Windows archives plus Windows installers. Binaries are unsigned. |

## Resolved in 0.4.0

These were real gaps in earlier releases and are covered by tests and live
acceptance now:

- Windows Docker nginx and Apache serving, including private HTTP-only vhosts
  on `127.0.0.1:18080` while the Windows daemon keeps public 80/443 and TLS.
- Direct nginx ↔ Apache switching without a built-in round trip, with automatic
  rollback to the previous container on failure.
- Automatic fallback to `builtin` when the active web service is deactivated or
  uninstalled.
- Dashboard serving choices disable nginx/Apache when that service is not
  installed, and remain usable if the service list cannot be read.
- WSL static sites on the Docker provider route through the managed web server.
- Adding or removing a site validates and gracefully reloads the active web
  container without a restart.
- Switching Docker → built-in starts WSL static backends, allocates ports, and
  publishes the builtin routes correctly.
- A stale static-site row (missing project folder or missing agent backend) no
  longer blocks a serving switch, stop, or delete.
- Balanced/Throughput/Custom tuning for managed services, WSL, the WSL VM, and
  Windows host settings. Throughput is the default.
- phpMyAdmin signed one-time dashboard signon replaced cookie/config auto-login.

## Current limitations and risks

### K1 — Windows VM and multi-machine coverage is incomplete

The Windows path is implemented and live-verified on the reference Windows 11
host, including the vertical slice, serving matrix, tuning matrix, installer,
and lifecycle suites. The disposable-VM rows INST-24/25/29 and a full
multi-machine matrix have not been completed. Treat Windows support as
**verified on the tested configuration**, not yet proven across every Windows
edition, locale, VPN, antivirus product, or VM provider.

### K2 — Database E2E cycles 9–15 are paused

The database unit/integration tests, dashboard flows, credentials, backups, and
the live phpMyAdmin signon pass. The full live database E2E cycles 9–15 were
paused during the latest campaign and have not been resumed. Do not claim
exhaustive coverage for every database operation until those cycles are green.

### K3 — Host tuning is global, opt-in, and partly host-specific

- Host tuning changes machine-wide settings, not only devstack.
- Throughput, Balanced, Custom, and Reset were live-verified on the reference
  Windows host. Linux real-root `/etc` tuning remains a manual acceptance gate.
- `.wslconfig` changes require `wsl --shutdown` (or a reboot) before they fully
  apply. The dashboard reports `restart_required`.
- Defender exclusions are best-effort. If the Defender PowerShell module is
  unavailable, the step is skipped and reported instead of failing the profile.
- The original pre-devstack tuning baseline is preserved across profile
  switches, but a machine-wide tuning profile can still affect unrelated local
  workloads.

### K4 — Managed projects roots are hard boundaries

Every site path must be a strict descendant of its source mode's managed
projects root. The root itself, sibling prefixes such as `projects-old`,
`..` traversal, and links that resolve outside the root are rejected.

This is intentional. Existing sites outside the root are not deleted, but they
are blocked from starting and reported by `devstack doctor`. Arbitrary
external-site Docker bind mounts are not supported; projects must live under the
managed Windows or WSL projects root.

On Windows, `C:\devstack\Windows-Projects` and `C:\devstack\WSL-Projects` are
navigation links only and are **not valid site paths**. Use the canonical target
path or the dashboard folder browser.

### K5 — MySQL container environment retains passwords

The official MySQL entrypoint exports `MYSQL_ROOT_PASSWORD` / `MYSQL_PASSWORD`
and does not unset them before the final server process. Compose files, logs,
and the dashboard do not contain the values, and the database ports bind
loopback only, but a local process with container-inspection rights can read the
environment. This is a local-development risk, not a remote exposure. MariaDB
and PostgreSQL unset their entrypoint credentials.

### K6 — Linux native external mode is administrator-managed

On Linux, native nginx/Apache mode requires the administrator to install and
configure the host server. devstack does not install packages or enable modules.
Apache vhosts require the documented `ssl`, `proxy`, `proxy_http`,
`proxy_wstunnel`, `headers`, and `rewrite` modules; PHP vhosts additionally need
`proxy_fcgi`.

Windows uses managed Docker nginx/Apache instead; a native Windows web server is
not supported as the serving provider.

### K7 — phpMyAdmin requires the dashboard signon

phpMyAdmin is loopback-only. The dashboard opens it with a short-lived,
single-use HMAC-signed signon token and consumes the nonce once. Opening the
phpMyAdmin port directly shows the normal manual login form, not the dashboard
auto-login. Do not bypass the dashboard flow in shared or untrusted environments.

### K8 — Releases are unsigned

The Linux archives and Windows archives/installers are **unsigned**. SHA-256
checksums detect corruption or tampering in transit, not a compromised release
host. Windows SmartScreen can show an unknown-publisher warning. Authenticode
signing, signed packages, and a signed installer are future work.

### K9 — Automatic GitHub CI is disabled

All GitHub workflows are `workflow_dispatch` only. Pushes, pull requests, and tag
pushes do not start automation. The local verification gate is authoritative.
The release workflow publishes the Linux archive with GoReleaser; the Windows
archive, tarball, and installers are built manually with
`build/release-windows.sh` and attached to the GitHub release.

### K10 — Terminal limitations

- A stopped site still gets a shell if its project directory is valid.
- Descendants that create a new session/process group can escape process-group
  teardown; normal shell jobs are killed.
- There is no background ticket janitor; expired tickets are pruned on issuance
  and consumption.
- Sessions are limited (4 total, 2 per site/user), idle time is 30 minutes,
  duration is 4 hours, and output is capped at 4 MiB.
- Keystrokes and terminal output are not audited; only session start/stop is.

### K11 — Unsupported platforms and packaging gaps

- macOS, Windows on ARM, and Linux arm64 have no supported artifact.
- There is no auto-update. Installing a newer release is an explicit installer
  run.
- There is no diagnostics bundle. `devstack doctor --json` is the support
  surface.
- There is no remote PHP catalog or log aggregation service.

### K12 — Performance limitations

- PHP-FPM `pm.*` pool sizing is persisted on the site row and applied to each
  running per-site FPM master through the same validate/swap flow as OPcache and
  realpath ini values. The generated fragment under `services/php/performance/`
  remains a readable preview of the applied budget.
- Performance-managed includes are reserved and hidden from the service config
  editor (`conf.d/performance/` and `conf.d/performance.conf`), so an editor
  write cannot be silently replaced by the next profile apply.
- Existing pre-change nginx/Apache base configs do not gain the managed include
  lines automatically; base files are user-owned. Fresh installs seed them, and
  apply reports a profile conflict until an existing base config adds them.
- Benchmarks use the labelled built-in Go HTTP generator, not wrk/hey. Results
  are measured but are not directly comparable to wrk/hey.
- Windows host tuning was live-verified; real Linux `/etc` host tuning remains a
  manual gate.
- Host tuning is machine-wide and needs a WSL restart for `.wslconfig` changes.

### K13 — Service config baseline and backups

- Generate-conflict reset uses the newest ordinary backup; it cannot reconstruct
  a generated baseline from a hash alone. A directly edited generated file with
  no prior backup cannot be reset. The UI requires an explicit keep/use/merge
  action instead.
- Backups live under `services/<id>/config/backups/{config,config-reset}` because
  the manager is rooted at the config directory. They are hidden from the
  editor/API policy.
- Apply validates before reload when the service is active. Inactive services
  save with `validation_pending`; the dashboard does not claim the file is live.

### K14 — Helper proxy targets follow daemon restart automatically

The daemon calls a narrow helper RPC with its configured HTTP/HTTPS ports before
serving-mode reconciliation. The helper validates the targets, rewrites only
`proxy_http_port` / `proxy_https_port` in `/etc/devstack/helper.yaml` atomically,
and updates its in-memory targets. Serving-mode reconciliation then restarts the
privileged forwarders against the new targets.

A helper started with flags but no `--config` file still updates in memory for
that process and reports that persistence was unavailable. Explicit
`--proxy-http` / `--proxy-https` flags also override the file after a helper
restart, so production units should rely on the config file for these two keys.

### K15 — Terminal limitations

See K10. The terminal is site-scoped, runs as the installing user on Linux or
inside the managed WSL environment on Windows, and never uses root/sudo or the
Docker socket. Live PTY coverage exists for static, PHP, and Node sites.

### K16 — Runtime and tooling caveats

- PHP `imap` is omitted from the current build pipeline because the verified
  upstream release does not cover the manifest's PHP 8.2/8.3 pairing.
- Node releases are Linux x64 only. Very old releases can fail the post-extract
  `node --version` smoke on a newer glibc/OpenSSL distro; installs fail closed
  rather than registering a broken build.
- Switching Node versions can invalidate native addons; run `npm rebuild` in the
  project after a switch. devstack does not run it automatically.
- The embedded catalog is intentionally limited to shipped LTS entries; use
  `devstack runtime available` for the full official Node list and `--offline`
  for the fallback.
- `build.node_version` is build-only. A site shell uses the site pin, then the
  global default; it never silently falls back to an arbitrary installed build.
- Upgrading a WSL install requires the installer to copy the new
  `devstack-agent` binary into `/data/devstack/bin` and restart the service.
  `devstack init` refreshes credentials/hosts/trust but does not replace an
  already-running agent binary.

### K17 — Windows-managed WSL runs as root

The managed Debian 12 distro runs its agent, PHP/Node workers, Docker engine, and
interactive shells as root inside the distro. That is deliberate for local
service management, but it means site code, build scripts, and terminal commands
have full power inside the managed Linux environment. Treat the distro as a
trusted local development machine and do not run untrusted code.

### K18 — Local-only authentication and exposure

- The dashboard is loopback-only and uses a local session cookie and CSRF token.
- Optional dashboard token authentication is available with `dashboard_auth:
  true` or `devstack init --dashboard-auth`.
- Service ports bind loopback. Web containers on Linux may use host networking
  for 80/443; on Windows the managed web server listens privately on 18080.
- The Windows firewall rule is scoped to the WSL subnet for the daemon proxy
  ports, not the LAN.
- None of this makes devstack safe for public exposure.

## Deferred and parked work

- Disposable-VM and multi-machine Windows acceptance (including INST-24/25/29).
- Full database E2E cycles 9–15.
- Linux real-root host-tuning acceptance and additional Linux distribution
  matrix runs.
- Authenticode/code signing for Windows binaries and installers.
- Automatic GitHub CI and an automated Windows release job.
- Auto-update and diagnostics bundles.
- Remote PHP catalog, log aggregation, arm64, and macOS support.
- External-site Docker bind mounts beyond the managed projects roots.
- The old standalone devstack-in-Docker image remains retired; Docker is used
  only for managed services and managed web servers.

## Reporting a problem

Collect the least-sensitive evidence first:

```sh
devstack version --json
devstack doctor --json
devstack site show <site> --json
devstack site logs <site> --tail 200
devstack service logs <service> --tail 200
```

Remove credentials, tokens, passwords, customer data, and private project files
before posting logs publicly. Never paste the local CA private key, agent token,
agent pin, database passwords, or dashboard auth token.
