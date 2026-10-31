#!/usr/bin/env bash
# Cycle 6 disposable Linux environment for E2E-07/E2E-18.
#
# Reuses the cycle 4/5 disposable helper/daemon home. MySQL and MariaDB run
# without published host ports; PostgreSQL uses its fixed loopback 5432 and
# Redis its fixed loopback 6379 (both free on this host). The real `devstack`
# distro, /data/devstack, helper, agent, and Docker engine are never touched.
#
# Cycle 6 adds backup/import helpers and a private daemon TMPDIR so upload
# staging can be inspected without scanning the shared /tmp.
set -Eeuo pipefail

cycle6_here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${cycle6_here}/cycle5-env.sh"

c6_tmp="${home}/tmp"
c6_backup_root="${devstack_home}/backups/databases"
c6_import_limit=$((1 << 30))
c6_tmpfs_mounts=()

c6_log() { printf 'cycle6: %s\n' "$*"; }
c6_warn() { printf 'cycle6: WARN: %s\n' "$*" >&2; }
c6_die() { printf 'cycle6: ERROR: %s\n' "$*" >&2; exit 1; }
c6_section() { printf '\n== %s ==\n' "$*"; }

c6_guard() {
	c5_guard
	case "${home}" in
	/tmp/devstack-e2e-* | /var/tmp/devstack-e2e-*) ;;
	*) c6_die "refusing home outside /tmp/devstack-e2e-*: ${home}" ;;
	esac
}

c6_init_dirs() {
	c5_init_dirs
	mkdir -p "${c6_tmp}"
	chown "${c5_user}:${c5_user}" "${c6_tmp}"
	chmod 700 "${c6_tmp}"
	export TMPDIR="${c6_tmp}"
}

c6_init() {
	c6_guard
	c6_init_dirs
	c5_write_helper_config
	c5_start_helper
	c5_start_daemon
}

c6_ensure_services() {
	c5_install_mysql_noport
	c5_install_postgresql
	c5_install_node
	# Redis is only needed for the unsupported-operation checks.
	if ! c5_service_active redis; then
		c5_cli --json service install redis >"${logs_dir}/install-redis.json" 2>"${logs_dir}/install-redis.err" ||
			c6_die "redis install failed: $(head -1 "${logs_dir}/install-redis.err")"
		c5_cli --json service activate redis >"${logs_dir}/activate-redis.json" 2>"${logs_dir}/activate-redis.err" ||
			c6_die "redis activation failed: $(head -1 "${logs_dir}/activate-redis.err")"
	fi
	c5_wait_container_healthy devstack-redis 120
}

# --- jobs -----------------------------------------------------------------

c6_json_field() {
	local json="$1" field="$2"
	printf '%s' "${json}" | python3 -c 'import json,sys
try:
    data=json.load(sys.stdin)
except Exception:
    raise SystemExit(0)
value=data
for part in sys.argv[1].split("."):
    if isinstance(value, dict):
        value=value.get(part)
    else:
        value=None
        break
if value is None:
    print("")
elif isinstance(value, bool):
    print("true" if value else "false")
else:
    print(value)' "${field}"
}

c6_job_wait() {
	local job_id="$1" timeout="${2:-900}" start="${SECONDS}" json="" status=""
	[ -n "${job_id}" ] || return 1
	while [ $((SECONDS - start)) -lt "${timeout}" ]; do
		json="$(c5_api_get "/api/v1/jobs/${job_id}" 2>/dev/null || true)"
		status="$(c6_json_field "${json}" status)"
		case "${status}" in
		succeeded | failed | canceled)
			printf '%s' "${json}"
			return 0
			;;
		esac
		sleep 0.5
	done
	printf '%s' "${json}"
	return 1
}

# Run a CLI command that returns a database job, then poll the job to a
# terminal state. Stdout is the terminal job JSON; stderr is saved next to it.
c6_cli_job() {
	local out="$1"
	shift
	local started json job_id terminal
	json="$("$@" 2>"${out}.err")" || {
		printf '%s\n' "${json}" >"${out}"
		return 1
	}
	printf '%s\n' "${json}" >"${out}"
	job_id="$(c6_json_field "${json}" id)"
	[ -n "${job_id}" ] || {
		printf 'missing job id in %s\n' "${out}" >&2
		return 1
	}
	terminal="$(c6_job_wait "${job_id}" "${E2E_JOB_TIMEOUT:-900}")" || {
		printf '%s\n' "${terminal}" >"${out}.terminal"
		return 1
	}
	printf '%s\n' "${terminal}" >"${out}.terminal"
	printf '%s' "${terminal}"
}

# --- API helpers ----------------------------------------------------------

c6_api_request() {
	local method="$1" path="$2" body="${3:-}" out="${4:-}"
	local args=(-sS --max-time 60 -o "${out}" -w '%{http_code}' -X "${method}")
	if [ -n "${body}" ]; then
		args+=(-H 'Content-Type: application/json' --data-binary "${body}")
	fi
	if [ -f "${private}/cookies.txt" ]; then
		args+=(-b "${private}/cookies.txt")
	fi
	if [ -n "${csrf:-}" ]; then
		args+=(-H "X-CSRF-Token: ${csrf}")
	fi
	curl "${args[@]}" "http://127.0.0.1:${ui_port}${path}"
}

c6_api_json_status() {
	local method="$1" path="$2" body="${3:-}" out="${4:-${private}/api-response.json}"
	local code
	code="$(c6_api_request "${method}" "${path}" "${body}" "${out}")"
	printf '%s' "${code}"
}

c6_api_post_json() {
	local path="$1" body="$2" out="${3:-${private}/api-response.json}"
	c5_api_session
	c6_api_json_status POST "${path}" "${body}" "${out}"
}

c6_api_delete() {
	local path="$1" out="${2:-${private}/api-response.json}"
	c5_api_session
	c6_api_json_status DELETE "${path}" "" "${out}"
}

c6_expect_api_code() {
	local scenario="$1" label="$2" want_status="$3" want_code="$4"
	shift 4
	local out="${private}/api-error.json" status
	status="$("$@" "${out}")" || true
	if [ "${status}" != "${want_status}" ]; then
		c4_fail "${scenario}" "${label}" "HTTP ${status}, want ${want_status}"
		return 0
	fi
	if ! grep -q "\"code\":\"${want_code}\"" "${out}"; then
		c4_fail "${scenario}" "${label}" "expected ${want_code}; got $(head -c 200 "${out}")"
		return 0
	fi
	c4_pass "${scenario}" "${label}"
	return 0
}

c6_partial_multipart() {
	local out="${1:-${private}/api-error.json}" body="${private}/partial.multipart"
	c5_api_session
	curl -sS --max-time 30 -o "${out}" -w '%{http_code}' -b "${private}/cookies.txt" \
		-H "X-CSRF-Token: ${csrf}" -H 'Content-Type: multipart/form-data; boundary=e2eboundary' \
		--data-binary @"${body}" \
		"http://127.0.0.1:${ui_port}/api/v1/services/mysql/databases/e2e_import/import"
}

c6_partial_multipart_at() {
	local service="$1" database="$2" body="$3" out="${private}/api-error.json"
	c5_api_session
	curl -sS --max-time 30 -o "${out}" -w '%{http_code}' -b "${private}/cookies.txt" \
		-H "X-CSRF-Token: ${csrf}" -H 'Content-Type: multipart/form-data; boundary=e2eboundary' \
		--data-binary @"${body}" \
		"http://127.0.0.1:${ui_port}/api/v1/services/${service}/databases/${database}/import"
}

c6_api_import_file() {
	local service="$1" database="$2" file="$3" filename="$4" mode="${5:-merge}" confirm="${6:-}" out="${7:-${private}/api-error.json}"
	c5_api_session
	local args=(-sS --max-time 1800 -o "${out}" -w '%{http_code}' -b "${private}/cookies.txt" \
		-H "X-CSRF-Token: ${csrf}" -F "file=@${file};filename=${filename}")
	[ -n "${mode}" ] && args+=(-F "mode=${mode}")
	[ -n "${confirm}" ] && args+=(-F "confirm=${confirm}")
	curl "${args[@]}" "http://127.0.0.1:${ui_port}/api/v1/services/${service}/databases/${database}/import"
}

c6_process_rss_kb() {
	local pid="$1"
	[ -n "${pid}" ] || {
		printf '0'
		return 0
	}
	awk '/^VmRSS:/ {print $2}' "/proc/${pid}/status" 2>/dev/null || printf '0'
}

c6_daemon_process() {
	local pid="${daemon_pid}" child
	child="$(pgrep -P "${pid}" 2>/dev/null | head -1 || true)"
	if [ -n "${child}" ]; then
		printf '%s' "${child}"
	else
		printf '%s' "${pid}"
	fi
}

# --- backup/import helpers ------------------------------------------------

c6_backup_list() {
	local service="$1"
	c5_cli --json db backup list --service "${service}"
}

c6_backup_json() {
	local service="$1" backup_id="$2"
	c6_backup_list "${service}" | python3 -c 'import json,sys
data=json.load(sys.stdin).get("backups", [])
for item in data:
    if item.get("id") == sys.argv[1]:
        print(json.dumps(item, sort_keys=True))
        raise SystemExit(0)
raise SystemExit(1)' "${backup_id}"
}

c6_backup_id_by_kind() {
	local service="$1" kind="$2"
	c6_backup_list "${service}" | python3 -c 'import json,sys
data=json.load(sys.stdin).get("backups", [])
rows=[b for b in data if b.get("kind") == sys.argv[1]]
rows.sort(key=lambda b: b.get("created_at", ""), reverse=True)
if rows:
    print(rows[0]["id"])' "${kind}"
}

c6_backup_file() {
	local service="$1" backup_id="$2" database filename
	database="${backup_id%%--*}"
	filename="${backup_id#*--}"
	printf '%s/%s/%s/%s' "${c6_backup_root}" "${service}" "${database}" "${filename}"
}

c6_backup_kind_from_id() {
	local backup_id="$1" filename
	filename="${backup_id#*--}"
	case "${filename}" in
	*.manual.sql.gz) printf 'manual' ;;
	*.pre_drop.sql.gz) printf 'pre_drop' ;;
	*.pre_restore.sql.gz) printf 'pre_restore' ;;
	*.pre_import.sql.gz) printf 'pre_import' ;;
	*) printf 'unknown' ;;
	esac
}

c6_backup_file_count() {
	local dir="$1"
	[ -d "${dir}" ] || {
		printf '0'
		return 0
	}
	find "${dir}" -maxdepth 1 -type f -name '*.sql.gz' | wc -l | tr -d ' '
}

c6_staging_files() {
	local service="$1"
	local dir="${c6_backup_root}/${service}/.staging"
	[ -d "${dir}" ] || return 0
	find "${dir}" -maxdepth 1 -type f -printf '%f\n' 2>/dev/null || true
}

c6_daemon_staging_files() {
	find "${c6_tmp}" -maxdepth 1 -type f -name 'devstack-import-*' -printf '%f\n' 2>/dev/null || true
}

c6_assert_file_mode() {
	local scenario="$1" label="$2" path="$3" want="$4" got
	got="$(stat -c '%a' "${path}" 2>/dev/null || true)"
	c4_assert_eq "${scenario}" "${label}" "${got}" "${want}"
}

c6_assert_job_status() {
	local scenario="$1" label="$2" job="$3" want="$4"
	c4_assert_eq "${scenario}" "${label}" "$(c6_json_field "${job}" status)" "${want}"
}

c6_expect_cli_code() {
	local scenario="$1" label="$2" want="$3" outfile="$4"
	shift 4
	local output status
	output="$("$@" 2>&1)" && status=0 || status=$?
	printf '%s\n' "${output}" >"${outfile}"
	if [ "${status}" -eq 0 ]; then
		c4_fail "${scenario}" "${label}" "command succeeded; expected ${want}"
		return 0
	fi
	if ! printf '%s' "${output}" | grep -q "\"code\":\"${want}\""; then
		c4_fail "${scenario}" "${label}" "expected ${want}; got $(printf '%s' "${output}" | head -c 240)"
		return 0
	fi
	c4_pass "${scenario}" "${label}"
	return 0
}

c6_assert_json_field() {
	local scenario="$1" label="$2" json="$3" field="$4" want="$5"
	c4_assert_eq "${scenario}" "${label}" "$(c6_json_field "${json}" "${field}")" "${want}"
}

c6_wait_service_state() {
	local service="$1" want="$2" timeout="${3:-120}" json state
	for _ in $(seq 1 "${timeout}"); do
		json="$(c5_service_json || true)"
		state="$(printf '%s' "${json}" | python3 -c 'import json,sys
try:
    rows=json.load(sys.stdin)
except Exception:
    raise SystemExit(0)
for row in rows:
    if row.get("id") == sys.argv[1]:
        print("active" if row.get("active") and row.get("state") == "active" else "inactive")
        break' "${service}")"
		[ "${state}" = "${want}" ] && return 0
		sleep 1
	done
	return 1
}

# Kill the daemon process without a graceful shutdown so the job rows stay
# running and the restart reconciliation path is exercised.
c6_crash_daemon() {
	local pid="${daemon_pid}"
	pkill -9 -f "devstack.*--home ${devstack_home}.*daemon run" 2>/dev/null || true
	if [ -n "${pid}" ]; then
		kill -9 "${pid}" 2>/dev/null || true
	fi
	wait "${pid}" 2>/dev/null || true
	daemon_pid=""
	for _ in $(seq 1 100); do
		if ! pgrep -f "devstack.*--home ${devstack_home}.*daemon run" >/dev/null 2>&1; then
			break
		fi
		sleep 0.1
	done
	rm -f "${devstack_home}/run/daemon.ready" "${devstack_home}/run/daemon.pid" "${devstack_home}/run/daemon.start"
	sleep 1
}

# --- tmpfs disk-full simulation ------------------------------------------

c6_mount_tmpfs() {
	local dir="$1" size="${2:-8m}"
	mkdir -p "${dir}"
	mount -t tmpfs -o "size=${size}" tmpfs "${dir}"
	c6_tmpfs_mounts+=("${dir}")
}

c6_umount_tmpfs() {
	local dir="$1" remaining=()
	for mounted in "${c6_tmpfs_mounts[@]:-}"; do
		if [ "${mounted}" = "${dir}" ]; then
			umount "${dir}" 2>/dev/null || true
		else
			remaining+=("${mounted}")
		fi
	done
	c6_tmpfs_mounts=("${remaining[@]:-}")
}

c6_umount_all() {
	local mounted
	for mounted in "${c6_tmpfs_mounts[@]:-}"; do
		[ -n "${mounted}" ] && umount "${mounted}" 2>/dev/null || true
	done
	c6_tmpfs_mounts=()
}

# --- misc -----------------------------------------------------------------

c6_audit_copy() { c4_audit_copy "$1"; }
c6_audit_query() { c4_audit_query "$1" "$2"; }

c6_secret_scan() {
	local scenario="$1" copy="${private}/state-copy-${scenario}"
	c6_audit_copy "${copy}" >/dev/null 2>&1 || true
	"${cycle4_root}/secret-scan.sh" "${evidence_dir}/${scenario}" "${logs_dir}" "${copy}" \
		>"${evidence_dir}/${scenario}/secret-scan.log" 2>&1
}

c6_record() {
	local scenario="$1" status="$2" priority="${3:-P0}" notes="${4:-}" residual="${5:-}"
	"${cycle4_root}/results.sh" record --id "${scenario}" --priority "${priority}" --environment LIN-UBE \
		--status "${status}" --evidence "${evidence_dir}/${scenario}" \
		--commit "$(git -c safe.directory="${repo}" -C "${repo}" rev-parse HEAD)" \
		--residual-risk "${residual}" --notes "${notes}" >>"${evidence_dir}/${scenario}/checks.log" 2>&1
}

c6_cleanup() {
	c6_umount_all
	c5_stop_daemon
	c5_stop_helper
	if [ "${E2E_KEEP:-0}" = "1" ]; then
		c6_warn "E2E_KEEP=1: leaving ${home} for inspection"
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
