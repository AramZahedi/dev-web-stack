# Known issues and parked work

- **Last reviewed:** 2026-10-26
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

### K1 — Remaining platform work

Waves 0–1, Wave 2, and the next batch are implemented. Still missing:
external-site Docker bind mounts, real root host-tuning acceptance, live Wave 3
benchmark/acceptance evidence, and merged PHP-FPM pool sizing. See the
[Next Batch Completion](superpowers/status/2026-10-26-next-batch-completion.md).

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

- PHP-FPM `pm.*` pool sizing is generated under `services/php/performance/` but is not
  merged into live per-site FPM masters because the site model has no persisted
  pool-sizing contract. OPcache/realpath ini values are applied through `sites.ApplyPHP`.
- Managed performance includes are not registered in the service-config manifest; they
  may appear as existing/custom files in the editor.
- Existing pre-change nginx/Apache base configs do not gain the managed include lines
  automatically; base files are user-owned. Fresh installs seed them, and apply reports a
  profile conflict until an existing base config adds them.
- Benchmarks use the labelled built-in Go HTTP generator, not wrk/hey. Results are
  measured but not directly comparable to wrk/hey.
- Host tuning was not applied to the real `/etc` during verification.

### K13 — Terminal limitations

- A stopped site still gets a shell if its project directory is valid.
- Descendants that deliberately create a new session/process group can escape process
  group teardown; normal shell jobs are killed.
- There is no background ticket janitor; expired tickets are pruned on issuance/consumption.
- Live host acceptance used a real PTY through the API/WebSocket test; it did not exercise
  installed PHP/Node shims on a real site.

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
- phpMyAdmin, credentials/rotation, service lifecycle, builds, service config editing,
  performance preview/apply/reset/benchmarks, site terminal, and editable listener ports
  are implemented.
- ImageMagick CLI is descoped; PHP `imagick` remains available.
- Docker web servers require sites under the managed projects root (K7).
- PHP-FPM pool sizing, external-site Docker mounts, and real host-tuning acceptance
  remain (K1/K12/K13).
- `install.sh` adds missing `service_root`, `tool_root`, and `tool_bin_dir` keys to an
  existing helper config and restarts it. A manually managed config without
  `service_root` leaves the service API at helper NOT_IMPLEMENTED until fixed.

## 4. Deferred/parked work

- Windows/WSL Plan 3 is deferred by user request.
- Signed packages/installers, auto-update, diagnostics bundles, remote catalog, arm64, and
  log aggregation are later milestones.
- The old standalone Docker image is not coming back; the new Docker model is service
  containers plus native devstack.
