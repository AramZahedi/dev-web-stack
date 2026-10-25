# Service Platform Wave 0 Review

- **Date:** 2026-10-25
- **Wave 0 merge:** `04d1a2d5f111b33995961a90b19cf8df6abaa489`
- **Handoff commit:** `fce8b32bbb42f259d134a8a4b693eb51829b7b19`
- **CI:** `https://github.com/AramZahedi/dev-web-stack/actions/runs/40200118349` (green)
- **Verdict:** Wave 0 met its approved foundation scope. It is not yet an operable service
  platform; Wave 1 must build the transactional control plane before adding real services.

## Verified evidence

Fresh checks at the handoff commit:

- `go vet ./...` — clean.
- `go test ./... -count=1` — all packages pass.
- `go test ./... -race -count=1` — all packages pass.
- UI `npm ci`, `npm run check`, `npm test -- --run`, `npm run build` — pass.
- `make build` — pass.
- `bash -n` + `shellcheck --severity=error` for acceptance/release/install scripts — pass.
- `git worktree list` — no Wave 0 worktrees remain; working tree clean.
- CI at `fce8b32` — green (CI run 40200118349).

The implementation evidence is credible:

- F1 root paths and secrets mode tests pass.
- F2 catalog/state + migration `0004_services.sql` tests pass.
- F4 manifest/conflict/backup tests pass.
- F3 Compose/helper RPC tests pass.
- F5 site-contract migration `0005_site_contracts.sql` and round-trip tests pass.
- F6 service API/CLI/UI skeleton tests pass.

## What is implemented

- Host layout: `services/`, `projects/`, `secrets/` (0700), `certs/`, `backups/`, `tools/`,
  CLI config directory.
- Service catalog/state types and SQLite service rows.
- Compose rendering for fixed catalog entries (empty catalog in Wave 0).
- Config manifest, atomic write, backup, diff, keep/use/merge/detach.
- Fixed privileged helper service RPCs and a read-only service list API/CLI/UI.
- Site front-controller/build contract fields and migration 0005.
- No service container is installed or activated.

## Review findings and disposition

| ID | Severity | Finding | Disposition |
|---|---|---|---|
| W0-1 | Important | The service catalog is empty and the helper can only pull/start/stop/remove; there is no exclusivity, health wait, port check, rollback, restart, validate, or reload. | Wave 1 P2 |
| W0-2 | Important | `docker compose ps` errors were silently reported as “not installed”, hiding Docker/Compose failures. | Wave 1 P2 |
| W0-3 | Important | `delete_data=true` used an unverified `RemoveAll`; the design requires a verified backup before deletion. | Wave 1 P2 |
| W0-4 | Important | Compose accepted floating tags; production images were not digest-pinned. | Wave 1 P1 |
| W0-5 | Important | The service state model lacked `installed_but_stopped`; the UI could not distinguish installed/stopped from absent. | Wave 1 P1 |
| W0-6 | Important | No manager/API/CLI lifecycle actions or credential reveal existed. | Wave 1 P3 and database lane |
| W0-7 | Important | Wave 0 had no web-server container provider, vhost renderer, `.htaccess`, or front-controller execution. | Wave 1 web lane |
| W0-8 | Important | No global PHP/Composer, shell/exec, or build executor existed. | Wave 1 tooling lane |
| W0-9 | Minor | `Coordinator.Get` compared error strings instead of `services.ErrNotFound`. | Wave 1 P1 |
| W0-10 | Minor | The foundation plan checkboxes/ledger were not updated in git. | Wave 1 docs cleanup |
| W0-11 | Minor | `docs/handoff.md` still described Wave 0 as next and `known-issues.md` K1 was stale. | Wave 1 docs cleanup |
| W0-12 | Minor | Front-controller/SPA files were validated syntactically but not checked for existence under the document root. | Wave 1 web lane |

No open Critical defect was found in Wave 0. The Important items are the expected next
layer, not regressions in the foundation.

## Wave 1 gate

Wave 1 starts from `integration/services-wave-1` and first completes the serial P1/P2/P3
contracts, then runs the database, web-server, and developer-tooling lanes in parallel.
See [the Wave 1 plan](../plans/2026-10-25-service-platform-wave1.md).
