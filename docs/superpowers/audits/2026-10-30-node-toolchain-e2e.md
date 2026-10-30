# Node Toolchain 100% Live E2E Report

- **Date:** 2026-10-30
- **Tree:** `master` plus the change set containing this report
- **Platform:** Windows 11 host, managed WSL2 Debian 12 distro `devstack`,
  live `DevstackHelper` service, daemon home `C:\Users\Aram\.devstack`
- **Dashboard:** `http://127.0.0.1:7070` and `https://devstack.test`
- **Node source:** live `https://nodejs.org/dist/index.json` with official
  per-release `SHASUMS256.txt`

This run used freshly built `devstack.exe`, `devstack-helper.exe`, and the Linux
`devstack-agent` from the tree. The live helper, daemon, WSL agent, hosts block,
and browser trust were upgraded through the supported install/init path. All
temporary sites were removed and both temporary runtimes were cleaned up after
the run.

## Gate results

| Gate | Command | Result |
|---|---|---|
| Formatting | `gofmt -l .` | PASS (no output) |
| Build | `go build ./...` | PASS |
| Vet | `go vet ./...` | PASS |
| Native tests | `go test ./...` | PASS |
| UI tests | `npm --prefix ui test -- --run` | PASS, 16 files / 161 tests |
| UI types | `npm --prefix ui run check` | PASS, 0 errors / 0 warnings |
| UI build | `npm --prefix ui run build` | PASS |
| Windows installer | `pwsh -NoProfile -File test/windows/installer_test.ps1` | PASS, 241 checks |
| WSL agent live | `devstack doctor` after agent upgrade | PASS |
| Race suite | `CGO_ENABLED=1 go test -race -p 1 ./...` | Not run: no host gcc/cgo; WSL has no Go toolchain |
| Windows lifecycle | `pwsh -NoProfile -File test/windows/lifecycle_test.ps1` | PASS, 41 tests |
| Windows release | `pwsh -NoProfile -File test/release/release_windows_ps_test.ps1` | PASS, 41 checks |
| Windows/WSL vertical slice | `pwsh -NoProfile -File test/windows/vertical_slice.ps1` with `DEVSTACK_HOME` | PASS, 100% green (static Windows/WSL, PHP-FPM, HTTPS, shims, doctor) |

## CLI coverage

| Surface | Actions exercised | Result |
|---|---|---|
| `devstack version` | host build reports `dev`, protocol 1 | PASS |
| `node default --status` | fallback, configured, source, resolved build, shim state | PASS |
| `node default 24.22.1` | exact set, helper wrapper install, `node.cmd -v` | PASS |
| `node default 22.11.0` | older exact set, `node.cmd`/`npm.cmd` version | PASS |
| `node default 22` | line resolution to newest installed 22.23.4 | PASS |
| `node default --clear` | fallback to newest installed 24.22.1 | PASS |
| `node default 99.99` | 409 `RUNTIME_MISSING`, not 500 | PASS after fix |
| `runtime available --kind node` | live source, newest patch per major | PASS |
| `runtime available --major 22 --all` | older minors/patches disclosed | PASS |
| `runtime available --offline` | cache/embedded fallback | PASS |
| `runtime available --json` | full snapshot JSON | PASS |
| `runtime available --kind php` | PHP entries separated | PASS |
| `runtime available --refresh --offline` | 400 `INVALID_FLAGS` | PASS |
| `runtime install node@22.11.0` | live preflight, checksum, WSL agent install | PASS after agent upgrade |
| `runtime install node@20.20.2` | older major install (also via dashboard) | PASS |
| `runtime remove node@22.11.0` while pinned | 409 `RUNTIME_IN_USE` naming sites | PASS after fix |
| `runtime remove node@22.11.0` after site removal | removed | PASS |
| `site add --kind static --node 22.11.0` | created and served | PASS |
| `site add --kind php --php 8.4 --node 22.11.0 --source-mode windows` | created, PHP 8.4.27 served | PASS |
| `site add --kind node --node 22.11.0 --command "node server.js"` | created with a discovery-installed build | PASS after fix |
| `site node <static> 24.22.1` | exact pin, shell changed | PASS |
| `site node <static> --clear` | inherit global, shell changed | PASS |
| `site node <php> 22.23.4` / `--clear` | per-site pin/inherit | PASS |
| `site node <node> --clear` | 400 `NODE_VERSION_REQUIRED` | PASS |
| `site node <php> 99.99` | 409 `RUNTIME_MISSING` with install-first remediation | PASS |
| `site exec -- node -v` / `npm -v` | static, PHP, and Node sites all resolve the exact build | PASS |
| `shell-init` | exports `DEVSTACK_NODE_VERSION` and exact runtime PATH | PASS |
| `tool remove/install node-default` | wrappers removed then restored; `node.cmd -v` | PASS |
| `doctor` | all checks pass after cleanup | PASS |

## Dashboard coverage

Every action below was exercised against the live daemon with Playwright.

| Surface | Actions exercised | Result |
|---|---|---|
| Auth | token bootstrap, session cookie, dashboard load | PASS |
| Sites list | search, Running/Stopped/Crashed filters, refresh, theme toggle | PASS |
| Add Site — validation | empty form rejected name/folder inline | PASS |
| Add Site — static | 3-step wizard, Node inherit label, options, create | PASS |
| Add Site — Node | Node kind, Windows source mode, exact Node 22.11.0, command, create | PASS after source-mode fix |
| Site Detail | status, stop, start, restart, copy URL, open site, logs pause/resume/auto-scroll/clear/download | PASS |
| Site Detail — terminal | start, `node -v`, `npm -v`, close | PASS |
| Site Detail — vhost | preview nginx/Apache, close | PASS |
| Site Detail — configuration | pin 24.22.1, pin 20.20.2, inherit 24.22.1, save | PASS |
| Site Detail — build | add build profile with Node 22.11.0, run build, exit 0, output version | PASS |
| Site Detail — source | stop guard, Change source available only when stopped | PASS |
| Site Detail — delete | typed-name confirmation, site removed, project retained | PASS |
| Runtimes | installed Node/PHP groups, 24 Node major rows, expand/collapse, refresh | PASS |
| Runtimes — install dialog | lazy checksum/size, security warning, cancel, install Node 20.20.2 | PASS |
| Runtimes — removal | confirm dialog, remove unused Node 20.20.2 | PASS |
| Settings | Node default fallback, set 22.11.0, clear to newest 24.22.1, managed shim state, install-more link | PASS |
| Doctor | checks-pass state, Run checks | PASS |
| Services | page load, Reload, Preview, profile/benchmark controls present | PASS |
| Services — Redis lifecycle | install, activate, logs, configuration, restart, deactivate, uninstall with permanent data deletion | PASS |
| Events/SSE | live logs and refresh after mutations | PASS |
| Error envelope | invalid Node default, deletion 404 after removal, source validation | PASS |
| Console | fresh tab across Sites, Runtimes, and Settings | PASS, 0 errors / 0 warnings |

## Bugs found and fixed during E2E

1. **Node default errors returned `500 INTERNAL`.** Invalid or missing defaults
   now classify as `409 RUNTIME_MISSING` and malformed values as
   `400 INVALID_REQUEST` (`internal/api/server.go`, API tests).
2. **Removing a Node runtime returned `PHP_VERSION_IN_USE`.** Node builds now
   return `409 RUNTIME_IN_USE` with the owning sites; PHP keeps its existing
   code (`internal/api/server.go`, API tests).
3. **Node site creation rejected discovery-installed builds.** `sites.Create`
   still called the embedded catalog installer for every Node site, so
   `node@22.11.0` (installed from live discovery) was reported missing. The
   obsolete catalog install call was removed; creation now validates the
   installed pin (`internal/sites/service.go`, regression test).
4. **`site add` lost install-first after the installed-only change.** Node add
   now resolves installed builds first, then live discovery/preflight, then the
   embedded catalog fallback. It still stores only the exact installed version
   (`internal/cli/site.go`, CLI tests).
5. **Add Site changed Node source mode to WSL while keeping a Windows path.**
   Selecting Node after typing a `C:\...` path left `source_mode=wsl`, and the
   error only appeared on Create. Source mode is now derived from the entered
   path and step 1 validates path/mode compatibility
   (`ui/src/routes/AddSite.svelte`, UI test).
6. **WSL agent upgrade is installer-owned.** `devstack init` refreshes
   credentials/hosts/trust but does not replace a running old agent binary. The
   installer-equivalent stop/copy/start flow was used; documented in
   [Known issues](../../known-issues.md).
7. **Windows PHP/Composer shims used ambient WSL `php`.** The vertical slice
   caught `php.cmd`/`composer.cmd` failing after a stale PHP 8.3 symlink was
   left in `/usr/local/bin/php`. The global shims now pass the resolved PHP
   build path, set the exact extension directory, and Composer executes its
   PHAR with that exact PHP binary (`internal/tooling/windows.go`,
   `internal/tooling/service.go`, tests).
8. **Vertical-slice harness assumed `C:\devstack` and loopback dashboard access
   from WSL.** The script now honors `DEVSTACK_HOME`, uses `system/secrets` for
   agent credentials, and asserts the intentional 403 loopback boundary instead
   of expecting dashboard access from the WSL network namespace
   (`test/windows/vertical_slice.ps1`).
9. **Tooling tests overwrote the live WSL Composer PHAR.** `InstallTool`
   called `ProvisionWSLComposer` with a fixture PHAR from a temporary test home,
   writing `composer fixture` into `/data/devstack/tools/composer/composer.phar`.
   Composer provisioning is now an injectable `Deps` seam and tooling tests use a
   no-op; the real PHAR is preserved (`internal/tooling/service.go`,
   `internal/tooling/service_test.go`).

## Cleanup and final state

- All E2E sites removed; `devstack site list` is empty.
- Temporary `node@22.11.0` and `node@20.20.2` removed.
- Installed: `node@22.23.4`, `node@24.22.1`, `php@8.4.27-r1`.
- Global default: `24.22.1` (explicit), wrappers installed.
- `devstack doctor`: all checks pass.
- Project files under `.devstack/projects/e2e-*` were retained by design; no
  project source was modified by devstack.

## Caveats

- The native Windows suite and live WSL agent were exercised. A WSL-side Go
  race suite could not run because the distro has no Go toolchain.
- Host cgo/race support is unavailable, so the native race suite was not run.
- Windows lifecycle (41 tests), release (41 checks), and the full Windows/WSL
  vertical slice passed in the final gate.
- The dashboard remains intentionally unreachable from the WSL network
  namespace; WSL site traffic and trusted TLS still pass, and the dashboard
  loopback boundary returns 403 as designed.
- The IPv6 doctor probe is skipped on this host because Windows forbids
  `::1` loopback dials (the listener itself is accepted); the explicit
  `tcp4`/`tcp6` probe remains in production code.
- Windows PHP/Composer wrappers now pin the exact resolved PHP build; the WSL
  `/usr/local/bin/php` legacy symlink is not used by the Windows wrappers.
