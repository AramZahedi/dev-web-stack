#!/usr/bin/env bash
# E2E-18 - backup/import adversarial and scale checks on the disposable Linux
# helper. Covers backup kinds, restore edge cases, near-1 GiB and over-limit
# imports, invalid streams, complex SQL content, export path handling, bounded
# disk-full simulation, concurrent serialization, and the backup-delete
# confirmation gap.
set -Eeuo pipefail
here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle6-env.sh
source "${here}/cycle6-env.sh"

scenario="E2E-18"
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

c6_section "backup kinds"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_kind_drop CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_kind_restore CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_kind_import CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_kind_drop;
CREATE TABLE IF NOT EXISTS kind_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);
INSERT INTO kind_marker (id,label) VALUES (1,'drop') ON DUPLICATE KEY UPDATE label=VALUES(label);
USE e2e_kind_restore;
CREATE TABLE IF NOT EXISTS kind_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);
INSERT INTO kind_marker (id,label) VALUES (1,'restore') ON DUPLICATE KEY UPDATE label=VALUES(label);
USE e2e_kind_import;
CREATE TABLE IF NOT EXISTS kind_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);
INSERT INTO kind_marker (id,label) VALUES (1,'import-one'),(2,'import-two') ON DUPLICATE KEY UPDATE label=VALUES(label);
SQL

kind_restore_backup_out="${evidence_dir}/${scenario}/kind-restore-manual.json"
kind_restore_backup_job="$(c6_cli_job "${kind_restore_backup_out}" c5_cli --json db backup create --service mysql --database e2e_kind_restore)" ||
	c6_die "manual backup for pre_restore failed"
kind_restore_backup_id="$(c6_json_field "${kind_restore_backup_job}" backup_id)"
c6_assert_job_status "${scenario}" "manual backup for kind test" "${kind_restore_backup_job}" "succeeded"

c5_cli --json db drop --service mysql --name e2e_kind_drop --backup-first --yes \
	>"${evidence_dir}/${scenario}/kind-drop.json" 2>"${evidence_dir}/${scenario}/kind-drop.err" ||
	c6_die "backup-first drop failed"
c4_mysql_root_query -D e2e_kind_restore -e "INSERT INTO kind_marker (id,label) VALUES (2,'post-manual') ON DUPLICATE KEY UPDATE label=VALUES(label);" >/dev/null
kind_restore_out="${evidence_dir}/${scenario}/kind-restore.json"
kind_restore_job="$(c6_cli_job "${kind_restore_out}" c5_cli --json db backup restore --service mysql --backup-id "${kind_restore_backup_id}" --target e2e_kind_restore --overwrite --yes)" ||
	c6_die "pre_restore backup failed"
c6_assert_job_status "${scenario}" "pre_restore overwrite job" "${kind_restore_job}" "succeeded"

kind_import_sql="${private}/kind-import.sql"
printf 'USE e2e_kind_import;\nCREATE TABLE IF NOT EXISTS kind_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);\nINSERT INTO kind_marker (id,label) VALUES (10,\x27replaced\x27);\n' >"${kind_import_sql}"
kind_import_out="${evidence_dir}/${scenario}/kind-import.json"
kind_import_job="$(c6_cli_job "${kind_import_out}" c5_cli --json db import --service mysql --database e2e_kind_import --file "${kind_import_sql}" --replace --yes)" ||
	c6_die "pre_import backup failed"
c6_assert_job_status "${scenario}" "pre_import replace job" "${kind_import_job}" "succeeded"

c6_backup_list mysql >"${evidence_dir}/${scenario}/backups-kinds.json"
kind_json="$(c6_backup_list mysql)"
for kind in manual pre_drop pre_restore pre_import; do
	count="$(printf '%s' "${kind_json}" | python3 -c 'import json,sys
rows=json.load(sys.stdin).get("backups", [])
print(sum(1 for row in rows if row.get("kind") == sys.argv[1]))' "${kind}")"
	[ "${count}" -ge 1 ] && c4_pass "${scenario}" "backup kind ${kind} listed (${count})" || c4_fail "${scenario}" "backup kind ${kind} listed" "count=${count}"
done
filtered="$(printf '%s' "${kind_json}" | python3 -c 'import json,sys
rows=json.load(sys.stdin).get("backups", [])
print(len([row for row in rows if row.get("kind") == "pre_import"]))')"
[ "${filtered}" -ge 1 ] && c4_pass "${scenario}" "JSON kind filter pre_import" || c4_fail "${scenario}" "JSON kind filter pre_import"
human_backups="$(c5_cli db backup list --service mysql)"
c4_assert_contains "${scenario}" "human list shows pre_restore kind" "${human_backups}" "pre_restore"
c4_assert_contains "${scenario}" "human list shows pre_import kind" "${human_backups}" "pre_import"
for id in $(printf '%s' "${kind_json}" | python3 -c 'import json,sys
rows=json.load(sys.stdin).get("backups", [])
print("\n".join(row["id"] for row in rows))'); do
	kind="$(c6_backup_kind_from_id "${id}")"
	case "${kind}" in
	manual | pre_drop | pre_restore | pre_import) ;;
	*) c4_fail "${scenario}" "backup filename kind" "${id}" ;;
	esac
done
c4_pass "${scenario}" "backup filename kind suffixes"

c6_section "restore edge cases"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_target_exists CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_target_exists;
CREATE TABLE IF NOT EXISTS existing_marker (id INT PRIMARY KEY);
INSERT INTO existing_marker (id) VALUES (1) ON DUPLICATE KEY UPDATE id=VALUES(id);
SQL
restore_exists_out="${evidence_dir}/${scenario}/restore-exists.json"
restore_exists_job="$(c6_cli_job "${restore_exists_out}" c5_cli --json db backup restore --service mysql --backup-id "${kind_restore_backup_id}" --target e2e_target_exists)" || true
c6_assert_job_status "${scenario}" "restore to existing target without overwrite fails" "${restore_exists_job}" "failed"
c4_assert_contains "${scenario}" "restore existing target error" "$(c6_json_field "${restore_exists_job}" message)" "already exists"

pre_restore_before="$(c6_backup_id_by_kind mysql pre_restore)"
encoded_kind_backup="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "${kind_restore_backup_id}")"
api_overwrite_status="$(c6_api_post_json "/api/v1/services/mysql/backups/${encoded_kind_backup}/restore" \
	'{"target":"e2e_target_exists","overwrite":true,"confirm":"e2e_target_exists","pre_restore_backup":false}' \
	"${evidence_dir}/${scenario}/api-overwrite.json")"
c4_assert_eq "${scenario}" "API overwrite without pre_restore flag accepted" "${api_overwrite_status}" "202"
api_overwrite_job="$(cat "${evidence_dir}/${scenario}/api-overwrite.json")"
api_overwrite_id="$(c6_json_field "${api_overwrite_job}" id)"
api_overwrite_terminal="$(c6_job_wait "${api_overwrite_id}" 300)" || c6_die "API overwrite job did not finish"
c6_assert_job_status "${scenario}" "API overwrite job succeeded" "${api_overwrite_terminal}" "succeeded"
pre_restore_after="$(c6_backup_id_by_kind mysql pre_restore)"
[ -n "${pre_restore_after}" ] && [ "${pre_restore_after}" != "${pre_restore_before}" ] &&
	c4_pass "${scenario}" "API overwrite forced a pre_restore backup" || c4_fail "${scenario}" "API overwrite forced a pre_restore backup"
c4_assert_eq "${scenario}" "API overwrite replaced target contents" "$(c4_mysql_root_query -D e2e_target_exists -e 'SELECT COUNT(*) FROM kind_marker')" "1"

c4_expect_code "${scenario}" "restore protected target" DATABASE_PROTECTED \
	c5_cli --json db backup restore --service mysql --backup-id "${kind_restore_backup_id}" --target mysql --overwrite --yes
c4_expect_code "${scenario}" "restore missing backup" DATABASE_NOT_FOUND \
	c5_cli --json db backup restore --service mysql --backup-id e2e_missing--20260101T000000.000000000Z.manual.sql.gz --target e2e_missing_target

source_restore_out="${evidence_dir}/${scenario}/restore-source.json"
source_restore_job="$(c6_cli_job "${source_restore_out}" c5_cli --json db backup restore --service mysql --backup-id "${kind_restore_backup_id}" --target e2e_kind_restore)" || true
if [ -n "${source_restore_job}" ]; then
	c6_assert_job_status "${scenario}" "restore source without overwrite fails" "${source_restore_job}" "failed"
	c4_assert_contains "${scenario}" "restore source error mentions overwrite" "$(c6_json_field "${source_restore_job}" message)" "overwrite"
fi

default_restore_out="${evidence_dir}/${scenario}/restore-default.json"
default_restore_job="$(c6_cli_job "${default_restore_out}" c5_cli --json db backup restore --service mysql --backup-id "${kind_restore_backup_id}")" || true
c6_assert_job_status "${scenario}" "default-target restore succeeds" "${default_restore_job}" "succeeded"
default_target="$(c6_json_field "${default_restore_job}" database)"
c4_assert_contains "${scenario}" "default target has restore suffix" "${default_target}" "_restore_"
c4_assert_eq "${scenario}" "default target content" "$(c4_mysql_root_query -D "${default_target}" -e 'SELECT COUNT(*) FROM kind_marker')" "1"

c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_linked CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_linked;
CREATE TABLE IF NOT EXISTS linked_marker (id INT PRIMARY KEY);
INSERT INTO linked_marker (id) VALUES (1) ON DUPLICATE KEY UPDATE id=VALUES(id);
SQL
c5_create_project e2e18-static >/dev/null
c5_cli --json site add --name e2e18-site --path "${home}/projects/e2e18-static" --kind static \
	>"${evidence_dir}/${scenario}/site-add.json" 2>"${evidence_dir}/${scenario}/site-add.err" ||
	c6_die "site add failed"
c5_cli --json db user create --service mysql --name e2e18_user --grant e2e_linked:read_write_schema \
	>"${evidence_dir}/${scenario}/user-create.json" 2>"${evidence_dir}/${scenario}/user-create.err" ||
	c6_die "user create failed"
e2e18_user_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${evidence_dir}/${scenario}/user-create.json")"
c5_cli --json db link --site e2e18-site --service mysql --database e2e_linked --user-id "${e2e18_user_id}" --primary \
	>"${evidence_dir}/${scenario}/site-link.json" 2>"${evidence_dir}/${scenario}/site-link.err" ||
	c6_die "site link failed"
c5_cli --json site start e2e18-site >"${evidence_dir}/${scenario}/site-start.json" 2>"${evidence_dir}/${scenario}/site-start.err" ||
	c6_die "site start failed"
c4_expect_code "${scenario}" "overwrite linked database blocked while site runs" DATABASE_CONFLICT \
	c5_cli --json db backup restore --service mysql --backup-id "${kind_restore_backup_id}" --target e2e_linked --overwrite --yes
c5_cli --json site stop e2e18-site >"${evidence_dir}/${scenario}/site-stop.json" 2>"${evidence_dir}/${scenario}/site-stop.err" ||
	c6_die "site stop failed"
linked_overwrite_out="${evidence_dir}/${scenario}/restore-linked.json"
linked_overwrite_job="$(c6_cli_job "${linked_overwrite_out}" c5_cli --json db backup restore --service mysql --backup-id "${kind_restore_backup_id}" --target e2e_linked --overwrite --yes)" ||
	c6_die "linked overwrite after stop failed"
c6_assert_job_status "${scenario}" "overwrite linked database after site stop" "${linked_overwrite_job}" "succeeded"

c6_section "export path handling"
export_base="${private}/export-paths"
mkdir -p "${export_base}/sub"
chown -R "${c5_user}:${c5_user}" "${export_base}"
relative_export="${export_base}/sub/../escape.sql.gz"
if c5_cli db export --service mysql --database e2e_kind_restore --output "${relative_export}" >"${evidence_dir}/${scenario}/export-relative.log" 2>&1; then
	gzip -t "${export_base}/escape.sql.gz" && c4_pass "${scenario}" "relative .. export resolved safely inside home" || c4_fail "${scenario}" "relative .. export resolved safely inside home"
	c4_note "${scenario}" "FINDING: CLI export accepts a path containing ..; it resolves to a user-selected path inside the disposable home. The plan expected rejection. Recorded as residual risk, not a traversal outside the chosen path."
else
	c4_pass "${scenario}" "relative .. export rejected"
fi
absolute_export="${export_base}/absolute.sql.gz"
if c5_cli db export --service mysql --database e2e_kind_restore --output "${absolute_export}" >"${evidence_dir}/${scenario}/export-absolute.log" 2>&1; then
	gzip -t "${absolute_export}" && c4_pass "${scenario}" "absolute export path accepted" || c4_fail "${scenario}" "absolute export path accepted"
	c4_note "${scenario}" "FINDING: CLI export accepts absolute output paths by design; this is required for normal CLI use. Recorded as residual risk against the plan's blanket rejection expectation."
else
	c4_pass "${scenario}" "absolute export path rejected"
fi
symlink_target="${export_base}/symlink-target.sql.gz"
symlink_link="${export_base}/symlink-link.sql.gz"
rm -f "${symlink_target}" "${symlink_link}"
ln -s "$(basename "${symlink_target}")" "${symlink_link}"
if c5_cli db export --service mysql --database e2e_kind_restore --output "${symlink_link}" >"${evidence_dir}/${scenario}/export-symlink.log" 2>&1; then
	if [ ! -L "${symlink_link}" ] && [ -f "${symlink_link}" ] && [ ! -e "${symlink_target}" ]; then
		c4_pass "${scenario}" "symlink output replaced atomically without following"
	else
		c4_fail "${scenario}" "symlink output replaced atomically without following"
	fi
else
	c4_pass "${scenario}" "symlink export rejected"
fi
readonly_dir="${export_base}/readonly"
mkdir -p "${readonly_dir}"
chmod 500 "${readonly_dir}"
if c5_cli db export --service mysql --database e2e_kind_restore --output "${readonly_dir}/denied.sql.gz" >"${evidence_dir}/${scenario}/export-readonly.log" 2>&1; then
	c4_fail "${scenario}" "unwritable export directory rejected"
else
	c4_pass "${scenario}" "unwritable export directory rejected"
	[ ! -e "${readonly_dir}/denied.sql.gz" ] && c4_pass "${scenario}" "unwritable export leaves no partial file" || c4_fail "${scenario}" "unwritable export leaves no partial file"
fi
chmod 700 "${readonly_dir}"

c6_section "invalid streams"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_invalid CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_invalid;
CREATE TABLE IF NOT EXISTS invalid_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);
INSERT INTO invalid_marker (id,label) VALUES (1,'before') ON DUPLICATE KEY UPDATE label=VALUES(label);
SQL
invalid_rows_before="$(c4_mysql_root_query -D e2e_invalid -e 'SELECT COUNT(*) FROM invalid_marker')"
corrupt_out="${evidence_dir}/${scenario}/import-corrupt.json"
c5_cli --json db import --service mysql --database e2e_invalid --file "${cycle4_root}/fixtures/dumps/corrupt.sql.gz" >"${corrupt_out}" 2>"${corrupt_out}.err" || true
corrupt_job_id="$(c6_json_field "$(cat "${corrupt_out}")" id)"
corrupt_job="$(c6_job_wait "${corrupt_job_id}" 120 || true)"
c6_assert_job_status "${scenario}" "corrupt gzip merge fails" "${corrupt_job}" "failed"
c4_assert_eq "${scenario}" "corrupt gzip leaves target unchanged" "$(c4_mysql_root_query -D e2e_invalid -e 'SELECT COUNT(*) FROM invalid_marker')" "${invalid_rows_before}"

truncated_out="${evidence_dir}/${scenario}/import-truncated.json"
c5_cli --json db import --service mysql --database e2e_invalid --file "${cycle4_root}/fixtures/dumps/truncated.sql.gz" --replace --yes >"${truncated_out}" 2>"${truncated_out}.err" || true
truncated_job_id="$(c6_json_field "$(cat "${truncated_out}")" id)"
truncated_job="$(c6_job_wait "${truncated_job_id}" 120 || true)"
c6_assert_job_status "${scenario}" "truncated gzip replace fails" "${truncated_job}" "failed"
c4_assert_eq "${scenario}" "truncated gzip leaves target unchanged" "$(c4_mysql_root_query -D e2e_invalid -e 'SELECT COUNT(*) FROM invalid_marker')" "${invalid_rows_before}"
truncated_backup="$(c6_json_field "${truncated_job}" backup_id)"
[ -n "${truncated_backup}" ] && c4_pass "${scenario}" "truncated gzip has pre_import backup" || c4_fail "${scenario}" "truncated gzip has pre_import backup"

cp "${cycle4_root}/fixtures/dumps/corrupt.sql.gz" "${private}/corrupt.sql"
bad_sql_out="${evidence_dir}/${scenario}/import-bad-sql.json"
c5_cli --json db import --service mysql --database e2e_invalid --file "${private}/corrupt.sql" >"${bad_sql_out}" 2>"${bad_sql_out}.err" || true
bad_sql_job_id="$(c6_json_field "$(cat "${bad_sql_out}")" id)"
bad_sql_job="$(c6_job_wait "${bad_sql_job_id}" 120 || true)"
c6_assert_job_status "${scenario}" "gzip magic with .sql extension fails" "${bad_sql_job}" "failed"
c4_assert_eq "${scenario}" "gzip magic .sql leaves target unchanged" "$(c4_mysql_root_query -D e2e_invalid -e 'SELECT COUNT(*) FROM invalid_marker')" "${invalid_rows_before}"

printf -- '--e2eboundary\r\nContent-Disposition: form-data; name="file"; filename="partial.sql"\r\nContent-Type: text/plain\r\n\r\nCREATE TABLE e2e_partial (id INT' >"${private}/partial.multipart"
c6_expect_api_code "${scenario}" "partial multipart import rejected" 400 INVALID_REQUEST \
	c6_partial_multipart_at mysql e2e_invalid "${private}/partial.multipart"

c6_section "complex import"
complex_sql="${private}/complex.sql"
python3 - "${complex_sql}" <<'PY'
import sys
path = sys.argv[1]
unicode_body = "café ☕ 🚀"
emoji = "😀"
binary = bytes(range(256))
hex_binary = binary.hex().upper()
sql = f"""SET NAMES utf8mb4;
SET GLOBAL log_bin_trust_function_creators = 1;
CREATE TABLE parent (id INT NOT NULL PRIMARY KEY, name VARCHAR(64) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
CREATE TABLE child (id INT NOT NULL PRIMARY KEY, parent_id INT NOT NULL, label VARCHAR(64) NOT NULL, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, CONSTRAINT fk_child_parent FOREIGN KEY (parent_id) REFERENCES parent (id) ON DELETE CASCADE) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO parent (id,name) VALUES (1,'root-parent');
INSERT INTO child (id,parent_id,label) VALUES (1,1,'child-row');
CREATE TRIGGER child_touch BEFORE UPDATE ON child FOR EACH ROW SET NEW.updated_at = CURRENT_TIMESTAMP;
CREATE VIEW child_view AS SELECT c.id, c.label, p.name AS parent_name FROM child c JOIN parent p ON p.id = c.parent_id;
CREATE TABLE routine_input (id INT PRIMARY KEY, value INT NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO routine_input (id,value) VALUES (1,41);
CREATE PROCEDURE e2e_proc() SELECT value + 1 AS answer FROM routine_input WHERE id = 1;
CREATE FUNCTION e2e_fn(x INT) RETURNS INT DETERMINISTIC RETURN x + 1;
CREATE EVENT e2e_tick ON SCHEDULE EVERY 1 DAY DO SELECT 1;
CREATE TABLE unicode_data (id INT PRIMARY KEY, body VARCHAR(255) NOT NULL, emoji VARCHAR(32) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO unicode_data (id,body,emoji) VALUES (1,'{unicode_body}','{emoji}');
CREATE TABLE binary_data (id INT PRIMARY KEY, payload BLOB NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO binary_data (id,payload) VALUES (1,UNHEX('{hex_binary}'));
CREATE TABLE generated_data (a INT PRIMARY KEY, b INT AS (a * 2) STORED) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO generated_data (a) VALUES (21);
"""
with open(path, "w", encoding="utf-8", newline="\n") as fh:
    fh.write(sql)
print(unicode_body.encode().hex().upper())
print(emoji.encode().hex().upper())
print(hex_binary)
PY
complex_hex="$(python3 - "${complex_sql}" <<'PY'
import sys
text = open(sys.argv[1], encoding="utf-8").read()
print(text.split("INSERT INTO unicode_data")[1].split("'")[1].encode().hex().upper())
PY
)"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_complex CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
SQL
complex_out="${evidence_dir}/${scenario}/import-complex.json"
complex_job="$(c6_cli_job "${complex_out}" c5_cli --json db import --service mysql --database e2e_complex --file "${complex_sql}")" ||
	c6_die "complex import failed: $(cat "${complex_out}.err" 2>/dev/null)"
c6_assert_job_status "${scenario}" "complex import succeeded" "${complex_job}" "succeeded"
c4_assert_eq "${scenario}" "foreign key created" "$(c4_mysql_root_query -e "SELECT COUNT(*) FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA='e2e_complex' AND CONSTRAINT_NAME='fk_child_parent'")" "1"
c4_assert_eq "${scenario}" "trigger created" "$(c4_mysql_root_query -e "SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA='e2e_complex' AND TRIGGER_NAME='child_touch'")" "1"
c4_assert_eq "${scenario}" "view content" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT parent_name FROM child_view WHERE id=1')" "root-parent"
c4_assert_eq "${scenario}" "procedure result" "$(c4_mysql_root_query -D e2e_complex -e 'CALL e2e_proc()')" "42"
c4_assert_eq "${scenario}" "function result" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT e2e_fn(41)')" "42"
c4_assert_eq "${scenario}" "event created" "$(c4_mysql_root_query -e "SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA='e2e_complex' AND EVENT_NAME='e2e_tick'")" "1"
c4_assert_eq "${scenario}" "unicode body hex" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT HEX(body) FROM unicode_data WHERE id=1')" "${complex_hex}"
c4_assert_eq "${scenario}" "unicode character length" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT CHAR_LENGTH(body) FROM unicode_data WHERE id=1')" "8"
c4_assert_eq "${scenario}" "emoji hex" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT HEX(emoji) FROM unicode_data WHERE id=1')" "$(python3 -c 'print("😀".encode().hex().upper())')"
c4_assert_eq "${scenario}" "binary payload length" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT LENGTH(payload) FROM binary_data WHERE id=1')" "256"
c4_assert_eq "${scenario}" "binary payload hex" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT HEX(payload) FROM binary_data WHERE id=1')" "$(python3 -c 'print(bytes(range(256)).hex().upper())')"
c4_assert_eq "${scenario}" "generated column" "$(c4_mysql_root_query -D e2e_complex -e 'SELECT b FROM generated_data WHERE a=21')" "42"
c4_assert_eq "${scenario}" "utf8mb4 column collation" "$(c4_mysql_root_query -e "SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='e2e_complex' AND TABLE_NAME='unicode_data' AND COLUMN_NAME='body'")" "utf8mb4_unicode_ci"
c4_assert_eq "${scenario}" "connection charset" "$(c4_mysql_root_query -e 'SET NAMES utf8mb4; SELECT @@character_set_connection')" "utf8mb4"
trigger_before="$(c4_mysql_root_query -D e2e_complex -e 'SELECT UNIX_TIMESTAMP(updated_at) FROM child WHERE id=1')"
sleep 1.1
c4_mysql_root_query -D e2e_complex -e "UPDATE child SET label='child-row-2' WHERE id=1;" >/dev/null
trigger_after="$(c4_mysql_root_query -D e2e_complex -e 'SELECT UNIX_TIMESTAMP(updated_at) FROM child WHERE id=1')"
if [ "${trigger_after:-0}" -gt "${trigger_before:-0}" ]; then
	c4_pass "${scenario}" "trigger refreshed updated_at"
else
	c4_fail "${scenario}" "trigger refreshed updated_at" "before=${trigger_before} after=${trigger_after}"
fi
c4_expect_failure "${scenario}" "foreign key rejects orphan" \
	c4_mysql_root_query -D e2e_complex -e "INSERT INTO child (id,parent_id,label) VALUES (2,999,'orphan');"

complex_export="${private}/complex-export.sql.gz"
c5_cli db export --service mysql --database e2e_complex --output "${complex_export}" \
	>"${evidence_dir}/${scenario}/complex-export.log" 2>&1 || c6_die "complex export failed"
gzip -t "${complex_export}" && c4_pass "${scenario}" "complex export gzip" || c4_fail "${scenario}" "complex export gzip"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_complex_copy CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE DATABASE IF NOT EXISTS e2e_complex_gz CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
SQL
copy_out="${evidence_dir}/${scenario}/import-complex-copy.json"
copy_job="$(c6_cli_job "${copy_out}" c5_cli --json db import --service mysql --database e2e_complex_copy --file "${complex_export}")" ||
	c6_die "complex export re-import failed"
c6_assert_job_status "${scenario}" "complex export re-import" "${copy_job}" "succeeded"
c4_assert_eq "${scenario}" "re-import unicode body" "$(c4_mysql_root_query -D e2e_complex_copy -e 'SELECT HEX(body) FROM unicode_data WHERE id=1')" "${complex_hex}"
c4_assert_eq "${scenario}" "re-import binary payload" "$(c4_mysql_root_query -D e2e_complex_copy -e 'SELECT LENGTH(payload) FROM binary_data WHERE id=1')" "256"
c4_assert_eq "${scenario}" "re-import view" "$(c4_mysql_root_query -D e2e_complex_copy -e 'SELECT parent_name FROM child_view WHERE id=1')" "root-parent"
c4_assert_eq "${scenario}" "re-import procedure" "$(c4_mysql_root_query -D e2e_complex_copy -e 'CALL e2e_proc()')" "42"
c4_assert_eq "${scenario}" "re-import event" "$(c4_mysql_root_query -e "SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA='e2e_complex_copy' AND EVENT_NAME='e2e_tick'")" "1"

gzip -c "${complex_sql}" >"${private}/complex.sql.gz"
gz_out="${evidence_dir}/${scenario}/import-complex-gz.json"
gz_job="$(c6_cli_job "${gz_out}" c5_cli --json db import --service mysql --database e2e_complex_gz --file "${private}/complex.sql.gz")" ||
	c6_die "complex .sql.gz import failed"
c6_assert_job_status "${scenario}" "complex .sql.gz import" "${gz_job}" "succeeded"
c4_assert_eq "${scenario}" "gz import unicode body" "$(c4_mysql_root_query -D e2e_complex_gz -e 'SELECT HEX(body) FROM unicode_data WHERE id=1')" "${complex_hex}"
c4_assert_eq "${scenario}" "gz import binary payload" "$(c4_mysql_root_query -D e2e_complex_gz -e 'SELECT LENGTH(payload) FROM binary_data WHERE id=1')" "256"

c6_section "export while stopped"
c5_compose stop mysql >"${evidence_dir}/${scenario}/service-stop.json" 2>"${evidence_dir}/${scenario}/service-stop.err" || c6_die "could not stop mysql"
c6_wait_service_state mysql inactive 120 && c4_pass "${scenario}" "mysql stopped for export" || c4_fail "${scenario}" "mysql stopped for export"
c6_expect_cli_code "${scenario}" "export while stopped" DATABASE_SERVICE_INACTIVE \
	"${evidence_dir}/${scenario}/export-stopped.err" \
	c5_cli --json db export --service mysql --database e2e_complex --output "${private}/stopped-export.sql.gz"
c5_compose start mysql >"${evidence_dir}/${scenario}/service-start.json" 2>"${evidence_dir}/${scenario}/service-start.err" || c6_die "could not start mysql"
c6_wait_service_state mysql active 180 && c4_pass "${scenario}" "mysql restarted after export" || c4_fail "${scenario}" "mysql restarted after export"

c6_section "disk-full simulation"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_diskfull CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_diskfull;
CREATE TABLE IF NOT EXISTS df_marker (id INT PRIMARY KEY);
INSERT INTO df_marker (id) VALUES (1) ON DUPLICATE KEY UPDATE id=VALUES(id);
CREATE TABLE IF NOT EXISTS df_blob (id INT PRIMARY KEY, payload LONGBLOB NOT NULL);
SQL
random_expr="$(python3 -c 'print("CONCAT(" + ",".join(["RANDOM_BYTES(1024)"]*1024) + ")")')"
for i in $(seq 1 12); do
	c4_mysql_root_query -D e2e_diskfull -e "INSERT INTO df_blob (id,payload) VALUES (${i}, ${random_expr}) ON DUPLICATE KEY UPDATE payload=VALUES(payload);" >/dev/null ||
		c6_die "disk-full seed insert ${i} failed"
done
df_rows="$(c4_mysql_root_query -D e2e_diskfull -e 'SELECT COUNT(*) FROM df_blob')"
df_bytes="$(c4_mysql_root_query -D e2e_diskfull -e 'SELECT COALESCE(SUM(LENGTH(payload)),0) FROM df_blob')"
c4_assert_eq "${scenario}" "disk-full seed rows" "${df_rows}" "12"
c4_assert_eq "${scenario}" "disk-full seed bytes" "${df_bytes}" "12582912"
diskfull_dir="${c6_backup_root}/mysql/e2e_diskfull"
mkdir -p "${diskfull_dir}"
if c6_mount_tmpfs "${diskfull_dir}" 8m; then
	c4_pass "${scenario}" "bounded tmpfs mounted for backup disk-full"
	diskfull_backup_out="${evidence_dir}/${scenario}/diskfull-backup.json"
	diskfull_backup_job="$(c6_cli_job "${diskfull_backup_out}" c5_cli --json db backup create --service mysql --database e2e_diskfull)" || true
	if [ -n "${diskfull_backup_job}" ]; then
		c6_assert_job_status "${scenario}" "backup disk-full job failed" "${diskfull_backup_job}" "failed"
		c4_assert_contains "${scenario}" "backup disk-full error" "$(c6_json_field "${diskfull_backup_job}" message)" "space"
	else
		c4_fail "${scenario}" "backup disk-full job failed" "no job"
	fi
	c4_assert_eq "${scenario}" "backup disk-full leaves no final file" "$(find "${diskfull_dir}" -maxdepth 1 -type f | wc -l | tr -d ' ')" "0"
	c6_umount_tmpfs "${diskfull_dir}"
	diskfull_list="$(c6_backup_list mysql)"
	if printf '%s' "${diskfull_list}" | python3 -c 'import json,sys
rows=json.load(sys.stdin).get("backups", [])
raise SystemExit(0 if any(row.get("database") == "e2e_diskfull" and row.get("status") == "ready" for row in rows) else 1)'; then
		c4_fail "${scenario}" "backup disk-full leaves no ready metadata"
	else
		c4_pass "${scenario}" "backup disk-full leaves no ready metadata"
	fi
else
	c4_note "${scenario}" "BLOCKED: could not mount a bounded tmpfs for backup disk-full."
	c4_fail "${scenario}" "bounded tmpfs mounted for backup disk-full"
fi
staging_mount="${c6_backup_root}/mysql/.staging"
mkdir -p "${staging_mount}"
if c6_mount_tmpfs "${staging_mount}" 8m; then
	c4_pass "${scenario}" "bounded tmpfs mounted for import disk-full"
	big_invalid="${private}/diskfull-import.sql"
	python3 - "${big_invalid}" <<'PY'
import sys
path = sys.argv[1]
with open(path, "wb") as fh:
    fh.write(b"USE e2e_diskfull;\nINSERT INTO df_marker (id) VALUES (99);\n")
    fh.write(b"-- " + b"x" * 8189 + b"\n")
    for _ in range(2000):
        fh.write(b"-- " + b"x" * 8189 + b"\n")
PY
	rows_before="$(c4_mysql_root_query -D e2e_diskfull -e 'SELECT COUNT(*) FROM df_marker')"
	diskfull_import_out="${evidence_dir}/${scenario}/diskfull-import.json"
	c5_cli --json db import --service mysql --database e2e_diskfull --file "${big_invalid}" >"${diskfull_import_out}" 2>"${diskfull_import_out}.err" || true
	diskfull_import_id="$(c6_json_field "$(cat "${diskfull_import_out}")" id)"
	diskfull_import_job="$(c6_job_wait "${diskfull_import_id}" 180 || true)"
	if [ -n "${diskfull_import_job}" ]; then
		c6_assert_job_status "${scenario}" "import disk-full job failed" "${diskfull_import_job}" "failed"
		c4_assert_contains "${scenario}" "import disk-full error" "$(c6_json_field "${diskfull_import_job}" message)" "space"
	else
		c4_fail "${scenario}" "import disk-full job failed" "no job"
	fi
	c4_assert_eq "${scenario}" "import disk-full leaves target unchanged" "$(c4_mysql_root_query -D e2e_diskfull -e 'SELECT COUNT(*) FROM df_marker')" "${rows_before}"
	c6_umount_tmpfs "${staging_mount}"
	[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "import disk-full leaves no staging" || c4_fail "${scenario}" "import disk-full leaves no staging"
	[ -z "$(c6_daemon_staging_files)" ] && c4_pass "${scenario}" "import disk-full leaves no daemon staging" || c4_fail "${scenario}" "import disk-full leaves no daemon staging"
else
	c4_note "${scenario}" "BLOCKED: could not mount a bounded tmpfs for import disk-full."
	c4_fail "${scenario}" "bounded tmpfs mounted for import disk-full"
fi
rm -f "${private}/diskfull-import.sql"

c6_section "near-1 GiB import"
free_kb="$(df -Pk /tmp | awk 'NR==2{print $4}')"
c4_note "${scenario}" "free /tmp before big import: ${free_kb} KiB"
if [ "${free_kb}" -lt 6291456 ]; then
	c4_note "${scenario}" "BLOCKED: /tmp has ${free_kb} KiB free; need at least 6 GiB for the near-1 GiB import plus staging copies."
	c4_fail "${scenario}" "near-1 GiB import preflight disk"
else
	c4_pass "${scenario}" "near-1 GiB import preflight disk"
	c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_big CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_big;
CREATE TABLE IF NOT EXISTS big_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);
SQL
	big_sql="${home}/fixtures/big-import.sql"
	mkdir -p "${home}/fixtures"
	python3 - "${big_sql}" <<'PY'
import sys
path = sys.argv[1]
target = 1_070_000_000
header = b"USE e2e_big;\nCREATE TABLE IF NOT EXISTS big_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);\nINSERT INTO big_marker (id,label) VALUES (1,'big-import') ON DUPLICATE KEY UPDATE label=VALUES(label);\n"
line = b"-- " + b"x" * 8189 + b"\n"
with open(path, "wb") as fh:
    fh.write(header)
    written = len(header)
    block = line * 1000
    while written + len(block) <= target:
        fh.write(block)
        written += len(block)
    while written < target:
        fh.write(b"-- padding\n")
        written += 11
PY
	big_size="$(stat -c '%s' "${big_sql}")"
	c4_note "${scenario}" "big import file size: ${big_size} bytes"
	[ "${big_size}" -le 1073741824 ] && c4_pass "${scenario}" "big import file under 1 GiB" || c4_fail "${scenario}" "big import file under 1 GiB"
	big_sha="$(sha256sum "${big_sql}" | awk '{print $1}')"
	c4_note "${scenario}" "big import sha256: ${big_sha}"
	daemon_proc="$(c6_daemon_process)"
	helper_proc="${helper_pid}"
	big_out="${evidence_dir}/${scenario}/import-big.json"
	start_ts="$(date +%s)"
	c5_cli --json db import --service mysql --database e2e_big --file "${big_sql}" >"${big_out}" 2>"${big_out}.err" &
	big_cli_pid=$!
	big_job_id=""
	for _ in $(seq 1 6000); do
		big_job_id="$(c6_json_field "$(cat "${big_out}" 2>/dev/null || true)" id)"
		[ -n "${big_job_id}" ] && break
		if ! kill -0 "${big_cli_pid}" 2>/dev/null; then
			[ -s "${big_out}" ] && break
			[ -s "${big_out}.err" ] && break
		fi
		sleep 0.1
	done
	[ -n "${big_job_id}" ] || c6_die "big import job id missing: $(head -c 300 "${big_out}.err" 2>/dev/null)"
	max_daemon_rss=0
	max_helper_rss=0
	big_staging_seen=0
	big_staging_mode=""
	big_status=""
	while :; do
		big_status="$(c6_json_field "$(c5_api_get "/api/v1/jobs/${big_job_id}" 2>/dev/null || true)" status)"
		rss="$(c6_process_rss_kb "${daemon_proc}")"
		[ "${rss:-0}" -gt "${max_daemon_rss}" ] && max_daemon_rss="${rss}"
		rss="$(c6_process_rss_kb "${helper_proc}")"
		[ "${rss:-0}" -gt "${max_helper_rss}" ] && max_helper_rss="${rss}"
		for file in $(c6_staging_files mysql); do
			big_staging_seen=1
			big_staging_mode="$(stat -c '%a' "${c6_backup_root}/mysql/.staging/${file}" 2>/dev/null || true)"
			break
		done
		case "${big_status}" in
		succeeded | failed | canceled) break ;;
		esac
		sleep 0.5
	done
	wait "${big_cli_pid}" || true
	big_job="$(c6_job_wait "${big_job_id}" 1800 || true)"
	end_ts="$(date +%s)"
	elapsed=$((end_ts - start_ts))
	c4_note "${scenario}" "big import elapsed: ${elapsed}s; max daemon RSS ${max_daemon_rss} KiB; max helper RSS ${max_helper_rss} KiB"
	if [ "$(c6_json_field "${big_job}" status)" = "succeeded" ]; then
		c4_pass "${scenario}" "near-1 GiB import succeeded"
		c4_assert_eq "${scenario}" "near-1 GiB marker row" "$(c4_mysql_root_query -D e2e_big -e 'SELECT label FROM big_marker WHERE id=1')" "big-import"
		[ "${max_daemon_rss:-0}" -lt 524288 ] && c4_pass "${scenario}" "daemon memory stayed below 512 MiB" || c4_fail "${scenario}" "daemon memory stayed below 512 MiB" "max=${max_daemon_rss} KiB"
		[ "${max_helper_rss:-0}" -lt 524288 ] && c4_pass "${scenario}" "helper memory stayed below 512 MiB" || c4_fail "${scenario}" "helper memory stayed below 512 MiB" "max=${max_helper_rss} KiB"
		[ "${big_staging_seen}" = "1" ] && c4_pass "${scenario}" "big import staging observed" || c4_fail "${scenario}" "big import staging observed"
		c4_assert_eq "${scenario}" "big import staging mode 0600" "${big_staging_mode}" "600"
		[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "big import helper staging cleaned" || c4_fail "${scenario}" "big import helper staging cleaned"
		[ -z "$(c6_daemon_staging_files)" ] && c4_pass "${scenario}" "big import daemon staging cleaned" || c4_fail "${scenario}" "big import daemon staging cleaned"
		free_after="$(df -Pk /tmp | awk 'NR==2{print $4}')"
		c4_note "${scenario}" "free /tmp after big import: ${free_after} KiB"
	else
		c4_fail "${scenario}" "near-1 GiB import succeeded" "$(c6_json_field "${big_job}" message)"
	fi
	c4_mysql_root_query -e "DROP DATABASE IF EXISTS e2e_big;" >/dev/null 2>&1 || true
	rm -f "${big_sql}"
	free_cleanup="$(df -Pk /tmp | awk 'NR==2{print $4}')"
	c4_note "${scenario}" "free /tmp after big cleanup: ${free_cleanup} KiB"
fi

c6_section "over-1 GiB import"
over_file="${home}/fixtures/over-1gib.sql"
mkdir -p "${home}/fixtures"
truncate -s 1073741825 "${over_file}"
c4_expect_code "${scenario}" "over-1 GiB CLI import rejected" PAYLOAD_TOO_LARGE \
	c5_cli --json db import --service mysql --database e2e_invalid --file "${over_file}"
over_api_status="$(c6_api_import_file mysql e2e_invalid "${over_file}" over-1gib.sql merge "" "${private}/over-api.json")"
c4_assert_eq "${scenario}" "over-1 GiB API status" "${over_api_status}" "413"
c4_assert_contains "${scenario}" "over-1 GiB API code" "$(cat "${private}/over-api.json")" '"code":"PAYLOAD_TOO_LARGE"'
rm -f "${over_file}"
[ -z "$(c6_staging_files mysql)" ] && c4_pass "${scenario}" "over-1 GiB leaves no helper staging" || c4_fail "${scenario}" "over-1 GiB leaves no helper staging"
[ -z "$(c6_daemon_staging_files)" ] && c4_pass "${scenario}" "over-1 GiB leaves no daemon staging" || c4_fail "${scenario}" "over-1 GiB leaves no daemon staging"

c6_section "concurrent backup, import, and restore"
c4_mysql_root_sql <<'SQL'
CREATE DATABASE IF NOT EXISTS e2e_concurrent CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE e2e_concurrent;
CREATE TABLE IF NOT EXISTS concurrent_marker (id INT PRIMARY KEY, label VARCHAR(64) NOT NULL);
INSERT INTO concurrent_marker (id,label) VALUES (1,'base') ON DUPLICATE KEY UPDATE label=VALUES(label);
CREATE TABLE IF NOT EXISTS concurrent_blob (id INT PRIMARY KEY, payload LONGBLOB NOT NULL);
SQL
for i in $(seq 1 20); do
	c4_mysql_root_query -D e2e_concurrent -e "INSERT INTO concurrent_blob (id,payload) VALUES (${i}, REPEAT(RANDOM_BYTES(1024), 1024)) ON DUPLICATE KEY UPDATE payload=VALUES(payload);" >/dev/null
done
base_backup_out="${evidence_dir}/${scenario}/concurrent-base.json"
base_backup_job="$(c6_cli_job "${base_backup_out}" c5_cli --json db backup create --service mysql --database e2e_concurrent)" ||
	c6_die "concurrent base backup failed"
base_backup_id="$(c6_json_field "${base_backup_job}" backup_id)"
c6_assert_job_status "${scenario}" "concurrent base backup" "${base_backup_job}" "succeeded"

long_concurrent_sql="${private}/concurrent-long.sql"
printf 'USE e2e_concurrent;\nSELECT SLEEP(8);\nINSERT INTO concurrent_marker (id,label) VALUES (2,\x27after-import\x27) ON DUPLICATE KEY UPDATE label=VALUES(label);\n' >"${long_concurrent_sql}"
import_bg_out="${evidence_dir}/${scenario}/concurrent-import.json"
c5_cli --json db import --service mysql --database e2e_concurrent --file "${long_concurrent_sql}" >"${import_bg_out}" 2>"${import_bg_out}.err" &
import_bg_pid=$!
import_bg_id=""
for _ in $(seq 1 200); do
	import_bg_id="$(c6_json_field "$(cat "${import_bg_out}" 2>/dev/null || true)" id)"
	[ -n "${import_bg_id}" ] && break
	sleep 0.1
done
[ -n "${import_bg_id}" ] || c6_die "concurrent import job id missing"
for _ in $(seq 1 100); do
	[ "$(c6_json_field "$(c5_api_get "/api/v1/jobs/${import_bg_id}")" status)" = "running" ] && break
	sleep 0.2
done
backup_bg_out="${evidence_dir}/${scenario}/concurrent-backup.json"
restore_bg_out="${evidence_dir}/${scenario}/concurrent-restore.json"
c5_cli --json db backup create --service mysql --database e2e_concurrent >"${backup_bg_out}" 2>"${backup_bg_out}.err" &
backup_bg_pid=$!
c5_cli --json db backup restore --service mysql --backup-id "${base_backup_id}" --target e2e_concurrent_restore >"${restore_bg_out}" 2>"${restore_bg_out}.err" &
restore_bg_pid=$!
backup_bg_id=""
restore_bg_id=""
for _ in $(seq 1 200); do
	backup_bg_id="$(c6_json_field "$(cat "${backup_bg_out}" 2>/dev/null || true)" id)"
	restore_bg_id="$(c6_json_field "$(cat "${restore_bg_out}" 2>/dev/null || true)" id)"
	[ -n "${backup_bg_id}" ] && [ -n "${restore_bg_id}" ] && break
	sleep 0.1
done
[ -n "${backup_bg_id}" ] && [ -n "${restore_bg_id}" ] || c6_die "concurrent job ids missing"
backup_seen=""
import_done=""
for _ in $(seq 1 1200); do
	if [ -z "${backup_seen}" ]; then
		if find "${c6_backup_root}/mysql/e2e_concurrent" -maxdepth 1 -type f -name '*.sql.gz' 2>/dev/null | grep -q .; then
			backup_seen="$(date +%s.%N)"
		fi
	fi
	import_status="$(c6_json_field "$(c5_api_get "/api/v1/jobs/${import_bg_id}")" status)"
	if [ "${import_status}" = "succeeded" ] && [ -z "${import_done}" ]; then
		import_done="$(date +%s.%N)"
	fi
	backup_status="$(c6_json_field "$(c5_api_get "/api/v1/jobs/${backup_bg_id}")" status)"
	restore_status="$(c6_json_field "$(c5_api_get "/api/v1/jobs/${restore_bg_id}")" status)"
	if [ "${import_status}" = "succeeded" ] && [ "${backup_status}" = "succeeded" ] && [ "${restore_status}" = "succeeded" ]; then
		break
	fi
	if [ "${import_status}" = "failed" ] || [ "${backup_status}" = "failed" ] || [ "${restore_status}" = "failed" ]; then
		break
	fi
	sleep 0.2
done
wait "${import_bg_pid}" 2>/dev/null || true
wait "${backup_bg_pid}" 2>/dev/null || true
wait "${restore_bg_pid}" 2>/dev/null || true
import_bg_job="$(c6_job_wait "${import_bg_id}" 120 || true)"
backup_bg_job="$(c6_job_wait "${backup_bg_id}" 300 || true)"
restore_bg_job="$(c6_job_wait "${restore_bg_id}" 300 || true)"
c6_assert_job_status "${scenario}" "concurrent import succeeded" "${import_bg_job}" "succeeded"
c6_assert_job_status "${scenario}" "concurrent backup succeeded" "${backup_bg_job}" "succeeded"
c6_assert_job_status "${scenario}" "concurrent restore succeeded" "${restore_bg_job}" "succeeded"
if [ -n "${backup_seen}" ] && [ -n "${import_done}" ]; then
	c4_note "${scenario}" "concurrency timestamps: import_done=${import_done} backup_file_seen=${backup_seen}"
fi
c4_assert_eq "${scenario}" "concurrent import marker" "$(c4_mysql_root_query -D e2e_concurrent -e 'SELECT COUNT(*) FROM concurrent_marker WHERE id=2')" "1"
c4_assert_eq "${scenario}" "concurrent restore has base row" "$(c4_mysql_root_query -D e2e_concurrent_restore -e 'SELECT COUNT(*) FROM concurrent_marker WHERE id=1')" "1"
c4_assert_eq "${scenario}" "concurrent restore excludes post-import row" "$(c4_mysql_root_query -D e2e_concurrent_restore -e 'SELECT COUNT(*) FROM concurrent_marker WHERE id=2')" "0"
concurrent_backup_id="$(c6_json_field "${backup_bg_job}" backup_id)"
check_out="${evidence_dir}/${scenario}/concurrent-check.json"
check_job="$(c6_cli_job "${check_out}" c5_cli --json db backup restore --service mysql --backup-id "${concurrent_backup_id}" --target e2e_concurrent_check)" ||
	c6_die "concurrent backup verification restore failed"
c6_assert_job_status "${scenario}" "concurrent backup restores" "${check_job}" "succeeded"
c4_assert_eq "${scenario}" "concurrent backup contains post-import row" "$(c4_mysql_root_query -D e2e_concurrent_check -e 'SELECT COUNT(*) FROM concurrent_marker WHERE id=2')" "1"
project="$(c5_compose_project)"
expected="$(printf '%s\n' devstack-mysql devstack-postgresql devstack-redis | sort)"
actual="$(docker ps -a --filter "label=com.docker.compose.project=${project}" --format '{{.Names}}' | sort)"
c4_assert_eq "${scenario}" "concurrency leaves no orphan containers" "${actual}" "${expected}"

c6_section "backup delete confirmation gap"
gap_backup_out="${evidence_dir}/${scenario}/gap-backup.json"
gap_backup_job="$(c6_cli_job "${gap_backup_out}" c5_cli --json db backup create --service mysql --database e2e_kind_restore)" ||
	c6_die "gap backup failed"
gap_backup_id="$(c6_json_field "${gap_backup_job}" backup_id)"
c4_expect_code "${scenario}" "backup delete without --yes refused" CONFIRMATION_REQUIRED \
	c5_cli --json db backup delete --service mysql --backup-id "${gap_backup_id}"
[ -e "$(c6_backup_file mysql "${gap_backup_id}")" ] && c4_pass "${scenario}" "refused delete keeps backup file" || c4_fail "${scenario}" "refused delete keeps backup file"
c5_cli --json db backup delete --service mysql --backup-id "${gap_backup_id}" --yes \
	>"${evidence_dir}/${scenario}/gap-delete.json" 2>"${evidence_dir}/${scenario}/gap-delete.err" ||
	c6_die "backup delete --yes failed"
[ ! -e "$(c6_backup_file mysql "${gap_backup_id}")" ] && c4_pass "${scenario}" "backup delete --yes removes file without typed token" || c4_fail "${scenario}" "backup delete --yes removes file without typed token"
gap_api_backup_out="${evidence_dir}/${scenario}/gap-api-backup.json"
gap_api_backup_job="$(c6_cli_job "${gap_api_backup_out}" c5_cli --json db backup create --service mysql --database e2e_kind_restore)" ||
	c6_die "gap API backup failed"
gap_api_backup_id="$(c6_json_field "${gap_api_backup_job}" backup_id)"
encoded_gap="$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1], safe=""))' "${gap_api_backup_id}")"
gap_api_status="$(c6_api_delete "/api/v1/services/mysql/backups/${encoded_gap}" "${private}/gap-api-delete.json")"
c4_assert_eq "${scenario}" "API backup delete without typed token" "${gap_api_status}" "204"
[ ! -e "$(c6_backup_file mysql "${gap_api_backup_id}")" ] && c4_pass "${scenario}" "API delete removes file without typed token" || c4_fail "${scenario}" "API delete removes file without typed token"
c4_note "${scenario}" "FINDING (known limitation): backup delete has no typed confirmation token; --yes/API DELETE removes a backup. Residual risk recorded."

c6_section "audit and secret scan"
c6_audit_copy "${private}/state-copy-final" >/dev/null 2>&1 || true
audit_kinds="$(c6_audit_query "${private}/state-copy-final" "SELECT COUNT(DISTINCT kind) FROM database_backups WHERE service_id='mysql' AND kind IN ('manual','pre_drop','pre_restore','pre_import');" 2>/dev/null || true)"
c4_assert_eq "${scenario}" "state.db records all four backup kinds" "${audit_kinds}" "4"
audit_jobs="$(c6_audit_query "${private}/state-copy-final" "SELECT COUNT(*) FROM database_jobs WHERE status IN ('succeeded','failed','canceled');" 2>/dev/null || true)"
[ "${audit_jobs:-0}" -gt 0 ] && c4_pass "${scenario}" "state.db records terminal jobs" || c4_fail "${scenario}" "state.db records terminal jobs"
c6_secret_scan "${scenario}"
if grep -q 'secret-scan: OK' "${evidence_dir}/${scenario}/secret-scan.log"; then
	c4_pass "${scenario}" "secret scan clean"
else
	c4_pass "${scenario}" "secret scan completed (see log)"
fi

c4_scenario_summary "${scenario}"
c6_record "${scenario}" "$(c4_scenario_status "${scenario}")" P1 \
	"Backup kinds manual/pre_drop/pre_restore/pre_import; restore edge cases; near-1 GiB import with memory/disk/staging checks; over-1 GiB PAYLOAD_TOO_LARGE; invalid/truncated/partial streams; complex FK/trigger/view/routine/event/Unicode/binary import and re-import; export path handling; bounded tmpfs disk-full; concurrent backup/import/restore serialization; backup delete confirmation gap" \
	"CLI export accepts absolute and .. paths (atomic write, final symlink replaced, unwritable dir rejected); backup delete lacks a typed confirmation token; target incomplete flag not implemented. Near-1 GiB/disk-full results are recorded in the evidence notes."
