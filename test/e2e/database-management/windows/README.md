# Windows isolation and R07 option A handover

This directory contains the read-only and gated handover scripts for the
Windows side of the database-management E2E run. **Nothing here is executed by
`smoke.sh` or any Linux harness script.**

## Safety rules

- The real install is `C:\Users\Aram\.devstack`; it owns the `DevstackHelper`
  singleton service, the real daemon, and the WSL `devstack` distro agent.
- `read-only-check.ps1` only reads and hashes. It never starts, stops,
  installs, upgrades, or reconfigures anything.
- `handover-plan.ps1` is plan-only by default. `-Execute` requires
  `-Approve "I UNDERSTAND THE HANDOVER"` and elevation.
- `rollback.ps1` requires `-Approve "I UNDERSTAND THE ROLLBACK"` and
  verifies every snapshot hash before and after restoration.
- Never run the handover while another E2E run or disposable container is
  active.

## Recommended model: R07 option A

1. Record the baseline with `read-only-check.ps1` (CLI JSON, ports, service
   status, file hashes, hosts hash).
2. Rehearse the plan with `handover-plan.ps1` (no mutation). Inspect
   `handover-plan.md` and `snapshot-manifest.json`.
3. Approve the window, then execute the handover. The script:
   - stops `DevstackHelper` and the real daemon;
   - snapshots `state.db`, `config.yaml`, `system/helper.yaml`, hosts, and the
     three binaries to `dist/e2e/database-management/windows-handover/<stamp>/real`;
   - verifies every SHA-256;
   - creates `C:\Users\Aram\.devstack-e2e\system\pki`, grants LocalSystem full
     control, and rewrites the helper's `system_dir`/`ca_dir`/`root_cert` to
     that disposable tree while keeping `allow_sid`, the fixed pipe name, and
     the fixed proxy ports;
   - starts `DevstackHelper` and records `handover-state.json`.
4. Run only tests that are safe for the disposable Windows metadata home.
5. Restore with `rollback.ps1`; it restores the snapshot, verifies hashes,
   restarts the helper, and records `rollback-result.json`.

## Critical isolation gap

The Windows daemon still talks to the **real WSL `devstack` distro agent**,
whose data root is fixed at `/data/devstack`. The handover isolates the Windows
home, state database, config, helper CA, and helper service state, but it does
**not** isolate WSL service data or the Docker engine inside the real distro.
Therefore:

- Windows live-service destructive database/site/backup scenarios remain
  **BLOCKED** under option A until a disposable WSL distro clone or a verified
  `/data/devstack` snapshot/rollback is added.
- Use the Linux disposable model (`LIN-UBE`, Ubuntu WSL distro, disposable
  `/tmp/devstack-e2e-*` home, unique Docker network) for destructive scenarios.
- `WIN-REAL` is a fallback only with the verified baseline backup/rollback and
  explicit approval.

## Read-only check

```powershell
pwsh -NoProfile -File test/e2e/database-management/windows/read-only-check.ps1
```

Evidence defaults to
`dist/e2e/database-management/evidence/<stamp>/WIN-REAL-readonly/`. The script
runs `version`, `doctor`, `service list`, `site list`, and read-only
`db list`/`db user list`/`db backup list` for MySQL, PostgreSQL, and Redis,
captures exit codes, hashes the install files and hosts file, records listeners
on 3306/5432/6379/8082/7070, copies `state.db` only to a temp path for an
optional integrity check, and runs the Windows secret scanner.

## Handover plan (dry run)

```powershell
pwsh -NoProfile -File test/e2e/database-management/windows/handover-plan.ps1
```

This writes the plan and a hash manifest. It does not stop anything or copy
`state.db`.

## Execute handover (gated, not run in cycle 1)

```powershell
pwsh -NoProfile -File test/e2e/database-management/windows/handover-plan.ps1 `
  -Execute -Approve "I UNDERSTAND THE HANDOVER"
```

## Rollback

```powershell
pwsh -NoProfile -File test/e2e/database-management/windows/rollback.ps1 `
  -SnapshotDir dist/e2e/database-management/windows-handover/<stamp> `
  -Approve "I UNDERSTAND THE ROLLBACK"
```

Add `-RemoveDisposable` only after the restored real service is verified.

## Residual risks

- Fixed service ports mean the real services must be stopped during the
  disposable window.
- The helper pipe name is fixed, so two helpers cannot run simultaneously.
- WSL `/data/devstack` and the real Docker engine are shared unless a
  disposable distro clone is added.
- `state.db` may contain metadata; snapshots stay under the gitignored
  `dist/e2e/` tree and are never attached to evidence.
