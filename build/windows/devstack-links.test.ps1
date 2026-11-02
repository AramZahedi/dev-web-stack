<#
.SYNOPSIS
    Plain PowerShell harness for build/windows/devstack-links.ps1.

.DESCRIPTION
    No Pester dependency. Runs in Windows PowerShell 5.1 and PowerShell 7.
    When invoked without -RunTests it re-runs itself under every available
    Windows PowerShell host, so both powershell.exe and pwsh.exe are covered.

    The harness uses temporary links and target roots only. It never touches
    C:\devstack.
#>
[CmdletBinding()]
param(
    [switch]$RunTests
)

$ErrorActionPreference = "Stop"

if (-not $RunTests) {
    $hosts = New-Object System.Collections.Generic.List[string]
    foreach ($name in @("powershell.exe", "pwsh.exe")) {
        $command = Get-Command $name -ErrorAction SilentlyContinue
        if ($null -ne $command -and -not $hosts.Contains([string]$command.Source)) {
            $hosts.Add([string]$command.Source)
        }
    }
    if ($hosts.Count -eq 0) {
        $hosts.Add((Get-Process -Id $PID).Path)
    }

    $anyFailed = $false
    foreach ($exe in $hosts) {
        Write-Host ""
        Write-Host "### Running devstack link harness with $exe"
        & $exe -NoProfile -ExecutionPolicy Bypass -File $PSCommandPath -RunTests
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) {
            $anyFailed = $true
            Write-Host "### $exe FAILED (exit $exitCode)" -ForegroundColor Red
        } else {
            Write-Host "### $exe PASSED" -ForegroundColor Green
        }
    }

    if ($anyFailed) { exit 1 }
    exit 0
}

$script:Passed = 0
$script:Failed = 0
$script:Failures = New-Object System.Collections.Generic.List[string]
$script:JunctionTag = [uint32]2684354563
$script:SymlinkTag = [uint32]2684354572

function Write-Step {
    param([string]$Message)
    Write-Host ""
    Write-Host "== $Message =="
}

function Assert-True {
    param(
        [bool]$Condition,
        [string]$Message
    )
    if ($Condition) {
        $script:Passed++
        Write-Host "  PASS $Message"
    } else {
        $script:Failed++
        $script:Failures.Add($Message)
        Write-Host "  FAIL $Message" -ForegroundColor Red
    }
}

function Assert-Equal {
    param(
        $Expected,
        $Actual,
        [string]$Message
    )
    $equal = $false
    if ($null -eq $Expected -and $null -eq $Actual) {
        $equal = $true
    } elseif ($null -ne $Expected -and $null -ne $Actual) {
        $equal = [string]::Equals([string]$Expected, [string]$Actual, [System.StringComparison]::OrdinalIgnoreCase)
    }
    Assert-True $equal "$Message (expected '$Expected', got '$Actual')"
}

function Normalize-TestPath {
    param([AllowNull()][string]$Path)
    if ([string]::IsNullOrWhiteSpace($Path)) { return "" }
    $value = $Path.Trim()
    if ($value.StartsWith('\??\UNC\', [System.StringComparison]::OrdinalIgnoreCase)) {
        $value = '\\' + $value.Substring(8)
    } elseif ($value.StartsWith('\\?\UNC\', [System.StringComparison]::OrdinalIgnoreCase)) {
        $value = '\\' + $value.Substring(8)
    } elseif ($value.StartsWith('\??\', [System.StringComparison]::OrdinalIgnoreCase)) {
        $value = $value.Substring(4)
    } elseif ($value.StartsWith('\\?\', [System.StringComparison]::OrdinalIgnoreCase)) {
        $value = $value.Substring(4)
    }
    return $value.TrimEnd('\', '/')
}

function Get-TestReparseInfo {
    param([string]$Path)

    $info = [pscustomobject]@{
        Exists = $false
        IsReparsePoint = $false
        Tag = $null
        Target = $null
    }

    $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if ($null -eq $item) {
        return $info
    }

    $info.Exists = $true
    $info.IsReparsePoint = (([System.IO.FileAttributes]$item.Attributes) -band [System.IO.FileAttributes]::ReparsePoint) -ne 0

    try {
        $raw = & fsutil.exe reparsepoint query $Path 2>$null | Out-String
        if ($raw -match '(?im)^\s*Reparse Tag Value\s*:\s*(0x[0-9a-fA-F]+)\s*$') {
            $info.Tag = [Convert]::ToUInt32($Matches[1].Substring(2), 16)
        }
    } catch {
    }

    if ($null -ne $item.PSObject.Properties['Target']) {
        $target = @($item.Target)[0]
        if (-not [string]::IsNullOrWhiteSpace([string]$target)) {
            $info.Target = [string]$target
        }
    }

    return $info
}

function Assert-Link {
    param(
        [string]$Path,
        [string]$ExpectedTarget,
        [uint32]$ExpectedTag,
        [string]$Label
    )

    $info = Get-TestReparseInfo $Path
    Assert-True $info.Exists "$Label exists at $Path"
    Assert-True $info.IsReparsePoint "$Label is a reparse point"
    Assert-Equal $ExpectedTag $info.Tag "$Label has the expected reparse tag"
    Assert-Equal (Normalize-TestPath $ExpectedTarget) (Normalize-TestPath $info.Target) "$Label resolves to its target"
}

function Remove-TestLink {
    param([string]$Path)
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if ($null -eq $item) { return }
    if ((([System.IO.FileAttributes]$item.Attributes) -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        try {
            [System.IO.Directory]::Delete($Path, $false)
        } catch {
            try { [System.IO.File]::Delete($Path) } catch {}
        }
    }
}

$helperPath = Join-Path $PSScriptRoot "devstack-links.ps1"
if (-not (Test-Path -LiteralPath $helperPath)) {
    Write-Host "FAIL: helper not found at $helperPath" -ForegroundColor Red
    exit 1
}
. $helperPath

$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-links-test-" + [guid]::NewGuid().ToString("N"))
$linksRoot = Join-Path $tempRoot "links"
$windowsRoot = Join-Path $tempRoot "windows-install"
$wslInstallTarget = Join-Path $tempRoot "wsl-install"
$wslProjectsTarget = Join-Path $tempRoot "wsl-projects"
$dryLinksRoot = Join-Path $tempRoot "dry-links"
$legacyRoot = Join-Path $tempRoot "legacy-root"
$skipLinksRoot = Join-Path $tempRoot "skip-links"
$missingWSLTarget = Join-Path $tempRoot "missing-wsl"
$oldJunctionTarget = Join-Path $tempRoot "old-junction-target"
$oldJunction = Join-Path $tempRoot "old-devstack"

$windowsInstallLink = Join-Path $linksRoot "Windows-Install"
$windowsProjectsLink = Join-Path $linksRoot "Windows-Projects"
$wslInstallLink = Join-Path $linksRoot "WSL-Install"
$wslProjectsLink = Join-Path $linksRoot "WSL-Projects"
$readmePath = Join-Path $linksRoot "README.txt"

try {
    New-Item -ItemType Directory -Force -Path $tempRoot | Out-Null
    New-Item -ItemType Directory -Force -Path $windowsRoot | Out-Null
    New-Item -ItemType Directory -Force -Path $wslInstallTarget | Out-Null
    New-Item -ItemType Directory -Force -Path $wslProjectsTarget | Out-Null

    Write-Host "devstack link harness"
    Write-Host "PowerShell: $($PSVersionTable.PSVersion) ($([System.Environment]::Version))"
    Write-Host "Temp root:  $tempRoot"

    Write-Step "First install creates four links and README.txt"
    $first = Install-DevstackLinks -LinksRoot $linksRoot -WindowsRoot $windowsRoot -WSLDistro "devstack" -WSLInstallTarget $wslInstallTarget -WSLProjectsTarget $wslProjectsTarget
    Assert-Equal 4 @($first.Created).Count "first run created four links"
    Assert-Equal 0 @($first.Warnings).Count "first run reported no warnings"
    Assert-True (Test-Path -LiteralPath $readmePath -PathType Leaf) "README.txt exists"

    Write-Step "Links have the right reparse types and targets"
    Assert-Link $windowsInstallLink $windowsRoot $script:JunctionTag "Windows-Install"
    Assert-Link $windowsProjectsLink (Join-Path $windowsRoot "projects") $script:JunctionTag "Windows-Projects"
    Assert-Link $wslInstallLink $wslInstallTarget $script:SymlinkTag "WSL-Install"
    Assert-Link $wslProjectsLink $wslProjectsTarget $script:SymlinkTag "WSL-Projects"

    Write-Step "README.txt is complete and UTF-8 without BOM"
    $readmeBytes = [System.IO.File]::ReadAllBytes($readmePath)
    $hasBom = ($readmeBytes.Length -ge 3 -and $readmeBytes[0] -eq 0xEF -and $readmeBytes[1] -eq 0xBB -and $readmeBytes[2] -eq 0xBF)
    Assert-True (-not $hasBom) "README.txt has no UTF-8 BOM"
    $readmeText = [System.IO.File]::ReadAllText($readmePath)
    foreach ($name in @("Windows-Install", "Windows-Projects", "WSL-Install", "WSL-Projects")) {
        Assert-True ($readmeText.Contains($name)) "README.txt names $name"
    }
    foreach ($target in @($windowsRoot, (Join-Path $windowsRoot "projects"), $wslInstallTarget, $wslProjectsTarget)) {
        Assert-True ($readmeText.Contains($target)) "README.txt contains target $target"
    }
    Assert-True ($readmeText -match 'live views') "README.txt says the links are live views"
    Assert-True ($readmeText -match 'not copies') "README.txt says the links are not copies"
    Assert-True ($readmeText -match 'deleting a link does not delete') "README.txt explains deleting a link is safe"
    Assert-True ($readmeText -match 'separate data sets') "README.txt says the project roots are separate data sets"
    Assert-True ($readmeText -match 'Do NOT use a link path as the "Project folder"') "README.txt forbids link paths as Add Site project folders"
    Assert-True ($readmeText -match 'install\.ps1 -Upgrade') "README.txt documents repair with install.ps1 -Upgrade"
    Assert-True ($readmeText -match 'WSL links require') "README.txt explains the WSL dependency"

    Write-Step "Second install is idempotent"
    $second = Install-DevstackLinks -LinksRoot $linksRoot -WindowsRoot $windowsRoot -WSLDistro "devstack" -WSLInstallTarget $wslInstallTarget -WSLProjectsTarget $wslProjectsTarget
    Assert-Equal 0 @($second.Created).Count "second run created no links"
    Assert-Equal 4 @($second.Skipped).Count "second run skipped four unchanged links"
    Assert-Equal 0 @($second.Warnings).Count "second run reported no warnings"
    Assert-Link $windowsInstallLink $windowsRoot $script:JunctionTag "Windows-Install after second run"
    Assert-Link $windowsProjectsLink (Join-Path $windowsRoot "projects") $script:JunctionTag "Windows-Projects after second run"
    Assert-Link $wslInstallLink $wslInstallTarget $script:SymlinkTag "WSL-Install after second run"
    Assert-Link $wslProjectsLink $wslProjectsTarget $script:SymlinkTag "WSL-Projects after second run"

    Write-Step "A stale link is repaired without deleting its old target"
    $staleWSLTarget = Join-Path $tempRoot "stale-wsl-projects"
    New-Item -ItemType Directory -Force -Path $staleWSLTarget | Out-Null
    $staleMarker = Join-Path $staleWSLTarget "stale-target-data.txt"
    Set-Content -LiteralPath $staleMarker -Value "stale target data" -Encoding Ascii
    [System.IO.Directory]::Delete($wslProjectsLink, $false)
    New-Item -ItemType SymbolicLink -Path $wslProjectsLink -Target $staleWSLTarget | Out-Null
    $repaired = Install-DevstackLinks -LinksRoot $linksRoot -WindowsRoot $windowsRoot -WSLDistro "devstack" -WSLInstallTarget $wslInstallTarget -WSLProjectsTarget $wslProjectsTarget
    Assert-Equal 1 @($repaired.Created).Count "a stale link is recreated once"
    Assert-Equal 3 @($repaired.Skipped).Count "the three correct links are still skipped"
    Assert-True (Test-Path -LiteralPath $staleMarker -PathType Leaf) "the stale link's old target data was not deleted"
    Assert-Link $wslProjectsLink $wslProjectsTarget $script:SymlinkTag "WSL-Projects after repair"

    Write-Step "A real directory at a link name is refused and preserved"
    [System.IO.Directory]::Delete($windowsProjectsLink, $false)
    New-Item -ItemType Directory -Force -Path $windowsProjectsLink | Out-Null
    $markerPath = Join-Path $windowsProjectsLink "keep-me.txt"
    Set-Content -LiteralPath $markerPath -Value "real directory data" -Encoding Ascii

    $refused = $false
    try {
        Set-DevstackDirectoryLink -Path $windowsProjectsLink -Target (Join-Path $windowsRoot "projects") -Type "Junction" | Out-Null
    } catch {
        $refused = $true
    }
    Assert-True $refused "Set-DevstackDirectoryLink refuses a real directory"
    Assert-True (Test-Path -LiteralPath $markerPath -PathType Leaf) "the real directory contents were not deleted"
    $realDirInfo = Get-TestReparseInfo $windowsProjectsLink
    Assert-True (-not $realDirInfo.IsReparsePoint) "the real directory is still a real directory"

    $refusalInstall = Install-DevstackLinks -LinksRoot $linksRoot -WindowsRoot $windowsRoot -WSLDistro "devstack" -WSLInstallTarget $wslInstallTarget -WSLProjectsTarget $wslProjectsTarget
    Assert-True (@($refusalInstall.Warnings).Count -ge 1) "Install-DevstackLinks reports the refused link as a warning"
    Assert-True (Test-Path -LiteralPath $markerPath -PathType Leaf) "the refused real directory survives a full install run"

    Write-Step "DryRun creates nothing"
    $dryRun = Install-DevstackLinks -LinksRoot $dryLinksRoot -WindowsRoot $windowsRoot -WSLDistro "devstack" -WSLInstallTarget $wslInstallTarget -WSLProjectsTarget $wslProjectsTarget -DryRun
    Assert-True (-not (Test-Path -LiteralPath $dryLinksRoot)) "DryRun does not create the links root"
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $dryLinksRoot "README.txt"))) "DryRun does not write README.txt"
    Assert-Equal 0 @($dryRun.Created).Count "DryRun creates no links"
    Assert-True (@($dryRun.Planned).Count -ge 4) "DryRun reports the planned links"

    Write-Step "Install root equal to the links root skips safely"
    New-Item -ItemType Directory -Force -Path $legacyRoot | Out-Null
    $legacyMarker = Join-Path $legacyRoot "legacy-marker.txt"
    Set-Content -LiteralPath $legacyMarker -Value "legacy install data" -Encoding Ascii
    $legacy = Install-DevstackLinks -LinksRoot $legacyRoot -WindowsRoot $legacyRoot -WSLDistro "devstack" -WSLInstallTarget $wslInstallTarget -WSLProjectsTarget $wslProjectsTarget
    Assert-Equal 0 @($legacy.Created).Count "legacy install root created no links"
    Assert-True (@($legacy.Warnings).Count -ge 1) "legacy install root returns a warning"
    Assert-True (Test-Path -LiteralPath $legacyMarker -PathType Leaf) "legacy install data is untouched"
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $legacyRoot "Windows-Install"))) "no Windows-Install link was created in the legacy root"
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $legacyRoot "README.txt"))) "no README.txt was written in the legacy root"

    Write-Step "SkipWSL with an unavailable WSL target skips only the WSL links"
    $skip = Install-DevstackLinks -LinksRoot $skipLinksRoot -WindowsRoot $windowsRoot -WSLDistro "devstack" -WSLInstallTarget $missingWSLTarget -WSLProjectsTarget (Join-Path $missingWSLTarget "projects") -SkipWSL
    Assert-Equal 2 @($skip.Created).Count "SkipWSL created the two Windows links"
    Assert-Equal 2 @($skip.Skipped).Count "SkipWSL skipped the two WSL links"
    Assert-True (@($skip.Warnings).Count -ge 1) "SkipWSL reports the skipped WSL links"
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $skipLinksRoot "WSL-Install"))) "WSL-Install was not created"
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $skipLinksRoot "WSL-Projects"))) "WSL-Projects was not created"
    Assert-True (Test-Path -LiteralPath (Join-Path $skipLinksRoot "README.txt") -PathType Leaf) "README.txt is still written"

    Write-Step "An existing reparse point at the links root is skipped, not deleted"
    New-Item -ItemType Directory -Force -Path $oldJunctionTarget | Out-Null
    $oldTargetMarker = Join-Path $oldJunctionTarget "old-target-data.txt"
    Set-Content -LiteralPath $oldTargetMarker -Value "old target data" -Encoding Ascii
    New-Item -ItemType Junction -Path $oldJunction -Target $oldJunctionTarget | Out-Null
    $old = Install-DevstackLinks -LinksRoot $oldJunction -WindowsRoot $windowsRoot -WSLDistro "devstack" -WSLInstallTarget $wslInstallTarget -WSLProjectsTarget $wslProjectsTarget
    Assert-Equal 0 @($old.Created).Count "old whole-folder junction created no links"
    Assert-True (@($old.Warnings).Count -ge 1) "old whole-folder junction returns a warning"
    Assert-True (Test-Path -LiteralPath $oldJunction -PathType Container) "old whole-folder junction is still present"
    Assert-True (Test-Path -LiteralPath $oldTargetMarker -PathType Leaf) "old junction target data is untouched"
} catch {
    $script:Failed++
    $script:Failures.Add("Unexpected harness error: $($_.Exception.Message)")
    Write-Host "  FAIL Unexpected harness error: $($_.Exception.Message)" -ForegroundColor Red
} finally {
    foreach ($path in @(
        $windowsInstallLink,
        $windowsProjectsLink,
        $wslInstallLink,
        $wslProjectsLink,
        (Join-Path $skipLinksRoot "Windows-Install"),
        (Join-Path $skipLinksRoot "Windows-Projects"),
        (Join-Path $skipLinksRoot "WSL-Install"),
        (Join-Path $skipLinksRoot "WSL-Projects"),
        $oldJunction
    )) {
        Remove-TestLink $path
    }
    Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "================================================"
if ($script:Failed -eq 0) {
    Write-Host "PASS: $($script:Passed) assertions passed under PowerShell $($PSVersionTable.PSVersion)." -ForegroundColor Green
    exit 0
}

Write-Host "FAIL: $($script:Failed) failed, $($script:Passed) passed under PowerShell $($PSVersionTable.PSVersion)." -ForegroundColor Red
foreach ($failure in $script:Failures) {
    Write-Host "  - $failure" -ForegroundColor Red
}
exit 1
