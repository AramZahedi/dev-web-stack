# Decision log

- **Last reviewed:** 2026-10-25
- **Status:** mix of implemented decisions and planned service-platform decisions.
- **Detailed service design:** [2026-10-25 design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)

This is an ADR-style record. “Current” decisions are shipped or in the released M1 code.
“Planned” decisions are approved in the design conversation but not implemented. “Open”
items are implementation choices that do not change the product architecture.

## 1. Current product decisions

| ID | Decision | Rationale / consequences |
|---|---|---|
| C1 | Go monolith with CLI, daemon, helper, embedded UI | One module, static binaries, easy local install, no runtime dependency on Go after install. |
| C2 | Native Linux systemd install | Base product must work on the host; helper is a root system service, daemon a user service. |
| C3 | Builtin Go proxy by default | Zero-config `.test` sites, no nginx/Apache install requirement. |
| C4 | Optional native nginx/Apache vhost modes | Projects needing native server behavior can opt in; server must already be installed. Exactly one mode owns 80/443. |
| C5 | Helper owns privileged operations | Root CA, trust, hosts, 80/443 forwarding, native vhosts, uninstall. Peer UID checked; no arbitrary commands/paths. |
| C6 | SQLite via `modernc.org/sqlite` | Pure-Go, no CGO, local single-user state. |
| C7 | Per-site PHP-FPM masters | Isolation, per-site PHP version/ini/extensions, safe generation swap and rollback. |
| C8 | Checksum-pinned PHP/Node runtime catalog | Reproducible downloads; no floating versions; SHA-256 and size verified. |
| C9 | Local CA + OS/browser trust | Trusted `.test` HTTPS without external DNS/certs; helper holds CA key. |
| C10 | Managed hosts block | Exact `.test` hostname routing with unrelated lines preserved. |
| C11 | No HSTS | Avoid poisoning local browser state; safe local development. |
| C12 | `.test` only | Reserved local TLD; no real DNS/public cert confusion. |
| C13 | One installing user per machine | Helper allow-UID model; no multi-user shared state in M1. |
| C14 | Embedded Svelte dashboard | Ships in the daemon binary; no separate web server for UI. |
| C15 | Unsigned checksummed releases | Integrity in transit; signing deferred to M4. |
| C16 | Static/Node/PHP site kinds | Clear runtime model; PHP front controller and Node process supervision are separate. |
| C17 | Current Docker image: Fedora 44, builtin only | Isolated packaging experiment; no systemd or external vhost support. Not the service-platform target. |
| C18 | Current Docker ports bind to localhost | Avoid LAN exposure by default; `DEVSTACK_BIND_ADDRESS=0.0.0.0` opt-in. |
| C19 | Current Docker helper runs root, daemon as UID 1000 | Matches native privilege split; `socat` bridges loopback listeners to public container ports. |
| C20 | Current Docker project files at `/home/devstack/projects` | Dashboard filesystem browser and `/workspace` compatibility symlink. |
| C21 | Docker does not import CA into host trust | Explicit trust is a host decision; CA is exported for manual import. |
| C22 | No native service packages in Docker image | Earlier image scope; superseded for native service platform by C30. |

## 2. Planned service-platform decisions

| ID | Decision | Rationale / consequences |
|---|---|---|
| S1 | devstack itself stays native | User requirement; Docker is only for optional tools. |
| S2 | Optional tools run as separate Docker service containers | Isolation, independent upgrades, database lifecycle; avoids one all-in-one tools container. |
| S3 | No native `dnf`/`apt`/systemd management for tools | Cross-distro consistency, avoids package drift and conflicts. |
| S4 | Root at `/data/devstack`, configurable | One backup/state root; user request. Existing `~/.devstack` stays valid. |
| S5 | Host-owned config/data/certs/secrets/backups | User can inspect/edit; no hidden container state. |
| S6 | Web-server containers use host networking | Must bind 80/443 and reach native PHP-FPM/Node on loopback. |
| S7 | Data services bind to `127.0.0.1` only | Local-only security; phpMyAdmin also loopback. |
| S8 | Privileged helper controls fixed Compose project | Daemon remains unprivileged; no Docker group requirement; no arbitrary Compose. |
| S9 | No Docker socket mounted into service containers | Prevents container escape from becoming host root. |
| S10 | Pinned image digests in embedded catalog | Reproducibility; no floating `latest`. |
| S11 | One active per conflict group | nginx vs Apache; MySQL vs MariaDB. Both can be installed, only one active. |
| S12 | Transactional activation with rollback | Stop conflict, start target, health/port check; restore previous on failure. |
| S13 | Native CA and trust remain authoritative | Containers receive leaf certs only; container replacement cannot break trust. |
| S14 | Configs editable; generated files conflict-protected | Never silently overwrite user edits; explicit keep/use/merge/detach. |
| S15 | Base configs user-owned after seeding | Upgrades create `.new`/diff; no silent replacements. |
| S16 | Custom includes never overwritten | Safe place for user directives. |
| S17 | Apache `.htaccess` honored | User requirement; default `AllowOverride All`; modes auto/generated/htaccess. |
| S18 | Front-controller filename configurable | Custom PHP bootstraps; `index.php` default. |
| S19 | SPA fallback filename configurable; separate from PHP front controller | `index.html` is static; never execute HTML as PHP. |
| S20 | Nuxt/Vite static build profiles | Build natively with selected Node; serve output as static; `--spa` only for client routes. |
| S21 | Nuxt SSR uses Node site | `.output/server/index.mjs` runs natively; no static fallback. |
| S22 | Native PHP/Node; Composer native PHAR | PHP already native; Composer needs project files/PHP; no container for PHP execution. |
| S23 | Global `php`/`composer` shims | Machine-wide CLI convenience; helper installs `/usr/local/bin` wrappers. |
| S24 | Machine-wide PHP CLI default | User picks installed build; global CLI ini; site shell overrides. |
| S25 | Random per-engine DB credentials | Avoid shared defaults; secrets `0600`; root local-only; `devstack` admin for phpMyAdmin. |
| S26 | phpMyAdmin cookie login on `127.0.0.1:8082` | No embedded credentials/auto-login; follows active MySQL family. |
| S27 | Dashboard parity | Every operational task available in UI; CLI alternative; site terminal/build/config/credentials. |
| S28 | Explicit build/terminal scope | Dashboard terminal runs as user, site root, no sudo; no arbitrary host file editing. |
| S29 | Auto-tuned throughput profile | Resource-aware values, capped, benchmarked; no blind maximum constants. |
| S30 | Host tuning via helper with backup/reset | User requested max optimization, but global sysctl changes must be reversible. |
| S31 | No explicit `use epoll` | nginx auto-selects epoll on Linux; portable config. |
| S32 | Version-aware nginx HTTP/2 | `http2 on` on >= 1.25.1; older syntax otherwise. |
| S33 | Profile-controlled gzip | No level-9 default; benchmark. |
| S34 | PHP-FPM budget owned by daemon | Daemon knows sites; helper only host/web-server tuning; avoids invalid global budgets. |
| S35 | Existing native external provider retained | Backward compatibility for manually installed nginx/Apache; Services page manages Docker only. |
| S36 | No automatic `~/.devstack` → `/data/devstack` migration | High-risk; new installs choose root; existing stays. |
| S37 | Data deletion backs up and verifies first | Irreversible DB deletion cannot be a single click without recovery. |
| S38 | Wrapper images published by release pipeline | Digest-pinned; no local wrapper accepted in production catalog. |
| S39 | Full vhost parity, including dashboard vhost | Switching to Docker web server must not strand `devstack.test`. |
| S40 | Deactivating/uninstalling active web server refused until switch | Avoid accidental outage/stranded 80/443. |
| S41 | Native server conflict is reported, never auto-stopped | No systemd management for tools; user must switch manually. |
| S42 | ImageMagick CLI container; PHP `imagick` bundled | PHP extension already ships MagickWand libs; CLI is separate tool. |
| S43 | Existing `/usr/local/bin/php`/`composer` requires `--force` backup | Avoid destroying unrelated tools. |
| S44 | Docker image remains builtin-only | Service platform is native devstack + tool containers; devstack-in-Docker is separate. |

## 3. Rejected alternatives

| Alternative | Why rejected |
|---|---|
| Native packages/systemd for all tools | Cross-distro drift, package conflicts, large privileged package-install surface; user chose containers. |
| One all-in-one tools container | Shared failure domain, complex DB supervision, no systemd, larger attack surface. |
| Run tools inside devstack Docker image | No systemd, awkward DB lifecycle, image bloat; user wants separate service containers. |
| Daemon uses user Docker socket/group | Gives unprivileged daemon root-equivalent Docker access. Helper-controlled fixed Compose is narrower. |
| Native Composer-only with system PHP | Breaks per-site PHP/extensions; native PHAR + site-aware shim needed. |
| Global `php` shim that always overrides site PHP | Breaks per-project PHP versions; site shell must win. |
| Apache generated rules with `AllowOverride None` | Rejected by user; Laravel `.htaccess` must work. |
| HTML front controller executed as PHP | Unsafe/non-standard; use static SPA fallback instead. |
| Fixed max worker counts | Can reduce throughput or prevent startup; resource-aware tuning + benchmarks. |
| Host sysctl changes without reset | Unsafe global change; helper backup/reset required. |
| Auto-moving `~/.devstack` | Risk to CA, SQLite, runtimes, certs, service data; deferred. |
| Shared `root`/`root` database password | Insecure; random per-engine credentials. |
| Auto-login phpMyAdmin | Credentials would be embedded/leaked; cookie login instead. |
| Native ImageMagick library for PHP | PHP builds already bundle MagickWand; container CLI is enough for CLI use. |
| Deleting DB data without backup | Irreversible; backup/verify required. |
| Exposing DB/Redis to LAN | Local-only scope and security. |

## 4. Open implementation decisions

These do not change the architecture:

- exact initial image tags/digests;
- exact nginx/Apache wrapper Dockerfiles;
- exact formulas and caps for worker/MPM/PHP budgets;
- benchmark workloads/concurrency;
- phpMyAdmin configuration mechanism;
- how much legacy native-provider UI remains;
- optional Redis password;
- exact Composer version/PHAR digest;
- exact host-tuning values per machine class.

## 5. Decision provenance

- M1 architecture and release decisions: `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`,
  `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`, release/acceptance reports.
- Service-platform decisions: `docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md`.
- Current implementation status: `docs/handoff.md` and `docs/architecture.md`.
