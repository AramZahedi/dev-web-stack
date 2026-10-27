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
    [switch]$Help
)

$ErrorActionPreference = "Stop"

if ($Help -or -not $Version) {
    Write-Host @"
Usage: release-windows.ps1 -Version <semver> [-Output <path>] [-BuildDir <path>] [-RootFSPath <path>] [-SkipUI] [-Help]

Parameters:
    -Version <semver>      Release version without leading 'v' (required, e.g. 0.1.0)
    -Output <path>         Output directory (default: dist\release)
    -BuildDir <path>       Directory for intermediate binaries (default: dist)
    -RootFSPath <path>     Path to WSL rootfs archive (optional)
    -SkipUI                Skip UI build step
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
    Copy-Item -Path (Join-Path $repoRoot "README.md") -Destination (Join-Path $stageDir "README.md") -Force
    if (Test-Path (Join-Path $repoRoot "LICENSE")) {
        Copy-Item -Path (Join-Path $repoRoot "LICENSE") -Destination (Join-Path $stageDir "LICENSE") -Force
    }

    # RootFS archive
    $targetRootFS = Join-Path $stageDir "debian-12-wsl-rootfs.tar.gz"
    if ($RootFSPath -and (Test-Path $RootFSPath)) {
        Copy-Item -Path $RootFSPath -Destination $targetRootFS -Force
    } elseif (Test-Path (Join-Path $repoRoot "build\wsl\debian-12-wsl-rootfs.tar.gz")) {
        Copy-Item -Path (Join-Path $repoRoot "build\wsl\debian-12-wsl-rootfs.tar.gz") -Destination $targetRootFS -Force
    } else {
        # Minimal rootfs placeholder
        $mockDir = Join-Path $env:TEMP ("mock-rootfs-" + [System.Guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Force -Path (Join-Path $mockDir "etc") | Out-Null
        New-Item -ItemType Directory -Force -Path (Join-Path $mockDir "data\devstack") | Out-Null
        Set-Content -Path (Join-Path $mockDir "etc\wsl.conf") -Value "[boot]`nsystemd=true`n[network]`ngenerateHosts=false`n"
        & tar.exe -czf $targetRootFS -C $mockDir .
        Remove-Item -Recurse -Force $mockDir -ErrorAction SilentlyContinue
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

    $zipHash = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
    Set-Content -Path (Join-Path $outDir "checksums.txt") -Value "$zipHash  $zipName`n" -Encoding ascii

    Write-Host "release-windows: Created $zipPath ($zipHash)"
} finally {
    Remove-Item -Recurse -Force $stageDir -ErrorAction SilentlyContinue
}
