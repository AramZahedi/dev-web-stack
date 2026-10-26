# Windows 11 + WSL2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development and superpowers:test-driven-development. Parallel lanes use superpowers:dispatching-parallel-agents. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class Windows 11 development platform with a native Windows daemon/helper, a managed WSL2 Linux agent for PHP/Node/static, and Docker service containers, while preserving the existing Linux product.

**Architecture:** The Windows daemon owns UI/API, SQLite, routes, and the builtin proxy. A LocalSystem helper owns Windows trust, hosts, firewall, CA, and 80/443. A managed Debian 12 WSL2 agent owns Linux PHP-FPM/Node/static, logs, ports, and Docker services. Docker PHP and native Windows PHP are excluded.

**Tech Stack:** Go, Svelte dashboard, SQLite, Windows named pipes, LocalSystem service, WSL2, Debian 12, systemd, PHP-FPM, Node, Docker Engine/Compose, PowerShell installer.

**Spec:** `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`

## Global Constraints

- Linux and WSL application root: `/data/devstack`; Windows root: `C:\devstack`.
- Default projects root: `/data/devstack/projects` on Linux/WSL, `C:\devstack\projects` on Windows.
- No devstack-created Windows file or directory starts with a dot.
- Site source modes: `local` (Linux), `windows`, `wsl`; default PHP/Node to `wsl`, static to `windows` on Windows.
- Windows-mode project paths map `C:\...` ↔ `/mnt/c/...`.
- WSL-mode project paths map `\\wsl.localhost\devstack\...` ↔ `/...`.
- Reject arbitrary UNC/device paths and cross-boundary symlinks/junctions.
- SQLite, daemon config, logs, backups, leaf certs, and CA stay on Windows NTFS in Windows deployments.
- CA private key: `C:\devstack\system\pki` or `/data/devstack/system/pki`, never WSL/Docker.
- Docker is only for nginx/Apache, MySQL/MariaDB, PostgreSQL, Redis, phpMyAdmin. No Docker PHP.
- PHP runs as native Linux PHP-FPM in WSL/Linux.
- Docker service data lives under `/data/devstack/services/<id>/data`; never an NTFS bind mount.
- Native Linux Docker engine data remains `/var/lib/docker`; dedicated WSL Docker data-root is `/data/devstack/docker`.
- Existing Linux installs and helper paths are detected and preserved; no automatic layout migration.
- Windows helper owns Windows 80/443; Linux helper owns Linux 80/443.
- WSL agent listens on the NAT interface with bearer auth; mirrored networking is rejected.
- No implementation is merged without a watched RED test and a green suite.
- At most **5 active subagents** at any time; never 3 as a hard cap.

## Review Focus

- A Windows path or WSL UNC path is accepted where the opposite mode is required.
- A cross-boundary symlink/junction escapes the project root or bypasses source-mode validation.
- SQLite or the CA key is written through `/mnt/c` or `\\wsl.localhost`.
- Docker database/Redis data ends up on NTFS.
- A Windows helper pipe is reachable by a SID other than the installing user/SYSTEM/Administrators.
- WSL agent HTTP requests succeed without the bearer token or from a non-NAT interface.
- Helper 80/443 targets survive daemon/wsl IP changes and helper restart.
- A Windows project is created with Linux-only PHP extensions missing because the runtime accidentally came from Docker or Windows.
- Existing Linux paths, tests, E2E, and Docker service behavior regress.

---

## Parallel Execution Protocol

The controller is the main OpenCode session. It dispatches implementers and reviewers with the OpenCode `subagent` tool, `agent: "general"`. The controller alone merges branches, pushes, and runs the final gate. Implementation is expected on a Windows 11 host with WSL2 and Docker; Linux regressions run in WSL or a Linux checkout using the same worktree layout.

### Worktrees and branches

1. Controller creates the integration branch and one worktree per lane:
   - integration: `feature/windows-wsl` at the foundation commit.
   - `lane/windows-helper`
   - `lane/wsl-agent`
   - `lane/windows-daemon`
   - `lane/docker-services`
   - `lane/site-source`
2. Every lane branch starts from the same frozen foundation commit. Lanes do not merge each other.

   ```sh
   git switch master
   git switch -c feature/windows-wsl
   # after Wave 0 foundation is committed as <foundation-sha>:
   git worktree add ../devstack-lanes/helper -b lane/windows-helper <foundation-sha>
   git worktree add ../devstack-lanes/wsl-agent -b lane/wsl-agent <foundation-sha>
   git worktree add ../devstack-lanes/windows-daemon -b lane/windows-daemon <foundation-sha>
   git worktree add ../devstack-lanes/docker-services -b lane/docker-services <foundation-sha>
   git worktree add ../devstack-lanes/site-source -b lane/site-source <foundation-sha>
   ```

3. Each lane has one implementer subagent. The implementer owns its branch/worktree and may make multiple TDD commits.
4. Maximum **5 active subagents**, including implementers and reviewers. Do not dispatch a sixth until one finishes.
5. Implementer subagents must not spawn subagents. Reviewers are dispatched by the controller only.
6. Lane implementers may not edit files owned by another lane. Changes to foundations after freeze go through the controller.
7. Implementers return: status, commit range, test commands/results, concerns, and a lane report path. Full report goes to a file; the return message stays short.

### Lane waves

- **Wave 0 — Foundation (sequential):** Tasks T0.1–T0.9. One implementer/controller stream. No parallel lane starts until interfaces compile and foundation tests pass.
- **Wave 1 — Core parallel lanes:** Five implementers, one per lane, run concurrently from the frozen foundation.
- **Integration 1:** Controller merges A→B→C→D→E, resolves owner conflicts, runs the vertical-slice gate.
- **Wave 2 — Parity lanes:** Five implementers, one per next lane, from the integrated base.
- **Integration 2:** Merge F→G→H→I→J, run full Windows/Linux gates, whole-branch review.
- **Pause:** After planning only. Implementation starts only after the human partner reviews this plan.

### Implementer dispatch template

For each lane, the controller dispatches one `general` subagent with a prompt containing:

- lane name, branch, worktree path, and frozen base SHA;
- spec path and the exact task section; never the whole plan or session history;
- the requirement to use `superpowers:test-driven-development` for every production change;
- the instruction not to spawn subagents;
- the requirement to commit each task, run the full relevant suite, and write a full lane report to a file;
- the allowed file ownership list and the prohibition on editing other lanes;
- the report contract: status, commit range, tests with results, concerns, unresolved interfaces.

The controller records the base commit before dispatch, verifies the worktree is isolated, and does not edit the lane while its implementer is active.

### Reviewer dispatch template

After a lane report arrives, the controller:

1. Creates a diff package from the lane base to lane head without loading it into the controller context.
2. Dispatches one fresh reviewer subagent with the diff package, spec, task section, and Global Constraints.
3. Requires two verdicts: spec compliance and code quality, with severity-ranked findings.
4. Sends Critical/Important findings back to the same lane implementer for a RED→GREEN fix; re-reviews only the scoped fix.
5. Merges only after both verdicts are clean.

Reviewers never edit code and never review while more than five total subagents are active.

### Reviews and merging

- After each wave, controller creates a diff package for each lane and dispatches one fresh reviewer per lane, max 5 concurrently.
- Review has two verdicts: spec compliance and code quality. Critical/Important findings go back to that lane's implementer; controller merges only clean lanes.
- Merge order is dependency order, not completion order. After each merge, run the lane's focused tests plus `go test ./...`.
- Integration conflicts are resolved by the controller; shared foundation files are never edited by two lanes.
- Final whole-branch review uses a fresh reviewer. A single fix pass follows for Critical/Important findings, each with a RED→GREEN test, then the full gate.
- Use `superpowers:using-git-worktrees`, `superpowers:subagent-driven-development`, `superpowers:dispatching-parallel-agents`, `superpowers:test-driven-development`, and `superpowers:verification-before-completion`.

---

## File Structure Map

### Foundation files

- Create: `.gitattributes` — LF for shell, Go, SQL, YAML, Makefile, WSL scripts.
- Create: `internal/ipc/ipc.go`, `internal/ipc/ipc_unix.go`, `internal/ipc/ipc_windows.go` — Unix socket/named-pipe endpoint.
- Create: `internal/agentproto/protocol.go`, `internal/agentproto/types.go` — agent API contract.
- Create: `internal/agentclient/client.go` — authenticated agent client.
- Create: `internal/wsl/runner.go`, `internal/wsl/path.go` — `wsl.exe` seam and path translation.
- Modify: `internal/config/config.go`, `internal/config/paths.go` — roots, projects root, Windows platform defaults.
- Modify: `internal/model/model.go` — source mode and source paths.
- Create: `internal/store/migrations/0007_site_source_mode.sql`.
- Modify: `internal/store/store.go`, `internal/store/store_test.go`.
- Create: `internal/platform/platform.go`, `internal/platform/platform_windows.go`, `internal/platform/platform_linux.go` — OS capabilities.
- Modify: `cmd/devstack/main.go`, `cmd/devstack-helper/main.go` only for cross-platform compile seams.

### Lane A — Windows helper

- Create/modify: `internal/helper/peer_windows.go`, `internal/helper/peer_linux.go`
- Create: `internal/helper/service_windows.go`
- Create: `internal/winhosts/`, `internal/wintrust/`, `internal/winfirewall/`, `internal/forwarder/windows_target.go`
- Modify: `internal/helper/proxy_targets.go`, `internal/helper/server.go`
- Modify: `cmd/devstack-helper/main.go`, `cmd/devstack-helper/config.go`
- Create: `internal/helper/windows_config_test.go`, `internal/helper/windows_service_test.go`, `internal/winhosts/*_test.go`, `internal/wintrust/*_test.go`

### Lane B — WSL provisioning and agent

- Create: `cmd/devstack-agent/main.go`
- Create: `internal/agent/server.go`, `internal/agent/auth.go`, `internal/agent/config.go`, `internal/agent/health.go`
- Create: `internal/wsl/provision.go`, `internal/wsl/distro.go`, `internal/wsl/rootfs.go`
- Create: `build/wsl/README.md`, `build/wsl/build-rootfs.sh`, `build/wsl/agent.service`
- Create: `internal/agent/*_test.go`, `internal/wsl/*_test.go`
- Create: `test/wsl/fake_wsl_test.go`

### Lane C — Windows daemon and public proxy

- Create: `internal/daemon/windows.go`, `internal/daemon/windows_test.go`
- Create: `internal/client/transport_windows.go`, `internal/client/transport_unix.go`
- Create: `internal/proxy/windows_listen.go`
- Modify: `internal/daemon/daemon.go`, `internal/config/config.go`
- Modify: `internal/daemon/daemon_test.go`
- Create: `internal/backend/wsl/backend.go`, `internal/backend/wsl/backend_test.go`
- Create: `internal/agentclient/client_test.go`

### Lane D — Docker services and web-serving adapter

- Create: `internal/agent/docker.go`, `internal/agent/docker_test.go`
- Create: `internal/servicecontrol/agent_docker.go`, `internal/servicecontrol/agent_docker_test.go`
- Create: `internal/services/agent_targets.go`
- Modify: `internal/dockerweb/`, `internal/vhost/` only through adapter interfaces.
- Create: `internal/agent/web_targets.go`, `internal/agent/web_targets_test.go`
- Create: `internal/wsl/docker_rootfs_test.go`

### Lane E — Source modes, paths, CLI/UI

- Create: `internal/sites/source_mode.go`, `internal/sites/source_mode_test.go`
- Create: `internal/pathmode/` — path translation/validation, tests.
- Modify: `internal/sites/service.go`, `internal/sites/php_config.go`, `internal/cli/site.go`, `internal/api/handlers_sites.go`
- Modify: `ui/src/lib/AddSite*`, `ui/src/lib/components/PerformancePanel.svelte` only for platform labels, `ui/tests/e2e.spec.ts`
- Create: `ui/src/lib/components/ProjectSourceMode.svelte` and test.
- Modify: `internal/doctor/checks.go` and tests for Windows/WSL checks.

### Wave 2 / integration files

- Create: `install.ps1`, `build/windows/` packaging scripts, `test/windows/` acceptance scripts.
- Modify: `cmd/devstack` startup, CLI help/docs.
- Modify: `docs/install-windows.md`, `docs/wsl.md`, `docs/windows-paths.md`, `docs/troubleshooting.md`.
- Modify: `.github/workflows/ci.yml` for manual Windows build/test dispatch.

---

## Wave 0 — Foundation (Sequential)

### Task T0.1: Repository platform hygiene and Windows compile baseline

**Files:**
- Create: `.gitattributes`
- Create: `Makefile` targets `build-windows`, `test-windows-cross`
- Modify: `.github/workflows/ci.yml` manual `workflow_dispatch` input `windows`
- Test: `test/release/workflow_test.sh` assertions for the manual Windows build

**Interfaces:**
- Produces: `GOOS=windows GOARCH=amd64` build targets for `./cmd/devstack` and `./cmd/devstack-helper`.

- [ ] **Step 1: Write the failing workflow assertion** in `test/release/workflow_test.sh`: assert the CI file contains `windows` dispatch input, `GOOS=windows`, and a named Windows cross-build step.
- [ ] **Step 2: Run it** `bash test/release/workflow_test.sh`; expect failure for the missing Windows entries.
- [ ] **Step 3: Add `.gitattributes` and the Makefile/workflow entries.**
- [ ] **Step 4: Run** `GOOS=windows GOARCH=amd64 go build ./cmd/devstack ./cmd/devstack-helper` and record expected compile failures caused by Unix-only files; add build-tag stubs only where needed for compilation, not production behavior.
- [ ] **Step 5: Run** `bash test/release/workflow_test.sh` and `go test ./...`; expect pass on Linux.
- [ ] **Step 6: Commit** `chore: add windows build baseline`.

### Task T0.2: IPC abstraction for Unix sockets and Windows named pipes

**Files:**
- Create: `internal/ipc/ipc.go`, `ipc_unix.go`, `ipc_windows.go`, `ipc_test.go`
- Modify: `internal/helperclient/client.go`, `internal/client/client.go`
- Add dependency: `github.com/Microsoft/go-winio` pinned

**Interfaces:**
- `func Listen(endpoint Endpoint) (net.Listener, error)`
- `func Dial(ctx context.Context, endpoint Endpoint, timeout time.Duration) (net.Conn, error)`
- `Endpoint{Network string, Address string}`; Windows network `"npipe"`, address `\\.\pipe\name`; Unix network `"unix"`.

- [ ] **Step 1: Write failing table tests** for `UnixEndpoint`, `WindowsEndpoint`, invalid empty endpoint, and path mapping.
- [ ] **Step 2: Run** `go test ./internal/ipc -run TestEndpoint -v`; expect undefined/fail.
- [ ] **Step 3: Implement endpoint parsing and Linux dial/listen.**
- [ ] **Step 4: Run tests** and `go test ./...`.
- [ ] **Step 5: Add Windows implementation and a Windows build-tagged test**; run `GOOS=windows go test -c ./internal/ipc` (compile only on Linux) and real test on Windows.
- [ ] **Step 6: Commit** `feat: add cross-platform IPC transport`.

### Task T0.3: Root, projects-root, and Windows path contract

**Files:**
- Modify: `internal/config/config.go`, `internal/config/paths.go`, `internal/config/config_test.go`
- Create: `internal/platform/platform.go`, `platform_linux.go`, `platform_windows.go`, `platform_test.go`

**Interfaces:**
- `DefaultRoot(goos string) string` returns `/data/devstack` for Linux and `C:\devstack` for Windows.
- `DefaultProjectsRoot(root, goos string) string`.
- `Paths.Root`, `Paths.ProjectsRoot`, plus existing subpaths.
- Config key `projects_root`; defaults derive from root.
- No devstack-created Windows basename may start with `.`.

- [ ] **Step 1: Write failing table tests** for Linux/Windows root, projects root, no-dot Windows names, and legacy `~/.devstack` detection.
- [ ] **Step 2: Run** `go test ./internal/config ./internal/platform`; expect fail.
- [ ] **Step 3: Implement defaults and config round-trip; preserve an existing legacy home without moving it.**
- [ ] **Step 4: Run tests** and update all config/paths callers; `go test ./...`.
- [ ] **Step 5: Commit** `feat: define cross-platform devstack roots`.

### Task T0.4: Site source mode model and migration

**Files:**
- Modify: `internal/model/model.go`
- Create: `internal/store/migrations/0007_site_source_mode.sql`
- Modify: `internal/store/store.go`, `internal/store/store_test.go`
- Create: `internal/pathmode/pathmode.go`, `pathmode_test.go`

**Interfaces:**
- `type SourceMode string`; constants `SourceLocal`, `SourceWindows`, `SourceWSL`.
- `Site.SourceMode SourceMode` JSON `source_mode`.
- Migration adds `source_mode TEXT NOT NULL DEFAULT 'local'`.
- `ValidateSourceMode(mode, platform string) error`: Linux only `local`; Windows only `windows`/`wsl`.
- `TranslateSource(mode, input string) (hostPath, backendPath string, err error)`.
  - windows: `C:\x` → `/mnt/c/x`
  - wsl: `\\wsl.localhost\devstack\home\x` → `/home/x`
  - local: absolute Linux path unchanged.

- [ ] **Step 1: Write failing model/store/pathmode tests** for all three modes, wrong-platform rejection, arbitrary UNC/device path rejection, `..` escape, and cross-boundary symlink paths.
- [ ] **Step 2: Run** `go test ./internal/model ./internal/store ./internal/pathmode`; expect fail.
- [ ] **Step 3: Implement model, migration, store scan/insert/update.**
- [ ] **Step 4: Implement path translation and validation.**
- [ ] **Step 5: Run** `go test ./...`; confirm migration upgrade from schema 0006.
- [ ] **Step 6: Commit** `feat: persist site source mode`.

### Task T0.5: Agent protocol, authenticated client, and token storage

**Files:**
- Create: `internal/agentproto/protocol.go`, `types.go`, `protocol_test.go`
- Create: `internal/agentclient/client.go`, `client_test.go`
- Create: `internal/securestore/securestore.go`, `securestore_windows.go`, `securestore_linux.go`, `securestore_test.go`

**Interfaces:**
- `PathHealth = "/v1/health"`, `PathVersion = "/v1/version"`, `PathRuntimeInstall`, `PathSitePrepare`, `PathSiteStart`, `PathSiteStop`, `PathSiteExec`, `PathLogs`, `PathService*`.
- `HealthResponse{Version, Distro, Root, Capabilities []string}`.
- `Client` methods for health/version and a version compatibility check; `ErrUnauthenticated`, `ErrVersionMismatch`.
- Agent requests carry `Authorization: Bearer <token>`; no query token.
- `securestore.Save(name, secret)` / `Load(name)` uses Windows DPAPI-backed storage and a 0600 file on Linux/WSL.

- [ ] **Step 1: Write failing httptest tests** for health, missing token, wrong version, JSON round-trip, and secure-store save/load/delete.
- [ ] **Step 2: Run** `go test ./internal/agentproto ./internal/agentclient ./internal/securestore`; expect fail.
- [ ] **Step 3: Implement types, client, and platform token storage.**
- [ ] **Step 4: Run tests and `go vet`.**
- [ ] **Step 5: Commit** `feat: add authenticated WSL agent protocol`.

### Task T0.6: WSL runner and path translation seam

**Files:**
- Create: `internal/wsl/runner.go`, `runner_test.go`, `path.go`, `path_test.go`

**Interfaces:**
- `type Runner interface { Run(ctx context.Context, args ...string) ([]byte, error) }`
- `type ExecRunner struct{}` using `wsl.exe`.
- `ParseDistroList([]byte) []string`.
- `DistroPath(windowsPath string) (string, error)`.

- [ ] **Step 1: Write failing fake-runner tests** for list/import/start/exec error propagation and Windows→WSL path mapping.
- [ ] **Step 2: Run** `go test ./internal/wsl`; expect fail.
- [ ] **Step 3: Implement runner seam and path translation.**
- [ ] **Step 4: Run tests with no real `wsl.exe`; assert fake is used.**
- [ ] **Step 5: Commit** `feat: add WSL runner seam`.

### Task T0.7: Daemon/CLI platform transport selection

**Files:**
- Modify: `internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`
- Modify: `internal/client/client.go`, `internal/client/client_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- `daemon.SocketPath(home)`/transport resolves to Unix socket or Windows named pipe through `internal/ipc`.
- No public API changes.

- [ ] **Step 1: Write failing tests** for Linux socket path and Windows named-pipe endpoint selection.
- [ ] **Step 2: Run** `go test ./internal/daemon ./internal/client`; expect fail.
- [ ] **Step 3: Route transport through `internal/ipc`.**
- [ ] **Step 4: Run** Linux tests and Windows cross-compile.
- [ ] **Step 5: Commit** `refactor: select daemon transport per platform`.

### Task T0.8: Platform capabilities and Windows service seams

**Files:**
- Create: `internal/platform/capabilities.go`, `capabilities_test.go`
- Create: `internal/platform/process.go`, `process_linux.go`, `process_windows.go`

**Interfaces:**
- `Capabilities{HasSystemd, HasDockerSocket, HasNamedPipe, HasWSL, SupportsHostTuning, SupportsConPTY}`
- Process command creation and hidden-window behavior; no production Windows process supervisor for native sites.

- [ ] **Step 1: Write failing capability table tests.**
- [ ] **Step 2: Run** `go test ./internal/platform`; expect fail.
- [ ] **Step 3: Implement Linux/Windows capability structs.**
- [ ] **Step 4: Run tests and cross-compile.**
- [ ] **Step 5: Commit** `feat: add platform capability seams`.

### Task T0.9: Freeze foundation and create lane worktrees

**Files:**
- Create: `docs/superpowers/status/2026-10-26-windows-baseline.md`
- Create: `.superpowers/sdd/<plan>/progress.md` using the SDD workspace script

- [ ] **Step 1: Run** `go test ./...`, `go vet ./...`, `gofmt -l .`.
- [ ] **Step 2: Record** base commit SHA, interfaces, and test evidence in the baseline status file.
- [ ] **Step 3: Create** integration branch and five lane worktrees from the frozen commit.
- [ ] **Step 4: Stop** and dispatch Wave 1 only after every lane can compile against the frozen interfaces.

---

## Wave 1 — Five Parallel Lanes

### Lane A — Windows helper and host integration

**Branch:** `lane/windows-helper`  
**Owner:** one implementer subagent.

#### Task A1: Named-pipe helper server and SID authentication

**Files:** `internal/helper/peer_windows.go`, `internal/helper/server_windows_test.go`, `cmd/devstack-helper/main_windows.go`, `command` build tags.

**Interfaces:**
- `PeerIdentity{UID int, SID string}`.
- Windows peer SID must match `helper.Config.AllowSID`; pipe ACL grants only that SID, SYSTEM, and Administrators.
- Linux behavior remains `AllowUID`/`SO_PEERCRED`.

- [ ] Write failing tests for allowed SID, foreign SID, empty SID, pipe ACL construction, and Linux peer regression.
- [ ] Run the Windows-tagged tests on Windows and the Linux suite on Linux; expect fail/compile fail.
- [ ] Implement named-pipe listener using `internal/ipc` and SID checks.
- [ ] Run focused tests, `go test ./...`, and cross-compile.
- [ ] Commit `feat: add windows helper named pipe`.

#### Task A2: Windows helper service lifecycle

**Files:** `internal/helper/service_windows.go`, `cmd/devstack-helper/service_windows.go`, `internal/helper/windows_service_test.go`.

- [ ] Write failing tests for service install/uninstall command construction, start/stop idempotency, and non-admin denial.
- [ ] Run tests; expect fail.
- [ ] Implement `golang.org/x/sys/windows/svc` handler and install/uninstall primitives behind interfaces.
- [ ] Run tests and a Windows service smoke on a real machine.
- [ ] Commit `feat: add windows helper service lifecycle`.

#### Task A3: Windows CA, trust stores, hosts, and firewall

**Files:** `internal/wintrust/`, `internal/winhosts/`, `internal/winfirewall/`, `internal/helper/windows_host_test.go`.

- [ ] Write failing tests for CA storage ACL, Windows root store install/remove, Firefox policy, hosts block preservation, and firewall rule create/delete with fakes.
- [ ] Run; expect fail.
- [ ] Implement adapters; keep CA key out of daemon-readable paths.
- [ ] Run tests and `go vet`.
- [ ] Commit `feat: add windows host integration`.

#### Task A4: Windows 80/443 forwarder and target synchronization

**Files:** `internal/forwarder/windows_target.go`, `internal/helper/proxy_targets.go`, `internal/helper/proxy_targets_windows_test.go`.

- [ ] Write failing tests for TCP forwarders to daemon ports, WSL nginx target IP/port updates, helper restart, and target persistence.
- [ ] Run; expect fail.
- [ ] Implement fixed listener/forwarder targets, validation, and Windows helper config persistence under `C:\devstack\system\helper.yaml`.
- [ ] Run tests and helper restart smoke.
- [ ] Commit `feat: add windows public forwarders` and report.

### Lane B — WSL provisioning and agent

**Branch:** `lane/wsl-agent`  
**Owner:** one implementer subagent.

#### Task B1: WSL distro provision/import lifecycle

**Files:** `internal/wsl/provision.go`, `provision_test.go`, `build/wsl/build-rootfs.sh`, `build/wsl/README.md`.

- [ ] Write failing fake-`wsl.exe` tests for existing-distro detection, import, start, systemd check, idempotent reuse, and unregister with confirmation.
- [ ] Run; expect fail.
- [ ] Implement provisioner with `C:\devstack\wsl\devstack`, Debian 12 rootfs, and no arbitrary commands.
- [ ] Run tests and a Windows fake integration.
- [ ] Commit `feat: provision managed WSL distro`.

#### Task B2: Agent server, auth, config, health

**Files:** `cmd/devstack-agent/main.go`, `internal/agent/server.go`, `auth.go`, `config.go`, `health.go`, tests.

- [ ] Write failing httptest tests for bearer auth, NAT bind validation, health, version, and config permissions.
- [ ] Run; expect fail.
- [ ] Implement agent with `/data/devstack/agent.token` and NAT-only listen.
- [ ] Run tests and `go vet`.
- [ ] Commit `feat: add WSL agent server`.

#### Task B3: Agent static backend and process/runtime foundation

**Files:** `internal/agent/runtime.go`, `static.go`, `process.go`, tests.

- [ ] Write failing tests for static site prepare/start/stop, document-root validation, port allocation, and log streaming.
- [ ] Run; expect fail.
- [ ] Reuse Linux staticserve/supervisor where possible; keep the agent API as the boundary.
- [ ] Run tests and a Debian WSL smoke.
- [ ] Commit `feat: add agent static backend`.

#### Task B4: Dedicated Docker Engine and agent readiness

**Files:** `build/wsl/agent.service`, `build/wsl/docker-daemon.json`, `internal/agent/docker.go`, tests.

- [ ] Write failing tests for Docker Engine health, data-root `/data/devstack/docker`, Compose presence, and startup failure mapping.
- [ ] Run; expect fail.
- [ ] Implement Docker bootstrap/health and systemd unit dependencies.
- [ ] Run tests and a real WSL Docker `hello-world`/Compose smoke.
- [ ] Commit `feat: add dedicated docker engine to WSL agent`.

### Lane C — Windows daemon and public proxy

**Branch:** `lane/windows-daemon`  
**Owner:** one implementer subagent.

#### Task C1: Windows daemon startup, config, SQLite, and CLI transport

**Files:** `internal/daemon/windows.go`, `windows_test.go`, `internal/client/transport_windows.go`, `internal/cli/root.go`.

- [ ] Write failing tests for `C:\devstack` defaults, no-dot names, SQLite on NTFS, named-pipe CLI, log path, and per-user startup.
- [ ] Run; expect fail.
- [ ] Implement platform startup; preserve Linux behavior.
- [ ] Run tests and Windows compile.
- [ ] Commit `feat: add windows daemon startup`.

#### Task C2: Builtin proxy and public-port target updates

**Files:** `internal/proxy/windows_listen.go`, `internal/daemon/daemon.go`, `internal/daemon/daemon_windows_test.go`.

- [ ] Write failing tests for 7070/8080/8443 listeners, helper target update before reconcile, and clean shutdown.
- [ ] Run; expect fail.
- [ ] Implement named-pipe helper target updates and proxy lifecycle.
- [ ] Run tests and Windows smoke.
- [ ] Commit `feat: add windows public proxy lifecycle`.

#### Task C3: WSL backend client

**Files:** `internal/backend/wsl/backend.go`, `backend_test.go`, `internal/agentclient/client_test.go`.

- [ ] Write failing tests for create/start/stop/status/logs/exec through the agent, path mapping, timeout, and auth failure.
- [ ] Run; expect fail.
- [ ] Implement the `backend.Backend` adapter over `agentclient`.
- [ ] Run tests and fake-agent integration.
- [ ] Commit `feat: add WSL backend client`.

#### Task C4: Windows daemon/helper/agent bootstrap

**Files:** `internal/daemon/windows_bootstrap.go`, tests.

- [ ] Write failing tests for startup order (helper → WSL agent → proxy → reconcile), token loading, and recovery after agent/helper restart.
- [ ] Run; expect fail.
- [ ] Implement bootstrap and diagnostics.
- [ ] Run tests and a Windows vertical smoke.
- [ ] Commit `feat: bootstrap windows daemon services`.

### Lane D — Docker services and web-serving adapter

**Branch:** `lane/docker-services`  
**Owner:** one implementer subagent.

#### Task D1: Agent Docker control API

**Files:** `internal/agentproto/types.go`, `internal/agent/docker.go`, `docker_test.go`.

- [ ] Write failing tests for fixed service IDs, Compose project name, image digest pinning, lifecycle actions, and arbitrary-command rejection.
- [ ] Run; expect fail.
- [ ] Implement agent-only Docker API and Compose execution.
- [ ] Run tests with fake Docker.
- [ ] Commit `feat: add agent docker control`.

#### Task D2: Servicecontrol adapter on Windows

**Files:** `internal/servicecontrol/agent_docker.go`, `agent_docker_test.go`.

- [ ] Write failing tests for service install/activate/deactivate/logs/credentials through the agent, path ownership, and Linux regressions.
- [ ] Run; expect fail.
- [ ] Implement adapter behind existing interfaces.
- [ ] Run focused and full tests.
- [ ] Commit `feat: add windows service control adapter`.

#### Task D3: Web-serving modes, vhosts, and TLS copies

**Files:** `internal/agent/web_targets.go`, `internal/vhost/agent_adapter.go`, `web_targets_test.go`.

- [ ] Write failing tests for nginx/Apache private ports, generated vhosts under `/data/devstack/vhosts`, leaf certificate copies, and public-helper target updates.
- [ ] Run; expect fail.
- [ ] Implement vhost/TLS sync and target updates.
- [ ] Run tests and a WSL nginx smoke.
- [ ] Commit `feat: add WSL docker web serving`.

#### Task D4: Service config/data/secrets permissions

**Files:** `internal/servicecontrol/agent_paths.go`, tests.

- [ ] Write failing tests for ext4 data paths, no NTFS data, config read/write, 0700 secrets, and service deletion backup.
- [ ] Run; expect fail.
- [ ] Implement path/permission rules.
- [ ] Run tests and full service smoke.
- [ ] Commit `feat: enforce WSL service data rules`.

### Lane E — Source modes, path UX, CLI/UI

**Branch:** `lane/site-source`  
**Owner:** one implementer subagent.

#### Task E1: Site source mode through service/API/CLI

**Files:** `internal/sites/source_mode.go`, `internal/sites/service.go`, `internal/api/handlers_sites.go`, `internal/cli/site.go`, tests.

- [ ] Write failing tests for Windows/WSL source defaults, path translation, move-mode rejection/success, and Linux `local`.
- [ ] Run; expect fail.
- [ ] Implement source-mode service/API/CLI behavior.
- [ ] Run tests.
- [ ] Commit `feat: expose site source modes`.

#### Task E2: Path validation and PhpStorm/WSL mapping

**Files:** `internal/pathmode/`, docs, tests.

- [ ] Write failing tests for `C:\`, `\\wsl.localhost`, UNC rejection, dot-name policy, symlink escape, and Windows→WSL mapping.
- [ ] Run; expect fail.
- [ ] Implement/complete path policy and docs.
- [ ] Run tests.
- [ ] Commit `feat: enforce windows/wsl path policy`.

#### Task E3: Dashboard project-source selector

**Files:** `ui/src/lib/components/ProjectSourceMode.svelte`, `AddSite*.svelte`, `ui/tests/e2e.spec.ts`.

- [ ] Write failing Vitest/Playwright tests for the selector, WSL default for PHP/Node, Windows default for static, warnings, and no dot path display.
- [ ] Run; expect fail.
- [ ] Implement the selector and warnings.
- [ ] Run `npm run check`, unit, Playwright.
- [ ] Commit `feat: add dashboard project source selector`.

#### Task E4: Windows/WSL doctor and settings

**Files:** `internal/doctor/checks.go`, `internal/api`, `ui` Settings, tests.

- [ ] Write failing tests for WSL/agent/helper/Docker checks, Windows port status, and remediation.
- [ ] Run; expect fail.
- [ ] Implement checks and UI.
- [ ] Run tests.
- [ ] Commit `feat: add windows/wsl diagnostics`.

---

## Integration 1 — Vertical Slice

### Task I1: Merge Wave 1 lanes and resolve shared interfaces

**Files:** merge commits only; controller-owned.

- [ ] Create integration branch from frozen foundation.
- [ ] Merge A, B, C, D, E in dependency order; run focused tests after each.
- [ ] Run `go test ./...`, `go vet ./...`, and UI checks.
- [ ] Commit merge commits; record SHAs in ledger.

### Task I2: Windows static-site vertical slice

**Files:** `test/windows/vertical_slice.ps1`, temporary test fixtures.

- [ ] Write an acceptance script that starts helper/daemon/agent, creates one Windows-mode and one WSL-mode static site, checks Windows hosts/CA trust, and fetches trusted `.test` HTTPS pages.
- [ ] Run on a fresh Windows 11 + WSL2 machine.
- [ ] Fix only integration defects; commit `test: add windows vertical slice`.
- [ ] **Gate:** If either path cannot serve trusted `.test` HTTPS, stop and do not start parity wave.

---

## Wave 2 — Five Parallel Parity Lanes

Before dispatching Wave 2, the controller expands each lane into the same task-brief format used by Wave 1: exact files, interfaces, RED tests, commands, and commits. The scopes below are binding, but no Wave 2 lane starts from a prose-only brief.

### Lane F — PHP/Node/FPM and terminal parity

- [ ] Agent PHP runtime install/select/validate/swap and per-site FPM pool; real Linux-only extension smoke (`swoole` or the approved extension set).
- [ ] Node runtime and static build parity.
- [ ] Site terminal/PTY through agent WebSocket; path mapping and Xdebug docs.
- [ ] Windows-mode PHP/Node performance warning and benchmark fixture.
- [ ] TDD tests: runtime missing, FPM invalid, extension isolation, terminal ticket/auth, process teardown.
- [ ] Commit `feat: add WSL PHP and Node parity`.

### Lane G — Installers, trust, upgrade, uninstall

- [ ] `install.ps1` with elevation, fixed `C:\devstack`, protected `system` ACL, helper service, WSL import, per-user startup.
- [ ] Update `install.sh` for new `/data/devstack` roots and helper `system` layout on a fresh Linux install.
- [ ] Detect legacy Linux `~/.devstack`, `/etc/devstack/helper.yaml`, and `/var/lib/devstack-helper`; preserve them without silent migration.
- [ ] Upgrade preserves state/projects/system; WSL distro export/import; rollback.
- [ ] Trust repair/hosts repair/firewall remove.
- [ ] Uninstall with project preservation and optional VHDX removal.
- [ ] TDD tests with fake PowerShell/sudo/service runners and `test/release/installer_test.sh`.
- [ ] Commit `feat: add cross-platform installer lifecycle`.

### Lane H — Docker service parity

- [ ] Full Compose render, nginx/Apache, databases, Redis, phpMyAdmin, credentials, rotation, logs, backup/delete.
- [ ] Verify service data ext4 and Docker root `/data/devstack/docker`.
- [ ] Helper target updates for nginx private ports.
- [ ] TDD with fake Docker and one real WSL Docker smoke.
- [ ] Commit `feat: complete WSL docker services`.

### Lane I — Performance, benchmarks, config, ports UI

- [ ] Windows resource collection, no host tuning, PHP FPM pool, service performance.
- [ ] Seed benchmark execution from Windows through public route.
- [ ] Config editor and listener-port changes on Windows.
- [ ] Dashboard platform status/source modes/settings.
- [ ] TDD unit + Playwright.
- [ ] Commit `feat: add windows performance and config parity`.

### Lane J — Acceptance, release, docs

- [ ] Windows release archive: daemon, helper, agent, rootfs, `install.ps1`, checksums; no Docker PHP.
- [ ] Manual-dispatch Windows workflow.
- [ ] Fresh Windows acceptance matrix from the spec.
- [ ] `docs/install-windows.md`, `wsl.md`, `windows-paths.md`, PhpStorm, troubleshooting, uninstall.
- [ ] Linux full regression gate.
- [ ] Commit `docs: add windows acceptance and release`.

---

## Integration 2 — Final Merge and Gate

### Task M1: Merge Wave 2

- [ ] Merge F→G→H→I→J; run focused tests after each.
- [ ] Resolve owner conflicts in the controller only.
- [ ] Run `go test ./...`, `go test -race ./...`, `go vet ./...`, UI check/unit/Playwright/build.
- [ ] Run Linux PHP acceptance, Linux E2E, release/install/workflow tests, Docker smokes.
- [ ] Run Windows fresh-install acceptance and reboot/WSL recovery.
- [ ] Commit merge.

### Task M2: Final independent review

- [ ] Generate whole-branch diff package.
- [ ] Dispatch fresh final reviewer with spec, plan, Review Focus, and ledger rulings.
- [ ] Fix Critical/Important with RED→GREEN tests; ledger Minors.
- [ ] Re-run full gate.
- [ ] Update handoff/known issues; do not claim Windows support until acceptance evidence exists.

### Task M3: Finish and pause

- [ ] Record final SHAs, test evidence, and remaining gates.
- [ ] Delete lane worktrees and mark plan complete.
- [ ] Do not implement beyond this plan without human approval.

---

## Acceptance Matrix

| Check | Required evidence |
|---|---|
| Fresh Windows install | No manual WSL setup; helper/daemon/agent healthy |
| Trusted `.test` | Edge/Chrome and Firefox trusted for both source modes |
| Windows project | `C:\devstack\projects\...` static/PHP/Node flows |
| WSL project | `\\wsl.localhost\devstack\...` static/PHP/Node flows |
| PHP | Linux FPM, extensions, pool sizing, Xdebug path mapping |
| Docker | nginx/Apache, MySQL/MariaDB/PostgreSQL/Redis/phpMyAdmin, rotation, logs |
| Ports | 80/443 follow daemon/WSL target changes and helper restart |
| Recovery | daemon, helper, WSL, Windows reboot |
| Upgrade/uninstall | State/projects preserved; trust/hosts/firewall cleaned |
| Linux regression | Existing full gate, E2E, PHP acceptance, Docker smokes |

## Pause Point

Planning ends after this document and the design spec are reviewed. No production code, worktree lane, or subagent implementation starts until the human partner approves the plan.
