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

usage() {
	cat <<'EOF'
Usage: install.sh [--dry-run] [--keep-config | --force-config] [--help]

Build devstack first with `make build` (or `make e2e` for the test build).
The installer must run as the user who will own devstack, not as root.

Options:
  --dry-run      Print the installation steps without changing the system.
  --keep-config  Keep an existing /etc/devstack/helper.yaml (the default).
  --force-config Replace /etc/devstack/helper.yaml with the generated defaults.
  -h, --help     Show this help.
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

for arg in "$@"; do
	case "${arg}" in
	--dry-run)
		DRY_RUN=1
		;;
	--keep-config)
		CONFIG_MODE="keep"
		;;
	--force-config)
		CONFIG_MODE="force"
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'install: unknown option %s\n\n' "${arg}" >&2
		usage >&2
		exit 2
		;;
	esac
done

# --force-config and --keep-config are mutually exclusive only when both were
# supplied. Detect that from the original argument list, not from the last
# option processed.
force_seen=0
keep_seen=0
for arg in "$@"; do
	case "${arg}" in
	--force-config) force_seen=1 ;;
	--keep-config) keep_seen=1 ;;
	esac
done
if [ "${force_seen}" -eq 1 ] && [ "${keep_seen}" -eq 1 ]; then
	die "--force-config and --keep-config cannot be used together"
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

USER_UNIT_DIR="${CONFIG_HOME}/systemd/user"
USER_UNIT="${USER_UNIT_DIR}/devstack-daemon.service"
USER_BIN="${USER_HOME}/.local/bin/devstack"

for binary in "${REPO_ROOT}/dist/devstack" "${REPO_ROOT}/dist/devstack-helper"; do
	if [ ! -x "${binary}" ]; then
		die "missing ${binary}; run 'make build' first"
	fi
done

log "installing for ${USER_NAME} (uid ${USER_UID}, home ${USER_HOME})"
if [ "${DRY_RUN}" -eq 1 ]; then
	log "dry run: no files or services will be changed"
fi

# User-facing CLI and daemon binary.
run install -d -m 0755 "${USER_HOME}/.local/bin"
run install -m 0755 "${REPO_ROOT}/dist/devstack" "${USER_BIN}"

# Root-owned helper binary and configuration.
run sudo install -d -m 0755 "${HELPER_ROOT}"
run sudo install -m 0755 "${REPO_ROOT}/dist/devstack-helper" "${HELPER_BINARY}"
run sudo install -d -m 0755 "${HELPER_CONFIG_DIR}"

if [ -e "${HELPER_CONFIG}" ] && [ "${CONFIG_MODE}" != "force" ]; then
	log "keeping existing ${HELPER_CONFIG} (use --force-config to replace it)"
else
	config_tmp="$(mktemp "${TMPDIR:-/tmp}/devstack-helper-config.XXXXXX")"
	cleanup_config() {
		rm -f "${config_tmp}"
	}
	trap cleanup_config EXIT
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
log_level: info
EOF
	run sudo install -m 0644 "${config_tmp}" "${HELPER_CONFIG}"
fi

# System helper unit. `enable` is idempotent; `restart` picks up a new binary
# and config on reinstall and starts the service on a first install.
run sudo install -m 0644 "${REPO_ROOT}/build/systemd/devstack-helper.service" "${HELPER_UNIT}"
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
	if [ ! -S /run/devstack-helper/helper.sock ]; then
		printf 'install: warning: helper socket is not ready yet; run `devstack init` after checking the service\n' >&2
	fi
fi

# Per-user daemon unit. A user systemd session may not exist over some SSH or
# container sessions; enabling is still safe, and init can start the daemon.
run install -d -m 0755 "${USER_UNIT_DIR}"
run install -m 0644 "${REPO_ROOT}/build/systemd/devstack-daemon.service" "${USER_UNIT}"
if systemctl --user show-environment >/dev/null 2>&1; then
	unit_active=0
	if systemctl --user is-active --quiet devstack-daemon.service 2>/dev/null; then
		unit_active=1
	fi
	run systemctl --user daemon-reload
	run systemctl --user enable devstack-daemon.service
	if [ "${unit_active}" -eq 1 ]; then
		# The unit owns the daemon; restart it so the new binary and unit are
		# active immediately.
		run systemctl --user restart devstack-daemon.service
	else
		# A previous `devstack init` may have spawned a detached daemon. Stop it
		# before the next start so the unit and CLI cannot disagree about which
		# process owns the home. The unit itself is started by `devstack init`
		# (or manually), not during a first install.
		if [ -x "${USER_BIN}" ]; then
			if ! run "${USER_BIN}" daemon stop; then
				log "warning: could not stop the detached daemon; run 'devstack daemon stop' manually"
			fi
		fi
	fi
else
	log "no user systemd session detected; enable the daemon unit later with:"
	log "  systemctl --user daemon-reload && systemctl --user enable devstack-daemon.service"
	log "headless installs: start the daemon with '$USER_BIN daemon run' or start the unit from a login session"
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

Next steps:
  1. Ensure ${USER_HOME}/.local/bin is in your PATH.
  2. Run: devstack init
  3. Open https://devstack.test (fallback: http://127.0.0.1:7070).

Installation and troubleshooting: docs/install.md
EOF
