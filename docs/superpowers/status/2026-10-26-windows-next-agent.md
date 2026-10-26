# Next Agent on Windows — Start Here

- **Date:** 2026-10-26
- **Purpose:** operational runbook for the first AI-agent session on the Windows machine
- **Status:** implementation has not started; begin with Wave 0 after reviewing the plan
- **Design:** [Windows 11 + WSL2 design](../specs/2026-10-26-windows-wsl-design.md)
- **Plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Knowledge handoff:** [Windows/WSL knowledge handoff](2026-10-26-windows-wsl-handoff.md)

## 1. First: verify the machine and checkout

Required:

- Windows 11 22H2 or newer, x86_64.
- WSL2 2.0.0 or newer with systemd support.
- Virtualization enabled.
- Administrator access for helper/service/ACL tests.
- Go 1.26+.
- Node.js 22 and npm 10+.
- Git for Windows, PowerShell 7.
- Docker Engine inside WSL, **not Docker Desktop**, for acceptance.
- 16 GB RAM recommended; at least 50 GB free.

Use a short repository path. Do not clone into `C:\devstack`; that is runtime state.

```powershell
git config --global core.autocrlf false
git clone git@github.com:AramZahedi/dev-web-stack.git C:\src\dev-web-stack
cd C:\src\dev-web-stack
git switch master
git pull --ff-only
git rev-parse HEAD
```

Record the commit SHA. Read, in order:

1. `docs/superpowers/status/2026-10-26-windows-wsl-handoff.md`
2. `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`
3. `docs/superpowers/plans/2026-10-26-windows-wsl.md`
4. `docs/handoff.md`
5. `docs/known-issues.md`

Do not re-open settled architecture questions unless the design is contradicted by the machine.

## 2. WSL prerequisites

Check:

```powershell
wsl --version
wsl --status
wsl -l -v
```

If WSL is missing or older than 2.0.0, stop and ask the human to run the required `wsl --install`/`wsl --update` and reboot. Do not silently enable Windows features or reboot.

Use a separate development distro such as `Ubuntu` for Linux tests. The managed `devstack` distro will be created by the implementation. Do not name the development distro `devstack`; the provisioner treats an unmarked `devstack` distro as a collision and refuses it.

Docker Engine must run inside a Linux WSL distro for the managed acceptance path. Docker Desktop is not the supported substrate.

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
```

Do not treat pre-foundation Windows compile failures as a defect; Wave 0 introduces the Windows seams.

## 4. Start Wave 0 only

Create the integration branch from the planning commit:

```powershell
git switch -c feature/windows-wsl
```

Wave 0 is sequential and comes first:

1. T0.1 repository hygiene and Windows build targets
2. T0.2 IPC Unix/named-pipe abstraction
3. T0.3 `/data/devstack` and `C:\devstack` roots/path contract
4. T0.4 site source mode and migration 0007
5. T0.5 agent protocol, authenticated client, DPAPI token storage
6. T0.6 WSL runner/path seam
7. T0.7 daemon/CLI transport selection
8. T0.8 platform capabilities/process seams
9. T0.9 foundation freeze and lane worktrees

Follow the plan’s exact RED→GREEN steps. Use `superpowers:test-driven-development` for every production change. Commit per task. Do not start lane implementation before T0.9.

Start with:

```powershell
# Read the exact T0.1–T0.9 briefs from the plan, then run the first failing test.
go test ./...
```

Use the plan’s commands and expected failures. Do not invent production code before the failing test.

## 5. Test isolation on one Windows host

Five subagents may edit in parallel, but they cannot all own the real Windows service, `C:\devstack`, `devstack` distro, ports 80/443, hosts, trust store, firewall, and Docker engine.

Rules:

- Lane implementers use unit tests, compile checks, and fakes concurrently.
- Real machine-wide resources are serialized by the controller.
- Lane-local smokes may use:
  - `C:\devstack-test\<lane>`
  - unique pipe names
  - `devstack-test-<lane>` distro names
  - high ports
  - no hosts/trust/firewall writes
- Do not install the production helper service or mutate real Windows trust/hosts during lane tests.
- Run Windows-tagged tests on the Windows host. Run Linux regression tests in WSL or a Linux checkout at the same commit.
- The controller alone runs production 80/443/hosts/trust/Docker acceptance.

## 6. After Wave 0: five parallel lanes

At the frozen foundation commit, the controller creates exactly five lane worktrees:

```powershell
git worktree add ..\devstack-lanes\helper -b lane/windows-helper <foundation-sha>
git worktree add ..\devstack-lanes\wsl-agent -b lane/wsl-agent <foundation-sha>
git worktree add ..\devstack-lanes\windows-daemon -b lane/windows-daemon <foundation-sha>
git worktree add ..\devstack-lanes\docker-services -b lane/docker-services <foundation-sha>
git worktree add ..\devstack-lanes\site-source -b lane/site-source <foundation-sha>
```

Use one implementer subagent per lane:

1. Windows helper
2. WSL provisioning/agent
3. Windows daemon/public proxy
4. Docker services
5. Site source/UI/CLI

Maximum **5 active subagents** at any time. This is a hard cap, not 3. Do not dispatch a sixth until one finishes.

Every implementer must:

- read its own task brief/spec, not the entire session history;
- use TDD and commit each task;
- stay within its file ownership;
- not spawn subagents;
- write a full lane report to a file;
- return status, commit range, tests, and concerns.

After each wave, the controller dispatches one fresh reviewer per lane (still max 5 total) with:
- the diff package;
- the lane brief;
- the design/plan;
- global constraints.

Critical/Important findings go back to the same implementer for a RED→GREEN fix. Merge only clean lanes, in dependency order A→B→C→D→E, running focused tests after each merge.

## 7. Integration gates

After Wave 1 merge, run the vertical slice:

- one static site with source on `C:\devstack\projects`
- one static site with source on `\\wsl.localhost\devstack\...`
- Windows hosts entry, Windows CA trust, helper 80/443, daemon proxy, WSL agent
- open trusted `https://<name>.test` in Edge/Chrome

If either mode fails, do not start Wave 2.

After Wave 2, run:

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- UI check/unit/Playwright/build
- Linux PHP acceptance
- Linux E2E
- release/installer/workflow tests
- Linux Docker smokes
- fresh Windows install, trust, static/Node/PHP, Docker services, reboot recovery, upgrade, uninstall

Do not claim Windows support until the acceptance matrix has evidence.

## 8. Hard rules

- No Docker PHP.
- No native Windows PHP.
- PHP is native Linux FPM inside WSL.
- Docker Engine runs inside WSL; Docker Desktop is unsupported.
- No devstack-created dot-prefixed names on Windows.
- SQLite and CA stay on native OS filesystems.
- Database/Redis data stays ext4.
- Agent token required; NAT only.
- CA private key never enters WSL or Docker.
- Cross-boundary symlinks/junctions are rejected.
- A pre-existing unmarked `devstack` distro is refused, never adopted.
- WSL projects are preserved by default on uninstall.
- No implementation without a failing test first.
- Do not merge a lane while it has open Critical/Important review findings.
- Do not push a shared branch or merge to master without the human’s explicit approval.

## 9. If the Windows machine differs from this runbook

Stop and record the difference in the SDD ledger. Likely cases:

- WSL older than 2.0.0 or systemd unavailable: stop; installer validation must handle it.
- No administrator access: helper/service/ACL/80/443 acceptance cannot run; stop.
- 80/443 already owned: document the owner; public-forwarder tests must use isolated high ports or wait.
- No Docker Engine in WSL: stop before service acceptance; Docker Desktop is not a substitute.
- Existing `devstack` distro: do not adopt it; choose/document a test distro or ask the human.
- Short disk space: WSL/Docker/database data need capacity.

## 10. First message to send the agent

Use this as the initial prompt:

> Read `docs/superpowers/status/2026-10-26-windows-wsl-handoff.md`, `docs/superpowers/specs/2026-10-26-windows-wsl-design.md`, and `docs/superpowers/plans/2026-10-26-windows-wsl.md`. Then start Wave 0 implementation on this Windows machine using TDD. Do not start Wave 1 until Wave 0 is complete and its interfaces are frozen. After Wave 0, use at most five active subagents for the five parallel lanes, with isolated worktrees and controller-only merges. Pause only for the stop conditions in the plan.

## 11. Pause/checkpoint

The planning phase is complete. Implementation begins at Wave 0 only after human approval. The current Linux baseline is `10db2e5`; the planning documents are on `master` and pushed.
