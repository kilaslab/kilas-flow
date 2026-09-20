---
id: FEAT-nqpvf6
title: 'Missing triggers + webhook mapping: Chat/Form/MCP/polling, error workflow, inputs, timezone'
status: todo
priority: high
labels:
    - triggers
    - webhook
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T12:06:10Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 8 finding(s) from dims: find:webhook-trigger-parity.

---
### No Chat Trigger: 29/100 templates start from chatTrigger and import as a blocking placeholder [find:webhook-trigger-parity] (high/parity-gap) · area: triggers / AI · confidence: high

There's no chat trigger node, and it isn't on the roadmap. KF's agent and memory already default to $json.chatInput and $json.sessionId, so the downstream half exists; only the entry point is missing.

Evidence: In import-baseline, 29 templates carry a blocking issue for @n8n/n8n-nodes-langchain.chatTrigger. No chat trigger appears in /api/v1/node-types or .pine/roadmap.md. Live n8n chatTrigger v1.1 (public, responseMode lastNode): POST /webhook/<webhookId>/chat {action:'sendMessage', sessionId, chatInput} runs the workflow with item {action, sessionId, chatInput} and answers with the last node's first item. GET on the same path serves a hosted chat HTML page, and OPTIONS returns 204 with CORS headers. Code: nodes/ai.go:401 and 568 already read sessionId and chatInput. None of the 29 templates is unblocked by the trigger alone; co-blockers are other models, Drive, SerpApi and similar.

n8n behavior: The Chat Trigger exposes /webhook/<id>/chat (JSON API plus a hosted page) and emits {action, sessionId, chatInput}.

Impact: This is the largest single trigger gap (29 templates), and it's needed by every AI-chat workflow customers bring over.

Suggested fix: Build kilasflow.chatTrigger on the webhook binding infrastructure: POST <route>/chat with n8n's body shape, CORS/allowedOrigins, responseMode lastNode/responseNode (streaming later), loadPreviousSession through memory, an optional hosted or embeddable white-label chat page, and basic auth. Add the importer mapping from chatTrigger (public, mode, initialMessages, options).

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go, /Users/izzadev/projects/k-flow/nodes/ai.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

---
### Manual 'Execute' runs every trigger in the workflow at once (a webhook fires with {}), so shared downstream nodes run once per trigger [find:webhook-trigger-parity] (high/bug) · area: engine manual run / editor Execute · confidence: high

A manual run starts from every trigger root. A Webhook trigger in a manual run emits an empty item instead of waiting for a test request, and a node reachable from two triggers runs for both items. The run API has no way to choose a trigger.

Evidence: Imported {Webhook(POST) -> HTTP POST to stub, Manual Trigger -> same HTTP node}. POST /api/v1/workflows/{id}/run {input:{}} produced node runs Manual (1 item), Webhook (1 item, json {}) and HTTP (2 items); the stub logged 2 POSTs. Firing the same workflow via its webhook runs only Webhook -> HTTP, with 1 call. RunWorkflowInputBody has only `input`.

n8n behavior: A manual execution starts from exactly one trigger (the Manual Trigger or the one selected in Execute); a Webhook in a manual run waits for a test call.

Impact: 32/100 templates have several triggers, e.g. 5171 (manual + 5 webhooks), 2063/2324/4846 (manual + schedule), 2846 (manual + webhook), 1960/2165/2508/2621/2982 (manual + chat). Testing them in KF duplicates side effects such as HTTP posts, messages and DB writes, and feeds empty webhook items into branches.

Suggested fix: Add triggerNodeId to the run API and a trigger picker on Execute, defaulting to the Manual Trigger when there is one. Run only the chosen trigger's subgraph. In manual runs, webhook, chat and form triggers should use pinned or test data, or a listen mode.

Files: /Users/izzadev/projects/k-flow/internal/engine/runner.go, /Users/izzadev/projects/k-flow/internal/api/handlers/workflows.go, /Users/izzadev/projects/k-flow/internal/repository/executions.go

Existing tickets: FEAT-fw0m2q

---
### Wait 'On Webhook Call' and 'On Form Submitted' are still refused, and the 1 h cap is still enforced, although durable suspend, /resume/{token} and $execution.resumeUrl shipped [find:webhook-trigger-parity] (medium/unfinished) · area: wait node · confidence: high

FEAT-rj17xj (done) built durable waits, the resume HTTP surface, the approval page and $execution.resumeUrl. The Wait node still offers only timeInterval and specificTime and refuses webhook and form resumes, both at validation and on import (blocking). MaxWaitDuration = 1h still applies, although the wait now suspends to storage (the engine allows up to 7 days).

Evidence: nodes/wait.go:41 (MaxWaitDuration), 69-76 (options, with the description 'this server does not do yet'), 120-123 (validator refusal); internal/interop/n8n/parameters.go:1319-1330 (blocking). The FEAT-rj17xj evidence says 'Webhook/form wait modes ... are nodes-owned future work', and no ticket was opened for it. The node's description still reads 'Pauses the workflow for up to 1h0m0s'. The resume surface lives in internal/api/handlers/resume.go and internal/engine/wait_service.go.

n8n behavior: Wait resumes on timeInterval, specificTime, webhook (with $execution.resumeUrl) or form, with no 1 h cap.

Impact: Human-in-the-loop and callback workflows (payment callbacks, approval links) can't be imported or built. Long waits such as 'wait 1 day then follow up' fail.

Suggested fix: Wire resume 'webhook' to the existing wait registry: return SuspendError with a resume token, plus method/auth/limitWaitTime options. Then add form resume. Drop MaxWaitDuration for suspending waits, and keep short waits (~65 s) in-process as n8n does.

Files: /Users/izzadev/projects/k-flow/nodes/wait.go, /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/internal/engine/wait_service.go, /Users/izzadev/projects/k-flow/internal/api/handlers/resume.go

Existing tickets: FEAT-rj17xj

---
### No Form Trigger or Form node: 9/100 templates need hosted forms [find:webhook-trigger-parity] (medium/parity-gap) · area: triggers · confidence: high

formTrigger and the multi-page Form node both import as blocking placeholders, and KF has no hosted form surface.

Evidence: In import-baseline, formTrigger (9) and n8n-nodes-base.form (9) are blocking. Live n8n formTrigger v2.2: GET /form/<webhookId> serves a 31 KB hosted HTML form, and submissions start the workflow with fields keyed by label plus submittedAt and formMode.

n8n behavior: Form Trigger serves /form/<id>; a submission starts the workflow.

Impact: 9 templates are affected, and lead-capture or intake workflows are common among n8n users.

Suggested fix: Build kilasflow.formTrigger with a hosted, brandable (white-label) form page on the webhook route: GET renders, POST submits, multipart files go to binary. Support text, email, number, date, dropdown, file and textarea fields, and responseMode onReceived/lastNode/responseNode. Later add the multi-page Form node and Wait 'form' resume.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go, /Users/izzadev/projects/k-flow/internal/webhook/webhook.go

---
### No error workflow: settings.errorWorkflow and the Error Trigger have no equivalent [find:webhook-trigger-parity] (medium/parity-gap) · area: workflow settings / triggers · confidence: high

KF has no concept of an error workflow. Import drops every setting except timezone, errorTrigger is a blocking placeholder, and nothing in internal/ or web/ implements it. It isn't on the roadmap either; the migration guide only states the gap.

Evidence: The importer reports 'n8n workflow settings other than the timezone — error workflow, execution order and the rest — have no KilasFlow equivalent yet' (dropped, 49/100 templates). errorTrigger (1 template) is blocking. A grep for errorWorkflow finds nothing outside tests. docs/src/content/docs/guides/n8n-migration.md:190 states the gap. callerPolicy (11 templates) and executionTimeout are dropped too.

n8n behavior: When a production execution fails, n8n runs the workflow named in settings.errorWorkflow, starting from its Error Trigger with the execution and workflow details.

Impact: n8n's standard production alerting (Slack or email on failure) is lost silently. Unattended webhook and schedule workflows fail with nobody notified. With callerPolicy dropped, any workflow in the tenant can call any sub-workflow.

Suggested fix: Add kilasflow.errorTrigger and an 'Error workflow' workflow setting. Fire it from the execution service on failed non-manual runs, with a recursion guard, passing {execution:{id,url,error,lastNodeExecuted,mode}, workflow:{id,name}}. Import settings.errorWorkflow as an unbound reference with a lossy issue. Consider supporting callerPolicy.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go, /Users/izzadev/projects/k-flow/internal/engine/service.go, /Users/izzadev/projects/k-flow/docs/src/content/docs/guides/n8n-migration.md

---
### Workflow timezone can't be set in the editor, and the Schedules page says 'Times are evaluated in UTC' although schedule triggers honour settings.timezone [find:webhook-trigger-parity] (medium/ux) · area: editor workflow settings / schedules page · confidence: high

The scheduler reads document.settings.timezone correctly, but no UI sets it. The only way is import or the API. The Schedules page copy also states UTC unconditionally.

Evidence: An imported schedule with settings.timezone Asia/Jakarta fired with Timezone 'Asia/Jakarta (UTC+07:00)' and Hour '16', which is correct (internal/scheduler/extract.go:17,78-81). A grep for 'timezone' in web/src finds nothing outside generated code, and there's no workflow-settings panel. The Schedule node help says 'Times are read in the workflow's timezone setting, which defaults to UTC'. The /schedules header reads 'Run an active workflow on a cron expression. Times are evaluated in UTC.'

n8n behavior: The Workflow Settings dialog has a Timezone selector (defaulting to the instance timezone) used by the Schedule Trigger.

Impact: Schedule workflows built natively can only run in UTC. For users in UTC+7, the primary market, schedules are 7 hours off. The Schedules page is misleading for imported workflows that carry a timezone.

Suggested fix: Add a Workflow settings panel with timezone first; error workflow, caller policy, timeout and save-data options can follow. Show the effective timezone on schedule rows and in the Schedule node panel.

Files: /Users/izzadev/projects/k-flow/internal/scheduler/extract.go, /Users/izzadev/projects/k-flow/nodes/webhook.go, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/schedules

---
### No polling-trigger framework (n8n pollTimes): Gmail/Drive/Sheets/RSS/IMAP-style triggers can't be built, even in node packs [find:webhook-trigger-parity] (medium/parity-gap) · area: trigger infrastructure · confidence: high

Triggers can only be webhook-bound, cron, or Telegram's own poller. There's no generic poll declaration with a durable cursor for built-ins or generated packs.

Evidence: Webhook triggers come from node.Definition.Webhook (internal/webhook/webhook.go:609-663), cron from internal/scheduler, and Telegram polling is custom code (nodes/telegram_lifecycle.go:216-357). internal/nodepack has no poll concept, and the importer doesn't recognise pollTimes. Templates: gmailTrigger 3, googleDriveTrigger 3 (4 nodes), googleSheetsTrigger 1, so 7/100 are blocked, together with the missing Google integrations.

n8n behavior: Polling triggers declare pollTimes, keep static data between polls, and emit only new items.

Impact: Common SaaS triggers (new email, new file, new row, RSS item) can't be delivered by packs.

Suggested fix: Add a poll trigger kind: schedule-driven, with a per-node durable cursor (static data) and dedupe, usable by built-ins and packs. Import pollTimes.

Files: /Users/izzadev/projects/k-flow/internal/webhook/webhook.go, /Users/izzadev/projects/k-flow/internal/scheduler/scheduler.go, /Users/izzadev/projects/k-flow/nodes/telegram_lifecycle.go

---
### No MCP Server Trigger (@n8n/n8n-nodes-langchain.mcpTrigger) [find:webhook-trigger-parity] (low/parity-gap) · area: triggers / AI · confidence: high

KF has an MCP client tool but no MCP server trigger that exposes workflow tools to MCP clients.

Evidence: 2/100 templates (3514, which has two mcpTrigger nodes, and 3770) import it as a blocking placeholder. The node catalog contains only kilasflow.mcpClientTool.

n8n behavior: The MCP Server Trigger serves the connected tools at /mcp/<path> over SSE / streamable HTTP.

Impact: Workflows that publish tools to Claude, Cursor and other MCP clients can't migrate.

Suggested fix: Add an MCP server trigger on the webhook route infrastructure that exposes connected tool sub-nodes (workflowTool, httpTool, datastoreTool) with bearer or header auth.

Files: /Users/izzadev/projects/k-flow/nodes/ai.go, /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go

## Acceptance criteria

- [ ] No Chat Trigger: 29/100 templates start from chatTrigger and import as a blocking placeholder
- [ ] Manual 'Execute' runs every trigger in the workflow at once (a webhook fires with {}), so shared downstream no
- [ ] Wait 'On Webhook Call' and 'On Form Submitted' are still refused, and the 1 h cap is still enforced, although 
- [ ] No Form Trigger or Form node: 9/100 templates need hosted forms
- [ ] No error workflow: settings.errorWorkflow and the Error Trigger have no equivalent
- [ ] Workflow timezone can't be set in the editor, and the Schedules page says 'Times are evaluated in UTC' althoug
- [ ] No polling-trigger framework (n8n pollTimes): Gmail/Drive/Sheets/RSS/IMAP-style triggers can't be built, even 
- [ ] No MCP Server Trigger (@n8n/n8n-nodes-langchain.mcpTrigger)
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (WebhookParity 2026-09-20) — Form Trigger landed

- **Form Trigger** (9/100 templates, the finding that imported as a blocking placeholder): `kilasflow.formTrigger` is now a real node — a hosted page on GET and the submission on POST, both bound on the workflow's own route. `nodes/webhook.go` holds the definition (`path`, `formTitle`, `formDescription`, `formFields` with text/email/number/date/dropdown/textarea/file and required/placeholder/dropdown options, `responseMode`, `appendAttribution`, `authentication`, and an `options` collection for the thank-you text, CORS origins and IP allow-list); `internal/webhook/form.go` renders the page (inline styles, no client script, `multipart/form-data`, posts to its own address, every document-sourced value escaped) and builds the submission; the boundary answers GET/HEAD from the route and reads POST as the submission, refusing one that omits a required field (400 + a page naming the field) rather than starting a run with an empty value. The item is n8n's own form shape — the field labels at the top level plus `submittedAt` (RFC3339) and `formMode: "production"` — and an immediate acknowledgement is a thank-you page, because a person filling in a browser is owed a page rather than `{"message":"Workflow was started"}`.
- Trigger kinds gained one optional hook (`TriggerKind.Page` / `webhook.HostedPage`) so the boundary still holds no node-type knowledge: a trigger registers readers, and the page is read from the *binding* on every request, so two workflows' forms cannot serve each other's page.
- Importer half landed by ImporterTail (1c8f78d): `n8n-nodes-base.formTrigger` and `@n8n/n8n-nodes-base.formTrigger` → `kilasflow.formTrigger`, with `formFields.values` reduced to the list of strings the native node reads and back out again; `formMode: test` and an unreadable field row named as issues. I corrected one line of that mapping: `appendAttribution` is now carried both ways rather than reported as dropped, since the native node has the option (the issue text claimed this server's form does not append an attribution, which is no longer true).
- Scoped proof: `go test ./internal/webhook/ -count=1` ok (including a new end-to-end form test: GET renders and queues nothing, an incomplete submission is refused and queues nothing, a complete one runs with the n8n item shape), `go test ./nodes/ -count=1` ok, `go test ./internal/interop/n8n/ -count=1` ok.
- **Remaining on this ticket** (not started, each needs an owner outside my files): Chat Trigger (29 templates — node in `nodes/ai.go`, which AINodes2 owns; the boundary half is mine: a `/chat` sub-route on the same binding infrastructure, CORS and responseMode; streaming later), MCP Server Trigger (2 templates — `nodes/ai.go` + a route that speaks MCP), error workflow + Error Trigger (EngineCore has `errorTriggerNode`/`stopAndErrorNode` in flight; firing it from the execution service, a workflow setting, and the import mapping for `settings.errorWorkflow`), workflow timezone in the editor (frontend settings panel — the scheduler already honours `settings.timezone`), the polling-trigger framework (a durable per-node cursor in `internal/nodepack` + the scheduler), and manual-Execute trigger selection (`triggerNodeId` on the run API + a picker; FEAT-fw0m2q). Also verified as already closed by other slices: the Wait node's webhook/form resume modes and the 7-day cap (EngineWaits, BUG-ysvmaa), and disabled triggers no longer binding a route (my half of BUG-c241hm, commit 90a005b).
