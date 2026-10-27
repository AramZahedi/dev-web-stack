# Next-agent startup prompt

Copy the block below into the next agent session. It assumes the repository is
at `C:\Users\Aram\Projects\Go\dev-web-stack` on `feature/windows-wsl`.

```text
You are resuming the Windows/WSL implementation of dev-web-stack. Wave 0 is
complete and frozen; Wave 1 was interrupted mid-flight by provider quota
exhaustion. No lane is finished, reviewed, merged, or accepted, and Windows
support must not be claimed.

START BY READING, IN THIS ORDER:
1. docs/superpowers/status/2026-10-27-windows-implementation-resume.md
   (authoritative resume record; lane-by-lane stopping points and remaining work)
2. docs/superpowers/status/2026-10-27-windows-baseline.md (Wave 0 evidence)
3. docs/superpowers/plans/2026-10-26-windows-wsl.md (Global Constraints,
   Parallel Execution Protocol, Wave 0 T0.1-T0.10, Wave 1 A1-E4)
4. docs/superpowers/specs/2026-10-26-windows-wsl-design.md (binding contracts)
5. docs/development-windows.md (toolchain and CRLF rules)

HARD CONTEXT:
- Main worktree: C:\Users\Aram\Projects\Go\dev-web-stack, branch
  feature/windows-wsl.
- Lane worktrees and branches (do NOT recreate or rebase them):
  C:\Users\Aram\Projects\Go\devstack-lanes\helper            lane/windows-helper, tip 2120b6d
  C:\Users\Aram\Projects\Go\devstack-lanes\wsl-agent         lane/wsl-agent,      tip 83fbfe5
  C:\Users\Aram\Projects\Go\devstack-lanes\windows-daemon    lane/windows-daemon, tip 5391e7a
  C:\Users\Aram\Projects\Go\devstack-lanes\docker-services   lane/docker-services, tip dd02bbb
  C:\Users\Aram\Projects\Go\devstack-lanes\site-source       lane/site-source,    tip 01cc25b
- Every lane already merged feature/windows-wsl through dd7e939.
- All in-flight lane edits were committed as wip: commits. Nothing is lost.
- Windows 10 22H2 (build 19045) x64 and Windows 11 are both required floors.
- The installer must own a complete resumable Windows+WSL prerequisite
  manifest (decision S74). It does not exist yet (Wave 2).
- Windows site backends run only in WSL. Public entry is helper 80/443 ->
  daemon 8080/8443. WSL ingress is fixed 80/443 -> host gateway.
- Do not run/edit in the main worktree for lane code. One implementer per lane.
- Never claim Windows support without the acceptance evidence listed in the
  resume doc section 8.

DO NOT START FROM T0.1. Wave 0 is done. Do not re-dispatch a lane's completed
tasks. Resume exactly where each lane stopped (resume doc section 4):

STEP 1 - FOUNDATION TASK T0.11 (controller, main worktree)
Native Windows `go test ./...` is red in ~21 packages because Linux-only tests
are not partitioned and because of real portability bugs. Do this first:
- Tag genuinely Linux-only tests `//go:build !windows` (permission bits,
  /proc and cgroups, Unix shells, errno strings, reserved-name fixtures).
- Fix real shared bugs: platform-aware POSIX absolute-path validation used by
  internal/vhost and internal/phpfpm; forwarder address-in-use mapping;
  performance DOMAIN\user parsing; doctor `.cmd` shim detection;
  domain.ValidateProjectPath private-dir behavior on Windows; api/openapi.yaml
  source endpoint (coordinate with Lane E, E owns the OpenAPI edit if you
  prefer).
- Commit to feature/windows-wsl, then merge feature/windows-wsl into all five
  lanes.
- Record the new Windows and Linux test counts in the baseline doc.

STEP 2 - CONTINUE THE FIVE LANES CONCURRENTLY (max 5 subagents)
Use one fresh implementer per lane, isolated worktree, TDD, no subagents from
implementers. Continue, do not restart:
- Lane A (helper): finish A4 review/coverage, write .superpowers/lane-report.md.
  Then controller-serialized service/pipe/foreign-SID/80-443 smoke.
- Lane C (daemon) - LARGEST BLOCKER: TestWindowsRunOpensSQLiteWritesLogAndCleans
  Readiness fails because Run hard-requires the helper pipe. Compose the real
  Run: load agent client (currently not called), build a Windows sites.Service
  with backend/wsl + ports agent provider + router + certs + logs, obtain the
  dashboard leaf certificate before serving 8443, call agentclient.InstallCA
  from Backend.InstallCA, order bootstrap helper->agent->proxy->reconcile
  without blocking readiness, implement CLI stop/status/restart over the named
  pipe, and add a green end-to-end readiness test with temp roots/high ports.
  Rewrite the stale lane report.
- Lane D (docker): finish D4, then implement D5 from scratch
  (internal/agentrelay + internal/helper/service_relay_targets_windows.go,
  fixed port allowlist, module mounting, tests). Write the lane report.
- Lane B (agent): write the lane report, prove CA bundle injection reaches
  site/terminal environments, run real Debian 12 WSL acceptance, and prepare
  to mount D modules (do not edit D packages).
- Lane E (source/UI): update api/openapi.yaml for /api/v1/sites/{ref}/source
  and source_mode, make doctor accept Windows .cmd shims, fix/partition
  internal/tooling Windows tests, run the lane Linux regression and Playwright,
  write the lane report.

STEP 3 - REVIEW AND MERGE
- One fresh reviewer per lane; two verdicts (spec compliance, code quality).
  Critical/Important findings go back to the same implementer as RED->GREEN.
- Merge only clean lanes in order A -> B -> C -> D -> E into
  feature/windows-wsl; run focused tests plus the Linux suite after each merge.
- During B/C integration, mount Lane D's agentdocker/agentweb/agentrelay
  modules in cmd/devstack-agent.

STEP 4 - INTEGRATION 1
Run test/windows/vertical_slice.ps1 on a real Windows 10 22H2 machine AND a
real Windows 11 machine: helper/daemon/agent up, one Windows-mode and one
WSL-mode static site, trusted HTTPS from Windows, curl from WSL for both sites
and devstack.test, doctor localhost forwarding. Gate: do not start Wave 2 if
any of these fail.
Then Wave 2, which includes install.ps1 and the full prerequisite manifest.

REPORTING RULES
- Update docs/superpowers/status/2026-10-27-windows-implementation-resume.md
  as you complete each item, and add a short lane report at
  <worktree>/.superpowers/lane-report.md.
- Ledger: .superpowers/sdd/2026-10-26-windows-wsl/progress.md (gitignored).
- Keep commits small and use the plan's commit messages.
- If you must deviate from the plan, record a Ruling with the cost of being
  wrong.
```

## Short version (if the next agent needs only the essentials)

```text
Resume dev-web-stack Windows/WSL from docs/superpowers/status/2026-10-27-windows-implementation-resume.md.
Wave 0 is done and frozen; Wave 1 lanes were interrupted by quota and are NOT done.
Do T0.11 (platform test partitioning + shared portability fixes) first, then continue
lanes A, C, D, B, E from their exact stopping points on branches lane/windows-helper,
lane/windows-daemon, lane/docker-services, lane/wsl-agent, lane/site-source.
C is the largest blocker (daemon Run composition + dashboard TLS + sites.Service).
D5 (service relay) was never started. Do not recreate worktrees, do not redo Wave 0,
do not claim Windows support, and run the Windows 10 + Windows 11 vertical slice before Wave 2.
```
