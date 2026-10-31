#!/usr/bin/env bash
# Tiny PASS/FAIL check ledger shared by the cycle 4 scenario scripts.
set -Eeuo pipefail

declare -gA c4_passes=()
declare -gA c4_failures=()
declare -gA c4_notes=()

c4_check_init() {
	local scenario="$1"
	c4_passes["${scenario}"]=0
	c4_failures["${scenario}"]=0
	mkdir -p "${evidence_dir}/${scenario}"
	: >"${evidence_dir}/${scenario}/checks.log"
}

c4_pass() {
	local scenario="$1" label="$2"
	c4_passes["${scenario}"]=$((c4_passes["${scenario}"] + 1))
	printf 'PASS %s: %s\n' "${scenario}" "${label}" | tee -a "${evidence_dir}/${scenario}/checks.log"
}

c4_fail() {
	local scenario="$1" label="$2" detail="${3:-}"
	c4_failures["${scenario}"]=$((c4_failures["${scenario}"] + 1))
	printf 'FAIL %s: %s%s\n' "${scenario}" "${label}" "${detail:+ (${detail})}" | tee -a "${evidence_dir}/${scenario}/checks.log" >&2
}

c4_note() {
	local scenario="$1" text="$2"
	printf '%s\n' "${text}" >>"${evidence_dir}/${scenario}/notes.log"
}

c4_expect_code() {
	local scenario="$1" label="$2" want="$3"
	shift 3
	local output status
	output="$("$@" 2>&1)" && status=0 || status=$?
	if [ "${status}" -eq 0 ]; then
		c4_fail "${scenario}" "${label}" "command succeeded; expected ${want}"
		return 0
	fi
	if ! printf '%s' "${output}" | grep -q "\"code\":\"${want}\""; then
		c4_fail "${scenario}" "${label}" "expected ${want}; got $(printf '%s' "${output}" | grep -o '"code":"[^"]*"' | head -1)"
		return 0
	fi
	c4_pass "${scenario}" "${label}"
	return 0
}

c4_expect_failure() {
	local scenario="$1" label="$2"
	shift 2
	if "$@" >/dev/null 2>&1; then
		c4_fail "${scenario}" "${label}" "command succeeded; expected failure"
		return 0
	fi
	c4_pass "${scenario}" "${label}"
	return 0
}

c4_assert_eq() {
	local scenario="$1" label="$2" got="$3" want="$4"
	if [ "${got}" = "${want}" ]; then
		c4_pass "${scenario}" "${label}"
		return 0
	fi
	c4_fail "${scenario}" "${label}" "got '${got}', want '${want}'"
	return 0
}

c4_assert_contains() {
	local scenario="$1" label="$2" haystack="$3" needle="$4"
	case "${haystack}" in
	*"${needle}"*)
		c4_pass "${scenario}" "${label}"
		return 0
		;;
	esac
	c4_fail "${scenario}" "${label}" "missing '${needle}'"
	return 0
}

c4_assert_not_contains() {
	local scenario="$1" label="$2" haystack="$3" needle="$4"
	case "${haystack}" in
	*"${needle}"*)
		c4_fail "${scenario}" "${label}" "unexpected '${needle}'"
		return 0
		;;
	esac
	c4_pass "${scenario}" "${label}"
	return 0
}

c4_scenario_status() {
	local scenario="$1"
	if [ "${c4_failures["${scenario}"]}" -gt 0 ]; then
		printf 'FAIL'
	else
		printf 'PASS'
	fi
}

c4_scenario_summary() {
	local scenario="$1"
	printf '%s: %s passed, %s failed\n' "${scenario}" "${c4_passes["${scenario}"]}" "${c4_failures["${scenario}"]}"
}
