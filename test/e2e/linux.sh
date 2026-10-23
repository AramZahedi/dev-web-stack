#!/usr/bin/env bash
# Linux end-to-end test for the Plan 1 builtin stack.
#
# The test is intentionally unprivileged: it builds the binaries, starts the
# helper with temporary state/hosts/trust paths and high forwarder ports,
# starts the daemon on high loopback ports, creates a static site, verifies
# HTTPS with the daemon CA bundle, restarts the daemon, and verifies that the
# site is reconciled exactly once. It never binds 80/443, edits /etc/hosts, or
# touches a real trust store.
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN="${REPO_ROOT}/dist/devstack"
HELPER_BIN="${REPO_ROOT}/dist/devstack-helper"

if [ "${E2E_SKIP_BUILD:-0}" != "1" ]; then
	make -C "${REPO_ROOT}" build
fi
for binary in "${BIN}" "${HELPER_BIN}"; do
	if [ ! -x "${binary}" ]; then
		printf 'e2e: missing %s; run `make build` first\n' "${binary}" >&2
		exit 1
	fi
done

TMP="$(mktemp -d "${TMPDIR:-/tmp}/devstack-e2e.XXXXXX")"
LOG_DIR="${TMP}/logs"
HELPER_LOG="${LOG_DIR}/helper.log"
DAEMON_LOG="${LOG_DIR}/daemon.log"
SITE_ADD_LOG="${LOG_DIR}/site-add.log"
STOP_LOG="${LOG_DIR}/daemon-stop.log"
E2E_HOME="${TMP}/home"
DEVSTACK_HOME="${E2E_HOME}/.devstack"
HELPER_STATE="${TMP}/helper-state"
HELPER_RUN="${TMP}/helper-run"
HELPER_SOCKET="${HELPER_RUN}/helper.sock"
HELPER_CONFIG="${TMP}/helper.yaml"
HOSTS_FILE="${TMP}/hosts"
TRUST_DIR="${TMP}/trust"
SITE_ROOT="${TMP}/site"
RESPONSE="${TMP}/response.html"

HELPER_PID=""
DAEMON_PID=""
SELECTED_PORTS=" "
PICKED_PORT=""

mkdir -p "${LOG_DIR}" "${E2E_HOME}" "${HELPER_RUN}" "${SITE_ROOT}"
printf '127.0.0.1 localhost\n' >"${HOSTS_FILE}"

# Use a temporary HOME so the daemon, Node processes, and trust lookups cannot
# reach the real user configuration.
export HOME="${E2E_HOME}"
export DEVSTACK_HOME

dump_logs() {
	set +e
	printf '\n---- helper log ----\n' >&2
	tail -n 80 "${HELPER_LOG}" 2>/dev/null >&2
	printf '\n---- daemon log ----\n' >&2
	tail -n 120 "${DAEMON_LOG}" 2>/dev/null >&2
	printf '\n---- site add log ----\n' >&2
	tail -n 120 "${SITE_ADD_LOG}" 2>/dev/null >&2
	printf '\n---- daemon stop log ----\n' >&2
	tail -n 40 "${STOP_LOG}" 2>/dev/null >&2
}

fail() {
	printf 'e2e: FAIL: %s\n' "$*" >&2
	dump_logs
	exit 1
}

on_error() {
	printf 'e2e: FAIL at line %s\n' "$1" >&2
	dump_logs
	exit 1
}

cleanup() {
	set +e
	trap - ERR EXIT INT TERM

	# Stop whichever daemon owns this test home, including one auto-started by
	# `devstack site add` if our direct process lost a startup race.
	"${BIN}" --home "${DEVSTACK_HOME}" --helper-socket "${HELPER_SOCKET}" daemon stop >/dev/null 2>&1

	if [ -n "${DAEMON_PID}" ] && kill -0 "${DAEMON_PID}" 2>/dev/null; then
		for ((i = 0; i < 100; i++)); do
			kill -0 "${DAEMON_PID}" 2>/dev/null || break
			sleep 0.1
		done
		kill -TERM "${DAEMON_PID}" 2>/dev/null
	fi
	wait "${DAEMON_PID}" 2>/dev/null

	if [ -n "${HELPER_PID}" ] && kill -0 "${HELPER_PID}" 2>/dev/null; then
		kill -TERM "${HELPER_PID}" 2>/dev/null
		for ((i = 0; i < 100; i++)); do
			kill -0 "${HELPER_PID}" 2>/dev/null || break
			sleep 0.1
		done
		kill -KILL "${HELPER_PID}" 2>/dev/null
	fi
	wait "${HELPER_PID}" 2>/dev/null

	# The daemon normally stops these; this is a belt-and-braces cleanup for a
	# failed startup. Use a literal command-line prefix so unrelated servers are
	# never matched.
	pkill -TERM -f -- "serve-static --root ${SITE_ROOT}" 2>/dev/null
	sleep 0.2
	pkill -KILL -f -- "serve-static --root ${SITE_ROOT}" 2>/dev/null

	rm -rf "${TMP}"
}
trap 'on_error ${LINENO}' ERR
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

port_in_use() {
	local port="$1"
	if command -v ss >/dev/null 2>&1; then
		[ -n "$(ss -H -ltn "sport = :${port}" 2>/dev/null || true)" ]
	else
		(exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null
	fi
}

pick_port() {
	local port=""
	for ((attempt = 0; attempt < 200; attempt++)); do
		port=$((20000 + RANDOM % 12000))
		case "${SELECTED_PORTS}" in
		*" ${port} "*) continue ;;
		esac
		if port_in_use "${port}"; then
			continue
		fi
		SELECTED_PORTS="${SELECTED_PORTS}${port} "
		PICKED_PORT="${port}"
		return 0
	done
	printf 'e2e: could not find a free high port\n' >&2
	return 1
}

pick_port
FORWARD_HTTP_PORT="${PICKED_PORT}"
pick_port
FORWARD_HTTPS_PORT="${PICKED_PORT}"
pick_port
PROXY_HTTP_PORT="${PICKED_PORT}"
pick_port
PROXY_HTTPS_PORT="${PICKED_PORT}"
pick_port
UI_PORT="${PICKED_PORT}"

printf 'e2e: helper forwarders %s/%s, proxy %s/%s, ui %s\n' \
	"${FORWARD_HTTP_PORT}" "${FORWARD_HTTPS_PORT}" \
	"${PROXY_HTTP_PORT}" "${PROXY_HTTPS_PORT}" "${UI_PORT}"

wait_for_socket() {
	local pid="$1"
	for ((i = 0; i < 300; i++)); do
		if [ -S "${HELPER_SOCKET}" ]; then
			return 0
		fi
		kill -0 "${pid}" 2>/dev/null || return 1
		sleep 0.1
	done
	return 1
}

wait_for_ready() {
	local pid="$1"
	for ((i = 0; i < 300; i++)); do
		if [ -f "${DEVSTACK_HOME}/run/daemon.ready" ]; then
			return 0
		fi
		kill -0 "${pid}" 2>/dev/null || return 1
		sleep 0.1
	done
	return 1
}

start_daemon() {
	"${BIN}" \
		--home "${DEVSTACK_HOME}" \
		--helper-socket "${HELPER_SOCKET}" \
		daemon run \
		--http-port "${PROXY_HTTP_PORT}" \
		--https-port "${PROXY_HTTPS_PORT}" \
		--ui-port "${UI_PORT}" \
		--log-level debug >>"${DAEMON_LOG}" 2>&1 &
	DAEMON_PID=$!
}

start_helper() {
	"${HELPER_BIN}" --config "${HELPER_CONFIG}" >>"${HELPER_LOG}" 2>&1 &
	HELPER_PID=$!
	wait_for_socket "${HELPER_PID}" || fail "helper did not create ${HELPER_SOCKET}"
	[ -S "${HELPER_SOCKET}" ] || fail "helper socket missing"
}

curl_site() {
	local port="$1" output="$2" attempts="${3:-1}"
	local attempt
	for ((attempt = 0; attempt < attempts; attempt++)); do
		if curl --fail --silent --show-error --http1.1 --noproxy '*' --max-time 15 \
			--resolve "blog.test:${port}:127.0.0.1" \
			--cacert "${DEVSTACK_HOME}/pki/ca-bundle.crt" \
			"https://blog.test:${port}/" >"${output}" 2>>"${LOG_DIR}/curl.log"; then
			return 0
		fi
		sleep 0.1
	done
	return 1
}

run_cli() {
	"${BIN}" --home "${DEVSTACK_HOME}" --helper-socket "${HELPER_SOCKET}" "$@"
}

count_serve_static_groups() {
	ps -eo pgid=,args= |
		awk -v root="${SITE_ROOT}" 'index($0, "serve-static") && index($0, "--root " root) { print $1 }' |
		sort -u |
		wc -l |
		tr -d ' '
}

assert_serve_static_groups() {
	local want="$1" count
	count="$(count_serve_static_groups)"
	if [ "${count}" -ne "${want}" ]; then
		printf 'e2e: expected %s serve-static process group(s), found %s\n' "${want}" "${count}" >&2
		ps -eo pid=,pgid=,args= >&2
		return 1
	fi
}

# The helper configuration path is exercised end-to-end: the unit uses the same
# --config flow in production. All state, hosts, CA, and forwarder ports are
# overridden so no privileged path is touched. Trust installation is
# deliberately not tested here because it would modify real system/browser
# stores; devstack init owns that flow.
cat >"${HELPER_CONFIG}" <<EOF
socket: ${HELPER_SOCKET}
state_dir: ${HELPER_STATE}
hosts: ${HOSTS_FILE}
trust_dir: ${TRUST_DIR}
allow_uid: $(id -u)
forward_http_port: ${FORWARD_HTTP_PORT}
forward_https_port: ${FORWARD_HTTPS_PORT}
proxy_http_port: ${PROXY_HTTP_PORT}
proxy_https_port: ${PROXY_HTTPS_PORT}
auto_forward: true
log_level: debug
EOF
start_helper
printf 'e2e: helper ready with automatic forwarders\n'

start_daemon
wait_for_ready "${DAEMON_PID}" || fail "daemon did not become ready"
[ "$(cat "${DEVSTACK_HOME}/run/daemon.ready")" = "${DAEMON_PID}" ] || fail "readiness marker does not name the daemon"
printf 'e2e: daemon ready (pid %s)\n' "${DAEMON_PID}"

cat >"${SITE_ROOT}/index.html" <<'HTML'
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>devstack e2e</title></head>
<body><h1>devstack-e2e-ok</h1></body>
</html>
HTML

run_cli site add --name blog --path "${SITE_ROOT}" >"${SITE_ADD_LOG}" 2>&1 ||
	fail "site add failed"
[ -f "${DEVSTACK_HOME}/pki/ca-bundle.crt" ] || fail "daemon CA bundle missing"

curl_site "${PROXY_HTTPS_PORT}" "${RESPONSE}" 50 ||
	fail "HTTPS request through the builtin proxy failed"
grep -q 'devstack-e2e-ok' "${RESPONSE}" || fail "response did not contain the site marker"
printf 'e2e: static site served at https://blog.test:%s\n' "${PROXY_HTTPS_PORT}"

# The forwarder is the path browsers use on 443, not the daemon proxy. It must
# work from the helper's startup configuration, without a SetForwarders RPC.
curl_site "${FORWARD_HTTPS_PORT}" "${RESPONSE}" 50 ||
	fail "HTTPS request through the helper forwarder failed"
grep -q 'devstack-e2e-ok' "${RESPONSE}" || fail "forwarder response did not contain the site marker"
printf 'e2e: static site served through helper forwarder https://blog.test:%s\n' "${FORWARD_HTTPS_PORT}"

assert_serve_static_groups 1 || fail "expected exactly one serve-static group before restart"

# A helper restart must restore its auto-forwarders; the daemon stays up.
OLD_HELPER_PID="${HELPER_PID}"
kill -TERM "${HELPER_PID}" 2>/dev/null || fail "could not stop the helper"
wait "${OLD_HELPER_PID}" 2>/dev/null || true
if kill -0 "${OLD_HELPER_PID}" 2>/dev/null; then
	fail "old helper process is still running after stop"
fi
start_helper
[ "${HELPER_PID}" != "${OLD_HELPER_PID}" ] || fail "helper pid did not change across restart"
curl_site "${FORWARD_HTTPS_PORT}" "${RESPONSE}" 100 ||
	fail "forwarder did not serve after helper restart"
grep -q 'devstack-e2e-ok' "${RESPONSE}" || fail "post-restart forwarder response did not contain the site marker"
printf 'e2e: helper restarted and forwarder restored automatically\n'

OLD_DAEMON_PID="${DAEMON_PID}"
run_cli daemon stop >"${STOP_LOG}" 2>&1 || fail "daemon stop failed"
wait "${OLD_DAEMON_PID}" 2>/dev/null || true
if kill -0 "${OLD_DAEMON_PID}" 2>/dev/null; then
	fail "old daemon process is still running after stop"
fi
[ ! -e "${DEVSTACK_HOME}/run/daemon.ready" ] || fail "readiness marker survived daemon stop"
assert_serve_static_groups 0 || fail "serve-static survived daemon stop"
printf 'e2e: daemon stopped cleanly\n'

DAEMON_PID=""
start_daemon
wait_for_ready "${DAEMON_PID}" || fail "daemon did not become ready after restart"
[ "$(cat "${DEVSTACK_HOME}/run/daemon.ready")" = "${DAEMON_PID}" ] || fail "restarted daemon readiness marker mismatch"
[ "${DAEMON_PID}" != "${OLD_DAEMON_PID}" ] || fail "daemon pid did not change across restart"

curl_site "${PROXY_HTTPS_PORT}" "${RESPONSE}" 50 ||
	fail "site did not serve after daemon restart"
grep -q 'devstack-e2e-ok' "${RESPONSE}" || fail "response after restart did not contain the site marker"
curl_site "${FORWARD_HTTPS_PORT}" "${RESPONSE}" 50 ||
	fail "forwarder did not serve after daemon restart"
grep -q 'devstack-e2e-ok' "${RESPONSE}" || fail "forwarder response after restart did not contain the site marker"
assert_serve_static_groups 1 || fail "expected exactly one serve-static group after restart"

printf 'e2e: PASS (site survived daemon restart; one serve-static process group)\n'
