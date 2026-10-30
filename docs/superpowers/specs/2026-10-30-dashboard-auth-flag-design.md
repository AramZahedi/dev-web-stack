# Dashboard Auth Flag Design

## Intent

Make the local dashboard usable without an install-token URL fragment by default,
while keeping token authentication available as an explicit opt-in.

## Goals

- Add a persisted `dashboard_auth` setting to `config.yaml`.
- Default it to `false`; the daemon then starts the API without an install-token gate.
- Provide `devstack init --dashboard-auth` to enable it, and `--dashboard-auth=false`
  to disable it again.
- `devstack ui` appends `#token=...` only when the setting is enabled.
- Keep the existing `api-token` secret and all auth middleware for enabled installs.

## Non-goals

- No installer prompt, no Settings API/UI toggle.
- No change to host allowlisting, loopback-only API access, or CSRF for mutations.
- No removal or rotation of existing `api-token` secrets.

## Behavior

| `dashboard_auth` | Daemon `AuthToken` | `devstack ui` fragment | API without token |
|------------------|--------------------|------------------------|-------------------|
| `false` (default) | empty | none | allowed (loopback + host guard only) |
| `true` | loaded/created `api-token` | `#token=<hex>` | `403` except `/health` and `/auth` |

## Approaches considered

1. Config flag plus `init --dashboard-auth` — chosen. Small, scriptable, survives
   upgrades, and works on every platform.
2. Installer-only prompt — rejected: Windows-only, and no way to change later.
3. Settings API and dashboard toggle — rejected for this change: larger surface than
   the user asked for.

## Risks

- Existing installs become unlocked after upgrading because the field is absent and
  therefore defaults to `false`. This is the requested behavior.
- Toggling the flag through `init` must restart a running daemon for it to take effect;
  `init` will report the restart requirement.

## Verification

- Unit tests for config round-trip and default.
- Unit tests for daemon token loading with the flag on/off.
- CLI test for `ui` with the flag on/off.
- CLI test for `init --dashboard-auth` persisting and restarting.
- Live check after migration: dashboard opens without a fragment, `/api/v1/sites` returns 200.
