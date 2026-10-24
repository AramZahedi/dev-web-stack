#!/usr/bin/env bash
# Verify one built PHP artifact before publication: extract it, run the CLI and
# FPM binaries, load every shared extension, lint the line's default ini, then
# start FPM and issue a real FastCGI health request through cmd/php-smoke. Any
# failure exits non-zero and the artifact must not be published.
set -euo pipefail
IFS=$'\n\t'
export LC_ALL=C

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir
repo_root=$(cd -- "$script_dir/../.." && pwd)
readonly repo_root
readonly manifest="$repo_root/internal/catalog/php.json"
readonly lock_file="${PHP_LOCK_FILE:-$script_dir/extensions.lock}"
# Keep in sync with build.sh and update-lock.sh. imap is documented as blocked.
readonly pecl_packages=(igbinary redis apcu xdebug imagick memcached ssh2 yaml msgpack grpc pcov)

usage() {
	cat <<'EOF'
usage: smoke.sh <php-<version>-r<revision>-linux-x64.tar.gz>

Extracts the artifact to a temporary directory and verifies the PHP CLI,
php-fpm, every shared extension, the line's default extension set, and one
FastCGI health request. The archive is not published if any check fails.
EOF
}

die() {
	printf 'smoke.sh: %s\n' "$*" >&2
	exit 1
}

note() {
	printf '==> %s\n' "$*"
}

[[ $# -eq 1 ]] || {
	usage >&2
	exit 2
}
tarball=$1
[[ -f $tarball ]] || die "tarball $tarball does not exist"

if [[ $(id -u) -eq 0 ]]; then
	die 'refusing to run as root; run the smoke test as the unprivileged build user'
fi

for tool in awk find grep jq readelf readlink realpath tar; do
	command -v "$tool" >/dev/null 2>&1 || die "required tool '$tool' is missing"
done

[[ -f $manifest ]] || die "PHP extension manifest $manifest is missing"
[[ -f $lock_file ]] || die "lock file $lock_file is missing; run build/php/update-lock.sh"

tarball=$(cd -- "$(dirname -- "$tarball")" && pwd)/$(basename -- "$tarball")
top=${tarball##*/}
top=${top%.tar.gz}
[[ $top =~ ^php-[0-9]+\.[0-9]+\.[0-9]+-r[1-9][0-9]*-linux-x64$ ]] ||
	die "tarball name must be php-<version>-r<revision>-linux-x64.tar.gz"

work=$(mktemp -d "${TMPDIR:-/tmp}/devstack-php-smoke-XXXXXX") ||
	die 'could not create a temporary directory'
trap 'rm -rf "$work"' EXIT

# Hard links are rejected before extraction: the artifact is a self-contained
# tree and the runtime installer refuses hard links outright. Symlinks are
# allowed when they are relative and stay inside the package prefix, matching
# the runtime installer, but their containment is checked on the extracted
# tree below.
verbose="$work/listing.verbose"
tar -tvzf "$tarball" >"$verbose" 2>&1 || die "could not list $tarball"
if hard_entry=$(grep -m1 -E '^h' "$verbose"); then
	die "archive contains a hard link: $hard_entry"
fi

# Reject symlink targets that are unsafe on their face: empty, absolute, or
# containing a backslash. A NUL cannot be represented in tar's textual listing
# or in a shell string; tar's Linkname is NUL-terminated, so there is nothing
# further to inspect there. Containment of relative targets (including ones
# using "..") is resolved against the extracted tree afterwards, because
# another archive symlink can redirect an otherwise lexically contained target.
if ! awk '
	function bad(message) { printf "smoke.sh: %s\n", message > "/dev/stderr"; exit 1 }
	/^l/ {
		line = $0
		for (i = 0; i < 5; i++) sub(/^[^ \t]+[ \t]+/, "", line)
		arrow = index(line, " -> ")
		if (arrow == 0) bad("malformed symlink listing: " $0)
		name = substr(line, 1, arrow - 1)
		target = substr(line, arrow + 4)
		if (name == "") bad("malformed symlink listing: " $0)
		if (target == "") bad("symlink " name " has an empty target")
		if (target ~ /^\//) bad("symlink " name " is absolute: " target)
		if (index(target, "\\") != 0) bad("symlink " name " has a backslash target: " target)
	}
' "$verbose"; then
	die 'archive contains an unsafe symbolic link'
fi

listing="$work/listing.txt"
tar -tzf "$tarball" >"$listing" 2>&1 || die "could not list $tarball"
while IFS= read -r entry; do
	case "$entry" in
	/* | ../* | */../* | */..)
		die "archive member escapes the extraction root: $entry"
		;;
	"$top" | "$top"/*) ;;
	*) die "archive member is outside the $top prefix: $entry" ;;
	esac
done <"$listing"
tar -xzf "$tarball" -C "$work" || die "could not extract $tarball"
root="$work/$top"
# The package root itself must be a real directory: a symlink here would make
# every later path and the containment check below resolve outside the tree.
[[ -d $root && ! -L $root ]] || die "archive did not contain the $top directory as a real directory"
root_real=$(cd -- "$root" && pwd -P)

# Every extracted symlink must be relative and resolve inside the package root.
# realpath -m follows links that exist (matching the runtime manager) and
# resolves dangling targets lexically. A link may not point at itself or an
# ancestor, which the runtime installer also rejects.
# Write the find output to a file and check find's status before iterating. A
# process substitution would swallow a traversal failure and skip every link,
# turning the symlink check into a fail-open pass.
link_listing="$work/symlinks.list"
find "$root_real" -type l -print0 >"$link_listing" ||
	die "could not list archive symlinks under $top"
while IFS= read -r -d '' link; do
	rel=${link#"$root_real"/}
	target=$(readlink -- "$link") || die "could not read archive symlink $rel"
	[[ -n $target ]] || die "archive symlink $rel has an empty target"
	[[ $target != *\\* ]] || die "archive symlink $rel has a backslash target: $target"
	[[ $target != *$'\n'* ]] || die "archive symlink $rel has a newline in its target"
	[[ $target != /* ]] || die "archive symlink $rel has an absolute target: $target"
	link_dir=$(realpath -m -- "$(dirname -- "$link")") ||
		die "could not resolve the directory of archive symlink $rel"
	resolved=$(realpath -m -- "$link_dir/$target") ||
		die "could not resolve archive symlink $rel target: $target"
	identity="$link_dir/$(basename -- "$link")"
	if [[ $resolved != "$root_real" && $resolved != "$root_real"/* ]]; then
		die "archive symlink $rel resolves outside $top: $target -> $resolved"
	fi
	if [[ $resolved == "$identity" || $identity == "$resolved"/* ]]; then
		die "archive symlink $rel points at itself or an ancestor"
	fi
done <"$link_listing"

[[ -x "$root/bin/php" ]] || die "$top/bin/php is missing or not executable"
[[ -x "$root/sbin/php-fpm" ]] || die "$top/sbin/php-fpm is missing or not executable"

# The build pipeline requests full RELRO and immediate binding. Verify the
# artifact rather than trusting that configure/make retained the flags.
for binary in "$root/bin/php" "$root/sbin/php-fpm"; do
	readelf -lW "$binary" | grep -q 'GNU_RELRO' ||
		die "$binary is missing GNU_RELRO"
	readelf -dW "$binary" | grep -Eq 'BIND_NOW|FLAGS_1.*NOW' ||
		die "$binary is missing BIND_NOW"
done
ext_dir="$root/lib/php/extensions"
[[ -d $ext_dir ]] || die "$top/lib/php/extensions is missing"
[[ -f "$root/etc/php.ini" ]] || die "$top/etc/php.ini is missing"
grep -qx '\[PHP\]' "$root/etc/php.ini" || die "$top/etc/php.ini has no [PHP] section"
if grep -Eq '^[[:space:]]*(zend_)?extension(_dir)?[[:space:]]*=' "$root/etc/php.ini"; then
	die "$top/etc/php.ini must not contain extension_dir or extension lines"
fi

php_bin="$root/bin/php"
fpm_bin="$root/sbin/php-fpm"

check_clean_output() {
	local label=$1 output=$2
	if grep -Eiq 'Failed loading|PHP Startup|PHP (Warning|Fatal error)|Unable to load dynamic library|Cannot load module|undefined symbol' <<<"$output"; then
		printf 'smoke.sh: %s produced a load error:\n%s\n' "$label" "$output" >&2
		die "output check failed for $label"
	fi
}

module_name_for_so() {
	case "$1" in
	opcache.so) printf '%s\n' 'Zend OPcache' ;;
	*) printf '%s\n' "${1%.so}" ;;
	esac
}

# assert_loaded LABEL MODULE [php args...] runs the already-loaded module set
# with a one-line php -r check. It captures stderr so Failed loading and PHP
# Startup errors cannot be mistaken for success.
assert_loaded() {
	local label=$1 module=$2
	shift 2
	local output
	# shellcheck disable=SC2016 # this is PHP source, not a shell expansion.
	if ! output=$("$php_bin" "$@" -r 'exit(extension_loaded($argv[1]) ? 0 : 1);' "$module" 2>&1); then
		printf 'smoke.sh: %s did not report extension_loaded(%q):\n%s\n' "$label" "$module" "$output" >&2
		die "extension presence check failed for $label"
	fi
	check_clean_output "$label" "$output"
}

note "checking $top/bin/php"
if ! php_version_output=$("$php_bin" -v 2>&1); then
	die 'php -v failed'
fi
check_clean_output 'php -v' "$php_version_output"
php_version=$(printf '%s\n' "$php_version_output" | sed -n '1p')

note "checking $top/sbin/php-fpm"
if ! fpm_version_output=$("$fpm_bin" -v 2>&1); then
	die 'php-fpm -v failed'
fi
check_clean_output 'php-fpm -v' "$fpm_version_output"
fpm_version=$(printf '%s\n' "$fpm_version_output" | sed -n '1p')

if ! module_output=$("$php_bin" -n -m 2>&1); then
	die 'php -m failed'
fi
check_clean_output 'php -m' "$module_output"
grep -qx 'Core' <<<"$module_output" || die 'php -m did not report Core'
module_count=$(printf '%s\n' "$module_output" | grep -Ec '^[A-Za-z]') || true

# Load the standard shared modules in manifest order. opcache is a zend
# extension and must be loaded before ordinary modules.
artifact_line=$(printf '%s' "$top" | sed -E 's/^php-([0-9]+\.[0-9]+)\..*/\1/')
mapfile -t standard_so < <(jq -r --arg line "$artifact_line" '
	(.builtin_by_version[$line] // []) as $builtins
	| .extensions[]
	| select(.standard == true)
	| select(.name != "imap")
	| select((.versions // []) | index($line))
	| .name as $name
	| select(($builtins | index($name)) == null)
	| .so
' "$manifest")
[[ ${#standard_so[@]} -gt 0 ]] || die 'no standard extensions found in the manifest'
for so in "${standard_so[@]}"; do
	[[ -f "$ext_dir/$so" ]] || die "standard extension $so is missing from the artifact"
done

standard_args=(-n -d "extension_dir=$ext_dir")
for so in "${standard_so[@]}"; do
	if [[ $so == 'opcache.so' ]]; then
		standard_args+=(-d "zend_extension=$so")
	fi
done
for so in "${standard_so[@]}"; do
	[[ $so == 'opcache.so' ]] && continue
	standard_args+=(-d "extension=$so")
done
if ! standard_output=$("$php_bin" "${standard_args[@]}" -m 2>&1); then
	die 'loading the standard extensions failed'
fi
check_clean_output 'standard extensions' "$standard_output"
for so in "${standard_so[@]}"; do
	assert_loaded "$so" "$(module_name_for_so "$so")" "${standard_args[@]}"
done
if [[ $artifact_line == '8.5' ]]; then
	if ! opcache_output=$("$php_bin" -n -r 'exit(extension_loaded("Zend OPcache") ? 0 : 1);' 2>&1); then
		die 'PHP 8.5 did not report its built-in Zend OPcache'
	fi
	check_clean_output 'PHP 8.5 built-in OPcache' "$opcache_output"
fi

# Every PECL package pinned in the lock must load and report extension_loaded.
# Start from the validated standard set, because the generated site ini always
# loads it: redis, for example, requires session.so. Preload igbinary for the
# serializer integrations that are compiled against it.
# shellcheck disable=SC1090
source "$lock_file"
for package in "${pecl_packages[@]}"; do
	package_key=$(printf '%s' "$package" | tr '[:lower:]' '[:upper:]')
	package_version_var="PECL_${package_key}_VERSION"
	[[ -n ${!package_version_var:-} ]] ||
		die "PECL package $package is not pinned in $lock_file; run build/php/update-lock.sh"
	so="$package.so"
	[[ -f "$ext_dir/$so" ]] || die "locked extension $so is missing from the artifact"
	args=("${standard_args[@]}")
	case "$package" in
	redis | apcu | memcached)
		[[ -f "$ext_dir/igbinary.so" ]] ||
			die "igbinary.so is missing; $package was built with igbinary support"
		args+=(-d 'extension=igbinary.so')
		;;
	esac
	if [[ $package == 'xdebug' ]]; then
		args+=(-d "zend_extension=$so")
	else
		args+=(-d "extension=$so")
	fi
	if ! output=$("$php_bin" "${args[@]}" -m 2>&1); then
		die "loading $so failed"
	fi
	check_clean_output "$so" "$output"
	assert_loaded "$so" "$(module_name_for_so "$so")" "${args[@]}"
done

# The default extension set for the line must lint cleanly against the artifact.
# This catches a default that names a missing shared object (the 8.5 OPcache
# case) before a user can create a site.
default_ini="$work/default-$artifact_line.ini"
mapfile -t default_modules < <(jq -r --arg line "$artifact_line" '
	.versions[$line].extensions as $enabled
	| .extensions[]
	| select($enabled[.name] == true)
	| "\(.type)\t\(.so)"
' "$manifest")
[[ ${#default_modules[@]} -gt 0 ]] || die "no default extensions found for PHP $artifact_line"
{
	printf '[PHP]\n'
	printf 'extension_dir = "%s"\n' "$ext_dir"
	for entry in "${default_modules[@]}"; do
		type=${entry%%$'\t'*}
		[[ $type == 'zend_extension' ]] || continue
		so=${entry#*$'\t'}
		[[ -f "$ext_dir/$so" ]] || die "default extension $so is missing from the artifact"
		printf 'zend_extension=%s\n' "$so"
	done
	for entry in "${default_modules[@]}"; do
		type=${entry%%$'\t'*}
		[[ $type != 'extension' ]] && continue
		so=${entry#*$'\t'}
		[[ -f "$ext_dir/$so" ]] || die "default extension $so is missing from the artifact"
		printf 'extension=%s\n' "$so"
	done
} >"$default_ini"
if ! default_lint_output=$(env -u PHPRC PHP_INI_SCAN_DIR= "$php_bin" -c "$default_ini" -r 'exit(0);' 2>&1); then
	printf 'smoke.sh: default extension lint failed for PHP %s:\n%s\n' "$artifact_line" "$default_lint_output" >&2
	die "default extension lint failed for PHP $artifact_line"
fi
if [[ -n $default_lint_output ]]; then
	printf 'smoke.sh: default extension lint produced output for PHP %s:\n%s\n' "$artifact_line" "$default_lint_output" >&2
	die "default extension lint produced output for PHP $artifact_line"
fi

note 'starting php-fpm and sending a FastCGI health request'
smoke_bin=${PHP_SMOKE_BIN:-}
if [[ -z $smoke_bin ]]; then
	command -v go >/dev/null 2>&1 || die 'go is required to build cmd/php-smoke (or set PHP_SMOKE_BIN)'
	smoke_bin="$work/php-smoke"
	(
		cd "$repo_root"
		go build -trimpath -o "$smoke_bin" ./cmd/php-smoke
	) || die 'could not build cmd/php-smoke'
fi

choose_port() {
	local port
	for _ in $(seq 1 100); do
		port=$((43000 + RANDOM % 1000))
		# shellcheck disable=SC2016 # this is PHP source, not a shell expansion.
		if "$php_bin" -n -r '$s = @stream_socket_server("tcp://127.0.0.1:" . $argv[1]); if (!$s) { exit(1); } fclose($s);' "$port" >/dev/null 2>&1; then
			printf '%s\n' "$port"
			return 0
		fi
	done
	return 1
}
port=$(choose_port) || die 'could not find a free port in 43000-43999'

"$smoke_bin" --root "$root" --port "$port" || die 'FastCGI smoke request failed'

printf 'smoke: ok\n'
printf '  php:     %s\n' "$php_version"
printf '  php-fpm: %s\n' "$fpm_version"
printf '  modules: %s\n' "$module_count"
printf '  extensions: %s standard + %s pecl loaded\n' "${#standard_so[@]}" "${#pecl_packages[@]}"
printf '  default: %s ini modules linted clean\n' "${#default_modules[@]}"
printf '  fastcgi: 200 ok on 127.0.0.1:%s\n' "$port"
