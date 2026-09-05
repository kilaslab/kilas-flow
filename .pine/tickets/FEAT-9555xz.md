---
id: FEAT-9555xz
title: Run executions across worker processes
status: todo
priority: low
labels:
    - platform
    - longtail
deps:
    - FEAT-gjzgkd
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:17:54Z"
updated: "2026-09-05T05:17:54Z"
---

## Scope

KilasFlow scales to one process. `cmd/kilasflow/main.go` calls `runtime.Start(ctx, cfg.Execution.MaxConcurrent)`, and `Service.Start` in `internal/engine/service.go` describes exactly what it does: "a bounded process-local worker pool". API, workers, scheduler and webhook handler all live in one binary, so a workload that needs more execution capacity has to be given a bigger machine. PRD §4 put "distributed workflow execution" and "Kubernetes-native worker scaling" outside V1, and §64 left open "whether distributed workers use NATS, Redis Streams, or PostgreSQL".

That question is already answered, and the answer is that no broker is needed. `GORMExecutionStore.ClaimNext` is a correct lease-based durable queue: it selects a queued record or one whose lease has expired, claims it with a conditional `UPDATE` inside a transaction, and stamps a `lease_owner` of `workerID + "/" + leaseID` with a freshly generated lease id. Every subsequent write is fenced on that owner — `UpdateRuntime` and `CreateNodeRun` both carry `AND lease_owner = ?` — so a worker whose lease was reclaimed cannot write over its successor. The durable queue is the source of truth, and p6-4 sharpens it with `FOR UPDATE SKIP LOCKED` and `LISTEN/NOTIFY`. This ticket does not replace any of that; it makes a second process able to participate.

Three things are process-local and each one breaks in a way that looks like the feature works. `Service.Wake()` sends on an in-process `chan struct{}`, so a webhook received by the API process cannot wake a worker in another process — that worker finds the job on its next 100 ms poll in `Service.worker`. `internal/events.Broker` is in-process, so an execution running on worker B publishes its live events only into B's broker and a browser connected to API process A sees an empty stream. And `Service.Cancel` looks up `service.active[executionID]` in the local map: for a run on another process it finds nothing, and nothing else stops the work — `runOnce` inspects `StatusCancelling` only at claim time, and `UpdateRuntime`'s `CASE WHEN status = 'cancelling'` expression relabels the record as cancelled only once the run has finished on its own. Cancelling a remote execution today does not cancel anything; it renames the outcome.

One thing already works and must not be broken while fixing the rest: `Handler.await` in `internal/webhook/webhook.go` polls the durable execution record rather than waiting on an in-process signal, so a synchronous webhook response survives the run happening in a different process.

## Acceptance criteria

- [ ] One binary, selectable role: a process runs the API only, workers only, or both, and the default behaviour of an existing deployment is unchanged.
- [ ] Two worker processes against one PostgreSQL database claim disjoint executions under sustained load. No execution runs twice and none is left stranded.
- [ ] The scheduler is active in exactly one process at a time; starting a second process does not double-fire a cron schedule.
- [ ] A newly queued execution is picked up by a worker in another process without waiting out the poll interval.
- [ ] An execution running in a worker process streams its live node events to a browser connected to a different API process.
- [ ] Cancelling an execution that is running in another process stops the work in flight, rather than relabelling the record after it finishes.
- [ ] A worker process killed mid-run has its execution reclaimed and completed by another worker, with no duplicate node runs and no write accepted from the dead worker's lease.
- [ ] A multi-process configuration on a driver that cannot support it is refused at startup with an explanation, not left to fail under load.

## Implementation Plan

Add the role first, because it is the cheapest thing to get wrong later. Put it inside the existing `Execution` config section rather than inventing a new top-level one, and default it to today's behaviour so an upgrade is a no-op. `cmd/kilasflow/main.go` then decides whether to call `runtime.Start`, whether to start `cronService`, and whether to mount the HTTP listener at all.

Fix worker identity next. `WorkerID` is `fmt.Sprintf("kilasflow-%d", os.Getpid())`, so two containers on different hosts can present the same worker id. This is not a correctness bug — `ClaimNext` appends a freshly generated lease id to build `leaseOwner`, so a colliding worker id can never cross-fence another's writes — but every log line and every future "which worker ran this" answer is wrong. Make it host-qualified and unique.

Then the three process-local pieces, in order of how badly they hurt.

Cancellation is the correctness one, and it is broken even before multi-process work: nothing checks the record's status between nodes. Add that check to the runner's node loop — it is the floor, because it depends on nothing but the database. Then layer the low-latency path on top of it, using p6-4's `LISTEN/NOTIFY` to interrupt the process actually holding the lease. Notification delivery is best-effort, so the polled check has to stay even once the notification works.

Wake is the same channel with a different payload: a queue write publishes a notification, and every worker process treats it exactly as it treats the local `wake` today. Keep the 100 ms fallback in `Service.worker` — without it a dropped notification stalls a queue indefinitely.

Events are the one with a real design choice. Options: fan out over the notification channel, persist events and have the SSE handler tail the database, or accept that live streaming works only when the API process also runs workers. Take the fan-out, and carry identifiers only. NOTIFY payloads cap at roughly 8000 bytes, and `events.Event` carries the node's whole output in `Data`, which will exceed that on any real workflow — publish `{tenant, execution, node, sequence, type, status}` and let the receiving API process read the durable node run it names.

Two traps beyond those. SQLite cannot host this: one writer, no `LISTEN/NOTIFY`, and a shared file across containers is a corruption story rather than a scaling one. A `worker` or split-role configuration on the SQLite driver must be refused at startup. And `database.max_open_conns` defaults to **1** — `internal/config/config.go` sets it for every driver — while `execution.max_concurrent` defaults to 10, so a worker process would serialise ten concurrent executions through a single connection. p6-3 owns that default; if it has not landed, this ticket cannot claim a throughput result without fixing it.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p8, V2-p8-7, and section p6, V2-p6-3 and V2-p6-4 (connection pool default, `FOR UPDATE SKIP LOCKED`, `LISTEN/NOTIFY`, the ~8000-byte NOTIFY payload cap, prefix-keyed advisory locks).
- PRD: `gflow-prd-v1.md` §4 ("distributed workflow execution", "Kubernetes-native worker scaling" out of scope for V1), §35 Workflow Execution Model, §64 ("whether distributed workers use NATS, Redis Streams, or PostgreSQL").
- Code: `internal/engine/service.go` (`Start`, `worker`, `Wake`, `runOnce`, `Cancel`, `active`), `cmd/kilasflow/main.go` (`WorkerID`, `runtime.Start`, `cronService.Start`), `internal/repository/executions.go` (`ClaimNext`, the `lease_owner` fencing in `UpdateRuntime` and `CreateNodeRun`), `internal/events/events.go` (in-process `Broker`), `internal/scheduler/scheduler.go` (the "runs due schedules in a single process" note and the transactional `ClaimDue`), `internal/webhook/webhook.go` (`await`), `internal/config/config.go` (`Database.MaxOpenConns` default 1, `Execution.MaxConcurrent` default 10).
