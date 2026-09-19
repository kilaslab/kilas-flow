---
id: BUG-rjd6fm
title: 'NDV inputs: condition builder, routing rules, key-value rows, defaults, validation, timestamps'
status: todo
priority: high
labels:
    - editor
    - ndv
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T12:06:10Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 14 finding(s) from dims: find:ui-ndv.

---
### If/Filter condition builder cannot read n8n-shaped conditions: it shows them as empty and 'Add condition' overwrites them [find:ui-ndv] (critical/bug) · area: NDV / conditions builder (If, Filter) · confidence: high

The runtime and importer store n8n's filter object {combinator, conditions:[{leftValue, operator:{type,operation}, rightValue}], options}. The panel only reads a bare array of {field, operator, value}, so imported or API-created conditions are invisible. Clicking 'Add condition' replaces the whole filter with [{field:'',operator:'equals',value:''}]. Save accepts this, and the run then fails. The builder has only 4 operators (equals / does not equal / exists / does not exist), no AND/OR combinator, no type-aware operators (gt, contains, regex, date, boolean) and no expression operands.

Evidence: web/src/lib/workflow-editor/conditions.ts:34-42 (readConditions returns [] unless Array.isArray); property-field.svelte:626-657. Live repro in workflow '[ui-ndv] repro edits' (id in work/ui-ndv/wf4.id): an If node with an n8n filter (leftValue {{ $json.greeting }}, contains 'Hello') ran successfully. The panel showed only '+ Add condition' (32-if-empty-after-add.png). After clicking it and saving, the stored conditions were [{"field":"","operator":"equals","value":""}] (rev 2). The run returned 422 'node "i" configuration is invalid: condition 0 requires field and operator'. The old workflow '[ui-ndv] core nodes v1' shows the same loss.

n8n behavior: The If/Filter NDV shows each condition as value1 / operator / value2 with 'fx' toggles, an AND/OR selector and typed operator menus (String, Number, Date & Time, Boolean, Array, Object).

Impact: If: 61 nodes in 33 templates. Filter: 15 nodes in 10 templates. Any edit of an imported If or Filter destroys its logic, and new nodes cannot express n8n's common comparisons.

Suggested fix: Make the conditions control read and write the n8n filter shape the runtime already executes: combinator select, leftValue and rightValue as expression-capable inputs, and a type-grouped operator list covering the operators the Go conditions package supports. Keep reading the legacy array for old documents and never overwrite a value the control cannot parse.

Files: web/src/lib/workflow-editor/conditions.ts, web/src/lib/components/workflow-editor/property-field.svelte

Existing tickets: FEAT-vvwpjw

---
### Switch 'Routing Rules' (and other structured json fields) show '[object Object]' and save typed JSON as a string, so the Switch cannot be configured in the UI [find:ui-ndv] (critical/bug) · area: NDV / json property kind (Switch, Summarize, …) · confidence: high

json-kind properties render in a textarea bound to String(value), so a rules array shows as '[object Object]'. Every keystroke writes the raw text back as a string. switchRules() requires a list, so any edit breaks the node, and even valid JSON typed into the box is stored as a string and refused.

Evidence: property-field.svelte:203 (stringValue=String(value)) and :348-349 (textarea oninput writes a string). nodes/flow.go:89-100 ('switch rules must be a list'). Live in '[ui-ndv] repro edits': the Switch textarea showed "[object Object]". Typing one space and saving stored "rules": "[object Object] ". Filling it with a valid JSON array and saving stored it as a JSON string (rev 3). The run returned 422 'node "w" configuration is invalid: switch rules must be a list'. Same kind is used for summarize.fieldsToSummarize (nodes/transform.go:439 also requires a list), set.jsonOutput, respondToWebhook.responseBodyJSON, workflowTool.workflowInputs, vectorStore ids/metadataFilter/queryVector, telegram replyMarkup/media/results, and the WAHA config/contacts/poll/etc. fields.

n8n behavior: The Switch NDV has a Routing Rules list with a condition builder per rule, 'Rename Output' and Fallback Output. Summarize has a 'Fields to Summarize' add-row UI.

Impact: Switch: 22 nodes in 21 templates, uneditable and corrupted by a single keystroke. Summarize: 8 nodes. The Switch cannot be configured from the editor at all, even when new.

Suggested fix: Give Switch a real rules builder (a repeated group of conditions plus output name), and make Summarize a fixedCollection. For the generic json kind, pretty-print objects with JSON.stringify(value, null, 2) and parse on blur. Store the parsed value when the property expects a structure, and show a parse error instead of writing a string.

Files: web/src/lib/components/workflow-editor/property-field.svelte, nodes/flow.go, nodes/transform.go

Existing tickets: FEAT-vvwpjw, FEAT-jwhdsy

---
### Set node assignment values holding expressions show as raw {"mode":"expression",…} JSON, and rows have no expression toggle [find:ui-ndv] (high/bug) · area: NDV / Set assignments · confidence: high

Assignment rows display expression markers as raw JSON, and there is no fixed/expression toggle per row. A user who edits the text, or types a {{ }} template, stores a literal string, because KilasFlow only evaluates values carrying the explicit marker. The expression is lost silently and the node outputs the template text.

Evidence: property-field.svelte:234-240 and :507 (assignmentText/displayValue JSON.stringify, plain input). 06-set-crop.png shows greeting value '{"mode":"expres…'. Live in '[ui-ndv] repro edits': filling the value with 'Hello {{ $json.who }}!' and saving stored the literal string (rev 2). '[ui-ndv] fixed braces' run output: fixedBraces:"{{ $json.sessionId }}" (literal) vs expr:"abc". The old '[ui-ndv] core nodes v1' holds the corrupted value "{\"mode\":\"expression\",…}!".

n8n behavior: Each Set 'Fields to Set' row value has a Fixed/Expression toggle with an inline result preview. Typing '{{' switches the field to expression mode.

Impact: 149 Set nodes with expression values in 48/100 templates; Set is the most common non-sticky node (208 uses). Any edit silently breaks the mapping.

Suggested fix: Give each assignment value the same expression-capable control as top-level fields: a fixed/expression toggle that shows the template text. Also auto-switch to expression mode when the user types '{{', as n8n does.

Files: web/src/lib/components/workflow-editor/property-field.svelte, web/src/lib/workflow-editor/assignments.ts

Existing tickets: FEAT-xqqjqv

---
### HTTP query/header key-value rows show expression values as raw JSON and cannot toggle to expression [find:ui-ndv] (high/bug) · area: NDV / keyValue kind (HTTP Request, HTTP Tool, Respond to Webhook) · confidence: high

keyValue rows render an expression marker as the text {"mode":"expression","value":"{{ $json.greeting }}"}. There is no per-row fixed/expression toggle, so users must hand-edit JSON markers to keep or create a dynamic header or query value.

Evidence: property-field.svelte:476-490 (displayValue JSON.stringify, parseValue on input). 03-http-v1-panel.png: query param q shows '{"mode":"expression","…'. Live '[ui-ndv] repro edits' snapshot: textbox "Query parameters field value": "{\"mode\":\"expression\",\"value\":\"{{ $json.greeting }}\"}".

n8n behavior: Each query or header parameter value in n8n's HTTP Request has its own Fixed/Expression toggle and preview.

Impact: 28/100 templates use expressions in HTTP query, header or body parameters (21 query, 24 header, 21 body nodes). Any edit is error-prone and a typed {{ }} stays literal.

Suggested fix: Render keyValue values with the expression-capable field component (toggle plus template text). Consider storing rows as an array so duplicate names are possible.

Files: web/src/lib/components/workflow-editor/property-field.svelte

---
### A Date & Time node added from the picker fails because its default date is the n8n-style literal '={{ $json.date }}' [find:ui-ndv] (high/bug) · area: NDV defaults / Date & Time · confidence: high

dateTime.date defaults to the string '={{ $json.date }}', n8n's '=' prefix syntax, which KilasFlow does not evaluate. The node therefore fails on its first run with its own default. The field also never appears (see the visibility finding), so the user cannot even see why.

Evidence: nodes/datetime.go:87 (Default: "={{ $json.date }}"). Workflow '[ui-ndv] default dateTime' (wf_01a0af2d-da02-7710-b64e-0c5b4e9ccff0), node created from the picker with defaults, run with input {"date":"2026-01-02T03:04:05Z"}: failed, 'node "Date & Time": item 1: "={{ $json.date }}" is not a date this server can read'.

n8n behavior: The n8n Date & Time node's default input date is an expression on the incoming item, and the node runs out of the box.

Impact: Every Date & Time node created in the editor is broken out of the box.

Suggested fix: Change the default to an expression marker {mode:'expression', value:'{{ $json.date }}'}. Add a registry test that no default string starts with '=' or contains '{{' without a marker.

Files: nodes/datetime.go

Existing tickets: FEAT-q81bq4

---
### Imported Merge v3 shows a blank Mode and hides combineBy (mode 'combine' is not in the options list) [find:ui-ndv] (medium/bug) · area: NDV / Merge · confidence: high

The importer stores n8n's mode 'combine' plus combineBy, and the runtime accepts it. The Merge definition only offers append / combineByFields / combineByPosition / combineAll / chooseBranch, so the Mode select is blank and combineBy is not shown. Picking any option silently changes the node's behaviour.

Evidence: 07-11-combined.png (Merge: Mode blank, Number of Inputs empty). node-types kilasflow.merge mode options lack 'combine'. nodes/executors.go:404-405 and nodes/flow.go:422-440 handle combine plus combineBy. Stored: {"combineBy":"combineByPosition","mode":"combine"}.

n8n behavior: The n8n Merge v3 NDV shows Mode 'Combine' and then 'Combine By: Position / Matching Fields / All Possible Combinations'.

Impact: Merge nodes in 29 imported templates (merge 3, 41 uses) show an unset mode.

Suggested fix: Normalise combine+combineBy to the flat mode on import, or add 'combine' plus a combineBy options property with a visibility rule to the definition.

Files: nodes/flow.go, internal/interop/n8n/parameters.go

Existing tickets: FEAT-vvwpjw

---
### Options selects and number fields show blank instead of the declared default when no value is stored [find:ui-ndv] (medium/bug) · area: NDV / property rendering · confidence: high

PropertyField receives only the stored value. For an absent value an options select gets '' (blank) and a number input shows empty, although visibility is evaluated with withDefaults. Imported and API-created nodes look unconfigured.

Evidence: properties-panel.svelte:130 (value={values[property.key]}, no default). property-field.svelte:466. 06-set-crop.png (Set Mode and 'Input Fields to Include' blank). 04-http-v1-panel-bottom.png (Response format blank). Switch Fallback Output blank. Postgres Limit / Statement timeout / Maximum rows empty (live snapshot).

n8n behavior: n8n always shows the parameter's default value in the control.

Impact: Every imported node that relies on defaults looks misconfigured, and users may 'fix' values that are fine.

Suggested fix: Pass `values[key] ?? property.default` to the control, or show the default as a placeholder or selected option with a 'default' marker, without writing it into the document.

Files: web/src/lib/components/workflow-editor/properties-panel.svelte, web/src/lib/components/workflow-editor/property-field.svelte

---
### Resource locator and load-options selects hide the stored value when the list cannot load [find:ui-ndv] (medium/bug) · area: NDV / resource locator, loadOptions · confidence: high

In 'From list' mode the select only contains fetched options. When loading fails or no credential is attached, the stored value (e.g. model gpt-5-mini) is not among them, so the control shows 'Choose…' with value ''. cachedResultName is written but never used for display. The same applies to options with loadOptions such as the telegram operation.

Evidence: Live '[ui-ndv] ai and packs', OpenAI Chat Model: stored model {"__rl":true,"mode":"list","value":"gpt-5-mini"}. DOM: select value "", options [""], with the hint 'attach a credential to load this field's options' (20-24-combined.png). property-field.svelte:540-545 and :466-470.

n8n behavior: n8n shows the cached name of the selected resource even when the list cannot be fetched.

Impact: Imported AI model, table and chat selections look unset, which invites accidental changes.

Suggested fix: Always include the current value as an option, labelled with cachedResultName or the raw value, when it is missing from the loaded list, and mark it 'not in list'. Capitalise the reason text.

Files: web/src/lib/components/workflow-editor/property-field.svelte, web/src/lib/workflow-editor/resource-locator.ts

Existing tickets: FEAT-45tfmh

---
### No inline validation: empty required fields and missing credentials are accepted on save and only fail at run [find:ui-ndv] (medium/ux) · area: NDV / validation messages · confidence: high

A red asterisk is the only signal. Clearing a required URL or leaving a required credential unset shows no field error and no canvas warning, and the draft saves. Errors surface only as a 422 when running.

Evidence: Live '[ui-ndv] multiline': cleared the HTTP URL and saved. Rev 3 stored url "" with 'All changes saved'. The run returned 422 ['node "h" configuration is invalid: url is required', 'node "t" requires a telegramApi credential']. '[ui-ndv] repro edits' rev 2 saved an If with an empty condition. 41-required-empty-save.png shows no node warnings.

n8n behavior: n8n shows 'Parameter X is required' under the field and a red warning triangle on the node with an issues tooltip.

Impact: Users discover configuration errors late, one run at a time, with no pointer to the field.

Suggested fix: Evaluate RequiredFor and credential requirements client-side (or via a validate endpoint) as the user edits. Show a message under each field and a warning badge on the canvas node, as the issues list already does after a refused save.

Files: web/src/lib/components/workflow-editor/property-field.svelte, web/src/lib/components/workflow-editor/canvas-node.svelte, web/src/lib/workflow-editor/validation.ts

Existing tickets: FEAT-qfr9xe

---
### Node-run timestamps are all stamped at persist time, so per-node durations are always 0 [find:ui-ndv] (medium/bug) · area: Execution data / output pane metadata · confidence: high

Every nodeRun has startedAt == finishedAt, all stamped at the end of the execution. Retry waits and slow nodes are invisible, and the inspector's per-node duration line always reads 0.

Evidence: Re-ran '[ui-ndv] run panes' (retryOnFail, maxTries 3, waitBetweenTries 500). The execution ran from 08:41:09.954 to 08:41:10.962, so the waits were honoured, but node m started at 08:41:10.960 and the three 'HTTP fail' attempts show 10.96138, 10.961586 and 10.961783, each with startedAt == finishedAt. internal/repository/executions.go:603-605 fills StartedAt = time.Now() when the engine gives none. The engine's NodeRun is appended at runner.go:445-528.

n8n behavior: The n8n output pane shows each node's run time and the number of items produced.

Impact: Users cannot see which node is slow or that retries waited, and n8n's 'Execution time' per node is unavailable.

Suggested fix: Record start and finish per attempt in the runner and persist them. Show duration and attempt count in the output pane.

Files: internal/engine/runner.go, internal/repository/executions.go

Existing tickets: FEAT-a6yg3n

---
### Key-value editor: 'Add field' twice yields one row, and renaming onto an existing key overwrites it [find:ui-ndv] (low/bug) · area: NDV / keyValue kind · confidence: medium

addKeyValue writes key '' into an object, so a second click adds nothing until the first row is named. Renaming a row to an existing name silently replaces that row. Duplicate query or header names, which are valid HTTP, cannot be represented.

Evidence: property-field.svelte:242-258 (renameKey / addKeyValue on an object map), :478 (each keyed by key).

n8n behavior: n8n's header and query parameters are an ordered list that allows repeated names.

Impact: Minor friction when adding several headers or params, and lossy for n8n workflows with repeated query keys.

Suggested fix: Store rows as an ordered array of {name, value} (as n8n's parameters[] does) and convert at execution time.

Files: web/src/lib/components/workflow-editor/property-field.svelte, web/src/lib/workflow-editor/key-value.ts

---
### A Simple Memory added in the editor uses the literal key "{{ $json.sessionId }}" for every conversation [find:ui-ndv] (high/security) · area: NDV defaults / AI memory · confidence: medium

memoryBuffer.sessionKey defaults to the fixed string "{{ $json.sessionId }}" with no expression marker. createWorkflowNode writes this default into new nodes, fixed strings with braces are not evaluated, and resolveSessionKey uses the value as-is (plus '__<node>' in Connected Chat Trigger mode). Every chat hitting an editor-built agent therefore shares one memory. The panel also shows Session Key as 'fixed' {{ $json.sessionId }} while 'Connected Chat Trigger Node' is selected.

Evidence: nodes/ai.go:401 (Default: "{{ $json.sessionId }}", PropertyString) and :864-879 (resolveSessionKey). web/src/lib/workflow-editor/document.ts:33 (defaults written on add). Fixed braces are proven literal by the '[ui-ndv] fixed braces' run (fixedBraces:"{{ $json.sessionId }}"). Stored node in '[ui-ndv] ai and packs': "sessionIdType":"fromInput","sessionKey":"{{ $json.sessionId }}". The importer instead writes an expression marker (internal/interop/n8n/parameters.go:2647), and nodes/ai_test.go uses the marker, so only editor-built nodes are affected. Not run end-to-end with an LLM because the GPU is reserved for the ai-ollama dimension.

n8n behavior: In 'Connected Chat Trigger Node' mode n8n reads the key from $json.sessionId, shown read-only. 'Define below' gives an expression field.

Impact: Conversation history leaks across end users of any agent whose memory node was added in the editor, a privacy issue for embedded multi-tenant hosts.

Suggested fix: Declare the default as an expression marker {mode:'expression', value:'{{ $json.sessionId }}'} (as the WAHA pack does for chatId), or have fromInput mode read $json.sessionId itself. Hide Session Key when 'Connected Chat Trigger Node' is selected. Add a test that two runs with different sessionIds get different keys.

Files: nodes/ai.go, web/src/lib/workflow-editor/document.ts

Existing tickets: FEAT-096vs9

---
### Nodes cannot be renamed, and new nodes get duplicate names that break $('Name') and the n8n export [find:ui-ndv] (high/bug) · area: NDV header / node naming · confidence: high

The panel header is a static heading and the canvas has no rename action. New nodes are named after definition.displayName with no de-duplication, so a second HTTP Request is also called 'HTTP Request'. The server accepts duplicate names, $('Name') becomes ambiguous, and the n8n export (whose connections are keyed by name) produces a self-loop.

Evidence: web/src/lib/workflow-editor/document.ts:38 (name: definition.displayName). properties-panel.svelte:84 (static h2). No rename or dblclick handler in the components. POST /api/v1/workflows '[ui-ndv] dup names' with two 'No Operation' nodes returned 200 (wf_01a0b8db-8c39-7f3a-8062-09a803c1084f). GET …/export returns nodes ['Manual','No Operation','No Operation'] with connections {"No Operation": {"main": [[{"node": "No Operation"…}]]}}.

n8n behavior: The NDV title is editable. Names are unique ('HTTP Request1'), and renaming updates references in expressions.

Impact: Any workflow with two nodes of the same type has ambiguous expressions and exports a broken n8n workflow. Users cannot give nodes meaningful names, which is standard practice in every template.

Suggested fix: Add inline rename in the NDV header and on the canvas (F2), with a server-side uniqueness check. Auto-suffix new names (HTTP Request1). Optionally rewrite $('Old') references on rename, as n8n does.

Files: web/src/lib/workflow-editor/document.ts, web/src/lib/components/workflow-editor/properties-panel.svelte, internal/workflow/compiler.go, internal/interop/n8n/n8n.go

---
### Notice properties render their text twice [find:ui-ndv] (low/ui) · area: NDV / notice kind · confidence: high

The generic description paragraph renders for every kind, and the notice box then renders `property.description || property.label` again, so the notice text appears twice.

Evidence: property-field.svelte:325 (description) and :339-345 (notice). 20-24-combined.png: OpenAI Chat Model shows 'A chat model is configuration for an agent…' twice under 'Connect this to an AI Agent'.

n8n behavior: n8n renders a notice once as a callout.

Impact: Visual noise on every AI sub-node (5 notice properties in the catalog).

Suggested fix: Skip the description line for kind 'notice', or render the notice box only.

Files: web/src/lib/components/workflow-editor/property-field.svelte

## Acceptance criteria

- [ ] If/Filter condition builder cannot read n8n-shaped conditions: it shows them as empty and 'Add condition' over
- [ ] Switch 'Routing Rules' (and other structured json fields) show '(object Object)' and save typed JSON as a stri
- [ ] Set node assignment values holding expressions show as raw {"mode":"expression",…} JSON, and rows have no expr
- [ ] HTTP query/header key-value rows show expression values as raw JSON and cannot toggle to expression
- [ ] A Date & Time node added from the picker fails because its default date is the n8n-style literal '={{ $json.da
- [ ] Imported Merge v3 shows a blank Mode and hides combineBy (mode 'combine' is not in the options list)
- [ ] Options selects and number fields show blank instead of the declared default when no value is stored
- [ ] Resource locator and load-options selects hide the stored value when the list cannot load
- [ ] No inline validation: empty required fields and missing credentials are accepted on save and only fail at run
- [ ] Node-run timestamps are all stamped at persist time, so per-node durations are always 0
- [ ] Key-value editor: 'Add field' twice yields one row, and renaming onto an existing key overwrites it
- [ ] A Simple Memory added in the editor uses the literal key "{{ $json.sessionId }}" for every conversation
- [ ] Nodes cannot be renamed, and new nodes get duplicate names that break $('Name') and the n8n export
- [ ] Notice properties render their text twice
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)