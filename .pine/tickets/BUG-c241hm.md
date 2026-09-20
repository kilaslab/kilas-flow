---
id: BUG-c241hm
title: 'Engine scheduling: branch order, fan-in, onError modes, disabled nodes, continue-on-fail'
status: done
priority: high
labels:
    - engine
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:03:59Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (7):
  - `ee999099` — chore(corpus): regenerate the n8n import scoreboard — activatable 14→15, runnable 4→5
  - `606355b1` — BUG-aede06: take EngineCore's pruned-trace relaxation with the migration
  - `e01c7ab6` — BUG-c241hm: record testing state — engine/scheduler slice
  - `327de63f` — BUG-c241hm: live HTTP repro for per-item tolerance and onError modes — engine
  - `976b2ce2` — BUG-c241hm: per-item error tolerance, error branch port, delivery-driven pruning, nested loops — engine/scheduler
  - `261c8ac6` — BUG-c241hm: n8n v1 stack scheduling, fan-in per branch, disabled nodes, onError modes — engine/workflow
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
 .pine/tickets/BUG-c241hm.md                        |  154 ++
 .pine/tickets/BUG-cq4yk3.md                        |  338 +++
 .pine/tickets/BUG-dndnhn.md                        |   48 +
 .pine/tickets/BUG-esb9sh.md                        |  138 ++
 .pine/tickets/BUG-f9frth.md                        |  410 +++
 .pine/tickets/BUG-fv5fer.md                        |  172 ++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
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
 434 files changed, 61516 insertions(+), 4725 deletions(-)
```
