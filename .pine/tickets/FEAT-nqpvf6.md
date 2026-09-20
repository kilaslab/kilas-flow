---
id: FEAT-nqpvf6
title: 'Missing triggers + webhook mapping: Chat/Form/MCP/polling, error workflow, inputs, timezone'
status: done
priority: high
labels:
    - triggers
    - webhook
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:07Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (4):
  - `2606e09f` — chore(pine): WebhookParity slices to testing (cq4yk3, kzkvv6, pwckhd, nqpvf6)
  - `4cc80ff0` — FEAT-nqpvf6: hosted Form Trigger — page on GET, submission on POST, n8n form item shape — webhook
  - `1c8f78d7` — FEAT-nqpvf6: map n8n's Form Trigger onto the native form trigger
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
 .pine/tickets/BUG-ysvmaa.md                        |  752 ++++++
 .pine/tickets/BUG-ze1nn8.md                        |  564 +++++
 .pine/tickets/BUG-ztzxck.md                        |  507 ++++
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  761 ++++++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  625 +++++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  642 +++++
 .pine/tickets/FEAT-j5s2n4.md                       |  639 +++++
 .pine/tickets/FEAT-jvembs.md                       |  813 ++++++
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
 434 files changed, 76908 insertions(+), 4725 deletions(-)
```
