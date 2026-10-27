# Windows/WSL Integration 1 vertical slice.
#
# Run from an elevated PowerShell prompt on a Windows 10 22H2 (19045+) or
# Windows 11 machine after the Web stack is installed and `devstack init` has
# completed. It uses only the installed product and temporary test projects.
#
# Exit codes: 0 all gates passed; 1 a gate failed; 2 environment prerequisite
# is missing.
param(
    [string]$WinSite = "win-slice",
    [string]$WslSite = "wsl-slice",
    [string]$TestRoot = "C:\devstack-test\vertical-slice"
)

$ErrorActionPreference = "Stop"
$script:Failures = 0

function Step([string]$Message) { Write-Host "vertical-slice: $Message" }
function Pass([string]$Message) { Write-Host "vertical-slice: ok: $Message" }
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

Step "checking Windows/WSL prerequisites"
$build = [Environment]::OSVersion.Version.Build
Require ($build -ge 19045) "Windows build $build is 19045+ (Windows 10 22H2/11)"
if ($build -lt 19045) { exit 2 }

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($identity)
Require ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) "running elevated"

$wslVersion = (& wsl.exe --version 2>&1 | Out-String)
Require ($wslVersion -match "2\.") "WSL 2.0.0+ is installed"
$distros = (& wsl.exe -l -q 2>&1 | Out-String)
Require ($distros -match "devstack") "managed devstack distro exists"
$systemd = (& wsl.exe -d devstack -- bash -lc "ps -p 1 -o comm= 2>/dev/null || true" 2>&1 | Out-String).Trim()
Require ($systemd -match "systemd") "managed distro runs systemd"
$docker = (& wsl.exe -d devstack -- bash -lc "docker info >/dev/null 2>&1 && echo ok" 2>&1 | Out-String)
Require ($docker -match "ok") "Docker Engine is healthy inside WSL"
$forwarding = (& wsl.exe -d devstack -- bash -lc "timeout 3 bash -c 'exec 3<>/dev/tcp/`$(ip route show default | awk '\''{print `$3; exit}'\'')/8443' && echo ok" 2>&1 | Out-String)
Require ($forwarding -match "ok") "WSL can reach the Windows daemon proxy through the host gateway"

Step "checking product services"
$helper = Get-Service -Name devstack-helper -ErrorAction SilentlyContinue
Require ($null -ne $helper -and $helper.Status -eq "Running") "devstack-helper service is running"
$daemon = Invoke-Devstack @("daemon", "status", "--json") | ConvertFrom-Json
Require ($daemon.running -eq $true -or $daemon.Running -eq $true) "devstack daemon is running"

Step "creating fixture projects"
New-Item -ItemType Directory -Force -Path $TestRoot | Out-Null
$winRoot = Join-Path $TestRoot "windows-static"
New-Item -ItemType Directory -Force -Path $winRoot | Out-Null
Set-Content -Path (Join-Path $winRoot "index.html") -Value "<h1>windows source ok</h1>" -NoNewline
$wslRoot = "/data/devstack-test/vertical-slice/wsl-static"
& wsl.exe -d devstack -- bash -lc "mkdir -p '$wslRoot' && printf '<h1>wsl source ok</h1>' > '$wslRoot/index.html'"
if ($LASTEXITCODE -ne 0) { throw "could not create WSL fixture" }
$wslUnc = "\\wsl.localhost\devstack" + ($wslRoot -replace "/", "\")

Step "creating one Windows-mode and one WSL-mode static site"
& devstack site remove $WinSite --force 2>$null | Out-Null
& devstack site remove $WslSite --force 2>$null | Out-Null
Invoke-Devstack @("site", "add", "--name", $WinSite, "--path", $winRoot, "--kind", "static", "--source-mode", "windows") | Out-Null
Invoke-Devstack @("site", "add", "--name", $WslSite, "--path", $wslUnc, "--kind", "static", "--source-mode", "wsl") | Out-Null

Step "fetching trusted HTTPS from Windows"
foreach ($site in @($WinSite, $WslSite)) {
    $url = "https://$site.test/"
    try {
        $response = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 20
        Require ($response.StatusCode -eq 200) "Windows trusted HTTPS $url"
        Require ($response.Content -match "source ok") "Windows content $url"
    } catch {
        Fail "Windows trusted HTTPS $url : $_"
    }
}
try {
    $dashboard = Invoke-WebRequest -Uri "https://devstack.test/" -UseBasicParsing -TimeoutSec 20
    Require ($dashboard.StatusCode -eq 200) "Windows trusted dashboard https://devstack.test"
} catch {
    Fail "Windows trusted dashboard https://devstack.test : $_"
}

Step "fetching .test sites from inside WSL"
foreach ($site in @($WinSite, $WslSite)) {
    $result = & wsl.exe -d devstack -- bash -lc "curl -fsS --max-time 20 https://$site.test/ | grep -q 'source ok' && echo ok"
    Require ($result -match "ok") "WSL curl https://$site.test/"
}
$dash = & wsl.exe -d devstack -- bash -lc "curl -fsS --max-time 20 https://devstack.test/ >/dev/null && echo ok"
Require ($dash -match "ok") "WSL curl https://devstack.test/"

Step "checking doctor"
$doctor = Invoke-Devstack @("doctor", "--json") | ConvertFrom-Json
$checks = @($doctor.checks)
$forwardingCheck = $checks | Where-Object { $_.id -match "localhost|forward" }
Require ($null -ne $forwardingCheck -and $forwardingCheck.status -ne "fail") "doctor reports localhost forwarding"
$agentCheck = $checks | Where-Object { $_.id -match "agent" }
Require ($null -ne $agentCheck -and $agentCheck.status -ne "fail") "doctor reports the WSL agent healthy"

if ($script:Failures -ne 0) {
    Write-Host "vertical-slice: $($script:Failures) gate(s) failed" -ForegroundColor Red
    exit 1
}
Write-Host "vertical-slice: all gates passed"
exit 0
