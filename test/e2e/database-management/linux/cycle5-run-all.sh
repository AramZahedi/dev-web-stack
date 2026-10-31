#!/usr/bin/env bash
# Run the cycle 5 Linux site integration batch:
# E2E-03, E2E-04, E2E-17, E2E-06, E2E-05.
#
# One disposable home/helper/daemon is shared for the whole batch. MySQL and
# MariaDB run without published ports; PostgreSQL uses fixed loopback 5432.
# The real devstack distro, /data/devstack, helper, agent, and Docker engine
# are never touched.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export E2E_STAMP="${E2E_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
export E2E_SCENARIO_ENV="${E2E_SCENARIO_ENV:-LIN-UBE}"
export E2E_CYCLE5_SHARED=1
# shellcheck source=cycle5-env.sh
source "${here}/cycle5-env.sh"

c5_guard
trap c5_cleanup EXIT
c5_init
c5_ensure_base

status=0
for scenario in e2e03 e2e04 e2e17 e2e06 e2e05; do
	printf '\n########## cycle5 %s ##########\n' "${scenario}"
	if ! bash "${here}/cycle5-${scenario}.sh"; then
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
