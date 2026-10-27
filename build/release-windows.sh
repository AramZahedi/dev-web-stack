#!/usr/bin/env bash
# Build the Windows amd64 devstack release archive and SHA-256 manifest.
#
# Packages devstack.exe, devstack-helper.exe, devstack-agent (Linux ELF),
# the Debian 12 WSL rootfs archive, install.ps1, uninstall.ps1, README.md,
# LICENSE (when present), internal checksums.txt, and SPDX SBOM as
# devstack_<version>_windows_amd64.zip (and .tar.gz), with outer checksums.txt.
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="github.com/AramZahedi/dev-web-stack"
DEFAULT_OUTPUT="dist/release"

usage() {
	cat <<'EOF'
Usage: build/release-windows.sh --version <semver> [options]

Builds devstack.exe, devstack-helper.exe, and devstack-agent (Linux ELF),
stages the WSL rootfs, install.ps1, uninstall.ps1, SBOM, and checksums,
and packages them into a Windows release archive.

Options:
  --version <semver>   Release version without leading "v" (required).
  --output <dir>       Output directory (default: dist/release).
  --build-dir <dir>    Directory for intermediate binaries (default: dist).
  --rootfs <path>      Path to debian-12-wsl-rootfs.tar.gz (optional).
  --skip-ui            Do not run `make ui`; use existing ui/dist build.
  --mock-rootfs        Generate a mock rootfs if no prebuilt rootfs is found.
  -h, --help           Show this help.
EOF
}

die() {
	printf 'release-windows: error: %s\n' "$*" >&2
	exit 1
}

VERSION=""
OUTPUT_DIR="${DEFAULT_OUTPUT}"
BUILD_DIR="dist"
ROOTFS_PATH=""
SKIP_UI=0
MOCK_ROOTFS=0

while [ "$#" -gt 0 ]; do
	case "$1" in
	--version)
		[ "$#" -ge 2 ] || die "--version requires a value"
		VERSION="$2"
		shift 2
		;;
	--output)
		[ "$#" -ge 2 ] || die "--output requires a value"
		OUTPUT_DIR="$2"
		shift 2
		;;
	--build-dir)
		[ "$#" -ge 2 ] || die "--build-dir requires a value"
		BUILD_DIR="$2"
		shift 2
		;;
	--rootfs)
		[ "$#" -ge 2 ] || die "--rootfs requires a value"
		ROOTFS_PATH="$2"
		shift 2
		;;
	--skip-ui)
		SKIP_UI=1
		shift
		;;
	--mock-rootfs)
		MOCK_ROOTFS=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		die "unknown argument: $1"
		;;
	esac
done

[ -n "${VERSION}" ] || die "--version is required (for example: --version 0.1.0)"

VERSION="${VERSION#v}"
if [[ ! "${VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
	die "invalid version ${VERSION@Q}: expected <major>.<minor>.<patch> with an optional prerelease suffix"
fi

COMMIT="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || true)"
COMMIT="${COMMIT:-unknown}"

case "${OUTPUT_DIR}" in
/* | [A-Za-z]:/* | [A-Za-z]:\\*) ;;
*) OUTPUT_DIR="${REPO_ROOT}/${OUTPUT_DIR}" ;;
esac
mkdir -p "${OUTPUT_DIR}"
OUTPUT_DIR="$(cd -- "${OUTPUT_DIR}" && pwd)"

[ -n "${BUILD_DIR}" ] || die "--build-dir must not be empty"
case "${BUILD_DIR}" in
/* | [A-Za-z]:/* | [A-Za-z]:\\*) ;;
*) BUILD_DIR="${REPO_ROOT}/${BUILD_DIR}" ;;
esac
mkdir -p "${BUILD_DIR}"
BUILD_DIR="$(cd -- "${BUILD_DIR}" && pwd)"

STAGE="$(mktemp -d "${TMPDIR:-/tmp}/devstack-win-release-stage.XXXXXX")"
cleanup() {
	rm -rf "${STAGE}"
	if [ -n "${INSTALLER_STAGE:-}" ]; then
		rm -rf "${INSTALLER_STAGE}"
	fi
	TRACKED_PAYLOAD="${REPO_ROOT}/cmd/devstack-installer/payload.zip"
	if [ -f "${TRACKED_PAYLOAD}" ]; then
		tracked_size=$(wc -c <"${TRACKED_PAYLOAD}" | tr -d ' ')
		if [ "${tracked_size}" -ne 22 ]; then
			printf '\x50\x4b\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00' >"${TRACKED_PAYLOAD}"
		fi
	fi
}
trap cleanup EXIT

if [ "${SKIP_UI}" -eq 0 ]; then
	printf 'release-windows: building UI\n'
	make -C "${REPO_ROOT}" ui
else
	printf 'release-windows: skipping UI build (--skip-ui)\n'
fi

for required in install.ps1 uninstall.ps1 README.md; do
	[ -f "${REPO_ROOT}/${required}" ] || die "missing required release file ${REPO_ROOT}/${required}"
done

LDFLAGS=(
	-s -w
	-X "${MODULE}/internal/version.Version=${VERSION}"
	-X "${MODULE}/internal/version.Commit=${COMMIT}"
)

# Build devstack.exe
printf 'release-windows: building devstack.exe\n'
(
	cd "${REPO_ROOT}"
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags "${LDFLAGS[*]}" -o "${BUILD_DIR}/devstack.exe" ./cmd/devstack
)

# Build devstack-helper.exe
printf 'release-windows: building devstack-helper.exe\n'
(
	cd "${REPO_ROOT}"
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags "${LDFLAGS[*]}" -o "${BUILD_DIR}/devstack-helper.exe" ./cmd/devstack-helper
)

# Build devstack-agent (Linux ELF for WSL)
printf 'release-windows: building devstack-agent (linux/amd64)\n'
(
	cd "${REPO_ROOT}"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -trimpath -ldflags "${LDFLAGS[*]}" -o "${BUILD_DIR}/devstack-agent" ./cmd/devstack-agent
)

# Stage executables
cp "${BUILD_DIR}/devstack.exe" "${STAGE}/devstack.exe"
chmod 0755 "${STAGE}/devstack.exe"
cp "${BUILD_DIR}/devstack-helper.exe" "${STAGE}/devstack-helper.exe"
chmod 0755 "${STAGE}/devstack-helper.exe"
cp "${BUILD_DIR}/devstack-agent" "${STAGE}/devstack-agent"
chmod 0755 "${STAGE}/devstack-agent"

# Verify binary signatures
# devstack.exe and devstack-helper.exe must start with MZ
for winBin in devstack.exe devstack-helper.exe; do
	magic=$(od -N 2 -A n -c "${STAGE}/${winBin}" 2>/dev/null | tr -d ' \n' || head -c 2 "${STAGE}/${winBin}")
	case "${magic}" in
	*MZ*) ;;
	*) die "binary ${winBin} does not have PE magic header 'MZ': got ${magic}" ;;
	esac
done

# devstack-agent must be a Linux ELF (magic \x7fELF)
agent_magic=$(od -N 4 -t x1 "${STAGE}/devstack-agent" 2>/dev/null | head -1 | tr -d ' ' || true)
case "${agent_magic}" in
*7f454c46* | *7f*45*4c*46*) ;;
*)
	# Fallback string check
	if ! head -c 4 "${STAGE}/devstack-agent" | grep -q "ELF"; then
		die "devstack-agent is not an ELF binary"
	fi
	;;
esac

# Stage scripts and docs
cp "${REPO_ROOT}/install.ps1" "${STAGE}/install.ps1"
chmod 0755 "${STAGE}/install.ps1"
cp "${REPO_ROOT}/uninstall.ps1" "${STAGE}/uninstall.ps1"
chmod 0755 "${STAGE}/uninstall.ps1"
if [ -f "${REPO_ROOT}/build/windows/devstack-daemon-task.ps1" ]; then
	cp "${REPO_ROOT}/build/windows/devstack-daemon-task.ps1" "${STAGE}/devstack-daemon-task.ps1"
	chmod 0644 "${STAGE}/devstack-daemon-task.ps1"
fi
if [ -f "${REPO_ROOT}/build/windows/devstack-daemon.vbs" ]; then
	cp "${REPO_ROOT}/build/windows/devstack-daemon.vbs" "${STAGE}/devstack-daemon.vbs"
	chmod 0644 "${STAGE}/devstack-daemon.vbs"
fi
cp "${REPO_ROOT}/README.md" "${STAGE}/README.md"
chmod 0644 "${STAGE}/README.md"
if [ -f "${REPO_ROOT}/LICENSE" ]; then
	cp "${REPO_ROOT}/LICENSE" "${STAGE}/LICENSE"
	chmod 0644 "${STAGE}/LICENSE"
fi

# Acquire or generate WSL rootfs
TARGET_ROOTFS="${STAGE}/debian-12-wsl-rootfs.tar.gz"
ROOTFS_MIN_BYTES=1073741824
if [ -f "${REPO_ROOT}/build/wsl/versions.lock" ]; then
	parsed_min=$(grep -E '^rootfs_minimum_bytes=' "${REPO_ROOT}/build/wsl/versions.lock" | cut -d'=' -f2 | tr -d ' \r\n')
	if [[ "${parsed_min}" =~ ^[0-9]+$ ]]; then
		ROOTFS_MIN_BYTES="${parsed_min}"
	fi
fi

if [ -n "${ROOTFS_PATH}" ]; then
	[ -f "${ROOTFS_PATH}" ] || die "specified --rootfs ${ROOTFS_PATH} not found"
	cp "${ROOTFS_PATH}" "${TARGET_ROOTFS}"
elif [ -f "${REPO_ROOT}/build/wsl/debian-12-wsl-rootfs.tar.gz" ]; then
	cp "${REPO_ROOT}/build/wsl/debian-12-wsl-rootfs.tar.gz" "${TARGET_ROOTFS}"
elif [ -f "${REPO_ROOT}/dist/wsl/debian-12-wsl-rootfs.tar.gz" ]; then
	cp "${REPO_ROOT}/dist/wsl/debian-12-wsl-rootfs.tar.gz" "${TARGET_ROOTFS}"
elif [ "${MOCK_ROOTFS}" -eq 1 ]; then
	# Create minimal valid rootfs archive
	printf 'release-windows: creating minimal WSL rootfs archive (--mock-rootfs)\n'
	mock_rootfs_dir="$(mktemp -d)"
	mkdir -p "${mock_rootfs_dir}/etc" "${mock_rootfs_dir}/data/devstack"
	cat >"${mock_rootfs_dir}/etc/wsl.conf" <<'EOF'
[boot]
systemd=true
[network]
generateHosts=false
EOF
	tar -czf "${TARGET_ROOTFS}" -C "${mock_rootfs_dir}" .
	rm -rf "${mock_rootfs_dir}"
else
	die "WSL rootfs archive not found. Expected build/wsl/debian-12-wsl-rootfs.tar.gz or --rootfs <path>. Real release builds require a real rootfs archive (>= 1GB). To create a mock rootfs for development/testing, pass --mock-rootfs."
fi

if [ "${MOCK_ROOTFS}" -eq 0 ]; then
	rootfs_size=$(wc -c <"${TARGET_ROOTFS}" | tr -d ' ')
	if [ "${rootfs_size}" -lt "${ROOTFS_MIN_BYTES}" ]; then
		die "WSL rootfs archive size (${rootfs_size} bytes) is below minimum required ${ROOTFS_MIN_BYTES} bytes (1GB). Use --mock-rootfs for testing."
	fi
fi

# Generate rootfs SHA256 sidecar
(
	cd "${STAGE}"
	sha256sum "debian-12-wsl-rootfs.tar.gz" >"debian-12-wsl-rootfs.tar.gz.sha256"
)

# Generate SBOM (devstack.spdx.json)
printf 'release-windows: generating SBOM\n'
SBOM_FILE="${STAGE}/devstack.spdx.json"
if command -v syft >/dev/null 2>&1; then
	syft "dir:${STAGE}" -o spdx-json >"${SBOM_FILE}"
else
	# Generate compliant SPDX 2.3 JSON document
	CREATED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || echo "2026-10-27T00:00:00Z")"
	DEVSTACK_SHA="$(sha256sum "${STAGE}/devstack.exe" | cut -d' ' -f1)"
	HELPER_SHA="$(sha256sum "${STAGE}/devstack-helper.exe" | cut -d' ' -f1)"
	AGENT_SHA="$(sha256sum "${STAGE}/devstack-agent" | cut -d' ' -f1)"
	ROOTFS_SHA="$(sha256sum "${TARGET_ROOTFS}" | cut -d' ' -f1)"
	INSTALL_SHA="$(sha256sum "${STAGE}/install.ps1" | cut -d' ' -f1)"
	UNINSTALL_SHA="$(sha256sum "${STAGE}/uninstall.ps1" | cut -d' ' -f1)"

	cat >"${SBOM_FILE}" <<EOF
{
  "spdxVersion": "SPDX-2.3",
  "dataLicense": "CC0-1.0",
  "SPDXID": "SPDXRef-DOCUMENT",
  "name": "devstack-windows-amd64-${VERSION}",
  "documentNamespace": "https://github.com/AramZahedi/dev-web-stack/spdx/devstack-${VERSION}-windows-amd64",
  "creationInfo": {
    "creators": [
      "Tool: devstack-release-windows-${VERSION}",
      "Organization: devstack contributors"
    ],
    "created": "${CREATED_AT}"
  },
  "packages": [
    {
      "SPDXID": "SPDXRef-Package-devstack-exe",
      "name": "devstack.exe",
      "versionInfo": "${VERSION}",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [
        {
          "algorithm": "SHA256",
          "checksumValue": "${DEVSTACK_SHA}"
        }
      ]
    },
    {
      "SPDXID": "SPDXRef-Package-devstack-helper-exe",
      "name": "devstack-helper.exe",
      "versionInfo": "${VERSION}",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [
        {
          "algorithm": "SHA256",
          "checksumValue": "${HELPER_SHA}"
        }
      ]
    },
    {
      "SPDXID": "SPDXRef-Package-devstack-agent",
      "name": "devstack-agent",
      "versionInfo": "${VERSION}",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [
        {
          "algorithm": "SHA256",
          "checksumValue": "${AGENT_SHA}"
        }
      ]
    },
    {
      "SPDXID": "SPDXRef-Package-wsl-rootfs",
      "name": "debian-12-wsl-rootfs.tar.gz",
      "versionInfo": "bookworm-snapshot",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "NOASSERTION",
      "checksums": [
        {
          "algorithm": "SHA256",
          "checksumValue": "${ROOTFS_SHA}"
        }
      ]
    },
    {
      "SPDXID": "SPDXRef-Package-install-ps1",
      "name": "install.ps1",
      "versionInfo": "${VERSION}",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [
        {
          "algorithm": "SHA256",
          "checksumValue": "${INSTALL_SHA}"
        }
      ]
    },
    {
      "SPDXID": "SPDXRef-Package-uninstall-ps1",
      "name": "uninstall.ps1",
      "versionInfo": "${VERSION}",
      "downloadLocation": "NOASSERTION",
      "licenseConcluded": "Apache-2.0",
      "checksums": [
        {
          "algorithm": "SHA256",
          "checksumValue": "${UNINSTALL_SHA}"
        }
      ]
    }
  ]
}
EOF
fi

# Generate internal checksums.txt (manifest of all packaged items)
printf 'release-windows: generating internal checksums manifest\n'
(
	cd "${STAGE}"
	sha256sum \
		devstack.exe \
		devstack-helper.exe \
		devstack-agent \
		debian-12-wsl-rootfs.tar.gz \
		debian-12-wsl-rootfs.tar.gz.sha256 \
		install.ps1 \
		uninstall.ps1 \
		$( [ -f devstack-daemon-task.ps1 ] && echo "devstack-daemon-task.ps1" ) \
		$( [ -f devstack-daemon.vbs ] && echo "devstack-daemon.vbs" ) \
		README.md \
		$( [ -f LICENSE ] && echo "LICENSE" ) \
		devstack.spdx.json >checksums.txt
)

# Package archive
ARCHIVE_ZIP_NAME="devstack_${VERSION}_windows_amd64.zip"
ARCHIVE_TAR_NAME="devstack_${VERSION}_windows_amd64.tar.gz"
ARCHIVE_ZIP="${OUTPUT_DIR}/${ARCHIVE_ZIP_NAME}"
ARCHIVE_TAR="${OUTPUT_DIR}/${ARCHIVE_TAR_NAME}"

printf 'release-windows: creating %s\n' "${ARCHIVE_ZIP}"
if command -v zip >/dev/null 2>&1; then
	(cd "${STAGE}" && zip -q -9 -r "${ARCHIVE_ZIP}" .)
elif command -v python3 >/dev/null 2>&1; then
	python3 -c "
import zipfile, os
stage = '${STAGE}'
archive = '${ARCHIVE_ZIP}'
with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
    for root, dirs, files in os.walk(stage):
        for file in sorted(files):
            full_path = os.path.join(root, file)
            arcname = os.path.relpath(full_path, stage)
            z.write(full_path, arcname)
"
elif command -v python >/dev/null 2>&1; then
	python -c "
import zipfile, os
stage = '${STAGE}'
archive = '${ARCHIVE_ZIP}'
with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
    for root, dirs, files in os.walk(stage):
        for file in sorted(files):
            full_path = os.path.join(root, file)
            arcname = os.path.relpath(full_path, stage)
            z.write(full_path, arcname)
"
else
	# Fallback PowerShell Compress-Archive
	powershell.exe -NoProfile -Command "Compress-Archive -Path '${STAGE}\*' -DestinationPath '${ARCHIVE_ZIP}' -Force"
fi

# Also create tar.gz archive for parity
printf 'release-windows: creating %s\n' "${ARCHIVE_TAR}"
TAR_MTIME="${SOURCE_DATE_EPOCH:-0}"
[[ "${TAR_MTIME}" =~ ^[0-9]+$ ]] || TAR_MTIME=0
(
	cd "${STAGE}"
	tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@${TAR_MTIME}" \
		-czf "${ARCHIVE_TAR}" *
)

# Build standalone binary installer executable (devstack-installer.exe)
INSTALLER_STAGE="${BUILD_DIR}/.installer-stage-$$"
mkdir -p "${INSTALLER_STAGE}"
cp "${REPO_ROOT}/cmd/devstack-installer/"*.go "${INSTALLER_STAGE}/"
cp -f "${ARCHIVE_ZIP}" "${INSTALLER_STAGE}/payload.zip"

printf 'release-windows: building standalone devstack-installer.exe\n'
(
	cd "${REPO_ROOT}"
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -ldflags "${LDFLAGS[*]}" -o "${OUTPUT_DIR}/devstack-installer.exe" "${INSTALLER_STAGE}"
	cp -f "${OUTPUT_DIR}/devstack-installer.exe" "${OUTPUT_DIR}/devstack-setup.exe"
)
rm -rf "${INSTALLER_STAGE}"

# Write outer checksums.txt in output directory
(
	cd "${OUTPUT_DIR}"
	sha256sum "${ARCHIVE_ZIP_NAME}" "${ARCHIVE_TAR_NAME}" devstack-installer.exe devstack-setup.exe >checksums.txt
)

printf 'release-windows: archive created successfully:\n'
printf '  zip:       %s\n' "${ARCHIVE_ZIP}"
printf '  tar.gz:    %s\n' "${ARCHIVE_TAR}"
printf '  installer: %s/devstack-installer.exe\n' "${OUTPUT_DIR}"
printf '  sha256:    %s\n' "$(cut -d' ' -f1 <"${OUTPUT_DIR}/checksums.txt" | head -1)"
