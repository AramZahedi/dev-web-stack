#!/usr/bin/env bash
# Run the cycle 6 Linux backup/import batch: E2E-07 and E2E-18.
#
# One disposable home/helper/daemon is shared for the whole batch. MySQL runs
# without published host ports; PostgreSQL uses its fixed loopback 5432 and
# Redis 6379. The real devstack distro, /data/devstack, helper, agent, and
# Docker engine are never touched.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export E2E_STAMP="${E2E_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
export E2E_SCENARIO_ENV="${E2E_SCENARIO_ENV:-LIN-UBE}"
export E2E_CYCLE6_SHARED=1
# shellcheck source=cycle6-env.sh
source "${here}/cycle6-env.sh"

c6_guard
trap c6_cleanup EXIT
c6_init
c6_ensure_services

status=0
for scenario in e2e07 e2e18; do
	printf '\n########## cycle6 %s ##########\n' "${scenario}"
	if ! E2E_CYCLE6_SHARED=1 bash "${here}/cycle6-${scenario}.sh"; then
		status=1
	fi
done

printf '\n########## final secret scan ##########\n'
if [ -d "${evidence_dir}" ]; then
	"${cycle4_root}/secret-scan.sh" "${evidence_dir}" "${logs_dir}" || status=1
fi

printf '\n########## results ##########\n'
"${cycle4_root}/results.sh" show || true
exit "${status}"
