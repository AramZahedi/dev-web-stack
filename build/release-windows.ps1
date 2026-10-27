<#
.SYNOPSIS
    Build and package the Windows devstack release archive.
.DESCRIPTION
    Compiles devstack.exe, devstack-helper.exe, and devstack-agent (Linux ELF),
    stages the WSL rootfs, install.ps1, uninstall.ps1, SPDX SBOM, and checksums,
    and packages them into a Windows release archive (ZIP).
.PARAMETER Version
    Release semver without leading "v" (e.g. 0.1.0).
.PARAMETER Output
    Output directory. Defaults to dist\release.
.PARAMETER BuildDir
    Directory for intermediate binaries. Defaults to dist.
.PARAMETER RootFSPath
    Path to debian-12-wsl-rootfs.tar.gz (optional).
.PARAMETER SkipUI
    Skip UI npm build step.
.PARAMETER MockRootFS
    Allow generating a mock rootfs placeholder (for testing/development only).
.PARAMETER Help
    Show usage and help.
#>

[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Output = "dist\release",
    [string]$BuildDir = "dist",
    [string]$RootFSPath = "",
    [switch]$SkipUI,
    [switch]$MockRootFS,
    [switch]$Help
)

$ErrorActionPreference = "Stop"

if ($Help -or -not $Version) {
    Write-Host @"
Usage: release-windows.ps1 -Version <semver> [-Output <path>] [-BuildDir <path>] [-RootFSPath <path>] [-SkipUI] [-MockRootFS] [-Help]

Parameters:
    -Version <semver>      Release version without leading 'v' (required, e.g. 0.1.0)
    -Output <path>         Output directory (default: dist\release)
    -BuildDir <path>       Directory for intermediate binaries (default: dist)
    -RootFSPath <path>     Path to WSL rootfs archive (optional)
    -SkipUI                Skip UI build step
    -MockRootFS            Allow generating a mock rootfs placeholder (for testing/development only)
    -Help                  Display this help message
"@
    if ($Help) { exit 0 } else { exit 1 }
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$normVersion = $Version.TrimStart("v")
if ($normVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$') {
    Write-Error "Invalid version '$Version'. Expected semver like 0.1.0"
    exit 1
}

$outDir = if ([System.IO.Path]::IsPathRooted($Output)) { $Output } else { Join-Path $repoRoot $Output }
$bldDir = if ([System.IO.Path]::IsPathRooted($BuildDir)) { $BuildDir } else { Join-Path $repoRoot $BuildDir }
New-Item -ItemType Directory -Force -Path $outDir | Out-Null
New-Item -ItemType Directory -Force -Path $bldDir | Out-Null

$stageDir = Join-Path $env:TEMP ("devstack-win-release-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $stageDir | Out-Null

try {
    if (-not $SkipUI -and (Test-Path (Join-Path $repoRoot "ui\package.json"))) {
        Write-Host "release-windows: Building UI..."
        Push-Location (Join-Path $repoRoot "ui")
        try {
            npm ci
            npm run build
        } finally {
            Pop-Location
        }
    }

    $commit = try { (& git -C $repoRoot rev-parse --short HEAD 2>$null).Trim() } catch { "unknown" }
    $ldflags = "-s -w -X github.com/AramZahedi/dev-web-stack/internal/version.Version=$normVersion -X github.com/AramZahedi/dev-web-stack/internal/version.Commit=$commit"

    # Build devstack.exe
    Write-Host "release-windows: Building devstack.exe..."
    $devstackExe = Join-Path $bldDir "devstack.exe"
    $env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
    & go build -trimpath -ldflags $ldflags -o $devstackExe (Join-Path $repoRoot "cmd\devstack")
    if ($LASTEXITCODE -ne 0) { throw "go build devstack.exe failed" }

    # Build devstack-helper.exe
    Write-Host "release-windows: Building devstack-helper.exe..."
    $helperExe = Join-Path $bldDir "devstack-helper.exe"
    & go build -trimpath -ldflags $ldflags -o $helperExe (Join-Path $repoRoot "cmd\devstack-helper")
    if ($LASTEXITCODE -ne 0) { throw "go build devstack-helper.exe failed" }

    # Build devstack-agent (Linux ELF)
    Write-Host "release-windows: Building devstack-agent (linux/amd64)..."
    $agentElf = Join-Path $bldDir "devstack-agent"
    $env:GOOS = "linux"
    & go build -trimpath -ldflags $ldflags -o $agentElf (Join-Path $repoRoot "cmd\devstack-agent")
    if ($LASTEXITCODE -ne 0) { throw "go build devstack-agent failed" }
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue

    # Copy binaries to stage
    Copy-Item -Path $devstackExe -Destination (Join-Path $stageDir "devstack.exe") -Force
    Copy-Item -Path $helperExe -Destination (Join-Path $stageDir "devstack-helper.exe") -Force
    Copy-Item -Path $agentElf -Destination (Join-Path $stageDir "devstack-agent") -Force

    # Copy scripts and docs
    Copy-Item -Path (Join-Path $repoRoot "install.ps1") -Destination (Join-Path $stageDir "install.ps1") -Force
    Copy-Item -Path (Join-Path $repoRoot "uninstall.ps1") -Destination (Join-Path $stageDir "uninstall.ps1") -Force
    if (Test-Path (Join-Path $repoRoot "build\windows\devstack-daemon-task.ps1")) {
        Copy-Item -Path (Join-Path $repoRoot "build\windows\devstack-daemon-task.ps1") -Destination (Join-Path $stageDir "devstack-daemon-task.ps1") -Force
    }
    Copy-Item -Path (Join-Path $repoRoot "README.md") -Destination (Join-Path $stageDir "README.md") -Force
    if (Test-Path (Join-Path $repoRoot "LICENSE")) {
        Copy-Item -Path (Join-Path $repoRoot "LICENSE") -Destination (Join-Path $stageDir "LICENSE") -Force
    }

    # RootFS archive
    $rootfsMinBytes = 1073741824 # 1GB minimum for real release archive
    $versionsLockPath = Join-Path $repoRoot "build\wsl\versions.lock"
    if (Test-Path $versionsLockPath) {
        Get-Content $versionsLockPath | ForEach-Object {
            if ($_ -match '^\s*rootfs_minimum_bytes\s*=\s*(\d+)') {
                $rootfsMinBytes = [int64]$Matches[1]
            }
        }
    }

    $targetRootFS = Join-Path $stageDir "debian-12-wsl-rootfs.tar.gz"
    if ($RootFSPath) {
        if (-not (Test-Path $RootFSPath)) {
            throw "release-windows: Specified -RootFSPath '$RootFSPath' does not exist."
        }
        Copy-Item -Path $RootFSPath -Destination $targetRootFS -Force
    } elseif (Test-Path (Join-Path $repoRoot "build\wsl\debian-12-wsl-rootfs.tar.gz")) {
        Copy-Item -Path (Join-Path $repoRoot "build\wsl\debian-12-wsl-rootfs.tar.gz") -Destination $targetRootFS -Force
    } elseif (Test-Path (Join-Path $repoRoot "dist\wsl\debian-12-wsl-rootfs.tar.gz")) {
        Copy-Item -Path (Join-Path $repoRoot "dist\wsl\debian-12-wsl-rootfs.tar.gz") -Destination $targetRootFS -Force
    } elseif ($MockRootFS) {
        # Minimal rootfs placeholder
        Write-Host "release-windows: creating minimal WSL rootfs archive (-MockRootFS)..."
        $mockDir = Join-Path $env:TEMP ("mock-rootfs-" + [System.Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Force -Path (Join-Path $mockDir "etc") | Out-Null
        New-Item -ItemType Directory -Force -Path (Join-Path $mockDir "data\devstack") | Out-Null
        Set-Content -Path (Join-Path $mockDir "etc\wsl.conf") -Value "[boot]`nsystemd=true`n[network]`ngenerateHosts=false`n"
        & tar.exe -czf $targetRootFS -C $mockDir .
        Remove-Item -Recurse -Force $mockDir -ErrorAction SilentlyContinue
    } else {
        throw "release-windows: WSL rootfs archive not found. Expected build\wsl\debian-12-wsl-rootfs.tar.gz or -RootFSPath <path>. Real release builds require a real rootfs archive (>= 1GB). To allow mock rootfs for development/testing, pass -MockRootFS."
    }

    if (-not $MockRootFS) {
        $rootfsSize = (Get-Item $targetRootFS).Length
        if ($rootfsSize -lt $rootfsMinBytes) {
            throw "release-windows: WSL rootfs archive '$targetRootFS' size ($rootfsSize bytes) is below minimum required $rootfsMinBytes bytes (1GB) for a release build. Use -MockRootFS for development/testing builds."
        }
    }

    $rootfsHash = (Get-FileHash -Path $targetRootFS -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path (Join-Path $stageDir "debian-12-wsl-rootfs.tar.gz.sha256") -Value "$rootfsHash  debian-12-wsl-rootfs.tar.gz`n" -NoNewline

    # Generate SPDX SBOM
    $createdDate = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
    $devHash = (Get-FileHash -Path (Join-Path $stageDir "devstack.exe") -Algorithm SHA256).Hash.ToLower()
    $helpHash = (Get-FileHash -Path (Join-Path $stageDir "devstack-helper.exe") -Algorithm SHA256).Hash.ToLower()
    $agtHash = (Get-FileHash -Path (Join-Path $stageDir "devstack-agent") -Algorithm SHA256).Hash.ToLower()
    $instHash = (Get-FileHash -Path (Join-Path $stageDir "install.ps1") -Algorithm SHA256).Hash.ToLower()
    $uninstHash = (Get-FileHash -Path (Join-Path $stageDir "uninstall.ps1") -Algorithm SHA256).Hash.ToLower()

    $sbom = @"
{
  "spdxVersion": "SPDX-2.3",
  "dataLicense": "CC0-1.0",
  "SPDXID": "SPDXRef-DOCUMENT",
  "name": "devstack-windows-amd64-$normVersion",
  "documentNamespace": "https://github.com/AramZahedi/dev-web-stack/spdx/devstack-$normVersion-windows-amd64",
  "creationInfo": {
    "creators": [
      "Tool: devstack-release-windows-$normVersion",
      "Organization: devstack contributors"
    ],
    "created": "$createdDate"
  },
  "packages": [
    {
      "SPDXID": "SPDXRef-Package-devstack-exe",
      "name": "devstack.exe",
      "versionInfo": "$normVersion",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [{ "algorithm": "SHA256", "checksumValue": "$devHash" }]
    },
    {
      "SPDXID": "SPDXRef-Package-devstack-helper-exe",
      "name": "devstack-helper.exe",
      "versionInfo": "$normVersion",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [{ "algorithm": "SHA256", "checksumValue": "$helpHash" }]
    },
    {
      "SPDXID": "SPDXRef-Package-devstack-agent",
      "name": "devstack-agent",
      "versionInfo": "$normVersion",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [{ "algorithm": "SHA256", "checksumValue": "$agtHash" }]
    },
    {
      "SPDXID": "SPDXRef-Package-wsl-rootfs",
      "name": "debian-12-wsl-rootfs.tar.gz",
      "versionInfo": "bookworm-snapshot",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "NOASSERTION",
      "checksums": [{ "algorithm": "SHA256", "checksumValue": "$rootfsHash" }]
    },
    {
      "SPDXID": "SPDXRef-Package-install-ps1",
      "name": "install.ps1",
      "versionInfo": "$normVersion",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [{ "algorithm": "SHA256", "checksumValue": "$instHash" }]
    },
    {
      "SPDXID": "SPDXRef-Package-uninstall-ps1",
      "name": "uninstall.ps1",
      "versionInfo": "$normVersion",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [{ "algorithm": "SHA256", "checksumValue": "$uninstHash" }]
    }
  ]
}
"@
    Set-Content -Path (Join-Path $stageDir "devstack.spdx.json") -Value $sbom -Encoding utf8

    # Internal checksums.txt
    $stageFiles = Get-ChildItem -File -Path $stageDir
    $manifestLines = @()
    foreach ($f in ($stageFiles | Sort-Object Name)) {
        $h = (Get-FileHash -Path $f.FullName -Algorithm SHA256).Hash.ToLower()
        $manifestLines += "$h  $($f.Name)"
    }
    Set-Content -Path (Join-Path $stageDir "checksums.txt") -Value ($manifestLines -join "`n") -Encoding ascii

    # Archive creation
    $zipName = "devstack_${normVersion}_windows_amd64.zip"
    $zipPath = Join-Path $outDir $zipName
    if (Test-Path $zipPath) { Remove-Item -Force $zipPath }
    Compress-Archive -Path (Join-Path $stageDir "*") -DestinationPath $zipPath -Force

    # Also generate tar.gz archive for release parity
    $tarName = "devstack_${normVersion}_windows_amd64.tar.gz"
    $tarPath = Join-Path $outDir $tarName
    if (Test-Path $tarPath) { Remove-Item -Force $tarPath }
    & tar.exe -czf $tarPath -C $stageDir .
    $tarHash = if (Test-Path $tarPath) { (Get-FileHash -Path $tarPath -Algorithm SHA256).Hash.ToLower() } else { "" }

    $zipHash = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
    # Build standalone binary installer executable (devstack-installer.exe)
    Write-Host "release-windows: Building standalone devstack-installer.exe..."
    $installerStageDir = Join-Path $bldDir (".installer-stage-" + [System.Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $installerStageDir | Out-Null
    try {
        Copy-Item -Path (Join-Path $repoRoot "cmd\devstack-installer\*.go") -Destination $installerStageDir -Force
        Copy-Item -Path $zipPath -Destination (Join-Path $installerStageDir "payload.zip") -Force

        $installerExe = Join-Path $outDir "devstack-installer.exe"
        $env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
        & go build -trimpath -ldflags $ldflags -o $installerExe $installerStageDir
        if ($LASTEXITCODE -ne 0) { throw "go build devstack-installer failed" }
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue

        Copy-Item -Path $installerExe -Destination (Join-Path $outDir "devstack-setup.exe") -Force
        $instHash = (Get-FileHash -Path $installerExe -Algorithm SHA256).Hash.ToLower()
        Write-Host "release-windows: Created $installerExe ($instHash)"
    } finally {
        Remove-Item -Recurse -Force $installerStageDir -ErrorAction SilentlyContinue
        # Ensure git-tracked payload.zip was never altered, restore sentinel if needed
        $trackedPayload = Join-Path $repoRoot "cmd\devstack-installer\payload.zip"
        $emptyZipBytes = [byte[]]@(0x50, 0x4B, 0x05, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
        if ((Test-Path $trackedPayload) -and (Get-Item $trackedPayload).Length -ne 22) {
            [System.IO.File]::WriteAllBytes($trackedPayload, $emptyZipBytes)
        }
    }

    $manifestLinesOut = @("$zipHash  $zipName")
    if ($tarHash) {
        $manifestLinesOut += "$tarHash  $tarName"
    }
    $manifestLinesOut += "$instHash  devstack-installer.exe"
    $manifestLinesOut += "$instHash  devstack-setup.exe"
    $manifestContent = ($manifestLinesOut -join "`n") + "`n"
    Set-Content -Path (Join-Path $outDir "checksums.txt") -Value $manifestContent -Encoding ascii

    Write-Host "release-windows: Created $zipPath ($zipHash)"
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
}
