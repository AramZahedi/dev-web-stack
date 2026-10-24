# Release and packaging

This document covers the Linux x86_64 M1 release: how a tag becomes a
checksummed archive, how the archive is installed and upgraded, and the gated
PHP catalog merge that runs only after real PHP artifacts are published.

## Scope and status

- **Platform:** Linux x86_64 (glibc 2.35 or newer) only.
- **Artifacts:** unsigned `tar.gz` archives with a SHA-256 manifest. Signed
  packages and installers are M4 work.
- **No agent:** the archive's binaries are `devstack` and `devstack-helper` only.
  There is no `devstack-agent` build.
- **No other platforms:** no Windows, WSL, macOS, or arm64 release artifact is
  produced by this release path.
- **No auto-update:** devstack never checks for or installs updates on its own.
  Installing a newer release is an explicit installer run.
- **No diagnostics bundle:** M1 does not create a diagnostics zip/bundle.
  `devstack doctor --json` is the support surface.
- **Not published yet:** this branch has no release tag and no GitHub release.
  The commands below describe the release path; they do not claim a published
  artifact exists.

Windows/WSL is deferred with Plan 3; signing, diagnostics bundles, and
auto-update are M4 non-goals. The Plan 4 acceptance report records the release,
install, upgrade, and remaining gates:
[2026-10-24-dev-web-stack-plan-4-acceptance.md](superpowers/plans/2026-10-24-dev-web-stack-plan-4-acceptance.md).
The M1 handoff records branch/commit state and next steps:
[2026-10-24-m1-handoff.md](superpowers/status/2026-10-24-m1-handoff.md).

## Version and tag format

Release tags use:

```text
v<major>.<minor>.<patch>
```

An optional prerelease suffix (`v1.2.3-rc.1`) is accepted by the tag guard and
the local packager. The version embedded in the binary and in the archive name
drops the leading `v`, so tag `v0.2.0` produces
`devstack_0.2.0_linux_amd64.tar.gz`.

Check the embedded identity with:

```sh
devstack version
devstack version --json
```

The JSON form includes the release version, commit, helper protocol, OS, and
architecture. Release builds report the tag version; local development builds
may report `dev`.

## What the archive contains

```text
devstack_<version>_linux_amd64.tar.gz
  devstack                (executable)
  devstack-helper         (executable)
  install.sh              (executable)
  README.md
  LICENSE                 (when the repository has one)
checksums.txt             (one lowercase SHA-256 line for the archive)
```

`checksums.txt` names only the archive basename so the installer can verify it
from the download directory. A checksum mismatch aborts before extraction or
any privileged command.

## Local packaging

The local packager mirrors the GoReleaser release and is useful before tagging:

```sh
make release VERSION=0.2.0
```

It builds the UI (unless `--skip-ui` is passed directly to
`build/release.sh`), cross-compiles both binaries with `CGO_ENABLED=0` for
`linux/amd64`, and writes the archive plus `checksums.txt` under
`dist/release/`. The intermediate binaries go to `dist/` by default; pass
`--build-dir <dir>` to use another directory. Tar member mtimes are pinned to
`SOURCE_DATE_EPOCH` (default `0`) and gzip runs with `-n`, so two builds of the
same source and version are byte-identical. The packaged binary is executed to
confirm it reports the requested version.

Run the archive contract test with:

```sh
make release-test
```

The test uses a temporary `--build-dir`, so it never overwrites or creates
`dist/devstack`/`dist/devstack-helper`. It also compares two independent builds
and asserts the archive and checksums are identical.

## Tag-driven release workflow

`.github/workflows/release.yml` publishes the release. It runs on:

- a pushed tag matching `v*`; or
- a manual `workflow_dispatch` with an existing `tag` input.

Both paths pass a tag guard before building. The tag must match
`v<major>.<minor>.<patch>` (with an optional prerelease suffix), the checkout is
pinned to `refs/tags/<tag>`, and the checked-out commit must equal the tag's
commit. A branch push, a malformed tag, or a tag that does not resolve fails the
job; nothing is published.

The workflow uses least privilege: the workflow default is `contents: read`, and
only the release job receives `contents: write` for the GitHub release. It does
not print secrets.

Before GoReleaser runs, the workflow:

1. installs Go from `go.mod` and Node 22;
2. runs the UI install, type check, unit tests, and production build;
3. runs `go test ./... -race` and `go vet ./...`;
4. runs `goreleaser check` to validate `.goreleaser.yaml`;
5. builds a GoReleaser snapshot, verifies `checksums.txt`, and inspects the
   archive member set and packaged version before publishing.

It then runs `goreleaser release --clean` through
`goreleaser/goreleaser-action@v6` with `GITHUB_TOKEN`. GoReleaser uses the
repository `.goreleaser.yaml`, builds `devstack` and `devstack-helper` for
linux/amd64 only, writes `checksums.txt`, and attaches `install.sh` to the
release. The configuration's `before` hook runs the UI build again so the
embedded dashboard matches the tagged source.

The workflow is configured to run `goreleaser check`, so a live CI run is the
first full GoReleaser validation when GoReleaser is not installed locally. The
local `test/release/workflow_test.sh` only validates the checked-in workflow and
configuration contract; it does not prove CI is green or that a release was
published.

The release workflow does **not** publish a PHP catalog. PHP catalog publication
is a separate gated step described below.

## Install from a release

Release installs download the archive and `checksums.txt` over HTTPS, verify the
checksum, reject unsafe archive members, and check the packaged version before
installing:

```sh
./install.sh --release 0.2.0
```

A local archive and checksum file can be used instead:

```sh
./install.sh \
  --archive devstack_0.2.0_linux_amd64.tar.gz \
  --checksums checksums.txt
```

`--base-url https://...` points at a mirror. Plain HTTP is rejected. See
[install.md](install.md) for source installs, dry runs, and the installer's
privilege model.

## Upgrade and database backup

Upgrades use the same verified archive path with `--upgrade`:

```sh
./install.sh --upgrade --release 0.2.0
```

The installer requires an existing `~/.local/bin/devstack`, stops the user
daemon before replacing binaries, keeps `/etc/devstack/helper.yaml`, and prints
the required `devstack init` follow-up. Downgrades are unsupported.

Before applying pending migrations to an existing database, the store creates a
consistent SQLite snapshot with `VACUUM INTO`:

```text
${DEVSTACK_HOME:-~/.devstack}/backups/state.db.pre-migrate-<UTC timestamp>.bak
```

At most three backups are kept. The backup is written before the first pending
migration; if it cannot be created or the backups directory cannot be rotated,
the migration is not applied. A fresh database skips the backup. See
[upgrade.md](upgrade.md) for the full upgrade and recovery procedure.

## PHP catalog gate (still BLOCKED)

The production catalog `internal/catalog/catalog.json` intentionally contains
**no PHP entries** in this environment. The PHP workflow
`.github/workflows/php.yml` builds and uploads a `php-catalog` artifact
(`{entries:[...]}`) for PHP 8.2, 8.3, 8.4, and 8.5, but it does not publish the
archives or edit the embedded catalog.

A release may merge that artifact only after all four checksummed
`php-<version>-r<revision>-linux-x64.tar.gz` archives have been published at a
real HTTPS host. Download those exact tarballs from the PHP workflow run (or
from the published host) into one local directory; `catalogmerge` verifies each
file's name, regular-file type, byte size, and SHA-256 before it writes
anything. The merge is a release-preparation step, not a CI commit, and it must
land in a commit before the release tag is created; a tag cannot add catalog
entries retroactively.

```sh
# Download the archives named by the generated php-catalog.json into the
# default artifacts/ directory (override with PHP_ARTIFACTS_DIR). The directory
# must contain exactly those tarballs and no other files.
mkdir -p artifacts
# ... copy or download php-<version>-r<revision>-linux-x64.tar.gz files here ...

# Validate first. --check writes nothing.
make php-catalog-merge \
  PHP_CATALOG_JSON=dist/php-catalog.json \
  CATALOG=internal/catalog/catalog.json \
  PHP_ARTIFACTS_DIR=artifacts \
  PHP_CATALOG_CHECK=1

# Then merge and commit the updated catalog with the release.
make php-catalog-merge \
  PHP_CATALOG_JSON=dist/php-catalog.json \
  CATALOG=internal/catalog/catalog.json \
  PHP_ARTIFACTS_DIR=artifacts
```

The equivalent direct command is:

```sh
go run ./cmd/catalogmerge \
  --catalog internal/catalog/catalog.json \
  --php-catalog dist/php-catalog.json \
  --artifacts artifacts \
  --check
```

Drop `--check` to write the catalog in place, or add `--output <path>` to write
the merged document elsewhere. `--artifacts` is required; a missing, extra,
unreadable, non-regular, or mismatched tarball aborts the merge.

`catalogmerge` refuses to write unless every incoming entry:

- has `kind: php` and `build: linux-x64`;
- belongs to exactly PHP 8.2, 8.3, 8.4, or 8.5 (an extra line is rejected);
- uses `revision >= 1` and an ID matching `php@<version>-r<revision>`;
- has an HTTPS URL with a non-empty hostname, no query, fragment, userinfo, or
  empty path segment, whose basename is exactly
  `php-<version>-r<revision>-linux-x64.tar.gz`;
- has a 64-character lowercase SHA-256 and a positive size;
- has a unique ID; and
- has a regular local artifact in `--artifacts` whose bytes match that size and
  SHA-256.

It also requires at least one entry for each of PHP 8.2, 8.3, 8.4, and 8.5.
Non-PHP entries are preserved in their existing order. Existing PHP entries are
replaced by the validated set, sorted by version and then revision. The write is
atomic, and malformed, duplicate, or HTTP entries fail without leaving partial
output. `--check` validates the complete merge without writing.

Until real 8.2-8.5 artifacts are published, this step remains **BLOCKED**:
do not invent URLs, checksums, sizes, or catalog entries. The production
catalog must stay Node-only and `devstack runtime list` may show no PHP builds.

## Release checklist

1. Confirm the branch is clean and the focused tests pass:
   `go test ./... -race`, `go vet ./...`, UI checks (`npm run check`,
   `npm test`, `npm run build`), and `make release-test`.
2. Confirm `.goreleaser.yaml` still builds only `devstack` and
   `devstack-helper` for linux/amd64, with `install.sh` attached.
3. Create and push the tag (`git tag v0.2.0 && git push origin v0.2.0`), or run
   the workflow manually with that tag.
4. Wait for the release workflow: tag guard, tests, `goreleaser check`, and
   `goreleaser release --clean` must all pass.
5. Download the published archive and `checksums.txt`; verify
   `sha256sum -c checksums.txt` and the final archive member list.
6. Test `./install.sh --dry-run --release <version>` on a clean machine or
   sandbox before announcing the release.
7. Leave the PHP catalog unchanged unless the four real artifacts and their
   checksums exist. If they do, run the gated merge above, commit the catalog,
   and build the release from that commit.
