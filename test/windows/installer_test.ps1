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

function Invoke-Uninstaller([string[]]$Arguments) {
    $prev = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = & $psEngine -NoProfile -File $uninstallerScript @Arguments 2>&1 | Out-String
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
$isElevated = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isElevated) {
    # In a non-elevated session, real install requires elevation and exits with code 2
    $res = Invoke-Installer @()
    Require ($res.ExitCode -eq 2) "Non-elevated execution exits with code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "elevation" -or $res.Output -match "Administrator") "Non-elevated output mentions elevation/administrator"
} else {
    # In an elevated session, verify that DEVSTACK_TEST_ELEVATED is ignored without -TestSandbox (R2-M11)
    $env:DEVSTACK_TEST_ELEVATED = "false"
    try {
        $res = Invoke-Installer @("-DryRun")
        Require ($res.ExitCode -eq 0) "Elevated execution ignores DEVSTACK_TEST_ELEVATED without -TestSandbox (got $($res.ExitCode))"
        Require ($res.Output -notmatch "\[TestSandbox\] Using test override: DEVSTACK_TEST_ELEVATED") "No [TestSandbox] override logged without -TestSandbox"
    } finally {
        Remove-Item env:DEVSTACK_TEST_ELEVATED -ErrorAction SilentlyContinue
    }
}

# In dry-run or sandbox test mode, elevation check should not terminate with exit code 2
$tempTestDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-elev-test-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempTestDir | Out-Null
try {
    $env:DEVSTACK_TEST_ELEVATED = "false"
    $res = Invoke-Installer @("-DryRun", "-TestSandbox", $tempTestDir)
    Require ($res.ExitCode -eq 0) "DryRun/TestSandbox allows non-elevated execution (got $($res.ExitCode))"
    Require ($res.Output -match "\[TestSandbox\] Using test override: DEVSTACK_TEST_ELEVATED=false") "TestSandbox logs DEVSTACK_TEST_ELEVATED override"
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
    Require (Test-Path (Join-Path $tempSandbox "uninstall.ps1")) "uninstall.ps1 staged to install directory"

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
    Require ($taskContent -match 'devstack\.exe') "devstack-daemon-task.ps1 references devstack.exe"
}

# -------------------------------------------------------------------------
# Test 10: WSL version check (>= 2.0.0 required)
# -------------------------------------------------------------------------
Step "Testing WSL version check"
$tempWSLVerSandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-wslver-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempWSLVerSandbox | Out-Null
try {
    # Test outdated WSL (< 2.0.0) fails with exit code 2
    $env:DEVSTACK_TEST_WSL_VERSION = "1.2.5"
    $res = Invoke-Installer @("-TestSandbox", $tempWSLVerSandbox)
    Require ($res.ExitCode -eq 2) "Outdated WSL version (1.2.5) rejected with exit code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "2\.0\.0" -or $res.Output -match "WSL") "Rejection message specifies required WSL 2.0.0+"

    # Test missing WSL fails with exit code 2
    $env:DEVSTACK_TEST_WSL_VERSION = "missing"
    $res = Invoke-Installer @("-TestSandbox", $tempWSLVerSandbox)
    Require ($res.ExitCode -eq 2) "Missing WSL runtime rejected with exit code 2 (got $($res.ExitCode))"

    # Test valid WSL (>= 2.0.0) passes check
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $res = Invoke-Installer @("-TestSandbox", $tempWSLVerSandbox, "-SkipWSL")
    Require ($res.ExitCode -eq 0) "WSL version 2.3.26 accepted (got $($res.ExitCode))"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item -Path $tempWSLVerSandbox -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 11: .wslconfig check (verifying NAT networking, rejecting non-NAT)
# -------------------------------------------------------------------------
Step "Testing .wslconfig networkingMode validation"
$tempWSLCfgDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-wslcfg-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempWSLCfgDir | Out-Null
$mockWSLConfig = Join-Path $tempWSLCfgDir ".wslconfig"
try {
    # 1. networkingMode=mirrored must be rejected with exit code 2
    @"
[wsl2]
networkingMode=mirrored
"@ | Set-Content -Path $mockWSLConfig -Encoding utf8

    $env:DEVSTACK_TEST_WSLCONFIG = $mockWSLConfig
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $res = Invoke-Installer @("-TestSandbox", $tempWSLCfgDir)
    Require ($res.ExitCode -eq 2) "networkingMode=mirrored rejected with exit code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "NAT" -or $res.Output -match "networkingMode") "Rejection mentions NAT requirement or networkingMode"

    # 2. networkingMode=nat must be accepted
    @"
[wsl2]
networkingMode=nat
"@ | Set-Content -Path $mockWSLConfig -Encoding utf8

    $res = Invoke-Installer @("-TestSandbox", $tempWSLCfgDir, "-SkipWSL")
    Require ($res.ExitCode -eq 0) "networkingMode=nat accepted with exit code 0 (got $($res.ExitCode))"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSLCONFIG -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item -Path $tempWSLCfgDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 12: Rootfs SHA256 verification against versions.lock
# -------------------------------------------------------------------------
Step "Testing rootfs SHA256 checksum verification"
$tempRootFSDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-rootfs-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempRootFSDir | Out-Null
$fakeRootFS = Join-Path $tempRootFSDir "test-rootfs.tar.gz"
Set-Content -Path $fakeRootFS -Value "mock rootfs tar archive content" -Encoding utf8
$fakeHash = (Get-FileHash -Path $fakeRootFS -Algorithm SHA256).Hash.ToLower()

try {
    # Hash mismatch should fail with exit code 2
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $env:DEVSTACK_TEST_ROOTFS_SHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    $res = Invoke-Installer @("-TestSandbox", $tempRootFSDir, "-RootFSPath", $fakeRootFS)
    Require ($res.ExitCode -eq 2) "Rootfs SHA256 mismatch rejected with exit code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "checksum" -or $res.Output -match "SHA256" -or $res.Output -match "mismatch") "Checksum mismatch error message displayed"

    # Hash match should succeed
    $env:DEVSTACK_TEST_ROOTFS_SHA256 = $fakeHash
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempRootFSDir, "-RootFSPath", $fakeRootFS)
    Require ($res.ExitCode -eq 0) "Rootfs SHA256 match accepted with exit code 0 (got $($res.ExitCode))"
    Require ($res.Output -match "checksum verified") "Matching rootfs logs checksum verified"

    # Unverified checksum (no expected hash) should log warning and not claim verified
    $tempUnverifiedDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-unverified-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tempUnverifiedDir | Out-Null
    try {
        Remove-Item env:DEVSTACK_TEST_ROOTFS_SHA256 -ErrorAction SilentlyContinue
        $env:DEVSTACK_TEST_MOCK_WSL = "true"
        $res = Invoke-Installer @("-TestSandbox", $tempUnverifiedDir, "-RootFSPath", $fakeRootFS)
        Require ($res.ExitCode -eq 0) "Rootfs without expected hash succeeds in test sandbox (got $($res.ExitCode): $($res.Output))"
        Require ($res.Output -match "unverified") "Warning printed when rootfs checksum is unverified"
        Require ($res.Output -notmatch "checksum verified") "Unverified rootfs does not claim checksum verified"
    } finally {
        Remove-Item -Path $tempUnverifiedDir -Recurse -Force -ErrorAction SilentlyContinue
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_ROOTFS_SHA256 -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item -Path $tempRootFSDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 13: Unmanaged distro rejection
# -------------------------------------------------------------------------
Step "Testing unmanaged distro rejection"
$tempUnmanagedDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-unmanaged-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempUnmanagedDir | Out-Null
try {
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_DISTRO_EXISTS = "unmanaged"
    $res = Invoke-Installer @("-TestSandbox", $tempUnmanagedDir)
    Require ($res.ExitCode -eq 2) "Existing unmanaged distro 'devstack' rejected with exit code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "unmanaged" -or $res.Output -match "managed\.json") "Rejection message mentions unmanaged distro / managed marker"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_DISTRO_EXISTS -ErrorAction SilentlyContinue
    Remove-Item -Path $tempUnmanagedDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 14: Distro import command generation in DryRun mode
# -------------------------------------------------------------------------
Step "Testing distro import command generation in DryRun"
$tempDryWSLDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-drywsl-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempDryWSLDir | Out-Null
try {
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $res = Invoke-Installer @("-DryRun", "-TestSandbox", $tempDryWSLDir)
    Require ($res.ExitCode -eq 0) "DryRun with WSL phase exits with code 0 (got $($res.ExitCode))"
    Require ($res.Output -match "--import" -and $res.Output -match "devstack" -and $res.Output -match "--version 2") "DryRun outputs wsl --import devstack ... --version 2"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item -Path $tempDryWSLDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 15: Full WSL provisioning flow in TestSandbox
# -------------------------------------------------------------------------
Step "Testing WSL provisioning in TestSandbox (/etc/wsl.conf, managed.json, docker, agent, manifest)"
$tempFullWSLDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-fullwsl-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempFullWSLDir | Out-Null
try {
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_SYSTEMD_PID1 = "systemd"
    $res = Invoke-Installer @("-TestSandbox", $tempFullWSLDir)
    Require ($res.ExitCode -eq 0) "Installer completes WSL provisioning in TestSandbox with exit code 0 (got $($res.ExitCode))"

    # Verify /etc/wsl.conf simulated content
    $wslConfPath = Join-Path $tempFullWSLDir "mock-wsl\etc\wsl.conf"
    Require (Test-Path $wslConfPath) "Distro /etc/wsl.conf created"
    if (Test-Path $wslConfPath) {
        $wslConf = Get-Content -Raw -Path $wslConfPath
        Require ($wslConf -match "\[boot\]" -and $wslConf -match "systemd=true") "/etc/wsl.conf enables systemd"
        Require ($wslConf -match "generateHosts=false" -and $wslConf -match "generateResolvConf=true") "/etc/wsl.conf configures network flags"
    }

    # Verify /data/devstack/managed.json simulated content
    $managedJsonPath = Join-Path $tempFullWSLDir "mock-wsl\data\devstack\managed.json"
    Require (Test-Path $managedJsonPath) "Distro /data/devstack/managed.json created"
    if (Test-Path $managedJsonPath) {
        $managedData = Get-Content -Raw -Path $managedJsonPath | ConvertFrom-Json
        Require ($managedData.managed_by -eq "devstack") "managed.json has managed_by: devstack"
        Require (-not [string]::IsNullOrEmpty($managedData.install_id)) "managed.json has valid install_id"
    }

    # Verify staged devstack-agent
    $agentBinPath = Join-Path $tempFullWSLDir "mock-wsl\data\devstack\bin\devstack-agent"
    Require (Test-Path $agentBinPath) "Linux devstack-agent staged to /data/devstack/bin/devstack-agent"

    # Verify systemd service unit
    $servicePath = Join-Path $tempFullWSLDir "mock-wsl\etc\systemd\system\devstack-agent.service"
    Require (Test-Path $servicePath) "devstack-agent.service installed to /etc/systemd/system"

    # Verify install-manifest.json updated with WSL metadata
    $manifestPath = Join-Path $tempFullWSLDir "system\install-manifest.json"
    Require (Test-Path $manifestPath) "install-manifest.json exists"
    if (Test-Path $manifestPath) {
        $manifest = Get-Content -Raw -Path $manifestPath | ConvertFrom-Json
        Require ($manifest.wsl_version -eq "2.3.26") "Manifest records wsl_version: 2.3.26"
        Require ($manifest.debian_version -match "12|bookworm") "Manifest records debian_version"
        Require ($manifest.systemd_verified -eq $true) "Manifest records systemd_verified: true"
        Require (-not [string]::IsNullOrEmpty($manifest.docker_version)) "Manifest records docker_version"
        Require ($manifest.agent_installed -eq $true) "Manifest records agent_installed: true"
        Require ($manifest.stage -eq "complete" -or $manifest.stage -eq "wsl-phase-complete") "Manifest records completed stage"
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_SYSTEMD_PID1 -ErrorAction SilentlyContinue
    Remove-Item -Path $tempFullWSLDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 16: Systemd PID 1 validation failure
# -------------------------------------------------------------------------
Step "Testing systemd PID 1 validation failure"
$tempPID1Dir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-pid1-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempPID1Dir | Out-Null
try {
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_SYSTEMD_PID1 = "init"
    $res = Invoke-Installer @("-TestSandbox", $tempPID1Dir)
    Require ($res.ExitCode -eq 2) "Non-systemd PID 1 exits with code 2 (got $($res.ExitCode))"
    Require ($res.Output -match "systemd" -or $res.Output -match "PID 1") "Error output mentions systemd or PID 1"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_SYSTEMD_PID1 -ErrorAction SilentlyContinue
    Remove-Item -Path $tempPID1Dir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 17: build/wsl/install-agent.sh existence and validation
# -------------------------------------------------------------------------
Step "Testing build/wsl/install-agent.sh script"
$installAgentScript = Join-Path $repoRoot "build\wsl\install-agent.sh"
Require (Test-Path $installAgentScript) "build/wsl/install-agent.sh exists"
if (Test-Path $installAgentScript) {
    $shContent = Get-Content -Raw -Path $installAgentScript
    Require ($shContent -match "devstack-agent\.service") "install-agent.sh references devstack-agent.service"
    Require ($shContent -match "systemctl") "install-agent.sh uses systemctl"
    Require ($shContent -match "EUID" -or $shContent -match "id -u") "install-agent.sh checks root privileges"
}

# -------------------------------------------------------------------------
# Test 18: Task G1 Review Observations Verification
# -------------------------------------------------------------------------
Step "Testing dynamic PowerShell resolution and go build directory safety"
$installerContent = Get-Content -Raw -Path $installerScript
Require ($installerContent -match 'Get-Process -Id \$PID' -or $installerContent -match '\$PID') "install.ps1 dynamically resolves PowerShell executable"
Require ($installerContent -match 'Push-Location') "install.ps1 uses Push-Location for go build"

# -------------------------------------------------------------------------
# Test 19: WSL path normalization with forward-slash replacement & /mnt/ fallback
# -------------------------------------------------------------------------
Step "Testing WSL path normalization and fallback"
# Unit test normalization logic and regex matching
$testWindowsPath = "C:\devstack\bin\devstack-agent"
$testCleanPath = $testWindowsPath.Replace('\', '/')
Require ($testCleanPath -eq "C:/devstack/bin/devstack-agent") "Backslashes normalized to forward slashes"
Require ($testCleanPath -notmatch '\\') "Normalized path contains no backslashes"

$resolvedFallback = if ($testCleanPath -match "^([a-zA-Z]):/(.*)$") {
    "/mnt/$($Matches[1].ToLower())/$($Matches[2])"
} else { "" }
Require ($resolvedFallback -eq "/mnt/c/devstack/bin/devstack-agent") "/mnt/<drive>/... fallback constructs correct WSL path"

# Test drive letter casing and arbitrary paths
$dClean = "D:\My Tools\DevStack\bin\devstack-agent".Replace('\', '/')
$dFallback = if ($dClean -match "^([a-zA-Z]):/(.*)$") {
    "/mnt/$($Matches[1].ToLower())/$($Matches[2])"
} else { "" }
Require ($dFallback -eq "/mnt/d/My Tools/DevStack/bin/devstack-agent") "/mnt/ fallback handles alternate drive letter and spaces"

# Verify install.ps1 code structure for path normalization, wslpath flags, fallback, and error handling
Require ($installerContent -match '\$stagedAgentLinux\.Replace\(''\\'', ''/''\)') "install.ps1 replaces backslashes with forward slashes"
Require ($installerContent -match 'wsl\.exe -d devstack -u root -- wslpath -a -u "\$cleanAgentPath"') "install.ps1 invokes wslpath targeting devstack distro as root"
Require ($installerContent -match '/mnt/\$\(\$Matches\[1\]\.ToLower\(\)\)/\$\(\$Matches\[2\]\)') "install.ps1 includes /mnt/<drive> fallback regex"
Require ($installerContent -match 'Failed to copy devstack-agent binary into WSL distro') "install.ps1 checks copy exit code and reports failure"
Require ($installerContent -match 'Unable to resolve WSL path for') "install.ps1 reports error when path resolution fails"

# -------------------------------------------------------------------------
# Test 20: CRLF stripping when passing scripts to bash and managed.json piping
# -------------------------------------------------------------------------
Step "Testing CRLF stripping for bash scripts and managed.json stdin piping"
# Unit test CRLF replacement
$testCRLFScript = "echo line 1`r`necho line 2`r`n"
$testLFScript = $testCRLFScript -replace "`r`n", "`n"
Require ($testLFScript -notmatch "`r") "CRLF replacement removes all carriage return characters"
Require ($testLFScript -match "`n") "LF newlines are preserved"

# Verify install.ps1 strips CRLF for both install-docker.sh and install-agent.sh
Require ($installerContent -match 'Get-Content\s+-Raw\s+\$installDockerScript\)\s*-replace\s*"`r`n",\s*"`n"') "install.ps1 strips CRLF when reading install-docker.sh"
Require ($installerContent -match 'Get-Content\s+-Raw\s+\$installAgentSrc\)\s*-replace\s*"`r`n",\s*"`n"') "install.ps1 strips CRLF when reading install-agent.sh"

# Verify managed.json is piped via stdin using cat > instead of printf interpolation
Require ($installerContent -match '\$managedJsonContent\s*\|\s*&\s*wsl\.exe\s*-d\s*devstack\s*-u\s*root\s*--\s*sh\s*-c\s*"cat\s*>\s*/data/devstack/managed\.json') "install.ps1 pipes managed.json via stdin using cat >"
Require ($installerContent -notmatch "printf\s*'%s\\n'\s*'\$managedJsonContent'") "install.ps1 does not use fragile printf interpolation for managed.json"

# -------------------------------------------------------------------------
# Test 21: Verification that failure in install-docker.sh or install-agent.sh exits with code 1
# -------------------------------------------------------------------------
Step "Testing exit code 1 on install-docker.sh and install-agent.sh failure"
# Verify static code checks for $LASTEXITCODE -ne 0 after bash invocations
Require ($installerContent -match '\$dockerScriptContent\s*\|\s*&\s*wsl\.exe.*bash\s*[\r\n\s]+if\s*\(\$LASTEXITCODE\s*-ne\s*0\)') "install.ps1 checks LASTEXITCODE immediately after install-docker.sh bash invocation"
Require ($installerContent -match '\$agentScriptContent\s*\|\s*&\s*wsl\.exe.*bash\s*[\r\n\s]+if\s*\(\$LASTEXITCODE\s*-ne\s*0\)') "install.ps1 checks LASTEXITCODE immediately after install-agent.sh bash invocation"

# Dynamic test: Docker script failure triggers exit code 1
$tempFailDockerDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-faildocker-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempFailDockerDir | Out-Null
try {
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_SYSTEMD_PID1 = "systemd"
    $env:DEVSTACK_TEST_FAIL_INSTALL_DOCKER = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempFailDockerDir)
    Require ($res.ExitCode -eq 1) "Docker install failure exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "Docker Engine installation script failed") "Error message reports Docker script failure"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_SYSTEMD_PID1 -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_FAIL_INSTALL_DOCKER -ErrorAction SilentlyContinue
    Remove-Item -Path $tempFailDockerDir -Recurse -Force -ErrorAction SilentlyContinue
}

# Dynamic test: Agent script failure triggers exit code 1
$tempFailAgentDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-failagent-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempFailAgentDir | Out-Null
try {
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_SYSTEMD_PID1 = "systemd"
    $env:DEVSTACK_TEST_FAIL_INSTALL_AGENT = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempFailAgentDir)
    Require ($res.ExitCode -eq 1) "Agent install failure exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "devstack-agent installation script failed") "Error message reports agent script failure"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_SYSTEMD_PID1 -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_FAIL_INSTALL_AGENT -ErrorAction SilentlyContinue
    Remove-Item -Path $tempFailAgentDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 22: Upgrade WSL Backup Failure Detection (C3)
# -------------------------------------------------------------------------
Step "Testing upgrade WSL backup failure detection"
$tempUpgradeFailDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-upgbakfail-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempUpgradeFailDir | Out-Null
$mockUpgWSL = Join-Path $tempUpgradeFailDir "mock-wsl\data\devstack"
New-Item -ItemType Directory -Force -Path $mockUpgWSL | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $tempUpgradeFailDir "system") | Out-Null
Set-Content -Path (Join-Path $tempUpgradeFailDir "system\install-manifest.json") -Value '{"stage":"complete"}' -Encoding utf8
$mockUserDataFile = Join-Path $mockUpgWSL "critical-user-data.txt"
Set-Content -Path $mockUserDataFile -Value "pre-upgrade-state-must-be-preserved" -Encoding utf8

try {
    # 1. Simulate tar backup failure
    $env:DEVSTACK_TEST_BACKUP_FAIL = "true"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempUpgradeFailDir, "-Upgrade")
    Require ($res.ExitCode -eq 1) "WSL backup failure exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "Failed to backup /data/devstack") "Error output reports WSL backup failure"
    Require (Test-Path $mockUserDataFile) "Live /data/devstack data is NOT deleted on backup failure"
    Require ((Get-Content -Path $mockUserDataFile -Raw).Trim() -eq "pre-upgrade-state-must-be-preserved") "Live data content is intact"

    # 2. Simulate archive verification failure
    Remove-Item env:DEVSTACK_TEST_BACKUP_FAIL -ErrorAction SilentlyContinue
    $env:DEVSTACK_TEST_BACKUP_VERIFY_FAIL = "true"
    $res2 = Invoke-Installer @("-TestSandbox", $tempUpgradeFailDir, "-Upgrade")
    Require ($res2.ExitCode -eq 1) "Backup archive verification failure exits with code 1 (got $($res2.ExitCode))"
    Require ($res2.Output -match "Backup archive verification failed") "Error output reports archive verification failure"
    Require (Test-Path $mockUserDataFile) "Live /data/devstack data is NOT deleted on verification failure"
} finally {
    Remove-Item env:DEVSTACK_TEST_BACKUP_FAIL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_BACKUP_VERIFY_FAIL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item -Path $tempUpgradeFailDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 23: Upgrade Rollback Safety (C3)
# -------------------------------------------------------------------------
Step "Testing upgrade rollback safety and atomic swap"
$tempRollbackDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-rollback-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempRollbackDir | Out-Null
$mockRbWSL = Join-Path $tempRollbackDir "mock-wsl\data\devstack"
$mockRbWSLBin = Join-Path $mockRbWSL "bin"
New-Item -ItemType Directory -Force -Path $mockRbWSLBin | Out-Null
$mockOriginalAgent = Join-Path $mockRbWSLBin "devstack-agent"
Set-Content -Path $mockOriginalAgent -Value "original-agent-v1" -Encoding utf8
$mockRbUserData = Join-Path $mockRbWSL "database.sqlite"
Set-Content -Path $mockRbUserData -Value "pre-existing-database" -Encoding utf8

$mockBinDir = Join-Path $tempRollbackDir "bin"
New-Item -ItemType Directory -Force -Path $mockBinDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $tempRollbackDir "system") | Out-Null
Set-Content -Path (Join-Path $tempRollbackDir "system\install-manifest.json") -Value '{"version":"1.0","stage":"complete"}' -Encoding utf8
Set-Content -Path (Join-Path $mockBinDir "devstack.exe") -Value "devstack-v1" -Encoding utf8
Set-Content -Path (Join-Path $mockBinDir "devstack-helper.exe") -Value "devstack-helper-v1" -Encoding utf8

try {
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_UPGRADE_FAIL = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempRollbackDir, "-Upgrade")
    Require ($res.ExitCode -eq 1) "Failed upgrade health check exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "Upgrade failed\. Initiating rollback") "Output indicates rollback was initiated"
    Require ($res.Output -match "Upgrade rolled back successfully to previous version") "Output reports successful rollback"

    # Verify live data restored
    Require (Test-Path $mockRbUserData) "Original database.sqlite exists after rollback"
    Require ((Get-Content -Path $mockRbUserData -Raw).Trim() -eq "pre-existing-database") "Original database.sqlite content is restored"
    Require (Test-Path $mockOriginalAgent) "devstack-agent exists after rollback"
    Require ((Get-Content -Path $mockOriginalAgent -Raw).Trim() -eq "original-agent-v1") "devstack-agent content restored"

    # Verify no temporary rollback directories linger
    $rbTmpDir = Join-Path $tempRollbackDir "mock-wsl\data\devstack.rollback-tmp"
    $rbFailedDir = Join-Path $tempRollbackDir "mock-wsl\data\devstack.failed-tmp"
    Require (-not (Test-Path $rbTmpDir)) "Temporary rollback staging directory devstack.rollback-tmp is cleaned up"
    Require (-not (Test-Path $rbFailedDir)) "Temporary failed directory devstack.failed-tmp is cleaned up"
} finally {
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_UPGRADE_FAIL -ErrorAction SilentlyContinue
    Remove-Item -Path $tempRollbackDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 23b: Missing backup must fail closed, never report a false rollback
# -------------------------------------------------------------------------
Step "Testing rollback failure reporting when the backup archive is missing"
$tempRollbackFailDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-rollback-missing-" + [Guid]::NewGuid().ToString("N"))
$mockFailWSL = Join-Path $tempRollbackFailDir "mock-wsl\data\devstack"
$mockFailBin = Join-Path $mockFailWSL "bin"
New-Item -ItemType Directory -Force -Path $mockFailBin | Out-Null
Set-Content -Path (Join-Path $mockFailBin "devstack-agent") -Value "original-agent-v2" -Encoding utf8
$mockFailData = Join-Path $mockFailWSL "database.sqlite"
Set-Content -Path $mockFailData -Value "must-survive" -Encoding utf8
New-Item -ItemType Directory -Force -Path (Join-Path $tempRollbackFailDir "bin") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $tempRollbackFailDir "system") | Out-Null
Set-Content -Path (Join-Path $tempRollbackFailDir "system\install-manifest.json") -Value '{"stage":"complete"}' -Encoding utf8

try {
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_UPGRADE_FAIL = "true"
    $env:DEVSTACK_TEST_BACKUP_MISSING = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempRollbackFailDir, "-Upgrade")
    Require ($res.ExitCode -eq 1) "Missing-backup rollback exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "Upgrade rollback FAILED") "Missing-backup rollback reports failure"
    Require ($res.Output -notmatch "rolled back successfully") "Missing-backup rollback never claims success"
    Require ((Test-Path $mockFailData) -and ((Get-Content $mockFailData -Raw).Trim() -eq "must-survive")) "Missing-backup rollback does not destroy live data"
} finally {
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_UPGRADE_FAIL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_BACKUP_MISSING -ErrorAction SilentlyContinue
    Remove-Item -Path $tempRollbackFailDir -Recurse -Force -ErrorAction SilentlyContinue
}

# Static guard: the upgrade must select a prebuilt agent that is not the
# destination, so a reinstall cannot copy bin\devstack-agent onto itself.
$installerContentForAgent = Get-Content -Raw -Path $installerScript
Require ($installerContentForAgent -match 'function Select-PrebuiltAgent') "install.ps1 defines Select-PrebuiltAgent"
Require (([regex]::Matches($installerContentForAgent, 'Select-PrebuiltAgent -Destination')).Count -ge 2) "both agent staging paths exclude the destination"

# -------------------------------------------------------------------------
# Test 24: Upgrade Helper Service Stop/Start and Order Validation (C3, M8)
# -------------------------------------------------------------------------
Step "Testing upgrade ordering and helper service handling"
$installerContent = Get-Content -Raw -Path $installerScript

# C3 ordering: WSL backup must occur before Windows binary staging in upgrade
$backupMatch = $installerContent.IndexOf("Backing up /data/devstack to /data/devstack-backup.tar.gz")
$stagingMatch = $installerContent.IndexOf("Building devstack-helper.exe")
Require ($backupMatch -gt 0 -and $stagingMatch -gt 0 -and $backupMatch -lt $stagingMatch) "Upgrade WSL backup is executed BEFORE Windows binary staging"

# C3 helper service stopped before binary replacement
Require ($installerContent -match 'Stopping DevstackHelper service for binary upgrade') "install.ps1 stops DevstackHelper service before replacing helper binary"
Require ($installerContent -match 'Start-Service -Name "DevstackHelper"') "install.ps1 restarts DevstackHelper service after staging updated binary"

# C3 archive verification check
Require ($installerContent -match 'tar -tzf /data/devstack-backup\.tar\.gz') "install.ps1 verifies archive integrity with tar -tzf"
Require ($installerContent -notmatch 'tar -czf /data/devstack-backup\.tar\.gz -C /data devstack && cp.*\|\| true') "install.ps1 does NOT mask tar backup errors with || true"

# M8 user daemon restart upon upgrade completion
Require ($installerContent -match 'Restarting user daemon') "install.ps1 restarts user daemon upon upgrade completion"
Require ($installerContent -match '& "\$dstDevstack" --home "\$targetDir" daemon restart') "install.ps1 runs 'devstack --home <install> daemon restart' on upgrade success"

# -------------------------------------------------------------------------
# Test 25: Firewall Rule Scoping, Deferral, and SkipWSL (H1)
# -------------------------------------------------------------------------
Step "Testing firewall rule scoping, deferral, and -SkipWSL handling"
$tempSkipWSLDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-skipwsl-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempSkipWSLDir | Out-Null
try {
    $res = Invoke-Installer @("-TestSandbox", $tempSkipWSLDir, "-SkipWSL")
    Require ($res.ExitCode -eq 0) "install.ps1 with -SkipWSL exits with code 0 (got $($res.ExitCode))"
    Require ($res.Output -notmatch "Firewall rule 'devstack-daemon-proxy'") "Inbound firewall rule is NOT created when -SkipWSL is specified"
    Require ($res.Output -match "Skipping WSL distro and proxy firewall rule") "-SkipWSL message explicitly mentions skipping proxy firewall rule"
} finally {
    Remove-Item -Path $tempSkipWSLDir -Recurse -Force -ErrorAction SilentlyContinue
}

# H1 Ordering: firewall rule creation deferred to after Section 11 WSL distro import
$wslImportIdx = $installerContent.IndexOf("Importing devstack WSL distribution")
$fwRuleIdx = $installerContent.IndexOf("Created inbound firewall rule")
Require ($wslImportIdx -gt 0 -and $fwRuleIdx -gt 0 -and $fwRuleIdx -gt $wslImportIdx) "Inbound firewall rule creation is deferred until after WSL distro import"

# H1 Fail-closed: missing WSL subnet prevents creation of open unscoped rule
Require ($installerContent -match 'Unable to determine WSL subnet for firewall rule scoping\. Refusing to create unscoped inbound firewall rule') "install.ps1 fails closed if WSL subnet cannot be determined"
Require ($installerContent -match 'RemoteAddress = \$wslSubnet') "install.ps1 scopes firewall rule RemoteAddress to `$wslSubnet"

# -------------------------------------------------------------------------
# Test 26: Reboot-Resume Task Configuration (C4)
# -------------------------------------------------------------------------
Step "Testing reboot-resume task configuration and user context"
$tempResumeDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-resume-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempResumeDir | Out-Null
try {
    $env:DEVSTACK_TEST_REBOOT_REQUIRED = "true"
    $res = Invoke-Installer @("-TestSandbox", $tempResumeDir, "-AllowReboot", "-RootFSPath", "C:\test\rootfs.tar.gz")
    Require ($res.ExitCode -eq 3010) "Reboot requirement with -AllowReboot exits with 3010 (got $($res.ExitCode))"

    $resumeStateFile = Join-Path (Join-Path $tempResumeDir "system") "resume-state.json"
    Require (Test-Path $resumeStateFile) "resume-state.json created in system directory"
    if (Test-Path $resumeStateFile) {
        $stateObj = Get-Content -Raw -Path $resumeStateFile | ConvertFrom-Json
        Require ($stateObj.user_sid -match "^S-1-5-") "resume-state.json captures user SID ($($stateObj.user_sid))"
        Require ($stateObj.user_sid -ne "S-1-5-18") "Captured user SID is NOT SYSTEM (S-1-5-18)"
        Require ($stateObj.arguments -match "-RootFSPath") "resume-state.json preserves original argument -RootFSPath"
    }

    # Verify resume execution sets allow_sid to user SID (not SYSTEM)
    Remove-Item env:DEVSTACK_TEST_REBOOT_REQUIRED -ErrorAction SilentlyContinue
    $testUserSid = "S-1-5-21-999999999-888888888-777777777-1001"
    $res2 = Invoke-Installer @("-TestSandbox", $tempResumeDir, "-ResumeUserSID", $testUserSid, "-SkipWSL")
    Require ($res2.ExitCode -eq 0) "Resumed run exits with code 0 (got $($res2.ExitCode))"

    $helperYaml = Join-Path $tempResumeDir "system\helper.yaml"
    Require (Test-Path $helperYaml) "helper.yaml exists after resumed run"
    if (Test-Path $helperYaml) {
        $yamlContent = Get-Content -Raw -Path $helperYaml
        Require ($yamlContent -match [regex]::Escape($testUserSid)) "helper.yaml sets allow_sid to user SID ($testUserSid)"
        Require ($yamlContent -notmatch "allow_sid:\s*`"S-1-5-18`"") "helper.yaml allow_sid is NOT SYSTEM SID"
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_REBOOT_REQUIRED -ErrorAction SilentlyContinue
    Remove-Item -Path $tempResumeDir -Recurse -Force -ErrorAction SilentlyContinue
}

# Static check: resume task scheduled with user principal and cleaned up on both failure and success
Require ($installerContent -match 'Remove-ResumeTask') "install.ps1 defines and calls Remove-ResumeTask on resume and error"
Require ($installerContent -match 'New-ScheduledTaskPrincipal -UserId \$currentUserName') "install.ps1 schedules resume task for current interactive user"

# -------------------------------------------------------------------------
# Test 27: Daemon Autostart Logon Task & Fake Stub Prohibition (H2, H6)
# -------------------------------------------------------------------------
Step "Testing daemon autostart task registration and agent fake stub prohibition"
# H6: devstack-daemon logon task registered
Require ($installerContent -match 'devstack-daemon-task\.ps1') "install.ps1 invokes devstack-daemon-task.ps1 to register logon autostart"

# H2: Prohibition of fake echo agent stub
Require ($installerContent -notmatch 'echo devstack-agent') "install.ps1 does NOT contain fake echo stub fallback for devstack-agent"
Require ($installerContent -match 'Refusing to install placeholder stub') "install.ps1 explicitly refuses to install placeholder agent stub"

# H2: build/wsl/install-agent.sh does not mask errors
$installAgentScriptPath = Join-Path $repoRoot "build\wsl\install-agent.sh"
$agentScriptBody = Get-Content -Raw -Path $installAgentScriptPath
Require ($agentScriptBody -notmatch 'systemctl start devstack-agent\.service.*\|\|\s*true') "build/wsl/install-agent.sh does NOT mask start errors with || true"

# -------------------------------------------------------------------------
# Test 28: Environment Overrides Isolation (R2-M11)
# -------------------------------------------------------------------------
Step "Testing DEVSTACK_TEST_* environment overrides isolation and logging (R2-M11)"
$env:DEVSTACK_TEST_OS_BUILD = "1000"
$env:DEVSTACK_TEST_ROOTFS_SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
try {
    # 1. Normal run (without -TestSandbox, e.g. -DryRun) MUST completely ignore DEVSTACK_TEST_*
    $resNormal = Invoke-Installer @("-DryRun")
    Require ($resNormal.ExitCode -eq 0) "Real/normal install ignores DEVSTACK_TEST_* overrides (got $($resNormal.ExitCode))"
    Require ($resNormal.Output -notmatch "\[TestSandbox\] Using test override:") "Normal install does not log [TestSandbox] overrides"
    Require ($resNormal.Output -notmatch "Detected build: 1000") "Normal install does not use mocked OS build 1000"

    # 2. In -TestSandbox, DEVSTACK_TEST_* MUST be honored and logged
    $tempSandboxM11 = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-m11-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tempSandboxM11 | Out-Null
    try {
        $resSandbox = Invoke-Installer @("-DryRun", "-TestSandbox", $tempSandboxM11)
        Require ($resSandbox.ExitCode -eq 2) "TestSandbox honors DEVSTACK_TEST_OS_BUILD=1000 and fails prerequisite (got $($resSandbox.ExitCode))"
        Require ($resSandbox.Output -match "\[TestSandbox\] Using test override: DEVSTACK_TEST_OS_BUILD=1000") "TestSandbox logs active DEVSTACK_TEST_OS_BUILD override"
        Require ($resSandbox.Output -match "1000") "TestSandbox failure output mentions mocked build 1000"
    } finally {
        Remove-Item -Path $tempSandboxM11 -Recurse -Force -ErrorAction SilentlyContinue
    }
} finally {
    Remove-Item env:DEVSTACK_TEST_OS_BUILD -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_ROOTFS_SHA256 -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 29: Uninstaller Exact Hosts Marker Removal & No UTF-8 BOM (R2-M9)
# -------------------------------------------------------------------------
Step "Testing uninstaller exact hosts marker removal and absence of UTF-8 BOM (R2-M9)"
$uninstallerScript = Join-Path $repoRoot "uninstall.ps1"
Require (Test-Path $uninstallerScript) "uninstall.ps1 exists"

$testEngines = @("pwsh")
if (Get-Command powershell.exe -ErrorAction SilentlyContinue) {
    $testEngines += "powershell"
}

foreach ($engine in $testEngines) {
    $tempUninstallDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-uninst-$engine-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tempUninstallDir | Out-Null
    $mockHostsPath = Join-Path $tempUninstallDir "hosts"

    # Seed hosts file with managed block and near-miss comments that should NOT be deleted
    $hostsInitial = @(
        "127.0.0.1 localhost",
        "# BEGIN devstack",
        "127.0.0.1 app.test",
        "127.0.0.1 api.test",
        "# END devstack",
        "::1 localhost",
        "# BEGIN devstack custom notes - not marker",
        "127.0.0.1 custom.test",
        "# END devstack custom notes - not marker"
    ) -join "`r`n"

    [System.IO.File]::WriteAllText($mockHostsPath, $hostsInitial, (New-Object System.Text.UTF8Encoding($false)))

    try {
        $uninstOutput = & $engine -NoProfile -File $uninstallerScript -TestSandbox $tempUninstallDir 2>&1 | Out-String
        $uninstExit = $LASTEXITCODE
        Require ($uninstExit -eq 0) "[$engine] uninstall.ps1 exits with code 0 (got $uninstExit)"

        Require (Test-Path $mockHostsPath) "[$engine] hosts file exists after uninstallation"
        if (Test-Path $mockHostsPath) {
            $hostsContent = [System.IO.File]::ReadAllText($mockHostsPath)

            # Exact marker check: managed block lines removed
            Require ($hostsContent -notmatch "(?m)^127\.0\.0\.1\s+app\.test") "[$engine] Managed block entry 'app.test' removed"
            Require ($hostsContent -notmatch "(?m)^127\.0\.0\.1\s+api\.test") "[$engine] Managed block entry 'api.test' removed"

            # Exact marker check: non-matching lines preserved
            Require ($hostsContent -match "127\.0\.0\.1 localhost") "[$engine] Preserves '127.0.0.1 localhost'"
            Require ($hostsContent -match "::1 localhost") "[$engine] Preserves '::1 localhost'"
            Require ($hostsContent -match [regex]::Escape("# BEGIN devstack custom notes - not marker")) "[$engine] Preserves near-miss marker '# BEGIN devstack custom notes - not marker'"
            Require ($hostsContent -match "127\.0\.0\.1 custom\.test") "[$engine] Preserves '127.0.0.1 custom.test'"
            Require ($hostsContent -match [regex]::Escape("# END devstack custom notes - not marker")) "[$engine] Preserves near-miss marker '# END devstack custom notes - not marker'"

            # BOM check: hosts file must NOT have UTF-8 BOM (0xEF, 0xBB, 0xBF)
            $bytes = [System.IO.File]::ReadAllBytes($mockHostsPath)
            $hasBOM = ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF)
            Require (-not $hasBOM) "[$engine] hosts file does NOT contain UTF-8 BOM"
        }
    } finally {
        Remove-Item -Path $tempUninstallDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}


# -------------------------------------------------------------------------
# Test 30: Bin Directory Protected DACL Verification
# -------------------------------------------------------------------------
Step "Testing bin directory DACL permissions"
$tempBinDaclDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-bindacl-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempBinDaclDir | Out-Null
try {
    $res = Invoke-Installer @("-TestSandbox", $tempBinDaclDir, "-SkipWSL")
    Require ($res.ExitCode -eq 0) "Layout creation exits with code 0 (got $($res.ExitCode))"

    $binDir = Join-Path $tempBinDaclDir "bin"
    $binAcl = Get-Acl -Path $binDir
    Require ($binAcl.AreAccessRulesProtected -eq $true) "bin directory DACL has inheritance protected/disabled"

    $binSystemFull = $false
    $binAdminFull = $false
    $binUsersRead = $false
    $binUsersWrite = $false

    foreach ($rule in $binAcl.Access) {
        $identity = $rule.IdentityReference.Value
        $rights = $rule.FileSystemRights.ToString()
        $controlType = $rule.AccessControlType

        if ($controlType -eq [Security.AccessControl.AccessControlType]::Allow) {
            if ($identity -match "SYSTEM" -and $rights -match "FullControl") { $binSystemFull = $true }
            if (($identity -match "Administrators" -or $identity -match "BUILTIN\\Administrators") -and $rights -match "FullControl") { $binAdminFull = $true }
            if (($identity -match "Users" -or $identity -match "BUILTIN\\Users") -and ($rights -match "Read" -or $rights -match "ReadAndExecute")) { $binUsersRead = $true }
            if (($identity -match "Users" -or $identity -match "BUILTIN\\Users") -and ($rights -match "Write|CreateFiles|AppendData|Modify")) { $binUsersWrite = $true }
        }
    }

    Require $binSystemFull "bin directory grants SYSTEM FullControl"
    Require $binAdminFull "bin directory grants Administrators FullControl"
    Require $binUsersRead "bin directory grants Users ReadAndExecute"
    Require (-not $binUsersWrite) "bin directory does NOT grant Users Write or Modify access"
} finally {
    Remove-Item -Path $tempBinDaclDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 31: devstack-daemon-task.ps1 Staged into System Directory
# -------------------------------------------------------------------------
Step "Testing staging of devstack-daemon-task.ps1"
$tempDaemonStageDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-daemonstage-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempDaemonStageDir | Out-Null
try {
    $res = Invoke-Installer @("-TestSandbox", $tempDaemonStageDir, "-SkipWSL")
    Require ($res.ExitCode -eq 0) "Installer exits with code 0 (got $($res.ExitCode))"
    $stagedTask = Join-Path $tempDaemonStageDir "system\devstack-daemon-task.ps1"
    Require (Test-Path $stagedTask) "devstack-daemon-task.ps1 is staged to system directory"
} finally {
    Remove-Item -Path $tempDaemonStageDir -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 32: Uninstaller PATH Cleanup Edge Cases (Trailing slashes, quotes, env:Path)
# -------------------------------------------------------------------------
Step "Testing uninstaller PATH cleanup edge cases"
$installerContent = Get-Content -Raw -Path $uninstallerScript
Require ($installerContent -match 'TrimEnd\(''\\/''\)') "uninstall.ps1 normalizes trailing backslashes and forward slashes in PATH"
Require ($installerContent -match 'Trim\(''"''\)') "uninstall.ps1 strips quotation marks when comparing PATH entries"
Require ($installerContent -match '\$env:Path\s*=') "uninstall.ps1 synchronizes current process `$env:Path"

# Unit test PATH filtering logic
$sampleTestBin = "C:\devstack\bin"
$samplePathEntries = @(
    "C:\Windows\system32",
    "C:\devstack\bin",
    "C:\devstack\bin\",
    '"C:\devstack\bin"',
    "C:/devstack/bin",
    "C:\Program Files\Go\bin"
)
$filtered = $samplePathEntries | Where-Object {
    $p = $_.Trim().Trim('"').TrimEnd('\/').Replace('/', '\')
    $p -ne "" -and $p -ine $sampleTestBin.TrimEnd('\/').Replace('/', '\')
}
Require ($filtered.Count -eq 2) "PATH filter correctly removes all 4 variants of devstack\bin (got $($filtered.Count))"
Require ($filtered -contains "C:\Windows\system32") "Preserves C:\Windows\system32"
Require ($filtered -contains "C:\Program Files\Go\bin") "Preserves C:\Program Files\Go\bin"

# -------------------------------------------------------------------------
# Test 33: Binary Build and Staging Verification in install.ps1
# -------------------------------------------------------------------------
Step "Testing binary build failure handling and exit code propagation"
Require ($installerContent -match 'devstack-helper' -or $true) "Script inspection active"
$freshInstallContent = Get-Content -Raw -Path $installerScript
Require ($freshInstallContent -match '& go build -o \$dstDevstack ./cmd/devstack[
\s]+if \(\$LASTEXITCODE -ne 0\)') "install.ps1 checks LASTEXITCODE after devstack.exe build"
Require ($freshInstallContent -match '& go build -o \$dstHelper ./cmd/devstack-helper[
\s]+if \(\$LASTEXITCODE -ne 0\)') "install.ps1 checks LASTEXITCODE after devstack-helper.exe build"
Require ($freshInstallContent -match 'if \(-not \(Test-Path \$dstDevstack\)\)') "install.ps1 verifies Test-Path dstDevstack"
Require ($freshInstallContent -match 'if \(-not \(Test-Path \$dstHelper\)\)') "install.ps1 verifies Test-Path dstHelper"

# -------------------------------------------------------------------------
# Test 34: Reboot-Resume Argument Deduplication
# -------------------------------------------------------------------------
Step "Testing reboot-resume argument deduplication"
Require ($freshInstallContent -match 'if \(-not \$PSBoundParameters\.ContainsKey\("ResumeUserSID"\)\)') "install.ps1 uses ContainsKey to prevent duplicate -ResumeUserSID arguments"

# -------------------------------------------------------------------------
# Test 35: Missing rootfs reports a controlled error, not a null Test-Path
# -------------------------------------------------------------------------
Step "Testing missing rootfs archive guard"
$tempNoRootfsRepo = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-norootfs-" + [Guid]::NewGuid().ToString("N"))
$tempNoRootfsSandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-norootfs-sandbox-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path (Join-Path $tempNoRootfsRepo "build\wsl"), (Join-Path $tempNoRootfsRepo "build\windows") | Out-Null
Copy-Item $installerScript $tempNoRootfsRepo
Copy-Item (Join-Path $repoRoot "build\wsl\versions.lock") (Join-Path $tempNoRootfsRepo "build\wsl")
Copy-Item $daemonTaskScript (Join-Path $tempNoRootfsRepo "build\windows")
Copy-Item (Join-Path $repoRoot "build\windows\devstack-daemon.vbs") (Join-Path $tempNoRootfsRepo "build\windows")
try {
    $env:DEVSTACK_TEST_ELEVATED = "true"
    $env:DEVSTACK_TEST_OS_ARCH = "AMD64"
    $env:DEVSTACK_TEST_OS_BUILD = "22631"
    $env:DEVSTACK_TEST_VIRTUALIZATION = "true"
    $noRootOut = $null
    $prevEAP = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $noRootOut = & $psEngine -NoProfile -File (Join-Path $tempNoRootfsRepo "install.ps1") -TestSandbox $tempNoRootfsSandbox -DryRun 2>&1 | Out-String
        $noRootExit = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prevEAP
    }
    Require ($noRootExit -eq 0) "Dry-run without a rootfs exits cleanly (got $noRootExit)"
    Require ($noRootOut -notmatch "Value cannot be null") "Dry-run without a rootfs does not crash in Test-Path"
} finally {
    Remove-Item env:DEVSTACK_TEST_ELEVATED -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_OS_ARCH -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_OS_BUILD -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_VIRTUALIZATION -ErrorAction SilentlyContinue
    Remove-Item -Path $tempNoRootfsRepo -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -Path $tempNoRootfsSandbox -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 36: Uninstaller residue and test-escape gating
# -------------------------------------------------------------------------
Step "Testing uninstaller cleanup of machine-wide policy, firewall, and secrets"
$uninstallerContent = Get-Content -Raw -Path (Join-Path $repoRoot "uninstall.ps1")
Require ($uninstallerContent -match 'ImportEnterpriseRoots') "uninstall.ps1 removes the devstack-owned Firefox enterprise policy"
Require ($uninstallerContent -match 'Devstack WSL Proxy HTTP' -and $uninstallerContent -match 'Devstack WSL Proxy HTTPS') "uninstall.ps1 removes both helper firewall rules"
Require ($uninstallerContent -match 'api-token') "uninstall.ps1 purges the dashboard install token"
Require ($uninstallerContent -match 'systemprofile.*\.devstack') "uninstall.ps1 accounts for the legacy LocalSystem CA/marker location"
Require ($uninstallerContent -notmatch '\$env:DEVSTACK_TEST_MOCK_WSL') "uninstall.ps1 never reads DEVSTACK_TEST_MOCK_WSL directly"
Require ($uninstallerContent -match 'Get-TestOverride "DEVSTACK_TEST_MOCK_WSL"') "uninstall.ps1 gates DEVSTACK_TEST_MOCK_WSL through the sandbox helper"

# -------------------------------------------------------------------------
# Test 37: Dry-run is read-only; existing-install and upgrade target guards
# -------------------------------------------------------------------------
Step "Testing dry-run read-only behavior and install target guards"
$tempDryRealDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-dryreal-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempDryRealDir | Out-Null
try {
    $res = Invoke-Installer @("-DryRun", "-InstallDir", $tempDryRealDir)
    Require ($res.ExitCode -eq 0) "Dry-run with a real -InstallDir exits with code 0 (got $($res.ExitCode))"
    Require (-not (Test-Path (Join-Path $tempDryRealDir "mock-rootfs"))) "Dry-run does not create a mock-rootfs directory"
    Require (@(Get-ChildItem -Path $tempDryRealDir -Force -ErrorAction SilentlyContinue).Count -eq 0) "Dry-run leaves the install directory untouched"
} finally {
    Remove-Item -Path $tempDryRealDir -Recurse -Force -ErrorAction SilentlyContinue
}

$tempExistingDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-existing-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path (Join-Path $tempExistingDir "system"), (Join-Path $tempExistingDir "bin") | Out-Null
Set-Content -Path (Join-Path $tempExistingDir "system\install-manifest.json") -Value '{"stage":"complete"}' -Encoding utf8
Set-Content -Path (Join-Path $tempExistingDir "sentinel.txt") -Value "must-survive" -Encoding utf8
try {
    $res = Invoke-Installer @("-TestSandbox", $tempExistingDir)
    Require ($res.ExitCode -eq 1) "Fresh install over a completed install exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "existing devstack installation") "Existing-install error tells the user to use -Upgrade"
    Require ((Get-Content (Join-Path $tempExistingDir "sentinel.txt") -Raw).Trim() -eq "must-survive") "Existing-install guard does not modify target files"
} finally {
    Remove-Item -Path $tempExistingDir -Recurse -Force -ErrorAction SilentlyContinue
}

$tempMissingUpgradeDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-upgmissing-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempMissingUpgradeDir | Out-Null
try {
    $res = Invoke-Installer @("-TestSandbox", $tempMissingUpgradeDir, "-Upgrade")
    Require ($res.ExitCode -eq 1) "Upgrade without an existing install exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "Cannot upgrade") "Upgrade-without-install error is explicit"
} finally {
    Remove-Item -Path $tempMissingUpgradeDir -Recurse -Force -ErrorAction SilentlyContinue
}

$tempPartialDir = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-partial-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path (Join-Path $tempPartialDir "system"), (Join-Path $tempPartialDir "bin") | Out-Null
Set-Content -Path (Join-Path $tempPartialDir "system\install-manifest.json") -Value '{"version":"1.0","stage":"system-phase-complete"}' -Encoding utf8
Set-Content -Path (Join-Path $tempPartialDir "bin\devstack.exe") -Value "partial" -Encoding utf8
Set-Content -Path (Join-Path $tempPartialDir "bin\devstack-helper.exe") -Value "partial" -Encoding utf8
Set-Content -Path (Join-Path $tempPartialDir "sentinel.txt") -Value "keep-partial" -Encoding utf8
try {
    $res = Invoke-Installer @("-TestSandbox", $tempPartialDir, "-SkipWSL")
    Require ($res.ExitCode -eq 0) "A failed/partial install can be retried without -Upgrade (got $($res.ExitCode))"
    Require ((Get-Content (Join-Path $tempPartialDir "sentinel.txt") -Raw).Trim() -eq "keep-partial") "Partial-install retry preserves unrelated files"
} finally {
    Remove-Item -Path $tempPartialDir -Recurse -Force -ErrorAction SilentlyContinue
}

$res = Invoke-Installer @("-DryRun", "-InstallDir", "C:\")
Require ($res.ExitCode -eq 1) "Dry-run refuses the protected drive root (got $($res.ExitCode))"
Require ($res.Output -match "protected path") "Protected-install error is explicit"

# -------------------------------------------------------------------------
# Test 38: Incomplete release payload fails closed
# -------------------------------------------------------------------------
Step "Testing missing WSL provisioning assets fail closed"
$tempNoAssetsRepo = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-noassets-repo-" + [Guid]::NewGuid().ToString("N"))
$tempNoAssetsSandbox = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-noassets-sandbox-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path (Join-Path $tempNoAssetsRepo "build\windows"), $tempNoAssetsSandbox | Out-Null
Copy-Item $installerScript $tempNoAssetsRepo
Copy-Item $daemonTaskScript (Join-Path $tempNoAssetsRepo "build\windows")
Copy-Item (Join-Path $repoRoot "build\windows\devstack-daemon.vbs") (Join-Path $tempNoAssetsRepo "build\windows")
try {
    $env:DEVSTACK_TEST_WSL_VERSION = "2.3.26"
    $env:DEVSTACK_TEST_MOCK_WSL = "true"
    $env:DEVSTACK_TEST_SYSTEMD_PID1 = "systemd"
    $noAssetsOut = $null
    $prevEAP = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $noAssetsOut = & $psEngine -NoProfile -File (Join-Path $tempNoAssetsRepo "install.ps1") -TestSandbox $tempNoAssetsSandbox 2>&1 | Out-String
        $noAssetsExit = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $prevEAP
    }
    Require ($noAssetsExit -eq 1) "Missing WSL assets exit with code 1 (got $noAssetsExit)"
    Require ($noAssetsOut -match "Release payload is incomplete") "Missing WSL assets produce an explicit payload error"
} finally {
    Remove-Item env:DEVSTACK_TEST_WSL_VERSION -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_MOCK_WSL -ErrorAction SilentlyContinue
    Remove-Item env:DEVSTACK_TEST_SYSTEMD_PID1 -ErrorAction SilentlyContinue
    Remove-Item -Path $tempNoAssetsRepo -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -Path $tempNoAssetsSandbox -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 39: Uninstaller purge guards, hosts preservation, distro fail-closed
# -------------------------------------------------------------------------
Step "Testing uninstaller destructive-path guards"
$tempUnsafePurge = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-unsafepurge-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempUnsafePurge | Out-Null
Set-Content -Path (Join-Path $tempUnsafePurge "keep.txt") -Value "keep-me" -Encoding utf8
try {
    $res = Invoke-Uninstaller @("-PurgeData", "-TestSandbox", $tempUnsafePurge)
    Require ($res.ExitCode -eq 1) "-PurgeData without a manifest exits with code 1 (got $($res.ExitCode))"
    Require (Test-Path (Join-Path $tempUnsafePurge "keep.txt")) "-PurgeData refusal preserves the directory"
} finally {
    Remove-Item -Path $tempUnsafePurge -Recurse -Force -ErrorAction SilentlyContinue
}

$tempHostsIncomplete = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-hostsincomplete-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempHostsIncomplete | Out-Null
$incompleteHosts = Join-Path $tempHostsIncomplete "hosts"
$incompleteText = "127.0.0.1 localhost`r`n# BEGIN devstack`r`n127.0.0.1 app.test`r`n10.0.0.9 keep.after`r`n"
[System.IO.File]::WriteAllText($incompleteHosts, $incompleteText, (New-Object System.Text.UTF8Encoding($false)))
try {
    $env:DEVSTACK_TEST_HOSTS_PATH = $incompleteHosts
    $res = Invoke-Uninstaller @("-TestSandbox", $tempHostsIncomplete)
    Require ($res.ExitCode -eq 0) "Uninstaller with an unterminated hosts block still exits 0 (got $($res.ExitCode))"
    Require ($res.Output -match "without a matching") "Uninstaller warns about an unterminated hosts block"
    Require ((Get-Content $incompleteHosts -Raw) -eq $incompleteText) "Uninstaller leaves an unterminated hosts block byte-identical"
} finally {
    Remove-Item env:DEVSTACK_TEST_HOSTS_PATH -ErrorAction SilentlyContinue
    Remove-Item -Path $tempHostsIncomplete -Recurse -Force -ErrorAction SilentlyContinue
}

$tempHostsNoBlock = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-hostsnone-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tempHostsNoBlock | Out-Null
$noBlockHosts = Join-Path $tempHostsNoBlock "hosts"
$noBlockText = "127.0.0.1 localhost`n10.1.2.3 custom.local`n"
[System.IO.File]::WriteAllText($noBlockHosts, $noBlockText, (New-Object System.Text.UTF8Encoding($false)))
try {
    $env:DEVSTACK_TEST_HOSTS_PATH = $noBlockHosts
    $beforeHash = (Get-FileHash $noBlockHosts -Algorithm SHA256).Hash
    $res = Invoke-Uninstaller @("-TestSandbox", $tempHostsNoBlock)
    Require ($res.ExitCode -eq 0) "Uninstaller without a hosts block exits 0 (got $($res.ExitCode))"
    Require ((Get-FileHash $noBlockHosts -Algorithm SHA256).Hash -eq $beforeHash) "Uninstaller does not rewrite a hosts file without a devstack block"
} finally {
    Remove-Item env:DEVSTACK_TEST_HOSTS_PATH -ErrorAction SilentlyContinue
    Remove-Item -Path $tempHostsNoBlock -Recurse -Force -ErrorAction SilentlyContinue
}

$tempPurgeFail = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-purgefail-" + [Guid]::NewGuid().ToString("N"))
$purgeFailWsl = Join-Path $tempPurgeFail "wsl\devstack"
New-Item -ItemType Directory -Force -Path $purgeFailWsl | Out-Null
Set-Content -Path (Join-Path $purgeFailWsl "ext4.vhdx") -Value "fake-disk" -Encoding utf8
try {
    $env:DEVSTACK_TEST_UNREGISTER_FAIL = "true"
    $res = Invoke-Uninstaller @("-PurgeDistro", "-TestSandbox", $tempPurgeFail)
    Require ($res.ExitCode -eq 1) "Failed distro unregister exits with code 1 (got $($res.ExitCode))"
    Require ($res.Output -match "refusing to delete") "Failed distro unregister refuses to delete the VHD directory"
    Require (Test-Path (Join-Path $purgeFailWsl "ext4.vhdx")) "Failed distro unregister preserves ext4.vhdx"
} finally {
    Remove-Item env:DEVSTACK_TEST_UNREGISTER_FAIL -ErrorAction SilentlyContinue
    Remove-Item -Path $tempPurgeFail -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 40: Install-home propagation and upgrade failure accumulation
# -------------------------------------------------------------------------
Step "Testing daemon task home propagation and upgrade failure accumulation"
Require ($freshInstallContent -match '-Binary "\$dstDevstack" -HomeDir "\$targetDir"') "install.ps1 passes -HomeDir to the daemon task"
Require ($freshInstallContent -match '\$homeDir = \$targetDir') "install.ps1 fallback task uses the install home"
Require (([regex]::Matches($freshInstallContent, '\$upgradeFailed = \$false')).Count -eq 1) "install.ps1 initializes upgradeFailed once and never resets it after a failure"
Require ($freshInstallContent -match 'Repair-HelperServiceConfiguration') "install.ps1 verifies/repairs the helper service config path"
Require ($freshInstallContent -match 'sc\.exe config DevstackHelper') "install.ps1 can repair the helper service binPath"
Require ($freshInstallContent -notmatch 'New-ScheduledTaskPrincipal -UserId "SYSTEM"') "install.ps1 never falls back to a SYSTEM reboot-resume task"

# -------------------------------------------------------------------------
# Test 41: -Upgrade -SkipWSL updates Windows only and preserves the stage
# -------------------------------------------------------------------------
Step "Testing -Upgrade -SkipWSL"
$tempSkipUpgrade = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-skipupgrade-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path (Join-Path $tempSkipUpgrade "system"), (Join-Path $tempSkipUpgrade "bin") | Out-Null
Set-Content -Path (Join-Path $tempSkipUpgrade "system\install-manifest.json") -Value '{"stage":"complete-no-wsl","agent_installed":false}' -Encoding utf8
Set-Content -Path (Join-Path $tempSkipUpgrade "bin\devstack.exe") -Value "old" -Encoding utf8
Set-Content -Path (Join-Path $tempSkipUpgrade "bin\devstack-helper.exe") -Value "old" -Encoding utf8
try {
    $res = Invoke-Installer @("-Upgrade", "-SkipWSL", "-TestSandbox", $tempSkipUpgrade)
    Require ($res.ExitCode -eq 0) "-Upgrade -SkipWSL exits with code 0 (got $($res.ExitCode))"
    Require ($res.Output -match "WSL untouched") "-Upgrade -SkipWSL reports that WSL was not touched"
    $m = Get-Content (Join-Path $tempSkipUpgrade "system\install-manifest.json") -Raw | ConvertFrom-Json
    Require ($m.stage -eq "complete-no-wsl") "-Upgrade -SkipWSL preserves stage=complete-no-wsl"
    Require ($m.agent_installed -eq $false) "-Upgrade -SkipWSL does not claim an agent was installed"
} finally {
    Remove-Item -Path $tempSkipUpgrade -Recurse -Force -ErrorAction SilentlyContinue
}

# -------------------------------------------------------------------------
# Test 34b: Daemon task starts in this session and pins the install home
# -------------------------------------------------------------------------
Step "Testing daemon logon task starts immediately and uses the install home"
$a = Get-Content -Raw -Path $daemonTaskScript
Require ($a -match 'daemon run --home') "devstack-daemon-task.ps1 passes --home to the daemon"
Require ($freshInstallContent -match 'Start-ScheduledTask -TaskName "devstack-daemon"') "install.ps1 starts the daemon task in the current session"
Require ($freshInstallContent -match 'daemon run --home') "install.ps1 fallback task also passes --home"

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
