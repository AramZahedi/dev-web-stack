#!/usr/bin/env bash
# Installer release/archive/upgrade test.
#
# Every invocation runs with fake sudo, systemctl, curl, getent, and sleep
# commands in PATH. The fake sudo rewrites the root-owned install targets into
# a temporary sandbox, so this test never touches /etc, /usr/local, systemd, or
# the real user's home.
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALLER="${REPO_ROOT}/install.sh"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/devstack-installer-test.XXXXXX")"
TOOLS="${TMP}/tools"
FAKE_HOME="${TMP}/home"
FAKE_ROOT="${TMP}/root"
FAKE_RELEASES="${TMP}/releases"
COMMAND_LOG="${TMP}/commands.log"
CURL_LOG="${TMP}/curl.log"
HTTP_SERVER_PID=""

TEST_UID="$(id -u)"
TEST_GID="$(id -g)"
VERSION="0.0.0-installer"

log() {
	printf 'installer-test: %s\n' "$*"
}

pass() {
	printf 'installer-test: ok: %s\n' "$*"
}

fail() {
	printf 'installer-test: FAIL: %s\n' "$*" >&2
	exit 1
}

skip() {
	printf 'installer-test: skip: %s\n' "$*"
}

cleanup() {
	if [ -n "${HTTP_SERVER_PID}" ]; then
		kill "${HTTP_SERVER_PID}" 2>/dev/null || true
		wait "${HTTP_SERVER_PID}" 2>/dev/null || true
	fi
	rm -rf "${TMP}"
}
trap cleanup EXIT

for tool in tar sha256sum mktemp; do
	if ! command -v "${tool}" >/dev/null 2>&1; then
		skip "required tool not found: ${tool}"
		exit 0
	fi
done
if [ ! -f "${INSTALLER}" ]; then
	fail "missing ${INSTALLER}"
fi
if [ "${TEST_UID}" -eq 0 ]; then
	skip "the installer refuses to run as root; run this test as a normal user"
	exit 0
fi

mkdir -p "${TOOLS}" "${FAKE_HOME}" "${FAKE_ROOT}" "${FAKE_RELEASES}"
: >"${COMMAND_LOG}"
: >"${CURL_LOG}"

# The installer resolves the home directory with getent. Shadow it so every
# user-file operation lands in the temporary sandbox.
cat >"${TOOLS}/getent" <<EOF
#!/usr/bin/env bash
printf 'devstack-test:x:${TEST_UID}:${TEST_GID}::${FAKE_HOME}:/bin/bash\n'
EOF
chmod 0755 "${TOOLS}/getent"

# sudo is never executed for real. Systemctl calls are accepted; install calls
# are redirected below FAKE_ROOT so unit/config contents can be inspected.
cat >"${TOOLS}/sudo" <<EOF
#!/usr/bin/env bash
printf 'sudo %s\n' "\$*" >>"${COMMAND_LOG}"
if [ "\${1:-}" = "-v" ] || [ "\${1:-}" = "-n" ]; then
	if [ "\${FAKE_SUDO_DENY:-0}" = 1 ]; then
		exit 1
	fi
	exit 0
fi
if [ "\${1:-}" = systemctl ]; then
	if [ "\${2:-}" = is-active ] && [ "\${4:-}" = devstack-helper.service ]; then
		if [ "\${FAKE_HELPER_ACTIVE:-1}" = 1 ]; then
			exit 0
		fi
		exit 1
	fi
	exit 0
fi
if [ "\${1:-}" = cat ]; then
	shift
	target="\$1"
	case "\${target}" in
	/usr/local/lib/devstack* | /etc/devstack* | /etc/systemd/system/* | /data/devstack*)
		target="${FAKE_ROOT}\${target}"
		;;
	esac
	exec cat "\${target}"
fi
if [ "\${1:-}" != install ]; then
	printf 'fake sudo: refusing unexpected command: %s\n' "\$*" >&2
	exit 1
fi
shift
mkdir -p "${FAKE_ROOT}/etc/systemd/system" "${FAKE_ROOT}/etc/devstack" "${FAKE_ROOT}/usr/local/lib/devstack" "${FAKE_ROOT}/data/devstack"
args=()
for arg in "\$@"; do
	case "\${arg}" in
	/usr/local/lib/devstack* | /etc/devstack* | /etc/systemd/system/* | /data/devstack*)
		arg="${FAKE_ROOT}\${arg}"
		;;
	esac
	args+=("\${arg}")
done
exec install "\${args[@]}"
EOF
chmod 0755 "${TOOLS}/sudo"

cat >"${TOOLS}/systemctl" <<EOF
#!/usr/bin/env bash
printf 'systemctl %s\n' "\$*" >>"${COMMAND_LOG}"
if [ "\${1:-}" = --user ] && [ "\${2:-}" = show-environment ]; then
	if [ "\${FAKE_SYSTEMD_SESSION:-1}" = 1 ]; then
		exit 0
	fi
	exit 1
fi
if [ "\${1:-}" = --user ] && [ "\${2:-}" = is-active ]; then
	if [ "\${FAKE_UNIT_ACTIVE:-0}" = 1 ]; then
		exit 0
	fi
	exit 1
fi
if [ "\${1:-}" = is-active ] && [ "\${3:-}" = devstack-helper.service ]; then
	if [ "\${FAKE_HELPER_ACTIVE:-1}" = 1 ]; then
		exit 0
	fi
	exit 1
fi
exit 0
EOF
chmod 0755 "${TOOLS}/systemctl"

# The release downloader must never touch the network. Copy fixtures from
# FAKE_RELEASES based on the requested basename, and record the URL.
cat >"${TOOLS}/curl" <<EOF
#!/usr/bin/env bash
printf 'curl %s\n' "\$*" >>"${COMMAND_LOG}"
output=""
url=""
while [ "\$#" -gt 0 ]; do
	case "\$1" in
	-o | --output)
		output="\$2"
		shift 2
		;;
	http://* | https://*)
		url="\$1"
		shift
		;;
	*)
		shift
		;;
	esac
done
printf '%s\n' "\${url}" >>"${CURL_LOG}"
if [ -z "\${output}" ] || [ -z "\${url}" ]; then
	printf 'fake curl: missing output or URL\n' >&2
	exit 2
fi
fixture="${FAKE_RELEASES}/\$(basename "\${url}")"
if [ ! -f "\${fixture}" ]; then
	printf 'fake curl: no fixture for %s\n' "\${url}" >&2
	exit 22
fi
cp "\${fixture}" "\${output}"
EOF
chmod 0755 "${TOOLS}/curl"

# The helper socket wait loop must not make the test slow.
cat >"${TOOLS}/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod 0755 "${TOOLS}/sleep"

INVOKE_OUT=""
INVOKE_STATUS=0
invoke() {
	local binary="$1"
	shift
	: >"${COMMAND_LOG}"
	: >"${CURL_LOG}"
	set +e
	local env_args=(
		"PATH=${TOOLS}:${PATH}"
		"HOME=${FAKE_HOME}"
		"XDG_CONFIG_HOME=${FAKE_HOME}/.config"
		"DEVSTACK_LEGACY_CONFIG=${FAKE_ROOT}/etc/devstack/helper.yaml"
	)
	if [ "${UNSET_DEVSTACK_HOME:-0}" -ne 1 ]; then
		env_args+=("DEVSTACK_HOME=${TEST_DEVSTACK_HOME:-${FAKE_HOME}/.devstack}")
	fi
	INVOKE_OUT="$(env "${env_args[@]}" "$binary" "$@" 2>&1)"
	INVOKE_STATUS=$?
	set -e
}

expect_success() {
	if [ "${INVOKE_STATUS}" -ne 0 ]; then
		fail "expected success, got status ${INVOKE_STATUS}: ${INVOKE_OUT}"
	fi
}

expect_failure() {
	if [ "${INVOKE_STATUS}" -eq 0 ]; then
		fail "expected failure but the installer succeeded: ${INVOKE_OUT}"
	fi
}

expect_output() {
	case "${INVOKE_OUT}" in
	*"$1"*) ;;
	*) fail "output does not contain '$1': ${INVOKE_OUT}" ;;
	esac
}

expect_no_sudo_install() {
	if grep -Eq '^sudo install' "${COMMAND_LOG}"; then
		fail "an install command was attempted before verification completed: $(cat "${COMMAND_LOG}")"
	fi
}

expect_no_privileged_commands() {
	if grep -Eq '^(sudo|systemctl) ' "${COMMAND_LOG}"; then
		fail "a privileged command was attempted before verification completed: $(cat "${COMMAND_LOG}")"
	fi
}

expect_no_service_stop() {
	if grep -Eq '(^| )systemctl( --user)? stop ' "${COMMAND_LOG}"; then
		fail "the installer stopped a service before sudo succeeded: $(cat "${COMMAND_LOG}")"
	fi
}

expect_no_download() {
	if [ -s "${CURL_LOG}" ]; then
		fail "the installer attempted a download: $(cat "${CURL_LOG}")"
	fi
}

# stage_release writes a small, self-contained release fixture. The fake
# devstack answers `version --json`, so no Go build is needed here.
stage_release() {
	local stage="$1" reported="$2"
	mkdir -p "${stage}"
	cat >"${stage}/devstack" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = version ] && [ "\${2:-}" = --json ]; then
	printf '{"version":"%s","protocol":"1","os":"linux","arch":"amd64"}\n' "${reported}"
	exit 0
fi
if [ "\${1:-}" = daemon ]; then
	exit 0
fi
exit 0
EOF
	chmod 0755 "${stage}/devstack"
	printf '#!/usr/bin/env bash\nexit 0\n' >"${stage}/devstack-helper"
	chmod 0755 "${stage}/devstack-helper"
	printf '#!/usr/bin/env bash\nexit 0\n' >"${stage}/install.sh"
	chmod 0755 "${stage}/install.sh"
	printf 'devstack test release\n' >"${stage}/README.md"
}

make_release() {
	local dir="$1" version="$2" reported="${3:-$2}"
	local stage="${dir}/stage"
	local name="devstack_${version}_linux_amd64.tar.gz"
	stage_release "${stage}" "${reported}"
	tar --sort=name --owner=0 --group=0 --numeric-owner \
		-czf "${dir}/${name}" -C "${stage}" \
		devstack devstack-helper install.sh README.md
	(cd "${dir}" && sha256sum "${name}" >checksums.txt)
}

make_bad_release() {
	local dir="$1" kind="$2"
	local version="0.0.0-bad"
	local stage="${dir}/stage"
	local name="devstack_${version}_linux_amd64.tar.gz"
	stage_release "${stage}" "${version}"
	case "${kind}" in
	traversal)
		printf 'evil\n' >"${stage}/evil"
		tar --sort=name --owner=0 --group=0 --numeric-owner \
			--transform='s|^evil$|../evil|' \
			-czf "${dir}/${name}" -C "${stage}" \
			devstack devstack-helper install.sh README.md evil 2>/dev/null
		;;
	absolute)
		printf 'evil\n' >"${stage}/evil"
		tar --sort=name --owner=0 --group=0 --numeric-owner \
			--transform='s|^evil$|/absolute|' \
			-czf "${dir}/${name}" -C "${stage}" \
			devstack devstack-helper install.sh README.md evil 2>/dev/null
		;;
	symlink)
		ln -s /etc/passwd "${stage}/evil"
		tar --sort=name --owner=0 --group=0 --numeric-owner \
			-czf "${dir}/${name}" -C "${stage}" \
			devstack devstack-helper install.sh README.md evil
		;;
	hardlink)
		printf 'evil\n' >"${stage}/evil"
		ln "${stage}/evil" "${stage}/evil-hard"
		tar --sort=name --owner=0 --group=0 --numeric-owner \
			-czf "${dir}/${name}" -C "${stage}" \
			devstack devstack-helper install.sh README.md evil evil-hard
		;;
	*)
		fail "unknown bad release kind ${kind}"
		;;
	esac
	(cd "${dir}" && sha256sum "${name}" >checksums.txt)
}

GOOD="${TMP}/good"
mkdir -p "${GOOD}"
make_release "${GOOD}" "${VERSION}"
GOOD_ARCHIVE="${GOOD}/devstack_${VERSION}_linux_amd64.tar.gz"
GOOD_CHECKSUMS="${GOOD}/checksums.txt"

# A local HTTP server makes the rejected URL realistic. Rejection happens from
# the scheme check, so no request should reach it.
SERVE="${TMP}/serve"
mkdir -p "${SERVE}"
cp "${GOOD_ARCHIVE}" "${GOOD_CHECKSUMS}" "${SERVE}/"
HTTP_PORT="$((20000 + RANDOM % 20000))"
if command -v python3 >/dev/null 2>&1; then
	python3 -m http.server "${HTTP_PORT}" --bind 127.0.0.1 --directory "${SERVE}" \
		>"${TMP}/http.log" 2>&1 &
	HTTP_SERVER_PID=$!
fi

# Source mode remains the default and still previews from dist/.
if [ -x "${REPO_ROOT}/dist/devstack" ] && [ -x "${REPO_ROOT}/dist/devstack-helper" ]; then
	invoke "${INSTALLER}" --dry-run
	expect_success
	expect_output "dist/devstack"
	expect_output "sudo install"
	pass "source-tree dry run still uses dist/"
else
	skip "source-tree dry run (dist/ binaries are not built)"
fi

# Archive mode: verify, extract, and print install commands without writing.
invoke "${INSTALLER}" --dry-run --archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
expect_success
expect_output "checksum verified"
expect_output "install -m 0755"
expect_output "sudo install"
expect_output "devstack init"
if [ -e "${FAKE_HOME}/.local/bin/devstack" ] || [ -e "${FAKE_ROOT}/usr/local/lib/devstack/devstack-helper" ]; then
	fail "archive dry run wrote install files"
fi
pass "archive dry run verifies the checksum and prints the install plan"

# HTTPS-only base URL: the HTTP URL is rejected before download.
invoke "${INSTALLER}" --dry-run --release "${VERSION}" \
	--base-url "http://127.0.0.1:${HTTP_PORT}"
expect_failure
expect_output "HTTPS"
expect_no_download
if [ -f "${TMP}/http.log" ] && grep -q 'GET ' "${TMP}/http.log"; then
	fail "the installer contacted the HTTP server before rejecting the URL"
fi
pass "an HTTP --base-url is rejected before download"

# Release mode download path. The fake curl resolves the fixture by basename.
make_release "${FAKE_RELEASES}" "${VERSION}"
invoke "${INSTALLER}" --dry-run --release "${VERSION}" \
	--base-url "https://example.invalid/releases/"
expect_success
expect_output "checksum verified"
grep -Fq "https://example.invalid/releases/devstack_${VERSION}_linux_amd64.tar.gz" "${CURL_LOG}" ||
	fail "the archive URL is wrong: $(cat "${CURL_LOG}")"
grep -Fq "https://example.invalid/releases/checksums.txt" "${CURL_LOG}" ||
	fail "the checksums URL is wrong: $(cat "${CURL_LOG}")"
grep -Fq -- "--proto =https" "${COMMAND_LOG}" ||
	fail "curl was not restricted to HTTPS: $(cat "${COMMAND_LOG}")"
pass "release mode downloads the archive and checksums over HTTPS"

# A corrupted archive must fail before any install or service command.
CORRUPT="${TMP}/corrupt"
mkdir -p "${CORRUPT}"
cp "${GOOD_ARCHIVE}" "${GOOD_CHECKSUMS}" "${CORRUPT}/"
printf 'corruption' >>"${CORRUPT}/$(basename "${GOOD_ARCHIVE}")"
invoke "${INSTALLER}" --dry-run --archive "${CORRUPT}/$(basename "${GOOD_ARCHIVE}")" \
	--checksums "${CORRUPT}/checksums.txt"
expect_failure
expect_output "checksum"
expect_no_privileged_commands
pass "a checksum mismatch aborts before any privileged command"

# Unsafe members: traversal, absolute, symlink, and hardlink.
for kind in traversal absolute symlink hardlink; do
	BAD="${TMP}/bad-${kind}"
	mkdir -p "${BAD}"
	make_bad_release "${BAD}" "${kind}"
	invoke "${INSTALLER}" --dry-run \
		--archive "${BAD}/devstack_0.0.0-bad_linux_amd64.tar.gz" \
		--checksums "${BAD}/checksums.txt"
	expect_failure
	expect_output "unsafe archive member"
	expect_no_privileged_commands
	pass "unsafe archive member rejected: ${kind}"
done

# A binary reporting the wrong version must be rejected even with a valid
# checksum for the archive that contains it.
WRONG="${TMP}/wrong-version"
mkdir -p "${WRONG}"
make_release "${WRONG}" "0.0.0-wrong" "9.9.9"
invoke "${INSTALLER}" --dry-run \
	--archive "${WRONG}/devstack_0.0.0-wrong_linux_amd64.tar.gz" \
	--checksums "${WRONG}/checksums.txt"
expect_failure
expect_output "reports version"
expect_no_privileged_commands
pass "a package whose devstack reports the wrong version is rejected"

# Option contract errors.
invoke "${INSTALLER}" --dry-run --archive "${GOOD_ARCHIVE}"
expect_failure
expect_output "must be provided together"

invoke "${INSTALLER}" --dry-run --release "${VERSION}" \
	--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
expect_failure
expect_output "cannot be combined"

invoke "${INSTALLER}" --dry-run --base-url "https://example.invalid/releases"
expect_failure
expect_output "requires --release"

invoke "${INSTALLER}" --dry-run --archive "${GOOD_ARCHIVE}" \
	--checksums "${GOOD_CHECKSUMS}" --upgrade
expect_failure
expect_output "requires an existing"

invoke "${INSTALLER}" --dry-run --keep-config --force-config
expect_failure
expect_output "cannot be used together"

BADNAME_DIR="${TMP}/bad-name"
mkdir -p "${BADNAME_DIR}"
cp "${GOOD_ARCHIVE}" "${BADNAME_DIR}/release.tar.gz"
invoke "${INSTALLER}" --dry-run --archive "${BADNAME_DIR}/release.tar.gz" \
	--checksums "${GOOD_CHECKSUMS}"
expect_failure
expect_output "must look like"
pass "invalid option combinations fail with clear messages"

# Upgrade dry run: an existing CLI and database exist, so the installer prints
# the stop, backup, and init plan without changing the sandbox.
mkdir -p "${FAKE_HOME}/.local/bin" "${FAKE_HOME}/.devstack"
cp "${GOOD}/stage/devstack" "${FAKE_HOME}/.local/bin/devstack"
chmod 0755 "${FAKE_HOME}/.local/bin/devstack"
printf 'placeholder database\n' >"${FAKE_HOME}/.devstack/state.db"
CLI_BEFORE="$(sha256sum "${FAKE_HOME}/.local/bin/devstack" | cut -d' ' -f1)"
export FAKE_UNIT_ACTIVE=1
invoke "${INSTALLER}" --dry-run --upgrade \
	--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
expect_success
expect_output "upgrade"
expect_output "systemctl --user stop devstack-daemon.service"
expect_output ".devstack/backups/state.db.pre-migrate-"
expect_output "devstack init"
CLI_AFTER="$(sha256sum "${FAKE_HOME}/.local/bin/devstack" | cut -d' ' -f1)"
[ "${CLI_BEFORE}" = "${CLI_AFTER}" ] || fail "upgrade dry run replaced the existing CLI"
if [ -e "${FAKE_ROOT}/usr/local/lib/devstack/devstack-helper" ]; then
	fail "upgrade dry run wrote the helper binary"
fi
pass "upgrade dry run prints the stop/backup/init plan without writing"
export FAKE_UNIT_ACTIVE=0

# A stop failure must abort before replacing binaries.
STOP_FAIL_BIN="${FAKE_HOME}/.local/bin/devstack"
cat >"${STOP_FAIL_BIN}" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = daemon ] && [ "${2:-}" = stop ]; then
	exit 1
fi
exit 0
EOF
chmod 0755 "${STOP_FAIL_BIN}"
export FAKE_SYSTEMD_SESSION=0
invoke "${INSTALLER}" --upgrade \
	--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
expect_failure
expect_output "could not stop"
expect_no_sudo_install
if [ -e "${FAKE_ROOT}/usr/local/lib/devstack/devstack-helper" ]; then
	fail "a failed daemon stop still replaced the helper binary"
fi
export FAKE_SYSTEMD_SESSION=1
pass "a failed daemon stop aborts the upgrade before binary replacement"

# A denied sudo must abort before the daemon is stopped or a binary replaced.
export FAKE_SUDO_DENY=1
export FAKE_UNIT_ACTIVE=1
export FAKE_SYSTEMD_SESSION=1
CLI_BEFORE="$(sha256sum "${FAKE_HOME}/.local/bin/devstack" | cut -d' ' -f1)"
invoke "${INSTALLER}" --upgrade \
	--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
expect_failure
expect_output "sudo authentication failed"
expect_no_service_stop
expect_no_sudo_install
CLI_AFTER="$(sha256sum "${FAKE_HOME}/.local/bin/devstack" | cut -d' ' -f1)"
[ "${CLI_BEFORE}" = "${CLI_AFTER}" ] || fail "denied sudo replaced the existing CLI"
if [ -e "${FAKE_ROOT}/usr/local/lib/devstack/devstack-helper" ]; then
	fail "denied sudo installed the helper binary"
fi
export FAKE_SUDO_DENY=0
export FAKE_UNIT_ACTIVE=0
pass "a denied sudo aborts before stopping services or replacing binaries"

# Packaged install.sh: run from a directory without build/systemd so the
# installer must use its embedded unit definitions. The fake sudo keeps every
# root-owned write inside FAKE_ROOT.
if [ ! -d /run/systemd/system ]; then
	skip "sandboxed real install needs systemd on the host for the installer preflight"
elif ! command -v install >/dev/null 2>&1; then
	skip "sandboxed real install needs the install command"
else
	STANDALONE="${TMP}/standalone"
	mkdir -p "${STANDALONE}"
	cp "${INSTALLER}" "${STANDALONE}/install.sh"
	chmod 0755 "${STANDALONE}/install.sh"
	rm -rf "${FAKE_HOME}/.local/bin" "${FAKE_HOME}/.config" "${FAKE_ROOT:?}/"*
	mkdir -p "${FAKE_ROOT}"
	export FAKE_UNIT_ACTIVE=0
	export FAKE_SYSTEMD_SESSION=1
	invoke "${STANDALONE}/install.sh" --force-config \
		--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
	expect_success
	[ -x "${FAKE_HOME}/.local/bin/devstack" ] || fail "the CLI was not installed"
	[ -x "${FAKE_ROOT}/usr/local/lib/devstack/devstack-helper" ] ||
		fail "the helper was not installed"
	[ -f "${FAKE_ROOT}/etc/devstack/helper.yaml" ] || fail "the helper config was not written"
	grep -Fq "allow_uid: ${TEST_UID}" "${FAKE_ROOT}/etc/devstack/helper.yaml" ||
		fail "the helper config does not record the installing UID"
	HELPER_UNIT_FILE="${FAKE_ROOT}/etc/systemd/system/devstack-helper.service"
	DAEMON_UNIT_FILE="${FAKE_HOME}/.config/systemd/user/devstack-daemon.service"
	[ -f "${HELPER_UNIT_FILE}" ] || fail "the helper unit was not installed"
	[ -f "${DAEMON_UNIT_FILE}" ] || fail "the daemon unit was not installed"
	grep -Fq -- "ReadWritePaths=/etc/hosts /etc/devstack /var/lib/devstack-helper /run/devstack-helper /usr/local/bin -\"${FAKE_HOME}/.devstack\"" \
		"${HELPER_UNIT_FILE}" ||
		fail "the generated helper unit cannot write ${FAKE_HOME}/.devstack: $(cat "${HELPER_UNIT_FILE}")"
	grep -Fq -- "ExecStart=%h/.local/bin/devstack daemon run" "${DAEMON_UNIT_FILE}" ||
		fail "the generated daemon unit has the wrong ExecStart"
	[ -d "${FAKE_HOME}/.devstack" ] || fail "the installer did not create the devstack home"
	: >"${FAKE_HOME}/.devstack/.write-test" ||
		fail "the devstack home is not writable by the installing user"
	rm -f "${FAKE_HOME}/.devstack/.write-test"
	if command -v systemd-analyze >/dev/null 2>&1; then
		# Verify syntax on copies with ExecStart replaced by /bin/true: the
		# sandbox has no real devstack binary at the production path, and the
		# generated ExecStart is asserted separately above.
		sed 's#^ExecStart=.*#ExecStart=/bin/true#' "${HELPER_UNIT_FILE}" >"${TMP}/helper.verify.service"
		if ! verify_out="$(systemd-analyze verify "${TMP}/helper.verify.service" 2>&1)"; then
			fail "the generated helper unit does not parse: ${verify_out}"
		fi
		sed 's#^ExecStart=.*#ExecStart=/bin/true#' "${DAEMON_UNIT_FILE}" >"${TMP}/daemon.verify.service"
		if ! verify_out="$(systemd-analyze verify "${TMP}/daemon.verify.service" 2>&1)"; then
			fail "the generated daemon unit does not parse: ${verify_out}"
		fi
	else
		log "systemd-analyze is unavailable; unit syntax parse check skipped"
	fi
	pass "packaged install.sh installs binaries/config and a parseable unit for the real home"

	# -------------------------------------------------------------------------
	# Fresh install: no legacy install exists (~/.devstack or /etc/devstack/helper.yaml),
	# no explicit DEVSTACK_HOME or --home provided. Must default to /data/devstack.
	# -------------------------------------------------------------------------
	rm -rf "${FAKE_HOME}/.devstack" "${FAKE_HOME}/.local/bin" "${FAKE_HOME}/.config" "${FAKE_ROOT:?}/"*
	mkdir -p "${FAKE_ROOT}"
	export UNSET_DEVSTACK_HOME=1
	export FAKE_UNIT_ACTIVE=0
	export FAKE_SYSTEMD_SESSION=1
	invoke "${STANDALONE}/install.sh" --force-config \
		--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
	expect_success
	[ -x "${FAKE_HOME}/.local/bin/devstack" ] || fail "fresh install did not install the CLI"
	[ -x "${FAKE_ROOT}/usr/local/lib/devstack/devstack-helper" ] ||
		fail "fresh install did not install the helper binary"
	[ -d "${FAKE_ROOT}/data/devstack" ] || fail "fresh install did not create /data/devstack"
	[ -d "${FAKE_ROOT}/data/devstack/bin" ] || fail "fresh install did not create /data/devstack/bin"
	[ -d "${FAKE_ROOT}/data/devstack/system" ] || fail "fresh install did not create /data/devstack/system"
	[ -f "${FAKE_ROOT}/data/devstack/system/helper.yaml" ] ||
		fail "fresh install did not create /data/devstack/system/helper.yaml"
	grep -Fq "allow_uid: ${TEST_UID}" "${FAKE_ROOT}/data/devstack/system/helper.yaml" ||
		fail "fresh install helper config does not record the installing UID"
	grep -Fq 'service_root: "/data/devstack"' "${FAKE_ROOT}/data/devstack/system/helper.yaml" ||
		fail "fresh install helper config does not record service_root: /data/devstack"
	HELPER_UNIT_FILE="${FAKE_ROOT}/etc/systemd/system/devstack-helper.service"
	DAEMON_UNIT_FILE="${FAKE_HOME}/.config/systemd/user/devstack-daemon.service"
	[ -f "${HELPER_UNIT_FILE}" ] || fail "fresh install helper unit was not installed"
	[ -f "${DAEMON_UNIT_FILE}" ] || fail "fresh install daemon unit was not installed"
	grep -Fq -- "ExecStart=/usr/local/lib/devstack/devstack-helper --config /data/devstack/system/helper.yaml" \
		"${HELPER_UNIT_FILE}" ||
		fail "fresh install helper unit does not point to /data/devstack/system/helper.yaml: $(cat "${HELPER_UNIT_FILE}")"
	grep -Fq -- "-/data/devstack/system" "${HELPER_UNIT_FILE}" ||
		fail "fresh install helper unit ReadWritePaths does not include -/data/devstack/system: $(cat "${HELPER_UNIT_FILE}")"
	grep -Fq -- "Environment=DEVSTACK_HOME=/data/devstack" "${DAEMON_UNIT_FILE}" ||
		fail "fresh install daemon unit does not set DEVSTACK_HOME=/data/devstack: $(cat "${DAEMON_UNIT_FILE}")"
	[ ! -e "${FAKE_ROOT}/etc/devstack/helper.yaml" ] ||
		fail "fresh install should not create /etc/devstack/helper.yaml"
	[ ! -e "${FAKE_HOME}/.devstack" ] ||
		fail "fresh install should not create ~/.devstack"
	pass "fresh install uses /data/devstack layout and /data/devstack/system/helper.yaml"

	# -------------------------------------------------------------------------
	# Upgrade of existing legacy install (~/.devstack & /etc/devstack/helper.yaml):
	# Must preserve legacy ~/.devstack and /etc/devstack paths.
	# -------------------------------------------------------------------------
	rm -rf "${FAKE_HOME}/.devstack" "${FAKE_HOME}/.local/bin" "${FAKE_HOME}/.config" "${FAKE_ROOT:?}/"*
	mkdir -p "${FAKE_ROOT}" "${FAKE_HOME}/.devstack" "${FAKE_ROOT}/etc/devstack"
	# Pre-populate legacy helper config (simulating v0.1.0 without service_root)
	cat >"${FAKE_ROOT}/etc/devstack/helper.yaml" <<EOF
socket: /run/devstack-helper/helper.sock
state_dir: /var/lib/devstack-helper
hosts: /etc/hosts
allow_uid: ${TEST_UID}
forward_http_port: 80
forward_https_port: 443
proxy_http_port: 8080
proxy_https_port: 8443
EOF
	# Pre-populate legacy devstack CLI & state.db
	mkdir -p "${FAKE_HOME}/.local/bin"
	cp "${GOOD}/stage/devstack" "${FAKE_HOME}/.local/bin/devstack"
	printf 'legacy db\n' >"${FAKE_HOME}/.devstack/state.db"
	export UNSET_DEVSTACK_HOME=1
	export FAKE_UNIT_ACTIVE=0
	export FAKE_SYSTEMD_SESSION=1
	invoke "${STANDALONE}/install.sh" --upgrade \
		--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
	expect_success
	[ -f "${FAKE_ROOT}/etc/devstack/helper.yaml" ] ||
		fail "upgrade did not preserve /etc/devstack/helper.yaml"
	grep -Fq 'service_root: "'"${FAKE_HOME}"'/.devstack"' "${FAKE_ROOT}/etc/devstack/helper.yaml" ||
		fail "upgrade did not append service_root for legacy install: $(cat "${FAKE_ROOT}/etc/devstack/helper.yaml")"
	[ ! -e "${FAKE_ROOT}/data/devstack/system/helper.yaml" ] ||
		fail "upgrade should not create /data/devstack/system/helper.yaml for legacy install"
	HELPER_UNIT_FILE="${FAKE_ROOT}/etc/systemd/system/devstack-helper.service"
	DAEMON_UNIT_FILE="${FAKE_HOME}/.config/systemd/user/devstack-daemon.service"
	grep -Fq -- "ExecStart=/usr/local/lib/devstack/devstack-helper --config /etc/devstack/helper.yaml" \
		"${HELPER_UNIT_FILE}" ||
		fail "upgrade helper unit does not point to legacy /etc/devstack/helper.yaml: $(cat "${HELPER_UNIT_FILE}")"
	grep -Fq -- "Environment=DEVSTACK_HOME=${FAKE_HOME}/.devstack" "${DAEMON_UNIT_FILE}" ||
		fail "upgrade daemon unit does not preserve DEVSTACK_HOME=~/.devstack: $(cat "${DAEMON_UNIT_FILE}")"
	pass "upgrade of legacy install preserves ~/.devstack and /etc/devstack paths"

	# -------------------------------------------------------------------------
	# Explicit --home flag: must override default /data/devstack and legacy ~/.devstack
	# -------------------------------------------------------------------------
	CUSTOM_HOME="${TMP}/custom-home"
	rm -rf "${CUSTOM_HOME}" "${FAKE_HOME}/.devstack" "${FAKE_ROOT:?}/"*
	mkdir -p "${FAKE_ROOT}"
	export UNSET_DEVSTACK_HOME=1
	invoke "${STANDALONE}/install.sh" --home "${CUSTOM_HOME}" --force-config \
		--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
	expect_success
	[ -d "${CUSTOM_HOME}" ] || fail "explicit --home was not created: ${CUSTOM_HOME}"
	[ -f "${CUSTOM_HOME}/system/helper.yaml" ] ||
		fail "helper config was not created at ${CUSTOM_HOME}/system/helper.yaml"
	DAEMON_UNIT_FILE="${FAKE_HOME}/.config/systemd/user/devstack-daemon.service"
	grep -Fq -- "Environment=DEVSTACK_HOME=${CUSTOM_HOME}" "${DAEMON_UNIT_FILE}" ||
		fail "daemon unit did not set DEVSTACK_HOME to explicit --home: $(cat "${DAEMON_UNIT_FILE}")"
	pass "explicit --home flag wins and creates custom home layout"

	# -------------------------------------------------------------------------
	# Legacy detected via /etc/devstack/helper.yaml without ~/.devstack:
	# Must preserve legacy /etc/devstack and ~/.devstack layout.
	# -------------------------------------------------------------------------
	rm -rf "${FAKE_HOME}/.devstack" "${FAKE_HOME}/.local/bin" "${FAKE_HOME}/.config" "${FAKE_ROOT:?}/"*
	mkdir -p "${FAKE_ROOT}/etc/devstack"
	cat >"${FAKE_ROOT}/etc/devstack/helper.yaml" <<EOF
socket: /run/devstack-helper/helper.sock
state_dir: /var/lib/devstack-helper
hosts: /etc/hosts
allow_uid: ${TEST_UID}
EOF
	export UNSET_DEVSTACK_HOME=1
	invoke "${STANDALONE}/install.sh" --keep-config \
		--archive "${GOOD_ARCHIVE}" --checksums "${GOOD_CHECKSUMS}"
	expect_success
	[ -f "${FAKE_ROOT}/etc/devstack/helper.yaml" ] ||
		fail "legacy install should keep /etc/devstack/helper.yaml"
	[ ! -e "${FAKE_ROOT}/data/devstack/system/helper.yaml" ] ||
		fail "legacy install should not create /data/devstack/system/helper.yaml"
	HELPER_UNIT_FILE="${FAKE_ROOT}/etc/systemd/system/devstack-helper.service"
	DAEMON_UNIT_FILE="${FAKE_HOME}/.config/systemd/user/devstack-daemon.service"
	grep -Fq -- "ExecStart=/usr/local/lib/devstack/devstack-helper --config /etc/devstack/helper.yaml" \
		"${HELPER_UNIT_FILE}" ||
		fail "helper unit does not point to /etc/devstack/helper.yaml: $(cat "${HELPER_UNIT_FILE}")"
	grep -Fq -- "Environment=DEVSTACK_HOME=${FAKE_HOME}/.devstack" "${DAEMON_UNIT_FILE}" ||
		fail "daemon unit does not set DEVSTACK_HOME=~/.devstack: $(cat "${DAEMON_UNIT_FILE}")"
	pass "legacy install detected via /etc/devstack/helper.yaml preserves legacy paths"
	export UNSET_DEVSTACK_HOME=0
fi
export FAKE_UNIT_ACTIVE=0
export FAKE_SYSTEMD_SESSION=1

log "all installer checks passed"
