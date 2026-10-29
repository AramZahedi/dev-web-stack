<#
.SYNOPSIS
    Devstack Windows Uninstaller
.DESCRIPTION
    Elevated lifecycle script to uninstall devstack from Windows.
    Reverts CA trust from Windows machine root store, cleans managed hosts entries,
    removes the Windows Firewall proxy rule, stops and unregisters the DevstackHelper
    Windows service, unregisters the devstack-daemon scheduled task, cleans the
    devstack bin directory from Machine PATH, and by default preserves the WSL distro and user data.
    Optional switches allow purging the WSL distro (-PurgeDistro) or purging DPAPI secrets
    and the installation directory (-PurgeData).
.PARAMETER PurgeDistro
    Unregister and delete the managed WSL distro 'devstack'.
.PARAMETER PurgeData
    Delete DPAPI secrets (agent-token, agent-pin) and remove installation directory.
.PARAMETER Force
    Skip confirmation prompts and allow removal of machine-wide registrations when the target has no devstack files or the helper service belongs to a different path.
.PARAMETER InstallDir
    Root installation directory (default: %USERPROFILE%\.devstack, or a detected legacy C:\devstack install).
.PARAMETER TestSandbox
    Run in an isolated directory for testing, skipping machine-wide modifications.
.PARAMETER DryRun
    Simulate uninstallation steps without altering system state.
.PARAMETER Help
    Show usage and help information.
#>

[CmdletBinding()]
param(
    [switch]$PurgeDistro,
    [switch]$PurgeData,
    [switch]$Force,
    [string]$InstallDir = "",
    [string]$TestSandbox = "",
    [switch]$DryRun,
    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Write-InstallerError([string]$Message) {
    [Console]::Error.WriteLine("devstack uninstaller: $Message")
}

function Show-Usage {
    Write-Host @"
Usage: uninstall.ps1 [-PurgeDistro] [-PurgeData] [-Force] [-InstallDir <path>] [-TestSandbox <path>] [-DryRun] [-Help]

Parameters:
    -PurgeDistro           Unregister and remove the managed WSL distro 'devstack'
    -PurgeData             Delete DPAPI secrets (agent-token, agent-pin) and remove installation directory
    -Force                 Skip confirmation prompts; allow removal of machine-wide registrations when the target has no devstack files or the helper service belongs to another path
    -InstallDir <path>     Installation target directory (default: %USERPROFILE%\.devstack)
    -TestSandbox <path>    Run uninstallation inside an isolated test directory
    -DryRun                Print planned operations without modifying system
    -Help                  Display this help message

Exit codes:
    0    Success
    1    General error
    2    Elevation required
"@
}

if ($Help) {
    Show-Usage
    exit 0
}

# Determine effective target directory
$isSandbox = -not [string]::IsNullOrEmpty($TestSandbox)
$script:InstallDirExplicit = $PSBoundParameters.ContainsKey("InstallDir")
$userProf = if ($env:USERPROFILE) { $env:USERPROFILE } else { "C:\Users\$env:USERNAME" }
$defaultInstallDir = Join-Path $userProf ".devstack"
if (-not $script:InstallDirExplicit -and [string]::IsNullOrEmpty($InstallDir) -and -not $isSandbox) {
    $legacyRoot = "C:\devstack"
    $legacyManifest = Join-Path $legacyRoot "system\install-manifest.json"
    $profileManifest = Join-Path $defaultInstallDir "system\install-manifest.json"
    if ((Test-Path -LiteralPath $legacyManifest) -and -not (Test-Path -LiteralPath $profileManifest)) {
        $InstallDir = $legacyRoot
        Write-Host "devstack uninstaller: Detected legacy installation at $legacyRoot; using it. Pass -InstallDir to choose another location."
    }
}
if ([string]::IsNullOrEmpty($InstallDir)) { $InstallDir = $defaultInstallDir }
$targetDir = if (-not [string]::IsNullOrEmpty($TestSandbox)) { $TestSandbox } else { $InstallDir }

$script:LoggedTestOverrides = @{}
function Get-TestOverride([string]$Name) {
    if (-not $isSandbox) {
        return $null
    }
    $val = [Environment]::GetEnvironmentVariable($Name)
    if ($null -ne $val -and $val -ne "") {
        if (-not $script:LoggedTestOverrides.ContainsKey($Name)) {
            $script:LoggedTestOverrides[$Name] = $true
            Write-Host "devstack uninstaller: [TestSandbox] Using test override: $Name=$val"
        }
        return $val
    }
    return $null
}
$binDir = Join-Path $targetDir "bin"
$systemDir = Join-Path $targetDir "system"
$wslDistroDir = Join-Path $targetDir "wsl\devstack"
$dstDevstack = Join-Path $binDir "devstack.exe"
$dstHelper = Join-Path $binDir "devstack-helper.exe"

# -----------------------------------------------------------------------------
# 0. Target validation (never recursively delete an arbitrary directory)
# -----------------------------------------------------------------------------
$targetManifest = Join-Path $systemDir "install-manifest.json"
$targetLooksInstalled = (Test-Path -LiteralPath $targetManifest) -or
    (Test-Path -LiteralPath $dstDevstack) -or
    (Test-Path -LiteralPath $dstHelper)
try {
    $fullTarget = [System.IO.Path]::GetFullPath($targetDir).TrimEnd('\', '/')
} catch {
    Write-InstallerError "Invalid -InstallDir '$targetDir': $($_.Exception.Message)"
    exit 1
}
$protectedTargets = @(
    ([System.IO.Path]::GetPathRoot($fullTarget)).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:SystemRoot).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:ProgramFiles).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:ProgramData).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath($env:USERPROFILE).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath((Join-Path $env:USERPROFILE "Downloads")).TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath("C:\Users").TrimEnd('\', '/'),
    [System.IO.Path]::GetFullPath("C:\Program Files (x86)").TrimEnd('\', '/')
)
if ([string]::IsNullOrWhiteSpace($fullTarget) -or $fullTarget.StartsWith('\\') -or $protectedTargets -contains $fullTarget) {
    Write-InstallerError "Refusing to uninstall from protected path '$targetDir'. Pass the devstack installation directory explicitly."
    exit 1
}
# Use the canonical absolute path for all subsequent reads, writes, and deletes.
$targetDir = $fullTarget
$targetItem = Get-Item -LiteralPath $targetDir -Force -ErrorAction SilentlyContinue
if ($null -ne $targetItem -and (($targetItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0)) {
    Write-InstallerError "Refusing to uninstall through a reparse point/junction at '$targetDir'."
    exit 1
}
$binDir = Join-Path $targetDir "bin"
$systemDir = Join-Path $targetDir "system"
$wslDistroDir = Join-Path $targetDir "wsl\devstack"
$dstDevstack = Join-Path $binDir "devstack.exe"
$dstHelper = Join-Path $binDir "devstack-helper.exe"
$targetManifest = Join-Path $systemDir "install-manifest.json"
if (-not $isSandbox -and -not $DryRun -and -not $targetLooksInstalled) {
    if (-not $Force) {
        Write-InstallerError "No devstack installation was found at '$targetDir'. Pass the correct -InstallDir, or -Force to remove only machine-wide registrations."
        exit 1
    }
    Write-Host "devstack uninstaller: Warning: no installation files found at '$targetDir'; -Force is removing machine-wide registrations only."
}
if ($PurgeData -and -not $DryRun -and -not (Test-Path -LiteralPath $targetManifest)) {
    Write-InstallerError "Refusing -PurgeData: '$targetDir' has no devstack install-manifest.json. Refusing to recursively delete an unverified directory."
    exit 1
}
if ($PurgeData -and -not $DryRun) {
    try {
        $purgeManifest = Get-Content -Raw -LiteralPath $targetManifest | ConvertFrom-Json
    } catch {
        Write-InstallerError "Refusing -PurgeData: '$targetManifest' is not valid JSON."
        exit 1
    }
    if ($null -eq $purgeManifest -or [string]::IsNullOrEmpty([string]$purgeManifest.stage) -or [string]::IsNullOrEmpty([string]$purgeManifest.version)) {
        Write-InstallerError "Refusing -PurgeData: '$targetManifest' is not a devstack installation manifest."
        exit 1
    }
}
if (-not $DryRun -and -not $isSandbox) {
    $existingHelperService = Get-CimInstance -ClassName Win32_Service -Filter "Name='DevstackHelper'" -ErrorAction SilentlyContinue
    if ($null -ne $existingHelperService) {
        $registeredHelper = [string]$existingHelperService.PathName
        if ($registeredHelper.IndexOf($dstHelper, [System.StringComparison]::OrdinalIgnoreCase) -lt 0 -and -not $Force) {
            Write-InstallerError "DevstackHelper is registered for a different installation ($registeredHelper), not '$dstHelper'. Pass -Force only if you intend to remove that service."
            exit 1
        }
    }
}


# -----------------------------------------------------------------------------
# 1. Elevation check
# -----------------------------------------------------------------------------
function Test-IsElevated {
    if ($isSandbox) {
        if ($env:DEVSTACK_TEST_ELEVATED -eq "false") {
            return $false
        }
        if ($env:DEVSTACK_TEST_ELEVATED -eq "true") {
            return $true
        }
    }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

$elevated = Test-IsElevated
if (-not $elevated) {
    if ($DryRun -or $isSandbox) {
        Write-Host "devstack uninstaller: running non-elevated (DryRun or TestSandbox active)."
    } else {
        Write-InstallerError "Administrative elevation is required. Please run this script in an elevated PowerShell session (Run as Administrator)."
        exit 2
    }
}

Write-Host "devstack uninstaller: Starting uninstallation..."

# -----------------------------------------------------------------------------
# 2. Stop daemon if running
# -----------------------------------------------------------------------------
Write-Host "devstack uninstaller: Stopping devstack daemon if running..."
if ($DryRun) {
    Write-Host "devstack uninstaller: [DryRun] Would stop devstack daemon"
} elseif (-not $isSandbox) {
    if (Test-Path $dstDevstack) {
        try {
            & "$dstDevstack" --home "$targetDir" daemon stop 2>$null | Out-Null
        } catch {}
    }
    try {
        Get-Process -Name "devstack" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    } catch {}
} else {
    Write-Host "devstack uninstaller: [TestSandbox] Skipping live daemon shutdown."
}

# -----------------------------------------------------------------------------
# 3. Remove trust from Windows root store
# -----------------------------------------------------------------------------
Write-Host "devstack uninstaller: Removing devstack root CA trust from Windows store..."
if ($DryRun) {
    Write-Host "devstack uninstaller: [DryRun] Would remove CA certificate from Windows root store"
} elseif (-not $isSandbox) {
    $rootCrtPath = Join-Path $systemDir "pki\root.crt"
    if (Test-Path $rootCrtPath) {
        try {
            $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($rootCrtPath)
            if ($cert.Thumbprint) {
                & certutil.exe -delstore Root ($cert.Thumbprint) 2>$null | Out-Null
            }
        } catch {}
    }
    try {
        Get-ChildItem Cert:\LocalMachine\Root -ErrorAction SilentlyContinue | Where-Object { $_.Subject -match "devstack" } | ForEach-Object {
            try {
                & certutil.exe -delstore Root ($_.Thumbprint) 2>$null | Out-Null
            } catch {}
        }
    } catch {}

    # Remove the Firefox enterprise-root policy only when devstack recorded
    # ownership. The helper runs as LocalSystem, so its marker may be in the
    # system profile rather than the installing user's tree.
    $firefoxMarkers = @(
        (Join-Path $systemDir "firefox-policy-managed"),
        "C:\Windows\system32\config\systemprofile\.devstack\system\firefox-policy-managed"
    )
    $ownsFirefoxPolicy = $false
    foreach ($marker in $firefoxMarkers) {
        if (Test-Path $marker) {
            try {
                if ((Get-Content -Path $marker -Raw -ErrorAction Stop).Trim() -eq "devstack-owned") {
                    $ownsFirefoxPolicy = $true
                }
            } catch {}
        }
    }
    if ($ownsFirefoxPolicy) {
        try {
            & reg.exe delete "HKLM\Software\Policies\Mozilla\Firefox\Certificates" /v ImportEnterpriseRoots /f 2>$null | Out-Null
        } catch {}
    }
    foreach ($marker in $firefoxMarkers) {
        Remove-Item -Path $marker -Force -ErrorAction SilentlyContinue
    }
}

# -----------------------------------------------------------------------------
# 4. Clean managed devstack hosts entries
# -----------------------------------------------------------------------------
Write-Host "devstack uninstaller: Cleaning managed hosts entries..."
$hostsPath = if ($isSandbox -and $env:DEVSTACK_TEST_HOSTS_PATH) {
    $env:DEVSTACK_TEST_HOSTS_PATH
} elseif ($isSandbox) {
    Join-Path $targetDir "hosts"
} else {
    "C:\Windows\System32\drivers\etc\hosts"
}

if ($DryRun) {
    Write-Host "devstack uninstaller: [DryRun] Would remove devstack managed block from $hostsPath"
} elseif (Test-Path $hostsPath) {
    try {
        $lines = Get-Content -Path $hostsPath
        $newLines = @()
        $inBlock = $false
        $blockFound = $false
        $blockComplete = $false
        foreach ($line in $lines) {
            $trimmed = $line.Trim()
            if ($trimmed -eq "# BEGIN devstack") {
                if (-not $inBlock) { $blockFound = $true }
                $inBlock = $true
                continue
            }
            if ($trimmed -eq "# END devstack") {
                if ($inBlock) {
                    $inBlock = $false
                    $blockComplete = $true
                    continue
                }
                # Stray END marker outside a devstack block is user content.
                $newLines += $line
                continue
            }
            if (-not $inBlock) {
                $newLines += $line
            }
        }
        if ($blockFound -and -not $blockComplete) {
            Write-Host "devstack uninstaller: Warning: found '# BEGIN devstack' without a matching '# END devstack'; leaving $hostsPath unchanged."
        } elseif ($blockFound) {
            $cleanedText = ($newLines -join "`r`n").TrimEnd() + "`r`n"
            [System.IO.File]::WriteAllText($hostsPath, $cleanedText, (New-Object System.Text.UTF8Encoding($false)))
        }
    } catch {
        Write-Host "devstack uninstaller: Warning: could not update hosts file: $_"
    }
}

# Also clean WSL distro /etc/hosts if distro exists and not in sandbox
if (-not $DryRun -and -not $isSandbox -and ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -ne "true")) {
    try {
        & wsl.exe -d devstack -u root -- sed -i '/# BEGIN DEVSTACK MANAGED HOSTS/,/# END DEVSTACK MANAGED HOSTS/d' /etc/hosts 2>$null
        & wsl.exe -d devstack -u root -- sed -i '/# BEGIN devstack/,/# END devstack/d' /etc/hosts 2>$null
    } catch {}
}

# -----------------------------------------------------------------------------
# 5. Remove Windows Firewall Rules
# -----------------------------------------------------------------------------
$ruleNames = @("devstack-daemon-proxy", "Devstack WSL Proxy HTTP", "Devstack WSL Proxy HTTPS")
Write-Host "devstack uninstaller: Removing Windows Firewall rules: $($ruleNames -join ', ')..."
if ($DryRun) {
    Write-Host "devstack uninstaller: [DryRun] Would remove firewall rules: $($ruleNames -join ', ')"
} elseif (-not $isSandbox) {
    foreach ($ruleName in $ruleNames) {
        try {
            Get-NetFirewallRule -ErrorAction SilentlyContinue | Where-Object {
                $_.Name -eq $ruleName -or $_.DisplayName -eq $ruleName
            } | Remove-NetFirewallRule -ErrorAction SilentlyContinue
        } catch {}
        try {
            & netsh.exe advfirewall firewall delete rule name="$ruleName" 2>$null | Out-Null
        } catch {}
    }
}

# -----------------------------------------------------------------------------
# 6. Stop and remove DevstackHelper Windows service
# -----------------------------------------------------------------------------
Write-Host "devstack uninstaller: Stopping and removing DevstackHelper Windows service..."
if ($DryRun) {
    Write-Host "devstack uninstaller: [DryRun] Would stop and delete DevstackHelper service"
} elseif (-not $isSandbox) {
    try {
        Stop-Service -Name "DevstackHelper" -Force -ErrorAction SilentlyContinue
    } catch {}
    try {
        & sc.exe stop DevstackHelper 2>$null | Out-Null
    } catch {}
    try {
        Get-Process -Name "devstack-helper" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    } catch {}
    # Remove private CA key material after the helper stopped. This is a
    # security-sensitive credential and is not preserved with user data.
    $caDirs = @(
        (Join-Path $systemDir "pki"),
        "C:\Windows\system32\config\systemprofile\.devstack\system\pki"
    )
    foreach ($caDir in $caDirs) {
        if (Test-Path -LiteralPath $caDir) {
            Remove-Item -Path $caDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
    try {
        & sc.exe delete DevstackHelper 2>$null | Out-Null
        $serviceGone = $false
        $serviceWait = [DateTime]::UtcNow.AddSeconds(5)
        while ([DateTime]::UtcNow -lt $serviceWait) {
            if ($null -eq (Get-Service -Name "DevstackHelper" -ErrorAction SilentlyContinue)) {
                $serviceGone = $true
                break
            }
            Start-Sleep -Milliseconds 200
        }
        if (-not $serviceGone) {
            $remainingService = Get-Service -Name "DevstackHelper" -ErrorAction SilentlyContinue
            Write-InstallerError "Failed to remove DevstackHelper service (still present, status $($remainingService.Status))."
            exit 1
        }
    } catch {
        Write-InstallerError "Failed to remove DevstackHelper service: $($_.Exception.Message)"
        exit 1
    }
}

# -----------------------------------------------------------------------------
# 7. Unregister scheduled task 'devstack-daemon'
# -----------------------------------------------------------------------------
Write-Host "devstack uninstaller: Unregistering scheduled task 'devstack-daemon'..."
if ($DryRun) {
    Write-Host "devstack uninstaller: [DryRun] Would unregister scheduled task 'devstack-daemon'"
} elseif (-not $isSandbox) {
    try {
        Unregister-ScheduledTask -TaskName "devstack-daemon" -Confirm:$false -ErrorAction SilentlyContinue
    } catch {}
    try {
        & schtasks.exe /Delete /TN "devstack-daemon" /F 2>$null | Out-Null
    } catch {}
    try {
        Unregister-ScheduledTask -TaskName "devstack-installer-resume" -Confirm:$false -ErrorAction SilentlyContinue
    } catch {}
    try {
        & schtasks.exe /Delete /TN "devstack-installer-resume" /F 2>$null | Out-Null
    } catch {}
}

# -----------------------------------------------------------------------------
# 8. Distro handling (-PurgeDistro)
# -----------------------------------------------------------------------------
if ($PurgeDistro) {
    Write-Host "devstack uninstaller: Purging WSL distro 'devstack'..."
    if ($DryRun) {
        Write-Host "devstack uninstaller: [DryRun] Would unregister WSL distro 'devstack'"
    } elseif ($isSandbox -or ((Get-TestOverride "DEVSTACK_TEST_MOCK_WSL") -eq "true")) {
        if ((Get-TestOverride "DEVSTACK_TEST_UNREGISTER_FAIL") -eq "true") {
            Write-InstallerError "Failed to unregister WSL distro 'devstack'; refusing to delete the distro directory."
            exit 1
        }
        if (Test-Path $wslDistroDir) {
            Remove-Item -Path $wslDistroDir -Recurse -Force -ErrorAction SilentlyContinue
            if (Test-Path $wslDistroDir) {
                Write-InstallerError "Failed to remove WSL distro directory '$wslDistroDir'."
                exit 1
            }
        }
        $mockWSLRoot = Join-Path $targetDir "mock-wsl"
        if (Test-Path $mockWSLRoot) {
            Remove-Item -Path $mockWSLRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    } else {
        $distroRegistered = $false
        try {
            $distroListRaw = & wsl.exe --list --quiet 2>&1 | Out-String
            $distroList = (($distroListRaw -replace "`0", "") -split "\r?\n") | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" }
            $distroRegistered = $distroList -contains "devstack"
        } catch {
            Write-InstallerError "Unable to enumerate WSL distributions before -PurgeDistro: $($_.Exception.Message)"
            exit 1
        }
        if ($distroRegistered) {
            try { & wsl.exe --terminate devstack 2>$null | Out-Null } catch {}
            & wsl.exe --unregister devstack 2>$null | Out-Null
            if ($LASTEXITCODE -ne 0) {
                Write-InstallerError "Failed to unregister WSL distro 'devstack' (exit code $LASTEXITCODE); refusing to delete the distro directory."
                exit 1
            }
        } else {
            Write-Host "devstack uninstaller: WSL distro 'devstack' is not registered; removing its install directory only."
        }
        if (Test-Path $wslDistroDir) {
            Remove-Item -Path $wslDistroDir -Recurse -Force -ErrorAction SilentlyContinue
            if (Test-Path $wslDistroDir) {
                Write-InstallerError "Failed to remove WSL distro directory '$wslDistroDir'."
                exit 1
            }
        }
        # Clean any orphan devstack Lxss registry entries across HKCU and HKEY_USERS
        try {
            $lxssPaths = @("HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss")
            if (Test-Path "Registry::HKEY_USERS") {
                Get-ChildItem "Registry::HKEY_USERS" -ErrorAction SilentlyContinue | ForEach-Object {
                    $sub = Join-Path $_.PSPath "Software\Microsoft\Windows\CurrentVersion\Lxss"
                    if (Test-Path $sub) { $lxssPaths += $sub }
                }
            }
            foreach ($lxPath in $lxssPaths) {
                Get-ChildItem $lxPath -ErrorAction SilentlyContinue | ForEach-Object {
                    $dp = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
                    $basePath = if ($null -ne $dp) { [string]$dp.BasePath } else { "" }
                    if ($null -ne $dp -and $dp.DistributionName -eq "devstack" -and $basePath) {
                        try { $normalizedBase = [System.IO.Path]::GetFullPath($basePath).TrimEnd('\', '/') } catch { $normalizedBase = $basePath.TrimEnd('\', '/') }
                        if ($normalizedBase -ieq $wslDistroDir.TrimEnd('\', '/')) {
                            Remove-Item $_.PSPath -Recurse -Force -ErrorAction SilentlyContinue
                        }
                    }
                }
            }
        } catch {}
    }
} else {
    Write-Host "devstack uninstaller: Preserving WSL distro 'devstack' and user projects."
}

# -----------------------------------------------------------------------------
# 9. Data handling (-PurgeData)
# -----------------------------------------------------------------------------
if ($PurgeData) {
    Write-Host "devstack uninstaller: Purging devstack data and DPAPI secrets..."
    $purgeDataFailed = $false
    $secretsDirs = @()
    if ($isSandbox -and $env:DEVSTACK_TEST_SECRETS_DIR) {
        $secretsDirs += $env:DEVSTACK_TEST_SECRETS_DIR
    }
    $secretsDirs += (Join-Path $systemDir "secrets")

    foreach ($sDir in $secretsDirs) {
        if (Test-Path $sDir) {
            Remove-Item -Path (Join-Path $sDir "agent-token") -Force -ErrorAction SilentlyContinue
            Remove-Item -Path (Join-Path $sDir "agent-pin") -Force -ErrorAction SilentlyContinue
            Remove-Item -Path (Join-Path $sDir "api-token") -Force -ErrorAction SilentlyContinue
            foreach ($secretName in @("agent-token", "agent-pin", "api-token")) {
                if (Test-Path -LiteralPath (Join-Path $sDir $secretName)) {
                    $purgeDataFailed = $true
                    Write-InstallerError "Failed to remove secret '$secretName' from '$sDir'."
                }
            }
        }
    }

    # Remove CA material wherever the helper may have created it, including
    # legacy LocalSystem-profile installs.
    $caDirs = @(
        (Join-Path $systemDir "pki"),
        "C:\Windows\system32\config\systemprofile\.devstack\system\pki"
    )
    foreach ($caDir in $caDirs) {
        if (Test-Path $caDir) {
            Remove-Item -Path $caDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }

    if (-not $DryRun) {
        # Avoid file locking if current working directory is inside targetDir
        try {
            $currPath = (Get-Location).Path
            if ($currPath.StartsWith($targetDir, [System.StringComparison]::OrdinalIgnoreCase)) {
                Set-Location $env:SystemRoot
            }
        } catch {}

        if ($isSandbox) {
            Get-ChildItem -Path $targetDir -Exclude "hosts" -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
        } else {
            Remove-Item -Path $targetDir -Recurse -Force -ErrorAction SilentlyContinue
            if (Test-Path -LiteralPath $targetDir) {
                $purgeDataFailed = $true
                Write-InstallerError "Failed to remove installation directory '$targetDir'."
            }
        }
    }
} else {
    Write-Host "devstack uninstaller: Preserving user data and configuration."
}

# -----------------------------------------------------------------------------
# 10. Machine PATH cleanup
# -----------------------------------------------------------------------------
Write-Host "devstack uninstaller: Cleaning $binDir from Machine PATH..."
if (-not $DryRun -and -not $isSandbox) {
    try {
        $cleanBinDir = $binDir.TrimEnd('\/').Replace('/', '\')
        $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
        if ($machinePath) {
            $paths = ($machinePath -split ";") | Where-Object {
                $p = $_.Trim().Trim('"').TrimEnd('\/').Replace('/', '\')
                $p -ne "" -and $p -ine $cleanBinDir
            }
            $newPath = $paths -join ";"
            [Environment]::SetEnvironmentVariable("Path", $newPath, "Machine")
        }
        if ($env:Path) {
            $currPaths = ($env:Path -split ";") | Where-Object {
                $p = $_.Trim().Trim('"').TrimEnd('\/').Replace('/', '\')
                $p -ne "" -and $p -ine $cleanBinDir
            }
            $env:Path = $currPaths -join ";"
        }
    } catch {}
}

if ($PurgeData -and $purgeDataFailed) {
    Write-InstallerError "Uninstall completed with errors; some devstack data could not be removed."
    exit 1
}

Write-Host "devstack uninstaller: Devstack uninstalled successfully."
exit 0
