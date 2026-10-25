# Known issues and parked work

- **Last reviewed:** 2026-10-25
- **Scope:** current implemented code, current Docker image, and planned service platform
  gaps.

## 1. Current product issues

### K1 — Docker external serving switch fails with 7070/7071

**Status:** Open, Docker-only, parked with Docker work.

Trying to switch to nginx in the Docker dashboard returns:

```text
INVALID_REQUEST: sites: switch helper to "nginx": helper: INVALID_REQUEST (HTTP 400):
helper: invalid request: dashboard backend port must be 7070, got 7071
```

Cause:

- Docker publishes dashboard port 7070 through a `socat` edge forwarder to the daemon's
  internal `127.0.0.1:7071`.
- The helper's native vhost manager intentionally requires the external dashboard vhost
  backend to be exactly `127.0.0.1:7070`.
- The Docker image has no nginx/Apache and is documented as builtin-only.

Workaround:

- Use builtin serving in Docker.
- Use a native devstack installation for native external nginx/Apache mode.

Potential future fixes:

- hide/disable external mode controls in the Docker dashboard;
- rework container internal/external dashboard ports;
- add a container-specific vhost manager. This is not part of the service-container design.

### K2 — Docker image does not include the planned optional services

The Docker image includes devstack and the current builtin PHP/Node/static stack. It does
not include MySQL, MariaDB, PostgreSQL, Redis, phpMyAdmin, nginx, or Apache. The planned
service platform is separate; not implemented.

### K3 — Container CA is not trusted by host browsers

The Docker CA is at:

```text
.docker-data/helper/pki/root.crt
```

It is not imported into host system/Chrome/Firefox stores. `curl --cacert` verifies it;
browsers will warn until manually imported. Native `devstack init` trust is separate.

### K4 — Docker dashboard/link ports are fixed at build time

The image bakes public HTTP/HTTPS ports into dashboard links. Changing host mappings
without rebuilding with matching `PUBLIC_HTTP_PORT`/`PUBLIC_HTTPS_PORT` can produce wrong
links. The current default is 8080/8443.

### K5 — Host ports are localhost-only

Docker Compose binds to `127.0.0.1` by default. LAN access requires
`DEVSTACK_BIND_ADDRESS=0.0.0.0`.

### K6 — Manual Linux acceptance gates

Browser trust, real root install/uninstall on 80/443, and live UI flows were not fully run
in the unprivileged acceptance. M1 is not claimed as fully accepted despite the Linux
release.

### K7 — Docker group requires re-login

The installing user was added to the `docker` group but an existing shell may still need
`sudo docker` until logout/login.

### K8 — Current host has no native devstack install

On the reference machine, `~/.devstack` is absent and `devstack-helper` is inactive.
Docker is the only running devstack instance. Native service-platform work must start
with a native install on the target machine.

### K9 — Test sites and hosts entries remain

The reference host has a devstack Docker block containing `blog.test`, `devstack.test`,
`nodeapp.test`, `phpapp.test`, and `secure.test`. These are verification artifacts, not
part of the image. Remove them from the dashboard or hosts file when no longer wanted.

### K10 — Docker helper state contains installed PHP/Node runtimes

`.docker-data/devstack/runtimes` contains PHP 8.4.27, PHP 8.5.12, and Node 22.23.4
(reference machine). These are host bind-mounted data, not baked into the image.

## 2. Planned service-platform gaps

### K11 — The service platform is not implemented

The design spec is ready for approval. No service catalog, Compose controller,
credential manager, build profiles, global PHP shim, dashboard terminal, or `.htaccess`
support exists in the current code.

### K12 — Native external provider vs Docker provider

The existing native external mode is retained for compatibility. The new Services page
is planned to manage Docker services only. The interaction/provider UI is not built.

### K13 — No root migration

Automatic migration from `~/.devstack` to `/data/devstack` is deliberately out of scope.
New installs can choose the root; existing installs remain in place.

### K14 — Open implementation decisions

Exact image digests, wrapper Dockerfiles, performance formulas, benchmark workloads,
phpMyAdmin config mechanism, legacy UI amount, Redis password, and Composer version/digest
remain to be chosen during planning/implementation.

### K15 — Host performance tuning changes are global

The design applies host `sysctl`/`limits` tuning with backup and reset. It affects the
machine, not just devstack. This is intentional but requires care and verification.

## 3. Historical/parked work

- Windows/WSL Plan 3 is deferred by user request.
- Signed packages/installers, auto-update, diagnostics bundle, remote catalog, arm64, and
  log aggregation are later milestones.
- The Docker image is not part of the service-platform design; do not assume its APIs or
  port layout match the native service design.
- The existing native external Apache mode uses the released vhost implementation. The
  planned service platform will add Docker Apache with `.htaccess`; it is not a retrofit
  already present in the release.
