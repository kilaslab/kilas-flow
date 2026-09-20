---
id: BUG-aede06
title: 'Engine policy: timeouts, timezone, error workflow, live progress, polls, manual triggers'
status: doing
priority: medium
labels:
    - engine
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:33:12Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 10 finding(s) from dims: find:engine-runtime, find:importer-fidelity.

---
### executeOnce and alwaysOutputData are only 'dropped' notes, so imported workflows multiply side effects and stop branches n8n would continue [find:importer-fidelity] (high/parity-gap) · area: engine + importer (node settings) · confidence: high

FEAT-a6yg3n deferred these two settings ('report as diagnostics until they have equivalents'). They are still severity dropped, so the workflow activates and runs with different semantics, and no follow-up ticket exists.

Evidence: Live probes. executeOnce: Manual -> 3 items -> HTTP (executeOnce:true, POST 127.0.0.1:8093/execute-once); KilasFlow's run succeeded and the stub got 3 POSTs (once_probe_kf.py). alwaysOutputData: Filter 2.2 that passes nothing (alwaysOutputData:true) -> Set {reached:true}; n8n emits [{}] and the Set outputs {"reached":true}, while in KilasFlow the Set produces nothing (always_probe.py, sbs2_always-probe.json). Corpus: executeOnce on 23 nodes in 9 templates, e.g. 3121/3442 'Render Final Video' (a paid API called once per item), 2878 chainLlm (N LLM calls), 1534 Slack notification, and 3770 Redis writes. alwaysOutputData on 41 nodes in 17 templates, e.g. 2803 Postgres 'Check Data on Database Is Exist', whose 'insert if missing' branch is never reached.

n8n behavior: executeOnce runs the node once with the first item. alwaysOutputData emits one empty item when the node outputs nothing.

Impact: Duplicate paid calls and notifications; conditional 'not found' branches silently never run.

Suggested fix: Implement both settings in the runner and map them on import and export. Until then raise them to blocking, or to lossy with a concrete warning.

Files: internal/engine/runner.go, internal/interop/n8n/n8n.go

Existing tickets: FEAT-a6yg3n (done, deferred), FEAT-nbqye0 (done)

---
### No live execution progress: node runs are persisted and node events published only after the whole graph finishes, and node.started is never emitted [find:engine-runtime] (high/ux) · area: engine/service trace + events · confidence: high

The trace loop runs only after runner.Run returns (service.go:305-358). The node.started event type is defined but no engine code produces it.

Evidence: Shared :8090, script <W>/t_progress.py: Manual -> HTTP /fast -> HTTP /sleep?s=6 -> NoOp. GET /executions/{id} at 0, 1.5, 3.0 and 4.5 s returned running with nodeRuns=0. At 6.0 s it returned succeeded with nodeRuns=4, although the first HTTP call finished at about 0.1 s. grep: events.NodeStarted appears only in the api/handlers schema. FEAT-5mvech itself notes that node.started has no producer.

n8n behavior: The editor shows a spinner on each node and its output as it finishes (nodeExecuteBefore/After push events).

Impact: Long runs (AI agents, loops, anything near the 60 s timeout) look frozen in the editor and executions view. After a crash or reclaim nothing is visible. The editor's event stream gets one burst at the end.

Suggested fix: Emit node.started before Execute, and persist and publish each NodeRun as it completes through a runner callback. Keep UpdateRuntime as the terminal write.

Files: internal/engine/service.go, internal/engine/runner.go, internal/events/events.go, web/src/lib/workflow-editor/event-stream.svelte.ts

Existing tickets: FEAT-9ns8cr, FEAT-5mvech

---
### alwaysOutputData and executeOnce are not implemented by the runner (the importer drops them) [find:engine-runtime] (high/parity-gap) · area: engine/runner node settings · confidence: high

isLive prunes any node whose input is empty (runner.go:810-836), and nothing reads alwaysOutputData or executeOnce. A node that should emit one empty item stops the branch, and executeOnce nodes run for every item.

Evidence: Script flags_wf.py: Split 3 items -> Filter(drops all, alwaysOutputData) -> HTTP stub /after-empty; Split -> HTTP stub /once (executeOnce). n8n 2.33.7: /after-empty 1 call, /once 1 call. KF :8090 (<W>/t_flags.py kf): AfterEmpty skipped (0 calls), /once 3 calls.

n8n behavior: alwaysOutputData emits one empty item when a node returns nothing. executeOnce runs the node with only the first item.

Impact: 18/100 top templates use alwaysOutputData (38 nodes) and 10 use executeOnce (27 nodes). Lookup-then-create flows silently stop, and 'once' notifications fire N times.

Suggested fix: Honour both as node settings in the runner. After Execute: if the output is empty and alwaysOutputData is set, emit [[{json:{}}]]. Before Execute: if executeOnce is set, pass only the first main item.

Files: internal/engine/runner.go, internal/interop/n8n/n8n.go

Existing tickets: FEAT-nbqye0

---
### Every execution is killed after execution.default_timeout (60 s stock), and the workflow's settings.executionTimeout is dropped on import [find:engine-runtime] (medium/parity-gap) · area: engine/service timeout + importer settings · confidence: high

runOnce gives every run the same 60 s budget (service.go:252, config.go:620). The importer keeps only the timezone setting.

Evidence: Shared :8090, script <W>/t_timeout.py: an n8n import with settings.executionTimeout=300 and three sequential 25 s HTTP calls. The import issue says 'settings other than the timezone ... not carried', and the execution failed at 60.1 s with execution.timeout ('SlowAPI3 ... context deadline exceeded').

n8n behavior: No execution timeout by default (EXECUTIONS_TIMEOUT=-1). A per-workflow timeout is honoured up to EXECUTIONS_TIMEOUT_MAX.

Impact: AI chains, paginated syncs, large batches and loops with pauses fail on KF. A timed-out run with a sizeable trace is also re-run (see the lease finding).

Suggested fix: Carry settings.executionTimeout (-1 = none) and apply min(workflow, instance max). Default to no limit, or a much larger one, for n8n-compatible installs. Decouple the lease from the run timeout.

Files: internal/engine/service.go, internal/config/config.go, internal/interop/n8n/n8n.go

---
### Execute Workflow in 'don't wait' mode still runs the child inline: the parent blocks for the child's full duration and fails if the child fails [find:engine-runtime] (medium/parity-gap) · area: engine/service InvokeWorkflow · confidence: high

InvokeWorkflow always runs the child inline and returns the child's error even when call.Wait is false (service.go:697-760).

Evidence: Shared :8090, script <W>/t_sub.py: child executeWorkflowTrigger -> HTTP /sleep?s=3; parent Manual -> executeWorkflow(fireAndForget) -> NoOp. The parent took 3.1 s, the same as mode each. <W>/t_sub_fail.py: with a failing child (500) the fireAndForget parent execution failed with `sub-workflow ... failed: ... status 500`.

n8n behavior: With 'Wait for sub-workflow completion' off, the parent continues immediately and is unaffected by the child's result.

Impact: Fan-out patterns serialize and count against the parent's 60 s timeout, and one failing child aborts the parent.

Suggested fix: For fire-and-forget, queue a real claimable child execution and return the input items immediately. Ignore the child's outcome in the parent.

Files: internal/engine/service.go, nodes/subworkflow.go

Existing tickets: FEAT-az620p

---
### A manual run of a workflow with several triggers fires every trigger at once, and the run API cannot choose one [find:engine-runtime] (medium/bug) · area: engine activeNodes + run API · confidence: high

An empty TriggerNodeID means every root runs (runner.go:966-973). RunWorkflowInputBody only has `input`, so every trigger emits the same item and shared downstream nodes run once per trigger.

Evidence: Shared :8090, script <W>/t_multitrigger.py: Manual + Schedule + Webhook triggers all feed HTTP POST stub /multi. POST /run {input:{x:1}}: all three triggers succeeded with the same {x:1} item, DoWork received 3 items and stub /multi was hit 3 times.

n8n behavior: A manual execution starts from the one trigger the user picked, with that trigger's item shape.

Impact: Test runs of real workflows (templates often pair Manual/Chat/Webhook/Schedule triggers) send duplicate writes and messages, and trigger-shape-dependent expressions get the wrong payload.

Suggested fix: Accept triggerNodeId on /run: default to the manual trigger if one exists, otherwise require a choice. Pass it as TriggerNodeID, and have the editor's Run button send the selected trigger.

Files: internal/engine/runner.go, internal/api/handlers/workflows.go, web/src/lib/workflow-editor

---
### No instance default timezone: schedules without settings.timezone run in UTC, while n8n uses the instance GENERIC_TIMEZONE [find:engine-runtime] (medium/parity-gap) · area: internal/scheduler timezone · confidence: high

documentTimezone returns an empty zone, which means UTC (extract.go), and config has no default-timezone key.

Evidence: Shared :8090, script <W>/t_sched_tz.py: import a Schedule Trigger v1.2 'every day at 09:00' without settings.timezone and activate it. GET /api/v1/schedules returns {cron:'0 9 * * *', timezone:null, nextRunAt:'2026-09-19T09:00:00Z'}, which is 16:00 WIB. Only 8/100 top templates set settings.timezone.

n8n behavior: A workflow timezone of DEFAULT resolves to GENERIC_TIMEZONE, which Schedule Trigger and $now use.

Impact: Almost every imported schedule shifts by the operator's UTC offset.

Suggested fix: Add an instance/tenant default timezone (config plus settings UI), apply it when a workflow has none, map n8n 'DEFAULT' to it, and show the effective zone and next run in the UI.

Files: internal/scheduler/extract.go, internal/scheduler/scheduler.go, internal/config/config.go

Existing tickets: FEAT-q81bq4

---
### No error-workflow mechanism: settings.errorWorkflow is dropped, Error Trigger and Stop and Error are unsupported placeholders, and a failed execution notifies nothing [find:engine-runtime] (medium/parity-gap) · area: engine failure path + importer · confidence: high

runOnce's failure path only writes the record and publishes an event. No errorTrigger or stopAndError node type exists, and the setting is not carried on import.

Evidence: Shared :8090: importing Error Trigger -> HTTP alert -> Stop and Error with settings.errorWorkflow reports `blocking Error Trigger ... unsupported placeholder`, `blocking Stop ... unsupported placeholder` and `dropped settings ... error workflow`. grep finds no errorWorkflow/errorTrigger code in internal/ or nodes/.

n8n behavior: The Error Workflow runs with {execution:{id,url,error,lastNodeExecuted,mode}, workflow:{id,name}} whenever an execution fails.

Impact: Production n8n setups rely on error workflows for alerting, so after migration failures go unnoticed. 1 top template uses errorTrigger and 3 use stopAndError.

Suggested fix: Add settings.errorWorkflow and an Error Trigger node. On a failed non-manual execution, queue that workflow with the n8n-shaped {execution, workflow} payload. Add a Stop and Error node.

Files: internal/engine/service.go, internal/interop/n8n/n8n.go, nodes/core.go

---
### The cancellation poll (every 100 ms per running execution) and webhook await (every 25 ms per waiting request) load the full execution record, including every node-run payload [find:engine-runtime] (low/perf) · area: engine pollCancellation + webhook await · confidence: medium

Both use executions.Get, which loads all execution_node_runs rows and their blobs (executions.go around line 275).

Evidence: Code: service.go:479-501 and webhook.go:364-386. A 20k-item execution's GET returned 11.4 MB in 0.4 s (<W>/t_big.py). For a resumed run the pre-wait trace is already persisted, so each 100 ms poll re-reads all of it.

n8n behavior: Not applicable (n8n uses push and in-memory hooks rather than DB polling).

Impact: CPU and DB load on SQLite's single connection under concurrency and after resumed waits.

Suggested fix: Add a lightweight status/lease query for polling, and use LISTEN/NOTIFY or an in-process completion channel where available.

Files: internal/engine/service.go, internal/webhook/webhook.go, internal/repository/executions.go

---
### Activating a parent does not check that its Execute Workflow targets are active; the run then fails with 'repository record not found: active workflow' [find:engine-runtime] (low/ux) · area: nodes/subworkflow + activation validation · confidence: high

The requirement that sub-workflows be active matches n8n 2.x, but KF checks it only at run time, and the error message is storage jargon.

Evidence: Shared :8090: a native parent calling a child that is not activated failed with `execute node "pe": node "pe": repository record not found: active workflow`. On live n8n 2.33.7, publishing such a parent returns 400 'Cannot publish workflow: Node "Call" references workflow ... which is not published. Please publish all referenced sub-workflows first.'

n8n behavior: Publishing the parent is refused until the referenced sub-workflows are published.

Impact: Imported multi-workflow suites (17/100 top templates are sub-workflows) fail on first run with an unhelpful message.

Suggested fix: Validate literal sub-workflow references on activation and in the editor, and reword the runtime error as 'sub-workflow <name> is not active'.

Files: nodes/subworkflow.go, internal/engine/service.go

Existing tickets: FEAT-az620p

## Acceptance criteria

- [ ] executeOnce and alwaysOutputData are only 'dropped' notes, so imported workflows multiply side effects and sto
- [ ] No live execution progress: node runs are persisted and node events published only after the whole graph finis
- [ ] alwaysOutputData and executeOnce are not implemented by the runner (the importer drops them)
- [ ] Every execution is killed after execution.default_timeout (60 s stock), and the workflow's settings.executionT
- [ ] Execute Workflow in 'don't wait' mode still runs the child inline: the parent blocks for the child's full dura
- [ ] A manual run of a workflow with several triggers fires every trigger at once, and the run API cannot choose on
- [ ] No instance default timezone: schedules without settings.timezone run in UTC, while n8n uses the instance GENE
- [ ] No error-workflow mechanism: settings.errorWorkflow is dropped, Error Trigger and Stop and Error are unsupport
- [ ] The cancellation poll (every 100 ms per running execution) and webhook await (every 25 ms per waiting request)
- [ ] Activating a parent does not check that its Execute Workflow targets are active; the run then fails with 'repo
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---
## Progress (EngineWaits)

Landed in this slice (internal/engine/service.go, internal/engine/wait_service.go, internal/scheduler/extract.go, nodes/wait.go):

- Run timeout: `settings.executionTimeout` (seconds; -1 = no timeout, n8n's own convention) is applied per run by `Service.runBudget`, capped by the new `ServiceDeps.MaxTimeout` (`execution.max_timeout`, validated in config). A workflow that names none still gets `execution.default_timeout`. Import carrying of the setting is ImporterTail's half.
- Instance timezone: `scheduler.DefaultTimezone` maps an absent or `DEFAULT` workflow zone to `execution.default_timezone`, and `Request.Workflow.Timezone` is now filled so `$now`/`$today`/`DateTime.local()` read it. Importer no longer stores the literal `DEFAULT` (which the compiler refuses); EngineFlow's `validateDocumentTimezone` accepts it as a belt-and-braces.
- Poll cost: the cancellation watcher (`pollCancellation`) reads one status column through the new `ExecutionState` store method instead of loading the whole execution record with every node-run payload, ten times a second. `*engine.Service.ExecutionState` is exposed for WebhookParity's 25 ms webhook await.
- Wait node resume modes (webhook/form) implemented in nodes/wait.go — see BUG-ysvmaa.

Remaining in other slices: node.started + incremental NodeRun persistence (EngineFlow's `Request.NodeRunSink`, my service-side writer once it lands), alwaysOutputData/executeOnce (EngineFlow runner + ImporterTail importer), manual trigger selection on /run (EngineFlow default + SecurityFront2 API + FrontendCore3 editor), error workflow + Error Trigger/Stop and Error (EngineCore failure path, ImporterTail n8n.go, ImporterTail nodes/core.go), sub-workflow activation validation (ImporterTail nodes/subworkflow.go + EngineCore).

### Landed SHAs and evidence (EngineWaits)

- `6e03f2c` — internal/scheduler/extract.go (`DefaultTimezone`), internal/scheduler/scheduler_test.go, internal/engine/service_test.go (light-poll and run-budget tests), internal/engine/wait_service.go (`workflowContext`).
- `5d46f4e` — repair commit: the previous partial stage had truncated the `workflowContext` block; the tip is the correct one.
- `591b3c4` (EngineCore's commit; my hunks rode along) — internal/engine/service.go: `ServiceDeps.MaxTimeout`/`DefaultTimezone`, `runBudget` + `ExecutionTimeoutSetting`, `pollCancellation` reading `ExecutionState`, the `ExecutionState` delegation.

Evidence (scoped, and re-run in a clean worktree at HEAD):

```
go test ./internal/engine/ -count=1                                  # ok  4.1s
go test ./internal/engine/ -run 'TestAWorkflowKeepsTheRunBudgetItAsksFor|TestCancellationReachesARunningExecutionThroughTheStatusRead' -count=1   # ok
go test ./internal/scheduler/ -run TestAnUnnamedWorkflowTimezoneResolvesToTheInstanceZone -count=1   # ok
```

What that proves: a workflow declaring `settings.executionTimeout` runs past the instance default and is still capped by the instance ceiling (and `-1` means no timeout); a cancellation written straight to the store by another process reaches a running execution through the one-column status read, and the poll makes no full-record read at all (the test's decorated store fails if it does); an absent or `DEFAULT` workflow zone resolves to the instance zone (`0 9 * * *` → 02:00Z for Asia/Jakarta), an author's own zone is untouched, and an unresolvable instance zone is never written into a schedule row (robfig would refuse the spec and the schedule would stop firing).

### Still open on this ticket (other slices)

- Live progress (`node.started`, incremental node-run persistence): EngineFlow's `Request.NodeRunSink` (BUG-c241hm) plus my service-side writer. Not landed when this note was written, so nothing of it is claimed here. `events.NodeStarted` already exists and needs no change.
- `alwaysOutputData` / `executeOnce`: EngineFlow (runner honours `node.Settings`) + ImporterTail (carry them in `errorHandlingSettings`, drop the two "dropped" diagnostics).
- Manual trigger selection on `POST /run`: EngineFlow (default to the manual trigger when `TriggerNodeID` is empty) + SecurityFront2 (`internal/api/handlers/workflows.go`) + FrontendCore3 (editor Run button sends the selected trigger).
- Error workflow: EngineCore (failure path queues `settings.errorWorkflow`) + ImporterTail (carry the setting; `errorTrigger` and `stopAndError` in nodes/core.go).
- Sub-workflow activation validation (the sibling finding on this ticket): ImporterTail (`nodes/subworkflow.go`) + EngineCore.
- Config keys `execution.wait_sweep_interval`, `execution.max_timeout`, `execution.default_timezone` and the main.go wiring for `SweepInterval`/`MaxTimeout`/`DefaultTimezone`/`Drain`: SecurityFront2 (agreed key names and signatures, see their thread).

## Work (EngineCore 2026-09-20, handover slice)
- Live progress (finding "No live execution progress"), commits ff3eb92 + 81d3f55 + d99c365: node rows are persisted and their events published the moment the runner appends them (`Request.NodeRunSink`), and `node.started` is published from `Request.NodeStartSink` just before each executor runs (EngineFlow 976b2ce) — so a run that takes minutes is readable while it runs and the node a run is sitting on lights up. The two writers cannot double-publish: `CreateNodeRuns` returns only the rows it created and the terminal flush announces exactly those, keyed by sequence; the suspend path skips rows the live writer already wrote. Proven red pre-fix: `TestNodeProgressIsDurableAndPublishedBeforeTheRunEnds` fails against the old service ("no node run is durable while the run is in flight") and passes now, with one start + one completion event per node replayed from the broker.
- Error workflow (finding "No error-workflow mechanism"), commit 1d8191e + a8700dd: a failed (never cancelled) run starts the workflow `settings.errorWorkflow` names, from its Error Trigger, with n8n's `{execution:{id,mode,lastNodeExecuted,error}, workflow:{id,name}, trigger:{mode}}` payload, as its own execution parented to the failure; best effort and logged, self-reference refused. New nodes `kilasflow.errorTrigger` (root; hands the error object through) and `kilasflow.stopAndError` (terminal; fails the run with the author's message — literal, expression-marked or an inline `{{ }}` template — or the imported `errorObject` JSON text). Importer carries `errorWorkflow` in and out and no longer calls it unmapped; the dropped-settings reason now names only what genuinely remains (execution order, save-data flags). `ServiceDeps.ErrorTriggerType` still needs wiring in main.go for the trigger branch to be selected rather than every root.
- Sub-workflow activation validation, commit 3e1fbda: `WithSubworkflows(nodes.SubworkflowCalls)` refuses to activate a document whose Execute Sub-workflow / Workflow Tool node calls a workflow that is missing or not active, naming the node; nothing is pinned on a refusal; nil keeps the old behaviour.
- Scoped proof at each commit and again at HEAD: `go test ./internal/engine/ ./internal/repository/ ./nodes/ ./internal/database/ ./internal/interop/n8n/ -count=1` → all ok.
