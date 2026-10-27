#!/usr/bin/env bash
set -euo pipefail

readonly DOCKER_KEY_FINGERPRINT=9DC858229FC7DD38854AE2D88D81803C0EBFCD88
readonly DOCKER_VERSION='5:27.5.1-1~debian.12~bookworm'
readonly CONTAINERD_VERSION='1.7.25-1'
readonly BUILDX_VERSION='0.20.0-1~debian.12~bookworm'
readonly COMPOSE_VERSION='2.32.4-1~debian.12~bookworm'
readonly CONFIG_SOURCE=/data/devstack/bin/docker-daemon.json

if [[ "${EUID}" -ne 0 ]]; then
  echo "install-docker.sh must run as root" >&2
  exit 2
fi
if [[ "$(. /etc/os-release && printf '%s' "${VERSION_CODENAME:-}")" != bookworm ]]; then
  echo "Docker bootstrap requires Debian 12 Bookworm" >&2
  exit 2
fi
for tool in apt-get curl gpg dpkg; do
  command -v "${tool}" >/dev/null || { echo "missing required tool: ${tool}" >&2; exit 2; }
done

install -d -m 0755 /etc/apt/keyrings /etc/apt/sources.list.d /etc/docker
curl --fail --silent --show-error --location https://download.docker.com/linux/debian/gpg \
  | gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg
chmod 0644 /etc/apt/keyrings/docker.gpg
fingerprint=$(gpg --show-keys --with-colons /etc/apt/keyrings/docker.gpg | awk -F: '$1 == "fpr" { print $10; exit }')
if [[ "${fingerprint}" != "${DOCKER_KEY_FINGERPRINT}" ]]; then
  echo "Docker repository signing key fingerprint mismatch" >&2
  exit 2
fi

architecture=$(dpkg --print-architecture)
cat >/etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/debian
Suites: bookworm
Components: stable
Architectures: ${architecture}
Signed-By: /etc/apt/keyrings/docker.gpg
EOF

printf 'Acquire::Check-Valid-Until "false";\n' >/etc/apt/apt.conf.d/99snapshot

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends \
  "containerd.io=${CONTAINERD_VERSION}" \
  "docker-ce=${DOCKER_VERSION}" \
  "docker-ce-cli=${DOCKER_VERSION}" \
  "docker-buildx-plugin=${BUILDX_VERSION}" \
  "docker-compose-plugin=${COMPOSE_VERSION}"

install -D -m 0644 "${CONFIG_SOURCE}" /etc/docker/daemon.json
install -d -o root -g root -m 0711 /data/devstack/docker
systemctl daemon-reload
systemctl enable docker.service
systemctl restart docker.service
docker info --format '{{.DockerRootDir}}' | grep -Fx /data/devstack/docker
docker compose version --short
