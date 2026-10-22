# dev-web-stack

A local web development stack for Linux: static and Node sites served at
trusted `https://<name>.test` URLs, managed by a Go CLI and a local daemon.

> **Status:** early scaffold. Only `devstack version` works today; the daemon,
> proxy, helper, and dashboard arrive in later tasks of Plan 1.

## Requirements

- Go 1.26 or newer
- Linux x86_64 with systemd (the helper and forwarders target this platform)
- `golangci-lint` for `make lint`
- Node.js and npm once the dashboard lands (`ui/`)

## Build and test

```sh
make build   # builds dist/devstack and dist/devstack-helper with version metadata
make test    # go test ./...
make lint    # golangci-lint run
make ui      # builds the dashboard when ui/package.json exists
```

Version metadata is injected with `-ldflags`, so binaries report the git
describe version and short commit:

```sh
$ ./dist/devstack version
v0.1.0-3-gabc1234 (abc1234)
```

Running `go run ./cmd/devstack version` prints the default `dev`.

## Layout

| Path | Purpose |
|---|---|
| `cmd/devstack` | CLI and (later) daemon entrypoint |
| `cmd/devstack-helper` | privileged helper service |
| `internal/` | implementation packages |
| `ui/` | Svelte dashboard, embedded into the daemon (later) |

Design and implementation plans live in `docs/superpowers/`.
