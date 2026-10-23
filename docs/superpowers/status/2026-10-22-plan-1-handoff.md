# dev-web-stack — Plan 1 Handoff / Status

- **Date:** 2026-10-23
- **Branch:** `plan-1-foundation`
- **Remote:** `origin` (`git@github.com:AramZahedi/dev-web-stack.git`)
- **Head before this handoff commit:** `70844ca` (`fix: close final vhost review residuals`)
- **Remote head before this run:** `ce3432a`; this branch is ahead and will be pushed with this handoff.
- **Base:** `1477c52` (master merge-base)
- **Working tree:** clean.
- **State:** **Plan 1 Tasks 1–26 complete and reviewed.** Automated acceptance PASS.
  Manual browser/root/Apache/uninstall checks are blocked on this workstation.
  Plans 2–4 are not started.

Do not redo Plan 1. The next work is **Plan 2 — PHP**.

---

## 1. What was delivered

`dev-web-stack` Plan 1 is a Linux local web stack:

- Go CLI (`devstack`) + unprivileged daemon + privileged helper (`devstack-helper`).
- Static and Node sites at trusted `https://<name>.test` URLs.
- Built-in Go proxy/static server and optional native nginx/Apache vhosts with one
  global `serving_mode` (`builtin` default).
- Managed hosts block, local ECDSA CA, per-site TLS, Linux/NSS trust install.
- Fixed 80/443 forwarders in builtin mode; external nginx/Apache ownership in
  external mode with atomic releases, bridge include, config test, reload, rollback.
- Verified Node runtime catalog/download/extract, per-site supervision/backoff.
- SQLite state, logs/rotation/SSE, Doctor checks/repair, REST/SSE API, CLI, Svelte UI,
  Linux installer/systemd units, CI, and real high-port nginx E2E.

PHP/FastCGI, Windows/WSL, and packaging remain Plans 2–4.

---

## 2. Verification at the final head

At `70844ca` (re-confirmed after the final review fixes):

- `go test ./...` — **PASS**
- `go test -race ./...` — **PASS** (acceptance run)
- `go vet ./...` — **PASS**
- `make build` — **PASS**
- `E2E_SKIP_BUILD=1 bash test/e2e/linux.sh` — **PASS**:
  builtin HTTPS, helper forwarder, helper restart, daemon restart, external nginx
  on high ports, force-HTTPS redirect, vhost preview, builtin restore.
- UI: `npm test` 27 tests, `svelte-check` 0/0, Vite build, Playwright 18 tests.
- `bash -n` + `shellcheck --severity=error` — PASS.
- `install.sh --dry-run` — PASS.
- `systemd-analyze verify` on rewritten units — PASS.
- Acceptance report: `docs/superpowers/plans/2026-10-22-dev-web-stack-plan-1-acceptance.md`.

**Manual checks still blocked:** real Chrome/Firefox trust install (no certutil/CA
install), fresh init/uninstall/root 80/443 (active system nginx, would modify the
workstation), real Apache (`mod_ssl` absent), live UI against a real daemon, and
browser HMR/WebSocket. These are recorded honestly in the acceptance report.

---

## 3. Key commits

Plan 1 Tasks 1–20 were completed earlier; the vhost extension and final review:

| Commit | Scope |
|---|---|
| `9b33fc5` | nginx/Apache vhost renderers + goldens |
| `d62dc12` | helper vhost manager, releases, bridge, RPC |
| `9b570ce` | rollback/server-detection hardening |
| `9a95f47` | bridge ownership on removal |
| `bd715b9` | daemon serving mode, static lifecycle, API |
| `d734070` | serving-mode rollback/settings fixes |
| `e52484b` | vhost CLI + Doctor |
| `67bb0da` | TLS metadata/current mode for Doctor |
| `3a904ca` | Serving UI, preview drawer, docs |
| `6d0882c` | preview focus/mock parity |
| `30e0c2f` | high-port listens + real nginx E2E |
| `2454fc7` | redirect targets use configured ports |
| `a516876` | Apache mixed-port redirects |
| `3c8810a` | spec/plan alignment |
| `d1d8c39`, `e88231f` | acceptance test harness fixes |
| `2e11d8b` | Plan 1 acceptance report |
| `d2db8f8` | final-review Critical/Important fixes |
| `70844ca` | final-review residuals |

---

## 4. Final-review fixes (important context)

The whole-branch review found production issues that were fixed in the final wave:

- Helper systemd unit now permits bridge writes to `/etc/nginx/conf.d`,
  `/etc/apache2/conf-enabled`, `/etc/httpd/conf.d` with optional `-` paths.
- Foreign nginx `default_server` is detected; our default vhost suppresses the
  marker and Doctor warns instead of `nginx -t` failing.
- Root-run helper preflights running static roots for server-worker traversal/read
  (Fedora 0700 home) with actionable remediation.
- Daemon starts the API/socket before vhost reconciliation; reconcile failure is
  non-fatal, so `vhost use`, Doctor, and site removal remain available.
- `devstack init` does not force-enable forwarders in external mode.
- `SetServingMode` consults helper state, not only config, and repairs mismatches.
- A bad site degrades to a branded 503 vhost instead of poisoning the full sync;
  stopped Node paths are path-free.
- Doctor is mode-aware for ports, service-active, TLS directory, and default-server
  conflict.
- Apache static vhosts deny dot path segments (`.git`, etc.).

---

## 5. Decisions/rulings to carry forward

These are deliberate; see the SDD ledger for details. Do not silently reverse them.

1. `RestartPolicy.MaxRestarts` is the failure-count threshold; `0` crashes on first
   failure. `Window<=0` disables pruning.
2. The dashboard UI was built ahead of the plan; embedding/Playwright are Task 19.
3. Vhosts are helper-managed and native; one global mode; static sites have no
   process externally; Node stays supervised. PHP vhosts are Plan 2.
4. Task numbering: vhost tasks were 21–25; acceptance is Task 26.
5. API read auth: read-only GET/SSE is allowed without a session on the loopback/
   Host-checked API; mutations require session+CSRF; socket auth bypasses
   cookie/CSRF but not Host.
6. Certificate schema v2 removed the cert→site FK so certs can exist before a site
   and for `__dashboard__`; site deletion removes the cert row explicitly.
7. Doctor is diagnostic; occupied 80/443 is normal in builtin, free is a warning;
   no `sudo devstack` remediation.
8. Daemon lifecycle: lifetime lock, readiness marker, `StopAll`, unit-first
   `EnsureRunning`, detached fallback.
9. External mode requires `ui_port: 7070` (helper dashboard backend); config
   validation enforces it.
10. `tls=false` external sites use the dashboard cert and 308 to HTTP; nginx uses
    `listen <port> ssl http2;` for nginx 1.24 compatibility.
11. Bridge files are content-hash owned; a user-edited bridge is never overwritten
    or removed.
12. High vhost listen ports (`vhost_http_port`/`vhost_https_port`) are a test/
    advanced seam. Production defaults are 80/443.
13. Vhost mode and TLS/low-level state are helper-owned; config is the source of
    truth on restart; runtime mismatch repair uses helper status.
14. Failed vhost syncs must not overwrite the old cert set; Update rollback
    re-ensures the old certificate before re-syncing.
15. External static errors degrade to 503 vhosts; unreadable server-worker roots
    are a hard configuration error with remediation.

---

## 6. Repository map (current)

```
cmd/devstack/            CLI + daemon entrypoint
cmd/devstack-helper/     privileged helper entrypoint
internal/config/         $DEVSTACK_HOME config/paths (ServingMode, 7070 rule)
internal/model/          Site, RuntimeBuild, CertRecord, Event
internal/domain/         validation, port allocation
internal/store/          SQLite + migrations v2
internal/logs/           rotation, ring, SSE, redaction
internal/supervisor/     process groups, readiness, backoff, StopAll
internal/backend/        local static/Node backend
internal/helperproto/    RPC types, vhost types, errors
internal/helper/         Unix RPC, auth, CA/trust, forwarders, vhost manager
internal/helperclient/   helper client (hosts/CA/trust/vhosts)
internal/hosts/          managed hosts block
internal/trust/          trust targets
internal/testcert/       test CSR/cert helpers
internal/forwarder/      fixed 80/443 forwarders
internal/proxy/          router, TLS/SNI, reverse proxy, WebSocket
internal/staticserve/    secure static handler
internal/certs/          per-site certs, renewal, PEM boundary
internal/catalog/        embedded Node catalog
internal/runtime/        download/extract/verify/Node installs
internal/vhost/          pure nginx/Apache renderers
internal/sites/          lifecycle, external mode, vhost request/preview
internal/doctor/         diagnostics/repair + vhost checks
internal/api/            REST/SSE/auth/fs/UI + vhost routes
internal/daemon/         startup/shutdown, reconcile, API/proxy
internal/client/         Unix-socket CLI client
internal/cli/            Cobra commands (site/runtime/vhost/doctor/...)
internal/version/        ldflags version
ui/                      Svelte 5 dashboard, mock/real API, Playwright
api/openapi.yaml         API contract
build/systemd/           helper + user daemon units
build/helper.yaml.example
docs/                    install/sites/tls/vhosts + superpowers plans/specs
test/e2e/linux.sh        builtin + real high-port nginx E2E
install.sh               Linux installer
```

Defaults: `~/.devstack`; proxy 8080/8443; UI/API 7070; helper socket
`/run/devstack-helper/helper.sock`; helper state `/var/lib/devstack-helper`;
vhost tree `/etc/devstack/vhosts`; TLS `/etc/devstack/tls`; Node backend ports
41000–42999; FastCGI reserved 43000–43999.

---

## 7. Plan 2 — PHP (next)

The M1 spec §10.2 and §11 define the scope. The plan has **not been written**.
Recommended Plan 2 tasks:

1. PHP build pipeline under `build/php/` (relocatable 8.2–8.5, shared extensions,
   RPATH, base php.ini, smoke tests, CI artifacts).
2. PHP catalog + runtime manager integration (build IDs, checksums, version
   defaults, extension metadata, install/remove/migrate/verify).
3. Per-site FPM master lifecycle/config (`sites/<id>/php-fpm.conf`, 43000–43999,
   validate-then-swap, env, logs, health).
4. php.ini composition/validation and extension toggles (version defaults + site
   overrides, managed extension block, raw editor).
5. FastCGI backend transport in the built-in proxy (SCRIPT_FILENAME/PATH_INFO/
   front controller/index files).
6. PHP CLI/API/client/UI (site version, ini/extensions, runtimes, `devstack php`).
7. External vhost FastCGI templates (nginx fastcgi_pass; Apache proxy:fcgi) and
   mode-switch/TLS integration.
8. Doctor PHP checks, install docs, E2E with real PHP/FPM, acceptance.

Write the plan first with `writing-plans`, then execute with
`subagent-driven-development`, one task/commit and a task review each. Use
`opencode-go/deepseek-v4.1-flash#max` for every subagent.

Plan 3 is Windows/WSL2; Plan 4 is packaging/release. Neither plan exists yet.

---

## 8. Known limitations / deferred items

- Manual acceptance blockers above (browser trust, root 80/443, Apache mod_ssl,
  live UI/uninstall, HMR).
- Real Apache E2E is still missing; CI installs Apache and unit/golden tests cover
  rendering, but the real-server E2E is nginx-only.
- Node E2E is conditional; no managed runtime in the temp HOME. Acceptance ran a
  supplemental Node install manually.
- PHP, Windows/WSL, packaging/upgrades are not implemented.
- Vhost edge/minor items: worker readability is root-only and mode-bit/ACL based
  (not SELinux identity-aware); nginx fallback scan only `.conf`; `systemctl`
  unavailable vs inactive is not distinguished; `serving_mode_error` is not
  exposed in settings; vhost UI tabs lack roving arrow-key behavior; some docs
  wording remains builtin-centric.
- Dev-only UI advisories from Vitest/@vitest/mocker; production audit clean.
- The SDD ledger/workspace is git-ignored and local:
  `.superpowers/sdd/2026-10-22-dev-web-stack-plan-1-foundation/`.

---

## 9. Push state

The branch should be pushed after the handoff commit:

```sh
git push origin plan-1-foundation
git fetch origin
git rev-list --left-right --count origin/plan-1-foundation...HEAD   # expect 0 0
```

The remote previously held `ce3432a`; this push includes all vhost and acceptance
commits through the final review fixes.
