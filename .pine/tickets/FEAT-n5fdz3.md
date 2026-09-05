---
id: FEAT-n5fdz3
title: Bring the PostgreSQL node to n8n's operation set
status: todo
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
updated: "2026-09-05T08:28:30Z"
---

## Scope

`databaseNode` in `nodes/database.go:38` is one factory serving `kilasflow.postgres`, `kilasflow.mysql` and `kilasflow.sqlite` (lines 84-94), every one at `workflow.V(1)`, offering the three operations declared at lines 32-36: `sqlOperationQuery = "query"`, `sqlOperationExecute = "execute"`, `sqlOperationTransaction = "transaction"`. n8n's Postgres node offers six with entirely different value strings — `deleteTable`, `executeQuery`, `insert`, `upsert`, `select`, `update` — on a node whose `defaultVersion` is 2.7. The value strings are the contract, and none of KilasFlow's three appears in n8n's set.

The importer already records the mismatch as a defect. `sqlToKilas` (`internal/interop/n8n/parameters.go:541`) branches at line 544 and returns `map[string]any{"operation": "query"}` for every operation but `executeQuery`, with no `statement` key at all, so an imported `insert` arrives as an empty query. The mappings table pins `n8n-nodes-base.postgres` and `n8n-nodes-base.mySql` at `kilasVersion: 1` with `exportTypeVersion: 2.4` (`internal/interop/n8n/n8n.go:177-184`), and `internal/interop/n8n/corpus/BASELINE.md` records 16 instances of `n8n-nodes-base.postgres` across 39 fixtures — the eighth most common node type in the corpus.

The version policy is the load-bearing half: v1 keeps query, execute and transaction, v2 is the n8n operation set, and the interop `kilasVersion` moves with it. Half the mechanism exists — `workflow.TypeVersion` is fixed-point and the registry is keyed on it, so V2-p1-11 has landed as `FEAT-k3fmj1` and the roadmap's description of `node.Definition.Version` as an `int` is stale. `@version` gating has not: V2-p2-3 is `FEAT-pd3p6x`, still `todo`, and without it one definition cannot present two parameter shapes.

An operation set also opens an injection boundary this codebase does not yet have. No identifier-quoting helper exists anywhere in the tree, because every statement is bound rather than built — `Connection.Query` hands parameters to `QueryContext` untouched (`internal/sqlnode/sqlnode.go:280`). Builders change that: schema, table and column names become identifiers interpolated into statement text, and identifiers cannot be bound.

This is where the epic's n8n-first claim becomes measurable rather than argued. The database family is the one node family carrying a cited, reproducible import defect, and its parity is settled by string equality on six values — an imported workflow's `operation` either lands on a shape that runs, or it does not.

## Acceptance criteria

- [ ] `kilasflow.postgres` v2 registers the six n8n operation values verbatim — `deleteTable`, `executeQuery`, `insert`, `upsert`, `select`, `update` — proven by a registry test asserting the exact strings rather than their labels.
- [ ] v1 stays registered and unaltered, and a document pinned at version 1 still resolves to the query/execute/transaction shape, proven by a `Registry.Resolve` test covering both versions.
- [ ] A stored document carrying an imported `typeVersion` of 2.4 or 2.7 resolves to v2 rather than v1, and its migration outcome is asserted rather than left to discovery, proven by a test over both parameter shapes.
- [ ] Each of the five non-`executeQuery` operations emits SQL matching n8n's byte for byte against a fixture table, captured as golden files rather than paraphrased in assertions.
- [ ] Identifier escaping rejects or correctly quotes every input a fuzz target produces, with no failing seed after a recorded run of `go test -fuzz` and the found corpus committed.
- [ ] A quoted identifier round-trips against a live PostgreSQL — created, then read back from `information_schema` and compared byte for byte — proven by an integration test gated on `KILASFLOW_TEST_POSTGRES_DSN`.
- [ ] `sqlToKilas` maps every one of the six operations onto its v2 equivalent with its parameters, so no operation is imported as an empty query, proven by fixtures and reflected in a regenerated `BASELINE.md`.
- [ ] No parameter key v2 declares collides with a key in `sharedSettings()`, proven by a test checking the two groups together rather than separately as `validateProperties` does today.

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
