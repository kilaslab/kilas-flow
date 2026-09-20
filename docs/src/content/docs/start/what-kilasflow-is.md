---
title: What KilasFlow is
description: The shape of the product, what it does today, and the things it deliberately does not do yet.
---

KilasFlow is a workflow engine. A workflow is a directed graph of nodes: one of
them is a trigger, the rest transform data, call HTTP APIs, query databases, run
a language model, branch, loop, or hand off to another workflow. The engine
compiles that graph, runs it against a durable queue, and records every node's
input and output so a run can be inspected afterwards.

It exists because the alternative for a SaaS product that wants to offer
automation to its customers is either to build a workflow engine or to embed
somebody else's, and the obvious candidate to embed is licensed in a way that a
white-label, multi-tenant product cannot use. KilasFlow is Apache-2.0 and
reimplements the interchange format rather than the software, which is why it
can read and write n8n's workflow JSON without containing any n8n code.

## What it is made of

The whole product is one Go binary. The editor is a SvelteKit application
compiled to static files and embedded into that binary with `go:embed`, so the
API and the user interface are served by the same process on the same origin.
There is no separate frontend to deploy, no reverse proxy to configure between
them, and no CORS to think about in the default deployment.

Persistence defaults to SQLite at `./data/kilasflow.db` using a pure-Go driver,
so the binary has no cgo dependency and no database to install. PostgreSQL is
the supported alternative for anything that needs more than one process to read
the same data. Those two are the whole list — MySQL and MariaDB are supported as
databases a *workflow* can connect to through the SQL nodes, but not as the
engine's own store.

## What works today

The engine, the editor, the node registry, encrypted credential storage, the
expression evaluator, webhook and cron triggers, sub-workflows, durable waits,
opt-in authentication, the n8n importer and exporter, and the embedded-editor
session flow are all implemented and covered by tests. The registry serves 49
distinct node types out of the box, across 56 type-and-version pairs: 46 types
(51 pairs) compiled into the binary and 3 types (5 pairs) from the declarative
packs it ships with. [`GET /api/v1/node-types`](/reference/api/) is the list of
record, and those numbers were measured from it rather than declared here as a
promise.

Execution is asynchronous. `POST /api/v1/workflows/{id}/run` writes a queued
execution and returns `202` immediately; a pool of workers claims work from the
database under a lease that the worker renews while it lives, so an execution
whose worker dies is picked up again rather than lost — up to a reclaim cap,
after which it settles as failed instead of being re-run for ever. Progress is
readable as a server-sent event stream.

Waiting is durable too. A `Wait` node does not hold its worker: the execution is
parked in storage with status `waiting`, a checkpoint and a single-use resume
token, and it continues from that checkpoint when its deadline arrives or when
something calls its resume URL. A pause of a day costs a row, not a worker.

## What is not there yet

These are the gaps most likely to matter to somebody evaluating the project, and
they are listed here rather than left to be discovered.

**Authentication is implemented and off by default.** With `auth.enabled` false
— the default — nothing in `/api/v1` requires a credential, and reaching the
port is equivalent to being an administrator; the server says so in its log at
every start. Turned on, every operation under `/api/v1` except health,
readiness, login and logout needs either a signed-in session cookie or a Bearer
API key: `/api/v1/auth/*` signs a person in, `/api/v1/api-keys` mints machine
keys for the tenant that issued them, and `/api/v1/stream-tickets` mints the
single-use ticket a browser needs for an event stream, where it cannot set a
header. An embed session token is accepted in place of either and grants
strictly less than both. The default is off for a reason rather than out of
laziness: turning identity on for an installation with no account and no key
would answer every request with `401` and lock its operator out, so enabling it
with no signing key refuses to start instead. Until an operator enables it,
KilasFlow must be deployed behind something that authenticates and must not be
exposed directly to the internet.

**A tenant is a scope, not an isolation boundary.** Every stored row carries a
tenant identifier and every repository call takes a tenant scope, and a
request's tenant is now resolved from whatever authenticated it — an embed
session first, then the signed-in session or the API key. A row belonging to
another tenant therefore does not fail a permission check; it does not exist.
What stays thin is what a tenant *is*: an id, a name and timestamps, with no
per-tenant configuration and no per-tenant quota, all tenants sharing the same
tables in the same database, so the separation is only ever as good as the
`WHERE` clause that enforces it. And with authentication off, which is the
default, every caller is the operator and every request resolves to the one
tenant named `default`.

**Waiting is durable; crashing is not.** A suspended execution resumes from its
checkpoint, but a worker that dies mid-run is a different path: the reclaimed
execution re-runs from the beginning, so a node with a non-idempotent side
effect can perform it twice. Two ceilings bound a suspension: a single wait is
refused past seven days, and a wait that resumes on a call and is never called
fails by name at its own deadline — 24 hours when the node names none — rather
than staying parked for ever. A wait inside a called sub-workflow is refused
outright, because a child runs inline in its caller.

**Scheduling is single-process.** The cron scheduler claims due rows
transactionally, so a second instance will not double-fire, but distributed
scheduling is not designed yet.

There is no external queue or broker: the queue is a table in the same database
as everything else. For the deployments this is aimed at, that is a feature — it
is one fewer thing to run — but it is a ceiling, and worth knowing about before
you reach it.

## Where the details are

The [concepts](/concepts/architecture/) section describes how each piece works —
the execution model, the node registry, credentials, items and lineage, webhooks,
tenancy and the embed boundary — and the [HTTP API reference](/reference/api/) is
generated from the running server's own OpenAPI document rather than written by
hand. The doc comments on the Go packages under `internal/` remain the closest
description of the code itself, and the repository's
[`README.md`](https://github.com/kilaslabs/k-flow/blob/main/README.md) covers
building it, running it and laying it out.
