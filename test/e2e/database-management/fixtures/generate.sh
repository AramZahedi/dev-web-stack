#!/usr/bin/env bash
# Deterministic fixture generator.
#
# The small fixtures are committed under fixtures/dumps. This script rebuilds
# them reproducibly (gzip -n), creates the corrupt/truncated variants, and can
# optionally create a large import file. The 1 GiB file is opt-in:
#   fixtures/generate.sh --large --large-size-mb 1024 --out <disposable-dir>
set -Eeuo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
sql_dir="${root}/sql"
out="${FIXTURES_OUT:-${root}/dumps}"
large=0
large_size_mb=64
redis_acl_dir=""
secret_dir=""
check=0

usage() {
	cat <<'EOF'
usage: generate.sh [--out DIR] [--large] [--large-size-mb N]
                   [--redis-acl DIR] [--secret-dir DIR] [--check]
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--out) out="$2"; shift 2 ;;
	--large) large=1; shift ;;
	--large-size-mb) large_size_mb="$2"; shift 2 ;;
	--redis-acl) redis_acl_dir="$2"; shift 2 ;;
	--secret-dir) secret_dir="$2"; shift 2 ;;
	--check) check=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) printf 'generate.sh: unknown argument %s\n' "$1" >&2; usage >&2; exit 2 ;;
	esac
done

mkdir -p "${out}"
umask 022

gzip_file() {
	local source="$1" target="$2"
	if command -v gzip >/dev/null 2>&1; then
		gzip -n -9 -c "${source}" >"${target}"
	elif command -v python3 >/dev/null 2>&1; then
		python3 - "${source}" "${target}" <<'PY'
import gzip, sys
with open(sys.argv[1], "rb") as src, gzip.GzipFile(sys.argv[2], "wb", mtime=0) as dst:
    dst.write(src.read())
PY
	else
		printf 'generate.sh: gzip or python3 required\n' >&2
		exit 1
	fi
}

gzip_file "${sql_dir}/seed.sql" "${out}/seed.sql.gz"
gzip_file "${sql_dir}/seed-postgres.sql" "${out}/seed-postgres.sql.gz"

# Corrupt and truncated gzip variants. They are intentionally invalid: the
# importer must fail and leave a usable pre-op backup / flagged target.
python3 - "${out}/seed.sql.gz" "${out}/corrupt.sql.gz" "${out}/truncated.sql.gz" <<'PY'
import sys
src, corrupt, truncated = sys.argv[1:4]
data = bytearray(open(src, "rb").read())
if len(data) < 24:
    raise SystemExit("valid gzip fixture is too small")
bad = bytearray(data)
bad[len(bad) // 2] ^= 0xFF
open(corrupt, "wb").write(bad)
open(truncated, "wb").write(data[: max(12, len(data) // 2)])
PY

printf 'This is not a SQL dump.\nIt exists to exercise the importer rejection path.\n' >"${out}/non-sql.txt"
: >"${out}/empty.sql"

if [ "${large}" = "1" ]; then
	case "${large_size_mb}" in
	'' | *[!0-9]*) printf 'generate.sh: --large-size-mb must be an integer\n' >&2; exit 2 ;;
	esac
	[ "${large_size_mb}" -ge 1 ] || { printf 'generate.sh: --large-size-mb must be >= 1\n' >&2; exit 2; }
	printf 'generate.sh: generating %s MiB large fixture (opt-in)\n' "${large_size_mb}" >&2
	python3 - "${out}/large.sql" "${large_size_mb}" <<'PY'
import sys
path, size_mb = sys.argv[1], int(sys.argv[2])
target = size_mb * 1024 * 1024
row = "INSERT INTO e2e_large (id, payload) VALUES (%d, '%s');\n"
payload = "x" * 900
with open(path, "w", encoding="utf-8", newline="\n") as fh:
    fh.write("CREATE TABLE IF NOT EXISTS e2e_large (id BIGINT PRIMARY KEY, payload TEXT NOT NULL);\n")
    written = fh.tell()
    i = 0
    while written < target:
        line = row % (i, payload)
        fh.write(line)
        written += len(line)
        i += 1
PY
	gzip_file "${out}/large.sql" "${out}/large.sql.gz"
fi

if [ -n "${redis_acl_dir}" ]; then
	mkdir -p "${redis_acl_dir}"
	secret_dir="${secret_dir:-${redis_acl_dir}/secrets}"
	mkdir -p "${secret_dir}"
	umask 077
	secret_value() {
		local name="$1" file="${secret_dir}/${1}"
		if [ -s "${file}" ]; then
			cat "${file}"
		else
			local value
			value="$(openssl rand -hex 24 2>/dev/null || head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
			printf '%s' "${value}" >"${file}"
			chmod 600 "${file}"
			cat "${file}"
		fi
	}
	hash_value() {
		printf '%s' "$1" | sha256sum | awk '{print $1}'
	}
	ro="$(secret_value redis-e2e_cache_ro)"
	full="$(secret_value redis-e2e_cache_full)"
	custom="$(secret_value redis-e2e_cache_custom)"
	sed \
		-e "s/__E2E_CACHE_RO_HASH__/$(hash_value "${ro}")/g" \
		-e "s/__E2E_CACHE_FULL_HASH__/$(hash_value "${full}")/g" \
		-e "s/__E2E_CACHE_CUSTOM_HASH__/$(hash_value "${custom}")/g" \
		-e "s/__E2E_CACHE_RO_USER__/e2e_cache_ro/g" \
		-e "s/__E2E_CACHE_FULL_USER__/e2e_cache_full/g" \
		-e "s/__E2E_CACHE_CUSTOM_USER__/e2e_cache_custom/g" \
		"${root}/redis/users.acl.tmpl" >"${redis_acl_dir}/users.acl"
	chmod 600 "${redis_acl_dir}/users.acl"
fi

if [ "${check}" = "1" ]; then
	for file in seed.sql.gz seed-postgres.sql.gz corrupt.sql.gz truncated.sql.gz non-sql.txt empty.sql; do
		[ -f "${out}/${file}" ] || { printf 'generate.sh: missing %s\n' "${out}/${file}" >&2; exit 1; }
	done
	gzip -t "${out}/seed.sql.gz" "${out}/seed-postgres.sql.gz"
	if gzip -t "${out}/corrupt.sql.gz" >/dev/null 2>&1; then
		printf 'generate.sh: corrupt.sql.gz unexpectedly passed gzip -t\n' >&2
		exit 1
	fi
	if gzip -t "${out}/truncated.sql.gz" >/dev/null 2>&1; then
		printf 'generate.sh: truncated.sql.gz unexpectedly passed gzip -t\n' >&2
		exit 1
	fi
	printf 'generate.sh: fixtures OK in %s\n' "${out}"
fi
