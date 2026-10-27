# Windows Installer Unit & Integration Test Suite
# Tests Task G1 requirements: elevation, OS build, virtualization,
# layout creation, DACL security, firewall rule building, parameter handling,
# dry-run mode, and install-manifest.json.

param(
    [switch]$VerboseOutput
)

$ErrorActionPreference = "Stop"
$script:Failures = 0
$script:TestsRun = 0

function Step([string]$Message) { Write-Host "`ninstaller-test: === $Message ===" }
function Pass([string]$Message) {
    Write-Host "installer-test: OK: $Message" -ForegroundColor Green
    $script:TestsRun++
}
function Fail([string]$Message) {
    Write-Host "installer-test: FAIL: $Message" -ForegroundColor Red
    $script:Failures++
    $script:TestsRun++
}
function Require([bool]$Condition, [string]$Message) {
    if ($Condition) { Pass $Message } else { Fail $Message }
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$installerScript = Join-Path $repoRoot "install.ps1"
$daemonTaskScript = Join-Path $repoRoot "build\windows\devstack-daemon-task.ps1"
$psEngine = if ($PSVersionTable.PSEdition -eq "Core") { "pwsh" } else { "powershell" }

function Invoke-Installer([string[]]$Arguments) {
    $prev = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = & $psEngine -NoProfile -File $installerScript @Arguments 2>&1 | Out-String
        return @{ ExitCode = $LASTEXITCODE; Output = $output }
    } finally {
        $ErrorActionPreference = $prev
    }
}

# -------------------------------------------------------------------------
# Test 1: install.ps1 existence
# -------------------------------------------------------------------------
Step "Checking installer file existence"
Require (Test-Path $installerScript) "install.ps1 exists at repository root"
if (-not (Test-Path $installerScript)) {
    Write-Host "installer-test: Skipping remaining tests because install.ps1 is missing" -ForegroundColor Yellow
    exit 1
}

# -------------------------------------------------------------------------
# Test 2: Help flag returns 0 and displays usage
# -------------------------------------------------------------------------
Step "Testing parameter handling: -Help"
$res = Invoke-Installer @("-Help")
Require ($res.ExitCode -eq 0) "install.ps1 -Help exits with code 0 (got $($res.ExitCode))"
Require ($res.Output -match "Usage:" -or $res.Output -match "install\.ps1") "install.ps1 -Help displays usage information"

# -------------------------------------------------------------------------
# Test 3: Elevation check
# -------------------------------------------------------------------------
Step "Testing elevation check logic"
# Simulate non-elevated environment via test hook
$env:DEVSTACK_TEST_ELEVATED = "false"
try {
    $res = Invoke-Installer @()
    Require ($res.ExitCode -eq 2) "Non-elevated execution exits with code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "elevation" -or $res.Output -match "Administrator") "Non-elevated output mentions elevation/administrator"
} finally {
    Remove-Item env:DEVSTACK_TEST_ELEVATED -ErrorAction SilentlyContinue
}

# In dry-run or sandbox test mode, elevation check should not terminate with exit code 2
$tempTestDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-elev-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempTestDir | Out-Null
try {
    $env:DEVSTACK_TEST_ELEVATED = "false"
    $res = Invoke-Installer @("-DryRun", "-TestSandbox", $tempTestDir)
    Require ($res.ExitCode -eq 0) "DryRun/TestSandbox allows non-elevated execution (got $($res.ExitCode))"
} finally {
    Remove-Item env:DEVSTACK_TEST_ELEVATED -ErrorAction SilentlyContinue
    Remove-Item -Path $tempTestDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 4: OS build and architecture validation
# -------------------------------------------------------------------------
Step "Testing OS build and architecture validation"
# Test build < 19045 rejected with exit code 2
$env:DEVSTACK_TEST_OS_BUILD = "19044"
$tempTestDir2 = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-build-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempTestDir2 | Out-Null
try {
    $res = Invoke-Installer @("-DryRun", "-TestSandbox", $tempTestDir2)
    Require ($res.ExitCode -eq 2) "OS Build 19044 (< 19045) rejected with exit code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "19045") "Rejection message specifies required build 19045"
} finally {
    Remove-Item env:DEVSTACK_TEST_OS_BUILD -ErrorAction SilentlyContinue
    Remove-Item -Path $tempTestDir2 -Recurse -Force -ErrorAction SilentlyContinue
}

# Test build >= 19045 accepted (e.g. 19045, 22631)
$env:DEVSTACK_TEST_OS_BUILD = "19045"
$tempTestDir3 = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-build-ok-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempTestDir3 | Out-Null
try {
    $res = Invoke-Installer @("-DryRun", "-TestSandbox", $tempTestDir3)
    Require ($res.ExitCode -eq 0) "OS Build 19045 accepted with exit code 0 (got $($res.ExitCode))"
} finally {
    Remove-Item env:DEVSTACK_TEST_OS_BUILD -ErrorAction SilentlyContinue
    Remove-Item -Path $tempTestDir3 -Recurse -Force -ErrorAction SilentlyContinue
}

# Test non-x64 architecture rejected with exit code 2
$env:DEVSTACK_TEST_OS_ARCH = "x86"
$tempTestDirArch = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-arch-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempTestDirArch | Out-Null
try {
    $res = Invoke-Installer @("-DryRun", "-TestSandbox", $tempTestDirArch)
    Require ($res.ExitCode -eq 2) "Non-x64 architecture rejected with exit code 2 (got $($res.ExitCode))"
} finally {
    Remove-Item env:DEVSTACK_TEST_OS_ARCH -ErrorAction SilentlyContinue
    Remove-Item -Path $tempTestDirArch -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 5: Hardware virtualization check
# -------------------------------------------------------------------------
Step "Testing hardware virtualization check"
$env:DEVSTACK_TEST_VIRTUALIZATION = "false"
$tempTestDirVirt = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-virt-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempTestDirVirt | Out-Null
try {
    $res = Invoke-Installer @("-TestSandbox", $tempTestDirVirt)
    Require ($res.ExitCode -eq 2) "Disabled virtualization returns exit code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "virtualization" -or $res.Output -match "firmware") "Message indicates virtualization is disabled in firmware"
} finally {
    Remove-Item env:DEVSTACK_TEST_VIRTUALIZATION -ErrorAction SilentlyContinue
    Remove-Item -Path $tempTestDirVirt -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 6: Directory layout creation and protected DACL
# -------------------------------------------------------------------------
Step "Testing directory layout creation and DACL permissions"
$tempSandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-layout-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempSandbox | Out-Null
try {
    $res = Invoke-Installer @("-TestSandbox", $tempSandbox, "-SkipWSL")
    Require ($res.ExitCode -eq 0) "Installer completes layout creation in TestSandbox with exit code 0 (got $($res.ExitCode))"

    $binDir = Join-Path $tempSandbox "bin"
    $systemDir = Join-Path $tempSandbox "system"
    $wslDir = Join-Path $tempSandbox "wsl\devstack"

    Require (Test-Path $binDir -PathType Container) "bin directory created: $binDir"
    Require (Test-Path $systemDir -PathType Container) "system directory created: $systemDir"
    Require (Test-Path $wslDir -PathType Container) "wsl\devstack directory created: $wslDir"

    # Verify protected DACL on system directory
    $acl = Get-Acl -Path $systemDir
    Require ($acl.AreAccessRulesProtected -eq $true) "system directory DACL has inheritance protected/disabled"

    $hasSystemFull = $false
    $hasAdminFull = $false
    $hasUsersRead = $false

    foreach ($rule in $acl.Access) {
        $identity = $rule.IdentityReference.Value
        $rights = $rule.FileSystemRights.ToString()
        $controlType = $rule.AccessControlType

        if ($controlType -eq [Security.AccessControl.AccessControlType]::Allow) {
            if ($identity -match "SYSTEM" -and $rights -match "FullControl") {
                $hasSystemFull = $true
            }
            if (($identity -match "Administrators" -or $identity -match "BUILTIN\\Administrators") -and $rights -match "FullControl") {
                $hasAdminFull = $true
            }
            if (($identity -match "Users" -or $identity -match "BUILTIN\\Users") -and ($rights -match "Read" -or $rights -match "ReadAndExecute")) {
                $hasUsersRead = $true
            }
        }
    }

    Require $hasSystemFull "system directory grants SYSTEM FullControl"
    Require $hasAdminFull "system directory grants Administrators FullControl"
    Require $hasUsersRead "system directory grants Users ReadAndExecute"

    # Verify install-manifest.json exists in system directory
    $manifestPath = Join-Path $systemDir "install-manifest.json"
    Require (Test-Path $manifestPath -PathType Leaf) "install-manifest.json created at $manifestPath"
    if (Test-Path $manifestPath) {
        $manifest = Get-Content -Raw -Path $manifestPath | ConvertFrom-Json
        Require ($manifest.os_build -ge 19045) "Manifest records os_build ($($manifest.os_build))"
        Require ($manifest.os_arch -match "x64|AMD64") "Manifest records os_arch ($($manifest.os_arch))"
        Require ($manifest.layout_created -eq $true) "Manifest records layout_created: true"
    }
} finally {
    Remove-Item -Path $tempSandbox -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 7: Reboot required handling (exit code 3010)
# -------------------------------------------------------------------------
Step "Testing reboot requirement handling"
$tempRebootSandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-reboot-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempRebootSandbox | Out-Null
try {
    $env:DEVSTACK_TEST_REBOOT_REQUIRED = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempRebootSandbox)
    Require ($res.ExitCode -eq 3010) "Reboot requirement without -AllowReboot exits with 3010 (got $($res.ExitCode))"

    $manifestPath = Join-Path $tempRebootSandbox "system\install-manifest.json"
    Require (Test-Path $manifestPath) "Manifest saved before reboot exit"
    if (Test-Path $manifestPath) {
        $manifest = Get-Content -Raw -Path $manifestPath | ConvertFrom-Json
        Require ($manifest.reboot_required -eq $true) "Manifest records reboot_required: true"
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_REBOOT_REQUIRED -ErrorAction SilentlyContinue
    Remove-Item -Path $tempRebootSandbox -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 8: Firewall rule specification & building
# -------------------------------------------------------------------------
Step "Testing firewall rule parameters"
# Test by running with -DryRun
$env:DEVSTACK_TEST_FIREWALL_CHECK = "true"
$tempFwSandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-fw-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempFwSandbox | Out-Null
try {
    $res = Invoke-Installer @("-DryRun", "-TestSandbox", $tempFwSandbox)
    Require ($res.Output -match "devstack-daemon-proxy") "Firewall rule name is 'devstack-daemon-proxy'"
    Require ($res.Output -match "8080" -and $res.Output -match "8443") "Firewall ports include 8080 and 8443"
    Require ($res.Output -match "TCP" -or $res.Output -match "tcp") "Firewall rule uses TCP protocol"
} finally {
    Remove-Item env:DEVSTACK_TEST_FIREWALL_CHECK -ErrorAction SilentlyContinue
    Remove-Item -Path $tempFwSandbox -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 9: Scheduled task references C:\devstack\bin\devstack.exe
# -------------------------------------------------------------------------
Step "Testing devstack-daemon-task.ps1 references C:\devstack\bin\devstack.exe"
Require (Test-Path $daemonTaskScript) "devstack-daemon-task.ps1 exists"
if (Test-Path $daemonTaskScript) {
    $taskContent = Get-Content -Raw -Path $daemonTaskScript
    Require ($taskContent -match 'C:\\devstack\\bin\\devstack\.exe') "devstack-daemon-task.ps1 references C:\devstack\bin\devstack.exe"
}

# -------------------------------------------------------------------------
# Test Summary
# -------------------------------------------------------------------------
Write-Host "`ninstaller-test: Completed $script:TestsRun checks with $script:Failures failure(s)."
if ($script:Failures -ne 0) {
    Write-Host "installer-test: FAILED" -ForegroundColor Red
    exit 1
}
Write-Host "installer-test: ALL TESTS PASSED" -ForegroundColor Green
exit 0
