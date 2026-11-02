# Complete E2E Coverage Inventory

> **Purpose:** the authoritative feature inventory for the full-coverage E2E
> testing program. Every user-visible, CLI, API, and lifecycle capability is
> grouped here with a stable ID, target layer, current coverage, priority, and
> the assertions a complete suite must prove.
>
> **Status:** planning baseline. This document records scope and coverage; it
> does not add tests.
>
> **Last updated:** 2026-11-02
>
> **Goal:** every capability below has a repeatable E2E test on every supported
> platform, or an explicit, reviewed waiver.

---

## 1. How to read this inventory

### 1.1 Test layers

| Layer | What it proves | Primary harness |
|---|---|---|
| **UI-Live** | Browser behavior against a real daemon, helper, agent, and real services | Playwright with a disposable live install |
| **UI-Mock** | Screen behavior and request/response handling without a real backend | Playwright against the in-page mock API |
| **CLI/API** | Command and HTTP contracts, side effects, status codes, and persistence | Go CLI, curl/REST, disposable home |
| **Installer/Lifecycle** | Fresh install, upgrade, rollback, reboot, uninstall, release packaging | PowerShell/bash in disposable VM or WSL |
| **Agent/WSL** | Windows-to-WSL preparation, serving, copying, Docker, port allocation, confinement | Live Windows + WSL2 install |

A complete E2E program needs all five layers. Mock UI tests do not prove backend
effects. A one-time live audit does not protect future changes unless it becomes
an automated gate.

### 1.2 Coverage codes

| Code | Meaning |
|---|---|
| **Live** | An automated live browser/API test exists today |
| **Mock** | An automated mock-browser test exists today |
| **System** | An automated CLI/installer/lifecycle script exists today |
| **Evidence** | Exercised in a completed live audit, but not a repeatable regression gate |
| **None** | No E2E coverage today |
| **N/A** | Unsupported by design or not applicable on the named platform |

Combinations such as **Mock + Evidence** mean the UI flow is automated against
a mock and was also exercised live once.

### 1.3 Priority

| Priority | Meaning |
|---|---|
| **P0** | Release-blocking core path; a regression breaks the product |
| **P1** | Important feature, destructive path, or recovery behavior |
| **P2** | Edge case, platform variant, quality gate, or scale/security depth |

### 1.4 Scope

In scope:

- Windows 10 22H2 and Windows 11 with WSL2
- Linux x86_64 with systemd
- Static, Node, and PHP sites
- Windows, WSL, and local source modes
- Builtin, nginx, and Apache serving modes
- nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis, and phpMyAdmin services
- Dashboard, CLI, HTTP API, daemon, helper, and WSL agent
- Fresh install, upgrade, rollback, reboot, repair, and uninstall
- Security boundaries, failure handling, accessibility, responsiveness, and
  cross-browser behavior

Out of scope:

- macOS and arm64
- Remote dashboard access and multi-user shared-workstation support as product
  features; local-user/SID isolation remains a P2 security test
- The retired all-in-one devstack Docker image
- Internal unit-only details that have no user, CLI, API, or lifecycle effect

### 1.5 Relationship to earlier E2E work

This inventory consolidates and supersedes the coverage map in:

- [2026-10-30 full dashboard/CLI E2E audit](superpowers/audits/2026-10-30-dashboard-full-e2e.md)
- [2026-10-30 E2E coverage gaps](superpowers/audits/2026-10-30-e2e-coverage-gaps.md)
- [2026-10-30 full dashboard/CLI coverage plan](superpowers/plans/2026-10-30-dashboard-full-e2e-coverage.md)
- [Database management E2E plan](superpowers/status/2026-10-31-database-management-e2e-plan.md)
- [Database management E2E runbook](superpowers/status/2026-10-31-database-management-e2e-runbook.md)

The 2026-10-30 audit was a broad live evidence run, not a permanent regression
gate. The database-management E2E run is paused after cycle 8; cycles 9-15
remain.

### 1.6 Maintenance rules

- IDs are permanent; never renumber or reuse one.
- Move a row to **Live** or **System** only when the automated test is committed
  and green in the required gate.
- **Evidence** is a temporary state; link the audit or evidence directory and
  plan its conversion to automation.
- A new user-visible, CLI, API, or lifecycle capability requires a new row with
  layer, coverage, and priority before merge.
- A waiver needs an owner, reason, and expiry date; waivers are not permanent
  coverage.
- Update the summary counts whenever rows or statuses change.

---

## 2. Domain map

| # | Domain | Primary surfaces | Backend owners |
|---|---|---|---|
| 01 | Installation and lifecycle | Installers, upgrade, uninstall, release | `install.ps1`, `install.sh`, helper, WSL, systemd |
| 02 | Authentication and dashboard shell | Login, session, navigation, theme, global UX | API middleware, Svelte shell |
| 03 | Sites list | Search, filters, summaries, quick actions | Sites API |
| 04 | Add Site | Project, runtime, options, review, create | Sites, pathmode, runtimes, databases |
| 05 | Site detail | Config, source mode, build, PHP/Node, logs, terminal | Sites, backend, agent, terminal |
| 06 | Services | Install, lifecycle, credentials, config, performance | Service control, Docker |
| 07 | Databases and service detail | DBs, users, grants, passwords, backups, jobs | Database service, dbproto |
| 08 | Runtimes and toolchains | PHP/Node builds, defaults, Composer, shims | Runtime, catalog, tooling |
| 09 | Doctor, trust, hosts, ports | Diagnostics, repair, CA trust, hosts, listeners | Doctor, trust, hosts, ports |
| 10 | Settings, serving, performance | Ports, serving modes, vhosts, performance | Settings, vhost, performance |
| 11 | CLI, API, daemon, agent | Every command/endpoint, lifecycle, persistence | CLI, API, daemon, agent |
| 12 | Cross-cutting quality | Security, failure, accessibility, platform matrix | All layers |

---

## 3. Coverage at a glance

| Domain | Features | P0 | P1 | P2 |
|---|---:|---:|---:|---:|
| 01 Installation and lifecycle | 30 | 18 | 11 | 1 |
| 02 Authentication and dashboard shell | 19 | 5 | 11 | 3 |
| 03 Sites list | 11 | 2 | 7 | 2 |
| 04 Add Site | 26 | 15 | 11 | 0 |
| 05 Site detail | 25 | 11 | 13 | 1 |
| 06 Services | 18 | 9 | 9 | 0 |
| 07 Databases and service detail | 28 | 16 | 11 | 1 |
| 08 Runtimes and toolchains | 18 | 10 | 7 | 1 |
| 09 Doctor, trust, hosts, ports | 17 | 9 | 8 | 0 |
| 10 Settings, serving, performance | 20 | 7 | 11 | 2 |
| 11 CLI, API, daemon, agent | 31 | 25 | 6 | 0 |
| 12 Cross-cutting quality | 25 | 9 | 8 | 8 |
| **Total** | **268** | **136** | **113** | **19** |

### Current evidence mix

| Coverage shape | Features | Interpretation |
|---|---:|---|
| Mock + Evidence | 128 | UI flow is automated against a mock and was exercised live once |
| Evidence only | 57 | Live audit only; no repeatable regression gate |
| Mock only | 27 | Automated UI flow without live backend proof |
| System + Evidence | 19 | Lifecycle/CLI script plus prior live evidence |
| None | 18 | No E2E coverage today |
| System only | 10 | Automated lifecycle/CLI script without live UI proof |
| Live | 5 | Automated live test today |
| Live + Evidence | 2 | Automated live test plus broader audit evidence |
| Mock + Live + Evidence | 2 | Fullest current coverage |

The largest gap is not missing UI automation; it is converting **evidence-only**
and **mock-only** coverage into repeatable live tests with backend assertions.

## 4. Domain inventory

### 01 · Installation and lifecycle

**Coverage posture:** automated Windows/Linux lifecycle scripts and a completed
live audit exist; clean-machine, real reboot, and rollback/purge matrices are
still incomplete.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-INST-01 | Windows installer entry points: standalone exe, release ZIP, source checkout | Installer | System + Evidence | P0 |
| E2E-INST-02 | Windows prerequisite checks: build, x64, virtualization, WSL 2.0+ | Installer | System + Evidence | P0 |
| E2E-INST-03 | Elevation/UAC and refusal when not elevated | Installer | System | P0 |
| E2E-INST-04 | WSL feature enablement, reboot requirement, resume task | Installer | Evidence | P1 |
| E2E-INST-05 | Managed `devstack` WSL distro import and provisioning | Installer | System + Evidence | P0 |
| E2E-INST-06 | WSL Docker Engine and agent provisioning | Installer | System + Evidence | P0 |
| E2E-INST-07 | Helper service registration, DACL, firewall rules | Installer | System + Evidence | P0 |
| E2E-INST-08 | Install-time hosts block and local CA trust | Installer | System + Evidence | P0 |
| E2E-INST-09 | Layout, config, state database, and protected directories | Installer | System + Evidence | P0 |
| E2E-INST-10 | Daemon autostart and logon task registration | Installer | System + Evidence | P1 |
| E2E-INST-11 | `C:\devstack` links and `README.txt` | Installer | System | P1 |
| E2E-INST-12 | Legacy `C:\devstack` install detection | Installer | System | P1 |
| E2E-INST-13 | Linux prerequisites and port availability | Installer | System | P0 |
| E2E-INST-14 | Linux install from release and source | Installer | System | P0 |
| E2E-INST-15 | systemd units, helper, forwarders, autostart | Installer | System | P0 |
| E2E-INST-16 | Linux `init`, CA/hosts registration, first run | Installer | System | P0 |
| E2E-INST-17 | Upgrade preflight and backup | Installer | System + Evidence | P0 |
| E2E-INST-18 | Upgrade staging, binary replacement, agent update | Installer | System + Evidence | P0 |
| E2E-INST-19 | Upgrade health verification and rollback on failure | Installer | Evidence | P1 |
| E2E-INST-20 | Upgrade database backup and state migration | Installer | Evidence | P1 |
| E2E-INST-21 | Protocol/version mismatch and compatibility policy | Installer | Evidence | P1 |
| E2E-INST-22 | Reboot/resume and second-boot autostart | Installer | Evidence | P1 |
| E2E-INST-23 | `-DryRun` and `-TestSandbox` isolation | Installer | System | P1 |
| E2E-INST-24 | Uninstall while preserving project data | Installer | Evidence | P0 |
| E2E-INST-25 | Uninstall with data and WSL distro purge | Installer | System + Evidence | P1 |
| E2E-INST-26 | Release packaging, checksums, and install from archive | Installer | System | P0 |
| E2E-INST-27 | Upgrade from the previous published release | Installer | Evidence | P1 |
| E2E-INST-28 | Windows 10 22H2 and a second Windows 11 build/locale | Installer | None | P2 |
| E2E-INST-29 | Clean Windows VM install from the release artifact | Installer | None | P0 |
| E2E-INST-30 | Clean Linux x86_64 systemd install from the release artifact | Installer | None | P0 |

**Key assertions**

- A failed install leaves no half-registered helper, service, hosts entry, trust
  record, logon task, or WSL distro.
- A failed upgrade restores the previous binaries, config, state, and agent
  health.
- Uninstall removes only the selected scope; projects and logs survive the
  default path.
- Dry-run and sandbox never touch the real home, hosts, trust, firewall, or
  distro.
- Every installer action is idempotent on a second run.

---

### 02 · Authentication and dashboard shell

**Coverage posture:** automated live auth/CSRF coverage plus broad mock UI
coverage; session-expiry depth, cross-browser behavior, and the auth-on matrix
still need a repeatable live gate.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-AUTH-01 | Install-token exchange from URL fragment | UI-Live | Live | P0 |
| E2E-AUTH-02 | Session cookie creation and reuse | UI-Live | Live | P0 |
| E2E-AUTH-03 | CSRF token required on every mutation | UI-Live | Live | P0 |
| E2E-AUTH-04 | Session expiry and locked-dashboard banner | UI-Live | Live | P1 |
| E2E-AUTH-05 | Invalid token, missing session, invalid CSRF rejection | UI-Live | Live | P0 |
| E2E-AUTH-06 | Loopback-only API, host allowlist, no CORS | UI-Live | Evidence | P0 |
| E2E-AUTH-07 | Navigation across all dashboard sections | UI-Live | Mock + Evidence | P1 |
| E2E-AUTH-08 | Deep links, browser back/forward, 404 route | UI-Live | Mock | P1 |
| E2E-AUTH-09 | Mobile drawer open/close/backdrop/Escape | UI-Live | Mock | P1 |
| E2E-AUTH-10 | Skip link and visible keyboard focus | UI-Live | Mock | P1 |
| E2E-AUTH-11 | Dialog Escape, focus trap, focus restoration | UI-Live | Mock | P1 |
| E2E-AUTH-12 | System/light/dark theme and persistence | UI-Live | Mock | P2 |
| E2E-AUTH-13 | Daemon, helper, and CA fingerprint status | UI-Live | Mock | P1 |
| E2E-AUTH-14 | SSE refresh, reconnect, and expired-session behavior | UI-Live | Evidence | P1 |
| E2E-AUTH-15 | Toasts, skeletons, empty states, error banners | UI-Live | Mock | P1 |
| E2E-AUTH-16 | Offline/daemon-unavailable and retry | UI-Live | Mock + Evidence | P1 |
| E2E-AUTH-17 | Responsive layout at desktop/tablet/mobile widths | UI-Live | Mock | P2 |
| E2E-AUTH-18 | Keyboard-only navigation, labels, contrast | UI-Live | Mock | P2 |
| E2E-AUTH-19 | `devstack ui` re-authentication and fragment cleanup | UI-Live | Evidence | P1 |

**Key assertions**

- The token never remains in browser history, traces, screenshots, or logs.
- Mutations fail closed without a session or with a stale CSRF token.
- The dashboard is unreachable from the WSL network namespace.
- An expired session produces a clear locked state, not an infinite SSE retry
  loop.
- The mobile drawer and every dialog restore focus and scroll state.

---

### 03 · Sites list

**Coverage posture:** strong mock coverage and prior live evidence; no dedicated
live mutation suite.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-SITE-01 | Site list and summary counts | UI-Live | Mock + Evidence | P0 |
| E2E-SITE-02 | Search by name and domain | UI-Live | Mock | P1 |
| E2E-SITE-03 | Status filters and clear-filters | UI-Live | Mock | P1 |
| E2E-SITE-04 | Kind, status, source-mode, and health badges | UI-Live | Mock + Evidence | P1 |
| E2E-SITE-05 | Site URL and open-in-new-tab action | UI-Live | Mock + Evidence | P1 |
| E2E-SITE-06 | Quick start, stop, and restart | UI-Live | Mock + Evidence | P0 |
| E2E-SITE-07 | Delete confirmation and linked-database warning | UI-Live | Mock + Evidence | P1 |
| E2E-SITE-08 | Empty, loading, and error states | UI-Live | Mock | P1 |
| E2E-SITE-09 | Mobile card layout and no horizontal overflow | UI-Live | Mock | P2 |
| E2E-SITE-10 | SSE-driven refresh after lifecycle changes | UI-Live | Evidence | P1 |
| E2E-SITE-11 | Resilient rendering when runtime inventory fails | UI-Live | Mock | P2 |

**Key assertions**

- A quick action updates the row and summary only after the backend confirms.
- A failed action leaves the previous status visible and surfaces the exact
  error.
- Search and filters combine predictably and survive refresh.
- Deleting a site never deletes project files.

---

### 04 · Add Site

**Coverage posture:** strong mock coverage plus prior live evidence for PHP/WSL
and validation; the full option matrix still needs automated live coverage.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-ADD-01 | Three-step wizard, back, cancel, and edit-back navigation | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-02 | Site name validation and automatic slugification | UI-Live | Mock | P0 |
| E2E-ADD-03 | Site kinds: static, Node, PHP | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-04 | Source modes: local, Windows, WSL | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-05 | Manual absolute project path | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-06 | Folder browser navigation and child selection | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-07 | Create new folder inside the managed root | UI-Live | Mock + Evidence | P1 |
| E2E-ADD-08 | Reject root, outside, sibling prefix, and `..` paths | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-09 | Namespace validation: drive path, WSL UNC, local POSIX | UI-Live | Mock | P0 |
| E2E-ADD-10 | Reject symlink/reparse escapes and root aliases | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-11 | Document root validation | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-12 | SPA fallback enable and fallback file | UI-Live | Mock | P1 |
| E2E-ADD-13 | Node toolchain: inherit, exact version, preflight, install | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-14 | PHP build: inherit, exact build, extensions | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-15 | Front controller file and mode | UI-Live | Mock + Evidence | P1 |
| E2E-ADD-16 | Apache `AllowOverride` | UI-Live | Mock + Evidence | P1 |
| E2E-ADD-17 | Static build profile: enable, Node version, command, output | UI-Live | Mock + Evidence | P1 |
| E2E-ADD-18 | Build environment variables | UI-Live | Mock | P1 |
| E2E-ADD-19 | Site environment variables | UI-Live | Mock | P1 |
| E2E-ADD-20 | HTTPS, force HTTPS, and `www` alias | UI-Live | Mock + Evidence | P1 |
| E2E-ADD-21 | Create a new database and user during site creation | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-22 | Link an existing database or user | UI-Live | Mock + Evidence | P1 |
| E2E-ADD-23 | Review step and create/start behavior | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-24 | Post-create database password modal | UI-Live | Mock + Evidence | P1 |
| E2E-ADD-25 | Duplicate name/path, runtime missing, and create failure | UI-Live | Mock + Evidence | P0 |
| E2E-ADD-26 | Windows static defaults to NTFS; PHP/Node default to WSL | UI-Live | Mock + Evidence | P1 |

**Key assertions**

- Every invalid path is rejected before any filesystem, backend, or database
  mutation.
- Creating a site is atomic: no orphan site record, backend process, route,
  hosts entry, or database link survives a failure.
- A missing Node build is installed only after explicit preflight and
  confirmation.
- Review shows no password; the generated password is revealed only after
  successful creation.
- Switching source mode or site kind clears stale runtime/path selections.

---

### 05 · Site detail

**Coverage posture:** broad mock coverage and prior live evidence, including
PHP configuration, source copy, build, terminal, and logs; a live regression
suite is still missing.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-DETAIL-01 | Overview, status, URL, certificate, and source mode | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-02 | Copy URL and open site | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-03 | Start, stop, and restart | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-04 | Delete site and confirmation | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-05 | Edit document root, start command, front controller, `AllowOverride`, SPA | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-06 | Edit site environment variables | UI-Live | Mock | P1 |
| E2E-DETAIL-07 | Edit build profile and build environment | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-08 | Source-mode change with stopped-site guard | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-09 | Source copy Windows to WSL and WSL to Windows | UI-Live | Evidence | P0 |
| E2E-DETAIL-10 | Source-copy failure and original-source preservation | UI-Live | Evidence | P1 |
| E2E-DETAIL-11 | Run static build and show duration/output | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-12 | Build log tail and refresh | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-13 | Failed build result and rollback | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-14 | Effective PHP configuration | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-15 | PHP version/build switch with validation and rollback | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-16 | PHP extensions and per-site `php.ini` | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-17 | PHP execution in site context | UI-Live | Evidence | P1 |
| E2E-DETAIL-18 | Node version pin, inherit, and switch | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-19 | Stale Node pin install-first and ABI rebuild warning | UI-Live | Mock | P1 |
| E2E-DETAIL-20 | Logs snapshot, live stream, pause/resume, tail | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-21 | Terminal ticket, WebSocket PTY, site environment | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-22 | Terminal resize, Ctrl+C, reconnect, idle timeout | UI-Live | Evidence | P2 |
| E2E-DETAIL-23 | Database links, reveal/reset password, `.env` snippet | UI-Live | Mock + Evidence | P0 |
| E2E-DETAIL-24 | Vhost preview | UI-Live | Mock + Evidence | P1 |
| E2E-DETAIL-25 | Site not found, load failure, and stale route ref | UI-Live | Mock | P1 |

**Key assertions**

- A configuration change is validated then swapped; a failure keeps the previous
  working configuration and process.
- Source copy never deletes or mutates the original source on failure.
- Build failure does not leave a partially served output.
- PHP switch does not drop the live FPM master or serve the wrong generation.
- Revealed passwords are cleared on route change, refresh, and link-set change.
- Terminal sessions close cleanly at the configured idle and maximum limits.

---

### 06 · Services

**Coverage posture:** broad mock and live audit coverage for all seven services;
the repeatable suite needs real Docker lifecycle and failure tests.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-SVC-01 | Service catalog and groups: web, MySQL family, standalone | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-02 | Install/pull/scaffold a service | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-03 | Activate a service | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-04 | Deactivate a service | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-05 | Restart a service | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-06 | Uninstall with keep-data or delete-data choice | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-07 | Exclusive web and MySQL-family conflicts | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-08 | Health, installed, and active badges | UI-Live | Mock + Evidence | P1 |
| E2E-SVC-09 | Service container logs | UI-Live | Mock + Evidence | P1 |
| E2E-SVC-10 | Credentials reveal, copy, and rotate | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-11 | phpMyAdmin launch only when active | UI-Live | Mock + Evidence | P1 |
| E2E-SVC-12 | Config file list, view, diff, apply, reset, and keep/use/merge conflict resolution | UI-Live | Mock + Evidence | P0 |
| E2E-SVC-13 | Performance panel: profile, preview, apply, reset, benchmark | UI-Live | Mock + Evidence | P1 |
| E2E-SVC-14 | Deactivation impact: linked sites, backups, jobs | UI-Live | Mock + Evidence | P1 |
| E2E-SVC-15 | SSE refresh after service changes | UI-Live | Evidence | P1 |
| E2E-SVC-16 | Loading, empty, error, and activation-in-flight states | UI-Live | Mock | P1 |
| E2E-SVC-17 | Docker daemon down, image pull failure, health timeout | UI-Live | None | P1 |
| E2E-SVC-18 | Port conflict and stale container recovery | UI-Live | None | P1 |

**Key assertions**

- Only one member of an exclusive group can own its ports.
- Uninstall never deletes data unless the exact destructive confirmation is
  supplied.
- Config apply validates and reloads the live server; failure rolls back.
- Credentials are never present in list responses, logs, or page HTML before
  reveal.
- A failed activation leaves the previous active service untouched.

---

### 07 · Databases and service detail

**Coverage posture:** deepest existing coverage: mock UI, live audit evidence,
and a disposable-WSL database E2E harness. The harness is paused after cycle 8;
cycles 9-15 and a permanent gate remain.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-DB-01 | Service-detail tabs and deep-link query state | UI-Live | Mock + Evidence | P1 |
| E2E-DB-02 | List databases with inventory and stale-data states | UI-Live | Mock + Evidence | P0 |
| E2E-DB-03 | Create database with safe-name validation | UI-Live | Mock + Evidence | P0 |
| E2E-DB-04 | Rename PostgreSQL database | UI-Live | Mock + Evidence | P1 |
| E2E-DB-05 | Drop database with typed confirmation | UI-Live | Mock + Evidence | P0 |
| E2E-DB-06 | Backup-first deletion and protected names | UI-Live | Mock + Evidence | P0 |
| E2E-DB-07 | Linked database block until site stopped | UI-Live | Mock + Evidence | P0 |
| E2E-DB-08 | List users with site attribution | UI-Live | Mock + Evidence | P0 |
| E2E-DB-09 | Create user with grants | UI-Live | Mock + Evidence | P0 |
| E2E-DB-10 | Edit user, grants, and revoke | UI-Live | Mock + Evidence | P0 |
| E2E-DB-11 | Delete user with typed confirmation | UI-Live | Mock + Evidence | P0 |
| E2E-DB-12 | Set, reveal, copy, rotate, and reset passwords | UI-Live | Mock + Live + Evidence | P0 |
| E2E-DB-13 | Metadata-only and external users | UI-Live | Mock + Live + Evidence | P0 |
| E2E-DB-14 | Protected users cannot be edited/deleted | UI-Live | Mock + Evidence | P1 |
| E2E-DB-15 | Create and list backups | UI-Live | Mock + Evidence | P0 |
| E2E-DB-16 | Download and delete backups | UI-Live | Mock + Evidence | P1 |
| E2E-DB-17 | Restore backup with overwrite confirmation | UI-Live | Mock + Evidence | P0 |
| E2E-DB-18 | Import SQL into a database | UI-Live | Mock + Evidence | P0 |
| E2E-DB-19 | Export database | UI-Live | Mock + Evidence | P1 |
| E2E-DB-20 | Corrupt, empty, truncated, partial, binary, and non-SQL dumps | UI-Live | System | P1 |
| E2E-DB-21 | Job progress, failure, cancellation, refresh recovery | UI-Live | Mock + Evidence | P0 |
| E2E-DB-22 | SSE inventory refresh and overlapping responses | UI-Live | Mock + Evidence | P1 |
| E2E-DB-23 | Redis password and ACL-file persistence | UI-Live | Mock + Evidence | P1 |
| E2E-DB-24 | Site database link, unlink, and environment resolution | UI-Live | Mock + Evidence | P0 |
| E2E-DB-25 | phpMyAdmin active-database follow and login | UI-Live | Evidence | P1 |
| E2E-DB-26 | Keyboard, focus, contrast, and responsive database screens | UI-Live | Mock | P2 |
| E2E-DB-27 | MariaDB and MySQL-family parity | UI-Live | System + Evidence | P1 |
| E2E-DB-28 | Database E2E cycles 9-15 completion | System | None | P1 |

**Key assertions**

- Every destructive operation requires the exact typed confirmation and creates
  a backup first when data is deleted.
- A metadata-only link never exposes or invents a password.
- A failed job leaves no partial database, user, grant, backup, or link.
- Import rejects corrupt/truncated input without replacing the existing data.
- A browser refresh mid-job reconnects to the persisted job state.
- Site environment values override database links only in the documented order.

---

### 08 · Runtimes and toolchains

**Coverage posture:** strong mock and live audit coverage; install/remove and
migration need a real catalog/network matrix and resilience tests.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-RT-01 | List installed runtimes and site usage | UI-Live | Mock + Evidence | P0 |
| E2E-RT-02 | List available catalog builds | UI-Live | Mock + Evidence | P0 |
| E2E-RT-03 | Install runtime with checksum/size verification | UI-Live | Mock + Evidence | P0 |
| E2E-RT-04 | Remove runtime | UI-Live | Mock + Evidence | P0 |
| E2E-RT-05 | PHP build in use; migrate sites before removal | UI-Live | Mock + Evidence | P0 |
| E2E-RT-06 | PHP line defaults editor | UI-Live | Mock + Evidence | P1 |
| E2E-RT-07 | Machine-wide PHP CLI default | UI-Live | Mock + Evidence | P0 |
| E2E-RT-08 | Machine-wide Node CLI default | UI-Live | Mock + Evidence | P0 |
| E2E-RT-09 | Global CLI `php.ini` | UI-Live | Mock + Evidence | P1 |
| E2E-RT-10 | Node release refresh, offline, stale, and cache | UI-Live | Mock + Evidence | P1 |
| E2E-RT-11 | Node releases by major, newest patch, expand | UI-Live | Mock + Evidence | P1 |
| E2E-RT-12 | Exact Node release preflight and install | UI-Live | Mock + Evidence | P0 |
| E2E-RT-13 | Composer install, update, and remove | UI-Live | Mock + Evidence | P1 |
| E2E-RT-14 | Global `php`, `composer`, `node`, `npm`, `npx` shims | CLI/API | Evidence | P0 |
| E2E-RT-15 | Site shell overrides PHP/Node versions | CLI/API | Evidence | P0 |
| E2E-RT-16 | `shell`, `exec`, `shell-init` context | CLI/API | Evidence | P1 |
| E2E-RT-17 | Download failure, checksum mismatch, corrupt archive | UI-Live | None | P1 |
| E2E-RT-18 | Concurrent install/remove and disk-full behavior | UI-Live | None | P2 |

**Key assertions**

- A runtime is staged and verified before it becomes visible or selectable.
- A checksum/size mismatch leaves no partial install and a stable error.
- Removing an in-use PHP build is blocked until every site is migrated.
- Global shims and site shells resolve the documented PHP/Node version and ini.
- A failed migration keeps every site on its previous working build.

---

### 09 · Doctor, trust, hosts, ports

**Coverage posture:** broad mock and live audit coverage; failure injection for
each repair class and clean-machine trust/hosts behavior still need automation.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-DOC-01 | Run all checks and render status summary | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-02 | Repair and rerun checks | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-03 | Remediation text and last-run time | UI-Live | Mock + Evidence | P1 |
| E2E-DOC-04 | Helper and local CA checks | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-05 | Hosts block check and repair | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-06 | Managed projects-root check | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-07 | Trust check, repair, and removal | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-08 | Port ownership check | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-09 | Runtime, tool, and disk checks | UI-Live | Mock + Evidence | P1 |
| E2E-DOC-10 | WSL, agent, Docker, forwarding, and WSL-port checks | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-11 | Serving-mode and vhost checks | UI-Live | Mock + Evidence | P1 |
| E2E-DOC-12 | PHP build, config, port, and master checks | UI-Live | Mock + Evidence | P0 |
| E2E-DOC-13 | Listener port change and restart guidance | UI-Live | Mock + Evidence | P1 |
| E2E-DOC-14 | Firewall and helper-forwarder behavior | Installer | Evidence | P1 |
| E2E-DOC-15 | Injected hosts drift and repair | System | Evidence | P1 |
| E2E-DOC-16 | Injected trust, port, runtime, vhost, and PHP-config drift | System | None | P1 |
| E2E-DOC-17 | Doctor JSON/API parity and stable check IDs | CLI/API | Evidence | P1 |

**Key assertions**

- Every failing check names the exact remediation and does not claim a repair it
  did not perform.
- Repair is idempotent and safe to run twice.
- Trust removal is reflected immediately in status, not inferred from the action
  response.
- Injected drift is detected, repaired, and verified by a second doctor run.

---

### 10 · Settings, serving, performance

**Coverage posture:** broad mock and live audit coverage; external nginx/Apache
on Linux, host tuning, and performance under load need a real platform matrix.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-SET-01 | Machine configuration readout | UI-Live | Mock + Evidence | P1 |
| E2E-SET-02 | Auto-start, graceful stop, log retention, max body | UI-Live | Mock + Evidence | P1 |
| E2E-SET-03 | WSL distro information | UI-Live | Mock + Evidence | P2 |
| E2E-SET-04 | Listener ports: HTTP, HTTPS, dashboard/API | UI-Live | Mock + Evidence | P0 |
| E2E-SET-05 | Invalid, out-of-range, and conflicting ports | UI-Live | Mock + Evidence | P0 |
| E2E-SET-06 | Fixed public ports on Windows | UI-Live | Mock + Evidence | P1 |
| E2E-SET-07 | Serving mode: builtin, nginx, Apache | UI-Live | Mock + Evidence | P0 |
| E2E-SET-08 | Serving status, sync, and preview | UI-Live | Mock + Evidence | P1 |
| E2E-SET-09 | nginx/Apache prerequisites and bridge include | UI-Live | Evidence | P1 |
| E2E-SET-10 | Vhost config validation, reload, and rollback | UI-Live | Mock + Evidence | P0 |
| E2E-SET-11 | Default-server conflict handling | UI-Live | Evidence | P1 |
| E2E-SET-12 | TLS custody, `.htaccess`, and custom document root | UI-Live | Evidence | P1 |
| E2E-SET-13 | PHP CLI default, Node CLI default, Composer | UI-Live | Mock + Evidence | P0 |
| E2E-SET-14 | Trust repair/removal | UI-Live | Mock + Evidence | P0 |
| E2E-SET-15 | Daemon information and uninstall guidance | UI-Live | Mock + Evidence | P2 |
| E2E-SET-16 | Performance profile, preview, apply, reset | UI-Live | Mock + Evidence | P1 |
| E2E-SET-17 | Benchmark run, history, and detail | UI-Live | Mock + Evidence | P1 |
| E2E-SET-18 | Host tuning and PHP-FPM pooling | System | Evidence | P1 |
| E2E-SET-19 | External nginx/Apache on Linux with PHP sites | UI-Live | Evidence | P0 |
| E2E-SET-20 | Serving-mode rollback on invalid config or reload failure | UI-Live | None | P1 |

**Key assertions**

- Changing a listener port reports restart-required and the daemon returns on the
  new port after restart.
- Switching serving mode never leaves two owners of ports 80/443.
- Invalid vhost config is rejected and the previous server configuration
  remains live.
- Performance preview never writes; apply and reset are reversible.
- Benchmark results are measured, not fabricated, and survive restart.

---

### 11 · CLI, API, daemon, agent

**Coverage posture:** broad live audit and system-script evidence; many commands
and endpoints are not in a permanent automated parity gate.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-CLI-01 | Global flags: `--home`, `--json`, `--yes`, exit codes | CLI/API | Evidence | P0 |
| E2E-CLI-02 | `init` | CLI/API | Evidence | P0 |
| E2E-CLI-03 | `daemon start/run/status/restart/stop` | CLI/API | System + Evidence | P0 |
| E2E-CLI-04 | Site add/list/show/start/stop/restart | CLI/API | System + Evidence | P0 |
| E2E-CLI-05 | Site add option matrix and validation | CLI/API | Evidence | P0 |
| E2E-CLI-06 | Site source copy | CLI/API | Evidence | P0 |
| E2E-CLI-07 | Site node/php/ini/ext | CLI/API | Evidence | P0 |
| E2E-CLI-08 | Site build/logs/open/remove | CLI/API | Evidence | P0 |
| E2E-CLI-09 | Runtime list/available/install/remove/migrate | CLI/API | Evidence | P0 |
| E2E-CLI-10 | PHP and Node defaults | CLI/API | Evidence | P0 |
| E2E-CLI-11 | Tool list/install/update/remove | CLI/API | Evidence | P1 |
| E2E-CLI-12 | Shell/exec/shell-init | CLI/API | Evidence | P1 |
| E2E-CLI-13 | Trust and vhost commands | CLI/API | Evidence | P1 |
| E2E-CLI-14 | Service lifecycle, logs, credentials, uninstall | CLI/API | Evidence | P0 |
| E2E-CLI-15 | Service config list/show/diff/apply/reset | CLI/API | Evidence | P0 |
| E2E-CLI-16 | DB core: list/create/rename/drop | CLI/API | System + Evidence | P0 |
| E2E-CLI-17 | DB users, grants, passwords | CLI/API | System + Evidence | P0 |
| E2E-CLI-18 | DB backups, restore, import, export | CLI/API | System + Evidence | P0 |
| E2E-CLI-19 | DB link/unlink and database environment | CLI/API | System + Evidence | P0 |
| E2E-CLI-20 | `ui`, `uninstall`, `version` | CLI/API | Evidence | P1 |
| E2E-CLI-21 | API auth, session, CSRF, host guard | CLI/API | Live + Evidence | P0 |
| E2E-CLI-22 | API sites and source | CLI/API | Evidence | P0 |
| E2E-CLI-23 | API services, config, performance | CLI/API | Evidence | P0 |
| E2E-CLI-24 | API databases, users, backups, jobs | CLI/API | System + Evidence | P0 |
| E2E-CLI-25 | API runtimes, tools, defaults | CLI/API | Evidence | P0 |
| E2E-CLI-26 | API doctor, trust, vhosts, settings, fs, events | CLI/API | Evidence | P0 |
| E2E-CLI-27 | API status codes, error codes, and JSON shapes | CLI/API | Evidence | P0 |
| E2E-CLI-28 | SSE events and terminal WebSocket | CLI/API | Evidence | P1 |
| E2E-CLI-29 | Daemon persistence, reconcile-once, crash recovery | CLI/API | Evidence | P0 |
| E2E-CLI-30 | Agent health, site ops, copy, Docker, ports, confinement | Agent/WSL | Evidence | P0 |
| E2E-CLI-31 | Protocol/version mismatch and reconnect | Agent/WSL | Evidence | P1 |

**Key assertions**

- Every CLI command has matching human and `--json` output and a stable exit
  code.
- Every OpenAPI method returns the documented status, error code, and shape.
- CLI and API produce the same persisted state as the dashboard.
- A daemon crash or restart does not duplicate running sites or lost desired
  state.
- The agent rejects out-of-root, wrong-distro, and stale-token requests.

---

### 12 · Cross-cutting quality

**Coverage posture:** many individual assertions exist; the full matrix of
security, failure, concurrency, accessibility, platform, and scale is not yet a
single gate.

| ID | Capability | Layer | Existing coverage | Priority |
|---|---|---|---|---|
| E2E-XC-01 | Managed-root confinement and symlink/reparse escapes | All | Mock + Evidence | P0 |
| E2E-XC-02 | CSRF, host guard, loopback-only, no CORS | All | Live + Evidence | P0 |
| E2E-XC-03 | Secrets never in HTML, logs, traces, screenshots, API lists | All | Mock + Evidence | P0 |
| E2E-XC-04 | Password masking, reveal audit, rotation, typed confirms | All | Mock + Evidence | P0 |
| E2E-XC-05 | Persistence across restart, upgrade, and rollback | All | Evidence | P0 |
| E2E-XC-06 | Atomic writes and idempotent operations | All | Evidence | P0 |
| E2E-XC-07 | Daemon/helper/Docker/WSL/network failure and retry | All | Evidence | P1 |
| E2E-XC-08 | Port, runtime, permission, and disk conflict handling | All | Evidence | P1 |
| E2E-XC-09 | Concurrent actions, stale responses, cancellation | All | Mock + Evidence | P1 |
| E2E-XC-10 | SSE refresh and route-change cleanup | All | Mock + Evidence | P1 |
| E2E-XC-11 | Keyboard, focus, labels, skip link, WCAG contrast | All | Mock | P2 |
| E2E-XC-12 | Responsive desktop/tablet/mobile, no overflow | All | Mock | P2 |
| E2E-XC-13 | Chromium, Firefox, and WebKit | All | Mock | P2 |
| E2E-XC-14 | Windows 10/11 and Linux platform matrix | All | None | P1 |
| E2E-XC-15 | Fresh, upgrade, rollback, uninstall install states | All | Evidence | P0 |
| E2E-XC-16 | Windows, WSL, and local source modes | All | Mock + Evidence | P0 |
| E2E-XC-17 | Builtin, nginx, and Apache serving modes | All | Evidence | P0 |
| E2E-XC-18 | Measured performance and no fabricated benchmark data | All | Mock + Evidence | P1 |
| E2E-XC-19 | Multi-user/SID isolation | All | None | P2 |
| E2E-XC-20 | VPN, proxy, IPv6-only, forwarding outage | All | None | P2 |
| E2E-XC-21 | Load/soak, many sites/services, log rotation | All | None | P2 |
| E2E-XC-22 | Disk-full, corrupt state, interrupted migration | All | None | P1 |
| E2E-XC-23 | Independent security review and fuzzing | All | None | P2 |
| E2E-XC-24 | Certificate expiry, renewal, and pinned-fingerprint update | All | None | P1 |
| E2E-XC-25 | Provider-switch vs activation interleaving | All | None | P2 |

**Key assertions**

- A regression in any cross-cutting rule fails more than one domain; the gate
  must be suite-wide, not per-page.
- Destructive tests run only in disposable environments and restore the baseline
  before the next test.
- No test prints or persists a password, token, private key, or secret.

---

## 5. Existing E2E assets

| Asset | Type | Covers | Gate status |
|---|---|---|---|
| `ui/tests/e2e.spec.ts` | Mock Playwright, 26 declarations | Sites, Add Site, site detail, services, settings, runtimes, doctor, terminal, vhost, responsive/a11y | Automated mock |
| `ui/tests/database-management.spec.ts` | Mock Playwright, 32 declarations | Databases, users, passwords, backups, import/export, jobs, SSE, failures, a11y | Automated mock |
| `ui/tests/live/dashboard.live.spec.ts` | Live Playwright, 1 declaration | Auth, CSRF, session expiry, core pages | Read-only live |
| `ui/tests/live/site-databases.live.spec.ts` | Live Playwright, 4 declarations | Password reveal, metadata-only, env keys, Add Site review | Read-only live |
| `test/e2e/linux.sh` | Linux shell E2E | Builtin proxy, helper, daemon, static site, nginx mode | Automated system |
| `test/e2e/database-management/linux/cycle*` | Disposable WSL shell E2E | DB lifecycle, users, grants, backups, import/export, jobs, concurrency, cleanup | Automated system; paused after cycle 8 |
| `test/windows/installer_test.ps1` | Windows installer suite | Installer checks, dry-run, sandbox, upgrade helpers | Automated system |
| `test/windows/lifecycle_test.ps1` | Windows lifecycle suite | Daemon/helper/service lifecycle | Automated system |
| `test/windows/vertical_slice.ps1` | Windows vertical slice | Real install, static/node/php sites, proxy, hosts, cleanup | Automated system |
| `test/release/*` | Release tests | Packaging, checksums, installer/release scripts | Automated system |
| `build/windows/devstack-links.test.ps1` | Link harness | Four links, README, idempotency, repair, refusal | Automated system |
| 2026-10-30 full dashboard/CLI audit | Live evidence | Every dashboard/CLI action, destructive paths, restore | Evidence only |
| 2026-10-30 Node toolchain audit | Live evidence | Node discovery, install, default, site pinning | Evidence only |
| 2026-10-29 services deep E2E | Live evidence | Service lifecycle and configuration | Evidence only |

---

## 6. Full-coverage definition of done

A domain is “complete” only when all of the following are true:

- [ ] Every **P0** capability has an automated live or system E2E test.
- [ ] Every **P1** capability is automated or has a reviewed, documented waiver.
- [ ] Every **P2** capability is in the nightly/extended matrix or explicitly
      deferred.
- [ ] The test proves the user-visible result and the persisted backend state.
- [ ] The test proves the main failure path, cleanup, and rollback.
- [ ] The test is isolated and can run repeatedly without manual repair.
- [ ] The test leaves no site, service, runtime, database, backup, link, hosts
      entry, trust record, or process behind.
- [ ] The test never prints or persists a secret.
- [ ] The suite runs on every release-blocking platform.
- [ ] The suite is part of a required gate, not an optional manual run.
- [ ] Flaky tests are fixed or quarantined with an owner and expiry date.

## 7. Suggested execution order

1. **Harness foundation:** disposable Windows/WSL and Linux homes, live
   Playwright bootstrap, fixture factory, evidence capture, cleanup, secret
   scanning, baseline restore.
2. **P0 core:** install, auth, Add Site, site lifecycle, static/Node/PHP
   serving, services core, database core, doctor, CLI/API parity.
3. **P1 destructive and recovery:** source copy, backups/restore/import/export,
   uninstall, upgrade rollback, trust repair, ports, vhost rollback, terminal,
   failure injection.
4. **P2 breadth:** cross-browser, accessibility, responsive, multi-user, network
   variants, load/soak, disk/corrupt state, security fuzzing.
5. **Permanent gate:** per-PR smoke, nightly extended, release-blocking full
   matrix.

## 8. Fixture and state matrix

| State family | Required variants |
|---|---|
| Install | clean machine, existing install, legacy `C:\devstack`, partial install, upgraded install, rolled-back install |
| Sites | none, static, Node, PHP, mixed kinds, many sites, legacy external site |
| Project source | local, Windows, WSL, copied source, symlinked alias, missing folder, escaping symlink |
| Runtimes | none, one per PHP line, multiple Node majors, stale site pin, in-use build, corrupt install |
| Services | none installed, installed inactive, active, exclusive conflict, unhealthy, data present, uninstall in flight |
| Databases | empty, seeded, large, Unicode, foreign keys, routines, corrupt dump, truncated dump, partial import |
| Users and links | none, managed, external, metadata-only, protected, shared across sites, broken link |
| Jobs | none, queued, running, failed, canceled, browser refresh mid-job, overlapping jobs |
| Auth | auth off, auth on, expired session, invalid token, invalid CSRF |
| Hosts and trust | healthy, missing, drifted, foreign entries, CA removed, helper down |
| Ports | default, custom, conflicting, reserved, fixed public Windows ports |
| Network | loopback, VPN, corporate proxy, IPv6-only, WSL forwarding outage, daemon restart |
| Disk and state | normal, low disk, full disk, corrupt `state.db`, interrupted migration |
| Concurrency | single client, two clients, overlapping mutations, reconnect storm, stale slow response |
| Viewports | desktop, tablet, mobile, 375px narrow, wide |
| Browsers | Chromium, Firefox, WebKit |

## 9. Planning inputs still needed

- Which platforms are release-blocking for the next release: Windows 11 only, or
  Windows 10 22H2 and Linux as well?
- Is a disposable Windows VM available for clean-install and reboot tests?
- Is a Linux CI runner available for systemd, nginx, Apache, and `-race`?
- Which browser matrix is required for live tests?
- How long may the nightly/extended suite run?
- Where should evidence artifacts live and how long should they be retained?
- Are destructive tests allowed in the shared real install, or only in
  disposable homes/VMs?
