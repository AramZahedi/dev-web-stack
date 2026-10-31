#!/usr/bin/env bash
# Run the cycle 4 Linux core batch: E2E-01, E2E-02, and E2E-16.
#
# Each scenario uses the same disposable run key and cleans up its own home.
# The runner does not touch the real devstack distro, /data/devstack, the real
# helper, or the real Docker engine.
set -Eeuo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
export E2E_STAMP="${E2E_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)}"
export E2E_SCENARIO_ENV="${E2E_SCENARIO_ENV:-LIN-UBE}"

status=0
for scenario in e2e01 e2e02 e2e16; do
	printf '\n########## cycle4 %s ##########\n' "${scenario}"
	if ! bash "${here}/cycle4-${scenario}.sh"; then
		status=1
	fi
done

run_key="$(printf '%s' "${E2E_STAMP}" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/-\{2,\}/-/g; s/^-//; s/-$//')"
evidence_root="${E2E_EVIDENCE_ROOT:-$(cd -- "${here}/../../../.." && pwd)/dist/e2e/database-management/evidence}"
if [ -d "${evidence_root}/${run_key}" ]; then
	printf '\n########## final secret scan ##########\n'
	"${here}/../secret-scan.sh" "${evidence_root}/${run_key}" || status=1
fi

printf '\n########## results ##########\n'
"${here}/../results.sh" show || true
exit "${status}"
