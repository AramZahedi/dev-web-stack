#!/usr/bin/env bash
# Cycle 4 / E2E-02: service-wide database and user management on MySQL and
# PostgreSQL in the disposable Ubuntu engine. MySQL runs without host ports;
# PostgreSQL uses its fixed loopback port (free on this host).
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle4-env.sh
source "${here}/cycle4-env.sh"
# shellcheck source=cycle4-check.sh
source "${here}/cycle4-check.sh"

scenario="E2E-02"
c4_guard
c4_check_init "${scenario}"
trap c4_cleanup EXIT
c4_init_dirs
c4_write_helper_config
c4_start_helper
c4_start_daemon

private="${home}/private"
mkdir -p "${private}"
chmod 700 "${private}"

run_json() {
	local output="$1"
	shift
	c4_cli --json "$@" >"${output}" 2>"${output}.err"
}

user_id() {
	local service="$1" username="$2"
	local file="${private}/${service}-users.json"
	python3 -c 'import json,sys; users=json.load(open(sys.argv[1]))["users"]; match=next((u for u in users if u["username"]==sys.argv[2]), None); print(match["id"] if match else "")' "${file}" "${username}"
}

assert_user_grant() {
	local service="$1" username="$2" database="$3" preset="$4"
	shift 4
	local file="${private}/${service}-users.json"
	run_json "${file}" db user list --service "${service}" || true
	if python3 - "${file}" "${username}" "${database}" "${preset}" "$@" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
user = next((u for u in data["users"] if u["username"] == sys.argv[2]), None)
grant = next((g for g in (user or {}).get("grants", []) if g["database"] == sys.argv[3]), None)
want_preset = sys.argv[4]
want_privs = {p.upper() for p in sys.argv[5:]}
have_privs = {p.upper() for p in (grant or {}).get("privileges", [])}
ok = grant is not None and grant.get("preset") == want_preset and (not want_privs or want_privs <= have_privs)
raise SystemExit(0 if ok else 1)
PY
	then
		c4_pass "${scenario}" "${service} metadata grant ${database}:${preset} matches"
	else
		c4_fail "${scenario}" "${service} metadata grant ${database}:${preset} matches"
	fi
}

json_has_no_secret() {
	local file="$1"
	python3 - "${file}" <<'PY'
import json, re, sys
text = open(sys.argv[1], encoding="utf-8").read()
data = json.loads(text)
bad = []
def walk(value, path=""):
    if isinstance(value, dict):
        for key, child in value.items():
            if key.lower() in {"password", "secret", "value", "token"} and isinstance(child, str) and child:
                bad.append(f"{path}.{key}")
            walk(child, f"{path}.{key}")
    elif isinstance(value, list):
        for i, child in enumerate(value):
            walk(child, f"{path}[{i}]")
walk(data)
if re.search(r"://[^/\s:@]+:[^@\s]+@", text):
    bad.append("url-userinfo")
raise SystemExit(0 if not bad else 1)
PY
}

# --- start services ----------------------------------------------------------
c4_cli --json service install mysql >"${evidence_dir}/${scenario}/install-mysql.json"
c4_strip_mysql_ports
c4_compose up -d --no-deps mysql >"${evidence_dir}/${scenario}/start-mysql.log" 2>&1
c4_wait_container_healthy devstack-mysql 180
c4_cli --json service install postgresql >"${evidence_dir}/${scenario}/install-postgresql.json"
c4_cli --json service activate postgresql >"${evidence_dir}/${scenario}/activate-postgresql.json"
c4_wait_container_healthy devstack-postgresql 180
c4_cli --json service install redis >"${evidence_dir}/${scenario}/install-redis.json"
c4_cli --json service activate redis >"${evidence_dir}/${scenario}/activate-redis.json"
c4_wait_container_healthy devstack-redis 120

# --- databases ---------------------------------------------------------------
for svc in mysql postgresql; do
	for db in e2e_app e2e_empty; do
		if run_json "${private}/${svc}-create-${db}.json" db create --service "${svc}" --name "${db}"; then
			c4_pass "${scenario}" "${svc} create ${db}"
		else
			c4_fail "${scenario}" "${svc} create ${db}" "$(head -1 "${private}/${svc}-create-${db}.json.err")"
		fi
	done
done
c4_mysql_root_sql <<'SQL'
CREATE TABLE IF NOT EXISTS e2e_app.e2e_marker (id INT PRIMARY KEY, label VARCHAR(64));
INSERT INTO e2e_app.e2e_marker (id, label) VALUES (1, 'marker') ON DUPLICATE KEY UPDATE label = VALUES(label);
SQL
c4_psql_root_sql e2e_app <<'SQL'
CREATE TABLE IF NOT EXISTS e2e_marker (id int primary key, label text);
INSERT INTO e2e_marker (id, label) VALUES (1, 'marker') ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label;
SQL
c4_pass "${scenario}" "marker tables created"

# --- users -------------------------------------------------------------------
for spec in \
	"mysql e2e_ro e2e_app:read_only" \
	"mysql e2e_rw e2e_app:read_write" \
	"mysql e2e_schema e2e_app:read_write_schema" \
	"mysql e2e_admin e2e_app:admin" \
	"postgresql e2e_ro e2e_app:read_only" \
	"postgresql e2e_rw e2e_app:read_write" \
	"postgresql e2e_schema e2e_app:read_write_schema" \
	"postgresql e2e_admin e2e_app:admin"; do
	set -- ${spec}
	svc="$1" name="$2" grant="$3"
	if run_json "${private}/${svc}-create-${name}.json" db user create --service "${svc}" --name "${name}" --grant "${grant}"; then
		c4_pass "${scenario}" "${svc} create ${name}"
	else
		c4_fail "${scenario}" "${svc} create ${name}" "$(head -1 "${private}/${svc}-create-${name}.json.err")"
	fi
done
if run_json "${private}/mysql-create-e2e_custom.json" db user create --service mysql --name e2e_custom --grant e2e_app:custom --privilege SELECT --privilege INSERT; then
	c4_pass "${scenario}" "mysql create custom-grant user"
else
	c4_fail "${scenario}" "mysql create custom-grant user"
fi
if run_json "${private}/postgresql-create-e2e_custom.json" db user create --service postgresql --name e2e_custom --grant e2e_app:custom --privilege SELECT --privilege INSERT; then
	c4_pass "${scenario}" "postgresql create custom-grant user"
else
	c4_fail "${scenario}" "postgresql create custom-grant user"
fi
for spec in \
	"localhost e2e_local" \
	"127.0.0.1 e2e_loop" \
	"10.% e2e_net" \
	"% e2e_any"; do
	set -- ${spec}
	host="$1" name="$2"
	if run_json "${private}/mysql-create-${name}.json" db user create --service mysql --name "${name}" --host "${host}" --grant e2e_app:read_only; then
		c4_pass "${scenario}" "mysql host scope ${host}"
	else
		c4_fail "${scenario}" "mysql host scope ${host}"
	fi
done

# --- list and secret-free JSON ----------------------------------------------
for svc in mysql postgresql; do
	if run_json "${private}/${svc}-databases.json" db list --service "${svc}"; then
		c4_pass "${scenario}" "${svc} list databases"
	else
		c4_fail "${scenario}" "${svc} list databases"
	fi
	if run_json "${private}/${svc}-users.json" db user list --service "${svc}"; then
		c4_pass "${scenario}" "${svc} list users"
	else
		c4_fail "${scenario}" "${svc} list users"
	fi
	if json_has_no_secret "${private}/${svc}-databases.json" && json_has_no_secret "${private}/${svc}-users.json"; then
		c4_pass "${scenario}" "${svc} list JSON has no secret or URL userinfo"
	else
		c4_fail "${scenario}" "${svc} list JSON has no secret or URL userinfo"
	fi
done

# --- reveal twice and audit --------------------------------------------------
for svc in mysql postgresql; do
	uid="$(user_id "${svc}" e2e_rw)"
	: >"${private}/${svc}-reveal1.json"
	: >"${private}/${svc}-reveal2.json"
	if c4_cli --json db user password --service "${svc}" --user-id "${uid}" --reveal >"${private}/${svc}-reveal1.json" 2>"${private}/${svc}-reveal1.err" &&
		c4_cli --json db user password --service "${svc}" --user-id "${uid}" --reveal >"${private}/${svc}-reveal2.json" 2>"${private}/${svc}-reveal2.err"; then
		c4_pass "${scenario}" "${svc} reveal twice"
	else
		c4_fail "${scenario}" "${svc} reveal twice"
	fi
	if grep -qi 'WARNING' "${private}/${svc}-reveal1.err" && grep -qi 'WARNING' "${private}/${svc}-reveal2.err"; then
		c4_pass "${scenario}" "${svc} reveal warning"
	else
		c4_fail "${scenario}" "${svc} reveal warning"
	fi
	python3 - "${private}/${svc}-reveal1.json" "${private}/${svc}-reveal2.json" "${evidence_dir}/${scenario}/${svc}-reveal-summary.txt" <<'PY'
import hashlib, json, sys
a = json.load(open(sys.argv[1]))["value"]
b = json.load(open(sys.argv[2]))["value"]
open(sys.argv[3], "w", encoding="utf-8").write(
    f"same={a == b} sha256_prefix={hashlib.sha256(a.encode()).hexdigest()[:12]}\n")
raise SystemExit(0 if a == b else 1)
PY
	if [ $? -eq 0 ]; then
		c4_pass "${scenario}" "${svc} reveal returns the same secret twice"
	else
		c4_fail "${scenario}" "${svc} reveal returns the same secret twice"
	fi
done

# --- rotate and verify old/new ----------------------------------------------
for svc in mysql postgresql; do
	uid="$(user_id "${svc}" e2e_rw)"
	old="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["value"])' "${private}/${svc}-reveal1.json")"
	if c4_cli --yes --json db user password --service "${svc}" --user-id "${uid}" >"${private}/${svc}-rotate.json" 2>"${private}/${svc}-rotate.err"; then
		c4_pass "${scenario}" "${svc} rotate"
	else
		c4_fail "${scenario}" "${svc} rotate"
	fi
	c4_cli --json db user password --service "${svc}" --user-id "${uid}" --reveal >"${private}/${svc}-new.json" 2>/dev/null
	new="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["value"])' "${private}/${svc}-new.json")"
	if [ "${svc}" = "mysql" ]; then
		if c4_mysql_user_query e2e_rw "${old}" e2e_app 'SELECT 1' >/dev/null 2>&1; then
			c4_fail "${scenario}" "mysql old password rejected"
		else
			c4_pass "${scenario}" "mysql old password rejected"
		fi
		if c4_mysql_user_query e2e_rw "${new}" e2e_app 'SELECT 1' >/dev/null; then
			c4_pass "${scenario}" "mysql new password works"
		else
			c4_fail "${scenario}" "mysql new password works"
		fi
	else
		if c4_psql_network_query e2e_rw "${old}" e2e_app 'SELECT 1' >/dev/null 2>&1; then
			c4_fail "${scenario}" "postgresql old password rejected"
		else
			c4_pass "${scenario}" "postgresql old password rejected"
		fi
		if c4_psql_network_query e2e_rw "${new}" e2e_app 'SELECT 1' >/dev/null; then
			c4_pass "${scenario}" "postgresql new password works"
		else
			c4_fail "${scenario}" "postgresql new password works"
		fi
	fi
done

# --- audit copy --------------------------------------------------------------
if c4_audit_copy "${private}/state-copy"; then
	c4_pass "${scenario}" "state.db copied for audit"
	reveal_count="$(c4_audit_query "${private}/state-copy" "SELECT COUNT(*) FROM audit_events WHERE action='database.user.password_revealed' AND result='ok';")"
	rotate_count="$(c4_audit_query "${private}/state-copy" "SELECT COUNT(*) FROM audit_events WHERE action='database.user.password_changed' AND result='ok';")"
	c4_assert_eq "${scenario}" "audit rows for reveal" "${reveal_count}" "6"
	c4_assert_eq "${scenario}" "audit rows for rotate" "${rotate_count}" "2"
	c4_audit_query "${private}/state-copy" "SELECT action, result, COUNT(*) FROM audit_events GROUP BY action, result ORDER BY action;" >"${evidence_dir}/${scenario}/audit-summary.txt"
else
	c4_fail "${scenario}" "state.db copied for audit"
fi

# --- grant/revoke and engine comparison -------------------------------------
uid_mysql="$(user_id mysql e2e_custom)"
uid_pg="$(user_id postgresql e2e_custom)"
if c4_cli --json db user grant --service mysql --user-id "${uid_mysql}" --database e2e_app --preset read_only >/dev/null 2>&1; then
	c4_pass "${scenario}" "mysql grant read_only"
else
	c4_fail "${scenario}" "mysql grant read_only"
fi
assert_user_grant mysql e2e_custom e2e_app read_only SELECT
mysql_grants="$(c4_mysql_root_query -e "SHOW GRANTS FOR 'e2e_custom'@'%';")"
c4_assert_contains "${scenario}" "mysql engine grant SELECT" "${mysql_grants}" "SELECT"
c4_assert_not_contains "${scenario}" "mysql engine grant no INSERT after read_only" "${mysql_grants}" "INSERT"
if c4_cli --json db user grant --service mysql --user-id "${uid_mysql}" --database e2e_app --preset custom --privilege SELECT --privilege INSERT >/dev/null 2>&1; then
	c4_pass "${scenario}" "mysql grant custom"
else
	c4_fail "${scenario}" "mysql grant custom"
fi
assert_user_grant mysql e2e_custom e2e_app custom SELECT INSERT
mysql_grants="$(c4_mysql_root_query -e "SHOW GRANTS FOR 'e2e_custom'@'%';")"
c4_assert_contains "${scenario}" "mysql engine custom SELECT" "${mysql_grants}" "SELECT"
c4_assert_contains "${scenario}" "mysql engine custom INSERT" "${mysql_grants}" "INSERT"
c4_assert_not_contains "${scenario}" "mysql engine custom no UPDATE" "${mysql_grants}" "UPDATE"
if c4_cli --json db user grant --service mysql --user-id "${uid_mysql}" --database e2e_app --preset read_write >/dev/null 2>&1; then
	c4_pass "${scenario}" "mysql revoke via preset change"
else
	c4_fail "${scenario}" "mysql revoke via preset change"
fi
assert_user_grant mysql e2e_custom e2e_app read_write SELECT INSERT UPDATE DELETE
mysql_grants="$(c4_mysql_root_query -e "SHOW GRANTS FOR 'e2e_custom'@'%';")"
c4_assert_contains "${scenario}" "mysql engine read_write UPDATE" "${mysql_grants}" "UPDATE"

if c4_cli --json db user grant --service postgresql --user-id "${uid_pg}" --database e2e_app --preset read_only >/dev/null 2>&1; then
	c4_pass "${scenario}" "postgresql grant read_only"
else
	c4_fail "${scenario}" "postgresql grant read_only"
fi
assert_user_grant postgresql e2e_custom e2e_app read_only SELECT
pg_grants="$(c4_psql_root_query -d e2e_app -c "SELECT privilege_type FROM information_schema.role_table_grants WHERE grantee='e2e_custom' UNION SELECT 'CONNECT' WHERE has_database_privilege('e2e_custom','e2e_app','CONNECT') UNION SELECT 'USAGE' WHERE has_schema_privilege('e2e_custom','public','USAGE') ORDER BY 1;")"
c4_assert_contains "${scenario}" "postgresql engine grant SELECT" "${pg_grants}" "SELECT"
c4_assert_not_contains "${scenario}" "postgresql engine grant no INSERT after read_only" "${pg_grants}" "INSERT"
if c4_cli --json db user grant --service postgresql --user-id "${uid_pg}" --database e2e_app --preset custom --privilege SELECT --privilege INSERT >/dev/null 2>&1; then
	c4_pass "${scenario}" "postgresql grant custom"
else
	c4_fail "${scenario}" "postgresql grant custom"
fi
assert_user_grant postgresql e2e_custom e2e_app custom SELECT INSERT
pg_grants="$(c4_psql_root_query -d e2e_app -c "SELECT privilege_type FROM information_schema.role_table_grants WHERE grantee='e2e_custom' UNION SELECT 'CONNECT' WHERE has_database_privilege('e2e_custom','e2e_app','CONNECT') UNION SELECT 'USAGE' WHERE has_schema_privilege('e2e_custom','public','USAGE') ORDER BY 1;")"
c4_assert_contains "${scenario}" "postgresql engine custom SELECT" "${pg_grants}" "SELECT"
c4_assert_contains "${scenario}" "postgresql engine custom INSERT" "${pg_grants}" "INSERT"
c4_assert_not_contains "${scenario}" "postgresql engine custom no UPDATE" "${pg_grants}" "UPDATE"
if c4_cli --json db user grant --service postgresql --user-id "${uid_pg}" --database e2e_app --preset read_write >/dev/null 2>&1; then
	c4_pass "${scenario}" "postgresql grant read_write"
else
	c4_fail "${scenario}" "postgresql grant read_write"
fi
assert_user_grant postgresql e2e_custom e2e_app read_write SELECT INSERT UPDATE DELETE

# --- drop with and without backup -------------------------------------------
for svc in mysql postgresql; do
	run_json "${private}/${svc}-create-drop-plain.json" db create --service "${svc}" --name e2e_drop_plain >/dev/null 2>&1 || true
	run_json "${private}/${svc}-create-drop-backup.json" db create --service "${svc}" --name e2e_drop_backup >/dev/null 2>&1 || true
	if c4_cli --yes --json db drop --service "${svc}" --name e2e_drop_plain >/dev/null 2>"${private}/${svc}-drop-plain.err"; then
		c4_pass "${scenario}" "${svc} drop without backup"
	else
		c4_fail "${scenario}" "${svc} drop without backup"
	fi
	if c4_cli --yes --json db drop --service "${svc}" --name e2e_drop_backup --backup-first >/dev/null 2>"${private}/${svc}-drop-backup.err"; then
		c4_pass "${scenario}" "${svc} drop with --backup-first"
	else
		c4_fail "${scenario}" "${svc} drop with --backup-first"
	fi
	run_json "${private}/${svc}-backups.json" db backup list --service "${svc}" || true
	if python3 - "${private}/${svc}-backups.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
rows = data.get("backups", [])
raise SystemExit(0 if any(b.get("database") == "e2e_drop_backup" and b.get("kind") == "pre_drop" for b in rows) else 1)
PY
	then
		c4_pass "${scenario}" "${svc} pre_drop backup recorded"
	else
		c4_fail "${scenario}" "${svc} pre_drop backup recorded"
	fi
done

# --- rename PostgreSQL and reject MySQL -------------------------------------
run_json "${private}/pg-rename-create.json" db create --service postgresql --name e2e_rename_src >/dev/null 2>&1 || true
run_json "${private}/pg-rename-user.json" db user create --service postgresql --name e2e_rename_user --grant e2e_rename_src:read_only >/dev/null 2>&1 || true
if c4_cli --yes --json db rename --service postgresql --name e2e_rename_src --to e2e_rename_dst >"${private}/pg-rename.json" 2>"${private}/pg-rename.err"; then
	c4_pass "${scenario}" "postgresql rename"
else
	c4_fail "${scenario}" "postgresql rename" "$(head -1 "${private}/pg-rename.err")"
fi
run_json "${private}/pg-users-after-rename.json" db user list --service postgresql || true
if python3 - "${private}/pg-users-after-rename.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
u = next((u for u in data["users"] if u["username"] == "e2e_rename_user"), None)
raise SystemExit(0 if u and any(g["database"] == "e2e_rename_dst" for g in u.get("grants", [])) else 1)
PY
then
	c4_pass "${scenario}" "postgresql rename updated grant metadata"
else
	c4_fail "${scenario}" "postgresql rename updated grant metadata"
fi
c4_expect_code "${scenario}" "mysql rename rejected before API" "DATABASE_UNSUPPORTED" \
	c4_cli --json db rename --service mysql --name e2e_app --to e2e_app2

# --- protected objects -------------------------------------------------------
for db in mysql information_schema performance_schema sys; do
	c4_expect_code "${scenario}" "protected mysql database ${db}" "DATABASE_PROTECTED" \
		c4_cli --json --yes db drop --service mysql --name "${db}"
done
for db in postgres template0 template1; do
	c4_expect_code "${scenario}" "protected postgresql database ${db}" "DATABASE_PROTECTED" \
		c4_cli --json --yes db drop --service postgresql --name "${db}"
done
for user in root mysql.sys mysql.session mysql.infoschema; do
	c4_expect_code "${scenario}" "protected mysql user ${user}" "DATABASE_PROTECTED" \
		c4_cli --json db user create --service mysql --name "${user}" --grant e2e_app:read_only
done
c4_expect_code "${scenario}" "protected postgresql user postgres" "DATABASE_PROTECTED" \
	c4_cli --json db user create --service postgresql --name postgres --grant e2e_app:read_only
c4_expect_code "${scenario}" "protected redis default user" "DATABASE_PROTECTED" \
	c4_cli --json db user create --service redis --name default --access-level full

# --- site link, linked-user delete, site removal preservation ---------------
mkdir -p "${home}/sites/e2e-site"
printf '<!doctype html><html><body>e2e</body></html>\n' >"${home}/sites/e2e-site/index.html"
if run_json "${private}/site-add.json" site add --name e2e-site --path "${home}/sites/e2e-site"; then
	c4_pass "${scenario}" "site add"
else
	c4_fail "${scenario}" "site add" "$(head -1 "${private}/site-add.json.err")"
fi
uid_site="$(user_id mysql e2e_site_user 2>/dev/null || true)"
if [ -z "${uid_site}" ]; then
	if run_json "${private}/mysql-create-e2e_site_user.json" db user create --service mysql --name e2e_site_user --grant e2e_app:read_write; then
		c4_pass "${scenario}" "site user create"
	else
		c4_fail "${scenario}" "site user create" "$(head -1 "${private}/mysql-create-e2e_site_user.json.err")"
	fi
	run_json "${private}/mysql-users.json" db user list --service mysql
	uid_site="$(user_id mysql e2e_site_user)"
fi
if run_json "${private}/site-link.json" db link --site e2e-site --service mysql --database e2e_app --user-id "${uid_site}" --primary; then
	c4_pass "${scenario}" "site database link"
else
	c4_fail "${scenario}" "site database link" "$(head -1 "${private}/site-link.json.err")"
fi
c4_expect_code "${scenario}" "linked user delete refused" "DATABASE_IN_USE" \
	c4_cli --json --yes db user delete --service mysql --user-id "${uid_site}"
c4_mysql_root_sql <<'SQL'
INSERT INTO e2e_app.e2e_marker (id, label) VALUES (2, 'site-marker') ON DUPLICATE KEY UPDATE label = VALUES(label);
SQL
c4_cli --json db backup create --service mysql --database e2e_app >"${private}/site-backup.json" 2>"${private}/site-backup.err" || true
if c4_cli --json site remove e2e-site --yes >/dev/null 2>&1; then
	c4_pass "${scenario}" "site remove"
else
	c4_fail "${scenario}" "site remove"
fi
run_json "${private}/mysql-databases-after-site.json" db list --service mysql || true
run_json "${private}/mysql-users-after-site.json" db user list --service mysql || true
run_json "${private}/mysql-backups-after-site.json" db backup list --service mysql || true
if python3 - "${private}/mysql-backups-after-site.json" <<'PY'
import json, sys
rows = json.load(open(sys.argv[1], encoding="utf-8")).get("backups", [])
raise SystemExit(0 if any(b.get("database") == "e2e_app" for b in rows) else 1)
PY
then
	c4_pass "${scenario}" "site removal preserves backups"
else
	c4_fail "${scenario}" "site removal preserves backups"
fi
if python3 - "${private}/mysql-databases-after-site.json" "${private}/mysql-users-after-site.json" <<'PY'
import json, sys
dbs = {d["name"] for d in json.load(open(sys.argv[1]))["databases"]}
users = {u["username"]: u for u in json.load(open(sys.argv[2]))["users"]}
u = users.get("e2e_site_user")
raise SystemExit(0 if "e2e_app" in dbs and u and u.get("password_managed") and u.get("grants") else 1)
PY
then
	c4_pass "${scenario}" "site removal preserves database, user, grants, password"
else
	c4_fail "${scenario}" "site removal preserves database, user, grants, password"
fi
marker="$(c4_mysql_root_query -D e2e_app -e 'SELECT label FROM e2e_marker WHERE id=2;')"
c4_assert_eq "${scenario}" "site removal preserves marker row" "${marker}" "site-marker"

# --- API parity and dashboard shell -----------------------------------------
run_json "${private}/mysql-users-final.json" db user list --service mysql || true
c4_api GET /api/v1/services/mysql/databases >"${private}/api-mysql-databases.json"
c4_api GET /api/v1/services/mysql/users >"${private}/api-mysql-users.json"
if python3 - "${private}/api-mysql-databases.json" "${private}/mysql-databases.json" <<'PY'
import json, sys
api = {d["name"] for d in json.load(open(sys.argv[1]))["databases"]}
cli = {d["name"] for d in json.load(open(sys.argv[2]))["databases"]}
raise SystemExit(0 if api == cli else 1)
PY
then
	c4_pass "${scenario}" "API and CLI database lists agree"
else
	c4_fail "${scenario}" "API and CLI database lists agree"
fi
if python3 - "${private}/api-mysql-users.json" "${private}/mysql-users-final.json" <<'PY'
import json, sys
api = {u["username"] for u in json.load(open(sys.argv[1]))["users"]}
cli = {u["username"] for u in json.load(open(sys.argv[2]))["users"]}
raise SystemExit(0 if api == cli else 1)
PY
then
	c4_pass "${scenario}" "API and CLI user lists agree"
else
	c4_fail "${scenario}" "API and CLI user lists agree"
fi
if c4_api GET / | grep -qi '<div id="app"'; then
	c4_pass "${scenario}" "disposable dashboard shell served"
else
	c4_fail "${scenario}" "disposable dashboard shell served"
fi
c4_note "${scenario}" "SKIP: full dashboard interaction is cycle 8/9 (Playwright); API parity is covered here."
scan_paths=("${evidence_dir}/${scenario}" "${logs_dir}")
[ -d "${private}/state-copy" ] && scan_paths+=("${private}/state-copy")
if "${cycle4_root}/secret-scan.sh" "${scan_paths[@]}" >"${private}/secret-scan.log" 2>&1; then
	c4_pass "${scenario}" "secret scanner clean over evidence, logs, and state copy"
else
	c4_fail "${scenario}" "secret scanner clean over evidence, logs, and state copy"
fi

# --- result ------------------------------------------------------------------
status="$(c4_scenario_status "${scenario}")"
"${cycle4_root}/results.sh" record --id "${scenario}" --priority P0 --environment LIN-UBE \
	--status "${status}" --evidence "${evidence_dir}/${scenario}" --commit "$(git -c safe.directory="${repo}" -C "${repo}" rev-parse HEAD)" \
	--residual-risk "Dashboard interaction deferred to cycle 8/9; PostgreSQL empty-database grants show only CONNECT/USAGE until tables exist" \
	--notes "MySQL no-port container; PostgreSQL product service; CLI/API parity checked" >>"${evidence_dir}/${scenario}/checks.log" 2>&1
c4_scenario_summary "${scenario}"
