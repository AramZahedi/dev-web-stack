# Known issues and parked work

- **Last reviewed:** 2026-10-27
- **Scope:** current native implementation, the planned service platform, and
  the revised Windows/WSL plan.

## 1. Standalone Docker image removed

The old devstack-in-Docker image was retired on 2026-10-25 and removed from the
repository and reference machine. The old runtime container, local images,
`.docker-data`, test projects, and `/etc/hosts` block were deleted.

The removed runtime state was backed up at:

```text
/tmp/opencode/devstack-old-docker-20261025T111246Z/
```

If another machine still runs the old image, remove it:

```sh
docker rm -f devstack
docker rmi devstack:0.1.0-docker devstack:latest
```

Docker Engine stays installed because the new service-platform design uses separate
nginx/Apache/database/Redis/phpMyAdmin containers. The old external-mode `7070/7071`
container bug is historical and no longer relevant to this repository.

## 2. Planned service-platform gaps

### K1 — Remaining platform work

Waves 0–1, Wave 2, and the next batch are implemented. Still missing:
external-site Docker bind mounts, real root host-tuning acceptance, and live Wave 3
benchmark/acceptance evidence. Per-site PHP-FPM pool sizing is persisted and applied
through the live validate/swap flow. See the
[Next Batch Completion](superpowers/status/2026-10-26-next-batch-completion.md) and the
[automatic performance/ports report](superpowers/status/2026-10-26-auto-performance-ports-completion.md).

### K2 — Existing native provider vs Docker provider

The existing native external nginx/Apache mode is retained for manually installed
servers. Docker nginx/Apache are managed by the service layer; `vhost use builtin` performs
the explicit Docker→builtin switch. The dashboard exposes lifecycle, configuration,
performance, benchmark, and terminal surfaces.

### K7 — Docker web servers require sites under the managed projects root

Docker nginx/Apache vhosts currently reject sites outside `<devstack-home>/projects`;
those sites remain available through builtin/native serving. External-site bind mounts
and per-site Docker mounts remain a follow-up.

### K8 — Official MySQL process environment retains passwords

The official MySQL entrypoint exports `MYSQL_ROOT_PASSWORD`/`MYSQL_PASSWORD` and does not
unset them before the final server process. Compose files, logs, and the dashboard never
contain the values, and ports are loopback-only, but container-local process inspection is
sensitive. PostgreSQL/MariaDB are unset by their entrypoints.

### K3 — No automatic root migration

Automatic migration from `~/.devstack` to `/data/devstack` is deliberately out of scope.
New installs can choose the root; existing installs remain in place.

### K4 — Manual Linux M1 gates

Browser trust, real root install/uninstall on 80/443, and live UI flows were not fully run
in the unprivileged M1 acceptance. M1 is not claimed as fully accepted despite the Linux
release.

### K5 — Settled implementation decisions

The listener-port restart contract, host-tuning fixed paths/opt-in, benchmark result
schema and built-in generator, service config scope, and terminal ticket/PTY limits are
implemented. Remaining host-specific decisions are limited to optional public 80/443
editing and live acceptance evidence.

### K6 — Host performance tuning changes are global

Host tuning is implemented behind an explicit `host_tuning:true` opt-in and a typed
confirmation in the dashboard. The helper writes only two fixed paths, backs up, verifies,
and rolls back. The real root apply/reset was not run on this host; unit/fake-runner and
destination-root tests pass.

### K12 — Performance limitations

- PHP-FPM `pm.*` pool sizing is persisted on the site row and applied to each running
  per-site FPM master through the same validate/swap flow as OPcache and realpath ini
  values. The generated fragment under `services/php/performance/` remains a readable
  preview of the applied budget.
- Performance-managed includes are reserved and hidden from the service-config editor
  (`conf.d/performance/` and `conf.d/performance.conf`), so an editor write cannot be
  silently replaced by the next profile apply.
- Existing pre-change nginx/Apache base configs do not gain the managed include lines
  automatically; base files are user-owned. Fresh installs seed them, and apply reports a
  profile conflict until an existing base config adds them.
- Benchmarks use the labelled built-in Go HTTP generator, not wrk/hey. Results are
  measured but not directly comparable to wrk/hey.
- Host tuning was not applied to the real `/etc` during verification.

### K13 — Service config baseline and backups

- Generate-conflict reset uses the newest ordinary backup; it cannot reconstruct a
  generated baseline from a hash alone. A directly edited generated file with no prior
  backup cannot be reset. The UI requires an explicit keep/use/merge action instead.
- Backups live under `services/<id>/config/backups/{config,config-reset}` because the
  manager is rooted at the config directory. They are hidden from the editor/API policy.

### K14 — Helper proxy targets follow daemon restart automatically

The daemon calls a narrow helper RPC with its configured HTTP/HTTPS ports before
serving-mode reconciliation. The helper validates the targets, rewrites only
`proxy_http_port`/`proxy_https_port` in `/etc/devstack/helper.yaml` atomically and
updates its in-memory targets. Serving-mode reconciliation then restarts the
privileged forwarders against the new targets. A helper started with flags but
no `--config` file still updates in memory for that process and reports that
persistence was unavailable. Explicit `--proxy-http`/`--proxy-https` flags also
override the file after a helper restart, so production units should rely on the
config file for these two keys.

### K15 — Terminal limitations

- A stopped site still gets a shell if its project directory is valid.
- Descendants that deliberately create a new session/process group can escape process
  group teardown; normal shell jobs are killed.
- There is no background ticket janitor; expired tickets are pruned on issuance/consumption.
- Live host acceptance now exercises a real PTY with installed PHP/Node shims on real
  static, PHP, and Node sites; see the
  [Node toolchain E2E report](superpowers/audits/2026-10-30-node-toolchain-e2e.md).

### K9 — ImageMagick CLI descoped

ImageMagick CLI is no longer planned: no Settings card, catalog service, helper tool RPC,
or image digest. PHP's `imagick` extension remains available per site. Historical design
text mentioning the CLI is superseded by this decision.

### K10 — Automatic GitHub CI is disabled

`.github/workflows/ci.yml`, `php.yml`, and `release.yml` are `workflow_dispatch` only.
Pushes, pull requests, and tags do not start workflows. Use the local verification gate in
the Next Batch Plan. Manual dispatch still works if explicitly invoked.

### K11 — Current test runtime uses `~/.devstack`

The local test daemon uses `--home /home/aram/.devstack` and a temporary root helper state
under `/tmp/opencode/devstack-runtime`; hosts/trust are intentionally not modified. This
is not a system install. Host nginx owns 80/443, so Docker web-server activation will
conflict until those ports are freed. See [Continuation State](superpowers/status/2026-10-26-continuation.md).

### K16 — Windows/WSL plan revised; implementation not started

The 2026-10-27 review resolved the first plan’s public-entry/WSL ingress,
backend/port/route/PHP-path, canonical storage, source-copy, agent TLS/token,
helper/daemon pipe security, no-dot, tooling, Linux layout, and rootfs/artifact
gaps. The revised [design](superpowers/specs/2026-10-26-windows-wsl-design.md),
[plan](superpowers/plans/2026-10-26-windows-wsl.md), and
[review resolution](superpowers/status/2026-10-27-windows-plan-review-resolution.md)
are the source of truth. No Windows artifact, agent, installer, ingress, or
acceptance run exists yet.

## 3. Current native product limitations

- Linux x86_64 is the implemented platform. Windows 11 + WSL2 has a revised
  design and implementation plan (2026-10-27) but no implementation has
  started; macOS and arm64 remain out of scope.
- Native external nginx/Apache mode requires the server to be installed and configured by
  the administrator; devstack does not install packages.
- Browser trust, root install on 80/443, and live UI gates remain manual.
- phpMyAdmin, credentials/rotation, service lifecycle, builds, service config editing,
  performance preview/apply/reset/benchmarks, site terminal, and editable listener ports
  are implemented.
- ImageMagick CLI is descoped; PHP `imagick` remains available.
- Docker web servers require sites under the managed projects root (K7).
- External-site Docker mounts and real host-tuning acceptance remain (K1/K7/K12).
- `install.sh` adds missing `service_root`, `tool_root`, and `tool_bin_dir` keys to an
  existing helper config and restarts it. A manually managed config without
  `service_root` leaves the service API at helper NOT_IMPLEMENTED until fixed.

### Node toolchain notes

- Node releases are Linux x64 only. Very old releases can fail the post-extract
  `node --version` smoke on a newer glibc/OpenSSL distro; installs fail closed rather
  than registering a broken build.
- Switching Node versions can invalidate native addons; run `npm rebuild` in the project
  after a switch. devstack does not run it automatically.
- The embedded catalog is intentionally limited to shipped LTS entries; use
  `devstack runtime available` for the full official release list and `--offline` for the
  fallback.
- `build.node_version` is build-only. A site shell uses the site pin, then the global
  default; it never silently falls back to an arbitrary installed build.
- Upgrading a WSL install requires the installer to copy the new `devstack-agent` binary
  into `/data/devstack/bin` and restart the service. `devstack init` refreshes
  credentials/hosts/trust but does not replace an already-running agent binary.

## 4. Deferred/parked work

- Windows/WSL is no longer deferred: it has a revised architecture and
  implementation plan (2026-10-27), but no Windows artifact exists yet and
  implementation has not started. The revised plan supersedes the 2026-10-26
  version.
- Signed packages/installers, auto-update, diagnostics bundles, remote catalog, arm64, and
  log aggregation are later milestones.
- The old standalone Docker image is not coming back; the new Docker model is service
  containers plus native devstack.
