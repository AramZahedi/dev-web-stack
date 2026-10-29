<#
.SYNOPSIS
    Devstack Windows Uninstaller
.DESCRIPTION
    Elevated lifecycle script to uninstall devstack from Windows.
    Reverts CA trust from Windows machine root store, cleans managed hosts entries,
    removes the Windows Firewall proxy rule, stops and unregisters the DevstackHelper
    Windows service, unregisters the devstack-daemon scheduled task, cleans C:\devstack\bin
    from Machine PATH, and by default preserves the WSL distro and user data.
    Optional switches allow purging the WSL distro (-PurgeDistro) or purging DPAPI secrets
    and the installation directory (-PurgeData).
.PARAMETER PurgeDistro
    Unregister and delete the managed WSL distro 'devstack'.
.PARAMETER PurgeData
    Delete DPAPI secrets (agent-token, agent-pin) and remove installation directory.
.PARAMETER Force
    Skip confirmation prompts.
.PARAMETER InstallDir
    Root installation directory (default: C:\devstack).
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
    -Force                 Skip confirmation prompts
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
if ([string]::IsNullOrEmpty($InstallDir)) { $userProf = if ($env:USERPROFILE) { $env:USERPROFILE } else { "C:\Users\$env:USERNAME" }; $InstallDir = Join-Path $userProf ".devstack" }; $targetDir = if (-not [string]::IsNullOrEmpty($TestSandbox)) { $TestSandbox } else { $InstallDir }
$isSandbox = -not [string]::IsNullOrEmpty($TestSandbox)
$binDir = Join-Path $targetDir "bin"
$systemDir = Join-Path $targetDir "system"
$wslDistroDir = Join-Path $targetDir "wsl\devstack"
$dstDevstack = Join-Path $binDir "devstack.exe"
$dstHelper = Join-Path $binDir "devstack-helper.exe"

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
} else {
    if (Test-Path $dstDevstack) {
        try {
            & "$dstDevstack" daemon stop 2>$null | Out-Null
        } catch {}
    }
    try {
        Get-Process -Name "devstack" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    } catch {}
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

    # Clean Firefox policy ownership marker if present
    $firefoxMarker = Join-Path $systemDir "firefox-policy-managed"
    if (Test-Path $firefoxMarker) {
        Remove-Item -Path $firefoxMarker -Force -ErrorAction SilentlyContinue
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
        foreach ($line in $lines) {
            $trimmed = $line.Trim()
            if ($trimmed -eq "# BEGIN devstack") {
                $inBlock = $true
                continue
            }
            if ($trimmed -eq "# END devstack") {
                $inBlock = $false
                continue
            }
            if (-not $inBlock) {
                $newLines += $line
            }
        }
        $cleanedText = ($newLines -join "`r`n").TrimEnd() + "`r`n"
        [System.IO.File]::WriteAllText($hostsPath, $cleanedText, (New-Object System.Text.UTF8Encoding($false)))
    } catch {
        Write-Host "devstack uninstaller: Warning: could not update hosts file: $_"
    }
}

# Also clean WSL distro /etc/hosts if distro exists and not in sandbox
if (-not $DryRun -and -not $isSandbox -and ($env:DEVSTACK_TEST_MOCK_WSL -ne "true")) {
    try {
        & wsl.exe -d devstack -u root -- sed -i '/# BEGIN DEVSTACK MANAGED HOSTS/,/# END DEVSTACK MANAGED HOSTS/d' /etc/hosts 2>$null
        & wsl.exe -d devstack -u root -- sed -i '/# BEGIN devstack/,/# END devstack/d' /etc/hosts 2>$null
    } catch {}
}

# -----------------------------------------------------------------------------
# 5. Remove Windows Firewall Rule
# -----------------------------------------------------------------------------
$ruleName = "devstack-daemon-proxy"
Write-Host "devstack uninstaller: Removing Windows Firewall rule '$ruleName'..."
if ($DryRun) {
    Write-Host "devstack uninstaller: [DryRun] Would remove firewall rule '$ruleName'"
} elseif (-not $isSandbox) {
    try {
        Remove-NetFirewallRule -Name $ruleName -ErrorAction SilentlyContinue
    } catch {}
    try {
        & netsh.exe advfirewall firewall delete rule name="$ruleName" 2>$null | Out-Null
    } catch {}
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
    try {
        & sc.exe delete DevstackHelper 2>$null | Out-Null
    } catch {}
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
    } elseif ($isSandbox -or ($env:DEVSTACK_TEST_MOCK_WSL -eq "true")) {
        if (Test-Path $wslDistroDir) {
            Remove-Item -Path $wslDistroDir -Recurse -Force -ErrorAction SilentlyContinue
        }
        $mockWSLRoot = Join-Path $targetDir "mock-wsl"
        if (Test-Path $mockWSLRoot) {
            Remove-Item -Path $mockWSLRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    } else {
        try {
            & wsl.exe --terminate devstack 2>$null
        } catch {}
        try {
            & wsl.exe --unregister devstack 2>$null
        } catch {}
        if (Test-Path $wslDistroDir) {
            Remove-Item -Path $wslDistroDir -Recurse -Force -ErrorAction SilentlyContinue
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
                    if ($null -ne $dp -and $dp.DistributionName -eq "devstack") {
                        Remove-Item $_.PSPath -Recurse -Force -ErrorAction SilentlyContinue
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
    $secretsDirs = @()
    if ($isSandbox -and $env:DEVSTACK_TEST_SECRETS_DIR) {
        $secretsDirs += $env:DEVSTACK_TEST_SECRETS_DIR
    }
    if ($env:DEVSTACK_HOME) {
        $secretsDirs += (Join-Path $env:DEVSTACK_HOME "system\secrets")
    }
    $secretsDirs += (Join-Path $env:USERPROFILE ".devstack\system\secrets")
    $secretsDirs += (Join-Path $systemDir "secrets")

    foreach ($sDir in $secretsDirs) {
        if (Test-Path $sDir) {
            Remove-Item -Path (Join-Path $sDir "agent-token") -Force -ErrorAction SilentlyContinue
            Remove-Item -Path (Join-Path $sDir "agent-pin") -Force -ErrorAction SilentlyContinue
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

Write-Host "devstack uninstaller: Devstack uninstalled successfully."
exit 0
