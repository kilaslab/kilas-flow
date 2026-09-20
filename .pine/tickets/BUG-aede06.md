---
id: BUG-aede06
title: 'Engine policy: timeouts, timezone, error workflow, live progress, polls, manual triggers'
status: done
priority: medium
labels:
    - engine
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T03:17:20Z"
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

## Work (EngineRemnants 2026-09-20, remaining slices)

Two of this ticket's findings were code-complete without proof, and one had no
route from the API at all. Commits: `c311215` (executeOnce/alwaysOutputData
coverage), `bcd5787` (manual trigger selection) + `606355b` (EngineCore's
pruned-trace relaxation, swept in because it shares the migrated file).

### alwaysOutputData / executeOnce — now covered, nothing to re-implement

The importer carries both (`errorHandlingSettings`) and the runner honours them
(`runner.go:716` delivery to a target that asked for data, `:959` executeOnce
trims the batch to its first item, `:998` an empty output becomes one empty
item). What was missing was proof, which is the shape the finding described ("a
setting that visibly existed and did nothing"):

- `TestImportCarriesTheErrorHandlingSettingsTheRunnerHonours` now asserts both
  keys land on the imported node and that neither is reported as dropped — the
  two "dropped" diagnostics are gone, including the false negative of a
  diagnostic naming a setting that crossed intact.
- `TestExecuteOnceRunsTheNodeOnceForTheWholeBatch`: a 3-item batch reaches such a
  node as one item (n8n runs it once, with the first item). Pre-fix message:
  `the node with executeOnce saw 3 items, want the first one alone`.
- `TestAlwaysOutputDataKeepsTheBranchAliveWithOneEmptyItem`: the node emits one
  empty item and the branch below runs once — with the same graph run without
  the setting as the control, so the test proves the setting is what kept the
  branch alive. Pre-fix message: `the branch after the empty node ran 0 times`.

Bite checks were run by temporarily disabling each handler in `runner.go` and
restoring it (both new tests failed with exactly the reported symptom).

### Manual trigger selection on POST /run — implemented end to end

A manual run of a workflow with several triggers fired all of them with the same
item (the live repro: three triggers, one `/multi` stub hit three times). Now:

- `POST /workflows/{id}/run` accepts `triggerNodeId` and echoes it on the queued
  request; `QueueManualLatest` gained the parameter (matching `QueueTriggered`'s
  shape) and stores it, so the choice is durable on the row rather than a
  process-local detail; the service already hands `record.TriggerNodeID` to the
  runner, which executes only that trigger's subgraph.
- Omitted means every trigger — the documented default, unchanged for a
  single-trigger workflow.
- A named node that cannot start a run is refused with 422 naming it (must
  exist in the pinned revision, not be disabled, and nothing may feed it): the
  runner would otherwise seed a mid-graph node with an empty input and report
  success.
- Proof: `TestQueueManualLatestCarriesTheChosenTriggerAndRefusesWhatCannotStartIt`
  (repository), `TestAManualRunStartsOnlyFromTheTriggerItChose` (engine: three
  triggers, chosen branch only, `shared` ran once), and
  `TestWorkflowAPIRunCanChooseTheTriggerToStartFrom` (API: echo, default,
  422 code/name). Bite check through the service seam (`request.TriggerNodeID =
  record.TriggerNodeID` dropped): `node "shared" ran 2 times, want once` — the
  duplicate delivery itself.

Scoped proof:

```
go test ./internal/interop/n8n/ -run TestImportCarriesTheErrorHandlingSettingsTheRunnerHonours -count=1   # ok
go test ./internal/engine/ -run 'TestExecuteOnceRunsTheNodeOnceForTheWholeBatch|TestAlwaysOutputDataKeepsTheBranchAliveWithOneEmptyItem|TestAManualRunStartsOnlyFromTheTriggerItChose' -count=1   # ok
go test ./internal/repository/ -run TestQueueManualLatestCarriesTheChosenTriggerAndRefusesWhatCannotStartIt -count=1   # ok
go test ./internal/api/ -run 'TestWorkflowAPIRunCanChooseTheTriggerToStartFrom|TestWorkflowAPIActivatesAndQueuesOnlyLatestValidDraft' -count=1   # ok
```

### Remaining on this ticket

- The editor's Run button should send the selected trigger (FrontendCore3's
  `web/` half). The API accepts it today; until the button sends it, an editor
  manual run of a multi-trigger workflow keeps the every-trigger default. No
  engine or API work is outstanding for it.

---

## Progress (FrontendCore3, 2026-09-20)

Editor half landed; the request is `POST /workflows/{id}/run` with an optional `triggerNodeId` (server half in `bcd5787`).

### Done (`workflow-editor.svelte`, new `workflow-editor/run-trigger.ts`)
- `isTriggerNode(node, definitions)` reads the registry's behavioural `trigger` group and never an annotation, so a sticky note (no ports at all) cannot be mistaken for the one node that starts a run.
- `runTriggerNodeID(nodes, definitions, selected)` returns a trigger id only when the workflow declares **more than one** trigger **and** the user has selected one of them. One trigger, nothing selected, or a non-trigger selected all return `undefined`, which leaves the server's existing "run every trigger" behaviour untouched.
- `onRun` is now `(selection?: RunSelection) => Promise<void>` with `export type RunSelection = { triggerNodeId?: string }` from the component's module script; `run()` passes the selection. Hosts that ignore the argument keep compiling (0 errors, 0 warnings after the change).

### Scoped proof
- `cd web && npx vitest run src/lib/workflow-editor` → 31 files, 370 tests, passing (new `run-trigger.test.ts`: multi-trigger pick, single-trigger silence, non-trigger selection, annotation in the selection).
- `cd web && npx svelte-check --tsconfig ./tsconfig.json` → 0 errors, 0 warnings.

### Also landed here (FrontendCore2 acked the hunks in their files)
- `src/routes/(dashboard)/app/workflows/[id]/+page.svelte` and `src/lib/embed/embed-editor.svelte`: `run(selection?: RunSelection)` now passes `selection?.triggerNodeId` into `runWorkflow(id, { triggerNodeId })`, with each host keeping its own 422 → `runIssues` mapping and its 30-minute run watch untouched. Without this forward the editor's answer never reaches the server.
- Verified together with the regenerated client (WebFormsOps3's pending regen, which adds `RunWorkflowInputBody.triggerNodeId`): `cd web && npx svelte-check` → 0 errors, 0 warnings; `cd web && npx vitest run src/lib/workflow-editor` → 31 files, 370 tests, passing.

End to end: the editor names the picked trigger only for a multi-trigger workflow, both hosts send it, the server runs that trigger's subgraph, and every other shape of workflow keeps the previous request byte-for-byte.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (27):
  - `aabb522d` — chore(pine): commit the outstanding ticket notes
  - `3c74c1c1` — chore(pine): record the fixes for ysvmaa and aede06 (per-item resume, expression targets)
  - `cb77ac76` — BUG-aede06: an expression sub-workflow target no longer blocks activation — engine
  - `b6a66d54` — chore(pine): reopen ysvmaa, aede06, tcqkad with the engine review's findings
  - `d0f26a19` — chore(pine): close the 53 EPIC-cfe7ny remediation tickets with work evidence
  - `09d3f64f` — BUG-aede06: both hosts send the picked trigger with the run request — editor hosts
  - `4e51f2c6` — BUG-aede06: record the editor half as testing — notes
  - `d1e27c76` — BUG-aede06: Execute names the trigger the user picked — editor
  - `b22092b1` — BUG-aede06 BUG-ysvmaa: record testing state and the per-finding evidence
  - `606355b1` — BUG-aede06: take EngineCore's pruned-trace relaxation with the migration
  - `bcd57876` — BUG-aede06: a manual run can start from the trigger the caller picked
  - `3dcee4ab` — BUG-aede06: correct the settings and dropped-flag statements in the migration guide — docs
  - `c3112155` — BUG-aede06: cover the imported executeOnce/alwaysOutputData pair end to end
  - `baa46d72` — BUG-aede06: wire ErrorTriggerType so an error workflow starts from its Error Trigger — composition
  - `30e8d154` — BUG-aede06: record the handover slice — live progress, error workflow, activation validation — pine
  - `3e1fbdaa` — BUG-aede06: activation refuses a call to a workflow that is not active — repository/nodes/composition
  - `a8700ddc` — BUG-aede06: carry n8n's errorWorkflow setting in and out, and narrow the dropped-settings notice — importer
  - `d99c3658` — BUG-aede06: the datastore trace test reads the completion event, not the started one — engine
  - `81d3f551` — BUG-aede06: announce node.started so a running node is visible before it finishes — engine
  - `1d8191e5` — BUG-aede06: run the error workflow a failed execution names, with Error Trigger and Stop and Error — engine/nodes
  - `d4ac9617` — BUG-aede06: register the error-workflow nodes and map n8n's error pair onto them
  - `ff3eb920` — BUG-aede06: node progress is durable and published while the run is still going — engine
  - `5990e7fd` — BUG-ysvmaa BUG-aede06: drain workers after the listener closes, and pass the instance timezone — ops/engine
  - `bb56e82f` — BUG-ysvmaa BUG-aede06: record landed SHAs, scoped evidence and what remains per finding
  - `5d46f4eb` — BUG-aede06: restore the workflowContext block a partial stage truncated — engine
  - `6e03f2ca` — BUG-aede06: per-workflow run budget, instance default timezone, light status poll — engine policy
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   12 +-
 .pine/MEMORY.md                                    |    2 +
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   39 +
 .pine/tickets/BUG-4053h6.md                        |  998 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 ++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  690 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  710 +++++
 .pine/tickets/BUG-8dmp5y.md                        |  660 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  715 +++++
 .pine/tickets/BUG-8t94wn.md                        |  628 +++++
 .pine/tickets/BUG-9853ay.md                        |  556 ++++
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  834 ++++++
 .pine/tickets/BUG-c241hm.md                        |  607 +++++
 .pine/tickets/BUG-cq4yk3.md                        |  832 ++++++
 .pine/tickets/BUG-dndnhn.md                        |  497 ++++
 .pine/tickets/BUG-esb9sh.md                        |  590 +++++
 .pine/tickets/BUG-f9frth.md                        |  870 ++++++
 .pine/tickets/BUG-fv5fer.md                        |  664 +++++
 .pine/tickets/BUG-gaavr5.md                        |  813 ++++++
 .pine/tickets/BUG-hfhzq6.md                        |  501 ++++
 .pine/tickets/BUG-hm76dq.md                        |  569 ++++
 .pine/tickets/BUG-j7rtv3.md                        |  137 +
 .pine/tickets/BUG-kzkvv6.md                        |  574 ++++
 .pine/tickets/BUG-mewhrd.md                        |  517 ++++
 .pine/tickets/BUG-mz8xrb.md                        |  506 ++++
 .pine/tickets/BUG-npfz43.md                        |  487 ++++
 .pine/tickets/BUG-pwckhd.md                        |  512 ++++
 .pine/tickets/BUG-qmgz2f.md                        |  668 +++++
 .pine/tickets/BUG-qq4xva.md                        |  506 ++++
 .pine/tickets/BUG-rjd6fm.md                        |  721 +++++
 .pine/tickets/BUG-rrkjrd.md                        |  526 ++++
 .pine/tickets/BUG-s0wy50.md                        |  507 ++++
 .pine/tickets/BUG-t2wezf.md                        |  536 ++++
 .pine/tickets/BUG-tcqkad.md                        |  652 +++++
 .pine/tickets/BUG-th16c1.md                        |  110 +
 .pine/tickets/BUG-txc9xg.md                        |  520 ++++
 .pine/tickets/BUG-wdypd2.md                        |  680 +++++
 .pine/tickets/BUG-wp2y0y.md                        |  495 ++++
 .pine/tickets/BUG-xf1wqm.md                        |  493 ++++
 .pine/tickets/BUG-y57cz4.md                        |  654 +++++
 .pine/tickets/BUG-ysvmaa.md                        |  790 ++++++
 .pine/tickets/BUG-ze1nn8.md                        |  564 ++++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++++
 .pine/tickets/EPIC-cfe7ny.md                       |   47 +
 .pine/tickets/FEAT-0895qc.md                       |  761 ++++++
 .pine/tickets/FEAT-15k49d.md                       |   36 +
 .pine/tickets/FEAT-56nep4.md                       |  625 +++++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  642 +++++
 .pine/tickets/FEAT-j5s2n4.md                       |  639 +++++
 .pine/tickets/FEAT-jvembs.md                       |  813 ++++++
 .pine/tickets/FEAT-nqpvf6.md                       |  611 +++++
 .pine/tickets/FEAT-qdedm0.md                       |   38 +
 .pine/tickets/FEAT-x5km1z.md                       |  590 +++++
 Dockerfile                                         |   15 +-
 Makefile                                           |  116 +-
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
 docs/src/content/docs/start/install.md             |   46 +-
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
 internal/ai/agent.go                               |  104 +-
 internal/ai/ai.go                                  |   47 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  146 +-
 internal/ai/openai_test.go                         |  145 +
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  136 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/credentials_test.go                   |   98 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/datastores_test.go                    |   41 +
 internal/api/embed_confinement_test.go             |  352 +++
 internal/api/embed_test.go                         |   27 +
 internal/api/events_test.go                        |   89 +
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 ++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   65 +-
 internal/api/handlers/datastores.go                |   76 +-
 internal/api/handlers/datastores_csv.go            |   78 +-
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  322 ++-
 internal/api/handlers/interop.go                   |  136 +-
 internal/api/handlers/nodes.go                     |   44 +-
 internal/api/handlers/problem.go                   |  112 +
 internal/api/handlers/schedules.go                 |   52 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 +
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 +++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  142 +
 internal/api/middleware/cors_test.go               |  235 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/node_types_test.go                    |   85 +-
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   41 +-
 internal/api/server.go                             |  139 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  323 +++
 internal/config/config.go                          |  327 ++-
 internal/credentials/redirect_test.go              |  141 +
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 +
 internal/embed/embed.go                            |   25 +
 internal/embed/embed_test.go                       |   14 +
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
 internal/engine/runner.go                          | 1740 ++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  726 ++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  176 +-
 internal/engine/wait_service_test.go               |  373 ++-
 internal/engine/worker_test.go                     |   15 +
 internal/execution/records.go                      |   11 +
 internal/expression/doc.go                         |  107 +-
 internal/expression/evaluator.go                   |  938 +++++++
 internal/expression/expression.go                  |  591 ++---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  565 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1217 +++++++++
 internal/expression/parity_test.go                 | 1041 ++++++++
 internal/expression/parser.go                      |  824 ++++++
 internal/expression/roots.go                       |  365 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         | 1087 ++++++++
 internal/interop/n8n/n8n.go                        |  493 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2780 ++++++++++++++++++--
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
 internal/repository/executions.go                  |  546 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   59 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  234 ++
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
 internal/webhook/request_lifecycle.go              |  425 ++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  825 +++++-
 internal/webhook/webhook_test.go                   |  789 +++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../postgres/000013_node_run_response.down.sql     |    9 +
 .../postgres/000013_node_run_response.up.sql       |   28 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 .../sqlite/000013_node_run_response.down.sql       |    9 +
 migrations/sqlite/000013_node_run_response.up.sql  |   23 +
 nodes/ai.go                                        |  541 +++-
 nodes/ai_test.go                                   |  874 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  217 ++
 nodes/embedscope_test.go                           |  288 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  363 ++-
 nodes/http_test.go                                 |  335 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |  158 +-
 nodes/subworkflow_calls_test.go                    |   90 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  480 +++-
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
 .../workflow-editor/property-field.svelte          |  593 ++++-
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  737 +++++-
 web/src/lib/dashboard/cursor-page.test.ts          |   85 +-
 web/src/lib/dashboard/cursor-page.ts               |   57 +
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  176 +-
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
 web/src/lib/workflow-editor/document.ts            |  291 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |  103 +
 web/src/lib/workflow-editor/expression-assist.ts   |  141 +
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
 web/src/lib/workflow-editor/shortcuts.test.ts      |  109 +
 web/src/lib/workflow-editor/shortcuts.ts           |  139 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  430 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  391 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  205 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |   63 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  219 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |  142 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  317 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 450 files changed, 82246 insertions(+), 4933 deletions(-)
```
