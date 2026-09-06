---
id: FEAT-gjzgkd
title: Claim work with SKIP LOCKED and wake workers over LISTEN/NOTIFY
status: doing
priority: medium
labels:
    - persistence
    - postgres
    - tier
deps:
    - FEAT-r6xhnp
    - FEAT-5fv8gf
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T05:02:14Z"
updated: "2026-09-06T04:17:56Z"
---

## Scope

`GORMExecutionStore.ClaimNext` in `internal/repository/executions.go` is already a correct lease-based durable queue. Inside one transaction it selects the oldest claimable row, then re-applies the whole predicate in a conditional `UPDATE` and treats `RowsAffected == 0` as "another worker got there first"; on a reclaimed expired lease it clears the partial `execution_node_runs` trace before handing the work over. Do not replace it with a queue library. River in particular would install seven or more tables of its own that carry no table prefix, which contradicts the shared-database decision in p6-2 outright, and it would become a second source of truth next to `executions`. What the queue lacks is not correctness but behaviour under contention and behaviour when idle.

Under contention, all `execution.max_concurrent` workers — ten by default — select the *same* oldest row. Nine lose the conditional update, return "nothing claimed", and immediately loop. Throughput is fine and the wasted work grows linearly with the pool. `FOR UPDATE SKIP LOCKED` makes each worker land on a different row instead of racing for one.

When idle, a worker waits on `service.wake` or 100 ms, whichever comes first (`internal/engine/service.go`). `Wake()` sends into a buffered channel of capacity 1 and drops the send if it is full; its callers are `internal/api/handlers/workflows.go` and two paths inside the engine. All of that is process-local. In a multi-process deployment — which is the only reason to be on PostgreSQL at all — an execution queued by one process is invisible to the others until their next 100 ms tick, and that tick is precisely the constant polling p6-3 has to add an index for. PostgreSQL `LISTEN/NOTIFY` turns the tick into a push and leaves the tick as the fallback.

The trap is SQLite. `glebarez/sqlite` v1.11.0 registers a `"FOR"` clause builder that returns without writing anything when the expression is a `clause.Locking` — its comment reads "SQLite3 does not support row-level locking". The clause is dropped silently, with no error and no warning. `GORMScheduleStore.ClaimDue` already depends on this without saying so: it passes `clause.Locking{Strength: "UPDATE"}` and gets a plain `SELECT` on the default driver. So on SQLite, correctness rests entirely on the conditional-`UPDATE` compare-and-swap, and any test that asserts skip-locked behaviour has to run against PostgreSQL or it asserts nothing.

## Acceptance criteria

- [ ] `ClaimNext` issues `FOR UPDATE SKIP LOCKED` on PostgreSQL, and ten concurrent workers claim ten distinct queued executions with no lost update and no wasted retry.
- [ ] The same code path stays correct on SQLite with the locking clause silently dropped: a concurrency test proves each queued execution is claimed exactly once.
- [ ] On PostgreSQL, queuing an execution in one process wakes an idle worker in another process without waiting for its poll interval.
- [ ] The notification payload carries identifiers only, stays far inside PostgreSQL's roughly 8000-byte payload limit, and a woken worker re-reads the row rather than trusting what the payload said.
- [ ] Losing the listener connection is neither silent nor fatal: it reconnects, and the 100 ms poll remains the fallback so a dropped notification costs latency and never a stuck execution.
- [ ] The notification channel name is derived from the configured table prefix, so two prefixed installs sharing one database do not wake each other's workers.
- [ ] If a PostgreSQL advisory lock is introduced to elect a single scheduler, its key is derived from the same prefix, because advisory locks are per-database and not per-schema.
- [ ] The scheduler's `ClaimDue` locking behaviour on SQLite is documented where a reader will find it, since its `FOR UPDATE` has never actually reached the database there.

## Implementation Plan

The claim change is small: add `clause.Locking{Strength: "UPDATE", Options: clause.LockingOptionsSkipLocked}` to the candidate select in `ClaimNext`. Keep the conditional `UPDATE` exactly as it is — it is what makes the operation correct on SQLite and it costs nothing on PostgreSQL. Keep `Order("started_at ASC, id ASC")` too: with SKIP LOCKED, dropping the order would fan workers out over an unordered heap and lose FIFO. Note that `First` appends `LIMIT 1` and a primary-key ordering of its own, which is the shape you want here.

For the wake path, notify from the same places that already call `Wake()`. The GORM PostgreSQL driver sits on pgx v5, already a direct dependency, so the listener needs a connection outside the pool — a pooled connection cannot be parked on `LISTEN`, and taking one out of a pool sized by p6-3 would quietly cost a worker a connection. Recommend a small listener type in `internal/database` owning its own `pgx.Conn` built from the same DSN, feeding the existing `service.Wake()` so the engine's worker loop keeps its current shape and the fallback tick keeps working unchanged.

One decision to make deliberately: `service.wake` is capacity 1 and `Wake()` drops the send when full, so exactly one worker wakes per notification. That is right for a single queued execution and wrong for a burst. The options are to widen the channel to `maxConcurrent`, or to leave it and let the 100 ms tick absorb the remainder. Recommend leaving it: the fallback already bounds the latency, and a wider channel means every burst wakes every worker to contend over the same rows, which is the problem SKIP LOCKED was added to remove.

Scheduler election is optional in this ticket and worth stating either way. Today `cronService.Start(ctx)` runs in every process, and `ClaimDue` holds a row lock while it advances `next_run_at`, so on PostgreSQL a due time still fires once even with several schedulers running. If a single-scheduler election is added anyway, use `pg_advisory_lock` keyed by a hash of the table prefix plus a fixed namespace, and say in the code why the prefix is in the key.

## References

- Roadmap plan, p6 section, entry V2-p6-4: `.pine/roadmap.md`.
- `internal/repository/executions.go` — `ClaimNext`, the select-then-conditional-update claim.
- `internal/repository/schedules.go` — `ClaimDue` and its `clause.Locking{Strength: "UPDATE"}`.
- `internal/engine/service.go` — the worker loop, the capacity-1 `wake` channel, and `Wake()`.
- `internal/api/handlers/workflows.go` — the API-side `Wake()` caller.
- `github.com/glebarez/sqlite@v1.11.0/sqlite.go` — the `"FOR"` clause builder that drops `clause.Locking`.
- `gorm.io/gorm@v1.31.2/clause/locking.go` — `LockingOptionsSkipLocked`.
- `go.mod` — `github.com/jackc/pgx/v5 v5.10.0` is already a direct dependency.

## Work evidence — ClaimTier (Batch-4, 2026-09-06)

Ownership kept to `internal/repository/*`, no config change: the listener
needs only the DSN and the table prefix, both already in `config.Database`,
so no LISTEN key was added. The ticket's plan put the listener in
`internal/database`; it lives in `internal/repository/wake.go` instead
because Batch-4 gives ClaimTier the repository only — same shape (own
`pgx.Conn` outside the pool, feeds the engine's existing `Wake`), one
package. Engine/API wiring (`WatchExecutions` -> `service.Wake()`) is left
for the integrator: no engine file is in this agent's ownership.

Done:
- `ClaimNext` candidate select carries
  `clause.Locking{Strength: "UPDATE", Options: clause.LockingOptionsSkipLocked}`;
  conditional UPDATE, `Order("started_at ASC, id ASC")` and `First` untouched.
  SQLite drops the clause silently (proven by test); the compare-and-set stays
  the correctness mechanism there.
- `notifyExecutionQueued` runs `SELECT pg_notify(channel, payload)` inside the
  enqueue transaction (fires on commit, never on rollback) from
  `QueueManualLatest`, `QueueTriggered` and `Create`; no-op off postgres.
  Channel = `ExecutionWakeChannel(tablePrefix)` (`<prefix>execution_wake`);
  payload = tenant + execution IDs only. Channel travels as a pg_notify
  argument (no quoting surface); LISTEN uses `pgx.Identifier` sanitize.
- `WatchExecutions(ctx, dsn, prefix, onWake, onError)`: dedicated conn,
  reconnect with backoff (1s doubling, 30s cap), every drop reported to
  onError, malformed payload drops the notification never the listener,
  nil error on ctx stop. Capacity-1 wake semantics left as the ticket
  recommends; the 100 ms tick stays the fallback.
- `ClaimDue` doc comment now states its FOR UPDATE never reaches SQLite.
- No advisory-lock election introduced: `ClaimDue`'s row lock already fires
  each due time once with several schedulers, so the conditional criterion
  is met by absence, stated here. If election is added later, key it by the
  table prefix per `ExecutionWakeChannel`'s doc comment.
- `-p 1` shared-server rule commented at the top of `claim_wake_test.go`.

Proof (`go test ./internal/repository/`, green on sqlite always; PG halves
green against pgvector/pgvector:pg16 with `KILASFLOW_TEST_POSTGRES_DSN` set;
note the stock `kf-pg` container has no `vector` extension so 000006 fails
there — a `kf-pg-vector` container on host port 55434 was started for this):
- `TestClaimSelectLocksSkippedRowsOnPostgres` (DryRun rendering),
  `TestClaimSelectDropsTheLockOnSQLite`, `TestClaimSelectPlanLocksRowsOnPostgres`
  (EXPLAIN of the exact sent statement incl. First's appended `"executions"."id"`
  ordering shows a LockRows node).
- `TestConcurrentWorkersClaimEachExecutionExactlyOnce` (sqlite, 10x10).
- `TestTenWorkersClaimTenDistinctExecutionsOnPostgres` (10x10 distinct).
- `TestQueuedExecutionWakesAListenerOnPostgres`: push wake, payload names a
  real queued row, worker re-reads via ClaimNext; measured queue-to-wake
  ~65us against the 100ms tick. Retry-loop instead of a setup sleep because
  the first queue can race the listener's LISTEN.
- `TestExecutionWakeChannelKeysTheTablePrefix`, payload size/shape +
  malformed-payload unit tests, `TestWatchExecutionsReportsDropsAndStopsWithContext`
  (unreachable DSN: drops reported, nil on ctx stop).

No other package touched. `go.mod`/`go.sum` unchanged (pgx was already direct).
Full package suite could not go green in-tree at handoff: sibling
`credentials_external_test.go` (LongtailSecrets, mid-flight) does not compile;
verified green in a scratch copy minus that file. Pre-existing tests
unaffected (same run).
