#!/usr/bin/env bash
# Verify the disposable database-management harness.
#
# Read-only except for scratch rows/databases that are created and removed in
# the same run. It never talks to the real install, the real WSL agent, or the
# real Docker engine.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

engines="all"
while [ $# -gt 0 ]; do
	case "$1" in
	--engine) engines="$2"; shift 2 ;;
	--home) home="$2"; shift 2 ;;
	-h | --help) printf 'usage: verify.sh [--engine all|mysql|mariadb|postgres|redis] [--home DIR]\n'; exit 0 ;;
	*) printf 'verify.sh: unknown argument %s\n' "$1" >&2; exit 2 ;;
	esac
done

e2e_guard
e2e_refresh_paths
e2e_require_docker
mkdir -p "${logs_dir}"
exec > >(tee -a "${logs_dir}/verify.log") 2>&1

fail() { e2e_die "$*"; }
expect_fail() {
	local label="$1"
	shift
	if "$@" >/dev/null 2>&1; then
		fail "expected failure but command succeeded: ${label}"
	fi
}

expect_redis_denied() {
	local label="$1"
	shift
	local output
	output="$("$@" 2>&1 || true)"
	case "${output}" in
	*NOPERM*|*NOAUTH*|*WRONGPASS*|*"AUTH failed"*) ;;
	*) fail "expected Redis denial: ${label} (got: ${output})" ;;
	esac
}

verify_images() {
	e2e_section "pinned images"
	local image digest
	for image in "${mysql_image}" "${mariadb_image}" "${postgres_image}" "${redis_image}"; do
		docker image inspect "${image}" >/dev/null 2>&1 || fail "missing pinned image ${image}"
		digest="${image##*@}"
		if ! docker image inspect --format '{{json .RepoDigests}}' "${image}" | grep -q "${digest}"; then
			fail "image ${image} does not expose its pinned digest"
		fi
		e2e_log "image present: ${image}"
	done
}

verify_container_layout() {
	e2e_section "container and network isolation"
	local service container
	for service in mysql mariadb postgres redis; do
		container="$(e2e_container_name "${service}")"
		[ "$(docker inspect -f '{{.State.Running}}' "${container}" 2>/dev/null || true)" = "true" ] ||
			fail "${service} container is not running"
		[ "$(docker inspect -f '{{index .Config.Labels "devstack.e2e.run"}}' "${container}")" = "${run_key}" ] ||
			fail "${service} container label mismatch"
		if [ "${E2E_PUBLISH_PORTS:-0}" != "1" ]; then
			[ -z "$(docker port "${container}" 2>/dev/null || true)" ] ||
				fail "${service} container publishes a host port while E2E_PUBLISH_PORTS!=1"
		fi
	done
	[ -n "$(docker network inspect --format '{{.Id}}' "${network}" 2>/dev/null || true)" ] ||
		fail "network ${network} is missing"
	e2e_log "network ${network} and four labelled containers are isolated"
}

verify_mysql_family() {
	local engine="$1" count grants
	e2e_section "verify ${engine}"
	[ "$(e2e_mysql_query "${engine}" e2e_app 'SELECT COUNT(*) FROM e2e_marker')" = "3" ] ||
		fail "${engine}: marker row count"
	[ "$(e2e_mysql_query "${engine}" e2e_app "SELECT COUNT(*) FROM e2e_marker WHERE note LIKE '%unicode%'")" = "1" ] ||
		fail "${engine}: unicode marker missing"
	[ "$(e2e_mysql_query "${engine}" e2e_empty 'SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()')" = "0" ] ||
		fail "${engine}: e2e_empty is not empty"
	[ "$(e2e_mysql_query "${engine}" e2e_reserved 'SELECT COUNT(*) FROM `select`')" = "1" ] ||
		fail "${engine}: reserved identifiers"
	[ "$(e2e_mysql_query "${engine}" e2e_case 'SELECT COUNT(*) FROM CaseProbe')" = "1" ] ||
		fail "${engine}: case identifier"
	[ "$(e2e_mysql_query "${engine}" e2e_fk 'SELECT COUNT(*) FROM child_view')" = "1" ] ||
		fail "${engine}: foreign-key view"
	[ "$(e2e_mysql_query "${engine}" e2e_fk 'SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() AND TRIGGER_NAME = "child_touch"')" = "1" ] ||
		fail "${engine}: trigger missing"
	[ "$(e2e_mysql_query "${engine}" e2e_binary 'SELECT LENGTH(payload) FROM blobs WHERE id = 1')" = "48" ] ||
		fail "${engine}: binary payload length"
	[ "$(e2e_mysql_query "${engine}" e2e_routines 'SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = DATABASE() AND ROUTINE_NAME IN ("e2e_proc", "e2e_fn")')" = "2" ] ||
		fail "${engine}: routines missing"

	[ "$(e2e_mysql_query "${engine}" - "SELECT COUNT(*) FROM mysql.user WHERE User = 'e2e_user' AND Host = '%'")" = "1" ] ||
		fail "${engine}: e2e_user missing"
	[ "$(e2e_mysql_query "${engine}" - "SELECT COUNT(*) FROM mysql.user WHERE User = 'e2e_user_ro' AND Host = '%'")" = "1" ] ||
		fail "${engine}: e2e_user_ro missing"
	[ "$(e2e_mysql_query "${engine}" - "SELECT COUNT(*) FROM mysql.user WHERE User = 'e2e_user_host_local' AND Host = 'localhost'")" = "1" ] ||
		fail "${engine}: localhost host scope missing"
	[ "$(e2e_mysql_query "${engine}" - "SELECT COUNT(*) FROM mysql.user WHERE User = 'e2e_user_host_net' AND Host = '10.%'")" = "1" ] ||
		fail "${engine}: 10.% host scope missing"

	grants="$(e2e_mysql_query "${engine}" - "SHOW GRANTS FOR 'e2e_user'@'%'")"
	e2e_assert_contains "${grants}" "e2e_app" "${engine} e2e_user grant"
	grants="$(e2e_mysql_query "${engine}" - "SHOW GRANTS FOR 'e2e_user_ro'@'%'")"
	e2e_assert_contains "${grants}" "SELECT" "${engine} e2e_user_ro grant"
	grants="$(e2e_mysql_query "${engine}" - "SHOW GRANTS FOR 'e2e_user_admin'@'%'")"
	e2e_assert_contains "${grants}" "ALL PRIVILEGES" "${engine} e2e_user_admin grant"

	local rw_pw ro_pw
	rw_pw="$(e2e_secret_get "${engine}-e2e_user")"
	ro_pw="$(e2e_secret_get "${engine}-e2e_user_ro")"
	[ "$(e2e_mysql_user_query "${engine}" e2e_app e2e_user "${rw_pw}" 'SELECT COUNT(*) FROM e2e_marker')" = "3" ] ||
		fail "${engine}: read-write login failed"
	e2e_mysql_user_query "${engine}" e2e_app e2e_user "${rw_pw}" \
		"INSERT INTO e2e_marker (id, label) VALUES (99, 'verify-rw') ON DUPLICATE KEY UPDATE label = VALUES(label)" >/dev/null ||
		fail "${engine}: read-write insert failed"
	e2e_mysql_query "${engine}" e2e_app 'DELETE FROM e2e_marker WHERE id = 99' >/dev/null
	[ "$(e2e_mysql_user_query "${engine}" e2e_app e2e_user_ro "${ro_pw}" 'SELECT COUNT(*) FROM e2e_marker')" = "3" ] ||
		fail "${engine}: read-only login failed"
	expect_fail "${engine}: read-only insert" \
		e2e_mysql_user_query "${engine}" e2e_app e2e_user_ro "${ro_pw}" \
		"INSERT INTO e2e_marker (id, label) VALUES (100, 'verify-ro')"
	expect_fail "${engine}: wrong password" \
		e2e_mysql_user_query "${engine}" e2e_app e2e_user "definitely-wrong-password" \
		'SELECT COUNT(*) FROM e2e_marker'

	local dump="${home}/backups/${engine}/e2e_app.sql.gz"
	[ -s "${dump}" ] || fail "${engine}: backup file missing"
	gzip -t "${dump}" || fail "${engine}: backup gzip invalid"
	[ "$(e2e_sha256 "${dump}")" = "$(cat "${dump}.sha256")" ] || fail "${engine}: backup checksum mismatch"
	e2e_log "${engine}: databases, users, grants, logins, backup, and fixtures verified"
}

verify_postgres() {
	e2e_section "verify postgres"
	[ "$(e2e_psql_query e2e_app 'SELECT count(*) FROM e2e_marker')" = "3" ] || fail "postgres: marker row count"
	[ "$(e2e_psql_query e2e_app "SELECT count(*) FROM e2e_marker WHERE note LIKE '%unicode%'")" = "1" ] || fail "postgres: unicode marker missing"
	[ "$(e2e_psql_query e2e_empty "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema')")" = "0" ] || fail "postgres: e2e_empty is not empty"
	[ "$(e2e_psql_query e2e_reserved 'SELECT count(*) FROM "select"')" = "1" ] || fail "postgres: reserved identifiers"
	[ "$(e2e_psql_query e2e_case 'SELECT count(*) FROM "CaseProbe"')" = "1" ] || fail "postgres: case identifier"
	[ "$(e2e_psql_query e2e_fk 'SELECT count(*) FROM child_view')" = "1" ] || fail "postgres: foreign-key view"
	[ "$(e2e_psql_query e2e_binary 'SELECT octet_length(payload) FROM blobs WHERE id = 1')" = "48" ] || fail "postgres: binary payload length"
	[ "$(e2e_psql_query e2e_routines 'SELECT e2e_fn(41)')" = "42" ] || fail "postgres: function result"
	[ "$(e2e_psql_query e2e_routines "SELECT count(*) FROM pg_proc WHERE proname = 'e2e_proc'")" = "1" ] || fail "postgres: procedure missing"

	[ "$(e2e_psql_query postgres "SELECT count(*) FROM pg_roles WHERE rolname IN ('e2e_user','e2e_user_ro','e2e_user_admin')")" = "3" ] ||
		fail "postgres: roles missing"
	[ "$(e2e_psql_query e2e_app "SELECT has_table_privilege('e2e_user','e2e_marker','SELECT')")" = "t" ] ||
		fail "postgres: e2e_user select privilege"
	[ "$(e2e_psql_query e2e_app "SELECT has_table_privilege('e2e_user_ro','e2e_marker','INSERT')")" = "f" ] ||
		fail "postgres: e2e_user_ro unexpectedly has INSERT"
	[ "$(e2e_psql_query e2e_app "SELECT has_table_privilege('e2e_user_admin','e2e_marker','DELETE')")" = "t" ] ||
		fail "postgres: e2e_user_admin delete privilege"

	local rw_pw ro_pw
	rw_pw="$(e2e_secret_get postgres-e2e_user)"
	ro_pw="$(e2e_secret_get postgres-e2e_user_ro)"
	[ "$(e2e_psql_network_query e2e_app e2e_user "${rw_pw}" 'SELECT count(*) FROM e2e_marker')" = "3" ] ||
		fail "postgres: read-write login failed"
	e2e_psql_network_query e2e_app e2e_user "${rw_pw}" \
		"INSERT INTO e2e_marker (id, label) VALUES (99, 'verify-rw') ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label" >/dev/null ||
		fail "postgres: read-write insert failed"
	e2e_psql_query e2e_app 'DELETE FROM e2e_marker WHERE id = 99' >/dev/null
	[ "$(e2e_psql_network_query e2e_app e2e_user_ro "${ro_pw}" 'SELECT count(*) FROM e2e_marker')" = "3" ] ||
		fail "postgres: read-only login failed"
	expect_fail "postgres: read-only insert" \
		e2e_psql_network_query e2e_app e2e_user_ro "${ro_pw}" \
		"INSERT INTO e2e_marker (id, label) VALUES (100, 'verify-ro')"
	expect_fail "postgres: wrong password" \
		e2e_psql_network_query e2e_app e2e_user "definitely-wrong-password" \
		'SELECT count(*) FROM e2e_marker'

	local dump="${home}/backups/postgres/e2e_app.sql.gz"
	[ -s "${dump}" ] || fail "postgres: backup file missing"
	gzip -t "${dump}" || fail "postgres: backup gzip invalid"
	[ "$(e2e_sha256 "${dump}")" = "$(cat "${dump}.sha256")" ] || fail "postgres: backup checksum mismatch"
	e2e_log "postgres: databases, roles, grants, logins, backup, and fixtures verified"
}

verify_redis() {
	e2e_section "verify redis"
	local ro_pw full_pw custom_pw acl
	ro_pw="$(e2e_secret_get redis-e2e_cache_ro)"
	full_pw="$(e2e_secret_get redis-e2e_cache_full)"
	custom_pw="$(e2e_secret_get redis-e2e_cache_custom)"
	acl="$(e2e_redis_cli ACL LIST)"
	for user in e2e_cache_ro e2e_cache_full e2e_cache_custom; do
		e2e_assert_contains "${acl}" "user ${user} on" "redis ${user} ACL"
	done
	[ "$(e2e_redis_user_cli e2e_cache_ro "${ro_pw}" GET e2e:seed:key)" = "seed-value" ] ||
		fail "redis: read-only GET failed"
	expect_redis_denied "redis: read-only SET" e2e_redis_user_cli e2e_cache_ro "${ro_pw}" SET e2e:ro:key nope
	[ "$(e2e_redis_user_cli e2e_cache_full "${full_pw}" SET e2e:full:key full-value)" = "OK" ] ||
		fail "redis: full SET failed"
	expect_redis_denied "redis: full CONFIG" e2e_redis_user_cli e2e_cache_full "${full_pw}" CONFIG GET maxmemory
	expect_redis_denied "redis: full FLUSHALL" e2e_redis_user_cli e2e_cache_full "${full_pw}" FLUSHALL
	[ "$(e2e_redis_user_cli e2e_cache_custom "${custom_pw}" GET e2e:custom:key)" = "custom-value" ] ||
		fail "redis: custom GET failed"
	[ "$(e2e_redis_user_cli e2e_cache_custom "${custom_pw}" SET e2e:custom:key custom-2)" = "OK" ] ||
		fail "redis: custom SET failed"
	expect_redis_denied "redis: custom key scope" e2e_redis_user_cli e2e_cache_custom "${custom_pw}" SET other:key nope
	expect_redis_denied "redis: wrong password" e2e_redis_user_cli e2e_cache_ro definitely-wrong-password GET e2e:seed:key

	docker exec "$(e2e_container_name redis)" test -s /data/users.acl || fail "redis: users.acl missing"
	for secret in "${ro_pw}" "${full_pw}" "${custom_pw}"; do
		if docker exec "$(e2e_container_name redis)" grep -Fq -- "${secret}" /data/users.acl; then
			fail "redis: users.acl contains a plaintext password"
		fi
	done
	[ -s "${home}/redis/fixture/users.acl" ] || fail "redis: generated ACL fixture missing"

	# ACL persistence across a restart.
	docker restart "$(e2e_container_name redis)" >/dev/null
	e2e_wait_service redis
	[ "$(e2e_redis_user_cli e2e_cache_ro "${ro_pw}" GET e2e:seed:key)" = "seed-value" ] ||
		fail "redis: ACL user did not survive restart"
	e2e_log "redis: ACL users, restrictions, hashes, and restart persistence verified"
}

verify_site_links() {
	e2e_section "verify site links"
	[ -s "${home}/sites/links.json" ] || fail "site link fixture missing"
	python3 - "${home}/sites/links.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
links = data.get("links", [])
assert len(links) == 2, links
assert sum(1 for link in links if link.get("primary")) == 1, links
for link in links:
    assert link.get("database") == "e2e_app", link
    assert link.get("password_managed") is True, link
    assert link["env"]["DB_DATABASE"] == "e2e_app", link
    def walk(value):
        if isinstance(value, dict):
            for key, child in value.items():
                if key.lower() in {"password", "secret", "token"}:
                    assert child in (None, "", False), (key, child)
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)
    walk(link)
PY
	if [ "${E2E_DEVSTACK_DAEMON:-0}" = "1" ]; then
		[ -x "${ds}" ] || fail "E2E_DEVSTACK_DAEMON=1 but ${ds} is not executable"
		local links_json
		links_json="$("${ds}" --home "${home}" --json site db list e2e-site)" ||
			fail "devstack site db list failed"
		e2e_assert_contains "${links_json}" "e2e_app" "devstack site links"
		if printf '%s' "${links_json}" | grep -Eiq '"(password|secret|token)"[[:space:]]*:[[:space:]]*"[^"]+"'; then
			fail "devstack site links leaked a secret field"
		fi
	fi
	e2e_log "site link fixture verified (no password field)"
}

verify_backup_fixtures() {
	e2e_section "verify dump fixtures"
	local dumps="${home}/fixtures/dumps"
	gzip -t "${dumps}/seed.sql.gz" || fail "valid gzip fixture is invalid"
	gzip -t "${dumps}/seed-postgres.sql.gz" || fail "valid PostgreSQL gzip fixture is invalid"
	expect_fail "corrupt gzip fixture" gzip -t "${dumps}/corrupt.sql.gz"
	expect_fail "truncated gzip fixture" gzip -t "${dumps}/truncated.sql.gz"
	[ -s "${dumps}/non-sql.txt" ] || fail "non-SQL fixture missing"
	[ -f "${dumps}/empty.sql" ] && [ ! -s "${dumps}/empty.sql" ] || fail "empty SQL fixture missing"
	e2e_log "gzip, corrupt, truncated, non-SQL, and empty fixtures verified"
}

case "${engines}" in
all) engine_list="mysql mariadb postgres redis" ;;
mysql | mariadb | postgres | redis) engine_list="${engines}" ;;
postgresql) engine_list="postgres" ;;
*) fail "unknown --engine ${engines}" ;;
esac

verify_images
verify_container_layout
for engine in ${engine_list}; do
	case "${engine}" in
	mysql | mariadb) verify_mysql_family "${engine}" ;;
	postgres) verify_postgres ;;
	redis) verify_redis ;;
	esac
done
verify_site_links
verify_backup_fixtures
"${e2e_root}/secret-scan.sh" "${logs_dir}" >/dev/null ||
	fail "secret scanner found a secret in ${logs_dir}"

e2e_log "VERIFY OK (${engines})"
