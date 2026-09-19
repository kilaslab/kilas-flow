---
id: FEAT-56nep4
title: 'NDV panes parity: INPUT/OUTPUT, execute step, expression editor, credentials, webhook panel, AI'
status: doing
priority: medium
labels:
    - editor
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T12:40:44Z"
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