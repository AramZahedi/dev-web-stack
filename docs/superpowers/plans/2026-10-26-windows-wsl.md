# Windows 10/11 + WSL2 Implementation Plan

> **Status:** Implemented across Waves 0–2 and released in v0.2.0; remediated on 2026-10-28 per [Audit Remediation Plan](2026-10-28-audit-remediation.md).
>
> **For agentic workers:** use the available OpenCode subagent/test-driven
> workflow. If a named `superpowers:*` skill is unavailable in the environment,
> follow the equivalent process written in this document rather than stopping.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class Windows 10 and Windows 11 development platform with a native
Windows daemon/helper, a managed WSL2 Linux agent for PHP/Node/static, and
Docker service containers, while preserving the existing Linux product.

**Architecture:** The Windows daemon owns the public proxy, UI/API, SQLite,
routes, and orchestration. A LocalSystem helper owns Windows trust, hosts,
firewall, CA, and 80/443. A managed Debian 12 WSL2 agent owns Linux
PHP-FPM/Node/static, ports, logs, the WSL `.test` ingress, and Docker
services. The Windows daemon terminates public TLS and routes Docker web
traffic to a private HTTP port in WSL. Docker PHP and native Windows PHP are
excluded.

**Tech Stack:** Go, Svelte dashboard, SQLite, Windows named pipes, LocalSystem
service, WSL2, Debian 12, systemd, PHP-FPM, Node, Docker Engine/Compose,
PowerShell installer.

**Spec:** `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`

**Review resolution record:**
`docs/superpowers/status/2026-10-27-windows-plan-review-resolution.md`

## Global Constraints

- Linux and WSL application root: `/data/devstack`; Windows root:
  `C:\devstack`.
- Default projects root: `/data/devstack/projects` on Linux/WSL,
  `C:\devstack\projects` on Windows.
- No devstack-created Windows file or directory starts with a dot. Project-owned
  `.git`, `.idea`, `.env`, and `.htaccess` are unaffected.
- Site source modes: `local` (Linux), `windows`, `wsl`; default PHP/Node to
  `wsl`, static to `windows` on Windows.
- Windows-mode project paths map `C:\...` ↔ `/mnt/c/...`.
- WSL-mode project paths map `\\wsl.localhost\<distro>\...` ↔ `/...`.
- Reject arbitrary UNC/device paths, alternate data streams, junctions, and
  cross-boundary symlinks.
- **Windows public entry:** the helper always forwards Windows 80/443 to the
  Windows daemon proxy on 8080/8443. It never forwards to WSL.
- **Windows daemon proxy:** public HTTP/TLS router and dashboard. It binds
  loopback plus the WSL host-adapter address; the firewall restricts the
  WSL-facing listener to the WSL subnet.
- **WSL ingress:** the agent listens on WSL 80/443 and forwards raw TCP to the
  Windows host gateway on 8080/8443. WSL `.test` hosts entries map to
  `127.0.0.1`.
- **Windows→WSL backends:** WSL localhost forwarding is required. Site
  backends bind WSL `127.0.0.1`; the daemon reaches them at
  `127.0.0.1:<port>`. The agent API also listens on the distro IP for control
  fallback only.
- **All Windows site backends run in WSL.** The Windows daemon does not run a
  local site supervisor. Windows-mode static source is read through `/mnt/c`.
- **Port ownership:** the agent allocates and validates WSL HTTP/FastCGI and
  private web-server ports; the daemon persists the returned values. Linux
  keeps the daemon-side allocator.
- **Canonical storage:** WSL ext4 is canonical for generated site configs,
  service config/data/secrets, runtimes, site/service logs, and Docker data.
  Windows keeps `state.db`, control-plane config, leaf certs, daemon logs,
  cache, and the VHDX. The daemon proxies content/credential/log operations
  to the agent.
- **Agent transport:** HTTPS with a pinned self-signed certificate plus a
  bearer token. Token and fingerprint are DPAPI-protected on Windows; agent
  copy is 0600/0700 in WSL.
- **Named pipes:** helper and daemon control pipes are ACL-restricted and
  peer-SID-checked. No other local user may call them.
- **Windows helper surface:** CA, trust, hosts, firewall, forwarder targets,
  health/version only. No service/tool/vhost/performance RPCs.
- **Tooling:** global `php`/`composer`, `devstack shell`/`exec`, and site
  builds run in WSL. Windows gets `.cmd` shims.
- CA private key: `C:\devstack\system\pki` or `/data/devstack/system/pki`,
  never WSL/Docker.
- Docker is only for nginx/Apache, MySQL/MariaDB, PostgreSQL, Redis,
  phpMyAdmin. No Docker PHP. PHP runs as native Linux PHP-FPM in WSL/Linux.
- Docker service data lives under `/data/devstack/services/<id>/data`;
  never an NTFS bind mount.
- Native Linux Docker engine data remains `/var/lib/docker`; dedicated WSL
  Docker data-root is `/data/devstack/docker`.
- Windows Docker web servers listen on a private HTTP port (default 18080);
  the Windows daemon terminates public TLS and proxies to it.
- Existing Linux installs and helper paths are detected and preserved; no
  automatic layout migration. New Linux installs use `/data/devstack`.
- Windows helper owns Windows 80/443; Linux helper owns Linux 80/443.
- WSL baseline: Windows 11 22H2+ x64, WSL 2.0.0+ with systemd. Installer
  validates but never enables Windows features or reboots.
- Managed WSL distro marker: `/data/devstack/managed.json`; never adopt or
  replace an unmarked distro.
- WSL-mode projects are never deleted by default uninstall; removal requires
  export or explicit discard confirmation.
- Windows loopback access to WSL services uses the agent service relay plus
  helper loopback forwarding.
- WSL PHP catalog artifacts must pass the Debian 12 compatibility gate before
  use.
- **No implementation is merged without a watched RED test and a green
  suite.**
- **At most 5 active subagents at any time.** Implementers and reviewers
  count toward the cap.
- **Do not claim Windows support** until the Windows acceptance matrix has
  evidence and the Linux regression gate is green.

## Resolved contracts (binding)

These are the contracts that were missing or contradictory in the first plan.

### C1 — Network contract

```text
Windows browser
  -> Windows helper 80/443
  -> Windows daemon proxy 8080/8443
  -> builtin WSL backend 127.0.0.1:<agent-port>
     or active Docker web server 127.0.0.1:<private-port>
     or dashboard 127.0.0.1:<ui-port>

WSL client
  -> agent ingress 127.0.0.1:80/443
  -> Windows host gateway:8080/8443
  -> Windows daemon proxy
  -> same routes as above
```

- The daemon proxy's WSL-facing listener is firewall-restricted to the WSL
  subnet.
- The agent ingress target is fixed and discovered from the WSL default
  route; it is never caller-supplied.
- No helper forwarder targets WSL. The existing loopback-only forwarder
  invariant remains.
- The dashboard/API control listener stays on Windows loopback; WSL reaches
  the dashboard through the public proxy's `devstack.test` route.

### C2 — Backend and port contract

- The Windows daemon uses one `backend.Backend` implementation: the WSL agent
  backend. It also implements `PHPStager`.
- `Prepare` returns `PreparedSite{BackendHost, HTTPPort, FastCGIPort}`.
  `BackendHost` defaults to `127.0.0.1` (localhost forwarding).
- The agent allocates and binds WSL ports. The daemon persists them on the
  site row and uses them for routes.
- On agent restart, the daemon sends the stored ports; the agent reuses them
  if free, otherwise allocates new ports and returns the new values. The
  daemon updates the site row and routes.
- Linux continues to use the local backend and daemon-side allocator.
- `sites.Service` gains a port-provider seam so it no longer assumes the
  Windows host allocator owns backend ports.

### C3 — PHP path contract

- `host_path`: Windows-visible project path (`C:\...` or
  `\\wsl.localhost\<distro>\...`).
- `backend_path`: WSL path passed to FPM/Node.
- `proxy_document_root`: Windows-visible absolute document root derived from
  `host_path` + relative `document_root`, used by the Go proxy for PHP
  static-file/front-controller checks.
- The agent validates `backend_path` and the document root; the daemon uses
  `proxy_document_root` for file existence checks. Both are containment
  checked and tested.

### C4 — Storage contract

- WSL ext4 owns generated site configs, service config/data/secrets,
  runtimes, site/service logs, and Docker data.
- Windows `state.db` owns control-plane metadata.
- The daemon exposes service config/content/diff/apply/reset, credentials,
  service logs, and web-server config operations by proxying to the agent.
- No canonical `sites\`, `services\`, `secrets\`, `certs\`, or `tools\` tree
  on Windows.

### C5 — Agent protocol contract

T0.5 freezes the complete protocol, not just health. It includes:

- `/v1/health`, `/v1/version`, `/v1/capabilities`
- `/v1/ports` (allocate/release/validate)
- `/v1/sites/{id}/prepare|start|stop|status|logs|exec`
- `/v1/sites/{id}/php/prepare|start|stop`
- `/v1/sites/{id}/build`
- `/v1/sites/{id}/terminal` (WebSocket, ticket-authenticated)
- `/v1/runtimes` install/remove/list
- `/v1/services/...` lifecycle, logs, credentials, config
- `/v1/web/targets` and `/v1/web/sync`
- `/v1/hosts`, `/v1/certs`, `/v1/ca`
- `/v1/paths/validate` and `/v1/source/copy`

Every endpoint has JSON request/response types, error codes, protocol
versioning, auth, limits, and timeout behavior. T0.5 is the only owner of
`internal/agentproto`; lanes may not redefine it.

### C6 — Source-mode copy contract

- WSL→Windows and Windows→WSL source moves are agent-coordinated tar
  transfers with a SHA-256 manifest, file-count check, Linux ownership/mode
  verification, and symlink/junction rejection.
- The destination is absent/empty. Copy to a temporary sibling; switch the
  row only after verification. Source is kept unless `--remove-source`.
- Windows never treats UNC permission bits as the authorization boundary.

## Review Focus

- A Windows path or WSL UNC path is accepted where the opposite mode is
  required.
- A cross-boundary symlink/junction escapes the project root or bypasses
  source-mode validation.
- SQLite or the CA key is written through `/mnt/c` or `\\wsl.localhost`.
- Docker database/Redis data ends up on NTFS.
- A Windows helper pipe or daemon control pipe is reachable by a SID other
  than the installing user/SYSTEM/Administrators.
- WSL agent HTTP/TLS requests succeed without the bearer token or certificate
  pin, or from a non-NAT/loopback interface.
- `.test` names fail from inside WSL in either builtin or Docker mode.
- The Docker dashboard vhost targets `127.0.0.1` inside WSL instead of the
  Windows daemon proxy.
- A WSL backend port is allocated by the Windows daemon without agent
  validation.
- A Windows-mode static site is served without the `/mnt/c` warning.
- The Windows daemon runs a local site supervisor or a Docker PHP container.
- An existing Linux `~/.devstack` or legacy helper install is moved or
  rewritten silently.
- A devstack-created Windows basename starts with a dot.
- Existing Linux paths, tests, E2E, and Docker service behavior regress.

---

## Parallel Execution Protocol

The controller is the main OpenCode session. It dispatches implementers and
reviewers with the OpenCode `subagent` tool, `agent: "general"`. The controller
alone merges branches, pushes, and runs the final gate. Implementation is
expected on a Windows 10 22H2 or Windows 11 host with WSL2 and Docker; Linux regressions run in
WSL ext4 or a Linux checkout using the same worktree layout.

### Environment prerequisites (do these before T0.1)

- `git config --global core.autocrlf false` and re-checkout/renormalize the
  working tree. The current checkout was made with `core.autocrlf=true`; shell
  scripts contain CRLF and `bash -n` fails.
- A working `bash` (Git Bash or WSL) and `make`, or the equivalent
  PowerShell build commands. `make` is not installed by default on Windows.
- Node 22 for the UI/CI baseline. Node 24 may work, but it is not the
  documented baseline.
- Go 1.26+ and network access to the module proxy. If `proxy.golang.org`
  returns 403, fix the module cache/proxy before claiming a baseline.
- WSL 2.0.0+ with systemd; use a separate development distro, not the managed
  `devstack` name.
- Docker Engine inside WSL for service acceptance. Docker Desktop is not the
  supported substrate.
- Administrator access for the serialized helper/service/ACL/80/443 tests.

### Worktrees and branches

1. Controller creates the integration branch and one worktree per lane:
   - integration: `feature/windows-wsl` at the foundation commit.
   - `lane/windows-helper`
   - `lane/wsl-agent`
   - `lane/windows-daemon`
   - `lane/docker-services`
   - `lane/site-source`
2. Every lane branch starts from the same frozen foundation commit. Lanes do
   not merge each other.
3. Each lane has one implementer subagent. The implementer owns its
   branch/worktree and may make multiple TDD commits.
4. Maximum **5 active subagents**, including implementers and reviewers. Do
   not dispatch a sixth until one finishes.
5. Implementer subagents must not spawn subagents. Reviewers are dispatched by
   the controller only.
6. Lane implementers may not edit files owned by another lane. Changes to
   foundations after freeze go through the controller.
7. Implementers return: status, commit range, test commands/results,
   concerns, and a lane report path. Full report goes to a file; the return
   message stays short.

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

### Lane dependency graph

All five lanes start concurrently from the frozen foundation. The graph below
describes **compile-time/merge dependencies**, not a serialized start order:

```text
Wave 0 (sequential)
  T0.1 toolchain
  T0.2 IPC/pipe security
  T0.3 roots/layout/dot policy
  T0.4 source mode/path contract
  T0.5 full agent protocol/TLS + agentapi module seam
  T0.6 WSL path/copy seam
  T0.7 daemon transport/lifecycle
  T0.8 platform seams
  T0.9 backend/port/route contract
  T0.10 freeze

Wave 1 — all five implementers start from the frozen foundation
  Lane A (helper)        independent after T0.2/T0.3
  Lane B (agent)         owns the agent server; uses agentapi modules
  Lane C (daemon)        uses agentclient/agentproto + fake agent; no wait on B
  Lane D (Docker/web)    owns separate agentdocker/agentrelay/agentweb packages
                         implementing agentapi.Module; no edits to B's server
  Lane E (source/UI)     depends on T0.4/T0.6; fully independent of B/C/D
```

Merge order is **A → B → C → D → E** because the integrated agent server must
exist before the modules are mounted. D can implement and test its modules
concurrently against `agentapi.Module`; C can implement and test against the
fake agent. No lane may edit another lane’s package. The controller mounts the
D modules during merge/integration.

If a lane cannot compile against the frozen `agentapi`/`agentproto` contracts,
the controller fixes the contract in the foundation and re-freezes; it does not
serialize the other four lanes.

### Implementer dispatch template

For each lane, the controller dispatches one `general` subagent with a prompt
containing:

- lane name, branch, worktree path, and frozen base SHA;
- spec path and the exact task section; never the whole plan or session
  history;
- the requirement to use TDD for every production change;
- the instruction not to spawn subagents;
- the requirement to commit each task, run the full relevant suite, and write
  a full lane report to a file;
- the allowed file ownership list and the prohibition on editing other lanes;
- the report contract: status, commit range, tests with results, concerns,
  unresolved interfaces.

The controller records the base commit before dispatch, verifies the worktree
is isolated, and does not edit the lane while its implementer is active.

### Reviewer dispatch template

After a lane report arrives, the controller:

1. Creates a diff package from the lane base to lane head without loading it
   into the controller context.
2. Dispatches one fresh reviewer subagent with the diff package, spec, task
   section, Global Constraints, and Review Focus.
3. Requires two verdicts: spec compliance and code quality, with
   severity-ranked findings.
4. Sends Critical/Important findings back to the same lane implementer for a
   RED→GREEN fix; re-reviews only the scoped fix.
5. Merges only after both verdicts are clean.

Reviewers never edit code and never review while more than five total
subagents are active.

### Test isolation and machine-wide resources

- Lane implementers run unit tests, compile checks, and fakes concurrently.
- Real Windows resources are controller-serialized: one helper service/pipe,
  one production `C:\devstack`, one managed `devstack` distro, one owner of
  80/443, one hosts/trust mutation, one Docker engine, and one real
  WSL-ingress/firewall test at a time.
- Lane-local smokes may use unique `C:\devstack-test\<lane>`, named-pipe
  suffix, `devstack-test-<lane>` distro, high ports, and no
  hosts/trust/firewall writes.
- Windows-tagged tests require the Windows host; Linux regression tests run in
  WSL ext4 or a Linux checkout on the same frozen code.
- Five implementers can edit concurrently, but they cannot all install the
  production Windows service or run real port 80/443 tests. The controller
  reserves those for integration and records the evidence.

### Reviews and merging

- After each wave, the controller creates a diff package for each lane and
  dispatches one fresh reviewer per lane, max 5 concurrently.
- Review has two verdicts: spec compliance and code quality.
  Critical/Important findings go back to that lane's implementer; the
  controller merges only clean lanes.
- Merge order is dependency order, not completion order. After each merge, run
  the lane's focused tests plus `go test ./...`.
- Integration conflicts are resolved by the controller; shared foundation
  files are never edited by two lanes.
- Final whole-branch review uses a fresh reviewer. A single fix pass follows
  for Critical/Important findings, each with a RED→GREEN test, then the full
  gate.
- Use the available worktree/subagent/TDD workflow. If a named
  `superpowers:*` skill is unavailable, use the equivalent process written
  here.

---

## File Structure Map

### Foundation files

- Create: `.gitattributes` — LF for shell, Go, SQL, YAML, Makefile, WSL
  scripts; no automatic CRLF conversion for Linux artifacts.
- Create: `internal/ipc/ipc.go`, `ipc_unix.go`, `ipc_windows.go` — Unix
  socket/named-pipe endpoint with ACL/SID helpers.
- Create: `internal/agentproto/protocol.go`, `types.go`, `errors.go`,
  `version.go` — complete authenticated agent API contract.
- Create: `internal/agentapi/api.go` — module interface seam for Lane D
  (`Name`, `MountPath`, `Handler`).
- Create: `internal/agentclient/client.go` — HTTPS pinned-certificate,
  bearer-token client with typed methods for every endpoint.
- Create: `internal/securestore/` — DPAPI/0600 token and pin storage.
- Create: `internal/wsl/runner.go`, `path.go`, `copy.go`, `validate.go` —
  `wsl.exe` seam, path translation, tar copy/validate.
- Modify: `internal/config/config.go`, `internal/config/paths.go` — roots,
  projects root, legacy detection, Windows/platform defaults.
- Modify: `internal/model/model.go` — source mode and path fields.
- Create: `internal/store/migrations/0007_site_source_mode.sql`.
- Modify: `internal/store/store.go`, `internal/store/store_test.go`.
- Create: `internal/platform/platform.go`, `platform_windows.go`,
  `platform_linux.go` — OS capabilities and process/lock/terminal seams.
- Modify: `cmd/devstack/main.go`, `cmd/devstack-helper/main.go` only for
  cross-platform compile seams.
- Modify: `internal/sites/service.go`, `internal/sites/routes.go`,
  `internal/backend/backend.go` — backend/port/route/provider contracts.
- Create: `internal/backend/wsl/backend.go` — WSL agent backend.
- Create: `internal/ports/ports.go` — port-provider seam.
- Create: `internal/daemon/windows.go`, `lifecycle_windows.go`, `pipe_windows.go`
- Create: `internal/helper/peer_windows.go`, `service_windows.go`,
  `config_windows.go`, `winserver.go` — Windows helper.
- Create: `internal/winhosts/`, `internal/wintrust/`, `internal/winfirewall/`.
- Create: `internal/agent/server.go`, `auth.go`, `config.go`, `health.go`,
  `hosts.go`, `ingress.go`, `docker_engine.go`, `runtime.go`, `static.go`,
  `process.go`; `internal/agent/tls.go` is created in T0.5.
- Create: `internal/wsl/provision.go`, `distro.go`, `rootfs.go`,
  `localhost_forwarding.go`.
- Create: `build/wsl/README.md`, `build/wsl/build-rootfs.sh`,
  `build/wsl/versions.lock`, `build/wsl/agent.service`,
  `build/wsl/docker-daemon.json`.
- Create: `install.ps1`, `build/windows/` packaging scripts,
  `test/windows/` acceptance scripts.
- Create: `internal/winhosts/*_test.go`, `internal/wintrust/*_test.go`,
  `internal/winfirewall/*_test.go`, `internal/agent/*_test.go`,
  `internal/wsl/*_test.go`, `internal/backend/wsl/*_test.go`.

### Lane A — Windows helper

- Create/modify: `internal/helper/peer_windows.go`, `peer_linux.go`
- Create: `internal/helper/service_windows.go`, `config_windows.go`,
  `winserver.go`
- Create: `internal/winhosts/`, `internal/wintrust/`, `internal/winfirewall/`
- Modify: `internal/helper/proxy_targets.go`, `internal/helper/server.go` only
  through platform interfaces
- Modify: `cmd/devstack-helper/main.go`, `cmd/devstack-helper/config.go`
- Create: `internal/helper/windows_config_test.go`,
  `internal/helper/windows_service_test.go`, `internal/winhosts/*_test.go`,
  `internal/wintrust/*_test.go`

### Lane B — WSL provisioning, agent, ingress

- Create: `cmd/devstack-agent/main.go`
- Create: `internal/agent/server.go`, `auth.go`, `config.go`, `health.go`,
  `tls.go`, `hosts.go`, `ingress.go`
- Create: `internal/wsl/provision.go`, `distro.go`, `rootfs.go`,
  `localhost_forwarding.go`
- Create: `build/wsl/README.md`, `build/wsl/build-rootfs.sh`,
  `build/wsl/versions.lock`, `build/wsl/agent.service`,
  `build/wsl/docker-daemon.json`
- Create: `internal/agent/*_test.go`, `internal/wsl/*_test.go`
- Create: `test/wsl/fake_wsl_test.go`

### Lane C — Windows daemon, public proxy, WSL backend

- Create: `internal/daemon/windows.go`, `windows_test.go`,
  `lifecycle_windows.go`, `pipe_windows.go`
- Create: `internal/client/transport_windows.go`, `transport_unix.go`
- Create: `internal/proxy/windows_listen.go`
- Create: `internal/backend/wsl/backend.go`, `backend_test.go`
- Create: `internal/agentclient/client_test.go`
- Modify: `internal/daemon/daemon.go` for platform wiring only
- Modify: `internal/config/config.go` for Windows defaults
- Modify: `internal/daemon/daemon_test.go`

### Lane D — Docker services and private web adapter

- Create: `internal/agentdocker/`, `internal/agentrelay/`,
  `internal/agentweb/` — Lane D-owned modules implementing `agentapi.Module`
- Create: `internal/servicecontrol/agent_docker.go`,
  `agent_docker_test.go`
- Create: `internal/servicecontrol/agent_paths.go`, tests
- Create: `internal/services/agent_targets.go`
- Create: `internal/dockerweb/windows_provider.go`, tests
- Create: `internal/helper/service_relay_targets_windows.go`, tests
- Modify: `internal/dockerweb/` and `internal/vhost/` only through adapter
  interfaces
- Create: `internal/wsl/docker_rootfs_test.go`

### Lane E — Source modes, paths, CLI/UI, doctor

- Create: `internal/sites/source_mode.go`, `source_mode_test.go`
- Create: `internal/pathmode/` — path translation/validation, tests
- Modify: `internal/sites/service.go`, `internal/sites/php_config.go`,
  `internal/cli/site.go`, `internal/api/handlers_sites.go`
- Modify: `ui/src/lib/AddSite*`, `ui/src/lib/components/ProjectSourceMode.svelte`,
  `ui/tests/e2e.spec.ts`
- Modify: `internal/doctor/checks.go` and tests for Windows/WSL checks
- Create: `internal/sites/source_copy.go`, `source_copy_test.go`
- Modify: `internal/tooling/` for Windows `.cmd` shims and WSL shell/exec;
  tests.

### Wave 2 / integration files

- Create: `install.ps1`, `build/windows/` packaging scripts,
  `test/windows/` acceptance scripts.
- Modify: `cmd/devstack` startup, CLI help/docs.
- Create/modify: `docs/install-windows.md`, `docs/wsl.md`,
  `docs/windows-paths.md`, `docs/windows-networking.md`,
  `docs/troubleshooting.md`.
- Modify: `.github/workflows/ci.yml` for manual Windows build/test dispatch.
- Modify: `build/systemd/devstack-helper.service` and the `install.sh`
  embedded unit for the `/data/devstack/system` layout.
- Modify: `api/openapi.yaml` for source modes and Windows-relevant settings
  (implementation phase only).

---

## Wave 0 — Foundation (Sequential)

Wave 0 must produce a compileable Windows daemon/helper, a Linux
`devstack-agent`, and a testable contract for every lane. Stubs are allowed
only where a platform implementation is genuinely owned by a later lane; they
must compile and return a typed not-implemented error, not silently work.

### Task T0.1: Repository platform hygiene and Windows toolchain baseline

**Files:**
- Create: `.gitattributes`
- Modify: `Makefile` targets `build-windows`, `test-windows-cross`,
  `test-linux`
- Modify: `.github/workflows/ci.yml` manual `workflow_dispatch` input
  `windows`
- Test: `test/release/workflow_test.sh` assertions for the manual Windows
  build
- Create: `docs/development-windows.md`

**Interfaces:**
- Produces `GOOS=windows GOARCH=amd64` build targets for `./cmd/devstack`
  and `./cmd/devstack-helper`.
- Produces `GOOS=linux GOARCH=amd64` build target for `./cmd/devstack-agent`.
- Defines the Windows dev shell (Git Bash or WSL) and the PowerShell-native
  fallback.

- [ ] **Step 1:** Write the failing workflow assertion in
      `test/release/workflow_test.sh`: assert the CI file contains `windows`
      dispatch input, `GOOS=windows`, and a named Windows cross-build step.
- [ ] **Step 2:** Run `bash test/release/workflow_test.sh`; expect failure for
      the missing Windows entries.
- [ ] **Step 3:** Add `.gitattributes`, the Makefile/workflow entries, and
      `docs/development-windows.md`.
- [ ] **Step 4:** Normalize the checkout: `git add --renormalize .`, verify
      `bash -n install.sh test/acceptance/m1.sh test/release/*.sh` passes in
      Git Bash/WSL, and record the result.
- [ ] **Step 5:** Run `GOOS=windows GOARCH=amd64 go build ./cmd/devstack
      ./cmd/devstack-helper`; record every compile failure as a Wave 0
      work item, not as a pass.
- [ ] **Step 6:** Add `go test` tags/exclusions for Linux-only packages so
      Windows test compilation is possible. Do not delete Linux tests.
- [ ] **Step 7:** Run `bash test/release/workflow_test.sh` and the Linux
      focused suite; expect pass.
- [ ] **Step 8:** Commit `chore: add windows build and test baseline`.

### Task T0.2: IPC abstraction and named-pipe security

**Files:**
- Create: `internal/ipc/ipc.go`, `ipc_unix.go`, `ipc_windows.go`,
  `ipc_test.go`
- Modify: `internal/helperclient/client.go`, `internal/client/client.go`,
  `internal/api/middleware.go`
- Add dependency: `github.com/Microsoft/go-winio` pinned
- Create: `internal/ipc/acl_windows.go`, `acl_windows_test.go`

**Interfaces:**
- `func Listen(endpoint Endpoint) (net.Listener, error)`
- `func Dial(ctx context.Context, endpoint Endpoint, timeout time.Duration) (net.Conn, error)`
- `Endpoint{Network string, Address string}`; Windows network `"npipe"`,
  address `\\.\pipe\name`; Unix network `"unix"`.
- Named-pipe ACL construction grants only the installing SID, SYSTEM, and
  (helper only) Administrators.
- Peer-SID verification is a required server hook for both helper and daemon
  control pipes. `api.WithSocketAuth` is applied only after verification.

- [ ] **Step 1:** Write failing table tests for Unix/Windows endpoint parsing,
      empty endpoint, path mapping, ACL construction, and foreign-SID
      rejection.
- [ ] **Step 2:** Run `go test ./internal/ipc`; expect failure.
- [ ] **Step 3:** Implement endpoint parsing and Linux dial/listen.
- [ ] **Step 4:** Implement Windows named-pipe listen/dial and ACL/SID hooks.
- [ ] **Step 5:** Add a Windows-tagged test for a foreign pipe caller.
- [ ] **Step 6:** Run `go test ./internal/ipc`, `go test ./internal/helperclient
      ./internal/client`, and Linux `go test ./...`.
- [ ] **Step 7:** Commit `feat: add cross-platform IPC with pipe ACLs`.

### Task T0.3: Roots, layout, legacy detection, and no-dot policy

**Files:**
- Modify: `internal/config/config.go`, `internal/config/paths.go`,
  `internal/config/config_test.go`
- Create: `internal/platform/platform.go`, `platform_linux.go`,
  `platform_windows.go`, `platform_test.go`
- Modify: `install.sh`, `build/systemd/devstack-helper.service` (new-install
  paths; legacy preserved)
- Create: `internal/platform/nodot_test.go` (path-name audit)
- Modify: `internal/serviceconfig/`, `internal/dockerweb/`,
  `internal/credentials/`, `internal/helper/`, `internal/build/` for
  dot-prefixed temp/state names and legacy migration
- Test: `test/release/installer_test.sh`

**Interfaces:**
- `DefaultRoot(goos string) string` returns `/data/devstack` for Linux and
  `C:\devstack` for Windows.
- `DefaultProjectsRoot(root, goos string) string`.
- `Paths.Root`, `Paths.ProjectsRoot`, `Paths.System`, plus existing subpaths.
- Config key `projects_root`; defaults derive from root.
- Root precedence: explicit `--home`, `DEVSTACK_HOME`, detected legacy
  `~/.devstack`, then platform default.
- No devstack-created Windows basename may start with `.`; project-owned
  paths are exempt.
- Dot-file migration: `.config-manifest.json` → `config-manifest.json`;
  `.serving-provider` → `serving-provider`; `.secret-*`/`.backup-*`/
  `.devstack-tool-*`/`.tmp-*`/`.health-*`/`.snapshot-*` → non-dot temp names.

- [ ] **Step 1:** Write failing table tests for Linux/Windows root, projects
      root, legacy detection, no-dot Windows names, protected `system`
      ownership, and installer layout.
- [ ] **Step 2:** Run `go test ./internal/config ./internal/platform`; expect
      failure.
- [ ] **Step 3:** Implement defaults, precedence, and config round-trip.
- [ ] **Step 4:** Implement the protected `system` path and installer/systemd
      updates. Preserve legacy installs without moving data.
- [ ] **Step 5:** Rename internal dot-prefixed files and add legacy read/
      rename migration. Update tests that assert the old names.
- [ ] **Step 6:** Run config/platform tests, `test/release/installer_test.sh`,
      and `go test ./...`.
- [ ] **Step 7:** Commit `feat: define cross-platform roots and dot-name policy`.

### Task T0.4: Site source mode and path contract

**Files:**
- Modify: `internal/model/model.go`, `model_test.go`
- Create: `internal/store/migrations/0007_site_source_mode.sql`
- Modify: `internal/store/store.go`, `internal/store/store_test.go`
- Create: `internal/pathmode/pathmode.go`, `pathmode_test.go`
- Modify: `internal/sites/service.go` for create/update validation seams

**Interfaces:**
- `type SourceMode string`; constants `SourceLocal`, `SourceWindows`,
  `SourceWSL`.
- `Site.SourceMode SourceMode` JSON `source_mode`.
- `Site.HostPath`, `Site.BackendPath` remain the Windows-visible and
  backend-visible paths.
- Migration adds `source_mode TEXT NOT NULL DEFAULT 'local'`.
- `ValidateSourceMode(mode, platform string) error`: Linux only `local`;
  Windows only `windows`/`wsl`.
- `TranslateSource(mode, input string) (hostPath, backendPath string, err error)`.
  - windows: `C:\x` → `/mnt/c/x`
  - wsl: `\\wsl.localhost\<distro>\home\x` → `/home/x`
  - local: absolute Linux path unchanged
- `ProxyDocumentRoot(site model.Site) (string, error)` derives the
  Windows-visible absolute document root for PHP file resolution.
- Path validation rejects UNC/device paths (except managed WSL), `..`
  escapes, alternate data streams, junctions, and cross-boundary symlinks.

- [ ] **Step 1:** Write failing model/store/pathmode tests for all three modes,
      wrong-platform rejection, arbitrary UNC/device rejection, `..` escape,
      junctions, and `ProxyDocumentRoot`.
- [ ] **Step 2:** Run `go test ./internal/model ./internal/store
      ./internal/pathmode`; expect failure.
- [ ] **Step 3:** Implement model, migration, store scan/insert/update.
- [ ] **Step 4:** Implement path translation, validation, and proxy-root
      derivation.
- [ ] **Step 5:** Run `go test ./...`; confirm migration from schema 0006.
- [ ] **Step 6:** Commit `feat: persist site source mode and path contract`.

### Task T0.5: Complete agent protocol, TLS identity, and token storage

**Files:**
- Create: `internal/agentproto/protocol.go`, `types.go`, `errors.go`,
  `version.go`, `protocol_test.go`
- Create: `internal/agentapi/api.go`, `api_test.go` — module interfaces that
  let Lane D build the Docker/relay/web modules without editing Lane B’s
  agent server package.
- Create: `internal/agentclient/client.go`, `client_test.go`
- Create: `internal/securestore/securestore.go`,
  `securestore_windows.go`, `securestore_linux.go`, `securestore_test.go`
- Create: `internal/agent/tls.go` (identity loader; server in Lane B)
- Modify: provisioning contract tests with fake `wsl.exe`

**Interfaces:**
- Every endpoint from C5 has a typed request/response, error code, timeout,
  and authorization requirement.
- `agentapi.Module{Name() string; MountPath() string; Handler() http.Handler}`
  is the parallel seam for Lane D’s Docker, service-relay, and web-target
  modules. Lane B mounts modules behind the authenticated agent handler; it
  does not import Lane D’s implementation packages.
- `HealthResponse{Version, Distro, Root, Capabilities []string}`.
- `Client` methods for every endpoint; `ErrUnauthenticated`,
  `ErrForbidden`, `ErrVersionMismatch`, `ErrCertPinMismatch`.
- HTTPS only. The agent certificate fingerprint is pinned; the bearer token
  is sent in `Authorization`.
- `securestore.Save(name, secret)` / `Load(name)` uses Windows DPAPI-backed
  storage and a 0600 file on Linux/WSL.
- Provisioning generates the agent token and TLS key/cert once, writes the
  WSL copies as root 0600/0700, and stores the Windows DPAPI copy. Secrets
  never appear in command-line arguments.

- [ ] **Step 1:** Write failing httptest tests for health, missing/wrong token,
      wrong protocol version, certificate pin mismatch, JSON round-trips,
      timeout, and secure-store save/load/delete.
- [ ] **Step 2:** Run `go test ./internal/agentproto ./internal/agentclient
      ./internal/securestore`; expect failure.
- [ ] **Step 3:** Implement the complete types, client, TLS pinning, and
      platform token storage.
- [ ] **Step 4:** Add a contract test that the fake agent and real client
      agree on every endpoint and error code.
- [ ] **Step 5:** Run tests, `go vet`, and a Windows cross-compile.
- [ ] **Step 6:** Commit `feat: add authenticated WSL agent protocol`.

### Task T0.6: WSL runner, path, copy, and localhost-forwarding seams

**Files:**
- Create: `internal/wsl/runner.go`, `runner_test.go`, `path.go`,
  `path_test.go`, `copy.go`, `copy_test.go`, `validate.go`,
  `validate_test.go`, `localhost_forwarding.go`,
  `localhost_forwarding_test.go`

**Interfaces:**
- `type Runner interface { Run(ctx context.Context, args ...string) ([]byte, error) }`
- `type ExecRunner struct{}` using `wsl.exe`.
- `ParseDistroList([]byte) []string`.
- `DistroPath(windowsPath string) (string, error)`.
- `ValidateWSLPath(ctx, distro, path string) (ValidatedPath, error)` using
  agent-side `lstat`/`readlink`/`realpath`.
- `CopySource(ctx, src, dst, mode string, removeSource bool) (CopyResult, error)`
  using tar + SHA-256 manifest + Linux ownership/mode verification.
- `ProbeLocalhostForwarding(ctx, distro, port int) error`.

- [ ] **Step 1:** Write failing fake-runner tests for list/import/start/exec
      error propagation, Windows→WSL path mapping, symlink/junction rejection,
      copy checksum/rollback, and localhost-forwarding failure.
- [ ] **Step 2:** Run `go test ./internal/wsl`; expect failure.
- [ ] **Step 3:** Implement the runner seam and path translation.
- [ ] **Step 4:** Implement the agent-side validate/copy protocol and tests.
- [ ] **Step 5:** Implement localhost-forwarding probing and typed failure.
- [ ] **Step 6:** Run tests with no real `wsl.exe`; assert fakes are used.
- [ ] **Step 7:** Commit `feat: add WSL runner, path, copy, and forwarding seams`.

### Task T0.7: Daemon/CLI transport, lifecycle, and control pipe

**Files:**
- Modify: `internal/daemon/daemon.go`, `daemon_test.go`
- Create: `internal/daemon/lock_windows.go`, `lock_unix.go`
- Modify: `internal/client/client.go`, `client_test.go`
- Modify: `internal/cli/root.go`, `internal/cli/daemon.go`
- Create: `build/windows/devstack-daemon-task.ps1` (or equivalent)

**Interfaces:**
- `daemon.SocketPath(home)`/transport resolves to Unix socket or Windows
  named pipe through `internal/ipc`.
- Lifetime lock, PID file, readiness marker, and control pipe are
  platform-specific; Windows uses `LockFileEx`, not `flock`.
- `daemon status|stop|restart` uses the control pipe on Windows.
- Per-user logon startup is a limited-privilege scheduled task or HKCU
  startup entry; it never runs elevated.
- No public API changes.

- [ ] **Step 1:** Write failing tests for Linux socket path, Windows named-pipe
      selection, lock exclusivity, readiness cleanup, stop/restart, and
      per-user startup command construction.
- [ ] **Step 2:** Run `go test ./internal/daemon ./internal/client`; expect
      failure.
- [ ] **Step 3:** Route transport and lifecycle through platform seams.
- [ ] **Step 4:** Add Windows control-pipe SID verification and tests.
- [ ] **Step 5:** Run Linux tests and Windows cross-compile.
- [ ] **Step 6:** Commit `refactor: select daemon transport and lifecycle per platform`.

### Task T0.8: Platform capability and process seams

**Files:**
- Create: `internal/platform/capabilities.go`, `capabilities_test.go`
- Create: `internal/platform/process.go`, `process_linux.go`,
  `process_windows.go`
- Create: `internal/platform/terminal.go`, `terminal_linux.go`,
  `terminal_windows.go` (Windows agent-proxy stub)
- Create: `internal/platform/perf.go`, `perf_linux.go`, `perf_windows.go`
- Create: `internal/platform/doctor.go`, `doctor_linux.go`,
  `doctor_windows.go`
- Create: `internal/platform/build.go`, `build_linux.go`,
  `build_windows.go`
- Create: `internal/platform/tooling.go`, `tooling_linux.go`,
  `tooling_windows.go`

**Interfaces:**
- `Capabilities{HasSystemd, HasDockerSocket, HasNamedPipe, HasWSL,
  SupportsHostTuning, SupportsConPTY, RequiresWSLAgent}`.
- Process command creation, hidden windows, signals/process-group teardown.
- Terminal: local PTY on Linux; agent-proxy stub on Windows until Lane F.
- Performance: Linux `/proc` collector; Windows resource collector; host
  tuning disabled.
- Doctor: Linux port/disk/systemd checks; Windows port/disk/WSL/agent checks.
- Build: local Linux executor; Windows agent-proxy stub until Lane F.
- Tooling: POSIX shims on Linux; Windows `.cmd` WSL shims stub.

- [ ] **Step 1:** Write failing capability/process/terminal/perf/doctor/build/
      tooling table tests.
- [ ] **Step 2:** Run `go test ./internal/platform`; expect failure.
- [ ] **Step 3:** Implement the Linux implementations and Windows compile
      stubs with typed not-implemented errors where the lane has not landed.
- [ ] **Step 4:** Run tests and Windows cross-compile.
- [ ] **Step 5:** Commit `feat: add platform capability and process seams`.

### Task T0.9: Backend, port, route, and proxy-path contract

**Files:**
- Modify: `internal/backend/backend.go` (document the contract; add
  `PortProvider` if needed)
- Create: `internal/ports/ports.go`, `ports_test.go`
- Modify: `internal/sites/service.go`, `routes.go`, `vhosts.go`
- Modify: `internal/proxy/router.go`, `php.go` for `ProxyDocumentRoot`
- Create: `internal/backend/wsl/backend.go` (skeleton only; implementation in
  Lane C)
- Create: `internal/backend/wsl/backend_contract_test.go`

**Interfaces:**
- `PortProvider` allocates/releases/validates backend ports. Linux uses the
  existing `domain.PortAllocator`; Windows uses the agent RPC.
- `PreparedSite.BackendHost` is used by routes; no hardcoded `127.0.0.1` in
  `sites.routes`.
- PHP routes carry `ProxyDocumentRoot` (Windows-visible) separately from the
  backend document root.
- `sites.Service` permits a single backend implementation that handles all
  Windows sites; no local Windows site supervisor.
- Runtime installation, exec, logs, build, and terminal are delegated through
  the same backend/agent boundary.

- [ ] **Step 1:** Write failing tests for port-provider selection, route
      `BackendHost`, PHP `ProxyDocumentRoot`, and a fake agent returning
      non-default ports.
- [ ] **Step 2:** Run `go test ./internal/sites ./internal/proxy
      ./internal/backend/wsl`; expect failure.
- [ ] **Step 3:** Implement the contracts and Linux regressions.
- [ ] **Step 4:** Add a compile-only Windows agent backend skeleton with typed
      not-implemented errors.
- [ ] **Step 5:** Run `go test ./...` and Windows cross-compile.
- [ ] **Step 6:** Commit `refactor: freeze backend, port, route, and PHP path contracts`.

### Task T0.10: Freeze foundation and create lane worktrees

**Files:**
- Create: `docs/superpowers/status/2026-10-27-windows-baseline.md`
- Create: `.superpowers/sdd/<plan>/progress.md` using the SDD workspace script
  (or the local equivalent)

- [ ] **Step 1:** Run `go test ./...`, `go vet ./...`, `gofmt -l .`, and the
      Windows cross-compile/build matrix.
- [ ] **Step 2:** Verify `bash -n` on Linux scripts inside WSL/Git Bash and
      record the command.
- [ ] **Step 3:** Record the base commit SHA, frozen interfaces, test evidence,
      and known compile stubs in the baseline status file.
- [ ] **Step 4:** Create the integration branch and five lane worktrees from
      the frozen commit.
- [ ] **Step 5:** Stop and dispatch Wave 1 only after every lane can compile
      against the frozen contracts. Do not dispatch D before B's agent server
      contract is available.

---

## Wave 1 — Five Concurrent Lanes (all start together)

### Lane A — Windows helper and host integration

**Branch:** `lane/windows-helper`
**Owner:** one implementer subagent.
**Depends on:** T0.2, T0.3.
**Merge:** first, after review.

#### Task A1: Named-pipe helper server and SID authentication

**Files:** `internal/helper/peer_windows.go`, `peer_linux.go`,
`internal/helper/server_windows_test.go`, `cmd/devstack-helper/main_windows.go`,
`command` build tags.

**Interfaces:**
- `PeerIdentity{UID int, SID string}`.
- Windows peer SID must match `helper.Config.AllowSID`; pipe ACL grants only
  that SID, SYSTEM, and Administrators.
- Linux behavior remains `AllowUID`/`SO_PEERCRED`.
- The Windows helper server has no Unix-socket/AllowUID code path.

- [ ] Write failing tests for allowed SID, foreign SID, empty SID, pipe ACL
      construction, and Linux peer regression.
- [ ] Run the Windows-tagged tests on Windows and the Linux suite on Linux;
      expect failure/compile failure.
- [ ] Implement named-pipe listener using `internal/ipc` and SID checks.
- [ ] Run focused tests, `go test ./...`, and cross-compile.
- [ ] Commit `feat: add windows helper named pipe`.

#### Task A2: Windows helper service lifecycle

**Files:** `internal/helper/service_windows.go`,
`cmd/devstack-helper/service_windows.go`,
`internal/helper/windows_service_test.go`.

- [ ] Write failing tests for service install/uninstall command construction,
      start/stop idempotency, non-admin denial, and LocalSystem config.
- [ ] Run tests; expect failure.
- [ ] Implement `golang.org/x/sys/windows/svc` handler and install/uninstall
      primitives behind interfaces.
- [ ] Run tests and a controller-serialized Windows service smoke.
- [ ] Commit `feat: add windows helper service lifecycle`.

#### Task A3: Windows CA, trust stores, hosts, and firewall

**Files:** `internal/wintrust/`, `internal/winhosts/`,
`internal/winfirewall/`, `internal/helper/windows_host_test.go`.

- [ ] Write failing tests for CA storage ACL, Windows root store install/
      remove, Firefox policy, hosts block preservation, firewall rule
      create/delete/disable, and protection of `C:\devstack\system`.
- [ ] Run; expect failure.
- [ ] Implement adapters; keep the CA key out of daemon-readable paths.
- [ ] Run tests, `go vet`, and a fake-runner smoke.
- [ ] Commit `feat: add windows host integration`.

#### Task A4: Windows 80/443 forwarders and target synchronization

**Files:** `internal/helper/proxy_targets.go`,
`internal/helper/forwarder_windows.go`, `cmd/devstack-helper/config_windows.go`,
`internal/helper/proxy_targets_windows_test.go`.

- [ ] Write failing tests for TCP forwarders to the Windows daemon ports,
      helper restart, target persistence, and rejection of non-loopback
      targets.
- [ ] Run; expect failure.
- [ ] Implement fixed listener/forwarder targets and Windows helper config
      persistence under `C:\devstack\system\helper.yaml`.
- [ ] Run tests and a controller-serialized helper restart smoke.
- [ ] Commit `feat: add windows public forwarders` and report.

### Lane B — WSL provisioning, agent, and ingress

**Branch:** `lane/wsl-agent`
**Owner:** one implementer subagent.
**Depends on:** T0.5, T0.6.
**Merge:** second; C/D may develop concurrently, but their modules mount only
after B's agent server is merged.

#### Task B1: WSL distro provision/import lifecycle

**Files:** `internal/wsl/provision.go`, `provision_test.go`,
`build/wsl/build-rootfs.sh`, `build/wsl/versions.lock`,
`build/wsl/README.md`.

- [ ] Write failing fake-`wsl.exe` tests for the WSL 2.0.0/systemd preflight,
      existing-distro marker, unmarked-distro refusal, import, start,
      idempotent reuse, localhost-forwarding check, and unregister with
      confirmation.
- [ ] Run; expect failure.
- [ ] Implement the provisioner with `C:\devstack\wsl\devstack`, the pinned
      Debian 12 rootfs, `/data/devstack/managed.json`, and no arbitrary
      commands. Refuse an unmarked `devstack` distro.
- [ ] Implement rootfs checksum verification and disk-space checks.
- [ ] Run tests and a Windows fake integration.
- [ ] Commit `feat: provision managed WSL distro`.

#### Task B2: Agent server, TLS, auth, config, health

**Files:** `cmd/devstack-agent/main.go`, `internal/agent/server.go`,
`auth.go`, `config.go`, `health.go`, tests. `internal/agent/tls.go` was
created in T0.5; use it here.

- [ ] Write failing httptest/TLS tests for bearer auth, certificate pinning,
      dual loopback/NAT listeners, health, version, config permissions, and
      token/key file modes.
- [ ] Run; expect failure.
- [ ] Implement the agent with `/data/devstack/agent.token`,
      `/data/devstack/agent-tls`, and dual listeners.
- [ ] Run tests, `go vet`, and a Debian WSL smoke.
- [ ] Commit `feat: add WSL agent server`.

#### Task B3: Agent static backend, process/runtime foundation, and port ownership

**Files:** `internal/agent/runtime.go`, `static.go`, `process.go`,
`ports.go`, tests.

- [ ] Write failing tests for static site prepare/start/stop, document-root
      validation, port allocation/validation/reuse, log streaming, and
      restart reconciliation.
- [ ] Run; expect failure.
- [ ] Reuse Linux staticserve/supervisor where possible; keep the agent API as
      the boundary.
- [ ] Run tests and a Debian WSL smoke.
- [ ] Commit `feat: add agent static backend and ports`.

#### Task B4: Dedicated Docker Engine and agent readiness

**Files:** `build/wsl/agent.service`, `build/wsl/docker-daemon.json`,
`internal/agent/docker_engine.go`, tests.

- [ ] Write failing tests for Docker Engine health, data-root
      `/data/devstack/docker`, Compose presence, startup failure mapping, and
      systemd readiness.
- [ ] Run; expect failure.
- [ ] Implement Docker bootstrap/health and systemd unit dependencies.
- [ ] Run tests and a real WSL Docker `hello-world`/Compose smoke.
- [ ] Commit `feat: add dedicated docker engine to WSL agent`.
      The Docker control API and service relay are owned by Lane D.

#### Task B5: WSL `/etc/hosts`, CA bundle, and 80/443 ingress

**Files:** `internal/agent/hosts.go`, `internal/agent/ingress.go`,
`hosts_test.go`, `ingress_test.go`,
`internal/wsl/hosts_config_test.go`.

- [ ] Write failing tests for a managed hosts block, localhost preservation,
      `generateHosts=false`, unrelated-line preservation, `.test` mapping to
      `127.0.0.1`, host-gateway discovery, ingress restart, and removal on
      uninstall.
- [ ] Run; expect failure.
- [ ] Implement root-only hosts updates and the fixed 80/443 ingress.
- [ ] Inject the CA bundle into site/terminal environments.
- [ ] Run tests and a WSL smoke resolving and fetching a registered `.test`
      name with curl.
- [ ] Commit `feat: manage WSL hosts and public ingress`.

### Lane C — Windows daemon, public proxy, and WSL backend

**Branch:** `lane/windows-daemon`
**Owner:** one implementer subagent.
**Depends on:** T0.5, T0.7, T0.9, and B's agent server contract.
**Merge:** third.

#### Task C1: Windows daemon startup, config, SQLite, lifecycle, and CLI pipe

**Files:** `internal/daemon/windows.go`, `windows_test.go`,
`lifecycle_windows.go`, `pipe_windows.go`,
`internal/client/transport_windows.go`, `internal/cli/root.go`.

- [ ] Write failing tests for `C:\devstack` defaults, no-dot names, SQLite on
      NTFS, named-pipe CLI, lock/readiness/stop/restart, log path, and
      per-user startup.
- [ ] Run; expect failure.
- [ ] Implement platform startup; preserve Linux behavior.
- [ ] Run tests and Windows compile.
- [ ] Commit `feat: add windows daemon startup`.

#### Task C2: Public proxy, listeners, and helper target updates

**Files:** `internal/proxy/windows_listen.go`, `internal/daemon/daemon.go`,
`internal/daemon/daemon_windows_test.go`.

- [ ] Write failing tests for 7070/8080/8443 listeners, WSL-adapter binding,
      firewall-scoped listener behavior, helper target update before
      reconcile, clean shutdown, and dashboard routing.
- [ ] Run; expect failure.
- [ ] Implement named-pipe helper target updates and proxy lifecycle.
- [ ] Run tests and a Windows smoke.
- [ ] Commit `feat: add windows public proxy lifecycle`.

#### Task C3: WSL backend client and route/port integration

**Files:** `internal/backend/wsl/backend.go`, `backend_test.go`,
`internal/agentclient/client_test.go`, `internal/sites/routes.go` wiring,
`internal/ports/ports.go` wiring.

- [ ] Write failing tests for create/start/stop/status/logs/exec through the
      agent, path mapping, timeout, auth failure, port allocation/reuse,
      `PreparedSite.BackendHost`, and PHP `ProxyDocumentRoot`.
- [ ] Run; expect failure.
- [ ] Implement the `backend.Backend`/`PHPStager` adapter over `agentclient`.
- [ ] Wire the Windows daemon to use it for all sites.
- [ ] Run tests and fake-agent integration.
- [ ] Commit `feat: add WSL backend client`.

#### Task C4: Windows daemon/helper/agent bootstrap

**Files:** `internal/daemon/windows_bootstrap.go`, tests.

- [ ] Write failing tests for startup order (helper → WSL agent → proxy →
      reconcile), token/certificate loading, localhost-forwarding failure
      mapping, and recovery after agent/helper restart.
- [ ] Run; expect failure.
- [ ] Implement bootstrap and diagnostics.
- [ ] Run tests and a Windows vertical smoke.
- [ ] Commit `feat: bootstrap windows daemon services`.

### Lane D — Docker services and private web-serving adapter

**Branch:** `lane/docker-services`
**Owner:** one implementer subagent.
**Depends on:** T0.5 `agentapi`/`agentproto` for compile. It owns separate
`internal/agentdocker`, `internal/agentrelay`, and `internal/agentweb`
packages and does not edit Lane B’s `internal/agent` server.
**Merge:** fourth, after B’s agent server is merged so the controller can mount
the modules.

#### Task D1: Agent Docker control and service lifecycle API

**Files:** `internal/agentdocker/docker.go`,
`internal/agentdocker/docker_test.go`.

- [ ] Write failing tests for fixed service IDs, Compose project name, image
      digest pinning, lifecycle actions, arbitrary-command rejection, and
      `agentapi.Module` mounting.
- [ ] Run; expect failure.
- [ ] Implement the agent-only Docker control module.
- [ ] Run tests with fake Docker.
- [ ] Commit `feat: add agent docker control`.

#### Task D2: Servicecontrol adapter on Windows

**Files:** `internal/servicecontrol/agent_docker.go`,
`agent_docker_test.go`, `internal/servicecontrol/agent_paths.go`, tests.

- [ ] Write failing tests for service install/activate/deactivate/logs/
      credentials through the agent, WSL path ownership, config paths, and
      Linux regressions.
- [ ] Run; expect failure.
- [ ] Implement the adapter behind existing interfaces.
- [ ] Route config/data/secrets to WSL; no NTFS service data.
- [ ] Run focused and full tests.
- [ ] Commit `feat: add windows service control adapter`.

#### Task D3: Private-port Docker web serving and TLS copies

**Files:** `internal/agentweb/web_targets.go`, `web_targets_test.go`,
`internal/dockerweb/windows_provider.go`, tests,
`internal/vhost/agent_adapter.go`.

- [ ] Write failing tests for nginx/Apache private HTTP ports, HTTP-only
      generated vhosts, leaf certificate copies, daemon public TLS routing,
      the absent container dashboard vhost, and `agentapi.Module` mounting.
- [ ] Run; expect failure.
- [ ] Implement the Windows-specific web adapter and target updates.
- [ ] Run tests and a WSL nginx smoke.
- [ ] Commit `feat: add WSL private-port web serving`.

#### Task D4: Service config/data/secrets permissions and backups

**Files:** `internal/servicecontrol/agent_paths.go`, tests,
`internal/serviceconfig/agent_backup.go`, tests.

- [ ] Write failing tests for ext4 data paths, no NTFS data, agent-side
      config read/write, 0700 secrets, backups, and deletion confirmation.
- [ ] Run; expect failure.
- [ ] Implement path/permission/backup rules through the agent.
- [ ] Run tests and a full service smoke.
- [ ] Commit `feat: enforce WSL service data rules`.

#### Task D5: Windows loopback service relay

**Files:** `internal/agentrelay/service_relay.go`,
`internal/agentrelay/service_relay_test.go`,
`internal/helper/service_relay_targets_windows.go`,
`internal/helper/service_relay_targets_windows_test.go`.

- [ ] Write failing tests for the fixed service-port allowlist, relay to WSL
      loopback, Windows helper loopback forwarding, firewall scoping,
      rejection of arbitrary ports, and `agentapi.Module` mounting.
- [ ] Run; expect failure.
- [ ] Implement the agent relay module and helper targets for MySQL/MariaDB,
      PostgreSQL, Redis, and phpMyAdmin.
- [ ] Run tests and a Windows→WSL phpMyAdmin smoke.
- [ ] Commit `feat: add windows loopback service relay`.

### Lane E — Source modes, paths, CLI/UI, and doctor

**Branch:** `lane/site-source`
**Owner:** one implementer subagent.
**Depends on:** T0.4, T0.6, T0.8; can run parallel with A/B.
**Merge:** fifth.

#### Task E1: Site source mode through service/API/CLI

**Files:** `internal/sites/source_mode.go`, `internal/sites/service.go`,
`internal/api/handlers_sites.go`, `internal/cli/site.go`,
`internal/sites/source_copy.go`, tests.

- [ ] Write failing tests for Windows/WSL source defaults, path translation,
      move-mode rejection/success, stopped-site requirement, agent-side
      copy/verify, source retention, failure rollback, and no-dot names.
- [ ] Run; expect failure.
- [ ] Implement source-mode service/API/CLI behavior through the agent copy
      contract.
- [ ] Run tests.
- [ ] Commit `feat: expose site source modes`.

#### Task E2: Path policy and PhpStorm/WSL mapping

**Files:** `internal/pathmode/`, docs, tests.

- [ ] Write failing tests for `C:\`, `\\wsl.localhost`, `\\wsl$`, UNC/device
      rejection, dot-name policy, junction/symlink escape,
      case-insensitive containment, and Windows→WSL mapping.
- [ ] Run; expect failure.
- [ ] Implement/complete path policy and docs.
- [ ] Run tests.
- [ ] Commit `feat: enforce windows/wsl path policy`.

#### Task E3: Dashboard project-source selector

**Files:** `ui/src/lib/components/ProjectSourceMode.svelte`, `AddSite*.svelte`,
`ui/tests/e2e.spec.ts`.

- [ ] Write failing Vitest/Playwright tests for the selector, WSL default for
      PHP/Node, Windows default for static, performance warnings, source-mode
      copy warnings, and no dot path display.
- [ ] Run; expect failure.
- [ ] Implement the selector and warnings.
- [ ] Run `npm run check`, unit, Playwright.
- [ ] Commit `feat: add dashboard project source selector`.

#### Task E4: Windows/WSL doctor and settings

**Files:** `internal/doctor/checks.go`, `internal/api`, `ui` Settings, tests.

- [ ] Write failing tests for WSL/agent/helper/Docker/localhost-forwarding/
      port checks and Windows settings presentation.
- [ ] Run; expect failure.
- [ ] Implement checks and UI.
- [ ] Run tests.
- [ ] Commit `feat: add windows/wsl diagnostics`.

---

## Integration 1 — Vertical Slice

### Task I1: Merge Wave 1 lanes and resolve shared interfaces

**Files:** merge commits only; controller-owned.

- [ ] Create the integration branch from the frozen foundation.
- [ ] Merge A, B, C, D, E in dependency order; run focused tests after each.
- [ ] Resolve owner conflicts in the controller only.
- [ ] Run `go test ./...`, `go vet ./...`, and UI checks.
- [ ] Record merge SHAs in the ledger.

### Task I2: Windows static-site vertical slice

**Files:** `test/windows/vertical_slice.ps1`, temporary test fixtures.

- [ ] Write an acceptance script that:
  - starts helper/daemon/agent;
  - creates one Windows-mode static site;
  - creates one WSL-mode static site;
  - checks Windows hosts/CA trust;
  - fetches trusted `.test` HTTPS pages from Windows;
  - runs `curl https://<site>.test` and `curl https://devstack.test` from
    inside WSL;
  - verifies the WSL ingress target and helper forwarder;
  - verifies `devstack doctor` reports localhost forwarding.
- [ ] Run on a fresh Windows 10 22H2 and a fresh Windows 11 machine.
- [ ] Fix only integration defects; commit `test: add windows vertical slice`.
- [ ] **Gate:** If either source mode or either Windows/WSL client path cannot
      serve trusted `.test` HTTPS, stop and do not start Wave 2.

---

## Wave 2 — Five Parallel Parity Lanes

Before dispatching Wave 2, the controller expands each lane into the exact
task-brief format used by Wave 1: files, interfaces, RED tests, commands, and
commits. The scopes below are binding, but no Wave 2 lane starts from a
prose-only brief.

### Lane F — PHP/Node/FPM, terminal, builds, and tooling parity

**Files:** `internal/agent/runtime.go`, `internal/agent/php.go`,
`internal/agent/fpm.go`, `internal/agent/terminal.go`,
`internal/agent/build.go`, `internal/tooling/*_windows.go`, tests.

- [ ] Agent PHP runtime install/select/validate/swap and per-site FPM
      configuration generation in WSL.
- [ ] Debian 12 PHP catalog compatibility gate for every supported artifact;
      publish a WSL variant/catalog entry if an artifact fails.
- [ ] Node runtime parity and WSL builds for Windows/WSL source modes.
- [ ] Site terminal/PTY through the agent WebSocket, ticket/auth, process
      teardown, and Xdebug/path-mapping docs.
- [ ] Windows `.cmd` shims for global PHP/Composer, site shell/exec, and
      shell-init guidance.
- [ ] Windows-mode PHP/Node performance warnings and benchmark fixture.
- [ ] TDD: runtime missing, FPM invalid, extension isolation, terminal
      ticket/auth, build rollback, shim quoting.
- [ ] Commit `feat: add WSL PHP, Node, terminal, build, and tooling parity`.

### Lane G — Installers, trust, upgrade, uninstall, and rootfs

**Files:** `install.ps1`, `build/windows/`, `test/windows/`,
`build/wsl/versions.lock`, `build/wsl/build-rootfs.sh`, `install.sh`,
`build/systemd/devstack-helper.service`, tests.

- [ ] `install.ps1` system phase: elevation, fixed `C:\devstack`, protected
      `system` ACL, helper service, firewall, per-user task, PATH, and a
      complete prerequisite manifest for Windows 10 22H2/Windows 11.
- [ ] Install prerequisites in advance: Windows build/virtualization check,
      WSL optional feature, `wsl --install`/`--update`, WSL 2.0.0+, systemd,
      and disk space. Reboot-required feature changes use `-AllowReboot` or a
      prompt, schedule a resume, and never reboot silently.
- [ ] WSL prerequisite phase: distro import, systemd enablement, Docker Engine
      and Compose, CA/build packages, agent, and pinned PHP/Node dependencies.
      Record installed/verified versions in the resumable manifest.
- [ ] `devstack init` user phase: WSL import, agent token/TLS provisioning,
      CA bundle, hosts, startup, and localhost-forwarding verification.
- [ ] Update `install.sh` and the systemd helper unit for new
      `/data/devstack` roots and the root-only `system` layout while
      preserving legacy installs.
- [ ] Pin the Debian 12 rootfs, Docker Engine/Compose, agent, and PHP/Node
      artifacts; publish checksums/SBOMs.
- [ ] Upgrade: export the WSL distro, replace rootfs/agent in a temporary
      distro, restore `/data/devstack`, verify, roll back on failure.
- [ ] Uninstall: trust/hosts/firewall/service cleanup; preserve WSL distro,
      projects, and state by default; explicit export/discard for WSL.
- [ ] TDD with fake PowerShell/sudo/service runners and
      `test/release/installer_test.sh`; test upgrade from the released
      `v0.1.0` Linux layout.
- [ ] Commit `feat: add cross-platform installer lifecycle`.

### Lane H — Docker service parity and private web serving

**Files:** `internal/agent/*`, `internal/servicecontrol/*`,
`internal/dockerweb/*`, `internal/services/*`, tests.

- [ ] Full Compose render, nginx/Apache, databases, Redis, phpMyAdmin,
      credentials, rotation, logs, backup/delete.
- [ ] Verify service data ext4 and Docker root `/data/devstack/docker`.
- [ ] Windows private-port web serving: daemon TLS/proxy, HTTP-only vhosts,
      no container dashboard vhost, service target updates.
- [ ] Helper target updates for daemon proxy ports (not WSL).
- [ ] TDD with fake Docker and one real WSL Docker smoke.
- [ ] Commit `feat: complete WSL docker services`.

### Lane I — Performance, benchmarks, config, ports, and UI

**Files:** `internal/performance/*`, `internal/servicecontrol/config.go`,
`internal/api`, `ui/`, tests.

- [ ] Windows resource collection; host tuning disabled; FPM pool via agent.
- [ ] Benchmarks from Windows through the public route, with stored measured
      results and labels.
- [ ] Service config editor/diff/apply/reset through the agent.
- [ ] Listener-port changes: daemon proxy, helper targets, WSL ingress
      recovery, restart-required handling.
- [ ] Dashboard platform status/source modes/settings/tooling.
- [ ] TDD unit + Playwright.
- [ ] Commit `feat: add windows performance, config, ports, and UI parity`.

### Lane J — Acceptance, release, and documentation

**Files:** `test/windows/`, `docs/install-windows.md`, `docs/wsl.md`,
`docs/windows-paths.md`, `docs/windows-networking.md`, `docs/troubleshooting.md`,
release workflow/docs.

- [ ] Windows release archive: daemon, helper, agent, rootfs, `install.ps1`,
      checksums, SBOM; no Docker PHP.
- [ ] Manual-dispatch Windows workflow.
- [ ] Fresh Windows acceptance matrix from the spec, including WSL curl and
      dashboard, source switch, reboot, upgrade, uninstall.
- [ ] Update README/handoff/known issues with truthful status.
- [ ] Linux full regression gate.
- [ ] Commit `docs: add windows acceptance and release`.

---

## Integration 2 — Final Merge and Gate

### Task M1: Merge Wave 2

- [ ] Merge F→G→H→I→J; run focused tests after each.
- [ ] Resolve owner conflicts in the controller only.
- [ ] Run `go test ./...`, `go test -race ./...`, `go vet ./...`, UI
      check/unit/Playwright/build.
- [ ] Run Linux PHP acceptance, Linux E2E, release/install/workflow tests,
      Docker smokes, and the Linux `v0.1.0` upgrade test.
- [ ] Run Windows fresh-install acceptance and reboot/WSL recovery.
- [ ] Commit merge.

### Task M2: Final independent review

- [ ] Generate a whole-branch diff package.
- [ ] Dispatch a fresh final reviewer with the spec, plan, Review Focus, and
      ledger rulings.
- [ ] Fix Critical/Important with RED→GREEN tests; ledger Minors.
- [ ] Re-run the full gate.
- [ ] Update handoff/known issues; do not claim Windows support until
      acceptance evidence exists.

### Task M3: Finish and pause

- [ ] Record final SHAs, test evidence, and remaining gates.
- [ ] Delete lane worktrees and mark the plan complete.
- [ ] Do not implement beyond this plan without human approval.

---

## Acceptance Matrix

| Check | Required evidence |
|---|---|
| Fresh Windows install | No manual WSL setup beyond prerequisites; helper/daemon/agent healthy; localhost forwarding reported |
| Trusted `.test` from Windows | Edge/Chrome and Firefox trusted for Windows-mode and WSL-mode projects |
| Trusted `.test` from WSL | `curl https://<site>.test` and `curl https://devstack.test` succeed in builtin and Docker modes |
| Windows project | `C:\devstack\projects\...` static/PHP/Node flows |
| WSL project | `\\wsl.localhost\devstack\...` static/PHP/Node flows |
| Source mode switch | Stopped-site copy/verify/rollback; source retained by default; no escaping symlinks |
| PHP | Linux FPM, extensions, pool sizing, Xdebug path mapping, global `php`/`composer` shims |
| Docker | nginx/Apache private HTTP, MySQL/MariaDB/PostgreSQL/Redis/phpMyAdmin, rotation, logs, credentials |
| Dashboard in Docker mode | `https://devstack.test` works from Windows and WSL; no container dashboard vhost |
| Ports | 80/443 follow daemon/WSL target changes; ingress recovers after WSL IP change |
| Recovery | daemon, helper, WSL, Windows reboot; sites reconcile |
| Upgrade | Rootfs/agent replace; state/projects/VHDX survive; rollback tested |
| Uninstall | State/projects preserved by default; trust/hosts/firewall cleaned; WSL removal requires export/discard |
| Security negatives | Foreign SID cannot use pipes; missing/wrong agent token and pin fail; no public/LAN agent or database exposure |
| No-dot policy | No devstack-created Windows basename starts with a dot |
| Linux regression | Existing full gate, E2E, PHP acceptance, Docker smokes, and v0.1.0 upgrade |
| Scope | No Docker PHP, no native Windows PHP, no container CA private key |

## Pre-Implementation Checklist

- [ ] `core.autocrlf=false`; `.gitattributes` committed; `git add
      --renormalize .`; `bash -n` passes in Git Bash/WSL.
- [ ] `make` available, or PowerShell equivalents documented and tested.
- [ ] Node 22 selected for the UI/CI baseline.
- [ ] Go module downloads work; `go test ./...` runs on Linux/WSL.
- [ ] Windows 10 22H2 or Windows 11; WSL 2.0.0+ with systemd; separate dev\n      distro available.
- [ ] Docker Engine inside WSL available for controller-serialized tests.
- [ ] Administrator access for helper/service/ACL/80/443 tests.
- [ ] The reviewed contracts C1–C6 are frozen before lane dispatch.
- [ ] The Windows plan is approved by the human partner.

## Pause Point

Planning is revised and internally consistent. No production code, lane
worktree, or implementation subagent may start until the human partner
approves this revised plan. The first implementation task is **T0.1**, not
Wave 1.
