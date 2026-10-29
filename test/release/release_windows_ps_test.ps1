# Windows release artifact contract test.
#
# Builds build/release-windows.ps1 with -MockRootFS into a temporary directory,
# then asserts the ZIP contract, the standalone installer payload, and that the
# extracted installer can provision a sandboxed install with the packaged WSL
# assets (agent unit + Docker config + complete manifest).
set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$script:Failures = 0
$script:Checks = 0
function Step([string]$Message) { Write-Host "`nrelease-ps-test: === $Message ===" }
function Require([bool]$Condition, [string]$Message) {
    $script:Checks++
    if ($Condition) {
        Write-Host "release-ps-test: OK: $Message"
    } else {
        $script:Failures++
        Write-Host "release-ps-test: FAIL: $Message" -ForegroundColor Red
    }
}
function Fail([string]$Message) {
    $script:Failures++
    $script:Checks++
    Write-Host "release-ps-test: FAIL: $Message" -ForegroundColor Red
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$releaseScript = Join-Path $repoRoot "build\release-windows.ps1"
$psEngine = if ($PSVersionTable.PSEdition -eq "Core") { "pwsh" } else { "powershell" }
$version = "0.0.0-psrelease"
$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-ps-release-" + [Guid]::NewGuid().ToString("N"))
$outDir = Join-Path $tempRoot "out"
$buildDir = Join-Path $tempRoot "build"
$extractDir = Join-Path $tempRoot "extract"
$sandboxDir = Join-Path $tempRoot "sandbox"
$dryInstallDir = Join-Path $tempRoot "dry-install"
New-Item -ItemType Directory -Force -Path $outDir, $buildDir, $extractDir, $sandboxDir, $dryInstallDir | Out-Null

function Get-TempStageDirs {
    return @(
        Get-ChildItem -Path ([System.IO.Path]::GetTempPath()) -Directory -Filter "devstack-install-*" -ErrorAction SilentlyContinue |
            Select-Object -ExpandProperty FullName
    )
}

try {
    Step "Building mock release with release-windows.ps1"
    & $releaseScript -Version $version -SkipUI -MockRootFS -Output $outDir -BuildDir $buildDir | Out-Host
    Require ($LASTEXITCODE -eq 0) "release-windows.ps1 exits with code 0 (got $LASTEXITCODE)"

    $zipName = "devstack_${version}_windows_amd64.zip"
    $zipPath = Join-Path $outDir $zipName
    Require (Test-Path $zipPath) "release ZIP exists: $zipName"
    $outerChecksums = Join-Path $outDir "checksums.txt"
    Require (Test-Path $outerChecksums) "outer checksums.txt exists"

    if (Test-Path $outerChecksums) {
        $ok = $true
        foreach ($line in Get-Content $outerChecksums) {
            if ($line -notmatch '^([0-9a-fA-F]{64})\s+(.+)$') { continue }
            $expected = $Matches[1].ToLower()
            $file = Join-Path $outDir $Matches[2]
            if (-not (Test-Path $file)) { $ok = $false; continue }
            $actual = (Get-FileHash $file -Algorithm SHA256).Hash.ToLower()
            if ($actual -ne $expected) { $ok = $false }
        }
        Require $ok "outer checksums.txt verifies release artifacts"
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
    try {
        $names = @($zip.Entries | ForEach-Object { $_.FullName.Replace('\', '/') })
        foreach ($required in @(
            "devstack.exe", "devstack-helper.exe", "devstack-agent",
            "debian-12-wsl-rootfs.tar.gz", "debian-12-wsl-rootfs.tar.gz.sha256",
            "install.ps1", "uninstall.ps1", "README.md", "checksums.txt",
            "devstack.spdx.json", "devstack-daemon-task.ps1", "devstack-daemon.vbs",
            "build/wsl/agent.service", "build/wsl/install-agent.sh",
            "build/wsl/install-docker.sh", "build/wsl/docker-daemon.json"
        )) {
            Require ($names -contains $required) "release ZIP contains $required"
        }
    } finally {
        $zip.Dispose()
    }

    [System.IO.Compression.ZipFile]::ExtractToDirectory($zipPath, $extractDir)

    $rootfs = Join-Path $extractDir "debian-12-wsl-rootfs.tar.gz"
    $sidecar = Join-Path $extractDir "debian-12-wsl-rootfs.tar.gz.sha256"
    if ((Test-Path $rootfs) -and (Test-Path $sidecar)) {
        $actual = (Get-FileHash $rootfs -Algorithm SHA256).Hash.ToLower()
        $expected = ((Get-Content $sidecar -Raw) -split '\s+')[0].ToLower()
        Require ($actual -eq $expected) "rootfs sidecar matches the packaged artifact"
    }

    $sbom = Get-Content (Join-Path $extractDir "devstack.spdx.json") -Raw | ConvertFrom-Json
    $packageNames = @($sbom.packages | ForEach-Object { $_.name })
    Require ($sbom.spdxVersion -eq "SPDX-2.3") "SBOM is SPDX-2.3"
    Require ($packageNames.Count -ge 10) "SBOM lists at least 10 packages (got $($packageNames.Count))"
    foreach ($asset in @("build/wsl/agent.service", "build/wsl/install-agent.sh", "build/wsl/install-docker.sh", "build/wsl/docker-daemon.json")) {
        Require ($packageNames -contains $asset) "SBOM records $asset"
    }

    # Internal checksums must cover every packaged file, including build/wsl.
    Push-Location $extractDir
    try {
        $internalOK = $true
        foreach ($line in Get-Content "checksums.txt") {
            if ($line -notmatch '^([0-9a-fA-F]{64})\s+(.+)$') { continue }
            $expected = $Matches[1].ToLower()
            $relative = $Matches[2].Replace('\', '/')
            $file = Join-Path $extractDir ($relative.Replace('/', '\'))
            if (-not (Test-Path $file)) { $internalOK = $false; continue }
            if (((Get-FileHash $file -Algorithm SHA256).Hash.ToLower()) -ne $expected) { $internalOK = $false }
        }
        Require $internalOK "internal checksums.txt verifies every packaged file"
    } finally {
        Pop-Location
    }

    Step "Running the extracted installer in TestSandbox"
    $env:DEVSTACK_TEST_WSL_VERSION = "3.0.3"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_SYSTEMD_PID1 = "systemd"
    $installOut = & $psEngine -NoProfile -File (Join-Path $extractDir "install.ps1") -TestSandbox $sandboxDir 2>&1 | Out-String
    $installExit = $LASTEXITCODE
    Require ($installExit -eq 0) "extracted install.ps1 completes in TestSandbox (got $installExit): $installOut"
    Require (Test-Path (Join-Path $sandboxDir "mock-wsl\etc\systemd\system\devstack-agent.service")) "extracted install creates devstack-agent.service"
    Require (Test-Path (Join-Path $sandboxDir "mock-wsl\data\devstack\bin\docker-daemon.json")) "extracted install stages Docker daemon config"
    Require (Test-Path (Join-Path $sandboxDir "mock-wsl\data\devstack\bin\devstack-agent")) "extracted install stages devstack-agent"
    $manifest = Get-Content (Join-Path $sandboxDir "system\install-manifest.json") -Raw | ConvertFrom-Json
    Require ($manifest.stage -eq "complete") "extracted install writes stage=complete"
    Require ($manifest.agent_installed -eq $true) "extracted install records agent_installed=true"
    Require ($manifest.systemd_verified -eq $true) "extracted install records systemd_verified=true"

    Step "Checking the pinned-rootfs gate for supplied artifacts"
    $fakeRootfs = Join-Path $tempRoot "unpinned-rootfs.tar.gz"
    $fs = [System.IO.File]::Create($fakeRootfs)
    try { $fs.SetLength(110000000) } finally { $fs.Dispose() }
    $pinnedOutDir = Join-Path $tempRoot "out-pinned"
    $pinnedBuildDir = Join-Path $tempRoot "build-pinned"
    New-Item -ItemType Directory -Force -Path $pinnedOutDir, $pinnedBuildDir | Out-Null
    $pinnedFailed = $false
    $pinnedMessage = ""
    try {
        & $releaseScript -Version $version -SkipUI -MockRootFS -RootFSPath $fakeRootfs -Output $pinnedOutDir -BuildDir $pinnedBuildDir | Out-Null
    } catch {
        $pinnedFailed = $true
        $pinnedMessage = $_.Exception.Message
    }
    Require $pinnedFailed "release-windows.ps1 rejects an unpinned rootfs supplied with -MockRootFS"
    Require ($pinnedMessage -match "pinned rootfs_sha256") "unpinned-rootfs error names the pinned hash gate: $pinnedMessage"

    Step "Checking the standalone installer's embedded payload and cleanup"
    $installerExe = Join-Path $outDir "devstack-installer.exe"
    Require (Test-Path $installerExe) "standalone devstack-installer.exe exists"
    $wslOK = $false
    try { & wsl.exe --version 2>$null | Out-Null; $wslOK = ($LASTEXITCODE -eq 0) } catch { $wslOK = $false }
    if (-not $wslOK) {
        Write-Host "release-ps-test: skip embedded payload dry-run (WSL is unavailable)"
    } else {
        $beforeStages = Get-TempStageDirs
        $dryOut = & $installerExe --install-dir $dryInstallDir --dry-run -y 2>&1 | Out-String
        $dryExit = $LASTEXITCODE
        $afterStages = Get-TempStageDirs
        Require ($dryExit -eq 0) "standalone installer --dry-run exits 0 (got $dryExit): $dryOut"
        $leftover = @($afterStages | Where-Object { $beforeStages -notcontains $_ })
        Require ($leftover.Count -eq 0) "standalone installer removes its extraction stage directory"
        Require (-not (Test-Path (Join-Path $dryInstallDir "mock-rootfs"))) "standalone dry-run does not create mock-rootfs"
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_SYSTEMD_PID1 -ErrorAction SilentlyContinue
    Remove-Item -Path $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host "`nrelease-ps-test: Completed $script:Checks checks with $script:Failures failure(s)."
if ($script:Failures -ne 0) {
    Write-Host "release-ps-test: FAILED" -ForegroundColor Red
    exit 1
}
Write-Host "release-ps-test: ALL TESTS PASSED" -ForegroundColor Green
exit 0
