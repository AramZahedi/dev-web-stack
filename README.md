# dev-web-stack

A local web development stack for Linux: static and Node sites served at
trusted `https://<name>.test` URLs, managed by a Go CLI and a local daemon.

> **Status:** Plan 1 foundation implemented. The CLI, daemon, loopback REST API,
> static and Node site lifecycle, embedded Svelte dashboard, privileged helper,
> Linux installer, systemd units, external nginx/Apache serving, and end-to-end
> tests are in place. PHP vhosts and Windows/WSL support arrive in later plans.

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
publishes every site. If nginx or Apache is already installed, you can switch to
native vhosts:

```sh
devstack vhost status
devstack vhost use nginx --yes     # or apache
devstack vhost use builtin --yes
```

devstack does not install or enable servers or modules. External mode requires
`ui_port: 7070`, and Apache needs `ssl`, `proxy`, `proxy_http`, `proxy_wstunnel`,
`headers`, and `rewrite`. Preview and sync are available through `devstack vhost
preview <site>`, `devstack vhost sync`, and Settings → Serving in the dashboard.
See [docs/vhosts.md](docs/vhosts.md) for layout, rollback, and troubleshooting.

## Build and test

```sh
make build   # builds the dashboard, then dist/devstack and dist/devstack-helper
make test    # go test ./...
make lint    # golangci-lint run
make ui      # builds the dashboard only (npm ci && npm run build)
make e2e     # builds, then runs the Linux end-to-end script
```

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

## Documentation

| Guide | Contents |
|---|---|
| [Install](docs/install.md) | prerequisites, installer, systemd, config, logs, upgrade, uninstall |
| [Sites](docs/sites.md) | first static site, Node sites, lifecycle commands, limitations |
| [Vhosts](docs/vhosts.md) | external nginx/Apache serving, mode switching, preview, TLS custody, troubleshooting |
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
| `internal/` | implementation packages |
| `ui/` | Svelte 5 dashboard (mock-mode development, embedded in the daemon) |
| `build/systemd/` | helper and user daemon units |
| `build/helper.yaml.example` | helper configuration reference |
| `install.sh` | Linux installer |
| `test/e2e/linux.sh` | unprivileged Linux end-to-end test |
| `docs/` | install, sites, vhosts, and TLS guides |

Design and implementation plans live in `docs/superpowers/`.
