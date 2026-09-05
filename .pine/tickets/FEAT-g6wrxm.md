---
id: FEAT-g6wrxm
title: Add the database node options collection and query batching
status: todo
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-n5fdz3
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T08:28:30Z"
---

## Scope

`databaseNode()` at `nodes/database.go:38` declares seven properties — `operation`, `statement`, `executeStatement`, `statements`, `parameters`, `timeoutSeconds` and `maxRows` — and no options collection at all. n8n's Postgres node puts nearly everything else behind a single `options` collection, so every one of those settings is absent here rather than named and refused: nothing carries `queryBatching`, `queryReplacement`, `largeNumbersOutput`, `replaceEmptyStrings`, `outputColumns`, `skipOnConflict`, `connectionTimeout` or `delayClosingIdleConnection`. The roadmap counts ten; the count is not checkable from inside this repository, and the collection must be enumerated from the reference checkout.

There is no batching either, and one mode already exists by accident. `DatabaseExecutor.Execute` at `nodes/database.go:203-213` loops over the input items, resolves expressions per item and calls `runOne`, returning on the first error at line 210, against a pool pinned to a single connection by `db.SetMaxOpenConns(1)` at `internal/sqlnode/sqlnode.go:100`. That is n8n's `transaction` mode without the transaction and without the rollback: it stops on the first failure and leaves everything before it committed. `single` and `independently` have no expression in the code at all.

Two of the options change the payload of every row, which is why they need item-level tests rather than a parameter-read test. `normalize` at `internal/sqlnode/sqlnode.go:389` already turns every `[]byte` a driver returns into a Go string and every `time.Time` into `RFC3339Nano`, unconditionally, before any option could be consulted. `largeNumbersOutput` and `replaceEmptyStrings` both have to be reconciled against that behaviour rather than layered on top of it.

`queryReplacement` sits in this collection but its translation belongs to V2-p4-12, which turns it into the bound `parameters` array. This ticket owns the option's declaration, its visibility and its defaults; that ticket owns what the importer does with a value found in it.

This matters because the options collection is where an n8n user's intent actually lives. A workflow imported with six correct operations and no `queryBatching` runs its inserts one at a time and aborts halfway through a partial write — behaviour the author explicitly chose against in n8n, silently reversed on arrival here.

## Acceptance criteria

- [ ] The Postgres node carries an `options` collection whose key set and defaults match n8n's exactly, asserted by a table test against the reference checkout rather than transcribed by hand.
- [ ] `queryBatching: single` produces one combined result for the whole input batch, proven by a test that counts the statements the connection executed.
- [ ] `queryBatching: independently` runs each item separately, continues past a failing item and attributes the failure to that item, proven by a test asserting both the successful items and the reported error.
- [ ] `queryBatching: transaction` rolls back every item's work when one item fails and stops at that item, proven by a test that reads the table back after the failure.
- [ ] `largeNumbersOutput` visibly changes the item payload for a bigint and a numeric column on both settings, proven by an item-level assertion rather than by reading the option back.
- [ ] `replaceEmptyStrings` writes a JSON null where an empty string was and leaves a non-empty string untouched, proven by an item-level test on both settings.
- [ ] Every key the collection declares is either applied by the executor or refused with a named diagnostic, proven by a test that enumerates the declared keys and fails on one nothing reads.
- [ ] The Postgres-specific runs are executed by hand through `make smoke-postgres` and recorded in the ticket's work evidence, because this repository has no CI to run them.

## Implementation Plan

Declare the collection first, before any batching behaviour. The option names are a document contract: the executor reads them, V2-p4-12's exporter writes them, and a stored workflow carries them. Getting a name or a default wrong after workflows exist is a document migration, whereas getting the behaviour wrong is a bug fix. Settle the names, then implement against them.

Derive the list mechanically. Recommend reading the option descriptions out of the reference checkout and writing a test that asserts the declared key set against them, so the collection cannot silently drift from n8n. Reject transcribing the list from the roadmap or from this ticket: the reference checkout is sparse and today holds only `HttpRequest`, `If`, `Schedule` and `Set`, so the Postgres node's descriptions are not yet on disk, and a hand-typed list would be an unverifiable claim in the one place that has to be exact.

Batching belongs in the executor, not in `sqlnode.Connection`. The per-item loop at `nodes/database.go:203-213` is where expressions are resolved — `expression.Resolve` runs once per item at line 204 — so the mode has to choose how many resolved statements become how many calls. `Connection` stays a thin, dialect-neutral pair of `Query`/`Execute` plus the existing `Transaction`, which already commits on success and rolls back on the first failure at `internal/sqlnode/sqlnode.go:352-380` and is very nearly the `transaction` mode as written.

The trap is `normalize`. It already converts every `[]byte` the driver hands back into a Go string, so a Postgres `numeric` or `bigint` that arrives as driver bytes is *already* text in the item before `largeNumbersOutput` is consulted. An implementation that reads the option, branches, and formats numbers as text on one path will pass a test that only checks the option was honoured — while the other path silently returns text too, because it always did. Assert the item's Go type, not its rendered value.

Say explicitly which of `independently` and the shared `continueOnFail` setting wins. `sharedSettings()` at `nodes/core.go:128` already declares `continueOnFail`, and it is a node-level answer, while `independently` is an item-level one. The two are not the same question and must not be collapsed; whichever ordering is chosen, write it into the option's description so the panel says it rather than leaving it to be discovered.

**Concatenation versus bound statements for `single`.** n8n's `single` mode joins the per-item queries into one string and sends it once. Recommend reproducing the observable outcome — one combined result, one item stream — while keeping each statement separately bound on the pinned connection, and documenting the divergence in the option's description. Reject literal concatenation: pgx's extended protocol refuses multiple statements per query, and the escape hatch is the simple protocol, which moves parameter binding client-side and undoes exactly what the package comment at `internal/sqlnode/sqlnode.go:1-7` and `Query`'s contract at line 263 exist to guarantee; on MySQL the same trick needs `multiStatements=true`, which `mysqlDSN` at line 150 does not set and which would make every workflow statement a multi-statement injection surface. What would reopen it is a driver-level batch API reached outside `database/sql` — pgx's `SendBatch` — which V2-p4-6's bulk-write work may introduce, at which point `single` can become one real round trip with binding intact.

## References

- Roadmap plan, p4 section, entry V2-p4-11: `.pine/roadmap.md`.
- `nodes/database.go` — the seven declared properties at lines 47 to 77, and `Execute`'s per-item loop at 203 to 213 with its first-error return at 210.
- `internal/sqlnode/sqlnode.go` — the package comment's binding contract, `Query` at line 263, `Transaction` at 352, `normalize` at 389, the pinned pool at 100 and `mysqlDSN` at 150.
- `nodes/core.go` — `sharedSettings` at line 128, which already declares `continueOnFail` and a second `timeoutSeconds`.
- `nodes/database_test.go` — the SQLite harness the batching tests can reuse without a server.
- `Makefile` — the `smoke-postgres` target, run by hand and recorded per ticket.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `packages/nodes-base/nodes/Postgres/` for the options collection, its exact names and its defaults. The checkout is sparse and does not yet contain it; V2-p0-1's widening list omits the database nodes.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 36-37 — the Options collection is an add-one-at-a-time list rather than a flat panel, which is why option names and defaults come from source rather than from the capture. Captured from a live local n8n 2.x instance; gitignored, never vendored.
