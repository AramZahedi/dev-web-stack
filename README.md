# dev-web-stack

A local web development stack for Linux: static, Node, and PHP sites served at
trusted `https://<name>.test` URLs, managed by a Go CLI and a local daemon.

> **Status:** Plan 1 implementation and its automated acceptance suite pass
> (unit, race, vet, build, Linux E2E, UI unit/type/build/Playwright). Plan 2's
> PHP runtime, per-site FPM, php.ini and extension controls, dashboard, and
> external vhost rendering are implemented and covered by automated tests; the
> final Plan 2 acceptance run is tracked separately.
>
> **PHP is not installable from the shipped catalog yet.** The production PHP
> catalog stays empty until the CI/release pipeline publishes checksummed
> `php-<version>-r<revision>-linux-x64.tar.gz` archives and their entries are
> merged. See [PHP](docs/php.md). The manual Fedora 44 checks for browser trust,
> privileged install/uninstall on 80/443, live UI flows, and real Apache serving
> were not run in the Plan 1 acceptance environment; see the
> [Plan 1 acceptance report](docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md).
> Windows/WSL support remains future work.

## Quick start

Requirements: Linux x86_64 with systemd, Go 1.26+, and Node.js 22/npm 10 for the
dashboard build. The helper's privileged forwarders need ports 80 and 443.

```sh
make build
./install.sh
devstack init
```

`install.sh` runs as your normal user and uses `sudo` for the helper binary,
configuration, and system unit. It does not install packages. `devstack init`
installs the local CA in the OS/browser trust stores, writes the hosts block,
starts the helper forwarders and daemon, and runs diagnostics. The helper's
`auto_forward` setting restores ports 80/443 whenever the service restarts.
Then open <https://devstack.test>.

Full instructions: [docs/install.md](docs/install.md).

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

- Plan 1 serves static and Node sites on Linux. Plan 2 adds PHP sites, per-site
  FPM masters, php.ini/extension controls, and PHP vhost previews. Windows/WSL
  (Plan 3) and packaging/release work (Plan 4) are not included.
- **PHP installation is gated.** The production catalog has no PHP entries until
  the release pipeline publishes checksummed archives and merges their entries.
  `devstack runtime list` shows only Node builds in shipped releases. The build,
  smoke, SBOM, and catalog-generation steps run in CI; this repository does not
  publish artifacts or commit catalog changes.
- `imap` is omitted from the current PHP build pipeline because the verified
  upstream release does not cover the manifest's PHP 8.2/8.3 pairing; see
  [build/php/README.md](build/php/README.md).
- Automated external serving is verified with real nginx on high ports. Real
  Apache serving and browser-trust checks remain manual acceptance items; see
  the [Plan 1 acceptance report](docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md).
- Node HMR WebSockets are covered by automated proxy tests, but no browser HMR
  session was part of the acceptance pass.
- The PHP full source build and FPM smoke test are verified by CI on
  `ubuntu-22.04`, not by the local development environment.

## Documentation

| Guide | Contents |
|---|---|
| [Install](docs/install.md) | prerequisites, installer, systemd, config, logs, upgrade, uninstall |
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
| `internal/` | implementation packages |
| `ui/` | Svelte 5 dashboard (mock-mode development, embedded in the daemon) |
| `build/systemd/` | helper and user daemon units |
| `build/php/` | PHP source/PECL pin lock, build, smoke, and lock scripts |
| `build/helper.yaml.example` | helper configuration reference |
| `.github/workflows/php.yml` | PHP build matrix, smoke, SBOM, and catalog artifact |
| `install.sh` | Linux installer |
| `test/e2e/linux.sh` | unprivileged Linux end-to-end test |
| `docs/` | install, sites, PHP, vhosts, and TLS guides |

Design and implementation plans live in `docs/superpowers/`.
