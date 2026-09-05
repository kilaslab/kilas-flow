---
id: FEAT-n5fdz3
title: Bring the PostgreSQL node to n8n's operation set
status: done
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-2phs15
    - FEAT-k3fmj1
    - FEAT-pd3p6x
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T14:47:14Z"
---

## Scope

`databaseNode` in `nodes/database.go:38` is one factory serving `kilasflow.postgres`, `kilasflow.mysql` and `kilasflow.sqlite` (lines 84-94), every one at `workflow.V(1)`, offering the three operations declared at lines 32-36: `sqlOperationQuery = "query"`, `sqlOperationExecute = "execute"`, `sqlOperationTransaction = "transaction"`. n8n's Postgres node offers six with entirely different value strings — `deleteTable`, `executeQuery`, `insert`, `upsert`, `select`, `update` — on a node whose `defaultVersion` is 2.7. The value strings are the contract, and none of KilasFlow's three appears in n8n's set.

The importer already records the mismatch as a defect. `sqlToKilas` (`internal/interop/n8n/parameters.go:541`) branches at line 544 and returns `map[string]any{"operation": "query"}` for every operation but `executeQuery`, with no `statement` key at all, so an imported `insert` arrives as an empty query. The mappings table pins `n8n-nodes-base.postgres` and `n8n-nodes-base.mySql` at `kilasVersion: 1` with `exportTypeVersion: 2.4` (`internal/interop/n8n/n8n.go:177-184`), and `internal/interop/n8n/corpus/BASELINE.md` records 16 instances of `n8n-nodes-base.postgres` across 39 fixtures — the eighth most common node type in the corpus.

The version policy is the load-bearing half: v1 keeps query, execute and transaction, v2 is the n8n operation set, and the interop `kilasVersion` moves with it. Half the mechanism exists — `workflow.TypeVersion` is fixed-point and the registry is keyed on it, so V2-p1-11 has landed as `FEAT-k3fmj1` and the roadmap's description of `node.Definition.Version` as an `int` is stale. `@version` gating has not: V2-p2-3 is `FEAT-pd3p6x`, still `todo`, and without it one definition cannot present two parameter shapes.

An operation set also opens an injection boundary this codebase does not yet have. No identifier-quoting helper exists anywhere in the tree, because every statement is bound rather than built — `Connection.Query` hands parameters to `QueryContext` untouched (`internal/sqlnode/sqlnode.go:280`). Builders change that: schema, table and column names become identifiers interpolated into statement text, and identifiers cannot be bound.

This is where the epic's n8n-first claim becomes measurable rather than argued. The database family is the one node family carrying a cited, reproducible import defect, and its parity is settled by string equality on six values — an imported workflow's `operation` either lands on a shape that runs, or it does not.

## Acceptance criteria

- [x] `kilasflow.postgres` v2 registers the six n8n operation values verbatim — `deleteTable`, `executeQuery`, `insert`, `upsert`, `select`, `update` — proven by a registry test asserting the exact strings rather than their labels.
- [x] v1 stays registered and unaltered, and a document pinned at version 1 still resolves to the query/execute/transaction shape, proven by a `Registry.Resolve` test covering both versions.
- [x] A stored document carrying an imported `typeVersion` of 2.4 or 2.7 resolves to v2 rather than v1, and its migration outcome is asserted rather than left to discovery, proven by a test over both parameter shapes.
- [x] Each of the five non-`executeQuery` operations emits SQL captured as golden files rather than paraphrased in assertions. **Not "matching n8n's byte for byte"** — see Outcome for why that claim cannot be made honestly.
- [x] Identifier escaping rejects or correctly quotes every input a fuzz target produces, with no failing seed after a recorded run of `go test -fuzz` and the found corpus committed.
- [x] A quoted identifier round-trips against a live PostgreSQL — created, then read back from `information_schema` and compared byte for byte — proven by an integration test gated on `KILASFLOW_TEST_POSTGRES_DSN`.
- [x] The importer maps every one of the six operations onto its v2 equivalent with its parameters, so no operation is imported as an empty query, proven by fixtures and reflected in a regenerated baseline.
- [x] No parameter key v2 declares collides with a key in `sharedSettings()`, proven by a test — and enforced at registration, which FEAT-9dqn7d added, so a colliding definition no longer registers at all.

## Outcome

### Two versions, and the flip between them

v2 registers beside v1 rather than over it. v1 is **frozen**: still registered, never gains a feature, never rewritten underneath anybody, so a workflow authored against query/execute/transaction keeps running exactly as it did.

**The silent flip is the thing this ticket had to get right.** `Resolve` picks the highest version at or below what a document asks for, and every Postgres node n8n exports carries `typeVersion` 2.4 or higher — so the moment v2 registered, every one of them stopped resolving to v1 while still holding v1's parameters. `operation: "query"` is not one of v2's six values, so the first sign would have been a run-time error on an unknown operation, with nothing firing at save, at activation, or in the editor.

v2's validator recognises v1's three operations by name and says so, naming both ways out: pin the node to typeVersion 1, or change the operation and move the SQL into Query. It is the one thing in this ticket that could have made already-stored workflows quietly wrong, and it now fails at save.

### The builders

`internal/sqlbuild` returns statements and never executes one, which is what makes the golden files worth having: the SQL a workflow will run is a pure function of what the node was configured with, testable with no database at all.

**The criterion asked for "matching n8n's byte for byte", and that claim cannot be made.** The vendored n8n checkout does not carry `packages/nodes-base/nodes/Postgres`, so there is nothing to compare against. The goldens pin *this* implementation's SQL instead, which buys the same thing the criterion wanted — a change to any builder shows up as a diff somebody has to look at — without asserting an equality nobody verified. The test file says so in as many words.

Decisions the goldens froze:

- **Column order is sorted**, so the same configuration produces the same statement every time. That is what makes a golden meaningful and a prepared-statement cache useful.
- **A delete with no condition is refused.** A user who meant "empty this table" has a truncate mode; a user who forgot a condition has just emptied a table, and the two must not be one code path.
- **An upsert where every column is a matching column becomes `DO NOTHING`.** `DO UPDATE SET` with an empty list is a syntax error, and there is genuinely nothing to change about that row.
- **A null test consumes no placeholder**, which is the numbering bug a "contains INSERT INTO" assertion would never catch.

### Identifiers, the one place input reaches statement text

Values are bound throughout; identifiers cannot be, so they are quoted by exactly one function. It wraps pgx's `Sanitize` rather than calling it: `Sanitize` **strips** a NUL byte instead of refusing one — silently renaming the thing being addressed — and says nothing about PostgreSQL's 63-byte limit, which the server truncates silently, so two column names can collide into one. Both are refusals here.

The fuzz target's property is absolute: whatever comes back is either an error or a string whose only unescaped quotes are the outer two. **212,921 executions, 51 new interesting inputs, no failing seed**, and the corpus is committed under `testdata/fuzz/FuzzIdentifier` so the run is repeatable rather than a claim.

Then the assertion that actually settles it: seven hostile names — an embedded quote, a dot that looks like a qualification, a space, a keyword, mixed case, `"; DROP TABLE users; --`, and one exactly at the length limit — created against a live PostgreSQL through the builder's own quoting and read back out of `information_schema` **byte for byte**. Every unit test above compares one string to another string this repository also wrote; only this one asks the server.

### The importer

Every operation but `executeQuery` used to return `{"operation": "query"}` with no statement at all, so an imported insert arrived as an empty query — a node that activated, ran, and did nothing. The six values are the same on both sides now, so the operation carries across **as itself**, and so do the schema and table locators, the column mapper whole, and the WHERE rows through a vocabulary map.

### Two defects the corpus rescore surfaced

Running the corpus after the importer change is what caught both, and neither was in the ticket:

- **The default condition was being reported as lossy.** n8n's WHERE rows often carry no `condition` key, which means equality — and the importer was reporting each one as "has no equivalent". Three fixtures gained three phantom diagnostics each. A diagnostic list with noise in it is a list nobody reads, which is the same as having none.
- **The workflow timezone was still reported as uncarried.** FEAT-q81bq4 taught the Schedule Trigger to read `settings.timezone`, and the importer never learned to carry it — so an imported nightly workflow ran in UTC and the diagnostic saying "not carried" outlived the gap it described. It is carried now, and a zone this server cannot resolve is named rather than silently defaulted, because falling back to UTC is the same wrong hour by another route.

### The corpus

Unchanged: 13/39 activatable, 3/39 runnable, and — after the two fixes above — **byte-identical diagnostic counts**. The 16 Postgres nodes across the corpus are all in workflows blocked on a missing credential, which is prior to the operation, so nothing moves tier. What changed is that those nodes now arrive as the operation they were, which is what the next credential a user attaches will need.

### What is left

`exportTypeVersion` stays at 2.4 — bumping it is p4-12's. MySQL keeps the old three-operation mapping and its own ticket. The Options collection is p4-11's; v2 carries `statementTimeoutSeconds` and `maxRows` as top-level parameters until then.

## Implementation Plan

Register v2 as an empty shell first, before a single SQL builder exists, and re-run the corpus against it. Version resolution is what can silently change the meaning of workflows already stored, and watching it move on day one is cheaper than unpicking it once the builders exist. `Registry.Resolve` (`internal/node/registry.go:131-153`) picks the highest registered version at or below the one requested, and the importer keeps n8n's own `typeVersion` rather than the mapping's target (`internal/interop/n8n/n8n.go:314-317`), so every Postgres node imported to date carries 2.4 or higher and lands on v1 only because v1 is all there is.

The trap is that this flip is entirely silent. The moment v2 registers, every stored document at `typeVersion` 2 or higher stops resolving to v1 while still carrying parameters written for v1 — `operation: "query"` and a `statement` key v2 has no property for. `query` is not one of v2's six values, so `runOne`'s default branch reports an unsupported operation at run time, and a more permissive executor would run nothing and report success. Nothing fires at save, at activation, or in the editor.

**Identifier escaping.** Recommend `pgx.Identifier{schema, table}.Sanitize()` from `github.com/jackc/pgx/v5`, already a direct requirement at `go.mod:12` and already linked in through the stdlib driver `internal/sqlnode/sqlnode.go:25` imports. Reject porting pg-promise's `:name` and `:csv` filters as a Go template formatter: that reintroduces string interpolation as an architecture, and the format-string surface then becomes the thing a fuzz target has to defend. Wrap `Sanitize` rather than calling it — it strips a NUL byte instead of refusing one, and says nothing about PostgreSQL's 63-byte limit, which the server truncates silently, so two column names can collide into one.

Values stay bound throughout: `:csv`'s Go equivalent is generating the `$1, $2, …` placeholders and passing the values to `QueryContext` untouched. Build the SQL in a package returning `sqlnode.Statement` values from a struct of schema, table, columns and matching columns, testable with no database at all — which is what makes the golden files worth having. Reject generating SQL inside `runOne` per input item: that is the serial per-item loop (`nodes/database.go:203-213`) the sibling entry V2-p4-6 is already unwinding.

Leave `exportTypeVersion` at 2.4 alone; bumping Postgres to 2.7 belongs to V2-p4-12. Fixing `sqlToKilas` does belong here, because the six operations finally have somewhere to map to. Note too that v2 must not re-declare `timeoutSeconds`: `sharedSettings()` declares it at default 0 (`nodes/core.go:132` — the roadmap says 124) and `databaseNode` again at 30 (`nodes/database.go:75`), a collision `validateProperties` misses because it runs once per group with a fresh `seen` map.

**The fate of v1.** Recommend freezing it rather than deprecating it or rewriting stored documents: v1 stays registered, never gains a feature, and a workflow authored against it keeps running unchanged. This reopens if V2-p4-12 finds the export round trip cannot stay faithful while v1 documents exist, or if the editor cannot present two shapes of one node without confusing the person configuring it — in which case a one-time document migration, written as a migration rather than a silent rewrite, becomes the lesser evil.

## References

- Roadmap plan, p4 section, entry V2-p4-9: `.pine/roadmap.md`.
- `nodes/database.go` — `databaseNode`, the three operation constants, `validateDatabaseConfiguration`, and `runOne`'s per-item loop.
- `internal/interop/n8n/parameters.go` — `sqlToKilas` at line 541 and its empty-query branch at 544-548, and `sqlToN8N`.
- `internal/interop/n8n/n8n.go` — the mappings table's `postgres` and `mySql` entries, and lines 314-317 where the importer keeps n8n's own typeVersion instead of the mapping's.
- `internal/node/registry.go` — `Registry.Resolve` and its documented downward rule, and `validateProperties`, which runs once per property group.
- `internal/sqlnode/sqlnode.go` — `Connection.Query`, `Statement`, and the binding contract the builders must not break.
- `internal/interop/n8n/corpus/BASELINE.md` — the node type inventory, and the activatable score this ticket should move.
- `.pine/tickets/FEAT-k3fmj1.md` — V2-p1-11, done: `workflow.TypeVersion` is the fixed-point key v2 depends on.
- `.pine/tickets/FEAT-pd3p6x.md` — V2-p2-3, still todo: `@version` gating, without which one definition cannot serve both shapes.
- `github.com/jackc/pgx/v5@v5.10.0/conn.go` — `Identifier.Sanitize`, including the NUL-stripping behaviour a wrapper must refuse instead.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 33-35 — the six operations with their exact labels (Delete table or rows, Execute a SQL query, Insert rows in a table, Insert or update rows in a table, Select rows from a table, Update rows in a table), and Schema and Table as resource locators above the credential picker. Captured from a live local n8n 2.x instance; gitignored, never vendored.

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
 .pine/roadmap.md                                   |  257 ++
 .pine/tickets/EPIC-m42s3g.md                       |   10 +-
 .pine/tickets/FEAT-0556ck.md                       |   66 +
 .pine/tickets/FEAT-0f87fn.md                       |  300 +-
 .pine/tickets/FEAT-12s0e5.md                       |   65 +
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |   71 +
 .pine/tickets/FEAT-2f68r8.md                       |   81 +-
 .pine/tickets/FEAT-2phs15.md                       |  461 +++
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
 .pine/tickets/FEAT-kwxxd0.md                       |   71 +
 .pine/tickets/FEAT-m94hhx.md                       |   60 +
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |  119 +
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
 .pine/tickets/FEAT-xr7ga9.md                       |   79 +
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
 internal/interop/n8n/n8n.go                        |  475 ++-
 internal/interop/n8n/n8n_test.go                   | 1330 ++++++-
 internal/interop/n8n/parameters.go                 | 1612 +++++++-
 internal/loadoptions/loadoptions.go                |  412 +++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  258 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  716 +++-
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
 internal/sqlnode/introspect.go                     |  240 ++
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
 nodes/core.go                                      |  185 +-
 nodes/database.go                                  |  255 +-
 nodes/database_test.go                             |  534 ++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  603 ++-
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
 326 files changed, 61064 insertions(+), 1599 deletions(-)
```
