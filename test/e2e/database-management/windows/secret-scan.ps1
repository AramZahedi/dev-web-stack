# Windows secret scanner for evidence/log files.
#
# Prints only redacted locations. Exits 1 on any hit. Secret-store files under
# a secrets directory are not scanned. Exact generated values can be supplied
# with -SecretsDir (one value per file).
[CmdletBinding()]
param(
    [string[]]$Path = @(),
    [string]$SecretsDir = '',
    [switch]$SelfTest
)
if (-not $SelfTest -and $Path.Count -eq 0) {
    throw 'secret-scan: -Path is required unless -SelfTest is used'
}

$ErrorActionPreference = 'Stop'
$patterns = @(
    @{ Name = 'private-key'; Regex = '-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----' },
    @{ Name = 'connection-string'; Regex = '(mysql|mariadb|postgres|postgresql|redis)://[^\s/@:]+:[^\s/@]+@' },
    @{ Name = 'password-assignment'; Regex = '(DB_PASSWORD|DATABASE_URL|MYSQL_PWD|PGPASSWORD|REDISCLI_AUTH|REDIS_PASSWORD|DEVSTACK_TOKEN|INSTALL_TOKEN|GITHUB_TOKEN|GH_TOKEN)\s*[:=]\s*[^\s"'']+' },
    @{ Name = 'github-or-aws-token'; Regex = '(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16})' },
    @{ Name = 'json-password'; Regex = '"password"\s*:\s*"[^"]+"' },
    @{ Name = 'password-literal'; Regex = 'password\s*=\s*[A-Za-z0-9+/=_-]{16,}' }
)
$exact = @()
if (-not [string]::IsNullOrWhiteSpace($SecretsDir) -and (Test-Path -LiteralPath $SecretsDir)) {
    foreach ($file in Get-ChildItem -LiteralPath $SecretsDir -File -ErrorAction SilentlyContinue) {
        if ($file.Length -gt 0) {
            $exact += (Get-Content -LiteralPath $file.FullName -Raw).Trim()
        }
    }
}

function Test-PathSkipped {
    param([string]$File)
    $normalized = $File.Replace('\', '/')
    if ($normalized -match '/secrets/') { return $true }
    $extension = [System.IO.Path]::GetExtension($File).ToLowerInvariant()
    return $extension -in @('.acl', '.tgz', '.gz', '.zip', '.png', '.jpg', '.jpeg', '.exe', '.dll', '.woff', '.woff2')
}

function Scan-Path {
    param([string[]]$Roots)
    $hits = 0
    foreach ($root in $Roots) {
        if (-not (Test-Path -LiteralPath $root)) { continue }
        $files = if ((Get-Item -LiteralPath $root).PSIsContainer) {
            Get-ChildItem -LiteralPath $root -Recurse -File -ErrorAction SilentlyContinue
        } else {
            Get-Item -LiteralPath $root
        }
        foreach ($file in $files) {
            if (Test-PathSkipped -File $file.FullName) { continue }
            foreach ($pattern in $patterns) {
                $matches = Select-String -LiteralPath $file.FullName -Pattern $pattern.Regex -AllMatches -ErrorAction SilentlyContinue
                if ($matches) {
                    foreach ($match in $matches) {
                        Write-Output ("REDACTED {0}:{1} pattern={2}" -f $file.FullName, $match.LineNumber, $pattern.Name)
                        $hits++
                    }
                }
            }
            foreach ($value in $exact) {
                if ([string]::IsNullOrWhiteSpace($value)) { continue }
                $matches = Select-String -LiteralPath $file.FullName -Pattern $value -SimpleMatch -AllMatches -ErrorAction SilentlyContinue
                if ($matches) {
                    foreach ($match in $matches) {
                        Write-Output ("REDACTED {0}:{1} pattern=exact-runtime-secret" -f $file.FullName, $match.LineNumber)
                        $hits++
                    }
                }
            }
        }
    }
    return $hits
}

if ($SelfTest) {
    $tmp = Join-Path $env:TEMP ("devstack-e2e-scan-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
        Set-Content -LiteralPath (Join-Path $tmp 'leak.log') -Value 'DATABASE_URL=mysql://demo:not-a-real-password@127.0.0.1/db' -Encoding utf8
        $leakHits = Scan-Path -Roots @($tmp)
        if ($leakHits -eq 0) { throw 'self-test: fake secret was not detected' }
        Remove-Item -LiteralPath (Join-Path $tmp 'leak.log') -Force
        Set-Content -LiteralPath (Join-Path $tmp 'clean.log') -Value 'clean evidence' -Encoding utf8
        $cleanHits = Scan-Path -Roots @($tmp)
        if ($cleanHits -ne 0) { throw 'self-test: false positive on clean evidence' }
        Write-Output 'secret-scan: self-test OK'
        exit 0
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$total = Scan-Path -Roots $Path
if ($total -gt 0) {
    Write-Error "secret-scan: FAIL ($total redacted hit(s))"
    exit 1
}
Write-Output 'secret-scan: OK'
exit 0
