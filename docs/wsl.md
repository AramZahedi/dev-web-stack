# Managed WSL2 Distribution & Linux Agent

devstack on Windows uses a dedicated, unshared WSL2 (Windows Subsystem for Linux)
instance running Debian 12 (Bookworm) to execute all Linux runtimes, processes,
and container services.

This document describes the design, provisioning, systemd integration, Docker
Engine architecture, agent lifecycle, and security model of the managed
distribution.

---

## 1. Why WSL2?

Web development tooling (PHP extensions, FPM master/worker architectures,
Composer, Node.js process managers, and Docker containers) relies heavily on
POSIX system calls, file permissions (`chmod`/`chown`), symbolic links, and
epoll concurrency.

Running these workloads natively on Windows or through Docker Desktop introduces:
- High I/O overhead from NTFS cross-boundary translation
- Incompatibilities with Linux-specific PHP extensions (e.g. pcntl, posix)
- Complex dependency on commercial Docker Desktop licenses and shared virtual machines

devstack solves this with a **split-plane architecture**:
- **Windows Host Plane:** Owns public network ports (80/443), system tray, embedded UI dashboard, Windows Service Controller, DPAPI secret storage, and OS certificate trust.
- **WSL Linux Plane:** Runs on native Linux ext4 storage, hosting the PHP FastCGI Process Manager (FPM), Node runtime supervisor, isolated site terminals, and dedicated Docker Engine.

---

## 2. Managed Distribution Specifications

The managed distribution is named `devstack` and is strictly isolated from user distributions:

```
Distribution Name:  devstack
Base Image:         Debian 12 (Bookworm) minbase
Filesystem:         ext4 (WSL2 VHDX virtual hard disk)
Import Location:    C:\devstack\wsl\devstack\ext4.vhdx
Data Root:          /data/devstack/
Default Shell:      /bin/bash
Init System:        systemd (PID 1)
```

### Reproducible RootFS Build Pipeline

The rootfs is generated deterministically by `build/wsl/build-rootfs.sh` using:
- Debian snapshot mirror pinned to snapshot timestamp `20251027T000000Z`
- Minbase packages: `systemd`, `systemd-sysv`, `dbus`, `ca-certificates`, `curl`, `gnupg`, `iproute2`, `procps`, `sudo`
- Fixed `versions.lock` recording exact artifact digests

### Ownership & Marker File

To protect user data and ensure safety across re-installations, devstack writes an immutable JSON marker upon initial provisioning:

```json
/data/devstack/managed.json
{
  "managed_by": "devstack",
  "install_id": "0192e486-95ab-7000-8000-000000000001",
  "created_at": "2026-10-27T21:00:00Z"
}
```

- devstack **refuses to adopt** an existing distro named `devstack` unless this marker exists and contains valid JSON with `"managed_by": "devstack"`.
- devstack **never touches** your default WSL distro (e.g. Ubuntu).

---

## 3. Configuration & Systemd Integration

The distro is configured during initialization with `/etc/wsl.conf`:

```ini
[boot]
systemd=true

[network]
generateHosts=false
generateResolvConf=true

[automount]
enabled=true
options="metadata,umask=22,fmask=11"
```

### Systemd as PID 1

By setting `systemd=true`, WSL2 runs full Linux systemd supervision:
- Services restart cleanly on failure
- Standard systemd journal logging (`journalctl -u devstack-agent`)
- Clean process group termination and cgroup resource tracking

### Hosts File Decoupling

`generateHosts=false` prevents WSL from overwriting `/etc/hosts` on startup. The `devstack-agent` owns `/etc/hosts` in the distro, ensuring `.test` domains always map locally to `127.0.0.1` and route through the WSL ingress.

---

## 4. Dedicated Docker Engine

devstack includes a self-contained Docker Engine directly inside the managed distro, completely free of Docker Desktop.

```
Service Name:      docker.service
Data Root:         /data/devstack/docker (ext4)
Socket:            /var/run/docker.sock (mode 0660, owned by root:docker)
Exposed Ports:     None (TCP listening is disabled)
```

### Version Pinning & Catalog

Installed components from `build/wsl/versions.lock`:
- `docker-ce`: `5:27.5.1-1~debian.12~bookworm`
- `containerd.io`: `1.7.25-1`
- `docker-compose-plugin`: `2.32.4-1~debian.12~bookworm`

### Storage & Security

- **Container and Volume Data:** Stored directly on ext4 at `/data/devstack/docker/` and `/data/devstack/services/`. This provides full Linux filesystem performance without cross-boundary NTFS lag.
- **Socket Isolation:** The Docker socket is never mounted into user containers or exposed to the Windows network. The Windows daemon interacts with Docker solely through the authenticated agent RPC interface.

---

## 5. The devstack-agent (`devstack-agent`)

The `devstack-agent` is a static Linux ELF binary built from `cmd/devstack-agent` running as a systemd service (`devstack-agent.service`).

```
Binary:        /data/devstack/bin/devstack-agent
Unit File:     /etc/systemd/system/devstack-agent.service
API Listen:    https://127.0.0.1:18443 and https://<wsl_ip>:18443
User:          root (with unprivileged worker delegation)
```

### Agent Responsibilities

1. **Port Allocation & Conflict Detection:** Allocates and recycles ephemeral ports for site backends (PHP-FPM, Node).
2. **PHP-FPM Pool Management:** Generates optimized FPM pool configurations in `/data/devstack/sites/<site>/fpm.conf` and monitors master processes.
3. **Node Process Supervision:** Spawns and supervisions Node applications, static Vite/Nuxt builds, and package managers (npm, pnpm, yarn).
4. **Site-Scoped PTY Terminals:** Manages pseudo-terminals (pty) streamed over WebSocket for browser-based interactive site consoles.
5. **Docker Compose Engine:** Renders dynamic Compose manifests, validates health status, and rotates container credentials for MySQL, PostgreSQL, Redis, phpMyAdmin, and nginx/Apache vhosts.
6. **Filesystem Validation:** Validates source files, symlink targets, ownership, and permissions without trusting Windows UNC boundaries alone.

---

## 6. Authentication & Security Model

Communication between the Windows daemon and the WSL agent is secured via mutual cryptographic verification (Decision S66).

```
┌────────────────────────┐                   ┌────────────────────────┐
│     Windows Host       │                   │    Managed WSL Distro  │
│                        │                   │                        │
│  devstack.exe          │   HTTPS + Bearer  │  devstack-agent        │
│  (Daemon & Router)     ├──────────────────►│  (Port 18443)          │
│                        │   Pinned Cert     │                        │
│  DPAPI Protected:      │                   │  Unix Permissions 0600:│
│  - agent-token         │                   │  - token               │
│  - agent-pin (SHA-256) │                   │  - server.crt/key      │
└────────────────────────┘                   └────────────────────────┘
```

1. **Bearer Token Authentication:**
   Upon installation, a cryptographically secure 256-bit random bearer token is generated.
   - On Windows: Stored encrypted using Windows Data Protection API (DPAPI) in `C:\devstack\system\agent-token`.
   - In WSL: Stored at `/data/devstack/system/token` with mode `0600` owned by `root`.
   - Every request from Windows must supply the header: `Authorization: Bearer <token>`. Unauthenticated requests immediately receive `401 Unauthorized`.

2. **TLS Certificate Pinning:**
   The agent serves traffic over TLS using a self-signed certificate.
   - The SHA-256 fingerprint of the certificate is pinned and encrypted with DPAPI in `C:\devstack\system\agent-pin`.
   - The Windows client verifies this pin on every TLS handshake, preventing local spoofing or man-in-the-middle attacks.

3. **Subnet & Loopback Isolation:**
   The agent binds to the WSL loopback and the internal Hyper-V virtual switch NAT interface. Windows Firewall rules explicitly drop any connection from external or LAN addresses.
