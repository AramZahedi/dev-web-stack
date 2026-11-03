# Windows Serving and Cross-Platform Tuning E2E Plan

**Goal:** Prove the new Windows Docker nginx/Apache serving path and the
Balanced/Throughput/Custom tuning layers on a real Windows + WSL install.

**Scripts:**
- `test/windows/serving_e2e.ps1`
- `test/windows/performance_e2e.ps1`

**Prerequisites:** installed devstack, managed `devstack` WSL distro, a running
PHP site with a front controller, Docker nginx and Apache images available, and
`dashboard_auth` disabled for direct API session access (the script still uses a
session cookie and CSRF token).

## Serving matrix

| # | Scenario | Expected |
|---|---|---|
| S1 | `devstack vhost use apache --yes` | mode `apache`, config OK, Laravel `/up` 200, root 200 from Apache |
| S2 | Generated Apache vhost | `/sites/<project>/<docroot>` DocumentRoot, `AllowOverride All`, FCGI handler, no public TLS |
| S3 | `devstack vhost use builtin --yes` | mode builtin, site still 200, Apache container stopped |
| S4 | `devstack vhost use nginx --yes` | mode nginx, config OK, Laravel `/up` 200, root 200 from nginx |
| S5 | Generated nginx vhost | `listen 18080`, `/sites` root, FastCGI 43000, front-controller location, no public TLS |
| S6 | Direct nginx → Apache | switches automatically; mode `apache`, site 200 from Apache |
| S7 | Deactivate active Apache | switches to builtin, site 200; Apache can be reactivated |
| S8 | Uninstall active Apache | switches to builtin, site 200; Apache can be reinstalled |
| S9 | Restore Apache | Apache active and serving; `devstack doctor` all checks pass |
| S10 | WSL static site on the Docker provider | external static route targets `127.0.0.1:18080` and serves 200 |
| S11 | Add or stop a site while nginx/Apache is active | generated vhost is applied without restarting the container |

## Tuning matrix

| # | Scenario | Expected |
|---|---|---|
| T1 | Apply Balanced with host tuning | Windows Balanced plan, 49152/16384 dynamic ports, balanced throttling, 50% WSL memory, WSL files applied |
| T2 | Apply Custom | explicit Windows ports 40000/10001, High performance, `TcpTimedWaitDelay=30`, throttling off, WSL memory/processors/swap from custom values |
| T3 | Reset | Windows power plan/ports restored, WSL live sysctl restored to 4096 / 1000 / 32768–60999, WSL files removed, `.wslconfig` restored or removed |
| T4 | Restore Throughput | High performance plan and Throughput defaults active |
| T5 | Step output | WSL, Windows, and WSL-VM targets reported; restart-required flags surfaced |

## Commands

```powershell
$env:DEVSTACK_E2E_SITE = 'laravel-new.test'
$env:DEVSTACK_E2E_STATIC = 'e2e-static.test'
pwsh -NoProfile -File test/windows/serving_e2e.ps1
pwsh -NoProfile -File test/windows/performance_e2e.ps1
```

## Results (2026-11-03)

- Serving matrix: `SERVING_E2E_PASS`
- Tuning matrix: `TUNING_E2E_PASS`
- Vertical slice: `ALL ACCEPTANCE GATES PASSED (100% GREEN)`
- `devstack doctor`: all checks pass after both matrices.
- Apache and nginx both served the Laravel front controller; Apache honored
  `.htaccess`; public TLS and the dashboard stayed with the daemon.
- Direct nginx ↔ Apache switching succeeded without a builtin round trip.
- Deactivating or uninstalling the active Apache container switched serving to
  builtin before the container stopped and kept the PHP and static sites at 200.
- A WSL static site added while Apache was running was served immediately after
  the site-driven validate/reload, with no container restart.
- Switching Docker → builtin started the WSL static backend, published its route,
  and served the static site at 200; switching back to Apache restored the
  Docker route.
- A stale static site row with a missing project path no longer blocks a serving
  switch or a site delete.
- Balanced, Custom, reset, and final Throughput all verified on the real host.
- Deferred by design: VM-only installer rows INST-24/25/29 and DB E2E cycles
  9–15. Defender exclusions are skipped when the Defender module is unavailable.

## Regression gates

- `go test ./...`
- `go vet ./...`
- `npm run check`
- `npm run test -- --run`
- `npm run build`
- `pwsh -NoProfile -File test/windows/installer_test.ps1`
- `pwsh -NoProfile -File test/windows/lifecycle_test.ps1`
- `pwsh -NoProfile -File test/windows/vertical_slice.ps1`
- `bash test/release/release_windows_test.sh`
- `bash test/release/release_test.sh`
