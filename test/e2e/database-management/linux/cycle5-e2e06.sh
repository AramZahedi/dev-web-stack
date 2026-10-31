#!/usr/bin/env bash
# Cycle 5 / E2E-06: a MySQL-linked site must not start while MariaDB is active
# on the exclusive 3306 group. MariaDB is started without published ports
# because this host's WSL relay already reserves 3306 for the real install.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

scenario="E2E-06"
c5_guard
c4_check_init "${scenario}"
if [ "${E2E_CYCLE5_SHARED:-0}" != "1" ]; then
	trap c5_cleanup EXIT
	c5_init
fi
c5_ensure_base

c5_cli --json db create --service mysql --name e2e06_app >/dev/null 2>&1 || true
c5_cli --json db user create --service mysql --name e2e06_user --grant e2e06_app:read_write >/dev/null 2>&1 || true
uid="$(c5_user_id mysql e2e06_user)"
c5_create_project e2e06-static >/dev/null
c5_cli --json site add --name e2e06-site --path "${home}/projects/e2e06-static" --kind static \
	>"${evidence_dir}/${scenario}/site-add.json" 2>"${evidence_dir}/${scenario}/site-add.err" &&
	c4_pass "${scenario}" "MySQL-linked site created" || c4_fail "${scenario}" "MySQL-linked site created"
c5_cli --json db link --site e2e06-site --service mysql --database e2e06_app --user-id "${uid}" --primary \
	>"${evidence_dir}/${scenario}/site-link.json" 2>"${evidence_dir}/${scenario}/site-link.err" &&
	c4_pass "${scenario}" "site linked to MySQL" || c4_fail "${scenario}" "site linked to MySQL"
c5_cli --json site start e2e06-site >/dev/null 2>&1 && c4_pass "${scenario}" "site starts with MySQL active" || c4_fail "${scenario}" "site starts with MySQL active"
c5_cli --json site stop e2e06-site >/dev/null 2>&1 && c4_pass "${scenario}" "site stopped before the engine switch" || c4_fail "${scenario}" "site stopped before the engine switch"

c5_cli --json service deactivate mysql >"${evidence_dir}/${scenario}/deactivate-mysql.json" 2>"${evidence_dir}/${scenario}/deactivate-mysql.err" &&
	c4_pass "${scenario}" "MySQL deactivated" || c4_fail "${scenario}" "MySQL deactivated"
if c5_service_active mysql; then c4_fail "${scenario}" "MySQL inactive after deactivation"; else c4_pass "${scenario}" "MySQL inactive after deactivation"; fi

# Product activation is expected to fail on the occupied fixed 3306 port. This
# is the BLOCKED sub-step; the guard itself is verified with a no-port MariaDB.
set +e
c5_cli --json service activate mariadb >"${evidence_dir}/${scenario}/activate-mariadb-product.json" 2>"${evidence_dir}/${scenario}/activate-mariadb-product.err"
product_status=$?
set -e
if [ "${product_status}" -ne 0 ] && grep -qE '3306|already in use' "${evidence_dir}/${scenario}/activate-mariadb-product.err" "${evidence_dir}/${scenario}/activate-mariadb-product.json"; then
	c4_note "${scenario}" "BLOCKED: product MariaDB activation on 3306 refused because the real install's relay owns the port; a no-port MariaDB container verifies the guard instead."
	c4_pass "${scenario}" "product MariaDB activation on 3306 fails closed (BLOCKED sub-step)"
else
	c4_fail "${scenario}" "product MariaDB activation on 3306 fails closed (BLOCKED sub-step)"
fi

c5_cli --json service install mariadb >"${evidence_dir}/${scenario}/install-mariadb.json" 2>"${evidence_dir}/${scenario}/install-mariadb.err" &&
	c4_pass "${scenario}" "MariaDB installed" || c4_fail "${scenario}" "MariaDB installed"
c5_strip_service_ports mariadb
c5_compose up -d --no-deps mariadb >"${evidence_dir}/${scenario}/start-mariadb.log" 2>&1
c5_wait_container_healthy devstack-mariadb 180
if c5_service_active mariadb; then c4_pass "${scenario}" "no-port MariaDB is active"; else c4_fail "${scenario}" "no-port MariaDB is active"; fi

set +e
start_output="$(c5_cli --json site start e2e06-site 2>&1)"
start_status=$?
set -e
printf '%s\n' "${start_output}" >"${evidence_dir}/${scenario}/wrong-engine-start.json"
if [ "${start_status}" -ne 0 ] && printf '%s' "${start_output}" | grep -q 'DATABASE_CONFLICT' && printf '%s' "${start_output}" | grep -qiE 'mariadb is active|exclusive port group'; then
	c4_pass "${scenario}" "site start blocked with the MySQL/MariaDB conflict"
else
	c4_fail "${scenario}" "site start blocked with the MySQL/MariaDB conflict"
fi
site_json="${private}/site-after-block.json"
c5_api_get "/api/v1/sites/e2e06-site" >"${site_json}"
if python3 - "${site_json}" <<'PY'
import json, sys
site = json.load(open(sys.argv[1], encoding="utf-8"))
raise SystemExit(0 if site.get("status") != "running" and site.get("desired_state") != "running" else 1)
PY
then
	c4_pass "${scenario}" "blocked start left the site stopped"
else
	c4_fail "${scenario}" "blocked start left the site stopped"
fi
if pgrep -af 'devstack serve-static' 2>/dev/null | grep -q 'e2e06-site'; then
	c4_fail "${scenario}" "blocked start did not spawn a site process"
else
	c4_pass "${scenario}" "blocked start did not spawn a site process"
fi

c5_api_session
api_status="$(curl -sS -o "${private}/api-start.json" -w '%{http_code}' -b "${private}/cookies.txt" \
	-H "X-CSRF-Token: ${csrf}" -H 'Content-Type: application/json' -X POST --data '{}' \
	"http://127.0.0.1:${ui_port}/api/v1/sites/e2e06-site/start")"
if [ "${api_status}" = "409" ] && grep -q 'DATABASE_CONFLICT' "${private}/api-start.json"; then
	c4_pass "${scenario}" "API start blocked with DATABASE_CONFLICT"
else
	c4_fail "${scenario}" "API start blocked with DATABASE_CONFLICT" "status=${api_status}"
fi
c5_api_get "/api/v1/services/mysql/database-impact" >"${private}/mysql-impact.json"
if python3 - "${private}/mysql-impact.json" <<'PY'
import json, sys
impact = json.load(open(sys.argv[1], encoding="utf-8"))
names = {s.get("name") for s in impact.get("linked_sites", [])}
raise SystemExit(0 if "e2e06-site" in names else 1)
PY
then
	c4_pass "${scenario}" "MySQL linked-site impact warning includes the site"
else
	c4_fail "${scenario}" "MySQL linked-site impact warning includes the site"
fi

c5_cli --json service deactivate mariadb >"${evidence_dir}/${scenario}/deactivate-mariadb.json" 2>"${evidence_dir}/${scenario}/deactivate-mariadb.err" ||
	c5_compose stop mariadb >/dev/null 2>&1 || true
if c5_service_active mariadb; then c4_fail "${scenario}" "MariaDB inactive after deactivation"; else c4_pass "${scenario}" "MariaDB inactive after deactivation"; fi
# Installing MariaDB re-rendered compose.yaml and restored MySQL's fixed 3306
# publication; strip it again before the manual no-port start.
c5_strip_service_ports mysql
c5_compose up -d --no-deps mysql >"${evidence_dir}/${scenario}/start-mysql-again.log" 2>&1
c5_wait_container_healthy devstack-mysql 180
if c5_service_active mysql; then c4_pass "${scenario}" "MySQL active again"; else c4_fail "${scenario}" "MySQL active again"; fi
c5_cli --json site start e2e06-site >"${evidence_dir}/${scenario}/correct-engine-start.json" 2>"${evidence_dir}/${scenario}/correct-engine-start.err" &&
	c4_pass "${scenario}" "site starts after switching back to MySQL" || c4_fail "${scenario}" "site starts after switching back to MySQL"
c5_api_get "/api/v1/sites/e2e06-site" >"${private}/site-after-start.json"
http_port="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8")).get("http_port", 0))' "${private}/site-after-start.json")"
if [ "${http_port}" -gt 0 ] && curl -sS --max-time 10 -o "${private}/site-body.html" -w '%{http_code}' "http://127.0.0.1:${http_port}/" | grep -q '^200$' && grep -q 'e2e06' "${private}/site-body.html"; then
	c4_pass "${scenario}" "site serves after the correct-engine start"
else
	c4_fail "${scenario}" "site serves after the correct-engine start"
fi

c5_audit_copy "${private}/state-copy" >/dev/null 2>&1 || true
c5_secret_scan "${scenario}" && c4_pass "${scenario}" "secret scan clean over evidence, logs, and state copy" || c4_fail "${scenario}" "secret scan clean over evidence, logs, and state copy"

c5_record "${scenario}" "$(c4_scenario_status "${scenario}")" P0 \
	"MySQL-linked site blocked while no-port MariaDB is active; API 409; impact warning; recovery on MySQL" \
	"BLOCKED sub-step: product MariaDB activation on fixed 3306 is refused because the real install owns the port; guard verified with a no-port MariaDB container"
c4_scenario_summary "${scenario}"
