#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "install-agent.sh must run as root" >&2
  exit 2
fi

SCRIPT_DIR=""
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi
SERVICE_SOURCE="${1:-}"
if [[ -z "${SERVICE_SOURCE}" && -n "${SCRIPT_DIR}" ]]; then
  SERVICE_SOURCE="${SCRIPT_DIR}/agent.service"
fi

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

# If agent.yaml and agent.token exist, restart the service without masking errors
if [[ -r /data/devstack/agent.yaml && -r /data/devstack/agent.token ]]; then
  systemctl restart devstack-agent.service
fi
exit 0
