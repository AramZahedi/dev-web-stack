# Restore the real install after the R07 option A handover.
#
#   .\rollback.ps1 -SnapshotDir <dir> -Approve "I UNDERSTAND THE ROLLBACK"
#
# Verifies every snapshot hash before and after restoring state.db, config.yaml,
# helper.yaml, hosts, and binaries. Restarts DevstackHelper. The real daemon is
# started by the next devstack CLI command.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$SnapshotDir,
    [string]$RealHome = 'C:\Users\Aram\.devstack',
    [string]$DisposableHome = 'C:\Users\Aram\.devstack-e2e',
    [string]$Approve = '',
    [switch]$RemoveDisposable
)

$ErrorActionPreference = 'Stop'
$snapshotReal = Join-Path $SnapshotDir 'real'
$manifestPath = Join-Path $SnapshotDir 'snapshot-manifest.json'
$resultPath = Join-Path $SnapshotDir 'rollback-result.json'

function Test-Elevated {
    $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object System.Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-Hash {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path)) { return $null }
    try {
        return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
    } catch {
        $tmp = [System.IO.Path]::GetTempFileName()
        try {
            Copy-WithShare -Source $Path -Destination $tmp
            return (Get-FileHash -LiteralPath $tmp -Algorithm SHA256).Hash
        } finally {
            Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
        }
    }
}

function Copy-WithShare {
    param([string]$Source, [string]$Destination)
    $share = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
    $input = [System.IO.File]::Open($Source, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, $share)
    try {
        $output = [System.IO.File]::Create($Destination)
        try { $input.CopyTo($output) } finally { $output.Dispose() }
    } finally {
        $input.Dispose()
    }
}

if ($Approve -ne 'I UNDERSTAND THE ROLLBACK') {
    throw 'rollback: -Approve "I UNDERSTAND THE ROLLBACK" is required'
}
if (-not (Test-Elevated)) {
    throw 'rollback: an elevated PowerShell session is required'
}
if (-not (Test-Path -LiteralPath $manifestPath)) {
    throw "rollback: missing $manifestPath"
}
if (-not (Test-Path -LiteralPath $snapshotReal)) {
    throw "rollback: missing snapshot directory $snapshotReal"
}
$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json

# Verify the snapshot before touching the real install.
foreach ($entry in $manifest.files) {
    $relative = $entry.path.Substring($manifest.real_home.Length).TrimStart('\')
    $copy = Join-Path $snapshotReal $relative
    if (-not (Test-Path -LiteralPath $copy)) {
        throw "rollback: snapshot is missing $copy"
    }
    if ((Get-Hash $copy) -ne $entry.sha256) {
        throw "rollback: snapshot hash mismatch for $copy"
    }
}
$hostsSnapshot = Join-Path $snapshotReal 'hosts'
if (-not (Test-Path -LiteralPath $hostsSnapshot)) {
    throw 'rollback: snapshot is missing hosts'
}
if ((Get-Hash $hostsSnapshot) -ne $manifest.hosts_sha256) {
    throw 'rollback: hosts snapshot hash mismatch'
}

Stop-Service -Name 'DevstackHelper' -Force -ErrorAction SilentlyContinue
$devstackExe = Join-Path $RealHome 'bin\devstack.exe'
if (Test-Path -LiteralPath $devstackExe) {
    & $devstackExe --home $RealHome daemon stop 2>&1 | Out-Null
}
Start-Sleep -Seconds 2

foreach ($entry in $manifest.files) {
    $relative = $entry.path.Substring($manifest.real_home.Length).TrimStart('\')
    $copy = Join-Path $snapshotReal $relative
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $entry.path) | Out-Null
    if ((Split-Path -Leaf $entry.path) -eq 'state.db') {
        Copy-WithShare -Source $copy -Destination $entry.path
    } else {
        Copy-Item -LiteralPath $copy -Destination $entry.path -Force
    }
    if ((Get-Hash $entry.path) -ne $entry.sha256) {
        throw "rollback: restored hash mismatch for $($entry.path)"
    }
}
$hostsPath = Join-Path $env:SystemRoot 'System32\drivers\etc\hosts'
Copy-Item -LiteralPath $hostsSnapshot -Destination $hostsPath -Force
if ((Get-Hash $hostsPath) -ne $manifest.hosts_sha256) {
    throw 'rollback: restored hosts hash mismatch'
}

Start-Service -Name 'DevstackHelper' -ErrorAction Stop
$service = Get-Service -Name 'DevstackHelper'
if ($service.Status -ne 'Running') {
    throw 'rollback: DevstackHelper did not start'
}

$version = $null
$devstackExe = Join-Path $RealHome 'bin\devstack.exe'
if (Test-Path -LiteralPath $devstackExe) {
    $version = (& $devstackExe version 2>&1 | Out-String).Trim()
}

$result = [ordered]@{
    restored_at = (Get-Date).ToUniversalTime().ToString('o')
    snapshot_dir = $SnapshotDir
    real_home = $RealHome
    service_status = $service.Status.ToString()
    version = $version
    files_verified = $manifest.files.Count
    hosts_verified = $true
    wsl_data_root_restored = $false
}
$result | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $resultPath -Encoding utf8

if ($RemoveDisposable) {
    if (Test-Path -LiteralPath $DisposableHome) {
        Remove-Item -LiteralPath $DisposableHome -Recurse -Force
    }
}
Write-Host "rollback: OK; result written to $resultPath"
