#!/usr/bin/env bash
# Remove only resources created by this harness run.
#
# The manifest is authoritative: it lists the exact containers, network,
# volumes, and home created by seed.sh/start.sh. If the manifest is missing,
# cleanup falls back to the run label and the exact devstack-e2e-<run-key>-*
# prefix. It never removes the real devstack containers, network, or install.
set -Eeuo pipefail

e2e_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${e2e_root}/lib.sh"

dry_run=0
keep_home=0
while [ $# -gt 0 ]; do
	case "$1" in
	--home) home="$2"; shift 2 ;;
	--dry-run) dry_run=1; shift ;;
	--keep-home) keep_home=1; shift ;;
	-h | --help) printf 'usage: cleanup.sh [--home DIR] [--dry-run] [--keep-home]\n'; exit 0 ;;
	*) printf 'cleanup.sh: unknown argument %s\n' "$1" >&2; exit 2 ;;
	esac
done

e2e_refresh_paths
if [ "${dry_run}" = "1" ]; then
	e2e_log "dry-run: would remove resources for ${run_key}"
else
	e2e_require_docker
fi

remove_container() {
	local name="$1"
	case "${name}" in
	"${prefix}-"*) ;;
	*) e2e_warn "refusing to remove non-harness container ${name}"; return 0 ;;
	esac
	if ! docker container inspect "${name}" >/dev/null 2>&1; then
		return 0
	fi
	if [ "${dry_run}" = "1" ]; then
		e2e_log "dry-run: docker rm -f ${name}"
	else
		docker rm -f "${name}" >/dev/null 2>&1 || e2e_warn "could not remove container ${name}"
	fi
}

remove_volume() {
	local name="$1"
	case "${name}" in
	"${prefix}-"*) ;;
	*) e2e_warn "refusing to remove non-harness volume ${name}"; return 0 ;;
	esac
	if ! docker volume inspect "${name}" >/dev/null 2>&1; then
		return 0
	fi
	if [ "${dry_run}" = "1" ]; then
		e2e_log "dry-run: docker volume rm ${name}"
	else
		docker volume rm "${name}" >/dev/null 2>&1 || e2e_warn "could not remove volume ${name}"
	fi
}

remove_network() {
	local name="$1"
	case "${name}" in
	"${prefix}-"*) ;;
	*) e2e_warn "refusing to remove non-harness network ${name}"; return 0 ;;
	esac
	if ! docker network inspect "${name}" >/dev/null 2>&1; then
		return 0
	fi
	if [ "${dry_run}" = "1" ]; then
		e2e_log "dry-run: docker network rm ${name}"
	else
		docker network rm "${name}" >/dev/null 2>&1 || e2e_warn "could not remove network ${name}"
	fi
}

if [ -f "${manifest}" ]; then
	# Remove containers before volumes/network so dependency ordering does not
	# produce false failures.
	while IFS=$'\t' read -r kind name; do
		[ "${kind:-}" = "container" ] && remove_container "${name}"
	done <"${manifest}"
	while IFS=$'\t' read -r kind name; do
		[ "${kind:-}" = "volume" ] && remove_volume "${name}"
	done <"${manifest}"
	while IFS=$'\t' read -r kind name; do
		[ "${kind:-}" = "network" ] && remove_network "${name}"
	done <"${manifest}"
fi

# Belt-and-braces: remove anything carrying this run's label, even if the
# manifest was lost. Names must still match the harness prefix.
if [ "${dry_run}" != "1" ] && command -v docker >/dev/null 2>&1; then
	for container in $(docker ps -aq --filter "label=devstack.e2e.run=${run_key}" 2>/dev/null || true); do
		name="$(docker inspect -f '{{.Name}}' "${container}" 2>/dev/null | sed 's#^/##')"
		remove_container "${name}"
	done
	for volume in $(docker volume ls -q --filter "label=devstack.e2e.run=${run_key}" 2>/dev/null || true); do
		remove_volume "${volume}"
	done
	for net in $(docker network ls -q --filter "label=devstack.e2e.run=${run_key}" 2>/dev/null || true); do
		name="$(docker network inspect -f '{{.Name}}' "${net}" 2>/dev/null || true)"
		[ -n "${name}" ] && remove_network "${name}"
	done
fi

if [ "${keep_home}" = "0" ] && [ -d "${home}" ]; then
	case "${home}" in
	/tmp/devstack-e2e-* | /var/tmp/devstack-e2e-* | "${repo}"/dist/e2e/database-management/*)
		if [ "${dry_run}" = "1" ]; then
			e2e_log "dry-run: rm -rf ${home}"
		else
			rm -rf "${home}"
		fi
		;;
	*) e2e_warn "refusing to remove home outside disposable roots: ${home}" ;;
	esac
fi

# Snapshots live next to the home so a cleanup does not leave multi-megabyte
# rollback files behind. They are removed only when they match the same
# disposable prefix.
for artifact in "${home}.snapshot.tgz" "${home}.snapshot.tgz.sha256"; do
	[ -f "${artifact}" ] || continue
	case "${artifact}" in
	/tmp/devstack-e2e-* | /var/tmp/devstack-e2e-*)
		if [ "${dry_run}" = "1" ]; then
			e2e_log "dry-run: rm -f ${artifact}"
		else
			rm -f "${artifact}"
		fi
		;;
	esac
done

if [ "${dry_run}" != "1" ]; then
	leftovers="$(docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E "^${prefix}-" || true)"
	[ -z "${leftovers}" ] || e2e_die "leftover harness containers: ${leftovers}"
fi
e2e_log "CLEANUP OK (${run_key})"
