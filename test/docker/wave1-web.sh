#!/usr/bin/env bash
# Live Docker smoke test for the Wave 1 nginx/Apache web-server provider.
#
# Guard: only runs with DEVSTACK_DOCKER_TESTS=1 and a usable Docker daemon.
# Exits 77 (skip) when Docker is unavailable, rate-limited, or an image pull
# fails. Uses high ports on host networking so it never touches 80/443 or any
# system web server.
set -euo pipefail

if [[ "${DEVSTACK_DOCKER_TESTS:-}" != "1" ]]; then
  echo "skip: set DEVSTACK_DOCKER_TESTS=1 to run the Docker web smoke test" >&2
  exit 77
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "skip: docker is not installed" >&2
  exit 77
fi
if ! docker info >/dev/null 2>&1; then
  echo "skip: docker daemon is not reachable (permission denied or not running)" >&2
  exit 77
fi
for tool in go openssl curl python3; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "skip: required tool $tool is missing" >&2
    exit 77
  fi
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

NGINX_IMAGE="mirror.gcr.io/library/nginx:1.30.6@sha256:a7675fcee220f59b4a99f7098c06a0d5d999c2c0ee37c0211efb780536e5cb20"
APACHE_IMAGE="mirror.gcr.io/library/httpd:2.4.69@sha256:a30cd4cd57f3f3c3be9b2d16384d2488839996bb14c67df2b51f1ecbb3e584a1"
PHP_IMAGE="${PHP_IMAGE:-mirror.gcr.io/library/php:8.4-fpm}"  # test-only dependency; catalog pins only web servers

pick_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

HTTP_PORT="${WAVE1_HTTP_PORT:-$(pick_port)}"
HTTPS_PORT="${WAVE1_HTTPS_PORT:-$(pick_port)}"
PHP_PORT="${WAVE1_PHP_PORT:-$(pick_port)}"
NODE_PORT="${WAVE1_NODE_PORT:-$(pick_port)}"
DASH_PORT="${WAVE1_DASH_PORT:-$(pick_port)}"

ROOT="$(mktemp -d /tmp/devstack-wave1-web.XXXXXX)"
PREFIX="devstack-wave1-web-$$"
NGINX_NAME="$PREFIX-nginx"
APACHE_NAME="$PREFIX-apache"
PHP_NAME="$PREFIX-php"
DASH_PID=""
NODE_PID=""

cleanup() {
  docker rm -f "$NGINX_NAME" "$APACHE_NAME" "$PHP_NAME" >/dev/null 2>&1 || true
  if [[ -n "$DASH_PID" ]]; then
    kill "$DASH_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$NODE_PID" ]]; then
    kill "$NODE_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$ROOT"
}
trap cleanup EXIT

mkdir -p "$ROOT/projects" "$ROOT/certs" "$ROOT/dashboard" "$ROOT/node"
cp -a test/fixtures/laravel-min "$ROOT/projects/"
cp -a test/fixtures/nuxt-static "$ROOT/projects/"
mkdir -p "$ROOT/projects/stopped"
printf 'stopped fixture\n' >"$ROOT/projects/stopped/index.html"
printf 'dashboard-ok\n' >"$ROOT/dashboard/index.html"
printf 'node-ok\n' >"$ROOT/node/index.html"
chmod -R a+rX "$ROOT"

openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -subj "/CN=devstack.test" \
  -addext "subjectAltName=DNS:devstack.test,DNS:laravel.test,DNS:www.laravel.test,DNS:node.test,DNS:nuxt.test,DNS:stopped.test" \
  -keyout "$ROOT/certs/__dashboard__.key" -out "$ROOT/certs/__dashboard__.crt" >/dev/null 2>&1
for name in laravel node nuxt stopped; do
  cp "$ROOT/certs/__dashboard__.crt" "$ROOT/certs/$name.crt"
  cp "$ROOT/certs/__dashboard__.key" "$ROOT/certs/$name.key"
done
chmod 644 "$ROOT/certs"/*.crt
chmod 600 "$ROOT/certs"/*.key

cat >"$ROOT/php-fpm.conf" <<'EOF'
[global]
error_log = /proc/self/fd/2
daemonize = no
[www]
user = www-data
group = www-data
listen = 127.0.0.1:PHP_PORT_PLACEHOLDER
listen.allowed_clients = 127.0.0.1
pm = dynamic
pm.max_children = 5
pm.start_servers = 2
pm.min_spare_servers = 1
pm.max_spare_servers = 3
catch_workers_output = yes
EOF
sed -i "s/PHP_PORT_PLACEHOLDER/$PHP_PORT/" "$ROOT/php-fpm.conf"

pull_or_skip() {
  local image="$1"
  if ! timeout 300 docker pull "$image" >/dev/null 2>&1; then
    echo "skip: could not pull $image (Docker unavailable or rate-limited)" >&2
    exit 77
  fi
}
pull_or_skip "$NGINX_IMAGE"
pull_or_skip "$APACHE_IMAGE"
pull_or_skip "$PHP_IMAGE"

wait_port() {
  local port="$1" tries="${2:-60}"
  for ((i = 0; i < tries; i++)); do
    if python3 - "$port" <<'PY'
import socket, sys
s = socket.socket()
s.settimeout(0.2)
try:
    s.connect(("127.0.0.1", int(sys.argv[1])))
except OSError:
    sys.exit(1)
finally:
    s.close()
PY
    then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

fail() {
  echo "FAIL: $*" >&2
  echo "--- $1 ---" >&2
  exit 1
}

CURL=(curl -sk --noproxy '*')
BODY="$ROOT/body"
HEADERS="$ROOT/headers"

request() {
  local url="$1"
  shift
  "${CURL[@]}" -D "$HEADERS" -o "$BODY" -w '%{http_code}' "$@" "$url"
}

assert_status() {
  local want="$1" got="$2" label="$3"
  if [[ "$got" != "$want" ]]; then
    echo "--- body ($label) ---" >&2
    cat "$BODY" >&2 || true
    fail "$label: status $got, want $want"
  fi
}

assert_contains() {
  local file="$1" want="$2" label="$3"
  if ! grep -qF -- "$want" "$file"; then
    echo "--- $file ---" >&2
    cat "$file" >&2 || true
    fail "$label: missing $want"
  fi
}

assert_absent_header() {
  local header="$1" label="$2"
  if grep -qi "^$header:" "$HEADERS"; then
    fail "$label: response must not include $header"
  fi
}

assert_header_contains() {
  local header="$1" want="$2" label="$3"
  if ! grep -i "^$header:" "$HEADERS" | grep -qF -- "$want"; then
    echo "--- headers ($label) ---" >&2
    cat "$HEADERS" >&2 || true
    fail "$label: missing $header containing $want"
  fi
}

url_for() {
  local host="$1" path="$2"
  printf 'https://%s:%s%s' "$host" "$HTTPS_PORT" "$path"
}

http_url_for() {
  local host="$1" path="$2"
  printf 'http://%s:%s%s' "$host" "$HTTP_PORT" "$path"
}

resolve_args() {
  local host="$1"
  printf -- '--resolve %s:%s:127.0.0.1' "$host" "$HTTPS_PORT"
}

resolve_http_args() {
  local host="$1"
  printf -- '--resolve %s:%s:127.0.0.1' "$host" "$HTTP_PORT"
}

render_config() {
  local server="$1" mode="$2" out="$3"
  go run ./test/docker/render_fixture.go \
    -server "$server" -mode "$mode" -root "$ROOT" -out "$out" \
    -http "$HTTP_PORT" -https "$HTTPS_PORT" \
    -php "$PHP_PORT" -node "$NODE_PORT" -dashboard "$DASH_PORT"
}

nginx_mounts() {
  local cfg="$1"
  printf -- '-v %s/base.conf:/etc/nginx/nginx.conf:ro ' "$cfg"
  printf -- '-v %s/conf.d:/etc/nginx/conf.d:ro ' "$cfg"
  printf -- '-v %s/projects:/sites:ro ' "$ROOT"
  printf -- '-v %s/certs:/certs:ro ' "$ROOT"
  printf -- '-v %s/logs:/var/log/nginx ' "$cfg"
}

apache_mounts() {
  local cfg="$1"
  printf -- '-v %s/base.conf:/usr/local/apache2/conf/httpd.conf:ro ' "$cfg"
  printf -- '-v %s/conf.d:/usr/local/apache2/conf/conf.d:ro ' "$cfg"
  printf -- '-v %s/projects:/sites:ro ' "$ROOT"
  printf -- '-v %s/certs:/certs:ro ' "$ROOT"
  printf -- '-v %s/logs:/usr/local/apache2/logs ' "$cfg"
}

start_php() {
  docker run -d --name "$PHP_NAME" --network host \
    -v "$ROOT:$ROOT:ro" \
    -v "$ROOT/php-fpm.conf:/usr/local/etc/php-fpm.conf:ro" \
    --entrypoint php-fpm "$PHP_IMAGE" -F -y /usr/local/etc/php-fpm.conf >/dev/null
  wait_port "$PHP_PORT" || fail "PHP-FPM did not start on $PHP_PORT"
}

start_nginx() {
  local cfg="$1"
  # shellcheck disable=SC2046
  docker run --rm $(nginx_mounts "$cfg") --entrypoint nginx "$NGINX_IMAGE" -t >/dev/null
  # shellcheck disable=SC2046
  docker run -d --name "$NGINX_NAME" --network host $(nginx_mounts "$cfg") \
    --entrypoint nginx "$NGINX_IMAGE" -g 'daemon off;' >/dev/null
  if ! wait_port "$HTTP_PORT" || ! wait_port "$HTTPS_PORT"; then
    docker logs "$NGINX_NAME" >&2 || true
    fail "nginx did not start on $HTTP_PORT/$HTTPS_PORT"
  fi
}

stop_nginx() {
  docker rm -f "$NGINX_NAME" >/dev/null 2>&1 || true
}

start_apache() {
  local cfg="$1"
  # shellcheck disable=SC2046
  if ! docker run --rm $(apache_mounts "$cfg") --entrypoint httpd "$APACHE_IMAGE" -t >/dev/null; then
    docker run --rm $(apache_mounts "$cfg") --entrypoint httpd "$APACHE_IMAGE" -t >&2 || true
    fail "apache config test failed for $cfg"
  fi
  # shellcheck disable=SC2046
  docker run -d --name "$APACHE_NAME" --network host $(apache_mounts "$cfg") \
    --entrypoint httpd "$APACHE_IMAGE" -DFOREGROUND >/dev/null
  if ! wait_port "$HTTP_PORT" || ! wait_port "$HTTPS_PORT"; then
    docker logs "$APACHE_NAME" >&2 || true
    fail "apache did not start on $HTTP_PORT/$HTTPS_PORT"
  fi
}

stop_apache() {
  docker rm -f "$APACHE_NAME" >/dev/null 2>&1 || true
}

exercise_laravel() {
  local label="$1"
  local args
  IFS=' ' read -r -a args <<<"$(resolve_args laravel.test)"
  local http_args
  IFS=' ' read -r -a http_args <<<"$(resolve_http_args laravel.test)"

  local header_args=(-H 'Cookie: smoke=1' -H 'User-Agent: devstack-smoke' -H 'Accept: application/json')
  if [[ "$label" == nginx* ]]; then
    header_args+=(-H 'Authorization: Bearer smoke-token')
  fi

  assert_status 308 "$(request "$(http_url_for laravel.test /)" "${http_args[@]}")" "$label laravel force HTTPS"
  assert_header_contains "Location" "https://laravel.test:$HTTPS_PORT/" "$label laravel force HTTPS"

  assert_status 200 "$(request "$(url_for laravel.test /)" "${args[@]}" "${header_args[@]}")" "$label laravel root"
  assert_contains "$BODY" '"route":"root"' "$label laravel root"
  assert_contains "$BODY" '"proto":"https"' "$label laravel forwarded proto"
  assert_contains "$BODY" '"cookie":"smoke=1"' "$label laravel cookie header"
  assert_contains "$BODY" '"user_agent":"devstack-smoke"' "$label laravel user-agent header"
  assert_contains "$BODY" '"accept":"application\/json"' "$label laravel accept header"
  if [[ "$label" == nginx* ]]; then
    assert_contains "$BODY" '"authorization":"Bearer smoke-token"' "$label laravel authorization header"
  fi
  assert_absent_header "Strict-Transport-Security" "$label laravel HSTS"

  local www_args
  IFS=' ' read -r -a www_args <<<"$(resolve_args www.laravel.test)"
  assert_status 200 "$(request "$(url_for www.laravel.test /)" "${www_args[@]}")" "$label laravel www alias"
  assert_contains "$BODY" '"route":"root"' "$label laravel www route"

  assert_status 200 "$(request "$(url_for laravel.test '/deep/path?x=1&y=two')" "${args[@]}")" "$label laravel deep"
  assert_contains "$BODY" '"route":"deep"' "$label laravel deep route"
  assert_contains "$BODY" '"x":"1"' "$label laravel query x"
  assert_contains "$BODY" '"y":"two"' "$label laravel query y"

  assert_status 201 "$(request "$(url_for laravel.test /items)" "${args[@]}" -X POST --data 'hello=world')" "$label laravel POST"
  assert_contains "$BODY" '"method":"POST"' "$label laravel POST method"
  assert_contains "$BODY" '"body":"hello=world"' "$label laravel POST body"

  assert_status 200 "$(request "$(url_for laravel.test /assets/app.js)" "${args[@]}")" "$label laravel static"
  assert_contains "$BODY" 'pong' "$label laravel static asset"

  assert_status 404 "$(request "$(url_for laravel.test /missing/route)" "${args[@]}")" "$label laravel framework 404"

  local secret
  for secret in /.htaccess /index.php.bak /.git/config; do
    local code
    code="$(request "$(url_for "laravel.test" "$secret")" "${args[@]}")"
    if [[ "$code" != "403" && "$code" != "404" ]]; then
      fail "$label source disclosure: $secret returned $code"
    fi
  done
}

exercise_static_and_dashboard() {
  local label="$1"
  local args
  IFS=' ' read -r -a args <<<"$(resolve_args nuxt.test)"

  assert_status 200 "$(request "$(url_for nuxt.test /)" "${args[@]}")" "$label nuxt index"
  assert_contains "$BODY" 'nuxt-generated-index' "$label nuxt index body"

  assert_status 200 "$(request "$(url_for nuxt.test /_nuxt/app.js)" "${args[@]}")" "$label nuxt asset"
  assert_contains "$BODY" 'nuxt-asset-ok' "$label nuxt asset body"

  assert_status 200 "$(request "$(url_for nuxt.test /about/)" "${args[@]}")" "$label nuxt nested route"
  assert_contains "$BODY" 'nuxt-nested-route' "$label nuxt nested route body"

  assert_status 200 "$(request "$(url_for nuxt.test /dashboard/settings)" "${args[@]}")" "$label nuxt SPA fallback"
  assert_contains "$BODY" 'nuxt-generated-index' "$label nuxt SPA fallback body"

  assert_status 404 "$(request "$(url_for nuxt.test /assets/missing.js)" "${args[@]}")" "$label nuxt missing asset"

  IFS=' ' read -r -a args <<<"$(resolve_args stopped.test)"
  assert_status 503 "$(request "$(url_for stopped.test /)" "${args[@]}")" "$label stopped site"
  assert_contains "$BODY" 'Site unavailable' "$label stopped branded 503"

  IFS=' ' read -r -a args <<<"$(resolve_args devstack.test)"
  assert_status 200 "$(request "$(url_for devstack.test /)" "${args[@]}")" "$label dashboard"
  assert_contains "$BODY" 'dashboard-ok' "$label dashboard upstream"

  IFS=' ' read -r -a args <<<"$(resolve_args unknown.test)"
  assert_status 404 "$(request "$(url_for unknown.test /)" "${args[@]}")" "$label default 404"
  assert_contains "$BODY" 'Site not found' "$label default 404 body"
}

exercise_node() {
  local label="$1"
  local args
  IFS=' ' read -r -a args <<<"$(resolve_args node.test)"
  assert_status 200 "$(request "$(url_for node.test /)" "${args[@]}")" "$label node proxy"
  assert_contains "$BODY" 'node-ok' "$label node upstream"
}

start_php
python3 -m http.server "$DASH_PORT" --bind 127.0.0.1 --directory "$ROOT/dashboard" >/dev/null 2>&1 &
DASH_PID=$!
python3 -m http.server "$NODE_PORT" --bind 127.0.0.1 --directory "$ROOT/node" >/dev/null 2>&1 &
NODE_PID=$!
wait_port "$DASH_PORT" || fail "dashboard responder did not start"
wait_port "$NODE_PORT" || fail "node upstream responder did not start"

NGINX_CFG="$ROOT/nginx"
render_config nginx auto "$NGINX_CFG"
start_nginx "$NGINX_CFG"
exercise_laravel "nginx"
exercise_static_and_dashboard "nginx"
exercise_node "nginx"
stop_nginx
echo "ok: nginx static/PHP/Node paths, TLS, dashboard, SPA, 503"

APACHE_CFG="$ROOT/apache-htaccess"
render_config apache htaccess "$APACHE_CFG"
start_apache "$APACHE_CFG"
exercise_laravel "apache-htaccess"
exercise_static_and_dashboard "apache-htaccess"
exercise_node "apache-htaccess"
stop_apache
echo "ok: apache .htaccess front controller"

APACHE_GEN_CFG="$ROOT/apache-generated"
render_config apache generated "$APACHE_GEN_CFG"
start_apache "$APACHE_GEN_CFG"
exercise_laravel "apache-generated"
stop_apache
echo "ok: apache generated front-controller fallback"

echo "PASS: wave1 docker web smoke"
