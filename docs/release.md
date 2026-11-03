# Release and packaging

This document describes how devstack releases are versioned, built, verified,
and published. The current release is **`v0.4.0`**.

> devstack is a local development tool. Publishing an artifact does not make it
> suitable for public or internet-facing hosting.

## Scope and status

| Platform | Artifact | Builder |
|---|---|---|
| Linux x86_64 | `devstack_<version>_linux_amd64.tar.gz` | `build/release.sh` or the manual GoReleaser workflow |
| Windows x64 | `devstack_<version>_windows_amd64.zip` | `build/release-windows.sh` |
| Windows x64 | `devstack_<version>_windows_amd64.tar.gz` | `build/release-windows.sh` |
| Windows x64 | `devstack-installer.exe`, `devstack-setup.exe` | `build/release-windows.sh` |
| All | `checksums.txt` | release builder / publication step |

- **Unsigned:** all archives and Windows installers are unsigned. SHA-256
  checksums verify transport integrity, not publisher identity. Authenticode
  signing and signed packages remain future work.
- **Local-only:** do not publish devstack as a hosting platform or expose its
  services to the internet.
- **No auto-update:** installing a newer release is an explicit installer run.
- **No diagnostics bundle:** `devstack doctor --json` is the support surface.
- **Unsupported:** macOS, Windows on ARM, and Linux arm64 have no release
  artifact.

## Version and tag format

Release tags use:

```text
v<major>.<minor>.<patch>
```

An optional prerelease suffix is accepted (`v1.2.3-rc.1`). The version embedded
in binaries and archive names drops the leading `v`, so tag `v0.4.0` produces
`devstack_0.4.0_linux_amd64.tar.gz`.

Check the embedded identity:

```sh
devstack version
devstack version --json
```

Release builds report the tag version and short commit. Local development builds
may report `dev`.

## Linux archive

```text
devstack_<version>_linux_amd64.tar.gz
  devstack
  devstack-helper
  install.sh
  README.md
  LICENSE
checksums.txt
```

`checksums.txt` names archive basenames so the installer can verify a download
from the same directory. A mismatch aborts before extraction or any privileged
command.

Build:

```sh
bash build/release.sh \
  --version 0.4.0 \
  --skip-ui \
  --output dist/release \
  --build-dir dist/build-linux
```

Omit `--skip-ui` to rebuild the dashboard first. Tar member mtimes are pinned to
`SOURCE_DATE_EPOCH` (default `0`) and gzip runs with `-n`, so two serial builds
of the same source and version are byte-identical.

## Windows archive and installer

```text
devstack_<version>_windows_amd64.zip
devstack_<version>_windows_amd64.tar.gz
  devstack.exe
  devstack-helper.exe
  devstack-agent
  debian-12-wsl-rootfs.tar.gz
  debian-12-wsl-rootfs.tar.gz.sha256
  install.ps1
  uninstall.ps1
  README.md
  LICENSE
  checksums.txt
  devstack.spdx.json
  build/wsl/agent.service
  build/wsl/install-agent.sh
  build/wsl/install-docker.sh
  build/wsl/docker-daemon.json
  devstack-installer.exe / devstack-setup.exe   (release payload)
```

Build from a Linux/macOS shell with Go 1.26+, Node 22, and the verified WSL
rootfs:

```sh
bash build/release-windows.sh \
  --version 0.4.0 \
  --skip-ui \
  --rootfs build/wsl/debian-12-wsl-rootfs.tar.gz \
  --output dist/release \
  --build-dir dist/build-win
```

The Windows package requires the rootfs. The script fails closed if the supplied
rootfs is missing. Use `--mock-rootfs` only for contract tests, never for a
published artifact.

The standalone installer embeds the Windows binaries, helper, Linux agent, WSL
rootfs, and installer payload so it needs no external downloads.

## Build both platforms and publish

Typical release sequence:

```sh
# 1. Clean the output.
rm -rf dist/release
mkdir -p dist/release

# 2. Build both platforms.
bash build/release.sh \
  --version 0.4.0 \
  --skip-ui \
  --output dist/release \
  --build-dir dist/build-linux

bash build/release-windows.sh \
  --version 0.4.0 \
  --skip-ui \
  --rootfs build/wsl/debian-12-wsl-rootfs.tar.gz \
  --output dist/release \
  --build-dir dist/build-win

# 3. Generate one combined checksum manifest.
cd dist/release
sha256sum \
  devstack_0.4.0_linux_amd64.tar.gz \
  devstack_0.4.0_windows_amd64.zip \
  devstack_0.4.0_windows_amd64.tar.gz \
  devstack-installer.exe \
  devstack-setup.exe > checksums.txt
sha256sum -c checksums.txt
```

Why combine manually? `release.sh` and `release-windows.sh` each write a
`checksums.txt` for their own artifacts. A combined release should publish one
manifest covering every downloadable asset.

## Release contract tests

Run serially when validating a release:

```sh
bash test/release/release_test.sh
bash test/release/release_windows_test.sh
```

The Linux test checks archive members, version identity, installer executable
bits, deterministic rebuilds, default output behavior, and that developer
`dist/devstack*` files are untouched. The Windows test checks the archive member
set, PE/ELF headers, SPDX SBOM validity, rootfs assets, installer references,
internal checksums, and corruption detection. It uses `--mock-rootfs` for
speed and does not replace a real-rootfs release build.

Do not run the two contract tests in parallel on a machine that is also building
the real release; concurrent Go builds can make the byte-identical rebuild check
appear nondeterministic.

## GitHub release publication

The repository workflows are manual-dispatch only. The Linux GoReleaser workflow
does not publish Windows assets. Publish both platforms with an explicit GitHub
release.

Example with GitHub CLI:

```sh
gh release create v0.4.0 \
  dist/release/devstack_0.4.0_linux_amd64.tar.gz \
  dist/release/devstack_0.4.0_windows_amd64.zip \
  dist/release/devstack_0.4.0_windows_amd64.tar.gz \
  dist/release/devstack-installer.exe \
  dist/release/devstack-setup.exe \
  dist/release/checksums.txt \
  --title "devstack 0.4.0" \
  --notes-file docs/releases/v0.4.0.md
```

Useful validation:

```sh
gh release view v0.4.0
gh release download v0.4.0 --dir /tmp/devstack-release-check
```

`v0.4.0` should publish to:

<https://github.com/AramZahedi/dev-web-stack/releases/tag/v0.4.0>

## Manual GitHub workflows

- `.github/workflows/ci.yml` is `workflow_dispatch` only and runs the Linux test
  matrix, UI checks, Playwright, and optional Windows jobs when dispatched.
- `.github/workflows/release.yml` is `workflow_dispatch` only, validates the
  tag, runs release contract tests, and publishes the Linux archive with
  GoReleaser. It does not build Windows or WSL assets.
- `.github/workflows/php.yml` is `workflow_dispatch` only and builds PHP
  artifacts for the separate runtime catalog.

Automatic CI is intentionally disabled. Run the local gates before tagging:

```sh
go test ./...
go vet ./...
go test ./... -race

cd ui
npm run check
npm run test -- --run
npm run build
cd ..

pwsh -NoProfile -File test/windows/installer_test.ps1
pwsh -NoProfile -File test/windows/lifecycle_test.ps1
pwsh -NoProfile -File test/windows/vertical_slice.ps1
pwsh -NoProfile -File test/windows/serving_e2e.ps1
pwsh -NoProfile -File test/windows/performance_e2e.ps1
```

## Install and upgrade from a release

Linux:

```sh
./install.sh --release 0.4.0
devstack init

./install.sh --upgrade --release 0.4.0
devstack doctor
```

Windows:

```powershell
.\devstack-installer.exe --upgrade
# or
.\install.ps1 -Upgrade
```

Windows upgrades back up binaries and `/data/devstack` and roll back on failure.
Linux upgrades take a SQLite snapshot before the first pending migration.
Downgrades are unsupported.

## PHP catalog gate

The production PHP catalog is embedded in the application release. Publishing a
new PHP build requires a new app release containing the updated catalog entry.
The PHP workflow builds artifacts and a catalog JSON; merging is a separate
gated step documented in [PHP](php.md) and implemented by
`cmd/catalogmerge`. A tag cannot add catalog entries retroactively, so merge and
commit the catalog before tagging.

## Release checklist

1. Confirm the working tree is clean and `go.mod`/UI lockfiles are current.
2. Run all local quality gates, including UI, installer, lifecycle, vertical
   slice, serving E2E, and performance E2E.
3. Run Linux and Windows release contract tests **serially**.
4. Update `README.md`, `docs/install.md`, `docs/install-windows.md`,
   `docs/upgrade.md`, `docs/release.md`, `docs/known-issues.md`, and the release
   notes with the new version and current limitations.
5. Build the Linux and Windows artifacts from the final commit.
6. Generate and verify the combined `checksums.txt`.
7. Tag the final commit with `v<version>` and push the tag.
8. Create the GitHub release with both archives, both Windows installers, and
   `checksums.txt`; attach the release notes.
9. Install the published Linux archive on a clean supported host and the Windows
   installer on a clean supported Windows host; run `devstack doctor`.
10. Do not describe the release as production-ready or internet-facing.

## Signing

Signing is not implemented. Do not imply that a checksum proves publisher
identity. If Authenticode signing is added later:

- sign `devstack.exe`, `devstack-helper.exe`, and the standalone installers,
- timestamp the signatures,
- document the certificate and verification command,
- keep the SHA-256 manifest for transport verification.

Until then, download only from the official GitHub release and verify checksums.
