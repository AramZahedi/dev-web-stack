# Read-only CLI security probes for the database-management E2E run.
#
# Every probe is a documented failure that must happen before any API call or
# mutation. Destructive commands are invoked only with a missing target and
# without --yes, so a broken confirmation gate would still hit NOT_FOUND rather
# than change real state.
[CmdletBinding()]
param(
    [string]$InstallHome = 'C:\Users\Aram\.devstack',
    [string]$DevstackExe = '',
    [string]$EvidenceDir = ''
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..\..')).Path
if ([string]::IsNullOrWhiteSpace($DevstackExe)) {
    $DevstackExe = Join-Path $InstallHome 'bin\devstack.exe'
}
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
    $EvidenceDir = Join-Path $repo "dist\e2e\database-management\evidence\$stamp\WIN-REAL-cli"
}
New-Item -ItemType Directory -Force -Path $EvidenceDir | Out-Null
if (-not (Test-Path -LiteralPath $DevstackExe)) {
    throw "devstack.exe not found: $DevstackExe"
}

function Invoke-CliProbe {
    param([string]$Name, [string[]]$Arguments, [switch]$OpenStdin)
    $stdoutPath = Join-Path $EvidenceDir "$Name.stdout.txt"
    $stderrPath = Join-Path $EvidenceDir "$Name.stderr.txt"
    $sw = [Diagnostics.Stopwatch]::StartNew()
    $exitCode = 0
    $stdinOpen = $false
    if ($OpenStdin) {
        $psi = [Diagnostics.ProcessStartInfo]::new()
        $psi.FileName = $DevstackExe
        $psi.UseShellExecute = $false
        $psi.RedirectStandardInput = $true
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        foreach ($arg in @('--home', $InstallHome) + $Arguments) { [void]$psi.ArgumentList.Add($arg) }
        $process = [Diagnostics.Process]::Start($psi)
        # Leave stdin open and unwritten. A command that reads stdin would block.
        $exited = $process.WaitForExit(5000)
        if (-not $exited) {
            $process.Kill($true)
            $process.WaitForExit()
            $stdinOpen = $true
        }
        $stdout = $process.StandardOutput.ReadToEnd()
        $stderr = $process.StandardError.ReadToEnd()
        $exitCode = $process.ExitCode
        Set-Content -LiteralPath $stdoutPath -Value $stdout -Encoding utf8
        Set-Content -LiteralPath $stderrPath -Value $stderr -Encoding utf8
    } else {
        $null | & $DevstackExe --home $InstallHome @Arguments 1> $stdoutPath 2> $stderrPath
        $exitCode = $LASTEXITCODE
        $stdout = [string](Get-Content -LiteralPath $stdoutPath -Raw -ErrorAction SilentlyContinue)
        $stderr = [string](Get-Content -LiteralPath $stderrPath -Raw -ErrorAction SilentlyContinue)
    }
    $sw.Stop()
    $code = ''
    $jsonStart = $stderr.IndexOf('{')
    if ($jsonStart -ge 0) {
        try { $code = [string](($stderr.Substring($jsonStart) | ConvertFrom-Json).code) } catch { }
    }
    return [pscustomobject]@{
        name = $Name
        exit = $exitCode
        ms = $sw.ElapsedMilliseconds
        stdin_open = $OpenStdin
        stdin_blocked = $stdinOpen
        code = $code
        stdout = ($stdout -replace '\s+', ' ').Trim()
        stderr = ($stderr -replace '\s+', ' ').Trim()
    }
}

$fixture = Join-Path $repo 'test\e2e\database-management\fixtures\sql\seed.sql'
$results = [System.Collections.Generic.List[object]]::new()
$results.Add((Invoke-CliProbe 'password-set' @('--json', 'db', 'user', 'password', '--service', 'mysql', '--user-id', 'usr_probe', '--set')))
$results.Add((Invoke-CliProbe 'password-set-open-stdin' @('--json', 'db', 'user', 'password', '--service', 'mysql', '--user-id', 'usr_probe', '--set') -OpenStdin))
$results.Add((Invoke-CliProbe 'rename-mysql' @('--json', 'db', 'rename', '--service', 'mysql', '--name', 'devstack', '--to', 'e2e_probe')))
$results.Add((Invoke-CliProbe 'rename-mariadb' @('--json', 'db', 'rename', '--service', 'mariadb', '--name', 'devstack', '--to', 'e2e_probe')))
$results.Add((Invoke-CliProbe 'site-add-password-stdin' @('--json', 'site', 'add', '--name', 'e2e_probe', '--path', $repo, '--database-service', 'mysql', '--database', 'devstack', '--database-user-new', 'e2e_probe', '--database-user-password-stdin')))
$results.Add((Invoke-CliProbe 'site-add-password-stdin-open' @('--json', 'site', 'add', '--name', 'e2e_probe', '--path', $repo, '--database-service', 'mysql', '--database', 'devstack', '--database-user-new', 'e2e_probe', '--database-user-password-stdin') -OpenStdin))
$results.Add((Invoke-CliProbe 'drop-missing-no-yes' @('--json', 'db', 'drop', '--service', 'mysql', '--name', 'e2e_probe_missing')))
$results.Add((Invoke-CliProbe 'drop-missing-yes-false' @('--json', 'db', 'drop', '--service', 'mysql', '--name', 'e2e_probe_missing', '--yes=false')))
$results.Add((Invoke-CliProbe 'user-delete-missing-no-yes' @('--json', 'db', 'user', 'delete', '--service', 'mysql', '--user-id', 'usr_probe_missing')))
$results.Add((Invoke-CliProbe 'backup-restore-overwrite-missing-no-yes' @('--json', 'db', 'backup', 'restore', '--service', 'mysql', '--backup-id', 'bkp_probe_missing', '--target', 'e2e_probe_missing', '--overwrite')))
$results.Add((Invoke-CliProbe 'backup-delete-missing-no-yes' @('--json', 'db', 'backup', 'delete', '--service', 'mysql', '--backup-id', 'bkp_probe_missing')))
$results.Add((Invoke-CliProbe 'import-replace-missing-no-yes' @('--json', 'db', 'import', '--service', 'mysql', '--database', 'e2e_probe_missing', '--file', $fixture, '--replace')))
$results.Add((Invoke-CliProbe 'unlink-missing-no-yes' @('--json', 'db', 'unlink', '--site', 'e2e_probe_missing', '--link-id', 'lnk_probe_missing')))
$results.Add((Invoke-CliProbe 'site-remove-missing-no-yes' @('--json', 'site', 'remove', 'e2e_probe_missing')))
$results.Add((Invoke-CliProbe 'user-password-rotate-missing-no-yes' @('--json', 'db', 'user', 'password', '--service', 'mysql', '--user-id', 'usr_probe_missing', '--rotate')))
$results.Add((Invoke-CliProbe 'redis-password-rotate-no-yes' @('--json', 'db', 'password', '--service', 'redis')))

# Semantic state check: no probe may create a database, user, backup, or site.
$state = [ordered]@{}
foreach ($item in @(
    @{ key = 'databases'; args = @('--json', 'db', 'list', '--service', 'mysql') },
    @{ key = 'users'; args = @('--json', 'db', 'user', 'list', '--service', 'mysql') },
    @{ key = 'backups'; args = @('--json', 'db', 'backup', 'list', '--service', 'mysql') },
    @{ key = 'sites'; args = @('--json', 'site', 'list') }
)) {
    $raw = & $DevstackExe --home $InstallHome @($item.args) 2>&1 | Out-String
    Set-Content -LiteralPath (Join-Path $EvidenceDir ("post-" + $item.key + ".txt")) -Value $raw -Encoding utf8
    try {
        $parsed = $raw | ConvertFrom-Json
        $state[$item.key] = switch ($item.key) {
            'databases' { @($parsed.databases).Count }
            'users' { @($parsed.users).Count }
            'backups' { @($parsed.backups).Count }
            'sites' { @($parsed).Count }
        }
    } catch {
        $state[$item.key] = -1
    }
}

$summary = [ordered]@{
    generated_at = (Get-Date).ToUniversalTime().ToString('o')
    home = $InstallHome
    devstack_exe = $DevstackExe
    probes = $results
    state = $state
}
$summary | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $EvidenceDir 'cli-probes.json') -Encoding utf8
$results | Format-Table name,exit,ms,code,stdin_blocked -AutoSize | Out-String -Width 220 | Set-Content -LiteralPath (Join-Path $EvidenceDir 'cli-probes.txt') -Encoding utf8
Write-Host "cli-probes: $($results.Count) probes -> $EvidenceDir"

$scanner = Join-Path $PSScriptRoot 'secret-scan.ps1'
if (Test-Path -LiteralPath $scanner) {
    & $scanner -Path $EvidenceDir
    if ($LASTEXITCODE -ne 0) { throw 'cli-probes: secret scanner found redacted hits' }
}
