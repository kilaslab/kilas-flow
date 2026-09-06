---
title: Deployment
description: The three supported topologies, what each one gives up, and the health endpoints.
sidebar:
  order: 1
---

The repository builds a distroless image that runs as a non-root user
(`make docker` builds it). Three topologies are supported. What follows is
written from what the smoke scripts prove, not from intent.

## Single container with SQLite

The default. The database is a file at `/app/data/kilasflow.db` inside the
container, on a declared `VOLUME`, created with its parent directories on
first boot:

```sh
mkdir data
chmod 777 data
docker run -d -p 127.0.0.1::8080 -v ./data:/app/data kilasflow:latest
```

The `chmod` is load-bearing rather than ceremonial: the process runs as
`nonroot`, so a bind mount owned by root with default permissions is a
database the server cannot write to. `scripts/smoke-docker.sh` proves exactly
this shape — non-root image, bind mount, health, OpenAPI document, SPA
fallback, and the database file appearing on the host.

What it gives up: SQLite tolerates a single writer, so the pool is pinned to
one connection and every execution worker serialises on it. One process, one
file, no replication. Backups are file copies; see
[Backups and upgrades](/operate/upgrades/).

## Container with external PostgreSQL

The Compose overlay `compose.postgres.yaml` is the working description:

```sh
docker compose -f compose.yaml -f compose.postgres.yaml up -d
```

It sets `KILASFLOW_DATABASE_DRIVER=postgres` and the DSN from
`KILASFLOW_POSTGRES_USER/PASSWORD/DB` (defaulting to `kilasflow` throughout),
waits on the database healthcheck before starting the app — KilasFlow applies
its migrations at boot and exits if it cannot reach the database, so losing
that race is a container that dies and restarts until PostgreSQL is ready —
and keeps the database port off the host. Nothing outside the stack needs it.
`scripts/smoke-postgres.sh` proves this stack end to end.

Two facts that surprise operators coming from the SQLite default:

- **Switching backends does not move data.** The schema is created fresh from
  `migrations/postgres` and the stack comes up empty. Decide before the first
  run, or export what matters first.
- **The pool follows the workers.** With `max_open_conns` unset the pool
  derives from `execution.max_concurrent` plus headroom for the scheduler, the
  sweepers, the webhook receiver and the API (clamped to 4–50, idle matching
  open). Raising the worker count raises the pool with it; setting either key
  takes it over. SQLite always reports one, because the single-writer pin and
  the WAL pragma set are a pair and a configuration claiming otherwise would
  send an operator hunting the wrong thing.

The remaining PostgreSQL tier work (queue wakeups, lock granularity, the
shared-database guard below) is tracked on the roadmap under V2-p6-3 and
follow-ups, and in `FEAT-a94c8y`. What is written here is what the tier does
today.

## Shared customer database with the `kflow_` prefix

The topology the white-label operator most wants: KilasFlow's tables live
alongside the host application's in one PostgreSQL database, distinguished by
a table prefix. This is supported as a naming convention today and must be
understood as exactly that — **a table prefix is a naming convention, not an
isolation boundary**. It stops table-name collisions. It does not stop a
`kilasflow.postgres` node whose credential points at the shared database from
reading `credentials`, `workflows` and every execution payload: the
internal-database guard currently refuses SQLite files only, and SQL nodes do
no host validation (`FEAT-a94c8y` closes both).

The only deployment that genuinely isolates KilasFlow from its host database
is a dedicated schema owned by a role with no rights outside it, with
`search_path` set on the KilasFlow connection. A guard inside the process
stops KilasFlow's own SQL nodes and nothing else; only database privileges
stop everything else.

## Health endpoints

Two endpoints for a load balancer or orchestrator, and the distinction is
deliberate:

- `GET /api/v1/health` is liveness. It answers `200` for as long as the
  process is serving and touches no dependency, so a database outage does not
  get the process killed and restarted into the same outage.
- `GET /api/v1/ready` is readiness. It answers `503` when the database is
  unreachable — the signal to stop sending traffic.

Note that the `Code` node needs a Go toolchain at run time and the distroless
image does not have one. The server reports the node as unavailable through
the node catalogue rather than failing at execution time, so the editor can
say so.
