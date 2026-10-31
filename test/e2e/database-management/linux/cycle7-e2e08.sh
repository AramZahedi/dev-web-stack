#!/usr/bin/env bash
# E2E-08 - Redis users, password, and ACL persistence on the disposable Linux
# helper/daemon. No real install object is touched.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle7-env.sh
source "${here}/cycle7-env.sh"

scenario="E2E-08"
c7_guard
c4_check_init "${scenario}"
trap c7_cleanup EXIT
c7_init
c7_redis_install

c7_section "E2E-08 pre-migration gate"
config_before="$(c7_redis_config_hash)"
started_before="$(c7_redis_started_at)"
events_pid="$(c7_redis_events_start)"

c6_expect_cli_code "${scenario}" "first Redis mutation before ACL persistence returns CONFIRMATION_REQUIRED" \
	CONFIRMATION_REQUIRED "${evidence_dir}/${scenario}/create-before-acl.json" \
	c5_cli --json db user create --service redis --name e2e_cache_ro --access-level read-only

config_after="$(c7_redis_config_hash)"
started_after="$(c7_redis_started_at)"
c4_assert_eq "${scenario}" "redis.conf unchanged after refused mutation" "${config_after}" "${config_before}"
c4_assert_eq "${scenario}" "Redis not restarted after refused mutation" "${started_after}" "${started_before}"
if [ ! -e "${c7_redis_acl}" ]; then
	c4_pass "${scenario}" "users.acl not created before confirmation"
else
	c4_fail "${scenario}" "users.acl not created before confirmation" "file exists"
fi
if [ ! -e "${c7_redis_marker}" ]; then
	c4_pass "${scenario}" "no ACL restart marker before confirmation"
else
	c4_fail "${scenario}" "no ACL restart marker before confirmation" "marker exists"
fi
sleep 1
c7_redis_events_stop "${events_pid}"
c4_assert_eq "${scenario}" "no Redis restart event before confirmation" "$(c7_redis_events_count)" "0"

c7_section "E2E-08 enable ACL persistence"
events_pid="$(c7_redis_events_start)"
c5_api_session
status="$(c6_api_json_status POST /api/v1/services/redis/redis/acl-file '{"confirm":true}' "${private}/acl-enable.json")"
c4_assert_eq "${scenario}" "confirmed ACL enable returns HTTP 200" "${status}" "200"
c4_assert_contains "${scenario}" "confirmed ACL enable reports enabled" "$(cat "${private}/acl-enable.json")" '"enabled":true'
c7_assert_file_contains "${scenario}" "redis.conf contains the managed aclfile directive" "${c7_redis_config}" "aclfile /data/users.acl"
if [ -f "${c7_redis_acl}" ]; then
	c4_pass "${scenario}" "users.acl exists after migration"
else
	c4_fail "${scenario}" "users.acl exists after migration" "missing"
fi
c4_assert_eq "${scenario}" "users.acl mode is 0644 (hashes only, readable by the Redis user)" "$(stat -c '%a' "${c7_redis_acl}" 2>/dev/null || true)" "644"
c4_assert_contains "${scenario}" "users.acl contains the default user" "$(cat "${c7_redis_acl}" 2>/dev/null || true)" "user default"
if compgen -G "${devstack_home}/services/redis/config/redis.conf.bak*" >/dev/null; then
	c4_pass "${scenario}" "redis.conf backup created"
else
	c4_fail "${scenario}" "redis.conf backup created" "no redis.conf.bak*"
fi
if c7_redis_wait 60; then
	c4_pass "${scenario}" "Redis healthy after ACL migration"
else
	c4_fail "${scenario}" "Redis healthy after ACL migration" "PING failed"
fi
sleep 1
c7_redis_events_stop "${events_pid}"
c4_assert_eq "${scenario}" "exactly one Redis restart for the migration" "$(c7_redis_events_count)" "1"

c7_section "E2E-08 users, reveal, rotate"
c5_cli --json db user create --service redis --name e2e_cache_ro --access-level read-only \
	>"${evidence_dir}/${scenario}/create-ro.json" 2>"${evidence_dir}/${scenario}/create-ro.err"
c5_cli --json db user create --service redis --name e2e_cache_full --access-level full \
	>"${evidence_dir}/${scenario}/create-full.json" 2>"${evidence_dir}/${scenario}/create-full.err"
ro_id="$(c6_json_field "$(cat "${evidence_dir}/${scenario}/create-ro.json")" id)"
full_id="$(c6_json_field "$(cat "${evidence_dir}/${scenario}/create-full.json")" id)"
[ -n "${ro_id}" ] && c4_pass "${scenario}" "read-only Redis user created" || c4_fail "${scenario}" "read-only Redis user created" "no id"
[ -n "${full_id}" ] && c4_pass "${scenario}" "full Redis user created" || c4_fail "${scenario}" "full Redis user created" "no id"

cp "$(c7_redis_secret_path e2e_cache_ro)" "${secret_store}/e2e_cache_ro.password"
cp "$(c7_redis_secret_path e2e_cache_full)" "${secret_store}/e2e_cache_full.password"
chmod 600 "${secret_store}/e2e_cache_ro.password" "${secret_store}/e2e_cache_full.password"
ro_pw="${secret_store}/e2e_cache_ro.password"
full_pw="${secret_store}/e2e_cache_full.password"

c5_cli --json db user list --service redis >"${evidence_dir}/${scenario}/users-list.json" 2>"${evidence_dir}/${scenario}/users-list.err"
c7_assert_text_lacks_secret "${scenario}" "user list JSON contains no read-only password" "${evidence_dir}/${scenario}/users-list.json" "${ro_pw}"
c7_assert_text_lacks_secret "${scenario}" "user list JSON contains no full password" "${evidence_dir}/${scenario}/users-list.json" "${full_pw}"

c5_cli --json db user password --service redis --user-id "${ro_id}" --reveal \
	>"${private}/ro-reveal.json" 2>"${private}/ro-reveal.err"
python3 - "${private}/ro-reveal.json" "${secret_store}/e2e_cache_ro.revealed" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
if not value:
    raise SystemExit(1)
open(sys.argv[2], "w", encoding="utf-8").write(value)
PY
chmod 600 "${secret_store}/e2e_cache_ro.revealed"
if cmp -s "${ro_pw}" "${secret_store}/e2e_cache_ro.revealed"; then
	c4_pass "${scenario}" "revealed read-only password matches the stored secret"
else
	c4_fail "${scenario}" "revealed read-only password matches the stored secret" "mismatch"
fi

c5_cli --json db user password --service redis --user-id "${ro_id}" --rotate --yes \
	>"${evidence_dir}/${scenario}/ro-rotate.json" 2>"${evidence_dir}/${scenario}/ro-rotate.err"
c5_cli --json db user password --service redis --user-id "${ro_id}" --reveal \
	>"${private}/ro-reveal-after.json" 2>"${private}/ro-reveal-after.err"
python3 - "${private}/ro-reveal-after.json" "${secret_store}/e2e_cache_ro.new" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
if not value:
    raise SystemExit(1)
open(sys.argv[2], "w", encoding="utf-8").write(value)
PY
chmod 600 "${secret_store}/e2e_cache_ro.new"
if cmp -s "${ro_pw}" "${secret_store}/e2e_cache_ro.new"; then
	c4_fail "${scenario}" "password rotation changed the secret" "same value"
else
	c4_pass "${scenario}" "password rotation changed the secret"
fi
if c7_redis_cli_as e2e_cache_ro "$(cat "${ro_pw}")" GET e2e:key >"${private}/old-password.out" 2>&1; then
	if grep -qiE 'WRONGPASS|NOAUTH|invalid username-password' "${private}/old-password.out"; then
		c4_pass "${scenario}" "old read-only password rejected after rotation"
	else
		c4_fail "${scenario}" "old read-only password rejected after rotation" "old password still works"
	fi
else
	c4_pass "${scenario}" "old read-only password rejected after rotation"
fi
if c7_redis_cli_as e2e_cache_ro "$(cat "${secret_store}/e2e_cache_ro.new")" GET e2e:key >/dev/null 2>&1; then
	c4_pass "${scenario}" "new read-only password authenticates"
else
	c4_fail "${scenario}" "new read-only password authenticates" "auth failed"
fi
cp "${secret_store}/e2e_cache_ro.new" "${ro_pw}"

c7_section "E2E-08 access boundaries"
if c7_redis_cli_as e2e_cache_ro "$(cat "${ro_pw}")" GET e2e:key >/dev/null 2>&1; then
	c4_pass "${scenario}" "read-only user can GET"
else
	c4_fail "${scenario}" "read-only user can GET" "GET failed"
fi
if c7_redis_cli_as e2e_cache_ro "$(cat "${ro_pw}")" SET e2e:key value >"${private}/ro-set.out" 2>&1; then
	if grep -qi 'NOPERM' "${private}/ro-set.out"; then
		c4_pass "${scenario}" "read-only user cannot SET"
	else
		c4_fail "${scenario}" "read-only user cannot SET" "SET succeeded"
	fi
else
	c4_pass "${scenario}" "read-only user cannot SET"
fi
if c7_redis_cli_as e2e_cache_full "$(cat "${full_pw}")" SET e2e:key value 2>/dev/null | grep -q OK; then
	c4_pass "${scenario}" "full user can SET"
else
	c4_fail "${scenario}" "full user can SET" "SET failed"
fi
for command in "CONFIG GET maxmemory" "FLUSHALL"; do
	label="$(printf '%s' "${command}" | tr '[:upper:] ' '[:lower:]-')"
	if c7_redis_cli_as e2e_cache_full "$(cat "${full_pw}")" ${command} 2>&1 | grep -qi 'NOPERM'; then
		c4_pass "${scenario}" "full user cannot run ${command}"
	else
		c4_fail "${scenario}" "full user cannot run ${command}" "command was not denied"
	fi
done

c7_section "E2E-08 Redis has no SQL surface"
c6_expect_cli_code "${scenario}" "db list rejects Redis with DATABASE_UNSUPPORTED" DATABASE_UNSUPPORTED \
	"${evidence_dir}/${scenario}/db-list.json" c5_cli --json db list --service redis
c6_expect_cli_code "${scenario}" "db create rejects Redis with DATABASE_UNSUPPORTED" DATABASE_UNSUPPORTED \
	"${evidence_dir}/${scenario}/db-create.json" c5_cli --json db create --service redis --name e2e_redis_db
c6_expect_cli_code "${scenario}" "db backup list rejects Redis with DATABASE_UNSUPPORTED" DATABASE_UNSUPPORTED \
	"${evidence_dir}/${scenario}/db-backup-list.json" c5_cli --json db backup list --service redis
status="$(c6_api_request GET /api/v1/services/redis/databases "" "${private}/redis-databases.json")"
c4_assert_eq "${scenario}" "API database list rejects Redis with HTTP 400" "${status}" "400"
c4_assert_contains "${scenario}" "API database list uses DATABASE_UNSUPPORTED" "$(cat "${private}/redis-databases.json")" '"code":"DATABASE_UNSUPPORTED"'
status="$(c6_api_request GET /api/v1/services/redis/backups "" "${private}/redis-backups.json")"
c4_assert_eq "${scenario}" "API backup list rejects Redis with HTTP 400" "${status}" "400"
c4_assert_contains "${scenario}" "API backup list uses DATABASE_UNSUPPORTED" "$(cat "${private}/redis-backups.json")" '"code":"DATABASE_UNSUPPORTED"'

c7_section "E2E-08 default password and audit"
c6_expect_cli_code "${scenario}" "default Redis password reveal fails before rotation" DATABASE_PASSWORD_MISSING \
	"${evidence_dir}/${scenario}/default-reveal-before.json" c5_cli --json db password --service redis --reveal
c5_cli --json db password --service redis --yes >"${evidence_dir}/${scenario}/default-rotate.json" 2>"${evidence_dir}/${scenario}/default-rotate.err"
c4_assert_contains "${scenario}" "default password rotation succeeded" "$(cat "${evidence_dir}/${scenario}/default-rotate.json")" '"rotated":true'
c5_cli --json db password --service redis --reveal >"${private}/default-reveal.json" 2>"${private}/default-reveal.err"
python3 - "${private}/default-reveal.json" "${secret_store}/redis_default.password" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
if not value:
    raise SystemExit(1)
open(sys.argv[2], "w", encoding="utf-8").write(value)
PY
chmod 600 "${secret_store}/redis_default.password"
if docker exec -i "${c7_redis_container}" redis-cli PING 2>&1 | grep -qi 'NOAUTH'; then
	c4_pass "${scenario}" "unauthenticated PING is rejected after default password rotation"
else
	c4_fail "${scenario}" "unauthenticated PING is rejected after default password rotation" "PING was not rejected"
fi
if c7_redis_cli PING 2>/dev/null | grep -q PONG; then
	c4_pass "${scenario}" "default password authenticates"
else
	c4_fail "${scenario}" "default password authenticates" "PONG missing"
fi
sleep 1
audit_copy="${private}/state-copy-${scenario}-audit"
c7_audit_copy "${audit_copy}"
audit_count="$(c7_audit_query "${audit_copy}" "SELECT count(*) FROM audit_events WHERE action='database.user.password_changed' AND object_name='default' AND result='ok';" 2>/dev/null || printf '0')"
c4_assert_eq "${scenario}" "default password rotation is audited" "${audit_count}" "1"
audit_reveal="$(c7_audit_query "${audit_copy}" "SELECT count(*) FROM audit_events WHERE action='database.user.password_revealed' AND object_name='default' AND result='ok';" 2>/dev/null || printf '0')"
c4_assert_eq "${scenario}" "default password reveal is audited" "${audit_reveal}" "1"

c7_section "E2E-08 Redis site links are rejected"
c5_create_project e2e08-site >/dev/null
c5_cli --json site add --name e2e08-site --path "${home}/projects/e2e08-site" --kind static \
	>"${evidence_dir}/${scenario}/site-add.json" 2>"${evidence_dir}/${scenario}/site-add.err"
c6_expect_cli_code "${scenario}" "CLI rejects a site link to Redis" DATABASE_UNSUPPORTED \
	"${evidence_dir}/${scenario}/link-cli.json" \
	c5_cli --json db link --site e2e08-site --service redis --database 0 --user-id "${ro_id}"
status="$(c6_api_post_json /api/v1/sites/e2e08-site/databases \
	"{\"service_id\":\"redis\",\"database\":\"0\",\"user_id\":\"${ro_id}\"}" "${private}/link-api.json")"
c4_assert_eq "${scenario}" "API rejects a site link to Redis with HTTP 400" "${status}" "400"
c4_assert_contains "${scenario}" "API Redis site link uses DATABASE_UNSUPPORTED" "$(cat "${private}/link-api.json")" '"code":"DATABASE_UNSUPPORTED"'

c7_section "E2E-08 final checks"
c7_assert_file_lacks_secret "${scenario}" "redis.conf contains no default password" "${c7_redis_config}" "${secret_store}/redis_default.password"
c7_assert_file_lacks_secret "${scenario}" "redis.conf contains no read-only password" "${c7_redis_config}" "${ro_pw}"
c7_assert_file_lacks_secret "${scenario}" "redis.conf contains no full password" "${c7_redis_config}" "${full_pw}"
c7_assert_acl_hashes_only "${scenario}" "users.acl contains hashes, not the read-only plaintext" "${c7_redis_acl}" "${ro_pw}"
c7_assert_acl_hashes_only "${scenario}" "users.acl contains hashes, not the full plaintext" "${c7_redis_acl}" "${full_pw}"
c7_assert_acl_hashes_only "${scenario}" "users.acl contains hashes, not the default plaintext" "${c7_redis_acl}" "${secret_store}/redis_default.password"
c7_assert_no_secret_in_tree "${scenario}" "logs and config contain no read-only password" "${logs_dir}" "${ro_pw}"
c7_assert_no_secret_in_tree "${scenario}" "logs and config contain no default password" "${logs_dir}" "${secret_store}/redis_default.password"
c7_scan "${scenario}"

if [ "${E2E_UI_HOLD:-1}" = "1" ]; then
	python3 - "${evidence_dir}/${scenario}/ui-fixture.json" "http://127.0.0.1:${ui_port}" "${ro_id}" "${full_id}" <<'PY'
import json, sys
json.dump({
    "ui_url": sys.argv[2],
    "service_id": "redis",
    "read_only_user": "e2e_cache_ro",
    "full_user": "e2e_cache_full",
    "read_only_id": sys.argv[3],
    "full_id": sys.argv[4],
}, open(sys.argv[1], "w", encoding="utf-8"), indent=2)
PY
	touch "${home}/ui-ready"
	for _ in $(seq 1 600); do
		[ -f "${home}/ui-stop" ] && break
		sleep 1
	done
	if [ -f "${home}/ui-stop" ]; then
		c4_pass "${scenario}" "live Redis dashboard tab check completed"
		if [ -f "${evidence_dir}/${scenario}/redis-ui.json" ]; then
			while IFS= read -r line; do
				case "${line}" in
				PASS\ *) c4_pass "${scenario}" "${line#PASS }" ;;
				FAIL\ *) c4_fail "${scenario}" "${line#FAIL }" ;;
				esac
			done < <(python3 - "${evidence_dir}/${scenario}/redis-ui.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
tabs = [t.strip() for t in data.get("tabs", [])]
def emit(ok, label, detail=""):
    print(("PASS " if ok else "FAIL ") + label + ((" (" + detail + ")") if detail and not ok else ""))
emit(tabs == ["Users", "Password"], "Redis service has only Users and Password tabs", str(tabs))
emit(not data.get("forbiddenTabs"), "Redis service has no Databases or Backups tabs", str(data.get("forbiddenTabs")))
emit(all(user in data.get("users", []) for user in ("e2e_cache_ro", "e2e_cache_full")), "Redis dashboard lists the managed users", str(data.get("users")))
emit(not data.get("consoleErrors"), "Redis dashboard has no console errors", str(data.get("consoleErrors")))
emit(data.get("auth") == "disabled", "Redis dashboard check records auth-disabled", str(data.get("auth")))
PY
			)
		else
			c4_fail "${scenario}" "live Redis dashboard wrote UI evidence" "missing redis-ui.json"
		fi
	else
		c4_fail "${scenario}" "live Redis dashboard tab check completed" "timed out waiting for ui-stop"
	fi
fi

status="$(c4_scenario_status "${scenario}")"
c7_record "${scenario}" "${status}" P0 \
	"$(c4_scenario_summary "${scenario}"); Redis users, reveal/rotate, ACL persistence, access boundaries, default password audit, Redis SQL surface, site-link rejection, live dashboard tabs"
printf '%s %s\n' "${scenario}" "$(c4_scenario_summary "${scenario}")"
