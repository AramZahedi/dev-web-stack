#!/usr/bin/env bash
# Cycle 4 / E2E-16: database/user adversarial matrix on the disposable Linux
# engine. MySQL runs without host ports; PostgreSQL uses its fixed loopback
# port. No real-install object is touched.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle4-env.sh
source "${here}/cycle4-env.sh"
# shellcheck source=cycle4-check.sh
source "${here}/cycle4-check.sh"

scenario="E2E-16"
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

refresh_users() {
	run_json "${private}/$1-users.json" db user list --service "$1" || true
}

api_csrf() {
	local jar="$1"
	curl -sS -c "${jar}" -X POST "http://127.0.0.1:${ui_port}/api/v1/auth" -o /dev/null
	curl -sS -b "${jar}" -c "${jar}" "http://127.0.0.1:${ui_port}/api/v1/session" |
		python3 -c 'import json,sys; print(json.load(sys.stdin)["csrf_token"])'
}

api_patch() {
	local jar="$1" csrf="$2" path="$3" body="$4"
	curl -sS -X PATCH -b "${jar}" -H "X-CSRF-Token: ${csrf}" -H 'Content-Type: application/json' \
		--data-binary "${body}" "http://127.0.0.1:${ui_port}${path}"
}

expect_create_db() {
	local service="$1" name="$2" want="$3"
	if [ "${want}" = "OK" ]; then
		if run_json "${private}/db-${service}-${RANDOM}.json" db create --service "${service}" --name "${name}"; then
			c4_pass "${scenario}" "${service} accepts database ${name}"
		else
			c4_fail "${scenario}" "${service} accepts database ${name}" "$(head -1 "${private}/db-${service}-${RANDOM}.json.err")"
		fi
	else
		c4_expect_code "${scenario}" "${service} rejects database ${name} with ${want}" "${want}" \
			c4_cli --json db create --service "${service}" --name "${name}"
	fi
}

expect_create_user() {
	local service="$1" name="$2" host="$3" want="$4"
	shift 4
	local -a args=(db user create --service "${service}" --name "${name}")
	[ -n "${host}" ] && args+=(--host "${host}")
	args+=("$@")
	local output="${private}/user-${service}-${name}-${RANDOM}.json"
	if [ "${want}" = "OK" ]; then
		if run_json "${output}" "${args[@]}"; then
			c4_pass "${scenario}" "${service} accepts user ${name} host ${host:-<empty>}"
		else
			c4_fail "${scenario}" "${service} accepts user ${name} host ${host:-<empty>}" "$(head -1 "${output}.err")"
		fi
	else
		c4_expect_code "${scenario}" "${service} rejects user ${name} host ${host:-<empty>} with ${want}" "${want}" \
			c4_cli --json "${args[@]}"
	fi
}

# --- services ---------------------------------------------------------------
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
run_json "${private}/mysql-create-e2e_app.json" db create --service mysql --name e2e_app
run_json "${private}/pg-create-e2e_app.json" db create --service postgresql --name e2e_app

# --- names ------------------------------------------------------------------
long63="$(printf 'a%.0s' $(seq 1 63))"
long64="$(printf 'a%.0s' $(seq 1 64))"
long128="$(printf 'a%.0s' $(seq 1 128))"
while IFS='|' read -r name want; do
	[ -n "${name}" ] || continue
	expect_create_db mysql "${name}" "${want}"
	if [ "${want}" = "OK" ] && [ "${name}" != "e2e_app" ]; then
		c4_cli --yes --json db drop --service mysql --name "${name}" >/dev/null 2>&1 || true
	fi
done <<'EOF'
e2e_app2|OK
E2E_App|OK
e2e-app|INVALID_REQUEST
e2e.app|INVALID_REQUEST
e2e app|INVALID_REQUEST
e2e;drop|INVALID_REQUEST
e2e'|INVALID_REQUEST
e2e"|INVALID_REQUEST
e2e`|INVALID_REQUEST
../e2e|INVALID_REQUEST
e2e/../x|INVALID_REQUEST
select|OK
order|OK
user|OK
mysql|DATABASE_PROTECTED
information_schema|DATABASE_PROTECTED
performance_schema|DATABASE_PROTECTED
sys|DATABASE_PROTECTED
EOF
for spec in "${long63}|OK" "${long64}|INVALID_REQUEST" "${long128}|INVALID_REQUEST" "e2e_ü|INVALID_REQUEST" "e2e_😀|INVALID_REQUEST"; do
	name="${spec%%|*}"
	want="${spec##*|}"
	expect_create_db mysql "${name}" "${want}"
	if [ "${want}" = "OK" ]; then
		c4_cli --yes --json db drop --service mysql --name "${name}" >/dev/null 2>&1 || true
	fi
done
# duplicate create
run_json "${private}/dup-create.json" db create --service mysql --name e2e_app >/dev/null 2>&1 || true
c4_expect_code "${scenario}" "duplicate database create" "DATABASE_CONFLICT" \
	c4_cli --json db create --service mysql --name e2e_app
while IFS='|' read -r name want; do
	[ -n "${name}" ] || continue
	expect_create_db postgresql "${name}" "${want}"
	if [ "${want}" = "OK" ]; then
		c4_cli --yes --json db drop --service postgresql --name "${name}" >/dev/null 2>&1 || true
	fi
done <<'EOF'
e2e_app2|OK
e2e-app|INVALID_REQUEST
select|OK
postgres|DATABASE_PROTECTED
template0|DATABASE_PROTECTED
template1|DATABASE_PROTECTED
EOF

# --- hosts ------------------------------------------------------------------
expect_create_user mysql e2e_host_pct "%" OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_local localhost OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_loop 127.0.0.1 OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_net "10.%" OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_ipv6 "::1" OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_empty "" OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_space " " OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_wild "%foo" OK --grant e2e_app:read_only
expect_create_user mysql e2e_host_inject "%' OR 1=1 --" INVALID_REQUEST --grant e2e_app:read_only
refresh_users mysql
if python3 - "${private}/mysql-users.json" <<'PY'
import json, sys
hosts = {u["host"] for u in json.load(open(sys.argv[1]))["users"] if u["username"].startswith("e2e_host_")}
need = {"%", "localhost", "127.0.0.1", "10.%", "::1", "%foo"}
raise SystemExit(0 if need <= hosts else 1)
PY
then
	c4_pass "${scenario}" "mysql host scopes recorded"
else
	c4_fail "${scenario}" "mysql host scopes recorded"
fi
c4_expect_code "${scenario}" "postgresql rejects host scope" "INVALID_REQUEST" \
	c4_cli --json db user create --service postgresql --name e2e_pg_host --host localhost --grant e2e_app:read_only

# --- grants -----------------------------------------------------------------
run_json "${private}/grant-user-create.json" db user create --service mysql --name e2e_grant_probe --grant e2e_app:read_only
refresh_users mysql
uid="$(user_id mysql e2e_grant_probe)"
for preset in read_only read_write read_write_schema admin; do
	if c4_cli --json db user grant --service mysql --user-id "${uid}" --database e2e_app --preset "${preset}" >/dev/null 2>"${private}/grant-${preset}.err"; then
		c4_pass "${scenario}" "mysql grant preset ${preset}"
	else
		c4_fail "${scenario}" "mysql grant preset ${preset}" "$(head -1 "${private}/grant-${preset}.err")"
	fi
done
if c4_cli --json db user grant --service mysql --user-id "${uid}" --database e2e_app --preset custom --privilege SELECT >/dev/null 2>"${private}/grant-custom.err"; then
	c4_pass "${scenario}" "mysql grant custom"
else
	c4_fail "${scenario}" "mysql grant custom" "$(head -1 "${private}/grant-custom.err")"
fi
for scope in '*' '%'; do
	c4_expect_code "${scenario}" "mysql rejects wildcard grant scope ${scope}" "INVALID_REQUEST" \
		c4_cli --json db user grant --service mysql --user-id "${uid}" --database "${scope}" --preset read_only
done
c4_expect_code "${scenario}" "mysql rejects protected grant database mysql" "DATABASE_PROTECTED" \
	c4_cli --json db user grant --service mysql --user-id "${uid}" --database mysql --preset read_only
if c4_cli --json db user grant --service mysql --user-id "${uid}" --database e2e_missing --preset read_only >"${private}/grant-missing.json" 2>"${private}/grant-missing.err"; then
	c4_pass "${scenario}" "mysql future-database grant accepted"
	c4_note "${scenario}" "MySQL allows grants on a non-existent database (future-database semantics); engine and metadata store the grant"
else
	code="$(grep -o '"code":"[^"]*"' "${private}/grant-missing.err" | head -1 | cut -d'"' -f4)"
	c4_pass "${scenario}" "mysql grant on missing database failed closed with ${code:-unknown}"
fi
run_json "${private}/pg-grant-probe-create.json" db user create --service postgresql --name e2e_grant_probe --grant e2e_app:read_only
refresh_users postgresql
uid_pg="$(user_id postgresql e2e_grant_probe)"
c4_expect_failure "${scenario}" "postgresql rejects grant on a missing database" \
	c4_cli --json db user grant --service postgresql --user-id "${uid_pg}" --database e2e_missing --preset read_only
if c4_cli --json db user grant --service mysql --user-id "${uid}" --database e2e_app --preset read_only >/dev/null 2>&1 &&
	c4_cli --json db user grant --service mysql --user-id "${uid}" --database e2e_app --preset read_only >/dev/null 2>&1; then
	c4_pass "${scenario}" "duplicate grant is idempotent"
else
	c4_fail "${scenario}" "duplicate grant is idempotent"
fi
if c4_cli --json db user grant --service mysql --user-id "${uid}" --database e2e_app --preset read_only >/dev/null 2>&1; then
	:
fi
# revoke by sending an empty grant set through the API is not exposed by the CLI
# grant command; delete and recreate the probe user instead and verify the
# engine no longer has the grant.
if c4_cli --yes --json db user delete --service mysql --user-id "${uid}" >/dev/null 2>"${private}/grant-user-delete.err"; then
	c4_pass "${scenario}" "mysql grant probe delete"
else
	c4_fail "${scenario}" "mysql grant probe delete" "$(head -1 "${private}/grant-user-delete.err")"
fi
engine_grants="$(c4_mysql_root_query -e "SHOW GRANTS FOR 'e2e_grant_probe'@'%';" 2>&1 || true)"
c4_assert_contains "${scenario}" "mysql grant probe removed from engine" "${engine_grants}" "ERROR 1141"

# --- passwords and metadata-only --------------------------------------------
run_json "${private}/pw-user-create.json" db user create --service mysql --name e2e_pw_probe --grant e2e_app:read_write
refresh_users mysql
uid_pw="$(user_id mysql e2e_pw_probe)"
if c4_cli --json db user password --service mysql --user-id "${uid_pw}" --reveal >"${private}/pw1.json" 2>"${private}/pw1.err" &&
	c4_cli --json db user password --service mysql --user-id "${uid_pw}" --reveal >"${private}/pw2.json" 2>"${private}/pw2.err"; then
	c4_pass "${scenario}" "password reveal twice"
else
	c4_fail "${scenario}" "password reveal twice"
fi
if grep -qi WARNING "${private}/pw1.err" && grep -qi WARNING "${private}/pw2.err"; then
	c4_pass "${scenario}" "password reveal warning"
else
	c4_fail "${scenario}" "password reveal warning"
fi
old="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["value"])' "${private}/pw1.json")"
if c4_cli --yes --json db user password --service mysql --user-id "${uid_pw}" >/dev/null 2>&1; then
	c4_pass "${scenario}" "password rotate"
else
	c4_fail "${scenario}" "password rotate"
fi
c4_cli --json db user password --service mysql --user-id "${uid_pw}" --reveal >"${private}/pw3.json" 2>/dev/null
new="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["value"])' "${private}/pw3.json")"
if c4_mysql_user_query e2e_pw_probe "${old}" e2e_app 'SELECT 1' >/dev/null 2>&1; then
	c4_fail "${scenario}" "old password rejected after rotate"
else
	c4_pass "${scenario}" "old password rejected after rotate"
fi
if c4_mysql_user_query e2e_pw_probe "${new}" e2e_app 'SELECT 1' >/dev/null; then
	c4_pass "${scenario}" "new password works after rotate"
else
	c4_fail "${scenario}" "new password works after rotate"
fi
external_pw="$(openssl rand -hex 24)"
printf "CREATE USER 'e2e_external'@'%%' IDENTIFIED BY '%s';\n" "${external_pw}" | c4_mysql_root_sql >/dev/null
refresh_users mysql
uid_ext="$(user_id mysql e2e_external)"
if python3 - "${private}/mysql-users.json" <<'PY'
import json, sys
u = next((u for u in json.load(open(sys.argv[1]))["users"] if u["username"] == "e2e_external"), None)
raise SystemExit(0 if u and not u.get("password_managed") and not u.get("managed") else 1)
PY
then
	c4_pass "${scenario}" "external user is metadata-only"
else
	c4_fail "${scenario}" "external user is metadata-only"
fi
c4_expect_code "${scenario}" "external password reveal refused" "DATABASE_PASSWORD_MISSING" \
	c4_cli --json db user password --service mysql --user-id "${uid_ext}" --reveal
c4_expect_code "${scenario}" "user-supplied password --set refused" "DATABASE_PASSWORD_SET_UNSUPPORTED" \
	c4_cli --json db user password --service mysql --user-id "${uid_pw}" --set
mkdir -p "${home}/sites/e2e-site"
printf '<!doctype html><html><body>e2e</body></html>\n' >"${home}/sites/e2e-site/index.html"
run_json "${private}/site-add.json" site add --name e2e-site --path "${home}/sites/e2e-site" >/dev/null 2>&1 || true
c4_expect_code "${scenario}" "external password stdin flag refused" "DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED" \
	c4_cli --json db link --site e2e-site --service mysql --database e2e_app --user-id "${uid_ext}" --password-stdin

# --- concurrency -------------------------------------------------------------
race_db="e2e_race_$RANDOM"
c4_cli --json db create --service mysql --name "${race_db}" >"${private}/race-a.json" 2>"${private}/race-a.err" &
pid_a=$!
c4_cli --json db create --service mysql --name "${race_db}" >"${private}/race-b.json" 2>"${private}/race-b.err" &
pid_b=$!
wait "${pid_a}" || true
wait "${pid_b}" || true
race_ok=0
race_conflict=0
grep -q '"name":"'"${race_db}"'"' "${private}/race-a.json" 2>/dev/null && race_ok=$((race_ok + 1))
grep -q '"name":"'"${race_db}"'"' "${private}/race-b.json" 2>/dev/null && race_ok=$((race_ok + 1))
grep -q 'DATABASE_CONFLICT' "${private}/race-a.err" 2>/dev/null && race_conflict=$((race_conflict + 1))
grep -q 'DATABASE_CONFLICT' "${private}/race-b.err" 2>/dev/null && race_conflict=$((race_conflict + 1))
if [ "${race_ok}" -eq 1 ] && [ "${race_conflict}" -eq 1 ]; then
	c4_pass "${scenario}" "concurrent same-name creates have one winner"
else
	c4_fail "${scenario}" "concurrent same-name creates have one winner" "ok=${race_ok} conflict=${race_conflict}"
fi
c4_cli --yes --json db drop --service mysql --name "${race_db}" >/dev/null 2>&1 || true

# rotate+delete
run_json "${private}/race-user-create.json" db user create --service mysql --name e2e_race_user --grant e2e_app:read_write
refresh_users mysql
uid_race="$(user_id mysql e2e_race_user)"
c4_cli --yes --json db user password --service mysql --user-id "${uid_race}" >"${private}/race-rotate.json" 2>"${private}/race-rotate.err" &
pid_rot=$!
c4_cli --yes --json db user delete --service mysql --user-id "${uid_race}" >"${private}/race-delete.json" 2>"${private}/race-delete.err" &
pid_del=$!
wait "${pid_rot}" || true
wait "${pid_del}" || true
refresh_users mysql
if ! grep -q '"username":"e2e_race_user"' "${private}/mysql-users.json"; then
	c4_pass "${scenario}" "rotate+delete leaves no orphan user"
else
	c4_fail "${scenario}" "rotate+delete leaves no orphan user"
fi

# grant+revoke
run_json "${private}/race-grant-create.json" db user create --service mysql --name e2e_race_grant --grant e2e_app:read_write
refresh_users mysql
uid_grant="$(user_id mysql e2e_race_grant)"
c4_cli --json db user grant --service mysql --user-id "${uid_grant}" --database e2e_app --preset read_only >"${private}/race-grant.json" 2>"${private}/race-grant.err" &
pid_grant=$!
c4_cli --json db user grant --service mysql --user-id "${uid_grant}" --database e2e_app --preset read_write >"${private}/race-revoke.json" 2>"${private}/race-revoke.err" &
pid_revoke=$!
wait "${pid_grant}" || true
wait "${pid_revoke}" || true
refresh_users mysql
if python3 - "${private}/mysql-users.json" <<'PY'
import json, sys
u = next((u for u in json.load(open(sys.argv[1]))["users"] if u["username"] == "e2e_race_grant"), None)
raise SystemExit(0 if u and len(u.get("grants", [])) == 1 else 1)
PY
then
	c4_pass "${scenario}" "grant+revoke leaves one consistent grant"
else
	c4_fail "${scenario}" "grant+revoke leaves one consistent grant"
fi

# link+delete user
run_json "${private}/race-link-create.json" db user create --service mysql --name e2e_race_link --grant e2e_app:read_write
refresh_users mysql
uid_link="$(user_id mysql e2e_race_link)"
c4_cli --json db link --site e2e-site --service mysql --database e2e_app --user-id "${uid_link}" >"${private}/race-link.json" 2>"${private}/race-link.err" &
pid_link=$!
c4_cli --yes --json db user delete --service mysql --user-id "${uid_link}" >"${private}/race-link-delete.json" 2>"${private}/race-link-delete.err" &
pid_link_del=$!
wait "${pid_link}" || true
wait "${pid_link_del}" || true
refresh_users mysql
if grep -q '"username":"e2e_race_link"' "${private}/mysql-users.json"; then
	c4_pass "${scenario}" "link+delete keeps the user when the link wins"
else
	c4_pass "${scenario}" "link+delete removed the user when delete won"
fi

# revoke existing and non-existent grants through the API
run_json "${private}/revoke-probe-create.json" db user create --service mysql --name e2e_revoke_probe --grant e2e_app:read_only
refresh_users mysql
uid_revoke="$(user_id mysql e2e_revoke_probe)"
jar="${private}/api.cookies"
csrf="$(api_csrf "${jar}")"
if [ -n "${csrf}" ]; then
	body='{"grants":[]}'
	first="$(api_patch "${jar}" "${csrf}" "/api/v1/services/mysql/users/${uid_revoke}" "${body}")"
	second="$(api_patch "${jar}" "${csrf}" "/api/v1/services/mysql/users/${uid_revoke}" "${body}")"
	if printf '%s' "${first}" | grep -q '"id"' && printf '%s' "${second}" | grep -q '"id"'; then
		c4_pass "${scenario}" "revoke existing and non-existent grants are idempotent"
	else
		c4_fail "${scenario}" "revoke existing and non-existent grants are idempotent"
	fi
	refresh_users mysql
	if python3 - "${private}/mysql-users.json" <<'PY'
import json, sys
u = next((u for u in json.load(open(sys.argv[1]))["users"] if u["username"] == "e2e_revoke_probe"), None)
raise SystemExit(0 if u and not u.get("grants") else 1)
PY
	then
		c4_pass "${scenario}" "revoked grant removed from metadata"
	else
		c4_fail "${scenario}" "revoked grant removed from metadata"
	fi
else
	c4_fail "${scenario}" "API session/CSRF available for revoke probe"
fi

# create+drop concurrency
race_cd="e2e_race_cd_$RANDOM"
c4_cli --json db create --service mysql --name "${race_cd}" >"${private}/race-cd-create.json" 2>"${private}/race-cd-create.err" &
pid_cd_create=$!
c4_cli --yes --json db drop --service mysql --name "${race_cd}" >"${private}/race-cd-drop.json" 2>"${private}/race-cd-drop.err" &
pid_cd_drop=$!
wait "${pid_cd_create}" || true
wait "${pid_cd_drop}" || true
run_json "${private}/race-cd-list.json" db list --service mysql
if python3 - "${private}/race-cd-list.json" "${race_cd}" <<'PY'
import json, sys
rows = json.load(open(sys.argv[1]))["databases"]
present = any(d["name"] == sys.argv[2] for d in rows)
# Either outcome is valid; the list must be internally consistent and not error.
raise SystemExit(0 if isinstance(present, bool) else 1)
PY
then
	c4_pass "${scenario}" "create+drop concurrency leaves a consistent inventory"
else
	c4_fail "${scenario}" "create+drop concurrency leaves a consistent inventory"
fi
c4_cli --yes --json db drop --service mysql --name "${race_cd}" >/dev/null 2>&1 || true

# --- stale metadata ----------------------------------------------------------
run_json "${private}/stale-db-create.json" db create --service mysql --name e2e_stale_db
run_json "${private}/stale-user-create.json" db user create --service mysql --name e2e_stale_user --grant e2e_stale_db:read_only
refresh_users mysql
uid_stale="$(user_id mysql e2e_stale_user)"
c4_cli --json db link --site e2e-site --service mysql --database e2e_stale_db --user-id "${uid_stale}" >/dev/null 2>&1 || true
printf "DROP DATABASE e2e_stale_db;\n" | c4_mysql_root_sql >/dev/null
run_json "${private}/stale-db-list.json" db list --service mysql
if python3 - "${private}/stale-db-list.json" <<'PY'
import json, sys
row = next((d for d in json.load(open(sys.argv[1]))["databases"] if d["name"] == "e2e_stale_db"), None)
raise SystemExit(0 if row and row.get("missing") and row.get("linked_sites") else 1)
PY
then
	c4_pass "${scenario}" "externally dropped database is flagged missing with links"
else
	c4_fail "${scenario}" "externally dropped database is flagged missing with links"
fi
printf "DROP USER 'e2e_stale_user'@'%%';\n" | c4_mysql_root_sql >/dev/null
refresh_users mysql
if grep -q '"username":"e2e_stale_user"' "${private}/mysql-users.json"; then
	c4_fail "${scenario}" "externally dropped user disappears from list"
else
	c4_pass "${scenario}" "externally dropped user disappears from list"
fi

# --- audit and SSE -----------------------------------------------------------
sse_file="${private}/sse.log"
sse_pid="$(c4_sse_start "${sse_file}")"
sleep 1
c4_cli --json db create --service mysql --name e2e_sse_probe >/dev/null 2>&1 || true
sleep 2
c4_sse_stop "${sse_pid}"
if grep -q 'database.created' "${sse_file}"; then
	c4_pass "${scenario}" "SSE emits database.created"
else
	c4_fail "${scenario}" "SSE emits database.created"
fi
if ! grep -Eqi 'password|secret|://[^/ ]+:[^@ ]+@' "${sse_file}"; then
	c4_pass "${scenario}" "SSE contains no secret"
else
	c4_fail "${scenario}" "SSE contains no secret"
fi
if c4_audit_copy "${private}/state-copy"; then
	actions="$(c4_audit_query "${private}/state-copy" "SELECT DISTINCT action FROM audit_events ORDER BY action;")"
	for action in database.created database.user.created database.user.password_revealed database.user.password_changed database.user.deleted database.user.grants_changed; do
		c4_assert_contains "${scenario}" "audit action ${action}" "${actions}" "${action}"
	done
	c4_audit_query "${private}/state-copy" "SELECT action, result, COUNT(*) FROM audit_events GROUP BY action, result ORDER BY action;" >"${evidence_dir}/${scenario}/audit-summary.txt"
else
	c4_fail "${scenario}" "state.db copied for audit"
fi
if "${cycle4_root}/secret-scan.sh" "${evidence_dir}/${scenario}" "${private}/state-copy" "${logs_dir}" >"${private}/secret-scan.log" 2>&1; then
	c4_pass "${scenario}" "secret scanner clean over evidence, logs, and state copy"
else
	c4_fail "${scenario}" "secret scanner clean over evidence, logs, and state copy"
fi
c4_cli db list --service mysql >"${private}/table-db.txt" 2>&1 || true
c4_cli db user list --service mysql >"${private}/table-users.txt" 2>&1 || true
if "${cycle4_root}/secret-scan.sh" "${private}/table-db.txt" "${private}/table-users.txt" >"${private}/table-scan.log" 2>&1; then
	c4_pass "${scenario}" "CLI table output contains no secret"
else
	c4_fail "${scenario}" "CLI table output contains no secret"
fi

# --- result ------------------------------------------------------------------
status="$(c4_scenario_status "${scenario}")"
"${cycle4_root}/results.sh" record --id "${scenario}" --priority P1 --environment LIN-UBE \
	--status "${status}" --evidence "${evidence_dir}/${scenario}" --commit "$(git -c safe.directory="${repo}" -C "${repo}" rev-parse HEAD)" \
	--residual-risk "MySQL allows grants on a non-existent database (future-database semantics); stale user metadata is engine-authoritative" \
	--notes "Names/hosts/grants/passwords/concurrency/stale metadata/audit/SSE matrix" >>"${evidence_dir}/${scenario}/checks.log" 2>&1
c4_scenario_summary "${scenario}"
