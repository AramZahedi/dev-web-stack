#!/usr/bin/env bash
# Scan evidence and logs for secrets. Prints only redacted locations.
#
# Default roots: the run evidence directory, the disposable log directory, and
# the results file. Explicit paths may be passed. Secret-store files under
# */secrets/* are never scanned. Exact runtime secret values are matched with
# grep -F so a leaked generated password is caught even if it has no label.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

self_test=0
roots=()
while [ $# -gt 0 ]; do
	case "$1" in
	--self-test) self_test=1; shift ;;
	--home) home="$2"; shift 2 ;;
	-h | --help) printf 'usage: secret-scan.sh [--self-test] [--home DIR] [PATH...]\n'; exit 0 ;;
	*) roots+=("$1"); shift ;;
	esac
done
e2e_refresh_paths

pattern_file="$(mktemp "${TMPDIR:-/tmp}/devstack-e2e-secret-patterns.XXXXXX")"
chmod 600 "${pattern_file}"
trap 'rm -f "${pattern_file}"' EXIT

cat >"${pattern_file}" <<'EOF'
-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----
(mysql|mariadb|postgres|postgresql|redis)://[^[:space:]/@:]+:[^[:space:]/@]+@
(DB_PASSWORD|DATABASE_URL|MYSQL_PWD|PGPASSWORD|REDISCLI_AUTH|REDIS_PASSWORD|DEVSTACK_TOKEN|INSTALL_TOKEN|GITHUB_TOKEN|GH_TOKEN)[[:space:]]*[:=][[:space:]]*[^[:space:]"']+
(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16})
"password"[[:space:]]*:[[:space:]]*"[^"]+"
password[[:space:]]*=[[:space:]]*[A-Za-z0-9+/=_-]{16,}
EOF

# Exact generated values. Each file under secrets/ is one value.
if [ -d "${secrets_dir}" ]; then
	for secret_file in "${secrets_dir}"/*; do
		[ -f "${secret_file}" ] || continue
		[ -s "${secret_file}" ] || continue
		cat "${secret_file}" >>"${pattern_file}"
		printf '\n' >>"${pattern_file}"
	done
fi

scan_file() {
	local file="$1" hit=0 pattern
	case "${file}" in
	*/secrets/* | *.acl | *.tgz | *.tar.gz | *.zip | *.png | *.jpg | *.jpeg | *.woff | *.woff2 | *.exe | *.dll) return 0 ;;
	esac
	[ -f "${file}" ] || return 0
	# -I skips binary files; evidence must be text for a line-level hit.
	while IFS= read -r pattern; do
		[ -n "${pattern}" ] || continue
		if grep -IqE -- "${pattern}" "${file}" 2>/dev/null; then
			printf 'REDACTED %s pattern=generic-%s\n' "${file}" "$(printf '%s' "${pattern}" | tr -cd 'A-Za-z0-9' | cut -c1-24)"
			hit=1
		fi
	done < <(head -n 6 "${pattern_file}")
	if [ "$(wc -l <"${pattern_file}")" -gt 6 ]; then
		if tail -n +7 "${pattern_file}" | grep -IqF -f - "${file}" 2>/dev/null; then
			printf 'REDACTED %s pattern=exact-runtime-secret\n' "${file}"
			hit=1
		fi
	fi
	return "${hit}"
}

run_scan() {
	local path hits=0 file
	for path in "$@"; do
		[ -e "${path}" ] || continue
		if [ -d "${path}" ]; then
			while IFS= read -r -d '' file; do
				if ! scan_file "${file}"; then
					hits=$((hits + 1))
				fi
			done < <(find "${path}" -type f -print0 2>/dev/null)
		else
			if ! scan_file "${path}"; then
				hits=$((hits + 1))
			fi
		fi
	done
	if [ "${hits}" -gt 0 ]; then
		printf 'secret-scan: FAIL (%s file(s) with redacted hits)\n' "${hits}" >&2
		return 1
	fi
	printf 'secret-scan: OK (%s root(s))\n' "$#"
}

if [ "${self_test}" = "1" ]; then
	tmp="$(mktemp -d "${TMPDIR:-/tmp}/devstack-e2e-scan-selftest.XXXXXX")"
	# shellcheck disable=SC2064
	trap 'rm -rf "${tmp}"; rm -f "${pattern_file}"' EXIT
	printf 'DATABASE_URL=mysql://demo:not-a-real-password@127.0.0.1/db\n' >"${tmp}/leak.log"
	if run_scan "${tmp}" >/dev/null 2>&1; then
		printf 'secret-scan: self-test FAILED (did not detect a fake secret)\n' >&2
		exit 1
	fi
	printf 'clean evidence\n' >"${tmp}/clean.log"
	rm -f "${tmp}/leak.log"
	if ! run_scan "${tmp}" >/dev/null 2>&1; then
		printf 'secret-scan: self-test FAILED (false positive on clean evidence)\n' >&2
		exit 1
	fi
	printf 'secret-scan: self-test OK\n'
	exit 0
fi

if [ "${#roots[@]}" -eq 0 ]; then
	[ -d "${evidence_dir}" ] && roots+=("${evidence_dir}")
	[ -d "${logs_dir}" ] && roots+=("${logs_dir}")
	[ -f "${results_file}" ] && roots+=("${results_file}")
fi
if [ "${#roots[@]}" -eq 0 ]; then
	printf 'secret-scan: no evidence paths to scan\n'
	exit 0
fi
run_scan "${roots[@]}"
