#!/usr/bin/env bash
# Release archive contract test for build/release.sh.
#
# The test builds a throwaway release with --skip-ui (ui/dist is committed to
# the build by the normal `make ui` step), then asserts the archive contract:
# exact members, no agent/Windows/darwin payloads, a real SHA-256 manifest, a
# packaged binary that reports the requested version and protocol, and an
# executable installer.
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
RELEASE_SCRIPT="${REPO_ROOT}/build/release.sh"
VERSION="0.0.0-test"
ARCHIVE_NAME="devstack_${VERSION}_linux_amd64.tar.gz"

log() {
	printf 'release-test: %s\n' "$*"
}

fail() {
	printf 'release-test: FAIL: %s\n' "$*" >&2
	exit 1
}

pass() {
	printf 'release-test: ok: %s\n' "$*"
}

[ -x "${RELEASE_SCRIPT}" ] || fail "missing executable ${RELEASE_SCRIPT}"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/devstack-release-test.XXXXXX")"
DEFAULT_RELEASE_DIR=""
DEFAULT_ARCHIVE=""
DEFAULT_CHECKSUMS=""
DEFAULT_BACKUP=""
DEFAULT_RELEASE_TOUCHED=0
cleanup() {
	if [ "${DEFAULT_RELEASE_TOUCHED}" -eq 1 ]; then
		rm -f "${DEFAULT_ARCHIVE}"
		if [ -f "${DEFAULT_BACKUP}/archive" ]; then
			mv "${DEFAULT_BACKUP}/archive" "${DEFAULT_ARCHIVE}"
		fi
		if [ -f "${DEFAULT_BACKUP}/checksums" ]; then
			mv "${DEFAULT_BACKUP}/checksums" "${DEFAULT_CHECKSUMS}"
		else
			rm -f "${DEFAULT_CHECKSUMS}"
		fi
		rmdir "${DEFAULT_RELEASE_DIR}" 2>/dev/null || true
	fi
	rm -rf "${TMP}"
}
trap cleanup EXIT

OUT="${TMP}/out"
mkdir -p "${OUT}"

log "building ${VERSION} in ${OUT}"
"${RELEASE_SCRIPT}" --version "${VERSION}" --skip-ui --output "${OUT}"

ARCHIVE="${OUT}/${ARCHIVE_NAME}"
CHECKSUMS="${OUT}/checksums.txt"
[ -f "${ARCHIVE}" ] || fail "missing archive ${ARCHIVE}"
[ -f "${CHECKSUMS}" ] || fail "missing checksum manifest ${CHECKSUMS}"
pass "archive and checksums.txt exist"

# The member set is exact. LICENSE is optional in the contract, so derive the
# expectation from the repository rather than hardcoding it.
expected_members=(devstack devstack-helper install.sh README.md)
if [ -f "${REPO_ROOT}/LICENSE" ]; then
	expected_members+=(LICENSE)
fi
expected="$(printf '%s\n' "${expected_members[@]}" | LC_ALL=C sort)"
actual="$(tar -tzf "${ARCHIVE}" | LC_ALL=C sort)"
if [ "${actual}" != "${expected}" ]; then
	printf 'expected members:\n%s\nactual members:\n%s\n' "${expected}" "${actual}" >&2
	fail "archive members do not match the release contract"
fi
pass "archive members are exactly: $(printf '%s ' "${expected_members[@]}")"

for forbidden in devstack-agent windows darwin; do
	case "${actual}" in
	*"${forbidden}"*)
		fail "archive contains forbidden member text: ${forbidden}"
		;;
	esac
done
pass "no devstack-agent, Windows, or darwin members"

# Extract into a fresh directory so the checks below exercise the archive, not
# the dist/ build outputs.
EXTRACT="${TMP}/extract"
mkdir -p "${EXTRACT}"
tar -xzf "${ARCHIVE}" -C "${EXTRACT}"
for binary in devstack devstack-helper; do
	[ -x "${EXTRACT}/${binary}" ] || fail "packaged ${binary} is not executable"
done
[ -x "${EXTRACT}/install.sh" ] || fail "packaged install.sh is not executable"
pass "packaged install.sh and both binaries are executable"

VERSION_JSON="$("${EXTRACT}/devstack" version --json)"
case "${VERSION_JSON}" in
*"\"version\":\"${VERSION}\""*) ;;
*) fail "packaged devstack version JSON does not report ${VERSION}: ${VERSION_JSON}" ;;
esac
PROTOCOL="$(printf '%s' "${VERSION_JSON}" | sed -n 's/.*"protocol":"\([^"]*\)".*/\1/p')"
[ -n "${PROTOCOL}" ] || fail "packaged devstack reports an empty helper protocol: ${VERSION_JSON}"
pass "packaged devstack reports version ${VERSION} and protocol ${PROTOCOL}"

# checksums.txt must name exactly the archive and be verifiable from the output
# directory, because that is how install.sh consumes it.
if [ "$(wc -l <"${CHECKSUMS}")" -ne 1 ]; then
	fail "checksums.txt must contain exactly one line"
fi
awk -v archive="${ARCHIVE_NAME}" '
	NF != 2 || length($1) != 64 || $1 !~ /^[0-9a-f]+$/ || $2 != archive { bad = 1 }
	{ lines++ }
	END { exit (bad || lines != 1) }
' "${CHECKSUMS}" ||
	fail "checksums.txt is not a single lowercase sha256 line for ${ARCHIVE_NAME}"
(cd "${OUT}" && sha256sum -c "${CHECKSUMS}" >/dev/null) ||
	fail "sha256sum -c rejected the archive"
pass "checksums.txt verifies the archive"

# A corrupted copy must fail verification. This proves the manifest covers the
# archive bytes rather than being a well-formed but meaningless line.
CORRUPT="${TMP}/corrupt"
mkdir -p "${CORRUPT}"
cp "${ARCHIVE}" "${CORRUPT}/${ARCHIVE_NAME}"
cp "${CHECKSUMS}" "${CORRUPT}/checksums.txt"
printf 'corruption' >>"${CORRUPT}/${ARCHIVE_NAME}"
if (cd "${CORRUPT}" && sha256sum -c checksums.txt >/dev/null 2>&1); then
	fail "checksum verification accepted a corrupted archive"
fi
pass "checksum verification rejects a corrupted archive"

# Regression: a leading v is normalised out of the archive filename, so a git
# tag can be passed directly without producing devstack_v<version>_...
VOUT="${TMP}/vout"
mkdir -p "${VOUT}"
"${RELEASE_SCRIPT}" --version "v${VERSION}" --skip-ui --output "${VOUT}"
[ -f "${VOUT}/${ARCHIVE_NAME}" ] ||
	fail "leading v was not normalised to ${ARCHIVE_NAME}"
for candidate in "${VOUT}"/*; do
	[ -e "${candidate}" ] || continue
	case "$(basename "${candidate}")" in
	*_v"${VERSION}"*)
		fail "leading v leaked into archive member name: ${candidate}"
		;;
	esac
done
pass "leading v is normalised in the archive filename"

# Regression: invalid versions and path traversal must be rejected before any
# output directory is created, so they cannot influence the archive filename.
BAD_OUT="${TMP}/bad-out"
for bad_version in "../evil" "1.2" "1.2.3/../../evil" "1.2.3 extra" "v../evil"; do
	if "${RELEASE_SCRIPT}" --version "${bad_version}" --skip-ui --output "${BAD_OUT}" >/dev/null 2>&1; then
		fail "invalid version was accepted: ${bad_version}"
	fi
done
[ ! -e "${BAD_OUT}" ] || fail "invalid version created the output directory"
traversal_artifact="$(find "${TMP}" -maxdepth 3 -name '*evil*' -print -quit)"
[ -z "${traversal_artifact}" ] || fail "invalid version produced a path-traversal artifact: ${traversal_artifact}"
pass "invalid versions and path traversal are rejected"

# Regression: without --output the script must write to dist/release under the
# repository root, regardless of the caller's working directory. Preserve any
# pre-existing artifacts and restore them on exit.
DEFAULT_VERSION="0.0.0-defaultpath"
DEFAULT_RELEASE_DIR="${REPO_ROOT}/dist/release"
DEFAULT_ARCHIVE="${DEFAULT_RELEASE_DIR}/devstack_${DEFAULT_VERSION}_linux_amd64.tar.gz"
DEFAULT_CHECKSUMS="${DEFAULT_RELEASE_DIR}/checksums.txt"
DEFAULT_BACKUP="${TMP}/default-backup"
mkdir -p "${DEFAULT_BACKUP}" "${DEFAULT_RELEASE_DIR}"
if [ -f "${DEFAULT_CHECKSUMS}" ]; then
	cp "${DEFAULT_CHECKSUMS}" "${DEFAULT_BACKUP}/checksums"
fi
if [ -f "${DEFAULT_ARCHIVE}" ]; then
	mv "${DEFAULT_ARCHIVE}" "${DEFAULT_BACKUP}/archive"
fi
DEFAULT_RELEASE_TOUCHED=1
if ! (cd "${TMP}" && "${RELEASE_SCRIPT}" --version "${DEFAULT_VERSION}" --skip-ui); then
	fail "default output build failed"
fi
[ -f "${DEFAULT_ARCHIVE}" ] ||
	fail "default build did not write ${DEFAULT_ARCHIVE}"
[ -f "${DEFAULT_CHECKSUMS}" ] ||
	fail "default build did not write ${DEFAULT_CHECKSUMS}"
grep -Fq "devstack_${DEFAULT_VERSION}_linux_amd64.tar.gz" "${DEFAULT_CHECKSUMS}" ||
	fail "default checksums.txt does not name the default archive"
pass "default output is dist/release from an unrelated working directory"

log "all release archive contract checks passed"
