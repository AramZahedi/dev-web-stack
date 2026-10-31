#!/usr/bin/env bash
# Cycle 5 / E2E-04: existing-user warning, no auto-grant, metadata-only
# external user, Set password, unsupported stdin password, explicit grant.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

scenario="E2E-04"
c5_guard
c4_check_init "${scenario}"
if [ "${E2E_CYCLE5_SHARED:-0}" != "1" ]; then
	trap c5_cleanup EXIT
	c5_init
fi
c5_ensure_base

c5_cli --json db create --service mysql --name e2e04_target >"${evidence_dir}/${scenario}/create-target.json" 2>"${evidence_dir}/${scenario}/create-target.err" &&
	c4_pass "${scenario}" "target database created" || c4_fail "${scenario}" "target database created"
c5_cli --json db user create --service mysql --name e2e04_existing >"${evidence_dir}/${scenario}/create-existing.json" 2>"${evidence_dir}/${scenario}/create-existing.err" &&
	c4_pass "${scenario}" "existing managed user created without a grant" || c4_fail "${scenario}" "existing managed user created without a grant"
uid="$(c5_user_id mysql e2e04_existing)"
[ -n "${uid}" ] && c4_pass "${scenario}" "existing user id resolved" || c4_fail "${scenario}" "existing user id resolved"

c5_create_project e2e04-static >/dev/null
c5_cli --json site add --name e2e04-site --path "${home}/projects/e2e04-static" --kind static \
	>"${evidence_dir}/${scenario}/site-add.json" 2>"${evidence_dir}/${scenario}/site-add.err" &&
	c4_pass "${scenario}" "site created without a database" || c4_fail "${scenario}" "site created without a database"

if c5_cli --json db link --site e2e04-site --service mysql --database e2e04_target --user-id "${uid}" \
	>"${private}/existing-link.json" 2>"${private}/existing-link.err"; then
	c4_pass "${scenario}" "existing user linked without a grant"
else
	c4_fail "${scenario}" "existing user linked without a grant"
fi
if grep -q 'no grant' "${private}/existing-link.json"; then
	c4_pass "${scenario}" "link warns that the existing user has no grant"
else
	c4_fail "${scenario}" "link warns that the existing user has no grant"
fi

c5_cli --json db user list --service mysql >"${private}/users-after-link.json"
if python3 - "${private}/users-after-link.json" <<'PY'
import json, sys
users = json.load(open(sys.argv[1], encoding="utf-8")).get("users", [])
user = next((u for u in users if u.get("username") == "e2e04_existing"), None)
raise SystemExit(0 if user is not None and not any(g.get("database") == "e2e04_target" for g in user.get("grants", [])) else 1)
PY
then
	c4_pass "${scenario}" "metadata did not auto-grant the existing user"
else
	c4_fail "${scenario}" "metadata did not auto-grant the existing user"
fi
engine_grants="$(c4_mysql_root_query -e "SHOW GRANTS FOR 'e2e04_existing'@'%';")"
if printf '%s' "${engine_grants}" | grep -q 'e2e04_target'; then
	c4_fail "${scenario}" "engine did not auto-grant the existing user"
else
	c4_pass "${scenario}" "engine did not auto-grant the existing user"
fi

c5_api_get "/api/v1/services/mysql/users/${uid}" >"${private}/existing-user.json"
if python3 - "${private}/existing-user.json" <<'PY'
import json, sys
user = json.load(open(sys.argv[1], encoding="utf-8"))
raise SystemExit(0 if user.get("username") == "e2e04_existing" else 1)
PY
then
	c4_pass "${scenario}" "Manage database user API resolves the user editor target"
else
	c4_fail "${scenario}" "Manage database user API resolves the user editor target"
fi

# Metadata-only external user created directly in the engine.
ext_pw="$(python3 -c 'import secrets; print(secrets.token_hex(16))')"
printf '%s' "${ext_pw}" >"${private}/e2e04-external.pw"
chmod 600 "${private}/e2e04-external.pw"
c4_mysql_root_sql <<SQL
CREATE USER IF NOT EXISTS 'e2e04_external'@'%' IDENTIFIED BY '${ext_pw}';
GRANT SELECT ON e2e04_target.* TO 'e2e04_external'@'%';
SQL
c4_pass "${scenario}" "external engine user created with a grant"
c5_cli --json db user list --service mysql >"${private}/users-external.json"
if python3 - "${private}/users-external.json" <<'PY'
import json, sys
users = json.load(open(sys.argv[1], encoding="utf-8")).get("users", [])
user = next((u for u in users if u.get("username") == "e2e04_external"), None)
raise SystemExit(0 if user is not None and user.get("password_managed") is False and user.get("managed") is False else 1)
PY
then
	c4_pass "${scenario}" "external user discovered as metadata-only"
else
	c4_fail "${scenario}" "external user discovered as metadata-only"
fi
ext_uid="$(c5_user_id mysql e2e04_external)"
[ -n "${ext_uid}" ] && c4_pass "${scenario}" "external user id resolved" || c4_fail "${scenario}" "external user id resolved"

c5_create_project e2e04-external >/dev/null
c5_cli --json site add --name e2e04-external --path "${home}/projects/e2e04-external" --kind static \
	>"${evidence_dir}/${scenario}/external-site-add.json" 2>"${evidence_dir}/${scenario}/external-site-add.err" &&
	c4_pass "${scenario}" "external-link site created" || c4_fail "${scenario}" "external-link site created"
c5_cli --json db link --site e2e04-external --service mysql --database e2e04_target --user-id "${ext_uid}" \
	>"${private}/external-link.json" 2>"${private}/external-link.err" &&
	c4_pass "${scenario}" "metadata-only external user linked" || c4_fail "${scenario}" "metadata-only external user linked"
if python3 - "${private}/external-link.json" <<'PY'
import json, sys
link = json.load(open(sys.argv[1], encoding="utf-8"))
conn = link.get("connection", {})
env = conn.get("env", {})
raise SystemExit(0 if link.get("password_managed") is False and not conn.get("url") and "DB_PASSWORD" not in env and "DATABASE_URL" not in env else 1)
PY
then
	c4_pass "${scenario}" "metadata-only link omits URL and password keys"
else
	c4_fail "${scenario}" "metadata-only link omits URL and password keys"
fi
c5_capture_site_env e2e04-external e2e04ext
ext_summary="$(cat "${evidence_dir}/${scenario}/e2e04ext-env-summary.txt")"
c4_assert_contains "${scenario}" "metadata-only env DB_DATABASE" "${ext_summary}" "DB_DATABASE=e2e04_target"
c4_assert_contains "${scenario}" "metadata-only env DB_USERNAME" "${ext_summary}" "DB_USERNAME=e2e04_external"
c4_assert_contains "${scenario}" "metadata-only env DB_PASSWORD absent" "${ext_summary}" "DB_PASSWORD_PRESENT=false"
c4_assert_contains "${scenario}" "metadata-only env DATABASE_URL absent" "${ext_summary}" "DATABASE_URL_PRESENT=false"

c5_terminal_probe e2e04-external "${evidence_dir}/${scenario}/external-terminal.txt"
ext_terminal="$(cat "${evidence_dir}/${scenario}/external-terminal.txt")"
c4_assert_contains "${scenario}" "terminal metadata-only DB_PASSWORD absent" "${ext_terminal}" "E2E_TERM DB_PASSWORD_PRESENT=false"
c4_assert_contains "${scenario}" "terminal metadata-only DATABASE_URL absent" "${ext_terminal}" "E2E_TERM DATABASE_URL_PRESENT=false"

# Site Detail shows Password not managed through the link/user views.
if python3 - "${private}/external-link.json" "${private}/users-external.json" <<'PY'
import json, sys
link = json.load(open(sys.argv[1], encoding="utf-8"))
users = json.load(open(sys.argv[2], encoding="utf-8")).get("users", [])
user = next((u for u in users if u.get("username") == "e2e04_external"), None)
raise SystemExit(0 if link.get("password_managed") is False and user is not None and user.get("password_managed") is False else 1)
PY
then
	c4_pass "${scenario}" "Site Detail data reports Password not managed"
else
	c4_fail "${scenario}" "Site Detail data reports Password not managed"
fi

# The Set password action is the user password endpoint used by the editor.
c5_api_session
c5_api_post "/api/v1/services/mysql/users/${ext_uid}/password" '{}' >"${private}/external-set-password.json"
if python3 - "${private}/external-set-password.json" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
raise SystemExit(0 if value else 1)
PY
then
	c4_pass "${scenario}" "Set password action returns a generated secret"
else
	c4_fail "${scenario}" "Set password action returns a generated secret"
fi
c5_cli --json db user list --service mysql >"${private}/users-external-after.json"
if python3 - "${private}/users-external-after.json" <<'PY'
import json, sys
users = json.load(open(sys.argv[1], encoding="utf-8")).get("users", [])
user = next((u for u in users if u.get("username") == "e2e04_external"), None)
raise SystemExit(0 if user is not None and user.get("password_managed") is True else 1)
PY
then
	c4_pass "${scenario}" "Set password marks the external user managed"
else
	c4_fail "${scenario}" "Set password marks the external user managed"
fi
c5_capture_site_env e2e04-external e2e04ext-after
after_summary="$(cat "${evidence_dir}/${scenario}/e2e04ext-after-env-summary.txt")"
c4_assert_contains "${scenario}" "Set password makes DB_PASSWORD available" "${after_summary}" "DB_PASSWORD_PRESENT=true"

# Unsupported link-time password must fail before reading stdin.
fifo="${private}/stdin.fifo"
rm -f "${fifo}"
mkfifo "${fifo}"
chmod 600 "${fifo}"
sleep 30 >"${fifo}" &
writer_pid=$!
start_ns="$(date +%s%N)"
set +e
stdin_output="$(timeout 5 runuser -u "${c5_user}" -- env HOME="${c5_user_home}" \
	"${ds}" --home "${devstack_home}" --helper-socket "${helper_socket}" --json \
	site add --name e2e04-stdin --path "${home}/projects/e2e04-static" --kind static \
	--database-service mysql --database e2e04_target --database-user "${uid}" \
	--database-user-password-stdin <"${fifo}" 2>&1)"
stdin_status=$?
set -e
end_ns="$(date +%s%N)"
kill "${writer_pid}" 2>/dev/null || true
wait "${writer_pid}" 2>/dev/null || true
rm -f "${fifo}"
elapsed_ms=$(( (end_ns - start_ns) / 1000000 ))
if [ "${stdin_status}" -ne 0 ] && printf '%s' "${stdin_output}" | grep -q 'DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED'; then
	c4_pass "${scenario}" "database-user-password-stdin fails with DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED"
else
	c4_fail "${scenario}" "database-user-password-stdin fails with DATABASE_EXTERNAL_PASSWORD_UNSUPPORTED"
fi
if [ "${elapsed_ms}" -lt 3000 ]; then
	c4_pass "${scenario}" "database-user-password-stdin did not read stdin (${elapsed_ms}ms)"
else
	c4_fail "${scenario}" "database-user-password-stdin did not read stdin" "${elapsed_ms}ms"
fi

# Explicit grant + site restart.
c5_cli --json db user grant --service mysql --user-id "${uid}" --database e2e04_target --preset read_only \
	>"${evidence_dir}/${scenario}/grant.json" 2>"${evidence_dir}/${scenario}/grant.err" &&
	c4_pass "${scenario}" "explicit grant applied" || c4_fail "${scenario}" "explicit grant applied"
engine_grants="$(c4_mysql_root_query -e "SHOW GRANTS FOR 'e2e04_existing'@'%';")"
if printf '%s' "${engine_grants}" | grep -q 'e2e04_target'; then
	c4_pass "${scenario}" "engine shows the explicit grant"
else
	c4_fail "${scenario}" "engine shows the explicit grant"
fi
c5_api_get "/api/v1/sites/e2e04-site/databases" >"${private}/existing-links-after-grant.json"
if python3 - "${private}/existing-links-after-grant.json" <<'PY'
import json, sys
links = json.load(open(sys.argv[1], encoding="utf-8")).get("links", [])
link = links[0] if links else {}
raise SystemExit(0 if link.get("database") == "e2e04_target" and not link.get("error") else 1)
PY
then
	c4_pass "${scenario}" "link warning clears after the explicit grant"
else
	c4_fail "${scenario}" "link warning clears after the explicit grant"
fi
c5_cli --json site restart e2e04-site >"${evidence_dir}/${scenario}/restart.json" 2>"${evidence_dir}/${scenario}/restart.err" &&
	c4_pass "${scenario}" "site restarts after the explicit grant" || c4_fail "${scenario}" "site restarts after the explicit grant"
c5_terminal_probe e2e04-site "${evidence_dir}/${scenario}/managed-terminal.txt"
managed_terminal="$(cat "${evidence_dir}/${scenario}/managed-terminal.txt")"
c4_assert_contains "${scenario}" "restarted managed link has DB_PASSWORD" "${managed_terminal}" "E2E_TERM DB_PASSWORD_PRESENT=true"

c5_audit_copy "${private}/state-copy" >/dev/null 2>&1 || true
c5_secret_scan "${scenario}" && c4_pass "${scenario}" "secret scan clean over evidence, logs, and state copy" || c4_fail "${scenario}" "secret scan clean over evidence, logs, and state copy"

c5_record "${scenario}" "$(c4_scenario_status "${scenario}")" P0 \
	"Existing user warning/no auto-grant; metadata-only external link; Set password; unsupported stdin; explicit grant + restart" \
	"UI Set password button and Manage user route are covered by the live Playwright spec"
c4_scenario_summary "${scenario}"
