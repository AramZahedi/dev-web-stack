#!/usr/bin/env bash
# Scenario wrapper: enforce an ID/environment, capture evidence, time out,
# clean up, scan for secrets, and record a result row.
#
# Example:
#   run-scenario.sh HARNESS-SMOKE --environment LIN-UBU --timeout 1800 \
#     -- bash test/e2e/database-management/smoke-inner.sh
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

scenario_id="${1:-}"
[ -n "${scenario_id}" ] || { printf 'run-scenario.sh: scenario id is required\n' >&2; exit 2; }
shift
case "${scenario_id}" in
E2E-[0-9][0-9] | HARNESS-[A-Z0-9-]*) ;;
*) printf 'run-scenario.sh: invalid scenario id %s (use E2E-NN or HARNESS-*)\n' "${scenario_id}" >&2; exit 2 ;;
esac

environment="${E2E_SCENARIO_ENV:-LIN-UBU}"
priority="${E2E_SCENARIO_PRIORITY:-P0}"
timeout_seconds="${E2E_TIMEOUT:-1800}"
do_cleanup=1
while [ $# -gt 0 ]; do
	case "$1" in
	--environment) environment="$2"; shift 2 ;;
	--priority) priority="$2"; shift 2 ;;
	--timeout) timeout_seconds="$2"; shift 2 ;;
	--no-cleanup) do_cleanup=0; shift ;;
	--stamp) stamp="$2"; run_key="$2"; shift 2 ;;
	--home) home="$2"; shift 2 ;;
	--) shift; break ;;
	*) printf 'run-scenario.sh: unknown option %s (use -- before the command)\n' "$1" >&2; exit 2 ;;
	esac
done
[ $# -gt 0 ] || { printf 'run-scenario.sh: command is required after --\n' >&2; exit 2; }

case "${environment}" in
WIN | WIN-REAL | LIN-UBE | LIN-DEB | LIN-FED | UI-MOCK | UI-LIVE | CLI-PS5 | CLI-PS7 | CLI-BASH) ;;
*) printf 'run-scenario.sh: invalid environment %s\n' "${environment}" >&2; exit 2 ;;
esac

e2e_refresh_paths
run_key="$(printf '%s' "${stamp}" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/-\{2,\}/-/g; s/^-//; s/-$//')"
prefix="devstack-e2e-${run_key}"
network="${E2E_NETWORK:-${prefix}-net}"
if [ -z "${E2E_HOME:-}" ]; then
	home="/tmp/devstack-e2e-${run_key}"
fi
e2e_refresh_paths
e2e_guard
evidence_dir="${evidence_root}/${run_key}/${scenario_id}"
mkdir -p "${evidence_dir}"
command_log="${evidence_dir}/command.log"
: >"${command_log}"

export E2E_SCENARIO_ID="${scenario_id}"
export E2E_SCENARIO_ENV="${environment}"
export E2E_SCENARIO_PRIORITY="${priority}"
export E2E_EVIDENCE_DIR="${evidence_dir}"
export E2E_STAMP="${stamp}"
export E2E_RUN_KEY="${run_key}"
export E2E_HOME="${home}"
export E2E_NETWORK="${network}"
export E2E_PREFIX="${prefix}"

cleanup_done=0
cleanup_now() {
	[ "${cleanup_done}" = "1" ] && return 0
	cleanup_done=1
	if [ "${do_cleanup}" = "1" ]; then
		"${e2e_root}/cleanup.sh" --home "${home}" >>"${command_log}" 2>&1 || true
	fi
}
on_signal() {
	printf 'run-scenario: interrupted\n' >>"${command_log}"
	cleanup_now
	exit 130
}
trap cleanup_now EXIT
trap on_signal INT TERM

e2e_log "scenario ${scenario_id} (${environment}) evidence ${evidence_dir}"
set +e
timeout --signal=TERM --kill-after=30s "${timeout_seconds}" "$@" 2>&1 | tee -a "${command_log}"
exit_code="${PIPESTATUS[0]}"
set -e

status="PASS"
notes=""
case "${exit_code}" in
0) status="PASS" ;;
77) status="BLOCKED"; notes="command returned 77 (blocked)" ;;
124 | 137) status="FAIL"; notes="timeout after ${timeout_seconds}s" ;;
*) status="FAIL"; notes="exit code ${exit_code}" ;;
esac

if [ -d "${logs_dir}" ]; then
	cp -a "${logs_dir}" "${evidence_dir}/logs" 2>/dev/null || true
fi
if ! "${e2e_root}/secret-scan.sh" "${evidence_dir}" >>"${command_log}" 2>&1; then
	status="FAIL"
	notes="${notes:+${notes}; }secret scanner found redacted hits"
fi

commit="$(e2e_git_sha)"
"${e2e_root}/results.sh" record --id "${scenario_id}" --priority "${priority}" \
	--environment "${environment}" --status "${status}" --evidence "${evidence_dir}" \
	--commit "${commit}" --residual-risk "${E2E_RESIDUAL_RISK:-none recorded}" \
	--notes "${notes}" >>"${command_log}" 2>&1 || true
"${e2e_root}/results.sh" render >>"${command_log}" 2>&1 || true

cleanup_now
e2e_log "scenario ${scenario_id}: ${status} (exit ${exit_code})"
case "${status}" in
PASS) exit 0 ;;
BLOCKED) exit 77 ;;
*) exit 1 ;;
esac
