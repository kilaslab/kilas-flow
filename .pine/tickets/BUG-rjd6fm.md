---
id: BUG-rjd6fm
title: 'NDV inputs: condition builder, routing rules, key-value rows, defaults, validation, timestamps'
status: done
priority: high
labels:
    - editor
    - ndv
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:04:02Z"
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
## Work (WebFormsOps 2026-09-19)
- status: doing → testing after commit.
- Condition builder reads/writes n8n filter shape (combinator + typed operators + expression operands), keeps legacy flat rows readable, never overwrites unparseable values with blank rows (conditions.ts + property-field builder).
- Switch/structured json: pretty-print + parse-on-blur with inline error instead of String()/string-write (no more [object Object] corruption).
- Set assignments + HTTP keyValue rows: per-row fixed/expr toggle, template text display, auto-expr on {{ typing; number rows no longer Number()-coerce on keystroke (typing preserved, '-' no longer clears field).
- KeyValue add: suffixed fieldN keys (double-add works); rename onto existing key overwrites still (object shape — array migration needs executor change, out of slice; documented).
- Defaults shown (values[key] ?? default), load-failure stored value kept as option, resource-locator same (needs panel edit below).
- Tests: conditions.test.ts rewritten for rich shape (15 passed); vitest conditions+credentials+visibility+assignments+key-value 52 passed; svelte-check 0 errors.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (3):
  - `d661a652` — chore(pine): align editor-core + ops-form ticket states (testing/doing)
  - `40cc8db1` — BUG-rjd6fm: rich condition builder, expr rows, json parse-on-blur — NDV inputs
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
 434 files changed, 68796 insertions(+), 4725 deletions(-)
```
