# Known issues and parked work

- **Last reviewed:** 2026-10-25
- **Scope:** current native implementation and the planned service platform.

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

### K1 — Remaining next-batch items

Waves 0–1 and the Wave 2 phpMyAdmin/dashboard work are implemented. Still missing:
service config editor/diff/apply, performance helper/apply/reset/benchmark wiring and
dashboard, site-scoped terminal/command runner, editable listener ports, and external-site
Docker mounts. The performance package is a pure engine today; it is not yet applied to
the host or dashboard. See the [Next Batch Plan](superpowers/plans/2026-10-26-service-platform-next-batch.md).

### K2 — Existing native provider vs Docker provider

The existing native external nginx/Apache mode is retained for manually installed
servers. Docker nginx/Apache are managed by the service layer; `vhost use builtin` performs
the explicit Docker→builtin switch. The dashboard service page now exposes lifecycle
actions; the provider/config/performance panels remain follow-ups.

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

### K5 — Remaining implementation decisions

Waves 1–2 settled the initial image digests, official httpd image (no wrapper), Composer
2.10.3 PHAR digest, phpMyAdmin config mechanism, Redis no-password default, and bounded
performance formulas/caps. Remaining: live host-tuning policy/user scope, benchmark tool
selection/result schema, exact listener-port restart behavior, and the amount of legacy
native-provider UI to retain.

### K6 — Host performance tuning changes are global

The planned host tuning applies sysctl/limits changes to the entire machine. It must be
applied with backup and reset, and only in the serialized performance phase.

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

## 3. Current native product limitations

- Linux x86_64 only; Windows/WSL/macOS/arm64 are out of scope.
- Native external nginx/Apache mode requires the server to be installed and configured by
  the administrator; devstack does not install packages.
- Browser trust, root install on 80/443, and live UI gates remain manual.
- phpMyAdmin, credentials/rotation, service lifecycle, builds, and most dashboard actions
  are implemented. Service config editing, performance apply/benchmark, site terminal, and
  editable listener ports remain.
- ImageMagick CLI is descoped; PHP `imagick` remains available.
- Docker web servers require sites under the managed projects root (K7).
- `install.sh` adds missing `service_root`, `tool_root`, and `tool_bin_dir` keys to an
  existing helper config and restarts it. A manually managed config without
  `service_root` leaves the service API at helper NOT_IMPLEMENTED until fixed.

## 4. Deferred/parked work

- Windows/WSL Plan 3 is deferred by user request.
- Signed packages/installers, auto-update, diagnostics bundles, remote catalog, arm64, and
  log aggregation are later milestones.
- The old standalone Docker image is not coming back; the new Docker model is service
  containers plus native devstack.
