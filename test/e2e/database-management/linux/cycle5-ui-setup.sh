#!/usr/bin/env bash
# Cycle 5 live dashboard fixture: start a disposable daemon, create one managed
# link, one metadata-only external link, one reserved-env link, and an Add Site
# review project. Stays alive until $home/stop exists.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

scenario="C5-UI"
c5_guard
c4_check_init "${scenario}"
trap c5_cleanup EXIT
c5_init
c5_ensure_base

c5_create_project e2e-ui-managed >/dev/null
c5_cli --json site add --name e2e-ui-managed --path "${home}/projects/e2e-ui-managed" --kind static \
	--database-service mysql --database-new e2e_ui_app --database-user-new e2e_ui_user \
	--database-grant read_write >"${evidence_dir}/${scenario}/managed-site.json" 2>"${evidence_dir}/${scenario}/managed-site.err"
uid="$(c5_user_id mysql e2e_ui_user)"

ext_pw="$(python3 -c 'import secrets; print(secrets.token_hex(16))')"
printf '%s' "${ext_pw}" >"${private}/ui-external.pw"
chmod 600 "${private}/ui-external.pw"
c4_mysql_root_sql <<SQL
CREATE USER IF NOT EXISTS 'e2e_ui_external'@'%' IDENTIFIED BY '${ext_pw}';
GRANT SELECT ON e2e_ui_app.* TO 'e2e_ui_external'@'%';
SQL
c5_cli --json db user list --service mysql >/dev/null 2>&1
ext_uid="$(c5_user_id mysql e2e_ui_external)"
c5_create_project e2e-ui-external >/dev/null
c5_cli --json site add --name e2e-ui-external --path "${home}/projects/e2e-ui-external" --kind static \
	>"${evidence_dir}/${scenario}/external-site.json" 2>"${evidence_dir}/${scenario}/external-site.err"
c5_cli --json db link --site e2e-ui-external --service mysql --database e2e_ui_app --user-id "${ext_uid}" \
	>"${evidence_dir}/${scenario}/external-link.json" 2>"${evidence_dir}/${scenario}/external-link.err"

c5_create_project e2e-ui-reserved >/dev/null
c5_cli --json site add --name e2e-ui-reserved --path "${home}/projects/e2e-ui-reserved" --kind static \
	--env 'DB_HOST=evil' --env 'DB_PASSWORD=leaked' --env 'DATABASE_URL=mysql://evil' \
	--database-service mysql --database e2e_ui_app --database-user "${uid}" \
	>"${private}/reserved-site.json" 2>"${evidence_dir}/${scenario}/reserved-site.err"
python3 - "${private}/reserved-site.json" "${evidence_dir}/${scenario}/reserved-site-summary.txt" <<'PY'
import json, sys
site = json.load(open(sys.argv[1], encoding="utf-8"))
keys = sorted((site.get("env") or {}).keys())
open(sys.argv[2], "w", encoding="utf-8").write(
    "site=%s\nkind=%s\nenv_keys=%s\nlinked=true\nnote=Values are intentionally redacted; the live Playwright check verified the link-override warning.\n"
    % (site.get("name", ""), site.get("kind", ""), ",".join(keys))
)
PY

review_project="$(c5_create_project e2e-ui-review)"
fixture="${evidence_dir}/${scenario}/ui-fixture.json"
python3 - "${fixture}" "http://127.0.0.1:${ui_port}" "${review_project}" <<'PY'
import json, sys
json.dump({
    "ui_url": sys.argv[2],
    "project_path": sys.argv[3],
    "managed_site": "e2e-ui-managed",
    "external_site": "e2e-ui-external",
    "reserved_site": "e2e-ui-reserved",
}, open(sys.argv[1], "w", encoding="utf-8"), indent=2)
PY
printf 'UI_READY %s\n' "${fixture}"
while [ ! -f "${home}/stop" ]; do
	sleep 1
done
