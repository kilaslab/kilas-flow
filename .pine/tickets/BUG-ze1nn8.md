---
id: BUG-ze1nn8
title: Single-line inputs strip newlines; editing silently flattens expressions/JSON/notes
status: testing
priority: critical
labels:
    - editor
    - data-loss
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T13:55:03Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 5 finding(s) from dims: find:ui-canvas, find:ui-ndv, find:web-frontend-code.

---
### Expression inputs are single-line, so every newline in a multi-line expression is silently removed on edit [find:ui-ndv] (critical/bug) · area: NDV / expression mode · confidence: high

Expression mode always renders an <input type=text>. Browsers strip line breaks from its value, so a multi-line expression shows flattened, and the first keystroke writes the flattened text back. Fixed-mode strings on fields without typeOptions.rows go through the same single-line input.

Evidence: property-field.svelte:328 (expression mode uses <input>) and :663 (default string input). Live in '[ui-ndv] multiline' (work/ui-ndv/wf7.id): HTTP body expression "{\n  \"a\": {{ 1 + 1 }},\n  \"b\": \"x\"\n}" and Telegram text expression "Hello {{ $json.body.method }}\nLine two\nLine three". After opening the panel, typing one character and saving, the stored values (rev 2) were "{  \"a\": {{ 1 + 1 }},  \"b\": \"x\"} " and "Hello {{ $json.body.method }}Line twoLine three!" (40-multiline-telegram.png).

n8n behavior: n8n's inline expression editor is multi-line (CodeMirror) and keeps newlines, and it can be expanded to a full-size editor.

Impact: 254 multi-line '=' expression values across 59/100 templates: httpRequest jsonBody 48, Set assignment values 38, agent systemMessage 24, agent text 23, chainLlm text 10. Message formatting, prompts and JSON bodies are corrupted with no warning.

Suggested fix: Use an auto-growing textarea (or a CodeMirror-based expression editor) for expression mode and for all string fields. Never bind multi-line content to <input>. Add a unit or e2e test that round-trips a value containing \n through edit and save.

Files: web/src/lib/components/workflow-editor/property-field.svelte

Existing tickets: FEAT-pn3dtq

---
### Long-text fields, including the Go Code node source, are single-line inputs [find:ui-ndv] (high/ux) · area: NDV / text editors · confidence: high

Code node `code` (the body of func run), AI Agent prompt and systemMessage, Basic LLM Chain text and HTTP Request body are string properties with no typeOptions.rows, so they render as one-line <input>s. Multi-line content cannot be read, and editing strips its newlines (see the multi-line expression finding). For Go source this breaks the code. Only foreignCode js/python, mysql/postgres query and telegram text get a textarea.

Evidence: node-types-live.json: kilasflow.code code string typeOptions None ('The body of func run(items []Item) ([]Item, error)…'); agent prompt/systemMessage None; chainLlm text None; httpRequest body None. property-field.svelte:658-663. Multi-line string properties found: foreignCode jsCode/pythonCode, mysql/postgres query, telegram text only.

n8n behavior: The Code node has a full code editor with highlighting and completion. Prompt and System Message are multi-line fields.

Impact: The Code node is effectively uneditable for real programs, and prompts and system messages (24 multi-line agent system messages in the templates) are unreadable.

Suggested fix: Add typeOptions {rows, editor:'code'|'sql'|'json', language} to these definitions and render a code editor (CodeMirror) for code and a textarea for prompts. Default any string longer than one line to a textarea.

Files: nodes/code.go, nodes/ai.go, nodes/core.go, web/src/lib/components/workflow-editor/property-field.svelte

Existing tickets: FEAT-w3s12y

---
### Single-line inputs strip newlines from multi-line values, so any edit silently flattens expressions, JSON bodies and sticky-note markdown [find:web-frontend-code] (critical/bug) · area: editor inspector (property-field) · confidence: high

Expression mode always renders as an <input> (property-field.svelte:328). String properties without typeOptions.rows also render as <input> (:662-663). The browser removes LF/CR from an <input>'s value, and the next keystroke writes that flattened value back into the draft, where Save persists it. Every multi-line expression, HTTP body, prompt and sticky-note content imported from n8n is destroyed the first time someone edits it.

Evidence: Verified in the browser. POST /api/v1/workflows created wf_01a0b8e2-fff7-7a02-98c1-64f1df146257 with an HTTP node (kilasflow.httpRequest v1) whose body is {mode:'expression', value:'{\n  "a": "{{ $json.x }}",\n  "b": 2\n}'} and a sticky note with content '## Title\n\nLine two\n- bullet'. Selecting HTTP gives #property-body.value = '{  "a": "{{ $json.x }}",  "b": 2}' with no newline. Typing one character and clicking Save produced revision 2, and GET returns the body with every newline removed. Sticky: #property-content.value = '## TitleLine two- bullet'. Code: web/src/lib/components/workflow-editor/property-field.svelte:328 (expression <input>), :662-663 (string <input>). The node definitions give no rows for httpRequest.body or stickyNote.content.

n8n behavior: n8n edits expressions in a multi-line expression editor, and string parameters such as JSON body, prompt, message text and sticky content keep their newlines.

Impact: 59 of the 100 templates carry 254 multi-line expression parameters (AI prompts, JSON bodies, chat messages), and 91 of 100 carry sticky notes. Any edit corrupts them with no warning.

Suggested fix: Render expression mode and any string value that contains a newline as an auto-growing <textarea>, or better a code editor. Add typeOptions.rows to body, content and prompt definitions. Add a component test that round-trips '\n'.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/property-field.svelte

---
### Sticky notes: editing Content strips every line break, and notes render as 68px icon tiles instead of notes [find:ui-canvas] (high/bug) · area: sticky notes (canvas + inspector) · confidence: high

The sticky note's content is a plain string property and is rendered as a single-line <input>, so browsers drop the newlines. Any edit therefore collapses multi-line markdown into one line. On the canvas a note is a 68×68 icon tile labelled 'Note'. Width, height and colour are ignored, there are no resize handles, and the markdown is never displayed.

Evidence: Repro (a5.js) on '[ui-canvas] Sticky test' wf_01a0b8d0-adfa-756e-aaa8-740794f23244, content "## Section title\nLine two\n\n- bullet". The inspector input shows '## Section titleLine two- bullet'. Typing one character and saving stores "## Section titleLine two- bullet!" (server GET). The note's bounding box is 68×68 with innerText 'Note', and there are 0 .svelte-flow__resize-control. Code: nodes/annotation.go:40 content is PropertyString with no typeOptions.rows; web/src/lib/components/workflow-editor/property-field.svelte:663 renders <input type=text>. Screenshots: 11-sticky-selected.png, 04-zoom-placeholders.png (10 sticky icons scattered over template 2465).

n8n behavior: Sticky notes are coloured, resizable rectangles behind nodes that render markdown. Double-clicking edits them in place.

Impact: 640 sticky notes in 91/100 templates, 428 of them multi-line. These notes are the documentation of imported templates. They are unreadable on the canvas, and any edit destroys their formatting.

Suggested fix: Add a dedicated sticky canvas node type: NodeResizer, rendered markdown, colour palette, z-index behind nodes, and in-place textarea editing. Persist width and height from the resize. Meanwhile give content typeOptions.rows so the inspector uses a textarea.

Files: /Users/izzadev/projects/k-flow/nodes/annotation.go, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/property-field.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/canvas-node.svelte

Existing tickets: FEAT-t5q318

---
### Set node rows cannot hold expressions: imported expression values show as raw JSON and are corrupted on edit, and number rows reject negative input [find:web-frontend-code] (high/bug) · area: editor inspector (assignmentCollection) · confidence: high

Assignment rows render their value through displayValue(), which is JSON.stringify for anything that is not a string, and write back the plain text. There is no per-row fixed/expression toggle. The importer stores n8n '={{...}}' assignment values as {mode:'expression', value:'{{ ... }}'}, so a row shows '{"mode":"expression",...}' and any edit turns the expression into a literal string. Number rows run Number() on every keystroke, so typing '-' gives NaN, which is cloned to null and clears the field.

Evidence: Verified with wf_01a0b8f2-8020-7226-89cc-8d533c2cf9dc: a Set v1 node with row a1 = {mode:expression, value:'{{ $json.route }}'} shows the value input as '{"mode":"expression","value":"{{ $json.route }}"}'. In number row a2, dispatching input '-' leaves the field ''. Every Set row in the template 3135 import has this expression-object shape. Code: web/src/lib/components/workflow-editor/property-field.svelte:216-240, 491-517, 310-313. keyValue (:478-481) and conditions (:650) rows also show expression objects as raw JSON.

n8n behavior: Every assignment value in n8n's Set node accepts expressions (drag and drop or '{{'), and number fields accept free text until validation.

Impact: Set is one of the most used nodes: 193 Set nodes across the 100 templates, all using expression values. Once the version mismatch is fixed, users still cannot author or safely edit Set expressions.

Suggested fix: Render each row's value with the same expression-capable field component (detect {mode:'expression'}) and add a per-row expr/fixed toggle. For numbers, keep the raw text while typing and coerce on blur. Apply the same fix to keyValue and conditions rows.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/property-field.svelte, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/assignments.ts

## Acceptance criteria

- [ ] Expression inputs are single-line, so every newline in a multi-line expression is silently removed on edit
- [ ] Long-text fields, including the Go Code node source, are single-line inputs
- [ ] Single-line inputs strip newlines from multi-line values, so any edit silently flattens expressions, JSON bodi
- [ ] Sticky notes: editing Content strips every line break, and notes render as 68px icon tiles instead of notes
- [ ] Set node rows cannot hold expressions: imported expression values show as raw JSON and are corrupted on edit, 
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress (WebFormsOps2 2026-09-19)

- Commit 87499d8: `needsMultiline(value, rows)` helper in parameter.ts — multiline is a different element, not an attribute; any fixed string carrying `\n` upgrades to textarea even when the definition has no rows (web-layer fallback, no nodes/*.go edits needed). property-field top-level branch now calls the helper; negative-number typing keeps raw text and coerces on blur; keyValue + assignment rows with multiline strings render textareas.
- Scoped tests: vitest parameter.test.ts + assignments + key-value + conditions — 4 files, 32 tests pass.
- Remaining: sticky canvas tile (canvas-node.svelte, not mine — hub WebEditorCore/Main), CodeMirror pending (deferred: textarea preserves newlines, the data-loss defect; full code editor is a larger FEAT), expression-mode textarea already landed by prior agent.
- Risk: number-as-text intermediate (e.g. "-" typed) flows into draft as string until blur coerces; server validation treats like before.
