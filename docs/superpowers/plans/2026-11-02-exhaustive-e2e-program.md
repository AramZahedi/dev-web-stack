# Exhaustive E2E Program Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and run an exhaustive, repeatable E2E program that proves all 268 capabilities in `docs/e2e-coverage.md` on this Windows machine, then promote the results into repeatable release gates.

**Architecture:** A golden Ubuntu 26.04 WSL image is imported into 8 disposable distros. Five lanes run concurrently without Docker: H1 (sites/Add Site), H2 (detail/runtimes), H5 (doctor/failure), L1 (auth/UI), and L2 (contracts/CLI). H3, H4, and H6 share **one serialized Docker service slot** because every WSL distro shares one network namespace and the product publishes fixed service ports (3306, 5432, 6379, 8082). The service slot uses a test-only no-published-ports mode, `docker compose exec` for database work, and high-port TCP relays for phpMyAdmin and web-service UIs. No worker runs its own Docker engine. Windows Playwright drives each worker's dashboard over WSL localhost forwarding. Machine-wide state (Windows hosts, root store, helper service, 80/443/3306/7070/8080/8082/8443, C:\devstack links, uninstall) is tested by one serialized Windows/Linux lane only, after all workers stop.

**Tech Stack:** Windows 11 24H2+, WSL2 3.x, Ubuntu 26.04, Go daemon/helper/agent, Docker Engine, Svelte dashboard, Playwright 1.64, PowerShell 7, bash, Python 3 for coverage aggregation.

**Spec:** `docs/e2e-coverage.md` (authoritative 268-capability inventory).

**Companion matrix:** `docs/e2e-lane-matrix.md` (one row per capability with primary/secondary lane, test asset, and gate).

---

## Global Constraints

- **Model policy:** every session and subagent in this repository uses `opencode-go/deepseek-v4.1-flash#max`. Pass that exact model explicitly to every dispatch.
- **Real install is read-only** for all parallel lanes: never run `install.ps1`, `uninstall.ps1`, `devstack trust remove/repair`, `devstack init`, or write `C:\Windows\System32\drivers\etc\hosts` from H1-H6/L1-L2. Only the serialized W lane may touch it, after `HostBaseline` snapshot.
- **One Docker engine at a time.** H3, H4, and H6 share the serialized service slot and must never start a second Docker daemon. The slot runs with `DEVSTACK_E2E_NO_PUBLISHED_PORTS=1`, so the product's fixed service ports are never bound in the shared WSL network namespace. Database work uses helper `docker compose exec`; phpMyAdmin and web UIs are reached through high-port relays.
- **Never purge the real install.** Uninstall/purge tests (`INST-24/25`) run only in a disposable Windows VM. No such VM is currently available on this host, so those IDs are `BLOCKED` until one is provisioned; they are not waived. The real install's `laravel` database/user and `/data/devstack` data must survive every test. A real upgrade/rollback is allowed only with the installer's verified backup and a `HostBaseline` snapshot; a real uninstall is not.
- **Forbidden targets:** `C:\Users\Aram\.devstack`, `/data/devstack`, distro `devstack`, ports `80, 443, 3306, 5432, 6379, 7070, 7443, 8080, 8082, 8443`. Guards must fail closed on any of them. The service slot may let containers listen on those ports *inside their own container namespace*, but must never publish them on the host.
- **No secrets:** never print, log, trace, screenshot, or persist a password, token, private key, or CA private material. Use `$E2E_HOME/secrets` (0600) and the secret scanner before recording PASS.
- **Database E2E cycles 9-15 stay paused** until the user explicitly approves resumption. Do not drop the leftover `laravel` database/user. DB-28 remains `BLOCKED` in the matrix with an owner and expiry.
- **Bug fixes follow TDD:** failing test → minimal fix → focused tests → `go test ./...` / UI equivalent → `go vet ./...` → commit → push. Never weaken a security boundary or delete a failing test to get green.
- **One commit per task** (or per small task group), with the task ID in the message. Push after the task gate passes.
- **Evidence:** `dist/e2e/<run-key>/<lane>/<scenario>/` only; `dist/` is gitignored. Every result row records commit SHA, worker, lane, attempt, status, evidence path, secret-scan result, and residual risk.
- **No `wsl --shutdown`, no `.wslconfig` memory change.** Both stop or destabilize the real install. The plan uses the default 15 GiB WSL limit and a memory budget that fits inside it.

---

## Review Focus

These are the five failure modes most likely to make the suite lie or destabilize the host. Each one is pinned by a test in the named task.

1. **Shared WSL network namespace.** All distros see the same ports and Docker bridges. A test that assumes per-worker Docker isolation will collide with the real install (which currently holds 3306 and 8082) or another worker. Pinned by Task 1 (guards reject protected ports) and Task 2A (single service slot, no published fixed ports).
2. **Five concurrent non-Docker lanes plus one Docker slot versus 15 GiB WSL memory.** The revised budget is 7.6 GB, inside the default cap. Pinned by Task 2 (5-worker smoke), Task 2A (one-engine service slot), and Task 3 (memory admission check).
3. **Machine-wide hosts/trust/browser state.** Parallel workers must never write Windows hosts or the root store; a single serialized lane owns them. Pinned by Task 7 (run-wide lease + `HostBaseline`) and Tasks 17/18.
4. **Mock-only coverage masquerading as live coverage.** Every P0/P1 UI row must assert persisted backend state, not just the mock API. Pinned by Tasks 8-15, each of which ends with an API/CLI/state assertion and a deliberate mutation check.
5. **Secret leakage into evidence.** Tokens and generated passwords can escape through traces, screenshots, console logs, or command logs. Pinned by Task 4 (`secret-scan` self-test) and Task 5 (Playwright leak fixture; trace/video off for secret pages).

---

## 1. Why this shape

The inventory has 268 capabilities. 128 are `Mock + Evidence`, 57 are `Evidence` only, 27 are `Mock` only, 18 are `None`, and the remaining 38 already have `Live`, `System`, or mixed proof. The largest gap is not missing UI automation; it is missing **repeatable live proof with backend assertions**. A single sequential run cannot close that gap in a reasonable time, and a mock-only expansion would make the counts look better without proving anything.

The program therefore has three layers:

1. **Parallel live lanes** (H1-H6, L1-L2) prove the bulk of user-visible and API/CLI behavior against disposable daemons, real Docker services, real databases, and real browsers.
2. **One serialized Windows/Linux lane** (W) proves the machine-wide install, upgrade, uninstall, trust, hosts, ports, links, browser, source-mode, and agent behavior that cannot be isolated from the host.
3. **A coverage aggregator** maps every result back to an inventory ID and fails the gate when a P0 has no live/system proof, a result is stale, or evidence contains a secret.

## 2. Host capacity and lane layout

Measured baseline (2026-11-02): Windows 11 Pro build 26300, Ryzen 9 7940HX 16C/32T, 31.2 GB RAM, 225 GB free on C:, WSL 3.0.3, Ubuntu 26.04, FedoraLinux-44, real `devstack` distro. WSL global memory is 15 GiB with no `.wslconfig`; inside WSL: 32 vCPU, 4 GiB swap. The real install currently holds 3306 (MySQL) and 8082 (phpMyAdmin) in the shared network namespace, so no E2E worker may publish those ports.

### 2.1 Lane table

| Lane | Kind | Distro | Owns | Docker services | Target memory cap | Disk |
|---|---|---|---|---|---|---|
| **H1** | parallel | `devstack-e2e-<run>-w1` | Sites list, Add Site, local source mode | none (static/Node/PHP only) | 1.2 GB | 10 GB |
| **H2** | parallel | `...-w2` | Site detail, runtimes, toolchains, PHP/Node execution, PHP ini/extensions | none (PHP/Node only) | 1.5 GB | 12 GB |
| **H3** | service slot | `...-w3` | Services, config, serving modes, performance, settings | nginx, Apache, MySQL, phpMyAdmin, Redis (no host ports) | 2.2 GB | 15 GB |
| **H4** | service slot | `...-w4` | Databases, users, grants, backups, jobs, Redis, PMA, DB parity | MySQL, MariaDB, PostgreSQL, Redis, phpMyAdmin (no host ports) | 2.5 GB | 18 GB |
| **H5** | parallel | `...-w5` | Doctor, temp trust/hosts/ports, failure injection, confinement, disk/corrupt state | none | 1.2 GB | 10 GB |
| **H6** | service slot | `...-w6` | CLI/API parity for services/DB, daemon crash recovery, concurrency, soak | MySQL + phpMyAdmin (no host ports) | 2.2 GB | 15 GB |
| **L1** | parallel | `...-w7` | Auth/shell, mock breadth, a11y, responsive, cross-browser | none | 0.6 GB | 5 GB |
| **L2** | parallel | `...-w8` | Contracts, light CLI/API, SSE/terminal, network variants, coverage | none | 0.6 GB | 5 GB |
| **W** | serialized | real Windows + `...-linstall` | Install, upgrade, rollback, trust, hosts, ports, links, browsers, source modes, agent, Linux systemd | real services (serialized) | n/a | n/a |

**Capacity gate:** start H1, H2, H5, L1, and L2 together (5 non-Docker lanes, 5.1 GB budget). Then start the single service slot and run H3, H4, H6 sequentially (max 2.5 GB). Total budget is 7.6 GB, leaving more than 7 GB inside the default 15 GiB WSL cap for the real install and page cache. Abort a lane if `free -m` available drops below 1 GB or the kernel OOM-kills a container. There is no multi-engine gate and no 24 GB `.wslconfig` change.

**Memory plan:** keep the default WSL configuration. The service slot starts one container family at a time (MySQL family exclusive, then PostgreSQL, then Redis, then phpMyAdmin) and stops each family before the next. H3/H4/H6 never run concurrently. Windows Playwright runs outside WSL and is included in the host headroom, not the WSL cap.

### 2.2 Port map

All distros share one network namespace. Use one 100-port block per worker; never bind a default product port in any lane. Service containers publish **no** host ports.

| Purpose | w1 | w2 | w3 | w4 | w5 | w6 | w7 | w8 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Dashboard/API | 24170 | 24270 | 24370 | 24470 | 24570 | 24670 | 24770 | 24870 |
| Proxy HTTP | 24180 | 24280 | 24380 | 24480 | 24580 | 24680 | 24780 | 24880 |
| Proxy HTTPS | 24181 | 24281 | 24381 | 24481 | 24581 | 24681 | 24781 | 24881 |
| Helper forward HTTP | 24182 | 24282 | 24382 | 24482 | 24582 | 24682 | 24782 | 24882 |
| Helper forward HTTPS | 24183 | 24283 | 24383 | 24483 | 24583 | 24683 | 24783 | 24883 |
| Vhost HTTP | 24184 | 24284 | 24384 | 24484 | 24584 | 24684 | 24784 | 24884 |
| Vhost HTTPS | 24185 | 24285 | 24385 | 24485 | 24585 | 24685 | 24785 | 24885 |
| phpMyAdmin relay | 24186 | 24286 | 24386 | 24486 | 24586 | 24686 | 24786 | 24886 |
| Web-service relay | 24187 | 24287 | 24387 | 24487 | 24587 | 24687 | 24787 | 24887 |

Service containers use only Docker-internal addresses. Database operations run through `docker compose exec`; phpMyAdmin and nginx/Apache UIs are exposed to Windows through the relay ports above, which forward to the container IP and container port. Relay processes are per-scenario, bound to `127.0.0.1`, and removed in teardown.

Windows Playwright connects to `http://127.0.0.1:<dashboard>` — never `localhost`, which may resolve to `::1`. Unique IPv4 loopback ports are proven to forward from WSL to Windows on this host; Task 3 adds a direct Windows probe against a worker dashboard before any lane depends on it.

## 3. Coverage ownership

`docs/e2e-lane-matrix.md` contains the full 268-row matrix. Summary:

| Lane | Capabilities | Primary domains |
|---|---:|---|
| H1 | 33 | SITE, ADD (non-DB/non-Windows source), DETAIL (non-source/DB), AUTH shell links |
| H2 | 45 | RT, DETAIL PHP/Node, ADD runtime options |
| H3 | 37 | SVC, SET (non-Windows), XC serving/perf (runs in the service slot) |
| H4 | 30 | DB (except a11y/blocked), ADD/DETAIL DB, CLI DB secondary (service slot) |
| H5 | 16 | DOC (non-Windows), XC failure/security/confinement |
| H6 | 12 | CLI services/DB, XC persistence/concurrency/soak (service slot) |
| L1 | 24 | AUTH, mock breadth, XC a11y/responsive/browsers |
| L2 | 14 | CLI light, contracts, XC network/secret scan, coverage |
| W | 56 | INST, Windows trust/hosts/ports/links/agent, Windows/WSL source modes, `init` |
| BLOCKED | 1 | DB-28 (cycles 9-15) |

Rules:

- A row is **Live** only when its primary lane has a committed, green, repeatable test with backend assertions and a clean secret scan.
- A row with only mock coverage stays **Mock**, even if the secondary lane is green. Secondary rows are recorded as `(id, lane, attempt)` and never satisfy the primary gate.
- Evidence-only rows must link an audit and a `conversion_task`; they do not count toward the release gate.
- DB-28 stays `BLOCKED` until the user explicitly resumes cycles 9-15. It carries a blocked waiver with owner `e2e-program` and expiry `2027-01-31`; expiry fails the gate.
- `INST-24/25/29` are `BLOCKED` until a disposable Windows VM exists. They are not waived and cannot be reported as Live from `-TestSandbox`.

## 4. Worker lifecycle and isolation contract

### 4.1 Run identity

- `E2E_RUN_KEY` matches `^[0-9]{8}T[0-9]{6}Z-[a-z0-9]{6}$`.
- Distro names match `^devstack-e2e-<run>-w[1-8]$`, `^devstack-e2e-<run>-golden$`, or `^devstack-e2e-<run>-linstall$`.
- Home is `/tmp/devstack-e2e/<run>/wN` inside the worker. Helper state, hosts, trust, vhost, nginx/Apache prefixes, Docker network, container names, volumes, evidence, and results shard all include the run key and worker id.
- `E2E_SERVICE_SLOT=1` marks the single lane allowed to use Docker. The slot holds a run-wide lease (`dist/e2e/<run>/locks/service-slot.lock`) for the whole H3/H4/H6 sequence; `worker_up` refuses to start a Docker-enabled lane without it.
- Guards refuse `devstack`, `Ubuntu`, `FedoraLinux-44`, `C:\Users\Aram\.devstack`, `/data/devstack`, protected ports, database user/database names equal to `laravel` or `root`, and any name that does not match the run pattern. Docker guards also assert the engine has no real `devstack-*` containers and that every created container carries `devstack.e2e.run=<run>`.

### 4.2 Golden image and worker bootstrap

Build one clean golden distro, export a VHDX, import eight copies:

```powershell
$run  = "20261102T173000Z-a1b2c3"
$root = "C:\devstack-e2e\runs\$run"
# Use Ubuntu-26.04 when `wsl --list --online` lists it; otherwise use Ubuntu
# and assert /etc/os-release is 26.04 before exporting the golden image.
wsl --install Ubuntu-26.04 --name "devstack-e2e-$run-golden" --location "$root\golden" --no-launch --web-download --version 2
wsl -d "devstack-e2e-$run-golden" -u root -- bash -lc "cd /mnt/c/Users/Aram/Projects/Go/dev-web-stack && bash test/e2e/isolation/linux/bootstrap-worker.sh"
wsl --terminate "devstack-e2e-$run-golden"
wsl --export "devstack-e2e-$run-golden" "$root\golden.vhdx" --format vhd
1..8 | % { wsl --import "devstack-e2e-$run-w$_" "$root\w$_" "$root\golden.vhdx" --vhd --version 2 }
```

Golden bootstrap installs: UID 1000 user `aram`, systemd, Docker Engine, Compose v2, git, make, curl, ca-certificates, python3, sqlite3, openssl, jq, nginx, uidmap, dbus-user-session, and the Playwright system dependencies for Chromium/Firefox/WebKit if browsers run inside the worker. It masks nginx and Docker service auto-start, pre-pulls the pinned images, then removes containers, networks, caches, logs, and `/tmp`. Each clone runs `systemd-machine-id-setup` (or writes a fresh `/etc/machine-id`) before first use; the bootstrap script asserts the ID differs from the golden image.

Build binaries once on Windows and copy them into each worker; do not run `make build` eight times:

```powershell
$env:CGO_ENABLED='0'
go build -trimpath -o dist/e2e/bin/devstack.exe ./cmd/devstack
go build -trimpath -o dist/e2e/bin/devstack-helper.exe ./cmd/devstack-helper
$env:GOOS='linux'; $env:GOARCH='amd64'
go build -trimpath -o dist/e2e/bin/devstack ./cmd/devstack
go build -trimpath -o dist/e2e/bin/devstack-helper ./cmd/devstack-helper
go build -trimpath -o dist/e2e/bin/terminal-probe ./test/e2e/database-management/linux/terminal-probe
Remove-Item Env:GOOS,Env:GOARCH
# Copy once into each imported worker; never rebuild inside the distro.
1..8 | % {
  $dst = "\\wsl$\devstack-e2e-$run-w$_\tmp\e2e\bin"
  New-Item -ItemType Directory -Force -Path $dst | Out-Null
  Copy-Item dist/e2e/bin/devstack,dist/e2e/bin/devstack-helper,dist/e2e/bin/terminal-probe $dst
}
```

### 4.3 Per-worker stack

Each worker runs the unprivileged E2E model already proven by `test/e2e/linux.sh` and `cycle4-env.sh`:

- Helper process as root with a temp config: `socket`, `state_dir`, `hosts`, `trust_dir`, `allow_uid`, `allow_home: $E2E_HOME`, `skip_trust_update: true`, high `forward_*`/`proxy_*`/`vhost_*` ports, temp vhost/nginx/Apache paths.
- Daemon as UID 1000 with `DEVSTACK_HOME=/tmp/devstack-e2e/<run>/wN`, helper socket, high `proxy_http_port`/`proxy_https_port`/dashboard port.
- No systemd unit, no `/etc/hosts`, no real trust store, no `/data/devstack`.
- **Non-Docker lanes** (H1, H2, H5, L1, L2) never start Docker and never bind a fixed service port.
- **Service slot** (H3, H4, H6, one at a time) uses the Ubuntu Docker engine with a unique Compose project `devstack-e2e-<run>-<lane>`, network `devstack-e2e-<run>-<lane>-net`, containers `devstack-e2e-<run>-<lane>-<svc>`, and volumes prefixed the same way. It sets `DEVSTACK_E2E_NO_PUBLISHED_PORTS=1` for the helper/daemon so Compose renders with no `ports:` entries. Database operations use `docker compose exec`; PMA/web UIs use the high-port relays in §2.2.
- Service slot container caps: MySQL 1.5 GB/2 CPU, MariaDB 1.5 GB/2 CPU, PostgreSQL 512 MB/1 CPU, Redis 256 MB/0.5 CPU, phpMyAdmin 256 MB/0.5 CPU, nginx/Apache 256 MB/1 CPU. Only one database family and PMA run at once. Abort a scenario if `free -m` available drops below 1 GB.

### 4.4 Cleanup

Every scenario uses a manifest of created resources. Teardown order: stop sites → unlink databases → stop/remove containers and volumes → stop relays → stop daemon/helper → secret-scan evidence → copy evidence out → remove temp home → assert no matching process/container/network/volume remains. Worker removal:

```powershell
if ($distro -notmatch '^devstack-e2e-[0-9]{8}T[0-9]{6}Z-[a-z0-9]{6}-(w[1-8]|golden|linstall)$') { throw }
wsl --terminate $distro
wsl --unregister $distro
Remove-Item -Recurse -Force "C:\devstack-e2e\runs\$run\$suffix"
```

`Remove-E2EWorkers` removes `w1..w8`, `golden`, and `linstall`; Task 21 additionally asserts that no `devstack-e2e-*` distro, container, network, volume, home, lock, or relay survives. `wsl --shutdown` is forbidden here; it would stop the real install. Orphan bridges/iptables from the single shared engine are recorded as residual risk; do not run a global shutdown without explicit user approval.

---

## 5. Harness phases and tasks

### Phase 0 — Preflight, capacity, and baseline

#### Task 0: Preflight and host baseline

**Files:**
- Create: `test/e2e/windows/New-E2ERun.ps1`, `test/e2e/windows/Set-E2ECapacity.ps1`, `test/e2e/windows/Test-E2EPreflight.ps1`
- Create: `test/e2e/lib/preflight.sh`
- Evidence: `dist/e2e/<run>/phase0/`

**Interfaces:**
- Consumes: current `devstack.exe`, `doctor --json`, `site list --json`, `service list --json`, `trust status --json`, `runtime list --json`, hosts hash, listener table, WSL distro list, `C:\devstack` links, helper service state.
- Produces: `phase0/baseline.json`, `phase0/state.db`, `phase0/config.yaml`, `phase0/helper.json`, `phase0/hosts`, `phase0/hosts.sha256`, `phase0/listeners.txt`, `phase0/links.txt`, `phase0/wsl.txt`, `phase0/doctor.json`, and a layout decision (`5-parallel+service-slot`) in `run.json`.

- [ ] **Step 1: Write the failing preflight test**

`test/e2e/windows/Test-E2EPreflight.ps1` asserts: clean git tree; `go test ./...` / `go vet ./...` green; `doctor.ok=true`; the real listeners `80, 443, 3306, 7070, 7443, 8080, 8082, 8443` are present with expected owners; `C:\devstack` links intact; host free RAM ≥ 10 GB; C: free ≥ 120 GB; WSL distro list contains `devstack`, `Ubuntu`, `FedoraLinux-44`; `laravel` database/user still exist and are not scheduled for deletion.

- [ ] **Step 2: Run it and confirm the current gaps**

Run: `pwsh -NoProfile -File test/e2e/windows/Test-E2EPreflight.ps1 -RunKey $run`
Expected: FAIL on the first missing baseline file or precondition.

- [ ] **Step 3: Implement snapshot and layout decision**

Capture the baseline, hash hosts, save `state.db` + `config.yaml` + helper config, and write `run.json` with layout `5-parallel+service-slot`. Record free RAM and the service-slot memory budget. Do not change `.wslconfig` and do not run `wsl --shutdown`.

- [ ] **Step 4: Re-run and verify baseline**

Expected: PASS, `baseline.json` contains all fields, `run.json` names the lane layout and confirms one Docker service slot.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/windows/New-E2ERun.ps1 test/e2e/windows/Set-E2ECapacity.ps1 test/e2e/windows/Test-E2EPreflight.ps1 test/e2e/lib/preflight.sh
git commit -m "test(e2e): add run preflight and host baseline"
```

### Phase 1 — Harness foundation

#### Task 1: Run context, guards, and port map

**Files:**
- Create: `test/e2e/lib/run-context.sh`, `test/e2e/lib/guards.sh`, `test/e2e/lib/ports.sh`, `test/e2e/lib/run-context_test.sh`
- Create: `test/e2e/windows/E2EContext.psm1`
- Modify: `test/e2e/database-management/linux/cycle4-env.sh` (allow `E2E_ALLOWED_DISTRO` and the `/tmp/devstack-e2e/<run>/wN` home layout), `test/e2e/database-management/linux/cycle5-env.sh`

**Interfaces:**
- `e2e_context_init <lane> <worker>` exports `E2E_RUN_KEY`, `E2E_WORKER_ID`, `E2E_WORKER_KIND`, `E2E_DISTRO`, `E2E_HOME`, `E2E_PORT_BASE`, `E2E_SERVICE_SLOT`, `E2E_EVIDENCE_DIR`, `E2E_RESULTS_SHARD`.
- `e2e_guard` aborts on protected distro/home/port, `laravel` database/user names, and missing service-slot lease; `e2e_port <purpose>` returns the per-worker port.
- PowerShell `New-E2EContext -Lane -Worker`, `Assert-E2ESafeTarget`.

- [ ] **Step 1: Write the failing guard test**

`run-context_test.sh` must assert: `e2e_guard` rejects `devstack`, `Ubuntu`, `FedoraLinux-44`, `C:\Users\Aram\.devstack`, `/data/devstack`, ports `80,443,3306,5432,6379,7070,7443,8080,8082,8443`, database/user names `laravel` and `root`, and any Docker container not labeled `devstack.e2e.run=<run>`; accepts `devstack-e2e-20261102T173000Z-a1b2c3-w1`, `...-golden`, `...-linstall`, and `/tmp/devstack-e2e/20261102T173000Z-a1b2c3/w1`; `e2e_port dashboard` for w1=24170 and w8=24870; `E2E_SERVICE_SLOT=1` is refused without the run-wide lease.

- [ ] **Step 2: Run it, verify it fails**

Run: `bash test/e2e/lib/run-context_test.sh`
Expected: FAIL because the functions do not exist.

- [ ] **Step 3: Implement the context, guards, and ports**

Use a single `E2E_PORT_BASE=$((24070 + 100 * worker))`; derive all other ports by offset. Keep the canonical protected-port list in one file and source it from both bash and PowerShell. The guard reads the real database name/user deny-list from a single file; `laravel` and `root` are hard failures even if a test asks for them.

- [ ] **Step 4: Run the test and a five-worker collision check**

Run: `bash test/e2e/lib/run-context_test.sh`
Expected: PASS. Then start dummy listeners on w1, w2, w5, w7, w8 dashboard ports and assert all bind and are distinct; assert a w1 attempt to bind 3306 is refused.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/lib/run-context.sh test/e2e/lib/guards.sh test/e2e/lib/ports.sh test/e2e/lib/run-context_test.sh test/e2e/windows/E2EContext.psm1 test/e2e/database-management/linux/cycle4-env.sh test/e2e/database-management/linux/cycle5-env.sh
git commit -m "test(e2e): add worker context, guards, and port map"
```

#### Task 2: Golden image and disposable workers

**Files:**
- Create: `test/e2e/isolation/New-E2EWorkers.ps1`, `Remove-E2EWorkers.ps1`, `Test-E2EIsolation.ps1`, `linux/bootstrap-worker.sh`, `README.md`

**Interfaces:**
- `New-E2EWorkers -RunKey -Layout '5-parallel+service-slot'` creates the golden distro, imports the workers, writes `workers.json` with names/ports/homes.
- `Remove-E2EWorkers -RunKey` terminates, unregisters, and deletes only matching distros/directories, including `golden` and `linstall`.

- [ ] **Step 1: Write the failing isolation test**

`Test-E2EIsolation.ps1 -DryRun` mocks `wsl` and asserts: golden built once; 8 imports; unique names; no protected distro; teardown removes only `devstack-e2e-<run>-*` including `golden` and `linstall`; a bad run key is refused.

- [ ] **Step 2: Run it, verify it fails**

Run: `pwsh -NoProfile -File test/e2e/isolation/Test-E2EIsolation.ps1 -DryRun`
Expected: FAIL because the module is missing.

- [ ] **Step 3: Implement the golden bootstrap and import**

Bootstrap the golden distro, clean it, export VHDX, import workers. Write `workers.json` with `name`, `kind`, `home`, `port_base`, `distro`, `service_slot`. There is no multi-engine gate; only w3/w4/w6 may set `service_slot: true`, and only one of them may hold the lease at a time.

- [ ] **Step 4: Run a 5-worker non-Docker smoke**

Create w1, w2, w5, w7, w8; run `bootstrap-worker.sh`; start one static site per worker; reach each dashboard from Windows on its 24xxx port; assert all five stacks are alive and distinct; remove all workers. Expected: PASS, no distro/files left, and no Docker engine started.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/isolation/
git commit -m "test(e2e): add golden WSL image and worker lifecycle"
```

#### Task 2A: Serialized Docker service slot

**Files:**
- Create: `test/e2e/service-slot/up.sh`, `down.sh`, `relay.go`, `no-ports_test.sh`, `README.md`
- Modify: `internal/helper/service_docker.go` (test-only `DEVSTACK_E2E_NO_PUBLISHED_PORTS` handling)
- Modify: `internal/services/compose.go` or add a narrow catalog filter used only by the helper when the env var is set
- Test: `internal/helper/service_docker_test.go`, `internal/services/compose_test.go`, `test/e2e/service-slot/no-ports_test.sh`

**Interfaces:**
- `DEVSTACK_E2E_NO_PUBLISHED_PORTS=1` is accepted only when the helper home is under `/tmp/devstack-e2e/<run>/`; otherwise startup fails closed.
- With the env var set, rendered Compose has no `ports:` entries for MySQL, MariaDB, PostgreSQL, Redis, or phpMyAdmin. The default catalog and normal helper behavior are unchanged.
- `service-slot/up.sh <lane> <worker>` acquires `service-slot.lock`, starts the shared engine, writes `service-slot.env`, and starts the PMA/web relays.
- `service-slot/down.sh <lane> <worker>` removes containers, networks, volumes, relays, and the lease.

- [ ] **Step 1: Write the failing no-port and relay tests**

`no-ports_test.sh` asserts: default Compose still contains `127.0.0.1:3306:3306` and `127.0.0.1:8082:80`; E2E mode removes them; a fake listener on 3306/8082 does not block activation; a container started in E2E mode is reachable by `docker compose exec` and by the relay from a high port; the relay refuses a protected target port.

- [ ] **Step 2: Run it, verify it fails**

Run: `bash test/e2e/service-slot/no-ports_test.sh`
Expected: FAIL because the env handling and relay do not exist.

- [ ] **Step 3: Implement the test-only filter and relay**

Filter only published host ports; keep container ports, health checks, network aliases, and PMA's internal `db:3306` target. The relay is a small Go program that listens on `127.0.0.1:<24x86|24x87>` and forwards to a container IP/port supplied by `docker inspect`; it never accepts a protected port as a target.

- [ ] **Step 4: Run the test with the real install holding 3306/8082**

Run: `bash test/e2e/service-slot/no-ports_test.sh`
Expected: PASS while `Get-NetTCPConnection -LocalPort 3306,8082` still shows the real install's listeners; no new listener on 3306/8082; Windows reaches the PMA relay on 24186 (or the active worker's relay port).

- [ ] **Step 5: Commit**

```bash
git add test/e2e/service-slot/ internal/helper/service_docker.go internal/helper/service_docker_test.go internal/services/compose.go internal/services/compose_test.go
git commit -m "test(e2e): add serialized service slot with no published ports"
```

#### Task 3: Worker stack bootstrap

**Files:**
- Create: `test/e2e/worker/lib.sh`, `up.sh`, `down.sh`, `health.sh`, `smoke.sh`

**Interfaces:**
- `worker_up <worker>` writes `$E2E_HOME/worker.env` with `E2E_DS`, `E2E_HELPER_BIN`, `E2E_UI_URL`, `E2E_PROXY_HTTP_PORT`, `E2E_PROXY_HTTPS_PORT`, `E2E_FORWARD_HTTP_PORT`, `E2E_FORWARD_HTTPS_PORT`, `E2E_PMA_RELAY_PORT`, `E2E_WEB_RELAY_PORT`, `E2E_SERVICE_SLOT`.
- `worker_down <worker>` stops daemon/helper/containers/relays and asserts no leftovers.
- Binaries are copied from `dist/e2e/bin` into `/tmp/e2e/bin` during bootstrap; no worker runs `go build`.

- [ ] **Step 1: Write the failing smoke**

`smoke.sh` starts one worker, runs `site add --kind static`, curls the site through the proxy and forwarder over HTTPS, restarts the daemon, asserts the site is reconciled exactly once, stops everything, and asserts no process/container remains.

- [ ] **Step 2: Run it, verify it fails**

Run: `bash test/e2e/worker/smoke.sh w1`
Expected: FAIL because `worker_up` is missing.

- [ ] **Step 3: Implement helper/daemon startup and teardown**

Reuse the helper config shape from `test/e2e/linux.sh:392-410` and the daemon readiness check. Use the worker's port map, not random ports. Write the token to `$E2E_HOME/secrets/ui-token` (0600), never to stdout. Add a Windows probe: `Invoke-WebRequest http://127.0.0.1:<dashboard>/` must return the dashboard shell before any lane starts.

- [ ] **Step 4: Run the 5-worker smoke**

Start w1, w2, w5, w7, w8; start one static site per worker; assert all five dashboards are reachable from Windows on their 24xxx ports and all sites serve distinct content; no port collision. Then start the single service slot and assert it can acquire the lease while the five workers run.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/worker/
git commit -m "test(e2e): add disposable worker stack bootstrap"
```

#### Task 4: Evidence, results, secret scan, and baseline

**Files:**
- Create: `test/e2e/lib/evidence.sh`, `test/e2e/lib/results.sh`, `test/e2e/lib/secret-scan.sh`, `test/e2e/lib/evidence_test.sh`
- Create: `test/e2e/schema/evidence.schema.json`, `test/e2e/schema/results.schema.json`
- Create: `test/e2e/windows/HostBaseline.psm1`, `test/e2e/windows/HostBaseline.Tests.ps1`

**Interfaces:**
- `e2e_evidence_init <lane> <scenario>` creates the evidence directory and `manifest.json`.
- `e2e_record_result <id> <lane> <attempt> <status> <evidence> [notes]` appends a schema-valid JSONL row keyed by `(id, lane, attempt)`. Secondary-lane rows are allowed; the aggregator selects only a primary-lane PASS.
- `e2e_secret_scan <dir>` exits 1 on a hit and prints only redacted locations.
- Result fields: `id`, `lane`, `attempt`, `status`, `commit`, `evidence`, `secret_scan`, `conversion_task`, `blocked{owner,reason,expiry}`, `quarantine{owner,expiry}`, `notes`.
- `Save-HostBaseline` / `Restore-HostBaseline` snapshot and verify: hosts file bytes, CA thumbprints (Windows root store and Firefox policy marker), helper service/DACL/firewall rules, scheduled tasks, `C:\devstack` links and README hashes, listener table, real state DB/config/secret hashes, PATH, `.wslconfig` bytes, and WSL distro list; restore runs in a `finally` and verifies actual values, not just counts.

- [ ] **Step 1: Write the failing tests**

`evidence_test.sh` creates a result with a duplicate `(id,lane,attempt)`, an invalid status, a secondary-only P0, an expired waiver, a mock-only P0, and a fake secret in a log. It asserts each bad case is rejected and the scanner exits 1 with a redacted message. `HostBaseline.Tests.ps1` snapshots temp hosts, CA thumbprints, helper config, links, and listeners, mutates each, restores, and asserts every value matches, including a CA replacement where the count stays the same.

- [ ] **Step 2: Run them, verify they fail**

Run: `bash test/e2e/lib/evidence_test.sh` and `pwsh -NoProfile -File test/e2e/windows/HostBaseline.Tests.ps1`
Expected: FAIL.

- [ ] **Step 3: Implement the modules**

Promote the database harness patterns from `test/e2e/database-management/{results,secret-scan,rollback}.sh`; add a per-worker JSONL shard and an atomic merge (lock file) so parallel workers cannot lose rows. Normalize evidence paths to repo-relative. Set Playwright `outputDir` under the evidence tree and include HTML reports/test-results in the secret scan.

- [ ] **Step 4: Run the tests and a two-shard merge**

Expected: PASS; merged results contain both shards exactly once; scanner self-test passes; a secondary row never satisfies a P0 primary gate.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/lib/evidence.sh test/e2e/lib/results.sh test/e2e/lib/secret-scan.sh test/e2e/lib/evidence_test.sh test/e2e/schema/ test/e2e/windows/HostBaseline.psm1 test/e2e/windows/HostBaseline.Tests.ps1
git commit -m "test(e2e): add evidence, results, secret scan, and host baseline"
```

#### Task 5: Live Playwright shared fixtures

**Files:**
- Create: `ui/tests/live/fixtures.ts`, `ui/tests/live/auth.ts`, `ui/tests/live/evidence.ts`, `ui/tests/live/smoke.live.spec.ts`, `ui/tests/live/README.md`
- Modify: `ui/playwright.live.config.ts` (`testMatch` for the new specs), `ui/playwright.live-site.config.ts`

**Interfaces:**
- `test` fixture reads `DEVSTACK_E2E_LIVE`, `DEVSTACK_UI_URL`, `DEVSTACK_E2E_TOKEN`; skips when missing.
- `authenticate(page)` navigates with the token fragment and asserts the token is gone from URL/history/storage/body.
- `assertNoConsoleErrors(page)`, `assertNoSecretLeak(page, secret)`.
- Secret-bearing specs call `test.use({ trace: 'off', video: 'off', screenshot: 'off' })`. All other live specs use `trace: 'retain-on-failure'` and screenshot only on failure for non-secret pages.
- `ui/playwright.live.config.ts` and `ui/playwright.live-site.config.ts` set `outputDir` and the HTML reporter output under `dist/e2e/<run>/<lane>/playwright`, never the repo root or `ui/playwright-report`.
- The evidence scanner scans `dist/e2e/**` including `test-results`, traces, screenshots, and HTML reports.

- [ ] **Step 1: Write the failing smoke spec**

`smoke.live.spec.ts` authenticates against a worker, asserts the Sites heading, asserts no token leak, asserts zero console errors, reads `/api/v1/session`, and asserts the CSRF token is 64 hex chars and differs from the install token.

- [ ] **Step 2: Run it against the real install, verify it passes only read-only**

Run:
```powershell
$env:DEVSTACK_E2E_LIVE='1'; $env:DEVSTACK_UI_URL='http://127.0.0.1:7070'
$env:DEVSTACK_E2E_TOKEN = (Get-Content 'C:\Users\Aram\.devstack\secrets\ui-token' -Raw)
$env:PATH='C:\Program Files\nodejs;'+$env:PATH
npm --prefix ui run test:e2e:live -- --grep smoke
```
Expected: PASS. If the real install has no token file, use `devstack ui --json` and read `open_url` without printing it.

- [ ] **Step 3: Run it against a worker and add the leak/negative fixtures**

Start `test/e2e/worker/up.sh w1`, set `DEVSTACK_UI_URL=http://127.0.0.1:24170`, and run the same spec. Add negative cases for missing token, wrong token, stale session, and wrong CSRF.

- [ ] **Step 4: Verify the fixture refuses secrets in artifacts**

Run with a deliberately leaked fake token in body text; assert the fixture fails and the evidence scanner catches it.

- [ ] **Step 5: Commit**

```bash
git add ui/tests/live/ ui/playwright.live.config.ts ui/playwright.live-site.config.ts
git commit -m "test(ui): add shared live Playwright fixtures and leak guards"
```

#### Task 6: Coverage manifest and aggregator

**Files:**
- Create: `test/e2e/coverage/manifest.json` (268 rows, generated from `docs/e2e-coverage.md` and `docs/e2e-lane-matrix.md`)
- Create: `test/e2e/coverage/schema.json`, `aggregate.py`, `test_aggregate.py`, `README.md`
- Create: `test/e2e/gates/pr.json`, `nightly.json`, `release.json`
- Modify: `Makefile` (add `e2e-coverage`)

**Interfaces:**
- Manifest row: `id`, `domain`, `capability`, `layer`, `priority`, `primary_lane`, `secondary_lane`, `coverage[]`, `harness_paths[]`, `test_ids[]`, `gate`, `waiver{owner,reason,expiry}`, `blocked{owner,reason,expiry}`, `quarantine{owner,expiry}`, `conversion_task`, `evidence[]`.
- `aggregate.py --results dist/e2e/<run> --manifest ... --out dist/e2e/<run>/coverage.json` merges result shards keyed by `(id, lane, attempt)`, validates schema, checks duplicate/missing IDs, selects only a **primary-lane** Live/System PASS, fails P0 without one, fails stale commit SHA, fails a missing secret scan, fails an expired waiver, and fails a `BLOCKED` row unless its blocked waiver is current.
- `DB-28` is a `BLOCKED` row with a current blocked waiver; `INST-24/25/29` are `BLOCKED` VM-required rows and cannot be satisfied by `-TestSandbox`. P0 has no waiver path.

- [ ] **Step 1: Write the failing aggregator test**

`test_aggregate.py` feeds fixtures with: 268 IDs, one missing P0, one duplicate `(id,lane,attempt)`, one stale SHA, one missing secret scan, one secondary-only P0, one mock-only P0, one expired waiver, one blocked row with a valid waiver, one blocked row with an expired waiver, and one valid primary-lane P0. It asserts the expected exit code for each case.

- [ ] **Step 2: Run it, verify it fails**

Run: `python test/e2e/coverage/test_aggregate.py`
Expected: FAIL because the aggregator is missing.

- [ ] **Step 3: Generate the manifest and implement the aggregator**

Generate one row per ID from the matrix; assert the count is 268 and every ID matches `docs/e2e-coverage.md`. Implement the gate rules from `docs/e2e-coverage.md` section 6. A secondary lane result is stored but never promotes the row; an evidence-only row must name a `conversion_task`.

- [ ] **Step 4: Run the test and the manifest count check**

Expected: PASS; `manifest.json` has 268 unique IDs; every P0 has a primary lane and a gate; DB-28 and the VM-required rows are explicitly blocked.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/coverage/ test/e2e/gates/ Makefile
git commit -m "test(e2e): add coverage manifest, aggregator, and gate manifests"
```

#### Task 7: Run-wide lease and host baseline

**Files:**
- Create: `test/e2e/windows/Invoke-SerializedLane.ps1`, `test/e2e/windows/Test-SerializedLock.ps1`
- Create: `test/e2e/lib/lease.sh`
- Modify: `test/e2e/windows/HostBaseline.psm1`, `test/e2e/worker/up.sh`, `test/e2e/service-slot/up.sh`

**Interfaces:**
- `Invoke-SerializedLane -Name <lane> -Script <block>` takes an exclusive run-wide lease (`dist/e2e/<run>/locks/run.lease`), snapshots the baseline, runs the block, restores in `finally`, verifies, and writes evidence.
- A second concurrent invocation exits with `SERIALIZED_LANE_BUSY` and does not touch the host.
- `worker_up` and `service-slot/up.sh` acquire a shared read lease; they refuse to start while the W lane holds the exclusive lease.
- W refuses to start while any `E2E_SERVICE_SLOT`, `worker_up`, or `devstack-e2e-*` process/distro is alive.
- Stale leases older than the run timeout are removed only after verifying the owning process is gone; crashes release the lease via the OS handle.

- [ ] **Step 1: Write the failing lock test**

Start an exclusive `Invoke-SerializedLane` and a concurrent `worker_up`; assert the worker exits with `SERIALIZED_LANE_BUSY` and does not start. Start two exclusive calls; assert exactly one runs. Kill a lease owner and assert a new run can recover. Assert hosts hash, CA thumbprints, and listener table are unchanged after the winner.

- [ ] **Step 2: Run it, verify it fails**

Expected: FAIL because the lease does not exist.

- [ ] **Step 3: Implement the lease, snapshot, restore, and verify**

Use a named mutex plus a lock file under `dist/e2e/<run>/locks/`. The W lane owns the exclusive lease; parallel workers and the service slot own shared leases. Refuse to run if the real daemon is mid-upgrade or the helper service is stopped. Restore and verify the expanded `HostBaseline` fields from Task 4.

- [ ] **Step 4: Run the test**

Expected: PASS, including stale-lease recovery and crash release.

- [ ] **Step 5: Commit**

```bash
git add test/e2e/windows/Invoke-SerializedLane.ps1 test/e2e/windows/Test-SerializedLock.ps1 test/e2e/windows/HostBaseline.psm1
git commit -m "test(e2e): serialize host-wide lanes with baseline restore"
```

### Phase 2 — Parallel live lanes

Each lane task follows the same contract:

- Start only its worker (`test/e2e/worker/up.sh wN`).
- Create fixtures under `$E2E_HOME/projects`; never use the real projects root.
- Write a shell/API test for each ID group and a Playwright spec for the UI group.
- Every UI assertion must be followed by an API/CLI/database assertion of persisted state.
- Add a deliberate mutation check: break the expected state (or use a fault injection) and prove the test fails.
- On failure, capture evidence, triage the defect ledger, fix under TDD if the product is wrong, and rerun from the start.
- End with `worker_down`, secret scan, cleanup, and a commit.

#### Task 8: H1 — Sites list and Add Site

**Files:**
- Create: `test/e2e/worker/h1/run.sh`, `fixtures.sh`, `sites-list.sh`, `add-site.sh`
- Create: `ui/tests/live/sites-list.live.spec.ts`, `ui/tests/live/add-site.live.spec.ts`
- Modify: `ui/playwright.live.config.ts`

**Covers:** SITE-01..11; ADD-01..20, 23, 25 (excluding the Windows portions of ADD-04/09/10, which are W-primary); CLI-04/05/22; AUTH-07/08/13/15 (site-shell parts); XC-16 local. H1 records ADD-04/09/10 local/WSL cases as **secondary** only; W owns the full source-mode proof.

**Fixtures:** `e2e-static` (running), `e2e-node` (Node 24 + start command), `e2e-php` (PHP 8.4 + front controller), `e2e-stopped`, `e2e-crashed` (killed backend), projects `alpha/`, `beta/public/`, `projects-evil/` sibling, `escape -> /tmp/outside`, hidden `.next`/`dist/.vite`, SPA fixture, build fixture. H4 owns ADD-21/22/24 (DB-coupled Add Site); H1 does not start Docker.

- [ ] **Step 1: Create the H1 fixtures and write the first failing shell test**

Assert static/Node/PHP sites and the path-trap fixtures exist; assert the path traps are outside the managed root.

- [ ] **Step 2: Write the site-list and quick-action cases**

Cover list/summary, search, filters, badges, URL/open, quick start/stop/restart, delete confirmation + linked-DB warning, empty/loading/error, mobile card layout, SSE refresh, runtime-inventory failure. After each quick action, assert `/api/v1/sites` and the process table. Also assert CLI-04/05/22 human/`--json` parity for site add/list/show/start/stop/restart and the local source path.

- [ ] **Step 3: Write the Add Site cases**

Cover wizard back/cancel/edit-back, name slug + validation, static/Node/PHP, source modes, manual path, folder browser, new folder, root/outside/sibling/`..`/symlink rejection, namespace validation, document root, SPA fallback, Node/PHP inherit/exact/preflight, front controller, AllowOverride, static build profile/env, site env, HTTPS/force/www, review, create/start, duplicate/path/runtime/create failure, and Windows-default assertions delegated to W.

- [ ] **Step 4: Run against H1, verify failures are real**

Run: `bash test/e2e/worker/h1/run.sh w1`.
Expected: any failure points to a real product or harness gap; capture evidence and triage D4/D7.

- [ ] **Step 5: Fix confirmed defects under TDD and rerun**

For each confirmed defect: failing test -> minimal fix -> focused tests -> full Go/UI gate -> commit -> push. Then rerun H1 until green.

- [ ] **Step 6: Secret scan, cleanup, and commit**

Run `e2e_secret_scan` and `worker_down`. Commit the H1 specs and any fixes.

#### Task 9: H2 — Site detail, runtimes, toolchains, PHP/Node execution

**Files:**
- Create: `test/e2e/worker/h2/run.sh`, `site-detail.sh`, `runtime-install.sh`, `php-node.sh`, `failure.sh`
- Create: `ui/tests/live/site-detail.live.spec.ts`, `ui/tests/live/runtimes.live.spec.ts`
- Modify: `ui/playwright.live.config.ts`

**Covers:** DETAIL-01..07, 11..22, 24, 25; RT-01..18; CLI-07..11/25; RT-14/15/16 shell/exec with L2 secondary.

**Fixtures:** PHP 8.4 and 8.5 builds, Node 22 and 24, stale site pin, corrupt runtime directory, offline catalog, Composer, a PHP site with `php.ini` overrides/extensions, a Node site with start command and build, static/Node/PHP sites for detail lifecycle.

- [ ] **Step 1: Write the failing runtime lifecycle test**

Assert list/available/usage, install with checksum/size verification, remove, in-use removal block, migrate-before-remove, PHP line defaults, CLI defaults, global ini, Node release refresh/offline/stale/cache, exact Node preflight, Composer install/update/remove, and the `php`/`composer`/`node`/`npm`/`npx` shims.

- [ ] **Step 2: Write the site-detail cases**

Cover overview/status/url/cert/source mode, copy/open, start/stop/restart, delete, docroot/start command/front controller/AllowOverride/SPA edits, site env, build profile/build env/run/build log/failed build rollback, logs snapshot/stream/pause/tail, terminal open/env/resize/Ctrl+C/reconnect/idle, vhost preview, not-found/load-failure/stale route. After each edit/action, assert the API/CLI persisted state and the live process.

- [ ] **Step 3: Write the PHP/Node execution cases**

Cover effective PHP config, version/build switch with rollback, extensions and per-site `php.ini`, PHP execution in site context, Node pin/inherit/switch, stale pin install-first, ABI rebuild warning, build switch rollback.

- [ ] **Step 4: Run against H2, verify failures**

Run: `bash test/e2e/worker/h2/run.sh w2`.
Expected: failures are real or reveal the catalog seam gap (D13).

- [ ] **Step 5: Fix confirmed defects and rerun**

Use TDD, full Go gate, commit, push. If RT-17/18 require a test-only catalog seam, add the seam behind a build tag or test env var, not production behavior.

- [ ] **Step 6: Secret scan, cleanup, commit**

#### Task 10: H3 — Services, settings, serving, performance

**Files:**
- Create: `test/e2e/worker/h3/run.sh`, `services.sh`, `config.sh`, `serving.sh`, `performance.sh`
- Create: `ui/tests/live/services.live.spec.ts`, `ui/tests/live/settings.live.spec.ts`
- Modify: `ui/playwright.live.config.ts`

**Covers:** SVC-01..18; SET-01, 02, 04, 05, 07..13, 15..17, 19, 20; XC-17/18/25. SET-03/06/14/18 are W-primary cross-checks.

**Service slot:** H3 runs under `service-slot/up.sh h3 w3`, holds the service slot lease, uses `DEVSTACK_E2E_NO_PUBLISHED_PORTS=1`, and exposes PMA/web UIs through the 24xxx relays.

**Fixtures:** nginx, Apache, MySQL, MariaDB, PostgreSQL, Redis, phpMyAdmin; exclusive-group conflict; unhealthy container; data-present uninstall; config with foreign/keep/use/merge conflict; performance profiles; benchmark history; a PHP site behind external nginx/Apache. SET-19 verifies the custom dashboard-port path (`internal/config` already permits a non-7070 UI port in external mode); if the live preflight fails, move SET-19 to W and record a defect.

- [ ] **Step 1: Write the failing service lifecycle test**

Cover catalog/groups, install/pull/scaffold, activate/deactivate/restart, uninstall keep-data vs delete-data, exclusive conflicts, health badges, logs, credentials reveal/copy/rotate, PMA launch only when active and auto-login as root, config list/view/diff/apply/reset/keep/use/merge, deactivation impact, SSE, loading/empty/error/in-flight, Docker down/pull failure/health timeout, port conflict/stale container.

- [ ] **Step 2: Write the settings/serving/performance cases**

Cover machine readout, auto-start/graceful/log/max-body readout, WSL info (W), listener ports + invalid/out-of-range/conflict, fixed Windows ports (W), serving builtin/nginx/Apache, status/sync/preview, prerequisites/bridge include, vhost validate/reload/rollback, default-server conflict, TLS custody/.htaccess/custom docroot, PHP/Node/Composer defaults, trust repair/removal (W for real), daemon info/uninstall guidance, performance profile/preview/apply/reset/benchmark, host tuning (W), external nginx/Apache PHP, serving-mode rollback.

- [ ] **Step 3: Run against H3, verify failures**

Run: `bash test/e2e/worker/h3/run.sh w3`.
Expected: failures are real; triage D10 (SET-02 read-only) and any service-config rollback gap. Every failure gets a failing test before a fix.

- [ ] **Step 4: Fix confirmed defects under TDD and rerun**

For each defect: failing test → run and confirm fail → minimal fix → focused tests → full Go/UI gate → commit → push → rerun.

- [ ] **Step 5: Secret scan, cleanup, release service slot, commit**

Run `service-slot/down.sh h3 w3`, then `e2e_secret_scan` and commit.

#### Task 11: H4 — Databases and service detail

**Files:**
- Create: `test/e2e/worker/h4/run.sh`, `db-core.sh`, `db-users.sh`, `db-backup.sh`, `db-jobs.sh`, `db-redis-pma.sh`, `db-parity.sh`
- Create: `ui/tests/live/database-management.live.spec.ts`, `ui/tests/live/pma-autologin.live.spec.ts`
- Reuse: `test/e2e/database-management/fixtures/**` (SQL, dumps, Redis ACL); do **not** run cycles 9-15.
- Modify: `ui/playwright.live.config.ts`

**Covers:** DB-01..25, 27; ADD-21/22/24; DETAIL-23; CLI-16..19/24 backend assertions. DB-26 is L1-primary a11y; DB-28 is blocked.

**Service slot:** H4 runs under `service-slot/up.sh h4 w4`, holds the service slot lease, uses `DEVSTACK_E2E_NO_PUBLISHED_PORTS=1`, and reaches PMA through the relay. It uses helper `docker compose exec`, never host-published 3306/5432/6379/8082.

**Fixtures:** MySQL 8.4, MariaDB 11.8, PostgreSQL 18, Redis 8.8, phpMyAdmin 5.2; databases `e2e_app`, `e2e_reserved`, `e2e_long_63`; managed/external/metadata-only/protected users; seed/corrupt/empty/truncated/partial/binary/non-SQL dumps; backup/restore/import/export jobs; site links and env overrides. The guard rejects `laravel`, `root`, and any name without the `e2e_` prefix for destructive tests.

- [ ] **Step 1: Write the failing DB core test**

Cover tabs/deep links, inventory, create with safe-name validation, rename PostgreSQL, drop with typed confirmation, backup-first deletion, protected names, linked-database block until site stopped. Run it, confirm it fails for the missing harness, then implement the harness.

- [ ] **Step 2: Write users/grants/passwords**

Cover list, create with grants, edit/revoke, delete typed confirmation, set/reveal/copy/rotate/reset, metadata-only/external users, protected users disabled, site attribution, no secrets in lists/logs/HTML.

- [ ] **Step 3: Write backups/jobs/import/export**

Cover create/list/download/delete, restore with overwrite confirmation, import SQL, export, corrupt/empty/truncated/partial/binary/non-SQL rejection without replacing data, job progress/failure/cancel/refresh recovery, SSE inventory refresh, overlapping responses.

- [ ] **Step 4: Write Redis/PMA/parity**

Cover Redis password + ACL persistence, **phpMyAdmin auto-login as the active root user**, active-database follow, MariaDB/MySQL parity through normalized JSON. For PMA, assert:
  - the dashboard's `Open phpMyAdmin` link opens the PMA UI without a login form;
  - the logged-in user is `root` and the server is the active engine (`db:3306`);
  - `config.user.inc.php` contains `auth_type = 'config'` and no literal password;
  - `services/phpmyadmin/secrets/root_password` matches the active engine's root secret and is mode `0644` inside a `0700` directory;
  - switching MySQL→MariaDB and rotating the engine credentials refreshes the PMA secret and the next open still auto-logs in;
  - no root password appears in HTML, logs, traces, screenshots, API payloads, or evidence.

- [ ] **Step 5: Run against H4, verify failures**

Run: `bash test/e2e/worker/h4/run.sh w4`.
Expected: failures are real; triage D5 (backup-first), D6 (backup delete typed confirm), D4 (site-create DB atomicity).

- [ ] **Step 6: Fix confirmed defects under TDD and rerun**

- [ ] **Step 7: Secret scan, cleanup, release service slot, commit**

**Do not** create or run `cycle9*`-`cycle15*`. DB-28 stays blocked.

#### Task 12: H5 — Doctor, temp trust/hosts/ports, failure, security

**Files:**
- Create: `test/e2e/worker/h5/run.sh`, `doctor.sh`, `trust-hosts.sh`, `ports.sh`, `failure.sh`, `security.sh`
- Create: `ui/tests/live/doctor.live.spec.ts`
- Modify: `ui/playwright.live.config.ts`

**Covers:** DOC-01..06, 09, 11, 12, 13, 16, 17 (17 with L2); XC-01/06/07/08/22; SET-14 **temp helper trust** only.

**Isolation:** H5 runs no Docker and never invokes real `devstack trust remove/repair`, global `iptables`, DNS, VPN, proxy, or IPv6 changes. Failure injection is process-local: fake clients, a child process with bad env, a temp hosts/trust dir, a temp state copy, and a loopback-only fault proxy. Global network variants belong to W.

**Fixtures:** temp hosts file, temp trust dir, temp vhost prefix, corrupt runtime dir, low-disk tmpfs, a bad state DB copy, a killed helper/daemon, a killed Docker container (simulated status), a symlink escape, a junction escape (W secondary).

- [ ] **Step 1: Write the failing doctor test**

Cover run/repair/rerun, remediation text + last-run time, helper/CA/hosts/projects-root/runtimes/disk/serving/PHP checks, injected hosts/trust/port/runtime/vhost/PHP drift, second run green, doctor JSON/API parity and stable check IDs. Run it, confirm the harness gap, then implement.

- [ ] **Step 2: Write the failure/security cases**

Cover daemon/helper/Docker/network failure and retry **without global network mutation**, port/runtime/permission/disk conflicts, atomic writes/idempotency, confinement, corrupt state, interrupted migration, and the secret scan across evidence.

- [ ] **Step 3: Run against H5, verify failures**

Run: `bash test/e2e/worker/h5/run.sh w5`.
Expected: failures are real; triage D1 (reconcile trust bypass), D9 (doctor trust zero-target), D8 (SSE retry).

- [ ] **Step 4: Fix confirmed defects and rerun**

- [ ] **Step 5: Secret scan, cleanup, commit**

#### Task 13: H6 — CLI/API heavy parity, recovery, soak

**Files:**
- Create: `test/e2e/worker/h6/run.sh`, `cli-services.sh`, `cli-db.sh`, `api-heavy.sh`, `recovery.sh`, `soak.sh`

**Covers:** CLI-14..19, 23, 24, 29; XC-05/09/21; service/DB CLI and API parity.

**Service slot:** H6 runs under `service-slot/up.sh h6 w6`, holds the service slot lease, uses `DEVSTACK_E2E_NO_PUBLISHED_PORTS=1`, and stops/removes all containers before releasing the lease.

**Fixtures:** MySQL + phpMyAdmin, one service config, one DB with users/backups, 20 static sites for soak, a daemon kill/restart harness.

- [ ] **Step 1: Write the failing CLI service/DB parity test**

For each CLI command, assert human output, `--json` output, exit code, and the same persisted state as the API. Cover service lifecycle/logs/credentials/uninstall, config list/show/diff/apply/reset, DB core/users/backups/import/export/link. Run it, confirm the harness gap, then implement.

- [ ] **Step 2: Write recovery/concurrency/soak**

Kill the daemon mid-operation; assert desired state, one process per site, no duplicate routes, and a clean restart. Run concurrent mutations and a 20-site soak; assert no stale response wins and no process/container leaks.

- [ ] **Step 3: Run against H6, verify failures**

Run: `bash test/e2e/worker/h6/run.sh w6`.
Expected: failures are real; triage D12 (result schema drift) and any CLI/API divergence.

- [ ] **Step 4: Fix confirmed defects under TDD and rerun**

- [ ] **Step 5: Secret scan, cleanup, release service slot, commit**

#### Task 14: L1 — Auth, shell, mock breadth, a11y, responsive, browsers

**Files:**
- Create: `ui/tests/live/auth.live.spec.ts`, `ui/tests/sites-list.spec.ts`, `ui/tests/add-site.spec.ts`, `ui/tests/site-detail.spec.ts`, `ui/tests/services-lifecycle.spec.ts`, `ui/tests/runtimes-lifecycle.spec.ts`, `ui/tests/a11y.spec.ts`, `ui/tests/responsive.spec.ts`
- Modify: `ui/tests/live/fixtures.ts`, `ui/playwright.live.config.ts`, `ui/playwright.config.ts`

**Covers:** AUTH-01..19 (AUTH-06 secondary L2); **CLI-21 API auth/session/CSRF/host guard**; **DB-26 database a11y/responsive**; mock gaps in SITE/ADD/DETAIL/SVC/DB/SET/RT; XC-10/11/12/13.

- [ ] **Step 1: Write the failing live auth and contract cases**

Cover token exchange, cookie flags, CSRF, expiry/locked banner, invalid/missing token, wrong CSRF, navigation/deep links/back/forward/404, SSE reconnect/expiry, `devstack ui` fragment cleanup, status/fingerprint, toasts/skeletons/empty/error/offline-retry. Include CLI-21's live auth/session/CSRF/host-guard assertions against the worker API.

- [ ] **Step 2: Write the mock breadth cases**

Fill the mock gaps for site search/filters/badges/open/quick actions/delete/empty/error/mobile/SSE/runtime-failure, Add Site cancel/back/name/path/kind/source/runtime/build/env/HTTPS/review/duplicate/create-failure, site detail copy/open/restart/delete/edit/env/build/logs/terminal/vhost/not-found, services lifecycle/config, runtimes install/remove/migrate/defaults, settings ports/serving/perf. Add DB-26's keyboard, focus, contrast, and responsive database-screen cases.

- [ ] **Step 3: Write a11y/responsive/cross-browser**

3 viewports (375/768/1440), skip link, focus trap/restore, keyboard-only flows, labels, WCAG AA contrast, drawers/dialogs, no horizontal overflow; run Chromium, Firefox, WebKit.

- [ ] **Step 4: Run against L1 and the mock runner**

Run:
```powershell
$env:PATH='C:\Program Files\nodejs;'+$env:PATH
npm --prefix ui run test:e2e:all
$env:DEVSTACK_E2E_LIVE='1'; $env:DEVSTACK_UI_URL='http://127.0.0.1:24770'
npm --prefix ui run test:e2e:live
```
Expected: failures are real; triage D8 (SSE), D7 (path validation), D14 (mock-only leak guard gap).

- [ ] **Step 5: Fix confirmed defects under TDD and rerun**

- [ ] **Step 6: Secret scan, cleanup, commit**

#### Task 15: L2 — Contracts, light CLI/API, SSE/terminal, network, coverage

**Files:**
- Create: `test/e2e/worker/l2/run.sh`, `cli-contract.sh`, `api-contract.sh`, `sse-terminal.sh`, `network.sh`
- Modify: `test/e2e/coverage/aggregate.py`, `test/e2e/coverage/test_aggregate.py`

**Covers:** CLI-01, 03, 12, 20, 26, 27, 28; AUTH-06; XC-02/03/04/20/23; DOC-17; SET-04/05/07/08/10/13. Secondary contract checks for CLI-05/13/21/22/25. `init` (CLI-02) and real `trust` (CLI-13) are W-primary and must not run here.

- [ ] **Step 1: Write the failing CLI contract test**

For every command: global flags, exit codes, human/`--json` parity, `daemon`, `site`, `shell`/`exec`, temp `vhost`, `ui`/`version`, `runtime`/`tool`/`php`/`node` defaults. Do not invoke `init` or real trust commands. Run it, confirm the gap, then implement.

- [ ] **Step 2: Write the API contract test**

Parse `api/openapi.yaml`; assert every method/status/error code/shape, auth/session/CSRF/host guard, no CORS, sites/source, runtimes/tools/defaults, doctor/trust/vhosts/settings/fs/events, SSE/terminal, error JSON shape.

- [ ] **Step 3: Write network/security cases**

Loopback-only, host allowlist, no CORS, **process-local** VPN/proxy/IPv6 env variants (bad `HTTP_PROXY`, `NO_PROXY`, IPv6 bind attempts in a child process), secret scanner, fuzz harness for path/DB/name inputs. Do not change global iptables, DNS, VPN, proxy, or IPv6 settings. XC-23's independent security review is external/deferred; the fuzz harness is the automated portion.

- [ ] **Step 4: Run against L2, verify failures**

Run: `bash test/e2e/worker/l2/run.sh w8`.
Expected: failures are real; triage D12 and any OpenAPI drift.

- [ ] **Step 5: Fix confirmed defects under TDD and rerun**

- [ ] **Step 6: Secret scan, cleanup, commit**

### Phase 3 — Serialized Windows/Linux lane

The W lane runs **after** H1-H6/L1-L2 are stopped, or before they start. It never overlaps the parallel lanes. Use `Invoke-SerializedLane` for every task.

#### Task 16: W1 — Sandbox installer/lifecycle and links

**Files:**
- Create: `test/windows/e2e/run-installer-sandbox.ps1`, `run-links.ps1`
- Extend: `test/windows/installer_test.ps1`, `test/windows/lifecycle_test.ps1`, `build/windows/devstack-links.test.ps1`

**Covers:** INST-01..03, 09..12, 17..18, 23, 26; INST-13/14 sandbox Linux; XC-15 sandbox.

- [ ] **Step 1: Write the failing sandbox test**

Run the standalone exe, release ZIP, and source checkout in `-DryRun` and `-TestSandbox <dir>`; assert layout/config/state/DACL, elevation refusal, idempotency, links/README, legacy detection, upgrade preflight/backup, release checksums, and no real host mutation. `-TestSandbox` does not prove trust, hosts, service, firewall, or uninstall behavior; those stay in Task 17A.

- [ ] **Step 2: Run it, verify it fails**

Expected: FAIL where the existing tests only inspect payloads.

- [ ] **Step 3: Implement the missing sandbox assertions**

Extend the existing installer/lifecycle tests; do not run a real install here.

- [ ] **Step 4: Run and commit**

#### Task 17: W2 — Real upgrade and rollback only

**Files:**
- Create: `test/windows/e2e/run-real-lifecycle.ps1`
- Extend: `test/windows/vertical_slice.ps1`, `test/windows/lifecycle_test.ps1`

**Covers:** INST-15..22, 27, 30; XC-05/14/15. Fresh install, uninstall, purge, and clean-VM IDs are Task 17A and are **BLOCKED** on this host.

- [ ] **Step 1: Snapshot and verify preconditions**

`HostBaseline` snapshot; assert helper/daemon/agent/Docker/hosts/trust/listeners/links; acquire the exclusive W lease; assert no parallel workers are running; confirm the installer's `/data/devstack` backup is present and verified; confirm the `laravel` database/user still exist.

- [ ] **Step 2: Write the failing real upgrade/rollback test**

Upgrade from the current tree and the previous published release on the real install; inject a health failure; assert rollback restores binaries, config, state, agent health, hosts, trust, and links. Do **not** run a fresh install or uninstall here.

- [ ] **Step 3: Run it, verify failures**

Expected: FAIL on real regressions, not on VM-only IDs.

- [ ] **Step 4: Fix confirmed defects under TDD and rerun**

- [ ] **Step 5: Restore baseline and commit**

#### Task 17A: VM-only fresh install and uninstall (BLOCKED without a disposable VM)

**Files:**
- Create: `test/windows/e2e/run-vm-lifecycle.ps1`, `test/windows/e2e/README-vm.md`
- Evidence: `dist/e2e/<run>/w/vm-gate/`

**Covers:** INST-04..08, 24, 25, 29; XC-15 fresh/uninstall states.

**Gate:** This host has no Windows Sandbox, Hyper-V VM, VirtualBox, VMware, or QEMU. `-TestSandbox` is not proof: it skips daemon shutdown, trust removal, hosts changes, and machine-wide registrations. These IDs are `BLOCKED` with owner `e2e-program` and no expiry waiver. Do not run `uninstall.ps1` against the real install.

- [ ] **Step 1: Write the failing VM preflight test**

Assert a disposable Windows VM is available, has a snapshot/rollback path, and is isolated from the real host's `C:\Users\Aram\.devstack`, hosts, root store, and WSL distros. If absent, record `BLOCKED` and stop.

- [ ] **Step 2: Run it and record the blocker**

Run: `pwsh -NoProfile -File test/windows/e2e/run-vm-lifecycle.ps1 -Preflight`
Expected: `BLOCKED: no disposable Windows VM` on this host.

- [ ] **Step 3: Implement the VM lifecycle test**

When a VM exists, run fresh install, WSL feature enablement/distro import, Docker/agent provisioning, install-time CA/hosts, default uninstall, purge uninstall, and a second-boot autostart check. Snapshot/restore the VM around each run.

- [ ] **Step 4: Run only when the VM exists; otherwise commit the blocker**

`INST-30` remains covered by the disposable `linstall` WSL distro in Task 20.

#### Task 18: W3 — Windows trust, browsers, cert renewal

**Files:**
- Create: `test/windows/e2e/run-trust-browsers.ps1`, `run-cert-renewal.ps1`
- Extend: `internal/wintrust` tests, `internal/certs` test seam

**Covers:** DOC-07/08/14/15; SET-03/06/14; XC-19/24; AUTH-19 real browser.

- [ ] **Step 1: Write the failing trust/browser test**

Create a fresh TLS site; assert the Windows machine root store contains the current CA fingerprint; assert Chrome, Edge, Firefox (enterprise policy), and PowerShell load the site without a warning; remove trust, create another TLS site, assert auto-repair and browser success; assert the CA bundle and pinned fingerprint update.

- [ ] **Step 2: Write the cert renewal test**

Add a test-only clock/renewal seam; force a near-expiry certificate; assert renewal, route reload, browser trust, and pinned-fingerprint update; assert a renewal failure does not break the live site.

- [ ] **Step 3: Run it, verify it fails**

Expected: FAIL on D1 (reconcile bypass) or D11 (no clock seam).

- [ ] **Step 4: Fix confirmed defects and rerun**

- [ ] **Step 5: Restore trust and verify actual values, then commit**

Restore the CA thumbprints, Firefox enterprise policy, helper service/DACL, firewall rules, and hosts bytes from `HostBaseline`; assert actual values, not counts. Include the PMA auto-login browser check in the same browser matrix (Open phpMyAdmin → no login form → root/db:3306).

#### Task 19: W4 — Windows source modes, agent, fixed ports, multi-user

**Files:**
- Create: `test/windows/e2e/run-source-modes.ps1`, `run-agent.ps1`, `run-fixed-ports.ps1`, `run-multiuser.ps1`

**Covers:** ADD-04/09/10/26 (primary); DETAIL-08/09/10; CLI-05/06/13/30/31; DOC-10; SET-06/18; XC-01/16/19. INST-28 (Windows 10 22H2 and a second Windows 11 build/locale) is P2 `extended`: record it as deferred on this single-build host with owner `e2e-program` and expiry `2027-01-31`; do not count it as Live.

- [ ] **Step 1: Write the failing source-mode/agent test**

Windows↔WSL source copy success/failure, original preservation, junction/reparse escapes, WSL UNC/local POSIX validation, agent health/site ops/copy/Docker/ports/confinement, protocol mismatch/reconnect, stale token, wrong distro. ADD-04/09/10 are W-primary; H1's local/WSL cases are secondary.

- [ ] **Step 2: Write fixed-port/multi-user/host-tuning cases**

80/443/7070/8080/8443 ownership, restart guidance, public-port fixed policy, host tuning/PHP-FPM pooling. Multi-user/SID isolation (helper/API/terminal/secure store) runs only in a disposable Windows VM or with a disposable local test account that is removed afterward; it must not alter the real user's ACLs or secure store. Record INST-28 as deferred, not green.

- [ ] **Step 3: Run it, verify it fails**

Expected: FAIL where the current tests only mock.

- [ ] **Step 4: Fix confirmed defects under TDD and rerun**

- [ ] **Step 5: Restore baseline and commit**

#### Task 20: W5 — Linux systemd install lane

**Files:**
- Create: `test/e2e/linux-install/run.sh`, `verify.sh`
- Extend: `test/release/installer_test.sh`

**Covers:** INST-13..16, 30; **CLI-02 `init`**; SET-18; DOC-04/05/06/08 on Linux.

- [ ] **Step 1: Write the failing Linux install test**

In a disposable `devstack-e2e-<run>-linstall` distro, run `install.sh` from source and release, assert systemd helper/units/forwarders/autostart, `devstack init` CA/hosts/first-run, doctor, and uninstall preserving projects.

- [ ] **Step 2: Run it, verify it fails**

Expected: FAIL where the current test only parses unit files.

- [ ] **Step 3: Implement and fix under TDD**

- [ ] **Step 4: Tear down the distro and commit**

#### Task 21: W6 — Final host restore and report

**Files:**
- Create: `test/windows/e2e/run-final-restore.ps1`

- [ ] **Step 1: Stop all workers and containers**

Run `Remove-E2EWorkers` (w1..w8, `golden`, `linstall`), stop all relays and the service slot, assert no `devstack-e2e-*` distro/container/network/volume/home/lock remains, and release the run-wide lease.

- [ ] **Step 2: Restore and verify the real install**

Restore the `HostBaseline` values (hosts bytes, CA thumbprints, Firefox policy, helper service/DACL/firewall, tasks, links, PATH, `.wslconfig`). Do **not** run `wsl --shutdown` because no global `.wslconfig` change was made; if a controlled shutdown is ever required, get explicit user approval. Restart helper/daemon/agent/Docker only if the baseline says they were running; run `devstack doctor`, assert all checks green and actual baseline values match.

- [ ] **Step 3: Write the final report and commit**

Aggregate results, update the coverage ledger, write `dist/e2e/<run>/REPORT.md`, and commit any harness/fix changes. Report blocked IDs, P0/P1/P2 counts, secret-scan result, cleanup result, and baseline diff.

### Phase 4 — Gates and permanent automation

#### Task 22: Coverage ledger update

**Files:**
- Modify: `docs/e2e-coverage.md` (statuses/counts/evidence links)
- Generate: `docs/e2e-coverage-results.md` (per-ID status + evidence)
- Modify: `test/e2e/coverage/manifest.json`

- [ ] **Step 1: Run the aggregator against the completed run**

Expected: exit 0 only if every P0 has a primary-lane Live/System PASS and every P1 is Live/System or has a current owner/expiry waiver. `BLOCKED` rows (DB-28, INST-24/25/29) are reported separately and do not satisfy a P0 gate.

- [ ] **Step 2: Update the ledger from the aggregator output**

Move rows to Live/System only when committed and green; link evidence; keep Evidence rows with a `conversion_task`; record blocked and quarantined rows with owner/expiry; update counts.

- [ ] **Step 3: Commit and push**

#### Task 23: Gate manifests and manual/self-hosted runners

**Files:**
- Create: `.github/workflows/e2e-manual.yml` (manual `workflow_dispatch` only; extend `ci.yml` if the repo forbids new workflows)
- Modify: `test/e2e/gates/{pr,nightly,release}.json`, `Makefile`
- Modify: `test/release/workflow_test.sh`

**Gate split (manual dispatch on a self-hosted Windows/WSL runner):**
- **PR-equivalent local gate:** mock Playwright on Chromium/Firefox/WebKit; live auth/CSRF smoke; Linux builtin E2E; release/installer/workflow contracts; coverage schema. Run via `make e2e-pr` or a manual dispatch, not a push trigger.
- **Nightly-equivalent:** H1-H6 + L1-L2 on the self-hosted runner; W sandbox subset; DB harness cycles 4-8 only; coverage aggregation; secret scan.
- **Release:** every P0 Live/System; real upgrade/rollback; service/DB matrix; trust/browser matrix; Linux systemd install; 100% P0 coverage; no unexpired waiver; DB cycles 9-15 only after explicit approval; VM-required IDs blocked until a VM exists.

The repository's `ci.yml` is intentionally manual-only (`workflow_dispatch`) and `test/release/workflow_test.sh` asserts no push/pull triggers. Do not add automatic triggers. GitHub-hosted Ubuntu cannot run WSL2, four Docker engines, or the real Windows lane; the live gates must use the self-hosted Windows/WSL runner or a local `make` target.

- [ ] **Step 1: Write the failing workflow contract test**

Extend `test/release/workflow_test.sh` to assert the manual e2e workflow exists, is `workflow_dispatch` only, is least-privilege, declares a self-hosted Windows runner, does not run destructive Windows tests on a PR trigger, and fails on a P0 coverage gap.

- [ ] **Step 2: Implement the workflow and gate manifests**

- [ ] **Step 3: Run the contract test and commit**

#### Task 24: Release-blocking full run

- [ ] **Step 1: Run the full program from a clean `master`**

Use the `5-parallel+service-slot` layout. Record the layout, memory evidence, and residual risks. Do not change `.wslconfig` or run `wsl --shutdown`.

- [ ] **Step 2: Require all P0 green**

No P0 waiver. Fix or block; do not mark green on evidence-only, secondary-lane, mock-only, or `-TestSandbox` rows.

- [ ] **Step 3: Publish the report and push**

Include per-lane totals, P0/P1/P2 counts, blocked IDs, secret-scan result, cleanup result, and baseline diff.

### Phase 5 — Cleanup and baseline

#### Task 25: Teardown and baseline verification

- [ ] **Step 1: Remove every worker and temp artifact**

- [ ] **Step 2: Verify the real install**

`devstack doctor` all green; `site list` baseline; `service list` baseline; trust status; hosts; listeners; C:\devstack links; WSL distro list; no `devstack-e2e-*` leftovers.

- [ ] **Step 3: Commit the cleanup report and push**

---

## 6. Defect ledger

These were found during planning research. Each is a **candidate** until reproduced by the owning lane. A confirmed defect gets a failing test, a minimal fix, the full Go/UI gate, a commit, and a push before the lane can claim green.

| ID | Finding | Owning lane | Required proof |
|---|---|---|---|
| D1 | Daemon reconcile starts TLS sites without the API trust preflight (`internal/daemon/daemon.go` startDesiredSites) | H5 / W3 | Remove trust, restart daemon, assert TLS site does not serve untrusted or trust is repaired |
| D2 | Installer does not install CA/hosts; `INST-08` claims it does | W1 / W2 | Fresh VM install → trust/hosts present, or docs/coverage corrected; `-TestSandbox` is not proof |
| D3 | Uninstall leaves `C:\devstack` links/README | W2 / Task 17A | Default uninstall in a disposable VM removes links/README, preserves projects |
| D4 | Site create is not DB-atomic; DB/user kept on later failure | H1 / H4 | Inject failure after DB create; assert no orphan DB/user or documented keep + warning |
| D5 | Backup-before-drop not enforced by default | H4 | Drop without `--backup-first` either backs up or fails closed |
| D6 | Backup delete has no typed confirmation | H4 | Exact typed confirmation required server-side; cancel leaves backup |
| D7 | Add Site rejects valid dot-prefixed/trailing-slash paths | H1 / L1 | `.next`, `dist/.vite`, `public/` accepted where valid |
| D8 | SSE has no `onerror`/retry/expiry handling | L1 | Cookie loss/daemon restart produces a locked state, not an infinite retry |
| D9 | Doctor trust zero-target is WARN/OK; API is fail-closed | H5 | Same zero-target case yields one documented behavior |
| D10 | `SET-02` is read-only; inventory implies management | H3 | Either implement the controls or relabel the capability |
| D11 | Cert renewal has no E2E clock seam | W3 | Test-only clock seam; renewal/reload/pin verified |
| D12 | Results schema is not enforced; ID space drifts | L2 / Task 4 | Aggregator rejects invalid/duplicate/stale rows |
| D13 | Runtime catalog has no test seam for download failure/checksum/disk-full | H2 | Test-only seam or explicit simulated catalog; no production behavior change |
| D14 | `e2e.spec.ts` lacks the DB spec's global leak/error guard | L1 | Shared leak/console/request guard on every mock spec |
| D15 | Service data deletion trusts a client boolean, not a typed confirmation | H3 | Server-side exact confirmation or documented API-only policy |
| D16 | Service activation used the generic 30s client timeout; first PostgreSQL boot exceeded it | H3/H4 | Long-running client path + caller cancellation test; fixed |
| D17 | Disposable helper homes could not set `allow_home`/`skip_trust_update`, so trust preflight wrote to the real user home or failed unprivileged | H1/H5 | Helper config/flag support, fail-closed temp anchor test; fixed |
| D18 | Linux E2E fixtures lived outside the managed projects root and site add was rejected | H1 | Move fixtures under the managed root; Linux E2E and cycle5 fixture setup use managed paths. |
| D19 | A second Docker engine in the shared WSL namespace cannot publish fixed ports while the real install engine is active. | H3/H4/H6 | Implemented `DEVSTACK_E2E_NO_PUBLISHED_PORTS`; DB lifecycle/core/adversarial and site/backup suites now pass with no host port publishing. |
| D20 | phpMyAdmin required cookie login and did not use the managed root account | H4 | `auth_type=config`, root secret mount, migration/refresh on restart; live browser test green. |
| D21 | Cycle5's UI fixture used `${home}/projects` while the product manages `${devstack_home}/projects` | H4/L1 | Use the managed root and disposable helper home; live database UI fixture now starts. |

## 7. Gates and definition of done

A capability is done only when all of the following are true, mirroring `docs/e2e-coverage.md` section 6:

- [ ] P0 has an automated live or system E2E test in its **primary** lane; no P0 waiver.
- [ ] P1 is automated or has a reviewed, documented waiver with owner/expiry; `BLOCKED` is not a waiver.
- [ ] P2 is in the nightly/extended matrix or explicitly deferred with owner/expiry.
- [ ] The test proves the user-visible result **and** the persisted backend state.
- [ ] The test proves the main failure path, cleanup, and rollback.
- [ ] The test is isolated and repeatable without manual repair.
- [ ] The test leaves no site, service, runtime, database, backup, link, hosts entry, trust record, or process behind.
- [ ] The test never prints or persists a secret; evidence, traces, screenshots, and HTML reports pass the scanner.
- [ ] The suite runs on every release-blocking platform that is available; VM-only IDs are explicitly `BLOCKED`, not green.
- [ ] The suite is part of a required manual/self-hosted gate, not an optional ad-hoc run.
- [ ] Flaky tests are fixed or quarantined with an owner and expiry date.

## 8. Execution handoff

Recommended execution: **subagent-driven**. One fresh implementer per task, a fresh reviewer per lane gate, and a whole-program review before the release gate. Every dispatch uses `opencode-go/deepseek-v4.1-flash#max`.

Suggested order:

1. Task 0 → Task 1 → Task 2 (5-worker smoke) → Task 2A (service slot) → Task 3 → Task 4 → Task 5 → Task 6 → Task 7.
2. Start H1, H2, H5, L1, L2 in parallel; start the single service slot for H3 → H4 → H6 sequentially. Stop and record any memory/OOM issue.
3. W1-W6 serialized, with the run-wide lease and `HostBaseline` before and after every task. Task 17A records the VM blocker; do not run it against the real install.
4. Task 22-24 aggregation, ledger, manual/self-hosted gates, full run.
5. Task 25 teardown and baseline verification.

Do not start a lane until its worker stack is green and its guard test passes. Do not mark a row Live until the aggregator sees a committed primary-lane PASS with a clean secret scan and a clean cleanup manifest.
