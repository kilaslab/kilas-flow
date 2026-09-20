---
id: BUG-c241hm
title: 'Engine scheduling: branch order, fan-in, onError modes, disabled nodes, continue-on-fail'
status: doing
priority: high
labels:
    - engine
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:46:24Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 6 finding(s) from dims: find:engine-runtime.

---
### Continue-on-fail works per node, not per item: one failing item discards the successful items' results and the remaining items never run [find:engine-runtime] (high/bug) · area: engine/runner error tolerance · confidence: high

Any executor error replaces the node's whole output with one `$error` item per input item (runner.go:526-551). Executors stop at the first failing item, so later items are skipped, and the results of items already sent are thrown away.

Evidence: Script peritem_wf.py: Split 3 items {p: ok1|fail|ok3} -> HTTP GET stub/{{$json.p}} with continueOnFail/onError=continueRegularOutput -> NoOp. n8n 2.33.7: output [ {ok,...}, {error}, {ok,...} ], and stub /ok3 was called. KF :8090 (<W>/t_peritem.py kf): output [ {$error}, {$error}, {$error} ], stub /ok3 never called, status succeeded.

n8n behavior: Each item is processed. Failed items become {error} items (or go to the error output), and successful items keep their results.

Impact: Bulk senders and syncs using continue-on-fail (9+ top templates) silently skip every item after the first failure and lose the successful responses. A retry re-sends items that already succeeded.

Suggested fix: Make error tolerance per item, like n8n's continueOnFail() inside the item loop: keep successful items and emit an error item only for failed ones. Apply the same to the error-output mode.

Files: internal/engine/runner.go, nodes/http.go, nodes/executors.go

Existing tickets: FEAT-a6yg3n

---
### n8n onError modes are not executed: continueRegularOutput stops the workflow, and a wired continueErrorOutput branch makes the workflow unrunnable [find:engine-runtime] (high/parity-gap) · area: engine error handling + importer · confidence: high

The runner only knows settings.continueOnFail and has no error output port. The importer drops onError without mapping continueRegularOutput to continueOnFail (n8n.go:1566). The n8n 1.x+ UI writes onError, not continueOnFail.

Evidence: Script onerr_wf.py, one workflow on both engines. Hook -> HTTP (stub /fail 500, onError continueRegularOutput) -> NoOp; Hook -> HTTP (continueOnFail) -> NoOp; Hook -> HTTP (onError continueErrorOutput) -> [ok NoOp, error NoOp]. n8n 2.33.7 (<W>/t_onerr_n8n.py): success; Regular and Legacy emit {error:{message,name,...}}; ErrOut emits [[], [input+error]] and ErrBranch runs. KF (<W>/t_onerr_kf.py): the ErrBranch connection is 'held back because ErrOut declares no main port', and POST /run returns 422 `workflow node "e3" is disconnected from the trigger`. With the ErrOut part removed (<W>/t_onerr_kf2.py), the execution failed at node Regular (`request failed with status 500`).

n8n behavior: continueRegularOutput passes an error item on the main output. continueErrorOutput routes failed items to a separate error output.

Impact: 14/100 top templates have a wired error output and cannot run (1534, 2035, 2063, 2415, 2417, 2431, 2454, 2534, 2557, 2567, 2605, 2729, 2803, 3135). 8 use continueRegularOutput and stop instead of continuing.

Suggested fix: Add onError (stopWorkflow|continueRegularOutput|continueErrorOutput) to node settings. Give nodes a dynamic `error` output that routes failed items (input json + error). Map legacy continueOnFail to continueRegularOutput.

Files: internal/engine/runner.go, internal/interop/n8n/n8n.go, internal/workflow/compiler.go

Existing tickets: FEAT-a6yg3n, FEAT-nbqye0

---
### Parallel branches run in alphabetical node-ID order (breadth-first) instead of n8n v1 order (depth-first by canvas position), so cross-branch $('Node') references fail [find:engine-runtime] (high/parity-gap) · area: engine/runner scheduling · confidence: high

runLoop sorts the ready set by node ID and runs the first entry (runner.go:402-416). Imported workflows keep n8n's random UUID node IDs, so branch order is effectively random, and a whole branch does not finish before the next one starts.

Evidence: Script order_wf.py, same workflow on both: Hook -> A1 (y=0, id zz-a1) -> A2 (id zz-a2); Hook -> B1 (y=300, id aa-b1). All nodes are HTTP calls to stub /order. n8n 2.33.7 (executionOrder v1) called A1, A2, B1. KF :8090 (<W>/t_order_kf.py) called B1, A1, A2. In a variant where B1's URL reads {{ $('A2').first().json.path }}, n8n succeeds (sawA2=/order) and KF fails with `$('A2') names a node that has not produced output in this run`.

n8n behavior: v1 runs each branch to completion before the next, ordered by output index and then by node position.

Impact: 48/100 top templates declare executionOrder v1. Cross-branch references fail, side effects happen in a different order, and the webhook lastNode response can come from a different node.

Suggested fix: Implement n8n v1 scheduling: an execution stack seeded by the trigger; per output, push children in position order (top->bottom, left->right); multi-input nodes wait in a waiting-execution map. Use ID order only as a last tie-breaker.

Files: internal/engine/runner.go

Existing tickets: EPIC-m42s3g

---
### A node fed by several branches on the same input runs once with concatenated items, instead of once per incoming branch as in n8n v1 [find:engine-runtime] (high/parity-gap) · area: engine/runner fan-in · confidence: high

dependenciesComplete plus nodeInput (runner.go:404, :838-848) concatenates every incoming edge on a port into a single invocation. n8n runs the node once per upstream run.

Evidence: Script fanin_wf.py: Hook -> Set A {src:A} and Hook -> Set B {src:B}; both into Limit(maxItems 1) input 0; Limit -> NoOp. n8n 2.33.7: Limit runs=2 ([A],[B]) and After runs=2. KF :8090 (<W>/t_fanin.py kf): Limit ran once with output [A], After ran once, and B's item was lost.

n8n behavior: The node executes once for each branch that delivers data (runIndex 0, 1, ...).

Impact: Branches that re-join a shared tail (common after IF/Switch) change results in Aggregate, Limit, Summarize, Remove Duplicates, Respond to Webhook and executeOnce nodes, and $runIndex and per-run side effects differ from n8n.

Suggested fix: In v1 mode, execute a single-input node once per upstream run that delivers data. Concatenate only for explicitly multi-input nodes (Merge). This shares a root fix with the v1 execution-order finding.

Files: internal/engine/runner.go

---
### Disabled nodes execute: the runtime has no disabled-node concept, so a switched-off node runs and a disabled trigger gets activated [find:engine-runtime] (high/parity-gap) · area: engine/runner + workflow model · confidence: high

The workflow document has no `disabled` field, and the runner never passes nodes through. The importer only warns 'this node will run'.

Evidence: Shared :8090, script <W>/t_disabled.py: Manual -> HTTP DELETE stub /delete-everything (disabled:true) -> NoOp, plus a disabled Schedule Trigger (every minute) wired to the same HTTP node. The import issue says 'n8n's disabled flag has no KilasFlow equivalent, so this node will run'. POST /run: all nodes succeeded and stub /delete-everything was hit 2 times. POST /activate succeeded and registered the disabled schedule; I deactivated it right away.

n8n behavior: A disabled node is skipped and passes its input through. A disabled trigger is not registered.

Impact: 8/100 top templates contain disabled nodes (15 nodes: Gmail, HTTP, MongoDB, Google Drive, Wait, Schedule/Webhook/Execute-Workflow triggers, chat models). Importing them fires sends and writes their authors switched off.

Suggested fix: Add `disabled` to the node model and an editor toggle. The runner treats a disabled node as pass-through (output[0] = main input). Activation skips disabled triggers.

Files: internal/workflow/document.go, internal/engine/runner.go, internal/interop/n8n/n8n.go

Existing tickets: FEAT-nbqye0

---
### Tolerated-failure items use `$error` instead of n8n's `error`, drop the input item and lose pairedItem [find:engine-runtime] (medium/parity-gap) · area: engine/runner errorOutput · confidence: high

errorOutput writes {"$error":{message,node}} (runner.go:1090-1123) with no pairedItem. n8n writes `error` (an object for HTTP, a message string for generic nodes), and its error-output branch carries the input json plus error.

Evidence: KF :8090 (<W>/t_onerr_kf2.py): Legacy continueOnFail output `[{"json":{"$error":{"message":"node \"Legacy\": request failed with status 500","node":"Legacy"}}}]` with no pairedItem. n8n (<W>/t_onerr_n8n.py): `[{"json":{"error":{"message":"500 - ...","name":"AxiosError",...}}}]`.

n8n behavior: Failed items are shaped as {error: ...}. On the error output they are the input item plus an error field.

Impact: Imported checks such as IF {{ $json.error }} exists never match, and `$('X').item` after the node fails.

Suggested fix: Emit `error` in the n8n shape, keep pairedItem to the input item, and merge input json with error for the error-output mode.

Files: internal/engine/runner.go

Existing tickets: FEAT-a6yg3n

## Acceptance criteria

- [ ] Continue-on-fail works per node, not per item: one failing item discards the successful items' results and the
- [ ] n8n onError modes are not executed: continueRegularOutput stops the workflow, and a wired continueErrorOutput 
- [ ] Parallel branches run in alphabetical node-ID order (breadth-first) instead of n8n v1 order (depth-first by ca
- [ ] A node fed by several branches on the same input runs once with concatenated items, instead of once per incomi
- [ ] Disabled nodes execute: the runtime has no disabled-node concept, so a switched-off node runs and a disabled t
- [ ] Tolerated-failure items use `$error` instead of n8n's `error`, drop the input item and lose pairedItem
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (EngineFlow 2026-09-20, engine/flow slice) — part 1: scheduling core landed

- Scheduler rewritten to n8n v1 semantics (stack + fan-in per branch). `preparedGraph` now carries outgoing edges sorted by output index then canvas position (top→bottom, left→right, ID only as tie-break); `runState` holds an execution stack; a node pushes one invocation per target carrying the items that branch delivered, so a node fed by two branches runs once per branch, each branch runs to its end before the next starts, and an untaken port delivers nothing (the old `isLive` prune and its cascade are gone — an undelivered node is never pushed and is recorded skipped at the end). The fallback that still runs a never-run node whose upstreams finished is gated on delivery, so an untaken arm cannot fire; it exists for convergence nodes (Merge) that need every branch at once. `findLoops`/`reopenLoops`/`closeIteration`/`iterationEdges`/`awaitingLoop`/`loopGraph`/`isLive` are deleted: a loop's `loop`/`done` ports and its runner-owned state are enough, and nested loops now work by construction.
- `Checkpoint.Pending` (`PendingNode{nodeID,input}`) carries the stack across a suspension; `Resume` restores it and no longer re-seeds the roots (which would re-run the trigger).
- `Request.NodeRunSink(index, run)` reports each trace row as it is appended (EngineWaits' incremental persistence). Runner-side node.started/completed events were deliberately NOT added: the live stream must go through the service's `projectTrace` (datastore cells are projected before redaction), and a raw event from the runner would bypass that. Told EngineWaits.
- Disabled nodes: `workflow.Node.Disabled`/`IRNode.Disabled`; the compiler skips a disabled node's required-parameter/credential/Validate checks (a switched-off Gmail node with a dead credential must not make a workflow unactivatable) and the runner never invokes it — it passes its first main input through, as n8n does. A disabled trigger is not seeded, so it never fires, and naming one as the trigger is refused.
- onError modes: `stopWorkflow`/`continueRegularOutput`/`continueErrorOutput` (legacy `continueOnFail` maps to continueRegularOutput); the compiler appends the node's own `error` output port for `continueErrorOutput` so a wired error branch compiles; the runner pads the executor's arity, routes failed items to that port (input json + error), and `ErrorItemKey` is now n8n's `error`.
- Compile-verified in an isolated worktree at HEAD with only these files; `go build ./internal/engine/ ./nodes/ ./internal/workflow/` clean. Two existing test expectations still to update in this ticket's next commit (fan-in per branch for a two-trigger manual run; per-item retry budgets under continueOnFail).

## Progress (EngineFlow 2026-09-20, engine/flow slice) — part 2: per-item tolerance, error branch, tests

- Per-item error tolerance: a node whose `onError` is continueRegularOutput/continueErrorOutput with a single item input (and no `executeOnce`, and not a loop entry) is invoked once per item, so a failing item costs that item only — the items that already succeeded keep their results and the ones after it still run (n8n's continueOnFail inside the item loop). Successful items keep their output; failures become n8n-shaped `error` items (`{message, node}`) with the input item's pairedItem; `continueErrorOutput` puts them on the node's own `error` port with the input json merged in. `ErrorItemKey` is now `error`, not `$error` (imported checks such as `{{ $json.error }}` match again).
- Delivery-driven pruning: a port that carried nothing delivers nothing, so the untaken arm is never invoked; the row is recorded at the delivery rather than at the end of the run, so a node inside a loop keeps one pruned row per iteration (EngineCore's trace-persist test). A node below a loop's `done` port is not reached while the loop is still dispatching, and a node below an unreached node is recorded skipped at the end of the run. The skip cascade deliberately does not travel around a loop's back edge.
- Typed attachments: a scheduled invocation merges its non-item inputs (chat model, memory, tools) from the nodes that have run, so an agent started by its trigger branch still finds its model (AINodes2's nodes/ai regression). `stampProvenance` now infers lineage from the ports an invocation actually read, so fan-in-per-branch does not mark every item lost.
- `Request.NodeStartSink(nodeID)` added beside `NodeRunSink` for EngineCore's node.started half of live progress; both are copied by the request clone. The runner still emits no node events itself: the live stream must go through the service's projectTrace.
- internal/scheduler/extract.go: a disabled schedule trigger is no longer registered on activation (EngineWaits handed the file over).
- Tests added (internal/engine/runner_test.go): `TestBranchesRunDepthFirstInCanvasOrder` (IDs reversed against the canvas: A1,A2,B1), `TestAFanInNodeRunsOncePerDeliveringBranch` (fan-in node and its tail run once per branch), `TestDisabledNodePassesItsInputThrough`, `TestDisabledTriggerNeverFires` (never seeded, named trigger refused), `TestContinueOnFailKeepsTheItemsThatSucceeded` (ok1,fail,ok3 → all attempted, successes kept, error item in n8n shape with lineage), `TestContinueErrorOutputRoutesFailuresToTheErrorBranch` (main = successes, error port = input+error, both branches run), `TestNestedLoopsIterateIndependently` (3 outer × 2 inner body runs, 6 items on done).
- Two existing expectations updated, because the behaviour they pinned is the bug: a manual run of a two-trigger workflow now runs the shared tail once per root branch (fan-in per branch), and a tolerated failure spends its retry budget per item (2 items × 3 attempts = 6 calls).
- Scoped proof (isolated worktree at 261c8ac with only these files): `go test ./internal/engine/ ./internal/workflow/ ./internal/scheduler/ ./nodes/ -count=1` all ok.

## Progress (EngineFlow 2026-09-20, engine/flow slice) — part 3: live repro + status

- Live verification (the ticket's own repro shape, no code-only close): `TestContinueOnFailSendsEveryItemToARealServer` runs the *product* HTTP node against a real `httptest` server with the SSRF policy allowing exactly that endpoint. Three items {p: ok1|fail|ok3}, the middle answered 500: the server logs `ok1,fail,ok3` (every item is sent, including the one after the failure), the node's output keeps the two successful responses and carries an `error` item for the failure; the same workflow *without* `onError` stops at the 500 and the server logs only `ok1,fail` — the exact difference the ticket reported against n8n 2.33.7.
- No n8n instance is reachable in this environment (nothing listens on 5678/8101; `scratchpad/work` is gone), so the n8n side of the comparison rests on the ticket's recorded evidence rather than a fresh side-by-side.
- Scoped proof after all parts: `go test ./internal/engine/ ./internal/workflow/ ./internal/scheduler/ -count=1` ok; `go test ./nodes/ -count=1` ok (26s, re-run after the typed-input fix).
- Still outside this slice (notified, not blocked on me): the n8n importer must set `Disabled` and carry `onError` into settings (ImporterTail); webhook registration must skip a disabled trigger (WebhookParity); the editor's disabled toggle and the error-port rendering are frontend work. The engine executes all of it correctly once the document carries it.
