# Troubleshooting devstack on Windows

This guide provides diagnostic procedures and solutions for common operational
issues, port conflicts, WSL integration failures, and certificate warnings on
Windows.

---

## 1. Diagnostic First Step: `devstack doctor`

Whenever unexpected behavior occurs, begin by running the diagnostic suite:

```powershell
devstack doctor
```

For automated scripting or programmatic checks, pass `--json`:

```powershell
devstack doctor --json
```

To attempt automated remediation of recoverable items (such as missing firewall
rules, uninstalled root CA certs, or stale hosts entries):

```powershell
devstack doctor --fix
```

---

## 2. Common Errors and Resolutions

### `PORT_IN_USE`: Port 80 or 443 Already Bound

**Symptom:**
The helper service or daemon fails to start, reporting:
`listen tcp 127.0.0.1:80: bind: An attempt was made to access a socket in a way forbidden by its access permissions.`

**Cause:**
Another Windows service or web server is already listening on port 80 or 443. The most common culprit is Microsoft Internet Information Services (IIS) or the Windows HTTP service (`HTTP.sys`).

**Resolution:**
1. Identify the process holding port 80:
   ```powershell
   Get-Process -Id (Get-NetTCPConnection -LocalPort 80).OwningProcess
   ```
2. If the owning process is `System` (PID 4) or IIS:
   ```powershell
   # Stop and disable the World Wide Web Publishing Service (IIS)
   Stop-Service W3SVC -Force
   Set-Service W3SVC -StartupType Disabled
   ```
3. If SQL Server Reporting Services or Web Deploy is holding port 80:
   ```powershell
   Stop-Service ReportServer -Force -ErrorAction SilentlyContinue
   Stop-Service MsDepSvc -Force -ErrorAction SilentlyContinue
   ```
4. Restart the devstack helper:
   ```powershell
   Restart-Service DevstackHelper
   ```

---

### `WSL_UNAVAILABLE`: WSL Distro Failed to Start

**Symptom:**
`devstack doctor` reports `WSL_UNAVAILABLE`, or commands time out waiting for WSL.

**Causes & Fixes:**
1. **Virtualization Disabled:**
   Check whether hardware virtualization is enabled:
   ```powershell
   (Get-CimInstance Win32_Processor).VirtualizationFirmwareEnabled
   ```
   If `$false`, enter your BIOS/UEFI settings and enable Intel VT-x or AMD SVM.
2. **Outdated WSL Kernel:**
   Update the WSL kernel to the latest version:
   ```powershell
   wsl --update
   ```
3. **Hung WSL Subsystem:**
   If WSL processes have hung, terminate the subsystem and let devstack restart it:
   ```powershell
   wsl --shutdown
   devstack daemon restart
   ```

---

### `WSL_LOCALHOST_FORWARDING_UNAVAILABLE`: Cannot Reach WSL Backends

**Symptom:**
`devstack doctor` reports:
`WSL localhost forwarding is unavailable. Traffic to 127.0.0.1 cannot reach WSL listeners.`

**Causes & Fixes:**
1. **`.wslconfig` Misconfiguration:**
   Check `%USERPROFILE%\.wslconfig`. If it contains `localhostForwarding=false` or `networkingMode=mirrored`, remove or comment out those lines. devstack requires default NAT networking with localhost forwarding enabled.
2. **Third-Party VPN or Antivirus Interference:**
   Some corporate VPNs or firewall software bind all loopback adapters. Add an exclusion for the Hyper-V virtual switch adapter `vEthernet (WSL)`.

---

### `AGENT_UNAUTHENTICATED` or `AGENT_CERT_UNTRUSTED`: Agent API Failure

**Symptom:**
Daemon logs report:
`agent HTTPS request rejected: 401 Unauthorized` or `certificate fingerprint mismatch`.

**Cause:**
The DPAPI-protected secrets in `C:\devstack\system\agent-token` or `agent-pin` are out of sync with the keys stored inside the WSL distro at `/data/devstack/system/`.

**Resolution:**
Run the repair tool to resynchronize the DPAPI secrets:
```powershell
# Elevated PowerShell
.\install.ps1 -Upgrade
```
Or check file permissions inside WSL:
```powershell
wsl -d devstack -u root -- ls -la /data/devstack/system/
# token and server.key must be mode 0600
```

---

### `PATH_ESCAPE`: Path Outside Project Boundary

**Symptom:**
The CLI or dashboard rejects a site path with:
`PATH_ESCAPE: Path attempts to traverse outside the declared site root.`

**Cause:**
The project contains symbolic links or Windows NTFS directory junctions that point to locations outside the designated project root.

**Resolution:**
devstack enforces strict path containment to prevent arbitrary file disclosure. Inspect symlinks in your project:
```powershell
Get-ChildItem -Recurse | Where-Object { $_.LinkType } | Select-Object FullName, LinkType, Target
```
Remove or relocate external symlink targets inside the project directory.

---

### Untrusted Certificate Warnings in Browsers

**Symptom:**
Visiting `https://mysite.test/` displays an `ERR_CERT_AUTHORITY_INVALID` or `SEC_ERROR_UNKNOWN_ISSUER` warning in Chrome, Edge, or Firefox.

**Resolution:**
1. Repair the certificate trust store:
   ```powershell
   devstack trust repair
   ```
2. Verify the root CA exists in the Windows machine store:
   ```powershell
   Get-ChildItem Cert:\LocalMachine\Root | Where-Object { $_.Subject -match "devstack" }
   ```
3. If using Mozilla Firefox:
   Enable enterprise root certificate trust in Firefox:
   Navigate to `about:config` in Firefox and set:
   `security.enterprise_roots.enabled` = `true`.
4. Fully close and restart the browser to clear cached SSL state.

---

### Named Pipe Access Denied

**Symptom:**
`devstack` CLI reports:
`connecting to \\.\pipe\devstack-helper: Access is denied.`

**Cause:**
Under Decision S67, both the helper and daemon control pipes enforce strict Access Control Lists (ACLs) and verify the caller's Security Identifier (SID). A foreign local user or non-administrator process cannot access another user's pipes.

**Resolution:**
Ensure you are running the `devstack` command under the exact same Windows user account that originally installed the software. Run `whoami` to verify your active username.
