#!/usr/bin/env bash
# Linux end-to-end test for the Plan 1 builtin stack and external nginx.
#
# The test is intentionally unprivileged: it builds the binaries, starts the
# helper with temporary state/hosts/trust paths and high forwarder ports,
# starts the daemon on high loopback ports, creates a static site, verifies
# HTTPS with the daemon CA bundle, restarts the daemon, and verifies that the
# site is reconciled exactly once. When nginx is installed it also runs a real
# nginx from a temporary prefix on high HTTP/HTTPS ports, switches
# builtin -> nginx -> builtin through the helper, and checks that the static
# site is served by the vhost and then by the built-in forwarder again. It
# never binds 80/443, edits /etc/hosts, touches a real trust store, or changes
# a system service; the nginx/systemctl wrappers exist only inside ${TMP}/bin.
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
REDIRECT_ROOT="${TMP}/redirect-site"
REDIRECT_HEADERS="${LOG_DIR}/redirect-headers.txt"
NODE_ROOT="${TMP}/node-site"

VHOST_ROOT="${TMP}/vhost-root"
VHOST_STATE="${TMP}/vhost-state/vhosts.json"
NGINX_PREFIX="${TMP}/nginx"
NGINX_CONF="${NGINX_PREFIX}/nginx.conf"
NGINX_PID="${NGINX_PREFIX}/nginx.pid"
NGINX_INCLUDE="${NGINX_PREFIX}/conf.d"
APACHE_INCLUDE="${TMP}/apache/conf.d"
TOOL_BIN="${TMP}/bin"

VHOST_STATUS_LOG="${LOG_DIR}/vhost-status.log"
VHOST_USE_LOG="${LOG_DIR}/vhost-use.log"
VHOST_PREVIEW_LOG="${LOG_DIR}/vhost-preview.log"
REDIRECT_ADD_LOG="${LOG_DIR}/redirect-add.log"
NODE_ADD_LOG="${LOG_DIR}/node-add.log"
NGINX_LOG="${LOG_DIR}/nginx.log"

HELPER_PID=""
DAEMON_PID=""
NGINX_MASTER_PID=""
SELECTED_PORTS=" "
PICKED_PORT=""
EXTERNAL_MODE_AVAILABLE=0

mkdir -p "${LOG_DIR}" "${E2E_HOME}" "${HELPER_RUN}" "${SITE_ROOT}" "${REDIRECT_ROOT}" \
	"${VHOST_ROOT}" "${NGINX_INCLUDE}" "${APACHE_INCLUDE}" "${TOOL_BIN}"
printf '127.0.0.1 localhost\n' >"${HOSTS_FILE}"

# Use a temporary HOME so the daemon, Node processes, and trust lookups cannot
# reach the real user configuration.
export HOME="${E2E_HOME}"
export DEVSTACK_HOME

dump_logs() {
	set +e
	trap - ERR
	printf '\n---- helper log ----\n' >&2
	tail -n 80 "${HELPER_LOG}" >&2 2>/dev/null
	printf '\n---- daemon log ----\n' >&2
	tail -n 120 "${DAEMON_LOG}" >&2 2>/dev/null
	printf '\n---- site add log ----\n' >&2
	tail -n 120 "${SITE_ADD_LOG}" >&2 2>/dev/null
	printf '\n---- daemon stop log ----\n' >&2
	tail -n 40 "${STOP_LOG}" >&2 2>/dev/null
	printf '\n---- vhost use log ----\n' >&2
	tail -n 80 "${VHOST_USE_LOG}" >&2 2>/dev/null
	printf '\n---- vhost status log ----\n' >&2
	tail -n 40 "${VHOST_STATUS_LOG}" >&2 2>/dev/null
	printf '\n---- vhost preview log ----\n' >&2
	tail -n 60 "${VHOST_PREVIEW_LOG}" >&2 2>/dev/null
	printf '\n---- redirect site log ----\n' >&2
	tail -n 60 "${REDIRECT_ADD_LOG}" >&2 2>/dev/null
	printf '\n---- redirect headers ----\n' >&2
	cat "${REDIRECT_HEADERS}" >&2 2>/dev/null
	printf '\n---- node site log ----\n' >&2
	tail -n 60 "${NODE_ADD_LOG}" >&2 2>/dev/null
	printf '\n---- nginx log ----\n' >&2
	tail -n 80 "${NGINX_LOG}" >&2 2>/dev/null
	printf '\n---- nginx error log ----\n' >&2
	tail -n 80 "${NGINX_PREFIX}/error.log" >&2 2>/dev/null
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

stop_nginx() {
	set +e
	if [ -n "${NGINX_MASTER_PID}" ] && kill -0 "${NGINX_MASTER_PID}" 2>/dev/null; then
		kill -QUIT "${NGINX_MASTER_PID}" 2>/dev/null
		for ((i = 0; i < 100; i++)); do
			kill -0 "${NGINX_MASTER_PID}" 2>/dev/null || break
			sleep 0.1
		done
		kill -KILL "${NGINX_MASTER_PID}" 2>/dev/null
	fi
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

	# The test-only nginx runs from a temporary prefix and is never a system
	# service. Stop its master directly so a failed mode switch cannot leave it
	# behind.
	stop_nginx

	# The daemon normally stops these; this is a belt-and-braces cleanup for a
	# failed startup. Use a literal command-line prefix so unrelated servers are
	# never matched.
	pkill -TERM -f -- "serve-static --root ${SITE_ROOT}" 2>/dev/null
	pkill -TERM -f -- "serve-static --root ${REDIRECT_ROOT}" 2>/dev/null
	sleep 0.2
	pkill -KILL -f -- "serve-static --root ${SITE_ROOT}" 2>/dev/null
	pkill -KILL -f -- "serve-static --root ${REDIRECT_ROOT}" 2>/dev/null

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
VHOST_HTTP_PORT="${PICKED_PORT}"
pick_port
VHOST_HTTPS_PORT="${PICKED_PORT}"

# External mode renders the dashboard vhost for 127.0.0.1:7070, and the daemon
# refuses serving_mode=nginx|apache with another UI port. Keep the normal builtin
# flow on a high port when 7070 is busy and skip only the external steps.
if port_in_use 7070; then
	pick_port
	UI_PORT="${PICKED_PORT}"
	printf 'e2e: port 7070 is in use; external vhost mode requires 7070 and will be skipped\n'
else
	UI_PORT=7070
	EXTERNAL_MODE_AVAILABLE=1
fi

printf 'e2e: helper forwarders %s/%s, proxy %s/%s, vhosts %s/%s, ui %s\n' \
	"${FORWARD_HTTP_PORT}" "${FORWARD_HTTPS_PORT}" \
	"${PROXY_HTTP_PORT}" "${PROXY_HTTPS_PORT}" \
	"${VHOST_HTTP_PORT}" "${VHOST_HTTPS_PORT}" "${UI_PORT}"

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

curl_host() {
	local host="$1" port="$2" output="$3" attempts="${4:-1}"
	local attempt
	for ((attempt = 0; attempt < attempts; attempt++)); do
		if curl --fail --silent --show-error --http1.1 --noproxy '*' --max-time 15 \
			--resolve "${host}:${port}:127.0.0.1" \
			--cacert "${DEVSTACK_HOME}/pki/ca-bundle.crt" \
			"https://${host}:${port}/" >"${output}" 2>>"${LOG_DIR}/curl.log"; then
			return 0
		fi
		sleep 0.1
	done
	return 1
}

curl_site() {
	local port="$1" output="$2" attempts="${3:-1}"
	curl_host blog.test "${port}" "${output}" "${attempts}"
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

# Resolve the real nginx before ${TOOL_BIN} is prepended to PATH, otherwise the
# wrapper would exec itself. The wrapper is test-only and runs nginx from a
# temporary prefix; it never touches a system service or system config.
REAL_NGINX="$(command -v nginx 2>/dev/null || true)"

install_test_wrappers() {
	[ -n "${REAL_NGINX}" ] || return 0
	cat >"${TOOL_BIN}/nginx" <<EOF
#!/usr/bin/env bash
exec "${REAL_NGINX}" -p "${NGINX_PREFIX}/" -c "${NGINX_CONF}" "\$@"
EOF
	cat >"${TOOL_BIN}/systemctl" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = "reload" ] || [ "\${1:-}" = "reload-or-restart" ]; then
	if [ "\${2:-}" = "nginx" ]; then
		exec "${REAL_NGINX}" -p "${NGINX_PREFIX}/" -c "${NGINX_CONF}" -s reload
	fi
fi
if [ "\${1:-}" = "is-active" ] && [ "\${3:-}" = "nginx" ]; then
	if [ -s "${NGINX_PID}" ] && kill -0 "\$(cat "${NGINX_PID}")" 2>/dev/null; then
		exit 0
	fi
	exit 3
fi
exit 0
EOF
	chmod 0755 "${TOOL_BIN}/nginx" "${TOOL_BIN}/systemctl"
}

write_nginx_config() {
	cat >"${NGINX_CONF}" <<EOF
pid ${NGINX_PID};
error_log ${NGINX_PREFIX}/error.log info;
worker_processes 1;
events {}
http {
    access_log off;
    include ${NGINX_INCLUDE}/*.conf;
}
EOF
}

start_nginx() {
	write_nginx_config
	"${REAL_NGINX}" -p "${NGINX_PREFIX}/" -c "${NGINX_CONF}" >>"${NGINX_LOG}" 2>&1 ||
		fail "could not start nginx from ${NGINX_PREFIX}"
	for ((i = 0; i < 100; i++)); do
		if [ -s "${NGINX_PID}" ]; then
			NGINX_MASTER_PID="$(cat "${NGINX_PID}")"
			break
		fi
		sleep 0.1
	done
	[ -n "${NGINX_MASTER_PID}" ] || fail "nginx did not write ${NGINX_PID}"
	kill -0 "${NGINX_MASTER_PID}" 2>/dev/null || fail "nginx master is not running"
}

# The helper and daemon must see the wrappers. This is the only PATH change and
# it is confined to the test process tree.
install_test_wrappers
export PATH="${TOOL_BIN}:${PATH}"

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
vhost_root: ${VHOST_ROOT}
vhost_state: ${VHOST_STATE}
nginx_include_dir: ${NGINX_INCLUDE}
apache_include_dir: ${APACHE_INCLUDE}
vhost_http_port: ${VHOST_HTTP_PORT}
vhost_https_port: ${VHOST_HTTPS_PORT}
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

# External nginx mode. nginx and the dashboard top out at 7070 for external
# serving, so both must be available; otherwise the builtin flow above stands
# alone and the skip is explicit.
if [ -z "${REAL_NGINX}" ]; then
	printf 'e2e: SKIP external nginx mode (nginx is not installed)\n'
elif [ "${EXTERNAL_MODE_AVAILABLE}" != "1" ]; then
	printf 'e2e: SKIP external nginx mode (port 7070 is in use; external mode requires the dashboard on 7070)\n'
else
	printf 'e2e: starting test nginx on %s/%s from %s\n' \
		"${VHOST_HTTP_PORT}" "${VHOST_HTTPS_PORT}" "${NGINX_PREFIX}"
	start_nginx
	"${REAL_NGINX}" -p "${NGINX_PREFIX}/" -c "${NGINX_CONF}" -t >>"${NGINX_LOG}" 2>&1 ||
		fail "nginx rejected the empty temporary config"

	run_cli vhost use nginx --yes >>"${VHOST_USE_LOG}" 2>&1 ||
		fail "vhost use nginx failed"
	run_cli vhost status >"${VHOST_STATUS_LOG}" 2>&1 ||
		fail "vhost status failed after switching to nginx"
	grep -q 'nginx' "${VHOST_STATUS_LOG}" || fail "vhost status does not mention nginx"
	[ -L "${VHOST_ROOT}/vhosts/nginx/current" ] || fail "nginx current release link is missing"
	[ -f "${NGINX_INCLUDE}/devstack-vhosts.conf" ] ||
		fail "nginx bridge include is missing"
	grep -Rqs "listen ${VHOST_HTTPS_PORT}" "${VHOST_ROOT}/vhosts/nginx/current/" ||
		fail "installed nginx vhosts do not use the high HTTPS port ${VHOST_HTTPS_PORT}"
	printf 'e2e: switched to external nginx (release and bridge installed)\n'

	curl_site "${VHOST_HTTPS_PORT}" "${RESPONSE}" 100 ||
		fail "HTTPS request through the nginx vhost failed"
	grep -q 'devstack-e2e-ok' "${RESPONSE}" ||
		fail "nginx response did not contain the site marker"
	printf 'e2e: nginx served https://blog.test:%s\n' "${VHOST_HTTPS_PORT}"

	assert_serve_static_groups 0 ||
		fail "serve-static is still running while nginx owns the vhost"

	# A force-HTTPS vhost must redirect to the configured high HTTPS port, not
	# the scheme default 443. The Location header carries the regression.
	cat >"${REDIRECT_ROOT}/index.html" <<'HTML'
<!doctype html><html lang="en"><head><meta charset="utf-8"><title>redirect</title></head><body>redirect</body></html>
HTML
	run_cli site add --name redirect --path "${REDIRECT_ROOT}" --force-https >"${REDIRECT_ADD_LOG}" 2>&1 ||
		fail "force-HTTPS site add failed"
	redirect_ok=0
	for ((attempt = 0; attempt < 100; attempt++)); do
		curl --silent --show-error --http1.1 --noproxy '*' --max-time 15 \
			--resolve "redirect.test:${VHOST_HTTP_PORT}:127.0.0.1" \
			--dump-header "${REDIRECT_HEADERS}" \
			"http://redirect.test:${VHOST_HTTP_PORT}/probe" >/dev/null 2>>"${LOG_DIR}/curl.log" || true
		if grep -q '^HTTP/1.1 308' "${REDIRECT_HEADERS}" 2>/dev/null &&
			grep -qi "^Location: https://redirect.test:${VHOST_HTTPS_PORT}/probe" "${REDIRECT_HEADERS}" 2>/dev/null; then
			redirect_ok=1
			break
		fi
		sleep 0.1
	done
	[ "${redirect_ok}" = "1" ] ||
		fail "nginx force-HTTPS redirect did not target the configured HTTPS port ${VHOST_HTTPS_PORT}"
	printf 'e2e: nginx force-HTTPS redirect targeted https://redirect.test:%s\n' "${VHOST_HTTPS_PORT}"

	run_cli vhost preview blog --server nginx >"${VHOST_PREVIEW_LOG}" 2>&1 ||
		fail "vhost preview failed"
	grep -Fq "${SITE_ROOT}" "${VHOST_PREVIEW_LOG}" ||
		fail "vhost preview does not contain the site root ${SITE_ROOT}"
	printf 'e2e: vhost preview rendered the site root\n'

	RUNTIMES_JSON="$(run_cli runtime list --json 2>/dev/null || true)"
	# Only the installed array counts; the available catalog also contains
	# "kind":"node" entries that site add would try to download.
	INSTALLED_RUNTIMES="$(printf '%s\n' "${RUNTIMES_JSON}" |
		sed -n 's/.*"installed":\(\[[^]]*\]\).*/\1/p')"
	NODE_VERSION="$(printf '%s\n' "${INSTALLED_RUNTIMES}" |
		sed -n 's/.*"kind":"node","version":"\([^"]*\)".*/\1/p' |
		head -n 1)"
	if [ -n "${NODE_VERSION}" ]; then
		mkdir -p "${NODE_ROOT}"
		cat >"${NODE_ROOT}/server.js" <<'JS'
const http = require("http");
http.createServer((_req, res) => {
  res.writeHead(200, { "Content-Type": "text/plain" });
  res.end("devstack-e2e-node-ok\n");
}).listen(Number(process.env.PORT), "127.0.0.1");
JS
		run_cli site add --name api --path "${NODE_ROOT}" --kind node \
			--node "${NODE_VERSION}" --command "node server.js" >"${NODE_ADD_LOG}" 2>&1 ||
			fail "node site add failed"
		curl_host api.test "${VHOST_HTTPS_PORT}" "${RESPONSE}" 100 ||
			fail "HTTPS request through the nginx Node vhost failed"
		grep -q 'devstack-e2e-node-ok' "${RESPONSE}" ||
			fail "nginx Node vhost response did not contain the site marker"
		printf 'e2e: nginx served a Node site through https://api.test:%s\n' "${VHOST_HTTPS_PORT}"
	else
		printf 'e2e: SKIP node vhost check (no installed Node runtime)\n'
	fi

	run_cli vhost use builtin --yes >>"${VHOST_USE_LOG}" 2>&1 ||
		fail "vhost use builtin failed"
	curl_site "${FORWARD_HTTPS_PORT}" "${RESPONSE}" 100 ||
		fail "helper forwarder did not serve the site after switching back to builtin"
	grep -q 'devstack-e2e-ok' "${RESPONSE}" ||
		fail "post-builtin forwarder response did not contain the site marker"
	assert_serve_static_groups 1 ||
		fail "expected exactly one serve-static group after switching back to builtin"
	printf 'e2e: switched back to builtin; helper forwarder and serve-static restored\n'
fi

printf 'e2e: PASS (builtin and, when available, external nginx serving verified)\n'
