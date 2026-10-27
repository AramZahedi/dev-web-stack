# Managed WSL distribution

The managed distribution is a Debian 12 (Bookworm) WSL 2 image built from the
Debian snapshot and package set in `versions.lock`. Build the archive on a
Debian 12 x86-64 builder with `debootstrap`, `tar`, `gzip`, and `sha256sum`:

```sh
sudo build/wsl/build-rootfs.sh dist/wsl
```

The build emits `debian-12-wsl-rootfs.tar.gz` and its `.sha256` sidecar. Release
automation publishes both together and records the resulting SHA-256 in the
release manifest consumed by the Windows installer. Provisioning verifies that
digest and available disk space before `wsl.exe --import`; it always imports
with `--version 2` and never adopts an existing unmarked distro.

The imported rootfs enables systemd and disables WSL-generated hosts entries.
The agent owns `/etc/hosts` updates. The marker
`/data/devstack/managed.json` is written only after import and is required on
every reuse or unregister operation. The distro is imported into
`C:\devstack\wsl\devstack`; it does not change the user's default WSL distro.

Never copy a running `ext4.vhdx` as a backup. Shut WSL down and use `wsl
--export` for a consistent archive. Unregistering the managed distro requires
an explicit confirmation and should be preceded by the documented export or
project-discard flow.

`install-docker.sh` installs the exact Engine/Compose versions in
`versions.lock`, verifies Docker's repository key fingerprint, installs the
fixed daemon configuration, and enables the systemd service. The daemon uses
`/data/devstack/docker` on WSL ext4 and does not expose a TCP API. The agent
reports readiness only when systemd's Docker service is active, Engine reports
that data-root, and the Compose v2 plugin is present.
