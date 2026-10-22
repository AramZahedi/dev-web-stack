# dev-web-stack — Plan 1 Handoff / Status

- **Date:** 2026-10-22
- **Branch:** `plan-1-foundation`
- **Base commit:** `1b22c43` (plan committed on `master`)
- **Current head:** `5f8451c` (implementation head `56d5248`; `5f8451c` adds this handoff)
- **Remote:** `origin` (`git@github.com:AramZahedi/dev-web-stack.git`), branch pushed and tracking `origin/plan-1-foundation`
- **Worktree used in this session:** `/home/aram/.local/share/opencode/worktree/dc072e/plan-1-foundation`
- **Original checkout:** `/data/wwwroot/development/python/dev-web-stack` (still on `master`)

This document is the recovery map for another agent or developer continuing Plan 1 on a different machine. Do **not** redo Tasks 1–5; their commits exist on `plan-1-foundation`.

## Where to find the plan and spec

- Plan: `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md`
- Spec: `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`
- Plan 1 covers the Linux foundation: static/Node sites, helper, hosts, CA/trust, TLS, proxy, runtime catalog, site lifecycle, API, CLI, dashboard, install/CI.
- PHP is Plan 2. Windows/WSL is Plan 3. Packaging/release is Plan 4.

## Completed tasks (reviewed and committed)

| Task | Commit | Scope | Review |
|---|---|---|---|
| 1 | `3a57470` | Go module, Cobra CLI, `version` command, helper stub, Makefile, CI, README | Approved |
| 2 | `1381efd` | `internal/config` (YAML, `$DEVSTACK_HOME`, paths), `internal/model` (Site/RuntimeBuild/CertRecord/Event) | Approved |
| 3 | `1201645` | `internal/domain` validation, project paths, port allocator | Approved |
| 4 | `0451938` | `internal/store` SQLite + embedded migrations, all Plan 1 tables | Approved |
| 5 | `56d5248` | `internal/logs` rotated files, 5,000-line ring, subscriptions, redaction | Approved |

Verification at `56d5248`:

```
go test ./...    PASS
go build ./...   PASS
go vet ./...     PASS
```

The worktree is clean. Tasks 1–5 each passed a fresh task review; only Minor findings were deferred (see below).

## Next task

**Task 6 — Supervisor, process groups, backoff, and orphan cleanup** in the plan.

- Start from `56d5248`.
- Task brief command:
  `bash <skills>/subagent-driven-development/scripts/task-brief docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md 6`
- Then Task 7 (backend/local), 8 (helper RPC/hosts), 9 (CA/trust), 10 (forwarders), 11 (proxy/static), 12 (certs/SNI), 13 (runtime/Node), 14 (site service), 15 (doctor), 16 (API), 17 (daemon/client), 18 (CLI), 19 (UI), 20 (install/CI), 21 (acceptance).
- Task 6’s BASE is `56d5248`.

## How to resume with Subagent-Driven Development

1. `git fetch origin && git switch plan-1-foundation` (the branch is already pushed; `5f8451c` includes this document).
2. Verify Go 1.26+ is installed (`go version`). No cgo is used; `modernc.org/sqlite` is pure Go.
3. Run `go test ./...` to confirm the baseline.
4. Load `superpowers:subagent-driven-development`.
5. Create a fresh SDD workspace/ledger for the plan:
   `bash <skills>/subagent-driven-development/scripts/sdd-workspace docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-foundation.md`
6. In the new ledger, mark Tasks 1–5 complete using the commits above, and record that this handoff document is the prior-session record. The old ledger was local scratch and is **not** in git.
7. Dispatch Task 6 with a fresh implementer, then a fresh task reviewer, exactly as the skill requires.
8. Keep one commit per task and do not proceed past a failed review gate.

The old local ledger (for reference only on this machine):
`/home/aram/.local/share/opencode/worktree/dc072e/plan-1-foundation/.superpowers/sdd/2026-10-22-dev-web-stack-plan-1-foundation/progress.md`

## Environment notes

- **Go was missing on the original machine.** The Task 1 implementer installed Go 1.27.3 system-wide at `/usr/local/go` (checksum-verified; `dl.google.com` mirror). This is outside the repo and not captured in git. On the laptop, install Go 1.26+ normally; 1.27.3 is fine.
- The worktree was initially created with a detached HEAD; a branch `plan-1-foundation` was created at the reviewed commit before continuing.
- `golangci-lint` is not installed locally; `go vet` and `gofmt` were used. CI defines linting.
- The repository’s `.gitignore` ignores `dist/`, `ui/dist/*` (except `.gitkeep`), `*.db`, `.idea/`, and the `.superpowers/` scratch workspace.
- No Docker/Podman, no PHP, no Windows/WSL implementation exists yet.

## Rulings made during this session

These were controller decisions; carry them forward unless the spec says otherwise.

1. **`ValidateDomain` vs `ValidateSiteDomain`:** `ValidateDomain` accepts any valid `*.test` name including `devstack.test`; `ValidateSiteDomain` rejects `devstack.test` so users cannot create a site that shadows the dashboard. Hosts sync uses the former, site creation the latter.
2. **CA key custody:** the root CA private key lives in the helper (root/SYSTEM), not the daemon. The helper signs only `.test`/`devstack.test` CSRs. This prevents a same-user process from getting an arbitrary root trusted.
3. **Static serving:** static sites run a supervised `devstack serve-static` subprocess rather than being served in-process, matching the future WSL backend and exercising the same lifecycle.
4. **Dashboard hosts entry:** every site-service hosts sync includes `devstack.test`; otherwise creating/deleting a site would remove the dashboard entry.
5. **Branch:** the worktree was detached; `plan-1-foundation` was created at `3a57470` and all later work landed there.

## Deferred Minor findings (do not block Plan 1, but triage at the final review)

- Task 1: Makefile dirty-version inconsistency; CI `go build` does not set `CGO_ENABLED=0` (module has no cgo).
- Task 2: `RuntimeBuild.Kind` is untyped; `Event.SiteID` uses `omitempty` while `CertRecord.SiteID` does not; `go.yaml.in/yaml/v3` import path is non-canonical; no parent-directory fsync after config rename; size fields not validated positive.
- Task 3: `NewPortAllocator` silently drops out-of-range seed ports while `Reserve` rejects them; non-ASCII label errors print a raw byte; bind-probe TOCTOU is inherent and deferred to the supervisor.
- Task 4: callers must set `CreatedAt`/`UpdatedAt` (Task 14 must do this); `store.Open` assumes the parent directory exists; migration check uses `COUNT(*)`.
- Task 5: redaction over-matches words containing key/token/secret; rotation collision ordering; `Tail` returns nil vs empty inconsistently. **`Tail` is ring-only by design**, so log history is empty after a daemon restart until new output arrives. Rehydrating disk history is a downstream concern; consider it for the final review or a later task.

## Important interfaces already established

- `internal/model`: `Site`, `RuntimeBuild`, `CertRecord`, `Event`; constants `SiteStatic/PHP/Node`, `DesiredRunning/Stopped`, `Status*`.
- `internal/config`: `Load(home)`, `Config` defaults/validation, `NewPaths(home)`, `Paths.Ensure()`.
- `internal/domain`: `NormalizeName`, `DomainFor`, `ValidateDomain`, `ValidateSiteDomain`, `ValidateProjectPath`, `NewPortAllocator`, `Allocate(HTTP|FastCGI)`, `Reserve`, `Release`.
- `internal/store`: `Open(path)`, site CRUD, `ListCerts`, runtime CRUD, settings, events, `ErrNotFound`.
- `internal/logs`: `New(dir,maxMB,keep)`, `Writer(siteID,source)`, `Tail`, `Subscribe`, `Close`, `Redact`; `maxMB=0` is the test rotation hook.

## Immediate next step

Push `plan-1-foundation` to origin (done by the controller as part of this handoff if the push succeeds), then resume at **Task 6** using the plan and the SDD process. Do not start PHP or Windows work until Plan 1 is accepted.

## Addendum 2026-10-22 — external vhost scope extension

Plan 1 has been extended with helper-managed native nginx/Apache vhosts. The task
numbering after Task 20 shifts: the old Task 21 acceptance pass is now **Task 26**, and
Tasks 21–25 cover the vhost renderers, helper manager/RPC, daemon/API integration, CLI +
Doctor, and UI/docs/E2E.

- New spec: `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`.
- M1 spec updated: external serving is now in scope; the "Apache/nginx configuration
  generation — not planned" non-goal was removed.
- Plan 1 header, Global Constraints, Review Focus, acceptance, and plan series updated.
- PHP vhost templates and FastCGI wiring belong to Plan 2 (FPM lands there).
- Task 6 (supervisor) is complete on this branch at `2adb116` after review and one fix round.
- The dashboard UI was built ahead of Task 19 and committed at `1d7a2bd`; its task review is
  still pending, and Task 25 will add the Serving screen and vhost preview.

Resume the plan at **Task 7**. Do not implement vhost code before the spec is reviewed.
