#!/usr/bin/env bash
# Cycle 5 environment smoke: non-root daemon, MySQL no-port, site link, terminal
# env, PHP-FPM config, and PHP exec. This is not a scored scenario.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

scenario="C5-SMOKE"
c5_guard
c4_check_init "${scenario}"
trap c5_cleanup EXIT
c5_init
c5_ensure_base
c5_install_php

c5_create_project e2e-smoke-static >/dev/null
c5_cli --json site add --name e2e-smoke --path "${home}/projects/e2e-smoke-static" --kind static \
	--database-service mysql --database-new e2e_smoke_app --database-user-new e2e_smoke_user \
	--database-grant read_write >"${evidence_dir}/${scenario}/site-add.json"
c4_pass "${scenario}" "site add with new database and user"

c5_capture_site_env e2e-smoke smoke
c4_assert_contains "${scenario}" "runtime env DB_CONNECTION" "$(cat "${evidence_dir}/${scenario}/smoke-env-summary.txt")" "DB_CONNECTION=mysql"
c4_assert_contains "${scenario}" "runtime env DB_HOST" "$(cat "${evidence_dir}/${scenario}/smoke-env-summary.txt")" "DB_HOST=127.0.0.1"
c4_assert_contains "${scenario}" "runtime env DB_PORT" "$(cat "${evidence_dir}/${scenario}/smoke-env-summary.txt")" "DB_PORT=3306"
c4_assert_contains "${scenario}" "runtime env DB_DATABASE" "$(cat "${evidence_dir}/${scenario}/smoke-env-summary.txt")" "DB_DATABASE=e2e_smoke_app"
c4_assert_contains "${scenario}" "runtime env DB_USERNAME" "$(cat "${evidence_dir}/${scenario}/smoke-env-summary.txt")" "DB_USERNAME=e2e_smoke_user"
c4_assert_contains "${scenario}" "runtime env DB_PASSWORD present" "$(cat "${evidence_dir}/${scenario}/smoke-env-summary.txt")" "DB_PASSWORD_PRESENT=true"

c5_terminal_probe e2e-smoke "${evidence_dir}/${scenario}/terminal.txt"
c4_assert_contains "${scenario}" "terminal DB_DATABASE" "$(cat "${evidence_dir}/${scenario}/terminal.txt")" "E2E_TERM DB_DATABASE=e2e_smoke_app"
c4_assert_contains "${scenario}" "terminal DB_PASSWORD present" "$(cat "${evidence_dir}/${scenario}/terminal.txt")" "E2E_TERM DB_PASSWORD_PRESENT=true"

c5_create_project e2e-smoke-php php >/dev/null
uid="$(c5_user_id mysql e2e_smoke_user)"
c5_cli --json site add --name e2e-smoke-php --path "${home}/projects/e2e-smoke-php" --kind php --php 8.4 \
	--database-service mysql --database e2e_smoke_app --database-user "${uid}" >"${evidence_dir}/${scenario}/php-site-add.json"
c5_cli --json site start e2e-smoke-php >"${evidence_dir}/${scenario}/php-site-start.json"
php_site="$(c5_site_id e2e-smoke-php)"
fpm="$(find "${devstack_home}/sites/${php_site}" -name php-fpm.conf -print -quit 2>/dev/null || true)"
if [ -n "${fpm}" ]; then
	c4_pass "${scenario}" "php-fpm.conf generated"
	c5_assert_file_lacks_secret "${scenario}" "php-fpm.conf has no password" "${fpm}" "$(c5_secret_file smoke)"
else
	c4_fail "${scenario}" "php-fpm.conf generated"
fi
c5_php_exec e2e-smoke-php "${evidence_dir}/${scenario}/php-exec.txt"
c4_assert_contains "${scenario}" "php exec DB_DATABASE" "$(cat "${evidence_dir}/${scenario}/php-exec.txt")" "E2E_EXEC DB_DATABASE=e2e_smoke_app"
c4_assert_contains "${scenario}" "php exec DB_PASSWORD present" "$(cat "${evidence_dir}/${scenario}/php-exec.txt")" "E2E_EXEC DB_PASSWORD_PRESENT=true"

c5_audit_copy "${private}/state-copy" && c4_pass "${scenario}" "state.db copied for audit" || c4_fail "${scenario}" "state.db copied for audit"
c5_secret_scan "${scenario}" && c4_pass "${scenario}" "secret scan clean" || c4_fail "${scenario}" "secret scan clean"
c4_scenario_summary "${scenario}"
