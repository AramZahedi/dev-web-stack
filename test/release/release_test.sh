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
cleanup() {
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
grep -Eq "^[0-9a-f]{64}  ${ARCHIVE_NAME}$" "${CHECKSUMS}" ||
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

log "all release archive contract checks passed"
