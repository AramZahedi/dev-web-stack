# dev-web-stack

A local web development stack for Linux: static, Node, and PHP sites served at
trusted `https://<name>.test` URLs, managed by a Go CLI and a local daemon.

> **Status:** M1's Linux scope is implemented on branch `plan-4-release`. Plan 1
> (foundation) and Plan 2 (PHP) automated acceptance pass. Plan 4 adds unsigned,
> checksummed Linux x86_64 release archives; verified source, archive, and
> HTTPS release installs; forward upgrades with an automatic pre-migration
> SQLite backup; a gated PHP catalog merge; and the full Linux M1 acceptance
> runner. See the
> [Plan 4 acceptance report](docs/superpowers/plans/2026-10-24-dev-web-stack-plan-4-acceptance.md)
> and the [M1 handoff](docs/superpowers/status/2026-10-24-m1-handoff.md). This
> branch has not been merged and no release tag or GitHub release has been
> published from it.
>
> **M1 is Linux-only and unsigned.** Windows/WSL (Plan 3) is deferred by user
> request: there is no `devstack-agent`, named-pipe helper, `install.ps1`, or
> Windows artifact. Signed packages/installers, diagnostics bundles,
> auto-update, remote catalogs, arm64, and log aggregation are M4 work.
>
> **PHP is not installable from the shipped catalog yet.** The production PHP
> catalog stays empty until the release pipeline publishes checksummed
> `php-<version>-r<revision>-linux-x64.tar.gz` archives and their entries are
> merged. The real PHP 8.2/8.5 side-by-side gate is **BLOCKED** on those
> artifacts. See [PHP](docs/php.md).
>
> **Support surface:** `devstack doctor --json` is the diagnostic snapshot to
> attach to a bug report. M1 does not create a diagnostics zip/bundle, and it
> never checks for or installs updates automatically.

## Quick start

Requirements: Linux x86_64 with systemd, Go 1.26+, and Node.js 22/npm 10 for the
dashboard build. The helper's privileged forwarders need ports 80 and 443.

```sh
make build
./install.sh
devstack init
```

To install a published release instead of building, pass `--release`; the
installer downloads a SHA-256-verified archive over HTTPS:

```sh
./install.sh --release 0.1.0
devstack init
```

A downloaded archive can be installed with
`--archive devstack_0.1.0_linux_amd64.tar.gz --checksums checksums.txt`.

No release tag has been published from this branch yet. Until one is, use the
source install above or a local archive produced with
`make release VERSION=<semver>`. The release path is documented in
[Release](docs/release.md).

`install.sh` runs as your normal user and uses `sudo` for the helper binary,
configuration, and system unit. It does not install packages. `devstack init`
installs the local CA in the OS/browser trust stores, writes the hosts block,
starts the helper forwarders and daemon, and runs diagnostics. The helper's
`auto_forward` setting restores ports 80/443 whenever the service restarts.
Then open <https://devstack.test>.

Full instructions: [docs/install.md](docs/install.md). To upgrade an existing
install, keep the helper config and run `./install.sh --upgrade`, then
`devstack init`; see the [upgrade guide](docs/upgrade.md).

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
development environment does not have the PHP build dependencies. The PHP
catalog remains gated as described in [docs/php.md](docs/php.md).

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
- **PHP installation is gated.** The production catalog has no PHP entries until
  the release pipeline publishes checksummed archives and merges their entries.
  `devstack runtime list` shows only Node builds in shipped releases. The build,
  smoke, SBOM, and catalog-generation steps run in CI; this repository does not
  publish artifacts or commit catalog changes.
- **The real multi-version PHP gate is blocked.** PHP 8.2 and 8.5 side-by-side
  acceptance requires the checksummed artifacts produced by the Task 11 CI
  pipeline. Until those are published, tests use fixture catalog rows plus the
  system PHP 8.4 build; this does not prove production PHP installation works.
  See the
  [Plan 2 acceptance report](docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-acceptance.md).
- `imap` is omitted from the current PHP build pipeline because the verified
  upstream release does not cover the manifest's PHP 8.2/8.3 pairing; see
  [build/php/README.md](build/php/README.md).
- Automated external serving is verified with real nginx on high ports. The Plan
  2 nginx PHP vhost passes `nginx -t`; the Apache HTTP PHP vhost passes
  `httpd -t`. Only the Apache HTTPS vhost is skipped on this workstation because
  `mod_ssl` is absent. Real Apache serving and browser-trust checks remain manual
  acceptance items; see the
  [Plan 1 acceptance report](docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md)
  and the
  [Plan 2 acceptance report](docs/superpowers/plans/2026-10-23-dev-web-stack-plan-2-acceptance.md).
- **Browser trust, root install/uninstall on 80/443, live UI flows, and real
  Apache serving are MANUAL and were not run** in this unprivileged acceptance.
  The acceptance runner never edits `/etc/hosts`, host trust stores, or system
  services.
- **In-flight request continuity across a successful PHP swap is NOT
  MEASURED.** The tests prove validate-then-swap and rollback, not concurrent
  request survival.
- Node HMR WebSockets are covered by automated proxy tests, but no browser HMR
  session was part of the acceptance pass.
- The PHP full source build and FPM smoke test are verified by CI on
  `ubuntu-22.04`, not by the local development environment.
- The release/installer/workflow tests validate the checked-in configuration,
  but this local acceptance did not observe a CI run. CI is not claimed green
  here. The manual platform matrix (Ubuntu 22.04/24.04, Debian 12, Fedora 44,
  Windows/WSL) was not run; local acceptance used Fedora 44.

## Documentation

| Guide | Contents |
|---|---|
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
| `.github/workflows/release.yml` | tag-driven Linux GoReleaser release |
| `install.sh` | source, verified release archive, and upgrade installer |
| `test/acceptance/m1.sh` | unprivileged Linux M1 acceptance runner |
| `test/e2e/linux.sh` | unprivileged Linux end-to-end test |
| `test/release/` | archive, installer, and release-workflow tests |
| `docs/` | install, upgrade, release, sites, PHP, vhosts, and TLS guides |

Design and implementation plans live in `docs/superpowers/`.
