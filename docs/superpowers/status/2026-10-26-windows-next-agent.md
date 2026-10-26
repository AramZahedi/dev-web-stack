# Next Agent on Windows — Start Here

- **Date:** 2026-10-26
- **Revised:** 2026-10-27 after the in-depth plan review
- **Purpose:** operational runbook for the first AI-agent session on the Windows machine
- **Status:** implementation has not started; the revised plan is waiting for approval
- **Design:** [Windows 11 + WSL2 design](../specs/2026-10-26-windows-wsl-design.md)
- **Plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Review resolution:** [Windows plan review resolution](2026-10-27-windows-plan-review-resolution.md)
- **Knowledge handoff:** [Windows/WSL knowledge handoff](2026-10-26-windows-wsl-handoff.md)
- **Planning checkpoint:** [Windows planning checkpoint](2026-10-26-windows-planning.md)

## 1. First: verify the machine and checkout

Required:

- Windows 11 22H2 or newer, x86_64.
- WSL2 2.0.0 or newer with systemd support.
- Virtualization enabled.
- Administrator access for helper/service/ACL tests.
- Go 1.26+.
- Node.js 22 and npm 10+.
- Git for Windows, PowerShell 7.
- `bash` (Git Bash or WSL) and `make`, or the PowerShell-native equivalents
  documented below.
- Docker Engine inside WSL, **not Docker Desktop**, for acceptance.
- 16 GB RAM recommended; at least 50 GB free.

Use a short repository path. Do not clone into `C:\devstack`; that is runtime
state.

```powershell
git config --global core.autocrlf false
git clone git@github.com:AramZahedi/dev-web-stack.git C:\src\dev-web-stack
cd C:\src\dev-web-stack
git switch master
git pull --ff-only
git rev-parse HEAD
```

If the checkout already exists, do not just edit in place. Run:

```powershell
git config --global core.autocrlf false
git config --local core.autocrlf false
git add --renormalize .
git status
```

The current checkout was created with `core.autocrlf=true`; its shell scripts
contain CRLF and `bash -n` fails. T0.1 must correct this and commit
`.gitattributes` before any Bash-based task.

Record the commit SHA. Read, in order:

1. `docs/superpowers/status/2026-10-26-windows-wsl-handoff.md`
2. `docs/superpowers/status/2026-10-27-windows-plan-review-resolution.md`
3. `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`
4. `docs/superpowers/plans/2026-10-26-windows-wsl.md`
5. `docs/handoff.md`
6. `docs/known-issues.md`

Do not re-open settled architecture questions unless the design is contradicted
by the machine.

## 2. WSL prerequisites

Check:

```powershell
wsl --version
wsl --status
wsl -l -v
```

If WSL is missing or older than 2.0.0, stop and ask the human to run the
required `wsl --install`/`wsl --update` and reboot. Do not silently enable
Windows features or reboot.

Use a separate development distro such as `Ubuntu` for Linux tests. The managed
`devstack` distro will be created by the implementation. Do not name the
development distro `devstack`; the provisioner treats an unmarked `devstack`
distro as a collision and refuses it.

Docker Engine must run inside a Linux WSL distro for the managed acceptance
path. Docker Desktop is not the supported substrate.

## 3. Verify the baseline before coding

Run the Linux regression suite from a clone inside WSL ext4, not `/mnt/c`:

```bash
mkdir -p ~/src
cd ~/src
git clone git@github.com:AramZahedi/dev-web-stack.git
cd dev-web-stack
git switch master
go test ./...
```

On Windows, record the current build state:

```powershell
cd C:\src\dev-web-stack
go version
node --version
npm --version
make --version
bash --version
```

Do not treat pre-foundation Windows compile failures as a defect; Wave 0
introduces the Windows seams. Do not treat a missing `make` as a pass; install
it or use the PowerShell equivalents documented in T0.1.

## 4. Start Wave 0 only

Create the integration branch from the planning commit:

```powershell
git switch -c feature/windows-wsl
```

Wave 0 is sequential and comes first. The revised task list is:

1. **T0.1** Repository platform hygiene and Windows toolchain baseline
   (CRLF, `.gitattributes`, build/test matrix, Windows dev shell).
2. **T0.2** IPC Unix/named-pipe abstraction with pipe ACL/SID checks.
3. **T0.3** Roots, layout, legacy detection, protected `system`, and no-dot
   policy.
4. **T0.4** Site source mode, path contract, and migration 0007.
5. **T0.5** Complete agent protocol, TLS identity, authenticated client, and
   DPAPI token storage.
6. **T0.6** WSL runner, path validation, source-copy, and localhost-forwarding
   seams.
7. **T0.7** Daemon/CLI transport, lock/readiness, control pipe, and per-user
   startup.
8. **T0.8** Platform capability/process/terminal/performance/doctor/build/
   tooling seams.
9. **T0.9** Backend, port, route, and PHP proxy-path contract.
10. **T0.10** Foundation freeze and lane worktrees.

Follow the plan’s exact RED→GREEN steps. Commit per task. Do not start lane
implementation before T0.10. At T0.10, all five lanes start concurrently from
the frozen foundation. Lane D uses the frozen `agentapi.Module` seam and its
own `internal/agentdocker`/`agentrelay`/`agentweb` packages, so it does not wait
for Lane B’s server implementation; the A→B→C→D→E merge order is only the
module-mounting order.

Start with:

```powershell
# Read the exact T0.1–T0.10 briefs from the plan, then run the first failing test.
go test ./...
```

Use the plan’s commands and expected failures. Do not invent production code
before the failing test.

## 5. Test isolation on one Windows host

Five subagents may edit in parallel, but they cannot all own the real Windows
service, `C:\devstack`, the `devstack` distro, ports 80/443, hosts, trust,
firewall, the WSL ingress, and the Docker engine.

Rules:

- Lane implementers use unit tests, compile checks, and fakes concurrently.
- Real machine-wide resources are serialized by the controller.
- Lane-local smokes may use:
  - `C:\devstack-test\<lane>`
  - unique pipe names
  - `devstack-test-<lane>` distro names
  - high ports
  - no hosts/trust/firewall writes
- Do not install the production helper service or mutate real Windows
  trust/hosts during lane tests.
- Run Windows-tagged tests on the Windows host. Run Linux regression tests in
  WSL ext4 or a Linux checkout at the same commit.
- The controller alone runs production 80/443/hosts/trust/Docker/WSL-ingress
  acceptance.

## 6. After Wave 0: five concurrent lanes

At the frozen foundation commit, the controller creates exactly five lane
worktrees and starts all five implementers concurrently from the same base:

```powershell
git worktree add ..\devstack-lanes\helper -b lane/windows-helper <foundation-sha>
git worktree add ..\devstack-lanes\wsl-agent -b lane/wsl-agent <foundation-sha>
git worktree add ..\devstack-lanes\windows-daemon -b lane/windows-daemon <foundation-sha>
git worktree add ..\devstack-lanes\docker-services -b lane/docker-services <foundation-sha>
git worktree add ..\devstack-lanes\site-source -b lane/site-source <foundation-sha>
```

Use one implementer subagent per lane:

1. Windows helper
2. WSL provisioning/agent/ingress
3. Windows daemon/public proxy
4. Docker services/private web adapter
5. Site source/UI/CLI/doctor

Maximum **5 active subagents** at any time. This is a hard cap, not 3. Do not
dispatch a sixth until one finishes.

Dependency rules:

- Lane A is independent after T0.2/T0.3.
- Lane B owns the agent server and consumes `agentapi.Module` implementations.
- Lane C uses `agentclient`/`agentproto` and the fake agent; it does not wait
  for Lane B’s server to be complete.
- Lane D owns separate `internal/agentdocker`, `internal/agentrelay`, and
  `internal/agentweb` packages implementing the frozen `agentapi.Module`
  seam. It does not edit Lane B’s server and can start concurrently.
- Lane E is independent after T0.4/T0.6.
- Merge order A→B→C→D→E is a module-mounting dependency, not a serialized
  start order. D’s modules are mounted after B’s agent server is merged.

Every implementer must:

- read its own task brief/spec, not the entire session history;
- use TDD and commit each task;
- stay within its file ownership;
- not spawn subagents;
- write a full lane report to a file;
- return status, commit range, tests, and concerns.

After each wave, the controller dispatches one fresh reviewer per lane (still
max 5 total) with:
- the diff package;
- the lane brief;
- the design/plan;
- global constraints and Review Focus.

Critical/Important findings go back to the same implementer for a RED→GREEN
fix. Merge only clean lanes, in dependency order A→B→C→D→E, running focused
tests after each merge.

## 7. Integration gates

After Wave 1 merge, run the vertical slice:

- one static site with source on `C:\devstack\projects`
- one static site with source on `\\wsl.localhost\devstack\...`
- Windows hosts entry, Windows CA trust, helper 80/443, daemon proxy, WSL
  agent, WSL ingress
- open trusted `https://<name>.test` in Edge/Chrome
- run `curl https://<site>.test` and `curl https://devstack.test` from inside
  WSL
- switch a stopped site’s source mode and verify copy/rollback
- verify `devstack doctor` reports localhost forwarding

If either source mode or either Windows/WSL client path fails, do not start
Wave 2.

After Wave 2, run:

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- UI check/unit/Playwright/build
- Linux PHP acceptance
- Linux E2E
- release/installer/workflow tests
- Linux Docker smokes
- Linux `v0.1.0` upgrade test
- fresh Windows install, trust, static/Node/PHP, Docker services, reboot
  recovery, upgrade, uninstall

Do not claim Windows support until the acceptance matrix has evidence.

## 8. Hard rules

- No Docker PHP.
- No native Windows PHP.
- PHP is native Linux FPM inside WSL.
- Docker Engine runs inside WSL; Docker Desktop is unsupported.
- No devstack-created dot-prefixed names on Windows.
- SQLite and CA stay on native OS filesystems.
- Database/Redis data stays ext4.
- Agent token and pinned certificate are required; NAT/loopback only.
- CA private key never enters WSL or Docker.
- Cross-boundary symlinks/junctions are rejected.
- A pre-existing unmarked `devstack` distro is refused, never adopted.
- WSL projects are preserved by default on uninstall.
- Windows helper forwards only to the Windows daemon; never to WSL.
- Windows daemon terminates public TLS; Docker web servers use private HTTP
  ports on Windows.
- No implementation without a failing test first.
- Do not merge a lane while it has open Critical/Important review findings.
- Do not push a shared branch or merge to master without the human’s explicit
  approval.

## 9. If the Windows machine differs from this runbook

Stop and record the difference in the SDD ledger. Likely cases:

- WSL older than 2.0.0 or systemd unavailable: stop; installer validation must
  handle it.
- No administrator access: helper/service/ACL/80/443 acceptance cannot run;
  stop.
- 80/443 already owned: document the owner; public-forwarder tests must use
  isolated high ports or wait.
- No Docker Engine in WSL: stop before service acceptance; Docker Desktop is
  not a substitute.
- Existing `devstack` distro: do not adopt it; choose/document a test distro or
  ask the human.
- Short disk space: WSL/Docker/database data need capacity.
- No `make`: install it or use the PowerShell equivalents; do not skip the
  build/test matrix.
- CRLF in shell scripts: stop and fix the checkout before T0.1.
- Go module proxy failure: fix module access before claiming a baseline.

## 10. First message to send the agent

Use this as the initial prompt:

> Read `docs/superpowers/status/2026-10-26-windows-wsl-handoff.md`,
> `docs/superpowers/status/2026-10-27-windows-plan-review-resolution.md`,
> `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`, and
> `docs/superpowers/plans/2026-10-26-windows-wsl.md`. Then start Wave 0
> implementation on this Windows machine using TDD, beginning with T0.1
> (including the CRLF/toolchain baseline). Do not start Wave 1 until Wave 0 is
> complete and its interfaces are frozen. After Wave 0, use at most five active
> subagents for the five lanes, with isolated worktrees and controller-only
> merges. All five lanes start concurrently from the frozen foundation; Lane D
> uses the frozen agentapi seam and its own agent* packages. Pause only for the
> stop conditions in the plan.

## 11. Pause/checkpoint

The planning phase is revised and complete. Implementation begins at Wave 0 T0.1
only after human approval. The current Linux baseline is `10db2e5`; the planning
documents are on `master` and pushed. The revised plan supersedes the 2026-10-26
version.
