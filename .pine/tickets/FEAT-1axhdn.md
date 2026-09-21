---
id: FEAT-1axhdn
title: Settle Datastore concurrency semantics
status: done
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
updated: "2026-09-21T00:38:31Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (6):
  - `d54871c8` — FEAT-1axhdn: settle the datastore's concurrency semantics
  - `2b7e35a2` — chore(pine): propose id-addressed upsert path on FEAT-1axhdn
  - `3bec38b6` — chore(pine): record PG concurrency evidence on FEAT-1axhdn
  - `c2e3d8c6` — merge: datastore purge, configured limits, and concurrency wording (FEAT-cjpbe6, FEAT-k9dwgn, FEAT-1axhdn)
  - `9132e097` — merge: datastore management API, node, and policy slice (FEAT-xeq6st, FEAT-3xqky1, FEAT-cjpbe6, FEAT-k9dwgn, FEAT-1axhdn)
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .agents/skills/pine/SKILL.md                       |    2 +-
 .editorconfig                                      |   33 +
 .env.example                                       |  186 +
 .github/ISSUE_TEMPLATE/bug_report.yml              |   89 +
 .github/ISSUE_TEMPLATE/config.yml                  |   11 +
 .github/ISSUE_TEMPLATE/feature_request.yml         |   57 +
 .github/PULL_REQUEST_TEMPLATE.md                   |   25 +
 .github/actions/js-toolchain/action.yml            |   58 +
 .github/dependabot.yml                             |   78 +
 .github/workflows/ci.yml                           |  518 ++
 .github/workflows/release.yml                      |  202 +
 .gitignore                                         |   16 +-
 .pine/CHECKPOINT.md                                |  148 +
 .pine/MEMORY.md                                    |   11 +
 .pine/learnings/LRN-jrxe9h.md                      |    9 +
 .pine/learnings/LRN-t016v0.md                      |    9 +
 .pine/memory/code-node.md                          |   74 +
 .pine/memory/docker.md                             |    9 +
 .pine/memory/e2e.md                                |    9 +
 .pine/memory/embedding.md                          |    8 +
 .pine/memory/licensing.md                          |    4 +-
 .pine/memory/live-databases.md                     |   40 +
 .pine/memory/persistence.md                        |   14 +
 .pine/memory/web-editor.md                         |   10 +
 .pine/roadmap.md                                   |  273 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++
 .pine/tickets/BUG-277a2m.md                        |  506 ++
 .pine/tickets/BUG-341sxn.md                        |  623 ++
 .pine/tickets/BUG-4053h6.md                        |  998 ++++
 .pine/tickets/BUG-57n76x.md                        |  576 ++
 .pine/tickets/BUG-5gws7n.md                        |   97 +
 .pine/tickets/BUG-66es9z.md                        |  248 +
 .pine/tickets/BUG-6as5y7.md                        |  690 +++
 .pine/tickets/BUG-6bqh51.md                        |  640 ++
 .pine/tickets/BUG-6jvcs5.md                        |  710 +++
 .pine/tickets/BUG-8dmp5y.md                        |  660 +++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++
 .pine/tickets/BUG-8sb0jw.md                        |  715 +++
 .pine/tickets/BUG-8t94wn.md                        |  628 ++
 .pine/tickets/BUG-9853ay.md                        |  556 ++
 .pine/tickets/BUG-9s3htg.md                        |  170 +
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  815 +++
 .pine/tickets/BUG-br7ggc.md                        |  189 +
 .pine/tickets/BUG-c241hm.md                        |  607 ++
 .pine/tickets/BUG-cq4yk3.md                        |  818 +++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++
 .pine/tickets/BUG-esb9sh.md                        |  590 ++
 .pine/tickets/BUG-f9frth.md                        |  870 +++
 .pine/tickets/BUG-fng4m2.md                        |   88 +
 .pine/tickets/BUG-fv5fer.md                        |  657 +++
 .pine/tickets/BUG-fvdz46.md                        |  400 ++
 .pine/tickets/BUG-gaavr5.md                        |  813 +++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++
 .pine/tickets/BUG-hm76dq.md                        |  569 ++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  548 ++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++
 .pine/tickets/BUG-npfz43.md                        |  487 ++
 .pine/tickets/BUG-p3t7yq.md                        |  212 +
 .pine/tickets/BUG-pwckhd.md                        |  512 ++
 .pine/tickets/BUG-qmgz2f.md                        |  652 ++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++
 .pine/tickets/BUG-rjd6fm.md                        |  721 +++
 .pine/tickets/BUG-rpkjpy.md                        |  984 ++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++
 .pine/tickets/BUG-t9j2ek.md                        |  452 ++
 .pine/tickets/BUG-tcqkad.md                        |  660 +++
 .pine/tickets/BUG-th16c1.md                        |  218 +
 .pine/tickets/BUG-txc9xg.md                        |  520 ++
 .pine/tickets/BUG-v6tdjr.md                        |  349 ++
 .pine/tickets/BUG-vzzkg3.md                        |  598 ++
 .pine/tickets/BUG-w8h3km.md                        |  123 +
 .pine/tickets/BUG-wdypd2.md                        |  680 +++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++
 .pine/tickets/BUG-xmcm8x.md                        |  152 +
 .pine/tickets/BUG-xmr673.md                        |  737 +++
 .pine/tickets/BUG-y57cz4.md                        |  640 ++
 .pine/tickets/BUG-ysvmaa.md                        |  773 +++
 .pine/tickets/BUG-ze1nn8.md                        |  564 ++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++
 .pine/tickets/EPIC-3844bz.md                       |   29 +
 .pine/tickets/EPIC-87t47t.md                       |   50 +
 .pine/tickets/EPIC-8n8aq8.md                       |   38 +
 .pine/tickets/EPIC-bkj6yf.md                       |   46 +
 .pine/tickets/EPIC-cfe7ny.md                       |   61 +
 .pine/tickets/EPIC-m0bne8.md                       |   57 +
 .pine/tickets/EPIC-m42s3g.md                       |   12 +-
 .pine/tickets/EPIC-qx56ay.md                       |   36 +
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-0556ck.md                       |  729 +++
 .pine/tickets/FEAT-0895qc.md                       |  761 +++
 .pine/tickets/FEAT-096vs9.md                       |  784 ++-
 .pine/tickets/FEAT-0f87fn.md                       |  302 +-
 .pine/tickets/FEAT-12s0e5.md                       |  539 ++
 .pine/tickets/FEAT-1500sp.md                       |  168 +-
 .pine/tickets/FEAT-15k49d.md                       |   36 +
 .pine/tickets/FEAT-1axhdn.md                       |  700 +++
 .pine/tickets/FEAT-1br8at.md                       |    2 +-
 .pine/tickets/FEAT-1c70nt.md                       |  173 +
 .pine/tickets/FEAT-1jqjtd.md                       |  131 +
 .pine/tickets/FEAT-27km39.md                       |  730 +++
 .pine/tickets/FEAT-2f68r8.md                       |   83 +-
 .pine/tickets/FEAT-2mth85.md                       |   57 +
 .pine/tickets/FEAT-2phs15.md                       |  461 ++
 .pine/tickets/FEAT-347egc.md                       |  806 ++-
 .pine/tickets/FEAT-3taswf.md                       | 1405 +++++
 .pine/tickets/FEAT-3xqky1.md                       |  916 +++
 .pine/tickets/FEAT-41m8dj.md                       |   55 +
 .pine/tickets/FEAT-45tfmh.md                       |  438 ++
 .pine/tickets/FEAT-48hreg.md                       |  857 ++-
 .pine/tickets/FEAT-4d0bje.md                       |  904 +++
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-4ve1bq.md                       |   67 +
 .pine/tickets/FEAT-53fht8.md                       |  120 +
 .pine/tickets/FEAT-55v09k.md                       |   84 +-
 .pine/tickets/FEAT-56nep4.md                       |  625 ++
 .pine/tickets/FEAT-5fhj6p.md                       |  144 +
 .pine/tickets/FEAT-5fv8gf.md                       |  190 +-
 .pine/tickets/FEAT-5kfctc.md                       |  208 +
 .pine/tickets/FEAT-5kv1jq.md                       |   84 +-
 .pine/tickets/FEAT-5mvech.md                       |  332 ++
 .pine/tickets/FEAT-5rvtzc.md                       |   93 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   85 +-
 .pine/tickets/FEAT-5z37xh.md                       |  767 +++
 .pine/tickets/FEAT-68zzqs.md                       |  438 ++
 .pine/tickets/FEAT-6vfn3s.md                       |  356 +-
 .pine/tickets/FEAT-76p02z.md                       |   44 +
 .pine/tickets/FEAT-77rveq.md                       |   73 +
 .pine/tickets/FEAT-7cg0cd.md                       |  848 ++-
 .pine/tickets/FEAT-7fs90q.md                       |   61 +
 .pine/tickets/FEAT-7tgasa.md                       |  252 +
 .pine/tickets/FEAT-8apb8n.md                       |   37 +
 .pine/tickets/FEAT-8mymac.md                       |   85 +
 .pine/tickets/FEAT-8qyfh1.md                       |  455 +-
 .pine/tickets/FEAT-8r9n21.md                       |  306 +-
 .pine/tickets/FEAT-91as16.md                       |   96 +-
 .pine/tickets/FEAT-9555xz.md                       | 1123 +++-
 .pine/tickets/FEAT-96p7m3.md                       |  784 ++-
 .pine/tickets/FEAT-9dqn7d.md                       |  422 ++
 .pine/tickets/FEAT-9knk67.md                       |   86 +-
 .pine/tickets/FEAT-9pe65j.md                       |   37 +
 .pine/tickets/FEAT-a5fhjw.md                       |  401 ++
 .pine/tickets/FEAT-a6yg3n.md                       |    2 +-
 .pine/tickets/FEAT-a7p1b2.md                       |  136 +
 .pine/tickets/FEAT-a94c8y.md                       |  877 ++-
 .pine/tickets/FEAT-adyeh0.md                       |   53 +
 .pine/tickets/FEAT-adzn0a.md                       |   76 +-
 .pine/tickets/FEAT-afs850.md                       |    3 +-
 .pine/tickets/FEAT-agj52c.md                       |  979 +++
 .pine/tickets/FEAT-ajw7wt.md                       |  122 +-
 .pine/tickets/FEAT-az620p.md                       |  450 +-
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp0ytb.md                       |  340 +-
 .pine/tickets/FEAT-bp59m4.md                       |  420 ++
 .pine/tickets/FEAT-bscygc.md                       |  713 +++
 .pine/tickets/FEAT-c2a081.md                       |  842 ++-
 .pine/tickets/FEAT-c32499.md                       |   39 +
 .pine/tickets/FEAT-c71wy8.md                       |   46 +
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cgm1y3.md                       |  786 ++-
 .pine/tickets/FEAT-cjpbe6.md                       | 1066 ++++
 .pine/tickets/FEAT-cpdp8y.md                       |  924 +++
 .pine/tickets/FEAT-csqgg5.md                       |    5 +-
 .pine/tickets/FEAT-cwmw90.md                       |  530 ++
 .pine/tickets/FEAT-cwz4ac.md                       |  763 +++
 .pine/tickets/FEAT-cx3hq1.md                       |  712 +++
 .pine/tickets/FEAT-czbzs6.md                       |  717 +++
 .pine/tickets/FEAT-ddzk2k.md                       |   77 +-
 .pine/tickets/FEAT-de8d4c.md                       |  818 +++
 .pine/tickets/FEAT-ds4e0m.md                       |   54 +
 .pine/tickets/FEAT-ed6wdy.md                       |  804 +++
 .pine/tickets/FEAT-edxxj7.md                       |  642 ++
 .pine/tickets/FEAT-ej0468.md                       |  826 ++-
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |  936 +++
 .pine/tickets/FEAT-frvez8.md                       |  841 +++
 .pine/tickets/FEAT-fw0m2q.md                       |    2 +-
 .pine/tickets/FEAT-g07pj8.md                       |   67 +
 .pine/tickets/FEAT-g6wrxm.md                       |  196 +
 .pine/tickets/FEAT-gg85se.md                       |  702 +++
 .pine/tickets/FEAT-gjzgkd.md                       | 1036 +++-
 .pine/tickets/FEAT-gvn62x.md                       |  146 +-
 .pine/tickets/FEAT-gxppx1.md                       |  902 +++
 .pine/tickets/FEAT-hj8pyx.md                       |  750 +++
 .pine/tickets/FEAT-hv4q8e.md                       |    2 +-
 .pine/tickets/FEAT-j5s2n4.md                       |  639 ++
 .pine/tickets/FEAT-je4f4t.md                       |  788 ++-
 .pine/tickets/FEAT-jq84xk.md                       |  790 +++
 .pine/tickets/FEAT-jvembs.md                       |  813 +++
 .pine/tickets/FEAT-jwhdsy.md                       |  414 +-
 .pine/tickets/FEAT-k3fmj1.md                       |    3 +-
 .pine/tickets/FEAT-k3grr5.md                       |    2 +-
 .pine/tickets/FEAT-k65hqv.md                       |  846 ++-
 .pine/tickets/FEAT-k9dwgn.md                       | 1050 ++++
 .pine/tickets/FEAT-knpfqf.md                       | 1014 +++-
 .pine/tickets/FEAT-kwxxd0.md                       |  141 +
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-m94hhx.md                       |  265 +
 .pine/tickets/FEAT-mbs0qq.md                       |   61 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +
 .pine/tickets/FEAT-mvegj5.md                       |   76 +-
 .pine/tickets/FEAT-n19dch.md                       |  981 ++++
 .pine/tickets/FEAT-n5fdz3.md                       |  458 ++
 .pine/tickets/FEAT-nbqye0.md                       |    2 +-
 .pine/tickets/FEAT-nc6z9r.md                       | 1014 ++++
 .pine/tickets/FEAT-nch9dg.md                       | 1012 ++++
 .pine/tickets/FEAT-nqpvf6.md                       |  611 ++
 .pine/tickets/FEAT-nrfg6e.md                       |  921 +++
 .pine/tickets/FEAT-nrfz6m.md                       |  197 +
 .pine/tickets/FEAT-nxxbs5.md                       |  213 +
 .pine/tickets/FEAT-p77zr3.md                       |   67 +
 .pine/tickets/FEAT-pd3p6x.md                       |   87 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |  834 +++
 .pine/tickets/FEAT-q81bq4.md                       |  447 +-
 .pine/tickets/FEAT-qcm5ec.md                       |   76 +-
 .pine/tickets/FEAT-qdedm0.md                       | 1025 ++++
 .pine/tickets/FEAT-qe6wb8.md                       |  344 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-r6xhnp.md                       |  808 ++-
 .pine/tickets/FEAT-rj17xj.md                       | 1045 +++-
 .pine/tickets/FEAT-sar60r.md                       |   90 +-
 .pine/tickets/FEAT-sbnejr.md                       |  789 ++-
 .pine/tickets/FEAT-sdjdh2.md                       |   82 +
 .pine/tickets/FEAT-sfy1tq.md                       |  139 +
 .pine/tickets/FEAT-snxxny.md                       |  409 ++
 .pine/tickets/FEAT-sp8cfm.md                       |  361 +-
 .pine/tickets/FEAT-ss44d9.md                       |  875 +++
 .pine/tickets/FEAT-t26rt7.md                       | 1066 ++++
 .pine/tickets/FEAT-t5q318.md                       |    2 +-
 .pine/tickets/FEAT-v3gk2x.md                       |   35 +
 .pine/tickets/FEAT-v8k1tc.md                       |   90 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  398 +-
 .pine/tickets/FEAT-vxbkhg.md                       |   42 +
 .pine/tickets/FEAT-vzp0kb.md                       |   37 +
 .pine/tickets/FEAT-w9kqeg.md                       |    2 +-
 .pine/tickets/FEAT-wdnc03.md                       |  120 +
 .pine/tickets/FEAT-whn5vb.md                       |  103 +-
 .pine/tickets/FEAT-wkmv5e.md                       |  886 +++
 .pine/tickets/FEAT-x35nqx.md                       |   58 +
 .pine/tickets/FEAT-x5km1z.md                       |  590 ++
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-xeq6st.md                       |  914 +++
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |  841 +++
 .pine/tickets/FEAT-xx6p22.md                       |  117 +
 .pine/tickets/FEAT-ybm2pd.md                       |   68 +-
 .pine/tickets/FEAT-ykyfbd.md                       |  972 +++
 .pine/tickets/FEAT-yx0qt6.md                       |  749 +++
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 .pine/tickets/FEAT-yyjfjq.md                       |    3 +-
 .pine/tickets/FEAT-za118x.md                       |  711 +++
 .pine/tickets/FEAT-zmfsjd.md                       |  146 +
 .pine/tickets/FEAT-znm60y.md                       |  316 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  347 +-
 AGENTS.md                                          |    2 +-
 CHANGELOG.md                                       |  169 +
 CODE_OF_CONDUCT.md                                 |  174 +
 CONTRIBUTING.md                                    |  145 +
 Dockerfile                                         |   67 +-
 Makefile                                           |  381 +-
 README.md                                          |  330 +-
 SECURITY.md                                        |  101 +
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +
 cmd/kilasflow/fleet.go                             |   92 +
 cmd/kilasflow/fleet_test.go                        |  395 ++
 cmd/kilasflow/idempotency_test.go                  |  225 +
 cmd/kilasflow/main.go                              | 1140 +++-
 cmd/kilasflow/main_test.go                         |  139 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 cmd/kilasflow/role_test.go                         |   70 +
 cmd/kilasflow/secrets_boot_test.go                 |  102 +
 cmd/kilasflow/webhook_wiring_test.go               |  138 +
 cmd/nodepackgen/authorcmd.go                       |  134 +
 cmd/nodepackgen/generate.go                        |  576 ++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  175 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 +
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 compose.build.yaml                                 |   35 +
 compose.postgres.yaml                              |   79 +
 compose.yaml                                       |  103 +
 config.example.yaml                                |  413 +-
 devbox.json                                        |    2 -
 docker-compose.yml                                 |   41 -
 docs/.gitignore                                    |    6 +
 docs/astro.config.mjs                              |  130 +
 docs/package.json                                  |   20 +
 docs/plugins/base-links.mjs                        |   57 +
 docs/pnpm-lock.yaml                                | 3807 ++++++++++++
 docs/src/components/ThemeProvider.astro            |   59 +
 docs/src/components/ThemeSelect.astro              |   79 +
 docs/src/content.config.ts                         |   11 +
 docs/src/content/docs/404.md                       |   21 +
 docs/src/content/docs/concepts/architecture.md     |  163 +
 docs/src/content/docs/concepts/credentials.md      |  232 +
 .../content/docs/concepts/datastore-concurrency.md |  179 +
 docs/src/content/docs/concepts/execution-model.md  |  454 ++
 docs/src/content/docs/concepts/expressions.md      |  241 +
 .../src/content/docs/concepts/items-and-lineage.md |  174 +
 docs/src/content/docs/concepts/node-registry.md    |  370 ++
 .../src/content/docs/concepts/safety-boundaries.md |  318 +
 .../content/docs/concepts/tenancy-and-embedding.md |  314 +
 docs/src/content/docs/concepts/webhooks.md         |  306 +
 docs/src/content/docs/contributing.md              |   89 +
 docs/src/content/docs/guides/community-nodes.md    |  139 +
 docs/src/content/docs/guides/embedding.md          |  397 ++
 docs/src/content/docs/guides/idempotency.md        |  197 +
 docs/src/content/docs/guides/n8n-migration.md      |  707 +++
 docs/src/content/docs/guides/node-authoring.md     |  511 ++
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +
 docs/src/content/docs/index.mdx                    |   59 +
 docs/src/content/docs/operate/benchmark.md         |   65 +
 .../docs/operate/configuration-reference.md        |  895 +++
 docs/src/content/docs/operate/configuration.md     |   71 +
 docs/src/content/docs/operate/deployment.md        |  193 +
 docs/src/content/docs/operate/security.md          |  147 +
 docs/src/content/docs/operate/tenant-deletion.md   |  194 +
 docs/src/content/docs/operate/upgrades.md          |  131 +
 docs/src/content/docs/reference/api-contract.md    |  341 ++
 docs/src/content/docs/reference/api.md             |   43 +
 docs/src/content/docs/reference/api/auth.md        |  136 +
 docs/src/content/docs/reference/api/credentials.md |  175 +
 docs/src/content/docs/reference/api/datastores.md  |  417 ++
 docs/src/content/docs/reference/api/embed.md       |   29 +
 docs/src/content/docs/reference/api/errors.md      |   47 +
 docs/src/content/docs/reference/api/events.md      |   40 +
 docs/src/content/docs/reference/api/executions.md  |  102 +
 docs/src/content/docs/reference/api/interop.md     |   51 +
 docs/src/content/docs/reference/api/nodes.md       |  111 +
 docs/src/content/docs/reference/api/schedules.md   |   95 +
 docs/src/content/docs/reference/api/system.md      |   43 +
 docs/src/content/docs/reference/api/tenants.md     |  221 +
 docs/src/content/docs/reference/api/webhooks.md    |   36 +
 docs/src/content/docs/reference/api/workflows.md   |  340 ++
 docs/src/content/docs/reference/cli.md             |  628 ++
 .../content/docs/reference/expression-grammar.md   |  390 ++
 docs/src/content/docs/reference/node-packs.md      |  166 +
 docs/src/content/docs/start/first-workflow.md      |   40 +
 docs/src/content/docs/start/install.md             |  313 +
 docs/src/content/docs/start/what-kilasflow-is.md   |  122 +
 docs/src/styles/kilasflow.css                      |  138 +
 .../specs/2026-09-20-agent-surface-design.md       |  519 ++
 docs/tsconfig.json                                 |    5 +
 e2e/.gitignore                                     |    2 +
 e2e/benchmark/README.md                            |   82 +
 e2e/benchmark/SUMMARY.md                           |   44 +
 e2e/benchmark/bench-2026-09-06T07-47-10.json       |  216 +
 e2e/benchmark/bench-2026-09-06T08-07-31.json       |  831 +++
 e2e/benchmark/lib.mjs                              |  399 ++
 e2e/benchmark/n8n.mjs                              |  166 +
 e2e/benchmark/run.mjs                              |  271 +
 e2e/benchmark/summarise.mjs                        |  118 +
 e2e/benchmark/workflows.mjs                        |  320 +
 e2e/fixtures.ts                                    |   40 +
 e2e/fixtures/ai-gateway.ts                         |  126 +
 e2e/fixtures/datastore.ts                          |  546 ++
 e2e/fixtures/epic-external.ts                      |  290 +
 e2e/fixtures/epic-telegram.ts                      |  442 ++
 e2e/fixtures/error-form-nodes.ts                   |  218 +
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import-exports/ai-agent.json  |   60 +
 .../library-import-exports/data-shaping.json       |   49 +
 .../library-import-exports/flow-control.json       |   67 +
 e2e/fixtures/library-import-exports/http-stub.json |   46 +
 .../library-import-exports/scheduled-tick.json     |   38 +
 .../library-import-exports/unsupported-node.json   |   37 +
 .../library-import-exports/webhook-echo.json       |   50 +
 e2e/fixtures/library-import.ts                     |  181 +
 e2e/fixtures/live-backend.ts                       |  263 +
 e2e/fixtures/n8n-live.ts                           |  284 +
 e2e/fixtures/pack-acme-transcription.json          |   91 +
 e2e/fixtures/pack-convert-driver.go                |   58 +
 e2e/fixtures/pack-install.ts                       |  269 +
 e2e/fixtures/waha-migration.ts                     |  210 +
 e2e/global-setup.ts                                |   27 +
 e2e/helpers/seed.ts                                |  174 +
 e2e/helpers/server.ts                              |  142 +
 e2e/helpers/stub.ts                                |  106 +
 e2e/package.json                                   |   14 +
 e2e/playwright.config.ts                           |   36 +
 e2e/pnpm-lock.yaml                                 |   57 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |  574 ++
 e2e/tests/dashboard-lists.spec.ts                  |  187 +
 e2e/tests/datastore-pg.spec.ts                     |  257 +
 e2e/tests/datastore.spec.ts                        |  376 ++
 e2e/tests/epic-acceptance.spec.ts                  |  769 +++
 e2e/tests/library-import.spec.ts                   |  363 ++
 e2e/tests/live-backend-api.spec.ts                 |  386 ++
 e2e/tests/live-backend-datastore.spec.ts           |  282 +
 e2e/tests/live-backend-http-auth.spec.ts           |   94 +
 e2e/tests/live-backend-queue.spec.ts               |  213 +
 e2e/tests/live-backend-webhook.spec.ts             |  401 ++
 e2e/tests/n8n-compare.spec.ts                      | 1073 ++++
 e2e/tests/node-coverage.spec.ts                    |  912 +++
 e2e/tests/pack-converted.spec.ts                   |  249 +
 e2e/tests/pack-editor.spec.ts                      |  248 +
 e2e/tests/pack-install.spec.ts                     |  173 +
 e2e/tests/pack-safety.spec.ts                      |  100 +
 e2e/tests/pack-trigger.spec.ts                     |  165 +
 e2e/tests/smoke.spec.ts                            |  108 +
 e2e/tests/waha-migration.spec.ts                   |  286 +
 executions-narrow.png                              |  Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |   32 +
 go.mod                                             |   13 +-
 go.sum                                             |   41 +-
 internal/ai/agent.go                               |  230 +-
 internal/ai/agent_output_test.go                   |  185 +
 internal/ai/ai.go                                  |   97 +-
 internal/ai/ai_test.go                             |  300 +-
 internal/ai/fromai.go                              |  548 ++
 internal/ai/fromai_test.go                         |  139 +
 internal/ai/maf/doc.go                             |   15 +-
 internal/ai/maf/runtime.go                         |  144 +
 internal/ai/maf/runtime_test.go                    |  131 +
 internal/ai/memory.go                              |  185 +-
 internal/ai/openai.go                              |  236 +-
 internal/ai/openai_test.go                         |  303 +-
 internal/ai/outputschema.go                        |  430 ++
 internal/api/auth_test.go                          |  668 +++
 internal/api/cors_test.go                          |  136 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/credentials_test.go                   |  499 ++
 internal/api/csv_export_test.go                    |   66 +
 internal/api/datastores_csv_test.go                |  353 ++
 internal/api/datastores_test.go                    |  817 +++
 internal/api/embed_confinement_test.go             |  352 ++
 internal/api/embed_datastore_test.go               |  258 +
 internal/api/embed_defaults_test.go                |  150 +
 internal/api/embed_test.go                         |   94 +-
 internal/api/events_test.go                        |   97 +-
 internal/api/handlers/admin.go                     |  678 +++
 internal/api/handlers/admin_admin_test.go          |  834 +++
 internal/api/handlers/auth.go                      |  575 ++
 internal/api/handlers/auth_test.go                 |  422 ++
 internal/api/handlers/credentials.go               |  402 +-
 internal/api/handlers/datastores.go                |  903 +++
 internal/api/handlers/datastores_csv.go            |  573 ++
 internal/api/handlers/datastores_csv_test.go       |  147 +
 internal/api/handlers/embed.go                     |   82 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  390 +-
 internal/api/handlers/idempotency.go               |  115 +
 internal/api/handlers/interop.go                   |  151 +-
 internal/api/handlers/nodes.go                     |  480 +-
 internal/api/handlers/problem.go                   |  129 +
 internal/api/handlers/resume.go                    |  216 +
 internal/api/handlers/resume_test.go               |  232 +
 internal/api/handlers/schedules.go                 |   56 +-
 internal/api/handlers/system.go                    |  142 +-
 internal/api/handlers/tenants.go                   |   46 +
 internal/api/handlers/workflows.go                 |  665 ++-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |  111 +
 internal/api/idempotency_test.go                   | 1004 ++++
 internal/api/import_diagnostics_test.go            |  142 +
 internal/api/list_pagination_test.go               |  221 +
 internal/api/middleware/auth.go                    |  243 +
 internal/api/middleware/auth_test.go               |  436 ++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  147 +
 internal/api/middleware/cors_test.go               |  235 +
 internal/api/middleware/embed.go                   |   80 +-
 internal/api/middleware/embed_test.go              |  122 +
 internal/api/middleware/loginlimit.go              |  216 +
 internal/api/middleware/loginlimit_test.go         |  171 +
 internal/api/middleware/sessioncache.go            |  156 +
 internal/api/node_types_test.go                    |  465 +-
 internal/api/node_visibility_test.go               |  544 ++
 internal/api/openapi_security_test.go              |  196 +
 internal/api/ready_fleet_test.go                   |  358 ++
 internal/api/routes.go                             |   68 +-
 internal/api/server.go                             |  234 +-
 internal/api/server_test.go                        |    4 +-
 internal/api/tenant_delete_test.go                 |  386 ++
 internal/api/workflow_history_test.go              |  188 +
 internal/api/workflows_test.go                     |  327 +-
 internal/auth/auth.go                              |   73 +
 internal/auth/auth_test.go                         |  347 ++
 internal/auth/keys.go                              |  197 +
 internal/auth/session.go                           |  338 ++
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |  340 ++
 internal/binary/binary_test.go                     |  427 ++
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  382 ++
 internal/cli/cli_test.go                           |  248 +
 internal/cli/client.go                             |  397 ++
 internal/cli/client_test.go                        |  320 +
 internal/cli/command.go                            |  139 +
 internal/cli/command_test.go                       |  302 +
 internal/cli/config.go                             |  258 +
 internal/cli/config_test.go                        |  749 +++
 internal/cli/context.go                            |  318 +
 internal/cli/context_test.go                       |  236 +
 internal/cli/doc.go                                |   35 +
 internal/cli/exit.go                               |  113 +
 internal/cli/exit_test.go                          |  101 +
 internal/cli/flags.go                              |   84 +
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  212 +
 internal/cli/openapi.go                            |  202 +
 internal/cli/openapi_contract_test.go              |  411 ++
 internal/cli/output.go                             |  116 +
 internal/cli/output_test.go                        |  246 +
 internal/cli/sse.go                                |  151 +
 internal/cli/sse_test.go                           |  149 +
 internal/cli/verbs_api.go                          |  315 +
 internal/cli/verbs_api_test.go                     |  820 +++
 internal/cli/verbs_auth.go                         |  289 +
 internal/cli/verbs_credential.go                   |  146 +
 internal/cli/verbs_credential_test.go              |  119 +
 internal/cli/verbs_datastore.go                    |  209 +
 internal/cli/verbs_datastore_test.go               |  215 +
 internal/cli/verbs_exec.go                         |  413 ++
 internal/cli/verbs_exec_test.go                    |  436 ++
 internal/cli/verbs_node.go                         |  292 +
 internal/cli/verbs_node_test.go                    |  196 +
 internal/cli/verbs_pack.go                         |  143 +
 internal/cli/verbs_pack_test.go                    |  195 +
 internal/cli/verbs_run.go                          |  257 +
 internal/cli/verbs_run_test.go                     |  314 +
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +
 internal/cli/verbs_system_test.go                  |  182 +
 internal/cli/verbs_tenant.go                       |   86 +
 internal/cli/verbs_tenant_test.go                  |  116 +
 internal/cli/verbs_workflow.go                     |  592 ++
 internal/cli/verbs_workflow_test.go                |  425 ++
 internal/conditions/conditions.go                  |  861 +++
 internal/conditions/conditions_test.go             |  426 ++
 internal/conditions/doc.go                         |   34 +
 internal/config/boot_strictness_test.go            |  323 +
 internal/config/config.go                          | 1030 +++-
 internal/config/config_test.go                     |  489 ++
 internal/config/embed_branding_test.go             |  192 +
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 +
 internal/config/packs_visibility_test.go           |  168 +
 internal/config/secrets_test.go                    |   35 +
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |  257 +
 internal/credentials/credentials.go                |  113 +-
 internal/credentials/credentials_test.go           |  304 +-
 internal/credentials/external.go                   |  437 ++
 internal/credentials/external_test.go              |  359 ++
 internal/credentials/keysource.go                  |   82 +
 internal/credentials/redirect_test.go              |  141 +
 internal/credentials/registry.go                   |  421 ++
 internal/credentials/vault.go                      |  155 +
 internal/database/database.go                      |   73 +-
 internal/database/database_test.go                 |   93 +-
 internal/database/migrate.go                       |  736 +++
 internal/database/migrate_test.go                  | 1032 ++++
 internal/database/prefix_test.go                   |  371 ++
 internal/database/tenant_columns_test.go           |  309 +
 internal/database/webhook_route_backfill_test.go   |  491 ++
 internal/datastore/catalogue.go                    |  267 +
 internal/datastore/catalogue_test.go               |  189 +
 internal/datastore/column_tenant_test.go           |  152 +
 internal/datastore/concurrency.go                  |  306 +
 internal/datastore/concurrency_test.go             |  862 +++
 internal/datastore/config_bind_test.go             |   28 +
 internal/datastore/doc.go                          |   42 +
 internal/datastore/engine.go                       |  453 ++
 internal/datastore/engine_test.go                  |  672 +++
 internal/datastore/evolve_test.go                  |  156 +
 internal/datastore/filter.go                       |  370 ++
 internal/datastore/fleet.go                        |  442 ++
 internal/datastore/fleet_engine_test.go            |  711 +++
 internal/datastore/fleet_test.go                   |  117 +
 internal/datastore/idents.go                       |  206 +
 internal/datastore/idents_test.go                  |  215 +
 internal/datastore/isolation.go                    |  126 +
 internal/datastore/isolation_test.go               |  450 ++
 internal/datastore/limits.go                       |  192 +
 internal/datastore/limits_test.go                  |  295 +
 internal/datastore/migrate_test.go                 |  245 +
 internal/datastore/model.go                        |   58 +
 internal/datastore/rows.go                         |  862 +++
 internal/datastore/rows_test.go                    |  651 ++
 internal/datastore/trace.go                        |  118 +
 internal/datastore/trace_test.go                   |  183 +
 internal/datastore/upsert_id.go                    |  247 +
 internal/datastore/upsert_id_test.go               |  498 ++
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/embed/confinement.go                      |  156 +
 internal/embed/confinement_test.go                 |  142 +
 internal/embed/embed.go                            |  251 +-
 internal/embed/embed_branding_test.go              |  164 +
 internal/embed/embed_lifetime_test.go              |  146 +
 internal/embed/embed_test.go                       |  135 +-
 internal/engine/approval.go                        |  341 ++
 internal/engine/approval_test.go                   |  213 +
 internal/engine/authenticate.go                    |  139 +
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |  106 +
 internal/engine/datastore_concurrency_test.go      |  393 ++
 internal/engine/error_workflow_test.go             |  230 +
 internal/engine/export_test.go                     |   20 +
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 ++
 internal/engine/live_progress_test.go              |  187 +
 internal/engine/loopstate_test.go                  |  183 +
 internal/engine/multiproc.go                       |  285 +
 internal/engine/multiprocess_test.go               | 1069 ++++
 internal/engine/runner.go                          | 1736 +++++-
 internal/engine/runner_test.go                     | 1950 +++++-
 internal/engine/service.go                         | 1269 +++-
 internal/engine/service_test.go                    |  448 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/tenant_visibility_test.go          |  379 ++
 internal/engine/trace.go                           |   28 +
 internal/engine/trace_persist_test.go              |  159 +
 internal/engine/trace_test.go                      |  269 +
 internal/engine/wait_service.go                    |  774 +++
 internal/engine/wait_service_test.go               | 1187 ++++
 internal/engine/worker_test.go                     |   57 +-
 internal/events/events.go                          |    2 +-
 internal/events/events_test.go                     |    2 +-
 internal/execution/records.go                      |   59 +-
 internal/execution/redact.go                       |    8 +
 internal/execution/redact_datastore_test.go        |   75 +
 internal/execution/redact_test.go                  |    2 +-
 internal/expression/doc.go                         |  138 +-
 internal/expression/evaluator.go                   |  938 +++
 internal/expression/expression.go                  |  374 +-
 internal/expression/expression_test.go             |  394 +-
 internal/expression/globals.go                     |  565 ++
 internal/expression/luxon.go                       |  320 +
 internal/expression/methods.go                     | 1217 ++++
 internal/expression/parity_test.go                 | 1041 ++++
 internal/expression/parser.go                      |  824 +++
 internal/expression/roots.go                       |  416 ++
 internal/expression/undefined.go                   |   22 +
 internal/guardrails/compile_scope_test.go          |  440 ++
 internal/guardrails/licence_boundary_test.go       |    4 +-
 internal/guardrails/shell_injection_test.go        |   70 +
 internal/idempotency/hash.go                       |   64 +
 internal/idempotency/hash_test.go                  |  142 +
 internal/idempotency/idempotency.go                |  432 ++
 internal/idempotency/idempotency_test.go           | 1120 ++++
 internal/idempotency/sweeper.go                    |   94 +
 internal/idempotency/sweeper_test.go               |  146 +
 internal/interop/n8n/corpus/BASELINE.md            |   59 +-
 internal/interop/n8n/corpus/baseline.json          |  140 +-
 .../n8n/corpus/fixtures/control-datatable.json     |   95 +
 internal/interop/n8n/corpus/scoreboard_test.go     |  153 +-
 internal/interop/n8n/export_test.go                |   15 +
 internal/interop/n8n/gowa.go                       |  190 +
 internal/interop/n8n/gowa_test.go                  |   89 +
 internal/interop/n8n/importer_tail_test.go         | 1087 ++++
 internal/interop/n8n/n8n.go                        | 1232 +++-
 internal/interop/n8n/n8n_test.go                   | 3060 +++++++++-
 internal/interop/n8n/parameters.go                 | 6208 ++++++++++++++++++--
 internal/interop/n8n/sqlfidelity_test.go           |  442 ++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |  110 +
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++
 internal/loadoptions/datastores.go                 |  124 +
 internal/loadoptions/datastores_test.go            |   74 +
 internal/loadoptions/loadoptions.go                |  423 ++
 internal/loadoptions/loadoptions_test.go           |  453 ++
 internal/loadoptions/redirect_test.go              |  117 +
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 +
 internal/loadoptions/sql_test.go                   |  362 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  749 ++-
 internal/node/registry_bench_test.go               |  112 +
 internal/node/registry_test.go                     |  822 ++-
 internal/node/visibility.go                        |  347 ++
 internal/node/visibility_test.go                   |  796 +++
 internal/nodepack/author.go                        |  159 +
 internal/nodepack/author_test.go                   |  307 +
 internal/nodepack/convert.go                       |  799 +++
 internal/nodepack/convert_test.go                  |  413 ++
 internal/nodepack/loaddir.go                       |  155 +
 internal/nodepack/loaddir_test.go                  |  336 ++
 internal/nodepack/nodepack.go                      |  450 ++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  441 ++
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |  428 ++
 internal/nodepack/visibility_test.go               |  262 +
 internal/property/loader.go                        |   92 +
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 +
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  465 ++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 +
 internal/property/visibility_test.go               |  111 +
 internal/repository/auth.go                        |  718 +++
 internal/repository/auth_admin_test.go             |  477 ++
 internal/repository/auth_test.go                   |  322 +
 internal/repository/claim_lease_test.go            |  266 +
 internal/repository/claim_wake_test.go             |  398 ++
 internal/repository/credentials.go                 |  303 +-
 internal/repository/credentials_external_test.go   |  246 +
 internal/repository/execution_retention.go         |  180 +
 internal/repository/execution_retention_test.go    |  395 ++
 internal/repository/executions.go                  |  755 ++-
 internal/repository/idempotency.go                 |  360 ++
 internal/repository/idempotency_test.go            |  615 ++
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 +
 internal/repository/models.go                      |  347 +-
 internal/repository/models_test.go                 |  163 +-
 internal/repository/postgres_execution_test.go     |  229 +
 internal/repository/prefix_test.go                 |   80 +
 internal/repository/schedule_list_test.go          |  188 +
 internal/repository/schedules.go                   |  259 +-
 internal/repository/subworkflow_activation_test.go |  234 +
 internal/repository/table_names_test.go            |   52 +
 internal/repository/tenant_purge.go                |   83 +
 internal/repository/tenant_purge_test.go           |  166 +
 internal/repository/tenant_rows.go                 |  283 +
 internal/repository/tenant_rows_test.go            |  420 ++
 internal/repository/waits.go                       |  521 ++
 internal/repository/waits_test.go                  |  313 +
 internal/repository/wake.go                        |  199 +
 internal/repository/wake_internal_test.go          |   93 +
 internal/repository/webhooks.go                    |  284 +-
 internal/repository/webhooks_delivery_test.go      |  147 +
 internal/repository/webhooks_routes_test.go        |   46 +
 internal/repository/webhooks_test.go               |  464 ++
 internal/repository/workflow_history.go            |  454 ++
 internal/repository/workflow_history_test.go       |  613 ++
 internal/repository/workflow_list_test.go          |  196 +
 internal/repository/workflows.go                   |  427 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 ++
 internal/routing/request.go                        |  412 ++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 +++
 internal/runcode/doc.go                            |   37 +-
 internal/runcode/runcode.go                        |   96 +-
 internal/runcode/runcode_test.go                   |  186 +-
 internal/safehttp/safehttp.go                      |  193 +-
 internal/safehttp/safehttp_test.go                 |  292 +-
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |  133 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 +
 internal/scheduler/rule_test.go                    |  278 +
 internal/scheduler/scheduler.go                    |  141 +-
 internal/scheduler/scheduler_test.go               |  236 +-
 internal/sqlbuild/dialect.go                       |  256 +
 internal/sqlbuild/sqlbuild.go                      |  392 ++
 internal/sqlbuild/sqlbuild_test.go                 |  657 +++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |    2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |    2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |    2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |    2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |    2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |    2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |    2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |    2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |    2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |    2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |    2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |    2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |    1 +
 .../testdata/mysql/delete_drop_cascade.sql         |    1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |    2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |    1 +
 .../testdata/mysql/delete_truncate_restart.sql     |    1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |    2 +
 .../testdata/mysql/insert_skip_conflict.sql        |    2 +
 internal/sqlbuild/testdata/mysql/select.sql        |    3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |    2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |    3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |    3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |    2 +
 internal/sqlbuild/testdata/mysql/update.sql        |    2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |    2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |    1 +
 .../testdata/postgres/delete_drop_cascade.sql      |    1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |    1 +
 .../testdata/postgres/delete_truncate_restart.sql  |    1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |    3 +
 .../testdata/postgres/insert_skip_conflict.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |    2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |    3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |    3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |    2 +
 internal/sqlbuild/testdata/postgres/update.sql     |    3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |    3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |    3 +
 internal/sqlguard/admit.go                         |  245 +
 internal/sqlguard/attack_test.go                   |  344 ++
 internal/sqlguard/dialect.go                       |  260 +
 internal/sqlguard/doc.go                           |   53 +
 internal/sqlguard/sqlguard.go                      |  443 ++
 internal/sqlguard/sqlguard_test.go                 |  338 ++
 internal/sqlnode/export_test.go                    |   11 +
 internal/sqlnode/guard_test.go                     |  126 +
 internal/sqlnode/internal_test.go                  |  251 +
 internal/sqlnode/introspect.go                     |  240 +
 internal/sqlnode/policy_test.go                    |  243 +
 internal/sqlnode/sqlnode.go                        |  905 ++-
 internal/sqlnode/sqlnode_test.go                   |  108 +-
 internal/tenantpurge/completeness_test.go          |  368 ++
 internal/tenantpurge/doc.go                        |  120 +
 internal/tenantpurge/docs_test.go                  |  115 +
 internal/tenantpurge/harness_test.go               |  614 ++
 internal/tenantpurge/purge.go                      |  412 ++
 internal/tenantpurge/purge_test.go                 |  507 ++
 internal/web/dist/index.html                       |    1 -
 internal/web/embed.go                              |  459 +-
 internal/web/embed_test.go                         |  335 +-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/form.go                           |  262 +
 internal/webhook/form_test.go                      |  169 +
 internal/webhook/jwt.go                            |  144 +
 internal/webhook/jwt_test.go                       |  212 +
 internal/webhook/lifecycle.go                      |  304 +
 internal/webhook/lifecycle_test.go                 |  229 +
 internal/webhook/request_lifecycle.go              |  556 ++
 internal/webhook/request_lifecycle_test.go         |  411 ++
 internal/webhook/require_auth.go                   |   74 +
 internal/webhook/require_auth_test.go              |  367 ++
 internal/webhook/route_label_test.go               |  172 +
 internal/webhook/shape.go                          |  544 ++
 internal/webhook/shape_test.go                     |  383 ++
 internal/webhook/webhook.go                        | 1113 +++-
 internal/webhook/webhook_test.go                   | 1198 +++-
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |  489 +-
 internal/workflow/compiler_test.go                 |  215 +
 internal/workflow/compiler_visibility_test.go      |  280 +
 internal/workflow/document.go                      |   85 +-
 internal/workflow/document_test.go                 |  366 +-
 internal/workflow/lifecycle.go                     |   51 +
 internal/workflow/typeversion.go                   |   10 +
 internal/workflow/typeversion_test.go              |    2 +-
 migrations/.gitkeep                                |    0
 migrations/embed.go                                |   27 +
 migrations/postgres/000001_baseline.down.sql       |   23 +
 migrations/postgres/000001_baseline.up.sql         |  192 +
 .../postgres/000002_workflow_history.down.sql      |   11 +
 migrations/postgres/000002_workflow_history.up.sql |   30 +
 migrations/postgres/000003_identity.down.sql       |   15 +
 migrations/postgres/000003_identity.up.sql         |   75 +
 .../postgres/000004_execution_indexes.down.sql     |    5 +
 .../postgres/000004_execution_indexes.up.sql       |   26 +
 migrations/postgres/000005_datastores.down.sql     |   10 +
 migrations/postgres/000005_datastores.up.sql       |   48 +
 migrations/postgres/000006_vector_store.down.sql   |   19 +
 migrations/postgres/000006_vector_store.up.sql     |  155 +
 .../postgres/000008_secret_bindings.down.sql       |    6 +
 migrations/postgres/000008_secret_bindings.up.sql  |   29 +
 .../postgres/000009_execution_waits.down.sql       |    6 +
 migrations/postgres/000009_execution_waits.up.sql  |   40 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../postgres/000013_node_run_response.down.sql     |    9 +
 .../postgres/000013_node_run_response.up.sql       |   28 +
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 migrations/sqlite/000001_baseline.down.sql         |   22 +
 migrations/sqlite/000001_baseline.up.sql           |  185 +
 migrations/sqlite/000002_workflow_history.down.sql |   11 +
 migrations/sqlite/000002_workflow_history.up.sql   |   29 +
 migrations/sqlite/000003_identity.down.sql         |   14 +
 migrations/sqlite/000003_identity.up.sql           |   73 +
 .../sqlite/000004_execution_indexes.down.sql       |    5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |   21 +
 migrations/sqlite/000005_datastores.down.sql       |   10 +
 migrations/sqlite/000005_datastores.up.sql         |   47 +
 migrations/sqlite/000006_vector_store.down.sql     |    6 +
 migrations/sqlite/000006_vector_store.up.sql       |   29 +
 migrations/sqlite/000008_secret_bindings.down.sql  |    6 +
 migrations/sqlite/000008_secret_bindings.up.sql    |   29 +
 migrations/sqlite/000009_execution_waits.down.sql  |    6 +
 migrations/sqlite/000009_execution_waits.up.sql    |   39 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 .../sqlite/000013_node_run_response.down.sql       |    9 +
 migrations/sqlite/000013_node_run_response.up.sql  |   23 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 nodes/ai.go                                        | 3321 ++++++++++-
 nodes/ai_mcp_test.go                               |  502 ++
 nodes/ai_ollama_test.go                            |  413 ++
 nodes/ai_test.go                                   | 2241 ++++++-
 nodes/ai_tools_test.go                             |  519 ++
 nodes/annotation.go                                |    9 +-
 nodes/apostrophe_live_test.go                      |   43 +
 nodes/assignments.go                               |  179 +
 nodes/bindings_test.go                             |  136 +
 nodes/code.go                                      |  112 +-
 nodes/code_test.go                                 |  157 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  213 +-
 nodes/database.go                                  |  339 +-
 nodes/database_test.go                             |  765 ++-
 nodes/datastore.go                                 | 1286 ++++
 nodes/datastore_increment.go                       |   85 +
 nodes/datastore_test.go                            |  857 +++
 nodes/datastore_tool_test.go                       |  758 +++
 nodes/datetime.go                                  |  476 ++
 nodes/datetime_test.go                             |  404 ++
 nodes/embedscope.go                                |  217 +
 nodes/embedscope_test.go                           |  288 +
 nodes/error_workflow.go                            |  227 +
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |  707 ++-
 nodes/executors_test.go                            |  546 ++
 nodes/flow.go                                      |  457 ++
 nodes/flow_test.go                                 |  464 ++
 nodes/http.go                                      |  546 +-
 nodes/http_test.go                                 |  562 +-
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/loop.go                                      |  235 +
 nodes/mysql_v2.go                                  |  199 +
 nodes/mysql_v2_test.go                             |  181 +
 nodes/pgvector.go                                  | 1236 ++++
 nodes/pgvector_test.go                             |  517 ++
 nodes/postgres_v2.go                               |  633 ++
 nodes/postgres_v2_test.go                          |  231 +
 nodes/presentation_test.go                         |   95 +
 nodes/routing.go                                   |   23 +
 nodes/sql_options.go                               |  569 ++
 nodes/sql_options_live_test.go                     |  632 ++
 nodes/sql_options_test.go                          |  257 +
 nodes/sqlite_attach_test.go                        |  161 +
 nodes/subworkflow.go                               |  427 ++
 nodes/subworkflow_calls_test.go                    |   90 +
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  244 +
 nodes/telegram_lifecycle.go                        |  428 ++
 nodes/telegram_test.go                             |  610 ++
 nodes/testdata/n8n_chat_model_options.json         |   38 +
 nodes/testdata/n8n_sql_options.json                |   17 +
 nodes/transform.go                                 |  767 +++
 nodes/transform_test.go                            |  315 +
 nodes/unsupported.go                               |   26 +-
 nodes/wait.go                                      |  383 ++
 nodes/webhook.go                                   |  758 ++-
 packs/gowa/GAPS.md                                 |   42 +
 packs/gowa/README.md                               |    7 +
 packs/telegram/README.md                           |   40 +
 packs/telegram/pack.json                           | 1119 ++++
 packs/telegram/telegram.go                         |   58 +
 packs/telegram/telegram_test.go                    |  466 ++
 packs/waha/README.md                               |   54 +
 packs/waha/REPORT-202409.md                        |  100 +
 packs/waha/REPORT-202502.md                        |  128 +
 packs/waha/manifest-202409.json                    |  115 +
 packs/waha/manifest-202502.json                    |  115 +
 packs/waha/pack-202409.json                        | 2794 +++++++++
 packs/waha/pack-202502.json                        | 3844 ++++++++++++
 packs/waha/pack-trigger-202409.json                |  115 +
 packs/waha/pack-trigger-202502.json                |  121 +
 packs/waha/waha.go                                 |  188 +
 packs/waha/waha_test.go                            | 1513 +++++
 packs/waha/webhook-lifecycle.json                  |   14 +
 pkg/sdk/.gitkeep                                   |    0
 pkg/sdk/doc.go                                     |   49 +
 pkg/sdk/example/echo/main.go                       |   35 +
 pkg/sdk/sdk.go                                     |   75 +
 pkg/sdk/sdk_test.go                                |   80 +
 pkg/sdk/wasm_exec_test.go                          |   86 +
 scripts/check-coordinates.sh                       |   87 +
 scripts/config-reference.go                        |  402 ++
 scripts/config-reference_test.go                   |   87 +
 scripts/docker-tags.sh                             |   84 +
 scripts/e2e-stub.mjs                               |   50 +
 scripts/generate-api-reference.mjs                 |  427 ++
 scripts/smoke-cli.sh                               |  228 +
 scripts/smoke-dev.sh                               |   27 +
 scripts/smoke-docker.sh                            |   13 +-
 scripts/smoke-postgres.sh                          |   65 +-
 sdk/CHANGELOG.md                                   |   45 +
 sdk/LICENSE                                        |  202 +
 sdk/README.md                                      |  306 +-
 sdk/RELEASING.md                                   |  188 +
 sdk/examples/host-page/README.md                   |   61 +-
 sdk/examples/host-page/index.html                  |   31 +-
 sdk/examples/host-page/package.json                |   13 +
 sdk/examples/host-page/server.mjs                  |   87 +-
 sdk/examples/reference-host/README.md              |  144 +
 sdk/examples/reference-host/package.json           |   13 +
 sdk/examples/reference-host/server.mjs             |  410 ++
 sdk/examples/reference-host/tenant.html            |  101 +
 sdk/package.json                                   |   29 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 +
 sdk/scripts/check-package.mjs                      |  315 +
 sdk/scripts/dump-openapi.mjs                       |   13 +
 sdk/scripts/lib/pack.mjs                           |   77 +
 sdk/scripts/lib/release.mjs                        |  266 +
 sdk/scripts/release.mjs                            |  149 +
 sdk/src/browser.ts                                 |  147 +-
 sdk/src/generated/models.ts                        | 4476 ++++++++++++--
 sdk/src/http.ts                                    |   90 +-
 sdk/src/server.ts                                  | 1007 +++-
 sdk/src/version.ts                                 |   15 +-
 sdk/test/browser.test.ts                           |  140 +
 sdk/test/datastore-live.test.mjs                   |  100 +
 sdk/test/operation-coverage.test.mjs               |  197 +
 sdk/test/operations.test.ts                        |  251 +
 sdk/test/release-workflow.test.mjs                 |  223 +
 sdk/test/release.test.mjs                          |  390 ++
 sdk/test/server.test.ts                            |  568 +-
 sdk/test/version.test.mjs                          |   40 +
 sidecar/doc.go                                     |   49 +
 sidecar/fixture/echo.js                            |   74 +
 sidecar/fixture_test.go                            |   98 +
 sidecar/protocol.go                                |   98 +
 sidecar/sidecar.go                                 |  494 ++
 sidecar/sidecar_test.go                            |  433 ++
 web/package.json                                   |    1 +
 web/pnpm-lock.yaml                                 |   15 +
 web/src/app.css                                    |   33 +-
 web/src/lib/api/generated/admin/admin.ts           | 1051 ++++
 web/src/lib/api/generated/auth/auth.ts             |  760 +++
 .../lib/api/generated/credentials/credentials.ts   |  229 +-
 .../datastore-columns/datastore-columns.ts         |  363 ++
 .../api/generated/datastore-rows/datastore-rows.ts | 1004 ++++
 web/src/lib/api/generated/datastores/datastores.ts |  659 +++
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 web/src/lib/api/generated/models/aPIKeyResource.ts |   20 +
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 web/src/lib/api/generated/models/cSVImportIssue.ts |   14 +
 .../lib/api/generated/models/cSVImportReport.ts    |   17 +
 .../generated/models/clearedDatastoreOutputBody.ts |   13 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   17 +
 .../generated/models/createDatastoreInputBody.ts   |   23 +
 .../models/createStreamTicketInputBody.ts          |   17 +
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../api/generated/models/createdAPIKeyResource.ts  |   18 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 .../api/generated/models/datastoreColumnInput.ts   |   22 +
 .../generated/models/datastoreColumnResource.ts    |   14 +
 .../generated/models/datastoreListOutputBody.ts    |   15 +
 .../lib/api/generated/models/datastoreResource.ts  |   17 +
 web/src/lib/api/generated/models/definition.ts     |   17 +
 .../api/generated/models/deleteRowsInputBody.ts    |   17 +
 .../api/generated/models/deleteRowsOutputBody.ts   |   16 +
 .../models/deleteRowsOutputBodyRowsItem.ts         |    9 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionNodeRunResource.ts   |    3 +
 .../generated/models/executionRequestResource.ts   |    1 +
 .../lib/api/generated/models/executionResource.ts  |    4 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../api/generated/models/executionWaitingEvent.ts  |   24 +
 .../generated/models/exportDatastoreRowsParams.ts  |   14 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 web/src/lib/api/generated/models/filter.ts         |   14 +
 .../lib/api/generated/models/filterCondition.ts    |   13 +
 .../lib/api/generated/models/getDatastoreRow200.ts |    9 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 .../api/generated/models/incrementRowsInputBody.ts |   22 +
 .../generated/models/incrementRowsOutputBody.ts    |   16 +
 .../models/incrementRowsOutputBodyRowsItem.ts      |    9 +
 web/src/lib/api/generated/models/index.ts          |   97 +
 .../api/generated/models/insertDatastoreRow201.ts  |    9 +
 .../lib/api/generated/models/insertRowInputBody.ts |   15 +
 .../generated/models/insertRowInputBodyValues.ts   |   12 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../generated/models/listDatastoreRowsParams.ts    |   37 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../generated/models/listWorkflowVersionsParams.ts |   20 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 .../api/generated/models/loadOptionsInputBody.ts   |   22 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/loginInputBody.ts |   24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/principalResource.ts  |   24 +
 .../lib/api/generated/models/propertyDefinition.ts |   19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/publishVersionInputBody.ts    |   17 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../models/renameDatastoreColumnInputBody.ts       |   17 +
 .../generated/models/renameDatastoreInputBody.ts   |   17 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../lib/api/generated/models/rowListOutputBody.ts  |   16 +
 .../generated/models/rowListOutputBodyItemsItem.ts |    9 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 .../models/streamExecutionEvents200Item.ts         |    9 +
 .../api/generated/models/streamTicketResource.ts   |   16 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 .../api/generated/models/updateRowsInputBody.ts    |   20 +
 .../generated/models/updateRowsInputBodyValues.ts  |   12 +
 .../api/generated/models/updateRowsOutputBody.ts   |   16 +
 .../models/updateRowsOutputBodyRowsItem.ts         |    9 +
 .../lib/api/generated/models/upsertRowInputBody.ts |   18 +
 .../generated/models/upsertRowInputBodyValues.ts   |   12 +
 .../api/generated/models/upsertRowOutputBody.ts    |   17 +
 .../models/upsertRowOutputBodyRowsItem.ts          |    9 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 .../models/workflowPublishEventResource.ts         |   19 +
 .../models/workflowPublishEventResourceAction.ts   |   16 +
 .../models/workflowVersionListResource.ts          |   17 +
 .../models/workflowVersionSummaryResource.ts       |   23 +
 web/src/lib/api/generated/nodes/nodes.ts           |  445 +-
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |  319 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  242 +-
 web/src/lib/api/http.test.ts                       |   79 +-
 web/src/lib/api/http.ts                            |   72 +
 .../lib/components/dashboard/dashboard-nav.svelte  |   27 +-
 .../lib/components/dashboard/list-states.svelte    |  136 +
 .../components/dashboard/synced-checkbox.svelte    |   44 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 web/src/lib/components/ui/table/index.ts           |   28 +
 web/src/lib/components/ui/table/table-body.svelte  |   15 +
 .../lib/components/ui/table/table-caption.svelte   |   20 +
 web/src/lib/components/ui/table/table-cell.svelte  |   15 +
 .../lib/components/ui/table/table-footer.svelte    |   20 +
 web/src/lib/components/ui/table/table-head.svelte  |   15 +
 .../lib/components/ui/table/table-header.svelte    |   20 +
 web/src/lib/components/ui/table/table-row.svelte   |   15 +
 web/src/lib/components/ui/table/table.svelte       |   17 +
 .../workflow-editor/activation-notices.svelte      |  120 +
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  395 +-
 .../workflow-editor/editor-controls.svelte         |   44 +
 .../workflow-editor/execution-canvas-node.svelte   |   69 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |  144 +-
 .../workflow-editor/properties-panel.svelte        |  220 +-
 .../workflow-editor/property-field.svelte          |  984 +++-
 .../workflow-editor/property-field.test.ts         |   81 +
 .../workflow-editor/version-panel.svelte           |  557 ++
 .../workflow-editor/workflow-editor.svelte         | 1077 +++-
 web/src/lib/dashboard/cursor-page.test.ts          |  437 ++
 web/src/lib/dashboard/cursor-page.ts               |  203 +
 web/src/lib/dashboard/execution-list.test.ts       |  248 +
 web/src/lib/dashboard/execution-list.ts            |  200 +
 web/src/lib/dashboard/list-state.test.ts           |   65 +
 web/src/lib/dashboard/list-state.ts                |   69 +
 web/src/lib/dashboard/nav-sections.test.ts         |   39 +
 web/src/lib/dashboard/request-guard.test.ts        |   55 +
 web/src/lib/dashboard/request-guard.ts             |   44 +
 web/src/lib/dashboard/workflow-list.test.ts        |  119 +
 web/src/lib/dashboard/workflow-list.ts             |  135 +
 web/src/lib/datastore/columns.test.ts              |  138 +
 web/src/lib/datastore/columns.ts                   |  120 +
 web/src/lib/datastore/transfer.test.ts             |   67 +
 web/src/lib/datastore/transfer.ts                  |  102 +
 web/src/lib/embed/embed-editor.svelte              |  225 +-
 web/src/lib/embed/session.svelte.ts                |   95 +-
 web/src/lib/embed/session.test.ts                  |   99 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/activation.test.ts     |  130 +
 web/src/lib/workflow-editor/activation.ts          |  108 +
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 +
 web/src/lib/workflow-editor/clipboard.ts           |  293 +
 web/src/lib/workflow-editor/collection.test.ts     |  131 +
 web/src/lib/workflow-editor/collection.ts          |  148 +
 web/src/lib/workflow-editor/conditions.test.ts     |  134 +
 web/src/lib/workflow-editor/conditions.ts          |  220 +
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   66 +-
 web/src/lib/workflow-editor/document.test.ts       |  182 +-
 web/src/lib/workflow-editor/document.ts            |  320 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   29 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   23 +-
 web/src/lib/workflow-editor/execution.test.ts      |   88 +-
 web/src/lib/workflow-editor/execution.ts           |   84 +-
 .../lib/workflow-editor/expression-assist.test.ts  |  103 +
 web/src/lib/workflow-editor/expression-assist.ts   |  141 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   92 +
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |  273 +
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  275 +
 web/src/lib/workflow-editor/layout.ts              |  319 +
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.test.ts    |  213 +-
 web/src/lib/workflow-editor/node-visual.ts         |  298 +-
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  337 +-
 web/src/lib/workflow-editor/ports.ts               |  157 +-
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |  109 +
 web/src/lib/workflow-editor/shortcuts.ts           |  139 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 .../lib/workflow-editor/version-history.test.ts    |  191 +
 web/src/lib/workflow-editor/version-history.ts     |  156 +
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  207 +
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |  100 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  530 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  494 +-
 .../app/workflows/[id]/export-dialog.svelte        |  120 +
 .../app/workflows/diagnostics-section.svelte       |  120 +
 .../(dashboard)/app/workflows/import-dialog.svelte |  226 +
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  142 +
 .../routes/(dashboard)/credentials/+page.svelte    |  239 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |  232 +
 .../(dashboard)/datastores/[id]/+page.svelte       |  960 +++
 web/src/routes/(dashboard)/executions/+page.svelte |  446 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  250 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |  180 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  317 +-
 web/src/routes/+page.svelte                        |    8 +-
 web/src/routes/approve/[token]/+page.svelte        |  173 +
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 1429 files changed, 342258 insertions(+), 5513 deletions(-)
```
