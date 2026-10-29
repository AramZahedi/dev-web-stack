#!/usr/bin/env bash
# Release archive contract test for build/release-windows.sh.
#
# Asserts the Windows release archive contract:
# - devstack_<version>_windows_amd64.zip and outer checksums.txt exist
# - Exact expected members: devstack.exe, devstack-helper.exe, devstack-agent,
#   debian-12-wsl-rootfs.tar.gz, sidecar sha256, install.ps1, uninstall.ps1,
#   README.md, internal checksums.txt, devstack.spdx.json, and the WSL
#   provisioning assets under build/wsl/ (agent.service, install-agent.sh,
#   install-docker.sh, docker-daemon.json)
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

log "asserting fail-closed release: missing rootfs without --mock-rootfs must fail"
if (cd "${REPO_ROOT}" && bash "${RELEASE_SCRIPT}" --version "${VERSION}" --skip-ui --rootfs /nonexistent/rootfs.tar.gz --output "${OUT}" --build-dir "${BUILD}" 2>/dev/null); then
	fail "expected release-windows.sh to fail with nonexistent rootfs"
fi
pass "release-windows.sh fails closed on missing rootfs without --mock-rootfs"

log "building Windows release ${VERSION} into ${OUT} using --mock-rootfs"
bash "${RELEASE_SCRIPT}" --version "${VERSION}" --skip-ui --mock-rootfs --output "${OUT}" --build-dir "${BUILD}"

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
for req in devstack.exe devstack-helper.exe devstack-agent debian-12-wsl-rootfs.tar.gz debian-12-wsl-rootfs.tar.gz.sha256 install.ps1 uninstall.ps1 README.md checksums.txt devstack.spdx.json build/wsl/agent.service build/wsl/install-agent.sh build/wsl/install-docker.sh build/wsl/docker-daemon.json; do
	[ -s "${EXTRACT}/${req}" ] || fail "missing or empty expected member: ${req}"
done
pass "all required release members present in archive"

# The installer resolves its provisioning assets under <payload>/build/wsl.
for asset in agent.service install-agent.sh install-docker.sh docker-daemon.json; do
	grep -Fq "build\\wsl\\${asset}" "${EXTRACT}/install.ps1" || fail "install.ps1 does not reference build\\wsl\\${asset}"
done
pass "install.ps1 references the packaged WSL provisioning assets"

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
packages = {p.get('name') for p in data.get('packages', [])}
assert len(packages) >= 10, packages
for required in ('devstack.exe', 'devstack-helper.exe', 'devstack-agent',
                 'debian-12-wsl-rootfs.tar.gz', 'install.ps1', 'uninstall.ps1',
                 'build/wsl/agent.service', 'build/wsl/install-agent.sh',
                 'build/wsl/install-docker.sh', 'build/wsl/docker-daemon.json'):
    assert required in packages, required
" || fail "devstack.spdx.json is invalid SPDX 2.3 JSON or omits packaged components"
elif command -v python >/dev/null 2>&1; then
	python -c "
import json
with open('${EXTRACT}/devstack.spdx.json') as f:
    data = json.load(f)
assert data.get('spdxVersion') == 'SPDX-2.3'
assert data.get('SPDXID') == 'SPDXRef-DOCUMENT'
packages = {p.get('name') for p in data.get('packages', [])}
assert len(packages) >= 10, packages
for required in ('devstack.exe', 'devstack-helper.exe', 'devstack-agent',
                 'debian-12-wsl-rootfs.tar.gz', 'install.ps1', 'uninstall.ps1',
                 'build/wsl/agent.service', 'build/wsl/install-agent.sh',
                 'build/wsl/install-docker.sh', 'build/wsl/docker-daemon.json'):
    assert required in packages, required
" || fail "devstack.spdx.json is invalid SPDX 2.3 JSON or omits packaged components"
fi
pass "devstack.spdx.json is valid SPDX-2.3 JSON with packaged components"

# The installer must fail closed when the WSL provisioning assets are absent.
grep -Fq "Release payload is incomplete" "${EXTRACT}/install.ps1" ||
	fail "install.ps1 does not fail closed on a missing WSL provisioning payload"

# --mock-rootfs must only skip the real-rootfs gates for a rootfs this build
# fabricated. Supplying an unpinned rootfs together with the flag must still
# fail closed.
PINNED_TEST_ROOTFS="${TMP}/unpinned-rootfs.tar.gz"
if command -v truncate >/dev/null 2>&1; then
	truncate -s 110000000 "${PINNED_TEST_ROOTFS}"
	OUT_PINNED="${TMP}/out-pinned"
	BUILD_PINNED="${TMP}/build-pinned"
	mkdir -p "${OUT_PINNED}" "${BUILD_PINNED}"
	if (cd "${REPO_ROOT}" && bash "${RELEASE_SCRIPT}" --version "${VERSION}" --skip-ui \
		--rootfs "${PINNED_TEST_ROOTFS}" --mock-rootfs \
		--output "${OUT_PINNED}" --build-dir "${BUILD_PINNED}" 2>"${TMP}/pinned.err"); then
		fail "release accepted an unpinned rootfs supplied together with --mock-rootfs"
	fi
	grep -Fq "does not match pinned rootfs_sha256" "${TMP}/pinned.err" ||
		fail "unpinned rootfs failure did not mention the pinned hash: $(cat "${TMP}/pinned.err")"
	pass "--mock-rootfs does not bypass the pinned-rootfs gate for supplied artifacts"
else
	log "truncate is unavailable; pinned-rootfs override check skipped"
fi

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
