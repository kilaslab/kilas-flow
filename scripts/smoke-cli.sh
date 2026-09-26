#!/bin/sh
set -eu

# Proves the agent CLI end to end against a real server: the command tree, the
# envelope, the exit codes and the config chain, driven the way an agent drives
# them. It is the acceptance proof for FEAT-bp59m4's stage 3.
#
# Auth is ON for this run, because half of what the CLI does — whoami, context's
# identity section, a 401 that has to become exit 3 — has no meaning on an
# unauthenticated instance. The operator key is registered at boot from the
# environment, so no API call is needed to obtain a credential.

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

command -v curl >/dev/null 2>&1 || {
	echo "smoke-cli: curl is required" >&2
	exit 1
}
command -v openssl >/dev/null 2>&1 || {
	echo "smoke-cli: openssl is required to mint the session signing key" >&2
	exit 1
}

cli="$repo_dir/bin/kilasflow"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/kilasflow-smoke-cli.XXXXXX")
server_pid=''
log_file="$work_dir/server.log"
# This smoke owns its port and its variable name. smoke-dev.sh binds 18081 by
# default and smoke-sqlite.sh 18080, both under other names: sharing either
# would let this script's wait_for_ready be satisfied by an instance it did not
# start, and then assert this ticket's ids and exit codes against a server
# built from different code.
port=${KILASFLOW_SMOKE_CLI_PORT:-18083}
base_url="http://127.0.0.1:$port"

# The key must be shaped kfa1_<hex prefix>_<secret>: repository.EnsureAPIKey
# refuses anything else, and it is registered at boot, so nothing has to mint it
# over the API first.
operator_key="kfa1_5f3a2b1c9d4e6f708192a3b4c5d6e7f8_smoke-operator-secret"

cleanup() {
	if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
		kill "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

fail() {
	echo "smoke-cli: $*" >&2
	if [ -f "$work_dir/step.err" ]; then
		cat "$work_dir/step.err" >&2
	fi
	if [ -f "$work_dir/step.out" ]; then
		cat "$work_dir/step.out" >&2
	fi
	exit 1
}

# run_ok runs the CLI, requires exit 0, and prints its stdout.
run_ok() {
	set +e
	"$@" >"$work_dir/step.out" 2>"$work_dir/step.err"
	code=$?
	set -e
	if [ "$code" -ne 0 ]; then
		fail "'$*' exited $code, want 0"
	fi
	cat "$work_dir/step.out"
}

# expect_exit runs the CLI and requires one exact exit code, which is half of
# what the CLI promises: the code is what an agent branches on.
expect_exit() {
	want=$1
	shift
	set +e
	"$@" >"$work_dir/step.out" 2>"$work_dir/step.err"
	code=$?
	set -e
	if [ "$code" -ne "$want" ]; then
		fail "'$*' exited $code, want $want"
	fi
}

# expect_in fails unless the text given holds the substring.
expect_in() {
	text=$1
	substring=$2
	case "$text" in
	*"$substring"*) ;;
	*) fail "expected $substring in: $text" ;;
	esac
}

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

	echo "smoke-cli: server did not become ready" >&2
	cat "$log_file" >&2
	return 1
}

make build-all

# wait_for_ready accepts whatever answers /api/v1/ready on this port, so an
# instance this script did not start would satisfy it and every assertion below
# — tenant ids, exec_* and wf_* shapes, the exit codes — would run against a
# server built from different code. Refuse before spawning rather than after.
if curl -fsS --max-time 2 "$base_url/api/v1/ready" >/dev/null 2>&1; then
	echo "smoke-cli: something is already serving $base_url; set KILASFLOW_SMOKE_CLI_PORT to a free port" >&2
	exit 1
fi

# The explicit `serve` verb is the documented spelling for scripts, and it is
# the one that used to exit 1 with the server's usage text: the hand-back left
# the word in argv and run() refused it as a positional. Booting through it here
# is the end-to-end proof, because a seam test cannot see the argument list the
# server's own flag parsing receives. `make smoke-sqlite` covers the bare form.
KILASFLOW_AUTH_ENABLED=true \
KILASFLOW_AUTH_SIGNING_KEY="$(openssl rand -base64 32)" \
KILASFLOW_AUTH_OPERATOR_KEY="$operator_key" \
KILASFLOW_SERVER_HOST=127.0.0.1 \
KILASFLOW_SERVER_PORT="$port" \
KILASFLOW_DATABASE_DSN="$work_dir/kilasflow.db" \
./bin/kilasflow serve -config '' >"$log_file" 2>&1 &
server_pid=$!

wait_for_ready

# Every verb reads its settings from the environment here, so the run also
# proves the chain's second rung rather than the flags.
KILASFLOW_URL="$base_url"
KILASFLOW_TOKEN="$operator_key"
export KILASFLOW_URL KILASFLOW_TOKEN

# A manual-trigger workflow: the one document shape that runs without a
# credential, a webhook or a schedule.
cat >"$work_dir/manual.json" <<'JSON'
{"schemaVersion":1,"name":"cli smoke","nodes":[{"id":"manual","name":"Manual Trigger","type":"kilasflow.manual","typeVersion":1,"position":{"x":0,"y":0}}],"connections":[],"settings":{}}
JSON

# 1. The briefing an agent runs first, with the identity the operator key
#    resolves to.
context_json=$(run_ok "$cli" context --json)
expect_in "$context_json" '"ok":true'
expect_in "$context_json" '"ready":true'
expect_in "$context_json" '"tenantId":"operator"'

# 2. Create, run and trace: the loop this phase exists for.
workflow_id=$(run_ok "$cli" workflow create --file "$work_dir/manual.json" --quiet)
[ -n "$workflow_id" ] || fail "workflow create printed no id"
case "$workflow_id" in
wf_*) ;;
*) fail "workflow create printed $workflow_id, want an id" ;;
esac

execution_id=$(run_ok "$cli" run "$workflow_id" --wait --timeout 60s --quiet)
[ -n "$execution_id" ] || fail "run --wait printed no execution id"
case "$execution_id" in
exec_*) ;;
*) fail "run printed $execution_id, want an execution id" ;;
esac

trace_json=$(run_ok "$cli" exec trace "$execution_id" --json)
expect_in "$trace_json" 'execution.completed'
expect_in "$trace_json" '"terminal":true'

# 3. The read-only families against the real router. This matters more than it
#    looks: a fake server cannot tell a right path from a wrong one — only a
#    booted server can, and these exit codes are what say the paths are real.
catalogue_json=$(run_ok "$cli" node list --json)
expect_in "$catalogue_json" '"operation":"list-node-types"'
expect_in "$catalogue_json" '"kilasflow.manual"'

node_json=$(run_ok "$cli" node describe kilasflow.manual --json)
expect_in "$node_json" '"type":"kilasflow.manual"'

credential_json=$(run_ok "$cli" credential list --json)
expect_in "$credential_json" '"operation":"list-credentials"'

datastore_json=$(run_ok "$cli" datastore list --json)
expect_in "$datastore_json" '"operation":"list-datastores"'

schedule_json=$(run_ok "$cli" schedule list --json)
expect_in "$schedule_json" '"operation":"list-schedules"'

tenant_json=$(run_ok "$cli" tenant list --json)
expect_in "$tenant_json" '"id":"operator"'

tenant_users_json=$(run_ok "$cli" tenant users operator --json)
expect_in "$tenant_users_json" '"operation":"list-tenant-users"'

tenant_one_json=$(run_ok "$cli" tenant get operator --json)
expect_in "$tenant_one_json" '"id":"operator"'

# The paths that need an id are exercised with an id nothing holds. A wrong path
# answers 404 as well now, so these only say the verb reaches the server and
# maps a 404 to exit 4; the listings above are what prove the paths exist.
expect_exit 4 "$cli" datastore get 00000000-0000-0000-0000-000000000000 --json
expect_exit 4 "$cli" datastore rows 00000000-0000-0000-0000-000000000000 --json
expect_exit 4 "$cli" datastore export 00000000-0000-0000-0000-000000000000 --out "$work_dir/rows.csv"
expect_exit 4 "$cli" credential get 00000000-0000-0000-0000-000000000000 --json

# pack validate is local: it must work with no server at all.
run_ok "$cli" pack validate "$repo_dir/packs/telegram/pack.json" --url '' --json >/dev/null

# 4. The exit codes an agent branches on. Each one is a different decision:
#    fix the invocation, stop, wait, or report.
expect_exit 4 "$cli" workflow get 00000000-0000-0000-0000-000000000000 --json
expect_exit 2 "$cli" api not-an-operation
expect_exit 1 "$cli" health --url http://127.0.0.1:1
expect_exit 3 "$cli" auth whoami --url "$base_url" --token kfa1_bad_nope

echo "smoke-cli: passed"
