---
title: Deploy for SaaS
description: The copy-paste topology for running KilasFlow inside a host product — schema and role isolation in a shared PostgreSQL, origin and proxy routing, sizing, pinning, and the tenant lifecycle.
sidebar:
  order: 1.5
---

[Deployment](/operate/deployment/) describes the three topologies and what each
gives up. This page is the recipe for the one a host SaaS usually wants:
KilasFlow beside the host application, on the same PostgreSQL server, isolated
by schema and role, reached through the host's own proxy at an origin of its
own. Everything here names the configuration keys and the SQL it takes; the
product behaviour behind them is covered on the pages this one links to.

Nothing on this page is proven by a smoke script in this repository — the
committed scripts cover the SQLite and Compose-PostgreSQL shapes. The SQL is
standard PostgreSQL 16 and the rest is configuration the reference documents;
where a claim rests on the product's code rather than a test, the page says so.

## A dedicated schema, a role, and `search_path`

The only placement that isolates KilasFlow from the host application's tables
is a schema KilasFlow owns, reached by a role that has no rights outside it
(see [What isolates, and what does not](/operate/security/#what-isolates-and-what-does-not)).
On PostgreSQL 16:

```sql
CREATE ROLE kilasflow LOGIN PASSWORD 'change-me';
CREATE SCHEMA kilasflow AUTHORIZATION kilasflow;
-- The role may use the database and its own schema, and nothing else:
REVOKE ALL ON SCHEMA public FROM kilasflow;
GRANT USAGE ON SCHEMA kilasflow TO kilasflow; -- owned, so this is explicitness
```

KilasFlow's migrations and queries name their tables unqualified, so they
follow the connection's `search_path`. Setting it on the DSN puts every table
it creates into that schema with no prefix configuration at all:

```sh
KILASFLOW_DATABASE_DRIVER=postgres
KILASFLOW_DATABASE_DSN='postgres://kilasflow:change-me@db.internal:5432/appdb?sslmode=require&options=-csearch_path%3Dkilasflow'
```

The `options` parameter is the standard libpq startup parameter (`-c` plus a
`key=value`, percent-encoded in the URL); the Go driver passes it through in
the startup message. A role with no rights on `public` that connects with this
DSN can create and reach exactly the `kilasflow` schema — the database
privileges, not the prefix, are the boundary.

Two notes on what this does not change:

- The kilasflow role can still read *its own* tables, which include every
  tenant's credentials (sealed) and execution payloads. Schema isolation is
  between KilasFlow and the host application, not between tenants — tenant
  isolation is a `WHERE` clause enforced in the repository layer (see
  [Tenancy and the embed boundary](/concepts/tenancy-and-embedding/)).
- A workflow credential naming the installation's own host, port and database
  is refused before it dials, schema or no schema — the guard compares the
  resolved target against the DSN, not the schema (see
  [Security](/operate/security/)).

## `table_prefix`: when the schema is not yours to take

Some hosts cannot grant a schema — the application's migrations own the
database, or a platform provisioner hands out one database and no DDL rights.
`database.table_prefix` is for that case, and `kflow_` is the value the
configuration reference uses in its example:

```sh
KILASFLOW_DATABASE_TABLE_PREFIX=kflow_
```

The prefix namespaces every table, index, constraint and datastore physical
table the install creates: `kflow_workflows` beside the host's `workflows`.
Its rules are enforced at boot, not advised:

- lowercase letters, digits and underscores, ending in an underscore;
- at most 16 characters, so the longest identifier built from it stays inside
  PostgreSQL's 63-byte name limit;
- fixed for the life of the install — starting with a different prefix than
  the database already holds refuses to boot rather than creating a second
  schema alongside the populated one.

**A prefix is a naming convention, not an isolation boundary.** It stops
table-name collisions; it does not stop the host application's role reading
`kflow_credentials` or any execution payload, because both live in one
database and no prefix changes what the other role may read. Take the schema
recipe above when you can; take the prefix when collisions are the whole
problem. `checkTablePrefix` in `internal/database` is the boot refusal.

## Deploying beside the host application

**KilasFlow needs its own origin.** There is no base-path configuration: the
SPA is served at `/`, the API at `/api/v1`, webhooks at `/webhook/{route}`,
and no setting moves them under a prefix. Behind the host's proxy that means
a dedicated subdomain (`flows.example.com`) or a port of its own. A shared
origin under a path (`example.com/flows/`) is not a shape the product serves.

Which routes must reach the process, by who uses them:

| Caller | Routes | Notes |
| --- | --- | --- |
| Browsers (host pages, operator dashboard) | `/`, `/docs`, `/api/v1/*`, `/embed/{workflowId}` | `/embed/*` is the iframe editor your pages mount; everything else is the dashboard and API |
| Webhook senders (server-to-server, third parties) | `/webhook/{opaque-route}` | Routes are minted per tenant, workflow and trigger node, and reported by workflow import |
| Continuation links (emailed, shown to a human) | `/resume/{token}`, `/approve/{token}` | Waiting runs continue or resolve through these |
| Health checks (your orchestrator) | `/api/v1/health`, `/api/v1/ready` | Liveness and readiness; `/ready` answers 503 through a datastore migration |

Set `server.public_url` (`KILASFLOW_SERVER_PUBLIC_URL`) to the address
browsers load KilasFlow from — the origin the proxy publishes, without a
trailing slash. Webhook lifecycle hooks tell remote services where to deliver
from it, and the embed boundary derives the iframe's own origin from it: with
it empty, KilasFlow works its origin out from the request's `Host`, so a proxy
that rewrites `Host` makes every embedded save and run answer 403 while
loading still works (see the
[operator checklist](/guides/embedding/#operator-checklist)).

`embed.allowed_origins` (`KILASFLOW_EMBED_ALLOWED_ORIGINS`) lists every origin
your pages are served from, exactly — scheme, host, port, no wildcards, no
suffix match. A host on staging and production lists both; local development
(`http://localhost:5173`) is another entry, not an exception. An empty list
disables embedding entirely, which fails as an iframe that never loads.

You do not write a framing CSP for KilasFlow: the process serves
`/embed/*` with `Content-Security-Policy: frame-ancestors 'self' …`, the
allowlist above appended as origins, and serves everything else — the
dashboard — with `frame-ancestors 'self'` plus `X-Frame-Options: SAMEORIGIN`,
because the operator's own pages may frame it and nobody else may. `X-Frame-Options`
is deliberately absent on `/embed/*`: it cannot express an origin list, so a
browser that still honours it would refuse the frames the allowlist permits.
What is yours to write is the host page's own policy, if it ships one: it must
allow KilasFlow's origin as a frame source (`frame-src`, or `child-src` on
older policies), or the iframe dies in your page before KilasFlow sees a
request.

**Separate database and role versus `table_prefix`** is the decision this
section and the two above divide between them. A separate database is the
clean cut — backup, restore, upgrade and quota stories do not have to
disentangle two applications' rows — and costs one more thing to operate. A
shared database with the schema recipe is nearly as clean and keeps one dump.
The prefix alone is last: it solves collisions and nothing else.

## Roles and sizing

One binary, three shapes via `--role` (`both` the default): `api` serves the
API and the scheduler with no workers; `worker` runs workers with no API and
no scheduler. A split deployment is one `api` process beside N `worker`
processes, all on the PostgreSQL driver — a split role on SQLite is refused
at startup (see [Deployment](/operate/deployment/#roles)).

The connection pool derives from `execution.max_concurrent` unless you set
`database.max_open_conns` yourself: open connections are
`max_concurrent + 5` (the headroom is the scheduler, the sweepers, the webhook
receiver and the API), clamped to at least 4 and at most 50; idle matches
open. Each process holds its own pool:

| Deployment | `execution.max_concurrent` | Pool per process | Connections |
| --- | --- | --- | --- |
| 1 × `both` (default) | 10 (default) | 15 | 15 |
| 1 × `api` + 2 × `worker` | 10 | 15 each | 45 |
| 1 × `api` + 4 × `worker` | 10 | 15 each | 75 |
| Same, capped | 10, `max_open_conns=10` | 10 each | 50 |

The 50-connection ceiling on the derived pool exists to stay under
PostgreSQL's stock `max_connections` of 100, which every KilasFlow process and
the host application's own pool draw on together. A five-process fleet at the
derived default plus the host app is how deployments run out: count
`sum(processes × pool) + host` against `max_connections` before scaling
workers, and either set `database.max_open_conns` per process or raise
`max_connections` on the server. `--worker-id` gives stable names to the
leases and logs when the default `kilasflow-<host>-<pid>-<random>` is not
what an operator wants to read.

Workers are the unit of throughput, not tenants: every process runs its pool
over the same durable queue, claims are fenced, and a tenant's work lands on
whatever worker is free. There is no tenant-to-worker pinning.

## Multi-tenant capacity

- **Each tenant's ingress is rate limited; the shared queue is not.** The
  `ratelimit` section bounds, per tenant, how many webhook deliveries
  (`webhook_per_minute`, default 600), manual runs (`run_per_minute`, default
  120) and published events (`events_per_minute`, default 600) may be
  admitted per minute — one customer's flood is answered `429` with
  `Retry-After` and the `rate_limited` problem code before any execution
  exists. With more than one server process behind a load balancer, set
  `ratelimit.store: database` so the processes share one allowance;
  in-memory buckets would each grant the full rate. Per-tenant *overrides*
  of these defaults arrive with the tenant-settings surface; the
  deployment-wide defaults stand alone.
- **Worker capacity is shared, not per tenant.** A tenant is a row with no
  configuration beyond which node types it may see; one customer's hundred
  concurrent runs occupy the same workers and connections as any other
  customer's. Plan capacity for the sum of tenants, and watch
  `kilasflow_executions_running` against `kilasflow_execution_worker_capacity`
  on the [metrics endpoint](/operate/metrics/) to know when to raise
  `execution.max_concurrent`.
- **The JavaScript sidecar, if enabled, runs one Node process per tenant**
  with every allowlisted package in it, and `sidecar.max_processes` (default
  16) caps how many live at once — a wait beyond the cap queues until a
  process frees. For hundreds of tenants, raise `max_processes` and size the
  worker host for that many Node processes (`sidecar.max_rss_mb` is the
  per-process watchdog), or keep the sidecar off: it is opt-in, the most
  expensive thing in the deployment, and covers the 0.46% of template demand
  that is programmatic community packages (see
  [JavaScript sidecar](/operate/javascript-sidecar/)).

## Backups, pinning and upgrades on one page

- **Backups**: `pg_dump` the database on a schedule and before every change
  below. A dump is the form that survives a move between machines and
  PostgreSQL majors. When the schema recipe is in play, the dump is of the
  one database; the kilasflow schema goes with it (see
  [Backup and restore: PostgreSQL](/operate/upgrades/#backup-and-restore-postgresql)).
- **Pin the image tag and the SDK version together.** No image has been
  published to a registry yet; the reserved coordinates are
  `ghcr.io/kilaslab/kilasflow` and will fill once a release tag exists. Until
  then "pinning" is building from a known commit — record it. From the first
  tag, pin an exact version on both sides: the embed handshake and the API
  move together, and the reference host's README shows the pairing.
- **Upgrade every replica together, or not at all.** Boot applies pending
  migrations and the listener opens only when they succeed; a migration
  failure stops the container rather than degrading it. There is no
  release-to-release compatibility promise yet — the backup is the rollback
  plan. The full procedure, including the credential-reseal pass
  (`kilasflow -reseal-credentials`) that a current upgrade requires once
  every replica is new, is [Upgrades](/operate/upgrades/).

## The tenant lifecycle

- **Provision**: tenant rows are operator-managed — there is no self-service
  signup endpoint. `POST /api/v1/tenants` (operator key) creates the customer;
  the first API key per tenant is minted by the operator through
  `POST /api/v1/tenants/{id}/api-keys`, because key creation is scoped to the
  caller's own tenant. Your backend holds that key; the end user never sees
  it. The whole sequence is
  [Tenants, provisioning and identity](/operate/tenants/).
- **Operate**: your backend mints embed sessions per page load, re-checking
  entitlement each time. The whole path is
  [Embedding and multi-tenancy](/guides/embedding/).
- **Offboard**: `DELETE /api/v1/tenants/{id}` removes the customer and
  everything KilasFlow holds for it, irreversibly, in an order forced by
  foreign keys — including the physical datastore tables. The steps and the
  transactional behaviour are on
  [Tenant deletion](/operate/tenant-deletion/).
