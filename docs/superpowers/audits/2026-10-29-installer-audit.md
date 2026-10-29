# Windows Installer / Release / Lifecycle Audit — 2026-10-29

**Worktree:** `C:\Users\Aram\.local\share\opencode\worktree\dc072e\audit-installer`
**Branch:** `audit/installer-2026-10-29`
**Baseline HEAD:** `d9c2c39` (`fix(windows): close round 4 audit findings and harden WSL lifecycle`)
**Code commits:** `457a6f1` (`fix(installer): harden Windows install, upgrade, uninstall, and release contracts`) and `55a51d1` (`fix(installer): keep sandbox lifecycle tests off the live host`)
**Scope:** `install.ps1`, `uninstall.ps1`, `cmd/devstack-installer/**`, `build/release-windows.ps1`, `build/release-windows.sh`, `build/windows/**`, `build/wsl/**`, `test/windows/**`, `test/release/**`, and `docs/install-windows.md`. No `internal/**` source was modified.

## Verdict

The installer/release/lifecycle layer is substantially safer than at `d9c2c39`, but it is not proven end-to-end on a clean machine. The audit found and fixed real fail-open, data-loss, and split-brain defects in all three lifecycle phases:

- a fresh install could silently skip Docker and the agent systemd unit while writing `agent_installed = true`;
- a failed upgrade could report success or lose its completed-install manifest;
- a failed upgrade or `-SkipWSL` upgrade could run WSL operations that were never provisioned;
- `-DryRun` wrote a fake rootfs into the install directory and could fail its own checksum gate;
- the uninstaller could recursively delete an unverified directory and could remove another install's CA key/tokens/registry entries;
- the release scripts could skip the pinned-rootfs gate when a real rootfs was supplied with `-MockRootFS`;
- the Windows logon task did not receive the custom install home, so the daemon could use a different home than the helper;
- a repository lifecycle test invoked the real uninstaller unsandboxed from an elevated test process.

The release ZIP contract is now tested for the WSL provisioning assets, checksums, SBOM, rootfs sidecar, standalone payload extraction/cleanup, and a sandboxed fresh install from the extracted ZIP. A real clean-machine install, real reboot-resume, real upgrade/rollback, and a real `devstack init` still require execution on a disposable Windows VM.

## Findings and Fixes

| ID | Severity | Finding | Evidence | Fix |
|---|---|---|---|---|
| I-01 | **Critical** | Installer treated the WSL provisioning payload as optional. With no `build/wsl/*` assets, Docker and the agent unit were silently skipped while the manifest said `agent_installed = true`. | `install.ps1` used `if (Test-Path $installDockerScript)` / `if (Test-Path $agentServiceSrc)`; the release ZIP at the audit baseline contained the assets, but any malformed/source payload still exited 0. | Require `install-docker.sh`, `docker-daemon.json`, `agent.service`, and `install-agent.sh` for every non-dry-run install; fail with `Release payload is incomplete`; verify Docker is active and `devstack-agent.service` is `enabled`; test 38 and the release tests. |
| I-02 | **Critical** | Uninstaller could recursively delete an arbitrary directory (`-PurgeData`) with no target validation. `-InstallDir C:\`, a profile directory, or a junction had no guard. | `Remove-Item -Path $targetDir -Recurse -Force` was reachable from user input; no manifest check. | Canonicalize and reject drive roots, `%SystemRoot%`, Program Files/ProgramData, `%USERPROFILE%`, Downloads, `C:\Users`, UNC paths, and reparse points. `-PurgeData` requires a parseable `install-manifest.json` with `version` and `stage`. Tests 39 and lifecycle test 8. |
| I-03 | **Critical** | The lifecycle test suite invoked the real `uninstall.ps1` without `-TestSandbox` in its elevation test. On an elevated test runner this stopped/deleted `DevstackHelper`, removed the firewall rule, scheduled tasks, hosts block, and CA trust from the host. | Baseline `lifecycle_test.ps1` test 3. It actually ran during this audit and changed the review machine (see incident/restoration below). | Test now skips the real unsandboxed path when the runner is elevated and uses only sandboxed paths; added static/behavioral coverage for elevation gating. |
| I-03b | **High** | Sandboxed upgrade/uninstall still killed the **live** `devstack` process: `Get-Process -Name devstack \| Stop-Process` was outside the `$isSandbox` guard in both scripts. Test runs repeatedly killed the host dashboard daemon. | Upgrade section 1 and uninstall section 2. | Guard live daemon shutdown with `-not $isSandbox`; sandbox runs log `[TestSandbox] Skipping live daemon shutdown`; tests 40. |
| I-04 | **High** | Upgrade reset `$upgradeFailed = $false` after Windows binary staging, discarding a failed `go build`/missing binary. The upgrade could continue and report success with old/missing binaries. | Original `install.ps1:628`; test 23 fault path. | Initialize the flag once before staging and never reset it; test 40. |
| I-05 | **High** | Upgrade wrote a fresh `install-manifest.json` with `stage = system-phase-complete` before backing up or staging. A failed upgrade destroyed the completed-install marker and blocked a normal retry. | Reproduced with test 22: second upgrade saw `system-phase-complete` and reported “no completed installation”. | Skip the early manifest write when `-Upgrade`; test 22. |
| I-06 | **High** | A fresh install over a completed install, or `-Upgrade` against an empty directory, was not rejected consistently. This risked overwriting a live install without rollback. | No target preflight; reinstall hit sharing violations. | Added completed-install/partial-install target validation. Fresh installs over a completed target fail with a `-Upgrade` instruction; partial failed installs can be retried; tests 37. |
| I-07 | **High** | The daemon logon task was registered without `-HomeDir`, and the fallback VBS hardcoded `%USERPROFILE%\.devstack`. A custom `-InstallDir` or legacy `C:\devstack` install ran the daemon against a different home. Upgrade/uninstall daemon commands also omitted `--home`. | `install.ps1` task invocation and fallback; `devstack daemon restart`/`stop` calls. | Pass `-HomeDir $targetDir`, use `$targetDir` in the fallback VBS, and use `--home $targetDir` for daemon stop/restart in both scripts; tests 40. |
| I-08 | **Critical (cross-area)** | `devstack-helper.exe service install --config <path>` persists the requested config but registers the service with its compiled-in default config path. `windowsServiceInstallArgs` ignores the parsed `--config` path. Custom `-InstallDir` and legacy `C:\devstack` helper services therefore point at the wrong YAML. | `cmd/devstack-helper/service_windows.go:45` uses `helper.DefaultWindowsConfigPath`; source review. | Installer-side mitigation: after create/upgrade, verify `Win32_Service.PathName` contains both the target helper and target `helper.yaml`; repair with `sc.exe config DevstackHelper "binPath= ..."`; fail closed if repair fails. Manual dummy-service test confirmed the quoting. The helper source still needs the root-cause fix (reported, not edited). |
| I-09 | **High** | `-Upgrade -SkipWSL` still staged the Linux agent, restarted `devstack-agent.service`, and could run WSL rollback even though no WSL backup/agent existed. | Upgrade section 6/7 had no `$SkipWSL` guard. | Guard agent staging, health probing, success cleanup, and rollback; test 41. A `-SkipWSL` upgrade updates Windows only and preserves `complete-no-wsl`. |
| I-10 | **High** | `-DryRun` created `$InstallDir\mock-rootfs\debian-12-wsl-rootfs.tar.gz`, then with `build/wsl/versions.lock` present compared that fake file with the pinned real SHA-256 and exited 2. It also left residue in the live install directory. | Baseline `install.ps1 -DryRun` output: `Rootfs SHA256 checksum mismatch ... Expected d232... got a35a...`; baseline installer tests failed 2 checks. | Dry-run no longer creates a rootfs; it uses a non-existent default path only for the plan, skips checksum/size reads, and skips `Test-Path` on empty strings; tests 37. |
| I-11 | **High** | Direct `install.ps1 -Upgrade`/`uninstall.ps1` ignored a legacy `C:\devstack` install because they defaulted to `%USERPROFILE%\.devstack`. This could remove machine-wide registrations for the wrong tree or leave legacy residue. | Default target code; live machine has legacy `C:\devstack` and a profile tree. | Both scripts detect a complete legacy manifest when no target is supplied and no complete profile install exists; docs and tests updated. The standalone installer already detected legacy for upgrade/uninstall and now rejects a fresh profile install beside a legacy tree. |
| I-12 | **High** | `-PurgeDistro` ignored `wsl --unregister` failure and deleted the distro directory anyway, potentially destroying the only VHD of a still-registered distro while reporting success. | Unconditional `Remove-Item $wslDistroDir`. | Enumerate registered distros, check `wsl --unregister` exit status, and refuse directory/registry cleanup on failure; sandbox test 39. |
| I-13 | **High** | Uninstaller hosts cleanup deleted everything after a `# BEGIN devstack` marker if the matching `# END devstack` was missing, and rewrote a hosts file that contained no devstack block (normalizing bytes/line endings). | Line-based loop dropped all `$inBlock` lines including unrelated entries; write happened unconditionally. | Track `blockFound`/`blockComplete`; leave the file byte-identical when no block or an incomplete block is present; tests 39. |
| I-14 | **High** | Release scripts could bypass the real-rootfs size and pinned-SHA gate by passing `-MockRootFS` together with a real `-RootFSPath`; neither script verified the artifact against `versions.lock`. | `release-windows.ps1`/`.sh` checked the flag, not whether a mock was actually generated; no pinned comparison. | Track `usedMockRootfs`; only a generated mock skips the gates; require and enforce `rootfs_sha256` from `build/wsl/versions.lock` for supplied real rootfs artifacts. Shell and PS release tests exercise the bypass. |
| I-15 | **High** | Upgrade rollback did not treat binary restore or helper restart failures as rollback failures; a WSL restore could succeed while `Move-Item`/`Start-Service` failed silently. The “rolled back successfully” message was too broad. | Rollback block; test 23. | Wrap binary restore/helper restart, accumulate failures into `$rollbackOK`, and print success only after all restores; message is now `Upgrade failed. Initiating rollback...`. |
| I-16 | **High** | Uninstaller removed CA **trust** but not the private CA key material, and removed key material while the helper service might still be running. The CA can sign certificates and should not survive uninstall. | `uninstall.ps1` cleanup order; `root.key` under `system\pki`. | Stop the helper, remove the target and legacy LocalSystem-profile `pki` directories, then delete the service; docs updated. |
| I-17 | **High** | `-PurgeData` deleted `agent-token`/`agent-pin`/`api-token` from `%USERPROFILE%\.devstack` and `$DEVSTACK_HOME` even when uninstalling a different target, and registry cleanup removed every user's `devstack` Lxss entry by name. | Secrets path list and per-user registry loop. | Purge only the target's `system\secrets` (plus the explicit sandbox test override); remove Lxss entries only when `BasePath` matches the target `wsl\devstack` directory. |
| I-18 | **High** | Reboot-resume fell back to a `SYSTEM` scheduled task if the per-user principal could not be created, reintroducing the C4 per-user/`allow_sid` failure. | `New-ScheduledTaskPrincipal -UserId "SYSTEM"` fallback. | Remove the SYSTEM fallback; fail with a clear instruction to rerun after reboot. Also normalize `-InstallDir`, `-TestSandbox`, and `-RootFSPath` in the persisted resume arguments. |
| I-19 | **Medium** | Temp payload extraction was never cleaned up: `defer os.RemoveAll(stageDir)` in `main()` never runs because every path calls `os.Exit`. Payload/rootfs files accumulated in `%TEMP%`. | `cmd/devstack-installer/main.go`. | Refactored to `run() int` with `main` calling `os.Exit(run(...))`; stage cleanup now runs. Release PS test asserts no new `devstack-install-*` stages after standalone dry-run. |
| I-20 | **Medium** | An explicit `--zip` was ignored when the executable contained an embedded payload; the embedded payload won. | `locatePayload` order. | Explicit `--zip` first, then `DEVSTACK_PAYLOAD_ZIP`, then embedded, then search; Go test `TestLocatePayloadExplicitZipOverridesEmbedded`. |
| I-21 | **Medium** | `-RootFSPath` size was not checked at install time; only the hash was checked. | `install.ps1` checksum section. | Enforce `rootfs_minimum_bytes` from `versions.lock` (default 100 MiB) for real installs; tests use sandbox mocks, which are exempt. |
| I-22 | **Medium** | WSL distro lists were split only on `\r\n`, which can miss names under PowerShell/native-output line-ending differences. | `Install.ps1` `Split("`r`n")`. | Use `-split "\r?\n"` in both scripts. |
| I-23 | **Medium** | SBOM omitted the packaged WSL provisioning assets and the release contract only checked `>= 6` packages. | Release scripts/tests. | SBOM now lists 10 packages including all four WSL assets; tests assert names and count. |
| I-24 | **Medium** | Release contract tests did not exercise `build/release-windows.ps1`, the embedded installer payload, or a fresh install from the ZIP. | Only `release_windows_test.sh` existed. | Added `test/release/release_windows_ps_test.ps1`: builds a mock release, verifies ZIP/checksums/SBOM/rootfs sidecar, extracts it, runs a sandbox install (agent unit, Docker config, manifest), runs the standalone installer dry-run, and asserts stage cleanup. |
| I-25 | **Medium** | Docs documented the wrong default (`C:\devstack`), the wrong installer flag (`-dir`), an upgrade order that did not match the code, and no legacy/`-Force` behavior. | `docs/install-windows.md`. | Updated default, flag, upgrade order, uninstaller guards/purge behavior, and daemon autostart note. |

## Test Evidence

All commands were run in the audit worktree on Windows 11 10.0.26300.0 x64 with WSL 3.0.3. No upgrade, rollback, or purge was run against `/data/devstack`. One test-safety incident did invoke the real uninstaller against the Windows host (see below); it preserved the WSL distro and `/data/devstack`.

| Gate | Result |
|---|---|
| `pwsh -NoProfile -File test/windows/installer_test.ps1` | **PASS** — 241 checks, 0 failures |
| `powershell.exe -NoProfile -ExecutionPolicy Bypass -File test\windows\installer_test.ps1` (Windows PowerShell 5.1) | **PASS** — 241 checks, 0 failures |
| `pwsh -NoProfile -File test/windows/lifecycle_test.ps1` | **PASS** — 41 tests, 0 failures |
| `powershell.exe -NoProfile -ExecutionPolicy Bypass -File test\windows\lifecycle_test.ps1` | **PASS** — 41 tests, 0 failures |
| `wsl -d Ubuntu -- bash test/release/release_windows_test.sh` | **PASS** — all Windows release contract assertions, including WSL assets, SBOM, pinned-rootfs bypass rejection, internal/outer checksums, corruption detection |
| `pwsh -NoProfile -File test/release/release_windows_ps_test.ps1` | **PASS** — 41 checks, 0 failures |
| `go test ./cmd/devstack-installer` | **PASS** |
| `go vet ./cmd/devstack-installer` | **PASS** |
| `go build ./...` | **PASS** |
| `build/release-windows.ps1 -Version 0.0.0-audit -SkipUI -MockRootFS` (manual) | **PASS** — 17 ZIP members, 10 SBOM packages, forward-slash `build/wsl/*` entries |
| Fresh ZIP install in `-TestSandbox` with mocked WSL/systemd | **PASS** — exit 0; `devstack-agent.service`, `docker-daemon.json`, agent binary present; manifest `stage=complete`, `agent_installed=true`, `systemd_verified=true` |
| `sc.exe config` quoting probe on a temporary dummy service | **PASS** — registered `binPath` preserved quoted paths and `--config`/`--allow-sid` |

The Windows release PS test also asserts that the standalone `devstack-installer.exe` extracts its embedded payload, runs `install.ps1 -DryRun`, removes its `%TEMP%\devstack-install-*` stage, and does not create `mock-rootfs` in the target.

## Live-Machine Incident and Restoration

While running the baseline `lifecycle_test.ps1`, test 3 invoked the real `uninstall.ps1` from an elevated session (the test assumed a non-elevated runner and relied on a test-only elevation override). That unsandboxed run removed the live `DevstackHelper` service, `devstack-daemon` task, `devstack-daemon-proxy` firewall rule, hosts block, and root CA trust. This was a test-safety defect, not an intended product action.

Restoration performed:

- reinstalled `DevstackHelper` from `C:\devstack\bin\devstack-helper.exe service install --config C:\devstack\system\helper.yaml` and confirmed Running/Automatic;
- re-imported `C:\devstack\system\pki\root.crt` into `Cert:\LocalMachine\Root`;
- recreated `devstack-daemon-proxy` scoped to `172.25.48.0/20` on `vEthernet (WSL (Hyper-V firewall))` for TCP 8080/8443;
- re-registered and started the `devstack-daemon` task; the daemon is running with its existing `C:\Users\Aram\.devstack` home;
- confirmed the hosts file was repopulated with an empty devstack block;
- removed the `%USERPROFILE%\.devstack\mock-rootfs` artifact created by the old dry-run bug.

The lifecycle test now refuses to exercise the real unsandboxed uninstall path when the test process is elevated. Separately, sandboxed upgrade/uninstall no longer kills the host `devstack` process; this had repeatedly stopped the live dashboard daemon during the test runs and is now covered by a regression assertion.

## Cross-Area Issues (Not Edited)

1. **`cmd/devstack-helper/service_windows.go` — `windowsServiceInstallArgs` ignores `--config`.** It always registers `helper.DefaultWindowsConfigPath`. This is the root cause of the custom/legacy helper-service mismatch. The installer now repairs the service registration and fails closed, but the helper should use the parsed `opts.configPath` in the service `binPath`. Suggested test: build service args with `--config D:\x\system\helper.yaml` and assert the binPath contains it.
2. **`internal/wintrust/trust_windows.go` — Firefox policy marker uses `DefaultSystemDir`, not the configured helper system directory.** `policyStatePath` is `filepath.Join(DefaultSystemDir, "firefox-policy-managed")`. The uninstaller checks both the target and the LocalSystem profile, but a custom helper system dir can still leave the marker/policy outside the configured tree. The helper should derive this from `cfg.SystemDir`.
3. **`.github/workflows/ci.yml` — the Windows matrix release step runs `release-windows.ps1 -Version 0.1.0 -SkipUI` with no rootfs and no `-MockRootFS`.** With the (correct) fail-closed release gate this step cannot succeed unless the job builds/downloads a real rootfs. The new `test/release/release_windows_ps_test.ps1` is also not wired into CI. CI changes are outside this audit's file scope.
4. **`versions.lock` pinned rootfs digest is not proven by an artifact in the repository.** The release scripts now enforce it, but no real rootfs was available to verify that `d232cfe6…` is the digest of the current reproducible build. A real release build will fail closed if it differs.
5. **Real clean-machine evidence is still absent:** no clean Windows 10 22H2 install, no real reboot-resume, no real upgrade/rollback against a populated `/data/devstack`, no real `devstack init`, no multi-user SID test. Native `go test -race` remains unavailable on Windows without CGO.
6. **Root-only WSL execution** remains the product model (R2-M12); this audit did not change it.
7. **Firefox policy removal depends on the helper ownership marker.** If an older/failed helper wrote the policy without the marker, `uninstall.ps1` deliberately does not delete the machine-wide policy. The live machine still has `ImportEnterpriseRoots=1` and a `C:\devstack\system\firefox-policy-managed` marker; legacy detection will now find that marker.

## Residual Risks

- The installer cannot fully recover a partial install whose binaries are present but whose manifest is corrupt; it treats that as a fresh partial retry and may overwrite those binaries. A completed manifest or a live helper service is the authoritative signal.
- `-PurgeData` removes the target after validating the manifest, but a privileged user can still point it at a directory containing a forged devstack manifest. Protected-path and reparse-point guards reduce, but do not eliminate, that operator-error risk.
- The release contract test uses a mock rootfs; it proves packaging and fail-closed gates, not that the shipped Debian rootfs boots with systemd and Docker.
- The helper-service repair uses `sc.exe config`; service configuration changes while the helper is stopped are verified, but a third-party tool that later rewrites the service binPath can still break it.
- The `-SkipWSL` path leaves the daemon task running without a managed distro; dashboard/WSL features will fail until the user runs a WSL-enabled install. This is expected for the explicit flag but is not covered by a real-install test.

## Commits and Files

- `457a6f1` — code, tests, release scripts, and `docs/install-windows.md` (amended from the audit WIP checkpoint; includes the release script changes from the interrupted session).
- `55a51d1` — follows up on the sandbox/live-process safety defect found during the final test runs.
- This document is committed separately so it can record the code commits.

Changed across the two code commits:

```text
cmd/devstack-installer/installer.go
cmd/devstack-installer/installer_test.go
cmd/devstack-installer/main.go
docs/install-windows.md
install.ps1
uninstall.ps1
build/release-windows.ps1
build/release-windows.sh
test/release/release_windows_test.sh
test/release/release_windows_ps_test.ps1   (new)
test/windows/installer_test.ps1
test/windows/lifecycle_test.ps1
```

No commit was pushed and no branch was merged.
