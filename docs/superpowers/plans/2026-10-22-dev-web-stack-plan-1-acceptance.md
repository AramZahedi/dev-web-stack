# Plan 1 Acceptance Report

- **Date:** 2026-10-23
- **Source revision tested:** `e88231fbb1d7f359c6bc59724c3624264fbb36e4`
  (`test: allocate unique ports in daemon tests`)
- **Plan:** [Plan 1 — Foundation](2026-10-22-dev-web-stack-plan-1-foundation.md)
- **Scope:** the Plan 1 subset of M1 design §21 plus the external-vhosts
  acceptance requirements
- **Overall result:** **automated PASS; manual checks BLOCKED/UNVERIFIED.**
  Plan 1 is not fully signed off until the manual checks below run on a clean
  Fedora 44 machine with root access and free 80/443.

## Environment

| Item | Value |
|---|---|
| OS | Fedora Linux 44 (Workstation Edition), x86_64 |
| Kernel | `7.2.9-200.fc44.x86_64` |
| systemd | 259 |
| Go | 1.27.3 linux/amd64 |
| Node / npm (build host) | v22.23.1 / 10.9.8 |
| nginx | 1.28.0, `/usr/local/nginx/sbin/nginx`; system `nginx` service active and bound to 80/443 |
| Apache | httpd 2.4.69 (RPM) installed, inactive; `mod_ssl` and `/etc/httpd/conf.d/ssl.conf` absent; `apache2` command absent |
| Browsers | Chrome 155.0.8059.39, Firefox 157.0.1 |
| NSS tools | `certutil` not installed |
| Privileges | user `uid=1000`; passwordless `sudo -n` is available, but no system-level install/uninstall was performed |
| Shell tooling | bash, ShellCheck 0.11.0, `systemd-analyze` |

The workstation already runs an nginx master (PID 1441) on ports 80 and 443.
That server was left untouched. A fresh system install, `devstack init`,
uninstall, browser-trust install, and real Apache mode switch would have
modified `/etc/hosts`, the system/browser trust stores, systemd units, and the
running server, so they were not attempted.

## Automated acceptance

The Go test cache was cleared with `go clean -testcache` before the final unit
and race runs so the results below are fresh.

| Check | Command | Result |
|---|---|---|
| Unit/integration | `go test ./...` | **PASS** — all packages `ok`; 4.96 s |
| Race detector | `go test -race ./...` | **PASS** — all packages `ok`; 6.50 s |
| Vet | `go vet ./...` | **PASS** — no output, exit 0 |
| Build | `make build` | **PASS** — dashboard + both binaries built; 6.63 s |
| E2E, standard | `E2E_SKIP_BUILD=1 bash test/e2e/linux.sh` | **PASS** — 2.01 s; Node check skipped (see below) |
| E2E, supplemental Node | temporary copy of `test/e2e/linux.sh` with `runtime install node@22.23.4 --yes` added after daemon readiness | **PASS** — 17.99 s; managed Node site served through nginx |
| UI install | `cd ui && npm ci` | **PASS** — 131 packages; 2 dev-only advisories reported |
| UI unit tests | `npm test` | **PASS** — 6 files, 27 tests |
| UI type/Svelte check | `npm run check` | **PASS** — 0 errors, 0 warnings |
| UI build | `npm run build` | **PASS** — Vite built dashboard |
| Browser tests | `npx playwright test` | **PASS** — 18 Chromium tests |
| Shell syntax | `bash -n install.sh test/e2e/linux.sh` | **PASS** — exit 0 |
| Shell lint | `shellcheck --severity=error install.sh test/e2e/linux.sh` | **PASS** — exit 0 |
| Installer preview | `./install.sh --dry-run` | **PASS** — exit 0; printed planned actions, changed nothing |
| systemd units | `systemd-analyze verify` on copies with `%h` and binary paths rewritten | **PASS** — exit 0 |
| Git state | `git status --short`; `git rev-parse HEAD` | clean tree; `e88231fbb1d7f359c6bc59724c3624264fbb36e4` |

Build artifacts from the tested revision:

```text
$ ./dist/devstack version
dev (e88231f)

75d4f9253fe67f59225fca8e611f7d13dbfd8d0bda5f982e0f4215b0f1299d69  dist/devstack
3eceed2f5df59d8c185030d254398793dac9cd77eb886efa2908e5e31fd3e9f7  dist/devstack-helper
```

`dist/` is gitignored, so these hashes identify local build outputs only.

### Standard E2E output

```text
e2e: static site served at https://blog.test:<high-port>
e2e: static site served through helper forwarder https://blog.test:<high-port>
e2e: helper restarted and forwarder restored automatically
e2e: daemon stopped cleanly
e2e: switched to external nginx (release and bridge installed)
e2e: nginx served https://blog.test:<high-port>
e2e: nginx force-HTTPS redirect targeted https://redirect.test:<high-port>
e2e: vhost preview rendered the site root
e2e: SKIP node vhost check (no installed Node runtime)
e2e: switched back to builtin; helper forwarder and serve-static restored
e2e: PASS (builtin and, when available, external nginx serving verified)
```

The standard script uses temporary HOME/state/hosts/trust paths and high ports.
It does not bind 80/443, edit `/etc/hosts`, or touch real trust stores. The
external nginx run uses a real nginx binary from a temporary prefix.

### Supplemental Node run

The standard E2E skips its Node vhost check when no managed Node runtime is
installed in the test home. To close that gap without changing the checked-in
script, the same script was copied to a temporary directory, its `REPO_ROOT` was
pinned to this checkout, and this line was inserted after daemon readiness:

```bash
run_cli runtime install node@22.23.4 --yes
```

That run produced:

```text
e2e: installed managed Node runtime
e2e: static site served at https://blog.test:<high-port>
e2e: static site served through helper forwarder https://blog.test:<high-port>
e2e: switched to external nginx (release and bridge installed)
e2e: nginx served https://api.test:<high-port>
e2e: switched back to builtin; helper forwarder and serve-static restored
e2e: PASS (builtin and, when available, external nginx serving verified)
```

This verifies a managed Node runtime install plus HTTP serving through an nginx
vhost for a Node site. It does not exercise a browser HMR session or a
WebSocket upgrade through nginx. The built-in proxy WebSocket path is covered by
`TestRouterWebSocketEcho` in `internal/proxy/server_test.go`.

### Failures found and fixed during acceptance

The first race run on the original revision (`3c8810a`) failed in two daemon
tests:

```text
--- FAIL: TestDaemonServesAndRestartsCleanly
    daemon Run returned daemon: publish routes: sites: ensure dashboard certificate: context canceled
--- FAIL: TestDaemonSocketHostGuard
    daemon Run returned daemon: publish routes: sites: list for routes: context canceled
```

Cause: `startDaemon` returned as soon as the control socket accepted
connections. The socket is created before route publishing and the readiness
marker; the test then cancelled the daemon during startup. Fixed in
`d1d8c39` (`test: wait for daemon readiness in shutdown tests`) by waiting for
`run/daemon.ready` in the test helper. No product code changed.

A later full race run failed because two independent `freePort` calls could
return the same port:

```text
--- FAIL: TestDaemonBuiltinStartsProxy
    daemon exited before the socket appeared:
    daemon: invalid configuration: http_port and https_port must differ, both are 19211
```

Fixed in `e88231f` (`test: allocate unique ports in daemon tests`) by tracking
handed-out ports and skipping duplicates. No product code changed.

After both fixes, `go test -race ./...` passed and `go test -race
./internal/daemon -count=5` passed five consecutive runs.

### UI dependency advisories

`npm ci` reports two advisories in the development dependency tree
(`@vitest/mocker` via Vitest: one moderate path-traversal/arbitrary-file-read
advisory and one critical manual-mock code-injection advisory). `npm audit
--omit=dev` reports **0 vulnerabilities**; the advisories do not affect the
built dashboard or daemon runtime. Upgrading Vitest is recorded as a follow-up
rather than changed during an acceptance-only task.

## Plan 1 acceptance checklist

Status values: **PASS** = verified in this run; **PARTIAL** = automated or
high-port coverage only; **BLOCKED** = not runnable safely in this environment;
**DEFERRED** = belongs to a later plan.

| # | Criterion | Status | Evidence |
|---|---|---|---|
| 1 | Fresh install → `devstack init` → Doctor reports every applicable trust target green | **BLOCKED** | No devstack install on this workstation; init would modify `/etc/hosts`, CA anchors, systemd, and 80/443. 80/443 are owned by the active nginx service; `certutil` is absent. |
| 2 | Static site reachable at trusted `https://project.test` with no browser warning; HTTP/HTTPS behavior matches settings | **PARTIAL** | E2E serves a static site and force-HTTPS redirect over real TLS, validated with `curl --cacert`; no browser warning check. |
| 3–5 | PHP 8.2/8.5 side by side, ini isolation, Xdebug, version management | **DEFERRED** | Plan 2. |
| 6 | Node site proxies HTTP and HMR WebSockets; crash logs/backoff/restart | **PARTIAL** | Supplemental E2E served a managed Node 22.23.4 site over nginx HTTP; built-in WebSocket echo test passes. No browser HMR session or nginx WebSocket E2E. |
| 7 | Start/stop/restart from CLI and UI; logs stream; daemon restart reconciles and preserves desired state | **PARTIAL** | E2E proves daemon stop/restart, site still serving, one static-server process, and readiness marker. UI lifecycle is covered by Playwright against the mock API, not a live daemon. |
| 8 | Fresh Windows install, WSL, Windows trust | **DEFERRED** | Plan 3. |
| 9 | `trust remove` and `uninstall` revert hosts, CA, listeners, units, processes; project files untouched; Doctor diagnoses broken preconditions | **BLOCKED** | Not run against the real system. Helper unit tests cover uninstall/trust/vhost removal and foreign-file refusal. |
| 10 | CI green, race clean, helper RPC/archive security tests pass, no privileged writes to user config | **PARTIAL** | Local race and vet pass; security tests are in the passing Go suite. No CI run was observed or linked in this environment. |
| 11 | Docs cover install, first site, TLS repair, upgrades, uninstall | **PASS** | `docs/install.md`, `docs/sites.md`, `docs/tls.md`, `docs/vhosts.md` exist and are linked from README. Two stale “builtin only” limitation bullets were corrected during this pass. |
| 12 | External serving works end to end for nginx and Apache, preview, rollback, builtin restore, clean uninstall | **PARTIAL** | nginx: static, force-HTTPS, preview, Node HTTP, and builtin restore pass on high ports. Apache: **BLOCKED**. Rollback and vhost-removal-only-ours pass as unit tests, not live. |

## Manual checklist and blockers

| Check | Status | Reason / evidence |
|---|---|---|
| Chrome shows no certificate warning for static and Node sites | **BLOCKED** | No CA installed in the real OS/browser stores; `certutil` is absent. Installing trust would modify the workstation and browser profiles. TLS chain was validated with the generated CA bundle via `curl` only. |
| Firefox shows no certificate warning | **BLOCKED** | Same as Chrome. Firefox 157 is installed but no devstack CA was imported. |
| `devstack trust status` matches Chrome and Firefox | **BLOCKED** | No `devstack init`/trust install was run, and NSS tooling is missing. |
| UI flows against a live daemon | **NOT RUN** | Playwright covers the seeded mock API (`18 passed`); a live-daemon browser pass was not performed. |
| Start/stop/restart from CLI and UI on a live site | **PARTIAL** | CLI/daemon lifecycle and idempotency are covered by tests and E2E; no interactive live UI/CLI walkthrough was recorded. |
| Daemon restart reconciliation | **PASS** | Standard E2E: daemon stopped and restarted, site served afterwards, exactly one static-server group, readiness marker updated. |
| External nginx serves static and Node at trusted HTTPS on 80/443 | **PARTIAL** | Real nginx served both on high ports with the devstack CA. 80/443 were not used because the workstation's nginx owns them. |
| External Apache serves static and Node at trusted HTTPS | **BLOCKED** | Apache is installed but inactive; `mod_ssl` is not installed, so HTTPS vhosts cannot be tested. Enabling/installing modules would change the system. |
| `devstack vhost use builtin` restores proxy/forwarders | **PASS** | Standard E2E: after nginx mode, builtin restored the helper forwarder and static server. |
| Deliberately broken vhost config leaves previous mode serving | **PARTIAL** | `TestSyncRollsBackOnConfigFailure` and `TestSyncRollsBackOnReloadFailure` pass with the fake runner; no live broken-config test was run. |
| `devstack uninstall` removes our vhosts/bridge/TLS but not unrelated configs | **BLOCKED** | Live uninstall was not run. `TestRemoveOnlyOurs`, `TestRemoveRefusesForeignBridgeAndKeepsMode`, and the edited-bridge tests cover ownership refusal. |

## Known limitations and follow-ups

1. **Manual acceptance is still open.** Run the browser-trust, privileged
   install/init/uninstall, live UI, and 80/443 mode-switch checks on a clean
   Fedora 44 workstation or VM. Required preconditions: free 80/443, root/sudo,
   `nss-tools` (`certutil`), and Apache with `mod_ssl` enabled.
2. **Real Apache serving has no end-to-end test.** The repository has golden and
   helper tests for Apache rendering, plus fake-runner rollback tests, but no
   real `httpd` serve test comparable to the nginx E2E. CI installs `apache2` on
   Ubuntu but the E2E script only exercises nginx.
3. **HMR is not exercised as a browser session.** Built-in WebSocket proxying is
   tested; nginx/Apache WebSocket upgrade behavior for a Node HMR client is not
   covered end to end.
4. **Node E2E is conditional.** The checked-in E2E skips Node when no runtime is
   installed; the supplemental run above installed `node@22.23.4` explicitly.
   Consider an opt-in E2E mode that performs the runtime download when network
   access is available.
5. **Development dependency advisories.** Upgrade Vitest (and `@vitest/mocker`)
   to a fixed release after checking the supported version range. Production
   dependencies audit clean.
6. **CI status was not independently observed.** Local equivalents pass, but no
   CI run URL or artifact was available during this acceptance pass.
