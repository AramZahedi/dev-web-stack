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
# Test Summary
# -------------------------------------------------------------------------
Write-Host "`ninstaller-test: Completed $script:TestsRun checks with $script:Failures failure(s)."
if ($script:Failures -ne 0) {
    Write-Host "installer-test: FAILED" -ForegroundColor Red
    exit 1
}
Write-Host "installer-test: ALL TESTS PASSED" -ForegroundColor Green
exit 0
