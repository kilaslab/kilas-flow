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
expression evaluator, webhook and cron triggers, sub-workflows, the n8n importer
and exporter, and the embedded-editor session flow are all implemented and
covered by tests. There are 39 node types available out of the box, counting
both the ones compiled into the binary and the declarative packs it ships with.

Execution is asynchronous. `POST /api/v1/workflows/{id}/run` writes a queued
execution and returns `202` immediately; a pool of workers claims work from the
database under a lease, so an execution whose worker dies is picked up again
rather than lost. Progress is readable as a server-sent event stream.

## What is not there yet

These are the gaps most likely to matter to somebody evaluating the project, and
they are listed here rather than left to be discovered.

**There is no authentication on the API.** Nothing in `/api/v1` requires a
credential. The one token mechanism that exists — the embed session — only ever
*narrows* what a caller may do; a request arriving without one is passed through
with full access. KilasFlow must therefore be deployed behind something that
authenticates, and must not be exposed directly to the internet.

**Multi-tenancy is modelled but not enforced.** Every stored row carries a
tenant identifier and every repository call takes a tenant scope, so the
plumbing is complete and consistent. But nothing resolves a real tenant from a
request, so at runtime there is exactly one tenant, named `default`. The
separation is ready for an authenticated caller to supply a scope; that caller
does not exist yet.

**Executions are not suspended to storage.** A `Wait` node holds its worker for
the duration and is capped at one hour. Waiting on an inbound webhook or a form
submission returns an error rather than parking the execution and resuming it
later.

**Scheduling is single-process.** The cron scheduler claims due rows
transactionally, so a second instance will not double-fire, but distributed
scheduling is not designed yet.

There is no external queue or broker: the queue is a table in the same database
as everything else. For the deployments this is aimed at, that is a feature — it
is one fewer thing to run — but it is a ceiling, and worth knowing about before
you reach it.

## Where the details are

Most of the sections of this site are still stubs. Until they are filled in, the
repository's [`README.md`](https://github.com/kilaslabs/k-flow/blob/main/README.md)
is the accurate document of record, and the doc comments on the Go packages
under `internal/` are the best description of how each piece works.
