#!/usr/bin/env bash
# Cycle 5 / E2E-03: Add Site with a new database and new user, dynamic env,
# secret-free persistence, Site Detail reveal audit, terminal and PHP exec.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

scenario="E2E-03"
c5_guard
c4_check_init "${scenario}"
if [ "${E2E_CYCLE5_SHARED:-0}" != "1" ]; then
	trap c5_cleanup EXIT
	c5_init
fi
c5_ensure_base
c5_install_php

c5_create_project e2e03-static >/dev/null
site_json="${evidence_dir}/${scenario}/site-add.json"
if c5_cli --json site add --name e2e03-site --path "${home}/projects/e2e03-static" --kind static \
	--database-service mysql --database-new e2e03_app --database-user-new e2e03_user \
	--database-grant read_write >"${site_json}" 2>"${site_json}.err"; then
	c4_pass "${scenario}" "Add Site created the site, database, user, and link"
else
	c4_fail "${scenario}" "Add Site created the site, database, user, and link" "$(head -1 "${site_json}.err")"
fi
if python3 - "${site_json}" <<'PY'
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
ok = d.get("name") == "e2e03-site" and d.get("kind") == "static"
raise SystemExit(0 if ok else 1)
PY
then
	c4_pass "${scenario}" "site add result carries the site identity"
else
	c4_fail "${scenario}" "site add result carries the site identity"
fi

uid="$(c5_user_id mysql e2e03_user)"
[ -n "${uid}" ] && c4_pass "${scenario}" "new managed user id resolved" || c4_fail "${scenario}" "new managed user id resolved"
link_id="$(c5_link_id e2e03-site e2e03_app)"
[ -n "${link_id}" ] && c4_pass "${scenario}" "site link id resolved" || c4_fail "${scenario}" "site link id resolved"

links_json="${private}/e2e03-links.json"
c5_api_get "/api/v1/sites/e2e03-site/databases" >"${links_json}"
if python3 - "${links_json}" "${evidence_dir}/${scenario}/link-summary.txt" <<'PY'
import json, sys
links = json.load(open(sys.argv[1], encoding="utf-8")).get("links", [])
link = links[0] if links else {}
conn = link.get("connection", {})
summary = [
    f"database={link.get('database')}",
    f"username={link.get('username')}",
    f"host={link.get('host')}",
    f"port={conn.get('port')}",
    f"password_managed={link.get('password_managed')}",
    f"primary={link.get('primary')}",
    f"available={link.get('available')}",
    f"url={conn.get('url')}",
]
open(sys.argv[2], "w", encoding="utf-8").write("\n".join(summary) + "\n")
ok = (link.get("database") == "e2e03_app" and link.get("username") == "e2e03_user"
      and link.get("host") == "%" and conn.get("port") == 3306
      and link.get("password_managed") is True and link.get("primary") is True
      and link.get("available") is True and ":" not in (conn.get("url") or "").split("//", 1)[-1].split("@", 1)[0])
raise SystemExit(0 if ok else 1)
PY
then
	c4_pass "${scenario}" "review fields database/user/grant/host/port and safe URL"
else
	c4_fail "${scenario}" "review fields database/user/grant/host/port and safe URL"
fi

users_json="${private}/e2e03-users.json"
c5_cli --json db user list --service mysql >"${users_json}"
if python3 - "${users_json}" <<'PY'
import json, sys
users = json.load(open(sys.argv[1], encoding="utf-8")).get("users", [])
user = next((u for u in users if u.get("username") == "e2e03_user"), None)
grant = next((g for g in (user or {}).get("grants", []) if g.get("database") == "e2e03_app"), None)
ok = user is not None and user.get("password_managed") is True and user.get("managed") is True and grant is not None and grant.get("preset") == "read_write"
raise SystemExit(0 if ok else 1)
PY
then
	c4_pass "${scenario}" "managed user has the requested grant"
else
	c4_fail "${scenario}" "managed user has the requested grant"
fi

c5_capture_site_env e2e03-site e2e03
summary="$(cat "${evidence_dir}/${scenario}/e2e03-env-summary.txt")"
c4_assert_contains "${scenario}" "env DB_CONNECTION" "${summary}" "DB_CONNECTION=mysql"
c4_assert_contains "${scenario}" "env DB_HOST" "${summary}" "DB_HOST=127.0.0.1"
c4_assert_contains "${scenario}" "env DB_PORT" "${summary}" "DB_PORT=3306"
c4_assert_contains "${scenario}" "env DB_DATABASE" "${summary}" "DB_DATABASE=e2e03_app"
c4_assert_contains "${scenario}" "env DB_USERNAME" "${summary}" "DB_USERNAME=e2e03_user"
c4_assert_contains "${scenario}" "env DB_PASSWORD present for managed user" "${summary}" "DB_PASSWORD_PRESENT=true"
c4_assert_contains "${scenario}" "env DATABASE_URL present for managed user" "${summary}" "DATABASE_URL_PRESENT=true"

secret="$(c5_secret_file e2e03)"
c5_assert_file_lacks_secret "${scenario}" "Site Detail link JSON has no password" "${links_json}" "${secret}"

c5_audit_copy "${private}/state-copy" && c4_pass "${scenario}" "state.db copied for audit" || c4_fail "${scenario}" "state.db copied for audit"
c5_assert_file_lacks_secret "${scenario}" "state.db has no generated password" "${private}/state-copy/state.db" "${secret}"
site_env_json="${private}/site-env.json"
c5_audit_query "${private}/state-copy" "SELECT COALESCE(env_json,'') FROM sites WHERE name='e2e03-site';" >"${site_env_json}"
c5_assert_file_lacks_secret "${scenario}" "persisted site env has no generated password" "${site_env_json}" "${secret}"

# PHP site with the same database/user, used for php-fpm.conf and PHP exec.
c5_create_project e2e03-php php >/dev/null
if c5_cli --json site add --name e2e03-php --path "${home}/projects/e2e03-php" --kind php --php 8.4 \
	--database-service mysql --database e2e03_app --database-user "${uid}" \
	>"${evidence_dir}/${scenario}/php-site-add.json" 2>"${evidence_dir}/${scenario}/php-site-add.err"; then
	c4_pass "${scenario}" "PHP site linked to the same database/user"
else
	c4_fail "${scenario}" "PHP site linked to the same database/user" "$(head -1 "${evidence_dir}/${scenario}/php-site-add.err")"
fi
c5_cli --json site start e2e03-php >"${evidence_dir}/${scenario}/php-site-start.json" 2>"${evidence_dir}/${scenario}/php-site-start.err" &&
	c4_pass "${scenario}" "PHP site started" || c4_fail "${scenario}" "PHP site started"
php_site="$(c5_site_id e2e03-php)"
fpm="$(find "${devstack_home}/sites/${php_site}" -name php-fpm.conf -print -quit 2>/dev/null || true)"
if [ -n "${fpm}" ]; then
	c4_pass "${scenario}" "php-fpm.conf generated"
	c5_assert_file_lacks_secret "${scenario}" "php-fpm.conf has no generated password" "${fpm}" "${secret}"
	if grep -q 'DB_PASSWORD' "${fpm}"; then
		c4_fail "${scenario}" "php-fpm.conf has no DB_PASSWORD directive"
	else
		c4_pass "${scenario}" "php-fpm.conf has no DB_PASSWORD directive"
	fi
else
	c4_fail "${scenario}" "php-fpm.conf generated"
fi

c5_php_exec e2e03-php "${evidence_dir}/${scenario}/php-exec.txt"
php_exec="$(cat "${evidence_dir}/${scenario}/php-exec.txt")"
c4_assert_contains "${scenario}" "PHP exec DB_CONNECTION" "${php_exec}" "E2E_EXEC DB_CONNECTION=mysql"
c4_assert_contains "${scenario}" "PHP exec DB_DATABASE" "${php_exec}" "E2E_EXEC DB_DATABASE=e2e03_app"
c4_assert_contains "${scenario}" "PHP exec DB_USERNAME" "${php_exec}" "E2E_EXEC DB_USERNAME=e2e03_user"
c4_assert_contains "${scenario}" "PHP exec DB_PASSWORD present" "${php_exec}" "E2E_EXEC DB_PASSWORD_PRESENT=true"

c5_terminal_probe e2e03-site "${evidence_dir}/${scenario}/terminal.txt"
terminal="$(cat "${evidence_dir}/${scenario}/terminal.txt")"
c4_assert_contains "${scenario}" "terminal DB_CONNECTION" "${terminal}" "E2E_TERM DB_CONNECTION=mysql"
c4_assert_contains "${scenario}" "terminal DB_HOST" "${terminal}" "E2E_TERM DB_HOST=127.0.0.1"
c4_assert_contains "${scenario}" "terminal DB_PORT" "${terminal}" "E2E_TERM DB_PORT=3306"
c4_assert_contains "${scenario}" "terminal DB_DATABASE" "${terminal}" "E2E_TERM DB_DATABASE=e2e03_app"
c4_assert_contains "${scenario}" "terminal DB_USERNAME" "${terminal}" "E2E_TERM DB_USERNAME=e2e03_user"
c4_assert_contains "${scenario}" "terminal DB_PASSWORD present" "${terminal}" "E2E_TERM DB_PASSWORD_PRESENT=true"
c4_assert_contains "${scenario}" "terminal DATABASE_URL present" "${terminal}" "E2E_TERM DATABASE_URL_PRESENT=true"

# Reveal through the CLI twice and the API once; the API is the Site Detail path.
c5_cli --json db user password --service mysql --user-id "${uid}" --reveal \
	>"${private}/reveal1.json" 2>"${private}/reveal1.err" && c4_pass "${scenario}" "CLI reveal once" || c4_fail "${scenario}" "CLI reveal once"
c5_cli --json db user password --service mysql --user-id "${uid}" --reveal \
	>"${private}/reveal2.json" 2>"${private}/reveal2.err" && c4_pass "${scenario}" "CLI reveal twice" || c4_fail "${scenario}" "CLI reveal twice"
grep -qi 'WARNING' "${private}/reveal1.err" && grep -qi 'WARNING' "${private}/reveal2.err" &&
	c4_pass "${scenario}" "CLI reveal warning" || c4_fail "${scenario}" "CLI reveal warning"
if python3 - "${private}/reveal1.json" "${private}/reveal2.json" "${secret}" <<'PY'
import json, sys
a = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
b = json.load(open(sys.argv[2], encoding="utf-8")).get("value", "")
want = open(sys.argv[3], encoding="utf-8").read().strip()
raise SystemExit(0 if a == b == want else 1)
PY
then
	c4_pass "${scenario}" "CLI reveals return the managed secret twice"
else
	c4_fail "${scenario}" "CLI reveals return the managed secret twice"
fi

c5_api_session
c5_api_post "/api/v1/services/mysql/users/${uid}/reveal" '{}' >"${private}/api-reveal.json"
if python3 - "${private}/api-reveal.json" "${secret}" <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8")).get("value", "")
want = open(sys.argv[2], encoding="utf-8").read().strip()
raise SystemExit(0 if value == want else 1)
PY
then
	c4_pass "${scenario}" "Site Detail API reveal returns the managed secret"
else
	c4_fail "${scenario}" "Site Detail API reveal returns the managed secret"
fi

c5_audit_copy "${private}/state-copy" >/dev/null 2>&1 || true
reveal_count="$(c5_audit_query "${private}/state-copy" "SELECT COUNT(*) FROM audit_events WHERE action='database.user.password_revealed' AND result='ok';")"
if [ "${reveal_count}" -ge 3 ]; then c4_pass "${scenario}" "reveal attempts are audited"; else c4_fail "${scenario}" "reveal attempts are audited" "count=${reveal_count}"; fi

c5_secret_scan "${scenario}" && c4_pass "${scenario}" "secret scan clean over evidence, logs, and state copy" || c4_fail "${scenario}" "secret scan clean over evidence, logs, and state copy"

c5_record "${scenario}" "$(c4_scenario_status "${scenario}")" P0 \
	"Add Site new DB/user; dynamic env; secret-free persistence; reveal audit; terminal and PHP exec" \
	"UI Add Site review and Site Detail reveal/copy are covered by the live Playwright spec; no residual product risk"
c4_scenario_summary "${scenario}"
