#!/usr/bin/env bash
# Snapshot, restore, and verify a disposable harness home.
#
# This is the rollback rehearsal for Linux. It never touches the real install.
# The Windows helper handover rollback is separate: windows/rollback.ps1.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

command_name="${1:-}"
shift || true
snapshot=""
while [ $# -gt 0 ]; do
	case "$1" in
	--home) home="$2"; shift 2 ;;
	--snapshot | --out) snapshot="$2"; shift 2 ;;
	-h | --help) printf 'usage: rollback.sh snapshot|restore|verify [--home DIR] [--snapshot FILE]\n'; exit 0 ;;
	*) printf 'rollback.sh: unknown argument %s\n' "$1" >&2; exit 2 ;;
	esac
done
e2e_refresh_paths
e2e_guard

snapshot_path() {
	if [ -n "${snapshot}" ]; then
		printf '%s\n' "${snapshot}"
	else
		printf '%s.snapshot.tgz\n' "${home}"
	fi
}

verify_home() {
	[ -d "${home}" ] || e2e_die "home does not exist: ${home}"
	[ -f "${home}/run-manifest.json" ] || e2e_die "home is missing run-manifest.json"
	python3 -m json.tool "${home}/run-manifest.json" >/dev/null || e2e_die "run-manifest.json is invalid"
	[ -f "${home}/sites/links.json" ] || e2e_die "home is missing sites/links.json"
	python3 -m json.tool "${home}/sites/links.json" >/dev/null || e2e_die "sites/links.json is invalid"
	if [ -f "${home}/state.db" ] && command -v sqlite3 >/dev/null 2>&1; then
		[ "$(sqlite3 "${home}/state.db" 'PRAGMA integrity_check;')" = "ok" ] ||
			e2e_die "state.db integrity check failed"
	fi
	e2e_log "home verification OK: ${home}"
}

do_snapshot() {
	local out parent base
	out="$(snapshot_path)"
	[ -d "${home}" ] || e2e_die "home does not exist: ${home}"
	verify_home >/dev/null
	parent="$(dirname "${home}")"
	base="$(basename "${home}")"
	umask 077
	tar -czf "${out}" -C "${parent}" "${base}"
	e2e_sha256 "${out}" >"${out}.sha256"
	tar -tzf "${out}" >/dev/null || e2e_die "snapshot archive is invalid"
	e2e_log "SNAPSHOT OK ${out} ($(stat -c '%s' "${out}") bytes)"
}

do_restore() {
	local out tmp base parent current
	out="$(snapshot_path)"
	[ -f "${out}" ] || e2e_die "snapshot not found: ${out}"
	if [ -f "${out}.sha256" ]; then
		[ "$(e2e_sha256 "${out}")" = "$(cat "${out}.sha256")" ] ||
			e2e_die "snapshot checksum mismatch"
	fi
	base="$(basename "${home}")"
	tmp="$(mktemp -d "${TMPDIR:-/tmp}/devstack-e2e-rollback.XXXXXX")"
	# shellcheck disable=SC2064
	trap 'rm -rf "${tmp}"' EXIT
	tar -xzf "${out}" -C "${tmp}"
	[ -d "${tmp}/${base}" ] || e2e_die "snapshot does not contain ${base}"
	if [ -f "${tmp}/${base}/state.db" ] && command -v sqlite3 >/dev/null 2>&1; then
		[ "$(sqlite3 "${tmp}/${base}/state.db" 'PRAGMA integrity_check;')" = "ok" ] ||
			e2e_die "snapshot state.db integrity check failed"
	fi
	parent="$(dirname "${home}")"
	mkdir -p "${parent}"
	current="${home}.pre-rollback-$(date -u +%Y%m%dT%H%M%SZ)-$$"
	if [ -d "${home}" ]; then
		mv "${home}" "${current}"
	fi
	if ! mv "${tmp}/${base}" "${home}"; then
		[ -d "${current}" ] && mv "${current}" "${home}"
		e2e_die "restore failed; previous home was put back"
	fi
	if ! (verify_home); then
		rm -rf "${home}"
		[ -d "${current}" ] && mv "${current}" "${home}"
		e2e_die "restored home failed verification; previous home was put back"
	fi
	rm -rf "${current}"
	trap - EXIT
	rm -rf "${tmp}"
	e2e_log "ROLLBACK OK ${home}"
}

case "${command_name}" in
snapshot) do_snapshot ;;
restore) do_restore ;;
verify) verify_home ;;
*) printf 'usage: rollback.sh snapshot|restore|verify [--home DIR] [--snapshot FILE]\n' >&2; exit 2 ;;
esac
