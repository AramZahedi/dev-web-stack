#!/usr/bin/env bash
# Seed the disposable database-management harness.
#
# Default flow (direct-engine mode):
#   1. start the pinned MySQL/MariaDB/PostgreSQL/Redis containers if needed;
#   2. create databases, tables, rows, users, and grants;
#   3. create a logical backup and verify a restore and a gzip import;
#   4. seed Redis ACL users and persist users.acl;
#   5. write a site-link fixture (and use the real CLI when E2E_DEVSTACK_DAEMON=1).
#
# Every password is generated at runtime and stored only under
# $home/secrets (mode 0600). Re-running is idempotent: the same secret files and
# object names are reused, and every SQL statement is create-or-update.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

engines="all"
start=0
do_backup=1
do_site_links=1

usage() {
	cat <<'EOF'
usage: seed.sh [--engine all|mysql|mariadb|postgres|redis] [--home DIR]
               [--start] [--no-backup] [--no-site-links]
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--engine) engines="$2"; shift 2 ;;
	--home) home="$2"; shift 2 ;;
	--start) start=1; shift ;;
	--no-backup) do_backup=0; shift ;;
	--no-site-links) do_site_links=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) printf 'seed.sh: unknown argument %s\n' "$1" >&2; usage >&2; exit 2 ;;
	esac
done

e2e_guard
e2e_refresh_paths
e2e_require_docker
e2e_init_dirs
export E2E_HOME="${home}" E2E_STAMP="${stamp}" E2E_RUN_KEY="${run_key}" E2E_NETWORK="${network}"
export E2E_MYSQL_IMAGE="${mysql_image}" E2E_MARIADB_IMAGE="${mariadb_image}"
export E2E_POSTGRES_IMAGE="${postgres_image}" E2E_REDIS_IMAGE="${redis_image}"
export E2E_PHPMYADMIN_IMAGE="${phpmyadmin_image}"

generate_fixtures() {
	local out="${home}/fixtures/dumps"
	mkdir -p "${out}"
	"${e2e_root}/fixtures/generate.sh" --out "${out}" --check
	"${e2e_root}/fixtures/generate.sh" --out "${out}" \
		--redis-acl "${home}/redis/fixture" --secret-dir "${secrets_dir}"
}

substitute_passwords() {
	local template="$1" output="$2" engine="$3"
	cp "${template}" "${output}"
	chmod 600 "${output}"
	local name value upper
	for name in e2e_user e2e_user_ro e2e_user_admin e2e_user_case e2e_user_host_local e2e_user_host_net; do
		upper="$(printf '%s' "${name}" | tr '[:lower:]' '[:upper:]')"
		if grep -q "__${upper}_PASSWORD__" "${template}"; then
			value="$(e2e_secret_get "${engine}-${name}")"
			sed -i "s/__${upper}_PASSWORD__/${value}/g" "${output}"
		fi
	done
}

ensure_user_secrets() {
	local engine="$1" name
	for name in e2e_user e2e_user_ro e2e_user_admin e2e_user_case e2e_user_host_local e2e_user_host_net; do
		e2e_secret_ensure "${engine}-${name}"
	done
}

seed_mysql_family() {
	local engine="$1"
	e2e_section "seed ${engine}"
	ensure_user_secrets "${engine}"
	e2e_mysql_exec "${engine}" "-" "${fixtures_dir}/sql/mysql-seed.sql"
	e2e_mysql_exec "${engine}" "-" "${fixtures_dir}/sql/seed-unicode.sql"
	e2e_mysql_exec "${engine}" "-" "${fixtures_dir}/sql/seed-fk.sql"
	e2e_mysql_exec "${engine}" "-" "${fixtures_dir}/sql/seed-binary.sql"
	e2e_mysql_exec "${engine}" "-" "${fixtures_dir}/sql/seed-routines.sql"
	local users_sql="${home}/fixtures/${engine}-users.sql"
	substitute_passwords "${fixtures_dir}/sql/mysql-users.sql.tmpl" "${users_sql}" "${engine}"
	e2e_mysql_exec "${engine}" "-" "${users_sql}"
	rm -f "${users_sql}"
	e2e_log "${engine}: databases, tables, users, and grants seeded"
}

seed_postgres() {
	e2e_section "seed postgres"
	local database
	for database in e2e_app e2e_empty e2e_unicode e2e_reserved e2e_case e2e_long_63 e2e_fk e2e_binary e2e_routines; do
		e2e_create_database postgres "${database}"
	done
	e2e_psql_exec postgres "${fixtures_dir}/sql/postgres-seed.sql"
	ensure_user_secrets postgres
	local users_sql="${home}/fixtures/postgres-users.sql"
	substitute_passwords "${fixtures_dir}/sql/postgres-users.sql.tmpl" "${users_sql}" postgres
	e2e_psql_exec e2e_app "${users_sql}"
	rm -f "${users_sql}"
	e2e_log "postgres: databases, tables, roles, and grants seeded"
}

create_backup_and_checks() {
	local engine="$1"
	[ "${do_backup}" = "1" ] || return 0
	local backup_dir="${home}/backups/${engine}"
	mkdir -p "${backup_dir}"
	local dump="${backup_dir}/e2e_app.sql.gz"
	e2e_section "backup and restore check (${engine})"
	case "${engine}" in
	mysql | mariadb) e2e_dump_mysql "${engine}" e2e_app "${dump}" ;;
	postgres) e2e_dump_postgres e2e_app "${dump}" ;;
	*) e2e_die "backup unsupported for ${engine}" ;;
	esac
	gzip -t "${dump}"
	e2e_sha256 "${dump}" >"${dump}.sha256"
	chmod 600 "${dump}" "${dump}.sha256"
	e2e_log "${engine}: backup $(basename "${dump}") ($(stat -c '%s' "${dump}") bytes)"

	# Restore into a scratch database and verify the marker rows.
	e2e_drop_database "${engine}" e2e_restore_check
	e2e_create_database "${engine}" e2e_restore_check
	case "${engine}" in
	mysql | mariadb) e2e_restore_mysql "${engine}" e2e_restore_check "${dump}" ;;
	postgres) e2e_restore_postgres e2e_restore_check "${dump}" ;;
	esac
	local count
	if [ "${engine}" = "postgres" ]; then
		count="$(e2e_psql_query e2e_restore_check 'SELECT count(*) FROM e2e_marker')"
	else
		count="$(e2e_mysql_query "${engine}" e2e_restore_check 'SELECT count(*) FROM e2e_marker')"
	fi
	e2e_assert_eq "${count}" "3" "${engine} restored marker count"
	e2e_drop_database "${engine}" e2e_restore_check
	e2e_log "${engine}: restore check passed"

	# Import the committed valid gzip fixture into a scratch database.
	e2e_drop_database "${engine}" e2e_import_check
	e2e_create_database "${engine}" e2e_import_check
	case "${engine}" in
	mysql | mariadb)
		gzip -dc "${home}/fixtures/dumps/seed.sql.gz" |
			docker exec -i -e MYSQL_PWD="$(e2e_secret_get "${engine}_root")" \
				"$(e2e_container_name "${engine}")" "$(e2e_engine_client "${engine}")" -uroot e2e_import_check
		count="$(e2e_mysql_query "${engine}" e2e_import_check 'SELECT count(*) FROM e2e_import_marker')"
		;;
	postgres)
		e2e_restore_postgres e2e_import_check "${home}/fixtures/dumps/seed-postgres.sql.gz"
		count="$(e2e_psql_query e2e_import_check 'SELECT count(*) FROM e2e_import_marker')"
		;;
	esac
	e2e_assert_eq "${count}" "2" "${engine} import marker count"
	e2e_drop_database "${engine}" e2e_import_check
	e2e_log "${engine}: gzip import check passed"
}

seed_redis() {
	e2e_section "seed redis"
	local ro full custom
	e2e_secret_ensure redis-e2e_cache_ro
	e2e_secret_ensure redis-e2e_cache_full
	e2e_secret_ensure redis-e2e_cache_custom
	ro="$(e2e_secret_get redis-e2e_cache_ro)"
	full="$(e2e_secret_get redis-e2e_cache_full)"
	custom="$(e2e_secret_get redis-e2e_cache_custom)"

	e2e_redis_cli ACL SETUSER e2e_cache_ro reset on '~*' '+@read' '+@connection' '-@admin' '-@dangerous' \
		'-config' '-debug' '-shutdown' '-flushall' '-flushdb' '-acl' '-module' '-replicaof' '-slaveof' \
		'-migrate' '-save' '-bgsave' '-bgrewriteaof' '-swapdb' '-cluster' '-failover' ">${ro}" >/dev/null
	e2e_redis_cli ACL SETUSER e2e_cache_full reset on '~*' '+@all' '-@admin' '-@dangerous' \
		'-config' '-debug' '-shutdown' '-flushall' '-flushdb' '-acl' '-module' '-replicaof' '-slaveof' \
		'-migrate' '-save' '-bgsave' '-bgrewriteaof' '-swapdb' '-cluster' '-failover' ">${full}" >/dev/null
	e2e_redis_cli ACL SETUSER e2e_cache_custom reset on '~e2e:*' '+get' '+set' ">${custom}" >/dev/null
	e2e_redis_cli ACL SAVE >/dev/null
	e2e_redis_cli SET e2e:seed:key 'seed-value' >/dev/null
	e2e_redis_cli SET e2e:custom:key 'custom-value' >/dev/null

	docker exec "$(e2e_container_name redis)" test -s /data/users.acl ||
		e2e_die "redis ACL file was not persisted at /data/users.acl"
	for secret in "${ro}" "${full}" "${custom}"; do
		if docker exec "$(e2e_container_name redis)" grep -Fq -- "${secret}" /data/users.acl; then
			e2e_die "redis ACL file contains a plaintext password"
		fi
	done
	e2e_log "redis: ACL users seeded and users.acl persisted (hashes only)"
}

seed_site_links() {
	[ "${do_site_links}" = "1" ] || return 0
	e2e_section "seed site links"
	mkdir -p "${home}/sites/e2e-site"
	printf '<!doctype html><html lang="en"><body><h1>e2e-site</h1></body></html>\n' \
		>"${home}/sites/e2e-site/index.html"

	python3 - "${home}/sites/links.json" <<'PY'
import json, sys
path = sys.argv[1]
data = {
    "schema_version": 1,
    "site": {"name": "e2e-site", "path": "sites/e2e-site", "kind": "static"},
    "links": [
        {
            "service": "mysql",
            "database": "e2e_app",
            "username": "e2e_user",
            "primary": True,
            "password_managed": True,
            "env": {
                "DB_CONNECTION": "mysql",
                "DB_HOST": "mysql",
                "DB_PORT": "3306",
                "DB_DATABASE": "e2e_app",
                "DB_USERNAME": "e2e_user",
            },
        },
        {
            "service": "postgres",
            "database": "e2e_app",
            "username": "e2e_user",
            "primary": False,
            "password_managed": True,
            "env": {
                "DB_CONNECTION": "pgsql",
                "DB_HOST": "postgres",
                "DB_PORT": "5432",
                "DB_DATABASE": "e2e_app",
                "DB_USERNAME": "e2e_user",
            },
        },
    ],
}
with open(path, "w", encoding="utf-8") as fh:
    json.dump(data, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY

	if [ "${E2E_DEVSTACK_DAEMON:-0}" = "1" ]; then
		[ -x "${ds}" ] || e2e_die "E2E_DEVSTACK_DAEMON=1 requires an executable devstack CLI at ${ds}"
		"${ds}" --home "${home}" site add --name e2e-site --path "${home}/sites/e2e-site" \
			>"${logs_dir}/site-add.log" 2>&1 || e2e_die "devstack site add failed; see ${logs_dir}/site-add.log"
		local user_id
		user_id="$("${ds}" --home "${home}" --json db user list --service mysql |
			python3 -c 'import json,sys; data=json.load(sys.stdin); print(next(u["id"] for u in data if u["username"]=="e2e_user"))')"
		"${ds}" --home "${home}" db link --site e2e-site --service mysql --database e2e_app \
			--user-id "${user_id}" --primary >"${logs_dir}/site-link.log" 2>&1 ||
			e2e_die "devstack db link failed; see ${logs_dir}/site-link.log"
		e2e_log "site links: real devstack CLI link created"
	else
		e2e_log "site links: disposable link fixture written (set E2E_DEVSTACK_DAEMON=1 for the real CLI path)"
	fi
}

generate_fixtures

case "${engines}" in
all) engine_list="mysql mariadb postgres redis" ;;
mysql | mariadb | postgres | redis) engine_list="${engines}" ;;
postgresql) engine_list="postgres" ;;
*) e2e_die "unknown --engine ${engines}" ;;
esac

if [ "${start}" = "1" ]; then
	for engine in ${engine_list}; do
		e2e_start_service "${engine}"
	done
fi

for engine in ${engine_list}; do
	case "${engine}" in
	mysql | mariadb)
		seed_mysql_family "${engine}"
		create_backup_and_checks "${engine}"
		;;
	postgres)
		seed_postgres
		create_backup_and_checks postgres
		;;
	redis) seed_redis ;;
	esac
done

seed_site_links
e2e_write_run_manifest
e2e_log "seed complete; manifest: ${home}/run-manifest.json"
