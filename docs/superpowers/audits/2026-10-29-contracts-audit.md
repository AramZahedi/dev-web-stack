# Contracts/Services Audit — 2026-10-29

**Scope:** `internal/store`, `internal/config`, `internal/pathmode`, `internal/model`,
`internal/domain`, `internal/certs`, `internal/hosts`, `internal/services`,
`internal/servicecontrol`, `internal/serviceconfig`, `internal/performance`,
`internal/tooling`, `internal/doctor`, `internal/logs`, `internal/ports`,
`internal/staticserve`, `internal/vhost`, `internal/forwarder`.

**Worktree:** `C:\Users\Aram\.local\share\opencode\worktree\dc072e\audit-contracts`
**Branch:** `audit/contracts-2026-10-29`
**Baseline:** `d9c2c39` (Round-4 remediation HEAD)
**Code commit:** `c0a3414` (`fix(audit): resolve lifecycle deadlocks and contract gaps`)
**Checkpoint commit:** `00ee221` (`wip(audit): checkpoint contracts work before resume`)

No files outside the listed scope were edited. The pre-existing dirty
`test/windows/installer_test.ps1` line-ending change was already present before
this audit and is unrelated; it was carried by the WIP checkpoint, not authored
here.

---

## Verdict

The scoped packages are materially healthier than at `d9c2c39`, but the audit
found **two lifecycle deadlock/lock-order defects and one regression in the
Windows shim path translation that the existing test suite was masking**. They
are fixed and covered by regression tests. Remaining risks are recorded at the
end.

---

## Findings

| ID | Severity | Area | Finding | Status |
|---|---|---|---|---|
| A-1 | Critical | servicecontrol / dockerweb | `Manager` mutex is non-reentrant, but Docker web Commit/Rollback call back into `Manager.Deactivate`/`Activate` while `Manager.Activate` holds it: deadlock on a failed Docker-web activation | Fixed + test |
| A-2 | Critical | servicecontrol / sites | Opposite lock order: `Manager.Activate` holds manager lock then takes `sites.mu` (Docker prepare); `sites.SetServingMode` holds `sites.mu` then calls `Manager.StopForProviderSwitch` which took manager lock. ABBA deadlock | Fixed (provider switch no longer takes manager mutex) |
| A-3 | High | tooling | Windows `.cmd` wrappers did **not** translate raw Windows paths. The test passed only because Git Bash/MSYS rewrote arguments before bash saw them | Fixed + test made representative |
| A-4 | High | tooling | Site env names were rendered unquoted into generated site shims and `shell-init`; a key such as `X; command` became executable shell syntax | Fixed + tests |
| A-5 | Medium | doctor | Wildcard bind can succeed while a specific loopback listener owns the port, so doctor reported healthy bound ports as "free" (Round-1 H4 residual) | Fixed + test |
| A-6 | Medium | doctor | `checkPHPBuilds` accepted a configured WSL distro but never forwarded it, and a visible WSL build missing `bin/php` was silently skipped | Fixed + test |
| A-7 | Medium | performance | `.test` benchmark targets resolved through the hosts file/DNS; a hosts entry could redirect a "loopback only" benchmark to a remote address | Fixed + test |
| A-8 | Medium | store | Concurrent processes could both apply a pending migration after reading the same pending list; `AddEvent` insert + prune were not atomic | Fixed + test |
| A-9 | Medium | serviceconfig | Two concurrent `New` calls could race the legacy manifest rename | Fixed |
| A-10 | Medium | pathmode | Configured WSL distro was interpolated into UNC paths without validation; a `..\`/separator value could escape | Fixed + tests |
| A-11 | Medium | services | Private web targets fell back to fixed ports 41000/43000 for stopped Node/PHP sites, potentially proxying a stopped domain to another site's backend | Fixed + test |
| A-12 | Low | config | No validation for `wsl_distro`, `log_max_mb`, `log_keep`, or `max_request_body_mb`; zero/negative values could select test-only log rotation or overflow request limits | Fixed + tests |
| A-13 | Low | tooling tests | `TestWindowsShimArgumentTranslationInBash` preferred Git Bash even when running under WSL/Linux, making results environment-dependent | Fixed |

---

## A-1 — Re-entrant lifecycle deadlock in the service manager

**Evidence**

`Manager.Activate` held `m.mu` while calling the preparer:

```go
// internal/servicecontrol/manager.go (before fix)
func (m *Manager) Activate(ctx context.Context, id services.ID) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    ...
    if cause := prep.CommitActivate(ctx, svc); cause != nil { ... }
}
```

The Docker web preparer delegates to `dockerweb.Provider.Commit`. On a failed
validate/reload/save it calls `Rollback`, which calls
`p.services.Deactivate` → `WebServiceControl.Deactivate` →
`Manager.Deactivate` → `m.mu.Lock()`. The same goroutine already owns the
mutex, so the activation hangs permanently. `WindowsProvider.Rollback` has the
same shape. The manager's own cleanup path calls `prep.CleanupActivate`, which
reaches the same provider rollback a second time.

**Fix**

- Added a context transition marker (`transitionMarker`) and
  `lockMutation`/`markTransition` helpers in `internal/servicecontrol/manager.go`.
- Lifecycle methods mark their context before invoking any preparer or helper.
  Nested calls that receive that context (Docker web Commit/Rollback) run the
  locked body without re-locking; independent callers still serialize.
- Applied the same wrapper to `Install`, `Activate`, `Deactivate`, `Restart`,
  `Uninstall`, `RotateCredentials`, `Validate`, `Reload`, and
  `RegisterPreparers`.
- `Restart` now enforces exclusive-group/phpMyAdmin dependencies when it is
  restarting a stopped member (an active member may still restart normally).

**Tests**

- `TestActivateAllowsPreparerReentrantDeactivateWithoutDeadlock` models a
  preparer that calls `Manager.Deactivate(ctx, ...)` from `CommitActivate` and
  fails after 3 s if the call deadlocks.
- `TestRestartOfStoppedExclusiveMemberIsRejectedWhilePeerRuns`.
- `TestRestartOfActiveExclusiveMemberStillWorks`.

**Residual**

`RegisterPreparers` is now serialized, but `Credentials`/`List` can still read
the preparer map concurrently with a late `RegisterPreparers` call. Production
wiring registers before serving; keep that ordering.

---

## A-2 — Opposite lock order between service control and site serving

**Evidence**

- `Manager.Activate` holds `manager.mu`, then `WebPreparer.PrepareActivate` →
  `dockerweb.Provider.Prepare` → `sites.Service.StopForDocker`, which takes
  `sites.mu`.
- `sites.Service.SetServingMode` holds `sites.mu`, then
  `switchDockerToBuiltinLocked` calls `DockerSwitch.StopActiveServer` →
  `servicecontrol.WebSwitch` → `Manager.StopForProviderSwitch`, which (after the
  initial exclusivity fix) took `manager.mu`.

A concurrent Docker-web activation and a `serving builtin` switch can therefore
deadlock each other. This is a lock-order defect, not a data race, so the race
detector cannot report it.

**Fix**

`StopForProviderSwitch` / `StartForProviderSwitch` intentionally do **not**
take `manager.mu`. The helper remains the final authority for the container
transition. This removes the `sites.mu → manager.mu` edge while preserving the
manager's exclusive-group critical section for activation.

**Residual / cross-area**

The provider switch is now not serialized against `Activate`; the two can
interleave. A complete fix requires a single lifecycle coordinator (or moving
the provider switch out of the `sites.mu` critical section) in
`internal/sites`/`internal/dockerweb`, both outside this audit's edit scope.
This should be a follow-up in those packages.

---

## A-3 — Windows `.cmd` path translation was broken (false-green test)

**Evidence**

The shim script used:

```bash
a_norm="${a//\\\\//}"          # replaces two consecutive backslashes
[[ "$a_norm" =~ ^//wsl(\\.localhost|\\$)/... ]]
```

For `C:\Projects\app`, `${a//\\\\//}` does not replace the single backslash, so
the `^([a-zA-Z]):/` branch never matched and the Windows path was passed
verbatim to WSL. The same applied to `\\wsl.localhost\...` arguments.

`TestWindowsShimArgumentTranslationInBash` passed on native Windows because it
invoked Git Bash/MSYS, which rewrote `C:\...` to `C:/...`/POSIX form before the
script ran. Running the same test under WSL with Linux bash failed all raw-path
cases. This directly contradicts the Round-4 claim that H5 was fixed.

**Fix**

- Corrected both generated scripts (`EnsureWindowsToolShims` and
  `EnsureWindowsSiteShims`) to replace one backslash and match `\.`/`\$`
  literally (`${a//\\//}`, `^//wsl(\.localhost|\$)/...`).
- Changed the test to execute the generated script from a file with the
  platform-appropriate bash. Under WSL it now uses `/usr/bin/bash`; native
  Windows continues to use Git Bash. This avoids MSYS command-line rewriting
  that does not occur in the real `wsl.exe` invocation.
- Verified the script in both native Windows Git Bash and WSL Linux bash.

**Tests**

`TestWindowsShimArgumentTranslationInBash` passes natively and in WSL. The test
still injects `C:\...`, `\\wsl.localhost\...`, `\\wsl$\...`, `//wsl...`, and
`--opt=...` cases.

**Residual**

The `.cmd` layer's `%*` handling for arguments containing cmd metacharacters
(`&`, `|`, `^`) is still inherently limited. This is a cmd.exe quoting issue,
not a WSL-path issue; no safe general fix short of a helper binary.

---

## A-4 — Site environment names could inject shell syntax

**Evidence**

`tooling.WindowsSiteEnvironment` copied `site.Env` into a map and
`EnsureWindowsSiteShims` wrote each key raw:

```go
baseScript.WriteString(" && export ")
baseScript.WriteString(k)          // unvalidated
baseScript.WriteByte('=')
baseScript.WriteString(posixQuote(env[k]))
```

`ShellExports` similarly wrote `export ` + key. The CLI validates env names, but
the sites API/store does not, and callers can construct the tooling types
directly. A key such as `X; touch /tmp/pwned` becomes shell syntax when the shim
or `shell-init` output is evaluated.

**Fix**

- Added `validateEnvironment` in `internal/tooling/siteenv.go` using the
  existing `envNamePattern`; rejects unsafe names and NUL values.
- `BuildSiteEnv` and `EnsureWindowsSiteShims` now fail closed.
- `ShellExports` skips invalid names as defense in depth.
- Tests: `TestBuildSiteEnvRejectsShellUnsafeEnvironmentNames`,
  `TestShellExportsSkipsInvalidEnvironmentNames`,
  `TestEnsureWindowsSiteShimsRejectsUnsafeEnvironmentNames`.

**Cross-area**

`internal/api` should reject invalid environment names at request validation
time so users get a clear 400 instead of a later shim-generation error. That
package was outside this audit's scope.

---

## A-5 — Doctor reported bound ports as free

**Evidence**

`checkPorts` treated a successful `Ports.Listenable(":port")` as "free". On
Unix, Go sets `SO_REUSEADDR`, so a wildcard bind can succeed while another
process owns `127.0.0.1:port` (for example the helper forwarder on 80/443).
The existing `isPortInUseByDevstack` only ran for permission/address errors,
not the success case. This left the Round-1 H4 false warning in place.

**Fix**

- When the bind probe succeeds, doctor now checks whether the port actually
  responds before calling it free or owned.
- `isPortResponding` probes both `127.0.0.1` and `[::1]`, covering IPv6-only
  and dual-stack listeners.
- Added `Deps.PortResponding` as a test seam; unit tests no longer depend on
  listeners present on the developer machine.
- Tests: `TestPortChecksTreatsSpecificListenerAsOwnedWhenWildcardBindSucceeds`,
  `TestIsPortRespondingChecksIPv6Loopback`.

**Residual**

The check still cannot identify which process owns a port; it only distinguishes
"responding" from "free". A foreign listener on a devstack port is reported as
owned. That is a diagnostic limitation, not a security boundary.

---

## A-6 — Doctor ignored the configured WSL distro and skipped missing WSL binaries

**Evidence**

`checkPHP` computed `distro := s.deps.Config.WSLDistro` and passed it to
`checkPHPBuilds`, but the function called `verifyPHPBuild(build)` and
`checkCompiledExtensionDir(build)` without forwarding it. On a custom distro,
the doctor only tried `\\wsl.localhost\devstack\...`.

Separately, `verifyPHPBuild` skipped a missing `bin/php`/`sbin/php-fpm` whenever
`build.Backend == BackendWSL`, even when the build path was a Windows-visible
directory and the files really were absent.

**Fix**

- Forward `distro...` to both calls.
- `resolvePHPBuildPath` now returns `(path, resolved bool)`.
- A WSL build is only allowed to skip missing files when the path itself was
  not resolvable from Windows; a visible build missing a binary now fails.
- Added `TestDoctorPHPBuildWSLBackendFailsWhenVisibleBinaryIsMissing`.

---

## A-7 — `.test` benchmark targets could be redirected off loopback

**Evidence**

`RunLoad`'s dialer consulted `net.LookupIP` for every `.test` host and dialed
the returned address. `.test` is reserved, but a local hosts entry or a
misconfigured resolver could point it at a remote IP, bypassing the
"loopback benchmark only" validation.

**Fix**

- `isTestHost` recognizes `test` / `*.test` (including trailing dot).
- The dialer always maps those hosts to `127.0.0.1`; it never consults DNS.
- `TestRunLoadSendsTestHostsToLoopback` runs a real `httptest` server behind a
  `bench.test:<port>` URL.

**Residual**

`localhost` is still resolved normally and could be influenced by a hosts file;
the same is true of any tool that uses the platform resolver. Restricting
`localhost` to loopback would be a separate hardening change.

---

## A-8 — Store migration concurrency and event atomicity

**Evidence**

`migrate` lists pending migrations, then `applyMigration` starts a deferred
transaction. Two processes (daemon + CLI, or two daemons) can both list the
same pending version; the second then applies the same DDL or records the
version twice. The store had no cross-process migration serialization.

`AddEvent` inserted the event and pruned old rows in two separate
autocommit statements. If the prune failed, the event was already persisted but
the caller received an error, so a retry could duplicate it.

**Fix**

- DSN now sets `_txlock=immediate`, so `BeginTx` takes the write lock up front
  instead of upgrading a read snapshot.
- `applyMigration` re-checks `schema_migrations` inside the transaction and
  returns success if another process already applied the version.
- `AddEvent` wraps insert + prune in one transaction.
- Added `TestConcurrentApplyMigrationRecordsVersionOnce`, which runs the same
  migration body against two store handles and asserts it ran once.

**Verification**

Store tests pass natively and under WSL, including `-race`.

---

## A-9 — Service-config legacy manifest migration race

`Manager.New` loaded a per-root lock but ran `migrateLegacyManifest` before
taking it. Two concurrent constructors could both see the legacy manifest and
one `os.Rename` would fail. The rename now runs under the shared per-root lock.

---

## A-10 — Pathmode trusted the configured WSL distro

`TranslateSourceForDistro` interpolated `configuredDistro` into a
`\\wsl.localhost\<distro>\...` path without validation. A config value
containing `..` or a separator could escape the distro namespace. `translateWSL`
also accepted a UNC distro segment such as `..`.

Added a shared distro pattern (`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`) and reject
invalid configured/extracted distro names. Tests cover invalid configured
distro and traversal-shaped UNC input.

---

## A-11 — Stopped Node/PHP sites were given fixed backend ports

`AgentTargetSource.PrivateWebTargets` used `41000` for a zero Node port and
`43000` for a zero PHP port. A stopped site has no allocated port, so its
private vhost could proxy to whatever another site later bound on that fixed
port. The target source now skips Node/PHP targets with a zero port; static
targets remain. Added a regression assertion for stopped Node/PHP sites.

---

## A-12 — Config validation gaps

`Config.Validate` accepted an empty/unsafe `wsl_distro`, `log_max_mb = 0`
(the logs package documents zero as a test-only rotate-on-every-write hook),
negative/oversized log/body values. Added validation and table tests in
`TestValidateRuntimeSettings`:

- distro: non-empty, 1-64 chars, safe charset;
- `log_max_mb`: 1..1,048,576;
- `log_keep`: 0..10,000;
- `max_request_body_mb`: 1..1,048,576.

---

## Tests and results

### Native Windows (HEAD `c0a3414`)

| Command | Result |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./...` | PASS (all packages) |
| `go test` on every scoped package | PASS |

### WSL Ubuntu (same worktree)

| Command | Result |
|---|---|
| `go test` on every scoped package | PASS |
| `CGO_ENABLED=1 go test -race` on `servicecontrol`, `store`, `tooling`, `config`, `pathmode`, `performance`, `doctor`, `services`, `serviceconfig`, `logs` | PASS |
| `go test ./...` (full repo) | 4 pre-existing failures outside this scope: `internal/client` (expects `/data/devstack/run/daemon.sock`), `internal/platform` (Windows path join assertion run on Linux), `internal/proxy` (`TestPHPDirectoryRedirectEscapesLocation`), `internal/securestore` (DPAPI test run on Linux). Scoped packages all pass. |

No live upgrade/rollback, uninstall, runtime deletion, or machine-wide
installer test was run. All edits were source-level; no destructive operations.

---

## Commits

| Commit | Meaning |
|---|---|
| `00ee221` | WIP checkpoint preserving the edits made before the session interruption |
| `c0a3414` | Final code fixes and regression tests for this audit |
| (docs commit) | This report |

The branch is not pushed and not merged.

---

## Residual risks / follow-ups

1. **Provider switch vs activation** (A-2): provider-switch methods are
   deliberately outside the manager mutex to avoid the sites/managers ABBA
   deadlock, so they can interleave with activation. A lifecycle coordinator in
   `internal/sites`/`internal/dockerweb` should serialize both directions.
2. **API env validation** (A-4): reject invalid site/build env names at the API
   boundary for a clear 400; tooling now fails closed but late.
3. **Windows hosts rewrite**: `hosts.WriteAtomic` falls back to an in-place
   rewrite only for `EBUSY`. A Windows sharing-violation rename failure
   (`ERROR_SHARING_VIOLATION`/`ACCESS_DENIED`) is not mapped to that fallback.
   Not reproduced live; worth a dedicated Windows hosts test.
4. **Cert manager nil receivers**: `Ensure`, `RenewDue`, and `Delete` lock
   before their nil checks and would panic for a nil receiver. No production
   caller does this; low priority.
5. **Service-config backup ordering**: `LatestBackup` compares formatted paths
   lexicographically; more than nine backups in one second can order `-10`
   before `-9`. Low impact (audit/restore selection only).
6. **Full WSL suite** remains red for the four unrelated packages listed above;
   those should be triaged by their owning areas.
7. **`test/windows/installer_test.ps1`** has a pre-existing line-ending-only
   modification captured by the WIP checkpoint. It is outside this audit's
   scope and was not authored here.
