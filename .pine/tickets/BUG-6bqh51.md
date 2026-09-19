---
id: BUG-6bqh51
title: 'Ops executions UI: SSE failed state, approval 500s, live progress, stop/retry, lists, inspector'
status: todo
priority: high
labels:
    - ui
    - executions
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T12:06:10Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 10 finding(s) from dims: find:ui-ops-surfaces, find:web-frontend-code.

---
### Failed executions show "Running · Live" forever: the SSE feed sends execution.failed without an event name [find:ui-ops-surfaces] (high/bug) · area: executions detail live feed (also affects SDK) · confidence: high

The server's typedEvent() has no case for execution.failed, so the terminal failure event goes out as an unnamed SSE message. The dashboard and the SDK listen only for named events, so they never see the run end. The detail page keeps the replayed 'running' status on top of the durable 'failed' status.

Evidence: Shared :8090, ran "[ui-ops-surfaces] failing http", which gave exec_01a0b8cf-1e7e-… with status failed. `curl -N /api/v1/executions/{id}/events` returns id1 `event: execution.started`, id2 `event: node.completed`, id3 `event: node.failed`, then id4 with no `event:` line (its data.type is "execution.failed"). A succeeded run's terminal id5 does carry `event: execution.completed`. In the browser, /executions/{failedId} shows "Execution Running • Live" directly above the red Execution error box, while the list says Failed. Re-checked after 20 s: unchanged (22-failed-shows-running.png, 11-failed-exec.png). Cause: internal/api/handlers/executions.go:62-83 typedEvent() is missing `case events.ExecutionFailed: return ExecutionFailedEvent(event)` and falls through to `default: return event`. web/src/lib/workflow-editor/event-stream.svelte.ts:66-80 and sdk/src/browser.ts:270 add listeners by name

n8n behavior: A failed execution shows as Error both in the list and in its preview.

Impact: Every failed execution opened from the list is labelled Running with a pulsing Live dot. The page never re-reads the final trace, and SDK subscribers never see a failure or auto-close.

Suggested fix: Add the missing case, plus a test that every registered event type keeps its name on the wire. In the client, also listen for 'message', and let a terminal durable status win over a live 'running'.

Files: internal/api/handlers/executions.go, web/src/lib/workflow-editor/event-stream.svelte.ts, web/src/routes/(dashboard)/executions/[id]/+page.svelte, sdk/src/browser.ts

---
### Every Wait node pause is shown as "Waiting for approval", and Approve/Reject on the approval page return HTTP 500 [find:ui-ops-surfaces] (high/bug) · area: executions detail + approve/[token] · confidence: high

A timer wait (n8n Wait, 3 minutes) gets an approvalUrl. The detail page tells the operator to record a decision, the approval page offers Approve/Reject, and either button returns 500 "approval resume failed". No node in the catalog creates an approval-mode wait, so this page can only ever be reached for timer waits.

Evidence: Shared :8090, workflow "[ui-ops-surfaces] wait 3m" (imported n8n Wait {amount:3, unit:minutes}). Run exec_01a0b8ce-151d-… reports status waiting with approvalUrl http://127.0.0.1:8090/approve/<token>. The detail page shows a violet "Waiting for approval … Open approval page" box (02-exec-wait.png). GET /resume/<token> returns mode "interval", yet the page renders "Approval requested" with Approve/Reject (03-approve-timer-wait.png). POST /resume/<token> {approved:false} returns HTTP 500 {"code":"wait.failed","message":"approval resume failed"}, and the UI shows the bare red text (04-approve-after-reject.png). Code: engine/wait_service.go:576 WaitingLinks returns approvalURL for any wait with a resume token, whatever its Mode. executions/[id]/+page.svelte:143 shows the banner for any waiting status with an approvalUrl. The approve page never checks info.mode. resume.go sends non-approval m

n8n behavior: A waiting execution shows "waiting until <time>" or "waiting for webhook", has no approve UI for timer waits, and can be stopped. Human review uses Wait "On webhook call" or "On form submitted", or sendAndWait operations.

Impact: Every n8n Wait node, which is common in templates, produces a misleading approval screen whose buttons fail with an unexplained 500. Human-in-the-loop patterns have no working surface.

Suggested fix: Only issue an approvalUrl for Mode==approval. For interval and specificTime waits, show "Resumes at <expiresAt>" plus a Stop button, and never answer 500 on the approval page (return a typed refusal). Wire a real approval or resume-on-webhook mode to a node.

Files: internal/engine/wait_service.go, internal/api/handlers/resume.go, internal/api/handlers/executions.go, web/src/routes/(dashboard)/executions/[id]/+page.svelte

Existing tickets: FEAT-rj17xj

---
### No live node progress during a run: node runs are written after the whole run ends, with startedAt equal to finishedAt [find:ui-ops-surfaces] (high/bug) · area: executions live view / engine trace · confidence: high

While an execution runs, the Live detail page shows every node as "Not reached". All node.completed events arrive together at the end, node.started is never published, and each node run's start and finish times are both set to the end of the run, so per-node durations are always 0 ms.

Evidence: "[ui-ops-surfaces] slow 12s" (Manual, then HTTP to the stub's 12 s /slow), exec_01a0b8d5-3485-…. Three seconds in, the canvas groups read "Manual: Not reached | Slow: Not reached" under "Running • Live" (25-live-running.png, 07-live-running.png). SSE: execution.started at 08:42:55.495, then node.completed ×2 at 08:43:07.498 and execution.completed at 08:43:07.499; there is no node.started. GET /executions/{id}: manual startedAt=finishedAt=08:43:07.496 and slow startedAt=finishedAt=08:43:07.498, although the HTTP call took 12 s. Code: internal/engine/service.go (~300-360) loops over result.NodeRuns after the graph has finished, calling CreateNodeRun with StartedAt: now, FinishedAt: &now and then publishing.

n8n behavior: During a run the canvas shows the executing node with a spinner and finished nodes with item counts. Each node shows its own execution time.

Impact: Operators cannot see where a long or stuck run is, cannot find slow nodes (every node shows about 0 ms), and a crashed worker leaves no partial trace.

Suggested fix: Persist each node run and publish node.started/node.completed from the executor callback as each node starts and finishes, with real timestamps. applyEvents in the UI already folds these events in.

Files: internal/engine/service.go, internal/engine/runner.go, web/src/lib/workflow-editor/event-stream.svelte.ts

---
### No way to stop a running or waiting execution from the UI, although the API supports it [find:ui-ops-surfaces] (high/parity-gap) · area: executions list/detail + editor · confidence: high

POST /executions/{id}/cancel works, but no UI surface calls it: there is no Stop on list rows, on the detail header or in the editor's run bar.

Evidence: On a waiting execution (exec_01a0b8ce-…), POST /api/v1/executions/{id}/cancel returned 202 and the status became cancelled. Grepping web/src for cancelExecution finds no caller outside the generated client. The Running detail page (07-live-running.png, 50 s HTTP call) and the Waiting one (02-exec-wait.png) have no Stop control. The workflow editor only styles the 'cancelling' status.

n8n behavior: Stop is available on running and waiting executions in the list and preview, and in the editor during a manual run.

Impact: A runaway loop, a hung HTTP call or a multi-day Wait can only be stopped with curl.

Suggested fix: Add Stop to running, waiting and queued rows in the list, to the detail header and to the editor run bar, calling cancelExecution and showing the 'cancelling' state.

Files: web/src/routes/(dashboard)/executions/+page.svelte, web/src/routes/(dashboard)/executions/[id]/+page.svelte, web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### Executions have no Retry, no Debug in editor, no delete, and the detail page never names the workflow [find:ui-ops-surfaces] (medium/parity-gap) · area: executions · confidence: high

The detail header shows only status, start time, duration, trigger and the execution id. There is no workflow name or link, no retry (with the original or current revision), no "debug in editor", and no delete. The API has no retry or delete-execution operation.

Evidence: executions/[id]/+page.svelte header text is "All executions / Execution / <status> / exec_…". The workflow's name appears nowhere on the page, and the list's workflow cell is plain text. OpenAPI covers executions with list, get, cancel and events only.

n8n behavior: Failed executions offer "Retry with currently saved workflow" and "Retry with original workflow". The preview has Debug in editor (copies the data in as pinned data) and delete, and the list supports bulk delete. Executions sit in their workflow's Executions tab.

Impact: Operators cannot re-run a failed webhook or schedule payload after fixing a node, which is the most common n8n recovery path. A shared execution link does not say which workflow ran. Executions cannot be cleaned up by hand (only global retention).

Suggested fix: Add POST /executions/{id}/retry (original vs current revision, same input) and DELETE /executions/{id} with bulk delete. Add UI actions for both, an "Open workflow" link with the workflow name in the header, and "Debug in editor" that loads the execution's data as pinned data.

Files: internal/api/handlers/executions.go, web/src/routes/(dashboard)/executions/[id]/+page.svelte, web/src/routes/(dashboard)/executions/+page.svelte

---
### Executions list has no auto-refresh and no date filter; filters are not in the URL; the editor's "Open executions" opens the unfiltered list [find:ui-ops-surfaces] (medium/ux) · area: executions list · confidence: high

Status and workflow filters live only in component state, so they are lost on reload and cannot be linked. The list loads once and on manual Refresh only, has no start-date range or search, and the editor's link lands on the global list.

Evidence: executions/+page.svelte keeps status and workflowID in $state with no polling or SSE. The only controls are the Status and Workflow selects (01-executions-list.png). Running rows stay Running until Refresh (08-list-running.png and 09-list-after-finish.png are identical files). workflow-editor.svelte:471 "Open executions" links to /executions with no filter. Only the id cell is a link; the row itself is not clickable.

n8n behavior: The executions list has Auto refresh (on by default), filters for status, workflow, start date range and custom data that are kept in the URL, and a per-workflow Executions tab.

Impact: Anyone watching production runs has to keep pressing Refresh, and cannot bookmark something like "today's failed runs of workflow X".

Suggested fix: Put the filters in the URL (?status=&workflowId=&from=&to=), add server parameters for a start-date range, add an auto-refresh toggle (polling or SSE), make rows clickable, and link the editor to /executions?workflowId=<id>.

Files: web/src/routes/(dashboard)/executions/+page.svelte, web/src/lib/components/workflow-editor/workflow-editor.svelte, internal/api/handlers/executions.go

---
### Failed HTTP nodes drop the response body and error details; errors are shown as a raw JSON blob that runs off the panel [find:ui-ops-surfaces] (medium/parity-gap) · area: executions detail error display · confidence: high

A 500 from an API is recorded only as "request failed with status 500", with no body or description. The UI prints the error as a JSON <pre> that scrolls sideways instead of wrapping.

Evidence: KilasFlow, "[ui-ops-surfaces] failing http" (stub /fail returns 500 {"error":"boom","detail":"stub failure"}): node error is {"code":"node.failed","message":"node \"Fail\": request failed with status 500"}. The detail page puts this in <pre class=overflow-x-auto> and the message is cut off at the panel edge (11-failed-exec.png). The same workflow on n8n 2.33.7 (webhook copy calling host.docker.internal:8099/fail) records NodeApiError with message "The service was not able to process your request", description "boom" and httpCode "500".

n8n behavior: The error panel shows a message, a description taken from the response body, and the HTTP code.

Impact: Users debugging third-party API failures cannot see the upstream reason in the execution.

Suggested fix: Capture httpCode and a truncated response body or description in HTTP node errors. Render errors as title, description and details (wrapped, with a copy button), and make the failing node's name select it on the canvas.

Files: web/src/routes/(dashboard)/executions/[id]/+page.svelte, HTTP Request node error construction (nodes/)

---
### Execution inspector shows JSON only, with no size limit: the full execution is downloaded and dumped into <pre> blocks [find:ui-ops-surfaces] (medium/perf) · area: executions detail node inspector · confidence: high

GET /executions/{id} includes every node's input and output. Selecting a node renders its full input and output as pretty-printed JSON. There is no item count, paging, table or schema view, large-data guard, or run selector for looped nodes.

Evidence: "[ui-ops-surfaces] huge payload" (HTTP GET stub /big?items=30000, about 9 MB) produced exec_01a0b8d4-3e4f-…. GET /executions/{id} returns 35.8 MB. Clicking the "After" node renders 17,930,298 characters into two <pre> blocks, with a JS heap of 175 MB (measured with run-code huge2.js; 24-huge-inspect.png, 06-huge-inspect.png). latestNodeRuns() keeps only the last run per node, so earlier loop iterations cannot be inspected.

n8n behavior: The run-data panel has Table, JSON and Schema views, item counts, paging, a run index selector for loops, and a "data is large, show anyway" guard.

Impact: Executions with realistic payloads (list APIs, CSV rows, AI transcripts) make the detail page slow and memory-heavy, and looped nodes cannot be debugged.

Suggested fix: Leave node payloads out of (or trim them in) GET /executions/{id}. Add a paginated per-node, per-run data endpoint. In the UI, reuse the editor's output viewer read-only: table/JSON/schema views, paging, a run selector and a large-data guard.

Files: web/src/routes/(dashboard)/executions/[id]/+page.svelte, web/src/lib/workflow-editor/execution.ts, internal/api/handlers/executions.go

---
### Execute polls for a fixed 20 s, then reports 'Run failed: ...did not finish in time' and re-enables Execute while the run continues [find:web-frontend-code] (medium/bug) · area: editor run feedback · confidence: high

run() polls getExecution 80 times at 250 ms. On timeout it sets runError, which renders in the red 'Run failed:' banner, and sets running=false. The SSE feed (/executions/{id}/events) exists but only the execution detail page uses it. The embed copies the same loop.

Evidence: web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte:186-215; workflow-editor.svelte:520-522 ('Run failed: {runError}'); web/src/lib/embed/embed-editor.svelte:118-148. Local agent runs make several model calls of about 4 s each (for example exec_01a0b8e5-ceb7-746c-b121-2523a947a75c), and Wait nodes or retries easily exceed 20 s.

n8n behavior: A manual execution streams node status onto the canvas until it finishes, with no client timeout, and shows item counts and output inline.

Impact: AI and long-running workflows are reported as failed when they are not, and users re-run them, creating duplicate executions with side effects.

Suggested fix: Subscribe to executionEvents(queuedId), or poll with backoff and no failure verdict. Show 'still running' with a link. Paint node statuses onto the editor canvas.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/embed/embed-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### The live execution feed silently drops ai.* events: the server sends them without an SSE event name and the client listens only for named events [find:web-frontend-code] (low/bug) · area: executions live view · confidence: high

The huma sse.Register map in executions.go lists only execution.* and node.* types. ai.model.started/completed, ai.tool.started/failed and ai.agent.completed are therefore sent with no `event:` line, which makes them default 'message' events. event-stream.svelte.ts registers listeners only for the 10 named types, and its comment says not to listen for 'message'.

Evidence: curl -N /api/v1/executions/exec_01a0b8e5-ceb7-746c-b121-2523a947a75c/events returned 13 data frames, only 6 with an `event:` line; the missing ones are 2 ai.model.started, 2 ai.model.completed, ai.tool.started, ai.tool.failed and ai.agent.completed. Code: internal/api/handlers/executions.go:169-186; web/src/lib/workflow-editor/event-stream.svelte.ts:62-77.

n8n behavior: n8n shows agent and tool progress, and token usage, live in the logs panel during execution.

Impact: AI step progress and tool failures never appear live; users see them only after the run finishes and the trace is refetched.

Suggested fix: Register ai.* types in the SSE map (typed), and add a 'message' fallback listener on the client. Derive the listener list from the OpenAPI SSE schema instead of hand-listing it.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/event-stream.svelte.ts, /Users/izzadev/projects/k-flow/internal/api/handlers/executions.go

## Acceptance criteria

- [ ] Failed executions show "Running · Live" forever: the SSE feed sends execution.failed without an event name
- [ ] Every Wait node pause is shown as "Waiting for approval", and Approve/Reject on the approval page return HTTP 
- [ ] No live node progress during a run: node runs are written after the whole run ends, with startedAt equal to fi
- [ ] No way to stop a running or waiting execution from the UI, although the API supports it
- [ ] Executions have no Retry, no Debug in editor, no delete, and the detail page never names the workflow
- [ ] Executions list has no auto-refresh and no date filter; filters are not in the URL; the editor's "Open executi
- [ ] Failed HTTP nodes drop the response body and error details; errors are shown as a raw JSON blob that runs off 
- [ ] Execution inspector shows JSON only, with no size limit: the full execution is downloaded and dumped into <pre
- [ ] Execute polls for a fixed 20 s, then reports 'Run failed: ...did not finish in time' and re-enables Execute wh
- [ ] The live execution feed silently drops ai.* events: the server sends them without an SSE event name and the cl
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)