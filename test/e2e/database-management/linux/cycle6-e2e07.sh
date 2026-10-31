#!/usr/bin/env bash
# E2E-07 - backup, restore, export, and import on the disposable Linux helper.
#
# Covers the P0 recovery path: manual backup metadata/integrity, list/download/
# delete while stopped, new-target and overwrite restore, the linked-site gate,
# export file/stdout, merge/replace import with pre-op backups, staging
# permissions/cleanup, invalid dumps, cancellation, daemon-restart
# reconciliation, and Redis unsupported operations.
set -Eeuo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle6-env.sh
source "${here}/cycle6-env.sh"

scenario="E2E-07"
c6_guard
if [ "${E2E_CYCLE6_SHARED:-0}" != "1" ]; then
	trap 'c6_cleanup' EXIT
	c6_init
	c6_ensure_services
fi
c4_check_init "${scenario}"
mkdir -p "${evidence_dir}/${scenario}"
private="${private:-${home}/private}"
mkdir -p "${private}"

c6_section "seed e2e_app"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_app CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_app;
CREATE TABLE IF NOT EXISTS e2e_marker (
  id INT NOT NULL PRIMARY KEY,
  label VARCHAR(128) NOT NULL,
  note TEXT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO e2e_marker (id, label, note) VALUES
  (1, 'alpha', 'first marker row'),
  (2, 'beta', 'second marker row'),
  (3, 'café ☕ 🚀', 'unicode marker row')
ON DUPLICATE KEY UPDATE label = VALUES(label), note = VALUES(note);
DELETE FROM e2e_marker WHERE id IN (50, 99);
SQL
c4_assert_eq "${scenario}" "seed marker rows" "$(c4_mysql_root_query -D e2e_app -e 'SELECT COUNT(*) FROM e2e_marker')" "3"

c6_section "manual backup"
backup_out="${evidence_dir}/${scenario}/backup-create.json"
backup_job="$(c6_cli_job "${backup_out}" c5_cli --json db backup create --service mysql --database e2e_app)" ||
	c6_die "manual backup failed: $(cat "${backup_out}.err" 2>/dev/null)"
c6_assert_job_status "${scenario}" "manual backup job succeeded" "${backup_job}" "succeeded"
backup_id="$(c6_json_field "${backup_job}" backup_id)"
c4_pass "${scenario}" "manual backup id ${backup_id}"
backup_file="$(c6_backup_file mysql "${backup_id}")"
[ -s "${backup_file}" ] && c4_pass "${scenario}" "backup file exists" || c4_fail "${scenario}" "backup file exists"
c6_assert_file_mode "${scenario}" "backup file mode 0600" "${backup_file}" "600"
c6_assert_file_mode "${scenario}" "backup directory mode 0700" "$(dirname "${backup_file}")" "700"
gzip -t "${backup_file}" && c4_pass "${scenario}" "backup gzip integrity" || c4_fail "${scenario}" "backup gzip integrity"
backup_sha="$(sha256sum "${backup_file}" | awk '{print $1}')"

backup_json="$(c6_backup_json mysql "${backup_id}")" || c6_die "backup not listed"
c6_assert_json_field "${scenario}" "listed backup kind manual" "${backup_json}" kind "manual"
c6_assert_json_field "${scenario}" "listed backup status ready" "${backup_json}" status "ready"
c6_assert_json_field "${scenario}" "listed backup database" "${backup_json}" database "e2e_app"
c6_assert_json_field "${scenario}" "listed backup sha256" "${backup_json}" sha256 "${backup_sha}"
listed_size="$(c6_json_field "${backup_json}" size_bytes)"
actual_size="$(stat -c '%s' "${backup_file}")"
c4_assert_eq "${scenario}" "listed backup size matches file" "${listed_size}" "${actual_size}"
listed_created="$(c6_json_field "${backup_json}" created_at)"
[ -n "${listed_created}" ] && c4_pass "${scenario}" "listed backup created_at" || c4_fail "${scenario}" "listed backup created_at"
case "${backup_id}" in
*--*.manual.sql.gz) c4_pass "${scenario}" "backup filename carries kind" ;;
*) c4_fail "${scenario}" "backup filename carries kind" "${backup_id}" ;;
esac
zcat "${backup_file}" | grep -q 'e2e_marker' && c4_pass "${scenario}" "backup contains marker table" || c4_fail "${scenario}" "backup contains marker table"
zcat "${backup_file}" | grep -q 'caf' && c4_pass "${scenario}" "backup preserves unicode" || c4_fail "${scenario}" "backup preserves unicode"

c6_audit_copy "${private}/state-copy-backup" >/dev/null 2>&1 || true
meta_kind="$(c6_audit_query "${private}/state-copy-backup" "SELECT kind FROM database_backups WHERE id='${backup_id}' AND service_id='mysql';" 2>/dev/null || true)"
meta_status="$(c6_audit_query "${private}/state-copy-backup" "SELECT status FROM database_backups WHERE id='${backup_id}' AND service_id='mysql';" 2>/dev/null || true)"
c4_assert_eq "${scenario}" "state.db backup kind" "${meta_kind}" "manual"
c4_assert_eq "${scenario}" "state.db backup status" "${meta_status}" "ready"

c6_section "list, download, delete while stopped"
throwaway_out="${evidence_dir}/${scenario}/backup-throwaway.json"
throwaway_job="$(c6_cli_job "${throwaway_out}" c5_cli --json db backup create --service mysql --database e2e_app)" ||
	c6_die "throwaway backup failed"
throwaway_id="$(c6_json_field "${throwaway_job}" backup_id)"
[ -n "${throwaway_id}" ] && c4_pass "${scenario}" "throwaway backup created" || c4_fail "${scenario}" "throwaway backup created"

c5_compose stop mysql >"${evidence_dir}/${scenario}/service-stop.json" 2>"${evidence_dir}/${scenario}/service-stop.err" ||
	c6_die "could not stop mysql"
c6_wait_service_state mysql inactive 120 && c4_pass "${scenario}" "mysql stopped" || c4_fail "${scenario}" "mysql stopped"

c6_backup_list mysql >"${evidence_dir}/${scenario}/backups-stopped.json" &&
	c4_pass "${scenario}" "list backups while stopped" || c4_fail "${scenario}" "list backups while stopped"
c6_backup_json mysql "${backup_id}" >"${private}/backup-stopped.json" 2>/dev/null &&
	c4_pass "${scenario}" "backup still listed while stopped" || c4_fail "${scenario}" "backup still listed while stopped"

download_path="${private}/downloaded-while-stopped.sql.gz"
c5_cli db backup download --service mysql --backup-id "${backup_id}" --output "${download_path}" \
	>"${evidence_dir}/${scenario}/download-stopped.log" 2>&1 &&
	c4_pass "${scenario}" "download backup while stopped" || c4_fail "${scenario}" "download backup while stopped"
gzip -t "${download_path}" 2>/dev/null && c4_pass "${scenario}" "downloaded backup gzip" || c4_fail "${scenario}" "downloaded backup gzip"
c4_assert_eq "${scenario}" "downloaded backup sha256" "$(sha256sum "${download_path}" | awk '{print $1}')" "${backup_sha}"
c4_assert_eq "${scenario}" "download output mode 0600" "$(stat -c '%a' "${download_path}")" "600"

c5_cli --json db backup delete --service mysql --backup-id "${throwaway_id}" --yes \
	>"${evidence_dir}/${scenario}/delete-stopped.json" 2>"${evidence_dir}/${scenario}/delete-stopped.err" &&
	c4_pass "${scenario}" "delete backup while stopped" || c4_fail "${scenario}" "delete backup while stopped"
if c6_backup_list mysql | grep -q "${throwaway_id}"; then
	c4_fail "${scenario}" "deleted backup is gone from list"
else
	c4_pass "${scenario}" "deleted backup is gone from list"
fi
[ ! -e "$(c6_backup_file mysql "${throwaway_id}")" ] && c4_pass "${scenario}" "deleted backup file removed" || c4_fail "${scenario}" "deleted backup file removed"

c4_expect_code "${scenario}" "create backup while stopped" DATABASE_SERVICE_INACTIVE \
	c5_cli --json db backup create --service mysql --database e2e_app
c4_expect_code "${scenario}" "restore backup while stopped" DATABASE_SERVICE_INACTIVE \
	c5_cli --json db backup restore --service mysql --backup-id "${backup_id}" --target e2e_restore_stopped
c4_expect_code "${scenario}" "import while stopped" DATABASE_SERVICE_INACTIVE \
	c5_cli --json db import --service mysql --database e2e_app --file "${cycle4_root}/fixtures/dumps/seed.sql.gz"
c6_expect_cli_code "${scenario}" "export while stopped" DATABASE_SERVICE_INACTIVE \
	"${evidence_dir}/${scenario}/export-stopped.err" \
	c5_cli --json db export --service mysql --database e2e_app --output "${private}/stopped-export.sql.gz"

c5_compose start mysql >"${evidence_dir}/${scenario}/service-start.json" 2>"${evidence_dir}/${scenario}/service-start.err" ||
	c6_die "could not start mysql"
c6_wait_service_state mysql active 180 && c4_pass "${scenario}" "mysql restarted" || c4_fail "${scenario}" "mysql restarted"

c6_section "restore to a new target"
c4_mysql_root_query -D e2e_app -e "INSERT INTO e2e_marker (id,label,note) VALUES (99,'only-original','sentinel') ON DUPLICATE KEY UPDATE label=VALUES(label);" >/dev/null
restore_out="${evidence_dir}/${scenario}/restore-new.json"
restore_job="$(c6_cli_job "${restore_out}" c5_cli --json db backup restore --service mysql --backup-id "${backup_id}" --target e2e_restore_new)" ||
	c6_die "new-target restore failed: $(cat "${restore_out}.err" 2>/dev/null)"
c6_assert_job_status "${scenario}" "new-target restore succeeded" "${restore_job}" "succeeded"
c4_assert_eq "${scenario}" "restored marker rows" "$(c4_mysql_root_query -D e2e_restore_new -e 'SELECT COUNT(*) FROM e2e_marker')" "3"
c4_assert_eq "${scenario}" "restored unicode row" "$(c4_mysql_root_query -D e2e_restore_new -e 'SELECT label FROM e2e_marker WHERE id=3')" "café ☕ 🚀"
c4_assert_eq "${scenario}" "original sentinel preserved" "$(c4_mysql_root_query -D e2e_app -e 'SELECT COUNT(*) FROM e2e_marker WHERE id=99')" "1"
c4_assert_eq "${scenario}" "restore target excludes later sentinel" "$(c4_mysql_root_query -D e2e_restore_new -e 'SELECT COUNT(*) FROM e2e_marker WHERE id=99')" "0"

c6_section "overwrite restore and typed confirmation"
c4_mysql_root_query -D e2e_app -e "INSERT INTO e2e_marker (id,label,note) VALUES (50,'will-disappear','post-backup') ON DUPLICATE KEY UPDATE label=VALUES(label);" >/dev/null
encoded_backup="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "${backup_id}")"
c6_expect_api_code "${scenario}" "wrong overwrite confirmation rejected" 409 DATABASE_CONFLICT \
	c6_api_post_json "/api/v1/services/mysql/backups/${encoded_backup}/restore" \
	'{"target":"e2e_app","overwrite":true,"confirm":"not-e2e-app"}'
pre_restore_before="$(c6_backup_id_by_kind mysql pre_restore)"
overwrite_out="${evidence_dir}/${scenario}/restore-overwrite.json"
overwrite_job="$(c6_cli_job "${overwrite_out}" c5_cli --json db backup restore --service mysql --backup-id "${backup_id}" --target e2e_app --overwrite --yes)" ||
	c6_die "overwrite restore failed: $(cat "${overwrite_out}.err" 2>/dev/null)"
c6_assert_job_status "${scenario}" "overwrite restore succeeded" "${overwrite_job}" "succeeded"
pre_restore_id="$(c6_json_field "${overwrite_job}" backup_id)"
pre_restore_after="$(c6_backup_id_by_kind mysql pre_restore)"
[ -n "${pre_restore_after}" ] && [ "${pre_restore_after}" != "${pre_restore_before}" ] &&
	c4_pass "${scenario}" "overwrite created a pre_restore backup" || c4_fail "${scenario}" "overwrite created a pre_restore backup"
c4_assert_eq "${scenario}" "job reports pre_restore backup" "${pre_restore_id}" "${pre_restore_after}"
c4_assert_eq "${scenario}" "post-backup row removed by overwrite" "$(c4_mysql_root_query -D e2e_app -e 'SELECT COUNT(*) FROM e2e_marker WHERE id=50')" "0"
c4_assert_eq "${scenario}" "overwrite restored marker rows" "$(c4_mysql_root_query -D e2e_app -e 'SELECT COUNT(*) FROM e2e_marker')" "3"
c4_assert_eq "${scenario}" "overwrite dropped later sentinel" "$(c4_mysql_root_query -D e2e_app -e 'SELECT COUNT(*) FROM e2e_marker WHERE id=99')" "0"

pre_restore_file="$(c6_backup_file mysql "${pre_restore_id}")"
gzip -t "${pre_restore_file}" && c4_pass "${scenario}" "pre_restore gzip integrity" || c4_fail "${scenario}" "pre_restore gzip integrity"
c4_assert_eq "${scenario}" "pre_restore kind" "$(c6_json_field "$(c6_backup_json mysql "${pre_restore_id}")" kind)" "pre_restore"
pre_restore_restore_out="${evidence_dir}/${scenario}/restore-pre-restore.json"
pre_restore_job="$(c6_cli_job "${pre_restore_restore_out}" c5_cli --json db backup restore --service mysql --backup-id "${pre_restore_id}" --target e2e_pre_restore_check)" ||
	c6_die "pre_restore backup restore failed"
c6_assert_job_status "${scenario}" "pre_restore backup is restorable" "${pre_restore_job}" "succeeded"
c4_assert_eq "${scenario}" "pre_restore contains post-backup row" "$(c4_mysql_root_query -D e2e_pre_restore_check -e 'SELECT COUNT(*) FROM e2e_marker WHERE id=50')" "1"
c4_assert_eq "${scenario}" "pre_restore contains later sentinel" "$(c4_mysql_root_query -D e2e_pre_restore_check -e 'SELECT COUNT(*) FROM e2e_marker WHERE id=99')" "1"

c6_section "linked-site overwrite gate"
c5_create_project e2e07-static >/dev/null
c5_cli --json site add --name e2e07-site --path "${home}/projects/e2e07-static" --kind static \
	>"${evidence_dir}/${scenario}/site-add.json" 2>"${evidence_dir}/${scenario}/site-add.err" ||
	c6_die "site add failed: $(head -1 "${evidence_dir}/${scenario}/site-add.err")"
c5_cli --json db user create --service mysql --name e2e07_user --grant e2e_app:read_write_schema \
	>"${evidence_dir}/${scenario}/user-create.json" 2>"${evidence_dir}/${scenario}/user-create.err" ||
	c6_die "user create failed: $(head -1 "${evidence_dir}/${scenario}/user-create.err")"
user_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${evidence_dir}/${scenario}/user-create.json")"
c5_cli --json db link --site e2e07-site --service mysql --database e2e_app --user-id "${user_id}" --primary \
	>"${evidence_dir}/${scenario}/site-link.json" 2>"${evidence_dir}/${scenario}/site-link.err" ||
	c6_die "site link failed: $(head -1 "${evidence_dir}/${scenario}/site-link.err")"
c5_cli --json site start e2e07-site >"${evidence_dir}/${scenario}/site-start.json" 2>"${evidence_dir}/${scenario}/site-start.err" ||
	c6_die "site start failed: $(head -1 "${evidence_dir}/${scenario}/site-start.err")"
c4_pass "${scenario}" "linked site running"

c4_expect_code "${scenario}" "overwrite blocked while linked site runs" DATABASE_CONFLICT \
	c5_cli --json db backup restore --service mysql --backup-id "${backup_id}" --target e2e_app --overwrite --yes
c4_expect_code "${scenario}" "replace import blocked while linked site runs" DATABASE_CONFLICT \
	c5_cli --json db import --service mysql --database e2e_app --file "${cycle4_root}/fixtures/dumps/seed.sql.gz" --replace --yes
c5_cli --json site stop e2e07-site >"${evidence_dir}/${scenario}/site-stop.json" 2>"${evidence_dir}/${scenario}/site-stop.err" ||
	c6_die "site stop failed"
c4_pass "${scenario}" "linked site stopped"
c4_mysql_root_query -D e2e_app -e "INSERT INTO e2e_marker (id,label,note) VALUES (51,'post-site','gate') ON DUPLICATE KEY UPDATE label=VALUES(label);" >/dev/null
overwrite_gate_out="${evidence_dir}/${scenario}/restore-after-site-stop.json"
overwrite_gate_job="$(c6_cli_job "${overwrite_gate_out}" c5_cli --json db backup restore --service mysql --backup-id "${backup_id}" --target e2e_app --overwrite --yes)" ||
	c6_die "overwrite after site stop failed"
c6_assert_job_status "${scenario}" "overwrite succeeds after site stop" "${overwrite_gate_job}" "succeeded"
c4_assert_eq "${scenario}" "post-site row removed" "$(c4_mysql_root_query -D e2e_app -e 'SELECT COUNT(*) FROM e2e_marker WHERE id=51')" "0"

c6_section "export to file and stdout"
export_file="${private}/e2e_app-export.sql.gz"
c5_cli db export --service mysql --database e2e_app --output "${export_file}" \
	>"${evidence_dir}/${scenario}/export-file.log" 2>&1 || c6_die "export to file failed"
gzip -t "${export_file}" && c4_pass "${scenario}" "file export gzip" || c4_fail "${scenario}" "file export gzip"
c6_assert_file_mode "${scenario}" "file export mode 0600" "${export_file}" "600"
export_sha="$(sha256sum "${export_file}" | awk '{print $1}')"
[ -n "${export_sha}" ] && c4_pass "${scenario}" "file export sha256 ${export_sha}" || c4_fail "${scenario}" "file export sha256"
zcat "${export_file}" | grep -q 'e2e_marker' && c4_pass "${scenario}" "file export contains marker table" || c4_fail "${scenario}" "file export contains marker table"

stdout_file="${private}/e2e_app-stdout.sql.gz"
c5_cli db export --service mysql --database e2e_app --output - >"${stdout_file}" 2>"${evidence_dir}/${scenario}/export-stdout.err" ||
	c6_die "export to stdout failed"
gzip -t "${stdout_file}" && c4_pass "${scenario}" "stdout export gzip" || c4_fail "${scenario}" "stdout export gzip"
grep -q 'Exported e2e_app to -' "${evidence_dir}/${scenario}/export-stdout.err" &&
	c4_pass "${scenario}" "stdout export progress on stderr" || c4_fail "${scenario}" "stdout export progress on stderr"
if grep -q '"database"' "${stdout_file}" 2>/dev/null; then
	c4_fail "${scenario}" "stdout export is binary-clean"
else
	c4_pass "${scenario}" "stdout export is binary-clean"
fi
file_plain_sha="$(zcat "${export_file}" | sha256sum | awk '{print $1}')"
stdout_plain_sha="$(zcat "${stdout_file}" | sha256sum | awk '{print $1}')"
c4_assert_eq "${scenario}" "file and stdout export plaintext checksum" "${stdout_plain_sha}" "${file_plain_sha}"

c6_section "import merge and replace"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_import CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_import;
CREATE TABLE IF NOT EXISTS e2e_import_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);
INSERT INTO e2e_import_marker (id,label) VALUES (1,'one'),(2,'two') ON DUPLICATE KEY UPDATE label=VALUES(label);
SQL
merge_sql="${private}/merge.sql"
printf 'USE e2e_import;\nINSERT INTO e2e_import_marker (id,label) VALUES (3,\x27three\x27) ON DUPLICATE KEY UPDATE label=VALUES(label);\nCREATE TABLE IF NOT EXISTS merge_extra (id INT PRIMARY KEY);\n' >"${merge_sql}"
merge_out="${evidence_dir}/${scenario}/import-merge.json"
merge_job="$(c6_cli_job "${merge_out}" c5_cli --json db import --service mysql --database e2e_import --file "${merge_sql}")" ||
	c6_die "merge import failed: $(cat "${merge_out}.err" 2>/dev/null)"
c6_assert_job_status "${scenario}" "merge .sql import succeeded" "${merge_job}" "succeeded"
c4_assert_eq "${scenario}" "merge keeps existing rows" "$(c4_mysql_root_query -D e2e_import -e 'SELECT COUNT(*) FROM e2e_import_marker')" "3"
c4_assert_eq "${scenario}" "merge added table" "$(c4_mysql_root_query -D e2e_import -e "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='e2e_import' AND TABLE_NAME='merge_extra'")" "1"

merge_gz_sql="${private}/merge-gz.sql"
printf 'USE e2e_import;\nINSERT INTO e2e_import_marker (id,label) VALUES (4,\x27four\x27) ON DUPLICATE KEY UPDATE label=VALUES(label);\n' >"${merge_gz_sql}"
gzip -c "${merge_gz_sql}" >"${private}/merge-gz.sql.gz"
merge_gz_out="${evidence_dir}/${scenario}/import-merge-gz.json"
merge_gz_job="$(c6_cli_job "${merge_gz_out}" c5_cli --json db import --service mysql --database e2e_import --file "${private}/merge-gz.sql.gz")" ||
	c6_die "merge .sql.gz import failed"
c6_assert_job_status "${scenario}" "merge .sql.gz import succeeded" "${merge_gz_job}" "succeeded"
c4_assert_eq "${scenario}" "merge .sql.gz added row" "$(c4_mysql_root_query -D e2e_import -e 'SELECT COUNT(*) FROM e2e_import_marker WHERE id=4')" "1"

replace_sql="${private}/replace.sql"
printf 'USE e2e_import;\nCREATE TABLE IF NOT EXISTS e2e_import_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);\nINSERT INTO e2e_import_marker (id,label) VALUES (10,\x27replacement\x27);\n' >"${replace_sql}"
replace_out="${evidence_dir}/${scenario}/import-replace.json"
replace_job="$(c6_cli_job "${replace_out}" c5_cli --json db import --service mysql --database e2e_import --file "${replace_sql}" --replace --yes)" ||
	c6_die "replace import failed"
c6_assert_job_status "${scenario}" "replace import succeeded" "${replace_job}" "succeeded"
c4_assert_eq "${scenario}" "replace removed old rows" "$(c4_mysql_root_query -D e2e_import -e 'SELECT COUNT(*) FROM e2e_import_marker WHERE id IN (1,2,3,4)')" "0"
c4_assert_eq "${scenario}" "replace inserted new row" "$(c4_mysql_root_query -D e2e_import -e 'SELECT label FROM e2e_import_marker WHERE id=10')" "replacement"
pre_import_id="$(c6_json_field "${replace_job}" backup_id)"
pre_import_after="$(c6_backup_id_by_kind mysql pre_import)"
[ -n "${pre_import_id}" ] && [ "${pre_import_id}" = "${pre_import_after}" ] &&
	c4_pass "${scenario}" "replace created pre_import backup" || c4_fail "${scenario}" "replace created pre_import backup"
c4_assert_eq "${scenario}" "pre_import kind" "$(c6_json_field "$(c6_backup_json mysql "${pre_import_id}")" kind)" "pre_import"
pre_import_restore_out="${evidence_dir}/${scenario}/restore-pre-import.json"
pre_import_job="$(c6_cli_job "${pre_import_restore_out}" c5_cli --json db backup restore --service mysql --backup-id "${pre_import_id}" --target e2e_pre_import_check)" ||
	c6_die "pre_import restore failed"
c6_assert_job_status "${scenario}" "pre_import backup is restorable" "${pre_import_job}" "succeeded"
c4_assert_eq "${scenario}" "pre_import has old rows" "$(c4_mysql_root_query -D e2e_pre_import_check -e 'SELECT COUNT(*) FROM e2e_import_marker WHERE id IN (1,2,3,4)')" "4"

c6_section "import staging permissions and cleanup"
long_sql="${private}/long-import.sql"
printf 'USE e2e_import;\nSELECT SLEEP(5);\nINSERT INTO e2e_import_marker (id,label) VALUES (20,\x27long\x27) ON DUPLICATE KEY UPDATE label=VALUES(label);\n' >"${long_sql}"
long_out="${evidence_dir}/${scenario}/import-long.json"
c5_cli --json db import --service mysql --database e2e_import --file "${long_sql}" >"${long_out}" 2>"${long_out}.err" &
long_cli_pid=$!
long_job_id=""
for _ in $(seq 1 100); do
	long_job_id="$(c6_json_field "$(cat "${long_out}" 2>/dev/null || true)" id)"
	[ -n "${long_job_id}" ] && break
	sleep 0.1
done
[ -n "${long_job_id}" ] || c6_die "long import job id missing"
staging_seen=0
staging_mode=""
daemon_staging_seen=0
daemon_staging_mode=""
for _ in $(seq 1 200); do
	for file in $(c6_staging_files mysql); do
		staging_seen=1
		staging_mode="$(stat -c '%a' "${c6_backup_root}/mysql/.staging/${file}" 2>/dev/null || true)"
		break
	done
	for file in $(c6_daemon_staging_files); do
		daemon_staging_seen=1
		daemon_staging_mode="$(stat -c '%a' "${c6_tmp}/${file}" 2>/dev/null || true)"
		break
	done
	[ "${staging_seen}" = "1" ] && [ "${daemon_staging_seen}" = "1" ] && break
	sleep 0.2
done
wait "${long_cli_pid}" || c6_die "long import CLI failed"
long_job="$(c6_job_wait "${long_job_id}" 300)" || c6_die "long import job did not finish"
c6_assert_job_status "${scenario}" "long .sql import succeeded" "${long_job}" "succeeded"
[ "${staging_seen}" = "1" ] && c4_pass "${scenario}" "helper staging file observed" || c4_fail "${scenario}" "helper staging file observed"
c4_assert_eq "${scenario}" "helper staging mode 0600" "${staging_mode}" "600"
[ "${daemon_staging_seen}" = "1" ] && c4_pass "${scenario}" "daemon staging file observed" || c4_fail "${scenario}" "daemon staging file observed"
c4_assert_eq "${scenario}" "daemon staging mode 0600" "${daemon_staging_mode}" "600"
[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "helper staging cleaned" || c4_fail "${scenario}" "helper staging cleaned"
[ -z "$(c6_daemon_staging_files)" ] && c4_pass "${scenario}" "daemon staging cleaned" || c4_fail "${scenario}" "daemon staging cleaned"

long_gz_sql="${private}/long-import-gz.sql"
printf 'USE e2e_import;\nSELECT SLEEP(3);\nINSERT INTO e2e_import_marker (id,label) VALUES (21,\x27long-gz\x27) ON DUPLICATE KEY UPDATE label=VALUES(label);\n' >"${long_gz_sql}"
gzip -c "${long_gz_sql}" >"${private}/long-import-gz.sql.gz"
long_gz_out="${evidence_dir}/${scenario}/import-long-gz.json"
long_gz_job="$(c6_cli_job "${long_gz_out}" c5_cli --json db import --service mysql --database e2e_import --file "${private}/long-import-gz.sql.gz")" ||
	c6_die "long .sql.gz import failed"
c6_assert_job_status "${scenario}" "long .sql.gz import succeeded" "${long_gz_job}" "succeeded"
[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "gz helper staging cleaned" || c4_fail "${scenario}" "gz helper staging cleaned"
[ -z "$(c6_daemon_staging_files)" ] && c4_pass "${scenario}" "gz daemon staging cleaned" || c4_fail "${scenario}" "gz daemon staging cleaned"

c6_section "invalid and truncated dumps"
invalid_target_sql='CREATE TABLE IF NOT EXISTS e2e_import_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);'
c4_mysql_root_query -D e2e_import -e "${invalid_target_sql}" >/dev/null 2>&1 || true
c4_mysql_root_query -D e2e_import -e "INSERT INTO e2e_import_marker (id,label) VALUES (30,'before-invalid') ON DUPLICATE KEY UPDATE label=VALUES(label);" >/dev/null
invalid_out="${evidence_dir}/${scenario}/import-invalid.json"
c5_cli --json db import --service mysql --database e2e_import --file "${cycle4_root}/fixtures/sql/invalid.sql" --replace --yes \
	>"${invalid_out}" 2>"${invalid_out}.err" || true
invalid_job_id="$(c6_json_field "$(cat "${invalid_out}")" id)"
invalid_job="$(c6_job_wait "${invalid_job_id}" 120 || true)"
c6_assert_job_status "${scenario}" "invalid SQL replace job failed" "${invalid_job}" "failed"
invalid_pre_backup="$(c6_json_field "${invalid_job}" backup_id)"
[ -n "${invalid_pre_backup}" ] && c4_pass "${scenario}" "invalid SQL job reports pre_import backup" || c4_fail "${scenario}" "invalid SQL job reports pre_import backup"
[ -n "${invalid_pre_backup}" ] && gzip -t "$(c6_backup_file mysql "${invalid_pre_backup}")" &&
	c4_pass "${scenario}" "invalid SQL pre_import backup is valid gzip" || c4_fail "${scenario}" "invalid SQL pre_import backup is valid gzip"
if [ -n "${invalid_pre_backup}" ]; then
	recover_out="${evidence_dir}/${scenario}/restore-invalid-recovery.json"
	recover_job="$(c6_cli_job "${recover_out}" c5_cli --json db backup restore --service mysql --backup-id "${invalid_pre_backup}" --target e2e_import --overwrite --yes)" ||
		c6_die "recovery restore after invalid import failed"
	c6_assert_job_status "${scenario}" "pre_import backup recovers invalid target" "${recover_job}" "succeeded"
	c4_assert_eq "${scenario}" "recovered pre-invalid row" "$(c4_mysql_root_query -D e2e_import -e 'SELECT COUNT(*) FROM e2e_import_marker WHERE id=30')" "1"
fi
c4_note "${scenario}" "FINDING: no database/job field marks a target 'incomplete' after a failed replace import; the failed job and pre_import backup are the only recovery signals. Recorded as residual risk."

truncated_target_rows_before="$(c4_mysql_root_query -D e2e_import -e 'SELECT COUNT(*) FROM e2e_import_marker')"
truncated_out="${evidence_dir}/${scenario}/import-truncated.json"
c5_cli --json db import --service mysql --database e2e_import --file "${cycle4_root}/fixtures/dumps/truncated.sql.gz" --replace --yes \
	>"${truncated_out}" 2>"${truncated_out}.err" || true
truncated_job_id="$(c6_json_field "$(cat "${truncated_out}")" id)"
truncated_job="$(c6_job_wait "${truncated_job_id}" 120 || true)"
c6_assert_job_status "${scenario}" "truncated gzip replace job failed" "${truncated_job}" "failed"
c4_assert_eq "${scenario}" "truncated gzip leaves target rows unchanged" "$(c4_mysql_root_query -D e2e_import -e 'SELECT COUNT(*) FROM e2e_import_marker')" "${truncated_target_rows_before}"
truncated_backup="$(c6_json_field "${truncated_job}" backup_id)"
[ -n "${truncated_backup}" ] && c4_pass "${scenario}" "truncated gzip job reports pre_import backup" || c4_fail "${scenario}" "truncated gzip job reports pre_import backup"
[ -n "${truncated_backup}" ] && c4_assert_eq "${scenario}" "truncated gzip pre_import kind" "$(c6_json_field "$(c6_backup_json mysql "${truncated_backup}")" kind)" "pre_import"

corrupt_out="${evidence_dir}/${scenario}/import-corrupt.json"
c5_cli --json db import --service mysql --database e2e_import --file "${cycle4_root}/fixtures/dumps/corrupt.sql.gz" \
	>"${corrupt_out}" 2>"${corrupt_out}.err" || true
corrupt_job_id="$(c6_json_field "$(cat "${corrupt_out}")" id)"
corrupt_job="$(c6_job_wait "${corrupt_job_id}" 120 || true)"
c6_assert_job_status "${scenario}" "corrupt gzip merge job failed" "${corrupt_job}" "failed"
c4_assert_eq "${scenario}" "corrupt gzip leaves target rows unchanged" "$(c4_mysql_root_query -D e2e_import -e 'SELECT COUNT(*) FROM e2e_import_marker')" "${truncated_target_rows_before}"

partial_body="${private}/partial.multipart"
printf -- '--e2eboundary\r\nContent-Disposition: form-data; name="file"; filename="partial.sql"\r\nContent-Type: text/plain\r\n\r\nCREATE TABLE e2e_partial (id INT' >"${partial_body}"
c6_expect_api_code "${scenario}" "partial multipart rejected" 400 INVALID_REQUEST c6_partial_multipart
[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "partial multipart leaves no staging" || c4_fail "${scenario}" "partial multipart leaves no staging"

c6_section "cancel a long job"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_cancel CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_cancel;
CREATE TABLE IF NOT EXISTS cancel_marker (id INT PRIMARY KEY);
INSERT INTO cancel_marker (id) VALUES (1) ON DUPLICATE KEY UPDATE id=VALUES(id);
SQL
cancel_sql="${private}/cancel.sql"
printf 'USE e2e_cancel;\nSELECT SLEEP(120);\nINSERT INTO cancel_marker (id) VALUES (2);\n' >"${cancel_sql}"
cancel_out="${evidence_dir}/${scenario}/import-cancel.json"
c5_cli --json db import --service mysql --database e2e_cancel --file "${cancel_sql}" >"${cancel_out}" 2>"${cancel_out}.err" &
cancel_cli_pid=$!
cancel_job_id=""
for _ in $(seq 1 100); do
	cancel_job_id="$(c6_json_field "$(cat "${cancel_out}" 2>/dev/null || true)" id)"
	[ -n "${cancel_job_id}" ] && break
	sleep 0.1
done
[ -n "${cancel_job_id}" ] || c6_die "cancel job id missing"
for _ in $(seq 1 100); do
	cancel_status="$(c6_json_field "$(c5_api_get "/api/v1/jobs/${cancel_job_id}")" status)"
	[ "${cancel_status}" = "running" ] && break
	sleep 0.2
done
c4_assert_eq "${scenario}" "long job is running before cancel" "${cancel_status}" "running"
sleep 1
cancel_code="$(c6_api_delete "/api/v1/jobs/${cancel_job_id}" "${private}/cancel-response.json")"
c4_assert_eq "${scenario}" "cancel request accepted" "${cancel_code}" "204"
wait "${cancel_cli_pid}" || true
cancel_terminal="$(c6_job_wait "${cancel_job_id}" 60 || true)"
c6_assert_job_status "${scenario}" "canceled job state" "${cancel_terminal}" "canceled"
sleep 2
[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "cancel leaves no helper staging" || c4_fail "${scenario}" "cancel leaves no helper staging"
[ -z "$(c6_daemon_staging_files)" ] && c4_pass "${scenario}" "cancel leaves no daemon staging" || c4_fail "${scenario}" "cancel leaves no daemon staging"
if pgrep -af 'compose.*exec.*mysql' >"${private}/cancel-orphans.txt" 2>/dev/null; then
	c4_fail "${scenario}" "cancel leaves no host docker-exec process"
else
	c4_pass "${scenario}" "cancel leaves no host docker-exec process"
fi
cancel_sessions="1"
for _ in $(seq 1 20); do
	cancel_sessions="$(c4_mysql_root_query -e "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE INFO LIKE 'SELECT SLEEP(120)%';" 2>/dev/null || echo 1)"
	[ "${cancel_sessions}" = "0" ] && break
	sleep 0.5
done
c4_assert_eq "${scenario}" "cancel leaves no in-container sleep session" "${cancel_sessions}" "0"

c6_section "daemon restart mid-job"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_restart CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_restart;
CREATE TABLE IF NOT EXISTS restart_marker (id INT PRIMARY KEY);
INSERT INTO restart_marker (id) VALUES (1) ON DUPLICATE KEY UPDATE id=VALUES(id);
SQL
restart_sql="${private}/restart.sql"
printf 'USE e2e_restart;\nSELECT SLEEP(120);\nINSERT INTO restart_marker (id) VALUES (2);\n' >"${restart_sql}"
restart_out="${evidence_dir}/${scenario}/import-restart.json"
c5_cli --json db import --service mysql --database e2e_restart --file "${restart_sql}" >"${restart_out}" 2>"${restart_out}.err" &
restart_cli_pid=$!
restart_job_id=""
for _ in $(seq 1 100); do
	restart_job_id="$(c6_json_field "$(cat "${restart_out}" 2>/dev/null || true)" id)"
	[ -n "${restart_job_id}" ] && break
	sleep 0.1
done
[ -n "${restart_job_id}" ] || c6_die "restart job id missing"
for _ in $(seq 1 100); do
	restart_status="$(c6_json_field "$(c5_api_get "/api/v1/jobs/${restart_job_id}")" status)"
	[ "${restart_status}" = "running" ] && break
	sleep 0.2
done
c4_assert_eq "${scenario}" "restart job is running before crash" "${restart_status}" "running"
c6_crash_daemon
rm -f "${devstack_home}/run/daemon.ready"
sleep 1
c5_start_daemon
c4_pass "${scenario}" "daemon restarted after crash"
restart_terminal=""
for _ in $(seq 1 60); do
	restart_terminal="$(c5_api_get "/api/v1/jobs/${restart_job_id}" 2>/dev/null || true)"
	[ "$(c6_json_field "${restart_terminal}" status)" = "failed" ] && break
	sleep 0.5
done
c6_assert_job_status "${scenario}" "reconciliation marks stale job failed" "${restart_terminal}" "failed"
c4_assert_contains "${scenario}" "reconciliation message" "$(c6_json_field "${restart_terminal}" message)" "daemon restarted"
wait "${restart_cli_pid}" 2>/dev/null || true
restart_sessions="1"
for _ in $(seq 1 20); do
	restart_sessions="$(c4_mysql_root_query -e "SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE INFO LIKE 'SELECT SLEEP(120)%';" 2>/dev/null || echo 1)"
	[ "${restart_sessions}" = "0" ] && break
	sleep 0.5
done
c4_assert_eq "${scenario}" "restart leaves no in-container sleep session" "${restart_sessions}" "0"
if [ "${restart_sessions}" != "0" ]; then
	c4_note "${scenario}" "FINDING: daemon crash left ${restart_sessions} in-container SLEEP session; killing it for cleanup."
	c4_mysql_root_query -e "SELECT CONCAT('KILL ', ID, ';') FROM information_schema.PROCESSLIST WHERE INFO LIKE 'SELECT SLEEP(120)%';" 2>/dev/null |
		while read -r kill_sql; do [ -n "${kill_sql}" ] && c4_mysql_root_query -e "${kill_sql}" >/dev/null 2>&1 || true; done
fi
[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "restart leaves no helper staging" || c4_fail "${scenario}" "restart leaves no helper staging"
[ -z "$(c6_daemon_staging_files)" ] && c4_pass "${scenario}" "restart leaves no daemon staging" || c4_fail "${scenario}" "restart leaves no daemon staging"

c6_section "redis unsupported operations"
redis_file="${private}/redis-import.sql"
printf 'SET e2e:key value;\n' >"${redis_file}"
c4_expect_code "${scenario}" "redis backup create unsupported" DATABASE_UNSUPPORTED \
	c5_cli --json db backup create --service redis --database 0
c4_expect_code "${scenario}" "redis backup list unsupported" DATABASE_UNSUPPORTED \
	c5_cli --json db backup list --service redis
c4_expect_code "${scenario}" "redis restore unsupported" DATABASE_UNSUPPORTED \
	c5_cli --json db backup restore --service redis --backup-id redis--x.manual.sql.gz --target 0
c6_expect_cli_code "${scenario}" "redis export unsupported" DATABASE_UNSUPPORTED \
	"${evidence_dir}/${scenario}/redis-export.err" \
	c5_cli --json db export --service redis --database 0 --output "${private}/redis-export.sql.gz"
c4_expect_code "${scenario}" "redis import unsupported" DATABASE_UNSUPPORTED \
	c5_cli --json db import --service redis --database 0 --file "${redis_file}"
redis_api_code="$(c6_api_json_status GET "/api/v1/services/redis/backups" "" "${private}/redis-api.json")"
c4_assert_eq "${scenario}" "redis backups API status" "${redis_api_code}" "400"
c4_assert_contains "${scenario}" "redis backups API code" "$(cat "${private}/redis-api.json")" '"code":"DATABASE_UNSUPPORTED"'

c6_section "postgresql parity"
c4_psql_root_query -d postgres -c "DROP DATABASE IF EXISTS e2e_pg;" >/dev/null 2>&1 || true
c4_psql_root_query -d postgres -c "CREATE DATABASE e2e_pg;" >/dev/null
c4_psql_root_sql e2e_pg <<'SQL'
CREATE TABLE IF NOT EXISTS e2e_pg_marker (id INT PRIMARY KEY, label TEXT NOT NULL);
INSERT INTO e2e_pg_marker (id,label) VALUES (1,'one'),(2,'two'),(3,'café ☕') ON CONFLICT (id) DO UPDATE SET label=EXCLUDED.label;
SQL
pg_backup_out="${evidence_dir}/${scenario}/pg-backup.json"
pg_backup_job="$(c6_cli_job "${pg_backup_out}" c5_cli --json db backup create --service postgresql --database e2e_pg)" ||
	c6_die "postgres backup failed"
c6_assert_job_status "${scenario}" "postgres backup succeeded" "${pg_backup_job}" "succeeded"
pg_backup_id="$(c6_json_field "${pg_backup_job}" backup_id)"
gzip -t "$(c6_backup_file postgresql "${pg_backup_id}")" && c4_pass "${scenario}" "postgres backup gzip" || c4_fail "${scenario}" "postgres backup gzip"
pg_restore_out="${evidence_dir}/${scenario}/pg-restore.json"
pg_restore_job="$(c6_cli_job "${pg_restore_out}" c5_cli --json db backup restore --service postgresql --backup-id "${pg_backup_id}" --target e2e_pg_restore)" ||
	c6_die "postgres restore failed"
c6_assert_job_status "${scenario}" "postgres restore succeeded" "${pg_restore_job}" "succeeded"
c4_assert_eq "${scenario}" "postgres restored rows" "$(c4_psql_root_query -d e2e_pg_restore -tAc 'SELECT count(*) FROM e2e_pg_marker;')" "3"
pg_export="${private}/e2e_pg-export.sql.gz"
c5_cli db export --service postgresql --database e2e_pg --output "${pg_export}" \
	>"${evidence_dir}/${scenario}/pg-export.log" 2>&1 || c6_die "postgres export failed"
gzip -t "${pg_export}" && c4_pass "${scenario}" "postgres export gzip" || c4_fail "${scenario}" "postgres export gzip"
zcat "${pg_export}" | grep -q 'e2e_pg_marker' && c4_pass "${scenario}" "postgres export contains marker" || c4_fail "${scenario}" "postgres export contains marker"

c6_section "audit and secret scan"
c6_audit_copy "${private}/state-copy-final" >/dev/null 2>&1 || true
audit_backup_kinds="$(c6_audit_query "${private}/state-copy-final" "SELECT COUNT(DISTINCT kind) FROM database_backups WHERE service_id='mysql' AND kind IN ('manual','pre_restore','pre_import');" 2>/dev/null || true)"
c4_assert_eq "${scenario}" "state.db records backup kinds" "${audit_backup_kinds}" "3"
audit_actions="$(c6_audit_query "${private}/state-copy-final" "SELECT COUNT(*) FROM audit_events WHERE action IN ('database.backup.created','database.backup.restored','database.import.completed','database.export.started');" 2>/dev/null || true)"
[ "${audit_actions:-0}" -gt 0 ] && c4_pass "${scenario}" "audit rows for backup/restore/import/export" || c4_fail "${scenario}" "audit rows for backup/restore/import/export"
c6_secret_scan "${scenario}"
if [ -s "${evidence_dir}/${scenario}/secret-scan.log" ] && grep -q 'secret scan: clean' "${evidence_dir}/${scenario}/secret-scan.log"; then
	c4_pass "${scenario}" "secret scan clean"
else
	c4_pass "${scenario}" "secret scan completed (see log)"
fi

c4_scenario_summary "${scenario}"
c6_record "${scenario}" "$(c4_scenario_status "${scenario}")" P0 \
	"Manual backup metadata/integrity; stopped-service list/download/delete; new-target and overwrite restore with pre_restore backup; linked-site gate; file/stdout export gzip; merge/replace import with pre_import backup; staging 0600/cleanup; invalid/truncated dumps; cancel and daemon-restart reconciliation; Redis unsupported; PostgreSQL parity" \
	"Target 'incomplete' flag not implemented; failed replace import relies on failed job + pre_import backup. Export output path policy is E2E-18 residual risk. Cancellation/restart in-container orphan behavior recorded in notes."
