#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "${script_dir}/.." && pwd)
cd "${repo_root}"

fail() {
	printf 'staging readiness failed: %s\n' "$1" >&2
	exit 1
}

for command_name in curl openssl go grep tr wc; do
	command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done

staging_base_url=${STAGING_BASE_URL:-}
jwt_secret=${JWT_SECRET:-}
jwt_issuer=${JWT_ISSUER:-}
jwt_audience=${JWT_AUDIENCE:-}
encryption_key=${APP_ENCRYPTION_KEY:-}
webhook_hosts=${WEBHOOK_ALLOWED_HOSTS:-}
database_driver=${DATABASE_DRIVER:-}
database_dsn=${DATABASE_DSN:-}
server_mode=${SERVER_MODE:-}
demo_seed_enabled=${DEMO_SEED_ENABLED:-}
deployment_mode=${STAGING_DEPLOYMENT_MODE:-external}

[ -n "${staging_base_url}" ] || fail 'STAGING_BASE_URL is required'
[ -n "${jwt_secret}" ] || fail 'JWT_SECRET is required'
[ -n "${jwt_issuer}" ] || fail 'JWT_ISSUER is required'
[ -n "${jwt_audience}" ] || fail 'JWT_AUDIENCE is required'
[ -n "${encryption_key}" ] || fail 'APP_ENCRYPTION_KEY is required'
[ -n "${webhook_hosts}" ] || fail 'WEBHOOK_ALLOWED_HOSTS is required'
[ -n "${database_driver}" ] || fail 'DATABASE_DRIVER is required'
[ -n "${database_dsn}" ] || fail 'DATABASE_DSN is required'
[ "${server_mode}" = 'release' ] || fail 'SERVER_MODE must be release'
[ "${demo_seed_enabled}" = 'false' ] || fail 'DEMO_SEED_ENABLED must be false'

case "${staging_base_url}" in
	https://*) ;;
	http://127.0.0.1:*|http://localhost:*|http://\[::1\]:*)
		[ "${STAGING_ALLOW_HTTP:-false}" = 'true' ] || fail 'loopback HTTP requires STAGING_ALLOW_HTTP=true'
		;;
	*) fail 'STAGING_BASE_URL must use HTTPS (HTTP is limited to an explicitly allowed loopback target)' ;;
esac
case "${staging_base_url}" in
	*'@'*) fail 'STAGING_BASE_URL must not contain user information' ;;
esac

jwt_length=$(LC_ALL=C printf '%s' "${jwt_secret}" | wc -c | tr -d ' ')
[ "${jwt_length}" -ge 32 ] || fail 'JWT_SECRET must contain at least 32 bytes'
[ "${jwt_secret}" = "$(printf '%s' "${jwt_secret}" | tr -d '\r\n')" ] || fail 'JWT_SECRET must not contain line breaks'

work_dir=$(mktemp -d)
cleanup() {
	rm -rf -- "${work_dir}"
}
trap cleanup EXIT HUP INT TERM
decoded_key="${work_dir}/encryption-key.bin"
if ! printf '%s' "${encryption_key}" | openssl base64 -d -A >"${decoded_key}" 2>/dev/null; then
	fail 'APP_ENCRYPTION_KEY must be valid base64'
fi
decoded_length=$(wc -c <"${decoded_key}" | tr -d ' ')
[ "${decoded_length}" -eq 32 ] || fail 'APP_ENCRYPTION_KEY must decode to exactly 32 bytes'

case "$(printf '%s' "${database_driver}" | tr '[:upper:]' '[:lower:]')" in
	postgres|postgresql) ;;
	*) fail 'staging DATABASE_DRIVER must be postgres' ;;
esac
lower_dsn=$(printf '%s' "${database_dsn}" | tr '[:upper:]' '[:lower:]')
case "${deployment_mode}" in
	external)
		case "${lower_dsn}" in
			*'sslmode=verify-full'*) ;;
			*) fail 'external staging PostgreSQL must use sslmode=verify-full' ;;
		esac
		;;
	compose)
		[ -n "${POSTGRES_PASSWORD:-}" ] || fail 'POSTGRES_PASSWORD is required for Compose staging'
		if printf '%s' "${lower_dsn}" | grep -q 'sslmode=disable'; then
			[ "${DATABASE_ALLOW_INSECURE_INTERNAL:-false}" = 'true' ] || fail 'internal plaintext PostgreSQL requires DATABASE_ALLOW_INSECURE_INTERNAL=true'
		fi
		command -v docker >/dev/null 2>&1 || fail 'docker is required for STAGING_DEPLOYMENT_MODE=compose'
		docker compose config --quiet
		;;
	*) fail 'STAGING_DEPLOYMENT_MODE must be external or compose' ;;
esac

if printf '%s' "${webhook_hosts}" | tr ',' '\n' | grep -Eiq '(^|\.)(localhost|local)$|^[[:space:]]*([0-9]{1,3}\.){3}[0-9]{1,3}[[:space:]]*$'; then
	fail 'WEBHOOK_ALLOWED_HOSTS must not include localhost, .local, or literal IPv4 targets'
fi

headers_file="${work_dir}/headers"
body_file="${work_dir}/body"
curl --silent --show-error --fail --max-time 10 --dump-header "${headers_file}" --output "${body_file}" "${staging_base_url}/health"
grep -Eq '"status"[[:space:]]*:[[:space:]]*"UP"' "${body_file}" || fail '/health did not report UP'
tr -d '\r' <"${headers_file}" >"${headers_file}.normalized"
grep -Eiq '^X-Content-Type-Options:[[:space:]]*nosniff[[:space:]]*$' "${headers_file}.normalized" || fail '/health is missing X-Content-Type-Options: nosniff'
grep -Eiq '^X-Frame-Options:[[:space:]]*DENY[[:space:]]*$' "${headers_file}.normalized" || fail '/health is missing X-Frame-Options: DENY'
grep -Eiq '^Content-Security-Policy:' "${headers_file}.normalized" || fail '/health is missing Content-Security-Policy'

curl --silent --show-error --fail --max-time 10 --output "${body_file}" "${staging_base_url}/ready"
grep -Eq '"status"[[:space:]]*:[[:space:]]*"READY"' "${body_file}" || fail '/ready did not report READY'

if [ "${STAGING_RUN_E2E:-true}" = 'true' ]; then
	E2E_BASE_URL="${staging_base_url}" \
	E2E_ALLOW_HTTP="${STAGING_ALLOW_HTTP:-false}" \
	E2E_JWT_SECRET="${jwt_secret}" \
	E2E_JWT_ISSUER="${jwt_issuer}" \
	E2E_JWT_AUDIENCE="${jwt_audience}" \
	E2E_STUDENT_TOKEN="${E2E_STUDENT_TOKEN:-}" \
	E2E_OTHER_STUDENT_TOKEN="${E2E_OTHER_STUDENT_TOKEN:-}" \
	E2E_ADMIN_TOKEN="${E2E_ADMIN_TOKEN:-}" \
	E2E_DELIVERY_ID="${E2E_DELIVERY_ID:-}" \
	E2E_ITEM_ID="${E2E_ITEM_ID:-}" \
	E2E_ITEM_RESPONSE="${E2E_ITEM_RESPONSE:-}" \
	E2E_EXPECTED_SCORE="${E2E_EXPECTED_SCORE:-}" \
	go test -count=1 -v ./tests/e2e
fi

printf 'staging readiness checks passed for %s\n' "${staging_base_url}"
