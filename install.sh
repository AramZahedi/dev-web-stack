#!/usr/bin/env bash
# Install devstack on a systemd-based Linux machine.
#
# The script runs as the normal user and uses sudo only for the root-owned
# helper binary, helper configuration, and system unit. It does not install
# packages, enable nginx/Apache, or run devstack init.
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HELPER_ROOT="/usr/local/lib/devstack"
HELPER_BINARY="${HELPER_ROOT}/devstack-helper"
HELPER_CONFIG_DIR="/etc/devstack"
HELPER_CONFIG="${HELPER_CONFIG_DIR}/helper.yaml"
HELPER_UNIT="/etc/systemd/system/devstack-helper.service"
USER_UNIT_DIR=""
USER_UNIT=""
DRY_RUN=0
CONFIG_MODE="keep"
UPGRADE=0
SOURCE_MODE="source"
RELEASE_VERSION=""
ARCHIVE_PATH=""
CHECKSUMS_PATH=""
BASE_URL=""
RELEASE_SEEN=0
ARCHIVE_SEEN=0
CHECKSUMS_SEEN=0
BASE_URL_SEEN=0
FORCE_SEEN=0
KEEP_SEEN=0
TMP_DIR=""
BINARY_DIR=""
UNIT_DIR=""
BACKUP_HINT=""

usage() {
	cat <<'EOF'
Usage: install.sh [options]

Install devstack from the source tree (default), a local release archive, or
an HTTPS release download. The installer must run as the user who will own
devstack, not as root.

Source:
  --dry-run                 Verify and print the install steps without writing.
  --upgrade                 Stop the existing daemon, keep its config, and
                            replace the binaries. Requires an existing CLI.
  --keep-config             Keep an existing /etc/devstack/helper.yaml (default).
  --force-config            Replace /etc/devstack/helper.yaml with defaults.

Release:
  --release <version>       Download devstack_<version>_linux_amd64.tar.gz and
                            checksums.txt from the GitHub release for v<version>.
  --archive <path>          Install from a local release archive (requires
                            --checksums).
  --checksums <path>        SHA-256 manifest for --archive.
  --base-url <https-url>    Override the release root; HTTPS only.
  -h, --help                Show this help.

Build from source first with `make build` (or `make e2e` for the test build).
Release mode verifies the archive checksum, rejects unsafe members, and checks
the packaged version before any install command. None of these modes run
`devstack init`; run it afterwards to sync hosts, forwarders, and trust.
EOF
}

log() {
	printf 'install: %s\n' "$*"
}

die() {
	printf 'install: error: %s\n' "$*" >&2
	exit 1
}

# run prints the command in dry-run mode and executes it otherwise.
run() {
	if [ "${DRY_RUN}" -eq 1 ]; then
		printf '  '
		printf '%q ' "$@"
		printf '\n'
		return 0
	fi
	"$@"
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--dry-run)
		DRY_RUN=1
		shift
		;;
	--release)
		[ "$#" -ge 2 ] || die "--release requires a version"
		RELEASE_VERSION="$2"
		RELEASE_SEEN=1
		shift 2
		;;
	--archive)
		[ "$#" -ge 2 ] || die "--archive requires a path"
		ARCHIVE_PATH="$2"
		ARCHIVE_SEEN=1
		shift 2
		;;
	--checksums)
		[ "$#" -ge 2 ] || die "--checksums requires a path"
		CHECKSUMS_PATH="$2"
		CHECKSUMS_SEEN=1
		shift 2
		;;
	--base-url)
		[ "$#" -ge 2 ] || die "--base-url requires a URL"
		BASE_URL="$2"
		BASE_URL_SEEN=1
		shift 2
		;;
	--upgrade)
		UPGRADE=1
		shift
		;;
	--keep-config)
		CONFIG_MODE="keep"
		KEEP_SEEN=1
		shift
		;;
	--force-config)
		CONFIG_MODE="force"
		FORCE_SEEN=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'install: unknown option %s\n\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
done

if [ "${RELEASE_SEEN}" -eq 1 ] && { [ "${ARCHIVE_SEEN}" -eq 1 ] || [ "${CHECKSUMS_SEEN}" -eq 1 ]; }; then
	die "--release cannot be combined with --archive or --checksums"
fi
if [ "${ARCHIVE_SEEN}" -ne "${CHECKSUMS_SEEN}" ]; then
	die "--archive and --checksums must be provided together"
fi
if [ "${BASE_URL_SEEN}" -eq 1 ] && [ "${RELEASE_SEEN}" -eq 0 ]; then
	die "--base-url requires --release"
fi
if [ "${RELEASE_SEEN}" -eq 1 ]; then
	SOURCE_MODE="release"
elif [ "${ARCHIVE_SEEN}" -eq 1 ]; then
	SOURCE_MODE="archive"
fi

# --force-config and --keep-config are mutually exclusive only when both were
# supplied.
if [ "${FORCE_SEEN}" -eq 1 ] && [ "${KEEP_SEEN}" -eq 1 ]; then
	die "--force-config and --keep-config cannot be used together"
fi

if [ "${RELEASE_SEEN}" -eq 1 ]; then
	RELEASE_VERSION="${RELEASE_VERSION#v}"
	if [[ ! "${RELEASE_VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
		die "invalid release version ${RELEASE_VERSION@Q}: expected <major>.<minor>.<patch> with an optional prerelease suffix"
	fi
fi

if [ "${BASE_URL_SEEN}" -eq 1 ]; then
	case "${BASE_URL}" in
	https://?*) ;;
	*) die "--base-url must use HTTPS; got ${BASE_URL@Q}" ;;
	esac
	case "${BASE_URL}" in
	*[[:space:]]*) die "--base-url must not contain whitespace" ;;
	esac
	BASE_URL="${BASE_URL%/}"
fi

if [ "$(uname -s)" != "Linux" ]; then
	die "devstack installer supports Linux only"
fi
if [ ! -d /run/systemd/system ]; then
	if [ "${DRY_RUN}" -eq 1 ]; then
		log "warning: systemd is not running; showing a dry run only"
	else
		die "systemd is required (PID 1 does not expose /run/systemd/system)"
	fi
fi
if [ "$(id -u)" -eq 0 ]; then
	die "run install.sh as your normal user; it uses sudo for system changes and records your UID in the helper config"
fi

for command in id getent install sudo systemctl; do
	if ! command -v "${command}" >/dev/null 2>&1; then
		die "required command not found: ${command}"
	fi
done
if [ "${SOURCE_MODE}" != "source" ]; then
	if ! command -v tar >/dev/null 2>&1; then
		die "required command not found: tar"
	fi
	if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
		die "required command not found: sha256sum or shasum"
	fi
fi
if [ "${SOURCE_MODE}" = "release" ]; then
	if ! command -v curl >/dev/null 2>&1; then
		die "required command not found: curl"
	fi
fi

USER_NAME="$(id -un)"
USER_UID="$(id -u)"
USER_HOME="$(getent passwd "${USER_UID}" | cut -d: -f6)"
if [ -z "${USER_HOME}" ] || [ ! -d "${USER_HOME}" ]; then
	die "cannot determine the home directory for UID ${USER_UID}"
fi

CONFIG_HOME="${XDG_CONFIG_HOME:-${USER_HOME}/.config}"
case "${CONFIG_HOME}" in
/*) ;;
*) die "XDG_CONFIG_HOME must be an absolute path, got ${CONFIG_HOME}" ;;
esac

DEVSTACK_HOME_DIR="${DEVSTACK_HOME:-${USER_HOME}/.devstack}"
case "${DEVSTACK_HOME_DIR}" in
/*) ;;
*) die "DEVSTACK_HOME must be an absolute path, got ${DEVSTACK_HOME_DIR}" ;;
esac

USER_UNIT_DIR="${CONFIG_HOME}/systemd/user"
USER_UNIT="${USER_UNIT_DIR}/devstack-daemon.service"
USER_BIN="${USER_HOME}/.local/bin/devstack"

if [ "${UPGRADE}" -eq 1 ] && [ ! -x "${USER_BIN}" ]; then
	die "--upgrade requires an existing executable ${USER_BIN}; run a normal install first"
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/devstack-install.XXXXXX")"
cleanup() {
	rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

# absolute_file resolves a local input and rejects symlinks and non-files.
absolute_file() {
	local path="$1" dir
	if [ ! -e "${path}" ]; then
		die "file not found: ${path}"
	fi
	if [ -L "${path}" ]; then
		die "refusing symlink input: ${path}"
	fi
	if [ ! -f "${path}" ]; then
		die "not a regular file: ${path}"
	fi
	dir="$(cd -- "$(dirname -- "${path}")" && pwd)"
	printf '%s/%s\n' "${dir}" "$(basename -- "${path}")"
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# verify_checksum requires exactly one lowercase SHA-256 line for the archive
# basename. Other manifest lines are ignored.
verify_checksum() {
	local archive="$1" checksums="$2" name="$3"
	local line hash rest found=0 expected="" actual=""
	while IFS= read -r line || [ -n "${line}" ]; do
		[ -n "${line}" ] || continue
		hash="${line%%[[:space:]]*}"
		rest="${line#"${hash}"}"
		rest="${rest#"${rest%%[![:space:]]*}"}"
		rest="${rest#\*}"
		if [ "${rest}" = "${name}" ]; then
			if [[ ! "${hash}" =~ ^[0-9a-f]{64}$ ]]; then
				die "checksums file has an invalid SHA-256 for ${name}"
			fi
			found=$((found + 1))
			expected="${hash}"
		fi
	done <"${checksums}"
	if [ "${found}" -ne 1 ]; then
		die "checksums file must contain exactly one entry for ${name} (found ${found})"
	fi
	actual="$(sha256_of "${archive}")"
	if [ "${actual}" != "${expected}" ]; then
		die "checksum mismatch for ${name}: expected ${expected}, got ${actual}"
	fi
}

# validate_archive lists the archive before extraction and rejects anything
# that could escape the extraction directory or change file types. The release
# contract contains regular files only.
validate_archive() {
	local archive="$1"
	local list_file="${TMP_DIR}/members.txt"
	local verbose_file="${TMP_DIR}/members.verbose.txt"
	local name line type part
	local -a members=()
	local -a verbose=()
	local -a parts=()
	local -A seen=()

	if ! tar -tzf "${archive}" >"${list_file}" 2>/dev/null; then
		die "cannot list archive ${archive}"
	fi
	if ! tar -tvzf "${archive}" >"${verbose_file}" 2>/dev/null; then
		die "cannot inspect archive types in ${archive}"
	fi
	mapfile -t members <"${list_file}"
	mapfile -t verbose <"${verbose_file}"
	if [ "${#members[@]}" -eq 0 ]; then
		die "archive ${archive} is empty"
	fi
	if [ "${#members[@]}" -ne "${#verbose[@]}" ]; then
		die "cannot reliably parse the member list of ${archive}"
	fi

	for line in "${verbose[@]}"; do
		type="${line:0:1}"
		if [ "${type}" != "-" ]; then
			die "unsafe archive member: ${line#* } is not a regular file"
		fi
	done

	for name in "${members[@]}"; do
		if [ -z "${name}" ]; then
			die "unsafe archive member: empty name"
		fi
		case "${name}" in
		/*) die "unsafe archive member ${name@Q}: absolute path" ;;
		-*) die "unsafe archive member ${name@Q}: leading dash" ;;
		*[[:cntrl:]]*) die "unsafe archive member: control character in name" ;;
		esac
		IFS='/' read -r -a parts <<<"${name}"
		for part in "${parts[@]}"; do
			if [ "${part}" = ".." ]; then
				die "unsafe archive member ${name@Q}: parent directory component"
			fi
		done
		case "${name}" in
		devstack | devstack-helper | install.sh | README.md | LICENSE) ;;
		*) die "unexpected archive member ${name@Q}" ;;
		esac
		if [ -n "${seen[${name}]+x}" ]; then
			die "duplicate archive member ${name@Q}"
		fi
		seen[${name}]=1
	done

	for name in devstack devstack-helper install.sh README.md; do
		if [ -z "${seen[${name}]+x}" ]; then
			die "archive is missing required member ${name}"
		fi
	done
}

# resolve_release verifies a local archive or downloads one over HTTPS, then
# verifies the checksum, member names/types, extraction, and reported version.
resolve_release() {
	if [ "${SOURCE_MODE}" = "release" ]; then
		if [ -z "${BASE_URL}" ]; then
			BASE_URL="https://github.com/AramZahedi/dev-web-stack/releases/download/v${RELEASE_VERSION}"
		fi
		ARCHIVE_PATH="${TMP_DIR}/${ARCHIVE_NAME}"
		CHECKSUMS_PATH="${TMP_DIR}/checksums.txt"
		log "downloading ${BASE_URL}/${ARCHIVE_NAME}"
		if ! curl -fL --proto '=https' --tlsv1.2 --retry 3 --connect-timeout 10 \
			--silent --show-error --output "${ARCHIVE_PATH}" \
			"${BASE_URL}/${ARCHIVE_NAME}"; then
			die "download failed: ${BASE_URL}/${ARCHIVE_NAME}"
		fi
		if ! curl -fL --proto '=https' --tlsv1.2 --retry 3 --connect-timeout 10 \
			--silent --show-error --output "${CHECKSUMS_PATH}" \
			"${BASE_URL}/checksums.txt"; then
			die "download failed: ${BASE_URL}/checksums.txt"
		fi
	else
		ARCHIVE_PATH="$(absolute_file "${ARCHIVE_PATH}")"
		CHECKSUMS_PATH="$(absolute_file "${CHECKSUMS_PATH}")"
	fi

	verify_checksum "${ARCHIVE_PATH}" "${CHECKSUMS_PATH}" "${ARCHIVE_NAME}"
	log "checksum verified for ${ARCHIVE_NAME}"

	validate_archive "${ARCHIVE_PATH}"
	log "archive members verified; extracting to a temporary directory"

	EXTRACT_DIR="${TMP_DIR}/extract"
	mkdir -m 0700 "${EXTRACT_DIR}"
	if ! tar -xzf "${ARCHIVE_PATH}" --no-same-owner --no-same-permissions \
		-C "${EXTRACT_DIR}"; then
		die "cannot extract ${ARCHIVE_NAME}"
	fi
	for member in devstack devstack-helper install.sh README.md; do
		if [ -L "${EXTRACT_DIR}/${member}" ] || [ ! -f "${EXTRACT_DIR}/${member}" ]; then
			die "archive extraction did not produce a regular ${member}"
		fi
	done
	for binary in devstack devstack-helper; do
		if [ ! -x "${EXTRACT_DIR}/${binary}" ]; then
			die "extracted ${binary} is not executable"
		fi
	done

	if [ "${RELEASE_VERSION}" != "dev" ]; then
		if ! reported_version="$("${EXTRACT_DIR}/devstack" version --json 2>/dev/null)"; then
			die "extracted devstack cannot report its version"
		fi
		case "${reported_version}" in
		*"\"version\":\"${RELEASE_VERSION}\""*) ;;
		*) die "extracted devstack reports version ${reported_version}; expected ${RELEASE_VERSION}" ;;
		esac
	fi

	BINARY_DIR="${EXTRACT_DIR}"
}

# resolve_units renders both units from the actual install context. The
# checked-in build/systemd files remain references, but source and archive
# installs must not silently use a unit with the wrong devstack home.
resolve_units() {
	UNIT_DIR="${TMP_DIR}/systemd"
	mkdir -p "${UNIT_DIR}"
	# Keep these definitions in sync with build/systemd/devstack-*.service.
	cat >"${UNIT_DIR}/devstack-helper.service" <<EOF
[Unit]
Description=devstack privileged helper
Documentation=https://github.com/AramZahedi/dev-web-stack/blob/master/docs/install.md
After=network.target

[Service]
Type=simple
User=root
Group=root
ExecStartPre=-/usr/bin/mkdir -p /usr/local/share/ca-certificates
ExecStartPre=-/usr/bin/mkdir -p /var/lib/devstack-helper
ExecStart=/usr/local/lib/devstack/devstack-helper --config /etc/devstack/helper.yaml
Restart=on-failure
RestartSec=2s
TimeoutStopSec=20s
UMask=0077
LimitNOFILE=65536
NoNewPrivileges=yes
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_SETUID CAP_SETGID CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER
AmbientCapabilities=
PrivateTmp=yes
ProtectSystem=strict
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
LockPersonality=yes
StateDirectory=devstack-helper
StateDirectoryMode=0700
RuntimeDirectory=devstack-helper
RuntimeDirectoryMode=0755
# Keep the writable surface narrow. Atomic hosts updates need the hosts file
# writable; the helper falls back to an in-place write if the parent /etc mount
# is read-only. Distribution trust paths are optional and differ between
# Fedora/RHEL and Debian/Ubuntu.
ReadWritePaths=/etc/hosts /etc/devstack /var/lib/devstack-helper /run/devstack-helper /usr/local/bin -"${DEVSTACK_HOME_DIR}"
# External mode writes exactly one bridge file into the server's include
# directory. These paths are optional because the selected server (or a
# custom include layout) may not exist on this machine.
ReadWritePaths=-/etc/nginx/conf.d
ReadWritePaths=-/etc/apache2/conf-enabled
ReadWritePaths=-/etc/httpd/conf.d
ReadWritePaths=-/etc/pki/ca-trust/source/anchors
ReadWritePaths=-/etc/pki/ca-trust/source
ReadWritePaths=-/etc/pki/ca-trust/extracted
ReadWritePaths=-/usr/local/share/ca-certificates
ReadWritePaths=-/etc/ssl/certs
ReadWritePaths=-/var/lib/ca-certificates

[Install]
WantedBy=multi-user.target
EOF
	cat >"${UNIT_DIR}/devstack-daemon.service" <<'EOF'
[Unit]
Description=devstack user daemon
Documentation=https://github.com/AramZahedi/dev-web-stack/blob/master/docs/install.md
After=network.target

[Service]
Type=simple
Environment=HOME=%h
Environment=DEVSTACK_HOME=%h/.devstack
ExecStart=%h/.local/bin/devstack daemon run
Restart=on-failure
RestartSec=2s
TimeoutStopSec=30s
KillMode=mixed
NoNewPrivileges=yes
LimitNOFILE=65536

[Install]
WantedBy=default.target
EOF
}

if [ "${SOURCE_MODE}" = "source" ]; then
	BINARY_DIR="${REPO_ROOT}/dist"
	for binary in "${BINARY_DIR}/devstack" "${BINARY_DIR}/devstack-helper"; do
		if [ ! -x "${binary}" ]; then
			die "missing ${binary}; run 'make build' first"
		fi
	done
else
	case "$(uname -m)" in
	x86_64 | amd64) ;;
	*) die "release archives are linux/amd64 only; this machine reports $(uname -m)" ;;
	esac
	if [ "${SOURCE_MODE}" = "release" ]; then
		ARCHIVE_NAME="devstack_${RELEASE_VERSION}_linux_amd64.tar.gz"
	else
		ARCHIVE_NAME="$(basename -- "${ARCHIVE_PATH}")"
		if [[ "${ARCHIVE_NAME}" =~ ^devstack_([0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?)_linux_amd64\.tar\.gz$ ]]; then
			RELEASE_VERSION="${BASH_REMATCH[1]}"
		else
			die "archive name ${ARCHIVE_NAME@Q} must look like devstack_<version>_linux_amd64.tar.gz"
		fi
	fi
	resolve_release
fi
resolve_units

# The helper's ReadWritePaths names the devstack home as optional, and both the
# helper and daemon need it. Create it as the installing user before any
# privileged install or service restart.
run install -d -m 0755 "${DEVSTACK_HOME_DIR}"

log "installing for ${USER_NAME} (uid ${USER_UID}, home ${USER_HOME})"
if [ "${DRY_RUN}" -eq 1 ]; then
	log "dry run: no files or services will be changed"
fi

STATE_DB="${DEVSTACK_HOME_DIR}/state.db"
if [ "${UPGRADE}" -eq 1 ] && [ -f "${STATE_DB}" ]; then
	BACKUP_HINT="${DEVSTACK_HOME_DIR}/backups/$(basename -- "${STATE_DB}").pre-migrate-<UTC>.bak"
fi

# Read-only daemon state is needed both for an upgrade stop and for deciding
# whether the user unit should be restarted after installing.
USER_SYSTEMD=0
UNIT_ACTIVE=0
if systemctl --user show-environment >/dev/null 2>&1; then
	USER_SYSTEMD=1
	if systemctl --user is-active --quiet devstack-daemon.service 2>/dev/null; then
		UNIT_ACTIVE=1
	fi
fi

if [ "${DRY_RUN}" -eq 0 ]; then
	if ! command -v sudo >/dev/null 2>&1; then
		die "sudo is required to install the privileged helper"
	fi
	# Validate elevation before stopping the daemon or replacing binaries. A
	# cancelled sudo must leave the existing installation untouched.
	if ! sudo -v; then
		die "sudo authentication failed; nothing was changed"
	fi
fi

if [ "${UPGRADE}" -eq 1 ]; then
	log "upgrade: keeping ${HELPER_CONFIG} unless --force-config is used"
	if [ -n "${BACKUP_HINT}" ]; then
		log "upgrade: a pre-migration backup will be written to ${BACKUP_HINT} if a schema migration is pending"
	else
		log "upgrade: no existing database at ${STATE_DB}; no pre-migration backup is needed"
	fi
fi

# Stop the daemon before replacing binaries. An upgrade aborts if the daemon
# cannot be stopped; a normal reinstall warns and continues, matching the
# existing non-destructive reinstall behavior.
if [ "${USER_SYSTEMD}" -eq 1 ] && [ "${UNIT_ACTIVE}" -eq 1 ]; then
	if ! run systemctl --user stop devstack-daemon.service; then
		if [ "${UPGRADE}" -eq 1 ]; then
			die "could not stop devstack-daemon.service; aborting before replacing binaries"
		fi
		log "warning: could not stop devstack-daemon.service; continuing with the reinstall"
	fi
elif [ -x "${USER_BIN}" ]; then
	if ! run "${USER_BIN}" daemon stop; then
		if [ "${UPGRADE}" -eq 1 ]; then
			die "could not stop the detached devstack daemon; aborting before replacing binaries"
		fi
		log "warning: could not stop the detached daemon; continuing with the reinstall"
	fi
fi

# User-facing CLI and daemon binary.
run install -d -m 0755 "${USER_HOME}/.local/bin"
run install -m 0755 "${BINARY_DIR}/devstack" "${USER_BIN}"

# Root-owned helper binary and configuration.
run sudo install -d -m 0755 "${HELPER_ROOT}"
run sudo install -m 0755 "${BINARY_DIR}/devstack-helper" "${HELPER_BINARY}"
run sudo install -d -m 0755 "${HELPER_CONFIG_DIR}"

if [ -e "${HELPER_CONFIG}" ] && [ "${CONFIG_MODE}" != "force" ]; then
	log "keeping existing ${HELPER_CONFIG} (use --force-config to replace it)"
	# Wave 1 added root/service/tool paths. Add only the missing keys so an
	# upgrade gains the service and tooling RPCs without discarding user edits.
	config_tmp="${TMP_DIR}/helper.yaml.existing"
	run sudo cat "${HELPER_CONFIG}" >"${config_tmp}"
	config_changed=0
	if ! grep -Eq '^[[:space:]]*service_root[[:space:]]*:' "${config_tmp}"; then
		printf 'service_root: "%s"\n' "${DEVSTACK_HOME_DIR}" >>"${config_tmp}"
		config_changed=1
	fi
	if ! grep -Eq '^[[:space:]]*tool_root[[:space:]]*:' "${config_tmp}"; then
		printf 'tool_root: "%s"\n' "${DEVSTACK_HOME_DIR}" >>"${config_tmp}"
		config_changed=1
	fi
	if ! grep -Eq '^[[:space:]]*tool_bin_dir[[:space:]]*:' "${config_tmp}"; then
		printf 'tool_bin_dir: /usr/local/bin\n' >>"${config_tmp}"
		config_changed=1
	fi
	if [ "${config_changed}" -eq 1 ]; then
		run sudo install -m 0644 "${config_tmp}" "${HELPER_CONFIG}"
		log "added missing service/tool paths to ${HELPER_CONFIG}"
	fi
else
	config_tmp="${TMP_DIR}/helper.yaml"
	cat >"${config_tmp}" <<EOF
# Managed by devstack install.sh. Edit and restart devstack-helper after
# changing ports or paths. Command-line flags override this file.
socket: /run/devstack-helper/helper.sock
state_dir: /var/lib/devstack-helper
hosts: /etc/hosts
trust_dir: ""
allow_uid: ${USER_UID}
forward_http_port: 80
forward_https_port: 443
proxy_http_port: 8080
proxy_https_port: 8443
auto_forward: true
service_root: "${DEVSTACK_HOME_DIR}"
tool_root: "${DEVSTACK_HOME_DIR}"
tool_bin_dir: /usr/local/bin
log_level: info
EOF
	run sudo install -m 0644 "${config_tmp}" "${HELPER_CONFIG}"
fi

# System helper unit. `enable` is idempotent; `restart` picks up a new binary
# and config on reinstall and starts the service on a first install.
run sudo install -m 0644 "${UNIT_DIR}/devstack-helper.service" "${HELPER_UNIT}"
run sudo systemctl daemon-reload
run sudo systemctl enable devstack-helper.service
if ! run sudo systemctl restart devstack-helper.service; then
	printf 'install: devstack-helper failed to start; inspect:\n' >&2
	printf '  sudo systemctl status devstack-helper --no-pager\n' >&2
	printf '  sudo journalctl -u devstack-helper -n 100 --no-pager\n' >&2
	exit 1
fi

if [ "${DRY_RUN}" -eq 0 ]; then
	for ((i = 0; i < 50; i++)); do
		if [ -S /run/devstack-helper/helper.sock ]; then
			break
		fi
		sleep 0.1
	done
	if ! run sudo systemctl is-active --quiet devstack-helper.service; then
		printf 'install: devstack-helper is not active after restart; inspect:\n' >&2
		printf '  sudo systemctl status devstack-helper --no-pager\n' >&2
		printf '  sudo journalctl -u devstack-helper -n 100 --no-pager\n' >&2
		exit 1
	fi
	if [ ! -S /run/devstack-helper/helper.sock ]; then
		# shellcheck disable=SC2016 # backticks are intentional user-facing text
		printf 'install: warning: helper socket is not ready yet; run `devstack init` after checking the service\n' >&2
	fi
fi

# Per-user daemon unit. A user systemd session may not exist over some SSH or
# container sessions; enabling is still safe, and init can start the daemon.
run install -d -m 0755 "${USER_UNIT_DIR}"
run install -m 0644 "${UNIT_DIR}/devstack-daemon.service" "${USER_UNIT}"
if [ "${USER_SYSTEMD}" -eq 1 ]; then
	run systemctl --user daemon-reload
	run systemctl --user enable devstack-daemon.service
	if [ "${UNIT_ACTIVE}" -eq 1 ] || [ "${UPGRADE}" -eq 1 ]; then
		# Restart an active unit, and start it during an upgrade so the new
		# binary is running before the required `devstack init` follow-up.
		if ! run systemctl --user restart devstack-daemon.service; then
			die "devstack-daemon failed to start; check systemctl --user status devstack-daemon.service"
		fi
	else
		# The daemon (unit or detached) was stopped before the binaries were
		# replaced. The unit itself is started by `devstack init` (or manually),
		# not during a first install.
		:
	fi
else
	log "no user systemd session detected; enable the daemon unit later with:"
	log "  systemctl --user daemon-reload && systemctl --user enable devstack-daemon.service"
	log "headless installs: start the daemon with '$USER_BIN daemon run' or start the unit from a login session"
	if [ "${UPGRADE}" -eq 1 ]; then
		log "upgrade: run 'devstack init' to start the daemon again after the upgrade"
	fi
fi

case ":${PATH}:" in
*":${USER_HOME}/.local/bin:"*) ;;
*)
	log "note: ${USER_HOME}/.local/bin is not in PATH for this shell"
	;;
esac

cat <<EOF

Installed:
  CLI              ${USER_BIN}
  helper           ${HELPER_BINARY}
  helper config    ${HELPER_CONFIG}
  helper unit      ${HELPER_UNIT}
  daemon unit      ${USER_UNIT}
EOF
if [ -n "${BACKUP_HINT}" ]; then
	cat <<EOF
  state backup     ${BACKUP_HINT}
                   only when the next start applies a pending migration;
                   the three newest backups under ${DEVSTACK_HOME_DIR}/backups are kept
EOF
fi

if [ "${UPGRADE}" -eq 1 ]; then
	cat <<EOF

Upgrade follow-up: run \`devstack init\` to re-sync hosts, forwarders, trust,
and the daemon. The installer does not run it for you.
EOF
fi

cat <<EOF

Next steps:
  1. Ensure ${USER_HOME}/.local/bin is in your PATH.
  2. Run: devstack init
  3. Open https://devstack.test (fallback: http://127.0.0.1:7070).

Installation and troubleshooting: docs/install.md
EOF
