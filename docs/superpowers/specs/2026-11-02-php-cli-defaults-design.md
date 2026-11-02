# PHP CLI default shares the per-line PHP profile

## Intent

The dashboard's PHP version defaults (ini directives and enabled extensions)
must configure the machine-wide CLI PHP as well as sites. Before this change the
CLI read a raw generated `config/cli/php.ini` that ignored the shared profile and
only loaded Composer's extensions.

## Behavior

- The version profile is the single source of truth. For a CLI default build on
  PHP 8.5, `php` uses the same directives and extension toggles as every site on
  PHP 8.5.
- Composer's required extensions (`phar`, `openssl`, `mbstring`) are always
  enabled, even when the profile disables them.
- A site can still override directives and extensions individually. Site
  overrides never mutate the line profile or the CLI ini.
- Advanced CLI-only directives are stored separately and rendered after the line
  defaults, so they can override the shared profile without affecting sites.
- Changing the CLI default build switches the CLI to that build's PHP line
  profile. Changing a line profile regenerates the CLI ini when that line is the
  current CLI default.

## Composition

The generated `config/cli/php.ini` is:

1. build base `php.ini`,
2. shared PHP line defaults,
3. CLI-only raw directives,
4. managed extension block (`extension_dir` plus resolved extensions).

## Migration

On first read after upgrade, an existing global ini is inspected. If it has the
generated site markers, the raw section is preserved as CLI-only directives;
otherwise the whole file is preserved as raw directives. The file is then
regenerated from the current line defaults. No user edits are discarded.

## Non-goals

- Global settings across different PHP lines. Extension availability and
  defaults differ per line, so profiles stay keyed by major.minor.
- Changing the per-site override model. Sites already support Inherit/On/Off
  extension state and directive overrides.
