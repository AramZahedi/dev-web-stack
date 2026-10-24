# dev-web-stack M1 — Plan 4: Release Packaging, Upgrade, and M1 Acceptance (Linux)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Use checkboxes (`- [ ]`) for tracking.

**Goal:** Ship a reproducible, checksum-verified Linux x86_64 release of dev-web-stack; let `install.sh` install or upgrade from a published archive; preserve data across schema migrations; make the PHP catalog publication a gated release step; polish the docs; and run an honest full-M1 acceptance for the Linux scope.

**Architecture:** A tag-driven release workflow packages the two Linux binaries (`devstack`, `devstack-helper`) and the installer into one archive with a SHA-256 manifest. The installer supports both a source-tree `dist/` install and a verified release archive (local file or HTTPS download), extracts with traversal/symlink guards, and keeps existing config on upgrade. The store backs itself up before applying pending migrations. A separate `catalogmerge` command merges the PHP workflow's generated catalog entries into the embedded catalog only when real checksummed 8.2–8.5 artifacts exist; until then the production catalog stays empty. A single acceptance runner records PASS/FAIL/SKIP/BLOCKED for every M1 Linux criterion instead of claiming the deferred Windows/PHP-artifact gates.

**Tech Stack:** Go 1.26+, GoReleaser v2, GitHub Actions, Svelte 5 + Vite, Bash, SHA-256, SQLite, nginx/Apache (external-mode tests only).

**Spec:** `docs/superpowers/specs/2026-10-22-dev-web-stack-m1-design.md` §§3, 17, 19–21; `docs/superpowers/specs/2026-10-22-external-vhosts-design.md` for external serving. Plans 1 and 2 are implemented; Plan 2 is merge-ready at `6add708`.

**Depends on:** Plan 1 (`4f7ab97`) and Plan 2 (`6add708`). Plan 3 (Windows/WSL) is **deferred by user request** and is out of scope.

> **Scope ruling (spec over plan-series outline):** M1 §3 explicitly lists diagnostics bundles, auto-update, signed MSI/deb/rpm packaging, and log aggregation as M4 non-goals. The older Plan 4 outline mentioned signed packages and a diagnostics bundle; this plan does **not** implement them. It ships **unsigned, checksummed tarballs**, keeps `devstack doctor --json` as the support surface, and leaves signing/auto-update/diagnostics bundles for M4.
>
> **Plan 3 ruling:** release artifacts are Linux amd64 only. There is no `devstack-agent`, no Windows archive, no WSL installer, and no Windows trust-store work in this plan.

**Build-artifact gate:** `internal/catalog/catalog.json` still contains **no PHP entries**. The release tooling can merge real entries produced by `.github/workflows/php.yml`, but no real 8.2–8.5 artifacts have been published in this environment. Do not invent URLs, checksums, or a catalog. The real side-by-side gate remains **BLOCKED**; M1 acceptance records that honestly.

## Global Constraints

- Linux x86_64, glibc 2.35+, systemd. No macOS/arm64/Windows release in this plan.
- Unsigned binaries only; SHA-256 checksums are mandatory. Signed packages are M4.
- Release version formats: tag `v<major>.<minor>.<patch>`; archive version without the `v`. Dev builds may report `dev`.
- Release archive name: `devstack_<version>_linux_amd64.tar.gz`; checksum file: `checksums.txt`; members: `devstack`, `devstack-helper`, `install.sh`, `README.md`, and `LICENSE` when present. No agent/Windows members.
- The installer must run as the installing user, never root; `sudo` is limited to the helper binary/config/unit. It never modifies project files, installs packages, or configures nginx/Apache.
- Release downloads use HTTPS only. Archive extraction rejects absolute paths, `..` components, and symlink/hardlink entries. A checksum mismatch aborts before any install step.
- Installer upgrades keep the existing helper config by default. The daemon is stopped before replacing binaries, and the store backs up the SQLite database before applying pending migrations. Downgrades are unsupported.
- No automatic update check or self-update in M1. No diagnostics zip/bundle. No remote catalog fetch.
- Preserve every Plan 1/Plan 2 security and lifecycle behavior: helper peer-UID checks, host/CSRF/session checks, vhost validation, PHP validate-then-swap, port ownership, cleanup contexts, artifact gates, and dashboard design.
- Every task ends with a green focused test run and one commit. No TODO/TBD placeholders.
- Use **`opencode-go/deepseek-v4.1-flash#max`** for every implementer/reviewer subagent.
- Do not merge to a shared branch or publish a release as part of this plan; the controller asks before those side effects.

## Review Focus

1. **Archive integrity and release identity.** The archive contains exactly the two Linux binaries, installer, README/license; no agent/Windows/secret files; the archive name and binary version match the tag; checksums cover every byte and are generated from the published archive.
2. **Installer download/extraction safety.** HTTPS only, exact checksum match before extraction, traversal/symlink rejection, no partial install after verification failure, no root-owned user files, and no project-file deletion.
3. **Upgrade data safety.** Existing config survives; the daemon is stopped before binary replacement; the SQLite database is backed up before pending migrations; helper/daemon protocol mismatch fails with the existing upgrade instruction; `devstack init` re-syncs hosts/forwarders.
4. **PHP catalog gate.** `catalogmerge` accepts only real HTTPS checksummed entries for all four supported lines, preserves Node entries, writes atomically, and cannot be used to invent a production URL/checksum. The production catalog remains empty in this environment.
5. **Release workflow safety.** Tag-only, least-privilege token, Linux-only matrix, GoReleaser config validated, no Windows/agent build, no release on a dirty/incorrect tag, and CI covers the packaging/installer/catalog tests.
6. **Acceptance honesty.** The runner and report distinguish PASS from SKIP/BLOCKED; Windows/WSL, real PHP 8.2/8.5 artifacts, Apache HTTPS, browser trust, and in-flight continuity are not reported as passing.

---

## File Structure

- `.goreleaser.yaml` — GoReleaser v2 builds, archive, checksums, release files.
- `.github/workflows/release.yml` — tag-driven Linux release workflow.
- `.github/workflows/ci.yml` — add release/installer/catalog tests and shell checks.
- `build/release.sh` — local deterministic Linux archive/checksum builder.
- `install.sh` — source or release install, verified extraction, upgrade mode.
- `internal/cli/root.go` — `version --json` release/protocol metadata.
- `internal/store/migrate.go` — pre-migration SQLite backup.
- `cmd/catalogmerge/` — merge verified PHP catalog entries into `internal/catalog/catalog.json`.
- `test/release/` — archive, installer, and workflow tests.
- `test/acceptance/m1.sh` — Linux M1 acceptance runner.
- `docs/release.md`, `docs/upgrade.md`, `docs/install.md`, `docs/php.md`, `README.md` — release/upgrade/support docs.
- `docs/superpowers/plans/2026-10-24-dev-web-stack-plan-4-acceptance.md` — final M1 acceptance report.

---

## Task 1: Release archive contract, local packager, and version metadata

**Files:**
- Create: `.goreleaser.yaml`
- Create: `build/release.sh`
- Create: `test/release/release_test.sh`
- Modify: `Makefile`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `.gitignore` only if a generated release path is missing

**Interfaces:**
- `build/release.sh --version <semver> [--output <dir>] [--skip-ui]` builds the UI unless skipped, builds both Linux amd64 binaries with `-trimpath` and version/commit ldflags, writes `devstack_<version>_linux_amd64.tar.gz` and `checksums.txt` under `dist/release/` by default.
- Archive members: `devstack`, `devstack-helper`, `install.sh`, `README.md`, and `LICENSE` when present. `install.sh` is executable. No `devstack-agent`, no Windows names, no docs tree.
- `make release VERSION=<semver>` invokes the script; `make release-test` runs `test/release/release_test.sh`.
- `devstack version --json` keeps the existing `version`, `commit`, `string` fields and adds `protocol`, `os`, `arch`, and `go_version`. Plain `devstack version` output is unchanged.
- `.goreleaser.yaml` uses GoReleaser v2, `before: make ui`, builds both `./cmd/devstack` and `./cmd/devstack-helper` for `linux/amd64` only, names the archive exactly like `build/release.sh`, writes `checksums.txt`, and attaches `install.sh` to the GitHub release (`release.extra_files`). It must not build `devstack-agent` or any Windows target.

**Steps:**

- [ ] **Step 1: Add the failing release test**

Create `test/release/release_test.sh` using temp directories. It should:
- run `build/release.sh --version 0.0.0-test --skip-ui --output "$tmp/out"`;
- assert the archive and `checksums.txt` exist;
- list archive members and assert exactly the expected set, with no `devstack-agent`, `windows`, or `darwin`;
- extract to a temp dir, run `./devstack version --json`, and assert `version == "0.0.0-test"` and `protocol` is non-empty;
- assert `sha256sum -c checksums.txt` succeeds from the output directory;
- corrupt a copied archive and assert `sha256sum -c` fails (proves the checksum is real);
- assert `install.sh` is executable in the archive.

Run it and confirm it fails because `build/release.sh` does not exist.

- [ ] **Step 2: Implement the local packager**

`build/release.sh`:
- `set -euo pipefail`.
- Normalize a leading `v` and require `^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$`; reject path separators/whitespace.
- Resolve the repo root from the script path.
- Build UI with `make ui` unless `--skip-ui`.
- Build `dist/devstack` and `dist/devstack-helper` with `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X .../version.Version=$version -X .../version.Commit=$commit"`.
- Stage into a fresh temp dir; copy the two binaries, `install.sh` (0755), `README.md`, and `LICENSE` if present.
- Create the archive with stable ordering and no absolute paths: `tar --sort=name --owner=0 --group=0 --numeric-owner -czf`.
- Write `checksums.txt` from inside the output directory so the line is just the archive basename plus two spaces.
- Verify the packaged binary reports the requested version; fail otherwise.
- Print the archive path and checksum.

Add `Makefile` targets. Do not add release output to git.

- [ ] **Step 3: Add GoReleaser config**

Write `.goreleaser.yaml` v2 with:
- `project_name: devstack`;
- `before.hooks: [make ui]`;
- two builds (`devstack`, `devstack-helper`) with `main`, `binary`, `env: [CGO_ENABLED=0]`, `goos: [linux]`, `goarch: [amd64]`, `flags: [-trimpath]`, and ldflags using `{{ .Version }}`/`{{ .ShortCommit }}`;
- one archive with `name_template: devstack_{{ .Version }}_linux_amd64`, `formats: [tar.gz]`, and `files: [install.sh, README.md, LICENSE*]`;
- `checksum.name_template: checksums.txt`;
- `release.extra_files: [{glob: install.sh}]`;
- no `devstack-agent`/Windows id.
Add a short comment that Windows/agent are deferred with Plan 3 and that signing/diagnostics bundles are M4.

- [ ] **Step 4: Add version metadata**

Extend the existing `devstack version --json` object in `internal/cli/root.go` with `protocol` (use `helperproto.Version`), `os`, `arch`, and `go_version` (`runtime.Version()`). Keep the human output exactly as before. Add or extend a CLI test asserting the JSON fields and that plain output still contains the version string.

- [ ] **Step 5: Run the focused tests**

```sh
bash -n build/release.sh test/release/release_test.sh
shellcheck --severity=error build/release.sh test/release/release_test.sh
bash test/release/release_test.sh
go test ./internal/cli -run TestVersion -count=1
make release-test
```

- [ ] **Step 6: Commit**

```sh
git add .goreleaser.yaml build/release.sh test/release/release_test.sh Makefile internal/cli/root.go internal/cli/cli_test.go
git commit -m "feat: package checksummed Linux release archives"
```

---

## Task 2: Verified installer release mode and upgrade data safety

**Files:**
- Modify: `install.sh`
- Create: `test/release/installer_test.sh`
- Modify: `internal/store/migrate.go`, `internal/store/store.go` only as needed
- Create/Modify: `internal/store/migrate_test.go`
- Modify: `docs/install.md`, `docs/upgrade.md`, `README.md`

**Interfaces:**
- New installer options:
  - `--release <version>`: download `devstack_<version>_linux_amd64.tar.gz` and `checksums.txt` from `https://github.com/AramZahedi/dev-web-stack/releases/download/v<version>/` by default; `--base-url <https-url>` overrides the release root (tests use a local HTTP server).
  - `--archive <path> --checksums <path>`: install from local files (mutually exclusive with `--release`; both required together).
  - `--upgrade`: require an existing `${USER_HOME}/.local/bin/devstack`; keep helper config; stop the daemon before replacing binaries; back up state through the store on next start; restart the helper and the daemon unit; print the `devstack init` follow-up.
  - `--dry-run` works with all source modes; it still verifies checksums and extraction but performs no writes.
- Release resolution validates:
  - the archive basename matches the expected release/version;
  - the checksum file contains exactly one matching SHA-256 line;
  - the archive has no absolute paths, `..` components, symlinks, or hardlinks;
  - the extracted `devstack version --json` reports the requested version (for non-`dev` releases).
- `build/release.sh` output is accepted unchanged by `install.sh --archive ... --checksums ...`.
- Store migration backup: before applying any pending migration to a non-empty database, write a consistent SQLite backup under `<db-path>.pre-migrate-<UTC timestamp>` (or a sibling `backups/` directory with a documented name), keep at most three, and fail closed if the backup cannot be created. A fresh/empty database does not need a backup.
- Existing `devstack init` remains the step that re-syncs hosts/forwarders and verifies helper protocol after an upgrade.

**Steps:**

- [ ] **Step 1: Add installer tests**

Create `test/release/installer_test.sh` with a fake `sudo`/`systemctl` PATH and a local HTTP server (`python3 -m http.server` or a tiny Go server) serving a release archive + checksums:
- `install.sh --dry-run --archive ... --checksums ...` succeeds and prints the install commands;
- `--release <version> --base-url http://127.0.0.1:<port>` is rejected because the URL is not HTTPS;
- a corrupted archive checksum fails before any `install`/`sudo` command is invoked;
- an archive containing `../evil`, `/absolute`, or a symlink is rejected;
- an archive whose `devstack version --json` reports the wrong version is rejected;
- `--archive` without `--checksums`, `--release` with `--archive`, and `--upgrade` without an existing CLI fail with clear messages;
- `--upgrade --dry-run` with an existing fake `${USER_HOME}/.local/bin/devstack` prints the upgrade/backup plan and does not write.
Run the test and confirm it fails before the installer changes.

- [ ] **Step 2: Implement verified release resolution**

Refactor `install.sh` carefully:
- Keep the existing source-tree (`dist/`) behavior as the default.
- Add a `SOURCE_MODE` (`source|release|archive`) and a temporary directory trap.
- Download with `curl -fL --proto '=https' --tlsv1.2 --retry 3 --connect-timeout 10` to a temp file; never pipe a remote script into bash.
- Use `sha256sum` (or `shasum -a 256` fallback) and compare the exact archive basename line. Require 64 lowercase hex.
- Reject non-regular archive files; list with `tar -tvzf` and reject names that are absolute, contain `..` components, begin with `-`, or whose type is symlink/hardlink. Extract with `tar -xzf --no-same-owner --no-same-permissions` into a fresh temp dir; verify the two binaries, installer, README, and version before any install command.
- Set `RELEASE_DIR` and use it for the binary install paths. Do not use `eval` on remote names.
- Keep `--dry-run` side-effect free after verification.

- [ ] **Step 3: Implement upgrade mode and pre-migration backup**

Installer:
- `--upgrade` requires the existing CLI and a real (non-`--dry-run`) install; refuse if the daemon cannot be stopped.
- Stop the user daemon before replacing binaries: use `systemctl --user stop devstack-daemon.service` when the unit is active, otherwise `${USER_BIN} daemon stop`; abort on failure.
- Reuse the existing unit/config flow; do not overwrite `/etc/devstack/helper.yaml` unless `--force-config`.
- After installing, restart the helper and (if it was active or upgrade mode) the user daemon unit; print the exact next step `devstack init` (do not silently run privileged trust/hosts changes from the installer).
- Record a `BACKUP_HINT` path in the summary when the store will back up on next start.

Store:
- In `migrate`, detect pending migrations before applying them. If the database already has applied migrations and pending work remains, create a consistent backup using `VACUUM INTO` (or a documented equivalent) while the store is single-connection and no write transaction is open. Use a filename under the DB directory such as `devstack.db.pre-migrate-<UTC>`; keep at most three and never delete a backup if pruning fails.
- If backup creation fails, return an error and do not apply migrations. A fresh database (no applied migrations) skips the backup.
- Add unit tests for: no backup on a fresh DB; backup created before a pending migration; backup failure blocks migration; retention keeps at most three. Use a test seam for the migration list if needed rather than modifying production migration files.

- [ ] **Step 4: Update docs**

`docs/install.md`:
- document source install vs release install (`install.sh --release 0.1.0`), `--archive/--checksums/--base-url`, checksum behavior, safe extraction, and the fact that the installer still does not run `init`.
- document `--upgrade` and the exact follow-up `devstack init`.
- document state backup location and downgrade limitation.
`docs/upgrade.md` (new): preflight, stop/install/start, automatic DB backup, protocol mismatch remediation, `devstack init`, verification with `devstack doctor`, rollback by reinstalling the previous release (downgrade unsupported).
`README.md`: link the upgrade guide and release install.

- [ ] **Step 5: Run focused tests**

```sh
bash -n install.sh test/release/installer_test.sh
shellcheck --severity=error install.sh test/release/installer_test.sh
bash test/release/installer_test.sh
go test ./internal/store -run 'Migrate|Backup' -count=1
go test ./internal/store ./internal/cli -count=1
./install.sh --dry-run
```

- [ ] **Step 6: Commit**

```sh
git add install.sh test/release/installer_test.sh internal/store docs/install.md docs/upgrade.md README.md
git commit -m "feat: verify release installs and back up before migrations"
```

---

## Task 3: Release workflow and gated PHP catalog merge

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `cmd/catalogmerge/main.go`, `cmd/catalogmerge/main_test.go`
- Create: `test/release/workflow_test.sh`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`
- Modify: `docs/release.md` (new), `docs/php.md`
- Do **not** modify `internal/catalog/catalog.json` with PHP entries.

**Interfaces:**
- `go run ./cmd/catalogmerge --catalog <path> --php-catalog <path> [--output <path>] [--check]`:
  - loads the current catalog and a PHP catalog document (`{entries:[...]}`);
  - requires at least one entry for each of PHP 8.2, 8.3, 8.4, 8.5;
  - validates `kind=php`, `build=linux-x64`, revision >= 1, HTTPS URL, 64-hex SHA-256, positive size, unique IDs, and URL basename `php-<version>-r<revision>-linux-x64.tar.gz`;
  - preserves all non-PHP entries in their existing order;
  - replaces existing PHP entries with the new validated set, sorted by version then revision;
  - writes atomically (`--check` validates but writes nothing);
  - refuses malformed/duplicate/HTTP entries with a non-zero exit and no partial output.
- `make php-catalog-merge PHP_CATALOG_JSON=<path> CATALOG=<path>` runs the tool; `--check` is available.
- `.github/workflows/release.yml`:
  - triggers on tags `v*` and `workflow_dispatch`;
  - `permissions: contents: write` only where needed;
  - checks out, sets up Go/Node, runs `go test ./... -race`, `go vet ./...`, UI check/test/build, `goreleaser check`, then `goreleaser release --clean` with `GITHUB_TOKEN`;
  - uses `goreleaser/goreleaser-action@v6` and the repo `.goreleaser.yaml`;
  - artifact/release contents are Linux amd64 only; no `devstack-agent` or Windows; `install.sh` attached; checksums generated;
  - no PHP catalog publication in this workflow (the PHP workflow's `php-catalog` artifact is a separate release step).
- `test/release/workflow_test.sh` asserts the workflow is tag-triggered, uses the GoReleaser action/config, has Linux-only expectations, attaches `install.sh`, and mentions no agent/Windows build.

**Steps:**

- [ ] **Step 1: Test the catalog merge contract first**

Add `cmd/catalogmerge/main_test.go` with table tests for:
- a valid 8.2/8.3/8.4/8.5 set preserving Node entries and writing deterministic output;
- a missing line, duplicate ID, HTTP URL, bad checksum, zero size, wrong basename, and `kind != php` each failing without changing the output;
- `--check` not writing;
- atomic output (a write failure leaves the old file intact if practical).
Run and confirm failure.

- [ ] **Step 2: Implement catalogmerge**

Use `encoding/json`, `net/url`, `crypto/sha256`-style validation, `os.CreateTemp` + fsync + rename, and clear errors. Accept the current catalog schema from `internal/catalog/catalog.json` (`{entries:[...]}`) and the PHP workflow output (`{entries:[...]}`). Do not import a YAML/JSON dependency beyond stdlib.

- [ ] **Step 3: Add the release workflow**

Write `.github/workflows/release.yml` with a tag guard:
- fail if the tag is not `v*` on `workflow_dispatch`/push;
- set `CGO_ENABLED=0`;
- run tests and UI build before GoReleaser;
- `goreleaser release --clean` with `GITHUB_TOKEN`;
- no `devstack-agent`/Windows build steps;
- attach `install.sh` through `.goreleaser.yaml`;
- keep `permissions` minimal and do not print secrets.
Add `test/release/workflow_test.sh` and run it.

- [ ] **Step 4: Wire CI and docs**

Add `go test ./cmd/catalogmerge`, `bash test/release/release_test.sh`, `bash test/release/installer_test.sh`, `bash test/release/workflow_test.sh`, and shellcheck for the new scripts to `.github/workflows/ci.yml` (or a dedicated job). Ensure the `ci.yml` branch list includes tags/PR behavior as appropriate and does not remove existing tests.

Write `docs/release.md`: tag/version, GoReleaser, archive/checksums, install.sh, Linux-only/unsigned, no agent, release checklist, database migration backup, and the PHP catalog gate. Update `docs/php.md` with the exact `catalogmerge` command and the remaining BLOCKED gate.

- [ ] **Step 5: Run focused tests**

```sh
go test ./cmd/catalogmerge -count=1
go test ./internal/catalog ./internal/cli -count=1
bash -n test/release/workflow_test.sh
bash test/release/workflow_test.sh
bash test/release/release_test.sh
bash test/release/installer_test.sh
```

If GoReleaser is installed locally, also run `goreleaser check`; otherwise record that CI is the first GoReleaser validation.

- [ ] **Step 6: Commit**

```sh
git add .github/workflows/release.yml .github/workflows/ci.yml cmd/catalogmerge test/release/workflow_test.sh Makefile docs/release.md docs/php.md
git commit -m "feat: add release workflow and gated PHP catalog merge"
```

---

## Task 4: Linux M1 acceptance runner, docs polish, and final report

**Files:**
- Create: `test/acceptance/m1.sh`
- Create: `docs/superpowers/plans/2026-10-24-dev-web-stack-plan-4-acceptance.md`
- Create: `docs/superpowers/status/2026-10-24-m1-handoff.md`
- Modify: `README.md`, `docs/install.md`, `docs/upgrade.md`, `docs/release.md`, `docs/php.md`, `docs/vhosts.md` as needed
- Modify: `Makefile` (`acceptance` target)

**Interfaces:**
- `test/acceptance/m1.sh [--quick] [--skip-playwright]`:
  - runs `go vet ./...`, `go test ./...`, `go test ./... -race` unless `--quick`, `make build`, UI `npm run check`, `npm test -- --run`, `npm run build`, and Playwright unless skipped;
  - runs `bash test/release/release_test.sh`, `bash test/release/installer_test.sh`, `bash test/release/workflow_test.sh`;
  - runs `bash install.sh --dry-run`;
  - runs `E2E_SKIP_BUILD=1 bash test/e2e/linux.sh` (high ports, no root/80/443);
  - runs the real-PHP acceptance when `/usr/local/php` exists;
  - checks no listeners on 43000–43999 (using `ss`/`lsof` when available);
  - prints a table of PASS/FAIL/SKIP/BLOCKED and exits 0 only if there are no FAIL; BLOCKED is allowed and printed.
- `make acceptance` runs the full Linux runner.
- The acceptance report maps M1 §21 items 1–12 to PASS/FAIL/SKIP/BLOCKED with commands and evidence. Windows/WSL (item 8) is **DEFERRED**, PHP 8.2/8.5 side-by-side is **BLOCKED** on artifacts, Apache HTTPS is **SKIP** without `mod_ssl`, browser trust/root install/uninstall are **MANUAL/BLOCKED**, and in-flight continuity is **NOT MEASURED**.
- The final handoff records branch/tag/commit state, release commands, known gates, and the next steps for M4 (signed packages, diagnostics bundles, auto-update) and Plan 3 (Windows/WSL), without starting them.

**Steps:**

- [ ] **Step 1: Write the runner and make target**

Implement the runner with a `run_phase` helper that records status, log path, and duration. Use temp logs and print the tail on failure. Do not hide failures behind `|| true`; explicitly classify known blocked/skip gates. Add `make acceptance`.

- [ ] **Step 2: Run the full Linux acceptance**

Run the full runner. Fix any real failure. Do not weaken a test or mark a failure as blocked. Record exact commands/output in the Plan 4 acceptance report.

- [ ] **Step 3: Write the M1 acceptance report**

Create `docs/superpowers/plans/2026-10-24-dev-web-stack-plan-4-acceptance.md` with:
- environment (OS, Go, Node, PHP, nginx, Apache, git);
- a table for M1 §21 items 1–12 with PASS/FAIL/SKIP/BLOCKED/MANUAL/DEFERRED and evidence;
- the exact blocked reasons: Windows/WSL deferred by user, real 8.2/8.5 artifacts not published, production PHP catalog empty, Apache HTTPS unavailable, browser trust/root install/manual checks not performed, in-flight continuity not measured;
- release artifact/checksum/installer verification results;
- upgrade/backup verification results;
- the final command list.
Do not claim full M1 success if those gates remain.

- [ ] **Step 4: Docs polish and handoff**

Update README and docs for:
- release install and upgrade, checksum verification, Linux-only/unsigned, no Windows/agent;
- automatic DB backup before migration and downgrade limitation;
- PHP catalog merge release step and remaining artifact gate;
- M1 status/limitations, Plan 3 deferred, M4 non-goals;
- `devstack doctor --json` as the support surface (no diagnostics bundle in M1).
Create `docs/superpowers/status/2026-10-24-m1-handoff.md` with exact branch/commit/tag state, commands, gates, and the next milestone. Link the Plan 4 acceptance report.

- [ ] **Step 5: Final focused verification**

```sh
bash -n test/acceptance/m1.sh test/release/*.sh install.sh build/release.sh
shellcheck --severity=error test/acceptance/m1.sh test/release/*.sh install.sh build/release.sh
make acceptance
git diff --check
```

- [ ] **Step 6: Commit**

```sh
git add test/acceptance/m1.sh Makefile docs README.md
git commit -m "docs: record Linux M1 release acceptance"
```

---

## Final review and completion

After Tasks 1–4:

1. Generate a whole-branch review package from `04067bc` (Plan 2 tip) to the Plan 4 head.
2. Dispatch the final whole-branch reviewer on **`opencode-go/deepseek-v4.1-flash#max`** with the Plan 4 spec, this plan, the acceptance report, and the deferred/blocked list.
3. Fix all Critical/Important findings in **one** fix dispatch, then one scoped re-review. Adjudicate residuals; no silent discards.
4. Re-run the full Linux acceptance at the final head; ensure no FPM listeners, no release artifacts with wrong versions, and no PHP entries in the production catalog.
5. Commit/push the branch; do not merge or publish without the user's explicit approval.
6. Create/update the final M1 handoff with the exact remaining gates (Windows/WSL, real PHP 8.2/8.5 artifacts, Apache HTTPS, browser/root manual checks, in-flight continuity, M4 items).

## Plan series and deferred work

- **Plan 1 — Foundation:** complete at `4f7ab97`.
- **Plan 2 — PHP:** complete/merge-ready at `6add708`.
- **Plan 3 — Windows/WSL:** **deferred by user request.** No agent, named pipe, Windows trust stores, `install.ps1`, or WSL lifecycle in this plan.
- **Plan 4 — Release (this plan):** Linux unsigned archives, verified installer/upgrade, gated PHP catalog merge, docs, M1 acceptance.
- **M4:** signed packages/installers, diagnostics bundles, auto-update, remote catalog, arm64, log aggregation.
