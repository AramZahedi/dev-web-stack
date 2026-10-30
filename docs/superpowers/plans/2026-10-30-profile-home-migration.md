# Profile-Home Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status (2026-10-30):** executed natively. All tasks verified complete except Task 9 Step 2, which intentionally retains the renamed legacy tree and WSL export until user sign-off.

**Goal:** Move the live devstack installation root from `C:\devstack` to `C:\Users\Aram\.devstack`, make every registration point at the profile path, and leave `C:\devstack` as an optional compatibility junction.

**Architecture:** The profile home becomes the only real install root: binaries, helper config, CA, manifest, state, and the managed WSL VHDX all live under `C:\Users\Aram\.devstack`. The WSL distro is exported and re-imported at the profile path so its registry `BasePath` is real, not a reparse point. The helper service, daemon task, PATH, and manifest are updated to the profile path. Only after verification is `C:\devstack` renamed away and replaced with a directory junction to the profile root for legacy path compatibility.

**Tech Stack:** Windows PowerShell 5.1/7, Windows service control, WSL2 export/import, NTFS junctions, Go binaries, Svelte dashboard.

**Spec:** `docs/superpowers/specs/2026-10-30-node-toolchain-design.md` (binding layout/security constraints) plus `docs/install-windows.md`.

## Global Constraints

- Canonical root: `C:\Users\Aram\.devstack`.
- Legacy root: `C:\devstack`; after migration it is an optional compatibility junction only, never a second data root.
- Do not overwrite active `state.db`, `config.yaml`, `system\secrets\agent-pin`, `system\secrets\agent-token`, or `system\secrets\api-token`.
- Preserve the helper CA fingerprint `785a5abdd8304724...` and the current dashboard token/session.
- Preserve `/data/devstack` exactly: 7 Docker images, PHP 8.4.27, Node 24.22.1, no sites.
- WSL registry `BasePath` must point at `C:\Users\Aram\.devstack\wsl\devstack`, not at a junction.
- Helper service `PathName` must contain `C:\Users\Aram\.devstack\bin\devstack-helper.exe` and `C:\Users\Aram\.devstack\system\helper.yaml`.
- Daemon task must run `C:\Users\Aram\.devstack\bin\devstack.exe daemon run --home C:\Users\Aram\.devstack`.
- Use a directory junction (`New-Item -ItemType Junction`), not a symbolic link. Junctions do not require Developer Mode and are transparent for local paths.
- Keep the pre-migration WSL export and the renamed legacy tree until every verification passes.
- All scripts must remain compatible with Windows PowerShell 5.1.
- Installer suite must stay green under PowerShell 7 and Windows PowerShell 5.1.
- Run every migration command from an elevated PowerShell session. The helper service, Machine PATH, and `C:\` junction require administrator rights.
- Downtime starts when the daemon and helper are stopped in Task 2 and ends after Task 7; expected 15–30 minutes.

## Review Focus

- **Uninstall through the junction:** `uninstall.ps1` must resolve the profile manifest by default and must refuse `-InstallDir C:\devstack` because it is a reparse point. Verify both paths.
- **WSL after import:** Docker, the agent TLS pin, and `/data/devstack` must survive the export/import; verify the agent pin still matches and Docker keeps 7 images.
- **Helper CA continuity:** Browsers and the OS must keep trusting the same CA after the helper config moves to the profile `system` directory.
- **Secret ACLs:** Tightening `system` must not lock the user daemon out of reading the DPAPI secrets; `devstack doctor` and dashboard auth must both work.
- **PATH shadowing:** After cleanup, `Get-Command devstack -All` must resolve to the profile binary, not a stale `C:\devstack\bin` entry.

---

### Task 1: Add installer reparse-point guard

**Files:**
- Modify: `install.ps1` (target validation after `$targetDir = $fullTarget`)
- Test: `test/windows/installer_test.ps1`

**Interfaces:**
- Produces: `install.ps1` exits non-zero with a reparse-point error when `-InstallDir` or `-TestSandbox` names a junction/symlink.
- Consumes: existing `$isSandbox`, `$fullTarget`, `Write-InstallerError`.

- [x] **Step 1: Write the failing regression check**

Add a test after the existing target-path validation tests in `test/windows/installer_test.ps1`:

```powershell
Step "Testing install refuses a reparse-point target"
$junctionRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("devstack-junction-" + [Guid]::NewGuid().ToString("N"))
$junctionTarget = Join-Path $junctionRoot "target"
$junctionPath = Join-Path $junctionRoot "link"
New-Item -ItemType Directory -Force -Path $junctionTarget | Out-Null
New-Item -ItemType Junction -Path $junctionPath -Target $junctionTarget | Out-Null
try {
    $out = & $psEngine -NoProfile -ExecutionPolicy Bypass -File $installerScript -TestSandbox $junctionPath -DryRun 2>&1
    Require ($LASTEXITCODE -ne 0) "install.ps1 rejects a reparse-point target"
    Require (($out | Out-String) -match 'reparse point|junction') "install.ps1 explains the reparse-point refusal"
} finally {
    Remove-Item -LiteralPath $junctionPath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $junctionRoot -Recurse -Force -ErrorAction SilentlyContinue
}
```

- [x] **Step 2: Run the installer suite and confirm the new check fails**

Run: `pwsh -NoProfile -File test/windows/installer_test.ps1`
Expected: FAIL on the new reparse-point check.

- [x] **Step 3: Implement the guard**

After `$targetDir = $fullTarget` and before any directory creation, add:

```powershell
# A compatibility junction is not an install target. Refuse aliases so
# service, PATH, and manifest updates cannot be written through a reparse point.
$targetItem = Get-Item -LiteralPath $targetDir -Force -ErrorAction SilentlyContinue
if ($null -ne $targetItem -and (($targetItem.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0)) {
    Write-InstallerError "Refusing to install through a reparse point/junction at '$targetDir'. Pass the real installation path instead."
    exit 1
}
```

- [x] **Step 4: Run the installer suite in both engines**

Run:
```powershell
pwsh -NoProfile -File test/windows/installer_test.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File test/windows/installer_test.ps1
```
Expected: 261 checks, 0 failures, in both.

- [x] **Step 5: Commit**

```powershell
git add install.ps1 test/windows/installer_test.ps1
git commit -m "fix: refuse installs through reparse-point targets"
```

---

### Task 2: Capture pre-migration state and backup

**Files:**
- Create: `C:\Users\Aram\devstack-migration-<stamp>\` (outside the repo)
- Read: `C:\devstack\system`, `C:\devstack\bin`, `C:\Users\Aram\.devstack\config.yaml`, `state.db`, `system\secrets`

**Interfaces:**
- Produces: `$backup` path, `windows\` copy, `wsl\devstack-pre-migration.tar`, `lxss.reg`, `machine-env.reg`, `user-env.reg`, `doctor-before.json`, `sites-before.json`, `services-before.json`, `task-before.xml`, `path-machine-before.txt`, `path-user-before.txt`.
- Consumes: current live install.

- [x] **Step 1: Set the backup root and create directories**

```powershell
$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$backup = "C:\Users\Aram\devstack-migration-$stamp"
New-Item -ItemType Directory -Force "$backup\windows", "$backup\wsl" | Out-Null
```

- [x] **Step 2: Capture registrations and live state while the daemon is still running**

```powershell
reg.exe export "HKCU\Software\Microsoft\Windows\CurrentVersion\Lxss" "$backup\lxss.reg" /y | Out-Null
reg.exe export "HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment" "$backup\machine-env.reg" /y | Out-Null
reg.exe export "HKCU\Environment" "$backup\user-env.reg" /y | Out-Null
Export-ScheduledTask -TaskName "devstack-daemon" | Set-Content "$backup\task-before.xml" -Encoding utf8
sc.exe qc DevstackHelper | Set-Content "$backup\service-before.txt" -Encoding utf8
[Environment]::GetEnvironmentVariable("Path", "Machine") | Set-Content "$backup\path-machine-before.txt" -Encoding utf8
[Environment]::GetEnvironmentVariable("Path", "User") | Set-Content "$backup\path-user-before.txt" -Encoding utf8
$env:DEVSTACK_HOME = "C:\Users\Aram\.devstack"
devstack doctor --json --home "C:\Users\Aram\.devstack" | Set-Content "$backup\doctor-before.json" -Encoding utf8
devstack site list --json --home "C:\Users\Aram\.devstack" | Set-Content "$backup\sites-before.json" -Encoding utf8
devstack service list --json --home "C:\Users\Aram\.devstack" | Set-Content "$backup\services-before.json" -Encoding utf8
```

- [x] **Step 3: Quiesce the daemon, helper, and WSL before copying state**

```powershell
devstack daemon stop --home "C:\Users\Aram\.devstack" 2>$null
Stop-Service DevstackHelper -Force -ErrorAction SilentlyContinue
wsl.exe --shutdown
Get-Process devstack, devstack-helper -ErrorAction SilentlyContinue
```

Expected: no devstack or devstack-helper process remains.

- [x] **Step 4: Copy Windows state without the 11.7 GB VHDX**

```powershell
robocopy C:\devstack "$backup\windows\legacy-root" /E /XD wsl /R:1 /W:1 | Out-Null
robocopy C:\Users\Aram\.devstack "$backup\windows\profile-home" /E /XD wsl /R:1 /W:1 | Out-Null
Copy-Item C:\Windows\System32\drivers\etc\hosts "$backup\windows\hosts" -Force
```

- [x] **Step 5: Export the WSL distro while it is still intact**

```powershell
wsl.exe --export devstack "$backup\wsl\devstack-pre-migration.tar"
if ($LASTEXITCODE -ne 0) { throw "WSL export failed" }
Get-Item "$backup\wsl\devstack-pre-migration.tar" | Select-Object FullName, Length
```

Expected: tar exists; length is roughly 3–5 GB.

- [x] **Step 6: Record checksums and confirm the backup is complete**

```powershell
Get-ChildItem "$backup\windows\legacy-root", "$backup\windows\profile-home" -Recurse -File |
    Get-FileHash -Algorithm SHA256 |
    Export-Csv "$backup\windows\hashes.csv" -NoTypeInformation
Get-ChildItem $backup -Recurse -File | Measure-Object Length -Sum
```

Expected: backup contains `legacy-root`, `profile-home`, `lxss.reg`, and the WSL tar. Do not proceed if the tar is missing or zero bytes.

---

### Task 3: Re-import the WSL distro at the profile path

**Files:**
- Modify: WSL registry `BasePath` by re-import
- Move: `/data/devstack` contents inside the exported tar

**Interfaces:**
- Produces: distro `devstack` registered at `C:\Users\Aram\.devstack\wsl\devstack`, `/data/devstack` intact.
- Consumes: `$backup\wsl\devstack-pre-migration.tar`.

- [x] **Step 1: Ensure the old distro is stopped and remove only its registration**

```powershell
wsl.exe --shutdown
wsl.exe --unregister devstack
if ($LASTEXITCODE -ne 0) { throw "WSL unregister failed" }
```

- [x] **Step 2: Import into the profile path**

```powershell
New-Item -ItemType Directory -Force "C:\Users\Aram\.devstack\wsl\devstack" | Out-Null
wsl.exe --import devstack "C:\Users\Aram\.devstack\wsl\devstack" "$backup\wsl\devstack-pre-migration.tar" --version 2
if ($LASTEXITCODE -ne 0) { throw "WSL import failed" }
```

- [x] **Step 3: Verify the registry and distro contents**

```powershell
$lx = Get-ChildItem "HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss" |
    ForEach-Object { Get-ItemProperty $_.PSPath } |
    Where-Object DistributionName -eq "devstack"
$lx | Select-Object DistributionName, BasePath, Version
wsl.exe -d devstack -u root -- sh -c "cat /data/devstack/managed.json; du -sh /data/devstack; systemctl is-enabled docker.service devstack-agent.service; ls /data/devstack/runtimes"
```

Expected:
- `BasePath` is `C:\Users\Aram\.devstack\wsl\devstack`.
- `/data/devstack` is about 3.4 GB.
- `docker.service` and `devstack-agent.service` are enabled.
- Runtimes include `php@8.4.27-r1`, `node@22.23.4`, `node@24.22.1`.

- [x] **Step 4: Start the distro and verify the agent TLS identity survived**

```powershell
wsl.exe -d devstack -u root -- sh -c "systemctl start docker.socket docker.service devstack-agent.service; sleep 3; systemctl is-active docker.service devstack-agent.service; docker info --format 'root={{.DockerRootDir}} images={{.Images}}'"
```

Expected: both active; Docker root `/data/devstack/docker`; 7 images.

If import fails, immediately run the rollback in Task 8 before touching Windows files.

---

### Task 4: Stage Windows files into the profile root

**Files:**
- Create: `C:\Users\Aram\.devstack\bin\devstack-helper.exe`, `devstack-agent`
- Create: `C:\Users\Aram\.devstack\system\helper.yaml`, `system\pki\root.crt`, `system\pki\root.key`, `system\install-manifest.json`, `system\firefox-policy-managed`
- Do not modify: `system\secrets\*`, `state.db`, `config.yaml`

**Interfaces:**
- Produces: profile root that the installer can upgrade.
- Consumes: legacy files under `C:\devstack`.

- [x] **Step 1: Copy binaries and helper state without touching active secrets**

```powershell
$profile = "C:\Users\Aram\.devstack"
New-Item -ItemType Directory -Force "$profile\bin", "$profile\system\pki" | Out-Null
Copy-Item C:\devstack\bin\devstack.exe "$profile\bin\devstack.exe" -Force
Copy-Item C:\devstack\bin\devstack-helper.exe "$profile\bin\devstack-helper.exe" -Force
Copy-Item C:\devstack\bin\devstack-agent "$profile\bin\devstack-agent" -Force
Copy-Item C:\devstack\system\helper.yaml "$profile\system\helper.yaml" -Force
Copy-Item C:\devstack\system\pki\root.crt "$profile\system\pki\root.crt" -Force
Copy-Item C:\devstack\system\pki\root.key "$profile\system\pki\root.key" -Force
Copy-Item C:\devstack\system\install-manifest.json "$profile\system\install-manifest.json" -Force
if (Test-Path C:\devstack\system\firefox-policy-managed) {
    Copy-Item C:\devstack\system\firefox-policy-managed "$profile\system\firefox-policy-managed" -Force
}
```

- [x] **Step 2: Prove active secrets and state were not overwritten**

```powershell
$before = Get-FileHash "$backup\windows\profile-home\system\secrets\agent-pin", "$backup\windows\profile-home\system\secrets\agent-token", "$backup\windows\profile-home\state.db" -Algorithm SHA256
$after  = Get-FileHash "$profile\system\secrets\agent-pin", "$profile\system\secrets\agent-token", "$profile\state.db" -Algorithm SHA256
$before.Hash
$after.Hash
```

Expected: hashes match pairwise.

- [x] **Step 3: Verify the CA is the same one currently trusted**

```powershell
(Get-FileHash C:\devstack\system\pki\root.crt -Algorithm SHA256).Hash
(Get-FileHash "$profile\system\pki\root.crt" -Algorithm SHA256).Hash
```

Expected: both `0FB871C56D201A90DC90B7EEC08310665B745F434D5462704764D843EF6493B6`.

---

### Task 5: Canonicalize with the supported installer upgrade

**Files:**
- Modify: `C:\Users\Aram\.devstack\bin`, `system`, helper service, manifest
- Read: `install.ps1`, `build\windows`, `build\wsl`

**Interfaces:**
- Produces: helper service and binaries registered against the profile root; manifest stage `complete`.
- Consumes: profile root staged in Task 4.

- [x] **Step 1: Build and stage the current binaries**

```powershell
$env:CGO_ENABLED = "0"
npm --prefix ui run build
$v = (git describe --tags --dirty).Trim()
$c = (git rev-parse --short HEAD).Trim()
$ld = "-s -w -X github.com/AramZahedi/dev-web-stack/internal/version.Version=$v -X github.com/AramZahedi/dev-web-stack/internal/version.Commit=$c"
go build -trimpath -ldflags $ld -o dist/devstack.exe ./cmd/devstack
go build -trimpath -ldflags $ld -o dist/devstack-helper.exe ./cmd/devstack-helper
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -trimpath -ldflags $ld -o dist/devstack-agent ./cmd/devstack-agent
Remove-Item Env:GOOS, Env:GOARCH
New-Item -ItemType Directory -Force build\windows, build\wsl | Out-Null
Copy-Item dist\devstack.exe, dist\devstack-helper.exe build\windows\ -Force
Copy-Item dist\devstack-agent build\wsl\devstack-agent -Force
```

- [x] **Step 2: Run the upgrade against the profile root**

```powershell
$log = "$backup\upgrade-profile.log"
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Upgrade -InstallDir "C:\Users\Aram\.devstack" *>&1 |
    Tee-Object -FilePath $log
if ($LASTEXITCODE -ne 0) { throw "Profile upgrade failed; see $log" }
```

Expected: exit 0, `Agent health verified successfully`, `Upgrade completed successfully`.

- [x] **Step 3: Verify service, binaries, and manifest**

```powershell
sc.exe qc DevstackHelper
Get-CimInstance Win32_Process -Filter "Name='devstack-helper.exe'" | Select-Object ProcessId, ExecutablePath, CommandLine
Get-Content "C:\Users\Aram\.devstack\system\install-manifest.json" -Raw
Get-Item "C:\Users\Aram\.devstack\bin\devstack.exe", "C:\Users\Aram\.devstack\bin\devstack-helper.exe" |
    Select-Object FullName, Length, LastWriteTime
```

Expected:
- Service path contains the profile helper and profile `helper.yaml`.
- Running helper process executable path is under the profile.
- Manifest stage is `complete`.
- Binaries exist under the profile.

---

### Task 6: Fix PATH and the daemon logon task

**Files:**
- Modify: Machine `PATH`
- Modify: `devstack-daemon` scheduled task
- Create: `C:\Users\Aram\.devstack\system\devstack-daemon.vbs`

**Interfaces:**
- Produces: `devstack` resolves to the profile binary; task runs the profile binary with `--home`.
- Consumes: profile install from Task 5.

- [x] **Step 1: Replace the legacy machine PATH entry**

Use the registry directly so `PATH` keeps its original `REG_EXPAND_SZ` type and does not expand `%SystemRoot%`-style entries:

```powershell
$profileBin = "C:\Users\Aram\.devstack\bin"
$key = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey(
    "SYSTEM\CurrentControlSet\Control\Session Manager\Environment", $true)
$kind = $key.GetValueKind("Path")
$machine = [string]$key.GetValue(
    "Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
$entries = @($machine -split ";" |
    Where-Object { $_ -and $_.TrimEnd("\") -ine "C:\devstack\bin" })
if (-not ($entries | Where-Object { $_.TrimEnd("\") -ieq $profileBin })) {
    $entries = @($profileBin) + $entries
}
$key.SetValue("Path", ($entries -join ";"), $kind)
$key.Close()
$env:Path = "$profileBin;$env:Path"
```

Open a new terminal after this step; existing shells keep their inherited `PATH`.

- [x] **Step 2: Re-register the logon task against the profile binary**

```powershell
& .\build\windows\devstack-daemon-task.ps1 `
    -Binary "C:\Users\Aram\.devstack\bin\devstack.exe" `
    -HomeDir "C:\Users\Aram\.devstack" `
    -UserId "ARAM-PC\Aram"
```

- [x] **Step 3: Verify resolution and task action**

```powershell
Get-Command devstack -All | Select-Object Source
(Get-ScheduledTask -TaskName "devstack-daemon").Actions | Select-Object Execute, Arguments, WorkingDirectory
Get-Content "C:\Users\Aram\.devstack\system\devstack-daemon.vbs" -Raw
```

Expected:
- `devstack` resolves to `C:\Users\Aram\.devstack\bin\devstack.exe`.
- Task action is `wscript.exe` with the profile VBS.
- VBS contains the profile binary, `daemon run`, and `--home C:\Users\Aram\.devstack`.

---

### Task 7: Initialize and verify the migrated install

**Files:**
- Read: `devstack doctor`, dashboard, WSL services, runtimes
- Modify: nothing unless a check fails

**Interfaces:**
- Produces: green live install on the profile root.
- Consumes: Tasks 3–6.

- [x] **Step 1: Run init and doctor**

```powershell
$env:DEVSTACK_HOME = "C:\Users\Aram\.devstack"
devstack init --yes --home "C:\Users\Aram\.devstack"
devstack doctor --json --home "C:\Users\Aram\.devstack" |
    ConvertFrom-Json |
    Select-Object ok, @{n="fail";e={($_.checks | Where-Object status -eq "fail").Count}}, @{n="warn";e={($_.checks | Where-Object status -eq "warn").Count}}
```

Expected: `ok: True`, 0 failures, 0 warnings.

- [x] **Step 2: Verify the dashboard and auth bootstrap**

```powershell
$ui = devstack ui --json | ConvertFrom-Json
curl.exe -k -sS -o NUL -w "https=%{http_code}`n" $ui.url
curl.exe -sS -o NUL -w "fallback=%{http_code}`n" $ui.fallback_url
$token = $ui.open_url -replace "^.*#token=", ""
curl.exe -k -sS -o NUL -w "sites=%{http_code}`n" -H "X-Devstack-Token: $token" "$($ui.url)/api/v1/sites"
```

Expected: HTTPS 200, fallback 200, sites 200.

- [x] **Step 3: Verify WSL, Docker, runtimes, and defaults**

```powershell
wsl.exe -d devstack -u root -- sh -c "systemctl is-active docker.service devstack-agent.service; docker info --format 'root={{.DockerRootDir}} images={{.Images}}'; ls /data/devstack/runtimes"
devstack php default --status --json
devstack node default --status --json
devstack site list --json
devstack service list --json
```

Expected:
- Docker and agent active.
- Docker root is `/data/devstack/docker`; 7 images.
- PHP `php@8.4.27-r1`; Node `24.22.1`.
- No sites; all services `not_installed`.

- [x] **Step 4: Verify the agent pin still matches**

```powershell
wsl.exe -d devstack -u root -- sh -c "openssl x509 -in /data/devstack/agent-tls/agent.crt -noout -fingerprint -sha256"
```

Expected: `B4:5B:9A:58:...:F7:1D`, matching the active profile pin.

---

### Task 8: Replace the legacy root with a compatibility junction

**Files:**
- Rename: `C:\devstack` to `C:\devstack.pre-junction-<stamp>`
- Create: `C:\devstack` directory junction to `C:\Users\Aram\.devstack`

**Interfaces:**
- Produces: legacy path resolves to the profile root; no live data remains in the old tree.
- Consumes: fully verified profile install from Task 7.

- [x] **Step 1: Prove no process or registration still uses the legacy root**

```powershell
Get-CimInstance Win32_Process |
    Where-Object { $_.ExecutablePath -like "C:\devstack\*" } |
    Select-Object ProcessId, Name, ExecutablePath, CommandLine
sc.exe qc DevstackHelper
(Get-ScheduledTask -TaskName "devstack-daemon").Actions | Select-Object Execute, Arguments
```

Expected: no process with a legacy executable path; service and task both point to the profile.

- [x] **Step 2: Rename the old tree, then create the junction**

```powershell
$legacyOld = "C:\devstack.pre-junction-$stamp"
Rename-Item -LiteralPath "C:\devstack" -NewName (Split-Path $legacyOld -Leaf)
New-Item -ItemType Junction -Path "C:\devstack" -Target "C:\Users\Aram\.devstack"
```

- [x] **Step 3: Verify the junction and legacy-path compatibility**

```powershell
$link = Get-Item "C:\devstack" -Force
$link | Select-Object FullName, LinkType, Target, Attributes
Test-Path "C:\devstack\bin\devstack.exe"
Test-Path "C:\devstack\wsl\devstack\ext4.vhdx"
Get-Content "C:\devstack\system\install-manifest.json" -Raw | ConvertFrom-Json | Select-Object stage
```

Expected:
- `LinkType` is `Junction`; target is `C:\Users\Aram\.devstack`.
- Both legacy paths resolve.
- Manifest stage is `complete`.

- [x] **Step 4: Verify WSL and uninstall safety**

```powershell
$lx = Get-ChildItem "HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss" |
    ForEach-Object { Get-ItemProperty $_.PSPath } |
    Where-Object DistributionName -eq "devstack"
$lx | Select-Object BasePath
```

Expected: `BasePath` remains `C:\Users\Aram\.devstack\wsl\devstack`, not `C:\devstack`.

Run an uninstall dry-run against the junction and confirm it refuses:

```powershell
& powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\uninstall.ps1 -InstallDir "C:\devstack" -DryRun 2>&1
if ($LASTEXITCODE -eq 0) { throw "uninstaller accepted a reparse point" }
```

Expected: non-zero exit with the reparse-point refusal.

- [x] **Step 5: Run the final doctor and dashboard check**

```powershell
devstack doctor --json | ConvertFrom-Json | Select-Object ok
curl.exe -k -sS -o NUL -w "%{http_code}`n" https://devstack.test/
```

Expected: `ok: True`, HTTP 200.

- [x] **Step 6: Commit the installer guard and documentation**

```powershell
git add install.ps1 test/windows/installer_test.ps1
git commit -m "fix: refuse installs through reparse-point targets"
```

---

### Task 9: Clean up and document the compatibility layout

**Files:**
- Modify: `install.ps1` header, `README.md`, `docs/install-windows.md`, `docs/wsl.md`, `docs/troubleshooting.md`
- Remove: `C:\devstack.pre-junction-<stamp>` after the verification window
- Keep: WSL export tar until the user confirms

**Interfaces:**
- Produces: documentation matches the profile-home behavior and the optional junction.
- Consumes: verified migration.

- [x] **Step 1: Update the stale default-path text**

Replace:
- `install.ps1` header `provisions C:\devstack` and `Defaults to C:\devstack`.
- `README.md` `configures C:\devstack`.
- `docs/wsl.md` import location and token/pin paths.
- `docs/troubleshooting.md` agent secret paths.
- `docs/windows-paths.md` and `docs/windows-networking.md` stale `C:\devstack` examples.
- Add a short `docs/install-windows.md` section:

````markdown
### Optional compatibility junction

Existing scripts that hardcode `C:\devstack` can keep working by replacing the
old directory with a junction:

```powershell
New-Item -ItemType Junction -Path C:\devstack -Target $env:USERPROFILE\.devstack
```

The junction is not a second installation. WSL, the helper service, the daemon
task, and PATH must all point at `%USERPROFILE%\.devstack`. `uninstall.ps1`
refuses to uninstall through a reparse point; run it without `-InstallDir` so it
uses the profile manifest, then remove the junction manually.
````

- [ ] **Step 2: Remove the renamed legacy tree after verification** — deferred until user sign-off.

The verified migration keeps the renamed tree and WSL export tar as rollback points.
After sign-off, remove them with:

```powershell
Remove-Item -LiteralPath "C:\devstack.pre-junction-$stamp" -Recurse -Force
```

Expected: the junction remains, the old physical tree is gone, and `Test-Path C:\devstack\bin\devstack.exe` still returns true.

- [x] **Step 3: Run the final gates**

```powershell
pwsh -NoProfile -File test/windows/installer_test.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File test/windows/installer_test.ps1
go test ./...
$env:DEVSTACK_HOME = "C:\Users\Aram\.devstack"
devstack doctor --json
```

Expected: 261 installer checks pass in both engines; Go tests pass; doctor `ok: True`.

- [x] **Step 4: Commit documentation**

```powershell
git add install.ps1 README.md docs/install-windows.md docs/wsl.md docs/troubleshooting.md docs/windows-paths.md docs/windows-networking.md
git commit -m "docs: document profile-home default and compatibility junction"
```

---

## Rollback

Run only if a task fails before the final cleanup.

1. Stop the profile daemon and helper:
   ```powershell
   devstack daemon stop --home "C:\Users\Aram\.devstack" 2>$null
   Stop-Service DevstackHelper -Force -ErrorAction SilentlyContinue
   wsl.exe --shutdown
   ```
2. Remove any junction:
   ```powershell
   if ((Get-Item C:\devstack -Force -ErrorAction SilentlyContinue).Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
       Remove-Item -LiteralPath C:\devstack -Force
   }
   ```
3. Restore the legacy tree if it was renamed:
   ```powershell
   if (Test-Path "C:\devstack.pre-junction-$stamp") {
       Rename-Item "C:\devstack.pre-junction-$stamp" "devstack"
   }
   ```
4. Re-import the backup distro at the legacy path:
   ```powershell
   wsl.exe --unregister devstack
   wsl.exe --import devstack "C:\devstack\wsl\devstack" "$backup\wsl\devstack-pre-migration.tar" --version 2
   ```
5. Restore the scheduled task from the backup. Do **not** import `lxss.reg` after a successful `wsl --import`; the import already writes the correct registry entry, and importing the old hive would add a duplicate distro key.
   ```powershell
   Register-ScheduledTask -TaskName "devstack-daemon" -Xml (Get-Content "$backup\task-before.xml" -Raw) -Force
   ```
6. Restore the helper binPath:
   ```powershell
   sc.exe config DevstackHelper binPath= "\"C:\devstack\bin\devstack-helper.exe\" service run --config \"C:\devstack\system\helper.yaml\" --allow-sid S-1-5-21-1813236471-1267824749-3672855341-1000"
   Start-Service DevstackHelper
   ```
7. Restore the environment hives, preserving value types:
   ```powershell
   reg.exe import "$backup\machine-env.reg"
   reg.exe import "$backup\user-env.reg"
   ```
8. Start the legacy daemon:
   ```powershell
   C:\devstack\bin\devstack.exe daemon start --home C:\devstack
   C:\devstack\bin\devstack.exe doctor --json --home C:\devstack
   ```
9. Verify dashboard, WSL agent, Docker, and sites before deleting anything else.

## Verification Checklist

- [ ] No process has an executable path under `C:\devstack` except through the junction.
- [ ] WSL `BasePath` is the real profile path.
- [ ] Helper service path and config are the profile path.
- [ ] Daemon task binary/home are the profile path.
- [ ] PATH resolves `devstack` to the profile binary.
- [ ] `devstack doctor` is green with 0 failures/warnings.
- [ ] Dashboard HTTPS and fallback both return 200.
- [ ] WSL agent pin matches the live certificate.
- [ ] Docker root is `/data/devstack/docker`; 7 images.
- [ ] PHP 8.4.27 and Node 24.22.1 defaults still resolve.
- [ ] `C:\devstack` is a junction; its target is the profile root.
- [ ] Uninstall dry-run through the junction refuses.
- [ ] Pre-migration WSL tar and renamed legacy tree remain until the user signs off.
