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

# Named explicitly rather than relying on Compose picking up whatever file is in
# the working directory. The PostgreSQL service moved out of the old
# docker-compose.yml and into an overlay that only exists when it is asked for,
# so both files have to be listed or `postgres` is not a service at all. Naming
# them also keeps this script honest about which stack it is proving: the one the
# quickstart documents, not a development file that has drifted from it.
compose="docker compose -p $project -f compose.yaml -f compose.postgres.yaml"

# The overlay reads these three from the environment, falling back to a
# developer's .env. Both DSNs further down spell the same credentials out, so
# pin them here: without this, somebody who set a real password in .env would get
# a PostgreSQL container using it and a smoke run still connecting with the
# default, and the failure would look like a broken database rather than a
# mismatch. The shell environment beats .env for Compose interpolation.
KILASFLOW_POSTGRES_USER=kilasflow
KILASFLOW_POSTGRES_PASSWORD=kilasflow
KILASFLOW_POSTGRES_DB=kilasflow
export KILASFLOW_POSTGRES_USER KILASFLOW_POSTGRES_PASSWORD KILASFLOW_POSTGRES_DB

cleanup() {
	docker rm -f "$app_container" >/dev/null 2>&1 || true
	$compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

wait_for_postgres() {
	container=$($compose ps -q postgres)
	attempt=0
	while [ "$attempt" -lt 50 ]; do
		if [ "$(docker inspect -f '{{.State.Health.Status}}' "$container" 2>/dev/null || true)" = healthy ]; then
			return 0
		fi
		attempt=$((attempt + 1))
		sleep 1
	done

	echo "smoke-postgres: PostgreSQL did not become healthy" >&2
	$compose logs postgres >&2 || true
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
$compose up -d postgres
wait_for_postgres

docker run --rm --network "${project}_default" \
	-v "$repo_dir:/src:ro" -w /src \
	-e 'KILASFLOW_TEST_POSTGRES_DSN=postgres://kilasflow:kilasflow@postgres:5432/kilasflow?sslmode=disable' \
	golang:1.27-alpine \
	go test ./internal/database -run 'Postgres' -count=1

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
