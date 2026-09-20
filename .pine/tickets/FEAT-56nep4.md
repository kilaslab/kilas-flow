---
id: FEAT-56nep4
title: 'NDV panes parity: INPUT/OUTPUT, execute step, expression editor, credentials, webhook panel, AI'
status: done
priority: medium
labels:
    - editor
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:05Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 9 finding(s) from dims: find:ui-ndv, find:webhook-trigger-parity.

---
### Settings tab lacks On Error modes, Always Output Data, Execute Once, Notes and Disable node; the document model cannot store them [find:ui-ndv] (high/parity-gap) · area: NDV / Settings tab · confidence: high

The Settings tab offers only Continue on Fail, Retry on Fail, Timeout, Maximum Attempts and Wait Between Attempts. The Node model has no notes, notesInFlow, disabled, onError, alwaysOutputData or executeOnce, so the importer drops them and users cannot set them. There is no 'Deactivate node' affordance anywhere either.

Evidence: 05-http-settings.png. nodes/core.go:300-330 (sharedSettings). web/src/lib/api/generated/models/node.ts (fields: credentials, id, name, parameters, position, settings, type, typeVersion). Template usage: onError 59 nodes / 20 templates (continueErrorOutput 33, continueRegularOutput 26), alwaysOutputData 38/18, executeOnce 27/10, notes 94/15, notesInFlow 29/8, disabled 15/8. Import baseline dropped: notes 77, onError 38, alwaysOutputData 15, executeOnce 14, disabled 9.

n8n behavior: The n8n Settings tab (design-refs 04) has Always Output Data, Execute Once, Retry On Fail, On Error (Stop Workflow / Continue / Continue (using error output)), Notes, Display Note in Flow and a version footer. Nodes can be deactivated.

Impact: Execution semantics change for around 30 templates (executeOnce nodes run per item, disabled nodes run, error branches are lost), and documentation notes are lost for 15.

Suggested fix: Add node-level fields for notes/notesInFlow/disabled/onError/alwaysOutputData/executeOnce to the canonical document. Honour them in the runner (skip disabled nodes, run once, emit an empty item, add an error output port). Expose them in the Settings tab.

Files: nodes/core.go, internal/workflow, internal/engine/runner.go, web/src/lib/components/workflow-editor/properties-panel.svelte

Existing tickets: FEAT-nbqye0, FEAT-a6yg3n

---
### The NDV has no INPUT/OUTPUT data panes, Execute step, pinned data or drag-and-drop mapping [find:ui-ndv] (high/parity-gap) · area: NDV layout / run data · confidence: high

The node editor is a fixed 20rem side panel with Parameters and Settings only. There is no input pane (so no drag-and-drop of fields into parameters), no output pane with Schema/Table/JSON views, no 'Execute step', and no pin or mock data. After a run from the editor the canvas shows only 'Run succeeded. View execution', with no per-node status or item counts. Node data is only on the separate execution page, as raw JSON <pre> blocks, and that page is currently unusable (see the execution-page finding). Execute is disabled while the draft is dirty.

Evidence: workflow-editor.svelte:556-599 (grid 1fr/20rem, PropertiesPanel only). executions/[id]/+page.svelte:203-210 (Input/Output <pre>{asJSON(...)}</pre>). 12-after-run-http.png (canvas after a run, no badges). The API has only POST /workflows/{id}/run (whole workflow), with no single-node or partial run and no pinData. No dragstart/drop handlers in property-field.svelte or properties-panel.svelte.

n8n behavior: design-refs 03 and 14: INPUT / Parameters / OUTPUT panes, Schema/Table/JSON toggles, 'Execute step', 'set mock data', and fields dragged from the input into any parameter.

Impact: n8n's core build loop (run a step, inspect its output, drag a field into the next node) is unavailable. Users must hand-type every $json path without seeing the data.

Suggested fix: Build a three-pane NDV: input pane (previous node output, Schema/Table/JSON) with draggable fields that insert {{ $json.path }} or {{ $('Node').item.json.path }}; output pane; Execute step backed by a partial-run API with pinned upstream data. Show per-node status and item counts on the canvas after editor runs.

Files: web/src/lib/components/workflow-editor/workflow-editor.svelte, web/src/lib/components/workflow-editor/properties-panel.svelte, web/src/routes/(dashboard)/executions/[id]/+page.svelte, internal/api/handlers

---
### The expression editor has no autocomplete, no resolved-value preview and no expanded editor [find:ui-ndv] (high/parity-gap) · area: NDV / expression editing · confidence: high

Expression mode is a plain input with a static hint ('Resolved per item on the server, for example {{ $json.id }}.') and client-side checks for balanced braces and roots only. Typing '{{ $json.' offers no suggestions for $json fields, $('Node') names, $now/DateTime methods or $input. No evaluated value is shown for the current item, there is no item stepping and there is no full-screen editor. The code comment says the preview 'never claims a value'.

Evidence: property-field.svelte:275-296 and :327-331. Live: typing 'http://127.0.0.1:8098/echo/{{ $json.' into the URL field shows no listbox or options (34-expr-typing.png, 35-expr-no-preview.png). An unknown root shows '$foo is not an available root. Use $json, $input, $node, $env, $execution, $workflow, $itemIndex, $now, $today, $fromAI, $(.'

n8n behavior: design-refs 14: Fixed/Expression toggle, fx marker, inline Result preview with Item ‹ › stepping, autocomplete for $json, $('…'), $now and Luxon methods, and an expandable editor.

Impact: Every expression is written blind. Combined with no input pane, this is the largest day-to-day gap for n8n users.

Suggested fix: Add a CodeMirror expression editor with completions from the server grammar (GET /expression-grammar), upstream node names and field paths from the last execution. Add a server endpoint that evaluates a template against a chosen item of the last run and shows the Result with item ‹ › stepping.

Files: web/src/lib/components/workflow-editor/property-field.svelte, web/src/lib/workflow-editor/expression-grammar.ts, internal/expression

---
### Credential block: raw type ids, one select per type not tied to Authentication, no inline create or Test [find:ui-ndv] (high/ux) · area: NDV / credentials · confidence: high

The panel renders one select per declared credential type, labelled with the raw id (httpBasicAuth, httpHeaderAuth, httpBearerAuth), whether or not that auth mode is in use. HTTP Request has no Authentication parameter at all, and Webhook shows basic and header selects while its Authentication is 'None'. There is no inline 'create credential', no edit and no 'Test' action, although POST /api/v1/credentials/{id}/test exists. The empty state tells users to leave the editor.

Evidence: properties-panel.svelte:95-125 ('No {typeID} credential yet — add one under Credentials.'). 03-http-v1-panel.png, 07-11-combined.png (Webhook, Authentication None, two credential selects). node-types: httpRequest credentials [httpBasicAuth, httpHeaderAuth, httpBearerAuth] and params with no 'authentication'.

n8n behavior: design-refs 05, 11, 14: credential dropdown with 'Set up credential' / 'Create new credential' and inline edit. HTTP Request uses Authentication → Generic Auth Type → credential.

Impact: 82/100 templates carry credentials (521 node references), so every imported workflow needs credentials attached. The current flow forces a context switch per credential and gives no confidence that the credential works.

Suggested fix: Add an Authentication selector (None / Predefined type / Generic: Basic, Header, Bearer, Query…) that gates which credential select appears, using display names. Add 'Create new credential' (modal using the credential-types schema), an edit link and a Test button wired to /credentials/{id}/test.

Files: web/src/lib/components/workflow-editor/properties-panel.svelte, web/src/lib/workflow-editor/credentials.ts, nodes/core.go

Existing tickets: FEAT-2f68r8

---
### AI Agent NDV lacks Source for Prompt, output-format and fallback toggles, and the sub-node slots [find:ui-ndv] (medium/parity-gap) · area: NDV / AI Agent · confidence: high

The Agent panel exposes prompt, systemMessage, systemPrompt (legacy), maxIterations, returnIntermediateSteps, passthroughBinaryImages and enableStreaming. It has no 'Source for Prompt' (Connected Chat Trigger vs Define below) with the {{ $json.chatInput }} default, no 'Require Specific Output Format', no 'Enable Fallback Model', no Options collection and no Chat Model* / Memory / Tool slots in the panel. The prompt has no default, and the HTTP Tool has no affordance for model-defined ($fromAI) parameters.

Evidence: node-types kilasflow.agent parameters (prompt default null). 20-24-combined.png compared with design-refs/n8n-v2/03-ndv-ai-agent.png.

n8n behavior: design-refs 03: Source for Prompt, the prompt as an fx expression, Require Specific Output Format, Enable Fallback Model, Options → System Message, and sub-node slots pinned to the panel floor.

Impact: Agent workflows (61 agent nodes in the templates) need manual prompt wiring, and output-parser and fallback-model usage is not discoverable from the panel.

Suggested fix: Add promptType (auto/define) with a chatInput default, hasOutputParser and needsFallback toggles gating the extra ports, move systemMessage into Options, and render the connected sub-nodes at the panel foot with add buttons.

Files: nodes/ai.go, web/src/lib/components/workflow-editor/properties-panel.svelte

Existing tickets: FEAT-cgm1y3

---
### The Webhook NDV shows no Test or Production URL and cannot listen for a test event [find:ui-ndv] (medium/parity-gap) · area: NDV / Webhook trigger · confidence: high

The Path field says the public URL is an opaque route minted on activation. The only place the URL appears is a dismissible activation notice. There is no test URL and no 'Listen for test event', so users cannot capture a sample payload to build the rest of the flow.

Evidence: 07-11-combined.png (Webhook panel: Path, Delivery ID Header, HTTP method, Authentication, Respond; no URLs). web/src/lib/components/workflow-editor/activation-notices.svelte:81-95 (URL only in notices). internal/webhook: no test-webhook route (grep for test webhook returns nothing).

n8n behavior: The n8n Webhook NDV has a collapsible 'Webhook URLs' block with Test and Production tabs and a 'Listen for test event' button (design-refs 11 shows the same for Telegram Trigger).

Impact: 12 webhook templates plus every webhook-built integration: payload-driven authoring is impossible and the URL is hard to rediscover once the notice is dismissed.

Suggested fix: Show the production URL (and state) at the top of the Webhook NDV once bound. Add a short-lived test route plus 'Listen for test event' that captures one request as the node's output or pinned data.

Files: web/src/lib/components/workflow-editor/properties-panel.svelte, internal/webhook

Existing tickets: FEAT-91as16

---
### Telegram pack Resource and Operation dropdowns show raw values instead of labels [find:ui-ndv] (low/ui) · area: NDV / pack.telegram · confidence: high

Resource shows 'message', 'chat', 'callback', 'file' and Operation shows 'sendMessage', 'editMessageText' and so on. The canvas subtitle reads 'sendMessage: message'.

Evidence: Live snapshot of '[ui-ndv] ai and packs' Telegram node: combobox Resource options "message" [selected], "chat", "callback", "file"; Operation options "sendMessage", "sendPhoto", … 41-required-empty-save.png subtitle 'sendMessage: message'. The WAHA pack shows proper labels ('Get QR').

n8n behavior: design-refs 13: Resource 'Message', Operation 'Send Message'.

Impact: Telegram (38 imported telegram 1.2 nodes) looks unfinished next to n8n's 'Message' / 'Send Message'.

Suggested fix: Provide display labels in the pack definition and loadOptions response (label ≠ value), and use labels in the subtitle.

Files: packs, nodes

Existing tickets: FEAT-6vfn3s

---
### No docs link, node version footer or description in the NDV, although packs declare documentationUrl [find:ui-ndv] (low/ux) · area: NDV header · confidence: high

The panel header shows only the node name and raw type id. documentationUrl (declared by pack.telegram, pack.waha, pack.wahaTrigger) is never rendered, core nodes declare none, and there is no version footer.

Evidence: grep documentationUrl in web/src/lib/components and web/src/routes: no matches. properties-panel.svelte:80-87.

n8n behavior: The n8n NDV header has a 'Docs' link, and the Settings tab footer shows the node version.

Impact: Users cannot reach node docs from the editor or see which version they are editing (relevant given the typeVersion finding).

Suggested fix: Render a Docs link when documentationUrl is present and add documentation URLs for core nodes. Add a footer '<Node> version X (resolved Y)'.

Files: web/src/lib/components/workflow-editor/properties-panel.svelte

---
### Webhook node panel always shows both credential pickers, whatever Authentication is set to, labelled with raw type ids [find:webhook-trigger-parity] (low/ui) · area: editor webhook node · confidence: high

With Authentication set to None, the panel still shows 'CREDENTIAL httpBasicAuth [None]' and 'httpHeaderAuth [None]' above Path.

Evidence: Screenshot /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/webhook-trigger-parity/ui-native-webhook-panel.png. Code: nodes/webhook.go:70-73 (two CredentialRequirements with no display conditions).

n8n behavior: A single credential selector appears, only for the selected auth mode.

Impact: The panel is noisy and confusing, and it's unclear which credential applies.

Suggested fix: Show each credential requirement only for its auth mode (authentication == basicAuth / headerAuth) and label it with a human-readable name.

Files: /Users/izzadev/projects/k-flow/nodes/webhook.go

## Acceptance criteria

- [ ] Settings tab lacks On Error modes, Always Output Data, Execute Once, Notes and Disable node; the document mode
- [ ] The NDV has no INPUT/OUTPUT data panes, Execute step, pinned data or drag-and-drop mapping
- [ ] The expression editor has no autocomplete, no resolved-value preview and no expanded editor
- [ ] Credential block: raw type ids, one select per type not tied to Authentication, no inline create or Test
- [ ] AI Agent NDV lacks Source for Prompt, output-format and fallback toggles, and the sub-node slots
- [ ] The Webhook NDV shows no Test or Production URL and cannot listen for a test event
- [ ] Telegram pack Resource and Operation dropdowns show raw values instead of labels
- [ ] No docs link, node version footer or description in the NDV, although packs declare documentationUrl
- [ ] Webhook node panel always shows both credential pickers, whatever Authentication is set to, labelled with raw 
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (4):
  - `f4d28d0c` — FEAT-0895qc FEAT-56nep4 BUG-6bqh51 BUG-esb9sh: record testing state — slices landed with scoped proof
  - `3e6a9b9a` — FEAT-56nep4: NDV credentials display-names+Test, webhook URL, docs+version footer, expression completions+preview
  - `f4322ac7` — BUG-npfz43 BUG-mewhrd: credential secrets empty+keep-marker, delete confirm, defaults+test — ops forms
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
 434 files changed, 75103 insertions(+), 4725 deletions(-)
```
