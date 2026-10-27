#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "install-agent.sh must run as root" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_SOURCE="${1:-${SCRIPT_DIR}/agent.service}"

if [[ ! -f "${SERVICE_SOURCE}" && -f "/etc/systemd/system/devstack-agent.service" ]]; then
  SERVICE_SOURCE="/etc/systemd/system/devstack-agent.service"
fi

if [[ ! -f "${SERVICE_SOURCE}" ]]; then
  echo "Service source file not found: ${SERVICE_SOURCE}" >&2
  exit 2
fi

install -d -m 0755 /data/devstack/bin /etc/systemd/system

if [[ "${SERVICE_SOURCE}" != "/etc/systemd/system/devstack-agent.service" ]]; then
  install -D -m 0644 "${SERVICE_SOURCE}" /etc/systemd/system/devstack-agent.service
else
  chmod 0644 /etc/systemd/system/devstack-agent.service
fi

systemctl daemon-reload
systemctl enable devstack-agent.service

# If agent.yaml and agent.token exist, start/restart the service
if [[ -r /data/devstack/agent.yaml && -r /data/devstack/agent.token ]]; then
  systemctl restart devstack-agent.service
else
  # Credentials are provisioned during user phase (devstack init); attempt start or leave enabled
  systemctl start devstack-agent.service 2>/dev/null || true
fi
