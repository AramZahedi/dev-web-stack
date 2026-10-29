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
    [string]$InstallDir = "",
    [string]$RootFSPath = "",
    [switch]$AllowReboot,
    [switch]$SkipWSL,
    [switch]$DryRun,
    [string]$TestSandbox = "",
    [switch]$Upgrade,
    [string]$ResumeUserSID = "",
    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Remove-ResumeTask {
    if (-not $DryRun -and -not $isSandbox) {
        try {
            Unregister-ScheduledTask -TaskName "devstack-installer-resume" -Confirm:$false -ErrorAction SilentlyContinue
        } catch {}
        try {
            & schtasks.exe /Delete /TN "devstack-installer-resume" /F 2>$null | Out-Null
        } catch {}
    }
}

function Write-InstallerError([string]$Message) {
    [Console]::Error.WriteLine("devstack installer: $Message")
    Remove-ResumeTask
}

function Show-Usage {
    Write-Host @"
Usage: install.ps1 [-InstallDir <path>] [-RootFSPath <path>] [-AllowReboot] [-SkipWSL] [-DryRun] [-TestSandbox <path>] [-Upgrade] [-Help]

Parameters:
    -InstallDir <path>     Installation target directory (default: %USERPROFILE%\.devstack)
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
$isSandbox = -not [string]::IsNullOrEmpty($TestSandbox)
$script:InstallDirExplicit = $PSBoundParameters.ContainsKey("InstallDir")
$userProf = if ($env:USERPROFILE) { $env:USERPROFILE } else { "C:\Users\$env:USERNAME" }
$defaultInstallDir = Join-Path $userProf ".devstack"

function Test-CompleteInstallManifest([string]$ManifestPath) {
    if (-not (Test-Path -LiteralPath $ManifestPath)) { return $false }
    try {
        $data = Get-Content -Raw -LiteralPath $ManifestPath | ConvertFrom-Json
        return ($data.stage -eq "complete" -or $data.stage -eq "complete-no-wsl")
    } catch {
        return $false
    }
}

if (-not $script:InstallDirExplicit -and [string]::IsNullOrEmpty($InstallDir) -and -not $isSandbox) {
    # Preserve a legacy C:\devstack installation when no target was supplied.
    # The standalone installer performs the same detection for upgrade and
    # uninstall; doing it here keeps direct install.ps1 invocations consistent.
    $legacyRoot = "C:\devstack"
    $legacyManifest = Join-Path $legacyRoot "system\install-manifest.json"
    $profileManifest = Join-Path $defaultInstallDir "system\install-manifest.json"
    if ((Test-CompleteInstallManifest $legacyManifest) -and -not (Test-CompleteInstallManifest $profileManifest)) {
        $InstallDir = $legacyRoot
        Write-Host "devstack installer: Detected legacy installation at $legacyRoot; using it. Pass -InstallDir to choose another location."
    }
}
if ([string]::IsNullOrEmpty($InstallDir)) { $InstallDir = $defaultInstallDir }
$targetDir = if (-not [string]::IsNullOrEmpty($TestSandbox)) { $TestSandbox } else { $InstallDir }

# Reject protected/relative targets before creating anything. The uninstaller
# has the equivalent guard for its recursive delete path.
try {
    $fullTarget = [System.IO.Path]::GetFullPath($targetDir).TrimEnd('\', '/')
} catch {
    Write-InstallerError "Invalid installation directory '$targetDir': $($_.Exception.Message)"
    exit 1
}
$protectedTargets = @(
    ([System.IO.Path]::GetPathRoot($fullTarget)).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:SystemRoot).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:ProgramFiles).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:ProgramData).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:USERPROFILE).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath((Join-Path $env:USERPROFILE "Downloads")).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath("C:\Users").TrimEnd('\', '/')
)
if ([string]::IsNullOrWhiteSpace($fullTarget) -or $fullTarget.StartsWith('\\') -or $protectedTargets -contains $fullTarget) {
    Write-InstallerError "Refusing to install into protected path '$targetDir'. Choose a dedicated directory such as '$defaultInstallDir'."
    exit 1
}
$targetDir = $fullTarget
$InstallDir = $fullTarget
$targetItem = Get-Item -LiteralPath $targetDir -Force -ErrorAction SilentlyContinue
if ($null -ne $targetItem -and (($targetItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0)) {
    Write-InstallerError "Refusing to install through a reparse point/junction at '$targetDir'."
    exit 1
}

$script:LoggedTestOverrides = @{}
function Get-TestOverride([string]$Name) {
    if (-not $isSandbox) {
        return $null
    }
    $val = [Environment]::GetEnvironmentVariable($Name)
    if ($null -ne $val -and $val -ne "") {
        if (-not $script:LoggedTestOverrides.ContainsKey($Name)) {
            $script:LoggedTestOverrides[$Name] = $true
            Write-Host "devstack installer: [TestSandbox] Using test override: $Name=$val"
        }
        return $val
    }
    return $null
}

if ($isSandbox) {
    Get-ChildItem env:DEVSTACK_TEST_* -ErrorAction SilentlyContinue | ForEach-Object {
        if ($_.Value) {
            if (-not $script:LoggedTestOverrides.ContainsKey($_.Name)) {
                $script:LoggedTestOverrides[$_.Name] = $true
                Write-Host "devstack installer: [TestSandbox] Using test override: $($_.Name)=$($_.Value)"
            }
        }
    }
}

# Select a prebuilt Linux agent while excluding the destination itself. A
# reinstall/upgrade in a source checkout can otherwise try to copy
# bin\devstack-agent onto itself and abort.
function Select-PrebuiltAgent([string]$Destination, [string]$RepoRoot, [string]$BinDir) {
    $destFull = [System.IO.Path]::GetFullPath($Destination)
    return @(
        (Join-Path $RepoRoot "devstack-agent"),
        (Join-Path $RepoRoot "build\wsl\devstack-agent"),
        (Join-Path $RepoRoot "bin\linux_amd64\devstack-agent"),
        (Join-Path $BinDir "devstack-agent")
    ) | Where-Object {
        if (-not (Test-Path -LiteralPath $_)) { return $false }
        try {
            return [System.IO.Path]::GetFullPath($_) -ne $destFull
        } catch {
            return $false
        }
    } | Select-Object -First 1
}

# The helper's `service install` persists the configured file but (as of the
# audited helper build) registers its compiled-in default config path. Verify
# the registered command and repair it so custom -InstallDir and legacy
# C:\devstack installs start the helper against the config the installer wrote.
function Repair-HelperServiceConfiguration([string]$HelperPath, [string]$ConfigPath, [string]$AllowSid) {
    if ([string]::IsNullOrEmpty($HelperPath) -or [string]::IsNullOrEmpty($ConfigPath) -or [string]::IsNullOrEmpty($AllowSid)) {
        return $false
    }
    $service = Get-CimInstance -ClassName Win32_Service -Filter "Name='DevstackHelper'" -ErrorAction SilentlyContinue
    if ($null -eq $service) {
        return $false
    }
    $expectedCommand = '"' + $HelperPath + '" service run --config "' + $ConfigPath + '" --allow-sid ' + $AllowSid
    $registered = [string]$service.PathName
    if ($registered.IndexOf($HelperPath, [System.StringComparison]::OrdinalIgnoreCase) -ge 0 -and
        $registered.IndexOf($ConfigPath, [System.StringComparison]::OrdinalIgnoreCase) -ge 0) {
        return $true
    }

    Write-Host "devstack installer: Repairing DevstackHelper service configuration (registered: $registered)..."
    try { Stop-Service -Name "DevstackHelper" -Force -ErrorAction SilentlyContinue } catch {}
    & sc.exe config DevstackHelper "binPath= $expectedCommand" | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Host "devstack installer: sc.exe config DevstackHelper failed with exit code $LASTEXITCODE."
        return $false
    }
    $service = Get-CimInstance -ClassName Win32_Service -Filter "Name='DevstackHelper'" -ErrorAction SilentlyContinue
    if ($null -eq $service) { return $false }
    $registered = [string]$service.PathName
    if ($registered.IndexOf($HelperPath, [System.StringComparison]::OrdinalIgnoreCase) -lt 0 -or
        $registered.IndexOf($ConfigPath, [System.StringComparison]::OrdinalIgnoreCase) -lt 0) {
        Write-Host "devstack installer: DevstackHelper service still points at '$registered'."
        return $false
    }
    Write-Host "devstack installer: DevstackHelper service now points at $ConfigPath."
    return $true
}


# -----------------------------------------------------------------------------
# 1. Elevation check
# -----------------------------------------------------------------------------
function Test-IsElevated {
    if ((Get-TestOverride "DEVSTACK_TEST_ELEVATED") -eq "false") {
        return $false
    }
    if ((Get-TestOverride "DEVSTACK_TEST_ELEVATED") -eq "true") {
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
$detectedArch = if ((Get-TestOverride "DEVSTACK_TEST_OS_ARCH")) { (Get-TestOverride "DEVSTACK_TEST_OS_ARCH") } else { $env:PROCESSOR_ARCHITECTURE }
$is64Bit = if ((Get-TestOverride "DEVSTACK_TEST_OS_ARCH")) {
    (Get-TestOverride "DEVSTACK_TEST_OS_ARCH") -match "64|AMD64"
} else {
    [Environment]::Is64BitOperatingSystem
}

if (-not $is64Bit) {
    Write-InstallerError "Devstack requires a 64-bit (x64) Windows operating system. Detected architecture: $detectedArch."
    exit 2
}

$detectedBuild = if ((Get-TestOverride "DEVSTACK_TEST_OS_BUILD")) {
    [int](Get-TestOverride "DEVSTACK_TEST_OS_BUILD")
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
if ((Get-TestOverride "DEVSTACK_TEST_VIRTUALIZATION") -eq "false") {
    $virtEnabled = $false
} elseif ((Get-TestOverride "DEVSTACK_TEST_VIRTUALIZATION") -eq "true") {
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
# 3.1 Target validation (fail closed before any file is created)
# -----------------------------------------------------------------------------
$targetManifest = Join-Path $targetDir "system\install-manifest.json"
$targetBinary = Join-Path $targetDir "bin\devstack.exe"
$targetHelper = Join-Path $targetDir "bin\devstack-helper.exe"
$targetLooksInstalled = Test-CompleteInstallManifest $targetManifest
$targetLooksPartial = (-not $targetLooksInstalled) -and ((Test-Path -LiteralPath $targetBinary) -or (Test-Path -LiteralPath $targetHelper))
if (-not $DryRun) {
    if ($Upgrade -and -not $targetLooksInstalled) {
        Write-InstallerError "Cannot upgrade: no completed devstack installation was found at '$targetDir'. Pass the correct -InstallDir or run a fresh install."
        exit 1
    }
    if (-not $Upgrade -and $targetLooksInstalled) {
        Write-InstallerError "An existing devstack installation was detected at '$targetDir'. Re-run with -Upgrade to upgrade in place, or pass a different -InstallDir."
        exit 1
    }
}
if (-not $DryRun -and -not $isSandbox) {
    $existingHelper = Get-CimInstance -ClassName Win32_Service -Filter "Name='DevstackHelper'" -ErrorAction SilentlyContinue
    if ($null -ne $existingHelper) {
        $registeredHelper = [string]$existingHelper.PathName
        $targetHelperPath = Join-Path $targetDir "bin\devstack-helper.exe"
        if ($registeredHelper.IndexOf($targetHelperPath, [System.StringComparison]::OrdinalIgnoreCase) -lt 0 -and -not $Upgrade) {
            Write-InstallerError "DevstackHelper is registered for a different installation ($registeredHelper). Use -Upgrade for that installation or remove the service first."
            exit 1
        }
    }
}

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

    # Apply protected DACL to bin directory to prevent unprivileged binary replacement or DLL planting
    $binAcl = Get-Acl -Path $binDir
    $binAcl.SetAccessRuleProtection($true, $false)
    foreach ($rule in @($binAcl.Access)) {
        $binAcl.RemoveAccessRule($rule) | Out-Null
    }
    $binAcl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sidSystem, $rightsFull, $inheritFlags, $propFlags, $allow)))
    $binAcl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sidAdmins, $rightsFull, $inheritFlags, $propFlags, $allow)))
    $binAcl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sidUsers, $rightsRead, $inheritFlags, $propFlags, $allow)))
    Set-Acl -Path $binDir -AclObject $binAcl

    # Stage devstack-daemon-task.ps1 to system directory if present
    $srcDaemonTask = @(
        (Join-Path $repoRoot "build\windows\devstack-daemon-task.ps1"),
        (Join-Path $repoRoot "devstack-daemon-task.ps1")
    ) | Where-Object { Test-Path $_ } | Select-Object -First 1
    if ($null -ne $srcDaemonTask) {
        Copy-Item -Path $srcDaemonTask -Destination (Join-Path $systemDir "devstack-daemon-task.ps1") -Force
    }
    $srcDaemonVbs = @(
        (Join-Path $repoRoot "build\windows\devstack-daemon.vbs"),
        (Join-Path $repoRoot "devstack-daemon.vbs")
    ) | Where-Object { Test-Path $_ } | Select-Object -First 1
    if ($null -ne $srcDaemonVbs) {
        Copy-Item -Path $srcDaemonVbs -Destination (Join-Path $systemDir "devstack-daemon.vbs") -Force
    }
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
} elseif ((Get-TestOverride "DEVSTACK_TEST_REBOOT_REQUIRED") -eq "true") {
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

if (-not $DryRun -and -not $Upgrade) {
    $manifestJson = ConvertTo-Json $manifest -Depth 5
    Set-Content -Path $manifestPath -Value $manifestJson -Encoding utf8
}

if ($rebootNeeded) {
    Write-Host "devstack installer: System restart is required to complete Windows feature enablement."
    $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $currentUserName = "$env:USERDOMAIN\$env:USERNAME"
    $argList = @()
    foreach ($key in $PSBoundParameters.Keys) {
        if ($key -in @("AllowReboot", "Help")) { continue }
        $val = $PSBoundParameters[$key]
        if ($key -eq "InstallDir") { $val = $InstallDir }
        if ($key -eq "TestSandbox") { $val = $targetDir }
        if ($key -eq "RootFSPath" -and -not [string]::IsNullOrEmpty($val)) {
            try { $val = [System.IO.Path]::GetFullPath([string]$val) } catch {}
        }
        if ($val -is [switch]) {
            if ($val.IsPresent) { $argList += "-$key" }
        } else {
            $argList += "-$key `"$val`""
        }
    }
    if (-not $PSBoundParameters.ContainsKey("ResumeUserSID")) {
        $argList += "-ResumeUserSID `"$currentSid`""
    }
    $argString = $argList -join " "

    $resumeStatePath = Join-Path $systemDir "resume-state.json"
    if (-not $DryRun) {
        $resumeState = [ordered]@{
            user_sid = $currentSid
            user_name = $currentUserName
            arguments = $argString
            resumed = $false
        }
        $resumeState | ConvertTo-Json -Depth 3 | Set-Content -Path $resumeStatePath -Encoding utf8
    }

    if ($AllowReboot -and -not $isSandbox) {
        Write-Host "devstack installer: Scheduling resume task and initiating restart..."
        $resumeTaskName = "devstack-installer-resume"
        $actionArgs = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
        if ($argString) {
            $actionArgs += " $argString"
        }
        $action = New-ScheduledTaskAction -Execute $psExe -Argument $actionArgs
        $trigger = New-ScheduledTaskTrigger -AtLogOn
        $principal = try {
            New-ScheduledTaskPrincipal -UserId $currentUserName -LogonType Interactive -RunLevel Highest
        } catch {
            Write-InstallerError "Unable to create a per-user resume task for '$currentUserName': $($_.Exception.Message). Re-run install.ps1 manually after rebooting."
            exit 3010
        }
        Register-ScheduledTask -TaskName $resumeTaskName -Action $action -Trigger $trigger -Principal $principal -Force | Out-Null
        Restart-Computer -Force
    } else {
        Write-Host "devstack installer: Please reboot your computer and run install.ps1 again to resume."
    }
    exit 3010
}

# Clean up resume task on resume or when no reboot needed
Remove-ResumeTask

# -----------------------------------------------------------------------------
# 7. Helper Configuration
# -----------------------------------------------------------------------------
$resumeStatePath = Join-Path $systemDir "resume-state.json"
$persistedSid = $null
if (Test-Path $resumeStatePath) {
    try {
        $resStateObj = Get-Content -Raw $resumeStatePath | ConvertFrom-Json
        if ($resStateObj.user_sid) {
            $persistedSid = $resStateObj.user_sid
        }
    } catch {}
}
$effectiveUserSid = if ($ResumeUserSID) {
    $ResumeUserSID
} elseif ($persistedSid) {
    $persistedSid
} else {
    [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
}
if ($effectiveUserSid -eq "S-1-5-18" -and $persistedSid) {
    $effectiveUserSid = $persistedSid
}

$helperYamlPath = Join-Path $systemDir "helper.yaml"
if (-not $DryRun) {
    $writeHelper = $false
    if (-not (Test-Path $helperYamlPath)) {
        $writeHelper = $true
    } else {
        $existingYaml = Get-Content -Raw $helperYamlPath -ErrorAction SilentlyContinue
        if ($existingYaml -match 'allow_sid:\s*"S-1-5-18"' -and $effectiveUserSid -ne "S-1-5-18") {
            $writeHelper = $true
        }
    }
    if ($writeHelper) {
        $systemDirYaml = $systemDir.Replace('\', '/')
        $caDirYaml = (Join-Path $systemDir "pki").Replace('\', '/')
        $rootCertYaml = (Join-Path $systemDir "pki\root.crt").Replace('\', '/')
        $helperYaml = @"
allow_sid: "$effectiveUserSid"
pipe: "\\\\.\\pipe\\devstack-helper"
system_dir: "$systemDirYaml"
ca_dir: "$caDirYaml"
root_cert: "$rootCertYaml"
proxy_http_port: 8080
proxy_https_port: 8443
"@
        Set-Content -Path $helperYamlPath -Value $helperYaml -Encoding utf8
    }
}

# -----------------------------------------------------------------------------
# 8. Upgrade Flow & Transactional Rollback
# -----------------------------------------------------------------------------
$repoRoot = $PSScriptRoot
$dstDevstack = Join-Path $binDir "devstack.exe"
$dstHelper = Join-Path $binDir "devstack-helper.exe"
$mockWSLRoot = Join-Path $targetDir "mock-wsl"
$mockData = Join-Path $mockWSLRoot "data\devstack"
$mockDataBin = Join-Path $mockData "bin"
$mockBackupTar = Join-Path $mockWSLRoot "data\devstack-backup.tar.gz"
$mockBackupDir = Join-Path $mockWSLRoot "data\devstack-backup"
$mockRollbackTmp = Join-Path $mockWSLRoot "data\devstack.rollback-tmp"
$mockFailedTmp = Join-Path $mockWSLRoot "data\devstack.failed-tmp"

if ($Upgrade) {
    Write-Host "devstack installer: Starting upgrade flow..."
    # Track every staging/provisioning failure in this flag. It must never be
    # reset after a failure: an upgrade that cannot stage all binaries or
    # verify the agent must roll back.
    $upgradeFailed = $false

    # 1. Stop daemon
    if (-not $isSandbox) {
        Write-Host "devstack installer: Stopping daemon for upgrade..."
        if (Test-Path $dstDevstack) {
            try { & "$dstDevstack" --home "$targetDir" daemon stop 2>$null | Out-Null } catch {}
        }
        try {
            Get-Process -Name "devstack" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
        } catch {}
    } else {
        Write-Host "devstack installer: [TestSandbox] Skipping live daemon shutdown."
    }

    # 2. Stop DevstackHelper service before staging/replacing binaries to avoid file sharing violations (C3)
    if (-not $isSandbox) {
        Write-Host "devstack installer: Stopping DevstackHelper service for binary upgrade..."
        try { Stop-Service -Name "DevstackHelper" -Force -ErrorAction SilentlyContinue } catch {}
        try { & sc.exe stop DevstackHelper 2>$null | Out-Null } catch {}
        try {
            Get-Process -Name "devstack-helper" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
        } catch {}
        # Wait for file handles to be released
        $stopWaitTimeout = [DateTime]::UtcNow.AddSeconds(5)
        while ([DateTime]::UtcNow -lt $stopWaitTimeout) {
            $lingering = Get-Process -Name "devstack", "devstack-helper" -ErrorAction SilentlyContinue
            if (-not $lingering) { break }
            Start-Sleep -Milliseconds 200
        }
        $lingering = Get-Process -Name "devstack", "devstack-helper" -ErrorAction SilentlyContinue
        if ($lingering) {
            $names = ($lingering | ForEach-Object { $_.ProcessName }) -join ", "
            Write-InstallerError "Could not stop running devstack processes ($names); refusing to replace binaries while they are locked."
            exit 1
        }
    }

    # 3. Back up Windows binaries
    if (Test-Path $dstDevstack) {
        Copy-Item -Path $dstDevstack -Destination "$dstDevstack.bak" -Force
    }
    if (Test-Path $dstHelper) {
        Copy-Item -Path $dstHelper -Destination "$dstHelper.bak" -Force
    }

    # 4. WSL Backup BEFORE Windows binary staging (C3)
    if ($DryRun) {
        Write-Host "devstack installer: [DryRun] Would backup /data/devstack to /data/devstack-backup.tar.gz"
        Write-Host "devstack installer: [DryRun] Would stage updated binaries and restart devstack-agent.service"
        Write-Host "devstack installer: [DryRun] Would verify agent health and rollback on error"
        exit 0
    }

    if (-not $SkipWSL) {
        Write-Host "devstack installer: Backing up /data/devstack to /data/devstack-backup.tar.gz..."
        if ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
            if ((Get-TestOverride "DEVSTACK_TEST_BACKUP_FAIL") -eq "true") {
                Write-InstallerError "Failed to backup /data/devstack inside WSL distro."
                exit 1
            }
            if (Test-Path $mockData) {
                if (Test-Path $mockBackupDir) { Remove-Item -Path $mockBackupDir -Recurse -Force }
                Copy-Item -Path $mockData -Destination $mockBackupDir -Recurse -Force
                Set-Content -Path $mockBackupTar -Value "mock-backup-archive" -Encoding utf8
            }
            if (Test-Path (Join-Path $mockDataBin "devstack-agent")) {
                Copy-Item -Path (Join-Path $mockDataBin "devstack-agent") -Destination (Join-Path $mockDataBin "devstack-agent.bak") -Force
            }
            if ((Get-TestOverride "DEVSTACK_TEST_BACKUP_VERIFY_FAIL") -eq "true") {
                Write-InstallerError "Backup archive verification failed for /data/devstack-backup.tar.gz."
                exit 1
            }
        } else {
            & wsl.exe -d devstack -u root -- tar -czf /data/devstack-backup.tar.gz -C /data devstack
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "Failed to backup /data/devstack inside WSL distro."
                exit 1
            }
            & wsl.exe -d devstack -u root -- tar -tzf /data/devstack-backup.tar.gz 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "Backup archive verification failed for /data/devstack-backup.tar.gz."
                exit 1
            }
            & wsl.exe -d devstack -u root -- sh -c "cp /data/devstack/bin/devstack-agent /data/devstack/bin/devstack-agent.bak 2>/dev/null || true"
        }
    }

    # 5. Stage updated Windows binaries
    if (-not $isSandbox) {
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
                if ($LASTEXITCODE -ne 0) {
                    Write-InstallerError "go build for devstack.exe failed with exit code $LASTEXITCODE."
                    $upgradeFailed = $true
                }
            }

            if (-not $upgradeFailed) {
                if ($null -ne $prebuiltHelper) {
                    Copy-Item -Path $prebuiltHelper -Destination $dstHelper -Force
                } elseif (Test-Path (Join-Path $repoRoot "cmd\devstack-helper\main.go")) {
                    Write-Host "devstack installer: Building devstack-helper.exe..."
                    & go build -o $dstHelper ./cmd/devstack-helper
                    if ($LASTEXITCODE -ne 0) {
                        Write-InstallerError "go build for devstack-helper.exe failed with exit code $LASTEXITCODE."
                        $upgradeFailed = $true
                    }
                }
            }

            if (-not (Test-Path $dstDevstack) -or -not (Test-Path $dstHelper)) {
                Write-InstallerError "Required Windows binaries not found after build/stage step."
                $upgradeFailed = $true
            }
        } finally {
            Pop-Location
        }

        # Restart DevstackHelper only after every Windows binary was staged.
        # The helper's service registration may still point at its old/default
        # config path; repair it before starting so custom install homes work.
        if (-not $upgradeFailed -and (Test-Path $dstHelper)) {
            if (-not (Repair-HelperServiceConfiguration -HelperPath $dstHelper -ConfigPath $helperYamlPath -AllowSid $effectiveUserSid)) {
                Write-InstallerError "Failed to configure DevstackHelper service to use '$helperYamlPath'."
                $upgradeFailed = $true
            } else {
                Write-Host "devstack installer: Restarting DevstackHelper service..."
                try {
                    Start-Service -Name "DevstackHelper" -ErrorAction Stop
                    Write-Host "devstack installer: DevstackHelper service started."
                } catch {
                    & sc.exe start DevstackHelper | Out-Null
                    $svcCheck = Get-Service -Name "DevstackHelper" -ErrorAction SilentlyContinue
                    if ($null -eq $svcCheck -or $svcCheck.Status -ne "Running") {
                        Write-InstallerError "Failed to start DevstackHelper service after upgrade."
                        $upgradeFailed = $true
                    }
                }
            }
        }
    }

    # 6. Stage updated Linux agent binary (skipped for -SkipWSL installs)
    if (-not $SkipWSL) {
    $stagedAgentLinux = Join-Path $binDir "devstack-agent"
    $prebuiltAgentLinux = Select-PrebuiltAgent -Destination $stagedAgentLinux -RepoRoot $repoRoot -BinDir $binDir

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
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "go build for devstack-agent failed with exit code $LASTEXITCODE."
                $upgradeFailed = $true
            }
        } finally {
            $env:GOOS = $prevGOOS
            $env:GOARCH = $prevGOARCH
            Pop-Location
        }
    }

    if (-not (Test-Path $stagedAgentLinux)) {
        Write-InstallerError "devstack-agent binary not found at '$stagedAgentLinux' and cross-compilation failed. Refusing to install placeholder stub."
        $upgradeFailed = $true
    }

    if (-not $upgradeFailed) {
        if ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
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
            } else {
                Write-InstallerError "Unable to resolve WSL path for '$stagedAgentLinux'."
                $upgradeFailed = $true
            }
        }
    }

    # 7. Restart devstack-agent.service and verify health (C3, H2)
    if (-not $upgradeFailed) {
        Write-Host "devstack installer: Restarting devstack-agent.service and verifying health..."
        if ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
            if (((Get-TestOverride "DEVSTACK_TEST_UPGRADE_FAIL") -eq "true") -or ((Get-TestOverride "DEVSTACK_TEST_AGENT_HEALTH") -eq "failing")) {
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
            } else {
                # Probe HTTPS health endpoint
                $probeCmd = "if [ -r /data/devstack/agent.yaml ] && [ -r /data/devstack/agent.token ]; then port=`$(awk '/port:/ {print `$2}' /data/devstack/agent.yaml 2>/dev/null || echo 7443); token=`$(cat /data/devstack/agent.token 2>/dev/null | tr -d '\r\n'); code=`$(curl -k -s -o /dev/null -w '%{http_code}' -H `"Authorization: Bearer `$token`" -H `"X-Devstack-Agent-Protocol: v1`" https://127.0.0.1:`$port/v1/health 2>/dev/null || echo 000); if [ `"`$code`" != `"200`" ]; then exit 1; fi; fi"
                [System.Text.Encoding]::UTF8.GetBytes("$probeCmd`n") | & wsl.exe -d devstack -u root -- sh
                if ($LASTEXITCODE -ne 0) {
                    Write-InstallerError "devstack-agent HTTPS health probe failed."
                    $upgradeFailed = $true
                }
            }
        }
    }

    } # end -not $SkipWSL agent provisioning

    # 8. Handle rollback on error (C3: Never rm -rf before verified extraction)
    if ($upgradeFailed) {
        Write-InstallerError "Upgrade failed. Initiating rollback..."
        $rollbackOK = $true
        $rollbackFailure = ""
        if ($SkipWSL) {
            Write-Host "devstack installer: -SkipWSL was specified; skipping WSL data rollback."
        } elseif ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
            if ((Get-TestOverride "DEVSTACK_TEST_BACKUP_MISSING") -eq "true") {
                Remove-Item -Path $mockBackupTar -Force -ErrorAction SilentlyContinue
            }
            if (-not (Test-Path $mockBackupTar)) {
                $rollbackOK = $false
                $rollbackFailure = "backup archive is missing"
            } elseif (-not (Test-Path $mockBackupDir)) {
                $rollbackOK = $false
                $rollbackFailure = "backup directory is missing"
            } else {
                try {
                    Remove-Item -Path $mockRollbackTmp -Recurse -Force -ErrorAction SilentlyContinue
                    New-Item -ItemType Directory -Force -Path $mockRollbackTmp | Out-Null
                    Copy-Item -Path $mockBackupDir -Destination (Join-Path $mockRollbackTmp "devstack") -Recurse -Force
                    if (-not (Test-Path (Join-Path $mockRollbackTmp "devstack"))) {
                        throw "staged restore is missing"
                    }
                    Remove-Item -Path $mockFailedTmp -Recurse -Force -ErrorAction SilentlyContinue
                    if (Test-Path $mockData) {
                        Move-Item -Path $mockData -Destination $mockFailedTmp -Force
                    }
                    Move-Item -Path (Join-Path $mockRollbackTmp "devstack") -Destination $mockData -Force
                    Remove-Item -Path $mockFailedTmp -Recurse -Force -ErrorAction SilentlyContinue
                    Remove-Item -Path $mockRollbackTmp -Recurse -Force -ErrorAction SilentlyContinue
                    Remove-Item -Path $mockBackupTar -Force -ErrorAction SilentlyContinue
                    Remove-Item -Path $mockBackupDir -Recurse -Force -ErrorAction SilentlyContinue
                } catch {
                    $rollbackOK = $false
                    $rollbackFailure = $_.Exception.Message
                }
            }
            if ($rollbackOK -and (Test-Path (Join-Path $mockDataBin "devstack-agent.bak"))) {
                try {
                    Move-Item -Path (Join-Path $mockDataBin "devstack-agent.bak") -Destination (Join-Path $mockDataBin "devstack-agent") -Force
                } catch {
                    $rollbackOK = $false
                    $rollbackFailure = "restore devstack-agent: $($_.Exception.Message)"
                }
            }
        } else {
            & wsl.exe -d devstack -u root -- tar -tzf /data/devstack-backup.tar.gz 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0) {
                $rollbackOK = $false
                $rollbackFailure = "backup archive is missing or corrupt"
            } else {
                $rollbackScript = @"
set -e
tar -tzf /data/devstack-backup.tar.gz >/dev/null
rm -rf /data/devstack.rollback-tmp
mkdir -p /data/devstack.rollback-tmp
tar -xzf /data/devstack-backup.tar.gz -C /data/devstack.rollback-tmp
test -d /data/devstack.rollback-tmp/devstack
rm -rf /data/devstack.failed-tmp
if [ -d /data/devstack ]; then mv /data/devstack /data/devstack.failed-tmp; fi
mv /data/devstack.rollback-tmp/devstack /data/devstack
rm -rf /data/devstack.failed-tmp /data/devstack.rollback-tmp /data/devstack-backup.tar.gz
"@
                $rollbackScript -replace "`r`n", "`n" | & wsl.exe -d devstack -u root -- sh
                if ($LASTEXITCODE -ne 0) {
                    $rollbackOK = $false
                    $rollbackFailure = "restore script exited with code $LASTEXITCODE"
                } else {
                    & wsl.exe -d devstack -u root -- systemctl restart devstack-agent.service 2>$null
                    if ($LASTEXITCODE -ne 0) {
                        $rollbackOK = $false
                        $rollbackFailure = "agent restart failed after restore"
                    }
                }
            }
        }
        if (Test-Path "$dstDevstack.bak") {
            try {
                Move-Item -Path "$dstDevstack.bak" -Destination $dstDevstack -Force
            } catch {
                $rollbackOK = $false
                $rollbackFailure = "restore devstack.exe: $($_.Exception.Message)"
            }
        }
        if (Test-Path "$dstHelper.bak") {
            try {
                Move-Item -Path "$dstHelper.bak" -Destination $dstHelper -Force
            } catch {
                $rollbackOK = $false
                $rollbackFailure = "restore devstack-helper.exe: $($_.Exception.Message)"
            }
        }
        if (-not $isSandbox -and (Test-Path $dstHelper)) {
            try {
                Stop-Service -Name "DevstackHelper" -Force -ErrorAction SilentlyContinue
                $helperService = Get-Service -Name "DevstackHelper" -ErrorAction SilentlyContinue
                if ($null -ne $helperService) {
                    Start-Service -Name "DevstackHelper" -ErrorAction Stop
                }
            } catch {
                $rollbackOK = $false
                $rollbackFailure = "restart DevstackHelper: $($_.Exception.Message)"
            }
        }
        if ($rollbackOK) {
            Write-Host "devstack installer: Upgrade rolled back successfully to previous version."
        } else {
            Write-InstallerError "Upgrade rollback FAILED: $rollbackFailure. The previous WSL data may be preserved at /data/devstack.failed-tmp; do not start new work until it is recovered."
        }
        exit 1
    }

    # 9. Success cleanup and manifest update
    if (-not $SkipWSL) {
        Write-Host "devstack installer: Agent health verified successfully."
    } else {
        Write-Host "devstack installer: Windows binaries updated (-SkipWSL; WSL untouched)."
    }
    if (-not $SkipWSL) {
        if ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
            Remove-Item -Path $mockBackupTar -Force -ErrorAction SilentlyContinue
            Remove-Item -Path $mockBackupDir -Recurse -Force -ErrorAction SilentlyContinue
            Remove-Item -Path (Join-Path $mockDataBin "devstack-agent.bak") -Force -ErrorAction SilentlyContinue
        } else {
            & wsl.exe -d devstack -u root -- rm -f /data/devstack-backup.tar.gz /data/devstack/bin/devstack-agent.bak
        }
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
    $previousStage = [string]$manifestDict["stage"]
    $manifestDict["upgraded_at"] = (Get-Date -Format "o")
    $manifestDict["updated_at"] = (Get-Date -Format "o")
    if ($SkipWSL -and $previousStage -eq "complete-no-wsl") {
        $manifestDict["stage"] = "complete-no-wsl"
    } else {
        $manifestDict["stage"] = "complete"
    }
    if (-not $SkipWSL) {
        $manifestDict["agent_installed"] = $true
    }

    $manifestJson = ConvertTo-Json $manifestDict -Depth 5
    Set-Content -Path $manifestPath -Value $manifestJson -Encoding utf8

    # Restart user daemon so dashboard is not left stopped (M8)
    Write-Host "devstack installer: Restarting user daemon..."
    if (Test-Path $dstDevstack) {
        try {
            & "$dstDevstack" --home "$targetDir" daemon restart 2>$null | Out-Null
        } catch {
            Write-Host "devstack installer: Note: Run 'devstack daemon restart' to restart the dashboard daemon."
        }
    }

    Write-Host "devstack installer: Upgrade completed successfully."
    exit 0
}

# -----------------------------------------------------------------------------
# 9. Binary Staging & System PATH (Fresh Install)
# -----------------------------------------------------------------------------
if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would stage binaries to $binDir"
} elseif (-not $isSandbox) {
    if ($targetLooksPartial) {
        # A previous install failed after staging binaries. Stop any daemon or
        # helper still holding those files so a retry can replace them.
        Write-Host "devstack installer: Partial installation detected; stopping devstack processes before staging..."
        if (Test-Path $dstDevstack) {
            try { & "$dstDevstack" --home "$targetDir" daemon stop 2>$null | Out-Null } catch {}
        }
        try { Get-Process -Name "devstack" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue } catch {}
        try { Stop-Service -Name "DevstackHelper" -Force -ErrorAction SilentlyContinue } catch {}
        try { & sc.exe stop DevstackHelper 2>$null | Out-Null } catch {}
        try { Get-Process -Name "devstack-helper" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue } catch {}
        $partialStopTimeout = [DateTime]::UtcNow.AddSeconds(5)
        while ([DateTime]::UtcNow -lt $partialStopTimeout) {
            $partialLingering = Get-Process -Name "devstack", "devstack-helper" -ErrorAction SilentlyContinue
            if (-not $partialLingering) { break }
            Start-Sleep -Milliseconds 200
        }
        $partialLingering = Get-Process -Name "devstack", "devstack-helper" -ErrorAction SilentlyContinue
        if ($partialLingering) {
            Write-InstallerError "Could not stop running devstack processes; refusing to replace partial-install binaries."
            exit 1
        }
    }

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
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "go build for devstack.exe failed with exit code $LASTEXITCODE."
                exit 1
            }
        }

        if ($null -ne $prebuiltHelper) {
            Copy-Item -Path $prebuiltHelper -Destination $dstHelper -Force
        } elseif (Test-Path (Join-Path $repoRoot "cmd\devstack-helper\main.go")) {
            Write-Host "devstack installer: Building devstack-helper.exe..."
            & go build -o $dstHelper ./cmd/devstack-helper
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "go build for devstack-helper.exe failed with exit code $LASTEXITCODE."
                exit 1
            }
        }
    } finally {
        Pop-Location
    }

    if (-not (Test-Path $dstDevstack)) {
        Write-InstallerError "devstack.exe binary not found at '$dstDevstack' and compilation failed."
        exit 1
    }
    if (-not (Test-Path $dstHelper)) {
        Write-InstallerError "devstack-helper.exe binary not found at '$dstHelper' and compilation failed."
        exit 1
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
# 10. Helper Service Registration & Startup (Fresh Install)
# -----------------------------------------------------------------------------
if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would register and start DevstackHelper service"
} elseif (-not $isSandbox -and (Test-Path $dstHelper)) {
    $svc = Get-Service -Name "DevstackHelper" -ErrorAction SilentlyContinue
    if ($null -eq $svc) {
        Write-Host "devstack installer: Installing helper service..."
        & "$dstHelper" service install --config "$helperYamlPath"
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "Failed to install DevstackHelper service (exit code $LASTEXITCODE)."
            exit 1
        }
    } else {
        $existingSvc = Get-CimInstance -ClassName Win32_Service -Filter "Name='DevstackHelper'" -ErrorAction SilentlyContinue
        if ($null -ne $existingSvc) {
            $registered = [string]$existingSvc.PathName
            if ($registered.IndexOf($dstHelper, [System.StringComparison]::OrdinalIgnoreCase) -lt 0 -or
                $registered.IndexOf($helperYamlPath, [System.StringComparison]::OrdinalIgnoreCase) -lt 0) {
                Write-InstallerError "DevstackHelper is already registered for a different installation ($registered). Use -Upgrade for that installation or remove the service first."
                exit 1
            }
        }
    }
    if (-not (Repair-HelperServiceConfiguration -HelperPath $dstHelper -ConfigPath $helperYamlPath -AllowSid $effectiveUserSid)) {
        Write-InstallerError "Failed to configure DevstackHelper service to use '$helperYamlPath'."
        exit 1
    }
    try {
        Start-Service -Name "DevstackHelper" -ErrorAction Stop
        Write-Host "devstack installer: DevstackHelper service started."
    } catch {
        & sc.exe start DevstackHelper | Out-Null
        $svcCheck = Get-Service -Name "DevstackHelper" -ErrorAction SilentlyContinue
        if ($null -eq $svcCheck -or $svcCheck.Status -ne "Running") {
            Write-InstallerError "Failed to start DevstackHelper service."
            exit 1
        }
    }
}

# -----------------------------------------------------------------------------
# 10.1 Daemon Logon Scheduled Task (H6)
# -----------------------------------------------------------------------------
$daemonTaskScript = @(
    (Join-Path $repoRoot "build\windows\devstack-daemon-task.ps1"),
    (Join-Path $repoRoot "devstack-daemon-task.ps1"),
    (Join-Path $systemDir "devstack-daemon-task.ps1")
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if (-not $DryRun -and -not $isSandbox) {
    Write-Host "devstack installer: Registering devstack-daemon logon task..."
    try {
        if ($null -ne $daemonTaskScript -and (Test-Path $daemonTaskScript)) {
            & $daemonTaskScript -Binary "$dstDevstack" -HomeDir "$targetDir" -UserId "$env:USERDOMAIN\$env:USERNAME"
        } else {
            # Direct registration fallback if helper script is absent in standalone package
            $taskName = "devstack-daemon"
            $homeDir = $targetDir
            if (-not (Test-Path $homeDir)) { New-Item -ItemType Directory -Force -Path $homeDir | Out-Null }
            $vbsPath = Join-Path $systemDir "devstack-daemon.vbs"
            $vbsContent = "Set WshShell = CreateObject(""WScript.Shell"")`r`nWshShell.Run """"""$dstDevstack"""" daemon run --home """"$homeDir"""""", 0, False`r`n"
            Set-Content -Path $vbsPath -Value $vbsContent -Encoding Ascii -Force
            $action = New-ScheduledTaskAction -Execute "wscript.exe" -Argument "`"$vbsPath`"" -WorkingDirectory $homeDir
            $trigger = try {
                New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
            } catch {
                New-ScheduledTaskTrigger -AtLogOn
            }
            $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
            $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit (New-TimeSpan -Days 0)
            Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
            Write-Host "devstack installer: Registered $taskName for limited user $env:USERNAME"
        }
    } catch {
        Write-Host "devstack installer: Note: Failed to register devstack-daemon task: $_"
    }
}

if ($SkipWSL) {
    Write-Host "devstack installer: Skipping WSL distro and proxy firewall rule (-SkipWSL specified)."
    if (-not $DryRun) {
        $manifest["agent_installed"] = $false
        $manifest["wsl_installed"] = $false
        $manifest["stage"] = "complete-no-wsl"
        $manifest["updated_at"] = (Get-Date -Format "o")
        ConvertTo-Json $manifest -Depth 5 | Set-Content -Path $manifestPath -Encoding utf8
    }
    if (-not $DryRun -and -not $isSandbox) {
        try { Start-ScheduledTask -TaskName "devstack-daemon" -ErrorAction SilentlyContinue } catch {}
        try { & schtasks.exe /Run /TN "devstack-daemon" 2>$null | Out-Null } catch {}
    }
    Write-Host "devstack installer: System phase completed successfully."
    exit 0
}
# -----------------------------------------------------------------------------
# 11. WSL Managed Distro, RootFS, & Agent Service Provisioning (WSL Phase)
# -----------------------------------------------------------------------------
function Get-WSLVersionString {
    if ((Get-TestOverride "DEVSTACK_TEST_WSL_VERSION")) {
        if ((Get-TestOverride "DEVSTACK_TEST_WSL_VERSION") -eq "missing") {
            return $null
        }
        return (Get-TestOverride "DEVSTACK_TEST_WSL_VERSION")
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
    if (-not $isSandbox -and -not $DryRun -and -not (Get-TestOverride "DEVSTACK_TEST_WSL_VERSION")) {
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
$wslConfigPath = if ((Get-TestOverride "DEVSTACK_TEST_WSLCONFIG")) {
    (Get-TestOverride "DEVSTACK_TEST_WSLCONFIG")
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
    $defaultCandidate = @((Join-Path $repoRoot "debian-12-wsl-rootfs.tar.gz"), (Join-Path $repoRoot "build\wsl\debian-12-wsl-rootfs.tar.gz")) | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    if ($defaultCandidate -and (Test-Path -LiteralPath $defaultCandidate)) {
        $effectiveRootFS = $defaultCandidate
    } elseif ($DryRun) {
        # A dry run must not create files. Point at the default artifact path so
        # the plan remains readable; the checksum/size gates below skip it.
        $effectiveRootFS = Join-Path $repoRoot "debian-12-wsl-rootfs.tar.gz"
    } elseif ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
        $mockDir = Join-Path $targetDir "mock-rootfs"
        New-Item -ItemType Directory -Force -Path $mockDir | Out-Null
        $effectiveRootFS = Join-Path $mockDir "debian-12-wsl-rootfs.tar.gz"
        if (-not (Test-Path -LiteralPath $effectiveRootFS)) {
            Set-Content -Path $effectiveRootFS -Value "mock rootfs archive content" -Encoding utf8
        }
    }
}

if (-not $DryRun -and ([string]::IsNullOrEmpty($effectiveRootFS) -or -not (Test-Path -LiteralPath $effectiveRootFS))) {
    Write-InstallerError "WSL rootfs archive not found. Expected at '$effectiveRootFS'. Please supply -RootFSPath <path>."
    exit 2
}

$rootfsMinBytes = 104857600
if ($wslVersions.ContainsKey("rootfs_minimum_bytes") -and $wslVersions["rootfs_minimum_bytes"] -match '^[0-9]+$') {
    $rootfsMinBytes = [int64]$wslVersions["rootfs_minimum_bytes"]
}
if (-not $DryRun -and -not $isSandbox -and (Test-Path -LiteralPath $effectiveRootFS)) {
    $rootfsSize = (Get-Item -LiteralPath $effectiveRootFS).Length
    if ($rootfsSize -lt $rootfsMinBytes) {
        Write-InstallerError "Rootfs archive '$effectiveRootFS' is only $rootfsSize bytes; the minimum required size is $rootfsMinBytes bytes."
        exit 2
    }
}

if (-not [string]::IsNullOrEmpty($effectiveRootFS) -and (Test-Path -LiteralPath $effectiveRootFS)) {
    $actualHash = (Get-FileHash -Path $effectiveRootFS -Algorithm SHA256).Hash.ToLower()
    $expectedHash = $null
    $rootfsFileName = [System.IO.Path]::GetFileName($effectiveRootFS)
    $expectedArtifact = if ($wslVersions.ContainsKey("rootfs_artifact")) { $wslVersions["rootfs_artifact"] } else { "debian-12-wsl-rootfs.tar.gz" }

    if ((Get-TestOverride "DEVSTACK_TEST_ROOTFS_SHA256")) {
        $expectedHash = (Get-TestOverride "DEVSTACK_TEST_ROOTFS_SHA256").ToLower()
    } elseif ($wslVersions.ContainsKey("rootfs_sha256") -and $wslVersions["rootfs_sha256"] -match "^[0-9a-fA-F]{64}$" -and ($rootfsFileName -eq $expectedArtifact)) {
        if (-not ($effectiveRootFS -like "*mock-rootfs*")) {
            $expectedHash = $wslVersions["rootfs_sha256"].ToLower()
        }
    } else {
        $sidecar = "$effectiveRootFS.sha256"
        if (Test-Path -LiteralPath $sidecar) {
            $expectedHash = ((Get-Content -Path $sidecar -Raw) -split '\s+')[0].Trim().ToLower()
        }
    }

    if ($expectedHash) {
        if ($actualHash -ne $expectedHash) {
            Write-InstallerError "Rootfs SHA256 checksum mismatch for $effectiveRootFS. Expected: $expectedHash, got: $actualHash."
            exit 2
        }
        Write-Host "devstack installer: RootFS archive checksum verified ($actualHash)."
    } else {
        Write-Warning "devstack installer: WARNING: RootFS archive checksum unverified ($actualHash); no expected checksum resolved from versions.lock, environment, or sidecar."
    }
}

# 11.4 Managed distro import & unmanaged distro check
$distroExists = $false
$isDistroManaged = $false

if ((Get-TestOverride "DEVSTACK_TEST_DISTRO_EXISTS") -eq "unmanaged") {
    $distroExists = $true
    $isDistroManaged = $false
} elseif ((Get-TestOverride "DEVSTACK_TEST_DISTRO_EXISTS") -eq "managed") {
    $distroExists = $true
    $isDistroManaged = $true
} elseif ($DryRun -or $isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
    $distroExists = $false
} else {
    try {
        $distroListRaw = & wsl.exe --list --quiet 2>&1 | Out-String
        $distroList = (($distroListRaw -replace "`0", "") -split "\r?\n") | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" }
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
    } elseif (-not $isSandbox -and ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -ne "true")) {
        Write-Host "devstack installer: Importing devstack WSL distribution into $wslDistroDir..."
        & wsl.exe --import devstack "$wslDistroDir" "$effectiveRootFS" --version 2
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "Failed to import WSL distribution 'devstack'."
            exit 1
        }
        # If running as SYSTEM and effective user SID is known, sync the Lxss registration to the user hive!
        if ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -eq "S-1-5-18" -and $effectiveUserSid -and $effectiveUserSid -ne "S-1-5-18") {
            try {
                $userLxssPath = "Registry::HKEY_USERS\$effectiveUserSid\Software\Microsoft\Windows\CurrentVersion\Lxss"
                if (Test-Path "HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss") {
                    if (-not (Test-Path $userLxssPath)) {
                        New-Item -Path $userLxssPath -Force | Out-Null
                    }
                    Get-ChildItem HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss | ForEach-Object {
                        $prop = Get-ItemProperty $_.PSPath
                        if ($prop.DistributionName -eq "devstack") {
                            $targetSubKey = Join-Path $userLxssPath $_.PSChildName
                            if (-not (Test-Path $targetSubKey)) {
                                New-Item -Path $targetSubKey -Force | Out-Null
                            }
                            $srcKey = Get-Item $_.PSPath
                            foreach ($valName in $srcKey.GetValueNames()) {
                                $v = $srcKey.GetValue($valName, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
                                $k = $srcKey.GetValueKind($valName)
                                Set-ItemProperty -Path $targetSubKey -Name $valName -Value $v -Type $k
                            }
                        }
                    }
                }
            } catch {
                Write-Host "devstack installer: Note: Could not sync WSL registration to user hive: $_"
            }
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
} elseif ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
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
    if ($LASTEXITCODE -ne 0) {
        Write-InstallerError "Failed to configure /etc/wsl.conf in managed distro."
        exit 1
    }
    Write-Host "devstack installer: Initializing /data/devstack/managed.json marker..."
    & wsl.exe -d devstack -u root -- sh -c "mkdir -p /data/devstack && chmod 0755 /data/devstack"
    $managedJsonContent | & wsl.exe -d devstack -u root -- sh -c "cat > /data/devstack/managed.json && chmod 0644 /data/devstack/managed.json"
    if ($LASTEXITCODE -ne 0) {
        Write-InstallerError "Failed to initialize /data/devstack/managed.json in managed distro."
        exit 1
    }
}

# 11.6 Systemd PID 1 verification
if (-not $DryRun -and -not $isSandbox -and ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -ne "true")) {
    & wsl.exe --terminate devstack
    & wsl.exe -d devstack -u root -- /bin/true
}

$comm = if ((Get-TestOverride "DEVSTACK_TEST_SYSTEMD_PID1")) {
    (Get-TestOverride "DEVSTACK_TEST_SYSTEMD_PID1")
} elseif ($DryRun -or $isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
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
$agentServiceSrc = Join-Path $repoRoot "build\wsl\agent.service"
$installAgentSrc = Join-Path $repoRoot "build\wsl\install-agent.sh"

if (-not $DryRun) {
    $missingProvisioning = @()
    foreach ($requiredAsset in @($dockerDaemonJsonSrc, $installDockerScript, $agentServiceSrc, $installAgentSrc)) {
        if (-not (Test-Path -LiteralPath $requiredAsset)) {
            $missingProvisioning += $requiredAsset
        }
    }
    if ($missingProvisioning.Count -gt 0) {
        Write-InstallerError "Release payload is incomplete; required WSL provisioning asset(s) are missing: $($missingProvisioning -join ', ')."
        exit 1
    }
}

if ((Get-TestOverride "DEVSTACK_TEST_FAIL_INSTALL_DOCKER") -eq "true") {
    Write-InstallerError "Docker Engine installation script failed inside WSL distro (exit code 1)."
    exit 1
}

if ($DryRun) {
    Write-Host "devstack installer: [DryRun] Would install Docker Engine using build/wsl/install-docker.sh with data-root /data/devstack/docker"
} elseif ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
    Copy-Item -Path $dockerDaemonJsonSrc -Destination (Join-Path $mockDataBin "docker-daemon.json") -Force
} else {
    Write-Host "devstack installer: Provisioning Docker Engine inside devstack distro..."
    Get-Content -Raw $dockerDaemonJsonSrc | & wsl.exe -d devstack -u root -- sh -c "mkdir -p /data/devstack/bin && cat > /data/devstack/bin/docker-daemon.json"
    if ($LASTEXITCODE -ne 0) {
        Write-InstallerError "Failed to install Docker daemon configuration inside WSL distro."
        exit 1
    }
    $dockerScriptContent = (Get-Content -Raw $installDockerScript) -replace "`r`n", "`n"
    $dockerScriptContent | & wsl.exe -d devstack -u root -- bash
    if ($LASTEXITCODE -ne 0) {
        Write-InstallerError "Docker Engine installation script failed inside WSL distro (exit code $LASTEXITCODE)."
        exit 1
    }
    $dockerActive = try { (& wsl.exe -d devstack -u root -- systemctl is-active docker.service 2>$null | Out-String).Trim() } catch { "" }
    if ($dockerActive -ne "active") {
        Write-InstallerError "Docker Engine is not active after provisioning (systemctl is-active returned '$dockerActive')."
        exit 1
    }
}

# 11.8 Linux devstack-agent binary staging and systemd service installation
$stagedAgentLinux = Join-Path $binDir "devstack-agent"
$prebuiltAgentLinux = Select-PrebuiltAgent -Destination $stagedAgentLinux -RepoRoot $repoRoot -BinDir $binDir

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
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "go build for devstack-agent failed with exit code $LASTEXITCODE."
                exit 1
            }
        } finally {
            $env:GOOS = $prevGOOS
            $env:GOARCH = $prevGOARCH
            Pop-Location
        }
    }

    if (-not (Test-Path $stagedAgentLinux)) {
        Write-InstallerError "devstack-agent binary not found at '$stagedAgentLinux' and cross-compilation failed. Refusing to install placeholder stub."
        exit 1
    }

    if ((Get-TestOverride "DEVSTACK_TEST_FAIL_INSTALL_AGENT") -eq "true") {
        Write-InstallerError "devstack-agent installation script failed inside WSL distro (exit code 1)."
        exit 1
    }

    if ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
        Copy-Item -Path $stagedAgentLinux -Destination (Join-Path $mockDataBin "devstack-agent") -Force
        Copy-Item -Path $agentServiceSrc -Destination (Join-Path $mockSystemd "devstack-agent.service") -Force
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
        Get-Content -Raw $agentServiceSrc | & wsl.exe -d devstack -u root -- sh -c "mkdir -p /etc/systemd/system && cat > /etc/systemd/system/devstack-agent.service && chmod 0644 /etc/systemd/system/devstack-agent.service"
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "Failed to install devstack-agent.service unit inside WSL distro."
            exit 1
        }
        $agentScriptContent = (Get-Content -Raw $installAgentSrc) -replace "`r`n", "`n"
        $agentScriptContent | & wsl.exe -d devstack -u root -- bash
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "devstack-agent installation script failed inside WSL distro (exit code $LASTEXITCODE)."
            exit 1
        }
        $agentEnabled = try { (& wsl.exe -d devstack -u root -- systemctl is-enabled devstack-agent.service 2>$null | Out-String).Trim() } catch { "" }
        if ($agentEnabled -ne "enabled") {
            Write-InstallerError "devstack-agent.service is not enabled after installation (systemctl is-enabled returned '$agentEnabled')."
            exit 1
        }
    }

    # Verify agent binary and health probe before writing manifest
    if ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
        if ((Get-TestOverride "DEVSTACK_TEST_AGENT_HEALTH") -eq "failing") {
            Write-InstallerError "devstack-agent health check probe failed."
            exit 1
        }
    } else {
        & wsl.exe -d devstack -u root -- /data/devstack/bin/devstack-agent -h 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "devstack-agent binary verification failed inside WSL distro."
            exit 1
        }
        $probeCmd = "if [ -r /data/devstack/agent.yaml ] && [ -r /data/devstack/agent.token ]; then port=`$(awk '/port:/ {print `$2}' /data/devstack/agent.yaml 2>/dev/null || echo 7443); token=`$(cat /data/devstack/agent.token 2>/dev/null | tr -d '\r\n'); code=`$(curl -k -s -o /dev/null -w '%{http_code}' -H `"Authorization: Bearer `$token`" -H `"X-Devstack-Agent-Protocol: v1`" https://127.0.0.1:`$port/v1/health 2>/dev/null || echo 000); if [ `"`$code`" != `"200`" ]; then exit 1; fi; fi"
        [System.Text.Encoding]::UTF8.GetBytes("$probeCmd`n") | & wsl.exe -d devstack -u root -- sh
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "devstack-agent HTTPS health probe failed."
            exit 1
        }
    }
}

# -----------------------------------------------------------------------------
# 11.10 Inbound Windows Firewall Rule for WSL Proxy (H1: Deferred until WSL adapter/subnet exists)
# -----------------------------------------------------------------------------
$ruleName = "devstack-daemon-proxy"
$displayName = "Devstack Daemon Proxy"
$ports = @("8080", "8443")

if ($DryRun -or $isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_FIREWALL_CHECK") -eq "true")) {
    Write-Host "devstack installer: Firewall rule '$ruleName' (DisplayName: '$displayName') Protocol: TCP Ports: $($ports -join ',') Direction: Inbound Action: Allow Scoped: WSL"
}

if (-not $DryRun -and -not $isSandbox) {
    # Detect WSL adapter or subnet
    $wslSubnet = $null
    if ((Get-TestOverride "DEVSTACK_TEST_WSL_SUBNET")) {
        if ((Get-TestOverride "DEVSTACK_TEST_WSL_SUBNET") -ne "missing") {
            $wslSubnet = (Get-TestOverride "DEVSTACK_TEST_WSL_SUBNET")
        }
    } else {
        $wslAdapter = Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object {
            ($_.InterfaceDescription -match "Hyper-V Virtual Ethernet" -or $_.Name -match "WSL") -and $_.Status -eq "Up"
        }
        if ($null -eq $wslAdapter) {
            $wslAdapter = Get-NetAdapter -ErrorAction SilentlyContinue | Where-Object { $_.Name -match "WSL" }
        }
        if ($null -ne $wslAdapter) {
            $ip = Get-NetIPAddress -InterfaceIndex $wslAdapter.InterfaceIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($null -ne $ip) {
                $wslSubnet = "$($ip.IPAddress)/$($ip.PrefixLength)"
            }
        }
    }

    if ([string]::IsNullOrEmpty($wslSubnet)) {
        Write-InstallerError "Unable to determine WSL subnet for firewall rule scoping. Refusing to create unscoped inbound firewall rule."
        exit 1
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
        RemoteAddress = $wslSubnet
    }
    if ($null -ne $wslAdapter) {
        $fwParams["InterfaceAlias"] = $wslAdapter.Name
    }

    try {
        New-NetFirewallRule @fwParams -ErrorAction Stop | Out-Null
        Write-Host "devstack installer: Created inbound firewall rule '$ruleName' for ports $($ports -join ', ')."
    } catch {
        $portStr = $ports -join ","
        & netsh.exe advfirewall firewall add rule name="$ruleName" dir=in action=allow protocol=TCP localport=$portStr profile=private,domain remoteip=$wslSubnet | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-InstallerError "Failed to create inbound firewall rule using netsh.exe (exit code $LASTEXITCODE)."
            exit 1
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
if (-not $DryRun -and -not $isSandbox) {
    # Start the logon task in this session too; without a running daemon the
    # WSL keepalive does not exist and the distro is eligible for idle shutdown.
    try { Start-ScheduledTask -TaskName "devstack-daemon" -ErrorAction SilentlyContinue } catch {}
    try { & schtasks.exe /Run /TN "devstack-daemon" 2>$null | Out-Null } catch {}
}
exit 0
