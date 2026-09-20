---
id: BUG-f9frth
title: 'Canvas editing core: save conflicts, unsaved guard, remount loss, ports, errors, run display'
status: done
priority: high
labels:
    - editor
    - canvas
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:00Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 15 finding(s) from dims: find:ui-canvas, find:ui-ndv, find:web-frontend-code.

---
### Concurrent saves silently overwrite each other (lost update): workflow save has no revision precondition [find:ui-canvas] (high/bug) · area: save / persistence · confidence: high

PUT /api/v1/workflows/{id} accepts only the document, with no base revision or If-Match. A stale editor tab, or the dashboard and the embed open at the same time, silently overwrites changes made elsewhere.

Evidence: Repro (a2.js). Editor open on '[ui-canvas] Conflict test' wf_01a0b8cf-0da5-7340-be59-169c1f4fdeed. Another writer PUTs a rename 'Set fields'→'Changed elsewhere' (200). The stale editor then drags a node and clicks Save → 'All changes saved', no alert; the server's node names are back to ['Manual','Set fields']. The OpenAPI update-workflow body is WorkflowDocumentInput {schemaVersion,name,nodes,connections,settings} with no version field. routes/(dashboard)/app/workflows/[id]/+page.svelte save() calls updateWorkflow(id, document).

n8n behavior: Workflow saves carry the loaded versionId. A save based on an outdated version is refused with a 'changed by someone else' conflict, and the user chooses to overwrite or reload.

Impact: Data loss for teams, and for embed + dashboard use of the same workflow.

Suggested fix: Send the loaded latestVersion.id or revision as If-Match or a baseVersionId field. Return 409/412 when latest has moved. Show a conflict banner in the editor with Reload / Overwrite / Copy my changes.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/workflows.go

---
### No unsaved-changes guard: leaving the editor silently discards the draft [find:ui-canvas] (high/bug) · area: editor navigation · confidence: high

Nothing guards a dirty canvas. In-app links, the back arrow, reload and tab close all drop unsaved edits without a prompt.

Evidence: There is no beforeNavigate/beforeunload anywhere in web/src (grep). Repro (a3.js): open wf_01a0b8cf-0da5-7340-be59-169c1f4fdeed, drag a node → header 'Unsaved changes'. Clicking the sidebar 'Executions' navigates immediately to /executions, 0 dialogs. Browser Back → 'All changes saved', and the edit is gone.

n8n behavior: A 'Save changes before leaving?' modal (Save / Leave without saving / Cancel) on in-app navigation, plus the browser beforeunload prompt.

Impact: Every user who edits and then clicks elsewhere loses work. This is made worse by the lack of Mod+S and undo.

Suggested fix: Add beforeNavigate in routes/(dashboard)/app/workflows/[id]/+page.svelte (expose `dirty` from WorkflowEditor via a callback or bindable) and a window beforeunload handler while dirty. The embed should emit a host event instead.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### Switch, Merge (numberInputs>2) and Datastore ifExists draw only their static ports; extra branches and their wires are invisible and cannot be created [find:ui-canvas] (high/bug) · area: canvas ports · confidence: high

The server derives ports from parameters (Definition.PortsFor), but /node-types exposes only the static list, and the SPA never re-derives it. A native Switch always shows a single 'Rule 1' output. Connections on outputs 1..n are silently not drawn and cannot be drawn.

Evidence: '[ui-canvas] Switch 3 rules' wf_01a0af2f-f06b-7b3b-9d98-9fb2ebf773c3 (3 rules, connections sw:0/1/2 → A/B/C). Canvas handles are 'Route input main | Route output Rule 1'. Rendered edges are only 'm main to sw main ; sw 0 to n0 main', and B and C look unconnected (a1-switch.png). Code: nodes/flow.go:134 switchPorts, :385 mergePorts, nodes/datastore.go:199; canvas-node.svelte mainPorts(data.definition.outputs); ports.ts canConnect validates against the static definition. FEAT-vvwpjw (done) states that PortsFor means "the connection check, the runner's output arity and the editor all see the same list". The editor does not.

n8n behavior: Switch shows one labelled output per rule, plus Fallback. Merge shows N inputs.

Impact: 21/100 templates contain a multi-output Switch (2454 has 7 outputs, 4827 has 11); 6/100 have a Merge with more than 2 inputs. Imported ones currently render only because of the unknown-version fallback (F1), so fixing F1 will expose this. Users cannot build a multi-branch Switch in the editor at all.

Suggested fix: Expose resolved ports per node: an endpoint, a field in the workflow response, or a TS port of PortsFor. Recompute handles when parameters change, label them with rule names, and draw edges to missing ports as dangling or invalid instead of hiding them.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/canvas-node.svelte, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/ports.ts, /Users/izzadev/projects/k-flow/nodes/flow.go, /Users/izzadev/projects/k-flow/nodes/datastore.go

Existing tickets: FEAT-vvwpjw

---
### Activate and Execute failures show only "422 — workflow validation failed"; the server's per-node errors are thrown away [find:ui-canvas] (high/ux) · area: activation / run error feedback · confidence: high

The server returns a 422 whose errors[] name each blocked node (value.nodeId + message). The editor shows only the generic detail, in one or two red strips. No node is marked and no issue list appears. Even the save path drops issues that have no node or connection id, and the server messages name nodes by UUID rather than by name.

Evidence: Template 1954 imported (wf_01a0af1e-8178-7b42-bc30-b87e0aa607e9). POST /activate returns 4 errors: unsupported chatTrigger, openAiApi credential required ×2, unsupported toolSerpApi. The UI shows 'Run failed: 422 — workflow validation failed' and 'Activation failed: 422 — workflow validation failed', and the 'Workflow validation issues' list is empty (t7.out, 18-1954-run.png). Code: web/src/lib/workflow-editor/activation.ts:103 activationFailure() → message(error); the +page.svelte run() catch sets runError=message(error); only save() calls validationIssuesFromApiError; validation.ts:33 returns [] for issues without nodeID/connectionID. Example server text: 'node "ef4c6982-…" configuration is invalid…' and 'requires a openAiApi credential'.

n8n behavior: Nodes with problems show a warning marker (with the issue list on hover) before activation is attempted, and the activation error names the node.

Impact: 96/100 imported templates fail activation (baseline), and users have to guess which nodes are at fault.

Suggested fix: Map 422 problems from activate and run into the same issue list and node badges as save. Keep issues that have no node. Use node names in messages (resolve id→name client-side, or on the server). Consider a validate call on load so blocked nodes are marked up front.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/activation.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/validation.ts, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte

Existing tickets: FEAT-sdjdh2

---
### A Webhook trigger's public URL is never shown, so after activation there is no way to find where to send requests [find:ui-canvas] (high/ux) · area: webhook trigger / activation · confidence: high

Webhook routes are minted server-side as opaque segments. They are reported only in the n8n import response. For a Webhook node built in the editor, neither the inspector, the activation notices nor any API endpoint reveals the URL.

Evidence: '[ui-canvas] Webhook exec' wf_01a0b8d5-edc2-7e19-81e7-24807ef36c94 (kilasflow.webhook path 'ui-canvas-wh'). Activate → header 'Active', no notice strip; POST /activate returns notices: []. The inspector text says 'The public URL uses an opaque route minted on activation…' but never shows it (a16-activated.png, a18.js). internal/repository/webhooks.go mintWebhookRoute/EnsureWebhookRoutes. The URL is only emitted in internal/api/handlers/interop.go:147 → import-report.svelte, and openapi.json has no webhook listing path. (Workflow deactivated again afterwards.)

n8n behavior: The Webhook node panel has a 'Webhook URLs' section with Test and Production URLs and copy buttons.

Impact: Webhook-triggered workflows are the most common production trigger. Users can't integrate them without reading the database.

Suggested fix: Add GET /workflows/{id}/webhooks (EnsureWebhookRoutes already exists). Render the URL(s) with a copy button at the top of every webhook-trigger inspector, and add them as an activation notice.

Files: /Users/izzadev/projects/k-flow/internal/repository/webhooks.go, /Users/izzadev/projects/k-flow/internal/api/handlers/interop.go, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/properties-panel.svelte

---
### Execute shows nothing per node: no run status or item counts on the canvas, no input/output data, no 'execute step', and polling gives up after 20 s [find:ui-canvas] (high/parity-gap) · area: manual execution feedback · confidence: high

After Execute the editor shows only a banner ('Run succeeded. View execution'). Run status exists only in the read-only execution replay. The inspector has no Input/Output view, nodes cannot be executed individually, and the page stops polling after 80×250 ms.

Evidence: Execute on '[ui-canvas] Canvas fixture' → banner only, [data-run-status] count 0 (15-after-execute.png, t6). The inspector has only Parameters|Settings tabs (properties-panel.svelte), and the node toolbar has only Delete (canvas-node.svelte NodeToolbar). +page.svelte run(): 80 iterations × 250 ms, then 'The execution did not finish in time…', so any LLM or agent run over 20 s reads as a failure. RunWorkflowInputBody has only `input` (no destination node). The toolbar 'Open executions' links to /executions unfiltered; the executions page's workflowID starts as '' and never reads a query parameter.

n8n behavior: Nodes show running, success or error state and item counts on each connection. The NDV shows the last run's input and output as table or JSON. 'Execute step' runs up to that node. The Executions tab is scoped to the workflow.

Impact: Users can't debug or iterate inside the editor. Every run means leaving for the executions page, and long AI runs are misreported.

Suggested fix: Use the existing event stream (event-stream.svelte.ts) or poll the execution to put nodeRuns status and item counts onto the editor canvas. Add Output/Input panes from GET /executions/{id}. Add a partial run to a destination node. Remove the 20 s cap. Link to /executions?workflowId=… and honour that parameter.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/properties-panel.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/executions/+page.svelte

---
### Every save remounts the editor: the view re-fits, selection and inspector are lost, and focus drops to <body> [find:ui-canvas] (medium/ux) · area: save UX · confidence: high

The route wraps WorkflowEditor in {#key currentWorkflow.latestVersion.id}. A save assigns the new version, so the canvas remounts and runs fitView.

Evidence: Repro (a4.js) on wf_01a0b8cf…: zoom out to scale 0.694, select 'Set fields', edit a field, Save → viewport 'translate(482px,362px) scale(1)', nothing selected, document.activeElement is BODY. Code: routes/(dashboard)/app/workflows/[id]/+page.svelte {#key currentWorkflow.latestVersion.id}; save() sets currentWorkflow = response.data.

n8n behavior: Saving keeps the viewport, the selection and the open NDV.

Impact: On large imported workflows the user loses their place after every save.

Suggested fix: Remount only on restore. For a normal save, adopt the returned document in place. Preserve the viewport with getViewport/setViewport.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### Every Save remounts the editor: the open node panel closes, selection and zoom are lost [find:ui-ndv] (high/ux) · area: Editor / save flow · confidence: high

save() assigns the returned workflow, whose latestVersion.id changes. The page wraps WorkflowEditor in {#key currentWorkflow.latestVersion.id}, so each save rebuilds the editor. The node being edited is deselected, the properties panel disappears and the canvas re-fits.

Evidence: web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte:87 (currentWorkflow = response.data) and :244 ({#key currentWorkflow.latestVersion.id}). The comment at :160-166 says the remount is intended only for restore. Live in '[ui-ndv] multiline': with 'HTTP properties' open, edit URL, Save. The 'HTTP properties' region is gone and the editor region ref changes (f7e487 to f7e648). 41-required-empty-save.png shows the re-fitted canvas with no panel.

n8n behavior: Saving in n8n keeps the NDV open, the selection and the viewport.

Impact: Every save interrupts editing and forces re-selecting and re-navigating, and undo history is lost.

Suggested fix: On save, update only the version metadata and the editor's baseline document without changing the key, or keep the remount key stable across saves and change it only on restore.

Files: web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, web/src/lib/components/workflow-editor/workflow-editor.svelte

Existing tickets: FEAT-ezeap5

---
### Save, activate and restore never update the TanStack cache: reopening a workflow within 30 s shows the pre-save graph as 'All changes saved', and the next save reverts the previous one [find:web-frontend-code] (high/bug) · area: editor state / query cache · confidence: high

save() and the other mutations assign the response only to the page-local `currentWorkflow`. Nothing in web/src calls setQueryData or invalidateQueries (grep outside /generated/ finds 0). The cached GET /workflows/{id} keeps the old revision and stays 'fresh' for 30 s, so navigating back into the workflow mounts the editor from the stale cached document.

Evidence: Verified. On wf_01a0b8e2-fff7-7a02-98c1-64f1df146257, dragged the Note node from (0,200) to (70,270) and clicked Save. The server reports rev 3 at (70,270). Clicked the back arrow, then reopened the workflow from the list: the Note renders at translate(0px, 200px) and the status says 'All changes saved'. Editing and saving from this state PUTs the stale base over rev 3, since there is no concurrency check. Code: web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte:79-137, 168-184; web/src/lib/embed/embed-editor.svelte:74-116; web/src/lib/query-client.ts:5.

n8n behavior: After a save, n8n's store holds the saved workflow, so reopening shows the saved state.

Impact: Users unknowingly revert their own saved work. The workflow list also shows a stale revision and active state until its next refetch.

Suggested fix: After each mutation, call queryClient.setQueryData(getGetWorkflowQueryKey(id), response) and invalidate the list query keys. Or switch to the generated createUpdateWorkflow/createActivateWorkflow mutations and update the cache in onSuccess.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/embed/embed-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/version-panel.svelte

---
### The validation-issue list uses a non-unique key: two issues with the same code on one node crash the editor (each_key_duplicate) and leave Publish stuck on 'Working…' [find:web-frontend-code] (high/bug) · area: editor validation rendering · confidence: high

workflow-editor.svelte:515 keys issues as `${code}-${nodeID ?? connectionID ?? message}`. The compiler often returns two issues with the same code for the same node, for example a missing credential and a missing required parameter, both 'config.required'. Svelte 5.57 throws each_key_duplicate in production builds too (node_modules/svelte/src/internal/client/dom/blocks/each.js:355-361), so the error list never renders and the component tree breaks.

Evidence: Verified. The script work/web-frontend-code/dupprobe.py published a bare node of each type and found duplicate keys for pack.telegram, pack.waha and mysql/postgres/sqlite v1. Repro: wf_01a0b8ef-2120-71dc-bc25-3106c0b86781 (Manual → Telegram, no credential or chatId) → History → Revision 1 → Publish → confirm. The console shows 'Error: https://svelte.dev/e/each_key_duplicate', 0 issues are rendered, the confirm button stays at 'Working…' and Cancel stays disabled until reload (screenshot .../work/web-frontend-code/dupkeys.png). The same list renders save 422 issues.

n8n behavior: n8n lists every node issue on the node and in the NDV without crashing.

Impact: Publishing any Telegram or WAHA workflow that lacks a credential or chat ID (a very common first attempt) freezes the editor, and the user sees no error.

Suggested fix: Key the list by index, or by code+node+location+message. Also make validationIssuesFromApiError keep workflow-level issues (it drops issues without a nodeId or connectionId). Add a component test with duplicate issues.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/validation.ts

---
### No unsaved-changes guard when navigating away or closing the tab [find:web-frontend-code] (high/ux) · area: editor · confidence: high

The editor tracks `dirty`, but nothing uses it to stop navigation: there is no beforeNavigate and no beforeunload anywhere in web/src. The back arrow, sidebar links, the 'Open executions' toolbar link, the 'View execution' link and closing the tab all drop a dirty draft silently.

Evidence: `grep -rn 'beforeunload\|beforeNavigate' web/src` returns nothing. dirty is defined at web/src/lib/components/workflow-editor/workflow-editor.svelte:179. Navigation links: +page.svelte:223 (back arrow), workflow-editor.svelte:471 (Open executions) and :524/:531 (View execution) all leave the editor.

n8n behavior: n8n asks 'Save changes before leaving?' (Save / Leave without saving / Cancel) on route change and uses beforeunload on tab close.

Impact: The most common way to lose work in any editor. Because Execute requires saving first, users often follow the 'View execution' link from a dirty canvas.

Suggested fix: Expose dirty to the host page (callback or bindable prop) and register beforeNavigate (confirm dialog) and a beforeunload handler while dirty. Open execution links in a new tab or side panel while dirty.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte

---
### JSON-kind parameters show "[object Object]", and editing one overwrites the structured value with a string [find:ui-canvas] (high/bug) · area: inspector / property fields · confidence: high

property-field renders 'json' properties with String(value) and commits the raw textarea string on input. Object and array values (Switch rules, Summarize fields, Execute Workflow Trigger inputs, and others) therefore display as [object Object], and one keystroke replaces the data with a literal string.

Evidence: '[ui-canvas] Switch 3 rules' wf_01a0af2f-f06b-7b3b-9d98-9fb2ebf773c3 → select 'Route' → Routing Rules textarea shows '[object Object],[object Object],[object Object]' (a1-switch.png). Code: property-field.svelte:203 stringValue = String(value); :349 textarea oninput={onChange(event.currentTarget.value)}. displayValue()/parseValue() already exist in the same file (~303-313) but are not used here. Affected PropertyJSON keys (grep nodes/*.go): switch.rules, summarize.fieldsToSummarize, set.jsonOutput, executeWorkflowTrigger.workflowInputs, respondToWebhook.responseBodyJSON, vectorStore queryVector/metadataFilter/ids.

n8n behavior: Switch rules are a structured rules editor. JSON fields show pretty-printed JSON and validate it.

Impact: Silent corruption of routing and config for any workflow whose JSON parameters are edited in the UI. Switch rules can't be edited at all without breaking them.

Suggested fix: Display JSON.stringify(value, null, 2) and JSON.parse on commit (keep the previous value and show an inline error when parsing fails). Add a structured rules editor for Switch that reuses the conditions control.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/property-field.svelte

---
### The version-history preview can't be used: the revision is drawn behind a blurred modal overlay and is discarded when the panel closes [find:ui-canvas] (medium/bug) · area: version history panel · confidence: high

The history panel is a modal Sheet whose overlay blurs the canvas and blocks the pointer. Closing the Sheet returns to the draft, so the previewed revision can never be seen clearly or inspected.

Evidence: version-panel.svelte:280 <Sheet.Root bind:open onOpenChange={(next) => !next && backToDraft()}>. The Sheet overlay has class 'fixed inset-0 z-50 bg-black/10 backdrop-blur-xs' (computed backdrop-filter blur(4px)). Repro (a14.js): History → Revision 1 → 'Previewing revision 1' but blurred, and elementFromPoint over a node returns the overlay (20-history-preview.png). Close → the preview strip disappears and the draft is back. Restore with confirmation and reason works (t10/t11).

n8n behavior: Version history is a side panel. The canvas shows the selected version clearly, and it can be panned and inspected (design-refs n8n-v2 15).

Impact: The users can't visually check a revision before restoring or publishing it. Only the text diff is readable.

Suggested fix: Render the history panel non-modal (no overlay) with the canvas resized beside it. Or keep the preview after the panel closes until 'Back to draft'. Allow read-only node selection so parameters can be read.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/version-panel.svelte

Existing tickets: FEAT-1500sp

---
### Import diagnostics (blocking, lossy, dropped) appear once in the import dialog and are then lost; the editor never shows what an imported node lost [find:ui-canvas] (medium/ux) · area: imported workflow diagnostics · confidence: high

The import report is returned in the POST /workflows/import response only and is not stored. Once the dialog closes, lossy or dropped fields and the reasons a node is blocked appear nowhere in the editor, beyond the 'Unsupported' pill on placeholders. API-driven imports never show them at all.

Evidence: internal/api/handlers/interop.go Import builds Unsupported[] and Webhooks[] into the response and stores only the document through SaveDraft. The editor components have no import-issue surface. Baseline: onError modes dropped in 25 templates, settings dropped in 49, 'Set node has no readable assignments' 15 times, plus notes, executeOnce and alwaysOutputData dropped.

n8n behavior: Not applicable natively. The V2 goal requires that users can see why an imported node is blocked or degraded.

Impact: Users can't tell after import which nodes behave differently from n8n.

Suggested fix: Store the import report with the created revision, or re-derive it on read. Show per-node warning badges and an 'Import report' drawer in the editor, and clear an entry when the node is edited or replaced.

Files: /Users/izzadev/projects/k-flow/internal/api/handlers/interop.go, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-report.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/canvas-node.svelte

Existing tickets: FEAT-nbqye0, FEAT-0556ck

---
### JSON-kind fields show structured values as '[object Object]', and any keystroke overwrites the stored structure with that string [find:web-frontend-code] (high/bug) · area: editor inspector (property-field) · confidence: high

stringValue is `String(value)` for anything that is not a string (property-field.svelte:203). The json-kind textarea (:349) and the multi-group fixedCollection fallback textarea (:463) show it, and oninput writes the raw text back. Arrays and objects therefore display as '[object Object],...' or 'US', and the first edit replaces the real structure with that text.

Evidence: Verified. A Switch v1 node with the rules array from the template 3135 import (wf_01a0b8f1-b58a-77a8-b6e8-ddf368815c3f) shows #property-rules = '[object Object],[object Object],[object Object],[object Object],[object Object],[object Object]'. Affected json-kind properties in the catalog: switch.rules, summarize.fieldsToSummarize, set.jsonOutput, respondToWebhook.responseBodyJSON, vectorStore ids/metadataFilter/queryVector, telegram media/replyMarkup/results, waha config/contacts/poll/..., and waha@202502 countries (default ['US'] shows as 'US') and categories ([] shows as '').

n8n behavior: n8n edits JSON parameters in a JSON code editor with pretty-printing and validation, and it never loses structure.

Impact: Silent corruption of Switch routing rules (16 Switch nodes in the templates, stored as lists by the importer), Telegram reply markup, WAHA arrays and Summarize specs the moment a user touches the field.

Suggested fix: Display JSON.stringify(value, null, 2). On input, keep a local text buffer, JSON.parse it, and commit only valid JSON, with an inline parse error. Use a code editor. Never String() an object anywhere in the inspector.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/property-field.svelte

## Acceptance criteria

- [ ] Concurrent saves silently overwrite each other (lost update): workflow save has no revision precondition
- [ ] No unsaved-changes guard: leaving the editor silently discards the draft
- [ ] Switch, Merge (numberInputs>2) and Datastore ifExists draw only their static ports; extra branches and their w
- [ ] Activate and Execute failures show only "422 — workflow validation failed"; the server's per-node errors are t
- [ ] A Webhook trigger's public URL is never shown, so after activation there is no way to find where to send reque
- [ ] Execute shows nothing per node: no run status or item counts on the canvas, no input/output data, no 'execute 
- [ ] Every save remounts the editor: the view re-fits, selection and inspector are lost, and focus drops to <body>
- [ ] Every Save remounts the editor: the open node panel closes, selection and zoom are lost
- [ ] Save, activate and restore never update the TanStack cache: reopening a workflow within 30 s shows the pre-sav
- [ ] The validation-issue list uses a non-unique key: two issues with the same code on one node crash the editor (e
- [ ] No unsaved-changes guard when navigating away or closing the tab
- [ ] JSON-kind parameters show "(object Object)", and editing one overwrites the structured value with a string
- [ ] The version-history preview can't be used: the revision is drawn behind a blurred modal overlay and is discard
- [ ] Import diagnostics (blocking, lossy, dropped) appear once in the import dialog and are then lost; the editor n
- [ ] JSON-kind fields show structured values as '(object Object)', and any keystroke overwrites the stored structur
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (FrontendCore2 2026-09-20) — LANDED vs REMAINING

Commits: 5c9f22b (editor-core modules), 50938a6 (editor + embed pages, shared with BUG-t2wezf/BUG-8h4yy1), 7ac3c8d (execution detail).

LANDED (my files)
- Concurrent save: PUT carries `baseVersionId` = the loaded revision (server already answers 409, internal/api/handlers/workflows.go:501). `saveConflict` is set on 409 and `reloadTheirs()` (re-GET + adopt + cache) / `overwriteTheirs()` (re-PUT the same draft with no base revision, which the handler treats as a deliberate overwrite) are wired to FrontendCore3's `saveConflict` / `onReloadConflict` / `onOverwriteConflict` props.
- No unsaved-changes guard → `beforeNavigate` confirm on in-app navigation plus a `beforeunload` handler while dirty, driven by FrontendCore3's new `onDirtyChange`.
- No-remount-on-save: the editor's key is now `canvasFrom` (`id:revision`) and moves on load/restore only. A save adopts the returned document in place (`noteCanvasRevision` + `currentWorkflow = response.data`), so viewport, selection and the open inspector survive. A successful focus refetch that finds a newer revision while the canvas is dirty is *held* in `newerRevision` with Reload theirs / Keep mine instead of replacing the draft; a failed refetch is a banner, never the page.
- TanStack cache: `cacheWorkflow(queryClient, workflow)` (web/src/lib/workflow-editor/workflow-cache.ts) sets the `getWorkflow` envelope + invalidates the list, called from save/activate/deactivate/restore/publish in both the dashboard page and the embed editor. Test: workflow-cache.test.ts (reopened editor reads the saved revision).
- 422 per-node: `validationIssuesFromApiError` keeps entries with no node/connection and entries with no `value` at all (draft refusals carry the reason only in the message); `withNodeNames` resolves node ids to names; the page maps save, run and activate 422s into the issue list and passes them as `hostIssues`.
- Dynamic ports: `resolvedPorts(node, definition)` in ports.ts mirrors the server's `PortsFor` for Switch (one output per rule, positional names, `Fallback` for `fallbackOutput: 'extra'`) and Merge (`input1..inputN` from `numberInputs`, cap 32), strictly collapsing to the single `0`/`Rule 1` port exactly when the server rejects the rule set. `canConnect`/`lookupPort` resolve through it, so a wire to a Switch's third branch now validates. FrontendCore3 renders it in canvas-node.svelte. 17 tests in ports.test.ts.
- Run polling cap: 80×250 ms ("did not finish in time" at 20 s) replaced by a 30-minute watch that reports "stopped watching" instead of a failure.

REMAINING (not mine to land, or blocked)
- Webhook public URL: no `GET /workflows/{id}/webhooks` exists (the URL is only in the n8n import response, internal/api/handlers/interop.go:147). SecurityFront2 declined for this wave (internal/api/routes.go owned elsewhere); asked for a ticket addressed to WebhookParity + the handler owner. FE surface = properties-panel.svelte (FrontendCore3).
- Datastore ifExists/ifNotExists: two outputs both named `main`, and the compiler resolves a connection's port name to the first index (internal/workflow/compiler.go:706), so the false branch is unreachable and a second handle would silently wire the true branch. Reproduced in ports.ts (mirrors the server); needs distinct port names server-side. SecurityFront2 confirms it is real and out of their tickets.
- Execute-per-node feedback (run status/item counts on the editor canvas, NDV input/output panes, execute step, `/executions?workflowId=`): needs WorkflowEditor props + properties-panel work (FrontendCore3) and, for a partial run, a server-side destination-node parameter. Not started.
- Import diagnostics surface (per-node badges + report drawer): needs the report stored with the revision (internal/api/handlers/interop.go) or re-derived on read, plus canvas-node/import-report work.
- Version preview: FrontendCore3 landed the non-modal panel + preview surviving close.
- JSON-kind fields: already pretty-printed and parse-guarded since 3e6a9b9 (FrontendCore3 re-checked); no `[object Object]` path remains in property-field.
- Validation-issue each-key: fixed by FrontendCore3 (index-backed key).

VERIFICATION
- Live: executions detail verified (see BUG-t2wezf note). Editor page verified to load, render the canvas and stay mounted on the stub harness.
- NOT verified by me: the UI-driven save scenario. Drag (mouse + pointer) and inspector typing did not produce a dirty canvas against the stub fixture — the node sits at the top edge of the canvas where the editor toolbar overlaps its centre, and `Add step`/`Tidy up` left the draft clean, so the marker-survives-save assertion is unproven. Status left at `doing` for that reason; the 409 banner path is likewise unexercised end-to-end.
- A real bug I introduced and fixed inside this ticket: the loading branch `(workflow.isPending || nodeTypes.isPending) && !currentWorkflow` let the editor mount while the catalogue was still pending, so `definitions` was undefined and the child threw on render — the page sat on "Loading workflow editor…" forever with no console error. Correct shape: `nodeTypes.isPending || (workflow.isPending && !currentWorkflow)` plus `definitions={nodeTypes.data ?? []}` (also applied to the embed editor).

### API-client drift migration (FrontendCore2 2026-09-20, requested by Main)

- `createListCredentials` call sites migrated to the regenerated signature (`params` first, options factory second): `web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte:58` and `web/src/lib/embed/embed-editor.svelte:46`, both `createListCredentials<CredentialResource[]>(undefined, () => ({ query: { select … } }))` — same shape WebFormsOps3 used on the credential list page.
- `cd web && pnpm check` → `1508 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`. This also confirms the two type errors FrontendCore3 reported against my files are gone (`definitions={nodeTypes.data ?? []}`).

## Work (PortsDiagnostics 2026-09-20) — remaining slice: import-diagnostics surface, ports, execute-per-node

Asked for the last workable remainder without a new API. Verdict up front: **the import-diagnostics surface cannot land without new server storage**, so it is recorded here rather than half-landed. The ports remainder is landed (under BUG-66es9z). The rest is recorded with what each needs.

### Import diagnostics (finding "Import diagnostics … are then lost") — NOT REACHABLE WITHOUT NEW STORAGE

Checked in the tree, not assumed:
- `internal/api/handlers/interop.go` `Import` (lines 142–165) builds `Unsupported []n8n.ImportIssue` and `Webhooks []WebhookRouteResource` into the **response body only**; the document is persisted through the ordinary `SaveDraft`, which takes no diagnostics.
- `n8n.ImportIssue` (`internal/interop/n8n/n8n.go:117`) is a response shape: severity, node name/id, field, type, typeVersion, reason. Nothing in it is written into the document — the importer's diagnostics are not recoverable from a stored revision, so "re-derive on read" is impossible without the original n8n JSON.
- `workflowVersionModel` (`internal/repository/models.go:215–232`) has no metadata/notes column: `definition`, `label`, `created_by`, `created_at` only. `appendVersion`'s only extra parameter is the 255-byte label.
- No read route exists (`openapi`/handlers have no import-report GET; the report appears once in `POST /workflows/import`).

What landing it needs, in order: (1) a migration adding a diagnostics column to `workflow_versions` (or a `workflow_import_reports` table keyed by revision) — `internal/database` + `internal/repository`; (2) `SaveDraft` carrying the report into `appendVersion`, and a read accessor; (3) a read route on the workflow resource — `internal/api/**`; (4) the editor surface — per-node warning badges (`canvas-node.svelte`) and an Import report drawer reusing `import-report.svelte`.

Rejected as a half-measure: stashing the report client-side (navigation state / localStorage) so the editor shows it once after import. It dies on reload, is invisible to the next tab or the next person, and covers nothing for an API-driven import — which is exactly what the finding names as the failure.

Ownership: repository files are EngineCore's active area and `internal/api/**` is SecurityFront2's, so both sides need a hub/ticket before anyone starts. **Recommend a ticket of its own** (the shape FEAT-cwmw90 took for the webhook URL): "Persist the n8n import report with the revision and surface it in the editor".

### Dynamic ports (Switch/Merge/datastore branch) — LANDED under BUG-66es9z

`db8831e` — `nodes/datastore.go` `datastorePortsFor` now declares `true`/`false` instead of two `main` outputs, and `web/src/lib/workflow-editor/ports.ts` `datastoreOutputs` mirrors the same names and labels, so the canvas handle, `canConnect` and the compiler agree. The executor's If Not Exists arm now emits on the first port when the table holds no match (it previously emitted nothing there at all). Proof and the pre-fix failures are on BUG-66es9z. Switch and Merge were already done by FrontendCore2 (`ports.test.ts`).

### Execute-per-node feedback (finding "Execute shows nothing per node") — needs a server parameter

`POST /workflows/{id}/run` carries `triggerNodeId` (`internal/api/handlers/workflows.go:237`) but no destination/start-node parameter, so "execute step" and a canvas that shows per-node status/item counts while a run goes need: a server-side destination node on the run request (EngineFlow/Service), `GET /executions?workflowId=` filtering, and the editor props/NDV panes (FrontendCore3/WebFormsOps). Not started here — the file owners are other live slices and the API change is not in this ticket's reach.

### Webhook public URL — ticketed

FEAT-cwmw90 ("Expose a workflow's webhook URLs through the API (GET /workflows/{id}/webhooks)"), status `todo`: the URL is minted server-side and only ever returned in the n8n import response.

### Everything else on this ticket

Landed and reported by FrontendCore2/FrontendCore3 in the Progress section above (concurrent-save `baseVersionId`, unsaved guard, no-remount, TanStack cache, 422 per-node issues, Switch/Merge ports, run-poll watch, version preview, JSON-kind fields, validation-issue key). Status stays `doing` for the three remainders above.

#### Update (PortsDiagnostics, same day) — the storage/read half landed after that verdict

The verdict above was written against the tree before `000a437` ("BUG-f9frth: read a revision's import report back over HTTP"). Since then the two server halves it called for exist, exactly in the shape it named:

- `workflowVersionModel.Diagnostics []byte` (`internal/repository/models.go`), the column the report needed;
- `repository.WorkflowDiagnosticsStore` — `SaveDraftWithDiagnostics` (the import path now stores instead of only returning) and `WorkflowDiagnostics` (`internal/repository/import_diagnostics.go`), so a report is read back per revision and a later hand-edit revision correctly carries none;
- `GET /workflows/{id}/diagnostics` (`operationId: workflow-diagnostics`, `internal/api/handlers/interop.go`), with the client regenerated in `9ae45a0`.

So the finding is now landable: what remains is the editor surface (per-node badges + report drawer), which the live `ImportDiagnostics` agent owns. My part of this ticket — the port defect — is landed under BUG-66es9z (`db8831e`). The other two remainders are unchanged: webhook URL = FEAT-cwmw90 (`todo`); execute-per-node still needs a destination-node parameter on the run request (the `triggerNodeId` half landed for trigger *selection*, not for "execute up to this node").

---
### Import-diagnostics surface (BUG-f9frth) — server + editor landed, one blocker

Status: testing. Commits: 68b0dbc (migration), 6cb0608 (repository), 000a437
(read route), 4268702 + 5709b4d (editor badge/drawer), plus 9ae45a0 from
WebFormsOps3 (generated client). Ticket not closed on the last acceptance line
only: the live browser proof of the badge/drawer.

Landed
- `workflow_versions.diagnostics` (blob/bytea, nullable), 000012 in both
  dialects, space-indented. Additive; no index. `internal/database` suite green
  (fresh, twice, rollback, both directions, tabs check). PostgreSQL leg SKIPPED:
  KILASFLOW_TEST_POSTGRES_DSN is unset and nothing listens on :5432 here.
- Repository: `WorkflowDiagnosticsStore` (optional capability, the
  WebhookRouteMinter shape) = `SaveDraftWithDiagnostics` + `WorkflowDiagnostics`.
  SaveDraft and the new variant share one append path; a restore writes no
  report; an empty report is refused. Tests: round trip byte-for-byte, report
  stays on its own revision after a later save, tenant scoping, empty refusal,
  restore. `go test ./internal/database/ ./internal/repository/ -count=1` -> ok.
- Route: `GET /workflows/{id}/diagnostics?versionId=` (operationId
  `workflow-diagnostics`). Import writes the report in the same call that saves
  the draft and refuses the import (503) when the store cannot keep one. Tests:
  the import response's report comes back identical from storage, a later draft
  save leaves it on its revision and the newest reports none, a hand-built
  workflow reports none, unknown ids are 404. `go test ./internal/api/ -run
  'Import|Diagnostic' -count=1` -> ok.
- Editor: badge per affected node (severity-coloured, reasons in the accessible
  name, `data-import-diagnostic` for tests), toolbar button with the counts, and
  a right-side drawer reusing import-report.svelte. import-report.svelte now
  takes what it renders (issues, name, optional webhooks/node names/open button)
  so the dialog and the drawer can both use it. Report is read for the revision
  the canvas was built from, so a save does not erase the explanation of nodes
  that are still on the canvas. `pnpm check` 0 errors / 0 warnings;
  vitest src/lib/workflow-editor 373 passed.

Live proof (stub-free, `/tmp/kf-diag`)
- POST /api/v1/workflows/import on a real server (sqlite) with an n8n file
  holding a manual trigger, an unknown node, `notes` and `pinData`: 201 with
  1 blocking + 2 dropped issues.
- GET /api/v1/workflows/{id}/diagnostics -> 200 with source "n8n", importedAt,
  the same three issues, and the imported revision id/1.
- Same result after `make build-web` and against the embedded SPA origin.

BLOCKER (pre-existing, not from this change): the editor page cannot render
state that arrives after its first flush. `effect_update_depth_exceeded` is
thrown on main at 9ae45a0 (page checked out from that commit, my changes
absent) and again from the production bundle, for a hand-built workflow as well
as the imported one. Cause, from reading the component: the canvas projection
effect in workflow-editor.svelte reads `selectedNodeIDs`, and
`onSelectionChange` assigns it a fresh array on every Flow selection event, so
each projection re-triggers the event that re-triggers the projection. Every
later write (the diagnostics included) dies with the aborted flush, which is why
no badge or report button can appear in a browser. Owner: whoever holds
workflow-editor.svelte. Evidence: the diagnostics request is visible in the page
(200, correct versionId) and the DOM never shows a badge; the same loop fires
with my page file replaced by the pre-change one; screenshots impossible while
the flush is aborted.

Not claimed
- No visual confirmation of the badge/drawer (blocked above); the canvas-side
  derivation is pinned only by unit tests.
- PostgreSQL migration execution (no DSN available).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (14):
  - `31c424f8` — chore(pine): file editor-loop, conditions-coercion, paging and follow-up tickets
  - `5709b4d2` — BUG-f9frth: read the revision's import report with a plain request
  - `42687025` — BUG-f9frth: show the stored import report in the editor, badge and drawer
  - `000a4372` — BUG-f9frth: read a revision's import report back over HTTP
  - `6cb0608d` — BUG-f9frth: store a draft together with the import report that produced it
  - `68b0dbc7` — BUG-f9frth: give a workflow revision somewhere to keep its import report
  - `9ba3c23c` — BUG-f9frth: migrate createListCredentials call sites to the regenerated params-first signature — web/api drift
  - `2a6fe46e` — BUG-t2wezf BUG-8h4yy1 BUG-f9frth: record landed slice + remaining items — editor/embed/execution detail
  - `50938a63` — BUG-f9frth BUG-8h4yy1 BUG-t2wezf: editor survives refetch/save, 409 conflict answers, unsaved guard, token before first embed query — editor + embed pages
  - `5c9f22b5` — BUG-f9frth: 422 issues keep node names + id-less entries, resolved ports mirror PortsFor, mutation answers land in the query cache — editor core
  - `d661a652` — chore(pine): align editor-core + ops-form ticket states (testing/doing)
  - `f56aa84c` — BUG-f9frth: save baseVersionId + 409 conflict flag — editor-core (partial)
  - `ad7c6246` — BUG-f9frth: PUT save precondition If-Match/baseVersionId — 409
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
 434 files changed, 63327 insertions(+), 4725 deletions(-)
```
