#!/bin/sh
set -eu

fail() {
	printf 'staging restore failed: %s\n' "$1" >&2
	exit 1
}

checksum() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		fail 'sha256sum or shasum is required'
	fi
}

verify_checksum() {
	archive_file=$1
	checksum_file=$2
	[ -f "${checksum_file}" ] || fail "checksum file does not exist: ${checksum_file}"
	expected_checksum=$(tr -d '[:space:]' <"${checksum_file}")
	[ -n "${expected_checksum}" ] || fail "checksum is empty: ${checksum_file}"
	[ "$(checksum "${archive_file}")" = "${expected_checksum}" ] || fail "checksum mismatch: ${archive_file}"
}

command -v pg_restore >/dev/null 2>&1 || fail 'pg_restore is required'
[ "${RESTORE_TARGET:-}" = 'staging' ] || fail 'RESTORE_TARGET must be staging'
[ "${RESTORE_CONFIRMATION:-}" = 'RESTORE_STAGING_DATABASE' ] || fail 'RESTORE_CONFIRMATION must equal RESTORE_STAGING_DATABASE'
[ -n "${RESTORE_DATABASE_DSN:-}" ] || fail 'RESTORE_DATABASE_DSN is required'
[ -n "${BACKUP_FILE:-}" ] || fail 'BACKUP_FILE is required'
[ -f "${BACKUP_FILE}" ] || fail 'BACKUP_FILE does not exist'
[ -n "${BACKUP_CHECKSUM_FILE:-}" ] || fail 'BACKUP_CHECKSUM_FILE is required'
[ -n "${PRE_RESTORE_BACKUP_FILE:-}" ] || fail 'PRE_RESTORE_BACKUP_FILE is required for rollback safety'
[ -f "${PRE_RESTORE_BACKUP_FILE}" ] || fail 'PRE_RESTORE_BACKUP_FILE does not exist'
[ -n "${PRE_RESTORE_BACKUP_CHECKSUM_FILE:-}" ] || fail 'PRE_RESTORE_BACKUP_CHECKSUM_FILE is required'

verify_checksum "${BACKUP_FILE}" "${BACKUP_CHECKSUM_FILE}"
verify_checksum "${PRE_RESTORE_BACKUP_FILE}" "${PRE_RESTORE_BACKUP_CHECKSUM_FILE}"

pg_restore --list "${BACKUP_FILE}" >/dev/null
pg_restore --list "${PRE_RESTORE_BACKUP_FILE}" >/dev/null

PGDATABASE="${RESTORE_DATABASE_DSN}" pg_restore \
	--exit-on-error \
	--clean \
	--if-exists \
	--no-owner \
	--no-privileges \
	"${BACKUP_FILE}"

printf 'staging restore completed; run cmd/migrate -mode verify before enabling traffic\n'
