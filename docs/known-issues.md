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

### K1 — Service platform not implemented

The design is approved, but no service catalog, Compose controller, credential manager,
build profiles, global PHP shim, dashboard terminal, or `.htaccess` support exists in the
current code.

### K2 — Existing native provider vs Docker provider

The existing native external nginx/Apache mode is retained for manually installed
servers. The planned Services page manages Docker services only. The interaction/provider
UI is not built.

### K3 — No automatic root migration

Automatic migration from `~/.devstack` to `/data/devstack` is deliberately out of scope.
New installs can choose the root; existing installs remain in place.

### K4 — Manual Linux M1 gates

Browser trust, real root install/uninstall on 80/443, and live UI flows were not fully run
in the unprivileged M1 acceptance. M1 is not claimed as fully accepted despite the Linux
release.

### K5 — Open implementation decisions

Exact image digests, wrapper Dockerfiles, performance formulas, benchmark workloads,
phpMyAdmin config mechanism, legacy UI amount, Redis password, and Composer version/digest
remain to be chosen during implementation.

### K6 — Host performance tuning changes are global

The planned host tuning applies sysctl/limits changes to the entire machine. It must be
applied with backup and reset, and only in the serialized performance phase.

## 3. Current native product limitations

- Linux x86_64 only; Windows/WSL/macOS/arm64 are out of scope.
- Native external nginx/Apache mode requires the server to be installed and configured by
  the administrator; devstack does not install packages.
- Browser trust, root install on 80/443, and live UI gates remain manual.
- No database services, Redis, phpMyAdmin, or global PHP/Composer shim exist yet.
- No configurable PHP front-controller filename or `.htaccess` support exists yet.
- No Nuxt/Vite build profile or dashboard build button exists yet.

## 4. Deferred/parked work

- Windows/WSL Plan 3 is deferred by user request.
- Signed packages/installers, auto-update, diagnostics bundles, remote catalog, arm64, and
  log aggregation are later milestones.
- The old standalone Docker image is not coming back; the new Docker model is service
  containers plus native devstack.
