#!/usr/bin/env bash
# shellcheck disable=SC2016 # container-side "$(cat ...)" expansions are intentional
# Wave 1 database live smoke test.
#
# The test is opt-in and uses only Docker with a unique Compose project, a
# temporary root, bind-mounted data, and high loopback ports. It never touches
# the user's devstack root, /etc/hosts, system services, or ports 3306/5432/6379.
#
# Exit codes: 0 pass, 77 skipped (Docker unavailable or image pull unavailable),
# 1 failure.
set -euo pipefail

readonly MYSQL_IMAGE="mirror.gcr.io/library/mysql@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69"
readonly MARIADB_IMAGE="mirror.gcr.io/library/mariadb@sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa"
readonly POSTGRES_IMAGE="mirror.gcr.io/library/postgres@sha256:edb0c08dc33befd0262ab3b5cf1c4f3f8cfd8b09e88c05b45adee29d6761ddb6"
readonly REDIS_IMAGE="mirror.gcr.io/library/redis@sha256:418e397fd48d66e450af13baab123397f2d2580b07ef310c3a5dbe96c3d731f1"

readonly MYSQL_PORT=33306
readonly MARIADB_PORT=33307
readonly POSTGRES_PORT=35432
readonly REDIS_PORT=36379

skip() {
	printf 'SKIP: %s\n' "$*" >&2
	exit 77
}

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

if [[ "${DEVSTACK_DOCKER_TESTS:-0}" != "1" ]]; then
	skip "set DEVSTACK_DOCKER_TESTS=1 to run the database Docker smoke test"
fi

if ! command -v docker >/dev/null 2>&1; then
	skip "docker is not installed"
fi

# Accessing the daemon may require the docker group. Run each command through
# sg when direct access is denied.
if docker info >/dev/null 2>&1; then
	docker_cmd() { docker "$@"; }
elif command -v sg >/dev/null 2>&1 && sg docker -c 'docker info' >/dev/null 2>&1; then
	docker_cmd() { sg docker -c "$(printf '%q ' docker "$@")"; }
else
	skip "the Docker daemon is not accessible"
fi
if ! docker_cmd compose version >/dev/null 2>&1; then
	skip "docker compose is not installed"
fi

for image in "$MYSQL_IMAGE" "$MARIADB_IMAGE" "$POSTGRES_IMAGE" "$REDIS_IMAGE"; do
	if ! docker_cmd image inspect "$image" >/dev/null 2>&1; then
		if ! docker_cmd pull "$image" >/dev/null 2>&1; then
			skip "image $image is unavailable and could not be pulled (network or registry limit)"
		fi
	fi
done

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/devstack-wave1-databases.XXXXXXXX")" || skip "cannot create a temporary directory"
PROJECT="devstack-wave1-db-$$-$(date +%s)"
COMPOSE_FILE="$ROOT/compose.yaml"
export ROOT PROJECT COMPOSE_FILE

cleanup() {
	docker_cmd compose -p "$PROJECT" -f "$COMPOSE_FILE" down --remove-orphans >/dev/null 2>&1 || true
	if [[ -d "$ROOT" ]]; then
		# Database entrypoints hand data directories to their container UIDs.
		# Reclaim ownership inside a container so the temporary root can be
		# removed without sudo.
		docker_cmd run --rm --entrypoint chown -v "$ROOT:/cleanup" "$REDIS_IMAGE" \
			-R "$(id -u):$(id -g)" /cleanup >/dev/null 2>&1 || true
	fi
	rm -rf "$ROOT" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

compose() {
	docker_cmd compose -p "$PROJECT" -f "$COMPOSE_FILE" "$@"
}

new_secret() {
	if command -v openssl >/dev/null 2>&1; then
		openssl rand -hex 32
	else
		head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
	fi
}

umask 077
mkdir -p "$ROOT"/{mysql,mariadb,postgresql,redis}/{config,data,logs} \
	"$ROOT/postgresql/config/init" "$ROOT/secrets"/{mysql,mariadb,postgresql}
chmod 700 "$ROOT/secrets" "$ROOT/secrets"/*

write_secret() {
	local path=$1
	printf '%s' "$(new_secret)" >"$path"
	chmod 600 "$path"
}
for secret in mysql/root_password mysql/devstack_password \
	mariadb/root_password mariadb/devstack_password \
	postgresql/postgres_password postgresql/devstack_password; do
	write_secret "$ROOT/secrets/$secret"
done
chmod 755 "$ROOT"/{mysql,mariadb,postgresql,redis}/{config,data,logs} \
	"$ROOT/postgresql/config/init"

cat >"$ROOT/mysql/config/devstack.cnf" <<'EOF'
[mysqld]
skip-name-resolve
max_connections=256
character-set-server=utf8mb4
collation-server=utf8mb4_unicode_ci
EOF
cp "$ROOT/mysql/config/devstack.cnf" "$ROOT/mariadb/config/devstack.cnf"

cat >"$ROOT/postgresql/config/postgresql.conf" <<'EOF'
listen_addresses = '*'
port = 5432
data_directory = '/var/lib/postgresql/data'
hba_file = '/etc/postgresql/pg_hba.conf'
EOF

cat >"$ROOT/postgresql/config/pg_hba.conf" <<'EOF'
local   all             postgres                                trust
local   all             all                                     trust
host    all             all             127.0.0.1/32            scram-sha-256
host    all             all             ::1/128                 scram-sha-256
host    all             all             0.0.0.0/0               scram-sha-256
EOF

cat >"$ROOT/postgresql/config/init/10-devstack.sh" <<'EOF'
#!/bin/sh
set -eu
if [ -z "${POSTGRES_USER:-}" ]; then
	POSTGRES_USER=postgres
fi
if [ -z "${POSTGRES_DEVSTACK_PASSWORD:-}" ]; then
	echo "devstack bootstrap: POSTGRES_DEVSTACK_PASSWORD is not set" >&2
	exit 1
fi
password="$POSTGRES_DEVSTACK_PASSWORD"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<EOSQL
\set ds_password '$password'
SELECT format('CREATE ROLE devstack LOGIN PASSWORD %L', :'ds_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'devstack')
\gexec
SELECT format('ALTER ROLE devstack WITH LOGIN PASSWORD %L', :'ds_password')
\gexec
SELECT format('CREATE DATABASE devstack OWNER devstack')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'devstack')
\gexec
EOSQL
EOF
chmod 755 "$ROOT/postgresql/config/init/10-devstack.sh"
chmod 644 "$ROOT/mysql/config/devstack.cnf" "$ROOT/mariadb/config/devstack.cnf" \
	"$ROOT/postgresql/config/postgresql.conf" "$ROOT/postgresql/config/pg_hba.conf"

cat >"$ROOT/redis/config/redis.conf" <<'EOF'
appendonly yes
appendfilename "appendonly.aof"
dir /data
protected-mode no
EOF
chmod 644 "$ROOT/redis/config/redis.conf"

cat >"$COMPOSE_FILE" <<EOF
services:
  mysql:
    image: $MYSQL_IMAGE
    environment:
      MYSQL_ROOT_PASSWORD_FILE: /run/secrets/mysql_root_password
      MYSQL_DATABASE: devstack
      MYSQL_USER: devstack
      MYSQL_PASSWORD_FILE: /run/secrets/mysql_devstack_password
    ports:
      - "127.0.0.1:$MYSQL_PORT:3306"
    volumes:
      - $ROOT/mysql/config:/etc/mysql/conf.d:ro,z
      - $ROOT/mysql/data:/var/lib/mysql:z
      - $ROOT/mysql/logs:/var/log/mysql:z
      - $ROOT/secrets/mysql/root_password:/run/secrets/mysql_root_password:ro,z
      - $ROOT/secrets/mysql/devstack_password:/run/secrets/mysql_devstack_password:ro,z
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD-SHELL", "mysqladmin ping -h 127.0.0.1 --silent"]
      interval: 5s
      timeout: 5s
      start_period: 30s
      retries: 20

  mariadb:
    image: $MARIADB_IMAGE
    environment:
      MARIADB_ROOT_PASSWORD_FILE: /run/secrets/mariadb_root_password
      MARIADB_DATABASE: devstack
      MARIADB_USER: devstack
      MARIADB_PASSWORD_FILE: /run/secrets/mariadb_devstack_password
    ports:
      - "127.0.0.1:$MARIADB_PORT:3306"
    volumes:
      - $ROOT/mariadb/config:/etc/mysql/conf.d:ro,z
      - $ROOT/mariadb/data:/var/lib/mysql:z
      - $ROOT/mariadb/logs:/var/log/mysql:z
      - $ROOT/secrets/mariadb/root_password:/run/secrets/mariadb_root_password:ro,z
      - $ROOT/secrets/mariadb/devstack_password:/run/secrets/mariadb_devstack_password:ro,z
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 5s
      timeout: 5s
      start_period: 30s
      retries: 20

  postgresql:
    image: $POSTGRES_IMAGE
    entrypoint:
      - /bin/sh
      - -c
      - |
        set -eu
        POSTGRES_PASSWORD="\$(cat /run/secrets/postgresql_postgres_password)"
        POSTGRES_DEVSTACK_PASSWORD="\$(cat /run/secrets/postgresql_devstack_password)"
        export POSTGRES_PASSWORD POSTGRES_DEVSTACK_PASSWORD
        unset POSTGRES_PASSWORD_FILE POSTGRES_DEVSTACK_PASSWORD_FILE
        exec /usr/local/bin/docker-entrypoint.sh postgres -c config_file=/etc/postgresql/postgresql.conf
    environment:
      POSTGRES_PASSWORD_FILE: /run/secrets/postgresql_postgres_password
      POSTGRES_USER: postgres
      PGDATA: /var/lib/postgresql/data
    ports:
      - "127.0.0.1:$POSTGRES_PORT:5432"
    volumes:
      - $ROOT/postgresql/config/postgresql.conf:/etc/postgresql/postgresql.conf:ro,z
      - $ROOT/postgresql/config/pg_hba.conf:/etc/postgresql/pg_hba.conf:ro,z
      - $ROOT/postgresql/config/init:/docker-entrypoint-initdb.d:ro,z
      - $ROOT/postgresql/data:/var/lib/postgresql/data:z
      - $ROOT/secrets/postgresql/postgres_password:/run/secrets/postgresql_postgres_password:ro,z
      - $ROOT/secrets/postgresql/devstack_password:/run/secrets/postgresql_devstack_password:ro,z
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d postgres -h 127.0.0.1"]
      interval: 5s
      timeout: 5s
      start_period: 30s
      retries: 20

  redis:
    image: $REDIS_IMAGE
    command: ["redis-server", "/usr/local/etc/redis/redis.conf"]
    ports:
      - "127.0.0.1:$REDIS_PORT:6379"
    volumes:
      - $ROOT/redis/config/redis.conf:/usr/local/etc/redis/redis.conf:ro,z
      - $ROOT/redis/data:/data:z
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      start_period: 5s
      retries: 20
EOF

wait_healthy() {
	local deadline=$((SECONDS + 240)) output line svc
	while ((SECONDS < deadline)); do
		output="$(compose ps --format '{{.Service}} {{.Health}} {{.State}}' 2>/dev/null || true)"
		local all_ready=1
		for svc in mysql mariadb postgresql redis; do
			line="$(printf '%s\n' "$output" | awk -v svc="$svc" '$1 == svc { print $2 " " $3 }')"
			if [[ "$line" == *" exited" || "$line" == *" dead" ]]; then
				compose ps >&2 || true
				compose logs --tail=40 >&2 || true
				return 1
			fi
			if [[ "$line" != "healthy running" ]]; then
				all_ready=0
				break
			fi
		done
		if ((all_ready == 1)); then
			return 0
		fi
		sleep 3
	done
	compose ps >&2 || true
	compose logs --tail=40 >&2 || true
	return 1
}

# The single-quoted exec snippets below are evaluated inside the service
# containers; their "$(cat ...)" expansions must not run on the host.
mysql_query() {
	compose exec -T mysql sh -ec \
		'MYSQL_PWD="$(cat /run/secrets/mysql_devstack_password)" mysql -udevstack -h 127.0.0.1 -N devstack -e "$1"' _ "$1"
}

mariadb_query() {
	compose exec -T mariadb sh -ec \
		'MYSQL_PWD="$(cat /run/secrets/mariadb_devstack_password)" mariadb -udevstack -h 127.0.0.1 -N devstack -e "$1"' _ "$1"
}

postgres_query() {
	compose exec -T postgresql sh -ec \
		'PGPASSWORD="$(cat /run/secrets/postgresql_devstack_password)" psql -h 127.0.0.1 -U devstack -d devstack -tAc "$1"' _ "$1"
}

redis_query() {
	compose exec -T redis redis-cli "$@"
}

assert_equals() {
	local got=$1 want=$2 label=$3
	if [[ "$got" != "$want" ]]; then
		fail "$label: got '$got', want '$want'"
	fi
}

printf 'Starting Wave 1 database smoke project %s\n' "$PROJECT"
if [[ "${DEVSTACK_SMOKE_INSPECT:-0}" == "1" ]]; then
	compose config
	exit 0
fi
if ! compose up -d 2>"$ROOT/up.err"; then
	if grep -qiE 'toomanyrequests|rate.?limit|pull access denied|network is unreachable|temporary failure in name resolution' "$ROOT/up.err"; then
		skip "Docker could not pull an image (rate limit or network): $(tr '\n' ' ' <"$ROOT/up.err")"
	fi
	fail "docker compose up failed: $(tr '\n' ' ' <"$ROOT/up.err")"
fi
wait_healthy || fail "database containers did not become healthy"

# Secret-file logins for each engine.
assert_equals "$(mysql_query 'SELECT 1' | tr -d '[:space:]')" "1" "mysql secret login"
assert_equals "$(mariadb_query 'SELECT 1' | tr -d '[:space:]')" "1" "mariadb secret login"
assert_equals "$(postgres_query 'SELECT 1' | tr -d '[:space:]')" "1" "postgresql secret login"
assert_equals "$(redis_query ping)" "PONG" "redis ping"

# Host-side secret files are never world-readable.
for secret in \
	"$ROOT/secrets/mysql/root_password" \
	"$ROOT/secrets/mysql/devstack_password" \
	"$ROOT/secrets/mariadb/root_password" \
	"$ROOT/secrets/mariadb/devstack_password" \
	"$ROOT/secrets/postgresql/postgres_password" \
	"$ROOT/secrets/postgresql/devstack_password"; do
	mode="$(stat -c '%a' "$secret")"
	assert_equals "$mode" "600" "secret mode $(basename "$(dirname "$secret")")/$(basename "$secret")"
done

# Write markers, restart the project, and verify the bind-mounted data survives.
mysql_query "CREATE TABLE IF NOT EXISTS wave1_smoke (id INT PRIMARY KEY, note VARCHAR(32)); REPLACE INTO wave1_smoke VALUES (1, 'mysql');" >/dev/null
mariadb_query "CREATE TABLE IF NOT EXISTS wave1_smoke (id INT PRIMARY KEY, note VARCHAR(32)); REPLACE INTO wave1_smoke VALUES (1, 'mariadb');" >/dev/null
postgres_query "CREATE TABLE IF NOT EXISTS wave1_smoke (id INT PRIMARY KEY, note TEXT); INSERT INTO wave1_smoke VALUES (1, 'postgresql') ON CONFLICT (id) DO UPDATE SET note = EXCLUDED.note;" >/dev/null
redis_query SET wave1:smoke redis >/dev/null

compose stop >/dev/null
compose start >/dev/null
wait_healthy || fail "database containers did not become healthy after restart"

assert_equals "$(mysql_query 'SELECT note FROM wave1_smoke WHERE id = 1' | tr -d '[:space:]')" "mysql" "mysql data persistence"
assert_equals "$(mariadb_query 'SELECT note FROM wave1_smoke WHERE id = 1' | tr -d '[:space:]')" "mariadb" "mariadb data persistence"
assert_equals "$(postgres_query 'SELECT note FROM wave1_smoke WHERE id = 1' | tr -d '[:space:]')" "postgresql" "postgresql data persistence"
assert_equals "$(redis_query GET wave1:smoke)" "redis" "redis data persistence"

printf 'PASS: Wave 1 database smoke test\n'
