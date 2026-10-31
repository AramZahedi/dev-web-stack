#!/usr/bin/env bash
# Cycle 5 / E2E-05: site deletion preserves service-wide data; service
# uninstall keep-data vs delete-data; simulated data-deletion failure.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

scenario="E2E-05"
c5_guard
c4_check_init "${scenario}"
if [ "${E2E_CYCLE5_SHARED:-0}" != "1" ]; then
	trap c5_cleanup EXIT
	c5_init
fi
c5_install_postgresql

backup_id() {
	local file="$1" database="$2"
	python3 -c 'import json,sys; rows=json.load(open(sys.argv[1], encoding="utf-8")).get("backups", []); print(next((b["id"] for b in rows if b.get("database")==sys.argv[2]), ""))' "${file}" "${database}"
}

wait_for_backup() {
	local database="$1" file="$2" id=""
	for _ in $(seq 1 60); do
		c5_cli --json db backup list --service postgresql >"${file}" 2>/dev/null || true
		id="$(backup_id "${file}" "${database}")"
		if [ -n "${id}" ]; then
			printf '%s\n' "${id}"
			return 0
		fi
		sleep 1
	done
	return 1
}

c5_cli --json db create --service postgresql --name e2e05_app >"${evidence_dir}/${scenario}/create-db.json" 2>"${evidence_dir}/${scenario}/create-db.err" &&
	c4_pass "${scenario}" "PostgreSQL database created" || c4_fail "${scenario}" "PostgreSQL database created"
c5_cli --json db user create --service postgresql --name e2e05_user --grant e2e05_app:read_write \
	>"${evidence_dir}/${scenario}/create-user.json" 2>"${evidence_dir}/${scenario}/create-user.err" &&
	c4_pass "${scenario}" "PostgreSQL user created" || c4_fail "${scenario}" "PostgreSQL user created"
uid="$(c5_user_id postgresql e2e05_user)"
[ -n "${uid}" ] && c4_pass "${scenario}" "PostgreSQL user id resolved" || c4_fail "${scenario}" "PostgreSQL user id resolved"
c4_psql_root_sql e2e05_app <<'SQL'
CREATE TABLE IF NOT EXISTS e2e05_marker (id int primary key, label text);
INSERT INTO e2e05_marker (id, label) VALUES (1, 'site-marker') ON CONFLICT (id) DO UPDATE SET label = EXCLUDED.label;
SQL
c4_pass "${scenario}" "marker row seeded"

c5_create_project e2e05-static >/dev/null
c5_cli --json site add --name e2e05-site --path "${home}/projects/e2e05-static" --kind static \
	>"${evidence_dir}/${scenario}/site-add.json" 2>"${evidence_dir}/${scenario}/site-add.err" &&
	c4_pass "${scenario}" "site created" || c4_fail "${scenario}" "site created"
c5_cli --json db link --site e2e05-site --service postgresql --database e2e05_app --user-id "${uid}" --primary \
	>"${evidence_dir}/${scenario}/site-link.json" 2>"${evidence_dir}/${scenario}/site-link.err" &&
	c4_pass "${scenario}" "site linked to PostgreSQL" || c4_fail "${scenario}" "site linked to PostgreSQL"
c5_cli --json site start e2e05-site >"${evidence_dir}/${scenario}/site-start.json" 2>"${evidence_dir}/${scenario}/site-start.err" &&
	c4_pass "${scenario}" "site started" || c4_fail "${scenario}" "site started"

c5_cli --json db backup create --service postgresql --database e2e05_app \
	>"${evidence_dir}/${scenario}/backup-create.json" 2>"${evidence_dir}/${scenario}/backup-create.err" &&
	c4_pass "${scenario}" "PostgreSQL backup job queued" || c4_fail "${scenario}" "PostgreSQL backup job queued"
bkp="$(wait_for_backup e2e05_app "${private}/backups.json" || true)"
[ -n "${bkp}" ] && c4_pass "${scenario}" "backup id resolved" || c4_fail "${scenario}" "backup id resolved"

c5_cli --json site remove e2e05-site --yes >"${evidence_dir}/${scenario}/site-remove.json" 2>"${evidence_dir}/${scenario}/site-remove.err" &&
	c4_pass "${scenario}" "site removed" || c4_fail "${scenario}" "site removed"
removed_links_status="$(curl -sS -o "${private}/removed-site-links.json" -w '%{http_code}' "http://127.0.0.1:${ui_port}/api/v1/sites/e2e05-site/databases")"
if [ "${removed_links_status}" = "404" ] || grep -q '"links":\[\]' "${private}/removed-site-links.json"; then
	c4_pass "${scenario}" "site links removed with the site"
else
	c4_fail "${scenario}" "site links removed with the site" "status=${removed_links_status}"
fi
c5_cli --json db list --service postgresql >"${private}/dbs-after-site.json"
c5_cli --json db user list --service postgresql >"${private}/users-after-site.json"
c5_cli --json db backup list --service postgresql >"${private}/backups-after-site.json"
if python3 - "${private}/dbs-after-site.json" "${private}/users-after-site.json" "${private}/backups-after-site.json" <<'PY'
import json, sys
dbs = {d.get("name") for d in json.load(open(sys.argv[1], encoding="utf-8")).get("databases", [])}
users = {u.get("username"): u for u in json.load(open(sys.argv[2], encoding="utf-8")).get("users", [])}
backups = json.load(open(sys.argv[3], encoding="utf-8")).get("backups", [])
user = users.get("e2e05_user")
raise SystemExit(0 if "e2e05_app" in dbs and user is not None and user.get("password_managed") and user.get("grants") and any(b.get("database") == "e2e05_app" for b in backups) else 1)
PY
then
	c4_pass "${scenario}" "database, user, grants, password, and backup remain after site removal"
else
	c4_fail "${scenario}" "database, user, grants, password, and backup remain after site removal"
fi
marker="$(c4_psql_root_query -d e2e05_app -tAc 'SELECT label FROM e2e05_marker WHERE id=1;')"
c4_assert_eq "${scenario}" "marker row remains after site removal" "${marker}" "site-marker"

c5_create_project e2e05-static-2 >/dev/null
c5_cli --json site add --name e2e05-site2 --path "${home}/projects/e2e05-static-2" --kind static \
	>"${evidence_dir}/${scenario}/site2-add.json" 2>"${evidence_dir}/${scenario}/site2-add.err" &&
	c4_pass "${scenario}" "replacement site created" || c4_fail "${scenario}" "replacement site created"
c5_cli --json db link --site e2e05-site2 --service postgresql --database e2e05_app --user-id "${uid}" --primary \
	>"${evidence_dir}/${scenario}/site2-link.json" 2>"${evidence_dir}/${scenario}/site2-link.err" &&
	c4_pass "${scenario}" "replacement site linked to the same database/user" || c4_fail "${scenario}" "replacement site linked to the same database/user"
c5_cli --json site start e2e05-site2 >"${evidence_dir}/${scenario}/site2-start.json" 2>"${evidence_dir}/${scenario}/site2-start.err" &&
	c4_pass "${scenario}" "replacement site started" || c4_fail "${scenario}" "replacement site started"
marker="$(c4_psql_root_query -d e2e05_app -tAc 'SELECT label FROM e2e05_marker WHERE id=1;')"
c4_assert_eq "${scenario}" "marker row readable through the recreated link" "${marker}" "site-marker"

# Keep-data uninstall: data, secrets, and backups survive.
c5_cli --json service uninstall postgresql >"${evidence_dir}/${scenario}/uninstall-keep.json" 2>"${evidence_dir}/${scenario}/uninstall-keep.err" &&
	c4_pass "${scenario}" "PostgreSQL uninstalled with keep-data" || c4_fail "${scenario}" "PostgreSQL uninstalled with keep-data"
[ -d "${devstack_home}/services/postgresql/data" ] && c4_pass "${scenario}" "keep-data preserves the data directory" || c4_fail "${scenario}" "keep-data preserves the data directory"
[ -d "${devstack_home}/secrets/postgresql" ] && c4_pass "${scenario}" "keep-data preserves the secrets directory" || c4_fail "${scenario}" "keep-data preserves the secrets directory"
c5_audit_copy "${private}/state-copy-keep" >/dev/null 2>&1 || true
keep_backups="$(c5_audit_query "${private}/state-copy-keep" "SELECT COUNT(*) FROM database_backups WHERE service_id='postgresql';")"
if [ -n "${bkp}" ] && [ "${keep_backups}" -gt 0 ]; then
	c4_pass "${scenario}" "keep-data leaves backup metadata readable"
else
	c4_fail "${scenario}" "keep-data leaves backup metadata readable" "count=${keep_backups}"
fi
if find "${devstack_home}/backups/databases/postgresql/e2e05_app" -type f -name '*.sql.gz' -print -quit 2>/dev/null | grep -q .; then
	c4_pass "${scenario}" "keep-data leaves the database backup archive"
else
	c4_fail "${scenario}" "keep-data leaves the database backup archive" "archive missing"
fi

# Reinstall over the preserved data and verify the user and marker still work.
c5_cli --json service install postgresql >"${evidence_dir}/${scenario}/reinstall.json" 2>"${evidence_dir}/${scenario}/reinstall.err" &&
	c5_cli --json service activate postgresql >"${evidence_dir}/${scenario}/reactivate.json" 2>"${evidence_dir}/${scenario}/reactivate.err" &&
	c5_wait_container_healthy devstack-postgresql 180 &&
	c4_pass "${scenario}" "PostgreSQL restarted over the preserved data" || c4_fail "${scenario}" "PostgreSQL restarted over the preserved data"
marker="$(c4_psql_root_query -d e2e05_app -tAc 'SELECT label FROM e2e05_marker WHERE id=1;')"
c4_assert_eq "${scenario}" "keep-data marker row survived reinstall" "${marker}" "site-marker"
if c5_cli --json db user list --service postgresql | grep -q '"username":"e2e05_user"'; then
	c4_pass "${scenario}" "keep-data user survived reinstall"
else
	c4_fail "${scenario}" "keep-data user survived reinstall"
fi

# Delete-data uninstall: data goes first, then links/metadata.
c5_cli --json service uninstall postgresql --delete-data --yes \
	>"${evidence_dir}/${scenario}/uninstall-delete.json" 2>"${evidence_dir}/${scenario}/uninstall-delete.err" &&
	c4_pass "${scenario}" "PostgreSQL uninstalled with delete-data" || c4_fail "${scenario}" "PostgreSQL uninstalled with delete-data"
[ ! -e "${devstack_home}/services/postgresql" ] && c4_pass "${scenario}" "delete-data removes the service data directory" || c4_fail "${scenario}" "delete-data removes the service data directory"
[ ! -e "${devstack_home}/secrets/postgresql" ] && c4_pass "${scenario}" "delete-data removes the service secrets" || c4_fail "${scenario}" "delete-data removes the service secrets"
if c5_api_get "/api/v1/sites/e2e05-site2/databases" | grep -q '"links":\[\]'; then
	c4_pass "${scenario}" "delete-data removes links after data deletion"
else
	c4_fail "${scenario}" "delete-data removes links after data deletion"
fi
c5_audit_copy "${private}/state-copy-delete" >/dev/null 2>&1 || true
links_left="$(c5_audit_query "${private}/state-copy-delete" "SELECT COUNT(*) FROM database_links WHERE service_id='postgresql';")"
users_left="$(c5_audit_query "${private}/state-copy-delete" "SELECT COUNT(*) FROM database_users WHERE service_id='postgresql';")"
backups_left="$(c5_audit_query "${private}/state-copy-delete" "SELECT COUNT(*) FROM database_backups WHERE service_id='postgresql';")"
c4_assert_eq "${scenario}" "delete-data leaves no link metadata" "${links_left}" "0"
c4_assert_eq "${scenario}" "delete-data leaves no user metadata" "${users_left}" "0"
c4_assert_eq "${scenario}" "delete-data leaves no backup metadata" "${backups_left}" "0"

# Simulated data-deletion failure: hide the service tree behind a bind mount so
# the helper's backup succeeds but os.RemoveAll fails with EBUSY.
c5_cli --json service install postgresql >"${evidence_dir}/${scenario}/failure-install.json" 2>"${evidence_dir}/${scenario}/failure-install.err" &&
	c5_cli --json service activate postgresql >"${evidence_dir}/${scenario}/failure-activate.json" 2>"${evidence_dir}/${scenario}/failure-activate.err" &&
	c5_wait_container_healthy devstack-postgresql 180 &&
	c4_pass "${scenario}" "PostgreSQL reinstalled for the failure simulation" || c4_fail "${scenario}" "PostgreSQL reinstalled for the failure simulation"
c5_cli --json db create --service postgresql --name e2e05_fail_app >/dev/null 2>&1 || true
c5_cli --json db user create --service postgresql --name e2e05_fail_user --grant e2e05_fail_app:read_write >/dev/null 2>&1 || true
fail_uid="$(c5_user_id postgresql e2e05_fail_user)"
[ -n "${fail_uid}" ] && c4_pass "${scenario}" "failure-simulation user created" || c4_fail "${scenario}" "failure-simulation user created"
c5_create_project e2e05-fail >/dev/null
c5_cli --json site add --name e2e05-fail-site --path "${home}/projects/e2e05-fail" --kind static >/dev/null 2>&1 || true
c5_cli --json db link --site e2e05-fail-site --service postgresql --database e2e05_fail_app --user-id "${fail_uid}" --primary >/dev/null 2>&1 || true
fail_link="$(c5_link_id e2e05-fail-site e2e05_fail_app)"
[ -n "${fail_link}" ] && c4_pass "${scenario}" "failure-simulation link created" || c4_fail "${scenario}" "failure-simulation link created"
c5_cli --json site start e2e05-fail-site >/dev/null 2>&1 || true

mount_dir="${home}/empty-mount"
mkdir -p "${mount_dir}"
mounted=0
if mount --bind "${mount_dir}" "${devstack_home}/services/postgresql" 2>"${private}/mount.err"; then
	mounted=1
	c4_pass "${scenario}" "service tree hidden behind a bind mount for failure injection"
else
	c4_fail "${scenario}" "service tree hidden behind a bind mount for failure injection" "$(head -1 "${private}/mount.err")"
fi
set +e
c5_cli --json service uninstall postgresql --delete-data --yes \
	>"${evidence_dir}/${scenario}/uninstall-fail.json" 2>"${evidence_dir}/${scenario}/uninstall-fail.err"
fail_status=$?
set -e
if [ "${fail_status}" -ne 0 ]; then
	c4_pass "${scenario}" "data-deletion failure returns an error"
else
	c4_fail "${scenario}" "data-deletion failure returns an error"
fi
if grep -qiE 'remove|busy|resource' "${evidence_dir}/${scenario}/uninstall-fail.err" "${evidence_dir}/${scenario}/uninstall-fail.json"; then
	c4_pass "${scenario}" "data-deletion failure error is actionable"
else
	c4_fail "${scenario}" "data-deletion failure error is actionable"
fi
if c5_api_get "/api/v1/sites/e2e05-fail-site/databases" | grep -q '"database":"e2e05_fail_app"'; then
	c4_pass "${scenario}" "data-deletion failure keeps the site link"
else
	c4_fail "${scenario}" "data-deletion failure keeps the site link"
fi
if [ "${mounted}" = "1" ]; then
	umount "${devstack_home}/services/postgresql" 2>/dev/null || true
fi

c5_audit_copy "${private}/state-copy" >/dev/null 2>&1 || true
c5_secret_scan "${scenario}" && c4_pass "${scenario}" "secret scan clean over evidence, logs, and state copy" || c4_fail "${scenario}" "secret scan clean over evidence, logs, and state copy"

c5_record "${scenario}" "$(c4_scenario_status "${scenario}")" P0 \
	"Site deletion preservation; keep-data vs delete-data uninstall; simulated data-deletion failure keeps links" \
	"PostgreSQL fixed 5432 was free on this host; no residual product risk"
c4_scenario_summary "${scenario}"
