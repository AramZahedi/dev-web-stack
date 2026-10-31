# Cycle 7 phpMyAdmin Playwright bridge.
#
# The disposable daemon/phpMyAdmin run in the Ubuntu WSL distro. This Windows
# script polls the WSL home for phase requests, runs the Playwright login matrix
# against the forwarded loopback port, writes metadata-only evidence, and
# acknowledges each phase. The password is read from a 0600 temp file and is
# never placed in argv, evidence, or console output.
param(
    [Parameter(Mandatory = $true)][string]$RunKey,
    [Parameter(Mandatory = $true)][string]$WslHome,
    [Parameter(Mandatory = $true)][string]$EvidenceDir,
    [Parameter(Mandatory = $true)][string]$Repo,
    [int]$TimeoutMinutes = 30
)

$ErrorActionPreference = 'Stop'
New-Item -ItemType Directory -Force -Path $EvidenceDir | Out-Null
$node = Join-Path $Repo 'test\e2e\database-management\linux\pma-login.js'
$env:NODE_PATH = Join-Path $Repo 'ui\node_modules'
$deadline = (Get-Date).AddMinutes($TimeoutMinutes)
$phases = 0

function Invoke-Wsl([string]$Command) {
    return (& wsl.exe -d Ubuntu -u root -- bash -c $Command 2>&1)
}

function Test-WslFile([string]$Path) {
    & wsl.exe -d Ubuntu -u root -- test -f $Path 2>$null
    return ($LASTEXITCODE -eq 0)
}

try {
    while ((Get-Date) -lt $deadline) {
        if (Test-WslFile "$WslHome/ui-finished") {
            break
        }
        if (-not (Test-WslFile "$WslHome/ui-request")) {
            Start-Sleep -Seconds 1
            continue
        }

        $requestJson = (Invoke-Wsl "cat '$WslHome/ui-request.json'") -join "`n"
        $request = $requestJson | ConvertFrom-Json
        $phase = [string]$request.phase
        $expect = [string]$request.expect
        $outFile = Join-Path $EvidenceDir "pma-$phase.json"

        $env:PMA_URL = [string]$request.url
        $env:PMA_USER = 'devstack'
        $env:PMA_PASS_FILE = [string]$request.pass_file
        $env:PMA_OUT = $EvidenceDir
        $env:PMA_PHASE = $phase
        $env:PMA_EXPECT = $expect

        try {
            & node $node 2>&1 | Out-File -FilePath $outFile -Encoding utf8
            if ($LASTEXITCODE -ne 0) {
                @{ phase = $phase; expect = $expect; error = "node exited $LASTEXITCODE" } |
                    ConvertTo-Json | Out-File -FilePath $outFile -Encoding utf8
            }
        }
        catch {
            @{ phase = $phase; expect = $expect; error = $_.Exception.Message } |
                ConvertTo-Json | Out-File -FilePath $outFile -Encoding utf8
        }
        finally {
            foreach ($name in @('PMA_URL', 'PMA_USER', 'PMA_PASS_FILE', 'PMA_OUT', 'PMA_PHASE', 'PMA_EXPECT')) {
                Remove-Item "Env:$name" -ErrorAction SilentlyContinue
            }
        }

        & wsl.exe -d Ubuntu -u root -- touch "$WslHome/ui-done" 2>$null
        # Wait for the scenario to consume this request before polling again;
        # otherwise the same request is executed twice.
        for ($i = 0; $i -lt 100; $i++) {
            if (-not (Test-WslFile "$WslHome/ui-request")) { break }
            Start-Sleep -Milliseconds 200
        }
        $phases++
    }
}
finally {
    Remove-Item Env:NODE_PATH -ErrorAction SilentlyContinue
}

Write-Output "cycle7-pma-ui: phases=$phases"
if ($phases -eq 0) {
    exit 1
}
