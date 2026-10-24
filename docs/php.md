# PHP

devstack runs PHP sites on per-site `php-fpm` masters and serves them through
FastCGI. Each site can pin a PHP major.minor line and an exact build, keep its
own `php.ini`, and toggle shared extensions.

> **Artifact gate:** the production catalog shipped in `devstack` currently has
> no PHP builds. `devstack runtime list` shows Node builds only, and PHP site
> creation or `devstack runtime install` will report that no PHP build is
> available until the release pipeline publishes checksummed
> `php-<version>-r<revision>-linux-x64.tar.gz` archives and the release job
> merges their catalog entries. The build pipeline in this repository produces
> those artifacts in CI; it does not publish them automatically. Do not expect
> PHP installation to work from a release build yet.

## Install a PHP build (after the artifact gate opens)

A PHP site needs an installed build on its line. Once catalog entries are
published, install the exact build:

```sh
devstack runtime list
devstack runtime install php@8.4.27-r1
```

Use `--yes` to skip the confirmation prompt. The release catalog is embedded in
the binary, so a new release is required before a newly published build becomes
available to `devstack runtime install`. Builds are linux-x64 and are pinned by
SHA-256 and size; a modified or truncated download is rejected.

Contributors can build an artifact locally with the pipeline; see
[build/php/README.md](../build/php/README.md). A locally built archive is for
pipeline verification and is not registered in the shipped catalog.

## Create a PHP site

```sh
mkdir -p "$HOME/projects/app/public"
cat >"$HOME/projects/app/public/index.php" <<'PHP'
<?php
header('Content-Type: text/plain');
echo "Hello from PHP ", PHP_VERSION, "\n";
PHP

devstack site add \
  --name app \
  --kind php \
  --path "$HOME/projects/app" \
  --document-root public \
  --php 8.4 \
  --front-controller \
  --yes
```

`--php` is required and accepts a line such as `8.2`, `8.3`, `8.4`, or `8.5`.
Use `--php-build php@8.4.27-r1` to pin an exact build. Without it, devstack uses
the newest installed build on the line and offers to install one from the
catalog when needed. `--front-controller` routes paths that do not match a file
to the site's `index.php`.

The new site gets a PHP master on a loopback port and is started and health
checked before `site add` returns.

## Commands

| Command | Purpose |
|---|---|
| `devstack site add --kind php --php <line> ...` | Create a PHP site |
| `devstack site php <name>` | Show the site's PHP line and build |
| `devstack site php <name> <line>` | Switch the line, using the newest installed build |
| `devstack site php <name> --build <id>` | Switch to an exact installed build |
| `devstack site php <name> --list` | List installed and catalog builds for the line |
| `devstack site ini <name>` | Print the generated `php.ini` and the site overrides |
| `devstack site ext <name>` | Show effective, default, and overridden extensions |
| `devstack php --site <name> -- <args...>` | Run the site's PHP CLI with its generated ini |
| `devstack runtime migrate <from> --to <to>` | Move every site on a line to a new line or build |
| `devstack runtime remove <id> --yes` | Remove an installed build that no site uses |

## Switch PHP version or build

```sh
devstack site php app 8.5
devstack site php app --build php@8.5.12-r1
devstack site php app --list
```

A running site is switched with validation and rollback: devstack writes a new
`php.ini` and `php-fpm.conf`, starts a staged master on a new loopback port,
checks readiness, and only then swaps traffic. If composing, linting, FPM
validation, or readiness fails, the previous master keeps serving and the error
is reported. A stopped site records the new configuration and uses it on the
next start.

The build must already be installed and must belong to the requested line.
`--build` and the line argument are cross-checked; a build from another line is
rejected with `INVALID_FLAGS`.

## Edit php.ini

Show the effective configuration:

```sh
devstack site ini app
```

The generated file is composed in this order:

1. the build's base `php.ini` (`[PHP]` only in devstack builds),
2. the editable defaults for the site's PHP line,
3. site rows and the site's raw section,
4. a managed extension block (read-only from the site editor).

Structured overrides and the raw section are both persisted. The raw section is
authoritative for comments and directives that devstack does not model. Set,
remove, or replace them with:

```sh
devstack site ini app --set memory_limit=512M --set display_errors=Off
devstack site ini app --unset memory_limit
devstack site ini app --raw 'max_input_vars = 5000'
devstack site ini app --raw-file ./php-extra.ini
devstack site ini app --apply
```

`--set` and `--unset` merge into the current rows; `--raw` and `--raw-file`
replace the whole raw section. `--apply` re-applies the current overrides without
changing them. A running site validates and swaps a new FPM generation; a stopped
site applies the configuration on its next start.

Directive names are restricted to letters, digits, dots, dashes, and
underscores. Values cannot contain newlines, and the devstack section markers are
reserved. Invalid input is rejected before any file is written.

## Extensions

```sh
devstack site ext app
devstack site ext app --enable xdebug --disable redis --inherit apcu --yes
```

Each extension is one of three states per site:

- **Inherit** (default): follow the line's defaults.
- **On**: force the extension on for this site.
- **Off**: force it off for this site.

Standard extensions are on by default except `ffi`; extras are off by default.
`opcache` is a standard Zend extension and is on by default; `xdebug` is an extra
Zend extension and is off by default. Turning on `redis` or `apcu` automatically
includes `igbinary`, and the managed block writes dependencies before their
dependents.

On PHP 8.5, OPcache is built into the PHP binary and upstream no longer produces
`opcache.so`. The build pipeline omits the shared module on that line. The
current extension manifest still lists `opcache` for 8.5 with its default on, so
a generated 8.5 ini may log a startup warning about the missing shared object
even though OPcache is active. Use the `opcache.*` ini rows to control it; 8.2,
8.3, and 8.4 artifacts include `opcache.so`. Making the extension toggle
version-aware is a catalog follow-up.

`imap` is listed in the extension manifest for PHP 8.2/8.3 only, but it is not
included in the current build pipeline because the upstream PECL release needs
PHP 8.3+ and the 8.2 bundled copy requires the UW-IMAP c-client library. If it
is enabled on those lines, the generated ini will name a missing `imap.so` and
`devstack php --site <name> -- -m` will report the load failure. See
[build/php/README.md](../build/php/README.md) for the blocked-entry record.

## Run PHP from a site

```sh
devstack php --site app -- -v
devstack php --site app -- -r 'echo PHP_VERSION, "\n";'
```

Only arguments after `--` are forwarded. The CLI runs the site build's
`bin/php` with the generated site `php.ini`. Flags that would bypass that ini
(`-c`, `--php-ini`, `-n`, `--no-php-ini`) are rejected, and the command exits
non-zero when PHP returns a non-zero status.

## Migrate or remove builds

Move every site on one line to another line or exact build:

```sh
devstack runtime migrate 8.4 --to 8.5
devstack runtime migrate 8.4 --to php@8.5.12-r1
```

Sites are migrated independently. A failure on one site is reported and the
command exits non-zero, but the sites that succeeded stay migrated.

Remove an installed build only after no site references it:

```sh
devstack runtime remove php@8.4.27-r1 --yes
```

Removal is blocked with `PHP_VERSION_IN_USE` while a site pins the build.
Switch those sites with `devstack site php`, or use `devstack runtime migrate`,
then remove the build. Removing a runtime never deletes project files.

`devstack site remove <name>` stops the site's master, removes its generated
configuration directory, domain, and certificate, and leaves the project
directory alone.

## FPM ports and readiness

- Each running PHP site gets one php-fpm master listening on
  `127.0.0.1:<port>`, where the port is in **43000-43999**.
- The port is allocated and persisted. A new configuration generation gets its
  own port and directory, which lets devstack stage it while the old master keeps
  serving.
- Readiness is a TCP connect to the listen address with a 30-second deadline.
  `devstack site start` and a live PHP apply fail if the master does not accept
  connections in time.
- The built-in proxy and the generated external vhosts both speak FastCGI to
  that loopback port. Only the master binds it; it is not exposed publicly.
- Generated per-site files live under `~/.devstack/sites/<site-id>/<hash>/`
  (`php.ini`, `php-fpm.conf`, `health.php`, `php-fpm.pid`). The current
  generation is the newest validated hash.

## Logs

```sh
devstack site logs app --tail 200
devstack site logs app --follow
journalctl --user -u devstack-daemon
```

The php-fpm master writes to the site log stream at
`~/.devstack/logs/<site-id>/current.log`. PHP warnings, extension load failures,
and pool startup errors appear there. FPM does not write a separate
`php-fpm.log`; its stderr is the site stream.

## Troubleshooting

### `PHP_CONFIG_INVALID`

The generated `php.ini` or `php-fpm.conf` was rejected, or an extension selection
is invalid. The error usually includes the directive and line.

1. Print the generated file with `devstack site ini <name>`.
2. Fix the reported row, remove a bad raw line with `--raw`, or adjust the
   extension.
3. Re-apply and watch the site log.

Common causes: a syntactically invalid value, a raw `[PATH=...]` section that
changes later directive scope, an extension that does not exist on the line, or
an enabled extension whose `.so` failed to load.

### `PHP_VERSION_IN_USE`

`devstack runtime remove` was asked to delete a build that a site still pins.
Switch or migrate the listed sites first:

```sh
devstack site php app 8.5
devstack runtime migrate 8.4 --to 8.5
devstack runtime remove php@8.4.27-r1 --yes
```

### `READY_TIMEOUT` (readiness timeout)

The FPM master did not accept TCP connections within 30 seconds. Depending on
where the failure is surfaced, the CLI message wraps the supervisor's
`readiness timeout` text rather than printing the code alone.

1. Read `devstack site logs <name>` for a startup error.
2. Check that the configured port is free: `ss -ltnp | grep 43`.
3. Check that every enabled extension loads:
   `devstack php --site <name> -- -m`.
4. Run `devstack doctor`; the PHP checks cover builds, generated config, unique
   ports, and a live master.
5. Fix the cause, then `devstack site restart <name>`.

A master that exits immediately after starting is usually caused by a missing
shared library, an extension compiled against a different PHP API, or an ini
directive that FPM rejects. The artifact bundles non-glibc libraries and uses an
`$ORIGIN`-relative RPATH; the host must still provide glibc compatible with the
Ubuntu 22.04 build baseline (glibc 2.35 or newer).

### No PHP build is available

This is the artifact gate, not a local failure. `devstack runtime list` will
show no PHP entries until the release pipeline publishes artifacts and a release
binary embeds their catalog entries. The build and smoke commands verify
artifacts in CI, but merging `php-catalog.json` into
`internal/catalog/catalog.json` is a release step, not a CI commit.

## Build and release gate

The pipeline is in [build/php/README.md](../build/php/README.md) and
[.github/workflows/php.yml](../.github/workflows/php.yml):

- a matrix on `ubuntu-22.04` builds PHP 8.2, 8.3, 8.4, and 8.5 from the pinned
  sources in `build/php/extensions.lock`;
- `build.sh` builds PHP with FPM and shared standard/PECL extensions, bundles
  dependencies, flattens `lib/php/extensions`, sets relative RPATHs, and creates
  a top-level `php-<version>-r<revision>-linux-x64/` archive;
- `smoke.sh` runs the CLI and FPM binaries, loads every shared extension, starts
  FPM with a temporary config, and asserts one FastCGI health request returns
  `200 ok`;
- CI generates an SBOM and a `.sha256` checksum and uploads the archive;
- a dependent job downloads all four archives, runs `cmd/phpcatalog` once per
  artifact, and uploads the combined `php-catalog.json`.

The workflow never edits `internal/catalog/catalog.json`. Publication and the
catalog merge happen only after the artifacts are uploaded to the release host,
and the production PHP catalog stays empty until then.
