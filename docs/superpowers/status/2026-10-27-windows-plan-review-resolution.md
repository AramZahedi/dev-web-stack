# Windows/WSL Plan Review Resolution — 2026-10-27

- **Status:** Review complete; design and plan revised; implementation not started
- **Inputs reviewed:** Windows design, Windows/WSL implementation plan, planning checkpoint, Windows handoff, next-agent runbook, Linux handoff, known issues, and the current codebase
- **Revised design:** [Windows 11 + WSL2 design](../specs/2026-10-26-windows-wsl-design.md)
- **Revised plan:** [Windows/WSL implementation plan](../plans/2026-10-26-windows-wsl.md)
- **Next step:** human approval of the revised plan, then Wave 0 T0.1
- **Do not claim Windows support** until the Windows acceptance matrix has evidence and the Linux regression gate is green.

## 1. Verdict from the review

The architecture was sound, but the first plan was not executable as written.
The missing pieces were cross-boundary contracts: network topology, backend
selection, port ownership, route targets, PHP file resolution, canonical
storage, source-mode copy/validation, agent protocol, and Windows tooling.
Wave 0 was also far too small for the actual Windows compile/port surface.

This resolution fixes the documents. It does **not** implement Windows
support.

## 2. Blocking findings and resolutions

### B1 — WSL↔Windows `.test` connectivity was contradictory

**Finding:** The design said the agent listens on NAT only, the daemon tries
localhost forwarding first, WSL hosts let curl/Composer resolve `.test`, and
Docker web containers use host networking with private ports. In WSL2 NAT
mode, `127.0.0.1` in WSL is not Windows; builtin mode had no WSL listener, and
the Docker dashboard vhost proxied to `127.0.0.1:7070` inside WSL.

**Resolution:** The Windows helper always forwards 80/443 to the Windows
daemon proxy. The daemon terminates public TLS and routes site hosts to WSL
backends or the active Docker web server on a private HTTP port. The agent
runs a fixed WSL ingress on 80/443 forwarding to the Windows host gateway.
WSL `.test` entries map to `127.0.0.1`. The daemon proxy listens on the WSL
host adapter, firewall-restricted to the WSL subnet. Docker web servers no
longer own public 80/443 on Windows; the dashboard is a daemon route.

### B2 — Backend, port, route, and PHP path contracts were incomplete

**Finding:** `sites.Service` had a single backend, always created
`BackendLocal`, allocated ports on the Windows host, hardcoded `127.0.0.1` in
routes, ignored `PreparedSite.BackendHost`, and used `site.BackendPath` for
PHP file resolution.

**Resolution:** Windows uses one WSL agent backend for all sites. The agent
allocates and validates WSL ports and returns `PreparedSite`. The daemon
persists returned ports and uses `BackendHost` in routes. `sites.Service`
gets a port-provider seam. PHP routes carry a separate Windows-visible
`proxy_document_root`; the agent uses `backend_path`. Linux keeps the local
backend and daemon-side allocator.

### B3 — Wave 0 underestimated the Windows port

**Finding:** 34 files under `internal/` and `cmd/` import Unix-only
`syscall`/`x/sys/unix`; only two files were build-tagged. The helper’s
Linux-only trust/vhost files were untagged. Locking, `/proc`, signals,
process groups, PTY, performance collection, doctor checks, builds, tooling,
and helper startup all needed platform seams.

**Resolution:** Wave 0 now includes T0.1–T0.10 with real seams for toolchain,
IPC/pipe ACLs, roots/layout/dot policy, source mode/path contract, full agent
protocol/TLS, WSL path/copy/validation, daemon lifecycle, platform
capabilities/process/terminal/performance/doctor/build/tooling, and the
backend/port/route contract. Windows and Linux test matrices are explicit.

### B4 — Agent protocol and lane ownership were not frozen

**Finding:** T0.5 defined only health; D1 edited the same `agentproto` file;
Lanes B, C, and D all needed types from each other. Lane D was not independent
of Lane B.

**Resolution:** T0.5 now freezes the complete typed protocol for health,
ports, sites, PHP, builds, terminal, runtimes, services, web targets, hosts,
certificates, path validation, and source copy. T0.5 is the only owner of
`internal/agentproto`. It also freezes `internal/agentapi.Module`
(`Name`, `MountPath`, `Handler`) so Lane D can build the Docker, service-relay,
and web-target modules in separate packages without editing Lane B’s agent
server. **All five Wave 1 lanes still start concurrently.** The A→B→C→D→E
merge order is a module-mounting dependency, not a serialized development
order.

### B5 — Source-mode validation/copy cannot rely on UNC semantics

**Finding:** Windows cannot reliably inspect WSL symlinks, ownership, or
permissions through `\\wsl.localhost`. A UNC copy cannot preserve Linux
ownership/executable bits.

**Resolution:** Validation and copy are agent operations. The agent uses
`lstat`/`readlink`/`realpath`, tar transfers, SHA-256 manifests, file-count
checks, Linux ownership/mode verification, and symlink/junction rejection.
The daemon switches the site row only after verification and keeps the source
by default.

### B6 — Canonical service storage was ambiguous

**Finding:** The design called Windows `services\` “optional canonical
service snapshots” while also saying configs/data/secrets live under
`/data/devstack/services` in WSL. Config editing and credentials needed one
source of truth.

**Resolution:** WSL ext4 is canonical for generated site configs, service
config/data/secrets, runtimes, site/service logs, and Docker data. Windows
keeps `state.db`, control-plane config, leaf certs, daemon logs, cache, and
the VHDX. The daemon proxies service config/content/credentials/log/web
operations to the agent. There is no canonical Windows `sites\`, `services\`,
`secrets\`, `certs\`, or `tools\` tree.

## 3. High-risk findings and resolutions

### H1 — Linux `/data/devstack` change could break existing installs

**Resolution:** Root precedence is explicit: `--home` → `DEVSTACK_HOME` →
detected legacy `~/.devstack` → platform default. Existing helper
`/etc/devstack` and `/var/lib/devstack-helper` layouts are preserved. New
Linux installs use `/data/devstack`; the top-level root is root-owned, the
user-facing subdirectories are user-owned, and `system` is root-only. The
systemd unit and `install.sh` embedded unit must be updated together. Upgrade
from `v0.1.0` is an acceptance gate.

### H2 — No-dot Windows policy was already violated

**Resolution:** All devstack-created internal dot names are renamed and
migrated: `.config-manifest.json` → `config-manifest.json`,
`.serving-provider` → `serving-provider`, `.secret-*`, `.backup-*`,
`.devstack-tool-*`, `.tmp-*`, `.health-*`, `.snapshot-*`, and the config/
credential atomic temp prefixes. Project-owned `.git`, `.idea`, `.env`, and
`.htaccess` remain allowed. T0.3 owns the audit/migration; tests enumerate
created paths.

### H3 — Checkout/tooling was not ready for Windows work

**Resolution:** The next-agent runbook now requires
`core.autocrlf=false`, renormalization, a verified `bash -n`, `make` or
documented PowerShell equivalents, Node 22, working Go module downloads, and
WSL ext4 for Linux tests. The current checkout was made with
`core.autocrlf=true` and its shell scripts contain CRLF; T0.1 must correct
that before any Bash-based task.

### H4 — Rootfs/Docker artifact supply chain was undefined

**Resolution:** The plan now pins the Debian 12 rootfs, Docker
Engine/Compose, agent, and PHP/Node artifacts in
`build/wsl/versions.lock`, publishes them with checksums/SBOMs, verifies them
in the installer and release test, and includes a clean WSL import/Docker
smoke.

### H5 — Security boundaries needed more definition

**Resolution:** The helper and daemon control pipes are ACL-restricted and
peer-SID-checked. The agent uses HTTPS with a pinned self-signed certificate
and a bearer token; token/key material never appears in process arguments.
The Windows helper exposes only CA/trust/hosts/firewall/forwarder/health
operations. The daemon proxy’s WSL-facing listener is firewall-restricted to
the WSL subnet. Helper forwarders remain loopback-only; no helper target
points at WSL.

### H6 — Windows global PHP/Composer/shell/build had no story

**Resolution:** Global `php`/`composer`, `devstack shell`/`exec`, and site
builds run in WSL. Windows gets `C:\devstack\bin\php.cmd` and
`composer.cmd` shims. Dashboard tooling/build/terminal actions route to the
agent.

### H7 — Upgrade/uninstall of the WSL distro was under-specified

**Resolution:** Upgrades export the old distro as a backup, import the new
pinned rootfs into a temporary distro, restore `/data/devstack` as the site
user, verify, swap, and roll back on failure. Uninstall preserves the distro,
projects, and state by default; removal requires a verified export or an
explicit discard confirmation.

### H8 — Acceptance matrix missed the risky paths

**Resolution:** Added Windows and WSL `.test` checks, WSL dashboard access,
source-mode copy/rollback, pipe/token/certificate negatives, localhost
forwarding, WSL IP changes, Docker private-port/dashboard behavior, reboot,
upgrade, uninstall, no-dot audit, and the Linux `v0.1.0` upgrade test.

## 4. Documentation and process fixes

- `README.md` no longer says the service platform is unimplemented; it now
  reflects Waves 0–2 and the next batch, and keeps Windows as planned.
- `docs/requirements.md` no longer lists Windows/WSL as an explicit non-goal;
  R26 remains “Planned — not implemented.”
- `docs/README.md` indexes this resolution and the revised plan.
- `docs/handoff.md` points to this resolution as the current next-step record.
- `docs/known-issues.md` records the Windows plan revision and the remaining
  pre-implementation status.
- `docs/decisions.md` records the resolved Windows/WSL contracts as decisions.
- `docs/architecture.md` keeps Windows as planned but references the revised
  network/storage model.
- `.goreleaser.yaml` no longer claims Windows/WSL is deferred to an obsolete
  “Plan 3.”
- The plan no longer *requires* unavailable `superpowers:*` skills. It uses
  the available OpenCode subagent/TDD workflow and documents the equivalent
  process inline.

## 5. What changed in each document

| Document | Change |
|---|---|
| `docs/superpowers/specs/2026-10-26-windows-wsl-design.md` | Full revision: resolved contracts, network model, canonical storage, source-mode copy, agent TLS, helper surface, tooling, install phases, artifacts, acceptance |
| `docs/superpowers/plans/2026-10-26-windows-wsl.md` | Full revision: T0.1–T0.10, frozen contracts, lane dependency graph, expanded lane tasks, Wave 2 detail, acceptance matrix, pre-implementation checklist |
| `docs/superpowers/status/2026-10-26-windows-planning.md` | Updated status/checkpoint with the revised contracts and pause point |
| `docs/superpowers/status/2026-10-26-windows-next-agent.md` | Updated startup sequence, tooling/CRLF prerequisites, Wave 0 tasks, lane dependency rules |
| `docs/superpowers/status/2026-10-26-windows-wsl-handoff.md` | Updated with the revised decisions and a pointer to this resolution |
| `docs/superpowers/status/2026-10-27-windows-plan-review-resolution.md` | This record |
| `docs/handoff.md` | Current next step points to this record and the revised plan |
| `README.md` | Stale service-platform status corrected; Windows remains planned |
| `docs/requirements.md` | Windows/WSL removed from non-goals; status clarified |
| `docs/README.md` | Index updated |
| `docs/known-issues.md` | Windows plan revision recorded |
| `docs/decisions.md` | Windows/WSL contract decisions added |
| `docs/architecture.md` | Planned Windows section/limitations updated |
| `.goreleaser.yaml` | Stale Windows comment corrected |

## 6. Pre-implementation actions that remain

These are not documentation gaps; they are machine/tooling checks the first
implementation session must perform:

- [ ] `core.autocrlf=false`, `.gitattributes` committed, `git add
      --renormalize .`, `bash -n` passes in Git Bash/WSL.
- [ ] `make` available, or PowerShell equivalents documented and tested.
- [ ] Node 22 selected for the UI/CI baseline.
- [ ] Go module downloads work; `go test ./...` runs on Linux/WSL.
- [ ] WSL 2.0.0+ with systemd and a separate development distro.
- [ ] Docker Engine inside WSL for controller-serialized tests.
- [ ] Administrator access for helper/service/ACL/80/443 tests.
- [ ] Human approval of the revised plan.

## 7. Pause point

No lane worktree, production code, or implementation subagent has started.
The revised plan is the source of truth. Start with **T0.1** after approval,
not Wave 1.
