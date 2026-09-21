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

- [ ] Upsert compiles to one statement on both drivers, proven by a test asserting the emitted SQL carries `ON CONFLICT` and contains no preceding `SELECT`. (AMENDED, box left unticked: the id-addressed upsert is that statement on both drivers and is pinned by a test; a filter-addressed upsert stays read-then-write. The ticket's "Possible amendment path" is implemented; the ack that path asks for is still outstanding. See the table below.)
- [x] Ten concurrent workflow executions each incrementing one datastore row leave the value at exactly ten on both drivers, proven by a test run at `execution.max_concurrent` of 10.
- [x] A test asserts that `clause.Locking` emits no SQL through the SQLite dialector, so no Datastore write path can be written believing in a row lock it never receives.
- [x] An optimistic precondition on `updatedAt` refuses a stale write with a distinct conflict error rather than overwriting, proven by a test that interleaves two updates against one row.
- [x] A refused precondition returns the row's current `updatedAt` in the error body, so a caller can retry without a second read, proven by a handler test.
- [x] The concurrency behaviour of upsert, filtered update and delete is stated on the Datastore node description and in the API operation descriptions, captured as evidence on this ticket.
- [x] The concurrent-increment suite passes against PostgreSQL through `make smoke-postgres`, run by hand with its output recorded on this ticket, because the repository has no CI configuration of any kind. (MET by the by-hand run; the "no CI" premise is false — see the stale-premise notes.)
- [ ] Widening the SQLite pool beyond one connection does not change the outcome of any concurrency test, proven by running the suite twice with the pool size varied. (AMENDED: the datastore suite is unchanged at every width; the execution-level concurrency test is not. See the table below.)

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

## Implementation notes — Stage 1, row store (2026-09-20)

Scope: `internal/datastore` only. No HTTP, node, config or migration change.
Files: rows.go, concurrency.go, upsert_id.go (new), doc.go, engine_test.go,
concurrency_test.go, upsert_id_test.go (new), isolation_test.go.
Branch/commit: `worktree-omp-FEAT-1axhdn`, one commit at the end of the
stage. The first two bullets of "Possible amendment path" above are now
implemented: the id-addressed upsert exists, and the widened-pool run is a
committed opt-in env hook (`KILASFLOW_TEST_SQLITE_POOL`) rather than an
unreproducible throwaway edit.

### Status of the 8 criteria after Stage 1

| # | criterion | state after Stage 1 |
|---|-----------|---------------------|
| 1 | Upsert one statement, ON CONFLICT, no preceding SELECT | AMENDED — id-addressed MET, filter-addressed stays read-then-write (pinned by a test) |
| 2 | Ten concurrent executions increment one row to ten (workflow level) | UNMET — row-store level MET; workflow level needs the node operation (Stage 2) and the engine test (Stage 3) |
| 3 | clause.Locking emits no SQL through the SQLite dialector | MET (ticked) |
| 4 | stale updatedAt precondition refused, interleaved test | MET (ticked) |
| 5 | refused precondition returns current updatedAt in the error body (handler test) | UNMET — engine carries `PreconditionError.Current`; the HTTP 409 + handler test is Stage 2 |
| 6 | concurrency behaviour stated on node/API descriptions | UNMET — Stage 2 |
| 7 | concurrent-increment suite via `make smoke-postgres` | UNMET — Stage 3; Stage 1 records a direct scratch-container run instead |
| 8 | widening the SQLite pool does not change any concurrency test | AMENDED — datastore suite MET at widths 1, 4, 8, 10 and on PG; the executions repository changes outcome under a widened pool (V2-p8-7, out of scope) |

MET: id-addressed upsert is one INSERT ... ON CONFLICT(id) DO UPDATE
statement on both drivers. AMENDED: filter-addressed upsert stays
read-then-write and is documented so; Increment and preconditioned writes
are the atomic primitives.

### Bugs found by the tests and fixed in this stage

1. **Insert returned another writer's row (SQLite).** The INSERT was followed
   by `SELECT last_insert_rowid()` on a second pool checkout, which is
   per-connection state. `TestConcurrentInsertsReturnTheirOwnRows` at HEAD
   (pool widths 1/4/10): returned `n=81` for a call that wrote `41` (pool 1),
   `n=81` for `121` (pool 4), and `row not found: 0` (pool 10) — 105/200
   wrong readbacks at pool 1 in the critic's run. Fixed by
   `INSERT ... RETURNING id` on both drivers (one statement).
2. **updatedAt repeated within a millisecond, so a stale precondition was
   accepted.** Three RED tests at HEAD (no `-race`, matching the critic's
   measurement of 468/500 equal stamps): the 10x10 read-then-precondition
   loop finished at 98/84/75 of exactly 100; two back-to-back writes shared
   a stamp; an interleaved write did not make the stale stamp lose. Fixed by
   making `stampNow` strictly increasing per row — SQLite
   `MAX(strftime(now), strftime(col,'+0.001 seconds'))`, PostgreSQL
   `GREATEST(now(), col + interval '1 millisecond')` — table-qualified,
   which is also mandatory inside `ON CONFLICT DO UPDATE` (SQLSTATE 42702
   otherwise).
3. **Correction of the record.** The 2026-09-06 Evidence claimed Increment
   "emits exactly one UPDATE with no preceding SELECT". It was false:
   `TestIncrementSemantics` at HEAD composed and sent three statements
   (SELECT, UPDATE, SELECT), and the old test only counted UPDATE-prefixed
   statements. Increment now emits one `UPDATE ... RETURNING`, asserted both
   by the engine's composeHook and by a dialector-level GORM callback
   capture, and its post-images are the statement's own (sorted by id).
4. **Explicit id was unbounded and could brick a datastore.** Creating a row
   at `math.MaxInt64` made every later plain Insert fail permanently on both
   drivers (SQLite `database or disk is full (13)`, PostgreSQL
   `nextval: reached maximum value of sequence`), reachable by a workflow
   keyValue or an embed session with datastore:write. Explicit ids are now
   capped at 2^53-1 (JavaScript-safe) and refused before any SQL. On
   PostgreSQL the single statement re-syncs the bigserial past the explicit
   id with `setval(seq, GREATEST(id, pg_sequence_last_value(seq)))`, with
   GREATEST computed inside setval so a racing plain insert cannot move the
   sequence backwards.

### Commands run and outcomes (real)

- `go test -race -count=1 ./internal/datastore` — ok (117.2s), SQLite pools 1/4/10.
- `KILASFLOW_TEST_SQLITE_POOL=8 go test -race -count=1 ./internal/datastore` — ok (76.6s).
- `gofmt -l internal/datastore` — clean; `go vet ./internal/datastore` — clean.
- RED (before the fix): `go test -count=1 ./internal/datastore -run
  'ConcurrentInsertsReturnTheirOwnRows|PreconditionedWritesLoseNoUpdateUnderContention|UpdatedAtStrictlyIncreasesOnEveryWrite|StalePreconditionAfterAnInterleavedWriteIsRefused|ConcurrentIncrementsReturnEachWritersOwnValue|IncrementSemantics'`
  — FAIL for every reason above.
- PostgreSQL, scratch `pgvector/pgvector:pg17` container
  `kf-pg-1axhdn` (host port 32787), `-p 1`:
  `KILASFLOW_TEST_POSTGRES_DSN=postgres://kilas:x@127.0.0.1:32787/kilasflow?sslmode=disable go test -race -count=1 -p 1 -v ./internal/datastore`
  — PASS (157.6s), every `postgres` subtest green, including
  `TestUpsertByIDStatementShape`, `TestUpsertByIDIsOneStatement/postgres`,
  `TestUpsertByIDCreatesAtTheIdAndUpdatesInPlace/postgres`,
  `TestUpsertByIDKeepsAutoIncrementAhead/postgres` (the setval re-sync),
  `TestConcurrentUpsertByIDInsertsOnce/postgres` and
  `TestUpsertByIDHonoursTheRowLimit/postgres`. Container kept for Stage 3.

### Stale premises corrected

- "the repository has no CI configuration of any kind" (criterion 7) is
  false: `.github/workflows/ci.yml` exists, its `make test` job runs the
  datastore package against `pgvector:pg17` with `KILASFLOW_TEST_POSTGRES_DSN`,
  and there is a `smoke-postgres` job (main only). Criterion 7's literal
  `make smoke-postgres` run is Stage 3.
- `clause.Locking` has **six** production call sites (executions.go:135 and
  :490, workflows.go:192 and :443, workflow_history.go:185,
  schedules.go:302), not the four the Scope section cites.
- The ticket's "Upsert atomicity ... belong to a later ticket" sentence in
  rows.go's header was replaced; the id path is no longer deferred.

### Proposed learnings (for the orchestrator to record; not recorded here)

- On this repo a pool-width env hook in the test opener is the cheap way to
  keep a "single connection hides the race" bug from regressing.
- A GORM `After("gorm:query"/"gorm:row"/"gorm:raw")` callback capture is a
  reliable DB-level statement counter for Raw/Exec paths; the engine's own
  composeHook is not sufficient evidence.

## Implementation notes — Stage 2, surfaces (2026-09-20)

Scope: the HTTP surface, the Data table node, one SDK method, the wording, and
the regenerated artifacts. The row store itself is untouched: Stage 1 is the
only change to `internal/datastore` in this ticket.
Files: `internal/api/handlers/datastores.go`, `internal/api/datastores_test.go`,
`internal/api/embed_datastore_test.go`, `internal/api/middleware/embed_test.go`,
`nodes/datastore.go`, `nodes/datastore_increment.go` (new),
`nodes/datastore_test.go`, `internal/interop/n8n/parameters.go`,
`internal/interop/n8n/n8n_test.go`, `sdk/src/server.ts`,
`sdk/test/server.test.ts`, `sdk/test/operation-coverage.test.mjs`,
`sdk/README.md`, `sdk/CHANGELOG.md`, root `CHANGELOG.md`,
`scripts/generate-api-reference.mjs`, and the regenerated artifacts
(`web/src/lib/api/generated/**`, `sdk/src/generated/models.ts`,
`docs/src/content/docs/reference/api.md`, `docs/.../reference/api/datastores.md`).

### Status of the 8 criteria after Stage 2

| # | criterion | state after Stage 2 |
|---|-----------|---------------------|
| 1 | Upsert one statement, ON CONFLICT, no preceding SELECT | AMENDED (unchanged) — id-addressed MET and now stated on the API operation; filter-addressed stays read-then-write |
| 2 | Ten concurrent executions increment one row to ten (workflow level) | UNMET — the row store and the HTTP surface are proven atomic at 10 concurrent writers; the workflow-level test at `execution.max_concurrent` 10 is Stage 3 |
| 3 | clause.Locking emits no SQL through the SQLite dialector | MET (ticked) |
| 4 | stale updatedAt precondition refused, interleaved test | MET (ticked) — Stage 2 adds the HTTP proof of the same rule |
| 5 | refused precondition returns current updatedAt in the error body (handler test) | **MET — ticked this stage** |
| 6 | concurrency behaviour stated on node/API descriptions | **MET — ticked this stage** |
| 7 | concurrent-increment suite via `make smoke-postgres` | UNMET — Stage 3. Stage 2 ran the new surfaces against live PostgreSQL through a scratch container instead (below) |
| 8 | widening the SQLite pool does not change any concurrency test | AMENDED (unchanged) |

### What the surface now exposes, and why

- `POST /api/v1/datastores/{id}/rows/increment` — body `{filter, column,
  amount?}`, output `{matched, rows}`. One `UPDATE ... RETURNING` per call in
  the row store, atomic per row on both drivers, and the rows are that
  statement's own post-images. `amount` is a `*float64` rather than a
  `float64` with huma's `default:"1"`: huma applies a default to any zero
  value, so an explicit `"amount": 0` would have silently become 1.
- `ifUpdatedAt` on `PUT /datastores/{id}/rows` and `DELETE
  /datastores/{id}/rows` — optional, so the unconditional last-writer-wins
  path is byte-for-byte what it was. Present, it is compare-and-swap on the
  row's `updatedAt`, and a stale stamp answers **409** with the current stamp
  in `errors[0].value` (`location` `body.ifUpdatedAt`) so a caller retries
  without a second read. The branch is first in `problem()`, ahead of the
  internal-failure and generic 422 branches.
- Data table node: `Row: Increment`, with `counterColumn` and `amount`.
  `counterColumn` is refused at save and at run when it is an expression
  (same reason the filter `keyName` is), and the increment needs at least one
  condition like every other write. The store capability is asserted
  (`DatastoreIncrementer`) rather than added to `DatastoreStore`, so the
  stand-in stores keep compiling and a store without it refuses by name.
- n8n export: an unmapped row operation used to leave silently as `insert`.
  Increment now carries a **blocking** diagnostic on `operation` naming the
  operation and saying it was exported as insert, because a counter exported
  as an append changes what the workflow does, not only what it looks like.
- SDK: `incrementDatastoreRows(datastoreId, filter, column, amount = 1,
  signal?)`, one method, no version bump. The two `IfUnchanged` methods stay
  cut as planned: `ifUpdatedAt` is reachable through the documented API and
  the generated types, and the SDK surface stays minimal ahead of FEAT-3taswf.

### Wording, verbatim as served

Node `Description` (`nodes/datastore.go`):
> Stores rows in a KilasFlow data table: insert, read, update, upsert,
> increment and delete without holding a database credential. A filtered
> update, delete or clear writes in one statement, atomic per row on both
> drivers: concurrent writers never interleave inside a row, the last writer
> wins, and nothing takes a row lock. Upsert matched on the id column is one
> statement (created at exactly that id, never inserted twice); upsert matched
> on any other column is read-then-write in no single transaction, so two
> concurrent upserts against the same filter may both insert. A counter or
> flag that must not lose writes uses Increment, which adds to a number column
> in one statement and returns the new value, never Get followed by Update.

`update-datastore-rows` / `delete-datastore-rows` keep their one-statement,
atomic-per-row, no-row-lock sentences and add: *"Pass ifUpdatedAt — a row's
updatedAt exactly as a previous read returned it — to make the write
conditional: the filter must then match exactly one row and the write lands
only if the row is unchanged. A stale stamp answers 409 with the row's current
updatedAt in errors[0].value, so the caller retries against the new stamp
without a second read."*

`upsert-datastore-row` now states the split: *"When the filter is exactly one
condition, id equals a value between 1 and 9007199254740991, this is a single
INSERT ... ON CONFLICT statement on both drivers: it never inserts the same id
twice and a missing id is created at exactly that id. Matched on any other
column it is read-then-write in no single transaction, so two concurrent
upserts against the same filter may both insert. A counter or flag that must
not lose writes uses increment."*

`increment-datastore-rows`: *"Adds amount (default 1, may be negative) to a
number column on every matching row in one statement, atomic per row on both
drivers, and returns each row as that statement left it. A NULL cell counts as
zero. Concurrent increments never lose a write."*

The sentences the previous Evidence section carried about "a preconditioned
write with a retry" as the node's advice are gone: a retry loop is what the
increment exists to avoid, and the node now says so.

### Tests added (each seen RED first for its own reason)

- `internal/api/datastores_test.go`: `TestStaleIfUpdatedAtIsRefusedWithTheCurrentStamp`,
  `TestStaleIfUpdatedAtIsRefusedOnDelete`, `TestIfUpdatedAtNeedsExactlyOneRow`
  (422 for a filter matching two rows, on update and on delete, rows
  untouched), `TestUpdateWithoutIfUpdatedAtStaysLastWriterWins`,
  `TestIncrementRowsIsAtomicOverHTTP` (10 concurrent POSTs: every response 200,
  the ten returned values are exactly {1..10}, the row reads 10, a missing
  amount counts as 1 and a negative amount subtracts),
  `TestIncrementRowsRefusesWhatItCannotDo` (422 for an empty filter and a
  string column; 404 for an unknown datastore and for another tenant's),
  `TestUpsertOnIDCreatesThenUpdatesOverHTTP` (id 50 inserted then updated,
  `9223372036854775807` refused with 422 and a later plain insert still works),
  `TestDatastoreOperationDescriptionsStateTheirConcurrency` (reads the served
  `/api/openapi.json` and asserts the phrases on the four operations).
- `internal/api/middleware/embed_test.go`: `write increments` allowed, `read
  cannot increment` denied. `internal/api/embed_datastore_test.go`:
  `TestDatastoreSessionCannotIncrementAnotherDatastore` — a session bound to one
  datastore reads another as unknown (404) and the other table is untouched.
- `nodes/datastore_test.go`: `TestDatastoreIncrementNodeAddsAndReturnsTheNewValue`
  (an empty cell starts at the delta, a missing amount is 1, a string column is
  refused, a filter matching nothing emits nothing),
  `TestDatastoreIncrementValidation` (no table, no condition, no counterColumn,
  expression counterColumn — each refused at Compile and at Execute, nothing
  committed), `TestDatastoreNodeDescriptionStatesItsConcurrency`.
- `internal/interop/n8n/n8n_test.go`:
  `TestDatastoreIncrementExportsWithABlockingDiagnostic`.
- `sdk/test/server.test.ts`: the increment request shape (endpoint, body,
  amount default) inside `filtered row writes`, plus the empty-filter refusal.

### Commands run and outcomes (real)

- RED first, then green, scoped:
  `go test -count=1 -run 'TestStaleIfUpdatedAt|TestIfUpdatedAtNeedsExactlyOneRow|TestUpdateWithoutIfUpdatedAt|TestIncrementRows|TestUpsertOnIDCreatesThenUpdatesOverHTTP|TestDatastoreOperationDescriptions' ./internal/api/`
  — 422 "unexpected property body.ifUpdatedAt" and 405 "method not allowed" on
  `/rows/increment` before the change; `ok` after.
- `go test -race -count=1 ./internal/api/... ./nodes/... ./internal/interop/n8n/... ./internal/guardrails/...` — ok.
- `gofmt -l` on every changed Go package — clean; `go vet` on the same — clean.
- `cd web && pnpm check` — 1520 files, 0 errors, 0 warnings; `cd sdk && pnpm check` — clean.
- `cd sdk && pnpm test` — 6 files, 82 tests passed, including the live
  `operation coverage > covers every operation the server declares` gate
  (5697 ms), which boots a real binary: the coverage map and the served
  document agree at 75 operations.
- `make generate-api generate-types generate-api-reference`, then
  `make generate-api-check generate-types-check generate-api-reference-check`
  — all clean (`api-reference: 14 pages fresh (KilasFlow 0.1.0-dev, 75
  operations)`).
- PostgreSQL, the scratch container Stage 1 left running (`kf-pg-1axhdn`, host
  port 32787), driven over HTTP by a throwaway program (since deleted) because
  the committed API tests open SQLite only:
  `KILASFLOW_TEST_POSTGRES_DSN=postgres://kilas:x@127.0.0.1:32787/kilasflow?sslmode=disable go run ./cmd/kf-pgprobe`
  — every check PASS: stale precondition 409 carrying the current stamp
  (`2026-09-20T10:47:56.327Z`), the refused write left the winner's value, ten
  concurrent increments all 200 returning exactly {1..10} and landing 12 in
  total, the id-addressed upsert created at exactly id 50 then updated in
  place, an unbounded id refused 422, and a plain insert still working after.

### Corrections to the record

- The SDK README's operation count was **already stale by one** before this
  stage: the served document declares 74 operations (75 with increment) while
  `sdk/README.md` said 73. It now says 75, which is the number the live
  coverage gate and `generate-api-reference` both report. `api-contract.md`
  states no count, so nothing else needed the same edit.
- `TestDatastoreIncrementNodeAddsAndReturnsTheNewValue` asserts the engine's
  actual refusal for a string column (`datastore: column "title" is a string,
  got float64`) rather than a "needs a number column" message: `Increment`
  coerces the delta against the catalogue before its own type check, so the
  binding refusal is what a caller sees. The HTTP layer maps it to 422 either
  way.
- The plan placed the SDK request-shape tests in `sdk/test/operations.test.ts`;
  the datastore request-shape tests live in `sdk/test/server.test.ts` under
  `filtered row writes`, so that is where the increment ones went.

### Still open (Stage 3)

- Criterion 2 at workflow level (ten concurrent executions through
  `execution.max_concurrent` 10) and criterion 7 (`make smoke-postgres` by
  hand, with the script's shared-image hazard fixed).
- The `docs/src/content/docs/concepts/datastore-concurrency.md` page.
- `generate-api-reference.mjs` prints `Embed: Deny` for every datastore
  operation, which is a pre-existing docs inaccuracy, not this ticket's.

## Implementation notes — Stage 3, proof and docs (2026-09-20)

Scope: the execution-level proof, the PostgreSQL smoke wiring, the concepts
page, and this ticket's evidence. No production Go code changed in this stage
— the row store and the surfaces are Stages 1 and 2 as committed.
Files: `internal/engine/datastore_concurrency_test.go` (new),
`scripts/smoke-postgres.sh` (two appended `docker run` blocks),
`docs/src/content/docs/concepts/datastore-concurrency.md` (new), this file.

### Status of the 8 criteria at the end of the ticket

| # | criterion | final state |
|---|-----------|-------------|
| 1 | Upsert one statement, ON CONFLICT, no preceding SELECT | AMENDED — id-addressed MET (one `INSERT ... ON CONFLICT (id) DO UPDATE ... RETURNING` on both drivers, `TestUpsertByIDIsOneStatement`/`TestUpsertByIDStatementShape`), filter-addressed stays read-then-write and is pinned by `TestFilterAddressedUpsertStaysReadThenWrite`; box left unticked pending the ack the amendment path asks for |
| 2 | Ten concurrent executions increment one row to ten, at `execution.max_concurrent` 10, both drivers | **MET — ticked this stage** |
| 3 | clause.Locking emits no SQL through the SQLite dialector | MET (ticked, Stage 1) |
| 4 | stale updatedAt precondition refused, interleaved test | MET (ticked, Stage 1) |
| 5 | refused precondition returns current updatedAt in the error body (handler test) | MET (ticked, Stage 2) |
| 6 | concurrency behaviour stated on node/API descriptions | MET (ticked, Stage 2) |
| 7 | concurrent-increment suite on PostgreSQL through `make smoke-postgres`, by hand | **MET — ticked this stage** |
| 8 | widening the SQLite pool does not change the outcome of any concurrency test | **AMENDED — box left unticked**: the datastore suite is unchanged at every width, but the execution-level concurrency test is not, and the criterion says "any" |

MET: id-addressed upsert is one INSERT ... ON CONFLICT(id) DO UPDATE
statement on both drivers. AMENDED: filter-addressed upsert stays
read-then-write and is documented so; Increment and preconditioned writes
are the atomic primitives.

### Criterion 2 — the workflow-level proof

New `internal/engine/datastore_concurrency_test.go` (package `engine_test`),
modelled on `TestServiceRunOncePersistsCompletedManualSetExecution`: real
`database.Open`/`Migrate`, real `WorkflowStore`/`ExecutionStore`, the real
catalog, `nodes.RegisterExecutors(..., nodes.WithDatastoreEngine(store))`, and
`engine.NewService` started through `service.Start(ctx, maxConcurrent)`.

`TestTenConcurrentExecutionsIncrementOneDatastoreRow`: one datastore with a
number column `n`, one row inserted at 0, a saved workflow
`kilasflow.manual -> kilasflow.datastore(increment)` filtered on `id eq <row>`
with `counterColumn: n`, `amount: 1`, ten executions queued with
`QueueManualLatest`, `maxConcurrent := config.Default().Execution.MaxConcurrent`
asserted to be 10 before `Start`, then every execution polled to
`StatusSucceeded` (60 s deadline, every status printed on failure), `cancel()`
and `service.Drain`. Asserts the row reads exactly 10 **and** the ten runs'
own returned post-images are exactly {1..10}, each once.
`TestHundredExecutionsIncrementOneDatastoreRowSoak` is the same at 100.

One design point worth recording, because the plan expected otherwise: a
datastore node run's durable output is not its item stream. The engine's trace
path replaces it with `datastore.ProjectTrace`'s envelope — `{"datastore":
{"ids":[1],"rows":1}}`, identifiers and a count, never cell contents — so the
value the increment returned cannot be read from the Data table node's own
`NodeRuns[].Output`. The test therefore observes it where a workflow author
would: a `kilasflow.set` node downstream copies `{{ $json.n }}` onto the item,
and the assertion reads that run's output. The workflow is
`manual -> ds(increment) -> set(counter)`, and the first draft of the test
failed on exactly this (it decoded `{"datastore":{...}}`), which is how the
redirection was found rather than assumed.

Sensitivity, so the new test is not a test that cannot fail: in a throwaway
copy of this tree (`git archive HEAD` into `/tmp`, never the worktree),
`Engine.Increment` was rewritten to the read-then-write shape the ticket
rejects (SELECT, then UPDATE with the read value + delta). The same test then
failed on SQLite pool 1 with `counter = 2, want exactly 10` and returned
values `1 x8, 2 x2` instead of {1..10} — the defect this ticket exists to
remove. The copy was discarded; nothing was committed from it.

### Criterion 7 — PostgreSQL through `make smoke-postgres`

`scripts/smoke-postgres.sh` gained two blocks in the existing style, after the
repository parity block and before the app container starts:
`go test ./internal/datastore -count=1` and
`go test ./internal/engine -run 'TenConcurrentExecutions|HundredExecutions' -count=1`,
both against `postgres://kilasflow:kilasflow@postgres:5432/kilasflow`, each
preceded by a comment saying why it lives there (RETURNING, ON CONFLICT over a
CAST-typed SELECT, the millisecond stamp predicate, sequence re-sync — the
shapes the two dialects can disagree about).

Run by hand with a uniquely tagged image, because plain `make docker` retags
the shared `kilasflow:latest` that other worktrees building in parallel also
tag:

    docker build --build-arg VERSION=1axhdn --build-arg REVISION=$(git rev-parse HEAD) \
      --build-arg SOURCE=local -t kilasflow:1axhdn .
    KILASFLOW_SMOKE_SKIP_BUILD=1 KILASFLOW_SMOKE_IMAGE=kilasflow:1axhdn make smoke-postgres

    ok  	github.com/kilaslab/kilas-flow/internal/database	4.857s
    ok  	github.com/kilaslab/kilas-flow/internal/repository	4.708s
    ok  	github.com/kilaslab/kilas-flow/internal/datastore	39.970s
    ok  	github.com/kilaslab/kilas-flow/internal/engine	2.099s
    smoke-postgres: passed
    exit=0

(The first attempt was killed at ~50 s with make's `Error 143` — SIGTERM to
the `docker run ... go test` process, not a test failure; the retry above is
the recorded run. `make smoke-postgres` itself was repaired by BUG-rpkjpy.)

### Criterion 8 — the widened-pool run, and the finding

The engine test's SQLite handle honours the same opt-in hook Stage 1 added to
the store tests (`KILASFLOW_TEST_SQLITE_POOL`, default off, ~6 lines in
`openDatastoreExecutionDatabase`, with a comment that widening is expected to
expose repository-layer failures and that CI never sets it). Production
pinning in `internal/database/database.go` is untouched.

- The datastore suite at a widened pool:
  `KILASFLOW_TEST_SQLITE_POOL=8 go test -race -count=1 ./internal/datastore`
  — **ok (49.950 s)**. The concurrency cases inside it run at widths 1, 4 and
  10 plus PostgreSQL. Widening changes nothing there.
- The execution-level test at a widened pool, 15 runs:
  `for i in $(seq 15); do KILASFLOW_TEST_SQLITE_POOL=10 go test -count=1 -run 'TenConcurrentExecutions' ./internal/engine; done`
  — **14 of 15 runs failed** (only run 8 passed), every one of them in the
  executions repository, not the datastore:
  `claim execution: database is locked (5) (SQLITE_BUSY)` (6×),
  `claim execution: database is locked (517)` (3×),
  `persist execution trace: create execution node runs: database is locked
  (5) (SQLITE_BUSY)` (2×) and `(517)` (1×); the test's own message names the
  executions that ended `failed` while others sat `queued`. One full run's
  output: 3 failed, 4 succeeded, 3 queued at the 60 s deadline.
  `ClaimNext`'s read-then-write transaction cannot survive a multi-connection
  SQLite pool. That is the queue/worker-mode work (V2-p8-7), and it is
  deliberately not fixed here: no datastore test was skipped or weakened, and
  `git diff` shows only the opt-in hook. The default (pool 1) run stays green,
  and this is why criterion 8 is AMENDED rather than MET.

### Commands run and outcomes (real)

- `go test -race -count=1 -run TestTenConcurrentExecutionsIncrementOneDatastoreRow -v ./internal/engine` — PASS (`sqlite`), 1.22 s.
- `go test -race -count=1 -run TestHundredExecutionsIncrementOneDatastoreRowSoak -v ./internal/engine` — PASS (`sqlite`), 11.98 s.
- `KILASFLOW_TEST_POSTGRES_DSN=postgres://kilas:kilas@127.0.0.1:32805/kilasflow?sslmode=disable go test -race -count=1 -p 1 -run TestTenConcurrentExecutionsIncrementOneDatastoreRow -v ./internal/engine` — PASS (`sqlite` 1.03 s, `postgres` 1.05 s).
- The same DSN, the wider gate:
  `go test -race -count=1 -p 1 ./internal/datastore ./internal/engine ./internal/api/... ./nodes/...`
  — `internal/datastore` ok 179.4 s, `internal/engine` ok 46.1 s, `internal/api` ok 68.7 s, `internal/api/handlers` ok 74.9 s, `internal/api/middleware` ok 2.0 s; **`nodes` FAIL** for two reasons that are not this ticket's, both reproduced identically on a clean copy of `main` against the same server (below).
- `go test -race -count=1 ./internal/engine ./internal/interop/... ./internal/guardrails/...` — ok (engine 28.7 s, interop/n8n 10.5 s, interop/n8n/corpus 6.1 s, guardrails 3.5 s).
- `gofmt -l internal/engine internal/datastore internal/api nodes internal/interop/n8n` — prints nothing. `go vet ./...` — clean. `go build ./...` — clean.
- `cd docs && pnpm install --frozen-lockfile && pnpm build` — 44 pages built, "All internal links are valid."
- `make generate-api-check generate-types-check generate-api-reference-check sdk-check sdk-test sdk-version-check` — exit 0; `api-reference: 14 pages fresh (KilasFlow 0.1.0-dev, 75 operations)`; SDK 6 files / 82 tests passed. `make generate-config-reference-check` — clean. `cd web && pnpm check && pnpm test` — clean, 44 files / 514 tests passed.
- Scratch PostgreSQL: `kf-pg-feat-1axhdn` (`pgvector/pgvector:pg17`, host port 32805), removed at the end of the stage; the Stage 1 container `kf-pg-1axhdn` was removed with it.

### Pre-existing failures observed, not caused by this ticket

1. `nodes` pgvector coverage: on a bare `pgvector/pgvector:pg17` container
   `TestVectorStoreRoundTripOnPostgres`, `TestVectorSearchUsesTheANNIndex` and
   `TestVectorStoreExecutorOnPostgres` fail with
   `relation "kvtest_vector_collections" does not exist (SQLSTATE 42P01)`,
   because migration 000006 is skipped when `pg_extension` has no `vector`
   row in the database the throwaway `kf_vector_test` database is cloned
   from. Installing the extension into `template1` (`psql -U kilas -d template1
   -c 'CREATE EXTENSION IF NOT EXISTS vector'`) makes the coverage run instead
   of failing on the missing relation — `TestVectorStoreRoundTripOnPostgres`
   then passed (1.05 s) — but `TestVectorSearchUsesTheANNIndex` then exceeded
   the 10-minute package timeout on this loaded machine (it inserts 5,000
   384-dimension vectors and builds an HNSW index), so
   `TestVectorStoreExecutorOnPostgres` was not reached in that run.
2. `nodes` `TestUnderIndependentlyAnItemThatNeverResolvesIsStillJustThatItem/postgres`
   fails with `item 1 = map[string]interface {}{"answer":""}, want the resolve
   failure in its place`.
   Every symptom above reproduces identically from a clean `git archive main`
   copy of the tree against the same server, and none of it involves datastore
   code. Recorded here rather than fixed: it belongs to the nodes/pgvector and
   SQL-options surfaces, not to this ticket.

### Stale premises corrected (all verified in this tree)

- The Scope section cites `clause.Locking` at `executions.go:96`,
  `workflows.go:93` and `:225`, `schedules.go:149` — **four** call sites, and
  those line numbers are from an older tree. There are **six** production call
  sites today: `executions.go:135`, `executions.go:490` (SkipLocked),
  `workflow_history.go:185`, `workflows.go:192`, `workflows.go:443`,
  `schedules.go:302`.
- "the repository has no CI configuration of any kind" (criterion 7) is
  **false**: `.github/workflows/ci.yml` runs `make test` with
  `KILASFLOW_TEST_POSTGRES_DSN` against `pgvector/pgvector:pg17` and has a
  `smoke-postgres` job. The criterion's premise is stale; the run it asks for
  was done by hand anyway.
- Concurrency line numbers are stale too: `config.Default()`'s
  `MaxConcurrent: 10` is at `internal/config/config.go:667` (field at :457),
  the call that applies it is `cmd/kilasflow/main.go:552`, and the fan-out is
  `internal/engine/service.go:780` (`Start`) spawning at :788 — not
  config.go:164 / main.go:154 / service.go:246-257.
- The node `Description` and the four operation descriptions are unchanged
  from Stage 2's "Wording, verbatim as served" above; re-verified by
  `make generate-api-reference-check` (14 pages fresh at 75 operations) and by
  `TestDatastoreOperationDescriptionsStateTheirConcurrency` reading the served
  document.

### Bugs found (numbers as measured; Stages 1–2 measured their own, this stage
measured the last two)

- Insert readback: 105 of 200 wrong readbacks at SQLite pool 1 before the fix,
  0 after (`INSERT ... RETURNING`). Measured in Stage 1.
- `updatedAt` ABA: 468 of 500 consecutive updates shared a stamp before the
  fix; the read-then-precondition loop ended at 536 of 599. After the strictly
  increasing stamp: 0 non-increasing stamps, the loop ends at exactly 100.
  Measured in Stage 1 and by the plan's critic.
- Increment was three statements (SELECT, UPDATE, SELECT) at HEAD while the
  2026-09-06 Evidence claimed one; it is now one `UPDATE ... RETURNING`.
  Correction of the record, measured in Stage 1.
- Unbounded explicit id bricked a datastore: SQLite
  `database or disk is full (13)`, PostgreSQL `nextval: reached maximum value
  of sequence`; now bounded at 1..9007199254740991. Measured in Stage 1.
- **This stage**: the executions repository under a widened SQLite pool — 14
  of 15 runs failed (tally above).
- **This stage**: the new execution-level test against a read-then-write
  `Increment` — `counter = 2, want exactly 10`, values `1 x8, 2 x2`.

### Proposed learnings (for the orchestrator to record; not recorded here)

- A datastore node run's durable trace is `datastore.ProjectTrace`'s id/count
  envelope, not its item stream. Any test that needs the *values* a Data table
  node produced must observe them downstream (a Set node) — reading
  `NodeRuns[].Output` on the datastore node itself yields
  `{"datastore":{"ids":[...],"rows":N}}`.
- Widening the SQLite pool is a datastore-safe, executions-unsafe operation:
  `ClaimNext` fails with SQLITE_BUSY/517 and traces fail to persist. The pin
  stays until V2-p8-7 lands.
- The scratch-container recipe should install the vector extension into
  `template1` (`psql -d template1 -c 'CREATE EXTENSION IF NOT EXISTS vector'`)
  or the nodes pgvector coverage fails with 42P01 on a bare
  `pgvector/pgvector:pg17` container; the datastore and engine PostgreSQL
  halves do not need it.
- `make smoke-postgres` boots several `golang:1.27-alpine` containers with a
  cold module cache, so a full run costs minutes of downloads; it is worth
  caching `GOMODCACHE` in a named volume.

### Review round 1 (fixes) — Implementation notes

The three-lens review found one HIGH, one MEDIUM and two LOW findings; the fix
round was interrupted mid-flight (the agent harness hit a provider usage limit)
and finished by the orchestrator.

1. **HIGH — preconditioned update/delete could mutate every row the filter
   matched.** The pre-read approved exactly one row, but the statement was
   `<UPDATE|DELETE> … WHERE <caller filter> AND updatedAt = <stamp>`: a second
   row matching the filter with the same stamp made `RowsAffected` 2, the `== 1`
   branch fell through, and the caller was told `{"matched":0}` / `{"deleted":0}`
   with a nil error — a silent bulk mutation, and silent data loss on delete.
   The predicate is now `id = ? AND updatedAt = ?` with the id the pre-read
   approved (`preconditionPredicate(dialect, approvedID, expectedUpdatedAt)`),
   so the statement can touch at most that row, and any affected-count but 1 is
   an error rather than a quiet "nothing matched". Pinned by
   `TestPreconditionedWritesCannotCatchASecondMatchingRow` on both drivers.
2. **MEDIUM — Insert returned a re-selected row, not its own image.** `Insert`
   appended only `RETURNING id` and then called `Get`, so a neighbour's write
   landing between the two statements came back as the inserted row (reproduced:
   wrote `n=1`, returned `n=99`). It now appends
   `RETURNING <quotedProjection>` and scans that row, so the statement's own
   image is what the caller gets. Pinned by
   `TestInsertReturnsTheStatementOwnImage`, which asserts exactly one INSERT
   statement, that it carries `RETURNING`, and that no SELECT follows it.
3. **LOW — the increment type guard was unreachable**: `canonicalValues` refuses
   a float bound against a non-number column before the guard could fire, so the
   guard and `columnDefinition` (its only caller) were dead. Both are removed;
   the binding refusal is the single documented path.
4. **LOW — `reference/api-contract.md` said the served document holds 74
   operations**; this branch adds `increment-datastore-rows`, so it holds 75
   (`docs/reference/api.md` and the generated reference already said 75). The
   count is corrected; the page is hand-written, so no generator would have
   caught it.

Commands and outcomes: `go test -count=1 ./internal/datastore/` → ok;
`go build ./...`, `go vet ./...` → clean;
`node scripts/generate-api-reference.mjs --check` → "14 pages fresh (KilasFlow
0.1.0-dev, 75 operations)".
