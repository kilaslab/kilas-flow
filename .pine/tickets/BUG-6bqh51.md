---
id: BUG-6bqh51
title: 'Ops executions UI: SSE failed state, approval 500s, live progress, stop/retry, lists, inspector'
status: done
priority: high
labels:
    - ui
    - executions
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:03:58Z"
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
## Progress (WebFormsOps2 2026-09-19, ops slice)

- Commit e57eb04 (executions/[id]/+page.svelte, rebase-safe per WebEditorCore — their guard not landed): Stop button (cancelExecution, 202/200, stopping state + error) in header + waiting boxes; workflow name + "Open workflow" link in header; execution/node errors as wrapped title+detail+collapsible JSON+copy (no sideways blob); inspector large-payload guard (200k chars, char count, "Show anyway" opt-in); waiting box split — approvalUrl → approval UI, otherwise "resumes on its own, no decision" + Stop; Live dot only while !finished.
- Not touched (owners'): typedEvent ExecutionFailed case + SSE map (SecurityDx, with regression test), framing/CSP (SecurityDx), 401 interceptor (SecurityDx — I build only /login route + Settings), event-stream/workflow-editor.svelte/[id] refetch guard (WebEditorCore), engine live-progress/timestamps/retry/delete endpoints (engine owners).
- Remaining mine: executions list auto-refresh + URL filters + Stop-per-row + deleted-marker (next), then FEAT-56nep4 NDV panes/credentials/webhook, FEAT-x5km1z /login + Settings API keys, FEAT-0895qc workflows CRUD/search/import dialog. Scoped proof: svelte-check clean on edited pages.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (5):
  - `f4d28d0c` — FEAT-0895qc FEAT-56nep4 BUG-6bqh51 BUG-esb9sh: record testing state — slices landed with scoped proof
  - `396d6ca8` — BUG-esb9sh BUG-6bqh51: record fix evidence — ops grids, execution detail
  - `e57eb04a` — BUG-6bqh51: stop button, workflow link, wrapped errors, large-payload guard, wait-box split — execution detail
  - `d661a652` — chore(pine): align editor-core + ops-form ticket states (testing/doing)
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
 .pine/tickets/BUG-6bqh51.md                        |  189 ++
 .pine/tickets/BUG-6jvcs5.md                        |  238 ++
 .pine/tickets/BUG-8dmp5y.md                        |  183 ++
 .pine/tickets/BUG-8h4yy1.md                        |   46 +
 .pine/tickets/BUG-8sb0jw.md                        |  239 ++
 .pine/tickets/BUG-8t94wn.md                        |  179 ++
 .pine/tickets/BUG-9853ay.md                        |   84 +
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |   29 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 10476 bytes
 .pine/tickets/BUG-aede06.md                        |  326 +++
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
 434 files changed, 57804 insertions(+), 4725 deletions(-)
```
