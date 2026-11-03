# End-to-end serving matrix for Windows Docker nginx/Apache.
# Requires an installed devstack with a running PHP site.
$ErrorActionPreference = 'Stop'
$site = if ($env:DEVSTACK_E2E_SITE) { $env:DEVSTACK_E2E_SITE } else { 'laravel-new.test' }
$staticSite = $env:DEVSTACK_E2E_STATIC
$siteName = $site -replace '\.test$', ''
$siteInfo = devstack site show $siteName --json | ConvertFrom-Json
$siteId = $siteInfo.id
$projectName = Split-Path $siteInfo.backend_path -Leaf
$docRel = ("$($siteInfo.document_root)" -replace '^[\\/]+', '' -replace '[\\/]+$', '')
$containerRoot = if ($docRel) { "/sites/$projectName/$docRel" } else { "/sites/$projectName" }

function Assert-Equal($actual, $expected, [string]$label) {
    if ("$actual" -ne "$expected") { throw "$label = '$actual', want '$expected'" }
}
function Vhost-Mode { ((devstack vhost status --json | ConvertFrom-Json).mode) }
function Vhost-OK { ((devstack vhost status --json | ConvertFrom-Json).config_ok) }
function HTTP-Headers([string]$url) {
    $raw = curl.exe -k -sS -D - -o NUL --max-time 30 $url 2>&1
    return ($raw -join "`n")
}
function Assert-HTTP([string]$url, [string]$server) {
    $headers = HTTP-Headers $url
    if ($headers -notmatch 'HTTP/\d\.\d 200') { throw "$url did not return 200:`n$headers" }
    if ($headers -notmatch "Server: $server") { throw "$url did not come from ${server}:`n$headers" }
}
function Assert-Status([string]$url) {
    $headers = HTTP-Headers $url
    if ($headers -notmatch 'HTTP/\d\.\d 200') { throw "$url did not return 200:`n$headers" }
}
function WSL-Read([string]$path) {
    return ((wsl -d devstack -u root -- cat $path) -join "`n")
}

Write-Host '== Apache default =='
$null = devstack vhost use builtin --yes
$null = devstack vhost use apache --yes
Assert-Equal (Vhost-Mode) 'apache' 'apache mode'
Assert-Equal (Vhost-OK) 'True' 'apache config'
Assert-Status "https://$site/up"
Assert-HTTP "https://$site/" 'Apache'
$apacheVhost = WSL-Read "/data/devstack/services/apache/config/conf.d/generated/$siteId.conf"
foreach ($needle in @("DocumentRoot `"$containerRoot`"", 'AllowOverride All', 'SetHandler "proxy:fcgi://127.0.0.1:43000"')) {
    if (-not $apacheVhost.Contains($needle)) { throw "Apache vhost missing '$needle':`n$apacheVhost" }
}
if ($apacheVhost.Contains('listen 443') -or $apacheVhost.Contains('ssl_certificate')) {
    throw "Apache private vhost contains public TLS:`n$apacheVhost"
}
Write-Host 'apache OK'

Write-Host '== Builtin restore =='
$null = devstack vhost use builtin --yes
Assert-Equal (Vhost-Mode) 'builtin' 'builtin mode'
$builtinHeaders = HTTP-Headers "https://$site/up"
if ($builtinHeaders -notmatch 'HTTP/\d\.\d 200') { throw "builtin route failed:`n$builtinHeaders" }
$runningApache = (wsl -d devstack -u root -- docker ps --format '{{.Names}}') -join ''
if ($runningApache.Contains('devstack-apache')) { throw 'Apache container is still running after builtin switch' }
if ($staticSite) { Assert-Status "https://$staticSite/" }
Write-Host 'builtin OK'

Write-Host '== nginx mode =='
$null = devstack vhost use nginx --yes
Assert-Equal (Vhost-Mode) 'nginx' 'nginx mode'
Assert-Equal (Vhost-OK) 'True' 'nginx config'
Assert-Status "https://$site/up"
Assert-HTTP "https://$site/" 'nginx'
if ($staticSite) { Assert-Status "https://$staticSite/" }
$nginxVhost = WSL-Read "/data/devstack/services/nginx/config/conf.d/generated/$siteId.conf"
foreach ($needle in @('listen 18080;', "root $containerRoot;", 'fastcgi_pass 127.0.0.1:43000;', '@devstack_front_controller')) {
    if (-not $nginxVhost.Contains($needle)) { throw "nginx vhost missing '$needle':`n$nginxVhost" }
}
if ($nginxVhost.Contains('listen 443') -or $nginxVhost.Contains('ssl_certificate')) {
    throw "nginx private vhost contains public TLS:`n$nginxVhost"
}
Write-Host 'nginx OK'

Write-Host '== Direct nginx -> Apache switch =='
$switch = (devstack vhost use apache --yes 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0) { throw "direct nginx -> apache switch failed:`n$switch" }
Assert-Equal (Vhost-Mode) 'apache' 'direct switch mode'
Assert-HTTP "https://$site/" 'Apache'
Write-Host 'direct switch OK'

Write-Host '== Deactivate active Apache falls back to builtin =='
$null = devstack service deactivate apache --yes
Assert-Equal (Vhost-Mode) 'builtin' 'deactivate fallback mode'
Assert-Status "https://$site/up"
$null = devstack service activate apache --yes
$null = devstack vhost use apache --yes
Assert-Equal (Vhost-Mode) 'apache' 'apache mode after deactivate fallback'
Write-Host 'deactivate fallback OK'

Write-Host '== Uninstall active Apache falls back to builtin =='
$uninstall = (devstack service uninstall apache --yes 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0) { throw "uninstall active Apache failed:`n$uninstall" }
Assert-Equal (Vhost-Mode) 'builtin' 'uninstall fallback mode'
Assert-Status "https://$site/up"
$null = devstack service install apache --yes
$null = devstack service activate apache --yes
$null = devstack vhost use apache --yes
Assert-Equal (Vhost-Mode) 'apache' 'apache mode after uninstall fallback'
Assert-HTTP "https://$site/" 'Apache'
Write-Host 'uninstall fallback OK'

Write-Host '== Restore Apache default =='
$null = devstack vhost use builtin --yes
$null = devstack vhost use apache --yes
Assert-Equal (Vhost-Mode) 'apache' 'final apache mode'
Assert-Status "https://$site/up"
if ($staticSite) { Assert-Status "https://$staticSite/" }

$doctor = (devstack doctor 2>&1 | Out-String)
if ($LASTEXITCODE -ne 0) { throw "doctor failed:`n$doctor" }
if ($doctor -notmatch 'All checks passed') { throw "doctor did not pass:`n$doctor" }
Write-Host 'SERVING_E2E_PASS'
