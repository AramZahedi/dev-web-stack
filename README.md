# dev-web-stack

A local web development stack for Linux: static and Node sites served at
trusted `https://<name>.test` URLs, managed by a Go CLI and a local daemon.

> **Status:** Plan 1 foundation in progress. The CLI, daemon, loopback REST API,
> static and Node site lifecycle, and embedded Svelte dashboard are implemented.
> External nginx/Apache vhosts, packaging, and later milestones are still pending.

## Requirements

- Go 1.26 or newer
- Linux x86_64 with systemd (the helper and forwarders target this platform)
- Node.js 22 and npm 10 for the dashboard build
- `golangci-lint` for `make lint`

## Build and test

```sh
make build   # builds the dashboard, then dist/devstack and dist/devstack-helper
make test    # go test ./...
make lint    # golangci-lint run
make ui      # builds the dashboard only (npm ci && npm run build)
```

The dashboard build must run before Go builds that embed it; `make build` and CI
handle that ordering automatically.

Version metadata is injected with `-ldflags`, so binaries report the git
describe version and short commit:

```sh
$ ./dist/devstack version
v0.1.0-3-gabc1234 (abc1234)
```

Running `go run ./cmd/devstack version` prints the default `dev`.

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

Design and implementation plans live in `docs/superpowers/`.
