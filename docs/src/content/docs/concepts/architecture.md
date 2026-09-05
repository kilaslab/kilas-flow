---
title: Architecture
description: The shape of the running system — one process, the layers inside it, and the dependency rules that keep them apart.
sidebar:
  order: 1
---

This section describes the system that exists. Where something is planned but not
built, it says so rather than being written in the present tense — and where
something exists but is not yet reached by any code path, it says that too.

These pages were written on 2026-09-06, against the repository rather than
against any design document. The engine, the compiler, the node registry and the
expression evaluator were all settled at that point: the execution semantics
described here — branch pruning, paired-item lineage, multiple trigger roots,
bounded loops, error handling — are implemented and tested rather than intended.
The parts still moving are named on the pages that describe them.

The pages that follow are organised by concept rather than by Go package,
because the package layout is an implementation fact that a reader arriving from
outside does not have. One concept usually draws on several packages —
[safety boundaries](/concepts/safety-boundaries/) alone pulls from four — and
each page names the packages it describes so the documentation is a route into
the code rather than a substitute for it.

## One process

KilasFlow is a single Go binary that does three things at once.

It serves an HTTP API under `/api/v1`, built with [huma](https://huma.rocks) so
that the OpenAPI document is generated from the same Go types that serve the
requests. It runs a pool of worker goroutines that claim queued executions from
the database and run them. And it serves the editor: a SvelteKit application
compiled to static files and embedded with `//go:embed all:dist` in
`internal/web`, so the API and the user interface arrive on the same origin from
the same process.

The `all:` prefix in that embed directive is load-bearing rather than
decorative. Without it `go:embed` skips every path whose name begins with `_`,
and SvelteKit emits all of its JavaScript and CSS into an `_app` directory — so
a plain `dist` pattern compiles cleanly and produces a binary that serves
`index.html` with no assets behind it.

There is no separate frontend to deploy, no reverse proxy to configure between
the two halves, and no CORS to reason about in the default deployment. The cost
is that a frontend change requires a rebuild of the binary, which is why the
documentation site you are reading is deliberately *not* part of that tree.

## The layers, and the rule that keeps them apart

```
  HTTP  ──▶  internal/api        transport: huma operations, middleware, /docs
             internal/api/handlers
                    │
                    ▼
             internal/engine     execution: the runner, the worker pool
             internal/workflow   the document, the compiler, the IR
             internal/node       the node catalogue
             nodes/              the built-in node implementations
                    │  (repository interfaces)
                    ▼
             internal/repository GORM implementations, tenant-scoped
             internal/database   connection, migrations
```

The engine's package comment states the constraint directly: `internal/engine`
must not import `internal/api`, must not import `gorm.io/gorm`, and must not
import the agent framework. Transport is a caller rather than a dependency;
persistence is reached through repository-shaped interfaces declared in
`internal/engine` itself; the AI runtime is reached through `internal/ai`.

That is not architectural decoration. It is what makes the engine testable
without a database and what lets either the ORM or the agent framework be
replaced without touching execution semantics. `internal/workflow` is subject to
the same discipline in the other direction: it declares a `Catalog` interface
that the node registry satisfies, so the document model never depends on the
catalogue of nodes it validates against.

## Composition happens once, at boot

Almost everything interesting about a running instance is decided in
`cmd/kilasflow/main.go`, in an order that matters. The node registry is
assembled there — built-ins first, then the generated packs — and is read-only
for the rest of the process's life. The executor registry, the outbound HTTP
policy, the database guard and the option loaders are all built and bound in the
same pass.

The reason to do it there rather than lazily is that a wrong binding should be a
startup failure rather than a runtime one. Several checks exist purely to
enforce that:

- A pack bound to an executor this server has not installed is refused at
  registration rather than at its first request, and a node type and version
  registered twice is refused with both sources named.
- Every webhook lifecycle hook a registered trigger declares is verified to have
  an implementation bound, once the packs and the built-ins have both
  registered. A trigger declaring a hook nobody registered would otherwise save,
  activate, and silently never register itself with the remote service.
- Enabling authentication with no signing key configured refuses to start,
  because the alternative is a server that answers every request with `401` and
  that nobody can reach to fix.

The one thing deliberately evaluated per request rather than at boot is node
availability — whether this deployment can actually run a given node type. A
distroless image carries no Go toolchain, so it cannot build a Code node, and
that is reported through the node catalogue's `unavailable` field so the editor
can say so before a workflow is saved.

## Persistence

Storage defaults to SQLite at `./data/kilasflow.db` through a pure-Go driver, so
the binary has no cgo dependency and no database to install. PostgreSQL is the
supported alternative, and the only one, for a deployment that needs more than
one process against the same data. MySQL and MariaDB are supported as databases
a *workflow* can reach through the SQL nodes — never as the engine's own store.

`internal/database` opens exactly one handle and is explicit that it is never
exposed to workflows. The SQL nodes build their connections from credential
fields through `database/sql`; there is no default, inferred or selectable
connection they could name, and a node never sees a DSN at all. That structural
separation is the primary defence, and the path guard described under
[safety boundaries](/concepts/safety-boundaries/) is the backstop for the one
case where a SQLite file path could coincidentally point at KilasFlow's own.

Schema changes are numbered SQL files under `migrations/`, one directory per
dialect, run by a hand-written runner in `internal/database/migrate.go`. Nothing
reflects over Go structs to derive a schema: the schema is whatever the
checked-in SQL says it is, which is the only form an operator can review before
letting it run against a database they share with something else.

Everything above the repository layer depends on repository *interfaces* rather
than on the GORM handle, and every repository operation takes a `TenantScope` as
its first argument. See [tenancy and embedding](/concepts/tenancy-and-embedding/)
for what that scope is and where it comes from.

## The workflow document is ours

KilasFlow defines its own canonical workflow document in `internal/workflow`, and
never executes any other format. n8n's JSON is converted into that document on
import and produced from it on export by `internal/interop/n8n`, which is a
boundary adapter and nothing more — the project takes no runtime dependency on
any n8n package.

This is a licensing decision as much as a technical one. Reimplementing the
interchange format is what lets KilasFlow read and write n8n workflow JSON under
Apache-2.0 without containing any n8n code. The consequence you will notice
everywhere in these pages is that the format's conventions are honoured
exactly — the thirteen connection channel names are spelled as n8n spells them,
`typeVersion` stays a JSON number, and a resource locator keeps its `__rl`
sentinel — because a document has to survive a round trip through this server
unchanged.

## Where to go next

[The execution model](/concepts/execution-model/) is the page to read first: it
follows one workflow from a trigger through the queue to a recorded execution,
and everything else here is a detail of some step in it.

## Source

`cmd/kilasflow/main.go`, `internal/engine/doc.go`, `internal/web/embed.go`,
`internal/database/database.go`, `internal/database/migrate.go`,
`internal/workflow/doc.go`, `internal/interop/n8n/n8n.go`.
