# dev-web-stack

A local web development stack for Linux and Windows: static, Node, and PHP sites served at
trusted `https://<name>.test` URLs, managed by a Go CLI and a local daemon.

> **Status:** The cross-platform Linux and Windows/WSL2 release is published as `v0.2.0`.
>
> - **Windows 11 / 10 22H2 (Build 19045+) + WSL2:** Fully implemented and supported. Features a standalone binary installer (`devstack-installer.exe`), native Windows helper service (`devstack-helper.exe`), user daemon (`devstack.exe`), DPAPI token management, host-to-WSL proxying, and a dedicated managed Debian 12 WSL2 distribution with rootless Docker and Linux runtimes.
> - **Linux (x86_64 systemd):** Foundation, PHP runtimes, managed services, and release packaging pass acceptance.
> - **PHP 8.2–8.5:** Installable from the shipped catalog with side-by-side execution and Composer integration.
> - **Service platform:** Managed Docker nginx/Apache, MySQL/MariaDB/PostgreSQL/Redis, phpMyAdmin, credentials rotation, and performance benchmarking.
>
> See the [Documentation Index](docs/README.md), [Linux Installation Guide](docs/install.md), and [Windows Installation Guide](docs/install-windows.md).

## Quick start

### Windows Installation

**Prerequisites:** Windows 10 (Build 19045+) or Windows 11 x64 with WSL 2.0.0+ (`wsl --version`) and hardware virtualization enabled in BIOS.

#### Option 1: Standalone Installer Executable (Recommended)

1. Download **[`devstack-installer.exe`](https://github.com/AramZahedi/dev-web-stack/releases/download/v0.2.0/devstack-installer.exe)** (or `devstack-setup.exe`) from the [v0.2.0 Release](https://github.com/AramZahedi/dev-web-stack/releases/tag/v0.2.0).
2. **Double-click** `devstack-installer.exe` (or run `./devstack-installer.exe` from PowerShell / Command Prompt).
   - Prompts for Administrator elevation via standard Windows UAC.
   - Extracts all embedded components, configures `C:\devstack`, installs the `DevstackHelper` Windows service, imports the Debian 12 WSL2 distribution, and provisions Docker and runtimes.
3. Open a new PowerShell terminal and verify:
   ```powershell
   devstack version
   devstack doctor
   ```
4. Start the daemon and access the dashboard:
   ```powershell
   devstack daemon start
   ```
   Open <https://devstack.test> in your browser.

#### Option 2: Release Zip Archive
Download `devstack_0.2.0_windows_amd64.zip` and `checksums.txt`, extract, and run `.\install.ps1` from an elevated PowerShell terminal. See [Windows Installation Guide](docs/install-windows.md) for details.

---

### Linux Installation

**Requirements:** Linux x86_64 with systemd as PID 1. Ports 80 and 443 must be free.

#### Option 1: Install Published Release
```sh
./install.sh --release 0.2.0
devstack init
```

#### Option 2: Build from Source
Requirements: Go 1.26+, Node.js 22/npm 10.
```sh
make build
./install.sh
devstack init
```

`install.sh` runs as your normal user and uses `sudo` for the privileged helper service and forwarders. `devstack init` registers the local CA in the OS and browser trust stores, writes the managed `.test` hosts block, and starts the daemon. Then open <https://devstack.test>.

Full Linux instructions: [docs/install.md](docs/install.md). Upgrade guide: [docs/upgrade.md](docs/upgrade.md).

> **Docker support is being replaced.** The standalone devstack-in-Docker image
> was retired on 2026-10-25. Docker remains supported for the planned optional
> service containers (nginx/Apache, databases, Redis, phpMyAdmin); the devstack
> application itself is native.

## Serving modes

Serving is `builtin` by default: the helper owns ports 80/443 and the Go proxy
publishes every site. PHP sites use FastCGI to a per-site `php-fpm` master on a
loopback port in 43000-43999. If nginx or Apache is already installed, you can
switch to native vhosts:

```sh
devstack vhost status
devstack vhost use nginx --yes     # or apache
devstack vhost use builtin --yes
```

devstack does not install or enable servers or modules. External mode requires
`ui_port: 7070`. Apache needs `ssl`, `proxy`, `proxy_http`, `proxy_wstunnel`,
`headers`, and `rewrite`; PHP vhosts additionally need `proxy_fcgi`. Preview and
sync are available through `devstack vhost preview <site>`, `devstack vhost
sync`, and Settings → Serving in the dashboard. See
[docs/vhosts.md](docs/vhosts.md) for layout, rollback, and troubleshooting.

## Build and test

```sh
make build   # builds the dashboard, then dist/devstack and dist/devstack-helper
make test    # go test ./...
make lint    # golangci-lint run
make ui      # builds the dashboard only (npm ci && npm run build)
make e2e     # builds, then runs the Linux end-to-end script
make acceptance # full unprivileged Linux M1 acceptance (PASS/FAIL/SKIP/BLOCKED)
make release VERSION=0.2.0 # local unsigned archive + checksums.txt
make release-test # release archive contract test
make php-test  # catalog/smoke tests plus shell syntax and shellcheck
make php-build # builds one PHP artifact (needs build/php/README.md deps)
make php-smoke # verifies the built artifact, including FPM over FastCGI
make php-catalog PHP_BASE_URL='https://release-host.invalid/php'  # emits one entry
```

The `php-catalog` target requires an explicit `PHP_BASE_URL` for the real HTTPS
release asset host; there is no default URL. The CI catalog job likewise runs
only when `download_base_url` (dispatch) or `PHP_DOWNLOAD_BASE_URL` (repository
variable) is supplied.

The PHP build is a real source build and runs in the dedicated
[PHP workflow](.github/workflows/php.yml) on `ubuntu-22.04`; the local
development environment does not have the PHP build dependencies. The production
catalog ships with PHP 8.2–8.5 builds; newly published versions follow the gated
merge procedure described in [docs/php.md](docs/php.md).

`go test -race ./...` is the race-enabled test command used by CI. `make e2e`
runs `test/e2e/linux.sh`, which uses temporary HOME/state/hosts/trust paths and
high ports. It never binds 80/443, edits `/etc/hosts`, or modifies real trust
stores, so it does not require root.

`make acceptance` runs `test/acceptance/m1.sh`: vet, unit tests, race tests,
build, UI type/unit/build/Playwright checks, the release/installer/workflow
tests, `install.sh --dry-run`, the Linux E2E flow, the real system-PHP
acceptance when `/usr/local/php` exists, and a final check for listeners on
43000-43999. It refuses to run as root, installs no packages, and prints a
PASS/FAIL/SKIP/BLOCKED table. `--quick` skips the race detector and
`--skip-playwright` skips Playwright; neither turns a skipped gate into a pass.

The dashboard build must run before Go builds that embed it; `make build` and CI
handle that ordering automatically. To run the compiler and race tests without
rebuilding the UI after a UI build, use `go test -race ./...` directly.

Version metadata is injected with `-ldflags`, so binaries report the git
describe version and short commit:

```sh
$ ./dist/devstack version
v0.1.0-3-gabc1234 (abc1234)
```

Running `go run ./cmd/devstack version` prints the default `dev`.

## Status and limitations

- M1's Linux scope is implemented: Plan 1 serves static and Node sites, Plan 2
  adds PHP sites, per-site FPM masters, php.ini/extension controls, and PHP
  vhost previews, and Plan 4 adds the unsigned release/install/upgrade path. The
  automated Linux acceptance passed for those implemented paths on Fedora 44
  with system PHP 8.4.4; the blocked and manual gates are listed below and in the
  [Plan 4 acceptance report](docs/superpowers/plans/2026-10-24-dev-web-stack-plan-4-acceptance.md).
- **Release archives are unsigned and Linux-only.** The archive contains
  `devstack`, `devstack-helper`, `install.sh`, `README.md`, and `LICENSE` when
  present, plus a SHA-256 `checksums.txt`. There is no Windows, WSL, macOS,
  arm64, or `devstack-agent` artifact. Signed packages/installers are M4.
- **PHP installation is published.** The `v0.1.0` catalog embeds PHP 8.2.35,
  8.3.36, 8.4.27, and 8.5.12 with checksummed archives in
  `php-runtimes-2026-10-25`. `devstack runtime list` shows those builds, and the
  final acceptance downloaded 8.2.35/8.5.12 and served both. A newer version
  needs a newer app release that embeds its catalog entry.
- **The real multi-version PHP gate passes.** Published 8.2.35 and 8.5.12
  artifacts were served side by side; the final public-catalog acceptance
  downloaded both and verified their checksums. See [PHP](docs/php.md).
- `imap` is omitted from the current PHP build pipeline because the verified
  upstream release does not cover the manifest's PHP 8.2/8.3 pairing; see
  [build/php/README.md](build/php/README.md).
- Automated external serving is verified with real nginx on high ports. The Plan
  2 nginx PHP vhost passes `nginx -t`; the Apache HTTP and HTTPS PHP vhosts pass
  `httpd -t`. With `mod_ssl` installed, a live Apache TLS listener served a real
  request through PHP-FPM. Real Apache mode switching and browser-trust checks
  remain manual acceptance items; see the
  [Plan 1 acceptance report](docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md)
  and the
  [Plan 2 acceptance report](docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-acceptance.md).
- **Browser trust, root install/uninstall on 80/443, live UI flows, and real
  Apache serving are MANUAL and were not run** in this unprivileged acceptance.
  The acceptance runner never edits `/etc/hosts`, host trust stores, or system
  services.
- **In-flight request continuity across a successful PHP swap passes.** The
  load test completed 89/89 requests with 0 failures while a new generation
  replaced the old one; a 2-second drain grace protects queued connections.
- Node HMR WebSockets are covered by automated proxy tests, but no browser HMR
  session was part of the acceptance pass.
- The PHP full source build and FPM smoke test are verified by CI on
  `ubuntu-22.04`; the published artifacts were downloaded and exercised in the
  final acceptance run.
- Historical app CI, release workflow, and master CI runs were green. Automatic
  GitHub Actions are now disabled and workflows are manual-dispatch only; use the
  local gate. The manual platform matrix (Ubuntu 22.04/24.04, Debian 12, Fedora 44,
  Windows/WSL) was not run as a full matrix; local acceptance used Fedora 44.

## Documentation

Start with the [documentation index](docs/README.md), the [handoff](docs/handoff.md),
the [architecture](docs/architecture.md), and the
[requirements/decision record](docs/requirements.md).

| Guide | Contents |
|---|---|
| [Documentation index](docs/README.md) | map of all current, planned, and historical docs |
| [Handoff](docs/handoff.md) | repo/release state, current machine, known issues, resume steps |
| [Architecture](docs/architecture.md) | implemented process model, package map, state, API, limitations |
| [Requirements](docs/requirements.md) | user requirements/concerns and traceability |
| [Decisions](docs/decisions.md) | implemented and planned ADRs with rejected alternatives |
| [Planned services](docs/planned-services.md) | overview of the unimplemented service platform |
| [Known issues](docs/known-issues.md) | current bugs, parked work, and limitations |
| [Glossary](docs/glossary.md) | project terminology and concepts |
| [Developer guide](docs/development.md) | build/test/debug, package boundaries, migrations, release |
| [Service-platform design](docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md) | detailed next-milestone spec (approved for implementation) |
| [Parallel implementation program](docs/superpowers/plans/2026-10-25-service-platform-program.md) | worktree/merge mechanism, waves, dependency graph |
| [Foundation plan](docs/superpowers/plans/2026-10-25-service-platform-foundation.md) | detailed Wave 0 tasks |
| [Wave 0 kickoff](docs/superpowers/plans/2026-10-25-service-platform-wave0-kickoff.md) | starting instructions for the external implementation agent |
| [Install](docs/install.md) | prerequisites, installer, release archives, systemd, config, logs, uninstall |
| [Upgrade](docs/upgrade.md) | preflight, stop/install/start, database backup, protocol mismatch, verification, rollback limits |
| [Release](docs/release.md) | tag/version, archive/checksums, GoReleaser workflow, release checklist, PHP catalog gate |
| [Sites](docs/sites.md) | first static site, Node sites, lifecycle commands, limitations |
| [PHP](docs/php.md) | PHP builds, sites, ini and extensions, migration, FPM ports, troubleshooting, artifact gate |
| [Vhosts](docs/vhosts.md) | external nginx/Apache serving, PHP FastCGI prerequisites, mode switching, preview, TLS custody, troubleshooting |
| [TLS and trust](docs/tls.md) | local CA, browser trust, repair, curl/Node usage |

## Dashboard

`make build` embeds the generated `ui/dist` directory into the daemon binary. For
dashboard development, run the Vite server directly:

```sh
cd ui
npm ci
npm run dev          # http://127.0.0.1:5173 with the in-memory mock API
```

Dev mode uses the mock API unless `VITE_API_MODE` is set; use
`VITE_API_MODE=api` to proxy to a running daemon (`DEVSTACK_API_ORIGIN`, default
`http://127.0.0.1:7070`). Production builds talk to the real loopback API.

## Layout

| Path | Purpose |
|---|---|
| `cmd/devstack` | CLI and daemon entrypoint |
| `cmd/devstack-helper` | privileged helper service |
| `cmd/phpcatalog` | emits one PHP catalog entry from a verified artifact |
| `cmd/php-smoke` | starts FPM and asserts a FastCGI health request |
| `cmd/catalogmerge` | gated merge of verified PHP catalog entries |
| `internal/` | implementation packages |
| `ui/` | Svelte 5 dashboard (mock-mode development, embedded in the daemon) |
| `build/systemd/` | helper and user daemon units |
| `build/php/` | PHP source/PECL pin lock, build, smoke, and lock scripts |
| `build/helper.yaml.example` | helper configuration reference |
| `.github/workflows/php.yml` | PHP build matrix, smoke, SBOM, and catalog artifact |
| `.github/workflows/release.yml` | manual-dispatch Linux GoReleaser release |
| `install.sh` | source, verified release archive, and upgrade installer |
| `test/acceptance/m1.sh` | unprivileged Linux M1 acceptance runner |
| `test/e2e/linux.sh` | unprivileged Linux end-to-end test |
| `test/release/` | archive, installer, and release-workflow tests |
| `docs/` | install, upgrade, release, sites, PHP, vhosts, and TLS guides |

Design and implementation plans live in `docs/superpowers/`.
