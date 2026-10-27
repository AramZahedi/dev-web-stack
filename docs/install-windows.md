# Install devstack on Windows

devstack is a local web development stack for Windows and Linux. On Windows, it
couples a native Windows service (`devstack-helper.exe`), a native user daemon
(`devstack.exe`), and an embedded dashboard with a dedicated, managed Debian 12
WSL2 distribution hosting Linux runtimes (PHP-FPM, Node.js) and Docker Engine
services. It serves local sites at trusted `https://<name>.test` URLs with zero
browser certificate warnings.

This document details the installation, system requirements, disk layouts,
upgrade procedures, uninstallation, and initial verification for Windows.

---

## 1. System Requirements & Prerequisites

devstack requires:

| Component | Minimum Requirement | Notes |
|---|---|---|
| **Operating System** | Windows 10 22H2 (Build 19045+) x64 or Windows 11 x64 | Windows on ARM is not supported. Tested on Windows 11 Build 22631+. |
| **Hardware Virtualization** | Enabled in BIOS/UEFI (Intel VT-x or AMD-V / SVM) | Required for WSL2 Hyper-V hypervisor platform. |
| **WSL Version** | WSL 2.0.0 or higher (`wsl --version`) | Features systemd and localhost forwarding support. |
| **WSL Networking Mode** | NAT (default) | `networkingMode=mirrored` is not supported due to port proxying requirements. |
| **Available Disk Space** | 10 GB free on `C:\` | For `C:\devstack` layout, Windows binaries, and WSL ext4 virtual disk (`ext4.vhdx`). |
| **Privileges** | Local Administrator rights | Elevation is required during installation to register the helper service, configure firewall rules, and provision the root CA. Day-to-day commands do not require elevation. |
| **Port Availability** | Ports 80 and 443 | Must be free on `127.0.0.1` for public HTTP/HTTPS forwarders. IIS (`W3SVC`) must be disabled or reassigned. |

Verify your environment before beginning:

```powershell
# Check Windows Build (must be 19045 or higher)
[Environment]::OSVersion.Version.Build

# Check WSL version (must be 2.0.0+)
wsl --version
```

If WSL is outdated or missing:

```powershell
wsl --update
```

---

## 2. Directory & Storage Layout

devstack maintains strict separation between Windows host concerns and WSL Linux execution:

```
C:\devstack\
├── bin\                     # Executables added to Machine PATH
│   ├── devstack.exe         # User CLI, daemon, embedded UI, proxy router
│   ├── devstack-helper.exe  # Privileged Windows system service
│   ├── php.cmd              # Windows shim forwarding to WSL PHP CLI
│   └── composer.cmd         # Windows shim forwarding to WSL Composer
├── config\                  # User-editable daemon configuration
│   └── config.yaml          # Port overrides, defaults, and runtime preferences
├── data\                    # Machine-local operational data
│   ├── devstack.db          # SQLite database (NTFS-exclusive flock; no dot prefix)
│   ├── certs\               # Root CA cert/key, generated site leaf certs
│   └── logs\                # Windows daemon and helper log files
├── system\                  # Protected system state (SYSTEM & Admin only)
│   ├── helper.yaml          # Helper service configuration and proxy targets
│   ├── install-manifest.json# Checksummed installation receipt and stage flags
│   ├── agent-token          # DPAPI-encrypted WSL agent authentication token
│   └── agent-pin            # DPAPI-encrypted WSL agent TLS certificate pin
├── wsl\
│   └── devstack\            # Managed WSL2 distribution disk
│       └── ext4.vhdx        # Debian 12 ext4 filesystem container
└── projects\                # Default root for Windows-mode project checkouts
```

Inside the managed WSL distribution (`\\wsl.localhost\devstack` or `/data/devstack`):

```
/data/devstack/
├── bin/                     # devstack-agent binary (Linux ELF)
├── system/                  # Agent configuration, TLS certs, token, pin (0600)
├── docker/                  # Docker Engine data-root on ext4
├── services/                # Database/cache storage (MySQL, Redis, PostgreSQL)
├── sites/                   # Generated per-site PHP-FPM pool configs & sockets
├── projects/                # Default root for WSL-mode project checkouts
└── managed.json             # Marker file confirming devstack ownership
```

> [!IMPORTANT]
> devstack never creates dot-prefixed basenames on Windows (Decision S70). Internal files use explicit names like `config.yaml`, `install-manifest.json`, and `devstack.db`.

---

## 3. Installation

Installation is performed via `install.ps1` from an elevated PowerShell terminal.

### Option A: From a Downloaded Release Archive

1. Download the latest `devstack_<version>_windows_amd64.zip` and `checksums.txt`.
2. Verify the archive checksum:
   ```powershell
   $expected = (Get-Content checksums.txt | Select-String "devstack_.*_windows_amd64.zip").Line.Split(" ")[0]
   $actual = (Get-FileHash devstack_*_windows_amd64.zip -Algorithm SHA256).Hash.ToLower()
   if ($actual -ne $expected) { throw "Checksum mismatch!" }
   ```
3. Extract the archive:
   ```powershell
   Expand-Archive -Path devstack_*_windows_amd64.zip -DestinationPath .\devstack-installer
   cd .\devstack-installer
   ```
4. Run the elevated installer:
   ```powershell
   # Run as Administrator
   .\install.ps1
   ```

### Option B: Building and Installing from Source

Requires Go 1.24+, Node 22 LTS, and Git:

```powershell
git clone https://github.com/AramZahedi/dev-web-stack.git
cd dev-web-stack

# Build dashboard and binaries
make build-windows
make build-agent

# Run elevated installation
.\install.ps1
```

### Installer Parameters

`install.ps1` supports the following flags:

| Parameter | Default | Description |
|---|---|---|
| `-InstallDir <path>` | `C:\devstack` | Target root installation directory on Windows. |
| `-RootFSPath <path>` | auto-detected | Path to `debian-12-wsl-rootfs.tar.gz`. |
| `-AllowReboot` | `$false` | Allows the installer to schedule a resume task and reboot if optional Windows features were enabled. |
| `-SkipWSL` | `$false` | Skips WSL distro import and agent provisioning (host layout and helper only). |
| `-DryRun` | `$false` | Simulates checks and planned modifications without changing system state. |
| `-Upgrade` | `$false` | Performs in-place binary upgrades with automatic rollback on failure. |
| `-TestSandbox <path>` | `$null` | Isolates installation inside a test directory without modifying machine services. |

---

## 4. Verification

After installation finishes, verify the environment:

1. **Verify Helper Windows Service:**
   ```powershell
   Get-Service -Name DevstackHelper
   # Status should be 'Running'
   ```

2. **Verify CLI and PATH:**
   Open a new PowerShell terminal to refresh your `PATH`:
   ```powershell
   devstack version
   ```

3. **Run Doctor Diagnostic Suite:**
   ```powershell
   devstack doctor
   ```
   The doctor tests:
   - Helper service connectivity and SID validation
   - Windows root CA installation in `Cert:\LocalMachine\Root`
   - Windows `hosts` managed block
   - Inbound Windows Firewall proxy rule (`devstack-daemon-proxy`)
   - WSL distro existence, systemd status, and Docker Engine readiness
   - WSL localhost forwarding reachability
   - PHP and Node runtime catalogs

4. **Start the User Daemon:**
   ```powershell
   devstack daemon start
   ```

5. **Visit the Dashboard:**
   Open your browser to:
   `https://devstack.test/`
   The browser should display the devstack management dashboard with a valid, trusted TLS lock.

---

## 5. Upgrades & Rollback

To upgrade devstack:

```powershell
# Extract new release package
Expand-Archive -Path devstack_<new_version>_windows_amd64.zip -DestinationPath .\upgrade-pack
cd .\upgrade-pack

# Run upgrade with elevated PowerShell
.\install.ps1 -Upgrade
```

The upgrade workflow:
1. Stops the running daemon and backs up `devstack.exe` and `devstack-helper.exe` to `.bak`.
2. Backs up `/data/devstack` inside WSL to a temporary archive.
3. Stages updated Windows binaries and restarts the helper service.
4. Stages the new `devstack-agent` into the WSL distribution and restarts `devstack-agent.service`.
5. Performs a health probe against the agent HTTPS API.
6. If the probe fails, the installer immediately initiates an automatic rollback to previous binaries and state.

---

## 6. Uninstallation

Uninstallation is performed using `uninstall.ps1` from an elevated prompt:

```powershell
cd C:\devstack
.\uninstall.ps1
```

By default, uninstallation:
- Stops and unregisters the `DevstackHelper` Windows service.
- Unregisters the user daemon scheduled startup task.
- Cleans the managed `.test` block from `C:\Windows\System32\drivers\etc\hosts`.
- Removes the devstack local CA from the Windows Root certificate store.
- Removes the inbound `devstack-daemon-proxy` Windows Firewall rule.
- Removes `C:\devstack\bin` from the Machine `PATH`.
- **Preserves** all user projects, SQLite state, and the managed WSL distribution (`ext4.vhdx`).

### Purge Options

To permanently remove user state or the WSL distro:

```powershell
# Remove the managed WSL distro and its ext4 disk:
.\uninstall.ps1 -PurgeDistro

# Remove DPAPI secrets, SQLite database, and the C:\devstack directory:
.\uninstall.ps1 -PurgeData -Force
```
