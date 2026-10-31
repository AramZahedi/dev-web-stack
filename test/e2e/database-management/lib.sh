#!/usr/bin/env bash
# Shared library for the database-management E2E harness.
#
# Source this file; do not execute it. It defines the disposable run identity,
# paths, Docker names, secret handling, and engine helpers. Every helper is
# deliberately Linux/WSL only: the Windows live path is documented and gated
# separately under windows/.
#
# Safety model:
#   * every Docker object is named devstack-e2e-<run-key>-*;
#   * every home is under /tmp/devstack-e2e-* (or an explicit disposable root);
#   * containers publish no host ports by default;
#   * the real devstack distro, /data/devstack, and the real engine are refused.
set -Eeuo pipefail

E2E_LIB_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo="${E2E_REPO_ROOT:-$(cd -- "${E2E_LIB_DIR}/../../.." && pwd)}"
e2e_root="${E2E_ROOT:-${repo}/test/e2e/database-management}"
fixtures_dir="${E2E_FIXTURES_DIR:-${e2e_root}/fixtures}"

stamp="${E2E_STAMP:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
run_key="$(printf '%s' "${E2E_RUN_KEY:-${stamp}}" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9-' '-' | sed 's/-\{2,\}/-/g; s/^-//; s/-$//')"
prefix="devstack-e2e-${run_key}"
network="${E2E_NETWORK:-${prefix}-net}"
home="${E2E_HOME:-/tmp/devstack-e2e-${run_key}}"
ds="${E2E_DS:-${repo}/dist/e2e/database-management/bin/devstack}"
svc="${E2E_SVC:-mysql}"
id="${E2E_ID:-e2e_app}"
evidence_root="${E2E_EVIDENCE_ROOT:-${repo}/dist/e2e/database-management/evidence}"
evidence_dir="${E2E_EVIDENCE_DIR:-${evidence_root}/${run_key}}"
results_file="${E2E_RESULTS_FILE:-${repo}/dist/e2e/database-management/results.json}"
manifest="${home}/manifest.tsv"
secrets_dir="${home}/secrets"
logs_dir="${home}/logs"

# Pinned images. Keep these in lock-step with internal/services/catalog_database.go
# and catalog.go; verify.sh checks the local image digests against this list.
mysql_image="mysql:8.4.13@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69"
mariadb_image="mariadb:11.8.9@sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa"
postgres_image="postgres:18.6@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6"
redis_image="redis:8.8.3@sha256:418e397fd48d66e450af13baab123397f2d2580b07ef310c3a5dbe96c3d731f1"
phpmyadmin_image="phpmyadmin:5.2.3@sha256:62a50b1419fe55b9b4c3cd6f5aed03a18f8f5b4a236f24735d76400bea75a5ff"

e2e_log() { printf 'e2e: %s\n' "$*"; }
e2e_warn() { printf 'e2e: WARN: %s\n' "$*" >&2; }
e2e_die() { printf 'e2e: ERROR: %s\n' "$*" >&2; exit 1; }
e2e_section() { printf '\n== %s ==\n' "$*"; }

e2e_require_cmd() {
	local cmd
	for cmd in "$@"; do
		command -v "${cmd}" >/dev/null 2>&1 || e2e_die "required command not found: ${cmd}"
	done
}

e2e_sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

e2e_sha256_string() {
	if command -v sha256sum >/dev/null 2>&1; then
		printf '%s' "$1" | sha256sum | awk '{print $1}'
	else
		printf '%s' "$1" | shasum -a 256 | awk '{print $1}'
	fi
}

e2e_git_sha() {
	git -c safe.directory="${repo}" -C "${repo}" rev-parse HEAD 2>/dev/null || printf 'unknown'
}

e2e_secret() {
	if command -v openssl >/dev/null 2>&1; then
		openssl rand -hex 24
	else
		head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
	fi
}

# e2e_guard refuses shared/real environments. Call this before creating any
# container, home, or evidence file.
e2e_guard() {
	case "$(uname -s)" in
	Linux) ;;
	*) e2e_die "this harness is Linux/WSL only; use windows/ for the Windows path" ;;
	esac
	if [ "${WSL_DISTRO_NAME:-}" = "devstack" ] && [ "${E2E_ALLOW_REAL_DISTRO:-0}" != "1" ]; then
		e2e_die "refusing to run inside the real devstack WSL distro (set E2E_ALLOW_REAL_DISTRO=1 only for an approved LIN disposable clone)"
	fi
	case "${home}" in
	/tmp/devstack-e2e-* | /var/tmp/devstack-e2e-* | "${repo}"/dist/e2e/database-management/*) ;;
	*) e2e_die "refusing disposable home outside /tmp/devstack-e2e-*: ${home}" ;;
	esac
	case "${home}" in
	*"/data/devstack"* | *"C:\\Users\\Aram\\.devstack"*) e2e_die "refusing real install path as disposable home: ${home}" ;;
	esac
	if [ "${home}" = "/data/devstack" ] || [ "${home}" = "/data/devstack/" ]; then
		e2e_die "refusing /data/devstack"
	fi
	# Cycle 0 quarantined a stale CA profile script; clear inherited CA
	# variables defensively so curl/Node inside the disposable run do not use a
	# deleted bundle from the real install.
	unset SSL_CERT_FILE CURL_CA_BUNDLE NODE_EXTRA_CA_CERTS REQUESTS_CA_BUNDLE
	export E2E_STAMP="${stamp}" E2E_RUN_KEY="${run_key}" E2E_HOME="${home}"
}

e2e_require_docker() {
	e2e_require_cmd docker
	docker info >/dev/null 2>&1 || e2e_die "Docker is not available in this distro"
	if docker ps -a --format '{{.Names}}' | grep -qx 'devstack-mysql\|devstack-phpmyadmin'; then
		e2e_die "real devstack containers are visible in this Docker engine; refusing to run (use the Ubuntu disposable engine)"
	fi
}

e2e_init_dirs() {
	mkdir -p "${home}"/{secrets,logs,backups,sites,state,fixtures,redis}
	chmod 700 "${home}" "${secrets_dir}"
	if [ ! -f "${manifest}" ]; then
		: >"${manifest}"
	fi
	# shellcheck disable=SC2064
	trap 'e2e_cleanup_home_on_error' ERR
}

e2e_cleanup_home_on_error() {
	local code=$?
	e2e_warn "failure at line ${BASH_LINENO[0]:-?}; leaving ${home} for inspection"
	exit "${code}"
}

e2e_manifest_add() {
	local kind="$1" name="$2"
	printf '%s\t%s\n' "${kind}" "${name}" >>"${manifest}"
}

e2e_manifest_has() {
	local kind="$1" name="$2"
	[ -f "${manifest}" ] && awk -F '\t' -v k="${kind}" -v n="${name}" '$1==k && $2==n {found=1} END {exit !found}' "${manifest}"
}

e2e_container_name() { printf '%s-%s' "${prefix}" "$1"; }
e2e_volume_name() { printf '%s-%s-data' "${prefix}" "$1"; }

e2e_refresh_paths() {
	manifest="${home}/manifest.tsv"
	secrets_dir="${home}/secrets"
	logs_dir="${home}/logs"
}

e2e_pick_port() {
	local port attempt
	for attempt in $(seq 1 200); do
		port=$((20000 + (RANDOM % 20000)))
		if command -v ss >/dev/null 2>&1; then
			if [ -z "$(ss -H -ltn "sport = :${port}" 2>/dev/null || true)" ]; then
				printf '%s\n' "${port}"
				return 0
			fi
		elif ! (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
			printf '%s\n' "${port}"
			return 0
		fi
	done
	e2e_die "could not find a free high host port"
}

e2e_publish_args() {
	# No published ports by default. This is the safe path because the real
	# install owns 3306 and 8082 on Windows localhost. Sets the global
	# E2E_PUBLISH_OPTS array and records the chosen port in the manifest.
	E2E_PUBLISH_OPTS=()
	if [ "${E2E_PUBLISH_PORTS:-0}" != "1" ]; then
		return 0
	fi
	local container_port="$1" label="$2" host_port
	host_port="$(e2e_pick_port)"
	e2e_manifest_add port "${label}=${host_port}"
	E2E_PUBLISH_OPTS=(-p "127.0.0.1:${host_port}:${container_port}")
}

e2e_secret_set() {
	local name="$1" value="$2"
	umask 077
	printf '%s' "${value}" >"${secrets_dir}/${name}"
	chmod 600 "${secrets_dir}/${name}"
}

e2e_secret_get() {
	local name="$1"
	[ -f "${secrets_dir}/${name}" ] || e2e_die "missing secret ${name} (run seed.sh or start.sh first)"
	cat "${secrets_dir}/${name}"
}

e2e_secret_ensure() {
	local name="$1" value
	if [ -s "${secrets_dir}/${name}" ]; then
		return 0
	fi
	value="$(e2e_secret)"
	e2e_secret_set "${name}" "${value}"
}

e2e_secret_file() { printf '%s/%s' "${secrets_dir}" "$1"; }

e2e_engine_image() {
	case "$1" in
	mysql) printf '%s\n' "${mysql_image}" ;;
	mariadb) printf '%s\n' "${mariadb_image}" ;;
	postgres | postgresql) printf '%s\n' "${postgres_image}" ;;
	redis) printf '%s\n' "${redis_image}" ;;
	phpmyadmin) printf '%s\n' "${phpmyadmin_image}" ;;
	*) e2e_die "unknown engine: $1" ;;
	esac
}

e2e_engine_client() {
	case "$1" in
	mysql) printf 'mysql\n' ;;
	mariadb) printf 'mariadb\n' ;;
	*) e2e_die "no SQL client for $1" ;;
	esac
}

e2e_start_service() {
	local service="$1"
	local container volume image
	container="$(e2e_container_name "${service}")"
	volume="$(e2e_volume_name "${service}")"
	image="$(e2e_engine_image "${service}")"

	if docker container inspect "${container}" >/dev/null 2>&1; then
		if [ "$(docker inspect -f '{{.State.Running}}' "${container}")" != "true" ]; then
			docker start "${container}" >/dev/null
		fi
		e2e_wait_service "${service}"
		return 0
	fi

	if ! docker image inspect "${image}" >/dev/null 2>&1; then
		e2e_die "pinned image is missing: ${image}; pull it in this disposable engine first"
	fi
	if ! docker network inspect "${network}" >/dev/null 2>&1; then
		docker network create --label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" "${network}" >/dev/null
		e2e_manifest_add network "${network}"
	fi
	if ! docker volume inspect "${volume}" >/dev/null 2>&1; then
		docker volume create --label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" "${volume}" >/dev/null
		e2e_manifest_add volume "${volume}"
	fi

	local -a publish_opts=()
	case "${service}" in
	mysql)
		e2e_secret_ensure mysql_root
		e2e_publish_args 3306 "${service}"
		publish_opts=("${E2E_PUBLISH_OPTS[@]}")
		docker run -d --name "${container}" --hostname "${service}" \
			--network "${network}" --network-alias "${service}" \
			--label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" \
			--restart=no \
			-e MYSQL_ROOT_PASSWORD_FILE=/run/e2e-secrets/mysql_root \
			-v "${secrets_dir}:/run/e2e-secrets:ro" \
			-v "${volume}:/var/lib/mysql" \
			"${publish_opts[@]}" \
			"${image}" >/dev/null
		;;
	mariadb)
		e2e_secret_ensure mariadb_root
		e2e_publish_args 3306 "${service}"
		publish_opts=("${E2E_PUBLISH_OPTS[@]}")
		docker run -d --name "${container}" --hostname "${service}" \
			--network "${network}" --network-alias "${service}" \
			--label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" \
			--restart=no \
			-e MARIADB_ROOT_PASSWORD_FILE=/run/e2e-secrets/mariadb_root \
			-v "${secrets_dir}:/run/e2e-secrets:ro" \
			-v "${volume}:/var/lib/mysql" \
			"${publish_opts[@]}" \
			"${image}" >/dev/null
		;;
	postgres)
		e2e_secret_ensure postgres_root
		e2e_publish_args 5432 "${service}"
		publish_opts=("${E2E_PUBLISH_OPTS[@]}")
		docker run -d --name "${container}" --hostname "${service}" \
			--network "${network}" --network-alias "${service}" \
			--label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" \
			--restart=no \
			-e POSTGRES_PASSWORD_FILE=/run/e2e-secrets/postgres_root \
			-e POSTGRES_USER=postgres \
			-e POSTGRES_DB=postgres \
			-v "${secrets_dir}:/run/e2e-secrets:ro" \
			-v "${volume}:/var/lib/postgresql" \
			"${publish_opts[@]}" \
			"${image}" >/dev/null
		;;
	redis)
		e2e_secret_ensure redis_root
		# Redis gets a real config file so ACL persistence can be exercised.
		# The file is mode 0600 under the disposable home and is never copied
		# into evidence.
		umask 077
		cat >"${home}/redis/redis.conf" <<EOF
bind 0.0.0.0
protected-mode no
port 6379
dir /data
appendonly yes
aclfile /data/users.acl
EOF
		chmod 644 "${home}/redis/redis.conf"
		# Redis loads aclfile after requirepass, so an empty ACL file would
		# clear the default password. Bootstrap the file with the default
		# user's hash instead of relying on requirepass.
		cat >"${home}/redis/bootstrap.acl" <<EOF
user default on #$(e2e_sha256_string "$(e2e_secret_get redis_root)") ~* &* +@all
EOF
		chmod 600 "${home}/redis/bootstrap.acl"
		if ! docker run --rm --label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" \
			-v "${home}/redis:/bootstrap:ro" -v "${volume}:/data" --entrypoint sh "${image}" \
			-c 'if [ ! -s /data/users.acl ]; then cp /bootstrap/bootstrap.acl /data/users.acl; fi; chown redis:redis /data/users.acl' >/dev/null; then
			e2e_die "could not initialize redis ACL file"
		fi
		e2e_publish_args 6379 "${service}"
		publish_opts=("${E2E_PUBLISH_OPTS[@]}")
		docker run -d --name "${container}" --hostname "${service}" \
			--network "${network}" --network-alias "${service}" \
			--label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" \
			--restart=no \
			-v "${home}/redis/redis.conf:/usr/local/etc/redis/redis.conf:ro" \
			-v "${volume}:/data" \
			"${publish_opts[@]}" \
			"${image}" redis-server /usr/local/etc/redis/redis.conf >/dev/null
		;;
	*) e2e_die "unsupported service: ${service}" ;;
	esac
	e2e_manifest_add container "${container}"
	e2e_wait_service "${service}"
}

e2e_wait_service() {
	local service="$1" container attempt
	container="$(e2e_container_name "${service}")"
	for attempt in $(seq 1 120); do
		case "${service}" in
		mysql)
			if docker exec -e MYSQL_PWD="$(e2e_secret_get mysql_root)" "${container}" \
				mysqladmin ping -h 127.0.0.1 -uroot --silent >/dev/null 2>&1; then
				return 0
			fi
			;;
		mariadb)
			if docker exec -e MYSQL_PWD="$(e2e_secret_get mariadb_root)" "${container}" \
				mariadb-admin ping -h 127.0.0.1 -uroot --silent >/dev/null 2>&1; then
				return 0
			fi
			;;
		postgres)
			if docker exec -e PGPASSWORD="$(e2e_secret_get postgres_root)" "${container}" \
				pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; then
				return 0
			fi
			;;
		redis)
			if docker exec -e REDISCLI_AUTH="$(e2e_secret_get redis_root)" "${container}" \
				redis-cli --no-auth-warning ping 2>/dev/null | grep -qx 'PONG'; then
				return 0
			fi
			;;
		esac
		if [ "$(docker inspect -f '{{.State.Running}}' "${container}" 2>/dev/null || echo false)" != "true" ]; then
			docker logs "${container}" >&2 2>&1 || true
			e2e_die "${service} container exited before becoming ready"
		fi
		sleep 1
	done
	docker logs "${container}" >&2 2>&1 || true
	e2e_die "${service} did not become ready"
}

e2e_start_all() {
	local service
	for service in mysql mariadb postgres redis; do
		e2e_section "start ${service}"
		e2e_start_service "${service}"
		e2e_log "${service} ready as $(e2e_container_name "${service}")"
	done
}

e2e_stop_service() {
	local service="$1" container
	container="$(e2e_container_name "${service}")"
	if docker container inspect "${container}" >/dev/null 2>&1; then
		docker stop --time 15 "${container}" >/dev/null 2>&1 || true
	fi
}

e2e_mysql_exec() {
	local service="$1" database="$2" sql_file="$3" client password
	client="$(e2e_engine_client "${service}")"
	if [ "${service}" = "mysql" ]; then
		password="$(e2e_secret_get mysql_root)"
	else
		password="$(e2e_secret_get mariadb_root)"
	fi
	if [ -n "${database}" ] && [ "${database}" != "-" ]; then
		docker exec -i -e MYSQL_PWD="${password}" "$(e2e_container_name "${service}")" \
			"${client}" -uroot --batch --skip-column-names "${database}" <"${sql_file}"
	else
		docker exec -i -e MYSQL_PWD="${password}" "$(e2e_container_name "${service}")" \
			"${client}" -uroot --batch --skip-column-names <"${sql_file}"
	fi
}

e2e_mysql_query() {
	local service="$1" database="$2" query="$3" client password
	client="$(e2e_engine_client "${service}")"
	if [ "${service}" = "mysql" ]; then
		password="$(e2e_secret_get mysql_root)"
	else
		password="$(e2e_secret_get mariadb_root)"
	fi
	if [ -n "${database}" ] && [ "${database}" != "-" ]; then
		docker exec -e MYSQL_PWD="${password}" "$(e2e_container_name "${service}")" \
			"${client}" -uroot --batch --skip-column-names "${database}" -e "${query}"
	else
		docker exec -e MYSQL_PWD="${password}" "$(e2e_container_name "${service}")" \
			"${client}" -uroot --batch --skip-column-names -e "${query}"
	fi
}

e2e_mysql_user_query() {
	local service="$1" database="$2" user="$3" password="$4" query="$5" client
	client="$(e2e_engine_client "${service}")"
	docker exec -e MYSQL_PWD="${password}" "$(e2e_container_name "${service}")" \
		"${client}" -u"${user}" --batch --skip-column-names "${database}" -e "${query}"
}

e2e_psql_exec() {
	local database="$1" sql_file="$2"
	docker exec -i -e PGPASSWORD="$(e2e_secret_get postgres_root)" \
		"$(e2e_container_name postgres)" psql -v ON_ERROR_STOP=1 -U postgres -d "${database}" <"${sql_file}"
}

e2e_psql_query() {
	local database="$1" query="$2"
	docker exec -e PGPASSWORD="$(e2e_secret_get postgres_root)" \
		"$(e2e_container_name postgres)" psql -v ON_ERROR_STOP=1 -U postgres -d "${database}" \
		--tuples-only --no-align --command "${query}"
}

e2e_psql_user_query() {
	local database="$1" user="$2" password="$3" query="$4"
	docker exec -e PGPASSWORD="${password}" "$(e2e_container_name postgres)" \
		psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "${user}" -d "${database}" --tuples-only --no-align --command "${query}"
}

# PostgreSQL's official image trusts 127.0.0.1/::1 in pg_hba.conf, so a
# password test from inside the server container cannot fail. Run the client
# from a one-shot container on the harness network instead; that path uses the
# final `host all all all scram-sha-256` rule.
e2e_psql_network_query() {
	local database="$1" user="$2" password="$3" query="$4"
	docker run --rm --network "${network}" \
		--label "devstack.e2e=1" --label "devstack.e2e.run=${run_key}" \
		-e PGPASSWORD="${password}" --entrypoint psql "${postgres_image}" \
		-v ON_ERROR_STOP=1 -h postgres -U "${user}" -d "${database}" \
		--tuples-only --no-align --command "${query}"
}

e2e_redis_cli() {
	local container
	container="$(e2e_container_name redis)"
	docker exec -e REDISCLI_AUTH="$(e2e_secret_get redis_root)" "${container}" \
		redis-cli --no-auth-warning "$@"
}

e2e_redis_user_cli() {
	local user="$1" password="$2"
	shift 2
	# --user needs -a/--askpass; REDISCLI_AUTH alone would authenticate as the
	# default user. --askpass reads the password from stdin, so it never lands
	# in argv or the container process list.
	printf '%s\n' "${password}" | docker exec -i "$(e2e_container_name redis)" \
		redis-cli --no-auth-warning --user "${user}" --askpass "$@"
}

e2e_dump_mysql() {
	local service="$1" database="$2" output="$3" container password dump
	container="$(e2e_container_name "${service}")"
	if [ "${service}" = "mysql" ]; then
		password="$(e2e_secret_get mysql_root)"
		dump="mysqldump"
	else
		password="$(e2e_secret_get mariadb_root)"
		dump="mariadb-dump"
	fi
	docker exec -e MYSQL_PWD="${password}" "${container}" \
		"${dump}" --single-transaction --routines --triggers --events --skip-comments "${database}" |
		gzip -n -9 >"${output}"
}

e2e_dump_postgres() {
	local database="$1" output="$2"
	docker exec -e PGPASSWORD="$(e2e_secret_get postgres_root)" \
		"$(e2e_container_name postgres)" pg_dump --no-owner --no-privileges -U postgres -d "${database}" |
		gzip -n -9 >"${output}"
}

e2e_restore_mysql() {
	local service="$1" database="$2" dump="$3" client password
	client="$(e2e_engine_client "${service}")"
	if [ "${service}" = "mysql" ]; then
		password="$(e2e_secret_get mysql_root)"
	else
		password="$(e2e_secret_get mariadb_root)"
	fi
	gzip -dc "${dump}" | docker exec -i -e MYSQL_PWD="${password}" \
		"$(e2e_container_name "${service}")" "${client}" -uroot "${database}"
}

e2e_restore_postgres() {
	local database="$1" dump="$2"
	gzip -dc "${dump}" | docker exec -i -e PGPASSWORD="$(e2e_secret_get postgres_root)" \
		"$(e2e_container_name postgres)" psql -v ON_ERROR_STOP=1 -U postgres -d "${database}"
}

e2e_drop_database() {
	local service="$1" database="$2"
	case "${service}" in
	mysql | mariadb) e2e_mysql_query "${service}" "-" "DROP DATABASE IF EXISTS \`${database}\`;" >/dev/null ;;
	postgres) e2e_psql_query postgres "DROP DATABASE IF EXISTS \"${database}\" WITH (FORCE);" >/dev/null ;;
	*) e2e_die "drop not supported for ${service}" ;;
	esac
}

e2e_create_database() {
	local service="$1" database="$2"
	case "${service}" in
	mysql | mariadb) e2e_mysql_query "${service}" "-" "CREATE DATABASE IF NOT EXISTS \`${database}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >/dev/null ;;
	postgres)
		if [ "$(e2e_psql_query postgres "SELECT 1 FROM pg_database WHERE datname = '${database}'")" != "1" ]; then
			e2e_psql_query postgres "CREATE DATABASE \"${database}\";" >/dev/null
		fi
		;;
	*) e2e_die "create not supported for ${service}" ;;
	esac
}

e2e_assert_contains() {
	local haystack="$1" needle="$2" label="${3:-value}"
	case "${haystack}" in
	*"${needle}"*) ;;
	*) e2e_die "${label} does not contain expected marker" ;;
	esac
}

e2e_assert_eq() {
	local got="$1" want="$2" label="${3:-value}"
	[ "${got}" = "${want}" ] || e2e_die "${label}: got '${got}', want '${want}'"
}

e2e_copy_evidence() {
	local source="$1" name="$2"
	mkdir -p "${evidence_dir}"
	cp -a "${source}" "${evidence_dir}/${name}"
}

e2e_json_escape() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\t/\\t/g; s/\r//g'
}

e2e_write_run_manifest() {
	local file="${home}/run-manifest.json"
	python3 - "$file" <<'PY'
import json, os, sys, datetime
path = sys.argv[1]
data = {
    "schema_version": 1,
    "run_key": os.environ.get("E2E_RUN_KEY", ""),
    "stamp": os.environ.get("E2E_STAMP", ""),
    "home": os.environ.get("E2E_HOME", ""),
    "network": os.environ.get("E2E_NETWORK", ""),
    "recorded_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "images": {
        "mysql": os.environ.get("E2E_MYSQL_IMAGE", ""),
        "mariadb": os.environ.get("E2E_MARIADB_IMAGE", ""),
        "postgres": os.environ.get("E2E_POSTGRES_IMAGE", ""),
        "redis": os.environ.get("E2E_REDIS_IMAGE", ""),
        "phpmyadmin": os.environ.get("E2E_PHPMYADMIN_IMAGE", ""),
    },
}
with open(path, "w", encoding="utf-8") as fh:
    json.dump(data, fh, indent=2, sort_keys=True)
    fh.write("\n")
PY
}
