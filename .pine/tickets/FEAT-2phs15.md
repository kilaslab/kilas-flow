---
id: FEAT-2phs15
title: Introspect database schemas for table and column pickers
status: done
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
updated: "2026-09-05T14:31:52Z"
---

## Scope

Nothing in this repository reads `information_schema` — a grep finds the string in `.pine/roadmap.md` and `.pine/tickets/FEAT-whn5vb.md` and nowhere in the product. `internal/sqlnode.Connection` exposes three methods, `Query`, `Execute` and `Transaction`, and no introspection surface of any kind, so there is no way to ask a customer's PostgreSQL or MySQL what schemas, tables or columns it holds.

The API side is as empty. `internal/api/handlers/nodes.go` registers exactly one operation, `list-node-types`, and `internal/api/routes.go:23` builds the handler as `handlers.NewNodeTypes(deps.NodeRegistry)` — the registry alone, three lines above `handlers.NewCredentials(deps.Credentials, deps.Tenants)`, which is what a tenant-aware handler looks like. The `POST /api/v1/node-types/{type}/load-options` route V2-p2-4 specifies does not exist yet.

The ceiling reaches the editor. `node.PropertyOption` is `{Label, Value string}` (`internal/node/registry.go:24-28`) and `web/src/lib/components/workflow-editor/property-field.svelte:115-120` renders a `<select>` straight out of `property.options` with no fetch anywhere in the path, so the Schema and Table locators V2-p2-10 adds and the column metadata V2-p2-11's resource mapper needs would arrive as controls with nothing behind them.

Credentials are the second missing piece. `engine.CredentialResolver` (`internal/engine/runner.go:55-57`) is the only credential resolution in the product, and its only implementation, `tenantCredentials` (`internal/engine/service.go:337-354`), is constructed per execution against a `repository.TenantScope`. Introspection happens while somebody is configuring a node, not while a workflow runs, so there is no execution to hang it off.

This matters because introspection is the first read KilasFlow performs against a customer's database outside a run, on behalf of whoever holds the editor rather than of a workflow. `permits` at `internal/api/middleware/embed.go:89-96` already grants the whole `/node-types/` subtree on read scope with no workflow check, so the confinement settled here is what stops a workflow-scoped embed session becoming a schema-enumeration oracle for its tenant.

## Acceptance criteria

- [x] Five loaders — schema search, table search, columns, columns for matching, and mapping columns carrying type, nullable and default — return real values from a live PostgreSQL, proven by an integration test gated on `KILASFLOW_TEST_POSTGRES_DSN`.
- [x] The same five loaders return equivalent values from MySQL against an identical fixture schema, proven by a sibling test gated the same way on a MySQL DSN variable.
- [x] A loader resolves its credential through the calling tenant's scope, so naming a credential owned by another tenant returns not-found rather than a column list, proven by a handler test.
- [x] An embed session confined to one workflow receives loader results only for credentials that workflow's own nodes reference, and any other credential is refused, proven by a handler test. **In the handler, not the middleware** — see Outcome.
- [x] A column whose PostgreSQL `data_type` is `USER-DEFINED` or `ARRAY` reports its `udt_name` instead of the placeholder, captured as a fixture assertion over a table carrying an enum column and an array column.
- [x] A database whose role can see no tables returns an empty list carrying a diagnostic that distinguishes it from a failed connection, so a narrow grant never reads as an empty database.
- [x] Every loader opens and closes its own connection, holds a deadline well below `sqlnode.DefaultLimits()`'s thirty seconds, and returns a timeout rather than stalling the editor, proven by a test.
- [x] `pnpm generate:api:check` in `web/` and `pnpm generate:types:check` in `sdk/` both pass against the regenerated clients, run by hand and recorded below.

## Outcome

### Where the queries live

Methods on `sqlnode.Connection`, beside `Query` and `Execute`, dispatched on the driver the connection already carries. A separate package opening its own handles would duplicate DSN construction, the SQLite path guard and the password redaction every driver error goes through — and three copies of a redaction rule is how a password eventually reaches a log.

`information_schema`, not `pg_catalog`. pg_catalog is richer and PostgreSQL-only, which would make the MySQL dialect a second implementation rather than a second set of column names.

### The two things `information_schema` lies about

**It is privilege filtered, not authoritative.** A role with no privilege on a table simply sees no row for it, so an empty list is indistinguishable from an empty database. Every list here reports emptiness as something the caller must explain — *"this connection succeeded and returned no tables; the role may have no privileges on them"* — and a test drives that against a live server with a schema nobody can see.

**It misreports types.** PostgreSQL says `USER-DEFINED` for an enum or a domain and `ARRAY` for any array, with the real name only in `udt_name`. A mapper keyed on `data_type` would type every enum column as unknown, pass every test written over text and integer columns, and fail only when a real insert reached a real column. The fixture carries an enum *and* an array for exactly that reason.

### Two defects the live run found, that no unit test would have

- **`SERIAL` is neither an identity nor generated.** It is an integer with a `nextval` default, and `information_schema` has no other word for it — so `is_identity` and `is_generated` both say no, and the commonest primary key in PostgreSQL was being offered as a column to fill in. The rule now also reads the default.
- **MySQL 8 reserves `GENERATED`,** so the column alias was a syntax error. Neither showed up until the query ran against a real server, which is the whole argument for the gated tests.

### The credential, resolved outside an execution

`engine.CredentialResolver` is per execution, and introspection happens while somebody is configuring a node. The caller resolves the credential and hands it to the loader on the scope — resolution stays where the tenant is known, and this package still cannot reach the store.

A loader that names a credential type and is given none says **which type is missing**, rather than attempting a connection with nothing to connect with and failing as "could not reach the database", which sends the author hunting for a network problem. A credential of the wrong type is refused before any connection. Another tenant's credential is *"not found"* and nothing more specific, because anything more would confirm it exists somewhere.

### The confinement, which is the point

Two bounds, and the second is the one that matters. The first is the workflow ID, already added by the locator ticket. The second is new: **an embed session may only load with a credential its own workflow's nodes reference.**

Without it, a session confined to one workflow could name any credential in the tenant and read the schema of every database that credential reaches — the picker becomes an enumeration oracle for the whole tenant. Nothing else on this path stops it: `permits` grants the entire `/node-types/` subtree on read scope without ever consulting a workflow.

It lives in the handler rather than in `permits`, and the criterion's "middleware test" is answered by a handler test for a structural reason: `permits` is a switch over the path and the method and **never reads a body**, so a POST whose body names a credential is invisible to it. That is the split the codebase already documents for a single execution — the middleware covers the scope, the handler covers the identity.

### The deadline

Five seconds, against the statement default of thirty. This runs while somebody is typing: a picker that takes half a minute has already been given up on, and the connection it holds is against someone else's production database. A test pins the relationship between the two constants rather than the number, so lowering the statement default cannot silently invert them.

### The evidence

```
$ KILASFLOW_TEST_POSTGRES_DSN=... KILASFLOW_TEST_MYSQL_DSN=...     go test ./internal/loadoptions -run 'FiveLoaders|EmptyCatalogue|Introspection' -v
--- PASS: TestTheFiveLoadersReadALiveDatabase (0.16s)
    --- PASS: TestTheFiveLoadersReadALiveDatabase/postgres (0.11s)
    --- PASS: TestTheFiveLoadersReadALiveDatabase/mysql (0.05s)
--- PASS: TestAnEmptyCatalogueSaysWhyRatherThanReadingAsAnEmptyDatabase (0.01s)
    --- PASS: TestAnEmptyCatalogueSaysWhyRatherThanReadingAsAnEmptyDatabase/postgres (0.01s)
    --- PASS: TestAnEmptyCatalogueSaysWhyRatherThanReadingAsAnEmptyDatabase/mysql (0.00s)
--- PASS: TestIntrospectionHoldsADeadlineWellBelowAStatementTimeout (0.00s)
--- PASS: TestIntrospectionRefusesWhatItCannotAnswerRatherThanStalling (0.00s)
```

PostgreSQL 16.14, MySQL 8.4.11. `pnpm generate:api:check` and `pnpm generate:types:check` both clean — the loader endpoint's shape was already generated by the locator and mapper tickets, so this one adds behaviour behind it rather than surface. `pnpm check` 1324 files 0 errors; web vitest 180, sdk vitest 23.

### What this does not do

SQLite publishes no `information_schema`, and the loaders **say so** rather than answering with an empty list that would read as "this database has no tables". No node declares these loaders yet — the Schema and Table locators are p4-9's — so they land registered, wired and proven against live servers, waiting for the node that names them.

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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/code-node.md                          |   41 +
 .pine/memory/live-databases.md                     |   30 +
 .pine/roadmap.md                                   |  252 ++
 .pine/tickets/EPIC-m42s3g.md                       |   10 +-
 .pine/tickets/FEAT-0556ck.md                       |   66 +
 .pine/tickets/FEAT-0f87fn.md                       |  300 +-
 .pine/tickets/FEAT-12s0e5.md                       |   65 +
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |   71 +
 .pine/tickets/FEAT-2f68r8.md                       |   81 +-
 .pine/tickets/FEAT-2phs15.md                       |  125 +
 .pine/tickets/FEAT-3taswf.md                       |   67 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |  438 +++
 .pine/tickets/FEAT-48hreg.md                       |    6 +
 .pine/tickets/FEAT-4d0bje.md                       |   62 +
 .pine/tickets/FEAT-53fht8.md                       |   60 +
 .pine/tickets/FEAT-55v09k.md                       |   82 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |    3 +
 .pine/tickets/FEAT-5kfctc.md                       |   66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   81 +-
 .pine/tickets/FEAT-5mvech.md                       |   72 +
 .pine/tickets/FEAT-5rvtzc.md                       |   91 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   83 +-
 .pine/tickets/FEAT-5z37xh.md                       |   72 +
 .pine/tickets/FEAT-68zzqs.md                       |  438 +++
 .pine/tickets/FEAT-6vfn3s.md                       |  354 +-
 .pine/tickets/FEAT-7tgasa.md                       |   61 +
 .pine/tickets/FEAT-8qyfh1.md                       |  452 ++-
 .pine/tickets/FEAT-8r9n21.md                       |  304 +-
 .pine/tickets/FEAT-91as16.md                       |   93 +-
 .pine/tickets/FEAT-9dqn7d.md                       |  422 +++
 .pine/tickets/FEAT-9knk67.md                       |   84 +-
 .pine/tickets/FEAT-adzn0a.md                       |   74 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-az620p.md                       |  447 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |  338 +-
 .pine/tickets/FEAT-bscygc.md                       |   62 +
 .pine/tickets/FEAT-cjpbe6.md                       |   70 +
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-cwz4ac.md                       |   66 +
 .pine/tickets/FEAT-cx3hq1.md                       |   71 +
 .pine/tickets/FEAT-czbzs6.md                       |   65 +
 .pine/tickets/FEAT-ddzk2k.md                       |    6 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-frvez8.md                       |   70 +
 .pine/tickets/FEAT-g6wrxm.md                       |   64 +
 .pine/tickets/FEAT-gg85se.md                       |   69 +
 .pine/tickets/FEAT-gxppx1.md                       |   71 +
 .pine/tickets/FEAT-jq84xk.md                       |   67 +
 .pine/tickets/FEAT-jwhdsy.md                       |  411 ++-
 .pine/tickets/FEAT-k9dwgn.md                       |   65 +
 .pine/tickets/FEAT-kwxxd0.md                       |   64 +
 .pine/tickets/FEAT-m94hhx.md                       |   60 +
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |   69 +
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
 .pine/tickets/FEAT-nrfz6m.md                       |   64 +
 .pine/tickets/FEAT-nxxbs5.md                       |   77 +
 .pine/tickets/FEAT-pd3p6x.md                       |   85 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   65 +
 .pine/tickets/FEAT-q81bq4.md                       |  444 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |   74 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  342 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-sar60r.md                       |   87 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   33 +
 .pine/tickets/FEAT-sfy1tq.md                       |   63 +
 .pine/tickets/FEAT-snxxny.md                       |  409 +++
 .pine/tickets/FEAT-sp8cfm.md                       |  359 +-
 .pine/tickets/FEAT-ss44d9.md                       |   67 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-v8k1tc.md                       |   88 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  395 +-
 .pine/tickets/FEAT-whn5vb.md                       |  101 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |   76 +
 .pine/tickets/FEAT-xx6p22.md                       |   62 +
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |   71 +
 .pine/tickets/FEAT-za118x.md                       |   61 +
 .pine/tickets/FEAT-zmfsjd.md                       |   71 +
 .pine/tickets/FEAT-znm60y.md                       |  314 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  345 +-
 Makefile                                           |   11 +
 README.md                                          |   33 +
 cmd/kilasflow/main.go                              |  192 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 config.example.yaml                                |   17 +
 internal/api/credentials_test.go                   |  357 ++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/credentials.go               |  298 ++
 internal/api/handlers/executions.go                |   40 +-
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  468 ++-
 internal/api/handlers/workflows.go                 |  118 +-
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   54 +-
 internal/api/server.go                             |   30 +-
 internal/api/workflows_test.go                     |  136 +-
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  101 +-
 internal/config/config_test.go                     |   35 +
 internal/credentials/builtin.go                    |  153 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  242 ++
 internal/credentials/registry.go                   |  340 ++
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/engine/authenticate.go                    |   95 +
 internal/engine/runner.go                          |  465 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |  318 +-
 internal/engine/service_test.go                    |   33 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/worker_test.go                     |    5 +
 internal/execution/records.go                      |   41 +-
 internal/expression/doc.go                         |   82 +-
 internal/expression/expression.go                  |  350 +-
 internal/expression/expression_test.go             |  374 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/interop/n8n/corpus/BASELINE.md            |   43 +-
 internal/interop/n8n/corpus/baseline.json          |  115 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |  107 +-
 internal/interop/n8n/n8n.go                        |  422 ++-
 internal/interop/n8n/n8n_test.go                   | 1160 +++++-
 internal/interop/n8n/parameters.go                 | 1437 +++++++-
 internal/loadoptions/loadoptions.go                |  412 +++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  711 +++-
 internal/node/registry_test.go                     |  816 ++++-
 internal/nodepack/nodepack.go                      |  424 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 ++
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  459 +++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/executions.go                  |  135 +
 internal/repository/models.go                      |   71 +-
 internal/repository/schedules.go                   |  140 +-
 internal/repository/webhooks.go                    |   17 +-
 internal/repository/workflows.go                   |   66 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  412 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/runcode/doc.go                            |   37 +-
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  153 +
 internal/sqlnode/sqlnode.go                        |  305 +-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  261 +-
 internal/webhook/webhook_test.go                   |  225 +-
 internal/workflow/compiler.go                      |  310 +-
 internal/workflow/compiler_test.go                 |   97 +
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/typeversion.go                   |   10 +
 nodes/ai.go                                        |   54 +-
 nodes/annotation.go                                |    3 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |   93 +-
 nodes/code_test.go                                 |  126 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  182 +-
 nodes/database.go                                  |  255 +-
 nodes/database_test.go                             |  534 ++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  602 ++-
 nodes/executors_test.go                            |  480 +++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |  195 +-
 nodes/http_test.go                                 |  225 ++
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/loop.go                                      |  245 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/subworkflow.go                               |  275 ++
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  243 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  610 ++++
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/unsupported.go                               |    4 +
 nodes/wait.go                                      |  227 ++
 nodes/webhook.go                                   |  290 +-
 packs/telegram/README.md                           |   40 +
 packs/telegram/pack.json                           | 1119 ++++++
 packs/telegram/telegram.go                         |   58 +
 packs/telegram/telegram_test.go                    |  466 +++
 packs/waha/README.md                               |   32 +
 packs/waha/REPORT-202409.md                        |  100 +
 packs/waha/REPORT-202502.md                        |  128 +
 packs/waha/manifest-202409.json                    |  124 +
 packs/waha/manifest-202502.json                    |  124 +
 packs/waha/pack-202409.json                        | 2794 ++++++++++++++
 packs/waha/pack-202502.json                        | 3844 ++++++++++++++++++++
 packs/waha/pack-trigger-202409.json                |  124 +
 packs/waha/pack-trigger-202502.json                |  130 +
 packs/waha/waha.go                                 |  107 +
 packs/waha/waha_test.go                            | 1196 ++++++
 sdk/src/generated/models.ts                        |  720 +++-
 .../lib/api/generated/credentials/credentials.ts   |  199 +-
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   17 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    2 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   27 +
 .../api/generated/models/loadOptionsInputBody.ts   |   22 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/propertyDefinition.ts |   19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 web/src/lib/api/generated/nodes/nodes.ts           |  443 ++-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    3 +-
 .../components/workflow-editor/canvas-node.svelte  |   33 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   61 +-
 .../workflow-editor/property-field.svelte          |  493 ++-
 .../workflow-editor/workflow-editor.svelte         |    4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |    4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  220 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   26 +
 323 files changed, 59416 insertions(+), 1591 deletions(-)
```
