# E2E Lane Matrix

> Derived from `docs/e2e-coverage.md` (268 capabilities) on 2026-11-02.
> **Primary** owns the live/system assertion. **Secondary** contributes mock, contract, or security coverage.
> Lane key: **H1-H6** heavy live WSL, **L1-L2** light live WSL, **W** serialized Windows/Linux, **BLOCKED** explicit blocker.

| ID | Capability | Priority | Primary | Secondary | Test asset | Gate |
|---|---|---|---|---|---|---|
| E2E-INST-01 | Windows installer entry points: standalone exe, release ZIP, source checkout | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-02 | Windows prerequisite checks: build, x64, virtualization, WSL 2.0+ | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-03 | Elevation/UAC and refusal when not elevated | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-04 | WSL feature enablement, reboot requirement, resume task | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-05 | Managed `devstack` WSL distro import and provisioning | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-06 | WSL Docker Engine and agent provisioning | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-07 | Helper service registration, DACL, firewall rules | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-08 | Install-time hosts block and local CA trust | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-09 | Layout, config, state database, and protected directories | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-10 | Daemon autostart and logon task registration | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-11 | `C:\devstack` links and `README.txt` | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-12 | Legacy `C:\devstack` install detection | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-13 | Linux prerequisites and port availability | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-14 | Linux install from release and source | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-15 | systemd units, helper, forwarders, autostart | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-16 | Linux `init`, CA/hosts registration, first run | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-17 | Upgrade preflight and backup | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-18 | Upgrade staging, binary replacement, agent update | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-19 | Upgrade health verification and rollback on failure | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-20 | Upgrade database backup and state migration | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-21 | Protocol/version mismatch and compatibility policy | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-22 | Reboot/resume and second-boot autostart | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-23 | `-DryRun` and `-TestSandbox` isolation | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-24 | Uninstall while preserving project data | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-25 | Uninstall with data and WSL distro purge | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-26 | Release packaging, checksums, and install from archive | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-27 | Upgrade from the previous published release | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-INST-28 | Windows 10 22H2 and a second Windows 11 build/locale | P2 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | extended |
| E2E-INST-29 | Clean Windows VM install from the release artifact | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-INST-30 | Clean Linux x86_64 systemd install from the release artifact | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-AUTH-01 | Install-token exchange from URL fragment | P0 | L1 |  | `ui/tests/live/`, `ui/tests/` | release |
| E2E-AUTH-02 | Session cookie creation and reuse | P0 | L1 |  | `ui/tests/live/`, `ui/tests/` | release |
| E2E-AUTH-03 | CSRF token required on every mutation | P0 | L1 |  | `ui/tests/live/`, `ui/tests/` | release |
| E2E-AUTH-04 | Session expiry and locked-dashboard banner | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-05 | Invalid token, missing session, invalid CSRF rejection | P0 | L1 |  | `ui/tests/live/`, `ui/tests/` | release |
| E2E-AUTH-06 | Loopback-only API, host allowlist, no CORS | P0 | L2 |  | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-AUTH-07 | Navigation across all dashboard sections | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-08 | Deep links, browser back/forward, 404 route | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-09 | Mobile drawer open/close/backdrop/Escape | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-10 | Skip link and visible keyboard focus | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-11 | Dialog Escape, focus trap, focus restoration | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-12 | System/light/dark theme and persistence | P2 | L1 |  | `ui/tests/live/`, `ui/tests/` | extended |
| E2E-AUTH-13 | Daemon, helper, and CA fingerprint status | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-14 | SSE refresh, reconnect, and expired-session behavior | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-15 | Toasts, skeletons, empty states, error banners | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-16 | Offline/daemon-unavailable and retry | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-AUTH-17 | Responsive layout at desktop/tablet/mobile widths | P2 | L1 |  | `ui/tests/live/`, `ui/tests/` | extended |
| E2E-AUTH-18 | Keyboard-only navigation, labels, contrast | P2 | L1 |  | `ui/tests/live/`, `ui/tests/` | extended |
| E2E-AUTH-19 | `devstack ui` re-authentication and fragment cleanup | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-SITE-01 | Site list and summary counts | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-SITE-02 | Search by name and domain | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-SITE-03 | Status filters and clear-filters | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-SITE-04 | Kind, status, source-mode, and health badges | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-SITE-05 | Site URL and open-in-new-tab action | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-SITE-06 | Quick start, stop, and restart | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-SITE-07 | Delete confirmation and linked-database warning | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-SITE-08 | Empty, loading, and error states | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-SITE-09 | Mobile card layout and no horizontal overflow | P2 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | extended |
| E2E-SITE-10 | SSE-driven refresh after lifecycle changes | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-SITE-11 | Resilient rendering when runtime inventory fails | P2 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | extended |
| E2E-ADD-01 | Three-step wizard, back, cancel, and edit-back navigation | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-02 | Site name validation and automatic slugification | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-03 | Site kinds: static, Node, PHP | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-04 | Source modes: local, Windows, WSL | P0 | W | H1 | `test/windows/e2e/`, `test/e2e/worker/h1/` | release |
| E2E-ADD-05 | Manual absolute project path | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-06 | Folder browser navigation and child selection | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-07 | Create new folder inside the managed root | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-08 | Reject root, outside, sibling prefix, and `..` paths | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-09 | Namespace validation: drive path, WSL UNC, local POSIX | P0 | W | H1 | `test/windows/e2e/`, `test/e2e/worker/h1/` | release |
| E2E-ADD-10 | Reject symlink/reparse escapes and root aliases | P0 | W | H1 | `test/windows/e2e/`, `test/e2e/worker/h1/` | release |
| E2E-ADD-11 | Document root validation | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-12 | SPA fallback enable and fallback file | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-13 | Node toolchain: inherit, exact version, preflight, install | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-14 | PHP build: inherit, exact build, extensions | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-15 | Front controller file and mode | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-16 | Apache `AllowOverride` | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-17 | Static build profile: enable, Node version, command, output | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-18 | Build environment variables | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-19 | Site environment variables | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-20 | HTTPS, force HTTPS, and `www` alias | P1 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | nightly |
| E2E-ADD-21 | Create a new database and user during site creation | P0 | H4 | H1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-ADD-22 | Link an existing database or user | P1 | H4 | H1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-ADD-23 | Review step and create/start behavior | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-24 | Post-create database password modal | P1 | H4 | H1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-ADD-25 | Duplicate name/path, runtime missing, and create failure | P0 | H1 | L1 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-ADD-26 | Windows static defaults to NTFS; PHP/Node default to WSL | P1 | W | H1 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-DETAIL-01 | Overview, status, URL, certificate, and source mode | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-02 | Copy URL and open site | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-03 | Start, stop, and restart | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-04 | Delete site and confirmation | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-05 | Edit document root, start command, front controller, `AllowOverride`, SPA | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-06 | Edit site environment variables | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-07 | Edit build profile and build environment | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-08 | Source-mode change with stopped-site guard | P0 | W | H2 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-DETAIL-09 | Source copy Windows to WSL and WSL to Windows | P0 | W | H2 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-DETAIL-10 | Source-copy failure and original-source preservation | P1 | W | H2 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-DETAIL-11 | Run static build and show duration/output | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-12 | Build log tail and refresh | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-13 | Failed build result and rollback | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-14 | Effective PHP configuration | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-15 | PHP version/build switch with validation and rollback | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-16 | PHP extensions and per-site `php.ini` | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-17 | PHP execution in site context | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-18 | Node version pin, inherit, and switch | P0 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-DETAIL-19 | Stale Node pin install-first and ABI rebuild warning | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-20 | Logs snapshot, live stream, pause/resume, tail | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-21 | Terminal ticket, WebSocket PTY, site environment | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-22 | Terminal resize, Ctrl+C, reconnect, idle timeout | P2 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | extended |
| E2E-DETAIL-23 | Database links, reveal/reset password, `.env` snippet | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DETAIL-24 | Vhost preview | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-DETAIL-25 | Site not found, load failure, and stale route ref | P1 | H2 | L1 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-SVC-01 | Service catalog and groups: web, MySQL family, standalone | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-02 | Install/pull/scaffold a service | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-03 | Activate a service | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-04 | Deactivate a service | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-05 | Restart a service | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-06 | Uninstall with keep-data or delete-data choice | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-07 | Exclusive web and MySQL-family conflicts | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-08 | Health, installed, and active badges | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-09 | Service container logs | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-10 | Credentials reveal, copy, and rotate | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-11 | phpMyAdmin launch only when active | P1 | H3 | H4 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-12 | Config file list, view, diff, apply, reset, and keep/use/merge conflict resolution | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SVC-13 | Performance panel: profile, preview, apply, reset, benchmark | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-14 | Deactivation impact: linked sites, backups, jobs | P1 | H3 | H4 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-15 | SSE refresh after service changes | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-16 | Loading, empty, error, and activation-in-flight states | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-17 | Docker daemon down, image pull failure, health timeout | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SVC-18 | Port conflict and stale container recovery | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-DB-01 | Service-detail tabs and deep-link query state | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-02 | List databases with inventory and stale-data states | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-03 | Create database with safe-name validation | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-04 | Rename PostgreSQL database | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-05 | Drop database with typed confirmation | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-06 | Backup-first deletion and protected names | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-07 | Linked database block until site stopped | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-08 | List users with site attribution | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-09 | Create user with grants | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-10 | Edit user, grants, and revoke | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-11 | Delete user with typed confirmation | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-12 | Set, reveal, copy, rotate, and reset passwords | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-13 | Metadata-only and external users | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-14 | Protected users cannot be edited/deleted | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-15 | Create and list backups | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-16 | Download and delete backups | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-17 | Restore backup with overwrite confirmation | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-18 | Import SQL into a database | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-19 | Export database | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-20 | Corrupt, empty, truncated, partial, binary, and non-SQL dumps | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-21 | Job progress, failure, cancellation, refresh recovery | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-22 | SSE inventory refresh and overlapping responses | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-23 | Redis password and ACL-file persistence | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-24 | Site database link, unlink, and environment resolution | P0 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | release |
| E2E-DB-25 | phpMyAdmin signed one-time signon from the dashboard as the active MySQL/MariaDB root user, with direct manual login and replay/expiry rejection | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts`, `ui/tests/live/pma-autologin.live.spec.ts` | nightly |
| E2E-DB-26 | Keyboard, focus, contrast, and responsive database screens | P2 | L1 | H4 | `ui/tests/live/`, `ui/tests/` | extended |
| E2E-DB-27 | MariaDB and MySQL-family parity | P1 | H4 | L1 | `test/e2e/worker/h4/`, `ui/tests/live/database-management.live.spec.ts` | nightly |
| E2E-DB-28 | Database E2E cycles 9-15 completion | P1 | BLOCKED |  | — | blocked |
| E2E-RT-01 | List installed runtimes and site usage | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-02 | List available catalog builds | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-03 | Install runtime with checksum/size verification | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-04 | Remove runtime | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-05 | PHP build in use; migrate sites before removal | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-06 | PHP line defaults editor | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-RT-07 | Machine-wide PHP CLI default | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-08 | Machine-wide Node CLI default | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-09 | Global CLI `php.ini` | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-RT-10 | Node release refresh, offline, stale, and cache | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-RT-11 | Node releases by major, newest patch, expand | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-RT-12 | Exact Node release preflight and install | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-13 | Composer install, update, and remove | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-RT-14 | Global `php`, `composer`, `node`, `npm`, `npx` shims | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-15 | Site shell overrides PHP/Node versions | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-RT-16 | `shell`, `exec`, `shell-init` context | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-RT-17 | Download failure, checksum mismatch, corrupt archive | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-RT-18 | Concurrent install/remove and disk-full behavior | P2 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | extended |
| E2E-DOC-01 | Run all checks and render status summary | P0 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-DOC-02 | Repair and rerun checks | P0 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-DOC-03 | Remediation text and last-run time | P1 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-DOC-04 | Helper and local CA checks | P0 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-DOC-05 | Hosts block check and repair | P0 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-DOC-06 | Managed projects-root check | P0 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-DOC-07 | Trust check, repair, and removal | P0 | W | H5 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-DOC-08 | Port ownership check | P0 | W | H5 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-DOC-09 | Runtime, tool, and disk checks | P1 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-DOC-10 | WSL, agent, Docker, forwarding, and WSL-port checks | P0 | W | H5 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-DOC-11 | Serving-mode and vhost checks | P1 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-DOC-12 | PHP build, config, port, and master checks | P0 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-DOC-13 | Listener port change and restart guidance | P1 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-DOC-14 | Firewall and helper-forwarder behavior | P1 | W | H5 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-DOC-15 | Injected hosts drift and repair | P1 | W | H5 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-DOC-16 | Injected trust, port, runtime, vhost, and PHP-config drift | P1 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-DOC-17 | Doctor JSON/API parity and stable check IDs | P1 | L2 | H5 | `test/e2e/worker/l2/`, `test/e2e/coverage/` | nightly |
| E2E-SET-01 | Machine configuration readout | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-02 | Auto-start, graceful stop, log retention, max body | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-03 | WSL distro information | P2 | W | H3 | `test/windows/e2e/`, `test/release/`, `build/windows/` | extended |
| E2E-SET-04 | Listener ports: HTTP, HTTPS, dashboard/API | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SET-05 | Invalid, out-of-range, and conflicting ports | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SET-06 | Fixed public ports on Windows | P1 | W | H3 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-SET-07 | Serving mode: builtin, nginx, Apache | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SET-08 | Serving status, sync, and preview | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-09 | nginx/Apache prerequisites and bridge include | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-10 | Vhost config validation, reload, and rollback | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SET-11 | Default-server conflict handling | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-12 | TLS custody, `.htaccess`, and custom document root | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-13 | PHP CLI default, Node CLI default, Composer | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SET-14 | Trust repair/removal | P0 | W | H5 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-SET-15 | Daemon information and uninstall guidance | P2 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | extended |
| E2E-SET-16 | Performance profile, preview, apply, reset | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-17 | Benchmark run, history, and detail | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-SET-18 | Host tuning and PHP-FPM pooling | P1 | W | H3 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-SET-19 | External nginx/Apache on Linux with PHP sites | P0 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-SET-20 | Serving-mode rollback on invalid config or reload failure | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-CLI-01 | Global flags: `--home`, `--json`, `--yes`, exit codes | P0 | L2 |  | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-CLI-02 | `init` | P0 | W | L2 | `test/windows/e2e/`, `test/e2e/linux-install/` | release |
| E2E-CLI-03 | `daemon start/run/status/restart/stop` | P0 | L2 | H6 | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-CLI-04 | Site add/list/show/start/stop/restart | P0 | H1 | L2 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-CLI-05 | Site add option matrix and validation | P0 | H1 | W | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-CLI-06 | Site source copy | P0 | W | H1 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-CLI-07 | Site node/php/ini/ext | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-CLI-08 | Site build/logs/open/remove | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-CLI-09 | Runtime list/available/install/remove/migrate | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-CLI-10 | PHP and Node defaults | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-CLI-11 | Tool list/install/update/remove | P1 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | nightly |
| E2E-CLI-12 | Shell/exec/shell-init | P1 | L2 | H2 | `test/e2e/worker/l2/`, `test/e2e/coverage/` | nightly |
| E2E-CLI-13 | Trust and vhost commands | P1 | W | L2 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-CLI-14 | Service lifecycle, logs, credentials, uninstall | P0 | H6 | H3 | `test/e2e/worker/h6/` | release |
| E2E-CLI-15 | Service config list/show/diff/apply/reset | P0 | H6 | H3 | `test/e2e/worker/h6/` | release |
| E2E-CLI-16 | DB core: list/create/rename/drop | P0 | H6 | H4 | `test/e2e/worker/h6/` | release |
| E2E-CLI-17 | DB users, grants, passwords | P0 | H6 | H4 | `test/e2e/worker/h6/` | release |
| E2E-CLI-18 | DB backups, restore, import, export | P0 | H6 | H4 | `test/e2e/worker/h6/` | release |
| E2E-CLI-19 | DB link/unlink and database environment | P0 | H6 | H4 | `test/e2e/worker/h6/` | release |
| E2E-CLI-20 | `ui`, `uninstall`, `version` | P1 | L2 | W | `test/e2e/worker/l2/`, `test/e2e/coverage/` | nightly |
| E2E-CLI-21 | API auth, session, CSRF, host guard | P0 | L1 | L2 | `ui/tests/live/`, `ui/tests/` | release |
| E2E-CLI-22 | API sites and source | P0 | H1 | L2 | `test/e2e/worker/h1/`, `ui/tests/live/{sites-list,add-site,site-detail}.live.spec.ts` | release |
| E2E-CLI-23 | API services, config, performance | P0 | H6 | H3 | `test/e2e/worker/h6/` | release |
| E2E-CLI-24 | API databases, users, backups, jobs | P0 | H6 | H4 | `test/e2e/worker/h6/` | release |
| E2E-CLI-25 | API runtimes, tools, defaults | P0 | H2 | L2 | `test/e2e/worker/h2/`, `ui/tests/live/{runtimes,site-detail}.live.spec.ts` | release |
| E2E-CLI-26 | API doctor, trust, vhosts, settings, fs, events | P0 | L2 | H5 | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-CLI-27 | API status codes, error codes, and JSON shapes | P0 | L2 |  | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-CLI-28 | SSE events and terminal WebSocket | P1 | L2 | L1 | `test/e2e/worker/l2/`, `test/e2e/coverage/` | nightly |
| E2E-CLI-29 | Daemon persistence, reconcile-once, crash recovery | P0 | H6 | L2 | `test/e2e/worker/h6/` | release |
| E2E-CLI-30 | Agent health, site ops, copy, Docker, ports, confinement | P0 | W | H6 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-CLI-31 | Protocol/version mismatch and reconnect | P1 | W | H6 | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-XC-01 | Managed-root confinement and symlink/reparse escapes | P0 | H5 | W | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-XC-02 | CSRF, host guard, loopback-only, no CORS | P0 | L2 | L1 | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-XC-03 | Secrets never in HTML, logs, traces, screenshots, API lists | P0 | L2 |  | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-XC-04 | Password masking, reveal audit, rotation, typed confirms | P0 | L2 | H4 | `test/e2e/worker/l2/`, `test/e2e/coverage/` | release |
| E2E-XC-05 | Persistence across restart, upgrade, and rollback | P0 | H6 | W | `test/e2e/worker/h6/` | release |
| E2E-XC-06 | Atomic writes and idempotent operations | P0 | H5 | L2 | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | release |
| E2E-XC-07 | Daemon/helper/Docker/WSL/network failure and retry | P1 | H5 | H6 | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-XC-08 | Port, runtime, permission, and disk conflict handling | P1 | H5 |  | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-XC-09 | Concurrent actions, stale responses, cancellation | P1 | H6 | L2 | `test/e2e/worker/h6/` | nightly |
| E2E-XC-10 | SSE refresh and route-change cleanup | P1 | L1 |  | `ui/tests/live/`, `ui/tests/` | nightly |
| E2E-XC-11 | Keyboard, focus, labels, skip link, WCAG contrast | P2 | L1 |  | `ui/tests/live/`, `ui/tests/` | extended |
| E2E-XC-12 | Responsive desktop/tablet/mobile, no overflow | P2 | L1 |  | `ui/tests/live/`, `ui/tests/` | extended |
| E2E-XC-13 | Chromium, Firefox, and WebKit | P2 | L1 |  | `ui/tests/live/`, `ui/tests/` | extended |
| E2E-XC-14 | Windows 10/11 and Linux platform matrix | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-XC-15 | Fresh, upgrade, rollback, uninstall install states | P0 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-XC-16 | Windows, WSL, and local source modes | P0 | W | H1 | `test/windows/e2e/`, `test/release/`, `build/windows/` | release |
| E2E-XC-17 | Builtin, nginx, and Apache serving modes | P0 | H3 | W | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | release |
| E2E-XC-18 | Measured performance and no fabricated benchmark data | P1 | H3 | L1 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | nightly |
| E2E-XC-19 | Multi-user/SID isolation | P2 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | extended |
| E2E-XC-20 | VPN, proxy, IPv6-only, forwarding outage | P2 | L2 |  | `test/e2e/worker/l2/`, `test/e2e/coverage/` | extended |
| E2E-XC-21 | Load/soak, many sites/services, log rotation | P2 | H6 |  | `test/e2e/worker/h6/` | extended |
| E2E-XC-22 | Disk-full, corrupt state, interrupted migration | P1 | H5 |  | `test/e2e/worker/h5/`, `ui/tests/live/doctor.live.spec.ts` | nightly |
| E2E-XC-23 | Independent security review and fuzzing | P2 | L2 |  | `test/e2e/worker/l2/`, `test/e2e/coverage/` | extended |
| E2E-XC-24 | Certificate expiry, renewal, and pinned-fingerprint update | P1 | W |  | `test/windows/e2e/`, `test/release/`, `build/windows/` | nightly |
| E2E-XC-25 | Provider-switch vs activation interleaving | P2 | H3 | H6 | `test/e2e/worker/h3/`, `ui/tests/live/{services,settings}.live.spec.ts` | extended |

## Summary

| Lane | Capabilities |
|---|---:|
| H1 | 33 |
| H2 | 45 |
| H3 | 37 |
| H4 | 30 |
| H5 | 16 |
| H6 | 12 |
| L1 | 24 |
| L2 | 14 |
| W | 56 |
| BLOCKED | 1 |

**Total: 268.** P0 = release gate, P1 = nightly, P2 = extended. DB-28 is blocked by the paused database E2E cycles 9-15 until explicit user approval.

