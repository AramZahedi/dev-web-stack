#!/usr/bin/env bash
# Print the newest stable PHP patch release for one supported line.
set -euo pipefail
IFS=$'\n\t'
export LC_ALL=C

readonly PHP_RELEASES_URL='https://www.php.net/releases/index.php?json&version='

usage() {
	cat <<'EOF'
usage: latest-patch.sh <8.2|8.3|8.4|8.5>

Prints the newest stable PHP patch version for the line using the PHP releases
JSON over HTTPS.
EOF
}

die() {
	printf 'latest-patch.sh: %s\n' "$*" >&2
	exit 1
}

if [[ $# -ne 1 ]]; then
	usage >&2
	exit 2
fi

line=$1
case "$line" in
8.2 | 8.3 | 8.4 | 8.5) ;;
*)
	die "unsupported PHP line '$line' (want 8.2, 8.3, 8.4, or 8.5)"
	;;
esac

command -v curl >/dev/null 2>&1 || die 'curl is required'
command -v jq >/dev/null 2>&1 || die 'jq is required'

json=$(curl -fsSL --retry 3 --connect-timeout 20 --max-time 60 "${PHP_RELEASES_URL}${line}") ||
	die "could not fetch the PHP $line release list"

version=$(printf '%s' "$json" | jq -er '.version // empty') ||
	die "PHP $line release list has no version"

if [[ ! $version =~ ^${line//./\.}\.[0-9]+$ ]]; then
	die "PHP $line release list returned unexpected version '$version'"
fi

printf '%s\n' "$version"
