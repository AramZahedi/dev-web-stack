<#
.SYNOPSIS
    Devstack Windows Prerequisites, Layout, & Helper Service Installer (System Phase)
.DESCRIPTION
    Elevated system-phase installer for devstack on Windows.
    Validates Windows build (10 22H2 19045+ x64 or Windows 11), checks hardware
    virtualization, enables required Windows features (WSL2 & VirtualMachinePlatform),
    orchestrates reboot handling if required (exit code 3010), provisions C:\devstack
    layout with protected DACLs, stages binaries, registers the helper Windows service,
    and configures inbound WSL proxy firewall rules.
.PARAMETER InstallDir
    Root installation directory. Defaults to C:\devstack.
.PARAMETER RootFSPath
    Path to WSL rootfs archive (reserved for WSL phase).
.PARAMETER AllowReboot
    Automatically schedule resume task and reboot if Windows features require a restart.
.PARAMETER SkipWSL
    Skip WSL optional features and distro provisioning.
.PARAMETER DryRun
    Simulate prerequisite checks and installation steps without altering system state.
.PARAMETER TestSandbox
    Run in an isolated directory for testing, skipping machine-wide modifications.
.PARAMETER Help
    Show usage and help information.
#>

[CmdletBinding()]
param(
    [string]$InstallDir = "C:\devstack",
    [string]$RootFSPath = "",
    [switch]$AllowReboot,
    [switch]$SkipWSL,
    [switch]$DryRun,
    [string]$TestSandbox = "",
    [switch]$Upgrade,
    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Write-InstallerError([string]$Message) {
    [Console]::Error.WriteLine("devstack installer: $Message")
}

function Show-Usage {
    Write-Host @"
Usage: install.ps1 [-InstallDir <path>] [-RootFSPath <path>] [-AllowReboot] [-SkipWSL] [-DryRun] [-TestSandbox <path>] [-Upgrade] [-Help]

Parameters:
    -InstallDir <path>     Installation target directory (default: C:\devstack)
    -RootFSPath <path>     Path to WSL rootfs tar.gz archive
    -AllowReboot           Allow automatic reboot and schedule resume if required by Windows features
    -SkipWSL               Skip WSL feature enablement and distro provisioning
    -DryRun                Print planned operations without modifying system
    -TestSandbox <path>    Run layout and configuration inside an isolated test directory
    -Upgrade               Upgrade existing devstack installation with rollback on failure
    -Help                  Display this help message

Exit codes:
    0    Success
    1    General error
    2    Elevation required or prerequisite missing
    3010 System reboot required to complete feature installation
"@
}

if ($Help) {
    Show-Usage
    exit 0
}

# Resolve current PowerShell executable dynamically
$psExe = try { (Get-Process -Id $PID).Path } catch { $null }
if (-not $psExe -or -not (Test-Path $psExe)) {
    $psCmd = Get-Command pwsh.exe -ErrorAction SilentlyContinue
    if ($null -ne $psCmd) {
        $psExe = $psCmd.Source
    } else {
        $psCmd = Get-Command powershell.exe -ErrorAction SilentlyContinue
        $psExe = if ($null -ne $psCmd) { $psCmd.Source } else { "powershell.exe" }
    }
}

# Determine effective target directory
$repoRoot = $PSScriptRoot
$targetDir = if (-not [string]::IsNullOrEmpty($TestSandbox)) { $TestSandbox } else { $InstallDir }
$isSandbox = -not [string]::IsNullOrEmpty($TestSandbox)

# -----------------------------------------------------------------------------
# 1. Elevation check
# -----------------------------------------------------------------------------
function Test-IsElevated {
    if ($env:DEVSTACK_TEST_ELEVATED -eq "false") {
        return $false
    }
    if ($env:DEVSTACK_TEST_ELEVATED -eq "true") {
        return $true
    }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

$elevated = Test-IsElevated
if (-not $elevated) {
    if ($DryRun -or $isSandbox) {
        Write-Host "devstack installer: running non-elevated (DryRun or TestSandbox active)."
    } else {
        Write-InstallerError "Administrative elevation is required. Please run this script in an elevated PowerShell session (Run as Administrator)."
        exit 2
    }
}

# -----------------------------------------------------------------------------
# 2. OS Build and Architecture Check
# -----------------------------------------------------------------------------
$detectedArch = if ($env:DEVSTACK_TEST_OS_ARCH) { $env:DEVSTACK_TEST_OS_ARCH } else { $env:PROCESSOR_ARCHITECTURE }
$is64Bit = if ($env:DEVSTACK_TEST_OS_ARCH) {
    $env:DEVSTACK_TEST_OS_ARCH -match "64|AMD64"
} else {
    [Environment]::Is64BitOperatingSystem
}

if (-not $is64Bit) {
    Write-InstallerError "Devstack requires a 64-bit (x64) Windows operating system. Detected architecture: $detectedArch."
    exit 2
}

$detectedBuild = if ($env:DEVSTACK_TEST_OS_BUILD) {
    [int]$env:DEVSTACK_TEST_OS_BUILD
} else {
    [Environment]::OSVersion.Version.Build
}

if ($detectedBuild -lt 19045) {
    Write-InstallerError "Windows 10 22H2 (build 19045+) x64 or Windows 11 (build 22000+) is required. Detected build: $detectedBuild."
    exit 2
}

Write-Host "devstack installer: OS build $detectedBuild ($detectedArch) verified."

# -----------------------------------------------------------------------------
# 3. Hardware Virtualization Check
# -----------------------------------------------------------------------------
$virtEnabled = $false
if ($env:DEVSTACK_TEST_VIRTUALIZATION -eq "false") {
    $virtEnabled = $false
} elseif ($env:DEVSTACK_TEST_VIRTUALIZATION -eq "true") {
    $virtEnabled = $true
} else {
    try {
        $proc = Get-CimInstance -ClassName Win32_Processor -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -ne $proc -and $proc.VirtualizationFirmwareEnabled) {
            $virtEnabled = $true
        }
    } catch {}

    if (-not $virtEnabled) {
        try {
            $cs = Get-CimInstance -ClassName Win32_ComputerSystem -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($null -ne $cs -and $cs.HypervisorPresent) {
                $virtEnabled = $true
            }
        } catch {}
    }
}

if (-not $virtEnabled) {
    Write-InstallerError "Hardware virtualization is disabled in system firmware (BIOS/UEFI). Please enable Intel VT-x or AMD-V in firmware settings."
    exit 2
}

Write-Host "devstack installer: Hardware virtualization enabled."

# -----------------------------------------------------------------------------
# 4. Directory Layout Creation & DACL
# -----------------------------------------------------------------------------
$binDir = Join-Path $targetDir "bin"
$systemDir = Join-Path $targetDir "system"
$wslDistroDir = Join-Path $targetDir "wsl\devstack"

if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would create directory layout: $binDir, $systemDir, $wslDistroDir"
} else {
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    New-Item -ItemType Directory -Force -Path $systemDir | Out-Null
    New-Item -ItemType Directory -Force -Path $wslDistroDir | Out-Null

    # Stage uninstaller to install root directory
    $srcUninstall = Join-Path $repoRoot "uninstall.ps1"
    $dstUninstall = Join-Path $targetDir "uninstall.ps1"
    if (Test-Path $srcUninstall) {
        Copy-Item -Path $srcUninstall -Destination $dstUninstall -Force
    }

    # Apply protected DACL to system directory
    $acl = Get-Acl -Path $systemDir
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($rule in @($acl.Access)) {
        $acl.RemoveAccessRule($rule) | Out-Null
    }

    $sidSystem = New-Object Security.Principal.SecurityIdentifier([Security.Principal.WellKnownSidType]::LocalSystemSid, $null)
    $sidAdmins = New-Object Security.Principal.SecurityIdentifier([Security.Principal.WellKnownSidType]::BuiltinAdministratorsSid, $null)
    $sidUsers = New-Object Security.Principal.SecurityIdentifier([Security.Principal.WellKnownSidType]::BuiltinUsersSid, $null)

    $rightsFull = [Security.AccessControl.FileSystemRights]::FullControl
    $rightsRead = [Security.AccessControl.FileSystemRights]::ReadAndExecute -bor [Security.AccessControl.FileSystemRights]::Synchronize
    $inheritFlags = [Security.AccessControl.InheritanceFlags]"ContainerInherit, ObjectInherit"
    $propFlags = [Security.AccessControl.PropagationFlags]::None
    $allow = [Security.AccessControl.AccessControlType]::Allow

    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sidSystem, $rightsFull, $inheritFlags, $propFlags, $allow)))
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sidAdmins, $rightsFull, $inheritFlags, $propFlags, $allow)))
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sidUsers, $rightsRead, $inheritFlags, $propFlags, $allow)))

    Set-Acl -Path $systemDir -AclObject $acl
    Write-Host "devstack installer: Directory layout created with protected system DACL."
}

# -----------------------------------------------------------------------------
# 5. Optional Windows Features Check & Enablement
# -----------------------------------------------------------------------------
$rebootNeeded = $false
$featuresStatus = @{
    "Microsoft-Windows-Subsystem-Linux" = "Unknown"
    "VirtualMachinePlatform" = "Unknown"
}

if ($SkipWSL) {
    Write-Host "devstack installer: Skipping WSL optional features check (-SkipWSL specified)."
} elseif ($env:DEVSTACK_TEST_REBOOT_REQUIRED -eq "true") {
    $rebootNeeded = $true
    $featuresStatus["Microsoft-Windows-Subsystem-Linux"] = "PendingRestart"
    $featuresStatus["VirtualMachinePlatform"] = "PendingRestart"
} elseif ($DryRun -or $isSandbox) {
    $featuresStatus["Microsoft-Windows-Subsystem-Linux"] = "Enabled"
    $featuresStatus["VirtualMachinePlatform"] = "Enabled"
} else {
    $features = @("Microsoft-Windows-Subsystem-Linux", "VirtualMachinePlatform")
    foreach ($feat in $features) {
        $featInfo = Get-WindowsOptionalFeature -Online -FeatureName $feat -ErrorAction SilentlyContinue
        if ($null -ne $featInfo -and $featInfo.State -eq "Enabled") {
            $featuresStatus[$feat] = "Enabled"
        } else {
            Write-Host "devstack installer: Enabling optional feature $feat..."
            $res = Enable-WindowsOptionalFeature -Online -FeatureName $feat -All -NoRestart -ErrorAction Stop
            if ($res.RestartNeeded) {
                $rebootNeeded = $true
                $featuresStatus[$feat] = "PendingRestart"
            } else {
                $featuresStatus[$feat] = "Enabled"
            }
        }
    }
}

# -----------------------------------------------------------------------------
# 6. Manifest Writing & Reboot Handling
# -----------------------------------------------------------------------------
$manifestPath = Join-Path $systemDir "install-manifest.json"
$manifest = [ordered]@{
    version = "1.0"
    created_at = (Get-Date -Format "o")
    updated_at = (Get-Date -Format "o")
    os_build = $detectedBuild
    os_arch = $detectedArch
    virtualization_enabled = $virtEnabled
    features = $featuresStatus
    layout_created = $true
    reboot_required = $rebootNeeded
    stage = if ($rebootNeeded) { "reboot-pending" } else { "system-phase-complete" }
}

if (-not $DryRun) {
    $manifestJson = ConvertTo-Json $manifest -Depth 5
    Set-Content -Path $manifestPath -Value $manifestJson -Encoding utf8
}

if ($rebootNeeded) {
    Write-Host "devstack installer: System restart is required to complete Windows feature enablement."
    if ($AllowReboot -and -not $isSandbox) {
        Write-Host "devstack installer: Scheduling resume task and initiating restart..."
        $resumeTaskName = "devstack-installer-resume"
        $action = New-ScheduledTaskAction -Execute $psExe -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
        $trigger = New-ScheduledTaskTrigger -AtLogOn
        $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
        Register-ScheduledTask -TaskName $resumeTaskName -Action $action -Trigger $trigger -Principal $principal -Force | Out-Null
        Restart-Computer -Force
    } else {
        Write-Host "devstack installer: Please reboot your computer and run install.ps1 again to resume."
    }
    exit 3010
}

# Clean up resume task if it exists
if (-not $DryRun -and -not $isSandbox) {
    Unregister-ScheduledTask -TaskName "devstack-installer-resume" -Confirm:$false -ErrorAction SilentlyContinue
}

# -----------------------------------------------------------------------------
# 7. Helper Configuration
# -----------------------------------------------------------------------------
$helperYamlPath = Join-Path $systemDir "helper.yaml"
if (-not $DryRun -and -not (Test-Path $helperYamlPath)) {
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $helperYaml = @"
allow_sid: "$currentSid"
pipe: "\\\\.\\pipe\\devstack-helper"
proxy_http_port: 8080
proxy_https_port: 8443
"@
    Set-Content -Path $helperYamlPath -Value $helperYaml -Encoding utf8
}

# -----------------------------------------------------------------------------
# 8. Binary Staging & System PATH
# -----------------------------------------------------------------------------
$repoRoot = $PSScriptRoot
$dstDevstack = Join-Path $binDir "devstack.exe"
$dstHelper = Join-Path $binDir "devstack-helper.exe"

if ($Upgrade) {
    Write-Host "devstack installer: Stopping daemon for upgrade..."
    if (Test-Path $dstDevstack) {
        try { & "$dstDevstack" daemon stop 2>$null | Out-Null } catch {}
    }
    try {
        Get-Process -Name "devstack" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    } catch {}

    if (Test-Path $dstDevstack) {
        Copy-Item -Path $dstDevstack -Destination "$dstDevstack.bak" -Force
    }
    if (Test-Path $dstHelper) {
        Copy-Item -Path $dstHelper -Destination "$dstHelper.bak" -Force
    }
}

if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would stage binaries to $binDir"
} elseif (-not $isSandbox) {
    # Check prebuilt binaries
    $prebuiltDevstack = @(
        (Join-Path $repoRoot "devstack.exe"),
        (Join-Path $repoRoot "bin\devstack.exe"),
        (Join-Path $repoRoot "build\windows\devstack.exe")
    ) | Where-Object { Test-Path $_ } | Select-Object -First 1

    $prebuiltHelper = @(
        (Join-Path $repoRoot "devstack-helper.exe"),
        (Join-Path $repoRoot "bin\devstack-helper.exe"),
        (Join-Path $repoRoot "build\windows\devstack-helper.exe")
    ) | Where-Object { Test-Path $_ } | Select-Object -First 1

    Push-Location $repoRoot
    try {
        if ($null -ne $prebuiltDevstack) {
            Copy-Item -Path $prebuiltDevstack -Destination $dstDevstack -Force
        } elseif (Test-Path (Join-Path $repoRoot "cmd\devstack\main.go")) {
            Write-Host "devstack installer: Building devstack.exe..."
            & go build -o $dstDevstack ./cmd/devstack
        }

        if ($null -ne $prebuiltHelper) {
            Copy-Item -Path $prebuiltHelper -Destination $dstHelper -Force
        } elseif (Test-Path (Join-Path $repoRoot "cmd\devstack-helper\main.go")) {
            Write-Host "devstack installer: Building devstack-helper.exe..."
            & go build -o $dstHelper ./cmd/devstack-helper
        }
    } finally {
        Pop-Location
    }

    # Add binDir to Machine PATH
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $paths = ($machinePath -split ";") | Where-Object { $_ -ne "" }
    if ($paths -notcontains $binDir) {
        $newPath = "$binDir;$machinePath"
        [Environment]::SetEnvironmentVariable("Path", $newPath, "Machine")
        $env:Path = "$binDir;$env:Path"
        Write-Host "devstack installer: Added $binDir to Machine PATH."
    }
}

# -----------------------------------------------------------------------------
# 9. Helper Service Registration & Startup
# -----------------------------------------------------------------------------
if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would register and start DevstackHelper service"
} elseif (-not $isSandbox -and (Test-Path $dstHelper)) {
    Write-Host "devstack installer: Installing helper service..."
    & "$dstHelper" service install --config "$helperYamlPath"
    try {
        Start-Service -Name "DevstackHelper" -ErrorAction Stop
        Write-Host "devstack installer: DevstackHelper service started."
    } catch {
        & sc.exe start DevstackHelper | Out-Null
    }
}

# -----------------------------------------------------------------------------
# 10. Inbound Windows Firewall Rule for WSL Proxy
# -----------------------------------------------------------------------------
$ruleName = "devstack-daemon-proxy"
$displayName = "Devstack Daemon Proxy"
$ports = @("8080", "8443")

if ($DryRun -or $isSandbox -or ($env:DEVSTACK_TEST_FIREWALL_CHECK -eq "true")) {
    Write-Host "devstack installer: Firewall rule '$ruleName' (DisplayName: '$displayName') Protocol: TCP Ports: $($ports -join ',') Direction: Inbound Action: Allow Scoped: WSL"
}

if (-not $DryRun -and -not $isSandbox) {
    # Detect WSL adapter or subnet
    $wslSubnet = $null
    $wslAdapter = Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.InterfaceDescription -match "Hyper-V Virtual Ethernet" -and $_.Name -match "WSL" }
    if ($null -ne $wslAdapter) {
        $ip = Get-NetIPAddress -InterfaceIndex $wslAdapter.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -ne $ip) {
            $wslSubnet = "$($ip.IPAddress)/$($ip.PrefixLength)"
        }
    }

    Get-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue

    $fwParams = @{
        Name = $ruleName
        DisplayName = $displayName
        Description = "Inbound proxy rule for devstack daemon allowing traffic from WSL"
        Direction = "Inbound"
        Action = "Allow"
        Protocol = "TCP"
        LocalPort = $ports
        Profile = "Private,Domain"
        Enabled = "True"
    }
    if ($null -ne $wslAdapter) {
        $fwParams["InterfaceAlias"] = $wslAdapter.Name
    }
    if ($null -ne $wslSubnet) {
        $fwParams["RemoteAddress"] = $wslSubnet
    }

    try {
        New-NetFirewallRule @fwParams -ErrorAction Stop | Out-Null
        Write-Host "devstack installer: Created inbound firewall rule '$ruleName' for ports $($ports -join ', ')."
    } catch {
        $portStr = $ports -join ","
        $remoteStr = if ($wslSubnet) { "remoteip=$wslSubnet" } else { "" }
        & netsh.exe advfirewall firewall add rule name="$ruleName" dir=in action=allow protocol=TCP localport=$portStr profile=private $remoteStr | Out-Null
    }
}

if ($SkipWSL) {
    Write-Host "devstack installer: Skipping WSL distro and agent provisioning (-SkipWSL specified)."
    Write-Host "devstack installer: System phase completed successfully."
    exit 0
}

# -----------------------------------------------------------------------------
# 10.1 Upgrade Flow & Transactional Rollback
# -----------------------------------------------------------------------------
if ($Upgrade) {
    Write-Host "devstack installer: Starting upgrade flow..."

    # 1. Backup /data/devstack inside WSL
    $mockWSLRoot = Join-Path $targetDir "mock-wsl"
    $mockData = Join-Path $mockWSLRoot "data\devstack"
    $mockDataBin = Join-Path $mockData "bin"
    $mockBackupTar = Join-Path $mockWSLRoot "data\devstack-backup.tar.gz"
    $mockBackupDir = Join-Path $mockWSLRoot "data\devstack-backup"

    if ($DryRun) {
        Write-Host "devstack installer: [DryRun] Would backup /data/devstack to /data/devstack-backup.tar.gz"
        Write-Host "devstack installer: [DryRun] Would stage updated binaries and restart devstack-agent.service"
        Write-Host "devstack installer: [DryRun] Would verify agent health and rollback on error"
        exit 0
    }

    Write-Host "devstack installer: Backing up /data/devstack to /data/devstack-backup.tar.gz..."
    if ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
        if (Test-Path $mockData) {
            if (Test-Path $mockBackupDir) { Remove-Item -Path $mockBackupDir -Recurse -Force }
            Copy-Item -Path $mockData -Destination $mockBackupDir -Recurse -Force
            Set-Content -Path $mockBackupTar -Value "mock-backup-archive" -Encoding utf8
        }
        if (Test-Path (Join-Path $mockDataBin "devstack-agent")) {
            Copy-Item -Path (Join-Path $mockDataBin "devstack-agent") -Destination (Join-Path $mockDataBin "devstack-agent.bak") -Force
        }
    } else {
        & wsl.exe -d devstack -u root -- sh -c "tar -czf /data/devstack-backup.tar.gz -C /data devstack && cp /data/devstack/bin/devstack-agent /data/devstack/bin/devstack-agent.bak 2>/dev/null || true"
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "Failed to backup /data/devstack inside WSL distro."
            exit 1
        }
    }

    # 2. Stage updated agent binary
    $stagedAgentLinux = Join-Path $binDir "devstack-agent"
    $prebuiltAgentLinux = @(
        (Join-Path $repoRoot "devstack-agent"),
        (Join-Path $repoRoot "build\wsl\devstack-agent"),
        (Join-Path $repoRoot "bin\linux_amd64\devstack-agent"),
        (Join-Path $binDir "devstack-agent")
    ) | Where-Object { Test-Path $_ } | Select-Object -First 1

    if ($null -ne $prebuiltAgentLinux) {
        Copy-Item -Path $prebuiltAgentLinux -Destination $stagedAgentLinux -Force
    } elseif (Test-Path (Join-Path $repoRoot "cmd\devstack-agent\main.go")) {
        Push-Location $repoRoot
        try {
            $prevGOOS = $env:GOOS
            $prevGOARCH = $env:GOARCH
            $env:GOOS = "linux"
            $env:GOARCH = "amd64"
            & go build -o $stagedAgentLinux ./cmd/devstack-agent
        } catch {} finally {
            $env:GOOS = $prevGOOS
            $env:GOARCH = $prevGOARCH
            Pop-Location
        }
    }

    if (-not (Test-Path $stagedAgentLinux)) {
        Set-Content -Path $stagedAgentLinux -Value "#!/bin/sh`necho devstack-agent`n" -Encoding utf8
    }

    if ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
        Copy-Item -Path $stagedAgentLinux -Destination (Join-Path $mockDataBin "devstack-agent") -Force
    } else {
        $cleanAgentPath = $stagedAgentLinux.Replace('\', '/')
        $wslAgentSrc = try {
            (& wsl.exe -d devstack -u root -- wslpath -a -u "$cleanAgentPath" 2>$null | Out-String).Trim()
        } catch { "" }
        if (-not $wslAgentSrc -and ($cleanAgentPath -match "^([a-zA-Z]):/(.*)$")) {
            $wslAgentSrc = "/mnt/$($Matches[1].ToLower())/$($Matches[2])"
        }
        if ($wslAgentSrc) {
            & wsl.exe -d devstack -u root -- sh -c "mkdir -p /data/devstack/bin && cp '$wslAgentSrc' /data/devstack/bin/devstack-agent && chmod 0755 /data/devstack/bin/devstack-agent"
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "Failed to copy updated devstack-agent binary into WSL distro."
                $upgradeFailed = $true
            }
        }
    }

    # 3. Restart devstack-agent.service and verify health
    Write-Host "devstack installer: Restarting devstack-agent.service and verifying health..."
    $upgradeFailed = $false
    if ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
        if (($env:DEVSTACK_TEST_UPGRADE_FAIL -eq "true") -or ($env:DEVSTACK_TEST_AGENT_HEALTH -eq "failing")) {
            $upgradeFailed = $true
        }
    } else {
        & wsl.exe -d devstack -u root -- systemctl restart devstack-agent.service
        $healthy = $false
        for ($i = 0; $i -lt 10; $i++) {
            Start-Sleep -Seconds 1
            $st = try { (& wsl.exe -d devstack -u root -- systemctl is-active devstack-agent.service 2>$null).Trim() } catch { "" }
            if ($st -eq "active") {
                $healthy = $true
                break
            }
        }
        if (-not $healthy) {
            $upgradeFailed = $true
        }
    }

    # 4. Handle rollback on error
    if ($upgradeFailed) {
        Write-InstallerError "devstack-agent health check failed. Initiating rollback..."
        if ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
            if (Test-Path $mockBackupDir) {
                Remove-Item -Path $mockData -Recurse -Force -ErrorAction SilentlyContinue
                Copy-Item -Path $mockBackupDir -Destination $mockData -Recurse -Force
                Remove-Item -Path $mockBackupDir -Recurse -Force -ErrorAction SilentlyContinue
            }
            if (Test-Path (Join-Path $mockDataBin "devstack-agent.bak")) {
                Move-Item -Path (Join-Path $mockDataBin "devstack-agent.bak") -Destination (Join-Path $mockDataBin "devstack-agent") -Force
            }
            Remove-Item -Path $mockBackupTar -Force -ErrorAction SilentlyContinue
        } else {
            & wsl.exe -d devstack -u root -- sh -c "rm -rf /data/devstack && tar -xzf /data/devstack-backup.tar.gz -C /data && rm -f /data/devstack-backup.tar.gz"
            & wsl.exe -d devstack -u root -- systemctl restart devstack-agent.service 2>$null
        }
        if (Test-Path "$dstDevstack.bak") {
            Move-Item -Path "$dstDevstack.bak" -Destination $dstDevstack -Force
        }
        if (Test-Path "$dstHelper.bak") {
            Move-Item -Path "$dstHelper.bak" -Destination $dstHelper -Force
        }
        Write-InstallerError "Upgrade rolled back successfully to previous version."
        exit 1
    }

    # 5. Success cleanup and manifest update
    Write-Host "devstack installer: Agent health verified successfully."
    if ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
        Remove-Item -Path $mockBackupTar -Force -ErrorAction SilentlyContinue
        Remove-Item -Path $mockBackupDir -Recurse -Force -ErrorAction SilentlyContinue
        Remove-Item -Path (Join-Path $mockDataBin "devstack-agent.bak") -Force -ErrorAction SilentlyContinue
    } else {
        & wsl.exe -d devstack -u root -- rm -f /data/devstack-backup.tar.gz /data/devstack/bin/devstack-agent.bak
    }
    Remove-Item -Path "$dstDevstack.bak" -Force -ErrorAction SilentlyContinue
    Remove-Item -Path "$dstHelper.bak" -Force -ErrorAction SilentlyContinue

    $manifestPath = Join-Path $systemDir "install-manifest.json"
    $manifestObj = if (Test-Path $manifestPath) {
        try { Get-Content $manifestPath -Raw | ConvertFrom-Json } catch { $null }
    } else { $null }

    $manifestDict = [ordered]@{}
    if ($null -ne $manifestObj) {
        foreach ($prop in $manifestObj.PSObject.Properties) {
            $manifestDict[$prop.Name] = $prop.Value
        }
    }
    $manifestDict["upgraded_at"] = (Get-Date -Format "o")
    $manifestDict["updated_at"] = (Get-Date -Format "o")
    $manifestDict["stage"] = "complete"
    $manifestDict["agent_installed"] = $true

    $manifestJson = ConvertTo-Json $manifestDict -Depth 5
    Set-Content -Path $manifestPath -Value $manifestJson -Encoding utf8

    Write-Host "devstack installer: Upgrade completed successfully."
    exit 0
}

# -----------------------------------------------------------------------------
# 11. WSL Managed Distro, RootFS, & Agent Service Provisioning (WSL Phase)
# -----------------------------------------------------------------------------
function Get-WSLVersionString {
    if ($env:DEVSTACK_TEST_WSL_VERSION) {
        if ($env:DEVSTACK_TEST_WSL_VERSION -eq "missing") {
            return $null
        }
        return $env:DEVSTACK_TEST_WSL_VERSION
    }
    try {
        $outRaw = & wsl.exe --version 2>&1 | Out-String
        $clean = ($outRaw -replace "`0", "").Trim()
        if ($clean -match "(?m)^\s*WSL version:\s*([0-9]+\.[0-9]+\.[0-9]+)") {
            return $Matches[1]
        }
    } catch {}
    return $null
}

function Compare-VersionString([string]$v1, [string]$v2) {
    $a1 = $v1.Split(".") | ForEach-Object { [int]$_ }
    $a2 = $v2.Split(".") | ForEach-Object { [int]$_ }
    for ($i = 0; $i -lt [Math]::Max($a1.Length, $a2.Length); $i++) {
        $p1 = if ($i -lt $a1.Length) { $a1[$i] } else { 0 }
        $p2 = if ($i -lt $a2.Length) { $a2[$i] } else { 0 }
        if ($p1 -lt $p2) { return -1 }
        if ($p1 -gt $p2) { return 1 }
    }
    return 0
}

Write-Host "devstack installer: Starting WSL provisioning phase..."

# 11.1 WSL runtime version check (>= 2.0.0)
$wslVer = Get-WSLVersionString
if (-not $wslVer -or (Compare-VersionString $wslVer "2.0.0") -lt 0) {
    if (-not $isSandbox -and -not $DryRun -and -not $env:DEVSTACK_TEST_WSL_VERSION) {
        Write-Host "devstack installer: Outdated or missing WSL runtime. Attempting 'wsl.exe --update'..."
        try {
            & wsl.exe --update
            $wslVer = Get-WSLVersionString
        } catch {}
    }
    if (-not $wslVer -or (Compare-VersionString $wslVer "2.0.0") -lt 0) {
        Write-InstallerError "WSL 2.0.0 or newer is required (detected: $(if ($wslVer) { $wslVer } else { 'none' })). Please install or update WSL using 'wsl --update'."
        exit 2
    }
}
Write-Host "devstack installer: WSL runtime version $wslVer verified (>= 2.0.0)."

# 11.2 .wslconfig check enforcing NAT networking
$wslConfigPath = if ($env:DEVSTACK_TEST_WSLCONFIG) {
    $env:DEVSTACK_TEST_WSLCONFIG
} else {
    Join-Path $env:USERPROFILE ".wslconfig"
}

if (Test-Path $wslConfigPath) {
    $lines = Get-Content -Path $wslConfigPath
    $currentSection = ""
    $networkingMode = ""
    foreach ($rawLine in $lines) {
        $line = $rawLine.Trim()
        if ($line.Contains("#")) {
            $line = $line.Substring(0, $line.IndexOf("#")).Trim()
        }
        if ($line.StartsWith("[") -and $line.EndsWith("]")) {
            $currentSection = $line.Substring(1, $line.Length - 2).Trim().ToLower()
            continue
        }
        if ($currentSection -eq "wsl2" -and $line.Contains("=")) {
            $parts = $line.Split("=", 2)
            $k = $parts[0].Trim()
            $v = $parts[1].Trim()
            if ($k -ieq "networkingMode") {
                $networkingMode = $v.ToLower()
            }
        }
    }

    if ($networkingMode -and $networkingMode -ne "nat") {
        Write-InstallerError "Devstack requires WSL NAT networking. Found networkingMode=$networkingMode in $wslConfigPath. Mirrored or non-NAT networking is unsupported. Please remove or set networkingMode=nat."
        exit 2
    }
}
Write-Host "devstack installer: WSL NAT networking verified."

# 11.3 RootFS archive acquisition & SHA256 checksum verification
$versionsLockPath = Join-Path $repoRoot "build\wsl\versions.lock"
$wslVersions = @{}
if (Test-Path $versionsLockPath) {
    Get-Content -Path $versionsLockPath | ForEach-Object {
        $l = $_.Trim()
        if ($l -and -not $l.StartsWith("#") -and $l.Contains("=")) {
            $idx = $l.IndexOf("=")
            $k = $l.Substring(0, $idx).Trim()
            $v = $l.Substring($idx + 1).Trim()
            $wslVersions[$k] = $v
        }
    }
}

$effectiveRootFS = $RootFSPath
if ([string]::IsNullOrEmpty($effectiveRootFS)) {
    $defaultCandidate = @((Join-Path $repoRoot "debian-12-wsl-rootfs.tar.gz"), (Join-Path $repoRoot "build\wsl\debian-12-wsl-rootfs.tar.gz")) | Where-Object { Test-Path $_ } | Select-Object -First 1
    if (Test-Path $defaultCandidate) {
        $effectiveRootFS = $defaultCandidate
    } elseif ($DryRun -or $isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
        $mockDir = Join-Path $targetDir "mock-rootfs"
        New-Item -ItemType Directory -Force -Path $mockDir | Out-Null
        $effectiveRootFS = Join-Path $mockDir "debian-12-wsl-rootfs.tar.gz"
        if (-not (Test-Path $effectiveRootFS)) {
            Set-Content -Path $effectiveRootFS -Value "mock rootfs archive content" -Encoding utf8
        }
    }
}

if (-not $DryRun -and (-not $effectiveRootFS -or -not (Test-Path $effectiveRootFS))) {
    Write-InstallerError "WSL rootfs archive not found. Expected at '$effectiveRootFS'. Please supply -RootFSPath <path>."
    exit 2
}

if (Test-Path $effectiveRootFS) {
    $actualHash = (Get-FileHash -Path $effectiveRootFS -Algorithm SHA256).Hash.ToLower()
    $expectedHash = $null
    if ($env:DEVSTACK_TEST_ROOTFS_SHA256) {
        $expectedHash = $env:DEVSTACK_TEST_ROOTFS_SHA256.ToLower()
    } elseif ($wslVersions.ContainsKey("rootfs_sha256") -and $wslVersions["rootfs_sha256"] -match "^[0-9a-fA-F]{64}$") {
        $expectedHash = $wslVersions["rootfs_sha256"].ToLower()
    } else {
        $sidecar = "$effectiveRootFS.sha256"
        if (Test-Path $sidecar) {
            $expectedHash = ((Get-Content -Path $sidecar -Raw) -split '\s+')[0].Trim().ToLower()
        }
    }

    if ($expectedHash -and ($actualHash -ne $expectedHash)) {
        Write-InstallerError "Rootfs SHA256 checksum mismatch for $effectiveRootFS. Expected: $expectedHash, got: $actualHash."
        exit 2
    }
    Write-Host "devstack installer: RootFS archive checksum verified ($actualHash)."
}

# 11.4 Managed distro import & unmanaged distro check
$distroExists = $false
$isDistroManaged = $false

if ($env:DEVSTACK_TEST_DISTRO_EXISTS -eq "unmanaged") {
    $distroExists = $true
    $isDistroManaged = $false
} elseif ($env:DEVSTACK_TEST_DISTRO_EXISTS -eq "managed") {
    $distroExists = $true
    $isDistroManaged = $true
} elseif ($DryRun -or $isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
    $distroExists = $false
} else {
    try {
        $distroListRaw = & wsl.exe --list --quiet 2>&1 | Out-String
        $distroList = ($distroListRaw -replace "`0", "").Split("`r`n") | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" }
        if ($distroList -contains "devstack") {
            $distroExists = $true
            $managedOut = & wsl.exe -d devstack -u root -- cat /data/devstack/managed.json 2>$null
            if ($managedOut) {
                try {
                    $mObj = $managedOut | ConvertFrom-Json
                    if ($mObj.managed_by -eq "devstack") {
                        $isDistroManaged = $true
                    }
                } catch {}
            }
        }
    } catch {}
}

if ($distroExists -and -not $isDistroManaged) {
    Write-InstallerError "Existing WSL distribution 'devstack' is not managed by devstack (/data/devstack/managed.json missing or invalid). Refusing to adopt unmanaged distro."
    exit 2
}

if (-not $distroExists) {
    if ($DryRun) {
        Write-Host "devstack installer: [DryRun] Would import distro: wsl.exe --import devstack `"$wslDistroDir`" `"$effectiveRootFS`" --version 2"
    } elseif (-not $isSandbox -and ($env:DEVSTACK_TEST_MOCK_WSL -ne "true")) {
        Write-Host "devstack installer: Importing devstack WSL distribution into $wslDistroDir..."
        & wsl.exe --import devstack "$wslDistroDir" "$effectiveRootFS" --version 2
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "Failed to import WSL distribution 'devstack'."
            exit 1
        }
    } else {
        Write-Host "devstack installer: [TestSandbox] Simulating distro import into $wslDistroDir."
    }
}

# 11.5 /etc/wsl.conf and /data/devstack/managed.json configuration
$installId = [Guid]::NewGuid().ToString("D")
$installedAt = (Get-Date -Format "o")

$wslConfContent = @"
[boot]
systemd=true

[network]
generateHosts=false
generateResolvConf=true

[automount]
enabled=true
options="metadata,umask=22,fmask=11"
"@

$managedJsonContent = ConvertTo-Json ([ordered]@{
    managed_by = "devstack"
    install_id = $installId
    installed_at = $installedAt
}) -Depth 3

if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would configure /etc/wsl.conf and /data/devstack/managed.json"
} elseif ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
    $mockWSLRoot = Join-Path $targetDir "mock-wsl"
    $mockEtc = Join-Path $mockWSLRoot "etc"
    $mockSystemd = Join-Path $mockWSLRoot "etc\systemd\system"
    $mockData = Join-Path $mockWSLRoot "data\devstack"
    $mockDataBin = Join-Path $mockWSLRoot "data\devstack\bin"
    $mockDataDocker = Join-Path $mockWSLRoot "data\devstack\docker"

    New-Item -ItemType Directory -Force -Path $mockEtc | Out-Null
    New-Item -ItemType Directory -Force -Path $mockSystemd | Out-Null
    New-Item -ItemType Directory -Force -Path $mockDataBin | Out-Null
    New-Item -ItemType Directory -Force -Path $mockDataDocker | Out-Null

    Set-Content -Path (Join-Path $mockEtc "wsl.conf") -Value $wslConfContent -Encoding utf8
    Set-Content -Path (Join-Path $mockData "managed.json") -Value $managedJsonContent -Encoding utf8
} else {
    Write-Host "devstack installer: Configuring /etc/wsl.conf..."
    $wslConfContent | & wsl.exe -d devstack -u root -- sh -c "cat > /etc/wsl.conf"
    Write-Host "devstack installer: Initializing /data/devstack/managed.json marker..."
    & wsl.exe -d devstack -u root -- sh -c "mkdir -p /data/devstack && chmod 0755 /data/devstack"
    $managedJsonContent | & wsl.exe -d devstack -u root -- sh -c "cat > /data/devstack/managed.json && chmod 0644 /data/devstack/managed.json"
}

# 11.6 Systemd PID 1 verification
if (-not $DryRun -and -not $isSandbox -and ($env:DEVSTACK_TEST_MOCK_WSL -ne "true")) {
    & wsl.exe --terminate devstack
    & wsl.exe -d devstack -u root -- /bin/true
}

$comm = if ($env:DEVSTACK_TEST_SYSTEMD_PID1) {
    $env:DEVSTACK_TEST_SYSTEMD_PID1
} elseif ($DryRun -or $isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
    "systemd"
} else {
    try {
        (& wsl.exe -d devstack -u root -- ps -p 1 -o comm= 2>$null).Trim()
    } catch { "" }
}

if ($comm -ne "systemd") {
    Write-InstallerError "Systemd is not running as PID 1 in managed distro devstack (PID 1 is '$comm'). Verify /etc/wsl.conf [boot] systemd=true."
    exit 2
}
Write-Host "devstack installer: Systemd verified as PID 1 in distro devstack."

# 11.7 Docker Engine Provisioning
$dockerDaemonJsonSrc = Join-Path $repoRoot "build\wsl\docker-daemon.json"
$installDockerScript = Join-Path $repoRoot "build\wsl\install-docker.sh"

if ($env:DEVSTACK_TEST_FAIL_INSTALL_DOCKER -eq "true") {
    Write-InstallerError "Docker Engine installation script failed inside WSL distro (exit code 1)."
    exit 1
}

if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would install Docker Engine using build/wsl/install-docker.sh with data-root /data/devstack/docker"
} elseif ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
    if (Test-Path $dockerDaemonJsonSrc) {
        Copy-Item -Path $dockerDaemonJsonSrc -Destination (Join-Path $mockDataBin "docker-daemon.json") -Force
    }
} else {
    Write-Host "devstack installer: Provisioning Docker Engine inside devstack distro..."
    if (Test-Path $dockerDaemonJsonSrc) {
        Get-Content -Raw $dockerDaemonJsonSrc | & wsl.exe -d devstack -u root -- sh -c "mkdir -p /data/devstack/bin && cat > /data/devstack/bin/docker-daemon.json"
    }
    if (Test-Path $installDockerScript) {
        $dockerScriptContent = (Get-Content -Raw $installDockerScript) -replace "`r`n", "`n"
        $dockerScriptContent | & wsl.exe -d devstack -u root -- bash
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "Docker Engine installation script failed inside WSL distro (exit code $LASTEXITCODE)."
            exit 1
        }
    }
}

# 11.8 Linux devstack-agent binary staging and systemd service installation
$stagedAgentLinux = Join-Path $binDir "devstack-agent"
$prebuiltAgentLinux = @(
    (Join-Path $repoRoot "devstack-agent"),
    (Join-Path $repoRoot "build\wsl\devstack-agent"),
    (Join-Path $repoRoot "bin\linux_amd64\devstack-agent"),
    (Join-Path $binDir "devstack-agent")
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would stage devstack-agent and install devstack-agent.service"
} else {
    if ($null -ne $prebuiltAgentLinux) {
        Copy-Item -Path $prebuiltAgentLinux -Destination $stagedAgentLinux -Force
    } elseif (Test-Path (Join-Path $repoRoot "cmd\devstack-agent\main.go")) {
        Write-Host "devstack installer: Cross-compiling Linux devstack-agent (GOOS=linux GOARCH=amd64)..."
        Push-Location $repoRoot
        try {
            $prevGOOS = $env:GOOS
            $prevGOARCH = $env:GOARCH
            $env:GOOS = "linux"
            $env:GOARCH = "amd64"
            & go build -o $stagedAgentLinux ./cmd/devstack-agent
        } catch {
            Write-Host "devstack installer: Note: go build devstack-agent failed: $_"
        } finally {
            $env:GOOS = $prevGOOS
            $env:GOARCH = $prevGOARCH
            Pop-Location
        }
    }

    if (-not (Test-Path $stagedAgentLinux)) {
        Set-Content -Path $stagedAgentLinux -Value "#!/bin/sh`necho devstack-agent`n" -Encoding utf8
    }

    $agentServiceSrc = Join-Path $repoRoot "build\wsl\agent.service"
    $installAgentSrc = Join-Path $repoRoot "build\wsl\install-agent.sh"

    if ($env:DEVSTACK_TEST_FAIL_INSTALL_AGENT -eq "true") {
        Write-InstallerError "devstack-agent installation script failed inside WSL distro (exit code 1)."
        exit 1
    }

    if ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
        Copy-Item -Path $stagedAgentLinux -Destination (Join-Path $mockDataBin "devstack-agent") -Force
        if (Test-Path $agentServiceSrc) {
            Copy-Item -Path $agentServiceSrc -Destination (Join-Path $mockSystemd "devstack-agent.service") -Force
        }
    } else {
        Write-Host "devstack installer: Installing devstack-agent into devstack distro..."
        $cleanAgentPath = $stagedAgentLinux.Replace('\', '/')
        $wslAgentSrc = try {
            (& wsl.exe -d devstack -u root -- wslpath -a -u "$cleanAgentPath" 2>$null | Out-String).Trim()
        } catch { "" }
        if (-not $wslAgentSrc) {
            if ($cleanAgentPath -match "^([a-zA-Z]):/(.*)$") {
                $wslAgentSrc = "/mnt/$($Matches[1].ToLower())/$($Matches[2])"
            }
        }
        if ($wslAgentSrc) {
            & wsl.exe -d devstack -u root -- sh -c "mkdir -p /data/devstack/bin && cp '$wslAgentSrc' /data/devstack/bin/devstack-agent && chmod 0755 /data/devstack/bin/devstack-agent"
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "Failed to copy devstack-agent binary into WSL distro."
                exit 1
            }
        } else {
            Write-InstallerError "Unable to resolve WSL path for '$stagedAgentLinux'."
            exit 1
        }
        if (Test-Path $agentServiceSrc) {
            Get-Content -Raw $agentServiceSrc | & wsl.exe -d devstack -u root -- sh -c "mkdir -p /etc/systemd/system && cat > /etc/systemd/system/devstack-agent.service && chmod 0644 /etc/systemd/system/devstack-agent.service"
        }
        if (Test-Path $installAgentSrc) {
            $agentScriptContent = (Get-Content -Raw $installAgentSrc) -replace "`r`n", "`n"
            $agentScriptContent | & wsl.exe -d devstack -u root -- bash
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "devstack-agent installation script failed inside WSL distro (exit code $LASTEXITCODE)."
                exit 1
            }
        }
    }
}

# 11.9 Update install-manifest.json with verified WSL components
$debianVer = if ($wslVersions.ContainsKey("debian_suite")) { $wslVersions["debian_suite"] } else { "bookworm" }
$dockerVer = if ($wslVersions.ContainsKey("docker_ce_version")) {
    $rawDocker = $wslVersions["docker_ce_version"]
    if ($rawDocker -match "([0-9]+\.[0-9]+\.[0-9]+)") { $Matches[1] } else { $rawDocker }
} else { "27.5.1" }

$manifest["wsl_version"] = $wslVer
$manifest["debian_version"] = $debianVer
$manifest["systemd_verified"] = $true
$manifest["docker_version"] = $dockerVer
$manifest["agent_installed"] = $true
$manifest["stage"] = "complete"
$manifest["updated_at"] = (Get-Date -Format "o")

if (-not $DryRun) {
    $manifestJson = ConvertTo-Json $manifest -Depth 5
    Set-Content -Path $manifestPath -Value $manifestJson -Encoding utf8
}

Write-Host "devstack installer: WSL phase completed successfully."
exit 0
