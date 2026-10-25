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

### K1 — Wave 2 items are not implemented

Wave 1 supplies the Docker service catalog, transactional lifecycle API/CLI, database
credentials/admin bootstrap, Docker nginx/Apache serving, site shells, global
PHP/Composer, and static builds. Still missing: phpMyAdmin, ImageMagick CLI, credential
rotation, performance profiles/host tuning/benchmarks, external-site Docker mounts, and
the full dashboard action/config/build/terminal panels. The Services page is read-only.

### K2 — Existing native provider vs Docker provider

The existing native external nginx/Apache mode is retained for manually installed
servers. Docker nginx/Apache are managed by the service layer; `vhost use builtin` performs
the explicit Docker→builtin switch. The dashboard does not yet expose service action/
provider controls.

### K7 — Docker web servers require sites under the managed projects root

Docker nginx/Apache vhosts currently reject sites outside `<devstack-home>/projects`;
those sites remain available through builtin/native serving. External-site bind mounts
are a Wave 2 item.

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

Wave 1 settled the initial image digests, official httpd image (no wrapper), Composer
2.10.3 PHAR digest, and Redis no-password default. Wave 2 still needs performance
formulas/caps, benchmark workloads, phpMyAdmin config mechanism, and the amount of legacy
native-provider UI to retain.

### K6 — Host performance tuning changes are global

The planned host tuning applies sysctl/limits changes to the entire machine. It must be
applied with backup and reset, and only in the serialized performance phase.

## 3. Current native product limitations

- Linux x86_64 only; Windows/WSL/macOS/arm64 are out of scope.
- Native external nginx/Apache mode requires the server to be installed and configured by
  the administrator; devstack does not install packages.
- Browser trust, root install on 80/443, and live UI gates remain manual.
- phpMyAdmin, ImageMagick CLI, performance profiles, host tuning, and credential rotation
  are not implemented yet.
- The dashboard service page is read-only; service/config/build/credential actions are
  API/CLI only until Wave 2.
- Docker web servers require sites under the managed projects root (K7).
- Existing helper configs require `service_root`, `tool_root`, and `tool_bin_dir` plus a
  helper restart; fresh installs get them from `install.sh`. Without `service_root`, the
  service API returns helper NOT_IMPLEMENTED.

## 4. Deferred/parked work

- Windows/WSL Plan 3 is deferred by user request.
- Signed packages/installers, auto-update, diagnostics bundles, remote catalog, arm64, and
  log aggregation are later milestones.
- The old standalone Docker image is not coming back; the new Docker model is service
  containers plus native devstack.
