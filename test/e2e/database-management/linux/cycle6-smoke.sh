#!/usr/bin/env bash
# Cycle 6 smoke: disposable env + one backup/restore/import/export round trip.
set -Eeuo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export E2E_STAMP="${E2E_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
export E2E_RUN_KEY="${E2E_RUN_KEY:-${E2E_STAMP}}"
export E2E_SCENARIO_ENV="${E2E_SCENARIO_ENV:-LIN-UBE}"
# shellcheck source=cycle6-env.sh
source "${here}/cycle6-env.sh"

scenario="C6-SMOKE"
trap 'c6_cleanup' EXIT
c6_init
c6_ensure_services
c4_check_init "${scenario}"
mkdir -p "${evidence_dir}/${scenario}"

c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_app CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_app;
CREATE TABLE IF NOT EXISTS e2e_marker (id INT PRIMARY KEY, label VARCHAR(128) NOT NULL, note TEXT NULL);
INSERT INTO e2e_marker (id,label,note) VALUES (1,'alpha','one'),(2,'beta','two'),(3,'café ☕ 🚀','unicode')
ON DUPLICATE KEY UPDATE label=VALUES(label), note=VALUES(note);
SQL
c4_assert_eq "${scenario}" "marker rows" "$(c4_mysql_root_query -D e2e_app -e 'SELECT COUNT(*) FROM e2e_marker')" "3"

out="${evidence_dir}/${scenario}/backup-create.json"
job="$(c6_cli_job "${out}" c5_cli --json db backup create --service mysql --database e2e_app)" || c6_die "backup create failed: $(cat "${out}.err" 2>/dev/null)"
c4_assert_eq "${scenario}" "backup job succeeded" "$(c6_json_field "${job}" status)" "succeeded"
backup_id="$(c6_json_field "${job}" backup_id)"
c4_pass "${scenario}" "backup id ${backup_id}"
file="$(c6_backup_file mysql "${backup_id}")"
[ -s "${file}" ] && c4_pass "${scenario}" "backup file exists" || c4_fail "${scenario}" "backup file exists"
gzip -t "${file}" && c4_pass "${scenario}" "gzip integrity" || c4_fail "${scenario}" "gzip integrity"
c6_assert_file_mode "${scenario}" "backup file mode" "${file}" "600"

restore_out="${evidence_dir}/${scenario}/restore.json"
restore_job="$(c6_cli_job "${restore_out}" c5_cli --json db backup restore --service mysql --backup-id "${backup_id}" --target e2e_smoke_restore)" || c6_die "restore failed: $(cat "${restore_out}.err" 2>/dev/null)"
c4_assert_eq "${scenario}" "restore job succeeded" "$(c6_json_field "${restore_job}" status)" "succeeded"
c4_assert_eq "${scenario}" "restored rows" "$(c4_mysql_root_query -D e2e_smoke_restore -e 'SELECT COUNT(*) FROM e2e_marker')" "3"

import_file="${home}/fixtures/smoke-import.sql"
printf 'USE e2e_smoke_restore;\nINSERT INTO e2e_marker (id,label,note) VALUES (9,\x27imported\x27,\x27merge\x27);\n' >"${import_file}"
import_out="${evidence_dir}/${scenario}/import.json"
import_job="$(c6_cli_job "${import_out}" c5_cli --json db import --service mysql --database e2e_smoke_restore --file "${import_file}")" || c6_die "import failed: $(cat "${import_out}.err" 2>/dev/null)"
c4_assert_eq "${scenario}" "import job succeeded" "$(c6_json_field "${import_job}" status)" "succeeded"
c4_assert_eq "${scenario}" "merged rows" "$(c4_mysql_root_query -D e2e_smoke_restore -e 'SELECT COUNT(*) FROM e2e_marker')" "4"

export_file="${evidence_dir}/${scenario}/export.sql.gz"
c5_cli db export --service mysql --database e2e_smoke_restore --output "${export_file}" >"${evidence_dir}/${scenario}/export.log" 2>&1 || c6_die "export failed"
gzip -t "${export_file}" && c4_pass "${scenario}" "export gzip" || c4_fail "${scenario}" "export gzip"

c4_scenario_summary "${scenario}"
c6_record "${scenario}" "$(c4_scenario_status "${scenario}")" P2 "cycle 6 smoke"
c6_secret_scan "${scenario}"
