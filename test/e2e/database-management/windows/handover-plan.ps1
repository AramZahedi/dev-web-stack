# R07 option A handover plan and gated executor.
#
# Default: plan only. It reads and hashes the real install, writes a plan and a
# snapshot manifest, and changes nothing.
#
# Execute (dangerous, never run by the E2E harness automatically):
#   .\handover-plan.ps1 -Execute -Approve "I UNDERSTAND THE HANDOVER"
#
# The executor stops the real daemon/helper, snapshots state.db, config.yaml,
# helper.yaml, hosts, and binaries, points the singleton helper at
# C:\Users\Aram\.devstack-e2e, starts it, and leaves the disposable state in
# place. Run windows/rollback.ps1 afterwards to restore the real service.
#
# Known isolation gap: the WSL agent still uses the fixed /data/devstack root.
# This handover isolates Windows metadata and helper state only. Destructive
# database/service scenarios still require the LIN disposable environment or a
# separately snapshotted WSL /data/devstack. Record that as residual risk.
[CmdletBinding()]
param(
    [string]$RealHome = 'C:\Users\Aram\.devstack',
    [string]$DisposableHome = 'C:\Users\Aram\.devstack-e2e',
    [string]$SnapshotDir = '',
    [switch]$Execute,
    [string]$Approve = '',
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..\..')).Path
$stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
if ([string]::IsNullOrWhiteSpace($SnapshotDir)) {
    $SnapshotDir = Join-Path $repo "dist\e2e\database-management\windows-handover\$stamp"
}
New-Item -ItemType Directory -Force -Path $SnapshotDir | Out-Null
$snapshotReal = Join-Path $SnapshotDir 'real'
$planPath = Join-Path $SnapshotDir 'handover-plan.md'
$manifestPath = Join-Path $SnapshotDir 'snapshot-manifest.json'

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

$realFiles = @(
    (Join-Path $RealHome 'state.db'),
    (Join-Path $RealHome 'config.yaml'),
    (Join-Path $RealHome 'system\helper.yaml'),
    (Join-Path $RealHome 'bin\devstack.exe'),
    (Join-Path $RealHome 'bin\devstack-helper.exe'),
    (Join-Path $RealHome 'bin\devstack-agent')
)
$hostsPath = Join-Path $env:SystemRoot 'System32\drivers\etc\hosts'
$manifest = [ordered]@{
    generated_at = (Get-Date).ToUniversalTime().ToString('o')
    real_home = $RealHome
    disposable_home = $DisposableHome
    hosts = $hostsPath
    files = @()
    hosts_sha256 = Get-Hash $hostsPath
    elevated = Test-Elevated
}
foreach ($path in $realFiles) {
    if (Test-Path -LiteralPath $path) {
        $manifest.files += [pscustomobject]@{
            path = $path
            bytes = (Get-Item -LiteralPath $path).Length
            sha256 = Get-Hash $path
        }
    }
}

$plan = @"
# R07 option A handover plan

- Generated: $($manifest.generated_at)
- Real home: ``$RealHome``
- Disposable home: ``$DisposableHome``
- Snapshot: ``$SnapshotDir``
- Elevated: $($manifest.elevated)
- WSL agent data root: **not isolated** (fixed ``/data/devstack``)

## Preconditions

1. No other E2E run is active; no harness container uses the real engine.
2. The real install baseline is recorded by ``windows/read-only-check.ps1``.
3. The operator has approved stopping ``DevstackHelper`` and the real daemon.
4. A verified snapshot exists in ``$snapshotReal`` before any mutation.

## Snapshot contents

- ``state.db`` (copied while the daemon/helper are stopped)
- ``config.yaml``
- ``system/helper.yaml``
- ``C:\Windows\System32\drivers\etc\hosts``
- ``bin/devstack.exe``, ``bin/devstack-helper.exe``, ``bin/devstack-agent``

## Execute steps

1. ``Stop-Service DevstackHelper -Force``
2. ``devstack.exe --home "$RealHome" daemon stop``
3. Copy the files above to ``$snapshotReal`` and verify every SHA-256.
4. Create ``$DisposableHome\system\pki`` and grant ``NT AUTHORITY\SYSTEM`` full control.
5. Rewrite ``$RealHome\system\helper.yaml`` with ``system_dir``/``ca_dir``/``root_cert`` pointing at ``$DisposableHome\system``; keep ``allow_sid``, ``pipe``, and proxy ports.
6. ``Start-Service DevstackHelper`` and verify it is running.
7. Run only disposable-home tests. Do not run destructive database tests until the WSL data root is separately isolated.
8. Run ``windows/rollback.ps1 -SnapshotDir "$SnapshotDir" -Approve "I UNDERSTAND THE ROLLBACK"``.

## Rollback verification

- Every snapshot hash matches the restored file.
- ``DevstackHelper`` is running.
- ``devstack.exe version`` and ``doctor`` succeed.
- The real daemon is restarted by the next CLI command.
- The disposable home is removed only with ``-RemoveDisposable``.

## Residual risks

- The WSL agent and ``/data/devstack`` are shared with the real install.
- Service ports 3306/5432/6379/8082 are fixed; the real services must stay stopped for the disposable window.
- The helper pipe name is fixed, so the real helper and the disposable helper cannot run together.
"@
Set-Content -LiteralPath $planPath -Value $plan -Encoding utf8
$manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath -Encoding utf8
Write-Host "handover-plan: plan written to $planPath"

if (-not $Execute) {
    Write-Host 'handover-plan: plan-only mode; nothing was stopped, copied, or reconfigured.'
    exit 0
}
if ($Approve -ne 'I UNDERSTAND THE HANDOVER') {
    throw 'handover-plan: -Execute requires -Approve "I UNDERSTAND THE HANDOVER"'
}
if (-not (Test-Elevated)) {
    throw 'handover-plan: -Execute requires an elevated PowerShell session'
}
if ((Test-Path -LiteralPath (Join-Path $DisposableHome 'handover-state.json')) -and -not $Force) {
    throw "handover-plan: $DisposableHome already has handover-state.json; run rollback.ps1 or pass -Force"
}

$realHelper = Join-Path $RealHome 'system\helper.yaml'
if (-not (Test-Path -LiteralPath $realHelper)) {
    throw "handover-plan: missing $realHelper"
}
$originalHelper = Get-Content -LiteralPath $realHelper -Raw
$sidMatch = [regex]::Match($originalHelper, 'allow_sid:\s*"([^"]+)"')
if (-not $sidMatch.Success) {
    throw 'handover-plan: could not read allow_sid from helper.yaml'
}
$allowSid = $sidMatch.Groups[1].Value

New-Item -ItemType Directory -Force -Path $snapshotReal | Out-Null
try {
    Stop-Service -Name 'DevstackHelper' -Force -ErrorAction SilentlyContinue
    $devstackExe = Join-Path $RealHome 'bin\devstack.exe'
    if (Test-Path -LiteralPath $devstackExe) {
        & $devstackExe --home $RealHome daemon stop 2>&1 | Out-Null
    }
    Start-Sleep -Seconds 2

    foreach ($path in $realFiles) {
        if (Test-Path -LiteralPath $path) {
            $relative = $path.Substring($RealHome.Length).TrimStart('\')
            $destination = Join-Path $snapshotReal $relative
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
            if ((Split-Path -Leaf $path) -eq 'state.db') {
                Copy-WithShare -Source $path -Destination $destination
            } else {
                Copy-Item -LiteralPath $path -Destination $destination -Force
            }
            if ((Get-Hash $path) -ne (Get-Hash $destination)) {
                throw "handover-plan: snapshot hash mismatch for $path"
            }
        }
    }
    $hostsDestination = Join-Path $snapshotReal 'hosts'
    Copy-Item -LiteralPath $hostsPath -Destination $hostsDestination -Force
    if ((Get-Hash $hostsPath) -ne (Get-Hash $hostsDestination)) {
        throw 'handover-plan: hosts snapshot hash mismatch'
    }

    $systemDir = Join-Path $DisposableHome 'system'
    $caDir = Join-Path $systemDir 'pki'
    New-Item -ItemType Directory -Force -Path $caDir | Out-Null
    & icacls.exe $DisposableHome /grant 'NT AUTHORITY\SYSTEM:(OI)(CI)F' /T /Q | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'handover-plan: icacls failed' }

    $helperYaml = @"
allow_sid: "$allowSid"
pipe: "\\\\.\\pipe\\devstack-helper"
system_dir: "$($systemDir.Replace('\', '/'))"
ca_dir: "$($caDir.Replace('\', '/'))"
root_cert: "$((Join-Path $caDir 'root.crt').Replace('\', '/'))"
proxy_http_port: 8080
proxy_https_port: 8443
"@
    Set-Content -LiteralPath $realHelper -Value $helperYaml -Encoding utf8
    Start-Service -Name 'DevstackHelper' -ErrorAction Stop
    $service = Get-Service -Name 'DevstackHelper'
    if ($service.Status -ne 'Running') {
        throw 'handover-plan: DevstackHelper did not start'
    }
    $state = [ordered]@{
        generated_at = (Get-Date).ToUniversalTime().ToString('o')
        real_home = $RealHome
        disposable_home = $DisposableHome
        snapshot_dir = $SnapshotDir
        helper_yaml_sha256 = Get-Hash $realHelper
        service_status = $service.Status.ToString()
        wsl_data_root_isolated = $false
    }
    $state | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $DisposableHome 'handover-state.json') -Encoding utf8
    Write-Host 'handover-plan: handover executed; run rollback.ps1 to restore the real service.'
} catch {
    Write-Error $_.Exception.Message
    Write-Host 'handover-plan: attempting automatic rollback from the snapshot...'
    try {
        Stop-Service -Name 'DevstackHelper' -Force -ErrorAction SilentlyContinue
        Copy-Item -LiteralPath (Join-Path $snapshotReal 'system\helper.yaml') -Destination $realHelper -Force
        Start-Service -Name 'DevstackHelper' -ErrorAction Stop
    } catch {
        Write-Error "handover-plan: automatic rollback failed: $($_.Exception.Message)"
    }
    throw
}
