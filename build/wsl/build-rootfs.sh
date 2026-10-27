#!/usr/bin/env bash
set -euo pipefail

readonly SUITE=bookworm
readonly SNAPSHOT=20251027T000000Z
readonly MIRROR="https://snapshot.debian.org/archive/debian/${SNAPSHOT}/"
readonly SECURITY_MIRROR="https://snapshot.debian.org/archive/debian-security/${SNAPSHOT}/"
readonly PACKAGES="systemd systemd-sysv dbus ca-certificates curl gnupg iproute2 procps sudo"

if [[ "${EUID}" -ne 0 ]]; then
  echo "build-rootfs.sh must run as root" >&2
  exit 2
fi
if [[ $# -ne 1 ]]; then
  echo "usage: $0 OUTPUT_DIRECTORY" >&2
  exit 2
fi
for tool in debootstrap tar gzip sha256sum; do
  command -v "${tool}" >/dev/null || { echo "missing required tool: ${tool}" >&2; exit 2; }
done

out=$(mkdir -p "$1" && cd "$1" && pwd)
work=$(mktemp -d)
cleanup() { rm -rf -- "${work}"; }
trap cleanup EXIT
root="${work}/rootfs"

debootstrap --arch=amd64 --variant=minbase --include="${PACKAGES// /,}" \
  "${SUITE}" "${root}" "${MIRROR}"

cat >"${root}/etc/apt/sources.list" <<EOF
deb ${MIRROR} bookworm main
deb ${SECURITY_MIRROR} bookworm-security main
EOF
mkdir -p "${root}/etc/apt/apt.conf.d"
cat >"${root}/etc/apt/apt.conf.d/99snapshot" <<'EOF'
Acquire::Check-Valid-Until "false";
EOF
cat >"${root}/etc/wsl.conf" <<'EOF'
[boot]
systemd=true

[network]
generateHosts=false
generateResolvConf=true

[automount]
enabled=true
options="metadata,umask=22,fmask=11"
EOF
printf 'devstack-wsl\n' >"${root}/etc/hostname"
install -d -m 0755 "${root}/data/devstack"

archive="${out}/debian-12-wsl-rootfs.tar.gz"
epoch=$(date -u -d "${SNAPSHOT:0:8} 00:00:00" +%s)
tar --sort=name --mtime="@${epoch}" --numeric-owner --owner=0 --group=0 \
  -C "${root}" -cf - . | gzip -n >"${archive}"
(cd "${out}" && sha256sum "$(basename "${archive}")" >"$(basename "${archive}").sha256")
echo "Built ${archive}"
