#!/usr/bin/env bash
# Refresh build/php/extensions.lock from verified upstream data.
#
# PHP source versions and SHA-256 values come from the PHP releases JSON and are
# re-verified by downloading the .tar.xz. PECL versions are the newest
# release PECL marks stable and their SHA-256 is computed from a real HTTPS
# download. Nothing is written for a package whose checksum cannot be computed.
set -euo pipefail
IFS=$'\n\t'
export LC_ALL=C

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir
readonly php_releases_url='https://www.php.net/releases/index.php?json&version='
readonly php_lines=(8.2 8.3 8.4 8.5)
# imap is intentionally absent: PECL imap 1.x requires PHP >= 8.3 while the
# extension catalog exposes imap only on 8.2/8.3, and the 8.2 bundled copy needs
# the UW-IMAP c-client library. See build/php/README.md.
readonly pecl_packages=(igbinary redis apcu xdebug imagick memcached ssh2 yaml msgpack grpc pcov)

output="$script_dir/extensions.lock"
if [[ -n ${PHP_SOURCE_CACHE:-} ]]; then
	cache_dir=$PHP_SOURCE_CACHE
elif [[ -n ${XDG_CACHE_HOME:-} ]]; then
	cache_dir=$XDG_CACHE_HOME/devstack/php-sources
elif [[ -n ${HOME:-} ]]; then
	cache_dir=$HOME/.cache/devstack/php-sources
else
	cache_dir=$script_dir/.cache/php-sources
fi

usage() {
	cat <<'EOF'
usage: update-lock.sh [--output <path>]

Refreshes the PHP build lock. PHP lines are pinned to the newest stable patch;
PECL packages are pinned to the newest release PECL marks stable. Source
archives and PECL tarballs are downloaded over HTTPS and verified locally.
EOF
}

die() {
	printf 'update-lock.sh: %s\n' "$*" >&2
	exit 1
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--output)
		[[ $# -ge 2 ]] || die '--output requires a path'
		output=$2
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		die "unknown argument '$1'"
		;;
	esac
done

for tool in curl jq sha256sum gzip tar; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

mkdir -p "$cache_dir"
work=$(mktemp -d "${TMPDIR:-/tmp}/devstack-php-lock-XXXXXX") ||
	die 'could not create a temporary directory'
trap 'rm -rf "$work"' EXIT
entries="$work/entries"
blocked="$work/blocked"
: >"$entries"
: >"$blocked"

checksum() {
	sha256sum "$1" | awk '{print $1}'
}

# download URL DEST LABEL returns non-zero instead of exiting so callers can
# decide whether an unavailable package blocks the whole lock.
download() {
	local url=$1 dest=$2 label=$3 temporary
	# Always fetch a fresh copy here: a lock writer must never record a checksum
	# computed from a stale or corrupted cache entry. build.sh may reuse this
	# cache because it verifies every file against the committed lock.
	mkdir -p "$(dirname "$dest")"
	temporary=$(mktemp "${dest}.tmp.XXXXXX")
	if ! curl -fsSL --retry 3 --connect-timeout 20 --max-time 600 -o "$temporary" "$url"; then
		rm -f "$temporary"
		printf 'update-lock.sh: could not download %s from %s\n' "$label" "$url" >&2
		return 1
	fi
	mv "$temporary" "$dest"
}

fetch_php() {
	local line=$1 json version file expected actual key dest
	if ! json=$(curl -fsSL --retry 3 --connect-timeout 20 --max-time 60 "${php_releases_url}${line}"); then
		die "could not fetch the PHP $line release list"
	fi
	version=$(printf '%s' "$json" | jq -er '.version // empty') ||
		die "PHP $line release list has no version"
	if [[ ! $version =~ ^${line//./\.}\.[0-9]+$ ]]; then
		die "PHP $line release list returned unexpected version '$version'"
	fi
	file="php-$version.tar.xz"
	expected=$(printf '%s' "$json" | jq -er --arg file "$file" \
		'.source[] | select(.filename == $file) | .sha256') ||
		die "PHP $version has no SHA-256 in the release list"
	if [[ ! $expected =~ ^[0-9a-f]{64}$ ]]; then
		die "PHP $version has an invalid SHA-256 '$expected'"
	fi
	dest="$cache_dir/$file"
	download "https://www.php.net/distributions/$file" "$dest" "PHP $version source" ||
		die "could not download PHP $version source"
	actual=$(checksum "$dest")
	if [[ $actual != "$expected" ]]; then
		die "PHP $version checksum mismatch: release list $expected, download $actual"
	fi
	key=${line//./_}
	printf 'PHP_%s_SHA256=%s\n' "$key" "$actual" >>"$entries"
	printf 'PHP_%s_VERSION=%s\n' "$key" "$version" >>"$entries"
	printf 'pinned PHP %s (%s)\n' "$version" "$actual"
}

latest_stable_pecl() {
	local package=$1 xml
	if ! xml=$(curl -fsSL --retry 3 --connect-timeout 20 --max-time 60 \
		"https://pecl.php.net/rest/r/$package/allreleases.xml"); then
		return 1
	fi
	printf '%s\n' "$xml" |
		grep -o '<v>[^<]*</v><s>stable</s>' |
		sed -e 's:</v><s>stable</s>::' -e 's:<v>::' |
		sed -n '1p'
}

fetch_pecl() {
	local package=$1 version tgz actual key
	if ! version=$(latest_stable_pecl "$package"); then
		printf 'blocked %s: release metadata unavailable\n' "$package" >>"$blocked"
		return
	fi
	if [[ -z $version ]]; then
		printf 'blocked %s: no release marked stable\n' "$package" >>"$blocked"
		return
	fi
	if [[ ! $version =~ ^[0-9][A-Za-z0-9._-]*$ ]]; then
		printf 'blocked %s: invalid version %s\n' "$package" "$version" >>"$blocked"
		return
	fi
	tgz="$cache_dir/$package-$version.tgz"
	if ! download "https://pecl.php.net/get/$package-$version.tgz" "$tgz" "PECL $package $version"; then
		printf 'blocked %s %s: download failed\n' "$package" "$version" >>"$blocked"
		return
	fi
	if ! gzip -t "$tgz" 2>/dev/null; then
		rm -f "$tgz"
		printf 'blocked %s %s: downloaded archive is not gzip data\n' "$package" "$version" >>"$blocked"
		return
	fi
	actual=$(checksum "$tgz")
	if [[ ! $actual =~ ^[0-9a-f]{64}$ ]]; then
		printf 'blocked %s %s: checksum was not computed\n' "$package" "$version" >>"$blocked"
		return
	fi
	key=$(printf '%s' "$package" | tr '[:lower:]' '[:upper:]')
	printf 'PECL_%s_SHA256=%s\n' "$key" "$actual" >>"$entries"
	printf 'PECL_%s_VERSION=%s\n' "$key" "$version" >>"$entries"
	printf 'pinned PECL %s %s (%s)\n' "$package" "$version" "$actual"
}

for line in "${php_lines[@]}"; do
	fetch_php "$line"
done
for package in "${pecl_packages[@]}"; do
	fetch_pecl "$package"
done

if [[ -s $blocked ]]; then
	printf 'update-lock.sh: packages left out of the lock:\n' >&2
	sed -e 's/^/  - /' "$blocked" >&2
	die "refusing to replace $output while any package is blocked"
fi

output_dir=$(dirname "$output")
mkdir -p "$output_dir"
temporary=$(mktemp "$output_dir/.extensions.lock.XXXXXX") ||
	die "could not create a temporary lock beside $output"
{
	cat <<'HEADER'
# devstack PHP build lock.
#
# Generated by build/php/update-lock.sh. Do not edit by hand. Keys are sorted
# and shell-sourceable: PHP_<line>_VERSION/SHA256 pin the upstream source, and
# PECL_<PACKAGE>_VERSION/SHA256 pin tarballs downloaded from pecl.php.net.
# build.sh refuses to run when a required entry is missing or mismatched.
HEADER
	sort "$entries"
} >"$temporary"
chmod 0644 "$temporary"
mv "$temporary" "$output"
printf 'wrote %s\n' "$output"
