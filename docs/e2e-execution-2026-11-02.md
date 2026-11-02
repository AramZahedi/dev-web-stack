# E2E Execution Report — 2026-11-02

**Plan:** `docs/superpowers/plans/2026-11-02-exhaustive-e2e-program.md`
**Matrix:** `docs/e2e-lane-matrix.md`
**Inventory:** `docs/e2e-coverage.md`

## Environment

- Windows 11 Pro build 26300, Ryzen 9 7940HX, 31.2 GB RAM, ~210 GiB free.
- WSL 3.0.3, Ubuntu 26.04, real `devstack` distro.
- Real install: `C:\Users\Aram\.devstack`, MySQL and phpMyAdmin active, `static` stopped.
- All WSL distros share one network namespace; the real install holds 3306 and 8082.

## Completed and verified

| Area | Command | Result |
|---|---|---|
| Go unit/integration | `go test ./... -count=1` | PASS |
| Go vet | `go vet ./...` | PASS |
| UI type check | `npm run check` | 0 errors, 0 warnings |
| UI unit | `npm test` | 377 passed |
| Mock E2E (Chromium/Firefox/WebKit) | `npm run test:e2e` | 192/192 passed (64 per browser) |
| Linux builtin E2E | `E2E_SKIP_BUILD=1 bash test/e2e/linux.sh` | PASS |
| Database lifecycle E2E-01 | `cycle4-e2e01.sh` | 36/36 passed |
| Database core E2E-02 | `cycle4-e2e02.sh` | 97/97 passed |
| Database adversarial E2E-16 | `cycle4-e2e16.sh` | 82/82 passed |
| Site database E2E-03/04/05/06/17 | `cycle5-run-all.sh` plus E2E-06 rerun | 40/40, 33/33, 40/40, 19/19, 58/58 |
| Backup/import E2E-07 | `cycle6-e2e07.sh` | 130/130 passed |
| Backup edge cases E2E-18 | `cycle6-e2e18.sh` | 115/115 passed (near-1 GiB, disk-full, concurrency) |
| Redis ACL E2E-08 | `cycle7-e2e08.sh` | 53/54 passed; only the optional live Redis tab wait timed out |
| Redis migration E2E-19 | `cycle7-e2e19.sh` | 58/58 passed |
| Parallel Linux workers | 8 disposable WSL distros, distinct UI ports + backend slots | **8/8 passed** |
| Live database UI (disposable daemon) | `playwright test -c playwright.live.config.ts --grep "Site Detail\|metadata-only\|reserved database\|Add Site review"` | 4 passed |
| phpMyAdmin auto-login (live) | `npx playwright test -c playwright.live.config.ts --grep "phpMyAdmin auto-login"` | 1 passed |
| Real upgrade | `install.ps1 -Upgrade` | PASS; daemon/helper/agent healthy |
| WSL restart hosts recovery | `wsl --terminate devstack` then `devstack doctor` | PASS; `static.test` restored automatically |

## phpMyAdmin auto-login

- `config.user.inc.php` uses `auth_type = 'config'`, `user = 'root'`, and reads the root secret from `/run/secrets/root_password`.
- The active MySQL/MariaDB root secret is copied to `services/phpmyadmin/secrets/root_password` on PMA activation, engine switch, credential rotation, and restart.
- Restart recreates the PMA container so new mounts take effect.
- Live browser check opens `http://127.0.0.1:8082/`, sees **no login form**, and reports `Server: db:3306`.
- Verified effective PHP config: `auth=config user=root host=db port=3306 passlen=64`.
- No root password is written into the PHP config or evidence.

## Defects fixed during execution

| ID | Fix |
|---|---|
| D1 | TLS site create/start/restart now runs the trust preflight and repairs or fails closed. |
| D2 | Daemon startup restores the managed hosts block after upgrade. |
| D16 | Service activation ignores the generic 30-second client timeout; caller cancellation still works. |
| D17 | Helper config supports `allow_home` and `skip_trust_update` for disposable homes. |
| D18 | Linux E2E fixtures now live under the managed projects root. |
| D19 | `DEVSTACK_E2E_NO_PUBLISHED_PORTS` lets the disposable helper run all fixed-port services without touching the real install's 3306/5432/6379/8082. |
| D20 | phpMyAdmin auto-login as the active root user. |
| D21 | Cycle5 UI fixtures now live under the managed projects root and use the disposable helper home. |
| D22 | Cycle6 large-import wait extended from 10 to 30 minutes; near-1 GiB uploads queue a job but take longer than the old wait. |
| D23 | Real-install hosts block lost `static.test` after an upgrade or WSL restart. Fixed: the keepalive now calls `SyncHosts` on every WSL start; `doctor` stays green across `wsl --terminate devstack`. |
| D24 | Eight concurrent WSL workers on distinct 24xxx dashboard ports and backend slots: **8/8 passed** after partitioning HTTP and FastCGI ranges. |
| D25 | After importing/unregistering disposable WSL distros, Windows `wslrelay.exe` on 8082 hung. Terminating the managed `devstack` distro and restarting its agent recreated the relay; PMA returned HTTP 200 and auto-login passed again. |

## Blocked / not completed

| IDs | Reason | Next action |
|---|---|---|
| E2E-20 | The old PMA-config fixture still assumes a published high port; the product now uses no published ports and auto-login. | Covered by `pma-autologin.live.spec.ts`; update the fixture if a full config-reinstall run is required. |
| E2E-08 live Redis tab | The optional live Redis dashboard tab check timed out waiting for its harness `ui-stop` file; the 53 non-UI Redis assertions passed. | Fix the fixture wait/cleanup and rerun the single UI check. |
| DB-28 / cycles 9–15 | Paused by explicit user instruction. | Resume only with explicit approval. |
| INST-24/25/29 | No disposable Windows VM, Windows Sandbox, Hyper-V, VirtualBox, VMware, or QEMU on this host. `-TestSandbox` does not prove uninstall/trust/hosts. | Provision a disposable VM, then run Task 17A. |
| H1/H2/H3/H4/H5/H6/L1/L2 full 268-row runs | The 8-worker infrastructure is proven (8/8 concurrent Linux suites passed), but the lane-specific H1–H6/L1–L2 test suites are not yet implemented; this session ran the existing Linux/UI/API suites and the full database lifecycle matrix. | Implement Tasks 0–7 lane tests, then run the lane matrix. |

## Cleanup

- Temporary PMA probe scripts and debug homes were removed.
- Real install baseline restored; `devstack doctor` remains green.
- Database debug containers/networks in the Ubuntu engine were removed.
- No `laravel` database or user was dropped.

## Commits

- `3caedc3` feat(services): auto-login phpMyAdmin as active root user
- `b69e0ef` fix(agent): recreate phpMyAdmin on restart so auto-login mounts apply
- `1ab3117` docs(e2e): revise plan for serialized service slot and executed gates
- `8f31a28` fix(e2e): support disposable helper homes and long service activation
- `35eb441` test(e2e): add live phpMyAdmin auto-login coverage
- `76535cf` test(e2e): run live database UI against managed-root fixtures
- `18c48d5` test(e2e): add no-published-ports service slot and fix lifecycle fixtures
- `ce16f26` docs(e2e): record green database lifecycle and backup suites
- `b4cd626` test(e2e): use managed roots for cycle7 fixtures and record results
