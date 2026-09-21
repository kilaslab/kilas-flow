---
title: Datastore concurrency
description: What a data table guarantees when ten workers write the same row at once, which operations are single statements, and where the remaining races are.
sidebar:
  order: 10
---

The worker pool runs `execution.max_concurrent` executions at once, and the
default is ten. Every one of those workers can be running a Data table node
against the same physical table, the same row, at the same instant. This page
states what that costs: which operations are one statement and therefore
atomic per row on both drivers, which are not, and where the remaining races
are so a workflow author can avoid them rather than discover them during a
reconciliation.

The short version: writes that can be composed as one statement are atomic;
compositions that need a read first are not, and the surface offers an atomic
primitive for each of them.

## What is atomic

Each of these is one SQL statement on both drivers. Two writers cannot
interleave inside a row: one statement's effect is applied whole, the other's
after it, and the last writer wins.

- **Insert.** `INSERT ... RETURNING` hands back the row the statement wrote,
  with the id and both timestamps set by the database. The engine reads the
  row back through `RETURNING` rather than by re-selecting, so a concurrent
  writer's image can never come back in place of this insert's own.
- **A filtered update or delete.** `UPDATE ... WHERE ...` and
  `DELETE ... WHERE ...` are atomic per row on both engines. No lock is taken
  and none is needed for the write itself.
- **Clear.** One `DELETE` over the table. The identity sequence is not reset,
  so a row inserted after a clear never reuses an id the table held before.
- **Increment.** `UPDATE ... SET col = COALESCE(col, 0) + ?, updatedAt = ...
  WHERE ... RETURNING ...` — one statement, and the returned rows are that
  statement's own post-images, not a re-read. A NULL cell counts as zero, the
  amount may be negative, and only a number column can be incremented. Use
  this for counters and flags instead of reading a value, changing it and
  writing it back: the read-modify-write loses writes by construction as soon
  as two workers meet, and no amount of retrying makes it a counter.
- **An upsert addressed by id.** A filter that is exactly one `id eq N`
  condition, with `1 <= N <= 9007199254740991`, compiles to a single
  `INSERT ... ON CONFLICT (id) DO UPDATE ... RETURNING` on both drivers. It
  never inserts the same id twice; a missing id is created at exactly that id.
  Only the columns the caller supplied are written on conflict, `createdAt` is
  left alone, and `updatedAt` strictly increases.

## What is not atomic

- **An upsert addressed by anything else.** Physical datastore tables carry no
  unique constraint on their user columns — a data table is a plain
  n8n-shaped table — so there is nothing to conflict on, and inventing a
  unique index on user data would change storage semantics to satisfy an
  implementation. A filter matched on any other column therefore stays a read
  followed by a write, inside no transaction: two concurrent upserts against
  the same filter may both insert. Address the row by id when the upsert must
  be safe.
- **The rows a filtered update or delete returns.** The *write* is one
  statement, but the rows that come back are read around it, so under
  contention they can show a neighbour's write rather than this statement's
  own image. Increment returns the statement's own image; a filtered update
  does not.
- **Row-count limits.** `MaxRowsPerDatastore` and the node's per-input-row
  limit are advisory under races: the count is read before the write, so two
  writers can each see room for one more row. The id-addressed upsert is the
  exception — its limit check happens inside the statement.
- **Any read-then-write a workflow composes by hand.** Get a row into an item,
  change a field, update the row: nothing in the engine makes that sequence
  atomic, and two runs doing it to one row interleave. This is the shape the
  atomic primitives above exist to replace.

## No row locks

Nothing in the datastore takes a row lock, on either driver, and
`clause.Locking` is barred from every datastore write path. The reason is that
the clause is not portable in the way it reads. On PostgreSQL it emits
`SELECT ... FOR UPDATE`; the SQLite dialector silently discards it, so the
same Go source promises a lock on one driver and gets nothing on the other.
The repository layer already reads that way in six places
(`internal/repository/executions.go`, `workflow_history.go`, `workflows.go`,
`schedules.go`); on SQLite they are not weaker locks, they are absent ones.

Correctness is put in single-statement writes and in the compare-and-swap
below instead, so no Datastore write path can be written believing in a lock
it never receives. A test pins the absence from both sides: it asserts that
the SQLite dialector drops `clause.Locking` and that a PostgreSQL dialector
does emit it, so the assertion cannot pass vacuously.

## Optimistic preconditions

`Update rows` and `Delete rows` accept an optional `ifUpdatedAt`: a row's
`updatedAt` exactly as a previous read returned it. When it is present, the
filter must match exactly one row and the write lands only if that row is
still unchanged. A stale stamp answers **409** with a problem+json body whose
`errors[0].location` is `body.ifUpdatedAt` and whose `errors[0].value` is the
row's *current* `updatedAt` — so a caller retries against the new stamp
without a second read, and the row is left exactly as it was.

The precondition is off by default. Making it mandatory would turn an ordinary
"set a flag on this row" into a read followed by a write, which is the race
this whole page is about.

## `updatedAt` is strictly increasing

Every write sets `updatedAt` to the greater of the wall clock and one
millisecond past the row's current stamp, per dialector, so two writes to one
row can never share a stamp. This is what makes the precondition mean
something: a stamp that can repeat cannot tell two writes in the same
millisecond apart, and a compare-and-swap loop built on one would lose
updates. SQLite stores milliseconds and PostgreSQL stores `TIMESTAMPTZ(3)`.

The caveat is the other side of that choice. Under a sustained burst above
roughly a thousand writes per second to a *single* row, `updatedAt` runs ahead
of the wall clock by the length of the burst. The column is an ordering
device, not a clock: do not read it as the time a row was written.

## Explicit ids are bounded

An id-addressed upsert may create a row at a chosen id. The bound is
`1..9007199254740991` (2^53-1, the largest integer a JSON number holds
exactly), and an id outside it is refused before any SQL runs. The reason is
not arithmetic tidiness: an id at `MaxInt64` exhausts the identity counter,
after which every later plain insert fails permanently — SQLite answers
`database or disk is full (13)` and PostgreSQL `nextval: reached maximum value
of sequence` — and a workflow key-value node or an embed session with
`datastore:write` could do that to a datastore.

On PostgreSQL the bigserial is re-synced past an explicit id inside the same
statement, with the maximum computed inside the `setval` call so a racing
plain insert cannot move the sequence backwards. One residual remains and is
inherent to addressing rows by number: a plain insert racing an id-addressed
upsert can still meet that number first, and then the insert is refused on the
duplicate key or the upsert updates the row the insert just made.

## The SQLite pool, and what widening it does

`database.Open` pins SQLite to one connection (`SetMaxOpenConns(1)`), which
serialises every statement in the process and hides interleaving by accident
rather than by design. The datastore does not depend on that accident: its
concurrency suite runs at pool widths 1, 4 and 10 as well as against
PostgreSQL, and every case passes at every width. Widening the pool is a
test-only opt-in (`KILASFLOW_TEST_SQLITE_POOL`), and production pinning is
untouched.

What widening *does* break is the executions repository: with ten workers
claiming from a multi-connection SQLite pool, `ClaimNext`'s read-then-write
transaction fails with `database is locked` (SQLITE_BUSY, and 517 for
`SQLITE_BUSY_SNAPSHOT`) and traces fail to persist. That is queue and
worker-mode work, not a datastore fault, and it is why the SQLite pin stays
until that work lands.

## How this is verified

The row store's guarantees are pinned by tests in `internal/datastore`
(`TestConcurrentIncrementsLoseNoWrites`,
`TestConcurrentIncrementsReturnEachWritersOwnValue`,
`TestUpsertByIDIsOneStatement`,
`TestUpsertOnAnIDFilterTakesTheSingleStatementPath`,
`TestUpsertByIDStatementShape`, `TestConcurrentUpsertByIDInsertsOnce`,
`TestFilterAddressedUpsertStaysReadThenWrite`, `TestUpsertByIDRefusesUnsafeIDs`,
`TestDatastoreWritesTakeNoRowLocks`,
`TestStalePreconditionAfterAnInterleavedWriteIsRefused`,
`TestPreconditionedWritesLoseNoUpdateUnderContention`), the 409 contract by
`TestStaleIfUpdatedAtIsRefusedWithTheCurrentStamp` in `internal/api`, and the
end-to-end claim by `TestTenConcurrentExecutionsIncrementOneDatastoreRow` and
`TestHundredExecutionsIncrementOneDatastoreRowSoak` in `internal/engine`,
which queue real executions and let the shipped worker pool run them at
`execution.max_concurrent`.

Both dialects run in the same test binary: SQLite always, PostgreSQL when
`KILASFLOW_TEST_POSTGRES_DSN` names a live server. `make smoke-postgres`
carries the datastore package and the two execution-level tests into the
Compose stack, so the PostgreSQL half is exercised against a server the
quickstart itself starts.

See the [Datastore API reference](/reference/api/datastores/) for the
request and response shapes, and the
[execution model](/concepts/execution-model/) for what a worker is.
