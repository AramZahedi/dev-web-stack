#!/usr/bin/env bash
# Linux M1 acceptance runner.
#
# The runner is deliberately unprivileged. It refuses to run as root, never
# installs system packages, never writes the real hosts file or trust stores,
# and never binds 80/443. Every phase runs with a temporary log; a failing
# phase prints the log tail and the final table records PASS/FAIL/SKIP/BLOCKED.
#
# Usage: test/acceptance/m1.sh [--quick] [--skip-playwright]
#
#   --quick            skip the race-detector phase
#   --skip-playwright  skip the dashboard Playwright phase
#
# Exit status is 0 when no phase FAILs. SKIP and BLOCKED phases are reported
# and do not fail the run; they are never converted into PASS.
set -Eeuo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
QUICK=0
SKIP_PLAYWRIGHT=0

usage() {
	cat <<'EOF'
Usage: test/acceptance/m1.sh [--quick] [--skip-playwright]

  --quick            skip the race-detector phase
  --skip-playwright  skip the dashboard Playwright phase
  -h, --help         show this help
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--quick)
		QUICK=1
		;;
	--skip-playwright)
		SKIP_PLAYWRIGHT=1
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'acceptance: unknown argument: %s\n' "$1" >&2
		usage >&2
		exit 2
		;;
	esac
	shift
done

if [ "$(id -u)" -eq 0 ]; then
	printf 'acceptance: refusing to run as root; run as the installing user\n' >&2
	exit 2
fi

for tool in go npm node make bash tar curl sha256sum mktemp; do
	if ! command -v "${tool}" >/dev/null 2>&1; then
		printf 'acceptance: required tool not found: %s\n' "${tool}" >&2
		exit 2
	fi
done

# Do not change the process umask: existing tests assert the modes of files
# they create under the caller's umask. mktemp creates the log directory 0700.
LOG_DIR="$(mktemp -d "${TMPDIR:-/tmp}/devstack-acceptance.XXXXXX")"
CURRENT_PHASE_LOG=""
CURRENT_PHASE_NOTE=""

PHASE_NAMES=()
PHASE_RESULTS=()
PHASE_SECONDS=()
PHASE_NOTES=()
PHASE_LOGS=()
SUMMARY_PRINTED=0

cleanup() {
	local status=$?
	set +e
	trap - EXIT INT TERM
	if [ "${SUMMARY_PRINTED}" -eq 0 ] && [ "${status}" -ne 0 ]; then
		printf '\nacceptance: phase logs kept at %s\n' "${LOG_DIR}" >&2
	fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

note() {
	local text="$*"
	if [ -n "${CURRENT_PHASE_NOTE}" ]; then
		printf '%s\n' "${text}" >>"${CURRENT_PHASE_NOTE}"
	fi
	printf '%s\n' "${text}"
}

run_phase() {
	local name="$1"
	shift
	local index=$(( ${#PHASE_NAMES[@]} + 1 ))
	local slug
	slug="$(printf '%02d-%s' "${index}" "${name}" | tr ' /' '__' | tr -cd 'A-Za-z0-9_.-')"
	local log="${LOG_DIR}/${slug}.log"
	local note_file="${log}.note"
	local start="${SECONDS}"
	local status=0
	local duration=0
	local result=""

	CURRENT_PHASE_LOG="${log}"
	CURRENT_PHASE_NOTE="${note_file}"
	: >"${log}"
	: >"${note_file}"

	printf '==> [%02d] %s\n' "${index}" "${name}"
	printf '$ %s\n' "$*" >>"${log}"

	set +e
	"$@" >>"${log}" 2>&1
	status=$?
	set -e

	duration=$((SECONDS - start))
	case "${status}" in
	0) result="PASS" ;;
	77) result="SKIP" ;;
	78) result="BLOCKED" ;;
	*) result="FAIL" ;;
	esac

	PHASE_NAMES+=("${name}")
	PHASE_RESULTS+=("${result}")
	PHASE_SECONDS+=("${duration}")
	PHASE_NOTES+=("$(tr '\n' ' ' <"${note_file}" | sed -E 's/[[:space:]]+/ /g; s/^ //; s/ $//')")
	PHASE_LOGS+=("${log}")

	printf '    %-8s %ss\n' "${result}" "${duration}"
	if [ "${result}" = "FAIL" ]; then
		printf '\nacceptance: FAIL in phase "%s" (exit %s); tail of %s\n' \
			"${name}" "${status}" "${log}" >&2
		tail -n 80 "${log}" >&2 || true
		printf '\n' >&2
	fi
	CURRENT_PHASE_LOG=""
	CURRENT_PHASE_NOTE=""
}

port_has_listener() {
	local port="$1"
	if command -v ss >/dev/null 2>&1; then
		[ -n "$(ss -H -ltn "sport = :${port}" 2>/dev/null || true)" ]
	elif command -v lsof >/dev/null 2>&1; then
		lsof -nP -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1
	else
		(exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null
	fi
}

phase_go_vet() {
	printf '+ (cd %s && go vet ./...)\n' "${REPO_ROOT}"
	(cd "${REPO_ROOT}" && go vet ./...)
}

phase_go_test() {
	printf '+ (cd %s && go test ./... -count=1)\n' "${REPO_ROOT}"
	(cd "${REPO_ROOT}" && go test ./... -count=1)
}

phase_go_race() {
	if [ "${QUICK}" -eq 1 ]; then
		note "SKIP: --quick skips the race-detector phase"
		return 77
	fi
	printf '+ (cd %s && go test ./... -race -count=1)\n' "${REPO_ROOT}"
	(cd "${REPO_ROOT}" && go test ./... -race -count=1)
}

phase_ui_check() {
	printf '+ (cd ui && npm run check)\n'
	(cd "${REPO_ROOT}/ui" && npm run check)
}

phase_ui_test() {
	printf '+ (cd ui && npm test -- --run)\n'
	(cd "${REPO_ROOT}/ui" && npm test -- --run)
}

phase_ui_build() {
	printf '+ (cd ui && npm run build)\n'
	(cd "${REPO_ROOT}/ui" && npm run build)
}

phase_playwright() {
	if [ "${SKIP_PLAYWRIGHT}" -eq 1 ]; then
		note "SKIP: --skip-playwright"
		return 77
	fi
	if [ ! -x "${REPO_ROOT}/ui/node_modules/.bin/playwright" ]; then
		note "SKIP: Playwright is not installed under ui/node_modules; run npm ci in ui"
		return 77
	fi
	if port_has_listener 4173; then
		note "SKIP: port 4173 is already in use; refusing to reuse an unknown server"
		return 77
	fi

	local browser=""
	browser="$(cd "${REPO_ROOT}/ui" && node -e '
const { chromium } = require("@playwright/test");
process.stdout.write(chromium.executablePath());
' 2>>"${CURRENT_PHASE_LOG}")"
	local status=$?
	if [ "${status}" -ne 0 ] || [ -z "${browser}" ]; then
		note "SKIP: Playwright cannot resolve the Chromium executable; no browser install was attempted"
		return 77
	fi
	if [ ! -x "${browser}" ]; then
		note "SKIP: Playwright Chromium is missing at ${browser}; no browser install was attempted"
		return 77
	fi
	printf '+ (cd ui && npm run test:e2e) [chromium: %s]\n' "${browser}"
	(cd "${REPO_ROOT}/ui" && npm run test:e2e)
}

phase_make_build() {
	# This is also the restore step after release_test.sh's default-output case
	# rewrites dist/devstack with its test version. It is the only `npm ci`
	# path, so it must precede the UI phases on a clean checkout.
	printf '+ make -C %s build (restores final dist after the release archive test)\n' "${REPO_ROOT}"
	make -C "${REPO_ROOT}" build
}

phase_release_test() {
	printf '+ bash test/release/release_test.sh\n'
	bash "${REPO_ROOT}/test/release/release_test.sh"
}

phase_installer_test() {
	printf '+ bash test/release/installer_test.sh\n'
	bash "${REPO_ROOT}/test/release/installer_test.sh"
}

phase_workflow_test() {
	printf '+ bash test/release/workflow_test.sh\n'
	bash "${REPO_ROOT}/test/release/workflow_test.sh"
}

phase_install_dry_run() {
	printf '+ bash install.sh --dry-run\n'
	(cd "${REPO_ROOT}" && bash install.sh --dry-run)
}

phase_e2e() {
	local status=0
	printf '+ E2E_SKIP_BUILD=1 bash test/e2e/linux.sh\n'
	(cd "${REPO_ROOT}" && E2E_SKIP_BUILD=1 bash test/e2e/linux.sh)
	status=$?
	if [ "${status}" -ne 0 ]; then
		return "${status}"
	fi
	if grep -q '^e2e: SKIP external nginx mode' "${CURRENT_PHASE_LOG}"; then
		note "builtin flow PASS; $(grep -m1 '^e2e: SKIP external nginx mode' "${CURRENT_PHASE_LOG}")"
	fi
	if grep -q '^e2e: SKIP node vhost' "${CURRENT_PHASE_LOG}"; then
		note "$(grep -m1 '^e2e: SKIP node vhost' "${CURRENT_PHASE_LOG}")"
	fi
	return 0
}

phase_php_acceptance() {
	if [ ! -e /usr/local/php ]; then
		note "SKIP: /usr/local/php does not exist; real-PHP acceptance was not run"
		return 77
	fi
	if [ ! -x /usr/local/php/bin/php ]; then
		note "SKIP: /usr/local/php/bin/php is not executable; real-PHP acceptance was not run"
		return 77
	fi

	local status=0
	printf '+ DEVSTACK_PHP_ROOT=/usr/local/php go test ./test/php -run TestPHPSubsystemAcceptance -count=1 -v\n'
	(cd "${REPO_ROOT}" && DEVSTACK_PHP_ROOT=/usr/local/php \
		go test ./test/php -run TestPHPSubsystemAcceptance -count=1 -v)
	status=$?
	if [ "${status}" -ne 0 ]; then
		return "${status}"
	fi
	if grep -q '^--- SKIP: TestPHPSubsystemAcceptance' "${CURRENT_PHASE_LOG}"; then
		note "SKIP: the acceptance test skipped: $(grep -m1 '^[[:space:]]*.*skipping\|^[[:space:]]*.*SKIP' "${CURRENT_PHASE_LOG}" || true)"
		return 77
	fi
	if ! grep -q '^--- PASS: TestPHPSubsystemAcceptance' "${CURRENT_PHASE_LOG}"; then
		note "FAIL: the acceptance test did not report PASS or SKIP for TestPHPSubsystemAcceptance"
		return 1
	fi
	grep -E '^acceptance: (PHP|blog|api|xdebug|per-site|malformed|incomplete|failed-switch|candidate|built-in|updated|removal|migrated|doctor)' \
		"${CURRENT_PHASE_LOG}" | tail -n 12 || true
	return 0
}

phase_fpm_ports() {
	local listeners=""
	local status=0
	if command -v ss >/dev/null 2>&1; then
		listeners="$(ss -H -ltn '( sport >= :43000 and sport <= :43999 )')"
		status=$?
		if [ "${status}" -ne 0 ]; then
			note "FAIL: ss could not inspect TCP listeners on 43000-43999"
			return 1
		fi
	elif command -v lsof >/dev/null 2>&1; then
		listeners="$(lsof -nP -iTCP:43000-43999 -sTCP:LISTEN 2>/dev/null)"
		status=$?
		if [ "${status}" -ne 0 ] && [ "${status}" -ne 1 ]; then
			note "FAIL: lsof could not inspect TCP listeners on 43000-43999"
			return 1
		fi
	else
		note "SKIP: neither ss nor lsof is available to inspect 43000-43999"
		return 77
	fi
	if [ -n "${listeners}" ]; then
		printf '%s\n' "${listeners}"
		note "FAIL: TCP listeners remain on 43000-43999 after the acceptance phases"
		return 1
	fi
	printf 'no TCP listeners on 43000-43999\n'
	return 0
}

catalog_php_count() {
	local catalog="$1"
	if command -v jq >/dev/null 2>&1; then
		jq -e '.entries | type == "array"' "${catalog}" >/dev/null 2>&1 || return 2
		jq -e '[.entries[] | select((.kind != "node") and (.kind != "php"))] | length == 0' \
			"${catalog}" >/dev/null 2>&1 || return 3
		jq -r '[.entries[] | select(.kind == "php")] | length' "${catalog}" || return 2
		return 0
	fi
	if command -v python3 >/dev/null 2>&1; then
		python3 - "${catalog}" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        document = json.load(handle)
except Exception as exc:  # noqa: BLE001 - report any parser failure
    print(f"catalog JSON invalid: {exc}", file=sys.stderr)
    raise SystemExit(2)

entries = document.get("entries") if isinstance(document, dict) else None
if not isinstance(entries, list):
    print("catalog has no entries array", file=sys.stderr)
    raise SystemExit(3)
for entry in entries:
    if not isinstance(entry, dict) or entry.get("kind") not in ("node", "php"):
        print("catalog has an entry with an unexpected kind", file=sys.stderr)
        raise SystemExit(4)
print(sum(1 for entry in entries if entry.get("kind") == "php"))
PY
		return $?
	fi
	return 4
}

phase_catalog_gate() {
	local catalog="${REPO_ROOT}/internal/catalog/catalog.json"
	local status=0
	local count=""

	if [ ! -f "${catalog}" ]; then
		note "FAIL: ${catalog} is missing; the production catalog cannot be validated"
		return 1
	fi

	count="$(catalog_php_count "${catalog}")"
	status=$?

	case "${status}" in
	0) ;;
	2)
		note "FAIL: ${catalog} is not valid catalog JSON"
		return 1
		;;
	3)
		note "FAIL: ${catalog} has no entries array"
		return 1
		;;
	4)
		note "FAIL: ${catalog} has an entry with an unexpected kind, or neither jq nor python3 is available"
		return 1
		;;
	*)
		note "FAIL: catalog validation failed with status ${status}"
		return 1
		;;
	esac
	if ! [[ "${count}" =~ ^[0-9]+$ ]]; then
		note "FAIL: catalog parser returned a non-numeric PHP entry count: ${count}"
		return 1
	fi
	if [ "${count}" -ne 0 ]; then
		note "FAIL: ${catalog} contains ${count} PHP entries; this runner cannot verify them without published 8.2-8.5 artifacts"
		return 1
	fi
	note "BLOCKED: ${catalog} is valid and Node-only (0 PHP entries); no real 8.2-8.5 artifacts are published"
	return 78
}

phase_php_side_by_side() {
	note "BLOCKED: real PHP 8.2 and 8.5 side-by-side acceptance needs published checksummed artifacts and catalog entries"
	return 78
}

phase_windows_wsl() {
	note "BLOCKED: Windows/WSL is deferred by user request (Plan 3); no Windows build was attempted"
	return 78
}

phase_apache_https() {
	local modules=""
	if command -v apachectl >/dev/null 2>&1; then
		modules="$(apachectl -M 2>/dev/null || true)"
	fi
	if [ -z "${modules}" ] && command -v httpd >/dev/null 2>&1; then
		modules="$(httpd -M 2>/dev/null || true)"
	fi
	if [ -z "${modules}" ]; then
		note "SKIP: Apache modules could not be inspected; no Apache HTTPS check was attempted"
		return 77
	fi
	if ! printf '%s\n' "${modules}" | grep -q 'ssl_module'; then
		note "SKIP: Apache ssl_module is not loaded; Apache HTTPS vhost is not tested"
		return 77
	fi
	note "SKIP: no separate Apache HTTPS service test in this runner; the PHP acceptance phase covers the config test when mod_ssl is available"
	return 77
}

phase_manual_checks() {
	note "SKIP: browser trust, root install/uninstall on 80/443, and live UI checks are MANUAL and were not run"
	return 77
}

phase_inflight() {
	note "SKIP: in-flight request continuity across a successful PHP swap is NOT MEASURED"
	return 77
}

print_summary() {
	local i
	local failures=0
	local passes=0
	local skips=0
	local blocked=0
	local notes=""
	local log_name=""

	SUMMARY_PRINTED=1
	printf '\n'
	printf '%-10s %-34s %5s %-30s %s\n' "STATUS" "PHASE" "SECS" "LOG" "NOTE"
	printf '%-10s %-34s %5s %-30s %s\n' "----------" "----------------------------------" "-----" "------------------------------" "----"
	for i in "${!PHASE_NAMES[@]}"; do
		notes="${PHASE_NOTES[$i]}"
		log_name="${PHASE_LOGS[$i]#"${LOG_DIR}/"}"
		printf '%-10s %-34s %5s %-30s %s\n' \
			"${PHASE_RESULTS[$i]}" "${PHASE_NAMES[$i]}" "${PHASE_SECONDS[$i]}" "${log_name}" "${notes}"
		case "${PHASE_RESULTS[$i]}" in
		PASS) passes=$((passes + 1)) ;;
		SKIP) skips=$((skips + 1)) ;;
		BLOCKED) blocked=$((blocked + 1)) ;;
		FAIL) failures=$((failures + 1)) ;;
		esac
	done
	printf '\nTOTAL: %d PASS, %d FAIL, %d SKIP, %d BLOCKED\n' \
		"${passes}" "${failures}" "${skips}" "${blocked}"
	printf 'Logs: %s\n' "${LOG_DIR}"
	if [ "${failures}" -ne 0 ]; then
		printf 'RESULT: FAIL (%d phase(s) failed)\n' "${failures}"
		return 1
	fi
	printf 'RESULT: PASS (no FAIL; SKIP/BLOCKED are not passes)\n'
	return 0
}

# Order matters for a clean checkout:
# - `release archive test` intentionally rewrites dist/devstack with a test
#   version, so `make build` follows it and restores the final binaries.
# - `make build` is the only `npm ci` path, so it precedes the UI phases.
# - The installer test and E2E then use the restored final binaries.
run_phase "go vet" phase_go_vet
run_phase "go test" phase_go_test
run_phase "go test race" phase_go_race
run_phase "release archive test" phase_release_test
run_phase "make build" phase_make_build
run_phase "ui check" phase_ui_check
run_phase "ui unit tests" phase_ui_test
run_phase "ui build" phase_ui_build
run_phase "ui Playwright" phase_playwright
run_phase "installer test" phase_installer_test
run_phase "release workflow test" phase_workflow_test
run_phase "installer dry-run" phase_install_dry_run
run_phase "linux E2E" phase_e2e
run_phase "real PHP acceptance" phase_php_acceptance
run_phase "FPM listener check" phase_fpm_ports
run_phase "PHP catalog gate" phase_catalog_gate
run_phase "PHP 8.2/8.5 side-by-side" phase_php_side_by_side
run_phase "Windows/WSL" phase_windows_wsl
run_phase "Apache HTTPS" phase_apache_https
run_phase "manual checks" phase_manual_checks
run_phase "in-flight continuity" phase_inflight

print_summary
