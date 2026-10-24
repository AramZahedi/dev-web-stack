#!/usr/bin/env bash
# Container entrypoint for devstack. The helper stays privileged inside the
# container; the daemon and CLI run as the unprivileged devstack user.
set -Eeuo pipefail

log() { printf 'devstack-container: %s\n' "$*"; }
die() { printf 'devstack-container: error: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "the container entrypoint must run as root (the helper needs the loopback forwarders)"

DEVSTACK_HOME="${DEVSTACK_HOME:-/home/devstack/.devstack}"
CONTAINER_HTTP_PORT="${DEVSTACK_CONTAINER_HTTP_PORT:-80}"
CONTAINER_HTTPS_PORT="${DEVSTACK_CONTAINER_HTTPS_PORT:-443}"
CONTAINER_UI_PORT="${DEVSTACK_CONTAINER_UI_PORT:-7070}"
PUBLIC_HTTP_PORT="${DEVSTACK_PUBLIC_HTTP_PORT:-8080}"
PUBLIC_HTTPS_PORT="${DEVSTACK_PUBLIC_HTTPS_PORT:-8443}"
PROXY_HTTP_PORT="${DEVSTACK_PROXY_HTTP_PORT:-8080}"
PROXY_HTTPS_PORT="${DEVSTACK_PROXY_HTTPS_PORT:-8443}"
UI_INTERNAL_PORT="${DEVSTACK_UI_INTERNAL_PORT:-7071}"
HELPER_HTTP_PORT="${DEVSTACK_HELPER_HTTP_PORT:-18080}"
HELPER_HTTPS_PORT="${DEVSTACK_HELPER_HTTPS_PORT:-18443}"
HOST_HTTP_PORT="${DEVSTACK_HOST_HTTP_PORT:-$PUBLIC_HTTP_PORT}"
HOST_HTTPS_PORT="${DEVSTACK_HOST_HTTPS_PORT:-$PUBLIC_HTTPS_PORT}"
HOST_UI_PORT="${DEVSTACK_HOST_UI_PORT:-$CONTAINER_UI_PORT}"
DEVSTACK_UID="${DEVSTACK_UID:-1000}"
DEVSTACK_GID="${DEVSTACK_GID:-1000}"
LOG_LEVEL="${DEVSTACK_LOG_LEVEL:-info}"

is_port() { [[ "$1" =~ ^[0-9]+$ ]] && [ "$1" -ge 1 ] && [ "$1" -le 65535 ]; }
for item in \
  "container HTTP:$CONTAINER_HTTP_PORT" \
  "container HTTPS:$CONTAINER_HTTPS_PORT" \
  "container UI:$CONTAINER_UI_PORT" \
  "public HTTP:$PUBLIC_HTTP_PORT" \
  "public HTTPS:$PUBLIC_HTTPS_PORT" \
  "proxy HTTP:$PROXY_HTTP_PORT" \
  "proxy HTTPS:$PROXY_HTTPS_PORT" \
  "internal UI:$UI_INTERNAL_PORT" \
  "helper HTTP:$HELPER_HTTP_PORT" \
  "helper HTTPS:$HELPER_HTTPS_PORT"; do
  name="${item%%:*}"
  port="${item##*:}"
  is_port "$port" || die "$name port must be 1-65535, got '$port'"
done
[ "$CONTAINER_HTTP_PORT" != "$CONTAINER_HTTPS_PORT" ] || die "container HTTP and HTTPS ports must differ"
[ "$CONTAINER_HTTP_PORT" != "$CONTAINER_UI_PORT" ] || die "container HTTP and UI ports must differ"
[ "$CONTAINER_HTTPS_PORT" != "$CONTAINER_UI_PORT" ] || die "container HTTPS and UI ports must differ"
[ "$PROXY_HTTP_PORT" != "$PROXY_HTTPS_PORT" ] || die "proxy HTTP and HTTPS ports must differ"
[ "$PROXY_HTTP_PORT" != "$UI_INTERNAL_PORT" ] || die "proxy HTTP and internal UI ports must differ"
[ "$HELPER_HTTP_PORT" != "$HELPER_HTTPS_PORT" ] || die "helper HTTP and HTTPS ports must differ"

install -d -m 0755 /etc/devstack /var/lib/devstack-helper /run/devstack-helper
install -d -o devstack -g devstack -m 0755 "$DEVSTACK_HOME" /home/devstack/projects
chown devstack:devstack "$DEVSTACK_HOME" /home/devstack/projects
# Keep /workspace working for sites created before the home-mounted layout.
if [ -e /workspace ] && [ ! -L /workspace ]; then
  rmdir /workspace 2>/dev/null || true
fi
ln -sfn /home/devstack/projects /workspace

HOSTS_PATH=/etc/hosts
if [ -f /host-hosts ] && [ ! -L /host-hosts ]; then
  HOSTS_PATH=/host-hosts
fi
log "hosts file: $HOSTS_PATH"

if [ "${DEVSTACK_CONFIGURE:-1}" = "1" ] || [ ! -f "$DEVSTACK_HOME/config.yaml" ]; then
  cat >"$DEVSTACK_HOME/config.yaml" <<YAML
http_port: $PROXY_HTTP_PORT
https_port: $PROXY_HTTPS_PORT
ui_port: $UI_INTERNAL_PORT
wsl_distro: devstack
auto_start_sites: true
serving_mode: builtin
graceful_stop_seconds: 10
max_request_body_mb: 1024
log_max_mb: 10
log_keep: 5
YAML
  chown devstack:devstack "$DEVSTACK_HOME/config.yaml"
fi

cat >/etc/devstack/helper.yaml <<YAML
socket: /run/devstack-helper/helper.sock
state_dir: /var/lib/devstack-helper
hosts: $HOSTS_PATH
trust_dir: ""
allow_uid: $DEVSTACK_UID
forward_http_port: $HELPER_HTTP_PORT
forward_https_port: $HELPER_HTTPS_PORT
proxy_http_port: $PROXY_HTTP_PORT
proxy_https_port: $PROXY_HTTPS_PORT
auto_forward: true
vhost_root: /etc/devstack
vhost_state: /var/lib/devstack-helper/vhosts.json
vhost_http_port: $HELPER_HTTP_PORT
vhost_https_port: $HELPER_HTTPS_PORT
nginx_include_dir: ""
apache_include_dir: ""
log_level: $LOG_LEVEL
YAML

HELPER_PID=""
DAEMON_PID=""
SOCAT_PIDS=()

cleanup() {
  trap - TERM INT
  for pid in "${SOCAT_PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  if [ -n "$DAEMON_PID" ]; then
    kill -TERM "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
  if [ -n "$HELPER_PID" ]; then
    kill -TERM "$HELPER_PID" 2>/dev/null || true
    wait "$HELPER_PID" 2>/dev/null || true
  fi
}
trap 'log "stopping"; cleanup; exit 0' TERM INT

start_socat() {
  local listen="$1" target="$2" pid
  socat -d -d "TCP-LISTEN:${listen},fork,reuseaddr" "TCP:127.0.0.1:${target}" &
  pid=$!
  SOCAT_PIDS+=("$pid")
  printf '%s' "$pid"
}

log "starting helper on 127.0.0.1:${HELPER_HTTP_PORT}/${HELPER_HTTPS_PORT}"
/usr/local/bin/devstack-helper --config /etc/devstack/helper.yaml &
HELPER_PID=$!
for _ in $(seq 1 150); do
  [ -S /run/devstack-helper/helper.sock ] && break
  if ! kill -0 "$HELPER_PID" 2>/dev/null; then
    wait "$HELPER_PID" || true
    die "helper exited before creating its socket"
  fi
  sleep 0.2
done
[ -S /run/devstack-helper/helper.sock ] || die "helper socket did not appear"

log "starting daemon (proxy ${PROXY_HTTP_PORT}/${PROXY_HTTPS_PORT}, UI ${UI_INTERNAL_PORT})"
setpriv --reuid="$DEVSTACK_UID" --regid="$DEVSTACK_GID" --init-groups \
  env HOME=/home/devstack USER=devstack LOGNAME=devstack \
  DEVSTACK_HOME="$DEVSTACK_HOME" \
  /usr/local/bin/devstack daemon run --hosts "$HOSTS_PATH" &
DAEMON_PID=$!

for _ in $(seq 1 150); do
  if curl -fsS "http://127.0.0.1:${UI_INTERNAL_PORT}/api/v1/health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
    wait "$DAEMON_PID" || true
    die "daemon exited before becoming ready"
  fi
  sleep 0.2
done
curl -fsS "http://127.0.0.1:${UI_INTERNAL_PORT}/api/v1/health" >/dev/null 2>&1 \
  || die "daemon did not become healthy on 127.0.0.1:${UI_INTERNAL_PORT}"

log "starting edge forwarders ${CONTAINER_HTTP_PORT}->${HELPER_HTTP_PORT}, ${CONTAINER_HTTPS_PORT}->${HELPER_HTTPS_PORT}, ${CONTAINER_UI_PORT}->${UI_INTERNAL_PORT}"
start_socat "$CONTAINER_HTTP_PORT" "$HELPER_HTTP_PORT" >/dev/null
start_socat "$CONTAINER_HTTPS_PORT" "$HELPER_HTTPS_PORT" >/dev/null
start_socat "$CONTAINER_UI_PORT" "$UI_INTERNAL_PORT" >/dev/null

if [ "${DEVSTACK_INIT:-1}" = "1" ]; then
  log "running devstack init"
  set +e
  setpriv --reuid="$DEVSTACK_UID" --regid="$DEVSTACK_GID" --init-groups \
    env HOME=/home/devstack USER=devstack LOGNAME=devstack \
    DEVSTACK_HOME="$DEVSTACK_HOME" \
    /usr/local/bin/devstack init --json >/tmp/devstack-init.json 2>/tmp/devstack-init.err
  init_status=$?
  set -e
  if [ "$init_status" -eq 0 ]; then
    log "devstack init complete"
  else
    log "devstack init returned $init_status; HTTP/HTTPS hosts may need attention"
    sed 's/^/init: /' /tmp/devstack-init.err || true
  fi
fi

if [ -f /var/lib/devstack-helper/pki/root.crt ]; then
  install -d -o devstack -g devstack -m 0755 "$DEVSTACK_HOME/ca"
  install -o devstack -g devstack -m 0644 /var/lib/devstack-helper/pki/root.crt "$DEVSTACK_HOME/ca/root.crt"
fi

log "ready"
log "UI:    http://localhost:${HOST_UI_PORT}"
log "sites: http://<name>.test:${HOST_HTTP_PORT} and https://<name>.test:${HOST_HTTPS_PORT}"
log "CA:    ${DEVSTACK_HOME}/ca/root.crt (inside the container)"
log "data:  ${DEVSTACK_HOME}"

wait -n "$HELPER_PID" "$DAEMON_PID" "${SOCAT_PIDS[@]}"
status=$?
log "a child exited with status $status; shutting down"
cleanup
exit "$status"
