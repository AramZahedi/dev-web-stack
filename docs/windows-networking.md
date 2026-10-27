# Windows & WSL Networking, Firewall Scoping, and Trusted HTTPS

devstack coordinates network traffic across the Windows host, the managed WSL2
virtual network, and Docker service containers. It terminates public TLS on the
Windows host while enabling seamless bi-directional `.test` domain resolution
from both Windows browsers and WSL command-line tools.

This document describes the networking architecture, port assignments, loopback
proxying, firewall security rules, and certificate trust mechanics.

---

## 1. Network Architecture Overview

```
                      ┌──────────────────────────────────────────────┐
                      │                 Windows Host                 │
                      │                                              │
                      │  Browser (Edge, Chrome, Firefox)             │
                      │       │                                      │
                      │       ▼  (https://mysite.test:443)           │
                      │  devstack-helper / devstack proxy            │
                      │  (Listens on 127.0.0.1: 80, 443)             │
                      │  (Proxy Listener: 127.0.0.1 & WSL IP: 8443)  │
                      └───────┬──────────────────────────────▲───────┘
                              │                              │
                     Forward to WSL Backend         WSL Ingress Forward
                     (WSL localhost:18080+)        (WSL Host Gateway:8443)
                              │                              │
                              ▼                              │
                      ┌──────────────────────────────────────┴───────┐
                      │              Managed WSL2 Distro             │
                      │                                              │
                      │  devstack-agent                              │
                      │  ├── WSL Ingress Proxy (80, 443)             │
                      │  ├── PHP-FPM Pools (127.0.0.1:9000+)         │
                      │  ├── Node Runtimes (127.0.0.1:3000+)         │
                      │  └── Docker Services (127.0.0.1:3306, 6379)  │
                      │                                              │
                      │  CLI tools: `curl https://mysite.test/`      │
                      └──────────────────────────────────────────────┘
```

---

## 2. Port Allocation & Listener Roles

| Port | Protocol | Binding Interface | Process | Purpose |
|---|---|---|---|---|
| **80** | HTTP | Windows `127.0.0.1` | `devstack-helper` / `devstack` | Public HTTP entry; redirects to HTTPS or serves ACME challenges. |
| **443** | HTTPS | Windows `127.0.0.1` | `devstack-helper` / `devstack` | Public HTTPS entry; terminates TLS for all `*.test` domains. |
| **8080** | HTTP | Windows `127.0.0.1` & WSL Subnet | `devstack` | Daemon internal HTTP listener. |
| **8443** | HTTPS | Windows `127.0.0.1` & WSL Subnet | `devstack` | Daemon internal HTTPS router & TLS termination endpoint. |
| **7070** | HTTP/WS | Windows `127.0.0.1` | `devstack` | Local dashboard and control API. |
| **18443**| HTTPS | WSL `127.0.0.1` & WSL IP | `devstack-agent` | Authenticated agent control API. |
| **18080**| HTTP | WSL `127.0.0.1` | Docker nginx/Apache | Private Docker web server port (Decision S60). |
| **3306** | TCP | WSL `127.0.0.1` | Docker MySQL / MariaDB | Database listener (reachable via loopback forwarding). |
| **5432** | TCP | WSL `127.0.0.1` | Docker PostgreSQL | Database listener. |
| **6379** | TCP | WSL `127.0.0.1` | Docker Redis | In-memory cache listener. |
| **8082** | HTTP | WSL `127.0.0.1` | Docker phpMyAdmin | Cookie-authenticated database administration GUI. |

---

## 3. WSL Ingress & Host Gateway Routing

A common frustration in WSL2 development is that `.test` domains work in Windows
browsers but fail inside the WSL Linux terminal when running `curl`, `composer`,
or background jobs.

devstack solves this with an **in-distro ingress forwarder** (Decision S61):
1. **WSL `/etc/hosts` Integration:**
   The `devstack-agent` manages `/etc/hosts` inside the distro. All `.test` domains are mapped to `127.0.0.1`.
2. **Fixed Ingress Listener:**
   Inside WSL, an ingress listener binds ports 80 and 443 on `127.0.0.1`.
3. **Gateway Forwarding:**
   When a process in WSL connects to `https://mysite.test/`, the ingress receives the connection on WSL `127.0.0.1:443` and transparently proxies it to the Windows host gateway IP on port `8443`.
4. **Unified Routing:**
   The Windows daemon terminates TLS, identifies the site, and proxies to the appropriate backend. This guarantees identical routing whether traffic originates from Windows or WSL.

---

## 4. Windows Firewall Scoping (Decision S72)

To satisfy the principle of least privilege, the Windows daemon's internal proxy
listeners (ports 8080 and 8443) must be reachable from the WSL virtual machine,
but must **never be exposed to the local area network (LAN) or public internet**.

The installer creates an inbound Windows Firewall rule:

```powershell
Name:         devstack-daemon-proxy
DisplayName:  Devstack Daemon Proxy
Direction:    Inbound
Action:       Allow
Protocol:     TCP
LocalPort:    8080, 8443
RemoteAddress: <WSL_NAT_Subnet> (e.g. 172.x.x.x/20)
```

### Dynamic Subnet Tracking

WSL2 creates an internal Hyper-V Virtual Ethernet adapter (`vEthernet (WSL)`).
The helper service and doctor dynamically query this adapter's IP address and
subnet mask. If the WSL subnet changes upon reboot, devstack reconciles the
firewall rule to match the active subnet.

---

## 5. Localhost Forwarding Requirement

devstack requires WSL2's built-in **localhost forwarding** feature.
Localhost forwarding allows traffic sent to `127.0.0.1:<port>` on the Windows
host to connect directly to listeners on `127.0.0.1:<port>` inside WSL.

- Verified automatically by `devstack doctor`.
- If disabled or blocked by third-party VPNs or endpoint protection software,
  `devstack doctor` will report:
  `WSL_LOCALHOST_FORWARDING_UNAVAILABLE`.
- Ensure `%USERPROFILE%\.wslconfig` does not contain `localhostForwarding=false`.

---

## 6. Trusted HTTPS & Root Certificate Authority

devstack creates and manages an internal, offline Root Certificate Authority
(Root CA) that provides trusted TLS certificates for all `.test` domains.

### Root CA Generation & Custody

- **CA Certificate:** `C:\devstack\data\certs\root-ca.crt`
- **Private Key:** `C:\devstack\data\certs\root-ca.key` (permission restricted to `SYSTEM` and `Administrators`)
- **Authority:** The helper service alone possesses the capability to mint site certificates.

### Multi-Store Trust Installation

The installer and `devstack trust repair` register the CA into all platform trust stores:

1. **Windows Local Machine Root Store:**
   Installed into `Cert:\LocalMachine\Root` via the Windows CryptoAPI (`internal/wintrust`). All Windows applications, including Microsoft Edge, Google Chrome, and native curl, automatically trust devstack certificates.
2. **Mozilla Firefox NSS Databases:**
   If Mozilla Firefox is installed, devstack scans user profiles and adds the CA to the NSS database (`cert9.db`).
3. **WSL Debian CA Store:**
   The CA certificate is copied into `/usr/local/share/ca-certificates/devstack-root-ca.crt` inside the managed distro and registered with `update-ca-certificates`. Tools like `curl`, `git`, and `composer` running in Linux ext4 trust all site certificates without SSL verification errors.
