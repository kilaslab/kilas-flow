#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

for command in curl go node; do
	command -v "$command" >/dev/null 2>&1 || {
		echo "smoke-dev: $command is required" >&2
		exit 1
	}
done

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/kilasflow-smoke-dev.XXXXXX")
api_pid=''
web_pid=''
api_log="$work_dir/api.log"
web_log="$work_dir/web.log"
api_port=${KILASFLOW_SMOKE_API_PORT:-18081}
web_port=${KILASFLOW_SMOKE_WEB_PORT:-5181}
api_url="http://127.0.0.1:$api_port"
# Vite may bind its default development listener to IPv6 localhost (`::1`).
# `localhost` follows that listener while still falling back to IPv4 on hosts
# that bind it there instead.
web_url="http://localhost:$web_port"

cleanup() {
	if [ -n "$web_pid" ] && kill -0 "$web_pid" 2>/dev/null; then
		kill "$web_pid" 2>/dev/null || true
		wait "$web_pid" 2>/dev/null || true
	fi
	if [ -n "$api_pid" ] && kill -0 "$api_pid" 2>/dev/null; then
		kill "$api_pid" 2>/dev/null || true
		wait "$api_pid" 2>/dev/null || true
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

wait_for_url() {
	url=$1
	pid=$2
	log_file=$3
	name=$4
	attempt=0
	while [ "$attempt" -lt 50 ]; do
		if curl -fsS "$url" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$pid" 2>/dev/null; then
			echo "smoke-dev: $name exited before becoming ready" >&2
			cat "$log_file" >&2
			return 1
		fi
		attempt=$((attempt + 1))
		sleep 1
	done

	echo "smoke-dev: $name did not become ready" >&2
	cat "$log_file" >&2
	return 1
}

go build -o "$work_dir/kilasflow" ./cmd/kilasflow
KILASFLOW_SERVER_HOST=127.0.0.1 \
KILASFLOW_SERVER_PORT="$api_port" \
KILASFLOW_DATABASE_DSN="$work_dir/kilasflow.db" \
"$work_dir/kilasflow" -config '' >"$api_log" 2>&1 &
api_pid=$!
wait_for_url "$api_url/api/v1/ready" "$api_pid" "$api_log" "Go API"

(
	cd web
	node scripts/vendor-docs.mjs
	KILASFLOW_BACKEND_URL="$api_url" \
	KILASFLOW_WEB_PORT="$web_port" \
		exec ./node_modules/.bin/vite dev
) >"$web_log" 2>&1 &
web_pid=$!
wait_for_url "$web_url" "$web_pid" "$web_log" "Vite"

curl -fsS "$web_url/api/v1/health" | grep -q '"status":"ok"'
curl -fsS "$web_url" | grep -qi '<!doctype html>'

# Every prefix the Go server mounts has to be proxied, and the failure mode when
# one is not is silent: Vite answers the request itself with the SPA document, so
# a page that POSTs to /resume/<token> reports success and resumes nothing.
#
# The check is the backend's own answer for that path, not a marker that Vite
# left behind. A marker does not work here: SvelteKit's development shell serves
# the application document without injecting `/@vite/client`, so "the body does
# not contain Vite's client" is true of a proxied request and of an unproxied one
# alike. Each expectation below is something only the Go server produces.
#
# Keep this list in step with `proxied` in web/vite.config.ts and the prefix
# block in internal/api/server.go.
probe_prefix() {
	prefix=$1
	expected=$2
	if ! curl -sS "$web_url$prefix" | grep -q "$expected"; then
		echo "smoke-dev: $prefix was not answered by the Go server (wanted $expected in the body)" >&2
		exit 1
	fi
}

probe_prefix /api/v1/health '"status":"ok"'
probe_prefix /webhook 'No webhook path was given'
probe_prefix /docs 'KilasFlow API'
probe_prefix /resume 'wait.not_found'

echo "smoke-dev: every mounted prefix reaches the Go server"
echo "smoke-dev: passed"
