#!/usr/bin/env bash
# Cycle 7 disposable Linux environment for E2E-08/E2E-19/E2E-20.
#
# Reuses the cycle 4/5/6 disposable helper/daemon home. Redis uses its fixed
# loopback 6379 (free on this host). MySQL/MariaDB run without published host
# ports and phpMyAdmin uses a unique high loopback port because the real install
# owns 3306/8082. The real `devstack` distro, /data/devstack, helper, agent, and
# Docker engine are never touched.
set -Eeuo pipefail

cycle7_here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle6-env.sh
source "${cycle7_here}/cycle6-env.sh"

c7_redis_container="devstack-redis"
c7_redis_secret_dir="${devstack_home}/services/redis/secrets/database-users"
c7_redis_config="${devstack_home}/services/redis/config/redis.conf"
c7_redis_acl="${devstack_home}/services/redis/data/users.acl"
c7_redis_marker="${devstack_home}/services/redis/config/.aclfile-restart-pending"
c7_events_file=""

c7_log() { printf 'cycle7: %s\n' "$*"; }
c7_warn() { printf 'cycle7: WARN: %s\n' "$*" >&2; }
c7_die() { printf 'cycle7: ERROR: %s\n' "$*" >&2; exit 1; }
c7_section() { printf '\n== %s ==\n' "$*"; }

c7_guard() {
	c6_guard
	command -v sha256sum >/dev/null 2>&1 || c7_die "sha256sum is required"
	command -v curl >/dev/null 2>&1 || c7_die "curl is required"
	command -v python3 >/dev/null 2>&1 || c7_die "python3 is required"
}

c7_init() {
	c7_guard
	c6_init
}

# --- Redis helpers --------------------------------------------------------

c7_redis_user_id() {
	python3 -c 'import hashlib,sys; print("redis_"+hashlib.sha256(sys.argv[1].encode()).hexdigest()[:16])' "$1"
}

c7_redis_secret_path() {
	printf '%s/%s' "${c7_redis_secret_dir}" "$(c7_redis_user_id "$1")"
}

c7_redis_password() {
	local path
	path="$(c7_redis_secret_path "$1")"
	[ -f "${path}" ] || return 1
	cat "${path}"
}

c7_redis_default_id() { c7_redis_user_id default; }

# Run redis-cli as the default user. The stored default password, when present,
# is piped to --askpass and never appears in argv or the host process list.
c7_redis_cli() {
	local secret
	if secret="$(c7_redis_password default 2>/dev/null)"; then
		printf '%s\n' "${secret}" | docker exec -i "${c7_redis_container}" sh -c '
			IFS= read -r pw || true
			printf "%s\n" "$pw" | redis-cli --askpass "$@"
		' sh "$@"
	else
		docker exec -i "${c7_redis_container}" redis-cli "$@"
	fi
}

# Run redis-cli as an ACL user. The password is read from stdin and fed to
# --askpass; it is never an argv element.
c7_redis_cli_as() {
	local user="$1" secret="$2"
	shift 2
	printf '%s\n' "${secret}" | docker exec -i "${c7_redis_container}" sh -c '
		IFS= read -r pw || true
		user="$1"
		shift
		printf "%s\n" "$pw" | redis-cli --user "$user" --askpass "$@"
	' sh "${user}" "$@"
}

c7_redis_cli_as_file() {
	local user="$1" file="$2"
	shift 2
	[ -f "${file}" ] || return 1
	c7_redis_cli_as "${user}" "$(cat "${file}")" "$@"
}

c7_redis_install() {
	if ! c5_service_active redis; then
		c5_cli --json service install redis >"${logs_dir}/install-redis.json" 2>"${logs_dir}/install-redis.err" ||
			c7_die "redis install failed: $(head -1 "${logs_dir}/install-redis.err")"
		c5_cli --json service activate redis >"${logs_dir}/activate-redis.json" 2>"${logs_dir}/activate-redis.err" ||
			c7_die "redis activation failed: $(head -1 "${logs_dir}/activate-redis.err")"
	fi
	c5_wait_container_healthy "${c7_redis_container}" 120
}

c7_redis_config_hash() {
	sha256sum "${c7_redis_config}" | awk '{print $1}'
}

c7_redis_acl_hash() {
	[ -f "${c7_redis_acl}" ] || {
		printf 'missing'
		return 0
	}
	sha256sum "${c7_redis_acl}" | awk '{print $1}'
}

c7_redis_started_at() {
	docker inspect -f '{{.State.StartedAt}}' "${c7_redis_container}" 2>/dev/null || true
}

c7_redis_restart_count() {
	docker inspect -f '{{.RestartCount}}' "${c7_redis_container}" 2>/dev/null || true
}

c7_redis_events_start() {
	local out="${private}/redis-events.log" pid
	: >"${out}"
	docker events --filter "container=${c7_redis_container}" --filter event=restart --format '{{.Time}} {{.Action}}' >>"${out}" 2>/dev/null &
	pid=$!
	sleep 0.5
	printf '%s\n' "${pid}"
}

c7_redis_events_stop() {
	local pid="$1"
	kill "${pid}" 2>/dev/null || true
	wait "${pid}" 2>/dev/null || true
}

c7_redis_events_count() {
	local file="${private}/redis-events.log"
	[ -f "${file}" ] || {
		printf '0'
		return 0
	}
	awk '/ restart$/ {n++} END {print n+0}' "${file}" 2>/dev/null || printf '0'
}

# Wait until Redis answers PING as default, for post-restart checks.
c7_redis_wait() {
	local timeout="${1:-60}"
	for _ in $(seq 1 "${timeout}"); do
		if c7_redis_cli PING 2>/dev/null | grep -q PONG; then
			return 0
		fi
		sleep 1
	done
	return 1
}

# --- service / config helpers ---------------------------------------------

c7_service_container() {
	printf 'devstack-%s' "$1"
}

c7_service_active_state() {
	local id="$1" json
	json="$(c5_service_json || true)"
	[ -n "${json}" ] || return 1
	python3 -c 'import json,sys
rows=json.load(sys.stdin)
for row in rows:
    if row.get("id")==sys.argv[1]:
        print("active" if row.get("active") and row.get("state")=="active" else "inactive")
        raise SystemExit(0)
print("missing")' "${id}" <<<"${json}"
}

c7_wait_service_active() {
	local id="$1" timeout="${2:-120}"
	for _ in $(seq 1 "${timeout}"); do
		[ "$(c7_service_active_state "${id}")" = "active" ] && return 0
		sleep 1
	done
	return 1
}

# --- assertion helpers ----------------------------------------------------

c7_assert_file_contains() {
	local scenario="$1" label="$2" file="$3" needle="$4"
	if [ -f "${file}" ] && grep -Fq -- "${needle}" "${file}"; then
		c4_pass "${scenario}" "${label}"
	else
		c4_fail "${scenario}" "${label}" "missing '${needle}' in ${file}"
	fi
}

c7_assert_file_not_contains() {
	local scenario="$1" label="$2" file="$3" needle="$4"
	if [ ! -f "${file}" ]; then
		c4_fail "${scenario}" "${label}" "missing file ${file}"
		return 0
	fi
	if grep -Fq -- "${needle}" "${file}"; then
		c4_fail "${scenario}" "${label}" "unexpected secret/pattern in ${file}"
	else
		c4_pass "${scenario}" "${label}"
	fi
}

# Assert that a file does not contain the exact value stored in secret_file.
# Both paths are passed to Python; the secret never appears in argv.
c7_assert_file_lacks_secret() {
	local scenario="$1" label="$2" file="$3" secret_file="$4"
	if [ ! -f "${file}" ] || [ ! -f "${secret_file}" ]; then
		c4_fail "${scenario}" "${label}" "file or secret fixture missing"
		return 0
	fi
	if python3 - "${file}" "${secret_file}" <<'PY'
import sys
needle = open(sys.argv[2], "rb").read().strip()
if not needle:
    raise SystemExit(0)
raise SystemExit(1 if needle in open(sys.argv[1], "rb").read() else 0)
PY
	then
		c4_pass "${scenario}" "${label}"
	else
		c4_fail "${scenario}" "${label}" "secret found in ${file}"
	fi
}

# Assert that a text file (for example list JSON) lacks the exact secret.
c7_assert_text_lacks_secret() {
	c7_assert_file_lacks_secret "$@"
}


c7_assert_no_secret_in_tree() {
	local scenario="$1" label="$2" root="$3" secret_file="$4"
	if [ ! -f "${secret_file}" ]; then
		c4_fail "${scenario}" "${label}" "secret fixture missing"
		return 0
	fi
	if python3 - "${root}" "${secret_file}" <<'PY'
import os, sys
root, secret_path = sys.argv[1], sys.argv[2]
needle = open(secret_path, "rb").read().strip()
if not needle:
    raise SystemExit(0)
for base, dirs, files in os.walk(root):
    dirs[:] = [d for d in dirs if d not in {".git", "node_modules"}]
    for name in files:
        path = os.path.join(base, name)
        if path.endswith((".acl", ".png", ".jpg", ".jpeg", ".zip", ".gz")):
            continue
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

c7_assert_acl_hashes_only() {
	local scenario="$1" label="$2" acl_file="$3" secret_file="$4"
	if [ ! -f "${acl_file}" ] || [ ! -f "${secret_file}" ]; then
		c4_fail "${scenario}" "${label}" "ACL/secret file missing"
		return 0
	fi
	if python3 - "${acl_file}" "${secret_file}" <<'PY'
import sys
acl = open(sys.argv[1], "rb").read()
secret = open(sys.argv[2], "rb").read().strip()
if not secret:
    raise SystemExit(0)
raise SystemExit(1 if secret in acl else 0)
PY
	then
		c4_pass "${scenario}" "${label}"
	else
		c4_fail "${scenario}" "${label}" "plaintext secret in users.acl"
	fi
}

# --- audit / scan ---------------------------------------------------------

c7_audit_copy() { c6_audit_copy "$1"; }
c7_audit_query() { c6_audit_query "$1" "$2"; }

c7_scan() {
	local scenario="$1" copy="${private}/state-copy-${scenario}"
	c7_audit_copy "${copy}" >/dev/null 2>&1 || true
	"${cycle4_root}/secret-scan.sh" "${evidence_dir}/${scenario}" "${logs_dir}" "${copy}" \
		>"${evidence_dir}/${scenario}/secret-scan.log" 2>&1 || true
	if grep -q 'secret-scan: OK' "${evidence_dir}/${scenario}/secret-scan.log"; then
		c4_pass "${scenario}" "secret scanner clean over evidence, logs, and state copy"
	else
		c4_fail "${scenario}" "secret scanner found a hit" "$(tail -1 "${evidence_dir}/${scenario}/secret-scan.log")"
	fi
}

c7_record() {
	local scenario="$1" status="$2" priority="${3:-P0}" notes="${4:-}" residual="${5:-}"
	"${cycle4_root}/results.sh" record --id "${scenario}" --priority "${priority}" --environment LIN-UBE \
		--status "${status}" --evidence "${evidence_dir}/${scenario}" \
		--commit "$(git -c safe.directory="${repo}" -C "${repo}" rev-parse HEAD)" \
		--residual-risk "${residual}" --notes "${notes}" >>"${evidence_dir}/${scenario}/checks.log" 2>&1
}

c7_cleanup() {
	c6_cleanup
}
