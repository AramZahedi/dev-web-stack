# PHP build pipeline

This directory builds the PHP linux-x64 artifacts that devstack installs from
its runtime catalog, verifies them, and emits catalog metadata. It does not
publish artifacts and it never edits `internal/catalog/catalog.json`.

> **Artifact gate:** the production catalog contains no PHP entries. A PHP build
> becomes installable only after CI publishes the four checksummed archives, a
> release job uploads them, and a subsequent release embeds their entries in
> `internal/catalog/catalog.json`. Until then `devstack runtime install` has no
> PHP build to install. CI uploads `php-catalog.json` as an artifact; merging it
> is a deliberate release step.

## Files

| File | Purpose |
|---|---|
| `build.sh` | Build one PHP artifact from the locked source and PECL tarballs |
| `smoke.sh` | Extract and verify an artifact, including a FastCGI request |
| `latest-patch.sh` | Print the newest stable patch for a PHP line |
| `update-lock.sh` | Refresh `extensions.lock` from verified upstream data |
| `extensions.lock` | Sorted `KEY=value` pins for PHP sources and PECL tarballs |
| `base-php.ini` | Build base ini: one `[PHP]` section, no extension directives |

## Quick reference

```sh
# Newest stable patch for a line
build/php/latest-patch.sh 8.4

# Refresh every pin, downloading and hashing the real tarballs
build/php/update-lock.sh

# Build (requires the dependencies below; do not run as root)
build/php/build.sh --line 8.4 --revision 1 --output dist/php

# Verify the archive, including a real FPM FastCGI request
build/php/smoke.sh dist/php/php-8.4.27-r1-linux-x64.tar.gz

# Emit one catalog entry for the artifact
go run ./cmd/phpcatalog \
  --version 8.4.27 --revision 1 \
  --artifact dist/php/php-8.4.27-r1-linux-x64.tar.gz \
  --sha256 "$(sha256sum dist/php/php-8.4.27-r1-linux-x64.tar.gz | cut -d' ' -f1)" \
  --size "$(stat -c '%s' dist/php/php-8.4.27-r1-linux-x64.tar.gz)" \
  --base-url https://example.invalid/php \
  --output dist/php/php-8.4.27-r1.json

# Focused Go tests and shell checks
make php-test
```

`build.sh --version` is optional. It defaults to the locked patch version and
must match the lock when supplied. `--revision` is required and must be at least
1. `--output` defaults to `dist/php`.

## Lock format

`extensions.lock` is committed, deterministic, shell-sourceable data: comments,
blank lines, then alphabetically sorted `KEY=value` assignments. Keys are
`PHP_<line>_VERSION`, `PHP_<line>_SHA256`, `PECL_<PACKAGE>_VERSION`, and
`PECL_<PACKAGE>_SHA256`. `build.sh` sources the file and refuses to run when a
required PHP or PECL entry is missing or not a lowercase 64-hex SHA-256.

`update-lock.sh` is the only supported writer:

- PHP versions and checksums come from
  `https://www.php.net/releases/index.php?json&version=<line>` and are verified
  by downloading `https://www.php.net/distributions/php-<version>.tar.xz` and
  hashing it locally.
- PECL versions are the newest release that PECL marks **stable** in
  `https://pecl.php.net/rest/r/<package>/allreleases.xml`; the `.tgz` is
  downloaded from `https://pecl.php.net/get/<package>-<version>.tgz`, checked as
  gzip data, and hashed locally.
- It writes through a temporary file in the destination directory and renames it
  into place, so a failed run never leaves a partial lock.
- If any package cannot be verified, the script reports it and exits non-zero
  **before** creating or replacing the lock. No partial lock is ever written.
  `build.sh` then fails with an actionable "run build/php/update-lock.sh" message
  for that package.

The current lock pins PHP 8.2.35, 8.3.36, 8.4.27, and 8.5.12. A PHP line is
advanced only by running the lock writer and committing the result; `build.sh`
never resolves a newer version on its own.

## Verified PECL extras

The lock verifies and the build includes these extras on every line:

| Package | Version | Notes |
|---|---|---|
| apcu | 5.1.28 | User cache |
| grpc | 1.84.0 | Builds the bundled gRPC core; the slowest extra |
| igbinary | 3.2.17RC1 | Serializer; PECL marks this release stable and it adds PHP 8.5 support. Headers are installed before redis/memcached |
| imagick | 3.8.1 | Needs `libmagickwand-dev` |
| memcached | 3.4.0 | Needs `libmemcached-dev`; built with igbinary, SASL disabled |
| msgpack | 3.0.1 | Serializer |
| pcov | 1.0.12 | Coverage driver |
| redis | 6.3.0 | Built with igbinary support |
| ssh2 | 1.5.0 | Needs `libssh2-1-dev` |
| xdebug | 3.5.3 | Zend extension, off by default |
| yaml | 2.3.0 | Needs `libyaml-dev` |

### Blocked entry

`imap` is **not** in the lock or any artifact:

- the extension manifest exposes `imap` only on PHP 8.2 and 8.3;
- PECL imap 1.0.3 (the verified upstream release) declares PHP >= 8.3, so it
  covers 8.3 but not 8.2;
- the bundled PHP 8.2/8.3 copy needs the UW-IMAP c-client library, which is not
  part of this build image. PHP 8.2 has both problems (below the PECL floor and
  no c-client); PHP 8.3 could use PECL imap 1.0.3, but that path is not wired
  into this pipeline.

The README and `docs/php.md` record this so the missing toggle is not mistaken
for a successful build. Adding it later requires a deliberate 8.2/8.3 split:
PECL imap for 8.3 and either c-client headers for the bundled 8.2 copy or a
decision to drop 8.2 support, plus an updated lock.

## Build dependencies

CI uses `ubuntu-22.04` so binaries target the glibc 2.35 baseline. Install the
build tools and development headers:

```sh
sudo apt-get update
sudo apt-get install -y --no-install-recommends \
  autoconf bison build-essential file libtool patchelf pkg-config re2c \
  libbz2-dev libcurl4-openssl-dev libffi-dev libfreetype6-dev libgdbm-dev \
  libgmp-dev libicu-dev libjpeg-dev libldap2-dev libmagickwand-dev \
  libmemcached-dev libonig-dev libpng-dev libpq-dev libreadline-dev \
  libsasl2-dev libsodium-dev libsqlite3-dev libssh2-1-dev libssl-dev \
  libtidy-dev libwebp-dev \
  libxml2-dev libxslt1-dev libyaml-dev libzip-dev zlib1g-dev
```

`build.sh` also needs `curl`, `jq`, `gcc`, `make`, `patchelf`, `file`, `ldd`,
`perl`, and `sha256sum`. It preflights `pkg-config --exists oniguruma libsodium`
and fails with an actionable message naming `libonig-dev`/`libsodium-dev` when
either module is absent. `smoke.sh` needs Go to build `cmd/php-smoke` unless
`PHP_SMOKE_BIN` points at an existing binary. Neither script runs as root; the
prefix is staged under a temporary directory and needs no system installation.

## What build.sh does

1. Validates the line, revision, and lock, then downloads the pinned PHP source
   over HTTPS and verifies its SHA-256 from the lock.
2. Configures PHP with `--enable-fpm`, `--disable-cgi`, `--without-pear`,
   `--prefix=<staged prefix>`, the required FPM/CLI options, and shared-extension
   flags for every standard module in `internal/catalog/php.json` except the
   blocked `imap` and PHP 8.5's built-in `opcache`.
3. Builds and installs, then downloads, verifies, and builds each locked PECL
   package with the new build's `phpize`/`php-config`. PECL modules are loaded as
   shared objects, never compiled into the CLI.
4. Flattens all `.so` files into `lib/php/extensions`, bundles non-glibc
   `ldd` dependencies transitively into `lib`, and sets RPATHs:
   - `bin/` and `sbin/` binaries: `$ORIGIN/../lib`
   - `lib/php/extensions/*.so`: `$ORIGIN/../..` (the real prefix `lib`) and the
     documented `$ORIGIN/../lib`
   - bundled libraries: `$ORIGIN`
5. Copies `base-php.ini` to `etc/php.ini` and writes a deterministic archive
   with the top-level directory
   `php-<version>-r<revision>-linux-x64/`.

`SOURCE_DATE_EPOCH` (default `0`) controls the archive mtime. `PHP_SOURCE_CACHE`
overrides the download cache (`~/.cache/devstack/php-sources` by default), and
`PHP_JOBS` overrides the parallel build job count. A failed build keeps its work
directory for inspection and prints the tail of the failing log.

PHP 8.5 builds OPcache into the PHP binary and no longer produces
`opcache.so`; the `--enable-opcache` configure flag was removed. `build.sh`
therefore omits that flag and the expected-module check on 8.5, and `smoke.sh`
skips the shared-module load there. The extension manifest marks OPcache as a
per-version built-in for 8.5 (`builtin_by_version`), so it is not offered as a
toggle and a default 8.5 ini does not reference `opcache.so`. PHP 8.2-8.4 still
build `opcache.so` as a shared Zend extension and keep the toggle.

## What smoke.sh does

1. Refuses root. Rejects archive members with `..` or absolute paths outside
   the expected top-level directory, and rejects symbolic or hard-link entries.
2. Runs `bin/php -n -v`, `sbin/php-fpm -n -v`, and `bin/php -n -m`, capturing
   both stdout and stderr and rejecting `Failed loading`, `PHP Startup`, and
   other load-error output.
3. Loads every standard shared module in manifest order (with `opcache` as a
   `zend_extension` on 8.2-8.4; 8.5 has built-in OPcache and no `opcache.so`);
   then loads every pinned PECL `.so` (preloading `igbinary` for
   redis/apcu/memcached and using `zend_extension` for xdebug). Each load is
   followed by an `extension_loaded()` assertion. Any load warning or missing
   module fails the run.
4. Builds a temporary ini from the line's default extension set, runs
   `php -c <ini> -r 'exit(0);'` with `PHP_INI_SCAN_DIR=` cleared, and fails on a
   non-zero exit or any output.
5. Finds a free port in 43000-43999, builds `cmd/php-smoke`, and runs it. That
   command renders a temporary FPM config with `internal/phpfpm`, starts
   `sbin/php-fpm --nodaemonize --fpm-config ... -c ...`, waits for TCP readiness,
   sends a FastCGI GET for a temporary `health.php`, asserts `200 ok`, and stops
   the process group with a bounded timeout.

Any failure exits non-zero, so CI does not upload the artifact.

## Catalog generation

`cmd/phpcatalog` validates the version/revision/artifact name, recomputes the
artifact size and SHA-256 (and rejects a mismatch with the supplied values),
requires an HTTPS base URL without userinfo, query, fragment, or doubled path
separators, and returns exactly one `catalog.Entry`:

```json
{
  "id": "php@8.4.27-r1",
  "kind": "php",
  "version": "8.4.27",
  "build": "linux-x64",
  "revision": 1,
  "url": "https://example.invalid/php/php-8.4.27-r1-linux-x64.tar.gz",
  "sha256": "...",
  "size": 12345678
}
```

With no `--output` it prints that object to stdout. With `--output` it atomically
writes only that object; it rejects an output path ending in
`internal/catalog/catalog.json` so the embedded catalog cannot be overwritten by
mistake. The CI catalog job combines the four files itself:

```sh
jq -s 'sort_by(.version) | {entries: .}' entries/*.json > php-catalog.json
```

## CI behavior

`.github/workflows/php.yml` runs a `line: [8.2, 8.3, 8.4, 8.5]` matrix on
`ubuntu-22.04`:

1. installs the dependencies above, restores the source cache, and resolves the
   locked version;
2. runs `bash -n`, `shellcheck --severity=error`,
   `go test ./cmd/phpcatalog ./cmd/php-smoke`, and `go vet`;
3. runs `build.sh` and `smoke.sh` for the line;
4. generates an SPDX JSON SBOM with `syft`, writes `.sha256` with `sha256sum`,
   and uploads the tarball, checksum, and SBOM;
5. a dependent `php-catalog` job downloads all four artifacts, verifies each
   checksum, runs `go run ./cmd/phpcatalog` per artifact, combines the entries,
   and uploads `php-catalog.json`.

The catalog job needs a real HTTPS release host:

- `workflow_dispatch` requires the `download_base_url` input.
- Push and pull-request runs use the repository variable
  `PHP_DOWNLOAD_BASE_URL`.
- If no base URL is configured for a push or pull request, the validate job
  logs that the catalog step is skipped; the build still runs, but no dead
  entries are generated.
- An empty dispatch input or a non-HTTPS value fails validation.

There is no default or fabricated release URL. The workflow does not commit or
push catalog changes and does not create a release; the release process must
publish the artifacts at the same HTTPS base URL before merging the entries into
the shipped catalog. `make php-catalog` likewise requires an explicit
`PHP_BASE_URL`:

```sh
make php-catalog PHP_BASE_URL='https://release-host.invalid/php'
```

## Local verification

This repository's development environment has no PHP build dependencies and no
time for a four-line source build. Local verification is limited to:

```sh
bash -n build/php/*.sh
shellcheck --severity=error build/php/*.sh
go test ./cmd/phpcatalog ./cmd/php-smoke
go test ./...
go vet ./cmd/phpcatalog ./cmd/php-smoke
```

Those commands passed when this pipeline was added. `build.sh` will stop with a
clear missing-tool message on a machine without `patchelf` or the development
headers; that is expected. The real source build, extension load checks, FPM
smoke request, SBOM, and checksum are performed by CI on `ubuntu-22.04`, not by
the local development environment.
