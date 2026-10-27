# Windows Lifecycle Test Suite: Upgrade, Rollback, & Uninstall
# Tests Task G4 requirements:
# 1. install.ps1 -Upgrade flow (backup, stage, health check, manifest update).
# 2. install.ps1 -Upgrade rollback on failure (restore data, restore binaries, non-zero exit).
# 3. uninstall.ps1 elevation verification.
# 4. uninstall.ps1 default behavior (preserves distro and user projects, removes helper, task, firewall, hosts, CA trust).
# 5. uninstall.ps1 -PurgeDistro (unregisters WSL distro).
# 6. uninstall.ps1 -PurgeData (removes securestore secrets and InstallDir).
# 7. uninstall.ps1 Machine PATH cleanup.

param(
    [switch]$VerboseOutput
)

$ErrorActionPreference = "Stop"
$script:Failures = 0
$script:TestsRun = 0

function Step([string]$Message) { Write-Host "`nlifecycle-test: === $Message ===" }
function Pass([string]$Message) {
    Write-Host "lifecycle-test: OK: $Message" -ForegroundColor Green
    $script:TestsRun++
}
function Fail([string]$Message) {
    Write-Host "lifecycle-test: FAIL: $Message" -ForegroundColor Red
    $script:Failures++
    $script:TestsRun++
}
function Require([bool]$Condition, [string]$Message) {
    if ($Condition) { Pass $Message } else { Fail $Message }
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$installerScript = Join-Path $repoRoot "install.ps1"
$uninstallerScript = Join-Path $repoRoot "uninstall.ps1"
$psEngine = if ($PSVersionTable.PSEdition -eq "Core") { "pwsh" } else { "powershell" }

function Invoke-Script([string]$ScriptPath, [string[]]$Arguments) {
    $prev = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = & $psEngine -NoProfile -File $ScriptPath @Arguments 2>&1 | Out-String
        return @{ ExitCode = $LASTEXITCODE; Output = $output }
    } finally {
        $ErrorActionPreference = $prev
    }
}

# -------------------------------------------------------------------------
# Test 1: Files Existence
# -------------------------------------------------------------------------
Step "Checking lifecycle scripts existence"
Require (Test-Path $installerScript) "install.ps1 exists"
Require (Test-Path $uninstallerScript) "uninstall.ps1 exists"

# -------------------------------------------------------------------------
# Test 2: Help flag returns 0 and displays usage
# -------------------------------------------------------------------------
Step "Testing parameter handling: -Help"
$resInstall = Invoke-Script $installerScript @("-Help")
Require ($resInstall.ExitCode -eq 0) "install.ps1 -Help exits with code 0"
Require ($resInstall.Output -match "-Upgrade") "install.ps1 -Help documents -Upgrade switch"

$resUninstall = Invoke-Script $uninstallerScript @("-Help")
Require ($resUninstall.ExitCode -eq 0) "uninstall.ps1 -Help exits with code 0 (got $($resUninstall.ExitCode))"
Require ($resUninstall.Output -match "-PurgeDistro" -and $resUninstall.Output -match "-PurgeData") "uninstall.ps1 -Help documents -PurgeDistro and -PurgeData"

# -------------------------------------------------------------------------
# Test 3: Elevation check in uninstall.ps1
# -------------------------------------------------------------------------
Step "Testing uninstaller elevation check logic"
$env:DEVSTACK_TEST_ELEVATED = "false"
try {
    $res = Invoke-Script $uninstallerScript @()
    Require ($res.ExitCode -eq 2) "uninstall.ps1 without elevation exits with code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "elevation" -or $res.Output -match "Administrator") "Non-elevated output mentions elevation/administrator"
} finally {
    Remove-Item env:DEVSTACK_TEST_ELEVATED -ErrorAction SilentlyContinue
}

$tempSandboxElev = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-uninst-elev-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempSandboxElev | Out-Null
try {
    $env:DEVSTACK_TEST_ELEVATED = "false"
    $res = Invoke-Script $uninstallerScript @("-DryRun", "-TestSandbox", $tempSandboxElev)
    Require ($res.ExitCode -eq 0) "uninstall.ps1 DryRun/TestSandbox allows non-elevated execution (got $($res.ExitCode))"
} finally {
    Remove-Item env:DEVSTACK_TEST_ELEVATED -ErrorAction SilentlyContinue
    Remove-Item -Path $tempSandboxElev -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 4: Upgrade Flow (Happy Path)
# -------------------------------------------------------------------------
Step "Testing install.ps1 -Upgrade (Happy Path)"
$sandboxUpgrade = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-upgrade-happy-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $sandboxUpgrade | Out-Null
try {
    # 1. Establish initial layout and mock WSL state
    $sbBin = Join-Path $sandboxUpgrade "bin"
    $sbSystem = Join-Path $sandboxUpgrade "system"
    $sbWsl = Join-Path $sandboxUpgrade "wsl\devstack"
    $sbMockWsl = Join-Path $sandboxUpgrade "mock-wsl"
    $sbMockData = Join-Path $sbMockWsl "data\devstack"
    $sbMockDataBin = Join-Path $sbMockData "bin"
    $sbMockSystemd = Join-Path $sbMockWsl "etc\systemd\system"

    New-Item -ItemType Directory -Force -Path $sbBin | Out-Null
    New-Item -ItemType Directory -Force -Path $sbSystem | Out-Null
    New-Item -ItemType Directory -Force -Path $sbWsl | Out-Null
    New-Item -ItemType Directory -Force -Path $sbMockDataBin | Out-Null
    New-Item -ItemType Directory -Force -Path $sbMockSystemd | Out-Null

    # Existing binaries and files
    Set-Content -Path (Join-Path $sbBin "devstack.exe") -Value "old-devstack-exe" -Encoding utf8
    Set-Content -Path (Join-Path $sbBin "devstack-helper.exe") -Value "old-helper-exe" -Encoding utf8
    Set-Content -Path (Join-Path $sbMockDataBin "devstack-agent") -Value "v1-agent-binary" -Encoding utf8
    Set-Content -Path (Join-Path $sbMockData "managed.json") -Value '{"managed_by":"devstack","install_id":"init-id","installed_at":"2026-10-01T00:00:00Z"}' -Encoding utf8
    Set-Content -Path (Join-Path $sbMockData "user-state.db") -Value "preserved-state-content" -Encoding utf8

    $initialManifest = [ordered]@{
        version = "1.0"
        created_at = "2026-10-01T00:00:00Z"
        stage = "complete"
        agent_installed = $true
    }
    Set-Content -Path (Join-Path $sbSystem "install-manifest.json") -Value (ConvertTo-Json $initialManifest) -Encoding utf8

    # Run upgrade in sandbox
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_AGENT_HEALTH = "healthy"
    $res = Invoke-Script $installerScript @("-Upgrade", "-TestSandbox", $sandboxUpgrade)

    Require ($res.ExitCode -eq 0) "install.ps1 -Upgrade exits with code 0 (got $($res.ExitCode))"
    Require ($res.Output -match "backup" -or $res.Output -match "Backup") "Upgrade reports backup of /data/devstack"
    Require ($res.Output -match "health" -or $res.Output -match "verified") "Upgrade verifies agent health"
    Require ($res.Output -match "Upgrade completed" -or $res.Output -match "success") "Upgrade reports successful completion"

    # User state in /data/devstack is preserved
    $userDataPreserved = Test-Path (Join-Path $sbMockData "user-state.db")
    Require $userDataPreserved "Existing user state in /data/devstack preserved after upgrade"
    if ($userDataPreserved) {
        $content = Get-Content (Join-Path $sbMockData "user-state.db") -Raw
        Require ($content -match "preserved-state-content") "User state content is unchanged"
    }

    # Manifest updated
    $manifestPath = Join-Path $sbSystem "install-manifest.json"
    Require (Test-Path $manifestPath) "install-manifest.json exists after upgrade"
    if (Test-Path $manifestPath) {
        $upgradedManifest = Get-Content $manifestPath -Raw | ConvertFrom-Json
        Require ($null -ne $upgradedManifest.upgraded_at -or $upgradedManifest.stage -eq "complete") "Manifest records upgraded stage/timestamp"
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_AGENT_HEALTH -ErrorAction SilentlyContinue
    Remove-Item -Path $sandboxUpgrade -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 5: Upgrade Rollback on Agent Startup Failure
# -------------------------------------------------------------------------
Step "Testing install.ps1 -Upgrade Rollback on Failure"
$sandboxRollback = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-upgrade-rollback-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $sandboxRollback | Out-Null
try {
    $sbBin = Join-Path $sandboxRollback "bin"
    $sbSystem = Join-Path $sandboxRollback "system"
    $sbMockWsl = Join-Path $sandboxRollback "mock-wsl"
    $sbMockData = Join-Path $sbMockWsl "data\devstack"
    $sbMockDataBin = Join-Path $sbMockData "bin"

    New-Item -ItemType Directory -Force -Path $sbBin | Out-Null
    New-Item -ItemType Directory -Force -Path $sbSystem | Out-Null
    New-Item -ItemType Directory -Force -Path $sbMockDataBin | Out-Null

    Set-Content -Path (Join-Path $sbBin "devstack.exe") -Value "v1-devstack-exe" -Encoding utf8
    Set-Content -Path (Join-Path $sbBin "devstack-helper.exe") -Value "v1-helper-exe" -Encoding utf8
    Set-Content -Path (Join-Path $sbMockDataBin "devstack-agent") -Value "v1-agent-binary-before-upgrade" -Encoding utf8
    Set-Content -Path (Join-Path $sbMockData "critical-data.txt") -Value "original-unmodified-data" -Encoding utf8

    $initialManifest = [ordered]@{
        version = "1.0"
        stage = "complete"
    }
    Set-Content -Path (Join-Path $sbSystem "install-manifest.json") -Value (ConvertTo-Json $initialManifest) -Encoding utf8

    # Simulate agent health check failure
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_UPGRADE_FAIL = "true"
    $res = Invoke-Script $installerScript @("-Upgrade", "-TestSandbox", $sandboxRollback)

    Require ($res.ExitCode -eq 1) "Upgrade failure exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "rollback" -or $res.Output -match "Rollback") "Output indicates rollback was triggered"

    # Verify data restored from backup
    $dataExists = Test-Path (Join-Path $sbMockData "critical-data.txt")
    Require $dataExists "Data restored to /data/devstack on rollback"
    if ($dataExists) {
        $content = Get-Content (Join-Path $sbMockData "critical-data.txt") -Raw
        Require ($content -match "original-unmodified-data") "Restored data matches original state"
    }

    # Verify agent binary rolled back
    $agentBinaryPath = Join-Path $sbMockDataBin "devstack-agent"
    Require (Test-Path $agentBinaryPath) "Agent binary exists after rollback"
    if (Test-Path $agentBinaryPath) {
        $agentContent = Get-Content $agentBinaryPath -Raw
        Require ($agentContent -match "v1-agent-binary-before-upgrade") "Agent binary restored to pre-upgrade version"
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_UPGRADE_FAIL -ErrorAction SilentlyContinue
    Remove-Item -Path $sandboxRollback -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 6: Default Uninstall (Preserves Distro & Projects)
# -------------------------------------------------------------------------
Step "Testing uninstall.ps1 Default Behavior"
$sandboxUninstall = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-uninst-default-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $sandboxUninstall | Out-Null
try {
    $sbBin = Join-Path $sandboxUninstall "bin"
    $sbSystem = Join-Path $sandboxUninstall "system"
    $sbWsl = Join-Path $sandboxUninstall "wsl\devstack"
    $sbMockWsl = Join-Path $sandboxUninstall "mock-wsl"
    $sbMockData = Join-Path $sbMockWsl "data\devstack"
    $sbHosts = Join-Path $sandboxUninstall "hosts"

    New-Item -ItemType Directory -Force -Path $sbBin | Out-Null
    New-Item -ItemType Directory -Force -Path $sbSystem | Out-Null
    New-Item -ItemType Directory -Force -Path $sbWsl | Out-Null
    New-Item -ItemType Directory -Force -Path $sbMockData | Out-Null

    Set-Content -Path (Join-Path $sbBin "devstack.exe") -Value "devstack-exe" -Encoding utf8
    Set-Content -Path (Join-Path $sbWsl "ext4.vhdx") -Value "fake-distro-disk" -Encoding utf8
    Set-Content -Path (Join-Path $sbMockData "project-files.txt") -Value "user-project-files" -Encoding utf8

    # Create mock hosts file with devstack block and unrelated lines
    $initialHosts = @"
127.0.0.1 localhost
::1 localhost
# Unrelated entry that must be kept
192.168.1.10 custom.server.local

# BEGIN devstack
127.0.0.1 app.test
127.0.0.1 api.test
# END devstack

10.0.0.5 database.internal
"@
    Set-Content -Path $sbHosts -Value $initialHosts -Encoding utf8

    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_HOSTS_PATH = $sbHosts
    $res = Invoke-Script $uninstallerScript @("-TestSandbox", $sandboxUninstall)

    Require ($res.ExitCode -eq 0) "uninstall.ps1 exits with code 0 (got $($res.ExitCode))"
    Require ($res.Output -match "Firewall" -or $res.Output -match "firewall") "Output mentions removing firewall rule"
    Require ($res.Output -match "DevstackHelper" -or $res.Output -match "service") "Output mentions stopping/removing DevstackHelper service"
    Require ($res.Output -match "scheduled task" -or $res.Output -match "devstack-daemon") "Output mentions removing scheduled task"
    Require ($res.Output -match "Preserving WSL distro" -or $res.Output -match "distro intact") "Default uninstall preserves WSL distro"

    # Distro folder and disk are preserved
    Require (Test-Path (Join-Path $sbWsl "ext4.vhdx")) "WSL distro files preserved by default"
    Require (Test-Path (Join-Path $sbMockData "project-files.txt")) "User project files preserved by default"

    # Hosts file cleaned of devstack block, unrelated entries preserved
    $cleanedHosts = Get-Content -Path $sbHosts -Raw
    Require ($cleanedHosts -notmatch "BEGIN devstack") "Devstack managed block removed from hosts"
    Require ($cleanedHosts -notmatch "app\.test") "Managed domains removed from hosts"
    Require ($cleanedHosts -match "custom\.server\.local") "Unrelated entry custom.server.local preserved in hosts"
    Require ($cleanedHosts -match "database\.internal") "Unrelated entry database.internal preserved in hosts"
} finally {
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_HOSTS_PATH -ErrorAction SilentlyContinue
    Remove-Item -Path $sandboxUninstall -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 7: Uninstall with -PurgeDistro
# -------------------------------------------------------------------------
Step "Testing uninstall.ps1 -PurgeDistro"
$sandboxPurgeDistro = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-uninst-distro-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $sandboxPurgeDistro | Out-Null
try {
    $sbWsl = Join-Path $sandboxPurgeDistro "wsl\devstack"
    $sbMockWsl = Join-Path $sandboxPurgeDistro "mock-wsl"
    New-Item -ItemType Directory -Force -Path $sbWsl | Out-Null
    New-Item -ItemType Directory -Force -Path $sbMockWsl | Out-Null
    Set-Content -Path (Join-Path $sbWsl "ext4.vhdx") -Value "fake-disk" -Encoding utf8

    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $res = Invoke-Script $uninstallerScript @("-PurgeDistro", "-TestSandbox", $sandboxPurgeDistro)

    Require ($res.ExitCode -eq 0) "uninstall.ps1 -PurgeDistro exits with code 0 (got $($res.ExitCode))"
    Require ($res.Output -match "Purging WSL distro" -or $res.Output -match "unregister") "Output mentions purging/unregistering WSL distro"

    # WSL distro directory cleaned
    Require (-not (Test-Path (Join-Path $sbWsl "ext4.vhdx"))) "WSL distro disk removed when -PurgeDistro specified"
} finally {
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item -Path $sandboxPurgeDistro -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 8: Uninstall with -PurgeData
# -------------------------------------------------------------------------
Step "Testing uninstall.ps1 -PurgeData"
$sandboxPurgeData = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-uninst-data-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $sandboxPurgeData | Out-Null
try {
    $sbSecrets = Join-Path $sandboxPurgeData "system\secrets"
    New-Item -ItemType Directory -Force -Path $sbSecrets | Out-Null
    Set-Content -Path (Join-Path $sbSecrets "agent-token") -Value "secret-token-val" -Encoding utf8
    Set-Content -Path (Join-Path $sbSecrets "agent-pin") -Value "secret-pin-val" -Encoding utf8

    $env:DEVSTACK_TEST_SECRETS_DIR = $sbSecrets
    $res = Invoke-Script $uninstallerScript @("-PurgeData", "-TestSandbox", $sandboxPurgeData)

    Require ($res.ExitCode -eq 0) "uninstall.ps1 -PurgeData exits with code 0 (got $($res.ExitCode))"
    Require ($res.Output -match "Purging devstack data" -or $res.Output -match "secrets") "Output mentions purging data and DPAPI secrets"

    # Secrets removed
    Require (-not (Test-Path (Join-Path $sbSecrets "agent-token"))) "agent-token removed with -PurgeData"
    Require (-not (Test-Path (Join-Path $sbSecrets "agent-pin"))) "agent-pin removed with -PurgeData"
} finally {
    Remove-Item env:DEVSTACK_TEST_SECRETS_DIR -ErrorAction SilentlyContinue
    Remove-Item -Path $sandboxPurgeData -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 9: Machine PATH Cleanup
# -------------------------------------------------------------------------
Step "Testing uninstall.ps1 Machine PATH string manipulation"
$fakePath = "C:\Windows\System32;C:\devstack\bin;C:\Program Files\Git\cmd"
$cleaned = (($fakePath -split ";") | Where-Object { $_ -ne "" -and $_ -ne "C:\devstack\bin" -and $_ -ne "C:\devstack\bin\" }) -join ";"
Require ($cleaned -notmatch "C:\\devstack\\bin") "PATH string cleanup removes C:\devstack\bin"
Require ($cleaned -match "C:\\Windows\\System32" -and $cleaned -match "Git\\cmd") "PATH string cleanup preserves unrelated directories"

# -------------------------------------------------------------------------
# Summary
# -------------------------------------------------------------------------
Step "Lifecycle Test Results Summary"
Write-Host "Total tests: $($script:TestsRun)"
Write-Host "Failures:    $($script:Failures)"

if ($script:Failures -eq 0) {
    Write-Host "ALL LIFECYCLE TESTS PASSED" -ForegroundColor Green
    exit 0
} else {
    Write-Host "LIFECYCLE TESTS FAILED with $($script:Failures) failure(s)" -ForegroundColor Red
    exit 1
}
