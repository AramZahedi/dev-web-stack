# Upgrade devstack

This guide covers forward upgrades of a Linux install. Upgrades replace the
`devstack` and `devstack-helper` binaries, keep the host integration, and back
up device state before the store applies a schema migration. Downgrades are not
supported.

M1 release archives are unsigned and Linux x86_64 only. The version numbers
below (`0.2.0`) are placeholders; no release tag has been published from this
branch yet. There is no automatic update check or self-update, and M1 does not
create a diagnostics bundle: use `devstack doctor --json` plus service logs for
support.

## Preflight

1. Read the release notes and note the current version:

   ```sh
   devstack version
   devstack version --json
   devstack doctor
   ```

2. Confirm the daemon and helper are healthy so a failed upgrade is not
   confused with an existing problem:

   ```sh
   systemctl --user status devstack-daemon --no-pager
   sudo systemctl status devstack-helper --no-pager
   ```

3. Make sure `~/.local/bin` is on `PATH`. `--upgrade` requires an existing
   executable at `~/.local/bin/devstack`; it refuses to guess that this is a
   fresh install.
4. Keep the previous release archive and `checksums.txt`. They are useful for
   diagnosis even though schema downgrades are unsupported.
5. Free space for the archive plus a copy of `~/.devstack/state.db`. The store
   refuses to migrate if it cannot write the pre-migration backup.

The installer must run as your normal user, never as root.

## Upgrade commands

Use the same source mode as the original install.

Published release (downloads over HTTPS):

```sh
./install.sh --upgrade --release 0.2.0
```

Downloaded archive:

```sh
./install.sh --upgrade \
  --archive devstack_0.2.0_linux_amd64.tar.gz \
  --checksums checksums.txt
```

Source checkout:

```sh
make build
./install.sh --upgrade
```

A custom HTTPS mirror is supported with `--base-url https://...`. Plain HTTP is
rejected. `--archive` and `--release` are mutually exclusive, and `--archive`
requires `--checksums`.

Preview the complete plan without writing files or changing services:

```sh
./install.sh --upgrade --dry-run --release 0.2.0
```

The dry run still downloads, verifies the checksum, checks archive members,
extracts the archive, and checks `devstack version --json`; it only prints the
stop/install/restart commands.

## What the installer does

1. Verifies the archive checksum and identity. An archive with an absolute path,
   `..` component, symlink, hardlink, unexpected member, or wrong packaged
   version is rejected before any privileged command.
2. Stops the user daemon before replacing binaries. If the user unit is active
   it uses `systemctl --user stop devstack-daemon.service`; otherwise it uses
   the old `devstack daemon stop`. If the stop fails, the upgrade aborts and the
   binaries are left untouched.
3. Installs the new CLI and helper, and restarts the helper. The existing
   `/etc/devstack/helper.yaml` is kept. Use `--force-config` only when you
   intend to replace it with generated defaults.
4. Reinstalls both units and restarts the user daemon unit when the session has
   a user systemd manager. Headless installs use the detached fallback on the
   next `devstack init`.
5. Prints the backup location and the required `devstack init` follow-up. It
   does not run `devstack init`, because that command changes privileged hosts,
   forwarder, and trust state.

## Automatic database backup

The store opens `${DEVSTACK_HOME:-~/.devstack}/state.db` with a single
connection and runs its embedded migrations. Before applying pending migrations
to an existing database, it creates a consistent snapshot with SQLite's
`VACUUM INTO`:

```text
${DEVSTACK_HOME:-~/.devstack}/backups/state.db.pre-migrate-<UTC timestamp>.bak
```

- The backup is taken before the first pending migration, not after a failed
  migration.
- At most three backups are kept. The three newest files are retained and older
  ones are removed.
- The backup file is user-only (`0600`).
- A fresh database with no applied migrations skips the backup because there is
  no state to preserve.
- If the backup cannot be created, or the backups directory cannot be rotated
  back to three files, the store returns an error and applies no migrations.
  Fix the filesystem problem (permissions, free space) and retry; do not delete
  or bypass the backup directory.

The installer prints the backup path in its summary when an existing database
will be backed up. The exact filename is generated on the next daemon start.

## Required follow-up: devstack init

After the installer finishes, run:

```sh
devstack init
```

`init` re-syncs the pieces that live outside the store:

- the helper CA and operating-system/browser trust;
- the managed `/etc/hosts` block;
- the privileged 80/443 forwarders, or the external vhost integration;
- daemon startup through the user unit (or the detached fallback);
- the helper protocol and diagnostic checks.

Do not skip this step after an upgrade. The installer deliberately does not
perform these privileged changes.

Then verify the result:

```sh
devstack doctor
devstack version --json
systemctl --user status devstack-daemon --no-pager
sudo systemctl status devstack-helper --no-pager
```

`devstack doctor --json` is the support snapshot to attach to a bug report.

## Protocol mismatch

The CLI and helper exchange a protocol version. If an older helper is still
running, commands fail with the existing upgrade instruction instead of
guessing. Re-run the installer for the same release, then run `devstack init`:

```sh
./install.sh --upgrade --release 0.2.0
devstack init
```

Check both services if the mismatch persists:

```sh
systemctl --user status devstack-daemon --no-pager
sudo systemctl status devstack-helper --no-pager
sudo journalctl -u devstack-helper -n 100 --no-pager
```

## Rollback and downgrades

Downgrades are unsupported. Migrations are forward-only, and an older binary
must not be pointed at a schema it does not understand.

If the upgrade fails before migrations run, you can reinstall the previous
release's binaries with the same forward installer path and rerun
`devstack init`. If the database has already migrated, keep the pre-migration
backup and restore it only as a deliberate recovery operation with the daemon
stopped; this is not a supported upgrade path. In both cases, preserve
`~/.devstack` before changing binaries.

## Troubleshooting

**The upgrade aborts because the daemon cannot be stopped.** Inspect the unit or
the detached process, stop it manually, and rerun the installer:

```sh
systemctl --user status devstack-daemon --no-pager
devstack daemon status
```

**The upgrade aborts before installing.** The checksum, archive member, or
packaged version check failed. Download the release archive and `checksums.txt`
again from the same release. Do not edit the checksum file.

**The store reports a pre-migration backup failure.** Check free space and
ownership of `~/.devstack` and `~/.devstack/backups`, then retry. The migration
has not run, so the old schema is still intact.

**The daemon does not start after the upgrade.** Run `devstack doctor`, inspect
the user unit logs, and rerun `devstack init`:

```sh
journalctl --user -u devstack-daemon -n 100 --no-pager
```
