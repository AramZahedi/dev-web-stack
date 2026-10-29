# Comprehensive Windows Exhaustive E2E & Publish-Readiness Review Plan

> **Goal**: Conduct an exhaustive, 100% complete review and live end-to-end verification of DevStack on Windows across CLI, daemon, helper, proxy, site runtimes, Docker infrastructure services, developer tooling, installer/uninstaller, diagnostics, and dashboard UI, resolving all discovered gaps to certify publish-ready stability.

## Phase 1: Static Quality & Type Safety Audit
- [ ] Task 1.1: Fix TypeScript type error in `ui/src/lib/api.ts` (`ServiceState` vs `'not-installed'`).
- [ ] Task 1.2: Run `npm --prefix ui run check` (`svelte-check`) and verify 0 errors, 0 warnings.
- [ ] Task 1.3: Run `go vet ./...` to verify Go static checks pass across all packages.
- [ ] Task 1.4: Codebase path hygiene check: verify no developer-specific hardcoded paths exist in tracked repo files (`C:\Users\Aram`, etc.).

## Phase 2: Windows Installer, Helper & Security Architecture Review
- [ ] Task 2.1: Audit `install.ps1`, `uninstall.ps1`, and `cmd/devstack-installer/main.go` for path correctness, permission escalation, scheduled tasks, firewall rules, and service registry entries.
- [ ] Task 2.2: Verify Windows Helper service (`devstack-helper.exe`) named pipe communication, ACL security, loopback port 80/443 forwarding, and CA root installation.
- [ ] Task 2.3: Verify WSL Agent mutual TLS authentication, private NAT IP pinning, and process group isolation.

## Phase 3: Exhaustive CLI Command Matrix Live E2E
- [ ] Task 3.1: **Core & Daemon Commands**:
  - `devstack version` (JSON and formatted output)
  - `devstack daemon status`
  - `devstack daemon stop` and `devstack daemon start`
- [ ] Task 3.2: **Doctor & Diagnostics**:
  - `devstack doctor` (verify all 24 check IDs, remediation output)
  - `devstack doctor --repair`
- [ ] Task 3.3: **Trust & Certificates**:
  - `devstack trust status`
  - `devstack trust repair`
- [ ] Task 3.4: **Runtimes & PHP Defaults**:
  - `devstack runtime list` and `devstack runtime available`
  - `devstack php default`, `devstack php extensions 8.4`, `devstack php info`
- [ ] Task 3.5: **Developer Tools & Shell Integration**:
  - `devstack tool list`
  - `devstack tool install composer`
  - `devstack shell-init powershell`, `devstack shell-init cmd`
  - Test `php.cmd -v` and `composer.cmd -v` directly from PowerShell
- [ ] Task 3.6: **Sites Lifecycle Management**:
  - Create Windows static site via CLI (`devstack site add`)
  - Create WSL PHP site via CLI (`devstack site add`)
  - Inspect sites with `devstack site list` and `devstack site get`
  - Test `devstack site stop`, `devstack site start`, `devstack site restart`
  - Test live HTTP and HTTPS traffic on both sites
  - Test `devstack site logs`
  - Delete test sites cleanly via `devstack site delete`
- [ ] Task 3.7: **Infrastructure Services Lifecycle Management**:
  - `devstack service list`
  - `devstack service install redis`
  - `devstack service activate redis`
  - Live TCP ping (`127.0.0.1:6379`)
  - `devstack service logs redis`
  - `devstack service config` (listing, viewing config content)
  - `devstack service restart redis`
  - `devstack service deactivate redis`
  - `devstack service uninstall redis --delete-data --yes`
- [ ] Task 3.8: **Vhosts & Web Proxy Modes**:
  - `devstack vhost status`
  - `devstack vhost preview`

## Phase 4: Developer Workflow Real-World Testing
- [ ] Task 4.1: Test real Composer workflow on Windows:
  - Create a temporary PHP project directory with `composer.json`.
  - Run `composer.cmd install` or `composer.cmd require` to verify network, SSL certs, and file writes.
- [ ] Task 4.2: Test `devstack exec` to run commands inside a site's configured runtime context.

## Phase 5: Dashboard UI Full Visual & Functional Audit (Playwright)
- [ ] Task 5.1: Sites page: empty state, site cards, search/filter, status badges.
- [ ] Task 5.2: Add Site modal:
  - Project source mode toggle (Windows vs WSL).
  - Slug generation from site name.
  - Prefilled path accuracy.
  - Segregated path browsing (WSL paths vs Windows paths).
- [ ] Task 5.3: Services page:
  - Deduplicated badges verification (`Not installed` only).
  - Install / Activate button responsiveness.
  - Logs drawer and configuration viewer.
  - Credentials reveal modal.
- [ ] Task 5.4: Runtimes page: catalog display, installed builds, migration modal.
- [ ] Task 5.5: Settings & Doctor page:
  - Doctor trigger and diagnostic results display.
  - Distro & helper status cards.
  - Dark / light theme toggle.
- [ ] Task 5.6: Browser console error audit: ensure zero uncaught errors or unhandled rejections across all routes.

## Phase 6: Full Test Suites & Final Production Build
- [ ] Task 6.1: Run `npm --prefix ui test` (Vitest suite).
- [ ] Task 6.2: Run `go test ./...` across all Go packages.
- [ ] Task 6.3: Rebuild dashboard production assets (`npm --prefix ui run build`).
- [ ] Task 6.4: Compile fresh production binaries:
  - `devstack.exe`
  - `devstack-helper.exe`
  - `devstack-installer.exe`
- [ ] Task 6.5: Verify clean working directory (`git status`) and certify publish-readiness.
