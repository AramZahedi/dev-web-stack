# Database management E2E harness

Reusable fixtures, seed/verify scripts, isolation guards, evidence capture,
result tracking, secret scanning, cleanup, and rollback for the
database-management E2E plan:

- Plan: `docs/superpowers/status/2026-10-31-database-management-e2e-plan.md`
- Runbook: `docs/superpowers/status/2026-10-31-database-management-e2e-runbook.md`

This harness is for **disposable environments only**. It refuses the real
`devstack` WSL distro, `/data/devstack`, and the real Docker engine.

## Quick start (Ubuntu WSL, root or docker-group user)

From Windows PowerShell:

```powershell
wsl -d Ubuntu -u root -- bash -lc 'cd /mnt/c/Users/Aram/Projects/Go/dev-web-stack && bash test/e2e/database-management/smoke.sh'
```

From an Ubuntu WSL shell:

```bash
cd /mnt/c/Users/Aram/Projects/Go/dev-web-stack
bash test/e2e/database-management/smoke.sh
```

`smoke.sh` is the R12 harness smoke, not an E2E scenario. It starts pinned
MySQL, MariaDB, PostgreSQL, and Redis containers on a unique network, runs
`seed.sh` and `verify.sh`, runs the secret scanner, snapshots and rolls back a
disposable home, runs `cleanup.sh`, and records `HARNESS-SMOKE` in the result
tracker.

Individual steps:

```bash
# Start containers only
source test/e2e/database-management/lib.sh
e2e_guard; e2e_require_docker; e2e_init_dirs; e2e_start_all

# Seed, verify, scan, clean up
test/e2e/database-management/seed.sh --engine all
test/e2e/database-management/verify.sh --engine all
test/e2e/database-management/secret-scan.sh
test/e2e/database-management/cleanup.sh
```

## Cycle 4 Linux core scenarios

`test/e2e/database-management/linux/` runs the Linux core batch against the
product helper and daemon, not the harness containers:

```bash
wsl -d Ubuntu -u root -- bash -lc \
  'cd /mnt/c/Users/Aram/Projects/Go/dev-web-stack && \
   E2E_STAMP="$(date -u +%Y%m%dT%H%M%SZ)" \
   bash test/e2e/database-management/linux/cycle4-run-all.sh'
```

| Script | Scenario | Notes |
|---|---|---|
| `cycle4-e2e01.sh` | E2E-01 lifecycle equivalent | PostgreSQL/Redis use product activate/restart/deactivate; MySQL/phpMyAdmin product activation is **BLOCKED** because this host already reserves 3306/8082 for the real install. Equivalent no-port/manual containers and phpMyAdmin login on a unique high port are verified. |
| `cycle4-e2e02.sh` | E2E-02 databases/users | MySQL no-port container and PostgreSQL product service; create/list/reveal/rotate/grant/revoke/drop/rename/protected/site-link/API parity. |
| `cycle4-e2e16.sh` | E2E-16 adversarial | Names, hosts, grants, passwords, concurrency, stale metadata, audit rows, SSE, and secret scans. |

`cycle4-env.sh` owns the disposable home, helper socket/config, high daemon
ports, Compose project helpers, safe SQL-client helpers, and cleanup. It
refuses any distro other than `Ubuntu`, any home outside
`/tmp/devstack-e2e-*`, and the real install paths. `cycle4-check.sh` records
PASS/FAIL evidence per scenario. All generated passwords stay in
`$E2E_HOME/secrets` or `$E2E_HOME/private`; evidence contains only hashes and
redacted summaries.

## Cycle 5 Linux site scenarios

`cycle5-run-all.sh` runs E2E-03, E2E-04, E2E-17, E2E-06, and E2E-05 in one
disposable home. The helper runs as root with `allow_uid: 1000`; the daemon,
CLI, site processes, and terminal PTY run as `aram` so the terminal path is
real. MySQL/MariaDB containers run without published host ports; PostgreSQL
uses fixed loopback 5432.

```bash
wsl -d Ubuntu -u root -- bash -lc \
  'cd /mnt/c/Users/Aram/Projects/Go/dev-web-stack && \
   E2E_STAMP="$(date -u +%Y%m%dT%H%M%SZ)" \
   bash test/e2e/database-management/linux/cycle5-run-all.sh'
```

| Script | Scenario | Notes |
|---|---|---|
| `cycle5-e2e03.sh` | E2E-03 Add Site new DB/user | Dynamic env, secret-free state/php-fpm, reveal audit, terminal and PHP exec. |
| `cycle5-e2e04.sh` | E2E-04 existing/metadata-only users | No auto-grant, external user, Set password, unsupported stdin, explicit grant + restart. |
| `cycle5-e2e05.sh` | E2E-05 site deletion/uninstall | Marker preservation, keep-data vs delete-data, simulated deletion failure keeps links. |
| `cycle5-e2e06.sh` | E2E-06 wrong-engine start | No-port MariaDB active; site start/API blocked; recovery on MySQL. Product 3306 activation is BLOCKED. |
| `cycle5-e2e17.sh` | E2E-17 site adversarial | Multi-link env, reserved keys, Add Site validation, concurrency, stale links, restore/import gates. |
| `cycle5-ui-setup.sh` | C5-UI live fixtures | Starts a daemon for `ui/playwright.live-site.config.ts`; stays alive until `$E2E_HOME/stop` exists. |

The terminal probe is
`test/e2e/database-management/linux/terminal-probe/`. It prints only
`E2E_TERM` key/presence lines, never a password or DSN. The live dashboard
config disables traces, screenshots, and video so a revealed password cannot be
persisted.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `E2E_REPO_ROOT` | repo root derived from `lib.sh` | alternate checkout |
| `E2E_STAMP` | UTC timestamp + PID | run identity; one per run |
| `E2E_RUN_KEY` | sanitized `E2E_STAMP` | Docker name suffix |
| `E2E_HOME` | `/tmp/devstack-e2e-<run-key>` | disposable home; must be under `/tmp/devstack-e2e-*` |
| `E2E_NETWORK` | `devstack-e2e-<run-key>-net` | unique Docker network |
| `E2E_DS` | `dist/e2e/database-management/bin/devstack` | CLI under test |
| `E2E_SVC` | `mysql` | service selector for ad-hoc checks |
| `E2E_ID` | `e2e_app` | resource marker |
| `E2E_EVIDENCE_ROOT` | `dist/e2e/database-management/evidence` | evidence root |
| `E2E_EVIDENCE_DIR` | `<root>/<run-key>` | evidence for this run |
| `E2E_RESULTS_FILE` | `dist/e2e/database-management/results.json` | result tracker |
| `E2E_PUBLISH_PORTS` | `0` | `1` publishes loopback high ports (avoid) |
| `E2E_DEVSTACK_DAEMON` | `0` | `1` uses the real CLI for site-link seeding |
| `E2E_ALLOW_REAL_DISTRO` | `0` | only for an approved disposable clone |
| `E2E_TIMEOUT` | `1800` | `run-scenario.sh` timeout seconds |
| `E2E_RESIDUAL_RISK` | `none recorded` | recorded with the result |
| `E2E_SCENARIO_ENV` | `LIN-UBU` | scenario environment label |

Never put a password in an environment variable that is captured by evidence.
The harness stores generated secrets only under `$E2E_HOME/secrets` (mode 0600)
and never prints them.

## Isolation model

### Linux (LIN-UBE / LIN-DEB)

- Use the **Ubuntu 26.04 WSL distro** (`Ubuntu`), not the real `devstack`
  distro. The real distro's Docker engine, agent, `/data/devstack`, MySQL, and
  phpMyAdmin must never be reused.
- The harness creates its own Docker network
  `devstack-e2e-<run-key>-net` and containers
  `devstack-e2e-<run-key>-{mysql,mariadb,postgres,redis}` with the label
  `devstack.e2e.run=<run-key>`.
- **No host ports are published by default.** The real install owns 3306 and
  8082 on Windows localhost, and WSL published ports reach Windows. If a future
  test truly needs a host port, `E2E_PUBLISH_PORTS=1` publishes a unique
  loopback high port (20000-39999) and records it in the manifest. Prefer
  container-network DNS and `docker exec`.
- The disposable home is `/tmp/devstack-e2e-<run-key>`. It contains `secrets/`,
  `logs/`, `backups/`, `sites/`, `fixtures/`, `redis/`, and `manifest.tsv`.
- `lib.sh` unsets `SSL_CERT_FILE`, `CURL_CA_BUNDLE`,
  `NODE_EXTRA_CA_CERTS`, and `REQUESTS_CA_BUNDLE` defensively because cycle 0
  quarantined a stale CA profile script.
- Run Docker as root (`wsl -d Ubuntu -u root`) until the user is in the
  `docker` group. The harness never uses `sudo` internally.

### Windows (R07)

- **Option A (preferred):** stop the real daemon/helper, snapshot
  `state.db`, `config.yaml`, `system/helper.yaml`, hosts, and binaries, point
  the singleton helper at `C:\Users\Aram\.devstack-e2e`, run tests, then
  restore and verify. See `windows/README.md` and `windows/handover-plan.ps1`.
- **Option B (fallback):** `WIN-REAL` only with a verified baseline
  backup/rollback and explicit approval. Never destructive by default.
- **Option C:** `-TestSandbox` is only for installer mechanics, never live
  services.
- **Known gap:** option A does not isolate the WSL agent's fixed
  `/data/devstack` data root. Windows destructive database tests stay
  **BLOCKED** until a disposable WSL distro clone or a verified WSL snapshot
  is added. Use LIN-UBE for destructive database scenarios.
- `windows/read-only-check.ps1` is safe to run now. The handover executor and
  rollback are gated and are **not** executed by cycle 1.

## Fixtures

Committed sources live under `fixtures/`:

- `sql/mysql-seed.sql`, `sql/mariadb-seed.sql` (shared MySQL-family seed)
- `sql/postgres-seed.sql`
- `sql/mysql-users.sql.tmpl`, `sql/postgres-users.sql.tmpl`
- `sql/seed.sql`, `sql/seed-postgres.sql` (import fixtures)
- `sql/seed-unicode.sql`, `sql/seed-fk.sql`, `sql/seed-binary.sql`,
  `sql/seed-routines.sql`
- `sql/invalid.sql`, `sql/seed-partial.sql`
- `redis/users.acl.tmpl`, `redis/foreign-aclfile.conf`
- `dumps/seed.sql.gz`, `dumps/seed-postgres.sql.gz` (valid gzip)
- `dumps/corrupt.sql.gz`, `dumps/truncated.sql.gz` (invalid gzip)
- `dumps/non-sql.txt`, `dumps/empty.sql`

`fixtures/generate.sh` rebuilds the dumps deterministically (`gzip -n`),
generates the corrupt/truncated variants, and can generate a large import file.
The 1 GiB file is **opt-in**:

```bash
fixtures/generate.sh --large --large-size-mb 1024 --out /tmp/e2e-large
```

It also generates a populated Redis ACL fixture from ephemeral secrets:

```bash
fixtures/generate.sh --redis-acl "$E2E_HOME/redis/fixture" \
  --secret-dir "$E2E_HOME/secrets"
```

## Seed and verify

`seed.sh` is idempotent:

- creates `e2e_app`, `e2e_empty`, `e2e_unicode`, `e2e_reserved`, `e2e_case`,
  `e2e_long_63`, `e2e_fk`, `e2e_binary`, `e2e_routines`;
- creates marker/unicode/reserved/FK/binary/routine fixtures;
- creates MySQL/MariaDB users with `%`, `localhost`, and `10.%` host scopes and
  `read_only`/`read_write`/`admin` grants;
- creates PostgreSQL roles and grants;
- creates Redis `e2e_cache_ro`, `e2e_cache_full`, and `e2e_cache_custom` ACL
  users, saves `users.acl`, and verifies the file contains hashes only;
- creates a logical backup, restores it into a scratch database, imports the
  gzip fixture into another scratch database, then drops the scratch databases;
- writes a site-link fixture (two links, one primary) and uses the real CLI
  only when `E2E_DEVSTACK_DAEMON=1`.

`verify.sh` re-checks all of the above, tests logins, read-only denial, wrong
passwords, backup checksums, corrupt/truncated gzip rejection, Redis ACL
restrictions and restart persistence, and the site-link fixture. It is safe to
re-run; it creates and removes only scratch objects.

## Evidence layout

```
dist/e2e/database-management/
  evidence/<run-key>/
    <scenario-id>/
      command.log
      logs/
  results.json
  results.md
  readiness/cycle-0/
  readiness/cycle-1/
  windows-handover/<stamp>/
```

`run-scenario.sh` writes `command.log` and copies `$E2E_HOME/logs`. The secret
scanner runs before the result is recorded. Raw secret stores are never copied
into evidence.

## Result schema

`results.schema.json` defines the tracked JSON. Each row records:

- `id` (`E2E-NN` or `HARNESS-*`)
- `priority` (`P0`-`P3`)
- `environment` (`WIN`, `WIN-REAL`, `LIN-UBE`, `LIN-DEB`, `LIN-FED`,
  `UI-MOCK`, `UI-LIVE`, `CLI-PS5`, `CLI-PS7`, `CLI-BASH`)
- `status` (`PASS`, `FAIL`, `SKIP`, `BLOCKED`)
- `evidence` path
- `commit` SHA
- `recorded_at`
- `residual_risk`
- `notes`

Use the wrapper rather than editing JSON by hand:

```bash
run-scenario.sh E2E-00 --environment LIN-UBE --timeout 900 -- ./my-check.sh
results.sh show
results.sh render
```

## Secret scanning

```bash
secret-scan.sh --self-test
secret-scan.sh [PATH...]
```

It scans evidence, logs, and results for private keys, DSNs with userinfo,
`DB_PASSWORD`/`DATABASE_URL`/`MYSQL_PWD`/`PGPASSWORD`/`REDISCLI_AUTH`/
`REDIS_PASSWORD` assignments, GitHub/AWS tokens, JSON password fields, and the
exact runtime secrets from `$E2E_HOME/secrets`. It prints only
`REDACTED <path>:<line> pattern=<name>` and exits 1 on any hit.

## Cleanup and rollback

```bash
cleanup.sh [--dry-run] [--keep-home]
rollback.sh snapshot --home "$E2E_HOME" --snapshot /tmp/run.tgz
rollback.sh restore  --home "$E2E_HOME" --snapshot /tmp/run.tgz
rollback.sh verify   --home "$E2E_HOME"
```

`cleanup.sh` removes only the manifest's containers, volumes, network, and the
`/tmp/devstack-e2e-*` home. It refuses names outside the
`devstack-e2e-<run-key>-*` prefix. It never removes real `devstack-*`
containers or `C:\Users\Aram\.devstack`.

`rollback.sh` snapshots a disposable home, verifies the snapshot checksum, and
restores it with a pre-rollback copy kept until verification passes. The
Windows handover rollback is `windows/rollback.ps1`; see `windows/README.md`.

## Pinned images

| Image | Digest |
|---|---|
| `mysql:8.4.13` | `sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69` |
| `mariadb:11.8.9` | `sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa` |
| `postgres:18.6` | `sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6` |
| `redis:8.8.3` | `sha256:418e397fd48d66e450af13baab123397f2d2580b07ef310c3a5dbe96c3d731f1` |
| `phpmyadmin:5.2.3` | `sha256:62a50b1419fe55b9b4c3cd6f5aed03a18f8f5b4a236f24735d76400bea75a5ff` |

Pull them only in the disposable Ubuntu engine. `verify.sh` checks the local
digests.

## Non-goals and limits

- This harness does not run the E2E scenario catalog; cycles 4+ do.
- It does not manage the real daemon/helper or the real WSL agent.
- It does not create the 1 GiB file unless `--large` is passed.
- It does not prove product behavior by itself; it provides the fixtures,
  isolation, and evidence plumbing for the scenario batches.
