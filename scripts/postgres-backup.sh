#!/bin/sh
set -eu

fail() {
	printf 'PostgreSQL backup failed: %s\n' "$1" >&2
	exit 1
}

for command_name in pg_dump pg_restore awk; do
	command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done
if command -v sha256sum >/dev/null 2>&1; then
	checksum_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
	checksum_tool=shasum
else
	fail 'sha256sum or shasum is required to checksum the completed backup'
fi

database_dsn=${DATABASE_DSN:-}
backup_dir_input=${BACKUP_DIR:-}
[ -n "${database_dsn}" ] || fail 'DATABASE_DSN is required'
[ -n "${backup_dir_input}" ] || fail 'BACKUP_DIR must be an explicit, dedicated directory'

umask 077
mkdir -p -- "${backup_dir_input}"
backup_dir=$(CDPATH= cd -- "${backup_dir_input}" && pwd)
[ "${backup_dir}" != '/' ] || fail 'BACKUP_DIR must not be the filesystem root'

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
backup_file="${backup_dir}/tao-core-go-${timestamp}-$$.dump"
temporary_file="${backup_file}.partial"
cleanup() {
	rm -f -- "${temporary_file}"
}
trap cleanup EXIT HUP INT TERM

PGDATABASE="${database_dsn}" pg_dump \
	--format=custom \
	--compress=9 \
	--no-owner \
	--no-privileges \
	--file="${temporary_file}"
pg_restore --list "${temporary_file}" >/dev/null
mv -- "${temporary_file}" "${backup_file}"

if [ "${checksum_tool}" = 'sha256sum' ]; then
	checksum=$(${checksum_tool} "${backup_file}" | awk '{print $1}')
else
	checksum=$(${checksum_tool} -a 256 "${backup_file}" | awk '{print $1}')
fi
printf '%s\n' "${checksum}" >"${backup_file}.sha256"

printf 'backup_file=%s\nchecksum_file=%s\n' "${backup_file}" "${backup_file}.sha256"
