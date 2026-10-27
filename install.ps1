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
    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Write-InstallerError([string]$Message) {
    [Console]::Error.WriteLine("devstack installer: $Message")
}

function Show-Usage {
    Write-Host @"
Usage: install.ps1 [-InstallDir <path>] [-RootFSPath <path>] [-AllowReboot] [-SkipWSL] [-DryRun] [-TestSandbox <path>] [-Help]

Parameters:
    -InstallDir <path>     Installation target directory (default: C:\devstack)
    -RootFSPath <path>     Path to WSL rootfs tar.gz archive
    -AllowReboot           Allow automatic reboot and schedule resume if required by Windows features
    -SkipWSL               Skip WSL feature enablement and distro provisioning
    -DryRun                Print planned operations without modifying system
    -TestSandbox <path>    Run layout and configuration inside an isolated test directory
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

# Determine effective target directory
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
        $action = New-ScheduledTaskAction -Execute "pwsh.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
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

Write-Host "devstack installer: System phase completed successfully."
exit 0
