# Service Platform Wave 2 Handoff

- **Date:** 2026-10-25
- **Integration branch:** `integration/services-wave-2`
- **Merge to master:** `ec2dca4523bf40d931d3e674001d6e832e5e1903`
- **CI run:** `https://github.com/AramZahedi/dev-web-stack/actions/runs/40220968186` (green)
- **Wave 2 status:** phpMyAdmin, credential rotation, dashboard parity, and the pure
  performance engine are implemented and tested. The remaining Wave 2/3 items are listed
  below; they are not claimed as shipped.

## Delivered

### phpMyAdmin and credentials
- phpMyAdmin 5.2.3, digest-pinned, loopback `127.0.0.1:8082`, cookie authentication.
- phpMyAdmin follows the active MySQL/MariaDB engine through the fixed `db` Compose alias;
  activation without an active MySQL family is rejected with a structured conflict.
- MySQL/MariaDB switch restarts an active phpMyAdmin.
- Credential rotation for MySQL/MariaDB/PostgreSQL through the helper:
  - old password read from host-owned 0600 secrets;
  - `ALTER USER`/`ALTER ROLE` SQL sent on stdin, never argv/logs/Compose;
  - old password reapplied on a failed update;
  - owner-preserving secret replacement (installing user, 0600);
  - active phpMyAdmin restarted, with a recorded warning if only the restart fails.
- Live Docker smoke covers phpMyAdmin 200, MySQL→MariaDB switch, old/new login checks,
  and MySQL/MariaDB/PostgreSQL rotation.

### Dashboard
- Grouped services page: Web servers, MySQL family, Standalone.
- Install/activate/deactivate/restart/uninstall, keep-data default, typed-id delete-data
  confirmation, logs, credential reveal, rotation warnings, conditional phpMyAdmin link.
- Site create/edit exposes front-controller file/mode, Apache override, SPA fallback file,
  and build profile (Node/command/output/env); SiteDetail runs builds and shows status,
  duration, output, restore/snapshot state, and log tail.
- Settings: global PHP default/status/clear, global ini editor, Composer tool management.
- Degraded site status; no optimistic action state; API conflicts shown verbatim.
- Service config and performance panels are explicitly labelled as pending backends, not
  fake controls.

### Performance engine
- Pure `internal/performance` package with bounded profiles for nginx, Apache MPM event,
  PHP-FPM global child/memory budget, OPcache/php.ini fragments, host sysctl/limits plans,
  deterministic includes, apply/reset plans, and wrk/hey benchmark plan.
- No host writes, no invented RPS values, no Docker socket.
- Unit/race tests at ~88% coverage. Helper/API/UI application wiring remains.

## Verification

- `go test ./... -count=1` and `go test ./... -race -count=1` — pass.
- `go vet ./...`, `go build ./...` — clean.
- UI `npm run check` 0 errors/0 warnings, 103 unit tests, production build, 25 Playwright
  tests — pass.
- `bash test/release/installer_test.sh` — pass.
- `sg docker -c 'DEVSTACK_DOCKER_TESTS=1 bash test/docker/wave2-phpmyadmin.sh'` — pass.
- Scoped shell syntax/shellcheck — pass.

## Remaining (not shipped)

1. Wire `internal/performance` to helper apply/reset/host tuning, active-server includes,
   PHP pool/ini merge, benchmark execution/result storage, API/CLI, and dashboard.
2. ImageMagick CLI tool management (no selected pinned image yet).
3. Service config editor/diff/apply in the dashboard.
4. Site-scoped terminal/command runner.
5. External-site bind mounts for Docker web servers; sites outside `projects/` currently
   use builtin/native serving.
6. Full Wave 3 acceptance/host benchmark evidence.

## Review trail

- Wave 0 review: [Wave 0 review](2026-10-25-wave0-review.md).
- Wave 1 handoff: [Wave 1 handoff](2026-10-25-wave1-handoff.md).
- Wave 1 plan: [Wave 1 plan](../plans/2026-10-25-service-platform-wave1.md).
- Design: [service platform design](../specs/2026-10-25-service-containers-and-performance-design.md).
- Lane reviews and fix rounds are recorded in the SDD workspace; phpMyAdmin/rotation and
  performance received independent reviews with fix rounds.
