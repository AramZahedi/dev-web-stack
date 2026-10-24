# Audit disposition ledger — 2026-10-24

Input: `AUDIT_FINDINGS_AND_ANALYSIS.md`, verified against `plan-4-release` at `bfb1593`.
Verdicts are independent verification results, not the audit's labels.

## Critical

| ID | Verdict | Corrected severity | Action |
|---|---|---|---|
| CRIT-01 | PARTIAL | Low | Fix: absolute document root must remain inside the site's registered backend path; keep arbitrary project locations. |
| CRIT-02 | CONFIRMED | Medium | Fix: fail closed when `UserHome` is empty; require the dashboard session for `/api/v1/fs`; test empty-home and no-cookie cases. |
| CRIT-03 | FALSE POSITIVE | None | No fix. Apache separates `SCRIPT_FILENAME=index.php` from `PATH_INFO`; add real Apache+FastCGI integration coverage if practical. |
| CRIT-04 | CONFIRMED | Low | Fix: filter `Proxy` before CGI header conversion; add `HTTP_PROXY` regression test. |
| CRIT-05 | PARTIAL | Low | Fix: persist/observably retry a failed post-renewal vhost sync; the daily tick already retries, so the audit's "no retry" claim is false. |

## High

| ID | Verdict | Corrected severity | Action |
|---|---|---|---|
| HIGH-01 | CONFIRMED | Low | Fix: reject root-owned/non-user backend or document roots in helper vhost sync; allow user-owned `/srv` and `/var/www`. |
| HIGH-02 | PARTIAL | Medium | Fix: remove root `chownNSSDatabase` or replace with descriptor-pinned `fchownat`; add symlink-race regression test. |
| HIGH-03 | CONFIRMED | Medium | Fix: stop the old external server before starting/reloading the new one; handle inactive units and rollback. |
| HIGH-04 | CONFIRMED | Medium | Fix: do not turn an early PHP response into 502 when STDIN write fails after response bytes/END_REQUEST. |
| HIGH-05 | PARTIAL | Low | No fix: `ReadHeaderTimeout`/`IdleTimeout` are spec-mandated; global `WriteTimeout` would break SSE/WebSockets. |
| HIGH-06 | FALSE POSITIVE | None | No fix: `Shutdown` closes listeners; `Serve` returns even if handlers time out. Optional `Close()` is not a deadlock fix. |
| HIGH-07 | CONFIRMED | Medium | Fix: terminate a surviving orphan group safely; do not blindly kill a recycled PGID. |
| HIGH-08 | PARTIAL | Low | No fix for M1: `Pdeathsig` conflicts with the spec's adoption/reconciliation intent; document crash-survival gap. |
| HIGH-09 | PARTIAL | Low | Fix: serialize `EnsureRunning` spawn with a dedicated file lock; keep lifetime lock semantics. |
| HIGH-10 | BY DESIGN | None | No fix: single SQLite connection is deliberate. Separate real issue: `events` never pruned to the spec's 500. |
| HIGH-11 | PARTIAL | Low | Fix: make site+PHP snapshot writes atomic or remove the write-only snapshot path. |
| HIGH-12 | PARTIAL | Low | Fix: rebuild routes in `restoreSiteStateLocked`; add regression test. |
| HIGH-13 | BY DESIGN | Medium on shared hosts | No fix in this wave: TCP FPM is spec-mandated for Linux/WSL; document single-user assumption. |
| HIGH-14 | FALSE POSITIVE | None | No fix: `-d`/`-z` execute as the site owner; not a privilege boundary. |
| HIGH-15 | CONFIRMED | Low | Fix: degraded vhosts retain site TLS/cert when the root is broken. |
| HIGH-16 | CONFIRMED | Medium | Fix: verify sudo before stopping the daemon/replacing binaries; add fake-sudo denial test. |
| HIGH-17 | CONFIRMED | Medium | Fix: reload `SiteDetail` on route `:ref` change, clear per-site state, guard stale responses. |
| HIGH-18 | CONFIRMED | High | Fix: invalidate cached CSRF token on 403, fetch a fresh session, retry mutation once. |

## Medium

| ID | Verdict | Corrected severity | Action |
|---|---|---|---|
| MED-01 | CONFIRMED | Low | Fix: `/etc/hosts` fallback writes without `O_TRUNC`, truncates after full write, fsyncs/flocks. |
| MED-02 | CONFIRMED | Low | Fix: cap hosts domains and total rendered bytes. |
| MED-03 | CONFIRMED | Low | Fix: add `CapabilityBoundingSet` to helper unit and test it. |
| MED-04 | CONFIRMED | Info | Fix: handle management records before application request-ID checks. |
| MED-05 | CONFIRMED | Low | Fix: build static directory redirects from cleaned relative paths. |
| MED-06 | CONFIRMED | Low | Fix: default CGI status to 302 when `Location` is present without `Status`. |
| MED-07 | CONFIRMED | Low | Fix: flush FastCGI streaming responses. |
| MED-08 | BY DESIGN | None | No fix: default 80/443 canonicalization is spec behavior. |
| MED-09 | BY DESIGN | None | No fix: single-use FastCGI connections are deliberate; no local-rate issue. |
| MED-10 | PARTIAL | Low | Fix: release supervisor mutex before waiting on `doneCh`; identity-check afterward. |
| MED-11 | BY DESIGN | None | No fix: port-probe TOCTOU is inherent; backend readiness catches external binders. |
| MED-12 | CONFIRMED | Medium | Fix: redact JSON quoted keys, URI userinfo, and `Cookie`/`Set-Cookie`. |
| MED-13 | PARTIAL | Low | Fix: Doctor reports owner-unknown/occupied expected ports; skip dialing non-running sites. |
| MED-14 | PARTIAL | Low | Fix: do not create permanent log store entries for unknown site IDs. |
| MED-15 | CONFIRMED | Low | Fix: polling effect depends on transient status, not mutable site object. |
| MED-16 | CONFIRMED | Low | Fix: `mktemp` for PHP build/download temp files; cleanup traps. |
| MED-17 | PARTIAL | Low | Optional: warn/fail when host glibc exceeds the pinned build baseline; CI is already pinned. |
| MED-18 | CONFIRMED | Low | Fix: add hardened CFLAGS/LDFLAGS and smoke ELF assertions. |
| MED-19 | FALSE POSITIVE | None | No fix: synchronous stop boundaries; retries would hide real leaks. |
| MED-20 | PARTIAL | Low | Fix: Playwright port collision must FAIL unless explicitly skipped; no silent PASS. |
| MED-21 | BY DESIGN | None | No fix: dry-run network/checksum/extraction is documented and tested. |
| MED-22 | CONFIRMED | Low | Fix: retry PHP smoke with a fresh port on bind/listen failure. |
| MED-23 | FALSE POSITIVE | None | No fix: daemon `flock` prevents concurrent CLI/daemon downloads. |
| MED-24 | CONFIRMED | Low | Fix: overwrite XFF at every proxy/vhost edge; add FastCGI XFF param. |
| MED-25 | BY DESIGN | None | No fix: raw php.ini is explicitly a site-owner feature. |
| MED-26 | FALSE POSITIVE | None | No fix: startup/mode-switch paths already tear down builtin backends. |

## Low / operational

| ID | Verdict | Action |
|---|---|---|
| LOW-01 | CONFIRMED | Fix: map `MaxBytesError` to 413 on PHP routes. |
| LOW-02 | BY DESIGN | No fix: local `.test` SNI fallback is not cross-name exploitable. |
| LOW-03 | CONFIRMED | Fix: use `os.Root` for PHP static file resolution. |
| LOW-04 | CONFIRMED | Fix: 301 directory paths without trailing slash before serving index. |
| LOW-05 | CONFIRMED | Fix: give `RunAs` a minimal explicit environment. |
| LOW-06 | CONFIRMED | Fix: never widen a pre-existing socket parent directory. |
| LOW-07 | BY DESIGN | No fix: helper forwarder is loopback-only. |
| LOW-08 | CONFIRMED | Fix: store PID+start ticks in daemon pid file and verify before signaling. |
| LOW-09 | BY DESIGN | No fix: cookies cannot be port-scoped; session is a CSRF nonce. |
| LOW-10 | PARTIAL | Document: `setsid` descendants can escape process groups; cgroups are out of M1 scope. |
| LOW-11 | CONFIRMED | Fix: reject `$` in `validateTLSDir`. |
| LOW-12 | CONFIRMED | Fix: graceful `SIGQUIT` for PHP generation drain, `SIGTERM` for normal stop. |
| LOW-13 | PARTIAL | No fix: `phpini.Parse` has no production caller; keep raw-value behavior. |
| LOW-14 | CONFIRMED | Optional: add client sequence IDs for log keys; perf only. |
| LOW-15 | FALSE POSITIVE | No fix: revoke-after-click works in supported browsers. |
| LOW-16 | PARTIAL | Optional: return a disposer from theme init; no current leak. |
| LOW-17 | PARTIAL | Fix: helper readiness loop checks `systemctl is-active`, not just socket existence. |
| LOW-18 | BY DESIGN | Optional: honor `DEVSTACK_PHP_ROOT` in acceptance; plan pins `/usr/local/php`. |
| LOW-19 | CONFIRMED | Fix: retry E2E startup with fresh ports on bind failure. |

## Fix wave 1 — commit c8eef74

Fixed (verified against current code; tests added where practical):
- CRIT-01, CRIT-02, CRIT-04, CRIT-05; HIGH-01,02,03,04,07,09,11,12,15,16,17,18;
- MED-01,02,03,04,05,06,07,10,12,14,15,16,18,20,24; LOW-01,03,04,05,06,08,11,12,17,18.
- Also bounded the events table to 500 (separate real issue from HIGH-10).

No-fix dispositions (by design/false positive): CRIT-03; HIGH-05,06,08,10,13,14; MED-08,09,11,19,21,23,25,26; LOW-02,07,09,10,13,15.

Parked for later (real but low-risk or needs a larger design):
- MED-13 Doctor owner attribution (needs OS-level owner inspection; current message remains "in use").
- MED-17 build-host glibc warning is advisory only (CI is pinned to Ubuntu 22.04).
- MED-22 PHP smoke port retry (needs whole-smoke retry refactor).
- LOW-14 log line key performance; LOW-16 theme disposer; LOW-19 E2E port retry.
- HIGH-13 loopback FastCGI remains the spec's Linux/WSL transport; document single-user assumption rather than switch transports in this wave.

Verification so far: `go test ./...`, `go test ./... -race`, `go vet ./...`, UI `check`/unit/build, Playwright targeted, installer/release/workflow tests, shellcheck. Full acceptance running.

## Independent parallel review (post c8eef74)

Reviewed by 7 read-only agents. Verified and fixed:
- Critical: root chown could still follow a user-swapped ancestor; pre-existing NSS components are no longer chowned, and created directories use a no-follow fd.
- High: root-owned backend poisoned all external vhosts; daemon preflight now degrades it. PHP Update/restart divergence and expired-cert startup were fixed by restarting on effective changes and ensuring certs before route publication. E2E wrapper updated for reload-or-restart.
- Medium: Apache X-Forwarded-Host, daemon-stop stale sidecar, mode-switch rollback/new-server stop, static dotfiles, nginx PATH_INFO, vhost TLS=false cert, HSTS stripping, force-HTTPS-without-TLS validation, DB/WAL 0600, Restart desired-state preservation, EnsureRunning timeout, zombie-aware orphan token check, daily renew self-heal, JSON cookie redaction/valid JSON, uninstall user unit/CA state, release archive ordering, PHP workflow branch, release workflow tests/version, acceptance external-skip and FPM baseline, UI log pause/reveal/modal/session/race/decode/PHPIni resync, redirect escaping.
- Low: default-vhost digit sort, dump_logs redirection, build-before-test ordering, smoke ELF RELRO/BIND_NOW, glibc warning.

Residual accepted/parked (documented, not silently dropped):
- MED-13 Doctor port owner attribution still says "in use" without resolving the OS owner; needs root-level proc/net inspection.
- Helper `Remove` still enables forwarders before the uninstall path stops them; clean uninstall works, but a port conflict can make the vhost removal report incomplete.
- Chrome "installed" status can be reported when only devstack-created ~/.pki/nssdb exists; detecting a real browser is environment-specific.
- LOW-14 log key performance, LOW-16 theme disposer, LOW-19 E2E port retry.
- GoReleaser archive mtimes and a successful non-dry upgrade fixture remain open release-hardening items.
- HIGH-13 loopback FastCGI remains the spec's Linux/WSL transport; single-user risk is documented.
