---
id: FEAT-5fv8gf
title: 'Make the PostgreSQL tier real: pooling, indexes and retention'
status: done
priority: medium
labels:
    - persistence
    - postgres
    - tier
deps:
    - FEAT-gvn62x
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T05:01:40Z"
updated: "2026-09-05T19:35:34Z"
---

## Scope

"PostgreSQL unlocks concurrency" is currently untrue. `config.Default()` in `internal/config/config.go` sets `MaxOpenConns: 1` and `MaxIdleConns: 1` for every driver, while `Execution.MaxConcurrent` defaults to 10. `internal/database/database.go` pins SQLite to a single connection on purpose and otherwise passes the configured numbers straight through, so a PostgreSQL install that never writes a `config.yaml` runs ten execution workers, a scheduler, the webhook handler and every API request through one connection. `config.example.yaml` advertises `max_open_conns: 10`, which is not the default — the documented value and the real value disagree, and the documented one is the one people believe.

The second gap is the index the queue depends on. `GORMExecutionStore.ClaimNext` selects on `status = 'queued' OR ((status = 'running' OR status = 'cancelling') AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)`, ordered by `started_at ASC, id ASC`, with no tenant predicate. `executionModel` indexes `(tenant_id, started_at)`, `(tenant_id, workflow_id)`, `workflow_version_id`, `lease_owner`, `lease_expires_at` and `cancellation_requested_at` — and never `status`. The worker loop in `internal/engine/service.go` polls every 100 ms per idle worker, so a default install runs on the order of a hundred of these queries a second while doing nothing, and each one scans a table that only grows.

Which is the third gap: nothing ever deletes an execution. `ExecutionRepository` exposes `Create`, `QueueManualLatest`, `QueueTriggered`, `Get`, `List` and `CreateNodeRun` and no delete of any kind. Every execution stores `Input`, `Output` and `Error` payloads, and every node attempt writes an `execution_node_runs` row with its own three payloads. A busy tenant's database grows without bound, and there is no knob anywhere to stop it.

## Acceptance criteria

- [x] The connection-pool default is derived from the driver and from `execution.max_concurrent` instead of being a flat 1; a PostgreSQL install with no config file runs its configured concurrency without queuing on the pool, and SQLite stays pinned at one connection.
- [x] `config.example.yaml` and `config.Default()` agree on every pool value.
- [x] `executions` carries an index that serves the `ClaimNext` predicate and ordering, added by a migration rather than a struct tag.
- [x] The claim query's PostgreSQL plan uses that index rather than a sequential scan, captured as evidence against a table holding a large execution history.
- [x] Execution retention is configurable, off by default, and deletes both `executions` rows and their `execution_node_runs` rows.
- [x] Pruning never touches a queued, running or cancelling execution, works in bounded batches so it cannot hold a long transaction or a lock on the queue, and is safe with several KilasFlow processes running at once.
- [x] Pruning an execution also removes its stored binary payloads, through `engine.Service.DiscardBinaries` — the store keys payloads by tenant and execution, and a pruner that deletes only the rows leaves the disk growing with no record of what is on it.
- [x] Retention and the claim plan are both proven against a real PostgreSQL through `make smoke-postgres`, not only in unit tests.

## Implementation Plan

Start with the pool, because it is three lines and it is the claim on the roadmap. Change the defaults in `internal/config/config.go`, but compute the driver-aware value after the file and environment layers have merged in `config.Load`, not inside `Default()` — an operator who sets only `execution.max_concurrent` must see the pool follow, and a value baked into `Default()` cannot. Recommend `max(4, execution.max_concurrent + headroom)` for PostgreSQL with the explicit key still winning when set, and leave `internal/database/database.go`'s SQLite branch exactly as it is: the single-writer pin and the WAL pragma set are a pair, and unpinning one without the other is how you get `SQLITE_BUSY` back.

For the index, the predicate has two arms and the ordering matters as much as the filter. Recommend one index on `(status, started_at, id)`: it serves the queued arm directly, serves the lease-expiry arm as a status-narrowed range, and stays dialect-neutral so SQLite gets it too. If the PostgreSQL plan still shows a scan on a large table, add a partial index restricted to the three non-terminal statuses in the PostgreSQL migration only — a partial index is the right tool here precisely because finished executions, which are almost all of them, are dead weight in the queue index. Do not add either as a struct tag; after p6-1 the migration is what exists.

Retention has one call it must not forget: `engine.Service.DiscardBinaries(tenantID, executionID)`, added by FEAT-0f87fn. It is a no-op when this server has no binary storage, so it is safe to call unconditionally, and it is the one function that exists precisely so the pruner does not have to reverse-engineer a directory tree.

Retention is the part with a trap in it. `executionNodeRunModel` declares its foreign key to `executions` with `constraint:OnUpdate:CASCADE,OnDelete:RESTRICT`, so deleting an execution while its node runs exist fails outright. Delete the node runs first, in the same transaction, batched by execution id. Run the pruner as a goroutine started from `cmd/kilasflow/main.go` alongside `cronService.Start(ctx)`, on a slow interval, selecting by `finished_at < now - retention` and never by `started_at` — an execution that is still running has no `finished_at`, which is exactly the row that must survive.

One decision to settle rather than leave open: whether retention is global or per tenant. Recommend a single global `execution.retention` knob now. Tenancy on the main API does not exist yet — every request resolves to `default` — so a per-tenant retention policy would have no authority to attach itself to. Revisit it when p8-1 gives tenants a real identity.

## References

- Roadmap plan, p6 section, entry V2-p6-3: `.pine/roadmap.md`.
- `internal/config/config.go` — `Default()`, `Database.MaxOpenConns`/`MaxIdleConns`, `Execution.MaxConcurrent`.
- `config.example.yaml` — the `database` and `execution` sections.
- `internal/database/database.go` — the SQLite pin and the pass-through for every other driver.
- `internal/repository/executions.go` — `ClaimNext` and the `ExecutionRepository` interface.
- `internal/repository/models.go` — `executionModel` indexes and `executionNodeRunModel`'s `OnDelete:RESTRICT` foreign key.
- `internal/engine/service.go` — the worker loop's 100 ms poll and `Wake`.
- `cmd/kilasflow/main.go` — where the scheduler is started, and where the pruner belongs.
- `scripts/smoke-postgres.sh`, `Makefile` target `smoke-postgres`.

## Work evidence

Every premise in the Scope section was re-checked against the tree and all of
them held: `Default()` really did set `MaxOpenConns: 1`/`MaxIdleConns: 1` for
every driver against a `MaxConcurrent` of 10, `config.example.yaml` really did
advertise `max_open_conns: 10`/`max_idle_conns: 5`, `executions` really carried
no index on `status`, and `ExecutionRepository` really had no delete of any
kind. The plan's recommendations were followed except for the partial index,
which the measurement below made unnecessary.

### The pool

`Database.PoolSize(maxConcurrent)` in `internal/config/config.go` derives the
pool, and `Load` calls it after the file and environment layers merge — not in
`Default()`, because koanf cannot tell a default of 1 apart from a file that
says 1, so a number baked into `Default()` could never follow
`execution.max_concurrent`. Zero on either key now means "derive". SQLite
derives to 1, matching the pin `database.Open` applies regardless; PostgreSQL
derives to `max_concurrent + 5`, floored at 4 and capped at 50 so a derived
default never exceeds PostgreSQL's stock `max_connections` of 100. Idle matches
open, so a sustained burst does not open a connection, use it once and destroy
it. `database.Open` now routes the non-SQLite branch through `PoolSize` too,
because `SetMaxOpenConns(0)` means *unlimited* and every caller that builds a
`config.Database` by hand was getting that.

`config.example.yaml` now comments both keys out and explains the derivation,
so the file and the defaults describe the same pool.
`TestTheExampleConfigAndTheDefaultsAgreeOnThePool` pins it and fails against the
old file — `config.example.yaml advertises max_open_conns 10 and the default is 1`.

### The index

`migrations/{sqlite,postgres}/000004_execution_indexes.{up,down}.sql` add
`idx_executions_status_started` on `(status, started_at, id)` and
`idx_executions_finished_at` on `(finished_at, id)`. Space-indented, both
dialects, both directions.

Measured on PostgreSQL 16 against a table of 500,003 executions (475,000
succeeded, 25,000 failed, 3 queued), using the statement GORM actually emits —
`First` appends a primary-key sort to the caller's `Order`, so the trailing
`, "executions"."id"` is part of the query the server sees.

Before:

```
 Limit  (cost=23869.64..23869.64 rows=1) (actual time=21.976..23.552 rows=1)
   Buffers: shared hit=11622 read=6566
   ->  Sort  (Sort Key: started_at, id)
         ->  Gather (Workers Launched: 2)
               ->  Parallel Seq Scan on executions
                     Rows Removed by Filter: 166667
 Execution Time: 23.583 ms
```

After:

```
 Limit  (cost=12.90..12.90 rows=1) (actual time=0.076..0.076 rows=1)
   Buffers: shared hit=10 read=3
   ->  Sort  (Sort Key: started_at, id)
         ->  Bitmap Heap Scan on executions   Heap Blocks: exact=1
               ->  BitmapOr
                     ->  Bitmap Index Scan on idx_executions_status_started
                           Index Cond: ((status)::text = 'queued'::text)
                     ->  Bitmap Index Scan on idx_executions_lease_expires_at
                           Index Cond: ((lease_expires_at IS NOT NULL) AND (lease_expires_at <= now()))
 Execution Time: 0.123 ms
```

23.583 ms and 18,188 buffers become 0.123 ms and 13 — a query each idle worker
runs ten times a second. **The partial index the plan offered as a fallback was
not added**: the planner already touches only the rows the three non-terminal
statuses cover, so a second index would cost a write on every execution and
save nothing.

The prune's candidate query is an index scan on `idx_executions_finished_at`
over the same table — 8 buffers, 0.111 ms, against 414,347 matching rows — so
the batch is bounded by the index rather than by sorting the whole history.

Both plans are now asserted executably, not only measured by hand:
`TestThePostgresClaimPlanUsesTheQueueIndexRatherThanASequentialScan` and
`TestThePostgresPrunePlanUsesTheFinishedAtIndex` seed 5,001 rows, `ANALYZE`,
and fail if the plan names `Seq Scan on executions` or omits the index. 5,001
is above the size at which the planner's own arithmetic flips: with the index
the plan is a bitmap scan, and with it dropped the same query at the same size
is `Seq Scan on executions (cost=0.00..199.52)`.

### Retention

`internal/repository/execution_retention.go` adds `ExecutionRetention` and
`GORMExecutionStore.PruneExpired`. Off by default (`execution.retention: 0`),
global rather than per tenant as the plan settled.

- Selects on `finished_at`, never `started_at`, and re-states both the age and
  the status predicates inside the `DELETE`. That second guard is load-bearing
  and is not redundant with the first: `Create` accepts a whole record, so a
  *running* execution can carry a `finished_at`, and on that row only the status
  test keeps it.
- Node runs are deleted first, in the same transaction — `execution_node_runs`'
  foreign key is `ON DELETE RESTRICT`, so the other order fails outright.
- Bounded batches, one transaction each, looping until the backlog is clear,
  with a context check between batches. A batch that removes nothing stops the
  sweep, so two processes sweeping the same database cannot spin.
- `discard` runs *before* the rows go, not after. The execution row is the only
  record of what that execution wrote to disk, so a failure between the two must
  leave a row the next sweep retries rather than a directory nothing points at.
- `cmd/kilasflow/main.go` starts `startExecutionPruner` beside the history
  sweeper, on a 15-minute tick, passing `runtime.DiscardBinaries`.

### Tests, and proof they fail without the change

Each was reverted, observed to fail, and restored.

| Reverted | Failure |
| --- | --- |
| migration 000004 | `migrating did not create the "idx_executions_status_started" index` |
| the `PoolSize` call in `Load` | `MaxOpenConns = 1 with 10 workers: the workers queue on the pool` |
| the old `config.example.yaml` values | `advertises max_open_conns 10 and the default is 1` |
| the status guard | `PruneExpired() = 1, want 0 — a running execution was deleted on age alone` |
| the node-run delete | `prune executions: constraint failed: FOREIGN KEY constraint failed (1811)` |
| the batch loop | `PruneExpired() = 2, want 5 — the sweep stopped after its first batch` |
| the `discard` call | `1 payload files survived the prune — the rows went and the disk did not` |
| the index, for the plan tests | `the claim plan still scans the executions table: Seq Scan on executions` |

### Gates

`gofmt -l .` clean, `go build ./...` clean, `go vet ./...` clean.

`go test ./... -count=1` green on SQLite (35 packages).

`go test ./... -count=1 -p 1` green against live PostgreSQL 16
(`KILASFLOW_TEST_POSTGRES_DSN` on a dedicated database). Every retention case
runs on both halves of `eachDriver`:

```
--- PASS: TestRetentionRemovesAFinishedExecutionWithItsNodeRuns/{sqlite,postgres}
--- PASS: TestRetentionKeepsAnExecutionInsideTheAgeBound/{sqlite,postgres}
--- PASS: TestRetentionOffDeletesNothing/{sqlite,postgres}
--- PASS: TestRetentionNeverRemovesAQueuedRunningOrCancellingExecution/{sqlite,postgres}
--- PASS: TestRetentionRefusesANonTerminalExecutionThatCarriesAFinishedAt/{sqlite,postgres}
--- PASS: TestRetentionClearsABacklogInBoundedBatches/{sqlite,postgres}
--- PASS: TestRetentionDiscardsTheStoredBinaryPayloads/{sqlite,postgres}
```

`scripts/smoke-postgres.sh` gains a second gated step running
`go test ./internal/repository -run 'Driver|Retention'` against the Compose
PostgreSQL; the plan assertions ride the existing `-run 'Postgres'` step. The
new step was run in the same `golang:1.27-alpine` container the script uses,
against a live server: `ok github.com/kilaslabs/kilas-flow/internal/repository`.

### Stale things found

- **`eachDriver`'s PostgreSQL cleanup never deleted anything.** It deleted from
  `node_runs`, which is not a table — it is `execution_node_runs` — and matched
  executions on `id LIKE 'drv_%'`, which no execution has, because a queued run
  is given a generated `exec_` ID. Both errors were discarded, so the
  `workflows` delete then failed on the executions foreign key and every row
  every run had ever written stayed on the shared server. Fixed to delete by
  tenant, in foreign-key order. Nothing failed until a test tried to count rows.
- **The PostgreSQL gate is not safe to run with parallel packages.**
  `internal/database`'s `openPostgres` drops every KilasFlow table on entry,
  while `internal/repository`'s `eachDriver` expects the schema to stay put;
  `go test ./...` runs those packages concurrently against one DSN. Observed
  directly — a `SELECT count(*) FROM executions` failed with `relation
  "executions" does not exist` seconds after `\dt` had listed it. Worked around
  here with `-p 1`; left as a real hazard for whoever wires this into CI.
