# Decision log

- **Last reviewed:** 2026-10-27
- **Status:** M1, Waves 0–2, and the next service-platform batch are implemented. ImageMagick CLI is descoped; automatic GitHub CI is disabled. Windows/WSL has a revised design/plan and is not implemented.
- **Detailed service design:** [2026-10-25 design](superpowers/specs/2026-10-25-service-containers-and-performance-design.md)
- **Windows/WSL design:** [2026-10-26 design, revised 2026-10-27](superpowers/specs/2026-10-26-windows-wsl-design.md)

This is an ADR-style record. “Current” decisions are shipped or in the released M1 code.
The service-platform decisions below are implemented; residual host gates and limitations
are tracked in [known issues](known-issues.md).

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
| C17 | Standalone devstack Docker image retired | Removed on 2026-10-25. Docker is reserved for the planned separate service containers. Generic public-port URL/redirect support and the mounted-hosts fallback were retained because they are useful outside the old image. |
| C18 | Automatic GitHub Actions disabled | Pushes, pull requests, and tags do not start workflows; `ci.yml`, `php.yml`, and `release.yml` are manual dispatch only. Local verification is authoritative. |

## 2. Service-platform decisions (implemented)

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
| S42 | ImageMagick CLI descoped; PHP `imagick` bundled | User no longer needs the CLI; removing it avoids a separate image/tool surface. PHP `imagick` remains available. |
| S43 | Existing `/usr/local/bin/php`/`composer` requires `--force` backup | Avoid destroying unrelated tools. |
| S44 | Service config editing is scoped to `services/<id>/config` | Never expose data, logs, secrets, certs, backups, or arbitrary host paths. |
| S45 | Performance apply/reset/host tuning is explicit and reversible | Host-wide kernel changes require confirmation, fixed helper targets, backup, verification, and reset. |
| S46 | Browser terminal is site-scoped and user-only | No root/sudo/Docker socket; PTY ticket bound to UID/site; no arbitrary host command surface. |
| S47 | Editable Settings ports are daemon listener ports | `http_port`/`https_port`/`ui_port` persist and require restart; public 80/443 remain helper/Docker-owned. |
| S48 | Service config manifest lives under the config root | Keeps the manifest inside the editable scope and shares it with Docker nginx/Apache; legacy service-root manifests are migrated with a `config/` prefix strip. |
| S49 | Performance benchmarks use a labelled built-in Go generator | No verified pinned wrk/hey image; measurements are real and labelled, never synthesized. |
| S50 | Host tuning is fixed-path helper data, not host content from the API | The helper derives paths, backs up, verifies, and rolls back; the daemon never sends arbitrary paths or commands. |
| S51 | Terminal tickets are site/UID-bound one-use capabilities | WebSocket GETs are authenticated by the ticket; issuance still requires session+CSRF; Origin and Host are checked. |
| S52 | UI builds are static and embedded; xterm.js is the terminal renderer | Keeps dashboard parity without a second server; terminal is explicit-start only. |
| S53 | Per-site PHP-FPM pool sizing is a persisted site field | The daemon owns the calculated budget and applies it through validate/swap; generated fragments remain previews, not the source of truth. |
| S54 | Helper owns a narrow proxy-target update RPC | Daemon listener changes remain one restart; the helper rewrites only `proxy_http_port`/`proxy_https_port` atomically, and serving-mode reconciliation restarts its forwarders. Privileged listen ports remain helper-fixed. Requests remain same-UID and loopback-only. |
| S55 | Windows 10 22H2/Windows 11 use a native daemon/helper plus a managed WSL2 Linux agent | Windows owns UI/OS integration/ports; WSL owns PHP/Node/static and Linux-only extensions; Docker is limited to infrastructure services. |
| S56 | Windows and WSL/Linux use platform roots `C:\devstack` and `/data/devstack` | Projects default under `projects/`; Windows has no dot-prefixed devstack names. SQLite/CA stay native, Linux runtimes/Docker data stay ext4. |
| S57 | Projects support per-site Windows or WSL source modes | Users choose editor/tool compatibility versus native WSL filesystem performance; cross-boundary symlink bridging is rejected. |
| S58 | Docker PHP and native Windows PHP are excluded | PHP stays native Linux PHP-FPM in WSL/Linux so Linux-only extensions and per-site FPM behavior remain available. |
| S59 | Windows public entry is always the helper → Windows daemon proxy | The helper never forwards to WSL. A dynamic root forwarder target is avoided; the daemon is the single public HTTP/TLS router and dashboard host. |
| S60 | Windows daemon terminates public TLS; Docker web servers use private HTTP ports | nginx/Apache still serve site traffic and `.htaccess`, but do not own public 80/443 or the dashboard on Windows. A container change cannot strand `devstack.test`. |
| S61 | The WSL agent runs a fixed 80/443 ingress to the Windows daemon | WSL `.test` names map to `127.0.0.1`; the ingress forwards to the Windows host gateway. This makes curl/Composer/Node/PHP/terminal `.test` access work in both serving modes. |
| S62 | Windows→WSL backend traffic requires WSL localhost forwarding | Site backends bind WSL loopback; the agent API also listens on the distro IP for control fallback. The doctor fails clearly when forwarding is unavailable. |
| S63 | All Windows site backends run in the WSL agent; the agent owns WSL port allocation | The Windows daemon does not run a local site supervisor. `PreparedSite` returns the backend host and ports; the daemon persists and routes them. |
| S64 | WSL ext4 is canonical for generated site/service data | Service config/data/secrets, generated PHP configs, runtimes, logs, and Docker data live in WSL. Windows keeps metadata, config, leaf certs, daemon logs, cache, and the VHDX; APIs proxy to the agent. |
| S65 | WSL path validation and source copy are agent-side | UNC is not a reliable authorization/permission boundary. The agent uses lstat/readlink/realpath, tar, SHA-256 manifests, ownership/mode checks, and symlink/junction rejection. |
| S66 | The agent API uses HTTPS with a pinned certificate and bearer token | Token/fingerprint are DPAPI-protected on Windows; agent token/key files are 0600/0700 in WSL. Secrets never appear in process arguments. |
| S67 | Helper and daemon control pipes are ACL-restricted and SID-checked; the Windows helper surface is reduced | Only the installing SID/SYSTEM (plus Administrators for the helper) may connect. The Windows helper serves CA/trust/hosts/firewall/forwarder/health only. |
| S68 | Global PHP/Composer, shells, and builds run in WSL; Windows gets `.cmd` shims | Preserves Linux PHP semantics and avoids native Windows PHP. `.cmd` shims invoke the managed distro and site/global context. |
| S69 | New Linux installs use `/data/devstack`; existing installs are preserved | Explicit `--home`/`DEVSTACK_HOME` wins; legacy `~/.devstack` and helper paths are detected. The top-level root is root-owned, user data is user-owned, and `system` is root-only. |
| S70 | No devstack-created Windows basename starts with a dot | Internal temp, manifest, state, and backup names are renamed and migrated; project-owned `.git`/`.env`/`.idea`/`.htaccess` are exempt. |
| S71 | WSL rootfs, Docker Engine/Compose, agent, and PHP/Node artifacts are pinned and published | Checksums/SBOMs and installer verification prevent floating or invented artifacts; a clean WSL import/Docker smoke is part of acceptance. |
| S72 | Windows daemon proxy is the only public listener; the control API stays on loopback | The WSL-facing proxy listener is firewall-restricted to the WSL subnet. WSL reaches the dashboard through the public `devstack.test` route, not the control API. |
| S73 | Supported Windows floor is Windows 10 22H2 (19045) x64 plus Windows 11 | No Windows-11-only API. The installer and acceptance matrix cover both, including WSL2/systemd/localhost forwarding on Windows 10. |
| S74 | The Windows installer owns a complete, resumable prerequisite manifest for Windows and WSL | It installs WSL/WSL update/Docker/CA/build/runtime dependencies in advance where possible, and uses an explicit opt-in plus reboot resume for Windows optional features. It never silently reboots or assumes a pre-provisioned machine. |

## 3. Rejected alternatives

| Alternative | Why rejected |
|---|---|
| Native packages/systemd for all tools | Cross-distro drift, package conflicts, large privileged package-install surface; user chose containers. |
| One all-in-one tools container | Shared failure domain, complex DB supervision, no systemd, larger attack surface. |
| Run tools inside a devstack application container | No systemd, awkward DB lifecycle, image bloat; the old image was retired and the user wants separate service containers. |
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

- optional public 80/443 editing (currently helper/Docker-owned);
- exact host-tuning values per machine class;
- external-site Docker bind mounts;
- live Wave 3 benchmark/host acceptance evidence.

## 5. Decision provenance

- M1 architecture and release decisions: `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md`,
  `docs/superpowers/specs/2026-10-22-external-vhosts-design.md`, release/acceptance reports.
- Service-platform decisions: `docs/superpowers/specs/2026-10-25-service-containers-and-performance-design.md`.
- Current implementation status: `docs/handoff.md` and `docs/architecture.md`.
