---
id: BUG-ysvmaa
title: 'Engine waits/loops: 1-min sweep, 1h cap, shutdown drain, nested loops, wait-in-loop, lineage'
status: doing
priority: high
labels:
    - engine
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:33:12Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 9 finding(s) from dims: find:core-node-parity, find:engine-runtime.

---
### Graceful shutdown does not drain workers: in-flight executions stay running and are re-executed from scratch after restart, repeating completed side effects [find:engine-runtime] (high/bug) · area: cmd/kilasflow shutdown + reclaim · confidence: high

On SIGTERM only the HTTP server is drained (api/server.go:219-229). Worker goroutines started by runtime.Start(ctx) are not awaited, so the process exits mid-run. The row stays `running`. After restart the expired lease is reclaimed, the trace deleted and the workflow re-run from the trigger. The final history shows one clean run.

Evidence: Private :8101, script <W>/t_crash.py: Manual -> HTTP POST stub /charge -> HTTP /sleep?s=8. SIGTERM was sent 2 s into the run, and server.log printed `shutting down`. After restart the execution was still `running` with no node runs and no error. At lease expiry it ran again: stub /charge at 15:49:43 and again at 15:49:58. Final status `succeeded` with a single trace. FEAT-9555xz (done) claims 'SIGTERM mid-run still writes the terminal cancelled state and releases the lease'. That holds in unit tests, but not for the real process.

n8n behavior: n8n waits for running executions on shutdown (graceful shutdown timeout). Executions interrupted by a crash are marked `crashed` at startup and not re-run.

Impact: Every deploy or restart duplicates the payments, emails and messages of whatever was mid-flight, and execution history hides it. Inline sub-workflow child records left running are reclaimed the same way.

Suggested fix: Track workers in a WaitGroup and drain them, bounded by shutdown_timeout, before exit. Persist per-node progress. On reclaim, resume from the last persisted node or mark the execution crashed (configurable). Never blindly re-run completed non-idempotent nodes.

Files: cmd/kilasflow/main.go, internal/api/server.go, internal/engine/service.go, internal/repository/executions.go

Existing tickets: FEAT-9555xz

---
### Nested loops are mis-detected: the inner loop's entry edge is classified as a back edge, so the inner loop is pruned before the trigger runs and the execution sticks [find:engine-runtime] (high/bug) · area: engine/runner findLoops · confidence: high

findLoops (runner.go:1206-1254) walks every main edge from a loop entry, including through the inner loop's `done` edge into the outer loop and back down. For the inner loop, the outer loop and SplitItems therefore count as 'body', and SplitItems->Inner is recorded as a back edge. schedulingEdges then hides all of Inner's inputs, so Inner is scheduled and skipped first, and the skip-row collision stalls the run.

Evidence: Script nested_wf.py: Trigger -> SplitOut(groups) -> Outer SIB -> SplitOut(items) -> Inner SIB -> NoOp -> Inner, with Inner.done -> Outer and Outer.done -> AllDone. Input {groups:[{items:[1,2]},{items:[3,4]}]}. On KF private :8101 (<W>/t_nested.py kf) the persisted trace begins `1 li(Inner) skipped, 2 b(Body) skipped, 3 t(Trigger), 4 so, 5 lo`, followed by `persist node "li" run: duplicated key`, and the execution stays running. On live n8n 2.33.7 with the documented inner reset option ({{ $prevNode.name === 'SplitItems' }}; <W>/t_nested_reset_n8n.py): Inner 6 runs, Body 4, AllDone 1 run with 4 items, success. KF also drops options.reset on import.

n8n behavior: Nested SplitInBatches work, and the inner loop re-initialises per outer batch with the reset option.

Impact: Every nested-loop workflow (pages x items, customers x orders) hangs and repeats side effects on each reclaim.

Suggested fix: Define a loop body as the nodes reachable from the entry's `loop` port that can reach a back edge to that entry. Never traverse `done` ports or the entry itself. Add nested-loop tests. Implement reset semantics (re-initialise when entered from outside the body).

Files: internal/engine/runner.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-sar60r

---
### A Wait inside a loop silently ends the loop after the first batch and the execution reports success [find:engine-runtime] (high/bug) · area: engine/runner Resume + checkpoint · confidence: high

Loop scheduling flags (loopGraph.started/finished) exist only in memory. prepareGraph rebuilds them fresh on Resume (runner.go:637), and snapshotCheckpoint (:589) does not store them. After a Wait resumes inside a loop body, the entry is never reopened and the done branch sees an empty stream.

Evidence: Shared :8090, script <W>/t_wait.py loop: SIB v3 over [1,2,3] -> Wait 1 s -> Loop, with done -> NoOp. KF trace: l run0 dispatches item 1, w run0, d skipped (0 items), status succeeded. Items 2 and 3 are never processed. Live n8n, same workflow (<W>/t_waitloop_n8n.py): success in 3.1 s, Loop 4 runs, Wait 3 runs, Done with 3 items.

n8n behavior: Wait inside a loop pauses each iteration, and all batches are processed.

Impact: The rate-limit pattern Loop -> API -> Wait -> Loop (34 Wait nodes across the top templates) processes only the first batch and reports success, which is silent data loss.

Suggested fix: Persist loop scheduling state (and runner-held loop node state) in the Checkpoint and restore it in Resume. Add a resume-inside-loop regression test.

Files: internal/engine/runner.go, internal/engine/checkpoint.go

Existing tickets: FEAT-rj17xj, FEAT-sar60r

---
### Timer waits resume only on the 1-minute expired-wait sweep, so 'Wait 2 seconds' takes about 54 s [find:engine-runtime] (high/bug) · area: engine/wait_service sweeper · confidence: high

Interval and specific-time waits suspend to storage and are only re-queued by sweepLoop. Its interval defaults to 1 minute (service.go:161), and main.go does not set it or expose it in config.

Evidence: Shared :8090, script <W>/t_wait.py plain: Manual -> Wait(2 seconds) -> NoOp was `waiting` at 0.5 s and succeeded at 53.9 s. The in-loop 1 s wait also took 53.9 s. n8n ran 3 x 1 s waits in 3.1 s (<W>/t_waitloop_n8n.py).

n8n behavior: Waits under 65 s resume on time in-process. A tracker arms precise timers for longer persisted waits.

Impact: Every sub-minute Wait (rate limiting, polling back-off) is 10-60x slower. Loops with a Wait per item turn into hour-long runs.

Suggested fix: Arm a precise timer per suspension (time.AfterFunc in the owning process), with a ~1 s DB poll of expires_at as fallback, or keep short waits in-process. Expose the sweep interval in config.

Files: internal/engine/wait_service.go, internal/engine/service.go, cmd/kilasflow/main.go

Existing tickets: FEAT-rj17xj

---
### Durable waits are capped at 1 hour, and resume on webhook/form/approval is unreachable even though the engine implements it (FEAT-rj17xj closed with the node wiring undone) [find:engine-runtime] (high/unfinished) · area: nodes/wait.go + engine approval · confidence: high

The Wait node refuses anything over MaxWaitDuration=1h (wait.go:41) even though it now suspends to storage (the engine allows 7 days, MaxWaitTTL). The engine's WaitModeWebhook/WaitModeApproval, /resume/{token} and /approve/{token} page are never reached, because no executor emits those modes. Wait's resume=webhook/form is refused with the stale text 'this server does not do yet'.

Evidence: Shared :8090, script <W>/t_wait_long.py: Wait 1 day fails with `a wait of 24h0m0s is longer than this server's limit of 1h0m0s`, and 2 hours fails the same way. With resume=webhook, the import marks it blocking and POST /run returns 422 `resuming on "webhook" needs the execution to be suspended to storage and woken later, which this server does not do yet`. grep: WaitModeApproval/WaitModeWebhook appear only in internal/engine and api/handlers/resume.go. FEAT-rj17xj's 'REMAINING: webhook/form wait modes ... nodes-owned future work' is still open, its acceptance boxes are unchecked, and the ticket is done.

n8n behavior: Wait supports intervals of days, a specific time, On Webhook Call and On Form Submitted. 'Send and wait' approvals build on it.

Impact: Drip and follow-up flows ('wait 2 days'), human-in-the-loop approvals and callback integrations cannot run, and the shipped approval page is effectively dead code.

Suggested fix: Raise the Wait cap to MaxWaitTTL. Implement resume=webhook ($execution.resumeUrl, limitWaitTime) and an approval mode that emits SuspendError. Refresh node descriptions and import diagnostics.

Files: nodes/wait.go, internal/engine/approval.go, internal/engine/wait_service.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-rj17xj

---
### Loops drop binary data and pairedItem lineage for every batch after the first and for the done output [find:engine-runtime] (high/bug) · area: nodes/loop.go serialization · confidence: high

Pending and collected items are round-tripped through itemsToAny/anyToItems (loop.go:208-228), which keep only JSON. Binary and Paired are lost from batch 2 on and in `done`.

Evidence: Script <W>/t_loop_binary2.py on private :8101 with binary storage enabled: manual -> splitOut(3) -> httpRequest(responseFormat=file) -> loop(batch 1) -> noOp -> loop, done -> noOp. Flags per item (B = binary, P = lineage intact, l = lost): http [BP,BP,BP]; loop run0 [BP]; body run1 [-l]; body run2 [-l]; loop done [-l,-l,-l]. Imported SIB workflows show pairedItem lost:true from iteration 2.

n8n behavior: Each batch keeps its binary, and done items keep their pairedItem to the original items.

Impact: Loops over files or attachments (upload each to Drive/S3, send each image) silently lose payloads for items 2..N. `$('Node').item` inside or after loops fails with 'changed the item correspondence'.

Suggested fix: Keep full workflow.Item values (Binary, Paired) in runner-held loop state (same fix as the loop-state finding). Stamp done items with lineage to the original upstream items.

Files: nodes/loop.go

Existing tickets: FEAT-sar60r

---
### Loop state embedded in items makes trace size and memory quadratic and leaks $loop (all pending and collected items) into body nodes' $json [find:engine-runtime] (high/perf) · area: nodes/loop.go + trace persistence · confidence: high

Every dispatched first item carries the entire remaining and collected dataset under `$loop`. That payload is persisted per node run (input and output), and every body node can see it and forward it to external systems.

Evidence: Script <W>/t_loop_many.py: an imported SIB loop with a NoOp body over tiny items {arr:i}. GET /executions/{id} = 66 KB for 25 items, 187 KB for 50 and 593 KB for 100. The Set body's $json = {"$loop":{"collected":[...],"pending":[...]},"arr":2,"copy":2}. The lease-race run (100 x 2 KB items) needed more than 1 s just to persist its trace.

n8n behavior: Body nodes see only their batch items, and trace size grows linearly.

Impact: 1,000 items x 2 KB gives multi-GB trace rows and worker memory. An HTTP body `={{ $json }}`, an auto-mapped DB insert or a Sheets append ships `$loop` externally. The large trace also feeds the lease-expiry re-run.

Suggested fix: Move state out of items into runner-held loop state. Strip any residual bookkeeping before a node sees its input.

Files: nodes/loop.go, internal/engine/service.go

Existing tickets: FEAT-sar60r

---
### Imported n8n loops are capped at 100 batches (kilasflow.loop maxIterations default), and because of the duplicate-key bug the failing run never ends [find:engine-runtime] (high/parity-gap) · area: nodes/loop.go + importer · confidence: high

splitInBatchesToKilas sets only batchSize, so imports inherit DefaultLoopMaxIterations=100. Batch 101 fails, and its failure row collides with loop run 0 (see the duplicate-key finding).

Evidence: Private :8101, <W>/t_loop_many.py 150 1: SIB v3 with batch 1 over 150 items fails at iteration 101 ('exceeded its maximum of 100 iterations'). The log then shows `persist node "l" run: duplicated key` every 15 s, and the execution stays running (202 node runs visible) until cancelled. 100 items succeeds.

n8n behavior: There is no iteration cap. The loop runs until the items are exhausted.

Impact: Any imported loop with more than 100 batches (for example 101 rows at batch size 1) fails and keeps re-running.

Suggested fix: Have the importer set maxIterations to MaxLoopIterations (10,000), or compute the runaway bound at runtime as ceil(initial items / batch size) once loop state is runner-held.

Files: nodes/loop.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-sar60r

---
### Short Wait nodes take up to 60 s because timer waits only settle on a 1-minute sweep, which also breaks synchronous webhook responses [find:core-node-parity] (high/perf) · area: engine waits · confidence: high

Any Wait longer than 0 becomes a durable suspension (SuspendError). Only the wait sweeper re-queues it, and the sweeper runs every minute by default with no config option. A 1-2 s rate-limit pause therefore costs 30-60 s, and a webhook in lastNode or responseNode mode with a Wait always times out (504).

Evidence: - Case wait_seconds (Webhook lastNode → Wait 2 s): n8n answers 200 after 2.03 s. KilasFlow answers 504 'The workflow did not finish before the response timeout' after 30 s; execution exec_01a0af2b-c9ec… ran 11:41:17.68 to 11:41:54.91 (37 s).
- Case wait_v1 (1 s): 504; the execution took 57 s.
Code: nodes/wait.go:145-175, internal/engine/wait_service.go:113-134 (sweepLoop), internal/engine/service.go:159-161 (default time.Minute, not wired to config).

n8n behavior: Waits under about 65 s run in-process and the webhook responds after the wait.

Impact: Wait v1.1 is used in 16 of the 100 templates (33 nodes), mostly as 1-5 s throttles inside loops. Loops slow down about 30-60x, and webhook flows containing a Wait always return 504.

Suggested fix: Sleep in-process (respecting context cancellation) for short pauses, or schedule an exact wake-up by timer or NOTIFY instead of relying on the sweep. Make the sweep interval configurable.

Files: /Users/izzadev/projects/k-flow/nodes/wait.go, /Users/izzadev/projects/k-flow/internal/engine/wait_service.go, /Users/izzadev/projects/k-flow/internal/engine/service.go

Existing tickets: FEAT-q81bq4, FEAT-rj17xj

## Acceptance criteria

- [ ] Graceful shutdown does not drain workers: in-flight executions stay running and are re-executed from scratch a
- [ ] Nested loops are mis-detected: the inner loop's entry edge is classified as a back edge, so the inner loop is 
- [ ] A Wait inside a loop silently ends the loop after the first batch and the execution reports success
- [ ] Timer waits resume only on the 1-minute expired-wait sweep, so 'Wait 2 seconds' takes about 54 s
- [ ] Durable waits are capped at 1 hour, and resume on webhook/form/approval is unreachable even though the engine 
- [ ] Loops drop binary data and pairedItem lineage for every batch after the first and for the done output
- [ ] Loop state embedded in items makes trace size and memory quadratic and leaks $loop (all pending and collected 
- [ ] Imported n8n loops are capped at 100 batches (kilasflow.loop maxIterations default), and because of the duplic
- [ ] Short Wait nodes take up to 60 s because timer waits only settle on a 1-minute sweep, which also breaks synchr
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---
## Progress (EngineWaits)

Landed in this slice (files: nodes/wait.go, internal/engine/wait_service.go, internal/engine/service.go, internal/scheduler/extract.go):

- Timer waits now resume at their own deadline. Every suspension arms an exact `time.AfterFunc` for the deadline the wait row was written with (`waitTimers` in internal/engine/wait_service.go, armed from `suspend`, stopped with the sweep loop on shutdown). The periodic sweep stays as the floor for waits a process left behind, so a restart costs one sweep interval instead of losing the wait. This is the "Wait 2 seconds takes 54 s" and the webhook-504 finding: the wait no longer waits for the next tick.
- Sweep interval is configurable (`ServiceDeps.SweepInterval`, wired from `execution.wait_sweep_interval` by SecurityFront2; default in config 10s, NewService fallback 1 min).
- Durable wait cap raised from 1 h to `engine.MaxWaitTTL` (7 days) in nodes/wait.go; the node now suspends on `resume=webhook` (Mode webhook) and `resume=form` (Mode approval, the /approve page), with n8n's `limitWaitTime`/`limitType`/`limitAmount`/`limitUnit`/`limitAt` implemented as the limit that resumes a call-resumed wait (held as interval/until so nothing arriving is not a failure). `$execution.resumeUrl` was already exposed by the expression layer.
- Per-workflow run budget: `settings.executionTimeout` (n8n semantics, -1 = none) is honoured, capped by the new instance ceiling `execution.max_timeout` (ServiceDeps.MaxTimeout). The worker lease is no longer the same number as the run timeout (EngineCore's heartbeat) so a longer budget cannot be reclaimed mid-run.
- Instance default timezone: `scheduler.DefaultTimezone(extract, zone)` resolves an absent or n8n-`DEFAULT` workflow zone to the instance zone (`execution.default_timezone`), and the service fills `Request.Workflow` (ID/Name/Active/Timezone) so `$workflow.*` and `$now`/`$today` read the instance zone. Config + main.go wiring by SecurityFront2.
- Graceful drain: `Service.Drain(ctx)` (WaitGroup over `Start`'s worker goroutines) returns only after each worker has settled the run it was in. main.go wiring by SecurityFront2; the process-level half of the "SIGTERM re-runs a completed run" finding.
- Cancellation poll reads one status column (`ExecutionState`) instead of the whole execution record; also exposed on the service for the webhook await (WebhookParity).

Remaining in other slices (handed over, not this slice's files): nested loops / wait-inside-loop / loop binary+lineage / `$loop` in items / maxIterations+`options.reset` on import — EngineFlow (loop state is runner-owned as of 9df1c1c, nested-loop scheduling rewrite in progress) and ImporterTail (import defaults); shutdown drain call in cmd/kilasflow/main.go and the config keys — SecurityFront2; `$execution.resumeUrl` in `ExpressionContext` — ExpressionParity.

### Landed SHAs and evidence (EngineWaits)

- `b41e9c4` — nodes/wait.go, internal/engine/wait_service.go (waitTimers/sweepLoop/armWaitTimer/suspend arming), internal/engine/wait_service_test.go, internal/engine/multiprocess_test.go. Exact per-suspension timers, 7-day cap, webhook/form resume modes with n8n's limit, worker drain assertions.
- `591b3c4` (EngineCore's commit; my hunks rode along, as they asked) — internal/engine/service.go: `ServiceDeps` wait/timeout fields, `pollCancellation` light read, `ExecutionState` delegation, and the shutdown drain itself (`workers` WaitGroup in `Start` + `Service.Drain(ctx)`).
- `6e03f2c`, `5d46f4e` — shared-file hunks for BUG-aede06 (run budget, instance timezone, poll), which is why they are listed on that ticket.

Evidence (scoped, run in the working tree and re-run in a clean worktree at b41e9c4):

```
go build ./internal/engine/ ./internal/scheduler/ ./nodes/            # ok
go test ./internal/engine/ -count=1                                   # ok  4.1s (whole package)
go test ./internal/engine/ -run 'TestExpiredWaitsResolveOnTheirOwnDeadline|TestDrainDoesNotWaitForeverOnAStuckNode' -count=1   # ok
go test ./internal/scheduler/ -count=1                                # ok  0.59s
go test ./nodes/ -run Wait -count=1                                   # ok  0.56s
go test ./internal/engine/ -run 'TestGracefulShutdownHandsNothingHalfDone' -count=1   # ok
```

What that proves: a 50 ms timer wait is re-queued inside a second without anyone calling SweepWaits (the old behaviour needed the minute tick, which is what made "wait 2 seconds" take 54 s); an unanswered approval still fails by name at its deadline; `SweepWaits` is idempotent; `Drain` returns only after the in-flight run is durably cancelled and obeys its bound on a node that ignores cancellation; a 2-day wait is accepted and a 30-day one is refused naming `MaxWaitDuration` (= engine.MaxWaitTTL, 7 days); `resume=webhook` suspends in webhook mode, `resume=form` in approval mode, and a limited call-resumed wait is held as interval/until.

### Still open on this ticket (other slices, not verified here)

- Nested loops and wait-inside-loop: EngineFlow's runner rewrite (loop state is runner-owned as of 9df1c1c; nested-loop scheduling rewrite and `Checkpoint.NodeState` in flight under BUG-c241hm). Not verified by me.
- Imported loops capped at 100 batches / dropped `options.reset`: ImporterTail (`splitInBatchesToKilas` in internal/interop/n8n/parameters.go).
- Graceful shutdown at the process level: `Service.Drain(ctx)` exists and is tested, but cmd/kilasflow/main.go must call it after the server drains, bounded by `server.shutdown_timeout` — SecurityFront2 (main.go is theirs this wave). Until that call lands, a real SIGTERM still exits without waiting for the workers, which is the half of the finding the unit test cannot cover.
