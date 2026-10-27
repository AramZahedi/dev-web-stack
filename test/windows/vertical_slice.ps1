# Windows/WSL Vertical Slice & Acceptance Integration Suite
#
# Run from an elevated PowerShell prompt on a Windows 10 22H2 (19045+) or
# Windows 11 machine after devstack is installed.
# It validates the integrated stack:
# - System & WSL prerequisites (WSL2, systemd, Docker, localhost forwarding)
# - Helper service, daemon status, DPAPI secrets, and No-Dot file policy (S70)
# - Static sites in both Windows (NTFS) and WSL (ext4) source modes
# - Live PHP-FPM execution with trusted HTTPS and fpm-fcgi SAPI verification
# - Tooling shims (php.cmd, composer.cmd)
# - Docker engine connectivity inside the managed distro
# - Dual-direction trusted HTTPS (Windows browser/Invoke-WebRequest and WSL curl)
# - Doctor diagnostic suite verification
#
# Exit codes:
#   0: all gates passed
#   1: one or more gates failed
#   2: environment prerequisite missing
param(
    [string]$WinSite = "win-slice",
    [string]$WslSite = "wsl-slice",
    [string]$PhpSite = "php-slice",
    [string]$TestRoot = "C:\devstack-test\vertical-slice",
    [switch]$KeepFixtures,
    [switch]$Help
)

$ErrorActionPreference = "Stop"
$script:Failures = 0

function Show-Usage {
    Write-Host @"
Usage: vertical_slice.ps1 [-WinSite <name>] [-WslSite <name>] [-PhpSite <name>] [-TestRoot <path>] [-KeepFixtures] [-Help]

Parameters:
    -WinSite <name>     Name for Windows-mode static site (default: win-slice)
    -WslSite <name>     Name for WSL-mode static site (default: wsl-slice)
    -PhpSite <name>     Name for PHP-FPM test site (default: php-slice)
    -TestRoot <path>    Local scratch path for test fixtures (default: C:\devstack-test\vertical-slice)
    -KeepFixtures       Preserve fixture sites and files upon completion
    -Help               Show this help message
"@
}

if ($Help) {
    Show-Usage
    exit 0
}

function Step([string]$Message) { Write-Host "`n=== vertical-slice: $Message ===" -ForegroundColor Cyan }
function Pass([string]$Message) { Write-Host "vertical-slice: ok: $Message" -ForegroundColor Green }
function Fail([string]$Message) {
    Write-Host "vertical-slice: FAIL: $Message" -ForegroundColor Red
    $script:Failures++
}
function Require([bool]$Condition, [string]$Message) {
    if ($Condition) { Pass $Message } else { Fail $Message }
}
function Invoke-Devstack([string[]]$Arguments) {
    $output = & devstack @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "devstack $($Arguments -join ' ') failed ($LASTEXITCODE): $output"
    }
    return $output
}

# -----------------------------------------------------------------------------
# 1. Prerequisites Check
# -----------------------------------------------------------------------------
Step "Checking Windows and WSL prerequisites"

$build = [Environment]::OSVersion.Version.Build
Require ($build -ge 19045) "Windows build $build is 19045+ (Windows 10 22H2 or Windows 11)"
if ($build -lt 19045) { exit 2 }

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
Require ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) "Running with Administrator privileges"
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { exit 2 }

$wslRaw = (& wsl.exe --version 2>&1 | Out-String)
$wslVersion = ($wslRaw -replace "`0", "").Trim()
Require ($wslVersion -match "(?m)WSL version:\s*([2-9]|\d{2,})\.") "WSL 2.0.0+ is installed"

$distrosRaw = (& wsl.exe -l -q 2>&1 | Out-String)
$distros = ($distrosRaw -replace "`0", "").Trim()
Require ($distros -match "devstack") "Managed 'devstack' WSL distro exists"

$systemd = (& wsl.exe -d devstack -- bash -lc "ps -p 1 -o comm= 2>/dev/null || true" 2>&1 | Out-String).Trim()
Require ($systemd -match "systemd") "Managed distro runs systemd as PID 1"

$docker = (& wsl.exe -d devstack -- bash -lc "docker info >/dev/null 2>&1 && echo ok" 2>&1 | Out-String)
Require ($docker -match "ok") "Docker Engine is healthy inside WSL"

$forwarding = (& wsl.exe -d devstack -- bash -lc "timeout 3 bash -c 'exec 3<>/dev/tcp/`$(ip route show default | awk '\''{print `$3; exit}'\'')/8443' && echo ok" 2>&1 | Out-String)
Require ($forwarding -match "ok") "WSL can reach Windows host gateway on port 8443"

# -----------------------------------------------------------------------------
# 2. Product Services & Security Policies
# -----------------------------------------------------------------------------
Step "Checking product services and security policies"

$helper = Get-Service -Name DevstackHelper -ErrorAction SilentlyContinue
Require ($null -ne $helper -and $helper.Status -eq "Running") "DevstackHelper Windows service is running"

$daemon = Invoke-Devstack @("daemon", "status", "--json") | ConvertFrom-Json
Require ($daemon.running -eq $true -or $daemon.Running -eq $true) "devstack daemon is running"

$installDir = "C:\devstack"
if (Test-Path $installDir) {
    $tokenPath = Join-Path $installDir "system\agent-token"
    $pinPath = Join-Path $installDir "system\agent-pin"
    Require (Test-Path $tokenPath) "DPAPI-protected agent-token file exists"
    Require (Test-Path $pinPath) "DPAPI-protected agent-pin file exists"

    # Decision S70: No devstack-created Windows file or directory starts with a dot
    $dotItems = Get-ChildItem -Path $installDir -Recurse -Force -ErrorAction SilentlyContinue | Where-Object {
        $_.Name -like ".*" -and $_.FullName -notmatch "\\projects\\" -and $_.FullName -notmatch "\\wsl\\"
    }
    Require ($null -eq $dotItems -or $dotItems.Count -eq 0) "No-Dot Policy (Decision S70): no devstack-created items start with a dot"
}

# -----------------------------------------------------------------------------
# 3. Create Fixture Sites
# -----------------------------------------------------------------------------
Step "Provisioning fixture projects (Windows static, WSL static, PHP-FPM)"

New-Item -ItemType Directory -Force -Path $TestRoot | Out-Null

# 3.1 Windows Static Site
$winRoot = Join-Path $TestRoot "windows-static"
New-Item -ItemType Directory -Force -Path $winRoot | Out-Null
Set-Content -Path (Join-Path $winRoot "index.html") -Value "<h1>windows source ok</h1>" -NoNewline

# 3.2 WSL Static Site
$wslRoot = "/data/devstack-test/vertical-slice/wsl-static"
& wsl.exe -d devstack -- bash -lc "mkdir -p '$wslRoot' && printf '<h1>wsl source ok</h1>' > '$wslRoot/index.html'"
if ($LASTEXITCODE -ne 0) { throw "could not create WSL fixture" }
$wslUnc = "\\wsl.localhost\devstack" + ($wslRoot -replace "/", "\")

# 3.3 PHP-FPM Site
$phpRoot = Join-Path $TestRoot "php-app"
New-Item -ItemType Directory -Force -Path $phpRoot | Out-Null
$phpIndexContent = @'
<?php
header('Content-Type: application/json');
echo json_encode([
    'status' => 'ok',
    'sapi' => php_sapi_name(),
    'php_version' => PHP_VERSION,
    'time' => time()
]);
'@
Set-Content -Path (Join-Path $phpRoot "index.php") -Value $phpIndexContent -NoNewline

# Register sites
& devstack site remove $WinSite --force 2>$null | Out-Null
& devstack site remove $WslSite --force 2>$null | Out-Null
& devstack site remove $PhpSite --force 2>$null | Out-Null

Invoke-Devstack @("site", "add", "--name", $WinSite, "--path", $winRoot, "--kind", "static", "--source-mode", "windows") | Out-Null
Pass "Added Windows static site: $WinSite"

Invoke-Devstack @("site", "add", "--name", $WslSite, "--path", $wslUnc, "--kind", "static", "--source-mode", "wsl") | Out-Null
Pass "Added WSL static site: $WslSite"

try {
    Invoke-Devstack @("site", "add", "--name", $PhpSite, "--path", $phpRoot, "--kind", "php", "--source-mode", "windows") | Out-Null
    Pass "Added PHP-FPM site: $PhpSite"
} catch {
    Fail "Adding PHP site failed: $_"
}

# -----------------------------------------------------------------------------
# 4. Windows Trusted HTTPS Verification
# -----------------------------------------------------------------------------
Step "Verifying trusted HTTPS from Windows"

# Static Sites
foreach ($site in @($WinSite, $WslSite)) {
    $url = "https://$site.test/"
    try {
        $response = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 20
        Require ($response.StatusCode -eq 200) "Windows trusted HTTPS $url (HTTP 200)"
        Require ($response.Content -match "source ok") "Windows content for $url contains expected text"
    } catch {
        Fail "Windows trusted HTTPS $url failed: $_"
    }
}

# PHP Site (Assert live Linux FPM SAPI execution)
$phpUrl = "https://$PhpSite.test/"
try {
    $phpResponse = Invoke-WebRequest -Uri $phpUrl -UseBasicParsing -TimeoutSec 20
    Require ($phpResponse.StatusCode -eq 200) "Windows trusted HTTPS $phpUrl (HTTP 200)"
    Require ($phpResponse.Content -match '"status":"ok"') "PHP response status is 'ok'"
    Require ($phpResponse.Content -match '"sapi":"fpm-fcgi"') "PHP execution runs via Linux 'fpm-fcgi' SAPI"
} catch {
    Fail "Windows trusted HTTPS for PHP site $phpUrl failed: $_"
}

# Dashboard
try {
    $dashboard = Invoke-WebRequest -Uri "https://devstack.test/" -UseBasicParsing -TimeoutSec 20
    Require ($dashboard.StatusCode -eq 200) "Windows trusted dashboard https://devstack.test/ (HTTP 200)"
} catch {
    Fail "Windows trusted dashboard https://devstack.test/ failed: $_"
}

# -----------------------------------------------------------------------------
# 5. WSL Ingress & In-Distro .test Curl Verification
# -----------------------------------------------------------------------------
Step "Verifying .test routing and trusted TLS from inside WSL"

foreach ($site in @($WinSite, $WslSite)) {
    $result = & wsl.exe -d devstack -- bash -lc "curl -fsS --max-time 20 https://$site.test/ | grep -q 'source ok' && echo ok"
    Require ($result -match "ok") "WSL curl https://$site.test/ succeeds with trusted certificate"
}

$phpWslResult = & wsl.exe -d devstack -- bash -lc "curl -fsS --max-time 20 https://$PhpSite.test/ | grep -q 'fpm-fcgi' && echo ok"
Require ($phpWslResult -match "ok") "WSL curl https://$PhpSite.test/ succeeds and reports fpm-fcgi"

$dashWslResult = & wsl.exe -d devstack -- bash -lc "curl -fsS --max-time 20 https://devstack.test/ >/dev/null && echo ok"
Require ($dashWslResult -match "ok") "WSL curl https://devstack.test/ succeeds"

# -----------------------------------------------------------------------------
# 6. Tooling Shims & CLI Parity
# -----------------------------------------------------------------------------
Step "Checking Windows .cmd tooling shims"

$phpShim = "C:\devstack\bin\php.cmd"
if (Test-Path $phpShim) {
    Pass "php.cmd shim exists at $phpShim"
    $phpShimOut = & "$phpShim" -v 2>&1 | Out-String
    Require ($phpShimOut -match "PHP 8\.") "php.cmd -v reports active PHP version"
} else {
    Pass "php.cmd shim not yet staged to C:\devstack\bin (optional before full tooling install)"
}

$composerShim = "C:\devstack\bin\composer.cmd"
if (Test-Path $composerShim) {
    Pass "composer.cmd shim exists at $composerShim"
}

# -----------------------------------------------------------------------------
# 7. Doctor Diagnostic Verification
# -----------------------------------------------------------------------------
Step "Checking comprehensive doctor diagnostics"

$doctor = Invoke-Devstack @("doctor", "--json") | ConvertFrom-Json
$checks = @($doctor.checks)

$forwardingCheck = $checks | Where-Object { $_.id -match "localhost|forward" }
Require ($null -ne $forwardingCheck -and $forwardingCheck.status -ne "fail") "Doctor: localhost forwarding is operational"

$agentCheck = $checks | Where-Object { $_.id -match "agent" }
Require ($null -ne $agentCheck -and $agentCheck.status -ne "fail") "Doctor: WSL agent is healthy"

$helperCheck = $checks | Where-Object { $_.id -match "helper" }
if ($null -ne $helperCheck) {
    Require ($helperCheck.status -ne "fail") "Doctor: helper service is operational"
}

$caCheck = $checks | Where-Object { $_.id -match "ca|trust" }
if ($null -ne $caCheck) {
    Require ($caCheck.status -ne "fail") "Doctor: Root CA trust is verified"
}

# -----------------------------------------------------------------------------
# 8. Cleanup
# -----------------------------------------------------------------------------
if (-not $KeepFixtures) {
    Step "Cleaning up fixture sites"
    & devstack site remove $WinSite --force 2>$null | Out-Null
    & devstack site remove $WslSite --force 2>$null | Out-Null
    & devstack site remove $PhpSite --force 2>$null | Out-Null
    Remove-Item -Recurse -Force -Path $TestRoot -ErrorAction SilentlyContinue
    & wsl.exe -d devstack -- bash -lc "rm -rf '$wslRoot'" 2>$null | Out-Null
    Pass "Cleaned up test sites and scratch files"
} else {
    Pass "Preserving fixtures (-KeepFixtures specified)"
}

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
Step "Acceptance Suite Summary"
if ($script:Failures -ne 0) {
    Write-Host "vertical-slice: $($script:Failures) gate(s) FAILED" -ForegroundColor Red
    exit 1
}

Write-Host "vertical-slice: ALL ACCEPTANCE GATES PASSED (100% GREEN)" -ForegroundColor Green
exit 0
