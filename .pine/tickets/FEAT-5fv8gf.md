---
id: FEAT-5fv8gf
title: 'Make the PostgreSQL tier real: pooling, indexes and retention'
status: todo
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
updated: "2026-09-05T05:01:40Z"
---

## Scope

"PostgreSQL unlocks concurrency" is currently untrue. `config.Default()` in `internal/config/config.go` sets `MaxOpenConns: 1` and `MaxIdleConns: 1` for every driver, while `Execution.MaxConcurrent` defaults to 10. `internal/database/database.go` pins SQLite to a single connection on purpose and otherwise passes the configured numbers straight through, so a PostgreSQL install that never writes a `config.yaml` runs ten execution workers, a scheduler, the webhook handler and every API request through one connection. `config.example.yaml` advertises `max_open_conns: 10`, which is not the default — the documented value and the real value disagree, and the documented one is the one people believe.

The second gap is the index the queue depends on. `GORMExecutionStore.ClaimNext` selects on `status = 'queued' OR ((status = 'running' OR status = 'cancelling') AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?)`, ordered by `started_at ASC, id ASC`, with no tenant predicate. `executionModel` indexes `(tenant_id, started_at)`, `(tenant_id, workflow_id)`, `workflow_version_id`, `lease_owner`, `lease_expires_at` and `cancellation_requested_at` — and never `status`. The worker loop in `internal/engine/service.go` polls every 100 ms per idle worker, so a default install runs on the order of a hundred of these queries a second while doing nothing, and each one scans a table that only grows.

Which is the third gap: nothing ever deletes an execution. `ExecutionRepository` exposes `Create`, `QueueManualLatest`, `QueueTriggered`, `Get`, `List` and `CreateNodeRun` and no delete of any kind. Every execution stores `Input`, `Output` and `Error` payloads, and every node attempt writes an `execution_node_runs` row with its own three payloads. A busy tenant's database grows without bound, and there is no knob anywhere to stop it.

## Acceptance criteria

- [ ] The connection-pool default is derived from the driver and from `execution.max_concurrent` instead of being a flat 1; a PostgreSQL install with no config file runs its configured concurrency without queuing on the pool, and SQLite stays pinned at one connection.
- [ ] `config.example.yaml` and `config.Default()` agree on every pool value.
- [ ] `executions` carries an index that serves the `ClaimNext` predicate and ordering, added by a migration rather than a struct tag.
- [ ] The claim query's PostgreSQL plan uses that index rather than a sequential scan, captured as evidence against a table holding a large execution history.
- [ ] Execution retention is configurable, off by default, and deletes both `executions` rows and their `execution_node_runs` rows.
- [ ] Pruning never touches a queued, running or cancelling execution, works in bounded batches so it cannot hold a long transaction or a lock on the queue, and is safe with several KilasFlow processes running at once.
- [ ] Pruning an execution also removes its stored binary payloads, through `engine.Service.DiscardBinaries` — the store keys payloads by tenant and execution, and a pruner that deletes only the rows leaves the disk growing with no record of what is on it.
- [ ] Retention and the claim plan are both proven against a real PostgreSQL through `make smoke-postgres`, not only in unit tests.

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
