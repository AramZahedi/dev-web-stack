# Automatic PHP-FPM sizing and helper proxy targets

- **Date:** 2026-10-26
- **Feature commit:** `37db40e`
- **Merge commit:** `6f68163`
- **Branch:** `feature/auto-performance-ports` (merged into `master`)

## Delivered

1. **Per-site PHP-FPM pool sizing is persisted and live.** `sites.php_fpm_json`
   stores the budget (mode, child/spare counts, listen backlog). Performance apply
   merges it through `sites.ApplyPHP`; reset restores the prior budget or clears it
   to renderer defaults. Generated `services/php/performance/*.conf` files remain
   readable previews.
2. **Helper proxy targets follow daemon listener changes.** A narrow helper RPC
   validates loopback targets, rewrites only `proxy_http_port`/`proxy_https_port`
   in the helper YAML atomically, and updates in-memory targets before serving-mode
   reconciliation restarts 80/443. The write is skipped when unchanged, symlink
   targets are preserved, and a persistence failure is reported without preventing
   the live target move. `devstack init` performs the same synchronization before
   enabling forwarders.
3. **Benchmark form controls share one row.** Per-field range hints moved to a
   single line below the form, and a Playwright geometry assertion guards all four
   controls' bottom alignment.

## Verification

- Go unit suite: `go test ./...`.
- Race suite: `go test -race ./...`.
- `go vet ./...` and `go build ./...`.
- Real PHP 8.4.4 acceptance (`test/php`): accepted `pm = dynamic`,
  `pm.max_children = 8`, `2/1/3` spare servers, and `listen.backlog = 511` in a
  live validate/swap generation; FastCGI responses stayed healthy.
- Linux E2E: moved daemon listener ports from the initial pair to a new pair,
  confirmed the helper config received both new keys, public forwarder traffic
  followed, and a helper restart reused the persisted targets.
- UI: `svelte-check`, 107 Vitest tests, production build, and 31 Playwright tests
  (including the benchmark geometry check).
- Release gate: installer, workflow, and release archive tests.
- Docker smoke: Wave 1 web, Wave 1 databases, Wave 2 phpMyAdmin.

## Remaining host gates

Real root host-tuning acceptance, live Wave 3 benchmark evidence, and
external-site Docker bind mounts remain tracked in [Known issues](../../known-issues.md).
