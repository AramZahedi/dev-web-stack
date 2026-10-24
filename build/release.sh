#!/usr/bin/env bash
# Build the Linux amd64 devstack release archive and SHA-256 manifest.
#
# This is the local counterpart to GoReleaser. CI publishes the same archive
# name and member set; `make release-test` checks this contract on every run.
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
MODULE="github.com/AramZahedi/dev-web-stack"
DEFAULT_OUTPUT="dist/release"

usage() {
	cat <<'EOF'
Usage: build/release.sh --version <semver> [--output <dir>] [--build-dir <dir>] [--skip-ui]

Builds devstack and devstack-helper for linux/amd64, packages them with
install.sh, README.md, and LICENSE (when present) as
devstack_<version>_linux_amd64.tar.gz, and writes checksums.txt.

Options:
  --version <semver>  Release version without the leading "v" (required).
  --output <dir>      Output directory (default: dist/release in the repo).
  --build-dir <dir>   Directory for the two intermediate binaries
                      (default: dist in the repo). Tests use a temp directory
                      so they never overwrite a developer's dist/devstack*.
  --skip-ui           Do not run `make ui`; use the existing ui/dist build.
  -h, --help          Show this help.
EOF
}

die() {
	printf 'release: error: %s\n' "$*" >&2
	exit 1
}

VERSION=""
OUTPUT_DIR="${DEFAULT_OUTPUT}"
BUILD_DIR="dist"
SKIP_UI=0

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
	--skip-ui)
		SKIP_UI=1
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

# Accept a leading v so a git tag can be passed directly, then require a plain
# semver. The character class excludes path separators and whitespace.
VERSION="${VERSION#v}"
if [[ ! "${VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
	die "invalid version ${VERSION@Q}: expected <major>.<minor>.<patch> with an optional prerelease suffix"
fi

COMMIT="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || true)"
COMMIT="${COMMIT:-unknown}"

# Relative output paths are relative to the repository root so `make release`
# works from any directory.
case "${OUTPUT_DIR}" in
/*) ;;
*) OUTPUT_DIR="${REPO_ROOT}/${OUTPUT_DIR}" ;;
esac
mkdir -p "${OUTPUT_DIR}"
OUTPUT_DIR="$(cd -- "${OUTPUT_DIR}" && pwd)"

[ -n "${BUILD_DIR}" ] || die "--build-dir must not be empty"
case "${BUILD_DIR}" in
/*) ;;
*) BUILD_DIR="${REPO_ROOT}/${BUILD_DIR}" ;;
esac
mkdir -p "${BUILD_DIR}"
BUILD_DIR="$(cd -- "${BUILD_DIR}" && pwd)"

STAGE="$(mktemp -d "${TMPDIR:-/tmp}/devstack-release-stage.XXXXXX")"
VERIFY_DIR="$(mktemp -d "${TMPDIR:-/tmp}/devstack-release-verify.XXXXXX")"
cleanup() {
	rm -rf "${STAGE}" "${VERIFY_DIR}"
}
trap cleanup EXIT

if [ "${SKIP_UI}" -eq 0 ]; then
	printf 'release: building UI\n'
	make -C "${REPO_ROOT}" ui
else
	printf 'release: skipping UI build (--skip-ui)\n'
fi

for required in install.sh README.md; do
	[ -f "${REPO_ROOT}/${required}" ] || die "missing required release file ${REPO_ROOT}/${required}"
done

LDFLAGS=(
	-s -w
	-X "${MODULE}/internal/version.Version=${VERSION}"
	-X "${MODULE}/internal/version.Commit=${COMMIT}"
)

build_binary() {
	local main="$1" output="$2"
	printf 'release: building %s\n' "${output}"
	(
		cd "${REPO_ROOT}"
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
			go build -trimpath -ldflags "${LDFLAGS[*]}" -o "${BUILD_DIR}/${output}" "${main}"
	)
}

build_binary ./cmd/devstack devstack
build_binary ./cmd/devstack-helper devstack-helper

install -m 0755 "${BUILD_DIR}/devstack" "${STAGE}/devstack"
install -m 0755 "${BUILD_DIR}/devstack-helper" "${STAGE}/devstack-helper"
install -m 0755 "${REPO_ROOT}/install.sh" "${STAGE}/install.sh"
install -m 0644 "${REPO_ROOT}/README.md" "${STAGE}/README.md"
if [ -f "${REPO_ROOT}/LICENSE" ]; then
	install -m 0644 "${REPO_ROOT}/LICENSE" "${STAGE}/LICENSE"
fi

# Verify the packaged binary before writing a publishable archive. A wrong
# ldflags value or stale dist/ binary must not leave a checksummed archive.
REPORTED_VERSION="$("${STAGE}/devstack" version --json)"
case "${REPORTED_VERSION}" in
*"\"version\":\"${VERSION}\""*) ;;
*) die "packaged devstack does not report ${VERSION}: ${REPORTED_VERSION}" ;;
esac

ARCHIVE_NAME="devstack_${VERSION}_linux_amd64.tar.gz"
ARCHIVE="${OUTPUT_DIR}/${ARCHIVE_NAME}"
MEMBERS=(devstack devstack-helper install.sh README.md)
if [ -f "${REPO_ROOT}/LICENSE" ]; then
	MEMBERS+=(LICENSE)
fi

# Pin every tar member's mtime and suppress gzip's filename/timestamp header so
# two builds of the same source and version produce byte-identical archives.
TAR_MTIME="${SOURCE_DATE_EPOCH:-0}"
[[ "${TAR_MTIME}" =~ ^[0-9]+$ ]] ||
	die "SOURCE_DATE_EPOCH must be a non-negative integer, got ${TAR_MTIME@Q}"
ARCHIVE_TAR="${STAGE}/archive.tar"
printf 'release: creating %s\n' "${ARCHIVE}"
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@${TAR_MTIME}" \
	-cf "${ARCHIVE_TAR}" -C "${STAGE}" "${MEMBERS[@]}"
gzip -n -c "${ARCHIVE_TAR}" >"${ARCHIVE}"
rm -f "${ARCHIVE_TAR}"

# Write the manifest from inside the output directory so each line is only the
# archive basename, which is what install.sh expects.
(
	cd "${OUTPUT_DIR}"
	sha256sum "${ARCHIVE_NAME}" >checksums.txt
)

CHECKSUM="$(cut -d' ' -f1 <"${OUTPUT_DIR}/checksums.txt")"
printf 'archive: %s\n' "${ARCHIVE}"
printf 'sha256:  %s\n' "${CHECKSUM}"
