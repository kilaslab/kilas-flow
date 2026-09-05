---
id: FEAT-2phs15
title: Introspect database schemas for table and column pickers
status: todo
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-45tfmh
    - FEAT-68zzqs
    - FEAT-whn5vb
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T08:28:30Z"
---

## Scope

Nothing in this repository reads `information_schema` — a grep finds the string in `.pine/roadmap.md` and `.pine/tickets/FEAT-whn5vb.md` and nowhere in the product. `internal/sqlnode.Connection` exposes three methods, `Query`, `Execute` and `Transaction`, and no introspection surface of any kind, so there is no way to ask a customer's PostgreSQL or MySQL what schemas, tables or columns it holds.

The API side is as empty. `internal/api/handlers/nodes.go` registers exactly one operation, `list-node-types`, and `internal/api/routes.go:23` builds the handler as `handlers.NewNodeTypes(deps.NodeRegistry)` — the registry alone, three lines above `handlers.NewCredentials(deps.Credentials, deps.Tenants)`, which is what a tenant-aware handler looks like. The `POST /api/v1/node-types/{type}/load-options` route V2-p2-4 specifies does not exist yet.

The ceiling reaches the editor. `node.PropertyOption` is `{Label, Value string}` (`internal/node/registry.go:24-28`) and `web/src/lib/components/workflow-editor/property-field.svelte:115-120` renders a `<select>` straight out of `property.options` with no fetch anywhere in the path, so the Schema and Table locators V2-p2-10 adds and the column metadata V2-p2-11's resource mapper needs would arrive as controls with nothing behind them.

Credentials are the second missing piece. `engine.CredentialResolver` (`internal/engine/runner.go:55-57`) is the only credential resolution in the product, and its only implementation, `tenantCredentials` (`internal/engine/service.go:337-354`), is constructed per execution against a `repository.TenantScope`. Introspection happens while somebody is configuring a node, not while a workflow runs, so there is no execution to hang it off.

This matters because introspection is the first read KilasFlow performs against a customer's database outside a run, on behalf of whoever holds the editor rather than of a workflow. `permits` at `internal/api/middleware/embed.go:89-96` already grants the whole `/node-types/` subtree on read scope with no workflow check, so the confinement settled here is what stops a workflow-scoped embed session becoming a schema-enumeration oracle for its tenant.

## Acceptance criteria

- [ ] Five loaders — schema search, table search, columns, columns for matching, and mapping columns carrying type, nullable and default — return real values from a live PostgreSQL, proven by an integration test gated on `KILASFLOW_TEST_POSTGRES_DSN`.
- [ ] The same five loaders return equivalent values from MySQL against an identical fixture schema, proven by a sibling test gated the same way on a MySQL DSN variable.
- [ ] A loader resolves its credential through the calling tenant's scope, so naming a credential owned by another tenant returns not-found rather than a column list, proven by a handler test.
- [ ] An embed session confined to one workflow receives loader results only for credentials that workflow's own nodes reference, and any other credential is refused, proven by a middleware test.
- [ ] A column whose PostgreSQL `data_type` is `USER-DEFINED` or `ARRAY` reports its `udt_name` instead of the placeholder, captured as a fixture assertion over a table carrying an enum column and an array column.
- [ ] A database whose role can see no tables returns an empty list carrying a diagnostic that distinguishes it from a failed connection, so a narrow grant never reads as an empty database.
- [ ] Every loader opens and closes its own connection, holds a deadline well below `sqlnode.DefaultLimits()`'s thirty seconds, and returns a timeout rather than stalling the editor, proven by a test.
- [ ] `pnpm generate:api:check` in `web/` and `pnpm generate:types:check` in `sdk/` both pass against the regenerated clients once the loader endpoint lands, run by hand and recorded on this ticket.

## Implementation Plan

Widen the node-types handler's dependencies first. Until a request handled outside an execution can resolve a credential in the caller's tenant there is nothing to introspect with and nothing to test, and the shape of that dependency decides whether the loader lives in the handler, behind an engine seam, or in a package of its own. `handlers.NewCredentials(deps.Credentials, deps.Tenants)` is the precedent to copy rather than invent around.

Put the queries in `internal/sqlnode`, as methods on `Connection` beside `Query` and `Execute`, dispatched on the unexported `driver` field the connection already carries. A separate `internal/schema` package opening its own handles is the obvious alternative and the wrong one: it duplicates DSN construction, the SQLite path guard and the password redaction `nodes.sanitize` performs on every driver error, and three copies of a redaction rule is how a password eventually reaches a log.

**Catalogue surface.** Query `information_schema` rather than `pg_catalog`. `pg_catalog` is richer — array element types, identity columns — but PostgreSQL-only, which would make the MySQL dialect a second implementation instead of a second set of column names. Reject caching the result in KilasFlow's own database as well: that turns a picker into a mirror needing invalidation, and copies the customer's schema into KilasFlow's storage.

The trap is that `information_schema` is privilege-filtered rather than authoritative. A role with no privilege on a table simply sees no row for it, so a picker listing nothing is indistinguishable from a database holding nothing. The same surface also lies about types: `data_type` reports `USER-DEFINED` for an enum or a domain and `ARRAY` for any array, with the real name only in `udt_name`, so a resource mapper keyed on `data_type` types every enum column as unknown, passes every test written over text and integer columns, and fails only when a real insert reaches a real column.

Bound the cost at the seam rather than in the client alone. `sqlnode.Open` pings and then pins the pool to a single connection (`internal/sqlnode/sqlnode.go:100-107`) against a credential defaulting to `sslmode=require`, so a loader invoked per keystroke is a handshake storm against someone else's production database. Confinement belongs in `permits`, matched ahead of the existing `strings.HasPrefix(path, "/node-types/")` case, which returns before any workflow is consulted; bound the returned values by `session.WorkflowID` (`internal/embed/embed.go:97`).

**Endpoint shape.** Recommend extending V2-p2-4's `load-options` with an internal-source kind rather than adding a second route, because two routes means two authorisation paths and the amendment already places the internal kind in that contract. This reopens if locator search needs a search term plus a continuation token the options contract cannot express, in which case a dedicated introspection route is more honest than a `load-options` call carrying a smuggled query object.

## References

- Roadmap plan, p4 section, entry V2-p4-8: `.pine/roadmap.md`.
- `internal/sqlnode/sqlnode.go` — `Connection`, `Open`, `DefaultLimits`, and the package doc stating why a connection is never cached.
- `internal/api/handlers/nodes.go` — the single registered operation, `list-node-types`.
- `internal/api/routes.go` — line 23 builds the node-types handler from the registry alone; line 26 shows the tenant-scoped constructor to copy.
- `internal/api/middleware/embed.go` and `internal/embed/embed.go` — `permits` and its `/node-types/` case granting the whole subtree on read scope, and `Session.WorkflowID`, the bound a loader's results must respect.
- `internal/database/database_test.go` — `KILASFLOW_TEST_POSTGRES_DSN`, the established gate for a test needing a live PostgreSQL.
- `internal/engine/runner.go` and `internal/engine/service.go` — `CredentialResolver` and `tenantCredentials`, both reachable only from a running execution.
- `internal/credentials/credentials.go` — the `postgres` and `mysql` credential field lists a loader connects with.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the static `<select>` these loaders exist to replace.
- `.pine/tickets/FEAT-whn5vb.md` — V2-p2-4 and its internal-source amendment, of which this ticket is the first consumer.
