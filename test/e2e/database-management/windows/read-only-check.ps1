# Windows read-only checks for the database-management E2E run.
#
# This script never starts, stops, installs, upgrades, or reconfigures
# anything. It reads the real install, writes evidence, hashes files, and runs
# the secret scanner. It is safe to run while the real services are active.
[CmdletBinding()]
param(
    [string]$InstallHome = 'C:\Users\Aram\.devstack',
    [string]$EvidenceDir = '',
    [string]$DevstackExe = '',
    [switch]$SkipSecretScan
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..\..')).Path
$stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
    $EvidenceDir = Join-Path $repo "dist\e2e\database-management\evidence\$stamp\WIN-REAL-readonly"
}
New-Item -ItemType Directory -Force -Path $EvidenceDir | Out-Null

if ([string]::IsNullOrWhiteSpace($DevstackExe)) {
    $candidate = Join-Path $InstallHome 'bin\devstack.exe'
    if (Test-Path -LiteralPath $candidate) {
        $DevstackExe = $candidate
    } else {
        $DevstackExe = (Get-Command devstack.exe -ErrorAction SilentlyContinue).Source
    }
}

function Write-EvidenceText {
    param([string]$Name, [string]$Text)
    Set-Content -LiteralPath (Join-Path $EvidenceDir $Name) -Value $Text -Encoding utf8
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

function Invoke-Capture {
    param([string]$Name, [scriptblock]$Block)
    $output = ''
    $code = 0
    try {
        $output = (& $Block 2>&1 | Out-String)
        if ($null -ne $LASTEXITCODE) { $code = $LASTEXITCODE }
    } catch {
        $output = $_.Exception.Message
        $code = 1
    }
    Write-EvidenceText "$Name.txt" $output
    return [pscustomobject]@{ Name = $Name; ExitCode = $code; Bytes = $output.Length }
}

$summary = [ordered]@{
    generated_at = (Get-Date).ToUniversalTime().ToString('o')
    home = $InstallHome
    devstack_exe = $DevstackExe
    read_only = $true
    commands = @()
    files = @()
    services = @()
    ports = @()
    warnings = @()
}

if ([string]::IsNullOrWhiteSpace($DevstackExe) -or -not (Test-Path -LiteralPath $DevstackExe)) {
    $summary.warnings += 'devstack.exe not found; CLI captures skipped'
} else {
    $summary.commands += Invoke-Capture 'version' { & $DevstackExe version }
    $summary.commands += Invoke-Capture 'doctor' { & $DevstackExe --home $InstallHome --json doctor }
    $summary.commands += Invoke-Capture 'service-list' { & $DevstackExe --home $InstallHome --json service list }
    $summary.commands += Invoke-Capture 'site-list' { & $DevstackExe --home $InstallHome --json site list }
    foreach ($service in @('mysql', 'postgres', 'redis')) {
        $summary.commands += Invoke-Capture "db-list-$service" { & $DevstackExe --home $InstallHome --json db list --service $service }
        $summary.commands += Invoke-Capture "db-user-list-$service" { & $DevstackExe --home $InstallHome --json db user list --service $service }
        $summary.commands += Invoke-Capture "db-backup-list-$service" { & $DevstackExe --home $InstallHome --json db backup list --service $service }
    }
}

$summary.services += [pscustomobject]@{
    name = 'DevstackHelper'
    status = (Get-Service -Name 'DevstackHelper' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Status -ErrorAction SilentlyContinue)
    start_type = (Get-Service -Name 'DevstackHelper' -ErrorAction SilentlyContinue | Select-Object -ExpandProperty StartType -ErrorAction SilentlyContinue)
}
$summary.services += [pscustomobject]@{
    name = 'devstack-daemon'
    processes = @(Get-Process -Name 'devstack' -ErrorAction SilentlyContinue | Select-Object Id, Path)
}

foreach ($port in @(3306, 5432, 6379, 8082, 7070)) {
    if (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue) {
        $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue |
            Select-Object LocalAddress, LocalPort, OwningProcess)
        $summary.ports += [pscustomobject]@{ port = $port; listeners = $listeners }
    } else {
        $summary.warnings += 'Get-NetTCPConnection unavailable; port capture skipped'
        break
    }
}

$paths = @(
    (Join-Path $InstallHome 'state.db'),
    (Join-Path $InstallHome 'config.yaml'),
    (Join-Path $InstallHome 'system\helper.yaml'),
    (Join-Path $InstallHome 'bin\devstack.exe'),
    (Join-Path $InstallHome 'bin\devstack-helper.exe'),
    (Join-Path $InstallHome 'bin\devstack-agent')
)
foreach ($path in $paths) {
    if (Test-Path -LiteralPath $path) {
        $item = Get-Item -LiteralPath $path
        $summary.files += [pscustomobject]@{
            path = $path
            bytes = $item.Length
            sha256 = Get-Hash $path
            last_write_utc = $item.LastWriteTimeUtc.ToString('o')
        }
    }
}
$hostsPath = Join-Path $env:SystemRoot 'System32\drivers\etc\hosts'
if (Test-Path -LiteralPath $hostsPath) {
    $summary.files += [pscustomobject]@{
        path = $hostsPath
        bytes = (Get-Item -LiteralPath $hostsPath).Length
        sha256 = (Get-FileHash -LiteralPath $hostsPath -Algorithm SHA256).Hash
        managed_block_present = ((Get-Content -LiteralPath $hostsPath -Raw) -match 'devstack')
    }
}

# Copy state.db for an integrity check; never open the live database in place.
$stateDb = Join-Path $InstallHome 'state.db'
if (Test-Path -LiteralPath $stateDb) {
    $copy = Join-Path $env:TEMP ("devstack-e2e-statecopy-" + $stamp + ".db")
    Copy-WithShare -Source $stateDb -Destination $copy
    try {
        $sqlite = Get-Command sqlite3 -ErrorAction SilentlyContinue
        if ($null -ne $sqlite) {
            $integrity = (& $sqlite.Source $copy 'PRAGMA integrity_check;' 2>&1 | Out-String).Trim()
            Write-EvidenceText 'state-db-integrity.txt' $integrity
            $summary.warnings += "state.db integrity: $integrity"
        } else {
            $summary.warnings += 'sqlite3 not installed on Windows; state.db integrity check skipped'
        }
    } finally {
        Remove-Item -LiteralPath $copy -Force -ErrorAction SilentlyContinue
    }
}

$summary | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $EvidenceDir 'read-only-summary.json') -Encoding utf8
Write-Host "read-only-check: evidence $EvidenceDir"

if (-not $SkipSecretScan) {
    $scanner = Join-Path $PSScriptRoot 'secret-scan.ps1'
    if (Test-Path -LiteralPath $scanner) {
        & $scanner -Path $EvidenceDir
        if ($LASTEXITCODE -ne 0) {
            throw 'read-only-check: secret scanner found redacted hits'
        }
    }
}
