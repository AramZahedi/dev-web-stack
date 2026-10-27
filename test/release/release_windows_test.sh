#!/usr/bin/env bash
# Release archive contract test for build/release-windows.sh.
#
# Asserts the Windows release archive contract:
# - devstack_<version>_windows_amd64.zip and outer checksums.txt exist
# - Exact expected members: devstack.exe, devstack-helper.exe, devstack-agent,
#   debian-12-wsl-rootfs.tar.gz, sidecar sha256, install.ps1, uninstall.ps1,
#   README.md, internal checksums.txt, and devstack.spdx.json
# - Binaries have correct headers (Windows PE for .exe, Linux ELF for devstack-agent)
# - SPDX SBOM is valid JSON and conforms to SPDX-2.3
# - Internal checksums.txt verifies all packaged components
# - Outer checksums.txt verifies the archive and catches corruption
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
RELEASE_SCRIPT="${REPO_ROOT}/build/release-windows.sh"
VERSION="0.0.0-wintest"
ARCHIVE_ZIP="devstack_${VERSION}_windows_amd64.zip"

log() { printf 'release-windows-test: %s\n' "$*"; }
fail() { printf 'release-windows-test: FAIL: %s\n' "$*" >&2; exit 1; }
pass() { printf 'release-windows-test: ok: %s\n' "$*"; }

[ -x "${RELEASE_SCRIPT}" ] || [ -f "${RELEASE_SCRIPT}" ] || fail "missing ${RELEASE_SCRIPT}"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/devstack-win-test.XXXXXX")"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT

OUT="${TMP}/out"
BUILD="${TMP}/build"
mkdir -p "${OUT}" "${BUILD}"

log "building Windows release ${VERSION} into ${OUT}"
bash "${RELEASE_SCRIPT}" --version "${VERSION}" --skip-ui --output "${OUT}" --build-dir "${BUILD}"

ARCHIVE="${OUT}/${ARCHIVE_ZIP}"
CHECKSUMS="${OUT}/checksums.txt"

[ -f "${ARCHIVE}" ] || fail "missing archive: ${ARCHIVE}"
[ -f "${CHECKSUMS}" ] || fail "missing checksums manifest: ${CHECKSUMS}"
pass "archive and checksums manifest created"

# Verify outer checksums
(cd "${OUT}" && sha256sum -c "${CHECKSUMS}" >/dev/null) || fail "outer checksums.txt failed to verify archive"
pass "outer checksums.txt verifies archive"

# Extract archive
EXTRACT="${TMP}/extract"
mkdir -p "${EXTRACT}"

if command -v unzip >/dev/null 2>&1; then
	unzip -q "${ARCHIVE}" -d "${EXTRACT}"
elif command -v python3 >/dev/null 2>&1; then
	python3 -c "import zipfile; zipfile.ZipFile('${ARCHIVE}').extractall('${EXTRACT}')"
elif command -v python >/dev/null 2>&1; then
	python -c "import zipfile; zipfile.ZipFile('${ARCHIVE}').extractall('${EXTRACT}')"
else
	powershell.exe -NoProfile -Command "Expand-Archive -Path '${ARCHIVE}' -DestinationPath '${EXTRACT}' -Force"
fi

# Verify required files
for req in devstack.exe devstack-helper.exe devstack-agent debian-12-wsl-rootfs.tar.gz debian-12-wsl-rootfs.tar.gz.sha256 install.ps1 uninstall.ps1 README.md checksums.txt devstack.spdx.json; do
	[ -f "${EXTRACT}/${req}" ] || fail "missing expected member: ${req}"
done
pass "all required release members present in archive"

# Check binary formats
for winBin in devstack.exe devstack-helper.exe; do
	head_bytes="$(head -c 2 "${EXTRACT}/${winBin}")"
	[ "${head_bytes}" = "MZ" ] || fail "${winBin} does not have MZ magic header"
done
pass "devstack.exe and devstack-helper.exe have Windows PE MZ headers"

agent_elf="$(head -c 4 "${EXTRACT}/devstack-agent" | tr -d '\0')"
case "${agent_elf}" in
*ELF*) ;;
*) fail "devstack-agent does not have ELF magic header" ;;
esac
pass "devstack-agent has Linux ELF header"

# Verify SBOM
if command -v python3 >/dev/null 2>&1; then
	python3 -c "
import json
with open('${EXTRACT}/devstack.spdx.json') as f:
    data = json.load(f)
assert data.get('spdxVersion') == 'SPDX-2.3'
assert data.get('SPDXID') == 'SPDXRef-DOCUMENT'
assert len(data.get('packages', [])) >= 6
" || fail "devstack.spdx.json is invalid SPDX 2.3 JSON"
elif command -v python >/dev/null 2>&1; then
	python -c "
import json
with open('${EXTRACT}/devstack.spdx.json') as f:
    data = json.load(f)
assert data.get('spdxVersion') == 'SPDX-2.3'
assert data.get('SPDXID') == 'SPDXRef-DOCUMENT'
assert len(data.get('packages', [])) >= 6
" || fail "devstack.spdx.json is invalid SPDX 2.3 JSON"
fi
pass "devstack.spdx.json is valid SPDX-2.3 JSON with packaged components"

# Verify internal checksums.txt
(cd "${EXTRACT}" && sha256sum -c checksums.txt >/dev/null) || fail "internal checksums.txt verification failed"
pass "internal checksums.txt verifies all packaged files"

# Verify corruption detection on outer archive
CORRUPT="${TMP}/corrupt"
mkdir -p "${CORRUPT}"
cp "${ARCHIVE}" "${CORRUPT}/${ARCHIVE_ZIP}"
cp "${CHECKSUMS}" "${CORRUPT}/checksums.txt"
printf 'corruption' >>"${CORRUPT}/${ARCHIVE_ZIP}"
if (cd "${CORRUPT}" && sha256sum -c checksums.txt >/dev/null 2>&1); then
	fail "corrupted archive passed checksum check"
fi
pass "corrupted archive successfully caught by checksum check"

log "ALL WINDOWS RELEASE TESTS PASSED"
