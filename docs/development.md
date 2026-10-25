# Developer guide

- **Last reviewed:** 2026-10-25
- **Current implementation:** Linux M1
- **Planned work:** [service platform](planned-services.md)

## 1. Prerequisites

- Linux x86_64 with systemd (Fedora 44 is the reference).
- Go 1.26+.
- Node.js 22 and npm 10+ for the dashboard.
- `make`, `bash`, `curl`, `tar`, `sha256sum`.
- `shellcheck` for shell changes.
- Docker Engine + Compose plugin for the future service-container work.
- PHP build dependencies only if building PHP artifacts; see `build/php/README.md`.
- Do not run the build/tests as root.

## 2. First checkout

```sh
git clone git@github.com:AramZahedi/dev-web-stack.git
cd dev-web-stack
make build
./dist/devstack version
```

Read [handoff](handoff.md), [architecture](architecture.md), and the relevant design
before changing behavior.

## 3. Build commands

| Command | Purpose |
|---|---|
| `make build` | Build UI, `dist/devstack`, `dist/devstack-helper` |
| `make ui` | `npm ci && npm run build` |
| `make test` | `go test ./...` |
| `make lint` | golangci-lint |
| `make e2e` | Build + unprivileged Linux E2E |
| `make acceptance` | Full unprivileged M1 acceptance table |
| `make release VERSION=0.2.0` | Local unsigned archive + checksums |
| `make release-test` | Release archive contract test |
| `make php-test` | PHP catalog/smoke/shell checks |

The Go binaries embed `ui/dist`; always build the UI before a Go build that should
include dashboard changes. `make build` handles the order.

## 4. Test commands

Fast loop:

```sh
go test ./internal/... -count=1
(cd ui && npm run check && npm test -- --run)
```

CI-equivalent:

```sh
go vet ./...
go test ./... -race -count=1
(cd ui && npm ci && npm run check && npm test -- --run && npm run build)
```

Focused areas:

```sh
go test ./internal/helper ./internal/vhost -count=1
go test ./internal/proxy ./internal/sites -count=1
go test ./internal/store -run 'Migrate|Backup' -count=1 -v
```

Shell changes:

```sh
bash -n install.sh test/acceptance/m1.sh test/release/*.sh
shellcheck --severity=error install.sh test/acceptance/m1.sh test/release/*.sh
```

Do not treat SKIP/BLOCKED as passes in acceptance output.

## 5. UI development

```sh
cd ui
npm ci
npm run dev
```

Dev mode uses the in-memory mock API unless `VITE_API_MODE=api` is set. With a running
daemon:

```sh
VITE_API_MODE=api DEVSTACK_API_ORIGIN=http://127.0.0.1:7070 npm run dev
```

Production build talks to the real loopback API. The daemon embeds `ui/dist`.

UI tests use Vitest and Playwright. `make acceptance` includes type/unit/build/Playwright
checks; `--skip-playwright` is available for a faster run but is not a pass.

## 6. Code boundaries

- `internal/helper` is the privileged boundary. It must never accept arbitrary commands,
  paths, images, or ports.
- `internal/daemon` wires services; don't put HTTP handlers there.
- `internal/api` owns transport, session/CSRF, and error envelopes.
- `internal/sites` owns site lifecycle and generated PHP/FPM configs.
- `internal/vhost` renders native nginx/Apache config; `internal/helper` installs and
  tests it.
- `internal/proxy` owns host routing, reverse proxy, and FastCGI request semantics.
- `internal/runtime` owns verified downloads/extraction; catalog entries are checksum
  pinned.
- `internal/config` owns layout and machine config.
- `internal/store` owns SQLite and migrations.
- `internal/cli` calls the daemon client; it should not duplicate business logic.
- `ui/` calls the API; mock mode must remain usable for UI development.

## 7. Adding a feature

1. Check the approved design. Do not implement service-platform features before the design
   is approved and planned.
2. Write tests first for behavior changes.
3. Keep the helper surface narrow; add only catalog-defined operations.
4. Add API route + client + CLI command + UI, or explicitly document the missing surface.
5. Update Doctor for new failure modes.
6. Update docs and the design/traceability notes.
7. Run focused tests, then `go test ./... -race -count=1`, UI checks, and acceptance where
   practical.
8. Commit and push; do not leave the only copy on one machine.

## 8. Migrations

- Add a new numbered SQL file in `internal/store/migrations/`.
- Update the migration list/test expectations.
- The upgrade path backs up the database with `VACUUM INTO` before applying migrations.
- Do not edit an existing migration after release; add a new one.
- Test both fresh creation and upgrade from the previous schema.

## 9. Runtime catalog

- Node/PHP entries are embedded in `internal/catalog/catalog.json`.
- Every entry must have a real HTTPS URL, size, and SHA-256 from a published artifact.
- Never invent versions, checksums, or URLs.
- PHP extension manifest is `internal/catalog/php.json`.
- New PHP artifacts require the PHP workflow and a new devstack release to embed the
  catalog entry.

## 10. Release

```sh
make release VERSION=0.2.0
make release-test
```

- Releases are unsigned Linux amd64 archives with `checksums.txt`.
- Tags are `v<major>.<minor>.<patch>`.
- Do not move/reuse a release tag.
- The app release `v0.1.0` and PHP runtime release `php-runtimes-2026-10-25` are published.

## 11. Docker development

The old standalone devstack image was removed in October 2026. Docker Engine is kept for
the planned service-container milestone. Until those Compose service definitions exist,
there is no devstack Docker build/run target; use the native build and install paths.

Do not recreate a devstack application container. The approved architecture is native
devstack plus separate nginx/Apache/database/Redis/phpMyAdmin containers.

## 12. Debugging

```sh
devstack doctor
devstack doctor --json
devstack daemon status
devstack daemon run --log-level debug
journalctl --user -u devstack-daemon -f
sudo journalctl -u devstack-helper -f
devstack site logs <name>
```

Use temporary HOME/state/hosts paths and high ports for tests; do not mutate the developer’s
real `/etc/hosts`, trust stores, or 80/443 in automated tests.

## 13. Commit discipline

- Keep work committed and pushed; the user moves between machines.
- Separate docs/design changes from implementation where practical.
- Run `git diff --check` before committing.
- Do not commit secrets, service data, generated certificates, or runtime archives.
- Keep CI green; docs-only commits still run CI.

## 14. Current constraints to remember

- Native devstack; Docker only for optional services in the planned milestone.
- Linux only. Do not start Windows/WSL Plan 3 without an explicit new instruction.
- Existing native external nginx/Apache mode must remain compatible.
- Do not implement service-platform features from the design until it is approved and
  planned.
