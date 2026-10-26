# Windows development

This repository keeps Linux as the release platform. Windows 11 + WSL2 support
is being implemented on the `feature/windows-wsl` branch. Windows development
uses the same source tree, tests, and CI contracts as Linux; only the process,
transport, certificate, and service integration layers are platform-specific.

## Checkout hygiene

The repository stores every script, Go file, SQL migration, YAML file, and
Linux configuration fragment with LF line endings. A checkout made with
`core.autocrlf=true` converts shell scripts to CRLF and breaks `bash -n`,
`install.sh`, and the WSL scripts.

```powershell
git config --global core.autocrlf false
git config --local core.autocrlf false
git checkout -- .
git status --short
```

If a working tree already contains CRLF files, renormalize it once:

```powershell
git add --renormalize .
git checkout -- .
```

`.gitattributes` is authoritative. Do not add a second `.gitattributes` entry
that converts `.go`, `.sh`, `.sql`, `Makefile`, or `*.service` to CRLF.

## Toolchain baseline

| Tool | Baseline | Notes |
| --- | --- | --- |
| Go | 1.26+ | `go.mod` is authoritative. |
| Node | 22 LTS | Node 24 may work but is not the documented UI/CI baseline. |
| bash | Git Bash or WSL | Needed for `make`, release tests, and scripts. |
| make | GNU make in Git Bash/WSL | PowerShell fallback commands are below. |
| WSL | 2.0.0+ with systemd | Use a development distro; do not reuse `devstack`. |
| Docker | Docker Engine inside WSL | Docker Desktop is not the supported substrate. |
| Administrator | for service/ACL/80/443 tests | Unit tests do not need elevation. |

The managed `devstack` distro is created by the product installer. A
developer should use a separate distro, for example `Ubuntu`, and must never
adopt or replace an unmarked distro.

## Build and test

From Git Bash or WSL at the repository root:

```bash
make test                 # native Linux tests
make test-windows-cross   # compile every Windows package and test binary
make build-windows        # dist/devstack.exe and dist/devstack-helper.exe
make build-agent          # dist/devstack-agent (after Lane B lands cmd/devstack-agent)
```

`make test-windows-cross` runs `go build`, `go vet`, and `go test` with
`-exec true`, so Windows test binaries are compiled but not executed on Linux.
Run the Windows host suite natively when a Windows runner is available:

```powershell
go test ./...
go vet ./...
```

PowerShell fallback when `make` is unavailable:

```powershell
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
go build ./...
go vet ./...
go test -run '^$' ./...
go build -trimpath -o dist/devstack.exe ./cmd/devstack
go build -trimpath -o dist/devstack-helper.exe ./cmd/devstack-helper
Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
```

## CI

`.github/workflows/ci.yml` stays manual-only. Set the `windows` dispatch input
to `true` to run the `Windows cross-build` job. The job cross-compiles both
binaries, compiles the Windows test matrix, and uploads the artifacts. It does
not run Windows services, named pipes, TLS, or WSL acceptance; those are
controller-serialized on a Windows host and recorded in the baseline and
acceptance evidence.

## Linux regression

Run Linux tests in WSL ext4 or a Linux checkout of the same commit:

```bash
git worktree add ../devstack-linux-regression <sha>
cd ../devstack-linux-regression
go test ./...
bash -n install.sh test/acceptance/m1.sh test/release/*.sh
```

Do not run the Linux regression suite from `/mnt/c`; file mode, symlink, and
case-sensitivity behavior differ from ext4 and will produce false results.

## Platform ownership

- The Windows daemon owns SQLite, routes, UI/API, public TLS, and orchestration.
- The Windows helper owns the CA, trust, hosts, firewall, forwarders, and
  80/443.
- The managed WSL agent owns PHP-FPM, Node, static serving, ports, logs, WSL
  ingress, and Docker services.
- All Windows site backends run in WSL. The daemon never runs a local site
  supervisor or a Docker PHP container.

See `docs/superpowers/specs/2026-10-26-windows-wsl-design.md` and
`docs/superpowers/plans/2026-10-26-windows-wsl.md` for the binding contracts.
