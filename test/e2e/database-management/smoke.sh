#!/usr/bin/env bash
# R12 wrapper. Runs the harness smoke through run-scenario.sh so the result
# tracker, evidence layout, timeout, and secret scanner are exercised too.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
stamp="${E2E_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
export E2E_STAMP="${stamp}"

exec "${e2e_root}/run-scenario.sh" HARNESS-SMOKE \
	--environment "${E2E_SCENARIO_ENV:-LIN-UBU}" \
	--priority P2 \
	--timeout "${E2E_TIMEOUT:-2400}" \
	--no-cleanup \
	--stamp "${stamp}" \
	-- bash "${e2e_root}/smoke-inner.sh"
