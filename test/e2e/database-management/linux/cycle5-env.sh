#!/usr/bin/env bash
# Cycle 5 disposable Linux environment for E2E-03/04/05/06/17.
#
# The helper runs as root with allow_uid=1000. The daemon, CLI, site
# processes, and terminal PTY run as the unprivileged `aram` user so the
# terminal path is real. MySQL/MariaDB containers run without published host
# ports; PostgreSQL uses its fixed loopback 5432 (free on this host). The real
# `devstack` distro, /data/devstack, helper, agent, and Docker engine are never
# touched.
set -Eeuo pipefail

cycle5_here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle4-env.sh
source "${cycle5_here}/cycle4-env.sh"
# shellcheck source=cycle4-check.sh
source "${cycle5_here}/cycle4-check.sh"

c5_user="${E2E_DAEMON_USER:-aram}"
c5_uid="$(id -u "${c5_user}")"
c5_user_home="$(getent passwd "${c5_user}" | cut -d: -f6)"
private="${home}/private"
secret_store="${home}/secrets"
c5_mysql_ready=0
c5_postgres_ready=0
c5_node_ready=0
c5_php_ready=0

c5_log() { printf 'cycle5: %s\n' "$*"; }
c5_warn() { printf 'cycle5: WARN: %s\n' "$*" >&2; }
c5_die() { printf 'cycle5: ERROR: %s\n' "$*" >&2; exit 1; }
c5_section() { printf '\n== %s ==\n' "$*"; }

c5_guard() {
	case "$(uname -s)" in
	Linux) ;;
	*) c5_die "Linux/WSL only" ;;
	esac
	if [ "${WSL_DISTRO_NAME:-}" != "Ubuntu" ]; then
		c5_die "cycle 5 must run in the disposable Ubuntu distro (got ${WSL_DISTRO_NAME:-unknown})"
	fi
	case "${home}" in
	/tmp/devstack-e2e-* | /var/tmp/devstack-e2e-*) ;;
	*) c5_die "refusing home outside /tmp/devstack-e2e-*: ${home}" ;;
	esac
	case "${home}" in
	*"/data/devstack"* | *"C:\\Users\\Aram\\.devstack"*) c5_die "refusing real install path" ;;
	esac
	[ -x "${ds}" ] || c5_die "missing Linux devstack binary at ${ds}"
	[ -x "${helper_bin}" ] || c5_die "missing Linux helper binary at ${helper_bin}"
	command -v docker >/dev/null 2>&1 || c5_die "docker is required"
	docker info >/dev/null 2>&1 || c5_die "docker engine is unavailable"
	[ "$(id -u "${c5_user}")" = "${c5_uid}" ] || c5_die "cannot resolve ${c5_user}"
	unset SSL_CERT_FILE CURL_CA_BUNDLE NODE_EXTRA_CA_CERTS REQUESTS_CA_BUNDLE
	export E2E_STAMP="${stamp}" E2E_RUN_KEY="${run_key}" E2E_HOME="${home}"
}

c5_init_dirs() {
	mkdir -p "${home}"/{logs,fixtures,secrets,private,projects} \
		"${helper_state}" "${helper_run}" "${vhost_root}" \
		"$(dirname "${vhost_state}")" "${nginx_include}" "${apache_include}" "${tool_bin}" \
		"${devstack_home}"
	chown "${c5_user}:${c5_user}" "${home}" "${home}/logs" "${home}/fixtures" \
		"${home}/secrets" "${home}/private" "${home}/projects" "${devstack_home}"
	chmod 711 "${home}"
	chmod 700 "${home}/secrets" "${home}/private" "${devstack_home}"
	printf '127.0.0.1 localhost\n' >"${hosts_file}"
	[ -f "${manifest}" ] || : >"${manifest}"
}

c5_write_helper_config() {
	cat >"${helper_config}" <<EOF
socket: ${helper_socket}
state_dir: ${helper_state}
hosts: ${hosts_file}
trust_dir: ${trust_dir}
allow_uid: ${c5_uid}
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

c5_start_helper() {
	if [ -n "${helper_pid}" ] && kill -0 "${helper_pid}" 2>/dev/null; then
		return 0
	fi
	"${helper_bin}" --config "${helper_config}" >>"${logs_dir}/helper.log" 2>&1 &
	helper_pid=$!
	for _ in $(seq 1 200); do
		[ -S "${helper_socket}" ] && return 0
		kill -0 "${helper_pid}" 2>/dev/null || { tail -n 80 "${logs_dir}/helper.log" >&2 || true; c5_die "helper exited"; }
		sleep 0.1
	done
	c5_die "helper did not create ${helper_socket}"
}

c5_stop_helper() {
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

c5_cli() {
	runuser -u "${c5_user}" -- env HOME="${c5_user_home}" \
		"${ds}" --home "${devstack_home}" --helper-socket "${helper_socket}" "$@"
}

c5_start_daemon() {
	if [ -n "${daemon_pid}" ] && kill -0 "${daemon_pid}" 2>/dev/null; then
		return 0
	fi
	runuser -u "${c5_user}" -- env HOME="${c5_user_home}" \
		"${ds}" --home "${devstack_home}" --helper-socket "${helper_socket}" daemon run \
		--http-port "${proxy_http_port}" --https-port "${proxy_https_port}" \
		--ui-port "${ui_port}" --log-level debug >>"${logs_dir}/daemon.log" 2>&1 &
	daemon_pid=$!
	for _ in $(seq 1 300); do
		[ -f "${devstack_home}/run/daemon.ready" ] && return 0
		kill -0 "${daemon_pid}" 2>/dev/null || { tail -n 120 "${logs_dir}/daemon.log" >&2 || true; c5_die "daemon exited"; }
		sleep 0.1
	done
	c5_die "daemon did not become ready"
}

c5_stop_daemon() {
	c5_cli daemon stop >/dev/null 2>&1 || true
	[ -n "${daemon_pid}" ] || return 0
	for _ in $(seq 1 100); do
		kill -0 "${daemon_pid}" 2>/dev/null || break
		sleep 0.1
	done
	kill -TERM "${daemon_pid}" 2>/dev/null || true
	wait "${daemon_pid}" 2>/dev/null || true
	daemon_pid=""
}

c5_compose_project() {
	local path hash
	path="$(readlink -f "${devstack_home}")"
	hash="$(printf '%s' "${path}" | sha256sum | awk '{print $1}' | cut -c1-12)"
	printf 'devstack-%s' "${hash}"
}

c5_compose() {
	local project
	project="$(c5_compose_project)"
	docker compose -p "${project}" -f "${devstack_home}/services/compose.yaml" "$@"
}

# Remove a service's published ports from the disposable Compose file. The
# container still listens on its service port inside the Compose network.
c5_strip_service_ports() {
	local service="$1" file="${devstack_home}/services/compose.yaml"
	[ -f "${file}" ] || c5_die "compose file is missing: ${file}"
	python3 - "${file}" "${service}" <<'PY'
import re, sys
path, service = sys.argv[1], sys.argv[2]
lines = open(path, encoding="utf-8").read().splitlines()
out = []
i = 0
in_service = False
service_indent = None
while i < len(lines):
    line = lines[i]
    m = re.match(r'^(\s*)' + re.escape(service) + r':\s*$', line)
    if m:
        in_service = True
        service_indent = len(m.group(1))
        out.append(line)
        i += 1
        continue
    if in_service:
        m2 = re.match(r'^(\s*)([A-Za-z0-9_-]+):\s*$', line)
        if m2 and len(m2.group(1)) <= service_indent:
            in_service = False
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

c5_wait_container_healthy() {
	local container="$1" timeout="${2:-120}" state health
	for _ in $(seq 1 "${timeout}"); do
		state="$(docker inspect -f '{{.State.Running}}' "${container}" 2>/dev/null || echo false)"
		health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${container}" 2>/dev/null || echo missing)"
		if [ "${state}" = "true" ] && { [ "${health}" = "healthy" ] || [ "${health}" = "none" ]; }; then
			return 0
		fi
		if [ "${state}" = "false" ] && [ "${health}" != "starting" ]; then
			docker logs --tail 80 "${container}" >&2 2>&1 || true
			c5_die "${container} is not running (health=${health})"
		fi
		sleep 1
	done
	docker logs --tail 80 "${container}" >&2 2>&1 || true
	c5_die "${container} did not become healthy"
}

c5_service_json() {
	c5_cli --json service list 2>/dev/null
}

c5_service_active() {
	local id="$1" json
	json="$(c5_service_json || true)"
	[ -n "${json}" ] || return 1
	python3 -c 'import json,sys; rows=json.load(sys.stdin); raise SystemExit(0 if any(r.get("id")==sys.argv[1] and r.get("active") for r in rows) else 1)' "${id}" <<<"${json}"
}

c5_runtime_installed() {
	local id="$1" json
	json="$(c5_cli --json runtime list 2>/dev/null || true)"
	[ -n "${json}" ] || return 1
	python3 -c 'import json,sys; d=json.load(sys.stdin); raise SystemExit(0 if any(b.get("id")==sys.argv[1] for b in d.get("installed", [])) else 1)' "${id}" <<<"${json}"
}

c5_install_mysql_noport() {
	[ "${c5_mysql_ready}" = "1" ] && return 0
	c5_cli --json service install mysql >"${logs_dir}/install-mysql.json" 2>"${logs_dir}/install-mysql.err" ||
		c5_die "mysql install failed: $(head -1 "${logs_dir}/install-mysql.err")"
	c5_strip_service_ports mysql
	c5_compose up -d --no-deps mysql >"${logs_dir}/start-mysql.log" 2>&1
	c5_wait_container_healthy devstack-mysql 180
	c5_mysql_ready=1
}

c5_install_mariadb_noport() {
	c5_cli --json service install mariadb >"${logs_dir}/install-mariadb.json" 2>"${logs_dir}/install-mariadb.err" ||
		c5_die "mariadb install failed: $(head -1 "${logs_dir}/install-mariadb.err")"
	c5_strip_service_ports mariadb
	c5_compose up -d --no-deps mariadb >"${logs_dir}/start-mariadb.log" 2>&1
	c5_wait_container_healthy devstack-mariadb 180
}

c5_install_postgresql() {
	[ "${c5_postgres_ready}" = "1" ] && return 0
	c5_cli --json service install postgresql >"${logs_dir}/install-postgresql.json" 2>"${logs_dir}/install-postgresql.err" ||
		c5_die "postgresql install failed: $(head -1 "${logs_dir}/install-postgresql.err")"
	c5_cli --json service activate postgresql >"${logs_dir}/activate-postgresql.json" 2>"${logs_dir}/activate-postgresql.err" ||
		c5_die "postgresql activation failed: $(head -1 "${logs_dir}/activate-postgresql.err")"
	c5_wait_container_healthy devstack-postgresql 180
	c5_postgres_ready=1
}

c5_install_node() {
	[ "${c5_node_ready}" = "1" ] && return 0
	if ! c5_runtime_installed "node@24.22.1"; then
		c5_cli --yes --json runtime install node@24.22.1 >"${logs_dir}/install-node.json" 2>"${logs_dir}/install-node.err" ||
			c5_die "node runtime install failed: $(head -1 "${logs_dir}/install-node.err")"
	fi
	c5_cli --json node default 24.22.1 >"${logs_dir}/node-default.json" 2>"${logs_dir}/node-default.err" ||
		c5_die "node default failed: $(head -1 "${logs_dir}/node-default.err")"
	c5_node_ready=1
}

c5_install_php() {
	[ "${c5_php_ready}" = "1" ] && return 0
	if ! c5_runtime_installed "php@8.4.27-r1"; then
		c5_cli --yes --json runtime install php@8.4.27-r1 >"${logs_dir}/install-php.json" 2>"${logs_dir}/install-php.err" ||
			c5_die "php runtime install failed: $(head -1 "${logs_dir}/install-php.err")"
	fi
	c5_php_ready=1
}

c5_init() {
	c5_guard
	c5_init_dirs
	c5_write_helper_config
	c5_start_helper
	c5_start_daemon
}

c5_ensure_base() {
	c5_install_mysql_noport
	c5_install_node
}

c5_create_project() {
	local name="$1" kind="${2:-static}" dir="${home}/projects/$1"
	mkdir -p "${dir}"
	case "${kind}" in
	php) printf '<?php echo "ok";\n' >"${dir}/index.php" ;;
	*) printf '<!doctype html><html><body>%s</body></html>\n' "${name}" >"${dir}/index.html" ;;
	esac
	chown -R "${c5_user}:${c5_user}" "${dir}"
	printf '%s\n' "${dir}"
}

c5_site_id() {
	local name="$1" json
	json="$(c5_cli --json site list 2>/dev/null || true)"
	python3 -c 'import json,sys; rows=json.load(sys.stdin); print(next((s["id"] for s in rows if s.get("name")==sys.argv[1]), ""))' "${name}" <<<"${json}"
}

c5_user_id() {
	local service="$1" username="$2" json
	json="$(c5_cli --json db user list --service "${service}" 2>/dev/null || true)"
	python3 -c 'import json,sys; users=json.load(sys.stdin).get("users", []); print(next((u["id"] for u in users if u.get("username")==sys.argv[1]), ""))' "${username}" <<<"${json}"
}

c5_link_id() {
	local site="$1" database="$2" json
	json="$(c5_api_get "/api/v1/sites/${site}/databases" 2>/dev/null || true)"
	python3 -c 'import json,sys; links=json.load(sys.stdin).get("links", []); print(next((l["id"] for l in links if l.get("database")==sys.argv[1]), ""))' "${database}" <<<"${json}"
}

c5_api_get() {
	local path="$1"
	curl -sS --max-time 20 "http://127.0.0.1:${ui_port}${path}"
}

c5_api_session() {
	local jar="${private}/cookies.txt" session="${private}/session.json"
	curl -sS --max-time 20 -c "${jar}" "http://127.0.0.1:${ui_port}/api/v1/session" >"${session}"
	csrf="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["csrf_token"])' "${session}")"
	export csrf
}

c5_api_post() {
	local path="$1" body="${2:-}"
	curl -sS --max-time 30 -b "${private}/cookies.txt" -H "X-CSRF-Token: ${csrf}" \
		-H 'Content-Type: application/json' -X POST --data-binary "${body}" \
		"http://127.0.0.1:${ui_port}${path}"
}

c5_api_delete() {
	local path="$1" body="${2:-}"
	curl -sS --max-time 30 -b "${private}/cookies.txt" -H "X-CSRF-Token: ${csrf}" \
		-H 'Content-Type: application/json' -X DELETE --data-binary "${body}" \
		"http://127.0.0.1:${ui_port}${path}"
}

# Capture the resolved site environment into a private JSON file and a
# redacted summary. The actual password is written only to the 0600 secret
# store for file scans; it is never written to evidence or stdout.
c5_capture_site_env() {
	local site="$1" tag="$2"
	local envfile="${private}/${tag}-env.json"
	local summary="${evidence_dir}/${scenario}/${tag}-env-summary.txt"
	c5_api_get "/api/v1/sites/${site}/database-env" >"${envfile}"
	chmod 600 "${envfile}"
	python3 - "${envfile}" "${secret_store}/${tag}.password" "${summary}" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8")).get("env", {})
secret_path, summary_path = sys.argv[2], sys.argv[3]
lines = []
secret = data.get("DB_PASSWORD", "")
if secret:
    with open(secret_path, "w", encoding="utf-8") as fh:
        fh.write(secret)
    import os
    os.chmod(secret_path, 0o600)
lines.append(f"DB_PASSWORD_PRESENT={'true' if data.get('DB_PASSWORD') else 'false'}")
lines.append(f"DATABASE_URL_PRESENT={'true' if data.get('DATABASE_URL') else 'false'}")
for key in sorted(data):
    value = data[key]
    if key in {"DB_PASSWORD", "DATABASE_URL"}:
        continue
    if "PASSWORD" in key:
        lines.append(f"{key}_PRESENT={'true' if value else 'false'}")
    elif key.endswith("_URL"):
        # Never persist userinfo; only report presence.
        lines.append(f"{key}_PRESENT={'true' if value else 'false'}")
    else:
        lines.append(f"{key}={value}")
open(summary_path, "w", encoding="utf-8").write("\n".join(lines) + "\n")
PY
}

c5_secret_file() { printf '%s/%s.password' "${secret_store}" "$1"; }

c5_assert_file_lacks_secret() {
	local scenario="$1" label="$2" file="$3" secret="$4"
	if [ ! -f "${secret}" ]; then
		c4_fail "${scenario}" "${label}" "secret fixture missing"
		return 0
	fi
	if python3 - "${file}" "${secret}" <<'PY'
import sys
needle = open(sys.argv[2], "rb").read().strip()
if not needle:
    raise SystemExit(0)
data = open(sys.argv[1], "rb").read()
raise SystemExit(0 if needle not in data else 1)
PY
	then
		c4_pass "${scenario}" "${label}"
	else
		c4_fail "${scenario}" "${label}" "secret found"
	fi
}

c5_assert_tree_lacks_secret() {
	local scenario="$1" label="$2" root="$3" secret="$4"
	if [ ! -f "${secret}" ]; then
		c4_fail "${scenario}" "${label}" "secret fixture missing"
		return 0
	fi
	if python3 - "${root}" "${secret}" <<'PY'
import os, sys
root, secret_path = sys.argv[1], sys.argv[2]
needle = open(secret_path, "rb").read().strip()
if not needle:
    raise SystemExit(0)
for base, dirs, files in os.walk(root):
    dirs[:] = [d for d in dirs if d not in {".git", "node_modules"}]
    for name in files:
        path = os.path.join(base, name)
        try:
            if needle in open(path, "rb").read():
                raise SystemExit(1)
        except OSError:
            continue
raise SystemExit(0)
PY
	then
		c4_pass "${scenario}" "${label}"
	else
		c4_fail "${scenario}" "${label}" "secret found in tree"
	fi
}

c5_audit_copy() { c4_audit_copy "$1"; }
c5_audit_query() { c4_audit_query "$1" "$2"; }

c5_terminal_probe() {
	local site="$1" out="$2" ticket
	c5_api_session
	ticket="$(c5_api_post "/api/v1/sites/${site}/terminal/session" '{}' | python3 -c 'import json,sys; print(json.load(sys.stdin).get("ticket", ""))')"
	[ -n "${ticket}" ] || c5_die "terminal ticket missing for ${site}"
	( cd "${repo}" && runuser -u "${c5_user}" -- env HOME="${c5_user_home}" \
		PATH="/usr/local/go/bin:/usr/bin:/bin" \
		go run ./test/e2e/database-management/linux/terminal-probe \
		-base "http://127.0.0.1:${ui_port}" -site "${site}" -ticket "${ticket}" ) >"${out}" 2>"${out}.err"
}

c5_php_exec() {
	local site="$1" out="$2" code body
	code='foreach (["DB_CONNECTION","DB_HOST","DB_PORT","DB_DATABASE","DB_USERNAME"] as $k) { echo "E2E_EXEC $k=".getenv($k)."\n"; } echo "E2E_EXEC DB_PASSWORD_PRESENT=".(getenv("DB_PASSWORD") !== false ? "true" : "false")."\n"; echo "E2E_EXEC DATABASE_URL_PRESENT=".(getenv("DATABASE_URL") !== false ? "true" : "false")."\n";'
	body="$(python3 -c 'import json,sys; print(json.dumps({"argv":["-r",sys.argv[1]]}))' "${code}")"
	c5_api_session
	c5_api_post "/api/v1/sites/${site}/php/exec" "${body}" |
		python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("stdout",""), end=""); print(d.get("stderr",""), end="", file=sys.stderr)' >"${out}"
}

c5_secret_scan() {
	local scenario="$1" copy="${private}/state-copy"
	"${cycle4_root}/secret-scan.sh" "${evidence_dir}/${scenario}" "${logs_dir}" "${copy}" \
		>"${evidence_dir}/${scenario}/secret-scan.log" 2>&1
}

c5_record() {
	local scenario="$1" status="$2" priority="${3:-P0}" notes="${4:-}" residual="${5:-}"
	"${cycle4_root}/results.sh" record --id "${scenario}" --priority "${priority}" --environment LIN-UBE \
		--status "${status}" --evidence "${evidence_dir}/${scenario}" \
		--commit "$(git -c safe.directory="${repo}" -C "${repo}" rev-parse HEAD)" \
		--residual-risk "${residual}" --notes "${notes}" >>"${evidence_dir}/${scenario}/checks.log" 2>&1
}

c5_cleanup() {
	c5_stop_daemon
	c5_stop_helper
	if [ "${E2E_KEEP:-0}" = "1" ]; then
		c5_warn "E2E_KEEP=1: leaving ${home} for inspection"
		return 0
	fi
	local project
	project="$(c5_compose_project 2>/dev/null || true)"
	if [ -n "${project}" ] && [ -f "${devstack_home}/services/compose.yaml" ]; then
		docker compose -p "${project}" -f "${devstack_home}/services/compose.yaml" down --remove-orphans --volumes >/dev/null 2>&1 || true
	fi
	if [ -n "${project}" ]; then
		for name in $(docker ps -a --filter "label=com.docker.compose.project=${project}" --format '{{.Names}}'); do
			docker rm -f "${name}" >/dev/null 2>&1 || true
		done
		docker network rm "${project}_default" >/dev/null 2>&1 || true
	fi
	rm -rf "${home}"
}
