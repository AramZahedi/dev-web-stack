#!/usr/bin/env bash
# Cycle 5 / E2E-17: site integration adversarial checks: multi-link env,
# reserved keys, Add Site validation, concurrency, stale links, reveal/restart,
# and linked-site restore/import gates.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

scenario="E2E-17"
c5_guard
c4_check_init "${scenario}"
if [ "${E2E_CYCLE5_SHARED:-0}" != "1" ]; then
	trap c5_cleanup EXIT
	c5_init
fi
c5_ensure_base

expect_fail() {
	local label="$1"
	shift
	if "$@" >/dev/null 2>&1; then
		c4_fail "${scenario}" "${label}"
	else
		c4_pass "${scenario}" "${label}"
	fi
}

site_exists() { [ -n "$(c5_site_id "$1")" ]; }

expect_linked_site_gate() {
	local label="$1" output
	shift
	if output="$("$@" 2>&1)"; then
		c4_fail "${scenario}" "${label}" "command succeeded"
		return 0
	fi
	if printf '%s' "${output}" | grep -q 'DATABASE_CONFLICT' && printf '%s' "${output}" | grep -qi 'stop linked sites first'; then
		c4_pass "${scenario}" "${label}"
	else
		c4_fail "${scenario}" "${label}" "$(printf '%s' "${output}" | grep -o '"code":"[^"]*"' | head -1)"
	fi
}

c5_cli --json db create --service mysql --name e2e17_app >/dev/null 2>&1 || true
c5_cli --json db create --service mysql --name e2e17_blog >/dev/null 2>&1 || true
c5_cli --json db create --service mysql --name e2e17_ops >/dev/null 2>&1 || true
c5_cli --json db user create --service mysql --name e2e17_user --grant e2e17_app:read_write >/dev/null 2>&1 || true
uid="$(c5_user_id mysql e2e17_user)"
for db in e2e17_blog e2e17_ops; do
	c5_cli --json db user grant --service mysql --user-id "${uid}" --database "${db}" --preset read_write >/dev/null 2>&1 || true
done
[ -n "${uid}" ] && c4_pass "${scenario}" "multi-link user prepared" || c4_fail "${scenario}" "multi-link user prepared"

c5_create_project e2e17-multi >/dev/null
c5_cli --json site add --name e2e17-site --path "${home}/projects/e2e17-multi" --kind static >/dev/null 2>&1 ||
	c5_fail "${scenario}" "multi-link site created"
c5_cli --json db link --site e2e17-site --service mysql --database e2e17_app --user-id "${uid}" --primary >/dev/null 2>&1 &&
	c4_pass "${scenario}" "primary database linked" || c4_fail "${scenario}" "primary database linked"
c5_cli --json db link --site e2e17-site --service mysql --database e2e17_blog --user-id "${uid}" >/dev/null 2>&1 &&
	c4_pass "${scenario}" "second database linked as non-primary" || c4_fail "${scenario}" "second database linked as non-primary"
c5_capture_site_env e2e17-site e2e17multi
multi="$(cat "${evidence_dir}/${scenario}/e2e17multi-env-summary.txt")"
c4_assert_contains "${scenario}" "multi-link primary DB" "${multi}" "DB_DATABASE=e2e17_app"
c4_assert_contains "${scenario}" "multi-link namespaced DB" "${multi}" "DB_E2E17_BLOG_DATABASE=e2e17_blog"
c4_assert_contains "${scenario}" "multi-link namespaced password" "${multi}" "DB_E2E17_BLOG_PASSWORD_PRESENT=true"
c4_assert_contains "${scenario}" "multi-link namespaced URL" "${multi}" "DB_E2E17_BLOG_URL_PRESENT=true"
c4_assert_contains "${scenario}" "multi-link primary URL" "${multi}" "DATABASE_URL_PRESENT=true"

c5_cli --json db link --site e2e17-site --service mysql --database e2e17_ops --user-id "${uid}" --primary >/dev/null 2>&1 &&
	c4_pass "${scenario}" "primary switched to the third database" || c4_fail "${scenario}" "primary switched to the third database"
c5_capture_site_env e2e17-site e2e17multi-switch
switched="$(cat "${evidence_dir}/${scenario}/e2e17multi-switch-env-summary.txt")"
c4_assert_contains "${scenario}" "switched primary DB" "${switched}" "DB_DATABASE=e2e17_ops"
c4_assert_contains "${scenario}" "previous primary is namespaced" "${switched}" "DB_E2E17_APP_DATABASE=e2e17_app"
c4_assert_contains "${scenario}" "second link stays namespaced" "${switched}" "DB_E2E17_BLOG_DATABASE=e2e17_blog"

blog_link="$(c5_link_id e2e17-site e2e17_blog)"
c5_cli --json db unlink --site e2e17-site --link-id "${blog_link}" --yes >/dev/null 2>&1 &&
	c4_pass "${scenario}" "one link unlinked" || c4_fail "${scenario}" "one link unlinked"
if c5_api_get "/api/v1/sites/e2e17-site/databases" | grep -q 'e2e17_blog'; then
	c4_fail "${scenario}" "unlinked database is absent from the site"
else
	c4_pass "${scenario}" "unlinked database is absent from the site"
fi
c5_cli --json db list --service mysql >"${private}/dbs-after-unlink.json"
c5_cli --json db user list --service mysql >"${private}/users-after-unlink.json"
if python3 - "${private}/dbs-after-unlink.json" "${private}/users-after-unlink.json" <<'PY'
import json, sys
dbs = {d.get("name") for d in json.load(open(sys.argv[1], encoding="utf-8")).get("databases", [])}
users = {u.get("username") for u in json.load(open(sys.argv[2], encoding="utf-8")).get("users", [])}
raise SystemExit(0 if "e2e17_blog" in dbs and "e2e17_user" in users else 1)
PY
then
	c4_pass "${scenario}" "unlink preserved the database and user"
else
	c4_fail "${scenario}" "unlink preserved the database and user"
fi

# Reserved env keys: link values must win at runtime.
c5_create_project e2e17-reserved >/dev/null
c5_cli --json site add --name e2e17-reserved --path "${home}/projects/e2e17-reserved" --kind static \
	--env 'DB_HOST=evil' --env 'DB_PASSWORD=leaked' --env 'DATABASE_URL=mysql://evil' \
	--database-service mysql --database e2e17_app --database-user "${uid}" >/dev/null 2>&1 &&
	c4_pass "${scenario}" "site with reserved env keys created" || c4_fail "${scenario}" "site with reserved env keys created"
c5_capture_site_env e2e17-reserved e2e17res
reserved="$(cat "${evidence_dir}/${scenario}/e2e17res-env-summary.txt")"
c4_assert_contains "${scenario}" "reserved DB_HOST overridden by the link" "${reserved}" "DB_HOST=127.0.0.1"
c4_assert_contains "${scenario}" "reserved DB_PASSWORD replaced by the managed secret" "${reserved}" "DB_PASSWORD_PRESENT=true"
if python3 - "${private}/e2e17res-env.json" <<'PY'
import json, sys
env = json.load(open(sys.argv[1], encoding="utf-8")).get("env", {})
url = env.get("DATABASE_URL", "")
raise SystemExit(0 if env.get("DB_HOST") == "127.0.0.1" and env.get("DB_PASSWORD") not in ("", "leaked") and "evil" not in url else 1)
PY
then
	c4_pass "${scenario}" "runtime env does not contain the user-set DB_PASSWORD or evil URL"
else
	c4_fail "${scenario}" "runtime env does not contain the user-set DB_PASSWORD or evil URL"
fi
c5_audit_copy "${private}/state-copy" >/dev/null 2>&1 || true
persisted="$(c5_audit_query "${private}/state-copy" "SELECT COALESCE(env_json,'') FROM sites WHERE name='e2e17-reserved';")"
if printf '%s' "${persisted}" | grep -q 'leaked'; then
	c4_pass "${scenario}" "user-set env remains in persisted site metadata"
	c4_note "${scenario}" "The persisted site env intentionally keeps the user-set password value; the runtime link value wins. The dashboard warning for this overlap is tracked separately."
else
	c4_fail "${scenario}" "user-set env remains in persisted site metadata"
fi

# Add Site validation.
expect_fail "duplicate database name rejected" c5_cli --json site add --name e2e17-dupdb --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new e2e17_app --database-user-new e2e17_dup1
site_exists e2e17-dupdb && c4_fail "${scenario}" "duplicate database did not create a site" || c4_pass "${scenario}" "duplicate database did not create a site"
expect_fail "duplicate user name rejected" c5_cli --json site add --name e2e17-dupuser --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new e2e17_dupdb --database-user-new e2e17_user
site_exists e2e17-dupuser && c4_fail "${scenario}" "duplicate user did not create a site" || c4_pass "${scenario}" "duplicate user did not create a site"
if c5_cli --json db list --service mysql | grep -q '"name":"e2e17_dupdb"'; then
	c4_fail "${scenario}" "duplicate user rejection did not leave an orphan database"
	c4_note "${scenario}" "FINDING: Add Site created e2e17_dupdb before rejecting the duplicate user."
else
	c4_pass "${scenario}" "duplicate user rejection did not leave an orphan database"
fi
expect_fail "protected database rejected" c5_cli --json site add --name e2e17-protected-db --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new mysql --database-user-new e2e17_p1
expect_fail "protected user rejected" c5_cli --json site add --name e2e17-protected-user --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new e2e17_p2 --database-user-new root
expect_fail "invalid database name rejected" c5_cli --json site add --name e2e17-bad-db --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new 'bad name' --database-user-new e2e17_b1
expect_fail "invalid username rejected" c5_cli --json site add --name e2e17-bad-user --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new e2e17_b2 --database-user-new 'bad-name'
expect_fail "SQL injection name rejected" c5_cli --json site add --name e2e17-inject --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new 'e2e;drop' --database-user-new e2e17_i1
expect_fail "missing service rejected" c5_cli --json site add --name e2e17-missing-svc --path "${home}/projects/e2e17-multi" --kind static --database-service nope --database-new e2e17_m1 --database-user-new e2e17_m2
expect_fail "inactive service rejected" c5_cli --json site add --name e2e17-inactive --path "${home}/projects/e2e17-multi" --kind static --database-service postgresql --database-new e2e17_i2 --database-user-new e2e17_i3
expect_fail "invalid grant rejected" c5_cli --json site add --name e2e17-grant --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database-new e2e17_g1 --database-user-new e2e17_g2 --database-grant nope
expect_fail "existing user with grant flag rejected" c5_cli --json site add --name e2e17-existing-grant --path "${home}/projects/e2e17-multi" --kind static --database-service mysql --database e2e17_app --database-user "${uid}" --database-grant read_only

# API parity: an existing user with a grant flag must be rejected, not silently ignored.
c5_api_session
api_grant_status="$(curl -sS -o "${private}/api-existing-grant.json" -w '%{http_code}' -b "${private}/cookies.txt" \
	-H "X-CSRF-Token: ${csrf}" -H 'Content-Type: application/json' -X POST \
	--data "{\"name\":\"e2e17-api-grant\",\"path\":\"${home}/projects/e2e17-multi\",\"kind\":\"static\",\"database\":{\"service_id\":\"mysql\",\"database\":\"e2e17_app\",\"user_id\":\"${uid}\",\"grant\":\"read_only\"}}" \
	"http://127.0.0.1:${ui_port}/api/v1/sites")"
if [ "${api_grant_status}" = "400" ] || [ "${api_grant_status}" = "409" ]; then
	c4_pass "${scenario}" "API rejects an existing user with a grant flag"
else
	c4_fail "${scenario}" "API rejects an existing user with a grant flag" "status=${api_grant_status}"
fi
if site_exists e2e17-api-grant; then
	c4_fail "${scenario}" "API rejection did not create a site"
	c5_cli --json site remove e2e17-api-grant --yes >/dev/null 2>&1 || true
else
	c4_pass "${scenario}" "API rejection did not create a site"
fi

# Concurrent link attempts: one winner, one conflict.
c5_create_project e2e17-race >/dev/null
c5_cli --json site add --name e2e17-race --path "${home}/projects/e2e17-race" --kind static >/dev/null 2>&1 || true
set +e
c5_cli --json db link --site e2e17-race --service mysql --database e2e17_app --user-id "${uid}" --primary >"${private}/race1.json" 2>&1 & p1=$!
c5_cli --json db link --site e2e17-race --service mysql --database e2e17_app --user-id "${uid}" >"${private}/race2.json" 2>&1 & p2=$!
wait "${p1}"; s1=$?
wait "${p2}"; s2=$?
set -e
successes=0
[ "${s1}" -eq 0 ] && successes=$((successes + 1))
[ "${s2}" -eq 0 ] && successes=$((successes + 1))
if [ "${successes}" -eq 1 ]; then c4_pass "${scenario}" "concurrent duplicate links have one winner"; else c4_fail "${scenario}" "concurrent duplicate links have one winner" "successes=${successes}"; fi
race_links="$(c5_api_get "/api/v1/sites/e2e17-race/databases" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("links", [])))')"
c4_assert_eq "${scenario}" "concurrent duplicate links leave one link" "${race_links}" "1"

# Concurrent link and site deletion: no orphan link.
c5_create_project e2e17-delrace >/dev/null
c5_cli --json site add --name e2e17-delrace --path "${home}/projects/e2e17-delrace" --kind static >/dev/null 2>&1 || true
delrace_id="$(c5_site_id e2e17-delrace)"
set +e
c5_cli --json db link --site e2e17-delrace --service mysql --database e2e17_app --user-id "${uid}" --primary >"${private}/delrace-link.json" 2>&1 & p1=$!
c5_cli --json site remove e2e17-delrace --yes >"${private}/delrace-remove.json" 2>&1 & p2=$!
wait "${p1}"; wait "${p2}"
set -e
if site_exists e2e17-delrace; then c4_fail "${scenario}" "concurrent site removal removed the site"; else c4_pass "${scenario}" "concurrent site removal removed the site"; fi
c5_audit_copy "${private}/state-copy-race" >/dev/null 2>&1 || true
orphan_links="$(c5_audit_query "${private}/state-copy-race" "SELECT COUNT(*) FROM database_links WHERE site_id='${delrace_id}';")"
c4_assert_eq "${scenario}" "concurrent site removal leaves no orphan link" "${orphan_links}" "0"

# Drop a linked database, then delete a linked user.
c5_cli --json db create --service mysql --name e2e17_drop >/dev/null 2>&1 || true
c5_cli --json db user create --service mysql --name e2e17_drop_user --grant e2e17_drop:read_write >/dev/null 2>&1 || true
drop_uid="$(c5_user_id mysql e2e17_drop_user)"
c5_create_project e2e17-drop >/dev/null
c5_cli --json site add --name e2e17-drop-site --path "${home}/projects/e2e17-drop" --kind static >/dev/null 2>&1 || true
c5_cli --json db link --site e2e17-drop-site --service mysql --database e2e17_drop --user-id "${drop_uid}" --primary >/dev/null 2>&1 || true
c5_cli --json site stop e2e17-drop-site >/dev/null 2>&1 || true
c5_cli --json db drop --service mysql --name e2e17_drop --yes >/dev/null 2>&1 &&
	c4_pass "${scenario}" "linked database dropped after stopping the site" || c4_fail "${scenario}" "linked database dropped after stopping the site"
c5_api_get "/api/v1/sites/e2e17-drop-site/databases" >"${private}/stale-link.json"
if python3 - "${private}/stale-link.json" <<'PY'
import json, sys
links = json.load(open(sys.argv[1], encoding="utf-8")).get("links", [])
link = links[0] if links else {}
raise SystemExit(0 if link.get("database") == "e2e17_drop" and link.get("available") is False and "no longer exists" in (link.get("error") or "") else 1)
PY
then
	c4_pass "${scenario}" "dropped database is flagged as a stale link"
else
	c4_fail "${scenario}" "dropped database is flagged as a stale link"
fi
if c4_mysql_root_query -e "SHOW DATABASES LIKE 'e2e17_drop';" | grep -q 'e2e17_drop'; then
	c4_fail "${scenario}" "stale link did not recreate the database"
else
	c4_pass "${scenario}" "stale link did not recreate the database"
fi
c4_expect_code "${scenario}" "linked user cannot be deleted" "DATABASE_IN_USE" \
	c5_cli --json --yes db user delete --service mysql --user-id "${drop_uid}"
if c5_cli --json db user list --service mysql | grep -q '"username":"e2e17_drop_user"'; then
	c4_pass "${scenario}" "linked user remains after the refused delete"
else
	c4_fail "${scenario}" "linked user remains after the refused delete"
fi

# Reveal, rotate, restart, and grant change.
c5_api_session
old_secret="$(cat "$(c5_secret_file e2e17multi)" 2>/dev/null || true)"
if [ -n "${old_secret}" ]; then
	c5_api_post "/api/v1/services/mysql/users/${uid}/reveal" '{}' >"${private}/e2e17-reveal.json"
	if python3 - "${private}/e2e17-reveal.json" "$(c5_secret_file e2e17multi)" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
want = open(sys.argv[2], encoding="utf-8").read().strip()
raise SystemExit(0 if value == want else 1)
PY
	then
		c4_pass "${scenario}" "reveal returns the linked managed secret"
	else
		c4_fail "${scenario}" "reveal returns the linked managed secret"
	fi
else
	c4_fail "${scenario}" "reveal returns the linked managed secret" "secret fixture missing"
fi
c5_cli --yes --json db user password --service mysql --user-id "${uid}" >"${private}/e2e17-rotate.json" 2>"${private}/e2e17-rotate.err" &&
	c4_pass "${scenario}" "password rotated" || c4_fail "${scenario}" "password rotated"
c5_cli --json db user password --service mysql --user-id "${uid}" --reveal >"${private}/e2e17-new.json" 2>/dev/null
new_secret="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8")).get("value", ""))' "${private}/e2e17-new.json")"
if [ -n "${old_secret}" ] && c4_mysql_user_query e2e17_user "${old_secret}" e2e17_app 'SELECT 1' >/dev/null 2>&1; then
	c4_fail "${scenario}" "old password is rejected after rotation"
else
	c4_pass "${scenario}" "old password is rejected after rotation"
fi
if c4_mysql_user_query e2e17_user "${new_secret}" e2e17_app 'SELECT 1' >/dev/null 2>&1; then
	c4_pass "${scenario}" "new password works after rotation"
else
	c4_fail "${scenario}" "new password works after rotation"
fi
c5_cli --json site restart e2e17-site >/dev/null 2>&1 && c4_pass "${scenario}" "site restarts after the password change" || c4_fail "${scenario}" "site restarts after the password change"
c5_terminal_probe e2e17-site "${evidence_dir}/${scenario}/terminal-after-rotate.txt"
c4_assert_contains "${scenario}" "terminal sees the rotated password" "$(cat "${evidence_dir}/${scenario}/terminal-after-rotate.txt")" "E2E_TERM DB_PASSWORD_PRESENT=true"
c5_cli --json db user grant --service mysql --user-id "${uid}" --database e2e17_app --preset read_only >/dev/null 2>&1 &&
	c4_pass "${scenario}" "grant changed to read_only" || c4_fail "${scenario}" "grant changed to read_only"
c5_cli --json site restart e2e17-site >/dev/null 2>&1 && c4_pass "${scenario}" "site restarts after the grant change" || c4_fail "${scenario}" "site restarts after the grant change"

# Restore/import gates while the linked site is running.
c5_cli --json db backup create --service mysql --database e2e17_app >"${private}/e2e17-backup-job.json" 2>&1 || true
for _ in $(seq 1 60); do
	c5_cli --json db backup list --service mysql >"${private}/e2e17-backups.json" 2>/dev/null || true
	bkp="$(python3 -c 'import json,sys; rows=json.load(open(sys.argv[1], encoding="utf-8")).get("backups", []); print(next((b["id"] for b in rows if b.get("database")=="e2e17_app"), ""))' "${private}/e2e17-backups.json")"
	[ -n "${bkp}" ] && break
	sleep 1
done
if [ -n "${bkp:-}" ]; then c4_pass "${scenario}" "backup available for the restore gate"; else c4_fail "${scenario}" "backup available for the restore gate"; fi
c5_cli --json site start e2e17-site >/dev/null 2>&1 || true
expect_linked_site_gate "overwrite restore is blocked while the site runs" \
	c5_cli --json --yes db backup restore --service mysql --backup-id "${bkp:-missing}" --target e2e17_app --overwrite
expect_linked_site_gate "replace import is blocked while the site runs" \
	c5_cli --json --yes db import --service mysql --database e2e17_app --file "${repo}/test/e2e/database-management/fixtures/sql/seed.sql" --replace
c5_api_get "/api/v1/sites/e2e17-site" >"${private}/site-running.json"
if python3 - "${private}/site-running.json" <<'PY'
import json, sys
site = json.load(open(sys.argv[1], encoding="utf-8"))
raise SystemExit(0 if site.get("status") == "running" else 1)
PY
then
	c4_pass "${scenario}" "site is still running after the blocked destructive operations"
else
	c4_fail "${scenario}" "site is still running after the blocked destructive operations"
fi

c5_audit_copy "${private}/state-copy" >/dev/null 2>&1 || true
c5_secret_scan "${scenario}" && c4_pass "${scenario}" "secret scan clean over evidence, logs, and state copy" || c4_fail "${scenario}" "secret scan clean over evidence, logs, and state copy"

c5_record "${scenario}" "$(c4_scenario_status "${scenario}")" P1 \
	"Multi-link env namespacing/primary switch/unlink; reserved keys; Add Site validation; concurrency; stale links; reveal/rotate/restart; restore/import gates" \
	"UI reveal/copy/route-clearing and the reserved-key warning are covered by the live Playwright spec; actual backup/restore/import content is cycle 6"
c4_scenario_summary "${scenario}"
