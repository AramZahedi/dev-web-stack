#!/usr/bin/env bash
# Regression tests for the symlink policy in build/php/smoke.sh. The fixtures
# are tiny synthetic archives, so the test needs no PHP, FPM, network, or root.
set -euo pipefail
IFS=$'\n\t'
export LC_ALL=C

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir
repo_root=$(cd -- "$script_dir/../.." && pwd)
readonly repo_root
readonly smoke="$repo_root/build/php/smoke.sh"
readonly top='php-8.2.35-r1-linux-x64'
readonly tarball_name="$top.tar.gz"

if [[ $(id -u) -eq 0 ]]; then
	printf 'smoke_links_test: skipping; smoke.sh intentionally refuses to run as root\n' >&2
	exit 0
fi

work=$(mktemp -d "${TMPDIR:-/tmp}/devstack-php-smoke-links-XXXXXX")
trap 'rm -rf "$work"' EXIT

fail() {
	printf 'smoke_links_test: %s\n' "$*" >&2
	exit 1
}

pack() { # pack STAGE TARBALL
	local stage=$1 tarball=$2
	tar -czf "$tarball" -C "$stage" "$top" ||
		fail "could not create test archive $tarball"
}

SMOKE_STATUS=0
run_smoke() { # run_smoke TARBALL OUTPUT
	local tarball=$1 output=$2
	if bash "$smoke" "$tarball" >"$output" 2>&1; then
		SMOKE_STATUS=0
	else
		SMOKE_STATUS=$?
	fi
}

expect_allowed_past_links() { # expect_allowed_past_links TARBALL LABEL
	local tarball=$1 label=$2 output
	output="$work/$label.out"
	run_smoke "$tarball" "$output"
	[[ $SMOKE_STATUS -eq 1 ]] ||
		fail "$label: expected exit 1 at the missing bin/php check, got $SMOKE_STATUS: $(cat "$output")"
	grep -q 'bin/php is missing or not executable' "$output" ||
		fail "$label: smoke did not reach the bin/php check: $(cat "$output")"
	if grep -Eiq 'symbolic link|hard link|symlink' "$output"; then
		fail "$label: safe symlink was rejected by the link check: $(cat "$output")"
	fi
}

expect_rejected() { # expect_rejected TARBALL PATTERN LABEL
	local tarball=$1 pattern=$2 label=$3 output
	output="$work/$label.out"
	run_smoke "$tarball" "$output"
	[[ $SMOKE_STATUS -ne 0 ]] || fail "$label: smoke accepted an unsafe archive"
	grep -Eiq -- "$pattern" "$output" ||
		fail "$label: expected /$pattern/ in smoke output, got: $(cat "$output")"
}

# A relative symlink whose target is inside the package prefix is legal. It
# must get past the link check and stop at the missing bin/php entry.
stage="$work/safe-relative/stage"
mkdir -p "$stage/$top/bin"
printf '#!/bin/sh\n' >"$stage/$top/bin/phar.phar"
ln -s phar.phar "$stage/$top/bin/phar"
pack "$stage" "$work/safe-relative/$tarball_name"
expect_allowed_past_links "$work/safe-relative/$tarball_name" safe-relative

# The same policy allows relative ".." as long as the result stays inside the
# package root.
stage="$work/safe-parent/stage"
mkdir -p "$stage/$top/bin" "$stage/$top/lib"
printf '#!/bin/sh\n' >"$stage/$top/lib/phar.phar"
ln -s ../lib/phar.phar "$stage/$top/bin/phar"
pack "$stage" "$work/safe-parent/$tarball_name"
expect_allowed_past_links "$work/safe-parent/$tarball_name" safe-parent

# An absolute symlink target is never allowed.
stage="$work/absolute/stage"
mkdir -p "$stage/$top/bin"
ln -s /etc/passwd "$stage/$top/bin/phar"
pack "$stage" "$work/absolute/$tarball_name"
expect_rejected "$work/absolute/$tarball_name" 'absolute' absolute-target

# A relative target that resolves outside the package root is rejected after
# extraction.
stage="$work/escaping/stage"
mkdir -p "$stage/$top/bin"
ln -s ../../escape "$stage/$top/bin/phar"
pack "$stage" "$work/escaping/$tarball_name"
expect_rejected "$work/escaping/$tarball_name" 'resolves outside' escaping-target

# Backslash targets are malformed on Linux and must be rejected.
stage="$work/backslash/stage"
mkdir -p "$stage/$top/bin"
ln -s '..\..\escape' "$stage/$top/bin/phar"
pack "$stage" "$work/backslash/$tarball_name"
expect_rejected "$work/backslash/$tarball_name" 'backslash' backslash-target

# Hardlinks are never part of the artifact contract.
stage="$work/hardlink/stage"
mkdir -p "$stage/$top/bin"
printf '#!/bin/sh\n' >"$stage/$top/bin/php"
ln "$stage/$top/bin/php" "$stage/$top/bin/php-hard"
pack "$stage" "$work/hardlink/$tarball_name"
tar -tvzf "$work/hardlink/$tarball_name" | grep -q '^h' ||
	fail 'hardlink: fixture did not contain a hardlink entry'
expect_rejected "$work/hardlink/$tarball_name" 'hard link' hardlink-entry

# The artifact name must be php-<version>-r<revision>-linux-x64.tar.gz.
mkdir -p "$work/bad-name"
cp "$work/safe-relative/$tarball_name" "$work/bad-name/php-8.2.35-linux-x64.tar.gz"
expect_rejected "$work/bad-name/php-8.2.35-linux-x64.tar.gz" 'tarball name must be' archive-name

printf 'smoke_links_test: ok\n'
