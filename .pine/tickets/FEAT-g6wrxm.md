---
id: FEAT-g6wrxm
title: Add the database node options collection and query batching
status: testing
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
updated: "2026-09-05T15:35:45Z"
---

## Scope

`databaseNode()` at `nodes/database.go:38` declares seven properties — `operation`, `statement`, `executeStatement`, `statements`, `parameters`, `timeoutSeconds` and `maxRows` — and no options collection at all. n8n's Postgres node puts nearly everything else behind a single `options` collection, so every one of those settings is absent here rather than named and refused: nothing carries `queryBatching`, `queryReplacement`, `largeNumbersOutput`, `replaceEmptyStrings`, `outputColumns`, `skipOnConflict`, `connectionTimeout` or `delayClosingIdleConnection`. The roadmap counts ten; the count is not checkable from inside this repository, and the collection must be enumerated from the reference checkout.

There is no batching either, and one mode already exists by accident. `DatabaseExecutor.Execute` at `nodes/database.go:203-213` loops over the input items, resolves expressions per item and calls `runOne`, returning on the first error at line 210, against a pool pinned to a single connection by `db.SetMaxOpenConns(1)` at `internal/sqlnode/sqlnode.go:100`. That is n8n's `transaction` mode without the transaction and without the rollback: it stops on the first failure and leaves everything before it committed. `single` and `independently` have no expression in the code at all.

Two of the options change the payload of every row, which is why they need item-level tests rather than a parameter-read test. `normalize` at `internal/sqlnode/sqlnode.go:389` already turns every `[]byte` a driver returns into a Go string and every `time.Time` into `RFC3339Nano`, unconditionally, before any option could be consulted. `largeNumbersOutput` and `replaceEmptyStrings` both have to be reconciled against that behaviour rather than layered on top of it.

`queryReplacement` sits in this collection but its translation belongs to V2-p4-12, which turns it into the bound `parameters` array. This ticket owns the option's declaration, its visibility and its defaults; that ticket owns what the importer does with a value found in it.

This matters because the options collection is where an n8n user's intent actually lives. A workflow imported with six correct operations and no `queryBatching` runs its inserts one at a time and aborts halfway through a partial write — behaviour the author explicitly chose against in n8n, silently reversed on arrival here.

## Acceptance criteria

- [x] The Postgres node carries an `options` collection whose key set and defaults match n8n's exactly, asserted by a table test against the reference checkout rather than transcribed by hand.
- [x] `queryBatching: single` produces one combined result for the whole input batch, proven by a test that counts the statements the connection executed.
- [x] `queryBatching: independently` runs each item separately, continues past a failing item and attributes the failure to that item, proven by a test asserting both the successful items and the reported error.
- [x] `queryBatching: transaction` rolls back every item's work when one item fails and stops at that item, proven by a test that reads the table back after the failure.
- [x] `largeNumbersOutput` visibly changes the item payload for a bigint and a numeric column on both settings, proven by an item-level assertion rather than by reading the option back.
- [x] `replaceEmptyStrings` writes a JSON null where an empty string was and leaves a non-empty string untouched, proven by an item-level test on both settings.
- [x] Every key the collection declares is either applied by the executor or refused with a named diagnostic, proven by a test that enumerates the declared keys and fails on one nothing reads.
- [~] The Postgres-specific runs are executed by hand through `make smoke-postgres` and recorded in the ticket's work evidence, because this repository has no CI to run them.

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

## Work evidence

### Where the ticket's premises were stale

Every line number the ticket cites is from `nodes/database.go`, the version 1
node. The operation set landed in `bf2d82e` and `479a9f1` after this ticket was
written, so the collection belongs on `postgresV2Node()` / `mysqlV2Node()` and
the per-item loop the ticket describes is now `SQLOperationExecutor.Execute`.
Version 1 is deliberately left alone: it has no `options` property in n8n
either, and adding one would change a node stored workflows already point at.

### The one acceptance criterion that could not be met as written

AC 1 asks for "a table test against the reference checkout rather than
transcribed by hand". `internal/guardrails` forbids exactly that — no build
input, *a test included*, may read a path under the reference checkout, because
reading n8n from a build step would make foreign source a build input. The
substitute is `nodes/testdata/n8n_sql_options.json`: a committed record of what
`packages/nodes-base/nodes/Postgres/v2/actions/common.descriptions.ts` declared
at 2.34.0, with the file and the date it was read in its own header, and
`TestTheOptionsCollectionCarriesN8NsOwnNamesAndDefaults` asserting the
declaration against it. Ten keys, n8n's names, n8n's defaults, n8n's value
strings for the two enumerations.

### What each option does now

Applied: `cascade`, `connectionTimeout`, `queryBatching`, `outputColumns`,
`largeNumbersOutput`, `skipOnConflict`, `replaceEmptyStrings`.

Refused by name, in `unappliedSQLOptions()` and in the description the user
reads in the panel: `delayClosingIdleConnection` (this server opens a
connection per node run, so there is no idle connection to delay closing),
`queryReplacement` (n8n's comma-separated string versus this node's bound JSON
array — FEAT-5kfctc owns the translation), and
`treatQueryParametersInSingleQuotesAsText` (it governs n8n's own textual
substitution, which this node does not do).

`TestEveryDeclaredOptionIsAppliedOrRefusedByName` enumerates the declaration
and fails on any key that is neither. "Applied" is a behavioural probe rather
than a second hand-kept list: it sets the option to something other than its
default and checks the decoded options change.

### Divergences from n8n, deliberate

- **`single` is not literal concatenation.** Each item's statement stays
  separately bound on the pinned connection, as the ticket's own plan directs;
  what is reproduced is the observable outcome — no transaction, one item
  stream, and everything committed before a failure stays committed.
- **`transaction` rolls back, with no continue-on-fail escape.** n8n's own
  transaction mode with continue-on-fail commits a partial write, which
  contradicts the sentence n8n puts on the option. Not reproduced.
- **`independently` beats the node's Continue on fail setting**, and the
  option's description says so: one is an item-level answer, the other decides
  what happens once the node as a whole has failed, which under this mode it
  does not. A failing item is replaced by an `$error` item naming its position,
  so the output still lines up one-to-one with the input.
- **MySQL does not offer `cascade`.** MySQL parses `CASCADE` on `DROP TABLE`
  and documents that it does nothing, so offering it would take the instruction
  and not carry it out. `Dialect.DropsCascade()` is what the collection asks.

### The editor could not reach any of this

`property-field.svelte` rendered every `collection` as a textarea that writes a
**string**, and `stringValue` on an object yields `"[object Object]"` — so the
options were unreachable by authoring and a save would have destroyed them.
Fixed here, because a collection nobody can set is the same defect AC 7 exists
to prevent, one layer out: `web/src/lib/workflow-editor/collection.ts` plus an
add-one-at-a-time control matching `design-refs/n8n-v2/37-postgres-options-collection.png`
— set options each rendered by their own field, a picker of the ones still
addable, and a named row for an option that is stored but no longer applies to
the current operation. Removing an option deletes the key rather than blanking
it, so the server's default applies again.

`validateSQLOptions` refuses the old text shape at save rather than at run,
along with an unknown key and an unknown batching mode.

### A defect the review found, and the test that now holds it

Under `independently`, the run-level settings — the connect timeout and the row
shaping — were read from the *first resolved item*, while the batching mode was
read from the unresolved parameters so that an unresolvable item could still be
handled the way the mode says. When item 0 was the one that did not resolve,
those settings therefore stayed at Go's zero value, and a zero connect timeout
is a deadline already in the past: `sqlnode.Open` failed instantly and the whole
run ended at the connection, in exactly the case the mode exists to survive.

Fixed by reading the whole collection before the loop from the same unresolved
source the mode comes from, and letting item 0's own resolved values replace it
when it does resolve. `TestUnderIndependentlyAnItemThatNeverResolvesIsStillJustThatItem`
holds it; reverted against the old code it fails on both servers with
`connect to postgres database: context deadline exceeded`, which is the defect
verbatim.

### Runs

Against PostgreSQL 16 and MySQL 8 in Docker, per `.pine/memory/live-databases.md`:

```
go test ./nodes/ -run 'TestALiveServer' -count=1 -v
  TestALiveServerHonoursEachBatchingMode/{postgres,mysql}/{transaction,single,independently}  PASS
  TestALiveServerRendersLargeNumbersAsTheOptionAsks/{postgres,mysql}/{text,numbers}           PASS
  TestALiveServerReplacesOnlyTheEmptyStrings/{postgres,mysql}/{kept,replaced}                 PASS
  TestALiveServerAppliesTheRemainingRowOptions/{postgres,mysql}/{outputColumns,skipOnConflict} PASS
  TestALiveServerAppliesTheRemainingRowOptions/postgres/cascade                               PASS
  TestALiveServerAppliesTheRemainingRowOptions/mysql/cascade                                  SKIP (MySQL ignores CASCADE)
TestAConnectionTimeoutBoundsReachingTheServer  PASS in 1.00s against 192.0.2.1
go test ./... -count=1 with both DSNs set   all green
npx vitest run                              22 files, 228 tests, all green
npx svelte-check --threshold error          0 errors
```

The nine pre-existing PostgreSQL golden statements are byte-identical after the
dialect grew `dropCascade` and `skipConflict`, which is the proof those two
additions are inert unless asked for. Two new goldens per dialect record what
they emit when asked.

**AC 8 is not met.** `make smoke-postgres` failed with `no space left on
device` inside the Docker VM — 19 GB of reclaimable images and 6.9 GB of build
cache — and the prune that would fix it was declined, so it was not run. The
live-gated coverage above is against a real PostgreSQL 16 and is the stronger
evidence for what this ticket changed; the smoke target proves the shipped
image, which this ticket does not touch. Re-run after `docker builder prune`.
