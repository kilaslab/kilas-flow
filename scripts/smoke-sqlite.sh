#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

command -v curl >/dev/null 2>&1 || {
	echo "smoke-sqlite: curl is required" >&2
	exit 1
}

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/kilasflow-smoke-sqlite.XXXXXX")
server_pid=''
log_file="$work_dir/server.log"
port=${KILASFLOW_SMOKE_PORT:-18080}
base_url="http://127.0.0.1:$port"

cleanup() {
	if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

wait_for_ready() {
	attempt=0
	while [ "$attempt" -lt 50 ]; do
		if curl -fsS "$base_url/api/v1/ready" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$server_pid" 2>/dev/null; then
			cat "$log_file" >&2
			return 1
		fi
		attempt=$((attempt + 1))
		sleep 1
	done

	echo "smoke-sqlite: server did not become ready" >&2
	cat "$log_file" >&2
	return 1
}

make build-all
KILASFLOW_SERVER_HOST=127.0.0.1 \
KILASFLOW_SERVER_PORT="$port" \
KILASFLOW_DATABASE_DSN="$work_dir/kilasflow.db" \
./bin/kilasflow -config '' >"$log_file" 2>&1 &
server_pid=$!

wait_for_ready
curl -fsS "$base_url/api/v1/health" | grep -q '"status":"ok"'
curl -fsS "$base_url/api/openapi.json" | grep -q '"openapi"'
curl -fsS "$base_url/app/workflows/smoke" | grep -qi '<!doctype html>'
test -f "$work_dir/kilasflow.db"

echo "smoke-sqlite: passed"
