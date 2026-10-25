# Task P3 report — Service manager, API, CLI, and provider config

- Branch: `feat/services-wave1-p3`
- Base: `bbe9ca6` (P1 + P2 contracts)
- Head: `PENDING`
- Commit: `feat: add service manager api and cli`

## Delivered

- `config.Config.ServingProvider` (`serving_provider`, default `native`, enum
  `native|docker`), including legacy-config defaulting and the rule that `docker`
  requires `serving_mode` `nginx` or `apache`.
- `internal/servicecontrol` with `Helper`, `Store`, `Preparer`, `Credentials`,
  `NoopPreparer`, `UninstallResult`, and `Manager` (`List`, `Get`, `Install`,
  `Activate`, `Deactivate`, `Restart`, `Uninstall`, `Logs`, `Credentials`,
  `RotateCredentials`).
- API routes for install/activate/deactivate/restart/uninstall/logs/credentials,
  including CSRF enforcement, `services.ErrNotFound` → 404, invalid helper
  requests → 400, helper RPC failures → 503, and credentials only on explicit
  reveal/rotate.
- `ServiceView` now documents and exposes `health`, `container_id`, and
  `installed`; settings expose `serving_provider`.
- Typed client methods and CLI commands:
  `service install|activate|deactivate|restart|uninstall|logs|credentials`.
  `uninstall --delete-data` and `credentials --rotate` require `--yes` or an
  interactive confirmation.
- `AddSite` now carries `front_controller_file`, `front_controller_mode`,
  `apache_allow_override`, `spa_fallback_file`, and the build profile.
- Daemon wires a real `servicecontrol.Manager` over the helper client and store
  with an empty preparer map (lanes register their preparers later).
- UI types/mock expose the new service runtime fields and settings provider; the
  UI API client normalizes the fields for older daemons.

## TDD red evidence

Focused tests were written before implementation and failed to compile/build:

```text
internal/config/config_test.go:40:9: cfg.ServingProvider undefined (type Config has no field or method ServingProvider)
internal/config/config_test.go:40:28: undefined: ServingProviderNative
...
FAIL github.com/AramZahedi/dev-web-stack/internal/config [build failed]
```

```text
internal/servicecontrol/manager_test.go:200:14: undefined: Credentials
internal/servicecontrol/manager_test.go:252:38: undefined: Store
internal/servicecontrol/manager_test.go:252:97: undefined: Manager
FAIL github.com/AramZahedi/dev-web-stack/internal/servicecontrol [build failed]
```

```text
github.com/AramZahedi/dev-web-stack/internal/servicecontrol: no non-test Go files
FAIL github.com/AramZahedi/dev-web-stack/internal/api [build failed]
FAIL github.com/AramZahedi/dev-web-stack/internal/cli [build failed]
FAIL github.com/AramZahedi/dev-web-stack/internal/client [build failed]
```

The API and OpenAPI tests then exercised the expanded `ServiceService`
interface, CSRF-protected action routes, error mapping, credential isolation,
and OpenAPI route/schema completeness. CLI and client tests failed against the
old fakes/interface until the new typed methods were added.

## Verification

Run in the P3 worktree:

```text
go test ./... -count=1      PASS (all packages)
go vet ./...                PASS
npm run check               PASS (0 errors, 0 warnings)
npm test                    PASS (10 files, 60 tests)
npm run build               PASS
```

The UI tests used the repository's installed `node_modules` through a local,
gitignored symlink; the symlink was removed before commit.

## Decisions

- **Merge precedence:** catalog metadata is authoritative for identity/group;
  live helper status is authoritative for runtime state, container ID, health,
  and installed/active; persisted state fills gaps and retains `last_error`
  and `last_operation`. A helper `Status` failure is returned, never converted
  into an all-not-installed list.
- **Error wrapping:** helper failures are wrapped in
  `servicecontrol.ErrHelperUnavailable` (mapped to 503) except
  `helperproto.ErrInvalidRequest`, which passes through to the existing 400
  mapping.
- **Read-only fallback:** when no lifecycle manager is configured, the API wraps
  the P1 `services.Coordinator` in `readOnlyServiceService`, preserving the M1
  list/get dashboard and returning 503 only for lifecycle actions.
- **Backup path:** the P2 helper RPC returns only an error from `Uninstall`, so
  `UninstallResult.BackupPath` is empty for now. The field and API schema are
  already optional/future-compatible; the helper still owns the backup and
  deletion flow.
- **Secrets:** credentials are returned only from the preparer through explicit
  reveal/rotate routes. `services.Record` stores only lifecycle metadata and
  `last_error`, with a regression test asserting secrets never appear in it.
- **Confirmations:** destructive CLI operations confirm unless `--yes` is set;
  JSON mode keeps prompts on stderr and successful payloads on stdout.
- **UI scope:** no action buttons were added (Wave 2); only types, mock data,
  settings, and defensive API-client normalization were added.

## Concerns

- The helper does not yet expose the backup archive path, so `backup_path` is
  always absent after `--delete-data`. A follow-up helper RPC can populate it
  without changing the API shape.
- Because the manager returns helper status errors, a helper that does not
  implement the P2 service endpoints makes `GET /api/v1/services` return 503
  instead of the old P1 read-only view. This follows the brief ("do not
  silently show everything as not installed") and P2 expected to provide the
  endpoints.
- No live Docker activation smoke was run in P3; lifecycle behavior is covered
  with fake helper/store/preparer tests, and lane tasks own real container
  behavior and preparers.
