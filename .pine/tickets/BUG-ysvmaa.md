---
id: BUG-ysvmaa
title: 'Engine waits/loops: 1-min sweep, 1h cap, shutdown drain, nested loops, wait-in-loop, lineage'
status: testing
priority: high
labels:
    - engine
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T03:12:18Z"
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

## Work (EngineRemnants 2026-09-20, verification slice)

Verified every remaining finding on this ticket against the tree, and closed the
two that had landed without a test. Commits: `06846cc` (loop resume + loop item
fidelity tests), `677c513` (loop-reset diagnostic), plus `bcd5787`/`606355b`
carry the `QueueManualLatest` signature this slice shares with BUG-aede06.

Per finding, what closes it:

- **Graceful shutdown does not drain workers** (high). Closed by `5990e7f`
  (SecurityFront2): `cmd/kilasflow/main.go:645-657` calls `runtime.Drain(ctx)`
  after the listener closes, bounded by `cfg.Server.ShutdownTimeout`, and logs a
  worker that outlived the window instead of exiting silently. Worker side:
  `Service.workers` WaitGroup in `Start` + `Service.Drain`. Tests:
  `TestGracefulShutdownHandsNothingHalfDone`, `TestDrainDoesNotWaitForeverOnAStuckNode`
  (both pass).
- **Nested loops mis-detected** (high). Closed by EngineFlow's `findLoops`
  rewrite (`schedulingEdges` no longer hides the inner entry);
  `TestNestedLoopsIterateIndependently` passes, and the loop bound, cursor and
  collect tests (`TestLoopDispatchesOneBatchPerIteration`,
  `TestLoopCollectsEveryBatchOntoDone`, `TestLoopKeepsItsCursorWhenTheBodyReturnsNewItems`,
  `TestLoopFailsRatherThanTruncatingAtItsBound`) pin the surrounding semantics.
- **A Wait inside a loop ends the loop after the first batch** (high). The fix
  keeps loop scheduling state in `Checkpoint.NodeState` (runner.go:778 stores it,
  :864-866 restores it). It had **no test**, which is what this slice adds:
  `TestResumeInsideALoopProcessesEveryBatch` suspends in batch one, resumes
  through the approval token on a *fresh* worker, and asserts all three batches
  ran (`done` carries 3 items). Bite check: with `checkpoint.NodeState` no longer
  written the test fails exactly as the finding reports it (`done carried 1
  items, want all 3 batches: the loop ended after the first one`).
- **Timer waits resume only on the 1-minute sweep** / **Short Wait nodes take up
  to 60 s**: closed by EngineWaits `b41e9c4` (per-suspension `time.AfterFunc`
  arming, sweep as the floor, configurable interval);
  `TestExpiredWaitsResolveOnTheirOwnDeadline` proves a 50 ms wait requeues inside
  a second.
- **Durable waits capped at 1 hour, webhook/form resume unreachable** (high).
  Closed: `engine.MaxWaitTTL` = 7 days (`internal/engine/approval.go:86`),
  refused past it (`TestWaitNeedsABoundableDeadline` covers the 30-day refusal
  and the ~24 h default), `resume=webhook`/`resume=form` implemented in
  `nodes/wait.go` with n8n's `limitWaitTime`/`limitType`/`limitAmount`/`limitUnit`
  (nodes/datetime_test.go:351+).
- **Loops drop binary data and pairedItem lineage after the first batch and on
  `done`** (high). Lineage already had a test
  (`TestDollarItemResolvesThroughHttpAndALoop`); the binary half had none, so
  `TestLoopCarriesBinaryAndLineageThroughEveryBatch` now asserts an item with a
  `BinaryRef` reaches *every* batch with its file and its source lineage, and that
  the collected `done` output keeps both.
- **`$loop` embedded in items / quadratic state** (high). Closed by EngineFlow
  (runner-owned loop state, `9df1c1c`); `TestLoopDoesNotWriteItsStateOntoItems`
  asserts no item carries a `$loop` key and the payload does not grow with
  batches (`internal/engine/loopstate_test.go`).
- **Imported loops capped at 100 batches** (medium). Closed by ImporterTail:
  `splitInBatchesToKilas` writes the instance ceiling
  (`workflow.MaxLoopIterations`) rather than the loop's own default, covered by
  `TestSplitInBatchesCarriesTheLoopBound` (internal/interop/n8n/waitsubworkflow_test.go).
- **`options.reset` on import**. Carried and reported lossy — the value is kept
  so a round trip returns the node as authored, and the restart semantics are
  deliberately *not* implemented. What this slice fixed: the diagnostic reason
  claimed "this was not carried", which contradicted the code that stores it
  (`677c513`), and the carry/report pair had no test
  (`TestSplitInBatchesKeepsAndNamesItsResetOption` now pins both). This is the
  one intentional residual on the ticket, documented at the surface that matters
  (the import diagnostic) rather than silently approximated.

Scoped proof (each package run once, working tree at `677c513`):

```
go test ./internal/engine/ -run 'TestResumeInsideALoopProcessesEveryBatch|TestLoopCarriesBinaryAndLineageThroughEveryBatch|TestLoopDoesNotWriteItsStateOntoItems|TestNestedLoopsIterateIndependently|TestDollarItemResolvesThroughHttpAndALoop|TestSuspendResumeContinuesWithExactUpstreamData|TestExpiredWaitsResolveOnTheirOwnDeadline' -count=1   # ok
go test ./internal/interop/n8n/ -count=1   # ok 0.351s (whole package)
```

Not re-verified here: the adversarial live repro against stub/n8n (the ticket's
last acceptance line) is the wave's end-to-end pass, not a package test.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (8):
  - `b22092b1` — BUG-aede06 BUG-ysvmaa: record testing state and the per-finding evidence
  - `677c5132` — BUG-ysvmaa: stop the loop-reset diagnostic claiming the value was dropped
  - `06846ccd` — BUG-ysvmaa: prove a resumed run inside a loop finishes every batch
  - `5990e7fd` — BUG-ysvmaa BUG-aede06: drain workers after the listener closes, and pass the instance timezone — ops/engine
  - `6202bb13` — BUG-ysvmaa: name the node runs a resumed-wait failure shows — test diagnostics
  - `bb56e82f` — BUG-ysvmaa BUG-aede06: record landed SHAs, scoped evidence and what remains per finding
  - `b41e9c4e` — BUG-ysvmaa: exact wait timers, 7-day cap, webhook/form resumes, worker drain — engine/wait
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  639 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  695 ++++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  534 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  794 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  795 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 +++++++
 .pine/tickets/BUG-fv5fer.md                        |  635 +++++
 .pine/tickets/BUG-gaavr5.md                        |  813 ++++++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++++
 .pine/tickets/BUG-hm76dq.md                        |  569 +++++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  524 ++++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++++
 .pine/tickets/BUG-npfz43.md                        |  487 ++++
 .pine/tickets/BUG-pwckhd.md                        |  512 ++++
 .pine/tickets/BUG-qmgz2f.md                        |  630 +++++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++++
 .pine/tickets/BUG-rjd6fm.md                        |  721 ++++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++++
 .pine/tickets/BUG-tcqkad.md                        |  639 +++++
 .pine/tickets/BUG-txc9xg.md                        |  520 ++++
 .pine/tickets/BUG-wdypd2.md                        |  680 +++++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++++
 .pine/tickets/BUG-y57cz4.md                        |  617 +++++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 73298 insertions(+), 4725 deletions(-)
```

## Reopened by review (2026-09-20) — Important
- **Per-item suspension loses the remaining items**: `runPerItem` appends the items a suspending node has not reached onto `state.pending` *after* `runner.invoke` marshalled the suspension checkpoint (`internal/engine/runner.go:1041` copies `state.pending`; the mutation at :1081-1091 is never read again), so the checkpoint does not carry them and a resumed run drops every item after the first. Trigger: a node with `onError: continueRegularOutput/continueErrorOutput` (perItemTolerance, :1780) that also suspends — e.g. Wait with On Error = Continue over three items; after resume `done` carries 1 instead of 3. Fix: schedule the remaining per-item work before the checkpoint is marshalled (or copy it into the checkpoint explicitly), plus a regression test that suspends inside a per-item node.

## Fix (2026-09-20, status doing) — commit 9b690d8
Fixed by moving the checkpoint out of `invoke` and into the caller:
`invoke` returns the suspension bare and `suspendWithCheckpoint` (runner.go) marshals
`snapshotCheckpoint` at the call site, so `runPerItem` appends the items the suspending
node had not reached to `state.pending` *before* the snapshot is taken (`runNode` attaches
its checkpoint the same way). No decode/re-marshal dance, and the ordering is now correct
by construction: the stack the checkpoint carries is the caller's.

Regression test `TestResumeOfAPerItemSuspendProcessesEveryItem`
(internal/engine/wait_service_test.go): manual -> 3 items -> wait (`onError
continueRegularOutput`, interval resume) -> done. `awaitExecutionStatus` is used for the
timer requeue, and the assertions are per item ("reached exactly once"), not a bare count.
`waitSuspendOnce` gained a `mode`/`expiresAt` pair (approval when empty, which the loop
test keeps) so the same fake can stand for either resume path.

Pre-fix proof in a detached worktree at 47a4d9b with only the new test copied in:
```
$ go test ./internal/engine/ -run TestResumeOfAPerItemSuspendProcessesEveryItem -count=1
--- FAIL: TestResumeOfAPerItemSuspendProcessesEveryItem (0.06s)
    wait_service_test.go:625: item 2 reached the node after the wait 0 times, want exactly once; it saw map[1:1]
    wait_service_test.go:625: item 3 reached the node after the wait 0 times, want exactly once; it saw map[1:1]
    wait_service_test.go:630: the waiting node ran 1 times, want one per item: 1
FAIL
```
Post-fix (worktree removed, fix in place):
```
$ go test ./internal/engine/ -run TestResumeOfAPerItemSuspendProcessesEveryItem -count=1
ok  	github.com/kilaslabs/kilas-flow/internal/engine
$ go test ./internal/engine/ -count=1
ok  	github.com/kilaslabs/kilas-flow/internal/engine	3.831s
```
No existing test was loosened. runner.go also carries FixImporterFindings' BUG-cq4yk3
response-capture hunks, which shared the file.

