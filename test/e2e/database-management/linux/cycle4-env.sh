#!/usr/bin/env bash
# Cycle 4 disposable Linux environment for E2E-01/E2E-02/E2E-16.
#
# Source this file. It never touches the real devstack distro, /data/devstack,
# the real helper, the real agent, or the real Docker engine. Services are
# started in the Ubuntu 26.04 WSL Docker engine under a disposable home.
#
# The product catalog publishes fixed loopback ports (3306, 5432, 6379, 8082).
# In this machine's WSL setup 3306 and 8082 are already held by the real
# install's relay, so product activation of MySQL/phpMyAdmin is BLOCKED. The
# helpers below can patch the generated Compose file to remove the MySQL port
# and to move phpMyAdmin to a unique high loopback port, then start those
# containers without colliding with the real install.
set -Eeuo pipefail

cycle4_here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cycle4_root="$(cd -- "${cycle4_here}/.." && pwd)"
repo="${E2E_REPO_ROOT:-$(cd -- "${cycle4_root}/../../.." && pwd)}"
ds="${E2E_DS:-${repo}/dist/e2e/database-management/bin/devstack}"
helper_bin="${E2E_HELPER_BIN:-${repo}/dist/e2e/database-management/bin/devstack-helper}"

stamp="${E2E_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
run_key="$(printf '%s' "${E2E_RUN_KEY:-${stamp}}" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/-\{2,\}/-/g; s/^-//; s/-$//')"
home="${E2E_HOME:-/tmp/devstack-e2e-${run_key}}"
devstack_home="${home}/devstack"
helper_state="${home}/helper-state"
helper_run="${home}/run"
helper_socket="${helper_run}/helper.sock"
helper_config="${home}/helper.yaml"
hosts_file="${home}/hosts"
trust_dir="${home}/trust"
vhost_root="${home}/vhost-root"
vhost_state="${home}/vhost-state/vhosts.json"
nginx_include="${home}/nginx/conf.d"
apache_include="${home}/apache/conf.d"
tool_bin="${home}/bin"
logs_dir="${home}/logs"
evidence_dir="${E2E_EVIDENCE_DIR:-${repo}/dist/e2e/database-management/evidence/${run_key}}"
manifest="${home}/manifest.tsv"

ui_port="${E2E_UI_PORT:-27170}"
proxy_http_port="${E2E_PROXY_HTTP_PORT:-27180}"
proxy_https_port="${E2E_PROXY_HTTPS_PORT:-27181}"
forward_http_port="${E2E_FORWARD_HTTP_PORT:-27182}"
forward_https_port="${E2E_FORWARD_HTTPS_PORT:-27183}"
vhost_http_port="${E2E_VHOST_HTTP_PORT:-27184}"
vhost_https_port="${E2E_VHOST_HTTPS_PORT:-27185}"
pma_host_port="${E2E_PMA_HOST_PORT:-28082}"

# Pinned image used for one-shot PostgreSQL clients on the Compose network.
postgres_image="${E2E_POSTGRES_IMAGE:-postgres:18.6@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6}"

helper_pid=""
daemon_pid=""

c4_log() { printf 'cycle4: %s\n' "$*"; }
c4_warn() { printf 'cycle4: WARN: %s\n' "$*" >&2; }
c4_die() { printf 'cycle4: ERROR: %s\n' "$*" >&2; exit 1; }
c4_section() { printf '\n== %s ==\n' "$*"; }

c4_guard() {
	case "$(uname -s)" in
	Linux) ;;
	*) c4_die "Linux/WSL only" ;;
	esac
	if [ "${WSL_DISTRO_NAME:-}" != "Ubuntu" ]; then
		c4_die "cycle 4 must run in the disposable Ubuntu distro (got ${WSL_DISTRO_NAME:-unknown})"
	fi
	case "${home}" in
	/tmp/devstack-e2e-* | /var/tmp/devstack-e2e-*) ;;
	*) c4_die "refusing home outside /tmp/devstack-e2e-*: ${home}" ;;
	esac
	case "${home}" in
	*"/data/devstack"* | *"C:\\Users\\Aram\\.devstack"*) c4_die "refusing real install path" ;;
	esac
	[ -x "${ds}" ] || c4_die "missing Linux devstack binary at ${ds}"
	[ -x "${helper_bin}" ] || c4_die "missing Linux helper binary at ${helper_bin}"
	command -v docker >/dev/null 2>&1 || c4_die "docker is required"
	docker info >/dev/null 2>&1 || c4_die "docker engine is unavailable"
	# The disposable engine must not contain the real install's containers.
	if docker ps -a --format '{{.Names}}' | grep -qx 'devstack-mysql' && ! docker inspect -f '{{index .Config.Labels "devstack.e2e.cycle"}}' devstack-mysql 2>/dev/null | grep -q '^4$'; then
		c4_die "a real-looking devstack-mysql container is visible in this engine"
	fi
	unset SSL_CERT_FILE CURL_CA_BUNDLE NODE_EXTRA_CA_CERTS REQUESTS_CA_BUNDLE
	export E2E_STAMP="${stamp}" E2E_RUN_KEY="${run_key}" E2E_HOME="${home}"
}

c4_init_dirs() {
	mkdir -p "${home}"/{logs,fixtures,secrets,state,backups} "${helper_state}" "${helper_run}" \
		"${vhost_root}" "$(dirname "${vhost_state}")" "${nginx_include}" "${apache_include}" "${tool_bin}"
	chmod 700 "${home}" "${home}/secrets"
	printf '127.0.0.1 localhost\n' >"${hosts_file}"
	[ -f "${manifest}" ] || : >"${manifest}"
}

c4_write_helper_config() {
	cat >"${helper_config}" <<EOF
socket: ${helper_socket}
state_dir: ${helper_state}
hosts: ${hosts_file}
trust_dir: ${trust_dir}
allow_uid: $(id -u)
forward_http_port: ${forward_http_port}
forward_https_port: ${forward_https_port}
proxy_http_port: ${proxy_http_port}
proxy_https_port: ${proxy_https_port}
auto_forward: false
vhost_root: ${vhost_root}
vhost_state: ${vhost_state}
nginx_include_dir: ${nginx_include}
apache_include_dir: ${apache_include}
vhost_http_port: ${vhost_http_port}
vhost_https_port: ${vhost_https_port}
service_root: ${devstack_home}
tool_root: ${devstack_home}
tool_bin_dir: ${tool_bin}
log_level: debug
EOF
	chmod 600 "${helper_config}"
}

c4_start_helper() {
	if [ -n "${helper_pid}" ] && kill -0 "${helper_pid}" 2>/dev/null; then
		return 0
	fi
	"${helper_bin}" --config "${helper_config}" >>"${logs_dir}/helper.log" 2>&1 &
	helper_pid=$!
	for _ in $(seq 1 200); do
		[ -S "${helper_socket}" ] && return 0
		kill -0 "${helper_pid}" 2>/dev/null || { tail -n 80 "${logs_dir}/helper.log" >&2 || true; c4_die "helper exited"; }
		sleep 0.1
	done
	c4_die "helper did not create ${helper_socket}"
}

c4_stop_helper() {
	[ -n "${helper_pid}" ] || return 0
	kill -TERM "${helper_pid}" 2>/dev/null || true
	for _ in $(seq 1 100); do
		kill -0 "${helper_pid}" 2>/dev/null || break
		sleep 0.1
	done
	kill -KILL "${helper_pid}" 2>/dev/null || true
	wait "${helper_pid}" 2>/dev/null || true
	helper_pid=""
}

c4_cli() {
	"${ds}" --home "${devstack_home}" --helper-socket "${helper_socket}" "$@"
}

c4_start_daemon() {
	if [ -n "${daemon_pid}" ] && kill -0 "${daemon_pid}" 2>/dev/null; then
		return 0
	fi
	"${ds}" --home "${devstack_home}" --helper-socket "${helper_socket}" daemon run \
		--http-port "${proxy_http_port}" --https-port "${proxy_https_port}" \
		--ui-port "${ui_port}" --log-level debug >>"${logs_dir}/daemon.log" 2>&1 &
	daemon_pid=$!
	for _ in $(seq 1 300); do
		[ -f "${devstack_home}/run/daemon.ready" ] && return 0
		kill -0 "${daemon_pid}" 2>/dev/null || { tail -n 120 "${logs_dir}/daemon.log" >&2 || true; c4_die "daemon exited"; }
		sleep 0.1
	done
	c4_die "daemon did not become ready"
}

c4_stop_daemon() {
	c4_cli daemon stop >/dev/null 2>&1 || true
	[ -n "${daemon_pid}" ] || return 0
	for _ in $(seq 1 100); do
		kill -0 "${daemon_pid}" 2>/dev/null || break
		sleep 0.1
	done
	kill -TERM "${daemon_pid}" 2>/dev/null || true
	wait "${daemon_pid}" 2>/dev/null || true
	daemon_pid=""
}

c4_compose_project() {
	local path hash
	path="$(readlink -f "${devstack_home}")"
	hash="$(printf '%s' "${path}" | sha256sum | awk '{print $1}' | cut -c1-12)"
	printf 'devstack-%s' "${hash}"
}

c4_compose() {
	local project
	project="$(c4_compose_project)"
	docker compose -p "${project}" -f "${devstack_home}/services/compose.yaml" "$@"
}

# Remove the fixed 3306 publication from the generated MySQL service. The
# container still listens on 3306 inside the Compose network and the helper
# talks to it through `docker compose exec`.
c4_strip_mysql_ports() {
	local file="${devstack_home}/services/compose.yaml"
	[ -f "${file}" ] || c4_die "compose file is missing: ${file}"
	python3 - "${file}" <<'PY'
import re, sys
path = sys.argv[1]
lines = open(path, encoding="utf-8").read().splitlines()
out = []
i = 0
in_mysql = False
mysql_indent = None
while i < len(lines):
    line = lines[i]
    m = re.match(r'^(\s*)mysql:\s*$', line)
    if m:
        in_mysql = True
        mysql_indent = len(m.group(1))
        out.append(line)
        i += 1
        continue
    if in_mysql:
        m2 = re.match(r'^(\s*)([A-Za-z0-9_-]+):\s*$', line)
        if m2 and len(m2.group(1)) <= mysql_indent:
            in_mysql = False
        else:
            mp = re.match(r'^(\s*)ports:\s*$', line)
            if mp:
                ports_indent = len(mp.group(1))
                i += 1
                while i < len(lines):
                    child = lines[i]
                    if child.strip() == "":
                        i += 1
                        continue
                    child_indent = len(child) - len(child.lstrip())
                    if child_indent <= ports_indent:
                        break
                    i += 1
                continue
    out.append(line)
    i += 1
open(path, "w", encoding="utf-8").write("\n".join(out) + "\n")
PY
}

# Move phpMyAdmin's fixed 8082 publication to a unique high loopback port and
# point PMA_ABSOLUTE_URI at it. Only the disposable Compose file is changed.
c4_patch_phpmyadmin_port() {
	local file="${devstack_home}/services/compose.yaml"
	[ -f "${file}" ] || c4_die "compose file is missing: ${file}"
	python3 - "${file}" "${pma_host_port}" <<'PY'
import re, sys
path, port = sys.argv[1], sys.argv[2]
lines = open(path, encoding="utf-8").read().splitlines()
out = []
for line in lines:
    if re.match(r'^\s*-\s*"?127\.0\.0\.1:8082:80"?\s*$', line):
        indent = line[:len(line) - len(line.lstrip())]
        out.append('%s- 127.0.0.1:%s:80' % (indent, port))
        continue
    if 'PMA_ABSOLUTE_URI:' in line and '127.0.0.1:8082' in line:
        out.append(line.replace('127.0.0.1:8082', '127.0.0.1:%s' % port))
        continue
    out.append(line)
open(path, "w", encoding="utf-8").write("\n".join(out) + "\n")
PY
}

c4_service_container() { printf 'devstack-%s' "$1"; }

c4_mysql_root_query() {
	docker exec -i devstack-mysql sh -c '
		MYSQL_PWD="$(cat /run/secrets/mysql_root_password)"
		export MYSQL_PWD
		exec mysql -uroot --batch --skip-column-names "$@"
	' sh "$@"
}

# Read SQL from stdin so generated passwords never appear in argv.
c4_mysql_root_sql() {
	docker exec -i devstack-mysql sh -c '
		MYSQL_PWD="$(cat /run/secrets/mysql_root_password)"
		export MYSQL_PWD
		exec mysql -uroot --batch --skip-column-names
	'
}

c4_psql_root_query() {
	docker exec -i devstack-postgresql sh -c '
		PGPASSWORD="$(cat /run/secrets/postgresql_postgres_password)"
		export PGPASSWORD
		exec psql --no-psqlrc --set=ON_ERROR_STOP=1 -U postgres "$@"
	' sh "$@"
}

c4_psql_root_sql() {
	local database="$1"
	docker exec -i devstack-postgresql sh -c '
		PGPASSWORD="$(cat /run/secrets/postgresql_postgres_password)"
		export PGPASSWORD
		exec psql --no-psqlrc --set=ON_ERROR_STOP=1 -U postgres --dbname="$1"
	' sh "${database}"
}

c4_api() {
	local method="$1" path="$2" body="${3:-}"
	if [ -n "${body}" ]; then
		curl -sS --max-time 20 -X "${method}" -H 'Content-Type: application/json' \
			--data-binary "${body}" "http://127.0.0.1:${ui_port}${path}"
	else
		curl -sS --max-time 20 -X "${method}" "http://127.0.0.1:${ui_port}${path}"
	fi
}

# Copy state.db and its WAL/SHM sidecars, then query only the copy.
c4_audit_copy() {
	local dest="$1"
	mkdir -p "${dest}"
	cp "${devstack_home}/state.db" "${dest}/state.db" || return 1
	for sidecar in state.db-wal state.db-shm; do
		[ -f "${devstack_home}/${sidecar}" ] && cp "${devstack_home}/${sidecar}" "${dest}/${sidecar}" || true
	done
}

c4_audit_query() {
	local copy="$1" sql="$2"
	sqlite3 "${copy}/state.db" "${sql}"
}

c4_sse_start() {
	local output="$1"
	: >"${output}"
	curl -sS -N --max-time 45 "http://127.0.0.1:${ui_port}/api/v1/events" >>"${output}" 2>/dev/null &
	printf '%s\n' "$!"
}

c4_sse_stop() {
	local pid="$1"
	kill "${pid}" 2>/dev/null || true
	wait "${pid}" 2>/dev/null || true
}

# Run a MySQL client query as a managed user without exposing the password in
# argv. The password is written to a 0600 defaults file inside the disposable
# container and removed immediately after the query.
c4_mysql_user_query() {
	local user="$1" password="$2" database="$3" query="$4"
	printf '[client]\nuser=%s\npassword=%s\n' "${user}" "${password}" |
		docker exec -i devstack-mysql sh -c '
			umask 077
			cat > /tmp/e2e-my.cnf
			if [ -n "$1" ]; then
				mysql --defaults-extra-file=/tmp/e2e-my.cnf --batch --skip-column-names "$1" -e "$2"
			else
				mysql --defaults-extra-file=/tmp/e2e-my.cnf --batch --skip-column-names -e "$2"
			fi
			rc=$?
			rm -f /tmp/e2e-my.cnf
			exit $rc
		' sh "${database}" "${query}"
}

# Run a psql query from a one-shot client container on the Compose network so
# pg_hba's scram-sha-256 rule is exercised. The password is stdin, never argv.
c4_psql_network_query() {
	local user="$1" password="$2" database="$3" query="$4" project
	project="$(c4_compose_project)"
	printf '%s\n' "${password}" | docker run --rm -i --network "${project}_default" \
		-e PGHOST=postgresql -e PGPORT=5432 -e PGUSER="${user}" -e PGDATABASE="${database}" \
		--entrypoint sh "${postgres_image}" -c '
			umask 077
			{ printf "%s:%s:%s:%s:" "$PGHOST" "$PGPORT" "$PGDATABASE" "$PGUSER"; cat; } > /tmp/e2e-pgpass
			PGPASSFILE=/tmp/e2e-pgpass psql --no-psqlrc --set=ON_ERROR_STOP=1 --tuples-only --no-align --command "$1"
			rc=$?
			rm -f /tmp/e2e-pgpass
			exit $rc
		' sh "${query}"
}

c4_wait_container_healthy() {
	local container="$1" timeout="${2:-120}" state health
	for _ in $(seq 1 "${timeout}"); do
		state="$(docker inspect -f '{{.State.Running}}' "${container}" 2>/dev/null || echo false)"
		health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${container}" 2>/dev/null || echo missing)"
		if [ "${state}" = "true" ] && { [ "${health}" = "healthy" ] || [ "${health}" = "none" ]; }; then
			return 0
		fi
		if [ "${state}" = "false" ] && [ "${health}" != "starting" ]; then
			docker logs --tail 80 "${container}" >&2 2>&1 || true
			c4_die "${container} is not running (health=${health})"
		fi
		sleep 1
	done
	docker logs --tail 80 "${container}" >&2 2>&1 || true
	c4_die "${container} did not become healthy"
}

c4_require_no_orphans() {
	local expected actual project
	project="$(c4_compose_project)"
	expected="$(printf '%s\n' "$@" | sort -u)"
	actual="$(docker ps -a --filter "label=com.docker.compose.project=${project}" --format '{{.Names}}' | sort -u)"
	[ "${actual}" = "${expected}" ] || {
		printf 'expected containers:\n%s\nactual:\n%s\n' "${expected}" "${actual}" >&2
		c4_die "orphan or missing disposable containers"
	}
}

c4_cleanup() {
	c4_stop_daemon
	c4_stop_helper
	if [ "${E2E_KEEP:-0}" = "1" ]; then
		c4_warn "E2E_KEEP=1: leaving ${home} for inspection"
		return 0
	fi
	local project
	project="$(c4_compose_project 2>/dev/null || true)"
	if [ -n "${project}" ] && [ -f "${devstack_home}/services/compose.yaml" ]; then
		docker compose -p "${project}" -f "${devstack_home}/services/compose.yaml" down --remove-orphans --volumes >/dev/null 2>&1 || true
	fi
	# Remove any container left under the disposable Compose project even if
	# the compose file was already gone.
	if [ -n "${project}" ]; then
		for name in $(docker ps -a --filter "label=com.docker.compose.project=${project}" --format '{{.Names}}'); do
			docker rm -f "${name}" >/dev/null 2>&1 || true
		done
		docker network rm "${project}_default" >/dev/null 2>&1 || true
	fi
	rm -rf "${home}"
}
