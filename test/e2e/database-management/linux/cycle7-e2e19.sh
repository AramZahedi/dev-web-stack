#!/usr/bin/env bash
# E2E-19 - Redis adversarial ACL migration and persistence on the disposable
# Linux helper/daemon. No real install object is touched.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle7-env.sh
source "${here}/cycle7-env.sh"

scenario="E2E-19"
c7_guard
c4_check_init "${scenario}"
trap c7_cleanup EXIT
c7_init
c7_redis_install

c7_redis_stop() {
	docker compose -p "$(c5_compose_project)" -f "${devstack_home}/services/compose.yaml" stop redis >/dev/null 2>&1 || true
}

c7_redis_start() {
	docker compose -p "$(c5_compose_project)" -f "${devstack_home}/services/compose.yaml" start redis >/dev/null 2>&1
	c5_wait_container_healthy "${c7_redis_container}" 120
	c7_redis_wait 60
}

c7_redis_config_without_acl() {
	python3 - "${c7_redis_config}" <<'PY'
import re, sys
path = sys.argv[1]
lines = open(path, encoding="utf-8").read().splitlines()
out = [line for line in lines if not re.match(r'^\s*aclfile\b', line, re.I)]
open(path, "w", encoding="utf-8").write("\n".join(out) + "\n")
PY
}

c7_redis_config_replace_acl() {
	local replacement="$1"
	python3 - "${c7_redis_config}" "${replacement}" <<'PY'
import re, sys
path, replacement = sys.argv[1], sys.argv[2]
lines = open(path, encoding="utf-8").read().splitlines()
out = []
replaced = False
for line in lines:
    if re.match(r'^\s*aclfile\b', line, re.I):
        if not replaced:
            out.append("aclfile " + replacement)
            replaced = True
        continue
    out.append(line)
if not replaced:
    out.append("aclfile " + replacement)
open(path, "w", encoding="utf-8").write("\n".join(out) + "\n")
PY
}

c7_redis_acl_summary() {
	python3 - "${c7_redis_acl}" <<'PY'
import re, sys
path = sys.argv[1]
for line in open(path, encoding="utf-8"):
    line = line.rstrip("\n")
    if line.startswith("user "):
        print(re.sub(r'#[0-9a-fA-F]{64}', '#<redacted-hash>', line))
PY
}

c7_start_helper_with_path() {
	local path_prefix="$1" log_name="$2"
	c5_stop_helper
	rm -f "${helper_socket}"
	PATH="${path_prefix}:$PATH" "${helper_bin}" --config "${helper_config}" >>"${logs_dir}/${log_name}" 2>&1 &
	helper_pid=$!
	for _ in $(seq 1 200); do
		[ -S "${helper_socket}" ] && return 0
		kill -0 "${helper_pid}" 2>/dev/null || c7_die "helper exited: $(tail -n 40 "${logs_dir}/${log_name}" 2>/dev/null || true)"
		sleep 0.1
	done
	c7_die "helper did not create ${helper_socket}"
}

c7_restore_normal_helper() {
	c5_stop_helper
	rm -f "${helper_socket}"
	c5_start_helper
}

c7_api_confirmed_acl() {
	local out="$1"
	c5_api_session
	c6_api_request POST /api/v1/services/redis/redis/acl-file '{"confirm":true}' "${out}"
}

c7_section "E2E-19 migration failure and rollback"
# The service starts without an ACL file. A Docker shim fails the first
# `compose restart redis`; the helper must restore redis.conf, remove the seeded
# ACL file/marker, and bring Redis back on the original config.
c7_redis_stop
c7_redis_config_without_acl
rm -f "${c7_redis_acl}" "${c7_redis_marker}"
c7_redis_start
c7_redis_cli SET e2e:rollback marker >/dev/null
config_before_failure="$(c7_redis_config_hash)"
shim_dir="${private}/docker-shim"
mkdir -p "${shim_dir}"
cat >"${shim_dir}/docker" <<'SH'
#!/usr/bin/env bash
set -eu
args=" $* "
count_file="${E2E_SHIM_COUNT:?}"
if [[ "${args}" == *" compose "* && "${args}" == *" restart "* && "${args}" == *" redis "* ]]; then
	if [ ! -s "${count_file}" ]; then
		printf '1\n' >"${count_file}"
		printf 'injected restart failure\n' >&2
		exit 1
	fi
fi
exec /usr/bin/docker "$@"
SH
chmod 755 "${shim_dir}/docker"
: >"${private}/shim-count"
export E2E_SHIM_COUNT="${private}/shim-count"
c7_start_helper_with_path "${shim_dir}" helper-shim.log
events_pid="$(c7_redis_events_start)"
status="$(c7_api_confirmed_acl "${private}/acl-failure.json")"
sleep 1
c7_redis_events_stop "${events_pid}"
if [ "${status}" != "200" ]; then
	c4_pass "${scenario}" "injected migration failure returned an error"
else
	c4_fail "${scenario}" "injected migration failure returned an error" "HTTP 200"
fi
c4_assert_eq "${scenario}" "redis.conf restored after restart failure" "$(c7_redis_config_hash)" "${config_before_failure}"
if compgen -G "${devstack_home}/services/redis/config/redis.conf.bak*" >/dev/null; then
	c4_pass "${scenario}" "redis.conf backup exists after the failed migration"
	backup_file="$(compgen -G "${devstack_home}/services/redis/config/redis.conf.bak*" | head -1)"
	c4_assert_eq "${scenario}" "redis.conf backup matches the pre-migration config" "$(sha256sum "${backup_file}" | awk '{print $1}')" "${config_before_failure}"
else
	c4_fail "${scenario}" "redis.conf backup exists after the failed migration" "no redis.conf.bak*"
fi
if [ ! -e "${c7_redis_acl}" ]; then
	c4_pass "${scenario}" "seeded users.acl removed after rollback"
else
	c4_fail "${scenario}" "seeded users.acl removed after rollback" "file remains"
fi
if [ ! -e "${c7_redis_marker}" ]; then
	c4_pass "${scenario}" "restart marker removed after rollback"
else
	c4_fail "${scenario}" "restart marker removed after rollback" "marker remains"
fi
if c7_redis_wait 60; then
	c4_pass "${scenario}" "Redis recovered after failed migration"
else
	c4_fail "${scenario}" "Redis recovered after failed migration" "PING failed"
fi
if [ "$(c7_redis_cli GET e2e:rollback 2>/dev/null | tr -d '\r')" = "marker" ]; then
	c4_pass "${scenario}" "Redis data survived the failed migration"
else
	c4_fail "${scenario}" "Redis data survived the failed migration" "marker missing"
fi
unset E2E_SHIM_COUNT
c7_restore_normal_helper
if c5_cli --json db user list --service redis >/dev/null 2>&1; then
	c4_pass "${scenario}" "normal helper resumes after rollback test"
else
	c4_fail "${scenario}" "normal helper resumes after rollback test" "user list failed"
fi

c7_section "E2E-19 existing users.acl with custom rules is preserved"
c7_redis_stop
c7_redis_config_without_acl
custom_pw="${secret_store}/e2e_custom.password"
python3 -c 'import secrets,sys; sys.stdout.write(secrets.token_hex(24))' >"${custom_pw}"
chmod 600 "${custom_pw}"
custom_hash="$(sha256sum "${custom_pw}" | awk '{print $1}')"
{
	printf 'user default on nopass ~* &* +@all\n'
	printf 'user e2e_custom on #%s ~e2e:* +get +set\n' "${custom_hash}"
} >"${c7_redis_acl}"
chmod 644 "${c7_redis_acl}"
acl_before="$(c7_redis_acl_hash)"
c7_redis_start
config_before="$(c7_redis_config_hash)"
started_before="$(c7_redis_started_at)"
c5_api_session
status="$(c6_api_request POST /api/v1/services/redis/redis/acl-file '{"confirm":false}' "${private}/acl-unconfirmed.json")"
c4_assert_eq "${scenario}" "unconfirmed ACL enable returns HTTP 409" "${status}" "409"
c4_assert_contains "${scenario}" "unconfirmed ACL enable uses CONFIRMATION_REQUIRED" "$(cat "${private}/acl-unconfirmed.json")" '"code":"CONFIRMATION_REQUIRED"'
c4_assert_eq "${scenario}" "existing ACL file unchanged before confirmation" "$(c7_redis_acl_hash)" "${acl_before}"
c4_assert_eq "${scenario}" "redis.conf unchanged before confirmation" "$(c7_redis_config_hash)" "${config_before}"
c4_assert_eq "${scenario}" "Redis not restarted before confirmation" "$(c7_redis_started_at)" "${started_before}"
events_pid="$(c7_redis_events_start)"
status="$(c7_api_confirmed_acl "${private}/acl-existing.json")"
sleep 1
c7_redis_events_stop "${events_pid}"
c4_assert_eq "${scenario}" "confirmed enable migrates an existing ACL file" "${status}" "200"
c4_assert_eq "${scenario}" "existing ACL file preserved byte-for-byte" "$(c7_redis_acl_hash)" "${acl_before}"
c4_assert_eq "${scenario}" "exactly one restart for the existing-ACL migration" "$(c7_redis_events_count)" "1"
c4_assert_eq "${scenario}" "existing ACL file mode normalized to 0644" "$(stat -c '%a' "${c7_redis_acl}" 2>/dev/null || true)" "644"
c4_assert_contains "${scenario}" "redis.conf points at the managed ACL file" "$(cat "${c7_redis_config}")" "aclfile /data/users.acl"
c7_redis_acl_summary >"${evidence_dir}/${scenario}/acl-summary.txt"
c4_assert_contains "${scenario}" "custom ACL rules survive migration" "$(cat "${evidence_dir}/${scenario}/acl-summary.txt")" "~e2e:* +get +set"
whoami="$(c7_redis_cli_as e2e_custom "$(cat "${custom_pw}")" ACL WHOAMI 2>&1 || true)"
c4_assert_contains "${scenario}" "custom ACL user authenticates after migration" "${whoami}" "e2e_custom"
if c7_redis_cli_as e2e_custom "$(cat "${custom_pw}")" SET e2e:ok value 2>/dev/null | grep -q OK; then
	c4_pass "${scenario}" "custom ACL user can write its allowed key after migration"
else
	c4_fail "${scenario}" "custom ACL user can write its allowed key after migration" "SET failed"
fi
if c7_redis_cli_as e2e_custom "$(cat "${custom_pw}")" SET other value >"${private}/custom-set-other.out" 2>&1; then
	if grep -qi 'NOPERM' "${private}/custom-set-other.out"; then
		c4_pass "${scenario}" "custom ACL key restriction survives migration"
	else
		c4_fail "${scenario}" "custom ACL key restriction survives migration" "SET other succeeded"
	fi
else
	c4_pass "${scenario}" "custom ACL key restriction survives migration"
fi

# Save the managed state for the foreign-aclfile phase.
cp "${c7_redis_config}" "${private}/managed-redis.conf"
cp "${c7_redis_acl}" "${private}/managed-users.acl"

c7_section "E2E-19 foreign aclfile is rejected"
c7_redis_stop
c7_redis_config_replace_acl "/data/foreign-users.acl"
{
	printf 'user default on nopass ~* &* +@all\n'
	printf 'user foreign_user on #%s ~foreign:* +get\n' "$(printf '%s' "foreign-not-a-real-password" | sha256sum | awk '{print $1}')"
} >"${devstack_home}/services/redis/data/foreign-users.acl"
chmod 644 "${devstack_home}/services/redis/data/foreign-users.acl"
c7_redis_start
foreign_config_hash="$(c7_redis_config_hash)"
foreign_acl_hash="$(sha256sum "${devstack_home}/services/redis/data/foreign-users.acl" | awk '{print $1}')"
managed_acl_hash="$(c7_redis_acl_hash)"
events_pid="$(c7_redis_events_start)"
status="$(c7_api_confirmed_acl "${private}/acl-foreign.json")"
sleep 1
c7_redis_events_stop "${events_pid}"
if [ "${status}" != "200" ]; then
	c4_pass "${scenario}" "foreign aclfile migration is rejected"
else
	c4_fail "${scenario}" "foreign aclfile migration is rejected" "HTTP 200"
fi
c4_assert_contains "${scenario}" "foreign aclfile rejection uses DATABASE_UNSUPPORTED" "$(cat "${private}/acl-foreign.json")" '"code":"DATABASE_UNSUPPORTED"'
c4_assert_eq "${scenario}" "foreign redis.conf is not rewritten" "$(c7_redis_config_hash)" "${foreign_config_hash}"
c4_assert_eq "${scenario}" "foreign ACL file is not rewritten" "$(sha256sum "${devstack_home}/services/redis/data/foreign-users.acl" | awk '{print $1}')" "${foreign_acl_hash}"
c4_assert_eq "${scenario}" "managed users.acl is not rewritten during foreign rejection" "$(c7_redis_acl_hash)" "${managed_acl_hash}"
c4_assert_eq "${scenario}" "no Redis restart during foreign rejection" "$(c7_redis_events_count)" "0"
if [ ! -e "${c7_redis_marker}" ]; then
	c4_pass "${scenario}" "no restart marker written for foreign aclfile"
else
	c4_fail "${scenario}" "no restart marker written for foreign aclfile" "marker exists"
fi
# Restore the managed state.
c7_redis_stop
cp "${private}/managed-redis.conf" "${c7_redis_config}"
cp "${private}/managed-users.acl" "${c7_redis_acl}"
chmod 644 "${c7_redis_acl}"
c7_redis_start

c7_section "E2E-19 idempotent enable and permissions"
config_hash="$(c7_redis_config_hash)"
acl_hash="$(c7_redis_acl_hash)"
events_pid="$(c7_redis_events_start)"
status="$(c7_api_confirmed_acl "${private}/acl-idempotent.json")"
sleep 1
c7_redis_events_stop "${events_pid}"
c4_assert_eq "${scenario}" "second confirmed enable is idempotent" "${status}" "200"
c4_assert_eq "${scenario}" "idempotent enable does not change redis.conf" "$(c7_redis_config_hash)" "${config_hash}"
c4_assert_eq "${scenario}" "idempotent enable does not change users.acl" "$(c7_redis_acl_hash)" "${acl_hash}"
c4_assert_eq "${scenario}" "idempotent enable does not restart Redis" "$(c7_redis_events_count)" "0"
host_stat="$(stat -c '%a %u:%g' "${c7_redis_acl}" 2>/dev/null || true)"
printf 'host users.acl mode uid:gid = %s\n' "${host_stat}" >"${evidence_dir}/${scenario}/acl-permissions.txt"
c4_assert_eq "${scenario}" "users.acl host mode is 0644 (hashes only, container-readable)" "${host_stat%% *}" "644"
container_stat="$(docker exec "${c7_redis_container}" stat -c '%a %u:%g' /data/users.acl 2>/dev/null || true)"
printf 'container users.acl mode uid:gid = %s\n' "${container_stat}" >>"${evidence_dir}/${scenario}/acl-permissions.txt"
docker exec "${c7_redis_container}" id redis >>"${evidence_dir}/${scenario}/acl-permissions.txt" 2>&1 || true
if docker exec -u redis "${c7_redis_container}" test -r /data/users.acl 2>/dev/null; then
	c4_pass "${scenario}" "users.acl is readable by the Redis container"
else
	c4_fail "${scenario}" "users.acl is readable by the Redis container" "not readable"
fi
c7_assert_acl_hashes_only "${scenario}" "custom users.acl stores a hash, not plaintext" "${c7_redis_acl}" "${custom_pw}"

c7_section "E2E-19 username matrix"
c6_expect_cli_code "${scenario}" "Unicode Redis username is rejected" INVALID_REQUEST \
	"${evidence_dir}/${scenario}/user-unicode.json" c5_cli --json db user create --service redis --name 'e2e_caché'
c6_expect_cli_code "${scenario}" "protected default user cannot be created" DATABASE_PROTECTED \
	"${evidence_dir}/${scenario}/user-default.json" c5_cli --json db user create --service redis --name default
long63="$(python3 -c 'print("e2e_"+"a"*59)')"
long64="$(python3 -c 'print("e2e_"+"a"*60)')"
c5_cli --json db user create --service redis --name "${long63}" --access-level read-only \
	>"${evidence_dir}/${scenario}/user-long63.json" 2>"${evidence_dir}/${scenario}/user-long63.err"
c4_assert_contains "${scenario}" "63-character Redis username is accepted" "$(cat "${evidence_dir}/${scenario}/user-long63.json")" '"username":"'"${long63}"'"'
c6_expect_cli_code "${scenario}" "64-character Redis username is rejected" INVALID_REQUEST \
	"${evidence_dir}/${scenario}/user-long64.json" c5_cli --json db user create --service redis --name "${long64}"
c5_cli --json db user create --service redis --name e2e_case_lower --access-level read-only \
	>"${evidence_dir}/${scenario}/user-case-lower.json" 2>"${evidence_dir}/${scenario}/user-case-lower.err"
c5_cli --json db user create --service redis --name E2E_CASE_LOWER --access-level read-only \
	>"${evidence_dir}/${scenario}/user-case-upper.json" 2>"${evidence_dir}/${scenario}/user-case-upper.err"
c5_cli --json db user list --service redis >"${evidence_dir}/${scenario}/users-after-case.json" 2>/dev/null
c4_assert_contains "${scenario}" "case-distinct Redis usernames both exist" "$(cat "${evidence_dir}/${scenario}/users-after-case.json")" '"username":"e2e_case_lower"'
c4_assert_contains "${scenario}" "uppercase Redis username exists separately" "$(cat "${evidence_dir}/${scenario}/users-after-case.json")" '"username":"E2E_CASE_LOWER"'
c5_cli --json db user create --service redis --name e2e_dup --access-level read-only \
	>"${evidence_dir}/${scenario}/user-dup-first.json" 2>"${evidence_dir}/${scenario}/user-dup-first.err"
dup_id="$(c6_json_field "$(cat "${evidence_dir}/${scenario}/user-dup-first.json")" id)"
cp "$(c7_redis_secret_path e2e_dup)" "${secret_store}/e2e_dup.password"
chmod 600 "${secret_store}/e2e_dup.password"
c6_expect_cli_code "${scenario}" "duplicate Redis username is rejected" DATABASE_CONFLICT \
	"${evidence_dir}/${scenario}/user-dup-second.json" c5_cli --json db user create --service redis --name e2e_dup --access-level full
if c7_redis_cli_as e2e_dup "$(cat "${secret_store}/e2e_dup.password")" GET e2e:ok >/dev/null 2>&1; then
	c4_pass "${scenario}" "duplicate create did not overwrite the original password"
else
	c4_fail "${scenario}" "duplicate create did not overwrite the original password" "original password failed"
fi
default_id="$(c7_redis_default_id)"
c6_expect_cli_code "${scenario}" "default Redis user cannot be deleted" DATABASE_PROTECTED \
	"${evidence_dir}/${scenario}/user-delete-default.json" c5_cli --json db user delete --service redis --user-id "${default_id}" --yes

c7_section "E2E-19 concurrency"
c5_cli --json db user create --service redis --name e2e_conc_same --access-level read-only \
	>"${private}/conc-create-1.json" 2>"${private}/conc-create-1.err" &
pid1=$!
c5_cli --json db user create --service redis --name e2e_conc_same --access-level read-only \
	>"${private}/conc-create-2.json" 2>"${private}/conc-create-2.err" &
pid2=$!
wait "${pid1}" || true
wait "${pid2}" || true
conc_ok=0
for out in "${private}/conc-create-1.json" "${private}/conc-create-2.json"; do
	if grep -q '"username":"e2e_conc_same"' "${out}" 2>/dev/null; then conc_ok=$((conc_ok + 1)); fi
done
c4_assert_eq "${scenario}" "concurrent same-name create has exactly one winner" "${conc_ok}" "1"
if c5_cli --json db user list --service redis 2>/dev/null | grep -q '"username":"e2e_conc_same"'; then
	c4_pass "${scenario}" "concurrent create leaves one consistent user"
else
	c4_fail "${scenario}" "concurrent create leaves one consistent user" "user missing"
fi
c5_cli --json db user create --service redis --name e2e_conc_rotate --access-level read-only \
	>"${private}/conc-rotate-create.json" 2>"${private}/conc-rotate-create.err"
conc_rotate_id="$(c6_json_field "$(cat "${private}/conc-rotate-create.json")" id)"
cp "$(c7_redis_secret_path e2e_conc_rotate)" "${secret_store}/e2e_conc_rotate.before"
chmod 600 "${secret_store}/e2e_conc_rotate.before"
c5_cli --json db user password --service redis --user-id "${conc_rotate_id}" --rotate --yes \
	>"${private}/conc-rotate-1.json" 2>"${private}/conc-rotate-1.err" &
pid1=$!
c5_cli --json db user password --service redis --user-id "${conc_rotate_id}" --rotate --yes \
	>"${private}/conc-rotate-2.json" 2>"${private}/conc-rotate-2.err" &
pid2=$!
wait "${pid1}" || true
wait "${pid2}" || true
c5_cli --json db user password --service redis --user-id "${conc_rotate_id}" --reveal \
	>"${private}/conc-rotate-reveal.json" 2>"${private}/conc-rotate-reveal.err"
python3 - "${private}/conc-rotate-reveal.json" "${secret_store}/e2e_conc_rotate.after" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
if not value:
    raise SystemExit(1)
open(sys.argv[2], "w", encoding="utf-8").write(value)
PY
chmod 600 "${secret_store}/e2e_conc_rotate.after"
if c7_redis_cli_as e2e_conc_rotate "$(cat "${secret_store}/e2e_conc_rotate.after")" GET e2e:ok >/dev/null 2>&1; then
	c4_pass "${scenario}" "concurrent rotation leaves the stored password valid"
else
	c4_fail "${scenario}" "concurrent rotation leaves the stored password valid" "final password failed"
fi
c5_cli --json db user create --service redis --name e2e_conc_delete --access-level read-only \
	>"${private}/conc-delete-create.json" 2>"${private}/conc-delete-create.err"
conc_delete_id="$(c6_json_field "$(cat "${private}/conc-delete-create.json")" id)"
c5_cli --json db user delete --service redis --user-id "${conc_delete_id}" --yes \
	>"${private}/conc-delete-1.json" 2>"${private}/conc-delete-1.err" &
pid1=$!
c5_cli --json db user password --service redis --user-id "${conc_delete_id}" --rotate --yes \
	>"${private}/conc-delete-2.json" 2>"${private}/conc-delete-2.err" &
pid2=$!
wait "${pid1}" || true
wait "${pid2}" || true
if c5_cli --json db user list --service redis 2>/dev/null | grep -q '"username":"e2e_conc_delete"'; then
	c4_fail "${scenario}" "concurrent delete/rotate leaves no stale user metadata" "user remains"
else
	c4_pass "${scenario}" "concurrent delete/rotate leaves no stale user metadata"
fi

c7_section "E2E-19 Redis restart with users"
users_before="$(c5_cli --json db user list --service redis 2>/dev/null)"
acl_hash="$(c7_redis_acl_hash)"
c5_cli service restart redis >"${evidence_dir}/${scenario}/restart.json" 2>"${evidence_dir}/${scenario}/restart.err" ||
	docker compose -p "$(c5_compose_project)" -f "${devstack_home}/services/compose.yaml" restart redis >/dev/null 2>&1
c5_wait_container_healthy "${c7_redis_container}" 120
c7_redis_wait 60
users_after="$(c5_cli --json db user list --service redis 2>/dev/null)"
c4_assert_eq "${scenario}" "Redis user inventory survives a restart" "${users_after}" "${users_before}"
c4_assert_eq "${scenario}" "users.acl is unchanged across restart" "$(c7_redis_acl_hash)" "${acl_hash}"
if c7_redis_cli_as e2e_dup "$(cat "${secret_store}/e2e_dup.password")" GET e2e:ok >/dev/null 2>&1; then
	c4_pass "${scenario}" "stored user password still authenticates after restart"
else
	c4_fail "${scenario}" "stored user password still authenticates after restart" "auth failed"
fi
if c7_redis_cli_as e2e_custom "$(cat "${custom_pw}")" GET e2e:ok >/dev/null 2>&1; then
	c4_pass "${scenario}" "custom ACL user survives a restart"
else
	c4_fail "${scenario}" "custom ACL user survives a restart" "auth failed"
fi

c7_section "E2E-19 secret scan"
c7_redis_acl_summary >"${evidence_dir}/${scenario}/acl-summary-final.txt"
c7_assert_file_lacks_secret "${scenario}" "redis.conf contains no custom password" "${c7_redis_config}" "${custom_pw}"
c7_assert_acl_hashes_only "${scenario}" "users.acl contains no custom plaintext" "${c7_redis_acl}" "${custom_pw}"
c7_assert_no_secret_in_tree "${scenario}" "logs and config contain no custom password" "${logs_dir}" "${custom_pw}"
c7_scan "${scenario}"

status="$(c4_scenario_status "${scenario}")"
c7_record "${scenario}" "${status}" P1 \
	"$(c4_scenario_summary "${scenario}"); custom ACL preservation, foreign aclfile rejection, restart-failure rollback, idempotent enable, permissions, usernames, concurrency, restart persistence, secret scan"
printf '%s %s\n' "${scenario}" "$(c4_scenario_summary "${scenario}")"
