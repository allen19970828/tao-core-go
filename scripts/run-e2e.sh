#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "${script_dir}/.." && pwd)
cd "${repo_root}"

for command_name in go curl; do
	if ! command -v "${command_name}" >/dev/null 2>&1; then
		printf 'required command is missing: %s\n' "${command_name}" >&2
		exit 1
	fi
done

e2e_port=${E2E_PORT:-18080}
case "${e2e_port}" in
	''|*[!0-9]*)
		printf 'E2E_PORT must be numeric\n' >&2
		exit 1
		;;
esac
if [ "${e2e_port}" -lt 1024 ] || [ "${e2e_port}" -gt 65535 ]; then
	printf 'E2E_PORT must be between 1024 and 65535\n' >&2
	exit 1
fi

base_url="http://127.0.0.1:${e2e_port}"
if curl --silent --fail --max-time 1 "${base_url}/health" >/dev/null 2>&1; then
	printf 'E2E port %s is already serving HTTP; choose another E2E_PORT\n' "${e2e_port}" >&2
	exit 1
fi

work_dir=$(mktemp -d)
server_pid=''
cleanup() {
	if [ -n "${server_pid}" ]; then
		kill "${server_pid}" >/dev/null 2>&1 || true
		wait "${server_pid}" >/dev/null 2>&1 || true
	fi
	rm -rf -- "${work_dir}"
}
trap cleanup EXIT HUP INT TERM

binary_path="${work_dir}/tao-core-go"
server_log="${work_dir}/server.log"
database_path="${work_dir}/e2e.db"
runtime_database_driver=${E2E_DATABASE_DRIVER:-sqlite}
runtime_database_dsn=${E2E_DATABASE_DSN:-${database_path}}
runtime_allow_insecure_database=${E2E_DATABASE_ALLOW_INSECURE_INTERNAL:-false}

CGO_ENABLED=1 go build -trimpath -o "${binary_path}" ./cmd/server

test_jwt_secret='e2e-only-jwt-secret-with-at-least-32-bytes'
test_jwt_issuer='tao-core-go-e2e'
test_jwt_audience='tao-core-go-e2e-client'
test_encryption_key='MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY='

SERVER_PORT="${e2e_port}" \
SERVER_MODE=release \
SERVER_TRUSTED_PROXIES='' \
DATABASE_DRIVER="${runtime_database_driver}" \
DATABASE_DSN="${runtime_database_dsn}" \
DATABASE_ALLOW_INSECURE_INTERNAL="${runtime_allow_insecure_database}" \
JWT_SECRET="${test_jwt_secret}" \
JWT_ISSUER="${test_jwt_issuer}" \
JWT_AUDIENCE="${test_jwt_audience}" \
APP_ENCRYPTION_KEY="${test_encryption_key}" \
WEBHOOK_ALLOWED_HOSTS='hooks.example.com' \
DEMO_SEED_ENABLED=true \
"${binary_path}" >"${server_log}" 2>&1 &
server_pid=$!

ready=false
attempt=1
while [ "${attempt}" -le 30 ]; do
	if curl --silent --fail --max-time 2 "${base_url}/ready" >/dev/null 2>&1; then
		ready=true
		break
	fi
	if ! kill -0 "${server_pid}" >/dev/null 2>&1; then
		break
	fi
	sleep 1
	attempt=$((attempt + 1))
done

if [ "${ready}" != true ]; then
	printf 'E2E server did not become ready. Server log follows:\n' >&2
	sed -n '1,240p' "${server_log}" >&2
	exit 1
fi

STAGING_BASE_URL="${base_url}" \
STAGING_ALLOW_HTTP=true \
STAGING_DEPLOYMENT_MODE=external \
STAGING_RUN_E2E=true \
SERVER_MODE=release \
DEMO_SEED_ENABLED=false \
DATABASE_DRIVER=postgres \
DATABASE_DSN='host=staging-db.example.com user=tao dbname=tao_core_db sslmode=verify-full' \
DATABASE_ALLOW_INSECURE_INTERNAL=false \
JWT_SECRET="${test_jwt_secret}" \
JWT_ISSUER="${test_jwt_issuer}" \
JWT_AUDIENCE="${test_jwt_audience}" \
APP_ENCRYPTION_KEY="${test_encryption_key}" \
WEBHOOK_ALLOWED_HOSTS='hooks.example.com' \
E2E_DELIVERY_ID='delivery-demo-01' \
E2E_ITEM_ID='item-demo-01' \
E2E_ITEM_RESPONSE='A' \
E2E_EXPECTED_SCORE='10' \
./scripts/staging-readiness.sh

printf 'process-level E2E acceptance passed against %s\n' "${base_url}"
