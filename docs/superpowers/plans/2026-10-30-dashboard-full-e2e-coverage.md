# Full Dashboard and CLI E2E Coverage Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. This is an execution checklist, not an implementation plan. Steps use checkbox (`- [ ]`) syntax for tracking. Do not change product code unless an assertion fails; if it does, follow TDD, review, commit, and re-run the failing task.

**Goal:** Close every dashboard and CLI action that was not exercised by the 2026-10-30 Node-toolchain E2E run, prove each one works live, restore the host to its baseline, and publish the evidence.

**Architecture:** Sequential live E2E lanes against the real Windows 11 + WSL2 install. Each task owns a fixture, exact actions, expected assertions, evidence files, and cleanup. Destructive tasks (services, ports, trust, performance, uninstall) take backups and restore the baseline before the next task. A final task re-runs the complete gate matrix and writes the acceptance report.

**Tech Stack:** Windows 11, PowerShell 7, WSL2 Debian 12 (`devstack`), Go daemon/helper/agent, Docker Engine in WSL, Svelte dashboard, Playwright MCP, `devstack` CLI, `curl.exe`.

**Evidence and prior reports:**
- [Node toolchain E2E report](../audits/2026-10-30-node-toolchain-e2e.md)
- [2026-10-29 full E2E audit](../audits/2026-10-29-e2e-report.md)
- [Services guide](../../services.md)
- [Windows paths](../../windows-paths.md)
- [Troubleshooting](../../troubleshooting.md)
- [Known issues](../../known-issues.md)

## Why this plan exists

The 2026-10-30 Node-toolchain run fully verified the Node feature and the Windows/WSL vertical slice. It did **not** re-test every dashboard action. The gaps recorded in the final report are:

1. Add Site for PHP and WSL source mode; full source-mode copy/switch.
2. Dashboard PHP site configuration: directives, raw ini, extensions, generated preview.
3. Runtimes PHP install/remove/migrate.
4. Settings listener ports/save/restart.
5. Settings trust repair/remove.
6. Settings PHP CLI default/global ini/Composer/native tools.
7. Settings serving modes.
8. Performance preview/apply/reset/benchmark.
9. Services other than Redis: nginx, Apache, MySQL, MariaDB, PostgreSQL, phpMyAdmin; config apply/reset; credentials reveal/rotate; phpMyAdmin activation.
10. Doctor Repair.
11. CLI groups: `trust`, `vhost`, `service`, `php default`, `site php/ini/ext`, `runtime migrate`, `ui`, `uninstall`, completion, `doctor --repair`.
12. SSE/event streams and terminal reconnect/resize edges.
13. Full final regression and live-state restoration.

## Coverage map

| Remaining area | Task |
|---|---|
| Add Site PHP, WSL source mode, validation | 1 |
| PHP site configuration, ini, extensions, preview | 2 |
| Source-mode copy/switch and stopped guard | 3 |
| PHP runtime install/remove/migrate | 4 |
| Service lifecycle, config, credentials, phpMyAdmin | 5 |
| Listener ports and daemon restart | 6 |
| Trust repair/remove/restore | 7 |
| PHP CLI default, global ini, Composer, native tools | 8 |
| Serving mode builtin/nginx/Apache | 9 |
| Performance preview/apply/reset/benchmark | 10 |
| Doctor repair | 11 |
| Remaining CLI command groups | 12 |
| SSE/event stream and terminal edges | 13 |
| Final regression, cleanup, report | 14 |

## Global constraints

- **Host:** Windows 11, WSL2 distro `devstack`, Docker Engine running inside WSL.
- **Live home:** `C:\Users\Aram\.devstack`; binary: `C:\devstack\bin\devstack.exe`.
- Set `$env:DEVSTACK_HOME = 'C:\Users\Aram\.devstack'` for every shell; pass `--home $h` explicitly when in doubt.
- Start from `master` at or after the commit containing this plan. Run `git status --short` and require a clean tree before live testing.
- Use freshly built binaries from the current tree. Never test a stale `C:\devstack\bin\devstack.exe`.
- Destructive operations require the exact typed confirmation shown in the dashboard and a backup of `state.db`, `config.yaml`, hosts block, helper config, and trust state before starting.
- Every task ends with:
  - `devstack site list` empty unless the next task explicitly needs the fixture;
  - `devstack service list` showing `not_installed` for every service touched unless the next task needs it;
  - `devstack doctor` with **All checks passed**;
  - no dashboard console errors on a fresh tab.
- Never edit project source. Fixtures live under `C:\Users\Aram\.devstack\projects\e2e-full-*` or WSL `/data/devstack/projects/e2e-full-*`.
- Evidence goes under `dist/e2e/full-dashboard/<task>/` (ignored): page snapshots, screenshots, command output, HTTP bodies, JSON responses, and timing.
- Playwright: always navigate with `devstack ui --json`'s `open_url` (or `fallback_url#token=...`), open a fresh tab per task, and assert the console has zero errors before marking the task complete.
- API/CLI assertions must use exact status codes and JSON fields. Dashboard assertions must name the visible label, not a guessed selector.
- If a bug is found: capture evidence, stop that task, write a focused failing test, fix, review, commit, redeploy, rerun the task from the beginning, and push only after the full gate.
- Do not weaken security boundaries to make a test pass. The dashboard is intentionally unreachable from the WSL network namespace (403).

## Test environment setup

Run once at the start:

```powershell
$h = 'C:\Users\Aram\.devstack'
$ds = 'C:\devstack\bin\devstack.exe'
$env:DEVSTACK_HOME = $h
$outRoot = 'dist\e2e\full-dashboard'
New-Item -ItemType Directory -Force $outRoot | Out-Null
```

Build and deploy the current tree:

```powershell
$env:CGO_ENABLED = '0'
go build -trimpath -o dist\e2e\devstack.exe ./cmd/devstack
go build -trimpath -o dist\e2e\devstack-helper.exe ./cmd/devstack-helper
$env:GOOS='linux'; $env:GOARCH='amd64'
go build -trimpath -o dist\e2e\devstack-agent ./cmd/devstack-agent
Remove-Item Env:GOOS,Env:GOARCH
```

If upgrading helper/daemon, use the installer-equivalent flow: stop daemon, stop `DevstackHelper`, back up binaries, copy the three binaries, start helper, run `devstack init --yes`, copy the agent into WSL with the service stopped, start it, then `devstack doctor`. Do not copy over a running executable.

Get the dashboard URL:

```powershell
$ui = & $ds ui --json --home $h | ConvertFrom-Json
$ui | ConvertTo-Json -Compress
$openUrl = $ui.open_url
```

Open `$openUrl` in a fresh Playwright tab. If TLS trust is missing, use `$ui.fallback_url + '#token=' + (($ui.open_url -split 'token=')[1])`.

---

## Task 0: Preflight, backup, and baseline

**Artifacts:** `dist/e2e/full-dashboard/task-0/`

- [x] **Step 1: Capture source and binary baseline**

```powershell
git status --short --branch
git log -1 --oneline
gofmt -l .
go build ./...
go vet ./...
go test ./...
& $ds version --home $h
& $ds doctor --json --home $h | Out-File "$outRoot\task-0\doctor-before.json"
```

Expected: clean tree, green build/vet/test, `doctor` has no failures. Record the commit SHA in the evidence file.

- [x] **Step 2: Capture live state**

```powershell
& $ds site list --json --home $h | Out-File "$outRoot\task-0\sites-before.json"
& $ds runtime list --json --home $h | Out-File "$outRoot\task-0\runtimes-before.json"
& $ds service list --json --home $h | Out-File "$outRoot\task-0\services-before.json"
& $ds node default --status --json --home $h | Out-File "$outRoot\task-0\node-default-before.json"
& $ds php default --status --json --home $h | Out-File "$outRoot\task-0\php-default-before.json"
& $ds trust status --json --home $h | Out-File "$outRoot\task-0\trust-before.json"
& $ds vhost status --json --home $h | Out-File "$outRoot\task-0\vhost-before.json"
```

Expected baseline: no sites, no installed services, Node default `24.22.1`, PHP default `php@8.4.27-r1`, serving mode `builtin`, trust healthy.

- [x] **Step 3: Back up the live state**

```powershell
$backup = "$outRoot\task-0\backup"
New-Item -ItemType Directory -Force $backup | Out-Null
Copy-Item "$h\state.db" "$backup\state.db" -Force
Copy-Item "$h\state.db-wal" "$backup\state.db-wal" -Force -ErrorAction SilentlyContinue
Copy-Item "$h\state.db-shm" "$backup\state.db-shm" -Force -ErrorAction SilentlyContinue
Copy-Item "$h\config.yaml" "$backup\config.yaml" -Force
Copy-Item 'C:\devstack\system\helper.yaml' "$backup\helper.yaml" -Force
Copy-Item 'C:\Windows\System32\drivers\etc\hosts' "$backup\hosts" -Force
Get-ChildItem 'C:\devstack\bin' -Filter 'devstack*' | Copy-Item -Destination $backup -Force
```

Do not restore the DB unless a task corrupts state; prefer reverting through the CLI/dashboard. Record the backup path.

- [x] **Step 4: Create fixture directories**

```powershell
$p = "$h\projects"
New-Item -ItemType Directory -Force "$p\e2e-full-php","$p\e2e-full-static","$p\e2e-full-node","$p\e2e-full-wsl" | Out-Null
Set-Content "$p\e2e-full-php\index.php" '<?php echo "php ".PHP_VERSION." sapi ".php_sapi_name();'
Set-Content "$p\e2e-full-static\index.html" '<h1>e2e full static</h1>'
Set-Content "$p\e2e-full-node\server.js" @'
const http=require('http');const {execFileSync}=require('child_process');
http.createServer((q,s)=>{let n='?';try{n=execFileSync('npm',['-v'],{encoding:'utf8'}).trim()}catch(e){};s.end(`node ${process.version} npm ${n}`)}).listen(Number(process.env.PORT||3000),'127.0.0.1');
'@
```

Create a WSL-mode fixture inside WSL:

```powershell
wsl.exe -d devstack -u root -- sh -c "mkdir -p /data/devstack/projects/e2e-full-wsl && printf '<h1>e2e full wsl</h1>' > /data/devstack/projects/e2e-full-wsl/index.html"
```

- [x] **Step 5: Open a fresh dashboard tab and assert no console errors**

Use the token URL. Assert the Sites page shows the current site count. Capture `task-0/sites.png` and the accessibility snapshot.

- [x] **Step 6: Record baseline**

Write `task-0/README.md` with commit SHA, dashboard URL, baseline JSON, backup path, and any pre-existing anomalies. Commit the plan only; do not commit evidence (ignored).

---

## Task 1: Add Site — PHP and WSL source modes

**Depends on:** Task 0. **Artifacts:** `task-1/`

- [x] **Step 1: Add a PHP site in Windows source mode**

Dashboard: Sites → Add site. Fill:
- Site name: `e2e-full-php`
- Project folder: `C:\Users\Aram\.devstack\projects\e2e-full-php`
- Site kind: PHP
- Project source: Windows drive
- Continue → Runtime: PHP `8.4` (or the installed PHP build), document root `.`, Node toolchain `Inherit global default (v24.22.1)`, front controller off for the first pass.
- Continue → Options: HTTPS on, force HTTPS off, www off, create.

Expected: Site Detail for `e2e-full-php`; status Running; PHP version/build shown; Node toolchain source global; `https://e2e-full-php.test` returns `php 8.4.27 sapi fpm-fcgi`.

Assertions:

```powershell
curl.exe -k -sS https://e2e-full-php.test
& $ds site show e2e-full-php --json --home $h
```

Expected body starts with `php 8.4.27`; JSON `kind=php`, `source_mode=windows`, `node_toolchain.source=global`.

- [x] **Step 2: Add a PHP site in WSL source mode**

Dashboard: Add site:
- Name `e2e-full-php-wsl`
- Project folder `\\wsl.localhost\devstack\data\devstack\projects\e2e-full-wsl`
- Kind PHP
- Source WSL
- PHP 8.4, document root `.`, front controller on with `index.php`; create.

Expected: Running; `source_mode=wsl`; `https://e2e-full-php-wsl.test` returns `php 8.4.27 sapi fpm-fcgi`. If the fixture has no PHP file, copy `index.php` into the WSL fixture first.

- [x] **Step 3: Add static and Node sites in WSL mode**

Static: `e2e-full-static-wsl`, path `\\wsl.localhost\devstack\data\devstack\projects\e2e-full-wsl`, kind static, source WSL, Node inherit. Expected `e2e full wsl`.

Node: `e2e-full-node-wsl`, same WSL path, kind Node, source WSL, explicit Node `24.22.1` or `22.23.4`, command `node server.js`. Before creating it, write the same `server.js` into the WSL fixture:

```powershell
wsl.exe -d devstack -u root -- sh -c "cat >/data/devstack/projects/e2e-full-wsl/server.js <<'EOF'
const http=require('http');const {execFileSync}=require('child_process');
http.createServer((q,s)=>{let n='?';try{n=execFileSync('npm',['-v'],{encoding:'utf8'}).trim()}catch(e){};s.end(`node ${process.version} npm ${n}`)}).listen(Number(process.env.PORT||3000),'127.0.0.1');
EOF"
```

Expected HTTP `node vX npm Y`; terminal `node -v` matches.

- [x] **Step 4: Exercise validation and navigation**

Attempt each invalid case and assert inline error, no site created:
- Empty name/folder → `Enter a site name.` / `Choose the project folder.`
- Windows path with WSL source mode → WSL path error.
- WSL path with Windows source mode → Windows drive error.
- PHP kind with no installed PHP build → install-first error.
- Node kind with no Node version/command → `Choose an installed Node version.` / `Enter the command that starts the dev server.`
- Back buttons preserve entered values; Cancel returns to Sites without creating.

- [x] **Step 5: Assert no console errors and evidence**

Fresh reload each created Site Detail; capture snapshot/screenshot and console. Expected 0 errors/warnings.

- [x] **Step 6: Cleanup**

Remove all four fixture sites through the dashboard typed confirmation (or CLI fallback):

```powershell
foreach ($n in 'e2e-full-php','e2e-full-php-wsl','e2e-full-static-wsl','e2e-full-node-wsl') {
  & $ds site remove $n --home $h --yes
}
```

Expected: `site list` empty; `doctor` green.

---

## Task 2: PHP site configuration, ini, extensions, preview

**Depends on:** Task 1 (recreate `e2e-full-php`). **Artifacts:** `task-2/`

- [ ] **Step 1: Open Configuration and edit safe fields**

Site Detail → Configuration → Edit configuration:
- Change document root `.` → `public` (create `public/index.php` first), save; verify Document root `public`.
- Toggle Front controller off/on; save; verify behavior.
- Toggle SPA fallback off; save.
- Change Apache AllowOverride (if visible) between `all` and `none`; save; verify.
- Invalid document root `../outside` rejected; no save.

Expected: each save produces a toast, detail refresh, no console errors; `devstack site show --json` reflects the value.

- [ ] **Step 2: Open PHP settings and exercise directives**

Site Detail → PHP settings:
- Set `memory_limit=700M` and `max_execution_time=210`.
- Save/apply; verify detail or `devstack site ini e2e-full-php` shows both.
- Run `devstack php --site e2e-full-php -- -r "echo ini_get('memory_limit');"` → `700M`.
- Run `curl.exe -k https://e2e-full-php.test/phpinfo.php` (create a phpinfo fixture) and assert both values.
- Remove one override with `--unset memory_limit`; verify revert.

CLI parity:

```powershell
& $ds site ini e2e-full-php --set memory_limit=700M --set max_execution_time=210 --home $h --yes
& $ds site ini e2e-full-php --json --home $h
& $ds php --site e2e-full-php --home $h -- -i
```

- [ ] **Step 3: Exercise extensions**

- Disable `fileinfo`; save; verify `php -m` lacks it and site still serves.
- Re-enable `fileinfo`; save; verify present.
- Try an unknown extension `definitely_missing`; assert a clear rejection and no mutation.
- `site ext e2e-full-php --inherit fileinfo` restores default.

- [ ] **Step 4: Exercise raw ini and generated preview**

- Open Global/site raw ini editor; set `memory_limit=512M` (or append a comment), save; verify applied.
- Revert to original raw content.
- Open generated ini preview; assert the expected directives are visible and the preview is read-only.
- Invalid raw line (`not a directive`) rejected with a line-specific error.

- [ ] **Step 5: Restart/apply behavior**

Stop the site, change a PHP directive, start; verify FPM applies it. Change while running; verify restart-required or automatic apply behavior matches the UI text.

- [ ] **Step 6: Cleanup**

Remove `e2e-full-php`; delete `public` fixture; `doctor` green.

---

## Task 3: Source-mode copy/switch

**Depends on:** Task 1. **Artifacts:** `task-3/`

- [ ] **Step 1: Create a stopped static site**

`e2e-full-source` in Windows mode with known content. Stop it.

- [ ] **Step 2: Open Change source and inspect the confirmation**

Expected dialog/drawer:
- explains the copy direction, target path, verification, and that the original is kept;
- Cancel returns without changes.

- [ ] **Step 3: Run Windows → WSL copy**

Choose WSL target; confirm. Expected:
- action disabled while running; enabled while stopped;
- copy verified (SHA-256 or equivalent) before route republish;
- site `source_mode=wsl`, host path UNC, backend path POSIX;
- `https://e2e-full-source.test` still returns the content;
- original Windows folder still exists.

- [ ] **Step 4: Run WSL → Windows copy**

Stop; Change source back to Windows; confirm. Verify path/content and original WSL folder retained.

- [ ] **Step 5: Negative cases**

- Change while running: control disabled and copy explains stopping first.
- Invalid/escaping target rejected.
- Missing source/target error is clear; no partial route.
- If staged removal is unsupported (501 per docs), assert the explicit error and no deletion.

- [ ] **Step 6: CLI parity**

Use `devstack site show --json` after each switch; if a CLI source command exists in this build, run its help and the same copy; otherwise record that the dashboard is the only surface and mark CLI N/A.

- [ ] **Step 7: Cleanup**

Remove the site; remove copied fixture folders; `doctor` green.

---

## Task 4: PHP runtimes install/remove/migrate

**Depends on:** Task 0. **Artifacts:** `task-4/`

- [ ] **Step 1: Discover PHP builds**

```powershell
& $ds runtime available --kind php --home $h
& $ds runtime available --kind php --json --home $h | Out-File "$outRoot\task-4\php-available.json"
```

Expected: embedded PHP builds 8.2–8.5 with exact IDs.

- [ ] **Step 2: Install PHP 8.5 from the dashboard**

Runtimes → Available PHP → install `php@8.5.12-r1` (or newest 8.5 in this build). Expected: install dialog, progress, installed badge; `runtime list` has 8.5.

CLI parity:

```powershell
& $ds runtime install php@8.5.12-r1 --home $h --yes
```

- [ ] **Step 3: Create PHP sites on 8.4 and 8.5**

Use Task 1 fixtures; pin one site to each. Verify HTTP `PHP_VERSION` and `php -v`.

- [ ] **Step 4: Migrate a PHP line**

```powershell
& $ds runtime migrate 8.4 --to 8.5 --home $h --yes
```

Expected: every 8.4 site moves to the 8.5 build, routes republished, HTTP reports 8.5; output lists each site; no 8.4 site remains.

Dashboard: Runtimes → migration controls, choose 8.4 → 8.5, confirm; same assertions.

- [ ] **Step 5: Removal protection and removal**

- Attempt `runtime remove php@8.5...` while sites pin it → `PHP_VERSION_IN_USE` / `RUNTIME_IN_USE` naming sites.
- Remove/migrate the sites, then remove the unused PHP build; verify directory removed and `runtime list` updated.
- Restore sites to 8.4 and remove 8.5.

- [ ] **Step 6: Negative cases and cleanup**

Unknown PHP line/build, in-use default, dashboard remove confirmation/cancel; verify errors and no mutation. Leave PHP default `php@8.4.27-r1`; `doctor` green.

---

## Task 5: Services full lifecycle

**Depends on:** Task 0. **Artifacts:** `task-5/` per service.

Services and order:
1. nginx (web)
2. Apache (exclusivity test, uninstall)
3. MySQL (mysql family)
4. MariaDB (exclusivity test, uninstall)
5. PostgreSQL (standalone)
6. Redis (already tested; retest install/activate only if needed)
7. phpMyAdmin (requires active MySQL)

- [ ] **Step 1: Install nginx through dashboard and CLI**

Dashboard Services → nginx → Install. Expected: installed, inactive; image digest shown; ports 80/443 ownership; no console errors.
CLI parity: `devstack service install nginx --yes`, `service status nginx --json`, `service list`.

- [ ] **Step 2: Activate, logs, config, restart, deactivate**

- Activate nginx; wait for active; verify `https://devstack.test` and a fixture site still serve.
- Logs: open, assert nginx access/error lines; close.
- Configuration: `config list`, `show`; `config diff` with a safe custom include; `apply`; verify reload and content; `reset`; verify revert.
- Restart; verify active; Deactivate; verify inactive.
- `service logs nginx --tail 50`, `service config list nginx`, `service config show nginx <path>`, `service config diff/apply/reset` CLI parity.

- [ ] **Step 3: Web-server exclusivity**

Install Apache; attempt Activate Apache while nginx active → conflict error naming nginx; deactivate/uninstall or keep installed; activate Apache only after nginx deactivated. Verify built-in/native serving behavior. Restore nginx or builtin.

- [ ] **Step 4: MySQL lifecycle and credentials**

Install MySQL; activate; verify port 3306; logs; configuration safe edit/apply/reset; credentials reveal, rotate, reveal again (new value); connect with `mysql` CLI or Docker exec? Use `devstack service credentials mysql --json` and helper output; verify connection with returned credentials from WSL Docker.
Attempt MariaDB activation while MySQL active → exclusive-group conflict. Uninstall MariaDB/keep data.

- [ ] **Step 5: PostgreSQL lifecycle and credentials**

Install/activate PostgreSQL; logs; config diff/apply/reset; credentials reveal/rotate; connect with `psql` from WSL if available; deactivate; uninstall keep data then install again and verify data retained; uninstall with delete data and verify backup/removal.

- [ ] **Step 6: phpMyAdmin activation**

With MySQL active, install/activate phpMyAdmin; verify port 8082, dashboard link, HTTP 200/cookie challenge; credentials reveal/rotate if exposed; deactivate. Without MySQL active, attempt activation → clear dependency error.

- [ ] **Step 7: Redis recheck (optional)**

If not already installed during this run, install/activate/deactivate/uninstall Redis; expected behavior already proven in the Node E2E report.

- [ ] **Step 8: Uninstall matrix**

For each service:
- uninstall keeping data → data directory remains;
- reinstall and verify the data is retained where applicable;
- uninstall with delete data → typed `redis`/service ID confirmation, backup/removal, data gone.
Expected: no containers, no active services, config/data dirs removed only with delete data.

- [ ] **Step 9: Final service state**

`service list --json` all `not_installed`; remove any leftover directories manually only if the uninstaller left them and evidence confirms they are test data. `doctor` green.

---

## Task 6: Settings listener ports

**Depends on:** Task 0. **Artifacts:** `task-6/`

- [ ] **Step 1: Capture current ports**

Settings → Listener ports; record HTTP `8080`, HTTPS `8443`, dashboard `7070`; capture the helper's proxy targets (`devstack vhost status --json`).

- [ ] **Step 2: Save non-default ports**

Change to HTTP `18080`, HTTPS `18443`, dashboard `17070`; Save. Expected toast `Ports saved - restart required`; `config.yaml` changes; helper proxy targets update after daemon restart.
`devstack daemon restart`; verify dashboard on `http://127.0.0.1:17070`, site URLs still on 80/443, `doctor` green.

- [ ] **Step 3: Invalid values**

- Same HTTP/HTTPS/dashboard values → validation error.
- Out-of-range 0/70000 → validation error.
- Conflict with an occupied test port → actionable error; no partial write.

- [ ] **Step 4: Restore**

Set back to 8080/8443/7070; restart; verify dashboard and sites; `config.yaml` baseline. If the dashboard becomes unreachable, use `devstack` CLI/API on the Unix socket or restore `config.yaml` from Task 0 backup and restart.

---

## Task 7: Trust repair/remove/restore

**Depends on:** Task 0. **Artifacts:** `task-7/`

- [ ] **Step 1: Baseline trust**

`devstack trust status --json`, Settings → Trust; all targets ok. Capture.

- [ ] **Step 2: Repair**

Dashboard Trust → Repair; CLI `devstack trust repair --yes`; expected all targets ok, no host changes; `doctor` green. Run repair twice to prove idempotence.

- [ ] **Step 3: Remove trust**

Dashboard Trust → Remove; typed confirmation if required; CLI `devstack trust remove --yes`. Expected OS/browser trust removed; `trust status` reports missing; `doctor` reports trust failure; HTTPS browser warning (use `curl -k` for assertions).

- [ ] **Step 4: Restore**

`devstack init --yes` or `trust repair`; expected trust restored, hosts intact, daemon/helper healthy, `doctor` green. Re-open dashboard without TLS warning.

- [ ] **Step 5: Negative/edge**

Helper stopped → clear error, no partial trust mutation; foreign CA conflict → backup/force behavior. Restore.

---

## Task 8: Settings PHP CLI default, global ini, Composer, native tools

**Depends on:** Task 0/4. **Artifacts:** `task-8/`

- [ ] **Step 1: PHP CLI default**

Settings → PHP CLI default:
- set `8.4` / `php@8.4.27-r1`; verify resolved build and shim;
- clear → fallback newest installed;
- invalid build/line → `RUNTIME_MISSING`/`INVALID_REQUEST`;
- verify `php.cmd -v` and `composer.cmd --version` use the exact build.
CLI: `php default --status`, `php default 8.4`, `php default --clear`, `php default 99.99`.

- [ ] **Step 2: Global CLI php.ini**

Settings → Global CLI php.ini: edit `memory_limit=700M`, save; verify `php.cmd -i`; invalid line rejected; reset to original. CLI/API parity if available.

- [ ] **Step 3: Composer and native tools**

Settings → Native tools: install/update/remove Composer; `composer.cmd --version`; reinstall. Verify `php-default`, `composer`, `node-default` status rows, conflicts, and `tool list`. Remove/reinstall `node-default` only if it can be restored immediately.
CLI: `tool list`, `tool install composer --force`, `tool remove composer`, `tool install composer`.

- [ ] **Step 4: Restore defaults**

PHP `php@8.4.27-r1`, Node `24.22.1`, Composer installed, shims exact; `doctor` green.

---

## Task 9: Serving mode builtin/nginx/Apache

**Depends on:** Task 5. **Artifacts:** `task-9/`

- [ ] **Step 1: Baseline**

Settings → Serving = Built-in; `vhost status` builtin; sites serve.

- [ ] **Step 2: Switch to nginx**

Install/activate nginx service. Settings → Serving → nginx; confirm impact dialog. Expected: native nginx owns 80/443; builtin proxy/forwarders stopped; fixture site serves through nginx; dashboard vhost preview renders nginx; `vhost status` nginx.

- [ ] **Step 3: Switch to Apache**

Deactivate nginx, install/activate Apache. Switch Serving → Apache; verify same. Attempt switching while the other server is active → conflict/disabled with clear copy.

- [ ] **Step 4: Restore builtin**

Settings → Built-in; confirm. Expected builtin proxy returns, helper forwarders own 80/443, sites serve, vhosts removed. `doctor` green.

- [ ] **Step 5: CLI parity**

`vhost status`, `vhost use nginx|apache|builtin`, `vhost sync`, `vhost preview <site> --server nginx|apache`, `vhost path`; verify exit codes and output. Restore builtin.

---

## Task 10: Performance preview/apply/reset/benchmark

**Depends on:** Tasks 5 and 9 (nginx active for web includes). **Artifacts:** `task-10/`

- [ ] **Step 1: Baseline**

Services page → Performance panel; capture active profile, current values, benchmark history. `doctor` green.

- [ ] **Step 2: Profile selection and preview**

- Select Balanced → Preview; assert generated nginx/Apache/PHP/host values and warnings.
- Select Throughput → Preview; compare.
- Select Custom → edit valid JSON; save; preview.
- Invalid JSON → validation error; no changes.

- [ ] **Step 3: Apply**

With nginx active, Apply profile; type `APPLY`; keep host tuning unchecked first. Expected: backup, include files written, nginx reload, PHP OPcache/realpath settings merged, result details. Verify site serves and `doctor` green.

- [ ] **Step 4: Reset**

Reset; type `RESET`; expected previous host files/includes restored, site serves. Verify no performance-managed includes remain.

- [ ] **Step 5: Host tuning opt-in (guarded)**

If host tuning is available and the user explicitly approves system changes, check the box and apply; verify fixed-path backup/rollback; reset. If not approved, assert the checkbox is present and explains the host impact, and record it as intentionally not applied.

- [ ] **Step 6: Benchmark**

Select a registered site; Run benchmark; verify a result row/history with RPS, latency, errors; rerun and verify history; inspect details; no errors. If no site exists, create a temporary static site, benchmark, then remove.

- [ ] **Step 7: Cleanup**

Reset profile to baseline, remove temporary site, `doctor` green.

---

## Task 11: Doctor repair

**Artifacts:** `task-11/`

- [ ] **Step 1: Baseline**

`devstack doctor --json`; all checks pass; Settings → Doctor → Run checks; Repair button visible.

- [ ] **Step 2: Dashboard Repair**

Click Repair; wait; expected checks pass, toast, no host breakage. Reload; `doctor` green.

- [ ] **Step 3: CLI repair**

`devstack doctor --repair --json`; expected exit 0, checks pass, idempotent second run.

- [ ] **Step 4: Safe repairable drift**

Use a sandbox or a reversible drift (for example, stop helper/daemon and start again; or remove a test-only hosts entry if the repair path supports it). Do not break the live trust store without Task 7's restore path. Expected: repair restores and reports what it fixed.

- [ ] **Step 5: Final**

`doctor` green; no sites/services left; evidence captured.

---

## Task 12: Remaining CLI command groups

**Artifacts:** `task-12/`

Run each command with `--json` where supported; capture stdout/stderr/exit code. For destructive commands, use the restore path from Tasks 5/7/9/14.

- [ ] **Step 1: Trust**

```powershell
& $ds trust status --json
& $ds trust repair --yes
& $ds trust remove --yes
& $ds init --yes
& $ds trust status --json
```

Expected: status JSON, repair idempotent, remove/init restore, exit codes 0.

- [ ] **Step 2: Vhost**

```powershell
& $ds vhost status --json
& $ds vhost path --json
& $ds vhost preview e2e-full-static --server nginx
& $ds vhost use nginx
& $ds vhost sync
& $ds vhost use builtin
```

Expected: preview renders, mode switch succeeds/rolls back, sync rebuilds, builtin restored.

- [ ] **Step 3: Service**

Run help and one representative lifecycle/status/config/logs/credentials command for each installed service (Task 5); verify CLI and dashboard agree. Capture `service list --json`.

- [ ] **Step 4: PHP and site PHP/ini/ext**

```powershell
& $ds php default --status --json
& $ds php --site e2e-full-php -- -v
& $ds site php e2e-full-php --list
& $ds site php e2e-full-php 8.4
& $ds site ini e2e-full-php --set memory_limit=700M
& $ds site ini e2e-full-php --unset memory_limit
& $ds site ext e2e-full-php --disable fileinfo
& $ds site ext e2e-full-php --inherit fileinfo
```

Expected: exact version/directive/extension changes; invalid values rejected; site still serves.

- [ ] **Step 5: Runtime migrate**

Create two PHP sites on 8.4; `runtime migrate 8.4 --to 8.5 --yes`; verify both moved; migrate back or remove sites. Restore PHP 8.4 default.

- [ ] **Step 6: UI, completion, uninstall**

```powershell
& $ds ui --json
& $ds completion powershell | Select-Object -First 5
```

Expected: token URL JSON; completion script output.

Uninstall is the final destructive CLI gate:
- back up `state.db`, `config.yaml`, hosts, helper config, trust;
- `devstack uninstall --yes`; verify daemon/helper stopped, trust/hosts removed, project files intact;
- `devstack init --yes`; verify daemon/helper/agent, trust, hosts, dashboard, and doctor restored.
- If uninstall cannot be safely restored in the current environment, record BLOCKED with the exact reason and do not claim coverage.

- [ ] **Step 7: Every other root/help command**

Run `devstack --help`, each group's `--help`, `version`, `init --help`, `uninstall --help`, `daemon status/restart/stop/start`, and error paths (`unknown command`, bad flags). Expected non-zero/usage output, no panic.

---

## Task 13: SSE/event streams and terminal edges

**Artifacts:** `task-13/`

- [ ] **Step 1: Logs SSE**

Create a static/Node fixture. Start logs; pause; generate output; resume; verify missed lines are delivered or documented; clear; download; restart site and verify stream recovers; close/reopen.

- [ ] **Step 2: Events SSE**

From a browser context authenticated with the dashboard cookie, stream `/api/v1/events` with `fetch`/EventSource; trigger site create/start/stop/remove, runtime install/remove, service change; assert event types/data; reconnect after daemon restart; no duplicate routes/events.

- [ ] **Step 3: Terminal**

- Start terminal; run `pwd`, `node -v`, `npm -v`, `php -v` where applicable.
- Resize to 900x650 and 1200x800; verify no disconnect.
- Send Ctrl+C; verify prompt returns.
- Close; reconnect; verify new session.
- Stopped site: verify terminal still opens per known behavior or the documented stopped-site error.
- Idle timeout: wait for configured timeout or use a short test timeout; verify clean close.
- Verify `DEVSTACK_SITE`, `DEVSTACK_PROJECT`, PATH, and site shims.

- [ ] **Step 4: Cleanup**

Close terminals, remove fixtures, verify no orphan PTYs; `doctor` green.

---

## Task 14: Final full regression, cleanup, and report

**Artifacts:** `task-14/` and `docs/superpowers/audits/2026-10-30-dashboard-full-e2e.md`

- [ ] **Step 1: Restore baseline**

Sites empty, services `not_installed`, serving builtin, Node `24.22.1`, PHP `8.4.27`, Composer installed, trust/hosts healthy, ports 8080/8443/7070, performance reset, no host tuning applied. Confirm against Task 0 baseline JSON.

- [ ] **Step 2: Re-run every gate**

```powershell
gofmt -l .
go build ./...
go vet ./...
go test ./...
npm --prefix ui test -- --run
npm --prefix ui run check
npm --prefix ui run build
pwsh -NoProfile -File test/windows/installer_test.ps1
powershell.exe -NoProfile -File test/windows/installer_test.ps1
pwsh -NoProfile -File test/windows/lifecycle_test.ps1
powershell.exe -NoProfile -File test/windows/lifecycle_test.ps1
pwsh -NoProfile -File test/release/release_windows_ps_test.ps1
$env:DEVSTACK_HOME='C:\Users\Aram\.devstack'
pwsh -NoProfile -File test/windows/vertical_slice.ps1
```

Expected: all green; record any environment skips with the exact reason.

- [ ] **Step 3: Final live assertions**

```powershell
& $ds doctor --json --home $h
& $ds site list --json --home $h
& $ds service list --json --home $h
& $ds node default --status --json --home $h
& $ds php default --status --json --home $h
```

Expected: doctor no failures; sites empty; services not installed; defaults exact; wrappers run.

- [ ] **Step 4: Dashboard final sweep**

Fresh tab: Sites, Services, Runtimes, Doctor, Settings; assert 0 console errors/warnings; screenshots for the report.

- [ ] **Step 5: Write the report**

Create `docs/superpowers/audits/2026-10-30-dashboard-full-e2e.md` and record:
- each task PASS/FAIL/BLOCKED with exact commands and evidence paths;
- every bug with root cause, files, test, fix commit, and re-run proof;
- all destructive actions and restoration evidence;
- remaining environment skips (for example, IPv6 dial restrictions);
- final commit SHA and remote push.

Link the report from `docs/README.md` and `docs/handoff.md`.

- [ ] **Step 6: Commit and push**

```powershell
git add internal ui test docs api
git commit -m "test: complete full dashboard and cli e2e coverage"
git push origin master
```

Do not commit `dist/`, `.playwright-mcp/`, runtime data, or secrets.

## Self-review checklist

- [ ] Every item in “Why this plan exists” maps to a task above.
- [ ] Every destructive task has a backup and a restore step.
- [ ] Every task ends with `doctor` green and no leftover sites/services.
- [ ] Every dashboard assertion names a visible label and captures console errors.
- [ ] Every CLI command has an expected exit code and JSON/table assertion.
- [ ] No task weakens a security boundary to pass.
- [ ] Final gate includes Go, UI, installer, lifecycle, release, vertical slice, and live state.
- [ ] The final report is committed and linked from `docs/README.md` and `docs/handoff.md`.


