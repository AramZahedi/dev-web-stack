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
| **Available Disk Space** | 10 GB free | For `%USERPROFILE%\.devstack` layout, Windows binaries, and WSL ext4 virtual disk (`ext4.vhdx`). |
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
%USERPROFILE%\.devstack\
├── bin\                     # Executables added to User/System PATH
│   ├── devstack.exe         # User CLI, daemon, embedded UI, proxy router
│   ├── devstack-helper.exe  # Privileged Windows system service
│   ├── php.cmd              # Windows shim forwarding to WSL PHP CLI
│   └── composer.cmd         # Windows shim forwarding to WSL Composer
├── config.yaml              # User-editable daemon configuration
├── state.db                 # SQLite database (NTFS-exclusive flock)
├── certs\                   # Root CA cert/key, generated site leaf certs
├── logs\                    # Windows daemon and helper log files
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

### WSL Root Execution Model & Project File Permissions

devstack enforces an explicit execution and permission model across the Windows-WSL boundary:

1. **Root User Execution Inside the Managed Distro:**
   - The managed WSL2 distribution (`devstack`) is imported without a default unprivileged Linux user. All internal automation, systemd units, Docker Engine (`dockerd`), and the Linux agent (`devstack-agent.service`) execute under the `root` account (`UID 0`).
   - Root execution is required inside the distro to manage system services (PHP-FPM pools, Docker containers, database daemons), bind internal ports, manipulate `/etc/hosts`, and configure systemd without interactive password prompts.
   - Interactive shell sessions spawned via `wsl.exe -d devstack` run as `root` by default, giving developers full administrative control over the Linux guest environment.

2. **Runtime Worker Privilege Separation:**
   - Although the daemon agent and supervisors run as root, web execution workers operate under dedicated unprivileged service accounts.
   - PHP-FPM site pools execute worker processes under the `www-data` account (`UID 33`, `GID 33`).
   - Script execution (PHP scripts, Node processes) cannot write to protected agent state (`/data/devstack/system/` is restricted to mode `0600` owned by `root`).

3. **Project File Permissions Across Storage Models:**
   - **WSL-Mode Projects (ext4 container at `/data/devstack/projects/`):**
     Recommended for maximum I/O performance. Files reside on the native ext4 virtual disk (`ext4.vhdx`). Standard POSIX file permissions (`chmod`, `chown`) and symbolic links are fully respected. When Windows IDEs (PhpStorm, VS Code) or Git for Windows access these projects through the network UNC share (`\\wsl.localhost\devstack\data\devstack\projects\<site>`), the WSL 9P / virtio-fs translation layer maps the Windows user to Linux permissions seamlessly.
   - **Windows-Mode Projects (NTFS filesystem at `%USERPROFILE%\.devstack\projects\` or `/mnt/c/...`):**
     When projects reside on the Windows host filesystem and are mounted into WSL under `/mnt/c/`, the distro's `/etc/wsl.conf` configures automount with `metadata,umask=22,fmask=11`. The `metadata` option stores Linux file ownership and permission bits in NTFS extended attributes, ensuring `chmod +x` and POSIX file mode changes persist correctly across both Windows and WSL without altering Windows ACLs. The `fmask=11` mask ensures regular files do not accidentally inherit world-executable bits.

---

## 3. Installation

You can install devstack on Windows using the standalone installer executable (recommended), a release zip archive, or by building from source.

### Option A: Standalone Binary Installer (Recommended)

The standalone binary installer executable (`devstack-installer.exe` or `devstack-setup.exe`) embeds the entire release payload—including Windows CLI binaries, the privileged helper service, the Linux agent, and the Debian 12 WSL root filesystem. It requires zero external downloads and circumvents PowerShell execution policy restrictions.

1. **Download:**
   Download **`devstack-installer.exe`** (or `devstack-setup.exe`) and `checksums.txt` from the [Releases](https://github.com/AramZahedi/dev-web-stack/releases/tag/v0.2.0) page.

2. **Verify Checksum (Optional but recommended):**
   ```powershell
   $expected = (Get-Content checksums.txt | Select-String "devstack-installer.exe").Line.Split(" ")[0]
   $actual = (Get-FileHash .\devstack-installer.exe -Algorithm SHA256).Hash.ToLower()
   if ($actual -ne $expected) { throw "Checksum mismatch!" }
   ```

3. **Run the Installer:**
   - **Double-click:** You can simply double-click `devstack-installer.exe` from Windows Explorer. Windows will prompt for Administrator elevation via UAC. A console window will show installation progress in real-time and pause when complete so you can review the results.
   - **From Terminal (PowerShell / Command Prompt):**
     ```powershell
     # Interactive run (elevates via UAC if not already elevated):
     .\devstack-installer.exe

     # Unattended / non-interactive run:
     .\devstack-installer.exe -y

     # Dry-run validation:
     .\devstack-installer.exe --dry-run -y
     ```

#### Installer Executable Command-Line Flags

| Flag | Default | Description |
|---|---|---|
| `-y`, `--unattended` | `false` | Run without interactive prompts or pause on exit. |
| `--install-dir <path>` | `%USERPROFILE%\.devstack` | Custom installation directory. |
| `--dry-run` | `false` | Validate environment and simulate installation without making system changes. |
| `--allow-reboot` | `false` | Allow automatic reboot scheduling if optional Windows features require a restart. |
| `--skip-wsl` | `false` | Skip WSL distro import and agent provisioning (host CLI and helper only). |
| `--upgrade` | `false` | Perform in-place upgrade of existing binaries with rollback safety. |
| `--uninstall` | `false` | Launch uninstallation of the stack. |

---

### Option B: From a Downloaded Release Archive (.zip)

If you prefer manual control or run in constrained environments where custom executables are restricted:

1. Download `devstack_<version>_windows_amd64.zip` and `checksums.txt`.
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

---

### Option C: Building and Installing from Source

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

### PowerShell Script Parameters (`install.ps1`)

When using `install.ps1` directly, the following flags are supported:

| Parameter | Default | Description |
|---|---|---|
| `-InstallDir <path>` | `%USERPROFILE%\.devstack` | Target root installation directory on Windows. |
| `-RootFSPath <path>` | auto-detected | Path to `debian-12-wsl-rootfs.tar.gz`. |
| `-AllowReboot` | `$false` | Allows the installer to schedule a resume task and reboot if optional Windows features were enabled. |
| `-SkipWSL` | `$false` | Skips WSL distro import and agent provisioning (host layout and helper only). |
| `-DryRun` | `$false` | Simulates checks and planned modifications without changing system state. |
| `-Upgrade` | `$false` | Performs in-place binary upgrades with automatic rollback on failure. |
| `-TestSandbox <path>` | `$null` | Isolates installation inside a test directory without modifying machine services. |

`install.ps1` and `uninstall.ps1` detect a legacy install at `C:\devstack` when
no `-InstallDir` is supplied and no `%USERPROFILE%\.devstack` install exists.
Pass `-InstallDir` explicitly to choose a different location. A fresh install
over a completed installation is refused; use `-Upgrade` for the existing
directory. `-DryRun` never writes to the install directory.

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
   The installer registers the `devstack-daemon` logon task and starts it in the
   current session, so the dashboard normally starts automatically. If it is not
   running, start it manually:
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
1. Refuses to continue unless a completed installation exists at the target.
2. Stops the running daemon and the `DevstackHelper` Windows service.
3. Backs up `devstack.exe` and `devstack-helper.exe` to `.bak`.
4. Backs up `/data/devstack` inside WSL to a verified temporary archive (`/data/devstack-backup.tar.gz`) before replacing binaries.
5. Stages updated Windows binaries and repairs/restarts the `DevstackHelper` service so it uses the target `helper.yaml`.
6. Stages the new `devstack-agent` into the WSL distribution and restarts `devstack-agent.service`.
7. Performs a health probe against the agent HTTPS API (when agent credentials already exist).
8. Restarts the user daemon.
9. If any staging or health step fails, initiates automatic rollback to the previous binaries and verified WSL state without data loss. A rollback that cannot be completed reports failure instead of claiming success.

---

## 6. Uninstallation

Uninstallation is performed using `uninstall.ps1` from an elevated prompt. If no
`-InstallDir` is supplied, the script detects a legacy `C:\devstack` install;
otherwise it targets `%USERPROFILE%\.devstack`:

```powershell
cd "$env:USERPROFILE\.devstack"
.\uninstall.ps1
```

By default, uninstallation:
- Stops and unregisters the `DevstackHelper` Windows service.
- Unregisters the user daemon scheduled startup task.
- Cleans the managed `.test` block from `C:\Windows\System32\drivers\etc\hosts`
  without rewriting the file when no block exists, and leaves an unterminated
  block untouched with a warning.
- Removes the devstack local CA from the Windows Root certificate store and
  deletes the CA private key material.
- Removes the inbound `devstack-daemon-proxy` Windows Firewall rule and the
  helper proxy rules.
- Removes the install's `bin` directory from `PATH`.
- **Preserves** all user projects, SQLite state, and the managed WSL distribution (`ext4.vhdx`).

The uninstaller refuses to remove a `DevstackHelper` service that belongs to a
different installation unless `-Force` is supplied. It also refuses to run from
a path that does not contain a devstack installation unless `-Force` is used to
remove machine-wide registrations only.

### Purge Options

To permanently remove user state or the WSL distro:

```powershell
# Remove the managed WSL distro and its ext4 disk:
.\uninstall.ps1 -PurgeDistro

# Remove DPAPI secrets, SQLite database, and the .devstack directory:
.\uninstall.ps1 -PurgeData -Force
```

`-PurgeDistro` refuses to delete the distro directory if `wsl --unregister`
fails. `-PurgeData` requires a valid `system\install-manifest.json` in the target
directory and fails instead of recursively deleting an unverified path.
