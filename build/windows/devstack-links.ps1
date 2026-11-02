<#
.SYNOPSIS
    Creates and repairs the navigation links under C:\devstack.

.DESCRIPTION
    The devstack Windows install lives under %USERPROFILE%\.devstack and the
    managed WSL distribution keeps its data under /data/devstack. This helper
    exposes both locations through four clearly named links in C:\devstack:

        Windows-Install   junction       <WindowsRoot>
        Windows-Projects  junction       <WindowsRoot>\projects
        WSL-Install       symlink        \\wsl.localhost\<distro>\data\devstack
        WSL-Projects      symlink        \\wsl.localhost\<distro>\data\devstack\projects

    The links are navigation aids only. They are never used as site paths.
    Set-DevstackDirectoryLink refuses to replace a real file or directory and
    removes an existing link with a non-recursive delete so the target data is
    never touched.

    This file is dot-sourced by install.ps1 and is also testable on its own.
#>

function Get-DevstackNormalizedPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $full = [System.IO.Path]::GetFullPath($Path)
    return $full.TrimEnd('\', '/')
}

function Get-DevstackNormalizedLinkTarget {
    [CmdletBinding()]
    param(
        [AllowNull()]
        [string]$Target
    )

    if ([string]::IsNullOrWhiteSpace($Target)) {
        return ""
    }

    $value = $Target.Trim()
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

function Test-DevstackPathEqual {
    [CmdletBinding()]
    param(
        [AllowNull()]
        [string]$Left,
        [AllowNull()]
        [string]$Right
    )

    $a = Get-DevstackNormalizedLinkTarget $Left
    $b = Get-DevstackNormalizedLinkTarget $Right
    if ([string]::IsNullOrEmpty($a) -or [string]::IsNullOrEmpty($b)) {
        return $false
    }

    return [string]::Equals($a, $b, [System.StringComparison]::OrdinalIgnoreCase)
}

function Get-DevstackReparseTag {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    try {
        $raw = & fsutil.exe reparsepoint query $Path 2>$null | Out-String
        if ($raw -match '(?im)^\s*Reparse Tag Value\s*:\s*(0x[0-9a-fA-F]+)\s*$') {
            return [Convert]::ToUInt32($Matches[1].Substring(2), 16)
        }
    } catch {
        # Fall through to the .NET property below.
    }

    try {
        $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
        if ($null -ne $item.PSObject.Properties['LinkType']) {
            switch ([string]$item.LinkType) {
                "Junction" { return [uint32]2684354563 }
                "SymbolicLink" { return [uint32]2684354572 }
            }
        }
    } catch {
    }

    return $null
}

function Get-DevstackLinkTarget {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    try {
        $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
        $attributes = [System.IO.FileAttributes]$item.Attributes
        if (($attributes -band [System.IO.FileAttributes]::ReparsePoint) -eq 0) {
            return $null
        }

        if ($null -ne $item.PSObject.Properties['Target']) {
            $target = @($item.Target)[0]
            if (-not [string]::IsNullOrWhiteSpace([string]$target)) {
                return [string]$target
            }
        }

        if ($null -ne $item.PSObject.Properties['ResolvedTarget']) {
            $target = @($item.ResolvedTarget)[0]
            if (-not [string]::IsNullOrWhiteSpace([string]$target)) {
                return [string]$target
            }
        }
    } catch {
    }

    try {
        $raw = & fsutil.exe reparsepoint query $Path 2>$null | Out-String
        if ($raw -match '(?im)^\s*Substitute Name:\s*(.+?)\s*$') {
            return $Matches[1].Trim()
        }
    } catch {
    }

    return $null
}

function New-DevstackDirectorySymbolicLink {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [string]$Target
    )

    if (-not ('DevstackDirectorySymbolicLink' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class DevstackDirectorySymbolicLink
{
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    public static extern bool CreateSymbolicLink(string lpSymlinkFileName, string lpTargetFileName, uint dwFlags);
}
'@
    }

    # 0x1 = SYMBOLIC_LINK_FLAG_DIRECTORY, 0x2 = ALLOW_UNPRIVILEGED_CREATE.
    # Retry with only the directory flag on older Windows builds that do not
    # understand ALLOW_UNPRIVILEGED_CREATE (ERROR_INVALID_PARAMETER).
    if ([DevstackDirectorySymbolicLink]::CreateSymbolicLink($Path, $Target, [uint32]3)) {
        return
    }
    $lastError = [System.Runtime.InteropServices.Marshal]::GetLastWin32Error()
    if ($lastError -eq 87) {
        if ([DevstackDirectorySymbolicLink]::CreateSymbolicLink($Path, $Target, [uint32]1)) {
            return
        }
        $lastError = [System.Runtime.InteropServices.Marshal]::GetLastWin32Error()
    }

    throw "Cannot create directory symbolic link '$Path' -> '$Target' (Win32 error $lastError). Enable Developer Mode or run elevated."
}

function Set-DevstackDirectoryLink {
    <#
    .SYNOPSIS
        Creates or refreshes one devstack directory link.

    .DESCRIPTION
        Idempotent when the requested link already exists with the right type
        and target. A real file or directory at the link path is refused. An
        existing junction or directory symlink is removed with
        [System.IO.Directory]::Delete($path, $false), which deletes only the
        link entry and never walks into the target.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [string]$Target,

        [ValidateSet("Junction", "SymbolicLink")]
        [string]$Type = "Junction",

        [switch]$DryRun
    )

    $fullPath = Get-DevstackNormalizedPath $Path
    $fullTarget = Get-DevstackNormalizedPath $Target
    $expectedTag = if ($Type -eq "Junction") { [uint32]2684354563 } else { [uint32]2684354572 }
    $typeLabel = if ($Type -eq "Junction") { "junction" } else { "directory symbolic link" }

    $item = Get-Item -LiteralPath $fullPath -Force -ErrorAction SilentlyContinue
    if ($null -ne $item) {
        $attributes = [System.IO.FileAttributes]$item.Attributes
        if (($attributes -band [System.IO.FileAttributes]::ReparsePoint) -eq 0) {
            throw "Refusing to replace a real file or directory at '$fullPath'. Move or remove it manually, then run install.ps1 -Upgrade again."
        }
        if (-not $item.PSIsContainer) {
            throw "Refusing to replace a non-directory reparse point at '$fullPath'. Move or remove it manually, then run install.ps1 -Upgrade again."
        }

        $existingTag = Get-DevstackReparseTag $fullPath
        if ($null -eq $existingTag -or ($existingTag -ne [uint32]2684354563 -and $existingTag -ne [uint32]2684354572)) {
            throw "Refusing to replace an unsupported reparse point at '$fullPath' (tag '$existingTag'). Move or remove it manually, then run install.ps1 -Upgrade again."
        }

        $existingTarget = Get-DevstackLinkTarget $fullPath
        if ($existingTag -eq $expectedTag -and $item.PSIsContainer -and (Test-DevstackPathEqual $existingTarget $fullTarget)) {
            return [pscustomobject]@{
                Path = $fullPath
                Target = $fullTarget
                Type = $typeLabel
                Status = "Unchanged"
            }
        }

        if ($DryRun) {
            return [pscustomobject]@{
                Path = $fullPath
                Target = $fullTarget
                Type = $typeLabel
                Status = "WouldReplace"
            }
        }

        # Delete only the link entry. Never use a recursive delete here: a
        # junction or directory symlink must not be traversed into its target.
        [System.IO.Directory]::Delete($fullPath, $false)
    } elseif ($DryRun) {
        return [pscustomobject]@{
            Path = $fullPath
            Target = $fullTarget
            Type = $typeLabel
            Status = "WouldCreate"
        }
    }

    if ($Type -eq "Junction" -and -not (Test-Path -LiteralPath $fullTarget -PathType Container)) {
        throw "Cannot create junction '$fullPath': target directory '$fullTarget' does not exist."
    }

    if ($Type -eq "Junction") {
        New-Item -ItemType Junction -Path $fullPath -Target $fullTarget -ErrorAction Stop | Out-Null
    } else {
        New-DevstackDirectorySymbolicLink -Path $fullPath -Target $fullTarget
    }

    return [pscustomobject]@{
        Path = $fullPath
        Target = $fullTarget
        Type = $typeLabel
        Status = "Created"
    }
}

function Write-DevstackLinksReadme {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$LinksRoot,

        [Parameter(Mandatory = $true)]
        [string]$WindowsRoot,

        [string]$WSLDistro = "devstack",

        [string]$WSLInstallTarget = "",

        [string]$WSLProjectsTarget = "",

        [switch]$DryRun
    )

    $fullLinksRoot = Get-DevstackNormalizedPath $LinksRoot
    $fullWindowsRoot = Get-DevstackNormalizedPath $WindowsRoot
    $windowsProjectsTarget = Get-DevstackNormalizedPath (Join-Path $fullWindowsRoot "projects")
    if ([string]::IsNullOrWhiteSpace($WSLDistro)) { $WSLDistro = "devstack" }
    if ([string]::IsNullOrWhiteSpace($WSLInstallTarget)) {
        $WSLInstallTarget = "\\wsl.localhost\$WSLDistro\data\devstack"
    }
    if ([string]::IsNullOrWhiteSpace($WSLProjectsTarget)) {
        $WSLProjectsTarget = "\\wsl.localhost\$WSLDistro\data\devstack\projects"
    }
    $WSLInstallTarget = Get-DevstackNormalizedPath $WSLInstallTarget
    $WSLProjectsTarget = Get-DevstackNormalizedPath $WSLProjectsTarget

    $readmePath = Join-Path $fullLinksRoot "README.txt"
    $readmeItem = Get-Item -LiteralPath $readmePath -Force -ErrorAction SilentlyContinue
    if ($null -ne $readmeItem) {
        $attributes = [System.IO.FileAttributes]$readmeItem.Attributes
        if (($attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0 -or $readmeItem.PSIsContainer) {
            throw "Refusing to replace a non-file entry at '$readmePath'. Move or remove it manually, then run install.ps1 -Upgrade again."
        }
    }

    if ($DryRun) {
        return [pscustomobject]@{
            Path = $readmePath
            Status = "WouldWrite"
        }
    }

    $content = @"
devstack links
==============

This folder ($fullLinksRoot) contains four links that make the managed devstack
locations easy to open in Explorer, a terminal, or an editor. They are live views
of the real folders, not copies. A change made through a link changes the target,
and deleting a link does not delete any data.

The links
---------

$fullLinksRoot\Windows-Install
    Type:   directory junction
    Target: $fullWindowsRoot
    The Windows installation root: binaries, config.yaml, state.db, logs, PKI,
    protected system state, and the managed WSL distro disk.

$fullLinksRoot\Windows-Projects
    Type:   directory junction
    Target: $windowsProjectsTarget
    Windows-mode project folders on NTFS. This is the Windows managed projects
    root.

$fullLinksRoot\WSL-Install
    Type:   directory symbolic link
    Target: $WSLInstallTarget
    The managed WSL distribution's devstack root: agent, runtimes, services,
    Docker data, and generated site configuration.

$fullLinksRoot\WSL-Projects
    Type:   directory symbolic link
    Target: $WSLProjectsTarget
    WSL-mode project folders on the distribution's ext4 disk. This is the WSL
    managed projects root.

Windows-Projects and WSL-Projects are separate data sets. A project stored in
one is not visible in the other. Switching a site between Windows and WSL source
mode copies the project between the two roots.

Using the links
---------------

The WSL links require the managed "$WSLDistro" WSL distribution to be installed
and available. If the distribution is stopped, opening a WSL link may start it
or fail until WSL is ready. The Windows links work without WSL. On a
-SkipWSL install, the WSL links may be absent until the distribution is
installed; rerun install.ps1 -Upgrade afterwards to create them.

Do NOT use a link path as the "Project folder" when adding a site. devstack
requires a site path to be a strict descendant of the managed projects root and
rejects link paths. Use the canonical target path instead:

    Windows mode: $windowsProjectsTarget\<site>
    WSL mode:     $WSLProjectsTarget\<site>

The dashboard's folder browser already starts at the correct managed root.

Deleting and repairing
----------------------

Deleting one of these links (for example in Explorer) removes only the link.
The target folder and all of its data remain. Do not delete the targets.

To recreate or repair the links, run the installer upgrade from an elevated
PowerShell:

    .\install.ps1 -Upgrade

The installer recreates missing links, repairs links with the wrong target or
type, and refreshes this README.txt. It never replaces a real folder or file at
a link path.

If an older installation replaced $fullLinksRoot with a single whole-folder
junction to $fullWindowsRoot, remove that junction first. The installer cannot
create the four links while $fullLinksRoot itself is a link, and it will not
delete the old junction automatically.

Generated by the devstack installer. Do not edit; it is refreshed on upgrade.
"@

    $content = $content -replace "`r?`n", "`r`n"
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText($readmePath, $content, $encoding)

    return [pscustomobject]@{
        Path = $readmePath
        Status = "Written"
    }
}

function Add-DevstackLinkResult {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name,

        [Parameter(Mandatory = $true)]
        [string]$LinkPath,

        [Parameter(Mandatory = $true)]
        [string]$Target,

        [Parameter(Mandatory = $true)]
        [string]$Type,

        [switch]$DryRun,

        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.Generic.List[string]]$Created,

        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.Generic.List[string]]$Skipped,

        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.Generic.List[string]]$Planned,

        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [System.Collections.Generic.List[string]]$Warnings
    )

    try {
        $result = Set-DevstackDirectoryLink -Path $LinkPath -Target $Target -Type $Type -DryRun:$DryRun
        switch ($result.Status) {
            "Created" { $Created.Add($LinkPath) }
            "Unchanged" { $Skipped.Add($LinkPath) }
            "WouldCreate" { $Planned.Add($LinkPath) }
            "WouldReplace" { $Planned.Add($LinkPath) }
        }
    } catch {
        $Warnings.Add("$Name link at '$LinkPath' was not created: $($_.Exception.Message)")
    }
}

function Install-DevstackLinks {
    <#
    .SYNOPSIS
        Creates the four C:\devstack links and README.txt.

    .DESCRIPTION
        Safe to run repeatedly. Existing links with the right type and target
        are left alone. Real files/directories are never replaced. When the
        install root is C:\devstack itself the links cannot be hosted and the
        function returns without changing anything. When C:\devstack already
        exists as a reparse point it is left untouched with a warning.

        Returns an object with Created, Skipped, Planned, and Warnings. The
        function does not throw for expected refusals; the caller can print the
        warnings without failing an otherwise successful install.
    #>
    [CmdletBinding()]
    param(
        [string]$LinksRoot = "C:\devstack",

        [string]$WindowsRoot = "",

        [string]$WSLDistro = "devstack",

        [string]$WSLInstallTarget = "",

        [string]$WSLProjectsTarget = "",

        [switch]$SkipWSL,

        [switch]$DryRun
    )

    $created = New-Object System.Collections.Generic.List[string]
    $skipped = New-Object System.Collections.Generic.List[string]
    $planned = New-Object System.Collections.Generic.List[string]
    $warnings = New-Object System.Collections.Generic.List[string]

    if ([string]::IsNullOrWhiteSpace($WindowsRoot)) {
        $WindowsRoot = Join-Path $env:USERPROFILE ".devstack"
    }
    if ([string]::IsNullOrWhiteSpace($WSLDistro)) {
        $WSLDistro = "devstack"
    }

    try {
        $fullLinksRoot = Get-DevstackNormalizedPath $LinksRoot
        $fullWindowsRoot = Get-DevstackNormalizedPath $WindowsRoot
    } catch {
        $warnings.Add("Cannot resolve the devstack link paths: $($_.Exception.Message)")
        return [pscustomobject]@{
            LinksRoot = $LinksRoot
            Created = @()
            Skipped = @()
            Planned = @()
            Warnings = @($warnings)
            DryRun = [bool]$DryRun
        }
    }

    $result = [pscustomobject]@{
        LinksRoot = $fullLinksRoot
        Created = @()
        Skipped = @()
        Planned = @()
        Warnings = @()
        DryRun = [bool]$DryRun
    }

    if (Test-DevstackPathEqual $fullLinksRoot $fullWindowsRoot) {
        $warnings.Add("Skipping the $fullLinksRoot links: the installation root is $fullLinksRoot itself (legacy layout). The links need a separate C:\devstack folder.")
        $result.Warnings = @($warnings)
        return $result
    }

    $linksItem = Get-Item -LiteralPath $fullLinksRoot -Force -ErrorAction SilentlyContinue
    if ($null -ne $linksItem) {
        $attributes = [System.IO.FileAttributes]$linksItem.Attributes
        if (($attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
            $warnings.Add("Skipping the $fullLinksRoot links: the path already exists as a reparse point (junction or symlink). Remove it manually if it is an old whole-folder junction, then run install.ps1 -Upgrade.")
            $result.Warnings = @($warnings)
            return $result
        }
        if (-not $linksItem.PSIsContainer) {
            $warnings.Add("Skipping the $fullLinksRoot links: the path exists but is not a directory.")
            $result.Warnings = @($warnings)
            return $result
        }
    } elseif (-not $DryRun) {
        try {
            New-Item -ItemType Directory -Force -Path $fullLinksRoot -ErrorAction Stop | Out-Null
        } catch {
            $warnings.Add("Cannot create $fullLinksRoot for the devstack links: $($_.Exception.Message)")
            $result.Warnings = @($warnings)
            return $result
        }
    }

    $windowsProjectsTarget = Get-DevstackNormalizedPath (Join-Path $fullWindowsRoot "projects")
    if (-not (Test-Path -LiteralPath $fullWindowsRoot -PathType Container)) {
        $warnings.Add("Skipping the Windows links: the Windows install root '$fullWindowsRoot' does not exist.")
        $skipped.Add((Join-Path $fullLinksRoot "Windows-Install"))
        $skipped.Add((Join-Path $fullLinksRoot "Windows-Projects"))
    } else {
        if (-not (Test-Path -LiteralPath $windowsProjectsTarget -PathType Container)) {
            if (-not $DryRun) {
                try {
                    New-Item -ItemType Directory -Force -Path $windowsProjectsTarget -ErrorAction Stop | Out-Null
                } catch {
                    $warnings.Add("Cannot create the Windows projects root '$windowsProjectsTarget': $($_.Exception.Message)")
                }
            }
        }

        Add-DevstackLinkResult -Name "Windows-Install" -LinkPath (Join-Path $fullLinksRoot "Windows-Install") -Target $fullWindowsRoot -Type "Junction" -DryRun:$DryRun -Created $created -Skipped $skipped -Planned $planned -Warnings $warnings
        Add-DevstackLinkResult -Name "Windows-Projects" -LinkPath (Join-Path $fullLinksRoot "Windows-Projects") -Target $windowsProjectsTarget -Type "Junction" -DryRun:$DryRun -Created $created -Skipped $skipped -Planned $planned -Warnings $warnings
    }

    if ([string]::IsNullOrWhiteSpace($WSLInstallTarget)) {
        $WSLInstallTarget = "\\wsl.localhost\$WSLDistro\data\devstack"
    }
    if ([string]::IsNullOrWhiteSpace($WSLProjectsTarget)) {
        $WSLProjectsTarget = "\\wsl.localhost\$WSLDistro\data\devstack\projects"
    }

    $wslInstallLink = Join-Path $fullLinksRoot "WSL-Install"
    $wslProjectsLink = Join-Path $fullLinksRoot "WSL-Projects"
    $createWSL = $true
    if ($SkipWSL -and -not (Test-Path -LiteralPath $WSLInstallTarget)) {
        $createWSL = $false
        $warnings.Add("Skipping the WSL links (WSL-Install and WSL-Projects): -SkipWSL was used and the WSL target '$WSLInstallTarget' is not available.")
        $skipped.Add($wslInstallLink)
        $skipped.Add($wslProjectsLink)
    }

    if ($createWSL) {
        Add-DevstackLinkResult -Name "WSL-Install" -LinkPath $wslInstallLink -Target $WSLInstallTarget -Type "SymbolicLink" -DryRun:$DryRun -Created $created -Skipped $skipped -Planned $planned -Warnings $warnings
        Add-DevstackLinkResult -Name "WSL-Projects" -LinkPath $wslProjectsLink -Target $WSLProjectsTarget -Type "SymbolicLink" -DryRun:$DryRun -Created $created -Skipped $skipped -Planned $planned -Warnings $warnings
    }

    try {
        $readme = Write-DevstackLinksReadme -LinksRoot $fullLinksRoot -WindowsRoot $fullWindowsRoot -WSLDistro $WSLDistro -WSLInstallTarget $WSLInstallTarget -WSLProjectsTarget $WSLProjectsTarget -DryRun:$DryRun
        if ($readme.Status -eq "WouldWrite") {
            $planned.Add($readme.Path)
        }
    } catch {
        $warnings.Add("README.txt at '$fullLinksRoot' was not written: $($_.Exception.Message)")
    }

    $result.Created = @($created)
    $result.Skipped = @($skipped)
    $result.Planned = @($planned)
    $result.Warnings = @($warnings)
    return $result
}
