#!/usr/bin/env bash
# Cycle 4 / E2E-01: Linux service lifecycle equivalent on the disposable
# Ubuntu Docker engine. MySQL and phpMyAdmin cannot use product activation
# because this WSL host already reserves 3306/8082 for the real install; their
# equivalent lifecycle runs on no-port/manual containers and is recorded as a
# BLOCKED product step, never as a real-install change.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle4-env.sh
source "${here}/cycle4-env.sh"
# shellcheck source=cycle4-check.sh
source "${here}/cycle4-check.sh"

scenario="E2E-01"
c4_guard
c4_check_init "${scenario}"
trap c4_cleanup EXIT
c4_init_dirs
c4_write_helper_config
c4_start_helper
c4_start_daemon

service_json() {
	c4_cli --json service status "$1"
}

service_field() {
	local svc="$1" field="$2"
	service_json "${svc}" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get(sys.argv[1], ""))' "${field}"
}

wait_stopped() {
	local container="$1"
	for _ in $(seq 1 60); do
		[ "$(docker inspect -f '{{.State.Running}}' "${container}" 2>/dev/null || echo false)" = "false" ] && return 0
		sleep 1
	done
	return 1
}

pma_login() {
	local jar="$1" password_file="$2" output="$3" page token set_session
	rm -f "${jar}"
	page="${home}/pma-login-$(basename "${jar}").html"
	curl -sS --max-time 10 -c "${jar}" "http://127.0.0.1:${pma_host_port}/" -o "${page}"
	token="$(grep -o 'name="token" value="[^"]*"' "${page}" | head -1 | sed 's/.*value="//; s/"$//')"
	set_session="$(grep -o 'name="set_session" value="[^"]*"' "${page}" | head -1 | sed 's/.*value="//; s/"$//')"
	[ -n "${token}" ] && [ -n "${set_session}" ] || return 1
	curl -sS --max-time 20 -b "${jar}" -c "${jar}" -L \
		--data-urlencode "pma_username=devstack" \
		--data-urlencode "pma_password@${password_file}" \
		--data-urlencode "server=1" \
		--data-urlencode "target=index.php" \
		--data-urlencode "token=${token}" \
		--data-urlencode "set_session=${set_session}" \
		"http://127.0.0.1:${pma_host_port}/index.php" -o "${output}"
}

# --- install and start the four services -------------------------------------
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

c4_cli --json service install phpmyadmin >"${evidence_dir}/${scenario}/install-phpmyadmin.json"
c4_patch_phpmyadmin_port
c4_compose up -d --no-deps phpmyadmin >"${evidence_dir}/${scenario}/start-phpmyadmin.log" 2>&1
c4_wait_container_healthy devstack-phpmyadmin 180

# --- health, ports, and loopback-only ---------------------------------------
for svc in mysql postgresql redis phpmyadmin; do
	container="devstack-${svc}"
	if [ "$(docker inspect -f '{{.State.Running}}' "${container}")" = "true" ]; then
		c4_pass "${scenario}" "${svc} container running"
	else
		c4_fail "${scenario}" "${svc} container running"
	fi
	health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "${container}")"
	c4_assert_eq "${scenario}" "${svc} container health" "${health}" "healthy"
done

c4_assert_eq "${scenario}" "mysql publishes no host port" "$(docker port devstack-mysql 2>/dev/null || true)" ""
c4_assert_contains "${scenario}" "phpmyadmin high loopback port" "$(docker port devstack-phpmyadmin)" "127.0.0.1:${pma_host_port}"
c4_assert_contains "${scenario}" "postgresql loopback port" "$(docker port devstack-postgresql)" "127.0.0.1:5432"
c4_assert_contains "${scenario}" "redis loopback port" "$(docker port devstack-redis)" "127.0.0.1:6379"
for svc in postgresql redis; do
	if docker port "devstack-${svc}" | grep -q '0.0.0.0'; then
		c4_fail "${scenario}" "${svc} not bound to all interfaces"
	else
		c4_pass "${scenario}" "${svc} not bound to all interfaces"
	fi
done

# --- product lifecycle for PostgreSQL and Redis -----------------------------
for svc in postgresql redis; do
	if c4_cli --json service restart "${svc}" >"${evidence_dir}/${scenario}/restart-${svc}.json" 2>&1; then
		c4_pass "${scenario}" "${svc} product restart"
	else
		c4_fail "${scenario}" "${svc} product restart"
	fi
	c4_wait_container_healthy "devstack-${svc}" 120
	if c4_cli --json service deactivate "${svc}" >"${evidence_dir}/${scenario}/deactivate-${svc}.json" 2>&1; then
		c4_pass "${scenario}" "${svc} product deactivate"
	else
		c4_fail "${scenario}" "${svc} product deactivate"
	fi
	wait_stopped "devstack-${svc}" && c4_pass "${scenario}" "${svc} stopped after deactivate" || c4_fail "${scenario}" "${svc} stopped after deactivate"
	if c4_cli --json service activate "${svc}" >"${evidence_dir}/${scenario}/reactivate-${svc}.json" 2>&1; then
		c4_pass "${scenario}" "${svc} product reactivate"
	else
		c4_fail "${scenario}" "${svc} product reactivate"
	fi
	c4_wait_container_healthy "devstack-${svc}" 120
done

# --- MySQL lifecycle: restart/deactivate work; activation is blocked by 3306 -
if c4_cli --json service restart mysql >"${evidence_dir}/${scenario}/restart-mysql.json" 2>&1; then
	c4_pass "${scenario}" "mysql product restart"
else
	c4_fail "${scenario}" "mysql product restart"
fi
c4_wait_container_healthy devstack-mysql 120
if c4_cli --json service deactivate mysql >"${evidence_dir}/${scenario}/deactivate-mysql.json" 2>&1; then
	c4_pass "${scenario}" "mysql product deactivate"
else
	c4_fail "${scenario}" "mysql product deactivate"
fi
wait_stopped devstack-mysql && c4_pass "${scenario}" "mysql stopped after deactivate" || c4_fail "${scenario}" "mysql stopped after deactivate"
set +e
activate_out="$(c4_cli --json service activate mysql 2>&1)"
activate_rc=$?
set -e
printf '%s\n' "${activate_out}" >"${evidence_dir}/${scenario}/activate-mysql-blocked.json"
if [ "${activate_rc}" -ne 0 ] && printf '%s' "${activate_out}" | grep -q '3306'; then
	c4_pass "${scenario}" "mysql product activation refused on occupied 3306"
	c4_note "${scenario}" "BLOCKED: product activation of MySQL cannot run because this host already reserves 3306 for the real install. Equivalent no-port container lifecycle verified manually."
else
	c4_fail "${scenario}" "mysql product activation refused on occupied 3306"
fi
c4_compose start mysql >"${evidence_dir}/${scenario}/manual-start-mysql.log" 2>&1
c4_wait_container_healthy devstack-mysql 120
c4_pass "${scenario}" "mysql manual no-port restart"

# --- phpMyAdmin lifecycle: restart/deactivate work; activation blocked by 8082 -
if c4_cli --json service restart phpmyadmin >"${evidence_dir}/${scenario}/restart-phpmyadmin.json" 2>&1; then
	c4_pass "${scenario}" "phpmyadmin product restart"
else
	c4_fail "${scenario}" "phpmyadmin product restart"
fi
c4_wait_container_healthy devstack-phpmyadmin 120
if c4_cli --json service deactivate phpmyadmin >"${evidence_dir}/${scenario}/deactivate-phpmyadmin.json" 2>&1; then
	c4_pass "${scenario}" "phpmyadmin product deactivate"
else
	c4_fail "${scenario}" "phpmyadmin product deactivate"
fi
wait_stopped devstack-phpmyadmin && c4_pass "${scenario}" "phpmyadmin stopped after deactivate" || c4_fail "${scenario}" "phpmyadmin stopped after deactivate"
set +e
activate_out="$(c4_cli --json service activate phpmyadmin 2>&1)"
activate_rc=$?
set -e
printf '%s\n' "${activate_out}" >"${evidence_dir}/${scenario}/activate-phpmyadmin-blocked.json"
if [ "${activate_rc}" -ne 0 ] && printf '%s' "${activate_out}" | grep -q '8082'; then
	c4_pass "${scenario}" "phpmyadmin product activation refused on occupied 8082"
	c4_note "${scenario}" "BLOCKED: product activation of phpMyAdmin cannot run because this host already reserves 8082 for the real install. Equivalent high-port container lifecycle verified manually."
else
	c4_fail "${scenario}" "phpmyadmin product activation refused on occupied 8082"
fi
c4_patch_phpmyadmin_port
c4_compose start phpmyadmin >"${evidence_dir}/${scenario}/manual-start-phpmyadmin.log" 2>&1
c4_wait_container_healthy devstack-phpmyadmin 120
c4_pass "${scenario}" "phpmyadmin manual high-port restart"

# --- doctor after transitions ------------------------------------------------
set +e
c4_cli --json doctor >"${evidence_dir}/${scenario}/doctor.json" 2>&1
doctor_rc=$?
set -e
python3 - "${evidence_dir}/${scenario}/doctor.json" "${evidence_dir}/${scenario}/doctor-summary.txt" <<'PY'
import json, sys
text = open(sys.argv[1], encoding="utf-8").read().lstrip()
data, _ = json.JSONDecoder().raw_decode(text)
checks = {c["id"]: c for c in data.get("checks", [])}
lines = []
for cid in ("helper", "ports", "serving-mode"):
    c = checks.get(cid, {})
    lines.append(f"{cid}: {c.get('status','missing')}")
open(sys.argv[2], "w", encoding="utf-8").write("\n".join(lines) + "\n")
PY
if grep -q '^helper: ok' "${evidence_dir}/${scenario}/doctor-summary.txt" &&
	grep -q '^ports: ok' "${evidence_dir}/${scenario}/doctor-summary.txt"; then
	c4_pass "${scenario}" "doctor helper/ports checks ok after transitions"
else
	c4_fail "${scenario}" "doctor helper/ports checks ok after transitions"
fi
c4_note "${scenario}" "doctor exit ${doctor_rc}; hosts/trust fail is expected because the disposable helper uses custom paths."

# --- no orphan containers ----------------------------------------------------
project="$(c4_compose_project)"
expected="$(printf '%s\n' devstack-mysql devstack-postgresql devstack-redis devstack-phpmyadmin | sort)"
actual="$(docker ps -a --filter "label=com.docker.compose.project=${project}" --format '{{.Names}}' | sort)"
c4_assert_eq "${scenario}" "no orphan or missing containers" "${actual}" "${expected}"

# --- phpMyAdmin login matrix ------------------------------------------------
pma_login "${home}/pma-ok.cookies" "${devstack_home}/secrets/mysql/devstack_password" "${evidence_dir}/${scenario}/pma-after-login.html"
if grep -q 'Server version' "${evidence_dir}/${scenario}/pma-after-login.html" &&
	grep -qi 'Log out' "${evidence_dir}/${scenario}/pma-after-login.html"; then
	c4_pass "${scenario}" "phpMyAdmin login with MySQL active"
else
	c4_fail "${scenario}" "phpMyAdmin login with MySQL active"
fi
printf '%s' 'definitely-wrong-password' >"${home}/wrong-password"
chmod 600 "${home}/wrong-password"
pma_login "${home}/pma-bad.cookies" "${home}/wrong-password" "${evidence_dir}/${scenario}/pma-wrong.html"
if grep -qiE 'Cannot log in|Access denied|#1045|incorrect' "${evidence_dir}/${scenario}/pma-wrong.html"; then
	c4_pass "${scenario}" "phpMyAdmin wrong password rejected"
else
	c4_fail "${scenario}" "phpMyAdmin wrong password rejected"
fi
c4_compose stop mysql >/dev/null 2>&1
sleep 2
pma_login "${home}/pma-down.cookies" "${devstack_home}/secrets/mysql/devstack_password" "${evidence_dir}/${scenario}/pma-down.html"
if grep -qiE 'Cannot log in|Access denied|#1045|Connection refused|server is not responding|mysqli' "${evidence_dir}/${scenario}/pma-down.html"; then
	c4_pass "${scenario}" "phpMyAdmin clear error with MySQL stopped"
else
	c4_fail "${scenario}" "phpMyAdmin clear error with MySQL stopped"
fi
c4_compose start mysql >/dev/null 2>&1
c4_wait_container_healthy devstack-mysql 120
c4_pass "${scenario}" "MySQL restarted after phpMyAdmin matrix"

# --- result ------------------------------------------------------------------
status="$(c4_scenario_status "${scenario}")"
if [ "${status}" = "PASS" ]; then
	status="BLOCKED"
	notes="PostgreSQL/Redis product lifecycle PASS; phpMyAdmin login PASS; MySQL/phpMyAdmin product activation BLOCKED by real-install fixed ports 3306/8082; equivalent no-port lifecycle verified"
else
	notes="one or more lifecycle checks failed"
fi
"${cycle4_root}/results.sh" record --id "${scenario}" --priority P0 --environment LIN-UBE \
	--status "${status}" --evidence "${evidence_dir}/${scenario}" --commit "$(git -c safe.directory="${repo}" -C "${repo}" rev-parse HEAD)" \
	--residual-risk "MySQL/phpMyAdmin product activation cannot run while the real install owns 3306/8082; no-port equivalent verified" \
	--notes "${notes}" >>"${evidence_dir}/${scenario}/checks.log" 2>&1
c4_scenario_summary "${scenario}"
