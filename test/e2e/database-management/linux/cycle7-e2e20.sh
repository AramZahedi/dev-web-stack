#!/usr/bin/env bash
# E2E-20 - Linux phpMyAdmin login matrix on the disposable helper/daemon.
# MySQL/MariaDB run without published host ports and phpMyAdmin uses a unique
# high loopback port because the real install owns 3306/8082.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle7-env.sh
source "${here}/cycle7-env.sh"

scenario="E2E-20"
pma_container="devstack-phpmyadmin"
pma_config="${devstack_home}/services/phpmyadmin/config/config.user.inc.php"
pma_pass_unix="/mnt/c/Users/Aram/AppData/Local/Temp/opencode/devstack-e2e-${run_key}-pma.pass"
pma_pass_win="C:\\Users\\Aram\\AppData\\Local\\Temp\\opencode\\devstack-e2e-${run_key}-pma.pass"

c7_pma_cleanup() {
	rm -f "${pma_pass_unix}"
	c7_cleanup
}

c7_guard
c4_check_init "${scenario}"
trap c7_pma_cleanup EXIT
c7_init

c7_pma_compose() {
	docker compose -p "$(c5_compose_project)" -f "${devstack_home}/services/compose.yaml" "$@"
}

c7_pma_restart() {
	c7_pma_compose restart phpmyadmin >"${logs_dir}/pma-restart.log" 2>&1
	c5_wait_container_healthy "${pma_container}" 120
}

c7_pma_http() {
	local out="$1"
	curl -sS --max-time 20 -o "${out}" -w '%{http_code}' "http://127.0.0.1:${pma_host_port}/"
}

c7_pma_env() {
	docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${pma_container}" 2>/dev/null || true
}

c7_pma_health() {
	docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${pma_container}" 2>/dev/null || true
}

c7_pma_ui_request() {
	local phase="$1" expect="$2" request="$home/ui-request.json" done="$home/ui-done"
	python3 - "${request}" "http://127.0.0.1:${pma_host_port}/" "${pma_pass_win}" "${phase}" "${expect}" <<'PY'
import json, sys
json.dump({
    "url": sys.argv[2],
    "pass_file": sys.argv[3],
    "phase": sys.argv[4],
    "expect": sys.argv[5],
}, open(sys.argv[1], "w", encoding="utf-8"), indent=2)
PY
	rm -f "${done}"
	touch "$home/ui-request"
	local waited=0
	while [ ! -f "${done}" ]; do
		waited=$((waited + 1))
		if [ "${waited}" -ge "${E2E_UI_TIMEOUT:-900}" ]; then
			c4_fail "${scenario}" "Playwright phase ${phase} completed" "timed out"
			rm -f "$home/ui-request"
			return 0
		fi
		sleep 1
	done
	rm -f "$home/ui-request" "${done}"
	local result="${evidence_dir}/${scenario}/pma-${phase}.json"
	if [ ! -f "${result}" ]; then
		c4_fail "${scenario}" "Playwright phase ${phase} wrote a result" "missing ${result}"
		return 0
	fi
	local checks="${private}/pma-${phase}-checks.txt"
	python3 - "${result}" "${phase}" "${expect}" >"${checks}" 2>&1 <<'PY' || true
import json, sys
path, phase, expect = sys.argv[1], sys.argv[2], sys.argv[3]
data = json.load(open(path, encoding="utf-8"))
def emit(ok, label, detail=""):
    print(("PASS " if ok else "FAIL ") + label + ((" (" + detail + ")") if detail and not ok else ""))
if data.get("error"):
    emit(False, f"Playwright phase {phase} did not crash", str(data["error"]))
if expect in {"login", "mariadb"}:
    emit(bool(data.get("login")), f"Playwright phase {phase} logged in")
    emit(bool(data.get("dashboard")), f"Playwright phase {phase} loaded the dashboard")
    emit(bool(data.get("logout")), f"Playwright phase {phase} logged out")
    emit(bool(data.get("wrongPasswordRejected")), f"Playwright phase {phase} rejected a wrong password")
    emit(bool(data.get("serverVersion")), f"Playwright phase {phase} reported the server version")
    emit(not data.get("connectionError"), f"Playwright phase {phase} had no connection error")
    emit(not data.get("consoleErrors"), f"Playwright phase {phase} had no console errors", str(data.get("consoleErrors")))
    if expect == "mariadb":
        emit("mariadb" in str(data.get("serverVersion", "")).lower(), f"Playwright phase {phase} reports MariaDB")
else:
    emit(bool(data.get("connectionError")), f"Playwright phase {phase} showed a connection error")
    emit(int(data.get("elapsedMs", 999999)) < 30000, f"Playwright phase {phase} failed fast")
    emit(int(data.get("httpStatus", 500)) < 500, f"Playwright phase {phase} did not return HTTP 5xx")
PY
	while IFS= read -r line; do
		case "${line}" in
		PASS\ *) c4_pass "${scenario}" "${line#PASS }" ;;
		FAIL\ *) c4_fail "${scenario}" "${line#FAIL }" ;;
		esac
	done <"${checks}"
	if [ ! -s "${checks}" ]; then
		c4_fail "${scenario}" "Playwright phase ${phase} produced result checks" "empty check output"
	fi
}

c7_pma_assert_config() {
	local label="$1"
	python3 - "${pma_config}" <<'PY' >"${evidence_dir}/${scenario}/pma-config-summary.txt"
import re, sys
text = open(sys.argv[1], encoding="utf-8").read()
for key in ("host", "port", "auth_type"):
    m = re.search(r"\$cfg\['Servers'\]\[1\]\['" + key + r"'\]\s*=\s*'([^']*)'", text)
    print(f"{key}={m.group(1) if m else ''}")
print("credential_keys=" + (",".join(sorted(set(re.findall(r"\['(user|password)'\]", text)))) or "none"))
PY
	c7_assert_file_contains "${scenario}" "${label}: host is db" "${pma_config}" "\$cfg['Servers'][1]['host'] = 'db';"
	c7_assert_file_contains "${scenario}" "${label}: port is 3306" "${pma_config}" "\$cfg['Servers'][1]['port'] = '3306';"
	c7_assert_file_contains "${scenario}" "${label}: auth_type is cookie" "${pma_config}" "\$cfg['Servers'][1]['auth_type'] = 'cookie';"
	if grep -Eq "\\['(user|password)'\\]" "${pma_config}"; then
		c4_fail "${scenario}" "${label}: no credential keys in config" "credential key found"
	else
		c4_pass "${scenario}" "${label}: no credential keys in config"
	fi
}

c7_section "E2E-20 fresh MySQL + phpMyAdmin install"
c5_cli --json service install mysql >"${logs_dir}/install-mysql.json" 2>"${logs_dir}/install-mysql.err" ||
	c7_die "mysql install failed: $(head -1 "${logs_dir}/install-mysql.err")"
c5_cli --json service install mariadb >"${logs_dir}/install-mariadb.json" 2>"${logs_dir}/install-mariadb.err" ||
	c7_die "mariadb install failed: $(head -1 "${logs_dir}/install-mariadb.err")"
c5_cli --json service install phpmyadmin >"${logs_dir}/install-phpmyadmin.json" 2>"${logs_dir}/install-phpmyadmin.err" ||
	c7_die "phpmyadmin install failed: $(head -1 "${logs_dir}/install-phpmyadmin.err")"
c5_strip_service_ports mysql
c5_strip_service_ports mariadb
c4_patch_phpmyadmin_port
# Use the MySQL devstack password for MariaDB so the same login works after the
# engine switch, as required by the login matrix.
install -m 600 "${devstack_home}/secrets/mysql/devstack_password" "${devstack_home}/secrets/mariadb/devstack_password"
c7_pma_compose up -d --no-deps mysql >"${logs_dir}/start-mysql.log" 2>&1
c5_wait_container_healthy devstack-mysql 180
c7_pma_compose up -d --no-deps phpmyadmin >"${logs_dir}/start-phpmyadmin.log" 2>&1
c5_wait_container_healthy "${pma_container}" 120

c4_assert_eq "${scenario}" "fresh phpMyAdmin container is healthy" "$(c7_pma_health)" "healthy"
c7_pma_assert_config "fresh install"
c7_pma_env >"${evidence_dir}/${scenario}/pma-env.txt"
c7_assert_file_contains "${scenario}" "PMA_HOST is set" "${evidence_dir}/${scenario}/pma-env.txt" "PMA_HOST=host.docker.internal"
c7_assert_file_contains "${scenario}" "PMA_PORT is 3306" "${evidence_dir}/${scenario}/pma-env.txt" "PMA_PORT=3306"
c7_assert_file_contains "${scenario}" "PMA_ARBITRARY is 0" "${evidence_dir}/${scenario}/pma-env.txt" "PMA_ARBITRARY=0"
if c7_pma_http "${private}/pma-login-page.html" | grep -q '^200$'; then
	c4_pass "${scenario}" "phpMyAdmin login page returns HTTP 200"
else
	c4_fail "${scenario}" "phpMyAdmin login page returns HTTP 200" "unexpected status"
fi
c7_assert_file_contains "${scenario}" "login page has the username field" "${private}/pma-login-page.html" 'name="pma_username"'
c7_assert_file_contains "${scenario}" "login page has the password field" "${private}/pma-login-page.html" 'name="pma_password"'
install -m 600 "${devstack_home}/secrets/mysql/devstack_password" "${pma_pass_unix}"
install -m 600 "${devstack_home}/secrets/mysql/devstack_password" "${secret_store}/mysql_devstack.password"

c7_pma_ui_request fresh login
c7_pma_restart
c4_assert_eq "${scenario}" "phpMyAdmin is healthy after restart" "$(c7_pma_health)" "healthy"
c7_pma_ui_request restart login

c7_section "E2E-20 MySQL stopped"
c7_pma_compose stop mysql >"${logs_dir}/stop-mysql.log" 2>&1
if c7_pma_http "${private}/pma-mysql-down.html" | grep -q '^200$'; then
	c4_pass "${scenario}" "phpMyAdmin login page stays available with MySQL stopped"
else
	c4_fail "${scenario}" "phpMyAdmin login page stays available with MySQL stopped" "login page failed"
fi
c7_pma_ui_request mysql-down down
c7_pma_compose start mysql >"${logs_dir}/start-mysql-again.log" 2>&1
c5_wait_container_healthy devstack-mysql 180

c7_section "E2E-20 MariaDB switch"
c7_pma_compose stop mysql >"${logs_dir}/stop-mysql-for-mariadb.log" 2>&1
c7_pma_compose up -d --no-deps mariadb >"${logs_dir}/start-mariadb.log" 2>&1
c5_wait_container_healthy devstack-mariadb 180
c7_pma_restart
c7_pma_ui_request mariadb mariadb
c7_pma_compose stop mariadb >"${logs_dir}/stop-mariadb.log" 2>&1
c7_pma_compose start mysql >"${logs_dir}/restore-mysql.log" 2>&1
c5_wait_container_healthy devstack-mysql 180
c7_pma_restart
c7_pma_ui_request after-mariadb login

c7_section "E2E-20 legacy config repair"
cat >"${pma_config}" <<'PHP'
<?php
// Old agent template: host.docker.internal does not reach the no-port MySQL
// container on this Linux helper.
$cfg['UploadDir'] = '';
$cfg['SaveDir'] = '';
$cfg['Servers'][1]['host'] = 'host.docker.internal';
$cfg['Servers'][1]['port'] = '3306';
$cfg['Servers'][1]['auth_type'] = 'cookie';
PHP
chmod 644 "${pma_config}"
c7_pma_restart
c7_pma_ui_request broken down
cat >"${private}/pma-fixed.php" <<'PHP'
<?php
// Generated by devstack as a safe starting point.
// This file is user-owned after the first install; devstack never overwrites it.
// "db" is the fixed Compose network alias for the active MySQL-family service.
$cfg['UploadDir'] = '';
$cfg['SaveDir'] = '';
$cfg['Servers'][1]['host'] = 'db';
$cfg['Servers'][1]['port'] = '3306';
$cfg['Servers'][1]['auth_type'] = 'cookie';
PHP
apply_out="${evidence_dir}/${scenario}/config-apply.json"
if c5_cli --yes --json service config apply phpmyadmin config.user.inc.php --file "${private}/pma-fixed.php" \
	>"${apply_out}" 2>"${apply_out}.err"; then
	c4_pass "${scenario}" "service config apply repaired the legacy phpMyAdmin config"
else
	if c5_cli --yes --json service config apply phpmyadmin config.user.inc.php --file "${private}/pma-fixed.php" --resolution use \
		>"${apply_out}" 2>"${apply_out}.err"; then
		c4_pass "${scenario}" "service config apply repaired the legacy config with conflict resolution"
	else
		c4_fail "${scenario}" "service config apply repaired the legacy config" "$(head -1 "${apply_out}.err")"
	fi
fi
c4_assert_contains "${scenario}" "config apply reports a backup path" "$(cat "${apply_out}")" '"backup_path"'
c7_pma_assert_config "after repair"
c7_pma_restart
c7_pma_ui_request repair login

c7_section "E2E-20 user-owned config survives reinstall"
printf '\n// e2e-user-owned-marker\n' >>"${pma_config}"
user_config_hash="$(sha256sum "${pma_config}" | awk '{print $1}')"
c5_cli --json service install phpmyadmin >"${evidence_dir}/${scenario}/reinstall-phpmyadmin.json" 2>"${evidence_dir}/${scenario}/reinstall-phpmyadmin.err" ||
	c4_fail "${scenario}" "phpMyAdmin reinstall succeeds with a user-owned config" "$(head -1 "${evidence_dir}/${scenario}/reinstall-phpmyadmin.err")"
c5_strip_service_ports mysql
c5_strip_service_ports mariadb
c4_patch_phpmyadmin_port
c4_assert_eq "${scenario}" "user-owned phpMyAdmin config is not overwritten" "$(sha256sum "${pma_config}" | awk '{print $1}')" "${user_config_hash}"
c7_assert_file_contains "${scenario}" "user-owned marker remains after reinstall" "${pma_config}" "e2e-user-owned-marker"
c7_pma_compose up -d --no-deps phpmyadmin >"${logs_dir}/restart-pma-user-config.log" 2>&1
c5_wait_container_healthy "${pma_container}" 120
c7_pma_ui_request user-owned login

c7_section "E2E-20 loopback, health, and fixed-port gate"
docker port "${pma_container}" >"${evidence_dir}/${scenario}/pma-ports.txt" 2>&1
c7_assert_file_contains "${scenario}" "phpMyAdmin publishes a loopback-only port" "${evidence_dir}/${scenario}/pma-ports.txt" "80/tcp -> 127.0.0.1:${pma_host_port}"
if grep -q '0.0.0.0' "${evidence_dir}/${scenario}/pma-ports.txt"; then
	c4_fail "${scenario}" "phpMyAdmin has no wildcard port binding" "0.0.0.0 found"
else
	c4_pass "${scenario}" "phpMyAdmin has no wildcard port binding"
fi
ss -ltnp >"${evidence_dir}/${scenario}/pma-listeners.txt" 2>&1 || true
c7_assert_file_contains "${scenario}" "phpMyAdmin listener is loopback-only" "${evidence_dir}/${scenario}/pma-listeners.txt" "127.0.0.1:${pma_host_port}"
if grep -Eq "(^|[[:space:]])0\.0\.0\.0:${pma_host_port}([[:space:]]|$)" "${evidence_dir}/${scenario}/pma-listeners.txt"; then
	c4_fail "${scenario}" "phpMyAdmin has no wildcard listener" "0.0.0.0 found"
else
	c4_pass "${scenario}" "phpMyAdmin has no wildcard listener"
fi
if grep -Eq ':(3306|8082)[[:space:]]' "${evidence_dir}/${scenario}/pma-listeners.txt"; then
	printf 'fixed 3306/8082 listeners are owned by the real install relay; product activation on those ports is BLOCKED and the no-port/high-port equivalent was used.\n' \
		>"${evidence_dir}/${scenario}/fixed-port-blocked.txt"
	cp "${evidence_dir}/${scenario}/pma-listeners.txt" "${evidence_dir}/${scenario}/fixed-port-listeners.txt"
	c4_pass "${scenario}" "fixed 3306/8082 collision recorded as BLOCKED with a working equivalent"
else
	printf 'The real install owns Windows loopback 3306/8082; those listeners are not visible inside the disposable Ubuntu netns. Product activation on the fixed ports is BLOCKED and the no-port/high-port equivalent was used.\n' \
		>"${evidence_dir}/${scenario}/fixed-port-blocked.txt"
	c4_pass "${scenario}" "fixed 3306/8082 collision recorded as BLOCKED with a working equivalent"
fi
docker inspect -f '{{json .Config.Healthcheck}}' "${pma_container}" >"${evidence_dir}/${scenario}/pma-health.json" 2>&1
c4_assert_contains "${scenario}" "phpMyAdmin healthcheck is configured" "$(cat "${evidence_dir}/${scenario}/pma-health.json")" '"Test"'
c4_assert_eq "${scenario}" "phpMyAdmin is healthy after the matrix" "$(c7_pma_health)" "healthy"
c7_pma_restart
c4_assert_eq "${scenario}" "phpMyAdmin recovers after a final restart" "$(c7_pma_health)" "healthy"
c7_pma_ui_request final-restart login
touch "$home/ui-finished"

c7_section "E2E-20 secret scan"
c7_assert_file_lacks_secret "${scenario}" "phpMyAdmin config contains no MySQL password" "${pma_config}" "${secret_store}/mysql_devstack.password"
c7_assert_no_secret_in_tree "${scenario}" "phpMyAdmin config/logs contain no MySQL password" "${logs_dir}" "${secret_store}/mysql_devstack.password"
c7_assert_no_secret_in_tree "${scenario}" "phpMyAdmin config/logs contain no password" "${devstack_home}/services/phpmyadmin" "${secret_store}/mysql_devstack.password"
c7_scan "${scenario}"

status="$(c4_scenario_status "${scenario}")"
c7_record "${scenario}" "${status}" P0 \
	"$(c4_scenario_summary "${scenario}"); fresh config/env/login, restart, MySQL stopped, MariaDB switch, legacy config apply repair, user-owned config reinstall, loopback/health, fixed-port sub-step BLOCKED"
printf '%s %s\n' "${scenario}" "$(c4_scenario_summary "${scenario}")"
