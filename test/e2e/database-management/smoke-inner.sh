#!/usr/bin/env bash
# R12 harness smoke: start pinned containers, seed, verify, scan, snapshot,
# mutate, roll back, and clean up. This is not an E2E scenario; it proves the
# harness itself is usable and isolated.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

e2e_guard
e2e_refresh_paths
e2e_require_docker
e2e_init_dirs
snapshot="${home}.snapshot.tgz"
cleaned=0

copy_logs() {
	if [ -n "${E2E_EVIDENCE_DIR:-}" ] && [ -d "${logs_dir}" ]; then
		mkdir -p "${E2E_EVIDENCE_DIR}"
		cp -a "${logs_dir}" "${E2E_EVIDENCE_DIR}/logs" 2>/dev/null || true
	fi
}

cleanup_all() {
	[ "${cleaned}" = "1" ] && return 0
	cleaned=1
	copy_logs
	"${e2e_root}/cleanup.sh" --home "${home}" >/dev/null 2>&1 || true
	rm -f "${snapshot}" "${snapshot}.sha256"
}
on_error() {
	local code=$?
	copy_logs
	cleanup_all
	exit "${code}"
}
trap on_error ERR
trap cleanup_all EXIT

e2e_section "R12 harness smoke"
e2e_log "run key ${run_key}; home ${home}; network ${network}"

"${e2e_root}/fixtures/generate.sh" --out "${home}/fixtures/dumps" --check
"${e2e_root}/fixtures/generate.sh" --out "${home}/fixtures/dumps" \
	--redis-acl "${home}/redis/fixture" --secret-dir "${secrets_dir}"

e2e_start_all
"${e2e_root}/seed.sh" --home "${home}" --engine all
"${e2e_root}/verify.sh" --home "${home}" --engine all

e2e_section "secret scanner"
"${e2e_root}/secret-scan.sh" --self-test
"${e2e_root}/secret-scan.sh" "${logs_dir}" "${E2E_EVIDENCE_DIR:-${evidence_dir}}" ||
	e2e_die "secret scanner found a secret"

e2e_section "rollback rehearsal"
"${e2e_root}/rollback.sh" snapshot --home "${home}" --snapshot "${snapshot}"
printf 'mutated\n' >"${home}/state/mutation.txt"
rm -f "${home}/sites/links.json"
"${e2e_root}/rollback.sh" restore --home "${home}" --snapshot "${snapshot}"
[ -f "${home}/sites/links.json" ] || e2e_die "rollback did not restore sites/links.json"
[ ! -f "${home}/state/mutation.txt" ] || e2e_die "rollback did not remove the mutation"
"${e2e_root}/rollback.sh" verify --home "${home}"

e2e_section "cleanup"
copy_logs
"${e2e_root}/cleanup.sh" --home "${home}"
[ ! -d "${home}" ] || e2e_die "cleanup left the disposable home"
if docker ps -a --format '{{.Names}}' | grep -Eq "^${prefix}-"; then
	e2e_die "cleanup left harness containers"
fi
if docker network inspect "${network}" >/dev/null 2>&1; then
	e2e_die "cleanup left the harness network"
fi
rm -f "${snapshot}" "${snapshot}.sha256"
cleaned=1
e2e_log "SMOKE OK"
