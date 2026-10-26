# Windows/WSL foundation baseline

**Date:** 2026-10-27

**Status:** Wave 0 foundation complete. Windows support is **not** claimed yet.
Wave 1 lanes may start from this commit; Windows acceptance evidence does not
exist.

**Base commit:** the commit that contains this file. It is the foundation-freeze
SHA recorded by the controller immediately before creating lane worktrees.

## Platform floor

- Windows 10 22H2 (build 19045) x64 and Windows 11.
- WSL 2.0.0+ from the Store/MSI with systemd.
- Linux/WSL new-install root `/data/devstack`; legacy `~/.devstack` preserved.
- Windows root `C:\devstack`.
- Go 1.26+; Node 22 for UI/CI.

## Gate evidence

| Gate | Command | Result |
| --- | --- | --- |
| Linux tests | `go test ./...` in WSL ext4 (`/data/devstack/src`) | pass, 55 packages |
| Windows packages | `GOOS=windows GOARCH=amd64 go build ./...` | pass |
| Windows test compile | `go test -run '^$' ./...` on Windows | pass |
| Vet | `go vet ./...` on Windows | pass |
| Shell syntax | `bash -n install.sh test/acceptance/m1.sh test/release/*.sh` | pass |
| Release workflow contract | `bash test/release/workflow_test.sh` | pass |
| Installer contract | `bash test/release/installer_test.sh` | pass |

The Windows cross-build compiles packages; it does not execute Windows services,
named pipes, TLS, WSL provisioning, or firewall/trust tests. Those are
controller-serialized acceptance gates.

## Frozen interfaces

- `internal/ipc`: Unix socket/named pipe endpoints, protected pipe DACLs, peer
  UID/SID verification.
- `internal/agentproto`: complete typed v1 protocol, paths, errors, timeouts.
- `internal/agentapi.Module`: `Name`, `MountPath`, `Handler`; Lane D modules
  mount behind the authenticated agent server.
- `internal/agentclient`: pinned-HTTPS bearer-token client and `API` interface.
- `internal/securestore`: DPAPI on Windows, 0600 files in WSL.
- `internal/pathmode`: `SourceLocal`/`SourceWindows`/`SourceWSL`, translation,
  source validation, proxy document roots.
- `internal/wsl`: `Runner`, distro parsing, path/copy/validation, localhost
  forwarding probe.
- `internal/ports.Provider`: Linux host allocator or WSL-agent allocator.
- `internal/backend/wsl`: typed skeleton for Lane C.
- `internal/platform`: roots, no-dot policy, capabilities, process, terminal,
  performance, doctor, build, and shim seams.
- `internal/daemon`: stable per-home endpoint, LockFileEx lifetime/spawn locks,
  Windows ready/lock paths.

## Database

Schema version 7 adds `sites.source_mode TEXT NOT NULL DEFAULT 'local'`.
Legacy rows remain local. Legacy `.config-manifest.json` and
`.serving-provider` are read/renamed once; new writes are non-dot.

## Known lane-owned stubs

These compile and fail closed with typed errors:

- Windows helper (`internal/helper/windows_stub.go`, `cmd/devstack-helper/main_windows.go`) — Lane A.
- Windows daemon `Run`/`EnsureRunning` — Lane C.
- WSL agent server/provisioning/ingress — Lane B.
- Agent Docker/relay/web modules — Lane D.
- Windows source-mode UI/CLI/doctor/tooling — Lane E.
- `internal/backend/wsl` methods — Lane C.
- Windows terminal/performance/build execution — lanes E/C.

## Parallelism

All five Wave 1 lanes start from this commit concurrently. Merge order
A→B→C→D→E is the module-mounting order, not a serialized start order.
