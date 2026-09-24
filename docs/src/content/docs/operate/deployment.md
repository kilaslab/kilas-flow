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
make docker
docker run -d -p 127.0.0.1::8080 -v ./data:/app/data kilasflow:latest
```

`kilasflow:latest` is the tag `make docker` writes next to the versioned one. No
image has been published to a registry yet, so that build above is not optional:
a `docker run` without it pulls nothing. The published coordinates are
`ghcr.io/kilaslab/kilasflow` (see the `IMAGE` variable in the Makefile), and they
will be filled once a release tag exists.

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

Queue wakeups over `LISTEN`/`NOTIFY` and claim granularity over
`FOR UPDATE SKIP LOCKED` are built into the PostgreSQL tier, as is the guard
that refuses a workflow credential naming the installation's own database (see
[Security](/operate/security/)). What is written here is what the tier does
today.

## Multiple workers against one PostgreSQL database

Throughput scales by running more processes against the same database, not a
bigger machine. Point two (or more) processes at the same PostgreSQL DSN and
each runs its own worker pool over the same durable queue: every claim stamps
a fenced lease owner, so of any number of workers racing for one execution
exactly one wins, and every later write carries that fencing token — a worker
whose lease was reclaimed cannot write over its successor.

### Roles

One binary, three shapes via `--role` (default `both`, today's behaviour):

- `both` runs the API, the workers and the scheduler in one process.
- `api` serves the API and the scheduler with no workers.
- `worker` (or `workers`) runs workers with no API and no scheduler.

A split deployment is one `api` process beside N `worker` processes. The
scheduler stays single by role — workers never run it — and several
`api`/`both` processes stay safe anyway: the due claim advances `next_run_at`
inside the same transaction that reads it, so one due time still queues
exactly one execution. A split role on the SQLite driver is refused at
startup: SQLite holds one writer, no `LISTEN`/`NOTIFY` and one file, so two
processes on it are a corruption story rather than a scaling story.

Five behaviours make this work:

- **Distinct worker identities.** Each process identifies itself as
  `kilasflow-<host>-<pid>-<random>` in `lease_owner` and the logs, so two
  containers never share an identity even when both start at pid 1.
  `--worker-id` overrides the default when an operator wants stable names.
- **Push wake with a poll fallback.** Queuing an execution notifies every
  listening process over PostgreSQL `LISTEN`/`NOTIFY`, so an idle worker in
  another process picks work up in milliseconds rather than on its next
  100 ms tick. The tick stays: a dropped notification costs latency, never a
  stuck execution.
- **Live events fan out.** A worker publishes node progress into its own
  broker and relays the event's identifiers (tenant, execution, node,
  sequence, type, status — never the node's output, which would exceed the
  ~8000-byte NOTIFY cap) to every API process, which republishes them into
  its own broker. A browser connected to the API process sees the run live;
  the durable trace behind `GET` stays complete whether or not a notice
  lands.
- **Cancel interrupts the holder.** `Cancel` persists `cancelling` in the
  row and notifies the process holding the lease, which stops its run
  between nodes — the runner checks the request before scheduling each
  node, and the holder polls the row on its tick even when the notice is
  dropped. A node that ignores its context still finishes its current
  attempt; the next node never starts, and the terminal write records
  `cancelled` with the lease released.
- **Shutdown settles, `kill -9` is reclaimed.** `SIGTERM` stops workers
  claiming new work; an in-flight run is interrupted and written as
  cancelled with its lease released, so nothing is left running with a
  lease held. A process that dies without settling (`kill -9`, power loss)
  leaves the lease held until it expires, and then another worker reclaims
  the execution and runs it — with the partial trace cleared and the dead
  worker's writes fenced out.

What breaks it:

- **Clock skew.** Leases compare wall clocks across processes: a worker
  whose clock runs ahead considers other workers' leases expired early and
  reclaims executions that are still running. Persistence stays
  at-most-once (fencing), but the graph's side effects — HTTP calls, sent
  messages — happen twice. Run NTP on every worker, and keep
  `execution.default_timeout` comfortably above both the skew and the
  longest run: the same timeout bounds one run end to end and the lease it
  holds, so a workflow that runs longer than its lease is reclaimed
  mid-flight by design.
- **SQLite.** One writer, no `LISTEN`/`NOTIFY`, one file: never point two
  processes at the same SQLite database. Multi-worker topologies require
  the PostgreSQL driver.

## Shared customer database with the `kflow_` prefix

The topology the white-label operator most wants: KilasFlow's tables live
alongside the host application's in one PostgreSQL database, distinguished by
a table prefix. This is supported as a naming convention today and must be
understood as exactly that — **a table prefix is a naming convention, not an
isolation boundary**. It stops table-name collisions. What it does not stop is
the *host application* reading `credentials`, `workflows` and every execution
payload: both live in one database, and no prefix changes what the other
client's role may read. KilasFlow's own fight is a different one, and it is
covered — a `kilasflow.postgres` node whose credential names the installation's
own host, port and database is refused before it dials, prefix or no prefix, and
SQL targets are held to the instance egress policy like HTTP ones (see
[Security](/operate/security/)).

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
  unreachable, and also while a datastore migration is outstanding — a
  datastore below the schema version this build serves. Its `datastores`
  block reports the schema-version spread as counts and versions only, and it
  is served on the `200` body beside `status` and `database` and on the `503`
  a migration in flight answers, so a monitor reads the spread from a field
  rather than parsing `detail`. A `503` for an unreachable database carries no
  block: it cannot read the catalogue. Note
  that `/ready` is public: the block therefore discloses the installation's
  total datastore count to anyone who can reach the port.

Note that the `Code` node needs a Go toolchain at run time and the distroless
image does not have one. The server reports the node as unavailable through
the node catalogue rather than failing at execution time, so the editor can
say so.

For the same reason in reverse, the opt-in JavaScript sidecar needs Node 24 in
the image and the operator's community packages installed beside the binary:
the catalogue is read from them at boot, so **every** process role that boots
with `sidecar.enabled` must carry both. See
[JavaScript sidecar](/operate/javascript-sidecar/).

The Code (JavaScript) node needs nothing in the image: its engine is linked into
the binary. It runs each script in a worker process the server starts from its
own executable, so `ps` shows the kilasflow binary more than once, and a
process that runs workflows needs room for them. Budget up to
`code.javascript_max_concurrent` workers (one per CPU by default) on top of the
server, each idling at a few tens of MiB and allowed a live heap up to
`code.javascript_heap_ceiling_mb` (1 GiB by default) while it runs. On Linux a
worker tells the kernel to kill it first when memory runs out, so an undersized
container loses a script rather than the server. See
[safety boundaries](/concepts/safety-boundaries/#worker-processes).

On Linux each worker is also confined — user, PID, network and IPC namespaces of
its own, and landlock — as far as the kernel grants it, and the server logs once
which layers it got. Docker's default seccomp profile refuses user namespaces, so
in a plain `docker run` the workers get landlock but not the namespaces; a
seccomp profile that allows `clone` with `CLONE_NEWUSER` gives them both. A
server running as root may instead run every worker as a user of its own with
`code.javascript_worker_uid` and `code.javascript_worker_gid`. See
[what confines a worker](/concepts/safety-boundaries/#what-confines-a-worker).
Note that the `Code` node needs a Go toolchain at run time to compile source it
has not seen before, and the distroless image does not have one. The server
reports the node as unavailable through the node catalogue rather than failing
at execution time, and the message names what to provide: mount a toolchain and
set `code.go_binary` (`KILASFLOW_CODE_GO_BINARY`) to its `go` binary, or put its
`bin` directory on the process's `PATH`. Nothing else is needed — the build runs
with the network disabled against the standard library only.

`code.cache_dir` (default `./data/codecache`, inside the data volume) is where a
compiled artifact and wazero's translation of it are kept, so a restart reuses
them instead of rebuilding; `code.cache_max_bytes` bounds that directory (default
2 GiB, `0` unbounded). Those files are native machine code the server executes,
so the directory must stay writable only by the kilasflow user. An empty
`code.cache_dir` keeps every cache in memory, which is what a deployment with no
writable volume should use.
