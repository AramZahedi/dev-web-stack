# Backend / WSL Agent Audit — 2026-10-29

**Branch:** `audit/backend-2026-10-29`
**Base:** `d9c2c39` (`fix(windows): close round 4 audit findings and harden WSL lifecycle`)
**Fix commit:** `f79b2df` (`fix(wsl): harden agent runtime, build, terminal, and source-copy paths`)
**Scope:** `internal/agent/**`, `internal/agentclient/**`, `internal/agentproto/**`,
`internal/backend/**`, `internal/build/**`, `internal/terminal/**`, `internal/sites/**`,
`internal/runtime/**`, `internal/wsl/**` (copy/provision/path validation only),
`internal/phpfpm/**`, `internal/fcgi/**`.
**Out of scope (not edited):** `internal/api/**`, `internal/daemon/**`, `internal/helper/**`,
`internal/cli/**`, `ui/**`, installer scripts.
**Method:** source review of the high-risk paths, targeted temporary probes, regression tests
written before/with each fix, and fresh gate runs on native Windows and in WSL2 Ubuntu
(ext4 via `/mnt/c`). No destructive operation was run against the live `/data/devstack`.

---

## Verdict

No new release-blocking defect was found in the scoped packages — the Round-4 Criticals
(`R4-C1`, `R4-C2`) are already fixed at the base commit. This pass found **6 High**,
**11 Medium**, and **4 Low** defects in the Windows/WSL backend. The most consequential are:

- runtime installs that **reported success for a runtime that was never downloaded**
  (including every Node build), and
- a PHP document-root check that **allowed a symlinked parent directory to escape the project**.

Both are fixed with regression tests. The remaining findings are bounded-output, timeout,
process-cleanup, concurrency, and fail-closed hardening. The agent still runs builds, PHP,
and terminals as **root**; that privilege-model issue (`R2-M12`/`R4-H1`) is unchanged and is
called out as a cross-area residual.

---

## Findings

Severity follows the existing audit convention: High = security boundary, data loss, or a
primary feature that silently does the wrong thing; Medium = bounded DoS, race, or
misleading failure; Low = hardening/consistency.

| ID | Severity | Area | One-line |
|---|---|---|---|
| BA-1 | High | agent runtime | `InstallRuntime` swallowed download/checksum errors and never staged Node builds |
| BA-2 | High | agent PHP | document root escaped through a symlinked intermediate directory |
| BA-3 | High | wsl copy | source copy could recurse into its own staging tree and could delete a concurrently published destination |
| BA-4 | High | agentclient | one 30 s client timeout truncated builds, runtime installs, copies, and followed logs |
| BA-5 | High | agent ingress | `Stop`/`Refresh` hung on idle keep-alive relays |
| BA-6 | High | agent FPM | a failed master start was masked by a stale listener on the same port |
| BA-7 | Medium | agent build | unbounded stdout/stderr captured in the root agent's memory |
| BA-8 | Medium | agent build | client-supplied timeouts were unbounded; only the shell was killed on timeout |
| BA-9 | Medium | agent terminal | environment error caused a nil-map panic after the WebSocket upgrade |
| BA-10 | Medium | agent terminal | ticket map grew without bound; PTY dimensions were not validated |
| BA-11 | Medium | agent static | failed shutdown left a phantom server that blocked a real restart |
| BA-12 | Medium | build service | on-disk build log was unbounded; concurrent builds raced snapshot/restore |
| BA-13 | Medium | wsl provision | disk-space probe ran before the import parent existed |
| BA-14 | Medium | wsl provision | relative `InstallDir` was accepted |
| BA-15 | Medium | agent runtime | archive extraction had no member validation and hashed by reading the whole archive |
| BA-16 | Medium | wsl runner | `wsl.exe` calls without a deadline could hang forever |
| BA-17 | Medium | agent runtime | install and remove could race on the same runtime directory |
| BA-18 | Low | agent runtime | `RemoveRuntime` forgets the row before deleting files (pre-existing) |
| BA-19 | Low | path translation | `toWSLPath`/`toPOSIXPath` drop any distro name, not only the managed one |
| BA-20 | Low | runtime manager | `InstallFile` hashes a path and later extracts the same path (TOCTOU) |
| BA-21 | Low | release/test hygiene | a pre-existing whitespace-only change in `test/windows/installer_test.ps1` |

---

### BA-1 — `InstallRuntime` failed open and never installed Node builds

**Evidence.** `internal/agent/php.go` (base):

```go
if _, err := os.Stat(phpBin); errors.Is(err, os.ErrNotExist) {
    _ = downloadAndExtractArchive(ctx, match.URL, match.SHA256, targetDir)
}
if _, err := os.Stat(phpBin); err == nil {
    if err := CheckDebian12PHPCompatibility(ctx, targetDir); err != nil { ... }
}
```

- A failed download, checksum mismatch, or extraction error was discarded. The subsequent
  `Stat` failed, the compatibility gate was skipped, and a `RuntimeResponse` was returned
  with `Path: targetDir` for a directory that does not contain the runtime.
- The download path existed **only for `match.Kind == "php"`**. A Node build was registered
  by creating an empty `<root>/runtimes/node@<version>` directory and returning success.
  `internal/sites/service.go` calls `EnsureRuntime(ctx, "node", …)` on site creation, so a
  Windows/WSL Node site was created against a runtime with no `bin/node`.

**Impact.** The dashboard/CLI reports a usable runtime and later builds fail with
`Node … has no bin/node`, or PHP-FPM cannot start. On the agent this is indistinguishable
from a real install because the map row is persisted.

**Fix (`f79b2df`).**
- Every download error now fails the install.
- Node builds are staged with the same pinned-archive flow as PHP.
- A build is only registered after its kind's binary (`bin/php`, `bin/node`) exists and is
  executable; unknown kinds are rejected.
- The PHP compatibility gate still runs when `bin/php` is present.
- `runtimeMu` serialises install/remove (BA-17).

**Tests.** `TestAgentInstallRuntimeCatalogValidationAndListing` now stages a runnable build so
it never downloads the real 242 MB archive; `TestAgentInstallRuntimeFailsClosedWhenTheBuildIsMissing`
sets `DEVSTACK_NO_DOWNLOAD=1` and asserts an error instead of a phantom registration.

---

### BA-2 — PHP document root escaped through a symlinked parent

**Evidence.** `resolvePHPDocumentRoot` (base) only inspected the **final** component:

```go
rel, err := filepath.Rel(projectRoot, resolved)
if err != nil || strings.HasPrefix(rel, "..") { ... }
info, err := os.Lstat(resolved)
if info.Mode()&os.ModeSymlink != 0 { ... }
```

For `DocumentRoot: "linked/sub"` where `linked` is a symlink to a directory outside the
project, `Lstat` sees a real directory (`sub`) and the containment check is lexical, so the
resolved root is accepted. The static path (`resolveStaticDocumentRoot`) already rejected
every symlink component; the PHP path did not. `strings.HasPrefix(rel, "..")` also rejected a
legitimate child directory named `..cache`.

**Impact.** A PHP site can be configured with a document root outside its project (for
example a symlink to `/etc` or another user's tree). PHP-FPM `chdir`s there and serves from
it. This is a document-root escape, not just a validation inconsistency.

**Fix.** Both static and PHP sites now use one `resolveDocumentRoot` helper (`internal/agent/static.go`)
that requires an absolute project path and calls `rejectSymlinkComponents` on every existing
component of the project and document root. The `Rel` check compares `..` exactly.

**Tests.** `TestResolvePHPDocumentRootRejectsSymlinkedIntermediateDirectory`,
`TestResolvePHPDocumentRootRequiresAbsoluteBackendPath`,
`TestResolvePHPDocumentRootAllowsSubdirectoryNamedWithDots` (Linux; symlink tests skip on
Windows without the privilege).

---

### BA-3 — `CopySource` could copy a tree into itself and could delete a fresh destination

**Evidence.** `internal/wsl/wsl.go` (base) created its staging directory beside the
destination and then walked the source:

```go
temp, _ := os.MkdirTemp(parent, filepath.Base(dst)+".copy-")
filepath.WalkDir(src, func(path string, ...) { ... })
...
os.RemoveAll(dst)
os.Rename(temp, dst)
```

- With `dst` inside `src` (for example a source-mode destination the user picks under the
  project), `parent == src`; the staging directory is created **inside the source before the
  walk**, so `WalkDir` recurses into its own partial output. That is a runaway copy until
  disk exhaustion.
- With `dst` equal to or containing `src`, the publish step deletes the source tree.
- Two concurrent copies both passed `ensureEmptyDestination`; the second `RemoveAll(dst)`
  silently deleted the first published copy.
- The "verification" compared only file count and total bytes; a same-size corrupted copy
  passed.

**Fix.**
- Canonicalise both paths, then reject equal or nested trees
  (`ErrOverlappingCopy`).
- Take an exclusive `.name.copy.lock` beside the destination (`ErrDestinationInUse`).
- Re-check that the destination is still empty immediately before publication.
- Rehash every staged file with the same manifest encoding used during the copy and compare
  the digest to the copied manifest.
- Create staging directories with `mode|0700` and apply the source mode after the walk
  (a `0555` source directory previously made child creation fail for non-root callers).

**Tests.** `TestCopySourceRejectsOverlappingTrees`, `TestCopySourceRefusesAnInProgressDestination`,
`TestCopySourceManifestIsStableAndVerifiesContent` (plus the existing Local/symlink tests).

---

### BA-4 — The agent client's 30-second timeout truncated long operations and log streams

**Evidence.** `internal/agentclient/client.go` (base) built a single client:

```go
timeout := cfg.Timeout
if timeout <= 0 { timeout = 30 * time.Second }
...
http: &http.Client{Transport: transport, Timeout: timeout}
```

The daemon constructs the client with no `Timeout`
(`internal/daemon/windows_bootstrap.go`), so **every** agent call was capped at 30 s — and
`http.Client.Timeout` covers reading the response body, not just the headers. Consequences:

- `InstallRuntime` (download + checksum + extract of a 50–240 MB build) aborted mid-download;
  the agent then cancelled the install with the request context, leaving the runtime absent.
- `BuildSite` (up to 5 min) and `CopySource` (large trees) were cut at 30 s.
- `SiteLogs(follow=true)` ended after 30 s, so `devstack logs -f` and the dashboard log view
  stopped silently.

**Fix.** `Config.LongTimeout` (default 30 min) and three clients over one transport:
`Timeout` for ordinary calls, `LongTimeout` for `BuildSite`, `ExecSite`, `InstallRuntime`,
`CopySource`, and a no-total-timeout client for streamed responses (bounded by the request
context). `Close` closes idle connections on all three.

**Tests.** `TestLongRunningOperationsUseLongTimeout` (25 ms short timeout, 150 ms handler,
3 s long timeout) and `TestFollowedLogStreamIsNotCutByTheClientTimeout`.

---

### BA-5 — Ingress `Stop`/`Refresh` hung on idle relays

**Evidence.** `internal/agent/ingress.go` relays with two blocking `io.Copy` calls. `Stop`
cancels the run context and closes listeners, then `wg.Wait()`:

```go
_, _ = io.Copy(client, upstream)
...
select { case <-done: case <-ctx.Done(): }
```

An idle HTTP keep-alive connection never returns from `io.Copy`, so `wg.Wait()` blocked
indefinitely. `Refresh` has the same wait. On a gateway change or agent shutdown, this pins
the caller (and delays service restart).

**Fix.** Each relay now starts a watcher that closes both connections when the run context
is cancelled, then stops when the relay returns.

**Test.** `TestIngressStopClosesIdleRelays` uses a `net.Pipe` upstream that never sends;
`Stop` must return within 3 s.

---

### BA-6 — A failed FPM start could be masked by a stale listener

**Evidence.** `startFPM` decided readiness by dialling `127.0.0.1:<port>`:

```go
c, err := net.DialTimeout("tcp", target, 50*time.Millisecond)
if err == nil { _ = c.Close(); close(ready); return }
```

If the port was still owned by a previous master, or by any other process, the dial succeeded
even when the new master failed to bind and exited. `StartPHP` recorded the site as running
with a dead process. The agent's `PortAllocator` reuses a persisted allocation without
probing it, so this was reachable after a crash/restart.

**Fix.** `startFPM` refuses to start when `portAvailable(port)` is false (the allocator's own
loopback bind probe), and after a successful dial it still requires `proc.running()`. The
readiness wait also returns as soon as the child exits. In addition, `killFPMProcess` now
requires `cmd.Process.Pid == pid` and re-checks the `done` channel before each of
SIGQUIT/SIGTERM/SIGKILL, narrowing the PID/PGID-reuse window described in R4-M5.

**Test.** `TestStartFPMRejectsAStaleListenerWhenTheMasterExits` (Ubuntu): a fixture
`sbin/php-fpm` exits 1 while another listener owns the port; `startFPM` must fail. The test
failed before the fix (it returned a process) and passes now.

---

### BA-7 / BA-8 — Build/exec output, timeout, and process-tree bounds

**Evidence.** `BuildSite` and `ExecSite` wrote stdout/stderr into `bytes.Buffer` with no
limit, accepted `TimeoutMS` verbatim (an arbitrarily large value kept the process alive for
years), and on timeout `exec.CommandContext` killed only `/bin/sh`. npm/pnpm/vite children
survived the timeout and kept the project directory busy.

**Fix.** `cappedBuffer` keeps 8 MiB per stream and appends an explicit truncation marker;
`commandTimeout` clamps every client timeout to 1 hour; `configureBuildCmd` puts the command
in its own process group and kills the group on cancellation (Windows keeps the default
process-local cancel). The same process-group rule is applied to the local build service.

**Tests.** `TestCappedBufferBoundsCapturedOutput`, `TestCommandTimeoutClampsToCeiling`,
`TestExecSiteTruncatesRunawayOutput` (20 MB of `/dev/zero`), and
`TestExecSiteKillsGrandchildrenOnTimeout` (a background `sleep` must be gone after the
timeout). The latter is `//go:build !windows`.

---

### BA-9 / BA-10 — Terminal handler robustness

**Evidence.** `HandleTerminalWebSocket` did `caEnv, _ := r.Environment(envMap)` and then
assigned `caEnv["TERM"]`. `Environment` returns `(nil, error)` when the CA bundle is missing,
unreadable, or not a regular file; assigning into a nil map panics **after** the WebSocket
has been hijacked. The ticket map only pruned on consume, and PTY cols/rows were cast to
`uint16` without a bound.

**Fix.** Handle the environment error and close the socket with `StatusInternalError`;
`terminalShell()` falls back to `/bin/sh`; clamp ticket and resize dimensions to 1000;
prune expired tickets on every issue.

**Tests.** `TestTerminalEnvironmentFailureClosesInsteadOfPanicking` (a directory at
`certs/ca-bundle.crt`; the old code panics), `TestIssueTerminalTicketClampsDimensionsAndPrunesExpired`,
`TestTerminalConsumesTicketOnlyOnce`.

---

### BA-11 — `StopSite` left a phantom server after a failed shutdown

`StopSite` called `server.Close()` on a `Shutdown` error but never cleared `site.server`
or persisted `status: stopped`. The next `StartSite` saw a non-nil server and returned
`{"status":"running"}` without listening. The error path now clears the pointer, records
`stopped`, and saves state.

**Test.** `TestStopSiteWithExpiredContextClearsTheServerAndRestarts` holds a partial HTTP
request open, cancels the shutdown context, then verifies a real restart serves traffic.

---

### BA-12 — Build log and concurrent-build bounds

`internal/build` described its log as "bounded" but only bounded **reads**; the file written
during a build was unlimited. Two concurrent builds of one site also shared the same output
directory, so one build's snapshot/restore could delete the other's output.

**Fix.** `cappedLogWriter` caps the on-disk log at 16 MiB with a marker; `Service.lockSite`
serialises builds per site. `configureBuildCommand` kills the process group on cancellation.

**Tests.** `TestCappedLogWriterBoundsTheBuildLog` plus the existing build suite (Linux).

---

### BA-13 / BA-14 — WSL provisioning preflight

`Provision` called `verifyRootFS` (which calls `FreeDiskBytes(filepath.Dir(InstallDir))`)
**before** creating the import parent. On Windows, `GetDiskFreeSpaceEx` on a missing path
returns `ERROR_PATH_NOT_FOUND`, so a clean `C:\devstack\wsl` failed the disk check before
`wsl --import`. `validate()` also accepted a relative `InstallDir`.

**Fix.** Create the import parent first; require an absolute `InstallDir`.

**Tests.** `TestProvisionCreatesImportParentBeforeDiskCheck` (the fake disk probe asserts the
directory exists) and `TestProvisionRequiresAbsoluteInstallDirectory`.

---

### BA-15 — Agent runtime archive validation and memory use

The agent downloaded with `curl` (no timeouts), read the entire archive with `os.ReadFile`
to hash it, and handed the path to `tar` without inspecting members. For the shipped PHP
builds this allocates ~242 MB in the root agent, and tar's traversal/symlink protections
depend on the tar implementation and member order.

**Fix.**
- Stream the checksum (`io.Copy` into SHA-256).
- `curl --connect-timeout 20 --max-time 900 --retry 2`.
- `validateRuntimeArchive` walks the gzip/tar stream and rejects absolute names, `..`
  escapes, backslashes/NUL, non-regular/dir/symlink types, hardlinks, and symlinks whose
  lexical target leaves the archive root; it bounds entries (200 000) and expanded bytes
  (2 GiB).
- `--no-same-owner`; after extraction, `verifyExtractedLinks` re-checks every produced link
  against the staging root.

**Tests.** `TestDownloadAndExtractArchiveRejectsUnsafeMembers` (parent traversal, absolute
path, escaping/absolute symlink, hardlink), `TestValidateRuntimeArchiveAllowsContainedRelativeSymlink`
(node-style `bin/npm -> ../lib/...`).

---

### BA-16 — `wsl.ExecRunner` had no deadline

`ExecRunner.Run` used the caller's context verbatim. A wedged `wsl.exe` (distro boot, 9P
stall, service restart) pinned the calling goroutine forever. It now applies a 3-minute
deadline **only when the caller supplied none**, and reports `ctx.Err()` distinctly.

---

### BA-17 / BA-18 — Runtime mutation races and removal ordering

`InstallRuntime` and `RemoveRuntime` could interleave: a removal could delete a build while
its archive was being extracted into the same directory. Both now hold `Runtime.runtimeMu`
(lock order `runtimeMu → mu`). `RemoveRuntime` still deletes the map row before the
filesystem; if `os.RemoveAll` fails the files are unreachable and the row is gone. This is
pre-existing and left unchanged (it would require a two-phase delete); it is listed under
residual risks.

---

### BA-19 / BA-20 / BA-21 — Low

- **BA-19:** `toWSLPath` (agent backend) and `toPOSIXPath` (agent FPM) map
  `\\wsl.localhost\<any-distro>\x` to `/x` for the managed distro. A path from another
  distro is silently reinterpreted. Validation lives in `internal/pathmode`; the mapping
  helper has no distro parameter. Residual (no production path has been shown to pass a
  foreign distro).
- **BA-20:** `internal/runtime.Manager.installEntry` verifies a caller-supplied `InstallFile`
  by path and later re-opens the same path for extraction. A local writer could swap the
  file between the two steps. The manager's own download path is a single hashed stream and
  is not affected. Recommended follow-up: copy the source into the private cache while
  hashing and extract the copy.
- **BA-21:** `test/windows/installer_test.ps1` arrived in this worktree with a pre-existing,
  whitespace-only working-tree change (stray `\r` removal inside a multi-line regex). The
  initial checkpoint commit captured it; the fix commit's tree was corrected so the branch
  **does not modify** that file. The worktree still shows the inherited change.

---

## Tests and gates actually run

All runs are fresh for this branch. Native Windows = `go1.27.0 windows/amd64`;
WSL = Ubuntu on `go1.26.8 linux/amd64`.

| Gate | Result |
|---|---|
| `go build ./...` (Windows) | **PASS** (exit 0) |
| `go vet ./...` (Windows) | **PASS** (exit 0) |
| `go test ./... -count=1` (Windows) | **PASS**, 59 packages ok, 0 failures |
| WSL `go build ./...` | **PASS** |
| WSL `go test ./internal/agent ./internal/agentclient ./internal/build ./internal/wsl ./internal/sites ./internal/terminal ./internal/fcgi ./internal/phpfpm ./internal/backend/...` | **PASS** (sites 67 s; all others < 4 s) |
| WSL `go test -race ./internal/agent ./internal/agentclient ./internal/build ./internal/wsl ./internal/terminal` | **PASS**, no data races |
| `gofmt` on changed packages | clean (pre-existing `internal/agent/tls.go` remains unformatted; untouched) |

New regression tests added by this audit:

```text
internal/agent/terminal_test.go
  TestIssueTerminalTicketClampsDimensionsAndPrunesExpired
  TestTerminalEnvironmentFailureClosesInsteadOfPanicking
  TestTerminalConsumesTicketOnlyOnce
internal/agent/build_test.go
  TestResolvePHPDocumentRootRejectsSymlinkedIntermediateDirectory
  TestResolvePHPDocumentRootRequiresAbsoluteBackendPath
  TestResolvePHPDocumentRootAllowsSubdirectoryNamedWithDots
  TestCappedBufferBoundsCapturedOutput
  TestCommandTimeoutClampsToCeiling
  TestExecSiteTruncatesRunawayOutput
  TestStartFPMRejectsAStaleListenerWhenTheMasterExits
internal/agent/process_tree_unix_test.go
  TestExecSiteKillsGrandchildrenOnTimeout
internal/agent/ingress_test.go
  TestIngressStopClosesIdleRelays
internal/agent/runtime_test.go
  TestStopSiteWithExpiredContextClearsTheServerAndRestarts
  TestAgentInstallRuntimeFailsClosedWhenTheBuildIsMissing
internal/agent/runtime_download_test.go
  TestDownloadAndExtractArchiveRejectsUnsafeMembers
  TestValidateRuntimeArchiveAllowsContainedRelativeSymlink
internal/agentclient/client_test.go
  TestLongRunningOperationsUseLongTimeout
  TestFollowedLogStreamIsNotCutByTheClientTimeout
internal/build/build_test.go
  TestCappedLogWriterBoundsTheBuildLog
internal/wsl/wsl_test.go
  TestCopySourceRejectsOverlappingTrees
  TestCopySourceRefusesAnInProgressDestination
  TestCopySourceManifestIsStableAndVerifiesContent
internal/wsl/provision_test.go
  TestProvisionCreatesImportParentBeforeDiskCheck
  TestProvisionRequiresAbsoluteInstallDirectory
```

Tests that could only run on Unix (symlinks, `/bin/sh`, process groups) were executed in WSL
and passed; the Windows run skips them explicitly.

---

## Commits

| Commit | Contents |
|---|---|
| `f79b2df` | All production fixes and regression tests listed above (25 files, in-scope only) |
| *(docs commit)* | This document |

The branch is not pushed and not merged.

---

## Residual risks (not fixed here)

1. **Root execution model (`R2-M12` / `R4-H1`).** The agent still runs as root and executes
   PHP, builds, and terminals as root. This audit bounds output, process trees, and paths but
   does not introduce a non-root user or a caller-identity boundary inside the distro. Owner:
   API/daemon + installer.
2. **Orphan FPM on a reused port.** BA-6 now fails closed when the FastCGI port is already
   bound, but the agent does not reap an orphaned master it did not start (for example after
   an abnormal agent death). The operator must stop the orphan; a future fix should read the
   generation's PID file and verify the process identity before signalling it.
3. **`RemoveRuntime` ordering.** Files are deleted before the store row; a failed `RemoveAll`
   leaves an unreachable tree. A two-phase (rename → delete → row) flow would be safer.
4. **`InstallFile` TOCTOU (BA-20).** Path-based verification followed by path-based
   extraction; the download path is unaffected.
5. **Foreign-distro path translation (BA-19).** `\\wsl.localhost\<other>\…` is mapped onto the
   managed distro without a distro check.
6. **PHP `extension_dir` on WSL.** `internal/sites/php_config.go` still assumes the flat
   `lib/php/extensions` layout for WSL builds (the shipped runtime matches). A future runtime
   with an API subdirectory would need the agent to report the detected directory (`R2-M10`).
7. **Tar dependency.** The agent still extracts with the system `tar`; the new Go-side
   pre-scan and post-extraction link check bound the risk, but a Go-native extractor would
   remove the dependency on tar's own semantics.
8. **Windows daemon E2E.** The agent-side terminal/build/runtime endpoints are now safer, but
   the Windows daemon's bridge (`R4-H2`) and the dashboard's E2E coverage are owned by
   `internal/daemon`/`internal/api`. The vertical slice should still exercise a real terminal,
   a real static build, and a real runtime install end to end.
9. **`go test -race` on native Windows** remains unavailable (requires CGO); race coverage is
   WSL-only, as in previous rounds.

---

## Cross-area issues for the owning agents

- **`internal/api`:** runtime IDs are now validated again at the agent boundary, and the
  agent deletes only the registered path, so `R4-C2` has defense in depth. The API should
  still reject `..`/separators and unknown IDs early, and `CheckRuntimeRemoval` should stay
  in the request path.
- **`internal/daemon` (Windows):** the default 30 s agent timeout was a daemon-visible
  functional break (BA-4). If the daemon supplies its own `agentclient.Config.Timeout`, it
  should leave `LongTimeout` unset (30 min default) or set it explicitly; do not set a short
  `LongTimeout`.
- **`internal/helper` / installer:** provisioning is performed by `install.ps1`, not the Go
  `Provisioner`; the BA-13 fix matters when the Go path is used by tests/tools. No installer
  change was made.
- **Release/test hygiene:** the inherited whitespace change in
  `test/windows/installer_test.ps1` should be committed or discarded by its owner; it is not
  part of this branch's tree.

---

## File-by-file change summary

| File | Change |
|---|---|
| `internal/agent/php.go` | fail-closed runtime installs, Node downloads, binary checks, install/remove lock, archive validation, streamed checksum, curl timeouts, shared docroot resolver |
| `internal/agent/static.go` | shared `resolveDocumentRoot`, HTTP-port guard, phantom-server cleanup |
| `internal/agent/fpm.go` | port pre-check, readiness requires a live master, exit detection |
| `internal/agent/process_unix.go` | process-group build cancel, PID/PGID-reuse re-checks for FPM |
| `internal/agent/process_windows.go` | Windows build-cancel stub |
| `internal/agent/build.go` | capped output, timeout clamp, env-error propagation, process-group cancel |
| `internal/agent/terminal.go` | env-error handling, shell fallback, dimension clamp, ticket pruning |
| `internal/agent/ingress.go` | close idle relays on context cancellation |
| `internal/agent/runtime.go` | `runtimeMu` field |
| `internal/agentclient/client.go` | long-operation and streaming clients, `LongTimeout` |
| `internal/build/build.go` | bounded log, per-site build lock, process-group cancel |
| `internal/build/exec_{unix,windows}.go` | platform process-group configuration |
| `internal/wsl/wsl.go` | overlap rejection, destination lock, re-verified manifest, dir modes, `ExecRunner` deadline |
| `internal/wsl/provision.go` | import-parent ordering, absolute install dir |
