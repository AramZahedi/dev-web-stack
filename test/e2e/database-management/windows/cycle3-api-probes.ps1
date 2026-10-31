# Read-only API/security probes for the database-management E2E run.
#
# Every request is either a read-only GET or a mutation that is expected to be
# rejected before any handler runs (missing session/CSRF, malformed body, or an
# oversized declared body). No successful mutation is attempted.
[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://127.0.0.1:7070',
    [string]$EvidenceDir = ''
)

$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..\..')).Path
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
    $stamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
    $EvidenceDir = Join-Path $repo "dist\e2e\database-management\evidence\$stamp\WIN-REAL-api"
}
New-Item -ItemType Directory -Force -Path $EvidenceDir | Out-Null
$uri = [Uri]$BaseUrl

function Redact-Text {
    param([string]$Text)
    if ([string]::IsNullOrEmpty($Text)) { return '' }
    $value = $Text
    $value = [regex]::Replace($value, '[0-9a-fA-F]{32,}', '<hex-redacted>')
    $value = [regex]::Replace($value, '(?i)(password|token|secret)\s*[:=]\s*\S+', '$1=<redacted>')
    if ($value.Length -gt 400) { $value = $value.Substring(0, 400) + '...' }
    return $value
}

function Get-ErrorFields {
    param([string]$Body)
    $code = ''
    $message = ''
    if ([string]::IsNullOrWhiteSpace($Body)) { return @{ code = ''; message = '' } }
    try {
        $parsed = $Body | ConvertFrom-Json
        if ($null -ne $parsed.code) { $code = [string]$parsed.code }
        if ($null -ne $parsed.message) { $message = [string]$parsed.message }
    } catch {
        $message = Redact-Text $Body
    }
    return @{ code = $code; message = (Redact-Text $message) }
}

function Invoke-Probe {
    param(
        [string]$Name,
        [string]$Method,
        [string]$Path,
        [string]$Body = '',
        [hashtable]$Headers = @{},
        [Microsoft.PowerShell.Commands.WebRequestSession]$Session = $null,
        [switch]$SkipHttpErrorCheck
    )
    $target = $BaseUrl.TrimEnd('/') + $Path
    $params = @{
        Uri                = $target
        Method             = $Method
        Headers            = $Headers
        TimeoutSec         = 20
        SkipHttpErrorCheck = $true
        UseBasicParsing     = $true
    }
    if ($null -ne $Session) { $params.WebSession = $Session }
    if ($Body -ne '') { $params.Body = $Body; $params.ContentType = 'application/json' }
    try {
        $resp = Invoke-WebRequest @params
        $text = [string]$resp.Content
        $fields = Get-ErrorFields $text
        return [pscustomobject]@{
            name        = $Name
            method      = $Method
            path        = $Path
            status      = [int]$resp.StatusCode
            code        = $fields.code
            message     = $fields.message
            bytes       = $text.Length
            content_type = [string]$resp.Headers['Content-Type']
        }
    } catch {
        return [pscustomobject]@{
            name = $Name; method = $Method; path = $Path
            status = 0; code = 'TRANSPORT'; message = (Redact-Text $_.Exception.Message); bytes = 0; content_type = ''
        }
    }
}

function Send-Raw {
    param([string]$Name, [byte[]]$RequestBytes, [int]$TimeoutMs = 10000)
    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $client.Connect($uri.Host, $uri.Port)
        $client.ReceiveTimeout = $TimeoutMs
        $client.SendTimeout = $TimeoutMs
        $stream = $client.GetStream()
        try {
            $stream.Write($RequestBytes, 0, $RequestBytes.Length)
            $stream.Flush()
        } catch {
            # The server may close early on oversized headers; still read the response.
        }
        $buffer = [byte[]]::new(65536)
        $sb = [System.Text.StringBuilder]::new()
        try {
            while ($true) {
                $read = $stream.Read($buffer, 0, $buffer.Length)
                if ($read -le 0) { break }
                [void]$sb.Append([System.Text.Encoding]::ASCII.GetString($buffer, 0, $read))
                if ($sb.ToString() -match '\r\n\r\n' -and $sb.ToString().Length -gt 256) { break }
            }
        } catch { }
        $text = $sb.ToString()
        $status = 0
        if ($text -match '^HTTP/\d\.\d\s+(\d{3})') { $status = [int]$Matches[1] }
        $body = ''
        $idx = $text.IndexOf("`r`n`r`n")
        if ($idx -ge 0) { $body = $text.Substring($idx + 4) }
        $fields = Get-ErrorFields $body
        return [pscustomobject]@{
            name = $Name; method = 'RAW'; path = $Name; status = $status
            code = $fields.code; message = $fields.message; bytes = $body.Length; content_type = ''
        }
    } finally {
        $client.Dispose()
    }
}

$results = [System.Collections.Generic.List[object]]::new()
$session = [Microsoft.PowerShell.Commands.WebRequestSession]::new()

# A. Read-only access without an install token/session.
$results.Add((Invoke-Probe 'health-no-token' 'GET' '/api/v1/health'))
$results.Add((Invoke-Probe 'services-no-token' 'GET' '/api/v1/services'))
$results.Add((Invoke-Probe 'service-mysql-no-token' 'GET' '/api/v1/services/mysql'))
$results.Add((Invoke-Probe 'doctor-no-token' 'GET' '/api/v1/doctor'))
$results.Add((Invoke-Probe 'settings-no-token' 'GET' '/api/v1/settings'))
$results.Add((Invoke-Probe 'fs-no-session' 'GET' '/api/v1/fs'))
$results.Add((Invoke-Probe 'config-content-no-session' 'GET' '/api/v1/services/phpmyadmin/config/content?path=config.user.inc.php'))

$authResp = Invoke-WebRequest -Uri ($BaseUrl.TrimEnd('/') + '/api/v1/auth') -Method Post -SkipHttpErrorCheck -UseBasicParsing -TimeoutSec 20
$results.Add([pscustomobject]@{ name = 'auth-no-token'; method = 'POST'; path = '/api/v1/auth'; status = [int]$authResp.StatusCode; code = ''; message = ''; bytes = ([string]$authResp.Content).Length; content_type = [string]$authResp.Headers['Content-Type'] })

# Establish a dashboard session and inspect its cookie flags. Do not persist the token.
$sessionResp = Invoke-WebRequest -Uri ($BaseUrl.TrimEnd('/') + '/api/v1/session') -Method Get -SkipHttpErrorCheck -UseBasicParsing -TimeoutSec 20 -SessionVariable webSession
$sessionBody = [string]$sessionResp.Content
$csrf = ''
try { $csrf = [string](($sessionBody | ConvertFrom-Json).csrf_token) } catch { }
$setCookie = [string]$sessionResp.Headers['Set-Cookie']
$cookieFlags = [ordered]@{
    name = 'devstack_session'
    http_only = $setCookie -match '(?i)HttpOnly'
    same_site_strict = $setCookie -match '(?i)SameSite=Strict'
    secure = $setCookie -match '(?i)Secure'
    max_age_present = $setCookie -match '(?i)Max-Age='
    expires_present = $setCookie -match '(?i)Expires='
}
$results.Add([pscustomobject]@{ name = 'session-bootstrap'; method = 'GET'; path = '/api/v1/session'; status = [int]$sessionResp.StatusCode; code = ''; message = 'csrf_present=' + [bool]$csrf + ' csrf_len=' + $csrf.Length; bytes = $sessionBody.Length; content_type = [string]$sessionResp.Headers['Content-Type'] })

# B. Mutations without a session or CSRF.
$results.Add((Invoke-Probe 'create-site-no-session' 'POST' '/api/v1/sites' '{}'))
$results.Add((Invoke-Probe 'doctor-run-no-session' 'POST' '/api/v1/doctor/run'))
$results.Add((Invoke-Probe 'reveal-service-credentials-no-session' 'POST' '/api/v1/services/mysql/credentials/reveal'))
$results.Add((Invoke-Probe 'reveal-user-password-no-session' 'POST' '/api/v1/services/mysql/users/usr_missing/reveal'))
$results.Add((Invoke-Probe 'create-site-session-no-csrf' 'POST' '/api/v1/sites' '{}' @{} $webSession))
$results.Add((Invoke-Probe 'create-site-session-wrong-csrf' 'POST' '/api/v1/sites' '{}' @{ 'X-CSRF-Token' = 'not-the-token' } $webSession))
$results.Add((Invoke-Probe 'reveal-service-credentials-session-no-csrf' 'POST' '/api/v1/services/mysql/credentials/reveal' '' @{} $webSession))
if ($csrf -ne '') {
    $results.Add((Invoke-Probe 'create-site-malformed-json' 'POST' '/api/v1/sites' '{' @{ 'X-CSRF-Token' = $csrf } $webSession))
    $results.Add((Invoke-Probe 'create-site-duplicate-fields' 'POST' '/api/v1/sites' '{"name":"e2e_probe_a","name":"e2e_probe_b"}' @{ 'X-CSRF-Token' = $csrf } $webSession))
    $results.Add((Invoke-Probe 'create-site-empty-body' 'POST' '/api/v1/sites' '' @{ 'X-CSRF-Token' = $csrf } $webSession))
    $huge = '{"name":"' + ('a' * (2 * 1024 * 1024)) + '"}'
    $results.Add((Invoke-Probe 'create-site-2mib-body' 'POST' '/api/v1/sites' $huge @{ 'X-CSRF-Token' = $csrf } $webSession))
}

# C. Method/path handling.
$results.Add((Invoke-Probe 'method-not-allowed' 'PUT' '/api/v1/health'))
$results.Add((Invoke-Probe 'api-not-found' 'GET' '/api/v1/does-not-exist'))
$results.Add((Invoke-Probe 'host-guard' 'GET' '/api/v1/health' '' @{ 'Host' = 'evil.test' }))
$results.Add((Invoke-Probe 'non-loopback-xff' 'GET' '/api/v1/health' '' @{ 'X-Forwarded-For' = '8.8.8.8' }))

# D. IDOR / read-only object probes.
$results.Add((Invoke-Probe 'service-postgresql-read' 'GET' '/api/v1/services/postgresql'))
$results.Add((Invoke-Probe 'protected-db-mysql-read' 'GET' '/api/v1/services/mysql/databases/mysql'))
$results.Add((Invoke-Probe 'protected-db-information-schema-read' 'GET' '/api/v1/services/mysql/databases/information_schema'))
$results.Add((Invoke-Probe 'protected-db-performance-schema-read' 'GET' '/api/v1/services/mysql/databases/performance_schema'))
$results.Add((Invoke-Probe 'protected-db-sys-read' 'GET' '/api/v1/services/mysql/databases/sys'))
$results.Add((Invoke-Probe 'missing-db-read' 'GET' '/api/v1/services/mysql/databases/e2e_missing'))
$results.Add((Invoke-Probe 'missing-user-read' 'GET' '/api/v1/services/mysql/users/usr_missing'))
$results.Add((Invoke-Probe 'missing-backup-read' 'GET' '/api/v1/services/mysql/backups/bkp_missing'))
$results.Add((Invoke-Probe 'missing-backup-download' 'GET' '/api/v1/services/mysql/backups/bkp_missing/download'))
$results.Add((Invoke-Probe 'missing-job-read' 'GET' '/api/v1/jobs/job_missing'))
$results.Add((Invoke-Probe 'missing-site-read' 'GET' '/api/v1/sites/s_missing'))
$results.Add((Invoke-Probe 'missing-site-databases-read' 'GET' '/api/v1/sites/s_missing/databases'))
$results.Add((Invoke-Probe 'redis-databases-read' 'GET' '/api/v1/services/redis/databases'))
$results.Add((Invoke-Probe 'postgresql-databases-read' 'GET' '/api/v1/services/postgresql/databases'))
$results.Add((Invoke-Probe 'mysql-database-impact-read' 'GET' '/api/v1/services/mysql/database-impact'))

# E. Read-only path traversal probes. Bodies are never saved.
$traversal = @(
    '..%2fstate.db',
    '..%2f..%2fstate.db',
    '..%5c..%5cstate.db',
    '%2fetc%2fpasswd',
    'C%3a%5cUsers%5cAram%5c.devstack%5cstate.db',
    'config.user.inc.php%2f..%2f..%2fstate.db'
)
foreach ($p in $traversal) {
    $results.Add((Invoke-Probe ("config-traversal-" + ($p -replace '[^A-Za-z0-9]', '_')) 'GET' ("/api/v1/services/phpmyadmin/config/content?path=" + $p) '' @{} $webSession))
}
$results.Add((Invoke-Probe 'backup-download-traversal' 'GET' '/api/v1/services/mysql/backups/..%2f..%2fstate.db/download'))
$results.Add((Invoke-Probe 'database-export-traversal' 'GET' '/api/v1/services/mysql/databases/..%2f..%2fetc%2fpasswd/export'))

# F. SQL/name injection through read-only GET paths.
$injection = @(
    'e2e%27%3bdrop%20table%20x%3b--',
    'e2e%22%3bdrop%20database%20mysql%3b--',
    '..%2f..%2fetc',
    'mysql%00',
    '%20',
    'e2e%3bdrop%20database%20devstack'
)
foreach ($p in $injection) {
    $results.Add((Invoke-Probe ("db-injection-" + ($p -replace '[^A-Za-z0-9]', '_')) 'GET' ("/api/v1/services/mysql/databases/" + $p)))
}
$userInjection = @(
    'usr_%27%20OR%201%3d1--',
    'usr_%00',
    'usr_%2e%2e%2fetc'
)
foreach ($p in $userInjection) {
    $results.Add((Invoke-Probe ("user-injection-" + ($p -replace '[^A-Za-z0-9]', '_')) 'GET' ("/api/v1/services/mysql/users/" + $p)))
}

# G. Raw transport probes: declared body over 1 GiB and an oversized header.
$cookieHeader = ''
if ($setCookie -ne '') {
    $cookiePair = ($setCookie -split ';')[0]
    $cookieHeader = $cookiePair.Trim()
}
if ($csrf -ne '') {
    $over = "POST /api/v1/sites HTTP/1.1`r`nHost: 127.0.0.1:$($uri.Port)`r`nCookie: $cookieHeader`r`nX-CSRF-Token: $csrf`r`nContent-Type: application/json`r`nContent-Length: 1181116006`r`nConnection: close`r`n`r`n"
    $results.Add((Send-Raw 'declared-body-over-1gib' ([System.Text.Encoding]::ASCII.GetBytes($over))))
}
$bigHeader = 'GET /api/v1/health HTTP/1.1' + "`r`n" + "Host: 127.0.0.1:$($uri.Port)" + "`r`n" + 'X-Big: ' + ('a' * (1100 * 1024)) + "`r`nConnection: close`r`n`r`n"
$results.Add((Send-Raw 'oversized-header' ([System.Text.Encoding]::ASCII.GetBytes($bigHeader))))

$summary = [ordered]@{
    generated_at = (Get-Date).ToUniversalTime().ToString('o')
    base_url = $BaseUrl
    auth_disabled = ($authResp.StatusCode -eq 204)
    session_cookie = $cookieFlags
    csrf_token_persisted = $false
    probes = $results
}
$summary | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $EvidenceDir 'api-probes.json') -Encoding utf8
$results | Format-Table name,status,code,bytes -AutoSize | Out-String -Width 240 | Set-Content -LiteralPath (Join-Path $EvidenceDir 'api-probes.txt') -Encoding utf8
Write-Host "api-probes: $($results.Count) probes -> $EvidenceDir"
