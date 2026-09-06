---
id: FEAT-9555xz
title: Run executions across worker processes
status: done
priority: low
labels:
    - platform
    - longtail
deps:
    - FEAT-gjzgkd
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:17:54Z"
updated: "2026-09-06T07:14:02Z"
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

- Roadmap plan, p8 section, entry V2-p8-7: `.pine/roadmap.md`.
- `.pine/roadmap.md` — p6 entries V2-p6-3 and V2-p6-4 (connection pool default, `FOR UPDATE SKIP LOCKED`, `LISTEN/NOTIFY`, the ~8000-byte NOTIFY payload cap, prefix-keyed advisory locks).
- PRD: `gflow-prd-v1.md` §4 ("distributed workflow execution", "Kubernetes-native worker scaling" out of scope for V1), §35 Workflow Execution Model, §64 ("whether distributed workers use NATS, Redis Streams, or PostgreSQL").
- Code: `internal/engine/service.go` (`Start`, `worker`, `Wake`, `runOnce`, `Cancel`, `active`), `cmd/kilasflow/main.go` (`WorkerID`, `runtime.Start`, `cronService.Start`), `internal/repository/executions.go` (`ClaimNext`, the `lease_owner` fencing in `UpdateRuntime` and `CreateNodeRun`), `internal/events/events.go` (in-process `Broker`), `internal/scheduler/scheduler.go` (the "runs due schedules in a single process" note and the transactional `ClaimDue`), `internal/webhook/webhook.go` (`await`), `internal/config/config.go` (`Database.MaxOpenConns` default 1, `Execution.MaxConcurrent` default 10).

## Work evidence — MultiProc (Batch-7 wave 1, 2026-09-06)

Ownership kept to the service loop, cmd worker flags, and the operate note;
no repository claim-path edits (ClaimTier's paths read-only).

Done:
- Worker identity is host-qualified and unique
  (`kilasflow-<host>-<pid>-<random>`, `unknown` when the hostname is
  unavailable, `/`/space sanitised, host capped at 48 chars so the fencing
  token stays inside `lease_owner`'s 128). `--worker-id` overrides it for
  operators who want stable names. Proof:
  `TestDefaultWorkerIDIsHostQualifiedAndUnique`,
  `TestResolveWorkerIDHonoursAnExplicitOverride`
  (`go test ./cmd/kilasflow/`, green).
- Graceful shutdown settles instead of stranding: post-run persistence in
  `runOnce` (suspend, node-run writes, both terminal writes) and the
  sub-workflow `persistChild` now use `context.WithoutCancel`, so SIGTERM
  mid-run still writes the terminal cancelled state and releases the lease.
  Before this, the terminal write rode the cancelled context and failed,
  leaving the execution running with its lease held until expiry (proven by
  the new test failing pre-fix, passing post-fix). `Start`'s doc comment
  now describes the shared durable queue rather than a process-local pool.
- Docs: `operate/deployment.md` gains "Multiple workers against one
  PostgreSQL database" (topology, distinct IDs, push-wake + 100 ms
  fallback, shutdown-settles/kill-reclaimed, clock-skew warning with the
  `default_timeout`-as-lease hazard, SQLite single-process refusal stated
  as docs) and the stale "tier work remains" paragraph now records
  wakeups + SKIP LOCKED as built in.

Proof (`go test ./internal/engine/ ./cmd/kilasflow/`, green; PG halves
green against kf-pg-vector:55434 with `KILASFLOW_TEST_POSTGRES_DSN` set,
skip cleanly without it; combined PG runs need `-p 1`):
- `TestTwoServicesClaimDisjointExecutionsOnPostgres`: 8 queued, two
  services x 2 workers, distinct IDs, all 8 succeed, echo calls == 8.
- `TestCrossProcessWakeReachesTheOtherWorkerOnPostgres`: both services on
  a 10 s tick, only B listens; queued via the store (the "other process"),
  success in ~0.1 s, far inside the tick; echo calls == 1.
- `TestGracefulShutdownHandsNothingHalfDone` (sqlite, always runs):
  context-aware executor held running, service context cancelled mid-run,
  execution settles cancelled with the lease released, and a second
  service's `RunOnce` finds nothing left to claim.

Not in this slice (left for the ticket's remaining boxes): selectable
API/worker role, single-scheduler election, cross-process event fan-out,
cross-process cancel interruption, kill-9 reclaim e2e, SQLite split-role
refusal at startup.

## Work evidence — MultiProc2 (Batch-8, 2026-09-06)

Ownership kept to the service loop + relay (`internal/engine`), worker flags
and role gating (`cmd/kilasflow`), and the operate note; no repository
claim-path edits (ClaimNext/UpdateRuntime/CreateNodeRun/ClaimDue untouched).
No new dependencies (pgx was already used by `repository/wake.go`).

Done, per acceptance box:
- Selectable role: `--role api|worker|both`, default `both` (today's
  behaviour: API + workers + scheduler in one process). `api` runs API +
  scheduler without workers; `worker`/`workers` runs workers with no API
  and no scheduler, waiting on SIGINT/SIGTERM instead of exiting. Proof:
  `TestParseProcessRoleDefaultsAndAcceptsThePlural`,
  `TestProcessRoleMatrixMatchesTheSingleBinaryContract`,
  (`go test ./cmd/kilasflow/`, green, incl. `-race`).
- Disjoint claims under load: prior slice's
  `TestTwoServicesClaimDisjointExecutionsOnPostgres`, re-run green with
  this slice (8 queued, 2 services x 2 workers, echo calls == 8).
- Single scheduler: no advisory-lock election added — `ClaimDue` already
  advances `next_run_at` inside the same transaction that reads it, so a
  due time fires exactly once with any number of schedulers (FEAT-gjzgkd
  reached the same conclusion). Role gating keeps a split deployment to
  one scheduler (workers never run it). Proof:
  `TestTwoSchedulersDoNotDoubleFireOnPostgres` (two services, concurrent
  `Tick`, exactly 1 queued run).
- Cross-process wake: prior slice's
  `TestCrossProcessWakeReachesTheOtherWorkerOnPostgres`, re-run green.
- Event fan-out: `Service.publish` relays identifiers-only notices
  (tenant/execution/node/sequence/type/status, never `Data`, tens of bytes
  vs the ~8000-byte NOTIFY cap; channels carry the table prefix) via an
  injected `RelaySend` (pooled `pg_notify` in `main.go`); API processes
  run `WatchRemoteEvents` and republish into their own broker, skipping
  their own origin. Proof:
  `TestWorkerEventsReachAnAPIProcessBrokerOnPostgres` (worker runs, API
  broker subscribed by execution ID sees node.completed then
  execution.completed).
- Cross-process cancel: floor is a status poll in `runOnce` plus a new
  `ctx.Err()` check at the top of the runner's node loop (current node may
  finish when its executor ignores context; the next node never starts);
  low-latency path is `notifyCancel` + `WatchCancellations` interrupting
  the lease holder. Proof:
  `TestRemoteCancelStopsTheHolderBetweenNodesOnSQLite` (sqlite, cancel
  straight at the store so only the poll can stop it: terminal cancelled,
  gate saw the interrupt, post-gate node never ran) and
  `TestCancelNoticeInterruptsTheHolderWithoutThePollIntervalOnPostgres`
  (holder on a 10 s tick, terminal cancelled in ~1 s, well inside the
  tick, lease released).
- Kill-9 reclaim e2e:
  `TestAbandonedLeaseIsReclaimedWithoutDuplicateNodeRunsOnPostgres`
  (dead claim with a 2 s lease + one partial trace row, abandoned 3 s, a
  second service's `RunOnce` reclaims and succeeds; echo calls == 1, one
  echo row with unique sequences, `UpdateRuntime`/`CreateNodeRun` under
  the dead lease both rejected).
- Split-role refusal: any non-`both` role on the sqlite driver is refused
  at startup naming both drivers and the reason (one writer, no
  LISTEN/NOTIFY). Proof: `TestSplitRoleOnSQLiteIsRefusedAtStartup`.
- Docs: `operate/deployment.md` "Multiple workers" gains Roles, fan-out,
  cancel-interrupts-the-holder, and kill-9-reclaimed entries.

Proof (`go test ./internal/engine/ ./cmd/kilasflow/ -count=1`, green both
without a DSN — PG halves skip cleanly — and green with
`KILASFLOW_TEST_POSTGRES_DSN` against kf-pg-vector:55434 using `-p 1`;
`-race` green on all eight multiprocess tests plus the role tests):
- engine 8.7 s / cmd 0.6 s with PG; engine 1.0 s / cmd 0.7 s without.
- One transient mid-flight failure noted: `go test -p 1` once failed to
  build `cmd/kilasflow` because a concurrent web build was rewriting
  `internal/web/dist` hashed names; retry after it settled was green.
  `M internal/web/dist/index.html` in the tree is that peer build's
  artifact, not this slice's.

Reasoned deferral: the ticket's plan puts the role inside the `Execution`
config section (file + `KILASFLOW_EXECUTION_*` env). This slice implements
it as the `--role` flag only, to hold the Batch-8 ownership boundary
(service/cmd/docs, no config edits). File/env support is a mechanical
follow-up (one `Role` key on `Execution` + `parseProcessRole` at load);
nothing in the role gating depends on where the value comes from.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (3):
  - `90b22950` — merge: worker identity plus webhook lifecycle integrity (FEAT-9555xz, FEAT-ykyfbd)
  - `f2699725` — chore(pine): point every roadmap citation at the in-repo roadmap
  - `71f15dab` — chore(pine): open the V2 n8n-first epic
- Files changed (base → working tree):

```
 .env.example                                       |   186 +
 .github/actions/js-toolchain/action.yml            |    49 +
 .github/workflows/ci.yml                           |   429 +
 .github/workflows/release.yml                      |   157 +
 .gitignore                                         |    13 +
 .pine/CHECKPOINT.md                                |   148 +
 .pine/MEMORY.md                                    |     9 +
 .pine/learnings/LRN-jrxe9h.md                      |     9 +
 .pine/learnings/LRN-t016v0.md                      |     9 +
 .pine/memory/code-node.md                          |    74 +
 .pine/memory/docker.md                             |     9 +
 .pine/memory/licensing.md                          |    14 +
 .pine/memory/live-databases.md                     |    40 +
 .pine/memory/n8n-reference.md                      |    11 +
 .pine/memory/persistence.md                        |    14 +
 .pine/memory/web-editor.md                         |    10 +
 .pine/roadmap.md                                   |  1174 ++
 .pine/tickets/BUG-9s3htg.md                        |   170 +
 .pine/tickets/BUG-br7ggc.md                        |   189 +
 .pine/tickets/BUG-v6tdjr.md                        |   349 +
 .pine/tickets/BUG-xmcm8x.md                        |   152 +
 .pine/tickets/EPIC-m42s3g.md                       |    79 +
 .pine/tickets/FEAT-0556ck.md                       |   729 ++
 .pine/tickets/FEAT-096vs9.md                       |   831 ++
 .pine/tickets/FEAT-0f87fn.md                       |   337 +
 .pine/tickets/FEAT-12s0e5.md                       |   539 +
 .pine/tickets/FEAT-1500sp.md                       |   212 +
 .pine/tickets/FEAT-1axhdn.md                       |   144 +
 .pine/tickets/FEAT-1br8at.md                       |   128 +
 .pine/tickets/FEAT-1c70nt.md                       |   173 +
 .pine/tickets/FEAT-1jqjtd.md                       |    73 +
 .pine/tickets/FEAT-27km39.md                       |   730 ++
 .pine/tickets/FEAT-2f68r8.md                       |   129 +
 .pine/tickets/FEAT-2phs15.md                       |   461 +
 .pine/tickets/FEAT-347egc.md                       |   854 ++
 .pine/tickets/FEAT-3taswf.md                       |   786 ++
 .pine/tickets/FEAT-3xqky1.md                       |   916 ++
 .pine/tickets/FEAT-45tfmh.md                       |   438 +
 .pine/tickets/FEAT-48hreg.md                       |   894 ++
 .pine/tickets/FEAT-4d0bje.md                       |   904 ++
 .pine/tickets/FEAT-53fht8.md                       |   120 +
 .pine/tickets/FEAT-55v09k.md                       |   124 +
 .pine/tickets/FEAT-5fhj6p.md                       |   144 +
 .pine/tickets/FEAT-5fv8gf.md                       |   226 +
 .pine/tickets/FEAT-5kfctc.md                       |   208 +
 .pine/tickets/FEAT-5kv1jq.md                       |   119 +
 .pine/tickets/FEAT-5mvech.md                       |   332 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
 .pine/tickets/FEAT-5z37xh.md                       |   767 ++
 .pine/tickets/FEAT-68zzqs.md                       |   438 +
 .pine/tickets/FEAT-6vfn3s.md                       |   395 +
 .pine/tickets/FEAT-7cg0cd.md                       |   886 ++
 .pine/tickets/FEAT-7tgasa.md                       |   252 +
 .pine/tickets/FEAT-8mymac.md                       |    58 +
 .pine/tickets/FEAT-8qyfh1.md                       |   486 +
 .pine/tickets/FEAT-8r9n21.md                       |   343 +
 .pine/tickets/FEAT-91as16.md                       |   142 +
 .pine/tickets/FEAT-9555xz.md                       |   183 +
 .pine/tickets/FEAT-96p7m3.md                       |   830 ++
 .pine/tickets/FEAT-9dqn7d.md                       |   422 +
 .pine/tickets/FEAT-9knk67.md                       |   121 +
 .pine/tickets/FEAT-a6yg3n.md                       |   126 +
 .pine/tickets/FEAT-a7p1b2.md                       |   136 +
 .pine/tickets/FEAT-a94c8y.md                       |   931 ++
 .pine/tickets/FEAT-adzn0a.md                       |   112 +
 .pine/tickets/FEAT-afs850.md                       |   114 +
 .pine/tickets/FEAT-agj52c.md                       |   979 ++
 .pine/tickets/FEAT-ajw7wt.md                       |   163 +
 .pine/tickets/FEAT-az620p.md                       |   482 +
 .pine/tickets/FEAT-bp0ytb.md                       |   376 +
 .pine/tickets/FEAT-bscygc.md                       |   713 ++
 .pine/tickets/FEAT-c2a081.md                       |   891 ++
 .pine/tickets/FEAT-cgm1y3.md                       |   830 ++
 .pine/tickets/FEAT-cjpbe6.md                       |  1066 ++
 .pine/tickets/FEAT-cpdp8y.md                       |   924 ++
 .pine/tickets/FEAT-csqgg5.md                       |   146 +
 .pine/tickets/FEAT-cwz4ac.md                       |   763 ++
 .pine/tickets/FEAT-cx3hq1.md                       |   712 ++
 .pine/tickets/FEAT-czbzs6.md                       |   717 ++
 .pine/tickets/FEAT-ddzk2k.md                       |   114 +
 .pine/tickets/FEAT-de8d4c.md                       |   818 ++
 .pine/tickets/FEAT-ed6wdy.md                       |   804 ++
 .pine/tickets/FEAT-ej0468.md                       |   874 ++
 .pine/tickets/FEAT-frvez8.md                       |   841 ++
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |   196 +
 .pine/tickets/FEAT-gg85se.md                       |   702 ++
 .pine/tickets/FEAT-gjzgkd.md                       |  1089 ++
 .pine/tickets/FEAT-gvn62x.md                       |   197 +
 .pine/tickets/FEAT-gxppx1.md                       |   902 ++
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |   838 ++
 .pine/tickets/FEAT-jq84xk.md                       |   790 ++
 .pine/tickets/FEAT-jwhdsy.md                       |   445 +
 .pine/tickets/FEAT-k3fmj1.md                       |   142 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |   900 ++
 .pine/tickets/FEAT-k9dwgn.md                       |  1050 ++
 .pine/tickets/FEAT-knpfqf.md                       |  1064 ++
 .pine/tickets/FEAT-kwxxd0.md                       |   141 +
 .pine/tickets/FEAT-m94hhx.md                       |   265 +
 .pine/tickets/FEAT-mvegj5.md                       |   112 +
 .pine/tickets/FEAT-n19dch.md                       |   981 ++
 .pine/tickets/FEAT-n5fdz3.md                       |   458 +
 .pine/tickets/FEAT-nbqye0.md                       |   129 +
 .pine/tickets/FEAT-nc6z9r.md                       |  1014 ++
 .pine/tickets/FEAT-nch9dg.md                       |  1012 ++
 .pine/tickets/FEAT-nrfg6e.md                       |   921 ++
 .pine/tickets/FEAT-nrfz6m.md                       |   197 +
 .pine/tickets/FEAT-nxxbs5.md                       |   213 +
 .pine/tickets/FEAT-pd3p6x.md                       |   129 +
 .pine/tickets/FEAT-pnbt4z.md                       |    91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   834 ++
 .pine/tickets/FEAT-q81bq4.md                       |   481 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-qfr9xe.md                       |   136 +
 .pine/tickets/FEAT-r6xhnp.md                       |   856 ++
 .pine/tickets/FEAT-rj17xj.md                       |  1103 ++
 .pine/tickets/FEAT-sar60r.md                       |   125 +
 .pine/tickets/FEAT-sbnejr.md                       |   834 ++
 .pine/tickets/FEAT-sdjdh2.md                       |    82 +
 .pine/tickets/FEAT-sfy1tq.md                       |   139 +
 .pine/tickets/FEAT-snxxny.md                       |   409 +
 .pine/tickets/FEAT-sp8cfm.md                       |   396 +
 .pine/tickets/FEAT-ss44d9.md                       |   875 ++
 .pine/tickets/FEAT-t26rt7.md                       |  1066 ++
 .pine/tickets/FEAT-t5q318.md                       |   131 +
 .pine/tickets/FEAT-v8k1tc.md                       |   132 +
 .pine/tickets/FEAT-vvwpjw.md                       |   433 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-wdnc03.md                       |    62 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |   886 ++
 .pine/tickets/FEAT-xeq6st.md                       |   914 ++
 .pine/tickets/FEAT-xqqjqv.md                       |   327 +
 .pine/tickets/FEAT-xr7ga9.md                       |   841 ++
 .pine/tickets/FEAT-xx6p22.md                       |   117 +
 .pine/tickets/FEAT-ybm2pd.md                       |   103 +
 .pine/tickets/FEAT-ykyfbd.md                       |   972 ++
 .pine/tickets/FEAT-yx0qt6.md                       |   749 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   124 +
 .pine/tickets/FEAT-za118x.md                       |   711 ++
 .pine/tickets/FEAT-zmfsjd.md                       |   146 +
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |   384 +
 Dockerfile                                         |    54 +-
 Makefile                                           |   251 +-
 README.md                                          |   276 +-
 cmd/kilasflow/main.go                              |   798 +-
 cmd/kilasflow/main_test.go                         |   135 +-
 cmd/kilasflow/secrets_boot_test.go                 |   102 +
 cmd/nodepackgen/authorcmd.go                       |   134 +
 cmd/nodepackgen/generate.go                        |   576 +
 cmd/nodepackgen/generate_test.go                   |   374 +
 cmd/nodepackgen/main.go                            |   175 +
 cmd/nodepackgen/openapi.go                         |   168 +
 cmd/nodepackgen/testdata/manifest.json             |    12 +
 cmd/nodepackgen/testdata/pack.golden.json          |   238 +
 cmd/nodepackgen/testdata/report.golden.md          |    20 +
 cmd/nodepackgen/testdata/spec.json                 |    98 +
 compose.build.yaml                                 |    35 +
 compose.postgres.yaml                              |    76 +
 compose.yaml                                       |   103 +
 config.example.yaml                                |   326 +-
 docker-compose.yml                                 |    41 -
 docs/.gitignore                                    |     6 +
 docs/astro.config.mjs                              |   130 +
 docs/package.json                                  |    20 +
 docs/plugins/base-links.mjs                        |    57 +
 docs/pnpm-lock.yaml                                |  3807 +++++++
 docs/src/components/ThemeProvider.astro            |    59 +
 docs/src/components/ThemeSelect.astro              |    79 +
 docs/src/content.config.ts                         |    11 +
 docs/src/content/docs/404.md                       |    21 +
 docs/src/content/docs/concepts/architecture.md     |   163 +
 docs/src/content/docs/concepts/credentials.md      |   223 +
 docs/src/content/docs/concepts/execution-model.md  |   335 +
 docs/src/content/docs/concepts/expressions.md      |   210 +
 .../src/content/docs/concepts/items-and-lineage.md |   174 +
 docs/src/content/docs/concepts/node-registry.md    |   319 +
 .../src/content/docs/concepts/safety-boundaries.md |   283 +
 .../content/docs/concepts/tenancy-and-embedding.md |   305 +
 docs/src/content/docs/concepts/webhooks.md         |   203 +
 docs/src/content/docs/contributing.md              |    89 +
 docs/src/content/docs/guides/community-nodes.md    |    94 +
 docs/src/content/docs/guides/embedding.md          |   365 +
 docs/src/content/docs/guides/n8n-migration.md      |   674 ++
 docs/src/content/docs/guides/node-authoring.md     |   499 +
 docs/src/content/docs/index.mdx                    |    59 +
 .../docs/operate/configuration-reference.md        |   770 ++
 docs/src/content/docs/operate/configuration.md     |    71 +
 docs/src/content/docs/operate/deployment.md        |   175 +
 docs/src/content/docs/operate/security.md          |   112 +
 docs/src/content/docs/operate/upgrades.md          |    69 +
 docs/src/content/docs/reference/api-contract.md    |   306 +
 docs/src/content/docs/reference/api.md             |    41 +
 docs/src/content/docs/reference/api/auth.md        |   129 +
 docs/src/content/docs/reference/api/credentials.md |   168 +
 docs/src/content/docs/reference/api/embed.md       |    29 +
 docs/src/content/docs/reference/api/errors.md      |    36 +
 docs/src/content/docs/reference/api/events.md      |    40 +
 docs/src/content/docs/reference/api/executions.md  |   102 +
 docs/src/content/docs/reference/api/interop.md     |    51 +
 docs/src/content/docs/reference/api/nodes.md       |   111 +
 docs/src/content/docs/reference/api/schedules.md   |    88 +
 docs/src/content/docs/reference/api/system.md      |    42 +
 docs/src/content/docs/reference/api/webhooks.md    |    36 +
 docs/src/content/docs/reference/api/workflows.md   |   288 +
 .../content/docs/reference/expression-grammar.md   |   183 +
 docs/src/content/docs/reference/node-packs.md      |   153 +
 docs/src/content/docs/start/first-workflow.md      |    40 +
 docs/src/content/docs/start/install.md             |   271 +
 docs/src/content/docs/start/what-kilasflow-is.md   |    84 +
 docs/src/styles/kilasflow.css                      |   138 +
 docs/tsconfig.json                                 |     5 +
 e2e/.gitignore                                     |     2 +
 e2e/fixtures.ts                                    |    40 +
 e2e/fixtures/ai-gateway.ts                         |   126 +
 e2e/fixtures/datastore.ts                          |   544 +
 e2e/fixtures/epic-external.ts                      |   290 +
 e2e/fixtures/epic-telegram.ts                      |   432 +
 e2e/fixtures/pack-acme-transcription.json          |    91 +
 e2e/fixtures/pack-convert-driver.go                |    58 +
 e2e/fixtures/pack-install.ts                       |   269 +
 e2e/fixtures/waha-migration.ts                     |   210 +
 e2e/global-setup.ts                                |    27 +
 e2e/helpers/seed.ts                                |   174 +
 e2e/helpers/server.ts                              |   142 +
 e2e/helpers/stub.ts                                |    98 +
 e2e/package.json                                   |    14 +
 e2e/playwright.config.ts                           |    32 +
 e2e/pnpm-lock.yaml                                 |    57 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   564 +
 e2e/tests/datastore-pg.spec.ts                     |   257 +
 e2e/tests/datastore.spec.ts                        |   376 +
 e2e/tests/epic-acceptance.spec.ts                  |   765 ++
 e2e/tests/node-coverage.spec.ts                    |   868 ++
 e2e/tests/pack-converted.spec.ts                   |   249 +
 e2e/tests/pack-editor.spec.ts                      |   239 +
 e2e/tests/pack-install.spec.ts                     |   173 +
 e2e/tests/pack-safety.spec.ts                      |   100 +
 e2e/tests/pack-trigger.spec.ts                     |   165 +
 e2e/tests/smoke.spec.ts                            |   108 +
 e2e/tests/waha-migration.spec.ts                   |   279 +
 executions-narrow.png                              |   Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |    32 +
 go.mod                                             |     9 +-
 go.sum                                             |    37 +-
 internal/ai/agent.go                               |   146 +-
 internal/ai/agent_output_test.go                   |   185 +
 internal/ai/ai.go                                  |    50 +-
 internal/ai/ai_test.go                             |   213 +
 internal/ai/fromai.go                              |   548 +
 internal/ai/fromai_test.go                         |   139 +
 internal/ai/maf/doc.go                             |    15 +-
 internal/ai/maf/runtime.go                         |   144 +
 internal/ai/maf/runtime_test.go                    |   131 +
 internal/ai/memory.go                              |   164 +-
 internal/ai/openai.go                              |   100 +-
 internal/ai/openai_test.go                         |   156 +
 internal/ai/outputschema.go                        |   414 +
 internal/api/auth_test.go                          |   668 ++
 internal/api/credentials_test.go                   |   401 +
 internal/api/datastores_csv_test.go                |   353 +
 internal/api/datastores_test.go                    |   358 +
 internal/api/embed_datastore_test.go               |   230 +
 internal/api/embed_test.go                         |    61 +-
 internal/api/handlers/auth.go                      |   380 +
 internal/api/handlers/credentials.go               |   339 +
 internal/api/handlers/datastores.go                |   640 ++
 internal/api/handlers/datastores_csv.go            |   501 +
 internal/api/handlers/embed.go                     |    64 +-
 internal/api/handlers/executions.go                |    85 +-
 internal/api/handlers/interop.go                   |    61 +-
 internal/api/handlers/nodes.go                     |   468 +-
 internal/api/handlers/resume.go                    |   216 +
 internal/api/handlers/resume_test.go               |   232 +
 internal/api/handlers/tenants.go                   |    46 +
 internal/api/handlers/workflows.go                 |   332 +-
 internal/api/handlers/workflows_delete_test.go     |   111 +
 internal/api/middleware/auth.go                    |   173 +
 internal/api/middleware/embed.go                   |    77 +-
 internal/api/middleware/embed_test.go              |   120 +
 internal/api/node_types_test.go                    |   424 +
 internal/api/routes.go                             |    69 +-
 internal/api/server.go                             |    70 +-
 internal/api/workflow_history_test.go              |   188 +
 internal/api/workflows_test.go                     |   211 +-
 internal/auth/auth.go                              |    73 +
 internal/auth/auth_test.go                         |   311 +
 internal/auth/keys.go                              |   197 +
 internal/auth/session.go                           |   274 +
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/conditions/conditions.go                  |   542 +
 internal/conditions/conditions_test.go             |   231 +
 internal/conditions/doc.go                         |    26 +
 internal/config/config.go                          |   627 +-
 internal/config/config_test.go                     |   387 +
 internal/config/secrets_test.go                    |    35 +
 internal/credentials/builtin.go                    |   189 +
 internal/credentials/credentials.go                |   113 +-
 internal/credentials/credentials_test.go           |   244 +
 internal/credentials/external.go                   |   437 +
 internal/credentials/external_test.go              |   359 +
 internal/credentials/keysource.go                  |    82 +
 internal/credentials/registry.go                   |   346 +
 internal/credentials/vault.go                      |   155 +
 internal/database/database.go                      |    71 +-
 internal/database/database_test.go                 |    91 +-
 internal/database/migrate.go                       |   648 ++
 internal/database/migrate_test.go                  |   898 ++
 internal/database/prefix_test.go                   |   371 +
 internal/datastore/catalogue.go                    |   113 +
 internal/datastore/catalogue_test.go               |    74 +
 internal/datastore/concurrency.go                  |   284 +
 internal/datastore/concurrency_test.go             |   289 +
 internal/datastore/config_bind_test.go             |    28 +
 internal/datastore/doc.go                          |    39 +
 internal/datastore/engine.go                       |   438 +
 internal/datastore/engine_test.go                  |   628 ++
 internal/datastore/evolve_test.go                  |   156 +
 internal/datastore/filter.go                       |   370 +
 internal/datastore/fleet.go                        |   163 +
 internal/datastore/fleet_test.go                   |   117 +
 internal/datastore/idents.go                       |   206 +
 internal/datastore/idents_test.go                  |   215 +
 internal/datastore/isolation.go                    |    74 +
 internal/datastore/isolation_test.go               |   207 +
 internal/datastore/limits.go                       |   192 +
 internal/datastore/limits_test.go                  |   295 +
 internal/datastore/migrate_test.go                 |   244 +
 internal/datastore/model.go                        |    51 +
 internal/datastore/rows.go                         |   832 ++
 internal/datastore/rows_test.go                    |   651 ++
 internal/datastore/trace.go                        |   118 +
 internal/datastore/trace_test.go                   |   183 +
 internal/datetime/datetime_test.go                 |   134 +
 internal/datetime/doc.go                           |    15 +
 internal/datetime/format.go                        |   195 +
 internal/datetime/parse.go                         |   108 +
 internal/embed/embed.go                            |   105 +-
 internal/embed/embed_test.go                       |   119 +
 internal/engine/approval.go                        |   334 +
 internal/engine/approval_test.go                   |   213 +
 internal/engine/authenticate.go                    |   104 +
 internal/engine/checkpoint.go                      |    82 +
 internal/engine/multiprocess_test.go               |   968 ++
 internal/engine/runner.go                          |  1122 +-
 internal/engine/runner_test.go                     |  1229 +-
 internal/engine/service.go                         |   613 +-
 internal/engine/service_test.go                    |    55 +-
 internal/engine/subworkflow_test.go                |   329 +
 internal/engine/trace.go                           |    28 +
 internal/engine/trace_test.go                      |   263 +
 internal/engine/wait_service.go                    |   585 +
 internal/engine/wait_service_test.go               |   713 ++
 internal/engine/worker_test.go                     |    38 +-
 internal/execution/records.go                      |    74 +-
 internal/execution/redact.go                       |   124 +-
 internal/execution/redact_datastore_test.go        |    75 +
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    91 +-
 internal/expression/expression.go                  |   359 +-
 internal/expression/expression_test.go             |   374 +-
 internal/expression/functions.go                   |   219 +
 internal/expression/roots.go                       |   125 +
 internal/expression/undefined.go                   |    22 +
 internal/guardrails/doc.go                         |     8 +
 internal/guardrails/licence_boundary_test.go       |   359 +
 internal/guardrails/shell_injection_test.go        |    70 +
 internal/interop/n8n/corpus/BASELINE.md            |   107 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   446 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-datatable.json     |    95 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   639 ++
 internal/interop/n8n/export_test.go                |    25 +
 internal/interop/n8n/n8n.go                        |  1387 ++-
 internal/interop/n8n/n8n_test.go                   |  3826 ++++++-
 internal/interop/n8n/parameters.go                 |  3328 +++++-
 internal/interop/n8n/sqlfidelity_test.go           |   442 +
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   110 +
 internal/loadoptions/datastores.go                 |   124 +
 internal/loadoptions/datastores_test.go            |    74 +
 internal/loadoptions/loadoptions.go                |   412 +
 internal/loadoptions/loadoptions_test.go           |   453 +
 internal/loadoptions/schema.go                     |    68 +
 internal/loadoptions/sql.go                        |   287 +
 internal/loadoptions/sql_test.go                   |   350 +
 internal/loadoptions/workflows.go                  |    64 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   771 +-
 internal/node/registry_test.go                     |   932 +-
 internal/nodepack/author.go                        |   159 +
 internal/nodepack/author_test.go                   |   292 +
 internal/nodepack/convert.go                       |   799 ++
 internal/nodepack/convert_test.go                  |   413 +
 internal/nodepack/loaddir.go                       |   155 +
 internal/nodepack/loaddir_test.go                  |   336 +
 internal/nodepack/nodepack.go                      |   432 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/nodepack/validate.go                      |   417 +
 internal/property/loader.go                        |    92 +
 internal/property/locator_test.go                  |   116 +
 internal/property/mapper.go                        |   322 +
 internal/property/mapper_test.go                   |   196 +
 internal/property/property.go                      |   459 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/auth.go                        |   330 +
 internal/repository/auth_test.go                   |   322 +
 internal/repository/claim_wake_test.go             |   395 +
 internal/repository/credentials.go                 |   196 +-
 internal/repository/credentials_external_test.go   |   246 +
 internal/repository/execution_retention.go         |   180 +
 internal/repository/execution_retention_test.go    |   396 +
 internal/repository/executions.go                  |   291 +-
 internal/repository/models.go                      |   333 +-
 internal/repository/models_test.go                 |    28 +-
 internal/repository/postgres_execution_test.go     |   213 +
 internal/repository/prefix_test.go                 |    80 +
 internal/repository/schedules.go                   |   145 +-
 internal/repository/table_names_test.go            |    51 +
 internal/repository/tenant_purge.go                |    57 +
 internal/repository/tenant_purge_test.go           |   118 +
 internal/repository/waits.go                       |   521 +
 internal/repository/waits_test.go                  |   313 +
 internal/repository/wake.go                        |   199 +
 internal/repository/wake_internal_test.go          |    93 +
 internal/repository/webhooks.go                    |   268 +-
 internal/repository/webhooks_routes_test.go        |    46 +
 internal/repository/workflow_history.go            |   446 +
 internal/repository/workflow_history_test.go       |   613 +
 internal/repository/workflows.go                   |   182 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   571 +
 internal/routing/request.go                        |   412 +
 internal/routing/response.go                       |   177 +
 internal/routing/routing.go                        |   373 +
 internal/routing/routing_test.go                   |   764 ++
 internal/runcode/doc.go                            |    37 +-
 internal/runcode/runcode.go                        |    96 +-
 internal/runcode/runcode_test.go                   |   184 +-
 internal/safehttp/safehttp.go                      |   139 +-
 internal/safehttp/safehttp_test.go                 |   193 +
 internal/scheduler/extract.go                      |    81 +
 internal/scheduler/item.go                         |    71 +
 internal/scheduler/rule.go                         |   321 +
 internal/scheduler/rule_test.go                    |   278 +
 internal/scheduler/scheduler.go                    |   147 +-
 internal/scheduler/scheduler_test.go               |   196 +-
 internal/sqlbuild/dialect.go                       |   256 +
 internal/sqlbuild/sqlbuild.go                      |   392 +
 internal/sqlbuild/sqlbuild_test.go                 |   650 ++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |     2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |     2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |     2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |     2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |     2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |     2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |     2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |     2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |     2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |     2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |     2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |     2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |     2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |     2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |     2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |     2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |     2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |     2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |     2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |     2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |     2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |     2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |     2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |     2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |     2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |     2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |     2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |     2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |     2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |     2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |     2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |     2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |     2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |     2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |     2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |     2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |     2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |     2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |     2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |     2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |     2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |     1 +
 .../testdata/mysql/delete_drop_cascade.sql         |     1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |     2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |     1 +
 .../testdata/mysql/delete_truncate_restart.sql     |     1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |     2 +
 .../testdata/mysql/insert_skip_conflict.sql        |     2 +
 internal/sqlbuild/testdata/mysql/select.sql        |     3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |     2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |     3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |     3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |     2 +
 internal/sqlbuild/testdata/mysql/update.sql        |     2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |     2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |     2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |     1 +
 .../testdata/postgres/delete_drop_cascade.sql      |     1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |     2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |     1 +
 .../testdata/postgres/delete_truncate_restart.sql  |     1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |     3 +
 .../testdata/postgres/insert_skip_conflict.sql     |     3 +
 internal/sqlbuild/testdata/postgres/select.sql     |     3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |     2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |     3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |     3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |     2 +
 internal/sqlbuild/testdata/postgres/update.sql     |     3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |     3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |     3 +
 internal/sqlguard/admit.go                         |   245 +
 internal/sqlguard/attack_test.go                   |   344 +
 internal/sqlguard/dialect.go                       |   260 +
 internal/sqlguard/doc.go                           |    53 +
 internal/sqlguard/sqlguard.go                      |   443 +
 internal/sqlguard/sqlguard_test.go                 |   338 +
 internal/sqlnode/export_test.go                    |    11 +
 internal/sqlnode/guard_test.go                     |   126 +
 internal/sqlnode/internal_test.go                  |   251 +
 internal/sqlnode/introspect.go                     |   240 +
 internal/sqlnode/policy_test.go                    |   243 +
 internal/sqlnode/sqlnode.go                        |   905 +-
 internal/sqlnode/sqlnode_test.go                   |   106 +
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   248 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   396 +-
 internal/webhook/webhook_test.go                   |   652 +-
 internal/workflow/compiler.go                      |   506 +-
 internal/workflow/compiler_test.go                 |   215 +
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/lifecycle.go                     |    51 +
 internal/workflow/typeversion.go                   |   159 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 migrations/.gitkeep                                |     0
 migrations/embed.go                                |    27 +
 migrations/postgres/000001_baseline.down.sql       |    23 +
 migrations/postgres/000001_baseline.up.sql         |   192 +
 .../postgres/000002_workflow_history.down.sql      |    11 +
 migrations/postgres/000002_workflow_history.up.sql |    30 +
 migrations/postgres/000003_identity.down.sql       |    15 +
 migrations/postgres/000003_identity.up.sql         |    75 +
 .../postgres/000004_execution_indexes.down.sql     |     5 +
 .../postgres/000004_execution_indexes.up.sql       |    26 +
 migrations/postgres/000005_datastores.down.sql     |    10 +
 migrations/postgres/000005_datastores.up.sql       |    48 +
 migrations/postgres/000006_vector_store.down.sql   |    19 +
 migrations/postgres/000006_vector_store.up.sql     |   155 +
 .../postgres/000008_secret_bindings.down.sql       |     6 +
 migrations/postgres/000008_secret_bindings.up.sql  |    29 +
 .../postgres/000009_execution_waits.down.sql       |     6 +
 migrations/postgres/000009_execution_waits.up.sql  |    40 +
 migrations/sqlite/000001_baseline.down.sql         |    22 +
 migrations/sqlite/000001_baseline.up.sql           |   185 +
 migrations/sqlite/000002_workflow_history.down.sql |    11 +
 migrations/sqlite/000002_workflow_history.up.sql   |    29 +
 migrations/sqlite/000003_identity.down.sql         |    14 +
 migrations/sqlite/000003_identity.up.sql           |    73 +
 .../sqlite/000004_execution_indexes.down.sql       |     5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |    21 +
 migrations/sqlite/000005_datastores.down.sql       |    10 +
 migrations/sqlite/000005_datastores.up.sql         |    47 +
 migrations/sqlite/000006_vector_store.down.sql     |     6 +
 migrations/sqlite/000006_vector_store.up.sql       |    29 +
 migrations/sqlite/000008_secret_bindings.down.sql  |     6 +
 migrations/sqlite/000008_secret_bindings.up.sql    |    29 +
 migrations/sqlite/000009_execution_waits.down.sql  |     6 +
 migrations/sqlite/000009_execution_waits.up.sql    |    39 +
 nodes/ai.go                                        |  2838 ++++-
 nodes/ai_mcp_test.go                               |   502 +
 nodes/ai_ollama_test.go                            |   413 +
 nodes/ai_test.go                                   |  1463 ++-
 nodes/ai_tools_test.go                             |   519 +
 nodes/annotation.go                                |    62 +
 nodes/apostrophe_live_test.go                      |    43 +
 nodes/assignments.go                               |   180 +
 nodes/bindings_test.go                             |   136 +
 nodes/code.go                                      |   106 +-
 nodes/code_test.go                                 |   155 +-
 nodes/conditions.go                                |   139 +
 nodes/core.go                                      |   224 +-
 nodes/database.go                                  |   331 +-
 nodes/database_test.go                             |   757 +-
 nodes/datastore.go                                 |  1214 ++
 nodes/datastore_test.go                            |   456 +
 nodes/datastore_tool_test.go                       |   758 ++
 nodes/datetime.go                                  |   408 +
 nodes/datetime_test.go                             |   299 +
 nodes/executors.go                                 |   666 +-
 nodes/executors_test.go                            |   480 +
 nodes/flow.go                                      |   457 +
 nodes/flow_test.go                                 |   464 +
 nodes/http.go                                      |   197 +-
 nodes/http_test.go                                 |   231 +-
 nodes/jscode.go                                    |   172 +
 nodes/jscode_test.go                               |   100 +
 nodes/loop.go                                      |   245 +
 nodes/mysql_v2.go                                  |   199 +
 nodes/mysql_v2_test.go                             |   181 +
 nodes/pgvector.go                                  |  1236 +++
 nodes/pgvector_test.go                             |   493 +
 nodes/postgres_v2.go                               |   633 ++
 nodes/postgres_v2_test.go                          |   231 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/sql_options.go                               |   569 +
 nodes/sql_options_live_test.go                     |   594 +
 nodes/sql_options_test.go                          |   257 +
 nodes/sqlite_attach_test.go                        |   161 +
 nodes/subworkflow.go                               |   275 +
 nodes/telegram.go                                  |   393 +
 nodes/telegram_download.go                         |   243 +
 nodes/telegram_lifecycle.go                        |   423 +
 nodes/telegram_test.go                             |   610 +
 nodes/testdata/n8n_chat_model_options.json         |    38 +
 nodes/testdata/n8n_sql_options.json                |    17 +
 nodes/transform.go                                 |   745 ++
 nodes/transform_test.go                            |   315 +
 nodes/unsupported.go                               |   116 +-
 nodes/wait.go                                      |   228 +
 nodes/webhook.go                                   |   302 +-
 packs/telegram/README.md                           |    40 +
 packs/telegram/pack.json                           |  1119 ++
 packs/telegram/telegram.go                         |    58 +
 packs/telegram/telegram_test.go                    |   466 +
 packs/waha/README.md                               |    32 +
 packs/waha/REPORT-202409.md                        |   100 +
 packs/waha/REPORT-202502.md                        |   128 +
 packs/waha/manifest-202409.json                    |   124 +
 packs/waha/manifest-202502.json                    |   124 +
 packs/waha/pack-202409.json                        |  2794 +++++
 packs/waha/pack-202502.json                        |  3844 +++++++
 packs/waha/pack-trigger-202409.json                |   124 +
 packs/waha/pack-trigger-202502.json                |   130 +
 packs/waha/waha.go                                 |   107 +
 packs/waha/waha_test.go                            |  1196 ++
 pkg/sdk/.gitkeep                                   |     0
 pkg/sdk/doc.go                                     |    49 +
 pkg/sdk/example/echo/main.go                       |    35 +
 pkg/sdk/sdk.go                                     |    75 +
 pkg/sdk/sdk_test.go                                |    80 +
 pkg/sdk/wasm_exec_test.go                          |    86 +
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/config-reference.go                        |   402 +
 scripts/config-reference_test.go                   |    87 +
 scripts/corpus-sync.sh                             |   225 +
 scripts/docker-tags.sh                             |    84 +
 scripts/e2e-stub.mjs                               |    50 +
 scripts/generate-api-reference.mjs                 |   394 +
 scripts/smoke-docker.sh                            |    13 +-
 scripts/smoke-postgres.sh                          |    43 +-
 sdk/CHANGELOG.md                                   |    23 +
 sdk/README.md                                      |   229 +-
 sdk/examples/host-page/README.md                   |    26 +-
 sdk/examples/host-page/index.html                  |    31 +-
 sdk/examples/host-page/package.json                |    13 +
 sdk/examples/host-page/server.mjs                  |    31 +-
 sdk/examples/reference-host/README.md              |   100 +
 sdk/examples/reference-host/package.json           |    13 +
 sdk/examples/reference-host/server.mjs             |   387 +
 sdk/examples/reference-host/tenant.html            |   101 +
 sdk/package.json                                   |    22 +-
 sdk/scripts/dump-openapi.mjs                       |    13 +
 sdk/src/browser.ts                                 |   147 +-
 sdk/src/generated/models.ts                        |  3030 ++++-
 sdk/src/http.ts                                    |    55 +-
 sdk/src/server.ts                                  |   721 +-
 sdk/src/version.ts                                 |    15 +-
 sdk/test/browser.test.ts                           |   140 +
 sdk/test/datastore-live.test.mjs                   |   100 +
 sdk/test/operation-coverage.test.mjs               |   160 +
 sdk/test/operations.test.ts                        |   220 +
 sdk/test/server.test.ts                            |   439 +-
 sdk/test/version.test.mjs                          |    40 +
 sidecar/doc.go                                     |    49 +
 sidecar/fixture/echo.js                            |    74 +
 sidecar/fixture_test.go                            |    98 +
 sidecar/protocol.go                                |    98 +
 sidecar/sidecar.go                                 |   494 +
 sidecar/sidecar_test.go                            |   433 +
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 web/src/lib/api/generated/auth/auth.ts             |   752 ++
 .../lib/api/generated/credentials/credentials.ts   |   199 +-
 .../datastore-columns/datastore-columns.ts         |   363 +
 .../api/generated/datastore-rows/datastore-rows.ts |   902 ++
 web/src/lib/api/generated/datastores/datastores.ts |   651 ++
 web/src/lib/api/generated/models/aPIKeyResource.ts |    20 +
 .../lib/api/generated/models/activationNotice.ts   |    13 +
 .../lib/api/generated/models/activationResource.ts |    23 +
 web/src/lib/api/generated/models/assignment.ts     |    14 +
 .../models/{unsupported.ts => cSVImportIssue.ts}   |     9 +-
 .../lib/api/generated/models/cSVImportReport.ts    |    17 +
 .../generated/models/clearedDatastoreOutputBody.ts |    13 +
 web/src/lib/api/generated/models/condition.ts      |    14 +
 .../api/generated/models/createAPIKeyInputBody.ts  |    17 +
 .../generated/models/createDatastoreInputBody.ts   |    23 +
 .../models/createStreamTicketInputBody.ts          |    17 +
 .../api/generated/models/createdAPIKeyResource.ts  |    18 +
 .../api/generated/models/credentialRequirement.ts  |    15 +
 .../api/generated/models/datastoreColumnInput.ts   |    22 +
 .../generated/models/datastoreColumnResource.ts    |    14 +
 .../generated/models/datastoreListOutputBody.ts    |    15 +
 .../lib/api/generated/models/datastoreResource.ts  |    17 +
 web/src/lib/api/generated/models/definition.ts     |    18 +
 .../api/generated/models/deleteRowsInputBody.ts    |    15 +
 .../api/generated/models/deleteRowsOutputBody.ts   |    16 +
 .../models/deleteRowsOutputBodyRowsItem.ts         |     9 +
 .../generated/models/executionNodeRunResource.ts   |     2 +
 .../lib/api/generated/models/executionResource.ts  |     6 +
 .../lib/api/generated/models/executionSummary.ts   |     4 +
 .../api/generated/models/executionWaitingEvent.ts  |    24 +
 .../generated/models/exportDatastoreRowsParams.ts  |    14 +
 web/src/lib/api/generated/models/exportIssue.ts    |    16 +
 .../api/generated/models/exportIssueSeverity.ts    |    19 +
 .../generated/models/exportedWorkflowResource.ts   |     4 +-
 .../lib/api/generated/models/expressionGrammar.ts  |    16 +
 web/src/lib/api/generated/models/field.ts          |     1 +
 web/src/lib/api/generated/models/filter.ts         |    14 +
 .../lib/api/generated/models/filterCondition.ts    |    13 +
 .../lib/api/generated/models/getDatastoreRow200.ts |     9 +
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    80 +-
 .../api/generated/models/insertDatastoreRow201.ts  |     9 +
 .../lib/api/generated/models/insertRowInputBody.ts |    15 +
 .../generated/models/insertRowInputBodyValues.ts   |    12 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |    15 +
 .../generated/models/listDatastoreRowsParams.ts    |    37 +
 .../generated/models/listWorkflowVersionsParams.ts |    20 +
 .../api/generated/models/loadOptionsInputBody.ts   |    22 +
 .../models/loadOptionsInputBodyParameters.ts       |     9 +
 .../api/generated/models/loadOptionsResource.ts    |    17 +
 .../lib/api/generated/models/loadSchemaResource.ts |    17 +
 web/src/lib/api/generated/models/loginInputBody.ts |    24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |    21 +
 web/src/lib/api/generated/models/node.ts           |     1 +
 web/src/lib/api/generated/models/nodeCodex.ts      |    16 +
 .../api/generated/models/nodeCodexSubcategories.ts |     9 +
 .../api/generated/models/{lossy.ts => nodeIcon.ts} |     7 +-
 web/src/lib/api/generated/models/option.ts         |    12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |    21 +
 web/src/lib/api/generated/models/port.ts           |     9 +-
 .../lib/api/generated/models/principalResource.ts  |    24 +
 .../lib/api/generated/models/propertyDefinition.ts |    19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |    15 +
 web/src/lib/api/generated/models/propertyMode.ts   |    19 +
 .../generated/models/publishVersionInputBody.ts    |    17 +
 .../models/renameDatastoreColumnInputBody.ts       |    17 +
 .../generated/models/renameDatastoreInputBody.ts   |    17 +
 .../generated/models/resourceMapperDeclaration.ts  |    15 +
 .../lib/api/generated/models/rowListOutputBody.ts  |    16 +
 .../generated/models/rowListOutputBodyItemsItem.ts |     9 +
 .../models/streamExecutionEvents200Item.ts         |     9 +
 .../api/generated/models/streamTicketResource.ts   |    16 +
 .../api/generated/models/testCredentialResource.ts |    17 +
 .../lib/api/generated/models/testPayloadBody.ts    |    22 +
 .../api/generated/models/testPayloadBodyFields.ts  |    12 +
 web/src/lib/api/generated/models/typeOptions.ts    |    17 +
 .../api/generated/models/updateRowsInputBody.ts    |    18 +
 .../generated/models/updateRowsInputBodyValues.ts  |    12 +
 .../api/generated/models/updateRowsOutputBody.ts   |    16 +
 .../models/updateRowsOutputBodyRowsItem.ts         |     9 +
 .../lib/api/generated/models/upsertRowInputBody.ts |    18 +
 .../generated/models/upsertRowInputBodyValues.ts   |    12 +
 .../api/generated/models/upsertRowOutputBody.ts    |    17 +
 .../models/upsertRowOutputBodyRowsItem.ts          |     9 +
 web/src/lib/api/generated/models/visibility.ts     |    15 +
 .../lib/api/generated/models/webhookDeclaration.ts |    15 +
 .../api/generated/models/webhookRouteResource.ts   |    14 +
 .../models/workflowPublishEventResource.ts         |    19 +
 .../models/workflowPublishEventResourceAction.ts   |    16 +
 .../models/workflowVersionListResource.ts          |    17 +
 .../models/workflowVersionSummaryResource.ts       |    23 +
 web/src/lib/api/generated/nodes/nodes.ts           |   443 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |   317 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   113 +-
 web/src/lib/api/http.test.ts                       |    79 +-
 web/src/lib/api/http.ts                            |    56 +
 .../lib/components/dashboard/dashboard-nav.svelte  |    12 +-
 .../lib/components/dashboard/list-states.svelte    |   136 +
 .../components/dashboard/synced-checkbox.svelte    |    44 +
 web/src/lib/components/ui/table/index.ts           |    28 +
 web/src/lib/components/ui/table/table-body.svelte  |    15 +
 .../lib/components/ui/table/table-caption.svelte   |    20 +
 web/src/lib/components/ui/table/table-cell.svelte  |    15 +
 .../lib/components/ui/table/table-footer.svelte    |    20 +
 web/src/lib/components/ui/table/table-head.svelte  |    15 +
 .../lib/components/ui/table/table-header.svelte    |    20 +
 web/src/lib/components/ui/table/table-row.svelte   |    15 +
 web/src/lib/components/ui/table/table.svelte       |    17 +
 .../workflow-editor/activation-notices.svelte      |   120 +
 .../components/workflow-editor/canvas-node.svelte  |    45 +-
 .../workflow-editor/execution-canvas-node.svelte   |    20 +-
 .../components/workflow-editor/node-icon.svelte    |    10 +-
 .../components/workflow-editor/node-picker.svelte  |     8 +-
 .../workflow-editor/properties-panel.svelte        |    61 +-
 .../workflow-editor/property-field.svelte          |   586 +-
 .../workflow-editor/version-panel.svelte           |   509 +
 .../workflow-editor/workflow-editor.svelte         |   290 +-
 web/src/lib/dashboard/cursor-page.test.ts          |    71 +
 web/src/lib/dashboard/cursor-page.ts               |    64 +
 web/src/lib/dashboard/list-state.test.ts           |    65 +
 web/src/lib/dashboard/list-state.ts                |    69 +
 web/src/lib/dashboard/nav-sections.test.ts         |    39 +
 web/src/lib/dashboard/request-guard.test.ts        |    55 +
 web/src/lib/dashboard/request-guard.ts             |    44 +
 web/src/lib/datastore/columns.test.ts              |    97 +
 web/src/lib/datastore/columns.ts                   |    98 +
 web/src/lib/datastore/transfer.test.ts             |    67 +
 web/src/lib/datastore/transfer.ts                  |   102 +
 web/src/lib/embed/embed-editor.svelte              |    50 +-
 web/src/lib/embed/session.svelte.ts                |    22 +-
 web/src/lib/embed/session.test.ts                  |    35 +-
 web/src/lib/workflow-editor/activation.test.ts     |   130 +
 web/src/lib/workflow-editor/activation.ts          |   108 +
 web/src/lib/workflow-editor/assignments.test.ts    |    82 +
 web/src/lib/workflow-editor/assignments.ts         |    89 +
 web/src/lib/workflow-editor/collection.test.ts     |   131 +
 web/src/lib/workflow-editor/collection.ts          |   148 +
 web/src/lib/workflow-editor/conditions.test.ts     |    82 +
 web/src/lib/workflow-editor/conditions.ts          |    80 +
 web/src/lib/workflow-editor/credentials.ts         |    41 +-
 web/src/lib/workflow-editor/document.test.ts       |    10 +-
 web/src/lib/workflow-editor/document.ts            |    14 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |     1 +
 web/src/lib/workflow-editor/event-stream.test.ts   |     2 +-
 web/src/lib/workflow-editor/execution.test.ts      |    72 +-
 web/src/lib/workflow-editor/execution.ts           |    92 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |    43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |   131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |    75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |   273 +
 web/src/lib/workflow-editor/history-diff.ts        |   Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.test.ts    |   186 +-
 web/src/lib/workflow-editor/node-visual.ts         |   228 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 .../lib/workflow-editor/resource-locator.test.ts   |   112 +
 web/src/lib/workflow-editor/resource-locator.ts    |   100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |   121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |   111 +
 .../lib/workflow-editor/version-history.test.ts    |   191 +
 web/src/lib/workflow-editor/version-history.ts     |   156 +
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   199 +
 web/src/routes/(dashboard)/+layout.svelte          |     1 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |   141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   130 +-
 .../app/workflows/[id]/export-dialog.svelte        |   118 +
 .../app/workflows/diagnostics-section.svelte       |   120 +
 .../(dashboard)/app/workflows/import-dialog.svelte |   204 +
 .../(dashboard)/app/workflows/import-report.svelte |   126 +
 .../routes/(dashboard)/credentials/+page.svelte    |    54 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |   209 +
 .../(dashboard)/datastores/[id]/+page.svelte       |   809 ++
 web/src/routes/(dashboard)/executions/+page.svelte |   192 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    43 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    54 +-
 web/src/routes/+page.svelte                        |     8 +-
 web/src/routes/approve/[token]/+page.svelte        |   173 +
 977 files changed, 229780 insertions(+), 2985 deletions(-)
```
