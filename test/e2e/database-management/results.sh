#!/usr/bin/env bash
# Record and render database-management E2E results.
#
# Storage: dist/e2e/database-management/results.json (gitignored).
# Render:  dist/e2e/database-management/results.md
#
# Every scenario row records: id, priority, environment, status, evidence path,
# commit SHA, recorded_at, residual_risk, notes. PASS, FAIL, SKIP, and BLOCKED
# are distinct and are never collapsed.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

command_name="${1:-help}"
shift || true
id=""
priority="P0"
environment="${E2E_SCENARIO_ENV:-}"
status=""
evidence=""
commit=""
residual_risk="none recorded"
notes=""

while [ $# -gt 0 ]; do
	case "$1" in
	--id) id="$2"; shift 2 ;;
	--priority) priority="$2"; shift 2 ;;
	--environment) environment="$2"; shift 2 ;;
	--status) status="$2"; shift 2 ;;
	--evidence) evidence="$2"; shift 2 ;;
	--commit) commit="$2"; shift 2 ;;
	--residual-risk) residual_risk="$2"; shift 2 ;;
	--notes) notes="$2"; shift 2 ;;
	--results-file) results_file="$2"; shift 2 ;;
	-h | --help) command_name=help; break ;;
	*) printf 'results.sh: unknown argument %s\n' "$1" >&2; exit 2 ;;
	esac
done

results_md="$(dirname "${results_file}")/results.md"

help() {
	cat <<'EOF'
usage:
  results.sh record --id E2E-00 --status PASS --environment LIN-UBU \
      --evidence PATH --commit SHA [--priority P0] \
      [--residual-risk TEXT] [--notes TEXT]
  results.sh render
  results.sh show
  results.sh init
Statuses: PASS, FAIL, SKIP, BLOCKED.
EOF
}

python_common() {
	python3 - "${results_file}" "$@"
}

case "${command_name}" in
init)
	python_common <<'PY'
import json, os, sys, datetime
path = sys.argv[1]
os.makedirs(os.path.dirname(path), exist_ok=True)
data = {"schema_version": 1, "updated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "scenarios": []}
with open(path, "w", encoding="utf-8") as fh:
    json.dump(data, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY
	printf 'results: initialized %s\n' "${results_file}"
	;;
record)
	[ -n "${id}" ] || { printf 'results.sh: --id is required\n' >&2; exit 2; }
	[ -n "${status}" ] || { printf 'results.sh: --status is required\n' >&2; exit 2; }
	[ -n "${environment}" ] || { printf 'results.sh: --environment is required\n' >&2; exit 2; }
	case "${status}" in PASS | FAIL | SKIP | BLOCKED) ;; *) printf 'results.sh: invalid status %s\n' "${status}" >&2; exit 2 ;; esac
	[ -n "${commit}" ] || commit="$(e2e_git_sha)"
	python_common "${id}" "${priority}" "${environment}" "${status}" "${evidence}" "${commit}" "${residual_risk}" "${notes}" <<'PY'
import json, os, sys, datetime
path = sys.argv[1]
id_, priority, environment, status, evidence, commit, risk, notes = sys.argv[2:10]
now = datetime.datetime.now(datetime.timezone.utc).isoformat()
if os.path.exists(path):
    with open(path, encoding="utf-8") as fh:
        data = json.load(fh)
else:
    data = {"schema_version": 1, "scenarios": []}
data.setdefault("schema_version", 1)
data["updated_at"] = now
rows = [row for row in data.get("scenarios", []) if row.get("id") != id_]
rows.append({
    "id": id_,
    "priority": priority,
    "environment": environment,
    "status": status,
    "evidence": evidence,
    "commit": commit,
    "recorded_at": now,
    "residual_risk": risk,
    "notes": notes,
})
data["scenarios"] = sorted(rows, key=lambda row: row.get("id", ""))
os.makedirs(os.path.dirname(path), exist_ok=True)
tmp = path + ".tmp"
with open(tmp, "w", encoding="utf-8") as fh:
    json.dump(data, fh, indent=2, sort_keys=True)
    fh.write("\n")
os.replace(tmp, path)
print(f"results: {status} {id_} ({environment})")
PY
	;;
render)
	python_common "${results_md}" <<'PY'
import json, os, sys, datetime
path, md_path = sys.argv[1], sys.argv[2]
if not os.path.exists(path):
    raise SystemExit(f"results: missing {path}")
with open(path, encoding="utf-8") as fh:
    data = json.load(fh)
rows = data.get("scenarios", [])
lines = [
    "# Database management E2E results",
    "",
    f"- Updated: {data.get('updated_at', '')}",
    f"- Schema: {data.get('schema_version', 1)}",
    "",
    "| Scenario | Priority | Environment | Status | Evidence | Commit | Residual risk |",
    "|---|---|---|---|---|---|---|",
]
for row in rows:
    lines.append("| {id} | {priority} | {environment} | **{status}** | {evidence} | `{commit}` | {risk} |".format(
        id=row.get("id", ""),
        priority=row.get("priority", ""),
        environment=row.get("environment", ""),
        status=row.get("status", ""),
        evidence=row.get("evidence", ""),
        commit=row.get("commit", ""),
        risk=(row.get("residual_risk", "") or "").replace("|", "\\|"),
    ))
lines.append("")
with open(md_path, "w", encoding="utf-8", newline="\n") as fh:
    fh.write("\n".join(lines))
print(f"results: rendered {md_path}")
PY
	;;
show)
	python_common <<'PY'
import json, os, sys
path = sys.argv[1]
if not os.path.exists(path):
    raise SystemExit(f"results: missing {path}")
with open(path, encoding="utf-8") as fh:
    data = json.load(fh)
for row in data.get("scenarios", []):
    print(f"{row.get('id',''):<12} {row.get('status',''):<8} {row.get('environment',''):<10} {row.get('evidence','')}")
PY
	;;
help | -h | --help) help ;;
*) printf 'results.sh: unknown command %s\n' "${command_name}" >&2; help >&2; exit 2 ;;
esac
