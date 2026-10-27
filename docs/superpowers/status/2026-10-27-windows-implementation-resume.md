# Windows/WSL implementation resume state

**Date:** 2026-10-27
**Status:**
- Wave 0: Complete and frozen (`992732b`).
- Task T0.11: Complete (native Windows test partitioning & shared portability fixes).
- Wave 1 (Lanes A, B, C, D, E): 100% implemented, independently reviewed, approved, and merged into `feature/windows-wsl`.
- Wave 2 Lane G (Installers, Lifecycle, RootFS, & Prerequisites): 100% implemented, independently reviewed, approved, and merged into `feature/windows-wsl`.
- Next Phase: Wave 2 parallel lanes (Lanes F, H, I, J) grounded in Lane G's installer and managed WSL distro.
**Main worktree:** `C:\Users\Aram\Projects\Go\dev-web-stack`
**Integration branch:** `feature/windows-wsl`
**Rule:** When using subagent-driven development skills, only Gemini 3.8 Flash (`Model: "flash"`) is allowed.

---

## 1. What happened

1. Wave 0 (T0.1–T0.10) was implemented inline by the controller and frozen at
   commit `992732b`. Evidence is in
   `docs/superpowers/status/2026-10-27-windows-baseline.md`.
2. Five lane worktrees were created from `992732b` and five implementer
   subagents were dispatched concurrently (A–E).
3. While they ran, the controller fixed two shared blockers found by Lane C:
   the Windows SQLite DSN/migration backup (`d9895e9`) and four contract gaps
   (`dd7e939`: agent port, CA-install RPC, site domain, CLI process liveness).
4. All five subagents were terminated by the provider with
   **"Go usage limit exceeded"**. This was a quota stop, not a code failure.
   Every unfinished edit was recovered and committed as a `wip:` commit so no
   work is lost.
5. The controller merged `feature/windows-wsl` into every lane branch so all
   lanes share the foundation fixes. Those merges are clean and committed.
6. No lane was reviewed, merged, or accepted. No real-machine Windows test was
   run.

---

## 2. Branch map (authoritative tips)

| Lane | Branch | Tip | State |
| --- | --- | --- | --- |
| Foundation | `feature/windows-wsl` | see `git log -1` | Wave 0 + shared fixes + this doc |
| A helper | `lane/windows-helper` | `2120b6d` | A1–A3 committed, A4 committed as WIP |
| B agent | `lane/wsl-agent` | `83fbfe5` | B1–B5 committed |
| C daemon | `lane/windows-daemon` | `5391e7a` | C1–C4 partial; C3 WIP committed |
| D docker | `lane/docker-services` | `dd02bbb` | D1–D3 committed, D4 WIP committed, D5 not started |
| E source/UI | `lane/site-source` | `01cc25b` | E1–E4 committed |

Every lane tip contains a merge of `feature/windows-wsl` through `dd7e939`.
Do not rebase or recreate the worktrees; continue on these branches.

Commits that matter on the foundation:

```text
4e20f4b chore: add windows build and test baseline
9fc0b87 feat: add cross-platform IPC with pipe ACLs
22e81b1 feat: define cross-platform roots and dot-name policy
5c07fc0 feat: persist site source mode and path contract
b2e6760 feat: add authenticated WSL agent protocol
6bacc3e feat: add WSL runner, path, copy, and forwarding seams
8121ffd refactor: select daemon transport and lifecycle per platform
f0a8a8a feat: add platform capability and process seams
7e433fc refactor: freeze backend, port, route, and PHP path contracts
2bcfb99 docs: record windows/wsl foundation baseline
992732b docs: point baseline at the freeze commit   <- lane base
8cc52a2 docs: require windows 10 support and full prerequisites
f8001d0 test: add windows vertical slice script
d9895e9 fix: make sqlite and migration backups work on windows
dd7e939 fix: close windows daemon foundation contract gaps
```

---

## 3. Verified foundation state

Re-verified on 2026-10-27 after the shared fixes:

| Gate | Result |
| --- | --- |
| Linux `go test ./...` in WSL ext4 (`/data/devstack/foundation`) | **PASS** (exit 0) |
| `go vet ./...` (Windows) | PASS |
| `GOOS=windows GOARCH=amd64 go build ./...` | PASS |
| `GOOS=windows GOARCH=amd64 go test -run '^$' ./...` | PASS (test compilation) |
| `bash -n install.sh test/acceptance/m1.sh test/release/*.sh` | PASS |
| `bash test/release/workflow_test.sh` | PASS |
| `bash test/release/installer_test.sh` | PASS |
| Native Windows `go test ./...` | **FAIL** across ~21 packages; see §6 |

Foundation packages and frozen interfaces:

- `internal/ipc` — Unix/named-pipe endpoints, protected DACL, peer UID/SID.
- `internal/agentproto` — full typed v1 protocol, including `PathCAInstall`.
- `internal/agentapi.Module` — `Name`/`MountPath`/`Handler` seam.
- `internal/agentclient` — pinned HTTPS + bearer, `API` interface, `InstallCA`.
- `internal/securestore` — DPAPI on Windows, 0600 files in WSL.
- `internal/pathmode` — source modes, translation, proxy document roots.
- `internal/wsl` — runner, distro parsing, path validation, verified copy,
  localhost-forwarding probe.
- `internal/ports.Provider` + `internal/ports.Local`.
- `internal/platform` — roots, no-dot policy, capabilities, process/shim seams.
- `internal/config` — `projects_root`, `agent_port` (default 7443).
- SQLite schema v7 with `sites.source_mode`.
- Windows SQLite uses `/C:/...` URI paths; migration backups open `O_RDWR`.

---

## 4. Lane-by-lane state and exact stopping points

### Lane A — Windows helper

Branch `lane/windows-helper`, tip `2120b6d`.

**Committed tasks**

| Task | Commit | Contents |
| --- | --- | --- |
| A1 named pipe + SID | `4d806fb` | `internal/helper/peer_windows.go`, `server_windows_test.go`, pipe ACL via `ipc`, Windows main |
| A2 service lifecycle | `178fdd3` | `internal/helper/service_windows.go`, `cmd/devstack-helper/service_windows.go`, install/uninstall |
| A3 host integration | `9829848` | `internal/winhosts`, `internal/wintrust`, `internal/winfirewall`, `ca_windows.go` |
| A4 (WIP) forwarders/config | `63b0e19` | `forwarder_windows.go`, `proxy_targets_windows.go`, `cmd/devstack-helper/config_windows.go`, config persistence |

**Verified**

- `go build ./...`, `go vet ./...`, Windows test compilation: PASS.
- `go test ./internal/helper ./internal/winhosts ./internal/wintrust ./internal/winfirewall ./cmd/devstack-helper -count=1`: **PASS**.
- `go test ./internal/helper -run 'ProxyTargets|Forwarder|Windows'`: PASS.
- `winserver.Start` enables the fixed 80/443 forwarders to `127.0.0.1:8080/8443`
  and rejects non-default proxy targets.

**Stopped**

- Immediately after committing the A4 implementation. The subagent was killed
  before writing `.superpowers/lane-report.md`, before a self-review, and
  before the controller-serialized real smoke.

**Remaining for A**

1. Review A4 as a finished task: forwarder restart, target persistence
   (`C:\devstack\system\helper.yaml`), rejection of non-loopback targets,
   idempotent enable/disable. Unit tests exist; confirm coverage against the
   plan's A4 checklist and add any missing RED test.
2. Write the lane report (`status`, commits, tests, concerns).
3. Controller-serialized real smoke (not the implementer): install the helper
   service as LocalSystem, connect from the installing SID, confirm a foreign
   SID is rejected, confirm 80/443 forward to 8080/8443, restart restores
   forwarders, confirm CA key is not readable by the daemon user.
4. Confirm firewall rules scope the daemon's WSL-facing listener to the WSL
   subnet and the relay rules only to fixed ports.
5. Nothing in Lane A is merged. Do not merge before review.

### Lane B — WSL provisioning, agent, ingress

Branch `lane/wsl-agent`, tip `83fbfe5`.

**Committed tasks**

| Task | Commit | Contents |
| --- | --- | --- |
| B1 provisioning | `2450b00`, reworked in `c2d27b0` | `internal/wsl/provision.go`, `build/wsl/*`, fake wsl tests |
| B2 agent server | `8c72394` | `cmd/devstack-agent`, `internal/agent/{server,auth,config,health}.go` |
| B3 static/ports | `ab2e0f5` | `internal/agent/{static,runtime,process,ports}.go` |
| B4 Docker engine | `3e4a81e` | `internal/agent/docker_engine.go`, `build/wsl/{agent.service,docker-daemon.json}` |
| B5 hosts/ingress | `406ae43` | `internal/agent/{hosts,ingress}.go`, `internal/wsl/hosts_config.go` |

`cmd/devstack-agent/main.go` wires runtime reconciliation, Docker engine,
config/token/TLS loading, the fixed 80/443 ingress with gateway refresh, and
the agent server. The server mounts `agentapi.Module` values and rejects
modules not under `/v1/`.

**Verified**

- `go build`, `go vet`, Windows test compilation: PASS.
- `go test ./internal/agent ./internal/wsl ./cmd/devstack-agent -count=1`:
  **PASS**.
- Full Linux suite in WSL (`/data/devstack/lane-wsl-agent`): **PASS** (exit 0).

**Stopped**

- After committing B5. No lane report, no self-review, no real Debian WSL
  acceptance.

**Remaining for B**

1. Write the lane report.
2. Real WSL acceptance (controller-serialized): import the pinned Debian 12
   rootfs, verify systemd, `wsl --version` ≥ 2.0.0, Docker Engine with
   `--data-root /data/devstack/docker`, Compose, agent token/TLS modes
   (root 0600/0700), `.test` resolution to `127.0.0.1`, ingress 80/443 to the
   Windows gateway 8080/8443, and `curl https://devstack.test` from WSL.
3. Verify `Internal/agent/hosts.go` CA bundle injection is actually applied to
   site and terminal environments (functions exist; wiring into runtime exec
   paths must be proven).
4. Mount Lane D modules in `cmd/devstack-agent` during integration (D owns
   them; B's server only mounts).
5. Build/publish the pinned rootfs and `versions.lock` checksums; the scripts
   exist but no artifact was produced.
6. `devstack init` user phase composition is not done (belongs with C4).

### Lane C — Windows daemon, public proxy, WSL backend

Branch `lane/windows-daemon`, tip `5391e7a`. **This is the largest blocker.**

**Committed**

| Task | Commit | Contents |
| --- | --- | --- |
| C1 startup/lifecycle | `cd85a7a`, `b0c54d0` | `internal/daemon/{windows,windows_test}.go`, lifecycle, pipe |
| C2 public proxy | `dbe694a` | `internal/proxy/windows_listen.go`, listener + dashboard route + target ordering |
| C3 WSL backend | `05707d1`, `5391e7a` | `internal/backend/wsl`, `internal/ports` agent provider, CA install |
| C4 bootstrap helpers | `2273e14` | `internal/daemon/windows_bootstrap.go` helpers |

**Verified**

- `go build`, `go vet`, Windows test compilation: PASS.
- `go test ./internal/backend/wsl ./internal/ports ./internal/client -count=1`:
  PASS.
- Focused proxy Windows listener tests: PASS.
- `internal/store` now fully passes natively after `d9895e9`.

**Still failing**

```text
--- FAIL: TestWindowsRunOpensSQLiteWritesLogAndCleansReadiness
    windows_test.go:33: Run returned before readiness:
    daemon: update helper proxy targets before reconcile:
    helperclient: POST /v1/proxy/targets: ...
    ipc: dial \\.\pipe\devstack-helper:
    open \\.\pipe\devstack-helper: The system cannot find the file specified.
```

The Windows `Run` path hard-fails when the helper is not already listening.
That ordering must be reconciled with C4's bootstrap (`helper → agent → proxy →
reconcile`) instead of failing readiness.

**Remaining for C (in order)**

1. Compose the real `Run` path: `loadWindowsAgentClient` is implemented but
   **not called** by `Run`; `Run` still passes placeholder/nil dependencies to
   `api.NewServer`, and does not build a `sites.Service`.
2. Wire a Windows `sites.Service` with:
   `backend/wsl`, `ports` agent provider, `proxy.Router`, `certs` manager,
   logs store, and route rebuild. All Windows sites use WSL; no local
   supervisor, no Docker PHP.
3. Dashboard/public TLS: obtain the dashboard leaf certificate from the certs
   manager before serving 8443. Currently the TLS listener has no valid
   certificate.
4. `Backend.InstallCA` should call `agentclient.InstallCA` (the RPC and client
   method now exist). Confirm it does; if it still returns
   `ErrCAInstallUnsupported`, finish it.
5. Helper target update must be ordered before reconcile but must not make
   daemon readiness depend on the helper process already running; use the
   bootstrap sequence and a typed retry/diagnostic.
6. Windows CLI `daemon status|stop|restart` over the named pipe. Process
   liveness is fixed (`processRunning` is build-tagged), but stop/restart
   still needs a Windows path.
7. A green end-to-end Windows test: temp `C:\devstack-test\<lane>` root, high
   ports, fake helper pipe, SQLite open, log + readiness markers, clean
   shutdown. This is the C1 acceptance test.
8. Rewrite `.superpowers/lane-report.md`; the existing one predates `d9895e9`
   and `dd7e939` and still says the SQLite DSN blocks C1.

### Lane D — Docker services and private web adapter

Branch `lane/docker-services`, tip `dd02bbb`.

**Committed**

| Task | Commit | Contents |
| --- | --- | --- |
| D1 agent Docker module | `94a7708` | `internal/agentdocker/docker.go` (`agentapi.Module`, fixed IDs, Compose) |
| D2 servicecontrol adapter | `2040a5f` | `internal/servicecontrol/agent_{docker,paths}.go` |
| D3 private web serving | `86aee41` | `internal/agentweb`, `internal/dockerweb/windows_provider.go`, `internal/vhost/agent_adapter.go` |
| D4 (WIP) config/backup/rootfs | `b18ca53` | `agentdocker/{config,backup}.go`, `servicecontrol/agent_config.go`, `wsl/docker_rootfs_test.go` |

**Verified**

- `go build`, `go vet`, Windows test compilation: PASS.
- `go test ./internal/agentdocker ./internal/agentweb ./internal/dockerweb -count=1`: PASS.
- `go test ./internal/vhost` fails on Windows because the shared renderer
  rejects `/etc/devstack/...` as non-absolute (see §6); this is a shared bug,
  not a D regression.

**Stopped**

- During D4. **D5 was never started**: `internal/agentrelay/` does not exist
  and `internal/helper/service_relay_targets_windows.go` does not exist.

**Remaining for D**

1. Finish D4: agent-side service config read/write/apply/reset, 0700 secrets,
   backups, deletion confirmation; verify `agentdocker/config.go` is wired to
   the module's handlers.
2. Implement D5: `internal/agentrelay/service_relay.go` as an
   `agentapi.Module` with a fixed MySQL/MariaDB/PostgreSQL/Redis/phpMyAdmin
   port allowlist, WSL-loopback relay, and Windows helper loopback forwarding
   targets. Reject arbitrary ports. Add tests, including module mounting.
3. Write the lane report.
4. Controller integration: mount `agentdocker`, `agentweb`, and `agentrelay`
   modules in Lane B's `cmd/devstack-agent` after B is merged.
5. Real WSL Docker acceptance (controller): private HTTP port 18080, daemon
   public TLS, no container dashboard vhost, no NTFS bind mounts.

### Lane E — Source modes, paths, CLI/UI, doctor

Branch `lane/site-source`, tip `01cc25b`.

**Committed**

| Task | Commit | Contents |
| --- | --- | --- |
| E1 source modes/copy | `507f1bd` | `internal/sites/{source_mode,source_copy}.go`, service/API/CLI |
| E2 path policy | `c513a77` | `internal/pathmode` expansion + docs |
| E3 dashboard selector | `b151ce9` | `ProjectSourceMode.svelte`, AddSite, Settings, e2e spec |
| E4 diagnostics | `bff0552` | `internal/doctor` Windows checks, settings UI |

**Verified**

- `go build`, `go vet`, Windows test compilation: PASS.
- **UI:** `npm run check` → 0 errors/0 warnings; `npm test -- --run` →
  **14 files, 113 tests passed**.
- `internal/pathmode` tests pass on Windows.

**Known failures / unfinished**

- `internal/api` on Windows fails `TestAPIContractMatchesOpenAPI`:
  `openapi missing path "/api/v1/sites/{ref}/source"`. **`api/openapi.yaml`
  was not updated** for the source-mode endpoint or the `source_mode` fields.
- `internal/doctor` `TestCheckToolsOKWhenManagedShimsWin` fails on Windows:
  the check looks for extensionless `php`/`composer` shims, but Windows uses
  `.cmd`. The check must be platform-aware.
- `internal/tooling` Windows tests fail when they execute Unix-era shell
  wrappers/permission assertions; the new `internal/tooling/windows.go` exists
  but the test matrix is not partitioned.
- Linux regression for this lane was **not recorded** (the run was interrupted).
  It must be run before merge.
- Playwright e2e was not run.

**Remaining for E**

1. Update `api/openapi.yaml`: `POST /api/v1/sites/{ref}/source`, request/response
   schemas, and `source_mode` on site create/read/update.
2. Make the doctor tool check accept Windows `.cmd` shims and the WSL-backed
   shim contract.
3. Partition/fix `internal/tooling` Windows tests; prove `EnsureWindowsToolShims`
   quoting with paths containing spaces.
4. Run the Linux regression suite for the lane and record the result.
5. Run Playwright e2e against the UI.
6. Write the lane report.

---

## 5. What was never done at all

These are **not** partial; they have no implementation yet:

- Integration 1 (`I1`): no lane merged into `feature/windows-wsl`; no conflict
  resolution; no module mounting of Lane D into Lane B's agent.
- Integration 2 (`I2`): no Windows vertical slice on a real machine.
- Any reviewer subagent or review verdict for any lane.
- Any real-machine Windows action: helper service install, named-pipe ACL test
  with a foreign SID, 80/443 bind, hosts/trust/firewall mutation, WSL distro
  import, Docker container start, Windows 10 run.
- `install.ps1` and the full resumable Windows/WSL prerequisite installer
  (Wave 2, Lane G). The requirement is recorded in `docs/decisions.md` S74 and
  the spec; no code exists.
- Wave 2 lanes (PHP/Node/terminal/tooling parity, installers, Docker parity,
  performance/config, packaging/acceptance).

---

## 6. Cross-cutting blocker: native Windows test suite is red

`go test ./...` on Windows fails in ~21 packages. The failures are **not**
Windows-feature failures; they are the existing Linux-oriented test suite and a
handful of real portability bugs. This must be fixed before any lane can claim
a green Windows suite.

Observed failure classes (examples are from current lane runs):

| Class | Examples |
| --- | --- |
| Unix permission bits | `hosts_test.go` mode `666` vs `640`; `servicecontrol/database_test.go` `0666` vs `0600/0644/0755`; `tooling/php_test.go` shim modes; `store` backup modes |
| POSIX absolute paths rejected by `filepath.IsAbs` on Windows | `phpfpm` build root `/opt/php`; `vhost` document root `/home/u/blog/public`; TLS dir `/etc/devstack/tls` |
| `/proc`, cgroups, rlimits | `performance` collectors; user `DOMAIN\user` rejected |
| Unix shell execution | `tooling` composer/php wrappers; `cli` exec tests |
| Error-string/errno differences | `forwarder` expects `EADDRINUSE`, Windows returns "Only one usage…" |
| Reserved Windows names | `staticserve` test path `a?b` |
| euid/root semantics | `api` terminal test refuses root; `domain` private-dir check |
| OpenAPI/API contract | missing `/api/v1/sites/{ref}/source` |

**Recommended new foundation task (call it T0.11):**

1. Partition the test suite by platform. Tag genuinely Linux-only tests
   `//go:build !windows` (permission-bit assertions, `/proc`, Unix shells,
   cgroup, errno strings, reserved-name fixtures). Keep Windows-relevant tests
   running natively.
2. Fix the real portability bugs in shared code rather than tagging them:
   - platform-aware absolute-POSIX-path validation used by `vhost` and `phpfpm`;
   - `forwarder` address-in-use mapping;
   - `performance` account parsing for `DOMAIN\user`;
   - `doctor` tool shim extension (`.cmd`);
   - `domain.ValidateProjectPath` private-dir/ownership behavior on Windows;
   - `api/openapi.yaml` source endpoint (Lane E).
3. Add a documented two-command gate:
   - Windows: `go test ./...` (after partitioning) and
     `go vet ./...`, `GOOS=windows go build ./...`.
   - Linux regression: `go test ./...` in WSL ext4.
4. Record the resulting counts in the baseline.

Until T0.11 lands, use the focused package commands in §4 as the per-lane
signal, and never report "all tests pass" from a Windows run.

---

## 7. Resume order (do not reorder without a recorded ruling)

1. Read this file, `docs/development-windows.md`, the plan, and the spec.
2. Do **T0.11** (test partitioning + shared portability bugs). Commit to
   `feature/windows-wsl`.
3. Merge `feature/windows-wsl` into every lane again.
4. Continue the lanes **where they stopped**, not from their first task:
   - **A**: finish A4 review + report; then controller smoke.
   - **C** (hardest): compose `Run`, sites.Service, dashboard TLS, agent
     discovery, CA install, helper ordering, CLI pipe, green readiness test.
   - **D**: finish D4, then implement D5 relay from scratch, then report.
   - **B**: report + real WSL acceptance, CA bundle wiring proof, mount D
     modules after D merges.
   - **E**: OpenAPI, doctor `.cmd`, tooling tests, Linux regression, Playwright,
     report.
5. Dispatch one fresh reviewer per lane (max 5). Fix Critical/Important with
   RED→GREEN.
6. Merge **A → B → C → D → E** into `feature/windows-wsl`, running focused
   tests after each merge. Mount D's modules during the B/C integration.
7. Integration 1: run `test/windows/vertical_slice.ps1` on a real Windows 10
   22H2 and Windows 11 machine. Do not start Wave 2 if either source mode or
   either client path cannot serve trusted `.test` HTTPS.
8. Only then Wave 2 (including `install.ps1` and the full prerequisite
   manifest).

---

## 8. Acceptance gates and claims

Must remain true until evidence exists:

- Windows support is **planned/in progress**, never "supported".
- No release artifact includes Windows until packaging and acceptance land.
- A green Windows cross-build is not a green test run.
- A lane self-report is not a review.
- Linux regression must run in WSL ext4 or a Linux checkout, never `/mnt/c`.
- No real hosts/trust/firewall/service/80-443/Docker/WSL action without the
  controller's serialized evidence.

Required evidence before claiming Windows support:

- Windows 10 22H2 and Windows 11 vertical slice (trusted HTTPS from Windows and
  WSL, dashboard, both source modes, doctor).
- Linux `go test ./...` green at the same commit.
- Named-pipe foreign-SID rejection; helper 80/443 to daemon; no WSL target.
- WSL import/systemd/Docker/localhost forwarding.
- Private Docker web port + daemon TLS, no NTFS service data.
- Upgrade/uninstall, reboot recovery, unmark/adopt refusal.

---

## 9. Environment notes

- Host: Windows, Go `1.27.0 windows/amd64`; Node `24.22.1` (CI baseline is 22).
- WSL: `Ubuntu` (26.04) with Go `1.26.8`, `make`, `build-essential`;
  `/data/devstack` exists and is owned by `aram`. `FedoraLinux-44` is also
  installed but was not used.
- Linux regression copies used:
  `/data/devstack/foundation`, `/data/devstack/lane-<name>`,
  `/data/devstack/src`. These are disposable copies; the git worktrees are the
  source of truth.
- Repository-local git identity: `Aram <aram.zahedi@247labs.com>`.
- Subagent failures were quota, not code. Do not interpret them as lane
  completion or as lane failure.

---

## 10. Recorded rulings

- Windows floor is **Windows 10 22H2 (19045) x64 and Windows 11**
  (`docs/decisions.md` S73).
- The installer owns a **complete resumable Windows + WSL prerequisite
  manifest** with explicit reboot opt-in (S74).
- Wave 1 lanes start concurrently; merge order A→B→C→D→E is module mounting,
  not a serialized start.
- Windows site backends are always WSL; the daemon never runs a local
  supervisor or Docker PHP.
- SQLite, config, leaf certs, daemon logs, cache stay on Windows; generated
  data, service config/data/secrets, runtimes, logs, Docker data stay in WSL
  ext4.
- Public entry is helper 80/443 → daemon 8080/8443; WSL ingress is fixed 80/443
  → host gateway.
