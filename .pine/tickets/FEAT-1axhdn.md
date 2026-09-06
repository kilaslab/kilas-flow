---
id: FEAT-1axhdn
title: Settle Datastore concurrency semantics
status: doing
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-nrfg6e
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-06T04:48:34Z"
---

## Scope

`Service.Start` at `internal/engine/service.go:246-257` spawns one goroutine per worker — `go service.worker(ctx, workerID)` at line 253, looped `maxConcurrent` times — and `cmd/kilasflow/main.go:154` feeds it `cfg.Execution.MaxConcurrent`, which `config.Default()` sets to `10` at `internal/config/config.go:164`. Ten executions therefore run at once out of the box. (The roadmap places this fan-out in `internal/engine/runner.go`; it is not there. `runner.go` spawns no goroutine at all and walks the compiled IR sequentially — the concurrency is entirely in `service.go`.) Every one of those ten workers can be running a Datastore node against the same physical table, the same row, at the same instant.

The shape of the traffic makes this worse than a theoretical race. The most popular real use of a data table is a cross-run key-value store — read a counter or a flag, change it, write it back — which is a read-modify-write, and a read-modify-write across ten workers loses writes by construction. n8n documents nothing about any of this: no statement about whether its upsert is atomic, no statement about locking on filtered updates, no precondition mechanism offered anywhere in the node. Whatever KilasFlow decides here is a decision n8n has not made in public, which is why this is a place to beat n8n rather than match it.

The repository layer already has an opinion, and on one driver it is a lie. `clause.Locking{Strength: "UPDATE"}` appears at `internal/repository/executions.go:96`, `internal/repository/workflows.go:93` and `:225`, and `internal/repository/schedules.go:149`. On PostgreSQL that emits `SELECT … FOR UPDATE`. On SQLite the dialector deletes it: `github.com/glebarez/sqlite@v1.11.0/sqlite.go:115-121` registers a `"FOR"` clause builder that returns without writing anything when the expression is a `clause.Locking`, above the comment `// SQLite3 does not support row-level locking.` The lock is not weaker on SQLite — it is absent, and the Go source reads identically either way.

What rescues the existing call sites is an accident of configuration rather than a property of the code. `internal/database/database.go:57-59` pins the SQLite pool to `SetMaxOpenConns(1)`, so the whole process is serialised and no two statements interleave. That accident does not extend to PostgreSQL, where the pool is `cfg.MaxOpenConns` wide, and it will not survive V2-p8-7's queue and worker mode, which puts a second process on the same database.

This matters because Datastore is the feature a host application's own users touch most directly. An embedded, white-label platform whose datastore silently loses one write in ten under its own shipped default is worse than a platform that has not shipped a datastore, because the failure is invisible to the customer until reconciliation.

## Acceptance criteria

- [ ] Upsert compiles to one statement on both drivers, proven by a test asserting the emitted SQL carries `ON CONFLICT` and contains no preceding `SELECT`.
- [ ] Ten concurrent workflow executions each incrementing one datastore row leave the value at exactly ten on both drivers, proven by a test run at `execution.max_concurrent` of 10.
- [ ] A test asserts that `clause.Locking` emits no SQL through the SQLite dialector, so no Datastore write path can be written believing in a row lock it never receives.
- [ ] An optimistic precondition on `updatedAt` refuses a stale write with a distinct conflict error rather than overwriting, proven by a test that interleaves two updates against one row.
- [ ] A refused precondition returns the row's current `updatedAt` in the error body, so a caller can retry without a second read, proven by a handler test.
- [ ] The concurrency behaviour of upsert, filtered update and delete is stated on the Datastore node description and in the API operation descriptions, captured as evidence on this ticket.
- [ ] The concurrent-increment suite passes against PostgreSQL through `make smoke-postgres`, run by hand with its output recorded on this ticket, because the repository has no CI configuration of any kind.
- [ ] Widening the SQLite pool beyond one connection does not change the outcome of any concurrency test, proven by running the suite twice with the pool size varied.

## Implementation Plan

Write the failing test before choosing any semantics. Ten goroutines, one row, one counter, driven through the row store the way the node will drive it. It goes first because every decision below is only checkable against it and because of a subtlety that will otherwise waste the whole ticket: on SQLite the naive read-modify-write passes this test today, purely because `SetMaxOpenConns(1)` serialises the process. The harness must be able to run with the pool widened and against PostgreSQL, or it proves nothing about the code under test.

**Upsert atomicity.** Make it one statement. Both dialectors already register what is needed — `github.com/glebarez/sqlite@v1.11.0/sqlite.go:59-61` lists `"ON CONFLICT"` and `"RETURNING"` in `CreateClauses`, `UpdateClauses` and `DeleteClauses`, and the bundled SQLite is 3.41.2, well past the 3.24 and 3.35 releases that introduced them; `gorm.io/driver/postgres` has both. Reject check-then-write outright. It is the shape n8n's operation surface invites, it is a lost update the moment two workers meet, and the retry loop it would need has no reason to exist when `gorm.Config{TranslateError: true}` at `internal/database/database.go:44` already maps the unique violation into a typed error.

Filtered updates take no locks and promise none: a single `UPDATE … WHERE` is atomic per row on both engines, and the semantics are then identical everywhere. Reject a `SELECT … FOR UPDATE` pre-pass. On PostgreSQL it doubles the statement count and holds locks for as long as the node runs; on SQLite it compiles to nothing, so identical Go source would give two different guarantees depending on a configuration value the node author cannot see.

Offer optimistic locking, and reuse the pattern the codebase already trusts rather than inventing one. `GORMExecutionStore.ClaimNext` does a conditional `Updates` at `internal/repository/executions.go:391-401` and reads `result.RowsAffected == 0` as "another worker won" at line 402. The precondition is the same shape with `updated_at = ?` added to the `WHERE`. Reject a separate version integer column: the physical table already carries `updatedAt` as a system column to match n8n, and a second column is one more thing the catalogue, the CSV path and the n8n import mapping must each learn about.

The trap is the row lock that is not there. Anyone reading `clause.Locking{Strength: "UPDATE"}` in review sees a lock; on SQLite the dialector silently discards it; the test passes because the single connection serialised everything anyway. The bug surfaces only when PostgreSQL is selected or a second process joins, at which point the code that "already had locking" races and nothing in the diff explains why. Bar `clause.Locking` from every Datastore write path as a rule, put correctness in single-statement writes and the `RowsAffected` precondition, and keep the dialector test above so the absence is a recorded fact rather than something rediscovered under load.

**Precondition surface.** Recommend an optional `updatedAt` precondition on Update and Delete, defaulting off. Mandatory would turn the ordinary "set a flag on this row" workflow into a two-step read-then-write, which is precisely the race this ticket exists to remove. What reopens it is evidence that hosts run counters through the node often enough that silent last-write-wins is the common case rather than the corner; the answer then is a dedicated atomic increment operation on the node, not a mandatory precondition on every write.

## References

- Roadmap plan, p9 section, entry V2-p9-6: `.pine/roadmap.md`.
- `internal/engine/service.go:246-257` — `Start` spawning one goroutine per worker at line 253; this, not `internal/engine/runner.go`, is the source of the concurrency.
- `internal/config/config.go:164` and `cmd/kilasflow/main.go:154` — `MaxConcurrent: 10` and the call that applies it at boot.
- `github.com/glebarez/sqlite@v1.11.0/sqlite.go:115-121` — the `"FOR"` clause builder that discards `clause.Locking` on SQLite without an error.
- `github.com/glebarez/sqlite@v1.11.0/sqlite.go:59-61` — `"ON CONFLICT"` and `"RETURNING"` registered across create, update and delete.
- `internal/repository/executions.go:391-402` — `ClaimNext`'s conditional `Updates` plus `RowsAffected == 0`, the compare-and-swap pattern to reuse.
- `internal/repository/executions.go:96`, `internal/repository/workflows.go:93,225`, `internal/repository/schedules.go:149` — the existing `clause.Locking` call sites that receive no lock on SQLite.
- `internal/database/database.go:44,57-59` — `TranslateError: true`, and the `SetMaxOpenConns(1)` that hides the missing lock today.
- `Makefile` — `smoke-postgres`, run by hand and recorded on this ticket.

## Evidence — 2026-09-06 (DatastorePolicy slice)

- Semantics settled as documented in `internal/datastore/concurrency.go`
  (new): filtered Update/Delete/Clear/Increment are one statement each
  (last-writer-wins per row, identical on both drivers); NO row locks
  anywhere (`clause.Locking` is discarded silently by the SQLite dialector,
  so it is barred from every datastore write path); Upsert stays
  read-then-write with its race documented (counters/flags use Increment or
  preconditions, never Get+Update); limit checks are advisory under races.
  Deviation from the ticket: no single-statement `ON CONFLICT` upsert —
  physical tables carry no unique constraint on user columns, so there is
  nothing to conflict on; the honest primitive is CAS instead.
- New primitives: `Increment` (atomic `SET col=COALESCE(col,0)+?`,
  NULL starts at delta, number columns only) and
  `UpdateWithPrecondition`/`DeleteWithPrecondition` (single-row CAS on
  `updatedAt`, `PreconditionError` carries the current stamp for retry
  without a second read, `errors.Is(err, ErrPreconditionConflict)`).
- Two real bugs found by the tests and fixed in the slice:
  (1) binding a Go `time.Time` against SQLite's second-precision
  `CURRENT_TIMESTAMP` text never matches — predicates now compare at
  storage resolution (`strftime %f` / `date_trunc('milliseconds')`);
  (2) second precision makes CAS useless under contention, so `stampNow`
  on SQLite moved to millisecond `STRFTIME` (existing tests assert only
  non-zero stamps — verified).
- Tests (`internal/datastore/concurrency_test.go`): 10x10 concurrent
  increments land exactly 100; increment emits exactly one UPDATE with no
  preceding SELECT; stale precondition refused with current stamp attached
  and row untouched; multi-row precondition refused; delete-underneath
  returns empty not conflict; emitted SQL contains no `FOR UPDATE` AND the
  SQLite dialector demonstrably drops `clause.Locking` (DryRun pin).
- NOT done in this slice: `make smoke-postgres` concurrent-increment run
  (no PG here; the `date_trunc` predicate and `DOUBLE PRECISION` increment
  are code-reviewed but unverified live); widened-SQLite-pool run (pool is
  pinned to 1 by database.Open with no override knob — a second-process
  race still belongs to queue/worker mode per the ticket); node/API
  description wording (node surface belongs to another agent — the
  semantics doc they must quote lives in concurrency.go's header).

## Evidence — 2026-09-06 (DatastoreFinish slice)

- Wording landed, quoting `concurrency.go`'s header (the wording source of
  record). `nodes/datastore.go` Description now states: filtered
  update/delete/clear are one statement, atomic per row on both drivers,
  last writer wins, no row locks; upsert is read-then-write in no single
  transaction, so counters use increment or a preconditioned write with a
  retry, never Get+Update. API operation descriptions for update-rows,
  delete-rows and upsert-row (`internal/api/handlers/datastores.go`)
  carry the same sentences additively. Verified live: the booted server's
  `/api/openapi.json` serves the new descriptions verbatim; SurfaceFinish
  regenerated the web API client + SDK types carrying them.
- PG evidence (was "code-reviewed but unverified"): the concurrency suite
  now runs against live PostgreSQL (pgvector/pg17 image,
  `KILASFLOW_TEST_POSTGRES_DSN`) — `TestConcurrentIncrementsLoseNoWrites`
  (10x10 land exactly 100), `TestPreconditionedUpdate`,
  `TestPreconditionedDelete`, `TestPurgeTenantDropsTablesAndKeepsNeighbours`
  all PASS on both dialects. `make smoke-postgres` itself stays red for
  the unrelated pgvector/compose reason recorded on FEAT-cjpbe6.
- AMENDMENT (criterion 1, ON CONFLICT single-statement upsert): cannot be
  met honestly — do not fake it. Physical tables carry no unique
  constraint on user columns, so there is nothing to conflict on; an
  `ON CONFLICT` clause without a conflict target/arbiter is a parse
  error or dead syntax, and inventing a unique index on user data to
  satisfy the test would change storage semantics for a test's sake.
  The honest primitives are the ones shipped and tested: single-statement
  `Increment` (atomic per row, both drivers) and single-row CAS on
  `updatedAt` (`UpdateWithPrecondition`/`DeleteWithPrecondition`,
  `PreconditionError` carries the current stamp for retry without a
  second read). Proposed: strike criterion 1, accept the CAS/increment
  tests as the atomicity proof. Needs epic-owner ack.
- Widened-SQLite-pool run (criterion 8): still not runnable — the pool is
  pinned to 1 inside `database.Open` with no override knob, and adding
  one to satisfy a test would change production pinning. The concurrency
  semantics hold by construction (single statements + CAS), identical on
  both drivers, and the PostgreSQL halves above exercise them under a
  real multi-connection pool. Recorded; same ack needed.
- Verdict: this ticket STAYS DOING (carried). Wording + PG halves are
  proven; criteria 1 and 8 are amended, not met, and closing over amended
  criteria needs the epic owner's explicit sign-off.

## PG concurrency evidence (Main, 2026-09-06)
`TestConcurrentIncrementsLoseNoWrites` (+ Increment semantics, evolution non-stall, upsert/clear) green on sqlite AND live PG (kf-pg-vector:55434), run by hand with `KILASFLOW_TEST_POSTGRES_DSN` + `-p 1`: 10x10 increments land exactly 100 on both drivers. Still open, needing owner ack: single-statement ON CONFLICT amendment (no unique constraint exists; CAS+Increment are the honest primitives) and the widened-pool run (SQLite pin is structural). No code change in this pass — evidence only.

## Possible amendment path (Main, 2026-09-06, no code changed)
The ON CONFLICT box may be satisfiable without amending it away: `id` is an auto-increment PK on every datastore table, so an upsert addressed BY ID can compile to one statement (`INSERT ... ON CONFLICT(id) DO UPDATE` on PG; SQLite `ON CONFLICT(id)` likewise) with no new constraint. Open questions for the owner: (a) is id-addressed upsert the wanted semantic, or must it match on a user column (which has no unique constraint by design)? (b) widened-pool run stays unrunnable regardless (SQLite single-connection pin is structural). If (a) is id-addressed, the work is: upsert-by-id path in rows.go + emitted-SQL test on both drivers + node/API wording. Awaiting ack before implementing.
