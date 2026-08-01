#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "${script_dir}/.." && pwd)
cd "${repo_root}"

fail() {
	printf 'staging migration failed: %s\n' "$1" >&2
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

[ "${MIGRATION_TARGET:-}" = 'staging' ] || fail 'MIGRATION_TARGET must be staging'
[ "${MIGRATION_CONFIRMATION:-}" = 'APPLY_STAGING_MIGRATION' ] || fail 'MIGRATION_CONFIRMATION must equal APPLY_STAGING_MIGRATION'
[ -n "${DATABASE_DRIVER:-}" ] || fail 'DATABASE_DRIVER is required'
[ -n "${DATABASE_DSN:-}" ] || fail 'DATABASE_DSN is required'
[ -n "${STAGING_BACKUP_FILE:-}" ] || fail 'STAGING_BACKUP_FILE is required'
[ -f "${STAGING_BACKUP_FILE}" ] || fail 'STAGING_BACKUP_FILE does not exist'
[ -n "${STAGING_BACKUP_CHECKSUM_FILE:-}" ] || fail 'STAGING_BACKUP_CHECKSUM_FILE is required'
[ -f "${STAGING_BACKUP_CHECKSUM_FILE}" ] || fail 'STAGING_BACKUP_CHECKSUM_FILE does not exist'
expected_checksum=$(tr -d '[:space:]' <"${STAGING_BACKUP_CHECKSUM_FILE}")
[ -n "${expected_checksum}" ] || fail 'staging backup checksum is empty'
[ "$(checksum "${STAGING_BACKUP_FILE}")" = "${expected_checksum}" ] || fail 'staging backup checksum does not match'

go run ./cmd/migrate -mode up
go run ./cmd/migrate -mode verify

printf 'staging migration and schema verification passed\n'
