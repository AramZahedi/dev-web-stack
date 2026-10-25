#!/usr/bin/env bash
# shellcheck disable=SC2016 # container-side "$(cat ...)" expansions are intentional
# Wave 2 phpMyAdmin live smoke test.
#
# The test is opt-in and uses a temporary root, a unique Compose project, high
# loopback ports, and mirror.gcr.io images. It verifies:
#   - phpMyAdmin reaches the active MySQL-family container through the fixed
#     "db" Compose network alias;
#   - switching MySQL -> MariaDB on the same host port keeps phpMyAdmin healthy;
#   - the fixed MySQL account-rotation statement updates the devstack password
#     and the old password stops working.
#
# The catalog also carries PMA_HOST=host.docker.internal and the host-gateway
# extra_hosts entry. On native Linux the bridge gateway cannot reach a port
# published only on 127.0.0.1, so the generated config.user.inc.php points
# phpMyAdmin at the "db" alias that follows the active engine.
#
# It never touches the user's devstack root, /etc/hosts, system services, or the
# production ports 3306/8082.
#
# Exit codes: 0 pass, 77 skipped (Docker or image unavailable), 1 failure.
set -euo pipefail

readonly MYSQL_IMAGE="mirror.gcr.io/library/mysql:8.4.13@sha256:76f0358884cb92d9a086d24e5671c38048d3fef40f2588703d73eb55392d6b69"
readonly MARIADB_IMAGE="mirror.gcr.io/library/mariadb:11.8.9@sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa"
readonly PMA_IMAGE="mirror.gcr.io/library/phpmyadmin:5.2.3@sha256:62a50b1419fe55b9b4c3cd6f5aed03a18f8f5b4a236f24735d76400bea75a5ff"

readonly DB_PORT=33326
readonly PMA_PORT=58082

skip() {
	printf 'SKIP: %s\n' "$*" >&2
	exit 77
}

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

if [[ "${DEVSTACK_DOCKER_TESTS:-0}" != "1" ]]; then
	skip "set DEVSTACK_DOCKER_TESTS=1 to run the phpMyAdmin Docker smoke test"
fi

if ! command -v docker >/dev/null 2>&1; then
	skip "docker is not installed"
fi

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
if ! command -v curl >/dev/null 2>&1; then
	skip "curl is required for the HTTP probe"
fi

for image in "$MYSQL_IMAGE" "$MARIADB_IMAGE" "$PMA_IMAGE"; do
	if ! docker_cmd image inspect "$image" >/dev/null 2>&1; then
		if ! docker_cmd pull "$image" >/dev/null 2>&1; then
			skip "image $image is unavailable and could not be pulled (network or registry limit)"
		fi
	fi
done

ROOT="$(mktemp -d "${TMPDIR:-/tmp}/devstack-wave2-phpmyadmin.XXXXXXXX")" || skip "cannot create a temporary directory"
PROJECT="devstack-wave2-pma-$$-$(date +%s)"
COMPOSE_FILE="$ROOT/compose.yaml"
export ROOT PROJECT COMPOSE_FILE

cleanup() {
	docker_cmd compose -p "$PROJECT" -f "$COMPOSE_FILE" down --remove-orphans >/dev/null 2>&1 || true
	if [[ -d "$ROOT" ]]; then
		docker_cmd run --rm --entrypoint chown -v "$ROOT:/cleanup" "$MYSQL_IMAGE" \
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
mkdir -p "$ROOT"/mysql/{config,data,logs} "$ROOT"/mariadb/{config,data,logs} \
	"$ROOT/phpmyadmin" \
	"$ROOT/mysql/config/init" "$ROOT/mariadb/config/init" \
	"$ROOT/secrets"/{mysql,mariadb}
chmod 700 "$ROOT/secrets" "$ROOT/secrets"/*
chmod 755 "$ROOT/phpmyadmin"

write_secret() {
	local path=$1
	printf '%s' "$(new_secret)" >"$path"
	chmod 600 "$path"
}
for secret in mysql/root_password mysql/devstack_password \
	mariadb/root_password mariadb/devstack_password; do
	write_secret "$ROOT/secrets/$secret"
done
MYSQL_DEVSTACK_PASSWORD="$(cat "$ROOT/secrets/mysql/devstack_password")"
chmod 755 "$ROOT"/mysql/{config,data,logs} "$ROOT"/mariadb/{config,data,logs} \
	"$ROOT/mysql/config/init" "$ROOT/mariadb/config/init"

cat >"$ROOT/mysql/config/devstack.cnf" <<'EOF'
[mysqld]
skip-name-resolve
max_connections=256
character-set-server=utf8mb4
collation-server=utf8mb4_unicode_ci
EOF
cp "$ROOT/mysql/config/devstack.cnf" "$ROOT/mariadb/config/devstack.cnf"

cat >"$ROOT/mysql/config/init/10-devstack-admin.sql" <<'EOF'
GRANT ALL PRIVILEGES ON *.* TO 'devstack'@'%' WITH GRANT OPTION;
FLUSH PRIVILEGES;
EOF
cp "$ROOT/mysql/config/init/10-devstack-admin.sql" "$ROOT/mariadb/config/init/10-devstack-admin.sql"
chmod 644 "$ROOT/mysql/config/devstack.cnf" "$ROOT/mariadb/config/devstack.cnf" \
	"$ROOT/mysql/config/init/10-devstack-admin.sql" "$ROOT/mariadb/config/init/10-devstack-admin.sql"

cat >"$ROOT/phpmyadmin/config.user.inc.php" <<'EOF'
<?php
$cfg['Servers'][1]['host'] = 'db';
$cfg['Servers'][1]['port'] = '3306';
$cfg['Servers'][1]['auth_type'] = 'cookie';
EOF
chmod 644 "$ROOT/phpmyadmin/config.user.inc.php"

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
      - "127.0.0.1:$DB_PORT:3306"
    volumes:
      - $ROOT/mysql/config:/etc/mysql/conf.d:ro,z
      - $ROOT/mysql/config/init:/docker-entrypoint-initdb.d:ro,z
      - $ROOT/mysql/data:/var/lib/mysql:z
      - $ROOT/mysql/logs:/var/log/mysql:z
      - $ROOT/secrets/mysql/root_password:/run/secrets/mysql_root_password:ro,z
      - $ROOT/secrets/mysql/devstack_password:/run/secrets/mysql_devstack_password:ro,z
    networks:
      default:
        aliases:
          - db
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
      - "127.0.0.1:$DB_PORT:3306"
    volumes:
      - $ROOT/mariadb/config:/etc/mysql/conf.d:ro,z
      - $ROOT/mariadb/config/init:/docker-entrypoint-initdb.d:ro,z
      - $ROOT/mariadb/data:/var/lib/mysql:z
      - $ROOT/mariadb/logs:/var/log/mysql:z
      - $ROOT/secrets/mariadb/root_password:/run/secrets/mariadb_root_password:ro,z
      - $ROOT/secrets/mariadb/devstack_password:/run/secrets/mariadb_devstack_password:ro,z
    networks:
      default:
        aliases:
          - db
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "healthcheck.sh", "--connect", "--innodb_initialized"]
      interval: 5s
      timeout: 5s
      start_period: 30s
      retries: 20

  phpmyadmin:
    image: $PMA_IMAGE
    environment:
      PMA_HOST: host.docker.internal
      PMA_PORT: "$DB_PORT"
      PMA_ABSOLUTE_URI: "http://127.0.0.1:$PMA_PORT/"
      PMA_ARBITRARY: "0"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "127.0.0.1:$PMA_PORT:80"
    volumes:
      - $ROOT/phpmyadmin/config.user.inc.php:/etc/phpmyadmin/config.user.inc.php:ro,z
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "curl", "-fsS", "-o", "/dev/null", "http://127.0.0.1/"]
      interval: 5s
      timeout: 5s
      start_period: 10s
      retries: 20
EOF

docker_cmd compose -p "$PROJECT" -f "$COMPOSE_FILE" config >/dev/null || fail "docker compose config rejected the generated stack"

wait_healthy() {
	local service=$1
	local id
	for _ in $(seq 1 90); do
		id="$(compose ps -q "$service" 2>/dev/null || true)"
		if [[ -n "$id" ]]; then
			case "$(docker_cmd inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id" 2>/dev/null || true)" in
			healthy | running)
				return 0
				;;
			esac
		fi
		sleep 1
	done
	fail "service $service did not become healthy"
}

probe_phpmyadmin_http() {
	local code
	code="$(curl -fsS -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:$PMA_PORT/")" ||
		fail "phpMyAdmin HTTP probe failed"
	[[ "$code" == "200" ]] || fail "phpMyAdmin HTTP status $code, want 200"
}

probe_phpmyadmin_to_database() {
	local configured_host
	configured_host="$(compose exec -T phpmyadmin php -d error_reporting=0 -d display_errors=0 -r 'require "/etc/phpmyadmin/config.inc.php"; echo $cfg["Servers"][1]["host"];' 2>/dev/null | tr -d '[:space:]')" ||
		fail "phpMyAdmin config could not be loaded"
	[[ "$configured_host" == "db" ]] ||
		fail "phpMyAdmin effective host is $configured_host, want the db network alias"
	compose exec -T phpmyadmin php -r '
$host = "db";
$port = 3306;
$socket = @fsockopen($host, $port, $errno, $error, 5);
if ($socket === false) {
    fwrite(STDERR, "phpMyAdmin cannot reach $host:$port: $error\n");
    exit(1);
}
fclose($socket);
' || fail "phpMyAdmin cannot reach the active MySQL-family service"
}

login_with_password() {
	# Reads an explicit test password on stdin and runs a fixed SELECT from the
	# devstack database. The password never appears in the Compose argv.
	local service=$1
	local password=$2
	local expect_success=$3
	local client=mysql
	if [[ "$service" == "mariadb" ]]; then
		client=mariadb
	fi
	local output status
	set +e
	output="$(printf '[client]\nuser=devstack\npassword=%s\nhost=127.0.0.1\n' "$password" |
		compose exec -T "$service" sh -c "cat > /tmp/devstack-login.cnf; $client --defaults-extra-file=/tmp/devstack-login.cnf --batch --skip-column-names -e 'SELECT 1' devstack; status=\$?; rm -f /tmp/devstack-login.cnf; exit \$status" 2>&1)"
	status=$?
	set -e
	if [[ "$expect_success" == "1" ]]; then
		[[ $status -eq 0 ]] || fail "devstack login with the new password failed: $output"
		[[ "$(printf '%s' "$output" | tr -d '[:space:]')" == "1" ]] || fail "unexpected SELECT output: $output"
	else
		[[ $status -ne 0 ]] || fail "the old devstack password still works after rotation"
	fi
}

compose up -d mysql
wait_healthy mysql
compose up -d phpmyadmin
wait_healthy phpmyadmin
probe_phpmyadmin_http
probe_phpmyadmin_to_database

# Rotate the MySQL devstack account using the helper's fixed statement shape.
MYSQL_ROTATED_PASSWORD="$(new_secret)"
printf "ALTER USER 'devstack'@'%%' IDENTIFIED BY '%s';\nFLUSH PRIVILEGES;\n" "$MYSQL_ROTATED_PASSWORD" |
	compose exec -T mysql sh -c 'set -eu; MYSQL_PWD="$(cat /run/secrets/mysql_root_password)"; export MYSQL_PWD; exec mysql --user=root --batch --skip-column-names' ||
	fail "rotating the MySQL devstack account failed"
login_with_password mysql "$MYSQL_DEVSTACK_PASSWORD" 0
login_with_password mysql "$MYSQL_ROTATED_PASSWORD" 1

# Switch engines on the same host port. The daemon restarts phpMyAdmin after a
# MySQL-family switch; the smoke repeats that fixed operation explicitly.
compose stop mysql
compose up -d mariadb
wait_healthy mariadb
compose restart phpmyadmin
wait_healthy phpmyadmin
probe_phpmyadmin_http
probe_phpmyadmin_to_database

# Rotate the devstack account using the same fixed statement shape as the
# privileged helper. The new password reaches mysql on stdin only.
NEW_PASSWORD="$(new_secret)"
printf "ALTER USER 'devstack'@'%%' IDENTIFIED BY '%s';\nFLUSH PRIVILEGES;\n" "$NEW_PASSWORD" |
	compose exec -T mariadb sh -c 'set -eu; MYSQL_PWD="$(cat /run/secrets/mariadb_root_password)"; export MYSQL_PWD; exec mariadb --user=root --batch --skip-column-names' ||
	fail "rotating the devstack account failed"
login_with_password mariadb "$MYSQL_DEVSTACK_PASSWORD" 0
login_with_password mariadb "$NEW_PASSWORD" 1

# A subsequent phpMyAdmin restart follows the rotated account while preserving
# cookie authentication (phpMyAdmin stores no database password).
compose restart phpmyadmin
wait_healthy phpmyadmin
probe_phpmyadmin_http
probe_phpmyadmin_to_database

printf 'PASS: phpMyAdmin follows MySQL/MariaDB and database credential rotation works\n'
