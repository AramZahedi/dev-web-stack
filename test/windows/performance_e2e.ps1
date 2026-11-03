# End-to-end cross-platform performance tuning matrix.
# Applies Balanced, Custom, resets to the pre-devstack baseline, then restores
# Throughput as the default profile.
$ErrorActionPreference = 'Stop'
$base = if ($env:DEVSTACK_E2E_UI_URL) { $env:DEVSTACK_E2E_UI_URL.TrimEnd('/') } else { 'http://127.0.0.1:7070' }
$web = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$session = Invoke-RestMethod -Uri "$base/api/v1/session" -WebSession $web -Method Get
$headers = @{ 'X-CSRF-Token' = $session.csrf_token }

function Put-Json([string]$uri, $body) {
    Invoke-RestMethod -Uri $uri -Method Put -WebSession $web -Headers $headers -ContentType 'application/json' -Body ($body | ConvertTo-Json -Depth 40)
}
function Post-Json([string]$uri, $body) {
    Invoke-RestMethod -Uri $uri -Method Post -WebSession $web -Headers $headers -ContentType 'application/json' -Body ($body | ConvertTo-Json -Depth 40)
}
function Assert-Equal($actual, $expected, [string]$label) {
    if ("$actual" -ne "$expected") { throw "$label = '$actual', want '$expected'" }
}
function Assert-Contains([string]$text, [string]$needle, [string]$label) {
    if (-not $text.Contains($needle)) { throw "$label does not contain '$needle': $text" }
}
function Windows-PowerPlan { (powercfg /getactivescheme) -replace '.*GUID:\s*([0-9a-f-]+).*', '$1' }
function Windows-TcpPorts {
    $out = netsh int ipv4 show dynamicport tcp
    $start = [regex]::Match($out, 'Start Port\s*:\s*(\d+)').Groups[1].Value
    $num = [regex]::Match($out, 'Number of Ports\s*:\s*(\d+)').Groups[1].Value
    return "$start/$num"
}
function Windows-RegValue([string]$path, [string]$name) {
    $out = reg query $path /v $name 2>$null
    if ($LASTEXITCODE -ne 0) { return '' }
    $line = $out | Where-Object { $_ -match 'REG_DWORD' } | Select-Object -Last 1
    if (-not $line) { return '' }
    return (($line -split '\s+') | Where-Object { $_ })[-1]
}
function WSL-Sysctl([string]$key) {
    return ((wsl -d devstack -u root -- sysctl -n $key) -join '').Trim()
}

Write-Host '== Balanced apply =='
$initialStatus = Invoke-RestMethod -Uri "$base/api/v1/performance" -WebSession $web -Method Get
$totalMB = [math]::Floor($initialStatus.resources.memory_total_bytes / 1MB)
$balancedMemory = [math]::Floor($totalMB * 50 / 100)
$null = Put-Json "$base/api/v1/performance/profile" @{ profile = 'balanced' }
$balanced = Post-Json "$base/api/v1/performance/apply" @{ confirm = $true; host_tuning = $true }
Assert-Equal $balanced.host_tuning $true 'balanced host_tuning'
Assert-Equal (Windows-PowerPlan) '381b4222-f694-41f0-9685-ff5bb260df2e' 'balanced power plan'
Assert-Equal (Windows-TcpPorts) '49152/16384' 'balanced dynamic ports'
$wslconfig = Get-Content "$env:USERPROFILE\.wslconfig" -Raw
Assert-Contains $wslconfig "memory=$($balancedMemory)MB" 'balanced .wslconfig memory'
Assert-Equal (Windows-RegValue 'HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Multimedia\SystemProfile' 'NetworkThrottlingIndex') '0xa' 'balanced throttling'
Write-Host 'balanced OK'

Write-Host '== Custom apply =='
$status = Invoke-RestMethod -Uri "$base/api/v1/performance" -WebSession $web -Method Get
$custom = $status.values
$custom.profile = 'custom'
$custom.nginx.workers = 8
$custom.windows.power_plan = 'high_performance'
$custom.windows.dynamic_port_min = 40000
$custom.windows.dynamic_port_max = 50000
$custom.windows.tcp_timed_wait_delay_seconds = 30
$custom.windows.network_throttling_index = 4294967295
$custom.windows.system_responsiveness = 0
$custom.windows.disable_last_access = $true
$custom.windows.disable_8dot3 = $true
$custom.windows.defender_exclusions = $true
$custom.windows.wsl_memory_mb = 8192
$custom.windows.wsl_processors = 4
$custom.windows.wsl_swap_mb = 0
$null = Put-Json "$base/api/v1/performance/profile" @{ profile = 'custom'; custom = $custom }
$customResult = Post-Json "$base/api/v1/performance/apply" @{ confirm = $true; host_tuning = $true }
Assert-Equal $customResult.profile 'custom' 'custom profile'
Assert-Equal (Windows-PowerPlan) '8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c' 'custom power plan'
Assert-Equal (Windows-TcpPorts) '40000/10001' 'custom dynamic ports'
$wslconfig = Get-Content "$env:USERPROFILE\.wslconfig" -Raw
Assert-Contains $wslconfig 'memory=8192MB' 'custom .wslconfig memory'
Assert-Contains $wslconfig 'processors=4' 'custom .wslconfig processors'
Assert-Contains $wslconfig 'swap=0MB' 'custom .wslconfig swap'
Write-Host 'custom OK'

Write-Host '== Reset =='
$reset = Post-Json "$base/api/v1/performance/reset" @{ confirm = $true }
Assert-Equal $reset.reset $true 'reset'
Assert-Equal (Windows-PowerPlan) '381b4222-f694-41f0-9685-ff5bb260df2e' 'reset power plan'
Assert-Equal (Windows-TcpPorts) '49152/16384' 'reset dynamic ports'
Assert-Equal (WSL-Sysctl 'net.core.somaxconn') '4096' 'reset WSL somaxconn'
Assert-Equal (WSL-Sysctl 'net.core.netdev_max_backlog') '1000' 'reset WSL netdev backlog'
$wslPortRange = ((wsl -d devstack -u root -- sysctl -n net.ipv4.ip_local_port_range) -join ' ').Trim()
$wslPortRange = (($wslPortRange -split '\s+') -join ' ')
Assert-Equal $wslPortRange '32768 60999' 'reset WSL dynamic ports'
$leftFiles = (wsl -d devstack -u root -- sh -c 'test ! -e /etc/sysctl.d/99-devstack-performance.conf && test ! -e /etc/security/limits.d/99-devstack-performance.conf && echo clean') -join ''
Assert-Equal $leftFiles 'clean' 'reset WSL files'
if (Test-Path "$env:USERPROFILE\.wslconfig") {
    $left = Get-Content "$env:USERPROFILE\.wslconfig" -Raw
    if ($left -match 'memory=\d+MB') { throw "reset left managed .wslconfig: $left" }
}
Write-Host 'reset OK'

Write-Host '== Restore Throughput default =='
$null = Put-Json "$base/api/v1/performance/profile" @{ profile = 'throughput' }
$final = Post-Json "$base/api/v1/performance/apply" @{ confirm = $true; host_tuning = $true }
Assert-Equal $final.profile 'throughput' 'final profile'
Assert-Equal (Windows-PowerPlan) '8c5e7fda-e8bf-4a96-9a85-a6e23a8c635c' 'final power plan'
Write-Host 'throughput restored'
Write-Host 'TUNING_E2E_PASS'
