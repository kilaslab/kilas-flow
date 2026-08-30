#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

for command in curl docker; do
	command -v "$command" >/dev/null 2>&1 || {
		echo "smoke-docker: $command is required" >&2
		exit 1
	}
done

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/kilasflow-smoke-docker.XXXXXX")
data_dir="$work_dir/data"
container="kilasflow-smoke-$(date +%s)-$$"
image=${KILASFLOW_SMOKE_IMAGE:-kilasflow:latest}

cleanup() {
	docker rm -f "$container" >/dev/null 2>&1 || true
	rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

wait_for_ready() {
	attempt=0
	while [ "$attempt" -lt 50 ]; do
		if curl -fsS "$1/api/v1/ready" >/dev/null 2>&1; then
			return 0
		fi
		if [ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null || true)" != true ]; then
			docker logs "$container" >&2 || true
			return 1
		fi
		attempt=$((attempt + 1))
		sleep 1
	done

	echo "smoke-docker: container did not become ready" >&2
	docker logs "$container" >&2 || true
	return 1
}

docker info >/dev/null
if [ "${KILASFLOW_SMOKE_SKIP_BUILD:-0}" != 1 ]; then
	make docker
fi
mkdir "$data_dir"
chmod 777 "$data_dir"
docker run -d --name "$container" -p 127.0.0.1::8080 \
	-v "$data_dir:/app/data" "$image" >/dev/null
port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$container")
base_url="http://127.0.0.1:$port"

wait_for_ready "$base_url"
curl -fsS "$base_url/api/v1/health" | grep -q '"status":"ok"'
curl -fsS "$base_url/api/openapi.json" | grep -q '"openapi"'
curl -fsS "$base_url/app/workflows/smoke" | grep -qi '<!doctype html>'
test -f "$data_dir/kilasflow.db"

echo "smoke-docker: passed"
