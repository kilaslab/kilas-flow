#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$repo_dir"

for command in curl docker; do
	command -v "$command" >/dev/null 2>&1 || {
		echo "smoke-postgres: $command is required" >&2
		exit 1
	}
done
docker compose version >/dev/null

project="kilasflowsmoke$(date +%s)$$"
app_container="${project}-app"
image=${KILASFLOW_SMOKE_IMAGE:-kilasflow:latest}

cleanup() {
	docker rm -f "$app_container" >/dev/null 2>&1 || true
	docker compose -p "$project" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

wait_for_postgres() {
	container=$(docker compose -p "$project" ps -q postgres)
	attempt=0
	while [ "$attempt" -lt 50 ]; do
		if [ "$(docker inspect -f '{{.State.Health.Status}}' "$container" 2>/dev/null || true)" = healthy ]; then
			return 0
		fi
		attempt=$((attempt + 1))
		sleep 1
	done

	echo "smoke-postgres: PostgreSQL did not become healthy" >&2
	docker compose -p "$project" logs postgres >&2 || true
	return 1
}

wait_for_ready() {
	attempt=0
	while [ "$attempt" -lt 50 ]; do
		if curl -fsS "$1/api/v1/ready" >/dev/null 2>&1; then
			return 0
		fi
		if [ "$(docker inspect -f '{{.State.Running}}' "$app_container" 2>/dev/null || true)" != true ]; then
			docker logs "$app_container" >&2 || true
			return 1
		fi
		attempt=$((attempt + 1))
		sleep 1
	done

	echo "smoke-postgres: KilasFlow did not become ready" >&2
	docker logs "$app_container" >&2 || true
	return 1
}

docker info >/dev/null
if [ "${KILASFLOW_SMOKE_SKIP_BUILD:-0}" != 1 ]; then
	make docker
fi
docker compose -p "$project" --profile postgres up -d postgres
wait_for_postgres

docker run --rm --network "${project}_default" \
	-v "$repo_dir:/src:ro" -w /src \
	-e 'KILASFLOW_TEST_POSTGRES_DSN=postgres://kilasflow:kilasflow@postgres:5432/kilasflow?sslmode=disable' \
	golang:1.27-alpine \
	go test ./internal/database -run '^TestMigratePostgres$$' -count=1

docker run -d --name "$app_container" --network "${project}_default" -p 127.0.0.1::8080 \
	-e KILASFLOW_DATABASE_DRIVER=postgres \
	-e 'KILASFLOW_DATABASE_DSN=postgres://kilasflow:kilasflow@postgres:5432/kilasflow?sslmode=disable' \
	"$image" >/dev/null
port=$(docker inspect -f '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "$app_container")
base_url="http://127.0.0.1:$port"

wait_for_ready "$base_url"
curl -fsS "$base_url/api/v1/health" | grep -q '"status":"ok"'
curl -fsS "$base_url/api/openapi.json" | grep -q '"openapi"'
curl -fsS "$base_url/app/workflows/smoke" | grep -qi '<!doctype html>'

echo "smoke-postgres: passed"
