# Linux parity and E2E review — recent Windows-era changes

- **Date:** 2026-11-03
- **Base:** `6215b71` (`v0.5.0`)
- **Scope:** review recent Windows/WSL-era changes, keep genuinely Windows-only
  work out of Linux, and restore/verify Linux parity for general features.
- **Rule:** Linux uses native services. Docker-only Windows serving is not a
  Linux parity requirement.

## 1. What was reviewed

The Windows implementation added site source modes, managed project roots,
readable site configuration names, Node toolchains, database management,
phpMyAdmin signon, benchmark engines/request builder, performance profiles,
host tuning, native/Docker serving switches, and dashboard/CLI surfaces.

The Linux audit checked every recent `runtime.GOOS` branch, name-based config
change, managed-root rule, and Windows-only test. Genuinely kernel/syntax-bound
work remains Windows-only: named pipes, DPAPI, Windows services/ACLs, Windows
certificate/hosts/firewall integration, PowerShell installers, WSL
provisioning, Windows drive/UNC rules, and Windows power/registry/NTFS/Defender
tuning.

## 2. Linux regressions fixed

### Site configuration naming

- Linux tests and helpers still looked for `sites/<id>` and
  `<name>-<id>.conf`; production now writes `sites/<name>` and
  `<name>.conf`.
- Updated CLI, terminal, helper, PHP acceptance, and generation tests to the
  name-based layout.
- The PHP acceptance test and helper tests now validate the actual generated
  vhost filename.

### Managed project roots

- Creating a new local project under the managed projects root failed because
  the path had to exist before document-root validation. Linux now creates the
  missing project directory and, for PHP sites, the missing document root after
  the boundary check, then canonicalizes the path.
- A missing path outside the managed root now returns
  `PROJECT_OUTSIDE_ROOT` instead of a raw `lstat` error.
- Symlink escapes and the projects root itself remain rejected.

### Host tuning

- Host tuning was gated on an active Docker catalog web server, so builtin and
  native nginx/Apache Linux installs skipped it entirely. Host tuning is now
  independent of the web-server provider.
- Linux apply/reset messages and dashboard copy now describe sysctl/limits
  instead of claiming Windows/WSL only.
- The real-root probe found that `/etc/sysctl.conf` can be loaded after a
  drop-in and override it. The helper now forces the generated values with
  `sysctl -w` after `sysctl --system`, verifies them, and still rolls back on
  failure.

### Dashboard/CLI/API

- Benchmark Playwright coverage now checks the two-row form correctly and the
  Host/public-URL preview label.
- Revealed Redis/default passwords and user passwords survive background
  inventory polling instead of disappearing on the next refresh.
- Linux Settings no longer shows a WSL distro row.
- Linux `GET/POST /api/v1/fs?mode=wsl|windows` now returns `400` instead of
  `503`, and the CLI source-mode help only advertises modes valid for the host.
- `devstack vhost use` on Linux now explains the native nginx/Apache
  prerequisite and points at `docs/vhosts.md`.

### Test stability

- The real PHP acceptance harness now waits for a created backend to reach
  `running` and retries the initial FastCGI dial, removing a load-dependent
  `connection refused`/`starting` flake.

### Windows-only tests

Windows-only tests no longer fail on Linux: root-owned agent config, Windows
install-path assertions, Windows source-mode cases, reserved Windows folder
names, WSL browsing, and Windows-only source-mode CLI changes are skipped or
made platform-aware. The Windows code paths are unchanged and still compile.

## 3. E2E additions

- `test/e2e/linux.sh` can install a real Node runtime with
  `E2E_INSTALL_NODE=1` and then verifies a live Node site through native nginx.
- `test/e2e/linux-performance` is a root-only probe that computes a profile,
  applies the fixed sysctl/limits files, verifies live values, and restores the
  pre-devstack state.
- Playwright runs Chromium and Firefox by default. WebKit is skipped with an
  explicit acceptance note on hosts without `libicu74` (for example Fedora 44).

## 4. Evidence

- `go test ./...` — all packages pass after the fixes, including the real PHP
  subsystem and in-flight swap acceptance tests.
- `go vet ./...` — pass.
- `go test -race` for the changed packages (`sites`, `performance/service`,
  `helper`, `api`, `cli`) — pass.
- `make test-windows-cross` — Windows build, vet, and test-package compile pass.
- `ui`: `svelte-check` 0 errors, 391 Vitest tests pass, production build passes.
- Playwright Chromium + Firefox: 128 tests pass.
- `test/e2e/linux.sh`: builtin proxy, helper forwarders, daemon port move,
  native nginx, force-HTTPS redirect, vhost preview, and switch back to builtin
  pass; the optional live Node runtime/vhost check passes.
- `test/e2e/linux-performance`: apply/verify/reset pass as root; the probe
  removed the generated files and restored the original live values.
- `test/acceptance/m1.sh --quick`: **18 PASS, 0 FAIL, 2 SKIP, 1 BLOCKED**.
  The skips are the race-detector phase (disabled by `--quick`) and the manual
  root/browser checks. The block is Windows/WSL acceptance, which requires a
  Windows host. WebKit was skipped because this host lacks `libicu74`; Chromium
  and Firefox passed.
- Real PHP subsystem acceptance and Apache HTTPS PHP vhost pass.

## 5. Remaining limitations

- WebKit cannot run on this Fedora 44 host because the Playwright build expects
  `libicu74`; Chromium and Firefox are the required Linux browsers.
- Native Linux service installation/configuration remains administrator-managed.
  devstack does not install distro packages or enable nginx/Apache modules.
- Database-management live cycles 9–15 remain paused; database code is covered
  by unit/integration tests, and the existing live harness is Docker-backed.
- Windows was not run on this host. Windows safety is guarded by
  `make test-windows-cross` and unchanged Windows-only branches; full Windows
  acceptance still belongs to the Windows host.
